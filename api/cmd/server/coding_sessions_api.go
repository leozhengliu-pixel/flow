package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/codehost/gitlab"
	"flow/api/internal/coding"
	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// Session, settings, models, usage and status routes for the web app (docs/coding-sessions-design.md §7.1).
//
// Owner: track B (session core). Keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerCodingSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/coding/status", s.getCodingStatus)
	mux.HandleFunc("GET /api/coding/settings", s.getCodingSettings)
	mux.HandleFunc("PATCH /api/coding/settings", s.updateCodingSettings)
	mux.HandleFunc("GET /api/coding/models", s.listCodingModels)
	mux.HandleFunc("GET /api/coding/usage", s.getCodingUsage)
	mux.HandleFunc("GET /api/coding/sessions", s.listCodingSessions)
	mux.HandleFunc("POST /api/coding/sessions", s.createCodingSession)
	mux.HandleFunc("GET /api/coding/sessions/{id}", s.getCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/prompt", s.promptCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/stop", s.stopCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/restart", s.restartCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/approve", s.approveCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/dismiss", s.dismissCodingSession)
	mux.HandleFunc("POST /api/coding/sessions/{id}/pull-request", s.createCodingSessionPullRequest)
	mux.HandleFunc("GET /api/coding/sessions/{id}/sandboxes/{sandboxId}/logs", s.getCodingSandboxLogs)
}

// codingErrorStatus maps a coding error to an HTTP status and a stable code.
func codingErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, coding.ErrNotFound):
		return http.StatusNotFound, "notFound"
	case errors.Is(err, coding.ErrInvalidInput):
		return http.StatusBadRequest, "invalidInput"
	case errors.Is(err, coding.ErrQuotaExceeded):
		return http.StatusPaymentRequired, string(domain.CodingErrQuotaExceeded)
	case errors.Is(err, coding.ErrConflict), errors.Is(err, coding.ErrVersionConflict), errors.Is(err, coding.ErrAlreadyUsed):
		return http.StatusConflict, "conflict"
	case errors.Is(err, coding.ErrDisabled):
		return http.StatusNotFound, string(domain.CodingErrSandboxDisabled)
	case errors.Is(err, coding.ErrNoAgent):
		return http.StatusConflict, "agentUnavailable"
	case errors.Is(err, coding.ErrTokenInvalid):
		return http.StatusUnauthorized, "tokenInvalid"
	case errors.Is(err, coding.ErrGone):
		return http.StatusGone, "gone"
	case errors.Is(err, coding.ErrForbidden), errors.Is(err, store.ErrAuthForbidden):
		return http.StatusForbidden, string(domain.CodingErrPermissionDenied)
	case errors.Is(err, codehost.ErrNotConnected), errors.Is(err, codehost.ErrNoAccess), errors.Is(err, codehost.ErrUserNoAccess):
		return http.StatusForbidden, string(codehost.ErrorCode("", err))
	case errors.Is(err, codehost.ErrTransient), errors.Is(err, sandbox.ErrTransient):
		return http.StatusServiceUnavailable, "transient"
	}
	return http.StatusInternalServerError, string(domain.CodingErrInternal)
}

func writeCodingError(w http.ResponseWriter, err error) {
	status, code := codingErrorStatus(err)
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "Coding sessions: internal error"
	}
	writeJSON(w, status, map[string]string{"error": strings.TrimPrefix(message, "coding: "), "code": code})
}

// codingViewer is the signed-in member and their workspace role.
type codingViewer struct {
	workspace string
	user      domain.User
	role      string
	data      domain.Bootstrap
}

func (v codingViewer) admin() bool { return workspaceAdminRole(v.role) }
func (v codingViewer) guest() bool { return v.role == "guest" }

// codingViewerFor resolves the caller; it writes the error response itself.
func (s *server) codingViewerFor(w http.ResponseWriter, r *http.Request) (codingViewer, bool) {
	if s.coding == nil || s.coding.service == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Coding sessions are not available", "code": string(domain.CodingErrSandboxDisabled)})
		return codingViewer{}, false
	}
	data, ok := s.store.WorkspaceSettingsMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return codingViewer{}, false
	}
	viewer := codingViewer{workspace: data.Workspace.URLKey, user: requestActor(s, r), data: data, role: "admin"}
	if !s.authDisabled {
		viewer.user = authUser(r)
		role, status, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, viewer.user.ID)
		if err != nil || status != "active" {
			writeError(w, http.StatusForbidden, "Workspace access required")
			return codingViewer{}, false
		}
		viewer.role = role
		if viewer.user.App {
			viewer.role = "app"
		}
	}
	if viewer.user.ID == "" {
		writeError(w, http.StatusUnauthorized, "Sign in required")
		return codingViewer{}, false
	}
	return viewer, true
}

// codingSessionAccess loads a session the viewer may see (guests only their
// own); editing additionally requires a member, the creator or an admin.
func (s *server) codingSessionAccess(w http.ResponseWriter, r *http.Request, editing bool) (codingViewer, domain.CodingSession, bool) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return viewer, domain.CodingSession{}, false
	}
	session, err := s.coding.service.Get(r.Context(), viewer.workspace, r.PathValue("id"))
	if err == nil && viewer.guest() && session.CreatorID != viewer.user.ID {
		err = coding.ErrNotFound
	}
	if err == nil && editing && viewer.guest() && session.CreatorID != viewer.user.ID {
		err = coding.ErrForbidden
	}
	if err != nil {
		writeCodingError(w, err)
		return viewer, session, false
	}
	return viewer, session, true
}

// codingSettingsView is the web CodingSettings document.
type codingSettingsView struct {
	Enabled                bool     `json:"enabled"`
	Harness                string   `json:"harness"`
	Model                  string   `json:"model"`
	ReasoningEffort        string   `json:"reasoningEffort,omitempty"`
	RequireSignedCommits   bool     `json:"requireSignedCommits"`
	PinnedRegion           string   `json:"pinnedRegion,omitempty"`
	TrustedExternalSources []string `json:"trustedExternalSources"`
	LoopRepositoryAccess   string   `json:"loopRepositoryAccess"`
	LoopRepositories       []string `json:"loopRepositories"`
}

func codingSettingsFor(settings domain.WorkspaceSettings) codingSettingsView {
	policy := coding.NormalizeAgentSettings(settings.CodingAgentSettings)
	return codingSettingsView{Enabled: settings.FeatureFlags[coding.FeatureFlag], Harness: policy.Harness, Model: policy.Model, ReasoningEffort: policy.ReasoningEffort, RequireSignedCommits: policy.CommitSigningEnabled, PinnedRegion: policy.PinnedRegion, TrustedExternalSources: policy.TrustedExternalSources, LoopRepositoryAccess: policy.LoopRepositoryAccess, LoopRepositories: policy.LoopRepositories}
}

// normalizeCodingAgentSettings validates and normalizes the coding policy a
// workspace preferences patch saves (settings_features.go calls it).
func (s *server) normalizeCodingAgentSettings(settings *domain.CodingAgentSettings) error {
	config := s.codingConfig()
	if err := coding.ValidateAgentSettings(*settings, config); err != nil {
		return fmt.Errorf("%w: %s", errInvalid, strings.TrimPrefix(err.Error(), "coding: invalid input: "))
	}
	*settings = coding.NormalizeAgentSettings(*settings)
	return nil
}

func (s *server) codingConfig() appconfig.CodingConfig {
	if s.coding == nil {
		return appconfig.CodingConfig{}
	}
	return s.coding.config
}

// getCodingStatus answers GET /api/coding/status.
func (s *server) getCodingStatus(w http.ResponseWriter, r *http.Request) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return
	}
	config := s.coding.config
	sizes := map[string]map[string]string{}
	for name, size := range config.Sizes {
		sizes[name] = map[string]string{"cpu": size.CPU, "memory": size.Memory}
	}
	regions := config.Regions
	if regions == nil || config.Provider == "docker" {
		regions = []string{}
	}
	hosts := []map[string]any{}
	for _, kind := range []string{domain.CodeHostGitHub, domain.CodeHostGitLab} {
		host, exists := s.coding.hosts[kind]
		if !exists {
			continue
		}
		connected, _ := host.Connected(r.Context(), viewer.workspace)
		hostName := "gitlab.com"
		if kind == domain.CodeHostGitHub {
			hostName = config.GitHubApp.Host
			if hostName == "" {
				hostName = "github.com"
			}
		}
		entry := map[string]any{"codeHost": kind, "hostName": hostName, "connected": connected}
		if gitlabHost, ok := host.(*gitlab.Host); ok {
			// FLOW_CODING_GITLAB_CREDENTIAL_MODE, so settings can show the
			// matching requirements and warnings.
			entry["credentialMode"] = gitlabHost.CredentialMode()
		}
		hosts = append(hosts, entry)
	}
	preflight := []map[string]any{}
	if viewer.admin() {
		for _, check := range s.coding.preflight {
			preflight = append(preflight, map[string]any{"name": check.Name, "ok": check.OK, "warning": check.Warning, "message": check.Message})
		}
	}
	strategy := config.Snapshot.Strategy
	if strategy == "" {
		strategy = "archive"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": s.codingEnabled() && viewer.data.WorkspaceSettings.FeatureFlags[coding.FeatureFlag], "provider": s.coding.sandbox.Name(), "snapshotStrategy": strategy,
		"regions": regions, "sizes": sizes, "codeHosts": hosts, "debugTerminal": config.DebugTerminal, "browserTesting": config.BrowserEnabled, "preflight": preflight,
	})
}

// getCodingSettings answers GET /api/coding/settings.
func (s *server) getCodingSettings(w http.ResponseWriter, r *http.Request) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, codingSettingsFor(viewer.data.WorkspaceSettings))
}

// updateCodingSettings answers PATCH /api/coding/settings (admins only).
func (s *server) updateCodingSettings(w http.ResponseWriter, r *http.Request) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return
	}
	if !viewer.admin() {
		writeError(w, http.StatusForbidden, "Workspace admin access required")
		return
	}
	var patch struct {
		Enabled                *bool     `json:"enabled"`
		Harness                *string   `json:"harness"`
		Model                  *string   `json:"model"`
		ReasoningEffort        *string   `json:"reasoningEffort"`
		RequireSignedCommits   *bool     `json:"requireSignedCommits"`
		PinnedRegion           *string   `json:"pinnedRegion"`
		TrustedExternalSources *[]string `json:"trustedExternalSources"`
		LoopRepositoryAccess   *string   `json:"loopRepositoryAccess"`
		LoopRepositories       *[]string `json:"loopRepositories"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if !decodeJSON(w, r, &patch) {
		return
	}
	var updated domain.WorkspaceSettings
	err := s.store.MutateWorkspace(r.Context(), viewer.workspace, "workspace_preferences.updated", "workspace", patch, func(data *domain.Bootstrap) error {
		settings := data.WorkspaceSettings.CodingAgentSettings
		set := func(target *string, value *string) {
			if value != nil {
				*target = strings.TrimSpace(*value)
			}
		}
		set(&settings.Harness, patch.Harness)
		set(&settings.Model, patch.Model)
		set(&settings.ReasoningEffort, patch.ReasoningEffort)
		set(&settings.PinnedRegion, patch.PinnedRegion)
		set(&settings.LoopRepositoryAccess, patch.LoopRepositoryAccess)
		if patch.RequireSignedCommits != nil {
			settings.CommitSigningEnabled = *patch.RequireSignedCommits
		}
		if patch.TrustedExternalSources != nil {
			settings.TrustedExternalSources = *patch.TrustedExternalSources
		}
		if patch.LoopRepositories != nil {
			settings.LoopRepositories = *patch.LoopRepositories
		}
		if err := s.normalizeCodingAgentSettings(&settings); err != nil {
			return err
		}
		data.WorkspaceSettings.CodingAgentSettings = settings
		if patch.Enabled != nil {
			flags := map[string]bool{}
			for key, value := range data.WorkspaceSettings.FeatureFlags {
				flags[key] = value
			}
			flags[coding.FeatureFlag] = *patch.Enabled
			data.WorkspaceSettings.FeatureFlags = flags
		}
		data.WorkspaceSettings.UpdatedAt = time.Now().UTC()
		updated = data.WorkspaceSettings
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, codingSettingsFor(updated))
}

// listCodingModels answers GET /api/coding/models.
func (s *server) listCodingModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.codingViewerFor(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, coding.ModelOptions(s.coding.config.LLM))
}

// getCodingUsage answers GET /api/coding/usage?since= (default: the last 30 days).
func (s *server) getCodingUsage(w http.ResponseWriter, r *http.Request) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return
	}
	since := time.Now().UTC().AddDate(0, 0, -30)
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "since must be an RFC 3339 time")
			return
		}
		since = parsed.UTC()
	}
	records, err := s.store.CodingUsage(r.Context(), viewer.workspace, since)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	summary := codingUsageSummary(records, since)
	if !viewer.admin() {
		// Members see their own spend only.
		summary.ByUser = map[string]int64{viewer.user.ID: summary.ByUser[viewer.user.ID]}
		summary.ByLoop = map[string]int64{}
	}
	writeJSON(w, http.StatusOK, summary)
}

type codingUsageView struct {
	Since             time.Time        `json:"since"`
	TotalCostMicros   int64            `json:"totalCostMicros"`
	LLMCostMicros     int64            `json:"llmCostMicros"`
	RuntimeCostMicros int64            `json:"runtimeCostMicros"`
	ByUser            map[string]int64 `json:"byUser"`
	ByLoop            map[string]int64 `json:"byLoop"`
	Sessions          int              `json:"sessions"`
}

func codingUsageSummary(records []domain.CodingUsageRecord, since time.Time) codingUsageView {
	summary := codingUsageView{Since: since, ByUser: map[string]int64{}, ByLoop: map[string]int64{}}
	sessions := map[string]bool{}
	for _, record := range records {
		summary.TotalCostMicros += record.CostMicros
		if record.Kind == coding.UsageLLM {
			summary.LLMCostMicros += record.CostMicros
		} else {
			summary.RuntimeCostMicros += record.CostMicros
		}
		if record.UserID != "" {
			summary.ByUser[record.UserID] += record.CostMicros
		}
		if record.LoopID != "" {
			summary.ByLoop[record.LoopID] += record.CostMicros
		}
		if record.SessionID != "" {
			sessions[record.SessionID] = true
		}
	}
	summary.Sessions = len(sessions)
	return summary
}

// listCodingSessions answers GET /api/coding/sessions?issueId=&state=&creatorId=.
func (s *server) listCodingSessions(w http.ResponseWriter, r *http.Request) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	filter := coding.SessionFilter{IssueID: query.Get("issueId"), CreatorID: query.Get("creatorId"), Source: query.Get("source"), Limit: 100}
	if state := query.Get("state"); state != "" {
		filter.States = []domain.CodingSessionState{domain.CodingSessionState(state)}
	}
	if viewer.guest() {
		filter.CreatorID = viewer.user.ID
	}
	sessions, err := s.coding.service.List(r.Context(), viewer.workspace, filter)
	if err != nil {
		writeCodingError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

// createCodingSession answers POST /api/coding/sessions (Idempotency-Key honoured).
func (s *server) createCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, ok := s.codingViewerFor(w, r)
	if !ok {
		return
	}
	if !s.codingEnabled() {
		writeCodingError(w, coding.ErrDisabled)
		return
	}
	if viewer.guest() || viewer.role == "app" {
		writeCodingError(w, coding.ErrForbidden)
		return
	}
	var input struct {
		Source          string `json:"source"`
		IssueID         string `json:"issueId"`
		SourceID        string `json:"sourceId"`
		Prompt          string `json:"prompt"`
		EnvironmentID   string `json:"environmentId"`
		Harness         string `json:"harness"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoningEffort"`
		BaseRef         string `json:"baseRef"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 80<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Source == "" {
		input.Source = domain.CodingSourceIssue
		if input.IssueID == "" {
			input.Source = domain.CodingSourceAgentChat
		}
	}
	if !slices.Contains([]string{domain.CodingSourceIssue, domain.CodingSourceAgentChat, domain.CodingSourceComment}, input.Source) {
		writeCodingError(w, fmt.Errorf("%w: source must be issue, agentChat or comment (other sources start through their triggers)", coding.ErrInvalidInput))
		return
	}
	if input.IssueID != "" {
		if _, err := s.store.IssueRecord(r.Context(), viewer.workspace, input.IssueID); err != nil {
			writeCodingError(w, coding.ErrNotFound)
			return
		}
	}
	session, created, err := s.coding.service.Start(r.Context(), coding.StartInput{
		WorkspaceKey: viewer.workspace, Source: input.Source, SourceID: input.SourceID, IssueID: input.IssueID, Prompt: input.Prompt, EnvironmentID: input.EnvironmentID,
		Harness: input.Harness, Model: input.Model, ReasoningEffort: input.ReasoningEffort, BaseRef: input.BaseRef, CreatorID: viewer.user.ID,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")), ResourceID: input.SourceID,
	})
	if err != nil {
		writeCodingError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, session)
}

// getCodingSession answers GET /api/coding/sessions/{id}.
func (s *server) getCodingSession(w http.ResponseWriter, r *http.Request) {
	if _, session, ok := s.codingSessionAccess(w, r, false); ok {
		writeJSON(w, http.StatusOK, session)
	}
}

func (s *server) respondCodingSession(w http.ResponseWriter, ctx context.Context, viewer codingViewer, session domain.CodingSession, err error) {
	if err != nil {
		writeCodingError(w, err)
		return
	}
	if enriched, getErr := s.coding.service.Get(ctx, viewer.workspace, session.ID); getErr == nil {
		session = enriched
	}
	writeJSON(w, http.StatusOK, session)
}

// promptCodingSession answers POST /api/coding/sessions/{id}/prompt.
func (s *server) promptCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Prompt      string   `json:"prompt"`
		Attachments []string `json:"attachments"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 80<<10)
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Attachments) > 20 {
		writeCodingError(w, fmt.Errorf("%w: at most 20 attachments", coding.ErrInvalidInput))
		return
	}
	updated, err := s.coding.service.Prompt(r.Context(), viewer.workspace, session.ID, coding.PromptInput{Prompt: input.Prompt, AuthorID: viewer.user.ID, Attachments: input.Attachments})
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// stopCodingSession answers POST /api/coding/sessions/{id}/stop.
func (s *server) stopCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	updated, err := s.coding.service.Stop(r.Context(), viewer.workspace, session.ID, viewer.user.ID, false)
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// restartCodingSession answers POST /api/coding/sessions/{id}/restart; {useDefaultModel:true} is "Restart with default model".
func (s *server) restartCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		UseDefaultModel bool `json:"useDefaultModel"`
	}
	if !decodeOptionalCodingJSON(w, r, &input) {
		return
	}
	updated, err := s.coding.service.Restart(r.Context(), viewer.workspace, session.ID, viewer.user.ID, input.UseDefaultModel)
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// approveCodingSession answers POST /api/coding/sessions/{id}/approve ({reject:true} rejects).
func (s *server) approveCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	if viewer.guest() {
		writeCodingError(w, coding.ErrForbidden)
		return
	}
	var input struct {
		Reject bool `json:"reject"`
	}
	if !decodeOptionalCodingJSON(w, r, &input) {
		return
	}
	// Through the triggers (track L): membership, idempotency and expiry
	// rules plus the approval notifications; the transition itself is the
	// session core's Approve.
	if triggers, ok := s.codingTriggers(viewer.workspace); ok {
		decision := coding.DecisionApprove
		if input.Reject {
			decision = coding.DecisionReject
		}
		updated, err := triggers.DecideApproval(r.Context(), viewer.workspace, session.ID, viewer.user.ID, codingMemberRole(viewer.role), decision)
		var triggerErr *coding.TriggerError
		if errors.As(err, &triggerErr) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": triggerErr.Message, "code": triggerErr.Code})
			return
		}
		s.respondCodingSession(w, r.Context(), viewer, updated, err)
		return
	}
	updated, err := s.coding.service.Approve(r.Context(), viewer.workspace, session.ID, viewer.user.ID, input.Reject)
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// dismissCodingSession answers POST /api/coding/sessions/{id}/dismiss: it
// rejects a pending approval or ends a session waiting for an answer.
func (s *server) dismissCodingSession(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	updated, err := s.coding.service.Dismiss(r.Context(), viewer.workspace, session.ID, viewer.user.ID)
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// createCodingSessionPullRequest answers POST /api/coding/sessions/{id}/pull-request.
func (s *server) createCodingSessionPullRequest(w http.ResponseWriter, r *http.Request) {
	viewer, session, ok := s.codingSessionAccess(w, r, true)
	if !ok {
		return
	}
	updated, err := s.coding.service.CreatePullRequest(r.Context(), viewer.workspace, session.ID)
	s.respondCodingSession(w, r.Context(), viewer, updated, err)
}

// getCodingSandboxLogs answers GET /api/coding/sessions/{id}/sandboxes/{sandboxId}/logs.
func (s *server) getCodingSandboxLogs(w http.ResponseWriter, r *http.Request) {
	_, session, ok := s.codingSessionAccess(w, r, false)
	if !ok {
		return
	}
	sandboxID := r.PathValue("sandboxId")
	var view *domain.CodingSandboxRecord
	for index := range session.Sandboxes {
		if session.Sandboxes[index].ID == sandboxID {
			view = &session.Sandboxes[index]
		}
	}
	if view == nil {
		writeCodingError(w, coding.ErrNotFound)
		return
	}
	complete := view.EndedAt != nil || view.Status == "stopped" || view.Status == "failed" || view.Status == "lost"
	logs := ""
	if reader, err := s.coding.sandbox.Logs(r.Context(), sandboxID, sandbox.LogOptions{TailLines: 2000}); err == nil {
		raw, _ := io.ReadAll(io.LimitReader(reader, 1<<20))
		reader.Close()
		logs = string(raw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "complete": complete})
}

// decodeOptionalCodingJSON decodes a body that may be empty.
func decodeOptionalCodingJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	if err := json.Unmarshal(raw, target); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return false
	}
	return true
}
