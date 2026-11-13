package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21MigrateResidentPolicyReaders(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.policy_fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "p21-migrate-private", Key: "Private", Name: "Private", Private: true}, domain.Team{ID: "p21-migrate-child", Key: "Child", Name: "Child"})
		data.TeamSettings["p21-migrate-private"] = domain.TeamSettings{TeamID: "p21-migrate-private", Access: "private", AgentGuidance: "private guidance"}
		data.TeamSettings["p21-migrate-child"] = domain.TeamSettings{TeamID: "p21-migrate-child", ParentTeamID: "p21-migrate-private", Access: "public", AgentGuidance: "child guidance"}
		data.Projects = append(data.Projects, domain.Project{ID: "p21-migrate-unrelated", Description: strings.Repeat("unrelated body ", 20000)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	probe.lastWorkspaceKey = workspace
	for _, key := range []string{workspace, "", "p21-migrate-missing"} {
		t.Run("workspace/"+key, func(t *testing.T) {
			before, beforeOK := f.WorkspaceSettingsMetadata(key)
			after, afterOK := probe.WorkspaceSettingsMetadata(key)
			if beforeOK != afterOK || !reflect.DeepEqual(before, after) {
				t.Fatal("workspace settings changed")
			}
			before, beforeOK = f.IssueAccessMetadata(t.Context(), key)
			after, afterOK = probe.IssueAccessMetadata(t.Context(), key)
			if beforeOK != afterOK || !reflect.DeepEqual(before, after) {
				t.Fatal("issue policy changed")
			}
			for _, team := range []string{f.data.Teams[0].ID, "p21-migrate-private", "p21-migrate-child", "p21-migrate-missing"} {
				left, leftOK := f.TeamSettingsFor(key, team)
				right, rightOK := probe.TeamSettingsFor(key, team)
				if leftOK != rightOK || !reflect.DeepEqual(left, right) {
					t.Fatalf("team settings changed for %s", team)
				}
			}
			for _, ref := range []string{"", f.data.Teams[0].ID, f.data.Teams[0].Key, "private", "PRIVATE", "child", "p21-migrate-private", "p21-migrate-missing"} {
				if f.ResolveTeamID(key, ref) != probe.ResolveTeamID(key, ref) {
					t.Fatalf("team key resolution changed for %s", ref)
				}
			}
		})
	}
	for _, key := range []string{workspace, "p21-migrate-missing"} {
		if f.HasWorkspace(key) != probe.HasWorkspace(key) {
			t.Fatal("workspace existence changed")
		}
	}
	for _, key := range []string{workspace, " " + workspace + " ", "", "p21-migrate-missing"} {
		if f.MustWorkspaceID(key) != probe.MustWorkspaceID(key) {
			t.Fatal("SCIM workspace identity changed")
		}
	}
	before, _ := probe.IssueAccessMetadata(t.Context(), workspace)
	before.Teams[0].Name = "caller mutation"
	settings, _ := probe.WorkspaceSettingsMetadata(workspace)
	settings.Workspace.Name = "caller mutation"
	after, _ := probe.IssueAccessMetadata(t.Context(), workspace)
	if after.Teams[0].Name == "caller mutation" || after.Workspace.Name == "caller mutation" {
		t.Fatal("policy readers returned mutable resident storage")
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("policy readers decoded full metadata or leaked admission")
	}
}

func TestScaleP21MigratePolicyPermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	public, private, child := "p21-migrate-public", "p21-migrate-private", "p21-migrate-child"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.policy_matrix", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: "Public"}, domain.Team{ID: private, Key: "Private", Private: true}, domain.Team{ID: child, Key: "Child"})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
		data.Projects = append(data.Projects,
			domain.Project{ID: "p21-migrate-shared", SlugID: "p21-migrate-new", PreviousSlugIDs: []string{"p21-migrate-old"}, Name: "Shared", TeamIDs: []string{public, private}, DependencyIDs: []string{"p21-migrate-hidden"}, IssueCount: 97, Creator: &f.data.Viewer},
			domain.Project{ID: "p21-migrate-hidden", Name: "Private", TeamIDs: []string{private}},
			domain.Project{ID: "p21-migrate-inherited", Name: "Child", TeamIDs: []string{child}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	identity := func(role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), "P21 policy "+role, idgen.New("scale-p21-migrate-")+"@example.test", "p21-migrate-password")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.ErasePersonFromWorkspace(context.Background(), workspace, user.ID); err != nil {
				t.Errorf("clean up owned membership: %v", err)
			}
			if err := f.EraseAccount(context.Background(), user.ID); err != nil {
				t.Errorf("clean up owned account: %v", err)
			}
		})
		if err := f.VerifyEmail(t.Context(), token); err != nil {
			t.Fatal(err)
		}
		invitation, err := f.Invite(t.Context(), workspaceID, f.data.Viewer.ID, user.Email, role, teams)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.AcceptInvitation(t.Context(), invitation.Token, user.ID); err != nil {
			t.Fatal(err)
		}
		return user
	}
	admin := identity("admin", []string{private})
	member := identity("member", []string{public})
	guest := identity("guest", []string{public})
	inherited := identity("member", []string{private})
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	var queuedLegacy, queuedResident AccessContext
	for _, principal := range []struct {
		name string
		user domain.User
	}{
		{"admin", admin}, {"admin-outside-private", f.data.Viewer},
		{"member-private-outsider", member}, {"guest-explicit-team-grant", guest}, {"sub-team-inheritance", inherited},
	} {
		for _, credential := range []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: "p21-migrate-key", Scopes: []string{"read"}, TeamRestriction: "all"}},
			{"selected-empty", &domain.APIKey{ID: "p21-migrate-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{"selected-set", &domain.APIKey{ID: "p21-migrate-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}}},
		} {
			t.Run(principal.name+"/"+credential.name, func(t *testing.T) {
				before, err := f.ResolveAccess(t.Context(), workspace, principal.user.ID, credential.key)
				if err != nil {
					t.Fatal(err)
				}
				after, err := probe.ResolveAccess(t.Context(), workspace, principal.user.ID, credential.key)
				if err != nil {
					t.Fatal(err)
				}
				if before.CredentialScope != after.CredentialScope || before.AuthzEpoch != after.AuthzEpoch || !reflect.DeepEqual(before.policy.teams, after.policy.teams) {
					t.Fatal("policy authority or credential team projection changed")
				}
				if err := f.SetLastWorkspace(t.Context(), principal.user.ID, workspace); err != nil {
					t.Fatal(err)
				}
				if err := probe.SetLastWorkspace(t.Context(), principal.user.ID, workspace); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{" " + workspace + " ", "p21-migrate-missing"} {
					if !errors.Is(f.SetLastWorkspace(t.Context(), principal.user.ID, key), ErrAuthForbidden) || !errors.Is(probe.SetLastWorkspace(t.Context(), principal.user.ID, key), ErrAuthForbidden) {
						t.Fatal("workspace selection changed missing or whitespace-key authorization")
					}
				}
				for _, key := range []string{"p21-migrate-shared", "p21-migrate-old", "p21-migrate-hidden", "p21-migrate-inherited"} {
					left, leftAllowed, leftErr := f.ResourceReadMetadata(t.Context(), before, "projects", key)
					right, rightAllowed, rightErr := probe.ResourceReadMetadata(t.Context(), after, "projects", key)
					if leftErr != nil || rightErr != nil || leftAllowed != rightAllowed || !reflect.DeepEqual(left, right) {
						t.Fatalf("detail authorization, hidden references, counts or actors changed for %s", key)
					}
				}
				for _, team := range []string{public, private, child} {
					left, leftOK := f.TeamSettingsFor(workspace, team)
					right, rightOK := probe.TeamSettingsFor(workspace, team)
					if leftOK != rightOK || !reflect.DeepEqual(left, right) || f.ResolveTeamID(workspace, strings.ToUpper(left.TeamID)) != probe.ResolveTeamID(workspace, strings.ToUpper(right.TeamID)) {
						t.Fatal("trusted policy record changed across a principal or scope")
					}
				}
				forged := after
				forged.WorkspaceID = "p21-migrate-other-workspace"
				if _, _, err := probe.ResourceReadMetadata(t.Context(), forged, "projects", "p21-migrate-shared"); !errors.Is(err, ErrAuthForbidden) {
					t.Fatalf("cross-workspace binding accepted: %v", err)
				}
				if principal.user.ID == inherited.ID && credential.key == nil {
					queuedLegacy, queuedResident = before, after
				}
			})
		}
	}
	if err := f.SetTeamMembership(t.Context(), workspaceID, private, inherited.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "replay"} {
		t.Run(phase, func(t *testing.T) {
			if !errors.Is(f.ValidateAccess(t.Context(), queuedLegacy), ErrAuthForbidden) || !errors.Is(probe.ValidateAccess(t.Context(), queuedResident), ErrAuthForbidden) {
				t.Fatal("revoked policy survived a queued or replayed batch")
			}
		})
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("policy differential decoded full metadata or leaked admission")
	}
}

func TestScaleP21MigrateInventoryCount(t *testing.T) {
	if count := len(metadataFullWorkspaceConsumers()); count != 909 {
		t.Fatalf("unmigrated consumers=%d, want 909", count)
	}
	if count := len(scaleP21MigrateReviewedConsumers()); count != 10 {
		t.Fatalf("reviewed migration consumers=%d, want 10", count)
	}
}
