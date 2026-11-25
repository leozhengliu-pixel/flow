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
)

func scaleP21SW92Data(workspace domain.Workspace, repetitions int) domain.Bootstrap {
	body := strings.Repeat("Unrelated complete image body. ", repetitions)
	at, zero := time.Now(), time.Time{}
	return domain.Bootstrap{
		Workspace: workspace, Viewer: domain.User{ID: "viewer", Name: "Native viewer", OutOfOfficeUntil: &at}, ViewerRole: "native-role",
		Labels: []domain.IssueLabel{
			{ID: "shared", Name: "Earlier shared", Description: body, IssueCount: 901},
			{ID: "", Name: "Empty label", IssueCount: 902},
			{ID: "issue-only", Name: "Issue label", IssueCount: 903},
			{ID: "project-only", Name: "Project label", IssueCount: 904},
			{ID: "initiative-only", Name: "Initiative label", IssueCount: 905},
			{ID: "archived-only", Name: "Archived label", IssueCount: 906},
			{ID: " ", Name: "Whitespace is nonempty", IssueCount: 907},
			{ID: "shared", Name: "Last shared", Description: body, IssueCount: 908, LastAppliedAt: &at},
		},
		Projects: []domain.Project{
			{ID: "project", Name: "First project", Description: body, LabelIDs: []string{"shared", "project-only", "project-only", ""}, IssueCount: 801,
				ProgressHistory: []domain.ProjectProgressHistoryPoint{{Date: at, Value: 0.5}}},
			{ID: "archived-project", Name: "Archived project", LabelIDs: []string{"archived-only"}, IssueCount: 802, ArchivedAt: &zero},
			{ID: "project", Name: "Duplicate project", LabelIDs: []string{"shared", "initiative-only"}, IssueCount: 803},
			{ID: "", Name: "Empty project", LabelIDs: []string{"project-only"}, IssueCount: 804},
		},
		Initiatives: []domain.Initiative{
			{ID: "initiative", Name: "First initiative", Description: body, LabelIDs: []string{"initiative-only", "initiative-only", "shared", ""}},
			{ID: "initiative", Name: "Duplicate initiative", LabelIDs: []string{"initiative-only"}},
		},
		Issues: []domain.Issue{
			{ID: "duplicate", Title: "First duplicate", Description: body, Project: &domain.ProjectSummary{ID: "project"},
				Labels: []domain.IssueLabel{{ID: "shared", Name: "Stale shared", IssueCount: 711}, {ID: "shared"}, {ID: ""}, {ID: "issue-only"}, {ID: " "}}},
			{ID: "duplicate", Title: "Second duplicate", Project: &domain.ProjectSummary{ID: "project"},
				Labels: []domain.IssueLabel{{ID: "shared"}, {ID: "unknown", Name: "Unknown remains exact", IssueCount: 712}}},
			{ID: "", Title: "Empty issue ID", Project: &domain.ProjectSummary{ID: ""}, Labels: []domain.IssueLabel{{ID: "project-only"}}},
			{ID: "archived-zero", ArchivedAt: &zero, Project: &domain.ProjectSummary{ID: "project"}, Labels: []domain.IssueLabel{{ID: "archived-only"}, {ID: "shared"}}},
			{ID: "archived-native", ArchivedAt: &at, Project: &domain.ProjectSummary{ID: ""}, Labels: []domain.IssueLabel{{ID: "archived-only"}}},
			{ID: "nil-project-labels", Project: nil, Labels: nil},
			{ID: "empty-labels", Project: &domain.ProjectSummary{ID: "archived-project"}, Labels: []domain.IssueLabel{}},
		},
		Comments:  map[string][]domain.Comment{"nil": nil, "empty": {}, "unrelated": {{ID: "comment", Body: body, CreatedAt: at}}},
		Documents: []domain.Document{{ID: "document", Content: body}},
		Settings:  map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "bytes": []byte{0, 255}, "nil": []string(nil), "empty": []string{}, "time": at}},
	}
}

func scaleP21SW92Compare(t *testing.T, view *SQLiteStore, source *metadataLogicalSource) *metadataLogicalSource {
	t.Helper()
	scope := scaleP21SW72FullScope(t, source)
	before, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	want, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	refreshResourceCounts(&want)
	queries, decodes := 0, view.WorkspaceMetadataDecodes()
	result, err := view.refreshLegacyResourceCountsFromImage(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), source)
	if err != nil || result == nil {
		t.Fatal("complete-image counts failed", err)
	}
	t.Cleanup(result.Release)
	scaleP21SW72Compare(t, result, scope, want)
	scaleP21SW72Compare(t, source, scope, before)
	for index, node := range source.fields {
		name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
		if name != "-" && name != "labels" && name != "projects" && name != "issues" && result.fields[index] != node {
			t.Fatal("image count pass rewrote an unrelated field", name)
		}
	}
	if result.Stamp() != source.Stamp() || queries != 0 ||
		view.WorkspaceMetadataDecodes() != decodes || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("image counts changed generation, read SQL, decoded metadata or retained transient work", queries)
	}
	return result
}

func TestScaleP21SW92LegacyImageCountsLiteral(t *testing.T) {
	for _, variant := range []string{"duplicates-and-native-archive", "nil-directories", "empty-directories", "nil-issues", "empty-issues", "large-unrelated-bodies"} {
		t.Run(variant, func(t *testing.T) {
			data := scaleP21SW92Data(domain.Workspace{ID: "image", URLKey: "image"}, 1000)
			switch variant {
			case "nil-directories":
				data.Labels, data.Projects, data.Initiatives = nil, nil, nil
			case "empty-directories":
				data.Labels, data.Projects, data.Initiatives = []domain.IssueLabel{}, []domain.Project{}, []domain.Initiative{}
			case "nil-issues":
				data.Issues = nil
			case "empty-issues":
				data.Issues = []domain.Issue{}
			case "large-unrelated-bodies":
				data.Issues[0].Description = strings.Repeat("Complete issue body must stay framed. ", 1<<17)
			}
			// A pure complete-image pass must work without a primary database.
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(source.Release)
			result := scaleP21SW92Compare(t, view, source)
			source.Release()
			scope := scaleP21SW72FullScope(t, result)
			want, err := result.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := result.Select(cancelled, scope, 64<<20); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled result read did not cancel", err)
			}
			scaleP21SW72Compare(t, result, scope, want)
			result.Release()
			result.Release()
			if _, err := result.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("released image count result remained selectable", err)
			}
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors ||
				view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("complete image count pass leaked storage or work")
			}
		})
	}
}

func TestScaleP21SW92LegacyImageCountsFrozenSource(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	seed := scaleP21SW92Data(f.data.Workspace, 100)
	f.db.SetMaxOpenConns(4)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "labels", "projects", "initiatives", "documents"), workspace,
		"scale_p21.sw92_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Labels, data.Projects, data.Initiatives, data.Documents = seed.Labels, seed.Projects, seed.Initiatives, seed.Documents
			return nil
		}); err != nil {
		t.Fatal(err)
	}
	for _, reload := range []bool{false, true} {
		t.Run(fmt.Sprintf("reload-%t", reload), func(t *testing.T) {
			f.db.SetMaxOpenConns(4)
			if reload {
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			base, err := f.view.retainMetadataLogicalSource(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer base.Release()
			f.db.SetMaxOpenConns(1)
			tx := f.begin(t)
			f.put(t, tx, workspace, "source-physical", 0,
				[]byte(`{"id":"source-issue","labels":[{"id":"shared"},{"id":"shared"}],"project":{"id":"project"}}`))
			source, err := f.view.prepareLegacyIssueImage(t.Context(), tx, workspace, base)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			// These rows become different after the complete source is frozen.
			// Any primary SQL rescan would either fail JSON decoding or count
			// extra labels/projects that were absent at the callback boundary.
			f.put(t, tx, workspace, "later-physical", 1,
				[]byte(`{"id":"later-issue","labels":[{"id":"shared"},{"id":"project-only"}],"project":{"id":"project"}}`))
			f.put(t, tx, workspace, "later-malformed", 2, []byte(`{"id":`))
			result := scaleP21SW92Compare(t, f.view, source)
			result.Release()
			scaleP21SW70Usable(t, tx)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP21SW92LegacyImageCountsHeaderBound(t *testing.T) {
	for _, size := range []int{1 << 14, 10 << 14} {
		t.Run(fmt.Sprintf("body-%d", size), func(t *testing.T) {
			data := scaleP21SW92Data(domain.Workspace{ID: "bounded-image", URLKey: "bounded-image"}, 100)
			data.Issues[0].Description = strings.Repeat("Ignored complete issue body. ", size)
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			// Reuse SW71's proved working ceiling. Tenfold body growth must
			// not turn one projected issue header into a complete body decode.
			view.metadataResidency.work.limit = 8 << 20
			result := scaleP21SW92Compare(t, view, source)
			result.Release()
		})
	}
}

func TestScaleP21SW92LegacyImageCountsFailureRelease(t *testing.T) {
	view := &SQLiteStore{metadataResidency: newMetadataResidency()}
	data := scaleP21SW92Data(domain.Workspace{ID: "failure-image", URLKey: "failure-image"}, 100)
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := view.refreshLegacyResourceCountsFromImage(cancelled, source)
	if result != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		if result != nil {
			result.Release()
		}
		t.Fatal("canceled count image escaped or leaked", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	result, err = view.refreshLegacyResourceCountsFromImage(t.Context(), source)
	view.metadataResidency.work.limit = limit
	if result != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		if result != nil {
			result.Release()
		}
		t.Fatal("count image admission escaped or leaked", err)
	}
	result = scaleP21SW92Compare(t, view, source)
	result.Release()
	source.Release()
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("failed count image operations retained staging storage")
	}
	if result, err := view.refreshLegacyResourceCountsFromImage(t.Context(), source); result != nil || !errors.Is(err, errMetadataLogicalClosed) {
		if result != nil {
			result.Release()
		}
		t.Fatal("closed source accepted by image count pass", err)
	}
}
