package main

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
	"unsafe"
)

func loopTextChunks(value []byte, width int) func(func([]byte) error) error {
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

func loopTextCompare(t *testing.T, name, instructions string, widths ...int) {
	t.Helper()
	wantHash, wantDescription := loopContractHash(name, instructions), fallbackLoopDescription(instructions)
	for _, width := range widths {
		hash, description, err := scaleLoopText(t.Context(), name, loopTextChunks([]byte(instructions), width))
		if err != nil || hash != wantHash || description != wantDescription {
			t.Fatalf("width=%d name=%q text=%.400q\nhash=%q/%q description=%q/%q err=%v",
				width, name, instructions, wantHash, hash, wantDescription, description, err)
		}
	}
}

func TestLoopTextDifferential(t *testing.T) {
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
				loopTextCompare(t, name, value, 1, 2, 3, 4, 7, 64, 1024)
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
		loopTextCompare(t, "\u3000random\xff ", string(value), 1, 2, 3, 7, 64)
	}
}

func TestLoopTextHugeAndReusedChunks(t *testing.T) {
	for _, value := range []string{
		strings.Repeat("**", 2<<20),
		strings.Repeat(" # Heading:\n", 100000) + "\u3000Last valid line? More text",
		strings.Repeat("# Heading:\n", 100000),
		"  start" + strings.Repeat("\u3000", 1<<20),
	} {
		loopTextCompare(t, " Large loop ", value, 1021, 16<<10)
	}
	value := " \u3000# Heading:\n**Do work**.\xa0discarded\x00"
	var reused [3]byte
	hash, description, err := scaleLoopText(t.Context(), "reused", func(visit func([]byte) error) error {
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

type loopTextCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *loopTextCancelContext) Err() error {
	c.checks++
	if c.checks == 8 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestLoopTextFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if hash, description, err := scaleLoopText(ctx, "", func(func([]byte) error) error {
		t.Fatal("canceled helper called its producer")
		return nil
	}); !errors.Is(err, context.Canceled) || hash != "" || description != "" {
		t.Fatal("initial cancellation escaped partial text", err)
	}
	failure := errors.New("instruction read failed")
	hash, description, err := scaleLoopText(t.Context(), "", func(visit func([]byte) error) error {
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
	observed := &loopTextCancelContext{Context: base, cancel: cancel}
	large := []byte(strings.Repeat("streamed text ", 100000))
	hash, description, err = scaleLoopText(observed, "", func(visit func([]byte) error) error {
		_ = visit(large)
		// Even a producer ignoring the callback failure must not publish output.
		_ = visit([]byte("ignored"))
		return nil
	})
	if !errors.Is(err, context.Canceled) || hash != "" || description != "" {
		t.Fatal("single-chunk cancellation escaped partial text", err)
	}
	if hash, description, err := scaleLoopText(t.Context(), "", nil); err == nil || hash != "" || description != "" {
		t.Fatal("nil producer was accepted")
	}
}

func TestLoopTextScratchBound(t *testing.T) {
	// TotalAlloc is process-wide. Run only this test in the child, with no
	// server or store fixtures to start background workers.
	const child = "FLOW_TEST_LOOP_TEXT_SCRATCH_CHILD"
	if os.Getenv(child) != "1" {
		// The child has the rest of this run's deadline: the instrumented
		// 12 MiB measurements take minutes under CI's -race with atomic
		// coverage, beyond any fixed child timeout that fits a fast machine.
		timeout := time.Duration(0)
		if deadline, ok := t.Deadline(); ok {
			timeout = max(time.Until(deadline)-30*time.Second, time.Second)
		}
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.timeout="+timeout.String(), "-test.v")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated scratch bound: %v\n%s", err, output)
		}
		t.Logf("%s", output)
		return
	}
	stateBytes := unsafe.Sizeof(scaleLoopTextState{})
	if stateBytes > 4<<10 {
		t.Fatal("streamed loop text state exceeds 4 KiB", stateBytes)
	}
	spaced, unspaced := []byte(strings.Repeat("Alpha ", 128)), []byte(strings.Repeat("Alpha_", 128))
	producer := func(block []byte, repeats int) func(func([]byte) error) error {
		return func(visit func([]byte) error) error {
			for range repeats {
				if err := visit(block); err != nil {
					return err
				}
			}
			return nil
		}
	}
	// Take the minimum to account for incidental runtime allocations.
	measure := func(chunks func(func([]byte) error) error) uint64 {
		least := uint64(math.MaxUint64)
		for range 3 {
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			hash, description, err := scaleLoopText(t.Context(), "allocation bound", chunks)
			runtime.ReadMemStats(&after)
			if err != nil || len(hash) != 24 || utf8.RuneCountInString(description) > 140 {
				t.Fatal("bounded fixture failed", hash, description, err)
			}
			least = min(least, after.TotalAlloc-before.TotalAlloc)
		}
		return least
	}
	// Every whitespace run checkpoints the SHA-256 state through the standard
	// library's Digest.AppendBinary, which pads with append(b, make(...)...).
	// The compiler turns that into an in-place extension, but disables the
	// optimisation when instrumenting (-race), so each checkpoint then
	// allocates a short-lived pad of up to 64 bytes: ~58 MB of garbage for the
	// 12 MiB spaced input, none of it retained or input-sized. The spaced
	// bound is therefore asserted only in normal builds (CI runs TestScaleP
	// without -race in the scale differential jobs); the whitespace-free
	// input never checkpoints and keeps the same bound under -race too.
	for _, input := range []struct {
		name  string
		block []byte
		race  bool
	}{{"spaced", spaced, false}, {"unspaced", unspaced, true}} {
		if raceDetector && !input.race {
			// Not measured either: the unasserted 12 MiB spaced runs were
			// most of this test's time under -race.
			t.Logf("%s: race build, allocation bound not asserted (instrumented stdlib checkpoint pad)", input.name)
			continue
		}
		shortBytes, largeBytes := measure(producer(input.block, 4)), measure(producer(input.block, 16384))
		if largeBytes > 16<<10 || largeBytes > shortBytes+8<<10 {
			t.Fatal("streamed helper allocated input-sized scratch", input.name, shortBytes, largeBytes)
		}
		t.Logf("%s: state=%d bytes; 3KiB input allocations=%d bytes; 12MiB input allocations=%d bytes; finish buffer=%d bytes",
			input.name, stateBytes, shortBytes, largeBytes, 137*utf8.UTFMax)
	}
}
