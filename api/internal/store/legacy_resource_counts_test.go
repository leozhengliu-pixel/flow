package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW71LegacyResourceCounts(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw71-counts-")
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	zeroAt := time.Time{}
	physical := []string{prefix + "physical-a", prefix + "physical-b", prefix + "physical-c", prefix + "physical-duplicate"}
	sqlIssues := []domain.Issue{
		{ID: physical[0], Labels: []domain.IssueLabel{{ID: "shared"}, {ID: "shared"}, {ID: ""}, {ID: "issue-only"}, {ID: " "}}, Project: &domain.ProjectSummary{ID: "project"}},
		{ID: physical[1], Labels: []domain.IssueLabel{{ID: "archived-issue-only"}}, Project: &domain.ProjectSummary{ID: "project"}, ArchivedAt: &at},
		{ID: physical[2], Labels: []domain.IssueLabel{{ID: "shared"}}, Project: &domain.ProjectSummary{ID: ""}},
		{ID: physical[3], Labels: []domain.IssueLabel{{ID: "shared"}, {ID: "issue-only"}}, Project: &domain.ProjectSummary{ID: "archived-project"}},
	}
	for index := range sqlIssues {
		sqlIssues[index].Identifier = physical[index]
		sqlIssues[index].Team, sqlIssues[index].Creator, sqlIssues[index].CreatedAt = f.data.Teams[0], f.data.Viewer, at
	}
	if err := f.ImportIssues(t.Context(), workspace, sqlIssues); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	directory := func(repetitions int) domain.Bootstrap {
		body := strings.Repeat("Unrelated directory/history body. ", repetitions)
		return domain.Bootstrap{
			Workspace: f.data.Workspace, Viewer: domain.User{ID: "stored-viewer", Name: "Full native viewer"}, ViewerRole: "unchanged-role",
			Labels: []domain.IssueLabel{
				{ID: "shared", Name: "First shared label", Description: body, IssueCount: 901, Scope: "workspace", Color: "#123456"},
				{ID: "", Name: "Empty label ID", Description: body, IssueCount: 902},
				{ID: "issue-only", Name: "Issue-only label", IssueCount: 903},
				{ID: "project-only", Name: "Project-only label", IssueCount: 904},
				{ID: "initiative-only", Name: "Initiative-only label", IssueCount: 905},
				{ID: "archived-issue-only", Name: "Archived issue only", IssueCount: 906},
				{ID: " ", Name: "Whitespace is not empty", IssueCount: 907},
				{ID: "shared", Name: "Later shared label", Description: body, IssueCount: 908, ResourceType: "project"},
			},
			Projects: []domain.Project{
				{ID: "project", Name: "First project", Description: body, LabelIDs: []string{"shared", "project-only", "project-only", ""}, IssueCount: 801, Progress: 0.6,
					Resources: []domain.ProjectResource{{ID: "resource", URL: "https://example.test/resource"}}, ProgressHistory: []domain.ProjectProgressHistoryPoint{{Date: at}}},
				{ID: "archived-project", Name: "Archived project", Description: body, LabelIDs: []string{"archived-project-only"}, IssueCount: 802, ArchivedAt: &zeroAt},
				{ID: "project", Name: "Later duplicate project", Description: body, LabelIDs: []string{"shared", "initiative-only"}, IssueCount: 803},
				{ID: "", Name: "Empty project ID", LabelIDs: []string{"project-only"}, IssueCount: 804},
			},
			Initiatives: []domain.Initiative{
				{ID: "initiative", Name: "First initiative", Description: body, LabelIDs: []string{"initiative-only", "initiative-only", "shared", ""}},
				{ID: "initiative", Name: "Duplicate initiative occurrence", LabelIDs: []string{"initiative-only"}},
			},
			Issues: []domain.Issue{
				{ID: "installed-issue-must-not-drive-counts", Title: body, Version: 71, Labels: []domain.IssueLabel{{ID: "shared", Name: "stale shared"}, {ID: "", Name: "stale empty"}, {ID: "not-in-catalog", Name: "preserved unknown", IssueCount: 909}}, Project: &domain.ProjectSummary{ID: "project"}},
				{ID: "archived-source-issue", Labels: []domain.IssueLabel{{ID: "shared"}}, ArchivedAt: &at},
				{ID: "nil-source-labels", Labels: nil},
				{ID: "empty-source-labels", Labels: []domain.IssueLabel{}},
			},
			Documents: []domain.Document{{ID: "unrelated-document", Content: body}},
			Settings:  map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "nil": []string(nil), "empty": []string{}}},
		}
	}
	begin := func(t *testing.T) (*sqlTx, context.Context) {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
		// Preserve physical keys while deliberately duplicating decoded JSON IDs.
		for index, record := range sqlIssues {
			record.ID = "json-a"
			if index == 1 {
				record.ID = "json-archived"
			}
			values, err := f.issueRecordColumns(workspace, record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET data=?,collection_order=? WHERE workspace_key=? AND id=?`, values[len(values)-1], 3-index, workspace, physical[index]); err != nil {
				t.Fatal("replace fixture JSON", err)
			}
		}
		if f.dialect == "sqlite" {
			// Fill the normal inline journal frontier with text-keyed rows so
			// the BLOB witness uses the ordinary non-inline dirty-record path.
			for range 16 {
				id := idgen.New(prefix + "journal-fill-")
				values, err := issueRecordValues(workspace, domain.Issue{ID: id, Identifier: id, Team: f.data.Teams[0], Creator: f.data.Viewer, CreatedAt: at})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := tx.ExecContext(ctx, issueRecordInsert+issueRecordValuesSQL, values...); err != nil {
					t.Fatal("fill journal frontier with count-neutral issues", err)
				}
			}
			var inline int
			if err := tx.QueryRowContext(ctx, `SELECT json_array_length(keys)+json_array_length(events) FROM record_journal_context WHERE id=1`).Scan(&inline); err != nil || inline < 16 {
				t.Fatal("BLOB fixture requires the normal non-inline journal path", inline, err)
			}
			blob := sqlIssues[0]
			blob.ID, blob.Identifier, blob.Labels = "json-blob", prefix+"blob-identifier", []domain.IssueLabel{{ID: "project-only"}}
			full, err := f.issueRecordColumns(workspace, blob)
			if err != nil {
				t.Fatal(err)
			}
			values, err := issueRecordValues(workspace, domain.Issue{ID: blob.Identifier, Identifier: blob.Identifier, Title: "SQL index title"})
			if err != nil {
				t.Fatal(err)
			}
			values[1], values[len(values)-1] = []byte(physical[0]), full[len(full)-1]
			if _, err := tx.ExecContext(ctx, issueRecordInsert+issueRecordValuesSQL, values...); err != nil {
				t.Fatal("insert physical BLOB fixture", err)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE issue_records SET collection_order=3 WHERE workspace_key=? AND id=?`, workspace, []byte(physical[0])); err != nil {
				t.Fatal("order physical BLOB fixture", err)
			}
		}
		return tx, ctx
	}
	literal := func(t *testing.T, ctx context.Context, tx *sqlTx, before domain.Bootstrap, overrides map[legacyIssueKey]*domain.Issue) domain.Bootstrap {
		t.Helper()
		loaded, err := f.readIssueRecordsWithBase(ctx, workspace, newRecordBase())
		if err != nil {
			t.Fatal(err)
		}
		storage := "0"
		if f.dialect == "sqlite" {
			storage = "typeof(id)='blob'"
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,`+storage+` FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		keys := []legacyIssueKey{}
		for rows.Next() {
			var key legacyIssueKey
			if err := rows.Scan(&key.ID, &key.Blob); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if len(keys) != len(loaded) {
			t.Fatal("literal physical-key and decoded row orders diverged")
		}
		next, seen := cloneBootstrap(before), map[legacyIssueKey]bool{}
		next.Issues = []domain.Issue{}
		for index, record := range loaded {
			if replacement, exists := overrides[keys[index]]; exists {
				seen[keys[index]] = true
				if replacement != nil {
					next.Issues = append(next.Issues, *replacement)
				}
			} else {
				next.Issues = append(next.Issues, record)
			}
		}
		for key, replacement := range overrides {
			if !seen[key] && replacement != nil {
				next.Issues = append(next.Issues, *replacement)
			}
		}
		refreshResourceCounts(&next)
		labels := make(map[string]domain.IssueLabel, len(next.Labels))
		for _, label := range next.Labels {
			labels[label.ID] = label
		}
		next.Issues = cloneBootstrap(before).Issues
		for index := range next.Issues {
			for labelIndex, label := range next.Issues[index].Labels {
				if refreshed, found := labels[label.ID]; found {
					next.Issues[index].Labels[labelIndex] = refreshed
				}
			}
		}
		return next
	}
	check := func(t *testing.T, ctx context.Context, tx *sqlTx, source *metadataLogicalSource, overrides map[legacyIssueKey]*domain.Issue) *metadataLogicalSource {
		t.Helper()
		scope := MetadataRecordScope{Fields: []string{"labels", "projects", "initiatives", "issues", "settings"}}
		before, err := source.Select(t.Context(), scope, 64<<20)
		if err != nil {
			t.Fatal(err)
		}
		want := literal(t, ctx, tx, before, overrides)
		result, err := view.refreshLegacyResourceCounts(ctx, tx, workspace, source, overrides)
		if err != nil || result == nil {
			t.Fatal("legacy resource count refresh failed", err)
		}
		got, err := result.Select(t.Context(), scope, 64<<20)
		if err != nil || !reflect.DeepEqual(got.Labels, want.Labels) || !reflect.DeepEqual(got.Projects, want.Projects) || !reflect.DeepEqual(got.Issues, want.Issues) {
			result.Release()
			t.Fatal("literal complete labels/project count or embedded issue label images differ", err)
		}
		for index := range source.fields {
			name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
			if name != "-" && name != "labels" && name != "projects" && name != "issues" && source.fields[index] != result.fields[index] {
				result.Release()
				t.Fatal("count refresh changed another source field", name)
			}
		}
		if result.Stamp() != source.Stamp() {
			result.Release()
			t.Fatal("count refresh changed the installed stamp")
		}
		unchanged, err := source.Select(t.Context(), scope, 64<<20)
		if err != nil || !reflect.DeepEqual(unchanged.Labels, before.Labels) || !reflect.DeepEqual(unchanged.Projects, before.Projects) || !reflect.DeepEqual(unchanged.Issues, before.Issues) {
			result.Release()
			t.Fatal("count refresh mutated the input directory", err)
		}
		return result
	}
	for _, variant := range []string{"baseline", "replace-physical", "delete-physical", "replace-blob", "delete-blob", "append-absent", "json-id-is-absent-key", "archive-replacement", "mixed", "nil-directories", "empty-directories", "nil-source-issues", "empty-source-issues"} {
		t.Run(variant, func(t *testing.T) {
			if f.dialect != "sqlite" && (variant == "replace-blob" || variant == "delete-blob") {
				t.Skip("SQLite physical TEXT/BLOB identity witness")
			}
			data := directory(1000)
			if variant == "nil-directories" {
				data.Labels, data.Projects = nil, nil
			} else if variant == "empty-directories" {
				data.Labels, data.Projects = []domain.IssueLabel{}, []domain.Project{}
			} else if variant == "nil-source-issues" {
				data.Issues = nil
			} else if variant == "empty-source-issues" {
				data.Issues = []domain.Issue{}
			}
			created := &domain.Issue{ID: "json-a", Labels: []domain.IssueLabel{{ID: "shared"}, {ID: "shared"}, {ID: "initiative-only"}, {ID: ""}}, Project: &domain.ProjectSummary{ID: ""}}
			overrides := map[legacyIssueKey]*domain.Issue{}
			switch variant {
			case "replace-physical":
				overrides[legacyIssueKey{ID: physical[0]}] = created
			case "delete-physical":
				overrides[legacyIssueKey{ID: physical[0]}] = nil
			case "replace-blob":
				overrides[legacyIssueKey{ID: physical[0], Blob: true}] = created
			case "delete-blob":
				overrides[legacyIssueKey{ID: physical[0], Blob: true}] = nil
			case "append-absent":
				overrides[legacyIssueKey{ID: "new-physical"}] = created
			case "json-id-is-absent-key":
				overrides[legacyIssueKey{ID: "json-a"}] = created
			case "archive-replacement":
				archived := *created
				archived.ArchivedAt = &at
				overrides[legacyIssueKey{ID: physical[0]}] = &archived
			case "mixed":
				overrides[legacyIssueKey{ID: physical[1]}], overrides[legacyIssueKey{ID: physical[2]}], overrides[legacyIssueKey{ID: "new-physical"}], overrides[legacyIssueKey{ID: "missing-nil"}] = created, nil, created, nil
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			tx, ctx := begin(t)
			result := check(t, ctx, tx, source, overrides)
			result.Release()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("resource count refresh leaked work or decoded metadata")
			}
			var one int
			if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
				t.Fatal("resource counts consumed the borrowed transaction", err)
			}
		})
	}
	t.Run("warm-reloaded-and-10x-unrelated-bodies", func(t *testing.T) {
		for _, size := range []int{2000, 20000} {
			seed := directory(size)
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "labels", "projects", "initiatives", "documents"), workspace, "scale_p21.sw71_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Labels, data.Projects, data.Initiatives, data.Documents = seed.Labels, seed.Projects, seed.Initiatives, seed.Documents
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
				source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer source.Release()
					tx, ctx := begin(t)
					defer tx.Rollback()
					budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
					budget.limit = 8 << 20
					defer func() { budget.limit = limit }()
					result := check(t, ctx, tx, source, map[legacyIssueKey]*domain.Issue{{ID: "new-physical"}: {ID: "new-json", Labels: []domain.IssueLabel{{ID: "shared"}}, Project: &domain.ProjectSummary{ID: "project"}}})
					result.Release()
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("body growth leaked process work", size, reload)
					}
				}()
			}
		}
	})
	t.Run("cancel-release-and-borrowed-source", func(t *testing.T) {
		data := directory(1000)
		source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: f.data.Workspace.ID, revision: -1})
		if err != nil {
			t.Fatal(err)
		}
		tx, ctx := begin(t)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if result, err := view.refreshLegacyResourceCounts(canceled, tx, workspace, source, nil); result != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			source.Release()
			t.Fatal("canceled count refresh escaped or leaked", err)
		}
		budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
		budget.limit = 1
		result, err := view.refreshLegacyResourceCounts(ctx, tx, workspace, source, nil)
		budget.limit = limit
		if result != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			source.Release()
			t.Fatal("count refresh admission failed to clean up", err)
		}
		result = check(t, ctx, tx, source, nil)
		source.Release()
		if _, err := result.Select(t.Context(), MetadataRecordScope{Fields: []string{"labels", "projects"}}, 64<<20); err != nil {
			result.Release()
			t.Fatal("returned count source borrowed the caller source lifetime", err)
		}
		result.Release()
		result.Release()
		if _, err := result.Select(t.Context(), MetadataRecordScope{}, 2<<20); !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("released count source remained usable or retained work", err)
		}
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("failed count operations closed borrowed tx", err)
		}
	})
	t.Run("mismatched-source-workspace", func(t *testing.T) {
		source, err := captureMetadataLogicalSource(t.Context(), directory(1), copyStamp{workspaceID: "different-workspace", revision: -1})
		if err != nil {
			t.Fatal(err)
		}
		defer source.Release()
		tx, ctx := begin(t)
		result, err := view.refreshLegacyResourceCounts(ctx, tx, workspace, source, nil)
		if result != nil {
			result.Release()
		}
		if err == nil || result != nil || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("resource counts accepted a different source workspace or leaked", err)
		}
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("workspace refusal closed borrowed tx", err)
		}
	})
}
