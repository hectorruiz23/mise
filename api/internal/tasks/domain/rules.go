package domain

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"
)

const takeoverReason = "takeover"

var transitions = map[State]map[ActorKind][]State{
	Backlog: {
		Human: {Ready},
	},
	Ready: {
		Human: {Backlog, InProgress},
		Agent: {InProgress},
	},
	InProgress: {
		Human: {Ready, Blocked, Review},
		Agent: {Blocked, Review},
	},
	Blocked: {
		Human: {Ready, InProgress},
		Agent: {InProgress},
	},
	Review: {
		Human: {InProgress, Done},
		Agent: {InProgress},
	},
	Done: {
		Human: {Ready},
	},
}

func AllowedTransitions(from State, kind ActorKind) []State {
	byKind, ok := transitions[from]
	if !ok {
		return nil
	}
	allowed, ok := byKind[kind]
	if !ok {
		return nil
	}
	return slices.Clone(allowed)
}

func IsLive(l Lease, now int64) bool {
	return l.ReleasedAt == nil && l.ExpiresAt > now
}

func ValidateMove(t Task, to State, actor Actor, reason string, hasDelivery bool) error {
	if t.State == Review && to == Done && actor.Kind != Human {
		return ruleError(CodeForbidden, "only a human may move a task from review to done")
	}

	if !transitionAllowed(t.State, to, actor.Kind) {
		return ruleError(CodeInvalidTransition,
			fmt.Sprintf("cannot move a task from %s to %s", t.State, to))
	}

	if to == Blocked || t.State == Blocked {
		if strings.TrimSpace(reason) == "" {
			return ruleError(CodeReasonRequired,
				"a reason is required when entering or leaving blocked")
		}
	} else if reason != "" && strings.TrimSpace(reason) == "" {
		return invalidInput("reason must not be blank")
	}

	if to == Review && !hasDelivery {
		return ruleError(CodeEvidenceRequired,
			"a delivery is required before a task can move to review")
	}

	if strings.TrimSpace(t.Title) == "" {
		return invalidInput("title is required")
	}

	return nil
}

func ApplyMove(t Task, to State, actor Actor, reason string, hasDelivery bool, now int64) (Task, Movement, error) {
	if err := ValidateMove(t, to, actor, reason, hasDelivery); err != nil {
		return Task{}, Movement{}, err
	}

	from := t.State
	next := t
	next.State = to
	next.Version = t.Version + 1
	next.UpdatedAt = now
	if to == Done {
		closedAt := now
		next.ClosedAt = &closedAt
	} else {
		next.ClosedAt = nil
	}

	return next, Movement{
		TaskKey:     t.Key,
		FromState:   &from,
		ToState:     to,
		ActorID:     actor.ID,
		Reason:      optionalText(reason),
		TaskVersion: next.Version,
		At:          now,
	}, nil
}

func Acquire(t Task, live *Lease, agent Actor, now int64, ttlSeconds int64) (Task, Lease, Movement, error) {
	if agent.Kind != Agent {
		return Task{}, Lease{}, Movement{}, ruleError(CodeForbidden, "only an agent may take a lease")
	}
	if ttlSeconds <= 0 {
		return Task{}, Lease{}, Movement{}, invalidInput("lease ttl must be positive")
	}

	hasLiveLease := live != nil && IsLive(*live, now)
	if hasLiveLease {
		return Task{}, Lease{}, Movement{}, ruleError(CodeLeaseConflict, "task already has a live lease")
	}

	switch t.State {
	case Ready:
	case InProgress:
	default:
		return Task{}, Lease{}, Movement{}, ruleError(CodeLeaseConflict,
			"a lease can only be taken on a ready task or an unleased in-progress task")
	}

	from := t.State
	next := t
	next.State = InProgress
	next.Version = t.Version + 1
	next.UpdatedAt = now
	next.ClosedAt = nil

	lease := Lease{
		TaskKey:   t.Key,
		AgentID:   agent.ID,
		CreatedAt: now,
		ExpiresAt: now + ttlSeconds,
	}

	var reason *string
	if t.State == InProgress {
		reason = optionalText(takeoverReason)
	}

	return next, lease, Movement{
		TaskKey:     t.Key,
		FromState:   &from,
		ToState:     InProgress,
		ActorID:     agent.ID,
		Reason:      reason,
		TaskVersion: next.Version,
		At:          now,
	}, nil
}

func ValidateDelivery(t Task, lease *Lease, actor Actor, summary string, evidence []string) error {
	if strings.TrimSpace(summary) == "" {
		return invalidInput("summary is required")
	}
	if utf8.RuneCountInString(summary) > 10000 {
		return invalidInput("summary must be at most 10000 characters")
	}

	switch actor.Kind {
	case Agent:
		if lease == nil {
			return ruleError(CodeLeaseRequired, "an agent needs a live lease it owns")
		}
		if lease.TaskKey != t.Key || lease.AgentID != actor.ID || lease.ReleasedAt != nil {
			return ruleError(CodeLeaseRequired, "an agent needs a live lease it owns")
		}
	case Human:
	default:
		return invalidInput("unknown actor kind")
	}

	if err := ValidateEvidence(evidence); err != nil {
		return err
	}

	if t.State != InProgress {
		return ruleError(CodeInvalidTransition, "a delivery is only possible from in_progress")
	}

	return nil
}

func ApplyDelivery(t Task, lease *Lease, actor Actor, summary string, evidence []string, prURL *string, now int64) (Task, Delivery, Movement, error) {
	if err := ValidateDelivery(t, lease, actor, summary, evidence); err != nil {
		return Task{}, Delivery{}, Movement{}, err
	}
	normalizedPRURL, err := NormalizePRURL(prURL)
	if err != nil {
		return Task{}, Delivery{}, Movement{}, err
	}
	if actor.Kind == Agent && !IsLive(*lease, now) {
		return Task{}, Delivery{}, Movement{}, ruleError(CodeLeaseRequired, "the lease has expired")
	}

	from := t.State
	next := t
	next.State = Review
	next.Version = t.Version + 1
	next.UpdatedAt = now
	next.ClosedAt = nil

	delivery := Delivery{
		TaskKey:   t.Key,
		Summary:   summary,
		Evidence:  slices.Clone(evidence),
		PRURL:     normalizedPRURL,
		CreatedAt: now,
		ActorID:   actor.ID,
	}
	if lease != nil {
		leaseID := lease.ID
		delivery.LeaseID = &leaseID
	}

	return next, delivery, Movement{
		TaskKey:     t.Key,
		FromState:   &from,
		ToState:     Review,
		ActorID:     actor.ID,
		TaskVersion: next.Version,
		At:          now,
	}, nil
}

func ValidateEvidence(evidence []string) error {
	if len(evidence) < 1 || len(evidence) > 20 {
		return invalidInput("evidence must contain between 1 and 20 entries")
	}
	for i, entry := range evidence {
		if strings.TrimSpace(entry) == "" {
			return invalidInput(fmt.Sprintf("evidence entry %d is blank", i+1))
		}
		if utf8.RuneCountInString(entry) > 4000 {
			return invalidInput(fmt.Sprintf("evidence entry %d exceeds 4000 characters", i+1))
		}
	}
	return nil
}

func NormalizePRURL(raw *string) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(value) > 500 {
		return nil, invalidInput("pr_url must be an absolute http(s) URL of at most 500 characters")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, invalidInput("pr_url must be an absolute http(s) URL of at most 500 characters")
	}
	return &value, nil
}

func ValidateComment(body string) error {
	if strings.TrimSpace(body) == "" {
		return invalidInput("comment body is required")
	}
	if utf8.RuneCountInString(body) > 10000 {
		return invalidInput("comment body must be at most 10000 characters")
	}
	return nil
}

func CheckVersion(t Task, ifMatch int64, present bool) error {
	if !present {
		return ruleError(CodePreconditionRequired, "If-Match is required")
	}
	if ifMatch != t.Version {
		return ruleError(CodeStaleVersion,
			fmt.Sprintf("If-Match %d does not match task version %d", ifMatch, t.Version))
	}
	return nil
}

func transitionAllowed(from, to State, kind ActorKind) bool {
	return slices.Contains(AllowedTransitions(from, kind), to)
}

func optionalText(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}
