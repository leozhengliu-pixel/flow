package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetadataMemoryPlan(t *testing.T) {
	const gib = 1 << 30
	for _, tc := range []struct {
		name                    string
		cgroup, physical, limit int64
		want                    int64
		source                  string
	}{
		{"host", 0, 16 * gib, 0, 16 * gib / 100 * 70, "physical memory"},
		{"container", 4 * gib, 16 * gib, 0, 4 * gib / 100 * 70, "cgroup"},
		{"small container", gib / 2, 16 * gib, 0, gib/2 - gib/2/5, "cgroup"},
		{"runtime limit", 4 * gib, 16 * gib, 2 * gib, 2 * gib, "GOMEMLIMIT"},
		{"unknown", 0, 0, 0, gib, "floor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if bytes, source := defaultMetadataMemory(tc.cgroup, tc.physical, tc.limit); bytes != tc.want || source != tc.source {
				t.Fatalf("ceiling %d from %s, want %d from %s", bytes, source, tc.want, tc.source)
			}
		})
	}

	t.Setenv("FLOW_METADATA_MEMORY_BYTES", "4294967296")
	r := newMetadataResidency()
	if r.work.limit != gib || r.policyLimit != gib/4 || r.cacheLimit != gib/8 || metadataLogicalDescriptorCap() != gib/8 ||
		metadataLogicalDiskCap() != 4*gib || coldDecodeCacheBudget() != int64(4*gib)/100*35 || metadataColdSlots() != 4 ||
		cap(r.accessorLane()) != 4 || BoundedMetadataReadConcurrency() != 8 {
		t.Fatal("unexpected derived memory or concurrency budgets")
	}
	t.Setenv("FLOW_METADATA_WORK_BYTES", "536870912")
	t.Setenv("FLOW_METADATA_POLICY_BYTES", "33554432")
	t.Setenv("FLOW_METADATA_SOURCE_BYTES", "67108864")
	t.Setenv("FLOW_METADATA_SOURCE_DISK_BYTES", "2147483648")
	t.Setenv("FLOW_COLD_DECODE_CACHE_BYTES", "0")
	t.Setenv("FLOW_COLD_SLOTS", "3")
	t.Setenv("FLOW_COLD_VIEW_LANES", "2")
	r = newMetadataResidency()
	if r.work.limit != gib/2 || r.policyLimit != gib/32 || metadataLogicalDescriptorCap() != gib/16 ||
		metadataLogicalDiskCap() != 2*gib || coldDecodeCacheBudget() != 0 || metadataColdSlots() != 3 || cap(r.accessorLane()) != 2 {
		t.Fatal("a tier override was ignored")
	}
}

func TestMetadataMemoryWorkConcurrency(t *testing.T) {
	t.Setenv("FLOW_METADATA_MEMORY_BYTES", "3221225472")
	r := newMetadataResidency()
	var releases []func()
	for range 3 {
		ctx, release, err := r.work.acquire(t.Context(), metadataWorkBytes)
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		releases = append(releases, release)
		_, nested, err := r.work.acquire(ctx, metadataWorkBytes)
		if err != nil {
			t.Fatal(err)
		}
		nested()
	}
	if used, peak := r.work.used, r.work.peak; used != r.work.limit || peak != used {
		t.Fatalf("work used %d peak %d, want %d", used, peak, r.work.limit)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := r.work.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("work over the aggregate budget returned %v", err)
	}
	for _, release := range releases {
		release()
	}
	if r.work.used != 0 || r.work.waiting != 0 {
		t.Fatal("work or waiter charge survived its release")
	}
}
