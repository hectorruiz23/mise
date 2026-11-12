package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"mise/api/internal/users/app"
	"mise/api/internal/users/domain"
)

type Repo struct {
	db *sql.DB
}

func New(db *sql.DB) *Repo {
	return &Repo{db: db}
}

func (r *Repo) CreateSignupRequest(ctx context.Context, email, secretHash string, at int64) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO signup_requests (email, secret_hash, requested_at)
		VALUES (?, ?, ?)
		ON CONFLICT(email) WHERE consumed_at IS NULL
		DO UPDATE SET secret_hash = excluded.secret_hash
		RETURNING id`, email, secretHash, at).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create signup request: %w", err)
	}
	return id, nil
}

func (r *Repo) FindPendingSignupByEmail(ctx context.Context, email string) (app.SignupRequest, bool, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, email, secret_hash, requested_at, approved_at, consumed_at, user_id
		FROM signup_requests
		WHERE email = ? AND consumed_at IS NULL
		ORDER BY id DESC
		LIMIT 1`, email)

	request, err := scanSignupRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return app.SignupRequest{}, false, nil
	}
	if err != nil {
		return app.SignupRequest{}, false, fmt.Errorf("find pending signup request: %w", err)
	}
	return request, true, nil
}

func (r *Repo) GetSignupRequest(ctx context.Context, id int64) (app.SignupRequest, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, email, secret_hash, requested_at, approved_at, consumed_at, user_id
		FROM signup_requests WHERE id = ?`, id)

	request, err := scanSignupRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return app.SignupRequest{}, notFound("unknown signup request")
	}
	if err != nil {
		return app.SignupRequest{}, fmt.Errorf("get signup request %d: %w", id, err)
	}
	return request, nil
}

func scanSignupRequest(s scanner) (app.SignupRequest, error) {
	var (
		request    app.SignupRequest
		approvedAt sql.NullInt64
		consumedAt sql.NullInt64
		userID     sql.NullInt64
	)
	if err := s.Scan(
		&request.ID, &request.Email, &request.SecretHash, &request.RequestedAt,
		&approvedAt, &consumedAt, &userID,
	); err != nil {
		return app.SignupRequest{}, err
	}
	if approvedAt.Valid {
		value := approvedAt.Int64
		request.ApprovedAt = &value
	}
	if consumedAt.Valid {
		value := consumedAt.Int64
		request.ConsumedAt = &value
	}
	if userID.Valid {
		value := userID.Int64
		request.UserID = &value
	}
	return request, nil
}

func (r *Repo) ApproveSignupRequest(ctx context.Context, id int64, at int64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE signup_requests SET approved_at = ?
		WHERE id = ? AND approved_at IS NULL`, at, id)
	if err != nil {
		return fmt.Errorf("approve signup request %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("approve signup request %d: rows affected: %w", id, err)
	}
	if affected > 0 {
		return nil
	}

	var exists int
	err = r.db.QueryRowContext(ctx, `SELECT 1 FROM signup_requests WHERE id = ?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("unknown signup request")
	}
	if err != nil {
		return fmt.Errorf("approve signup request %d: check existence: %w", id, err)
	}
	return nil
}

func (r *Repo) ConsumeSignupRequest(ctx context.Context, id int64, userID int64, at int64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE signup_requests SET consumed_at = ?, user_id = ?
		WHERE id = ? AND consumed_at IS NULL AND approved_at IS NOT NULL`, at, userID, id)
	if err != nil {
		return fmt.Errorf("consume signup request %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("consume signup request %d: rows affected: %w", id, err)
	}
	if affected > 0 {
		return nil
	}

	var (
		approvedAt sql.NullInt64
		consumedAt sql.NullInt64
	)
	err = r.db.QueryRowContext(ctx,
		`SELECT approved_at, consumed_at FROM signup_requests WHERE id = ?`, id).
		Scan(&approvedAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("unknown signup request")
	}
	if err != nil {
		return fmt.Errorf("consume signup request %d: check state: %w", id, err)
	}
	if !approvedAt.Valid {
		return ruleError(domain.CodeSignupPending, "signup request is not approved")
	}
	return ruleError(domain.CodeSignupConsumed, "signup request was already consumed")
}

func (r *Repo) ClaimSignupRequest(ctx context.Context, id int64, u domain.User, tokenHash string, at int64) (domain.User, *domain.RuleError, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, nil, fmt.Errorf("claim signup request %d: begin: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	user, err := insertUser(ctx, tx, u, tokenHash)
	if err != nil {
		rule, stateErr := claimState(ctx, tx, id)
		if stateErr != nil {
			return domain.User{}, nil, stateErr
		}
		if rule != nil {
			return domain.User{}, rule, nil
		}
		return domain.User{}, nil, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE signup_requests
		SET consumed_at = ?, user_id = ?
		WHERE id = ? AND approved_at IS NOT NULL AND consumed_at IS NULL`,
		at, user.ID, id)
	if err != nil {
		return domain.User{}, nil, fmt.Errorf("claim signup request %d: consume: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.User{}, nil, fmt.Errorf("claim signup request %d: rows affected: %w", id, err)
	}
	if affected == 0 {
		rule, stateErr := claimState(ctx, tx, id)
		if stateErr != nil {
			return domain.User{}, nil, stateErr
		}
		if rule == nil {
			return domain.User{}, nil, fmt.Errorf("claim signup request %d: no row consumed", id)
		}
		return domain.User{}, rule, nil
	}

	if err := tx.Commit(); err != nil {
		return domain.User{}, nil, fmt.Errorf("claim signup request %d: commit: %w", id, err)
	}
	return user, nil, nil
}

func claimState(ctx context.Context, tx *sql.Tx, id int64) (*domain.RuleError, error) {
	var (
		approvedAt sql.NullInt64
		consumedAt sql.NullInt64
	)
	err := tx.QueryRowContext(ctx,
		`SELECT approved_at, consumed_at FROM signup_requests WHERE id = ?`, id).
		Scan(&approvedAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown signup request"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim signup request %d: check state: %w", id, err)
	}
	if !approvedAt.Valid {
		return &domain.RuleError{Code: domain.CodeSignupPending, Msg: "signup request is not approved"}, nil
	}
	if consumedAt.Valid {
		return &domain.RuleError{Code: domain.CodeSignupConsumed, Msg: "signup request was already consumed"}, nil
	}
	return nil, nil
}

func (r *Repo) CreateUser(ctx context.Context, u domain.User, tokenHash string) (domain.User, error) {
	return insertUser(ctx, r.db, u, tokenHash)
}

func insertUser(ctx context.Context, ex execer, u domain.User, tokenHash string) (domain.User, error) {
	result, err := ex.ExecContext(ctx, `
		INSERT INTO users (email, name, kind, token_hash, active, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		u.Email, u.Name, string(u.Kind), tokenHash, boolInt(u.Active), u.CreatedAt)
	if err != nil {
		return domain.User{}, fmt.Errorf("create user %q: %w", u.Email, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return domain.User{}, fmt.Errorf("create user %q: last insert id: %w", u.Email, err)
	}
	u.ID = id
	u.TokenHash = tokenHash
	return u, nil
}

func (r *Repo) GetUserByTokenHash(ctx context.Context, hash string) (domain.User, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, email, name, kind, token_hash, active, created_at
		FROM users WHERE token_hash = ?`, hash)

	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, notFound("unknown token")
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("get user by token hash: %w", err)
	}
	return user, nil
}

func (r *Repo) CreateSession(ctx context.Context, s domain.Session) error {
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (id, user_id, created_at, expires_at, revoked_at)
		VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.CreatedAt, s.ExpiresAt, nullableInt64(s.RevokedAt)); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (r *Repo) GetSession(ctx context.Context, idHash string) (domain.Session, domain.User, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.created_at, s.expires_at, s.revoked_at,
		       u.id, u.email, u.name, u.kind, u.token_hash, u.active, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = ?`, idHash)

	var (
		session   domain.Session
		revokedAt sql.NullInt64
		user      domain.User
		kind      string
		active    int
	)
	if err := row.Scan(
		&session.ID, &session.UserID, &session.CreatedAt, &session.ExpiresAt, &revokedAt,
		&user.ID, &user.Email, &user.Name, &kind, &user.TokenHash, &active, &user.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Session{}, domain.User{}, notFound("unknown session")
		}
		return domain.Session{}, domain.User{}, fmt.Errorf("get session: %w", err)
	}
	if revokedAt.Valid {
		value := revokedAt.Int64
		session.RevokedAt = &value
	}
	user.Kind = domain.Kind(kind)
	user.Active = active != 0
	return session, user, nil
}

func (r *Repo) RevokeSession(ctx context.Context, idHash string, at int64) error {
	if _, err := r.db.ExecContext(ctx, `
		UPDATE sessions SET revoked_at = ?
		WHERE id = ? AND revoked_at IS NULL`, at, idHash); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func scanUser(s scanner) (domain.User, error) {
	var (
		user   domain.User
		kind   string
		active int
	)
	if err := s.Scan(
		&user.ID, &user.Email, &user.Name, &kind, &user.TokenHash, &active, &user.CreatedAt,
	); err != nil {
		return domain.User{}, err
	}
	user.Kind = domain.Kind(kind)
	user.Active = active != 0
	return user, nil
}

type scanner interface {
	Scan(dest ...any) error
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func notFound(msg string) *domain.RuleError {
	return ruleError(domain.CodeNotFound, msg)
}

func ruleError(code, msg string) *domain.RuleError {
	return &domain.RuleError{Code: code, Msg: msg}
}
