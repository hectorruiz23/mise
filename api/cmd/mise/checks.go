package main

import (
	"context"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/domain"
)

type githubChecks struct {
	client *githubinfo.Client
}

func (g githubChecks) Checks(ctx context.Context, repository, revision string) ([]domain.Check, error) {
	runs, err := g.client.CheckRuns(ctx, repository, revision)
	if err != nil {
		return nil, err
	}
	checks := make([]domain.Check, 0, len(runs))
	for _, run := range runs {
		checks = append(checks, domain.Check{
			Name: run.Name, Status: run.Status, Conclusion: run.Conclusion, URL: run.URL,
		})
	}
	return checks, nil
}
