package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"flow/api/internal/codehost"
	githubhost "flow/api/internal/codehost/github"
	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// GitHub App installation, setup callback and webhook (docs/coding-sessions-design.md §13).
//
// Owner: track D (GitHub App). Keep the route list in sync with
// publicapi.InternalRoutes.
func (s *server) registerGitHubAppRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/github-app", s.getGitHubApp)
	mux.HandleFunc("POST /api/integrations/github-app/install", s.startGitHubAppInstall)
	mux.HandleFunc("GET /api/integrations/github-app/setup", s.finishGitHubAppSetup)
	mux.HandleFunc("DELETE /api/integrations/github-app/installations/{id}", s.removeGitHubAppInstallation)
	mux.HandleFunc("POST /api/integrations/github-app/webhook", s.gitHubAppWebhook)
	mux.HandleFunc("POST /api/integrations/github-app/authorize", s.startGitHubAppAuthorization)
	mux.HandleFunc("DELETE /api/integrations/github-app/authorization", s.removeGitHubAppAuthorization)
}

// The store implements what the GitHub host and the coding layer need.
var (
	_ codehost.Installations    = (*store.SQLiteStore)(nil)
	_ coding.ChangeRequestStore = (*store.SQLiteStore)(nil)
)

// codingWebhookDispatch hands a verified, normalized code host webhook to the
// coding session layer for follow-ups (track L assigns it, from an init in
// coding_webhook_dispatch.go, to its dispatchCodingWebhook). Nil until then.
var codingWebhookDispatch func(s *server, ctx context.Context, workspaceKey string, event codehost.WebhookEvent) error

// gitHubAppHost is the GitHub code host, or nil when coding hosts were not built.
func (s *server) gitHubAppHost() *githubhost.Host {
	if s.coding == nil {
		return nil
	}
	host, _ := s.coding.hosts[domain.CodeHostGitHub].(*githubhost.Host)
	return host
}

func appBaseURL() string {
	if value := strings.TrimRight(strings.TrimSpace(os.Getenv("FLOW_APP_URL")), "/"); value != "" {
		return value
	}
	return "http://localhost:5173"
}

// gitHubAppStatus is GitHubAppStatus in web/src/types/coding-sessions.ts.
type gitHubAppStatus struct {
	Configured bool   `json:"configured"`
	HostName   string `json:"hostName"`
	Slug       string `json:"slug,omitempty"`
	// CanLinkAccount: user-to-server OAuth is configured, so members can link
	// their GitHub account for personal access checks.
	CanLinkAccount bool                    `json:"canLinkAccount"`
	LinkedAccount  *gitHubAppLinkedAccount `json:"linkedAccount,omitempty"`
	Installations  []gitHubAppInstallation `json:"installations"`
}

type gitHubAppLinkedAccount struct {
	Login    string    `json:"login"`
	LinkedAt time.Time `json:"linkedAt"`
}

type gitHubAppInstallation struct {
	ID                  string     `json:"id"`
	InstallationID      string     `json:"installationId"`
	AccountLogin        string     `json:"accountLogin"`
	AccountType         string     `json:"accountType,omitempty"`
	RepositorySelection string     `json:"repositorySelection,omitempty"`
	SuspendedAt         *time.Time `json:"suspendedAt,omitempty"`
}

// getGitHubApp — GET /api/integrations/github-app: App configuration status,
// this workspace's installations and the viewer's linked GitHub account.
func (s *server) getGitHubApp(w http.ResponseWriter, r *http.Request) {
	host := s.gitHubAppHost()
	status := gitHubAppStatus{Installations: []gitHubAppInstallation{}}
	if host == nil {
		writeJSON(w, http.StatusOK, status)
		return
	}
	status.Configured, status.HostName, status.Slug, status.CanLinkAccount = host.Configured(), host.HostName(), host.Slug(), host.CanAuthorizeUsers()
	workspace := s.workspaceData(r).Workspace.URLKey
	installations, err := s.store.CodeHostInstallations(r.Context(), workspace, domain.CodeHostGitHub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read GitHub App installations")
		return
	}
	for _, item := range installations {
		if item.HostName != "" && !strings.EqualFold(item.HostName, host.HostName()) {
			continue
		}
		status.Installations = append(status.Installations, gitHubAppInstallation{ID: item.ID, InstallationID: item.InstallationID, AccountLogin: item.AccountLogin, AccountType: item.AccountType, RepositorySelection: item.RepositorySelection, SuspendedAt: item.SuspendedAt})
	}
	if user := authUser(r); user.ID != "" && status.Configured {
		if link, err := host.UserLink(r.Context(), user.ID); err == nil {
			status.LinkedAccount = &gitHubAppLinkedAccount{Login: link.Login, LinkedAt: link.LinkedAt}
		}
	}
	writeJSON(w, http.StatusOK, status)
}

// gitHubAppState is the signed state carried through GitHub's install and
// OAuth redirects: HMAC(workspace, user, purpose, nonce, expiry).
type gitHubAppState struct {
	Workspace string `json:"w"`
	User      string `json:"u"`
	Purpose   string `json:"p"` // install | link
	Nonce     string `json:"n"`
	Expires   int64  `json:"e"`
}

const gitHubAppStateTTL = 30 * time.Minute

// gitHubAppStateKey derives the state MAC key from the App's own secrets, so
// no extra configuration is needed and states die when the App is rotated.
func (s *server) gitHubAppStateKey() []byte {
	app := s.coding.config.GitHubApp
	sum := sha256.Sum256([]byte("flow/github-app-state\x00" + app.AppID + "\x00" + app.WebhookSecret + "\x00" + app.PrivateKey))
	return sum[:]
}

func (s *server) signGitHubAppState(state gitHubAppState) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	state.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	state.Expires = time.Now().Add(gitHubAppStateTTL).Unix()
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.gitHubAppStateKey())
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *server) verifyGitHubAppState(value string) (gitHubAppState, error) {
	payload, signature, ok := strings.Cut(value, ".")
	if !ok {
		return gitHubAppState{}, errors.New("malformed state")
	}
	got, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return gitHubAppState{}, errors.New("malformed state")
	}
	mac := hmac.New(sha256.New, s.gitHubAppStateKey())
	mac.Write([]byte(payload))
	if !hmac.Equal(got, mac.Sum(nil)) {
		return gitHubAppState{}, errors.New("invalid state")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return gitHubAppState{}, errors.New("malformed state")
	}
	var state gitHubAppState
	if err := json.Unmarshal(raw, &state); err != nil || state.Workspace == "" {
		return gitHubAppState{}, errors.New("malformed state")
	}
	if time.Now().Unix() > state.Expires {
		return gitHubAppState{}, errors.New("the GitHub App link expired; start again from Flow")
	}
	return state, nil
}

// startGitHubAppInstall — POST /api/integrations/github-app/install (admins):
// returns the installation URL with a signed state.
func (s *server) startGitHubAppInstall(w http.ResponseWriter, r *http.Request) {
	host := s.gitHubAppHost()
	if host == nil || !host.Configured() || host.Slug() == "" {
		writeError(w, http.StatusConflict, "The GitHub App is not configured (FLOW_GITHUB_APP_ID, _SLUG, _PRIVATE_KEY and _WEBHOOK_SECRET)")
		return
	}
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	state, err := s.signGitHubAppState(gitHubAppState{Workspace: s.workspaceData(r).Workspace.URLKey, User: authUser(r).ID, Purpose: "install"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start the GitHub App installation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": host.InstallURL(state)})
}

func gitHubAppSetupURL() string { return appBaseURL() + "/api/integrations/github-app/setup" }

// startGitHubAppAuthorization — POST /api/integrations/github-app/authorize:
// returns the user-to-server OAuth URL that links the viewer's GitHub account
// (Linear's personal access check, requiresGithubConnection). The App's
// callback URL must be the setup URL.
func (s *server) startGitHubAppAuthorization(w http.ResponseWriter, r *http.Request) {
	host := s.gitHubAppHost()
	if host == nil || !host.CanAuthorizeUsers() {
		writeError(w, http.StatusConflict, "GitHub account linking needs FLOW_GITHUB_APP_CLIENT_ID and FLOW_GITHUB_APP_CLIENT_SECRET")
		return
	}
	user := authUser(r)
	if user.ID == "" {
		writeError(w, http.StatusUnauthorized, "sign in to link a GitHub account")
		return
	}
	state, err := s.signGitHubAppState(gitHubAppState{Workspace: s.workspaceData(r).Workspace.URLKey, User: user.ID, Purpose: "link"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not start GitHub authorization")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": host.AuthorizeURL(state, gitHubAppSetupURL())})
}

// removeGitHubAppAuthorization — DELETE /api/integrations/github-app/authorization:
// forgets the viewer's linked GitHub account.
func (s *server) removeGitHubAppAuthorization(w http.ResponseWriter, r *http.Request) {
	host := s.gitHubAppHost()
	user := authUser(r)
	if host == nil || user.ID == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.DeleteConnectorSecret(r.Context(), githubhost.UserLinkSecretID(host.HostName(), user.ID)); err != nil {
		writeError(w, http.StatusInternalServerError, "could not unlink the GitHub account")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// finishGitHubAppSetup — GET /api/integrations/github-app/setup: GitHub
// redirects here after an installation (installation_id, setup_action, state)
// and after user authorization (code, state). The signed state must belong to
// the signed-in user, who must still be a member (an admin, to install) of
// the state's workspace.
func (s *server) finishGitHubAppSetup(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	host := s.gitHubAppHost()
	if host == nil || !host.Configured() {
		s.gitHubAppSetupResult(w, r, "", "error", "The GitHub App is not configured")
		return
	}
	state, err := s.verifyGitHubAppState(query.Get("state"))
	if err != nil {
		s.gitHubAppSetupResult(w, r, "", "error", err.Error())
		return
	}
	user := authUser(r)
	if !s.authDisabled && (user.ID == "" || user.ID != state.User) {
		s.gitHubAppSetupResult(w, r, state.Workspace, "error", "This GitHub App link was started by another user")
		return
	}
	if err := s.checkGitHubAppMember(r.Context(), state.Workspace, state.User, state.Purpose == "install"); err != nil {
		s.gitHubAppSetupResult(w, r, state.Workspace, "error", err.Error())
		return
	}
	result := ""
	if code := strings.TrimSpace(query.Get("code")); code != "" && host.CanAuthorizeUsers() {
		link, err := host.ExchangeUserCode(r.Context(), code, gitHubAppSetupURL())
		if err != nil {
			s.gitHubAppSetupResult(w, r, state.Workspace, "error", "GitHub authorization failed")
			log.Printf("github app: user authorization: %v", err)
			return
		}
		raw, _ := json.Marshal(link)
		if err := s.store.PutConnectorSecret(r.Context(), githubhost.UserLinkSecretID(host.HostName(), state.User), raw, time.Now().AddDate(10, 0, 0)); err != nil {
			s.gitHubAppSetupResult(w, r, state.Workspace, "error", "Could not save the linked GitHub account (FLOW_CONNECTOR_SECRET_KEY)")
			return
		}
		result = "linked"
	}
	if installationID := strings.TrimSpace(query.Get("installation_id")); installationID != "" && state.Purpose == "install" {
		if query.Get("setup_action") == "request" {
			// An organization owner must approve the request first; the
			// installation webhook arrives once they do and the admin repeats setup.
			s.gitHubAppSetupResult(w, r, state.Workspace, "requested", "")
			return
		}
		installation, err := host.Installation(r.Context(), installationID)
		if err != nil {
			log.Printf("github app: setup installation %s: %v", installationID, err)
			s.gitHubAppSetupResult(w, r, state.Workspace, "error", "Could not read the GitHub App installation")
			return
		}
		installation.WorkspaceKey = state.Workspace
		if err := s.store.UpsertCodeHostInstallation(r.Context(), installation); err != nil {
			s.gitHubAppSetupResult(w, r, state.Workspace, "error", "Could not save the GitHub App installation")
			return
		}
		host.InvalidateInstallation(installation.InstallationID)
		result = "installed"
	}
	if result == "" {
		s.gitHubAppSetupResult(w, r, state.Workspace, "error", "GitHub did not return an installation or authorization")
		return
	}
	s.gitHubAppSetupResult(w, r, state.Workspace, result, "")
}

// checkGitHubAppMember checks the state's user is an active member (admin
// when required) of the state's workspace.
func (s *server) checkGitHubAppMember(ctx context.Context, workspace, userID string, admin bool) error {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok || data.Workspace.URLKey != workspace {
		return errors.New("The workspace no longer exists")
	}
	if s.authDisabled {
		return nil
	}
	role, status, err := s.store.WorkspaceRole(ctx, data.Workspace.ID, userID)
	if err != nil || status != "active" {
		return errors.New("You are no longer a member of this workspace")
	}
	if admin && !workspaceAdminRole(role) {
		return errors.New("Only workspace administrators can install the GitHub App")
	}
	return nil
}

// gitHubAppSetupResult redirects browsers to the GitHub integration settings
// (with githubApp=<status>) and answers API clients with JSON.
func (s *server) gitHubAppSetupResult(w http.ResponseWriter, r *http.Request, workspace, status, message string) {
	if !wantsHTMLRedirect(r) {
		code := http.StatusOK
		if status == "error" {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"status": status, "error": message, "workspace": workspace})
		return
	}
	target := appBaseURL() + "/"
	if workspace != "" {
		target += url.PathEscape(workspace) + "/settings/integrations/github"
	}
	values := url.Values{"githubApp": {status}}
	if message != "" {
		values.Set("error", message)
	}
	http.Redirect(w, r, target+"?"+values.Encode(), http.StatusFound)
}

// removeGitHubAppInstallation — DELETE /api/integrations/github-app/installations/{id}
// (admins): unlinks an installation from this workspace. The App stays
// installed on GitHub, and other workspaces keep their links.
func (s *server) removeGitHubAppInstallation(w http.ResponseWriter, r *http.Request) {
	if !s.requireWorkspaceAdmin(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	workspace := s.workspaceData(r).Workspace.URLKey
	installations, err := s.store.CodeHostInstallations(r.Context(), workspace, domain.CodeHostGitHub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read GitHub App installations")
		return
	}
	index := slices.IndexFunc(installations, func(item domain.CodeHostInstallation) bool { return item.ID == id || item.InstallationID == id })
	if id == "" || index < 0 {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if _, err := s.store.UnlinkCodeHostInstallation(r.Context(), workspace, domain.CodeHostGitHub, installations[index].ID); err != nil {
		writeError(w, http.StatusInternalServerError, "could not remove the GitHub App installation")
		return
	}
	if host := s.gitHubAppHost(); host != nil {
		host.InvalidateInstallation(installations[index].InstallationID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// gitHubAppWebhook — POST /api/integrations/github-app/webhook: verified with
// X-Hub-Signature-256 and FLOW_GITHUB_APP_WEBHOOK_SECRET. Installation events
// keep code_host_installations current; pull request, review, comment and
// check events go to every workspace linked to the installation, once per
// delivery: session PR links are updated, the CodeReview list is synced
// (unless a classic GitHub connection already syncs that workspace), and the
// event is dispatched to coding session follow-ups.
func (s *server) gitHubAppWebhook(w http.ResponseWriter, r *http.Request) {
	host := s.gitHubAppHost()
	if host == nil || !host.Configured() {
		writeError(w, http.StatusNotFound, "The GitHub App is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 25<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read webhook")
		return
	}
	event, err := host.ParseWebhook(r.Context(), r.Header, body)
	if errors.Is(err, codehost.ErrBadSignature) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}
	switch event.Type {
	case codehost.EventIgnored:
		// Comment edits, deletions and thread resolution still update the
		// review's synced comments.
		if !githubReviewActivityEvent(r.Header.Get("X-GitHub-Event")) {
			writeJSON(w, http.StatusAccepted, map[string]any{"ignored": true, "deliveryId": event.DeliveryID})
			return
		}
	case codehost.EventInstallation:
		if err := s.applyGitHubAppInstallationEvent(r.Context(), host, event); err != nil {
			log.Printf("github app webhook %s: installation: %v", event.DeliveryID, err)
			writeError(w, http.StatusInternalServerError, "could not persist webhook")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"deliveryId": event.DeliveryID})
		return
	}
	if event.InstallationID == "" {
		writeJSON(w, http.StatusAccepted, map[string]any{"ignored": true, "reason": "no installation"})
		return
	}
	workspaces, err := s.store.WorkspacesForInstallation(r.Context(), domain.CodeHostGitHub, host.HostName(), event.InstallationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not route webhook")
		return
	}
	applied, failed := 0, 0
	for _, workspace := range workspaces {
		claimed, err := s.store.ClaimCodeHostWebhookDelivery(r.Context(), domain.CodeHostGitHub, event.DeliveryID, workspace)
		if err != nil {
			failed++
			continue
		}
		if !claimed {
			continue
		}
		if err := s.applyGitHubAppWorkspaceEvent(r.Context(), workspace, r.Header.Get("X-GitHub-Event"), event, body); err != nil {
			log.Printf("github app webhook %s: workspace %s: %v", event.DeliveryID, workspace, err)
			// Forget the claim so GitHub's redelivery is applied.
			_ = s.store.ReleaseCodeHostWebhookDelivery(context.WithoutCancel(r.Context()), domain.CodeHostGitHub, event.DeliveryID, workspace)
			failed++
			continue
		}
		applied++
	}
	if failed > 0 {
		writeError(w, http.StatusInternalServerError, "could not persist webhook")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"deliveryId": event.DeliveryID, "workspaces": applied})
}

func (s *server) applyGitHubAppInstallationEvent(ctx context.Context, host *githubhost.Host, event codehost.WebhookEvent) error {
	installation := event.Installation
	if installation == nil || installation.InstallationID == "" {
		return nil
	}
	defer host.InvalidateInstallation(installation.InstallationID)
	switch event.Action {
	case "deleted":
		return s.store.DeleteCodeHostInstallation(ctx, domain.CodeHostGitHub, host.HostName(), installation.InstallationID)
	case "created":
		// Linked to a workspace by the setup callback, which knows the workspace.
		return nil
	default: // suspend, unsuspend, new_permissions_accepted, added, removed
		return s.store.RefreshCodeHostInstallation(ctx, *installation)
	}
}

// codeReviewGitHubEvents are the App events the CodeReview sync understands.
var codeReviewGitHubEvents = []string{"pull_request", "pull_request_review", "pull_request_review_comment", "pull_request_review_thread", "issue_comment"}

func (s *server) applyGitHubAppWorkspaceEvent(ctx context.Context, workspace, githubEvent string, event codehost.WebhookEvent, body []byte) error {
	reviewID := ""
	if slices.Contains(codeReviewGitHubEvents, githubEvent) && !s.classicGitHubSync(workspace) {
		var review externalCodeReviewEvent
		if err := json.Unmarshal(body, &review); err != nil {
			return err
		}
		header := http.Header{}
		header.Set("X-GitHub-Event", githubEvent)
		if event.Type != codehost.EventIgnored && !githubReviewActivityOnly(githubEvent, review.Action) && normalizeCodeReviewEvent(domain.CodeHostGitHub, header, &review) {
			updated, err := s.applyCodeReviewEvent(ctx, workspace, domain.CodeHostGitHub, event.DeliveryID, review)
			if err != nil {
				return fmt.Errorf("code review sync: %w", err)
			}
			reviewID = updated.ID
		}
		// Files, comments, reviews and thread resolution of the review.
		if _, err := s.afterCodeReviewWebhook(ctx, workspace, domain.CodeHostGitHub, githubEvent, body); err != nil {
			return fmt.Errorf("code review comment sync: %w", err)
		}
	}
	if err := s.updateSessionPullRequests(ctx, workspace, event, body, reviewID); err != nil {
		return err
	}
	if codingWebhookDispatch != nil {
		return codingWebhookDispatch(s, ctx, workspace, event)
	}
	return nil
}

// classicGitHubSync reports whether a classic GitHub connection with a
// webhook secret already syncs this workspace's pull requests, in which case
// the App's copy of the same events is not applied twice.
func (s *server) classicGitHubSync(workspace string) bool {
	data, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		return false
	}
	return slices.ContainsFunc(data.IntegrationConnections, func(item domain.IntegrationConnection) bool {
		return item.Provider == domain.CodeHostGitHub && slices.Contains([]string{"configured", "connected"}, item.Status) &&
			(strings.TrimSpace(item.Config["webhookSecret"]) != "" || strings.TrimSpace(item.Config["webhookSecretEnv"]) != "")
	})
}

// updateSessionPullRequests mirrors PR state changes (opened, reopened,
// closed, merged, ready for review, new head) onto linked sessions.
func (s *server) updateSessionPullRequests(ctx context.Context, workspace string, event codehost.WebhookEvent, body []byte, reviewID string) error {
	switch event.Type {
	case codehost.EventChangeRequestOpened, codehost.EventChangeRequestClosed, codehost.EventChangeRequestMerged, codehost.EventChangeRequestUpdate:
	default:
		return nil
	}
	change := event.ChangeRequest
	if change == nil || change.Number <= 0 || change.Repo.FullName == "" {
		return nil
	}
	sessions, err := s.store.SessionsForChangeRequest(ctx, domain.CodeHostGitHub, event.HostName, change.Repo.FullName, change.Number)
	if err != nil {
		return err
	}
	var stamp struct {
		PullRequest struct {
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"pull_request"`
	}
	_ = json.Unmarshal(body, &stamp)
	updatedAt := stamp.PullRequest.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	for _, session := range sessions {
		if session.WorkspaceKey != workspace {
			continue
		}
		link := domain.SessionPullRequest{SessionID: session.ID, CodeHost: domain.CodeHostGitHub, HostName: event.HostName, Repository: change.Repo.FullName, Number: change.Number,
			ExternalID: change.ExternalID, URL: change.URL, Title: change.Title, State: change.State, Draft: change.Draft, HeadBranch: change.HeadBranch,
			BaseBranch: change.BaseBranch, HeadSHA: change.HeadSHA, ReviewID: reviewID, UpdatedAt: updatedAt}
		if err := s.store.UpsertSessionPullRequest(ctx, workspace, link); err != nil {
			return err
		}
	}
	return nil
}
