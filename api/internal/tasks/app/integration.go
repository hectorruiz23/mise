package app

import "context"

const (
	MergeOutcomeMerged  = "merged"
	MergeOutcomeRefused = "refused"
	MergeOutcomeFailed  = "failed"
)

type MergeCandidate struct {
	Repository, Branch string
	TaskKey            string
	ProjectKey         string
	DeliveryID         int64
	PRURL              string
	HeadSHA            *string
	ActorID            int64
}

type MergeOutcome struct {
	DeliveryID int64
	TaskKey    string
	PRURL      string
	ActorID    int64
	Outcome    string
	Reason     string
	MergeSHA   *string
}

type MergeEvidence struct {
	TaskKey    string
	PRURL      string
	DeliveryID int64
	HeadSHA    *string
	MergeSHA   string
	ActorID    int64
}

type DeploymentCandidate struct {
	PlanID      int64
	TaskKey     string
	Revision    string
	Environment string
	URL         string
}

type IntegrationStore interface {
	PendingMerges(context.Context) ([]MergeCandidate, error)
	RecordMergeAttempt(context.Context, MergeOutcome, int64) error
	RecordMerge(context.Context, MergeEvidence, int64) error
	PendingDeployments(context.Context) ([]DeploymentCandidate, error)
	RecordDeployment(context.Context, int64, string, string, string, int64) error
}
