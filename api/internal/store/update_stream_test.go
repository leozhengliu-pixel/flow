package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW209UpdateResult struct {
	before, next map[string][]domain.ProjectUpdate
	row          json.RawMessage
}

func scaleP21SW209UpdateRun(t *testing.T, fixture scaleP09Fixture, bounded *SQLiteStore, before, requested domain.Bootstrap, canonicalBody string, useBounded bool) (scaleP21SW209UpdateResult, int) {
	t.Helper()
	tx, ctx := scaleP21SW77Begin(t, fixture, before)
	defer tx.Rollback()
	ctx = context.WithValue(ctx, advancedScheduleStreamKey{}, true)
	parent := before.Projects[0].ID
	id := before.ProjectUpdates[parent][0].ID
	key := updateRecordKey(parent, id)
	canonical := before.ProjectUpdates[parent][0]
	if canonicalBody != "" {
		canonical.Body = canonicalBody
	}
	item, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(metadataUpdate{Parent: parent, Item: item})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projectUpdates' AND record_key=?`, StripJSONNUL(raw), before.Workspace.URLKey, key); err != nil {
		t.Fatal(err)
	}
	field, _ := metadataFieldByName("projectUpdates")
	result := scaleP21SW209UpdateResult{}
	peak := bounded.MetadataWorkUsedForTesting()
	if !useBounded {
		previous, next := cloneBootstrap(before), cloneBootstrap(requested)
		if _, _, err := fixture.rebaseMetadata(ctx, tx, before.Workspace.URLKey, &previous, &next,
			[]metadataField{field}, map[string][]string{"projectUpdates": {key}}); err != nil {
			t.Fatal(err)
		}
		result.before, result.next = previous.ProjectUpdates, next.ProjectUpdates
		ok, err := writeUpdatesFieldDelta(ctx, tx, before.Workspace.URLKey, newIssueReferences(next), field,
			reflect.ValueOf(previous.ProjectUpdates), reflect.ValueOf(next.ProjectUpdates))
		if err != nil || !ok {
			t.Fatal("literal schedule update fixture did not write", ok, err)
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
		frames, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer frames.Close()
		for _, source := range []*metadataLogicalSource{left, right} {
			for _, storage := range source.stores {
				if err := frames.inheritLocations(storage); err != nil {
					t.Fatal(err)
				}
			}
		}
		owner := &ResourceRead{store: bounded}
		defer owner.Close()
		plan := &legacyMetadataWritePlan{store: bounded, tx: tx, workspace: before.Workspace.URLKey,
			workspaceID: before.Workspace.ID, work: owner, frames: frames, requested: right, actual: right}
		index, _, _ := metadataLogicalField("projectUpdates")
		selected := &legacyMetadataWriteField{field: field, before: left.fields[index], next: right.fields[index]}
		measuring := true
		ctx = WithSQLStatementObserver(ctx, func(string) {
			if measuring {
				peak = max(peak, bounded.MetadataWorkUsedForTesting())
			}
		})
		if err := plan.headers(ctx, selected); err != nil {
			t.Fatal(err)
		}
		if len(selected.rows) != 1 || selected.full {
			t.Fatal("schedule update fixture lost its unique occurrence", len(selected.rows), selected.full)
		}
		handled, err := plan.rebaseProjectUpdateScheduleRow(ctx, selected, 0)
		if !handled || err != nil {
			t.Fatal("schedule update escaped its closed rebase", handled, err)
		}
		row := &selected.rows[0]
		handled, err = plan.writeProjectUpdateScheduleRow(ctx, selected, row)
		if !handled || err != nil {
			t.Fatal("schedule update escaped its closed writer", handled, err)
		}
		measuring = false
		for index, node := range []metadataLogicalNode{row.before, row.next} {
			value, err := node.Value(ctx, int(node.cost))
			if err != nil {
				t.Fatal(err)
			}
			updates := map[string][]domain.ProjectUpdate{parent: {value.Interface().(domain.ProjectUpdate)}}
			if index == 0 {
				result.before = updates
			} else {
				result.next = updates
			}
		}
		owner.Close()
		if bounded.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("streamed update retained work", bounded.MetadataWorkUsedForTesting())
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projectUpdates' AND record_key=?`, before.Workspace.URLKey, key).Scan(&result.row); err != nil {
		t.Fatal(err)
	}
	var scratch int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&scratch); err != nil || scratch != 0 {
		t.Fatal("schedule update retained SQL stage input", scratch, err)
	}
	return result, peak
}

func TestScaleP21SW209ProjectUpdateStreamDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	for _, reload := range []bool{false, true} {
		for _, concurrent := range []bool{false, true} {
			for _, nativeInvalid := range []bool{false, true} {
				smallPeak := 0
				for _, size := range []int{32, 5 << 20} {
					before := cloneBootstrap(fixture.data)
					project := domain.Project{ID: "sw209-" + before.Workspace.ID, Name: "Scheduled project"}
					before.Projects = []domain.Project{project}
					body := strings.Repeat("x", size) + "<tag>&\x00\u2028"
					if nativeInvalid {
						body += string([]byte{0xff})
					}
					before.ProjectUpdates = map[string][]domain.ProjectUpdate{project.ID: {{
						ID: "sw209-update", ProjectID: project.ID, Body: body, User: before.Viewer,
						CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
					}}}
					if reload {
						raw, err := json.Marshal(before)
						if err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(raw, &before); err != nil {
							t.Fatal(err)
						}
					}
					requested := cloneBootstrap(before)
					due := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
					requested.ProjectUpdates[project.ID][0].DueAt = &due
					requested.ProjectUpdates[project.ID][0].Missing = true
					canonical := ""
					if concurrent {
						canonical = "Concurrent canonical body <changed>&\u2028"
					}
					want, _ := scaleP21SW209UpdateRun(t, fixture, bounded, before, requested, canonical, false)
					got, peak := scaleP21SW209UpdateRun(t, fixture, bounded, before, requested, canonical, true)
					if !reflect.DeepEqual(got, want) {
						t.Fatal("schedule changed native/body source choice or SQL", reload, concurrent, nativeInvalid, size)
					}
					if smallPeak == 0 {
						smallPeak = peak
					}
					if peak > smallPeak+(2<<20) {
						t.Fatal("schedule update materialized unchanged Body", smallPeak, peak)
					}
					t.Logf("reloaded=%t concurrent_body=%t native_invalid=%t body_bytes=%d peak_work=%d", reload, concurrent, nativeInvalid, size, peak)
				}
			}
		}
	}
}

func TestScaleP21SW209StreamCounterIsPayloadLength(t *testing.T) {
	var counter legacyProjectByteCounter
	var chunk [metadataRecordChunkBytes]byte
	wanted := metadataWorkBytes + len(chunk)
	for counted := 0; counted < wanted; counted += len(chunk) {
		if _, err := counter.Write(chunk[:]); err != nil {
			t.Fatal("SQL length counter treated streamed bytes as retained heap", counted, err)
		}
	}
	if int(counter) != wanted {
		t.Fatal("streamed SQL payload count changed", counter, wanted)
	}
}
