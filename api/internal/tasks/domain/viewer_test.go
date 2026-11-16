package domain

import (
	"errors"
	"testing"
)

func viewerActor() Actor {
	return Actor{ID: 0, Kind: Viewer, Name: "Visitor"}
}

func requireMessage(t *testing.T, err error, want string) {
	t.Helper()
	var rule *RuleError
	if !errors.As(err, &rule) {
		t.Fatalf("error = %v, want *RuleError", err)
	}
	if rule.Msg != want {
		t.Errorf("message = %q, want %q", rule.Msg, want)
	}
}

func TestViewerCannotMove(t *testing.T) {
	for _, from := range allStates() {
		for _, to := range allStates() {
			err := ValidateMove(baseTask(from), to, viewerActor(), "a reason", true)
			requireCode(t, err, CodeForbidden)
			requireMessage(t, err, "viewers cannot modify the board")

			_, _, err = ApplyMove(baseTask(from), to, viewerActor(), "a reason", true, 1000)
			requireCode(t, err, CodeForbidden)
			requireMessage(t, err, "viewers cannot modify the board")
		}
	}
}

func TestViewerCannotDeliver(t *testing.T) {
	lease := &Lease{ID: 3, TaskKey: "MI-1", AgentID: 7}
	cases := []struct {
		name     string
		lease    *Lease
		summary  string
		evidence []string
	}{
		{name: "valid input", lease: lease, summary: "Done", evidence: []string{"go test ./..."}},
		{name: "without lease", summary: "Done", evidence: []string{"go test ./..."}},
		{name: "empty summary", lease: lease, evidence: []string{"go test ./..."}},
		{name: "empty evidence", lease: lease, summary: "Done"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDelivery(baseTask(InProgress), tt.lease, viewerActor(), tt.summary, tt.evidence)
			requireCode(t, err, CodeForbidden)
			requireMessage(t, err, "viewers cannot modify the board")

			_, _, _, err = ApplyDelivery(baseTask(InProgress), tt.lease, viewerActor(),
				tt.summary, tt.evidence, nil, 1000)
			requireCode(t, err, CodeForbidden)
			requireMessage(t, err, "viewers cannot modify the board")
		})
	}
}

func TestMoveRulesKeepHumanAndAgentPaths(t *testing.T) {
	if err := ValidateMove(baseTask(Ready), InProgress, actorOf(Human), "", false); err != nil {
		t.Fatalf("human ready to in_progress: %v", err)
	}
	if err := ValidateMove(baseTask(Review), Done, actorOf(Human), "", true); err != nil {
		t.Fatalf("human review to done: %v", err)
	}
	if err := ValidateMove(baseTask(Review), Done, actorOf(Agent), "", true); err == nil {
		t.Fatal("agent review to done was allowed")
	} else {
		requireCode(t, err, CodeForbidden)
		requireMessage(t, err, "only a human may move a task from review to done")
	}
	if err := ValidateMove(baseTask(Backlog), InProgress, actorOf(Agent), "", false); err == nil {
		t.Fatal("agent backlog to in_progress was allowed")
	} else {
		requireCode(t, err, CodeInvalidTransition)
	}
	if err := ValidateMove(baseTask(Blocked), Ready, actorOf(Human), "", false); err == nil {
		t.Fatal("leaving blocked without a reason was allowed")
	} else {
		requireCode(t, err, CodeReasonRequired)
	}
}

func TestDeliveryRulesKeepHumanAndAgentPaths(t *testing.T) {
	lease := &Lease{ID: 3, TaskKey: "MI-1", AgentID: 7, ExpiresAt: 2000}

	if err := ValidateDelivery(baseTask(InProgress), nil, actorOf(Human), "Done", []string{"go test ./..."}); err != nil {
		t.Fatalf("human delivery: %v", err)
	}
	if err := ValidateDelivery(baseTask(InProgress), lease, actorOf(Agent), "Done", []string{"go test ./..."}); err != nil {
		t.Fatalf("agent delivery: %v", err)
	}
	if err := ValidateDelivery(baseTask(InProgress), nil, actorOf(Agent), "Done", []string{"go test ./..."}); err == nil {
		t.Fatal("agent delivery without a lease was allowed")
	} else {
		requireCode(t, err, CodeLeaseRequired)
	}
	if err := ValidateDelivery(baseTask(Backlog), nil, actorOf(Human), "Done", []string{"go test ./..."}); err == nil {
		t.Fatal("delivery from backlog was allowed")
	} else {
		requireCode(t, err, CodeInvalidTransition)
	}
	if err := ValidateDelivery(baseTask(InProgress), nil, actorOf(Human), "", []string{"go test ./..."}); err == nil {
		t.Fatal("human delivery without a summary was allowed")
	} else {
		requireCode(t, err, CodeInvalidInput)
	}
}
