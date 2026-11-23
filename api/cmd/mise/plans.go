package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
)

var repositoryName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*/[a-zA-Z0-9_.-]+$`)

func loadPlanPolicy() (app.PlanPolicy, error) {
	repository := os.Getenv("MISE_PLAN_REPOSITORY")
	branch := os.Getenv("MISE_PLAN_BRANCH")
	workflow := os.Getenv("MISE_PLAN_WORKFLOW_ID")
	if repository == "" && branch == "" && workflow == "" {
		return app.PlanPolicy{}, nil
	}
	if !repositoryName.MatchString(repository) || strings.HasSuffix(repository, "/.") || strings.HasSuffix(repository, "/..") {
		return app.PlanPolicy{}, errors.New("MISE_PLAN_REPOSITORY must be owner/repository")
	}
	if branch == "" || strings.TrimSpace(branch) != branch || strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "..") {
		return app.PlanPolicy{}, errors.New("MISE_PLAN_BRANCH must be a branch name")
	}
	id, err := strconv.ParseInt(workflow, 10, 64)
	if err != nil || id < 1 {
		return app.PlanPolicy{}, errors.New("MISE_PLAN_WORKFLOW_ID must be a positive integer")
	}
	return app.PlanPolicy{Repository: repository, Branch: branch, WorkflowID: id}, nil
}

type planWorker struct {
	plans    app.DependencyPlans
	policy   app.PlanPolicy
	github   *githubinfo.Client
	clock    app.Clock
	lastTask string
}

func (w *planWorker) run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()
	for {
		if err := w.poll(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("verify dependency plan", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *planWorker) poll(ctx context.Context) error {
	plans, err := w.plans.PendingPlans(ctx)
	if err != nil {
		return err
	}
	var first, next *app.DependencyPlan
	for i := range plans {
		p := &plans[i]
		if p.Policy != w.policy {
			continue
		}
		if first == nil || p.FirstTask < first.FirstTask {
			first = p
		}
		if p.FirstTask > w.lastTask && (next == nil || p.FirstTask < next.FirstTask) {
			next = p
		}
	}
	if next == nil {
		next = first
	}
	if next == nil {
		return nil
	}
	w.lastTask = next.FirstTask
	return w.reconcile(ctx, *next)
}

func (w *planWorker) reconcile(ctx context.Context, p app.DependencyPlan) error {
	if p.Policy != w.policy {
		return nil
	}
	proof, err := w.github.VerifyMerge(ctx, p.Policy.Repository, p.Policy.Branch, p.Policy.WorkflowID, p.PRURL, p.HeadSHA, p.ApprovedAt)
	if err != nil {
		return fmt.Errorf("%s: %w", p.FirstTask, err)
	}
	if proof == nil {
		return nil
	}
	_, err = w.plans.ReleasePlan(ctx, p.FirstTask, app.MergeProof{DeliveryID: p.DeliveryID, PRURL: p.PRURL, HeadSHA: p.HeadSHA, RunID: proof.RunID, MergeSHA: proof.MergeSHA, Attempt: proof.Attempt}, w.clock.Now())
	return err
}
