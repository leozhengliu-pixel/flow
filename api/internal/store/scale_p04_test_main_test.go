package store

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	comparisons, differences := NarrowMutationVerificationCounts()
	fmt.Fprintf(os.Stderr, "P04 verification callbacks=%d mismatches=%d\n", comparisons, differences)
	if differences != 0 {
		code = 1
	}
	os.Exit(code)
}
