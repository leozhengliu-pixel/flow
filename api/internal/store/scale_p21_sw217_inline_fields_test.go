package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW217Selected(t *testing.T, repo *SQLiteStore, tx *sqlTx, workspace string, before, target domain.Bootstrap, name string) (*legacyMetadataWritePlan, *legacyMetadataWriteField, func()) {
	t.Helper()
	left, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{workspaceID: before.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	right, err := captureMetadataLogicalSource(t.Context(), target, copyStamp{workspaceID: target.Workspace.ID})
	if err != nil {
		left.Release()
		t.Fatal(err)
	}
	field, found := metadataFieldByName(name)
	if !found {
		left.Release()
		right.Release()
		t.Fatal("missing selected field", name)
	}
	owner := &ResourceRead{store: repo}
	plan := &legacyMetadataWritePlan{
		store: repo, tx: tx, workspace: workspace, workspaceID: before.Workspace.ID,
		work: owner, requested: right,
	}
	selected := &legacyMetadataWriteField{
		field: field, before: left.fields[field.index], next: right.fields[field.index],
		full: true, changed: true, persist: true,
	}
	return plan, selected, func() {
		owner.Close()
		left.Release()
		right.Release()
	}
}

func scaleP21SW217Shapes(t *testing.T, ctx context.Context, tx *sqlTx, workspace string) (int64, map[string]string) {
	t.Helper()
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var shapes map[string]string
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		t.Fatal(err)
	}
	return int64(len(raw)), shapes
}

func scaleP21SW217GenericWrite(t *testing.T, ctx context.Context, repo *SQLiteStore, tx *sqlTx, before, target domain.Bootstrap, name string, session bool) error {
	t.Helper()
	workspace := before.Workspace.URLKey
	plan, field, release := scaleP21SW217Selected(t, repo, tx, workspace, before, target, name)
	defer release()
	size, shapes := scaleP21SW217Shapes(t, ctx, tx, workspace)
	ready, err := plan.inlineFieldReady(ctx, field, shapes)
	if err != nil || !ready {
		t.Fatal("inline fixture did not select streamed field", ready, err)
	}
	patch, err := plan.prepareInlineFields(ctx, []*legacyMetadataWriteField{field})
	if err != nil {
		return err
	}
	defer patch.Close()
	ctx = context.WithValue(ctx, legacyInlineFieldsPatchKey{}, patch)
	owner := &ResourceRead{store: repo}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return err
	}
	root, err := repo.scanMetadataInlineRoot(ctx, tx, workspace, size)
	if err != nil {
		return err
	}
	ordinary := map[string]json.RawMessage{}
	names := []string{name}
	if before.Workspace.Name != target.Workspace.Name {
		ordinary["workspace"], err = json.Marshal(target.Workspace)
		if err != nil {
			return err
		}
		names = append(names, "workspace")
	}
	if session {
		raw, err := json.Marshal(target.AgentSessions)
		if err != nil {
			return err
		}
		ctx = context.WithValue(ctx, agentSessionLifecycleWriteKey{}, true)
		ctx = context.WithValue(ctx, legacySessionRootPatchKey{}, &legacySessionRootPatch{
			part: legacySessionJSONPart{bytes.NewReader(raw), 0, int64(len(raw))},
		})
		names = append(names, "agentSessions")
	}
	// The old full-field splitter produces no rows for these inline values.
	// Include their names in the ordinary sync, preserving its zero-row work.
	if err := syncMetadataRecords(withLegacyMetadataDeltaReadBudget(ctx, owner), tx, workspace,
		newIssueReferences(target), map[string]string{}, map[metadataRecordKey]metadataRecord{}, names,
		map[metadataRecordKey]metadataRecord{}); err != nil {
		return err
	}
	return repo.writeLegacyInlineFieldsRootPatch(ctx, tx, workspace, target.Workspace.ID, root, ordinary, owner, true, patch)
}

func TestScaleP21SW217InlineFieldsLiteralDifferential(t *testing.T) {
	for _, variant := range []string{"ask-5.5MiB", "cycles-20001", "empty-id", "overlong-id", "invalid-UTF8-collision", "ordinary-and-session"} {
		t.Run(variant, func(t *testing.T) {
			f, view, before := scaleP21SW77Fixture(t)
			name := "asks"
			before.Asks = []domain.Ask{{ID: "same", Title: "first"}, {ID: "same", Title: "later"}}
			switch variant {
			case "ask-5.5MiB":
				before.Asks[0].Body = strings.Repeat("x", 11<<19)
			case "cycles-20001":
				name = "cycles"
				before.Cycles = make([]domain.Cycle, 20001)
				for index := range before.Cycles {
					before.Cycles[index] = domain.Cycle{ID: "same", Name: "first", Description: strings.Repeat("x", 64)}
				}
			case "empty-id":
				before.Asks = []domain.Ask{{ID: "", Body: "body"}}
			case "overlong-id":
				before.Asks = []domain.Ask{{ID: strings.Repeat("x", 192), Body: "body"}}
			case "invalid-UTF8-collision":
				before.Asks = []domain.Ask{{ID: string([]byte{0xff}), Title: "first"}, {ID: string([]byte{0xfe}), Title: "later"}}
			case "ordinary-and-session":
				before.AgentSessions = []domain.AgentSession{{ID: "same", Title: "first"}, {ID: "same", Title: "later"}}
			}
			target := cloneBootstrap(before)
			if name == "cycles" {
				target.Cycles[0].Name = "changed"
			} else {
				target.Asks[0].Title = "changed <>&"
			}
			session := variant == "ordinary-and-session"
			if session {
				target.Workspace.Name = "ordinary scalar changed"
				target.AgentSessions[0].Title = "session changed"
			}
			tx, ctx := scaleP21SW77Begin(t, f, before)
			start := tx.writtenBytes
			want := cloneBootstrap(target)
			if _, err := f.writeWorkspaceMetadataDelta(ctx, tx, before.Workspace.URLKey, before, &want); err != nil {
				t.Fatal("literal inline oracle", err)
			}
			expected := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey)
			var stamp string
			if err := tx.QueryRowContext(ctx, "SELECT updated_at FROM workspace_states WHERE workspace_key=?", before.Workspace.URLKey).Scan(&stamp); err != nil {
				t.Fatal(err)
			}
			// Only the generated clock's textual width may differ between runs.
			expectedBill := tx.writtenBytes - start - len(stamp)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			tx, ctx = scaleP21SW77Begin(t, f, before)
			start = tx.writtenBytes
			rootWrites := 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if _, root := scaleP21SW83MetadataWrite(query); root {
					rootWrites++
				}
			})
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			if err := scaleP21SW217GenericWrite(t, ctx, view, tx, before, target, name, session); err != nil {
				t.Fatal("streamed inline root", err)
			}
			actual := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("literal/streamed inline root or canonical rows differ")
			}
			if err := tx.QueryRowContext(ctx, "SELECT updated_at FROM workspace_states WHERE workspace_key=?", before.Workspace.URLKey).Scan(&stamp); err != nil {
				t.Fatal(err)
			}
			if got := tx.writtenBytes - start - len(stamp); got != expectedBill || rootWrites != 1 {
				t.Fatal("inline root changed original SQL bill or root write count", got, expectedBill, rootWrites)
			}
			if tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 ||
				view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
				metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("inline root retained work, SQL stage or spool")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP21SW217InlineEligibilityAndTransitions(t *testing.T) {
	for _, variant := range []string{"nil", "empty", "unique", "invalid-UTF8-unique", "invalid-UTF8-collision", "normalized-overlong", "empty-id", "overlong", "canonical-row", "split-root"} {
		t.Run(variant, func(t *testing.T) {
			f, view, before := scaleP21SW77Fixture(t)
			before.Asks = []domain.Ask{{ID: "same"}, {ID: "same"}}
			if variant == "split-root" {
				before.Asks = []domain.Ask{{ID: "first"}, {ID: "second"}}
			}
			target := cloneBootstrap(before)
			want := false
			switch variant {
			case "nil":
				target.Asks = nil
			case "empty":
				target.Asks = []domain.Ask{}
			case "unique":
				target.Asks = []domain.Ask{{ID: "first"}, {ID: "second"}}
			case "invalid-UTF8-unique":
				target.Asks = []domain.Ask{{ID: string([]byte{0xff})}}
			case "invalid-UTF8-collision":
				target.Asks = []domain.Ask{{ID: string([]byte{0xff})}, {ID: string([]byte{0xfe})}}
				want = true
			case "normalized-overlong":
				target.Asks = []domain.Ask{{ID: string(bytes.Repeat([]byte{0xff}, 64))}}
				want = true
			case "empty-id":
				target.Asks = []domain.Ask{{ID: ""}}
				want = true
			case "overlong":
				target.Asks = []domain.Ask{{ID: strings.Repeat("x", 192)}}
				want = true
			}
			tx, ctx := scaleP21SW77Begin(t, f, before)
			if variant == "canonical-row" {
				if err := writeMetadataRecordRow(ctx, tx, before.Workspace.URLKey,
					metadataRecordKey{"asks", "unexpected"}, metadataRecord{data: []byte(`{"id":"unexpected"}`)}, nil); err != nil {
					t.Fatal(err)
				}
			}
			_, shapes := scaleP21SW217Shapes(t, ctx, tx, before.Workspace.URLKey)
			plan, field, release := scaleP21SW217Selected(t, view, tx, before.Workspace.URLKey, before, target, "asks")
			ready, err := plan.inlineFieldReady(ctx, field, shapes)
			release()
			if err != nil || ready != want || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("inline eligibility/transition differs", ready, want, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScaleP21SW217InlineCancellationAndRelease(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	before.Asks = []domain.Ask{{ID: "same", Body: strings.Repeat("x", 11<<19)}, {ID: "same"}}
	target := cloneBootstrap(before)
	target.Asks[0].Title = "changed"
	tx, ctx := scaleP21SW77Begin(t, f, before)
	plan, field, release := scaleP21SW217Selected(t, view, tx, before.Workspace.URLKey, before, target, "asks")
	defer release()
	disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	patch, err := plan.prepareInlineFields(canceled, []*legacyMetadataWriteField{field})
	if patch != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled inline preparation changed error or leaked work", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = logicalJSONStreamWork / 2
	patch, err = plan.prepareInlineFields(ctx, []*legacyMetadataWriteField{field})
	view.metadataResidency.work.limit = limit
	if patch != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("partial field admission leaked work/spools", err)
	}
	patch, err = plan.prepareInlineFields(ctx, []*legacyMetadataWriteField{field})
	if err != nil {
		t.Fatal(err)
	}
	patch.Close()
	patch.Close()
	if len(patch.parts) != 0 || view.MetadataWorkUsedForTesting() != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("idempotent inline release retained work/spools")
	}
	expected := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey)
	limitBytes := view.maxStateBytes
	view.maxStateBytes = 1024
	patch, err = plan.prepareInlineFields(ctx, []*legacyMetadataWriteField{field})
	view.maxStateBytes = limitBytes
	if patch != nil || err == nil || !strings.Contains(err.Error(), "workspace state exceeds") {
		t.Fatal("inline field bypassed the original max-state limit", err)
	}
	if got := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey); !reflect.DeepEqual(got, expected) ||
		view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
		metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("max-state refusal changed metadata or retained work/spools")
	}
	patch, err = plan.prepareInlineFields(ctx, []*legacyMetadataWriteField{field})
	if err != nil {
		t.Fatal(err)
	}
	owner := &ResourceRead{store: view}
	if err := owner.reserve(ctx, 2<<20); err != nil {
		patch.Close()
		t.Fatal(err)
	}
	size, _ := scaleP21SW217Shapes(t, ctx, tx, before.Workspace.URLKey)
	root, err := view.scanMetadataInlineRoot(ctx, tx, before.Workspace.URLKey, size)
	if err != nil {
		owner.Close()
		patch.Close()
		t.Fatal(err)
	}
	writeCtx, cancelWrite := context.WithCancel(ctx)
	inserted := false
	writeCtx = WithSQLStatementObserver(writeCtx, func(query string) {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "insert into bounded_metadata_write_stage") {
			inserted = true
			cancelWrite()
		}
	})
	err = view.writeLegacyInlineFieldsRootPatch(writeCtx, tx, before.Workspace.URLKey, target.Workspace.ID, root, nil, owner, true, patch)
	cancelWrite()
	owner.Close()
	patch.Close()
	if !inserted || !errors.Is(err, context.Canceled) || tx.metadataSQLStages != 0 ||
		len(tx.metadataSQLStageOwners) != 0 || view.MetadataWorkUsedForTesting() != 0 ||
		metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("canceled root SQL staging retained its owner, stage or spool", err)
	}
	if got := scaleP21SW76MetadataImage(t, tx, before.Workspace.URLKey); !reflect.DeepEqual(got, expected) {
		t.Fatal("canceled root staging changed metadata")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
