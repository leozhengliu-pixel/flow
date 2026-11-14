package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestScaleP21SW31CatalogNumberGrammar(t *testing.T) {
	cases := []string{"", " ", "-", "+", ".", "1.", "1e", "1E", "1e+", "1e-", "--1", "01", "-01", "+1", ".1", "00", "-00", "1.e1", "1e1e1", "1..0", "NaN", "Infinity", "-Infinity", "0x1", "1_0", "1+2", "1e+e", "[1", "{}", `"1"`, "tru", "nul", "]", "1\x00", "1\xff", "1\\", "1 2", "1,2", "1}garbage", "0", "-0", "1", "-1", "12", "0.0", "-0.125", "1e0", "1E+2", "1e-2", "-12.5E-7", " 1", "\n-0.125", "1 \t\r\n", "1e9999999999999999999999999999999999999999999"}
	for _, integer := range []string{"0", "1", "12", "-0", "-12"} {
		for _, fraction := range []string{"", ".0", ".125"} {
			for _, exponent := range []string{"", "e0", "E+12", "e-123"} {
				cases = append(cases, integer+fraction+exponent)
			}
		}
	}
	cases = append(cases, "1"+strings.Repeat("0", 1<<20), "0."+strings.Repeat("0", 1<<20)+"1", "1e"+strings.Repeat("9", 1<<20))
	for index, raw := range cases {
		for _, chunk := range []int{1, 3, 4096, 16384, len(raw) + 1} {
			t.Run(fmt.Sprintf("%d/%d", index, chunk), func(t *testing.T) {
				var number metadataCatalogScalarNumber
				for offset := 0; offset < len(raw); offset += chunk {
					if n, err := number.Write([]byte(raw[offset:min(offset+chunk, len(raw))])); err != nil || n != min(chunk, len(raw)-offset) {
						t.Fatal("number observer changed reader behavior", n, err)
					}
				}
				trimmed := strings.TrimSpace(raw)
				want := json.Valid([]byte(raw)) && len(trimmed) > 0 && (trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9')
				if number.complete() != want {
					t.Fatalf("chunked number grammar differs from standard JSON bytes=%d", len(raw))
				}
			})
		}
	}
}
