package githubinfo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyMerge(t *testing.T) {
	const repository = "owner/project"
	const pullURL = "https://github.com/owner/project/pull/15"
	headSHA := strings.Repeat("a", 40)
	mergeSHA := strings.Repeat("b", 40)
	merged := time.Unix(1700000001, 0).UTC()
	success := "success"
	failure := "failure"
	cases := []struct {
		name         string
		changePull   func(*pullResponse)
		changeRun    func(*workflowRun)
		addFailedRun bool
		wantProof    bool
	}{
		{name: "verified", wantProof: true},
		{name: "not merged", changePull: func(p *pullResponse) { p.Merged = false }},
		{name: "merged before approval", changePull: func(p *pullResponse) { earlier := merged.Add(-time.Minute); p.MergedAt = &earlier }},
		{name: "another repository", changePull: func(p *pullResponse) { p.Base.Repo.FullName = "other/project" }},
		{name: "another base", changePull: func(p *pullResponse) { p.Base.Ref = "staging" }},
		{name: "head changed", changePull: func(p *pullResponse) { p.Head.SHA = mergeSHA }},
		{name: "abbreviated merge", changePull: func(p *pullResponse) { short := mergeSHA[:7]; p.MergeCommitSHA = &short }},
		{name: "failed push", changeRun: func(r *workflowRun) { r.Conclusion = &failure }},
		{name: "incomplete run", changeRun: func(r *workflowRun) { r.Status = "in_progress" }},
		{name: "wrong workflow", changeRun: func(r *workflowRun) { r.WorkflowID++ }},
		{name: "checked only PR", changeRun: func(r *workflowRun) { r.Event = ContextPullRequest }},
		{name: "checked wrong commit", changeRun: func(r *workflowRun) { r.HeadSHA = headSHA }},
		{name: "checked wrong branch", changeRun: func(r *workflowRun) { r.HeadBranch = "staging" }},
		{name: "run on fork", changeRun: func(r *workflowRun) { r.Repository.FullName = "other/project" }},
		{name: "newer run failed", addFailedRun: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := workflowRun{ID: 456, WorkflowID: 17, Repository: repositoryIdentity{FullName: repository},
				Event: ContextPush, HeadBranch: "main", HeadSHA: mergeSHA, Status: "completed", Conclusion: &success, RunAttempt: 2}
			if tc.changeRun != nil {
				tc.changeRun(&run)
			}
			var pull pullResponse
			pull.Merged, pull.MergedAt, pull.MergeCommitSHA = true, &merged, &mergeSHA
			pull.Base.Ref, pull.Base.Repo.FullName, pull.Head.SHA = "main", repository, headSHA
			if tc.changePull != nil {
				tc.changePull(&pull)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("verification request missing credential")
				}
				switch r.URL.Path {
				case "/repos/owner/project/pulls/15":
					_ = json.NewEncoder(w).Encode(pull)
				case "/repos/owner/project/actions/workflows/17/runs":
					query := r.URL.Query()
					if query.Get("head_sha") != mergeSHA || query.Get("branch") != "main" || query.Get("event") != "push" {
						t.Errorf("wrong filters: %s", r.URL.RawQuery)
					}
					runs := []workflowRun{run}
					if tc.addFailedRun {
						later := run
						later.ID++
						later.Conclusion = &failure
						runs = append(runs, later)
					}
					writeJSON(w, runsBody(len(runs), runs))
				default:
					t.Errorf("unexpected URL: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := New(Options{BaseURL: server.URL, VerificationToken: "test-token"})
			proof, err := client.VerifyMerge(context.Background(), repository, "main", 17, pullURL, headSHA, merged.Unix())
			if err != nil || (proof != nil) != tc.wantProof {
				t.Fatalf("proof = %+v, err = %v", proof, err)
			}
			if proof != nil && *proof != (MergeProof{RunID: 456, Attempt: 2, MergeSHA: mergeSHA}) {
				t.Fatalf("wrong proof: %+v", proof)
			}
		})
	}
}

func TestVerificationDoesNotReuseStaleProof(t *testing.T) {
	client, _ := newClient(t, &testClock{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	proof, err := client.VerifyMerge(context.Background(), "owner/project", "main", 42,
		"https://github.com/owner/project/pull/4", strings.Repeat("a", 40), 1700000000)
	if proof != nil || err == nil {
		t.Fatalf("proof = %+v, err = %v", proof, err)
	}
}
