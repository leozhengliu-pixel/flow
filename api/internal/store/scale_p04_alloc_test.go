//go:build loadtest

package store

import (
	"context"
	"maps"
	"path/filepath"
	"runtime"
	"testing"

	"flow/api/internal/domain"
)

var scaleP04InputSink domain.Bootstrap

func scaleP04AllocationFixture(t testing.TB) domain.Bootstrap {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	if err := repo.SeedSyntheticMetadata(context.Background(), "test-workspace", SyntheticMetadata{
		Projects: 2321, ProjectBytes: 12000, Trash: 210, TrashBytes: 41000,
		Documents: 78, DocumentBytes: 64000, Conversations: 400, ConversationBytes: 2000,
		TeamIDs: []string{"team_test"},
	}); err != nil {
		t.Fatal(err)
	}
	data, _ := repo.storedWorkspace("test-workspace")
	return data
}

func BenchmarkScaleP04MutationInputs(b *testing.B) {
	data := scaleP04AllocationFixture(b)
	fields := issueRecordCallbackFields(domain.IssueUpdateInput{})
	b.Run("issue", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			scaleP04InputSink = issueUpdateMetadata(data, fields...)
		}
	})
	b.Run("comment", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			scaleP04InputSink, _ = issueScopeCallbackInput(data, true)
		}
	})
	b.Run("team", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			next, _ := teamCallbackInput(data, "team.updated", map[string]string{"name": "Renamed"})
			_ = snapshotTeamMutation("team.updated", "team_test", &data)
			scaleP04InputSink = next
		}
	})
}

func TestScaleP04CallbackAllocationBudget(t *testing.T) {
	data := scaleP04AllocationFixture(t)
	for _, kind := range []string{"issue", "comment"} {
		t.Run(kind, func(t *testing.T) {
			measure := func(mode string) uint64 {
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
				fields := issueRecordCallbackFields(domain.IssueUpdateInput{})
				apply := func() {
					if kind == "issue" {
						scaleP04InputSink = issueUpdateMetadata(data, fields...)
					} else {
						scaleP04InputSink, _ = issueScopeCallbackInput(data, true)
					}
				}
				apply()
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				for range 20 {
					apply()
				}
				runtime.ReadMemStats(&after)
				return (after.TotalAlloc - before.TotalAlloc) / 20
			}
			legacy, narrow := measure("off"), measure("on")
			t.Logf("%s callback input: legacy=%d B/op narrow=%d B/op reduction=%.2f%%", kind, legacy, narrow, 100*(1-float64(narrow)/float64(legacy)))
			if narrow*5 > legacy {
				t.Fatal("callback input allocation reduction is below 80%")
			}
		})
	}
}

func BenchmarkScaleP04TeamCreationInputs(b *testing.B) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(b.TempDir(), "flow.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { repo.Close() })
	key := seedBulkTeams(b, repo, 1000)
	data, _ := repo.storedWorkspace(key)
	stamps := 0
	for _, team := range data.Teams {
		if team.CreatedAt != nil || team.UpdatedAt != nil {
			stamps++
		}
	}
	b.Logf("teams=%d settings=%d children=%d labels=%d stamps=%d", len(data.Teams), len(data.TeamSettings), len(data.TeamChildren), len(data.LabelIndex), stamps)
	b.Run("input", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			scaleP04InputSink, _ = teamCallbackInput(data, "team.created", nil)
		}
	})
	b.Run("settings", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			scaleP04InputSink.TeamSettings = maps.Clone(data.TeamSettings)
		}
	})
	b.Run("directory", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			next := data
			domain.RebuildTeamDirectory(&next)
			scaleP04InputSink = next
		}
	})
}
