package main

import (
	"context"
	"errors"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"
)

func scaleP21SW136Chunks(value []byte, width int) func(func([]byte) error) error {
	return func(visit func([]byte) error) error {
		if err := visit(nil); err != nil {
			return err
		}
		for offset := 0; offset < len(value); {
			end := min(len(value), offset+width)
			if err := visit(value[offset:end]); err != nil {
				return err
			}
			offset = end
		}
		return visit([]byte{})
	}
}

func scaleP21SW136Compare(t *testing.T, name, instructions string, widths ...int) {
	t.Helper()
	wantHash, wantDescription := loopContractHash(name, instructions), fallbackLoopDescription(instructions)
	for _, width := range widths {
		hash, description, err := scaleP21LoopText(t.Context(), name, scaleP21SW136Chunks([]byte(instructions), width))
		if err != nil || hash != wantHash || description != wantDescription {
			t.Fatalf("width=%d name=%q text=%.400q\nhash=%q/%q description=%q/%q err=%v",
				width, name, instructions, wantHash, hash, wantDescription, description, err)
		}
	}
}

func TestScaleP21SW136LoopTextDifferential(t *testing.T) {
	cases := []string{
		"", " \t\r\n\v\f\u0085\u00a0\u2000\u2028\u3000", "plain",
		" \n\t# Heading:\n## Another:\n  - Do work. Then continue.\n ",
		"# One:\n## Two:", "#\n##\n> -- 0123.", "# Title\nnext line",
		"Heading:\n  \u00a0# Keep hash\nlast", "\u00a0# Remove hash\nlast",
		"\n\u3000# Remove hash after blanks", "123. **Bold** plan. Next",
		"foo.\u00a0bar", "foo.\u0085bar", "foo.\u2000bar",
		"foo.\xa0bar", "foo.\x85bar", "foo.\xffbar", "foo.\x00bar",
		"foo.\u3000", "foo.\u3000 \t\n", "foo?X ! stop", "foo!\r\nnext",
		"***", "****", "*****", "a**b***c****d", "** \t **",
		"a" + strings.Repeat(" ", 200) + "**",
		strings.Repeat("**", 200) + "x",
		strings.Repeat("x", 136) + "\xff\xfe\xfdxy",
		strings.Repeat("x", 137) + "\xff\xfe\xfdxy",
		strings.Repeat("x", 139) + "\xff",
		strings.Repeat("x", 140) + "\xff",
		strings.Repeat(" ", 5) + strings.Repeat("x", 140) + strings.Repeat("\u3000", 200),
		strings.Repeat("界", 12) + ":", strings.Repeat("界", 13) + ":",
		strings.Repeat("界", 14) + ":", strings.Repeat("x", 38) + ":",
		strings.Repeat("x", 39) + ":", "\x00\xff\xc0\xaf\xe2\x82",
		"# Heading:\n\t0123. \u3000* Kept asterisk",
	}
	for _, count := range []int{136, 137, 138, 139, 140, 141, 142} {
		cases = append(cases, strings.Repeat("界", count), strings.Repeat("x", count)+"**")
	}
	names := []string{"", " \u3000 Loop \t", "name\x00\xff", strings.Repeat("name", 2048)}
	for index, value := range cases {
		for _, name := range names {
			t.Run(strings.Join([]string{"case", string(rune('A' + index))}, "-"), func(t *testing.T) {
				scaleP21SW136Compare(t, name, value, 1, 2, 3, 4, 7, 64, 1024)
			})
		}
	}
	random := rand.New(rand.NewSource(136))
	alphabet := []byte("aA#-*>\t01239.!?: \n\r**")
	alphabet = append(alphabet, 0, 0x85, 0xa0, 0xc2, 0xe2, 0x80, 0xff, 0xbd, 0xe7, 0x95, 0x8c)
	for range 400 {
		value := make([]byte, random.Intn(513))
		for index := range value {
			value[index] = alphabet[random.Intn(len(alphabet))]
		}
		scaleP21SW136Compare(t, "\u3000random\xff ", string(value), 1, 2, 3, 7, 64)
	}
}

func TestScaleP21SW136LoopTextHugeAndReusedChunks(t *testing.T) {
	for _, value := range []string{
		strings.Repeat("**", 2<<20),
		strings.Repeat(" # Heading:\n", 100000) + "\u3000Last valid line? More text",
		strings.Repeat("# Heading:\n", 100000),
		"  start" + strings.Repeat("\u3000", 1<<20),
	} {
		scaleP21SW136Compare(t, " Large loop ", value, 1021, 16<<10)
	}
	value := " \u3000# Heading:\n**Do work**.\xa0discarded\x00"
	var reused [3]byte
	hash, description, err := scaleP21LoopText(t.Context(), "reused", func(visit func([]byte) error) error {
		for offset := 0; offset < len(value); {
			n := copy(reused[:], value[offset:])
			if err := visit(reused[:n]); err != nil {
				return err
			}
			for index := range reused {
				reused[index] = 0xff
			}
			offset += n
		}
		return nil
	})
	if err != nil || hash != loopContractHash("reused", value) || description != fallbackLoopDescription(value) {
		t.Fatal("reused producer buffers changed text", hash, description, err)
	}
}

type scaleP21SW136CancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *scaleP21SW136CancelContext) Err() error {
	c.checks++
	if c.checks == 8 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScaleP21SW136LoopTextFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if hash, description, err := scaleP21LoopText(ctx, "", func(func([]byte) error) error {
		t.Fatal("canceled helper called its producer")
		return nil
	}); !errors.Is(err, context.Canceled) || hash != "" || description != "" {
		t.Fatal("initial cancellation escaped partial text", err)
	}
	failure := errors.New("instruction read failed")
	hash, description, err := scaleP21LoopText(t.Context(), "", func(visit func([]byte) error) error {
		if err := visit([]byte("partial")); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || hash != "" || description != "" {
		t.Fatal("producer failure escaped partial text", err)
	}
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := &scaleP21SW136CancelContext{Context: base, cancel: cancel}
	large := []byte(strings.Repeat("streamed text ", 100000))
	hash, description, err = scaleP21LoopText(observed, "", func(visit func([]byte) error) error {
		_ = visit(large)
		// Even a producer ignoring the callback failure must not publish output.
		_ = visit([]byte("ignored"))
		return nil
	})
	if !errors.Is(err, context.Canceled) || hash != "" || description != "" {
		t.Fatal("single-chunk cancellation escaped partial text", err)
	}
	if hash, description, err := scaleP21LoopText(t.Context(), "", nil); err == nil || hash != "" || description != "" {
		t.Fatal("nil producer was accepted")
	}
}

func TestScaleP21SW136LoopTextScratchBound(t *testing.T) {
	stateBytes := unsafe.Sizeof(scaleP21LoopTextState{})
	if stateBytes > 4<<10 {
		t.Fatal("streamed loop text state exceeds 4 KiB", stateBytes)
	}
	block := []byte(strings.Repeat("Alpha ", 128))
	producer := func(repeats int) func(func([]byte) error) error {
		return func(visit func([]byte) error) error {
			for range repeats {
				if err := visit(block); err != nil {
					return err
				}
			}
			return nil
		}
	}
	small, large := producer(4), producer(16384)
	measure := func(chunks func(func([]byte) error) error) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		hash, description, err := scaleP21LoopText(t.Context(), "allocation bound", chunks)
		runtime.ReadMemStats(&after)
		if err != nil || len(hash) != 24 || utf8.RuneCountInString(description) > 140 {
			t.Fatal("bounded fixture failed", hash, description, err)
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	shortBytes, largeBytes := measure(small), measure(large)
	if largeBytes > 16<<10 || largeBytes > shortBytes+8<<10 {
		t.Fatal("streamed helper allocated input-sized scratch", shortBytes, largeBytes)
	}
	t.Logf("state=%d bytes; 3KiB input allocations=%d bytes; 12MiB input allocations=%d bytes; finish buffer=%d bytes",
		stateBytes, shortBytes, largeBytes, 137*utf8.UTFMax)
}
