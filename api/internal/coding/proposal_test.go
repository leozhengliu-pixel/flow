package coding

import (
	"errors"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestSnapshotConfigHash(t *testing.T) {
	base := domain.CodingEnvironment{CodeHost: "github", HostName: "github.com", Repository: "a/b", Mise: domain.CodingMiseConfig{Tools: map[string]string{"node": "22", "go": "1.26"}, Files: []domain.CodingMiseFile{{Path: "b"}, {Path: "a"}}, EnvironmentVariables: []domain.CodingEnvVar{{Name: "Y", Value: "1"}, {Name: "X", Value: "2"}}}, PrepareScript: "npm ci"}
	hash := SnapshotConfigHash(base, "img:1", "main", []string{"S2", "S1"})
	reordered := base
	reordered.Mise = domain.CodingMiseConfig{Tools: map[string]string{"go": "1.26", "node": "22"}, Files: []domain.CodingMiseFile{{Path: "a"}, {Path: "b"}}, EnvironmentVariables: []domain.CodingEnvVar{{Name: "X", Value: "2"}, {Name: "Y", Value: "1"}}}
	if SnapshotConfigHash(reordered, "img:1", "main", []string{"S1", "S2"}) != hash {
		t.Fatal("hash must not depend on order")
	}
	defaultMode := base
	defaultMode.DependencyManagement = domain.CodingDependenciesCustomAndGenerated
	if SnapshotConfigHash(defaultMode, "img:1", "main", []string{"S1", "S2"}) != hash {
		t.Fatal("empty dependency mode is custom-and-generated")
	}
	for name, change := range map[string]func(*domain.CodingEnvironment) (string, string, []string){
		"image": func(*domain.CodingEnvironment) (string, string, []string) {
			return "img:2", "main", []string{"S1", "S2"}
		},
		"baseRef": func(*domain.CodingEnvironment) (string, string, []string) {
			return "img:1", "dev", []string{"S1", "S2"}
		},
		"secrets": func(*domain.CodingEnvironment) (string, string, []string) { return "img:1", "main", []string{"S1"} },
		"script": func(e *domain.CodingEnvironment) (string, string, []string) {
			e.PrepareScript = "pnpm i"
			return "img:1", "main", []string{"S1", "S2"}
		},
		"mode": func(e *domain.CodingEnvironment) (string, string, []string) {
			e.DependencyManagement = domain.CodingDependenciesCustomOnly
			return "img:1", "main", []string{"S1", "S2"}
		},
		"tool": func(e *domain.CodingEnvironment) (string, string, []string) {
			e.Mise.Tools = map[string]string{"node": "20", "go": "1.26"}
			return "img:1", "main", []string{"S1", "S2"}
		},
	} {
		env := base
		image, ref, secrets := change(&env)
		if SnapshotConfigHash(env, image, ref, secrets) == hash {
			t.Errorf("%s change must change the hash", name)
		}
	}
	// Changing the model or the network does not invalidate snapshots.
	other := base
	other.Model, other.Network = "x", domain.CodingNetworkOpen
	if SnapshotConfigHash(other, "img:1", "main", []string{"S1", "S2"}) != hash {
		t.Fatal("model/network must not change the hash")
	}
}

func TestNormalizeAndApplyProposal(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	script, size := "pnpm install --frozen-lockfile", domain.CodingSizeLarge
	proposal := domain.CodingEnvironmentProposal{
		SessionID: "s1",
		Mise: &domain.CodingMiseConfig{Tools: map[string]string{"node": "22"}, EnvironmentVariables: []domain.CodingEnvVar{
			{Name: "NODE_ENV", Value: "test"}, {Name: "NPM_TOKEN", Value: "leaked"}, {Name: "DATABASE_URL", Value: "postgres://u:p@h/d"},
		}},
		PrepareScript:  &script,
		SandboxSize:    &size,
		MissingSecrets: []string{"STRIPE_KEY", "STRIPE_KEY", "FLOW_X", "bad name"},
		Explanation:    "  Uses pnpm. ",
		Verified:       true,
	}
	if err := NormalizeProposal(&proposal, now); err != nil {
		t.Fatal(err)
	}
	if len(proposal.Mise.EnvironmentVariables) != 1 || proposal.Mise.EnvironmentVariables[0].Name != "NODE_ENV" {
		t.Fatalf("secret values must be stripped: %+v", proposal.Mise.EnvironmentVariables)
	}
	if strings.Join(proposal.MissingSecrets, ",") != "STRIPE_KEY,NPM_TOKEN,DATABASE_URL" {
		t.Fatalf("missing secrets: %v", proposal.MissingSecrets)
	}
	if proposal.Explanation != "Uses pnpm." || !proposal.CreatedAt.Equal(now) {
		t.Fatalf("%+v", proposal)
	}
	env := domain.CodingEnvironment{Repository: "a/b", PrepareScript: "npm ci", CustomGuidance: "keep", PendingProposal: &proposal}
	if err := ApplyProposal(&env); err != nil {
		t.Fatal(err)
	}
	if env.PrepareScript != script || env.SandboxSize != size || env.CustomGuidance != "keep" || env.Mise.Tools["node"] != "22" || env.PendingProposal != nil {
		t.Fatalf("applied: %+v", env)
	}
	if err := ApplyProposal(&env); !errors.Is(err, ErrNoProposal) {
		t.Fatalf("second apply: %v", err)
	}
	empty := domain.CodingEnvironmentProposal{}
	if err := NormalizeProposal(&empty, now); err == nil {
		t.Fatal("an empty proposal is rejected")
	}
	bad := "weird"
	if err := NormalizeProposal(&domain.CodingEnvironmentProposal{DependencyManagement: &bad}, now); err == nil {
		t.Fatal("bad dependency mode")
	}
}

func TestPrompts(t *testing.T) {
	env := domain.CodingEnvironment{CodeHost: "github", HostName: "github.com", Repository: "a/b", PrepareScript: "make deps", Secrets: []domain.CodingSecretRef{{Name: "API_KEY"}}, Mise: domain.CodingMiseConfig{Tools: map[string]string{"node": "22"}}}
	configure := ConfigurePrompt(env, "use pnpm")
	for _, want := range []string{"a/b", "propose_environment", "node@22", "make deps", "secret API_KEY", "use pnpm", "missingSecrets"} {
		if !strings.Contains(configure, want) {
			t.Errorf("configure prompt lacks %q", want)
		}
	}
	fix := FixPrompt(env, &domain.CodingEnvironmentSnapshot{ID: "snap1", ErrorCode: domain.CodingErrEnvironmentSetup, Error: "exit 1"}, "npm ERR! missing")
	for _, want := range []string{"snap1", "environmentSetupFailed", "exit 1", "npm ERR! missing", "propose_environment"} {
		if !strings.Contains(fix, want) {
			t.Errorf("fix prompt lacks %q", want)
		}
	}
}
