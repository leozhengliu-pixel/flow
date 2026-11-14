package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW43HierarchyDiskIndex(t *testing.T) {
	diskBefore := metadataLogicalDiskUsed.Load()
	index, err := newMetadataHierarchyIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(index.close)
	first, err := index.intern(t.Context(), strings.NewReader("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	// A hash match must never substitute for exact native key equality.
	digest := sha256.Sum256([]byte("bravo"))
	if _, err := index.tx.ExecContext(t.Context(), `UPDATE nodes SET hash=? WHERE id=?`, digest[:], first); err != nil {
		t.Fatal(err)
	}
	second, err := index.intern(t.Context(), strings.NewReader("bravo"))
	if err != nil || second == first {
		t.Fatal("hash collision aliased different keys", first, second, err)
	}
	again, err := index.intern(t.Context(), strings.NewReader("bravo"))
	if err != nil || again != second {
		t.Fatal("collision page skipped exact key", second, again, err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	huge, err := index.intern(t.Context(), io.LimitReader(&scaleP21SW43KeyPattern{}, 20<<20))
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := index.intern(t.Context(), io.LimitReader(&scaleP21SW43KeyPattern{}, 20<<20))
	if err != nil || huge != duplicate {
		t.Fatal("streamed huge native key did not intern exactly", huge, duplicate, err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("20 MiB key retained whole-key Go storage: allocated=%d", allocated)
	}
	if info, err := index.keys.Stat(); err != nil || info.Size() != 10+(20<<20) {
		t.Fatal("duplicate huge key retained duplicate disk bytes", info, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	reader := &scaleP21SW43CancelKey{cancel: cancel}
	if _, err := index.intern(canceled, reader); !errors.Is(err, context.Canceled) {
		t.Fatal("streamed key lost cancellation", err)
	}
	directory := index.directory
	index.close()
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("hierarchy scratch survived close", err)
	}
	if after := metadataLogicalDiskUsed.Load(); after != diskBefore {
		t.Fatalf("hierarchy disk reservation leaked: before=%d after=%d", diskBefore, after)
	}
}

func TestScaleP21SW43HierarchyDiskDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(43))
	stamp := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	for fixture := range 20 {
		index, err := newMetadataHierarchyIndex(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer index.close()
			data := domain.Bootstrap{TeamSettings: map[string]domain.TeamSettings{}}
			keys := []string{"", "native\x00team", "missing"}
			for key := range 40 {
				keys = append(keys, fmt.Sprintf("team-%d", key))
			}
			root := keys[random.Intn(len(keys))]
			for range 120 {
				team := domain.Team{ID: keys[random.Intn(len(keys))]}
				if random.Intn(4) == 0 {
					team.RetiredAt = &stamp
				}
				data.Teams = append(data.Teams, team)
			}
			for _, key := range keys {
				if random.Intn(5) == 0 {
					continue
				}
				data.TeamSettings[key] = domain.TeamSettings{
					ParentTeamID:            keys[random.Intn(len(keys))],
					InheritWorkflowStatuses: random.Intn(4) == 0,
				}
			}
			intern := func(key string) int64 {
				id, err := index.intern(t.Context(), strings.NewReader(key))
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			empty := intern("")
			for key, settings := range data.TeamSettings {
				if _, err := index.tx.ExecContext(t.Context(), `UPDATE nodes SET parent=?,inherits=? WHERE id=?`, intern(settings.ParentTeamID), settings.InheritWorkflowStatuses, intern(key)); err != nil {
					t.Fatal(err)
				}
			}
			for _, team := range data.Teams {
				if _, err := index.tx.ExecContext(t.Context(), `UPDATE nodes SET has_team=1,active=?,parent=COALESCE(parent,?) WHERE id=?`, team.RetiredAt == nil, empty, intern(team.ID)); err != nil {
					t.Fatal(err)
				}
			}
			expected := true
			for _, id := range append([]string{root}, domain.TeamDescendantIDs(&data, root)...) {
				if settings := data.TeamSettings[id]; settings.InheritWorkflowStatuses && settings.ParentTeamID != "" {
					expected = false
				}
			}
			for range 2 {
				actual, err := index.independent(t.Context(), intern(root))
				if err != nil || actual != expected {
					t.Fatalf("fixture %d native graph differs: old=%t new=%t err=%v", fixture, expected, actual, err)
				}
			}
		}()
	}
}

func TestScaleP21SW43HierarchyDiskGrowth(t *testing.T) {
	before := metadataLogicalDiskUsed.Load()
	index, err := newMetadataHierarchyIndex(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer index.close()
	for ordinal := range 32768 {
		if _, err := index.intern(t.Context(), strings.NewReader(fmt.Sprintf("cardinality-key-%d", ordinal))); err != nil {
			t.Fatal(err)
		}
	}
	if index.dbBytes <= 1<<20 {
		t.Fatal("fixture did not exercise reserved SQLite page growth")
	}
	var pages int64
	if err := index.tx.QueryRowContext(t.Context(), `PRAGMA page_count`).Scan(&pages); err != nil || pages*4096 > index.dbBytes {
		t.Fatal("physical graph exceeded admitted disk pages", pages, index.dbBytes, err)
	}
	index.close()
	if after := metadataLogicalDiskUsed.Load(); after != before {
		t.Fatalf("grown graph disk reservation leaked: before=%d after=%d", before, after)
	}
}

type scaleP21SW43KeyPattern struct{ position int }

func (r *scaleP21SW43KeyPattern) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte((r.position + i) % 251)
	}
	r.position += len(p)
	return len(p), nil
}

type scaleP21SW43CancelKey struct{ cancel context.CancelFunc }

func (r *scaleP21SW43CancelKey) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	r.cancel()
	return len(p), nil
}
