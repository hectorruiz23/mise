package githubinfo

import (
	"context"
	"errors"
)

const (
	StatusFresh       = "fresh"
	StatusStale       = "stale"
	StatusUnavailable = "unavailable"
	StatusUnknown     = "unknown"
)

type Envelope[T any] struct {
	Status    string  `json:"status"`
	Data      T       `json:"data"`
	CheckedAt *int64  `json:"checked_at"`
	ExpiresAt *int64  `json:"expires_at"`
	Reason    *string `json:"reason"`
	RetryAt   *int64  `json:"retry_at"`
}

func observe[T any](
	client *Client,
	ctx context.Context,
	key string,
	fetch func(context.Context) (T, error),
) Envelope[T] {
	now := client.clock.Now()
	client.mu.Lock()
	entry, found := client.cache[key]
	client.mu.Unlock()

	if found && now < entry.expiresAt {
		return envelopeOf[T](entry, StatusFresh, nil, nil)
	}

	value, err := group(client, ctx, key, fetch)
	if err != nil {
		reason, retryAt := failureOf(err)
		if found {
			return envelopeOf[T](entry, StatusStale, &reason, retryAt)
		}
		return Envelope[T]{Status: StatusUnavailable, Reason: &reason, RetryAt: retryAt}
	}

	now = client.clock.Now()
	entry = cacheEntry{data: value, checkedAt: now, expiresAt: now + client.ttl}
	client.mu.Lock()
	client.cache[key] = entry
	client.mu.Unlock()
	return envelopeOf[T](entry, StatusFresh, nil, nil)
}

func envelopeOf[T any](
	entry cacheEntry,
	status string,
	reason *string,
	retryAt *int64,
) Envelope[T] {
	data, _ := entry.data.(T)
	checkedAt := entry.checkedAt
	expiresAt := entry.expiresAt
	return Envelope[T]{
		Status:    status,
		Data:      data,
		CheckedAt: &checkedAt,
		ExpiresAt: &expiresAt,
		Reason:    reason,
		RetryAt:   retryAt,
	}
}

func group[T any](
	client *Client,
	ctx context.Context,
	key string,
	fetch func(context.Context) (T, error),
) (T, error) {
	client.mu.Lock()
	if active, ok := client.inflight[key]; ok {
		client.mu.Unlock()
		select {
		case <-active.done:
			if active.err != nil {
				var zero T
				return zero, active.err
			}
			value, _ := active.value.(T)
			return value, nil
		case <-ctx.Done():
			var zero T
			return zero, &fetchError{reason: ReasonTimeout}
		}
	}
	active := &call{done: make(chan struct{})}
	client.inflight[key] = active
	client.mu.Unlock()

	value, err := fetch(ctx)

	active.value = value
	active.err = err
	client.mu.Lock()
	delete(client.inflight, key)
	client.mu.Unlock()
	close(active.done)
	return value, err
}

func failureOf(err error) (string, *int64) {
	var observed *fetchError
	if errors.As(err, &observed) {
		reason := observed.reason
		if reason == "" {
			reason = ReasonUpstreamError
		}
		return reason, observed.retryAt
	}
	return ReasonUpstreamError, nil
}

func mapEnvelope[T any, U any](source Envelope[T], transform func(T) U) Envelope[U] {
	result := Envelope[U]{
		Status:    source.Status,
		CheckedAt: source.CheckedAt,
		ExpiresAt: source.ExpiresAt,
		Reason:    source.Reason,
		RetryAt:   source.RetryAt,
	}
	if source.Status == StatusFresh || source.Status == StatusStale {
		result.Data = transform(source.Data)
	}
	return result
}
