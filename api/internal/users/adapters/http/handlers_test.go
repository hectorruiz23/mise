package http_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	"mise/api/internal/platform/httpserver"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
	usersdomain "mise/api/internal/users/domain"
)

const (
	usersTestOrigin = "https://mise.example"
	usersHumanToken = "users-human-token"
	usersAgentToken = "users-agent-token"
)

type fixedClock struct{}

func (fixedClock) Now() int64 { return 1_700_000_000 }

type usersEnv struct {
	db      *sql.DB
	http    *httptest.Server
	service *usersapp.Service
}

func newUsersEnv(t *testing.T) *usersEnv {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "mise.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	ctx := context.Background()
	repo := userssqlite.New(database)
	for _, fixture := range []struct {
		email string
		name  string
		kind  usersdomain.Kind
		token string
	}{
		{"human@example.com", "Human", usersdomain.Human, usersHumanToken},
		{"agent@example.com", "Builder", usersdomain.Agent, usersAgentToken},
	} {
		user, err := usersdomain.NewUser(fixture.email, fixture.name, fixture.kind,
			credential.Hash(fixture.token), 1_700_000_000)
		if err != nil {
			t.Fatalf("new user: %v", err)
		}
		if _, err := repo.CreateUser(ctx, user, credential.Hash(fixture.token)); err != nil {
			t.Fatalf("create user: %v", err)
		}
	}
	service := usersapp.New(repo, fixedClock{})

	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator: usershttp.NewAuthenticator(service),
		PublicOrigin:  usersTestOrigin,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	handler := usershttp.New(service, usersTestOrigin)
	server.HandleFunc("GET /api/me", false, handler.Me)
	server.HandleFunc("POST /api/sessions", true, handler.Login)
	server.HandleFunc("DELETE /api/sessions/current", false, handler.Logout)
	server.HandleFunc("POST /api/signup-requests", true, handler.RequestSignup)
	server.HandleFunc("GET /api/signup-requests/{id}", true, handler.SignupStatus)
	server.HandleFunc("POST /api/signup-requests/{id}/claim", true, handler.ClaimSignup)

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	return &usersEnv{db: database, http: httpServer, service: service}
}

func (e *usersEnv) request(t *testing.T, method, path, body string, prepare func(*http.Request)) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.http.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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

func withOrigin(req *http.Request) {
	req.Header.Set("Origin", usersTestOrigin)
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

func TestLoginCookieAndLogout(t *testing.T) {
	env := newUsersEnv(t)

	resp := env.request(t, http.MethodPost, "/api/sessions",
		`{"token":"`+usersHumanToken+`"}`, withOrigin)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("login status = %d, want 201", resp.StatusCode)
	}
	var cookie *http.Cookie
	for _, candidate := range resp.Cookies() {
		if candidate.Name == httpserver.SessionCookieName {
			cookie = candidate
		}
	}
	resp.Body.Close()
	if cookie == nil {
		t.Fatal("login set no mise_session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if !cookie.Secure {
		t.Error("cookie is not Secure on an https origin")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q, want /", cookie.Path)
	}
	if cookie.MaxAge != 2592000 {
		t.Errorf("Max-Age = %d, want 2592000", cookie.MaxAge)
	}

	agent := env.request(t, http.MethodPost, "/api/sessions",
		`{"token":"`+usersAgentToken+`"}`, withOrigin)
	if agent.StatusCode != http.StatusForbidden {
		t.Fatalf("agent login status = %d, want 403", agent.StatusCode)
	}
	agent.Body.Close()

	badOrigin := env.request(t, http.MethodPost, "/api/sessions",
		`{"token":"`+usersHumanToken+`"}`, func(req *http.Request) {
			req.Header.Set("Origin", "https://evil.example")
		})
	if badOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong origin login status = %d, want 403", badOrigin.StatusCode)
	}
	badOrigin.Body.Close()

	logout := env.request(t, http.MethodDelete, "/api/sessions/current", "",
		func(req *http.Request) {
			req.AddCookie(cookie)
			withOrigin(req)
		})
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", logout.StatusCode)
	}
	cleared := logout.Cookies()
	logout.Body.Close()
	if len(cleared) != 1 || cleared[0].Name != httpserver.SessionCookieName || cleared[0].MaxAge >= 0 {
		t.Fatalf("logout cookies = %+v, want a cleared mise_session", cleared)
	}

	after := env.request(t, http.MethodGet, "/api/me", "", func(req *http.Request) {
		req.AddCookie(cookie)
	})
	if after.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked cookie status = %d, want 401", after.StatusCode)
	}
	after.Body.Close()

	bearerLogout := env.request(t, http.MethodDelete, "/api/sessions/current", "",
		func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+usersHumanToken)
		})
	if bearerLogout.StatusCode != http.StatusForbidden {
		t.Fatalf("bearer logout status = %d, want 403", bearerLogout.StatusCode)
	}
	bearerLogout.Body.Close()
}

func TestSignupLifecycle(t *testing.T) {
	env := newUsersEnv(t)

	request := env.request(t, http.MethodPost, "/api/signup-requests",
		`{"email":"new@example.com"}`, withOrigin)
	if request.StatusCode != http.StatusCreated {
		t.Fatalf("signup request status = %d, want 201", request.StatusCode)
	}
	created := decode[struct {
		ID     int64  `json:"id"`
		Secret string `json:"secret"`
	}](t, request)
	if created.ID == 0 || created.Secret == "" {
		t.Fatalf("signup response = %+v", created)
	}

	statusURL := "/api/signup-requests/" + strconv.FormatInt(created.ID, 10) + "?secret=" + created.Secret
	status := env.request(t, http.MethodGet, statusURL, "", nil)
	if status.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(status.Body)
		t.Fatalf("signup status status = %d, want 200 (url %s, body %s)", status.StatusCode, statusURL, body)
	}
	state := decode[struct {
		Status string `json:"status"`
	}](t, status)
	if state.Status != "pending" {
		t.Fatalf("signup status = %q, want pending", state.Status)
	}

	wrongSecret := env.request(t, http.MethodGet,
		"/api/signup-requests/"+strconv.FormatInt(created.ID, 10)+"?secret=wrong", "", nil)
	if wrongSecret.StatusCode != http.StatusNotFound {
		t.Fatalf("wrong status secret = %d, want 404", wrongSecret.StatusCode)
	}
	wrongSecret.Body.Close()

	pendingClaim := env.request(t, http.MethodPost,
		"/api/signup-requests/"+strconv.FormatInt(created.ID, 10)+"/claim",
		`{"secret":"`+created.Secret+`"}`, withOrigin)
	if pendingClaim.StatusCode != http.StatusConflict {
		t.Fatalf("claim before approval = %d, want 409", pendingClaim.StatusCode)
	}
	pendingClaim.Body.Close()

	if _, err := env.db.ExecContext(context.Background(),
		`UPDATE signup_requests SET approved_at = 1700000000 WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("approve signup: %v", err)
	}

	claim := env.request(t, http.MethodPost,
		"/api/signup-requests/"+strconv.FormatInt(created.ID, 10)+"/claim",
		`{"secret":"`+created.Secret+`"}`, withOrigin)
	if claim.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(claim.Body)
		_, _, err := env.service.ClaimSignup(context.Background(), created.ID, created.Secret)
		t.Fatalf("claim status = %d, want 201 (body %s, service error %v)", claim.StatusCode, body, err)
	}
	claimed := decode[struct {
		UserID int64  `json:"user_id"`
		Token  string `json:"token"`
	}](t, claim)
	if claimed.UserID == 0 || claimed.Token == "" {
		t.Fatalf("claim response = %+v", claimed)
	}

	me := env.request(t, http.MethodGet, "/api/me", "", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+claimed.Token)
	})
	if me.StatusCode != http.StatusOK {
		t.Fatalf("claimed token status = %d, want 200", me.StatusCode)
	}
	me.Body.Close()

	second := env.request(t, http.MethodPost,
		"/api/signup-requests/"+strconv.FormatInt(created.ID, 10)+"/claim",
		`{"secret":"`+created.Secret+`"}`, withOrigin)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second claim status = %d, want 409", second.StatusCode)
	}
	second.Body.Close()
}
