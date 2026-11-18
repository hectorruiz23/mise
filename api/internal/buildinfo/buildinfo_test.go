package buildinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withBuildInfo(t *testing.T, revisionValue, treeStateValue, repositoryValue string) {
	t.Helper()
	previousRevision, previousTreeState, previousRepository := revision, treeState, repository
	t.Cleanup(func() {
		revision, treeState, repository = previousRevision, previousTreeState, previousRepository
	})
	revision = revisionValue
	treeState = treeStateValue
	repository = repositoryValue
}

func serveBuildInfo(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	Handler()(recorder, httptest.NewRequest(http.MethodGet, "/api/build", nil))
	return recorder
}

func TestHandlerServesInjectedBuildInfo(t *testing.T) {
	withBuildInfo(t, "abc123", "clean", "github.com/hectorruiz23/mise")

	recorder := serveBuildInfo(t)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var info Info
	if err := json.NewDecoder(recorder.Body).Decode(&info); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if info.Repository == nil || *info.Repository != "github.com/hectorruiz23/mise" {
		t.Errorf("repository = %v, want github.com/hectorruiz23/mise", info.Repository)
	}
	if info.Revision == nil || *info.Revision != "abc123" {
		t.Errorf("revision = %v, want abc123", info.Revision)
	}
	if info.TreeState != "clean" {
		t.Errorf("tree_state = %q, want clean", info.TreeState)
	}
}

func TestHandlerReportsUnknownTreeStateAndNullsWithoutInjection(t *testing.T) {
	withBuildInfo(t, "", "", "")

	recorder := serveBuildInfo(t)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var raw map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if raw["repository"] != nil {
		t.Errorf("repository = %v, want null", raw["repository"])
	}
	if raw["revision"] != nil {
		t.Errorf("revision = %v, want null", raw["revision"])
	}
	if raw["tree_state"] != "unknown" {
		t.Errorf("tree_state = %v, want unknown", raw["tree_state"])
	}
}
