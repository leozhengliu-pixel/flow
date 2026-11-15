package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21InstalledPointJSON(t *testing.T, data domain.Bootstrap, kind, ref string) []byte {
	t.Helper()
	var selected any
	if kind == "projects" {
		if index := domain.ProjectIndexBySlugRef(data.Projects, ref); index >= 0 {
			selected = data.Projects[index]
		}
	} else if index := domain.InitiativeIndexBySlugRef(data.Initiatives, ref); index >= 0 {
		selected = data.Initiatives[index]
	}
	raw, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21InstalledResourcePointReadNative(t *testing.T) {
	stamp := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("native", 7*3600+123))
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "organization", URLKey: "logical"},
		Projects: []domain.Project{
			{ID: "first", Name: "Native\x00first", SlugID: "duplicate", PreviousSlugIDs: []string{"previous", "stale-000000000021"}, IssueCount: 73, TeamIDs: []string{"private"}, Creator: &domain.User{ID: "stale-actor", Name: "Stored actor"}, ArchivedAt: &stamp, Progress: 0.75, LabelIDs: []string{"hidden-label"}},
			{ID: "suffix", Name: "First suffix", SlugID: "current-000000000021", PreviousSlugIDs: []string{"previous"}},
			{ID: "duplicate", Name: "Later exact ID", SlugID: "later-000000000022"},
			{ID: "later-suffix", Name: "Later suffix", SlugID: "later-000000000021"},
			{ID: "duplicate", Name: "Later duplicate ID"},
			{ID: "", SlugID: "", Name: "Empty"},
		},
		Initiatives: []domain.Initiative{
			{ID: "first", Name: "Native\x00initiative", SlugID: "duplicate", PreviousSlugIDs: []string{"previous", "stale-000000000021"}, Favorite: true, Creator: domain.User{ID: "stale-actor", Name: "Stored actor"}, ProjectIDs: []string{"hidden-project"}, CreatedAt: stamp},
			{ID: "suffix", Name: "First suffix", SlugID: "current-000000000021", PreviousSlugIDs: []string{"previous"}},
			{ID: "duplicate", Name: "Later exact ID", SlugID: "later-000000000022"},
			{ID: "later-suffix", Name: "Later suffix", SlugID: "later-000000000021"},
		},
	}
	values := map[string]domain.Bootstrap{"physical\x00": data}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = "physical\x00"
	legacy := &SQLiteStore{workspaces: values, lastWorkspaceKey: "physical\x00"}
	for _, workspace := range []string{"physical\x00", ""} {
		prior, found := legacy.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("missing legacy fixture")
		}
		for _, kind := range []string{"projects", "initiatives"} {
			for _, ref := range []string{"first", "duplicate", "suffix", "current-000000000021", "stale-000000000021", "000000000021", "previous", " first ", "FIRST", "", "missing"} {
				queries := 0
				ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
				read, found, err := view.BeginInstalledResourcePointRead(ctx, workspace, kind, ref)
				if err != nil || !found {
					t.Fatal("trusted point read failed", kind, ref, found, err)
				}
				want := scaleP21InstalledPointJSON(t, prior, kind, ref)
				got := scaleP21InstalledPointJSON(t, read.Data, kind, ref)
				if !bytes.Equal(got, want) || read.Allowed != !bytes.Equal(want, []byte("null")) {
					t.Fatalf("native point output differs %s/%q:\n%s\n%s", kind, ref, want, got)
				}
				if view.MetadataWorkUsedForTesting() == 0 || !read.SourceRetainedForTesting() || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("trusted point lacks ownership or used SQL/full decode", queries)
				}
				read.Close()
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 || read.SourceRetainedForTesting() || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) || read.Allowed {
					t.Fatal("trusted point close retained output/source/admission")
				}
			}
		}
	}
	for _, workspace := range []string{"logical", "missing", "physical", "physical\x00 "} {
		if read, found, err := view.BeginInstalledResourcePointRead(t.Context(), workspace, "projects", "first"); err != nil || found || read != nil {
			t.Fatal("workspace default/physical binding changed", workspace, found, err)
		}
	}
	if _, _, err := view.BeginInstalledResourcePointRead(t.Context(), "", "labels", "first"); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("trusted point accepted an unreviewed family", err)
	}
	off := &SQLiteStore{}
	if _, _, err := off.BeginInstalledResourcePointRead(t.Context(), "", "projects", "first"); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("trusted point inferred a literal off accessor", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := view.BeginInstalledResourcePointRead(cancelled, "", "projects", "first"); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancellation retained work", err)
	}
	limit := view.metadataResidency.work.limit
	for _, value := range []int{1, 128 << 10, 2<<20 + 128<<10} {
		view.metadataResidency.work.limit = value
		if read, _, err := view.BeginInstalledResourcePointRead(t.Context(), "", "projects", "first"); !errors.Is(err, ErrMetadataWorkBudget) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial admission failure retained a source/output", value, err)
		}
	}
	view.metadataResidency.work.limit = limit
}

func TestScaleP21InstalledResourcePointReadPinnedAndConcurrent(t *testing.T) {
	before := domain.Bootstrap{Projects: []domain.Project{{ID: "target", Name: "Before", IssueCount: 99}}}
	after := domain.Bootstrap{Projects: []domain.Project{{ID: "target", Name: "After", IssueCount: 7}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before})
	view.lastWorkspaceKey = "physical"
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	read, found, err := view.BeginInstalledResourcePointRead(pinned, "", "projects", "target")
	if err != nil || !found || len(read.Data.Projects) != 1 || read.Data.Projects[0].Name != "Before" {
		t.Fatal("trusted child changed the explicit installed generation", found, err)
	}
	parent.Close()
	if read.Data.Projects[0].IssueCount != 99 || !read.SourceRetainedForTesting() {
		t.Fatal("trusted child borrowed its parent's output/source lifetime")
	}
	read.Close()
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 5 {
				read, found, err := view.BeginInstalledResourcePointRead(t.Context(), "", "projects", "target")
				if err == nil && (!found || len(read.Data.Projects) != 1 || read.Data.Projects[0].Name != "After" || read.Data.Projects[0].IssueCount != 7) {
					err = ErrBoundedMetadataCoverage
				}
				if read != nil {
					read.Close()
				}
				if err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("trusted concurrent reads retained work")
	}
}

func TestScaleP21InstalledResourcePointReadWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	prefix := idgen.New("p21-installed-resource-")
	held := map[string]int{}
	for _, count := range []int{10000, 100000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "projects", "initiatives"), workspace, "scale_p21.installed_resource_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{
				{ID: prefix + "project", SlugID: prefix + "current-project", PreviousSlugIDs: []string{prefix + "old-project"}, Name: "Warm\x00project", Description: strings.Repeat("selected body ", 22000), IssueCount: 81, Creator: &domain.User{ID: data.Viewer.ID, Name: "Stored actor"}},
				{ID: prefix + "unrelated", Name: strings.Repeat("ignored ", count), Description: strings.Repeat("unrelated body ", count)},
			}
			data.Initiatives = []domain.Initiative{
				{ID: prefix + "initiative", SlugID: prefix + "current-initiative", PreviousSlugIDs: []string{prefix + "old-initiative"}, Name: "Warm\x00initiative", Description: strings.Repeat("selected body ", 22000), Favorite: true, Creator: domain.User{ID: data.Viewer.ID, Name: "Stored actor"}},
				{ID: prefix + "unrelated", Name: strings.Repeat("ignored ", count), Description: strings.Repeat("unrelated body ", count)},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{"warm", "reloaded", "unknown-revision"} {
			if state == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "unknown-revision" {
				if err := f.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			prior, _ := f.WorkspaceMetadata(workspace)
			for _, resource := range []struct{ kind, ref string }{{"projects", prefix + "old-project"}, {"initiatives", prefix + "old-initiative"}} {
				queries := 0
				ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
				read, found, err := view.BeginInstalledResourcePointRead(ctx, "", resource.kind, resource.ref)
				if err != nil || !found {
					t.Fatal("warm/reloaded trusted point failed", state, found, err)
				}
				want := scaleP21InstalledPointJSON(t, prior, resource.kind, resource.ref)
				got := scaleP21InstalledPointJSON(t, read.Data, resource.kind, resource.ref)
				if !bytes.Equal(want, got) {
					t.Fatal("warm/reloaded native output changed", state, resource.kind)
				}
				cost := view.MetadataWorkUsedForTesting()
				if held[state+resource.kind] == 0 {
					held[state+resource.kind] = cost
				}
				if cost != held[state+resource.kind] || queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("unrelated body growth changed retained work or used SQL/full decode", cost, queries)
				}
				read.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("warm/reloaded trusted point retained work")
				}
				t.Logf("body_repetitions=%d state=%s kind=%s held_work=%d SQL=%d", count, state, resource.kind, cost, queries)
			}
		}
	}
}

func TestScaleP21InstalledResourcePointReadProjectedComposition(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	prefix := idgen.New("p21-projected-point-")
	teamID := prefix + "team"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings", "projects", "initiatives"), workspace, "scale_p21.projected_point_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: teamID}, domain.Team{ID: prefix + "private", Private: true}, domain.Team{ID: ""})
		data.TeamSettings[teamID] = domain.TeamSettings{Access: "private\x00"}
		data.TeamSettings[prefix+"private"] = domain.TeamSettings{Access: "private"}
		data.TeamSettings[""] = domain.TeamSettings{Access: "private"}
		data.Projects = []domain.Project{
			{ID: prefix + "hidden", SlugID: prefix + "alias", TeamIDs: []string{prefix + "private"}},
			{ID: prefix + "target", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "alias"}, Name: "Native\x00target", TeamIDs: []string{teamID}, Creator: &domain.User{ID: userID, Name: "Stored actor"}},
			{ID: prefix + "empty-team-project", TeamIDs: []string{""}},
		}
		data.Initiatives = []domain.Initiative{
			{ID: prefix + "initiative", Name: "Native\x00initiative", ProjectIDs: []string{prefix + "target"}, Creator: domain.User{ID: userID, Name: "Stored actor"}, Favorite: true},
			{ID: prefix + "empty-team-initiative", ProjectIDs: []string{prefix + "empty-team-project"}},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	resources := []struct{ kind, ref string }{{"projects", prefix + "target"}, {"projects", prefix + "alias"}, {"initiatives", prefix + "initiative"}, {"projects", prefix + "empty-team-project"}, {"initiatives", prefix + "empty-team-initiative"}, {"projects", "missing"}}
	for _, provenance := range []string{"installed", "caller", "caller-private-policy", "pinned"} {
		prior, _ := f.WorkspaceMetadata(workspace)
		ctx := t.Context()
		closeSource := func() {}
		switch provenance {
		case "caller", "caller-private-policy":
			prior.Projects[1].Name = "Caller\x00target"
			prior.Projects[1].Creator.Name = "Caller actor"
			if provenance == "caller-private-policy" {
				for index := range prior.Teams {
					if prior.Teams[index].ID == teamID {
						prior.Teams[index].Private = true
					}
				}
				prior.TeamSettings[teamID] = domain.TeamSettings{Access: "private"}
			}
			tx, err := f.db.BeginTx(ctx, metadataReadOptions(f.dialect))
			if err != nil {
				t.Fatal(err)
			}
			closeSource = func() { _ = tx.Rollback() }
			ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx, metadata: prior})
		case "pinned":
			parent, _, err := view.BeginInstalledMetadataRead(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			ctx, err = parent.Context(ctx)
			if err != nil {
				t.Fatal(err)
			}
			closeSource = parent.Close
			after := cloneBootstrap(prior)
			after.Projects[1].Name = "Later installed value"
			source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{workspaceID: prior.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			view.installMetadataLogicalSource(workspace, source)
		}
		for _, resource := range resources {
			literalCtx := ctx
			closeLiteral := func() {}
			// The old accessor has no logical-pin mechanism; give it the
			// identical retained generation as its supported caller snapshot.
			if provenance == "pinned" {
				tx, err := f.db.BeginTx(t.Context(), metadataReadOptions(f.dialect))
				if err != nil {
					t.Fatal(err)
				}
				closeLiteral = func() { _ = tx.Rollback() }
				literalCtx = context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx, metadata: prior})
			}
			want, err := f.PagedWorkspaceMetadata(literalCtx, workspace, userID)
			closeLiteral()
			if err != nil {
				t.Fatal(err)
			}
			access, err := view.ResolveAccess(t.Context(), workspace, userID, nil)
			if err != nil {
				t.Fatal(err)
			}
			normalizedTargetQueries := 0
			readCtx := WithSQLStatementObserver(ctx, func(query string) {
				if strings.Contains(query, "field='projectStatuses'") || strings.Contains(query, "field='users'") || strings.Contains(query, "field='projects'") || strings.Contains(query, "field='initiatives'") {
					normalizedTargetQueries++
				}
			})
			read, err := view.BeginProjectedInstalledResourcePointRead(readCtx, access, resource.kind, resource.ref)
			if err != nil {
				t.Fatal("native projected read failed", provenance, resource, err)
			}
			before := scaleP21InstalledPointJSON(t, want, resource.kind, resource.ref)
			after := scaleP21InstalledPointJSON(t, read.Data, resource.kind, resource.ref)
			if !bytes.Equal(before, after) || normalizedTargetQueries != 0 {
				t.Fatalf("native projection differs provenance=%s kind=%s ref=%s SQL=%d\n%s\n%s", provenance, resource.kind, resource.ref, normalizedTargetQueries, before, after)
			}
			policy, err := read.installedResourcePolicy(t.Context())
			if err != nil || policy.TeamSettings[""].Access != "private" {
				t.Fatal("actual empty team identity lost its policy", provenance, err)
			}
			read.Close()
		}
		closeSource()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("projected source provenance retained work")
		}
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
			t.Fatal(err)
		}
	}
	access, err := view.ResolveAccess(t.Context(), workspace, userID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_memberships SET status='suspended' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, userID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = f.db.ExecContext(context.Background(), `UPDATE workspace_memberships SET status='active' WHERE workspace_id=? AND user_id=?`, f.data.Workspace.ID, userID)
	}()
	if read, err := view.BeginProjectedInstalledResourcePointRead(t.Context(), access, "projects", prefix+"target"); !errors.Is(err, ErrAuthForbidden) || read != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("projected installed read reused revoked authority or retained output", err)
	}
}

func TestScaleP21InstalledResourcePointReadSQLIdentityDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	if f.dialect == "postgres" {
		t.Skip("PostgreSQL text parameters cannot retain the NUL record key")
	}
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("p21-sql-point-")
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	teamID, sqlKey := prefix+"team", prefix+"\x00team"
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "projects", "initiatives"), workspace, "scale_p21.sql_point_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: teamID})
		data.Projects = append(data.Projects, domain.Project{ID: prefix + "project", Name: "SQL identity project", TeamIDs: []string{teamID}})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: prefix + "initiative", Name: "SQL identity initiative", ProjectIDs: []string{prefix + "project"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// SQL is a supported producer. Legacy full hydration takes the JSON ID;
	// the physical lookup key is valid SQLite/MySQL text but contains NUL.
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET record_key=? WHERE workspace_key=? AND field='teams' AND record_key=?`, sqlKey, workspace, teamID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.db.ExecContext(context.Background(), `UPDATE workspace_metadata_records SET record_key=? WHERE workspace_key=? AND field='teams' AND record_key=?`, teamID, workspace, sqlKey); err != nil {
			t.Error(err)
		}
	}()
	if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	prior, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
	if err != nil {
		t.Fatal("literal projected source rejected the supported SQL state", err)
	}
	access, err := view.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal("supported SQL identity was rejected", err)
	}
	for _, resource := range []struct{ kind, ref string }{{"projects", prefix + "project"}, {"initiatives", prefix + "initiative"}} {
		before := scaleP21InstalledPointJSON(t, prior, resource.kind, resource.ref)
		read, err := view.BeginProjectedInstalledResourcePointRead(t.Context(), access, resource.kind, resource.ref)
		if err != nil || bytes.Equal(before, []byte("null")) {
			t.Fatal("supported SQL public point read failed", err)
		}
		after := scaleP21InstalledPointJSON(t, read.Data, resource.kind, resource.ref)
		read.Close()
		if !bytes.Equal(before, after) {
			t.Fatal("supported SQL point output changed")
		}
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("SQL identity read retained work")
	}
	t.Log("supported SQL identity: literal and bounded projected project/initiative outputs match")
}
