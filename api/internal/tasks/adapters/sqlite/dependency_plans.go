package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

var _ app.DependencyPlans = (*Repo)(nil)

const planQuery = `
	SELECT p.first_task, p.next_task, p.approved_by, p.approved_at,
	       p.repository, p.branch, p.workflow_id, p.proof, d.id, d.pr_url, d.evidence
	FROM dependency_plans p
	JOIN tasks t ON t.key = p.first_task
	LEFT JOIN deliveries d ON t.state IN ('review','done') AND d.id = (
		SELECT MAX(id) FROM deliveries
		WHERE task_key = p.first_task AND id > p.previous_delivery_id
	)`

func readPlan(row scanner) (app.DependencyPlan, error) {
	var plan app.DependencyPlan
	var proof, prURL, evidence sql.NullString
	var deliveryID sql.NullInt64
	err := row.Scan(&plan.FirstTask, &plan.NextTask, &plan.ApprovedBy, &plan.ApprovedAt,
		&plan.Policy.Repository, &plan.Policy.Branch, &plan.Policy.WorkflowID, &proof, &deliveryID, &prURL, &evidence)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return plan, &domain.RuleError{Code: domain.CodeNotFound, Msg: "dependency plan not found"}
		}
		return plan, err
	}
	if proof.Valid {
		err = json.Unmarshal([]byte(proof.String), &plan.Proof)
		return plan, err
	}
	if prURL.Valid {
		_, items, err := decodeEvidence(evidence.String)
		if err != nil {
			return plan, err
		}
		for _, item := range items {
			if item.Label != nil && *item.Label == "head_sha" {
				if plan.HeadSHA != "" || !commitSHA(item.Raw) {
					plan.HeadSHA = ""
					return plan, nil
				}
				plan.HeadSHA = strings.ToLower(item.Raw)
			}
		}
		if plan.HeadSHA != "" {
			plan.DeliveryID, plan.PRURL = deliveryID.Int64, prURL.String
		}
	}
	return plan, nil
}

func commitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func (r *Repo) Plan(ctx context.Context, first string) (app.DependencyPlan, error) {
	return readPlan(r.db.QueryRowContext(ctx, planQuery+" WHERE p.first_task = ?", first))
}

func (r *Repo) PendingPlans(ctx context.Context) ([]app.DependencyPlan, error) {
	rows, err := r.db.QueryContext(ctx, planQuery+" WHERE p.proof IS NULL AND d.pr_url IS NOT NULL ORDER BY p.first_task")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var plans []app.DependencyPlan
	for rows.Next() {
		plan, err := readPlan(rows)
		if err != nil {
			return nil, err
		}
		if plan.DeliveryID != 0 {
			plans = append(plans, plan)
		}
	}
	return plans, rows.Err()
}

func planConflict(message string) error {
	return &domain.RuleError{Code: domain.CodeInvalidTransition, Msg: message}
}

func (r *Repo) ApprovePlan(ctx context.Context, actor domain.Actor, first, next string, policy app.PlanPolicy, now int64) (app.DependencyPlan, error) {
	var plan app.DependencyPlan
	if actor.Kind != domain.Human {
		return plan, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human can approve a plan"}
	}
	if first == next || first == "" || next == "" || policy.Branch == "" || policy.Repository == "" || policy.WorkflowID <= 0 {
		return plan, invalidInput("two distinct tasks and a workflow policy are required")
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var existingFirst string
		err := conn.QueryRowContext(ctx, `SELECT first_task FROM dependency_plans
			WHERE first_task IN (?, ?) OR next_task IN (?, ?) LIMIT 1`, first, next, first, next).Scan(&existingFirst)
		switch {
		case err == nil:
			stored, err := readPlan(conn.QueryRowContext(ctx, planQuery+" WHERE p.first_task = ?", existingFirst))
			if err != nil {
				return err
			}
			if stored.FirstTask != first || stored.NextTask != next || stored.Policy != policy || stored.ApprovedBy != actor.ID {
				return planConflict("a task already belongs to another approved plan")
			}
			plan = stored
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var sameProject, backlog bool
		err = conn.QueryRowContext(ctx, `SELECT a.project_key=b.project_key, a.state='backlog' AND b.state='backlog'
			FROM tasks a JOIN tasks b ON b.key=? WHERE a.key=?`, next, first).Scan(&sameProject, &backlog)
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan task not found"}
		}
		if err != nil {
			return err
		}
		if !sameProject || !backlog {
			return planConflict("both tasks must be in the same project backlog")
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO dependency_plans
			(first_task,next_task,approved_by,approved_at,repository,branch,workflow_id,previous_delivery_id)
			VALUES (?,?,?,?,?,?,?,(SELECT coalesce(max(id),0) FROM deliveries WHERE task_key=?))`,
			first, next, actor.ID, now, policy.Repository, policy.Branch, policy.WorkflowID, first); err != nil {
			return err
		}
		plan = app.DependencyPlan{FirstTask: first, NextTask: next, Policy: policy, ApprovedAt: now, ApprovedBy: actor.ID}
		return readyPlanTask(ctx, conn, first, actor.ID, now, "Dependency plan approved")
	})
	return plan, err
}

func readyPlanTask(ctx context.Context, conn *sql.Conn, key string, actorID, now int64, reason string) error {
	var version int64
	err := conn.QueryRowContext(ctx, `UPDATE tasks SET state='ready', version=version+1, updated_at=?
		WHERE key=? AND state='backlog' RETURNING version`, now, key).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return planConflict("plan task is no longer waiting in backlog")
	}
	if err != nil {
		return err
	}
	from := domain.Backlog
	return insertMovement(ctx, conn, domain.Movement{TaskKey: key, FromState: &from, ToState: domain.Ready,
		ActorID: actorID, Reason: &reason, TaskVersion: version, At: now})
}

func (r *Repo) ReleasePlan(ctx context.Context, first string, proof app.MergeProof, now int64) (app.DependencyPlan, error) {
	var plan app.DependencyPlan
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var err error
		plan, err = readPlan(conn.QueryRowContext(ctx, planQuery+" WHERE p.first_task = ?", first))
		if err != nil {
			return err
		}
		if plan.Proof != nil {
			if *plan.Proof == proof {
				return nil
			}
			return planConflict("dependency was already released with different evidence")
		}
		if proof.DeliveryID <= 0 || proof.DeliveryID != plan.DeliveryID || proof.PRURL != plan.PRURL ||
			proof.HeadSHA != plan.HeadSHA || !commitSHA(proof.MergeSHA) || proof.Attempt <= 0 || proof.RunID <= 0 {
			return planConflict("verified evidence must match the latest eligible delivery")
		}
		var reused bool
		err = conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dependency_plans WHERE repository=?
			AND json_extract(proof, '$.merge_sha')=?)`, plan.Policy.Repository, proof.MergeSHA).Scan(&reused)
		if err != nil {
			return err
		}
		if reused {
			return planConflict("the verified merge was already used by another plan")
		}
		encoded, err := json.Marshal(proof)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "UPDATE dependency_plans SET proof=? WHERE first_task=?", string(encoded), first); err != nil {
			return err
		}
		if err := readyPlanTask(ctx, conn, plan.NextTask, plan.ApprovedBy, now, "Verified dependency merge: "+proof.MergeSHA); err != nil {
			return err
		}
		plan.Proof = &proof
		plan.DeliveryID, plan.HeadSHA, plan.PRURL = 0, "", ""
		return nil
	})
	return plan, err
}
