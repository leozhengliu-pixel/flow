package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Compare installed legacy snapshots with canonical reads after policy-only
// recovery. This certifies the native project/initiative read boundary only;
// it does not bypass the application's incomplete activation coverage.
func TestScaleP21ResidentPolicyPermissionDifferential(t *testing.T) {
	for _, journal := range []string{"off", "on"} {
		t.Run(journal, func(t *testing.T) {
			t.Setenv("FLOW_BOUNDED_METADATA", "off")
			t.Setenv("FLOW_RECORD_JOURNAL", journal)
			f := scaleP09Database(t)
			workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
			public, private, child := "p21-public", "p21-private", "p21-child"
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.seed", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
				data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
				data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
				data.TeamSettings[child] = domain.TeamSettings{TeamID: child, Access: "public", ParentTeamID: private}
				data.Projects = append(data.Projects,
					domain.Project{ID: "p21-public-project", SlugID: "p21-current", PreviousSlugIDs: []string{"p21-old-slug"}, Name: "Shared", Description: "Shared body", TeamIDs: []string{public, private}, DependencyIDs: []string{"p21-private-project"}, Initiatives: []string{"p21-initiative"}, Creator: &f.data.Viewer, Comments: []domain.Comment{{ID: "p21-comment", Body: "Discussion", User: f.data.Viewer}}, IssueCount: 99},
					domain.Project{ID: "p21-private-project", Name: "Private", Description: "Private body", TeamIDs: []string{private}},
					domain.Project{ID: "p21-child-project", Name: "Inherited", TeamIDs: []string{child}})
				data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "p21-initiative", Name: "Shared initiative", ProjectIDs: []string{"p21-public-project", "p21-private-project"}, Creator: f.data.Viewer})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			identity := func(role string, teams []string) domain.User {
				t.Helper()
				user, token, err := f.Register(t.Context(), "P21 "+role, idgen.New("scale-p21-")+"@example.test", "p21-permission-password")
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
				invite, err := f.Invite(t.Context(), workspaceID, f.data.Viewer.ID, user.Email, role, teams)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.AcceptInvitation(t.Context(), invite.Token, user.ID); err != nil {
					t.Fatal(err)
				}
				return user
			}
			member := identity("member", []string{public})
			guest := identity("guest", []string{public})
			inherited := identity("member", []string{private})
			admin := identity("admin", []string{private})
			other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "P21 other owned workspace", idgen.New("scale-p21-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
					t.Errorf("clean up other owned workspace: %v", err)
				}
			})
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			probe := scaleP21ResidentProbe(f.SQLiteStore)
			if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
				t.Fatal(err)
			}
			for _, repo := range []*SQLiteStore{f.SQLiteStore, probe} {
				if _, err := repo.ResolveAccess(t.Context(), other.Workspace.URLKey, member.ID, nil); !errors.Is(err, ErrAuthForbidden) {
					t.Fatalf("cross-workspace principal was accepted: %v", err)
				}
			}
			for _, principal := range []struct {
				name string
				user domain.User
			}{
				{"admin", admin}, {"admin-outside-private", f.data.Viewer},
				{"member-private-outsider", member}, {"guest-explicit-grant", guest},
				{"sub-team-inheritance", inherited},
			} {
				t.Run(principal.name, func(t *testing.T) {
					for _, credential := range []struct {
						name string
						key  *domain.APIKey
					}{
						{"session", nil},
						{"unrestricted", &domain.APIKey{ID: "p21-key", Scopes: []string{"read"}, TeamRestriction: "all"}},
						{"selected-empty", &domain.APIKey{ID: "p21-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
						{"selected-set", &domain.APIKey{ID: "p21-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}}},
					} {
						t.Run(credential.name, func(t *testing.T) {
							legacy, err := f.ResolveAccess(t.Context(), workspace, principal.user.ID, credential.key)
							if err != nil {
								t.Fatal(err)
							}
							resident, err := probe.ResolveAccess(t.Context(), workspace, principal.user.ID, credential.key)
							if err != nil {
								t.Fatal(err)
							}
							if legacy.WorkspaceID != resident.WorkspaceID || legacy.PrincipalID != resident.PrincipalID || legacy.CredentialScope != resident.CredentialScope {
								t.Fatal("resident policy changed the access binding")
							}
							// The off-mode compatibility digest includes normalized
							// defaults and is opaque across snapshot representations.
							// Bounded activation requires the journal's SQL authority.
							if journal == "on" && legacy.AuthzEpoch != resident.AuthzEpoch {
								t.Fatal("resident policy changed the authoritative epoch")
							}
							for _, key := range []RecordKey{{"projects", "p21-public-project"}, {"projects", "p21-old-slug"}, {"projects", "p21-private-project"}, {"projects", "p21-child-project"}, {"initiatives", "p21-initiative"}} {
								before, beforeAllowed, beforeErr := f.ResourceReadMetadata(t.Context(), legacy, key.Kind, key.ID)
								after, afterAllowed, afterErr := probe.ResourceReadMetadata(t.Context(), resident, key.Kind, key.ID)
								if beforeErr != nil || afterErr != nil || beforeAllowed != afterAllowed || !reflect.DeepEqual(before, after) {
									t.Fatalf("%s/%s changed fields or authorization: legacy=%v/%v resident=%v/%v", key.Kind, key.ID, beforeAllowed, beforeErr, afterAllowed, afterErr)
								}
								if credential.name == "session" && (key.ID == "p21-private-project" || key.ID == "p21-child-project") {
									visible := principal.name == "admin" || principal.name == "sub-team-inheritance"
									if (len(after.Projects) == 1) != visible {
										t.Fatalf("private boundary changed for %s", key.ID)
									}
								}
							}
							for _, repo := range []*SQLiteStore{f.SQLiteStore, probe} {
								forged := resident
								if repo == f.SQLiteStore {
									forged = legacy
								}
								forged.WorkspaceID = "another-workspace"
								if _, _, err := repo.ResourceReadMetadata(t.Context(), forged, "projects", "p21-public-project"); !errors.Is(err, ErrAuthForbidden) {
									t.Fatalf("cross-workspace binding accepted: %v", err)
								}
							}
						})
					}
				})
			}
			legacy, err := f.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			resident, err := probe.ResolveAccess(t.Context(), workspace, inherited.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetTeamMembership(t.Context(), workspaceID, private, inherited.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			for _, batch := range []string{"queued", "replay"} {
				t.Run(batch, func(t *testing.T) {
					if err := f.ValidateAccess(t.Context(), legacy); !errors.Is(err, ErrAuthForbidden) {
						t.Fatalf("legacy access survived revocation: %v", err)
					}
					if err := probe.ValidateAccess(t.Context(), resident); !errors.Is(err, ErrAuthForbidden) {
						t.Fatalf("resident access survived revocation: %v", err)
					}
				})
			}
			if probe.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("native permission differential decoded a full workspace")
			}
		})
	}
}
