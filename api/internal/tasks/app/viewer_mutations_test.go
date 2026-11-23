package app_test

import (
	"context"
	"errors"
	"testing"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type untouchedRepo struct {
	t *testing.T
}

func (r untouchedRepo) touch(method string) {
	r.t.Fatalf("repository method %s was called for a viewer", method)
}

func (r untouchedRepo) CreateTask(context.Context, app.CreateParams) (domain.Task, bool, error) {
	r.touch("CreateTask")
	return domain.Task{}, false, nil
}

func (r untouchedRepo) GetTask(context.Context, string, app.ReadScope) (domain.Task, error) {
	r.touch("GetTask")
	return domain.Task{}, nil
}

func (r untouchedRepo) ListTasks(context.Context, app.Filter) ([]domain.Task, string, error) {
	r.touch("ListTasks")
	return nil, "", nil
}

func (r untouchedRepo) ListProjects(context.Context, app.ReadScope) ([]app.Project, error) {
	r.touch("ListProjects")
	return nil, nil
}

func (r untouchedRepo) ListPublicProjects(context.Context) ([]app.Project, error) {
	r.touch("ListPublicProjects")
	return nil, nil
}

func (r untouchedRepo) CreateProject(context.Context, app.Project, int64) error {
	r.touch("CreateProject")
	return nil
}

func (r untouchedRepo) ListBoardTasks(context.Context, string, app.ReadScope) ([]domain.Task, error) {
	r.touch("ListBoardTasks")
	return nil, nil
}

func (r untouchedRepo) UpdateTask(context.Context, domain.Task, domain.Movement, int64) error {
	r.touch("UpdateTask")
	return nil
}

func (r untouchedRepo) CountDeliveries(context.Context, string) (int, error) {
	r.touch("CountDeliveries")
	return 0, nil
}

func (r untouchedRepo) GetLiveLease(context.Context, string, int64) (*domain.Lease, error) {
	r.touch("GetLiveLease")
	return nil, nil
}

func (r untouchedRepo) AcquireLease(context.Context, domain.Task, domain.Lease, domain.Movement, int64) error {
	r.touch("AcquireLease")
	return nil
}

func (r untouchedRepo) RenewLease(context.Context, int64, int64, int64) error {
	r.touch("RenewLease")
	return nil
}

func (r untouchedRepo) ReleaseLease(context.Context, int64, int64, int64, string) error {
	r.touch("ReleaseLease")
	return nil
}

func (r untouchedRepo) CloseLiveLease(context.Context, string, int64, string) (bool, error) {
	r.touch("CloseLiveLease")
	return false, nil
}

func (r untouchedRepo) Deliver(context.Context, domain.Task, domain.Delivery, domain.Movement, int64) error {
	r.touch("Deliver")
	return nil
}

func (r untouchedRepo) ListLeases(context.Context, string, app.ReadScope) ([]domain.Lease, error) {
	r.touch("ListLeases")
	return nil, nil
}

func (r untouchedRepo) ListDeliveries(context.Context, string, app.ReadScope) ([]domain.Delivery, error) {
	r.touch("ListDeliveries")
	return nil, nil
}

func (r untouchedRepo) ListMovements(context.Context, string, app.ReadScope) ([]domain.Movement, error) {
	r.touch("ListMovements")
	return nil, nil
}

func (r untouchedRepo) CreateComment(context.Context, domain.Comment) (domain.Comment, error) {
	r.touch("CreateComment")
	return domain.Comment{}, nil
}

func (r untouchedRepo) ListComments(context.Context, string, app.ReadScope) ([]domain.Comment, error) {
	r.touch("ListComments")
	return nil, nil
}

func TestViewerMutatorsAreForbiddenBeforeTheRepository(t *testing.T) {
	viewer := domain.Actor{ID: 0, Kind: domain.Viewer, Name: "Visitor"}
	ctx := context.Background()
	title := "Renamed"

	tests := []struct {
		name string
		call func(*app.Service) error
	}{
		{
			name: "CreateProject",
			call: func(s *app.Service) error {
				_, err := s.CreateProject(ctx, app.CreateProjectParams{Key: "MI", Name: "Mise", Actor: viewer})
				return err
			},
		},
		{
			name: "Create",
			call: func(s *app.Service) error {
				_, _, err := s.Create(ctx, app.CreateParams{
					ProjectKey: "MI", Title: "A task", Actor: viewer,
				})
				return err
			},
		},
		{
			name: "Update",
			call: func(s *app.Service) error {
				_, err := s.Update(ctx, "MI-1", app.Patch{Title: &title}, viewer, 1, true)
				return err
			},
		},
		{
			name: "Move",
			call: func(s *app.Service) error {
				_, err := s.Move(ctx, "MI-1", domain.InProgress, "reason", viewer, 1, true)
				return err
			},
		},
		{
			name: "Acquire",
			call: func(s *app.Service) error {
				_, _, err := s.Acquire(ctx, "MI-1", viewer)
				return err
			},
		},
		{
			name: "Renew",
			call: func(s *app.Service) error {
				_, err := s.Renew(ctx, "MI-1", 1, viewer)
				return err
			},
		},
		{
			name: "Release",
			call: func(s *app.Service) error {
				return s.Release(ctx, "MI-1", 1, viewer)
			},
		},
		{
			name: "Deliver",
			call: func(s *app.Service) error {
				_, _, err := s.Deliver(ctx, "MI-1", viewer, nil, "Done", []string{"go test ./..."}, nil)
				return err
			},
		},
		{
			name: "AddComment",
			call: func(s *app.Service) error {
				_, err := s.AddComment(ctx, "MI-1", viewer, "hello")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := app.New(untouchedRepo{t: t}, fakeClock(1000))
			err := tt.call(service)
			if err == nil {
				t.Fatal("viewer mutation was allowed")
			}
			var rule *domain.RuleError
			if !errors.As(err, &rule) {
				t.Fatalf("error = %v, want *domain.RuleError", err)
			}
			if rule.Code != domain.CodeForbidden {
				t.Errorf("code = %q, want %q", rule.Code, domain.CodeForbidden)
			}
			if rule.Msg != "viewers cannot modify the board" {
				t.Errorf("message = %q, want the viewer refusal", rule.Msg)
			}
		})
	}
}

func TestViewerIsRefusedBeforeInputValidation(t *testing.T) {
	viewer := domain.Actor{ID: 0, Kind: domain.Viewer, Name: "Visitor"}
	ctx := context.Background()
	service := app.New(untouchedRepo{t: t}, fakeClock(1000))

	if _, _, err := service.Create(ctx, app.CreateParams{Actor: viewer}); err == nil {
		t.Fatal("viewer create without input was allowed")
	} else if code := ruleCode(t, err); code != domain.CodeForbidden {
		t.Errorf("Create code = %q, want %q", code, domain.CodeForbidden)
	}
	if _, err := service.Move(ctx, "", "", "", viewer, 0, false); err == nil {
		t.Fatal("viewer move without input was allowed")
	} else if code := ruleCode(t, err); code != domain.CodeForbidden {
		t.Errorf("Move code = %q, want %q", code, domain.CodeForbidden)
	}
	if _, _, err := service.Deliver(ctx, "", viewer, nil, "", nil, nil); err == nil {
		t.Fatal("viewer delivery without input was allowed")
	} else if code := ruleCode(t, err); code != domain.CodeForbidden {
		t.Errorf("Deliver code = %q, want %q", code, domain.CodeForbidden)
	}
}

func (r untouchedRepo) ProjectChecks(_ context.Context, _ string) ([]string, error) {
	r.touch("ProjectChecks")
	return nil, nil
}

func (r untouchedRepo) SetProjectChecks(_ context.Context, _ string, _ []string, _ int64) error {
	r.touch("SetProjectChecks")
	return nil
}

func (r untouchedRepo) UpsertSubmission(_ context.Context, s domain.Submission) (domain.Submission, error) {
	r.touch("UpsertSubmission")
	return s, nil
}

func (r untouchedRepo) MarkSubmissionDelivered(_ context.Context, _ int64, _ int64) error {
	r.touch("MarkSubmissionDelivered")
	return nil
}

func (r untouchedRepo) CountFailedAttempts(_ context.Context, _ string) (int, error) {
	r.touch("CountFailedAttempts")
	return 0, nil
}

func (r untouchedRepo) ListSubmissions(_ context.Context, _ string, _ app.ReadScope) ([]domain.Submission, error) {
	r.touch("ListSubmissions")
	return nil, nil
}
