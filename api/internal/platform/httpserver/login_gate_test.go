package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mise/api/internal/platform/db"
)

const loginOrigin = "http://127.0.0.1:8787"

func loginServer(t *testing.T, opts Options, limit int) http.Handler {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mise.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	opts.DB = database
	opts.Logger = discardLogger()
	opts.Authenticator = fakeAuthenticator{}
	opts.PublicOrigin = loginOrigin
	opts.LoginLimit = limit
	opts.LoginWindow = 15 * time.Minute

	server, err := New(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.HandleFunc("POST /api/sessions", true, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &payload); err != nil || payload.Token != "good" {
			WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
			return
		}
		w.WriteHeader(http.StatusCreated)
	})
	return server.Handler()
}

func postLogin(t *testing.T, handler http.Handler, token, forwardedFor string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", loginOrigin)
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestLoginGateCountsOnlyFailedAttempts(t *testing.T) {
	handler := loginServer(t, Options{}, 3)

	for i := 1; i <= 5; i++ {
		if status := postLogin(t, handler, "good", "").Code; status != http.StatusCreated {
			t.Fatalf("successful login %d status = %d, want 201", i, status)
		}
	}

	for i := 1; i <= 3; i++ {
		if status := postLogin(t, handler, "bad", "").Code; status != http.StatusUnauthorized {
			t.Fatalf("failed login %d status = %d, want 401", i, status)
		}
	}
	if status := postLogin(t, handler, "bad", "").Code; status != http.StatusTooManyRequests {
		t.Fatalf("fourth failure status = %d, want 429", status)
	}

	if status := postLogin(t, handler, "good", "").Code; status != http.StatusTooManyRequests {
		t.Fatalf("good token behind an exhausted bucket status = %d, want 429", status)
	}
}

func TestLoginGateKeysOnForwardedAddressWhenTrusted(t *testing.T) {
	handler := loginServer(t, Options{TrustProxy: true}, 2)

	for i := 1; i <= 2; i++ {
		if status := postLogin(t, handler, "bad", "203.0.113.5").Code; status != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401", i, status)
		}
	}
	if status := postLogin(t, handler, "bad", "203.0.113.5").Code; status != http.StatusTooManyRequests {
		t.Fatalf("third failure from the same address status = %d, want 429", status)
	}

	if status := postLogin(t, handler, "bad", "203.0.113.9").Code; status != http.StatusUnauthorized {
		t.Fatalf("failure from a second address status = %d, want 401", status)
	}
	if status := postLogin(t, handler, "good", "203.0.113.9").Code; status != http.StatusCreated {
		t.Fatalf("successful login from a second address status = %d, want 201", status)
	}
}

func TestLoginGateIgnoresForwardedAddressByDefault(t *testing.T) {
	handler := loginServer(t, Options{}, 2)

	for i := 1; i <= 2; i++ {
		if status := postLogin(t, handler, "bad", fmt.Sprintf("203.0.113.%d", i)).Code; status != http.StatusUnauthorized {
			t.Fatalf("failure %d status = %d, want 401", i, status)
		}
	}
	if status := postLogin(t, handler, "bad", "198.51.100.77").Code; status != http.StatusTooManyRequests {
		t.Fatalf("spoofed X-Forwarded-For status = %d, want 429", status)
	}
}

func TestForwardedClient(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"", ""},
		{"203.0.113.5", "203.0.113.5"},
		{"203.0.113.5, 198.51.100.7", "198.51.100.7"},
		{"203.0.113.5:4711", "203.0.113.5"},
		{"not-an-ip", ""},
		{"203.0.113.5, not-an-ip", ""},
		{"::1", "::1"},
		{"[2001:db8::1]:443", "2001:db8::1"},
	}
	for _, tc := range cases {
		if got := forwardedClient(tc.header); got != tc.want {
			t.Errorf("forwardedClient(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}
