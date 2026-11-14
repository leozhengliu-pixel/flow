package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW53Used(s *SQLiteStore) int {
	s.metadataResidency.work.mu.Lock()
	defer s.metadataResidency.work.mu.Unlock()
	return s.metadataResidency.work.used
}

func TestScaleP21SW53LifetimeOffIdentityAndBindings(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(t.Context(), struct{}{}, "untouched"))
	cancel()
	off := &SQLiteStore{}
	ctx, lifetime, err := off.BeginMetadataLifetime(parent)
	if err != nil || ctx != parent || lifetime != nil {
		t.Fatal("off path changed literal context or added cancellation handling", err)
	}
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	if _, owner, err := s.BeginMetadataLifetime(parent); owner != nil || !errors.Is(err, context.Canceled) || scaleP21SW53Used(s) != 0 {
		t.Fatal("canceled begin retained a descriptor", err)
	}
	ctx, lifetime, err = s.BeginMetadataLifetime(t.Context())
	if err != nil || scaleP21SW53Used(s) != metadataLifetimeDescriptorWork {
		t.Fatal("lifetime descriptor was not independently admitted", err)
	}
	if found, err := s.MetadataLifetime(ctx); err != nil || found != lifetime {
		t.Fatal("lifetime binding lost", err)
	}
	foreign := &SQLiteStore{metadataResidency: newMetadataResidency()}
	if _, err := foreign.MetadataLifetime(ctx); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("lifetime crossed store budget identity", err)
	}
	if _, err := s.MetadataLifetime(t.Context()); !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("missing caller owner was silently accepted", err)
	}
	lifetime.Close()
	lifetime.Close()
	if _, err := s.MetadataLifetime(ctx); !errors.Is(err, errMetadataLogicalClosed) || scaleP21SW53Used(s) != 0 {
		t.Fatal("closed lifetime remained usable or leaked", err)
	}
}

func TestScaleP21SW53ResultPreAdmissionRetryAndClose(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	ctx, lifetime, err := s.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, metadataWorkBytes-metadataLifetimeDescriptorWork)
	if err != nil {
		t.Fatal(err)
	}
	if slot, err := lifetime.NewResultSlot(ctx, 1<<20); slot != nil || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("result slot borrowed lifetime descriptor credit", err)
	}
	release()
	slot, err := lifetime.NewResultSlot(ctx, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if scaleP21SW53Used(s) != metadataLifetimeDescriptorWork+1<<20 {
		t.Fatal("result allowance missing before callback")
	}
	for _, candidate := range []string{"first attempt", "retry candidate"} {
		if err := slot.Capture(ctx, candidate); err != nil || slot.Data != candidate {
			t.Fatal("retry did not replace one owned candidate", err)
		}
		if len(lifetime.owners) != 1 || scaleP21SW53Used(s) != metadataLifetimeDescriptorWork+1<<20 {
			t.Fatal("retry accumulated owners/work")
		}
	}
	if err := slot.Capture(ctx, strings.Repeat("x", 128<<10)); !errors.Is(err, ErrMetadataWorkBudget) || slot.Data != nil {
		t.Fatal("oversized candidate escaped precommit validation", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := slot.Capture(cancelled, "cancelled"); !errors.Is(err, context.Canceled) || slot.Data != nil {
		t.Fatal("cancelled capture exposed a candidate", err)
	}
	slot.Close()
	slot.Close()
	if len(lifetime.owners) != 0 || scaleP21SW53Used(s) != metadataLifetimeDescriptorWork {
		t.Fatal("early result close did not release owner/work")
	}
}

func TestScaleP21SW53RawHandoffNilCallbacksOnly(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	ctx, lifetime, err := s.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	value := domain.Bootstrap{ViewerRole: "member", Settings: map[string]any{"exact": int64(9007199254740993)}}
	slot, err := lifetime.RetainValue(ctx, value)
	if err != nil || !reflect.DeepEqual(slot.Data, value) {
		t.Fatal("nil write-only Bootstrap callbacks prevented exact raw handoff ownership", err)
	}
	slot.Close()
	called := false
	value.FormerMembers = func() map[string]bool { called = true; return nil }
	if slot, err := lifetime.RetainValue(ctx, value); slot != nil || !errors.Is(err, ErrBoundedMetadataCoverage) || called {
		t.Fatal("non-nil hidden callback/closure was retained or invoked", err)
	}
	if slot, err := lifetime.RetainValue(ctx, struct {
		Hidden func() `json:"-"`
	}{}); slot != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("Bootstrap exception broadened to arbitrary ignored callback types", err)
	}
}

func TestScaleP21SW53LifetimeBoundedOwnersAndPanicCleanup(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	_, lifetime, err := s.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	var early func()
	for i := 0; i < metadataLifetimeOwners; i++ {
		release, err := lifetime.Hold(func() { closed++ })
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			early = release
		}
	}
	if _, err := lifetime.Hold(func() { closed++ }); !errors.Is(err, ErrMetadataWorkBudget) || closed != 1 {
		t.Fatal("full registry retained an uncharged owner", closed, err)
	}
	early()
	early()
	if _, err := lifetime.Hold(func() { closed++ }); err != nil {
		t.Fatal("early release failed to return owner capacity", err)
	}
	lifetime.Close()
	lifetime.Close()
	if closed != metadataLifetimeOwners+2 || scaleP21SW53Used(s) != 0 {
		t.Fatal("lifetime owner count/close mismatch", closed)
	}
	if _, err := lifetime.Hold(func() { closed++ }); !errors.Is(err, errMetadataLogicalClosed) || closed != metadataLifetimeOwners+3 {
		t.Fatal("registration after close leaked its supplied owner", err)
	}

	_, lifetime, err = s.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var total int
	for i := 0; i < 3; i++ {
		index := i
		if _, err := lifetime.Hold(func() {
			total++
			if index == 1 {
				panic("cleanup marker")
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		lifetime.Close()
	}()
	if recovered != "cleanup marker" || total != 3 || scaleP21SW53Used(s) != 0 {
		t.Fatal("panicking cleanup leaked sibling owners/descriptor", recovered, total)
	}
}

func TestScaleP21SW53ConcurrentLifetimeClose(t *testing.T) {
	s := &SQLiteStore{metadataResidency: newMetadataResidency()}
	ctx, lifetime, err := s.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	entered, proceed, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-proceed:
		default:
			close(proceed)
		}
		<-finished
	}()
	if _, err := lifetime.Hold(func() { close(entered); <-proceed }); err != nil {
		t.Fatal(err)
	}
	go func() { lifetime.Close(); close(finished) }()
	<-entered
	var rejected atomic.Int32
	if _, err := lifetime.Hold(func() { rejected.Add(1) }); !errors.Is(err, errMetadataLogicalClosed) || rejected.Load() != 1 {
		t.Fatal("concurrent close accepted/leaked a new owner", err)
	}
	if scaleP21SW53Used(s) != metadataLifetimeDescriptorWork {
		t.Fatal("active cleanup lost its descriptor")
	}
	if _, err := s.MetadataLifetime(ctx); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("closing lifetime was still usable", err)
	}
	close(proceed)
	<-finished
	if scaleP21SW53Used(s) != 0 {
		t.Fatal("concurrent cleanup leaked descriptor")
	}
}

func TestScaleP21SW53LifetimeFreshReadsAndExplicitIncomingPin(t *testing.T) {
	f, warm, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	ctx, lifetime, err := view.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	read, found, err := view.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err := lifetime.Hold(read.Close); err != nil {
		t.Fatal(err)
	}
	old, err := read.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "settings"), workspace, "scale_p21_sw53.peer", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "fresh current"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
		t.Fatal(err)
	}
	fresh, found, err := view.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	selected, err := fresh.Select(ctx, MetadataRecordScope{Read: []RecordKey{{Kind: "settings", ID: "sw27Typed"}}})
	if err != nil || selected.Data.Settings["sw27Typed"] != "fresh current" {
		t.Fatal("lifetime globally pinned ordinary reads", err)
	}
	fresh.Close()
	if !reflect.DeepEqual(old.Data.Settings["sw27Typed"], warm.Settings["sw27Typed"]) {
		t.Fatal("bag-owned old result followed current generation")
	}
	pinned, err := read.Context(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nested, batch, err := view.BeginMetadataLifetime(pinned)
	if err != nil {
		t.Fatal(err)
	}
	if nested.Value(metadataLogicalReadKey{}) != pinned.Value(metadataLogicalReadKey{}) {
		t.Fatal("new finite batch replaced supplied incoming read pin")
	}
	batch.Close()
	lifetime.Close()
	if old.Data.Settings != nil || scaleP21SW53Used(view) != 0 {
		t.Fatal("caller Close did not close transferred read/results")
	}
}

func TestScaleP21SW53CaptureFailureRollsBackMutation(t *testing.T) {
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	ctx, lifetime, err := view.BeginMetadataLifetime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	row := func() string {
		var raw string
		if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='sw27Typed'`, workspace).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := row()
	events := func() int {
		var count int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	beforeEvents := events()
	slot, err := lifetime.NewResultSlot(ctx, 4<<10)
	if err != nil {
		t.Fatal(err)
	}
	err = view.MutateWorkspaceScoped(ctx, workspace, "scale_p21_sw53.output", "", nil, MetadataRecordScope{
		Write: []RecordKey{{Kind: "settings", ID: "sw27Typed"}},
	}, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "must rollback"
		return slot.Capture(ctx, strings.Repeat("x", 64<<10))
	})
	if !errors.Is(err, ErrMetadataWorkBudget) || row() != before || events() != beforeEvents || slot.Data != nil {
		t.Fatal("output reservation failed after mutation publication", err)
	}
	slot.Close()
	slot, err = lifetime.NewResultSlot(ctx, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	err = view.MutateWorkspaceScoped(ctx, workspace, "scale_p21_sw53.output", "", nil, MetadataRecordScope{
		Write: []RecordKey{{Kind: "settings", ID: "sw27Typed"}},
	}, func(data *domain.Bootstrap) error {
		data.Settings["sw27Typed"] = "committed"
		return slot.Capture(ctx, "response survives writer return")
	})
	if err != nil || !strings.Contains(row(), "committed") || events() != beforeEvents+1 || slot.Data != "response survives writer return" ||
		scaleP21SW53Used(view) != metadataLifetimeDescriptorWork+1<<20 {
		t.Fatal("writer released captured output before caller encoding", err)
	}
	lifetime.Close()
	if slot.Data != nil || scaleP21SW53Used(view) != 0 {
		t.Fatal("mutation caller lifetime leaked output")
	}
}
