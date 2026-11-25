package store

// Read windows of materialized nodes.
//
// Materializing a node (a record, a struct, a slice of small values) reads
// it as many small frame fields: 16-byte headers, 8-byte scalars and short
// strings, nested as deep as the value. Each went through the storage's
// shared block cache (logical_spool_io.go), whose four slots a
// nested decode (one cursor per level) or a few concurrent decodes of one
// installed storage evicted in turn: a COLD decode or a whole-workspace
// reload spent most of its time in pread system calls for blocks it had just
// dropped. materialize now reads a node of at most
// metadataLogicalWindowBytes in one read and decodes it, and every node
// nested in it, from that copy.
//
// A window is a copy of bytes the decode would have read anyway, taken when
// the decode starts and dropped when it returns; it is never shared between
// decodes, so a later patch of the storage cannot be served stale from it.
// Its memory is bounded per decode level by metadataLogicalWindowBytes and
// only the outermost windowed node allocates one (nested nodes reuse it).

const (
	metadataLogicalWindowBytes = 256 << 10
	// Below this a node is cheaper to read field by field from the cache.
	metadataLogicalWindowMin = 64
)

// A window serves only reads of the storage it was read from: a node moved
// to another storage (a copied frame, a memo origin) keeps the field but
// reads that storage.
type metadataLogicalWindow struct {
	storage *metadataLogicalStorage
	at      int64
	data    []byte
	// slid marks a cursor's read-ahead chunk (slide); lent marks that a child
	// node holds it, so the cursor never refills it. A chunk only the cursor
	// read from is refilled by its next slide: a header scan of a large
	// collection whose children take no window (a resource read) allocates
	// one chunk, not one per 64 KiB. Only the cursor's goroutine sets either.
	slid, lent bool
}

// windowed returns n with a window over its bytes when it has none and its
// size suits one. On a read error n is returned unchanged: the decode then
// reads the storage and reports the error as before.
func (n metadataLogicalNode) windowed() metadataLogicalNode {
	if n.window != nil && n.window.storage == n.source || n.source == nil || n.size < metadataLogicalWindowMin || n.size > metadataLogicalWindowBytes {
		return n
	}
	data := make([]byte, n.size)
	if count, err := n.source.ReadAt(data, n.offset); err != nil || count != len(data) {
		return n
	}
	n.window = &metadataLogicalWindow{storage: n.source, at: n.offset, data: data}
	return n
}

// read copies p from the window at offset of storage when the window holds
// those bytes.
func (w *metadataLogicalWindow) read(p []byte, storage *metadataLogicalStorage, offset int64) bool {
	if w == nil || w.storage != storage || offset < w.at || offset+int64(len(p)) > w.at+int64(len(w.data)) {
		return false
	}
	copy(p, w.data[offset-w.at:])
	return true
}
