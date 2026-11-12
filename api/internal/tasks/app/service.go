package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"mise/api/internal/tasks/domain"
)

const LeaseTTLSeconds int64 = 900

const CodeIdempotencyConflict = "idempotency_conflict"

const CodeNotFound = domain.CodeNotFound

type Repo interface {
	CreateTask(ctx context.Context, params CreateParams) (domain.Task, bool, error)
	GetTask(ctx context.Context, key string) (domain.Task, error)
	ListTasks(ctx context.Context, f Filter) ([]domain.Task, string, error)
	ListProjects(ctx context.Context) ([]Project, error)
	ListBoardTasks(ctx context.Context, projectKey string) ([]domain.Task, error)
	UpdateTask(ctx context.Context, t domain.Task, m domain.Movement, expectedVersion int64) error
	CountDeliveries(ctx context.Context, taskKey string) (int, error)

	GetLiveLease(ctx context.Context, taskKey string, now int64) (*domain.Lease, error)
	AcquireLease(ctx context.Context, t domain.Task, l domain.Lease, m domain.Movement, expectedVersion int64) error
	RenewLease(ctx context.Context, leaseID int64, agentID int64, expiresAt int64) error
	ReleaseLease(ctx context.Context, leaseID int64, agentID int64, at int64, reason string) error
	CloseLiveLease(ctx context.Context, taskKey string, at int64, reason string) (bool, error)
	Deliver(ctx context.Context, t domain.Task, d domain.Delivery, m domain.Movement, expectedVersion int64) error
	ListLeases(ctx context.Context, taskKey string) ([]domain.Lease, error)
	ListDeliveries(ctx context.Context, taskKey string) ([]domain.Delivery, error)
	ListMovements(ctx context.Context, taskKey string) ([]domain.Movement, error)
	CreateComment(ctx context.Context, c domain.Comment) (domain.Comment, error)
	ListComments(ctx context.Context, taskKey string) ([]domain.Comment, error)
}

type Clock interface{ Now() int64 }

type CreateParams struct {
	ProjectKey     string
	Title          string
	Notes          *string
	Acceptance     *string
	Priority       domain.Priority
	Size           *domain.Size
	Actor          domain.Actor
	IdempotencyKey string
	Digest         string
	Now            int64
}

type Patch struct {
	Title      *string
	Notes      *string
	NotesSet   bool
	Acceptance *string
	AcceptSet  bool
	Priority   *domain.Priority
	Size       *domain.Size
	SizeSet    bool
	State      *domain.State
	Reason     string
}

func (p Patch) HasFieldEdits() bool {
	return p.Title != nil || p.NotesSet || p.AcceptSet || p.Priority != nil || p.SizeSet
}

type Filter struct {
	ProjectKey string
	State      domain.State
	Limit      int
	Cursor     string
}

type Project struct {
	Key  string
	Name string
}

type Board struct {
	Columns     map[domain.State][]domain.Task
	Counts      map[domain.State]int
	GeneratedAt int64
}

type Detail struct {
	Task       domain.Task
	Movements  []domain.Movement
	Leases     []domain.Lease
	Deliveries []domain.Delivery
	Comments   []domain.Comment
}

type Service struct {
	repo  Repo
	clock Clock
}

func New(repo Repo, clock Clock) *Service {
	return &Service{repo: repo, clock: clock}
}

func (s *Service) Create(ctx context.Context, p CreateParams) (domain.Task, bool, error) {
	if p.Actor.Kind != domain.Human {
		return domain.Task{}, false, &domain.RuleError{
			Code: domain.CodeForbidden,
			Msg:  "only a human may create tasks",
		}
	}
	if strings.TrimSpace(p.Title) == "" {
		return domain.Task{}, false, invalidInput("title is required")
	}
	if utf8.RuneCountInString(p.Title) > 200 {
		return domain.Task{}, false, invalidInput("title must be at most 200 characters")
	}
	if strings.TrimSpace(p.ProjectKey) == "" {
		return domain.Task{}, false, invalidInput("project is required")
	}

	priority := p.Priority
	if priority == "" {
		priority = domain.Normal
	}
	if !priority.Valid() {
		return domain.Task{}, false, invalidInput("unknown priority")
	}

	var size *domain.Size
	if p.Size != nil {
		value := *p.Size
		if !value.Valid() {
			return domain.Task{}, false, invalidInput("unknown size")
		}
		size = &value
	}

	params := p
	params.Notes = optionalText(p.Notes)
	params.Acceptance = optionalText(p.Acceptance)
	params.Priority = priority
	params.Size = size
	params.Now = s.clock.Now()

	return s.repo.CreateTask(ctx, params)
}

func (s *Service) Get(ctx context.Context, key string) (domain.Task, error) {
	return s.repo.GetTask(ctx, key)
}

func (s *Service) List(ctx context.Context, f Filter) ([]domain.Task, string, error) {
	return s.repo.ListTasks(ctx, f)
}

func (s *Service) ListProjects(ctx context.Context) ([]Project, error) {
	return s.repo.ListProjects(ctx)
}

func (s *Service) Board(ctx context.Context, projectKey string) (Board, error) {
	generatedAt := s.clock.Now()
	tasks, err := s.repo.ListBoardTasks(ctx, projectKey)
	if err != nil {
		return Board{}, err
	}

	columns := make(map[domain.State][]domain.Task, 6)
	counts := make(map[domain.State]int, 6)
	for _, state := range []domain.State{
		domain.Backlog, domain.Ready, domain.InProgress,
		domain.Blocked, domain.Review, domain.Done,
	} {
		columns[state] = make([]domain.Task, 0)
		counts[state] = 0
	}
	for _, task := range tasks {
		columns[task.State] = append(columns[task.State], task)
		counts[task.State]++
	}

	return Board{Columns: columns, Counts: counts, GeneratedAt: generatedAt}, nil
}

func (s *Service) Move(
	ctx context.Context,
	key string,
	to domain.State,
	reason string,
	actor domain.Actor,
	ifMatch int64,
	ifMatchPresent bool,
) (domain.Task, error) {
	t, err := s.repo.GetTask(ctx, key)
	if err != nil {
		return domain.Task{}, err
	}
	if err := domain.CheckVersion(t, ifMatch, ifMatchPresent); err != nil {
		return domain.Task{}, err
	}

	deliveries, err := s.repo.CountDeliveries(ctx, key)
	if err != nil {
		return domain.Task{}, err
	}

	next, movement, err := domain.ApplyMove(t, to, actor, reason, deliveries > 0, s.clock.Now())
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.UpdateTask(ctx, next, movement, t.Version); err != nil {
		return domain.Task{}, err
	}

	return next, nil
}

func (s *Service) Update(
	ctx context.Context,
	key string,
	p Patch,
	actor domain.Actor,
	ifMatch int64,
	ifMatchPresent bool,
) (domain.Task, error) {
	if actor.Kind == domain.Agent && (p.HasFieldEdits() || p.State == nil) {
		return domain.Task{}, &domain.RuleError{
			Code: domain.CodeForbidden,
			Msg:  "only a human may edit task fields",
		}
	}
	if actor.Kind != domain.Human && actor.Kind != domain.Agent {
		return domain.Task{}, &domain.RuleError{
			Code: domain.CodeForbidden,
			Msg:  "only a human may edit tasks",
		}
	}

	current, err := s.repo.GetTask(ctx, key)
	if err != nil {
		return domain.Task{}, err
	}
	if err := domain.CheckVersion(current, ifMatch, ifMatchPresent); err != nil {
		return domain.Task{}, err
	}

	next := current
	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" {
			return domain.Task{}, invalidInput("title is required")
		}
		if utf8.RuneCountInString(title) > 200 {
			return domain.Task{}, invalidInput("title must be at most 200 characters")
		}
		next.Title = title
	}
	if p.NotesSet {
		next.Notes = optionalText(p.Notes)
		if next.Notes != nil && utf8.RuneCountInString(*next.Notes) > 50000 {
			return domain.Task{}, invalidInput("notes must be at most 50000 characters")
		}
	}
	if p.AcceptSet {
		next.Acceptance = optionalText(p.Acceptance)
		if next.Acceptance != nil && utf8.RuneCountInString(*next.Acceptance) > 10000 {
			return domain.Task{}, invalidInput("acceptance must be at most 10000 characters")
		}
	}
	if p.Priority != nil {
		if !p.Priority.Valid() {
			return domain.Task{}, invalidInput("unknown priority")
		}
		next.Priority = *p.Priority
	}
	if p.SizeSet {
		if p.Size == nil {
			next.Size = nil
		} else {
			if !p.Size.Valid() {
				return domain.Task{}, invalidInput("unknown size")
			}
			value := *p.Size
			next.Size = &value
		}
	}

	now := s.clock.Now()
	if actor.Kind == domain.Agent {
		live, err := s.repo.GetLiveLease(ctx, key, now)
		if err != nil {
			return domain.Task{}, err
		}
		if live == nil || live.AgentID != actor.ID {
			return domain.Task{}, leaseRequired("an agent needs a live lease it owns")
		}
	}
	if p.State == nil {
		next.Version = current.Version + 1
		next.UpdatedAt = now
		if err := s.repo.UpdateTask(ctx, next, domain.Movement{}, current.Version); err != nil {
			return domain.Task{}, err
		}
		return next, nil
	}

	deliveries, err := s.repo.CountDeliveries(ctx, key)
	if err != nil {
		return domain.Task{}, err
	}
	moved, movement, err := domain.ApplyMove(next, *p.State, actor, p.Reason, deliveries > 0, now)
	if err != nil {
		return domain.Task{}, err
	}
	if err := s.repo.UpdateTask(ctx, moved, movement, current.Version); err != nil {
		return domain.Task{}, err
	}
	return moved, nil
}

func (s *Service) Acquire(ctx context.Context, key string, agent domain.Actor) (domain.Task, domain.Lease, error) {
	task, err := s.repo.GetTask(ctx, key)
	if err != nil {
		return domain.Task{}, domain.Lease{}, err
	}

	now := s.clock.Now()
	live, err := s.repo.GetLiveLease(ctx, key, now)
	if err != nil {
		return domain.Task{}, domain.Lease{}, err
	}

	next, lease, movement, err := domain.Acquire(task, live, agent, now, LeaseTTLSeconds)
	if err != nil {
		return domain.Task{}, domain.Lease{}, err
	}
	if err := s.repo.AcquireLease(ctx, next, lease, movement, task.Version); err != nil {
		return domain.Task{}, domain.Lease{}, err
	}

	stored, err := s.repo.GetLiveLease(ctx, key, now)
	if err != nil {
		return domain.Task{}, domain.Lease{}, err
	}
	if stored == nil {
		return domain.Task{}, domain.Lease{}, fmt.Errorf("acquire lease on %q: the lease was not stored", key)
	}
	return next, *stored, nil
}

func (s *Service) Renew(ctx context.Context, key string, leaseID int64, agent domain.Actor) (domain.Lease, error) {
	if agent.Kind != domain.Agent {
		return domain.Lease{}, forbiddenLease("only an agent may renew a lease")
	}
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return domain.Lease{}, err
	}

	now := s.clock.Now()
	live, err := s.repo.GetLiveLease(ctx, key, now)
	if err != nil {
		return domain.Lease{}, err
	}
	if live == nil || live.ID != leaseID || live.AgentID != agent.ID {
		return domain.Lease{}, leaseConflict("the lease is not a live lease owned by this agent")
	}

	expiresAt := now + LeaseTTLSeconds
	if err := s.repo.RenewLease(ctx, leaseID, agent.ID, expiresAt); err != nil {
		return domain.Lease{}, err
	}

	renewed := *live
	renewed.ExpiresAt = expiresAt
	return renewed, nil
}

func (s *Service) Release(ctx context.Context, key string, leaseID int64, agent domain.Actor) error {
	if agent.Kind != domain.Agent {
		return forbiddenLease("only an agent may release a lease")
	}
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return err
	}

	now := s.clock.Now()
	live, err := s.repo.GetLiveLease(ctx, key, now)
	if err != nil {
		return err
	}
	if live == nil || live.ID != leaseID || live.AgentID != agent.ID {
		return leaseConflict("the lease is not a live lease owned by this agent")
	}

	return s.repo.ReleaseLease(ctx, leaseID, agent.ID, now, "released")
}

func (s *Service) Deliver(
	ctx context.Context,
	key string,
	actor domain.Actor,
	leaseID *int64,
	summary string,
	evidence []string,
) (domain.Task, domain.Delivery, error) {
	task, err := s.repo.GetTask(ctx, key)
	if err != nil {
		return domain.Task{}, domain.Delivery{}, err
	}

	now := s.clock.Now()
	var lease *domain.Lease
	if actor.Kind == domain.Agent {
		lease, err = s.repo.GetLiveLease(ctx, key, now)
		if err != nil {
			return domain.Task{}, domain.Delivery{}, err
		}
		if lease != nil && leaseID != nil && lease.ID != *leaseID {
			lease = nil
		}
	}

	next, delivery, movement, err := domain.ApplyDelivery(task, lease, actor, summary, evidence, now)
	if err != nil {
		return domain.Task{}, domain.Delivery{}, err
	}
	if err := s.repo.Deliver(ctx, next, delivery, movement, task.Version); err != nil {
		return domain.Task{}, domain.Delivery{}, err
	}

	deliveries, err := s.repo.ListDeliveries(ctx, key)
	if err != nil {
		return domain.Task{}, domain.Delivery{}, err
	}
	if len(deliveries) == 0 {
		return domain.Task{}, domain.Delivery{}, fmt.Errorf("deliver to %q: the delivery was not stored", key)
	}
	return next, deliveries[len(deliveries)-1], nil
}

func (s *Service) Detail(ctx context.Context, key string) (Detail, error) {
	task, err := s.repo.GetTask(ctx, key)
	if err != nil {
		return Detail{}, err
	}

	movements, err := s.repo.ListMovements(ctx, key)
	if err != nil {
		return Detail{}, err
	}
	leases, err := s.repo.ListLeases(ctx, key)
	if err != nil {
		return Detail{}, err
	}
	deliveries, err := s.repo.ListDeliveries(ctx, key)
	if err != nil {
		return Detail{}, err
	}
	comments, err := s.repo.ListComments(ctx, key)
	if err != nil {
		return Detail{}, err
	}

	return Detail{
		Task:       task,
		Movements:  movements,
		Leases:     leases,
		Deliveries: deliveries,
		Comments:   comments,
	}, nil
}

func (s *Service) Movements(ctx context.Context, key string) ([]domain.Movement, error) {
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return nil, err
	}
	return s.repo.ListMovements(ctx, key)
}

func (s *Service) Leases(ctx context.Context, key string) ([]domain.Lease, error) {
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return nil, err
	}
	return s.repo.ListLeases(ctx, key)
}

func (s *Service) Deliveries(ctx context.Context, key string) ([]domain.Delivery, error) {
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return nil, err
	}
	return s.repo.ListDeliveries(ctx, key)
}

func (s *Service) Comments(ctx context.Context, key string) ([]domain.Comment, error) {
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return nil, err
	}
	return s.repo.ListComments(ctx, key)
}

func (s *Service) AddComment(ctx context.Context, key string, actor domain.Actor, body string) (domain.Comment, error) {
	if _, err := s.repo.GetTask(ctx, key); err != nil {
		return domain.Comment{}, err
	}
	if err := domain.ValidateComment(body); err != nil {
		return domain.Comment{}, err
	}

	switch actor.Kind {
	case domain.Agent:
		live, err := s.repo.GetLiveLease(ctx, key, s.clock.Now())
		if err != nil {
			return domain.Comment{}, err
		}
		if live == nil || live.AgentID != actor.ID {
			return domain.Comment{}, leaseRequired("an agent needs a live lease it owns")
		}
	case domain.Human:
	default:
		return domain.Comment{}, invalidInput("unknown actor kind")
	}

	return s.repo.CreateComment(ctx, domain.Comment{
		TaskKey:   key,
		ActorID:   actor.ID,
		Actor:     actor,
		Body:      body,
		CreatedAt: s.clock.Now(),
	})
}

func forbiddenLease(msg string) *domain.RuleError {
	return &domain.RuleError{Code: domain.CodeForbidden, Msg: msg}
}

func leaseRequired(msg string) *domain.RuleError {
	return &domain.RuleError{Code: domain.CodeLeaseRequired, Msg: msg}
}

func leaseConflict(msg string) *domain.RuleError {
	return &domain.RuleError{Code: domain.CodeLeaseConflict, Msg: msg}
}

func optionalText(value *string) *string {
	if value == nil {
		return nil
	}
	if strings.TrimSpace(*value) == "" {
		return nil
	}
	copied := *value
	return &copied
}

func invalidInput(msg string) *domain.RuleError {
	return &domain.RuleError{Code: domain.CodeInvalidInput, Msg: msg}
}
