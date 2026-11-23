package http_test

import (
	"net/http"
	"testing"
)

func TestScenarioReportIsGuarded(t *testing.T) {
	env := newTasksEnv(t)

	response := env.request(t, http.MethodPost, "/api/plans/1/scenario",
		`{"task_key":"MI-1","head_sha":"1111111111111111111111111111111111111111","command":"make scenario","outcome":"passed"}`,
		env.withCookie)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("scenario from a human = %d, want 403: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = env.request(t, http.MethodPost, "/api/plans/1/scenario", `{`, env.withCookie)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed scenario body = %d, want 400: %s", response.StatusCode, readBody(t, response))
	}
	response.Body.Close()

	response = env.request(t, http.MethodPost, "/api/plans/abc/scenario", `{}`, env.withCookie)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("scenario for a malformed plan id = %d, want 400: %s",
			response.StatusCode, readBody(t, response))
	}
	response.Body.Close()
}
