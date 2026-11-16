package app_test

import (
	"context"
	"testing"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type scopeRepo struct {
	app.Repo
	scopes []app.ReadScope
	filter app.Filter
}

func (r *scopeRepo) record(scope app.ReadScope) {
	r.scopes = append(r.scopes, scope)
}

func (r *scopeRepo) GetTask(_ context.Context, _ string, scope app.ReadScope) (domain.Task, error) {
	r.record(scope)
	return domain.Task{}, nil
}

func (r *scopeRepo) ListTasks(_ context.Context, f app.Filter) ([]domain.Task, string, error) {
	r.record(f.Scope)
	r.filter = f
	return nil, "", nil
}

func (r *scopeRepo) ListProjects(_ context.Context, scope app.ReadScope) ([]app.Project, error) {
	r.record(scope)
	return nil, nil
}

func (r *scopeRepo) ListBoardTasks(_ context.Context, _ string, scope app.ReadScope) ([]domain.Task, error) {
	r.record(scope)
	return nil, nil
}

func (r *scopeRepo) ListMovements(_ context.Context, _ string, scope app.ReadScope) ([]domain.Movement, error) {
	r.record(scope)
	return nil, nil
}

func (r *scopeRepo) ListLeases(_ context.Context, _ string, scope app.ReadScope) ([]domain.Lease, error) {
	r.record(scope)
	return nil, nil
}

func (r *scopeRepo) ListDeliveries(_ context.Context, _ string, scope app.ReadScope) ([]domain.Delivery, error) {
	r.record(scope)
	return nil, nil
}

func (r *scopeRepo) ListComments(_ context.Context, _ string, scope app.ReadScope) ([]domain.Comment, error) {
	r.record(scope)
	return nil, nil
}

type readCall struct {
	name string
	run  func(*app.Service, domain.Actor, app.ReadScope) error
}

func readCalls() []readCall {
	ctx := context.Background()
	return []readCall{
		{"Get", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Get(ctx, actor, "MI-1")
			return err
		}},
		{"List", func(s *app.Service, actor domain.Actor, scope app.ReadScope) error {
			_, _, err := s.List(ctx, actor, app.Filter{Scope: scope})
			return err
		}},
		{"ListProjects", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.ListProjects(ctx, actor)
			return err
		}},
		{"Board", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Board(ctx, actor, "MI")
			return err
		}},
		{"Detail", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Detail(ctx, actor, "MI-1")
			return err
		}},
		{"Movements", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Movements(ctx, actor, "MI-1")
			return err
		}},
		{"Leases", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Leases(ctx, actor, "MI-1")
			return err
		}},
		{"Deliveries", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Deliveries(ctx, actor, "MI-1")
			return err
		}},
		{"Comments", func(s *app.Service, actor domain.Actor, _ app.ReadScope) error {
			_, err := s.Comments(ctx, actor, "MI-1")
			return err
		}},
	}
}

func TestReadScopeFollowsActor(t *testing.T) {
	cases := []struct {
		name  string
		actor domain.Actor
		want  app.ReadScope
	}{
		{"human", human(), app.AllProjects},
		{"agent", agent(), app.AllProjects},
		{"viewer", domain.Actor{Kind: domain.Viewer, Name: "Visitor"}, app.PublicProjects},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, call := range readCalls() {
				t.Run(call.name, func(t *testing.T) {
					repo := &scopeRepo{}
					service := app.New(repo, fakeClock(100))
					clientScope := app.AllProjects
					if tc.want == app.AllProjects {
						clientScope = app.PublicProjects
					}
					if err := call.run(service, tc.actor, clientScope); err != nil {
						t.Fatalf("%s: %v", call.name, err)
					}
					if len(repo.scopes) == 0 {
						t.Fatalf("%s did not reach the repository", call.name)
					}
					for _, scope := range repo.scopes {
						if scope != tc.want {
							t.Fatalf("%s scope = %d, want %d", call.name, scope, tc.want)
						}
					}
					if call.name == "List" && repo.filter.Scope != tc.want {
						t.Fatalf("List filter scope = %d, want %d", repo.filter.Scope, tc.want)
					}
				})
			}
		})
	}
}

func TestUnknownActorIsForbiddenOnReads(t *testing.T) {
	actor := domain.Actor{Kind: domain.ActorKind("robot"), Name: "Robot"}
	for _, call := range readCalls() {
		t.Run(call.name, func(t *testing.T) {
			repo := &scopeRepo{}
			service := app.New(repo, fakeClock(100))
			err := call.run(service, actor, app.AllProjects)
			if code := ruleCode(t, err); code != domain.CodeForbidden {
				t.Fatalf("code = %q, want %q", code, domain.CodeForbidden)
			}
			if len(repo.scopes) != 0 {
				t.Fatalf("%s reached the repository with scopes %v", call.name, repo.scopes)
			}
		})
	}
}
