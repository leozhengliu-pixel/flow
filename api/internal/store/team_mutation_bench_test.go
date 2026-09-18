package store

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"flow/api/internal/domain"
)

type benchmarkWorkspaceID struct {
	kind  string
	teams int
}

var (
	benchmarkStoresMu sync.Mutex
	benchmarkStores   = map[benchmarkWorkspaceID]*SQLiteStore{}
	benchmarkKeys     = map[benchmarkWorkspaceID]string{}
	benchmarkRunSeq   atomic.Uint64
)

// Seed once per (kind, size); the harness reruns this function for the final b.N.
func benchmarkWorkspace(b *testing.B, kind string, teams int) (*SQLiteStore, string) {
	id := benchmarkWorkspaceID{kind: kind, teams: teams}
	benchmarkStoresMu.Lock()
	defer benchmarkStoresMu.Unlock()
	if repo, known := benchmarkStores[id]; known {
		return repo, benchmarkKeys[id]
	}
	repo, err := OpenSQLiteTestFixture(b.TempDir() + "/flow.db")
	if err != nil {
		b.Fatal(err)
	}
	previousStateBytes, previousTransactionBytes := repo.maxStateBytes, repo.db.maxTransactionBytes
	repo.maxStateBytes, repo.db.maxTransactionBytes = 1<<30, 0
	key := seedBulkTeams(b, repo, teams)
	repo.maxStateBytes, repo.db.maxTransactionBytes = previousStateBytes, previousTransactionBytes
	benchmarkStores[id], benchmarkKeys[id] = repo, key
	return repo, key
}

func benchmarkTeamCreate(b *testing.B, teams int) {
	repo, key := benchmarkWorkspace(b, "create", teams)
	run := benchmarkRunSeq.Add(1)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("team_bench_%d_%d", run, i)
		if err := repo.MutateWorkspace(ctx, key, "team.created", id, nil, func(next *domain.Bootstrap) error {
			next.Teams = append(next.Teams, domain.Team{ID: id, Name: id, Key: "BNC"})
			next.TeamSettings[id] = bulkTeamSettings(id)
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTeamMetadataMutation(b *testing.B) {
	for _, teams := range []int{1000, 10000, 20000} {
		b.Run(fmt.Sprintf("%dTeams", teams), func(b *testing.B) { benchmarkTeamCreate(b, teams) })
	}
}

func BenchmarkTeamSettingsParentPatch(b *testing.B) {
	for _, teams := range []int{1000, 10000, 20000} {
		b.Run(fmt.Sprintf("%dTeams", teams), func(b *testing.B) {
			repo, key := benchmarkWorkspace(b, "parent", teams)
			ctx := context.Background()
			child := "bulk-team-00000"
			parents := []string{"bulk-team-00001", "bulk-team-00002"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				parent := parents[i%2]
				if err := repo.MutateWorkspace(ctx, key, "team.settings_updated", child, map[string]string{"parentTeamId": parent}, func(next *domain.Bootstrap) error {
					settings := next.TeamSettings[child]
					settings.ParentTeamID = parent
					next.TeamSettings[child] = settings
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Non-scoped event clones the whole workspace so write cost can be compared.
func BenchmarkGenericTeamMutationBaseline(b *testing.B) {
	for _, teams := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("%dTeams", teams), func(b *testing.B) {
			repo, key := benchmarkWorkspace(b, "generic", teams)
			run := benchmarkRunSeq.Add(1)
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprintf("team_generic_%d_%d", run, i)
				if err := repo.MutateWorkspace(ctx, key, "test.team_generic", id, nil, func(next *domain.Bootstrap) error {
					next.Teams = append(next.Teams, domain.Team{ID: id, Name: id, Key: "GNC"})
					next.TeamSettings[id] = bulkTeamSettings(id)
					return nil
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Measures the reflection clone the generic write path still pays per mutation.
func BenchmarkCloneBootstrapBaseline(b *testing.B) {
	for _, teams := range []int{1000, 10000, 20000} {
		b.Run(fmt.Sprintf("%dTeams", teams), func(b *testing.B) {
			data := bulkBootstrap(teams)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = cloneBootstrap(data)
			}
		})
	}
}

func bulkBootstrap(teams int) domain.Bootstrap {
	data := EmptyWorkspace("Bench", "bench", "us", domain.User{ID: "bench-user", Name: "Bench"})
	baseStates := data.States
	for i := 0; i < teams; i++ {
		id := fmt.Sprintf("bulk-team-%05d", i)
		data.Teams = append(data.Teams, domain.Team{ID: id, Name: id})
		data.TeamSettings[id] = bulkTeamSettings(id)
		data.CycleSettings[id] = domain.CycleSettings{DurationWeeks: 2}
		for j, state := range baseStates {
			clone := state
			clone.ID = fmt.Sprintf("%s-state-%d", id, j)
			clone.TeamID = id
			data.States = append(data.States, clone)
		}
	}
	return data
}
