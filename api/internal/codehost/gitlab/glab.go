package gitlab

import (
	"net/url"
	"slices"
	"strings"
)

// glab runtime auth contract (consumed by the runner, track C,
// sandbox-runner/src/cli-env.ts). Nothing is configured at image build time.
//
//  1. The runner calls POST /internal/coding-runtime/credentials
//     {purpose:"cli"} and receives Credential.CLIEnv (keys below).
//  2. It writes them to ~/.flow/cli-env; the /opt/flow/bin/glab wrapper
//     re-sources that file on every call, so a refreshed token takes effect
//     without restarting the harness. Refresh before Credential.ExpiresAt.
//  3. GITLAB_TOKEN authenticates glab for GITLAB_HOST (glab gives the env
//     token precedence over any config file). For a self-managed host
//     (GITLAB_HOST != gitlab.com) the runner additionally runs
//     GlabConfigCommands once per sandbox so glab treats the host as the
//     default and talks HTTPS/HTTP as configured. The config written there
//     never contains the token.
//  4. The env is exported only into the harness process environment, never
//     into setup scripts.
const (
	EnvGitLabToken   = "GITLAB_TOKEN"
	EnvGitLabHost    = "GITLAB_HOST"
	EnvAPIProtocol   = "GITLAB_API_PROTOCOL"
	EnvGlabConfigDir = "GLAB_CONFIG_DIR"
	EnvNoPrompt      = "NO_PROMPT"
	// GlabConfigDir lives in the sandbox home (emptyDir), not in the image.
	GlabConfigDir = "/home/flow/.config/glab-cli"
)

// CLIEnv is the glab environment for a credential: the token, the host
// (with a relative root when GitLab is served under a path) and the API
// protocol.
func CLIEnv(connection Connection, token string) map[string]string {
	host, protocol := connection.HostName, "https"
	if parsed, err := url.Parse(connection.BaseURL); err == nil && parsed.Host != "" {
		host = strings.ToLower(parsed.Host) + strings.TrimRight(parsed.EscapedPath(), "/")
		if parsed.Scheme == "http" {
			protocol = "http"
		}
	}
	if host == "" {
		host = "gitlab.com"
	}
	return map[string]string{
		EnvGitLabToken:   token,
		EnvGitLabHost:    host,
		EnvAPIProtocol:   protocol,
		EnvGlabConfigDir: GlabConfigDir,
		EnvNoPrompt:      "1",
	}
}

// GlabConfigCommands are the commands the runner runs once (with the CLI env
// exported) to make a self-managed host glab's default. gitlab.com needs none.
func GlabConfigCommands(env map[string]string) [][]string {
	host := strings.TrimSpace(env[EnvGitLabHost])
	if host == "" || host == "gitlab.com" {
		return nil
	}
	protocol := env[EnvAPIProtocol]
	if protocol != "http" {
		protocol = "https"
	}
	return [][]string{
		{"glab", "config", "set", "--global", "host", host},
		{"glab", "config", "set", "--host", host, "api_protocol", protocol},
		{"glab", "config", "set", "--host", host, "git_protocol", "https"},
	}
}

// CanWriteNotes reports whether sandbox credentials can post MR notes with
// glab (`glab mr note` needs the api scope).
func CanWriteNotes(mode string, scopes []string) bool {
	if mode != ModeProjectToken {
		return true // oauth/connection tokens carry the connection's own scopes
	}
	return slices.Contains(scopes, "api")
}

// AgentGuidance is the GitLab block the session layer (track B) appends to
// the bootstrap guidance for sessions whose environment uses GitLab.
func AgentGuidance(hostName string, canWriteNotes bool) string {
	if hostName == "" {
		hostName = "gitlab.com"
	}
	var b strings.Builder
	b.WriteString("## GitLab\n\n")
	b.WriteString("This repository is hosted on GitLab (" + hostName + "). The `glab` CLI is already authenticated with a short-lived token for this project only. Do not run `glab auth login` and do not print or store the token.\n\n")
	b.WriteString("Use glab to read context:\n\n")
	b.WriteString("- `glab mr view <iid> --comments` (or `glab mr view --comments` on the session branch) to read the merge request and its discussion.\n")
	b.WriteString("- `glab mr diff <iid>` to read the merge request diff.\n")
	b.WriteString("- `glab ci status --branch <branch>` for the latest pipeline of a branch.\n")
	b.WriteString("- `glab ci get --branch <branch>` for the pipeline's jobs, and `glab ci trace <job-id>` for a failed job's log. `glab ci view` is an interactive viewer: only use it with a terminal, never in scripts.\n")
	if canWriteNotes {
		b.WriteString("- `glab mr note <iid> -m \"…\"` to reply on the merge request when a reviewer asked you something.\n")
	} else {
		b.WriteString("- The token cannot post merge request notes. Put replies to reviewers in your final response; Flow posts it.\n")
	}
	b.WriteString("\nFlow opens and updates the draft merge request itself after each turn. Never run `glab mr create`, `glab mr merge`, `glab mr approve`, `glab mr close` or `glab mr update`, and never change CI/CD variables, releases or project settings. Push only the session branch.\n")
	return b.String()
}

// ModeInfo describes a credential mode for the settings UI and
// /api/coding/status.
type ModeInfo struct {
	Mode        string `json:"mode"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Requirements lists what the connection needs for this mode.
	Requirements string `json:"requirements,omitempty"`
	// Warning is shown as a red banner when the mode is active.
	Warning string `json:"warning,omitempty"`
}

// CredentialModes is the copy for FLOW_CODING_GITLAB_CREDENTIAL_MODE.
var CredentialModes = []ModeInfo{
	{
		Mode:         ModeProjectToken,
		Title:        "Project access tokens",
		Description:  "Flow creates a project access token (Developer role) for each session, scoped to one project, and revokes it when the session ends.",
		Requirements: "The GitLab connection token needs the api scope and the Maintainer role (or higher) on each project. On GitLab.com, project access tokens need the Premium or Ultimate tier.",
	},
	{
		Mode:         ModeOAuth,
		Title:        "OAuth token",
		Description:  "Sandboxes use the workspace's GitLab OAuth access token, which expires after about two hours and is refreshed by Flow.",
		Requirements: "Connect GitLab with OAuth.",
		Warning:      "The OAuth token is not limited to one project: it reaches every project the authorizing user can access.",
	},
	{
		Mode:         ModeConnection,
		Title:        "Connection token",
		Description:  "Sandboxes use the GitLab connection token itself.",
		Warning:      "The connection token is long-lived and broad. Agent-run code in the sandbox can read and use it. Use project access tokens where possible.",
		Requirements: "Any GitLab connection.",
	},
}

// CredentialModeInfo returns the copy for mode (project-token when unknown).
func CredentialModeInfo(mode string) ModeInfo {
	for _, info := range CredentialModes {
		if info.Mode == mode {
			return info
		}
	}
	return CredentialModes[0]
}

// SessionGuidance is AgentGuidance for this host's credential mode and token
// scopes (the session core's HostGuidance hook calls it).
func (h *Host) SessionGuidance(hostName string) string {
	return AgentGuidance(hostName, CanWriteNotes(h.CredentialMode(), h.tokenScopes()))
}
