package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
)

const reviewInterval = 3 * time.Minute

const (
	reviewApproved         = "APPROVED"
	reviewChangesRequested = "CHANGES_REQUESTED"
)

type reviewer interface {
	Reviews(context.Context, githubinfo.Ref) ([]githubinfo.Review, error)
}

type reviewWorker struct {
	store  app.PlanStore
	github reviewer
	clock  app.Clock
}

func (w *reviewWorker) run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(reviewInterval)
	defer ticker.Stop()
	for {
		if err := w.poll(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("read plan reviews", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *reviewWorker) poll(ctx context.Context) error {
	pending, err := w.store.PendingPlanReviews(ctx)
	if err != nil {
		return err
	}
	for _, item := range pending {
		ref, ok := githubinfo.ParsePullRequest(item.PRURL)
		if !ok {
			continue
		}
		reviews, err := w.github.Reviews(ctx, ref)
		if err != nil {
			return err
		}
		owner := repositoryOwner(item.Repository)
		review, ok := latestReviewBy(reviews, owner)
		if !ok || !strings.EqualFold(review.CommitID, item.HeadSHA) {
			continue
		}
		switch strings.ToUpper(review.State) {
		case reviewApproved:
			if _, err := w.store.RecordApproval(ctx, item.PlanID, item.TaskKey, item.HeadSHA,
				review.User, w.clock.Now()); err != nil {
				return err
			}
		case reviewChangesRequested:
			if _, err := w.store.RequestCorrection(ctx, item.PlanID, item.TaskKey, item.HeadSHA,
				"review:"+item.HeadSHA, "the owner asked for changes", w.clock.Now()); err != nil {
				return err
			}
		}
	}
	return nil
}

func latestReviewBy(reviews []githubinfo.Review, login string) (githubinfo.Review, bool) {
	var latest githubinfo.Review
	found := false
	for _, review := range reviews {
		if !strings.EqualFold(review.User, login) {
			continue
		}
		if !found || !review.SubmittedAt.Before(latest.SubmittedAt) {
			latest = review
			found = true
		}
	}
	return latest, found
}

func repositoryOwner(repository string) string {
	parts := strings.SplitN(strings.TrimSpace(repository), "/", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}
