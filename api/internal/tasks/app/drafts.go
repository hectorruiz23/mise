package app

import (
	"context"

	"mise/api/internal/tasks/domain"
)

const (
	DraftDraft     = "draft"
	DraftApproved  = "approved"
	DraftDiscarded = "discarded"
)

type DraftTask struct {
	Key        string  `json:"key"`
	Title      string  `json:"title"`
	Notes      *string `json:"notes"`
	Acceptance *string `json:"acceptance"`
	Size       *string `json:"size"`
	TaskKey    *string `json:"task_key"`
}

type DraftEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type PlanDraft struct {
	ID                int64       `json:"id"`
	Project           string      `json:"project"`
	AuthorID          int64       `json:"author_id"`
	Policy            PlanPolicy  `json:"policy"`
	Outcome           string      `json:"outcome"`
	Scenario          string      `json:"scenario"`
	TargetEnvironment string      `json:"target_environment"`
	VisibleChange     *string     `json:"visible_change"`
	VisualReview      bool        `json:"visual_review"`
	RevealKey         *string     `json:"reveal_key"`
	Version           int64       `json:"version"`
	State             string      `json:"state"`
	ApprovedBy        *int64      `json:"approved_by"`
	ApprovedAt        *int64      `json:"approved_at"`
	PlanID            *int64      `json:"plan_id"`
	Tasks             []DraftTask `json:"tasks"`
	Edges             []DraftEdge `json:"edges"`
	Roots             []string    `json:"roots"`
	CreatedAt         int64       `json:"created_at"`
	UpdatedAt         int64       `json:"updated_at"`
}

type DraftInput struct {
	Project           string
	Outcome           string
	Scenario          string
	TargetEnvironment string
	VisibleChange     *string
	VisualReview      bool
	RevealKey         string
	Tasks             []DraftTask
	Edges             []DraftEdge
}

type PlanDraftStore interface {
	CreateDraft(context.Context, domain.Actor, DraftInput, PlanPolicy, int64) (PlanDraft, error)
	UpdateDraft(context.Context, domain.Actor, int64, int64, DraftInput, int64) (PlanDraft, error)
	Draft(context.Context, domain.Actor, int64) (PlanDraft, error)
	DiscardDraft(context.Context, domain.Actor, int64, int64) (PlanDraft, error)
	ApproveDraft(context.Context, domain.Actor, int64, int64, PlanPolicy, int64) (PlanDraft, error)
}
