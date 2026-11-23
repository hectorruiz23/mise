package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mise/api/internal/githubinfo"
	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	"mise/api/internal/platform/httpserver"
	taskshttp "mise/api/internal/tasks/adapters/http"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
)

func TestVerifiedDependencyFlow(t *testing.T) {
	ctx := t.Context()
	database, err := db.Open(filepath.Join(t.TempDir(), "plans.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	const ownerToken, agentToken = "owner-token", "agent-token"
	_, err = database.Exec(`INSERT INTO users (id,email,name,kind,token_hash,created_at) VALUES
		(1,'owner@example.com','Owner','human',?,1),(2,'agent@example.com','Agent','agent',?,1)`,
		credential.Hash(ownerToken), credential.Hash(agentToken))
	if err != nil {
		t.Fatal(err)
	}

	clock := routeClock{}
	repo := taskssqlite.New(database)
	tasks := app.New(repo, clock)
	usersService := usersapp.New(userssqlite.New(database), clock)
	owner := domain.Actor{ID: 1, Kind: domain.Human, Name: "Owner"}
	agent := domain.Actor{ID: 2, Kind: domain.Agent, Name: "Agent"}
	for _, title := range []string{"Create a project", "Rename a project"} {
		if _, _, err := tasks.Create(ctx, app.CreateParams{ProjectKey: "MI", Title: title, Actor: owner}); err != nil {
			t.Fatal(err)
		}
	}
	const origin = "https://mise.example"
	server, err := httpserver.New(httpserver.Options{DB: database, PublicOrigin: origin,
		Authenticator: usershttp.NewAuthenticator(usersService)})
	if err != nil {
		t.Fatal(err)
	}
	policy := app.PlanPolicy{Repository: "example/project", Branch: "main", WorkflowID: 15}
	plans := taskshttp.NewDependencyPlans(repo, policy, clock)
	server.HandleFunc("POST /api/dependency-plans", false, plans.Approve)
	server.HandleFunc("GET /api/dependency-plans/{firstTask}", false, plans.Get)
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		response := call(method, path, body, ownerToken)
		if response.Code < 200 || response.Code >= 300 {
			t.Fatalf("%s %s returned %d: %s", method, path, response.Code, response.Body)
		}
		return response
	}
	approval := `{"prerequisite_key":"MI-1","dependent_key":"MI-2"}`
	if code := call("POST", "/api/dependency-plans", approval, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("approval without a credential returned %d", code)
	}
	if code := call("GET", "/api/dependency-plans/MI-1", "", "").Code; code != http.StatusUnauthorized {
		t.Fatalf("plan without a credential returned %d", code)
	}
	if code := call("POST", "/api/dependency-plans", approval, agentToken).Code; code != http.StatusForbidden {
		t.Fatalf("an agent approved a plan: %d", code)
	}
	unconfigured, err := httpserver.New(httpserver.Options{DB: database, PublicOrigin: origin,
		Authenticator: usershttp.NewAuthenticator(usersService)})
	if err != nil {
		t.Fatal(err)
	}
	unconfigured.HandleFunc("POST /api/dependency-plans", false,
		taskshttp.NewDependencyPlans(repo, app.PlanPolicy{}, clock).Approve)
	refused := httptest.NewRequest("POST", "/api/dependency-plans", strings.NewReader(approval))
	refused.Header.Set("Origin", origin)
	refused.Header.Set("Content-Type", "application/json")
	refused.Header.Set("Authorization", "Bearer "+ownerToken)
	recorder := httptest.NewRecorder()
	unconfigured.Handler().ServeHTTP(recorder, refused)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("unconfigured approval returned %d: %s", recorder.Code, recorder.Body)
	}
	request("POST", "/api/dependency-plans", approval)
	_, lease, err := tasks.Acquire(ctx, "MI-1", agent)
	if err != nil {
		t.Fatal(err)
	}
	head, merge := strings.Repeat("a", 40), strings.Repeat("b", 40)
	label, pull := "head_sha", "https://github.com/example/project/pull/7"
	items := []domain.EvidenceItem{{Label: &label, Raw: head}}
	if _, _, err := tasks.DeliverItems(ctx, "MI-1", agent, &lease.ID, "Create endpoint", items, &pull); err != nil {
		t.Fatal(err)
	}

	var passing atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/project/pulls/7":
			fmt.Fprintf(w, `{"merged":true,"merged_at":"2024-01-01T00:00:00Z","head":{"sha":%q},
				"base":{"ref":"main","repo":{"full_name":"example/project"}},"merge_commit_sha":%q}`, head, merge)
		case "/repos/example/project/actions/workflows/15/runs":
			conclusion := "failure"
			if passing.Load() {
				conclusion = "success"
			}
			fmt.Fprintf(w, `{"total_count":1,"workflow_runs":[{"id":99,"workflow_id":15,
				"head_sha":%q,"repository":{"full_name":"example/project"},"event":"push","head_branch":"main",
				"status":"completed","conclusion":%q,"run_attempt":2}]}`, merge, conclusion)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	worker := planWorker{plans: repo, policy: policy, clock: clock,
		github: githubinfo.New(githubinfo.Options{BaseURL: upstream.URL, Clock: clock})}
	if err := worker.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Acquire(ctx, "MI-2", agent); err == nil {
		t.Fatal("failed CI released the dependent task")
	}
	passing.Store(true)
	for range 2 {
		if err := worker.poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	response := request("GET", "/api/dependency-plans/MI-1", "")
	var plan struct {
		State string `json:"state"`
		Proof struct {
			MergeSHA string `json:"merge_sha"`
			RunID    int64  `json:"run_id"`
		} `json:"proof"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.State != "released" || plan.Proof.RunID != 99 || plan.Proof.MergeSHA != merge {
		t.Fatalf("unexpected plan: %s", response.Body)
	}
	movements, err := tasks.Movements(ctx, owner, "MI-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(movements) != 2 || movements[0].ToState == movements[1].ToState {
		t.Fatalf("expected creation and a single release: %+v", movements)
	}
	if _, _, err := tasks.Acquire(ctx, "MI-2", agent); err != nil {
		t.Fatalf("claim after verified integration: %v", err)
	}
}
