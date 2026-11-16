package types

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"strings"

	"flow/api/internal/boundedcrdt/internal/block"
	"flow/api/internal/boundedcrdt/internal/doc"
	"flow/api/internal/boundedcrdt/internal/utf16"
)

// XmlFragment is a root container of XmlElement and XmlText
// children. Mirrors Y.XmlFragment — the top-level entry for
// collaborative XML/DOM documents (ProseMirror, Tiptap, BlockNote
// all use this shape). Children are accessed positionally.
//
// Per docs/yrs-port-notes/nested-types.md §1, the underlying
// Branch carries TypeRef = TypeRefXmlFragment; NewXmlFragment sets
// it on wrap so cross-client receivers that wrap the same root by
// name end up with consistent state.
type XmlFragment struct {
	branch *block.Branch
}

// NewXmlFragment wraps the given branch as an XmlFragment. Sets
// Branch.TypeRef = TypeRefXmlFragment.
func NewXmlFragment(mc *meter.Meter, branch *block.Branch) *XmlFragment {
	mc.Enter()
	defer mc.Leave()

	branch.TypeRef = block.TypeRefXmlFragment
	return meter.Pointer(mc, XmlFragment{branch: branch})
}

// Branch returns the underlying *block.Branch.
func (f *XmlFragment) Branch(mc *meter.Meter) *block.Branch {
	mc.Enter()
	defer mc.Leave()
	return f.branch
}

// Length returns the number of live children.
func (f *XmlFragment) Length(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return f.branch.BlockLen
}

// InsertXmlElement inserts a new XmlElement child with the given
// nodeName at position idx. Returns the wrapper bound to the new
// child branch.
func (f *XmlFragment) InsertXmlElement(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, nodeName string) *XmlElement {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{
		TypeRef: block.TypeRefXmlElement,
		Name:    utf16.ValidString(mc, nodeName),
		Map: meter.Literal[map[string]*block.Item](mc, 128, func() map[string]*block.Item {
			return map[string]*block.Item{}
		}),
	})
	insertXmlChild(mc, txn, f.branch, idx, inner)
	return meter.Pointer(mc, XmlElement{branch: inner})
}

// InsertXmlText inserts a new XmlText child at position idx. Use
// the returned wrapper to populate the text content.
func (f *XmlFragment) InsertXmlText(mc *meter.Meter, txn *doc.TransactionMut, idx uint64) *XmlText {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{TypeRef: block.TypeRefXmlText})
	insertXmlChild(mc, txn, f.branch, idx, inner)
	return meter.Pointer(mc, XmlText{Text: Text{branch: inner}})
}

// Get returns the child at idx as *XmlElement, *XmlText, or nil if
// idx is out of range or the underlying item is of an unsupported
// kind.
func (f *XmlFragment) Get(mc *meter.Meter, idx uint64) any {
	mc.Enter()
	defer mc.Leave()

	return getXmlChildAt(mc, f.branch, idx)
}

// Delete removes length children starting at idx.
func (f *XmlFragment) Delete(mc *meter.Meter, txn *doc.TransactionMut, idx, length uint64) {
	mc.Enter()
	defer mc.Leave()

	asArray(mc, f.branch).Delete(mc, txn, idx, length)
}

// Range iterates over children in document order. fn receives the
// index and the wrapped child; return false to stop early.
func (f *XmlFragment) Range(mc *meter.Meter, fn func(idx uint64, child any) bool) {
	mc.Enter()
	defer mc.Leave()

	rangeXmlChildren(mc, f.branch, fn)
}

// ToString renders the fragment as an XML-like string by
// concatenating child renderings. Attribute keys are sorted
// ascending for deterministic output (tests and snapshots compare
// stable strings).
func (f *XmlFragment) ToString(mc *meter.Meter) string {
	mc.Enter()
	defer mc.Leave()

	var sb strings.Builder
	f.Range(mc, func(_ uint64, child any) bool {
		sb.WriteString(stringifyXmlChild(mc, child))
		return true
	})
	return sb.String()
}

// XmlElement is an XML element — a tag name (NodeName), an
// attribute map (Map-like), and a list of children (Array-like) —
// all backed by the same Branch.
//
// Attributes are stored as map-keyed Items with ContentAny string
// values. Children are stored as positional Items. The two
// surfaces (map and positional) coexist on the same Branch because
// they use disjoint fields (branch.Map vs branch.Start).
type XmlElement struct {
	branch *block.Branch
}

// NewXmlElement wraps the given branch as an XmlElement. Sets
// TypeRef = TypeRefXmlElement if not already set; the caller is
// responsible for setting Branch.Name (which is the nodeName) if
// the branch is freshly constructed at the root level. For nested
// elements, use the InsertXmlElement family which sets Name
// automatically.
func NewXmlElement(mc *meter.Meter, branch *block.Branch) *XmlElement {
	mc.Enter()
	defer mc.Leave()

	if branch.TypeRef != block.TypeRefXmlElement {
		branch.TypeRef = block.TypeRefXmlElement
	}
	if branch.Map == nil {
		branch.Map = meter.Literal[map[string]*block.Item](mc, 128, func() map[string]*block.Item {
			return map[string]*block.Item{}
		})
	}
	return meter.Pointer(mc, XmlElement{branch: branch})
}

// Branch returns the underlying *block.Branch.
func (e *XmlElement) Branch(mc *meter.Meter) *block.Branch {
	mc.Enter()
	defer mc.Leave()
	return e.branch
}

// NodeName returns the element's tag name (the JS Yjs nodeName).
func (e *XmlElement) NodeName(mc *meter.Meter) string {
	mc.Enter()
	defer mc.Leave()
	return e.branch.Name
}

// Length returns the number of live children.
func (e *XmlElement) Length(mc *meter.Meter) uint64 {
	mc.Enter()
	defer mc.Leave()
	return e.branch.BlockLen
}

// GetAttribute returns the value of the named attribute. The
// second return reports whether the attribute is set.
func (e *XmlElement) GetAttribute(mc *meter.Meter, name string) (string, bool) {
	mc.Enter()
	defer mc.Leave()

	item, ok := e.branch.Map[utf16.ValidString(mc, name)]
	if !ok || item.IsDeleted(mc) {
		return "", false
	}
	if item.Content.Kind != block.KindAny || len(item.Content.Anys) == 0 {
		return "", false
	}
	s, ok := item.Content.Anys[0].(string)
	if !ok {
		return "", false
	}
	return s, true
}

// SetAttribute sets the named attribute to value, replacing any
// prior value. Reuses the Map.Set machinery — attributes are
// map-keyed entries on the same Branch.
func (e *XmlElement) SetAttribute(mc *meter.Meter, txn *doc.TransactionMut, name, value string) {
	mc.Enter()
	defer mc.Leave()

	asMap(mc, e.branch).Set(mc, txn, name, value)
}

// RemoveAttribute tombstones the named attribute. Calling on an
// unset attribute is a no-op.
func (e *XmlElement) RemoveAttribute(mc *meter.Meter, txn *doc.TransactionMut, name string) {
	mc.Enter()
	defer mc.Leave()

	asMap(mc, e.branch).Delete(mc, txn, name)
}

// Attributes returns a snapshot of all live attribute (name, value)
// pairs. Returned map is a copy; mutate freely.
func (e *XmlElement) Attributes(mc *meter.Meter) map[string]string {
	mc.Enter()
	defer mc.Leave()

	out := meter.MakeMap[map[string]string, string, string](mc, uint64(len(e.branch.Map)))
	for k, v := range e.branch.Map {
		mc.Step()

		if v.IsDeleted(mc) {
			continue
		}
		if v.Content.Kind != block.KindAny || len(v.Content.Anys) == 0 {
			continue
		}
		s, ok := v.Content.Anys[0].(string)
		if !ok {
			continue
		}
		meter.Set(mc, out, k, s)
	}
	return out
}

// InsertXmlElement inserts a new XmlElement child with nodeName at
// position idx. Returns the wrapper.
func (e *XmlElement) InsertXmlElement(mc *meter.Meter, txn *doc.TransactionMut, idx uint64, nodeName string) *XmlElement {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{
		TypeRef: block.TypeRefXmlElement,
		Name:    utf16.ValidString(mc, nodeName),
		Map: meter.Literal[map[string]*block.Item](mc, 128, func() map[string]*block.Item {
			return map[string]*block.Item{}
		}),
	})
	insertXmlChild(mc, txn, e.branch, idx, inner)
	return meter.Pointer(mc, XmlElement{branch: inner})
}

// InsertXmlText inserts a new XmlText child at position idx.
func (e *XmlElement) InsertXmlText(mc *meter.Meter, txn *doc.TransactionMut, idx uint64) *XmlText {
	mc.Enter()
	defer mc.Leave()

	inner := meter.Pointer(mc, block.Branch{TypeRef: block.TypeRefXmlText})
	insertXmlChild(mc, txn, e.branch, idx, inner)
	return meter.Pointer(mc, XmlText{Text: Text{branch: inner}})
}

// Get returns the child at idx, wrapped as *XmlElement or *XmlText.
func (e *XmlElement) Get(mc *meter.Meter, idx uint64) any {
	mc.Enter()
	defer mc.Leave()

	return getXmlChildAt(mc, e.branch, idx)
}

// Delete removes length children starting at idx.
func (e *XmlElement) Delete(mc *meter.Meter, txn *doc.TransactionMut, idx, length uint64) {
	mc.Enter()
	defer mc.Leave()

	asArray(mc, e.branch).Delete(mc, txn, idx, length)
}

// Range iterates over children. fn returns false to stop early.
func (e *XmlElement) Range(mc *meter.Meter, fn func(idx uint64, child any) bool) {
	mc.Enter()
	defer mc.Leave()

	rangeXmlChildren(mc, e.branch, fn)
}

// ToString renders the element as yjs YXmlElement.toString does:
//
//	<nodename attr1="value1" attr2="value2">...children...</nodename>
//
// The tag is lower-cased, an empty element still gets a closing tag
// (never self-closing), and attribute keys are sorted. Attribute
// values are inlined without escaping, as in yjs; an HTML encoder
// would escape them.
func (e *XmlElement) ToString(mc *meter.Meter) string {
	mc.Enter()
	defer mc.Leave()

	var sb strings.Builder
	name := strings.ToLower(e.branch.Name)
	sb.WriteString("<")
	sb.WriteString(name)

	attrs := e.Attributes(mc)
	keys := meter.MakeSlice[[]string, string](mc, uint64(0), uint64(len(attrs)))
	for k := range attrs {
		mc.Step()

		keys = meter.Append(mc, keys, k)
	}
	meter.Strings(mc, keys)
	for _, k := range keys {
		mc.Step()

		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(attrs[k])
		sb.WriteString(`"`)
	}

	sb.WriteString(">")
	e.Range(mc, func(_ uint64, child any) bool {
		sb.WriteString(stringifyXmlChild(mc, child))
		return true
	})
	sb.WriteString("</")
	sb.WriteString(name)
	sb.WriteString(">")
	return sb.String()
}

// XmlText is rich-text content within an XML tree. Embeds the
// regular Text wrapper, so every Insert / InsertWithAttributes /
// Format / InsertEmbed / Range / ToDelta method is available
// directly. The Branch's TypeRef is set to TypeRefXmlText so wire
// round-trips identify the child as text rather than a generic
// Y.Text.
type XmlText struct {
	Text
}

// NewXmlText wraps the given branch as an XmlText, setting
// TypeRef = TypeRefXmlText.
func NewXmlText(mc *meter.Meter, branch *block.Branch) *XmlText {
	mc.Enter()
	defer mc.Leave()

	branch.TypeRef = block.TypeRefXmlText
	return meter.Pointer(mc, XmlText{Text: Text{branch: branch}})
}

// ToString returns the underlying text — alias for Text.String()
// kept for parity with XmlElement/XmlFragment ToString.
func (x *XmlText) ToString(mc *meter.Meter) string {
	mc.Enter()
	defer mc.Leave()

	return x.String(mc)
}

// asArray and asMap are tiny adapters that let XML wrappers reuse
// the Array and Map machinery on their own Branch. The XML
// surfaces are subsets of those types; rather than duplicate
// findInsertPosition, SplitBlock, LWW chaining etc., the XML calls
// route through the shared implementations.
func asArray(mc *meter.Meter, b *block.Branch) *Array {
	mc.Enter()
	defer mc.Leave()

	return meter.Pointer(mc, Array{branch: b})
}
func asMap(mc *meter.Meter, b *block.Branch) *Map {
	mc.Enter()
	defer mc.Leave()

	return meter.Pointer(mc, Map{branch: b})
}

// insertXmlChild builds a KindType Item whose Content.Branch is
// the supplied inner branch, integrates at position idx of parent.
func insertXmlChild(mc *meter.Meter, txn *doc.TransactionMut, parent *block.Branch, idx uint64, inner *block.Branch) {
	mc.Enter()
	defer mc.Leave()

	asArray(mc, parent).insertNested(mc, txn, idx, inner)
}

// getXmlChildAt returns the child at idx, converting Array's
// returned wrappers into the correct XML wrapper based on the
// inner Branch's TypeRef. extractValueAt already dispatches Map /
// Array / Text wrappers; for XML we re-wrap below.
func getXmlChildAt(mc *meter.Meter, branch *block.Branch, idx uint64) any {
	mc.Enter()
	defer mc.Leave()

	// Walk branch.Start to find the live countable item at idx,
	// then wrap based on its Content.Branch.TypeRef directly.
	// We bypass extractValueAt because the existing dispatch
	// returns *Map / *Array / *Text and we need the XML wrappers.
	var counted uint64
	for cur := branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur.IsDeleted(mc) || !cur.IsCountable(mc) {
			continue
		}
		if counted+cur.Len > idx {
			return wrapXmlContent(mc, cur.Content)
		}
		counted += cur.Len
	}
	return nil
}

// rangeXmlChildren walks branch.Start, emitting one (idx, wrapped)
// pair per live countable item. Stops when fn returns false.
func rangeXmlChildren(mc *meter.Meter, branch *block.Branch, fn func(idx uint64, child any) bool) {
	mc.Enter()
	defer mc.Leave()

	var idx uint64
	for cur := branch.Start; cur != nil; cur = cur.Right {
		mc.Step()

		if cur.IsDeleted(mc) || !cur.IsCountable(mc) {
			continue
		}
		// For multi-element items (rare in XML — InsertXmlElement
		// emits Len=1), expand into per-element iterations.
		for off := uint64(0); off < cur.Len; off++ {
			mc.Step()

			if !fn(idx, wrapXmlContent(mc, cur.Content)) {
				return
			}
			idx++
		}
	}
}

// wrapXmlContent converts a Content into the appropriate XML
// wrapper based on Branch.TypeRef. Falls back to extractValueAt's
// dispatch for non-XML content kinds (e.g. a plain string embedded
// directly).
func wrapXmlContent(mc *meter.Meter, c block.Content) any {
	mc.Enter()
	defer mc.Leave()

	if c.Kind != block.KindType || c.Branch == nil {
		return extractValueAt(mc, c, 0)
	}
	switch c.Branch.TypeRef {
	case block.TypeRefXmlElement:
		return meter.Pointer(mc, XmlElement{branch: c.Branch})
	case block.TypeRefXmlFragment:
		return meter.Pointer(mc, XmlFragment{branch: c.Branch})
	case block.TypeRefXmlText:
		return meter.Pointer(mc, XmlText{Text: Text{branch: c.Branch}})
	}
	return extractValueAt(mc, c, 0)
}

// stringifyXmlChild formats one wrapped XML child for the ToString
// output. Unknown types render as the empty string.
func stringifyXmlChild(mc *meter.Meter, child any) string {
	mc.Enter()
	defer mc.Leave()

	switch c := child.(type) {
	case *XmlElement:
		return c.ToString(mc)
	case *XmlText:
		return c.ToString(mc)
	case *XmlFragment:
		return c.ToString(mc)
	}
	return ""
}
