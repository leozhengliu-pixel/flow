package store

import "sync/atomic"

// metadataLogicalMemoRanges indexes the frames of one memo map (child
// indexes, clone checks or user indexes) by [offset, offset+size): an
// interval treap keyed by offset whose nodes keep the greatest end in their
// subtree. A write then finds the memo entries it overlaps in O(log n + k)
// instead of walking the whole map, which made rebuilding a large logical
// source quadratic (every header patch scanned every memo entry).
//
// Every insert into and delete from a memo map goes through set and drop
// under memoMu, so count equals the map's length; dropFrameMemo rebuilds a
// tree that disagrees (and counts it in metadataLogicalMemoRebuilds, which
// tests require to stay zero) rather than miss an overlapping entry.
//
// Priorities are a hash of the offset, so the expected depth is
// O(log n) whatever order frames are written in. A node is
// metadataLogicalMemoRangeBytes: child indexes charge it to
// metadataLogicalIndexBytes with their headers; clone checks and user
// indexes are not budgeted, and the node is smaller than their map entry.
type metadataLogicalMemoRanges struct {
	root  *metadataLogicalMemoRange
	count int
}

type metadataLogicalMemoRange struct {
	key, end, maxEnd int64
	priority         uint64
	left, right      *metadataLogicalMemoRange
}

const metadataLogicalMemoRangeBytes = 48

var metadataLogicalMemoRebuilds atomic.Int64

func metadataLogicalMemoPriority(key int64) uint64 {
	x := uint64(key) + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (n *metadataLogicalMemoRange) refresh() {
	n.maxEnd = n.end
	if n.left != nil && n.left.maxEnd > n.maxEnd {
		n.maxEnd = n.left.maxEnd
	}
	if n.right != nil && n.right.maxEnd > n.maxEnd {
		n.maxEnd = n.right.maxEnd
	}
}

// metadataLogicalMemoSplit splits root into the keys below key and the rest.
func metadataLogicalMemoSplit(root *metadataLogicalMemoRange, key int64) (left, right *metadataLogicalMemoRange) {
	if root == nil {
		return nil, nil
	}
	if root.key < key {
		root.right, right = metadataLogicalMemoSplit(root.right, key)
		root.refresh()
		return root, right
	}
	left, root.left = metadataLogicalMemoSplit(root.left, key)
	root.refresh()
	return left, root
}

// metadataLogicalMemoMerge joins two trees whose keys are all ordered left
// before right.
func metadataLogicalMemoMerge(left, right *metadataLogicalMemoRange) *metadataLogicalMemoRange {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority > right.priority {
		left.right = metadataLogicalMemoMerge(left.right, right)
		left.refresh()
		return left
	}
	right.left = metadataLogicalMemoMerge(left, right.left)
	right.refresh()
	return right
}

// metadataLogicalMemoInsert inserts node, whose key root does not hold.
func metadataLogicalMemoInsert(root, node *metadataLogicalMemoRange) *metadataLogicalMemoRange {
	if root == nil {
		return node
	}
	if node.priority > root.priority {
		node.left, node.right = metadataLogicalMemoSplit(root, node.key)
		node.refresh()
		return node
	}
	if node.key < root.key {
		root.left = metadataLogicalMemoInsert(root.left, node)
	} else {
		root.right = metadataLogicalMemoInsert(root.right, node)
	}
	root.refresh()
	return root
}

// metadataLogicalMemoRemove removes key from root, if present.
func metadataLogicalMemoRemove(root *metadataLogicalMemoRange, key int64) (*metadataLogicalMemoRange, bool) {
	if root == nil {
		return nil, false
	}
	if key == root.key {
		return metadataLogicalMemoMerge(root.left, root.right), true
	}
	var removed bool
	if key < root.key {
		root.left, removed = metadataLogicalMemoRemove(root.left, key)
	} else {
		root.right, removed = metadataLogicalMemoRemove(root.right, key)
	}
	if removed {
		root.refresh()
	}
	return root, removed
}

// set records the frame [key, key+size), replacing an earlier frame at key.
func (r *metadataLogicalMemoRanges) set(key, size int64) {
	root, replaced := metadataLogicalMemoRemove(r.root, key)
	end := key + size
	r.root = metadataLogicalMemoInsert(root, &metadataLogicalMemoRange{
		key: key, end: end, maxEnd: end, priority: metadataLogicalMemoPriority(key),
	})
	if !replaced {
		r.count++
	}
}

// drop removes every frame overlapping [offset, end) and appends its key to
// keys.
func (r *metadataLogicalMemoRanges) drop(offset, end int64, keys []int64) []int64 {
	before := len(keys)
	r.root, keys = metadataLogicalMemoDrop(r.root, offset, end, keys)
	r.count -= len(keys) - before
	return keys
}

func metadataLogicalMemoDrop(node *metadataLogicalMemoRange, offset, end int64, keys []int64) (*metadataLogicalMemoRange, []int64) {
	// No frame below ends after offset; none right of a key at or past end
	// starts before end.
	if node == nil || node.maxEnd <= offset {
		return node, keys
	}
	before := len(keys)
	node.left, keys = metadataLogicalMemoDrop(node.left, offset, end, keys)
	if node.key >= end {
		if len(keys) != before {
			node.refresh()
		}
		return node, keys
	}
	node.right, keys = metadataLogicalMemoDrop(node.right, offset, end, keys)
	if offset < node.end {
		return metadataLogicalMemoMerge(node.left, node.right), append(keys, node.key)
	}
	if len(keys) != before {
		node.refresh()
	}
	return node, keys
}

func (r *metadataLogicalMemoRanges) reset() {
	r.root = nil
	r.count = 0
}
