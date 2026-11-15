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

func scaleP21SW78Fixture(t *testing.T) (scaleP09Fixture, *SQLiteStore) {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	f.db.SetMaxOpenConns(1)
	return f, view
}

func scaleP21SW78Data(f scaleP09Fixture, repetitions int) domain.Bootstrap {
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	body := strings.Repeat("Known unchanged count-write body. ", repetitions)
	user := f.data.Viewer
	user.Name, user.DisplayName, user.JobTitle = "Directory baseline", "Directory display", "Small reference body"
	lead, creator := user, user
	lead.Name, creator.Name = "Native stale lead", "Native stale creator"
	return domain.Bootstrap{
		Workspace: f.data.Workspace, Viewer: f.data.Viewer, ViewerRole: "unchanged-role",
		Users: []domain.User{user, {ID: "sw78-unrelated-user", Name: "Unrelated native user", JobTitle: body}},
		Labels: []domain.IssueLabel{
			{ID: "sw78-z-label", Name: "First unchanged label", Description: body, IssueCount: 11},
			{ID: "sw78-a-label", Name: "Changed label", Description: "Small changed label body", Scope: "workspace", Color: "#123456", IssueCount: 12},
			{ID: "sw78-m-label", Name: "Last unchanged label", Description: body, IssueCount: 13},
		},
		Projects: []domain.Project{
			{ID: "sw78-z-project", Name: "First unchanged project", Description: body, IssueCount: 21},
			{ID: "sw78-a-project", Name: "Changed project", Description: "Small changed project body", IssueCount: 22, Lead: &lead, Creator: &creator,
				LabelIDs: []string{"sw78-a-label"}, Resources: []domain.ProjectResource{{ID: "native-resource", URL: "https://example.test/resource"}},
				ProgressHistory: []domain.ProjectProgressHistoryPoint{{Date: at, Value: 0.75}}, CreatedAt: at, UpdatedAt: at},
			{ID: "sw78-m-project", Name: "Last unchanged project", Description: body, IssueCount: 23},
		},
		Issues:    []domain.Issue{{ID: "transient-issue", Labels: []domain.IssueLabel{{ID: "sw78-a-label", IssueCount: 12}}}},
		Comments:  map[string][]domain.Comment{"native": {{ID: "unchanged-comment", Body: "Native discussion body"}}},
		Documents: []domain.Document{{ID: "sw78-unrelated-document", Content: body}},
		Settings:  map[string]any{"native": map[string]any{"integer": int64(9007199254740993), "bytes": []byte{0, 255}, "nil": []string(nil), "empty": []string{}, "time": at}},
	}
}

func scaleP21SW78Capture(t *testing.T, data domain.Bootstrap) *metadataLogicalSource {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{workspaceID: data.Workspace.ID, revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	return source
}

func scaleP21SW78Peer(t *testing.T, tx *sqlTx, workspace, mode string, before domain.Bootstrap) {
	t.Helper()
	put := func(field, id string, value any, order int, unknown bool) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if unknown {
			var members map[string]json.RawMessage
			if err := json.Unmarshal(raw, &members); err != nil {
				t.Fatal(err)
			}
			members["peerUnknown"] = json.RawMessage(`{"keep":"canonical unknown member"}`)
			raw, err = json.Marshal(members)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := writeMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{field, id}, metadataRecord{data: raw, order: order}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "malformed-unchanged-rows" {
		for _, row := range [][2]string{{"labels", before.Labels[2].ID}, {"projects", before.Projects[2].ID}} {
			raw, err := json.Marshal(map[string]any{"id": row[1], "name": "Untouched canonical row", "issueCount": "invalid-native-count"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, raw, workspace, row[0], row[1]); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if mode == "gapped-order" || mode == "unknown-canonical" {
		for index := range before.Labels {
			if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field='labels' AND record_key=?`, 17+index*31, workspace, before.Labels[index].ID); err != nil {
				t.Fatal(err)
			}
		}
		for index := range before.Projects {
			if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET collection_order=? WHERE workspace_key=? AND field='projects' AND record_key=?`, 23+index*41, workspace, before.Projects[index].ID); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "gapped-order" {
			return
		}
		put("labels", "sw78-peer-only-label", domain.IssueLabel{ID: "sw78-peer-only-label", Name: "Unknown canonical label", IssueCount: 71}, -13, true)
		put("projects", "sw78-peer-only-project", domain.Project{ID: "sw78-peer-only-project", Name: "Unknown canonical project", IssueCount: 72}, -17, true)
		var raw []byte
		if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		root["sw78-peer-root"] = json.RawMessage(`{"keep":"unknown root member"}`)
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode == "deleted-label" || mode == "deleted-project" || mode == "deleted-both" {
		if mode != "deleted-project" {
			if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"labels", before.Labels[1].ID}); err != nil {
				t.Fatal(err)
			}
		}
		if mode != "deleted-label" {
			if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projects", before.Projects[1].ID}); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if mode == "canonical-merged" || mode == "project-count-conflict" {
		label := before.Labels[1]
		label.Name, label.Description = "Canonical label name", "Canonical label body"
		put("labels", label.ID, label, 73, true)
		project := cloneSnapshotValue(reflect.ValueOf(before.Projects[1])).Interface().(domain.Project)
		project.Name, project.Description = "Canonical project name", "Canonical project body"
		if mode == "project-count-conflict" {
			project.IssueCount += 3
		}
		put("projects", project.ID, project, 79, true)
		return
	}
	if mode == "display-refs" {
		project := cloneSnapshotValue(reflect.ValueOf(before.Projects[1])).Interface().(domain.Project)
		project.Lead.Name, project.Creator.Name = "Canonical stale lead", "Canonical stale creator"
		project.Lead.JobTitle, project.Creator.JobTitle = "Canonical lead body", "Canonical creator body"
		put("projects", project.ID, project, 83, false)
	}
}

func scaleP21SW78Compare(t *testing.T, f scaleP09Fixture, view *SQLiteStore, before, after, directory *metadataLogicalSource, peer string, releaseInput func()) int {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	scope := scaleP21SW72FullScope(t, after)
	base, err := before.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	next, err := after.Select(t.Context(), scope, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	users, err := directory.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	old, target := cloneBootstrap(base), cloneBootstrap(next)
	old.Users, target.Users = users.Users, users.Users
	tx, ctx := scaleP21SW77Begin(t, f, base)
	scaleP21SW78Peer(t, tx, workspace, peer, base)
	preimage := scaleP21SW76MetadataImage(t, tx, workspace)
	_, wantErr := f.writeWorkspaceMetadataDelta(ctx, tx, workspace, old, &target)
	wantSQL := scaleP21SW76MetadataImage(t, tx, workspace)
	scaleP21SW70Usable(t, tx)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, ctx = scaleP21SW77Begin(t, f, base)
	scaleP21SW78Peer(t, tx, workspace, peer, base)
	if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, preimage) {
		t.Fatal("literal and count writes did not start from the same SQL preimage")
	}
	peak := 0
	ctx, cancel := context.WithCancel(WithSQLStatementObserver(ctx, func(string) { peak = max(peak, view.MetadataWorkUsedForTesting()) }))
	defer cancel()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	result, gotErr := view.persistLegacyResourceCounts(ctx, tx, workspace, before, after, directory)
	if reflect.TypeOf(wantErr) != reflect.TypeOf(gotErr) || wantErr != nil && wantErr.Error() != gotErr.Error() {
		if result != nil {
			result.Release()
		}
		t.Fatalf("literal/count write errors differ: %T %v / %T %v", wantErr, wantErr, gotErr, gotErr)
	}
	if wantKeys, want := conflictedMetadataKeys(wantErr); want {
		if gotKeys, got := conflictedMetadataKeys(gotErr); !got || !reflect.DeepEqual(gotKeys, wantKeys) {
			t.Fatal("count conflict keys differ", gotKeys, wantKeys)
		}
	}
	if got := scaleP21SW76MetadataImage(t, tx, workspace); !reflect.DeepEqual(got, wantSQL) {
		if result != nil {
			result.Release()
		}
		t.Fatal("full canonical root, metadata rows or collection orders differ")
	}
	scaleP21SW72Compare(t, before, scope, base)
	scaleP21SW72Compare(t, after, scope, next)
	stillUsers, err := directory.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 64<<20)
	if err != nil || !reflect.DeepEqual(stillUsers, users) {
		t.Fatal("count persistence changed its reference directory", err)
	}
	scaleP21SW70Usable(t, tx)
	if wantErr != nil {
		if result != nil {
			result.Release()
			t.Fatal("failed count write exposed a partial native postimage")
		}
		if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
			t.Fatal("failed count write leaked")
		}
		return peak
	}
	if result == nil {
		t.Fatal("count write omitted its native postimage")
	}
	t.Cleanup(result.Release)
	want := next
	want.Labels, want.Projects = target.Labels, target.Projects
	scaleP21SW72Compare(t, result, scope, want)
	for index, node := range after.fields {
		name := reflect.TypeFor[domain.Bootstrap]().Field(index).Tag.Get("json")
		if name != "-" && name != "labels" && name != "projects" && result.fields[index] != node {
			t.Fatal("count write changed another native source field", name)
		}
	}
	if result.Stamp() != after.Stamp() || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("count write changed generation or decoded workspace metadata")
	}
	releaseInput()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	cancel()
	scaleP21SW72Compare(t, result, scope, want)
	result.Release()
	result.Release()
	if _, err := result.Select(t.Context(), scope, 64<<20); !errors.Is(err, errMetadataLogicalClosed) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("released count postimage remained usable or retained work", err)
	}
	return peak
}

func TestScaleP21SW78LegacyCountWriteLiteral(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	for _, test := range []struct{ name, shape, peer string }{
		{name: "unchanged"}, {name: "unchanged-with-canonical-peer", peer: "canonical-merged"}, {name: "unchanged-with-canonical-deletion", peer: "deleted-both"},
		{name: "labels-only"}, {name: "projects-only"}, {name: "both"},
		{name: "source-order-and-gaps", peer: "gapped-order"},
		{name: "unknown-canonical-rows-and-root", peer: "unknown-canonical"},
		{name: "canonical-merged", peer: "canonical-merged"},
		{name: "untouched-canonical-rows-not-decoded", peer: "malformed-unchanged-rows"},
		{name: "late-project-count-conflict", peer: "project-count-conflict"},
		{name: "canonical-deleted-label", peer: "deleted-label"},
		{name: "canonical-deleted-project", peer: "deleted-project"},
		{name: "canonical-deleted-both", peer: "deleted-both"},
		{name: "callback-directory-display-refs", peer: "display-refs"},
		{name: "duplicate-inline", shape: "duplicate"}, {name: "empty-id-inline", shape: "empty-id"}, {name: "long-id-inline", shape: "long-id"},
		{name: "nil-directories", shape: "nil"}, {name: "empty-directories", shape: "empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := scaleP21SW78Data(f, 1000)
			switch test.shape {
			case "duplicate":
				data.Labels[1].ID, data.Projects[1].ID = data.Labels[0].ID, data.Projects[0].ID
			case "empty-id":
				data.Labels[1].ID, data.Projects[1].ID = "", ""
			case "long-id":
				data.Labels[1].ID, data.Projects[1].ID = strings.Repeat("label", 40), strings.Repeat("project", 30)
			case "nil":
				data.Labels, data.Projects = nil, nil
			case "empty":
				data.Labels, data.Projects = []domain.IssueLabel{}, []domain.Project{}
			}
			next := cloneBootstrap(data)
			if !strings.HasPrefix(test.name, "unchanged-with") && test.name != "unchanged" && len(next.Labels) > 1 {
				if test.name != "projects-only" {
					next.Labels[1].IssueCount++
				}
				if test.name != "labels-only" {
					next.Projects[1].IssueCount++
				}
				next.Issues[0].Labels[0].IssueCount = next.Labels[1].IssueCount
			}
			ref := cloneBootstrap(data)
			if test.peer == "display-refs" {
				first, last := ref.Users[0], ref.Users[0]
				first.Name, last.Name = "Earlier duplicate reference", "Callback-final last reference"
				last.JobTitle = "Callback-final reference body"
				ref.Users = []domain.User{first, last}
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			before, after, directory := scaleP21SW78Capture(t, data), scaleP21SW78Capture(t, next), scaleP21SW78Capture(t, ref)
			scaleP21SW78Compare(t, f, view, before, after, directory, test.peer, func() { before.Release(); after.Release(); directory.Release() })
			before.Release()
			after.Release()
			directory.Release()
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("count postimage or released inputs leaked storage")
			}
		})
	}
}

func TestScaleP21SW78LegacyCountWriteWarmReloadBodyBound(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	workspace := f.data.Workspace.URLKey
	baseline, ok := f.storedWorkspace(workspace)
	if !ok {
		t.Fatal("fixture workspace missing")
	}
	baseline = cloneBootstrap(baseline)
	userSettings := defaultUserSettings("sw78-unrelated-user")
	userSettings.UpdatedAt = time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	baseline.UserSettings[userSettings.UserID] = userSettings
	preferences := defaultNotificationPreferences(userSettings.UserID)
	preferences.UpdatedAt = userSettings.UpdatedAt
	baseline.NotificationPreferences[userSettings.UserID] = preferences
	peaks := map[bool]int{}
	nonbody := map[bool]domain.Bootstrap{}
	canonical := map[bool]scaleP21SW76Metadata{}
	for _, size := range []int{2000, 20000} {
		seed := scaleP21SW78Data(f, size)
		f.db.SetMaxOpenConns(4)
		// A prior reload adds defaults and normalizes references. Reset every
		// field and persist it in full so body sizes start from the same state.
		err := func() error {
			restore := ForceFullMutationsForTesting(true)
			defer restore()
			return f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw78_seed", "", nil, func(data *domain.Bootstrap) error {
				*data = cloneBootstrap(baseline)
				data.Labels, data.Projects, data.Users, data.Documents, data.Settings = seed.Labels, seed.Projects, seed.Users, seed.Documents, seed.Settings
				return nil
			})
		}()
		if err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("body-%d/reload-%t", size, reload), func(t *testing.T) {
				f.db.SetMaxOpenConns(4)
				if reload {
					// Exercise decoding even when the seeded local copy is current.
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
				before, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				beforeOwned := true
				t.Cleanup(func() {
					if beforeOwned {
						before.Release()
					}
				})
				data, err := before.Select(t.Context(), scaleP21SW72FullScope(t, before), 64<<20)
				if err != nil {
					t.Fatal(err)
				}
				small := cloneBootstrap(data)
				body := seed.Documents[0].Content
				for _, index := range []int{0, 2} {
					if small.Labels[index].Description != body || small.Projects[index].Description != body {
						t.Fatal("fixture did not retain the requested label and project body sizes")
					}
					small.Labels[index].Description = ""
					small.Projects[index].Description = ""
				}
				if small.Users[1].JobTitle != body || small.Documents[0].Content != body {
					t.Fatal("fixture did not retain the requested user and document body sizes")
				}
				small.Users[1].JobTitle, small.Documents[0].Content = "", ""
				if previous, ok := nonbody[reload]; ok {
					beforeValue, afterValue := reflect.ValueOf(previous), reflect.ValueOf(small)
					for index := range beforeValue.NumField() {
						if !reflect.DeepEqual(beforeValue.Field(index).Interface(), afterValue.Field(index).Interface()) {
							first, _ := json.Marshal(beforeValue.Field(index).Interface())
							second, _ := json.Marshal(afterValue.Field(index).Interface())
							t.Errorf("nonbody %s differs: %s / %s", beforeValue.Type().Field(index).Name, first, second)
						}
					}
					if t.Failed() {
						t.FailNow()
					}
				} else {
					nonbody[reload] = small
				}
				tx, _ := scaleP21SW77Begin(t, f, small)
				image := scaleP21SW76MetadataImage(t, tx, workspace)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if previous, ok := canonical[reload]; ok {
					if !reflect.DeepEqual(image, previous) {
						t.Fatal("body sizes changed nonbody canonical root, rows or collection orders")
					}
				} else {
					canonical[reload] = image
				}
				data.Labels[1].IssueCount++
				data.Projects[1].IssueCount++
				after := scaleP21SW78Capture(t, data)
				f.db.SetMaxOpenConns(1)
				peak := scaleP21SW78Compare(t, f, view, before, after, before, "", func() { before.Release(); beforeOwned = false; after.Release() })
				if peak == 0 {
					t.Fatal("count write had no admitted work")
				}
				if peaks[reload] == 0 {
					peaks[reload] = peak
				}
				if peak != peaks[reload] {
					t.Fatal("10x unchanged bodies increased count write work", peak, peaks[reload])
				}
				t.Logf("body_size=%d reload=%t count_write_sql_peak=%d", size, reload, peak)
			})
		}
	}
}

func TestScaleP21SW78LegacyCountWriteFailureRelease(t *testing.T) {
	f, view := scaleP21SW78Fixture(t)
	data := scaleP21SW78Data(f, 1000)
	next := cloneBootstrap(data)
	next.Labels[1].IssueCount++
	next.Projects[1].IssueCount++
	before, after := scaleP21SW78Capture(t, data), scaleP21SW78Capture(t, next)
	scope := scaleP21SW72FullScope(t, after)
	for _, failure := range []string{"cancel-before", "cancel-after-start", "quota"} {
		t.Run(failure, func(t *testing.T) {
			tx, ctx := scaleP21SW77Begin(t, f, data)
			preimage := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			want := error(context.Canceled)
			if failure == "cancel-before" {
				cancel()
			}
			if failure == "cancel-after-start" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					lower := strings.ToLower(query)
					if strings.Contains(lower, "from workspace_states") || strings.Contains(lower, "from workspace_metadata_records") {
						cancel()
					}
				})
			}
			budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
			if failure == "quota" {
				budget.limit, want = 1, ErrMetadataWorkBudget
			}
			defer func() { budget.limit = limit }()
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			result, err := view.persistLegacyResourceCounts(ctx, tx, data.Workspace.URLKey, before, after, before)
			budget.limit = limit
			if result != nil {
				result.Release()
				t.Fatal("failed count write exposed a native postimage")
			}
			if !errors.Is(err, want) || view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("failed count write changed its error or leaked", err)
			}
			if got := scaleP21SW76MetadataImage(t, tx, data.Workspace.URLKey); !reflect.DeepEqual(got, preimage) {
				t.Fatal("preflight failure wrote canonical count metadata")
			}
			scaleP21SW72Compare(t, before, scope, data)
			scaleP21SW72Compare(t, after, scope, next)
			scaleP21SW70Usable(t, tx)
		})
	}
}
