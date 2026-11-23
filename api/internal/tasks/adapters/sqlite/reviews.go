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

func (r *Repo) ReportScenario(
	ctx context.Context,
	actor domain.Actor,
	planID int64,
	report app.ScenarioReport,
	now int64,
) (app.PlanGraph, error) {
	if actor.Kind != domain.Agent {
		return app.PlanGraph{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only the agent working the card reports the scenario"}
	}
	head := strings.ToLower(strings.TrimSpace(report.HeadSHA))
	if !commitSHA(head) {
		return app.PlanGraph{}, invalidInput("head_sha must be the full 40-character commit")
	}
	if strings.TrimSpace(report.Command) == "" {
		return app.PlanGraph{}, invalidInput("the scenario command is required")
	}
	if report.Outcome != app.ScenarioPassed && report.Outcome != app.ScenarioFailed {
		return app.PlanGraph{}, invalidInput("scenario outcome must be passed or failed")
	}
	if report.EvidenceURL == nil || strings.TrimSpace(*report.EvidenceURL) == "" {
		return app.PlanGraph{}, invalidInput("the evidence link of the scenario is required")
	}

	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var state string
		var completedAt sql.NullInt64
		if err := conn.QueryRowContext(ctx, `SELECT state, completed_at FROM plans WHERE id = ?`,
			planID).Scan(&state, &completedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan not found"}
			}
			return err
		}
		if state != domain.PlanActive || completedAt.Valid {
			return planConflict("the plan is not running")
		}
		var taskState string
		err := conn.QueryRowContext(ctx, `SELECT t.state FROM plan_nodes n
			JOIN tasks t ON t.key = n.task_key WHERE n.plan_id = ? AND n.task_key = ?`,
			planID, report.TaskKey).Scan(&taskState)
		if errors.Is(err, sql.ErrNoRows) {
			return planConflict("the task is not part of the plan")
		}
		if err != nil {
			return err
		}
		if taskState != string(domain.InProgress) {
			return planConflict("the scenario is reported while the card is in progress")
		}
		var leased bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leases
			WHERE task_key = ? AND agent_id = ? AND released_at IS NULL AND expires_at > ?)`,
			report.TaskKey, actor.ID, now).Scan(&leased); err != nil {
			return err
		}
		if !leased {
			return &domain.RuleError{Code: domain.CodeLeaseRequired, Msg: "the agent needs a live lease it owns"}
		}
		var declared string
		err = conn.QueryRowContext(ctx, `SELECT head_sha FROM submissions
			WHERE task_key = ? ORDER BY id DESC LIMIT 1`, report.TaskKey).Scan(&declared)
		switch {
		case err == nil && declared != head:
			return planConflict("the reported revision is not the one declared for the card")
		case err == nil, errors.Is(err, sql.ErrNoRows):
		default:
			return err
		}

		var existing string
		err = conn.QueryRowContext(ctx, `SELECT outcome FROM plan_scenarios
			WHERE plan_id = ? AND task_key = ? AND head_sha = ?`, planID, report.TaskKey, head).Scan(&existing)
		switch {
		case err == nil && existing == report.Outcome:
			return nil
		case err == nil:
			return planConflict("the scenario for that revision was already reported")
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO plan_scenarios
			(plan_id, task_key, head_sha, command, outcome, evidence_url, notes, recorded_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			planID, report.TaskKey, head, strings.TrimSpace(report.Command), report.Outcome,
			nullableText(report.EvidenceURL), nullableText(report.Notes), now); err != nil {
			return fmt.Errorf("report scenario: %w", err)
		}
		if report.Outcome == app.ScenarioFailed {
			var evidence *string
			if report.EvidenceURL != nil && strings.TrimSpace(*report.EvidenceURL) != "" {
				value := strings.TrimSpace(*report.EvidenceURL)
				evidence = &value
			}
			if _, err := r.ensureCorrection(ctx, conn, planID, report.TaskKey, "scenario:"+head,
				"the acceptance scenario failed", evidence, now); err != nil {
				return err
			}
		}
		return r.settlePlan(ctx, conn, planID, now)
	})
	if err != nil {
		return app.PlanGraph{}, err
	}
	return r.Graph(ctx, planID)
}

func (r *Repo) PendingPlanReviews(ctx context.Context) ([]app.PendingPlanReview, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, n.task_key, d.pr_url, lower(d.head_sha), p.repository
		FROM plans p
		JOIN plan_nodes n ON n.plan_id = p.id AND n.reveals = 1
		JOIN deliveries d ON d.id = (SELECT MAX(id) FROM deliveries
		                             WHERE task_key = n.task_key AND pr_url IS NOT NULL
		                               AND head_sha IS NOT NULL)
		WHERE p.state = 'active' AND p.visual_review = 1 AND p.completed_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM plan_approvals a
		                  WHERE a.plan_id = p.id AND a.task_key = n.task_key
		                    AND a.head_sha = lower(d.head_sha))
		ORDER BY p.id`)
	if err != nil {
		return nil, fmt.Errorf("pending plan reviews: %w", err)
	}
	defer rows.Close()

	pending := make([]app.PendingPlanReview, 0)
	for rows.Next() {
		var item app.PendingPlanReview
		if err := rows.Scan(&item.PlanID, &item.TaskKey, &item.PRURL, &item.HeadSHA,
			&item.Repository); err != nil {
			return nil, fmt.Errorf("pending plan reviews: scan: %w", err)
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (r *Repo) RecordApproval(
	ctx context.Context,
	planID int64,
	taskKey string,
	headSHA string,
	reviewer string,
	now int64,
) (app.PlanGraph, error) {
	head := strings.ToLower(strings.TrimSpace(headSHA))
	if !commitSHA(head) {
		return app.PlanGraph{}, invalidInput("head_sha must be the full 40-character commit")
	}
	if strings.TrimSpace(reviewer) == "" {
		return app.PlanGraph{}, invalidInput("the reviewer is required")
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `INSERT INTO plan_approvals
			(plan_id, task_key, head_sha, reviewer, approved_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (plan_id, task_key, head_sha) DO NOTHING`,
			planID, taskKey, head, strings.TrimSpace(reviewer), now); err != nil {
			return fmt.Errorf("record approval: %w", err)
		}
		return r.settlePlan(ctx, conn, planID, now)
	})
	if err != nil {
		return app.PlanGraph{}, err
	}
	return r.Graph(ctx, planID)
}

func (r *Repo) RequestCorrection(
	ctx context.Context,
	planID int64,
	taskKey string,
	headSHA string,
	origin string,
	reason string,
	now int64,
) (app.PlanGraph, error) {
	head := strings.ToLower(strings.TrimSpace(headSHA))
	if !commitSHA(head) || strings.TrimSpace(origin) == "" {
		return app.PlanGraph{}, invalidInput("a correction needs the revision and the event that asked for it")
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var completedAt sql.NullInt64
		if err := conn.QueryRowContext(ctx, `SELECT completed_at FROM plans WHERE id = ?`,
			planID).Scan(&completedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan not found"}
			}
			return err
		}
		if completedAt.Valid {
			return planConflict("the plan is already completed")
		}
		if _, err := r.ensureCorrection(ctx, conn, planID, taskKey, origin, reason, nil, now); err != nil {
			return err
		}
		return r.settlePlan(ctx, conn, planID, now)
	})
	if err != nil {
		return app.PlanGraph{}, err
	}
	return r.Graph(ctx, planID)
}

func (r *Repo) ensureCorrection(
	ctx context.Context,
	conn *sql.Conn,
	planID int64,
	taskKey string,
	origin string,
	reason string,
	evidence *string,
	now int64,
) (bool, error) {
	var taken bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plan_nodes
		WHERE plan_id = ? AND origin_evidence = ?)`, planID, origin).Scan(&taken); err != nil {
		return false, err
	}
	if taken {
		return false, nil
	}
	var project string
	var approvedBy int64
	if err := conn.QueryRowContext(ctx, `SELECT project_key, approved_by FROM plans WHERE id = ?`,
		planID).Scan(&project, &approvedBy); err != nil {
		return false, err
	}
	number, err := nextTaskNumber(ctx, conn, project)
	if err != nil {
		return false, err
	}
	key := fmt.Sprintf("%s-%d", project, number)
	head := strings.TrimSuffix(origin, "")
	if index := strings.LastIndex(origin, ":"); index >= 0 {
		head = origin[index+1:]
	}
	title := "Fix what the plan review found"
	if len(head) >= 7 {
		title = "Fix what the plan review found at " + head[:7]
	}
	notes := "Correction opened by the plan review. " + reason + "."
	notes += "\nThe work belongs to the plan of " + taskKey + "."
	if evidence != nil && strings.TrimSpace(*evidence) != "" {
		notes += "\nEvidence: " + strings.TrimSpace(*evidence)
	}
	notes += "\nFix what the approved criteria demand. If the fix needs a different acceptance"
	notes += " criterion, stop and ask the owner; the plan keeps the one it was approved with."

	idempotency := "correction:" + origin
	if len(idempotency) > 128 {
		idempotency = idempotency[:128]
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO idempotency_records
		(key, actor_id, request_digest, response_body, status_code, created_at)
		VALUES (?, ?, 'system', '{}', 201, ?)`, idempotency, approvedBy, now); err != nil {
		return false, fmt.Errorf("correction idempotency: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO tasks
		(key, project_key, title, notes, state, priority, created_by, version,
		 created_at, updated_at, idempotency_key)
		VALUES (?, ?, ?, ?, 'ready', 'high', ?, 1, ?, ?, ?)`,
		key, project, title, notes, approvedBy, now, now, idempotency); err != nil {
		return false, fmt.Errorf("correction task: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO plan_nodes
		(plan_id, task_key, reveals, correction, origin_evidence) VALUES (?, ?, 0, 1, ?)`,
		planID, key, origin); err != nil {
		return false, fmt.Errorf("correction node: %w", err)
	}
	from := domain.Backlog
	reasonText := "Correction requested by the plan review"
	if err := insertMovement(ctx, conn, domain.Movement{
		TaskKey: key, FromState: &from, ToState: domain.Ready,
		ActorID: approvedBy, Reason: &reasonText, TaskVersion: 1, At: now,
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repo) completePlanIfReady(ctx context.Context, conn *sql.Conn, planID int64, now int64) error {
	var completedAt sql.NullInt64
	var visual int
	var project string
	err := conn.QueryRowContext(ctx, `SELECT completed_at, visual_review, project_key
		FROM plans WHERE id = ? AND state = 'active'`, planID).Scan(&completedAt, &visual, &project)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if completedAt.Valid {
		return nil
	}

	var open int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM plan_nodes n
		JOIN tasks t ON t.key = n.task_key
		WHERE n.plan_id = ? AND t.state <> 'done'`, planID).Scan(&open); err != nil {
		return err
	}
	if open > 0 {
		return nil
	}

	var finalKey string
	if visual == 1 {
		err = conn.QueryRowContext(ctx, `SELECT task_key FROM plan_nodes
			WHERE plan_id = ? AND reveals = 1`, planID).Scan(&finalKey)
	} else {
		err = conn.QueryRowContext(ctx, `SELECT pp.task_key FROM plan_proofs pp
			WHERE pp.plan_id = ? ORDER BY pp.verified_at DESC, pp.task_key DESC LIMIT 1`,
			planID).Scan(&finalKey)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	proofs, err := r.graphProofs(ctx, conn, planID)
	if err != nil {
		return err
	}
	proof, ok := proofs[finalKey]
	if !ok {
		return nil
	}
	var outcome string
	err = conn.QueryRowContext(ctx, `SELECT outcome FROM plan_scenarios
		WHERE plan_id = ? AND task_key = ? AND head_sha = ?`,
		planID, finalKey, proof.HeadSHA).Scan(&outcome)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	case outcome != app.ScenarioPassed:
		return nil
	}
	if visual == 1 {
		var approved bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plan_approvals
			WHERE plan_id = ? AND task_key = ? AND head_sha = ?)`,
			planID, finalKey, proof.HeadSHA).Scan(&approved); err != nil {
			return err
		}
		if !approved {
			return nil
		}
	}

	environments, err := planEnvironments(ctx, conn, project)
	if err != nil {
		return err
	}
	if len(environments) > 0 {
		observed, err := planObservedDeployments(ctx, conn, planID)
		if err != nil {
			return err
		}
		for _, environment := range environments {
			if observed[finalKey][environment] != proof.MergeSHA {
				return nil
			}
		}
	}

	if _, err := conn.ExecContext(ctx, `UPDATE plans SET completed_at = ?
		WHERE id = ? AND completed_at IS NULL`, now, planID); err != nil {
		return fmt.Errorf("complete plan %d: %w", planID, err)
	}
	return nil
}
