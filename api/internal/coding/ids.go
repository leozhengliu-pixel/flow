package coding

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// crockford is the lowercase Crockford base32 alphabet used by ULIDs.
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// NewID returns a 26-character lowercase ULID: 48 bits of milliseconds then
// 80 random bits. Ids sort by creation time and are valid Kubernetes object
// name suffixes (design §5: deterministic object names derive from them).
func NewID() string { return newIDAt(time.Now()) }

func newIDAt(at time.Time) string {
	var raw [16]byte
	ms := uint64(at.UnixMilli())
	raw[0], raw[1], raw[2], raw[3], raw[4], raw[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	if _, err := rand.Read(raw[6:]); err != nil {
		panic("coding: crypto/rand failed: " + err.Error())
	}
	hi, lo := binary.BigEndian.Uint64(raw[:8]), binary.BigEndian.Uint64(raw[8:])
	out := make([]byte, 26)
	// 128 bits → 26 base32 digits (the first digit carries 3 bits).
	for index := 25; index >= 0; index-- {
		out[index] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(out)
}
