package store

import (
	"context"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// Default COLD routing (owner decision of 2026-11-14): HOT consumers are
// migrated to bounded reads and writes; COLD consumers keep their full legacy
// behaviour behind a per-replica limit of one.
//
// A ColdScope marks a flow that runs COLD: the server opens one for every
// request whose route is not proven bounded (and for background COLD work).
// Inside a scope:
//
//   - the projection permit (acquireProjection) and the legacy authorization
//     projection take the replica's COLD slot for the rest of the scope; the
//     scope owns the slot and the one facade it lends to every later need,
//     and Close releases them. Any other COLD need of the scope (a legacy
//     write, a COLD facade) takes the slot for that operation only;
//   - a write that no migrated bounded writer selects runs its literal
//     legacy dispatch on the scope's facade and publishes exactly as the
//     registered COLD writers do (coldLegacyMutation).
//
// The legacy whole-workspace accessors (WorkspaceMetadata, BootstrapFor,
// BootstrapForUser, WorkspaceMetadataFields of a non-resident field) carry no
// request context, so they cannot tell a COLD request from any other caller.
// With bounded metadata on they answer from a per-call legacy view of the
// installed workspace (coldAccessorView): the complete image decoded from the
// installed logical source, the same decode the COLD facade performs, so the
// answer is exactly the legacy one. Those decodes are serialized per replica
// on their own lane, which never waits for the COLD slot (a COLD scope may
// call them while it holds the slot).

type coldScopeKey struct{}

// ColdScope is one COLD flow's claim on a store's COLD slot.
type ColdScope struct {
	mu      sync.Mutex
	pattern string
	owner   *metadataResidency
	store   *SQLiteStore
	held    bool
	read    *ColdMetadataRead
	closed  bool
	// slotWait is the time the scope waited for the COLD slot (SW551).
	slotWait time.Duration
}

// WithColdScope opens a COLD scope for ctx. pattern names the route (or the
// background work) for diagnostics. The caller must Close the scope when the
// flow ends; values the flow read stay valid, its facades do not.
func WithColdScope(ctx context.Context, pattern string) (context.Context, *ColdScope) {
	scope := &ColdScope{pattern: pattern}
	return context.WithValue(ctx, coldScopeKey{}, scope), scope
}

// ColdScopeFromContext returns the COLD scope ctx carries.
func ColdScopeFromContext(ctx context.Context) (*ColdScope, bool) {
	scope, ok := ctx.Value(coldScopeKey{}).(*ColdScope)
	return scope, ok && scope != nil
}

// InColdScope reports whether ctx runs inside an open COLD scope.
func InColdScope(ctx context.Context) bool {
	scope, ok := ColdScopeFromContext(ctx)
	if !ok {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return !scope.closed
}

type coldWorkKey struct{}

// WithColdWork marks background COLD work (a ticker, a worker, a job): with
// bounded metadata on, each of its COLD needs takes the COLD slot on its own
// for that operation only (a write no migrated writer serves runs its
// literal legacy dispatch on a COLD facade, a legacy projection is read from
// a legacy view), so long-lived work never holds the slot between them.
func WithColdWork(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, coldWorkKey{}, name)
}

// coldCapable reports whether ctx may run COLD needs: a COLD scope (open, or
// closed for work the flow left running) or COLD work.
func coldCapable(ctx context.Context) bool {
	if _, scoped := ColdScopeFromContext(ctx); scoped {
		return true
	}
	_, work := ctx.Value(coldWorkKey{}).(string)
	return work
}

// Pattern names the scope's route or background work.
func (c *ColdScope) Pattern() string { return c.pattern }

// Held reports whether the scope holds a COLD slot.
func (c *ColdScope) Held() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.held
}

// SlotWait is how long the scope waited for its COLD slot.
func (c *ColdScope) SlotWait() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.slotWait
}

// Close releases the scope's facade and COLD slot. Later COLD needs of the
// flow fail closed.
func (c *ColdScope) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	read, held, owner := c.read, c.held, c.owner
	c.read, c.held, c.closed = nil, false, true
	c.mu.Unlock()
	if read != nil {
		read.Close()
	}
	if held {
		<-owner.coldSlot
		coldSlotHolders.Add(-1)
	}
}

// coldSlotHolders counts the COLD slots held process-wide (SW551).
var coldSlotHolders atomic.Int64

// coldSlotAcquisitions counts COLD slot acquisitions process-wide (SW550).
var coldSlotAcquisitions atomic.Int64

// ColdSlotAcquisitionsForTesting reports how many times a COLD slot was taken.
func ColdSlotAcquisitionsForTesting() int64 { return coldSlotAcquisitions.Load() }

// ColdSlotsHeldForTesting reports how many COLD slots are held right now.
func ColdSlotsHeldForTesting() int64 { return coldSlotHolders.Load() }

// acquireLocked takes s's COLD slot for the scope. The caller holds c.mu. A
// scope serves one bounded store; another store's needs take their own slot.
func (c *ColdScope) acquireLocked(ctx context.Context, s *SQLiteStore) (bool, error) {
	r := s.metadataResidency
	if c.closed {
		// Work the flow left running after it ended (a goroutine with its
		// context) is COLD per operation: each takes the slot on its own.
		return false, nil
	}
	if c.owner != nil && c.owner != r {
		return false, nil
	}
	if c.held {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.coldOnce.Do(func() { r.coldSlot = make(chan struct{}, 1) })
	started := time.Now()
	select {
	case r.coldSlot <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	coldSlotHolders.Add(1)
	coldSlotAcquisitions.Add(1)
	c.slotWait += time.Since(started)
	c.owner, c.store, c.held = r, s, true
	return true, nil
}

// HoldColdScope takes the COLD slot for the scope ctx carries, for the rest
// of the scope, without building a facade: the projection permit of a COLD
// request (in place of the projection slot) and the authorization projection.
// Later COLD needs of the scope share the slot and one facade. It reports
// whether ctx carries an open scope this store serves.
func (s *SQLiteStore) HoldColdScope(ctx context.Context) (bool, error) {
	if s.coldParent != nil {
		return s.coldParent.HoldColdScope(ctx)
	}
	scope, ok := ColdScopeFromContext(ctx)
	if !ok || s.metadataResidency == nil {
		return false, nil
	}
	if nested, ok := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead); ok && nested.owner == s.metadataResidency {
		nested.mu.Lock()
		active := nested.facade != nil
		nested.mu.Unlock()
		if active {
			// Inside a COLD facade's callback: its slot is this flow's.
			return true, nil
		}
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.acquireLocked(ctx, s)
}

// beginScopedColdMetadataRead lends the facade of a scope that holds its
// COLD slot (it took its projection or authorization permit), decoding it on
// first use. selected is false when ctx carries no such scope: the COLD need
// then takes the slot for its own operation only, so a long request (a
// stream, a terminal) never holds the slot for a write it made.
func (s *SQLiteStore) beginScopedColdMetadataRead(ctx context.Context) (read *ColdMetadataRead, selected bool, err error) {
	scope, ok := ColdScopeFromContext(ctx)
	if !ok || s.metadataResidency == nil {
		return nil, false, nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.closed || !scope.held || scope.owner != s.metadataResidency {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	if scope.read == nil {
		owned, err := s.decodeOwnedColdMetadataRead(ctx, func() {})
		if err != nil {
			return nil, true, err
		}
		scope.read = owned
	}
	state := scope.read.state
	return &ColdMetadataRead{context: context.WithValue(ctx, coldMetadataReadKey{}, state), state: state}, true, nil
}

// coldAccessorObservers report each legacy accessor a bounded store served
// through its legacy view (SW530/SW550: a proven-bounded route makes none).
var coldAccessorLanes sync.Map // *metadataResidency -> chan struct{}

func (r *metadataResidency) accessorLane() chan struct{} {
	lane, _ := coldAccessorLanes.LoadOrStore(r, make(chan struct{}, 1))
	return lane.(chan struct{})
}

// coldAccessorView returns a legacy store holding the installed image of the
// requested workspace (only fields, plus the workspace record, when fields is
// set), decoded from the installed logical source. handled is false when the
// store is not bounded, the read names only resident fields, or the store has
// no installed logical source (a test view without a capture keeps the
// resident skeleton). view is nil when the workspace does not exist. release
// must be called once the accessor's answer is built.
func (s *SQLiteStore) coldAccessorView(ctx context.Context, accessor, workspace string, fields []string) (view *SQLiteStore, release func(), handled bool) {
	r := s.metadataResidency
	if r == nil || s.coldParent != nil {
		return nil, nil, false
	}
	if fields != nil {
		residency := metadataResidencyFields()
		resident := true
		for _, name := range fields {
			if mode := residency[name]; mode != "core" && mode != "policy" {
				resident = false
				accessor += "(" + name + ")"
				break
			}
		}
		if resident {
			return nil, nil, false
		}
	}
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	_, exists := s.workspaces[workspace]
	s.mu.RUnlock()
	r.mu.Lock()
	logical := r.logical[workspace] != nil
	r.mu.Unlock()
	if !exists {
		// Not found, as the legacy accessor answers.
		return nil, func() {}, true
	}
	if !logical {
		// No installed logical image of this workspace (a test view that
		// captured another one): the resident read as before.
		return nil, nil, false
	}
	if observe, ok := skeletalReadObservers.Load(s); ok {
		observe.(func(string))(accessor)
	}
	coldAccessorReads.Add(1)
	none := func() {}
	lane := r.accessorLane()
	select {
	case lane <- struct{}{}:
	case <-ctx.Done():
		return nil, none, true
	}
	release = func() { <-lane }
	s.mu.RLock()
	r.mu.Lock()
	source := r.logical[workspace]
	view = s.newColdFacadeLocked(1)
	view.lastWorkspaceKey = workspace
	var err error
	if source == nil {
		err = ErrBoundedMetadataCoverage
	} else {
		err = source.Retain()
	}
	r.mu.Unlock()
	s.mu.RUnlock()
	if err != nil {
		coldAccessorFailures.Add(1)
		return nil, release, true
	}
	done := release
	release = func() {
		source.Release()
		view.mu.Lock()
		view.workspaces, view.coldParent = nil, nil
		view.mu.Unlock()
		done()
	}
	decodeStarted := time.Now()
	if err := view.decodeColdWorkspace(ctx, workspace, source, fields); err != nil {
		coldAccessorFailures.Add(1)
		release()
		return nil, none, true
	}
	coldAccessorDecodeNanos.Add(int64(time.Since(decodeStarted)))
	return view, release, true
}

// coldAccessorReads counts the legacy views served (SW550).
var coldAccessorReads atomic.Int64

// ColdAccessorReadsForTesting reports how many legacy accessor reads bounded
// stores served through their legacy view.
func ColdAccessorReadsForTesting() int64 { return coldAccessorReads.Load() }

// Decode cost of COLD facades and legacy views (the measured hydration time).
var coldFacadeDecodes, coldFacadeDecodeNanos, coldAccessorDecodeNanos atomic.Int64

// ColdHydrationForTesting reports the COLD facade decodes, their total time
// and the total time of legacy-view decodes so far.
func ColdHydrationForTesting() (facades int64, facadeTime, viewTime time.Duration) {
	return coldFacadeDecodes.Load(), time.Duration(coldFacadeDecodeNanos.Load()), time.Duration(coldAccessorDecodeNanos.Load())
}

// The shared write lock a COLD legacy write holds (HOT writers wait for it).
var coldWriteLocks, coldWriteLockNanos, coldWriteLockMax atomic.Int64

// ColdWriteLockHoldForTesting reports how many times COLD legacy writes held
// the shared write lock, for how long in total and at most.
func ColdWriteLockHoldForTesting() (holds int64, total, longest time.Duration) {
	return coldWriteLocks.Load(), time.Duration(coldWriteLockNanos.Load()), time.Duration(coldWriteLockMax.Load())
}

// coldAccessorFailures counts legacy views that could not be decoded (the
// accessor then reports the workspace as missing, as SW531 did).
var coldAccessorFailures atomic.Int64

// decodeColdWorkspace is decodeColdMetadataRead for one workspace, limited to
// fields (and the workspace record) when fields is set.
func (s *SQLiteStore) decodeColdWorkspace(ctx context.Context, workspace string, source *metadataLogicalSource, fields []string) error {
	ctx = coldMetadataGenericDecodeContext(ctx, map[string]*metadataLogicalSource{workspace: source})
	var data domain.Bootstrap
	value := reflect.ValueOf(&data).Elem()
	index := 0
	err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		defer func() { index++ }()
		if fields != nil && name != "workspace" && !slices.Contains(fields, name) {
			return nil
		}
		decoded, err := node.Value(ctx, int(^uint(0)>>1))
		if err != nil {
			return err
		}
		value.Field(index).Set(decoded)
		return nil
	})
	if err != nil {
		return err
	}
	domain.RebuildTeamDirectory(&data)
	s.mu.Lock()
	s.workspaces[workspace] = data
	s.mu.Unlock()
	return ctx.Err()
}

type coldLegacyWriteKey struct{}

// coldLegacyWrite reports a default COLD legacy write (coldLegacyMutation).
func coldLegacyWrite(ctx context.Context) bool {
	selected, _ := ctx.Value(coldLegacyWriteKey{}).(bool)
	return selected
}

// coldLegacyMutation runs a COLD write that no migrated writer serves: the
// literal legacy dispatch on the scope's COLD facade, with the facade's write
// lock shared with the parent. Every legacy writer takes that lock around its
// base, transaction and install: right after it takes it the facade is
// brought to the parent's latest installed generation, and right before it
// returns it the parent reloads the workspace's installed image from SQL
// (a no-op when the writer published an exact image itself, as the full
// metadata writer does). Events, webhooks and realtime publications are the
// legacy writer's own.
func (s *SQLiteStore) coldLegacyMutation(ctx context.Context, workspace, eventType string, payload any, mutate func(*domain.Bootstrap) (string, error)) error {
	return s.coldLegacyWrite(ctx, workspace, func(ctx context.Context, facade *SQLiteStore) error {
		return facade.dispatchMutation(ctx, workspace, eventType, payload, mutate)
	})
}

// coldLegacyWrite runs one literal legacy writer (run, on the facade) as
// coldLegacyMutation describes.
func (s *SQLiteStore) coldLegacyWrite(ctx context.Context, workspace string, run func(context.Context, *SQLiteStore) error) error {
	ctx = context.WithValue(ctx, coldLegacyWriteKey{}, true)
	return s.withColdMutationStore(ctx, workspace, func(ctx context.Context, facade *SQLiteStore) error {
		facade.coldWrite.legacy = true
		requested := func() string {
			if key := facade.coldWrite.requested; key != "" {
				return key
			}
			facade.mu.RLock()
			defer facade.mu.RUnlock()
			return facade.lastWorkspaceKey
		}
		var locked time.Time
		facade.writeMu.onLock = func() {
			locked = time.Now()
			facade.syncColdLegacyWriteLocked(ctx, requested())
		}
		facade.writeMu.onUnlock = func() {
			s.publishColdLegacyWriteLocked(ctx, requested())
			held := int64(time.Since(locked))
			coldWriteLocks.Add(1)
			coldWriteLockNanos.Add(held)
			for {
				longest := coldWriteLockMax.Load()
				if held <= longest || coldWriteLockMax.CompareAndSwap(longest, held) {
					break
				}
			}
		}
		defer func() { facade.writeMu.onLock, facade.writeMu.onUnlock = nil, nil }()
		return run(ctx, facade)
	})
}

// coldReceiver is an empty legacy store sharing s's database and services,
// for a legacy method that reads SQL only (no installed workspace).
func (s *SQLiteStore) coldReceiver() *SQLiteStore {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.newColdFacadeLocked(0)
}

// syncColdLegacyWriteLocked brings the facade to the parent's installed
// generation of workspace. The caller holds the parent's write lock. When the
// refresh fails the workspace is withdrawn from the facade, so the legacy
// writer fails (workspace not found) instead of writing from a stale base.
func (s *SQLiteStore) syncColdLegacyWriteLocked(ctx context.Context, workspace string) {
	read, ok := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
	parent := s.coldParent
	if !ok || parent == nil {
		return
	}
	r := parent.metadataResidency
	r.mu.Lock()
	current := r.logical[workspace]
	r.mu.Unlock()
	read.mu.Lock()
	pinned := read.sources[workspace]
	read.mu.Unlock()
	if current != nil && current == pinned {
		return
	}
	if err := s.refreshColdMetadataRead(ctx, workspace); err != nil {
		slog.Error("bounded metadata: COLD legacy write could not refresh its facade", "workspace", workspace, "error", err)
		s.mu.Lock()
		delete(s.workspaces, workspace)
		s.mu.Unlock()
	}
}

// publishColdLegacyWriteLocked reloads the parent's installed image of
// workspace from SQL after a COLD legacy write committed. The caller holds
// the parent's write lock. reloadResidentWorkspace keeps an image whose
// stamp already matches SQL.
func (s *SQLiteStore) publishColdLegacyWriteLocked(ctx context.Context, workspace string) {
	if err := s.reloadResidentWorkspace(context.WithoutCancel(ctx), workspace); err != nil {
		slog.Error("bounded metadata: COLD legacy write could not publish its workspace", "workspace", workspace, "error", err)
	}
}
