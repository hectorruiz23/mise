package http

import (
	"net/http"
	"strconv"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

type Graphs struct {
	store  app.PlanStore
	policy app.PlanPolicy
	clock  app.Clock
}

func NewGraphs(store app.PlanStore, policy app.PlanPolicy, clock app.Clock) *Graphs {
	return &Graphs{store: store, policy: policy, clock: clock}
}

func (h *Graphs) Approve(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "only a human may approve a plan")
		return
	}

	var body struct {
		TargetEnvironment string   `json:"target_environment"`
		Tasks             []string `json:"tasks"`
		Scenario          string   `json:"scenario"`
		VisibleChange     *string  `json:"visible_change"`
		VisualReview      bool     `json:"visual_review"`
		RevealTask        string   `json:"reveal_task"`
		Edges             []struct {
			From string `json:"from"`
			To   string `json:"to"`
			Kind string `json:"kind"`
		} `json:"edges"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	edges := make([]domain.PlanEdge, 0, len(body.Edges))
	for _, edge := range body.Edges {
		kind := edge.Kind
		if kind == "" {
			kind = domain.EdgeIntegration
		}
		edges = append(edges, domain.PlanEdge{From: edge.From, To: edge.To, Kind: kind})
	}
	graph, err := h.store.ApproveGraph(r.Context(), actorOf(principal), body.Tasks, edges,
		h.policy, app.PlanReview{
			TargetEnvironment: body.TargetEnvironment,
			Scenario:          body.Scenario,
			VisibleChange:     body.VisibleChange,
			VisualReview:      body.VisualReview,
			RevealTask:        body.RevealTask,
		}, h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.Header().Set("Location", "/api/plans/"+strconv.FormatInt(graph.ID, 10))
	httpserver.WriteJSON(w, http.StatusCreated, graph)
}

func (h *Graphs) Get(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if isViewer(principal) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "a credential is required to inspect a plan")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "plan id must be a positive integer")
		return
	}
	graph, err := h.store.Graph(r.Context(), id)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, graph)
}

func (h *Graphs) List(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if isViewer(principal) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "a credential is required to inspect a plan")
		return
	}
	graphs, err := h.store.ListGraphs(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	if graphs == nil {
		graphs = []app.PlanGraph{}
	}
	httpserver.WriteJSON(w, http.StatusOK, graphs)
}

func (h *Graphs) Cancel(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "only a human may cancel a plan")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "plan id must be a positive integer")
		return
	}
	graph, err := h.store.CancelGraph(r.Context(), actorOf(principal), id, h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, graph)
}

func (h *Graphs) ReportScenario(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "plan id must be a positive integer")
		return
	}
	var body struct {
		TaskKey     string  `json:"task_key"`
		HeadSHA     string  `json:"head_sha"`
		Command     string  `json:"command"`
		Outcome     string  `json:"outcome"`
		EvidenceURL *string `json:"evidence_url"`
		Notes       *string `json:"notes"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	graph, err := h.store.ReportScenario(r.Context(), actorOf(principal), id, app.ScenarioReport{
		TaskKey:     body.TaskKey,
		HeadSHA:     body.HeadSHA,
		Command:     body.Command,
		Outcome:     body.Outcome,
		EvidenceURL: body.EvidenceURL,
		Notes:       body.Notes,
	}, h.clock.Now())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, graph)
}
