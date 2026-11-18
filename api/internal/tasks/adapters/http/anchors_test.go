package http_test

import (
	"fmt"
	"net/http"
	"testing"

	"mise/api/internal/githubinfo"
)

type anchorsResponse struct {
	TaskKey    string                       `json:"task_key"`
	DeliveryID *int64                       `json:"delivery_id"`
	PR         *anchorsPRResponse           `json:"pr"`
	Main       githubinfo.Envelope[*string] `json:"main"`
}

type anchorsPRResponse struct {
	Kind     string                                  `json:"kind"`
	URL      string                                  `json:"url"`
	State    githubinfo.Envelope[string]             `json:"state"`
	MergeSHA githubinfo.Envelope[*string]            `json:"merge_sha"`
	Checks   githubinfo.Envelope[*githubinfo.Checks] `json:"checks"`
}

func moveTask(t *testing.T, env *tasksEnv, key, state string) taskBody {
	t.Helper()
	detail := decode[detailBody](t, env.request(t, http.MethodGet, "/api/tasks/"+key, "", withBearer))
	resp := env.request(t, http.MethodPatch, "/api/tasks/"+key,
		fmt.Sprintf(`{"state":%q}`, state), func(req *http.Request) {
			withBearer(req)
			req.Header.Set("If-Match", fmt.Sprintf(`"%d"`, detail.Task.Version))
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move %s to %s status = %d", key, state, resp.StatusCode)
	}
	return decode[taskBody](t, resp)
}

func deliverPR(t *testing.T, env *tasksEnv, key, prURL string) deliveryBody {
	t.Helper()
	body := fmt.Sprintf(`{"summary":"Handed back","evidence":["go test ./..."],"pr_url":%s}`,
		fmt.Sprintf("%q", prURL))
	resp := env.request(t, http.MethodPost, "/api/tasks/"+key+"/deliveries", body, withBearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delivery status = %d", resp.StatusCode)
	}
	return decode[deliveryResponse](t, resp).Delivery
}

func deliverWithoutPR(t *testing.T, env *tasksEnv, key string) deliveryBody {
	t.Helper()
	body := `{"summary":"Handed back","evidence":["go test ./..."],"pr_url":null}`
	resp := env.request(t, http.MethodPost, "/api/tasks/"+key+"/deliveries", body, withBearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("delivery status = %d", resp.StatusCode)
	}
	return decode[deliveryResponse](t, resp).Delivery
}

func readyForDelivery(t *testing.T, env *tasksEnv, key string) {
	t.Helper()
	moveTask(t, env, key, "ready")
	moveTask(t, env, key, "in_progress")
}

func TestAnchorsReturnsLatestGitHubPullRequest(t *testing.T) {
	env := newTasksEnv(t)
	makeProjectPublic(t, env, "MI")
	task := createTask(t, env, "anchors-github", `{"project":"MI","title":"Anchors"}`)
	readyForDelivery(t, env, task.Key)
	delivery := deliverPR(t, env, task.Key, "https://github.com/hectorruiz23/mise/pull/15")

	resp := env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/anchors", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anchors status = %d, want 200", resp.StatusCode)
	}
	body := decode[anchorsResponse](t, resp)
	if body.TaskKey != task.Key || body.DeliveryID == nil || *body.DeliveryID != delivery.ID {
		t.Fatalf("identity = %q/%v, want %q/%d", body.TaskKey, body.DeliveryID, task.Key, delivery.ID)
	}
	if body.PR == nil || body.PR.Kind != "github" ||
		body.PR.URL != "https://github.com/hectorruiz23/mise/pull/15" {
		t.Fatalf("pr = %+v", body.PR)
	}
	if body.PR.State.Status != githubinfo.StatusFresh || body.PR.State.Data != "open" {
		t.Fatalf("pr state = %+v", body.PR.State)
	}
	if body.PR.Checks.Status != githubinfo.StatusFresh || body.PR.Checks.Data == nil ||
		body.PR.Checks.Data.Result != githubinfo.ResultSuccess {
		t.Fatalf("checks = %+v", body.PR.Checks)
	}
	if body.Main.Status != githubinfo.StatusFresh || body.Main.Data == nil || *body.Main.Data != "main-sha" {
		t.Fatalf("main = %+v", body.Main)
	}
	if env.githubHits.Load() == 0 {
		t.Fatal("github upstream was never queried")
	}
}

func TestAnchorsKeepsTheNewestDeliveryOnATie(t *testing.T) {
	env := newTasksEnv(t)
	task := createTask(t, env, "anchors-latest", `{"project":"MI","title":"Latest"}`)
	readyForDelivery(t, env, task.Key)
	first := deliverPR(t, env, task.Key, "https://example.com/first")
	moveTask(t, env, task.Key, "in_progress")
	second := deliverPR(t, env, task.Key, "https://example.com/second")
	if second.ID <= first.ID {
		t.Fatalf("delivery ids = %d then %d", first.ID, second.ID)
	}

	resp := env.request(t, http.MethodGet, "/api/tasks/"+task.Key+"/anchors", "", withBearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anchors status = %d, want 200", resp.StatusCode)
	}
	body := decode[anchorsResponse](t, resp)
	if body.DeliveryID == nil || *body.DeliveryID != second.ID {
		t.Fatalf("delivery_id = %v, want %d", body.DeliveryID, second.ID)
	}
	if body.PR == nil || body.PR.Kind != "link" || body.PR.URL != "https://example.com/second" {
		t.Fatalf("pr = %+v", body.PR)
	}
}

func TestAnchorsWithoutDeliveryHasNullPR(t *testing.T) {
	env := newTasksEnv(t)
	task := createTask(t, env, "anchors-empty", `{"project":"MI","title":"Empty"}`)

	body := decode[anchorsResponse](t, env.request(t, http.MethodGet,
		"/api/tasks/"+task.Key+"/anchors", "", withBearer))
	if body.DeliveryID != nil {
		t.Fatalf("delivery_id = %v, want null", body.DeliveryID)
	}
	if body.PR != nil {
		t.Fatalf("pr = %+v, want null", body.PR)
	}
	if body.Main.Status != githubinfo.StatusFresh {
		t.Fatalf("main = %+v", body.Main)
	}
}

func TestAnchorsRedactsPathsForTheVisitor(t *testing.T) {
	env := newTasksEnv(t)
	makeProjectPublic(t, env, "MI")
	task := createTask(t, env, "anchors-redact", `{"project":"MI","title":"Redact"}`)
	readyForDelivery(t, env, task.Key)
	deliverPR(t, env, task.Key, "https://example.com/home/alice/private")

	body := decode[anchorsResponse](t, env.request(t, http.MethodGet,
		"/api/tasks/"+task.Key+"/anchors", "", nil))
	if body.PR == nil || body.PR.Kind != "link" {
		t.Fatalf("pr = %+v", body.PR)
	}
	if body.PR.URL != "https://example.com<path>" {
		t.Fatalf("url = %q, want redacted path", body.PR.URL)
	}
	if env.githubHits.Load() != 1 {
		t.Fatalf("github requests = %d, want only the main query", env.githubHits.Load())
	}
}

func TestAnchorsHiddenAndMissingCardsReturn404WithoutUpstream(t *testing.T) {
	env := newTasksEnv(t)
	task := createTask(t, env, "anchors-hidden", `{"project":"MI","title":"Hidden"}`)

	for _, key := range []string{task.Key, "MI-999"} {
		resp := env.request(t, http.MethodGet, "/api/tasks/"+key+"/anchors", "", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s anchors status = %d, want 404", key, resp.StatusCode)
		}
		body := decode[apiErrorBody](t, resp)
		if body.Error != "not_found" {
			t.Fatalf("GET %s anchors error = %q, want not_found", key, body.Error)
		}
	}
	if env.githubHits.Load() != 0 {
		t.Fatalf("github requests = %d, want 0", env.githubHits.Load())
	}
}

func makeProjectPublic(t *testing.T, env *tasksEnv, key string) {
	t.Helper()
	if _, err := env.db.Exec(`UPDATE projects SET is_public = 1 WHERE key = ?`, key); err != nil {
		t.Fatalf("make project %s public: %v", key, err)
	}
}
