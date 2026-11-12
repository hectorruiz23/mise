package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	"mise/api/internal/users/adapters/sqlite"
	"mise/api/internal/users/app"
	"mise/api/internal/users/domain"
)

func openAt(t *testing.T, path string) *sql.DB {
	t.Helper()

	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}

func TestRequestSignupReusesPendingRowAndRotatesSecret(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	firstID, firstSecret, err := service.RequestSignup(context.Background(), "Human@Example.COM")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	secondID, secondSecret, err := service.RequestSignup(context.Background(), "  human@example.com ")
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if firstID != secondID {
		t.Fatalf("request ids = %d and %d, want the same pending row", firstID, secondID)
	}
	if firstSecret == secondSecret {
		t.Fatal("two requests returned the same secret")
	}

	var rows int
	if err := database.QueryRow(`SELECT count(*) FROM signup_requests`).Scan(&rows); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if rows != 1 {
		t.Fatalf("signup_requests rows = %d, want 1", rows)
	}

	if err := sqlite.New(database).ApproveSignupRequest(context.Background(), secondID, testNow); err != nil {
		t.Fatalf("approve: %v", err)
	}

	_, _, err = service.ClaimSignup(context.Background(), firstID, firstSecret)
	if code := ruleCode(t, err); code != domain.CodeNotFound {
		t.Fatalf("old secret code = %q, want not_found", code)
	}

	if _, _, err := service.ClaimSignup(context.Background(), secondID, secondSecret); err != nil {
		t.Fatalf("new secret claim: %v", err)
	}
}

func TestClaimSignupPendingCreatesNoUser(t *testing.T) {
	database := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	id, secret, err := service.RequestSignup(context.Background(), "human@example.com")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}
	_, _, err = service.ClaimSignup(context.Background(), id, secret)
	if code := ruleCode(t, err); code != domain.CodeSignupPending {
		t.Fatalf("claim code = %q, want signup_pending", code)
	}

	var users int
	if err := database.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 0 {
		t.Fatalf("users = %d after a premature claim, want 0", users)
	}
}

func TestClaimSignupConcurrentSeparateConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.db")
	firstDB := openAt(t, path)
	if err := db.Migrate(firstDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	secondDB := openAt(t, path)

	clock := &manualClock{now: testNow}
	first := app.New(sqlite.New(firstDB), clock)
	second := app.New(sqlite.New(secondDB), clock)

	id, secret := requestAndApprove(t, firstDB, first, "human@example.com")

	type outcome struct {
		token string
		err   error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for _, service := range []*app.Service{first, second} {
		wg.Add(1)
		go func(service *app.Service) {
			defer wg.Done()
			<-start
			_, token, err := service.ClaimSignup(context.Background(), id, secret)
			results <- outcome{token: token, err: err}
		}(service)
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, consumed int
	for result := range results {
		if result.err == nil {
			if result.token == "" {
				t.Error("successful claim returned an empty token")
			}
			successes++
			continue
		}
		if code := ruleCode(t, result.err); code == domain.CodeSignupConsumed {
			consumed++
		} else {
			t.Errorf("loser error code = %q, want signup_consumed", code)
		}
	}
	if successes != 1 || consumed != 1 {
		t.Fatalf("claims = %d successes / %d consumed, want 1 / 1", successes, consumed)
	}

	var users, consumedRequests int
	if err := firstDB.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := firstDB.QueryRow(
		`SELECT count(*) FROM signup_requests WHERE consumed_at IS NOT NULL`).
		Scan(&consumedRequests); err != nil {
		t.Fatalf("count consumed requests: %v", err)
	}
	if users != 1 {
		t.Fatalf("users = %d, want 1", users)
	}
	if consumedRequests != 1 {
		t.Fatalf("consumed requests = %d, want 1", consumedRequests)
	}
}

func TestClaimSignupRequestConcurrentLoserGetsConsumed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.db")
	firstDB := openAt(t, path)
	if err := db.Migrate(firstDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	secondDB := openAt(t, path)

	clock := &manualClock{now: testNow}
	first := sqlite.New(firstDB)
	second := sqlite.New(secondDB)
	service := app.New(first, clock)

	id, _, err := service.RequestSignup(context.Background(), "human@example.com")
	if err != nil {
		t.Fatalf("request signup: %v", err)
	}
	if err := first.ApproveSignupRequest(context.Background(), id, testNow); err != nil {
		t.Fatalf("approve: %v", err)
	}
	request, err := first.GetSignupRequest(context.Background(), id)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}

	makeUser := func(token string) domain.User {
		user, err := domain.NewUser(
			request.Email, domain.NameFromEmail(request.Email), domain.Human,
			credential.Hash(token), testNow)
		if err != nil {
			t.Fatalf("new user: %v", err)
		}
		return user
	}
	humanA := makeUser("token-a")
	humanB := makeUser("token-b")

	type outcome struct {
		rule *domain.RuleError
		err  error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for _, claim := range []struct {
		repo *sqlite.Repo
		user domain.User
	}{
		{repo: first, user: humanA},
		{repo: second, user: humanB},
	} {
		wg.Add(1)
		go func(repo *sqlite.Repo, user domain.User) {
			defer wg.Done()
			<-start
			_, rule, err := repo.ClaimSignupRequest(
				context.Background(), id, user, user.TokenHash, testNow)
			results <- outcome{rule: rule, err: err}
		}(claim.repo, claim.user)
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, consumed int
	for result := range results {
		if result.err != nil {
			t.Fatalf("claim signup request: %v", result.err)
		}
		if result.rule == nil {
			successes++
			continue
		}
		if result.rule.Code != domain.CodeSignupConsumed {
			t.Fatalf("rule code = %q, want signup_consumed", result.rule.Code)
		}
		consumed++
	}
	if successes != 1 || consumed != 1 {
		t.Fatalf("claims = %d successes / %d consumed, want 1 / 1", successes, consumed)
	}

	var users int
	if err := firstDB.QueryRow(`SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Fatalf("users = %d, want 1", users)
	}
}

func TestRequestSignupConcurrentSameEmailKeepsOneRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.db")
	firstDB := openAt(t, path)
	if err := db.Migrate(firstDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	secondDB := openAt(t, path)

	clock := &manualClock{now: testNow}
	first := newService(firstDB, clock)
	second := newService(secondDB, clock)

	type outcome struct {
		id     int64
		secret string
		err    error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for _, service := range []*app.Service{first, second} {
		wg.Add(1)
		go func(service *app.Service) {
			defer wg.Done()
			<-start
			id, secret, err := service.RequestSignup(context.Background(), "Human@Example.COM")
			results <- outcome{id: id, secret: secret, err: err}
		}(service)
	}
	close(start)
	wg.Wait()
	close(results)

	var (
		outcomes []outcome
		rows     int
	)
	for result := range results {
		if result.err != nil {
			t.Fatalf("request signup: %v", result.err)
		}
		outcomes = append(outcomes, result)
	}
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	if outcomes[0].id != outcomes[1].id {
		t.Fatalf("request ids = %d and %d, want the same pending row",
			outcomes[0].id, outcomes[1].id)
	}
	if err := firstDB.QueryRow(`SELECT count(*) FROM signup_requests`).Scan(&rows); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if rows != 1 {
		t.Fatalf("signup_requests rows = %d, want 1", rows)
	}

	var stored string
	if err := firstDB.QueryRow(
		`SELECT secret_hash FROM signup_requests WHERE id = ?`, outcomes[0].id).
		Scan(&stored); err != nil {
		t.Fatalf("read secret hash: %v", err)
	}
	matches := 0
	for _, result := range outcomes {
		if credential.Hash(result.secret) == stored {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("stored hash matches %d returned secrets, want exactly 1", matches)
	}
}
