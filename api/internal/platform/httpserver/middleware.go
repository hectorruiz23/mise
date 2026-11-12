package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"mise/api/internal/kernel"
)

const SessionCookieName = "mise_session"

const maxBodyBytes int64 = 1 << 20

type Principal struct {
	ID    int64
	Name  string
	Email string
	Kind  string
}

type Authenticator interface {
	Bearer(ctx context.Context, token string) (Principal, error)
	Session(ctx context.Context, sessionID string) (Principal, error)
}

type contextKey int

const (
	principalKey contextKey = iota
	requestIDKey
	authSourceKey
)

type AuthSource string

const (
	AuthSourceBearer AuthSource = "bearer"
	AuthSourceCookie AuthSource = "cookie"
	AuthSourceDev    AuthSource = "dev"
)

type authSource = AuthSource

const (
	sourceBearer = AuthSourceBearer
	sourceCookie = AuthSourceCookie
	sourceDev    = AuthSourceDev
)

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(principalKey).(Principal)
	return principal, ok
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func withPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

func authSourceFrom(ctx context.Context) (authSource, bool) {
	source, ok := ctx.Value(authSourceKey).(authSource)
	return source, ok
}

func withAuthSource(ctx context.Context, source authSource) context.Context {
	return context.WithValue(ctx, authSourceKey, source)
}

func AuthSourceFrom(ctx context.Context) (AuthSource, bool) {
	source, ok := authSourceFrom(ctx)
	return AuthSource(source), ok
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(
			context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func newRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func logging(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)

			actorID := int64(0)
			if principal, ok := PrincipalFrom(r.Context()); ok {
				actorID = principal.ID
			}
			logger.Info("http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.status),
				slog.String("duration", time.Since(start).String()),
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.Int64("actor_id", actorID),
			)
		})
	}
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("panic serving request",
						slog.Any("panic", recovered),
						slog.String("request_id", RequestIDFrom(r.Context())),
						slog.String("stack", string(debug.Stack())),
					)
					WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")
		header.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBodyBytes {
			WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				"request body exceeds 1 MiB")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authenticate(r *http.Request) (Principal, authSource, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		fields := strings.Fields(header)
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || fields[1] == "" {
			return Principal{}, "", unauthorized("invalid Authorization header")
		}
		principal, err := s.authenticator.Bearer(r.Context(), fields[1])
		if err != nil {
			return Principal{}, "", err
		}
		return principal, sourceBearer, nil
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		if s.dev != nil {
			return *s.dev, sourceDev, nil
		}
		return Principal{}, "", unauthorized("credential required")
	}
	principal, err := s.authenticator.Session(r.Context(), cookie.Value)
	if err != nil {
		return Principal{}, "", err
	}
	return principal, sourceCookie, nil
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, source, err := s.authenticate(r)
		if err != nil {
			WriteRuleError(w, err)
			return
		}
		ctx := withAuthSource(withPrincipal(r.Context(), principal), source)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func unauthorized(msg string) error {
	return &kernel.RuleError{Code: kernel.CodeUnauthorized, Msg: msg}
}

func (s *Server) origin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requiresOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}

		if source, ok := authSourceFrom(r.Context()); ok && source == sourceBearer {
			next.ServeHTTP(w, r)
			return
		}

		if !originAllowed(r, s.publicOrigin) {
			WriteError(w, http.StatusForbidden, kernel.CodeForbidden, "origin check failed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requiresOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func originAllowed(r *http.Request, publicOrigin string) bool {
	if publicOrigin == "" {
		return false
	}
	if r.Header.Get("Origin") != publicOrigin {
		return false
	}
	switch site := r.Header.Get("Sec-Fetch-Site"); site {
	case "", "same-origin", "none":
		return true
	default:
		return false
	}
}

func isAPI(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}
