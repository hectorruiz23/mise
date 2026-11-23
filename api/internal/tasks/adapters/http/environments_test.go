package http_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"mise/api/internal/platform/httpserver"
)

func (e *tasksEnv) withCookie(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: httpserver.SessionCookieName, Value: e.cookie})
	req.Header.Set("Origin", testOrigin)
}

func TestProjectEnvironmentsAreRegisteredByTheOwner(t *testing.T) {
	env := newTasksEnv(t)

	response := env.request(t, http.MethodPost, "/api/projects/MI/environments",
		`{"name":"staging","url":"https://staging.example.test/"}`, env.withCookie)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST environments = %d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = env.request(t, http.MethodGet, "/api/projects/MI/environments", "", env.withCookie)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET environments = %d: %s", response.StatusCode, readBody(t, response))
	}
	var environments []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	response.Body.Close()
	if len(environments) != 1 || environments[0].Name != "staging" ||
		environments[0].URL != "https://staging.example.test" {
		t.Fatalf("environments = %+v", environments)
	}

	response = env.request(t, http.MethodPost, "/api/projects/MI/environments",
		`{"name":"staging","url":"https://other.example.test"}`, env.withCookie)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate environment = %d, want 409: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = env.request(t, http.MethodPost, "/api/projects/MI/environments",
		`{"name":"broken","url":"not-a-url"}`, env.withCookie)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed url = %d, want 400: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

func TestEnvironmentsNeedACredential(t *testing.T) {
	env := newTasksEnv(t)

	for _, call := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/projects/MI/environments", ""},
		{http.MethodPost, "/api/projects/MI/environments", `{"name":"staging","url":"https://staging.example.test"}`},
	} {
		response := env.request(t, call.method, call.path, call.body, nil)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s without a credential = %d, want 401",
				call.method, call.path, response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestProjectPatchAcceptsOneSettingAtATime(t *testing.T) {
	env := newTasksEnv(t)

	response := env.request(t, http.MethodPatch, "/api/projects/MI",
		`{"auto_merge":false}`, env.withCookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH auto_merge = %d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	var autoMerge int
	if err := env.db.QueryRow(`SELECT auto_merge FROM projects WHERE key = 'MI'`).Scan(&autoMerge); err != nil {
		t.Fatalf("read auto_merge: %v", err)
	}
	if autoMerge != 0 {
		t.Fatalf("auto_merge = %d, want 0", autoMerge)
	}

	response = env.request(t, http.MethodPatch, "/api/projects/MI",
		`{"auto_dispatch":true}`, env.withCookie)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("PATCH auto_dispatch = %d: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	var enabled, limit int
	if err := env.db.QueryRow(`SELECT auto_dispatch, dispatch_limit FROM projects WHERE key = 'MI'`).
		Scan(&enabled, &limit); err != nil {
		t.Fatalf("read dispatch policy: %v", err)
	}
	if enabled != 1 || limit != 3 {
		t.Fatalf("dispatch policy = %d/%d, want 1/3", enabled, limit)
	}

	response = env.request(t, http.MethodPatch, "/api/projects/MI", `{}`, env.withCookie)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("PATCH without a setting = %d, want 400: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}
