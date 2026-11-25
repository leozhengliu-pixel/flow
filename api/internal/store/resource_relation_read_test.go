package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21RelationJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestScaleP21ResourceRelationNativeAndShape(t *testing.T) {
	at := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("relation-native", 7*3600+123))
	for _, shape := range []string{"nil", "empty", "records"} {
		data := domain.Bootstrap{
			Workspace: domain.Workspace{ID: "organization", URLKey: "logical"},
			Projects: []domain.Project{
				{ID: "target", SlugID: "target-alias", Name: strings.Repeat("unrelated ", 200000)},
			},
			Initiatives: []domain.Initiative{{ID: "target", SlugID: "target-alias"}},
		}
		if shape != "nil" {
			data.ProjectRelations, data.InitiativeRelations = []domain.ProjectRelation{}, []domain.InitiativeRelation{}
		}
		if shape == "records" {
			data.ProjectRelations = []domain.ProjectRelation{
				{ID: "duplicate", ProjectID: "target", RelatedProjectID: "dangling", Type: "blocks\x00", ProjectMilestoneID: "native\x00milestone", RelatedProjectMilestoneID: "other", CreatedAt: at, UpdatedAt: at},
				{ID: "duplicate", ProjectID: "missing", RelatedProjectID: "target", Type: "related", CreatedAt: at.Add(-time.Hour), UpdatedAt: at},
				{ID: "", ProjectID: "", RelatedProjectID: "target", CreatedAt: at, UpdatedAt: at},
				{ID: "unrelated", ProjectID: "other", RelatedProjectID: "elsewhere"},
			}
			data.InitiativeRelations = []domain.InitiativeRelation{
				{ID: "duplicate", InitiativeID: "target", RelatedInitiativeID: "dangling", Type: "parent\x00", SortOrder: 3, CreatedAt: at, UpdatedAt: at},
				{ID: "duplicate", InitiativeID: "missing", RelatedInitiativeID: "target", Type: "blocks", SortOrder: -1, CreatedAt: at, UpdatedAt: at},
				{ID: "", InitiativeID: "", RelatedInitiativeID: "target", SortOrder: -1},
				{ID: "unrelated", InitiativeID: "other", RelatedInitiativeID: "elsewhere"},
			}
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		view.lastWorkspaceKey = "physical"
		for _, workspace := range []string{"physical", ""} {
			for _, kind := range []string{"projects", "initiatives"} {
				for _, ref := range []string{"target", "target-alias", "", "missing", "target "} {
					read, err := view.BeginResourceRelationRead(t.Context(), workspace, "", nil, kind)
					if err != nil {
						t.Fatal(shape, kind, err)
					}
					if err := read.Select(t.Context(), ref); err != nil {
						t.Fatal(shape, kind, err)
					}
					wantProjects := slices.DeleteFunc(slices.Clone(data.ProjectRelations), func(item domain.ProjectRelation) bool {
						return item.ProjectID != ref && item.RelatedProjectID != ref
					})
					wantInitiatives := slices.DeleteFunc(slices.Clone(data.InitiativeRelations), func(item domain.InitiativeRelation) bool {
						return item.InitiativeID != ref && item.RelatedInitiativeID != ref
					})
					if kind == "projects" && scaleP21RelationJSON(t, read.ProjectRelations) != scaleP21RelationJSON(t, wantProjects) ||
						kind == "initiatives" && scaleP21RelationJSON(t, read.InitiativeRelations) != scaleP21RelationJSON(t, wantInitiatives) {
						t.Fatal("native relation/shape selection changed", shape, workspace, kind, ref)
					}
					if view.MetadataWorkUsedForTesting() == 0 {
						t.Fatal("relation output lacks owner")
					}
					read.Close()
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 ||
						read.ProjectRelations != nil || read.InitiativeRelations != nil {
						t.Fatal("relation lifetime leaked")
					}
				}
			}
		}
	}
}

func TestScaleP21ResourceRelationCaptureQuotaAndConcurrent(t *testing.T) {
	data := domain.Bootstrap{
		Workspace:        domain.Workspace{ID: "organization"},
		ProjectRelations: []domain.ProjectRelation{{ID: "old", ProjectID: "target"}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	old, err := view.BeginResourceRelationRead(t.Context(), "physical", "", nil, "projects")
	if err != nil {
		t.Fatal(err)
	}
	data.ProjectRelations[0].ID = "new"
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	current, err := view.BeginResourceRelationRead(pinned, "physical", "", nil, "projects")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Select(t.Context(), "target"); err != nil {
		t.Fatal(err)
	}
	if err := current.Select(t.Context(), "target"); err != nil {
		t.Fatal(err)
	}
	if old.ProjectRelations[0].ID != "old" || current.ProjectRelations[0].ID != "new" {
		t.Fatal("fresh source or retained output generation changed")
	}
	current.Close()
	old.Close()
	parent.Close()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.BeginResourceRelationRead(canceled, "physical", "", nil, "projects"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored", err)
	}
	if _, err := view.BeginResourceRelationRead(t.Context(), "missing", "", nil, "projects"); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("missing source changed", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if _, err := view.BeginResourceRelationRead(t.Context(), "physical", "", nil, "projects"); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("constructor budget ignored", err)
	}
	view.metadataResidency.work.limit = limit
	read, err := view.BeginResourceRelationRead(t.Context(), "physical", "", nil, "projects")
	if err != nil {
		t.Fatal(err)
	}
	view.metadataResidency.work.limit = view.MetadataWorkUsedForTesting()
	if err := read.Select(t.Context(), "target"); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("selection budget ignored", err)
	}
	read.Close()
	view.metadataResidency.work.limit = limit
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			read, err := view.BeginResourceRelationRead(t.Context(), "physical", "", nil, "projects")
			if err != nil {
				t.Error(err)
				return
			}
			defer read.Close()
			if err := read.Select(t.Context(), "target"); err != nil || len(read.ProjectRelations) != 1 {
				t.Error("concurrent selection", err)
			}
		})
	}
	group.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("failed/concurrent relation read leaked work")
	}
}

func TestScaleP21ResourceRelationWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("relation-reader-")
	at := time.Date(2026, 11, 14, 1, 2, 3, 4, time.UTC)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	held := map[string]int{}
	for _, scale := range []int{1, 10} {
		if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.relation_reader_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{
				{ID: prefix + "target", Name: "Target", TeamIDs: []string{data.Teams[0].ID}, Description: strings.Repeat("unrelated body ", scale*10000)},
				{ID: prefix + "other", Name: "Other", TeamIDs: []string{data.Teams[0].ID}},
			}
			data.Initiatives = []domain.Initiative{
				{ID: prefix + "target", Name: "Target initiative", ProjectIDs: []string{prefix + "target"}},
				{ID: prefix + "other", Name: "Other initiative", ProjectIDs: []string{prefix + "other"}, Description: strings.Repeat("unrelated body ", scale*10000)},
			}
			data.ProjectRelations = []domain.ProjectRelation{{ID: prefix + "project-edge", ProjectID: prefix + "target", RelatedProjectID: prefix + "other", Type: "related", CreatedAt: at, UpdatedAt: at}}
			data.InitiativeRelations = []domain.InitiativeRelation{{ID: prefix + "initiative-edge", InitiativeID: prefix + "target", RelatedInitiativeID: prefix + "other", Type: "parent", CreatedAt: at, UpdatedAt: at}}
			data.Documents = []domain.Document{{ID: prefix + "document", Content: strings.Repeat("unrelated body ", scale*10000)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			for _, principal := range []string{"", f.data.Viewer.ID} {
				var expected domain.Bootstrap
				if principal == "" {
					expected, _ = f.BootstrapFor(workspace)
				} else {
					expected, _, err = f.BootstrapForUser(t.Context(), workspace, principal)
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, kind := range []string{"projects", "initiatives"} {
					read, err := view.BeginResourceRelationRead(t.Context(), workspace, principal, nil, kind)
					if err != nil {
						t.Fatal(err)
					}
					if err := read.Select(t.Context(), prefix+"target"); err != nil {
						t.Fatal(err)
					}
					if kind == "projects" && !reflect.DeepEqual(read.ProjectRelations, expected.ProjectRelations) ||
						kind == "initiatives" && !reflect.DeepEqual(read.InitiativeRelations, expected.InitiativeRelations) {
						t.Fatal("warm/reloaded relation changed", reload, principal, kind)
					}
					key := kind + "/" + principal
					cost := view.MetadataWorkUsedForTesting()
					if previous, found := held[key]; found && previous != cost {
						t.Fatal("body10x or reload increased admitted relation work", key, cost, previous)
					}
					held[key] = cost
					t.Logf("scale=%d reload=%v authenticated=%v kind=%s held_work=%d", scale, reload, principal != "", kind, cost)
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("SQL fixture relation read leaked ownership or decoded full metadata")
					}
				}
			}
		}
	}
}

func TestScaleP21ResourceRelationBorrowedSQLPoolOne(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("relation-snapshot-")
	private := prefix + "private"
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.relation_snapshot_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: private, Key: private, Private: true})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.Projects = []domain.Project{
			{ID: prefix + "target", Name: "Public", TeamIDs: []string{data.Teams[0].ID}},
			{ID: prefix + "other", Name: "Private", TeamIDs: []string{private}},
		}
		data.Initiatives = []domain.Initiative{
			{ID: prefix + "target", Name: "Public initiative", ProjectIDs: []string{prefix + "target"}},
			{ID: prefix + "other", Name: "Private initiative", LeadTeamID: private, ProjectIDs: []string{prefix + "other"}},
		}
		data.ProjectRelations = []domain.ProjectRelation{{ID: prefix + "old-project", ProjectID: prefix + "target", RelatedProjectID: prefix + "other"}}
		data.InitiativeRelations = []domain.InitiativeRelation{{ID: prefix + "old-initiative", InitiativeID: prefix + "target", RelatedInitiativeID: prefix + "other", Type: "related"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?) ON CONFLICT(workspace_id,team_id,user_id) DO UPDATE SET role=excluded.role`,
		f.data.Workspace.ID, private, f.data.Viewer.ID, "member", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	oldSource, _, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer oldSource.Close()
	snapshot, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	pinned, err := oldSource.Context(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	held := pinned.Value(workspaceReadKey{}).(workspaceRead)
	if held.reader == nil || held.metadata.ProjectRelations[0].ID != prefix+"old-project" {
		t.Fatal("test did not capture a real earlier SQL/native snapshot")
	}
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.relation_snapshot_current", "", nil, func(data *domain.Bootstrap) error {
		data.ProjectRelations[0].ID = prefix + "current-project"
		data.InitiativeRelations[0].ID = prefix + "current-initiative"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`, f.data.Workspace.ID, private, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
		t.Fatal(err)
	}
	f.db.SetMaxOpenConns(1)
	defer func() {
		release()
		f.db.SetMaxOpenConns(4)
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(pinned, 10*time.Second)
	defer cancel()
	literal, ok, err := f.BootstrapForUser(ctx, workspace, f.data.Viewer.ID)
	if err != nil || !ok || len(literal.ProjectRelations) != 1 || len(literal.InitiativeRelations) != 1 {
		t.Fatal("literal borrowed authority did not retain old membership", ok, err)
	}
	if literal.ProjectRelations[0].ID != prefix+"current-project" || literal.InitiativeRelations[0].ID != prefix+"current-initiative" {
		t.Fatal("literal unexpectedly used earlier native snapshot metadata")
	}
	base := view.MetadataWorkUsedForTesting()
	for _, kind := range []string{"projects", "initiatives"} {
		read, err := view.BeginResourceRelationRead(ctx, workspace, f.data.Viewer.ID, nil, kind)
		if err != nil {
			t.Fatal("borrowed SQL pool-one relation read", kind, err)
		}
		if !read.directory.borrowed {
			t.Fatal("relation reader did not borrow the supplied SQL reader")
		}
		if err := read.Select(ctx, prefix+"target"); err != nil {
			read.Close()
			t.Fatal(kind, err)
		}
		if kind == "projects" && !reflect.DeepEqual(read.ProjectRelations, literal.ProjectRelations) ||
			kind == "initiatives" && !reflect.DeepEqual(read.InitiativeRelations, literal.InitiativeRelations) {
			t.Fatal("current native relation/old SQL authority boundary changed", kind)
		}
		read.Close()
		if view.MetadataWorkUsedForTesting() != base {
			t.Fatal("borrowed relation lifetime leaked", kind)
		}
	}
	var status string
	if err := held.reader.QueryRowContext(ctx, `SELECT status FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, f.data.Viewer.ID).Scan(&status); err != nil || status != "active" {
		t.Fatal("relation reader closed or escaped the caller transaction", status, err)
	}
	oldSource.Close()
	release()
	if _, _, err := f.BootstrapForUser(t.Context(), workspace, f.data.Viewer.ID); !errors.Is(err, ErrAuthForbidden) {
		t.Fatal("current SQL authority was not actually revoked", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("borrowed snapshot relation read leaked work or decoded full metadata")
	}
}
