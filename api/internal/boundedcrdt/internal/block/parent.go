package block

import

// ParentKind discriminates the four states a parent reference can be in.
// Mirrors yrs/src/types/mod.rs TypePtr.
"flow/api/internal/boundedcrdt/internal/meter"

type ParentKind uint8

const (
	// ParentUnknown is the pre-resolution sentinel. Items decoded
	// from updates start here; integrate() resolves to one of the
	// concrete variants. Items in this state are not yet stored.
	ParentUnknown ParentKind = iota
	// ParentBranch points directly at a live owning collection.
	// Stored items are always in this state.
	ParentBranch
	// ParentNamed identifies a root type by name; resolves to
	// ParentBranch on integrate via the Doc's root-type registry.
	ParentNamed
	// ParentID identifies a nested type by the ID of the Item whose
	// content owns it; resolves to ParentBranch on integrate via the
	// store.
	ParentID
)

// Parent is a tagged union holding either a resolved Branch reference
// or one of the unresolved forms used during update decoding.
//
// See docs/yrs-port-notes/block.md "BranchPtr / parent reference".
type Parent struct {
	Kind   ParentKind
	Branch *Branch // ParentBranch
	Named  string  // ParentNamed
	ID     ID      // ParentID
}

// IsResolved reports whether this parent reference points at a live
// Branch and is therefore safe to use as the owning collection.
func (p Parent) IsResolved(mc *meter.Meter) bool {
	mc.Enter()
	defer mc.Leave()

	return p.Kind == ParentBranch && p.Branch != nil
}
