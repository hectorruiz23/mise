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

const (
	ScenarioPassed = "passed"
	ScenarioFailed = "failed"

	PlanCompleted = "completed"
)

type PlanReview struct {
	TargetEnvironment string
	Scenario          string
	VisibleChange     *string
	VisualReview      bool
	RevealTask        string
}

type ScenarioReport struct {
	TaskKey     string
	HeadSHA     string
	Command     string
	Outcome     string
	EvidenceURL *string
	Notes       *string
}

type PlanScenario struct {
	TaskKey     string  `json:"task_key"`
	HeadSHA     string  `json:"head_sha"`
	Command     string  `json:"command"`
	Outcome     string  `json:"outcome"`
	EvidenceURL *string `json:"evidence_url"`
	Notes       *string `json:"notes"`
	RecordedAt  int64   `json:"recorded_at"`
}

type PlanApproval struct {
	TaskKey    string `json:"task_key"`
	HeadSHA    string `json:"head_sha"`
	Reviewer   string `json:"reviewer"`
	ApprovedAt int64  `json:"approved_at"`
}

type PlanProof struct {
	TaskKey    string `json:"task_key"`
	DeliveryID int64  `json:"delivery_id"`
	PRURL      string `json:"pr_url"`
	HeadSHA    string `json:"head_sha"`
	MergeSHA   string `json:"merge_sha"`
	RunID      int64  `json:"run_id"`
	Attempt    int64  `json:"attempt"`
	VerifiedAt int64  `json:"verified_at"`
	Origin     string `json:"origin"`
}

type PlanDeployment struct {
	Environment string `json:"environment"`
	Revision    string `json:"revision"`
	ObservedAt  int64  `json:"observed_at"`
}

type PlanNode struct {
	Key            string           `json:"task_key"`
	Title          string           `json:"title"`
	State          string           `json:"state"`
	Acceptance     *string          `json:"acceptance"`
	WaitingOn      []string         `json:"waiting_on"`
	Proof          *PlanProof       `json:"proof"`
	Deployments    []PlanDeployment `json:"deployments"`
	Reveals        bool             `json:"reveals"`
	Correction     bool             `json:"correction"`
	OriginEvidence *string          `json:"origin_evidence"`
}

type PlanGraph struct {
	TargetEnvironment string            `json:"target_environment,omitempty"`
	ID                int64             `json:"id"`
	Project           string            `json:"project"`
	Policy            PlanPolicy        `json:"policy"`
	Scenario          string            `json:"scenario"`
	VisibleChange     *string           `json:"visible_change"`
	VisualReview      bool              `json:"visual_review"`
	ApprovedBy        int64             `json:"approved_by"`
	ApprovedAt        int64             `json:"approved_at"`
	CompletedAt       *int64            `json:"completed_at"`
	State             string            `json:"state"`
	CancelledBy       int64             `json:"cancelled_by,omitempty"`
	CancelledAt       int64             `json:"cancelled_at,omitempty"`
	Approval          *PlanApproval     `json:"approval"`
	Nodes             []PlanNode        `json:"nodes"`
	Edges             []domain.PlanEdge `json:"edges"`
}

type PendingPlanTask struct {
	ApprovedAt int64
	PlanID     int64
	TaskKey    string
	DeliveryID int64
	PRURL      string
	HeadSHA    string
	Policy     PlanPolicy
}

type PendingPlanReview struct {
	PlanID     int64
	TaskKey    string
	PRURL      string
	HeadSHA    string
	Repository string
}

type PlanStore interface {
	ApproveGraph(context.Context, domain.Actor, []string, []domain.PlanEdge, PlanPolicy, PlanReview, int64) (PlanGraph, error)
	Graph(context.Context, int64) (PlanGraph, error)
	ListGraphs(context.Context, string) ([]PlanGraph, error)
	CancelGraph(context.Context, domain.Actor, int64, int64) (PlanGraph, error)
	PendingGraphTasks(context.Context) ([]PendingPlanTask, error)
	ReleaseGraphTask(context.Context, int64, string, PlanProof, int64) (PlanGraph, error)
	ReportScenario(context.Context, domain.Actor, int64, ScenarioReport, int64) (PlanGraph, error)
	PendingPlanReviews(context.Context) ([]PendingPlanReview, error)
	RecordApproval(context.Context, int64, string, string, string, int64) (PlanGraph, error)
	RequestCorrection(context.Context, int64, string, string, string, string, int64) (PlanGraph, error)
}
