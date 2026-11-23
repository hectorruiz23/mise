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
	"strings"
	"sync/atomic"
	"testing"

	"mise/api/internal/githubinfo"
	"mise/api/internal/platform/credential"
	"mise/api/internal/platform/db"
	"mise/api/internal/platform/httpserver"
	taskshttp "mise/api/internal/tasks/adapters/http"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	tasksapp "mise/api/internal/tasks/app"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
	usersdomain "mise/api/internal/users/domain"
)

const (
	testOrigin = "https://mise.example"
	testToken  = "tasks-test-human-token"
)

type fixedClock struct{}

func (fixedClock) Now() int64 { return 1_700_000_000 }

type tasksEnv struct {
	db         *sql.DB
	http       *httptest.Server
	cookie     string
	githubHits *atomic.Int64
}

func newTasksEnv(t *testing.T) *tasksEnv {
	t.Helper()
	return newTasksEnvWithGitHub(t, defaultGitHubHandler)
}

func newTasksEnvWithGitHub(t *testing.T, githubHandler http.HandlerFunc) *tasksEnv {
	t.Helper()

	var githubHits atomic.Int64
	githubServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		githubHits.Add(1)
		githubHandler(w, r)
	}))
	t.Cleanup(githubServer.Close)

	database, err := db.Open(filepath.Join(t.TempDir(), "mise.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	ctx := context.Background()
	usersRepo := userssqlite.New(database)
	user, err := usersdomain.NewUser("human@example.com", "Human", usersdomain.Human,
		credential.Hash(testToken), 1_700_000_000)
	if err != nil {
		t.Fatalf("new user: %v", err)
	}
	user, err = usersRepo.CreateUser(ctx, user, credential.Hash(testToken))
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	usersService := usersapp.New(usersRepo, fixedClock{})
	tasksService := tasksapp.New(taskssqlite.New(database), fixedClock{})

	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator: usershttp.NewAuthenticator(usersService),
		PublicOrigin:  testOrigin,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	tasksHandler := taskshttp.NewWithAnchors(tasksService, githubinfo.New(githubinfo.Options{
		BaseURL: githubServer.URL,
		Clock:   fixedClock{},
	}))
	usersHandler := usershttp.New(usersService, testOrigin)
	graphsHandler := taskshttp.NewGraphs(taskssqlite.New(database), tasksapp.PlanPolicy{}, fixedClock{})
	server.HandleReadFunc("GET /api/me", usersHandler.Me)
	server.HandleFunc("GET /api/public/board", true, tasksHandler.PublicBoard)
	server.HandleReadFunc("GET /api/projects", tasksHandler.Projects)
	server.HandleFunc("POST /api/projects", false, tasksHandler.CreateProject)
	server.HandleFunc("PATCH /api/projects/{key}", false, tasksHandler.PatchProject)
	server.HandleFunc("GET /api/projects/{key}/environments", false, tasksHandler.Environments)
	server.HandleFunc("POST /api/projects/{key}/environments", false, tasksHandler.AddEnvironment)
	server.HandleFunc("POST /api/plans/{id}/scenario", false, graphsHandler.ReportScenario)
	server.HandleReadFunc("GET /api/board", tasksHandler.Board)
	server.HandleReadFunc("GET /api/tasks", tasksHandler.List)
	server.HandleFunc("POST /api/tasks", false, tasksHandler.Create)
	server.HandleReadFunc("GET /api/tasks/{key}", tasksHandler.Detail)
	server.HandleFunc("PATCH /api/tasks/{key}", false, tasksHandler.Patch)
	server.HandleReadFunc("GET /api/tasks/{key}/movements", tasksHandler.Movements)
	server.HandleReadFunc("GET /api/tasks/{key}/leases", tasksHandler.ListLeases)
	server.HandleFunc("POST /api/tasks/{key}/leases", false, tasksHandler.AcquireLease)
	server.HandleFunc("PATCH /api/tasks/{key}/leases/{id}", false, tasksHandler.RenewLease)
	server.HandleFunc("DELETE /api/tasks/{key}/leases/{id}", false, tasksHandler.ReleaseLease)
	server.HandleReadFunc("GET /api/tasks/{key}/deliveries", tasksHandler.ListDeliveries)
	server.HandleFunc("POST /api/tasks/{key}/deliveries", false, tasksHandler.CreateDelivery)
	server.HandleReadFunc("GET /api/tasks/{key}/anchors", tasksHandler.Anchors)
	server.HandleReadFunc("GET /api/tasks/{key}/comments", tasksHandler.ListComments)
	server.HandleFunc("POST /api/tasks/{key}/comments", false, tasksHandler.CreateComment)
	server.HandleFunc("POST /api/sessions", true, usersHandler.Login)

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	_, cookie, _, err := usersService.Login(ctx, testToken)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	_ = user
	return &tasksEnv{db: database, http: httpServer, cookie: cookie, githubHits: &githubHits}
}

func defaultGitHubHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.HasSuffix(r.URL.Path, "/commits/main"):
		_, _ = io.WriteString(w, `{"sha":"main-sha"}`)
	case strings.Contains(r.URL.Path, "/pulls/"):
		_, _ = io.WriteString(w, `{"state":"open","merged":false,"merge_commit_sha":null,"head":{"sha":"pr-sha","ref":"feature"}}`)
	case strings.Contains(r.URL.Path, "/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs"):
		_, _ = io.WriteString(w, `{"total_count":3,"jobs":[
			{"name":"api","status":"completed","conclusion":"success","html_url":"https://github.com/hectorruiz23/mise/actions/runs/100/job/1"},
			{"name":"accept","status":"completed","conclusion":"success","html_url":"https://github.com/hectorruiz23/mise/actions/runs/100/job/2"},
			{"name":"panel","status":"completed","conclusion":"success","html_url":"https://github.com/hectorruiz23/mise/actions/runs/100/job/3"}]}`)
	case strings.HasSuffix(r.URL.Path, "/actions/runs"):
		_, _ = io.WriteString(w, `{"total_count":1,"workflow_runs":[
			{"id":100,"head_sha":"pr-sha","head_branch":"feature","event":"pull_request","status":"completed","conclusion":"success","run_attempt":1}]}`)
	default:
		http.NotFound(w, r)
	}
}

func (e *tasksEnv) request(t *testing.T, method, path, body string, prepare func(*http.Request)) *http.Response {
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

func withBearer(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+testToken)
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

type taskBody struct {
	Key        string  `json:"key"`
	Title      string  `json:"title"`
	Notes      *string `json:"notes"`
	State      string  `json:"state"`
	Version    int64   `json:"version"`
	Acceptance *string `json:"acceptance"`
	CreatedBy  int64   `json:"created_by"`
}

func createTask(t *testing.T, env *tasksEnv, key, body string) taskBody {
	t.Helper()
	resp := env.request(t, http.MethodPost, "/api/tasks", body, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("Idempotency-Key", key)
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	return decode[taskBody](t, resp)
}

func TestCreateByAgentLandsInBacklogAndCannotPromote(t *testing.T) {
	env := newTasksEnv(t)
	worker := createAgent(t, env)

	created := createTaskAs(t, env, "agent-create-1",
		`{"title":"Filed by an agent","project":"MI","notes":"proposal","acceptance":"the owner promotes or drops it"}`,
		withAgentBearer)
	if created.State != "backlog" {
		t.Fatalf("state = %q, want backlog", created.State)
	}
	if created.CreatedBy != worker.ID {
		t.Fatalf("created_by = %d, want the agent id %d", created.CreatedBy, worker.ID)
	}
	if created.Version != 1 {
		t.Fatalf("version = %d, want 1", created.Version)
	}

	detail := env.request(t, http.MethodGet, "/api/tasks/"+created.Key, "", withBearer)
	movements := decode[detailBody](t, detail).Movements
	if len(movements) != 1 || movements[0].ToState != "backlog" || movements[0].ActorID != worker.ID {
		t.Fatalf("movements = %+v, want a single backlog movement by the agent", movements)
	}

	resp := env.request(t, http.MethodPatch, "/api/tasks/"+created.Key, `{"state":"ready"}`,
		func(req *http.Request) {
			withAgentBearer(req)
			req.Header.Set("If-Match", `"1"`)
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("promote status = %d, want 409", resp.StatusCode)
	}
	body := decode[apiErrorBody](t, resp)
	if body.Error != "lease_required" {
		t.Fatalf("promote error = %q, want lease_required", body.Error)
	}

	gate := env.request(t, http.MethodPost, "/api/tasks",
		`{"title":"Straight to ready","project":"MI","state":"ready"}`, func(req *http.Request) {
			withAgentBearer(req)
			req.Header.Set("Idempotency-Key", "agent-create-ready")
		})
	defer gate.Body.Close()
	if gate.StatusCode != http.StatusBadRequest {
		t.Fatalf("state in body status = %d, want 400", gate.StatusCode)
	}
}

func createTaskAs(t *testing.T, env *tasksEnv, key, body string, auth func(*http.Request)) taskBody {
	t.Helper()
	resp := env.request(t, http.MethodPost, "/api/tasks", body, func(req *http.Request) {
		auth(req)
		req.Header.Set("Idempotency-Key", key)
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	return decode[taskBody](t, resp)
}

func TestCreateRequiresAndReplaysIdempotencyKey(t *testing.T) {
	env := newTasksEnv(t)

	resp := env.request(t, http.MethodPost, "/api/tasks",
		`{"title":"No key","project":"MI"}`, withBearer)
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("missing key status = %d, want 428", resp.StatusCode)
	}
	resp.Body.Close()

	body := `{"title":"Idempotent","project":"MI","notes":"first"}`
	first := env.request(t, http.MethodPost, "/api/tasks", body, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("Idempotency-Key", "create-1")
	})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", first.StatusCode)
	}
	location := first.Header.Get("Location")
	if location == "" || first.Header.Get("X-Task-Version") != "1" {
		t.Fatalf("create headers Location=%q X-Task-Version=%q", location, first.Header.Get("X-Task-Version"))
	}
	created := decode[taskBody](t, first)
	if location != "/api/tasks/"+created.Key {
		t.Fatalf("Location = %q, want /api/tasks/%s", location, created.Key)
	}

	var count int
	if err := env.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM tasks WHERE key = ?`, created.Key).Scan(&count); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count != 1 {
		t.Fatalf("task rows = %d, want 1", count)
	}

	replay := env.request(t, http.MethodPost, "/api/tasks", body, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("Idempotency-Key", "create-1")
	})
	if replay.StatusCode != http.StatusCreated {
		t.Fatalf("replay status = %d, want 201", replay.StatusCode)
	}
	if replay.Header.Get("Location") != location {
		t.Errorf("replay Location = %q, want %q", replay.Header.Get("Location"), location)
	}
	again := decode[taskBody](t, replay)
	if again.Key != created.Key || again.Version != 1 {
		t.Errorf("replay task = %+v, want key %s version 1", again, created.Key)
	}
	if err := env.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM tasks WHERE key = ?`, created.Key).Scan(&count); err != nil {
		t.Fatalf("count tasks after replay: %v", err)
	}
	if count != 1 {
		t.Fatalf("task rows after replay = %d, want 1", count)
	}

	conflict := env.request(t, http.MethodPost, "/api/tasks",
		`{"title":"Different","project":"MI"}`, func(req *http.Request) {
			withBearer(req)
			req.Header.Set("Idempotency-Key", "create-1")
		})
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("different payload status = %d, want 409", conflict.StatusCode)
	}
	conflict.Body.Close()
}

func TestCreateValidation(t *testing.T) {
	env := newTasksEnv(t)

	cases := []struct {
		name string
		body string
	}{
		{"unknown field", `{"title":"X","project":"MI","bogus":true}`},
		{"blank title", `{"title":"   ","project":"MI"}`},
		{"unknown project", `{"title":"X","project":"NOPE"}`},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.request(t, http.MethodPost, "/api/tasks", tc.body, func(req *http.Request) {
				withBearer(req)
				req.Header.Set("Idempotency-Key", "validation-"+string(rune('a'+i)))
			})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			resp.Body.Close()
		})
	}
}

func TestPatchPreconditionsVersionAndNulls(t *testing.T) {
	env := newTasksEnv(t)
	task := createTask(t, env, "patch-create", `{"title":"Original","project":"MI","notes":"keep"}`)
	path := "/api/tasks/" + task.Key

	resp := env.request(t, http.MethodPatch, path, `{"title":"Nope"}`, withBearer)
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d, want 428", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPatch, path, `{"title":"Nope"}`, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("If-Match", `"99"`)
	})
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match status = %d, want 412", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPatch, path, `{"title":"Nope"}`, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("If-Match", "3")
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed If-Match status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPatch, path, `{}`, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("If-Match", `"1"`)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty patch status = %d, want 200", resp.StatusCode)
	}
	edited := decode[taskBody](t, resp)
	if edited.Version != 2 || resp.Header.Get("X-Task-Version") != "2" {
		t.Fatalf("version = %d header = %q, want 2", edited.Version, resp.Header.Get("X-Task-Version"))
	}
	if edited.Notes == nil || *edited.Notes != "keep" {
		t.Fatalf("notes = %v, want keep", edited.Notes)
	}

	resp = env.request(t, http.MethodPatch, path, `{"notes":null}`, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("If-Match", `"2"`)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("null notes status = %d, want 200", resp.StatusCode)
	}
	cleared := decode[taskBody](t, resp)
	if cleared.Version != 3 || cleared.Notes != nil {
		t.Fatalf("cleared task version=%d notes=%v, want version 3 and nil notes", cleared.Version, cleared.Notes)
	}

	resp = env.request(t, http.MethodPatch, path, `{"title":"Renamed","acceptance":"prove it"}`, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("If-Match", `"3"`)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("title patch status = %d, want 200", resp.StatusCode)
	}
	renamed := decode[taskBody](t, resp)
	if renamed.Title != "Renamed" || renamed.Version != 4 {
		t.Fatalf("renamed = %+v", renamed)
	}
	if renamed.Notes != nil {
		t.Fatalf("notes = %v, want still nil", renamed.Notes)
	}
}

func TestPatchOriginRules(t *testing.T) {
	env := newTasksEnv(t)
	task := createTask(t, env, "origin-create", `{"title":"Origin","project":"MI"}`)
	path := "/api/tasks/" + task.Key

	resp := env.request(t, http.MethodPatch, path, `{"title":"No origin"}`, func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: httpserver.SessionCookieName, Value: env.cookie})
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie without Origin status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPatch, path, `{"title":"Wrong origin"}`, func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: httpserver.SessionCookieName, Value: env.cookie})
		req.Header.Set("Origin", "https://evil.example")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cookie with wrong Origin status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPatch, path, `{"title":"Bearer"}`, func(req *http.Request) {
		withBearer(req)
		req.Header.Set("If-Match", `"1"`)
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bearer without Origin status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}
