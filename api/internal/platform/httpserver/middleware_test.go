package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"mise/api/internal/kernel"
)

type fakeAuthenticator struct {
	bearer  Principal
	session Principal
	err     error
}

func (f fakeAuthenticator) Bearer(context.Context, string) (Principal, error) {
	if f.err != nil {
		return Principal{}, f.err
	}
	return f.bearer, nil
}

func (f fakeAuthenticator) Session(context.Context, string) (Principal, error) {
	if f.err != nil {
		return Principal{}, f.err
	}
	return f.session, nil
}

func TestErrorStatus(t *testing.T) {
	tests := []struct {
		code string
		want int
	}{
		{kernel.CodeInvalidInput, http.StatusBadRequest},
		{kernel.CodeUnauthorized, http.StatusUnauthorized},
		{kernel.CodeSessionExpired, http.StatusUnauthorized},
		{kernel.CodeForbidden, http.StatusForbidden},
		{kernel.CodeNotFound, http.StatusNotFound},
		{kernel.CodeLeaseConflict, http.StatusConflict},
		{kernel.CodeInvalidTransition, http.StatusConflict},
		{kernel.CodeStaleVersion, http.StatusPreconditionFailed},
		{kernel.CodeSignupPending, http.StatusConflict},
		{kernel.CodePreconditionRequired, http.StatusPreconditionRequired},
		{"something_else", http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := ErrorStatus(tt.code); got != tt.want {
				t.Errorf("ErrorStatus(%q) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}
}

func TestWriteRuleError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", &kernel.RuleError{Code: kernel.CodeNotFound, Msg: "missing"}, 404, "not_found"},
		{"invalid input", &kernel.RuleError{Code: kernel.CodeInvalidInput, Msg: "bad"}, 400, "invalid_input"},
		{"unmapped rule", &kernel.RuleError{Code: "wat", Msg: "secret sql"}, 500, CodeInternal},
		{"raw error", errors.New("SELECT * FROM users failed"), 500, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WriteRuleError(recorder, tt.err)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			var body ErrorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error != tt.wantCode {
				t.Errorf("error = %q, want %q", body.Error, tt.wantCode)
			}
			if tt.wantStatus == 500 && strings.Contains(body.Detail, "SELECT") {
				t.Errorf("500 leaked internal detail: %q", body.Detail)
			}
		})
	}
}

func TestAuthenticatePrecedence(t *testing.T) {
	valid := Principal{ID: 7, Name: "Human", Email: "h@example.com", Kind: "human"}
	server := &Server{authenticator: fakeAuthenticator{
		bearer:  valid,
		session: valid,
		err:     &kernel.RuleError{Code: kernel.CodeUnauthorized, Msg: "bad"},
	}}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer nope")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid-cookie"})
	if _, _, err := server.authenticate(req); err == nil {
		t.Fatal("invalid bearer fell back to the cookie")
	}

	server.authenticator = fakeAuthenticator{bearer: valid, session: valid}
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid-cookie"})
	if _, source, err := server.authenticate(req); err != nil || source != sourceCookie {
		t.Fatalf("cookie auth = %v, %v", source, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer good")
	if principal, source, err := server.authenticate(req); err != nil || source != sourceBearer || principal.ID != 7 {
		t.Fatalf("bearer auth = %+v, %v, %v", principal, source, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	if _, _, err := server.authenticate(req); err == nil {
		t.Fatal("missing credential authenticated")
	}
}

func TestOriginMiddleware(t *testing.T) {
	const origin = "https://mise.example"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	tests := []struct {
		name   string
		source authSource
		method string
		origin string
		site   string
		want   int
	}{
		{"cookie mutation with origin", sourceCookie, http.MethodPost, origin, "same-origin", http.StatusNoContent},
		{"cookie mutation without origin", sourceCookie, http.MethodPost, "", "", http.StatusForbidden},
		{"cookie wrong origin", sourceCookie, http.MethodPost, "https://evil.example", "", http.StatusForbidden},
		{"cookie contradictory fetch site", sourceCookie, http.MethodPost, origin, "cross-site", http.StatusForbidden},
		{"bearer exempt", sourceBearer, http.MethodPost, "", "", http.StatusNoContent},
		{"read exempt", sourceCookie, http.MethodGet, "", "", http.StatusNoContent},
		{"session creation needs origin", "", http.MethodPost, "", "", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &Server{publicOrigin: origin}
			req := httptest.NewRequest(tt.method, "/api/tasks", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.site != "" {
				req.Header.Set("Sec-Fetch-Site", tt.site)
			}
			ctx := req.Context()
			if tt.source != "" {
				ctx = withAuthSource(ctx, tt.source)
			}
			req = req.WithContext(ctx)

			recorder := httptest.NewRecorder()
			server.origin(next).ServeHTTP(recorder, req)
			if recorder.Code != tt.want {
				t.Errorf("status = %d, want %d", recorder.Code, tt.want)
			}
		})
	}
}

func TestBodyLimitRejectsOversizedBody(t *testing.T) {
	handler := bodyLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader("x"))
	req.ContentLength = 2 << 20
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
	var body ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != CodePayloadTooLarge {
		t.Errorf("error = %q, want %q", body.Error, CodePayloadTooLarge)
	}
}

func TestRateLimiterWindowAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(10, 15*time.Minute)
	limiter.now = func() time.Time { return now }

	for i := 1; i <= 10; i++ {
		if allowed, _ := limiter.Allow("1.2.3.4"); !allowed {
			t.Fatalf("attempt %d was limited", i)
		}
	}
	allowed, retry := limiter.Allow("1.2.3.4")
	if allowed {
		t.Fatal("eleventh attempt was allowed")
	}
	if retry <= 0 || retry > 15*time.Minute {
		t.Errorf("retry after = %v, want within the window", retry)
	}

	now = now.Add(15*time.Minute + time.Second)
	if allowed, _ := limiter.Allow("1.2.3.4"); !allowed {
		t.Fatal("attempt after the window was limited")
	}
}

func TestRateLimiterIsBounded(t *testing.T) {
	limiter := NewRateLimiter(1, time.Minute)
	limiter.maxBuckets = 2
	limiter.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	limiter.Allow("a")
	limiter.Allow("b")
	limiter.Allow("c")

	if len(limiter.buckets) > 2 {
		t.Fatalf("buckets = %d, want <= 2", len(limiter.buckets))
	}
}
