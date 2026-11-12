package kernel

const (
	CodeInvalidTransition    = "invalid_transition"
	CodeForbidden            = "forbidden"
	CodeReasonRequired       = "reason_required"
	CodeEvidenceRequired     = "evidence_required"
	CodeLeaseRequired        = "lease_required"
	CodeLeaseConflict        = "lease_conflict"
	CodeStaleVersion         = "stale_version"
	CodePreconditionRequired = "precondition_required"
	CodeInvalidInput         = "invalid_input"
	CodeNotFound             = "not_found"
	CodeIdempotencyConflict  = "idempotency_conflict"
	CodeSignupPending        = "signup_pending"
	CodeSignupConsumed       = "signup_consumed"
	CodeUnauthorized         = "unauthorized"
	CodeSessionExpired       = "session_expired"
)

type RuleError struct {
	Code string
	Msg  string
}

func (e *RuleError) Error() string {
	if e.Msg == "" {
		return e.Code
	}
	return e.Code + ": " + e.Msg
}
