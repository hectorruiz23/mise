package githubinfo

import (
	"context"
	"strconv"
	"time"
)

type Review struct {
	State       string
	CommitID    string
	User        string
	SubmittedAt time.Time
}

func (c *Client) Reviews(ctx context.Context, ref Ref) ([]Review, error) {
	token := c.mergeToken
	if token == "" {
		token = c.verificationToken
	}
	base := repositoryPath(ref.Owner, ref.Repo) + "/pulls/" + strconv.Itoa(ref.Number) + "/reviews"
	reviews := make([]Review, 0)
	for page := 1; page <= 20; page++ {
		var response []struct {
			State       string    `json:"state"`
			CommitID    string    `json:"commit_id"`
			SubmittedAt time.Time `json:"submitted_at"`
			User        struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		path := base + "?per_page=100&page=" + strconv.Itoa(page)
		if err := c.getWithToken(ctx, path, &response, token); err != nil {
			return nil, err
		}
		for _, item := range response {
			reviews = append(reviews, Review{
				State:       item.State,
				CommitID:    item.CommitID,
				User:        item.User.Login,
				SubmittedAt: item.SubmittedAt,
			})
		}
		if len(response) < 100 {
			break
		}
	}
	return reviews, nil
}
