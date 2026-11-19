package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// SW540: project and initiative collections whose IDs repeat are kept inline
// in the workspace row (no per-record rows). The bounded point readers must
// answer exactly as the hydrated literal copy, also on their canonical
// (non-resident) path.
func TestScaleP21SW540ResourceReadInlineCollections(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	prefix := idgen.New("sw540-")
	public, private := prefix+"public", prefix+"private"
	project, initiative := prefix+"project", prefix+"initiative"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw540_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public}, domain.Team{ID: private, Private: true})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.Projects = append(data.Projects,
			domain.Project{ID: project, SlugID: prefix + "first", Name: "First", TeamIDs: []string{public}, DependencyIDs: []string{prefix + "secret"}},
			domain.Project{ID: project, SlugID: prefix + "second", Name: "Second", TeamIDs: []string{private}},
			domain.Project{ID: prefix + "secret", SlugID: prefix + "secret-slug", PreviousSlugIDs: []string{prefix + "first"}, TeamIDs: []string{private}},
			domain.Project{ID: prefix + "linked", TeamIDs: []string{public}},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: initiative, SlugID: prefix + "initiative-first", ProjectIDs: []string{prefix + "linked"}, Creator: data.Viewer},
			domain.Initiative{ID: initiative, SlugID: prefix + "initiative-second", ProjectIDs: []string{prefix + "secret"}, Creator: data.Viewer},
			domain.Initiative{ID: prefix + "parent", ProjectIDs: []string{prefix + "secret"}, LeadTeamID: private, Creator: data.Viewer},
		)
		data.InitiativeRelations = append(data.InitiativeRelations,
			domain.InitiativeRelation{ID: prefix + "relation", Type: "parent", InitiativeID: initiative, RelatedInitiativeID: prefix + "parent"},
			domain.InitiativeRelation{ID: prefix + "relation", Type: "related", InitiativeID: prefix + "parent", RelatedInitiativeID: initiative},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"projects", "initiatives", "initiativeRelations"} {
		if inline, err := metadataFieldStoredInline(t.Context(), f.db, f.dialect, workspace, field); err != nil || !inline {
			t.Fatal("fixture field is not inline", field, inline, err)
		}
	}
	member := func(name, role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), name, idgen.New("sw540-user-")+"@example.test", "sw540-password")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = f.ErasePersonFromWorkspace(context.Background(), workspace, user.ID)
			_ = f.EraseAccount(context.Background(), user.ID)
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
	users := []domain.User{f.data.Viewer, member("Public member", "member", []string{public}), member("Guest", "guest", []string{public})}
	keys := []*domain.APIKey{nil,
		{ID: prefix + "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}},
		{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamIDs: []string{public}, TeamRestriction: "selected"},
	}
	items := []struct{ kind, ref string }{
		{"projects", project}, {"projects", prefix + "first"}, {"projects", prefix + "second"}, {"projects", prefix + "secret"},
		{"projects", prefix + "missing"},
		{"initiatives", initiative}, {"initiatives", prefix + "initiative-second"}, {"initiatives", prefix + "parent"},
	}
	type literal struct {
		body    string
		allowed bool
	}
	literals := func() map[string]literal {
		// The literal answer comes from the current hydrated copy.
		f.mu.RLock()
		known, exact := f.copyRevisionLocked(workspace)
		f.mu.RUnlock()
		if stamp, err := readMetadataStamp(t.Context(), f.db, workspace); err != nil || !exact || known.revision != stamp.revision {
			t.Fatal("literal copy is not current", known, stamp, exact, err)
		}
		want := map[string]literal{}
		for _, user := range users {
			for keyIndex, key := range keys {
				for _, item := range items {
					access, err := f.ResolveAccess(t.Context(), workspace, user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					data, allowed, err := f.ResourceReadMetadata(t.Context(), access, item.kind, item.ref)
					if err != nil {
						t.Fatal(err)
					}
					body, err := json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
					want[user.ID+"\x00"+string(rune('0'+keyIndex))+"\x00"+item.kind+"\x00"+item.ref] = literal{string(body), allowed}
				}
			}
		}
		if owner := want[users[0].ID+"\x000\x00projects\x00"+project]; !owner.allowed || !strings.Contains(owner.body, `"name":"First"`) {
			t.Fatal("literal fixture does not read the first inline project", owner)
		}
		return want
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "canonical"} {
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if state == "canonical" {
			// Another writer moves the stored revision on: the view's
			// installed source is stale, so its point readers take their
			// canonical SQL path while the literal copy stays current.
			if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw540_unrelated", "", nil, func(data *domain.Bootstrap) error {
				data.Labels = append(data.Labels, domain.IssueLabel{ID: prefix + "label", Name: "Unrelated"})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		want := literals()
		for _, user := range users {
			for keyIndex, key := range keys {
				for _, item := range items {
					expected := want[user.ID+"\x00"+string(rune('0'+keyIndex))+"\x00"+item.kind+"\x00"+item.ref]
					access, err := view.ResolveAccess(t.Context(), workspace, user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					view.metadataResidency.mu.Lock()
					stale := view.metadataResidency.logical[workspace].Stamp() != access.policy.stamp
					view.metadataResidency.mu.Unlock()
					if stale != (state == "canonical") {
						t.Fatal("point read source choice not exercised", state, stale)
					}
					read, err := view.BeginResourceRead(t.Context(), access, item.kind, item.ref)
					if err != nil {
						t.Fatalf("state=%s user=%s key=%d %s/%s read: %v", state, user.Name, keyIndex, item.kind, item.ref, err)
					}
					body, err := json.Marshal(read.Data)
					if err != nil {
						t.Fatal(err)
					}
					if read.Allowed != expected.allowed || string(body) != expected.body {
						read.Close()
						t.Fatalf("state=%s user=%s key=%d %s/%s allowed=%v/%v\nbounded=%s\nliteral=%s", state, user.Name, keyIndex, item.kind, item.ref, read.Allowed, expected.allowed, body, expected.body)
					}
					read.Close()
					point, err := view.BeginResourceAuthorizationRead(t.Context(), access, item.kind, item.ref)
					if err != nil {
						t.Fatalf("state=%s user=%s key=%d %s/%s authorization: %v", state, user.Name, keyIndex, item.kind, item.ref, err)
					}
					if point.Allowed != expected.allowed {
						point.Close()
						t.Fatalf("state=%s user=%s key=%d %s/%s point allowed=%v literal=%v", state, user.Name, keyIndex, item.kind, item.ref, point.Allowed, expected.allowed)
					}
					point.Close()
				}
			}
		}
		if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
			t.Fatal("point reads decoded complete metadata or leaked work")
		}
		view.releaseMetadataLogicalSources()
	}
}
