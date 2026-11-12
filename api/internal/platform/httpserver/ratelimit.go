package httpserver

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	count    int
	resetAt  time.Time
	lastSeen time.Time
}

type RateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*rateBucket
	limit      int
	window     time.Duration
	maxBuckets int
	now        func() time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		buckets:    make(map[string]*rateBucket),
		limit:      limit,
		window:     window,
		maxBuckets: 4096,
		now:        time.Now,
	}
}

func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	allowed, retry := l.Peek(key)
	if !allowed {
		return false, retry
	}
	l.Record(key)
	return true, 0
}

func (l *RateLimiter) Peek(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok || !now.Before(bucket.resetAt) {
		return true, 0
	}
	if bucket.count >= l.limit {
		return false, bucket.resetAt.Sub(now)
	}
	return true, 0
}

func (l *RateLimiter) Record(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok {
		l.makeRoom(now)
		bucket = &rateBucket{resetAt: now.Add(l.window)}
		l.buckets[key] = bucket
	}
	if !now.Before(bucket.resetAt) {
		bucket.count = 0
		bucket.resetAt = now.Add(l.window)
	}

	bucket.count++
	bucket.lastSeen = now
}

func (l *RateLimiter) makeRoom(now time.Time) {
	if len(l.buckets) < l.maxBuckets {
		return
	}
	for key, bucket := range l.buckets {
		if !now.Before(bucket.resetAt) {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) < l.maxBuckets {
		return
	}

	var oldestKey string
	var oldest time.Time
	for key, bucket := range l.buckets {
		if oldestKey == "" || bucket.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = bucket.lastSeen
		}
	}
	delete(l.buckets, oldestKey)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if r.RemoteAddr == "" {
			return "unknown"
		}
		return r.RemoteAddr
	}
	return host
}

func (s *Server) clientKey(r *http.Request) string {
	if s.trustProxy {
		if forwarded := forwardedClient(r.Header.Get("X-Forwarded-For")); forwarded != "" {
			return forwarded
		}
	}
	return clientIP(r)
}

func forwardedClient(header string) string {
	if header == "" {
		return ""
	}
	hops := strings.Split(header, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(hops[i])
		if candidate == "" {
			continue
		}
		if host, _, err := net.SplitHostPort(candidate); err == nil {
			candidate = host
		}
		if net.ParseIP(candidate) != nil {
			return candidate
		}
		return ""
	}
	return ""
}

func retryAfterSeconds(d time.Duration) int {
	seconds := int(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}
