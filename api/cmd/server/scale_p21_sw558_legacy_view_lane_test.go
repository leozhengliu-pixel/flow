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

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// SW558: the legacy-view lane (the replica's whole-workspace decodes of the
// legacy accessors) bounds a live request's wait like the COLD slot queue's:
// while the lane is held, a request's accessor gives up after
// FLOW_COLD_QUEUE_TIMEOUT (or at once when FLOW_COLD_QUEUE_LIMIT waiters
// already wait) and the request is answered 503 + Retry-After instead of
// the handler's "not found", on HOT and COLD routes alike. Every other wait
// is bounded only by its own context, as the legacy store waited: background
// COLD work (store.WithColdWork, WorkerContext), work a request left running
// after its handler returned and the accessors that take no context are
// delayed, never answered "not found". The refusal is attributed to exactly
// the refused request: another request's genuine error is kept. A success
// answer is never rewritten, and flag off nothing changes.
func TestScaleP21SW558LegacyViewLaneBoundedWait(t *testing.T) {
	f, workspace := scaleP21SW550Fixture(t)
	view := f.probe
	const timeout = 60 * time.Millisecond
	view.SetLegacyViewQueueForTesting(64, timeout)
	t.Cleanup(func() { view.SetLegacyViewQueueForTesting(64, 15*time.Second) })

	if _, ok := view.WorkspaceMetadata(workspace); !ok {
		t.Fatal("SW558 legacy accessor did not answer with the lane free")
	}
	waitFor := func(waiting int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for view.LegacyViewWaitingForTesting() < waiting && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if view.LegacyViewWaitingForTesting() < waiting {
			t.Fatalf("SW558 %d waiters, want %d", view.LegacyViewWaitingForTesting(), waiting)
		}
	}
	// waits runs read while the lane is held for well past the timeout and
	// requires it to wait and then answer once the lane is free.
	waits := func(name string, read func() bool) {
		t.Helper()
		release := view.HoldLegacyViewLaneForTesting()
		refusals := store.LegacyViewRefusalsForTesting()
		answered := make(chan bool, 1)
		go func() { answered <- read() }()
		select {
		case ok := <-answered:
			release()
			t.Fatalf("SW558 %s answered %v while the lane was held", name, ok)
		case <-time.After(5 * timeout):
		}
		release()
		select {
		case ok := <-answered:
			if !ok {
				t.Fatalf("SW558 %s answered not found after waiting", name)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("SW558 %s did not answer once the lane was free", name)
		}
		if store.LegacyViewRefusalsForTesting() != refusals {
			t.Fatalf("SW558 %s was refused", name)
		}
	}

	t.Run("unbounded-waits", func(t *testing.T) {
		work := store.WithColdWork(context.Background(), "sw558")
		waits("context-less WorkspaceMetadata", func() bool { _, ok := view.WorkspaceMetadata(workspace); return ok })
		waits("context-less WorkspaceMetadataFields", func() bool { _, ok := view.WorkspaceMetadataFields(workspace, "labels"); return ok })
		waits("context-less BootstrapFor", func() bool { _, ok := view.BootstrapFor(workspace); return ok })
		waits("background COLD work", func() bool { _, ok := view.WorkspaceMetadataForRead(work, workspace); return ok })
		waits("worker context", func() bool { _, ok := view.WorkspaceMetadataForRead(view.WorkerContext(), workspace); return ok })
		// COLD work a live request started (it carries the request's marker)
		// is background work.
		live, _, end := store.WithLegacyViewBusyRecorder(context.Background())
		defer end()
		waits("COLD work under a request", func() bool {
			_, ok := view.WorkspaceMetadataForRead(store.WithColdWork(live, "sw558-left"), workspace)
			return ok
		})
		// Work a request left running after its handler returned.
		left, refused, endLeft := store.WithLegacyViewBusyRecorder(context.Background())
		endLeft()
		waits("work a finished request left running", func() bool { _, ok := view.WorkspaceMetadataForRead(left, workspace); return ok })
		if refused() {
			t.Fatal("SW558 a finished request's work was recorded as refused")
		}
		// Beyond the queue limit, background work still waits.
		view.SetLegacyViewQueueForTesting(1, timeout)
		defer view.SetLegacyViewQueueForTesting(64, timeout)
		release := view.HoldLegacyViewLaneForTesting()
		answered := make(chan bool, 2)
		go func() { _, ok := view.WorkspaceMetadataForRead(work, workspace); answered <- ok }()
		waitFor(1)
		go func() { _, ok := view.WorkspaceMetadataForRead(work, workspace); answered <- ok }()
		waitFor(2)
		select {
		case <-answered:
			release()
			t.Fatal("SW558 background work beyond the queue limit answered while the lane was held")
		case <-time.After(5 * timeout):
		}
		release()
		for range 2 {
			if !<-answered {
				t.Fatal("SW558 queued background work answered not found")
			}
		}
		// A background wait still ends with its own context.
		cancelled, cancel := context.WithTimeout(work, timeout)
		defer cancel()
		release = view.HoldLegacyViewLaneForTesting()
		if _, ok := view.WorkspaceMetadataForRead(cancelled, workspace); ok {
			t.Fatal("SW558 a cancelled background read answered")
		}
		release()
	})

	t.Run("request-timeout-queue-limit-and-attribution", func(t *testing.T) {
		ctx, refused, end := store.WithLegacyViewBusyRecorder(context.Background())
		defer end()
		other, otherRefused, endOther := store.WithLegacyViewBusyRecorder(context.Background())
		defer endOther()
		release := view.HoldLegacyViewLaneForTesting()
		started := time.Now()
		if _, ok := view.WorkspaceMetadataForRead(ctx, workspace); ok || !refused() {
			t.Fatal("SW558 a request's wait was not refused and recorded")
		}
		if waited := time.Since(started); waited < timeout || waited > 20*timeout {
			t.Fatalf("SW558 a request waited %v, want about %v", waited, timeout)
		}
		if otherRefused() {
			t.Fatal("SW558 a refusal was recorded on another request")
		}
		view.SetLegacyViewQueueForTesting(1, time.Minute)
		waiter := make(chan bool)
		go func() {
			_, ok := view.WorkspaceMetadataForRead(context.Background(), workspace)
			waiter <- ok
		}()
		waitFor(1)
		started = time.Now()
		if _, ok := view.WorkspaceMetadataForRead(other, workspace); ok || !otherRefused() {
			t.Fatal("SW558 a request beyond the queue limit was not refused and recorded")
		}
		if time.Since(started) > time.Second {
			t.Fatal("SW558 a queue-full refusal waited")
		}
		release()
		if !<-waiter {
			t.Fatal("SW558 the queued waiter was not served once the lane was free")
		}
		view.SetLegacyViewQueueForTesting(64, timeout)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		ctx, refused, end = store.WithLegacyViewBusyRecorder(cancelled)
		defer end()
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
			// A COLD write whose refusal the handler answers as its own error.
			mux.HandleFunc("POST /sw558/slot", func(w http.ResponseWriter, r *http.Request) {
				if err := s.store.MutateWorkspace(r.Context(), workspace, "scale_p21_sw558.label", "", nil, func(data *domain.Bootstrap) error {
					data.Labels = append(data.Labels, domain.IssueLabel{ID: "sw558-slot", Name: "slot"})
					return nil
				}); err != nil {
					writeError(w, http.StatusForbidden, "Workspace not found")
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			mux.HandleFunc("GET /sw558/success", func(w http.ResponseWriter, r *http.Request) {
				_, _ = s.store.WorkspaceMetadataForRead(r.Context(), workspace)
				w.WriteHeader(http.StatusOK)
			})
			missing := make(chan struct{})
			mux.HandleFunc("GET /sw558/missing", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("slow") {
					<-missing
				}
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
			// A request whose genuine error is written while another
			// request is refused keeps its error.
			slow := make(chan *httptest.ResponseRecorder, 1)
			go func() { slow <- send("/sw558/missing?slow=1") }()
			// A context-less accessor of a request waits as before.
			waiting := make(chan *httptest.ResponseRecorder, 1)
			go func() { waiting <- send("/sw558/cold") }()
			waitFor(1)
			got := send("/sw558/context")
			if got.Code != http.StatusServiceUnavailable || got.Header().Get("Retry-After") != "5" {
				t.Fatalf("SW558 /sw558/context with the lane held: %d Retry-After=%q %s", got.Code, got.Header().Get("Retry-After"), got.Body.String())
			}
			close(missing)
			if got := <-slow; got.Code != http.StatusNotFound || got.Header().Get("Retry-After") != "" {
				t.Fatalf("SW558 another request's refusal rewrote a genuine error: %d", got.Code)
			}
			release()
			if got := <-waiting; got.Code != http.StatusNoContent {
				t.Fatalf("SW558 a context-less accessor answered %d after waiting", got.Code)
			}
			if got := send("/sw558/success").Code; got != http.StatusOK {
				t.Fatalf("SW558 a success answer was rewritten: %d", got)
			}
			release = view.HoldLegacyViewLaneForTesting()
			if got := send("/sw558/success").Code; got != http.StatusOK {
				t.Fatalf("SW558 a success answer was rewritten after a refusal: %d", got)
			}
			release()
			// A refused COLD slot wait is recorded on the request too.
			view.SetColdQueueForTesting(64, timeout)
			holding, done := make(chan struct{}), make(chan struct{})
			held := make(chan error, 1)
			go func() {
				held <- view.MutateWorkspace(store.WithColdWork(context.Background(), "sw558-hold"), workspace, "scale_p21_sw558.hold", "", nil, func(*domain.Bootstrap) error {
					close(holding)
					<-done
					return nil
				})
			}()
			<-holding
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/sw558/slot", nil))
			close(done)
			if err := <-held; err != nil {
				t.Fatal(err)
			}
			view.SetColdQueueForTesting(64, 15*time.Second)
			if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") != "5" {
				t.Fatalf("SW558 a refused COLD slot wait answered %d %s", recorder.Code, recorder.Body.String())
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
// wait on the lane for as long as their own context allows.
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
