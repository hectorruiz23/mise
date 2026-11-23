package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func (r *Repo) DispatchPolicy(ctx context.Context, projectKey string) (app.DispatchPolicy, error) {
	var policy app.DispatchPolicy
	var enabled int
	err := r.db.QueryRowContext(ctx, `SELECT auto_dispatch, dispatch_limit FROM projects WHERE key = ?`,
		projectKey).Scan(&enabled, &policy.Limit)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	if err != nil {
		return policy, fmt.Errorf("dispatch policy for %q: %w", projectKey, err)
	}
	policy.Enabled = enabled == 1
	return policy, nil
}

func (r *Repo) SetProjectDispatch(ctx context.Context, projectKey string, enabled bool, limit int, now int64) error {
	flag := 0
	if enabled {
		flag = 1
	}
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET auto_dispatch = ?, dispatch_limit = ? WHERE key = ?`,
		flag, limit, projectKey)
	if err != nil {
		return fmt.Errorf("set dispatch policy for %q: %w", projectKey, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set dispatch policy for %q: rows affected: %w", projectKey, err)
	}
	if affected == 0 {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	return nil
}

func (r *Repo) CountLeases(ctx context.Context, taskKey string) (int, error) {
	var leases int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM leases WHERE task_key = ?`,
		taskKey).Scan(&leases); err != nil {
		return 0, fmt.Errorf("count leases for %q: %w", taskKey, err)
	}
	return leases, nil
}

func (r *Repo) CountReopens(ctx context.Context, taskKey string) (int, error) {
	var reopens int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM task_movements m
		JOIN users u ON u.id = m.actor_id AND u.kind = 'human'
		WHERE m.task_key = ? AND m.from_state = 'blocked' AND m.to_state IN ('ready', 'in_progress')`,
		taskKey).Scan(&reopens); err != nil {
		return 0, fmt.Errorf("count reopens for %q: %w", taskKey, err)
	}
	return reopens, nil
}
