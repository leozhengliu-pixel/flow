package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW347IssueRowOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, id := f.data.Workspace.URLKey, idgen.New("sw347-row-")
	at := time.Date(2026, 11, 17, 1, 2, 3, 456, time.FixedZone("stored", 5400))
	issue := domain.Issue{
		ID: id, Identifier: "OWN-347", Number: 347, Version: 9,
		Title: "Retained <row>\x00", Description: strings.Repeat("complete row body ", 32768),
		Team: f.data.Teams[0], Creator: f.data.Viewer,
		State:       domain.WorkflowState{ID: "sw347-state", Name: "Original state"},
		Labels:      []domain.IssueLabel{{ID: "sw347-label", Name: "Complete label", Description: "Native label body"}},
		Reactions:   map[string][]string{"like": {"actor", "actor"}},
		Attachments: []domain.Attachment{{ID: "sw347-attachment", Title: "Complete attachment"}},
		CreatedAt:   at, UpdatedAt: at,
	}
	if err := f.ImportIssues(t.Context(), workspace, []domain.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(issue)
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte(" \n"), encoded[:len(encoded)-1]...)
	raw = append(raw, []byte(`,"unknown":{"retained":"raw comparison only"}}`+"\n\t")...)
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	f.db.SetMaxOpenConns(1)
	begin := func(t *testing.T) *sqlTx {
		t.Helper()
		tx, err := f.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		if _, err := tx.ExecContext(t.Context(), `UPDATE issue_records SET data=?,collection_order=-347 WHERE workspace_key=? AND id=?`, raw, workspace, id); err != nil {
			t.Fatal(err)
		}
		return tx
	}
	usable := func(t *testing.T, tx *sqlTx) {
		t.Helper()
		var one int
		if err := tx.QueryRowContext(t.Context(), `SELECT 1`).Scan(&one); err != nil || one != 1 {
			t.Fatal("row/output owner consumed its caller's transaction", err)
		}
	}
	setLimit := func(t *testing.T, limit int) {
		t.Helper()
		budget := view.metadataResidency.work
		budget.mu.Lock()
		original := budget.limit
		budget.limit = limit
		budget.mu.Unlock()
		t.Cleanup(func() {
			budget.mu.Lock()
			budget.limit = original
			budget.mu.Unlock()
		})
	}
	t.Run("compact-raw-and-native-retain-exact-preimages", func(t *testing.T) {
		tx := begin(t)
		want, err := f.loadIssueRecordsByID(t.Context(), tx, workspace, []string{id}, false)
		if err != nil {
			t.Fatal(err)
		}
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		first, err := read.load(t.Context(), []string{id}, false)
		if err != nil || !reflect.DeepEqual(first, want) || !bytes.Equal(first[id].raw, raw) {
			t.Fatal("compaction changed exact raw/native row or physical order", err)
		}
		decodeWork, err := metadataAggregatePayloadJSONWork(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		// A complete decode envelope alone is much larger than the retained
		// row. This also catches keeping the SQL envelope beside compact rows.
		held := view.MetadataWorkUsedForTesting()
		t.Logf("raw_bytes=%d retained_work=%d original_decode_envelope=%d", len(raw), held, decodeWork)
		if held <= len(raw) || uint64(held) >= decodeWork/3 {
			t.Fatal("temporary SQL/decode allowances survived compaction", held, decodeWork)
		}
		second, err := read.load(t.Context(), []string{id}, true)
		if err != nil || !reflect.DeepEqual(first, want) || !reflect.DeepEqual(second, want) {
			t.Fatal("later SQL/decode scratch changed a still-owned row", err)
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		held = view.MetadataWorkUsedForTesting()
		if result, err := read.load(cancelled, []string{id}, false); !errors.Is(err, context.Canceled) || result != nil || view.MetadataWorkUsedForTesting() != held || !reflect.DeepEqual(first, want) || !reflect.DeepEqual(second, want) {
			t.Fatal("cancellation released successful row owners", err)
		}
		read.Close()
		read.Close()
		if len(first) != 0 || len(second) != 0 || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("idempotent close retained raw/native maps or allowances")
		}
		usable(t, tx)
	})
	t.Run("native-compaction-pressure-retains-original-envelope", func(t *testing.T) {
		tx := begin(t)
		want, err := f.loadIssueRecordsByID(t.Context(), tx, workspace, []string{id}, false)
		if err != nil {
			t.Fatal(err)
		}
		baseline := view.newIssueMutationRows(tx, workspace)
		records, err := baseline.load(t.Context(), []string{id}, false)
		if err != nil {
			baseline.Close()
			t.Fatal(err)
		}
		fixed := uint64(reflect.TypeFor[storedIssueRecord]().Size())*2 + 4096
		native, err := metadataAggregateNativeCost(t.Context(), reflect.ValueOf(records[id].issue), 0)
		if err != nil {
			baseline.Close()
			t.Fatal(err)
		}
		decode, err := metadataAggregatePayloadJSONWork(t.Context(), records[id].raw)
		if err != nil {
			baseline.Close()
			t.Fatal(err)
		}
		compactHeld := view.MetadataWorkUsedForTesting()
		// Admit exactly the original decode envelope beside the retained raw
		// row, leaving no room for a second native replacement allowance.
		limit := compactHeld - int(native+fixed) + int(decode+fixed)
		baseline.Close()
		if view.MetadataWorkUsedForTesting() != 0 || limit <= compactHeld || limit >= metadataWorkBytes {
			t.Fatal("pressure fixture cannot isolate the replacement admission", limit, compactHeld)
		}
		setLimit(t, limit)
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		got, err := read.load(t.Context(), []string{id}, false)
		if err != nil || !reflect.DeepEqual(got, want) || !bytes.Equal(got[id].raw, raw) {
			t.Fatal("optional compact admission introduced a refusal or changed the row", err)
		}
		if view.MetadataWorkUsedForTesting() != limit {
			t.Fatal("fallback did not retain its already-admitted complete envelope", view.MetadataWorkUsedForTesting(), limit)
		}
		read.Close()
		read.Close()
		if len(got) != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("fallback envelope survived its row owner")
		}
		usable(t, tx)
	})
	t.Run("cancelled-payload-releases-temporary-sql-owner", func(t *testing.T) {
		tx := begin(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		atPayload := 0
		ctx = WithSQLStatementObserver(ctx, func(query string) {
			if strings.Contains(query, " AS ordinal") {
				atPayload = view.MetadataWorkUsedForTesting()
				cancel()
			}
		})
		read := view.newIssueMutationRows(tx, workspace)
		defer read.Close()
		got, err := read.load(ctx, []string{id}, false)
		if !errors.Is(err, context.Canceled) || got != nil || atPayload == 0 || view.MetadataWorkUsedForTesting() >= atPayload {
			t.Fatal("cancelled SQL payload retained temporary admission", err, atPayload, view.MetadataWorkUsedForTesting())
		}
		read.Close()
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled row owner retained work")
		}
		usable(t, tx)
	})
	t.Run("keep-results-before-commit-through-output-release", func(t *testing.T) {
		tx := begin(t)
		want, err := f.loadIssueRecordsByID(t.Context(), tx, workspace, []string{id}, false)
		if err != nil {
			t.Fatal(err)
		}
		issues := []domain.Issue{want[id].issue}
		affected := []domain.IssueVersion{{ID: id + "-sidecar", Version: 11}}
		before, beforeAffected := slices.Clone(issues), slices.Clone(affected)
		producer := &ResourceRead{store: view}
		defer producer.Close()
		if err := producer.reserve(t.Context(), 4<<20); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var closes []func()
		ctx = WithMetadataOutputOwner(ctx, func(close func()) bool {
			usable(t, tx)
			if view.MetadataWorkUsedForTesting() <= 4<<20 {
				t.Fatal("result registered before its independent admission")
			}
			closes = append(closes, close)
			return true
		})
		if err := view.KeepIssueResults(ctx, issues, affected); err != nil || len(closes) != 1 {
			t.Fatal("precommit native response owner was not registered", err, len(closes))
		}
		if issues[0].AffectedIssues != nil || !reflect.DeepEqual(issues, before) || !reflect.DeepEqual(affected, beforeAffected) {
			t.Fatal("keeping results attached or mutated the post-triage sidecar")
		}
		producer.Close()
		held := view.MetadataWorkUsedForTesting()
		if held <= len(raw) {
			t.Fatal("producer release discarded the independent native response owner", held)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		cancel()
		if view.MetadataWorkUsedForTesting() != held || !reflect.DeepEqual(issues, before) || !reflect.DeepEqual(affected, beforeAffected) {
			t.Fatal("commit/cancellation discarded still-used results or mutated their sidecar")
		}
		closes[0]()
		closes[0]()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("idempotent output release retained response work")
		}
	})
	t.Run("keep-results-refuses-before-commit-without-output-owner", func(t *testing.T) {
		tx := begin(t)
		issues := []domain.Issue{issue}
		affected := []domain.IssueVersion{{ID: id + "-sidecar", Version: 12}}
		beforeAffected := slices.Clone(affected)
		closed := &ResourceRead{store: view}
		closedCtx := closed.metadataOutputContext(t.Context())
		closed.Close()
		for name, ctx := range map[string]context.Context{
			"missing":       t.Context(),
			"nil-registrar": WithMetadataOutputOwner(t.Context(), nil),
			"closed":        closedCtx,
		} {
			t.Run(name, func(t *testing.T) {
				if err := view.KeepIssueResults(ctx, issues, affected); !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("unowned result escaped or retained an allowance", err)
				}
				usable(t, tx)
			})
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		registrations := 0
		cancelled = WithMetadataOutputOwner(cancelled, func(func()) bool { registrations++; return true })
		if err := view.KeepIssueResults(cancelled, issues, affected); !errors.Is(err, context.Canceled) || registrations != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("cancelled result reached output registration", err, registrations)
		}
		setLimit(t, 1)
		ctx := WithMetadataOutputOwner(t.Context(), func(func()) bool { registrations++; return true })
		if err := view.KeepIssueResults(ctx, issues, affected); !errors.Is(err, ErrMetadataWorkBudget) || registrations != 0 || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("precommit result admission failure escaped or retained work", err, registrations)
		}
		if issues[0].AffectedIssues != nil || !reflect.DeepEqual(affected, beforeAffected) {
			t.Fatal("rejected result owner attached or mutated the sidecar")
		}
		usable(t, tx)
	})
}
