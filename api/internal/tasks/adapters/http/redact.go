package http

import (
	"regexp"

	"mise/api/internal/githubinfo"
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
	items := make([]evidenceItemJSON, len(item.EvidenceItems))
	for index, evidenceItem := range item.EvidenceItems {
		items[index] = evidenceItemJSON{
			Label: redactedOptional(evidenceItem.Label),
			Raw:   redactedText(evidenceItem.Raw),
		}
	}
	item.EvidenceItems = items
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

func redactedAnchors(body anchorsJSON) anchorsJSON {
	body.TaskKey = redactedText(body.TaskKey)
	body.Main = redactedStringEnvelope(body.Main)
	switch pr := body.PR.(type) {
	case linkPRJSON:
		pr.URL = redactedText(pr.URL)
		body.PR = pr
	case githubPRJSON:
		pr.URL = redactedText(pr.URL)
		pr.State = redactedStateEnvelope(pr.State)
		pr.MergeSHA = redactedStringEnvelope(pr.MergeSHA)
		pr.Checks = redactedChecksEnvelope(pr.Checks)
		body.PR = pr
	}
	return body
}

func redactedStateEnvelope(envelope githubinfo.Envelope[string]) githubinfo.Envelope[string] {
	envelope.Data = redactedText(envelope.Data)
	return envelope
}

func redactedStringEnvelope(envelope githubinfo.Envelope[*string]) githubinfo.Envelope[*string] {
	if envelope.Data != nil {
		value := redactedText(*envelope.Data)
		envelope.Data = &value
	}
	return envelope
}

func redactedChecksEnvelope(envelope githubinfo.Envelope[*githubinfo.Checks]) githubinfo.Envelope[*githubinfo.Checks] {
	if envelope.Data == nil {
		return envelope
	}
	checks := *envelope.Data
	checks.SHA = redactedText(checks.SHA)
	checks.Context = redactedText(checks.Context)
	checks.Jobs = make([]githubinfo.Job, len(envelope.Data.Jobs))
	for index, job := range envelope.Data.Jobs {
		job.Name = redactedText(job.Name)
		job.Status = redactedText(job.Status)
		if job.Conclusion != nil {
			value := redactedText(*job.Conclusion)
			job.Conclusion = &value
		}
		if job.URL != nil {
			value := redactedText(*job.URL)
			job.URL = &value
		}
		checks.Jobs[index] = job
	}
	envelope.Data = &checks
	return envelope
}
