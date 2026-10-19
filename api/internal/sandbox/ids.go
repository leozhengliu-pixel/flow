package sandbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// crockford is the lowercase Crockford base32 alphabet used by ULIDs.
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// NewID returns a 26-character lowercase ULID (48-bit millisecond time plus
// 80 random bits). Object names derive from it, so it is DNS-label safe.
func NewID(now time.Time) string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(now.UnixMilli())<<16)
	if _, err := rand.Read(raw[6:]); err != nil {
		panic("sandbox: crypto/rand failed: " + err.Error())
	}
	// 128 bits → 26 base32 characters (the first carries 3 bits).
	out := make([]byte, 26)
	var acc uint64
	hi := binary.BigEndian.Uint64(raw[:8])
	lo := binary.BigEndian.Uint64(raw[8:])
	for i := 25; i >= 0; i-- {
		acc = lo & 31
		out[i] = crockford[acc]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}

// ValidID reports whether id is safe to embed in object names: 1-40
// lowercase letters and digits (ULIDs are 26).
func ValidID(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// WorkspaceLabel is the flow.dev/workspace label value: a short hash, since
// workspace keys are not guaranteed to be valid label values.
func WorkspaceLabel(workspaceKey string) string {
	if workspaceKey == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(workspaceKey))
	return hex.EncodeToString(sum[:8])
}
