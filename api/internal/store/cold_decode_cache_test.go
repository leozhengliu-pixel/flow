package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// coldDecodeReference decodes source exactly as the COLD facade did before
// the decode cache: every field whole, then the team directory.
func coldDecodeReference(t *testing.T, source *metadataLogicalSource) domain.Bootstrap {
	t.Helper()
	ctx := coldMetadataGenericDecodeContext(t.Context(), map[string]*metadataLogicalSource{"": source})
	var data domain.Bootstrap
	value := reflect.ValueOf(&data).Elem()
	index := 0
	if err := source.VisitFields(ctx, func(_ string, node metadataLogicalNode) error {
		decoded, err := node.Value(ctx, int(^uint(0)>>1))
		if err != nil {
			return err
		}
		value.Field(index).Set(decoded)
		index++
		return nil
	}); err != nil {
		t.Fatal("reference decode", err)
	}
	domain.RebuildTeamDirectory(&data)
	return data
}

// coldDecodeFixture is the COLD incremental fixture with a projects
// collection large enough for the per-record memo.
func coldDecodeFixture(t *testing.T) (scaleP09Fixture, *SQLiteStore, string) {
	t.Helper()
	f, _, workspace := coldIncrementalFixture(t)
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.cold_decode_seed", "", nil, func(data *domain.Bootstrap) error {
		for i := range 160 {
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("cd-project-%03d", i), Name: fmt.Sprintf("Decode %d", i),
				SlugID: fmt.Sprintf("decode-%d-%012x", i, i), Color: "#223344", Description: strings.Repeat(fmt.Sprintf("decode %d ", i), 80),
				LabelIDs: []string{fmt.Sprintf("ci-label-%d", i%4)}, CreatedAt: at, UpdatedAt: at})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scaleP21CompleteWorkspaceCatalog(t, f, workspace)
	view := coldIncrementalReloadedView(t, f, workspace)
	return f, view, workspace
}

func coldDecodeInstalled(t *testing.T, view *SQLiteStore, workspace string) *metadataLogicalSource {
	t.Helper()
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func coldDecodeAssertEqual(t *testing.T, label string, got, want domain.Bootstrap) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	gv, wv := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := 0; i < gv.NumField(); i++ {
		if !reflect.DeepEqual(gv.Field(i).Interface(), wv.Field(i).Interface()) {
			t.Errorf("%s: field %s differs from the reference decode", label, gv.Type().Field(i).Name)
		}
	}
	t.FailNow()
}

// coldDecodeScribble edits a decoded image in place the ways legacy code may
// edit a facade's own graph: element writes, appends into spare capacity,
// sorts and map writes. None of it may reach the cache.
func coldDecodeScribble(data *domain.Bootstrap) {
	if len(data.Projects) > 1 {
		data.Projects[0].Name = "scribbled"
		data.Projects[0].LabelIDs = append(data.Projects[0].LabelIDs[:0], "scribbled")
		slices.Reverse(data.Projects)
		data.Projects = append(data.Projects[:1], data.Projects[2:]...)
	}
	if len(data.Labels) > 0 {
		data.Labels[0].Color = "#000000"
	}
	if data.Settings != nil {
		data.Settings["scribbled"] = true
	}
	for key := range data.TeamByID {
		data.TeamByID[key] = -1
	}
	if len(data.Users) > 0 {
		data.Users[0].DisplayName = "scribbled"
	}
}

// Every COLD facade, refresh and legacy view decodes exactly what decoding
// its pinned generation whole gives: across COLD writes, HOT writes, peer
// writes reloaded incrementally and in-place edits of earlier copies, and
// later generations decode only the records that changed.
func TestColdDecodeCacheDifferential(t *testing.T) {
	f, view, workspace := coldDecodeFixture(t)
	rng := rand.New(rand.NewPCG(20261119, 7))
	stats := ColdDecodeCacheStatsForTesting()
	check := func(label string) {
		t.Helper()
		source := coldDecodeInstalled(t, view, workspace)
		defer source.Release()
		want := coldDecodeReference(t, source)
		// The facade (and its nested borrow) and a copy edited in place.
		if err := view.withColdMetadataRead(t.Context(), func(ctx context.Context, facade *SQLiteStore) error {
			facade.mu.RLock()
			got := facade.workspaces[workspace]
			facade.mu.RUnlock()
			coldDecodeAssertEqual(t, label+" facade", got, want)
			coldDecodeScribble(&got)
			facade.mu.Lock()
			facade.workspaces[workspace] = got
			facade.mu.Unlock()
			return nil
		}); err != nil {
			t.Fatal(label, err)
		}
		// A legacy view, whole and field-limited, after the edits above.
		legacy, release, handled := view.coldAccessorView(t.Context(), "decode-cache-test", workspace, nil)
		if !handled || legacy == nil {
			t.Fatal(label, "no legacy view")
		}
		legacy.mu.RLock()
		coldDecodeAssertEqual(t, label+" view", legacy.workspaces[workspace], want)
		legacy.mu.RUnlock()
		release()
		limited, release, handled := view.coldAccessorView(t.Context(), "decode-cache-test", workspace, []string{"projects", "trash"})
		if !handled || limited == nil {
			t.Fatal(label, "no field-limited view")
		}
		limitedWant := domain.Bootstrap{Workspace: want.Workspace, Projects: want.Projects, Trash: want.Trash}
		domain.RebuildTeamDirectory(&limitedWant)
		limited.mu.RLock()
		coldDecodeAssertEqual(t, label+" field-limited view", limited.workspaces[workspace], limitedWant)
		limited.mu.RUnlock()
		release()
	}
	check("initial")
	for step := range 40 {
		before := ColdDecodeCacheStatsForTesting()
		kind := "cold"
		switch {
		case step%5 == 1:
			kind = "hot"
			// A HOT writer's generation.
			var closes []func()
			output := WithMetadataOutputOwner(t.Context(), func(close func()) bool {
				closes = append(closes, close)
				return true
			})
			err := view.MutateWorkflowRun(output, workspace, "ci-flow-0", "", "", nil, func(data *domain.Bootstrap) error {
				data.WorkflowRuns = append(data.WorkflowRuns, domain.WorkflowRun{ID: fmt.Sprintf("decode-run-%d", step), WorkflowID: "ci-flow-0", Trigger: "manual", Status: "succeeded", Attempt: 1})
				return nil
			})
			for _, close := range closes {
				close()
			}
			if err != nil && !errors.Is(err, ErrNoMutation) {
				t.Fatalf("step %d HOT run: %v", step, err)
			}
		case step%5 == 3:
			kind = "peer"
			// A peer's write, reloaded as a peer event is.
			if err := f.MutateWorkspace(t.Context(), workspace, "project.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Projects[rng.IntN(len(data.Projects))].Description = fmt.Sprintf("peer %d", step)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal("peer reload", err)
			}
		default:
			writes := coldIncrementalWrites(rng, step)
			write := writes[rng.IntN(len(writes))]
			if step%4 == 0 {
				write = coldIncrementalWrite{"project-edit", func(ctx context.Context) context.Context { return ctx }, "project.updated", func(data *domain.Bootstrap) error {
					data.Projects[rng.IntN(len(data.Projects))].Description = fmt.Sprintf("cold %d", step)
					return nil
				}, "", nil}
			}
			kind = write.name
			ctx := write.ctx(WithColdWork(t.Context(), "cold-decode-test"))
			var err error
			if write.aggregate != "" || write.payload != nil {
				err = view.MutateWorkspace(ctx, workspace, write.event, write.aggregate, write.payload, write.mutate)
			} else {
				err = view.coldLegacyMutation(ctx, workspace, write.event, nil, func(data *domain.Bootstrap) (string, error) {
					return "", write.mutate(data)
				})
			}
			if err != nil {
				t.Fatalf("step %d %s: %v", step, write.name, err)
			}
		}
		check(fmt.Sprintf("step %d", step))
		now := ColdDecodeCacheStatsForTesting()
		t.Logf("step %d %s: records decoded %d reused %d, fields decoded %d reused %d", step, kind,
			now["elementDecodes"]-before["elementDecodes"], now["elementReuses"]-before["elementReuses"],
			now["fieldDecodes"]-before["fieldDecodes"], now["fieldReuses"]-before["fieldReuses"])
	}
	after := ColdDecodeCacheStatsForTesting()
	delta := func(key string) int64 { return after[key] - stats[key] }
	t.Logf("decode cache: %v", map[string]int64{"imageHits": delta("imageHits"), "imageBuilds": delta("imageBuilds"),
		"fieldReuses": delta("fieldReuses"), "fieldDecodes": delta("fieldDecodes"), "elementReuses": delta("elementReuses"), "elementDecodes": delta("elementDecodes")})
	if delta("imageHits") == 0 || delta("fieldReuses") == 0 || delta("elementReuses") == 0 {
		t.Fatal("the decode cache was never reused")
	}
	// Most records are reused across generations.
	if delta("elementDecodes") >= delta("elementReuses") {
		t.Fatalf("%d records decoded again, %d reused", delta("elementDecodes"), delta("elementReuses"))
	}
}

// A facade pinned to an older generation decodes that generation, not the
// cached newer one; the newer one is decoded exactly too, and only the
// installed generation is kept.
func TestColdDecodeCacheStaleGeneration(t *testing.T) {
	_, view, workspace := coldDecodeFixture(t)
	r := view.metadataResidency
	old := coldDecodeInstalled(t, view, workspace)
	defer old.Release()
	oldWant := coldDecodeReference(t, old)
	coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
		data.Projects[3].Description = "newer generation"
		return nil
	})
	current := coldDecodeInstalled(t, view, workspace)
	defer current.Release()
	if current == old {
		t.Fatal("the write installed no new generation")
	}
	want := coldDecodeReference(t, current)
	got, err := r.decodeColdImage(t.Context(), workspace, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	coldDecodeAssertEqual(t, "current", got, want)
	stale, err := r.decodeColdImage(t.Context(), workspace, old, nil)
	if err != nil {
		t.Fatal(err)
	}
	coldDecodeAssertEqual(t, "pinned older generation", stale, oldWant)
	if reflect.DeepEqual(stale.Projects, got.Projects) {
		t.Fatal("the generations do not differ")
	}
	r.coldImages.mu.Lock()
	cached := r.coldImages.images[workspace]
	r.coldImages.mu.Unlock()
	if cached == nil || cached.source != current {
		t.Fatal("the cache does not hold the installed generation")
	}
	again, err := r.decodeColdImage(t.Context(), workspace, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	coldDecodeAssertEqual(t, "current again", again, want)
}

// Concurrent readers of several generations each get their own exact copy,
// whatever the others do to theirs (run with -race).
func TestColdDecodeCacheConcurrentReaders(t *testing.T) {
	_, view, workspace := coldDecodeFixture(t)
	r := view.metadataResidency
	var sources []*metadataLogicalSource
	var wants []domain.Bootstrap
	for i := range 3 {
		source := coldDecodeInstalled(t, view, workspace)
		defer source.Release()
		sources = append(sources, source)
		wants = append(wants, coldDecodeReference(t, source))
		coldIncrementalRun(t, view, workspace, "project.updated", func(data *domain.Bootstrap) error {
			data.Projects[i*7].Description = fmt.Sprintf("concurrent %d", i)
			return nil
		})
	}
	var wait sync.WaitGroup
	errs := make(chan error, 64)
	for reader := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for round := range 6 {
				index := (reader + round) % len(sources)
				got, err := r.decodeColdImage(context.Background(), workspace, sources[index], nil)
				if err != nil {
					errs <- err
					return
				}
				if !reflect.DeepEqual(got, wants[index]) {
					errs <- fmt.Errorf("reader %d round %d: generation %d differs", reader, round, index)
					return
				}
				coldDecodeScribble(&got)
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestColdProjectionSharedImage(t *testing.T) {
	t.Setenv("FLOW_COLD_VIEW_LANES", "3")
	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "268435456")
	_, view, workspace := coldDecodeFixture(t)
	source := coldDecodeInstalled(t, view, workspace)
	defer source.Release()
	image, ok, err := view.metadataResidency.residentImage(t.Context(), workspace, source)
	if !ok || err != nil {
		t.Fatalf("building the shared image: %v", err)
	}
	legacy, release, handled := view.coldProjectionView(t.Context(), "shared-projection-test", workspace)
	if !handled || legacy == nil {
		t.Fatal("missing COLD projection view")
	}
	projects := legacy.workspaces[workspace].Projects
	release()
	if len(projects) == 0 || &projects[0] != &image.data.Projects[0] {
		t.Fatal("COLD projection input copied the shared image")
	}
	want := coldDecodeReference(t, source)
	var wait sync.WaitGroup
	for range 9 {
		wait.Go(func() {
			ctx, scope := WithColdScope(t.Context(), "test COLD projection")
			defer scope.Close()
			data, err := view.PagedWorkspaceMetadata(ctx, workspace, want.Viewer.ID)
			if err != nil || len(data.Projects) == 0 {
				t.Errorf("COLD projection got %d projects: %v", len(data.Projects), err)
				return
			}
			coldDecodeScribble(&data)
		})
	}
	wait.Wait()
	coldDecodeAssertEqual(t, "image after concurrent projections", image.data, want)
}

func TestColdImageCopyReusePreservesIsolation(t *testing.T) {
	data := domain.Bootstrap{Projects: make([]domain.Project, 16384)}
	data.Projects[0] = domain.Project{ID: "source", LabelIDs: []string{"source"}, Lead: &domain.User{ID: "owner"}}
	ordinary := coldImageCopy(context.Background(), data, nil)
	if !reflect.DeepEqual(ordinary, data) {
		t.Fatal("ordinary COLD copy differs from its source")
	}

	ctx, lease, _ := beginColdCopyReuse(context.Background())
	first := coldImageCopy(ctx, data, nil)
	first.Projects[0].ID = "first"
	first.Projects[0].LabelIDs[0] = "first"
	first.Projects[0].Lead.ID = "first"
	if data.Projects[0].ID != "source" || data.Projects[0].LabelIDs[0] != "source" || data.Projects[0].Lead.ID != "owner" {
		t.Fatal("the first COLD copy changed its source")
	}
	lease.Close()

	ctx, lease, _ = beginColdCopyReuse(context.Background())
	second := coldImageCopy(ctx, data, nil)
	if !reflect.DeepEqual(second, data) {
		t.Fatal("reused COLD copy differs from its source")
	}
	second.Projects[0].ID = "second"
	second.Projects[0].LabelIDs[0] = "second"
	second.Projects[0].Lead.ID = "second"
	if data.Projects[0].ID != "source" || data.Projects[0].LabelIDs[0] != "source" || data.Projects[0].Lead.ID != "owner" {
		t.Fatal("the reused COLD copy changed its source")
	}
	lease.Close()
}

// A facade lock held by a panicking COLD callback cannot be acquired during
// scope cleanup. Keep the pooled arrays leased in that SW554 case so they
// cannot be reused while the abandoned facade still points at them.
func TestColdScopeCloseDefersCopyReuseWhenViewLocked(t *testing.T) {
	data := domain.Bootstrap{Projects: make([]domain.Project, 16384)}
	ctx, lease, _ := beginColdCopyReuse(t.Context())
	_ = coldImageCopy(ctx, data, nil)
	lease.mu.Lock()
	bufferCount := len(lease.buffers)
	lease.mu.Unlock()
	if bufferCount == 0 {
		t.Fatal("large COLD copy did not retain pooled storage")
	}

	_, scope := WithColdScope(t.Context(), "copy reuse close")
	view := &SQLiteStore{}
	view.mu.Lock()
	scope.mu.Lock()
	scope.view, scope.viewStore, scope.copyReuse = view, view, lease
	scope.mu.Unlock()
	scope.Close()

	lease.mu.Lock()
	closed, retained := lease.closed, len(lease.buffers)
	lease.mu.Unlock()
	if closed || retained != bufferCount {
		t.Fatalf("locked facade cleanup changed the pooled lease: closed=%t buffers=%d want closed=false buffers=%d", closed, retained, bufferCount)
	}
	view.mu.Unlock()
	lease.Close()
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if !lease.closed || len(lease.buffers) != 0 {
		t.Fatal("explicit cleanup did not release deferred COLD copy storage")
	}
}

// The cache keeps at most its byte budget (least recently used images go
// first), keeps nothing with a budget of 0 or below one image, and drops an
// image left unused for the idle period.
func TestColdDecodeCacheBudgetAndIdle(t *testing.T) {
	_, view, workspace := coldDecodeFixture(t)
	r := view.metadataResidency
	source := coldDecodeInstalled(t, view, workspace)
	defer source.Release()
	want := coldDecodeReference(t, source)
	// A second key holding the same generation, as a second workspace.
	alias := workspace + "-alias"
	if err := source.Retain(); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.logical[alias] = source
	r.mu.Unlock()
	t.Cleanup(func() {
		r.mu.Lock()
		delete(r.logical, alias)
		r.mu.Unlock()
		source.Release()
		r.forgetColdImage("")
	})
	decode := func(key string) {
		t.Helper()
		got, err := r.decodeColdImage(t.Context(), key, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		coldDecodeAssertEqual(t, key, got, want)
	}
	cached := func() (int, int64) {
		r.coldImages.mu.Lock()
		defer r.coldImages.mu.Unlock()
		return len(r.coldImages.images), r.coldImages.bytes
	}

	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "0")
	r.forgetColdImage("")
	decode(workspace)
	if count, _ := cached(); count != 0 {
		t.Fatal("a disabled cache kept an image")
	}
	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "1024")
	decode(workspace)
	if count, _ := cached(); count != 0 {
		t.Fatal("an image larger than the budget was kept")
	}

	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "")
	decode(workspace)
	count, size := cached()
	if count != 1 || size <= 0 {
		t.Fatalf("%d images (%d bytes) cached, want 1", count, size)
	}
	// Room for one image: the alias evicts the least recently used.
	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", fmt.Sprint(size+1))
	evictions := coldDecodeEvictions.Load()
	decode(alias)
	if count, _ := cached(); count != 1 || coldDecodeEvictions.Load() != evictions+1 {
		t.Fatalf("%d images cached after %d evictions, want 1 after 1", count, coldDecodeEvictions.Load()-evictions)
	}
	r.coldImages.mu.Lock()
	_, kept := r.coldImages.images[alias]
	r.coldImages.mu.Unlock()
	if !kept {
		t.Fatal("the newest image was evicted")
	}
	decode(workspace)

	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "")
	t.Setenv("FLOW_COLD_DECODE_CACHE_IDLE", "50ms")
	r.forgetColdImage("")
	decode(workspace)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if count, _ := cached(); count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an idle image was not dropped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	decode(workspace)
}
