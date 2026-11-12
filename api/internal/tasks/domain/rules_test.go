package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func requireCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rule error %q, got nil", want)
	}
	var ruleErr *RuleError
	if !errors.As(err, &ruleErr) {
		t.Fatalf("expected *RuleError, got %T: %v", err, err)
	}
	if ruleErr.Code != want {
		t.Fatalf("error code = %q, want %q (message: %s)", ruleErr.Code, want, ruleErr.Msg)
	}
}

func baseTask(state State) Task {
	return Task{
		Key:        "MI-1",
		ProjectKey: "MI",
		Title:      "Implement the domain rules",
		State:      state,
		Priority:   Normal,
		CreatedBy:  1,
		Version:    3,
		CreatedAt:  100,
		UpdatedAt:  200,
	}
}

func actorOf(kind ActorKind) Actor {
	return Actor{ID: 7, Kind: kind, Name: "actor"}
}

func allStates() []State {
	return []State{Backlog, Ready, InProgress, Blocked, Review, Done}
}

func expectedMatrix() map[State]map[ActorKind][]State {
	return map[State]map[ActorKind][]State{
		Backlog: {
			Human: {Ready},
			Agent: nil,
		},
		Ready: {
			Human: {Backlog, InProgress},
			Agent: {InProgress},
		},
		InProgress: {
			Human: {Ready, Blocked, Review},
			Agent: {Blocked, Review},
		},
		Blocked: {
			Human: {Ready, InProgress},
			Agent: {InProgress},
		},
		Review: {
			Human: {InProgress, Done},
			Agent: {InProgress},
		},
		Done: {
			Human: {Ready},
			Agent: nil,
		},
	}
}

func TestTransitionMatrix(t *testing.T) {
	want := expectedMatrix()

	for _, from := range allStates() {
		for _, kind := range []ActorKind{Human, Agent} {
			t.Run(fmt.Sprintf("%s/%s", from, kind), func(t *testing.T) {
				allowed := want[from][kind]
				if got := AllowedTransitions(from, kind); !slices.Equal(got, allowed) {
					t.Errorf("AllowedTransitions(%q, %q) = %v, want %v", from, kind, got, allowed)
				}

				for _, to := range allStates() {
					err := ValidateMove(baseTask(from), to, actorOf(kind), "a written reason", true)
					isAllowed := slices.Contains(allowed, to)

					if isAllowed {
						if err != nil {
							t.Errorf("move %s -> %s as %s rejected: %v", from, to, kind, err)
						}
						continue
					}

					wantCode := CodeInvalidTransition
					if from == Review && to == Done && kind == Agent {
						wantCode = CodeForbidden
					}
					if err == nil {
						t.Errorf("move %s -> %s as %s was allowed, want %q", from, to, kind, wantCode)
						continue
					}
					requireCode(t, err, wantCode)
				}
			})
		}
	}
}

func TestAllowedTransitionsReturnsACopy(t *testing.T) {
	got := AllowedTransitions(InProgress, Human)
	if len(got) == 0 {
		t.Fatal("expected transitions from in_progress")
	}
	got[0] = Done
	if again := AllowedTransitions(InProgress, Human); again[0] == Done {
		t.Fatal("AllowedTransitions exposed the internal matrix")
	}

	if got := AllowedTransitions(State("unknown"), Human); got != nil {
		t.Errorf("unknown state returned %v, want nil", got)
	}
	if got := AllowedTransitions(Backlog, ActorKind("robot")); got != nil {
		t.Errorf("unknown actor returned %v, want nil", got)
	}
}

func TestHumanGateOnReviewToDone(t *testing.T) {
	now := int64(5000)
	task := baseTask(Review)

	_, _, err := ApplyMove(task, Done, actorOf(Agent), "", true, now)
	requireCode(t, err, CodeForbidden)
	if task.Version != 3 || task.State != Review || task.ClosedAt != nil {
		t.Fatalf("rejected move mutated the receiver: %+v", task)
	}

	next, movement, err := ApplyMove(task, Done, actorOf(Human), "", true, now)
	if err != nil {
		t.Fatalf("human close: %v", err)
	}
	if next.State != Done {
		t.Errorf("State = %q, want %q", next.State, Done)
	}
	if next.Version != task.Version+1 {
		t.Errorf("Version = %d, want exactly %d", next.Version, task.Version+1)
	}
	if next.UpdatedAt != now {
		t.Errorf("UpdatedAt = %d, want %d", next.UpdatedAt, now)
	}
	if next.ClosedAt == nil || *next.ClosedAt != now {
		t.Errorf("ClosedAt = %v, want %d", next.ClosedAt, now)
	}
	if task.ClosedAt != nil {
		t.Errorf("receiver ClosedAt = %v, want nil", task.ClosedAt)
	}
	if movement.FromState == nil || *movement.FromState != Review || movement.ToState != Done {
		t.Errorf("movement = %+v, want review -> done", movement)
	}
	if movement.TaskVersion != next.Version || movement.At != now || movement.ActorID != 7 {
		t.Errorf("movement metadata = %+v, want version %d at %d by 7", movement, next.Version, now)
	}
}

func TestReopeningClearsClosedAt(t *testing.T) {
	closedAt := int64(4000)
	task := baseTask(Done)
	task.ClosedAt = &closedAt

	next, _, err := ApplyMove(task, Ready, actorOf(Human), "", false, 5000)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if next.ClosedAt != nil {
		t.Errorf("ClosedAt = %v, want nil after reopening", next.ClosedAt)
	}
	if next.State != Ready || next.Version != task.Version+1 {
		t.Errorf("reopened task = %+v", next)
	}
	if task.ClosedAt == nil || *task.ClosedAt != closedAt {
		t.Errorf("receiver ClosedAt changed: %v", task.ClosedAt)
	}
}

func TestReasonRequired(t *testing.T) {
	tests := []struct {
		name   string
		from   State
		to     State
		kind   ActorKind
		reason string
		want   string
	}{
		{name: "enter blocked without reason", from: InProgress, to: Blocked, kind: Human, want: CodeReasonRequired},
		{name: "enter blocked with blank reason", from: InProgress, to: Blocked, kind: Agent, reason: " \t ", want: CodeReasonRequired},
		{name: "leave blocked without reason", from: Blocked, to: InProgress, kind: Human, want: CodeReasonRequired},
		{name: "leave blocked with blank reason", from: Blocked, to: Ready, kind: Human, reason: "  ", want: CodeReasonRequired},
		{name: "enter blocked with reason", from: InProgress, to: Blocked, kind: Human, reason: "waiting on the API"},
		{name: "leave blocked with reason", from: Blocked, to: InProgress, kind: Agent, reason: "API fixed"},
		{name: "blank optional reason", from: Ready, to: InProgress, kind: Human, reason: "   ", want: CodeInvalidInput},
		{name: "empty optional reason", from: Backlog, to: Ready, kind: Human},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMove(baseTask(tt.from), tt.to, actorOf(tt.kind), tt.reason, true)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			requireCode(t, err, tt.want)
		})
	}
}

func TestReviewRequiresDelivery(t *testing.T) {
	for _, kind := range []ActorKind{Human, Agent} {
		t.Run(string(kind), func(t *testing.T) {
			err := ValidateMove(baseTask(InProgress), Review, actorOf(kind), "", false)
			requireCode(t, err, CodeEvidenceRequired)

			if err := ValidateMove(baseTask(InProgress), Review, actorOf(kind), "", true); err != nil {
				t.Fatalf("move with a historical delivery rejected: %v", err)
			}
		})
	}
}

func TestValidateMoveRejectsBlankTitle(t *testing.T) {
	task := baseTask(Ready)
	task.Title = "   "
	requireCode(t, ValidateMove(task, InProgress, actorOf(Human), "", true), CodeInvalidInput)
}

func TestValidateEvidence(t *testing.T) {
	entries := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("evidence %d", i+1)
		}
		return out
	}

	tests := []struct {
		name     string
		evidence []string
		want     string
	}{
		{name: "nil", evidence: nil, want: CodeInvalidInput},
		{name: "empty", evidence: []string{}, want: CodeInvalidInput},
		{name: "one valid", evidence: []string{"go test ./..."}},
		{name: "blank entry", evidence: []string{"ok", "  "}, want: CodeInvalidInput},
		{name: "empty entry", evidence: []string{""}, want: CodeInvalidInput},
		{name: "twenty valid", evidence: entries(20)},
		{name: "twenty one", evidence: entries(21), want: CodeInvalidInput},
		{name: "four thousand characters", evidence: []string{strings.Repeat("x", 4000)}},
		{name: "four thousand and one characters", evidence: []string{strings.Repeat("x", 4001)}, want: CodeInvalidInput},
		{name: "four thousand multibyte characters", evidence: []string{strings.Repeat("é", 4000)}},
		{name: "four thousand and one multibyte characters", evidence: []string{strings.Repeat("é", 4001)}, want: CodeInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEvidence(tt.evidence)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			requireCode(t, err, tt.want)
		})
	}
}

func TestIsLive(t *testing.T) {
	lease := Lease{ExpiresAt: 1000}
	if !IsLive(lease, 999) {
		t.Error("lease should be live before expiry")
	}
	if IsLive(lease, 1000) {
		t.Error("lease should be dead at expires_at == now")
	}
	if IsLive(lease, 1001) {
		t.Error("lease should be dead after expiry")
	}

	releasedAt := int64(900)
	lease.ReleasedAt = &releasedAt
	if IsLive(lease, 999) {
		t.Error("released lease should never be live")
	}
}

func TestAcquireOnReady(t *testing.T) {
	now := int64(1000)
	task := baseTask(Ready)

	next, lease, movement, err := Acquire(task, nil, actorOf(Agent), now, 900)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if next.State != InProgress {
		t.Errorf("State = %q, want %q", next.State, InProgress)
	}
	if next.Version != task.Version+1 {
		t.Errorf("Version = %d, want exactly %d", next.Version, task.Version+1)
	}
	if next.UpdatedAt != now {
		t.Errorf("UpdatedAt = %d, want %d", next.UpdatedAt, now)
	}
	if task.State != Ready || task.Version != 3 {
		t.Errorf("Acquire mutated the receiver: %+v", task)
	}

	if lease.TaskKey != task.Key || lease.AgentID != 7 || lease.CreatedAt != now {
		t.Errorf("lease identity = %+v", lease)
	}
	if lease.ExpiresAt != now+900 {
		t.Errorf("ExpiresAt = %d, want %d", lease.ExpiresAt, now+900)
	}
	if lease.ReleasedAt != nil || lease.ClosedReason != nil {
		t.Errorf("new lease is not open: %+v", lease)
	}

	if movement.FromState == nil || *movement.FromState != Ready || movement.ToState != InProgress {
		t.Errorf("movement = %+v, want ready -> in_progress", movement)
	}
	if movement.Reason != nil {
		t.Errorf("movement reason = %q, want nil", *movement.Reason)
	}
	if movement.TaskVersion != next.Version || movement.ActorID != 7 || movement.At != now {
		t.Errorf("movement metadata = %+v", movement)
	}
}

func TestAcquireTakeover(t *testing.T) {
	now := int64(1000)

	tests := []struct {
		name string
		live *Lease
	}{
		{name: "no lease", live: nil},
		{name: "expired lease", live: &Lease{ExpiresAt: now}},
		{name: "released lease", live: &Lease{ExpiresAt: now + 900, ReleasedAt: ptrInt64(900)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := baseTask(InProgress)
			next, lease, movement, err := Acquire(task, tt.live, actorOf(Agent), now, 900)
			if err != nil {
				t.Fatalf("takeover: %v", err)
			}
			if next.State != InProgress || next.Version != task.Version+1 {
				t.Errorf("next = %+v, want in_progress at version %d", next, task.Version+1)
			}
			if lease.ExpiresAt != now+900 {
				t.Errorf("ExpiresAt = %d, want %d", lease.ExpiresAt, now+900)
			}
			if movement.FromState == nil || *movement.FromState != InProgress || movement.ToState != InProgress {
				t.Errorf("movement = %+v, want in_progress -> in_progress", movement)
			}
			if movement.Reason == nil || *movement.Reason != "takeover" {
				t.Errorf("movement reason = %v, want takeover", movement.Reason)
			}
		})
	}
}

func TestAcquireRejections(t *testing.T) {
	now := int64(1000)
	live := &Lease{ExpiresAt: now + 60}

	tests := []struct {
		name  string
		task  Task
		live  *Lease
		agent Actor
		ttl   int64
		want  string
	}{
		{name: "live lease on in_progress", task: baseTask(InProgress), live: live, agent: actorOf(Agent), ttl: 900, want: CodeLeaseConflict},
		{name: "live lease on ready", task: baseTask(Ready), live: live, agent: actorOf(Agent), ttl: 900, want: CodeLeaseConflict},
		{name: "backlog", task: baseTask(Backlog), agent: actorOf(Agent), ttl: 900, want: CodeLeaseConflict},
		{name: "blocked", task: baseTask(Blocked), agent: actorOf(Agent), ttl: 900, want: CodeLeaseConflict},
		{name: "review", task: baseTask(Review), agent: actorOf(Agent), ttl: 900, want: CodeLeaseConflict},
		{name: "done", task: baseTask(Done), agent: actorOf(Agent), ttl: 900, want: CodeLeaseConflict},
		{name: "human", task: baseTask(Ready), agent: actorOf(Human), ttl: 900, want: CodeForbidden},
		{name: "zero ttl", task: baseTask(Ready), agent: actorOf(Agent), ttl: 0, want: CodeInvalidInput},
		{name: "negative ttl", task: baseTask(Ready), agent: actorOf(Agent), ttl: -1, want: CodeInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := Acquire(tt.task, tt.live, tt.agent, now, tt.ttl)
			requireCode(t, err, tt.want)
		})
	}
}

func TestApplyDeliveryByHumanWithoutLease(t *testing.T) {
	now := int64(1500)
	task := baseTask(InProgress)
	evidence := []string{"go test ./...", "commit abc123"}

	next, delivery, movement, err := ApplyDelivery(task, nil, actorOf(Human), "Done and verified", evidence, now)
	if err != nil {
		t.Fatalf("human delivery: %v", err)
	}
	if next.State != Review {
		t.Errorf("State = %q, want %q", next.State, Review)
	}
	if next.Version != task.Version+1 {
		t.Errorf("Version = %d, want exactly %d", next.Version, task.Version+1)
	}
	if next.UpdatedAt != now || next.ClosedAt != nil {
		t.Errorf("timestamps = updated %d closed %v, want %d and nil", next.UpdatedAt, next.ClosedAt, now)
	}
	if task.State != InProgress || task.Version != 3 {
		t.Errorf("ApplyDelivery mutated the receiver: %+v", task)
	}

	if delivery.TaskKey != task.Key || delivery.ActorID != 7 || delivery.CreatedAt != now {
		t.Errorf("delivery identity = %+v", delivery)
	}
	if delivery.LeaseID != nil {
		t.Errorf("LeaseID = %v, want nil for a human delivery", delivery.LeaseID)
	}
	if delivery.Summary != "Done and verified" || !slices.Equal(delivery.Evidence, evidence) {
		t.Errorf("delivery body = %+v", delivery)
	}

	evidence[0] = "mutated after the call"
	if delivery.Evidence[0] != "go test ./..." {
		t.Error("Delivery.Evidence aliases the caller's slice")
	}

	if movement.FromState == nil || *movement.FromState != InProgress || movement.ToState != Review {
		t.Errorf("movement = %+v, want in_progress -> review", movement)
	}
	if movement.TaskVersion != next.Version || movement.At != now {
		t.Errorf("movement metadata = %+v", movement)
	}
}

func TestApplyDeliveryByAgent(t *testing.T) {
	now := int64(1500)
	task := baseTask(InProgress)
	lease := Lease{ID: 42, TaskKey: task.Key, AgentID: 7, CreatedAt: 1000, ExpiresAt: now + 60}

	next, delivery, _, err := ApplyDelivery(task, &lease, actorOf(Agent), "Ready for review", []string{"proof"}, now)
	if err != nil {
		t.Fatalf("agent delivery: %v", err)
	}
	if next.State != Review || next.Version != task.Version+1 {
		t.Errorf("next = %+v, want review at version %d", next, task.Version+1)
	}
	if delivery.LeaseID == nil || *delivery.LeaseID != 42 {
		t.Errorf("LeaseID = %v, want 42", delivery.LeaseID)
	}
}

func TestValidateDeliveryRejections(t *testing.T) {
	task := baseTask(InProgress)
	otherAgentLease := Lease{ID: 1, TaskKey: task.Key, AgentID: 99, ExpiresAt: 2000}
	otherTaskLease := Lease{ID: 2, TaskKey: "MI-2", AgentID: 7, ExpiresAt: 2000}
	released := int64(1400)
	releasedLease := Lease{ID: 3, TaskKey: task.Key, AgentID: 7, ExpiresAt: 2000, ReleasedAt: &released}

	tests := []struct {
		name     string
		task     Task
		lease    *Lease
		actor    Actor
		summary  string
		evidence []string
		want     string
	}{
		{name: "agent without lease", task: task, actor: actorOf(Agent), summary: "ok", evidence: []string{"proof"}, want: CodeLeaseRequired},
		{name: "agent with another agent's lease", task: task, lease: &otherAgentLease, actor: actorOf(Agent), summary: "ok", evidence: []string{"proof"}, want: CodeLeaseRequired},
		{name: "agent with another task's lease", task: task, lease: &otherTaskLease, actor: actorOf(Agent), summary: "ok", evidence: []string{"proof"}, want: CodeLeaseRequired},
		{name: "agent with a released lease", task: task, lease: &releasedLease, actor: actorOf(Agent), summary: "ok", evidence: []string{"proof"}, want: CodeLeaseRequired},
		{name: "blank summary", task: task, actor: actorOf(Human), summary: "  ", evidence: []string{"proof"}, want: CodeInvalidInput},
		{name: "bad evidence", task: task, actor: actorOf(Human), summary: "ok", evidence: nil, want: CodeInvalidInput},
		{name: "not in progress", task: baseTask(Review), actor: actorOf(Human), summary: "ok", evidence: []string{"proof"}, want: CodeInvalidTransition},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDelivery(tt.task, tt.lease, tt.actor, tt.summary, tt.evidence)
			requireCode(t, err, tt.want)
		})
	}
}

func TestApplyDeliveryRejectsExpiredLease(t *testing.T) {
	now := int64(2000)
	task := baseTask(InProgress)
	lease := Lease{ID: 42, TaskKey: task.Key, AgentID: 7, CreatedAt: 1000, ExpiresAt: now}

	_, _, _, err := ApplyDelivery(task, &lease, actorOf(Agent), "Ready", []string{"proof"}, now)
	requireCode(t, err, CodeLeaseRequired)
}

func TestCheckVersion(t *testing.T) {
	task := baseTask(Ready)

	requireCode(t, CheckVersion(task, 3, false), CodePreconditionRequired)
	requireCode(t, CheckVersion(task, 2, true), CodeStaleVersion)
	if err := CheckVersion(task, 3, true); err != nil {
		t.Fatalf("matching version rejected: %v", err)
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
