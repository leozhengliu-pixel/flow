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
		Restored:    session.SnapshotID != "",
		Turn:        turn,
		Limits:      BootstrapLimits{TurnTimeoutSeconds: int(s.cfg.Timeouts.Turn.Seconds()), HeartbeatSeconds: int(HeartbeatInterval.Seconds()), SetupTimeoutSeconds: int(s.cfg.Timeouts.Setup.Seconds()), StopGraceSeconds: int(s.cfg.Timeouts.StopGrace.Seconds())},
	}
	browser := ""
	if s.cfg.BrowserEnabled && s.BrowserGuidance != nil {
		browser = s.BrowserGuidance()
	}
	host := ""
	if s.HostGuidance != nil {
		host = s.HostGuidance(session)
	}
	response.Guidance = Guidance(GuidanceInput{Workspace: info.Settings.AgentInstructions, Team: issue.Guidance, Environment: env.CustomGuidance, Issue: issue, BranchName: session.BranchName, BaseRef: session.BaseRef, CodeHost: session.CodeHost, Host: host, Browser: browser})
	return response, nil
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
	git := BootstrapGit{AuthorName: authorName, AuthorEmail: authorEmail, CommitterName: botName, CommitterEmail: botEmail, CoAuthorTrailer: "Co-authored-by: Flow <" + botEmail + ">"}
	if signed && user.SigningFormat != "" {
		git.CommitterName, git.CommitterEmail = authorName, authorEmail
		git.SigningFormat, git.SigningPublicKey = user.SigningFormat, user.SigningPublicKey
	}
	return git
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
