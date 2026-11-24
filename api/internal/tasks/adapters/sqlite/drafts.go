package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

var _ app.PlanDraftStore = (*Repo)(nil)

func validateDraft(input app.DraftInput) error {
	if strings.TrimSpace(input.Project) == "" {
		return invalidInput("project is required")
	}
	outcome := strings.TrimSpace(input.Outcome)
	if outcome == "" || len([]rune(outcome)) > 2000 {
		return invalidInput("outcome must be between 1 and 2000 characters")
	}
	if strings.TrimSpace(input.Scenario) == "" {
		return invalidInput("the acceptance scenario command is required")
	}
	if len(strings.TrimSpace(input.TargetEnvironment)) > 40 {
		return invalidInput("the target environment name is at most 40 characters")
	}
	if len(input.Tasks) < 2 {
		return invalidInput("a plan needs at least two tasks")
	}
	if len(input.Tasks) > 30 {
		return invalidInput("a plan takes at most 30 tasks")
	}
	keys := make([]string, 0, len(input.Tasks))
	seen := make(map[string]struct{}, len(input.Tasks))
	for _, task := range input.Tasks {
		key := strings.TrimSpace(task.Key)
		if key == "" || len(key) > 16 {
			return invalidInput("every proposed task needs a key of at most 16 characters")
		}
		if _, ok := seen[key]; ok {
			return invalidInput("a proposed task appears twice")
		}
		seen[key] = struct{}{}
		title := strings.TrimSpace(task.Title)
		if title == "" || len([]rune(title)) > 200 {
			return invalidInput("every proposed task needs a title of at most 200 characters")
		}
		if task.Notes != nil && len([]rune(*task.Notes)) > 50000 {
			return invalidInput("proposed notes must be at most 50000 characters")
		}
		if task.Acceptance != nil && len([]rune(*task.Acceptance)) > 10000 {
			return invalidInput("proposed acceptance must be at most 10000 characters")
		}
		if task.Size != nil {
			size := domain.Size(*task.Size)
			if !size.Valid() {
				return invalidInput("proposed size must be S, M or L")
			}
		}
		keys = append(keys, key)
	}
	edges := make([]domain.PlanEdge, 0, len(input.Edges))
	for _, edge := range input.Edges {
		kind := edge.Kind
		if kind == "" {
			kind = domain.EdgeIntegration
		}
		edges = append(edges, domain.PlanEdge{From: edge.From, To: edge.To, Kind: kind})
	}
	if err := domain.ValidatePlanEdges(keys, edges); err != nil {
		return err
	}
	if input.VisualReview {
		reveal := strings.TrimSpace(input.RevealKey)
		if reveal == "" {
			return invalidInput("a proposal with visual review needs the task that reveals it")
		}
		if _, ok := seen[reveal]; !ok {
			return invalidInput("the revealing task must belong to the proposal")
		}
		if input.VisibleChange == nil || strings.TrimSpace(*input.VisibleChange) == "" {
			return invalidInput("a proposal with visual review needs the change that makes it visible")
		}
	} else if strings.TrimSpace(input.RevealKey) != "" {
		return invalidInput("only a proposal with visual review names a revealing task")
	}
	return nil
}

func (r *Repo) CreateDraft(
	ctx context.Context,
	actor domain.Actor,
	input app.DraftInput,
	policy app.PlanPolicy,
	now int64,
) (app.PlanDraft, error) {
	if actor.Kind != domain.Agent && actor.Kind != domain.Human {
		return app.PlanDraft{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "a proposal needs an identity"}
	}
	if err := validateDraft(input); err != nil {
		return app.PlanDraft{}, err
	}
	var draftID int64
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var exists bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE key = ?)`,
			strings.TrimSpace(input.Project)).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return &domain.RuleError{Code: domain.CodeNotFound,
				Msg: fmt.Sprintf("unknown project %q", input.Project)}
		}
		if err := requireDraftEnvironment(ctx, conn, strings.TrimSpace(input.Project), input.TargetEnvironment); err != nil {
			return err
		}
		policy, err := projectPlanPolicy(ctx, conn, strings.TrimSpace(input.Project), policy)
		if err != nil {
			return err
		}
		err = conn.QueryRowContext(ctx, `INSERT INTO plan_drafts
			(project_key, author_id, repository, branch, workflow_id, outcome, scenario, target_environment,
			 visual_review, visible_change, reveal_key, version, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 'draft', ?, ?) RETURNING id`,
			strings.TrimSpace(input.Project), actor.ID, policy.Repository, policy.Branch,
			policy.WorkflowID, strings.TrimSpace(input.Outcome), strings.TrimSpace(input.Scenario),
			strings.TrimSpace(input.TargetEnvironment),
			boolFlag(input.VisualReview), nullableText(input.VisibleChange),
			nullableKey(input.RevealKey), now, now).Scan(&draftID)
		if err != nil {
			return fmt.Errorf("create draft: %w", err)
		}
		return writeDraftContent(ctx, conn, draftID, input, now)
	})
	if err != nil {
		return app.PlanDraft{}, err
	}
	return r.Draft(ctx, actor, draftID)
}

func writeDraftContent(ctx context.Context, conn *sql.Conn, draftID int64, input app.DraftInput, now int64) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM plan_draft_edges WHERE draft_id = ?`, draftID); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM plan_draft_tasks WHERE draft_id = ?`, draftID); err != nil {
		return err
	}
	for position, task := range input.Tasks {
		if _, err := conn.ExecContext(ctx, `INSERT INTO plan_draft_tasks
			(draft_id, key, position, title, notes, acceptance, size)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			draftID, strings.TrimSpace(task.Key), position, strings.TrimSpace(task.Title),
			nullableText(task.Notes), nullableText(task.Acceptance), nullableSizeText(task.Size)); err != nil {
			return fmt.Errorf("draft task: %w", err)
		}
	}
	for _, edge := range input.Edges {
		kind := edge.Kind
		if kind == "" {
			kind = domain.EdgeIntegration
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO plan_draft_edges
			(draft_id, from_key, to_key, kind) VALUES (?, ?, ?, ?)`,
			draftID, edge.From, edge.To, kind); err != nil {
			return fmt.Errorf("draft edge: %w", err)
		}
	}
	return nil
}

func (r *Repo) Draft(ctx context.Context, actor domain.Actor, id int64) (app.PlanDraft, error) {
	if actor.Kind == domain.Viewer {
		return app.PlanDraft{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "a credential is required to read a proposal"}
	}
	return r.readDraft(ctx, r.db, id)
}

func (r *Repo) ListDrafts(ctx context.Context, actor domain.Actor, project string) ([]app.PlanDraftSummary, error) {
	if actor.Kind == domain.Viewer {
		return nil, &domain.RuleError{Code: domain.CodeForbidden, Msg: "a credential is required to read a proposal"}
	}
	query := `SELECT id, project_key, version, state, plan_id FROM plan_drafts`
	args := make([]any, 0, 1)
	if trimmed := strings.TrimSpace(project); trimmed != "" {
		query += ` WHERE project_key = ?`
		args = append(args, trimmed)
	}
	query += ` ORDER BY id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := make([]app.PlanDraftSummary, 0)
	for rows.Next() {
		var draft app.PlanDraftSummary
		var planID sql.NullInt64
		if err := rows.Scan(&draft.ID, &draft.Project, &draft.Version, &draft.State, &planID); err != nil {
			return nil, err
		}
		if planID.Valid {
			value := planID.Int64
			draft.PlanID = &value
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

type draftQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (r *Repo) readDraft(ctx context.Context, q draftQuerier, id int64) (app.PlanDraft, error) {
	var draft app.PlanDraft
	var visibleChange, revealKey sql.NullString
	var visualReview int
	var approvedBy, approvedAt, planID sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT id, project_key, author_id, repository, branch, workflow_id,
		outcome, scenario, target_environment, visual_review, visible_change, reveal_key, version, state,
		approved_by, approved_at, plan_id, created_at, updated_at
		FROM plan_drafts WHERE id = ?`, id).
		Scan(&draft.ID, &draft.Project, &draft.AuthorID, &draft.Policy.Repository, &draft.Policy.Branch,
			&draft.Policy.WorkflowID, &draft.Outcome, &draft.Scenario, &draft.TargetEnvironment,
			&visualReview, &visibleChange,
			&revealKey, &draft.Version, &draft.State, &approvedBy, &approvedAt, &planID,
			&draft.CreatedAt, &draft.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return draft, &domain.RuleError{Code: domain.CodeNotFound, Msg: "proposal not found"}
	}
	if err != nil {
		return draft, err
	}
	draft.VisualReview = visualReview == 1
	if visibleChange.Valid {
		value := visibleChange.String
		draft.VisibleChange = &value
	}
	if revealKey.Valid {
		value := revealKey.String
		draft.RevealKey = &value
	}
	if approvedBy.Valid {
		value := approvedBy.Int64
		draft.ApprovedBy = &value
	}
	if approvedAt.Valid {
		value := approvedAt.Int64
		draft.ApprovedAt = &value
	}
	if planID.Valid {
		value := planID.Int64
		draft.PlanID = &value
	}

	rows, err := q.QueryContext(ctx, `SELECT key, title, notes, acceptance, size, task_key
		FROM plan_draft_tasks WHERE draft_id = ? ORDER BY position`, id)
	if err != nil {
		return draft, err
	}
	defer rows.Close()
	tasks := make([]app.DraftTask, 0)
	for rows.Next() {
		var task app.DraftTask
		var notes, acceptance, size, taskKey sql.NullString
		if err := rows.Scan(&task.Key, &task.Title, &notes, &acceptance, &size, &taskKey); err != nil {
			return draft, err
		}
		if notes.Valid {
			value := notes.String
			task.Notes = &value
		}
		if acceptance.Valid {
			value := acceptance.String
			task.Acceptance = &value
		}
		if size.Valid {
			value := size.String
			task.Size = &value
		}
		if taskKey.Valid {
			value := taskKey.String
			task.TaskKey = &value
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return draft, err
	}
	draft.Tasks = tasks

	edges, err := draftEdges(ctx, q, id)
	if err != nil {
		return draft, err
	}
	draft.Edges = edges
	draft.Roots = draftRoots(tasks, edges)
	return draft, nil
}

func draftEdges(ctx context.Context, q draftQuerier, id int64) ([]app.DraftEdge, error) {
	rows, err := q.QueryContext(ctx, `SELECT from_key, to_key, kind FROM plan_draft_edges
		WHERE draft_id = ? ORDER BY from_key, to_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edges := make([]app.DraftEdge, 0)
	for rows.Next() {
		var edge app.DraftEdge
		if err := rows.Scan(&edge.From, &edge.To, &edge.Kind); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func draftRoots(tasks []app.DraftTask, edges []app.DraftEdge) []string {
	incoming := make(map[string]struct{}, len(edges))
	for _, edge := range edges {
		incoming[edge.To] = struct{}{}
	}
	roots := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if _, ok := incoming[task.Key]; !ok {
			roots = append(roots, task.Key)
		}
	}
	return roots
}

func requireDraftEnvironment(ctx context.Context, conn *sql.Conn, project, name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	var exists bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environments
		WHERE project_key = ? AND name = ?)`, project, trimmed).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return invalidInput("the target environment must be registered in the project")
	}
	return nil
}

func (r *Repo) UpdateDraft(
	ctx context.Context,
	actor domain.Actor,
	id int64,
	version int64,
	input app.DraftInput,
	now int64,
) (app.PlanDraft, error) {
	if actor.Kind != domain.Agent && actor.Kind != domain.Human {
		return app.PlanDraft{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "a proposal needs an identity"}
	}
	if err := validateDraft(input); err != nil {
		return app.PlanDraft{}, err
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var current app.PlanDraft
		var state string
		var project string
		if err := conn.QueryRowContext(ctx, `SELECT project_key, version, state FROM plan_drafts WHERE id = ?`,
			id).Scan(&project, &current.Version, &state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{Code: domain.CodeNotFound, Msg: "proposal not found"}
			}
			return err
		}
		if state != app.DraftDraft {
			return planConflict("an approved proposal cannot be edited")
		}
		if current.Version != version {
			return &domain.RuleError{Code: domain.CodeStaleVersion,
				Msg: fmt.Sprintf("If-Match %d does not match proposal version %d", version, current.Version)}
		}
		if strings.TrimSpace(input.Project) != project {
			return planConflict("a proposal cannot change its project")
		}
		if err := requireDraftEnvironment(ctx, conn, project, input.TargetEnvironment); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `UPDATE plan_drafts SET outcome = ?, scenario = ?,
			target_environment = ?, visual_review = ?, visible_change = ?, reveal_key = ?,
			version = version + 1, updated_at = ?
			WHERE id = ? AND version = ?`,
			strings.TrimSpace(input.Outcome), strings.TrimSpace(input.Scenario),
			strings.TrimSpace(input.TargetEnvironment), boolFlag(input.VisualReview),
			nullableText(input.VisibleChange), nullableKey(input.RevealKey), now, id, version); err != nil {
			return fmt.Errorf("update draft: %w", err)
		}
		return writeDraftContent(ctx, conn, id, input, now)
	})
	if err != nil {
		return app.PlanDraft{}, err
	}
	return r.Draft(ctx, actor, id)
}

func (r *Repo) DiscardDraft(ctx context.Context, actor domain.Actor, id int64, version int64) (app.PlanDraft, error) {
	if actor.Kind != domain.Human {
		return app.PlanDraft{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human may discard a proposal"}
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var state string
		var current int64
		if err := conn.QueryRowContext(ctx, `SELECT state, version FROM plan_drafts WHERE id = ?`,
			id).Scan(&state, &current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{Code: domain.CodeNotFound, Msg: "proposal not found"}
			}
			return err
		}
		if state == app.DraftDiscarded {
			return nil
		}
		if state == app.DraftApproved {
			return planConflict("an approved proposal cannot be discarded")
		}
		if current != version {
			return &domain.RuleError{Code: domain.CodeStaleVersion,
				Msg: fmt.Sprintf("If-Match %d does not match proposal version %d", version, current)}
		}
		_, err := conn.ExecContext(ctx, `UPDATE plan_drafts SET state = 'discarded' WHERE id = ?`, id)
		return err
	})
	if err != nil {
		return app.PlanDraft{}, err
	}
	return r.Draft(ctx, actor, id)
}

func (r *Repo) ApproveDraft(
	ctx context.Context,
	actor domain.Actor,
	id int64,
	version int64,
	policy app.PlanPolicy,
	now int64,
) (app.PlanDraft, error) {
	if actor.Kind != domain.Human {
		return app.PlanDraft{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human may approve a proposal"}
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		draft, err := r.readDraft(ctx, conn, id)
		if err != nil {
			return err
		}
		if draft.State == app.DraftApproved {
			return nil
		}
		if draft.State == app.DraftDiscarded {
			return planConflict("the proposal was discarded")
		}
		if draft.Version != version {
			return &domain.RuleError{Code: domain.CodeStaleVersion,
				Msg: fmt.Sprintf("If-Match %d does not match proposal version %d", version, draft.Version)}
		}
		policy, err = projectPlanPolicy(ctx, conn, draft.Project, policy)
		if err != nil {
			return err
		}
		if draft.Policy != policy {
			return planConflict("the plan verification changed since the proposal; propose it again")
		}

		keys := make([]string, 0, len(draft.Tasks))
		mapped := make(map[string]string, len(draft.Tasks))
		for _, task := range draft.Tasks {
			number, err := nextTaskNumber(ctx, conn, draft.Project)
			if err != nil {
				return err
			}
			key := fmt.Sprintf("%s-%d", draft.Project, number)
			idempotency := fmt.Sprintf("draft:%d:%s", id, task.Key)
			if _, err := conn.ExecContext(ctx, `INSERT INTO idempotency_records
				(key, actor_id, request_digest, response_body, status_code, created_at)
				VALUES (?, ?, 'proposal', '{}', 201, ?)`, idempotency, actor.ID, now); err != nil {
				return fmt.Errorf("draft approval idempotency: %w", err)
			}
			if _, err := conn.ExecContext(ctx, `INSERT INTO tasks
				(key, project_key, title, notes, acceptance, state, priority, size, created_by,
				 version, created_at, updated_at, idempotency_key)
				VALUES (?, ?, ?, ?, ?, 'backlog', 'normal', ?, ?, 1, ?, ?, ?)`,
				key, draft.Project, task.Title, nullableText(task.Notes), nullableText(task.Acceptance),
				nullableSizeText(task.Size), actor.ID, now, now, idempotency); err != nil {
				return fmt.Errorf("draft approval task: %w", err)
			}
			if _, err := conn.ExecContext(ctx, `UPDATE projects
				SET next_task_number = next_task_number + 1 WHERE key = ?`, draft.Project); err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, `UPDATE plan_draft_tasks SET task_key = ?
				WHERE draft_id = ? AND key = ?`, key, id, task.Key); err != nil {
				return err
			}
			if err := insertMovement(ctx, conn, domain.Movement{
				TaskKey: key, ToState: domain.Backlog, ActorID: actor.ID, TaskVersion: 1, At: now,
			}); err != nil {
				return err
			}
			keys = append(keys, key)
			mapped[task.Key] = key
		}

		edges := make([]domain.PlanEdge, 0, len(draft.Edges))
		for _, edge := range draft.Edges {
			edges = append(edges, domain.PlanEdge{From: mapped[edge.From], To: mapped[edge.To], Kind: edge.Kind})
		}
		reveal := ""
		if draft.RevealKey != nil {
			reveal = mapped[*draft.RevealKey]
		}
		planID, err := r.activatePlan(ctx, conn, actor.ID, draft.Project, keys, edges, policy,
			app.PlanReview{
				Scenario:          draft.Scenario,
				TargetEnvironment: draft.TargetEnvironment,
				VisibleChange:     draft.VisibleChange,
				VisualReview:      draft.VisualReview,
				RevealTask:        reveal,
			}, now)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `UPDATE plan_drafts
			SET state = 'approved', approved_by = ?, approved_at = ?, plan_id = ? WHERE id = ?`,
			actor.ID, now, planID, id); err != nil {
			return fmt.Errorf("approve draft: %w", err)
		}
		return nil
	})
	if err != nil {
		return app.PlanDraft{}, err
	}
	return r.Draft(ctx, actor, id)
}

func nullableKey(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func nullableSizeText(value *string) any {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}
