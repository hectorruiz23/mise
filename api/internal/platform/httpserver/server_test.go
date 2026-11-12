package httpserver

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mise/api/internal/platform/db"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewRefusesNonWALDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.db")
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open plain database: %v", err)
	}
	t.Cleanup(func() { _ = plain.Close() })

	if _, err := plain.Exec("PRAGMA journal_mode=delete"); err != nil {
		t.Fatalf("force journal_mode=delete: %v", err)
	}
	var mode string
	if err := plain.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want a non-WAL database", mode)
	}

	_, err = New(Options{DB: plain, Logger: discardLogger(), Authenticator: fakeAuthenticator{}})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "wal") {
		t.Fatalf("New error = %v, want a WAL refusal", err)
	}
}

func TestHealthAndDatabaseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	server, err := New(Options{DB: database, Logger: discardLogger(), Authenticator: fakeAuthenticator{}})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	handler := server.Handler()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", recorder.Code)
	}
	var healthy struct {
		OK bool   `json:"ok"`
		DB string `json:"db"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &healthy); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if !healthy.OK || healthy.DB != "ok" {
		t.Fatalf("health = %+v, want ok", healthy)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status after close = %d, want 503", recorder.Code)
	}
}

func TestUnknownAPIRouteAndMethodAreJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mise.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	server, err := New(Options{DB: database, Logger: discardLogger(), Authenticator: fakeAuthenticator{}})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown route status = %d, want 404", recorder.Code)
	}
	var body ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 404: %v", err)
	}
	if body.Error != "not_found" {
		t.Errorf("404 error = %q, want not_found", body.Error)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/health", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d, want 405", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); !strings.Contains(allow, http.MethodGet) {
		t.Errorf("Allow = %q, want GET", allow)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 405: %v", err)
	}
	if body.Error != CodeMethodNotAllowed {
		t.Errorf("405 error = %q, want %q", body.Error, CodeMethodNotAllowed)
	}
}
