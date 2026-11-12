package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"mise/api/internal/kernel"
)

const (
	CodePayloadTooLarge      = "payload_too_large"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeRateLimited          = "rate_limited"
	CodeInternal             = "internal"
	CodeUnsupportedMediaType = "unsupported_media_type"
)

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type ErrorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

func WriteError(w http.ResponseWriter, status int, code, detail string) {
	WriteJSON(w, status, ErrorBody{Error: code, Detail: detail})
}

func ErrorStatus(code string) int {
	switch code {
	case kernel.CodeInvalidInput:
		return http.StatusBadRequest
	case kernel.CodeUnauthorized, kernel.CodeSessionExpired:
		return http.StatusUnauthorized
	case kernel.CodeForbidden:
		return http.StatusForbidden
	case kernel.CodeNotFound:
		return http.StatusNotFound
	case kernel.CodeLeaseConflict,
		kernel.CodeInvalidTransition,
		kernel.CodeSignupPending,
		kernel.CodeSignupConsumed,
		kernel.CodeIdempotencyConflict,
		kernel.CodeConflict,
		kernel.CodeReasonRequired,
		kernel.CodeEvidenceRequired,
		kernel.CodeLeaseRequired:
		return http.StatusConflict
	case kernel.CodeStaleVersion:
		return http.StatusPreconditionFailed
	case kernel.CodePreconditionRequired:
		return http.StatusPreconditionRequired
	default:
		return http.StatusInternalServerError
	}
}

func WriteRuleError(w http.ResponseWriter, err error) {
	var rule *kernel.RuleError
	if !errors.As(err, &rule) {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
		return
	}

	status := ErrorStatus(rule.Code)
	if status == http.StatusInternalServerError {
		WriteError(w, http.StatusInternalServerError, CodeInternal, "internal server error")
		return
	}

	detail := rule.Msg
	if detail == "" {
		detail = http.StatusText(status)
	}
	WriteError(w, status, rule.Code, detail)
}
