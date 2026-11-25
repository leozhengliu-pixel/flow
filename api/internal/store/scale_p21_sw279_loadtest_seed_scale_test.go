//go:build loadtest

package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestScaleP21SW279BoundedSeedScale seeds the P01 metadata fixture (count
// scale FLOW_P21_SEED_SCALE, default 2) through a bounded store and measures
// it: the heap stays bounded by a batch, not by the seeded workspace, no
// workspace is decoded and every record is reported.
func TestScaleP21SW279BoundedSeedScale(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	scale := 2.0
	if raw := os.Getenv("FLOW_P21_SEED_SCALE"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 {
			t.Fatal("FLOW_P21_SEED_SCALE must be positive")
		}
		scale = value
	}
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspace := "test-workspace"
	data, _ := repo.storedWorkspace(workspace)
	users := []string{}
	for _, user := range data.Users {
		users = append(users, user.ID)
	}
	view, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	spec := SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000, Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000,
		CountScale: scale, BodyScale: 1, TeamIDs: []string{data.Teams[0].ID}, UserIDs: users, Notifications: len(users) * 2, Relations: true,
	}
	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapInuse > peak.Load() {
					peak.Store(stats.HeapInuse)
				}
			}
		}
	}()
	started := time.Now()
	err = view.SeedSyntheticMetadata(t.Context(), workspace, spec)
	var sizes map[string]int
	if err == nil {
		sizes, err = view.WorkspaceStateBytes(t.Context(), workspace)
	}
	close(done)
	<-sampled
	if err != nil {
		t.Fatal(err)
	}
	growth := int64(peak.Load()) - int64(base.HeapInuse)
	var records int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND record_key LIKE 'lt-%'`, workspace).Scan(&records); err != nil {
		t.Fatal(err)
	}
	seeded := sizes["projects"] + sizes["trash"] + sizes["documents"] + sizes["conversations"]
	t.Logf("scale=%g seeded=%dB records=%d heapGrowth=%dB elapsed=%s sizes=%v", scale, seeded, records, growth, time.Since(started).Round(time.Millisecond), sizes)
	if view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("bounded seed decoded the workspace or leaked work")
	}
	want := int(2321*scale+0.999) + int(210*scale+0.999) + int(78*scale+0.999) + int(400*scale+0.999)
	if records < want-4 || records > want {
		t.Fatalf("records=%d want about %d", records, want)
	}
	// A batch is at most 6 MiB of records; the heap must not track the
	// seeded workspace (legacy holds and clones all of it).
	if growth > 96<<20 || seeded < 40<<20 {
		t.Fatalf("heap growth %d for %d seeded bytes", growth, seeded)
	}
}
