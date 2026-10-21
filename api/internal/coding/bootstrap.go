package coding

import (
	"context"
	"strings"

	"flow/api/internal/domain"
)

// Bootstrap assembly (design §16): the runner's whole session spec, built
// from the session, its environment, the workspace policy and the creator.

func (s *Service) buildBootstrap(ctx context.Context, grant SandboxGrant, session domain.CodingSession, turn *Turn) (BootstrapResponse, error) {
	info, err := s.workspaces.CodingWorkspace(ctx, session.WorkspaceKey)
	if err != nil {
		return BootstrapResponse{}, err
	}
	policy := NormalizeAgentSettings(info.Settings.CodingAgentSettings)
	env := domain.CodingEnvironment{ID: session.EnvironmentID}
	if s.envs != nil && session.EnvironmentID != "" {
		if env, err = s.envs.CodingEnvironment(ctx, session.WorkspaceKey, session.EnvironmentID); err != nil {
			return BootstrapResponse{}, err
		}
	}
	user := UserInfo{ID: session.CreatorID}
	issue := IssueContext{ID: session.IssueID}
	if s.directory != nil {
		if found, err := s.directory.CodingUser(ctx, session.WorkspaceKey, session.CreatorID); err == nil {
			user = found
		}
		if session.IssueID != "" {
			if found, err := s.directory.CodingIssue(ctx, session.WorkspaceKey, session.IssueID); err == nil {
				issue = found
			}
		}
	}
	runtime := strings.TrimRight(s.cfg.RuntimeURL, "/")
	dependencies := env.DependencyManagement
	if dependencies == "" {
		dependencies = domain.CodingDependenciesCustomAndGenerated
	}
	response := BootstrapResponse{
		SandboxID: grant.SandboxID, SessionID: session.ID, SnapshotID: session.SnapshotID, Mode: ModeSession,
		Repo:        BootstrapRepo{CodeHost: session.CodeHost, HostName: session.HostName, FullName: session.Repository, CloneURL: CloneURL(session.HostName, session.Repository), BaseRef: session.BaseRef, BranchName: session.BranchName, ExistingBranch: session.ExistingBranch, BranchPrefix: sessionBranchPrefix(session, fallback(s.cfg.BranchPrefix, "flow/"))},
		Harness:     BootstrapHarness{Name: session.Harness, Model: session.Model, ReasoningEffort: session.ReasoningEffort, ResumeID: session.HarnessResumeID},
		LLM:         BootstrapLLM{AnthropicBaseURL: runtime + "/internal/llm-proxy/anthropic", OpenAIBaseURL: runtime + "/internal/llm-proxy/openai/v1", OpenSourceBaseURL: runtime + "/internal/llm-proxy/opensource/v1"},
		Environment: BootstrapEnv{EnvironmentID: env.ID, Mise: env.Mise, PrepareScript: env.PrepareScript, DependencyManagement: dependencies, HasSecrets: len(env.Secrets) > 0},
		Git:         s.sessionGitIdentity(ctx, session, s.signingIdentity(ctx, session, user, policy.CommitSigningEnabled), policy.CommitSigningEnabled),
		Browser:     BootstrapBrowser{Enabled: s.cfg.BrowserEnabled},
		Restored:    session.SnapshotID != "" && s.sandboxRestoresSnapshot(ctx, session, grant.SandboxID),
		Turn:        turn,
		Limits:      BootstrapLimits{TurnTimeoutSeconds: int(s.cfg.Timeouts.Turn.Seconds()), HeartbeatSeconds: int(HeartbeatInterval.Seconds()), SetupTimeoutSeconds: int(s.cfg.Timeouts.Setup.Seconds()), StopGraceSeconds: int(s.cfg.Timeouts.StopGrace.Seconds())},
	}
	response.Repo.BaseSHA = s.unpublishedBaseline(ctx, session)
	response.Repo.BranchUnconfirmed = branchUnconfirmed(session, response.Repo.BranchPrefix)
	if s.usesGitRelay(session) {
		response.Repo.PushURL = GitRelayURL(runtime, session.HostName, session.Repository)
	}
	browser := ""
	if s.cfg.BrowserEnabled && s.BrowserGuidance != nil {
		browser = s.BrowserGuidance()
	}
	host := ""
	if s.HostGuidance != nil {
		host = s.HostGuidance(session)
	}
	if s.MCPServers != nil {
		response.MCPServers = s.MCPServers(ctx, session, runtime)
	}
	response.Guidance = Guidance(GuidanceInput{Workspace: info.Settings.AgentInstructions, Team: issue.Guidance, Environment: env.CustomGuidance, Issue: issue, BranchName: session.BranchName, BaseRef: session.BaseRef, CodeHost: session.CodeHost, Host: host, Browser: browser, MCPServers: response.MCPServers})
	return response, nil
}

// branchUnconfirmed reports whether ExistingBranch is only a guess: sessions
// flagged by the old head-SHA heuristic carry the flag without any recorded
// push. A recorded push (BranchPushed), a PR/MR, a PR-sourced session or a
// branch outside the session branch prefix (someone else's branch) is
// confirmed evidence and is never recovered by starting over.
func branchUnconfirmed(session domain.CodingSession, prefix string) bool {
	if !session.ExistingBranch || session.BranchPushed || session.Pushed || len(session.PullRequests) > 0 || session.Source == domain.CodingSourcePullRequest {
		return false
	}
	return prefix != "" && strings.HasPrefix(session.BranchName, prefix)
}

// unpublishedBaseline is the exact revision an unpublished branch is created
// from again: the head an earlier turn reported when it never committed (the
// clone baseline). Empty for a new or published branch, after unpushed
// commits (their head never reached the code host) or when the commits
// cannot be read; the runner then branches from the base ref.
func (s *Service) unpublishedBaseline(ctx context.Context, session domain.CodingSession) string {
	if session.ExistingBranch || session.HeadSHA == "" {
		return ""
	}
	if commits, err := s.store.CodingCommits(ctx, session.ID); err != nil || len(commits) > 0 {
		return ""
	}
	return session.HeadSHA
}

// sandboxRestoresSnapshot reports whether the sandbox was started with the
// snapshot as its workspace (the restore step or a restored volume ran before
// the runner). A warm sandbox bound to the session was not: its runner
// restores the archive itself after bootstrap, so Restored is false for it.
func (s *Service) sandboxRestoresSnapshot(ctx context.Context, session domain.CodingSession, sandboxID string) bool {
	records, err := s.store.CodingSandboxes(ctx, session.WorkspaceKey, session.ID)
	if err != nil {
		return true // keep the previous behavior when the record cannot be read
	}
	for _, record := range records {
		if record.ID == sandboxID {
			return record.Workspace.ArchiveURL != "" || record.Workspace.VolumeID != ""
		}
	}
	return true
}

// CloneURL is the https clone URL of a repository (credentials come from the helper).
func CloneURL(hostName, repository string) string {
	if hostName == "" || repository == "" {
		return ""
	}
	return "https://" + hostName + "/" + repository + ".git"
}

// gitIdentity is the commit identity (design §13): the requesting user is
// the author; the committer is the bot unless commits are signed with the
// user's key (hosts verify the signature against the committer).
func (s *Service) gitIdentity(session domain.CodingSession, user UserInfo, signed bool) BootstrapGit {
	botName, botEmail := "Flow", "flow@users.noreply."+fallback(session.HostName, "flow.local")
	if session.CodeHost != domain.CodeHostGitLab && s.cfg.GitHubApp.Slug != "" {
		botName = s.cfg.GitHubApp.Slug + "[bot]"
		botEmail = s.cfg.GitHubApp.AppID + "+" + s.cfg.GitHubApp.Slug + "[bot]@users.noreply." + fallback(s.cfg.GitHubApp.Host, "github.com")
	}
	authorName := fallback(user.Name, botName)
	authorEmail := user.Email
	if authorEmail == "" && user.Login != "" && session.CodeHost == domain.CodeHostGitHub {
		authorEmail = user.Login + "@users.noreply." + fallback(session.HostName, "github.com")
	}
	authorEmail = fallback(authorEmail, botEmail)
	git := BootstrapGit{AuthorName: authorName, AuthorEmail: authorEmail, CommitterName: botName, CommitterEmail: botEmail, CoAuthorTrailer: coAuthorTrailer(botName, botEmail)}
	if signed && user.SigningFormat != "" {
		git.CommitterName, git.CommitterEmail = authorName, authorEmail
		git.SigningFormat, git.SigningPublicKey = user.SigningFormat, user.SigningPublicKey
	}
	return git
}

// coAuthorTrailer is the trailer line every commit of a session carries, in
// Claude Code's spelling ("Co-Authored-By: Name <email>"). The name and email
// are the bot's identity, so the code host links the commit to the app.
func coAuthorTrailer(name, email string) string {
	return "Co-Authored-By: " + name + " <" + email + ">"
}

func fallback(value, otherwise string) string {
	if strings.TrimSpace(value) == "" {
		return otherwise
	}
	return value
}

// GuidanceInput is what the bootstrap guidance is assembled from.
type GuidanceInput struct {
	Workspace   string
	Team        string
	Environment string
	Issue       IssueContext
	BranchName  string
	BaseRef     string
	CodeHost    string
	// Host is code-host specific guidance (glab usage for GitLab).
	Host    string
	Browser string
	// MCPServers are the workspace MCP servers behind Flow's gateway.
	MCPServers []BootstrapMCPServer
}

// Guidance assembles the system prompt addition for the harness.
func Guidance(in GuidanceInput) string {
	var out strings.Builder
	section := func(title, body string) {
		body = strings.TrimSpace(body)
		if body == "" {
			return
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString("## " + title + "\n\n" + body)
	}
	review := "pull request"
	if in.CodeHost == domain.CodeHostGitLab {
		review = "merge request"
	}
	section("Flow coding session", strings.Join([]string{
		"You are Flow's coding agent working in an isolated sandbox on branch `" + in.BranchName + "` (base `" + fallback(in.BaseRef, "main") + "`).",
		"Make focused changes, run the project's tests and linters when they exist, and leave the working tree in a state that builds.",
		"Flow commits and pushes your work at the end of the turn and opens a draft " + review + "; do not create or merge " + review + "s yourself and do not push other branches.",
		"Never print, commit or exfiltrate secrets or tokens.",
		"When you need a decision from a person, ask with the ask_user tool instead of guessing.",
	}, "\n"))
	if in.Issue.Identifier != "" || in.Issue.Title != "" {
		issue := strings.TrimSpace(in.Issue.Identifier + " " + in.Issue.Title)
		if in.Issue.URL != "" {
			issue += "\n" + in.Issue.URL
		}
		if description := strings.TrimSpace(in.Issue.Description); description != "" {
			if len(description) > 16<<10 {
				description = description[:16<<10] + "…"
			}
			issue += "\n\n" + description
		}
		section("Issue", issue)
	}
	section("Workspace guidance", in.Workspace)
	section("Team guidance", in.Team)
	section("Repository guidance", in.Environment)
	section("Code host", in.Host)
	section("Browser testing", in.Browser)
	if len(in.MCPServers) > 0 {
		lines := []string{"These MCP servers were approved by your workspace and are available as tools (Flow relays the calls and records them in the audit log):"}
		for _, server := range in.MCPServers {
			lines = append(lines, "- `"+server.Name+"`: "+server.Title)
		}
		section("Workspace MCP servers", strings.Join(lines, "\n"))
	}
	return out.String()
}

// signingIdentity fills the user's signing key from the commit signing
// resolver (track E) when signed commits are required.
func (s *Service) signingIdentity(ctx context.Context, session domain.CodingSession, user UserInfo, required bool) UserInfo {
	if !required {
		return user
	}
	if session.SigningFormat != "" {
		user.SigningFormat, user.SigningPublicKey = session.SigningFormat, session.SigningPublicKey
		return user
	}
	if s.ResolveCommitSigning == nil {
		return user
	}
	if format, publicKey, err := s.ResolveCommitSigning(ctx, session.WorkspaceKey, session.CreatorID); err == nil && format != "" {
		user.SigningFormat, user.SigningPublicKey = format, publicKey
	}
	return user
}
