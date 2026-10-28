package idgen

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

var stampShape = regexp.MustCompile(`^[0-9]{19}_[0-9a-hjkmnp-tv-z]{10}$`)

// Goroutines of one server sharing a frozen clock (every call in the same
// tick) never get the same id, and each goroutine's ids keep increasing.
func TestNewIsUniqueWithinAProcessOnAFrozenClock(t *testing.T) {
	frozen := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	generator := NewGenerator(func() time.Time { return frozen })
	const goroutines, perGoroutine = 32, 5000
	results := make([][]string, goroutines)
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				results[g] = append(results[g], generator.New("evt_"))
			}
		}()
	}
	wg.Wait()
	seen := map[string]bool{}
	digits := map[string]bool{}
	for _, list := range results {
		for index, id := range list {
			if seen[id] {
				t.Fatalf("duplicate id %s", id)
			}
			seen[id] = true
			// The time part alone is unique within a process.
			digits[id[len("evt_"):len("evt_")+19]] = true
			if index > 0 && id <= list[index-1] {
				t.Fatalf("ids of one goroutine went backwards: %s after %s", id, list[index-1])
			}
		}
	}
	if len(digits) != goroutines*perGoroutine {
		t.Fatalf("%d distinct time parts for %d ids", len(digits), goroutines*perGoroutine)
	}
}

// Servers are separate processes reading the same coarse clock, so their time
// parts coincide; the random part keeps their ids apart. Each generator below
// is one server with its own process state, all frozen in the same tick.
func TestNewIsUniqueAcrossServersOnTheSameTick(t *testing.T) {
	frozen := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	const servers, perServer = 16, 20000
	results := make([][]string, servers)
	var wg sync.WaitGroup
	for s := range servers {
		generator := NewGenerator(func() time.Time { return frozen })
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perServer {
				results[s] = append(results[s], generator.New("evt_"))
			}
		}()
	}
	wg.Wait()
	seen := make(map[string]bool, servers*perServer)
	sameTime := 0
	for _, list := range results {
		for _, id := range list {
			if seen[id] {
				t.Fatalf("duplicate id %s", id)
			}
			seen[id] = true
			if id[4:23] == results[0][0][4:23] {
				sameTime++
			}
		}
	}
	// The simulation does put servers on the same time part.
	if sameTime != servers {
		t.Fatalf("%d ids share the first time part, want %d", sameTime, servers)
	}
}

// The shape keeps what callers rely on: the prefix, 19 zero-padded digits of
// Unix nanoseconds (the clock, or just past it), URL-safe characters.
func TestStampShape(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 123456789, time.UTC)
	generator := NewGenerator(func() time.Time { return at })
	id := generator.New("customer_")
	if len(id) != len("customer_")+StampLength || id[:9] != "customer_" || !stampShape.MatchString(id[9:]) {
		t.Fatalf("id %q", id)
	}
	if got := id[9:28]; got != strconv.FormatInt(at.UnixNano(), 10) {
		t.Fatalf("time part %s, want %d", got, at.UnixNano())
	}
	if stamp := generator.Stamp(); !stampShape.MatchString(stamp) || stamp[:19] != strconv.FormatInt(at.UnixNano()+1, 10) {
		t.Fatalf("stamp %q", stamp)
	}
	early := NewGenerator(func() time.Time { return time.Unix(0, 42) }).Stamp()
	if !stampShape.MatchString(early) || early[:19] != "0000000000000000042" {
		t.Fatalf("early stamp %q", early)
	}
}

// Ids of one prefix sort by creation time as strings, and ids generated now
// sort after the older "<prefix><nanoseconds>" ids written before them, so
// `ORDER BY id` and `id > cursor` pagination keep their order.
func TestIDsSortByTimeIncludingOlderIDs(t *testing.T) {
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	current := start
	generator := NewGenerator(func() time.Time { return current })
	var stored []string
	for index := range 50 {
		// Older ids interleaved with new ones, as a table holds both; two
		// new ids per (coarse) clock reading.
		current = start.Add(time.Duration(index) * time.Microsecond)
		stored = append(stored, fmt.Sprintf("agent_activity_%d", current.Add(-500*time.Nanosecond).UnixNano()))
		stored = append(stored, generator.New("agent_activity_"), generator.New("agent_activity_"))
	}
	want := append([]string(nil), stored...)
	sort.Strings(stored)
	for index := range stored {
		if stored[index] != want[index] {
			t.Fatalf("sorted position %d is %s, want %s", index, stored[index], want[index])
		}
	}
	// The same nanosecond: the older id sorts first.
	old := fmt.Sprintf("evt_%d", start.UnixNano())
	if fresh := NewGenerator(func() time.Time { return start }).New("evt_"); !(old < fresh) {
		t.Fatalf("%s does not sort after %s", fresh, old)
	}
}

// Simulated servers share the time part and differ in the random part; the
// real generator comes back on restore.
func TestSimulateServersForTest(t *testing.T) {
	frozen := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	restore := SimulateServersForTest(frozen)
	first, second := New("x_"), New("x_")
	restore()
	want := strconv.FormatInt(frozen.UnixNano(), 10)
	if first == second || first[2:21] != want || second[2:21] != want {
		t.Fatalf("simulated ids %s %s", first, second)
	}
	if after := New("x_"); after[2:21] == want {
		t.Fatalf("generator not restored: %s", after)
	}
}

func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = New("evt_")
		}
	})
}
