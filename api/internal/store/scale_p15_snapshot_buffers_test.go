package store

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP15SnapshotBuffersPreserveIsolation(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	data := domain.Bootstrap{Projects: make([]domain.Project, 2048), Settings: map[string]any{"nested": []any{"original"}}}
	data.Projects[0] = domain.Project{ID: "original", MemberIDs: []string{"member"}, Lead: &domain.User{ID: "owner"}}
	ctx, release := BeginHydratedSnapshotReuse(t.Context())
	first := CloneHydratedReadSnapshot(ctx, data)
	second := CloneHydratedReadSnapshot(ctx, data)
	first.Projects[0].ID = "changed"
	first.Projects[0].MemberIDs[0] = "changed"
	first.Projects[0].Lead.ID = "changed"
	first.Settings["nested"].([]any)[0] = "changed"
	if data.Projects[0].ID != "original" || second.Projects[0].ID != "original" || data.Projects[0].MemberIDs[0] != "member" || second.Projects[0].Lead.ID != "owner" || second.Settings["nested"].([]any)[0] != "original" {
		t.Fatal("a read snapshot changed another snapshot or its source")
	}
	release()
	release()
	// A closed lifetime must return an ordinary owned copy.
	late := CloneHydratedReadSnapshot(ctx, data)
	if !reflect.DeepEqual(late, data) {
		t.Fatal("closed snapshot lifetime changed clone semantics")
	}
	ctx, release = BeginHydratedSnapshotReuse(t.Context())
	defer release()
	reused := CloneHydratedReadSnapshot(ctx, data)
	if !reflect.DeepEqual(reused, data) {
		t.Fatal("reused storage retained another viewer's fields")
	}
}

func TestScaleP15SnapshotBufferCacheIsBoundedAndCleared(t *testing.T) {
	pool := &hydrationSnapshotBuffers{}
	value := reflect.ValueOf(make([]*int, 1024))
	marker := 42
	value.Index(0).Set(reflect.ValueOf(&marker))
	pool.put(value)
	reused := pool.take(value.Type(), 1024)
	if !reused.Index(0).IsNil() || pool.bytes != 0 {
		t.Fatal("cached backing storage retained a source reference")
	}
	pool.bytes = hydrationSnapshotBufferLimit
	pool.put(reused)
	if len(pool.idle) != 0 || pool.bytes != hydrationSnapshotBufferLimit {
		t.Fatal("snapshot buffers exceeded the shared idle-byte cap")
	}
	pool = &hydrationSnapshotBuffers{}
	pool.put(reflect.ValueOf(make([]*int, 8192)))
	smaller := pool.take(value.Type(), 1024)
	if smaller.Cap() > 2048 || pool.bytes != 0 || len(pool.idle) != 0 {
		t.Fatal("a smaller workspace retained oversized inactive storage")
	}
}

func TestScaleP15SnapshotBuffersConcurrentLifetimes(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	data := domain.Bootstrap{Projects: make([]domain.Project, 2048)}
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			ctx, release := BeginHydratedSnapshotReuse(context.Background())
			defer release()
			owned := CloneHydratedReadSnapshot(ctx, data)
			owned.Projects[0].ID = "owned"
			if data.Projects[0].ID != "" {
				t.Error("concurrent snapshot changed its source")
			}
		})
	}
	readers.Wait()
}

func TestScaleP15SnapshotReuseDefaultsOff(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "")
	ctx, release := BeginHydratedSnapshotReuse(t.Context())
	defer release()
	if ctx.Value(hydrationSnapshotReuseKey{}) != nil {
		t.Fatal("default-off reads acquired reusable snapshot storage")
	}
}

func TestScaleP15SnapshotReservationAccountsForContainers(t *testing.T) {
	data := domain.Bootstrap{Projects: make([]domain.Project, 2048)}
	before := HydratedSnapshotReservation(data)
	data.Projects[0].Description = string(make([]byte, 2<<20))
	if got := HydratedSnapshotReservation(data); got != before {
		t.Fatalf("immutable body bytes changed the owned-container reservation: %d -> %d", before, got)
	}
	data.Projects[0].MemberIDs = make([]string, 100000)
	if got := HydratedSnapshotReservation(data); got <= before {
		t.Fatal("nested slice containers were omitted from the reservation")
	}
	data.Settings = map[string]any{}
	body := make([]byte, 1<<20)
	for i := range 128 {
		data.Settings[fmt.Sprint(i)] = body
	}
	if got := HydratedSnapshotReservation(data); got != HydratedSnapshotReservationMaxBytes {
		t.Fatalf("oversized nested containers did not take the full allowance: %d", got)
	}
}
