package http_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type evidenceItemBody struct {
	Label *string `json:"label"`
	Raw   string  `json:"raw"`
}

type structuredDeliveryBody struct {
	ID            int64              `json:"id"`
	TaskKey       string             `json:"task_key"`
	Summary       string             `json:"summary"`
	Evidence      []string           `json:"evidence"`
	EvidenceItems []evidenceItemBody `json:"evidence_items"`
}

type structuredDeliveryResponse struct {
	Delivery structuredDeliveryBody `json:"delivery"`
	Task     taskBody               `json:"task"`
}

type structuredDetailBody struct {
	Deliveries []structuredDeliveryBody `json:"deliveries"`
}

func requireUniformEvidence(t *testing.T, delivery structuredDeliveryBody, want []string) {
	t.Helper()

	if len(delivery.Evidence) != len(delivery.EvidenceItems) || len(delivery.Evidence) != len(want) {
		t.Fatalf("evidence lengths = %d/%d, want %d", len(delivery.Evidence), len(delivery.EvidenceItems), len(want))
	}
	for index, raw := range want {
		if delivery.Evidence[index] != raw || delivery.EvidenceItems[index].Raw != raw {
			t.Fatalf("entry %d = %q/%q, want %q", index, delivery.Evidence[index], delivery.EvidenceItems[index].Raw, raw)
		}
	}
}

func TestEvidenceItemsDeliveryReturnsBothRepresentations(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Structured")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "{}")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries", mutAgentToken,
		`{"summary":"Structured evidence","evidence_items":[{"label":"tests","raw":"go test ./..."},{"label":"build","raw":"make accept"}]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delivery status = %d, want 201", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Task-Version"); got != "4" {
		t.Errorf("X-Task-Version = %q, want 4", got)
	}
	created := decodeBody[structuredDeliveryResponse](t, resp)
	if created.Task.State != "review" {
		t.Errorf("task state = %q, want review", created.Task.State)
	}
	requireUniformEvidence(t, created.Delivery, []string{"go test ./...", "make accept"})
	if created.Delivery.EvidenceItems[0].Label == nil || *created.Delivery.EvidenceItems[0].Label != "tests" {
		t.Errorf("first label = %v, want tests", created.Delivery.EvidenceItems[0].Label)
	}

	var stored string
	if err := env.db.QueryRow(`SELECT evidence FROM deliveries WHERE id = ?`,
		created.Delivery.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored evidence: %v", err)
	}
	var storedItems []evidenceItemBody
	if err := json.Unmarshal([]byte(stored), &storedItems); err != nil {
		t.Fatalf("stored evidence is not an object array: %v (%s)", err, stored)
	}
	if len(storedItems) != 2 || storedItems[0].Label == nil || *storedItems[0].Label != "tests" {
		t.Fatalf("stored items = %+v", storedItems)
	}

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/deliveries", mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deliveries status = %d, want 200", resp.StatusCode)
	}
	list := decodeBody[[]structuredDeliveryBody](t, resp)
	if len(list) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(list))
	}
	requireUniformEvidence(t, list[0], []string{"go test ./...", "make accept"})

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key, mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", resp.StatusCode)
	}
	detail := decodeBody[structuredDetailBody](t, resp)
	if len(detail.Deliveries) != 1 {
		t.Fatalf("detail deliveries = %d, want 1", len(detail.Deliveries))
	}
	requireUniformEvidence(t, detail.Deliveries[0], []string{"go test ./...", "make accept"})
}

func TestEvidenceRepresentationsAreExclusive(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Rejections")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "{}")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	manyItems := func(count int) string {
		entries := make([]string, count)
		for index := range entries {
			entries[index] = fmt.Sprintf(`{"label":"label %d","raw":"raw %d"}`, index, index)
		}
		return "[" + strings.Join(entries, ",") + "]"
	}

	cases := []struct {
		name string
		body string
	}{
		{name: "no representation", body: `{"summary":"x"}`},
		{name: "both representations", body: `{"summary":"x","evidence":["ok"],"evidence_items":[{"label":"l","raw":"r"}]}`},
		{name: "null evidence", body: `{"summary":"x","evidence":null}`},
		{name: "null evidence items", body: `{"summary":"x","evidence_items":null}`},
		{name: "evidence is not an array", body: `{"summary":"x","evidence":"proof"}`},
		{name: "evidence items is not an array", body: `{"summary":"x","evidence_items":"proof"}`},
		{name: "zero items", body: `{"summary":"x","evidence_items":[]}`},
		{name: "twenty one items", body: `{"summary":"x","evidence_items":` + manyItems(21) + `}`},
		{name: "blank raw", body: `{"summary":"x","evidence_items":[{"label":"l","raw":"  "}]}`},
		{name: "raw over the limit", body: `{"summary":"x","evidence_items":[{"label":"l","raw":"` + strings.Repeat("é", 4001) + `"}]}`},
		{name: "blank label", body: `{"summary":"x","evidence_items":[{"label":"  ","raw":"r"}]}`},
		{name: "label over the limit", body: `{"summary":"x","evidence_items":[{"label":"` + strings.Repeat("é", 121) + `","raw":"r"}]}`},
		{name: "null label", body: `{"summary":"x","evidence_items":[{"label":null,"raw":"r"}]}`},
		{name: "null raw", body: `{"summary":"x","evidence_items":[{"label":"l","raw":null}]}`},
		{name: "unknown item field", body: `{"summary":"x","evidence_items":[{"label":"l","raw":"r","extra":1}]}`},
		{name: "missing label", body: `{"summary":"x","evidence_items":[{"raw":"r"}]}`},
		{name: "missing raw", body: `{"summary":"x","evidence_items":[{"label":"l"}]}`},
		{name: "item is not an object", body: `{"summary":"x","evidence_items":["proof"]}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			resp := env.request(t, http.MethodPost,
				"/api/tasks/"+task.Key+"/deliveries", mutAgentToken, tt.body)
			if resp.StatusCode != http.StatusBadRequest {
				body := decodeError(t, resp)
				t.Fatalf("status = %d (%s), want 400", resp.StatusCode, body.Error)
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

func TestLegacyEvidenceReadsAsUnlabelledItems(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Legacy")

	if _, err := env.db.Exec(`
		INSERT INTO deliveries (task_key, lease_id, summary, evidence, created_at, actor_id)
		VALUES (?, NULL, 'Legacy delivery', ?, 1700000000, ?)`,
		task.Key, `["legacy proof","/home/someone/legacy/file"]`, env.agents[mutHumanToken]); err != nil {
		t.Fatalf("insert legacy delivery: %v", err)
	}

	resp := env.request(t, http.MethodGet, "/api/tasks/"+task.Key, mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", resp.StatusCode)
	}
	detail := decodeBody[structuredDetailBody](t, resp)
	if len(detail.Deliveries) != 1 {
		t.Fatalf("detail deliveries = %d, want 1", len(detail.Deliveries))
	}
	legacy := detail.Deliveries[0]
	requireUniformEvidence(t, legacy, []string{"legacy proof", "/home/someone/legacy/file"})
	for index, item := range legacy.EvidenceItems {
		if item.Label != nil {
			t.Errorf("legacy item %d label = %v, want null", index, *item.Label)
		}
	}

	resp = env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/deliveries", mutHumanToken, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deliveries status = %d, want 200", resp.StatusCode)
	}
	list := decodeBody[[]structuredDeliveryBody](t, resp)
	if len(list) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(list))
	}
	requireUniformEvidence(t, list[0], []string{"legacy proof", "/home/someone/legacy/file"})
}

func TestLegacyEvidenceWritesStayStrings(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Legacy write")
	env.toReady(t, task.Key)

	resp := env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/leases", mutAgentToken, "{}")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lease status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.request(t, http.MethodPost, "/api/tasks/"+task.Key+"/deliveries", mutAgentToken,
		`{"summary":"Old contract","evidence":["go test ./...","make verify"]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delivery status = %d, want 201", resp.StatusCode)
	}
	created := decodeBody[structuredDeliveryResponse](t, resp)
	requireUniformEvidence(t, created.Delivery, []string{"go test ./...", "make verify"})
	for index, item := range created.Delivery.EvidenceItems {
		if item.Label != nil {
			t.Errorf("old-contract item %d label = %v, want null", index, *item.Label)
		}
	}

	var stored string
	if err := env.db.QueryRow(`SELECT evidence FROM deliveries WHERE id = ?`,
		created.Delivery.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored evidence: %v", err)
	}
	var stringsOnly []string
	if err := json.Unmarshal([]byte(stored), &stringsOnly); err != nil {
		t.Fatalf("stored evidence is not a string array: %v (%s)", err, stored)
	}
	if len(stringsOnly) != 2 || stringsOnly[0] != "go test ./..." {
		t.Fatalf("stored evidence = %v", stringsOnly)
	}
}

func TestUnreadableEvidenceIsInternalError(t *testing.T) {
	env := newMutationEnv(t)
	task := env.create(t, "Broken evidence")

	if _, err := env.db.Exec(`
		INSERT INTO deliveries (task_key, lease_id, summary, evidence, created_at, actor_id)
		VALUES (?, NULL, 'Broken delivery', ?, 1700000000, ?)`,
		task.Key, `["ok",{"label":5,"raw":"unreadable"}]`, env.agents[mutHumanToken]); err != nil {
		t.Fatalf("insert broken delivery: %v", err)
	}

	for _, path := range []string{"/api/tasks/" + task.Key, "/api/tasks/" + task.Key + "/deliveries"} {
		resp := env.request(t, http.MethodGet, path, mutHumanToken, "")
		if resp.StatusCode != http.StatusInternalServerError {
			resp.Body.Close()
			t.Fatalf("%s status = %d, want 500", path, resp.StatusCode)
		}
		if body := decodeError(t, resp); body.Error != "internal" {
			t.Errorf("%s error = %q, want internal", path, body.Error)
		}
	}
}
