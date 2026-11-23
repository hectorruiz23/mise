package domain

import (
	"encoding/hex"
	"net/url"
	"strings"
)

type CheckState string

const (
	CheckPassed       CheckState = "passed"
	CheckFailed       CheckState = "failed"
	CheckPending      CheckState = "pending"
	CheckMissing      CheckState = "missing"
	CheckInconclusive CheckState = "inconclusive"
)

const (
	VerdictOpen   = "open"
	VerdictPassed = "passed"
	VerdictFailed = "failed"
)

type Check struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
}

type Submission struct {
	ID          int64
	TaskKey     string
	PRURL       string
	HeadSHA     string
	Required    []string
	Verdict     string
	Checks      []Check
	DeclaredAt  int64
	DeliveredAt *int64
	CheckedAt   *int64
}

func ValidRevision(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func RepositoryOf(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func CheckStateOf(c Check) CheckState {
	return classify(c)
}

func classify(c Check) CheckState {
	if c.Status != "" && c.Status != "completed" {
		return CheckPending
	}
	switch c.Conclusion {
	case "failure":
		return CheckFailed
	case "", "cancelled", "timed_out", "action_required", "stale":
		return CheckInconclusive
	default:
		return CheckPassed
	}
}

func EvaluateChecks(required []string, observed []Check) (string, []Check) {
	latest := make(map[string]Check, len(observed))
	for _, c := range observed {
		latest[c.Name] = c
	}
	verdict := VerdictPassed
	checks := make([]Check, 0, len(required))
	for _, name := range required {
		c, ok := latest[name]
		if !ok {
			c = Check{Name: name}
		}
		checks = append(checks, c)
		switch classify(c) {
		case CheckFailed:
			verdict = VerdictFailed
		case CheckPending, CheckMissing, CheckInconclusive:
			if verdict != VerdictFailed {
				verdict = VerdictOpen
			}
		}
	}
	if len(required) == 0 {
		return VerdictPassed, checks
	}
	return verdict, checks
}

func ChecksOf(sub Submission) []Check {
	if sub.Checks == nil {
		return []Check{}
	}
	return sub.Checks
}

func FailedChecks(checks []Check) string {
	names := make([]string, 0, len(checks))
	for _, c := range checks {
		if classify(c) == CheckFailed {
			names = append(names, c.Name)
		}
	}
	if len(names) == 0 {
		return "no check"
	}
	return strings.Join(names, ", ")
}

func NormalizeChecks(checks []string) ([]string, error) {
	if len(checks) > 50 {
		return nil, invalidInput("required_checks accepts at most 50 names")
	}
	cleaned := make([]string, 0, len(checks))
	seen := make(map[string]struct{}, len(checks))
	for _, name := range checks {
		value := strings.TrimSpace(name)
		if value == "" {
			return nil, invalidInput("required_checks entries must not be blank")
		}
		if len(value) > 100 {
			return nil, invalidInput("required_checks entries must be at most 100 characters")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned, nil
}
