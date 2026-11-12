package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP12JournalFunc func(context.Context, string, string, int, int) (store.CommittedPage, error)

func (read scaleP12JournalFunc) ReadCommittedChanges(ctx context.Context, workspace, after string, limit, maxBytes int) (store.CommittedPage, error) {
	return read(ctx, workspace, after, limit, maxBytes)
}

func scaleP12Changes(t *testing.T, f scaleP12Fixture, principal scaleP12Principal, transactions []store.CommittedChange, next string, hasMore bool) (*httptest.ResponseRecorder, syncV2ChangesResponse) {
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
	journal := scaleP12JournalFunc(func(_ context.Context, workspace, after string, limit, maxBytes int) (store.CommittedPage, error) {
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

func TestScaleP12SummaryBoundsAndPartialRecordContract(t *testing.T) {
	for _, kind := range []string{"issue.updated", "document.updated", "project.updated", "initiative.updated"} {
		t.Run(kind, func(t *testing.T) {
			for _, title := range []string{"Small title", strings.Repeat("quoted \" < > ", 5000)} {
				payload := mustJSON(t, map[string]any{"entity": map[string]any{"id": "record", "version": json.Number("9007199254740993"), "contentVersion": json.Number("9007199254740994"), "title": title, "description": strings.Repeat("secret body", 20000), "content": "editor body", "revisions": []string{"history"}, "permissions": []string{"hidden subject"}}})
				event := domain.RealtimeEvent{ID: "event", Seq: 9007199254740993, Type: kind, AggregateID: "record", ActorID: "actor", Payload: payload}
				frame, err := summarizeEventFrame(EventFrame{encoded: mustJSON(t, event), id: event.ID, visible: true}, "epoch")
				if err != nil {
					t.Fatal(err)
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
			}
		})
	}
}

func TestScaleP12SparseInvisibleChangesAdvanceScannedCursor(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		issue, err := repo.IssueRecord(t.Context(), f.data.Workspace.URLKey, f.secret.ID)
		if err != nil {
			t.Fatal(err)
		}
		transactions := []store.CommittedChange{}
		for i := range 20 {
			transactions = append(transactions, store.CommittedChange{TxID: fmt.Sprintf("sparse-%d", i), Seq: strconv.Itoa(i + 1), Records: []store.RecordDelta{{Key: store.RecordKey{Kind: "issues", ID: issue.ID}, Version: strconv.FormatInt(issue.Version, 10)}}})
		}
		w, result := scaleP12Changes(t, f, f.principals[2], transactions, "4200", false)
		if result.Next != "4200" || result.HasMore || result.ResyncRequired || len(result.Transactions) != 0 || strings.Contains(w.Body.String(), issue.ID) || strings.Contains(w.Body.String(), f.data.Viewer.ID) {
			t.Fatalf("invisible log did not scan safely: %s", w.Body)
		}
	}
}

func TestScaleP12DeltaHasAtomicBudgetAndOversizeInvalidation(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		issue, err := repo.IssueRecord(t.Context(), f.data.Workspace.URLKey, f.open.ID)
		if err != nil {
			t.Fatal(err)
		}
		record := store.RecordDelta{Key: store.RecordKey{Kind: "issues", ID: issue.ID}, Version: strconv.FormatInt(issue.Version, 10)}
		if os.Getenv("FLOW_RECORD_JOURNAL") == "on" {
			version, deleted, err := repo.RecordVersion(t.Context(), f.data.Workspace.URLKey, record.Key)
			if err != nil || deleted || version == "" {
				t.Fatalf("fixture has no committed record version: %s %t %v", version, deleted, err)
			}
			record.Version = version
		}
		_, normal := scaleP12Changes(t, f, f.principals[0], []store.CommittedChange{{TxID: "small", Seq: "1", Records: []store.RecordDelta{record}}}, "1", false)
		if len(normal.Transactions) != 1 || len(normal.Transactions[0].Upserts) != 1 {
			t.Fatal("small delta did not upsert its summary")
		}
		large := store.CommittedChange{TxID: "oversized", Seq: "2"}
		for range 2000 {
			large.Records = append(large.Records, record)
		}
		w, result := scaleP12Changes(t, f, f.principals[0], []store.CommittedChange{large}, "2", false)
		if result.Next != "2" || len(result.Transactions) != 1 || len(result.Transactions[0].Upserts) != 0 || len(result.Transactions[0].Removals) != 0 || len(result.Transactions[0].Invalidations) != 1 || w.Body.Len() > 2048 {
			t.Fatalf("oversize invalidation: %s", w.Body)
		}
		// A stale upsert cannot be labelled as the version in an older journal
		// transaction. The entire transaction becomes an invalidation.
		stale := record
		stale.Version = "0"
		_, result = scaleP12Changes(t, f, f.principals[0], []store.CommittedChange{{TxID: "stale", Seq: "3", Records: []store.RecordDelta{record, stale}}}, "3", false)
		if len(result.Transactions) != 1 || len(result.Transactions[0].Upserts) != 0 || len(result.Transactions[0].Invalidations) == 0 {
			t.Fatal("transaction mixed stale and current versions")
		}
	}
}

func TestScaleP12TombstonesUseRecordedGrantsAndStringVersions(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		authRequest[any](t, f.admin, http.MethodDelete, f.host.URL+"/api/issues/"+f.secret.ID, nil, f.data.Workspace.URLKey, http.StatusNoContent)
		head, err := repo.WorkspaceChangeHead(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		change := store.CommittedChange{TxID: "tombstone", Seq: strconv.FormatInt(head, 10), Records: []store.RecordDelta{{Key: store.RecordKey{Kind: "issues", ID: f.secret.ID}, Version: "9007199254740993", Deleted: true}, {Key: store.RecordKey{Kind: "issues", ID: f.descendant.ID}, Version: "9007199254740994", Deleted: true}}}
		for _, principal := range []scaleP12Principal{f.principals[0], f.principals[2], f.principals[3], f.principals[6]} {
			w, result := scaleP12Changes(t, f, principal, []store.CommittedChange{change}, change.Seq, false)
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

func TestScaleP12RevokeWhileDeltaReadAndDurableReplay(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		principal := f.principals[4]
		before, err := repo.WorkspaceChangeHead(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "issue.updated", f.secret.ID, map[string]string{"title": "Private replay"}, func(*domain.Bootstrap) error { return nil }); err != nil {
			t.Fatal(err)
		}
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, principal.user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		journal := scaleP12JournalFunc(func(context.Context, string, string, int, int) (store.CommittedPage, error) {
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

func TestScaleP12OldServerAndRollbackWireFallback(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		for _, mode := range []string{"off", "on"} {
			t.Setenv("FLOW_SYNC_V2", mode)
			request := f.principals[0].request(f.data.Workspace.URLKey)
			request.Header.Set("X-Flow-Sync", "summary")
			event := domain.RealtimeEvent{ID: "legacy-shape", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{"title":"Title"}`)}
			scaleP12EqualEvent(t, f.service, request, event)
			w := httptest.NewRecorder()
			f.service.syncChanges(w, request)
			if w.Code != http.StatusNotFound {
				t.Fatalf("unavailable journal advertised delta: %d", w.Code)
			}
		}
	}
}
