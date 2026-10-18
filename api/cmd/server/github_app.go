package main

import "net/http"

// GitHub App installation, setup callback and webhook (docs/coding-sessions-design.md §13).
//
// Owner: track D (GitHub App). Handlers are 501 stubs until the track implements
// them; keep the route list in sync with publicapi.InternalRoutes.
func (s *server) registerGitHubAppRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/github-app", s.getGitHubApp)
	mux.HandleFunc("POST /api/integrations/github-app/install", s.startGitHubAppInstall)
	mux.HandleFunc("GET /api/integrations/github-app/setup", s.finishGitHubAppSetup)
	mux.HandleFunc("DELETE /api/integrations/github-app/installations/{id}", s.removeGitHubAppInstallation)
	mux.HandleFunc("POST /api/integrations/github-app/webhook", s.gitHubAppWebhook)
}

// getGitHubApp — TODO(track D).
//
//	GET /api/integrations/github-app: App configuration status and this workspace's installations.
func (s *server) getGitHubApp(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }

// startGitHubAppInstall — TODO(track D).
//
//	POST /api/integrations/github-app/install: Returns the installation URL with a signed state.
func (s *server) startGitHubAppInstall(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// finishGitHubAppSetup — TODO(track D).
//
//	GET /api/integrations/github-app/setup: Setup URL GitHub redirects to after install (installation_id, state).
func (s *server) finishGitHubAppSetup(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// removeGitHubAppInstallation — TODO(track D).
//
//	DELETE /api/integrations/github-app/installations/{id}: Unlinks an installation from the workspace.
func (s *server) removeGitHubAppInstallation(w http.ResponseWriter, r *http.Request) {
	codingNotImplemented(w, r)
}

// gitHubAppWebhook — TODO(track D).
//
//	POST /api/integrations/github-app/webhook: App webhook (X-Hub-Signature-256): installations, PRs, comments, reviews, checks.
func (s *server) gitHubAppWebhook(w http.ResponseWriter, r *http.Request) { codingNotImplemented(w, r) }
