package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type scaleP21SW197Progress string

func (p scaleP21SW197Progress) HasProject(context.Context, string) (bool, error) {
	return true, nil
}
func (p scaleP21SW197Progress) ForEachProject(ctx context.Context, visit func(string) error) error {
	return visit(string(p))
}
func (p scaleP21SW197Progress) Rebuild(ctx context.Context, _ *sqlTx, _ string, source *metadataLogicalSource, _ time.Time) (*metadataLogicalSource, error) {
	return source, source.Retain()
}

func TestScaleP21SW197ProjectBodyWireEquality(t *testing.T) {
	for _, test := range []struct {
		name, left, right string
		equal             bool
	}{
		{"plain", "same", "same", true},
		{"invalid-native-vs-decoded", "bad-\xff", "bad-\ufffd", false},
		{"html", "<tag>&\u2028", "<tag>&\u2028", true},
		{"nul", "stored\x00body", "storedbody", false},
		{"changed", "before", "after", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer frames.Close()
			left, err := frames.writeValue(reflect.ValueOf(test.left), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			right, err := frames.writeValue(reflect.ValueOf(test.right), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			owner := &ResourceRead{store: view}
			defer owner.Close()
			if err := owner.reserve(t.Context(), 512<<10); err != nil {
				t.Fatal(err)
			}
			got, err := legacyProjectBodyEqual(t.Context(), owner, left, right)
			if err != nil || got != test.equal {
				t.Fatal("streamed equality differs from native Marshal", got, test.equal, err)
			}
		})
	}
}

func TestScaleP21SW197ProjectStoredBodyEquality(t *testing.T) {
	for _, test := range []struct {
		name, native, stored string
	}{
		{"invalid-native-escaped", "bad-\xff", `"bad-\ufffd"`},
		{"invalid-native-decoded", "bad-\xff", `"bad-\ufffd"`},
		{"html", "<tag>&", `"<tag>&"`},
		{"alternate-unicode-escape", "a", `"\u0061"`},
		{"null", "", `null`},
		{"literal-backslash", "\\u0000", `"\\u0000"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(test.stored)
			if test.name == "invalid-native-decoded" {
				raw = []byte("\"bad-\ufffd\"")
			}
			want, err := json.Marshal(json.RawMessage(raw))
			if err != nil {
				t.Fatal(err)
			}
			native, err := json.Marshal(test.native)
			if err != nil {
				t.Fatal(err)
			}
			expected := bytes.Equal(want, native)
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer frames.Close()
			node, err := frames.writeValue(reflect.ValueOf(test.native), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			owner := &ResourceRead{store: view}
			defer owner.Close()
			if err := owner.reserve(t.Context(), 512<<10); err != nil {
				t.Fatal(err)
			}
			got, err := legacyProjectStoredBodyEqual(t.Context(), owner, node, bytes.NewReader(raw), metadataCollectionRange{size: int64(len(raw))})
			if err != nil || got != expected {
				t.Fatal("stored-body equality differs from literal RawMessage compaction", test.name, got, expected, err)
			}
		})
	}
}

func TestScaleP21SW197ProjectStoredNULBoundaries(t *testing.T) {
	for _, pad := range []int{0, metadataRecordChunkBytes - 7, metadataRecordChunkBytes - 3} {
		raw := []byte(`{"description":"` + strings.Repeat("x", pad) + `\u0000\\u0000\"tail","other":"\u0000"}`)
		var output bytes.Buffer
		if err := legacyProjectCopyStoredJSON(t.Context(), &output, bytes.NewReader(raw)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(output.Bytes(), StripJSONNUL(raw)) {
			t.Fatal("streamed NUL stripping changed escaping at a page boundary", pad)
		}
	}
}

func TestScaleP21SW197ProjectSearchProjection(t *testing.T) {
	for _, limit := range []int{0, 1, 17, 100000} {
		project := domain.Project{
			ID: "search", Name: "Unicode \u4e2d\u6587", Summary: "summary",
			Description: "<tag>&\u2028\x00invalid-\xff",
			Status:      domain.ProjectStatus{ID: "status", Name: "Active", Color: "green", Type: "started"},
		}
		original, err := json.Marshal(project)
		if err != nil {
			t.Fatal(err)
		}
		want := capSearchText(metadataSearchText(StripJSONNUL(original)), limit)
		body := project.Description
		project.Description = ""
		header, _ := json.Marshal(project)
		frames, err := newMetadataLogicalBuilder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		node, err := frames.writeValue(reflect.ValueOf(body), 0, false)
		if err != nil {
			frames.Close()
			t.Fatal(err)
		}
		view := &SQLiteStore{db: &sqlDatabase{searchMaxChars: limit}, metadataResidency: newMetadataResidency()}
		owner := &ResourceRead{store: view}
		if err := owner.reserve(t.Context(), 512<<10); err != nil {
			frames.Close()
			t.Fatal(err)
		}
		var got bytes.Buffer
		err = legacyProjectSearchContent(t.Context(), owner, header, node, &got)
		owner.Close()
		frames.Close()
		if err != nil || got.String() != want {
			t.Fatal("streamed search changed map key order, UTF-8, NUL or character cap", limit, err, got.String(), want)
		}
	}
}

func TestScaleP21SW197ProjectCanonicalDescriptionProperties(t *testing.T) {
	for _, raw := range []string{
		`{"id":"p","description":"first","Description":"last","description":null}`,
		`{"id":"p","descrip\u0074ion":"<tag>&\u2028","description":"invalid-\ufffd"}`,
		`{"id":"p","description":null}`,
	} {
		t.Run(raw, func(t *testing.T) {
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer frames.Close()
			view := &SQLiteStore{metadataResidency: newMetadataResidency()}
			owner := &ResourceRead{store: view}
			defer owner.Close()
			if err := owner.reserve(t.Context(), 512<<10); err != nil {
				t.Fatal(err)
			}
			small, body, err := legacyProjectCanonicalHeader(t.Context(), owner, frames, strings.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			var wanted, got domain.Project
			if err := json.Unmarshal([]byte(raw), &wanted); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(small, &got); err != nil {
				t.Fatal(err)
			}
			value, err := body.Value(t.Context(), int(body.cost))
			if err != nil {
				t.Fatal(err)
			}
			got.Description = value.String()
			if !reflect.DeepEqual(got, wanted) {
				t.Fatal("streamed canonical description changed typed property precedence")
			}
		})
	}
}

type scaleP21SW197Outcome struct {
	before, next []domain.Project
	row, search  []byte
	failed       bool
	failure      error
}

func scaleP21SW197Run(t *testing.T, fixture scaleP09Fixture, bounded *SQLiteStore, before, requested domain.Bootstrap, mode string, useBounded bool) (scaleP21SW197Outcome, int) {
	t.Helper()
	tx, ctx := scaleP21SW77Begin(t, fixture, before)
	defer tx.Rollback()
	workspace, id := before.Workspace.URLKey, before.Projects[0].ID
	canonical := cloneSnapshotValue(reflect.ValueOf(before.Projects[0])).Interface().(domain.Project)
	switch mode {
	case "canonical-name", "native-invalid-canonical-name":
		canonical.Name = "Canonical changed name"
	case "canonical-body", "canonical-defaults-body":
		canonical.Description = "Concurrent canonical body <changed>&\u2028"
		if mode == "canonical-defaults-body" {
			canonical.Resources[0].PinnedTeamIDs = nil
		}
	case "count-conflict":
		canonical.IssueCount += 7
	case "display-canonical-name", "display-actor-canonical-name":
		canonical.Name = "Canonical changed project name"
	case "display-canonical-body":
		canonical.Description = "Concurrent description <body>&\u2028"
	case "display-canonical-reference":
		if canonical.Lead != nil {
			canonical.Lead.Name = "Canonical stale reference snapshot"
		}
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "alternate-escapes" {
		raw = bytes.ReplaceAll(raw, []byte(`\u003c`), []byte("<"))
		raw = bytes.ReplaceAll(raw, []byte(`\u003e`), []byte(">"))
		raw = bytes.ReplaceAll(raw, []byte(`\u0026`), []byte("&"))
	}
	if mode == "unknown-canonical" {
		raw = append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"unknown":{"retainedBySQL":true}}`)...)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='projects' AND record_key=?`, StripJSONNUL(raw), workspace, id); err != nil {
		t.Fatal(err)
	}
	field, _ := metadataFieldByName("projects")
	result := scaleP21SW197Outcome{}
	peak := bounded.MetadataWorkUsedForTesting()
	if !useBounded {
		previous, next := cloneBootstrap(before), cloneBootstrap(requested)
		_, _, err = fixture.rebaseMetadata(ctx, tx, workspace, &previous, &next, []metadataField{field}, map[string][]string{"projects": {id}})
		result.failed = errors.Is(err, ErrConcurrentWrite)
		result.failure = err
		if err != nil && !result.failed {
			t.Fatal(err)
		}
		result.before, result.next = previous.Projects, next.Projects
		if err == nil {
			prior, _ := json.Marshal(previous.Projects[0])
			raw, _ := json.Marshal(next.Projects[0])
			refs := newIssueReferences(next)
			if !refs.equalDisplayData(prior, raw) {
				if err := writeMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{"projects", id}, metadataRecord{data: raw, order: 0}, prior); err != nil {
					t.Fatal(err)
				}
			}
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
		plan := &legacyMetadataWritePlan{
			store: bounded, tx: tx, workspace: workspace, workspaceID: before.Workspace.ID,
			work: owner, frames: frames, requested: right, actual: right,
			progress: scaleP21SW197Progress(id),
		}
		if strings.HasPrefix(mode, "display-") {
			plan.progress = nil
		}
		index, _, _ := metadataLogicalField("projects")
		selected := &legacyMetadataWriteField{field: field, before: left.fields[index], next: right.fields[index]}
		measuring := true
		ctx = WithSQLStatementObserver(ctx, func(_ string) {
			if measuring {
				peak = max(peak, bounded.MetadataWorkUsedForTesting())
			}
		})
		if err := plan.headers(ctx, selected); err != nil {
			measuring = false
			t.Fatal(err)
		}
		handled, err := plan.rebaseProjectProgressRow(ctx, selected, 0)
		result.failed = errors.Is(err, ErrConcurrentWrite)
		result.failure = err
		if !handled || err != nil && !result.failed {
			measuring = false
			t.Fatal("progress rebase escaped its closed operation", handled, err)
		}
		row := &selected.rows[0]
		if err == nil {
			handled, err = plan.writeProjectProgressRow(ctx, selected, row, 0)
			if !handled || err != nil {
				measuring = false
				t.Fatal("progress write escaped its closed operation", handled, err)
			}
		}
		measuring = false
		// Verification materialization is outside the measured operation.
		for index, node := range []metadataLogicalNode{row.before, row.next} {
			value, err := node.Value(ctx, int(node.cost))
			if err != nil {
				t.Fatal(err)
			}
			if index == 0 {
				result.before = []domain.Project{value.Interface().(domain.Project)}
			} else {
				result.next = []domain.Project{value.Interface().(domain.Project)}
			}
		}
		owner.Close()
		if bounded.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("streamed progress leaked operation work", bounded.MetadataWorkUsedForTesting())
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&result.row); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT content FROM metadata_search_documents WHERE workspace_key=? AND field='projects' AND record_key=?`, workspace, id).Scan(&result.search); err != nil {
		t.Fatal(err)
	}
	var scratch int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bounded_metadata_write_stage`).Scan(&scratch); err != nil || scratch != 0 {
		t.Fatal("progress SQL scratch survived operation", scratch, err)
	}
	return result, peak
}

func TestScaleP21SW197ProjectProgressLegacyDifferential(t *testing.T) {
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
		for _, mode := range []string{"unchanged", "native-invalid", "nul", "alternate-escapes", "canonical-name", "native-invalid-canonical-name", "canonical-body", "canonical-defaults-body", "unknown-canonical", "count-conflict"} {
			t.Run(mode+map[bool]string{false: "/warm", true: "/reloaded"}[reload], func(t *testing.T) {
				before := cloneBootstrap(fixture.data)
				body := "Original <tag>&\u2028"
				if strings.HasPrefix(mode, "native-invalid") {
					body += "\xff"
				}
				if mode == "nul" {
					body += "\x00tail"
				}
				before.Projects = []domain.Project{{
					ID: "sw197-" + before.Workspace.ID, Name: "Original project", Description: body,
					IssueCount: 11, Progress: 0.25,
					Resources: []domain.ProjectResource{{ID: "resource", PinnedTeamIDs: []string{}}},
					CreatedAt: time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
				}}
				if reload {
					raw, _ := json.Marshal(before)
					if err := json.Unmarshal(raw, &before); err != nil {
						t.Fatal(err)
					}
				}
				next := cloneBootstrap(before)
				next.Projects[0].IssueCount = 13
				next.Projects[0].ProgressHistory = []domain.ProjectProgressHistoryPoint{{Date: before.Projects[0].CreatedAt, Value: 0.5}}
				want, _ := scaleP21SW197Run(t, fixture, bounded, before, next, mode, false)
				got, _ := scaleP21SW197Run(t, fixture, bounded, before, next, mode, true)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("streamed progress changed native postimages, exact SQL, search or conflict")
				}
			})
		}
	}
}

func TestScaleP21SW197ProjectProgressBodyWorkBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	var small int
	for _, size := range []int{32, 5 << 20} {
		before := cloneBootstrap(fixture.data)
		before.Projects = []domain.Project{{ID: "sw197-large-" + before.Workspace.ID, Name: "Large project", Description: strings.Repeat("x", size), IssueCount: 11}}
		next := cloneBootstrap(before)
		next.Projects[0].IssueCount = 13
		want, _ := scaleP21SW197Run(t, fixture, bounded, before, next, "canonical-name", false)
		got, peak := scaleP21SW197Run(t, fixture, bounded, before, next, "canonical-name", true)
		if !reflect.DeepEqual(got, want) {
			t.Fatal("large progress write changed exact native/SQL/search output", size)
		}
		if small == 0 {
			small = peak
		}
		if peak > small+(2<<20) {
			t.Fatal("opaque project description entered progress allowance", small, peak)
		}
		t.Logf("description_bytes=%d peak_work=%d", size, peak)
	}
}

func TestScaleP21SW197ProjectStoredNULCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := legacyProjectCopyStoredJSON(ctx, io.Discard, strings.NewReader(`{"description":"body"}`)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled streamed persistence ignored context", err)
	}
}
