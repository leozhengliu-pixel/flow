package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW82Header(project domain.Project) domain.Project {
	return domain.Project{ID: project.ID, TeamIDs: slices.Clone(project.TeamIDs)}
}

func scaleP21SW82Compare(t *testing.T, legacy, view *SQLiteStore, workspace, id string) int {
	t.Helper()
	original, exists := legacy.ProjectByID(workspace, id)
	queries := 0
	read, found, err := view.BeginProjectVisibilityRead(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), workspace, id)
	if err != nil || !found || read == nil {
		t.Fatal("project visibility reader lost its workspace", workspace, id, found, err)
	}
	defer read.Close()
	if read.Allowed != exists || len(read.Data.Projects) != 0 && len(read.Data.Projects) != 1 {
		t.Fatal("project visibility existence differs", id)
	}
	if exists {
		if len(read.Data.Projects) != 1 || !reflect.DeepEqual(read.Data.Projects[0], scaleP21SW82Header(original)) {
			t.Fatalf("first exact project header differs for %q: %#v / %#v", id, read.Data.Projects, original)
		}
	}
	if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || !read.SourceRetainedForTesting() {
		t.Fatal("project header used SQL/full hydration or lost source ownership", queries)
	}
	held := view.MetadataWorkUsedForTesting()
	if held <= 0 {
		t.Fatal("project visibility output has no retained work")
	}
	read.Close()
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 || read.SourceRetainedForTesting() ||
		!reflect.DeepEqual(read.Data, domain.Bootstrap{}) || read.Allowed {
		t.Fatal("project header close retained output/source/work")
	}
	return held
}

func TestScaleP21SW82ProjectVisibilityLiteral(t *testing.T) {
	longID := strings.Repeat("exact-long-id-", 30)
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "organization", URLKey: "logical"},
		Projects: []domain.Project{
			{ID: "slug-collision", SlugID: "target", PreviousSlugIDs: []string{"old-target"}, TeamIDs: []string{"wrong-team"}, Description: strings.Repeat("Unused project body. ", 2000)},
			{ID: "target", Name: "First exact native record", TeamIDs: []string{"private", "public", "private"}, Creator: &domain.User{ID: "hidden"}, LabelIDs: []string{"hidden-label"}},
			{ID: "target", Name: "Later duplicate", TeamIDs: []string{"later-team"}},
			{ID: "", Name: "First empty", TeamIDs: nil},
			{ID: "", Name: "Later empty", TeamIDs: []string{"wrong-empty"}},
			{ID: " spaced ", TeamIDs: []string{}},
			{ID: "nul\x00id", TeamIDs: []string{"team\x00name"}},
			{ID: longID, TeamIDs: []string{"long-id-team"}},
			{ID: "\xff-native", TeamIDs: []string{"invalid-utf8-id-team"}},
			{ID: "nil", TeamIDs: nil},
			{ID: "empty", TeamIDs: []string{}},
		},
	}
	values := map[string]domain.Bootstrap{"physical\x00": data}
	legacy := &SQLiteStore{workspaces: values, lastWorkspaceKey: "physical\x00"}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = "physical\x00"
	for _, workspace := range []string{"physical\x00", ""} {
		for _, id := range []string{"target", "slug-collision", "old-target", "", " spaced ", "spaced", "nul\x00id", longID, "\xff-native", "nil", "empty", "missing", "TARGET"} {
			t.Run(fmt.Sprintf("%q/%q", workspace, id), func(t *testing.T) {
				scaleP21SW82Compare(t, legacy, view, workspace, id)
			})
		}
	}
	for _, workspace := range []string{"logical", "physical", "physical\x00 ", "missing"} {
		for _, repository := range []*SQLiteStore{legacy, view} {
			if read, found, err := repository.BeginProjectVisibilityRead(t.Context(), workspace, "target"); read != nil || found || err != nil {
				t.Fatal("missing physical workspace changed", workspace, found, err)
			}
		}
	}
	off, found, err := legacy.BeginProjectVisibilityRead(t.Context(), "", "target")
	if err != nil || !found || !off.Allowed || !reflect.DeepEqual(off.Data.Projects[0], scaleP21SW82Header(data.Projects[1])) {
		t.Fatal("off reader changed literal first exact header", found, err)
	}
	off.Data.Projects[0].TeamIDs[0] = "caller-owned"
	if legacy.workspaces["physical\x00"].Projects[1].TeamIDs[0] != "private" || off.SourceRetainedForTesting() {
		t.Fatal("off header aliases installed TeamIDs or acquired a bounded source")
	}
	off.Close()
	for _, shape := range [][]domain.Project{nil, {}} {
		emptyView := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"empty": {Projects: shape}})
		read, found, err := emptyView.BeginProjectVisibilityRead(t.Context(), "empty", "")
		if err != nil || !found || read.Allowed || len(read.Data.Projects) != 0 {
			t.Fatal("empty project collection became an empty-ID match", found, err)
		}
		read.Close()
	}
}

func TestScaleP21SW82ProjectVisibilityHandoff(t *testing.T) {
	before := domain.Bootstrap{Projects: []domain.Project{{ID: "target", TeamIDs: []string{"before"}}}}
	after := domain.Bootstrap{Projects: []domain.Project{{ID: "target", TeamIDs: []string{"after"}}}}
	foreign := domain.Bootstrap{Projects: []domain.Project{{ID: "target", TeamIDs: []string{"foreign"}}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before, "foreign": foreign})
	view.lastWorkspaceKey = "physical"
	legacy := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": after, "foreign": foreign}, lastWorkspaceKey: "physical"}
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), "")
	if err != nil || !found {
		t.Fatal("pin project source", found, err)
	}
	ctx, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	view.mu.Lock()
	view.workspaces["physical"] = after
	view.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	read, found, err := view.BeginProjectVisibilityRead(ctx, "", "target")
	want, exists := legacy.ProjectByID("", "target")
	if err != nil || !found || !exists || !read.Allowed || !reflect.DeepEqual(read.Data.Projects[0], scaleP21SW82Header(want)) {
		t.Fatal("stale request pin replaced the current literal project generation", found, err)
	}
	parent.Close()
	cancel()
	if !slices.Equal(read.Data.Projects[0].TeamIDs, []string{"after"}) || !read.SourceRetainedForTesting() || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("child project borrowed parent lifetime")
	}
	read.Data.Projects[0].TeamIDs[0] = "owned-result"
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("project child leaked work")
	}
	fresh, found, err := view.BeginProjectVisibilityRead(t.Context(), "", "target")
	if err != nil || !found || !fresh.Allowed || !slices.Equal(fresh.Data.Projects[0].TeamIDs, []string{"after"}) {
		t.Fatal("fresh project lost its replacement source", found, err)
	}
	fresh.Close()
	foreignParent, found, err := view.BeginInstalledMetadataRead(t.Context(), "foreign")
	if err != nil || !found {
		t.Fatal("pin foreign project workspace", found, err)
	}
	foreignContext, err := foreignParent.Context(t.Context())
	if err != nil {
		foreignParent.Close()
		t.Fatal(err)
	}
	for _, workspace := range []string{"physical", ""} {
		read, found, err := view.BeginProjectVisibilityRead(foreignContext, workspace, "target")
		want, exists := legacy.ProjectByID(workspace, "target")
		if err != nil || !found || !exists || !read.Allowed || !reflect.DeepEqual(read.Data.Projects[0], scaleP21SW82Header(want)) {
			foreignParent.Close()
			t.Fatal("foreign request pin changed current/default project lookup", workspace, found, err)
		}
		read.Close()
	}
	foreignParent.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("foreign request-pin lookup retained work")
	}
	cancelled, done := context.WithCancel(t.Context())
	done()
	if read, _, err := view.BeginProjectVisibilityRead(cancelled, "", "target"); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancelled project read retained output/work", err)
	}
	limit := view.metadataResidency.work.limit
	for _, budget := range []int{1, 128 << 10, 2 << 20} {
		view.metadataResidency.work.limit = budget
		read, _, err := view.BeginProjectVisibilityRead(t.Context(), "", "target")
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("failed project admission retained output/work", budget, err)
		}
	}
	view.metadataResidency.work.limit = limit
	view.metadataResidency.mu.Lock()
	retained := view.metadataResidency.logical["physical"]
	delete(view.metadataResidency.logical, "physical")
	view.metadataResidency.mu.Unlock()
	read, found, err = view.BeginProjectVisibilityRead(t.Context(), "physical", "target")
	view.metadataResidency.mu.Lock()
	view.metadataResidency.logical["physical"] = retained
	view.metadataResidency.mu.Unlock()
	if read != nil || !found || !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("missing installed source became an absent project", found, err)
	}
}

func TestScaleP21SW82ProjectVisibilityLargeHeader(t *testing.T) {
	team := strings.Repeat("complete-large-team-id.", 20000)
	data := domain.Bootstrap{Projects: []domain.Project{
		{ID: "target", TeamIDs: []string{team, "second", team}, Description: strings.Repeat("Ignored body. ", 100000)},
	}}
	legacy := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}}
	view := scaleP21HotLogoStore(t, legacy.workspaces)
	scaleP21SW82Compare(t, legacy, view, "physical", "target")
}

func TestScaleP21SW82ProjectVisibilityWarmReloadBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	held := map[bool]int{}
	for _, size := range []int{2000, 20000} {
		body := strings.Repeat("Unrelated project and document bodies. ", size)
		restore := ForceFullMutationsForTesting(true)
		err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw82_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{
				{ID: "unused", SlugID: "target", TeamIDs: []string{"wrong"}, Description: body},
				{ID: "target", Name: body, Description: body, TeamIDs: []string{"one", "two", "one"}},
				{ID: "trailing", Description: body},
			}
			data.Documents = []domain.Document{{ID: "unrelated", Content: body}}
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				if reload {
					f.mu.Lock()
					f.forgetCopyRevisionLocked(workspace)
					f.mu.Unlock()
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				current := scaleP21SW82Compare(t, f.SQLiteStore, view, workspace, "target")
				if held[reload] == 0 {
					held[reload] = current
				}
				if current != held[reload] {
					t.Fatal("10x unrelated bodies increased exact project header work", current, held[reload])
				}
				t.Logf("body_size=%d reload=%t project_header_held=%d", size, reload, current)
			})
		}
	}
}
