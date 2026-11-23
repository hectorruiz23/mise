package main

import (
	"context"
	"log/slog"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
)

type graphWorker struct {
	store  app.PlanStore
	policy app.PlanPolicy
	github *githubinfo.Client
	clock  app.Clock
}

func (w *graphWorker) run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		if err := w.poll(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("verify plan graph", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *graphWorker) poll(ctx context.Context) error {
	reviews := reviewWorker{store: w.store, github: w.github, clock: w.clock}
	if err := reviews.poll(ctx); err != nil {
		return err
	}
	pending, err := w.store.PendingGraphTasks(ctx)
	if err != nil {
		return err
	}
	for _, item := range pending {
		if item.Policy != w.policy {
			continue
		}
		proof, err := w.github.VerifyMerge(ctx, item.Policy.Repository, item.Policy.Branch,
			item.Policy.WorkflowID, item.PRURL, item.HeadSHA, item.ApprovedAt)
		if err != nil {
			return err
		}
		if proof == nil {
			continue
		}
		if _, err := w.store.ReleaseGraphTask(ctx, item.PlanID, item.TaskKey, app.PlanProof{
			TaskKey:    item.TaskKey,
			DeliveryID: item.DeliveryID,
			PRURL:      item.PRURL,
			HeadSHA:    item.HeadSHA,
			MergeSHA:   proof.MergeSHA,
			RunID:      proof.RunID,
			Attempt:    proof.Attempt,
		}, w.clock.Now()); err != nil {
			return err
		}
	}
	return nil
}
