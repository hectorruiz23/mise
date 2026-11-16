package http

import (
	"regexp"

	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/domain"
)

const redactedPath = "<path>"

var privatePathPattern = regexp.MustCompile(
	"(?:/home/[A-Za-z0-9._-]+|/Users/[A-Za-z0-9._-]+)(?:/[^`\"'()\\[\\],;<>|\\n]*)?|~/(?:[^`\"'()\\[\\],;<>|\\n ]*)")

func isViewer(principal httpserver.Principal) bool {
	return principal.Kind == string(domain.Viewer)
}

func redactedText(value string) string {
	if value == "" {
		return value
	}
	return privatePathPattern.ReplaceAllString(value, redactedPath)
}

func redactedOptional(value *string) *string {
	if value == nil {
		return nil
	}
	redacted := redactedText(*value)
	return &redacted
}

func redactedTask(item taskJSON) taskJSON {
	item.Title = redactedText(item.Title)
	item.Notes = redactedOptional(item.Notes)
	item.Acceptance = redactedOptional(item.Acceptance)
	return item
}

func redactedTasks(items []taskJSON) []taskJSON {
	for index := range items {
		items[index] = redactedTask(items[index])
	}
	return items
}

func redactedMovement(item movementJSON) movementJSON {
	item.Reason = redactedOptional(item.Reason)
	return item
}

func redactedMovements(items []movementJSON) []movementJSON {
	for index := range items {
		items[index] = redactedMovement(items[index])
	}
	return items
}

func redactedDelivery(item deliveryJSON) deliveryJSON {
	item.Summary = redactedText(item.Summary)
	evidence := make([]string, len(item.Evidence))
	for index, line := range item.Evidence {
		evidence[index] = redactedText(line)
	}
	item.Evidence = evidence
	return item
}

func redactedDeliveries(items []deliveryJSON) []deliveryJSON {
	for index := range items {
		items[index] = redactedDelivery(items[index])
	}
	return items
}

func redactedComment(item commentJSON) commentJSON {
	item.Body = redactedText(item.Body)
	return item
}

func redactedComments(items []commentJSON) []commentJSON {
	for index := range items {
		items[index] = redactedComment(items[index])
	}
	return items
}

func redactedDetail(detail detailJSON) detailJSON {
	detail.Task = redactedTask(detail.Task)
	detail.Movements = redactedMovements(detail.Movements)
	detail.Deliveries = redactedDeliveries(detail.Deliveries)
	detail.Comments = redactedComments(detail.Comments)
	return detail
}
