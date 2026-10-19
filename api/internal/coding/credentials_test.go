package coding

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
	"flow/api/internal/sandbox"
)

// grantStore authenticates every token as one fixed grant.
type grantStore struct {
	CoreStore
	grant SandboxGrant
}

func (g grantStore) AuthenticateSandboxToken(context.Context, string) (SandboxGrant, error) {
	return g.grant, nil
}

func (g grantStore) CodingSession(_ context.Context, workspaceKey, id string) (domain.CodingSession, error) {
	return domain.CodingSession{ID: id, WorkspaceKey: workspaceKey, SandboxID: g.grant.SandboxID, Phase: domain.CodingPhaseRunning}, nil
}

type credentialEnvs struct {
	envs map[string]domain.CodingEnvironment
}

func (c credentialEnvs) CodingEnvironment(_ context.Context, _ string, id string) (domain.CodingEnvironment, error) {
	if env, ok := c.envs[id]; ok {
		return env, nil
	}
	return domain.CodingEnvironment{}, ErrNotFound
}
func (c credentialEnvs) ListCodingEnvironments(context.Context, string, bool) ([]domain.CodingEnvironment, error) {
	return nil, nil
}
func (c credentialEnvs) CodingEnvironmentSecretValues(context.Context, string, string) ([]domain.CodingEnvVar, error) {
	return nil, nil
}

type mintingHost struct {
	codehost.Stub
	requests []codehost.CredentialRequest
}

func (m *mintingHost) MintCredential(_ context.Context, request codehost.CredentialRequest) (codehost.Credential, error) {
	m.requests = append(m.requests, request)
	return codehost.Credential{Username: "x-access-token", Password: "ghs_minted", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

// A snapshot build clones the environment's repository before any session
// exists: its sandbox gets a read credential for exactly that repository.
func TestCredentialsForSnapshotBuild(t *testing.T) {
	envs := credentialEnvs{envs: map[string]domain.CodingEnvironment{
		"env1": {ID: "env1", CodeHost: domain.CodeHostGitHub, HostName: "github.com", Repository: "acme/app"},
	}}
	tests := []struct {
		name    string
		grant   SandboxGrant
		request CredentialRequest
		wantErr error
	}{
		{"snapshot clone", SandboxGrant{WorkspaceKey: "w", SandboxID: "sb1", Kind: sandbox.KindSnapshot, SnapshotID: "snap1", EnvironmentID: "env1"}, CredentialRequest{Protocol: "https", Host: "github.com", Path: "acme/app.git", Purpose: "git"}, nil},
		{"snapshot other repository", SandboxGrant{WorkspaceKey: "w", SandboxID: "sb1", Kind: sandbox.KindSnapshot, EnvironmentID: "env1"}, CredentialRequest{Host: "github.com", Path: "acme/other.git"}, ErrForbidden},
		{"snapshot without environment", SandboxGrant{WorkspaceKey: "w", SandboxID: "sb1", Kind: sandbox.KindSnapshot}, CredentialRequest{Host: "github.com", Path: "acme/app.git"}, ErrForbidden},
		{"debug sandbox", SandboxGrant{WorkspaceKey: "w", SandboxID: "sb1", Kind: sandbox.KindDebug, EnvironmentID: "env1"}, CredentialRequest{Host: "github.com", Path: "acme/app.git"}, ErrForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := &mintingHost{Stub: codehost.Stub{HostKind: domain.CodeHostGitHub}}
			service := &Service{store: grantStore{grant: test.grant}, envs: envs, hosts: map[string]codehost.Host{domain.CodeHostGitHub: host}, now: time.Now}
			response, err := service.Credentials(context.Background(), SandboxTokenPrefix+"x", test.request)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) || len(host.requests) != 0 {
					t.Fatalf("err = %v, minted %d; want %v and nothing minted", err, len(host.requests), test.wantErr)
				}
				return
			}
			if err != nil || response.Password != "ghs_minted" {
				t.Fatalf("credentials: %+v %v", response, err)
			}
			if len(host.requests) != 1 || host.requests[0].Repo.FullName != "acme/app" || host.requests[0].Access != codehost.AccessRead || host.requests[0].SessionID != "" {
				t.Fatalf("mint request: %+v", host.requests)
			}
		})
	}
}

// The harness state key must work with every object store driver, including
// the default local one, which refuses keys with directories.
func TestHarnessStateKeyIsStorable(t *testing.T) {
	service := &Service{store: grantStore{grant: SandboxGrant{WorkspaceKey: "acme", SandboxID: "sb1", SessionID: "s1", Kind: sandbox.KindSession}}, now: time.Now}
	key, err := service.HarnessStateKey(context.Background(), SandboxTokenPrefix+"x")
	if err != nil || key != "coding-harness_acme_s1.tar.zst" {
		t.Fatalf("key %q %v", key, err)
	}
	store, err := objectstore.Open(context.Background(), objectstore.Config{Driver: "local", LocalPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(context.Background(), key, bytes.NewReader([]byte("state")), "application/zstd"); err != nil {
		t.Fatalf("local object store: %v", err)
	}
}
