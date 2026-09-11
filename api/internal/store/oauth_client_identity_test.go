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
	client := domain.OAuthClient{ClientID: "identity-test", ClientName: "Codex identity verification", TokenEndpointAuthMethod: "none", RedirectURIs: []string{"http://127.0.0.1:43119/callback/fixed"}, GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"}}
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
	candidate := client
	candidate.GrantTypes = []string{"authorization_code"}
	if _, found, err := repository.FindOAuthClientByMetadata(t.Context(), candidate); err != nil || found {
		t.Fatalf("grant metadata mismatch reused client: %v %v", found, err)
	}
}
