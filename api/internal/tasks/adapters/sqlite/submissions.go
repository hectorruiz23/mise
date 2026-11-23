package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

var _ app.Repo = (*Repo)(nil)

func (r *Repo) ProjectChecks(ctx context.Context, projectKey string) ([]string, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT required_checks FROM projects WHERE key = ?`, projectKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	if err != nil {
		return nil, fmt.Errorf("project checks for %q: %w", projectKey, err)
	}
	return decodeChecks(raw)
}

func (r *Repo) SetProjectChecks(ctx context.Context, projectKey string, checks []string, now int64) error {
	encoded, err := encodeChecks(checks)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE projects SET required_checks = ? WHERE key = ?`,
		string(encoded), projectKey)
	if err != nil {
		return fmt.Errorf("set project checks for %q: %w", projectKey, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set project checks for %q: rows affected: %w", projectKey, err)
	}
	if affected == 0 {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: fmt.Sprintf("unknown project %q", projectKey)}
	}
	return nil
}

func (r *Repo) UpsertSubmission(ctx context.Context, s domain.Submission) (domain.Submission, error) {
	required, err := encodeChecks(s.Required)
	if err != nil {
		return domain.Submission{}, err
	}
	checks, err := encodeChecksOf(s.Checks)
	if err != nil {
		return domain.Submission{}, err
	}
	var checkedAt any
	if s.CheckedAt != nil {
		checkedAt = *s.CheckedAt
	}
	var stored domain.Submission
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO submissions (task_key, pr_url, head_sha, required_checks, verdict, checks,
		                         declared_at, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (task_key, head_sha) DO UPDATE SET
		    pr_url          = excluded.pr_url,
		    required_checks = excluded.required_checks,
		    verdict         = excluded.verdict,
		    checks          = excluded.checks,
		    checked_at      = excluded.checked_at
		RETURNING id, task_key, pr_url, head_sha, required_checks, verdict, checks,
		          declared_at, delivered_at, checked_at`,
		s.TaskKey, s.PRURL, s.HeadSHA, string(required), s.Verdict, string(checks),
		s.DeclaredAt, checkedAt).Scan(
		&stored.ID, &stored.TaskKey, &stored.PRURL, &stored.HeadSHA, &required, &stored.Verdict,
		&checks, &stored.DeclaredAt, &stored.DeliveredAt, &stored.CheckedAt)
	if err != nil {
		return domain.Submission{}, fmt.Errorf("upsert submission for %q: %w", s.TaskKey, err)
	}
	stored.Required, err = decodeChecks(string(required))
	if err != nil {
		return domain.Submission{}, err
	}
	stored.Checks, err = decodeChecksOf(string(checks))
	if err != nil {
		return domain.Submission{}, err
	}
	return stored, nil
}

func (r *Repo) RecordDelivery(ctx context.Context, d domain.Delivery) error {
	evidence, err := encodeEvidence(d)
	if err != nil {
		return fmt.Errorf("record delivery for %q: encode evidence: %w", d.TaskKey, err)
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO deliveries
		(task_key, lease_id, summary, evidence, pr_url, head_sha, created_at, actor_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.TaskKey, nullableInt64(d.LeaseID), d.Summary, string(evidence),
		nullableText(d.PRURL), nullableText(d.HeadSHA), d.CreatedAt, d.ActorID); err != nil {
		return fmt.Errorf("record delivery for %q: %w", d.TaskKey, err)
	}
	return nil
}

func (r *Repo) MarkSubmissionDelivered(ctx context.Context, id int64, now int64) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE submissions SET delivered_at = coalesce(delivered_at, ?) WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("mark submission %d delivered: %w", id, err)
	}
	return nil
}

func (r *Repo) CountFailedAttempts(ctx context.Context, taskKey string) (int, error) {
	var failures int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM submissions
		WHERE task_key = ? AND verdict = 'failed' AND delivered_at IS NOT NULL`, taskKey).Scan(&failures)
	if err != nil {
		return 0, fmt.Errorf("count failed submissions for %q: %w", taskKey, err)
	}
	return failures, nil
}

func (r *Repo) ListSubmissions(ctx context.Context, taskKey string, scope app.ReadScope) ([]domain.Submission, error) {
	query := `SELECT s.id, s.task_key, s.pr_url, s.head_sha, s.required_checks, s.verdict, s.checks,
	                 s.declared_at, s.delivered_at, s.checked_at
	          FROM submissions s
	          JOIN tasks t ON t.key = s.task_key`
	args := make([]any, 0, 1)
	if scope == app.PublicProjects {
		query += ` JOIN projects p ON p.key = t.project_key`
	}
	query += ` WHERE s.task_key = ?`
	args = append(args, taskKey)
	if scope == app.PublicProjects {
		query += ` AND p.is_public = 1`
	}
	query += ` ORDER BY s.id`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list submissions for %q: %w", taskKey, err)
	}
	defer rows.Close()

	submissions := make([]domain.Submission, 0)
	for rows.Next() {
		var (
			submission domain.Submission
			required   string
			checks     sql.NullString
		)
		if err := rows.Scan(&submission.ID, &submission.TaskKey, &submission.PRURL, &submission.HeadSHA,
			&required, &submission.Verdict, &checks, &submission.DeclaredAt,
			&submission.DeliveredAt, &submission.CheckedAt); err != nil {
			return nil, fmt.Errorf("list submissions for %q: scan: %w", taskKey, err)
		}
		submission.Required, err = decodeChecks(required)
		if err != nil {
			return nil, err
		}
		if checks.Valid {
			submission.Checks, err = decodeChecksOf(checks.String)
			if err != nil {
				return nil, err
			}
		}
		submissions = append(submissions, submission)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list submissions for %q: %w", taskKey, err)
	}
	return submissions, nil
}

func encodeChecks(checks []string) ([]byte, error) {
	if checks == nil {
		checks = []string{}
	}
	return json.Marshal(checks)
}

func decodeChecks(raw string) ([]string, error) {
	checks := make([]string, 0)
	if err := json.Unmarshal([]byte(raw), &checks); err != nil {
		return nil, fmt.Errorf("decode required checks: %w", err)
	}
	return checks, nil
}

type storedCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

func encodeChecksOf(checks []domain.Check) ([]byte, error) {
	stored := make([]storedCheck, 0, len(checks))
	for _, check := range checks {
		stored = append(stored, storedCheck{
			Name: check.Name, Status: check.Status, Conclusion: check.Conclusion, URL: check.URL,
		})
	}
	return json.Marshal(stored)
}

func decodeChecksOf(raw string) ([]domain.Check, error) {
	var stored []storedCheck
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, fmt.Errorf("decode submission checks: %w", err)
	}
	checks := make([]domain.Check, 0, len(stored))
	for _, check := range stored {
		checks = append(checks, domain.Check{
			Name: check.Name, Status: check.Status, Conclusion: check.Conclusion, URL: check.URL,
		})
	}
	return checks, nil
}
