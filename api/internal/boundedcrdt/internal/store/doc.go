// Package store implements the per-client block storage that owns the
// memory for every Item in a Yjs document.
//
// The store is responsible for:
//   - Owning the per-client list of BlockCells (Item or GC) in clock order.
//   - Looking up cells by ID via binary search.
//   - Producing a StateVector summarizing what the local doc knows.
//   - Computing clean-edge ItemSlices for sync / integration paths.
//
// It is not responsible for:
//   - Splicing Items mid-clock-range (that lives on block.Item.Splice; see tech-debt.md).
//   - Integration / YATA conflict resolution (lives on block.Item.Integrate).
//   - Squashing adjacent runs (lives on block.Item.TrySquash).
//   - Building the delete set (separate id_set layer).
//   - Concurrency control (the Doc layer holds a sync.RWMutex around store ops).
//
// See docs/yrs-port-notes/store.md for the per-method contract and the
// 13 invariants every mutation must preserve.
package store
