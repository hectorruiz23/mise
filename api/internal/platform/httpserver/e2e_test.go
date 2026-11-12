package httpserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	"mise/api/internal/platform/httpserver"
	taskshttp "mise/api/internal/tasks/adapters/http"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	tasksapp "mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
	usersdomain "mise/api/internal/users/domain"
)

const humanToken = "test-human-token"

type testClock struct{}

func (testClock) Now() int64 { return 1_700_000_000 }

type testEnv struct {
	db           *sql.DB
	server       *httpserver.Server
	http         *httptest.Server
	userID       int64
	cookie       string
	expiredToken string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "mise.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	ctx := context.Background()
	usersRepo := userssqlite.New(database)
	user, err := usersdomain.NewUser("human@example.com", "Human", usersdomain.Human,
		credential.Hash(humanToken), 1_700_000_000)
	if err != nil {
		t.Fatalf("new user: %v", err)
	}
	user, err = usersRepo.CreateUser(ctx, user, credential.Hash(humanToken))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	usersService := usersapp.New(usersRepo, testClock{})

	tasksService := tasksapp.New(taskssqlite.New(database), testClock{})
	actor := domain.Actor{ID: user.ID, Kind: domain.Human, Name: user.Name}
	for _, title := range []string{"First", "Second", "Third"} {
		if _, _, err := tasksService.Create(ctx, tasksapp.CreateParams{
			ProjectKey: "MI",
			Title:      title,
			Actor:      actor,
		}); err != nil {
			t.Fatalf("create task %q: %v", title, err)
		}
	}

	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator: usershttp.NewAuthenticator(usersService),
		PublicOrigin:  "https://mise.example",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	tasksHandler := taskshttp.New(tasksService)
	usersHandler := usershttp.New(usersService, "https://mise.example")
	server.HandleFunc("GET /api/me", false, usersHandler.Me)
	server.HandleFunc("GET /api/projects", false, tasksHandler.Projects)
	server.HandleFunc("POST /api/projects", false, tasksHandler.CreateProject)
	server.HandleFunc("GET /api/board", false, tasksHandler.Board)
	server.HandleFunc("GET /api/tasks", false, tasksHandler.List)
	server.HandleFunc("POST /api/tasks", false, tasksHandler.Create)
	server.HandleFunc("PATCH /api/tasks/{key}", false, tasksHandler.Patch)
	server.HandleFunc("POST /api/sessions", true, usersHandler.Login)
	server.HandleFunc("DELETE /api/sessions/current", false, usersHandler.Logout)
	server.HandleFunc("POST /api/signup-requests", true, usersHandler.RequestSignup)
	server.HandleFunc("GET /api/signup-requests/{id}", true, usersHandler.SignupStatus)
	server.HandleFunc("POST /api/signup-requests/{id}/claim", true, usersHandler.ClaimSignup)

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(func() {
		httpServer.Close()
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	_, cookie, _, err := usersService.Login(ctx, humanToken)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	expiredSession, expiredCookie, _, err := usersService.Login(ctx, humanToken)
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`UPDATE sessions SET created_at = 1, expires_at = 2 WHERE id = ?`,
		expiredSession.ID); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	return &testEnv{
		db:           database,
		server:       server,
		http:         httpServer,
		userID:       user.ID,
		cookie:       cookie,
		expiredToken: expiredCookie,
	}
}

func (e *testEnv) request(t *testing.T, method, path string, body io.Reader, prepare func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.http.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if prepare != nil {
		prepare(req)
	}
	resp, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func bearer(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+humanToken)
}

func cookie(value string) func(*http.Request) {
	return func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: httpserver.SessionCookieName, Value: value})
	}
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var value T
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return value
}

func decodeError(t *testing.T, resp *http.Response) httpserver.ErrorBody {
	t.Helper()
	return decode[httpserver.ErrorBody](t, resp)
}

func TestHealthAndAuthentication(t *testing.T) {
	env := newTestEnv(t)

	resp := env.request(t, http.MethodGet, "/api/health", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	health := decode[struct {
		OK bool   `json:"ok"`
		DB string `json:"db"`
	}](t, resp)
	if !health.OK || health.DB != "ok" {
		t.Fatalf("health = %+v", health)
	}

	for _, path := range []string{"/api/me", "/api/projects", "/api/board", "/api/tasks"} {
		resp := env.request(t, http.MethodGet, path, nil, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without credential status = %d, want 401", path, resp.StatusCode)
		}
		if body := decodeError(t, resp); body.Error != "unauthorized" {
			t.Errorf("%s error = %q, want unauthorized", path, body.Error)
		}
	}

	resp = env.request(t, http.MethodGet, "/api/me", nil, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want 200", resp.StatusCode)
	}
	me := decode[struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Email string `json:"email"`
	}](t, resp)
	if me.ID != env.userID || me.Name != "Human" || me.Kind != "human" || me.Email != "human@example.com" {
		t.Fatalf("me = %+v", me)
	}
}

func TestProjectsAndBoard(t *testing.T) {
	env := newTestEnv(t)

	resp := env.request(t, http.MethodGet, "/api/projects", nil, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("projects status = %d, want 200", resp.StatusCode)
	}
	projects := decode[[]struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}](t, resp)
	if len(projects) != 3 || projects[0].Key != "MI" || projects[1].Key != "PO" || projects[2].Key != "DE" {
		t.Fatalf("projects = %+v", projects)
	}

	resp = env.request(t, http.MethodGet, "/api/board", nil, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("board status = %d, want 200", resp.StatusCode)
	}
	board := decode[struct {
		Columns     map[string][]json.RawMessage `json:"columns"`
		Counts      map[string]int               `json:"counts"`
		GeneratedAt int64                        `json:"generated_at"`
	}](t, resp)
	for _, state := range []string{"backlog", "ready", "in_progress", "blocked", "review", "done"} {
		if _, ok := board.Columns[state]; !ok {
			t.Errorf("board has no %q column", state)
		}
		if _, ok := board.Counts[state]; !ok {
			t.Errorf("board has no %q count", state)
		}
	}
	if board.GeneratedAt != 1_700_000_000 {
		t.Errorf("generated_at = %d", board.GeneratedAt)
	}
	if board.Counts["backlog"] != 3 {
		t.Errorf("backlog count = %d, want 3", board.Counts["backlog"])
	}
	if len(board.Columns["backlog"]) != board.Counts["backlog"] {
		t.Errorf("backlog rows = %d, count = %d", len(board.Columns["backlog"]), board.Counts["backlog"])
	}
}

func TestTasksPaginationAndFilterBoundCursor(t *testing.T) {
	env := newTestEnv(t)

	resp := env.request(t, http.MethodGet, "/api/tasks?project=MI&state=backlog&limit=2", nil, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first page status = %d, want 200", resp.StatusCode)
	}
	first := decode[struct {
		Tasks      []json.RawMessage `json:"tasks"`
		NextCursor string            `json:"next_cursor"`
	}](t, resp)
	if len(first.Tasks) != 2 {
		t.Fatalf("first page tasks = %d, want 2", len(first.Tasks))
	}
	if first.NextCursor == "" {
		t.Fatal("first page has no next_cursor")
	}

	resp = env.request(t, http.MethodGet,
		"/api/tasks?project=MI&state=backlog&limit=2&cursor="+first.NextCursor, nil, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second page status = %d, want 200", resp.StatusCode)
	}
	second := decode[struct {
		Tasks      []json.RawMessage `json:"tasks"`
		NextCursor string            `json:"next_cursor"`
	}](t, resp)
	if len(second.Tasks) != 1 {
		t.Fatalf("second page tasks = %d, want 1", len(second.Tasks))
	}
	if second.NextCursor != "" {
		t.Errorf("second page next_cursor = %q, want empty", second.NextCursor)
	}

	resp = env.request(t, http.MethodGet,
		"/api/tasks?project=PO&state=backlog&limit=2&cursor="+first.NextCursor, nil, bearer)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign cursor status = %d, want 400", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error != "invalid_input" {
		t.Errorf("foreign cursor error = %q, want invalid_input", body.Error)
	}
}

func TestCookieSessionsAndBearerPrecedence(t *testing.T) {
	env := newTestEnv(t)

	resp := env.request(t, http.MethodGet, "/api/me", nil, cookie(env.cookie))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid cookie status = %d, want 200", resp.StatusCode)
	}

	resp = env.request(t, http.MethodGet, "/api/me", nil, cookie(env.expiredToken))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired cookie status = %d, want 401", resp.StatusCode)
	}

	resp = env.request(t, http.MethodGet, "/api/me", nil, func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: httpserver.SessionCookieName, Value: env.cookie})
		req.Header.Set("Authorization", "Bearer bad-token")
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad bearer plus valid cookie status = %d, want 401", resp.StatusCode)
	}
}

func TestBodyLimitAndUnknownRoute(t *testing.T) {
	env := newTestEnv(t)

	resp := env.request(t, http.MethodGet, "/api/health", strings.NewReader(strings.Repeat("x", 2<<20)), nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodGet, "/api/unknown", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want 404", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error != "not_found" {
		t.Errorf("unknown route error = %q, want not_found", body.Error)
	}
}

func TestSessionRateLimit(t *testing.T) {
	env := newTestEnv(t)

	attempt := func(token string) *http.Response {
		t.Helper()
		return env.request(t, http.MethodPost, "/api/sessions",
			strings.NewReader(`{"token":"`+token+`"}`), func(req *http.Request) {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", "https://mise.example")
			})
	}

	for i := 1; i <= 10; i++ {
		resp := attempt(fmt.Sprintf("wrong-token-%d", i))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d status = %d, want 401", i, resp.StatusCode)
		}
		resp.Body.Close()
	}

	last := attempt("wrong-token-11")
	defer last.Body.Close()
	if last.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("eleventh attempt status = %d, want 429", last.StatusCode)
	}
	if last.Header.Get("Retry-After") == "" {
		t.Error("429 has no Retry-After")
	}
	if body := decodeError(t, last); body.Error != "rate_limited" {
		t.Errorf("error = %q, want rate_limited", body.Error)
	}
}
