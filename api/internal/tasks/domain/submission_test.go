package domain_test

import (
	"testing"

	"mise/api/internal/tasks/domain"
)

func TestEvaluateChecks(t *testing.T) {
	required := []string{"build", "unit"}
	cases := []struct {
		name     string
		observed []domain.Check
		want     string
	}{
		{
			name: "all green",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "success"},
				{Name: "unit", Status: "completed", Conclusion: "success"},
			},
			want: domain.VerdictPassed,
		},
		{
			name: "three reds are one failure",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "failure"},
				{Name: "unit", Status: "completed", Conclusion: "failure"},
				{Name: "lint", Status: "completed", Conclusion: "failure"},
			},
			want: domain.VerdictFailed,
		},
		{
			name: "a failure wins over a pending check",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "failure"},
				{Name: "unit", Status: "in_progress"},
			},
			want: domain.VerdictFailed,
		},
		{
			name: "a cancelled runner is not a verdict",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "cancelled"},
				{Name: "unit", Status: "completed", Conclusion: "success"},
			},
			want: domain.VerdictOpen,
		},
		{
			name: "a timeout is not a verdict",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "timed_out"},
				{Name: "unit", Status: "completed", Conclusion: "success"},
			},
			want: domain.VerdictOpen,
		},
		{
			name: "a running check holds the verdict",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "success"},
				{Name: "unit", Status: "in_progress"},
			},
			want: domain.VerdictOpen,
		},
		{
			name: "a required check that never ran holds the verdict",
			observed: []domain.Check{
				{Name: "unit", Status: "completed", Conclusion: "success"},
			},
			want: domain.VerdictOpen,
		},
		{
			name: "a skipped check counts as passed",
			observed: []domain.Check{
				{Name: "build", Status: "completed", Conclusion: "skipped"},
				{Name: "unit", Status: "completed", Conclusion: "success"},
			},
			want: domain.VerdictPassed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verdict, checks := domain.EvaluateChecks(required, tc.observed)
			if verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", verdict, tc.want)
			}
			if len(checks) != len(required) {
				t.Fatalf("checks = %d, want one per required name", len(checks))
			}
		})
	}
}

func TestRepositoryOf(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://github.com/owner/project/pull/12", "owner/project"},
		{"https://github.com/owner/project", "owner/project"},
		{"https://gitlab.com/owner/project/-/merge_requests/1", ""},
		{"not a url", ""},
		{"https://github.com/owner", ""},
	}
	for _, tc := range cases {
		got, ok := domain.RepositoryOf(tc.url)
		if tc.want == "" {
			if ok {
				t.Fatalf("RepositoryOf(%q) = %q, want no match", tc.url, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Fatalf("RepositoryOf(%q) = %q, %v; want %q", tc.url, got, ok, tc.want)
		}
	}
}
