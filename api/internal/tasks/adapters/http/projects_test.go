package http_test

import (
	"context"
	"net/http"
	"testing"

	"mise/api/internal/platform/credential"
	userssqlite "mise/api/internal/users/adapters/sqlite"
	usersdomain "mise/api/internal/users/domain"
)

const projectsAgentToken = "projects-agent-token"

type projectBody struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type apiErrorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

func withAgentBearer(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+projectsAgentToken)
}

func createAgent(t *testing.T, env *tasksEnv) {
	t.Helper()

	repo := userssqlite.New(env.db)
	user, err := usersdomain.NewUser("agent@example.com", "Worker", usersdomain.Agent,
		credential.Hash(projectsAgentToken), 1_700_000_000)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := repo.CreateUser(context.Background(), user,
		credential.Hash(projectsAgentToken)); err != nil {
		t.Fatalf("create agent: %v", err)
	}
}

func TestCreateProject(t *testing.T) {
	env := newTasksEnv(t)

	resp := env.request(t, http.MethodPost, "/api/projects",
		`{"key":"XP","name":"Cross"}`, withBearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want 201", resp.StatusCode)
	}
	created := decode[projectBody](t, resp)
	if created.Key != "XP" || created.Name != "Cross" {
		t.Fatalf("created project = %+v, want XP/Cross", created)
	}

	list := env.request(t, http.MethodGet, "/api/projects", "", withBearer)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("list projects status = %d, want 200", list.StatusCode)
	}
	projects := decode[[]projectBody](t, list)
	found := false
	for _, project := range projects {
		if project.Key == "XP" && project.Name == "Cross" {
			found = true
		}
	}
	if !found {
		t.Fatalf("projects = %+v, want XP/Cross", projects)
	}
}

func TestCreateProjectUppercasesKey(t *testing.T) {
	env := newTasksEnv(t)

	resp := env.request(t, http.MethodPost, "/api/projects",
		`{"key":"xy","name":"Why"}`, withBearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d, want 201", resp.StatusCode)
	}
	created := decode[projectBody](t, resp)
	if created.Key != "XY" || created.Name != "Why" {
		t.Fatalf("created project = %+v, want XY/Why", created)
	}
}

func TestCreateProjectDuplicate(t *testing.T) {
	env := newTasksEnv(t)

	resp := env.request(t, http.MethodPost, "/api/projects",
		`{"key":"MI","name":"Second"}`, withBearer)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate project status = %d, want 409", resp.StatusCode)
	}
	body := decode[apiErrorBody](t, resp)
	if body.Error != "conflict" {
		t.Fatalf("duplicate project error = %q, want conflict", body.Error)
	}
}

func TestCreateProjectInvalidKeys(t *testing.T) {
	env := newTasksEnv(t)

	for _, key := range []string{"", "has space", "1AB", "ABCDEFGHIJKLMNOPQ"} {
		t.Run(key, func(t *testing.T) {
			resp := env.request(t, http.MethodPost, "/api/projects",
				`{"key":"`+key+`","name":"Bad"}`, withBearer)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			body := decode[apiErrorBody](t, resp)
			if body.Error != "invalid_input" {
				t.Fatalf("error = %q, want invalid_input", body.Error)
			}
		})
	}
}

func TestCreateProjectForbiddenForAgent(t *testing.T) {
	env := newTasksEnv(t)
	createAgent(t, env)

	resp := env.request(t, http.MethodPost, "/api/projects",
		`{"key":"AG","name":"Agent"}`, withAgentBearer)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent status = %d, want 403", resp.StatusCode)
	}
	body := decode[apiErrorBody](t, resp)
	if body.Error != "forbidden" {
		t.Fatalf("agent error = %q, want forbidden", body.Error)
	}
}
