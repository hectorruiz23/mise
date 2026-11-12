//go:build dev

package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mise/api/internal/platform/db"
)

func TestDevPrincipalEntersWithoutCredential(t *testing.T) {
	t.Setenv("MISE_DEV_USER", "1")
	t.Setenv("MISE_DEV_NAME", "dev")

	path := filepath.Join(t.TempDir(), "mise.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	server, err := New(Options{
		DB:            database,
		Logger:        discardLogger(),
		Authenticator: fakeAuthenticator{},
		PublicOrigin:  loginOrigin,
		DevPrincipal:  DevPrincipalFromEnv(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.HandleFunc("GET /api/me", false, func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			WriteError(w, http.StatusInternalServerError, "internal", "no principal")
			return
		}
		WriteJSON(w, http.StatusOK, principal)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var principal Principal
	if err := json.Unmarshal(recorder.Body.Bytes(), &principal); err != nil {
		t.Fatalf("decode principal: %v", err)
	}
	if principal.ID != 1 || principal.Kind != "human" {
		t.Errorf("principal = %+v, want the dev human", principal)
	}
}

func TestDevPrincipalDoesNotOverrideABadBearer(t *testing.T) {
	t.Setenv("MISE_DEV_USER", "1")

	path := filepath.Join(t.TempDir(), "mise.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	server, err := New(Options{
		DB:            database,
		Logger:        discardLogger(),
		Authenticator: fakeAuthenticator{err: unauthorized("invalid token")},
		PublicOrigin:  loginOrigin,
		DevPrincipal:  DevPrincipalFromEnv(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.HandleFunc("GET /api/me", false, func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, struct{}{})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/me", strings.NewReader(""))
	request.Header.Set("Authorization", "Bearer nonsense")
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestDevPrincipalIgnoresAnInvalidUserID(t *testing.T) {
	t.Setenv("MISE_DEV_USER", "not-a-number")
	if principal := DevPrincipalFromEnv(); principal != nil {
		t.Fatalf("principal = %+v, want nil", principal)
	}
	t.Setenv("MISE_DEV_USER", "")
	if principal := DevPrincipalFromEnv(); principal != nil {
		t.Fatalf("principal = %+v, want nil", principal)
	}
}
