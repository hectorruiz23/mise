package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
)

const (
	integrationInterval = 2 * time.Minute
	environmentTimeout  = 5 * time.Second
)

var protectedPrefixes = []string{".github/workflows/", "api/migrations/"}

type merger interface {
	Merge(context.Context, githubinfo.Ref, string) (string, error)
	PullFiles(context.Context, githubinfo.Ref) ([]string, error)
	PullTarget(context.Context, githubinfo.Ref) (githubinfo.Target, error)
}

type integrationWorker struct {
	store  app.IntegrationStore
	github merger
	clock  app.Clock
	client *http.Client
	owned  string
	target githubinfo.Target
}

func (w *integrationWorker) run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(integrationInterval)
	defer ticker.Stop()
	for {
		if err := w.poll(ctx); err != nil && ctx.Err() == nil {
			logger.Warn("integrate deliveries", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *integrationWorker) poll(ctx context.Context) error {
	if w.github != nil {
		candidates, err := w.store.PendingMerges(ctx)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			if err := w.merge(ctx, candidate); err != nil {
				return err
			}
		}
	}

	pending, err := w.store.PendingDeployments(ctx)
	if err != nil {
		return err
	}
	for _, item := range pending {
		revision, err := w.servedRevision(ctx, item.URL)
		if err != nil || !strings.EqualFold(revision, item.Revision) {
			continue
		}
		if err := w.store.RecordDeployment(ctx, item.PlanID, item.TaskKey,
			item.Environment, revision, w.clock.Now()); err != nil {
			return err
		}
	}
	return nil
}

func (w *integrationWorker) merge(ctx context.Context, candidate app.MergeCandidate) error {
	ref, ok := githubinfo.ParsePullRequest(candidate.PRURL)
	if !ok {
		return w.store.RecordMergeAttempt(ctx, app.MergeOutcome{
			DeliveryID: candidate.DeliveryID,
			TaskKey:    candidate.TaskKey,
			PRURL:      candidate.PRURL,
			ActorID:    candidate.ActorID,
			Outcome:    app.MergeOutcomeFailed,
			Reason:     "the delivery does not link a GitHub pull request",
		}, w.clock.Now())
	}
	if w.owned != "" && strings.EqualFold(ref.Owner+"/"+ref.Repo, w.owned) {
		return nil
	}

	destination, err := w.github.PullTarget(ctx, ref)
	if err != nil {
		return err
	}
	if !w.authorized() {
		return errors.New("auto merge has no authorized repository and base branch")
	}
	if reason := destinationMismatch(w.target, destination); reason != "" {
		return w.store.RecordMergeAttempt(ctx, app.MergeOutcome{
			DeliveryID: candidate.DeliveryID,
			TaskKey:    candidate.TaskKey,
			PRURL:      candidate.PRURL,
			ActorID:    candidate.ActorID,
			Outcome:    app.MergeOutcomeRefused,
			Reason:     reason,
		}, w.clock.Now())
	}

	files, err := w.github.PullFiles(ctx, ref)
	if err != nil {
		return err
	}
	if offending := protected(files); len(offending) > 0 {
		return w.store.RecordMergeAttempt(ctx, app.MergeOutcome{
			DeliveryID: candidate.DeliveryID,
			TaskKey:    candidate.TaskKey,
			PRURL:      candidate.PRURL,
			ActorID:    candidate.ActorID,
			Outcome:    app.MergeOutcomeRefused,
			Reason:     "it changes " + strings.Join(offending, ", "),
		}, w.clock.Now())
	}

	sha, err := w.github.Merge(ctx, ref, *candidate.HeadSHA)
	if err != nil {
		if refusal, refused := githubinfo.RefusalOf(err); refused {
			return w.store.RecordMergeAttempt(ctx, app.MergeOutcome{
				DeliveryID: candidate.DeliveryID,
				TaskKey:    candidate.TaskKey,
				PRURL:      candidate.PRURL,
				ActorID:    candidate.ActorID,
				Outcome:    app.MergeOutcomeFailed,
				Reason:     "GitHub answered " + refusal.Message,
			}, w.clock.Now())
		}
		return err
	}

	return w.store.RecordMerge(ctx, app.MergeEvidence{
		TaskKey:    candidate.TaskKey,
		PRURL:      candidate.PRURL,
		DeliveryID: candidate.DeliveryID,
		HeadSHA:    candidate.HeadSHA,
		MergeSHA:   sha,
		ActorID:    candidate.ActorID,
	}, w.clock.Now())
}

func (w *integrationWorker) authorized() bool {
	return strings.TrimSpace(w.target.Repository) != "" && strings.TrimSpace(w.target.Base) != ""
}

func destinationMismatch(authorized, destination githubinfo.Target) string {
	if !strings.EqualFold(destination.Repository, strings.TrimSpace(authorized.Repository)) {
		return "it targets " + destination.Repository + ", not " + authorized.Repository
	}
	if destination.Base != strings.TrimSpace(authorized.Base) {
		return "it targets branch " + destination.Base + ", not " + authorized.Base
	}
	return ""
}

func protected(files []string) []string {
	offending := make([]string, 0)
	for _, file := range files {
		for _, prefix := range protectedPrefixes {
			if strings.HasPrefix(file, prefix) {
				offending = append(offending, file)
				break
			}
		}
	}
	return offending
}

func (w *integrationWorker) servedRevision(ctx context.Context, base string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, environmentTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(base, "/")+"/api/build", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := w.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("environment answered %d", response.StatusCode)
	}
	var body struct {
		Revision *string `json:"revision"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.Revision == nil {
		return "", errors.New("the environment serves no revision")
	}
	revision := strings.ToLower(strings.TrimSpace(*body.Revision))
	if !commitRevision(revision) {
		return "", errors.New("the environment serves a malformed revision")
	}
	return revision, nil
}

func commitRevision(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
