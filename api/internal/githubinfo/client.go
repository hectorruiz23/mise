package githubinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL    = "https://api.github.com"
	DefaultRepository = "hectorruiz23/mise"
	DefaultBudget     = 50
	DefaultWindow     = time.Hour
	DefaultTTL        = 5 * time.Minute
	DefaultTimeout    = 5 * time.Second

	ReasonTimeout         = "timeout"
	ReasonRateLimited     = "rate_limited"
	ReasonBudgetExhausted = "budget_exhausted"
	ReasonNotFound        = "not_found"
	ReasonUpstreamError   = "upstream_error"
	ReasonUnresolved      = "unresolved"
)

type Clock interface {
	Now() int64
}

type Options struct {
	BaseURL    string
	Repository string
	HTTPClient *http.Client
	Clock      Clock
	Budget     int
	Window     time.Duration
	TTL        time.Duration
	Timeout    time.Duration
}

type Client struct {
	baseURL     string
	repository  string
	httpClient  *http.Client
	clock       Clock
	budgetLimit int
	window      int64
	ttl         int64

	mu           sync.Mutex
	budgetTimes  []int64
	blockedUntil int64
	cache        map[string]cacheEntry
	inflight     map[string]*call
}

type cacheEntry struct {
	data      any
	checkedAt int64
	expiresAt int64
}

type call struct {
	done  chan struct{}
	value any
	err   error
}

type fetchError struct {
	reason  string
	retryAt *int64
}

func (e *fetchError) Error() string {
	return e.reason
}

type systemClock struct{}

func (systemClock) Now() int64 {
	return time.Now().Unix()
}

func New(options Options) *Client {
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	repository := strings.TrimSpace(options.Repository)
	if repository == "" {
		repository = DefaultRepository
	}
	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	budget := options.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	window := options.Window
	if window <= 0 {
		window = DefaultWindow
	}
	ttl := options.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	ttlSeconds := int64(ttl / time.Second)
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}
	windowSeconds := int64(window / time.Second)
	if windowSeconds < 1 {
		windowSeconds = 1
	}

	return &Client{
		baseURL:     baseURL,
		repository:  repository,
		httpClient:  httpClient,
		clock:       clock,
		budgetLimit: budget,
		window:      windowSeconds,
		ttl:         ttlSeconds,
		cache:       make(map[string]cacheEntry),
		inflight:    make(map[string]*call),
	}
}

func (c *Client) Repository() string {
	return c.repository
}

func (c *Client) get(ctx context.Context, path string, destination any) error {
	if err := c.reserve(); err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return &fetchError{reason: ReasonUpstreamError}
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "mise")

	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil || isTimeout(err) {
			return &fetchError{reason: ReasonTimeout}
		}
		return &fetchError{reason: ReasonUpstreamError}
	}
	defer response.Body.Close()

	if retry, limited := rateLimit(response, c.clock.Now()); limited {
		c.block(retry)
		return &fetchError{reason: ReasonRateLimited, retryAt: &retry}
	}
	switch {
	case response.StatusCode == http.StatusNotFound:
		return &fetchError{reason: ReasonNotFound}
	case response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusGatewayTimeout:
		return &fetchError{reason: ReasonTimeout}
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return &fetchError{reason: ReasonUpstreamError}
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(destination); err != nil {
		return &fetchError{reason: ReasonUpstreamError}
	}
	return nil
}

func (c *Client) reserve() error {
	now := c.clock.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	if now < c.blockedUntil {
		retry := c.blockedUntil
		return &fetchError{reason: ReasonRateLimited, retryAt: &retry}
	}

	cutoff := now - c.window
	first := 0
	for first < len(c.budgetTimes) && c.budgetTimes[first] <= cutoff {
		first++
	}
	c.budgetTimes = c.budgetTimes[first:]

	if len(c.budgetTimes) >= c.budgetLimit {
		retry := c.budgetTimes[0] + c.window
		return &fetchError{reason: ReasonBudgetExhausted, retryAt: &retry}
	}
	c.budgetTimes = append(c.budgetTimes, now)
	return nil
}

func (c *Client) block(retryAt int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if retryAt > c.blockedUntil {
		c.blockedUntil = retryAt
	}
}

func rateLimit(response *http.Response, now int64) (int64, bool) {
	retryAfter := strings.TrimSpace(response.Header.Get("Retry-After"))
	reset := strings.TrimSpace(response.Header.Get("X-RateLimit-Reset"))
	remaining := strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining"))
	limited := response.StatusCode == http.StatusTooManyRequests ||
		(response.StatusCode == http.StatusForbidden &&
			(retryAfter != "" || reset != "" || remaining == "0")) ||
		(response.StatusCode == http.StatusServiceUnavailable && retryAfter != "")
	if !limited {
		return 0, false
	}

	if retryAfter != "" {
		if seconds, err := strconv.ParseInt(retryAfter, 10, 64); err == nil {
			return now + seconds, true
		}
		if moment, err := http.ParseTime(retryAfter); err == nil {
			return moment.Unix(), true
		}
	}
	if reset != "" {
		if moment, err := strconv.ParseInt(reset, 10, 64); err == nil {
			if moment > now {
				return moment, true
			}
			return now + 1, true
		}
	}
	return now + 60, true
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout())
}

type Ref struct {
	Owner  string
	Repo   string
	Number int
}

func ParsePullRequest(raw string) (Ref, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Ref{}, false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Ref{}, false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return Ref{}, false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return Ref{}, false
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if !validSegment(owner) || !validSegment(repo) {
		return Ref{}, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 {
		return Ref{}, false
	}
	return Ref{Owner: owner, Repo: repo, Number: number}, true
}

func validSegment(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-', char == '_', char == '.':
		default:
			return false
		}
	}
	return true
}

func repositoryPath(owner, repo string) string {
	return fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
}
