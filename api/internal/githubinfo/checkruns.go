package githubinfo

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type CheckRun struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
}

type checkRunsResponse struct {
	TotalCount int `json:"total_count"`
	CheckRuns  []struct {
		Name       string  `json:"name"`
		Status     string  `json:"status"`
		Conclusion *string `json:"conclusion"`
		HeadSHA    string  `json:"head_sha"`
		HTMLURL    string  `json:"html_url"`
	} `json:"check_runs"`
}

func (c *Client) CheckRuns(ctx context.Context, repository, revision string) ([]CheckRun, error) {
	if strings.TrimSpace(repository) == "" || strings.TrimSpace(revision) == "" {
		return nil, errors.New("githubinfo: repository and revision are required")
	}
	runs := make([]CheckRun, 0)
	for page := 1; page <= 100; page++ {
		path := "/repos/" + repository + "/commits/" + url.PathEscape(revision) +
			"/check-runs?per_page=100&page=" + strconv.Itoa(page)
		var response checkRunsResponse
		if err := c.getWithToken(ctx, path, &response, c.verificationToken); err != nil {
			return nil, err
		}
		for _, run := range response.CheckRuns {
			if !strings.EqualFold(run.HeadSHA, revision) {
				continue
			}
			conclusion := ""
			if run.Conclusion != nil {
				conclusion = *run.Conclusion
			}
			runs = append(runs, CheckRun{
				Name: run.Name, Status: run.Status, Conclusion: conclusion, URL: run.HTMLURL,
			})
		}
		if len(response.CheckRuns) < 100 || page*100 >= response.TotalCount {
			break
		}
	}
	return runs, nil
}
