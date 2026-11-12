package domain

import (
	"errors"
	"testing"
)

func TestNewTask(t *testing.T) {
	task, err := NewTask("MI-1", "MI", "Write the domain", 7, 1000)
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}

	if task.Key != "MI-1" || task.ProjectKey != "MI" || task.Title != "Write the domain" {
		t.Errorf("unexpected identity fields: %+v", task)
	}
	if task.State != Backlog {
		t.Errorf("State = %q, want %q", task.State, Backlog)
	}
	if task.Priority != Normal {
		t.Errorf("Priority = %q, want %q", task.Priority, Normal)
	}
	if task.Version != 1 {
		t.Errorf("Version = %d, want 1", task.Version)
	}
	if task.CreatedAt != 1000 || task.UpdatedAt != 1000 {
		t.Errorf("timestamps = (%d, %d), want (1000, 1000)", task.CreatedAt, task.UpdatedAt)
	}
	if task.ClosedAt != nil {
		t.Errorf("ClosedAt = %v, want nil", task.ClosedAt)
	}
	if task.CreatedBy != 7 {
		t.Errorf("CreatedBy = %d, want 7", task.CreatedBy)
	}
}

func TestNewTaskRejectsBlankFields(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		projectKey string
		title      string
	}{
		{name: "blank key", key: "  ", projectKey: "MI", title: "Title"},
		{name: "blank project", key: "MI-1", projectKey: "", title: "Title"},
		{name: "blank title", key: "MI-1", projectKey: "MI", title: " \t "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTask(tt.key, tt.projectKey, tt.title, 7, 1000)
			requireCode(t, err, CodeInvalidInput)
		})
	}
}

func TestNewLease(t *testing.T) {
	lease, err := NewLease("MI-1", 7, 1000, 1900)
	if err != nil {
		t.Fatalf("NewLease: %v", err)
	}
	if lease.TaskKey != "MI-1" || lease.AgentID != 7 {
		t.Errorf("unexpected lease identity: %+v", lease)
	}
	if lease.CreatedAt != 1000 || lease.ExpiresAt != 1900 {
		t.Errorf("lease window = (%d, %d), want (1000, 1900)", lease.CreatedAt, lease.ExpiresAt)
	}
	if lease.ReleasedAt != nil || lease.ClosedReason != nil {
		t.Errorf("new lease should be open: %+v", lease)
	}

	if _, err := NewLease("MI-1", 7, 1000, 1000); err == nil {
		t.Fatal("NewLease accepted an expiry equal to its creation")
	} else {
		requireCode(t, err, CodeInvalidInput)
	}
	if _, err := NewLease(" ", 7, 1000, 1900); err == nil {
		t.Fatal("NewLease accepted a blank task key")
	} else {
		requireCode(t, err, CodeInvalidInput)
	}
}

func TestEnumValidity(t *testing.T) {
	for _, state := range []State{Backlog, Ready, InProgress, Blocked, Review, Done} {
		if !state.Valid() {
			t.Errorf("%q should be a valid state", state)
		}
	}
	if State("archived").Valid() {
		t.Error("unknown state reported valid")
	}

	for _, kind := range []ActorKind{Human, Agent} {
		if !kind.Valid() {
			t.Errorf("%q should be a valid actor kind", kind)
		}
	}
	if ActorKind("robot").Valid() {
		t.Error("unknown actor kind reported valid")
	}

	for _, priority := range []Priority{Low, Normal, High} {
		if !priority.Valid() {
			t.Errorf("%q should be a valid priority", priority)
		}
	}
	if Priority("urgent").Valid() {
		t.Error("unknown priority reported valid")
	}

	for _, size := range []Size{Small, Medium, Large} {
		if !size.Valid() {
			t.Errorf("%q should be a valid size", size)
		}
	}
	if Size("XL").Valid() {
		t.Error("unknown size reported valid")
	}
}

func TestRuleError(t *testing.T) {
	err := &RuleError{Code: CodeInvalidInput, Msg: "title is required"}
	if got, want := err.Error(), "invalid_input: title is required"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	bare := &RuleError{Code: CodeForbidden}
	if got, want := bare.Error(), "forbidden"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	var target *RuleError
	if !errors.As(error(err), &target) || target.Code != CodeInvalidInput {
		t.Error("RuleError does not survive errors.As")
	}
}
