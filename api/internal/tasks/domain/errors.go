package domain

import "mise/api/internal/kernel"

type RuleError = kernel.RuleError

const (
	CodeInvalidTransition    = kernel.CodeInvalidTransition
	CodeForbidden            = kernel.CodeForbidden
	CodeReasonRequired       = kernel.CodeReasonRequired
	CodeEvidenceRequired     = kernel.CodeEvidenceRequired
	CodeLeaseRequired        = kernel.CodeLeaseRequired
	CodeLeaseConflict        = kernel.CodeLeaseConflict
	CodeStaleVersion         = kernel.CodeStaleVersion
	CodePreconditionRequired = kernel.CodePreconditionRequired
	CodeInvalidInput         = kernel.CodeInvalidInput
	CodeNotFound             = kernel.CodeNotFound
	CodeSignupPending        = kernel.CodeSignupPending
	CodeSignupConsumed       = kernel.CodeSignupConsumed
	CodeUnauthorized         = kernel.CodeUnauthorized
	CodeSessionExpired       = kernel.CodeSessionExpired
)

func invalidInput(msg string) *RuleError {
	return &RuleError{Code: CodeInvalidInput, Msg: msg}
}

func ruleError(code, msg string) *RuleError {
	return &RuleError{Code: code, Msg: msg}
}
