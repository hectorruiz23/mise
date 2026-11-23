package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mise/api/internal/platform/db"
	"mise/api/internal/platform/httpserver"
	taskshttp "mise/api/internal/tasks/adapters/http"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	tasksapp "mise/api/internal/tasks/app"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
)

type routeClock struct{}

func (routeClock) Now() int64 { return 1_700_000_000 }

func newRouteServer(t *testing.T) *httpserver.Server {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "mise.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	usersService := usersapp.New(userssqlite.New(database), routeClock{})
	tasksRepo := taskssqlite.New(database)
	tasksService := tasksapp.New(tasksRepo, routeClock{})
	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator: usershttp.NewAuthenticator(usersService),
		PublicOrigin:  "https://mise.example",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	registerRoutes(server, taskshttp.New(tasksService), usershttp.New(usersService, "https://mise.example"),
		taskshttp.NewDependencyPlans(tasksRepo, tasksapp.PlanPolicy{}, routeClock{}),
		taskshttp.NewGraphs(tasksRepo, tasksapp.PlanPolicy{}, routeClock{}))
	return server
}

func TestReadRoutesAllowTheVisitor(t *testing.T) {
	server := newRouteServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	cases := []struct {
		path   string
		status int
	}{
		{"/api/me", http.StatusOK},
		{"/api/board", http.StatusOK},
		{"/api/projects", http.StatusOK},
		{"/api/tasks", http.StatusOK},
		{"/api/tasks/NOPE-1", http.StatusNotFound},
		{"/api/tasks/NOPE-1/movements", http.StatusNotFound},
		{"/api/tasks/NOPE-1/comments", http.StatusNotFound},
		{"/api/tasks/NOPE-1/deliveries", http.StatusNotFound},
		{"/api/tasks/NOPE-1/leases", http.StatusNotFound},
	}

	for _, tc := range cases {
		resp, err := httpServer.Client().Get(httpServer.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		if resp.StatusCode != tc.status {
			t.Errorf("GET %s status = %d, want %d", tc.path, resp.StatusCode, tc.status)
		}
		resp.Body.Close()
	}

	resp, err := httpServer.Client().Get(httpServer.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer resp.Body.Close()
	var visitor struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&visitor); err != nil {
		t.Fatalf("decode visitor: %v", err)
	}
	if visitor.ID != 0 || visitor.Name != "Visitor" || visitor.Kind != "viewer" || visitor.Email != "" {
		t.Fatalf("visitor = %+v", visitor)
	}
}

func TestMutationsStillRequireACredential(t *testing.T) {
	server := newRouteServer(t)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/projects"},
		{http.MethodPost, "/api/tasks"},
		{http.MethodPatch, "/api/tasks/NOPE-1"},
		{http.MethodPost, "/api/tasks/NOPE-1/leases"},
		{http.MethodPatch, "/api/tasks/NOPE-1/leases/1"},
		{http.MethodDelete, "/api/tasks/NOPE-1/leases/1"},
		{http.MethodPost, "/api/tasks/NOPE-1/deliveries"},
		{http.MethodPost, "/api/tasks/NOPE-1/comments"},
		{http.MethodPatch, "/api/projects/NOPE-1"},
		{http.MethodPost, "/api/tasks/NOPE-1/submissions"},
		{http.MethodPost, "/api/plans"},
		{http.MethodGet, "/api/plans"},
		{http.MethodGet, "/api/plans/1"},
		{http.MethodPost, "/api/plans/1/cancel"},
		{http.MethodPost, "/api/dependency-plans"},
		{http.MethodGet, "/api/dependency-plans/NOPE-1"},
		{http.MethodPost, "/api/dependency-plans/NOPE-1/cancel"},
		{http.MethodDelete, "/api/sessions/current"},
	}

	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, httpServer.URL+tc.path, nil)
		if err != nil {
			t.Fatalf("new request %s %s: %v", tc.method, tc.path, err)
		}
		resp, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s status = %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}
