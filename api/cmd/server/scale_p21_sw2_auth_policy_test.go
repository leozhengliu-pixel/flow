package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW2AuthenticationPolicyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw2.auth", "", nil, func(data *domain.Bootstrap) error {
			data.IdentityProviders = make([]domain.IdentityProvider, 75)
			for i := range data.IdentityProviders {
				data.IdentityProviders[i] = domain.IdentityProvider{ID: fmt.Sprintf("sw2-provider-%d", i), Enabled: true, Enforced: true, Issuer: fmt.Sprintf("https://idp.example.test/%d///", i)}
			}
			data.IdentityProviders[0].Name = strings.Repeat("large ignored provider name ", 15000)
			data.IdentityProviders[1].Enabled = false
			data.IdentityProviders[2].Enforced = false
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, settings := range []domain.WorkspaceSettings{
				{EmailAuthEnabled: true, GoogleAuthEnabled: true},
				{DisableAdminBypass: true, EmailAuthEnabled: true, GoogleAuthEnabled: true},
				{DisableAdminBypass: true, RequireTwoFactor: true, EmailAuthEnabled: true, GoogleAuthEnabled: true},
				{DisableAdminBypass: true},
			} {
				for _, auth := range []domain.ChatIdentity{
					{AuthProvider: "password"},
					{AuthProvider: "legacy"},
					{AuthProvider: "google"},
					{AuthProvider: "enterprise:sw2-provider-74"},
					{AuthProvider: "enterprise:SW2-provider-74"},
					{AuthProvider: "enterprise:sw2-provider-1"},
					{AuthProvider: "enterprise:sw2-provider-2"},
					{AuthProvider: "oidc", AuthIssuer: "https://idp.example.test/74"},
					{AuthProvider: "saml", AuthIssuer: "https://idp.example.test/74////"},
					{AuthProvider: "oidc", AuthIssuer: "https://IDP.example.test/74"},
				} {
					var outcomes []any
					for _, s := range []*server{{store: repo}, {store: f.probe}} {
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						auth.UserID = user.ID
						r = r.WithContext(withChatIdentityAuthentication(r.Context(), auth))
						role, _, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, user.ID)
						if err != nil {
							t.Fatal(err)
						}
						allowed := s.authorizeAuthenticationPolicy(w, r, domain.Bootstrap{WorkspaceSettings: settings}, role)
						outcomes = append(outcomes, []any{allowed, w.Code, scaleP21M2Response(t, w, false)})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("auth %s/%s settings=%+v: %s", auth.AuthProvider, auth.AuthIssuer, settings, scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				}
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("authentication policy decoded full workspace metadata")
		}
		for _, s := range []*server{{store: repo}, {store: f.probe}} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Header.Set("X-Workspace-Key", workspace+"-missing")
			r = r.WithContext(withChatIdentityAuthentication(r.Context(), domain.ChatIdentity{UserID: f.data.Viewer.ID, AuthProvider: "password"}))
			if s.authorizeAuthenticationPolicy(w, r, domain.Bootstrap{}, "member") || w.Code != http.StatusForbidden {
				t.Fatalf("missing workspace policy: %d %s", w.Code, w.Body.String())
			}
		}
	}
}
