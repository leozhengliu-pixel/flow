package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW220Result struct {
	image    scaleP21SW77SQLImage
	native   map[string][]domain.ProjectUpdate
	bill     int
	peak     int
	mentions int
}

func scaleP21SW220Canonical(t *testing.T, ctx context.Context, tx *sqlTx, workspace, parent, variant string, target domain.Bootstrap) {
	t.Helper()
	switch variant {
	case "deleted-parent":
		if _, err := tx.ExecContext(ctx, "DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='projectUpdates' AND record_key=?", workspace, parent); err != nil {
			t.Fatal(err)
		}
	case "foreign-parent":
		raw := []byte(`[{"id":"foreign","body":"Unchanged foreign parent","future":true}]`)
		if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_metadata_records(workspace_key,field,record_key,collection_order,data) VALUES(?,'projectUpdates',?,0,?)",
			workspace, "foreign-parent", raw); err != nil {
			t.Fatal(err)
		}
	case "canonical-unknown", "canonical-folded-id", "canonical-comments", "canonical-occurrences", "order-only":
		raw, err := json.Marshal(target.ProjectUpdates[parent])
		if err != nil {
			t.Fatal(err)
		}
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatal(err)
		}
		switch variant {
		case "canonical-unknown":
			items[0]["future"] = json.RawMessage(`{"unknown":"Discard exactly when the typed replacement writes"}`)
			items[0]["body"] = json.RawMessage(`"Concurrent body must not override the native requested array"`)
		case "canonical-folded-id":
			items[0]["ID"] = items[0]["id"]
			delete(items[0], "id")
		case "canonical-comments":
			delete(items[0], "comments")
		case "canonical-occurrences":
			items[0]["comments"], items[1]["comments"] = items[1]["comments"], items[0]["comments"]
		}
		if variant != "order-only" {
			raw, err = json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
		}
		order := 0
		if variant == "order-only" {
			order = 19
		}
		if _, err := tx.ExecContext(ctx, "UPDATE workspace_metadata_records SET data=?,collection_order=? WHERE workspace_key=? AND field='projectUpdates' AND record_key=?",
			StripJSONNUL(raw), order, workspace, parent); err != nil {
			t.Fatal(err)
		}
		if variant == "order-only" {
			if _, err := tx.ExecContext(ctx, "UPDATE workspace_metadata_records SET collection_order=19 WHERE workspace_key=? AND field='projectUpdates' AND record_key='nil-parent'", workspace); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func scaleP21SW220Run(t *testing.T, fixture scaleP09Fixture, view *SQLiteStore, before, target domain.Bootstrap, parent, variant string, bounded bool) scaleP21SW220Result {
	t.Helper()
	tx, ctx := scaleP21SW77Begin(t, fixture, before)
	defer tx.Rollback()
	ctx = context.WithValue(ctx, advancedScheduleStreamKey{}, true)
	scaleP21SW220Canonical(t, ctx, tx, before.Workspace.URLKey, parent, variant, target)
	start := tx.writtenBytes
	result := scaleP21SW220Result{}
	ctx = WithSQLStatementObserver(ctx, func(query string) {
		if strings.Contains(query, "SELECT list_data FROM issue_records WHERE workspace_key=? AND id=?") {
			result.mentions++
		}
	})
	if !bounded {
		prior, next := cloneBootstrap(before), cloneBootstrap(target)
		field, _ := metadataFieldByName("projectUpdates")
		if _, _, err := fixture.rebaseMetadata(ctx, tx, before.Workspace.URLKey, &prior, &next, []metadataField{field}, nil); err != nil {
			t.Fatal("literal parent-map rebase", err)
		}
		if _, err := fixture.writeWorkspaceMetadataDelta(ctx, tx, before.Workspace.URLKey, prior, &next); err != nil {
			t.Fatal("literal parent-map write", err)
		}
		result.native = next.ProjectUpdates
	} else {
		left, err := captureMetadataLogicalSource(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
		if err != nil {
			t.Fatal(err)
		}
		defer left.Release()
		right, err := captureMetadataLogicalSource(ctx, target, left.Stamp())
		if err != nil {
			t.Fatal(err)
		}
		defer right.Release()
		measuring := true
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if measuring {
				result.peak = max(result.peak, view.MetadataWorkUsedForTesting())
			}
			if strings.Contains(query, "SELECT list_data FROM issue_records WHERE workspace_key=? AND id=?") {
				result.mentions++
			}
		})
		image, err := view.persistLegacyMetadataImage(ctx, tx, before.Workspace.URLKey, left, right)
		if err != nil {
			t.Fatal("streamed parent-map write must succeed", err)
		}
		measuring = false
		index, _, _ := metadataLogicalField("projectUpdates")
		value, err := image.fields[index].Value(ctx, metadataWorkBytes)
		if err != nil {
			image.Release()
			t.Fatal(err)
		}
		result.native = value.Interface().(map[string][]domain.ProjectUpdate)
		image.Release()
		if view.MetadataWorkUsedForTesting() != 0 || tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 {
			t.Fatal("parent-map write retained native work or SQL staging", view.MetadataWorkUsedForTesting())
		}
	}
	result.bill = tx.writtenBytes - start
	result.image = scaleP21SW77Image(t, tx, before.Workspace.URLKey)
	var stages int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM bounded_metadata_write_stage").Scan(&stages); err != nil || stages != 0 {
		t.Fatal("parent-map SQL scratch rows escaped", err, stages)
	}
	return result
}

// The parent-map differential runs its variants in two top-level tests so
// the shards can spread them: each variant writes 5 MiB duplicate bodies
// through both writers.
func TestScaleP21SW220ProjectUpdateParentMapDifferential(t *testing.T) {
	scaleP21SW220ParentMap(t, "duplicate", "empty-parent", "empty-id", "normalized-id", "normalized-parent", "deleted-parent", "foreign-parent")
}

func TestScaleP21SW220ProjectUpdateParentMapCanonicalDifferential(t *testing.T) {
	scaleP21SW220ParentMap(t, "canonical-unknown", "canonical-folded-id", "canonical-comments", "canonical-occurrences", "order-only", "hash-parent-null")
}

func scaleP21SW220ParentMap(t *testing.T, variants ...string) {
	f, view, seed := scaleP21SW77Fixture(t)
	clock := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	for _, variant := range variants {
		t.Run(variant, func(t *testing.T) {
			smallPeak := 0
			for _, size := range []int{32, 5 << 20} {
				before := cloneBootstrap(seed)
				parent := "ordinary-parent"
				if variant == "empty-parent" {
					parent = ""
				}
				first := domain.ProjectUpdate{
					ID: "duplicate", ProjectID: parent, Body: strings.Repeat("x", size) + "\x00\xff",
					Health: "onTrack", User: before.Viewer, CreatedAt: clock,
					BodyData: map[string]any{"native": int64(1<<53 + 1), "time": clock},
					Comments: []domain.Comment{{ID: "old-comment", Body: "Existing comment without application mentions", User: before.Viewer, CreatedAt: clock}},
				}
				second := first
				second.Body = strings.Repeat("y", size)
				second.Comments = []domain.Comment{{ID: "second-comment", Body: "Second duplicate's independent comments", User: before.Viewer, CreatedAt: clock}}
				if variant == "hash-parent-null" {
					first.Body = strings.Repeat("x", size)
					second = first
				}
				if variant == "canonical-occurrences" {
					mention := map[string]any{"type": "doc", "content": []any{map[string]any{
						"type": "mention", "attrs": map[string]any{"id": "app_sw220_mention", "type": "user"},
					}}}
					first.Comments[0].BodyData = mention
					second.Comments[0].BodyData = mention
				}
				if variant == "empty-id" {
					first.ID, second.ID = "", "other"
				}
				if variant == "normalized-id" {
					first.ID, second.ID = string([]byte{0xff}), string([]byte{0xfe})
				}
				before.ProjectUpdates = map[string][]domain.ProjectUpdate{parent: {first, second}, "nil-parent": nil, "empty-list": {}}
				if variant == "hash-parent-null" {
					before.ProjectUpdates[updateRecordKey(parent, first.ID)] = nil
				}
				if variant == "normalized-parent" {
					before.ProjectUpdates = map[string][]domain.ProjectUpdate{
						string([]byte{0xfe}): {first, second},
						string([]byte{0xff}): {second, first},
					}
				}
				target := cloneBootstrap(before)
				for name, items := range target.ProjectUpdates {
					if len(items) != 0 {
						due := clock.Add(time.Hour)
						items[0].DueAt, items[0].Missing = &due, true
						target.ProjectUpdates[name] = items
					}
				}
				want := scaleP21SW220Run(t, f, view, before, target, parent, variant, false)
				got := scaleP21SW220Run(t, f, view, before, target, parent, variant, true)
				if !reflect.DeepEqual(want.image, got.image) || !reflect.DeepEqual(want.native, got.native) ||
					want.bill != got.bill || want.mentions != got.mentions {
					t.Fatalf("parent-map differs variant=%s bytes=%d image_equal=%v native_equal=%v bill=%d/%d mentions=%d/%d",
						variant, size, reflect.DeepEqual(want.image, got.image), reflect.DeepEqual(want.native, got.native),
						want.bill, got.bill, want.mentions, got.mentions)
				}
				if variant == "canonical-occurrences" && got.mentions != 0 {
					t.Fatal("canonical comments replaced the literal caller-before occurrence matching", got.mentions)
				}
				if size == 32 {
					smallPeak = got.peak
				} else if got.peak > smallPeak+(2<<20) {
					t.Fatal("parent-map native ownership grew with unchanged bodies", smallPeak, got.peak)
				}
				if variant != "deleted-parent" && variant != "normalized-parent" {
					row, found := got.image.Rows[metadataRecordKey{"projectUpdates", parent}]
					var updates []domain.ProjectUpdate
					if !found || json.Unmarshal(row.data, &updates) != nil || len(updates) != 2 ||
						!updates[0].Missing || updates[0].DueAt == nil || updates[1].Missing ||
						len(updates[0].Body) < size || len(updates[1].Body) != size {
						t.Fatal("successful parent row lost first/second duplicate semantics", variant, size)
					}
				}
				t.Logf("variant=%s body_bytes=%d peak=%d SQL_bill=%d", variant, size, got.peak, got.bill)
			}
		})
	}
}

func TestScaleP21SW220ParentSQLStageDeclaration(t *testing.T) {
	base := metadataSQLStageTarget{workspace: "workspace", field: "projectUpdates", searchBytes: -1, dataBytes: 2, projectUpdateParent: true}
	if !base.valid() {
		t.Fatal("legacy empty parent row identity was refused")
	}
	for _, edit := range []func(*metadataSQLStageTarget){
		func(value *metadataSQLStageTarget) { value.projectUpdateParent = false },
		func(value *metadataSQLStageTarget) { value.field = "projects" },
		func(value *metadataSQLStageTarget) { value.field = "initiativeUpdates" },
		func(value *metadataSQLStageTarget) { value.key = strings.Repeat("x", 192) },
		func(value *metadataSQLStageTarget) { value.order = 1 },
		func(value *metadataSQLStageTarget) { value.searchBytes = 0 },
		func(value *metadataSQLStageTarget) { value.workspaceState = true },
	} {
		candidate := base
		edit(&candidate)
		if candidate.valid() {
			t.Fatal("parent-row declaration widened another SQL stage contract", candidate)
		}
	}
}

func TestScaleP21SW220ParentMapCancellationAndRelease(t *testing.T) {
	f, view, before := scaleP21SW77Fixture(t)
	before.ProjectUpdates = map[string][]domain.ProjectUpdate{"ordinary": {{ID: "same", Body: strings.Repeat("x", 300<<10)}, {ID: "same"}}}
	target := cloneBootstrap(before)
	target.ProjectUpdates["ordinary"][0].Missing = true
	for _, phase := range []string{"before", "append"} {
		t.Run(phase, func(t *testing.T) {
			tx, ctx := scaleP21SW77Begin(t, f, before)
			defer tx.Rollback()
			preimage := scaleP21SW77Image(t, tx, before.Workspace.URLKey)
			left, err := captureMetadataLogicalSource(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer left.Release()
			right, err := captureMetadataLogicalSource(ctx, target, left.Stamp())
			if err != nil {
				t.Fatal(err)
			}
			defer right.Release()
			ctx = context.WithValue(ctx, advancedScheduleStreamKey{}, true)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			cancelled := phase == "before"
			if cancelled {
				cancel()
			} else {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.HasPrefix(query, "UPDATE bounded_metadata_write_stage SET data=") {
						cancelled = true
						cancel()
					}
				})
			}
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			if _, err := view.persistLegacyMetadataImage(ctx, tx, before.Workspace.URLKey, left, right); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled parent-map mutation continued", err)
			}
			if !cancelled {
				t.Fatal("SQL append cancellation checkpoint was never reached")
			}
			if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk ||
				metadataLogicalDescriptorsUsed.Load() != descriptors || tx.metadataSQLStages != 0 || len(tx.metadataSQLStageOwners) != 0 {
				t.Fatal("cancelled parent-map mutation retained work/spools")
			}
			if got := scaleP21SW77Image(t, tx, before.Workspace.URLKey); !reflect.DeepEqual(preimage, got) {
				t.Fatal("cancelled staging changed product metadata")
			}
			var stages int
			if err := tx.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM bounded_metadata_write_stage").Scan(&stages); err != nil || stages != 0 {
				t.Fatal("cancelled parent-map SQL scratch escaped", err, stages)
			}
		})
	}
}

func TestScaleP21SW220ParentHashArrayErrorDifferential(t *testing.T) {
	f, view, seed := scaleP21SW77Fixture(t)
	for _, size := range []int{32, 5 << 20} {
		before := cloneBootstrap(seed)
		item := domain.ProjectUpdate{ID: "duplicate", Body: strings.Repeat("x", size)}
		before.ProjectUpdates = map[string][]domain.ProjectUpdate{
			"ordinary": {item, item}, updateRecordKey("ordinary", item.ID): {},
		}
		target := cloneBootstrap(before)
		target.ProjectUpdates["ordinary"][0].Missing = true
		var outcomes []error
		for _, bounded := range []bool{false, true} {
			tx, ctx := scaleP21SW77Begin(t, f, before)
			preimage := scaleP21SW77Image(t, tx, before.Workspace.URLKey)
			ctx = context.WithValue(ctx, advancedScheduleStreamKey{}, true)
			var mutationErr error
			if bounded {
				left, err := captureMetadataLogicalSource(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
				if err != nil {
					t.Fatal(err)
				}
				right, err := captureMetadataLogicalSource(ctx, target, left.Stamp())
				if err != nil {
					left.Release()
					t.Fatal(err)
				}
				image, err := view.persistLegacyMetadataImage(ctx, tx, before.Workspace.URLKey, left, right)
				mutationErr = err
				if image != nil {
					image.Release()
				}
				left.Release()
				right.Release()
			} else {
				prior, next := cloneBootstrap(before), cloneBootstrap(target)
				field, _ := metadataFieldByName("projectUpdates")
				_, _, mutationErr = f.rebaseMetadata(ctx, tx, before.Workspace.URLKey, &prior, &next, []metadataField{field}, nil)
			}
			outcomes = append(outcomes, mutationErr)
			if got := scaleP21SW77Image(t, tx, before.Workspace.URLKey); !reflect.DeepEqual(preimage, got) {
				t.Fatal("hash-parent array error changed product metadata")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		var literal, streamed *json.UnmarshalTypeError
		if !errors.As(outcomes[0], &literal) || !errors.As(outcomes[1], &streamed) ||
			!reflect.DeepEqual(literal, streamed) {
			t.Fatal("hashed parent-array typed error changed", size, outcomes)
		}
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("hash-parent array refusal retained native work")
		}
	}
}

func TestScaleP21SW220ParentMapClosedEligibility(t *testing.T) {
	f, view, seed := scaleP21SW77Fixture(t)
	for _, variant := range []string{"no-marker", "body", "id", "order", "parent", "nil-list", "unique", "long-parent"} {
		t.Run(variant, func(t *testing.T) {
			before := cloneBootstrap(seed)
			parent := "ordinary"
			if variant == "long-parent" {
				parent = strings.Repeat("x", 192)
			}
			before.ProjectUpdates = map[string][]domain.ProjectUpdate{parent: {{ID: "duplicate", Body: "first"}, {ID: "duplicate", Body: "second"}}}
			if variant == "unique" {
				before.ProjectUpdates[parent][1].ID = "other"
			}
			target := cloneBootstrap(before)
			target.ProjectUpdates[parent][0].Missing = true
			switch variant {
			case "body":
				target.ProjectUpdates[parent][0].Body = "changed"
			case "id":
				target.ProjectUpdates[parent][0].ID = "changed"
			case "order":
				target.ProjectUpdates[parent][0], target.ProjectUpdates[parent][1] = target.ProjectUpdates[parent][1], target.ProjectUpdates[parent][0]
			case "parent":
				target.ProjectUpdates["new-parent"] = target.ProjectUpdates[parent]
				delete(target.ProjectUpdates, parent)
			case "nil-list":
				target.ProjectUpdates[parent] = nil
			}
			tx, ctx := scaleP21SW77Begin(t, f, before)
			defer tx.Rollback()
			if variant != "no-marker" {
				ctx = context.WithValue(ctx, advancedScheduleStreamKey{}, true)
			}
			left, err := captureMetadataLogicalSource(ctx, before, copyStamp{workspaceID: before.Workspace.ID})
			if err != nil {
				t.Fatal(err)
			}
			defer left.Release()
			right, err := captureMetadataLogicalSource(ctx, target, left.Stamp())
			if err != nil {
				t.Fatal(err)
			}
			defer right.Release()
			work := &ResourceRead{store: view}
			defer work.Close()
			field, _ := metadataFieldByName("projectUpdates")
			index, _, _ := metadataLogicalField("projectUpdates")
			entry := &legacyMetadataWriteField{field: field, before: left.fields[index], next: right.fields[index]}
			plan := &legacyMetadataWritePlan{store: view, tx: tx, workspace: before.Workspace.URLKey, work: work}
			if err := plan.headers(ctx, entry); err != nil {
				t.Fatal(err)
			}
			handled, err := plan.rebaseProjectUpdateScheduleParentMap(ctx, entry)
			if entry.scheduleParents != nil {
				entry.scheduleParents.Close()
			}
			if err != nil || handled || entry.scheduleParents != nil {
				t.Fatal("closed parent-map profile accepted an unsupported edit", variant, handled, err)
			}
		})
	}
}
