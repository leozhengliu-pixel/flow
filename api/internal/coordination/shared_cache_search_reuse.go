package coordination

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"
)

const sharedSearchReuseWindow = 15 * time.Second

type sharedSearchObservation struct {
	key            uint64
	previous, last time.Time
}

type sharedSearchReuse struct {
	mu      sync.Mutex
	entries [1024]sharedSearchObservation
}

var sharedSearchReuseSeed = maphash.MakeSeed()
var sharedSearchBypasses atomic.Int64

// Observe a workspace, principal, credential and normalized search term set.
// This only decides whether to pay for the SQL stamp and cache lookup. The
// versioned Redis key still includes the full query and its visibility inputs.
func (r *sharedSearchReuse) observe(now time.Time, workspace, user, credential string, terms []string) bool {
	var hash maphash.Hash
	hash.SetSeed(sharedSearchReuseSeed)
	writeSearchReusePart(&hash, workspace)
	writeSearchReusePart(&hash, user)
	writeSearchReusePart(&hash, credential)
	for _, term := range terms {
		writeSearchReusePart(&hash, term)
	}
	key := hash.Sum64()
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := &r.entries[key%uint64(len(r.entries))]
	if entry.key != key {
		*entry = sharedSearchObservation{key: key}
	}
	admitted := !entry.previous.IsZero() && now.Sub(entry.previous) <= sharedSearchReuseWindow
	entry.previous, entry.last = entry.last, now
	return admitted
}

func writeSearchReusePart(hash *maphash.Hash, part string) {
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(part)))
	_, _ = hash.Write(size[:])
	_, _ = hash.WriteString(part)
}

// SharedCacheSearchReusable skips Redis for searches without recent reuse on
// this replica. Direct-mapped collisions only discard admission observations.
func (r *Redis) SharedCacheSearchReusable(workspace, user, credential string, terms []string) bool {
	if !r.SharedCacheEnabled() {
		return false
	}
	if !r.shared.searchReuse.observe(time.Now(), workspace, user, credential, terms) {
		sharedSearchBypasses.Add(1)
		return false
	}
	return true
}
