package codingenv

import (
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestNormalizeEnvironment(t *testing.T) {
	for _, test := range []struct {
		name  string
		input domain.CodingEnvironment
		check func(t *testing.T, env domain.CodingEnvironment)
		field string // expected invalid field; "" = valid
	}{
		{name: "defaults", input: domain.CodingEnvironment{Repository: "Acme/Web"}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.CodeHost != "github" || env.HostName != "github.com" || env.Name != "Web" || env.Repository != "Acme/Web" {
				t.Fatalf("%+v", env)
			}
		}},
		{name: "github url", input: domain.CodingEnvironment{Repository: "https://github.com/acme/web.git"}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.Repository != "acme/web" || env.HostName != "github.com" {
				t.Fatalf("%+v", env)
			}
		}},
		{name: "scp url ghe", input: domain.CodingEnvironment{Repository: "git@ghe.corp:acme/web.git"}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.Repository != "acme/web" || env.HostName != "ghe.corp" {
				t.Fatalf("%+v", env)
			}
		}},
		{name: "gitlab subgroups", input: domain.CodingEnvironment{CodeHost: "gitlab", Repository: "https://gitlab.example.com/g/sub/proj/-/tree/main"}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.Repository != "g/sub/proj" || env.HostName != "gitlab.example.com" {
				t.Fatalf("%+v", env)
			}
		}},
		{name: "host mismatch", input: domain.CodingEnvironment{HostName: "github.com", Repository: "https://ghe.corp/a/b"}, field: "repository"},
		{name: "github three parts", input: domain.CodingEnvironment{Repository: "a/b/c"}, field: "repository"},
		{name: "bad code host", input: domain.CodingEnvironment{CodeHost: "bitbucket", Repository: "a/b"}, field: "codeHost"},
		{name: "dotdot", input: domain.CodingEnvironment{Repository: "a/.."}, field: "repository"},
		{name: "legacy harness", input: domain.CodingEnvironment{Repository: "a/b", Harness: "claude"}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.Harness != domain.CodingHarnessClaudeCode {
				t.Fatal(env.Harness)
			}
		}},
		{name: "bad harness", input: domain.CodingEnvironment{Repository: "a/b", Harness: "x"}, field: "harness"},
		{name: "bad network", input: domain.CodingEnvironment{Repository: "a/b", Network: "wide"}, field: "network"},
		{name: "domains", input: domain.CodingEnvironment{Repository: "a/b", AllowedDomains: []string{" .Example.com ", "api.x.io", "api.x.io"}}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if len(env.AllowedDomains) != 2 || env.AllowedDomains[0] != ".example.com" {
				t.Fatal(env.AllowedDomains)
			}
		}},
		{name: "bad domain", input: domain.CodingEnvironment{Repository: "a/b", AllowedDomains: []string{"http://x"}}, field: "allowedDomains"},
		{name: "bad size", input: domain.CodingEnvironment{Repository: "a/b", SandboxSize: "xl"}, field: "sandboxSize"},
		{name: "owners dedupe", input: domain.CodingEnvironment{Repository: "a/b", OwnerIDs: []string{"u1", " u1", "", "u2"}}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if len(env.OwnerIDs) != 2 {
				t.Fatal(env.OwnerIDs)
			}
		}},
		{name: "mise tools", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{Tools: map[string]string{"node": " 22 ", "go": "", "npm:pnpm": "9"}}}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.Mise.Tools["node"] != "22" || env.Mise.Tools["go"] != "latest" || env.Mise.Tools["npm:pnpm"] != "9" {
				t.Fatal(env.Mise.Tools)
			}
		}},
		{name: "bad tool version", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{Tools: map[string]string{"node": "22;rm -rf"}}}, field: "mise.tools"},
		{name: "file escape", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{Files: []domain.CodingMiseFile{{Path: "../etc/passwd"}}}}, field: "mise.files"},
		{name: "file abs", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{Files: []domain.CodingMiseFile{{Path: "/etc/x"}}}}, field: "mise.files"},
		{name: "file home", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{Files: []domain.CodingMiseFile{{Path: "~/.npmrc", Content: "x"}}}}, check: func(t *testing.T, env domain.CodingEnvironment) {
			if env.Mise.Files[0].Path != ".npmrc" {
				t.Fatal(env.Mise.Files)
			}
		}},
		{name: "reserved var", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{EnvironmentVariables: []domain.CodingEnvVar{{Name: "FLOW_TOKEN_FILE", Value: "x"}}}}, field: "mise.environmentVariables"},
		{name: "duplicate var", input: domain.CodingEnvironment{Repository: "a/b", Mise: domain.CodingMiseConfig{EnvironmentVariables: []domain.CodingEnvVar{{Name: "A"}, {Name: "A"}}}}, field: "mise.environmentVariables"},
		{name: "bad dependency mode", input: domain.CodingEnvironment{Repository: "a/b", DependencyManagement: "auto"}, field: "dependencyManagement"},
		{name: "script too long", input: domain.CodingEnvironment{Repository: "a/b", PrepareScript: strings.Repeat("x", EnvironmentScriptMax+1)}, field: "prepareScript"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := test.input
			err := NormalizeEnvironment(&env)
			if test.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				if test.check != nil {
					test.check(t, env)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != test.field || !errors.Is(err, ErrInvalidEnvironment) {
				t.Fatalf("got %v, want invalid %s", err, test.field)
			}
		})
	}
}

func TestActiveRepoKey(t *testing.T) {
	if ActiveRepoKey("github", "github.com", "Acme/Web") != ActiveRepoKey("github", "GitHub.com", "acme/web") {
		t.Fatal("repo keys must be case-insensitive")
	}
	long := ActiveRepoKey("gitlab", "gitlab.com", strings.Repeat("g/", 120)+"p")
	if len(long) > 191 || !strings.HasPrefix(long, "sha256:") {
		t.Fatal(long)
	}
}
