package githubinfo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	MergeMethodSquash = "squash"

	ReasonMergeRefused = "merge_refused"
)

var ErrMergeTokenMissing = errors.New("merge token is not configured")

type MergeRefusal struct {
	Status  int
	Message string
}

func (e *MergeRefusal) Error() string {
	return strconv.Itoa(e.Status) + ": " + e.Message
}

func (c *Client) Merge(ctx context.Context, ref Ref) (string, error) {
	if c.mergeToken == "" {
		return "", ErrMergeTokenMissing
	}
	body, err := json.Marshal(struct {
		Method string `json:"merge_method"`
	}{Method: MergeMethodSquash})
	if err != nil {
		return "", &fetchError{reason: ReasonUpstreamError}
	}
	path := repositoryPath(ref.Owner, ref.Repo) + "/pulls/" + strconv.Itoa(ref.Number) + "/merge"
	response, err := c.send(ctx, http.MethodPut, path, c.mergeToken, body)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&failure)
		message := strings.TrimSpace(failure.Message)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return "", &MergeRefusal{Status: response.StatusCode, Message: message}
	}

	var merged struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&merged); err != nil {
		return "", &fetchError{reason: ReasonUpstreamError}
	}
	sha := strings.ToLower(strings.TrimSpace(merged.SHA))
	if !merged.Merged || !fullCommitSHA(sha) {
		return "", &fetchError{reason: ReasonUpstreamError}
	}
	return sha, nil
}

func (c *Client) PullFiles(ctx context.Context, ref Ref) ([]string, error) {
	token := c.mergeToken
	if token == "" {
		token = c.verificationToken
	}
	base := repositoryPath(ref.Owner, ref.Repo) + "/pulls/" + strconv.Itoa(ref.Number) + "/files"
	files := make([]string, 0)
	for page := 1; page <= 30; page++ {
		var response []struct {
			Filename string `json:"filename"`
		}
		path := base + "?per_page=100&page=" + strconv.Itoa(page)
		if err := c.getWithToken(ctx, path, &response, token); err != nil {
			return nil, err
		}
		for _, file := range response {
			files = append(files, file.Filename)
		}
		if len(response) < 100 {
			break
		}
	}
	return files, nil
}

func RefusalOf(err error) (*MergeRefusal, bool) {
	var refusal *MergeRefusal
	if errors.As(err, &refusal) {
		return refusal, true
	}
	return nil, false
}
