package app

import (
	"context"

	"mise/api/internal/tasks/domain"
)

type PlanPolicy struct {
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	WorkflowID int64  `json:"workflow_id"`
}

type MergeProof struct {
	DeliveryID int64  `json:"delivery_id"`
	PRURL      string `json:"pr_url"`
	HeadSHA    string `json:"head_sha"`
	MergeSHA   string `json:"merge_sha"`
	RunID      int64  `json:"run_id"`
	Attempt    int64  `json:"attempt"`
}

type DependencyPlan struct {
	FirstTask  string      `json:"first_task"`
	NextTask   string      `json:"next_task"`
	ApprovedBy int64       `json:"approved_by"`
	ApprovedAt int64       `json:"approved_at"`
	Policy     PlanPolicy  `json:"policy"`
	DeliveryID int64       `json:"delivery_id,omitempty"`
	PRURL      string      `json:"pr_url,omitempty"`
	HeadSHA    string      `json:"head_sha,omitempty"`
	Proof      *MergeProof `json:"proof"`
}

type DependencyPlans interface {
	ApprovePlan(context.Context, domain.Actor, string, string, PlanPolicy, int64) (DependencyPlan, error)
	Plan(context.Context, string) (DependencyPlan, error)
	PendingPlans(context.Context) ([]DependencyPlan, error)
	ReleasePlan(context.Context, string, MergeProof, int64) (DependencyPlan, error)
}
