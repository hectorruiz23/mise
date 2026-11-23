package githubinfo

import (
	"context"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
)

type MergeProof struct {
	RunID    int64
	Attempt  int64
	MergeSHA string
}

func fullCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (c *Client) VerifyMerge(ctx context.Context, repository, baseBranch string, workflowID int64, pullURL, headSHA string, approvedAt int64) (*MergeProof, error) {
	ref, ok := ParsePullRequest(pullURL)
	if !ok || !strings.EqualFold(ref.Owner+"/"+ref.Repo, repository) ||
		baseBranch == "" || workflowID <= 0 || !fullCommitSHA(headSHA) || approvedAt <= 0 {
		return nil, nil
	}

	base := repositoryPath(ref.Owner, ref.Repo)
	var pull pullResponse
	if err := c.getWithToken(ctx, base+"/pulls/"+strconv.Itoa(ref.Number), &pull, c.verificationToken); err != nil {
		return nil, err
	}
	if !pull.Merged || pull.MergedAt == nil || pull.MergedAt.Unix() < approvedAt ||
		!strings.EqualFold(pull.Head.SHA, headSHA) || pull.Base.Ref != baseBranch ||
		!strings.EqualFold(pull.Base.Repo.FullName, repository) ||
		pull.MergeCommitSHA == nil || !fullCommitSHA(*pull.MergeCommitSHA) {
		return nil, nil
	}

	mergeSHA := *pull.MergeCommitSHA
	query := url.Values{
		"head_sha": {mergeSHA},
		"branch":   {baseBranch},
		"event":    {ContextPush},
		"per_page": {"100"},
	}
	path := base + "/actions/workflows/" + strconv.FormatInt(workflowID, 10) + "/runs?"
	var latest workflowRun
	for page := 1; ; page++ {
		query.Set("page", strconv.Itoa(page))
		var response runsResponse
		if err := c.getWithToken(ctx, path+query.Encode(), &response, c.verificationToken); err != nil {
			return nil, err
		}
		for _, run := range response.WorkflowRuns {
			if run.WorkflowID == workflowID && run.Event == ContextPush && run.HeadBranch == baseBranch &&
				strings.EqualFold(run.HeadSHA, mergeSHA) && strings.EqualFold(run.Repository.FullName, repository) &&
				run.ID > latest.ID {
				latest = run
			}
		}
		if len(response.WorkflowRuns) < 100 || page*100 >= response.TotalCount {
			break
		}
	}
	if latest.ID <= 0 || latest.RunAttempt <= 0 || latest.Status != "completed" ||
		latest.Conclusion == nil || *latest.Conclusion != ResultSuccess {
		return nil, nil
	}
	return &MergeProof{RunID: latest.ID, Attempt: int64(latest.RunAttempt), MergeSHA: mergeSHA}, nil
}
