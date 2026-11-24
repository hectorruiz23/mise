package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

const (
	integrationNow = 1_700_000_000
	headCommit     = "1111111111111111111111111111111111111111"
	mergeCommit    = "2222222222222222222222222222222222222222"
	olderCommit    = "3333333333333333333333333333333333333333"
)

type integrationClock struct{ now int64 }

func (c *integrationClock) Now() int64 { return c.now }

type passingChecks struct{ name string }

func (c passingChecks) Checks(context.Context, string, string) ([]domain.Check, error) {
	return []domain.Check{{Name: c.name, Status: "completed", Conclusion: "success"}}, nil
}

type githubStub struct {
	mu           sync.Mutex
	files        []string
	mergeSHA     string
	mergeStatus  int
	mergeMessage string
	fileReads    int
	merges       int
	baseRepo     string
	base         string
}

func (s *githubStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files"):
			s.fileReads++
			files := make([]map[string]string, 0, len(s.files))
			for _, name := range s.files {
				files = append(files, map[string]string{"filename": name})
			}
			writeJSON(w, http.StatusOK, files)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/"):
			writeJSON(w, http.StatusOK, map[string]any{
				"state": "open",
				"base": map[string]any{
					"ref":  s.base,
					"repo": map[string]string{"full_name": s.baseRepo},
				},
				"head": map[string]string{"sha": headCommit, "ref": "feature"},
			})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/merge"):
			s.merges++
			if s.mergeStatus != 0 {
				writeJSON(w, s.mergeStatus, map[string]string{"message": s.mergeMessage})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"sha": s.mergeSHA, "merged": true})
		default:
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "not found"})
		}
	}
}

func (s *githubStub) counts() (fileReads, merges int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileReads, s.merges
}

type revisionStub struct {
	mu       sync.Mutex
	revision string
	hits     int
}

func (s *revisionStub) set(revision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision = revision
}

func (s *revisionStub) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits++
		if r.URL.Path != "/api/build" {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revision": s.revision, "tree_state": "clean"})
	}
}

type integrationFixture struct {
	ctx      context.Context
	database *sql.DB
	repo     *taskssqlite.Repo
	service  *app.Service
	worker   *integrationWorker
	clock    *integrationClock
	github   *githubStub
	env      *revisionStub
	envURL   string
	owner    domain.Actor
	agent    domain.Actor
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "mise.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO users (id,email,name,kind,token_hash,created_at) VALUES
		(1,'owner@example.com','Owner','human',?,?),(2,'agent@example.com','Agent','agent',?,?)`,
		credential.Hash("owner-token"), integrationNow,
		credential.Hash("agent-token"), integrationNow); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	clock := &integrationClock{now: integrationNow}
	repo := taskssqlite.New(database)
	service := app.NewWithChecks(repo, clock, passingChecks{name: "api"})
	owner := domain.Actor{ID: 1, Kind: domain.Human, Name: "Owner"}
	agent := domain.Actor{ID: 2, Kind: domain.Agent, Name: "Agent"}

	checks := []string{"api"}
	autoMerge := true
	if err := service.UpdateProjectSettings(context.Background(), owner, "MI", app.ProjectSettings{
		RequiredChecks: &checks,
		AutoMerge:      &autoMerge,
	}); err != nil {
		t.Fatalf("configure project: %v", err)
	}

	github := &githubStub{mergeSHA: mergeCommit, baseRepo: "example/project", base: "main"}
	githubServer := httptest.NewServer(github.handler())
	t.Cleanup(githubServer.Close)

	environment := &revisionStub{revision: olderCommit}
	environmentServer := httptest.NewServer(environment.handler())
	t.Cleanup(environmentServer.Close)

	client := githubinfo.New(githubinfo.Options{
		BaseURL:           githubServer.URL,
		Repository:        "example/project",
		VerificationToken: "verification-token",
		MergeToken:        "merge-token",
		Clock:             clock,
		Budget:            1000,
	})
	worker := &integrationWorker{
		store:  repo,
		github: client,
		clock:  clock,
		client: &http.Client{Timeout: time.Second},
		target: githubinfo.Target{Repository: "example/project", Base: "main"},
	}

	return &integrationFixture{
		ctx: context.Background(), database: database, repo: repo, service: service,
		worker: worker, clock: clock, github: github, env: environment, envURL: environmentServer.URL,
		owner: owner, agent: agent,
	}
}

func (f *integrationFixture) newCard(t *testing.T, title string) domain.Task {
	t.Helper()
	task, _, err := f.service.Create(f.ctx, app.CreateParams{
		ProjectKey: "MI", Title: title, Actor: f.owner, Priority: domain.Normal,
	})
	if err != nil {
		t.Fatalf("create card %q: %v", title, err)
	}
	return task
}

func (f *integrationFixture) deliverAsAgent(t *testing.T, key, prURL string) {
	t.Helper()
	task, err := f.service.Get(f.ctx, f.owner, key)
	if err != nil {
		t.Fatalf("read card %s: %v", key, err)
	}
	if task.State == domain.Backlog {
		if _, err := f.service.Move(f.ctx, key, domain.Ready, "", f.owner, task.Version, true); err != nil {
			t.Fatalf("move %s to ready: %v", key, err)
		}
	}
	leased, lease, err := f.service.Acquire(f.ctx, key, f.agent)
	if err != nil {
		t.Fatalf("lease %s: %v", key, err)
	}
	head := headCommit
	if _, _, err := f.service.DeliverRevision(f.ctx, key, f.agent, &lease.ID, "Work done",
		[]string{"https://example.test/build"}, nil, &prURL, &head); err != nil {
		t.Fatalf("deliver %s: %v", key, err)
	}
	if leased.State != domain.InProgress {
		t.Fatalf("lease left %s in %q", key, leased.State)
	}
}

func (f *integrationFixture) deliverAsOwner(t *testing.T, key, prURL string) {
	t.Helper()
	task, err := f.service.Get(f.ctx, f.owner, key)
	if err != nil {
		t.Fatalf("read card %s: %v", key, err)
	}
	if task.State == domain.Backlog {
		if task, err = f.service.Move(f.ctx, key, domain.Ready, "", f.owner, task.Version, true); err != nil {
			t.Fatalf("move %s to ready: %v", key, err)
		}
	}
	if task, err = f.service.Move(f.ctx, key, domain.InProgress, "", f.owner, task.Version, true); err != nil {
		t.Fatalf("move %s to in progress: %v", key, err)
	}
	head := headCommit
	if _, _, err := f.service.DeliverRevision(f.ctx, key, f.owner, nil, "Work done by hand",
		[]string{"https://example.test/build"}, nil, &prURL, &head); err != nil {
		t.Fatalf("deliver %s as the owner: %v", key, err)
	}
}

func (f *integrationFixture) state(t *testing.T, key string) domain.State {
	t.Helper()
	task, err := f.service.Get(f.ctx, f.owner, key)
	if err != nil {
		t.Fatalf("read card %s: %v", key, err)
	}
	return task.State
}

func (f *integrationFixture) mergeAttempts(t *testing.T, key string) []struct {
	outcome string
	reason  string
} {
	t.Helper()
	rows, err := f.database.Query(
		`SELECT outcome, reason FROM merge_attempts WHERE task_key = ? ORDER BY attempted_at`, key)
	if err != nil {
		t.Fatalf("read merge attempts: %v", err)
	}
	defer rows.Close()
	attempts := make([]struct {
		outcome string
		reason  string
	}, 0)
	for rows.Next() {
		var item struct {
			outcome string
			reason  string
		}
		if err := rows.Scan(&item.outcome, &item.reason); err != nil {
			t.Fatalf("scan merge attempt: %v", err)
		}
		attempts = append(attempts, item)
	}
	return attempts
}

func (f *integrationFixture) commentBodies(t *testing.T, key string) []string {
	t.Helper()
	rows, err := f.database.Query(`SELECT body FROM comments WHERE task_key = ? ORDER BY id`, key)
	if err != nil {
		t.Fatalf("read comments: %v", err)
	}
	defer rows.Close()
	bodies := make([]string, 0)
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan comment: %v", err)
		}
		bodies = append(bodies, body)
	}
	return bodies
}

func (f *integrationFixture) poll(t *testing.T) {
	t.Helper()
	if err := f.worker.poll(f.ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestAutoMergeMergesAGreenPullRequest(t *testing.T) {
	f := newIntegrationFixture(t)
	task := f.newCard(t, "Request a project rename")
	f.deliverAsAgent(t, task.Key, "https://github.com/example/project/pull/7")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Done {
		t.Fatalf("state = %q, want done", state)
	}
	if _, merges := f.github.counts(); merges != 1 {
		t.Fatalf("merge calls = %d, want 1", merges)
	}
	attempts := f.mergeAttempts(t, task.Key)
	if len(attempts) != 1 || attempts[0].outcome != app.MergeOutcomeMerged {
		t.Fatalf("merge attempts = %+v, want one merged attempt", attempts)
	}
	if !strings.Contains(attempts[0].reason, mergeCommit) {
		t.Fatalf("reason = %q, want the merge commit", attempts[0].reason)
	}
	var reason string
	if err := f.database.QueryRow(`SELECT reason FROM task_movements
		WHERE task_key = ? AND from_state = 'review' AND to_state = 'done'`, task.Key).Scan(&reason); err != nil {
		t.Fatalf("read closing movement: %v", err)
	}
	if !strings.Contains(reason, "https://github.com/example/project/pull/7") {
		t.Fatalf("movement reason = %q, want the pull request", reason)
	}
}

func TestAutoMergeMergesInTheRepositoryTheBoardLivesIn(t *testing.T) {
	f := newIntegrationFixture(t)
	f.worker.target = githubinfo.Target{Repository: githubinfo.DefaultRepository, Base: "main"}
	f.github.baseRepo = githubinfo.DefaultRepository
	task := f.newCard(t, "Ship the board's own change")
	f.deliverAsAgent(t, task.Key, "https://github.com/"+githubinfo.DefaultRepository+"/pull/77")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Done {
		t.Fatalf("state = %q, want done", state)
	}
	if _, merges := f.github.counts(); merges != 1 {
		t.Fatalf("merge calls = %d, want 1", merges)
	}
}

func TestAutoMergeRefusesWorkflowAndMigrations(t *testing.T) {
	f := newIntegrationFixture(t)
	f.github.files = []string{"README.md", ".github/workflows/image.yml", "api/migrations/0010_extra.sql"}
	task := f.newCard(t, "Touch the pipeline")
	f.deliverAsAgent(t, task.Key, "https://github.com/example/project/pull/8")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Review {
		t.Fatalf("state = %q, want review", state)
	}
	if _, merges := f.github.counts(); merges != 0 {
		t.Fatalf("merge calls = %d, want none", merges)
	}
	attempts := f.mergeAttempts(t, task.Key)
	if len(attempts) != 1 || attempts[0].outcome != app.MergeOutcomeRefused {
		t.Fatalf("merge attempts = %+v, want one refusal", attempts)
	}
	bodies := f.commentBodies(t, task.Key)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "api/migrations/0010_extra.sql") {
		t.Fatalf("comments = %+v, want the offending path", bodies)
	}

	f.poll(t)
	if reads, merges := f.github.counts(); reads != 1 || merges != 0 {
		t.Fatalf("second poll read files %d and merged %d times, want 1 and 0", reads, merges)
	}
	if bodies := f.commentBodies(t, task.Key); len(bodies) != 1 {
		t.Fatalf("comments after the second poll = %d, want 1", len(bodies))
	}
}

func TestMergeFailureLeavesTheCardInReview(t *testing.T) {
	f := newIntegrationFixture(t)
	f.github.mergeStatus = http.StatusMethodNotAllowed
	f.github.mergeMessage = "Pull Request is not mergeable"
	task := f.newCard(t, "Break the merge")
	f.deliverAsAgent(t, task.Key, "https://github.com/example/project/pull/9")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Review {
		t.Fatalf("state = %q, want review", state)
	}
	attempts := f.mergeAttempts(t, task.Key)
	if len(attempts) != 1 || attempts[0].outcome != app.MergeOutcomeFailed {
		t.Fatalf("merge attempts = %+v, want one failure", attempts)
	}
	bodies := f.commentBodies(t, task.Key)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "Pull Request is not mergeable") {
		t.Fatalf("comments = %+v, want the GitHub answer", bodies)
	}

	f.poll(t)
	if _, merges := f.github.counts(); merges != 1 {
		t.Fatalf("merge calls after the second poll = %d, want 1", merges)
	}
}

func TestOnlyDispatchedCardsCloseThemselves(t *testing.T) {
	f := newIntegrationFixture(t)
	task := f.newCard(t, "Delivered by hand")
	f.deliverAsOwner(t, task.Key, "https://github.com/example/project/pull/10")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Review {
		t.Fatalf("state = %q, want review", state)
	}
	if reads, merges := f.github.counts(); reads != 0 || merges != 0 {
		t.Fatalf("GitHub reads = %d and merges = %d, want none", reads, merges)
	}
	if attempts := f.mergeAttempts(t, task.Key); len(attempts) != 0 {
		t.Fatalf("merge attempts = %+v, want none", attempts)
	}
}

func TestAutoMergeRefusesAnotherBaseBranch(t *testing.T) {
	f := newIntegrationFixture(t)
	f.github.base = "develop"
	task := f.newCard(t, "Ship against the wrong branch")
	f.deliverAsAgent(t, task.Key, "https://github.com/example/project/pull/31")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Review {
		t.Fatalf("state = %q, want review", state)
	}
	if reads, merges := f.github.counts(); merges != 0 {
		t.Fatalf("GitHub reads = %d and merges = %d, want no merge", reads, merges)
	}
	attempts := f.mergeAttempts(t, task.Key)
	if len(attempts) != 1 || attempts[0].outcome != app.MergeOutcomeRefused {
		t.Fatalf("merge attempts = %+v, want one refusal", attempts)
	}
	if !strings.Contains(attempts[0].reason, "develop") || !strings.Contains(attempts[0].reason, "main") {
		t.Fatalf("reason = %q, want both branches", attempts[0].reason)
	}
	bodies := f.commentBodies(t, task.Key)
	if len(bodies) != 1 || !strings.Contains(bodies[0], "develop") {
		t.Fatalf("comments = %+v, want the refused base", bodies)
	}
	f.poll(t)
	if _, merges := f.github.counts(); merges != 0 {
		t.Fatalf("a later poll merged %d times, want none", merges)
	}
}

func TestAutoMergeRefusesAnotherRepository(t *testing.T) {
	f := newIntegrationFixture(t)
	f.github.baseRepo = "someone/fork"
	task := f.newCard(t, "Ship from a fork")
	f.deliverAsAgent(t, task.Key, "https://github.com/example/project/pull/32")

	f.poll(t)

	if state := f.state(t, task.Key); state != domain.Review {
		t.Fatalf("state = %q, want review", state)
	}
	if _, merges := f.github.counts(); merges != 0 {
		t.Fatalf("merge calls = %d, want none", merges)
	}
	attempts := f.mergeAttempts(t, task.Key)
	if len(attempts) != 1 || attempts[0].outcome != app.MergeOutcomeRefused {
		t.Fatalf("merge attempts = %+v, want one refusal", attempts)
	}
	if !strings.Contains(attempts[0].reason, "someone/fork") {
		t.Fatalf("reason = %q, want the unauthorized repository", attempts[0].reason)
	}
}

func TestAutoMergeWithoutAnAuthorizedDestinationMergesNothing(t *testing.T) {
	f := newIntegrationFixture(t)
	f.worker.target = githubinfo.Target{}
	task := f.newCard(t, "No destination configured")
	f.deliverAsAgent(t, task.Key, "https://github.com/example/project/pull/33")

	if err := f.worker.poll(f.ctx); err == nil {
		t.Fatal("a worker without an authorized destination reported success")
	}

	if state := f.state(t, task.Key); state != domain.Review {
		t.Fatalf("state = %q, want review", state)
	}
	if _, merges := f.github.counts(); merges != 0 {
		t.Fatalf("merge calls = %d, want none", merges)
	}
	if attempts := f.mergeAttempts(t, task.Key); len(attempts) != 0 {
		t.Fatalf("merge attempts = %+v, want none kept", attempts)
	}
}

func TestDeploymentGateWaitsForTheRevision(t *testing.T) {
	f := newIntegrationFixture(t)
	environment, err := f.service.AddEnvironment(f.ctx, f.owner, "MI", "staging", f.envURL)
	if err != nil {
		t.Fatalf("add environment: %v", err)
	}
	if environment.Name != "staging" {
		t.Fatalf("environment = %+v", environment)
	}

	first := f.newCard(t, "Serve the panel")
	second := f.newCard(t, "Publish the panel")
	policy := app.PlanPolicy{Repository: "example/project", Branch: "main", WorkflowID: 42}
	graph, err := f.repo.ApproveGraph(f.ctx, f.owner, []string{first.Key, second.Key},
		[]domain.PlanEdge{{From: first.Key, To: second.Key, Kind: domain.EdgeDeployment}}, policy, app.PlanReview{Scenario: "make scenario"}, integrationNow)
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	f.deliverAsAgent(t, first.Key, "https://github.com/example/project/pull/11")

	f.poll(t)

	if state := f.state(t, first.Key); state != domain.Review {
		t.Fatalf("merged card = %q, want review while the environment serves an older revision", state)
	}
	if state := f.state(t, second.Key); state != domain.Backlog {
		t.Fatalf("dependent = %q, want backlog", state)
	}
	waiting, err := f.repo.Graph(f.ctx, graph.ID)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	for _, node := range waiting.Nodes {
		if node.Key == second.Key && len(node.WaitingOn) != 1 {
			t.Fatalf("waiting_on = %+v, want the prerequisite", node.WaitingOn)
		}
	}

	f.env.set(mergeCommit)
	f.poll(t)

	if state := f.state(t, first.Key); state != domain.Done {
		t.Fatalf("served card = %q, want done", state)
	}
	if state := f.state(t, second.Key); state != domain.Ready {
		t.Fatalf("dependent = %q, want ready", state)
	}
	var revision string
	if err := f.database.QueryRow(`SELECT revision FROM plan_deployments
		WHERE plan_id = ? AND task_key = ? AND environment = 'staging'`,
		graph.ID, first.Key).Scan(&revision); err != nil {
		t.Fatalf("read observed deployment: %v", err)
	}
	if revision != mergeCommit {
		t.Fatalf("observed revision = %q, want %q", revision, mergeCommit)
	}

	f.poll(t)
	closed := f.state(t, first.Key)
	if closed != domain.Done {
		t.Fatalf("card reopened as %q", closed)
	}
	var moves int
	if err := f.database.QueryRow(`SELECT count(*) FROM task_movements
		WHERE task_key = ? AND to_state = 'done'`, first.Key).Scan(&moves); err != nil {
		t.Fatalf("count closings: %v", err)
	}
	if moves != 1 {
		t.Fatalf("closing movements = %d, want 1", moves)
	}
}
