package main

// Environment, secret, snapshot and agent-assisted setup routes
// (docs/coding-sessions-design.md §7.1, §7.2, §10, §18). Owner: track K.
//
// Access: every workspace member except guests reads environments and their
// snapshot history; workspace admins create environments; admins and the
// environment's owners change it, manage its secrets and snapshots, read its
// build logs, open the debug terminal and run Configure/Fix with agent.
// Secrets are write-only: responses list names, never values.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/coding"
	"flow/api/internal/codingenv"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

func (s *server) registerCodingEnvironmentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/coding/repositories", s.listCodingRepositories)
	mux.HandleFunc("GET /api/coding/environments", s.listCodingEnvironments)
	mux.HandleFunc("POST /api/coding/environments", s.createCodingEnvironment)
	mux.HandleFunc("GET /api/coding/environments/{id}", s.getCodingEnvironment)
	mux.HandleFunc("PATCH /api/coding/environments/{id}", s.updateCodingEnvironment)
	mux.HandleFunc("POST /api/coding/environments/{id}/archive", s.archiveCodingEnvironment)
	mux.HandleFunc("POST /api/coding/environments/{id}/unarchive", s.unarchiveCodingEnvironment)
	mux.HandleFunc("POST /api/coding/environments/{id}/secrets", s.addCodingEnvironmentSecrets)
	mux.HandleFunc("DELETE /api/coding/environments/{id}/secrets/{name}", s.deleteCodingEnvironmentSecret)
	mux.HandleFunc("POST /api/coding/environments/{id}/env-import", s.importCodingEnvironmentFile)
	mux.HandleFunc("GET /api/coding/environments/{id}/snapshots", s.listCodingSnapshots)
	mux.HandleFunc("POST /api/coding/environments/{id}/snapshots", s.refreshCodingSnapshot)
	mux.HandleFunc("POST /api/coding/snapshots/{id}/cancel", s.cancelCodingSnapshot)
	mux.HandleFunc("GET /api/coding/snapshots/{id}/logs", s.getCodingSnapshotLogs)
	mux.HandleFunc("POST /api/coding/environments/{id}/configure-with-agent", s.configureCodingEnvironmentWithAgent)
	mux.HandleFunc("POST /api/coding/environments/{id}/fix-with-agent", s.fixCodingEnvironmentWithAgent)
	mux.HandleFunc("GET /api/coding/environments/{id}/terminal", s.codingEnvironmentTerminal)
	mux.HandleFunc("PUT /internal/coding-runtime/snapshots/{id}/archive", s.codingRuntimePutSnapshotArchive)
	mux.HandleFunc("GET /internal/coding-runtime/snapshots/{id}/archive", s.codingRuntimeGetSnapshotArchive)
	mux.HandleFunc("POST /internal/coding-runtime/snapshots/{id}/logs", s.codingRuntimeAppendSnapshotLogs)
	mux.HandleFunc("POST /internal/coding-runtime/snapshots/{id}/complete", s.codingRuntimeCompleteSnapshot)
	mux.HandleFunc("POST /internal/coding-runtime/environment-proposal", s.codingRuntimeEnvironmentProposal)
	mux.HandleFunc("POST /api/coding/environments/{id}/proposal/apply", s.applyCodingEnvironmentProposal)
}

// ---------------------------------------------------------------------------
// Integration points for other tracks (set from their files; nil = unavailable).

// codingEnvironmentSetupInput starts an environmentSetup coding session.
type codingEnvironmentSetupInput struct {
	WorkspaceKey  string
	EnvironmentID string
	CreatorID     string
	Prompt        string
	TurnKind      string // coding.TurnConfigureEnvironment | coding.TurnFixEnvironment
	SnapshotID    string // Fix with agent: the failed snapshot
}

// codingEnvironmentSessionStarter overrides how Configure/Fix with agent
// start their session (tests); nil uses startCodingEnvironmentSetupSession,
// which goes through the session core (coding.Service.Start).
var codingEnvironmentSessionStarter func(ctx context.Context, s *server, input codingEnvironmentSetupInput) (domain.CodingSession, error)

// codingEnvironmentAnchorResource is the AgentTask resource type of
// environment-setup sessions (visible to workspace admins).
const codingEnvironmentAnchorResource = "codingEnvironment"

// startCodingEnvironmentSetupSession starts a source=environmentSetup session
// anchored on the environment, with the configureEnvironment or
// fixEnvironment turn kind (the runner gives that turn the setup guidance and
// the propose_environment tool).
func startCodingEnvironmentSetupSession(ctx context.Context, s *server, input codingEnvironmentSetupInput) (domain.CodingSession, error) {
	if s.coding == nil || s.coding.service == nil {
		return domain.CodingSession{}, coding.ErrDisabled
	}
	title := "Configure environment"
	if input.TurnKind == coding.TurnFixEnvironment {
		title = "Fix environment setup"
	}
	session, _, err := s.coding.service.Start(ctx, coding.StartInput{
		WorkspaceKey: input.WorkspaceKey, Source: domain.CodingSourceEnvironmentSetup, SourceID: input.EnvironmentID,
		ResourceType: codingEnvironmentAnchorResource, ResourceID: input.EnvironmentID,
		Prompt: input.Prompt, TurnKind: input.TurnKind, EnvironmentID: input.EnvironmentID, CreatorID: input.CreatorID, Title: title,
	})
	return session, err
}

// codingSandboxAuthenticator overrides sandbox token authentication (tests);
// by default the store's AuthenticateSandboxToken (track B) is used.
var codingSandboxAuthenticator func(ctx context.Context, token string) (coding.SandboxGrant, error)

// codingSessionLookup overrides the session lookup used by the proposal route
// (tests); by default the store's CodingSession (track B) is used.
var codingSessionLookup func(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)

// codingCredentialModeChecker is implemented by the GitLab host (track J,
// (*gitlab.Host).CheckCredentialMode): it verifies the connection can serve
// FLOW_CODING_GITLAB_CREDENTIAL_MODE for a repository.
type codingCredentialModeChecker interface {
	CheckCredentialMode(ctx context.Context, workspaceKey string, repo codehost.RepoRef) error
}

// checkCodingEnvironmentHost fails early when a GitLab environment's
// connection cannot mint the configured sandbox credentials. Transient host
// errors and hosts without the check do not block saving.
func (s *server) checkCodingEnvironmentHost(ctx context.Context, workspace string, environment domain.CodingEnvironment) error {
	if s.coding == nil || environment.CodeHost != domain.CodeHostGitLab {
		return nil
	}
	checker, ok := s.coding.hosts[environment.CodeHost].(codingCredentialModeChecker)
	if !ok {
		return nil
	}
	err := checker.CheckCredentialMode(ctx, workspace, codehost.RepoRef{CodeHost: environment.CodeHost, HostName: environment.HostName, FullName: environment.Repository, ID: environment.RepositoryID})
	if err == nil || errors.Is(err, codehost.ErrTransient) || errors.Is(err, codehost.ErrNotImplemented) {
		return nil
	}
	return err
}

func writeCodingHostCheckError(w http.ResponseWriter, environment domain.CodingEnvironment, err error) {
	writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": string(codehost.ErrorCode(environment.CodeHost, err))})
}

// codingSnapshotStrategyFactory overrides the snapshot strategy (tests); nil
// uses track A's sandbox.NewStrategy for FLOW_CODING_SNAPSHOT_STRATEGY.
var codingSnapshotStrategyFactory func(s *server) sandbox.SnapshotStrategy

// codingEnvironmentLauncherOverride replaces the environment sandbox launcher (tests).
var codingEnvironmentLauncherOverride func(s *server) coding.SandboxLauncher

// ---------------------------------------------------------------------------
// Shared helpers

type codingEnvironmentRequest struct {
	workspace string
	user      domain.User
	admin     bool
}

// codingEnvironmentAccess checks the workspace, the coding-sessions feature
// flag and the caller's role. Guests are refused.
func (s *server) codingEnvironmentAccess(w http.ResponseWriter, r *http.Request) (codingEnvironmentRequest, bool) {
	data, ok := s.store.WorkspaceMetadata(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "Workspace not found")
		return codingEnvironmentRequest{}, false
	}
	if !workspaceFeatureEnabled(data.WorkspaceSettings, "coding-sessions") {
		writeError(w, http.StatusForbidden, "Coding sessions are turned off for this workspace")
		return codingEnvironmentRequest{}, false
	}
	request := codingEnvironmentRequest{workspace: s.codingWorkspaceKey(r, data), user: requestActor(s, r)}
	if s.authDisabled {
		request.admin = true
		return request, true
	}
	role, _, err := s.store.WorkspaceRole(r.Context(), data.Workspace.ID, request.user.ID)
	if err != nil || role == "" || strings.EqualFold(role, "guest") {
		writeError(w, http.StatusForbidden, "Guests cannot use coding environments")
		return codingEnvironmentRequest{}, false
	}
	request.admin = workspaceAdminRole(role)
	return request, true
}

// codingWorkspaceKey is the store key of the request's workspace.
func (s *server) codingWorkspaceKey(r *http.Request, data domain.Bootstrap) string {
	if key := workspaceKey(r); key != "" {
		return key
	}
	for _, key := range s.store.WorkspaceKeys() {
		if candidate, ok := s.store.WorkspaceMetadata(key); ok && candidate.Workspace.ID == data.Workspace.ID {
			return key
		}
	}
	return data.Workspace.URLKey
}

func (request codingEnvironmentRequest) canManage(environment domain.CodingEnvironment) bool {
	return request.admin || codingenv.EnvironmentOwnedBy(environment, request.user.ID)
}

// managedCodingEnvironment loads the path's environment and requires an admin or owner.
func (s *server) managedCodingEnvironment(w http.ResponseWriter, r *http.Request) (codingEnvironmentRequest, domain.CodingEnvironment, bool) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return request, domain.CodingEnvironment{}, false
	}
	environment, err := s.store.CodingEnvironment(r.Context(), request.workspace, r.PathValue("id"))
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return request, domain.CodingEnvironment{}, false
	}
	if !request.canManage(environment) {
		writeError(w, http.StatusForbidden, "Only workspace admins and the environment's owners can change it")
		return request, domain.CodingEnvironment{}, false
	}
	return request, environment, true
}

func writeCodingEnvironmentError(w http.ResponseWriter, err error) {
	var validation *codingenv.ValidationError
	switch {
	case errors.Is(err, coding.ErrNotFound):
		writeError(w, http.StatusNotFound, "resource not found")
	case errors.Is(err, coding.ErrRepoInUse):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This repository already has an active environment", "code": "REPO_IN_USE"})
	case errors.Is(err, coding.ErrVersionConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "This environment changed in another session", "code": "VERSION_CONFLICT"})
	case errors.Is(err, coding.ErrSecretExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "A secret with this name already exists; delete it and add it again to change it", "code": "SECRET_EXISTS"})
	case errors.Is(err, coding.ErrNoProposal):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "NO_PROPOSAL"})
	case errors.Is(err, coding.ErrSnapshotsUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Snapshots need coding sessions to be enabled with a sandbox provider", "code": string(domain.CodingErrSandboxDisabled)})
	case errors.Is(err, coding.ErrDisabled):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Coding sessions are not enabled for this workspace", "code": string(domain.CodingErrSandboxDisabled)})
	case errors.Is(err, coding.ErrNoAgent):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "NO_AGENT"})
	case errors.Is(err, coding.ErrQuotaExceeded):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": string(domain.CodingErrQuotaExceeded)})
	case errors.Is(err, coding.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, coding.ErrEnvImportTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.As(err, &validation):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validation.Error(), "field": validation.Field})
	case errors.Is(err, codingenv.ErrInvalidEnvironment):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		log.Printf("coding environments: %v", err)
		writeError(w, http.StatusInternalServerError, "Coding environment request failed")
	}
}

func (s *server) auditCodingEnvironment(r *http.Request, workspace, action, resourceID string, metadata map[string]any) {
	ctx := store.WithMetadataFields(context.WithoutCancel(r.Context()), "auditLog")
	if err := s.store.MutateWorkspace(ctx, workspace, "audit.coding_environment", resourceID, map[string]any{"action": action}, func(data *domain.Bootstrap) error {
		appendAudit(data, action, "coding_environment", resourceID, metadata)
		return nil
	}); err != nil {
		log.Printf("coding environment audit workspace=%s action=%s: %v", workspace, action, err)
	}
}

func (s *server) configureCodingSecretKey() {
	if s.coding != nil && s.coding.config.SecretKey != "" {
		if err := store.SetCodingSecretKey(s.coding.config.SecretKey); err != nil {
			log.Printf("coding environments: %v", err)
		}
	}
}

// codingEnvironmentLauncher picks how environment sandboxes are started:
// coding_sandboxes rows (RecordLauncher), unless a test overrides it.
func (s *server) codingEnvironmentLauncher() coding.SandboxLauncher {
	if codingEnvironmentLauncherOverride != nil {
		return codingEnvironmentLauncherOverride(s)
	}
	if s.coding == nil || !s.coding.config.Enabled {
		return nil
	}
	// Environment sandboxes are coding_sandboxes rows: the sandbox reconciler
	// (track A) starts, bills, collects and garbage-collects them like session
	// sandboxes. coding.ProviderLauncher is for tests only.
	return coding.RecordLauncher{Store: s.codingStore(), Config: s.coding.config}
}

func (s *server) codingSnapshotStrategy() sandbox.SnapshotStrategy {
	if s.coding == nil || !s.coding.config.Enabled {
		return nil
	}
	if codingSnapshotStrategyFactory != nil {
		if strategy := codingSnapshotStrategyFactory(s); strategy != nil {
			return strategy
		}
	}
	strategy, err := sandbox.NewStrategy(s.coding.config, s.coding.sandbox, s.objectStore)
	if err != nil {
		return nil
	}
	return strategy
}

func (s *server) codingSnapshotWorkspace(ctx context.Context, snapshotID string) (string, error) {
	workspace, err := s.store.CodingSnapshotWorkspace(ctx, snapshotID)
	if err != nil {
		return "", err
	}
	return workspace, nil
}

// codingSnapshotReconciler builds the (stateless) snapshot reconciler.
func (s *server) codingSnapshotReconciler(owner string) *coding.SnapshotReconciler {
	reconciler := &coding.SnapshotReconciler{Store: s.store, Owner: owner}
	if s.coding == nil {
		return reconciler
	}
	reconciler.Config = s.coding.config
	reconciler.Provider = s.coding.sandbox
	reconciler.Launcher = s.codingEnvironmentLauncher()
	reconciler.Strategy = s.codingSnapshotStrategy()
	reconciler.BranchHead = s.codingEnvironmentBranchHead
	return reconciler
}

func (s *server) codingEnvironmentBranchHead(ctx context.Context, environment domain.CodingEnvironment) (string, error) {
	if s.coding == nil {
		return "", codehost.ErrNotConnected
	}
	host, ok := s.coding.hosts[environment.CodeHost]
	if !ok {
		return "", codehost.ErrNotConnected
	}
	repo := codehost.RepoRef{CodeHost: environment.CodeHost, HostName: environment.HostName, FullName: environment.Repository, ID: environment.RepositoryID}
	branch := environment.DefaultBranch
	if branch == "" {
		resolved, err := host.ResolveRepository(ctx, environment.WorkspaceKey, repo)
		if err != nil {
			return "", err
		}
		branch = resolved.DefaultBranch
	}
	return host.BranchHead(ctx, environment.WorkspaceKey, repo, branch)
}

// runCodingSnapshots runs the snapshot reconciler until ctx ends (called from runCoding).
func (s *server) runCodingSnapshots(ctx context.Context) {
	s.configureCodingSecretKey()
	host, _ := os.Hostname()
	nonce := make([]byte, 4)
	_, _ = rand.Read(nonce)
	owner := fmt.Sprintf("%s:%d:%s", host, os.Getpid(), hex.EncodeToString(nonce))
	s.codingSnapshotReconciler(owner).Run(ctx)
}

// requestCodingSnapshot queues a build after a config change; it is a no-op
// while sandboxes are unavailable.
func (s *server) requestCodingSnapshot(ctx context.Context, environment domain.CodingEnvironment, userID string) {
	if _, _, err := s.codingSnapshotReconciler("request").Request(ctx, environment, userID); err != nil && !errors.Is(err, coding.ErrSnapshotsUnavailable) {
		log.Printf("coding environment %s: queue snapshot: %v", environment.ID, err)
	}
}

func (s *server) codingSnapshotHash(ctx context.Context, environment domain.CodingEnvironment) string {
	names, _ := s.store.CodingEnvironmentSecretNames(ctx, environment.WorkspaceKey, environment.ID)
	image := ""
	if s.coding != nil {
		image = s.coding.config.Image
	}
	return coding.SnapshotConfigHash(environment, image, coding.SnapshotBaseRef(environment), names)
}

// ---------------------------------------------------------------------------
// Web routes

// listCodingRepositories serves the environment editor's repository picker.
func (s *server) listCodingRepositories(w http.ResponseWriter, r *http.Request) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	kind := strings.ToLower(strings.TrimSpace(query.Get("codeHost")))
	if kind == "" {
		kind = domain.CodeHostGitHub
	}
	var host codehost.Host
	if s.coding != nil {
		host = s.coding.hosts[kind]
	}
	if host == nil {
		writeError(w, http.StatusBadRequest, "codeHost must be github or gitlab")
		return
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	page, err := host.ListRepositories(r.Context(), request.workspace, codehost.ListOptions{HostName: query.Get("hostName"), Query: query.Get("q"), Cursor: query.Get("cursor"), Limit: min(max(limit, 0), 100)})
	if err != nil {
		switch {
		case errors.Is(err, codehost.ErrNotImplemented):
			writeError(w, http.StatusNotImplemented, "This code host is not available yet")
		case errors.Is(err, codehost.ErrNotConnected):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Connect the code host first", "code": string(codehost.ErrorCode(kind, err))})
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "The code host request failed", "code": string(codehost.ErrorCode(kind, err))})
		}
		return
	}
	type repository struct {
		CodeHost      string `json:"codeHost"`
		HostName      string `json:"hostName"`
		FullName      string `json:"fullName"`
		ID            string `json:"id,omitempty"`
		DefaultBranch string `json:"defaultBranch"`
		WebURL        string `json:"webUrl"`
		Private       bool   `json:"private"`
		Archived      bool   `json:"archived"`
	}
	result := struct {
		Repositories []repository `json:"repositories"`
		NextCursor   string       `json:"nextCursor,omitempty"`
	}{Repositories: []repository{}, NextCursor: page.NextCursor}
	for _, item := range page.Repositories {
		result.Repositories = append(result.Repositories, repository{CodeHost: item.CodeHost, HostName: item.HostName, FullName: item.FullName, ID: item.ID, DefaultBranch: item.DefaultBranch, WebURL: item.WebURL, Private: item.Private, Archived: item.Archived})
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) listCodingEnvironments(w http.ResponseWriter, r *http.Request) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return
	}
	archived, ok := queryBool(w, r, "archived")
	if !ok {
		return
	}
	environments, err := s.store.ListCodingEnvironments(r.Context(), request.workspace, archived)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, environments)
}

func (s *server) getCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return
	}
	environment, err := s.store.CodingEnvironment(r.Context(), request.workspace, r.PathValue("id"))
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, environment)
}

// codingEnvironmentFields are the fields create and PATCH accept.
var codingEnvironmentFields = map[string]bool{"name": true, "codeHost": true, "hostName": true, "repository": true, "repositoryId": true, "defaultBranch": true, "ownerIds": true, "harness": true, "model": true, "network": true, "allowedDomains": true, "sandboxSize": true, "mise": true, "prepareScript": true, "customGuidance": true, "dependencyManagement": true}

// applyCodingEnvironmentPatch decodes the accepted fields of patch into environment.
func applyCodingEnvironmentPatch(environment *domain.CodingEnvironment, patch map[string]json.RawMessage) error {
	fields := map[string]json.RawMessage{}
	for key, value := range patch {
		if codingEnvironmentFields[key] {
			fields[key] = value
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if fields["mise"] != nil {
		environment.Mise = domain.CodingMiseConfig{}
	}
	if err := json.Unmarshal(encoded, environment); err != nil {
		return codingenv.Invalid("body", "%v", err)
	}
	return nil
}

type codingSecretInput struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func validateCodingSecretInputs(secrets []codingSecretInput) error {
	seen := map[string]bool{}
	for _, secret := range secrets {
		secret.Name = strings.TrimSpace(secret.Name)
		if err := codingenv.ValidateSecret(secret.Name, secret.Value); err != nil {
			return err
		}
		if seen[secret.Name] {
			return codingenv.Invalid("secrets", "%s is listed twice", secret.Name)
		}
		seen[secret.Name] = true
	}
	if len(secrets) > codingenv.EnvironmentSecretsMax {
		return codingenv.Invalid("secrets", "at most %d secrets", codingenv.EnvironmentSecretsMax)
	}
	return nil
}

func (s *server) createCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return
	}
	if !request.admin {
		writeError(w, http.StatusForbidden, "Only workspace admins can create coding environments")
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSON(w, r, &body) {
		return
	}
	var secrets []codingSecretInput
	if raw := body["secrets"]; raw != nil {
		if err := json.Unmarshal(raw, &secrets); err != nil {
			writeError(w, http.StatusBadRequest, "secrets must be a list of {name, value}")
			return
		}
	}
	if err := validateCodingSecretInputs(secrets); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if len(secrets) > 0 {
		s.configureCodingSecretKey()
		if !store.CodingSecretsConfigured() {
			writeError(w, http.StatusServiceUnavailable, "Set FLOW_CODING_SECRET_KEY to store environment secrets")
			return
		}
	}
	environment := domain.CodingEnvironment{WorkspaceKey: request.workspace, CreatedBy: request.user.ID}
	if err := applyCodingEnvironmentPatch(&environment, body); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if len(environment.OwnerIDs) == 0 && request.user.ID != "" {
		environment.OwnerIDs = []string{request.user.ID}
	}
	if err := codingenv.NormalizeEnvironment(&environment); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if err := s.checkCodingEnvironmentHost(r.Context(), request.workspace, environment); err != nil {
		writeCodingHostCheckError(w, environment, err)
		return
	}
	created, err := s.store.CreateCodingEnvironment(r.Context(), environment)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	for _, secret := range secrets {
		if err := s.store.PutCodingEnvironmentSecret(r.Context(), request.workspace, created.ID, strings.TrimSpace(secret.Name), secret.Value, request.user.ID); err != nil {
			writeCodingEnvironmentError(w, err)
			return
		}
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.created", created.ID, map[string]any{"repository": created.CodeHost + ":" + created.HostName + "/" + created.Repository, "secrets": codingSecretNames(secrets)})
	created, err = s.store.CodingEnvironment(r.Context(), request.workspace, created.ID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	s.requestCodingSnapshot(r.Context(), created, request.user.ID)
	created, _ = s.store.CodingEnvironment(r.Context(), request.workspace, created.ID)
	writeJSON(w, http.StatusCreated, created)
}

func codingSecretNames(secrets []codingSecretInput) []string {
	names := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		names = append(names, strings.TrimSpace(secret.Name))
	}
	return names
}

func (s *server) updateCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if !decodeJSON(w, r, &body) {
		return
	}
	var version int64
	if raw := body["version"]; raw == nil || json.Unmarshal(raw, &version) != nil || version < 1 {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}
	if _, changesOwners := body["ownerIds"]; changesOwners && !request.admin {
		writeError(w, http.StatusForbidden, "Only workspace admins can change an environment's owners")
		return
	}
	preview := environment
	if err := applyCodingEnvironmentPatch(&preview, body); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if err := codingenv.NormalizeEnvironment(&preview); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if preview.CodeHost != environment.CodeHost || preview.HostName != environment.HostName || preview.Repository != environment.Repository {
		if err := s.checkCodingEnvironmentHost(r.Context(), request.workspace, preview); err != nil {
			writeCodingHostCheckError(w, preview, err)
			return
		}
	}
	before := s.codingSnapshotHash(r.Context(), environment)
	updated, err := s.store.UpdateCodingEnvironment(r.Context(), request.workspace, environment.ID, version, func(next *domain.CodingEnvironment) error {
		return applyCodingEnvironmentPatch(next, body)
	})
	if errors.Is(err, coding.ErrVersionConflict) {
		current, _ := s.store.CodingEnvironment(r.Context(), request.workspace, environment.ID)
		writeVersionConflict(w, current)
		return
	}
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.updated", updated.ID, map[string]any{"version": updated.Version})
	if s.codingSnapshotHash(r.Context(), updated) != before {
		s.requestCodingSnapshot(r.Context(), updated, request.user.ID)
		updated, _ = s.store.CodingEnvironment(r.Context(), request.workspace, updated.ID)
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) setCodingEnvironmentArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	updated, err := s.store.SetCodingEnvironmentArchived(r.Context(), request.workspace, environment.ID, archived)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	action := "coding_environment.unarchived"
	if archived {
		action = "coding_environment.archived"
	}
	s.auditCodingEnvironment(r, request.workspace, action, environment.ID, nil)
	writeJSON(w, http.StatusOK, updated)
}

func (s *server) archiveCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	s.setCodingEnvironmentArchived(w, r, true)
}

func (s *server) unarchiveCodingEnvironment(w http.ResponseWriter, r *http.Request) {
	s.setCodingEnvironmentArchived(w, r, false)
}

func (s *server) addCodingEnvironmentSecrets(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	var body struct {
		Secrets []codingSecretInput `json:"secrets"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Secrets) == 0 {
		writeError(w, http.StatusBadRequest, "secrets is required")
		return
	}
	if err := validateCodingSecretInputs(body.Secrets); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	existing := map[string]bool{}
	for _, secret := range environment.Secrets {
		existing[secret.Name] = true
	}
	var taken []string
	for _, secret := range body.Secrets {
		if existing[strings.TrimSpace(secret.Name)] {
			taken = append(taken, strings.TrimSpace(secret.Name))
		}
	}
	if len(taken) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Secrets cannot be changed once saved; delete and add them again", "code": "SECRET_EXISTS", "names": taken})
		return
	}
	s.configureCodingSecretKey()
	if !store.CodingSecretsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "Set FLOW_CODING_SECRET_KEY to store environment secrets")
		return
	}
	before := s.codingSnapshotHash(r.Context(), environment)
	for _, secret := range body.Secrets {
		if err := s.store.PutCodingEnvironmentSecret(r.Context(), request.workspace, environment.ID, strings.TrimSpace(secret.Name), secret.Value, request.user.ID); err != nil {
			writeCodingEnvironmentError(w, err)
			return
		}
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.secrets_added", environment.ID, map[string]any{"names": codingSecretNames(body.Secrets)})
	s.respondAfterSecretChange(w, r, request, environment.ID, before)
}

func (s *server) deleteCodingEnvironmentSecret(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	name := r.PathValue("name")
	before := s.codingSnapshotHash(r.Context(), environment)
	if err := s.store.DeleteCodingEnvironmentSecret(r.Context(), request.workspace, environment.ID, name); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.secret_deleted", environment.ID, map[string]any{"name": name})
	s.respondAfterSecretChange(w, r, request, environment.ID, before)
}

// respondAfterSecretChange answers with the environment; the secret names are
// part of the snapshot config hash, so a change queues a rebuild.
func (s *server) respondAfterSecretChange(w http.ResponseWriter, r *http.Request, request codingEnvironmentRequest, id, before string) {
	updated, err := s.store.CodingEnvironment(r.Context(), request.workspace, id)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if s.codingSnapshotHash(r.Context(), updated) != before {
		s.requestCodingSnapshot(r.Context(), updated, request.user.ID)
		updated, _ = s.store.CodingEnvironment(r.Context(), request.workspace, id)
	}
	writeJSON(w, http.StatusOK, updated)
}

// importCodingEnvironmentFile parses a .env into variables and secret
// suggestions. Nothing is stored.
func (s *server) importCodingEnvironmentFile(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.managedCodingEnvironment(w, r); !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	result, err := coding.ImportDotEnv(body.Content)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	// Reserved names would be rejected on save; report them now.
	variables := result.Variables[:0]
	for _, variable := range result.Variables {
		if codingenv.ReservedEnvName(variable.Name) {
			result.Warnings = append(result.Warnings, coding.EnvImportWarning{Message: variable.Name + " is reserved by Flow and was skipped"})
			continue
		}
		variables = append(variables, variable)
	}
	result.Variables = variables
	secrets := result.Secrets[:0]
	for _, secret := range result.Secrets {
		if codingenv.ReservedEnvName(secret.Name) {
			result.Warnings = append(result.Warnings, coding.EnvImportWarning{Message: secret.Name + " is reserved by Flow and was skipped"})
			continue
		}
		secrets = append(secrets, secret)
	}
	result.Secrets = secrets
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}

func (s *server) listCodingSnapshots(w http.ResponseWriter, r *http.Request) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return
	}
	environment, err := s.store.CodingEnvironment(r.Context(), request.workspace, r.PathValue("id"))
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	snapshots, err := s.store.ListCodingSnapshots(r.Context(), request.workspace, environment.ID, limit)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshots)
}

func (s *server) refreshCodingSnapshot(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	snapshot, created, err := s.codingSnapshotReconciler("request").Request(r.Context(), environment, request.user.ID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.auditCodingEnvironment(r, request.workspace, "coding_environment.snapshot_requested", environment.ID, map[string]any{"snapshotId": snapshot.ID})
	}
	writeJSON(w, status, snapshot)
}

// managedCodingSnapshot loads the path's snapshot and requires an admin or
// owner of its environment.
func (s *server) managedCodingSnapshot(w http.ResponseWriter, r *http.Request) (codingEnvironmentRequest, domain.CodingEnvironmentSnapshot, bool) {
	request, ok := s.codingEnvironmentAccess(w, r)
	if !ok {
		return request, domain.CodingEnvironmentSnapshot{}, false
	}
	snapshot, err := s.store.CodingSnapshot(r.Context(), request.workspace, r.PathValue("id"))
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return request, snapshot, false
	}
	environment, err := s.store.CodingEnvironment(r.Context(), request.workspace, snapshot.EnvironmentID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return request, snapshot, false
	}
	if !request.canManage(environment) {
		writeError(w, http.StatusForbidden, "Only workspace admins and the environment's owners can manage its snapshots")
		return request, snapshot, false
	}
	return request, snapshot, true
}

func (s *server) cancelCodingSnapshot(w http.ResponseWriter, r *http.Request) {
	request, snapshot, ok := s.managedCodingSnapshot(w, r)
	if !ok {
		return
	}
	updated, err := s.store.RequestCodingSnapshotCancel(r.Context(), request.workspace, snapshot.ID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.snapshot_canceled", snapshot.EnvironmentID, map[string]any{"snapshotId": snapshot.ID})
	writeJSON(w, http.StatusOK, updated)
}

// getCodingSnapshotLogs answers {logs, complete, after}; ?after=<seq> reads
// incrementally and ?follow=true streams text/plain until the build ends.
func (s *server) getCodingSnapshotLogs(w http.ResponseWriter, r *http.Request) {
	request, snapshot, ok := s.managedCodingSnapshot(w, r)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	follow, ok := queryBool(w, r, "follow")
	if !ok {
		return
	}
	if !follow {
		logs, last, err := s.store.CodingSnapshotLogs(r.Context(), request.workspace, snapshot.ID, after, 0)
		if err != nil {
			writeCodingEnvironmentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": string(logs), "complete": codingSnapshotFinished(snapshot.Status), "after": last})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		logs, last, err := s.store.CodingSnapshotLogs(ctx, request.workspace, snapshot.ID, after, 0)
		if err != nil {
			return
		}
		if len(logs) > 0 {
			if _, err := w.Write(logs); err != nil {
				return
			}
			_ = controller.Flush()
			after = last
			continue
		}
		current, err := s.store.CodingSnapshot(ctx, request.workspace, snapshot.ID)
		if err != nil || codingSnapshotFinished(current.Status) {
			if rest, _, err := s.store.CodingSnapshotLogs(ctx, request.workspace, snapshot.ID, after, 0); err == nil && len(rest) > 0 {
				_, _ = w.Write(rest)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func codingSnapshotFinished(status string) bool {
	return status != domain.CodingSnapshotQueued && status != domain.CodingSnapshotBuilding
}

func (s *server) startCodingEnvironmentSession(w http.ResponseWriter, r *http.Request, request codingEnvironmentRequest, environment domain.CodingEnvironment, input codingEnvironmentSetupInput) {
	if environment.ArchivedAt != nil {
		writeError(w, http.StatusConflict, "Unarchive the environment first")
		return
	}
	starter := codingEnvironmentSessionStarter
	if starter == nil {
		starter = startCodingEnvironmentSetupSession
	}
	if !s.codingEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Coding sessions are not available on this server", "code": string(domain.CodingErrSandboxDisabled)})
		return
	}
	input.WorkspaceKey, input.EnvironmentID, input.CreatorID = request.workspace, environment.ID, request.user.ID
	session, err := starter(r.Context(), s, input)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment."+input.TurnKind, environment.ID, map[string]any{"sessionId": session.ID})
	writeJSON(w, http.StatusCreated, session)
}

func (s *server) configureCodingEnvironmentWithAgent(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	var body struct {
		Prompt string `json:"prompt"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	s.startCodingEnvironmentSession(w, r, request, environment, codingEnvironmentSetupInput{Prompt: coding.ConfigurePrompt(environment, body.Prompt), TurnKind: coding.TurnConfigureEnvironment})
}

func (s *server) fixCodingEnvironmentWithAgent(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	var body struct {
		SnapshotID string `json:"snapshotId"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	var snapshot *domain.CodingEnvironmentSnapshot
	if body.SnapshotID != "" {
		found, err := s.store.CodingSnapshot(r.Context(), request.workspace, body.SnapshotID)
		if err != nil || found.EnvironmentID != environment.ID {
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}
		snapshot = &found
	} else if snapshots, err := s.store.ListCodingSnapshots(r.Context(), request.workspace, environment.ID, 20); err == nil {
		for index := range snapshots {
			if snapshots[index].Status == domain.CodingSnapshotFailed {
				snapshot = &snapshots[index]
				break
			}
		}
	}
	tail, snapshotID := "", ""
	if snapshot != nil {
		snapshotID = snapshot.ID
		if logs, _, err := s.store.CodingSnapshotLogs(r.Context(), request.workspace, snapshot.ID, 0, 0); err == nil {
			tail = codingLogTail(string(logs), 8<<10)
		}
	}
	s.startCodingEnvironmentSession(w, r, request, environment, codingEnvironmentSetupInput{Prompt: coding.FixPrompt(environment, snapshot, tail), TurnKind: coding.TurnFixEnvironment, SnapshotID: snapshotID})
}

func codingLogTail(logs string, limit int) string {
	if len(logs) <= limit {
		return logs
	}
	logs = logs[len(logs)-limit:]
	if index := strings.IndexByte(logs, '\n'); index >= 0 {
		logs = logs[index+1:]
	}
	return logs
}

func (s *server) applyCodingEnvironmentProposal(w http.ResponseWriter, r *http.Request) {
	request, environment, ok := s.managedCodingEnvironment(w, r)
	if !ok {
		return
	}
	var body struct {
		Discard bool `json:"discard"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	if environment.PendingProposal == nil {
		writeCodingEnvironmentError(w, coding.ErrNoProposal)
		return
	}
	if body.Discard {
		updated, err := s.store.SetCodingEnvironmentProposal(r.Context(), request.workspace, environment.ID, nil)
		if err != nil {
			writeCodingEnvironmentError(w, err)
			return
		}
		s.auditCodingEnvironment(r, request.workspace, "coding_environment.proposal_discarded", environment.ID, map[string]any{"sessionId": environment.PendingProposal.SessionID})
		writeJSON(w, http.StatusOK, updated)
		return
	}
	updated, err := s.store.UpdateCodingEnvironment(r.Context(), request.workspace, environment.ID, 0, coding.ApplyProposal)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	s.auditCodingEnvironment(r, request.workspace, "coding_environment.proposal_applied", environment.ID, map[string]any{"sessionId": environment.PendingProposal.SessionID})
	s.requestCodingSnapshot(r.Context(), updated, request.user.ID)
	updated, _ = s.store.CodingEnvironment(r.Context(), request.workspace, environment.ID)
	writeJSON(w, http.StatusOK, updated)
}

// ---------------------------------------------------------------------------
// Runner-facing routes (sandbox token)

func (s *server) codingRuntimeGrant(w http.ResponseWriter, r *http.Request) (coding.SandboxGrant, bool) {
	header := strings.Fields(r.Header.Get("Authorization"))
	if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") || !strings.HasPrefix(header[1], coding.SandboxTokenPrefix) {
		writeError(w, http.StatusUnauthorized, "sandbox token required")
		return coding.SandboxGrant{}, false
	}
	authenticate := codingSandboxAuthenticator
	if authenticate == nil {
		if authenticator, ok := any(s.codingStore()).(interface {
			AuthenticateSandboxToken(ctx context.Context, token string) (coding.SandboxGrant, error)
		}); ok {
			authenticate = authenticator.AuthenticateSandboxToken
		}
	}
	if authenticate == nil {
		writeError(w, http.StatusServiceUnavailable, "sandbox tokens are not available")
		return coding.SandboxGrant{}, false
	}
	grant, err := authenticate(r.Context(), header[1])
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid sandbox token")
		return coding.SandboxGrant{}, false
	}
	return grant, true
}

// codingRuntimeSnapshotGrant requires a snapshot-build sandbox for the path's snapshot.
func (s *server) codingRuntimeSnapshotGrant(w http.ResponseWriter, r *http.Request) (coding.SandboxGrant, domain.CodingEnvironmentSnapshot, bool) {
	grant, ok := s.codingRuntimeGrant(w, r)
	if !ok {
		return grant, domain.CodingEnvironmentSnapshot{}, false
	}
	if grant.Kind != sandbox.KindSnapshot || grant.SnapshotID != r.PathValue("id") {
		writeError(w, http.StatusForbidden, "this sandbox does not build that snapshot")
		return grant, domain.CodingEnvironmentSnapshot{}, false
	}
	snapshot, err := s.store.CodingSnapshot(r.Context(), grant.WorkspaceKey, grant.SnapshotID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return grant, snapshot, false
	}
	return grant, snapshot, true
}

func (s *server) codingRuntimePutSnapshotArchive(w http.ResponseWriter, r *http.Request) {
	grant, snapshot, ok := s.codingRuntimeSnapshotGrant(w, r)
	if !ok {
		return
	}
	if snapshot.Status != domain.CodingSnapshotBuilding || snapshot.SandboxID != grant.SandboxID {
		writeError(w, http.StatusConflict, "the snapshot is not being built by this sandbox")
		return
	}
	archives, ok := s.codingSnapshotStrategy().(coding.ArchiveStore)
	if !ok {
		writeError(w, http.StatusConflict, "the snapshot strategy does not use archives")
		return
	}
	if limit := s.coding.config.Snapshot.ArchiveMaxBytes; limit > 0 && r.ContentLength > limit {
		writeError(w, http.StatusRequestEntityTooLarge, "the archive exceeds FLOW_CODING_ARCHIVE_MAX_BYTES")
		return
	}
	_, size, sum, err := archives.Store(r.Context(), snapshot.ID, grant.WorkspaceKey, r.Body)
	if err != nil {
		message := err.Error()
		switch {
		case errors.Is(err, coding.ErrArchiveTooLarge) || strings.Contains(message, "FLOW_CODING_ARCHIVE_MAX_BYTES"):
			writeError(w, http.StatusRequestEntityTooLarge, "the archive exceeds FLOW_CODING_ARCHIVE_MAX_BYTES")
		case errors.Is(err, coding.ErrArchiveInvalid) || strings.Contains(message, "not zstd"):
			writeError(w, http.StatusBadRequest, "the archive must be a zstd stream")
		default:
			log.Printf("coding snapshot %s: store archive: %v", snapshot.ID, err)
			writeError(w, http.StatusBadGateway, "storing the archive failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sizeBytes": size, "sha256": sum})
}

func (s *server) codingRuntimeGetSnapshotArchive(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.codingRuntimeGrant(w, r)
	if !ok {
		return
	}
	if grant.SnapshotID == "" || grant.SnapshotID != r.PathValue("id") {
		writeError(w, http.StatusForbidden, "this sandbox does not restore that snapshot")
		return
	}
	snapshot, err := s.store.CodingSnapshot(r.Context(), grant.WorkspaceKey, grant.SnapshotID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	archives, ok := s.codingSnapshotStrategy().(coding.ArchiveStore)
	if snapshot.Status != domain.CodingSnapshotReady || snapshot.StorageRef == "" || !ok {
		writeError(w, http.StatusNotFound, "the snapshot archive is not available")
		return
	}
	reader, size, err := archives.Open(r.Context(), snapshot.StorageRef)
	if err != nil {
		writeError(w, http.StatusNotFound, "the snapshot archive is not available")
		return
	}
	defer reader.Close()
	w.Header().Set("Content-Type", "application/zstd")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func (s *server) codingRuntimeAppendSnapshotLogs(w http.ResponseWriter, r *http.Request) {
	grant, snapshot, ok := s.codingRuntimeSnapshotGrant(w, r)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "log batch is too large")
		return
	}
	text := raw
	if strings.Contains(r.Header.Get("Content-Type"), "json") {
		var body coding.SnapshotLogsRequest
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		joined := ""
		for _, line := range body.Lines {
			joined += strings.TrimRight(line, "\n") + "\n"
		}
		text = []byte(joined)
	}
	for len(text) > 0 {
		chunk := text[:min(len(text), 256<<10)]
		text = text[len(chunk):]
		if err := s.store.AppendCodingSnapshotLog(r.Context(), grant.WorkspaceKey, snapshot.ID, chunk); err != nil {
			writeCodingEnvironmentError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) codingRuntimeCompleteSnapshot(w http.ResponseWriter, r *http.Request) {
	grant, snapshot, ok := s.codingRuntimeSnapshotGrant(w, r)
	if !ok {
		return
	}
	var body coding.SnapshotCompleteRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Status != domain.CodingSnapshotReady && body.Status != domain.CodingSnapshotFailed {
		writeError(w, http.StatusBadRequest, "status must be ready or failed")
		return
	}
	body.Error = codingLogTail(body.Error, 4<<10)
	if err := s.store.RecordCodingSnapshotResult(r.Context(), grant.WorkspaceKey, snapshot.ID, grant.SandboxID, codingenv.SnapshotResult(body)); err != nil {
		if errors.Is(err, coding.ErrVersionConflict) {
			writeError(w, http.StatusConflict, "the snapshot is not being built by this sandbox")
			return
		}
		writeCodingEnvironmentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// codingRuntimeEnvironmentProposal stores a Configure/Fix with agent result
// as the environment's pending proposal.
func (s *server) codingRuntimeEnvironmentProposal(w http.ResponseWriter, r *http.Request) {
	grant, ok := s.codingRuntimeGrant(w, r)
	if !ok {
		return
	}
	if grant.SessionID == "" {
		writeError(w, http.StatusForbidden, "only coding sessions can propose environment changes")
		return
	}
	lookup := codingSessionLookup
	if lookup == nil {
		if sessions, ok := any(s.codingStore()).(interface {
			CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error)
		}); ok {
			lookup = sessions.CodingSession
		}
	}
	if lookup == nil {
		writeError(w, http.StatusServiceUnavailable, "coding sessions are not available")
		return
	}
	session, err := lookup(r.Context(), grant.WorkspaceKey, grant.SessionID)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	if session.Source != domain.CodingSourceEnvironmentSetup || session.EnvironmentID == "" {
		writeError(w, http.StatusForbidden, "this session does not set up an environment")
		return
	}
	var proposal domain.CodingEnvironmentProposal
	if !decodeJSON(w, r, &proposal) {
		return
	}
	proposal.SessionID, proposal.CreatedAt = session.ID, time.Time{}
	if err := coding.NormalizeProposal(&proposal, time.Now()); err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	environment, err := s.store.SetCodingEnvironmentProposal(r.Context(), grant.WorkspaceKey, session.EnvironmentID, &proposal)
	if err != nil {
		writeCodingEnvironmentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"environmentId": environment.ID, "missingSecrets": proposal.MissingSecrets})
}
