package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW17WorkspaceByIDActualInstalledSource(t *testing.T) {
	large := strings.Repeat("unrelated full body ", 200000)
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			source := domain.Bootstrap{
				Workspace: domain.Workspace{ID: "raw\x00identity", URLKey: "logical-old-slug", Name: "Installed source"},
				Teams:     []domain.Team{{ID: "private-team", Private: true}},
				TeamSettings: map[string]domain.TeamSettings{
					"private-team": {TeamID: "private-team", ParentTeamID: "parent\x00team"},
				},
				WorkspaceSettings: domain.WorkspaceSettings{SCIMEnabled: true},
				Issues:            []domain.Issue{{ID: "counted-1"}, {ID: "counted-2"}},
				Projects:          []domain.Project{{ID: "unrelated", Description: large}},
			}
			catalog := map[string]domain.Bootstrap{
				"physical\x00registry": source,
				"empty-identity":       {Workspace: domain.Workspace{ID: "", URLKey: "empty-logical"}},
			}
			old := &SQLiteStore{dialect: dialect, workspaces: catalog, lastWorkspaceKey: "physical\x00registry"}
			bounded := &SQLiteStore{dialect: dialect, workspaces: catalog, lastWorkspaceKey: old.lastWorkspaceKey, metadataResidency: newMetadataResidency()}
			for _, id := range []string{"raw\x00identity", "", "logical-old-slug", "physical\x00registry", "rawidentity", "RAW\x00identity", "missing"} {
				want, wantKey, wantFound := old.workspaceByID(id)
				got, key, found := bounded.workspaceByID(id)
				if found != wantFound || key != wantKey || !reflect.DeepEqual(got, want) {
					t.Fatalf("exact installed by-ID lookup changed: id=%q found=%v/%v key=%q/%q", id, found, wantFound, key, wantKey)
				}
				if id == source.Workspace.ID {
					if key != "physical\x00registry" || got.Workspace.URLKey != "logical-old-slug" || len(got.Issues) != 2 ||
						got.TeamSettings["private-team"].ParentTeamID != "parent\x00team" || !got.WorkspaceSettings.SCIMEnabled {
						t.Fatal("registry identity, raw policy or invitation IssueCount input was projected away")
					}
					if &got.Teams[0] != &source.Teams[0] || &got.Issues[0] != &source.Issues[0] || &got.Projects[0] != &source.Projects[0] {
						t.Fatal("lookup stopped returning the original borrowed immutable snapshot")
					}
				}
			}
			for _, repo := range []*SQLiteStore{old, bounded} {
				allocations := testing.AllocsPerRun(100, func() {
					data, key, found := repo.workspaceByID(source.Workspace.ID)
					if !found || key != "physical\x00registry" || len(data.Issues) != 2 || data.Projects[0].Description != large {
						panic("borrowed registry result changed")
					}
				})
				if allocations != 0 {
					t.Fatalf("registry lookup allocated for unrelated metadata: bounded=%v allocations=%v", repo.BoundedMetadataEnabled(), allocations)
				}
				t.Logf("dialect=%s bounded=%v allocations/op=%v unrelated-body-bytes=%d issue-count=%d", dialect, repo.BoundedMetadataEnabled(), allocations, len(large), len(source.Issues))
			}
			// Duplicate IDs already have unordered map traversal semantics.
			// Preserve a matching installed entry rather than inventing
			// URL-key precedence or SQL collation behavior.
			catalog["other-physical"] = domain.Bootstrap{Workspace: domain.Workspace{ID: source.Workspace.ID, URLKey: "other-logical"}}
			for range 50 {
				got, key, found := bounded.workspaceByID(source.Workspace.ID)
				if !found || got.Workspace.ID != source.Workspace.ID ||
					key != "physical\x00registry" && key != "other-physical" ||
					got.Workspace.URLKey != catalog[key].Workspace.URLKey {
					t.Fatal("duplicate ID lookup no longer returns an actual matching installed entry")
				}
			}
			if bounded.WorkspaceMetadataDecodes() != 0 || bounded.metadataResidency.work.used != 0 {
				t.Fatal("borrowed registry lookup hydrated metadata or allocated admission")
			}
		})
	}
}

func TestScaleP21SW17WorkspaceByIDAuthorizationCallers(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	prefix := idgen.New("sw17-auth-registry-")
	parent, child, public := prefix+"-private", prefix+"-child", prefix+"-public"
	if err := f.MutateWorkspace(t.Context(), workspace, "test.sw17.auth_registry", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: parent, Private: true}, domain.Team{ID: child, Private: true}, domain.Team{ID: public})
		data.TeamSettings[parent] = domain.TeamSettings{TeamID: parent, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: parent, Access: "private"}
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installed, ok := f.storedWorkspace(workspace)
	if !ok {
		t.Fatal("installed source missing")
	}
	// Deliberately share this actual installed logical source. A canonical
	// reload would test the separately pending policy producer instead.
	probe := &SQLiteStore{db: f.db, dialect: f.dialect, workspaces: map[string]domain.Bootstrap{workspace: installed}, metadataResidency: newMetadataResidency()}
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for _, principal := range []struct {
		name, role, teamRole, team string
	}{
		{"admin-outside-private", "admin", "", ""},
		{"member-private-outsider", "member", "member", public},
		{"guest-explicit-member", "guest", "member", child},
		{"sub-team-owner", "member", "owner", parent},
	} {
		t.Run(principal.name, func(t *testing.T) {
			identity, _, err := f.Register(t.Context(), "SW17 "+principal.name, idgen.New("sw17-auth-principal-")+"@example.test", "sw17-registry-password")
			if err != nil {
				t.Fatal(err)
			}
			user := identity.ID
			if _, err := f.db.ExecContext(t.Context(), `INSERT INTO workspace_memberships(workspace_id,user_id,role,status,joined_at,last_seen_at) VALUES(?,?,?,'active',?,?)`, workspaceID, user, principal.role, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.db.ExecContext(context.Background(), `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, user); err != nil {
					t.Error(err)
				}
				if _, err := f.db.ExecContext(context.Background(), `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, user); err != nil {
					t.Error(err)
				}
				if err := f.EraseAccount(context.Background(), user); err != nil {
					t.Error(err)
				}
			})
			if principal.team != "" {
				if _, err := f.db.ExecContext(t.Context(), `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?)`, workspaceID, principal.team, user, principal.teamRole, stamp); err != nil {
					t.Fatal(err)
				}
			}
			for _, team := range []string{parent, child, public, "missing-team"} {
				want, oldErr := f.TeamRole(t.Context(), workspaceID, team, user)
				got, newErr := probe.TeamRole(t.Context(), workspaceID, team, user)
				if got != want || !errors.Is(newErr, oldErr) || (oldErr == nil) != (newErr == nil) {
					t.Fatal("live TeamRole changed", team, got, want, newErr, oldErr)
				}
			}
			if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, workspaceID, user); err != nil {
				t.Fatal(err)
			}
			want, oldErr := f.TeamRole(t.Context(), workspaceID, child, user)
			got, newErr := probe.TeamRole(t.Context(), workspaceID, child, user)
			if got != want || fmt.Sprint(newErr) != fmt.Sprint(oldErr) {
				t.Fatal("live revocation changed caller semantics", got, want, newErr, oldErr)
			}
			if _, oldErr := f.TeamRole(t.Context(), workspaceID+"-other", child, user); !errors.Is(oldErr, ErrAuthForbidden) {
				t.Fatal("cross-workspace oracle was not forbidden", oldErr)
			}
			if _, err := probe.TeamRole(t.Context(), workspaceID+"-other", child, user); !errors.Is(err, ErrAuthForbidden) {
				t.Fatal("cross-workspace bounded caller was not forbidden", err)
			}
		})
	}
	invitation, err := f.Invite(t.Context(), workspaceID, f.data.Viewer.ID, idgen.New("sw17-preview-")+"@example.test", "guest", []string{child})
	if err != nil {
		t.Fatal(err)
	}
	wantInvite, wantWorkspace, oldErr := f.InvitationPreview(t.Context(), invitation.Token)
	gotInvite, gotWorkspace, newErr := probe.InvitationPreview(t.Context(), invitation.Token)
	if oldErr != nil || newErr != nil || !reflect.DeepEqual(wantInvite, gotInvite) || !reflect.DeepEqual(wantWorkspace, gotWorkspace) {
		t.Fatal("actual invitation preview changed installed workspace identity", oldErr, newErr)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("policy caller hydrated full metadata or leaked work")
	}
}
