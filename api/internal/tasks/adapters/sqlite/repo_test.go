package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"mise/api/internal/platform/db"
	"mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

const testNow = 1_700_000_000

type manualClock struct {
	now int64
}

func (c *manualClock) Now() int64 { return c.now }

func human() domain.Actor {
	return domain.Actor{ID: 1, Kind: domain.Human, Name: "Human"}
}

func agent() domain.Actor {
	return domain.Actor{ID: 2, Kind: domain.Agent, Name: "Agent"}
}

func otherAgent() domain.Actor {
	return domain.Actor{ID: 3, Kind: domain.Agent, Name: "Other Agent"}
}

func openMigrated(t *testing.T) (*sql.DB, string) {
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

	seedUsers(t, database)
	return database, path
}

func seedUsers(t *testing.T, database *sql.DB) {
	t.Helper()

	const humanHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const agentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const otherAgentHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	if _, err := database.Exec(`
		INSERT INTO users (id, email, name, kind, token_hash, active, created_at)
		VALUES
			(1, 'human@example.com', 'Human', 'human', ?, 1, ?),
			(2, 'agent@example.com', 'Agent', 'agent', ?, 1, ?),
			(3, 'other@example.com', 'Other Agent', 'agent', ?, 1, ?)`,
		humanHash, testNow, agentHash, testNow, otherAgentHash, testNow); err != nil {
		t.Fatalf("seed users: %v", err)
	}
}

func newService(database *sql.DB, clock app.Clock) *app.Service {
	return app.New(sqlite.New(database), clock)
}

func createTask(t *testing.T, service *app.Service, params app.CreateParams) (domain.Task, bool) {
	t.Helper()

	if params.ProjectKey == "" {
		params.ProjectKey = "MI"
	}
	if params.Priority == "" {
		params.Priority = domain.Normal
	}
	if params.Actor.Kind == "" {
		params.Actor = human()
	}

	task, replayed, err := service.Create(context.Background(), params)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task, replayed
}

func wantRuleCode(t *testing.T, err error, want string) {
	t.Helper()

	var rule *domain.RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want *domain.RuleError", err)
	}
	if rule.Code != want {
		t.Fatalf("code = %q, want %q (%v)", rule.Code, want, rule)
	}
}

func TestCreateAllocatesProjectKeys(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	first, replayed := createTask(t, service, app.CreateParams{Title: "First"})
	if replayed {
		t.Fatal("first creation reported a replay")
	}
	if first.Key != "MI-1" {
		t.Fatalf("first key = %q, want MI-1", first.Key)
	}
	if first.State != domain.Backlog || first.Version != 1 {
		t.Fatalf("first = %+v, want backlog version 1", first)
	}
	if first.CreatedAt != testNow || first.UpdatedAt != testNow {
		t.Fatalf("created/updated = %d/%d, want %d", first.CreatedAt, first.UpdatedAt, testNow)
	}
	if first.CreatedBy != human().ID {
		t.Fatalf("created_by = %d, want %d", first.CreatedBy, human().ID)
	}
	if first.ClosedAt != nil {
		t.Fatalf("closed_at = %v, want nil", *first.ClosedAt)
	}

	second, _ := createTask(t, service, app.CreateParams{Title: "Second"})
	if second.Key != "MI-2" {
		t.Fatalf("second key = %q, want MI-2", second.Key)
	}

	var next int
	if err := database.QueryRow(
		`SELECT next_task_number FROM projects WHERE key = 'MI'`).Scan(&next); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if next != 3 {
		t.Fatalf("next_task_number = %d, want 3", next)
	}
}

func TestCreateWritesCreationMovement(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	createTask(t, service, app.CreateParams{Title: "First"})

	var (
		count     int
		fromState sql.NullString
		toState   string
		actorID   int64
		reason    sql.NullString
		version   int64
		at        int64
	)
	if err := database.QueryRow(`
		SELECT count(*), from_state, to_state, actor_id, reason, task_version, at
		FROM task_movements WHERE task_key = 'MI-1'`).
		Scan(&count, &fromState, &toState, &actorID, &reason, &version, &at); err != nil {
		t.Fatalf("read movement: %v", err)
	}

	if count != 1 {
		t.Fatalf("movement count = %d, want 1", count)
	}
	if fromState.Valid {
		t.Fatalf("from_state = %q, want NULL", fromState.String)
	}
	if toState != string(domain.Backlog) {
		t.Fatalf("to_state = %q, want backlog", toState)
	}
	if actorID != human().ID {
		t.Fatalf("actor_id = %d, want %d", actorID, human().ID)
	}
	if reason.Valid {
		t.Fatalf("reason = %q, want NULL", reason.String)
	}
	if version != 1 || at != testNow {
		t.Fatalf("task_version/at = %d/%d, want 1/%d", version, at, testNow)
	}

	var idempotencyKey string
	if err := database.QueryRow(
		`SELECT idempotency_key FROM tasks WHERE key = 'MI-1'`).Scan(&idempotencyKey); err != nil {
		t.Fatalf("read task idempotency key: %v", err)
	}
	if idempotencyKey == "" {
		t.Fatal("tasks.idempotency_key is empty")
	}
}

func TestCreateIdempotencyReplayAndConflict(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	params := app.CreateParams{
		ProjectKey:     "MI",
		Title:          "First",
		Priority:       domain.Normal,
		Actor:          human(),
		IdempotencyKey: "idem-1",
		Digest:         "digest-1",
	}
	first, replayed := createTask(t, service, params)
	if replayed {
		t.Fatal("first creation reported a replay")
	}
	if first.Key != "MI-1" {
		t.Fatalf("first key = %q, want MI-1", first.Key)
	}

	replay, replayed := createTask(t, service, params)
	if !replayed {
		t.Fatal("same key and digest was not reported as a replay")
	}
	if replay.Key != first.Key || replay.Title != first.Title || replay.Version != first.Version {
		t.Fatalf("replay = %+v, want the original %+v", replay, first)
	}

	assertCount(t, database, "tasks", 1)
	assertCount(t, database, "idempotency_records", 1)

	conflict := params
	conflict.Digest = "digest-2"
	_, _, err := service.Create(context.Background(), conflict)
	wantRuleCode(t, err, app.CodeIdempotencyConflict)

	third, _ := createTask(t, service, app.CreateParams{
		Title:          "Third",
		IdempotencyKey: "idem-2",
		Digest:         "digest-3",
	})
	if third.Key != "MI-2" {
		t.Fatalf("third key = %q, want MI-2", third.Key)
	}

	var next int
	if err := database.QueryRow(
		`SELECT next_task_number FROM projects WHERE key = 'MI'`).Scan(&next); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if next != 3 {
		t.Fatalf("next_task_number = %d, want 3", next)
	}
}

func TestCreateUnknownProject(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	_, _, err := service.Create(context.Background(), app.CreateParams{
		ProjectKey: "ZZ",
		Title:      "Nowhere",
		Priority:   domain.Normal,
		Actor:      human(),
	})
	wantRuleCode(t, err, domain.CodeInvalidInput)
}

func TestStaleVersionChangesNothing(t *testing.T) {
	database, _ := openMigrated(t)
	repo := sqlite.New(database)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "First"})

	_, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 99, true)
	wantRuleCode(t, err, domain.CodeStaleVersion)
	assertTaskVersion(t, database, created.Key, 1)
	assertCount(t, database, "task_movements", 1)

	stale := created
	stale.Title = "Changed"
	stale.Version = created.Version + 1
	stale.UpdatedAt = testNow + 1
	movement := domain.Movement{
		TaskKey:     created.Key,
		ToState:     domain.Ready,
		ActorID:     human().ID,
		TaskVersion: stale.Version,
		At:          testNow + 1,
	}
	err = repo.UpdateTask(context.Background(), stale, movement, 99)
	wantRuleCode(t, err, domain.CodeStaleVersion)

	assertTaskTitle(t, database, created.Key, "First")
	assertTaskVersion(t, database, created.Key, 1)
	assertCount(t, database, "task_movements", 1)
}

func TestConcurrentMoveSameIfMatch(t *testing.T) {
	database, path := openMigrated(t)
	database.SetMaxOpenConns(1)

	second, err := db.Open(path)
	if err != nil {
		t.Fatalf("open second database handle: %v", err)
	}
	second.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second database: %v", err)
		}
	})

	firstService := newService(database, &manualClock{now: testNow})
	secondService := newService(second, &manualClock{now: testNow + 1})

	created, _ := createTask(t, firstService, app.CreateParams{Title: "First"})
	ready, err := firstService.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true)
	if err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	if ready.Version != 2 {
		t.Fatalf("ready version = %d, want 2", ready.Version)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, service := range []*app.Service{firstService, secondService} {
		wg.Add(1)
		go func(service *app.Service) {
			defer wg.Done()
			<-start
			_, err := service.Move(
				context.Background(), created.Key, domain.InProgress, "", human(), ready.Version, true)
			results <- err
		}(service)
	}
	close(start)
	wg.Wait()
	close(results)

	var success, stale int
	for err := range results {
		switch {
		case err == nil:
			success++
		case isRuleCode(err, domain.CodeStaleVersion):
			stale++
		default:
			t.Errorf("unexpected move error: %v", err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("success = %d, stale = %d, want 1 and 1", success, stale)
	}

	assertTaskVersion(t, database, created.Key, 3)
	assertCount(t, database, "task_movements", 3)
}

func TestListOrderFilterAndCursor(t *testing.T) {
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)

	createTask(t, service, app.CreateParams{Title: "First"})
	createTask(t, service, app.CreateParams{Title: "Second"})
	clock.now = testNow + 10
	createTask(t, service, app.CreateParams{Title: "Third"})

	if _, err := service.Move(
		context.Background(), "MI-2", domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move second to ready: %v", err)
	}

	firstPage, cursor, err := service.List(context.Background(), app.Filter{
		ProjectKey: "MI", Limit: 2,
	})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	assertKeys(t, firstPage, "MI-1", "MI-2")
	if cursor == "" {
		t.Fatal("first page has no next cursor")
	}

	secondPage, cursor, err := service.List(context.Background(), app.Filter{
		ProjectKey: "MI", Limit: 2, Cursor: cursor,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	assertKeys(t, secondPage, "MI-3")
	if cursor != "" {
		t.Fatalf("last page cursor = %q, want empty", cursor)
	}

	ready, _, err := service.List(context.Background(), app.Filter{
		ProjectKey: "MI", State: domain.Ready,
	})
	if err != nil {
		t.Fatalf("list ready: %v", err)
	}
	assertKeys(t, ready, "MI-2")

	otherProject, _, err := service.List(context.Background(), app.Filter{
		ProjectKey: "PO",
	})
	if err != nil {
		t.Fatalf("list other project: %v", err)
	}
	if len(otherProject) != 0 {
		t.Fatalf("other project tasks = %+v, want none", otherProject)
	}

	_, _, err = service.List(context.Background(), app.Filter{
		ProjectKey: "MI", State: domain.Ready, Limit: 2,
		Cursor: mustCursor(t, service, app.Filter{ProjectKey: "MI", Limit: 2}),
	})
	wantRuleCode(t, err, domain.CodeInvalidInput)

	_, _, err = service.List(context.Background(), app.Filter{
		ProjectKey: "PO", Cursor: mustCursor(t, service, app.Filter{ProjectKey: "MI", Limit: 2}),
	})
	wantRuleCode(t, err, domain.CodeInvalidInput)

	_, _, err = service.List(context.Background(), app.Filter{Cursor: "not a cursor !!"})
	wantRuleCode(t, err, domain.CodeInvalidInput)
}

func mustCursor(t *testing.T, service *app.Service, filter app.Filter) string {
	t.Helper()

	_, cursor, err := service.List(context.Background(), filter)
	if err != nil {
		t.Fatalf("list for cursor: %v", err)
	}
	if cursor == "" {
		t.Fatal("expected a next cursor")
	}
	return cursor
}

func assertKeys(t *testing.T, tasks []domain.Task, want ...string) {
	t.Helper()

	if len(tasks) != len(want) {
		t.Fatalf("task count = %d, want %d: %+v", len(tasks), len(want), tasks)
	}
	for i, key := range want {
		if tasks[i].Key != key {
			t.Fatalf("task %d = %q, want %q (all: %+v)", i, tasks[i].Key, key, tasks)
		}
	}
}

func assertCount(t *testing.T, database *sql.DB, table string, want int) {
	t.Helper()

	var got int
	if err := database.QueryRow(`SELECT count(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func assertTaskVersion(t *testing.T, database *sql.DB, key string, want int64) {
	t.Helper()

	var got int64
	if err := database.QueryRow(
		`SELECT version FROM tasks WHERE key = ?`, key).Scan(&got); err != nil {
		t.Fatalf("read task version: %v", err)
	}
	if got != want {
		t.Fatalf("version = %d, want %d", got, want)
	}
}

func assertTaskTitle(t *testing.T, database *sql.DB, key, want string) {
	t.Helper()

	var got string
	if err := database.QueryRow(
		`SELECT title FROM tasks WHERE key = ?`, key).Scan(&got); err != nil {
		t.Fatalf("read task title: %v", err)
	}
	if got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func isRuleCode(err error, want string) bool {
	var rule *domain.RuleError
	return errors.As(err, &rule) && rule.Code == want
}

func TestAcquireOnReady(t *testing.T) {
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	ready, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true)
	if err != nil {
		t.Fatalf("move to ready: %v", err)
	}

	task, lease, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if task.State != domain.InProgress || task.Version != ready.Version+1 {
		t.Fatalf("task = %+v, want in_progress at version %d", task, ready.Version+1)
	}
	if lease.ID == 0 || lease.AgentID != agent().ID ||
		lease.ExpiresAt != testNow+900 {
		t.Fatalf("lease = %+v, want agent 2 expiring at %d", lease, testNow+900)
	}
	assertCount(t, database, "leases", 1)

	var fromState, toState string
	var reason sql.NullString
	var version int64
	if err := database.QueryRow(`
		SELECT from_state, to_state, reason, task_version
		FROM task_movements WHERE task_key = ? ORDER BY id DESC LIMIT 1`,
		created.Key).Scan(&fromState, &toState, &reason, &version); err != nil {
		t.Fatalf("read acquire movement: %v", err)
	}
	if fromState != string(domain.Ready) || toState != string(domain.InProgress) {
		t.Fatalf("movement = %s -> %s, want ready -> in_progress", fromState, toState)
	}
	if reason.Valid {
		t.Fatalf("movement reason = %q, want NULL", reason.String)
	}
	if version != task.Version {
		t.Fatalf("movement version = %d, want %d", version, task.Version)
	}
}

func TestConcurrentAcquireHasOneWinner(t *testing.T) {
	database, path := openMigrated(t)
	database.SetMaxOpenConns(1)

	second, err := db.Open(path)
	if err != nil {
		t.Fatalf("open second database handle: %v", err)
	}
	second.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second database: %v", err)
		}
	})

	firstService := newService(database, &manualClock{now: testNow})
	secondService := newService(second, &manualClock{now: testNow})

	created, _ := createTask(t, firstService, app.CreateParams{Title: "Ship it"})
	if _, err := firstService.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, contender := range []struct {
		service *app.Service
		actor   domain.Actor
	}{
		{service: firstService, actor: agent()},
		{service: secondService, actor: otherAgent()},
	} {
		wg.Add(1)
		go func(service *app.Service, actor domain.Actor) {
			defer wg.Done()
			<-start
			_, _, err := service.Acquire(context.Background(), created.Key, actor)
			results <- err
		}(contender.service, contender.actor)
	}
	close(start)
	wg.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case isRuleCode(err, domain.CodeLeaseConflict):
			conflict++
		default:
			t.Errorf("unexpected acquire error: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success = %d, conflict = %d, want 1 and 1", success, conflict)
	}

	assertCount(t, database, "leases", 1)
	assertTaskVersion(t, database, created.Key, 3)
}

func TestAcquireTakeoverOfExpiredLease(t *testing.T) {
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, first, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	clock.now = testNow + 901
	task, lease, err := service.Acquire(context.Background(), created.Key, otherAgent())
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if task.State != domain.InProgress || lease.AgentID != otherAgent().ID {
		t.Fatalf("task/lease = %+v / %+v", task, lease)
	}

	var (
		releasedAt sql.NullInt64
		reason     sql.NullString
	)
	if err := database.QueryRow(
		`SELECT released_at, closed_reason FROM leases WHERE id = ?`, first.ID).
		Scan(&releasedAt, &reason); err != nil {
		t.Fatalf("read old lease: %v", err)
	}
	if !releasedAt.Valid || releasedAt.Int64 != clock.now {
		t.Fatalf("released_at = %v, want %d", releasedAt, clock.now)
	}
	if !reason.Valid || reason.String != "expired" {
		t.Fatalf("closed_reason = %v, want expired", reason)
	}

	assertCount(t, database, "leases", 2)
	var fromState, toState string
	var movementReason sql.NullString
	if err := database.QueryRow(`
		SELECT from_state, to_state, reason FROM task_movements
		WHERE task_key = ? ORDER BY id DESC LIMIT 1`, created.Key).
		Scan(&fromState, &toState, &movementReason); err != nil {
		t.Fatalf("read takeover movement: %v", err)
	}
	if fromState != string(domain.InProgress) || toState != string(domain.InProgress) {
		t.Fatalf("movement = %s -> %s, want in_progress -> in_progress", fromState, toState)
	}
	if !movementReason.Valid || movementReason.String != "takeover" {
		t.Fatalf("movement reason = %v, want takeover", movementReason)
	}
}

func TestAcquireAfterRelease(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, first, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := service.Release(context.Background(), created.Key, first.ID, agent()); err != nil {
		t.Fatalf("release: %v", err)
	}

	task, lease, err := service.Acquire(context.Background(), created.Key, otherAgent())
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if task.State != domain.InProgress || lease.AgentID != otherAgent().ID {
		t.Fatalf("task/lease = %+v / %+v", task, lease)
	}

	var reason sql.NullString
	if err := database.QueryRow(
		`SELECT closed_reason FROM leases WHERE id = ?`, first.ID).Scan(&reason); err != nil {
		t.Fatalf("read released lease: %v", err)
	}
	if !reason.Valid || reason.String != "released" {
		t.Fatalf("closed_reason = %v, want released", reason)
	}
	assertCount(t, database, "leases", 2)
}

func TestRenewLease(t *testing.T) {
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, lease, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	_, err = service.Renew(context.Background(), created.Key, lease.ID, otherAgent())
	wantRuleCode(t, err, domain.CodeLeaseConflict)

	clock.now = testNow + 100
	renewed, err := service.Renew(context.Background(), created.Key, lease.ID, agent())
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if renewed.ExpiresAt != clock.now+900 {
		t.Fatalf("expires_at = %d, want %d", renewed.ExpiresAt, clock.now+900)
	}
	assertTaskVersion(t, database, created.Key, 3)
	assertCount(t, database, "task_movements", 3)

	clock.now = testNow + 2000
	_, err = service.Renew(context.Background(), created.Key, lease.ID, agent())
	wantRuleCode(t, err, domain.CodeLeaseConflict)
	assertTaskVersion(t, database, created.Key, 3)
}

func TestReleaseLease(t *testing.T) {
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, lease, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	err = service.Release(context.Background(), created.Key, lease.ID, otherAgent())
	wantRuleCode(t, err, domain.CodeLeaseConflict)

	if err := service.Release(context.Background(), created.Key, lease.ID, agent()); err != nil {
		t.Fatalf("release: %v", err)
	}

	var (
		releasedAt sql.NullInt64
		reason     sql.NullString
	)
	if err := database.QueryRow(
		`SELECT released_at, closed_reason FROM leases WHERE id = ?`, lease.ID).
		Scan(&releasedAt, &reason); err != nil {
		t.Fatalf("read released lease: %v", err)
	}
	if !releasedAt.Valid || releasedAt.Int64 != testNow {
		t.Fatalf("released_at = %v, want %d", releasedAt, testNow)
	}
	if !reason.Valid || reason.String != "released" {
		t.Fatalf("closed_reason = %v, want released", reason)
	}

	task, err := service.Get(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.State != domain.InProgress || task.Version != 3 {
		t.Fatalf("task = %+v, want in_progress at version 3", task)
	}
	assertCount(t, database, "task_movements", 3)

	err = service.Release(context.Background(), created.Key, lease.ID, agent())
	wantRuleCode(t, err, domain.CodeLeaseConflict)
}

func TestDeliverByAgent(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, lease, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	evidence := []string{"go test ./...", "commit abc123"}
	task, delivery, err := service.Deliver(
		context.Background(), created.Key, agent(), &lease.ID, "Ready for review", evidence)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if task.State != domain.Review || task.Version != 4 {
		t.Fatalf("task = %+v, want review at version 4", task)
	}
	if delivery.ID == 0 || delivery.LeaseID == nil || *delivery.LeaseID != lease.ID {
		t.Fatalf("delivery = %+v, want stored with lease %d", delivery, lease.ID)
	}
	if !slices.Equal(delivery.Evidence, evidence) {
		t.Fatalf("evidence = %v, want %v", delivery.Evidence, evidence)
	}

	repo := sqlite.New(database)
	deliveries, err := repo.ListDeliveries(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || !slices.Equal(deliveries[0].Evidence, evidence) {
		t.Fatalf("deliveries = %+v, want one with the same evidence", deliveries)
	}

	var fromState, toState string
	if err := database.QueryRow(`
		SELECT from_state, to_state FROM task_movements
		WHERE task_key = ? ORDER BY id DESC LIMIT 1`, created.Key).
		Scan(&fromState, &toState); err != nil {
		t.Fatalf("read delivery movement: %v", err)
	}
	if fromState != string(domain.InProgress) || toState != string(domain.Review) {
		t.Fatalf("movement = %s -> %s, want in_progress -> review", fromState, toState)
	}

	var released sql.NullInt64
	if err := database.QueryRow(
		`SELECT released_at FROM leases WHERE id = ?`, lease.ID).Scan(&released); err != nil {
		t.Fatalf("read lease: %v", err)
	}
	if released.Valid {
		t.Fatalf("released_at = %d, want NULL", released.Int64)
	}
}

func TestDeliverRejectionsWriteNothing(t *testing.T) {
	t.Run("expired lease", func(t *testing.T) {
		database, _ := openMigrated(t)
		clock := &manualClock{now: testNow}
		service := newService(database, clock)

		created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
		if _, err := service.Move(
			context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
			t.Fatalf("move to ready: %v", err)
		}
		_, lease, err := service.Acquire(context.Background(), created.Key, agent())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}

		clock.now = testNow + 901
		_, _, err = service.Deliver(
			context.Background(), created.Key, agent(), &lease.ID, "Late", []string{"proof"})
		wantRuleCode(t, err, domain.CodeLeaseRequired)

		assertTaskVersion(t, database, created.Key, 3)
		assertCount(t, database, "deliveries", 0)
		assertCount(t, database, "task_movements", 3)
	})

	t.Run("lease of another task", func(t *testing.T) {
		database, _ := openMigrated(t)
		service := newService(database, &manualClock{now: testNow})

		first, _ := createTask(t, service, app.CreateParams{Title: "First"})
		second, _ := createTask(t, service, app.CreateParams{Title: "Second"})
		for _, key := range []string{first.Key, second.Key} {
			if _, err := service.Move(
				context.Background(), key, domain.Ready, "", human(), 1, true); err != nil {
				t.Fatalf("move %s to ready: %v", key, err)
			}
		}
		_, lease, err := service.Acquire(context.Background(), first.Key, agent())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}

		_, _, err = service.Deliver(
			context.Background(), second.Key, agent(), &lease.ID, "Wrong task", []string{"proof"})
		wantRuleCode(t, err, domain.CodeLeaseRequired)

		assertTaskVersion(t, database, second.Key, 2)
		assertCount(t, database, "deliveries", 0)
	})
}

func TestHumanDeliverWithoutLease(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	if _, err := service.Move(
		context.Background(), created.Key, domain.InProgress, "", human(), 2, true); err != nil {
		t.Fatalf("move to in_progress: %v", err)
	}

	task, delivery, err := service.Deliver(
		context.Background(), created.Key, human(), nil, "Done by hand", []string{"manual check"})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if task.State != domain.Review || task.Version != 4 {
		t.Fatalf("task = %+v, want review at version 4", task)
	}
	if delivery.LeaseID != nil {
		t.Fatalf("lease_id = %v, want NULL", *delivery.LeaseID)
	}
	assertCount(t, database, "leases", 0)

	var leaseID sql.NullInt64
	if err := database.QueryRow(
		`SELECT lease_id FROM deliveries WHERE id = ?`, delivery.ID).Scan(&leaseID); err != nil {
		t.Fatalf("read delivery lease: %v", err)
	}
	if leaseID.Valid {
		t.Fatalf("stored lease_id = %d, want NULL", leaseID.Int64)
	}
}

func TestMoveToReadyAndDoneClosesLease(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, lease, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	returned, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 3, true)
	if err != nil {
		t.Fatalf("move back to ready: %v", err)
	}
	if returned.State != domain.Ready || returned.Version != 4 {
		t.Fatalf("task = %+v, want ready at version 4", returned)
	}
	assertLeaseClosed(t, database, lease.ID, "returned")

	if _, err := service.Move(
		context.Background(), created.Key, domain.InProgress, "", human(), 4, true); err != nil {
		t.Fatalf("move to in_progress: %v", err)
	}
	_, second, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if _, _, err := service.Deliver(
		context.Background(), created.Key, agent(), &second.ID, "Ready", []string{"proof"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	done, err := service.Move(
		context.Background(), created.Key, domain.Done, "", human(), 7, true)
	if err != nil {
		t.Fatalf("move to done: %v", err)
	}
	if done.State != domain.Done || done.ClosedAt == nil {
		t.Fatalf("task = %+v, want done with closed_at", done)
	}
	assertLeaseClosed(t, database, second.ID, "done")
}

func TestListLeasesAndDeliveriesInInsertionOrder(t *testing.T) {
	database, _ := openMigrated(t)
	service := newService(database, &manualClock{now: testNow})

	created, _ := createTask(t, service, app.CreateParams{Title: "Ship it"})
	if _, err := service.Move(
		context.Background(), created.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move to ready: %v", err)
	}
	_, first, err := service.Acquire(context.Background(), created.Key, agent())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if err := service.Release(context.Background(), created.Key, first.ID, agent()); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, second, err := service.Acquire(context.Background(), created.Key, otherAgent())
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}

	repo := sqlite.New(database)
	leases, err := repo.ListLeases(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("list leases: %v", err)
	}
	if len(leases) != 2 || leases[0].ID != first.ID || leases[1].ID != second.ID {
		t.Fatalf("leases = %+v, want ids %d then %d", leases, first.ID, second.ID)
	}

	if _, _, err := service.Deliver(
		context.Background(), created.Key, otherAgent(), &second.ID, "First", []string{"one"}); err != nil {
		t.Fatalf("first deliver: %v", err)
	}
	if _, err := service.Move(
		context.Background(), created.Key, domain.InProgress, "", human(), 5, true); err != nil {
		t.Fatalf("move back to in_progress: %v", err)
	}
	if _, _, err := service.Deliver(
		context.Background(), created.Key, human(), nil, "Second", []string{"two"}); err != nil {
		t.Fatalf("second deliver: %v", err)
	}

	deliveries, err := repo.ListDeliveries(context.Background(), created.Key)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 2 ||
		deliveries[0].Summary != "First" || deliveries[1].Summary != "Second" {
		t.Fatalf("deliveries = %+v, want First then Second", deliveries)
	}
}

func assertLeaseClosed(t *testing.T, database *sql.DB, leaseID int64, wantReason string) {
	t.Helper()

	var (
		releasedAt sql.NullInt64
		reason     sql.NullString
	)
	if err := database.QueryRow(
		`SELECT released_at, closed_reason FROM leases WHERE id = ?`, leaseID).
		Scan(&releasedAt, &reason); err != nil {
		t.Fatalf("read lease %d: %v", leaseID, err)
	}
	if !releasedAt.Valid {
		t.Fatalf("lease %d is still unreleased", leaseID)
	}
	if !reason.Valid || reason.String != wantReason {
		t.Fatalf("lease %d closed_reason = %v, want %s", leaseID, reason, wantReason)
	}
}
