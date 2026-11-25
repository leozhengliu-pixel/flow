package main

import (
	"os"
	"testing"
)

func unsetScaleActivateAEnv(t *testing.T, name string) {
	t.Helper()
	value, present := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(name, value)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func TestScaleActivateAFlagDefaults(t *testing.T) {
	for _, name := range []string{"FLOW_SYNC_MANIFEST", "FLOW_INDEXED_READS"} {
		unsetScaleActivateAEnv(t, name)
		if syncFlagEnabled(name) {
			t.Fatalf("%s should remain off pending the load gate", name)
		}
		t.Setenv(name, "")
		if syncFlagEnabled(name) {
			t.Fatalf("%s empty value should preserve the explicit off rollback", name)
		}
		t.Setenv(name, "off")
		if syncFlagEnabled(name) {
			t.Fatalf("%s off value enabled the flag", name)
		}
		t.Setenv(name, "on")
		if !syncFlagEnabled(name) {
			t.Fatalf("%s on value disabled the flag", name)
		}
	}

	unsetScaleActivateAEnv(t, "FLOW_SYNC_V2")
	if syncV2Enabled() {
		t.Fatal("P12 FLOW_SYNC_V2 changed its default")
	}
}

func TestScaleActivateASyncV2CapabilityGate(t *testing.T) {
	unsetScaleActivateAEnv(t, "FLOW_SYNC_V2")
	unsetScaleActivateAEnv(t, "FLOW_SYNC_MANIFEST")
	unsetScaleActivateAEnv(t, "FLOW_INDEXED_READS")
	if syncV2CapabilityAdvertised(true, true) {
		t.Fatal("sync-v2 was advertised while P12 was off")
	}

	t.Setenv("FLOW_SYNC_V2", "on")
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	if !syncV2CapabilityAdvertised(true, true) {
		t.Fatal("complete sync-v2 dependencies were not advertised")
	}
	t.Setenv("FLOW_SYNC_MANIFEST", "off")
	if syncV2CapabilityAdvertised(true, true) {
		t.Fatal("sync-v2 ignored the manifest rollback")
	}
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "off")
	if syncV2CapabilityAdvertised(true, true) {
		t.Fatal("sync-v2 ignored the indexed-read rollback")
	}
	t.Setenv("FLOW_INDEXED_READS", "on")
	if syncV2CapabilityAdvertised(false, true) {
		t.Fatal("sync-v2 was advertised without a journal")
	}
	if syncV2CapabilityAdvertised(true, false) {
		t.Fatal("sync-v2 was advertised with incomplete core collections")
	}
}
