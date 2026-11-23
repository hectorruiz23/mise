package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type fakeChecks map[string][]domain.Check

func (f fakeChecks) Checks(_ context.Context, _, revision string) ([]domain.Check, error) {
	return f[revision], nil
}

type brokenChecks struct{ err error }

func (b brokenChecks) Checks(context.Context, string, string) ([]domain.Check, error) {
	return nil, b.err
}

func green(names ...string) []domain.Check {
	checks := make([]domain.Check, 0, len(names))
	for _, name := range names {
		checks = append(checks, domain.Check{Name: name, Status: "completed", Conclusion: "success"})
	}
	return checks
}

func red(name string) domain.Check {
	return domain.Check{Name: name, Status: "completed", Conclusion: "failure"}
}

func TestSubmissionGate(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	first := strings.Repeat("1", 40)
	second := strings.Repeat("2", 40)
	cancelled := strings.Repeat("3", 40)
	checks := fakeChecks{
		first:     {red("build"), red("unit"), red("lint")},
		second:    {red("build"), red("unit")},
		cancelled: {{Name: "build", Status: "completed", Conclusion: "cancelled"}},
	}
	service := app.NewWithChecks(repo, &manualClock{now: testNow}, checks)
	if err := service.UpdateProjectChecks(ctx, human(), "MI", []string{"build", "unit", "lint"}); err != nil {
		t.Fatal(err)
	}
	task, _ := createTask(t, service, app.CreateParams{Title: "Ship the endpoint"})
	prURL := "https://github.com/owner/project/pull/12"
	label := "check"
	items := []domain.EvidenceItem{{Label: &label, Raw: "tests run locally"}}
	deliver := func(head string) error {
		t.Helper()
		_, _, err := service.DeliverRevision(ctx, task.Key, agent(), nil, "Work done", nil, items, &prURL, &head)
		return err
	}
	if _, err := service.Move(ctx, task.Key, domain.Ready, "", human(), task.Version, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Acquire(ctx, task.Key, agent()); err != nil {
		t.Fatal(err)
	}

	declared, err := service.DeclareSubmission(ctx, agent(), task.Key, &prURL, strings.Repeat("4", 40))
	if err != nil {
		t.Fatalf("declare a pending revision: %v", err)
	}
	if declared.Verdict != domain.VerdictOpen {
		t.Fatalf("declared verdict = %q, want open", declared.Verdict)
	}

	err = deliver(cancelled)
	wantRuleCode(t, err, domain.CodeChecksPending)
	if state := mustTask(t, service, task.Key).State; state != domain.InProgress {
		t.Fatalf("state after an inconclusive check = %q, want in_progress", state)
	}

	err = deliver(first)
	wantRuleCode(t, err, domain.CodeChecksFailed)
	card := mustTask(t, service, task.Key)
	if card.State != domain.InProgress {
		t.Fatalf("state after the first failure = %q, want in_progress", card.State)
	}
	detail, err := service.Detail(ctx, human(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Deliveries) != 1 || detail.Deliveries[0].HeadSHA == nil {
		t.Fatalf("the failed attempt left no delivery with its revision: %+v", detail.Deliveries)
	}
	failures, err := repo.CountFailedAttempts(ctx, task.Key)
	if err != nil || failures != 1 {
		t.Fatalf("failed attempts = %d, %v; want 1", failures, err)
	}

	err = deliver(first)
	wantRuleCode(t, err, domain.CodeChecksFailed)
	if failures, err := repo.CountFailedAttempts(ctx, task.Key); err != nil || failures != 1 {
		t.Fatalf("a duplicate submission consumed an attempt: %d, %v", failures, err)
	}

	err = deliver(second)
	wantRuleCode(t, err, domain.CodeChecksFailed)
	card = mustTask(t, service, task.Key)
	if card.State != domain.Blocked {
		t.Fatalf("state after the second failure = %q, want blocked", card.State)
	}
	blocked, err := service.Detail(ctx, human(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.Submissions) != 4 {
		t.Fatalf("submissions = %d, want 4", len(blocked.Submissions))
	}
}

func TestSubmissionGatePasses(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	head := strings.Repeat("a", 40)
	service := app.NewWithChecks(repo, &manualClock{now: testNow},
		fakeChecks{head: green("build", "unit")})
	if err := service.UpdateProjectChecks(ctx, human(), "MI", []string{"build", "unit"}); err != nil {
		t.Fatal(err)
	}
	task, _ := createTask(t, service, app.CreateParams{Title: "Ship the endpoint"})
	if _, err := service.Move(ctx, task.Key, domain.Ready, "", human(), task.Version, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Acquire(ctx, task.Key, agent()); err != nil {
		t.Fatal(err)
	}
	prURL := "https://github.com/owner/project/pull/13"
	label := "check"
	delivered, delivery, err := service.DeliverRevision(ctx, task.Key, agent(), nil, "Work done", nil,
		[]domain.EvidenceItem{{Label: &label, Raw: "tests run locally"}}, &prURL, &head)
	if err != nil {
		t.Fatalf("deliver a green revision: %v", err)
	}
	if delivered.State != domain.Review {
		t.Fatalf("state = %q, want review", delivered.State)
	}
	if delivery.HeadSHA == nil || *delivery.HeadSHA != head {
		t.Fatalf("delivery head_sha = %v, want %s", delivery.HeadSHA, head)
	}
	failures, err := repo.CountFailedAttempts(ctx, task.Key)
	if err != nil || failures != 0 {
		t.Fatalf("failed attempts = %d, %v; want 0", failures, err)
	}
}

func TestUnavailableCheckProviderCannotAdvanceADelivery(t *testing.T) {
	ctx := context.Background()
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	clock := &manualClock{now: testNow}
	head := strings.Repeat("b", 40)
	outage := errors.New("CI temporarily unavailable")

	service := app.NewWithChecks(repo, clock, brokenChecks{err: outage})
	if err := service.UpdateProjectChecks(ctx, human(), "MI", []string{"build"}); err != nil {
		t.Fatal(err)
	}
	task, _ := createTask(t, service, app.CreateParams{Title: "Review check"})
	if _, err := service.Move(ctx, task.Key, domain.Ready, "", human(), task.Version, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Acquire(ctx, task.Key, agent()); err != nil {
		t.Fatal(err)
	}

	prURL := "https://github.com/owner/project/pull/21"
	label := "check"
	items := []domain.EvidenceItem{{Label: &label, Raw: "tests run locally"}}
	if _, _, err := service.DeliverRevision(ctx, task.Key, agent(), nil, "Work done", nil, items, &prURL, &head); err == nil {
		t.Fatal("an unavailable provider let the delivery through")
	} else {
		wantRuleCode(t, err, domain.CodeChecksPending)
	}

	if state := mustTask(t, service, task.Key).State; state != domain.InProgress {
		t.Fatalf("state after an unverifiable delivery = %q, want in_progress", state)
	}
	if failures, err := repo.CountFailedAttempts(ctx, task.Key); err != nil || failures != 0 {
		t.Fatalf("failed attempts = %d, %v; want 0", failures, err)
	}
	candidates, err := repo.PendingMerges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("an unavailable provider produced %d merge candidates", len(candidates))
	}
	detail, err := service.Detail(ctx, human(), task.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Deliveries) != 0 {
		t.Fatalf("deliveries = %d, want 0 for an unverifiable revision", len(detail.Deliveries))
	}
	if len(detail.Submissions) != 1 {
		t.Fatalf("submissions = %d, want 1 kept attempt", len(detail.Submissions))
	}
	if verdict := detail.Submissions[0].Verdict; verdict != domain.VerdictOpen {
		t.Fatalf("kept submission verdict = %q, want open", verdict)
	}
	if detail.Submissions[0].CheckedAt != nil {
		t.Fatalf("an unavailable provider stamped checked_at = %d", *detail.Submissions[0].CheckedAt)
	}

	recovered := app.NewWithChecks(repo, clock, fakeChecks{head: green("build")})
	_, delivery, err := recovered.DeliverRevision(ctx, task.Key, agent(), nil, "Work done", nil, items, &prURL, &head)
	if err != nil {
		t.Fatalf("recover the same revision once the provider answers: %v", err)
	}
	if delivery.HeadSHA == nil || *delivery.HeadSHA != head {
		t.Fatalf("delivery head_sha = %v, want %s", delivery.HeadSHA, head)
	}
	if state := mustTask(t, recovered, task.Key).State; state != domain.Review {
		t.Fatalf("state after recovery = %q, want review", state)
	}
	if failures, err := repo.CountFailedAttempts(ctx, task.Key); err != nil || failures != 0 {
		t.Fatalf("failed attempts after recovery = %d, %v; want 0", failures, err)
	}
	candidates, err = repo.PendingMerges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("merge candidates after recovery = %d, want 1", len(candidates))
	}
}

func mustTask(t *testing.T, service *app.Service, key string) domain.Task {
	t.Helper()
	detail, err := service.Detail(context.Background(), human(), key)
	if err != nil {
		t.Fatalf("detail %s: %v", key, err)
	}
	return detail.Task
}
