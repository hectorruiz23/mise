package kernel

import (
	"errors"
	"testing"
)

func TestRuleError(t *testing.T) {
	err := &RuleError{Code: CodeInvalidInput, Msg: "title is required"}
	if got, want := err.Error(), "invalid_input: title is required"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	bare := &RuleError{Code: CodeForbidden}
	if got, want := bare.Error(), "forbidden"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}

	var target *RuleError
	if !errors.As(error(err), &target) || target.Code != CodeInvalidInput {
		t.Error("RuleError does not survive errors.As")
	}
}

func TestRuleCodesAreUnique(t *testing.T) {
	codes := []string{
		CodeInvalidTransition,
		CodeForbidden,
		CodeReasonRequired,
		CodeEvidenceRequired,
		CodeLeaseRequired,
		CodeLeaseConflict,
		CodeStaleVersion,
		CodePreconditionRequired,
		CodeInvalidInput,
		CodeNotFound,
		CodeIdempotencyConflict,
		CodeSignupPending,
		CodeSignupConsumed,
		CodeUnauthorized,
		CodeSessionExpired,
	}

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code == "" {
			t.Error("empty rule code")
		}
		if seen[code] {
			t.Errorf("duplicate rule code %q", code)
		}
		seen[code] = true
	}
}
