package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21ResourceReadDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	prefix := idgen.New("hot-resource-")
	public, private, child := prefix+"public", prefix+"private", prefix+"child"
	target, secret, linked := prefix+"target", prefix+"secret", prefix+"initiative"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "projects", "initiatives", "initiativeRelations", "favorites"), workspace, "scale_p21.resource_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public}, domain.Team{ID: private, Private: true}, domain.Team{ID: child})
		data.TeamSettings[public] = domain.TeamSettings{TeamID: public, Access: "public"}
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
		data.Projects = append(data.Projects,
			domain.Project{ID: target, SlugID: prefix + "renamed", PreviousSlugIDs: []string{prefix + "old"}, Name: "Native\x00name", TeamIDs: []string{public}, DependencyIDs: []string{secret, prefix + "child-project"}, Initiatives: []string{linked, prefix + "hidden"}, IssueCount: 73, Lead: &data.Viewer, Comments: []domain.Comment{{ID: "owned", Body: "body", User: data.Viewer, Reactions: map[string][]string{"like": {data.Viewer.ID}}}}},
			domain.Project{ID: secret, TeamIDs: []string{private}},
			domain.Project{ID: prefix + "child-project", TeamIDs: []string{child}},
			domain.Project{ID: prefix + "alias-hidden", SlugID: prefix + "alias", TeamIDs: []string{private}},
			domain.Project{ID: prefix + "alias-visible", PreviousSlugIDs: []string{prefix + "alias"}, TeamIDs: []string{public}},
			domain.Project{ID: prefix + "unrelated", Description: strings.Repeat("unrelated", 400000)},
		)
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: linked, SlugID: prefix + "initiative-slug", PreviousSlugIDs: []string{prefix + "initiative-old"}, ProjectIDs: []string{target, secret}, ParentInitiativeIDs: []string{prefix + "hidden"}, ContributingTeamIDs: []string{public, private}, Creator: data.Viewer, Owner: &data.Viewer},
			domain.Initiative{ID: prefix + "hidden", ProjectIDs: []string{secret}, Creator: data.Viewer},
			domain.Initiative{ID: prefix + "parent", ProjectIDs: []string{target}, Creator: data.Viewer},
		)
		data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: prefix + "relation", Type: "parent", InitiativeID: linked, RelatedInitiativeID: prefix + "parent"})
		data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + "favorite", UserID: data.Viewer.ID, ResourceType: "initiative", ResourceID: linked})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	member := func(name, role string, teams []string) domain.User {
		t.Helper()
		user, token, err := f.Register(t.Context(), name, idgen.New("hot-resource-user-")+"@example.test", "hot-resource-password")
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
	users := []domain.User{f.data.Viewer,
		member("Private admin", "admin", []string{private}),
		member("Member private outsider", "member", []string{public}),
		member("Guest explicit", "guest", []string{public}),
		member("Inherited member", "member", []string{private}),
	}
	if err := f.ReplaceTeamDefaultFavorites(t.Context(), workspace, public, []domain.TeamDefaultFavorite{{ResourceType: "initiative", ResourceID: prefix + "parent"}}); err != nil {
		t.Fatal(err)
	}
	keys := []*domain.APIKey{nil,
		{ID: prefix + "unrestricted", Scopes: []string{"read"}},
		{ID: prefix + "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: prefix + "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{public}},
		{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamIDs: []string{public}, TeamRestriction: "selected"},
	}
	for _, state := range []string{"warm", "reloaded", "canonical"} {
		if state == "reloaded" {
			if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if state == "canonical" {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, user := range users {
			for _, key := range keys {
				for _, item := range []struct{ kind, ref string }{
					{"projects", target}, {"projects", prefix + "old"}, {"projects", prefix + "alias"},
					{"projects", secret}, {"projects", "missing"},
					{"initiatives", linked}, {"initiatives", prefix + "initiative-old"}, {"initiatives", prefix + "parent"},
				} {
					oldAccess, err := f.ResolveAccess(t.Context(), workspace, user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					want, allowed, wantErr := f.ResourceReadMetadata(t.Context(), oldAccess, item.kind, item.ref)
					access, err := view.ResolveAccess(t.Context(), workspace, user.ID, key)
					if err != nil {
						t.Fatal(err)
					}
					read, err := view.BeginResourceRead(t.Context(), access, item.kind, item.ref)
					if err != nil || wantErr != nil {
						t.Fatalf("state=%s kind=%s ref=%s bounded=%v literal=%v", state, item.kind, item.ref, err, wantErr)
					}
					before, beforeErr := json.Marshal(want)
					after, afterErr := json.Marshal(read.Data)
					if read.Allowed != allowed || beforeErr != nil || afterErr != nil || string(before) != string(after) {
						read.Close()
						t.Fatalf("state=%s user=%s kind=%s ref=%s key=%+v allowed=%v/%v\nbounded=%s\nliteral=%s", state, user.Name, item.kind, item.ref, key, read.Allowed, allowed, after, before)
					}
					read.Close()
					read.Close()
					if !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || read.Allowed || view.metadataResidency.work.used != 0 {
						t.Fatal("owned read did not release/clear all output")
					}
				}
			}
		}
		if view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("detail decoded full workspace")
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21ResourceReadLargeOwnershipAndErrors(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	target := idgen.New("hot-resource-large-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.resource_large", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, Name: "large", Description: strings.Repeat("body", 800000), TeamIDs: []string{data.Teams[0].ID}, Comments: []domain.Comment{{ID: "owned", Body: "unchanged", Reactions: map[string][]string{"like": {data.Viewer.ID}}}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	for _, canonical := range []bool{false, true} {
		if canonical {
			f.mu.Lock()
			f.forgetCopyRevisionLocked(workspace)
			f.mu.Unlock()
		}
		view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		oldAccess, err := f.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		legacySQL, boundedSQL := 0, 0
		if _, _, err := f.ResourceReadMetadata(WithSQLStatementObserver(t.Context(), func(string) { legacySQL++ }), oldAccess, "projects", target); err != nil {
			t.Fatal(err)
		}
		read, err := view.BeginResourceRead(WithSQLStatementObserver(t.Context(), func(string) { boundedSQL++ }), access, "projects", target)
		if err != nil || !read.Allowed || len(read.Data.Projects[0].Description) != 3200000 {
			t.Fatal("legal large detail rejected/truncated", canonical, err)
		}
		if read.Data.Projects[0].IssueCount != 0 || view.metadataResidency.work.used == 0 {
			t.Fatal("paged count or owned admission changed")
		}
		t.Logf("large detail canonical=%t SQL literal=%d bounded=%d; extra canonical statements are byte preflights inside the extraction snapshot", canonical, legacySQL, boundedSQL)
		read.Data.Projects[0].Comments[0].Reactions["like"][0] = "edited"
		read.Close()
		again, err := view.BeginResourceRead(t.Context(), access, "projects", target)
		if err != nil || again.Data.Projects[0].Comments[0].Reactions["like"][0] == "edited" {
			t.Fatal("detail shared mutable output", err)
		}
		again.Close()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := view.BeginResourceRead(ctx, access, "projects", target); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation not propagated", err)
		}
		oldLimit := view.metadataResidency.work.limit
		view.metadataResidency.work.limit = 1
		if _, err := view.BeginResourceRead(t.Context(), access, "projects", target); !errors.Is(err, ErrMetadataWorkBudget) {
			t.Fatal("exhausted admission accepted", err)
		}
		view.metadataResidency.work.limit = oldLimit
		forged := access
		forged.WorkspaceID = "foreign"
		if _, err := view.BeginResourceRead(t.Context(), forged, "projects", target); !errors.Is(err, ErrAuthForbidden) {
			t.Fatal("cross-workspace access binding accepted", err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("failed detail leaked admission")
		}
		view.releaseMetadataLogicalSources()
	}
}

func TestScaleP21ResourceReadInheritedSnapshotPoolOne(t *testing.T) {
	for _, journal := range []string{"off", "on"} {
		t.Run(journal, func(t *testing.T) { scaleP21ResourceReadInheritedSnapshot(t, journal) })
	}
}

func scaleP21ResourceReadInheritedSnapshot(t *testing.T, journal string) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", journal)
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("hot-resource-snapshot-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.resource_snapshot", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, Name: "Caller snapshot", TeamIDs: []string{data.Teams[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.forgetCopyRevisionLocked(workspace)
	f.mu.Unlock()
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	access, err := view.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	literalAccess, err := f.ResolveAccess(ctx, workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, allowed, wantErr := f.ResourceReadMetadata(ctx, literalAccess, "projects", target)
	read, err := view.BeginResourceRead(ctx, access, "projects", target)
	if journal == "on" {
		if !errors.Is(wantErr, ErrRecordJournalUnavailable) || !errors.Is(err, wantErr) || read != nil {
			t.Fatal("pool-one live authority boundary changed", err, wantErr)
		}
	} else {
		if err != nil || wantErr != nil || read.Allowed != allowed || !reflect.DeepEqual(read.Data, want) {
			t.Fatal("borrowed read snapshot failed", err, wantErr)
		}
		read.Close()
	}
	var alive int
	if err := tx.QueryRowContext(t.Context(), "SELECT 1").Scan(&alive); err != nil || alive != 1 {
		t.Fatal("owned output closed caller snapshot", err)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("borrowed snapshot leaked output admission")
	}
}

func TestScaleP21ResourceReadNativeWireTarget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, idgen.New("hot-resource-native-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.resource_native", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, CreatedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.FixedZone("unsupported", 7*3600+123))})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldAccess, err := f.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, allowed, err := f.ResourceReadMetadata(t.Context(), oldAccess, "projects", target)
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginResourceRead(t.Context(), access, "projects", target)
	if err != nil {
		t.Fatal("native wire detail rejected", err)
	}
	before, beforeErr := json.Marshal(want)
	after, afterErr := json.Marshal(read.Data)
	if read.Allowed != allowed || beforeErr != nil || afterErr != nil || string(before) != string(after) {
		t.Fatal("native wire detail changed JSON", string(before), string(after), beforeErr, afterErr)
	}
	read.Close()
	if view.metadataResidency.work.used != 0 {
		t.Fatal("native wire detail leaked admission")
	}
}

func TestScaleP21ResourceReadRevokedAuthority(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	target := idgen.New("hot-resource-revoke-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.resource_revoke", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, TeamIDs: []string{data.Teams[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	literal, err := f.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID)
	}()
	for _, stage := range []string{"queued", "replayed"} {
		_, _, oldErr := f.ResourceReadMetadata(t.Context(), literal, "projects", target)
		if _, err := view.BeginResourceRead(t.Context(), access, "projects", target); !errors.Is(err, ErrResourceAccessChanged) || !errors.Is(err, ErrAuthForbidden) || !errors.Is(oldErr, ErrResourceAccessChanged) {
			t.Fatal("retained authority survived revocation", stage, err, oldErr)
		}
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("revoked read leaked output")
	}
}

func TestScaleP21ResourceReadUnrelatedCountBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	f := scaleP09Database(t)
	workspace, target := f.data.Workspace.URLKey, "target--00000001"
	alias := "old-small-target"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "scale_p21.resource_count", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: target, Name: "small", PreviousSlugIDs: []string{alias}, TeamIDs: []string{data.Teams[0].ID}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	base, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("missing fixture")
	}
	var selected domain.Project
	for _, item := range base.Projects {
		if item.ID == target {
			selected = item
		}
	}
	inserted := 0
	for _, canonical := range []bool{false, true} {
		var heldBaseline int
		var allocBaseline float64
		for _, count := range []int{100, 1000} {
			if canonical {
				tx, err := f.db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				for index := inserted; index < count; index++ {
					id := fmt.Sprintf("others--%08d", index)
					if _, err := tx.ExecContext(t.Context(), `INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projects',?,?,?)`, workspace, id, index+1,
						`{"id":"`+id+`","previousSlugIds":[],"teamIds":[],"description":"`+strings.Repeat("x", 4096)+`"}`); err != nil {
						t.Fatal(err)
					}
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				inserted = count
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			// A deterministic installed-source fixture puts the target last:
			// all unrelated identities must be scanned without retained headers.
			data := base
			data.Projects = nil
			for index := range count {
				data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("others--%08d", index), Description: strings.Repeat("x", 4096)})
			}
			data.Projects = append(data.Projects, selected)
			stamp := access.policy.stamp
			if canonical {
				stamp.revision = -1
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, stamp)
			if err != nil {
				t.Fatal(err)
			}
			view.installMetadataLogicalSource(workspace, source)
			view.metadataResidency.work.limit = 8 << 20
			ref := target
			if canonical {
				ref = alias // force the original full alias-header scan
			}
			held := 0
			allocs := testing.AllocsPerRun(1, func() {
				read, err := view.BeginResourceRead(t.Context(), access, "projects", ref)
				if err != nil {
					t.Fatal("unrelated count consumed detail quota", canonical, count, err)
				}
				if !read.Allowed || len(read.Data.Projects) != 1 || read.Data.Projects[0].ID != target {
					t.Fatal("count fixture changed selected detail")
				}
				held = view.metadataResidency.work.used
				read.Close()
			})
			if count == 100 {
				heldBaseline, allocBaseline = held, allocs
			} else if held != heldBaseline {
				t.Fatal("retained work scales with unrelated rows", canonical, heldBaseline, held)
			}
			if !canonical && count == 1000 && allocs > allocBaseline+64 {
				t.Fatal("native header scan allocates per unrelated record", allocBaseline, allocs)
			}
			if view.metadataResidency.work.used != 0 {
				t.Fatal("count fixture leaked work")
			}
			t.Logf("canonical=%t unrelated=%d held_work=%d allocs/op=%.0f (canonical SQL driver/header allocations are transient)", canonical, count, held, allocs)
			view.releaseMetadataLogicalSources()
		}
	}
}

func TestScaleP21ResourceReadNativeDuplicateReferences(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, team := f.data.Workspace.URLKey, f.data.Teams[0].ID
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, &domain.APIKey{ID: "duplicate-key", TeamRestriction: "selected", TeamIDs: []string{team}, Scopes: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	data := domain.Bootstrap{
		Workspace: f.data.Workspace,
		Projects: []domain.Project{
			{ID: "duplicate", SlugID: "alias", TeamIDs: []string{}},
			{ID: "selected", PreviousSlugIDs: []string{"alias"}, TeamIDs: []string{team}, Initiatives: []string{"parent"}},
			{ID: "duplicate", TeamIDs: []string{team}},
		},
		Initiatives: []domain.Initiative{{ID: "parent", ProjectIDs: []string{"duplicate"}}},
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, access.policy.stamp)
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(workspace, source)
	want, allowed, err := resourceReadMetadata(access.policy, data, "projects", "alias")
	if err != nil {
		t.Fatal(err)
	}
	read, err := view.BeginResourceRead(t.Context(), access, "projects", "alias")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	before, _ := json.Marshal(want)
	after, _ := json.Marshal(read.Data)
	if read.Allowed != allowed || string(before) != string(after) || len(read.Data.Projects[0].Initiatives) != 1 {
		t.Fatal("streamed reference representative changed duplicate-ID visibility", string(before), string(after))
	}
}
