package store

import (
	"flow/api/internal/domain"
	"path/filepath"
	"testing"
)

func TestOAuthClientReuseDoesNotAdoptUnrelatedRedirects(t *testing.T) {
	repository, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "oauth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	client := domain.OAuthClient{ClientID: "identity-test", ClientName: "Codex identity verification", ClientURI: "https://openai.example/codex", LogoURI: "https://openai.example/logo.png", TokenEndpointAuthMethod: "none", RedirectURIs: []string{"http://127.0.0.1:43119/callback/fixed"}, GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}}
	if err := repository.RegisterOAuthClient(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, redirect string
		want           bool
	}{
		{"new listener", "http://127.0.0.1:50123/callback/fixed", true},
		{"external host", "https://unrelated.example/callback/fixed", false},
		{"different path", "http://127.0.0.1:50123/callback/other", false},
		{"different query", "http://127.0.0.1:50123/callback/fixed?next=other", false},
		{"different loopback host", "http://localhost:50123/callback/fixed", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := client
			candidate.RedirectURIs = []string{test.redirect}
			got, found, err := repository.FindOAuthClientByMetadata(t.Context(), candidate)
			if err != nil || found != test.want || found && got.ClientID != client.ClientID {
				t.Fatalf("found=%v id=%q error=%v", found, got.ClientID, err)
			}
		})
	}
	for _, test := range []struct {
		name     string
		mutate   func(*domain.OAuthClient)
		wantFind bool
	}{
		{"omitted marketing metadata", func(candidate *domain.OAuthClient) {
			candidate.ClientURI = ""
			candidate.LogoURI = ""
		}, true},
		{"grant types omitted by MCP clients", func(candidate *domain.OAuthClient) {
			candidate.GrantTypes = []string{"authorization_code"}
			candidate.ResponseTypes = nil
		}, true},
		{"different client name", func(candidate *domain.OAuthClient) {
			candidate.ClientName = "grok-cli"
		}, false},
		{"synthetic default name", func(candidate *domain.OAuthClient) {
			candidate.ClientName = "MCP client"
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := client
			test.mutate(&candidate)
			got, found, err := repository.FindOAuthClientByMetadata(t.Context(), candidate)
			if err != nil || found != test.wantFind || found && got.ClientID != client.ClientID {
				t.Fatalf("found=%v id=%q error=%v", found, got.ClientID, err)
			}
		})
	}
	unnamed := domain.OAuthClient{ClientID: "default-name", ClientName: "MCP client", TokenEndpointAuthMethod: "none", RedirectURIs: []string{"http://127.0.0.1:43119/callback/fixed"}}
	if err := repository.RegisterOAuthClient(t.Context(), unnamed); err != nil {
		t.Fatal(err)
	}
	got, found, err := repository.FindOAuthClientByMetadata(t.Context(), unnamed)
	if err != nil || found {
		t.Fatalf("default MCP client name was reused: found=%v id=%q error=%v", found, got.ClientID, err)
	}
}

func TestOAuthRedirectURIAllowedIgnoresLoopbackPort(t *testing.T) {
	registered := []string{"http://127.0.0.1:43119/callback/fixed"}
	if !OAuthRedirectURIAllowed(registered, "http://127.0.0.1:50123/callback/fixed") {
		t.Fatal("ephemeral loopback port was rejected")
	}
	if OAuthRedirectURIAllowed(registered, "http://127.0.0.1:50123/callback/other") {
		t.Fatal("different path was accepted")
	}
	if OAuthRedirectURIAllowed(registered, "http://localhost:50123/callback/fixed") {
		t.Fatal("different loopback host was accepted")
	}
	if OAuthRedirectURIAllowed(registered, "https://unrelated.example/callback/fixed") {
		t.Fatal("external redirect was accepted")
	}
}
