package http_test

import (
	"fmt"
	"net/http"
	"testing"
)

type listedDraft struct {
	ID      int64  `json:"id"`
	Project string `json:"project"`
	Version int64  `json:"version"`
	State   string `json:"state"`
	PlanID  *int64 `json:"plan_id"`
}

type listedPlan struct {
	ID      int64  `json:"id"`
	Project string `json:"project"`
}

func createDraft(t *testing.T, env *tasksEnv, project string) listedDraft {
	t.Helper()
	body := fmt.Sprintf(`{"project":%q,"outcome":"List the proposals","scenario":"make accept",
		"tasks":[{"key":"T1","title":"First"},{"key":"T2","title":"Second"}]}`, project)
	resp := env.request(t, http.MethodPost, "/api/plan-drafts", body, withBearer)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create draft status = %d, want 201", resp.StatusCode)
	}
	return decode[listedDraft](t, resp)
}

func approveDraft(t *testing.T, env *tasksEnv, id int64) listedDraft {
	t.Helper()
	resp := env.request(t, http.MethodPost, fmt.Sprintf("/api/plan-drafts/%d/approve", id), "",
		func(req *http.Request) {
			withBearer(req)
			req.Header.Set("If-Match", `"1"`)
		})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve draft status = %d, want 200", resp.StatusCode)
	}
	return decode[listedDraft](t, resp)
}

func TestPlanDraftListIsFilteredAndNewestFirst(t *testing.T) {
	env := newTasksEnv(t)
	first := createDraft(t, env, "MI")
	other := createDraft(t, env, "PO")
	last := createDraft(t, env, "MI")
	approved := approveDraft(t, env, first.ID)
	if approved.PlanID == nil {
		t.Fatal("the approved proposal has no plan")
	}

	resp := env.request(t, http.MethodGet, "/api/plan-drafts?project=MI", "", withBearer)
	drafts := decode[[]listedDraft](t, resp)
	if len(drafts) != 2 {
		t.Fatalf("MI drafts = %+v, want two", drafts)
	}
	if drafts[0].ID != last.ID || drafts[1].ID != first.ID {
		t.Fatalf("MI drafts = %+v, want newest first %d then %d", drafts, last.ID, first.ID)
	}
	if drafts[0].Project != "MI" || drafts[0].Version != 1 || drafts[0].State != "draft" ||
		drafts[0].PlanID != nil {
		t.Fatalf("newest MI draft = %+v, want draft v1 without a plan", drafts[0])
	}
	if drafts[1].State != "approved" || drafts[1].PlanID == nil || *drafts[1].PlanID != *approved.PlanID {
		t.Fatalf("approved MI draft = %+v, want plan %d", drafts[1], *approved.PlanID)
	}
	for _, draft := range drafts {
		if draft.ID == other.ID || draft.Project != "MI" {
			t.Fatalf("MI drafts leaked a foreign proposal: %+v", draft)
		}
	}

	all := env.request(t, http.MethodGet, "/api/plan-drafts", "", withBearer)
	if got := decode[[]listedDraft](t, all); len(got) != 3 {
		t.Fatalf("all drafts = %+v, want three", got)
	}
}

func TestPlanListIsFilteredByProject(t *testing.T) {
	env := newTasksEnv(t)
	miDraft := createDraft(t, env, "MI")
	poDraft := createDraft(t, env, "PO")
	miPlan := approveDraft(t, env, miDraft.ID)
	poPlan := approveDraft(t, env, poDraft.ID)
	if miPlan.PlanID == nil || poPlan.PlanID == nil {
		t.Fatal("an approved proposal has no plan")
	}

	resp := env.request(t, http.MethodGet, "/api/plans?project=MI", "", withBearer)
	plans := decode[[]listedPlan](t, resp)
	if len(plans) != 1 {
		t.Fatalf("MI plans = %+v, want one", plans)
	}
	if plans[0].ID != *miPlan.PlanID || plans[0].Project != "MI" {
		t.Fatalf("MI plan = %+v, want plan %d of MI", plans[0], *miPlan.PlanID)
	}

	all := env.request(t, http.MethodGet, "/api/plans", "", withBearer)
	if got := decode[[]listedPlan](t, all); len(got) != 2 || got[0].ID != *poPlan.PlanID {
		t.Fatalf("all plans = %+v, want the two newest first", got)
	}
}

func TestPlanListsRefuseTheViewer(t *testing.T) {
	env := newTasksEnv(t)
	for _, path := range []string{"/api/plan-drafts?project=MI", "/api/plans?project=MI"} {
		resp := env.request(t, http.MethodGet, path, "", nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want 403", path, resp.StatusCode)
		}
		body := decode[apiErrorBody](t, resp)
		if body.Error != "forbidden" {
			t.Fatalf("GET %s error = %q, want forbidden", path, body.Error)
		}
	}
}
