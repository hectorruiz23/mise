package http_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func deliverableTask(t *testing.T, env *mutationEnv, title string) taskBody {
	t.Helper()
	task := env.create(t, title)
	env.toReady(t, task.Key)
	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d (%s), want 201", resp.StatusCode, decodeError(t, resp).Detail)
	}
	resp.Body.Close()
	return task
}

func TestDeliveryPRURLIsStoredAndReadBack(t *testing.T) {
	env := newMutationEnv(t)
	task := deliverableTask(t, env, "PR link")

	const prURL = "https://github.com/hector/mise/pull/42"
	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries", mutAgentToken,
		`{"summary":"With a PR","evidence":["go test ./..."],"pr_url":"`+prURL+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delivery status = %d (%s), want 201", resp.StatusCode, decodeError(t, resp).Detail)
	}
	delivered := decodeBody[deliveryResponse](t, resp)
	if delivered.Delivery.PRURL == nil || *delivered.Delivery.PRURL != prURL {
		t.Fatalf("delivery pr_url = %v, want %q", delivered.Delivery.PRURL, prURL)
	}

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/deliveries", mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", resp.StatusCode)
	}
	listed := decodeBody[[]deliveryBody](t, resp)
	if len(listed) != 1 || listed[0].PRURL == nil || *listed[0].PRURL != prURL {
		t.Fatalf("listed deliveries = %+v, want one with pr_url %q", listed, prURL)
	}

	detail := decodeBody[detailBody](t, env.request(t, http.MethodGet,
		"/api/tasks/"+task.Key, mutHumanToken, ""))
	if len(detail.Deliveries) != 1 || detail.Deliveries[0].PRURL == nil ||
		*detail.Deliveries[0].PRURL != prURL {
		t.Fatalf("detail deliveries = %+v, want one with pr_url %q", detail.Deliveries, prURL)
	}

	var stored sql.NullString
	if err := env.db.QueryRow(`SELECT pr_url FROM deliveries WHERE id = ?`,
		delivered.Delivery.ID).Scan(&stored); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	if !stored.Valid || stored.String != prURL {
		t.Fatalf("stored pr_url = %v, want %q", stored, prURL)
	}
}

func TestDeliveryPRURLValidation(t *testing.T) {
	env := newMutationEnv(t)
	task := deliverableTask(t, env, "Bad PR link")

	for _, tt := range []struct {
		name  string
		value string
	}{
		{name: "no scheme", value: "github.com/hector/mise/pull/42"},
		{name: "other scheme", value: "ftp://example.com/pull/42"},
		{name: "javascript scheme", value: "javascript:alert(1)"},
		{name: "no host", value: "https:///pull/42"},
		{name: "too long", value: "https://example.com/" + strings.Repeat("a", 500)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"summary":  "With a bad link",
				"evidence": []string{"proof"},
				"pr_url":   tt.value,
			})
			if err != nil {
				t.Fatalf("encode body: %v", err)
			}
			resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries",
				mutAgentToken, string(payload))
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			body := decodeError(t, resp)
			if body.Error != "invalid_input" {
				t.Errorf("error = %q, want invalid_input", body.Error)
			}
			if !strings.Contains(body.Detail, "http(s)") {
				t.Errorf("detail = %q, want it to name the expected URL", body.Detail)
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

func TestDeliveryWithoutPRURL(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "absent", body: `{"summary":"No PR","evidence":["analysis only"]}`},
		{name: "blank", body: `{"summary":"No PR","evidence":["analysis only"],"pr_url":"  "}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := newMutationEnv(t)
			task := deliverableTask(t, env, "No PR")

			resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries",
				mutAgentToken, tt.body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("status = %d (%s), want 201", resp.StatusCode, decodeError(t, resp).Detail)
			}
			delivered := decodeBody[deliveryResponse](t, resp)
			if delivered.Delivery.PRURL != nil {
				t.Fatalf("delivery pr_url = %q, want null", *delivered.Delivery.PRURL)
			}
			if delivered.Task.State != "review" {
				t.Fatalf("task state = %q, want review", delivered.Task.State)
			}

			var stored sql.NullString
			if err := env.db.QueryRow(`SELECT pr_url FROM deliveries WHERE id = ?`,
				delivered.Delivery.ID).Scan(&stored); err != nil {
				t.Fatalf("read delivery: %v", err)
			}
			if stored.Valid {
				t.Fatalf("stored pr_url = %q, want NULL", stored.String)
			}
		})
	}
}
