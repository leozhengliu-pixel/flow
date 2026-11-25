package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW28InstalledProjectionPermissionMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	public, private, child := idgen.New("team_sw28_public_"), idgen.New("team_sw28_private_"), idgen.New("team_sw28_child_")
	emojiID := idgen.New("emoji_sw28_matrix_")
	if err := f.MutateWorkspace(t.Context(), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams,
			domain.Team{ID: public, Key: "SW28PUBLIC"},
			domain.Team{ID: private, Key: "SW28PRIVATE", Private: true},
			domain.Team{ID: child, Key: "SW28CHILD"})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
		data.Settings["applicationPolicies"] = map[string]any{"warm": "typed\x00value", "level": int64(9007199254740993)}
		data.CustomEmojis = []domain.CustomEmoji{{ID: emojiID, Name: "warm\x00emoji", ImageURL: "https://example.test/warm", Creator: data.Viewer}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	identity := func(role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), "SW28 "+role, idgen.New("sw28-matrix-")+"@example.test", "sw28-matrix-password")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := f.ErasePersonFromWorkspace(context.Background(), workspace, user.ID); err != nil {
				t.Errorf("clean up owned membership: %v", err)
			}
			if err := f.EraseAccount(context.Background(), user.ID); err != nil {
				t.Errorf("clean up owned identity: %v", err)
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
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings", "customEmojis"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["applicationPolicies"] = map[string]any{"warm": "typed\x00value", "level": int64(9007199254740993)}
		data.CustomEmojis = []domain.CustomEmoji{{ID: emojiID, Name: "warm\x00emoji", ImageURL: "https://example.test/warm", Creator: data.Viewer}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope := MetadataRecordScope{
		Read: []RecordKey{{Kind: "settings", ID: "applicationPolicies"}, {Kind: "customEmojis", ID: emojiID}},
	}
	var queuedOld, queuedNew AccessContext
	for _, principal := range []struct {
		name string
		user domain.User
	}{
		{"admin", admin},
		{"admin-private-outsider", f.data.Viewer},
		{"member-private-outsider", member},
		{"guest-explicit-team-grant", guest},
		{"sub-team-inheritance", inherited},
	} {
		for _, credential := range []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: "sw28-key", Scopes: []string{"read"}, TeamRestriction: "all"}},
			{"selected-empty", &domain.APIKey{ID: "sw28-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{"selected-set", &domain.APIKey{ID: "sw28-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}}},
		} {
			t.Run(principal.name+"/"+credential.name, func(t *testing.T) {
				legacy, ok, err := f.BootstrapForUser(t.Context(), workspace, principal.user.ID)
				if err != nil || !ok {
					t.Fatalf("old authorized projection ok=%t err=%v", ok, err)
				}
				policy, ok := legacy.Settings["applicationPolicies"].(map[string]any)
				if !ok || policy["warm"] != "typed\x00value" || policy["level"] != int64(9007199254740993) ||
					len(legacy.CustomEmojis) != 1 || legacy.CustomEmojis[0].Name != "warm\x00emoji" {
					t.Fatal("old oracle did not retain the warm typed source")
				}
				actual, err := probe.ProjectScopedMetadata(t.Context(), workspace, principal.user.ID, credential.key, scope)
				if err != nil {
					t.Fatal(err)
				}
				// Team-scoped credentials leave these workspace settings and
				// custom emoji fields unchanged in applyAPIKeyProjection.
				if !reflect.DeepEqual(actual.CustomEmojis, legacy.CustomEmojis) ||
					!reflect.DeepEqual(actual.Settings["applicationPolicies"], legacy.Settings["applicationPolicies"]) ||
					!reflect.DeepEqual(actual.Viewer, legacy.Viewer) || actual.ViewerRole != legacy.ViewerRole {
					t.Fatal("installed bounded source changed old authorized fields or actor")
				}
				if len(actual.Projects) != 0 || len(actual.Documents) != 0 || len(actual.Users) != 0 {
					t.Fatal("narrow authorized projection exposed undeclared records")
				}
				oldAccess, err := f.ResolveAccess(t.Context(), workspace, principal.user.ID, credential.key)
				if err != nil {
					t.Fatal(err)
				}
				newAccess, err := probe.ResolveAccess(t.Context(), workspace, principal.user.ID, credential.key)
				if err != nil {
					t.Fatal(err)
				}
				if oldAccess.AuthzEpoch != newAccess.AuthzEpoch || oldAccess.CredentialScope != newAccess.CredentialScope {
					t.Fatal("source changed live authority or credential binding")
				}
				if principal.name == "sub-team-inheritance" && credential.key == nil {
					queuedOld, queuedNew = oldAccess, newAccess
				}
			})
		}
	}
	foreign, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW28 foreign", idgen.New("sw28-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey); err != nil {
			t.Errorf("clean up owned foreign workspace: %v", err)
		}
	})
	if _, err := probe.ProjectScopedMetadata(t.Context(), foreign.Workspace.URLKey, member.ID, nil, scope); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("cross-workspace principal accepted: %v", err)
	}
	if err := f.SetTeamMembership(t.Context(), workspaceID, private, inherited.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"queued", "replay"} {
		t.Run(phase, func(t *testing.T) {
			if !errors.Is(f.ValidateAccess(t.Context(), queuedOld), ErrAuthForbidden) ||
				!errors.Is(probe.ValidateAccess(t.Context(), queuedNew), ErrAuthForbidden) {
				t.Fatal("old authorization survived revocation while work was retained")
			}
		})
	}
	if err := f.SuspendMember(t.Context(), workspaceID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.BootstrapForUser(t.Context(), workspace, member.ID); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("old projection accepted suspended member: %v", err)
	}
	if _, err := probe.ProjectScopedMetadata(t.Context(), workspace, member.ID, nil, scope); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("bounded projection accepted suspended member: %v", err)
	}
	if probe.WorkspaceMetadataDecodes() != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatal("permission projection decoded full workspace or leaked work")
	}
}
