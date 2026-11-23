package app

import (
	"context"
	"strings"

	"mise/api/internal/tasks/domain"
)

type Checks interface {
	Checks(ctx context.Context, repository, revision string) ([]domain.Check, error)
}

func (s *Service) WithChecks(checks Checks) *Service {
	s.checks = checks
	return s
}

func (s *Service) DeclareSubmission(
	ctx context.Context,
	actor domain.Actor,
	key string,
	prURL *string,
	headSHA string,
) (domain.Submission, error) {
	if err := viewerForbidden(actor); err != nil {
		return domain.Submission{}, err
	}
	task, err := s.repo.GetTask(ctx, key, AllProjects)
	if err != nil {
		return domain.Submission{}, err
	}
	if actor.Kind == domain.Agent {
		lease, err := s.repo.GetLiveLease(ctx, key, s.clock.Now())
		if err != nil {
			return domain.Submission{}, err
		}
		if lease == nil {
			return domain.Submission{}, &domain.RuleError{
				Code: domain.CodeLeaseRequired,
				Msg:  "a live lease is required to declare a submission",
			}
		}
	}
	built, err := s.snapshotSubmission(ctx, task, prURL, headSHA, s.clock.Now())
	if err != nil {
		return domain.Submission{}, err
	}
	return s.repo.UpsertSubmission(ctx, built)
}

func (s *Service) UpdateProjectChecks(ctx context.Context, actor domain.Actor, key string, checks []string) error {
	if actor.Kind != domain.Human {
		return &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human may set the required checks"}
	}
	cleaned, err := domain.NormalizeChecks(checks)
	if err != nil {
		return err
	}
	return s.repo.SetProjectChecks(ctx, key, cleaned, s.clock.Now())
}

func (s *Service) snapshotSubmission(
	ctx context.Context,
	task domain.Task,
	prURL *string,
	headSHA string,
	now int64,
) (domain.Submission, error) {
	normalized, err := domain.NormalizePRURL(prURL)
	if err != nil {
		return domain.Submission{}, err
	}
	if normalized == nil {
		return domain.Submission{}, invalidInput("pr_url is required for a submission")
	}
	if !domain.ValidRevision(headSHA) {
		return domain.Submission{}, invalidInput("head_sha must be the full 40-character commit")
	}
	required, err := s.repo.ProjectChecks(ctx, task.ProjectKey)
	if err != nil {
		return domain.Submission{}, err
	}
	submission := domain.Submission{
		TaskKey:    task.Key,
		PRURL:      *normalized,
		HeadSHA:    strings.ToLower(headSHA),
		Required:   required,
		Verdict:    domain.VerdictPassed,
		DeclaredAt: now,
	}
	if len(required) == 0 {
		return submission, nil
	}
	if s.checks == nil {
		return domain.Submission{}, &domain.RuleError{
			Code: domain.CodeConflict,
			Msg:  "check verification is not configured",
		}
	}
	repository, ok := domain.RepositoryOf(*normalized)
	if !ok {
		return domain.Submission{}, invalidInput("pr_url must point at a GitHub pull request")
	}
	observed, err := s.checks.Checks(ctx, repository, submission.HeadSHA)
	if err != nil {
		return submission, nil
	}
	submission.Verdict, submission.Checks = domain.EvaluateChecks(required, observed)
	checkedAt := now
	submission.CheckedAt = &checkedAt
	return submission, nil
}

func (s *Service) recordFailedAttempt(
	ctx context.Context,
	task domain.Task,
	actor domain.Actor,
	submission domain.Submission,
	now int64,
) error {
	if err := s.repo.MarkSubmissionDelivered(ctx, submission.ID, now); err != nil {
		return err
	}
	failures, err := s.repo.CountFailedAttempts(ctx, task.Key)
	if err != nil {
		return err
	}
	if failures < 2 {
		return nil
	}
	next, movement, err := domain.ApplyMove(task, domain.Blocked, actor,
		"Two failed deliveries: "+domain.FailedChecks(submission.Checks), true, now)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateTask(ctx, next, movement, task.Version); err != nil {
		return err
	}
	_, err = s.repo.CloseLiveLease(ctx, task.Key, now, "returned")
	return err
}
