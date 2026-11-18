package http

import (
	"context"
	nethttp "net/http"
	"time"

	"mise/api/internal/githubinfo"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/domain"
)

const anchorsTimeout = 5 * time.Second

type anchorsJSON struct {
	TaskKey    string                       `json:"task_key"`
	DeliveryID *int64                       `json:"delivery_id"`
	PR         any                          `json:"pr"`
	Main       githubinfo.Envelope[*string] `json:"main"`
}

type linkPRJSON struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

type githubPRJSON struct {
	Kind     string                                  `json:"kind"`
	URL      string                                  `json:"url"`
	State    githubinfo.Envelope[string]             `json:"state"`
	MergeSHA githubinfo.Envelope[*string]            `json:"merge_sha"`
	Checks   githubinfo.Envelope[*githubinfo.Checks] `json:"checks"`
}

func (h *Handler) Anchors(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	key := r.PathValue("key")
	deliveries, err := h.service.Deliveries(r.Context(), actorOf(principal), key)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	latest := latestDelivery(deliveries)
	body := anchorsJSON{TaskKey: key}
	if latest != nil {
		id := latest.ID
		body.DeliveryID = &id
	}

	ctx, cancel := context.WithTimeout(r.Context(), anchorsTimeout)
	defer cancel()

	body.Main = h.github.Main(ctx)

	if latest != nil && latest.PRURL != nil {
		ref, isPullRequest := githubinfo.ParsePullRequest(*latest.PRURL)
		if !isPullRequest {
			body.PR = linkPRJSON{Kind: "link", URL: *latest.PRURL}
		} else {
			state, mergeSHA, checks := h.github.PullRequest(ctx, ref)
			body.PR = githubPRJSON{
				Kind:     "github",
				URL:      *latest.PRURL,
				State:    state,
				MergeSHA: mergeSHA,
				Checks:   checks,
			}
		}
	}

	body = redactedAnchors(body)
	httpserver.WriteJSON(w, nethttp.StatusOK, body)
}

func latestDelivery(deliveries []domain.Delivery) *domain.Delivery {
	var latest *domain.Delivery
	for index := range deliveries {
		candidate := &deliveries[index]
		if latest == nil ||
			candidate.CreatedAt > latest.CreatedAt ||
			(candidate.CreatedAt == latest.CreatedAt && candidate.ID > latest.ID) {
			latest = candidate
		}
	}
	return latest
}
