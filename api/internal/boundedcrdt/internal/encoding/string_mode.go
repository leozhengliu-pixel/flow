package encoding

import "flow/api/internal/boundedcrdt/internal/meter"

import "flow/api/internal/boundedcrdt/internal/lib0"

// stringMode says how a decoder reads wire strings: strictly, as lib0 does,
// for bytes that arrive from outside, or as stored, for bytes ygo itself
// stored. Releases before v1.26 accepted and stored strings with invalid
// UTF-8; reading those as stored keeps such documents loading as they did,
// with nothing in them changed.
type stringMode bool

const (
	strictStrings stringMode = false
	storedStrings stringMode = true
)

func (m stringMode) readString(mc *meter.Meter, buf []byte) (string, int, error) {
	mc.Enter()
	defer mc.Leave()

	if m == storedStrings {
		return lib0.ReadVarStringAsStored(mc, buf)
	}
	return lib0.ReadVarString(mc, buf)
}
