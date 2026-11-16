package http

import (
	nethttp "net/http"
	"time"

	"mise/api/internal/platform/httpserver"
)

func (h *Handler) PublicBoard(w nethttp.ResponseWriter, r *nethttp.Request) {
	projects, err := h.service.ListPublicProjects(r.Context())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	items := make([]publicProjectJSON, 0, len(projects))
	for _, project := range projects {
		items = append(items, publicProjectJSON{
			Key:         project.Key,
			Name:        project.Name,
			Description: redactedOptional(project.Description),
			Order:       project.SortOrder,
			State:       project.State,
			RepoURL:     project.RepoURL,
			DemoURL:     project.DemoURL,
			Cards:       cardsJSON{Total: project.Cards.Total, Done: project.Cards.Done},
			LastActive:  project.LastActive,
		})
	}

	w.Header().Set("Cache-Control", "max-age=60")
	httpserver.WriteJSON(w, nethttp.StatusOK, publicBoardJSON{
		GeneratedAt: time.Now().Unix(),
		Projects:    items,
	})
}

type publicBoardJSON struct {
	GeneratedAt int64               `json:"generated_at"`
	Projects    []publicProjectJSON `json:"projects"`
}

type publicProjectJSON struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Order       int       `json:"order"`
	State       string    `json:"state"`
	RepoURL     *string   `json:"repo_url"`
	DemoURL     *string   `json:"demo_url"`
	Cards       cardsJSON `json:"cards"`
	LastActive  *int64    `json:"last_active"`
}
