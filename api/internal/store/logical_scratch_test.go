package store

import (
	"errors"
	"testing"
)

func TestScaleP21LogicalScratchContention(t *testing.T) {
	t.Setenv("FLOW_METADATA_SOURCE_DISK_BYTES", "1024")
	ctx, _ := withMetadataWorkDemand(t.Context())
	before := metadataLogicalDiskUsed.Load()
	held, err := HoldMetadataLogicalDiskForTesting(512)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if _, err := spool.Write(make([]byte, 512)); err != nil {
		t.Fatal(err)
	}
	if err := spool.checkAppend(1); !errors.Is(err, ErrRecordSpoolContended) {
		t.Fatal("a fitting preflight must report contention")
	}
	if _, err := spool.Write([]byte{0}); !errors.Is(err, ErrRecordSpoolContended) {
		t.Fatal("a fitting append must report contention")
	}
	if _, err := newMetadataLogicalRecordSpool(ctx); !errors.Is(err, ErrRecordSpoolContended) {
		t.Fatal("a fitting spool creation must report contention")
	}
	for _, size := range []int64{513, metadataLogicalDiskBytes + 1} {
		if err := spool.checkAppend(size); !errors.Is(err, ErrRecordSpoolBudget) || errors.Is(err, ErrRecordSpoolContended) {
			t.Fatal("an append that cannot fit must retain its size refusal")
		}
	}
	held()
	if _, err := spool.Write(make([]byte, 512)); err != nil {
		t.Fatal(err)
	}
	if err := spool.Close(); err != nil || metadataLogicalDiskUsed.Load() != before || metadataLogicalDiskDemand(ctx).Load() != 0 {
		t.Fatal("closing a spool must return its quota")
	}
}
