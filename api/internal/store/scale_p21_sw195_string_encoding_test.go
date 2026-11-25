package store

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func scaleP21SW195StringWidth(t *testing.T, value string) uint64 {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	want := uint64(len(raw)) * 4
	got, err := metadataAggregateStringEncodingWork(t.Context(), value)
	if err != nil || got != want {
		t.Fatalf("string encoding width: bytes=%d got=%d want=%d err=%v", len(value), got, want, err)
	}
	work, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(value), 0)
	if err != nil || uint64(work) != want+128 {
		t.Fatalf("payload lost scalar overhead or string buffers: got=%d want=%d err=%v", work, want+128, err)
	}
	return got
}

func TestScaleP21SW195StringEncodingDifferential(t *testing.T) {
	for char := range 256 {
		scaleP21SW195StringWidth(t, string([]byte{byte(char)}))
	}
	var pairs []byte
	for first := range 256 {
		for second := range 256 {
			pairs = append(pairs, byte(first), byte(second))
		}
	}
	scaleP21SW195StringWidth(t, string(pairs))
	for _, value := range []string{
		"", "ordinary ASCII", `backslash \ and quote "`, "<script>&</script>",
		"\x00\b\f\n\r\t\x1f\x7f", "\u2028\u2029", "\ufffd",
		"\u00a2\u754c\U0001f30d\U0010ffff",
		string([]byte{0xc0, 0xaf, 0xed, 0xa0, 0x80, 0xf0, 0x9f, 0x92, 0xff}),
	} {
		scaleP21SW195StringWidth(t, value)
	}
	var valid strings.Builder
	valid.Grow(4 << 20)
	for char := rune(0); char <= utf8.MaxRune; char++ {
		if char >= 0xd800 && char <= 0xdfff {
			continue
		}
		valid.WriteRune(char)
	}
	scaleP21SW195StringWidth(t, valid.String())
	rng := rand.New(rand.NewSource(195))
	for range 4096 {
		value := make([]byte, rng.Intn(257))
		if _, err := rng.Read(value); err != nil {
			t.Fatal(err)
		}
		scaleP21SW195StringWidth(t, string(value))
	}
}

func TestScaleP21SW195StringEncodingLarge(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{"ASCII-4MiB", strings.Repeat("x", 4<<20)},
		{"HTML-control-2MiB", strings.Repeat("<>&\x00", 1<<19)},
		{"invalid-UTF8-1MiB", strings.Repeat(string([]byte{0xff}), 1<<20)},
		{"UTF8-separators", strings.Repeat("\u2028\u2029", 1<<17)},
	} {
		t.Run(test.name, func(t *testing.T) {
			work := scaleP21SW195StringWidth(t, test.value)
			t.Logf("input_bytes=%d previous_string_allowance=%d exact_string_allowance=%d", len(test.value), len(test.value)*24, work)
		})
	}
}

type scaleP21SW195CheckedContext struct {
	context.Context
	checks, cancelAt int
	cancel           context.CancelFunc
}

func (c *scaleP21SW195CheckedContext) Err() error {
	c.checks++
	if c.cancelAt > 0 && c.checks == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScaleP21SW195StringEncodingCancellation(t *testing.T) {
	for _, value := range []string{"", "ASCII", strings.Repeat("\u754c", 1<<15)} {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if work, err := metadataAggregateStringEncodingWork(ctx, value); work != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("pre-cancelled string: work=%d err=%v", work, err)
		}
	}
	for _, value := range []string{
		strings.Repeat("a", 1<<17),
		strings.Repeat("\u754c", 1<<15),
		"\u00a2" + strings.Repeat("\u754c", 1<<15),
	} {
		checked := &scaleP21SW195CheckedContext{Context: t.Context()}
		if _, err := metadataAggregateStringEncodingWork(checked, value); err != nil {
			t.Fatal(err)
		}
		if minimum := len(value) / (4096 + utf8.UTFMax - 1); checked.checks < minimum {
			t.Fatalf("UTF8 stepping skipped cancellation checks: bytes=%d checks=%d minimum=%d", len(value), checked.checks, minimum)
		}
		ctx, cancel := context.WithCancel(t.Context())
		checked = &scaleP21SW195CheckedContext{Context: ctx, cancelAt: 2, cancel: cancel}
		work, err := metadataAggregateStringEncodingWork(checked, value)
		cancel()
		if work != 0 || !errors.Is(err, context.Canceled) || checked.checks != 2 {
			t.Fatalf("periodic cancellation: work=%d err=%v checks=%d", work, err, checked.checks)
		}
	}
}

func TestScaleP21SW195StringEncodingBudgetBoundary(t *testing.T) {
	encodedLimit := metadataWorkBytes / 4
	repeated := strings.Repeat("<", (encodedLimit-2)/6)
	atLimit := repeated + strings.Repeat("a", encodedLimit-2-len(repeated)*6)
	work, err := metadataAggregateStringEncodingWork(t.Context(), atLimit)
	if err != nil || work != metadataWorkBytes {
		t.Fatalf("exact limit: work=%d limit=%d err=%v", work, metadataWorkBytes, err)
	}
	if work, err := metadataAggregateStringEncodingWork(t.Context(), atLimit+"x"); work != 0 || !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("one encoded byte over budget: work=%d err=%v", work, err)
	}
	if _, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(atLimit), 0); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("standalone width lost enclosing payload overhead: %v", err)
	}
}
