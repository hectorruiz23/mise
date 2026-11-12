package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	"mise/api/internal/users/adapters/sqlite"
	"mise/api/internal/users/app"
	"mise/api/internal/users/domain"
)

const testNow = 1_700_000_000

type manualClock struct {
	now int64
}

func (c *manualClock) Now() int64 { return c.now }

func openMigrated(t *testing.T) *sql.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mise.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	return database
}

func ruleCode(t *testing.T, err error) string {
	t.Helper()
	var rule *domain.RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want *domain.RuleError", err)
	}
	return rule.Code
}

func newService(database *sql.DB, clock app.Clock) *app.Service {
	return app.New(sqlite.New(database), clock)
}

func requestAndApprove(t *testing.T, database *sql.DB, service *app.Service, email string) (int64, string) {
	t.Helper()
	id, secret, err := service.RequestSignup(context.Background(), email)
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}
	if err := sqlite.New(database).ApproveSignupRequest(context.Background(), id, testNow); err != nil {
		t.Fatalf("approve signup: %v", err)
	}
	return id, secret
}

func TestRequestSignupStoresNormalizedEmailAndHash(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	id, secret, err := service.RequestSignup(context.Background(), "  Human@Example.COM  ")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}

	var (
		email      string
		secretHash string
		requested  int64
	)
	if err := database.QueryRow(
		`SELECT email, secret_hash, requested_at FROM signup_requests WHERE id = ?`, id).
		Scan(&email, &secretHash, &requested); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if email != "human@example.com" {
		t.Fatalf("email = %q, want human@example.com", email)
	}
	if secretHash != credential.Hash(secret) {
		t.Fatal("stored hash does not match the returned secret")
	}
	if len(secretHash) != 64 {
		t.Fatalf("hash length = %d, want 64", len(secretHash))
	}
	if requested != testNow {
		t.Fatalf("requested_at = %d, want %d", requested, testNow)
	}
}

func TestClaimSignupCreatesHumanAndConsumesOnce(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})
	id, secret := requestAndApprove(t, database, service, "  Human@Example.COM ")

	user, token, err := service.ClaimSignup(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("claim signup: %v", err)
	}
	if user.Kind != domain.Human || !user.Active {
		t.Fatalf("user = %+v, want an active human", user)
	}
	if user.Email != "human@example.com" {
		t.Fatalf("email = %q, want human@example.com", user.Email)
	}

	var (
		kind          string
		active        int
		tokenHash     string
		consumedAt    sql.NullInt64
		requestUserID sql.NullInt64
	)
	if err := database.QueryRow(
		`SELECT kind, active, token_hash FROM users WHERE id = ?`, user.ID).
		Scan(&kind, &active, &tokenHash); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if kind != "human" || active != 1 {
		t.Fatalf("kind/active = %q/%d, want human/1", kind, active)
	}
	if tokenHash != credential.Hash(token) {
		t.Fatal("stored token hash does not match the returned token")
	}
	if err := database.QueryRow(
		`SELECT consumed_at, user_id FROM signup_requests WHERE id = ?`, id).
		Scan(&consumedAt, &requestUserID); err != nil {
		t.Fatalf("read request: %v", err)
	}
	if !consumedAt.Valid || consumedAt.Int64 != testNow {
		t.Fatalf("consumed_at = %v, want %d", consumedAt, testNow)
	}
	if !requestUserID.Valid || requestUserID.Int64 != user.ID {
		t.Fatalf("user_id = %v, want %d", requestUserID, user.ID)
	}

	_, _, err = service.ClaimSignup(context.Background(), id, secret)
	if code := ruleCode(t, err); code != domain.CodeSignupConsumed {
		t.Fatalf("second claim code = %q, want signup_consumed", code)
	}
}

func TestClaimSignupPendingAndWrongSecret(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})
	id, secret, err := service.RequestSignup(context.Background(), "human@example.com")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}

	_, _, err = service.ClaimSignup(context.Background(), id, secret)
	if code := ruleCode(t, err); code != domain.CodeSignupPending {
		t.Fatalf("premature claim code = %q, want signup_pending", code)
	}

	if _, err := database.Exec(
		`UPDATE signup_requests SET approved_at = ? WHERE id = ?`, testNow, id); err != nil {
		t.Fatalf("approve request: %v", err)
	}
	_, _, err = service.ClaimSignup(context.Background(), id, "wrong")
	if code := ruleCode(t, err); code != domain.CodeNotFound {
		t.Fatalf("wrong secret code = %q, want not_found", code)
	}
}

func TestLoginStoresHashedSessionWithThirtyDayExpiry(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})
	id, secret := requestAndApprove(t, database, service, "human@example.com")
	user, token, err := service.ClaimSignup(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("claim signup: %v", err)
	}

	session, cookieValue, loggedIn, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if loggedIn.ID != user.ID {
		t.Fatalf("logged-in user = %d, want %d", loggedIn.ID, user.ID)
	}
	if session.ID != credential.Hash(cookieValue) {
		t.Fatal("session id is not the hash of the returned value")
	}
	if session.ExpiresAt != testNow+30*24*3600 {
		t.Fatalf("expires_at = %d, want %d", session.ExpiresAt, testNow+30*24*3600)
	}

	var (
		storedID  string
		userID    int64
		expiresAt int64
		revokedAt sql.NullInt64
	)
	if err := database.QueryRow(
		`SELECT id, user_id, expires_at, revoked_at FROM sessions WHERE id = ?`, session.ID).
		Scan(&storedID, &userID, &expiresAt, &revokedAt); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if storedID != credential.Hash(cookieValue) {
		t.Fatal("stored session id is not the hash of the returned value")
	}
	if userID != user.ID || expiresAt != testNow+30*24*3600 || revokedAt.Valid {
		t.Fatalf("session row = %d/%d/%v, want user %d, expiry %d, not revoked",
			userID, expiresAt, revokedAt, user.ID, testNow+30*24*3600)
	}
}

func TestLoginRejectsAgentAndInactiveUserWithoutSession(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	agentToken := "agent-token"
	if _, err := database.Exec(`
		INSERT INTO users (email, name, kind, token_hash, active, created_at)
		VALUES (?, ?, 'agent', ?, 1, ?)`,
		"agent@example.com", "Agent", credential.Hash(agentToken), testNow); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	_, _, _, err := service.Login(context.Background(), agentToken)
	if code := ruleCode(t, err); code != domain.CodeForbidden {
		t.Fatalf("agent login code = %q, want forbidden", code)
	}

	inactiveToken := "inactive-token"
	if _, err := database.Exec(`
		INSERT INTO users (email, name, kind, token_hash, active, created_at)
		VALUES (?, ?, 'human', ?, 0, ?)`,
		"inactive@example.com", "Inactive", credential.Hash(inactiveToken), testNow); err != nil {
		t.Fatalf("seed inactive user: %v", err)
	}
	_, _, _, err = service.Login(context.Background(), inactiveToken)
	if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("inactive login code = %q, want unauthorized", code)
	}

	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Fatalf("session count = %d, want 0", count)
	}
}

func TestSessionUserExpiredRevokedAndUnknown(t *testing.T) {
	database := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)
	id, secret := requestAndApprove(t, database, service, "human@example.com")
	user, token, err := service.ClaimSignup(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("claim signup: %v", err)
	}
	_, cookieValue, _, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	got, err := service.SessionUser(context.Background(), cookieValue)
	if err != nil {
		t.Fatalf("session user: %v", err)
	}
	if got.ID != user.ID {
		t.Fatalf("user = %d, want %d", got.ID, user.ID)
	}

	clock.now = testNow + 30*24*3600
	_, err = service.SessionUser(context.Background(), cookieValue)
	if code := ruleCode(t, err); code != domain.CodeSessionExpired {
		t.Fatalf("expired code = %q, want session_expired", code)
	}

	clock.now = testNow
	if _, err := database.Exec(
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ?`, testNow, user.ID); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	_, err = service.SessionUser(context.Background(), cookieValue)
	if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("revoked code = %q, want unauthorized", code)
	}

	_, err = service.SessionUser(context.Background(), "unknown")
	if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("unknown code = %q, want unauthorized", code)
	}
}

func TestLogoutIsIdempotent(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})
	id, secret := requestAndApprove(t, database, service, "human@example.com")
	_, token, err := service.ClaimSignup(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("claim signup: %v", err)
	}
	_, cookieValue, _, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := service.Logout(context.Background(), cookieValue); err != nil {
		t.Fatalf("first logout: %v", err)
	}
	if err := service.Logout(context.Background(), cookieValue); err != nil {
		t.Fatalf("second logout: %v", err)
	}
	if _, err := service.SessionUser(context.Background(), cookieValue); err == nil {
		t.Fatal("revoked session still authenticated")
	}
}

func TestAuthenticateBothKinds(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	if _, err := database.Exec(`
		INSERT INTO users (email, name, kind, token_hash, active, created_at)
		VALUES
			('human@example.com', 'Human', 'human', ?, 1, ?),
			('agent@example.com', 'Agent', 'agent', ?, 1, ?)`,
		credential.Hash("human-token"), testNow,
		credential.Hash("agent-token"), testNow); err != nil {
		t.Fatalf("seed users: %v", err)
	}

	for _, token := range []string{"human-token", "agent-token"} {
		if _, err := service.Authenticate(context.Background(), token); err != nil {
			t.Fatalf("authenticate %q: %v", token, err)
		}
	}
	if _, err := service.Authenticate(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown token authenticated")
	}
}
