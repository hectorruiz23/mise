package githubinfo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now int64
}

func (c *testClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(value int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = value
}

func (c *testClock) Advance(seconds int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += seconds
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func newClient(t *testing.T, clock *testClock, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(Options{BaseURL: server.URL, Clock: clock}), server
}

func pullBody(state string, merged bool, mergeSHA string) string {
	merge := "null"
	if mergeSHA != "" {
		merge = fmt.Sprintf("%q", mergeSHA)
	}
	return fmt.Sprintf(`{"state":%q,"merged":%t,"merge_commit_sha":%s,"head":{"sha":"pr-sha","ref":"feature"}}`,
		state, merged, merge)
}

func runsBody(total int, runs []workflowRun) string {
	raw, err := json.Marshal(runsResponse{TotalCount: total, WorkflowRuns: runs})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func jobsBody(values map[string]string) string {
	names := []string{"api", "accept", "panel"}
	jobs := make([]workflowJob, 0, len(names))
	for _, name := range names {
		value, ok := values[name]
		if !ok {
			continue
		}
		job := workflowJob{
			Name:    name,
			Status:  "completed",
			HTMLURL: "https://github.com/hectorruiz23/mise/actions/runs/100/job/" + name,
		}
		if value == "in_progress" || value == "queued" {
			job.Status = value
		} else {
			conclusion := value
			job.Conclusion = &conclusion
		}
		jobs = append(jobs, job)
	}
	raw, err := json.Marshal(jobsResponse{TotalCount: len(jobs), Jobs: jobs})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestParsePullRequest(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
		ref Ref
	}{
		{"https://github.com/hectorruiz23/mise/pull/15", true, Ref{"hectorruiz23", "mise", 15}},
		{"https://github.com/hectorruiz23/mise/pull/15/files", true, Ref{"hectorruiz23", "mise", 15}},
		{"http://www.github.com/hectorruiz23/mise/pull/7", true, Ref{"hectorruiz23", "mise", 7}},
		{"https://github.com/hectorruiz23/mise/issues/15", false, Ref{}},
		{"https://example.com/hectorruiz23/mise/pull/15", false, Ref{}},
		{"https://github.com/hectorruiz23/mise/pull/nope", false, Ref{}},
		{"not a url", false, Ref{}},
	}
	for _, tc := range cases {
		ref, ok := ParsePullRequest(tc.url)
		if ok != tc.ok || ref != tc.ref {
			t.Errorf("ParsePullRequest(%q) = %+v, %t; want %+v, %t", tc.url, ref, ok, tc.ref, tc.ok)
		}
	}
}

func TestPullRequestStates(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		state    string
		mergeSHA *string
	}{
		{"open", pullBody("open", false, ""), "open", nil},
		{"closed", pullBody("closed", false, ""), "closed", nil},
		{"merged", pullBody("closed", true, "merge-sha"), "merged", stringPointer("merge-sha")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &testClock{now: 1_700_000_000}
			client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/repos/hectorruiz23/mise/pulls/15":
					writeJSON(w, tc.body)
				case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs":
					writeJSON(w, runsBody(1, []workflowRun{{
						ID: 100, HeadSHA: "pr-sha", HeadBranch: "feature",
						Event: ContextPullRequest, Status: "completed", RunAttempt: 1,
					}}))
				case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs/100/attempts/1/jobs":
					writeJSON(w, jobsBody(map[string]string{
						"api": "success", "accept": "success", "panel": "success",
					}))
				default:
					http.NotFound(w, r)
				}
			})

			state, merge, checks := client.PullRequest(context.Background(),
				Ref{Owner: "hectorruiz23", Repo: "mise", Number: 15})
			if state.Status != StatusFresh || state.Data != tc.state {
				t.Fatalf("state = %+v, want fresh %q", state, tc.state)
			}
			if state.CheckedAt == nil || *state.CheckedAt != 1_700_000_000 {
				t.Fatalf("state checked_at = %v", state.CheckedAt)
			}
			if state.ExpiresAt == nil || *state.ExpiresAt != 1_700_000_300 {
				t.Fatalf("state expires_at = %v", state.ExpiresAt)
			}
			if state.Reason != nil || state.RetryAt != nil {
				t.Fatalf("state reason/retry = %v/%v", state.Reason, state.RetryAt)
			}
			if tc.mergeSHA == nil {
				if merge.Data != nil {
					t.Fatalf("merge_sha = %q, want null", *merge.Data)
				}
			} else if merge.Data == nil || *merge.Data != *tc.mergeSHA {
				t.Fatalf("merge_sha = %v, want %q", merge.Data, *tc.mergeSHA)
			}
			if checks.Status != StatusFresh || checks.Data == nil || checks.Data.Result != ResultSuccess {
				t.Fatalf("checks = %+v, want fresh success", checks)
			}
		})
	}
}

func TestChecksPartialResults(t *testing.T) {
	cases := []struct {
		name      string
		jobs      map[string]string
		runStatus string
		result    string
		status    string
		jobCount  int
	}{
		{
			name:      "success",
			jobs:      map[string]string{"api": "success", "accept": "success", "panel": "success"},
			runStatus: "completed", result: ResultSuccess, status: StatusFresh, jobCount: 3,
		},
		{
			name:      "failure",
			jobs:      map[string]string{"api": "success", "accept": "failure", "panel": "success"},
			runStatus: "completed", result: ResultFailure, status: StatusFresh, jobCount: 3,
		},
		{
			name:      "pending",
			jobs:      map[string]string{"api": "in_progress", "accept": "success", "panel": "success"},
			runStatus: "in_progress", result: ResultPending, status: StatusFresh, jobCount: 3,
		},
		{
			name:      "incomplete",
			jobs:      map[string]string{"api": "success", "accept": "success"},
			runStatus: "completed", result: ResultUnknown, status: StatusUnknown, jobCount: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &testClock{now: 1_700_000_000}
			client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/repos/hectorruiz23/mise/pulls/15":
					writeJSON(w, pullBody("open", false, ""))
				case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs":
					writeJSON(w, runsBody(1, []workflowRun{{
						ID: 100, HeadSHA: "pr-sha", HeadBranch: "feature",
						Event: ContextPullRequest, Status: tc.runStatus, RunAttempt: 1,
					}}))
				case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs/100/attempts/1/jobs":
					writeJSON(w, jobsBody(tc.jobs))
				default:
					http.NotFound(w, r)
				}
			})

			_, _, checks := client.PullRequest(context.Background(),
				Ref{Owner: "hectorruiz23", Repo: "mise", Number: 15})
			if checks.Data == nil {
				t.Fatalf("checks data is nil: %+v", checks)
			}
			if checks.Data.Result != tc.result {
				t.Errorf("result = %q, want %q", checks.Data.Result, tc.result)
			}
			if checks.Status != tc.status {
				t.Errorf("status = %q, want %q", checks.Status, tc.status)
			}
			if len(checks.Data.Jobs) != tc.jobCount {
				t.Errorf("jobs = %d, want %d", len(checks.Data.Jobs), tc.jobCount)
			}
			if tc.result == ResultUnknown {
				if checks.Reason == nil || *checks.Reason != ReasonUnresolved {
					t.Errorf("unknown reason = %v, want unresolved", checks.Reason)
				}
			}
		})
	}
}

func TestChecksPaginateWorkflowRuns(t *testing.T) {
	clock := &testClock{now: 1_700_000_000}
	var runPages atomic.Int64
	client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/hectorruiz23/mise/pulls/15":
			writeJSON(w, pullBody("open", false, ""))
		case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs":
			runPages.Add(1)
			page := r.URL.Query().Get("page")
			if page == "1" {
				runs := make([]workflowRun, 0, 100)
				for index := 0; index < 100; index++ {
					runs = append(runs, workflowRun{
						ID: int64(index + 1), HeadSHA: "pr-sha", HeadBranch: "other",
						Event: ContextPush, Status: "completed", RunAttempt: 1,
					})
				}
				writeJSON(w, runsBody(101, runs))
				return
			}
			writeJSON(w, runsBody(101, []workflowRun{{
				ID: 9999, HeadSHA: "pr-sha", HeadBranch: "feature",
				Event: ContextPullRequest, Status: "completed", RunAttempt: 1,
			}}))
		case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs/9999/attempts/1/jobs":
			writeJSON(w, jobsBody(map[string]string{
				"api": "success", "accept": "success", "panel": "success",
			}))
		default:
			http.NotFound(w, r)
		}
	})

	_, _, checks := client.PullRequest(context.Background(),
		Ref{Owner: "hectorruiz23", Repo: "mise", Number: 15})
	if checks.Data == nil || checks.Data.RunID == nil || *checks.Data.RunID != 9999 {
		t.Fatalf("run_id = %+v, want 9999", checks.Data)
	}
	if got := runPages.Load(); got != 2 {
		t.Fatalf("run pages requested = %d, want 2", got)
	}
}

func TestCacheExpiresAfterFiveMinutes(t *testing.T) {
	clock := &testClock{now: 1_000}
	var requests atomic.Int64
	client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeJSON(w, `{"sha":"main-sha"}`)
	})

	first := client.Main(context.Background())
	if first.Status != StatusFresh || first.Data == nil || *first.Data != "main-sha" {
		t.Fatalf("first main = %+v", first)
	}
	if first.CheckedAt == nil || *first.CheckedAt != 1_000 ||
		first.ExpiresAt == nil || *first.ExpiresAt != 1_300 {
		t.Fatalf("first dates = %v/%v", first.CheckedAt, first.ExpiresAt)
	}

	clock.Set(1_299)
	second := client.Main(context.Background())
	if requests.Load() != 1 {
		t.Fatalf("requests before expiry = %d, want 1", requests.Load())
	}
	if second.CheckedAt == nil || *second.CheckedAt != 1_000 {
		t.Fatalf("cached checked_at = %v, want 1000", second.CheckedAt)
	}

	clock.Set(1_300)
	third := client.Main(context.Background())
	if requests.Load() != 2 {
		t.Fatalf("requests at expiry = %d, want 2", requests.Load())
	}
	if third.CheckedAt == nil || *third.CheckedAt != 1_300 {
		t.Fatalf("refreshed checked_at = %v, want 1300", third.CheckedAt)
	}
}

func TestExpiredDataIsServedStaleAfterFailure(t *testing.T) {
	clock := &testClock{now: 1_000}
	var failing atomic.Bool
	client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeJSON(w, `{"sha":"main-sha"}`)
	})

	first := client.Main(context.Background())
	if first.Status != StatusFresh {
		t.Fatalf("first status = %q", first.Status)
	}
	failing.Store(true)
	clock.Set(1_300)
	stale := client.Main(context.Background())
	if stale.Status != StatusStale {
		t.Fatalf("stale status = %q, want stale", stale.Status)
	}
	if stale.Data == nil || *stale.Data != "main-sha" {
		t.Fatalf("stale data = %v", stale.Data)
	}
	if stale.CheckedAt == nil || *stale.CheckedAt != 1_000 {
		t.Fatalf("stale checked_at = %v, want 1000", stale.CheckedAt)
	}
	if stale.Reason == nil || *stale.Reason != ReasonUpstreamError {
		t.Fatalf("stale reason = %v, want upstream_error", stale.Reason)
	}
}

func TestIdenticalRequestsAreGrouped(t *testing.T) {
	clock := &testClock{now: 1_000}
	var fetches atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	client := New(Options{
		BaseURL: "http://grouped.invalid",
		Clock:   clock,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			fetches.Add(1)
			close(started)
			<-release
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"sha":"main-sha"}`)),
			}, nil
		})},
	})

	results := make(chan Envelope[*string], 2)
	for index := 0; index < 2; index++ {
		go func() {
			results <- client.Main(context.Background())
		}()
	}
	<-started
	time.Sleep(50 * time.Millisecond)
	close(release)

	first := <-results
	second := <-results
	if fetches.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", fetches.Load())
	}
	if first.Data == nil || second.Data == nil {
		t.Fatalf("grouped results = %+v / %+v", first, second)
	}
}

func TestBudgetExhausted(t *testing.T) {
	clock := &testClock{now: 5_000}
	var requests atomic.Int64
	client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch {
		case r.URL.Path == "/repos/hectorruiz23/mise/actions/runs":
			writeJSON(w, runsBody(0, nil))
		case strings.Contains(r.URL.Path, "/pulls/"):
			writeJSON(w, pullBody("open", false, ""))
		default:
			http.NotFound(w, r)
		}
	})

	for index := 1; index <= 25; index++ {
		state, _, _ := client.PullRequest(context.Background(),
			Ref{Owner: "hectorruiz23", Repo: "mise", Number: index})
		if state.Status != StatusFresh {
			t.Fatalf("request %d state = %+v, want fresh", index, state)
		}
	}
	if got := requests.Load(); got != DefaultBudget {
		t.Fatalf("requests = %d, want %d", got, DefaultBudget)
	}

	state, _, checks := client.PullRequest(context.Background(),
		Ref{Owner: "hectorruiz23", Repo: "mise", Number: 26})
	if state.Status != StatusUnavailable || state.Reason == nil ||
		*state.Reason != ReasonBudgetExhausted {
		t.Fatalf("budget state = %+v", state)
	}
	if state.RetryAt == nil || *state.RetryAt != 5_000+3_600 {
		t.Fatalf("retry_at = %v, want %d", state.RetryAt, 8_600)
	}
	if checks.Status != StatusUnavailable || checks.Reason == nil ||
		*checks.Reason != ReasonUnresolved {
		t.Fatalf("dependent checks = %+v", checks)
	}
	if got := requests.Load(); got != DefaultBudget {
		t.Fatalf("requests after budget stopped = %d, want %d", got, DefaultBudget)
	}
}

func TestTimeoutReturnsUnavailable(t *testing.T) {
	clock := &testClock{now: 1_000}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client := New(Options{
		BaseURL:    server.URL,
		Clock:      clock,
		HTTPClient: &http.Client{Timeout: 40 * time.Millisecond},
	})

	observed := client.Main(context.Background())
	if observed.Status != StatusUnavailable || observed.Reason == nil ||
		*observed.Reason != ReasonTimeout {
		t.Fatalf("timeout observation = %+v", observed)
	}
	if observed.Data != nil || observed.CheckedAt != nil || observed.ExpiresAt != nil {
		t.Fatalf("timeout kept data or dates: %+v", observed)
	}
}

func TestRetryAfterRecovery(t *testing.T) {
	clock := &testClock{now: 2_000}
	var requests atomic.Int64
	client, _ := newClient(t, clock, func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(w, `{"sha":"main-sha"}`)
	})

	limited := client.Main(context.Background())
	if limited.Status != StatusUnavailable || limited.Reason == nil ||
		*limited.Reason != ReasonRateLimited {
		t.Fatalf("limited = %+v", limited)
	}
	if limited.RetryAt == nil || *limited.RetryAt != 2_030 {
		t.Fatalf("limited retry_at = %v, want 2030", limited.RetryAt)
	}

	clock.Set(2_010)
	stillLimited := client.Main(context.Background())
	if stillLimited.Reason == nil || *stillLimited.Reason != ReasonRateLimited {
		t.Fatalf("before retry_at = %+v", stillLimited)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests inside cooldown = %d, want 1", requests.Load())
	}

	clock.Set(2_030)
	recovered := client.Main(context.Background())
	if recovered.Status != StatusFresh || recovered.Data == nil || *recovered.Data != "main-sha" {
		t.Fatalf("recovered = %+v", recovered)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests after retry_at = %d, want 2", requests.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
