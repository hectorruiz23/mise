package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

const (
	listDefaultLimit = 50
	listMaxLimit     = 200
)

type Repo struct {
	db *sql.DB
}

func New(db *sql.DB) *Repo {
	return &Repo{db: db}
}

func (r *Repo) CreateTask(ctx context.Context, p app.CreateParams) (domain.Task, bool, error) {
	var task domain.Task
	var replayed bool

	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if p.IdempotencyKey != "" {
			stored, err := readIdempotency(ctx, conn, p.IdempotencyKey)
			switch {
			case err == nil:
				if stored.actorID != p.Actor.ID || stored.digest != digestOf(p.Digest) {
					return &domain.RuleError{
						Code: app.CodeIdempotencyConflict,
						Msg:  "Idempotency-Key was already used with a different request",
					}
				}
				var snapshot domain.Task
				if err := json.Unmarshal([]byte(stored.body), &snapshot); err != nil {
					return fmt.Errorf("create task: decode idempotent response: %w", err)
				}
				task, replayed = snapshot, true
				return nil
			case errors.Is(err, sql.ErrNoRows):
			default:
				return err
			}
		}

		next, err := nextTaskNumber(ctx, conn, p.ProjectKey)
		if err != nil {
			return err
		}

		key := fmt.Sprintf("%s-%d", p.ProjectKey, next)
		created, err := domain.NewTask(key, p.ProjectKey, p.Title, p.Actor.ID, p.Now)
		if err != nil {
			return err
		}
		created.Notes = p.Notes
		created.Acceptance = p.Acceptance
		created.Size = p.Size
		if p.Priority != "" {
			created.Priority = p.Priority
		}

		idempotencyKey := p.IdempotencyKey
		if idempotencyKey == "" {
			idempotencyKey = "auto:" + key
		}
		body, err := json.Marshal(created)
		if err != nil {
			return fmt.Errorf("create task: encode response: %w", err)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO idempotency_records
				(key, actor_id, request_digest, response_body, status_code, created_at)
			VALUES (?, ?, ?, ?, 201, ?)`,
			idempotencyKey, p.Actor.ID, digestOf(p.Digest), string(body), p.Now); err != nil {
			return fmt.Errorf("create task: record idempotency: %w", err)
		}

		if _, err := conn.ExecContext(ctx, `
			INSERT INTO tasks
				(key, project_key, title, notes, acceptance, state, priority, size,
				 created_by, version, created_at, updated_at, closed_at, archived_at,
				 idempotency_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, ?)`,
			created.Key, created.ProjectKey, created.Title, nullableText(created.Notes),
			nullableText(created.Acceptance), string(created.State), string(created.Priority),
			nullableSize(created.Size), created.CreatedBy, created.Version,
			created.CreatedAt, created.UpdatedAt, idempotencyKey); err != nil {
			return fmt.Errorf("create task: insert task: %w", err)
		}

		movement := domain.Movement{
			TaskKey:     created.Key,
			ToState:     domain.Backlog,
			ActorID:     p.Actor.ID,
			TaskVersion: created.Version,
			At:          p.Now,
		}
		if err := insertMovement(ctx, conn, movement); err != nil {
			return err
		}

		if _, err := conn.ExecContext(ctx,
			`UPDATE projects SET next_task_number = next_task_number + 1 WHERE key = ?`,
			p.ProjectKey); err != nil {
			return fmt.Errorf("create task: advance project counter: %w", err)
		}

		task = created
		return nil
	})
	if err != nil {
		return domain.Task{}, false, err
	}

	return task, replayed, nil
}

func (r *Repo) GetTask(ctx context.Context, key string) (domain.Task, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT key, project_key, title, notes, acceptance, state, priority, size,
		       created_by, version, created_at, updated_at, closed_at
		FROM tasks WHERE key = ?`, key)

	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, &domain.RuleError{
			Code: app.CodeNotFound,
			Msg:  fmt.Sprintf("unknown task %q", key),
		}
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task %q: %w", key, err)
	}

	return task, nil
}

func (r *Repo) ListTasks(ctx context.Context, f app.Filter) ([]domain.Task, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = listDefaultLimit
	}
	if limit > listMaxLimit {
		limit = listMaxLimit
	}
	if f.State != "" && !f.State.Valid() {
		return nil, "", invalidInput("unknown state filter")
	}

	var position *listCursor
	if f.Cursor != "" {
		decoded, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", invalidInput("cursor is not valid")
		}
		if decoded.Project != f.ProjectKey || decoded.State != string(f.State) {
			return nil, "", invalidInput("cursor does not belong to this filter")
		}
		position = &decoded
	}

	query := strings.Builder{}
	query.WriteString(`
		SELECT key, project_key, title, notes, acceptance, state, priority, size,
		       created_by, version, created_at, updated_at, closed_at
		FROM tasks WHERE 1 = 1`)
	args := make([]any, 0, 5)
	if f.ProjectKey != "" {
		query.WriteString(` AND project_key = ?`)
		args = append(args, f.ProjectKey)
	}
	if f.State != "" {
		query.WriteString(` AND state = ?`)
		args = append(args, string(f.State))
	}
	if position != nil {
		query.WriteString(` AND (created_at > ? OR (created_at = ? AND key > ?))`)
		args = append(args, position.CreatedAt, position.CreatedAt, position.Key)
	}
	query.WriteString(` ORDER BY created_at, key LIMIT ?`)
	args = append(args, limit+1)

	rows, err := r.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]domain.Task, 0, limit)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, "", fmt.Errorf("list tasks: scan: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list tasks: %w", err)
	}

	var nextCursor string
	if len(tasks) > limit {
		tasks = tasks[:limit]
		last := tasks[len(tasks)-1]
		nextCursor = encodeCursor(listCursor{
			CreatedAt: last.CreatedAt,
			Key:       last.Key,
			Project:   f.ProjectKey,
			State:     string(f.State),
		})
	}

	return tasks, nextCursor, nil
}

func (r *Repo) ListProjects(ctx context.Context) ([]app.Project, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT key, name FROM projects ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	projects := make([]app.Project, 0, 3)
	for rows.Next() {
		var project app.Project
		if err := rows.Scan(&project.Key, &project.Name); err != nil {
			return nil, fmt.Errorf("list projects: scan: %w", err)
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return projects, nil
}

func (r *Repo) ListBoardTasks(ctx context.Context, projectKey string) ([]domain.Task, error) {
	query := `
		SELECT key, project_key, title, notes, acceptance, state, priority, size,
		       created_by, version, created_at, updated_at, closed_at
		FROM tasks`
	args := make([]any, 0, 1)
	if projectKey != "" {
		query += ` WHERE project_key = ?`
		args = append(args, projectKey)
	}
	query += ` ORDER BY created_at, key`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list board tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]domain.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list board tasks: scan: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list board tasks: %w", err)
	}
	return tasks, nil
}

func (r *Repo) UpdateTask(ctx context.Context, t domain.Task, m domain.Movement, expectedVersion int64) error {
	return r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
			UPDATE tasks
			SET title = ?, notes = ?, acceptance = ?, state = ?, priority = ?,
			    size = ?, version = ?, updated_at = ?, closed_at = ?
			WHERE key = ? AND version = ?`,
			t.Title, nullableText(t.Notes), nullableText(t.Acceptance), string(t.State),
			string(t.Priority), nullableSize(t.Size), t.Version, t.UpdatedAt,
			nullableInt64(t.ClosedAt), t.Key, expectedVersion)
		if err != nil {
			return fmt.Errorf("update task %q: %w", t.Key, err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("update task %q: rows affected: %w", t.Key, err)
		}
		if affected == 0 {
			var exists int
			err := conn.QueryRowContext(ctx,
				`SELECT 1 FROM tasks WHERE key = ?`, t.Key).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{
					Code: app.CodeNotFound,
					Msg:  fmt.Sprintf("unknown task %q", t.Key),
				}
			}
			if err != nil {
				return fmt.Errorf("update task %q: check existence: %w", t.Key, err)
			}
			return &domain.RuleError{
				Code: domain.CodeStaleVersion,
				Msg:  fmt.Sprintf("task %q changed since it was read", t.Key),
			}
		}

		if m.ToState != "" {
			if err := insertMovement(ctx, conn, m); err != nil {
				return err
			}
		}

		if reason, ok := leaseCloseReason(m.ToState); ok {
			if err := closeLiveLease(ctx, conn, t.Key, m.At, reason); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) CountDeliveries(ctx context.Context, taskKey string) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM deliveries WHERE task_key = ?`, taskKey).Scan(&count); err != nil {
		return 0, fmt.Errorf("count deliveries for %q: %w", taskKey, err)
	}
	return count, nil
}

func (r *Repo) GetLiveLease(ctx context.Context, taskKey string, now int64) (*domain.Lease, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT l.id, l.task_key, l.agent_id, l.created_at, l.expires_at,
		       l.released_at, l.closed_reason, u.name, u.kind
		FROM leases l
		JOIN users u ON u.id = l.agent_id
		WHERE l.task_key = ? AND l.released_at IS NULL AND l.expires_at > ?
		ORDER BY l.id DESC LIMIT 1`, taskKey, now)

	lease, err := scanLease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get live lease for %q: %w", taskKey, err)
	}
	return &lease, nil
}

func (r *Repo) AcquireLease(ctx context.Context, t domain.Task, l domain.Lease, m domain.Movement, expectedVersion int64) error {
	return r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		now := l.CreatedAt

		var (
			oldID      int64
			oldExpires int64
		)
		err := conn.QueryRowContext(ctx, `
			SELECT id, expires_at FROM leases
			WHERE task_key = ? AND released_at IS NULL
			ORDER BY id DESC LIMIT 1`, t.Key).Scan(&oldID, &oldExpires)
		switch {
		case err == nil:
			if oldExpires > now {
				return leaseConflict("task already has a live lease")
			}
			if _, err := conn.ExecContext(ctx, `
				UPDATE leases SET released_at = ?, closed_reason = 'expired'
				WHERE id = ? AND released_at IS NULL`, now, oldID); err != nil {
				return fmt.Errorf("acquire lease on %q: close expired lease: %w", t.Key, err)
			}
		case errors.Is(err, sql.ErrNoRows):
		default:
			return fmt.Errorf("acquire lease on %q: read previous lease: %w", t.Key, err)
		}

		result, err := conn.ExecContext(ctx, `
			UPDATE tasks
			SET title = ?, notes = ?, acceptance = ?, state = ?, priority = ?,
			    size = ?, version = ?, updated_at = ?, closed_at = ?
			WHERE key = ? AND version = ?`,
			t.Title, nullableText(t.Notes), nullableText(t.Acceptance), string(t.State),
			string(t.Priority), nullableSize(t.Size), t.Version, t.UpdatedAt,
			nullableInt64(t.ClosedAt), t.Key, expectedVersion)
		if err != nil {
			return fmt.Errorf("acquire lease on %q: update task: %w", t.Key, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("acquire lease on %q: rows affected: %w", t.Key, err)
		}
		if affected == 0 {
			var exists int
			err := conn.QueryRowContext(ctx,
				`SELECT 1 FROM tasks WHERE key = ?`, t.Key).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{
					Code: app.CodeNotFound,
					Msg:  fmt.Sprintf("unknown task %q", t.Key),
				}
			}
			if err != nil {
				return fmt.Errorf("acquire lease on %q: check existence: %w", t.Key, err)
			}
			return leaseConflict("task already has a live lease")
		}

		if _, err := conn.ExecContext(ctx, `
			INSERT INTO leases (task_key, agent_id, created_at, expires_at)
			VALUES (?, ?, ?, ?)`,
			l.TaskKey, l.AgentID, l.CreatedAt, l.ExpiresAt); err != nil {
			return fmt.Errorf("acquire lease on %q: insert lease: %w", t.Key, err)
		}

		return insertMovement(ctx, conn, m)
	})
}

func (r *Repo) RenewLease(ctx context.Context, leaseID int64, agentID int64, expiresAt int64) error {
	now := expiresAt - app.LeaseTTLSeconds
	result, err := r.db.ExecContext(ctx, `
		UPDATE leases SET expires_at = ?
		WHERE id = ? AND agent_id = ? AND released_at IS NULL AND expires_at > ?`,
		expiresAt, leaseID, agentID, now)
	if err != nil {
		return fmt.Errorf("renew lease %d: %w", leaseID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("renew lease %d: rows affected: %w", leaseID, err)
	}
	if affected == 0 {
		return leaseConflict("the lease is not a live lease owned by this agent")
	}
	return nil
}

func (r *Repo) ReleaseLease(ctx context.Context, leaseID int64, agentID int64, at int64, reason string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE leases SET released_at = ?, closed_reason = ?
		WHERE id = ? AND agent_id = ? AND released_at IS NULL AND expires_at > ?`,
		at, reason, leaseID, agentID, at)
	if err != nil {
		return fmt.Errorf("release lease %d: %w", leaseID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("release lease %d: rows affected: %w", leaseID, err)
	}
	if affected == 0 {
		return leaseConflict("the lease is not a live lease owned by this agent")
	}
	return nil
}

func (r *Repo) CloseLiveLease(ctx context.Context, taskKey string, at int64, reason string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE leases SET released_at = ?, closed_reason = ?
		WHERE task_key = ? AND released_at IS NULL`, at, reason, taskKey)
	if err != nil {
		return false, fmt.Errorf("close lease for %q: %w", taskKey, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("close lease for %q: rows affected: %w", taskKey, err)
	}
	return affected > 0, nil
}

func (r *Repo) Deliver(ctx context.Context, t domain.Task, d domain.Delivery, m domain.Movement, expectedVersion int64) error {
	return r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `
			UPDATE tasks
			SET title = ?, notes = ?, acceptance = ?, state = ?, priority = ?,
			    size = ?, version = ?, updated_at = ?, closed_at = ?
			WHERE key = ? AND version = ?`,
			t.Title, nullableText(t.Notes), nullableText(t.Acceptance), string(t.State),
			string(t.Priority), nullableSize(t.Size), t.Version, t.UpdatedAt,
			nullableInt64(t.ClosedAt), t.Key, expectedVersion)
		if err != nil {
			return fmt.Errorf("deliver to %q: update task: %w", t.Key, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("deliver to %q: rows affected: %w", t.Key, err)
		}
		if affected == 0 {
			var exists int
			err := conn.QueryRowContext(ctx,
				`SELECT 1 FROM tasks WHERE key = ?`, t.Key).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{
					Code: app.CodeNotFound,
					Msg:  fmt.Sprintf("unknown task %q", t.Key),
				}
			}
			if err != nil {
				return fmt.Errorf("deliver to %q: check existence: %w", t.Key, err)
			}
			return &domain.RuleError{
				Code: domain.CodeStaleVersion,
				Msg:  fmt.Sprintf("task %q changed since it was read", t.Key),
			}
		}

		evidence, err := json.Marshal(d.Evidence)
		if err != nil {
			return fmt.Errorf("deliver to %q: encode evidence: %w", t.Key, err)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO deliveries (task_key, lease_id, summary, evidence, created_at, actor_id)
			VALUES (?, ?, ?, ?, ?, ?)`,
			d.TaskKey, nullableInt64(d.LeaseID), d.Summary, string(evidence),
			d.CreatedAt, d.ActorID); err != nil {
			return fmt.Errorf("deliver to %q: insert delivery: %w", t.Key, err)
		}

		return insertMovement(ctx, conn, m)
	})
}

func (r *Repo) ListLeases(ctx context.Context, taskKey string) ([]domain.Lease, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT l.id, l.task_key, l.agent_id, l.created_at, l.expires_at,
		       l.released_at, l.closed_reason, u.name, u.kind
		FROM leases l
		JOIN users u ON u.id = l.agent_id
		WHERE l.task_key = ? ORDER BY l.id`, taskKey)
	if err != nil {
		return nil, fmt.Errorf("list leases for %q: %w", taskKey, err)
	}
	defer rows.Close()

	leases := make([]domain.Lease, 0)
	for rows.Next() {
		lease, err := scanLease(rows)
		if err != nil {
			return nil, fmt.Errorf("list leases for %q: scan: %w", taskKey, err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list leases for %q: %w", taskKey, err)
	}
	return leases, nil
}

func (r *Repo) ListDeliveries(ctx context.Context, taskKey string) ([]domain.Delivery, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT d.id, d.task_key, d.lease_id, d.summary, d.evidence, d.created_at,
		       d.actor_id, u.name, u.kind
		FROM deliveries d
		JOIN users u ON u.id = d.actor_id
		WHERE d.task_key = ? ORDER BY d.id`, taskKey)
	if err != nil {
		return nil, fmt.Errorf("list deliveries for %q: %w", taskKey, err)
	}
	defer rows.Close()

	deliveries := make([]domain.Delivery, 0)
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("list deliveries for %q: scan: %w", taskKey, err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deliveries for %q: %w", taskKey, err)
	}
	return deliveries, nil
}

func (r *Repo) ListMovements(ctx context.Context, taskKey string) ([]domain.Movement, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.task_key, m.from_state, m.to_state, m.actor_id, u.name, u.kind,
		       m.reason, m.task_version, m.at
		FROM task_movements m
		JOIN users u ON u.id = m.actor_id
		WHERE m.task_key = ? ORDER BY m.id`, taskKey)
	if err != nil {
		return nil, fmt.Errorf("list movements for %q: %w", taskKey, err)
	}
	defer rows.Close()

	movements := make([]domain.Movement, 0)
	for rows.Next() {
		movement, err := scanMovement(rows)
		if err != nil {
			return nil, fmt.Errorf("list movements for %q: scan: %w", taskKey, err)
		}
		movements = append(movements, movement)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list movements for %q: %w", taskKey, err)
	}
	return movements, nil
}

func (r *Repo) CreateComment(ctx context.Context, c domain.Comment) (domain.Comment, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO comments (task_key, actor_id, body, created_at)
		VALUES (?, ?, ?, ?)`,
		c.TaskKey, c.ActorID, c.Body, c.CreatedAt)
	if err != nil {
		return domain.Comment{}, fmt.Errorf("create comment on %q: %w", c.TaskKey, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.Comment{}, fmt.Errorf("create comment on %q: last insert id: %w", c.TaskKey, err)
	}
	c.ID = id
	return c, nil
}

func (r *Repo) ListComments(ctx context.Context, taskKey string) ([]domain.Comment, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.task_key, c.actor_id, c.body, c.created_at, u.name, u.kind
		FROM comments c
		JOIN users u ON u.id = c.actor_id
		WHERE c.task_key = ? ORDER BY c.id`, taskKey)
	if err != nil {
		return nil, fmt.Errorf("list comments for %q: %w", taskKey, err)
	}
	defer rows.Close()

	comments := make([]domain.Comment, 0)
	for rows.Next() {
		var (
			comment   domain.Comment
			actorName string
			actorKind string
		)
		if err := rows.Scan(&comment.ID, &comment.TaskKey, &comment.ActorID,
			&comment.Body, &comment.CreatedAt, &actorName, &actorKind); err != nil {
			return nil, fmt.Errorf("list comments for %q: scan: %w", taskKey, err)
		}
		comment.Actor = domain.Actor{
			ID:   comment.ActorID,
			Kind: domain.ActorKind(actorKind),
			Name: actorName,
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list comments for %q: %w", taskKey, err)
	}
	return comments, nil
}

func (r *Repo) withTx(ctx context.Context, fn func(context.Context, *sql.Conn) error) (err error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}()

	if err := fn(ctx, conn); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true
	return nil
}

func nextTaskNumber(ctx context.Context, conn *sql.Conn, projectKey string) (int64, error) {
	var next int64
	err := conn.QueryRowContext(ctx,
		`SELECT next_task_number FROM projects WHERE key = ?`, projectKey).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, invalidInput(fmt.Sprintf("unknown project %q", projectKey))
	}
	if err != nil {
		return 0, fmt.Errorf("read project %q: %w", projectKey, err)
	}
	return next, nil
}

func leaseCloseReason(to domain.State) (string, bool) {
	switch to {
	case domain.Ready:
		return "returned", true
	case domain.Done:
		return "done", true
	case domain.Backlog:
		return "backlog", true
	default:
		return "", false
	}
}

func closeLiveLease(ctx context.Context, conn *sql.Conn, taskKey string, at int64, reason string) error {
	if _, err := conn.ExecContext(ctx, `
		UPDATE leases SET released_at = ?, closed_reason = ?
		WHERE task_key = ? AND released_at IS NULL`, at, reason, taskKey); err != nil {
		return fmt.Errorf("close lease for %q: %w", taskKey, err)
	}
	return nil
}

func leaseConflict(msg string) *domain.RuleError {
	return &domain.RuleError{Code: domain.CodeLeaseConflict, Msg: msg}
}

func insertMovement(ctx context.Context, conn *sql.Conn, m domain.Movement) error {
	var from any
	if m.FromState != nil {
		from = string(*m.FromState)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO task_movements
			(task_key, from_state, to_state, actor_id, reason, task_version, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.TaskKey, from, string(m.ToState), m.ActorID, nullableText(m.Reason),
		m.TaskVersion, m.At); err != nil {
		return fmt.Errorf("insert movement for %q: %w", m.TaskKey, err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(s scanner) (domain.Task, error) {
	var (
		task       domain.Task
		notes      sql.NullString
		acceptance sql.NullString
		state      string
		priority   string
		size       sql.NullString
		closedAt   sql.NullInt64
	)
	if err := s.Scan(
		&task.Key, &task.ProjectKey, &task.Title, &notes, &acceptance,
		&state, &priority, &size, &task.CreatedBy, &task.Version,
		&task.CreatedAt, &task.UpdatedAt, &closedAt,
	); err != nil {
		return domain.Task{}, err
	}

	task.State = domain.State(state)
	task.Priority = domain.Priority(priority)
	if notes.Valid {
		task.Notes = &notes.String
	}
	if acceptance.Valid {
		task.Acceptance = &acceptance.String
	}
	if size.Valid {
		value := domain.Size(size.String)
		task.Size = &value
	}
	if closedAt.Valid {
		value := closedAt.Int64
		task.ClosedAt = &value
	}

	return task, nil
}

func scanLease(s scanner) (domain.Lease, error) {
	var (
		lease        domain.Lease
		releasedAt   sql.NullInt64
		closedReason sql.NullString
		actorName    string
		actorKind    string
	)
	if err := s.Scan(
		&lease.ID, &lease.TaskKey, &lease.AgentID, &lease.CreatedAt,
		&lease.ExpiresAt, &releasedAt, &closedReason, &actorName, &actorKind,
	); err != nil {
		return domain.Lease{}, err
	}
	lease.Agent = domain.Actor{
		ID:   lease.AgentID,
		Kind: domain.ActorKind(actorKind),
		Name: actorName,
	}
	if releasedAt.Valid {
		value := releasedAt.Int64
		lease.ReleasedAt = &value
	}
	if closedReason.Valid {
		value := closedReason.String
		lease.ClosedReason = &value
	}
	return lease, nil
}

func scanMovement(s scanner) (domain.Movement, error) {
	var (
		movement  domain.Movement
		from      sql.NullString
		to        string
		reason    sql.NullString
		actorName string
		actorKind string
	)
	if err := s.Scan(
		&movement.TaskKey, &from, &to, &movement.ActorID, &actorName, &actorKind,
		&reason, &movement.TaskVersion, &movement.At,
	); err != nil {
		return domain.Movement{}, err
	}
	movement.Actor = domain.Actor{
		ID:   movement.ActorID,
		Kind: domain.ActorKind(actorKind),
		Name: actorName,
	}
	if from.Valid {
		value := domain.State(from.String)
		movement.FromState = &value
	}
	movement.ToState = domain.State(to)
	if reason.Valid {
		value := reason.String
		movement.Reason = &value
	}
	return movement, nil
}

func scanDelivery(s scanner) (domain.Delivery, error) {
	var (
		delivery  domain.Delivery
		leaseID   sql.NullInt64
		evidence  string
		actorName string
		actorKind string
	)
	if err := s.Scan(
		&delivery.ID, &delivery.TaskKey, &leaseID, &delivery.Summary,
		&evidence, &delivery.CreatedAt, &delivery.ActorID, &actorName, &actorKind,
	); err != nil {
		return domain.Delivery{}, err
	}
	delivery.Actor = domain.Actor{
		ID:   delivery.ActorID,
		Kind: domain.ActorKind(actorKind),
		Name: actorName,
	}
	if leaseID.Valid {
		value := leaseID.Int64
		delivery.LeaseID = &value
	}
	if err := json.Unmarshal([]byte(evidence), &delivery.Evidence); err != nil {
		return domain.Delivery{}, fmt.Errorf("decode delivery %d evidence: %w", delivery.ID, err)
	}
	return delivery, nil
}

type idempotencyRecord struct {
	actorID int64
	digest  string
	body    string
}

func readIdempotency(ctx context.Context, conn *sql.Conn, key string) (idempotencyRecord, error) {
	var record idempotencyRecord
	err := conn.QueryRowContext(ctx, `
		SELECT actor_id, request_digest, response_body
		FROM idempotency_records WHERE key = ?`, key).
		Scan(&record.actorID, &record.digest, &record.body)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return idempotencyRecord{}, sql.ErrNoRows
		}
		return idempotencyRecord{}, fmt.Errorf("read idempotency record %q: %w", key, err)
	}
	return record, nil
}

type listCursor struct {
	CreatedAt int64  `json:"c"`
	Key       string `json:"k"`
	Project   string `json:"p"`
	State     string `json:"s"`
}

func encodeCursor(c listCursor) string {
	raw, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(value string) (listCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, err
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return listCursor{}, err
	}
	if c.Key == "" {
		return listCursor{}, errors.New("cursor has no key")
	}
	return c, nil
}

func digestOf(digest string) string {
	if strings.TrimSpace(digest) == "" {
		return "none"
	}
	return digest
}

func nullableText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableSize(size *domain.Size) any {
	if size == nil {
		return nil
	}
	return string(*size)
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func invalidInput(msg string) *domain.RuleError {
	return &domain.RuleError{Code: domain.CodeInvalidInput, Msg: msg}
}
