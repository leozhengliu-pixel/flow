package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP12EpochContextsReuseAcrossEvents(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		principal := f.principals[4]
		request := principal.request(f.data.Workspace.URLKey)
		event := domain.RealtimeEvent{ID: "p12-epoch-warm", Type: "issue.updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{}`)}
		frame, _, err := f.service.sharedEventFrame(request, event)
		if err != nil || !frame.Visible() {
			t.Fatalf("warm frame: %v", err)
		}
		frame.Close()
		resolves := 0
		ctx := store.WithSQLStatementObserver(request.Context(), func(query string) {
			if strings.Contains(query, "FROM workspace_memberships m JOIN auth_users") {
				resolves++
			}
		})
		request = request.WithContext(ctx)
		for i := range 5 {
			event.ID = fmt.Sprintf("p12-epoch-event-%d", i)
			frame, _, err = f.service.sharedEventFrame(request, event)
			if err != nil || !frame.Visible() {
				t.Fatalf("cached frame: %v", err)
			}
			frame.Close()
		}
		if resolves != 0 {
			t.Fatalf("unchanged epoch re-resolved principal %d times", resolves)
		}
		authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issue-records/"+f.secret.ID, map[string]string{"title": "Body-only epoch cache edit"}, f.data.Workspace.URLKey, http.StatusOK)
		frame, _, err = f.service.sharedEventFrame(request, event)
		if err != nil || !frame.Visible() || resolves != 0 {
			t.Fatalf("body-only edit lost reusable policy: resolves=%d err=%v", resolves, err)
		}
		frame.Close()
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, principal.user.ID, "member", false); err != nil {
			t.Fatal(err)
		}
		frame, _, err = f.service.sharedEventFrame(request, event)
		defer frame.Close()
		if err != nil || frame.Visible() || resolves != 1 {
			t.Fatalf("revocation did not re-resolve once: visible=%v resolves=%d err=%v", frame.Visible(), resolves, err)
		}
	}
}

func TestScaleP12EpochReadSealsBeforeReadingAuthority(t *testing.T) {
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		entered, release := make(chan struct{}), make(chan struct{})
		var released sync.Once
		var workers sync.WaitGroup
		finish := func() { released.Do(func() { close(release) }) }
		t.Cleanup(func() { finish(); workers.Wait() })
		result := make(chan eventAuthority, 1)
		var cache eventAuthorityReads
		read := func(ctx context.Context) (eventAuthority, error) { return f.service.readEventAuthority(ctx, workspace) }
		workers.Go(func() {
			authority, err := cache.load(t.Context(), workspace, func(ctx context.Context) (eventAuthority, error) {
				authority, err := read(ctx)
				close(entered)
				<-release
				return authority, err
			})
			if err != nil {
				t.Error(err)
			}
			result <- authority
		})
		<-entered
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, f.principals[4].user.ID, "member", false); err != nil {
			t.Fatal(err)
		}
		freshResult := make(chan eventAuthority, 1)
		workers.Go(func() {
			fresh, err := cache.load(t.Context(), workspace, read)
			if err != nil {
				t.Error(err)
			}
			freshResult <- fresh
		})
		var fresh eventAuthority
		select {
		case fresh = <-freshResult:
		case <-time.After(5 * time.Second):
			finish()
			<-result
			<-freshResult
			t.Fatal("a delivery arriving after revocation joined an already-read proof")
		}
		finish()
		previous := <-result
		if fresh.epoch == previous.epoch {
			t.Fatal("a later delivery reused authority from before revocation")
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := f.service.readEventAuthority(cancelled, workspace); err == nil {
			t.Fatal("failed authority read permitted delivery")
		}
	}
}

func TestScaleP12EpochCacheStillRejectsQueuedTeamMoves(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		request := f.principals[2].request(f.data.Workspace.URLKey)
		event := domain.RealtimeEvent{ID: "p12-epoch-move", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
		frame, _, err := f.service.sharedEventFrame(request, event)
		if err != nil || !frame.Visible() {
			t.Fatalf("initial frame: %v", err)
		}
		defer frame.Close()
		authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issue-records/"+f.open.ID, map[string]string{"teamId": f.private}, f.data.Workspace.URLKey, http.StatusOK)
		writer := httptest.NewRecorder()
		if frame.WriteSSE(writer, "") || writer.Body.Len() != 0 {
			t.Fatal("a queued frame survived a team move")
		}
		scaleP12EqualEvent(t, f.service, request, event)
	}
}

func TestScaleP12EpochCacheRetainsKeyExpiry(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		principal := f.principals[5]
		deadline := time.Now().Add(5 * time.Second)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p12.key_expiry", "", nil, func(data *domain.Bootstrap) error {
			for i := range data.APIKeys {
				if data.APIKeys[i].ID == principal.key.ID {
					data.APIKeys[i].ExpiresAt = &deadline
					live := data.APIKeys[i]
					principal.key = &live
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		request := principal.request(f.data.Workspace.URLKey)
		event := domain.RealtimeEvent{ID: "p12-epoch-expiry", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
		frame, _, err := f.service.sharedEventFrame(request, event)
		if err != nil || !frame.Visible() {
			t.Fatalf("initial expiring key frame: %v", err)
		}
		defer frame.Close()
		epoch, err := repo.AuthzEpoch(t.Context(), f.data.Workspace.ID)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Until(deadline) + time.Millisecond)
		current, err := repo.AuthzEpoch(t.Context(), f.data.Workspace.ID)
		if err != nil || current != epoch {
			t.Fatal("expiry unexpectedly changed the workspace epoch")
		}
		writer := httptest.NewRecorder()
		if frame.WriteSSE(writer, "") || writer.Body.Len() != 0 {
			t.Fatal("cached key expiry reached the wire")
		}
		if _, _, err := f.service.sharedEventFrame(request, event); err == nil {
			t.Fatal("expired cached credential was reused")
		}
	}
}

func TestScaleP12EpochLiveCohortDoesNotReadAuthorityPerViewer(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		principal := f.principals[2]
		if _, err := f.service.resolveSyncAccess(principal.request(f.data.Workspace.URLKey)); err != nil {
			t.Fatal(err)
		}
		const viewers = 100
		for range viewers {
			_, stop := f.service.realtime.subscribe(f.data.Workspace.URLKey)
			t.Cleanup(stop)
		}
		for range 10 {
			subscriber, stop := f.service.realtime.subscribeTransport(f.data.Workspace.URLKey, "", true)
			subscriber.setSyncV2QueueLimit()
			t.Cleanup(stop)
		}
		var statements, epochs, failures atomic.Int64
		retained := f.service.realtime.budget.used.Load()
		start := make(chan struct{})
		var workers sync.WaitGroup
		event := domain.RealtimeEvent{ID: "p12-live-cohort", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
		if request := f.service.eventDeliveryRequest(principal.request(f.data.Workspace.URLKey), event); request.Context().Value(eventDeliveryKey{}).(eventDelivery).viewers != viewers {
			t.Fatal("journal streams joined the legacy resource cohort")
		}
		for range viewers {
			workers.Go(func() {
				request := principal.request(f.data.Workspace.URLKey)
				ctx := store.WithSQLStatementObserver(request.Context(), func(query string) {
					statements.Add(1)
					if strings.Contains(query, "workspace_authz_epochs") {
						epochs.Add(1)
					}
				})
				<-start
				w := httptest.NewRecorder()
				if !f.service.writeProjectedRealtime(w, request.WithContext(ctx), event) || w.Body.Len() == 0 || strings.Contains(w.Body.String(), f.secret.ID) {
					failures.Add(1)
				}
			})
		}
		close(start)
		workers.Wait()
		if failures.Load() != 0 || epochs.Load() != 1 || statements.Load() > 32 {
			t.Fatalf("failures=%d epoch reads=%d total SQL=%d", failures.Load(), epochs.Load(), statements.Load())
		}
		if f.service.realtime.budget.used.Load() != retained {
			t.Fatal("a completed fan-out retained frame memory")
		}
	}
}

func TestScaleP12EpochCacheRejectsPeerRevocationWithoutAnInvalidation(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	writer, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	f := newScaleP12Fixture(t, writer)
	reader, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	peer := &server{store: reader, realtime: newRealtimeHub()}
	request := f.principals[4].request(f.data.Workspace.URLKey)
	event := domain.RealtimeEvent{ID: "p12-epoch-peer", Type: "issue.updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{}`)}
	frame, _, err := peer.sharedEventFrame(request, event)
	if err != nil || !frame.Visible() {
		t.Fatalf("initial peer frame: %v", err)
	}
	defer frame.Close()
	if err := writer.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, f.principals[4].user.ID, "member", false); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if frame.WriteSSE(w, "") || w.Body.Len() != 0 {
		t.Fatal("a peer's queued frame survived revocation without a cache invalidation")
	}
	fresh, _, err := peer.sharedEventFrame(request, event)
	defer fresh.Close()
	if err != nil || fresh.Visible() {
		t.Fatalf("stale peer context was reused: visible=%v err=%v", fresh.Visible(), err)
	}
}

func TestScaleP12EpochCacheRejectsCredentialScopeEdits(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		principal := f.principals[5]
		event := domain.RealtimeEvent{ID: "p12-epoch-key-scope", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
		frame, _, err := f.service.sharedEventFrame(principal.request(f.data.Workspace.URLKey), event)
		if err != nil || !frame.Visible() {
			t.Fatalf("initial credential frame: %v", err)
		}
		defer frame.Close()
		var restricted domain.APIKey
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p12.key_scope", "", nil, func(data *domain.Bootstrap) error {
			for i := range data.APIKeys {
				if data.APIKeys[i].ID == principal.key.ID {
					data.APIKeys[i].TeamRestriction = "selected"
					data.APIKeys[i].TeamIDs = []string{}
					restricted = data.APIKeys[i]
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		if frame.WriteSSE(w, "") || w.Body.Len() != 0 {
			t.Fatal("queued frame retained the credential's former scope")
		}
		if stale, _, err := f.service.sharedEventFrame(principal.request(f.data.Workspace.URLKey), event); err == nil {
			stale.Close()
			t.Fatal("stale authenticated credential reused its former cache entry")
		}
		principal.key = &restricted
		scaleP12EqualEvent(t, f.service, principal.request(f.data.Workspace.URLKey), event)
	}
}

func TestScaleP12EpochDeliveryIgnoresAnOlderPageSnapshot(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		request := f.principals[4].request(f.data.Workspace.URLKey)
		ctx, release, err := repo.BeginWorkspaceRead(request.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		request = request.WithContext(ctx)
		authRequest[domain.Issue](t, f.admin, http.MethodPatch, f.host.URL+"/api/issue-records/"+f.open.ID, map[string]string{"title": "Current live body"}, f.data.Workspace.URLKey, http.StatusOK)
		event := domain.RealtimeEvent{ID: "p12-old-page", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)}
		frame, _, err := f.service.sharedEventFrame(request, event)
		if err != nil || !frame.Visible() || !strings.Contains(string(frame.encoded), "Current live body") {
			frame.Close()
			t.Fatalf("page snapshot entered a live source: visible=%v err=%v", frame.Visible(), err)
		}
		frame.Close()
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, f.principals[4].user.ID, "member", false); err != nil {
			t.Fatal(err)
		}
		event.ID, event.AggregateID = "p12-old-page-revoked", f.secret.ID
		frame, _, err = f.service.sharedEventFrame(request, event)
		defer frame.Close()
		if err != nil || frame.Visible() {
			t.Fatalf("page snapshot restored a revoked grant: visible=%v err=%v", frame.Visible(), err)
		}
	}
}
