package store

import "testing"

func TestScaleP15HeapSpoolSizeCheckUsesSharedAllowance(t *testing.T) {
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "10")
	t.Setenv("TMPDIR", t.TempDir())
	first, err := NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !first.CanAppend(10) || first.CanAppend(11) || first.CanAppend(-1) {
		t.Fatal("size preflight did not use the configured allowance")
	}
	if _, err := second.Write([]byte("123456")); err != nil {
		t.Fatal(err)
	}
	if !first.CanAppend(4) || first.CanAppend(5) {
		t.Fatal("size preflight ignored another spool's reservation")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if !first.CanAppend(10) {
		t.Fatal("size preflight retained a closed spool's reservation")
	}
}
