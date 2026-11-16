package lib0

import "flow/api/internal/boundedcrdt/internal/meter"

import (
	"encoding/binary"
	"math"
)

// WriteVarUint appends the LEB128-style varuint encoding of n to buf and returns
// the extended slice. Bytes use 7 bits of value with the MSB as the continuation
// flag. This matches lib0's writeVarUint.
func WriteVarUint(mc *meter.Meter, buf []byte, n uint64) []byte {
	mc.Enter()
	defer mc.Leave()

	for n > 0x7f {
		mc.Step()

		buf = meter.Append(mc, buf, byte(n&0x7f)|0x80)
		n >>= 7
	}
	return meter.Append(mc, buf, byte(n&0x7f))
}

// WriteVarInt appends the zigzag-then-LEB128 encoding of n to buf and returns
// the extended slice. The sign is folded into the LSB of the first byte: bit 6
// holds the sign, bits 0-5 hold the lowest 6 bits of |n|, bit 7 is continuation.
// This matches lib0's writeVarInt.
func WriteVarInt(mc *meter.Meter, buf []byte, n int64) []byte {
	mc.Enter()
	defer mc.Leave()

	var sign byte
	if n < 0 {
		sign = 0x40
		// Flip to positive magnitude. math.MinInt64 case: ^uint64(0)>>1 + 1 = 1<<63.
		if n == math.MinInt64 {
			return writeVarIntMagnitude(mc, buf, sign, 1<<63)
		}
		n = -n
	}
	return writeVarIntMagnitude(mc, buf, sign, uint64(n))
}

func writeVarIntMagnitude(mc *meter.Meter, buf []byte, sign byte, mag uint64) []byte {
	mc.Enter()
	defer mc.Leave()

	// First byte: 6 bits of magnitude + sign bit + continuation if more.
	first := byte(mag&0x3f) | sign
	mag >>= 6
	if mag == 0 {
		return meter.Append(mc, buf, first)
	}
	buf = meter.Append(mc, buf, first|0x80)
	for mag > 0x7f {
		mc.Step()

		buf = meter.Append(mc, buf, byte(mag&0x7f)|0x80)
		mag >>= 7
	}
	return meter.Append(mc, buf, byte(mag&0x7f))
}

// WriteVarString appends the lib0 varstring encoding of s to buf: a varuint
// length prefix in bytes (UTF-8) followed by the UTF-8 bytes themselves.
func WriteVarString(mc *meter.Meter, buf []byte, s string) []byte {
	mc.Enter()
	defer mc.Leave()

	buf = WriteVarUint(mc, buf, uint64(len(s)))
	return meter.AppendString(mc, buf, s)
}

// WriteVarUint8Array appends the lib0 varbuffer encoding of b to buf: a varuint
// length prefix followed by the raw bytes.
func WriteVarUint8Array(mc *meter.Meter, buf, b []byte) []byte {
	mc.Enter()
	defer mc.Leave()

	buf = WriteVarUint(mc, buf, uint64(len(b)))
	return meter.Append(mc, buf, b...)
}

// WriteUint8 appends a single byte.
func WriteUint8(mc *meter.Meter, buf []byte, n uint8) []byte {
	mc.Enter()
	defer mc.Leave()

	return meter.Append(mc, buf, n)
}

// WriteUint16 appends a little-endian 16-bit unsigned integer.
func WriteUint16(mc *meter.Meter, buf []byte, n uint16) []byte {
	mc.Enter()
	defer mc.Leave()

	return binary.LittleEndian.AppendUint16(buf, n)
}

// WriteUint32 appends a little-endian 32-bit unsigned integer.
func WriteUint32(mc *meter.Meter, buf []byte, n uint32) []byte {
	mc.Enter()
	defer mc.Leave()

	return binary.LittleEndian.AppendUint32(buf, n)
}

// WriteFloat32 appends a big-endian IEEE-754 32-bit float. lib0 uses big-endian
// for floats to match the DataView default in JS.
func WriteFloat32(mc *meter.Meter, buf []byte, f float32) []byte {
	mc.Enter()
	defer mc.Leave()

	return binary.BigEndian.AppendUint32(buf, math.Float32bits(f))
}

// WriteFloat64 appends a big-endian IEEE-754 64-bit float.
func WriteFloat64(mc *meter.Meter, buf []byte, f float64) []byte {
	mc.Enter()
	defer mc.Leave()

	return binary.BigEndian.AppendUint64(buf, math.Float64bits(f))
}
