package main

import (
	"testing"

	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func TestGitHubDestinationsAreScopedToProjects(t *testing.T) {
	f := newIntegrationFixture(t)
	_, err := f.service.CreateProject(f.ctx, app.CreateProjectParams{Key: "NW", Name: "Second", Actor: f.owner})
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	checks := []string{"api"}
	for key, repo := range map[string]string{"MI": "example/project", "NW": "other/service"} {
		policy := app.PlanPolicy{Repository: repo, Branch: "release", WorkflowID: 92}
		if err := f.service.UpdateProjectSettings(f.ctx, f.owner, key, app.ProjectSettings{GitHub: &policy, AutoMerge: &enabled, RequiredChecks: &checks}); err != nil {
			t.Fatal(err)
		}
	}
	f.github.baseRepo = "other/service"
	f.github.base = "release"
	for _, example := range []struct {
		project string
		want    domain.State
	}{{"MI", domain.Review}, {"NW", domain.Done}} {
		task, _, err := f.service.Create(f.ctx, app.CreateParams{ProjectKey: example.project, Title: "Deliver change", Actor: f.owner, Priority: domain.Normal})
		if err != nil {
			t.Fatal(err)
		}
		f.deliverAsAgent(t, task.Key, "https://github.com/other/service/pull/55")
		f.poll(t)
		if got := f.state(t, task.Key); got != example.want {
			t.Fatalf("%s state = %s, want %s", task.Key, got, example.want)
		}
	}
}
