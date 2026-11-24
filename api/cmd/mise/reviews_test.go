package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

const (
	revealMerge = "4444444444444444444444444444444444444444"
	otherHead   = "5555555555555555555555555555555555555555"
)

type reviewStub struct {
	reviews   []githubinfo.Review
	mergedBy  string
	mergedErr error
}

func (s *reviewStub) Reviews(context.Context, githubinfo.Ref) ([]githubinfo.Review, error) {
	return s.reviews, nil
}

func (s *reviewStub) MergedBy(context.Context, githubinfo.Ref) (string, error) {
	return s.mergedBy, s.mergedErr
}

func (f *integrationFixture) card(t *testing.T, key string) domain.Task {
	t.Helper()
	task, err := f.service.Get(f.ctx, f.owner, key)
	if err != nil {
		t.Fatalf("read card %s: %v", key, err)
	}
	return task
}

func (f *integrationFixture) approvePlan(
	t *testing.T,
	tasks []string,
	edges []domain.PlanEdge,
	review app.PlanReview,
) app.PlanGraph {
	t.Helper()
	policy := app.PlanPolicy{Repository: "example/project", Branch: "main", WorkflowID: 42}
	graph, err := f.repo.ApproveGraph(f.ctx, f.owner, tasks, edges, policy, review, integrationNow)
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}
	return graph
}

func (f *integrationFixture) deliverWithScenario(
	t *testing.T,
	planID int64,
	key string,
	prURL string,
	outcome string,
) string {
	t.Helper()
	task := f.card(t, key)
	if task.State == domain.Backlog {
		if _, err := f.service.Move(f.ctx, key, domain.Ready, "", f.owner, task.Version, true); err != nil {
			t.Fatalf("move %s to ready: %v", key, err)
		}
	}
	_, lease, err := f.service.Acquire(f.ctx, key, f.agent)
	if err != nil {
		t.Fatalf("lease %s: %v", key, err)
	}
	head := headCommit
	evidence := "https://github.com/example/project/pull/1#issuecomment-1"
	if _, err := f.repo.ReportScenario(f.ctx, f.agent, planID, app.ScenarioReport{
		TaskKey:     key,
		HeadSHA:     head,
		Command:     "make scenario",
		Outcome:     outcome,
		EvidenceURL: &evidence,
	}, f.clock.Now()); err != nil {
		t.Fatalf("report scenario for %s: %v", key, err)
	}
	if _, _, err := f.service.DeliverRevision(f.ctx, key, f.agent, &lease.ID, "Work done",
		[]string{"https://example.test/build"}, nil, &prURL, &head); err != nil {
		t.Fatalf("deliver %s: %v", key, err)
	}
	return head
}

func (f *integrationFixture) prove(t *testing.T, planID int64, key, prURL, head, merge string) {
	t.Helper()
	if _, err := f.repo.ReleaseGraphTask(f.ctx, planID, key, app.PlanProof{
		TaskKey: key, DeliveryID: 1, PRURL: prURL, HeadSHA: head, MergeSHA: merge,
		RunID: 7, Attempt: 1,
	}, f.clock.Now()); err != nil {
		t.Fatalf("release %s: %v", key, err)
	}
}

func (f *integrationFixture) corrections(t *testing.T, planID int64) []app.PlanNode {
	t.Helper()
	graph, err := f.repo.Graph(f.ctx, planID)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	corrections := make([]app.PlanNode, 0)
	for _, node := range graph.Nodes {
		if node.Correction {
			corrections = append(corrections, node)
		}
	}
	return corrections
}

func TestRevealPullRequestIsNeverMergedAutomatically(t *testing.T) {
	f := newIntegrationFixture(t)
	plain := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{plain.Key, reveal.Key}, nil, app.PlanReview{
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealTask:    reveal.Key,
	})

	f.deliverAsAgent(t, plain.Key, "https://github.com/example/project/pull/21")
	f.deliverWithScenario(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/22", app.ScenarioPassed)

	f.poll(t)

	if state := f.state(t, plain.Key); state != domain.Done {
		t.Fatalf("plain card = %q, want done once merged", state)
	}
	if _, merges := f.github.counts(); merges != 1 {
		t.Fatalf("merge calls = %d, want only the plain pull request", merges)
	}
	if state := f.state(t, reveal.Key); state != domain.Review {
		t.Fatalf("revealing card = %q, want review until the owner merges", state)
	}
	if attempts := f.mergeAttempts(t, reveal.Key); len(attempts) != 0 {
		t.Fatalf("merge attempts on the revealing card = %+v, want none", attempts)
	}
}

func TestPlanWaitsForTheApprovedRevision(t *testing.T) {
	f := newIntegrationFixture(t)
	environment, err := f.service.AddEnvironment(f.ctx, f.owner, "MI", "staging", f.envURL)
	if err != nil || environment.Name != "staging" {
		t.Fatalf("add environment: %v", err)
	}
	first := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, reveal.Key}, nil, app.PlanReview{
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealTask:    reveal.Key,
	})

	firstPR := "https://github.com/example/project/pull/31"
	head := f.deliverWithScenario(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/32", app.ScenarioPassed)
	f.deliverAsAgent(t, first.Key, firstPR)
	f.prove(t, graph.ID, first.Key, firstPR, headCommit, mergeCommit)
	f.prove(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/32", head, revealMerge)

	if _, err := f.repo.RecordApproval(f.ctx, graph.ID, reveal.Key, otherHead, "example", f.clock.Now()); err != nil {
		t.Fatalf("record approval of another revision: %v", err)
	}
	if done := f.completed(t, graph.ID); done != nil {
		t.Fatalf("plan completed with an approval of another revision: %d", *done)
	}

	if _, err := f.repo.RecordApproval(f.ctx, graph.ID, reveal.Key, head, "example", f.clock.Now()); err != nil {
		t.Fatalf("record approval: %v", err)
	}
	if done := f.completed(t, graph.ID); done != nil {
		t.Fatalf("plan completed before the approved revision was serving: %d", *done)
	}

	f.env.set(revealMerge)
	f.poll(t)

	if done := f.completed(t, graph.ID); done == nil {
		t.Fatal("plan did not complete once the approved revision was serving")
	}
	f.poll(t)
	if done := f.completed(t, graph.ID); done == nil {
		t.Fatal("plan lost its completion")
	}
}

func TestFailedScenarioOpensOneCorrection(t *testing.T) {
	f := newIntegrationFixture(t)
	first := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, reveal.Key}, nil, app.PlanReview{
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealTask:    reveal.Key,
	})

	f.deliverWithScenario(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/41", app.ScenarioFailed)

	corrections := f.corrections(t, graph.ID)
	if len(corrections) != 1 {
		t.Fatalf("corrections = %+v, want one", corrections)
	}
	if corrections[0].State != string(domain.Ready) {
		t.Fatalf("correction state = %q, want ready", corrections[0].State)
	}
	card := f.card(t, corrections[0].Key)
	if card.Notes == nil || !strings.Contains(*card.Notes, "issuecomment-1") {
		t.Fatalf("correction notes = %v, want the evidence link", card.Notes)
	}
	if done := f.completed(t, graph.ID); done != nil {
		t.Fatal("a failed scenario completed the plan")
	}

	if _, err := f.repo.RequestCorrection(f.ctx, graph.ID, reveal.Key, headCommit,
		"scenario:"+headCommit, "the acceptance scenario failed", f.clock.Now()); err != nil {
		t.Fatalf("repeat correction: %v", err)
	}
	if again := f.corrections(t, graph.ID); len(again) != 1 {
		t.Fatalf("corrections after the same event twice = %d, want 1", len(again))
	}
}

func TestMergingTheRevealingPullRequestRecordsTheApproval(t *testing.T) {
	f := newIntegrationFixture(t)
	first := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, reveal.Key}, nil, app.PlanReview{
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealTask:    reveal.Key,
	})
	f.deliverWithScenario(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/52", app.ScenarioPassed)

	worker := &reviewWorker{
		store:  f.repo,
		clock:  f.clock,
		github: &reviewStub{mergedBy: "hectorruiz23"},
	}
	if err := worker.poll(f.ctx); err != nil {
		t.Fatalf("poll reviews: %v", err)
	}

	current, err := f.repo.Graph(f.ctx, graph.ID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if current.Approval == nil {
		t.Fatal("the merge left no approval")
	}
	if current.Approval.HeadSHA != headCommit || current.Approval.Reviewer != "hectorruiz23" {
		t.Fatalf("approval = %+v, want the merge of %s by its author", current.Approval, headCommit)
	}
}

func TestAnUnmergedPullRequestIsNotAnApproval(t *testing.T) {
	f := newIntegrationFixture(t)
	first := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, reveal.Key}, nil, app.PlanReview{
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealTask:    reveal.Key,
	})
	f.deliverWithScenario(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/53", app.ScenarioPassed)

	worker := &reviewWorker{store: f.repo, clock: f.clock, github: &reviewStub{}}
	if err := worker.poll(f.ctx); err != nil {
		t.Fatalf("poll reviews: %v", err)
	}

	current, err := f.repo.Graph(f.ctx, graph.ID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if current.Approval != nil {
		t.Fatalf("an unmerged pull request produced the approval %+v", current.Approval)
	}

	unreadable := &reviewWorker{store: f.repo, clock: f.clock,
		github: &reviewStub{mergedErr: errors.New("upstream unavailable")}}
	if err := unreadable.poll(f.ctx); err != nil {
		t.Fatalf("an unreadable pull request broke the poll: %v", err)
	}
	if again, err := f.repo.Graph(f.ctx, graph.ID); err != nil || again.Approval != nil {
		t.Fatalf("an unreadable pull request produced the approval %+v (%v)", again.Approval, err)
	}
}

func TestTheScenarioCanBeReportedAfterTheCardClosed(t *testing.T) {
	f := newIntegrationFixture(t)
	first := f.newCard(t, "Prepare the panel")
	second := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, second.Key}, nil, app.PlanReview{Scenario: "make scenario"})

	prURL := "https://github.com/example/project/pull/54"
	f.deliverAsAgent(t, first.Key, "https://github.com/example/project/pull/55")
	f.deliverAsAgent(t, second.Key, prURL)

	card := f.card(t, second.Key)
	if _, err := f.service.Move(f.ctx, second.Key, domain.Done, "", f.owner, card.Version, true); err != nil {
		t.Fatalf("close the card: %v", err)
	}

	evidence := prURL + "#issuecomment-9"
	report := app.ScenarioReport{TaskKey: second.Key, HeadSHA: headCommit,
		Command: "make scenario", Outcome: app.ScenarioPassed, EvidenceURL: &evidence}
	if _, err := f.repo.ReportScenario(f.ctx, f.agent, graph.ID, report, f.clock.Now()); err != nil {
		t.Fatalf("report the scenario of a revision it delivered: %v", err)
	}

	other := domain.Actor{ID: 3, Kind: domain.Agent, Name: "Other"}
	if _, err := f.repo.ReportScenario(f.ctx, other, graph.ID, app.ScenarioReport{
		TaskKey: second.Key, HeadSHA: otherHead, Command: "make scenario",
		Outcome: app.ScenarioPassed, EvidenceURL: &evidence}, f.clock.Now()); err == nil {
		t.Fatal("an agent that never delivered that revision reported its scenario")
	}
}

func TestChangesRequestedOpensACorrection(t *testing.T) {
	f := newIntegrationFixture(t)
	first := f.newCard(t, "Prepare the panel")
	reveal := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, reveal.Key}, nil, app.PlanReview{
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealTask:    reveal.Key,
	})
	f.deliverWithScenario(t, graph.ID, reveal.Key, "https://github.com/example/project/pull/51", app.ScenarioPassed)

	worker := &reviewWorker{
		store: f.repo,
		clock: f.clock,
		github: &reviewStub{reviews: []githubinfo.Review{{
			State:       "CHANGES_REQUESTED",
			CommitID:    headCommit,
			User:        "example",
			SubmittedAt: time.Unix(integrationNow, 0),
		}}},
	}
	if err := worker.poll(f.ctx); err != nil {
		t.Fatalf("poll reviews: %v", err)
	}
	if corrections := f.corrections(t, graph.ID); len(corrections) != 1 {
		t.Fatalf("corrections = %+v, want one", corrections)
	}

	if err := worker.poll(f.ctx); err != nil {
		t.Fatalf("poll reviews again: %v", err)
	}
	if again := f.corrections(t, graph.ID); len(again) != 1 {
		t.Fatalf("corrections after the same review twice = %d, want 1", len(again))
	}
}

func TestPlanWithoutVisualReviewClosesByItself(t *testing.T) {
	f := newIntegrationFixture(t)
	environment, err := f.service.AddEnvironment(f.ctx, f.owner, "MI", "staging", f.envURL)
	if err != nil || environment.Name != "staging" {
		t.Fatalf("add environment: %v", err)
	}
	first := f.newCard(t, "Prepare the panel")
	second := f.newCard(t, "Show the panel")
	graph := f.approvePlan(t, []string{first.Key, second.Key}, nil, app.PlanReview{Scenario: "make scenario"})

	firstPR := "https://github.com/example/project/pull/61"
	secondPR := "https://github.com/example/project/pull/62"
	f.deliverAsAgent(t, first.Key, firstPR)
	head := f.deliverWithScenario(t, graph.ID, second.Key, secondPR, app.ScenarioPassed)
	f.prove(t, graph.ID, first.Key, firstPR, headCommit, mergeCommit)
	f.prove(t, graph.ID, second.Key, secondPR, head, revealMerge)

	f.env.set(revealMerge)
	f.poll(t)

	if done := f.completed(t, graph.ID); done == nil {
		t.Fatal("a plan without visual review did not close by itself")
	}
	var approvals int
	if err := f.database.QueryRow(`SELECT count(*) FROM plan_approvals`).Scan(&approvals); err != nil {
		t.Fatalf("count approvals: %v", err)
	}
	if approvals != 0 {
		t.Fatalf("approvals = %d, want none", approvals)
	}
}

func (f *integrationFixture) completed(t *testing.T, planID int64) *int64 {
	t.Helper()
	graph, err := f.repo.Graph(f.ctx, planID)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	return graph.CompletedAt
}

func stringPointer(value string) *string {
	return &value
}
