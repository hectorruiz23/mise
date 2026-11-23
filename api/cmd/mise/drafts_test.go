package main

import (
	"errors"
	"testing"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func draftPolicy() app.PlanPolicy {
	return app.PlanPolicy{Repository: "example/project", Branch: "main", WorkflowID: 9}
}

func draftInput() app.DraftInput {
	return app.DraftInput{
		Project:       "MI",
		Outcome:       "the board shows what was closed",
		Scenario:      "make scenario",
		VisibleChange: stringPointer("the board gains a Done tab"),
		VisualReview:  true,
		RevealKey:     "T3",
		Tasks: []app.DraftTask{
			{Key: "T1", Title: "Prepare the tab"},
			{Key: "T2", Title: "Prepare the route"},
			{Key: "T3", Title: "Show the tab", Acceptance: stringPointer("the tab opens the closed cards")},
		},
		Edges: []app.DraftEdge{
			{From: "T1", To: "T3", Kind: "integration"},
			{From: "T2", To: "T3", Kind: "integration"},
		},
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var rule *domain.RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want a rule error with code %q", err, code)
	}
	if rule.Code != code {
		t.Fatalf("code = %q, want %q (%v)", rule.Code, code, rule)
	}
}

func TestDraftProposalKeepsItsLinkAndActivatesOnce(t *testing.T) {
	f := newIntegrationFixture(t)
	policy := draftPolicy()

	draft, err := f.repo.CreateDraft(f.ctx, f.agent, draftInput(), policy, f.clock.Now())
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if draft.ID < 1 || len(draft.Roots) != 2 || draft.Roots[0] != "T1" || draft.Roots[1] != "T2" {
		t.Fatalf("draft = %+v, want two roots", draft)
	}
	var before int
	if err := f.database.QueryRow(`SELECT count(*) FROM tasks`).Scan(&before); err != nil {
		t.Fatalf("count tasks: %v", err)
	}

	edited := draftInput()
	edited.Outcome = "the board shows what was closed, newest first"
	edited.Tasks[2].Acceptance = stringPointer("the tab lists closed cards, newest first")
	f.clock.now += 60
	updated, err := f.repo.UpdateDraft(f.ctx, f.agent, draft.ID, draft.Version, edited, f.clock.Now())
	if err != nil {
		t.Fatalf("update draft: %v", err)
	}
	if updated.ID != draft.ID {
		t.Fatalf("the link changed: %d became %d", draft.ID, updated.ID)
	}
	if updated.Version != draft.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, draft.Version+1)
	}
	if updated.Outcome != edited.Outcome || updated.Tasks[2].Acceptance == nil ||
		*updated.Tasks[2].Acceptance != *edited.Tasks[2].Acceptance {
		t.Fatalf("the edit did not survive: %+v", updated)
	}
	var afterEdit int
	if err := f.database.QueryRow(`SELECT count(*) FROM tasks`).Scan(&afterEdit); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if afterEdit != before {
		t.Fatalf("the proposal created %d cards before approval", afterEdit-before)
	}

	_, err = f.repo.ApproveDraft(f.ctx, f.owner, draft.ID, draft.Version, policy, f.clock.Now())
	wantCode(t, err, domain.CodeStaleVersion)

	f.clock.now += 60
	approved, err := f.repo.ApproveDraft(f.ctx, f.owner, draft.ID, updated.Version, policy, f.clock.Now())
	if err != nil {
		t.Fatalf("approve draft: %v", err)
	}
	if approved.State != app.DraftApproved || approved.PlanID == nil {
		t.Fatalf("approved = %+v, want a plan", approved)
	}
	plan, err := f.repo.Graph(f.ctx, *approved.PlanID)
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}
	if plan.Scenario != edited.Scenario || !plan.VisualReview {
		t.Fatalf("plan = %+v, want the proposal's scenario and visual review", plan)
	}
	states := map[string]string{}
	reveals := ""
	for _, node := range plan.Nodes {
		states[node.Key] = node.State
		if node.Reveals {
			reveals = node.Key
		}
	}
	if len(states) != 3 {
		t.Fatalf("plan nodes = %+v, want three", states)
	}
	var roots []string
	for key, state := range states {
		if state == string(domain.Ready) {
			roots = append(roots, key)
		}
	}
	if len(roots) != 2 {
		t.Fatalf("roots = %+v, want two ready", states)
	}
	if reveals == "" || states[reveals] != string(domain.Backlog) {
		t.Fatalf("revealing task = %q in %q, want waiting in backlog", reveals, states[reveals])
	}

	f.clock.now += 60
	again, err := f.repo.ApproveDraft(f.ctx, f.owner, draft.ID, updated.Version, policy, f.clock.Now())
	if err != nil {
		t.Fatalf("approve twice: %v", err)
	}
	if again.PlanID == nil || *again.PlanID != *approved.PlanID {
		t.Fatalf("a second approval created another plan: %v then %v", approved.PlanID, again.PlanID)
	}
	var plans, cards int
	if err := f.database.QueryRow(`SELECT count(*) FROM plans`).Scan(&plans); err != nil {
		t.Fatalf("count plans: %v", err)
	}
	if err := f.database.QueryRow(`SELECT count(*) FROM tasks WHERE idempotency_key LIKE 'draft:%'`).Scan(&cards); err != nil {
		t.Fatalf("count cards: %v", err)
	}
	if plans != 1 || cards != 3 {
		t.Fatalf("plans = %d and cards = %d, want 1 and 3", plans, cards)
	}

	if _, _, err := f.service.Acquire(f.ctx, roots[0], f.agent); err != nil {
		t.Fatalf("claim the root: %v", err)
	}
	if _, _, err := f.service.Acquire(f.ctx, roots[0], f.agent); err == nil {
		t.Fatal("the same root was claimed twice")
	}
	if _, _, err := f.service.Acquire(f.ctx, reveals, f.agent); err == nil {
		t.Fatal("the revealing task was claimable before its prerequisites")
	}
}

func TestDraftGuardsApprovalAndEditing(t *testing.T) {
	f := newIntegrationFixture(t)
	policy := draftPolicy()

	draft, err := f.repo.CreateDraft(f.ctx, f.agent, draftInput(), policy, f.clock.Now())
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	wantCode(t, func() error {
		_, err := f.repo.ApproveDraft(f.ctx, f.agent, draft.ID, draft.Version, policy, f.clock.Now())
		return err
	}(), domain.CodeForbidden)
	wantCode(t, func() error {
		_, err := f.repo.Draft(f.ctx, domain.Actor{Kind: domain.Viewer}, draft.ID)
		return err
	}(), domain.CodeForbidden)

	approved, err := f.repo.ApproveDraft(f.ctx, f.owner, draft.ID, draft.Version, policy, f.clock.Now())
	if err != nil {
		t.Fatalf("approve draft: %v", err)
	}
	wantCode(t, func() error {
		_, err := f.repo.UpdateDraft(f.ctx, f.agent, draft.ID, approved.Version, draftInput(), f.clock.Now())
		return err
	}(), domain.CodeInvalidTransition)

	other := draftInput()
	other.Outcome = "something else"
	stale, err := f.repo.CreateDraft(f.ctx, f.agent, other, policy, f.clock.Now())
	if err != nil {
		t.Fatalf("create second draft: %v", err)
	}
	if _, err := f.repo.UpdateDraft(f.ctx, f.agent, stale.ID, stale.Version, other, f.clock.Now()); err != nil {
		t.Fatalf("update second draft: %v", err)
	}
	wantCode(t, func() error {
		_, err := f.repo.ApproveDraft(f.ctx, f.owner, stale.ID, stale.Version, policy, f.clock.Now())
		return err
	}(), domain.CodeStaleVersion)
}
