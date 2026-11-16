package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/db"
)

type viewerAuthenticator struct {
	bearer       Principal
	bearerErr    error
	session      Principal
	sessionErr   error
	bearerCalls  int
	sessionCalls int
}

func (a *viewerAuthenticator) Bearer(context.Context, string) (Principal, error) {
	a.bearerCalls++
	if a.bearerErr != nil {
		return Principal{}, a.bearerErr
	}
	return a.bearer, nil
}

func (a *viewerAuthenticator) Session(context.Context, string) (Principal, error) {
	a.sessionCalls++
	if a.sessionErr != nil {
		return Principal{}, a.sessionErr
	}
	return a.session, nil
}

func newViewerServer(t *testing.T, authenticator Authenticator, dev *Principal) *Server {
	t.Helper()

	database, err := db.Open(filepath.Join(t.TempDir(), "mise.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	server, err := New(Options{
		DB:            database,
		Logger:        discardLogger(),
		Authenticator: authenticator,
		DevPrincipal:  dev,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

func decodeViewerError(t *testing.T, recorder *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body
}

func decodeViewerPrincipal(t *testing.T, recorder *httptest.ResponseRecorder) Principal {
	t.Helper()
	var principal Principal
	if err := json.Unmarshal(recorder.Body.Bytes(), &principal); err != nil {
		t.Fatalf("decode principal: %v", err)
	}
	return principal
}

func requireViewerCacheHeaders(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	vary := recorder.Header().Get("Vary")
	if !strings.Contains(vary, "Authorization") || !strings.Contains(vary, "Cookie") {
		t.Errorf("Vary = %q, want Authorization and Cookie", vary)
	}
}

func viewerReadHandler(called *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*called = true
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "no principal")
			return
		}
		WriteJSON(w, http.StatusOK, principal)
	}
}

func TestViewerReadServesVisitor(t *testing.T) {
	authenticator := &viewerAuthenticator{}
	server := newViewerServer(t, authenticator, nil)
	called := false
	server.HandleRead("GET /api/viewer/board", viewerReadHandler(&called))

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	principal := decodeViewerPrincipal(t, recorder)
	want := Principal{ID: 0, Name: "Visitor", Email: "", Kind: "viewer"}
	if principal != want {
		t.Errorf("principal = %+v, want %+v", principal, want)
	}
	requireViewerCacheHeaders(t, recorder)
	if authenticator.bearerCalls != 0 || authenticator.sessionCalls != 0 {
		t.Errorf("authentication was consulted without credentials: bearer=%d session=%d",
			authenticator.bearerCalls, authenticator.sessionCalls)
	}
}

func TestViewerReadAuthenticatesValidBearer(t *testing.T) {
	human := Principal{ID: 7, Name: "Human", Email: "h@example.com", Kind: "human"}
	authenticator := &viewerAuthenticator{bearer: human}
	server := newViewerServer(t, authenticator, nil)
	called := false
	server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

	request := httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil)
	request.Header.Set("Authorization", "Bearer good")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if principal := decodeViewerPrincipal(t, recorder); principal != human {
		t.Errorf("principal = %+v, want %+v", principal, human)
	}
	requireViewerCacheHeaders(t, recorder)
}

func TestViewerReadRejectsInvalidBearerWithoutCookieFallback(t *testing.T) {
	human := Principal{ID: 7, Name: "Human", Kind: "human"}
	authenticator := &viewerAuthenticator{
		session:   human,
		bearerErr: &kernel.RuleError{Code: kernel.CodeUnauthorized, Msg: "invalid token"},
	}
	server := newViewerServer(t, authenticator, nil)
	called := false
	server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

	request := httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil)
	request.Header.Set("Authorization", "Bearer nope")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "valid-cookie"})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if body := decodeViewerError(t, recorder); body.Error != kernel.CodeUnauthorized {
		t.Errorf("error = %q, want %q", body.Error, kernel.CodeUnauthorized)
	}
	if called {
		t.Fatal("handler was called")
	}
	if authenticator.sessionCalls != 0 {
		t.Errorf("session authenticator called %d times, want 0", authenticator.sessionCalls)
	}
	requireViewerCacheHeaders(t, recorder)
}

func TestViewerReadRejectsEmptyAuthorizationHeader(t *testing.T) {
	server := newViewerServer(t, &viewerAuthenticator{}, nil)
	called := false
	server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

	request := httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil)
	request.Header["Authorization"] = []string{""}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if body := decodeViewerError(t, recorder); body.Error != kernel.CodeUnauthorized {
		t.Errorf("error = %q, want %q", body.Error, kernel.CodeUnauthorized)
	}
	if called {
		t.Fatal("handler was called")
	}
	requireViewerCacheHeaders(t, recorder)
}

func TestViewerReadRejectsEmptyCookie(t *testing.T) {
	human := Principal{ID: 7, Name: "Human", Kind: "human"}
	authenticator := &viewerAuthenticator{session: human}
	server := newViewerServer(t, authenticator, nil)
	called := false
	server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

	request := httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ""})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if body := decodeViewerError(t, recorder); body.Error != kernel.CodeUnauthorized {
		t.Errorf("error = %q, want %q", body.Error, kernel.CodeUnauthorized)
	}
	if called {
		t.Fatal("handler was called")
	}
	if authenticator.sessionCalls != 0 {
		t.Errorf("session authenticator called %d times, want 0", authenticator.sessionCalls)
	}
	requireViewerCacheHeaders(t, recorder)
}

func TestViewerReadRejectsExpiredAndRevokedSessions(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "expired",
			err:  &kernel.RuleError{Code: kernel.CodeSessionExpired, Msg: "session expired"},
			code: kernel.CodeSessionExpired,
		},
		{
			name: "revoked",
			err:  &kernel.RuleError{Code: kernel.CodeUnauthorized, Msg: "session not found"},
			code: kernel.CodeUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authenticator := &viewerAuthenticator{sessionErr: tt.err}
			server := newViewerServer(t, authenticator, nil)
			called := false
			server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

			request := httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "some-session"})
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", recorder.Code)
			}
			if body := decodeViewerError(t, recorder); body.Error != tt.code {
				t.Errorf("error = %q, want %q", body.Error, tt.code)
			}
			if called {
				t.Fatal("handler was called")
			}
			requireViewerCacheHeaders(t, recorder)
		})
	}
}

func TestViewerReadKeepsTheDevelopmentIdentity(t *testing.T) {
	dev := &Principal{ID: 4, Name: "Developer", Email: "dev@example.com", Kind: "human"}
	authenticator := &viewerAuthenticator{}
	server := newViewerServer(t, authenticator, dev)
	called := false
	server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if principal := decodeViewerPrincipal(t, recorder); principal != *dev {
		t.Errorf("principal = %+v, want %+v", principal, *dev)
	}
	if authenticator.bearerCalls != 0 || authenticator.sessionCalls != 0 {
		t.Errorf("authentication was consulted for the dev identity: bearer=%d session=%d",
			authenticator.bearerCalls, authenticator.sessionCalls)
	}
}

func TestViewerReadTreatsAuthenticationFailureAsInternal(t *testing.T) {
	authenticator := &viewerAuthenticator{bearerErr: errors.New("database is down")}
	server := newViewerServer(t, authenticator, nil)
	called := false
	server.HandleReadFunc("GET /api/viewer/board", viewerReadHandler(&called))

	request := httptest.NewRequest(http.MethodGet, "/api/viewer/board", nil)
	request.Header.Set("Authorization", "Bearer good")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if body := decodeViewerError(t, recorder); body.Error != CodeInternal {
		t.Errorf("error = %q, want %q", body.Error, CodeInternal)
	}
	if called {
		t.Fatal("handler was called")
	}
	requireViewerCacheHeaders(t, recorder)
}

func TestAuthenticatedRouteRefusesTheViewerPrincipal(t *testing.T) {
	dev := &Principal{ID: 0, Name: "Visitor", Email: "", Kind: "viewer"}
	server := newViewerServer(t, &viewerAuthenticator{}, dev)
	called := false
	server.HandleFunc("POST /api/tasks", false, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/tasks", nil))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	body := decodeViewerError(t, recorder)
	if body.Error != kernel.CodeForbidden {
		t.Errorf("error = %q, want %q", body.Error, kernel.CodeForbidden)
	}
	if body.Detail != "viewers cannot modify the board" {
		t.Errorf("detail = %q, want the viewer refusal", body.Detail)
	}
	if called {
		t.Fatal("handler was called")
	}
	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want it absent", got)
	}
}

func TestPublicRouteHasNoViewerCacheHeaders(t *testing.T) {
	server := newViewerServer(t, &viewerAuthenticator{}, nil)
	server.HandleFunc("GET /api/public/thing", true, func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, struct{}{})
	})

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/thing", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Errorf("Cache-Control = %q, want it absent", got)
	}
	if got := recorder.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want it absent", got)
	}
}

func TestHandleReadRejectsNonGETPattern(t *testing.T) {
	server := newViewerServer(t, &viewerAuthenticator{}, nil)

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("HandleReadFunc accepted a POST pattern")
		}
	}()
	server.HandleReadFunc("POST /api/viewer/mutate", func(http.ResponseWriter, *http.Request) {})
}
