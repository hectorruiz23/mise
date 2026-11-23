package domain_test

import (
	"testing"

	"mise/api/internal/tasks/domain"
)

func TestValidatePlanEdges(t *testing.T) {
	tasks := []string{"MI-1", "MI-2", "MI-3"}
	valid := []domain.PlanEdge{
		{From: "MI-1", To: "MI-3", Kind: domain.EdgeIntegration},
		{From: "MI-2", To: "MI-3", Kind: domain.EdgeIntegration},
	}
	if err := domain.ValidatePlanEdges(tasks, valid); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	cases := []struct {
		name  string
		tasks []string
		edges []domain.PlanEdge
	}{
		{"cycle", tasks, append(valid, domain.PlanEdge{From: "MI-3", To: "MI-1", Kind: domain.EdgeIntegration})},
		{"self", tasks, []domain.PlanEdge{{From: "MI-1", To: "MI-1", Kind: domain.EdgeIntegration}}},
		{"foreign", tasks, []domain.PlanEdge{{From: "MI-1", To: "MI-9", Kind: domain.EdgeIntegration}}},
		{"duplicate", tasks, append(valid, valid[0])},
		{"kind", tasks, []domain.PlanEdge{{From: "MI-1", To: "MI-3", Kind: "magic"}}},
		{"one task", []string{"MI-1"}, nil},
		{"repeated task", []string{"MI-1", "MI-1"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := domain.ValidatePlanEdges(tc.tasks, tc.edges); err == nil {
				t.Fatal("broken graph accepted")
			}
		})
	}
}
