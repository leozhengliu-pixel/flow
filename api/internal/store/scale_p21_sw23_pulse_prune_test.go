package store

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP21SW23PulsePruneRegistryDifferential(t *testing.T) {
	ignored := strings.Repeat("unrelated private metadata ", 400000)
	keys := []string{"active", "Case", "trail ", "a\x00b", ""}
	want := map[string]*pulseIndex{}
	for index, key := range keys {
		want[key] = &pulseIndex{fingerprint: uint64(index + 1)}
	}
	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprint(bounded), func(t *testing.T) {
			repository := &SQLiteStore{workspaces: map[string]domain.Bootstrap{}}
			if bounded {
				repository.metadataResidency = newMetadataResidency()
			}
			repository.pulseIndexes.entries = map[string]*pulseIndex{}
			for key, index := range want {
				repository.workspaces[key] = domain.Bootstrap{
					Workspace: domain.Workspace{ID: key, Name: ignored},
					Settings:  map[string]any{"ignored-private": ignored},
					Teams:     []domain.Team{{ID: "private", Private: true}},
				}
				repository.pulseIndexes.entries[key] = index
			}
			for _, key := range []string{"deleted", "case", "trail", "ab"} {
				repository.pulseIndexes.entries[key] = &pulseIndex{fingerprint: 999}
			}
			repository.prunePulseIndexes()
			if !reflect.DeepEqual(repository.pulseIndexes.entries, want) {
				t.Fatal("pruning changed exact key membership or retained index identity")
			}
			if len(repository.workspaces) != len(keys) || repository.workspaces["active"].Settings["ignored-private"] != ignored {
				t.Fatal("pruning changed resident metadata")
			}
			if repository.WorkspaceMetadataDecodes() != 0 || repository.PulseIndexBuilds() != 0 {
				t.Fatal("pruning decoded metadata or rebuilt an index")
			}
			if bounded && repository.metadataResidency.work.used != 0 {
				t.Fatal("constant-space registry pruning acquired body work")
			}
		})
	}
}

func TestScaleP21SW23PulsePruneConstantAllocation(t *testing.T) {
	for _, bodyBytes := range []int{64 << 10, 8 << 20} {
		t.Run(fmt.Sprint(bodyBytes), func(t *testing.T) {
			repository := &SQLiteStore{
				workspaces:        make(map[string]domain.Bootstrap, 1024),
				metadataResidency: newMetadataResidency(),
			}
			repository.pulseIndexes.entries = make(map[string]*pulseIndex, 2048)
			body := strings.Repeat("x", bodyBytes)
			index := &pulseIndex{}
			for i := range 1000 {
				key := fmt.Sprintf("workspace-%04d", i)
				repository.workspaces[key] = domain.Bootstrap{Settings: map[string]any{"ignored": body}}
				repository.pulseIndexes.entries[key] = index
			}
			allocs := testing.AllocsPerRun(20, func() {
				repository.pulseIndexes.entries["deleted"] = index
				repository.prunePulseIndexes()
			})
			if allocs != 0 {
				t.Fatalf("registry-only pruning allocated with %d ignored body bytes: %g", bodyBytes, allocs)
			}
			if len(repository.pulseIndexes.entries) != 1000 {
				t.Fatal("pruning dropped a live workspace or retained a deleted one")
			}
			t.Logf("ignored body bytes=%d workspaces=1000 allocations/op=%g", bodyBytes, allocs)
		})
	}
}
