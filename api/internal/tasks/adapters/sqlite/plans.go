package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

var _ app.PlanStore = (*Repo)(nil)

func (r *Repo) ApproveGraph(
	ctx context.Context,
	actor domain.Actor,
	tasks []string,
	edges []domain.PlanEdge,
	policy app.PlanPolicy,
	review app.PlanReview,
	now int64,
) (app.PlanGraph, error) {
	if actor.Kind != domain.Human {
		return app.PlanGraph{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human can approve a plan"}
	}
	if policy.Repository == "" || policy.Branch == "" || policy.WorkflowID <= 0 {
		return app.PlanGraph{}, invalidInput("a plan needs a repository, a branch and a workflow")
	}
	if err := domain.ValidatePlanEdges(tasks, edges); err != nil {
		return app.PlanGraph{}, err
	}
	if strings.TrimSpace(review.Scenario) == "" {
		return app.PlanGraph{}, invalidInput("a plan needs the acceptance scenario command")
	}
	if review.VisualReview {
		if strings.TrimSpace(review.RevealTask) == "" {
			return app.PlanGraph{}, invalidInput("a plan with visual review needs the task that reveals it")
		}
		if review.VisibleChange == nil || strings.TrimSpace(*review.VisibleChange) == "" {
			return app.PlanGraph{}, invalidInput("a plan with visual review needs the change that makes it visible")
		}
		member := false
		for _, key := range tasks {
			if key == review.RevealTask {
				member = true
				break
			}
		}
		if !member {
			return app.PlanGraph{}, invalidInput("the revealing task must belong to the plan")
		}
	} else if strings.TrimSpace(review.RevealTask) != "" {
		return app.PlanGraph{}, invalidInput("only a plan with visual review names a revealing task")
	}
	var planID int64
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		projects := make(map[string]struct{}, 1)
		for _, key := range tasks {
			var project, state string
			err := conn.QueryRowContext(ctx,
				`SELECT project_key, state FROM tasks WHERE key = ?`, key).Scan(&project, &state)
			if errors.Is(err, sql.ErrNoRows) {
				return &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown task %q", key)}
			}
			if err != nil {
				return err
			}
			if state != string(domain.Backlog) {
				return planConflict("every task of a plan must wait in backlog")
			}
			projects[project] = struct{}{}
			var taken bool
			if err := conn.QueryRowContext(ctx, `SELECT EXISTS(
				SELECT 1 FROM plan_nodes WHERE task_key = ?
				UNION ALL
				SELECT 1 FROM dependency_plans WHERE first_task = ? OR next_task = ?)`,
				key, key, key).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return planConflict("a task already belongs to another plan")
			}
		}
		if len(projects) != 1 {
			return planConflict("every task of a plan must share a project")
		}
		project := ""
		for key := range projects {
			project = key
		}
		if err := requireEnvironments(ctx, conn, project, edges); err != nil {
			return err
		}
		err := conn.QueryRowContext(ctx, `INSERT INTO plans
			(project_key, repository, branch, workflow_id, approved_by, approved_at, state,
			 scenario, visible_change, visual_review)
			VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?) RETURNING id`,
			project, policy.Repository, policy.Branch, policy.WorkflowID, actor.ID, now,
			strings.TrimSpace(review.Scenario), nullableText(review.VisibleChange),
			boolFlag(review.VisualReview)).Scan(&planID)
		if err != nil {
			return err
		}
		for _, key := range tasks {
			reveals := review.VisualReview && key == review.RevealTask
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO plan_nodes (plan_id, task_key, reveals) VALUES (?, ?, ?)`,
				planID, key, boolFlag(reveals)); err != nil {
				return err
			}
		}
		for _, edge := range edges {
			if _, err := conn.ExecContext(ctx,
				`INSERT INTO plan_edges (plan_id, from_task, to_task, kind) VALUES (?, ?, ?, ?)`,
				planID, edge.From, edge.To, edge.Kind); err != nil {
				return err
			}
		}
		proofs := make(map[string]struct{})
		for _, node := range tasks {
			if len(domain.Unsatisfied(edges, proofs, map[string]struct{}{}, node)) > 0 {
				continue
			}
			if err := readyPlanTask(ctx, conn, node, actor.ID, now, "Plan dependencies satisfied"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return app.PlanGraph{}, err
	}
	return r.Graph(ctx, planID)
}

func (r *Repo) Graph(ctx context.Context, id int64) (app.PlanGraph, error) {
	return r.readGraph(ctx, r.db, id)
}

type planQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (r *Repo) readGraph(ctx context.Context, q planQuerier, id int64) (app.PlanGraph, error) {
	var plan app.PlanGraph
	var cancelledBy, cancelledAt sql.NullInt64
	var visibleChange sql.NullString
	var visualReview int
	var completedAt sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT id, project_key, repository, branch, workflow_id,
		approved_by, approved_at, state, cancelled_by, cancelled_at,
		scenario, visible_change, visual_review, completed_at FROM plans WHERE id = ?`, id).
		Scan(&plan.ID, &plan.Project, &plan.Policy.Repository, &plan.Policy.Branch, &plan.Policy.WorkflowID,
			&plan.ApprovedBy, &plan.ApprovedAt, &plan.State, &cancelledBy, &cancelledAt,
			&plan.Scenario, &visibleChange, &visualReview, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return plan, &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan not found"}
	}
	if err != nil {
		return plan, err
	}
	plan.CancelledBy, plan.CancelledAt = cancelledBy.Int64, cancelledAt.Int64
	plan.VisualReview = visualReview == 1
	if visibleChange.Valid {
		value := visibleChange.String
		plan.VisibleChange = &value
	}
	if completedAt.Valid {
		value := completedAt.Int64
		plan.CompletedAt = &value
		plan.State = app.PlanCompleted
	}
	approval, err := r.planApproval(ctx, q, id)
	if err != nil {
		return plan, err
	}
	plan.Approval = approval

	edges, err := r.graphEdges(ctx, q, id)
	if err != nil {
		return plan, err
	}
	plan.Edges = edges
	proofs, err := r.graphProofs(ctx, q, id)
	if err != nil {
		return plan, err
	}
	released := make(map[string]struct{}, len(proofs))
	for key := range proofs {
		released[key] = struct{}{}
	}
	var project string
	if err := q.QueryRowContext(ctx, `SELECT project_key FROM plans WHERE id = ?`, id).Scan(&project); err != nil {
		return plan, err
	}
	observed, deployed, err := r.planDeploymentState(ctx, q, id, project, proofs)
	if err != nil {
		return plan, err
	}

	rows, err := q.QueryContext(ctx, `SELECT n.task_key, t.title, t.state, t.acceptance,
		n.reveals, n.correction, n.origin_evidence
		FROM plan_nodes n JOIN tasks t ON t.key = n.task_key
		WHERE n.plan_id = ? ORDER BY n.task_key`, id)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	nodes := make([]app.PlanNode, 0)
	for rows.Next() {
		var node app.PlanNode
		var acceptance, origin sql.NullString
		var reveals, correction int
		if err := rows.Scan(&node.Key, &node.Title, &node.State, &acceptance,
			&reveals, &correction, &origin); err != nil {
			return plan, err
		}
		if acceptance.Valid {
			value := acceptance.String
			node.Acceptance = &value
		}
		node.Reveals = reveals == 1
		node.Correction = correction == 1
		if origin.Valid {
			value := origin.String
			node.OriginEvidence = &value
		}
		if proof, ok := proofs[node.Key]; ok {
			node.Proof = &proof
		}
		node.Deployments = make([]app.PlanDeployment, 0, len(observed[node.Key]))
		for environment, revision := range observed[node.Key] {
			node.Deployments = append(node.Deployments, app.PlanDeployment{
				Environment: environment,
				Revision:    revision,
			})
		}
		slices.SortFunc(node.Deployments, func(a, b app.PlanDeployment) int {
			return strings.Compare(a.Environment, b.Environment)
		})
		if node.State == string(domain.Backlog) {
			node.WaitingOn = domain.Unsatisfied(edges, released, deployed, node.Key)
		} else {
			node.WaitingOn = []string{}
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return plan, err
	}
	plan.Nodes = nodes
	return plan, nil
}

func (r *Repo) graphEdges(ctx context.Context, q planQuerier, id int64) ([]domain.PlanEdge, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT from_task, to_task, kind FROM plan_edges WHERE plan_id = ? ORDER BY from_task, to_task`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edges := make([]domain.PlanEdge, 0)
	for rows.Next() {
		var edge domain.PlanEdge
		if err := rows.Scan(&edge.From, &edge.To, &edge.Kind); err != nil {
			return nil, err
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

func (r *Repo) graphProofs(ctx context.Context, q planQuerier, id int64) (map[string]app.PlanProof, error) {
	rows, err := q.QueryContext(ctx, `SELECT task_key, delivery_id, pr_url, head_sha, merge_sha,
		run_id, attempt, verified_at, origin FROM plan_proofs WHERE plan_id = ? ORDER BY task_key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	proofs := make(map[string]app.PlanProof)
	for rows.Next() {
		var proof app.PlanProof
		if err := rows.Scan(&proof.TaskKey, &proof.DeliveryID, &proof.PRURL, &proof.HeadSHA,
			&proof.MergeSHA, &proof.RunID, &proof.Attempt, &proof.VerifiedAt, &proof.Origin); err != nil {
			return nil, err
		}
		proofs[proof.TaskKey] = proof
	}
	return proofs, rows.Err()
}

func (r *Repo) ListGraphs(ctx context.Context) ([]app.PlanGraph, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM plans ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	graphs := make([]app.PlanGraph, 0, len(ids))
	for _, id := range ids {
		graph, err := r.Graph(ctx, id)
		if err != nil {
			return nil, err
		}
		graphs = append(graphs, graph)
	}
	return graphs, nil
}

func (r *Repo) CancelGraph(ctx context.Context, actor domain.Actor, id int64, now int64) (app.PlanGraph, error) {
	if actor.Kind != domain.Human {
		return app.PlanGraph{}, &domain.RuleError{Code: domain.CodeForbidden, Msg: "only a human can cancel a plan"}
	}
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		result, err := conn.ExecContext(ctx, `UPDATE plans
			SET state = 'cancelled', cancelled_by = ?, cancelled_at = ?
			WHERE id = ? AND state = 'active'`, actor.ID, now, id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected > 0 {
			return nil
		}
		var state string
		err = conn.QueryRowContext(ctx, `SELECT state FROM plans WHERE id = ?`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan not found"}
		}
		return err
	})
	if err != nil {
		return app.PlanGraph{}, err
	}
	return r.Graph(ctx, id)
}

func (r *Repo) PendingGraphTasks(ctx context.Context) ([]app.PendingPlanTask, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, n.task_key, p.repository, p.branch, p.workflow_id, d.id, d.pr_url, d.evidence
		FROM plan_nodes n
		JOIN plans p ON p.id = n.plan_id AND p.state = 'active'
		JOIN tasks t ON t.key = n.task_key
		LEFT JOIN deliveries d ON t.state IN ('review','done') AND d.id = (
		    SELECT MAX(id) FROM deliveries
		    WHERE task_key = n.task_key AND pr_url IS NOT NULL AND created_at >= p.approved_at
		)
		WHERE NOT EXISTS (SELECT 1 FROM plan_proofs pp
		    WHERE pp.plan_id = n.plan_id AND pp.task_key = n.task_key)
		ORDER BY p.id, n.task_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pending := make([]app.PendingPlanTask, 0)
	for rows.Next() {
		var item app.PendingPlanTask
		var deliveryID sql.NullInt64
		var prURL, evidence sql.NullString
		if err := rows.Scan(&item.PlanID, &item.TaskKey, &item.Policy.Repository, &item.Policy.Branch,
			&item.Policy.WorkflowID, &deliveryID, &prURL, &evidence); err != nil {
			return nil, err
		}
		if !deliveryID.Valid || !prURL.Valid {
			continue
		}
		head, ok := evidenceHeadSHA(evidence.String)
		if !ok {
			continue
		}
		item.DeliveryID, item.PRURL, item.HeadSHA = deliveryID.Int64, prURL.String, head
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func evidenceHeadSHA(raw string) (string, bool) {
	_, items, err := decodeEvidence(raw)
	if err != nil {
		return "", false
	}
	head := ""
	for _, item := range items {
		if item.Label == nil || *item.Label != "head_sha" {
			continue
		}
		if head != "" || !commitSHA(item.Raw) {
			return "", false
		}
		head = strings.ToLower(item.Raw)
	}
	return head, head != ""
}

func (r *Repo) ReleaseGraphTask(
	ctx context.Context,
	planID int64,
	taskKey string,
	proof app.PlanProof,
	now int64,
) (app.PlanGraph, error) {
	err := r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		var state string
		err := conn.QueryRowContext(ctx, `SELECT state FROM plans WHERE id = ?`, planID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan not found"}
		}
		if err != nil {
			return err
		}
		if state != domain.PlanActive {
			return planConflict("the plan was cancelled")
		}
		var member bool
		if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plan_nodes
			WHERE plan_id = ? AND task_key = ?)`, planID, taskKey).Scan(&member); err != nil {
			return err
		}
		if !member {
			return planConflict("the task is not part of the plan")
		}
		var existing string
		err = conn.QueryRowContext(ctx, `SELECT merge_sha FROM plan_proofs
			WHERE plan_id = ? AND task_key = ?`, planID, taskKey).Scan(&existing)
		switch {
		case err == nil && existing == proof.MergeSHA:
			return nil
		case err == nil:
			return planConflict("the task was already verified with a different merge")
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if proof.DeliveryID <= 0 || proof.RunID <= 0 || proof.Attempt <= 0 ||
			!commitSHA(proof.MergeSHA) || !commitSHA(proof.HeadSHA) {
			return planConflict("verified evidence is incomplete")
		}
		var used string
		err = conn.QueryRowContext(ctx, `SELECT task_key FROM plan_proofs WHERE merge_sha = ?`,
			proof.MergeSHA).Scan(&used)
		switch {
		case err == nil:
			return planConflict("the verified merge was already used by another plan")
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO plan_proofs
			(plan_id, task_key, delivery_id, pr_url, head_sha, merge_sha, run_id, attempt, verified_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			planID, taskKey, proof.DeliveryID, proof.PRURL, proof.HeadSHA, proof.MergeSHA,
			proof.RunID, proof.Attempt, now); err != nil {
			return err
		}
		return r.settlePlan(ctx, conn, planID, now)
	})
	if err != nil {
		return app.PlanGraph{}, err
	}
	return r.Graph(ctx, planID)
}

func (r *Repo) planApproval(ctx context.Context, q planQuerier, planID int64) (*app.PlanApproval, error) {
	var approval app.PlanApproval
	err := q.QueryRowContext(ctx, `SELECT task_key, head_sha, reviewer, approved_at
		FROM plan_approvals WHERE plan_id = ? ORDER BY approved_at DESC LIMIT 1`, planID).
		Scan(&approval.TaskKey, &approval.HeadSHA, &approval.Reviewer, &approval.ApprovedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &approval, nil
}

func boolFlag(value bool) int {
	if value {
		return 1
	}
	return 0
}
