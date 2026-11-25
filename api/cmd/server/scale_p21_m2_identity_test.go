package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21M2IdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.auth_policy", "", nil, func(data *domain.Bootstrap) error {
			data.IdentityProviders = []domain.IdentityProvider{{ID: "m2-idp", Enabled: true, Enforced: true, Issuer: "https://idp.example.test"}}
			data.WorkspaceSettings.SCIMDefaultRole = "guest"
			data.WorkspaceSettings.SCIMRoleMapping = map[string]string{"developer": "member"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		services := []*server{{store: repo}, {store: f.probe}}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, provider := range []string{"password", "enterprise:m2-idp", "enterprise:missing"} {
				var outcomes []any
				for _, s := range services {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r = r.WithContext(withChatIdentityAuthentication(r.Context(), domain.ChatIdentity{UserID: user.ID, AuthProvider: provider}))
					data, _ := repo.WorkspaceSettingsMetadata(workspace)
					role, _, err := repo.WorkspaceRole(t.Context(), data.Workspace.ID, user.ID)
					if err != nil {
						t.Fatal(err)
					}
					allowed := s.authorizeAuthenticationPolicy(w, r, data, role)
					binding := domain.ChatBinding{TeamID: f.public, Authorization: &domain.ChatBindingAuthorization{UserID: user.ID, PrivateTeam: true}}
					outcomes = append(outcomes, []any{allowed, w.Code, scaleP21M2Response(t, w, false), s.codingMemberRoleOf(workspace, user.ID), s.chatBindingAuthorizerValid(t.Context(), workspace, binding), s.scimRole(workspace, "developer"), s.scimRole(workspace, "invalid"), scaleP21M2JSON(t, s.scimWorkspaceSettings(workspace), false)})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("identity differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		for _, userID := range []string{f.principals[2].user.ID, "m2-missing-user"} {
			if userID != "m2-missing-user" {
				if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, userID); err != nil {
					t.Fatal(err)
				}
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if services[0].codingMemberRoleOf(workspace, userID) != services[1].codingMemberRoleOf(workspace, userID) {
				t.Fatal("coding role changed for suspended/missing principal")
			}
		}
		var audits []any
		for _, s := range services {
			ctx := store.ContextWithActor(context.Background(), f.data.Viewer)
			s.auditChatIntegrationContext(ctx, workspace, "m2-chat", "chat_integration", "m2-chat", map[string]any{"binding": f.public})
			entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: "m2-chat", Limit: 1})
			if err != nil || len(entries) != 1 {
				t.Fatal("chat audit missing", err)
			}
			entry := entries[0]
			entry.ID, entry.CreatedAt, entry.Seq, entry.Hash = "", time.Time{}, 0, ""
			audits = append(audits, scaleP21M2JSON(t, entry, false))
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(audits[0], audits[1]) {
			t.Fatalf("chat audit changed: %v", audits)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("identity operations decoded full metadata")
		}
	}
}

func TestScaleP21M2CodingBootstrapIsRuntimeProtocol(t *testing.T) {
	w := httptest.NewRecorder()
	(&server{}).codingRuntimeBootstrap(w, httptest.NewRequest(http.MethodGet, "/internal/coding-runtime/bootstrap", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("disabled coding runtime: %d %s", w.Code, w.Body.String())
	}
}
