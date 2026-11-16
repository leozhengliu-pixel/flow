package meter

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"testing"
)

func TestScaleP21JSONRetainedStringWidthMatchesStandardDecoder(t *testing.T) {
	cases := [][]byte{
		[]byte(`""`), []byte(`"ASCII"`), []byte(`"\u0000"`),
		[]byte(`"\u003c\u003e\u0026"`), []byte(`"\u07ff\u0800"`),
		[]byte(`"\ud800"`), []byte(`"\udc00"`), []byte(`"\ud800\udc00"`),
		[]byte(`"\udc00\ud800"`), []byte(`"\ud800\u0041"`),
		[]byte(`"\\\"\/\b\f\n\r\t"`), {'"', 0xff, 0xc0, 0x80, '"'},
	}
	random := rand.New(rand.NewSource(205))
	for i := 0; i < 1000; i++ {
		text := make([]byte, random.Intn(512))
		if _, err := random.Read(text); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(string(text))
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, raw)
	}
	for i, raw := range cases {
		var want string
		if err := json.Unmarshal(raw, &want); err != nil {
			t.Fatal("fixture", i, err)
		}
		width, _ := jsonStringWidth(nil, raw)
		if width != uint64(len(want)) {
			t.Fatalf("scalar retained width differs: case=%d width=%d want=%d raw=%q", i, width, len(want), raw)
		}
	}
}

func TestScaleP21JSONTemporaryScratchDoesNotChangeIterationQuota(t *testing.T) {
	exhausted := errors.New("step quota remains bounded")
	used := uint64(0)
	m, err := NewMeter(context.Background(), func(size uint64) (func(), error) {
		if size > 48<<10-used {
			return nil, exhausted
		}
		used += size
		return func() { used -= size }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		failure, ok := recover().(*Abort)
		var failureErr error
		if ok {
			failureErr = failure.Err
		}
		m.Close()
		if !ok || !errors.Is(failureErr, exhausted) || used != 0 {
			t.Fatal("CRDT Step no longer enforces/release its original quota", failure, used)
		}
	}()
	for i := 0; i < 1<<20; i++ {
		m.Step()
	}
	t.Fatal("CRDT Step accepted unbounded repeated iteration")
}
