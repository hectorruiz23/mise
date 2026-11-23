package sqlite_test

import (
	"context"
	"strings"
	"testing"

	"mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func TestPlanApprovalReleasesRoots(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "Request"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Store"})
	c, _ := createTask(t, service, app.CreateParams{Title: "Surface"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}

	graph, err := repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key, c.Key},
		[]domain.PlanEdge{{From: a.Key, To: c.Key, Kind: domain.EdgeIntegration},
			{From: b.Key, To: c.Key, Kind: domain.EdgeIntegration}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, node := range graph.Nodes {
		states[node.Key] = node.State
	}
	if states[a.Key] != string(domain.Ready) || states[b.Key] != string(domain.Ready) {
		t.Fatalf("roots = %+v, want both ready", states)
	}
	if states[c.Key] != string(domain.Backlog) {
		t.Fatalf("dependent = %q, want backlog", states[c.Key])
	}
	for _, node := range graph.Nodes {
		if node.Key == c.Key && len(node.WaitingOn) != 2 {
			t.Fatalf("waiting_on = %+v, want both prerequisites", node.WaitingOn)
		}
	}

	if _, err := repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key},
		[]domain.PlanEdge{}, policy, app.PlanReview{Scenario: "make scenario"}, testNow); err == nil {
		t.Fatal("a task was approved into two plans")
	}
}

func TestPlanReleasesWhenAllPrerequisitesVerified(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "Request"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Store"})
	c, _ := createTask(t, service, app.CreateParams{Title: "Surface"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}
	graph, err := repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key, c.Key},
		[]domain.PlanEdge{{From: a.Key, To: c.Key, Kind: domain.EdgeIntegration},
			{From: b.Key, To: c.Key, Kind: domain.EdgeIntegration}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	label := "head_sha"
	verify := func(key, prURL, head, merge string) app.PlanProof {
		t.Helper()
		if _, _, err := service.Acquire(ctx, key, agent()); err != nil {
			t.Fatal(err)
		}
		_, delivery, err := service.DeliverItems(ctx, key, agent(), nil, "Work done",
			[]domain.EvidenceItem{{Label: &label, Raw: head}}, &prURL)
		if err != nil {
			t.Fatal(err)
		}
		proof := app.PlanProof{TaskKey: key, DeliveryID: delivery.ID, PRURL: prURL,
			HeadSHA: head, MergeSHA: merge, RunID: 7, Attempt: 1}
		if _, err := repo.ReleaseGraphTask(ctx, graph.ID, key, proof, testNow); err != nil {
			t.Fatal(err)
		}
		return proof
	}
	aHead, bHead := strings.Repeat("a", 40), strings.Repeat("c", 40)
	first := verify(a.Key, "https://github.com/owner/project/pull/1", aHead, strings.Repeat("b", 40))
	graph, err = repo.Graph(ctx, graph.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, node := range graph.Nodes {
		states[node.Key] = node.State
	}
	if states[c.Key] != string(domain.Backlog) {
		t.Fatalf("dependent released with one prerequisite: %q", states[c.Key])
	}
	if _, err := repo.ReleaseGraphTask(ctx, graph.ID, a.Key, first, testNow); err != nil {
		t.Fatalf("repeated verification: %v", err)
	}
	verify(b.Key, "https://github.com/owner/project/pull/2", bHead, strings.Repeat("d", 40))
	graph, err = repo.Graph(ctx, graph.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.Key == c.Key && node.State != string(domain.Ready) {
			t.Fatalf("dependent = %q, want ready", node.State)
		}
	}
	movements, err := service.Movements(ctx, human(), c.Key)
	if err != nil || len(movements) != 2 {
		t.Fatalf("dependent movements = %+v, %v; want creation and one release", movements, err)
	}
}

func TestPlanRejectsBrokenGraphs(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "One"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Two"})
	other, _ := createTask(t, service, app.CreateParams{ProjectKey: "PO", Title: "Elsewhere"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}
	if _, err := repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key},
		[]domain.PlanEdge{{From: a.Key, To: b.Key, Kind: domain.EdgeIntegration},
			{From: b.Key, To: a.Key, Kind: domain.EdgeIntegration}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow); err == nil {
		t.Fatal("a cycle was approved")
	}
	_, err := repo.ApproveGraph(ctx, human(), []string{a.Key, other.Key},
		[]domain.PlanEdge{{From: a.Key, To: other.Key, Kind: domain.EdgeIntegration}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
	if _, err := repo.ApproveGraph(ctx, agent(), []string{a.Key, b.Key},
		[]domain.PlanEdge{}, policy, app.PlanReview{Scenario: "make scenario"}, testNow); err == nil {
		t.Fatal("an agent approved a plan")
	}
	_, err = repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key},
		[]domain.PlanEdge{{From: a.Key, To: b.Key, Kind: domain.EdgeDeployment}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow)
	wantRuleCode(t, err, domain.CodeInvalidInput)
}

func TestPlanCancellationLeavesBacklog(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "Request"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Surface"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}
	graph, err := repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key},
		[]domain.PlanEdge{{From: a.Key, To: b.Key, Kind: domain.EdgeIntegration}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CancelGraph(ctx, agent(), graph.ID, testNow); err == nil {
		t.Fatal("an agent cancelled a plan")
	}
	cancelled, err := repo.CancelGraph(ctx, human(), graph.ID, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != domain.PlanCancelled || cancelled.CancelledAt != testNow {
		t.Fatalf("cancelled plan = %+v", cancelled)
	}
	card := mustTask(t, service, b.Key)
	if card.State != domain.Backlog {
		t.Fatalf("dependent = %q, want backlog", card.State)
	}
	if _, err := service.Move(ctx, b.Key, domain.Ready, "", human(), card.Version, true); err != nil {
		t.Fatalf("the dependent task is not manageable after the cancellation: %v", err)
	}
}

func TestPlanShowsBlockedBranch(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "Request"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Store"})
	c, _ := createTask(t, service, app.CreateParams{Title: "Surface"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}
	graph, err := repo.ApproveGraph(ctx, human(), []string{a.Key, b.Key, c.Key},
		[]domain.PlanEdge{{From: a.Key, To: c.Key, Kind: domain.EdgeIntegration},
			{From: b.Key, To: c.Key, Kind: domain.EdgeIntegration}}, policy, app.PlanReview{Scenario: "make scenario"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	card := mustTask(t, service, b.Key)
	card, err = service.Move(ctx, b.Key, domain.InProgress, "", human(), card.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Move(ctx, b.Key, domain.Blocked, "the upstream API changed", human(), card.Version, true); err != nil {
		t.Fatal(err)
	}
	graph, err = repo.Graph(ctx, graph.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		switch node.Key {
		case b.Key:
			if node.State != string(domain.Blocked) {
				t.Fatalf("stuck node = %q, want blocked", node.State)
			}
		case c.Key:
			if len(node.WaitingOn) != 2 {
				t.Fatalf("waiting_on = %+v, want both prerequisites", node.WaitingOn)
			}
		case a.Key:
			if node.State != string(domain.Ready) {
				t.Fatalf("independent branch = %q, want ready", node.State)
			}
		}
	}
}
