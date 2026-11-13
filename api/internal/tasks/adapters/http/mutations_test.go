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
	taskshttp "mise/api/internal/tasks/adapters/http"
	taskssqlite "mise/api/internal/tasks/adapters/sqlite"
	tasksapp "mise/api/internal/tasks/app"
	usershttp "mise/api/internal/users/adapters/http"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersapp "mise/api/internal/users/app"
	usersdomain "mise/api/internal/users/domain"
)

const (
	mutHumanToken = "mutations-human-token"
	mutAgentToken = "mutations-agent-token"
	mutOtherToken = "mutations-other-agent-token"
)

type mutableClock struct {
	now int64
}

func (c *mutableClock) Now() int64 { return c.now }

type mutationEnv struct {
	db     *sql.DB
	http   *httptest.Server
	clock  *mutableClock
	agents map[string]int64
}

func newMutationEnv(t *testing.T) *mutationEnv {
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
	usersRepo := userssqlite.New(database)
	users := []struct {
		email string
		name  string
		kind  usersdomain.Kind
		token string
	}{
		{email: "human@example.com", name: "Human", kind: usersdomain.Human, token: mutHumanToken},
		{email: "agent@example.com", name: "Worker", kind: usersdomain.Agent, token: mutAgentToken},
		{email: "other@example.com", name: "Other", kind: usersdomain.Agent, token: mutOtherToken},
	}
	ids := make(map[string]int64, len(users))
	for _, item := range users {
		user, err := usersdomain.NewUser(item.email, item.name, item.kind,
			credential.Hash(item.token), 1_700_000_000)
		if err != nil {
			t.Fatalf("new user %q: %v", item.email, err)
		}
		stored, err := usersRepo.CreateUser(ctx, user, credential.Hash(item.token))
		if err != nil {
			t.Fatalf("create user %q: %v", item.email, err)
		}
		ids[item.token] = stored.ID
	}

	clock := &mutableClock{now: 1_700_000_000}
	usersService := usersapp.New(usersRepo, clock)
	tasksService := tasksapp.New(taskssqlite.New(database), clock)

	server, err := httpserver.New(httpserver.Options{
		DB:            database,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Authenticator: usershttp.NewAuthenticator(usersService),
		PublicOrigin:  testOrigin,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	tasksHandler := taskshttp.New(tasksService)
	server.HandleFunc("POST /api/tasks", false, tasksHandler.Create)
	server.HandleFunc("GET /api/tasks/{key}", false, tasksHandler.Detail)
	server.HandleFunc("PATCH /api/tasks/{key}", false, tasksHandler.Patch)
	server.HandleFunc("GET /api/tasks/{key}/movements", false, tasksHandler.Movements)
	server.HandleFunc("GET /api/tasks/{key}/leases", false, tasksHandler.ListLeases)
	server.HandleFunc("POST /api/tasks/{key}/leases", false, tasksHandler.AcquireLease)
	server.HandleFunc("PATCH /api/tasks/{key}/leases/{id}", false, tasksHandler.RenewLease)
	server.HandleFunc("DELETE /api/tasks/{key}/leases/{id}", false, tasksHandler.ReleaseLease)
	server.HandleFunc("GET /api/tasks/{key}/deliveries", false, tasksHandler.ListDeliveries)
	server.HandleFunc("POST /api/tasks/{key}/deliveries", false, tasksHandler.CreateDelivery)
	server.HandleFunc("GET /api/tasks/{key}/comments", false, tasksHandler.ListComments)
	server.HandleFunc("POST /api/tasks/{key}/comments", false, tasksHandler.CreateComment)

	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	return &mutationEnv{db: database, http: httpServer, clock: clock, agents: ids}
}

func (e *mutationEnv) request(t *testing.T, method, path, token, body string) *http.Response {
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func (e *mutationEnv) create(t *testing.T, title string) taskBody {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.http.URL+"/api/tasks",
		strings.NewReader(`{"title":"`+title+`","project":"MI"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mutHumanToken)
	req.Header.Set("Idempotency-Key", "create-"+title)
	resp, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	return decodeTask(t, resp)
}

func (e *mutationEnv) toReady(t *testing.T, key string) taskBody {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, e.http.URL+"/api/tasks/"+key,
		strings.NewReader(`{"state":"ready"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mutHumanToken)
	req.Header.Set("If-Match", `"1"`)
	resp, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("ready status = %d, want 200", resp.StatusCode)
	}
	return decodeTask(t, resp)
}

type actorBody struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type leaseBody struct {
	ID           int64      `json:"id"`
	TaskKey      string     `json:"task_key"`
	AgentID      int64      `json:"agent_id"`
	Agent        *actorBody `json:"agent"`
	CreatedAt    int64      `json:"created_at"`
	ExpiresAt    int64      `json:"expires_at"`
	ReleasedAt   *int64     `json:"released_at"`
	ClosedReason *string    `json:"closed_reason"`
}

type deliveryBody struct {
	ID        int64      `json:"id"`
	TaskKey   string     `json:"task_key"`
	LeaseID   *int64     `json:"lease_id"`
	Summary   string     `json:"summary"`
	Evidence  []string   `json:"evidence"`
	PRURL     *string    `json:"pr_url"`
	CreatedAt int64      `json:"created_at"`
	ActorID   int64      `json:"actor_id"`
	Actor     *actorBody `json:"actor"`
}

type commentBody struct {
	ID        int64      `json:"id"`
	TaskKey   string     `json:"task_key"`
	ActorID   int64      `json:"actor_id"`
	Actor     *actorBody `json:"actor"`
	Body      string     `json:"body"`
	CreatedAt int64      `json:"created_at"`
}

type movementBody struct {
	TaskKey     string     `json:"task_key"`
	FromState   *string    `json:"from_state"`
	ToState     string     `json:"to_state"`
	ActorID     int64      `json:"actor_id"`
	Actor       *actorBody `json:"actor"`
	Reason      *string    `json:"reason"`
	TaskVersion int64      `json:"task_version"`
	At          int64      `json:"at"`
}

type detailBody struct {
	Task       taskBody       `json:"task"`
	Movements  []movementBody `json:"movements"`
	Leases     []leaseBody    `json:"leases"`
	Deliveries []deliveryBody `json:"deliveries"`
	Comments   []commentBody  `json:"comments"`
}

type deliveryResponse struct {
	Delivery deliveryBody `json:"delivery"`
	Task     taskBody     `json:"task"`
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	var value T
	if err := json.NewDecoder(resp.Body).Decode(&value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return value
}

func decodeTask(t *testing.T, resp *http.Response) taskBody {
	return decodeBody[taskBody](t, resp)
}

func decodeError(t *testing.T, resp *http.Response) httpserver.ErrorBody {
	return decodeBody[httpserver.ErrorBody](t, resp)
}

func TestAgentFlowLeaseDeliverApprove(t *testing.T) {
	env := newMutationEnv(t)

	task := env.create(t, "Flow")
	if task.Key != "MI-1" || task.State != "backlog" || task.Version != 1 {
		t.Fatalf("created = %+v", task)
	}
	task = env.toReady(t, task.Key)
	if task.State != "ready" || task.Version != 2 {
		t.Fatalf("ready = %+v", task)
	}

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		body := decodeError(t, resp)
		t.Fatalf("lease status = %d (%s), want 201", resp.StatusCode, body.Error)
	}
	if got := resp.Header.Get("X-Task-Version"); got != "3" {
		t.Errorf("lease X-Task-Version = %q, want 3", got)
	}
	lease := decodeBody[leaseBody](t, resp)
	if lease.ID == 0 || lease.AgentID != env.agents[mutAgentToken] ||
		lease.ExpiresAt != env.clock.now+900 {
		t.Fatalf("lease = %+v", lease)
	}

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutOtherToken, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second lease status = %d, want 409", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error != "lease_conflict" {
		t.Errorf("second lease error = %q, want lease_conflict", body.Error)
	}

	resp = env.request(t, http.MethodPatch,
		"/api/tasks/"+task.Key+"/leases/"+strconv.FormatInt(lease.ID, 10), mutOtherToken, "{}")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("foreign renewal status = %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()

	env.clock.now += 100
	resp = env.request(t, http.MethodPatch,
		"/api/tasks/"+task.Key+"/leases/"+strconv.FormatInt(lease.ID, 10), mutAgentToken, "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner renewal status = %d, want 200", resp.StatusCode)
	}
	renewed := decodeBody[leaseBody](t, resp)
	if renewed.ExpiresAt <= lease.ExpiresAt {
		t.Fatalf("renewed expires_at = %d, want later than %d", renewed.ExpiresAt, lease.ExpiresAt)
	}

	deliver := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries", mutAgentToken,
		`{"summary":"Implemented","evidence":["go test ./...","make verify"]}`)
	if deliver.StatusCode != http.StatusCreated {
		body := decodeError(t, deliver)
		t.Fatalf("delivery status = %d (%s), want 201", deliver.StatusCode, body.Error)
	}
	if got := deliver.Header.Get("X-Task-Version"); got != "4" {
		t.Errorf("delivery X-Task-Version = %q, want 4", got)
	}
	delivered := decodeBody[deliveryResponse](t, deliver)
	if delivered.Task.State != "review" || delivered.Task.Version != 4 {
		t.Fatalf("delivered task = %+v", delivered.Task)
	}
	if len(delivered.Delivery.Evidence) != 2 || delivered.Delivery.LeaseID == nil ||
		*delivered.Delivery.LeaseID != lease.ID {
		t.Fatalf("delivery = %+v", delivered.Delivery)
	}

	req, err := http.NewRequest(http.MethodPatch, env.http.URL+"/api/tasks/"+task.Key,
		strings.NewReader(`{"state":"done"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mutHumanToken)
	req.Header.Set("If-Match", `"4"`)
	resp, err = env.http.Client().Do(req)
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("done status = %d, want 200", resp.StatusCode)
	}
	closed := decodeTask(t, resp)
	if closed.State != "done" || closed.Version != 5 {
		t.Fatalf("closed = %+v", closed)
	}

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key, mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", resp.StatusCode)
	}
	detail := decodeBody[detailBody](t, resp)
	if detail.Task.State != "done" {
		t.Errorf("detail task state = %q, want done", detail.Task.State)
	}
	if len(detail.Movements) != 5 {
		t.Fatalf("movements = %d, want 5", len(detail.Movements))
	}
	wantStates := []string{"backlog", "ready", "in_progress", "review", "done"}
	for i, want := range wantStates {
		if detail.Movements[i].ToState != want {
			t.Errorf("movement %d to = %q, want %q", i, detail.Movements[i].ToState, want)
		}
	}
	if detail.Movements[0].FromState != nil {
		t.Errorf("creation movement from_state = %v, want null", *detail.Movements[0].FromState)
	}
	if len(detail.Leases) != 1 || len(detail.Deliveries) != 1 || detail.Comments == nil {
		t.Errorf("detail children = leases %d, deliveries %d, comments %v",
			len(detail.Leases), len(detail.Deliveries), detail.Comments)
	}
}

func TestDetailExposesActorNames(t *testing.T) {
	env := newMutationEnv(t)
	humanID := env.agents[mutHumanToken]
	agentID := env.agents[mutAgentToken]

	task := env.create(t, "Actors")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	lease := decodeBody[leaseBody](t, resp)
	requireActor(t, "acquired lease agent", lease.Agent, agentID, "Worker", "agent")

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries", mutAgentToken,
		`{"summary":"Implemented","evidence":["go test ./..."]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delivery status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/comments", mutHumanToken,
		`{"body":"Human note"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("human comment status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/comments", mutAgentToken,
		`{"body":"Agent note"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("agent comment status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key, mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", resp.StatusCode)
	}
	detail := decodeBody[detailBody](t, resp)

	if len(detail.Movements) != 4 {
		t.Fatalf("movements = %d, want 4", len(detail.Movements))
	}
	requireActor(t, "creation movement", detail.Movements[0].Actor, humanID, "Human", "human")
	requireActor(t, "ready movement", detail.Movements[1].Actor, humanID, "Human", "human")
	requireActor(t, "lease movement", detail.Movements[2].Actor, agentID, "Worker", "agent")
	requireActor(t, "delivery movement", detail.Movements[3].Actor, agentID, "Worker", "agent")
	if detail.Movements[0].ActorID != humanID || detail.Movements[2].ActorID != agentID {
		t.Errorf("movement actor_id = %d/%d, want %d/%d",
			detail.Movements[0].ActorID, detail.Movements[2].ActorID, humanID, agentID)
	}
	if detail.Movements[0].Actor.Name == detail.Movements[2].Actor.Name {
		t.Errorf("human and agent names are not distinguishable: %q", detail.Movements[0].Actor.Name)
	}

	if len(detail.Leases) != 1 || detail.Leases[0].Agent == nil {
		t.Fatalf("leases = %+v, want one with agent", detail.Leases)
	}
	requireActor(t, "lease agent", detail.Leases[0].Agent, agentID, "Worker", "agent")
	if detail.Leases[0].AgentID != agentID {
		t.Errorf("lease agent_id = %d, want %d", detail.Leases[0].AgentID, agentID)
	}

	if len(detail.Deliveries) != 1 || detail.Deliveries[0].Actor == nil {
		t.Fatalf("deliveries = %+v, want one with actor", detail.Deliveries)
	}
	requireActor(t, "delivery actor", detail.Deliveries[0].Actor, agentID, "Worker", "agent")
	if detail.Deliveries[0].ActorID != agentID {
		t.Errorf("delivery actor_id = %d, want %d", detail.Deliveries[0].ActorID, agentID)
	}

	if len(detail.Comments) != 2 || detail.Comments[0].Actor == nil || detail.Comments[1].Actor == nil {
		t.Fatalf("comments = %+v, want two with actor", detail.Comments)
	}
	requireActor(t, "human comment actor", detail.Comments[0].Actor, humanID, "Human", "human")
	requireActor(t, "agent comment actor", detail.Comments[1].Actor, agentID, "Worker", "agent")
	if detail.Comments[0].ActorID != humanID || detail.Comments[1].ActorID != agentID {
		t.Errorf("comment actor_id = %d/%d, want %d/%d",
			detail.Comments[0].ActorID, detail.Comments[1].ActorID, humanID, agentID)
	}

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/movements", mutHumanToken, "")
	movements := decodeBody[[]movementBody](t, resp)
	if len(movements) != 4 {
		t.Fatalf("sub-resource movements = %d, want 4", len(movements))
	}
	requireActor(t, "movements list actor", movements[2].Actor, agentID, "Worker", "agent")

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/leases", mutHumanToken, "")
	leases := decodeBody[[]leaseBody](t, resp)
	if len(leases) != 1 {
		t.Fatalf("sub-resource leases = %d, want 1", len(leases))
	}
	requireActor(t, "leases list agent", leases[0].Agent, agentID, "Worker", "agent")

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/deliveries", mutHumanToken, "")
	deliveries := decodeBody[[]deliveryBody](t, resp)
	if len(deliveries) != 1 {
		t.Fatalf("sub-resource deliveries = %d, want 1", len(deliveries))
	}
	requireActor(t, "deliveries list actor", deliveries[0].Actor, agentID, "Worker", "agent")

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/comments", mutHumanToken, "")
	comments := decodeBody[[]commentBody](t, resp)
	if len(comments) != 2 {
		t.Fatalf("sub-resource comments = %d, want 2", len(comments))
	}
	requireActor(t, "comments list human actor", comments[0].Actor, humanID, "Human", "human")
	requireActor(t, "comments list agent actor", comments[1].Actor, agentID, "Worker", "agent")
}

func requireActor(t *testing.T, where string, actor *actorBody, wantID int64, wantName, wantKind string) {
	t.Helper()
	if actor == nil {
		t.Fatalf("%s is missing", where)
	}
	if actor.ID != wantID || actor.Name != wantName || actor.Kind != wantKind {
		t.Fatalf("%s = %+v, want id %d name %q kind %q", where, *actor, wantID, wantName, wantKind)
	}
}

func TestReleaseAllowsTakeover(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Takeover")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	lease := decodeBody[leaseBody](t, resp)

	resp = env.request(t, http.MethodDelete,
		"/api/tasks/"+task.Key+"/leases/"+strconv.FormatInt(lease.ID, 10), mutOtherToken, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("foreign release status = %d, want 409", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodDelete,
		"/api/tasks/"+task.Key+"/leases/"+strconv.FormatInt(lease.ID, 10), mutAgentToken, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("release status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key, mutHumanToken, "")
	detail := decodeBody[detailBody](t, resp)
	if detail.Task.State != "in_progress" {
		t.Errorf("after release state = %q, want in_progress", detail.Task.State)
	}
	if len(detail.Leases) != 1 || detail.Leases[0].ReleasedAt == nil ||
		detail.Leases[0].ClosedReason == nil || *detail.Leases[0].ClosedReason != "released" {
		t.Fatalf("leases after release = %+v", detail.Leases)
	}

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutOtherToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("takeover status = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Task-Version"); got != "4" {
		t.Errorf("takeover X-Task-Version = %q, want 4", got)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/movements", mutHumanToken, "")
	movements := decodeBody[[]movementBody](t, resp)
	last := movements[len(movements)-1]
	if last.Reason == nil || *last.Reason != "takeover" {
		t.Fatalf("last movement = %+v, want takeover reason", last)
	}
	if last.FromState == nil || *last.FromState != "in_progress" || last.ToState != "in_progress" {
		t.Fatalf("takeover movement = %+v, want in_progress -> in_progress", last)
	}
}

func TestHumanDeliveryWithoutLease(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Human delivery")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries", mutHumanToken,
		`{"summary":"Checked by hand","evidence":["manual review"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("human delivery status = %d, want 201", resp.StatusCode)
	}
	result := decodeBody[deliveryResponse](t, resp)
	if result.Delivery.LeaseID != nil {
		t.Fatalf("human lease_id = %v, want null", *result.Delivery.LeaseID)
	}
	if result.Task.State != "review" {
		t.Fatalf("task state = %q, want review", result.Task.State)
	}

	var leaseID sql.NullInt64
	if err := env.db.QueryRow(`SELECT lease_id FROM deliveries WHERE id = ?`,
		result.Delivery.ID).Scan(&leaseID); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if leaseID.Valid {
		t.Fatalf("stored lease_id = %d, want NULL", leaseID.Int64)
	}
}

func TestDeliveryEvidenceValidation(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Evidence")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	twentyOne := `["` + strings.Join(make([]string, 21), `","`) + `"]`
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "empty array", body: `{"summary":"x","evidence":[]}`},
		{name: "blank entry", body: `{"summary":"x","evidence":["   "]}`},
		{name: "twenty one entries", body: `{"summary":"x","evidence":` + twentyOne + `}`},
		{name: "not an array", body: `{"summary":"x","evidence":"proof"}`},
		{name: "missing summary", body: `{"evidence":["proof"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.request(t, http.MethodPost,
				"/api/tasks/"+task.Key+"/deliveries", mutAgentToken, tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			if body := decodeError(t, resp); body.Error != "invalid_input" {
				t.Errorf("error = %q, want invalid_input", body.Error)
			}
		})
	}

	var deliveries int
	if err := env.db.QueryRow(`SELECT count(*) FROM deliveries`).Scan(&deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveries != 0 {
		t.Fatalf("deliveries = %d, want 0", deliveries)
	}
}

func TestCommentsNeedLiveLeaseForAgents(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Comments")

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/comments", mutAgentToken,
		`{"body":"No lease yet"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("agent without lease status = %d, want 409", resp.StatusCode)
	}
	if body := decodeError(t, resp); body.Error != "lease_required" {
		t.Errorf("agent without lease error = %q, want lease_required", body.Error)
	}

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/comments", mutHumanToken,
		`{"body":"Human <b>note</b>"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("human comment status = %d, want 201", resp.StatusCode)
	}
	humanComment := decodeBody[commentBody](t, resp)
	if humanComment.Body != "Human <b>note</b>" || humanComment.ActorID != env.agents[mutHumanToken] {
		t.Fatalf("human comment = %+v", humanComment)
	}

	env.toReady(t, task.Key)
	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/comments", mutAgentToken,
		`{"body":"Agent note"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("agent with lease status = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Task-Version"); got != "3" {
		t.Errorf("comment X-Task-Version = %q, want 3", got)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/comments", mutAgentToken,
		`{"body":"`+strings.Repeat("x", 10001)+`"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized comment status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/comments", mutOtherToken, "")
	comments := decodeBody[[]commentBody](t, resp)
	if len(comments) != 2 || comments[0].Body != "Human <b>note</b>" || comments[1].Body != "Agent note" {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestDetailInternalErrorIsGeneric(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Blob")

	if err := env.db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	resp := env.request(t, http.MethodGet, "/api/tasks/"+task.Key, mutHumanToken, "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	body := decodeError(t, resp)
	if body.Error != "internal" || body.Detail != "internal server error" {
		t.Fatalf("body = %+v, want the generic envelope", body)
	}
	for _, leak := range []string{"select", "SELECT", "sql", "comments"} {
		if strings.Contains(body.Detail, leak) {
			t.Errorf("detail leaks %q: %q", leak, body.Detail)
		}
	}
}
