package codingenv

import (
	"crypto/rand"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// EnvValueMaxBytes bounds one environment variable value.
const EnvValueMaxBytes = 64 << 10

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName reports whether name is a portable environment variable name.
func ValidEnvName(name string) bool { return len(name) <= 255 && envNamePattern.MatchString(name) }

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// NewResourceID returns a 26-character lowercase ULID (48-bit millisecond
// time, 80 random bits), the id format of coding rows and sandbox objects.
func NewResourceID(now time.Time) string {
	var raw [16]byte
	ms := uint64(now.UnixMilli())
	for index := 5; index >= 0; index-- {
		raw[index] = byte(ms)
		ms >>= 8
	}
	if _, err := rand.Read(raw[6:]); err != nil {
		panic(err)
	}
	var out strings.Builder
	out.Grow(26)
	// 128 bits → 26 base32 digits, most significant first (the first digit holds 3 bits).
	out.WriteByte(crockford[raw[0]>>5])
	buffer, bits := uint64(raw[0]&0x1f), uint(5)
	for index := 1; index < 16; index++ {
		buffer = buffer<<8 | uint64(raw[index])
		bits += 8
		for bits >= 5 {
			bits -= 5
			out.WriteByte(crockford[(buffer>>bits)&0x1f])
		}
	}
	return out.String()
}

// SnapshotResult is the runner's POST …/snapshots/{id}/complete body
// (field-for-field coding.SnapshotCompleteRequest, convertible to it).
type SnapshotResult struct {
	Status    string                 `json:"status"`
	BaseSHA   string                 `json:"baseSha,omitempty"`
	SizeBytes int64                  `json:"sizeBytes,omitempty"`
	ErrorCode domain.CodingErrorCode `json:"errorCode,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// SnapshotJob is a snapshot row as the snapshot reconciler sees it: the
// public snapshot plus the internal build state kept in the row's data.
type SnapshotJob struct {
	WorkspaceKey    string
	Snapshot        domain.CodingEnvironmentSnapshot
	Attempts        int // build sandboxes launched so far
	StartFailures   int // consecutive transient launch/plan failures
	FinishFailures  int // consecutive transient Finish failures
	CancelRequested bool
	// Plan is the strategy's sandbox.BuildPlan (JSON; opaque to the store).
	Plan json.RawMessage
	// Result is what the runner reported for the current build sandbox.
	Result *SnapshotResult
	// ResultSeq is the stored result's sequence when the job was read, so a
	// result that arrives during a step is not lost.
	ResultSeq int64
	// SucceededAt is when the sandbox was first seen exited without a result.
	SucceededAt *time.Time
}

// StaleEnvironment is an environment whose newest snapshot is ready but old.
type StaleEnvironment struct {
	Environment domain.CodingEnvironment
	Latest      domain.CodingEnvironmentSnapshot
}
