package app

import (
	"context"
	"mise/api/internal/tasks/domain"
)

type DispatchPolicy struct {
	Enabled bool
	Limit   int
}

func (s *Service) UpdateProjectDispatch(ctx context.Context, actor domain.Actor, key string, enabled bool, limit int) error {
	if actor.Kind != domain.Human {
		return &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human may change the dispatch policy"}
	}
	if limit < 1 || limit > 20 {
		return invalidInput("dispatch_limit must be between 1 and 20")
	}
	return s.repo.SetProjectDispatch(ctx, key, enabled, limit, s.clock.Now())
}

func (s *Service) checkDispatchBudget(ctx context.Context, task domain.Task) error {
	policy, err := s.repo.DispatchPolicy(ctx, task.ProjectKey)
	if err != nil {
		return err
	}
	if !policy.Enabled {
		return nil
	}
	leases, err := s.repo.CountLeases(ctx, task.Key)
	if err != nil {
		return err
	}
	reopens, err := s.repo.CountReopens(ctx, task.Key)
	if err != nil {
		return err
	}
	if leases >= policy.Limit+reopens {
		return &domain.RuleError{
			Code: domain.CodeDispatchExhausted,
			Msg:  "this card was dispatched too many times without a delivery",
		}
	}
	return nil
}
