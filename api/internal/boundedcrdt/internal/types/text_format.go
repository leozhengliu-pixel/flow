package types

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"flow/api/internal/boundedcrdt/internal/anyval"
	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
	"flow/api/internal/boundedcrdt/internal/utf16"
)

// Attrs is the format-attribute map carried by KindFormat markers
// and exposed in Range / ToDelta output. Per
// docs/yrs-port-notes/types-text-rich.md §9.
//
// Nil values inside Attrs mean "clear this attribute" — they encode
// to the Any-Null tag on the wire and are interpreted by
// currentAttributes as removing the key. To distinguish "key not
// present" from "key present with nil value", check via the second
// return of a map lookup.
type Attrs = map[string]any

// DeltaOp is a single op from a Quill-style delta. Exactly one of
// Insert / Embed / Retain / Delete is set for any given op. Used by
// ToDelta to expose the doc and by ApplyDelta to ingest one.
type DeltaOp struct {
	// Insert is the text to insert at the cursor. Mutually exclusive
	// with Embed, Retain, Delete.
	Insert string

	// Embed is a single embedded value (image, mention, etc.). Mutually
	// exclusive with Insert, Retain, Delete. The Embed value's runtime
	// type matches whatever Y.encodeStateAsUpdate-produced JS clients
	// would round-trip through Any — typically a map[string]any.
	Embed any

	// Retain advances the cursor by N UTF-16 units, optionally
	// applying Attributes to the retained range. Mutually exclusive
	// with Insert, Embed, Delete.
	Retain uint64

	// Delete removes N UTF-16 units from the cursor. Mutually exclusive
	// with Insert, Embed, Retain.
	Delete uint64

	// Attributes applies to Insert, Embed, or Retain. An Insert or
	// Embed carries exactly these attributes: one in effect at the
	// cursor that is absent here is cleared for the inserted content,
	// and nil means none. On a Retain, a nil-valued key clears that
	// attribute over the range and an absent key leaves it as it is.
	Attributes Attrs
}

// InsertWithAttributes inserts str at idx carrying exactly attrs, as yjs
// insert(index, text, attributes) does: an attribute in effect at idx
// that attrs does not name is cleared for the inserted text, an opening
// marker is written for each value that differs from the one in effect,
// and the values in effect are restored after the text.
//
// nil attrs is Insert(idx, str): the text takes the formatting in effect
// at idx. A non-nil empty map is yjs's {}: the text is inserted with no
// attributes, every inherited one closed around it.
func (t *Text) InsertWithAttributes(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, str string, attrs Attrs) error {
	mc.Enter()
	defer mc.Leave()

	if str == "" {
		return nil
	}
	if attrs == nil {
		return t.Insert(mc, txn, idx, str)
	}
	str = utf16.ValidString(mc, str)
	length := t.Length(mc)
	if idx > length {
		return meter.Errorf(mc, "text: insert index %d out of range [0, %d]", idx, length)
	}
	attrs = validAttrs(mc, attrs)
	return t.insertContent(mc, txn, idx, block.Content{Kind: block.KindString, Str: str}, utf16.Length(mc, str), attrs, sortedAttrKeys(mc, attrs))
}

// insertContent inserts content at idx through insertText, with attrs in
// the key order keys. A text that never had a format item has no
// attributes in effect anywhere, so the position comes from the search
// markers and insertText starts from empty attributes; otherwise the
// cursor walks from the start and collects them, as yjs findPosition does
// for a type with formatting.
func (t *Text) insertContent(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, content block.Content, length uint64, attrs Attrs, keys []string) error {
	mc.Enter()
	defer mc.Leave()

	var pos *textPos
	if t.branch.HasFormatting {
		var err error
		if pos, err = findTextPos(mc, txn, t.branch, idx); err != nil {
			return err
		}
	} else {
		left, right, err := findTextPosition(mc, t.branch, txn, idx)
		if err != nil {
			return err
		}
		pos = meter.Pointer(mc, textPos{left: left, right: right, index: idx, attrs: meter.Literal[Attrs](mc, 128, func() Attrs {
			return Attrs{}
		})})
	}
	item, at := insertText(mc, txn, t.branch, pos, content, length, attrs, keys)
	if !item.IsDeleted(mc) {
		t.branch.Markers.ShiftAfter(mc, at, item.Len)
		rememberMarker(mc, t.branch, item, at)
	}
	return nil
}

// insertText inserts content at pos carrying exactly attrs, and leaves
// pos after the content and the markers that restore the attributes
// that were in effect. A port of yjs insertText: every attribute in
// effect that attrs does not name is set to nil (cleared), the cursor
// steps over tombstones and markers that already set the requested
// values, an opening marker goes in for each value that differs, then
// the content, then insertNegatedAttributes. keys is the caller's key
// order; the keys filled in come after it, in the cursor's order, and the
// whole list is enumerated as JavaScript enumerates the object yjs
// builds (jsPropertyOrder). attrs is not modified. It returns the
// content item and the index it went in at.
func insertText(mc *meter.Meter, txn *doc.TransactionMut, branch *block.Branch, pos *textPos, content block.Content, length uint64, attrs Attrs, keys []string) (*block.Item, uint64) {
	mc.Enter()
	defer mc.Leave()

	full := meter.MakeMap[Attrs, string, any](mc, uint64(len(attrs)+len(pos.attrs)))
	for k, v := range attrs {
		mc.Step()
		meter.Set(mc, full, k, v)
	}
	order := meter.Append(mc, []string(nil), keys...)
	for _, k := range pos.order {
		mc.Step()

		if _, ok := full[k]; !ok {
			meter.Set(mc, full, k, nil)
			order = meter.Append(mc, order, k)
		}
	}
	order = jsPropertyOrder(mc, order)
	minimizeAttributeChanges(mc, pos, full)
	negated := insertAttributes(mc, txn, branch, pos, full, order)
	at := pos.index
	item := integrateContent(mc, txn, branch, pos.left, pos.right, content, length)
	pos.right = item
	pos.forward(mc)
	insertNegatedAttributes(mc, txn, branch, pos, negated)
	return item, at
}

// integrateContent builds an item of the local client holding content
// between left and right, and integrates it. Format markers are built by
// buildFormatMarker.
func integrateContent(mc *meter.Meter, txn *doc.TransactionMut, branch *block.Branch, left, right *block.Item, content block.Content, length uint64) *block.Item {
	mc.Enter()
	defer mc.Leave()

	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}
	if right != nil {
		rid := right.ID
		rightOrigin = meter.Pointer(mc, rid)
	}
	clientID := txn.Doc(mc).ClientID(mc)
	item := meter.Pointer(mc, block.Item{
		ID:          block.ID{Client: clientID, Clock: txn.Store(mc).GetClock(mc, clientID)},
		Len:         length,
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content:     content,
		Parent:      block.Parent{Kind: block.ParentBranch, Branch: branch},
		Flags:       block.FlagCountable,
	})
	txn.Store(mc).PushBlock(mc, item)
	if dropped := item.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, item)
	}
	return item
}

// Format applies attrs to the range [idx, idx+length). It is a port of
// yjs YText.format / formatText: the cursor stops at idx before any
// format markers there, markers that already set a requested value are
// stepped over, an opening marker is written for every attribute that
// changes, markers inside the range for a requested key are deleted
// (they would otherwise re-establish the old value mid-range), and
// markers after the range restore what was in effect there.
//
// A nil value in attrs clears that attribute over the range. length == 0
// and empty attrs are no-ops. A range past the end of the text is an
// error; yjs instead appends newlines for the missing length, a Quill
// convention this port does not follow.
func (t *Text) Format(mc *meter.Meter, txn *doc.TransactionMut, idx, length uint64, attrs Attrs) error {
	mc.Enter()
	defer mc.Leave()

	if length == 0 || len(attrs) == 0 {
		return nil
	}
	total := t.Length(mc)
	if idx > total || length > total-idx {
		return meter.Errorf(mc, "text: format range of %d at %d exceeds length %d", length, idx, total)
	}
	pos, err := findTextPos(mc, txn, t.branch, idx)
	if err != nil {
		return err
	}
	return formatText(mc, txn, t.branch, pos, length, validAttrs(mc, attrs))
}

// textPos is a cursor between two items of a text, carrying the
// formatting in effect at that point. It mirrors yjs
// ItemTextListPosition.
type textPos struct {
	left, right *block.Item
	index       uint64
	attrs       Attrs
	// order lists the keys of attrs in the order a JavaScript Map
	// iterates yjs's currentAttributes: a set on a present key keeps its
	// place, a delete removes it, a set on an absent key appends it.
	order []string
}

// forward steps the cursor over right, which must not be nil. A live
// format marker updates the formatting in effect; live content advances
// the index. Deleted items change neither.
func (p *textPos) forward(mc *meter.Meter) {
	mc.Enter()
	defer mc.Leave()

	r := p.right
	if !r.IsDeleted(mc) {
		if r.Content.Kind == block.KindFormat {
			p.updateAttr(mc, r.Content.FormatKey, formatValue(mc, r))
		} else {
			p.index += r.Len
		}
	}
	p.left = r
	p.right = r.Right
}

// setAttr is currentAttributes.set(key, value); value may be nil.
func (p *textPos) setAttr(mc *meter.Meter, key string, value any) {
	mc.Enter()
	defer mc.Leave()

	if _, ok := p.attrs[key]; !ok {
		p.order = meter.Append(mc, p.order, key)
	}
	meter.Set(mc, p.attrs, key, value)
}

// deleteAttr is currentAttributes.delete(key).
func (p *textPos) deleteAttr(mc *meter.Meter, key string) {
	mc.Enter()
	defer mc.Leave()

	if _, ok := p.attrs[key]; !ok {
		return
	}
	delete(p.attrs, key)
	for i, k := range p.order {
		mc.Step()

		if k == key {
			p.order = meter.Append(mc, p.order[:i:i], p.order[i+1:]...)
			return
		}
	}
}

// updateAttr is yjs updateCurrentAttributes: nil clears the key.
func (p *textPos) updateAttr(mc *meter.Meter, key string, value any) {
	mc.Enter()
	defer mc.Leave()

	if value == nil {
		p.deleteAttr(mc, key)
		return
	}
	p.setAttr(mc, key, value)
}

// currentAttrs returns a copy of the attributes in effect and their keys
// in Map order.
func (p *textPos) currentAttrs(mc *meter.Meter) (Attrs, []string) {
	mc.Enter()
	defer mc.Leave()

	attrs := meter.MakeMap[Attrs, string, any](mc, uint64(len(p.attrs)))
	for k, v := range p.attrs {
		mc.Step()
		meter.Set(mc, attrs, k, v)
	}
	return attrs, meter.Append(mc, []string(nil), p.order...)
}

// findTextPos walks from the start of the text to index, splitting a
// string item the index falls inside. Like yjs findNextPosition it
// stops as soon as index units of content are behind it, so format
// markers right after the index stay ahead of the cursor. It does not
// use search markers: the formatting in effect at the cursor needs
// every marker before it.
func findTextPos(mc *meter.Meter, txn *doc.TransactionMut, branch *block.Branch, index uint64) (*textPos, error) {
	mc.Enter()
	defer mc.Leave()

	pos := meter.Pointer(mc, textPos{right: branch.Start, attrs: meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})})
	for pos.right != nil && index > 0 {
		mc.Step()

		r := pos.right
		if !r.IsDeleted(mc) && r.Content.Kind != block.KindFormat && index < r.Len {
			if txn.Store(mc).SplitBlock(mc, r, index) == nil {
				return nil, meter.Errorf(mc, "text: split failed at offset %d in item %v", index, r.ID)
			}
		}
		if !r.IsDeleted(mc) && r.Content.Kind != block.KindFormat {
			index -= r.Len
		}
		pos.forward(mc)
	}
	return pos, nil
}

// negatedAttrs holds the values to restore after a formatted range, in
// the order they were recorded. Setting an existing key keeps its place
// and deleting then setting moves it last, as with a JavaScript Map.
type negatedAttrs struct {
	keys   []string
	values map[string]any
}

func (n *negatedAttrs) set(mc *meter.Meter, key string, value any) {
	mc.Enter()
	defer mc.Leave()

	if n.values == nil {
		n.values = meter.Literal[map[string]any](mc, 128, func() map[string]any {
			return map[string]any{}
		})
	}
	if _, ok := n.values[key]; !ok {
		n.keys = meter.Append(mc, n.keys, key)
	}
	meter.Set(mc, n.values, key, value)
}

func (n *negatedAttrs) get(mc *meter.Meter, key string) (any, bool) {
	mc.Enter()
	defer mc.Leave()

	v, ok := n.values[key]
	return v, ok
}

func (n *negatedAttrs) remove(mc *meter.Meter, key string) {
	mc.Enter()
	defer mc.Leave()

	if _, ok := n.values[key]; !ok {
		return
	}
	delete(n.values, key)
	for i, k := range n.keys {
		mc.Step()

		if k == key {
			n.keys = meter.Append(mc, n.keys[:i], n.keys[i+1:]...)
			return
		}
	}
}

// minimizeAttributeChanges steps over deleted items and over format
// markers that already set the value attrs asks for, so no marker is
// written for a change that is already in effect. An attribute missing
// from attrs counts as nil. Mirrors yjs minimizeAttributeChanges.
func minimizeAttributeChanges(mc *meter.Meter, pos *textPos, attrs Attrs) {
	mc.Enter()
	defer mc.Leave()

	for pos.right != nil {
		mc.Step()

		r := pos.right
		if !r.IsDeleted(mc) && (r.Content.Kind != block.KindFormat || !attrValuesEqual(mc, attrs[r.Content.FormatKey], formatValue(mc, r))) {
			return
		}
		pos.forward(mc)
	}
}

// insertAttributes writes an opening marker for every attribute whose
// requested value differs from the one in effect, in the order of keys,
// and returns the values that were in effect so they can be restored
// after the range. Mirrors yjs insertAttributes, whose keys come in the
// order JavaScript enumerates the attributes object (jsPropertyOrder).
func insertAttributes(mc *meter.Meter, txn *doc.TransactionMut, branch *block.Branch, pos *textPos, attrs Attrs, keys []string) *negatedAttrs {
	mc.Enter()
	defer mc.Leave()

	negated := meter.Pointer(mc, negatedAttrs{})
	for _, key := range keys {
		mc.Step()

		value := attrs[key]
		current := pos.attrs[key]
		if attrValuesEqual(mc, current, value) {
			continue
		}
		negated.set(mc, key, current)
		pos.right = buildFormatMarker(mc, txn, pos.left, pos.right, branch, key, value)
		pos.forward(mc)
	}
	return negated
}

// formatText applies attrs to length units of content from pos. Mirrors
// yjs formatText: after the opening markers it walks the range, deleting
// markers for requested keys, and keeps walking past the range while
// values remain to restore and the next item is a marker or deleted, so
// a marker that already restores a value is reused instead of doubled.
func formatText(mc *meter.Meter, txn *doc.TransactionMut, branch *block.Branch, pos *textPos, length uint64, attrs Attrs) error {
	mc.Enter()
	defer mc.Leave()

	minimizeAttributeChanges(mc, pos, attrs)
	negated := insertAttributes(mc, txn, branch, pos, attrs, sortedAttrKeys(mc, attrs))
walk:
	for pos.right != nil {
		mc.Step()

		r := pos.right
		if length == 0 && (len(negated.keys) == 0 || (!r.IsDeleted(mc) && r.Content.Kind != block.KindFormat)) {
			break
		}
		if !r.IsDeleted(mc) {
			if r.Content.Kind == block.KindFormat {
				key, value := r.Content.FormatKey, formatValue(mc, r)
				if requested, ok := attrs[key]; ok {
					if attrValuesEqual(mc, requested, value) {
						negated.remove(mc, key)
					} else {
						if length == 0 {
							break walk
						}
						negated.set(mc, key, value)
					}
					txn.Delete(mc, r)
				} else {
					pos.setAttr(mc, key, value)
				}
			} else {
				if length < r.Len {
					if txn.Store(mc).SplitBlock(mc, r, length) == nil {
						return meter.Errorf(mc, "text: split failed at offset %d in item %v", length, r.ID)
					}
				}
				length -= r.Len
			}
		}
		pos.forward(mc)
	}
	// The range runs past the end of the text: yjs appends a newline for
	// each missing unit (Quill keeps a trailing newline). Format checks
	// its range first, so only ApplyDelta gets here.
	if length > 0 {
		if length > maxRetainPadding {
			return meter.Errorf(mc, "text: retain runs %d units past the end, more than %d", length, maxRetainPadding)
		}
		item := integrateContent(mc, txn, branch, pos.left, pos.right, block.Content{Kind: block.KindString, Str: strings.Repeat("\n", int(length))}, length)
		pos.right = item
		pos.forward(mc)
	}
	insertNegatedAttributes(mc, txn, branch, pos, negated)
	return nil
}

// insertNegatedAttributes restores the values recorded in negated after a
// formatted range. Deleted items are stepped over, and so are markers
// that already set a recorded value, which drop that value from the list.
// Mirrors yjs insertNegatedAttributes.
func insertNegatedAttributes(mc *meter.Meter, txn *doc.TransactionMut, branch *block.Branch, pos *textPos, negated *negatedAttrs) {
	mc.Enter()
	defer mc.Leave()

	for pos.right != nil {
		mc.Step()

		r := pos.right
		if !r.IsDeleted(mc) {
			if r.Content.Kind != block.KindFormat {
				break
			}
			value, ok := negated.get(mc, r.Content.FormatKey)
			if !ok || !attrValuesEqual(mc, value, formatValue(mc, r)) {
				break
			}
			negated.remove(mc, r.Content.FormatKey)
		}
		pos.forward(mc)
	}
	for _, key := range negated.keys {
		mc.Step()

		pos.right = buildFormatMarker(mc, txn, pos.left, pos.right, branch, key, negated.values[key])
		pos.forward(mc)
	}
}

// formatValue returns the value a format marker sets; nil clears.
func formatValue(mc *meter.Meter, it *block.Item) any {
	mc.Enter()
	defer mc.Leave()

	if len(it.Content.Anys) > 0 {
		return it.Content.Anys[0]
	}
	return nil
}

// InsertEmbed inserts a single embedded value at idx with no
// attributes: as yjs insertEmbed(index, embed) does, the attributes in
// effect at idx are closed around it. It is
// InsertEmbedWithAttributes(txn, idx, value, nil).
func (t *Text) InsertEmbed(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, value any) error {
	mc.Enter()
	defer mc.Leave()

	return t.InsertEmbedWithAttributes(mc, txn, idx, value, nil)
}

// InsertEmbedWithAttributes inserts a single embedded value at idx
// carrying exactly attrs, as yjs insertEmbed(index, embed, attributes).
// nil attrs and an empty map both mean no attributes. The value must be
// representable as a lib0 Any, typically a map[string]any or a scalar.
// A string value is inserted as text, as yjs does with a string embed;
// an empty string inserts nothing.
//
// Embeds count as one UTF-16 position in Text.Length. They are
// emitted by Range / ToDelta as an Embed op rather than a string
// chunk.
func (t *Text) InsertEmbedWithAttributes(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, value any, attrs Attrs) error {
	mc.Enter()
	defer mc.Leave()

	total := t.Length(mc)
	if idx > total {
		return meter.Errorf(mc, "text: embed index %d out of range [0, %d]", idx, total)
	}
	content, length := embedContent(mc, value)
	if length == 0 {
		return nil
	}
	if attrs == nil {
		attrs = meter.Literal[Attrs](mc, 128, func() Attrs {
			return Attrs{}
		})
	}
	attrs = validAttrs(mc, attrs)
	return t.insertContent(mc, txn, idx, content, length, attrs, sortedAttrKeys(mc, attrs))
}

// embedContent classifies an inserted value as yjs insertText does: a
// string is text, anything else an embed. It returns the content and its
// length in UTF-16 units.
func embedContent(mc *meter.Meter, value any) (block.Content, uint64) {
	mc.Enter()
	defer mc.Leave()

	if s, ok := value.(string); ok {
		s = utf16.ValidString(mc, s)
		return block.Content{Kind: block.KindString, Str: s}, utf16.Length(mc, s)
	}
	return block.Content{Kind: block.KindEmbed, Anys: meter.Literal[[]block.Any](mc, 144, func() []block.Any {
		return []block.Any{anyval.ValidJSON(mc, value)}
	})}, 1
}

// validAttrs returns attrs with each key fixed by utf16.ValidString and each
// value by anyval.ValidJSON (format values are written as JSON), copying the
// map only when something changes, so the caller's map is not modified; nil
// stays nil and an empty map stays empty. Two keys that become equal keep
// the value of the key that sorts last.
func validAttrs(mc *meter.Meter, attrs Attrs) Attrs {
	mc.Enter()
	defer mc.Leave()

	changed := false
	for k, v := range attrs {
		mc.Step()

		if !utf8.ValidString(k) || anyval.NeedsJSONFix(mc, v) {
			changed = true
			break
		}
	}
	if !changed {
		return attrs
	}
	keys := meter.MakeSlice[[]string, string](mc, uint64(0), uint64(len(attrs)))
	for k := range attrs {
		mc.Step()

		keys = meter.Append(mc, keys, k)
	}
	meter.Strings(mc, keys)
	out := meter.MakeMap[Attrs, string, any](mc, uint64(len(attrs)))
	for _, k := range keys {
		mc.Step()
		meter.Set(mc, out, utf16.ValidString(mc, k), anyval.ValidJSON(mc, attrs[k]))
	}
	return out
}

// ChunkKind discriminates the variants emitted by Text.Range.
type ChunkKind uint8

const (
	// ChunkString is a contiguous run of UTF-8 text from a KindString
	// item; the value passed to fn is a string.
	ChunkString ChunkKind = iota
	// ChunkEmbed is a single embedded value from a KindEmbed item;
	// the value passed to fn is whatever the embed payload's runtime
	// type is (typically map[string]any or a primitive Any).
	ChunkEmbed
)

// Range walks the document and invokes fn for each (kind, value, attrs)
// chunk. attrs reflects the current formatting at this chunk's start.
//
// Stops early when fn returns false. Returns immediately for an
// empty doc.
//
// Per docs/yrs-port-notes/types-text-rich.md §8 — companion to ToDelta.
// Explicit ChunkKind discriminator means a string-typed embed payload
// is not misidentified as a text chunk by callers that switch on
// the runtime type of the value.
func (t *Text) Range(mc *meter.Meter, fn func(kind ChunkKind, value any, attrs Attrs) bool) {
	mc.Enter()
	defer mc.Leave()

	current := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})
	for cur := t.branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur.IsDeleted(mc) {
			continue
		}
		switch cur.Content.Kind {
		case block.KindFormat:
			var value any
			if len(cur.Content.Anys) > 0 {
				value = cur.Content.Anys[0]
			}
			updateCurrentAttrs(mc, current, cur.Content.FormatKey, value)
		case block.KindString:
			if cur.Content.Str == "" {
				continue
			}
			if !fn(ChunkString, cur.Content.Str, copyAttrs(mc, current)) {
				return
			}
		case block.KindEmbed:
			var v any
			if len(cur.Content.Anys) > 0 {
				v = cur.Content.Anys[0]
			}
			if !fn(ChunkEmbed, v, copyAttrs(mc, current)) {
				return
			}
		}
	}
}

// maxRetainPadding bounds the newlines a retain past the end of a text may
// append, so the count fits an int on every platform.
const maxRetainPadding = math.MaxInt32

// ApplyDelta applies a Quill-style delta, as yjs applyDelta does with its
// default options: one cursor starts at the beginning of the text and
// every op continues from where the previous one left it.
//
//   - Insert (string) or Embed inserts at the cursor carrying exactly
//     Attributes; nil Attributes means none, so inherited attributes are
//     closed around the insert. A string Embed is inserted as text.
//   - Retain N formats the next N units with Attributes (nil means none,
//     which only moves the cursor). A retain that runs past the end of
//     the text appends a newline for each missing unit, up to
//     math.MaxInt32; a longer one fails before the op writes anything.
//   - Delete N deletes the next N units, or as many as there are.
//
// An op with every field zero is a no-op. yjs runs an explicit
// {retain: 0}, which can move the cursor across markers; a DeltaOp cannot
// say whether its Retain was given, so ygo cannot. Quill does not emit
// zero retains. yjs also writes the nulls of cleared attributes into the
// attributes object it is given, so a caller reusing one object across
// ops sees them in later ops; ygo never writes into Attributes.
//
// All ops execute inside the supplied txn so the entire delta is atomic
// from peers' perspective. Errors abort mid-delta; the partial mutation
// persists (the doc layer's TransactionMut has no Rollback).
func (t *Text) ApplyDelta(mc *meter.Meter, txn *doc.TransactionMut, ops []DeltaOp) error {
	mc.Enter()
	defer mc.Leave()

	pos := meter.Pointer(mc, textPos{right: t.branch.Start, attrs: meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})})
	defer t.branch.Markers.Invalidate(mc)
	for i, op := range ops {
		mc.Step()

		attrs := validAttrs(mc, op.Attributes)
		if attrs == nil {
			attrs = meter.Literal[Attrs](mc, 128, func() Attrs {
				return Attrs{}
			})
		}
		switch {
		case op.Insert != "":
			s := utf16.ValidString(mc, op.Insert)
			insertText(mc, txn, t.branch, pos, block.Content{Kind: block.KindString, Str: s}, utf16.Length(mc, s), attrs, sortedAttrKeys(mc, attrs))
		case op.Embed != nil:
			if content, length := embedContent(mc, op.Embed); length > 0 {
				insertText(mc, txn, t.branch, pos, content, length, attrs, sortedAttrKeys(mc, attrs))
			}
		case op.Retain > 0:
			// Refuse padding that cannot be built before anything is written.
			if rest := t.branch.BlockLen - pos.index; op.Retain > rest && op.Retain-rest > maxRetainPadding {
				return meter.Errorf(mc, "ApplyDelta op[%d] retain: runs %d units past the end, more than %d", i, op.Retain-rest, maxRetainPadding)
			}
			if err := formatText(mc, txn, t.branch, pos, op.Retain, attrs); err != nil {
				return meter.Errorf(mc, "ApplyDelta op[%d] retain: %w", i, err)
			}
		case op.Delete > 0:
			if err := deleteText(mc, txn, t.branch, pos, op.Delete); err != nil {
				return meter.Errorf(mc, "ApplyDelta op[%d] delete: %w", i, err)
			}
		}
	}
	return nil
}

// ToDelta returns the Quill-style delta representation of the doc.
// Consecutive strings are joined into one insert op up to the next live
// format marker or embed. As in yjs toDelta, every live format marker
// ends the op, even one that changes no attribute, so two adjacent ops
// can carry equal attributes, and an attribute named "ychange", which
// yjs reserves for snapshot diffs, is dropped at the next string. Embeds
// appear as separate ops.
//
// The returned slice has no Retain / Delete ops — those exist only
// in deltas representing CHANGES, not snapshots.
func (t *Text) ToDelta(mc *meter.Meter) []DeltaOp {
	mc.Enter()
	defer mc.Leave()

	var out []DeltaOp
	var str strings.Builder
	current := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})

	flush := func() {
		if str.Len() == 0 {
			return
		}
		out = meter.Append(mc, out, DeltaOp{
			Insert:     str.String(),
			Attributes: copyAttrs(mc, current),
		})
		str.Reset()
	}

	for cur := t.branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur.IsDeleted(mc) {
			continue
		}
		switch cur.Content.Kind {
		case block.KindFormat:
			flush()
			updateCurrentAttrs(mc, current, cur.Content.FormatKey, formatValue(mc, cur))
		case block.KindString:
			// yjs toDelta reserves "ychange" for snapshot diffs: without
			// snapshots, a string drops it from the attributes in effect.
			if _, ok := current["ychange"]; ok {
				flush()
				delete(current, "ychange")
			}
			str.WriteString(cur.Content.Str)
		case block.KindEmbed:
			flush()
			var v any
			if len(cur.Content.Anys) > 0 {
				v = cur.Content.Anys[0]
			}
			out = meter.Append(mc, out, DeltaOp{
				Embed:      v,
				Attributes: copyNonEmptyAttrs(mc, current),
			})
		}
	}
	flush()
	return out
}

// buildFormatMarker constructs and integrates a single KindFormat
// Item between left and right neighbours. Returns the new Item so
// the caller can chain subsequent inserts off it.
func buildFormatMarker(mc *meter.Meter, txn *doc.TransactionMut, left, right *block.Item, parentBranch *block.Branch, key string, value any) *block.Item {
	mc.Enter()
	defer mc.Leave()

	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID(mc)
		origin = meter.Pointer(mc, lid)
	}
	if right != nil {
		rid := right.ID
		rightOrigin = meter.Pointer(mc, rid)
	}
	clientID := txn.Doc(mc).ClientID(mc)
	clock := txn.Store(mc).GetClock(mc, clientID)
	marker := meter.Pointer(mc, block.Item{
		ID:          block.ID{Client: clientID, Clock: clock},
		Len:         1,
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content: block.Content{Kind: block.KindFormat, FormatKey: key, Anys: meter.Literal[[]block.Any](mc, 144, func() []block.Any {
			return []block.Any{value}
		})},
		Parent: block.Parent{Kind: block.ParentBranch, Branch: parentBranch},
		// FlagCountable left off — format markers do not contribute
		// to branch.BlockLen (per Content.IsCountable() returning
		// false for KindFormat).
	})
	txn.Store(mc).PushBlock(mc, marker)
	if dropped := marker.Integrate(mc, txn, 0); dropped {
		txn.Delete(mc, marker)
	}
	return marker
}

// currentAttributesAt is the branch-aware accumulator used by
// InsertWithAttributes / Format. Walks branch.Start forward until
// it reaches the supplied right neighbour (or the end), accumulating
// format markers along the way. The last occurrence per key wins.
//
// Tombstoned items are skipped. nil-valued format markers clear
// the key (deleted from the running map).
func currentAttributesAt(mc *meter.Meter, branch *block.Branch, right *block.Item) Attrs {
	mc.Enter()
	defer mc.Leave()

	out := meter.Literal[Attrs](mc, 128, func() Attrs {
		return Attrs{}
	})
	for cur := branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur == right {
			break
		}
		if cur.IsDeleted(mc) {
			continue
		}
		if cur.Content.Kind != block.KindFormat {
			continue
		}
		var value any
		if len(cur.Content.Anys) > 0 {
			value = cur.Content.Anys[0]
		}
		updateCurrentAttrs(mc, out, cur.Content.FormatKey, value)
	}
	return out
}

// updateCurrentAttrs applies one KindFormat marker to the running
// attrs map: a nil value clears the key, a non-nil value sets it.
func updateCurrentAttrs(mc *meter.Meter, attrs Attrs, key string, value any) {
	mc.Enter()
	defer mc.Leave()

	if value == nil {
		delete(attrs, key)
		return
	}
	meter.Set(mc, attrs, key, value)
}

// sortedAttrKeys returns the keys of attrs in the order yjs walks an
// attributes object the caller wrote with its keys in ascending order:
// integer-index keys first, by value, then the others ascending (see
// jsPropertyOrder). Each attribute becomes its own format marker, and the
// order the markers are emitted in is part of the encoded update. A Go
// map has no order of its own and ranging over one is randomised, which
// made the same edit encode to different bytes from run to run.
func sortedAttrKeys(mc *meter.Meter, attrs Attrs) []string {
	mc.Enter()
	defer mc.Leave()

	keys := meter.MakeSlice[[]string, string](mc, uint64(0), uint64(len(attrs)))
	for k := range attrs {
		mc.Step()

		keys = meter.Append(mc, keys, k)
	}
	meter.Strings(mc, keys)
	return jsPropertyOrder(mc, keys)
}

// jsPropertyOrder reorders keys, given in insertion order, the way
// JavaScript enumerates the own string keys of an object: integer-index
// keys (canonical decimal below 2^32-1, such as "0" or "10") first, in
// numeric order, then the rest in the order given. keys is reordered in
// place and returned.
func jsPropertyOrder(mc *meter.Meter, keys []string) []string {
	mc.Enter()
	defer mc.Leave()

	var ints []string
	for _, k := range keys {
		mc.Step()

		if isArrayIndexKey(mc, k) {
			ints = meter.Append(mc, ints, k)
		}
	}
	if len(ints) == 0 {
		return keys
	}
	meter.Slice(mc, ints, func(i, j int) bool {
		a, _ := strconv.ParseUint(ints[i], 10, 32)
		b, _ := strconv.ParseUint(ints[j], 10, 32)
		return a < b
	})
	rest := meter.MakeSlice[[]string, string](mc, uint64(0), uint64(len(keys)-len(ints)))
	for _, k := range keys {
		mc.Step()

		if !isArrayIndexKey(mc, k) {
			rest = meter.Append(mc, rest, k)
		}
	}
	return meter.Append(mc, meter.Append(mc, keys[:0], ints...), rest...)
}

// isArrayIndexKey reports whether k is an array index in the sense of
// ECMAScript: the canonical decimal form of an integer below 2^32-1.
func isArrayIndexKey(mc *meter.Meter, k string) bool {
	mc.Enter()
	defer mc.Leave()

	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < 1<<32-1
}

// copyAttrs returns a shallow copy of attrs so callers cannot
// mutate the walker's running map.
func copyAttrs(mc *meter.Meter, attrs Attrs) Attrs {
	mc.Enter()
	defer mc.Leave()

	if len(attrs) == 0 {
		return nil
	}
	out := meter.MakeMap[Attrs, string, any](mc, uint64(len(attrs)))
	for k, v := range attrs {
		mc.Step()
		meter.Set(mc, out, k, v)
	}
	return out
}

// copyNonEmptyAttrs returns nil for empty attrs (cleaner serialization)
// or a deep copy otherwise.
func copyNonEmptyAttrs(mc *meter.Meter, attrs Attrs) Attrs {
	mc.Enter()
	defer mc.Leave()

	if len(attrs) == 0 {
		return nil
	}
	return copyAttrs(mc, attrs)
}

// attrValuesEqual decides whether two attribute values are equal, the way yjs
// decides it: YText.js equalAttrs is `a === b || (both objects && equalFlat)`,
// and lib0 object.equalFlat is "same size, and every entry strictly equal".
//
// Each part of that is load-bearing, and each was checked against yjs 13.6.32:
//
//   - The identity check comes first, so one container reused for both values
//     is equal even when it holds a NaN, which fails an entry-by-entry check.
//   - "Strictly equal" is JS ===, so the comparison goes one level deep and a
//     nested container compares by identity: {x:{y:1}} and a separate,
//     equal-by-value {x:{y:1}} stay distinct.
//   - JS arrays and objects are both objects to equalFlat, so [1,2] equals
//     {"0":1,"1":2}, and [] equals {}.
//
// This function decides whether Format and InsertWithAttributes emit format
// markers at all, so any difference from yjs is a different item structure for
// the same operation, not just a different answer.
//
// It used to be a bare a == b, which panics at runtime when both values are
// maps or slices: reformatting a range carrying such an attribute, reading
// ToDelta across two such runs, or building an observer event for them crashed
// the process - and the event path runs on updates from peers.
//
// JS has one number type, so every Go integer and float kind compares by
// value as a number: int(1), int64(1) (how lib0 integers and bigints decode)
// and float64(1) (how JSON numbers decode) are equal, NaN is not equal to
// itself, and 0 equals -0.
func attrValuesEqual(mc *meter.Meter, a, b any) bool {
	mc.Enter()
	defer mc.Leave()

	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// A value stored as its JSON (anyval.ValidJSON) is equal only to another
	// with the same JSON bytes, never to a container whose entries happen to
	// match those bytes.
	ra, aRaw := a.(json.RawMessage)
	rb, bRaw := b.(json.RawMessage)
	if aRaw || bRaw {
		return aRaw && bRaw && bytes.Equal(ra, rb)
	}
	if sameObject(mc, a, b) {
		return true
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if isObjectLike(mc, va) && isObjectLike(mc, vb) {
		return equalFlat(mc, va, vb)
	}
	return strictEqual(mc, a, b)
}

// isObjectLike reports whether v is what JS would call an object for the
// purposes of equalFlat: a string-keyed map, or a list.
func isObjectLike(mc *meter.Meter, v reflect.Value) bool {
	mc.Enter()
	defer mc.Leave()

	switch v.Kind() {
	case reflect.Map:
		return v.Type().Key().Kind() == reflect.String
	case reflect.Slice, reflect.Array:
		return true
	}
	return false
}

// equalFlat is lib0 object.equalFlat: the same number of entries, and every
// entry of a present in b and strictly equal. A list's entries are keyed by
// their decimal index, which is how a JS array exposes them.
func equalFlat(mc *meter.Meter, a, b reflect.Value) bool {
	mc.Enter()
	defer mc.Leave()

	if a.Len() != b.Len() {
		return false
	}
	if a.Kind() == reflect.Map {
		it := a.MapRange()
		for it.Next() {
			mc.Step()

			bv, ok := entry(mc, b, it.Key().String())
			if !ok || !strictEqual(mc, it.Value().Interface(), bv) {
				return false
			}
		}
		return true
	}
	for i := 0; i < a.Len(); i++ {
		mc.Step()

		bv, ok := entry(mc, b, strconv.Itoa(i))
		if !ok || !strictEqual(mc, a.Index(i).Interface(), bv) {
			return false
		}
	}
	return true
}

// entry looks up key in an object-like value, treating a list index as its
// canonical decimal string.
func entry(mc *meter.Meter, v reflect.Value, key string) (any, bool) {
	mc.Enter()
	defer mc.Leave()

	if v.Kind() == reflect.Map {
		mv := v.MapIndex(reflect.ValueOf(key).Convert(v.Type().Key()))
		if !mv.IsValid() {
			return nil, false
		}
		return mv.Interface(), true
	}
	i, err := strconv.Atoi(key)
	if err != nil || i < 0 || i >= v.Len() || strconv.Itoa(i) != key {
		return nil, false
	}
	return v.Index(i).Interface(), true
}

// sameObject is JS === between two containers: the same object, not an equal
// one. Nil containers of the same type are the same, as null is.
//
// A zero-length slice is never the same object as another. Go gives every
// empty slice the runtime's shared zerobase address, so pointer identity would
// call two separately decoded empty arrays one object - and merge runs yjs keeps
// apart ({x:[]} next to {x:[]}). A non-empty slice has a real backing array.
func sameObject(mc *meter.Meter, a, b any) bool {
	mc.Enter()
	defer mc.Leave()

	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Type() != vb.Type() {
		return false
	}
	switch va.Kind() {
	case reflect.Map:
		if va.IsNil() || vb.IsNil() {
			return va.IsNil() && vb.IsNil()
		}
		return va.Pointer() == vb.Pointer()
	case reflect.Slice:
		if va.IsNil() || vb.IsNil() {
			return va.IsNil() && vb.IsNil()
		}
		if va.Len() == 0 {
			return false
		}
		return va.Pointer() == vb.Pointer() && va.Len() == vb.Len()
	}
	return false
}

// strictEqual is JS === for an entry: primitives by value, containers by
// identity. It never panics, including on a comparable type whose dynamic
// contents are not, such as a struct holding a slice.
func strictEqual(mc *meter.Meter, a, b any) bool {
	mc.Enter()
	defer mc.Leave()

	if a == nil || b == nil {
		return a == nil && b == nil
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if na, ok := jsNumber(mc, va); ok {
		nb, ok := jsNumber(mc, vb)
		return ok && na == nb
	}
	if va.Type() != vb.Type() {
		return false
	}
	switch va.Kind() {
	case reflect.Map, reflect.Slice:
		return sameObject(mc, a, b)
	}
	if !va.Comparable() {
		return false
	}
	return a == b
}

// jsNumber reports the value of v as a JavaScript number when v is a Go
// integer or float kind.
func jsNumber(mc *meter.Meter, v reflect.Value) (float64, bool) {
	mc.Enter()
	defer mc.Leave()

	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), true
	}
	return 0, false
}
