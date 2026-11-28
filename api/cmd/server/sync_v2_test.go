package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

type scaleJournalFunc func(context.Context, string, string, int, int) (store.CommittedPage, error)

func (read scaleJournalFunc) ReadCommittedChanges(ctx context.Context, workspace, after string, limit, maxBytes int) (store.CommittedPage, error) {
	return read(ctx, workspace, after, limit, maxBytes)
}

func syncV2ScaleChanges(t *testing.T, f eventSourceScaleFixture, principal scalePrincipal, transactions []store.CommittedChange, next string, hasMore bool) (*httptest.ResponseRecorder, syncV2ChangesResponse) {
	t.Helper()
	r := principal.request(f.data.Workspace.URLKey)
	r.URL.Path = "/api/sync/changes"
	query := r.URL.Query()
	query.Set("after", "0")
	r.URL.RawQuery = query.Encode()
	access, err := f.repository.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, principal.user.ID, principal.key)
	if err != nil {
		t.Fatal(err)
	}
	journal := scaleJournalFunc(func(_ context.Context, workspace, after string, limit, maxBytes int) (store.CommittedPage, error) {
		if workspace != f.data.Workspace.ID || after != "0" || limit <= 0 || maxBytes != syncV2DeltaBytes {
			t.Fatal("journal request escaped its scope or budget")
		}
		return store.CommittedPage{Next: next, Head: next, AuthzEpoch: access.AuthzEpoch, Transactions: transactions, HasMore: hasMore}, nil
	})
	w := httptest.NewRecorder()
	f.service.syncChangesWithJournal(w, r, journal)
	if w.Code != http.StatusOK {
		t.Fatalf("changes: %d %s", w.Code, w.Body)
	}
	if w.Body.Len() > syncV2DeltaBytes {
		t.Fatalf("delta exceeds 256 KiB: %d", w.Body.Len())
	}
	var result syncV2ChangesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return w, result
}

func TestScaleSummaryBoundsAndPartialRecordContract(t *testing.T) {
	for _, kind := range []string{"issue.updated", "document.updated", "project.updated", "initiative.updated"} {
		t.Run(kind, func(t *testing.T) {
			for _, title := range []string{"Small title", strings.Repeat("quoted \" < > ", 5000)} {
				payload := mustJSON(t, map[string]any{"entity": map[string]any{"id": "record", "version": json.Number("9007199254740993"), "contentVersion": json.Number("9007199254740994"), "title": title, "description": strings.Repeat("secret body", 20000), "content": "editor body", "revisions": []string{"history"}, "permissions": []string{"hidden subject"}}})
				event := domain.RealtimeEvent{ID: "event", Seq: 9007199254740993, Type: kind, AggregateID: "record", ActorID: "actor", Payload: payload}
				budget := &realtimeBudget{limit: 1 << 20}
				frame, err := summarizeEventFrame(t.Context(), budget, EventFrame{encoded: mustJSON(t, event), id: event.ID, visible: true}, "epoch")
				if err != nil {
					t.Fatal(err)
				}
				defer frame.Close()
				if budget.used.Load() < int64(frame.Size()) {
					t.Fatal("summary frame lost its output lease")
				}
				if frame.Size()+len(frame.id)+128 > syncV2FrameBytes {
					t.Fatalf("ordinary frame exceeds 16 KiB: %d", frame.Size())
				}
				for _, hidden := range []string{"secret body", "editor body", "history", "hidden subject", `"seq":9007199254740993`, `"version":9007199254740993`} {
					if strings.Contains(string(frame.encoded), hidden) {
						t.Fatalf("summary contains detail or numeric cursor %q", hidden)
					}
				}
				projected, err := frame.Event()
				if err != nil {
					t.Fatal(err)
				}
				var payloadView struct {
					Schema      int               `json:"schema"`
					Transaction syncV2Transaction `json:"transaction"`
				}
				if err := json.Unmarshal(projected.Payload, &payloadView); err != nil {
					t.Fatal(err)
				}
				if payloadView.Schema != 2 || payloadView.Transaction.Seq != "9007199254740993" {
					t.Fatal("schema/sequence contract changed")
				}
				if title == "Small title" {
					if len(payloadView.Transaction.Upserts) != 1 || !payloadView.Transaction.Upserts[0].DetailStale || payloadView.Transaction.Upserts[0].Projection != "summary" {
						t.Fatal("summary did not mark detail stale")
					}
				} else if len(payloadView.Transaction.Upserts) != 0 || len(payloadView.Transaction.Invalidations) != 1 {
					t.Fatal("oversized transaction was partially applied")
				}
				frame.Close()
				if budget.used.Load() != 0 {
					t.Fatal("summary frame lease survived close")
				}
			}
		})
	}
}

func TestScaleSyncV2JSONSizes(t *testing.T) {
	key := syncV2Key{Kind: "issues", ID: "<&\u2028\xff"}
	tx := emptySyncTransaction("tx", "9007199254740993")
	tx.Upserts = append(tx.Upserts, syncV2Upsert{
		Key: key, Version: "1", Data: json.RawMessage(` { "title" : "<&" } `), Projection: "summary", DetailStale: true,
	}, syncV2Upsert{Key: key})
	tx.Removals = append(tx.Removals, syncV2Removal{Key: key, Version: "2"})
	tx.Invalidations = append(tx.Invalidations, "projects", "<")
	for _, page := range []syncV2ChangesResponse{
		{},
		{Transactions: []syncV2Transaction{}},
		{Next: "2", Head: "3", AuthzEpoch: "<&", Transactions: []syncV2Transaction{tx, {}}, HasMore: true, ResyncRequired: true},
	} {
		raw, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if size, err := page.jsonBytes(); err != nil || size != int64(len(raw)) {
			t.Fatalf("page size %d, want %d, err=%v", size, len(raw), err)
		}
	}
	tx.Upserts[0].Data = json.RawMessage{}
	if _, err := tx.jsonBytes(); err == nil {
		t.Fatal("invalid raw JSON accepted")
	}
}

func TestScaleSparseInvisibleChangesAdvanceScannedCursor(t *testing.T) {
	for _, repo := range scaleServerTestStores(t) {
		f := newScaleFixture(t, repo)
		issue, err := repo.IssueRecord(t.Context(), f.data.Workspace.URLKey, f.secret.ID)
		if err != nil {
			t.Fatal(err)
		}
		transactions := []store.CommittedChange{}
		for i := range 20 {
			transactions = append(transactions, store.CommittedChange{TxID: fmt.Sprintf("sparse-%d", i), Seq: strconv.Itoa(i + 1), Records: []store.RecordDelta{{Key: store.RecordKey{Kind: "issues", ID: issue.ID}, Version: strconv.FormatInt(issue.Version, 10)}}})
		}
		w, result := syncV2ScaleChanges(t, f, f.principals[2], transactions, "4200", false)
		if result.Next != "4200" || result.HasMore || result.ResyncRequired || len(result.Transactions) != 0 || strings.Contains(w.Body.String(), issue.ID) || strings.Contains(w.Body.String(), f.data.Viewer.ID) {
			t.Fatalf("invisible log did not scan safely: %s", w.Body)
		}
	}
}

func TestScaleDeltaHasAtomicBudgetAndOversizeInvalidation(t *testing.T) {
	for _, repo := range scaleServerTestStores(t) {
		f := newScaleFixture(t, repo)
		record := syncV2ScaleIssueDelta(t, f)
		_, normal := syncV2ScaleChanges(t, f, f.principals[0], []store.CommittedChange{{TxID: "small", Seq: "1", Records: []store.RecordDelta{record}}}, "1", false)
		if len(normal.Transactions) != 1 || len(normal.Transactions[0].Upserts) != 1 {
			t.Fatal("small delta did not upsert its summary")
		}
		large := store.CommittedChange{TxID: "oversized", Seq: "2"}
		for range 2000 {
			large.Records = append(large.Records, record)
		}
		w, result := syncV2ScaleChanges(t, f, f.principals[0], []store.CommittedChange{large}, "2", false)
		if result.Next != "2" || len(result.Transactions) != 1 || len(result.Transactions[0].Upserts) != 0 || len(result.Transactions[0].Removals) != 0 || len(result.Transactions[0].Invalidations) != 1 || w.Body.Len() > 2048 {
			t.Fatalf("oversize invalidation: %s", w.Body)
		}
		// A stale upsert cannot be labelled as the version in an older journal
		// transaction. The entire transaction becomes an invalidation.
		stale := record
		stale.Version = "0"
		_, result = syncV2ScaleChanges(t, f, f.principals[0], []store.CommittedChange{{TxID: "stale", Seq: "3", Records: []store.RecordDelta{record, stale}}}, "3", false)
		if len(result.Transactions) != 1 || len(result.Transactions[0].Upserts) != 0 || len(result.Transactions[0].Invalidations) == 0 {
			t.Fatal("transaction mixed stale and current versions")
		}
	}
}

func syncV2ScaleIssueDelta(t *testing.T, f eventSourceScaleFixture) store.RecordDelta {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	issue, err := f.repository.IssueRecord(t.Context(), workspace, f.open.ID)
	if err != nil {
		t.Fatal(err)
	}
	record := store.RecordDelta{Key: store.RecordKey{Kind: "issues", ID: issue.ID}, Version: strconv.FormatInt(issue.Version, 10)}
	if f.repository.RecordJournalReady() {
		version, deleted, err := f.repository.RecordVersion(t.Context(), workspace, record.Key)
		if err != nil || deleted || version == "" {
			t.Fatalf("fixture has no committed record version: %s %t %v", version, deleted, err)
		}
		record.Version = version
	}
	return record
}

func TestScaleSyncIssueProjectionBudgetInvalidatesTransaction(t *testing.T) {
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := newScaleFixture(t, repo)
			workspace, principal := f.data.Workspace.URLKey, f.principals[0]
			record := syncV2ScaleIssueDelta(t, f)
			access, err := repo.ResolveEventAccess(t.Context(), workspace, principal.user.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			before := store.CommittedChange{TxID: "before-budget", Seq: "10", Records: []store.RecordDelta{record}}
			blocked := store.CommittedChange{TxID: "over-budget", Seq: "11", Records: []store.RecordDelta{record}, Invalidations: []string{"documents"}}
			after := store.CommittedChange{TxID: "after-budget", Seq: "12", Records: []store.RecordDelta{record}}
			event := domain.RealtimeEvent{ID: blocked.TxID, Seq: 11, Type: "issue.batch_updated",
				Payload: mustJSON(t, map[string]any{"issueIds": []string{record.Key.ID}})}
			source, err := f.service.eventSourceForAuthority(t.Context(), workspace, event, eventAuthority{access.SourceVersion(), access.AuthzEpoch})
			if err != nil {
				t.Fatal(err)
			}
			// Only this source has a small projection budget. Access, source
			// selection, neighboring transactions and page encoding can succeed.
			source.budget = &realtimeBudget{limit: int64(4<<10 + source.policy.Bytes())}
			frame, err := ProjectEvent(t.Context(), source, access)
			frame.Close()
			if !errors.Is(err, store.ErrEventSourceBudget) || source.budget.peak.Load() == 0 {
				t.Fatalf("expected budget exhaustion inside issue projection, got %v", err)
			}

			journal := scaleJournalFunc(func(_ context.Context, workspace, cursor string, limit, maxBytes int) (store.CommittedPage, error) {
				if workspace != access.WorkspaceID || limit != 2 || maxBytes != syncV2DeltaBytes {
					t.Fatal("journal request escaped its scope or budget")
				}
				page := store.CommittedPage{Head: after.Seq, AuthzEpoch: access.AuthzEpoch}
				switch cursor {
				case "9":
					page.Next, page.HasMore, page.Transactions = blocked.Seq, true, []store.CommittedChange{before, blocked}
				case blocked.Seq:
					page.Next, page.Transactions = after.Seq, []store.CommittedChange{after}
				default:
					t.Fatalf("unexpected journal cursor %q", cursor)
				}
				return page, nil
			})
			read := func(cursor string) syncV2ChangesResponse {
				t.Helper()
				r := principal.request(workspace)
				query := r.URL.Query()
				query.Set("after", cursor)
				query.Set("limit", "2")
				r.URL.RawQuery = query.Encode()
				w := httptest.NewRecorder()
				f.service.syncChangesWithJournal(w, r, journal)
				var result syncV2ChangesResponse
				if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
					t.Fatalf("changes from %s: %d %s", cursor, w.Code, w.Body)
				}
				return result
			}
			first := read("9")
			if first.Next != blocked.Seq || first.Head != after.Seq || !first.HasMore || first.ResyncRequired || len(first.Transactions) != 2 {
				t.Fatalf("projection budget changed the page boundary: %+v", first)
			}
			normal, invalidated := first.Transactions[0], first.Transactions[1]
			if normal.TxID != before.TxID || len(normal.Upserts) != 1 || invalidated.TxID != blocked.TxID || invalidated.Seq != blocked.Seq ||
				len(invalidated.Upserts) != 0 || len(invalidated.Removals) != 0 || !slices.Equal(invalidated.Invalidations, []string{"documents", "issues"}) {
				t.Fatalf("expected complete transactions and an atomic invalidation: %+v", first.Transactions)
			}
			next := read(first.Next)
			if next.Next != after.Seq || next.HasMore || next.ResyncRequired || len(next.Transactions) != 1 ||
				next.Transactions[0].TxID != after.TxID || len(next.Transactions[0].Upserts) != 1 {
				t.Fatalf("next page skipped an undelivered transaction: %+v", next)
			}
			if used := source.budget.used.Load(); used != 0 {
				t.Fatalf("failed projection leaked %d bytes", used)
			}
		})
	}
}

type syncV2WriteObserver struct {
	*httptest.ResponseRecorder
	observe func([]byte)
	err     error
}

func (w *syncV2WriteObserver) Write(raw []byte) (int, error) {
	w.observe(raw)
	if w.err != nil {
		return 0, w.err
	}
	return w.ResponseRecorder.Write(raw)
}

func TestScaleSyncPageKeepsSummariesChargedThroughWrite(t *testing.T) {
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := newScaleFixture(t, repo)
			workspace, principal := f.data.Workspace.URLKey, f.principals[0]
			record := syncV2ScaleIssueDelta(t, f)
			access, err := repo.ResolveEventAccess(t.Context(), workspace, principal.user.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			page := store.CommittedPage{Next: "2", Head: "2", AuthzEpoch: access.AuthzEpoch, Transactions: []store.CommittedChange{
				{TxID: "retained-1", Seq: "1", Records: []store.RecordDelta{record}},
				{TxID: "retained-2", Seq: "2", Records: []store.RecordDelta{record}},
			}}
			journal := scaleJournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
				return page, nil
			})
			budget := f.service.realtime.budget
			baseline := budget.used.Load()
			for _, tc := range []struct {
				name string
				err  error
			}{{"success", nil}, {"write-error", io.ErrClosedPipe}} {
				t.Run(tc.name, func(t *testing.T) {
					r := principal.request(workspace)
					query := r.URL.Query()
					query.Set("after", "0")
					r.URL.RawQuery = query.Encode()
					writes := 0
					w := &syncV2WriteObserver{ResponseRecorder: httptest.NewRecorder(), err: tc.err, observe: func(raw []byte) {
						writes++
						var result syncV2ChangesResponse
						if err := json.Unmarshal(raw, &result); err != nil || result.Next != "2" || len(result.Transactions) != 2 {
							t.Fatalf("expected two projected transactions: %s (%v)", raw, err)
						}
						var bytes int64
						for _, tx := range result.Transactions {
							if len(tx.Upserts) != 1 {
								t.Fatalf("expected a summary: %s", raw)
							}
							bytes += int64(len(tx.Upserts[0].Data))
						}
						if held := budget.used.Load() - baseline; held < bytes {
							t.Errorf("write held %d realtime bytes for %d live summary bytes", held, bytes)
						}
					}}
					f.service.syncChangesWithJournal(w, r, journal)
					if writes != 1 || w.Header().Get("X-Flow-Sync") != "delta" {
						t.Fatalf("expected one delta write, got %d", writes)
					}
					if used := budget.used.Load(); used != baseline {
						t.Fatalf("write leaked reservations: got %d, started at %d", used, baseline)
					}
				})
			}
		})
	}
}

func TestScaleSyncSharedPageKeepsSummariesCharged(t *testing.T) {
	for driver, repo := range scaleServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := newScaleFixture(t, repo)
			workspace, principal := f.data.Workspace.URLKey, f.principals[0]
			access, err := repo.ResolveEventAccess(t.Context(), workspace, principal.user.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			redis := miniredis.RunT(t)
			coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: "sync-page-work",
				Cache: coordination.SharedCacheConfig{SyncPage: true, OpTimeout: time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			defer coordinator.Close()
			service := &server{store: repo, coordinator: coordinator, realtime: newRealtimeHub()}
			page := store.CommittedPage{Next: "1", Head: "1", AuthzEpoch: access.AuthzEpoch, Transactions: []store.CommittedChange{
				{TxID: "shared-summary", Seq: "1", Records: []store.RecordDelta{syncV2ScaleIssueDelta(t, f)}},
			}}
			r := principal.request(workspace)
			for _, phase := range []string{"compute", "cache-hit"} {
				t.Run(phase, func(t *testing.T) {
					encoding := service.syncPageEncoding(t.Context())
					defer encoding.close()
					computed := false
					project := func(ctx context.Context) (syncV2ChangesResponse, error) {
						computed = true
						result := syncV2ChangesResponse{Next: "0", Head: page.Head, AuthzEpoch: page.AuthzEpoch, Transactions: []syncV2Transaction{}}
						return service.projectSyncPage(r.WithContext(ctx), access, page, result, &encoding)
					}
					result, err := service.sharedSyncPage(t.Context(), access, page, "0", 1, &encoding, project)
					if err != nil || computed != (phase == "compute") || len(result.Transactions) != 1 || len(result.Transactions[0].Upserts) != 1 {
						t.Fatalf("expected a summary at %s, computed=%t result=%+v err=%v", phase, computed, result, err)
					}
					if held, want := service.realtime.budget.used.Load(), int64(cap(result.Transactions[0].Upserts[0].Data)); held < want {
						t.Fatalf("held %d realtime bytes for %d live summary bytes", held, want)
					}
					encoding.close()
					if used := service.realtime.budget.used.Load(); used != 0 {
						t.Fatalf("shared page leaked %d bytes", used)
					}
				})
			}
		})
	}
}

func TestScaleTombstonesUseRecordedGrantsAndStringVersions(t *testing.T) {
	for _, repo := range scaleServerTestStores(t) {
		f := newScaleFixture(t, repo)
		authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/issues/"+f.secret.ID, nil, f.data.Workspace.URLKey, http.StatusNoContent)
		head, err := repo.WorkspaceChangeHead(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		change := store.CommittedChange{TxID: "tombstone", Seq: strconv.FormatInt(head, 10), Records: []store.RecordDelta{{Key: store.RecordKey{Kind: "issues", ID: f.secret.ID}, Version: "9007199254740993", Deleted: true}, {Key: store.RecordKey{Kind: "issues", ID: f.descendant.ID}, Version: "9007199254740994", Deleted: true}}}
		for _, principal := range []scalePrincipal{f.principals[0], f.principals[2], f.principals[3], f.principals[6]} {
			w, result := syncV2ScaleChanges(t, f, principal, []store.CommittedChange{change}, change.Seq, false)
			if principal.name == "admin" || principal.name == "guest-explicit-grant" {
				if len(result.Transactions) != 1 || len(result.Transactions[0].Removals) != 2 || result.Transactions[0].Removals[0].Version != "9007199254740993" {
					t.Fatalf("authorized tombstone: %s", w.Body)
				}
			} else if len(result.Transactions) != 0 || strings.Contains(w.Body.String(), f.secret.ID) {
				t.Fatalf("tombstone disclosed a private identifier: %s", w.Body)
			}
		}
	}
}

func TestScaleRevokeWhileDeltaReadAndDurableReplay(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleServerTestStores(t) {
		f := newScaleFixture(t, repo)
		principal := f.principals[4]
		before, err := repo.WorkspaceChangeHead(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(workspaceFixtureContext(t.Context()), f.data.Workspace.URLKey, "issue.updated", f.secret.ID, map[string]string{"title": "Private replay"}, func(*domain.Bootstrap) error { return nil }); err != nil {
			t.Fatal(err)
		}
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, principal.user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		journal := scaleJournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, principal.user.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			return store.CommittedPage{Next: "10", Head: "10", AuthzEpoch: access.AuthzEpoch, Transactions: []store.CommittedChange{{TxID: "revoked", Seq: "10", Invalidations: []string{"issues"}}}}, nil
		})
		r := principal.request(f.data.Workspace.URLKey)
		query := r.URL.Query()
		query.Set("after", "0")
		r.URL.RawQuery = query.Encode()
		w := httptest.NewRecorder()
		f.service.syncChangesWithJournal(w, r, journal)
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "transactions") {
			t.Fatalf("in-flight revoke: %d %s", w.Code, w.Body)
		}
		stream := httptest.NewRecorder()
		cursor, ok := f.service.replayChanges(stream, principal.request(f.data.Workspace.URLKey), f.data.Workspace.ID, before, stream)
		if !ok || cursor <= before || strings.Contains(stream.Body.String(), f.secret.ID) || strings.Contains(stream.Body.String(), "Private replay") {
			t.Fatalf("revoked replay cursor=%d ok=%v body=%s", cursor, ok, stream.Body)
		}
	}
}

func TestScaleOldServerAndRollbackWireFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, repo := range scaleServerTestStores(t) {
		f := newScaleFixture(t, repo)
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_SYNC_V2", mode)
			request := f.principals[0].request(f.data.Workspace.URLKey)
			request.Header.Set("X-Flow-Sync", "summary")
			event := domain.RealtimeEvent{ID: "legacy-shape", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{"title":"Title"}`)}
			scaleEqualEvent(t, f.service, request, event)
			w := httptest.NewRecorder()
			f.service.syncChanges(w, request)
			if w.Code != http.StatusNotFound {
				t.Fatalf("unavailable journal advertised delta: %d", w.Code)
			}
		}
	}
}

func TestScaleSyncRaceProofAndCoverageReset(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleServerTestStores(t) {
		f := setupPolicyDifferential(t, repo)
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		request := f.literalRequest(f.data.Viewer, nil, nil)
		service := &server{store: repo}
		if got := service.syncChangesProjectionError(request, access, store.ErrAuthForbidden); errors.Is(got, errRealtimeProjectionStale) {
			t.Fatal("bare authorization denial was classified as a source race")
		}
		beforeEpoch := access.AuthzEpoch
		authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "sync race moved", "teamId": f.public}, f.data.Workspace.URLKey, http.StatusCreated)
		afterEpoch, err := repo.AuthzEpoch(t.Context(), f.data.Workspace.ID)
		if err != nil || afterEpoch != beforeEpoch {
			t.Fatalf("body write changed authorization epoch: before=%q after=%q err=%v", beforeEpoch, afterEpoch, err)
		}
		stale := service.syncChangesProjectionError(request, access, store.ErrAuthForbidden)
		if !errors.Is(stale, errRealtimeProjectionStale) {
			t.Fatal("source movement did not acquire the stale proof")
		}
		journal := scaleJournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
			return store.CommittedPage{
				Head:       "7",
				AuthzEpoch: access.AuthzEpoch,
			}, store.ErrRecordJournalCoverage
		})
		result, ok := service.syncChangesResyncAfterRace(request, access, journal, "0", stale)
		if !ok || !result.ResyncRequired || result.Next != "0" || result.Head != "7" || result.AuthzEpoch != access.AuthzEpoch || result.HasMore || len(result.Transactions) != 0 {
			t.Fatalf("coverage-gap reset was not authenticated: ok=%v result=%+v", ok, result)
		}

		settledAccess, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, authzEpoch, err := repo.RealtimeSourceVersionAndEpoch(t.Context(), f.data.Workspace.URLKey)
		if err != nil || authzEpoch != settledAccess.AuthzEpoch {
			t.Fatalf("settled stale-source setup moved authorization: epoch=%q expected=%q err=%v", authzEpoch, settledAccess.AuthzEpoch, err)
		}
		settledJournal := scaleJournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
			return store.CommittedPage{
				Head:       "8",
				AuthzEpoch: settledAccess.AuthzEpoch,
			}, nil
		})
		settled, ok := service.syncChangesResyncAfterRace(request, settledAccess, settledJournal, "7", errLiveEventSourceStale)
		if !ok || !settled.ResyncRequired || settled.Next != "7" || settled.Head != "8" ||
			settled.AuthzEpoch != settledAccess.AuthzEpoch || settled.HasMore || len(settled.Transactions) != 0 {
			t.Fatalf("settled stale source did not produce an authenticated empty reset: ok=%v result=%+v", ok, settled)
		}

		authzPrincipal := f.principals[2]
		authzRequest := f.literalRequest(authzPrincipal.user, nil, nil)
		authzAccess, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, authzPrincipal.user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		beforeAuthzEpoch := authzAccess.AuthzEpoch
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.public, authzPrincipal.user.ID, "member", false); err != nil {
			t.Fatal(err)
		}
		afterAuthzEpoch, err := repo.AuthzEpoch(t.Context(), f.data.Workspace.ID)
		if err != nil || afterAuthzEpoch == beforeAuthzEpoch {
			t.Fatalf("membership change did not move authorization epoch: before=%q after=%q err=%v", beforeAuthzEpoch, afterAuthzEpoch, err)
		}
		if service.syncChangesResetAllowed(authzRequest, authzAccess, errLiveEventSourceStale) {
			t.Fatal("stale source reset crossed an authorization epoch change")
		}
	}
}
