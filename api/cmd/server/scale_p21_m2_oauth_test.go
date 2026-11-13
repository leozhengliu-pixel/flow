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

func TestScaleP21M2OAuthDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		app, err := repo.InstallApplication(store.ContextWithActor(t.Context(), f.data.Viewer), workspace, domain.ApplicationInstallation{ClientID: "m2-consent-client", Name: "M2 consent", Active: true, AllPublicTeams: true, InstalledBy: f.data.Viewer.ID, Scopes: []string{"read"}}, "")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.appUsers = append(f.resources.appUsers, struct{ workspace, id string }{workspace, app.UserID})
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, target := range []string{"m2-authorization", "m2-missing"} {
				var outcomes, effects, audits []any
				for _, s := range []*server{{store: repo}, {store: f.probe}} {
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset_authorizations", "", nil, func(data *domain.Bootstrap) error {
						data.OAuthAuthorizations = []domain.OAuthAuthorization{{ID: "m2-authorization", ClientID: "m2-client", ClientName: "M2", UserID: f.data.Viewer.ID, Scopes: []string{"read"}}, {ID: "m2-other", ClientID: "other-client", UserID: f.data.Viewer.ID}}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.SetPathValue("id", target)
					// This is the handler boundary fixture, as in the shared P21
					// template. Session-policy middleware remains a separate blocker.
					role, _, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, user.ID)
					if err != nil {
						t.Fatal(err)
					}
					access := f.data
					access.Viewer, access.ViewerRole = user, role
					r = r.WithContext(context.WithValue(r.Context(), oauthWorkspaceAccessKey{}, oauthWorkspaceAccess{workspace: workspace, userID: user.ID, data: access}))
					s.revokeOAuthAuthorization(w, r)
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					data, _ := repo.WorkspaceMetadata(workspace)
					for i := range data.OAuthAuthorizations {
						if data.OAuthAuthorizations[i].RevokedAt != nil {
							stamp := time.Unix(1, 0).UTC()
							data.OAuthAuthorizations[i].RevokedAt = &stamp
						}
					}
					effects = append(effects, scaleP21M2JSON(t, data.OAuthAuthorizations, false))
					if w.Code == http.StatusNoContent {
						entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: target, Limit: 1})
						if err != nil || len(entries) != 1 {
							t.Fatal("revocation audit missing", err)
						}
						entry := entries[0]
						entry.ID, entry.CreatedAt, entry.Seq, entry.Hash = "", time.Time{}, 0, ""
						audits = append(audits, scaleP21M2JSON(t, entry, false))
					}
					r, w = f.request(user, key, nil), httptest.NewRecorder()
					r.URL.RawQuery = "workspace=" + workspace + "&client_id=" + app.ClientID
					r = r.WithContext(context.WithValue(r.Context(), oauthWorkspaceAccessKey{}, oauthWorkspaceAccess{workspace: workspace, userID: user.ID, data: access}))
					s.oauthConsentInstallation(w, r)
					outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
				}
				if !reflect.DeepEqual(outcomes[0:2], outcomes[2:4]) || !reflect.DeepEqual(effects[0], effects[1]) || len(audits) == 2 && !reflect.DeepEqual(audits[0], audits[1]) {
					t.Fatalf("OAuth differential: responses=%v effects=%v audits=%v", outcomes, effects, audits)
				}
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("OAuth boundaries decoded full metadata")
		}
	}
}
