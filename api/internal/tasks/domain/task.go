package domain

import "strings"

type State string

const (
	Backlog    State = "backlog"
	Ready      State = "ready"
	InProgress State = "in_progress"
	Blocked    State = "blocked"
	Review     State = "review"
	Done       State = "done"
)

type ActorKind string

const (
	Human ActorKind = "human"
	Agent ActorKind = "agent"
)

type Actor struct {
	ID   int64
	Kind ActorKind
	Name string
}

type Priority string

const (
	Low    Priority = "low"
	Normal Priority = "normal"
	High   Priority = "high"
)

type Size string

const (
	Small  Size = "S"
	Medium Size = "M"
	Large  Size = "L"
)

type Task struct {
	Key        string
	ProjectKey string
	Title      string
	Notes      *string
	Acceptance *string
	State      State
	Priority   Priority
	Size       *Size
	CreatedBy  int64
	Version    int64
	CreatedAt  int64
	UpdatedAt  int64
	ClosedAt   *int64
}

type Lease struct {
	ID           int64
	TaskKey      string
	AgentID      int64
	Agent        Actor
	CreatedAt    int64
	ExpiresAt    int64
	ReleasedAt   *int64
	ClosedReason *string
}

type Movement struct {
	TaskKey     string
	FromState   *State
	ToState     State
	ActorID     int64
	Actor       Actor
	Reason      *string
	TaskVersion int64
	At          int64
}

type Delivery struct {
	ID        int64
	TaskKey   string
	LeaseID   *int64
	Summary   string
	Evidence  []string
	CreatedAt int64
	ActorID   int64
	Actor     Actor
}

type Comment struct {
	ID        int64
	TaskKey   string
	ActorID   int64
	Actor     Actor
	Body      string
	CreatedAt int64
}

func (s State) Valid() bool {
	switch s {
	case Backlog, Ready, InProgress, Blocked, Review, Done:
		return true
	default:
		return false
	}
}

func (k ActorKind) Valid() bool {
	return k == Human || k == Agent
}

func (p Priority) Valid() bool {
	switch p {
	case Low, Normal, High:
		return true
	default:
		return false
	}
}

func (s Size) Valid() bool {
	switch s {
	case Small, Medium, Large:
		return true
	default:
		return false
	}
}

func NewTask(key, projectKey, title string, createdBy, now int64) (Task, error) {
	if strings.TrimSpace(key) == "" {
		return Task{}, invalidInput("task key is required")
	}
	if strings.TrimSpace(projectKey) == "" {
		return Task{}, invalidInput("project key is required")
	}
	if strings.TrimSpace(title) == "" {
		return Task{}, invalidInput("title is required")
	}

	return Task{
		Key:        key,
		ProjectKey: projectKey,
		Title:      title,
		State:      Backlog,
		Priority:   Normal,
		CreatedBy:  createdBy,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func NewLease(taskKey string, agentID, createdAt, expiresAt int64) (Lease, error) {
	if strings.TrimSpace(taskKey) == "" {
		return Lease{}, invalidInput("task key is required")
	}
	if expiresAt <= createdAt {
		return Lease{}, invalidInput("lease must expire after it starts")
	}

	return Lease{
		TaskKey:   taskKey,
		AgentID:   agentID,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
	}, nil
}
