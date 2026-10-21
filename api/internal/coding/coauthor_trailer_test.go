package coding

import (
	"strings"
	"testing"

	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Every session commit carries one "Co-Authored-By: <bot> <email>" trailer
// in Claude Code's spelling; the bot identity decides how the host links it.
func TestGitIdentityCoAuthorTrailer(t *testing.T) {
	app := config.CodingConfig{GitHubApp: config.GitHubAppConfig{AppID: "42", Slug: "flow-app", Host: "github.com"}}
	user := UserInfo{Name: "Ada", Email: "ada@example.com"}
	cases := []struct {
		name    string
		cfg     config.CodingConfig
		session domain.CodingSession
		signed  bool
		want    string
	}{
		{"github app", app, domain.CodingSession{CodeHost: domain.CodeHostGitHub, HostName: "github.com"}, false,
			"Co-Authored-By: flow-app[bot] <42+flow-app[bot]@users.noreply.github.com>"},
		{"github enterprise app", config.CodingConfig{GitHubApp: config.GitHubAppConfig{AppID: "7", Slug: "flow", Host: "ghe.acme.dev"}}, domain.CodingSession{CodeHost: domain.CodeHostGitHub, HostName: "ghe.acme.dev"}, false,
			"Co-Authored-By: flow[bot] <7+flow[bot]@users.noreply.ghe.acme.dev>"},
		{"gitlab ignores the github app", app, domain.CodingSession{CodeHost: domain.CodeHostGitLab, HostName: "gitlab.com"}, false,
			"Co-Authored-By: Flow <flow@users.noreply.gitlab.com>"},
		{"no app", config.CodingConfig{}, domain.CodingSession{CodeHost: domain.CodeHostGitHub, HostName: "github.com"}, false,
			"Co-Authored-By: Flow <flow@users.noreply.github.com>"},
		{"signed commits keep the bot trailer", app, domain.CodingSession{CodeHost: domain.CodeHostGitHub, HostName: "github.com"}, true,
			"Co-Authored-By: flow-app[bot] <42+flow-app[bot]@users.noreply.github.com>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := user
			if c.signed {
				u.SigningFormat, u.SigningPublicKey = "ssh", "ssh-ed25519 AAAA"
			}
			service := &Service{cfg: c.cfg}
			git := service.gitIdentity(c.session, u, c.signed)
			if git.CoAuthorTrailer != c.want {
				t.Fatalf("trailer %q, want %q", git.CoAuthorTrailer, c.want)
			}
			if strings.Count(git.CoAuthorTrailer, "\n") != 0 {
				t.Fatalf("trailer must be one line: %q", git.CoAuthorTrailer)
			}
			if c.signed && git.CommitterEmail != "ada@example.com" {
				t.Fatalf("signed commits are committed by the member: %+v", git)
			}
		})
	}
}
