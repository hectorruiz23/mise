package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	nethttp "net/http"
	"strconv"
	"strings"

	"mise/api/internal/kernel"
	"mise/api/internal/platform/httpserver"
	"mise/api/internal/tasks/app"
	"mise/api/internal/tasks/domain"
)

const maxIdempotencyKeyLength = 128

type Handler struct {
	service *app.Service
}

func New(service *app.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Create(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		httpserver.WriteError(w, nethttp.StatusPreconditionRequired,
			kernel.CodePreconditionRequired, "Idempotency-Key is required")
		return
	}
	if len(key) > maxIdempotencyKeyLength {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"Idempotency-Key must be at most 128 characters")
		return
	}

	var body createRequest
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}

	priority := domain.Priority(body.Priority)
	if priority == "" {
		priority = domain.Normal
	}
	var size *domain.Size
	if body.Size != nil {
		value := domain.Size(*body.Size)
		size = &value
	}

	task, _, err := h.service.Create(r.Context(), app.CreateParams{
		ProjectKey:     body.Project,
		Title:          body.Title,
		Notes:          body.Notes,
		Acceptance:     body.Acceptance,
		Priority:       priority,
		Size:           size,
		Actor:          actorOf(principal),
		IdempotencyKey: key,
		Digest:         digestOf(body, priority),
	})
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	w.Header().Set("Location", "/api/tasks/"+task.Key)
	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	httpserver.WriteJSON(w, nethttp.StatusCreated, taskOf(task))
}

func (h *Handler) Patch(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	ifMatch, present, err := parseIfMatch(r)
	if err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"If-Match must be a quoted task version")
		return
	}
	if !present {
		httpserver.WriteError(w, nethttp.StatusPreconditionRequired,
			kernel.CodePreconditionRequired, "If-Match is required")
		return
	}

	var body patchRequest
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}
	patch, err := body.toPatch()
	if err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput, err.Error())
		return
	}
	if principal.Kind != string(domain.Human) && (patch.HasFieldEdits() || patch.State == nil) {
		httpserver.WriteError(w, nethttp.StatusForbidden, kernel.CodeForbidden,
			"only a human may edit task fields")
		return
	}

	task, err := h.service.Update(r.Context(), r.PathValue("key"), patch,
		actorOf(principal), ifMatch, present)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	httpserver.WriteJSON(w, nethttp.StatusOK, taskOf(task))
}

func (h *Handler) List(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	query := r.URL.Query()
	filter := app.Filter{
		ProjectKey: query.Get("project"),
		Cursor:     query.Get("cursor"),
	}
	if state := query.Get("state"); state != "" {
		value := domain.State(state)
		if !value.Valid() {
			httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
				"unknown state filter")
			return
		}
		filter.State = value
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
				"limit must be a positive integer")
			return
		}
		filter.Limit = limit
	}

	tasks, nextCursor, err := h.service.List(r.Context(), filter)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	items := make([]taskJSON, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskOf(task))
	}
	httpserver.WriteJSON(w, nethttp.StatusOK, struct {
		Tasks      []taskJSON `json:"tasks"`
		NextCursor string     `json:"next_cursor"`
	}{Tasks: items, NextCursor: nextCursor})
}

func (h *Handler) Board(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	board, err := h.service.Board(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	columns := make(map[string][]taskJSON, len(board.Columns))
	counts := make(map[string]int, len(board.Counts))
	for _, state := range []domain.State{
		domain.Backlog, domain.Ready, domain.InProgress,
		domain.Blocked, domain.Review, domain.Done,
	} {
		tasks := board.Columns[state]
		items := make([]taskJSON, 0, len(tasks))
		for _, task := range tasks {
			items = append(items, taskOf(task))
		}
		columns[string(state)] = items
		counts[string(state)] = board.Counts[state]
	}

	httpserver.WriteJSON(w, nethttp.StatusOK, struct {
		Columns     map[string][]taskJSON `json:"columns"`
		Counts      map[string]int        `json:"counts"`
		GeneratedAt int64                 `json:"generated_at"`
	}{Columns: columns, Counts: counts, GeneratedAt: board.GeneratedAt})
}

func (h *Handler) Projects(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	projects, err := h.service.ListProjects(r.Context())
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	items := make([]projectJSON, 0, len(projects))
	for _, project := range projects {
		items = append(items, projectJSON{Key: project.Key, Name: project.Name})
	}
	httpserver.WriteJSON(w, nethttp.StatusOK, items)
}

func (h *Handler) CreateProject(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	var body createProjectRequest
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}

	project, err := h.service.CreateProject(r.Context(), app.CreateProjectParams{
		Key:   body.Key,
		Name:  body.Name,
		Actor: actorOf(principal),
	})
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	httpserver.WriteJSON(w, nethttp.StatusCreated, projectJSON{
		Key:  project.Key,
		Name: project.Name,
	})
}

func (h *Handler) Detail(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	detail, err := h.service.Detail(r.Context(), r.PathValue("key"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	httpserver.WriteJSON(w, nethttp.StatusOK, detailJSON{
		Task:       taskOf(detail.Task),
		Movements:  movementsOf(detail.Movements),
		Leases:     leasesOf(detail.Leases),
		Deliveries: deliveriesOf(detail.Deliveries),
		Comments:   commentsOf(detail.Comments),
	})
}

func (h *Handler) Movements(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	movements, err := h.service.Movements(r.Context(), r.PathValue("key"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, nethttp.StatusOK, movementsOf(movements))
}

func (h *Handler) ListLeases(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	leases, err := h.service.Leases(r.Context(), r.PathValue("key"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, nethttp.StatusOK, leasesOf(leases))
}

func (h *Handler) AcquireLease(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	task, lease, err := h.service.Acquire(r.Context(), r.PathValue("key"), actorOf(principal))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	httpserver.WriteJSON(w, nethttp.StatusCreated, leaseOf(lease))
}

func (h *Handler) RenewLease(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	leaseID, ok := pathID(w, r)
	if !ok {
		return
	}

	key := r.PathValue("key")
	lease, err := h.service.Renew(r.Context(), key, leaseID, actorOf(principal))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	task, err := h.service.Get(r.Context(), key)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	httpserver.WriteJSON(w, nethttp.StatusOK, leaseOf(lease))
}

func (h *Handler) ReleaseLease(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	leaseID, ok := pathID(w, r)
	if !ok {
		return
	}

	key := r.PathValue("key")
	if err := h.service.Release(r.Context(), key, leaseID, actorOf(principal)); err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	task, err := h.service.Get(r.Context(), key)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	w.WriteHeader(nethttp.StatusNoContent)
}

func (h *Handler) ListDeliveries(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	deliveries, err := h.service.Deliveries(r.Context(), r.PathValue("key"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, nethttp.StatusOK, deliveriesOf(deliveries))
}

func (h *Handler) CreateDelivery(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	var body deliveryRequest
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}

	task, delivery, err := h.service.Deliver(r.Context(), r.PathValue("key"),
		actorOf(principal), body.LeaseID, body.Summary, body.Evidence, body.PRURL)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	httpserver.WriteJSON(w, nethttp.StatusCreated, struct {
		Delivery deliveryJSON `json:"delivery"`
		Task     taskJSON     `json:"task"`
	}{Delivery: deliveryOf(delivery), Task: taskOf(task)})
}

func (h *Handler) ListComments(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := authenticated(w, r); !ok {
		return
	}

	comments, err := h.service.Comments(r.Context(), r.PathValue("key"))
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	httpserver.WriteJSON(w, nethttp.StatusOK, commentsOf(comments))
}

func (h *Handler) CreateComment(w nethttp.ResponseWriter, r *nethttp.Request) {
	principal, ok := authenticated(w, r)
	if !ok {
		return
	}

	var body commentRequest
	if err := decodeStrict(r, &body); err != nil {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"invalid JSON body")
		return
	}

	key := r.PathValue("key")
	comment, err := h.service.AddComment(r.Context(), key, actorOf(principal), body.Body)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}

	task, err := h.service.Get(r.Context(), key)
	if err != nil {
		httpserver.WriteRuleError(w, err)
		return
	}
	w.Header().Set("X-Task-Version", strconv.FormatInt(task.Version, 10))
	httpserver.WriteJSON(w, nethttp.StatusCreated, commentOf(comment))
}

func pathID(w nethttp.ResponseWriter, r *nethttp.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		httpserver.WriteError(w, nethttp.StatusBadRequest, kernel.CodeInvalidInput,
			"lease id must be a positive integer")
		return 0, false
	}
	return id, true
}

type createRequest struct {
	Title      string  `json:"title"`
	Notes      *string `json:"notes"`
	Acceptance *string `json:"acceptance"`
	Project    string  `json:"project"`
	Priority   string  `json:"priority"`
	Size       *string `json:"size"`
}

type digestPayload struct {
	Project    string  `json:"project"`
	Title      string  `json:"title"`
	Notes      *string `json:"notes"`
	Acceptance *string `json:"acceptance"`
	Priority   string  `json:"priority"`
	Size       *string `json:"size"`
}

func digestOf(body createRequest, priority domain.Priority) string {
	payload := digestPayload{
		Project:    body.Project,
		Title:      body.Title,
		Notes:      normalizedText(body.Notes),
		Acceptance: normalizedText(body.Acceptance),
		Priority:   string(priority),
		Size:       normalizedSize(body.Size),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func normalizedText(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	copied := *value
	return &copied
}

func normalizedSize(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

type patchRequest struct {
	Title      json.RawMessage `json:"title"`
	Notes      json.RawMessage `json:"notes"`
	Acceptance json.RawMessage `json:"acceptance"`
	Priority   json.RawMessage `json:"priority"`
	Size       json.RawMessage `json:"size"`
	State      json.RawMessage `json:"state"`
	Reason     json.RawMessage `json:"reason"`
}

func (b patchRequest) toPatch() (app.Patch, error) {
	var patch app.Patch

	if len(b.Title) > 0 {
		value, err := requiredString(b.Title)
		if err != nil {
			return app.Patch{}, errors.New("title must be a string")
		}
		patch.Title = &value
	}
	if len(b.Notes) > 0 {
		value, err := nullableString(b.Notes)
		if err != nil {
			return app.Patch{}, errors.New("notes must be a string or null")
		}
		patch.Notes, patch.NotesSet = value, true
	}
	if len(b.Acceptance) > 0 {
		value, err := nullableString(b.Acceptance)
		if err != nil {
			return app.Patch{}, errors.New("acceptance must be a string or null")
		}
		patch.Acceptance, patch.AcceptSet = value, true
	}
	if len(b.Priority) > 0 {
		value, err := requiredString(b.Priority)
		if err != nil {
			return app.Patch{}, errors.New("priority must be a string")
		}
		priority := domain.Priority(value)
		patch.Priority = &priority
	}
	if len(b.Size) > 0 {
		value, err := nullableString(b.Size)
		if err != nil {
			return app.Patch{}, errors.New("size must be a string or null")
		}
		if value == nil {
			patch.SizeSet = true
		} else {
			size := domain.Size(*value)
			patch.Size, patch.SizeSet = &size, true
		}
	}
	if len(b.State) > 0 {
		value, err := requiredString(b.State)
		if err != nil {
			return app.Patch{}, errors.New("state must be a string")
		}
		state := domain.State(value)
		if !state.Valid() {
			return app.Patch{}, errors.New("unknown state")
		}
		patch.State = &state
	}
	if len(b.Reason) > 0 {
		value, err := nullableString(b.Reason)
		if err != nil {
			return app.Patch{}, errors.New("reason must be a string or null")
		}
		if value != nil {
			patch.Reason = *value
		}
	}

	return patch, nil
}

func parseIfMatch(r *nethttp.Request) (int64, bool, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, false, nil
	}
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, true, errors.New("If-Match is not a quoted version")
	}
	inner := raw[1 : len(raw)-1]
	if inner == "" || strings.Contains(inner, "\"") {
		return 0, true, errors.New("If-Match is not a quoted version")
	}
	version, err := strconv.ParseInt(inner, 10, 64)
	if err != nil || version < 1 {
		return 0, true, errors.New("If-Match is not a positive version")
	}
	return version, true, nil
}

func requiredString(raw json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("null is not a string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value, nil
}

func nullableString(raw json.RawMessage) (*string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeStrict(r *nethttp.Request, dst any) error {
	if r.Body == nil {
		return errors.New("body required")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("body must contain exactly one JSON object")
	}
	return nil
}

func actorOf(principal httpserver.Principal) domain.Actor {
	return domain.Actor{
		ID:   principal.ID,
		Kind: domain.ActorKind(principal.Kind),
		Name: principal.Name,
	}
}

func actorOfDomain(actor domain.Actor) actorJSON {
	return actorJSON{
		ID:   actor.ID,
		Name: actor.Name,
		Kind: string(actor.Kind),
	}
}

func authenticated(w nethttp.ResponseWriter, r *nethttp.Request) (httpserver.Principal, bool) {
	principal, ok := httpserver.PrincipalFrom(r.Context())
	if !ok {
		httpserver.WriteError(w, nethttp.StatusUnauthorized,
			kernel.CodeUnauthorized, "credential required")
		return httpserver.Principal{}, false
	}
	return principal, true
}

type taskJSON struct {
	Key        string  `json:"key"`
	Project    string  `json:"project"`
	Title      string  `json:"title"`
	Notes      *string `json:"notes"`
	Acceptance *string `json:"acceptance"`
	State      string  `json:"state"`
	Priority   string  `json:"priority"`
	Size       *string `json:"size"`
	CreatedBy  int64   `json:"created_by"`
	Version    int64   `json:"version"`
	CreatedAt  int64   `json:"created_at"`
	UpdatedAt  int64   `json:"updated_at"`
	ClosedAt   *int64  `json:"closed_at"`
}

type projectJSON struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type createProjectRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type detailJSON struct {
	Task       taskJSON       `json:"task"`
	Movements  []movementJSON `json:"movements"`
	Leases     []leaseJSON    `json:"leases"`
	Deliveries []deliveryJSON `json:"deliveries"`
	Comments   []commentJSON  `json:"comments"`
}

type movementJSON struct {
	TaskKey     string    `json:"task_key"`
	FromState   *string   `json:"from_state"`
	ToState     string    `json:"to_state"`
	ActorID     int64     `json:"actor_id"`
	Actor       actorJSON `json:"actor"`
	Reason      *string   `json:"reason"`
	TaskVersion int64     `json:"task_version"`
	At          int64     `json:"at"`
}

type leaseJSON struct {
	ID           int64     `json:"id"`
	TaskKey      string    `json:"task_key"`
	AgentID      int64     `json:"agent_id"`
	Agent        actorJSON `json:"agent"`
	CreatedAt    int64     `json:"created_at"`
	ExpiresAt    int64     `json:"expires_at"`
	ReleasedAt   *int64    `json:"released_at"`
	ClosedReason *string   `json:"closed_reason"`
}

type deliveryJSON struct {
	ID        int64     `json:"id"`
	TaskKey   string    `json:"task_key"`
	LeaseID   *int64    `json:"lease_id"`
	Summary   string    `json:"summary"`
	Evidence  []string  `json:"evidence"`
	PRURL     *string   `json:"pr_url"`
	CreatedAt int64     `json:"created_at"`
	ActorID   int64     `json:"actor_id"`
	Actor     actorJSON `json:"actor"`
}

type commentJSON struct {
	ID        int64     `json:"id"`
	TaskKey   string    `json:"task_key"`
	ActorID   int64     `json:"actor_id"`
	Actor     actorJSON `json:"actor"`
	Body      string    `json:"body"`
	CreatedAt int64     `json:"created_at"`
}

type actorJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type deliveryRequest struct {
	LeaseID  *int64   `json:"lease_id"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
	PRURL    *string  `json:"pr_url"`
}

type commentRequest struct {
	Body string `json:"body"`
}

func movementsOf(movements []domain.Movement) []movementJSON {
	items := make([]movementJSON, 0, len(movements))
	for _, movement := range movements {
		var from *string
		if movement.FromState != nil {
			value := string(*movement.FromState)
			from = &value
		}
		items = append(items, movementJSON{
			TaskKey:     movement.TaskKey,
			FromState:   from,
			ToState:     string(movement.ToState),
			ActorID:     movement.ActorID,
			Actor:       actorOfDomain(movement.Actor),
			Reason:      movement.Reason,
			TaskVersion: movement.TaskVersion,
			At:          movement.At,
		})
	}
	return items
}

func leasesOf(leases []domain.Lease) []leaseJSON {
	items := make([]leaseJSON, 0, len(leases))
	for _, lease := range leases {
		items = append(items, leaseOf(lease))
	}
	return items
}

func leaseOf(lease domain.Lease) leaseJSON {
	return leaseJSON{
		ID:           lease.ID,
		TaskKey:      lease.TaskKey,
		AgentID:      lease.AgentID,
		Agent:        actorOfDomain(lease.Agent),
		CreatedAt:    lease.CreatedAt,
		ExpiresAt:    lease.ExpiresAt,
		ReleasedAt:   lease.ReleasedAt,
		ClosedReason: lease.ClosedReason,
	}
}

func deliveriesOf(deliveries []domain.Delivery) []deliveryJSON {
	items := make([]deliveryJSON, 0, len(deliveries))
	for _, delivery := range deliveries {
		items = append(items, deliveryOf(delivery))
	}
	return items
}

func deliveryOf(delivery domain.Delivery) deliveryJSON {
	return deliveryJSON{
		ID:        delivery.ID,
		TaskKey:   delivery.TaskKey,
		LeaseID:   delivery.LeaseID,
		Summary:   delivery.Summary,
		Evidence:  delivery.Evidence,
		PRURL:     delivery.PRURL,
		CreatedAt: delivery.CreatedAt,
		ActorID:   delivery.ActorID,
		Actor:     actorOfDomain(delivery.Actor),
	}
}

func commentsOf(comments []domain.Comment) []commentJSON {
	items := make([]commentJSON, 0, len(comments))
	for _, comment := range comments {
		items = append(items, commentOf(comment))
	}
	return items
}

func commentOf(comment domain.Comment) commentJSON {
	return commentJSON{
		ID:        comment.ID,
		TaskKey:   comment.TaskKey,
		ActorID:   comment.ActorID,
		Actor:     actorOfDomain(comment.Actor),
		Body:      comment.Body,
		CreatedAt: comment.CreatedAt,
	}
}

func taskOf(task domain.Task) taskJSON {
	var size *string
	if task.Size != nil {
		value := string(*task.Size)
		size = &value
	}
	return taskJSON{
		Key:        task.Key,
		Project:    task.ProjectKey,
		Title:      task.Title,
		Notes:      task.Notes,
		Acceptance: task.Acceptance,
		State:      string(task.State),
		Priority:   string(task.Priority),
		Size:       size,
		CreatedBy:  task.CreatedBy,
		Version:    task.Version,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
		ClosedAt:   task.ClosedAt,
	}
}
