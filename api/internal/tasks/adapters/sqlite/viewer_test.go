package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"mise/api/internal/tasks/adapters/sqlite"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func viewer() domain.Actor {
	return domain.Actor{ID: 0, Kind: domain.Viewer, Name: "Visitor"}
}

func TestViewerReadsOnlyPublicProjects(t *testing.T) {
	database, _ := openMigrated(t)
	clock := &manualClock{now: testNow}
	service := newService(database, clock)
	repo := sqlite.New(database)

	if _, err := database.Exec(`UPDATE projects SET is_public = 1 WHERE key = 'MI'`); err != nil {
		t.Fatalf("publish MI: %v", err)
	}

	publicOne, _ := createTask(t, service, app.CreateParams{Title: "Public one"})
	clock.now++
	privateOne, _ := createTask(t, service, app.CreateParams{
		ProjectKey: "PO", Title: "Private one",
	})
	clock.now++
	publicTwo, _ := createTask(t, service, app.CreateParams{Title: "Public two"})
	clock.now++
	privateTwo, _ := createTask(t, service, app.CreateParams{
		ProjectKey: "PO", Title: "Private two",
	})
	clock.now++
	publicThree, _ := createTask(t, service, app.CreateParams{Title: "Public three"})

	ctx := context.Background()

	viewerPage, cursor, err := service.List(ctx, viewer(), app.Filter{Limit: 2})
	if err != nil {
		t.Fatalf("viewer list: %v", err)
	}
	assertKeys(t, viewerPage, publicOne.Key, publicTwo.Key)
	if cursor == "" {
		t.Fatal("viewer first page has no cursor")
	}
	secondPage, cursor, err := service.List(ctx, viewer(), app.Filter{Limit: 2, Cursor: cursor})
	if err != nil {
		t.Fatalf("viewer second page: %v", err)
	}
	assertKeys(t, secondPage, publicThree.Key)
	if cursor != "" {
		t.Fatalf("viewer last page cursor = %q, want empty", cursor)
	}

	samePage, _, err := service.List(ctx, viewer(), app.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("viewer full list: %v", err)
	}
	assertKeys(t, samePage, publicOne.Key, publicTwo.Key, publicThree.Key)

	privatePage, _, err := service.List(ctx, viewer(), app.Filter{ProjectKey: "PO"})
	if err != nil {
		t.Fatalf("viewer private project list: %v", err)
	}
	if len(privatePage) != 0 {
		t.Fatalf("viewer private project tasks = %+v, want none", privatePage)
	}

	humanPage, _, err := service.List(ctx, human(), app.Filter{Limit: 2})
	if err != nil {
		t.Fatalf("human list: %v", err)
	}
	assertKeys(t, humanPage, publicOne.Key, privateOne.Key)

	_, privateCursor, err := service.List(ctx, human(), app.Filter{ProjectKey: "PO", Limit: 1})
	if err != nil {
		t.Fatalf("human private cursor: %v", err)
	}
	reused, _, err := service.List(ctx, viewer(), app.Filter{
		ProjectKey: "PO", Limit: 2, Cursor: privateCursor,
	})
	if err != nil {
		t.Fatalf("viewer with private cursor: %v", err)
	}
	if len(reused) != 0 {
		t.Fatalf("viewer with private cursor = %+v, want none", reused)
	}

	board, err := service.Board(ctx, viewer(), "")
	if err != nil {
		t.Fatalf("viewer board: %v", err)
	}
	var visible int
	for _, count := range board.Counts {
		visible += count
	}
	if visible != 3 {
		t.Fatalf("viewer board total = %d, want 3", visible)
	}
	if board.Counts[domain.Backlog] != 3 {
		t.Fatalf("viewer backlog count = %d, want 3", board.Counts[domain.Backlog])
	}
	privateBoard, err := service.Board(ctx, viewer(), "PO")
	if err != nil {
		t.Fatalf("viewer private board: %v", err)
	}
	if sumCounts(privateBoard.Counts) != 0 {
		t.Fatalf("viewer private board counts = %+v, want zero", privateBoard.Counts)
	}
	unknownBoard, err := service.Board(ctx, viewer(), "NOPE")
	if err != nil {
		t.Fatalf("viewer unknown board: %v", err)
	}
	if sumCounts(unknownBoard.Counts) != 0 {
		t.Fatalf("viewer unknown board counts = %+v, want zero", unknownBoard.Counts)
	}
	humanBoard, err := service.Board(ctx, human(), "")
	if err != nil {
		t.Fatalf("human board: %v", err)
	}
	if sumCounts(humanBoard.Counts) != 5 {
		t.Fatalf("human board total = %d, want 5", sumCounts(humanBoard.Counts))
	}

	projects, err := service.ListProjects(ctx, viewer())
	if err != nil {
		t.Fatalf("viewer projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Key != "MI" || projects[0].Cards.Total != 3 {
		t.Fatalf("viewer projects = %+v, want MI with 3 cards", projects)
	}
	allProjects, err := service.ListProjects(ctx, human())
	if err != nil {
		t.Fatalf("human projects: %v", err)
	}
	if len(allProjects) != 3 {
		t.Fatalf("human projects = %d, want 3", len(allProjects))
	}

	if _, err := service.Get(ctx, viewer(), publicOne.Key); err != nil {
		t.Fatalf("viewer get public task: %v", err)
	}
	privateErr := getError(t, service, privateOne.Key)
	unknownErr := getError(t, service, "NOPE-1")
	if privateErr.Code != domain.CodeNotFound || unknownErr.Code != domain.CodeNotFound {
		t.Fatalf("private error = %+v, unknown error = %+v", privateErr, unknownErr)
	}
	if privateErr.Msg != "task not found" || privateErr.Msg != unknownErr.Msg {
		t.Fatalf("private detail = %q, unknown detail = %q", privateErr.Msg, unknownErr.Msg)
	}
	if _, err := service.Get(ctx, human(), privateTwo.Key); err != nil {
		t.Fatalf("human get private task: %v", err)
	}

	if _, err := service.Move(ctx, publicOne.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move public task: %v", err)
	}
	if _, _, err := service.Acquire(ctx, publicOne.Key, agent()); err != nil {
		t.Fatalf("acquire public task: %v", err)
	}
	if _, _, err := service.Deliver(ctx, publicOne.Key, agent(), nil,
		"Public delivery", []string{"proof"}, nil); err != nil {
		t.Fatalf("deliver public task: %v", err)
	}
	if _, err := service.AddComment(ctx, publicOne.Key, human(), "Public comment"); err != nil {
		t.Fatalf("comment public task: %v", err)
	}

	if _, err := service.Move(ctx, privateOne.Key, domain.Ready, "", human(), 1, true); err != nil {
		t.Fatalf("move private task: %v", err)
	}
	if _, _, err := service.Acquire(ctx, privateOne.Key, agent()); err != nil {
		t.Fatalf("acquire private task: %v", err)
	}
	if _, _, err := service.Deliver(ctx, privateOne.Key, agent(), nil,
		"Private delivery", []string{"proof"}, nil); err != nil {
		t.Fatalf("deliver private task: %v", err)
	}
	if _, err := service.AddComment(ctx, privateOne.Key, human(), "Private comment"); err != nil {
		t.Fatalf("comment private task: %v", err)
	}

	detail, err := service.Detail(ctx, viewer(), publicOne.Key)
	if err != nil {
		t.Fatalf("viewer detail public task: %v", err)
	}
	if len(detail.Movements) == 0 || len(detail.Leases) == 0 ||
		len(detail.Deliveries) == 0 || len(detail.Comments) == 0 {
		t.Fatalf("viewer detail = %+v, want every child collection", detail)
	}

	childChecks := []struct {
		name  string
		read  func(string) (int, error)
		calls func(domain.Actor, string) (int, error)
	}{
		{"movements",
			func(key string) (int, error) {
				items, err := repo.ListMovements(ctx, key, app.PublicProjects)
				return len(items), err
			},
			func(actor domain.Actor, key string) (int, error) {
				items, err := service.Movements(ctx, actor, key)
				return len(items), err
			}},
		{"leases",
			func(key string) (int, error) {
				items, err := repo.ListLeases(ctx, key, app.PublicProjects)
				return len(items), err
			},
			func(actor domain.Actor, key string) (int, error) {
				items, err := service.Leases(ctx, actor, key)
				return len(items), err
			}},
		{"deliveries",
			func(key string) (int, error) {
				items, err := repo.ListDeliveries(ctx, key, app.PublicProjects)
				return len(items), err
			},
			func(actor domain.Actor, key string) (int, error) {
				items, err := service.Deliveries(ctx, actor, key)
				return len(items), err
			}},
		{"comments",
			func(key string) (int, error) {
				items, err := repo.ListComments(ctx, key, app.PublicProjects)
				return len(items), err
			},
			func(actor domain.Actor, key string) (int, error) {
				items, err := service.Comments(ctx, actor, key)
				return len(items), err
			}},
	}

	for _, check := range childChecks {
		t.Run(check.name, func(t *testing.T) {
			visible, err := check.read(publicOne.Key)
			if err != nil {
				t.Fatalf("read public %s: %v", check.name, err)
			}
			if visible == 0 {
				t.Fatalf("public %s = 0, want rows", check.name)
			}
			hidden, err := check.read(privateOne.Key)
			if err != nil {
				t.Fatalf("read private %s: %v", check.name, err)
			}
			if hidden != 0 {
				t.Fatalf("private %s = %d, want 0", check.name, hidden)
			}
			if _, err := check.calls(viewer(), privateOne.Key); err == nil {
				t.Fatalf("viewer %s on private task returned no error", check.name)
			}
		})
	}
}

func getError(t *testing.T, service *app.Service, key string) *domain.RuleError {
	t.Helper()
	_, err := service.Get(context.Background(), viewer(), key)
	var rule *domain.RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("get %q error = %v, want *domain.RuleError", key, err)
	}
	return rule
}

func sumCounts(counts map[domain.State]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
