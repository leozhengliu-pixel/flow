package types

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/anyval"
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
	"flow/api/internal/boundedcrdt/internal/utf16"
)

// Map is the user-facing wrapper around a map-like Branch. Construct
// via NewMap; usually obtained as types.NewMap(d.Branch("name")).
//
// Map exposes Set / Get / Delete / Has / Len / Range / Clear plus a
// Branch accessor for low-level integration.
type Map struct {
	branch *block.Branch
}

// NewMap wraps the given branch as a Map. The branch's Map field is
// initialized lazily if nil — typical for a freshly-created root
// branch from Doc.Branch(name).
func NewMap(mc *meter.Meter, branch *block.Branch) *Map {
	mc.Enter()
	defer mc.Leave()

	if branch.Map == nil {
		branch.Map = meter.Literal[map[string]*block.Item](mc, 128, func() map[string]*block.Item {
			return map[string]*block.Item{}
		})
	}
	return meter.Pointer(mc, Map{branch: branch})
}

// Branch returns the underlying *block.Branch. Useful for
// cross-package wiring (e.g. observers, encoders) that need to
// operate at the block layer.
func (m *Map) Branch(mc *meter.Meter) *block.Branch {
	mc.Enter()
	defer mc.Leave()
	return m.branch
}

// Set stores value under key in this map. Concurrent writes from
// different replicas converge to the same final value — the writer
// with the higher (clientID, clock) wins per YATA.
//
// value may be of any type; the encoding-layer port (deferred per
// tech-debt.md) will decide how each Go type maps to a wire variant.
// For now value is stored as ContentAny, which round-trips through
// reflection.
//
// Mirrors yrs Map::insert (yrs/src/types/map.rs ~line 73).
func (m *Map) Set(mc *meter.Meter, txn *doc.TransactionMut, key string, value any) {
	mc.Enter()
	defer mc.Leave()

	key = utf16.ValidString(mc, key)
	value = anyval.Valid(mc, value)
	clientID := txn.Doc(mc).ClientID(mc)
	clock := txn.Store(mc).GetClock(mc, clientID)

	var origin *block.ID
	var left *block.Item
	if existing, ok := m.branch.Map[key]; ok {
		// Per types-map.md finding 1: branch.Map[key] is the tail
		// already. Per finding 2: Origin = left.LastID().
		left = existing
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}

	keyCopy := key // borrow-stable string for ParentSub pointer
	item := meter.Pointer(mc, block.Item{
		ID:     block.ID{Client: clientID, Clock: clock},
		Len:    1,
		Origin: origin,
		Left:   left,
		Content: block.Content{Kind: block.KindAny, Anys: meter.Literal[[]block.Any](mc, 144, func() []block.Any {
			return []block.Any{value}
		})},
		Parent:    block.Parent{Kind: block.ParentBranch, Branch: m.branch},
		ParentSub: meter.Pointer(mc, keyCopy),
		Flags:     block.FlagCountable,
	})

	txn.Store(mc).PushBlock(mc, item)
	if dropped := item.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, item)
	}
}

// Get returns the current value under key, or nil if the key is
// absent or its tail item is tombstoned.
//
// Per types-map.md finding 5: this is one-step. branch.Map[key] is
// the LWW winner; if it's deleted there is no live predecessor to
// fall back to.
func (m *Map) Get(mc *meter.Meter, key string) any {
	mc.Enter()
	defer mc.Leave()

	item, ok := m.branch.Map[utf16.ValidString(mc, key)]
	if !ok || item.IsDeleted(mc) {
		return nil
	}
	return extractValue(mc, item.Content)
}

// Has reports whether key has a live (non-tombstoned) entry.
func (m *Map) Has(mc *meter.Meter, key string) bool {
	mc.Enter()
	defer mc.Leave()

	item, ok := m.branch.Map[utf16.ValidString(mc, key)]
	return ok && !item.IsDeleted(mc)
}

// Delete tombstones the entry under key. Calling Delete when the
// key is absent or already tombstoned is a no-op.
//
// Per types-map.md finding 3: Delete does NOT clear branch.Map[key].
// The map entry continues to point at the tombstoned item so a
// subsequent Set can chain off it as Origin/Left, preserving YATA
// convergence for concurrent writers that didn't see the delete yet.
func (m *Map) Delete(mc *meter.Meter, txn *doc.TransactionMut, key string) {
	mc.Enter()
	defer mc.Leave()

	item, ok := m.branch.Map[utf16.ValidString(mc, key)]
	if !ok || item.IsDeleted(mc) {
		return
	}
	txn.Delete(mc, item)
}

// Len returns the number of live (non-tombstoned) entries.
//
// O(N) over the map size: iterates and counts. yrs has a TODO at
// map.rs:158 about caching this on the Branch — we'd inherit that
// optimization for free if we add the cache to Branch. Tracked in
// tech-debt.md if it shows up in benchmarks.
func (m *Map) Len(mc *meter.Meter) int {
	mc.Enter()
	defer mc.Leave()

	n := 0
	for _, item := range m.branch.Map {
		mc.Step()

		if !item.IsDeleted(mc) {
			n++
		}
	}
	return n
}

// Range iterates over live (key, value) pairs in unspecified order
// (Go map iteration). The callback returns false to stop early.
func (m *Map) Range(mc *meter.Meter, fn func(key string, value any) bool) {
	mc.Enter()
	defer mc.Leave()

	for key, item := range m.branch.Map {
		mc.Step()

		if item.IsDeleted(mc) {
			continue
		}
		if !fn(key, extractValue(mc, item.Content)) {
			return
		}
	}
}

// Clear deletes every entry in the map, in the order the keys were
// first set, as yjs YMap.clear walks _map. The order is that of the
// transaction's deletions, which an UndoManager restores in.
func (m *Map) Clear(mc *meter.Meter, txn *doc.TransactionMut) {
	mc.Enter()
	defer mc.Leave()

	for _, key := range m.branch.MapKeys(mc) {
		mc.Step()

		if item := m.branch.Map[key]; item != nil && !item.IsDeleted(mc) {
			txn.Delete(mc, item)
		}
	}
}

// SetMap inserts a freshly-constructed nested Map under key and
// returns the wrapper bound to it. Subsequent edits via the returned
// *Map are addressed to this map-key slot in m; concurrent peers
// see the same nested-map structure once they apply the resulting
// updates.
//
// Per docs/yrs-port-notes/nested-types.md §5 — recommended API over
// overloading Set(any) so the caller never has to type-assert the
// returned wrapper.
func (m *Map) SetMap(mc *meter.Meter, txn *doc.TransactionMut, key string) *Map {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{
		TypeRef: block.TypeRefMap,
		Map: meter.Literal[map[string]*block.Item](mc, 128, func() map[string]*block.Item {
			return map[string]*block.Item{}
		}),
	})
	m.setNested(mc, txn, key, inner)
	return meter.Pointer(mc, Map{branch: inner})
}

// SetArray inserts a freshly-constructed nested Array under key.
func (m *Map) SetArray(mc *meter.Meter, txn *doc.TransactionMut, key string) *Array {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{TypeRef: block.TypeRefArray})
	m.setNested(mc, txn, key, inner)
	return meter.Pointer(mc, Array{branch: inner})
}

// SetText inserts a freshly-constructed nested Text under key
// (plain-text only — see tech-debt.md for rich-text formatting).
func (m *Map) SetText(mc *meter.Meter, txn *doc.TransactionMut, key string) *Text {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{TypeRef: block.TypeRefText})
	m.setNested(mc, txn, key, inner)
	return meter.Pointer(mc, Text{branch: inner})
}

// setNested is the shared scaffolding for SetMap/SetArray/SetText:
// build an Item with KindType content and the supplied Branch,
// chain off the existing tail at this key (Origin = left.LastID()),
// push to store, integrate. The Item.Integrate KindType arm wires
// SetDoc nests a new subdocument under key and returns its handle. The
// subdocument carries its own GUID; its content syncs as a separate
// update stream, while the parent stores only the reference (GUID +
// options). Mirrors yjs `map.set(key, new Y.Doc())`.
func (m *Map) SetDoc(mc *meter.Meter, txn *doc.TransactionMut, key string) *doc.Doc {
	mc.Enter()
	defer mc.Leave()

	sub := doc.NewDocWithOptions(mc, doc.Options{DisableGC: true})
	m.setDocRefWithOpts(mc, txn, key, sub.GUID(mc), meter.Literal[map[string]any](mc, 128, func() map[string]any {
		return map[string]any{}
	}))
	txn.Doc(mc).PutSubdoc(mc, sub)
	return sub
}

// SetDocWithOptions is SetDoc with subdocument options. When autoLoad
// is true the reference carries the autoLoad flag on the wire, so peers
// that decode it mark the subdocument to load automatically (it appears
// in their SubdocsLoaded set).
func (m *Map) SetDocWithOptions(mc *meter.Meter, txn *doc.TransactionMut, key string, autoLoad bool) *doc.Doc {
	mc.Enter()
	defer mc.Leave()

	sub := doc.NewDocWithOptions(mc, doc.Options{DisableGC: true})
	if autoLoad {
		sub.Load(mc)
	}
	opts := meter.Literal[map[string]any](mc, 128, func() map[string]any {
		return map[string]any{}
	})
	if autoLoad {
		meter.Set(mc, opts, "autoLoad", true)
	}
	m.setDocRefWithOpts(mc, txn, key, sub.GUID(mc), opts)
	txn.Doc(mc).PutSubdoc(mc, sub)
	return sub
}

// GetDoc returns the subdocument referenced under key within parent d,
// or (nil, false) if key holds no live subdoc. The returned handle is
// the registered instance for the referenced GUID (created on first
// access for subdocs surfaced from a decoded update).
func (m *Map) GetDoc(mc *meter.Meter, d *doc.Doc, key string) (*doc.Doc, bool) {
	mc.Enter()
	defer mc.Leave()

	item, ok := m.branch.Map[utf16.ValidString(mc, key)]
	if !ok || item == nil || item.IsDeleted(mc) || item.Content.Kind != block.KindDoc {
		return nil, false
	}
	return d.Subdoc(mc, item.Content.DocGuid), true
}

// setDocRefWithOpts integrates a ContentDoc reference item under key.
func (m *Map) setDocRefWithOpts(mc *meter.Meter, txn *doc.TransactionMut, key, guid string, opts map[string]any) {
	mc.Enter()
	defer mc.Leave()

	key = utf16.ValidString(mc, key)
	clientID := txn.Doc(mc).ClientID(mc)
	clock := txn.Store(mc).GetClock(mc, clientID)

	var origin *block.ID
	var left *block.Item
	if existing, ok := m.branch.Map[key]; ok {
		left = existing
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}

	keyCopy := key
	item := meter.Pointer(mc, block.Item{
		ID:        block.ID{Client: clientID, Clock: clock},
		Len:       1,
		Origin:    origin,
		Left:      left,
		Content:   block.Content{Kind: block.KindDoc, DocGuid: guid, DocOpts: opts},
		Parent:    block.Parent{Kind: block.ParentBranch, Branch: m.branch},
		ParentSub: meter.Pointer(mc, keyCopy),
		Flags:     block.FlagCountable,
	})

	txn.Store(mc).PushBlock(mc, item)
	if dropped := item.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, item)
	}
}

// Branch.Item back to the Item.
func (m *Map) setNested(mc *meter.Meter, txn *doc.TransactionMut, key string, inner *block.Branch) {
	mc.Enter()
	defer mc.Leave()

	key = utf16.ValidString(mc, key)
	clientID := txn.Doc(mc).ClientID(mc)
	clock := txn.Store(mc).GetClock(mc, clientID)

	var origin *block.ID
	var left *block.Item
	if existing, ok := m.branch.Map[key]; ok {
		left = existing
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}

	keyCopy := key
	item := meter.Pointer(mc, block.Item{
		ID:        block.ID{Client: clientID, Clock: clock},
		Len:       1,
		Origin:    origin,
		Left:      left,
		Content:   block.Content{Kind: block.KindType, Branch: inner},
		Parent:    block.Parent{Kind: block.ParentBranch, Branch: m.branch},
		ParentSub: meter.Pointer(mc, keyCopy),
		Flags:     block.FlagCountable,
	})

	txn.Store(mc).PushBlock(mc, item)
	if dropped := item.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, item)
	}
}

// extractValue unpacks a Content into the user-visible value.
//
// For KindType the returned wrapper is the right concrete type
// based on Branch.TypeRef — *Map for TypeRefMap, *Array for
// TypeRefArray, *Text for TypeRefText. Returns nil for branch
// types we have not implemented yet (Xml*, Doc).
func extractValue(mc *meter.Meter, c block.Content) any {
	mc.Enter()
	defer mc.Leave()

	switch c.Kind {
	case block.KindAny:
		if len(c.Anys) > 0 {
			return c.Anys[0]
		}
	case block.KindString:
		return c.Str
	case block.KindEmbed:
		if len(c.Anys) > 0 {
			return c.Anys[0]
		}
	case block.KindBinary:
		return c.Bytes
	case block.KindType:
		if c.Branch == nil {
			return nil
		}
		switch c.Branch.TypeRef {
		case block.TypeRefMap:
			return NewMap(mc, c.Branch)
		case block.TypeRefArray:
			return NewArray(mc, c.Branch)
		case block.TypeRefText:
			return NewText(mc, c.Branch)
		}
	}
	return nil
}
