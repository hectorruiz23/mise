package http_test

import (
	"net/http"
	"strings"
	"testing"

	"mise/api/internal/platform/httpserver"
)

type redactedDetailBody struct {
	Task struct {
		Key   string  `json:"key"`
		Notes *string `json:"notes"`
	} `json:"task"`
}

const privateNotes = "el repo vive en /home/hector/Projects/mise y el token en ~/.config/mise/token"

func readDetail(t *testing.T, env *tasksEnv, key string, prepare func(*http.Request)) redactedDetailBody {
	t.Helper()

	resp := env.request(t, http.MethodGet, "/api/tasks/"+key, "", prepare)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail %s status = %d, want 200", key, resp.StatusCode)
	}
	return decode[redactedDetailBody](t, resp)
}

func TestVisitorReadsRedactedPaths(t *testing.T) {
	env := newTasksEnv(t)

	created := createTask(t, env, "redact-visitor",
		`{"project":"MI","title":"Rutas","notes":"`+privateNotes+`"}`)

	if _, err := env.db.Exec(`UPDATE projects SET is_public = 1 WHERE key = 'MI'`); err != nil {
		t.Fatalf("make MI public: %v", err)
	}

	visitor := readDetail(t, env, created.Key, nil)
	if visitor.Task.Notes == nil {
		t.Fatal("visitor response without notes")
	}
	if strings.Contains(*visitor.Task.Notes, "/home/hector") {
		t.Fatalf("visitor still sees the home path: %q", *visitor.Task.Notes)
	}
	if strings.Contains(*visitor.Task.Notes, "~/.config") {
		t.Fatalf("visitor still sees the tilde path: %q", *visitor.Task.Notes)
	}
	if !strings.Contains(*visitor.Task.Notes, "<path>") {
		t.Fatalf("visitor notes carry no redaction mark: %q", *visitor.Task.Notes)
	}

	owner := readDetail(t, env, created.Key, func(req *http.Request) {
		req.AddCookie(&http.Cookie{Name: httpserver.SessionCookieName, Value: env.cookie})
	})
	if owner.Task.Notes == nil || *owner.Task.Notes != privateNotes {
		t.Fatalf("owner notes = %v, want the original text", owner.Task.Notes)
	}
}

func TestVisitorDeliveryHidesPathsInEvidenceItems(t *testing.T) {
	env := newTasksEnv(t)

	created := createTask(t, env, "redact-items", `{"project":"MI","title":"Evidencia"}`)
	for _, step := range []struct {
		version string
		state   string
	}{
		{version: "1", state: "ready"},
		{version: "2", state: "in_progress"},
	} {
		resp := env.request(t, http.MethodPatch, "/api/tasks/"+created.Key,
			`{"state":"`+step.state+`"}`, func(req *http.Request) {
				withBearer(req)
				req.Header.Set("If-Match", `"`+step.version+`"`)
			})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("move to %s status = %d, want 200", step.state, resp.StatusCode)
		}
		resp.Body.Close()
	}

	resp := env.request(t, http.MethodPost, "/api/tasks/"+created.Key+"/deliveries",
		`{"summary":"hecho en /home/hector/tmp","evidence_items":[{"label":"ruta /home/hector/Projects/mise","raw":"`+"`cd ~/Projects/mise`"+`"}]}`,
		withBearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("structured delivery status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	if _, err := env.db.Exec(`UPDATE projects SET is_public = 1 WHERE key = 'MI'`); err != nil {
		t.Fatalf("make MI public: %v", err)
	}

	visitor := env.request(t, http.MethodGet, "/api/tasks/"+created.Key, "", nil)
	if visitor.StatusCode != http.StatusOK {
		t.Fatalf("visitor detail status = %d, want 200", visitor.StatusCode)
	}
	detail := decode[struct {
		Deliveries []struct {
			Evidence      []string `json:"evidence"`
			EvidenceItems []struct {
				Label *string `json:"label"`
				Raw   string  `json:"raw"`
			} `json:"evidence_items"`
		} `json:"deliveries"`
	}](t, visitor)
	if len(detail.Deliveries) != 1 || len(detail.Deliveries[0].EvidenceItems) != 1 {
		t.Fatalf("visitor deliveries = %+v", detail.Deliveries)
	}
	item := detail.Deliveries[0].EvidenceItems[0]
	if item.Label == nil || strings.Contains(*item.Label, "/home/hector") {
		t.Fatalf("visitor still sees the label path: %v", item.Label)
	}
	if strings.Contains(item.Raw, "~/Projects") {
		t.Fatalf("visitor still sees the raw path: %q", item.Raw)
	}
	if detail.Deliveries[0].Evidence[0] != item.Raw {
		t.Fatalf("representations diverged: %q vs %q", detail.Deliveries[0].Evidence[0], item.Raw)
	}
}

func TestVisitorBoardAndListHidePaths(t *testing.T) {
	env := newTasksEnv(t)

	created := createTask(t, env, "redact-board",
		`{"project":"MI","title":"Rutas","notes":"`+privateNotes+`"}`)
	if _, err := env.db.Exec(`UPDATE projects SET is_public = 1 WHERE key = 'MI'`); err != nil {
		t.Fatalf("make MI public: %v", err)
	}

	board := env.request(t, http.MethodGet, "/api/board", "", nil)
	if board.StatusCode != http.StatusOK {
		t.Fatalf("visitor board status = %d, want 200", board.StatusCode)
	}
	boardBody := decode[struct {
		Columns map[string][]struct {
			Key   string  `json:"key"`
			Notes *string `json:"notes"`
		} `json:"columns"`
	}](t, board)

	found := false
	for _, cards := range boardBody.Columns {
		for _, card := range cards {
			if card.Key != created.Key {
				continue
			}
			found = true
			if card.Notes != nil && strings.Contains(*card.Notes, "/home/hector") {
				t.Fatalf("board card still carries the home path: %q", *card.Notes)
			}
		}
	}
	if !found {
		t.Fatalf("card %s did not appear on the visitor board", created.Key)
	}

	list := env.request(t, http.MethodGet, "/api/tasks", "", nil)
	if list.StatusCode != http.StatusOK {
		t.Fatalf("visitor list status = %d, want 200", list.StatusCode)
	}
	listBody := decode[struct {
		Tasks []struct {
			Key   string  `json:"key"`
			Notes *string `json:"notes"`
		} `json:"tasks"`
	}](t, list)

	found = false
	for _, task := range listBody.Tasks {
		if task.Key != created.Key {
			continue
		}
		found = true
		if task.Notes != nil && strings.Contains(*task.Notes, "/home/hector") {
			t.Fatalf("listed card still carries the home path: %q", *task.Notes)
		}
	}
	if !found {
		t.Fatalf("card %s did not appear in the visitor list", created.Key)
	}
}
