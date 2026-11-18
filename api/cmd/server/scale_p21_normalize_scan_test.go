package main

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// The differential normalizer runs over every outcome of every leg, and the
// two regular expressions it applies dominated whole matrix tests. These
// scanners replace exactly the matches of scaleP21TriageStamp
// (\d{19}_[0-9a-z]{10}) and scaleP21TriageClock
// (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})), leftmost
// first and non-overlapping, as ReplaceAllString/ReplaceAllStringFunc do.

func scaleP21ScanDigits(text string, at, count int) bool {
	if at+count > len(text) {
		return false
	}
	for _, b := range []byte(text[at : at+count]) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

// scaleP21StampAt returns the end of a stamp match starting at at, or -1.
func scaleP21StampAt(text string, at int) int {
	if !scaleP21ScanDigits(text, at, 19) || at+30 > len(text) || text[at+19] != '_' {
		return -1
	}
	for _, b := range []byte(text[at+20 : at+30]) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'z') {
			return -1
		}
	}
	return at + 30
}

// scaleP21ClockAt returns the end of a clock match starting at at, or -1.
func scaleP21ClockAt(text string, at int) int {
	for _, part := range []struct {
		offset, digits int
		separator      byte
	}{{0, 4, '-'}, {5, 2, '-'}, {8, 2, 'T'}, {11, 2, ':'}, {14, 2, ':'}, {17, 2, 0}} {
		if !scaleP21ScanDigits(text, at+part.offset, part.digits) {
			return -1
		}
		if part.separator != 0 && (at+part.offset+part.digits >= len(text) || text[at+part.offset+part.digits] != part.separator) {
			return -1
		}
	}
	end := at + 19
	if end+1 < len(text) && text[end] == '.' && text[end+1] >= '0' && text[end+1] <= '9' {
		end += 2
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
	}
	if end < len(text) && text[end] == 'Z' {
		return end + 1
	}
	if end < len(text) && (text[end] == '+' || text[end] == '-') && scaleP21ScanDigits(text, end+1, 2) &&
		end+3 < len(text) && text[end+3] == ':' && scaleP21ScanDigits(text, end+4, 2) {
		return end + 6
	}
	return -1
}

func scaleP21ReplaceScan(text string, match func(string, int) int, replace func(string) string) string {
	var out strings.Builder
	last := 0
	for at := 0; at < len(text); {
		if text[at] < '0' || text[at] > '9' {
			at++
			continue
		}
		end := match(text, at)
		if end < 0 {
			at++
			continue
		}
		if out.Len() == 0 {
			out.Grow(len(text))
		}
		out.WriteString(text[last:at])
		out.WriteString(replace(text[at:end]))
		last, at = end, end
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

func scaleP21NormalizeStampsAndClocks(text string, started time.Time) string {
	text = scaleP21ReplaceScan(text, scaleP21StampAt, func(string) string { return "<stamp>" })
	return scaleP21ReplaceScan(text, scaleP21ClockAt, func(clock string) string {
		if at, err := time.Parse(time.RFC3339Nano, clock); err == nil && !at.Before(started) {
			return "<now>"
		}
		return clock
	})
}

func TestScaleP21NormalizeScanMatchesRegexp(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	regexp := func(text string) string {
		text = scaleP21TriageStamp.ReplaceAllString(text, "<stamp>")
		return scaleP21TriageClock.ReplaceAllStringFunc(text, func(clock string) string {
			if at, err := time.Parse(time.RFC3339Nano, clock); err == nil && !at.Before(started) {
				return "<now>"
			}
			return clock
		})
	}
	pieces := []string{
		"1794978293476611000_2r00naz7r4", "17949782934766110001_2r00naz7r4", "1794978293476611000_2R00naz7r4",
		"1794978293476611000_2r00naz7r", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05.123456789Z",
		"2027-11-17T21:18:29.5+08:00", "2025-11-17T21:18:29-07:00", "2026-01-02T03:04:05.Z", "2026-01-02T03:04:05.1",
		"2026-01-02T03:04:05+0800", "12026-01-02T03:04:05Z", "2026-1-02T03:04:05Z", "2026-01-02 03:04:05Z",
		"0000-00-00T00:00:00Z", "9999-99-99T99:99:99.9-99:99", "\"", ",", "é", "\xff", "_", "T", ":", ".", "-", "+", "Z",
		"0", "1", "9", "a", "z", " ", "{\"updatedAt\":", "\\u0000",
	}
	r := rand.New(rand.NewSource(198))
	for range 20000 {
		var text strings.Builder
		for range r.Intn(12) {
			text.WriteString(pieces[r.Intn(len(pieces))])
		}
		if got, want := scaleP21NormalizeStampsAndClocks(text.String(), started), regexp(text.String()); got != want {
			t.Fatalf("normalizer scan differs for %q:\n got %q\nwant %q", text.String(), got, want)
		}
	}
}
