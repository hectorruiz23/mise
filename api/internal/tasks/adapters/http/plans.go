package http

import (
	"net/http"
	"net/url"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type DependencyPlans struct {
	plans  app.DependencyPlans
	policy app.PlanPolicy
	clock  app.Clock
}

func NewDependencyPlans(plans app.DependencyPlans, policy app.PlanPolicy, clock app.Clock) *DependencyPlans {
	return &DependencyPlans{plans: plans, policy: policy, clock: clock}
}

func (h *DependencyPlans) Approve(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "only a human may approve a plan")
		return
	}
	if h.policy.WorkflowID == 0 {
		httpserver.WriteError(w, http.StatusConflict, kernel.CodeConflict, "plan verification is not configured")
		return
	}
	var body struct {
		Prerequisite string `json:"prerequisite_key"`
		Dependent    string `json:"dependent_key"`
	}
	if err := decodeStrict(r, &body); err != nil || body.Prerequisite == "" || body.Dependent == "" {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "prerequisite_key and dependent_key are required")
		return
	}
	plan, err := h.plans.ApprovePlan(r.Context(), actorOf(principal), body.Prerequisite, body.Dependent, h.policy, h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.Header().Set("Location", "/api/dependency-plans/"+url.PathEscape(plan.FirstTask))
	httpserver.WriteJSON(w, http.StatusCreated, planOf(plan))
}

func (h *DependencyPlans) Get(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if isViewer(principal) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "a credential is required to inspect a plan")
		return
	}
	plan, err := h.plans.Plan(r.Context(), r.PathValue("firstTask"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, planOf(plan))
}

func (h *DependencyPlans) Cancel(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "only a human may cancel a plan")
		return
	}
	plan, err := h.plans.CancelPlan(r.Context(), actorOf(principal), r.PathValue("firstTask"), h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, planOf(plan))
}

type planJSON struct {
	Prerequisite string          `json:"prerequisite_key"`
	Dependent    string          `json:"dependent_key"`
	State        string          `json:"state"`
	Repository   string          `json:"repository"`
	Branch       string          `json:"branch"`
	WorkflowID   int64           `json:"workflow_id"`
	ApprovedBy   int64           `json:"approved_by"`
	ApprovedAt   int64           `json:"approved_at"`
	Proof        *app.MergeProof `json:"proof"`
	CancelledBy  int64           `json:"cancelled_by,omitempty"`
	CancelledAt  int64           `json:"cancelled_at,omitempty"`
}

func planOf(plan app.DependencyPlan) planJSON {
	body := planJSON{
		Prerequisite: plan.FirstTask, Dependent: plan.NextTask, State: "waiting",
		ApprovedBy: plan.ApprovedBy, ApprovedAt: plan.ApprovedAt,
		Repository: plan.Policy.Repository, Branch: plan.Policy.Branch, WorkflowID: plan.Policy.WorkflowID,
		CancelledBy: plan.CancelledBy, CancelledAt: plan.CancelledAt,
	}
	switch {
	case plan.Proof != nil:
		body.State = "released"
		body.Proof = plan.Proof
	case plan.CancelledAt > 0:
		body.State = "cancelled"
	}
	return body
}
