package sqlite_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func TestDependencyPlanRelease(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "Create a project"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Rename a project"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}

	_, err := repo.ApprovePlan(ctx, agent(), a.Key, b.Key, policy, testNow)
	wantRuleCode(t, err, domain.CodeForbidden)
	for range 2 {
		if _, err := repo.ApprovePlan(ctx, human(), a.Key, b.Key, policy, testNow); err != nil {
			t.Fatal(err)
		}
	}
	_, err = service.Move(ctx, b.Key, domain.Ready, "", human(), b.Version, true)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
	_, _, err = service.Acquire(ctx, b.Key, agent())
	wantRuleCode(t, err, domain.CodeLeaseConflict)
	if _, _, err := service.Acquire(ctx, a.Key, agent()); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	prURL, label := "https://github.com/owner/project/pull/72", "head_sha"
	deliver := func(items []domain.EvidenceItem) (domain.Task, domain.Delivery) {
		t.Helper()
		task, delivery, err := service.DeliverItems(ctx, a.Key, agent(), nil, "Working implementation", items, &prURL)
		if err != nil {
			t.Fatal(err)
		}
		return task, delivery
	}
	rework := func(task domain.Task) {
		t.Helper()
		if _, err := service.Move(ctx, a.Key, domain.InProgress, "", agent(), task.Version, true); err != nil {
			t.Fatal(err)
		}
	}
	item := domain.EvidenceItem{Label: &label, Raw: head}
	task, _ := deliver([]domain.EvidenceItem{item, item})
	if plans, err := repo.PendingPlans(ctx); err != nil || len(plans) != 0 {
		t.Fatalf("ambiguous SHA eligible: %+v, %v", plans, err)
	}
	rework(task)
	task, delivery := deliver([]domain.EvidenceItem{item})
	if plans, err := repo.PendingPlans(ctx); err != nil || len(plans) != 1 || plans[0].DeliveryID != delivery.ID || plans[0].HeadSHA != head {
		t.Fatalf("eligible plans: %+v, %v", plans, err)
	}
	proof := app.MergeProof{DeliveryID: delivery.ID, PRURL: prURL, HeadSHA: head, MergeSHA: strings.Repeat("b", 40), RunID: 9, Attempt: 1}
	rework(task)
	_, latest := deliver([]domain.EvidenceItem{item})
	_, err = repo.ReleasePlan(ctx, a.Key, proof, testNow)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
	proof.DeliveryID = latest.ID
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			plan, err := repo.ReleasePlan(ctx, a.Key, proof, testNow)
			if err != nil || plan.Proof == nil || *plan.Proof != proof {
				t.Errorf("release: %+v, %v", plan, err)
			}
		})
	}
	group.Wait()
	plan, err := repo.Plan(ctx, a.Key)
	if err != nil || plan.Proof == nil || *plan.Proof != proof {
		t.Fatalf("persisted plan: %+v, %v", plan, err)
	}
	movements, err := service.Movements(ctx, human(), b.Key)
	if err != nil || len(movements) != 2 || movements[1].ToState != domain.Ready {
		t.Fatalf("dependent movements: %+v, %v", movements, err)
	}
	if _, _, err := service.Acquire(ctx, b.Key, agent()); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyPlanOwnership(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "First"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Next"})
	c, _ := createTask(t, service, app.CreateParams{ProjectKey: "PO", Title: "Different project"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}
	_, err := repo.ApprovePlan(ctx, human(), a.Key, c.Key, policy, testNow)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
	if _, err = repo.ApprovePlan(ctx, human(), a.Key, b.Key, policy, testNow); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ApprovePlan(ctx, human(), b.Key, a.Key, policy, testNow)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
	policy.Branch = "other"
	_, err = repo.ApprovePlan(ctx, human(), a.Key, b.Key, policy, testNow)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
}

func TestDependencyPlanCancellation(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})
	a, _ := createTask(t, service, app.CreateParams{Title: "Create a project"})
	b, _ := createTask(t, service, app.CreateParams{Title: "Rename a project"})
	policy := app.PlanPolicy{Repository: "owner/project", Branch: "main", WorkflowID: 72}
	if _, err := repo.ApprovePlan(ctx, human(), a.Key, b.Key, policy, testNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Acquire(ctx, a.Key, agent()); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	prURL, label := "https://github.com/owner/project/pull/72", "head_sha"
	_, delivery, err := service.DeliverItems(ctx, a.Key, agent(), nil, "Working implementation",
		[]domain.EvidenceItem{{Label: &label, Raw: head}}, &prURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CancelPlan(ctx, agent(), a.Key, testNow)
	wantRuleCode(t, err, domain.CodeForbidden)
	plan, err := repo.CancelPlan(ctx, human(), a.Key, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CancelledAt != testNow || plan.CancelledBy != human().ID {
		t.Fatalf("cancellation was not recorded: %+v", plan)
	}
	if plans, err := repo.PendingPlans(ctx); err != nil || len(plans) != 0 {
		t.Fatalf("cancelled plan is still pending: %+v, %v", plans, err)
	}
	proof := app.MergeProof{DeliveryID: delivery.ID, PRURL: prURL, HeadSHA: head,
		MergeSHA: strings.Repeat("b", 40), RunID: 9, Attempt: 1}
	_, err = repo.ReleasePlan(ctx, a.Key, proof, testNow)
	wantRuleCode(t, err, domain.CodeInvalidTransition)
	movements, err := service.Movements(ctx, human(), b.Key)
	if err != nil || len(movements) != 1 {
		t.Fatalf("the dependent task left backlog after the cancellation: %+v, %v", movements, err)
	}
	if _, err := repo.CancelPlan(ctx, human(), a.Key, testNow); err != nil {
		t.Fatalf("repeated cancellation: %v", err)
	}
	if _, err := repo.ApprovePlan(ctx, human(), a.Key, b.Key, policy, testNow); err == nil {
		t.Fatal("a cancelled plan was approved again")
	}
	if _, err := service.Move(ctx, b.Key, domain.Ready, "", human(), movements[0].TaskVersion, true); err != nil {
		t.Fatalf("the dependent task is not manageable after the cancellation: %v", err)
	}
}
