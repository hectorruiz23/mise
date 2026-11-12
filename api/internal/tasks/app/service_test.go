package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type fakeClock int64

func (c fakeClock) Now() int64 { return int64(c) }

type fakeRepo struct {
	task       domain.Task
	getErr     error
	deliveries int
	countErr   error

	countCalled bool
	updateErr   error
	updateArgs  struct {
		called   bool
		task     domain.Task
		movement domain.Movement
		expected int64
	}

	createArgs struct {
		called bool
		params app.CreateParams
	}
	createResult   domain.Task
	createReplayed bool
	createErr      error

	listArgs  app.Filter
	listTasks []domain.Task
	listNext  string
	listErr   error

	liveLease *domain.Lease
	liveErr   error

	acquireArgs struct {
		called   bool
		task     domain.Task
		lease    domain.Lease
		movement domain.Movement
		expected int64
	}
	acquireErr error

	renewArgs struct {
		called    bool
		leaseID   int64
		agentID   int64
		expiresAt int64
	}
	renewErr error

	releaseArgs struct {
		called  bool
		leaseID int64
		agentID int64
		at      int64
		reason  string
	}
	releaseErr error

	deliverArgs struct {
		called   bool
		task     domain.Task
		delivery domain.Delivery
		movement domain.Movement
		expected int64
	}
	deliverErr error

	listLeases        []domain.Lease
	listLeasesErr     error
	listDeliveries    []domain.Delivery
	listDeliveriesErr error
	listMovements     []domain.Movement
	listMovementsErr  error
	listComments      []domain.Comment
	listCommentsErr   error
}

func (r *fakeRepo) CreateTask(_ context.Context, p app.CreateParams) (domain.Task, bool, error) {
	r.createArgs.called = true
	r.createArgs.params = p
	return r.createResult, r.createReplayed, r.createErr
}

func (r *fakeRepo) GetTask(_ context.Context, _ string) (domain.Task, error) {
	if r.getErr != nil {
		return domain.Task{}, r.getErr
	}
	return r.task, nil
}

func (r *fakeRepo) ListTasks(_ context.Context, f app.Filter) ([]domain.Task, string, error) {
	r.listArgs = f
	return r.listTasks, r.listNext, r.listErr
}

func (r *fakeRepo) ListProjects(_ context.Context) ([]app.Project, error) {
	return nil, nil
}

func (r *fakeRepo) CreateProject(_ context.Context, _ app.Project, _ int64) error {
	return nil
}

func (r *fakeRepo) ListBoardTasks(_ context.Context, _ string) ([]domain.Task, error) {
	return nil, nil
}

func (r *fakeRepo) UpdateTask(_ context.Context, t domain.Task, m domain.Movement, expected int64) error {
	r.updateArgs.called = true
	r.updateArgs.task = t
	r.updateArgs.movement = m
	r.updateArgs.expected = expected
	return r.updateErr
}

func (r *fakeRepo) CountDeliveries(_ context.Context, _ string) (int, error) {
	r.countCalled = true
	return r.deliveries, r.countErr
}

func (r *fakeRepo) GetLiveLease(_ context.Context, _ string, _ int64) (*domain.Lease, error) {
	if r.liveErr != nil {
		return nil, r.liveErr
	}
	return r.liveLease, nil
}

func (r *fakeRepo) AcquireLease(_ context.Context, t domain.Task, l domain.Lease, m domain.Movement, expected int64) error {
	r.acquireArgs.called = true
	r.acquireArgs.task = t
	r.acquireArgs.lease = l
	r.acquireArgs.movement = m
	r.acquireArgs.expected = expected
	if r.acquireErr != nil {
		return r.acquireErr
	}
	stored := l
	stored.ID = 9
	r.liveLease = &stored
	return nil
}

func (r *fakeRepo) RenewLease(_ context.Context, leaseID, agentID, expiresAt int64) error {
	r.renewArgs.called = true
	r.renewArgs.leaseID = leaseID
	r.renewArgs.agentID = agentID
	r.renewArgs.expiresAt = expiresAt
	return r.renewErr
}

func (r *fakeRepo) ReleaseLease(_ context.Context, leaseID, agentID, at int64, reason string) error {
	r.releaseArgs.called = true
	r.releaseArgs.leaseID = leaseID
	r.releaseArgs.agentID = agentID
	r.releaseArgs.at = at
	r.releaseArgs.reason = reason
	return r.releaseErr
}

func (r *fakeRepo) CloseLiveLease(_ context.Context, _ string, _ int64, _ string) (bool, error) {
	return false, nil
}

func (r *fakeRepo) Deliver(_ context.Context, t domain.Task, d domain.Delivery, m domain.Movement, expected int64) error {
	r.deliverArgs.called = true
	r.deliverArgs.task = t
	r.deliverArgs.delivery = d
	r.deliverArgs.movement = m
	r.deliverArgs.expected = expected
	if r.deliverErr != nil {
		return r.deliverErr
	}
	d.ID = int64(len(r.listDeliveries) + 1)
	r.listDeliveries = append(r.listDeliveries, d)
	return nil
}

func (r *fakeRepo) ListLeases(_ context.Context, _ string) ([]domain.Lease, error) {
	return r.listLeases, r.listLeasesErr
}

func (r *fakeRepo) ListDeliveries(_ context.Context, _ string) ([]domain.Delivery, error) {
	return r.listDeliveries, r.listDeliveriesErr
}

func (r *fakeRepo) ListMovements(_ context.Context, _ string) ([]domain.Movement, error) {
	return r.listMovements, r.listMovementsErr
}

func (r *fakeRepo) CreateComment(_ context.Context, c domain.Comment) (domain.Comment, error) {
	c.ID = int64(len(r.listComments) + 1)
	r.listComments = append(r.listComments, c)
	return c, nil
}

func (r *fakeRepo) ListComments(_ context.Context, _ string) ([]domain.Comment, error) {
	return r.listComments, r.listCommentsErr
}

func human() domain.Actor {
	return domain.Actor{ID: 1, Kind: domain.Human, Name: "Human"}
}

func agent() domain.Actor {
	return domain.Actor{ID: 2, Kind: domain.Agent, Name: "Worker"}
}

func otherAgent() domain.Actor {
	return domain.Actor{ID: 3, Kind: domain.Agent, Name: "Other"}
}

func ruleCode(t *testing.T, err error) string {
	t.Helper()
	var rule *domain.RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want *domain.RuleError", err)
	}
	return rule.Code
}

func TestCreateRejectsAgents(t *testing.T) {
	repo := &fakeRepo{}
	service := app.New(repo, fakeClock(100))

	_, _, err := service.Create(context.Background(), app.CreateParams{
		ProjectKey: "MI",
		Title:      "Agent task",
		Actor:      agent(),
	})

	if code := ruleCode(t, err); code != domain.CodeForbidden {
		t.Fatalf("code = %q, want %q", code, domain.CodeForbidden)
	}
	if repo.createArgs.called {
		t.Fatal("repository was called for an agent")
	}
}

func TestCreateValidatesTitle(t *testing.T) {
	for _, tt := range []struct {
		name  string
		title string
	}{
		{name: "blank", title: "   "},
		{name: "too long", title: strings.Repeat("a", 201)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{}
			service := app.New(repo, fakeClock(100))

			_, _, err := service.Create(context.Background(), app.CreateParams{
				ProjectKey: "MI",
				Title:      tt.title,
				Actor:      human(),
			})

			if code := ruleCode(t, err); code != domain.CodeInvalidInput {
				t.Fatalf("code = %q, want %q", code, domain.CodeInvalidInput)
			}
			if repo.createArgs.called {
				t.Fatal("repository was called with an invalid title")
			}
		})
	}
}

func TestCreateFillsClockAndDefaults(t *testing.T) {
	blank := "   "
	repo := &fakeRepo{
		createResult:   domain.Task{Key: "MI-1", Title: "Ship it"},
		createReplayed: true,
	}
	service := app.New(repo, fakeClock(1700))

	task, replayed, err := service.Create(context.Background(), app.CreateParams{
		ProjectKey: "MI",
		Title:      "Ship it",
		Notes:      &blank,
		Actor:      human(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.Key != "MI-1" || !replayed {
		t.Fatalf("task = %+v, replayed = %v", task, replayed)
	}
	params := repo.createArgs.params
	if params.Now != 1700 {
		t.Errorf("Now = %d, want 1700", params.Now)
	}
	if params.Priority != domain.Normal {
		t.Errorf("Priority = %q, want normal", params.Priority)
	}
	if params.Notes != nil {
		t.Errorf("Notes = %v, want nil for blank text", *params.Notes)
	}
	if params.Actor.ID != 1 {
		t.Errorf("Actor.ID = %d, want 1", params.Actor.ID)
	}
}

func TestMoveToReviewRequiresDelivery(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{
			Key:     "MI-1",
			Title:   "Ship it",
			State:   domain.InProgress,
			Version: 4,
		},
		deliveries: 0,
	}
	service := app.New(repo, fakeClock(1700))

	_, err := service.Move(
		context.Background(), "MI-1", domain.Review, "", human(), 4, true)

	if code := ruleCode(t, err); code != domain.CodeEvidenceRequired {
		t.Fatalf("code = %q, want %q", code, domain.CodeEvidenceRequired)
	}
	if repo.updateArgs.called {
		t.Fatal("repository update was called after a rejected move")
	}
}

func TestMoveStaleIfMatchDoesNotCountDeliveries(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{
			Key:     "MI-1",
			Title:   "Ship it",
			State:   domain.InProgress,
			Version: 4,
		},
	}
	service := app.New(repo, fakeClock(1700))

	_, err := service.Move(
		context.Background(), "MI-1", domain.Ready, "", human(), 3, true)

	if code := ruleCode(t, err); code != domain.CodeStaleVersion {
		t.Fatalf("code = %q, want %q", code, domain.CodeStaleVersion)
	}
	if repo.countCalled {
		t.Fatal("deliveries were counted after a stale If-Match")
	}
	if repo.updateArgs.called {
		t.Fatal("repository update was called after a stale If-Match")
	}
}

func TestMoveAppliesDomainRulesOnce(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{
			Key:     "MI-1",
			Title:   "Ship it",
			State:   domain.InProgress,
			Version: 4,
		},
		deliveries: 0,
	}
	service := app.New(repo, fakeClock(1700))

	next, err := service.Move(
		context.Background(), "MI-1", domain.Ready, "", human(), 4, true)
	if err != nil {
		t.Fatalf("Move: %v", err)
	}

	if next.Version != 5 || next.State != domain.Ready {
		t.Fatalf("next = %+v, want ready at version 5", next)
	}
	if !repo.updateArgs.called {
		t.Fatal("repository update was not called")
	}
	if repo.updateArgs.expected != 4 {
		t.Errorf("expected version = %d, want 4", repo.updateArgs.expected)
	}
	if got := repo.updateArgs.movement; got.TaskVersion != 5 ||
		got.FromState == nil || *got.FromState != domain.InProgress ||
		got.ToState != domain.Ready {
		t.Errorf("movement = %+v, want in_progress -> ready at version 5", got)
	}
}

func TestMoveReturnsInfrastructureError(t *testing.T) {
	wantErr := errors.New("database is gone")
	repo := &fakeRepo{
		task: domain.Task{
			Key:     "MI-1",
			Title:   "Ship it",
			State:   domain.InProgress,
			Version: 1,
		},
		countErr: wantErr,
	}
	service := app.New(repo, fakeClock(1700))

	_, err := service.Move(
		context.Background(), "MI-1", domain.Ready, "", human(), 1, true)

	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestListDelegatesToRepo(t *testing.T) {
	repo := &fakeRepo{
		listTasks: []domain.Task{{Key: "MI-1"}},
		listNext:  "next",
	}
	service := app.New(repo, fakeClock(1700))
	filter := app.Filter{ProjectKey: "MI", State: domain.Backlog, Limit: 10}

	tasks, next, err := service.List(context.Background(), filter)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Key != "MI-1" || next != "next" {
		t.Fatalf("tasks = %+v, next = %q", tasks, next)
	}
	if repo.listArgs != filter {
		t.Errorf("filter = %+v, want %+v", repo.listArgs, filter)
	}
}

func int64Ptr(value int64) *int64 { return &value }

func TestAcquireTakesLeaseFromReady(t *testing.T) {
	repo := &fakeRepo{task: domain.Task{
		Key: "MI-1", Title: "Ship it", State: domain.Ready, Version: 3,
	}}
	service := app.New(repo, fakeClock(1700))

	task, lease, err := service.Acquire(context.Background(), "MI-1", agent())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if task.State != domain.InProgress || task.Version != 4 {
		t.Fatalf("task = %+v, want in_progress at version 4", task)
	}
	if lease.ID != 9 || lease.AgentID != agent().ID ||
		lease.ExpiresAt != 1700+app.LeaseTTLSeconds {
		t.Fatalf("lease = %+v, want stored id 9 expiring 900 s after now", lease)
	}
	if !repo.acquireArgs.called {
		t.Fatal("AcquireLease was not called")
	}
	if repo.acquireArgs.expected != 3 {
		t.Errorf("expected version = %d, want 3", repo.acquireArgs.expected)
	}
	if got := repo.acquireArgs.movement; got.FromState == nil ||
		*got.FromState != domain.Ready || got.ToState != domain.InProgress {
		t.Errorf("movement = %+v, want ready -> in_progress", got)
	}
}

func TestAcquireRejectsLiveLease(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{Key: "MI-1", Title: "Ship it", State: domain.Ready, Version: 3},
		liveLease: &domain.Lease{
			ID: 3, TaskKey: "MI-1", AgentID: agent().ID, ExpiresAt: 2000,
		},
	}
	service := app.New(repo, fakeClock(1700))

	_, _, err := service.Acquire(context.Background(), "MI-1", agent())
	if code := ruleCode(t, err); code != domain.CodeLeaseConflict {
		t.Fatalf("code = %q, want %q", code, domain.CodeLeaseConflict)
	}
	if repo.acquireArgs.called {
		t.Fatal("AcquireLease was called for a live lease")
	}
}

func TestRenewOnlyByOwnerAndOnlyWhenLive(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{Key: "MI-1", Title: "Ship it", State: domain.InProgress, Version: 4},
		liveLease: &domain.Lease{
			ID: 5, TaskKey: "MI-1", AgentID: agent().ID, ExpiresAt: 2000,
		},
	}
	service := app.New(repo, fakeClock(1700))

	_, err := service.Renew(context.Background(), "MI-1", 5, otherAgent())
	if code := ruleCode(t, err); code != domain.CodeLeaseConflict {
		t.Fatalf("foreign renew code = %q, want %q", code, domain.CodeLeaseConflict)
	}
	if repo.renewArgs.called {
		t.Fatal("RenewLease was called for a foreign lease")
	}

	renewed, err := service.Renew(context.Background(), "MI-1", 5, agent())
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if renewed.ExpiresAt != 1700+app.LeaseTTLSeconds {
		t.Fatalf("expires_at = %d, want %d", renewed.ExpiresAt, 1700+app.LeaseTTLSeconds)
	}
	if !repo.renewArgs.called || repo.renewArgs.expiresAt != 1700+app.LeaseTTLSeconds {
		t.Fatalf("renew args = %+v", repo.renewArgs)
	}
	if repo.updateArgs.called {
		t.Fatal("renewal changed the task")
	}

	repo.liveLease = nil
	_, err = service.Renew(context.Background(), "MI-1", 5, agent())
	if code := ruleCode(t, err); code != domain.CodeLeaseConflict {
		t.Fatalf("expired renew code = %q, want %q", code, domain.CodeLeaseConflict)
	}
}

func TestReleaseOnlyByOwner(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{Key: "MI-1", Title: "Ship it", State: domain.InProgress, Version: 4},
		liveLease: &domain.Lease{
			ID: 5, TaskKey: "MI-1", AgentID: agent().ID, ExpiresAt: 2000,
		},
	}
	service := app.New(repo, fakeClock(1700))

	err := service.Release(context.Background(), "MI-1", 5, otherAgent())
	if code := ruleCode(t, err); code != domain.CodeLeaseConflict {
		t.Fatalf("foreign release code = %q, want %q", code, domain.CodeLeaseConflict)
	}
	if repo.releaseArgs.called {
		t.Fatal("ReleaseLease was called for a foreign lease")
	}

	if err := service.Release(context.Background(), "MI-1", 5, agent()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if !repo.releaseArgs.called || repo.releaseArgs.at != 1700 ||
		repo.releaseArgs.reason != "released" {
		t.Fatalf("release args = %+v", repo.releaseArgs)
	}
	if repo.updateArgs.called {
		t.Fatal("release changed the task")
	}
}

func TestDeliverAgentWithLiveLease(t *testing.T) {
	repo := &fakeRepo{
		task: domain.Task{Key: "MI-1", Title: "Ship it", State: domain.InProgress, Version: 4},
		liveLease: &domain.Lease{
			ID: 5, TaskKey: "MI-1", AgentID: agent().ID, ExpiresAt: 2000,
		},
	}
	service := app.New(repo, fakeClock(1700))

	task, delivery, err := service.Deliver(
		context.Background(), "MI-1", agent(), int64Ptr(5), "Ready", []string{"go test ./..."})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if task.State != domain.Review || task.Version != 5 {
		t.Fatalf("task = %+v, want review at version 5", task)
	}
	if delivery.ID != 1 || delivery.LeaseID == nil || *delivery.LeaseID != 5 {
		t.Fatalf("delivery = %+v, want id 1 with lease 5", delivery)
	}
	if !repo.deliverArgs.called || repo.deliverArgs.expected != 4 {
		t.Fatalf("deliver args = %+v", repo.deliverArgs)
	}
	if got := repo.deliverArgs.movement; got.ToState != domain.Review {
		t.Errorf("movement = %+v, want -> review", got)
	}
}

func TestDeliverAgentRequiresLiveOwnedLease(t *testing.T) {
	for _, tt := range []struct {
		name    string
		live    *domain.Lease
		leaseID *int64
	}{
		{name: "no live lease"},
		{name: "supplied other lease", leaseID: int64Ptr(77)},
		{name: "another agent's lease", live: &domain.Lease{
			ID: 5, TaskKey: "MI-1", AgentID: otherAgent().ID, ExpiresAt: 2000,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{
				task: domain.Task{
					Key: "MI-1", Title: "Ship it", State: domain.InProgress, Version: 4,
				},
				liveLease: tt.live,
			}
			service := app.New(repo, fakeClock(1700))

			_, _, err := service.Deliver(
				context.Background(), "MI-1", agent(), tt.leaseID, "Ready", []string{"proof"})
			if code := ruleCode(t, err); code != domain.CodeLeaseRequired {
				t.Fatalf("code = %q, want %q", code, domain.CodeLeaseRequired)
			}
			if repo.deliverArgs.called {
				t.Fatal("Deliver was called without a live owned lease")
			}
		})
	}
}

func TestDeliverHumanWithoutLease(t *testing.T) {
	repo := &fakeRepo{task: domain.Task{
		Key: "MI-1", Title: "Ship it", State: domain.InProgress, Version: 4,
	}}
	service := app.New(repo, fakeClock(1700))

	task, delivery, err := service.Deliver(
		context.Background(), "MI-1", human(), nil, "Done by hand", []string{"manual check"})
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if task.State != domain.Review {
		t.Fatalf("task state = %q, want review", task.State)
	}
	if delivery.LeaseID != nil {
		t.Fatalf("lease_id = %v, want nil", *delivery.LeaseID)
	}
}
