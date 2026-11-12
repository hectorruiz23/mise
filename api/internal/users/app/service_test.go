package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mise/api/internal/platform/credential"
	"mise/api/internal/users/app"
	"mise/api/internal/users/domain"
)

const testNow = 1_700_000_000

type fakeClock int64

func (c fakeClock) Now() int64 { return int64(c) }

type fakeRepo struct {
	requests map[int64]app.SignupRequest
	users    map[int64]domain.User
	sessions map[string]domain.Session
	nextID   int64
	nextUser int64

	createdSignup *app.SignupRequest
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		requests: make(map[int64]app.SignupRequest),
		users:    make(map[int64]domain.User),
		sessions: make(map[string]domain.Session),
	}
}

func (r *fakeRepo) CreateSignupRequest(_ context.Context, email, secretHash string, at int64) (int64, error) {
	r.nextID++
	request := app.SignupRequest{
		ID:          r.nextID,
		Email:       email,
		SecretHash:  secretHash,
		RequestedAt: at,
	}
	r.requests[request.ID] = request
	r.createdSignup = &request
	return request.ID, nil
}

func (r *fakeRepo) GetSignupRequest(_ context.Context, id int64) (app.SignupRequest, error) {
	request, ok := r.requests[id]
	if !ok {
		return app.SignupRequest{}, &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown signup request"}
	}
	return request, nil
}

func (r *fakeRepo) ApproveSignupRequest(_ context.Context, id int64, at int64) error {
	request, ok := r.requests[id]
	if !ok {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown signup request"}
	}
	if request.ApprovedAt == nil {
		request.ApprovedAt = &at
		r.requests[id] = request
	}
	return nil
}

func (r *fakeRepo) ConsumeSignupRequest(_ context.Context, id int64, userID int64, at int64) error {
	request, ok := r.requests[id]
	if !ok {
		return &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown signup request"}
	}
	if request.ConsumedAt != nil {
		return &domain.RuleError{Code: domain.CodeSignupConsumed, Msg: "already consumed"}
	}
	request.ConsumedAt = &at
	request.UserID = &userID
	r.requests[id] = request
	return nil
}

func (r *fakeRepo) CreateUser(_ context.Context, u domain.User, tokenHash string) (domain.User, error) {
	r.nextUser++
	u.ID = r.nextUser
	u.TokenHash = tokenHash
	r.users[u.ID] = u
	return u, nil
}

func (r *fakeRepo) GetUserByTokenHash(_ context.Context, hash string) (domain.User, error) {
	for _, user := range r.users {
		if user.TokenHash == hash {
			return user, nil
		}
	}
	return domain.User{}, &domain.RuleError{Code: domain.CodeNotFound, Msg: "unknown token"}
}

func (r *fakeRepo) CreateSession(_ context.Context, s domain.Session) error {
	r.sessions[s.ID] = s
	return nil
}

func (r *fakeRepo) GetSession(_ context.Context, idHash string) (domain.Session, domain.User, error) {
	session, ok := r.sessions[idHash]
	if !ok {
		return domain.Session{}, domain.User{}, &domain.RuleError{
			Code: domain.CodeNotFound, Msg: "unknown session",
		}
	}
	user, ok := r.users[session.UserID]
	if !ok {
		return domain.Session{}, domain.User{}, &domain.RuleError{
			Code: domain.CodeNotFound, Msg: "unknown user",
		}
	}
	return session, user, nil
}

func (r *fakeRepo) RevokeSession(_ context.Context, idHash string, at int64) error {
	session, ok := r.sessions[idHash]
	if !ok {
		return nil
	}
	if session.RevokedAt == nil {
		session.RevokedAt = &at
		r.sessions[idHash] = session
	}
	return nil
}

func ruleCode(t *testing.T, err error) string {
	t.Helper()
	var rule *domain.RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want *domain.RuleError", err)
	}
	return rule.Code
}

func requestAndApprove(t *testing.T, service *app.Service, repo *fakeRepo, email string) (int64, string) {
	t.Helper()
	id, secret, err := service.RequestSignup(context.Background(), email)
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}
	if err := repo.ApproveSignupRequest(context.Background(), id, testNow); err != nil {
		t.Fatalf("approve signup: %v", err)
	}
	return id, secret
}

func TestRequestSignupNormalizesAndHashes(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))

	id, secret, err := service.RequestSignup(context.Background(), "  Human@Example.COM  ")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}
	if secret == "" {
		t.Fatal("empty secret")
	}
	request := repo.requests[id]
	if request.Email != "human@example.com" {
		t.Fatalf("email = %q, want human@example.com", request.Email)
	}
	if request.SecretHash == secret {
		t.Fatal("secret was stored in plain text")
	}
	if request.SecretHash != credential.Hash(secret) {
		t.Fatalf("stored hash = %q, want hash of secret", request.SecretHash)
	}
	if len(request.SecretHash) != 64 {
		t.Fatalf("hash length = %d, want 64", len(request.SecretHash))
	}
}

func TestRequestSignupRejectsInvalidEmail(t *testing.T) {
	service := app.New(newFakeRepo(), fakeClock(testNow))

	for _, email := range []string{"", "   ", "no-at-sign", strings.Repeat("a", 320) + "@x"} {
		_, _, err := service.RequestSignup(context.Background(), email)
		if err == nil {
			t.Fatalf("email %q was accepted", email)
		}
		if code := ruleCode(t, err); code != domain.CodeInvalidInput {
			t.Fatalf("email %q code = %q, want invalid_input", email, code)
		}
	}
}

func TestRequestSignupIssuesDifferentSecrets(t *testing.T) {
	service := app.New(newFakeRepo(), fakeClock(testNow))

	_, first, err := service.RequestSignup(context.Background(), "human@example.com")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, second, err := service.RequestSignup(context.Background(), "  HUMAN@example.com ")
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if first == second {
		t.Fatal("two requests returned the same secret")
	}
}

func TestSignupStatusLifecycle(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))
	id, secret := requestAndApprove(t, service, repo, "human@example.com")

	status, err := service.SignupStatus(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "approved" {
		t.Fatalf("status = %q, want approved", status)
	}

	_, _, err = service.ClaimSignup(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	status, err = service.SignupStatus(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("status after claim: %v", err)
	}
	if status != "consumed" {
		t.Fatalf("status = %q, want consumed", status)
	}
}

func TestSignupStatusPendingAndWrongSecret(t *testing.T) {
	service := app.New(newFakeRepo(), fakeClock(testNow))
	id, secret, err := service.RequestSignup(context.Background(), "human@example.com")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}

	status, err := service.SignupStatus(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}

	_, err = service.SignupStatus(context.Background(), id, "wrong")
	if code := ruleCode(t, err); code != domain.CodeNotFound {
		t.Fatalf("code = %q, want not_found", code)
	}
	_, err = service.SignupStatus(context.Background(), id+1, secret)
	if code := ruleCode(t, err); code != domain.CodeNotFound {
		t.Fatalf("code = %q, want not_found", code)
	}
}

func TestClaimSignupRequiresApprovalAndOneUse(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))
	id, secret, err := service.RequestSignup(context.Background(), "human@example.com")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}

	_, _, err = service.ClaimSignup(context.Background(), id, secret)
	if code := ruleCode(t, err); code != domain.CodeSignupPending {
		t.Fatalf("premature claim code = %q, want signup_pending", code)
	}
	_, _, err = service.ClaimSignup(context.Background(), id, "wrong")
	if code := ruleCode(t, err); code != domain.CodeNotFound {
		t.Fatalf("wrong secret code = %q, want not_found", code)
	}

	if err := repo.ApproveSignupRequest(context.Background(), id, testNow); err != nil {
		t.Fatalf("approve: %v", err)
	}
	user, token, err := service.ClaimSignup(context.Background(), id, secret)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if user.Kind != domain.Human {
		t.Fatalf("kind = %q, want human", user.Kind)
	}
	if !user.Active {
		t.Fatal("new user is inactive")
	}
	if user.Email != "human@example.com" {
		t.Fatalf("email = %q, want human@example.com", user.Email)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	if user.TokenHash != credential.Hash(token) {
		t.Fatal("stored token hash does not match the returned token")
	}
	request := repo.requests[id]
	if request.ConsumedAt == nil {
		t.Fatal("request was not marked consumed")
	}
	if request.UserID == nil || *request.UserID != user.ID {
		t.Fatal("request was not linked to the new user")
	}

	_, _, err = service.ClaimSignup(context.Background(), id, secret)
	if code := ruleCode(t, err); code != domain.CodeSignupConsumed {
		t.Fatalf("second claim code = %q, want signup_consumed", code)
	}
}

func TestLoginCreatesHashedThirtyDaySession(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))
	user, token, err := claimUser(t, service, repo, "Human@Example.COM")
	if err != nil {
		t.Fatalf("claim user: %v", err)
	}

	session, cookieValue, loggedIn, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if loggedIn.ID != user.ID {
		t.Fatalf("logged-in user = %d, want %d", loggedIn.ID, user.ID)
	}
	if cookieValue == "" {
		t.Fatal("empty session id")
	}
	if session.ID != credential.Hash(cookieValue) {
		t.Fatal("stored session id is not the hash of the returned id")
	}
	if session.ExpiresAt != testNow+30*24*3600 {
		t.Fatalf("expires_at = %d, want %d", session.ExpiresAt, testNow+30*24*3600)
	}
	if _, ok := repo.sessions[session.ID]; !ok {
		t.Fatal("session was not stored")
	}
}

func TestLoginRejectsAgentsAndInactiveUsers(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))

	agentToken := "agent-token"
	repo.users[1] = domain.User{
		ID: 1, Email: "agent@example.com", Name: "Agent", Kind: domain.Agent,
		TokenHash: credential.Hash(agentToken), Active: true, CreatedAt: testNow,
	}
	_, _, _, err := service.Login(context.Background(), agentToken)
	if code := ruleCode(t, err); code != domain.CodeForbidden {
		t.Fatalf("agent login code = %q, want forbidden", code)
	}
	if len(repo.sessions) != 0 {
		t.Fatal("agent login created a session")
	}

	inactiveToken := "inactive-token"
	repo.users[2] = domain.User{
		ID: 2, Email: "inactive@example.com", Name: "Inactive", Kind: domain.Human,
		TokenHash: credential.Hash(inactiveToken), Active: false, CreatedAt: testNow,
	}
	_, _, _, err = service.Login(context.Background(), inactiveToken)
	if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("inactive login code = %q, want unauthorized", code)
	}
	if len(repo.sessions) != 0 {
		t.Fatal("inactive login created a session")
	}
}

func TestAuthenticateAcceptsBothKinds(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))
	repo.users[1] = domain.User{
		ID: 1, Kind: domain.Human, TokenHash: credential.Hash("human-token"), Active: true,
	}
	repo.users[2] = domain.User{
		ID: 2, Kind: domain.Agent, TokenHash: credential.Hash("agent-token"), Active: true,
	}

	for _, token := range []string{"human-token", "agent-token"} {
		if _, err := service.Authenticate(context.Background(), token); err != nil {
			t.Fatalf("authenticate %q: %v", token, err)
		}
	}
	if _, err := service.Authenticate(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown token authenticated")
	} else if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("unknown token code = %q, want unauthorized", code)
	}
}

func TestSessionUserHonoursExpiryAndRevocation(t *testing.T) {
	repo := newFakeRepo()
	clock := fakeClock(testNow)
	service := app.New(repo, clock)
	user, token, err := claimUser(t, service, repo, "human@example.com")
	if err != nil {
		t.Fatalf("claim user: %v", err)
	}

	session, cookieValue, _, err := service.Login(context.Background(), token)
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

	expired := session
	expired.ExpiresAt = testNow
	repo.sessions[session.ID] = expired
	_, err = service.SessionUser(context.Background(), cookieValue)
	if code := ruleCode(t, err); code != domain.CodeSessionExpired {
		t.Fatalf("expired code = %q, want session_expired", code)
	}

	revoked := session
	revoked.RevokedAt = ptr(testNow)
	repo.sessions[session.ID] = revoked
	_, err = service.SessionUser(context.Background(), cookieValue)
	if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("revoked code = %q, want unauthorized", code)
	}

	_, err = service.SessionUser(context.Background(), "unknown")
	if code := ruleCode(t, err); code != domain.CodeUnauthorized {
		t.Fatalf("unknown code = %q, want unauthorized", code)
	}
}

func TestLogoutIsIdempotentAndRejectsSession(t *testing.T) {
	repo := newFakeRepo()
	service := app.New(repo, fakeClock(testNow))
	_, token, err := claimUser(t, service, repo, "human@example.com")
	if err != nil {
		t.Fatalf("claim user: %v", err)
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

func claimUser(t *testing.T, service *app.Service, repo *fakeRepo, email string) (domain.User, string, error) {
	t.Helper()
	id, secret := requestAndApprove(t, service, repo, email)
	return service.ClaimSignup(context.Background(), id, secret)
}

func ptr(value int64) *int64 { return &value }
