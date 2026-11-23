package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func TestCancelledVisualPlanKeepsPullRequestOpen(t *testing.T) {
	f := newIntegrationFixture(t)
	plain := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{plain.Key, reveal.Key}, nil, app.PlanReview{
		Scenario: "make scenario", VisualReview: true, RevealTask: reveal.Key,
		VisibleChange: stringPointer("Show the panel"),
	})
	f.deliverAsAgent(t, reveal.Key, "https://github.com/example/project/pull/71")
	if _, err := f.repo.CancelGraph(f.ctx, f.owner, graph.ID, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	f.deliverAsAgent(t, plain.Key, "https://www.github.com/Example/Project/pull/71/files")
	f.poll(t)

	if reads, merges := f.github.counts(); reads != 0 || merges != 0 {
		t.Fatalf("GitHub file reads and merge calls = %d, %d, want zero", reads, merges)
	}
	if state := f.state(t, reveal.Key); state != domain.Review {
		t.Fatalf("visual card state = %s, want review", state)
	}
}

func TestAutoMergeRequiresTheHeadThatPassedChecks(t *testing.T) {
	f := newIntegrationFixture(t)
	heads := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/files"):
			writeJSON(w, http.StatusOK, []string{})
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/merge"):
			var request struct {
				SHA string `json:"sha"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			heads <- request.SHA
			if request.SHA != otherHead {
				writeJSON(w, http.StatusConflict, map[string]string{"message": "Head branch was modified"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"sha": mergeCommit, "merged": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	f.worker.github = githubinfo.New(githubinfo.Options{BaseURL: server.URL, MergeToken: "test-token"})
	card := f.newCard(t, "Ship the panel")
	pr := "https://github.com/example/project/pull/72"
	f.deliverAsAgent(t, card.Key, pr)

	f.poll(t)
	f.poll(t)
	if len(heads) != 1 || <-heads != headCommit || f.state(t, card.Key) != domain.Review {
		t.Fatal("expected one rejected merge of the delivered head and the card still in review")
	}

	lease := reopenForRevision(t, f, card.Key)
	if _, _, err := f.service.DeliverRevision(f.ctx, card.Key, f.agent, &lease.ID, "Updated panel",
		[]string{pr}, nil, &pr, stringPointer(otherHead)); err != nil {
		t.Fatal(err)
	}
	f.poll(t)
	if len(heads) != 1 || <-heads != otherHead || f.state(t, card.Key) != domain.Done {
		t.Fatal("expected the new checked head to merge after redelivery")
	}
}

func TestVisualPlanClosesAfterReviewManualMergeAndProduction(t *testing.T) {
	f := newIntegrationFixture(t)
	approvedAt := time.Date(2023, 11, 15, 10, 0, 0, 0, time.UTC)
	mergedAt := approvedAt.Add(2 * time.Minute)
	polledAt := approvedAt.Add(5 * time.Minute)
	f.clock.now = approvedAt.Unix()

	preview := &revisionStub{revision: revealMerge}
	previewServer := httptest.NewServer(preview.handler())
	defer previewServer.Close()
	for name, url := range map[string]string{"production": f.envURL, "preview": previewServer.URL} {
		if _, err := f.service.AddEnvironment(f.ctx, f.owner, "MI", name, url); err != nil {
			t.Fatal(err)
		}
	}
	first := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	review := app.PlanReview{
		Scenario: "make scenario", VisualReview: true, RevealTask: reveal.Key,
		VisibleChange: stringPointer("Show the panel"), TargetEnvironment: "missing",
	}
	policy := app.PlanPolicy{Repository: "example/project", Branch: "main", WorkflowID: 42}
	if _, err := f.repo.ApproveGraph(f.ctx, f.owner, []string{first.Key, reveal.Key}, nil, policy, review, f.clock.Now()); err == nil {
		t.Fatal("approved a plan with an unknown target environment")
	}
	review.TargetEnvironment = "production"
	graph, err := f.repo.ApproveGraph(f.ctx, f.owner, []string{first.Key, reveal.Key}, nil, policy, review, f.clock.Now())
	if err != nil || graph.TargetEnvironment != "production" {
		t.Fatalf("approve production plan: %+v, %v", graph, err)
	}

	revealPR := "https://github.com/example/project/pull/74"
	f.deliverAsAgent(t, first.Key, "https://github.com/example/project/pull/73")
	f.deliverWithScenario(t, graph.ID, reveal.Key, revealPR, app.ScenarioPassed)
	f.poll(t)

	var githubHead = headCommit
	var githubMerged bool
	var reviews []map[string]any
	addReview := func(state, head string, at time.Time) {
		reviews = append(reviews, map[string]any{
			"state": state, "commit_id": head, "submitted_at": at,
			"user": map[string]string{"login": "example"},
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/example/project/pulls/74/reviews":
			writeJSON(w, http.StatusOK, reviews)
		case "/repos/example/project/pulls/74":
			writeJSON(w, http.StatusOK, map[string]any{
				"merged": githubMerged, "merged_at": mergedAt, "merge_commit_sha": revealMerge,
				"head": map[string]string{"sha": githubHead},
				"base": map[string]any{"ref": "main", "repo": map[string]string{"full_name": "example/project"}},
			})
		case "/repos/example/project/actions/workflows/42/runs":
			if r.URL.Query().Get("head_sha") != revealMerge {
				t.Errorf("workflow queried for %q, want %s", r.URL.Query().Get("head_sha"), revealMerge)
			}
			writeJSON(w, http.StatusOK, map[string]any{"total_count": 1, "workflow_runs": []map[string]any{{
				"id": 7, "run_attempt": 1, "workflow_id": 42, "event": "push",
				"head_branch": "main", "head_sha": revealMerge, "status": "completed",
				"conclusion": "success", "repository": map[string]string{"full_name": "example/project"},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	worker := graphWorker{store: f.repo, clock: f.clock, policy: policy,
		github: githubinfo.New(githubinfo.Options{BaseURL: server.URL, VerificationToken: "test-token", Budget: 1000})}
	pollGraph := func() app.PlanGraph {
		t.Helper()
		if err := worker.poll(f.ctx); err != nil {
			t.Fatal(err)
		}
		current, err := f.repo.Graph(f.ctx, graph.ID)
		if err != nil {
			t.Fatal(err)
		}
		return current
	}
	addReview("APPROVED", headCommit, approvedAt)
	if approval := pollGraph().Approval; approval == nil || approval.HeadSHA != headCommit {
		t.Fatalf("initial approval = %+v", approval)
	}
	addReview("CHANGES_REQUESTED", headCommit, approvedAt.Add(20*time.Second))
	addReview("COMMENTED", headCommit, approvedAt.Add(30*time.Second))
	if approval := pollGraph().Approval; approval != nil {
		t.Fatalf("changes requested left approval %+v", approval)
	}
	pollGraph()
	corrections := f.corrections(t, graph.ID)
	if len(corrections) != 1 {
		t.Fatalf("corrections = %+v, want one", corrections)
	}

	f.github.mergeSHA = olderCommit
	f.deliverAsAgent(t, corrections[0].Key, "https://github.com/example/project/pull/75")
	f.poll(t)
	lease := reopenForRevision(t, f, reveal.Key)
	githubHead = otherHead
	report := app.ScenarioReport{TaskKey: reveal.Key, HeadSHA: otherHead, Command: "true",
		Outcome: app.ScenarioPassed, EvidenceURL: stringPointer(revealPR + "#issuecomment-1")}
	if _, err := f.repo.ReportScenario(f.ctx, f.agent, graph.ID, report, f.clock.Now()); err == nil {
		t.Fatal("accepted a passed result from a different command")
	}
	report.Command = "make scenario"
	if _, err := f.repo.ReportScenario(f.ctx, f.agent, graph.ID, report, f.clock.Now()); err != nil {
		t.Fatalf("report scenario for the new head: %v", err)
	}
	if _, _, err := f.service.DeliverRevision(f.ctx, reveal.Key, f.agent, &lease.ID, "Addressed review",
		[]string{revealPR}, nil, &revealPR, &githubHead); err != nil {
		t.Fatal(err)
	}
	if approval := pollGraph().Approval; approval != nil {
		t.Fatalf("new head inherited approval %+v", approval)
	}

	addReview("APPROVED", githubHead, approvedAt.Add(time.Minute))
	f.clock.now = approvedAt.Add(time.Minute).Unix()
	if approval := pollGraph().Approval; approval == nil || approval.HeadSHA != githubHead {
		t.Fatalf("new head approval = %+v", approval)
	}

	githubMerged = true
	f.clock.now = polledAt.Unix()
	observed := pollGraph()
	for _, node := range observed.Nodes {
		if node.Key == reveal.Key && (node.Proof == nil || node.Proof.HeadSHA != githubHead ||
			node.Proof.MergeSHA != revealMerge || node.Proof.VerifiedAt != polledAt.Unix()) {
			t.Fatalf("visual merge proof = %+v, want merge at %s observed at %s", node.Proof, mergedAt, polledAt)
		}
	}
	f.poll(t)
	if done := f.completed(t, graph.ID); done != nil {
		t.Fatal("plan completed while only preview served the merged revision")
	}
	f.env.set(revealMerge)
	f.poll(t)
	if done := f.completed(t, graph.ID); done == nil {
		t.Fatal("production serves the reviewed merge and the plan should be complete")
	}
	if _, merges := f.github.counts(); merges != 2 {
		t.Fatalf("automerge calls = %d, want only the preparatory and correction pull requests", merges)
	}
}

func reopenForRevision(t *testing.T, f *integrationFixture, key string) domain.Lease {
	t.Helper()
	card := f.card(t, key)
	if _, err := f.service.Move(f.ctx, key, domain.InProgress, "", f.owner, card.Version, true); err != nil {
		t.Fatal(err)
	}
	lease, err := f.repo.GetLiveLease(f.ctx, key, f.clock.Now())
	if err != nil || lease == nil {
		t.Fatalf("read the delivering agent lease: %v", err)
	}
	return *lease
}
