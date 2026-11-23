package sqlite_test

import (
	"context"
	"testing"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func TestDispatchBudget(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)
	if err := service.UpdateProjectDispatch(ctx, human(), "MI", true, 2); err != nil {
		t.Fatal(err)
	}
	task, _ := createTask(t, service, app.CreateParams{Title: "Ship the endpoint"})
	if _, err := service.Move(ctx, task.Key, domain.Ready, "", human(), task.Version, true); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := service.Acquire(ctx, task.Key, agent()); err != nil {
			t.Fatalf("dispatch within budget: %v", err)
		}
		clock.now += app.LeaseTTLSeconds + 1
	}
	_, _, err := service.Acquire(ctx, task.Key, agent())
	wantRuleCode(t, err, domain.CodeDispatchExhausted)

	if _, err := service.Move(ctx, task.Key, domain.Blocked, "two dead dispatches", human(),
		mustVersion(t, service, task.Key), true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Move(ctx, task.Key, domain.InProgress, "reopened", human(),
		mustVersion(t, service, task.Key), true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Acquire(ctx, task.Key, agent()); err != nil {
		t.Fatalf("a human reopen does not grant a dispatch: %v", err)
	}
	if _, _, err := service.Acquire(ctx, task.Key, agent()); err == nil {
		t.Fatal("the extra dispatch was granted twice")
	}
}

func TestDispatchBudgetIsOptIn(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)
	task, _ := createTask(t, service, app.CreateParams{Title: "Ship the endpoint"})
	if _, err := service.Move(ctx, task.Key, domain.Ready, "", human(), task.Version, true); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, _, err := service.Acquire(ctx, task.Key, agent()); err != nil {
			t.Fatalf("a project without auto_dispatch was capped: %v", err)
		}
		clock.now += app.LeaseTTLSeconds + 1
	}
}

func mustVersion(t *testing.T, service *app.Service, key string) int64 {
	t.Helper()
	detail, err := service.Detail(context.Background(), human(), key)
	if err != nil {
		t.Fatal(err)
	}
	return detail.Task.Version
}
