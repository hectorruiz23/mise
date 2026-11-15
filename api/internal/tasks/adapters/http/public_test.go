package http_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type publicBoardBody struct {
	GeneratedAt int64               `json:"generated_at"`
	Projects    []publicProjectBody `json:"projects"`
}

type publicProjectBody struct {
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Order       int     `json:"order"`
	State       string  `json:"state"`
	RepoURL     *string `json:"repo_url"`
	DemoURL     *string `json:"demo_url"`
	Cards       struct {
		Total int `json:"total"`
		Done  int `json:"done"`
	} `json:"cards"`
	LastActive *int64 `json:"last_active"`
}

func setTaskState(t *testing.T, env *tasksEnv, key string, version int64, state string) {
	t.Helper()

	resp := env.request(t, http.MethodPatch, "/api/tasks/"+key,
		fmt.Sprintf(`{"state":%q}`, state), func(req *http.Request) {
			withBearer(req)
			req.Header.Set("If-Match", fmt.Sprintf(`"%d"`, version))
		})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move %s to %s status = %d, want 200", key, state, resp.StatusCode)
	}
}

func publicProjects(t *testing.T, env *tasksEnv) publicBoardBody {
	t.Helper()

	resp := env.request(t, http.MethodGet, "/api/public/board", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("public board status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "max-age=60" {
		t.Fatalf("Cache-Control = %q, want max-age=60", got)
	}
	return decode[publicBoardBody](t, resp)
}

func TestPublicBoardReturnsWhitelistedSnapshot(t *testing.T) {
	env := newTasksEnv(t)

	description := "Public project"
	repoURL := "https://example.com/repo"
	if _, err := env.db.Exec(`
		UPDATE projects
		SET is_public = 1, sort_order = 1, description = ?, repo_url = ?
		WHERE key = 'MI'`, description, repoURL); err != nil {
		t.Fatalf("make MI public: %v", err)
	}

	createTask(t, env, "public-backlog", `{"project":"MI","title":"Backlog"}`)
	ready := createTask(t, env, "public-ready", `{"project":"MI","title":"Ready"}`)
	setTaskState(t, env, ready.Key, ready.Version, "ready")

	board := publicProjects(t, env)
	if board.GeneratedAt <= 0 {
		t.Fatalf("generated_at = %d, want a positive Unix time", board.GeneratedAt)
	}
	if len(board.Projects) != 1 {
		t.Fatalf("projects = %d, want 1 public project", len(board.Projects))
	}

	project := board.Projects[0]
	if project.Key != "MI" || project.Name != "Mise" || project.Order != 1 {
		t.Fatalf("project = %+v, want MI/Mise order 1", project)
	}
	if project.Description == nil || *project.Description != description {
		t.Fatalf("description = %v, want %q", project.Description, description)
	}
	if project.RepoURL == nil || *project.RepoURL != repoURL {
		t.Fatalf("repo_url = %v, want %q", project.RepoURL, repoURL)
	}
	if project.DemoURL != nil {
		t.Fatalf("demo_url = %v, want null", project.DemoURL)
	}
	if project.State != "in_progress" {
		t.Fatalf("state = %q, want in_progress", project.State)
	}
	if project.Cards.Total != 2 || project.Cards.Done != 0 {
		t.Fatalf("cards = %+v, want total 2 cerradas 0", project.Cards)
	}
	if project.LastActive == nil {
		t.Fatal("last_active is null on a project with cards")
	}

	resp := env.request(t, http.MethodGet, "/api/public/board", "", nil)
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read public body: %v", err)
	}
	for _, forbidden := range []string{
		"notes", "acceptance", "deliver", "lease", "comment", "actor", "token", "next_task_number",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("public body exposes %q: %s", forbidden, raw)
		}
	}
}

func TestPublicBoardFiltersAndDerivesState(t *testing.T) {
	env := newTasksEnv(t)

	if len(publicProjects(t, env).Projects) != 0 {
		t.Fatal("a non-public project appeared on the public board")
	}

	if _, err := env.db.Exec(`UPDATE projects SET is_public = 1, sort_order = 1 WHERE key = 'MI'`); err != nil {
		t.Fatalf("make MI public: %v", err)
	}
	if len(publicProjects(t, env).Projects) != 1 {
		t.Fatal("the public project did not appear after is_public = 1")
	}

	if _, err := env.db.Exec(`
		INSERT INTO projects (key, name, next_task_number, created_at, is_public, sort_order)
		VALUES ('NP', 'No cards', 1, 1700000000, 1, 0)`); err != nil {
		t.Fatalf("create NP: %v", err)
	}
	board := publicProjects(t, env)
	if len(board.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(board.Projects))
	}
	noCards := board.Projects[0]
	if noCards.Key != "NP" || noCards.State != "idea" {
		t.Fatalf("first project = %+v, want NP as idea", noCards)
	}
	if noCards.Cards.Total != 0 || noCards.Cards.Done != 0 {
		t.Fatalf("NP cards = %+v, want zero", noCards.Cards)
	}
	if noCards.LastActive != nil {
		t.Fatalf("NP last_active = %v, want null", noCards.LastActive)
	}

	if _, err := env.db.Exec(`UPDATE projects SET public_state = 'done' WHERE key = 'MI'`); err != nil {
		t.Fatalf("override MI state: %v", err)
	}
	board = publicProjects(t, env)
	if board.Projects[1].State != "done" {
		t.Fatalf("MI state = %q, want public_state done", board.Projects[1].State)
	}

	if _, err := env.db.Exec(`UPDATE projects SET is_public = 0 WHERE key = 'MI'`); err != nil {
		t.Fatalf("hide MI: %v", err)
	}
	board = publicProjects(t, env)
	if len(board.Projects) != 1 || board.Projects[0].Key != "NP" {
		t.Fatalf("projects after hiding MI = %+v", board.Projects)
	}
}
