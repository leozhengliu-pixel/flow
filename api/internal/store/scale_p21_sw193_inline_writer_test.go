package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW193Field(t *testing.T, repo *SQLiteStore, workspace, name string) json.RawMessage {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), "SELECT data FROM workspace_states WHERE workspace_key=?", workspace).Scan(&raw); err != nil {
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
	if shapes[name] == "array" {
		rows, err := repo.db.QueryContext(t.Context(), "SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key", workspace, name)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		values := []json.RawMessage{}
		for rows.Next() {
			var value json.RawMessage
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			values = append(values, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		value, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, root[name]); err != nil {
		t.Fatal(err)
	}
	return append(json.RawMessage(nil), compact.Bytes()...)
}

func TestScaleP21SW193PulseInlineSettings(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	candidates := scaleP21PulseSummaryCandidates(idgen.New("sw193-pulse-"), 2, true)
	for _, large := range []bool{false, true} {
		var expected any
		for _, mode := range []string{"legacy", "warm", "reloaded"} {
			t.Run(fmt.Sprintf("%t/%s", large, mode), func(t *testing.T) {
				f := scaleP09Database(t)
				scaleP21PulseSummarySeed(t, f, candidates, 2)
				workspace := f.data.Workspace.URLKey
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21.sw193_inline_settings", "", nil, func(data *domain.Bootstrap) error {
					data.Settings["summary-unrelated"] = "retained"
					data.Settings[strings.Repeat("k", 192)] = "overlong-key-retained"
					if large {
						data.Settings["summary-unrelated"] = strings.Repeat("legal inline setting ", 18000)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var rootBytes int
				if err := f.db.QueryRowContext(t.Context(), "SELECT LENGTH(CAST(data AS BLOB)) FROM workspace_states WHERE workspace_key=?", workspace).Scan(&rootBytes); err != nil {
					t.Fatal(err)
				}
				if (rootBytes > MetadataRecordPageMaxBytes) != large {
					t.Fatal("inline root size fixture", rootBytes, large)
				}
				repo := f.SQLiteStore
				var peer *SQLiteStore
				if mode != "legacy" {
					var err error
					repo, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer repo.releaseMetadataLogicalSources()
					if mode == "reloaded" {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					peer, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.releaseMetadataLogicalSources()
				}
				var effects []string
				webhook := func(_ string, event domain.DomainEvent) {
					raw, _ := json.Marshal([]any{"webhook", event.Type, event.Payload, event.PreviousValues, event.Snapshot})
					effects = append(effects, string(raw))
				}
				repo.SetWebhookSink(webhook)
				realtime := func(_ string, event domain.RealtimeEvent) {
					raw, _ := json.Marshal([]any{"realtime", event.Type, event.AggregateID, event.Payload})
					effects = append(effects, string(raw))
				}
				f.SetRealtimeSink(realtime)
				repo.SetRealtimeSink(realtime)
				if peer != nil {
					peer.SetWebhookSink(webhook)
					peer.SetRealtimeSink(realtime)
				}
				sqlCount, rootChunks := 0, 0
				ctx := WithSQLStatementObserver(t.Context(), func(query string) {
					sqlCount++
					if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
						rootChunks++
					}
				})
				run := func() error {
					if mode == "legacy" {
						return scaleP21PulseSummaryLegacy(ctx, repo, workspace, candidates, scaleP21PulseSummaryPrepare)
					}
					return repo.CommitPulseSummaries(ctx, workspace, candidates, scaleP21PulseSummaryPrepare)
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
				count := len(effects)
				if peer != nil {
					if err := peer.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); err != nil || len(effects) != count {
						t.Fatal("stale inline peer duplicated delivery or failed", err, count, len(effects))
					}
					if peer.MetadataWorkUsedForTesting() != 0 || peer.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("stale inline peer leaked work or hydrated metadata")
					}
				}
				if err := run(); err != nil || len(effects) != count {
					t.Fatal("repeated inline cursor tick changed output", err, count, len(effects))
				}
				outcome := []any{scaleP21SW193Field(t, repo, workspace, "settings"), scaleP21PulseSummaryRows(t, f), effects}
				if mode == "legacy" {
					expected = outcome
				} else {
					if !reflect.DeepEqual(expected, outcome) {
						t.Fatal("inline Pulse settings/rows/notifications/events differ from legacy")
					}
					if repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("inline Pulse leaked work or fully hydrated metadata")
					}
					pages := (rootBytes + metadataRecordChunkBytes - 1) / metadataRecordChunkBytes
					if rootChunks > 8*pages+64 {
						t.Fatal("inline Pulse root SQL exceeded fixed-pass chunk bound", rootChunks, pages)
					}
				}
				t.Logf("root_bytes=%d sql_two_ticks=%d root_chunks=%d bound=8*ceil(root/16384)+64", rootBytes, sqlCount, rootChunks)
			})
		}
	}
}

func TestScaleP21SW193PulseInlineCancellationAndBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, cut := range []int{0, 1, 10} {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			f := scaleP09Database(t)
			candidates := scaleP21PulseSummaryCandidates(idgen.New("sw193-cancel-"), 2, true)
			scaleP21PulseSummarySeed(t, f, candidates, 2)
			workspace := f.data.Workspace.URLKey
			if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21.sw193_cancel_seed", "", nil, func(data *domain.Bootstrap) error {
				data.Settings[strings.Repeat("k", 192)] = strings.Repeat("legal inline value ", 18000)
				data.Settings["summary-unrelated"] = "retained"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := scaleP21SW193Field(t, f.SQLiteStore, workspace, "settings")
			rows := scaleP21PulseSummaryRows(t, f)
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			published := 0
			view.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if cut == 0 {
				view.metadataResidency.work.limit = 8 << 20
			} else {
				chunks := 0
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if strings.Contains(query, "SELECT SUBSTR(") && strings.Contains(query, "FROM workspace_states") {
						chunks++
						if chunks == cut {
							cancel()
						}
					}
				})
			}
			err = view.CommitPulseSummaries(ctx, workspace, candidates, scaleP21PulseSummaryPrepare)
			if cut == 0 {
				if !errors.Is(err, ErrMetadataWorkBudget) {
					t.Fatal("inline owner was not independently admitted", err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal("streamed inline cancellation was not propagated", cut, err)
			}
			if published != 0 || view.MetadataWorkUsedForTesting() != 0 ||
				!bytes.Equal(before, scaleP21SW193Field(t, f.SQLiteStore, workspace, "settings")) ||
				!reflect.DeepEqual(rows, scaleP21PulseSummaryRows(t, f)) {
				t.Fatal("refused inline Pulse changed state, published, or leaked work")
			}
		})
	}
}

func TestScaleP21SW193DiscussionInlinePostimage(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	stamp := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
	for _, kind := range []string{"thread-occurrences", "discussion-occurrences", "point", "complete", "split-point", "split-complete"} {
		for _, large := range []bool{false, true} {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			count := 5
			if large && !strings.HasPrefix(kind, "split-") {
				count = 1800
			}
			initial := make([]domain.ThreadSubscription, count)
			for i := range initial {
				initial[i] = domain.ThreadSubscription{
					ID: fmt.Sprintf("sw193-%04d", i), UserID: f.data.Viewer.ID,
					IssueID: "other-issue", CommentID: fmt.Sprintf("comment-%04d", i),
					State: "subscribed", CreatedAt: stamp, UpdatedAt: stamp,
				}
			}
			initial[0].ID, initial[0].IssueID = "target", "selected-issue"
			if !strings.HasPrefix(kind, "split-") {
				initial[1].ID, initial[1].IssueID = "duplicate", "selected-issue"
				initial[2].ID, initial[2].IssueID = "duplicate", "selected-issue"
				initial[3].ID = ""
				initial[4].ID = strings.Repeat("x", 192)
			} else if large {
				if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21.sw193_other_inline", "", nil, func(data *domain.Bootstrap) error {
					data.Settings = map[string]any{strings.Repeat("k", 192): strings.Repeat("unrelated inline value ", 18000)}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			var expected json.RawMessage
			for _, mode := range []string{"legacy", "warm", "reloaded"} {
				t.Run(fmt.Sprintf("%s/%t/%s", kind, large, mode), func(t *testing.T) {
					if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "threadSubscriptions"), workspace, "scale_p21.sw193_seed", "", nil, func(data *domain.Bootstrap) error {
						data.ThreadSubscriptions = append([]domain.ThreadSubscription(nil), initial...)
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if mode == "legacy" {
						if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "threadSubscriptions"), workspace, "scale_p21.sw193_legacy", "", nil, func(data *domain.Bootstrap) error {
							data.ThreadSubscriptions[0].State = "muted"
							data.ThreadSubscriptions[0].UpdatedAt = stamp.Add(time.Hour)
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						expected = scaleP21SW193Field(t, f.SQLiteStore, workspace, "threadSubscriptions")
						return
					}
					view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer view.releaseMetadataLogicalSources()
					if mode == "reloaded" {
						if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					defer source.Release()
					scope := MetadataRecordScope{Write: []RecordKey{{Kind: "threadSubscriptions", ID: "target"}}}
					if kind != "point" && kind != "split-point" {
						scope = MetadataRecordScope{Fields: []string{"threadSubscriptions"}}
					}
					before, err := source.Select(t.Context(), scope, 8<<20)
					if err != nil {
						t.Fatal(err)
					}
					if kind == "thread-occurrences" || kind == "discussion-occurrences" {
						selected := []domain.ThreadSubscription{}
						for _, item := range before.ThreadSubscriptions {
							if item.IssueID == "selected-issue" {
								selected = append(selected, item)
							}
						}
						before.ThreadSubscriptions = selected
						scope = MetadataRecordScope{Write: []RecordKey{{Kind: "threadSubscriptions", ID: "target"}}}
					}
					next := cloneBootstrap(before)
					next.ThreadSubscriptions[0].State = "muted"
					next.ThreadSubscriptions[0].UpdatedAt = stamp.Add(time.Hour)
					ctx := t.Context()
					if kind == "thread-occurrences" {
						ctx = context.WithValue(ctx, discussionThreadTargetKey{}, "selected-issue")
					} else if kind == "discussion-occurrences" {
						ctx = context.WithValue(ctx, issueRecordMutationContext{}, []string{"selected-issue"})
					}
					view.writeMu.Lock()
					defer view.writeMu.Unlock()
					tx, err := view.db.BeginTx(ctx, view.recordTxOptions())
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					base, err := readMetadataStamp(ctx, tx, workspace)
					if err != nil {
						t.Fatal(err)
					}
					publication, err := view.persistDiscussionMetadata(ctx, tx, workspace, source, before, next, scope, base, source.Stamp() == base)
					if err != nil {
						t.Fatal(err)
					}
					defer publication.Close()
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					if err := publication.Install(); err != nil {
						t.Fatal(err)
					}
					if actual := scaleP21SW193Field(t, view, workspace, "threadSubscriptions"); !bytes.Equal(actual, expected) {
						t.Fatal("inline discussion field differs from legacy")
					}
					current, err := view.retainMetadataLogicalSource(t.Context(), workspace)
					if err != nil {
						t.Fatal(err)
					}
					selected, err := current.Select(t.Context(), MetadataRecordScope{Fields: []string{"threadSubscriptions"}}, 8<<20)
					current.Release()
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.Marshal(selected.ThreadSubscriptions)
					if err != nil || !bytes.Equal(raw, expected) {
						t.Fatal("installed inline discussion postimage differs", err)
					}
					if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("inline discussion leaked work or fully hydrated metadata")
					}
				})
			}
		}
	}
}
