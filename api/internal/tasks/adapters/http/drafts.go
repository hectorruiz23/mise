package http

import (
	"net/http"
	"strconv"
	"strings"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type Drafts struct {
	store  app.PlanDraftStore
	policy app.PlanPolicy
	clock  app.Clock
}

func NewDrafts(store app.PlanDraftStore, policy app.PlanPolicy, clock app.Clock) *Drafts {
	return &Drafts{store: store, policy: policy, clock: clock}
}

type draftBody struct {
	Project           string  `json:"project"`
	Outcome           string  `json:"outcome"`
	Scenario          string  `json:"scenario"`
	TargetEnvironment string  `json:"target_environment"`
	VisibleChange     *string `json:"visible_change"`
	VisualReview      bool    `json:"visual_review"`
	RevealKey         string  `json:"reveal_key"`
	Tasks             []struct {
		Key        string  `json:"key"`
		Title      string  `json:"title"`
		Notes      *string `json:"notes"`
		Acceptance *string `json:"acceptance"`
		Size       *string `json:"size"`
	} `json:"tasks"`
	Edges []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	} `json:"edges"`
}

func (b draftBody) input() app.DraftInput {
	tasks := make([]app.DraftTask, 0, len(b.Tasks))
	for _, task := range b.Tasks {
		tasks = append(tasks, app.DraftTask{
			Key: task.Key, Title: task.Title, Notes: task.Notes,
			Acceptance: task.Acceptance, Size: task.Size,
		})
	}
	edges := make([]app.DraftEdge, 0, len(b.Edges))
	for _, edge := range b.Edges {
		edges = append(edges, app.DraftEdge{From: edge.From, To: edge.To, Kind: edge.Kind})
	}
	return app.DraftInput{
		Project:           b.Project,
		Outcome:           b.Outcome,
		Scenario:          b.Scenario,
		TargetEnvironment: b.TargetEnvironment,
		VisibleChange:     b.VisibleChange,
		VisualReview:      b.VisualReview,
		RevealKey:         b.RevealKey,
		Tasks:             tasks,
		Edges:             edges,
	}
}

func (h *Drafts) CreateDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if isViewer(principal) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "viewers cannot propose a plan")
		return
	}
	if h.policy.WorkflowID == 0 {
		httpserver.WriteError(w, http.StatusConflict, kernel.CodeConflict, "plan verification is not configured")
		return
	}
	var body draftBody
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	draft, err := h.store.CreateDraft(r.Context(), actorOf(principal), body.input(), h.policy, h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.Header().Set("Location", "/api/plan-drafts/"+strconv.FormatInt(draft.ID, 10))
	httpserver.WriteJSON(w, http.StatusCreated, draft)
}

func (h *Drafts) UpdateDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if isViewer(principal) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "viewers cannot change a proposal")
		return
	}
	id, ok := draftID(w, r)
	if !ok {
		return
	}
	version, ok := draftVersion(w, r)
	if !ok {
		return
	}
	var body draftBody
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	draft, err := h.store.UpdateDraft(r.Context(), actorOf(principal), id, version, body.input(), h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, draft)
}

func (h *Drafts) Draft(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	id, ok := draftID(w, r)
	if !ok {
		return
	}
	draft, err := h.store.Draft(r.Context(), actorOf(principal), id)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, draft)
}

func (h *Drafts) List(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	drafts, err := h.store.ListDrafts(r.Context(), actorOf(principal), r.URL.Query().Get("project"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	if drafts == nil {
		drafts = []app.PlanDraftSummary{}
	}
	httpserver.WriteJSON(w, http.StatusOK, drafts)
}

func (h *Drafts) DiscardDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden,
			"only a human may discard a proposal")
		return
	}
	id, ok := draftID(w, r)
	if !ok {
		return
	}
	version, ok := draftVersion(w, r)
	if !ok {
		return
	}
	draft, err := h.store.DiscardDraft(r.Context(), actorOf(principal), id, version)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, draft)
}

func (h *Drafts) ApproveDraft(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden,
			"only a human may approve a proposal")
		return
	}
	id, ok := draftID(w, r)
	if !ok {
		return
	}
	version, ok := draftVersion(w, r)
	if !ok {
		return
	}
	draft, err := h.store.ApproveDraft(r.Context(), actorOf(principal), id, version, h.policy, h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, draft)
}

func draftID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput,
			"proposal id must be a positive integer")
		return 0, false
	}
	return id, true
}

func draftVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		httpserver.WriteError(w, http.StatusPreconditionRequired, kernel.CodePreconditionRequired,
			"If-Match is required")
		return 0, false
	}
	trimmed := strings.Trim(raw, "\"")
	version, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || version < 1 {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput,
			"If-Match must be a quoted proposal version")
		return 0, false
	}
	return version, true
}
