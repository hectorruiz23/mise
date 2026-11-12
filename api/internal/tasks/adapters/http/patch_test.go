package http_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func (e *mutationEnv) patchTask(t *testing.T, key, token string, version int64, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPatch, e.http.URL+"/api/tasks/"+key,
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("If-Match", `"`+strconv.FormatInt(version, 10)+`"`)
	resp, err := e.http.Client().Do(req)
	if err != nil {
		t.Fatalf("patch %s: %v", key, err)
	}
	return resp
}

func (e *mutationEnv) lease(t *testing.T, key, token string) leaseBody {
	t.Helper()

	resp := e.request(t, http.MethodPost, "/api/tasks/"+key+"/leases", token, "")
	if resp.StatusCode != http.StatusCreated {
		body := decodeError(t, resp)
		t.Fatalf("lease status = %d (%s), want 201", resp.StatusCode, body.Error)
	}
	return decodeBody[leaseBody](t, resp)
}

func (e *mutationEnv) detail(t *testing.T, key, token string) detailBody {
	t.Helper()

	resp := e.request(t, http.MethodGet, "/api/tasks/"+key, token, "")
	if resp.StatusCode != http.StatusOK {
		body := decodeError(t, resp)
		t.Fatalf("detail status = %d (%s), want 200", resp.StatusCode, body.Error)
	}
	return decodeBody[detailBody](t, resp)
}

func TestAgentPatchStateWithLiveLease(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Agent blocks")
	ready := env.toReady(t, task.Key)
	lease := env.lease(t, ready.Key, mutAgentToken)
	if lease.AgentID != env.agents[mutAgentToken] {
		t.Fatalf("lease agent = %d, want %d", lease.AgentID, env.agents[mutAgentToken])
	}

	const reason = "waiting for the design decision"
	resp := env.patchTask(t, ready.Key, mutAgentToken, 3,
		`{"state":"blocked","reason":"`+reason+`"}`)
	if resp.StatusCode != http.StatusOK {
		body := decodeError(t, resp)
		t.Fatalf("patch status = %d (%s), want 200", resp.StatusCode, body.Error)
	}
	if got := resp.Header.Get("X-Task-Version"); got != "4" {
		t.Errorf("X-Task-Version = %q, want 4", got)
	}
	blocked := decodeTask(t, resp)
	if blocked.State != "blocked" || blocked.Version != 4 {
		t.Fatalf("patched task = %+v, want blocked at version 4", blocked)
	}

	detail := env.detail(t, ready.Key, mutHumanToken)
	last := detail.Movements[len(detail.Movements)-1]
	if last.ToState != "blocked" || last.ActorID != env.agents[mutAgentToken] {
		t.Fatalf("last movement = %+v, want blocked by agent %d", last, env.agents[mutAgentToken])
	}
	if last.Reason == nil || *last.Reason != reason {
		t.Fatalf("last movement reason = %v, want %q", last.Reason, reason)
	}
}

func TestAgentPatchStateWithoutLiveLease(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "No lease")
	ready := env.toReady(t, task.Key)

	resp := env.patchTask(t, ready.Key, mutHumanToken, 2, `{"state":"in_progress"}`)
	if resp.StatusCode != http.StatusOK {
		body := decodeError(t, resp)
		t.Fatalf("human move status = %d (%s), want 200", resp.StatusCode, body.Error)
	}
	resp.Body.Close()

	resp = env.patchTask(t, ready.Key, mutAgentToken, 3,
		`{"state":"blocked","reason":"waiting for the design decision"}`)
	if resp.StatusCode != http.StatusConflict {
		body := decodeError(t, resp)
		t.Fatalf("patch status = %d (%s), want 409", resp.StatusCode, body.Error)
	}
	if body := decodeError(t, resp); body.Error != "lease_required" {
		t.Errorf("error = %q, want lease_required", body.Error)
	}

	detail := env.detail(t, ready.Key, mutHumanToken)
	if detail.Task.State != "in_progress" || detail.Task.Version != 3 {
		t.Fatalf("task = %+v, want in_progress at version 3", detail.Task)
	}
}

func TestAgentPatchFieldsForbidden(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "field only", body: `{"title":"x"}`},
		{name: "fields and state", body: `{"title":"x","state":"blocked","reason":"y"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newMutationEnv(t)
			task := env.create(t, "Agent fields")
			ready := env.toReady(t, task.Key)
			env.lease(t, ready.Key, mutAgentToken)

			resp := env.patchTask(t, ready.Key, mutAgentToken, 3, tc.body)
			if resp.StatusCode != http.StatusForbidden {
				body := decodeError(t, resp)
				t.Fatalf("patch status = %d (%s), want 403", resp.StatusCode, body.Error)
			}
			if body := decodeError(t, resp); body.Error != "forbidden" {
				t.Errorf("error = %q, want forbidden", body.Error)
			}

			detail := env.detail(t, ready.Key, mutHumanToken)
			if detail.Task.Title != "Agent fields" {
				t.Errorf("title = %q, want unchanged", detail.Task.Title)
			}
			if detail.Task.State != "in_progress" || detail.Task.Version != 3 {
				t.Errorf("task = %+v, want in_progress at version 3", detail.Task)
			}
		})
	}
}

func TestHumanPatchShapesWithoutLease(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		title string
		state string
	}{
		{name: "field only", body: `{"title":"Renamed"}`, title: "Renamed", state: "backlog"},
		{name: "state only", body: `{"state":"ready"}`, title: "Human shapes", state: "ready"},
		{name: "fields and state", body: `{"title":"Renamed","state":"ready"}`, title: "Renamed", state: "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newMutationEnv(t)
			task := env.create(t, "Human shapes")

			resp := env.patchTask(t, task.Key, mutHumanToken, 1, tc.body)
			if resp.StatusCode != http.StatusOK {
				body := decodeError(t, resp)
				t.Fatalf("patch status = %d (%s), want 200", resp.StatusCode, body.Error)
			}
			patched := decodeTask(t, resp)
			if patched.Title != tc.title || patched.State != tc.state || patched.Version != 2 {
				t.Fatalf("patched task = %+v, want title %q state %q version 2",
					patched, tc.title, tc.state)
			}
		})
	}
}
