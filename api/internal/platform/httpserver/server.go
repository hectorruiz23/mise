package httpserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultLoginLimit  = 10
	DefaultLoginWindow = 15 * time.Minute
)

type Options struct {
	DB            *sql.DB
	Logger        *slog.Logger
	Authenticator Authenticator
	PublicOrigin  string
	LoginLimit    int
	LoginWindow   time.Duration
	TrustProxy    bool
}

type Server struct {
	db            *sql.DB
	logger        *slog.Logger
	authenticator Authenticator
	publicOrigin  string
	trustProxy    bool
	limiter       *RateLimiter
	mux           *http.ServeMux
	routes        map[string]bool
	fallback      http.Handler
}

func New(opts Options) (*Server, error) {
	if opts.DB == nil {
		return nil, errors.New("httpserver: database is required")
	}
	if err := VerifyWAL(opts.DB); err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.LoginLimit <= 0 {
		opts.LoginLimit = DefaultLoginLimit
	}
	if opts.LoginWindow <= 0 {
		opts.LoginWindow = DefaultLoginWindow
	}

	server := &Server{
		db:            opts.DB,
		logger:        opts.Logger,
		authenticator: opts.Authenticator,
		publicOrigin:  opts.PublicOrigin,
		trustProxy:    opts.TrustProxy,
		limiter:       NewRateLimiter(opts.LoginLimit, opts.LoginWindow),
		mux:           http.NewServeMux(),
		routes:        make(map[string]bool),
		fallback:      http.NotFoundHandler(),
	}
	server.Handle("GET /api/health", true, http.HandlerFunc(server.health))
	return server, nil
}

func VerifyWAL(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("httpserver: acquire connection for WAL check: %w", err)
	}
	defer conn.Close()

	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("httpserver: read journal_mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("httpserver: database journal_mode is %q, want wal", mode)
	}
	return nil
}

func (s *Server) Handle(pattern string, public bool, handler http.Handler) {
	s.routes[pattern] = public
	s.mux.Handle(pattern, s.wrap(public, handler))
}

func (s *Server) wrap(public bool, handler http.Handler) http.Handler {
	if public {
		return s.origin(handler)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authenticator == nil {
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
			return
		}
		s.auth(s.origin(handler)).ServeHTTP(w, r)
	})
}

func (s *Server) HandleFunc(pattern string, public bool, handler http.HandlerFunc) {
	s.Handle(pattern, public, handler)
}

func (s *Server) SetFallback(handler http.Handler) {
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	s.fallback = handler
}

func (s *Server) Handler() http.Handler {
	core := http.HandlerFunc(s.serve)
	return requestID(logging(s.logger)(recoverer(s.logger)(securityHeaders(bodyLimit(core)))))
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if !isAPI(r.URL.Path) {
		s.fallback.ServeHTTP(w, r)
		return
	}
	s.serveAPI(w, r)
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	handler, pattern := s.mux.Handler(r)

	if s.isLoginAttempt(r) {
		s.serveLoginAttempt(w, r)
		return
	}

	if _, registered := s.routes[pattern]; !registered {
		rewriteMuxError(w, r, handler)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) serveLoginAttempt(w http.ResponseWriter, r *http.Request) {
	key := s.clientKey(r)
	if allowed, retryAfter := s.limiter.Peek(key); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfterSeconds(retryAfter)))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimited,
			"too many login attempts; try again later")
		return
	}

	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	s.mux.ServeHTTP(recorder, r)
	if recorder.status == http.StatusUnauthorized {
		s.limiter.Record(key)
	}
}

func (s *Server) isLoginAttempt(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/api/sessions"
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		WriteJSON(w, http.StatusServiceUnavailable, struct {
			OK bool `json:"ok"`
		}{OK: false})
		return
	}
	WriteJSON(w, http.StatusOK, struct {
		OK bool   `json:"ok"`
		DB string `json:"db"`
	}{OK: true, DB: "ok"})
}

type errorWriter struct {
	http.ResponseWriter
	wroteHeader bool
	intercept   bool
	status      int
}

func (w *errorWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status

	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			w.intercept = true
			code := CodeMethodNotAllowed
			detail := "method not allowed"
			if status == http.StatusNotFound {
				code = "not_found"
				detail = "unknown endpoint"
			}
			WriteError(w.ResponseWriter, status, code, detail)
			return
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *errorWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.intercept {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}

func rewriteMuxError(w http.ResponseWriter, r *http.Request, handler http.Handler) {
	writer := &errorWriter{ResponseWriter: w}
	handler.ServeHTTP(writer, r)
}
