package sqlite

import (
	"context"
	"database/sql"
	"strings"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func (r *Repo) activatePlan(
	ctx context.Context,
	conn *sql.Conn,
	approverID int64,
	project string,
	tasks []string,
	edges []domain.PlanEdge,
	policy app.PlanPolicy,
	review app.PlanReview,
	now int64,
) (int64, error) {
	var planID int64
	if err := requireEnvironments(ctx, conn, project, edges); err != nil {
		return 0, err
	}
	target := strings.TrimSpace(review.TargetEnvironment)
	if target != "" {
		var exists bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM environments
			WHERE project_key = ? AND name = ?)`, project, target).Scan(&exists); err != nil {
			return 0, err
		}
		if !exists {
			return 0, invalidInput("the target environment must be registered in the project")
		}
	}
	err := conn.QueryRowContext(ctx, `INSERT INTO plans
	(project_key, repository, branch, workflow_id, approved_by, approved_at, state,
	 scenario, visible_change, visual_review, target_environment)
	VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?) RETURNING id`,
		project, policy.Repository, policy.Branch, policy.WorkflowID, approverID, now,
		strings.TrimSpace(review.Scenario), nullableText(review.VisibleChange),
		boolFlag(review.VisualReview), target).Scan(&planID)
	if err != nil {
		return 0, err
	}
	for _, key := range tasks {
		reveals := review.VisualReview && key == review.RevealTask
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO plan_nodes (plan_id, task_key, reveals) VALUES (?, ?, ?)`,
			planID, key, boolFlag(reveals)); err != nil {
			return 0, err
		}
	}
	for _, edge := range edges {
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO plan_edges (plan_id, from_task, to_task, kind) VALUES (?, ?, ?, ?)`,
			planID, edge.From, edge.To, edge.Kind); err != nil {
			return 0, err
		}
	}
	proofs := make(map[string]struct{})
	for _, node := range tasks {
		if len(domain.Unsatisfied(edges, proofs, map[string]struct{}{}, node)) > 0 {
			continue
		}
		if err := readyPlanTask(ctx, conn, node, approverID, now, "Plan dependencies satisfied"); err != nil {
			return 0, err
		}
	}
	return planID, nil
}
