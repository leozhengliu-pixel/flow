// Package idgen generates record ids that stay distinct when many goroutines and
// many API servers sharing one database create records at the same instant.
//
// An id is "<prefix><stamp>" and a stamp is "<19 digits>_<10 characters>":
//
//   - The digits are Unix nanoseconds, zero-padded so that ids of one prefix
//     sort by creation time as plain strings, older "<prefix><nanoseconds>"
//     ids included. Within a process they strictly increase: two calls in
//     the same clock tick (macOS reads the wall clock in microseconds, many
//     virtual machines more coarsely) get consecutive values instead of the
//     same one, so the value can run slightly ahead of the clock under load.
//   - The characters are 50 random bits in lowercase Crockford base32. Other
//     processes read the same clock, so they are what keeps two servers'
//     ids apart when their digits match.
//
// Stamps use only digits, lowercase letters and one underscore: safe in URLs,
// object keys and composite ids, and at most prefix+30 bytes.
package idgen

import (
	"crypto/rand"
	"sync/atomic"
	"time"
)

// StampLength is the length of a stamp; an id is its prefix plus this.
const StampLength = 19 + 1 + randomLength

const (
	randomLength = 10
	crockford    = "0123456789abcdefghjkmnpqrstvwxyz"
)

// Generator produces stamps from one clock. Each server process uses the
// package default; tests make several to stand in for several servers.
type Generator struct {
	clock func() time.Time
	last  atomic.Int64
	// stateless: every stamp acts as if from a different process (tests).
	stateless bool
}

// NewGenerator returns a generator reading clock (time.Now when nil).
func NewGenerator(clock func() time.Time) *Generator {
	if clock == nil {
		clock = time.Now
	}
	return &Generator{clock: clock}
}

var defaultGenerator atomic.Pointer[Generator]

func init() { defaultGenerator.Store(NewGenerator(nil)) }

// New returns prefix followed by a fresh stamp, e.g. New("evt_") returns
// "evt_1793112233445566778_k3j9x0abcd".
func New(prefix string) string { return defaultGenerator.Load().New(prefix) }

// Stamp returns a fresh stamp, for ids that embed it between other parts.
func Stamp() string { return defaultGenerator.Load().Stamp() }

// SimulateServersForTest makes every New and Stamp call act as if a different
// server made it in the same clock tick at: all stamps get the same digits
// and only their random part tells them apart, the worst case of servers on
// a coarse clock. It returns a function restoring the real generator. Tests
// only.
func SimulateServersForTest(at time.Time) (restore func()) {
	simulated := NewGenerator(func() time.Time { return at })
	simulated.stateless = true
	previous := defaultGenerator.Swap(simulated)
	return func() { defaultGenerator.Store(previous) }
}

// New returns prefix followed by a fresh stamp.
func (g *Generator) New(prefix string) string {
	out := make([]byte, 0, len(prefix)+StampLength)
	out = append(out, prefix...)
	return string(g.appendStamp(out))
}

// Stamp returns a fresh stamp.
func (g *Generator) Stamp() string {
	return string(g.appendStamp(make([]byte, 0, StampLength)))
}

func (g *Generator) appendStamp(out []byte) []byte {
	nanos := g.next()
	var digits [19]byte
	for index := len(digits) - 1; index >= 0; index-- {
		digits[index] = byte('0' + nanos%10)
		nanos /= 10
	}
	out = append(out, digits[:]...)
	out = append(out, '_')
	var random [8]byte
	// crypto/rand.Read never fails (Go 1.24+): it crashes the program instead.
	_, _ = rand.Read(random[:])
	bits := uint64(random[0]) | uint64(random[1])<<8 | uint64(random[2])<<16 | uint64(random[3])<<24 | uint64(random[4])<<32 | uint64(random[5])<<40 | uint64(random[6])<<48 | uint64(random[7])<<56
	for range randomLength {
		out = append(out, crockford[bits&31])
		bits >>= 5
	}
	return out
}

// next returns the clock in Unix nanoseconds, or one more than the last value
// handed out when the clock has not moved past it. Values before 1970 (a
// broken or test clock) count as 0, keeping the 19 digits.
func (g *Generator) next() uint64 {
	if g.stateless {
		return uint64(max(g.clock().UnixNano(), 0))
	}
	for {
		last := g.last.Load()
		now := max(g.clock().UnixNano(), 0)
		if now <= last {
			now = last + 1
		}
		if g.last.CompareAndSwap(last, now) {
			return uint64(now)
		}
	}
}
