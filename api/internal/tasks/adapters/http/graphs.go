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
	if h.policy.WorkflowID == 0 {
		httpserver.WriteError(w, http.StatusConflict, kernel.CodeConflict, "plan verification is not configured")
		return
	}
	var body struct {
		Tasks []string `json:"tasks"`
		Edges []struct {
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
		h.policy, h.clock.Now())
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
	graphs, err := h.store.ListGraphs(r.Context())
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
