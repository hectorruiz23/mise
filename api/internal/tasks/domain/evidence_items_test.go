package domain

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func textPtr(value string) *string {
	return &value
}

func evidenceItems(count int) []EvidenceItem {
	items := make([]EvidenceItem, count)
	for index := range items {
		items[index] = EvidenceItem{
			Label: textPtr(fmt.Sprintf("label %d", index+1)),
			Raw:   fmt.Sprintf("evidence %d", index+1),
		}
	}
	return items
}

func TestValidateEvidenceItems(t *testing.T) {
	tests := []struct {
		name  string
		items []EvidenceItem
		want  string
	}{
		{name: "nil", items: nil, want: CodeInvalidInput},
		{name: "empty", items: []EvidenceItem{}, want: CodeInvalidInput},
		{name: "one valid", items: evidenceItems(1)},
		{name: "twenty valid", items: evidenceItems(20)},
		{name: "twenty one", items: evidenceItems(21), want: CodeInvalidInput},
		{name: "missing label", items: []EvidenceItem{{Raw: "proof"}}, want: CodeInvalidInput},
		{name: "blank label", items: []EvidenceItem{{Label: textPtr("  "), Raw: "proof"}}, want: CodeInvalidInput},
		{name: "label at the limit", items: []EvidenceItem{{Label: textPtr(strings.Repeat("é", 120)), Raw: "proof"}}},
		{name: "label over the limit", items: []EvidenceItem{{Label: textPtr(strings.Repeat("é", 121)), Raw: "proof"}}, want: CodeInvalidInput},
		{name: "blank raw", items: []EvidenceItem{{Label: textPtr("tests"), Raw: "  "}}, want: CodeInvalidInput},
		{name: "raw at the limit", items: []EvidenceItem{{Label: textPtr("tests"), Raw: strings.Repeat("é", 4000)}}},
		{name: "raw over the limit", items: []EvidenceItem{{Label: textPtr("tests"), Raw: strings.Repeat("é", 4001)}}, want: CodeInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEvidenceItems(tt.items)
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

func TestApplyDeliveryItemsBuildsBothRepresentations(t *testing.T) {
	now := int64(1500)
	task := baseTask(InProgress)
	items := []EvidenceItem{
		{Label: textPtr("tests"), Raw: "go test ./..."},
		{Label: textPtr("build"), Raw: "make accept"},
	}

	next, delivery, movement, err := ApplyDeliveryItems(
		task, nil, actorOf(Human), "Done and verified", items, nil, now)
	if err != nil {
		t.Fatalf("human delivery: %v", err)
	}
	if next.State != Review || next.Version != task.Version+1 {
		t.Fatalf("task = %+v, want review at version %d", next, task.Version+1)
	}
	if !slices.Equal(delivery.Evidence, []string{"go test ./...", "make accept"}) {
		t.Errorf("evidence = %v", delivery.Evidence)
	}
	if len(delivery.EvidenceItems) != len(items) {
		t.Fatalf("evidence items = %+v", delivery.EvidenceItems)
	}
	for index, item := range delivery.EvidenceItems {
		if item.Label == nil || *item.Label != *items[index].Label || item.Raw != items[index].Raw {
			t.Errorf("evidence item %d = %+v, want %+v", index, item, items[index])
		}
	}
	if movement.FromState == nil || *movement.FromState != InProgress || movement.ToState != Review {
		t.Errorf("movement = %+v, want in_progress -> review", movement)
	}

	items[0].Raw = "mutated after the call"
	*items[1].Label = "mutated"
	if delivery.Evidence[0] != "go test ./..." || *delivery.EvidenceItems[1].Label != "build" {
		t.Error("Delivery aliases the caller's evidence items")
	}
}
