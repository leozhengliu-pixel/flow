package main

import (
	"context"
	"fmt"
	"go/ast"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strconv"
	"testing"
	"time"

	"flow/api/internal/store"
)

// SW558: the legacy-view lane (the replica's whole-workspace decodes of the
// legacy accessors) has the COLD slot queue's bounded wait. While the lane is
// held, a legacy accessor gives up after FLOW_COLD_QUEUE_TIMEOUT (or at once
// when FLOW_COLD_QUEUE_LIMIT waiters already wait) instead of waiting
// forever, including the accessors that take no context; and a request whose
// accessor was refused is answered 503 + Retry-After instead of the
// handler's "not found", on HOT and COLD routes alike. A success answer is
// never rewritten, a request not refused keeps its own error, and flag off
// nothing changes.
func TestScaleP21SW558LegacyViewLaneBoundedWait(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	view := f.probe
	const timeout = 60 * time.Millisecond
	view.SetLegacyViewQueueForTesting(64, timeout)
	t.Cleanup(func() { view.SetLegacyViewQueueForTesting(64, 15*time.Second) })

	if _, ok := view.WorkspaceMetadata(workspace); !ok {
		t.Fatal("SW558 legacy accessor did not answer with the lane free")
	}

	t.Run("context-less-accessor-times-out", func(t *testing.T) {
		release := view.HoldLegacyViewLaneForTesting()
		defer release()
		refusals, unrecorded := store.LegacyViewRefusalsForTesting(), store.UnrecordedLegacyViewRefusals()
		started := time.Now()
		if _, ok := view.WorkspaceMetadata(workspace); ok {
			t.Fatal("SW558 accessor answered while the lane was held")
		}
		if waited := time.Since(started); waited < timeout || waited > 20*timeout {
			t.Fatalf("SW558 context-less accessor waited %v, want about %v", waited, timeout)
		}
		if _, ok := view.WorkspaceMetadataFields(workspace, "labels"); ok {
			t.Fatal("SW558 field accessor answered while the lane was held")
		}
		if store.LegacyViewRefusalsForTesting()-refusals != 2 || store.UnrecordedLegacyViewRefusals()-unrecorded != 2 {
			t.Fatal("SW558 refusals were not counted as unrecorded")
		}
	})

	t.Run("recorder-and-queue-limit", func(t *testing.T) {
		release := view.HoldLegacyViewLaneForTesting()
		view.SetLegacyViewQueueForTesting(1, time.Minute)
		waiter := make(chan bool)
		go func() {
			_, ok := view.WorkspaceMetadataForRead(context.Background(), workspace)
			waiter <- ok
		}()
		deadline := time.Now().Add(5 * time.Second)
		for view.LegacyViewWaitingForTesting() != 1 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		ctx, refused := store.WithLegacyViewBusyRecorder(context.Background())
		unrecorded := store.UnrecordedLegacyViewRefusals()
		started := time.Now()
		if _, ok := view.WorkspaceMetadataForRead(ctx, workspace); ok || !refused() {
			t.Fatal("SW558 a waiter beyond the queue limit was not refused and recorded")
		}
		if time.Since(started) > time.Second || store.UnrecordedLegacyViewRefusals() != unrecorded {
			t.Fatal("SW558 queue-full refusal waited or was counted unrecorded")
		}
		release()
		if !<-waiter {
			t.Fatal("SW558 the queued waiter was not served once the lane was free")
		}
		view.SetLegacyViewQueueForTesting(64, timeout)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		ctx, refused = store.WithLegacyViewBusyRecorder(cancelled)
		release = view.HoldLegacyViewLaneForTesting()
		if _, ok := view.WorkspaceMetadataForRead(ctx, workspace); ok || refused() {
			t.Fatal("SW558 a cancelled read was recorded as refused")
		}
		release()
	})

	t.Run("http-503", func(t *testing.T) {
		for _, bounded := range []bool{false, true} {
			repo := f.repository
			if bounded {
				repo = f.probe
			}
			s := &server{store: repo}
			mux := http.NewServeMux()
			// A COLD route (missing from the table) and a HOT one.
			mux.HandleFunc("GET /sw558/cold", func(w http.ResponseWriter, r *http.Request) {
				if _, ok := s.store.WorkspaceMetadata(workspace); !ok {
					writeError(w, http.StatusNotFound, "Workspace not found")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			mux.HandleFunc("GET /sw558/context", func(w http.ResponseWriter, r *http.Request) {
				if _, ok := s.store.WorkspaceMetadataForRead(r.Context(), workspace); !ok {
					writeError(w, http.StatusForbidden, "Workspace not found")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			mux.HandleFunc("GET /sw558/success", func(w http.ResponseWriter, r *http.Request) {
				_, _ = s.store.WorkspaceMetadata(workspace)
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("GET /sw558/missing", func(w http.ResponseWriter, r *http.Request) {
				writeError(w, http.StatusNotFound, "Not found")
			})
			handler := s.withColdMetadataRouteMux(mux, mux)
			send := func(path string) *httptest.ResponseRecorder {
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				return recorder
			}
			for _, path := range []string{"/sw558/cold", "/sw558/context"} {
				if got := send(path).Code; got != http.StatusNoContent {
					t.Fatalf("SW558 bounded=%v %s with the lane free: %d", bounded, path, got)
				}
			}
			if !bounded {
				continue // flag off there is no lane: nothing waits
			}
			release := view.HoldLegacyViewLaneForTesting()
			for _, path := range []string{"/sw558/cold", "/sw558/context"} {
				got := send(path)
				if got.Code != http.StatusServiceUnavailable || got.Header().Get("Retry-After") != "5" {
					t.Fatalf("SW558 %s with the lane held: %d Retry-After=%q %s", path, got.Code, got.Header().Get("Retry-After"), got.Body.String())
				}
			}
			if got := send("/sw558/success").Code; got != http.StatusOK {
				t.Fatalf("SW558 a success answer was rewritten: %d", got)
			}
			release()
			if got := send("/sw558/missing"); got.Code != http.StatusNotFound || got.Header().Get("Retry-After") != "" {
				t.Fatalf("SW558 a request not refused lost its own error: %d", got.Code)
			}
		}
	})
	if held := store.ColdSlotsHeldForTesting(); held != 0 {
		t.Fatalf("SW558 %d COLD slots still held", held)
	}
}

// scaleP21SW558LegacyViews are the accessors that, with bounded metadata on,
// decode a whole workspace (or a non-resident field) on the legacy-view lane
// in any context. PagedWorkspaceMetadata and ColdRequestView use the lane
// only inside a COLD context, which a HOT route never has.
var scaleP21SW558LegacyViews = map[string]bool{
	"store/SQLiteStore.WorkspaceMetadata":        true,
	"store/SQLiteStore.WorkspaceMetadataForRead": true,
	"store/SQLiteStore.WorkspaceMetadataFields":  true,
	"store/SQLiteStore.BootstrapFor":             true,
	"store/SQLiteStore.BootstrapForContext":      true,
	"store/SQLiteStore.BootstrapForUser":         true,
}

// scaleP21SW558ResidentFields are the fields the bounded image holds: a
// WorkspaceMetadataFields call naming only these decodes nothing.
var scaleP21SW558ResidentFields = map[string]bool{
	"workspace": true, "viewer": true, "viewerRole": true,
	"teams": true, "teamSettings": true, "workspaceSettings": true, "teamParents": true,
}

// scaleP21SW558HotLegacyViews names HOT entries (a HOT route's handler or a
// middleware every request passes outside the COLD scope) that still reach a
// legacy view with the flag on, with the reason it is acceptable. Empty: a
// HOT request never decodes a whole workspace on the lane.
var scaleP21SW558HotLegacyViews = map[string]string{}

// TestScaleP21SW558HotEntriesReachNoLegacyView walks the SW286 graph from
// every HOT route's handlers and from the request middleware that runs
// outside the COLD route mux (and for HOT routes inside it), and fails on a
// flag-on path to a legacy view that scaleP21SW558HotLegacyViews does not
// name. Background goroutine roots reaching one are logged: their accessors
// now wait at most FLOW_COLD_QUEUE_TIMEOUT.
func TestScaleP21SW558HotEntriesReachNoLegacyView(t *testing.T) {
	graph := scaleP21SW286Load(t)
	out := map[string][]int{}
	roots := map[string][]string{}
	for index, ref := range graph.refs {
		out[ref.from] = append(out[ref.from], index)
		if ref.kind == "route" && ref.pattern != "" && !slices.Contains(roots[ref.pattern], ref.to) {
			roots[ref.pattern] = append(roots[ref.pattern], ref.to)
		}
	}
	for pattern, targets := range scaleP21SW550LocalHandlers(t) {
		if len(roots[pattern]) == 0 {
			roots[pattern] = targets
		}
	}
	resident := func(ref scaleP21SW286Ref) bool {
		if ref.to != "store/SQLiteStore.WorkspaceMetadataFields" {
			return false
		}
		all := true
		for _, ancestors := range graph.refAncestors(ref) {
			call, ok := ancestors[len(ancestors)-2].(*ast.CallExpr)
			if !ok || len(call.Args) < 2 || call.Ellipsis.IsValid() {
				return false
			}
			for _, arg := range call.Args[1:] {
				literal, ok := arg.(*ast.BasicLit)
				name, err := strconv.Unquote(literalValue(literal, ok))
				if !ok || err != nil || !scaleP21SW558ResidentFields[name] {
					all = false
				}
			}
		}
		return all
	}
	walk := func(start string) string {
		paths := map[string]string{start: start}
		queue := []string{start}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, index := range out[current] {
				ref := graph.refs[index]
				if ref.offOnly || ref.kind == "route" || ref.byName || scaleP21SW286Cut(ref) || paths[ref.to] != "" {
					continue
				}
				path := fmt.Sprintf("%s -(%s:%d)-> %s", paths[current], ref.file, ref.line, ref.to)
				if scaleP21SW558LegacyViews[ref.to] && !resident(ref) {
					return path
				}
				paths[ref.to] = path
				queue = append(queue, ref.to)
			}
		}
		return ""
	}
	entries := map[string][]string{}
	for pattern, targets := range roots {
		if scaleP21RouteHot(pattern) {
			entries["route "+pattern] = targets
		}
	}
	for _, middleware := range []string{"server/server.withStaticFiles", "server/boundedLegacyIssueWrites", "server/server.limitFailedAuthentication",
		"server/projectionGate.holdProjections", "server/metadataReadGate.holdProjections", "server/server.withColdMetadataRouteMux",
		"server/server.authenticate", "server/server.limitAuthenticatedAPI", "server/server.codingMainHandler", "server/telemetryHandler"} {
		if graph.funcs[middleware] != nil {
			entries["middleware "+middleware] = []string{middleware}
		}
	}
	reached := map[string]bool{}
	for entry, targets := range entries {
		sort.Strings(targets)
		for _, target := range targets {
			if path := walk(target); path != "" {
				reached[entry] = true
				if scaleP21SW558HotLegacyViews[entry] == "" {
					t.Errorf("SW558 HOT entry %s decodes a whole workspace on the legacy-view lane:\n  %s", entry, path)
				}
				break
			}
		}
	}
	for entry := range scaleP21SW558HotLegacyViews {
		if !reached[entry] {
			t.Errorf("SW558 stale HOT legacy-view entry %s", entry)
		}
	}
	background := 0
	for _, ref := range graph.refs {
		if !ref.async || ref.offOnly || ref.byName || ref.kind == "route" {
			continue
		}
		if path := walk(ref.to); path != "" {
			background++
			if testing.Verbose() {
				t.Logf("SW558 background %s go %s: %s", ref.from, ref.to, path)
			}
		}
	}
	t.Logf("SW558: %d HOT entries checked, %d reach a legacy view; %d goroutine references reach one", len(entries), len(reached), background)
}

func literalValue(literal *ast.BasicLit, ok bool) string {
	if !ok || literal == nil {
		return ""
	}
	return literal.Value
}
