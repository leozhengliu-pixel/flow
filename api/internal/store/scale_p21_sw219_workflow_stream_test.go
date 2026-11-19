package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW219WorkflowCanonicalFrames(t *testing.T) {
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	for _, raw := range []string{
		`null`,
		`{"id":"run","error":"first","ERROR":null,"output":null}`,
		`{"id":"run","error":"first","ERROR":"last\u0000","output":{}}`,
		`{"id":"run","output":{"a":"old","same":"first","same":null},"OUTPUT":{"b":"new","a":null}}`,
		`{"id":"run","output":{"a":"old"},"output":null,"output":{"b":"new"}}`,
		`{"id":"run","error":"\ufffd","output":{"\u0061":"<>&\u2028","a":"last"},"nextRetryAt":"2026-01-02T03:04:05.000000006+07:00"}`,
		`{"id":"run","error":"` + strings.Repeat("large error ", 900000) + `","output":{"large":"` + strings.Repeat("large output ", 900000) + `"}}`,
	} {
		var oracle domain.WorkflowRun
		if err := json.Unmarshal([]byte(raw), &oracle); err != nil {
			t.Fatal(err)
		}
		expected, err := json.Marshal(oracle)
		if err != nil {
			t.Fatal(err)
		}
		frames, err := newMetadataLogicalBuilder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		node, err := workflowRunCanonicalRecord(t.Context(), view, frames, legacySessionJSONPart{strings.NewReader(raw), 0, int64(len(raw))})
		if err != nil {
			frames.Close()
			t.Fatal(err)
		}
		owner := &ResourceRead{store: view}
		got, err := legacySessionNodeSpool(t.Context(), owner, node)
		if err != nil {
			frames.Close()
			owner.Close()
			t.Fatal(err)
		}
		actual := make([]byte, int(got.bytes))
		_, err = got.ReadAt(actual, 0)
		got.Close()
		owner.Close()
		frames.Close()
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatal("canonical WorkflowRun changed typed JSON", err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canonical framing leaked")
		}
	}
}

type scaleP21SW219Result struct {
	native   json.RawMessage
	root     json.RawMessage
	rows     [][]any
	conflict bool
	peak     int
}

func scaleP21SW219Execute(t *testing.T, fixture scaleP09Fixture, view *SQLiteStore, before, requested domain.Bootstrap, peer []byte, deleted, bounded, cancelRead, cancelWrite bool) scaleP21SW219Result {
	t.Helper()
	workspace := before.Workspace.URLKey
	if err := fixture.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	// Seed in a separate ordinary transaction: multiple 10 MiB historical
	// writes must not be charged to the retry's single 32 MiB transaction.
	if err := fixture.MutateWorkspace(t.Context(), workspace, "scale_p21.sw219_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkflowRuns = cloneBootstrap(before).WorkflowRuns
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if peer != nil || deleted {
		tx, err := fixture.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if deleted {
			err = deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"workflowRuns", before.WorkflowRuns[0].ID})
		} else {
			_, err = tx.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='workflowRuns' AND record_key=?`, StripJSONNUL(peer), workspace, before.WorkflowRuns[0].ID)
		}
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.WithValue(t.Context(), workspaceReadKey{}, workspaceRead{database: fixture.db, workspace: workspace, reader: tx})
	var cancelledBaseline scaleP21SW77SQLImage
	if cancelRead || cancelWrite {
		cancelledBaseline = scaleP21SW77Image(t, tx, workspace)
	}
	result := scaleP21SW219Result{}
	if !bounded {
		target := cloneBootstrap(requested)
		_, err = fixture.writeWorkspaceMetadataDelta(ctx, tx, workspace, cloneBootstrap(before), &target)
		if errors.Is(err, ErrConcurrentWrite) {
			result.conflict = true
			return result
		}
		if err != nil {
			t.Fatal("literal retry clear", err)
		}
		result.native, err = json.Marshal(target.WorkflowRuns)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		left, err := captureMetadataLogicalSource(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer left.Release()
		right, err := captureMetadataLogicalSource(ctx, requested, left.Stamp())
		if err != nil {
			t.Fatal(err)
		}
		defer right.Release()
		ctx = context.WithValue(ctx, workflowRunStreamKey{}, true)
		ctx = context.WithValue(ctx, legacyMetadataExtraFieldsKey{}, []string{"workflowRuns"})
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		reads := 0
		cancelled := false
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			result.peak = max(result.peak, view.MetadataWorkUsedForTesting())
			lower := strings.ToLower(query)
			if strings.Contains(lower, "select substr(") && strings.Contains(lower, "workspace_metadata_records") {
				reads++
			}
			if !cancelled && (cancelRead && reads == 4 || cancelWrite && strings.Contains(lower, "insert into bounded_metadata_write_chunks(operation_id,part,seq,chunk) values(?,0,")) {
				cancelled = true
				cancel()
			}
		})
		merged, runErr := view.persistLegacyMetadataImage(ctx, tx, workspace, left, right)
		if cancelRead || cancelWrite {
			if merged != nil {
				merged.Release()
			}
			if !cancelled || !errors.Is(runErr, context.Canceled) {
				t.Fatal("streamed cancellation boundary was not reached", cancelled, runErr)
			}
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				t.Fatal(err)
			}
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled retry clear leaked")
			}
			verify, err := fixture.db.BeginTx(t.Context(), metadataReadOptions(fixture.dialect))
			if err != nil {
				t.Fatal(err)
			}
			unchanged := reflect.DeepEqual(cancelledBaseline, scaleP21SW77Image(t, verify, workspace))
			verify.Rollback()
			if !unchanged {
				t.Fatal("cancelled retry clear changed durable metadata")
			}
			return result
		}
		if errors.Is(runErr, ErrConcurrentWrite) {
			result.conflict = true
			return result
		}
		if runErr != nil {
			t.Fatal("streamed retry clear", runErr)
		}
		defer merged.Release()
		index, _, _ := metadataLogicalField("workflowRuns")
		owner := &ResourceRead{store: view}
		raw, err := legacySessionNodeSpool(t.Context(), owner, merged.fields[index])
		if err != nil {
			owner.Close()
			t.Fatal(err)
		}
		result.native = make([]byte, int(raw.bytes))
		_, err = raw.ReadAt(result.native, 0)
		raw.Close()
		owner.Close()
		if err != nil {
			t.Fatal(err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("retry clear retained native work")
		}
	}
	image := scaleP21SW77Image(t, tx, workspace)
	result.root = json.RawMessage(image.Root)
	for key, row := range image.Rows {
		if key.field == "workflowRuns" {
			result.rows = append(result.rows, []any{key.key, row.order, json.RawMessage(row.data)})
		}
	}
	sort.Slice(result.rows, func(i, j int) bool { return result.rows[i][0].(string) < result.rows[j][0].(string) })
	var scratch int
	if err := tx.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&scratch); err != nil || scratch != 0 {
		t.Fatal("workflow SQL stage leaked", scratch, err)
	}
	return result
}

func TestScaleP21SW219WorkflowRetryStreamDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	view := &SQLiteStore{db: fixture.db, dialect: fixture.dialect, maxStateBytes: fixture.maxStateBytes, metadataResidency: newMetadataResidency()}
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	base := domain.WorkflowRun{ID: "sw219-run", WorkflowID: "sw219-workflow", Status: "failed", Attempt: 2, Error: "before\x00" + string([]byte{0xff}),
		Output: map[string]string{"body": strings.Repeat("retained output ", 700000)}, ScheduledAt: at, StartedAt: at, NextRetryAt: &at}
	for _, test := range []struct {
		name            string
		peer            func(*domain.WorkflowRun)
		deleted, inline bool
	}{
		{"raw-equality", nil, false, false},
		{"native-invalid-raw", nil, false, false},
		{"typed-equality", nil, false, false},
		{"peer-error", func(run *domain.WorkflowRun) { run.Error = "peer\x00error" }, false, false},
		{"peer-output", func(run *domain.WorkflowRun) {
			run.Output = map[string]string{"body": strings.Repeat("peer output ", 900000), "other": "keep"}
		}, false, false},
		{"peer-retry-conflict", func(run *domain.WorkflowRun) { next := at.Add(time.Hour); run.NextRetryAt = &next }, false, false},
		{"peer-already-cleared", func(run *domain.WorkflowRun) { run.NextRetryAt = nil }, false, false},
		{"peer-deleted", nil, true, false},
		{"inline-duplicate", nil, false, true},
		{"inline-empty-id", nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := cloneBootstrap(fixture.data)
			before.WorkflowRuns = []domain.WorkflowRun{base}
			if test.name == "raw-equality" {
				before.WorkflowRuns[0].Error = "same raw error"
			}
			if test.name == "native-invalid-raw" {
				before.WorkflowRuns[0].Error = string([]byte{0xff})
				before.WorkflowRuns[0].Output = map[string]string{"body": base.Output["body"] + string([]byte{0xff})}
			}
			if test.name == "typed-equality" {
				before.WorkflowRuns[0].Error = "\ufffd"
			}
			if test.inline {
				// Two 5 MiB entries keep the legal inline transaction below
				// 32 MiB while still refusing the former 64x fallback.
				before.WorkflowRuns[0].Output = map[string]string{"body": strings.Repeat("inline value ", 400000)}
				if test.name == "inline-duplicate" {
					before.WorkflowRuns = append(before.WorkflowRuns, before.WorkflowRuns[0])
				} else {
					before.WorkflowRuns[0].ID = ""
				}
			}
			requested := cloneBootstrap(before)
			requested.WorkflowRuns[0].NextRetryAt = nil
			requested.WorkflowRuns = append(requested.WorkflowRuns, domain.WorkflowRun{ID: "sw219-created", WorkflowID: base.WorkflowID, Status: "success", Attempt: 3, Output: map[string]string{}, ScheduledAt: at, StartedAt: at})
			var peer []byte
			if test.peer != nil {
				changed := cloneBootstrap(before).WorkflowRuns[0]
				test.peer(&changed)
				peer, _ = json.Marshal(changed)
			}
			if test.name == "typed-equality" {
				peer, _ = json.Marshal(before.WorkflowRuns[0])
				peer = bytes.ReplaceAll(peer, []byte("\ufffd"), []byte(`\ufffd`))
			}
			literal := scaleP21SW219Execute(t, fixture, view, before, requested, peer, test.deleted, false, false, false)
			bounded := scaleP21SW219Execute(t, fixture, view, before, requested, peer, test.deleted, true, false, false)
			if literal.conflict != bounded.conflict {
				t.Fatal("streamed conflict differs", literal.conflict, bounded.conflict)
			}
			if !literal.conflict && (!bytes.Equal(literal.native, bounded.native) || !reflect.DeepEqual(literal.rows, bounded.rows) || !bytes.Equal(literal.root, bounded.root)) {
				t.Fatal("large retry clear changed native or physical output")
			}
			if bounded.peak >= 32<<20 {
				t.Fatal("opaque workflow body entered native work", bounded.peak)
			}
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("workflow stream leaked")
			}
		})
	}
	for _, phase := range []string{"read", "write"} {
		t.Run("cancel-"+phase, func(t *testing.T) {
			before := cloneBootstrap(fixture.data)
			before.WorkflowRuns = []domain.WorkflowRun{base}
			requested := cloneBootstrap(before)
			requested.WorkflowRuns[0].NextRetryAt = nil
			scaleP21SW219Execute(t, fixture, view, before, requested, nil, false, true, phase == "read", phase == "write")
		})
	}
	if metadataSearchFieldSet["workflowRuns"] {
		t.Fatal("WorkflowRun gained a search hook; extend the streamed writer")
	}
	if err := writeCustomerFilterRecord(t.Context(), nil, "", "workflowRuns", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := writeReleaseIssueRecords(t.Context(), nil, "", "workflowRuns", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW219WorkflowLargeConflictRefresh(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	workspace := fixture.data.Workspace.URLKey
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, deleted := range []bool{false, true} {
		before := cloneBootstrap(fixture.data)
		before.WorkflowRuns = []domain.WorkflowRun{
			{ID: "sw219-refresh", WorkflowID: "workflow", Status: "failed", Error: "native", Output: map[string]string{"body": "old"}, ScheduledAt: at, StartedAt: at, NextRetryAt: &at},
			{ID: "sw219-refresh", WorkflowID: "later duplicate", Status: "success", Output: map[string]string{"keep": "later"}, ScheduledAt: at, StartedAt: at},
		}
		if err := fixture.MutateWorkspace(t.Context(), workspace, "scale_p21.sw219_refresh_seed", "", nil, func(data *domain.Bootstrap) error {
			data.WorkflowRuns = []domain.WorkflowRun{before.WorkflowRuns[0]}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		peer := before.WorkflowRuns[0]
		peer.Error, peer.Output = "current", map[string]string{"body": strings.Repeat("large refreshed output ", 500000)}
		peer.NextRetryAt = nil
		tx, err := fixture.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if deleted {
			err = deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"workflowRuns", peer.ID})
		} else {
			scaleP09Put(t, tx, workspace, "workflowRuns", peer.ID, peer)
		}
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{workspace: before})
		view.db, view.dialect = fixture.db, fixture.dialect
		failure := &metadataConflictError{keys: []metadataRecordKey{{"workflowRuns", peer.ID}}}
		if err := view.refreshWorkflowRunStreamConflict(t.Context(), workspace)(failure); err != nil {
			t.Fatal("large named conflict refresh", deleted, err)
		}
		source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		index, _, _ := metadataLogicalField("workflowRuns")
		value, err := source.fields[index].Value(t.Context(), metadataWorkBytes)
		source.Release()
		expected := []domain.WorkflowRun{peer, before.WorkflowRuns[1]}
		if deleted {
			expected = expected[1:]
		}
		if err != nil || !reflect.DeepEqual(value.Interface(), expected) {
			t.Fatal("large conflict refresh changed first-occurrence or deletion behavior", deleted, err)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("large conflict refresh leaked")
		}
	}
}

func TestScaleP21SW219WorkflowLargeConflictRetrySuccess(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	workspace := fixture.data.Workspace.URLKey
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	base := domain.WorkflowRun{
		ID: "sw219-retry", WorkflowID: "sw219-workflow", Status: "failed", Attempt: 1,
		Error: "before", Output: map[string]string{"body": "before"},
		ScheduledAt: at, StartedAt: at, NextRetryAt: &at,
	}
	if err := fixture.MutateWorkspace(t.Context(), workspace, "scale_p21.sw219_retry_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkflowRuns = []domain.WorkflowRun{base}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := fixture.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	nextRetry := at.Add(time.Hour)
	peer := base
	peer.Error = strings.Repeat("large peer error ", 320000)
	peer.Output = map[string]string{"body": strings.Repeat("large peer output ", 320000)}
	peer.NextRetryAt = &nextRetry
	tx, err := fixture.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	scaleP09Put(t, tx, workspace, "workflowRuns", peer.ID, peer)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	appended := domain.WorkflowRun{
		ID: "sw219-retried", WorkflowID: base.WorkflowID, Status: "succeeded", Attempt: 2,
		Output: map[string]string{}, ScheduledAt: at, StartedAt: at,
	}
	var closes []func()
	ctx := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
		closes = append(closes, close)
		return true
	})
	defer func() {
		for _, close := range closes {
			close()
		}
	}()
	attempts := 0
	err = view.MutateWorkflowRun(ctx, workspace, base.WorkflowID, "", "", map[string]string{"trigger": "retry"}, func(data *domain.Bootstrap) error {
		attempts++
		if len(data.WorkflowRuns) != 1 || data.WorkflowRuns[0].NextRetryAt == nil {
			return errors.New("retry callback lost its failed predecessor")
		}
		wantRetry := at
		if attempts > 1 {
			wantRetry = nextRetry
		}
		if !data.WorkflowRuns[0].NextRetryAt.Equal(wantRetry) {
			return errors.New("retry callback did not observe the refreshed retry time")
		}
		data.WorkflowRuns[0].NextRetryAt = nil
		data.WorkflowRuns = append(data.WorkflowRuns, appended)
		return nil
	})
	for _, close := range closes {
		close()
		close()
	}
	closes = nil
	if err != nil || attempts != 2 {
		t.Fatal("large named conflict did not refresh and retry successfully", attempts, err)
	}
	peer.NextRetryAt = nil
	expected := []domain.WorkflowRun{peer, appended}
	if err := fixture.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	persisted, found := fixture.WorkspaceMetadataFields(workspace, "workflowRuns")
	if !found || !reflect.DeepEqual(persisted.WorkflowRuns, expected) {
		t.Fatal("successful large retry changed persisted bodies, order, clear or append")
	}
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	index, _, _ := metadataLogicalField("workflowRuns")
	value, err := source.fields[index].Value(t.Context(), metadataWorkBytes)
	source.Release()
	if err != nil || !reflect.DeepEqual(value.Interface(), expected) {
		t.Fatal("successful large retry changed the installed publication", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("successful large retry leaked work or hydrated full metadata", view.MetadataWorkUsedForTesting(), view.WorkspaceMetadataDecodes())
	}
}

func TestScaleP21SW219WorkflowInlineRootLifecycleFieldDifferential(t *testing.T) {
	for _, mode := range []string{"agent-default", "workflow-inline", "workflow-split"} {
		t.Run(mode, func(t *testing.T) {
			f, view, before := scaleP21SW77Fixture(t)
			at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			before.Asks = []domain.Ask{{ID: "duplicate", Title: "before"}, {ID: "duplicate", Title: "keep second"}}
			before.AgentSessions = []domain.AgentSession{{ID: "session", Title: "keep first"}, {ID: "session", Title: "keep second"}}
			before.WorkflowRuns = []domain.WorkflowRun{
				{ID: "run", WorkflowID: "workflow", Status: "failed", NextRetryAt: &at},
				{ID: "run", WorkflowID: "workflow", Status: "failed", Error: "keep second"},
			}
			split := mode == "workflow-split"
			if split {
				before.WorkflowRuns = before.WorkflowRuns[:1]
			}
			target := cloneBootstrap(before)
			target.Asks[0].Title = "changed inline ask"
			kind := "workflowRuns"
			field := kind
			if mode == "agent-default" {
				kind, field = "agentSessions", ""
				target.AgentSessions[0].Title = "changed legacy default session"
			} else if !split {
				target.WorkflowRuns[0].NextRetryAt = nil
			}
			tx, ctx := scaleP21SW77Begin(t, f, before)
			want := cloneBootstrap(target)
			if _, err := f.writeWorkspaceMetadataDelta(ctx, tx, before.Workspace.URLKey, before, &want); err != nil {
				t.Fatal("literal inline/lifecycle oracle", err)
			}
			expected := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}

			tx, ctx = scaleP21SW77Begin(t, f, before)
			defer tx.Rollback()
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			plan, selected, release := scaleP21SW217Selected(t, view, tx, before.Workspace.URLKey, before, target, "asks")
			released := false
			closeSelected := func() {
				if !released {
					release()
					released = true
				}
			}
			defer closeSelected()
			patch, err := plan.prepareInlineFields(ctx, []*legacyMetadataWriteField{selected})
			if err != nil {
				t.Fatal(err)
			}
			defer patch.Close()
			var raw []byte
			if kind == "agentSessions" {
				raw, err = json.Marshal(target.AgentSessions)
				ctx = context.WithValue(ctx, agentSessionLifecycleWriteKey{}, true)
			} else {
				raw, err = json.Marshal(target.WorkflowRuns)
				ctx = context.WithValue(ctx, workflowRunStreamKey{}, true)
			}
			if err != nil {
				t.Fatal(err)
			}
			if split {
				raw = []byte("[]")
			}
			ctx = context.WithValue(ctx, legacySessionRootPatchKey{}, &legacySessionRootPatch{
				field: field, split: split, part: legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))},
			})
			owner := &ResourceRead{store: view}
			defer owner.Close()
			if err := owner.reserve(ctx, 2<<20); err != nil {
				t.Fatal(err)
			}
			size, _ := scaleP21SW217Shapes(t, ctx, tx, before.Workspace.URLKey)
			root, err := view.scanMetadataInlineRoot(ctx, tx, before.Workspace.URLKey, size)
			if err != nil {
				t.Fatal(err)
			}
			closeRoot := func() {
				if closer, ok := any(root).(interface{ Close() }); ok {
					closer.Close()
				} else if closer, ok := any(root).(interface{ Close() error }); ok {
					_ = closer.Close()
				}
			}
			defer closeRoot()
			writes := 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if _, root := scaleP21SW83MetadataWrite(query); root {
					writes++
				}
			})
			if err := view.writeLegacyInlineFieldsRootPatch(ctx, tx, before.Workspace.URLKey, target.Workspace.ID, root, nil, owner, true, patch); err != nil {
				t.Fatal("combined inline/lifecycle root write", err)
			}
			if got := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey); !reflect.DeepEqual(got, expected) || writes != 1 {
				t.Fatal("lifecycle field replaced the wrong root collection or changed canonical rows", kind, writes)
			}
			closeRoot()
			owner.Close()
			patch.Close()
			closeSelected()
			if tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 ||
				view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
				metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("combined inline/lifecycle root retained work or staging")
			}
		})
	}
}
