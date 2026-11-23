package main

import (
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"

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
