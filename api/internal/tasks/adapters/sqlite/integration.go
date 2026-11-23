package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"mise/api/internal/githubinfo"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

var _ app.IntegrationStore = (*Repo)(nil)

func (r *Repo) PendingMerges(ctx context.Context) ([]app.MergeCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.key, t.project_key, d.id, d.pr_url, d.head_sha, d.actor_id
		FROM tasks t
		JOIN projects p ON p.key = t.project_key
		JOIN deliveries d ON d.id = (SELECT MAX(id) FROM deliveries WHERE task_key = t.key)
		JOIN users u ON u.id = d.actor_id
		WHERE t.state = 'review'
		  AND p.auto_merge = 1
		  AND json_array_length(p.required_checks) > 0
		  AND d.pr_url IS NOT NULL
		  AND d.head_sha IS NOT NULL
		  AND d.lease_id IS NOT NULL
		  AND u.kind = 'agent'
		  AND EXISTS (SELECT 1 FROM submissions s
		              WHERE s.task_key = t.key AND s.head_sha = lower(d.head_sha)
		                AND s.pr_url = d.pr_url AND s.verdict = 'passed' AND s.delivered_at IS NOT NULL)
		  AND NOT EXISTS (SELECT 1 FROM merge_attempts m
		                  WHERE m.task_key = t.key AND m.pr_url = d.pr_url
		                    AND (m.delivery_id IS NULL OR m.delivery_id = d.id))
		ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("pending merges: %w", err)
	}
	defer rows.Close()

	candidates := make([]app.MergeCandidate, 0)
	for rows.Next() {
		var candidate app.MergeCandidate
		var headSHA sql.NullString
		if err := rows.Scan(&candidate.TaskKey, &candidate.ProjectKey, &candidate.DeliveryID,
			&candidate.PRURL, &headSHA, &candidate.ActorID); err != nil {
			return nil, fmt.Errorf("pending merges: scan: %w", err)
		}
		if headSHA.Valid {
			value := headSHA.String
			candidate.HeadSHA = &value
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return candidates, nil
	}

	visual, err := r.visualPullRequests(ctx)
	if err != nil {
		return nil, err
	}
	eligible := candidates[:0]
	for _, candidate := range candidates {
		ref, _ := githubinfo.ParsePullRequest(candidate.PRURL)
		if _, protected := visual[canonicalPullRequest(ref)]; !protected {
			eligible = append(eligible, candidate)
		}
	}
	return eligible, nil
}

func (r *Repo) visualPullRequests(ctx context.Context) (map[githubinfo.Ref]struct{}, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT d.pr_url FROM plan_nodes n
		JOIN deliveries d ON d.task_key = n.task_key
		WHERE n.reveals = 1 AND d.pr_url IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("read visual pull requests: %w", err)
	}
	defer rows.Close()

	visual := make(map[githubinfo.Ref]struct{})
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return nil, err
		}
		if ref, ok := githubinfo.ParsePullRequest(url); ok {
			visual[canonicalPullRequest(ref)] = struct{}{}
		}
	}
	return visual, rows.Err()
}

func canonicalPullRequest(ref githubinfo.Ref) githubinfo.Ref {
	ref.Owner = strings.ToLower(ref.Owner)
	ref.Repo = strings.ToLower(ref.Repo)
	return ref
}

func (r *Repo) RecordMergeAttempt(ctx context.Context, outcome app.MergeOutcome, now int64) error {
	return r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO merge_attempts (task_key, pr_url, outcome, reason, merge_sha, attempted_at, delivery_id)
			VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, 0))
			ON CONFLICT (task_key, pr_url) DO UPDATE SET outcome = excluded.outcome,
				reason = excluded.reason, merge_sha = excluded.merge_sha, attempted_at = excluded.attempted_at,
				delivery_id = excluded.delivery_id
			WHERE merge_attempts.outcome <> 'merged'`,
			outcome.TaskKey, outcome.PRURL, outcome.Outcome, outcome.Reason, nullableText(outcome.MergeSHA), now, outcome.DeliveryID); err != nil {
			return fmt.Errorf("record merge attempt for %q: %w", outcome.TaskKey, err)
		}
		if outcome.Outcome == app.MergeOutcomeMerged {
			return nil
		}
		body := "Mise did not merge this pull request: " + outcome.Reason
		if runes := []rune(body); len(runes) > 10000 {
			body = string(runes[:9990]) + "\u2026"
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO comments (task_key, actor_id, body, created_at) VALUES (?, ?, ?, ?)`,
			outcome.TaskKey, outcome.ActorID, body, now); err != nil {
			return fmt.Errorf("record merge refusal for %q: %w", outcome.TaskKey, err)
		}
		return nil
	})
}

func (r *Repo) RecordMerge(ctx context.Context, evidence app.MergeEvidence, now int64) error {
	return r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		reason := "Merged as " + evidence.MergeSHA
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO merge_attempts (task_key, pr_url, outcome, reason, merge_sha, attempted_at)
			VALUES (?, ?, 'merged', ?, ?, ?)
			ON CONFLICT (task_key, pr_url) DO UPDATE SET outcome = excluded.outcome,
				reason = excluded.reason, merge_sha = excluded.merge_sha, attempted_at = excluded.attempted_at,
				delivery_id = NULL WHERE merge_attempts.outcome <> 'merged'`,
			evidence.TaskKey, evidence.PRURL, reason, evidence.MergeSHA, now); err != nil {
			return fmt.Errorf("record merge for %q: %w", evidence.TaskKey, err)
		}

		planID, err := activePlanOf(ctx, conn, evidence.TaskKey)
		if err != nil {
			return err
		}
		if planID == 0 {
			return closeMergedTask(ctx, conn, evidence, "Merged as "+evidence.MergeSHA+" in "+evidence.PRURL, now)
		}

		var existing string
		err = conn.QueryRowContext(ctx, `SELECT merge_sha FROM plan_proofs
			WHERE plan_id = ? AND task_key = ?`, planID, evidence.TaskKey).Scan(&existing)
		switch {
		case err == nil && existing == evidence.MergeSHA:
			return r.settlePlan(ctx, conn, planID, now)
		case err == nil:
			return planConflict("the task was already verified with a different merge")
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		headSHA := ""
		if evidence.HeadSHA != nil {
			headSHA = strings.ToLower(*evidence.HeadSHA)
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO plan_proofs (plan_id, task_key, delivery_id, pr_url, head_sha, merge_sha,
			                         run_id, attempt, verified_at, origin)
			VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?, 'merge')`,
			planID, evidence.TaskKey, evidence.DeliveryID, evidence.PRURL, headSHA,
			evidence.MergeSHA, now); err != nil {
			return fmt.Errorf("record merge proof for %q: %w", evidence.TaskKey, err)
		}
		return r.settlePlan(ctx, conn, planID, now)
	})
}

func (r *Repo) PendingDeployments(ctx context.Context) ([]app.DeploymentCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.id, n.task_key, pp.merge_sha, env.name, env.url
		FROM plan_nodes n
		JOIN plans p ON p.id = n.plan_id AND p.state = 'active'
		JOIN plan_proofs pp ON pp.plan_id = n.plan_id AND pp.task_key = n.task_key
		JOIN environments env ON env.project_key = p.project_key
		WHERE NOT EXISTS (SELECT 1 FROM plan_deployments d
		                  WHERE d.plan_id = p.id AND d.task_key = n.task_key
		                    AND d.environment = env.name AND d.revision = pp.merge_sha)
		GROUP BY p.id, n.task_key, pp.merge_sha, env.name, env.url
		ORDER BY p.id, n.task_key, env.name`)
	if err != nil {
		return nil, fmt.Errorf("pending deployments: %w", err)
	}
	defer rows.Close()

	pending := make([]app.DeploymentCandidate, 0)
	for rows.Next() {
		var item app.DeploymentCandidate
		if err := rows.Scan(&item.PlanID, &item.TaskKey, &item.Revision,
			&item.Environment, &item.URL); err != nil {
			return nil, fmt.Errorf("pending deployments: scan: %w", err)
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (r *Repo) RecordDeployment(
	ctx context.Context,
	planID int64,
	taskKey string,
	environment string,
	revision string,
	now int64,
) error {
	return r.withTx(ctx, func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO plan_deployments (plan_id, task_key, environment, revision, observed_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (plan_id, task_key, environment) DO UPDATE
				SET revision = excluded.revision, observed_at = excluded.observed_at`,
			planID, taskKey, environment, strings.ToLower(revision), now); err != nil {
			return fmt.Errorf("record deployment for %q: %w", taskKey, err)
		}
		return r.settlePlan(ctx, conn, planID, now)
	})
}

func activePlanOf(ctx context.Context, conn *sql.Conn, taskKey string) (int64, error) {
	var planID int64
	err := conn.QueryRowContext(ctx, `
		SELECT n.plan_id FROM plan_nodes n
		JOIN plans p ON p.id = n.plan_id AND p.state = 'active'
		WHERE n.task_key = ?`, taskKey).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return planID, nil
}

func closeMergedTask(ctx context.Context, conn *sql.Conn, evidence app.MergeEvidence, reason string, now int64) error {
	var version int64
	err := conn.QueryRowContext(ctx, `UPDATE tasks
		SET state = 'done', version = version + 1, updated_at = ?, closed_at = ?
		WHERE key = ? AND state = 'review' RETURNING version`, now, now, evidence.TaskKey).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("close merged task %q: %w", evidence.TaskKey, err)
	}
	from := domain.Review
	if err := insertMovement(ctx, conn, domain.Movement{
		TaskKey: evidence.TaskKey, FromState: &from, ToState: domain.Done,
		ActorID: evidence.ActorID, Reason: &reason, TaskVersion: version, At: now,
	}); err != nil {
		return err
	}
	return closeLiveLease(ctx, conn, evidence.TaskKey, now, "done")
}

func (r *Repo) settlePlan(ctx context.Context, conn *sql.Conn, planID int64, now int64) error {
	var projectKey string
	var approvedBy int64
	err := conn.QueryRowContext(ctx, `SELECT project_key, approved_by FROM plans WHERE id = ?`,
		planID).Scan(&projectKey, &approvedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: "plan not found"}
	}
	if err != nil {
		return err
	}

	edges, err := r.graphEdges(ctx, conn, planID)
	if err != nil {
		return err
	}
	proofs, err := r.graphProofs(ctx, conn, planID)
	if err != nil {
		return err
	}
	_, deployed, err := r.planDeploymentState(ctx, conn, planID, projectKey, proofs)
	if err != nil {
		return err
	}

	released := make(map[string]struct{}, len(proofs))
	for key := range proofs {
		released[key] = struct{}{}
	}

	needsDeployment := make(map[string]bool, len(edges))
	for _, edge := range edges {
		if edge.Kind == domain.EdgeDeployment {
			needsDeployment[edge.From] = true
		}
	}

	if _, err := closeMergedPlanTasks(ctx, conn, planID, needsDeployment, deployed, now); err != nil {
		return err
	}

	rows, err := conn.QueryContext(ctx, `SELECT n.task_key FROM plan_nodes n
		JOIN tasks t ON t.key = n.task_key
		WHERE n.plan_id = ? AND t.state = 'backlog' ORDER BY n.task_key`, planID)
	if err != nil {
		return err
	}
	waiting := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		waiting = append(waiting, key)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range waiting {
		if len(domain.Unsatisfied(edges, released, deployed, key)) > 0 {
			continue
		}
		if err := readyPlanTask(ctx, conn, key, approvedBy, now, "Plan dependencies satisfied"); err != nil {
			return err
		}
	}
	return r.completePlanIfReady(ctx, conn, planID, now)
}

func closeMergedPlanTasks(
	ctx context.Context,
	conn *sql.Conn,
	planID int64,
	needsDeployment map[string]bool,
	deployed map[string]struct{},
	now int64,
) (bool, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT n.task_key, pp.pr_url, pp.merge_sha, d.actor_id
		FROM plan_nodes n
		JOIN plan_proofs pp ON pp.plan_id = n.plan_id AND pp.task_key = n.task_key
		JOIN deliveries d ON d.id = pp.delivery_id
		JOIN users u ON u.id = d.actor_id AND u.kind = 'agent'
		JOIN tasks t ON t.key = n.task_key AND t.state = 'review'
		WHERE n.plan_id = ? AND d.lease_id IS NOT NULL
		ORDER BY n.task_key`, planID)
	if err != nil {
		return false, err
	}
	type merged struct {
		key     string
		prURL   string
		sha     string
		actorID int64
	}
	pending := make([]merged, 0)
	for rows.Next() {
		var item merged
		if err := rows.Scan(&item.key, &item.prURL, &item.sha, &item.actorID); err != nil {
			rows.Close()
			return false, err
		}
		pending = append(pending, item)
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	closed := false
	for _, item := range pending {
		if needsDeployment[item.key] {
			if _, ok := deployed[item.key]; !ok {
				continue
			}
		}
		reason := "Merged as " + item.sha + " in " + item.prURL
		if needsDeployment[item.key] {
			reason += "; serving it in every environment"
		}
		var version int64
		err := conn.QueryRowContext(ctx, `UPDATE tasks
			SET state = 'done', version = version + 1, updated_at = ?, closed_at = ?
			WHERE key = ? AND state = 'review' RETURNING version`, now, now, item.key).Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("close merged task %q: %w", item.key, err)
		}
		from := domain.Review
		if err := insertMovement(ctx, conn, domain.Movement{
			TaskKey: item.key, FromState: &from, ToState: domain.Done,
			ActorID: item.actorID, Reason: &reason, TaskVersion: version, At: now,
		}); err != nil {
			return false, err
		}
		if err := closeLiveLease(ctx, conn, item.key, now, "done"); err != nil {
			return false, err
		}
		closed = true
	}
	return closed, nil
}

func planEnvironments(ctx context.Context, q planQuerier, projectKey string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name FROM environments WHERE project_key = ? ORDER BY name`, projectKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func planObservedDeployments(ctx context.Context, q planQuerier, planID int64) (map[string]map[string]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT task_key, environment, revision FROM plan_deployments WHERE plan_id = ?`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	observed := make(map[string]map[string]string)
	for rows.Next() {
		var task, environment, revision string
		if err := rows.Scan(&task, &environment, &revision); err != nil {
			return nil, err
		}
		if observed[task] == nil {
			observed[task] = make(map[string]string)
		}
		observed[task][environment] = revision
	}
	return observed, rows.Err()
}

func (r *Repo) planDeploymentState(
	ctx context.Context,
	q planQuerier,
	planID int64,
	projectKey string,
	proofs map[string]app.PlanProof,
) (map[string]map[string]string, map[string]struct{}, error) {
	environments, err := planEnvironments(ctx, q, projectKey)
	if err != nil {
		return nil, nil, err
	}
	observed, err := planObservedDeployments(ctx, q, planID)
	if err != nil {
		return nil, nil, err
	}
	deployed := make(map[string]struct{}, len(proofs))
	for key, proof := range proofs {
		if len(environments) == 0 {
			continue
		}
		serving := true
		for _, environment := range environments {
			if observed[key][environment] != proof.MergeSHA {
				serving = false
				break
			}
		}
		if serving {
			deployed[key] = struct{}{}
		}
	}
	return observed, deployed, nil
}

func requireEnvironments(ctx context.Context, conn *sql.Conn, projectKey string, edges []domain.PlanEdge) error {
	deployment := false
	for _, edge := range edges {
		if edge.Kind == domain.EdgeDeployment {
			deployment = true
			break
		}
	}
	if !deployment {
		return nil
	}
	var count int
	if err := conn.QueryRowContext(ctx,
		`SELECT count(*) FROM environments WHERE project_key = ?`, projectKey).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return invalidInput("a deployment edge needs at least one environment in the project")
	}
	return nil
}
