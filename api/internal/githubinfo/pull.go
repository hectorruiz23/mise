package githubinfo

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ContextPullRequest = "pull_request"
	ContextPush        = "push"
	ContextUnknown     = "unknown"

	ResultSuccess = "success"
	ResultFailure = "failure"
	ResultPending = "pending"
	ResultUnknown = "unknown"
)

var requiredJobs = []string{"api", "accept", "panel"}

type Job struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	URL        *string `json:"url"`
}

type Checks struct {
	SHA     string `json:"sha"`
	Context string `json:"context"`
	RunID   *int64 `json:"run_id"`
	Attempt *int   `json:"attempt"`
	Jobs    []Job  `json:"jobs"`
	Result  string `json:"result"`
}

type pullMeta struct {
	State    string
	MergeSHA *string
	HeadSHA  string
	HeadRef  string
}

type repositoryIdentity struct {
	FullName string `json:"full_name"`
}

type pullResponse struct {
	MergedAt *time.Time `json:"merged_at"`
	Base     struct {
		Ref  string             `json:"ref"`
		Repo repositoryIdentity `json:"repo"`
	} `json:"base"`
	State          string  `json:"state"`
	Merged         bool    `json:"merged"`
	MergeCommitSHA *string `json:"merge_commit_sha"`
	Head           struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

type runsResponse struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowRun struct {
	WorkflowID int64              `json:"workflow_id"`
	Repository repositoryIdentity `json:"repository"`
	ID         int64              `json:"id"`
	HeadSHA    string             `json:"head_sha"`
	HeadBranch string             `json:"head_branch"`
	Event      string             `json:"event"`
	Status     string             `json:"status"`
	Conclusion *string            `json:"conclusion"`
	RunAttempt int                `json:"run_attempt"`
}

type jobsResponse struct {
	TotalCount int           `json:"total_count"`
	Jobs       []workflowJob `json:"jobs"`
}

type workflowJob struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
	HTMLURL    string  `json:"html_url"`
}

func (c *Client) Main(ctx context.Context) Envelope[*string] {
	key := "main:" + c.repository
	return observe[*string](c, ctx, key, func(ctx context.Context) (*string, error) {
		var response struct {
			SHA string `json:"sha"`
		}
		path := repositoryPathParts(c.repository) + "/commits/main"
		if err := c.get(ctx, path, &response); err != nil {
			return nil, err
		}
		if strings.TrimSpace(response.SHA) == "" {
			return nil, &fetchError{reason: ReasonUpstreamError}
		}
		sha := response.SHA
		return &sha, nil
	})
}

func (c *Client) PullRequest(
	ctx context.Context,
	ref Ref,
) (Envelope[string], Envelope[*string], Envelope[*Checks]) {
	metaKey := fmt.Sprintf("pull:%s/%s#%d", ref.Owner, ref.Repo, ref.Number)
	meta := observe[pullMeta](c, ctx, metaKey, func(ctx context.Context) (pullMeta, error) {
		return c.fetchPullRequest(ctx, ref)
	})

	state := mapEnvelope(meta, func(value pullMeta) string { return value.State })
	mergeSHA := mapEnvelope(meta, func(value pullMeta) *string { return value.MergeSHA })
	checks := c.checks(ctx, ref, meta)
	return state, mergeSHA, checks
}

func (c *Client) fetchPullRequest(ctx context.Context, ref Ref) (pullMeta, error) {
	var response pullResponse
	path := repositoryPath(ref.Owner, ref.Repo) + "/pulls/" + strconv.Itoa(ref.Number)
	if err := c.get(ctx, path, &response); err != nil {
		return pullMeta{}, err
	}
	state := strings.TrimSpace(response.State)
	if state == "" {
		return pullMeta{}, &fetchError{reason: ReasonUpstreamError}
	}
	if response.Merged {
		state = "merged"
	}
	var mergeSHA *string
	if response.Merged && response.MergeCommitSHA != nil && strings.TrimSpace(*response.MergeCommitSHA) != "" {
		value := *response.MergeCommitSHA
		mergeSHA = &value
	}
	return pullMeta{
		State:    state,
		MergeSHA: mergeSHA,
		HeadSHA:  response.Head.SHA,
		HeadRef:  response.Head.Ref,
	}, nil
}

func (c *Client) checks(ctx context.Context, ref Ref, meta Envelope[pullMeta]) Envelope[*Checks] {
	if meta.Status == StatusUnavailable {
		return Envelope[*Checks]{Status: StatusUnavailable, Reason: stringPointer(ReasonUnresolved)}
	}
	headSHA := meta.Data.HeadSHA
	if strings.TrimSpace(headSHA) == "" {
		return Envelope[*Checks]{Status: StatusUnavailable, Reason: stringPointer(ReasonUnresolved)}
	}
	refName := meta.Data.HeadRef

	key := fmt.Sprintf("checks:%s/%s#%d:%s", ref.Owner, ref.Repo, ref.Number, headSHA)
	observed := observe[*Checks](c, ctx, key, func(ctx context.Context) (*Checks, error) {
		runs, err := c.listRuns(ctx, ref, headSHA)
		if err != nil {
			return nil, err
		}
		run, kind, found := selectRun(runs, headSHA, refName)
		if !found {
			return &Checks{
				SHA:     headSHA,
				Context: ContextUnknown,
				Jobs:    []Job{},
				Result:  ResultUnknown,
			}, nil
		}
		jobs, err := c.listJobs(ctx, ref, run.ID, run.RunAttempt)
		if err != nil {
			return nil, err
		}
		runID := run.ID
		attempt := run.RunAttempt
		return &Checks{
			SHA:     headSHA,
			Context: kind,
			RunID:   &runID,
			Attempt: &attempt,
			Jobs:    relevantJobs(jobs),
			Result:  checkResult(run, jobs),
		}, nil
	})

	if observed.Status == StatusFresh && observed.Data != nil && observed.Data.Result == ResultUnknown {
		observed.Status = StatusUnknown
		observed.Reason = stringPointer(ReasonUnresolved)
	}
	return observed
}

func (c *Client) PullTarget(ctx context.Context, ref Ref) (Target, error) {
	var response pullResponse
	path := repositoryPath(ref.Owner, ref.Repo) + "/pulls/" + strconv.Itoa(ref.Number)
	if err := c.get(ctx, path, &response); err != nil {
		return Target{}, err
	}
	repository := strings.TrimSpace(response.Base.Repo.FullName)
	branch := strings.TrimSpace(response.Base.Ref)
	if repository == "" || branch == "" {
		return Target{}, &fetchError{reason: ReasonUpstreamError}
	}
	return Target{Repository: repository, Base: branch}, nil
}

func (c *Client) listRuns(ctx context.Context, ref Ref, sha string) ([]workflowRun, error) {
	base := repositoryPath(ref.Owner, ref.Repo) + "/actions/runs"
	runs := make([]workflowRun, 0)
	for page := 1; page <= 100; page++ {
		var response runsResponse
		path := base + "?head_sha=" + url.QueryEscape(sha) +
			"&per_page=100&page=" + strconv.Itoa(page)
		if err := c.get(ctx, path, &response); err != nil {
			return nil, err
		}
		runs = append(runs, response.WorkflowRuns...)
		if len(response.WorkflowRuns) < 100 || page*100 >= response.TotalCount {
			break
		}
	}
	return runs, nil
}

func (c *Client) listJobs(ctx context.Context, ref Ref, runID int64, attempt int) ([]workflowJob, error) {
	base := repositoryPath(ref.Owner, ref.Repo) + "/actions/runs/" +
		strconv.FormatInt(runID, 10) + "/attempts/" + strconv.Itoa(attempt) + "/jobs"
	jobs := make([]workflowJob, 0)
	for page := 1; page <= 100; page++ {
		var response jobsResponse
		path := base + "?per_page=100&page=" + strconv.Itoa(page)
		if err := c.get(ctx, path, &response); err != nil {
			return nil, err
		}
		jobs = append(jobs, response.Jobs...)
		if len(response.Jobs) < 100 || page*100 >= response.TotalCount {
			break
		}
	}
	return jobs, nil
}

func selectRun(runs []workflowRun, sha, ref string) (workflowRun, string, bool) {
	var pull workflowRun
	foundPull := false
	for _, run := range runs {
		if run.HeadSHA != sha || run.Event != ContextPullRequest {
			continue
		}
		if !foundPull || run.ID > pull.ID {
			pull = run
			foundPull = true
		}
	}
	if foundPull {
		return pull, ContextPullRequest, true
	}

	var push workflowRun
	foundPush := false
	for _, run := range runs {
		if run.HeadSHA != sha || run.Event != ContextPush || run.HeadBranch != ref {
			continue
		}
		if !foundPush || run.ID > push.ID {
			push = run
			foundPush = true
		}
	}
	if foundPush {
		return push, ContextPush, true
	}
	return workflowRun{}, ContextUnknown, false
}

func relevantJobs(jobs []workflowJob) []Job {
	byName := make(map[string]workflowJob, len(jobs))
	for _, job := range jobs {
		name := strings.ToLower(strings.TrimSpace(job.Name))
		if _, exists := byName[name]; !exists {
			byName[name] = job
		}
	}
	result := make([]Job, 0, len(requiredJobs))
	for _, name := range requiredJobs {
		job, ok := byName[name]
		if !ok {
			continue
		}
		item := Job{
			Name:   job.Name,
			Status: job.Status,
		}
		if job.Conclusion != nil && *job.Conclusion != "" {
			value := *job.Conclusion
			item.Conclusion = &value
		}
		if job.HTMLURL != "" {
			value := job.HTMLURL
			item.URL = &value
		}
		result = append(result, item)
	}
	return result
}

func checkResult(run workflowRun, jobs []workflowJob) string {
	byName := make(map[string]workflowJob, len(jobs))
	for _, job := range jobs {
		name := strings.ToLower(strings.TrimSpace(job.Name))
		if _, exists := byName[name]; !exists {
			byName[name] = job
		}
	}

	present := 0
	pending := false
	failure := false
	for _, name := range requiredJobs {
		job, ok := byName[name]
		if !ok {
			continue
		}
		present++
		if job.Status != "completed" {
			pending = true
			continue
		}
		if job.Conclusion == nil || *job.Conclusion != ResultSuccess {
			failure = true
		}
	}

	if failure {
		return ResultFailure
	}
	if present == len(requiredJobs) && !pending && run.Status == "completed" {
		return ResultSuccess
	}
	if pending || run.Status != "completed" {
		return ResultPending
	}
	return ResultUnknown
}

func repositoryPathParts(repository string) string {
	parts := strings.SplitN(repository, "/", 2)
	if len(parts) != 2 {
		return repositoryPath(repository, "")
	}
	return repositoryPath(parts[0], parts[1])
}

func stringPointer(value string) *string {
	return &value
}
