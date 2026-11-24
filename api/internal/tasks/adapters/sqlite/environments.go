package sqlite

import (
	"context"
	"fmt"
	"strings"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func (r *Repo) ListEnvironments(ctx context.Context, projectKey string) ([]app.Environment, error) {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE key = ?)`,
		projectKey).Scan(&exists); err != nil {
		return nil, fmt.Errorf("list environments for %q: %w", projectKey, err)
	}
	if !exists {
		return nil, &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, url FROM environments WHERE project_key = ? ORDER BY name`, projectKey)
	if err != nil {
		return nil, fmt.Errorf("list environments for %q: %w", projectKey, err)
	}
	defer rows.Close()

	environments := make([]app.Environment, 0)
	for rows.Next() {
		var environment app.Environment
		if err := rows.Scan(&environment.Name, &environment.URL); err != nil {
			return nil, fmt.Errorf("list environments for %q: scan: %w", projectKey, err)
		}
		environments = append(environments, environment)
	}
	return environments, rows.Err()
}

func (r *Repo) AddEnvironment(ctx context.Context, projectKey string, environment app.Environment) error {
	var exists bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE key = ?)`,
		projectKey).Scan(&exists); err != nil {
		return fmt.Errorf("add environment for %q: %w", projectKey, err)
	}
	if !exists {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO environments (project_key, name, url) VALUES (?, ?, ?)`,
		projectKey, environment.Name, environment.URL)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return &domain.RuleError{Code: domain.CodeConflict, Msg: "the project already has an environment with that name"}
		}
		return fmt.Errorf("add environment for %q: %w", projectKey, err)
	}
	return nil
}

func (r *Repo) SetProjectAutoMerge(ctx context.Context, projectKey string, autoMerge bool) error {
	flag := 0
	if autoMerge {
		flag = 1
	}
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET auto_merge = ? WHERE key = ?`, flag, projectKey)
	if err != nil {
		return fmt.Errorf("set auto merge for %q: %w", projectKey, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set auto merge for %q: rows affected: %w", projectKey, err)
	}
	if affected == 0 {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	return nil
}

func (r *Repo) SetProjectGitHub(ctx context.Context, key string, policy app.PlanPolicy, autoMerge *bool) error {
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET github_repository=?,github_branch=?,github_workflow_id=?,auto_merge=coalesce(?,auto_merge) WHERE key=?`, policy.Repository, policy.Branch, policy.WorkflowID, autoMerge, key)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown project"}
	}
	return err
}

func (r *Repo) ImportGitHubPolicy(ctx context.Context, p app.PlanPolicy) error {
	if p.WorkflowID == 0 {
		return nil
	}
	_, err := r.db.ExecContext(ctx, `UPDATE projects SET github_repository=?,github_branch=?,github_workflow_id=? WHERE github_workflow_id=0 AND lower(rtrim(repo_url,'/'))=lower(?)`, p.Repository, p.Branch, p.WorkflowID, "https://github.com/"+p.Repository)
	return err
}
