package types

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
)

func init() {
	// Bridge the doc -> types direction without an import cycle: doc
	// invokes this hook at Commit to dispatch shared-type events.
	doc.TypeEventHook = dispatchEvents
}

// KeyChange describes one map key's change within a transaction: the
// kind of change and the value the key held before it. Mirrors yjs's
// YMapEvent change entry ({ action, oldValue }).
type KeyChange struct {
	// Action is "add", "update", or "delete".
	Action string
	// OldValue is the value the key held before this transaction; nil
	// for an "add".
	OldValue any
}

// MapEvent is delivered to Map observers after a transaction that
// changed the map. Mirrors yjs YMapEvent.
type MapEvent struct {
	// Target is the map that changed.
	Target *Map
	// Path is the location of Target relative to the type whose
	// ObserveDeep callback is firing; empty for a shallow Observe and
	// for a deep observer on Target itself.
	Path []any
	// Keys maps each changed key to how it changed. Only keys whose
	// observable value actually changed appear (a key added and
	// removed in the same transaction is omitted, matching yjs).
	Keys map[string]KeyChange
}

// KeysChanged returns the set of keys that changed, for callers that
// only need the names.
func (e *MapEvent) KeysChanged(mc *meter.Meter) []string {
	mc.Enter()
	defer mc.Leave()

	out := meter.MakeSlice[[]string, string](mc, uint64(0), uint64(len(e.Keys)))
	for k := range e.Keys {
		mc.Step()

		out = meter.Append(mc, out, k)
	}
	return out
}

// Observe registers fn to run after every transaction that changes
// this map, with a MapEvent describing the changed keys. Returns an
// unsubscribe function. Observers fire on local edits and on remote
// updates applied via ApplyUpdate.
//
// Mirrors yjs ymap.observe. The callback runs while the doc write
// lock is held; it must not start a new transaction on the same doc.
func (m *Map) Observe(mc *meter.Meter, fn func(*MapEvent)) func() {
	mc.Enter()
	defer mc.Leave()

	adapter := func(ev any) {
		if e, ok := ev.(*MapEvent); ok {
			fn(e)
		}
	}
	m.branch.Observers = meter.Append(mc, m.branch.Observers, adapter)
	idx := len(m.branch.Observers) - 1
	return func() {
		// Detach by clearing the slot; keep indices stable so a later
		// unsubscribe does not target the wrong observer.
		if idx < len(m.branch.Observers) {
			m.branch.Observers[idx] = nil
		}
	}
}

// ObserveDeep registers fn to run after any transaction that changes
// this map OR any shared type nested under it, with the list of events
// (each carrying its Path relative to this map). Returns an
// unsubscribe function. Mirrors yjs ymap.observeDeep.
func (m *Map) ObserveDeep(mc *meter.Meter, fn func([]any)) func() {
	mc.Enter()
	defer mc.Leave()

	return observeDeep(mc, m.branch, fn)
}

// dispatchEvents is installed as doc.TypeEventHook. It fires observers the
// way yjs cleanupTransactions does: types in the order they were first
// changed, deleted types skipped, then every deep observer once per
// transaction with all the events under it.
//
// Routing is by which kind of change was recorded (keyed vs positional),
// which is reliable even for root branches whose TypeRef defaults to zero.
func dispatchEvents(mc *meter.Meter, t *doc.TransactionMut) {
	mc.Enter()
	defer mc.Leave()

	// Events per type, for the type itself and each of its ancestors, in
	// the order the types were first reached: yjs changedParentTypes. An
	// event is recorded here before its shallow observers run, as yjs
	// callTypeObservers does.
	var parents []*block.Branch
	under := meter.Literal[map[*block.Branch][]any](mc, 128, func() map[*block.Branch][]any {
		return map[*block.Branch][]any{}
	})

	for _, br := range t.ChangedTypes(mc) {
		mc.Step()

		// A type deleted later in this transaction fires nothing.
		if br.Item != nil && br.Item.IsDeleted(mc) {
			continue
		}
		var ev any
		if keys := t.ChangedKeys(mc, br); len(keys) > 0 {
			ev = buildMapEvent(mc, t, br, keys)
		} else if t.PositionalChanged(mc, br) {
			// Positional change: Text (tagged TypeRefText by NewText) or
			// XmlText, whose Observe takes a TextEvent too, vs Array (the
			// default). Root branches otherwise share TypeRef
			// zero, so we cannot rely on it for arrays; the Text tag is the
			// one reliable discriminator.
			if br.TypeRef == block.TypeRefText || br.TypeRef == block.TypeRefXmlText {
				ev = meter.Pointer(mc, TextEvent{Target: meter.Pointer(mc, Text{branch: br}), Delta: buildTextDelta(mc, t, br)})
			} else {
				ev = meter.Pointer(mc, ArrayEvent{Target: meter.Pointer(mc, Array{branch: br}), Delta: buildPositionalDelta(mc, t, br)})
			}
		}
		if ev == nil {
			continue
		}
		for anc := br; anc != nil; anc = ancestorBranch(mc, anc) {
			mc.Step()

			if _, seen := under[anc]; !seen {
				parents = meter.Append(mc, parents, anc)
			}
			meter.Set(mc, under, anc, meter.Append(mc, under[anc], ev))
		}
		for _, o := range br.Observers {
			mc.Step()

			if o != nil {
				o(ev)
			}
		}
	}

	// Deep observers run after every shallow one, each once, with the
	// events of its own type and everything under it: shallower paths
	// first, events of the same depth in the order they were recorded.
	// Each type gets its own copy of an event with the path relative to
	// that type, so a callback that keeps an event keeps the path it was
	// given (yjs shares one event object and moves its path, #768).
	// Which types get a deep call is settled before the first deep callback
	// runs, as yjs queues them: a type that had no deep observer at that
	// point gets no deep call in this transaction, even if a deep callback
	// registers one on it.
	var deepTargets []*block.Branch
	for _, anc := range parents {
		mc.Step()

		if hasDeepObserver(mc, anc) && (anc.Item == nil || !anc.Item.IsDeleted(mc)) {
			deepTargets = meter.Append(mc, deepTargets, anc)
		}
	}
	for _, anc := range deepTargets {
		mc.Step()

		type pathed struct {
			ev    any
			depth int
		}
		batch := meter.MakeSlice[[]pathed, pathed](mc, uint64(0), uint64(len(under[anc])))
		for _, ev := range under[anc] {
			mc.Step()

			target := eventBranch(mc, ev)
			if target == nil || (target.Item != nil && target.Item.IsDeleted(mc)) {
				continue
			}
			path, ok := pathBetween(mc, anc, target)
			if !ok {
				continue
			}
			batch = meter.Append(mc, batch, pathed{ev: withPath(mc, ev, path), depth: len(path)})
		}
		meter.SliceStable(mc, batch, func(i, j int) bool { return batch[i].depth < batch[j].depth })
		events := meter.MakeSlice[[]any, any](mc, uint64(len(batch)), uint64(len(batch)))
		for i, p := range batch {
			mc.Step()

			events[i] = p.ev
		}
		for _, o := range anc.DeepObservers {
			mc.Step()

			if o != nil {
				o(events)
			}
		}
	}
}

// hasDeepObserver reports whether br has a live deep observer. An
// unsubscribed observer leaves a nil slot behind.
func hasDeepObserver(mc *meter.Meter, br *block.Branch) bool {
	mc.Enter()
	defer mc.Leave()

	for _, o := range br.DeepObservers {
		mc.Step()

		if o != nil {
			return true
		}
	}
	return false
}

// eventBranch returns the branch an event is about.
func eventBranch(mc *meter.Meter, ev any) *block.Branch {
	mc.Enter()
	defer mc.Leave()

	switch e := ev.(type) {
	case *MapEvent:
		return e.Target.branch
	case *ArrayEvent:
		return e.Target.branch
	case *TextEvent:
		return e.Target.branch
	}
	return nil
}

// ancestorBranch returns the branch that directly contains br (the
// parent of br's owning item), or nil if br is a root.
func ancestorBranch(mc *meter.Meter, br *block.Branch) *block.Branch {
	mc.Enter()
	defer mc.Leave()

	if br == nil || br.Item == nil {
		return nil
	}
	return br.Item.Parent.Branch
}

// pathBetween returns the path of segments (map keys and array
// indices) from ancestor down to target, or ok=false when target is
// not nested under ancestor.
func pathBetween(mc *meter.Meter, ancestor, target *block.Branch) ([]any, bool) {
	mc.Enter()
	defer mc.Leave()

	var segs []any
	cur := target
	for cur != ancestor {
		mc.Step()

		item := cur.Item
		if item == nil {
			return nil, false // reached a root without hitting ancestor
		}
		if item.ParentSub != nil {
			segs = meter.Append[[]any, any](mc, segs, *item.ParentSub)
		} else {
			segs = meter.Append[[]any, any](mc, segs, itemIndex(mc, item))
		}
		cur = item.Parent.Branch
		if cur == nil {
			return nil, false
		}
	}
	// segs is target..ancestor; reverse to ancestor..target.
	for i, j := 0, len(segs)-1; i < j; i, j = i+1, j-1 {
		mc.Step()

		segs[i], segs[j] = segs[j], segs[i]
	}
	return segs, true
}

// itemIndex returns the positional index of item within its parent's
// live element sequence (the path segment for an array-nested type).
func itemIndex(mc *meter.Meter, item *block.Item) int {
	mc.Enter()
	defer mc.Leave()

	idx := 0
	parent := item.Parent.Branch
	if parent == nil {
		return 0
	}
	for it := parent.Start; it != nil && it != item; it = it.Right {
		mc.Step()

		if !it.IsDeleted(mc) && it.IsCountable(mc) {
			idx += int(it.Len)
		}
	}
	return idx
}

// withPath returns a copy of ev carrying path, the location of its target
// relative to the deep observer it is delivered to.
func withPath(mc *meter.Meter, ev any, path []any) any {
	mc.Enter()
	defer mc.Leave()

	switch e := ev.(type) {
	case *MapEvent:
		c := *e
		c.Path = path
		return meter.Pointer(mc, c)
	case *ArrayEvent:
		c := *e
		c.Path = path
		return meter.Pointer(mc, c)
	case *TextEvent:
		c := *e
		c.Path = path
		return meter.Pointer(mc, c)
	}
	return ev
}

// adds reports whether item was created during this transaction (the
// y-protocols adds() predicate: clock >= beforeState).
func adds(mc *meter.Meter, t *doc.TransactionMut, it *block.Item) bool {
	mc.Enter()
	defer mc.Leave()

	return it != nil && it.ID.Clock >= t.BeforeState(mc)[it.ID.Client]
}

// deletes reports whether item was tombstoned during this transaction.
func deletes(mc *meter.Meter, t *doc.TransactionMut, it *block.Item) bool {
	mc.Enter()
	defer mc.Leave()

	return it != nil && t.DeletedThisTxn(mc, it.ID)
}

// contentValues returns each element value of an item's content, the
// equivalent of yjs item.content.getContent(). A multi-value
// ContentAny item (a packed range insert) yields all its values; other
// content kinds yield a single value.
func contentValues(mc *meter.Meter, c block.Content) []any {
	mc.Enter()
	defer mc.Leave()

	if c.Kind == block.KindAny {
		return meter.Append(mc, []any(nil), c.Anys...)
	}
	return meter.Literal[[]any](mc, 144, func() []any {
		return []any{extractValue(mc, c)}
	})
}

// buildMapEvent builds a MapEvent for a changed map branch following
// yjs YMapEvent.keys semantics. The caller fires it to observers.
func buildMapEvent(mc *meter.Meter, t *doc.TransactionMut, br *block.Branch, changed map[string]struct{}) *MapEvent {
	mc.Enter()
	defer mc.Leave()

	before := t.BeforeState(mc)
	adds := func(it *block.Item) bool {
		return it != nil && it.ID.Clock >= before[it.ID.Client]
	}
	deletes := func(it *block.Item) bool {
		return it != nil && t.DeletedThisTxn(mc, it.ID)
	}

	keys := meter.MakeMap[map[string]KeyChange, string, KeyChange](mc, uint64(len(changed)))
	for key := range changed {
		mc.Step()

		item := br.Map[key] // current winner (may be deleted)
		if item == nil {
			continue
		}
		var change KeyChange
		var skip bool
		if adds(item) {
			prev := item.Left
			for prev != nil && adds(prev) {
				mc.Step()

				prev = prev.Left
			}
			if deletes(item) {
				if prev != nil && deletes(prev) {
					change = KeyChange{Action: "delete", OldValue: extractValue(mc, prev.Content)}
				} else {
					skip = true // added and removed this txn: net no-op
				}
			} else {
				if prev != nil && deletes(prev) {
					change = KeyChange{Action: "update", OldValue: extractValue(mc, prev.Content)}
				} else {
					change = KeyChange{Action: "add"}
				}
			}
		} else {
			if deletes(item) {
				change = KeyChange{Action: "delete", OldValue: extractValue(mc, item.Content)}
			} else {
				skip = true // no observable change
			}
		}
		if !skip {
			meter.Set(mc, keys, key, change)
		}
	}
	// Return even when keys ends up empty (e.g. a key added and removed
	// in the same transaction nets to no-op): yjs still delivers the
	// event, with an empty changes.keys, because the type was touched.
	return meter.Pointer(mc, MapEvent{Target: meter.Pointer(mc, Map{branch: br}), Keys: keys})
}

// ArrayDeltaOp is one operation of an array change delta. Exactly one
// of the three is set: Insert (the inserted values), Delete (a count
// of removed elements), or Retain (a count of unchanged elements). The
// text DeltaOp (text_format.go) is the string-valued analogue for Text.
type ArrayDeltaOp struct {
	Insert []any
	Delete int
	Retain int
}

// ArrayEvent is delivered to Array observers (Array.Observe) after a
// transaction that changed the array. Delta is the Quill-style change
// description. Mirrors yjs YArrayEvent.
type ArrayEvent struct {
	Target *Array
	// Path locates Target relative to the ObserveDeep type firing;
	// empty for shallow Observe.
	Path  []any
	Delta []ArrayDeltaOp
}

// Observe registers fn to run after every transaction that changes
// this array, with an ArrayEvent carrying the change delta. Returns an
// unsubscribe function. Fires on local edits and remote updates.
//
// Mirrors yjs yarray.observe. The callback runs while the doc write
// lock is held; it must not start a new transaction on the same doc.
func (a *Array) Observe(mc *meter.Meter, fn func(*ArrayEvent)) func() {
	mc.Enter()
	defer mc.Leave()

	adapter := func(ev any) {
		if e, ok := ev.(*ArrayEvent); ok {
			fn(e)
		}
	}
	a.branch.Observers = meter.Append(mc, a.branch.Observers, adapter)
	idx := len(a.branch.Observers) - 1
	return func() {
		if idx < len(a.branch.Observers) {
			a.branch.Observers[idx] = nil
		}
	}
}

// op kinds for the delta builder.
const (
	opNone = iota
	opInsert
	opDelete
	opRetain
)

// buildPositionalDelta walks a branch's positional item list and
// builds the array change delta following yjs YEvent.changes: items
// deleted this transaction (and not also added) become delete ops,
// items added this transaction become insert ops, surviving
// pre-existing items become retain ops. A trailing retain is dropped
// (a delta ending in retain is meaningless).
func buildPositionalDelta(mc *meter.Meter, t *doc.TransactionMut, br *block.Branch) []ArrayDeltaOp {
	mc.Enter()
	defer mc.Leave()

	var delta []ArrayDeltaOp
	var cur ArrayDeltaOp
	kind := opNone
	pack := func() {
		if kind != opNone {
			delta = meter.Append(mc, delta, cur)
			cur = ArrayDeltaOp{}
			kind = opNone
		}
	}
	for it := br.Start; it != nil; it = it.Right {
		mc.Step()

		switch {
		case it.IsDeleted(mc):
			if deletes(mc, t, it) && !adds(mc, t, it) {
				if kind != opDelete {
					pack()
					kind = opDelete
				}
				cur.Delete += int(it.Len)
			}
		case adds(mc, t, it):
			if kind != opInsert {
				pack()
				cur = ArrayDeltaOp{Insert: meter.Literal[[]any](mc, 128, func() []any {
					return []any{}
				})}
				kind = opInsert
			}
			cur.Insert = meter.Append(mc, cur.Insert, contentValues(mc, it.Content)...)
		default:
			if kind != opRetain {
				pack()
				kind = opRetain
			}
			cur.Retain += int(it.Len)
		}
	}
	// Drop a trailing retain; pack any trailing insert/delete.
	if kind != opNone && kind != opRetain {
		pack()
	}
	return delta
}

// ObserveDeep registers fn to run after any transaction that changes
// this array OR any shared type nested under it, with the list of
// events (each carrying its Path relative to this array). Returns an
// unsubscribe function. Mirrors yjs yarray.observeDeep.
func (a *Array) ObserveDeep(mc *meter.Meter, fn func([]any)) func() {
	mc.Enter()
	defer mc.Leave()

	return observeDeep(mc, a.branch, fn)
}

// TextEvent is delivered to Text observers (Text.Observe) after a
// transaction that changed the text. Delta is the Quill-style,
// formatting-aware change description (the same DeltaOp shape ToDelta
// produces). Mirrors yjs YTextEvent.
type TextEvent struct {
	Target *Text
	// Path locates Target relative to the ObserveDeep type firing;
	// empty for shallow Observe.
	Path  []any
	Delta []DeltaOp
}

// Observe registers fn to run after every transaction that changes
// this text, with a TextEvent carrying the formatting-aware change
// delta. Returns an unsubscribe function. Fires on local edits and
// remote updates.
//
// Mirrors yjs ytext.observe. The callback runs while the doc write
// lock is held; it must not start a new transaction on the same doc.
func (t *Text) Observe(mc *meter.Meter, fn func(*TextEvent)) func() {
	mc.Enter()
	defer mc.Leave()

	adapter := func(ev any) {
		if e, ok := ev.(*TextEvent); ok {
			fn(e)
		}
	}
	t.branch.Observers = meter.Append(mc, t.branch.Observers, adapter)
	idx := len(t.branch.Observers) - 1
	return func() {
		if idx < len(t.branch.Observers) {
			t.branch.Observers[idx] = nil
		}
	}
}

// observeDeep registers a deep observer on a branch and returns an
// unsubscribe. Shared by Map.ObserveDeep / Array.ObserveDeep.
func observeDeep(mc *meter.Meter, br *block.Branch, fn func([]any)) func() {
	mc.Enter()
	defer mc.Leave()

	br.DeepObservers = meter.Append(mc, br.DeepObservers, fn)
	idx := len(br.DeepObservers) - 1
	return func() {
		if idx < len(br.DeepObservers) {
			br.DeepObservers[idx] = nil
		}
	}
}

// buildTextDelta is a faithful port of yjs YTextEvent.delta: it walks
// the text's items tracking the formatting active at the cursor
// (currentAttrs), and emits insert (string or embed) / delete / retain
// ops, where retain ops carry the attribute changes a format marker
// introduced. Trailing retains without attribute changes are dropped.
func buildTextDelta(mc *meter.Meter, txn *doc.TransactionMut, br *block.Branch) []DeltaOp {
	mc.Enter()
	defer mc.Leave()

	var delta []DeltaOp
	currentAttrs := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})
	oldAttrs := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})
	attributes := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	}) // retain-op attribute diff
	action := "" // "insert" | "delete" | "retain" | ""
	insertStr := ""
	var insertEmbed any
	insertIsEmbed := false
	var retain, deleteLen uint64

	addOp := func() {
		if action == "" {
			return
		}
		switch action {
		case "delete":
			if deleteLen > 0 {
				delta = meter.Append(mc, delta, DeltaOp{Delete: deleteLen})
			}
			deleteLen = 0
		case "insert":
			if insertIsEmbed {
				delta = meter.Append(mc, delta, DeltaOp{Embed: insertEmbed, Attributes: nonNullAttrs(mc, currentAttrs)})
			} else if insertStr != "" {
				delta = meter.Append(mc, delta, DeltaOp{Insert: insertStr, Attributes: nonNullAttrs(mc, currentAttrs)})
			}
			insertStr = ""
			insertEmbed = nil
			insertIsEmbed = false
		case "retain":
			if retain > 0 {
				op := DeltaOp{Retain: retain}
				if len(attributes) > 0 {
					op.Attributes = copyAttrs(mc, attributes)
				}
				delta = meter.Append(mc, delta, op)
			}
			retain = 0
		}
		action = ""
	}

	for it := br.Start; it != nil; it = it.Right {
		mc.Step()

		switch it.Content.Kind {
		case block.KindEmbed, block.KindType, block.KindDoc:
			if adds(mc, txn, it) {
				if !deletes(mc, txn, it) {
					addOp()
					action = "insert"
					insertIsEmbed = true
					insertEmbed = contentValues(mc, it.Content)[0]
					addOp()
				}
			} else if deletes(mc, txn, it) {
				if action != "delete" {
					addOp()
					action = "delete"
				}
				deleteLen++
			} else if !it.IsDeleted(mc) {
				if action != "retain" {
					addOp()
					action = "retain"
				}
				retain++
			}
		case block.KindString:
			if adds(mc, txn, it) {
				if !deletes(mc, txn, it) {
					if action != "insert" {
						addOp()
						action = "insert"
					}
					insertStr += it.Content.Str
				}
			} else if deletes(mc, txn, it) {
				if action != "delete" {
					addOp()
					action = "delete"
				}
				deleteLen += it.Len
			} else if !it.IsDeleted(mc) {
				if action != "retain" {
					addOp()
					action = "retain"
				}
				retain += it.Len
			}
		case block.KindFormat:
			key := it.Content.FormatKey
			var value any
			if len(it.Content.Anys) > 0 {
				value = it.Content.Anys[0]
			}
			if adds(mc, txn, it) {
				if !deletes(mc, txn, it) {
					if !attrValuesEqual(mc, currentAttrs[key], value) {
						if action == "retain" {
							addOp()
						}
						if attrValuesEqual(mc, value, oldAttrs[key]) {
							delete(attributes, key)
						} else {
							meter.Set(mc, attributes, key, value)
						}
					}
				}
			} else if deletes(mc, txn, it) {
				meter.Set(mc, oldAttrs, key, value)
				if !attrValuesEqual(mc, currentAttrs[key], value) {
					if action == "retain" {
						addOp()
					}
					meter.Set(mc, attributes, key, currentAttrs[key])
				}
			} else if !it.IsDeleted(mc) {
				meter.Set(mc, oldAttrs, key, value)
				if attr, ok := attributes[key]; ok {
					if !attrValuesEqual(mc, attr, value) {
						if action == "retain" {
							addOp()
						}
						if value == nil {
							delete(attributes, key)
						} else {
							meter.Set(mc, attributes, key, value)
						}
					}
				}
			}
			if !it.IsDeleted(mc) {
				if action == "insert" {
					addOp()
				}
				updateCurrentAttrs(mc, currentAttrs, key, value)
			}
		}
	}
	addOp()
	// Drop trailing retains that carry no attribute change.
	for len(delta) > 0 {
		mc.Step()

		last := delta[len(delta)-1]
		if last.Retain > 0 && last.Insert == "" && last.Embed == nil && last.Delete == 0 && len(last.Attributes) == 0 {
			delta = delta[:len(delta)-1]
		} else {
			break
		}
	}
	return delta
}

// nonNullAttrs copies attrs, dropping keys whose value is nil (a nil
// value in currentAttributes means the attribute is cleared, which an
// insert op should not carry). Returns nil for an empty result.
func nonNullAttrs(mc *meter.Meter, attrs Attrs) Attrs {
	mc.Enter()
	defer mc.Leave()

	var out Attrs
	for k, v := range attrs {
		mc.Step()

		if v != nil {
			if out == nil {
				out = meter.Literal[Attrs](mc, 128, func() Attrs {
					return Attrs{}
				})
			}
			meter.Set(mc, out, k, v)
		}
	}
	return out
}
