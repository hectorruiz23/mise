package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/users/app"
	"mise/api/internal/users/domain"
)

const sessionMaxAge = 30 * 24 * 60 * 60

type Handler struct {
	service      *app.Service
	publicOrigin string
}

func New(service *app.Service, publicOrigin string) *Handler {
	return &Handler{service: service, publicOrigin: publicOrigin}
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := decodeStrict(r, &body); err != nil || body.Token == "" {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput,
			"token is required")
		return
	}

	session, cookieValue, user, err := h.service.Login(r.Context(), body.Token)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	http.SetCookie(w, h.sessionCookie(cookieValue, session.ExpiresAt))

	httpserver.WriteJSON(w, http.StatusCreated, struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Email string `json:"email"`
	}{
		ID:    user.ID,
		Name:  user.Name,
		Kind:  string(user.Kind),
		Email: user.Email,
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpserver.PrincipalFrom(r.Context()); !ok {
		httpserver.WriteError(w, http.StatusUnauthorized,
			kernel.CodeUnauthorized, "credential required")
		return
	}
	source, ok := httpserver.AuthSourceFrom(r.Context())
	if !ok || source != httpserver.AuthSourceCookie {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden,
			"only a browser session can be logged out")
		return
	}

	cookie, err := r.Cookie(httpserver.SessionCookieName)
	if err != nil || cookie.Value == "" {
		httpserver.WriteError(w, http.StatusUnauthorized,
			kernel.CodeUnauthorized, "credential required")
		return
	}
	if err := h.service.Logout(r.Context(), cookie.Value); err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	cleared := h.sessionCookie("", 0)
	cleared.MaxAge = -1
	cleared.Expires = time.Unix(1, 0)
	http.SetCookie(w, cleared)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RequestSignup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}

	id, secret, err := h.service.RequestSignup(r.Context(), body.Email)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, struct {
		ID     int64  `json:"id"`
		Secret string `json:"secret"`
	}{ID: id, Secret: secret})
}

func (h *Handler) SignupStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := signupID(w, r)
	if !ok {
		return
	}

	status, err := h.service.SignupStatus(r.Context(), id, r.URL.Query().Get("secret"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: status})
}

func (h *Handler) ClaimSignup(w http.ResponseWriter, r *http.Request) {
	id, ok := signupID(w, r)
	if !ok {
		return
	}

	var body struct {
		Secret string `json:"secret"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}

	user, token, err := h.service.ClaimSignup(r.Context(), id, body.Secret)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, struct {
		UserID int64  `json:"user_id"`
		Token  string `json:"token"`
	}{UserID: user.ID, Token: token})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	principal, ok := httpserver.PrincipalFrom(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized,
			kernel.CodeUnauthorized, "credential required")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Kind  string `json:"kind"`
		Email string `json:"email"`
	}{
		ID:    principal.ID,
		Name:  principal.Name,
		Kind:  principal.Kind,
		Email: principal.Email,
	})
}

type Authenticator struct {
	service *app.Service
}

func NewAuthenticator(service *app.Service) *Authenticator {
	return &Authenticator{service: service}
}

func (a *Authenticator) Bearer(ctx context.Context, token string) (httpserver.Principal, error) {
	user, err := a.service.Authenticate(ctx, token)
	if err != nil {
		return httpserver.Principal{}, err
	}
	return principalOf(user), nil
}

func (a *Authenticator) Session(ctx context.Context, sessionID string) (httpserver.Principal, error) {
	user, err := a.service.SessionUser(ctx, sessionID)
	if err != nil {
		return httpserver.Principal{}, err
	}
	return principalOf(user), nil
}

func principalOf(user domain.User) httpserver.Principal {
	return httpserver.Principal{
		ID:    user.ID,
		Name:  user.Name,
		Email: user.Email,
		Kind:  string(user.Kind),
	}
}

func (h *Handler) sessionCookie(value string, expiresAt int64) *http.Cookie {
	cookie := &http.Cookie{
		Name:     httpserver.SessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.publicOrigin, "https://"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   sessionMaxAge,
	}
	if expiresAt > 0 {
		cookie.Expires = time.Unix(expiresAt, 0)
	}
	return cookie
}

func signupID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput,
			"signup request id must be a positive integer")
		return 0, false
	}
	return id, true
}

func decodeStrict(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("body required")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("body must contain exactly one JSON object")
	}
	return nil
}
