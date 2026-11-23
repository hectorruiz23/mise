package http

import (
	"net/http"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func (h *Handler) Environments(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if isViewer(principal) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden,
			"a credential is required to inspect environments")
		return
	}
	environments, err := h.service.Environments(r.Context(), actorOf(principal), r.PathValue("key"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	if environments == nil {
		environments = []app.Environment{}
	}
	httpserver.WriteJSON(w, http.StatusOK, environments)
}

func (h *Handler) AddEnvironment(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden,
			"only a human may register an environment")
		return
	}
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	environment, err := h.service.AddEnvironment(r.Context(), actorOf(principal),
		r.PathValue("key"), body.Name, body.URL)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, environment)
}
