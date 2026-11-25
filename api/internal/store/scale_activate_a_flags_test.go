package store

import (
	"os"
	"testing"
)

func TestScaleActivateAIndexedReadsDefault(t *testing.T) {
	value, present := os.LookupEnv("FLOW_INDEXED_READS")
	if err := os.Unsetenv("FLOW_INDEXED_READS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv("FLOW_INDEXED_READS", value)
		} else {
			_ = os.Unsetenv("FLOW_INDEXED_READS")
		}
	})
	if got := indexedReadsMode(); got != "off" {
		t.Fatalf("unset FLOW_INDEXED_READS = %q, want off pending the load gate", got)
	}
	for _, value := range []string{"", "off", "invalid"} {
		t.Setenv("FLOW_INDEXED_READS", value)
		if got := indexedReadsMode(); got != "off" {
			t.Fatalf("FLOW_INDEXED_READS=%q = %q, want off", value, got)
		}
	}
	t.Setenv("FLOW_INDEXED_READS", "shadow")
	if got := indexedReadsMode(); got != "shadow" {
		t.Fatalf("shadow mode = %q", got)
	}
}
