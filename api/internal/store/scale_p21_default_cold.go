package store

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// Default COLD routing (owner decision of 2026-11-14): HOT consumers are
// migrated to bounded reads and writes; COLD consumers keep their full legacy
// behaviour behind a per-replica limit of one.
//
// A ColdScope marks a request whose route is not proven bounded; WithColdWork
// marks background COLD work. In either, a COLD need never holds the COLD
// slot beyond its own operation, so a long request (a stream, a terminal, an
// MCP session) or a long job never holds it between operations:
//
//   - a write that no migrated bounded writer selects takes the slot, runs
//     its literal legacy dispatch on a COLD facade and publishes exactly as
//     the registered COLD writers do, then returns the slot
//     (coldLegacyMutation);
//   - a legacy projection (PagedWorkspaceMetadata, the authorization's
//     request view) is read from a legacy view of the installed workspace.
//
// The slot is a FIFO queue with a bounded wait (coldSlotQueue): a waiter
// beyond the queue limit, or one that waited past the timeout, gets
// ErrColdMetadataBusy, which the server answers with 503 + Retry-After. The
// server bounds COLD projections separately from HOT ones (its COLD projection
// gate), so a COLD request never holds a slot a HOT one waits for.
//
// The legacy whole-workspace accessors (WorkspaceMetadata, BootstrapFor,
// BootstrapForUser, WorkspaceMetadataFields of a non-resident field) carry no
// request context, so they cannot tell a COLD request from any other caller.
// With bounded metadata on they answer from a per-call legacy view of the
// installed workspace (coldAccessorView): the complete image decoded from the
// installed logical source, the same decode the COLD facade performs, so the
// answer is exactly the legacy one. Those decodes are serialized per replica
// on their own lane, which never waits for the COLD slot (a COLD facade's
// callback may call them while it holds the slot).

type coldScopeKey struct{}

// ColdScope marks one COLD request and caches its authorization view.
type ColdScope struct {
	mu      sync.Mutex
	pattern string
	closed  bool
	// view is the request's legacy view for the authorization projection
	// (ColdRequestView), decoded once and dropped at Close.
	view      *SQLiteStore
	viewStore *SQLiteStore
}

// WithColdScope opens a COLD scope for ctx. pattern names the route for
// diagnostics. The caller must Close the scope when the request ends; work
// the request leaves running stays COLD per operation.
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

// WithColdWork marks background COLD work (a ticker, a worker, a job, a loop
// run): with bounded metadata on, each of its COLD needs takes the COLD slot
// for that operation only.
func WithColdWork(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, coldWorkKey{}, name)
}

// coldCapable reports whether ctx may run COLD needs: a COLD scope (open, or
// closed for work the request left running) or COLD work.
func coldCapable(ctx context.Context) bool {
	if _, scoped := ColdScopeFromContext(ctx); scoped {
		return true
	}
	_, work := ctx.Value(coldWorkKey{}).(string)
	return work
}

// InColdFacade reports whether ctx runs inside the callback of a COLD facade
// (it holds the COLD slot), which bounds its projections itself.
func InColdFacade(ctx context.Context) bool {
	read, ok := ctx.Value(coldMetadataReadKey{}).(*coldMetadataRead)
	if !ok {
		return false
	}
	read.mu.Lock()
	defer read.mu.Unlock()
	return read.facade != nil
}

// Pattern names the scope's route.
func (c *ColdScope) Pattern() string { return c.pattern }

// Close ends the scope and drops its view.
func (c *ColdScope) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.view, c.viewStore, c.closed = nil, nil, true
	c.mu.Unlock()
}

// ColdRequestView returns the COLD request's legacy view of workspace for its
// authorization projection: a legacy store holding the installed image,
// decoded once per request on the legacy-view lane (no COLD slot) and kept
// until the scope closes, so the projection and the values authorization
// hands to the handler are the legacy ones. Outside an open scope, or flag
// off, it returns s.
func (s *SQLiteStore) ColdRequestView(ctx context.Context, workspace string) (*SQLiteStore, error) {
	scope, ok := ColdScopeFromContext(ctx)
	if !ok || s.metadataResidency == nil || s.coldParent != nil {
		return s, nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.closed {
		return s, nil
	}
	if scope.view != nil && scope.viewStore == s && scope.view.lastWorkspaceKey == workspace {
		return scope.view, nil
	}
	view, release, handled := s.coldAccessorViewKept(ctx, "ColdRequestView", workspace, nil, true)
	if !handled {
		return s, nil
	}
	release()
	if view == nil {
		// A missing workspace: the live store answers it as before.
		return s, nil
	}
	scope.view, scope.viewStore = view, s
	return view, nil
}

// ErrColdMetadataBusy reports that the COLD slot's queue was full or the wait
// ran out; the request may be retried (503 + Retry-After).
var ErrColdMetadataBusy = errors.New("the workspace is busy with other requests; retry in a moment")

// ColdMetadataBusyRetryAfter is the retry delay answered with
// ErrColdMetadataBusy.
const ColdMetadataBusyRetryAfter = 5 * time.Second

// coldSlotQueue is the replica's COLD slot: one holder, waiters served in
// arrival order (a buffered channel's blocked senders queue FIFO), at most
// limit of them, each for at most timeout.
type coldSlotQueue struct {
	slot    chan struct{}
	waiting atomic.Int64
	limit   atomic.Int64
	timeout atomic.Int64
}

var coldSlotQueues sync.Map // *metadataResidency -> *coldSlotQueue

func (r *metadataResidency) coldQueue() *coldSlotQueue {
	r.coldOnce.Do(func() { r.coldSlot = make(chan struct{}, 1) })
	if queue, ok := coldSlotQueues.Load(r); ok {
		return queue.(*coldSlotQueue)
	}
	queue := &coldSlotQueue{slot: r.coldSlot}
	queue.limit.Store(int64(envPositiveInt("FLOW_COLD_QUEUE_LIMIT", 64)))
	queue.timeout.Store(int64(envPositiveDuration("FLOW_COLD_QUEUE_TIMEOUT", 15*time.Second)))
	actual, _ := coldSlotQueues.LoadOrStore(r, queue)
	return actual.(*coldSlotQueue)
}

// SetColdQueueForTesting sets the COLD slot's queue limit and wait timeout.
func (s *SQLiteStore) SetColdQueueForTesting(limit int, timeout time.Duration) {
	queue := s.metadataResidency.coldQueue()
	queue.limit.Store(int64(limit))
	queue.timeout.Store(int64(timeout))
}

// ColdSlotWaitingForTesting reports how many waiters the COLD slot has.
func (s *SQLiteStore) ColdSlotWaitingForTesting() int64 {
	return s.metadataResidency.coldQueue().waiting.Load()
}

// acquire waits for the slot in arrival order. It fails with
// ErrColdMetadataBusy when limit others already wait or the wait times out,
// and with the context's error when the caller leaves.
func (q *coldSlotQueue) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case q.slot <- struct{}{}:
		coldSlotHolders.Add(1)
		coldSlotAcquisitions.Add(1)
		return nil
	default:
	}
	if q.waiting.Add(1) > q.limit.Load() {
		q.waiting.Add(-1)
		coldSlotRefusals.Add(1)
		return ErrColdMetadataBusy
	}
	defer q.waiting.Add(-1)
	timer := time.NewTimer(time.Duration(q.timeout.Load()))
	defer timer.Stop()
	select {
	case q.slot <- struct{}{}:
		coldSlotHolders.Add(1)
		coldSlotAcquisitions.Add(1)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		coldSlotRefusals.Add(1)
		slog.Warn("bounded metadata: COLD slot wait timed out", "timeout", time.Duration(q.timeout.Load()), "waiting", q.waiting.Load())
		return ErrColdMetadataBusy
	}
}

func (q *coldSlotQueue) release() {
	coldSlotHolders.Add(-1)
	<-q.slot
}

// coldSlotHolders counts the COLD slots held process-wide (SW551).
var coldSlotHolders, coldSlotRefusals atomic.Int64

// coldSlotAcquisitions counts COLD slot acquisitions process-wide (SW550).
var coldSlotAcquisitions atomic.Int64

// ColdSlotAcquisitionsForTesting reports how many times a COLD slot was taken.
func ColdSlotAcquisitionsForTesting() int64 { return coldSlotAcquisitions.Load() }

// ColdSlotsHeldForTesting reports how many COLD slots are held right now.
func ColdSlotsHeldForTesting() int64 { return coldSlotHolders.Load() }

// ColdSlotRefusalsForTesting reports how many COLD slot waits were refused.
func ColdSlotRefusalsForTesting() int64 { return coldSlotRefusals.Load() }

func envPositiveInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

func envPositiveDuration(name string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
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
	return s.coldAccessorViewKept(ctx, accessor, workspace, fields, false)
}

// coldAccessorViewKept is coldAccessorView; with keep, release returns the
// lane and the installed source but leaves the decoded view usable (its
// values are its own memory) for the caller to keep.
func (s *SQLiteStore) coldAccessorViewKept(ctx context.Context, accessor, workspace string, fields []string, keep bool) (view *SQLiteStore, release func(), handled bool) {
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
		if !keep {
			view.mu.Lock()
			view.workspaces, view.coldParent = nil, nil
			view.mu.Unlock()
		}
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
//
// also names further workspace keys the write may create or remove (a
// rename's new key): each is published the same way, a key SQL no longer
// holds is dropped from the parent.
func (s *SQLiteStore) coldLegacyWrite(ctx context.Context, workspace string, run func(context.Context, *SQLiteStore) error, also ...string) error {
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
		// The store keeps installed logical sources (always in production;
		// a test view once it captured one): a key the write creates gets one.
		s.metadataResidency.mu.Lock()
		logical := len(s.metadataResidency.logical) != 0
		s.metadataResidency.mu.Unlock()
		facade.writeMu.onUnlock = func() {
			s.publishColdLegacyWriteLocked(ctx, requested(), logical)
			for _, key := range also {
				s.publishColdLegacyWriteLocked(ctx, key, logical)
			}
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
func (s *SQLiteStore) publishColdLegacyWriteLocked(ctx context.Context, workspace string, logical bool) {
	ctx = context.WithoutCancel(ctx)
	var present int
	switch err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&present); {
	case err != nil:
		slog.Error("bounded metadata: COLD legacy write could not publish its workspace", "workspace", workspace, "error", err)
		return
	case present == 0:
		// The legacy writer removed the key (a rename's old key).
		s.dropResidentWorkspace(workspace)
		return
	}
	if err := s.reloadResidentWorkspaceLogical(ctx, workspace, logical); err != nil {
		slog.Error("bounded metadata: COLD legacy write could not publish its workspace", "workspace", workspace, "error", err)
	}
}

// coldInstallHookForTesting runs inside a COLD write's install, while the
// facade's lock is held (SW554).
var coldInstallHookForTesting atomic.Pointer[func()]

// SetColdInstallHookForTesting sets (nil clears) the COLD install hook.
func SetColdInstallHookForTesting(hook func()) {
	if hook == nil {
		coldInstallHookForTesting.Store(nil)
		return
	}
	coldInstallHookForTesting.Store(&hook)
}

// dropResidentWorkspace removes workspace from the bounded store's installed
// image after a COLD legacy write removed it from SQL (a rename's old key):
// the resident copy, its installed logical source, stamps and policy charge.
// The caller holds the write lock.
func (s *SQLiteStore) dropResidentWorkspace(workspace string) {
	r := s.metadataResidency
	s.mu.Lock()
	delete(s.workspaces, workspace)
	s.forgetCopyRevisionLocked(workspace)
	r.mu.Lock()
	source := r.logical[workspace]
	delete(r.logical, workspace)
	delete(r.stamps, workspace)
	delete(r.applied, workspace)
	costs := make(map[string]int, len(r.policy))
	for key, cost := range r.policy {
		if key != workspace {
			costs[key] = cost
		}
	}
	r.mu.Unlock()
	s.mu.Unlock()
	if err := r.replacePolicyCosts(costs); err != nil {
		slog.Error("bounded metadata: dropping a renamed workspace's policy charge", "workspace", workspace, "error", err)
	}
	if source != nil {
		source.Release()
	}
	s.forgetChatSnapshotFlag(workspace)
	s.issueGroupCounts.drop(workspace)
}

// setLastWorkspaceKey records the workspace the legacy writer last wrote, as
// the legacy store does after a workspace update.
func (s *SQLiteStore) setLastWorkspaceKey(workspace string) {
	s.mu.Lock()
	if _, ok := s.workspaces[workspace]; ok {
		s.lastWorkspaceKey = workspace
	}
	s.mu.Unlock()
}
