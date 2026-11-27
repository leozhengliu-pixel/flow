package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestBoundedSQLiteIssueAuthorizationPolicyRace(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	repo, err := store.OpenSQLite(filepath.Join(t.TempDir(), "issues.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := resourceAccessScaleFixture(t, repo)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	team := f.data.Teams[0].ID
	_, member := resourceAccessScaleJoin(t, f, "Issue writer", "issue-policy-race@example.test", "member", team)
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]string{"title": "Policy race", "teamId": team}, workspace, http.StatusCreated)
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "team.updated", team, nil, func(data *domain.Bootstrap) error {
		for i := range data.Teams {
			if data.Teams[i].ID == team {
				data.Teams[i].Private = true
			}
		}
		settings := data.TeamSettings[team]
		settings.Access = "private"
		data.TeamSettings[team] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	service := &server{store: view}
	for _, test := range []struct {
		name, method, tail, body string
		bearer, revoke           bool
		want                     int
	}{
		{name: "comment", method: http.MethodPost, tail: "/comments", body: `{"body":"comment"}`, want: http.StatusOK},
		{name: "update", method: http.MethodPatch, body: `{"title":"updated"}`, want: http.StatusOK},
		{name: "bearer", method: http.MethodPatch, body: `{"title":"updated"}`, bearer: true, want: http.StatusServiceUnavailable},
		{name: "revoked", method: http.MethodPost, tail: "/comments", body: `{"body":"comment"}`, revoke: true, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			policyChecks := 0
			ctx := store.WithSQLStatementObserver(t.Context(), func(query string) {
				if !strings.HasPrefix(query, "SELECT COALESCE((SELECT epoch FROM workspace_authz_epochs ") {
					return
				}
				policyChecks++
				if policyChecks != 1 {
					return
				}
				// Commit after the authorization snapshot releases its SQL
				// connection and before the live epoch check.
				if test.revoke {
					if err := repo.SetTeamMembership(t.Context(), workspaceID, team, member.ID, "member", false); err != nil {
						t.Fatal(err)
					}
					return
				}
				id := "policy-race-" + test.name
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "documents"), workspace, "document.created", id, nil, func(data *domain.Bootstrap) error {
					data.Documents = append(data.Documents, domain.Document{ID: id, Title: id})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			})
			ctx = context.WithValue(ctx, authUserContextKey{}, member)
			if test.bearer {
				ctx = context.WithValue(ctx, apiKeyContextKey{}, domain.APIKey{ID: "issue-policy-race"})
			}
			r := httptest.NewRequestWithContext(ctx, test.method, "/api/issue-records/"+issue.ID+test.tail, strings.NewReader(test.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Workspace-Key", workspace)
			r, closeReads := beginRequestResourceReads(r)
			defer closeReads()
			w := httptest.NewRecorder()
			if service.authorizeResourceRequest(w, r, workspace, member.ID) {
				w.WriteHeader(http.StatusOK)
			}
			wantChecks := 2
			if test.bearer {
				wantChecks = 1
			}
			if policyChecks != wantChecks || w.Code != test.want {
				t.Fatalf("authorization checked %d times and returned %d, want %d checks and status %d", policyChecks, w.Code, wantChecks, test.want)
			}
			if test.bearer && w.Header().Get("Retry-After") == "" {
				t.Fatal("stale bearer authorization omitted Retry-After")
			}
			cached, ok := r.Context().Value(issueRequestAccessKey{}).(issueRequestAccess)
			wantTeamAccess := !test.revoke
			if !ok || slices.Contains(cached.access.VisibleTeamIDs, team) != wantTeamAccess {
				t.Fatal("request query retained the wrong team access")
			}
			closeReads()
			if got := view.MetadataWorkUsedForTesting(); got != 0 {
				t.Fatalf("authorization retained %d bytes after close", got)
			}
		})
	}
}
