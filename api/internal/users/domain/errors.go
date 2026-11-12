package domain

import "mise/api/internal/kernel"

type RuleError = kernel.RuleError

const (
	CodeNotFound       = kernel.CodeNotFound
	CodeInvalidInput   = kernel.CodeInvalidInput
	CodeForbidden      = kernel.CodeForbidden
	CodeSignupPending  = kernel.CodeSignupPending
	CodeSignupConsumed = kernel.CodeSignupConsumed
	CodeUnauthorized   = kernel.CodeUnauthorized
	CodeSessionExpired = kernel.CodeSessionExpired
)

func invalidInput(msg string) *RuleError {
	return &RuleError{Code: CodeInvalidInput, Msg: msg}
}

func ruleError(code, msg string) *RuleError {
	return &RuleError{Code: code, Msg: msg}
}
