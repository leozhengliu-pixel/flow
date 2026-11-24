package store

import (
	"errors"
	"strconv"
	"testing"
)

func TestLogicalBuilderKeepsDiskBudgetForItsLifetime(t *testing.T) {
	before := metadataLogicalDiskUsed.Load()
	t.Setenv("FLOW_METADATA_SOURCE_DISK_BYTES", strconv.FormatInt(before+16, 10))
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()

	t.Setenv("FLOW_METADATA_SOURCE_DISK_BYTES", strconv.FormatInt(before+8, 10))
	if err := builder.write(make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	if err := builder.write([]byte{0}); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("append beyond the builder's disk budget must fail")
	}
	builder.Close()

	next, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := next.write(make([]byte, 16)); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("the next builder must use the new disk budget")
	}
	if got := metadataLogicalDiskUsed.Load(); got != before {
		t.Fatalf("builder left %d bytes charged", got-before)
	}
}

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
