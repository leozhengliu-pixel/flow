package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21M3IdentityProviderDifferential(t *testing.T) {
	seed := func(data *domain.Bootstrap, _ domain.User) {
		data.IdentityProviders = []domain.IdentityProvider{
			{ID: "m3-provider", Type: "oidc", Name: "Before", Issuer: "https://example.test", ClientID: "m3-client", Scopes: []string{"openid"}, Domains: []string{"example.test"}, DefaultRole: "member", RoleMapping: map[string]string{}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: "m3-other-provider", Type: "oidc", Name: "Retained", Issuer: "https://other.example.test", ClientID: strings.Repeat("unrelated ", 40000)},
		}
	}
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "identity-provider-update", kind: "identityProviders", id: "m3-provider", body: `{"name":"After","roleMapping":{"ADMIN":"member"},"defaultRole":"guest"}`,
			seed: seed, handler: (*server).updateIdentityProvider,
			otherKeys: []store.RecordKey{{Kind: "identityProviders", ID: "m3-other-provider"}},
		},
		{
			name: "identity-provider-invalid", kind: "identityProviders", id: "m3-provider", body: `{"defaultRole":"invalid"}`,
			seed: seed, handler: (*server).updateIdentityProvider,
			otherKeys: []store.RecordKey{{Kind: "identityProviders", ID: "m3-other-provider"}},
		},
		{
			name: "identity-provider-delete", kind: "identityProviders", id: "m3-provider", body: `{}`,
			seed: seed,
			handler: func(s *server, w http.ResponseWriter, r *http.Request) {
				s.deleteIdentityProvider(w, r)
			},
			otherKeys: []store.RecordKey{{Kind: "identityProviders", ID: "m3-other-provider"}},
		},
	})
}
