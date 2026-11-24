package http

import (
	"net/http"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

func (h *Handler) PatchProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	if principal.Kind != string(domain.Human) {
		httpserver.WriteError(w, http.StatusForbidden, kernel.CodeForbidden,
			"only a human may change a project")
		return
	}
	var body struct {
		GitHub         *app.PlanPolicy `json:"github"`
		RequiredChecks *[]string       `json:"required_checks"`
		AutoDispatch   *bool           `json:"auto_dispatch"`
		DispatchLimit  *int            `json:"dispatch_limit"`
		AutoMerge      *bool           `json:"auto_merge"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	err := h.service.UpdateProjectSettings(r.Context(), actorOf(principal), r.PathValue("key"),
		app.ProjectSettings{
			GitHub:         body.GitHub,
			RequiredChecks: body.RequiredChecks,
			AutoDispatch:   body.AutoDispatch,
			DispatchLimit:  body.DispatchLimit,
			AutoMerge:      body.AutoMerge,
		})
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CreateSubmission(w http.ResponseWriter, r *http.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}
	var body struct {
		PRURL   *string `json:"pr_url"`
		HeadSHA string  `json:"head_sha"`
	}
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, kernel.CodeInvalidInput, "invalid JSON body")
		return
	}
	submission, err := h.service.DeclareSubmission(r.Context(), actorOf(principal),
		r.PathValue("key"), body.PRURL, body.HeadSHA)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, submissionOf(submission))
}

type submissionJSON struct {
	TaskKey     string      `json:"task_key"`
	PRURL       string      `json:"pr_url"`
	HeadSHA     string      `json:"head_sha"`
	Verdict     string      `json:"verdict"`
	Required    []string    `json:"required_checks"`
	Checks      []checkJSON `json:"checks"`
	DeclaredAt  int64       `json:"declared_at"`
	DeliveredAt *int64      `json:"delivered_at"`
	CheckedAt   *int64      `json:"checked_at"`
}

type checkJSON struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

func submissionOf(submission domain.Submission) submissionJSON {
	checks := make([]checkJSON, 0, len(submission.Checks))
	for _, check := range submission.Checks {
		checks = append(checks, checkJSON{
			Name:       check.Name,
			State:      string(domain.CheckStateOf(check)),
			Status:     check.Status,
			Conclusion: check.Conclusion,
			URL:        check.URL,
		})
	}
	required := submission.Required
	if required == nil {
		required = []string{}
	}
	return submissionJSON{
		TaskKey:     submission.TaskKey,
		PRURL:       submission.PRURL,
		HeadSHA:     submission.HeadSHA,
		Verdict:     submission.Verdict,
		Required:    required,
		Checks:      checks,
		DeclaredAt:  submission.DeclaredAt,
		DeliveredAt: submission.DeliveredAt,
		CheckedAt:   submission.CheckedAt,
	}
}

func submissionsOf(submissions []domain.Submission) []submissionJSON {
	body := make([]submissionJSON, 0, len(submissions))
	for _, submission := range submissions {
		body = append(body, submissionOf(submission))
	}
	return body
}
