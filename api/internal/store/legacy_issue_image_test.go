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
)

func scaleP21SW72FullScope(t *testing.T, source *metadataLogicalSource) MetadataRecordScope {
	t.Helper()
	scope := MetadataRecordScope{}
	if err := source.VisitFields(t.Context(), func(name string, _ metadataLogicalNode) error {
		if name != "-" {
			scope.Fields = append(scope.Fields, name)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return scope
}

func scaleP21SW72Want(t *testing.T, f scaleP21SW70Fixture, tx *sqlTx, source *metadataLogicalSource, scope MetadataRecordScope) (domain.Bootstrap, domain.Bootstrap) {
	t.Helper()
	before, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	workspace := f.data.Workspace.URLKey
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
	issues, err := f.readIssueRecordsWithBase(ctx, workspace, newRecordBase())
	if err != nil {
		t.Fatal(err)
	}
	want := before
	want.Issues = issues
	return before, want
}

func scaleP21SW72Compare(t *testing.T, source *metadataLogicalSource, scope MetadataRecordScope, want domain.Bootstrap) {
	t.Helper()
	got, err := source.Select(t.Context(), scope, 64<<20)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("full logical image differs from source fields plus literal SQL issues", err)
	}
}

func TestScaleP21SW72LegacyIssueImageNative(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	nativeTime := time.Now()
	storedTime := time.Date(2026, 11, 14, 1, 2, 3, 400, time.FixedZone("stored", 5400))
	nativeStoredTime := storedTime.UTC()
	sqlIssue := domain.Issue{ID: "semantic-duplicate", Title: "SQL <title>\x00\u51c6", Description: strings.Repeat("Complete legal SQL issue body. ", 1<<14),
		Team: domain.Team{ID: "sql-team", Name: "SQL embedded team"}, Creator: domain.User{ID: "sql-user", Name: "SQL embedded creator"},
		State: domain.WorkflowState{ID: "sql-state", Name: "SQL embedded state"}, Version: 0,
		Labels:    []domain.IssueLabel{{ID: "sql-label", Name: "Complete SQL label", Description: "Full description"}},
		Reactions: map[string][]string{"like": {"actor", "actor"}}, SubscriberIDs: []string{"one", "one"},
		Relations:   []domain.IssueRelation{{ID: "relation", RelatedIssueID: "other", Type: "blocks"}},
		Attachments: []domain.Attachment{{ID: "attachment", Title: "Full attachment"}}, CreatedAt: storedTime, UpdatedAt: storedTime}
	raw, err := json.Marshal(sqlIssue)
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []string{"nil", "empty", "stale-duplicates"} {
		for _, populated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/sql-populated-%t", shape, populated), func(t *testing.T) {
				data := domain.Bootstrap{Workspace: f.data.Workspace, ViewerRole: "native-role",
					Viewer:    domain.User{ID: "native-viewer", Name: "Native viewer", OutOfOfficeUntil: &nativeTime},
					Users:     []domain.User{{ID: "sql-user", Name: "Different native directory name", OutOfOfficeUntil: &nativeStoredTime}},
					Teams:     []domain.Team{{ID: "sql-team", Name: "Different native team name"}},
					Projects:  []domain.Project{{ID: "project", Name: "Native project", IssueCount: 777}},
					Documents: []domain.Document{{ID: "unrelated-document", Content: "Native body"}},
					Comments:  map[string][]domain.Comment{"native-issue": {{ID: "native-comment", Body: "Unchanged native comment", User: f.data.Viewer, CreatedAt: nativeTime}}},
					Settings:  map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "bytes": []byte{0, 255}, "nil": []string(nil), "empty": []string{}, "time": nativeTime}},
				}
				if shape == "empty" {
					data.Issues = []domain.Issue{}
				} else if shape == "stale-duplicates" {
					data.Issues = []domain.Issue{{ID: "native-stale", Description: "Old source issue"}, {ID: "native-stale", Title: "Later native duplicate"}}
				}
				source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				scope := scaleP21SW72FullScope(t, source)
				tx := f.begin(t)
				if populated {
					f.put(t, tx, workspace, "physical-z", 4, raw)
					f.put(t, tx, workspace, "physical-a", -2, []byte(`{"id":"semantic-duplicate","title":"First physical row","version":0,"labels":null}`))
					f.put(t, tx, workspace, "physical-m", 4, []byte(" \nnull\t"))
				}
				before, want := scaleP21SW72Want(t, f, tx, source, scope)
				image, err := f.view.prepareLegacyIssueImage(t.Context(), tx, workspace, source)
				if err != nil || image == nil {
					t.Fatal("complete issue image failed", err)
				}
				defer image.Release()
				if image.Stamp() != source.Stamp() {
					t.Fatal("SQL issue replacement changed the supplied metadata generation")
				}
				scaleP21SW72Compare(t, image, scope, want)
				scaleP21SW72Compare(t, source, scope, before)
				if want.Issues == nil || len(want.Issues) != map[bool]int{false: 0, true: 3}[populated] {
					t.Fatal("literal SQL nil/empty/count fixture changed", populated, len(want.Issues))
				}
				for _, name := range scope.Fields {
					if name == "issues" {
						continue
					}
					index, _, _ := metadataLogicalField(name)
					if image.fields[index] != source.fields[index] {
						t.Fatal("issue image rewrote an unrelated source field", name)
					}
				}
				if f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("completed issue image retained transient row work or decoded complete metadata")
				}
				scaleP21SW70Usable(t, tx)
				source.Release()
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				scaleP21SW72Compare(t, image, scope, want)
				image.Release()
				if _, err := image.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) {
					t.Fatal("released issue image remained selectable", err)
				}
			})
		}
	}
}

func TestScaleP21SW72LegacyIssueImageWarmReloadBodyBound(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	peaks := map[bool]int{}
	for _, size := range []int{2000, 20000} {
		f.db.SetMaxOpenConns(4)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents", "settings"), workspace, "scale_p21.sw72_seed", "", nil, func(data *domain.Bootstrap) error {
			unused := strings.Repeat("Unrelated full installed document/settings body. ", size)
			data.Documents = []domain.Document{{ID: "unrelated", Content: unused}}
			data.Settings["sw72-unrelated"] = unused
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				f.db.SetMaxOpenConns(4)
				if reload {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
					t.Fatal(err)
				}
				source, err := f.view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				scope := scaleP21SW72FullScope(t, source)
				f.db.SetMaxOpenConns(1)
				tx := f.begin(t)
				f.put(t, tx, workspace, "physical-current", 0, []byte(`{"id":"semantic-current","title":"Complete SQL title","description":"Fixed SQL body","reactions":{"like":["actor"]}}`))
				before, want := scaleP21SW72Want(t, f, tx, source, scope)
				peak, writes := 0, 0
				ctx := WithSQLStatementObserver(t.Context(), func(query string) {
					peak = max(peak, f.view.MetadataWorkUsedForTesting())
					upper := strings.ToUpper(strings.TrimSpace(query))
					if strings.HasPrefix(upper, "INSERT ") || strings.HasPrefix(upper, "UPDATE ") || strings.HasPrefix(upper, "DELETE ") {
						writes++
					}
				})
				image, err := f.view.prepareLegacyIssueImage(ctx, tx, workspace, source)
				if err != nil || image == nil {
					t.Fatal(err)
				}
				defer image.Release()
				scaleP21SW72Compare(t, image, scope, want)
				scaleP21SW72Compare(t, source, scope, before)
				if peak == 0 || writes != 0 || f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("issue image skipped admission, wrote SQL, retained transient work or hydrated metadata", peak, writes)
				}
				if peaks[reload] == 0 {
					peaks[reload] = peak
				}
				if peak != peaks[reload] {
					t.Fatal("10x unrelated source bodies changed SQL-image admitted work", reload, peak, peaks[reload])
				}
				t.Logf("source_body_size=%d reload=%t constructor_sql_peak=%d", size, reload, peak)
				scaleP21SW70Usable(t, tx)
			})
		}
	}
}

func TestScaleP21SW72LegacyIssueImageFailureRelease(t *testing.T) {
	f := scaleP21SW70Setup(t)
	workspace := f.data.Workspace.URLKey
	for _, failure := range []string{"cancel-before", "cancel-after-start", "malformed-sql", "quota", "foreign-source"} {
		t.Run(failure, func(t *testing.T) {
			data := domain.Bootstrap{Workspace: f.data.Workspace, Viewer: f.data.Viewer,
				Issues: []domain.Issue{{ID: "native-before"}}, Settings: map[string]any{"unchanged": int64(7)}}
			if failure == "foreign-source" {
				data.Workspace.ID += "-foreign"
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			scope := scaleP21SW72FullScope(t, source)
			before, err := source.Select(t.Context(), scope, 64<<20)
			if err != nil {
				t.Fatal(err)
			}
			tx := f.begin(t)
			f.put(t, tx, workspace, "physical-first", 0, []byte(`{"id":"semantic-first","description":"Complete first row"}`))
			next := []byte(`{"id":"semantic-next"}`)
			if failure == "malformed-sql" {
				next = []byte(`{"id":`)
			}
			f.put(t, tx, workspace, "physical-next", 1, next)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var want error
			if failure == "cancel-before" {
				cancel()
				want = context.Canceled
			} else if failure == "cancel-after-start" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.Contains(strings.ToLower(query), "from issue_records") {
						cancel()
					}
				})
				want = context.Canceled
			} else if failure == "malformed-sql" {
				literalCtx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: f.db, workspace: workspace, reader: tx})
				_, want = f.readIssueRecordsWithBase(literalCtx, workspace, newRecordBase())
				if want == nil {
					t.Fatal("malformed SQL fixture did not fail in literal reader")
				}
			} else if failure == "foreign-source" {
				want = ErrMetadataRecordQuery
			} else {
				want = ErrMetadataWorkBudget
			}
			budget, limit := f.view.metadataResidency.work, f.view.metadataResidency.work.limit
			if failure == "quota" {
				budget.limit = 1
			}
			defer func() { budget.limit = limit }()
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			image, err := f.view.prepareLegacyIssueImage(ctx, tx, workspace, source)
			budget.limit = limit
			if image != nil {
				image.Release()
				t.Fatal("failed issue image returned a partial source", failure)
			}
			if failure == "malformed-sql" {
				if reflect.TypeOf(err) != reflect.TypeOf(want) || err.Error() != want.Error() {
					t.Fatal("image changed the literal SQL decoding error", err, want)
				}
			} else if !errors.Is(err, want) {
				t.Fatal("image changed cancellation/admission error", failure, err)
			}
			if f.view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("failed issue image retained row work, candidate storage or descriptors", failure)
			}
			scaleP21SW72Compare(t, source, scope, before)
			scaleP21SW70Usable(t, tx)
		})
	}
}
