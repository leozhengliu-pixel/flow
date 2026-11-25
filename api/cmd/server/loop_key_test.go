package main

import (
	"fmt"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW23LoopTeamKeyDispatchDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repository := range scaleP03ServerTestStores(t) {
		fixture := scaleP21M2Setup(t, repository)
		workspace := fixture.data.Workspace.URLKey
		first, later, unicode := idgen.New("sw23-first-"), idgen.New("sw23-later-"), idgen.New("sw23-unicode-")
		largeName := strings.Repeat("legal installed team name ", 5000)
		if err := repository.MutateWorkspace(t.Context(), workspace, "scale_p21_sw23.loop_key_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams,
				domain.Team{ID: first, Key: "DUP", Name: "First alias", Private: true},
				domain.Team{ID: later, Key: "dup", Name: largeName},
				domain.Team{ID: unicode, Key: "K", Name: "\u017f"},
			)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repository.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repository, workspace, "teams")
		for index, allowed := range []map[string]bool{
			nil, {}, {first: true}, {later: true}, {unicode: true},
			{first: true, later: true, unicode: true},
		} {
			for _, key := range []string{"dup", "DUP", "first ALIAS", largeName, "K", "\u212a", "s", "\u017f", " first alias ", "", "missing"} {
				t.Run(fmt.Sprintf("%d/%q", index, key[:min(len(key), 30)]), func(t *testing.T) {
					legacy := &loopScope{s: &server{store: repository}, workspace: workspace, teams: allowed}
					bounded := &loopScope{s: &server{store: probe}, workspace: workspace, teams: allowed}
					if got, want := bounded.teamKeyAllowed(key), legacy.teamKeyAllowed(key); got != want {
						t.Fatalf("loop scope lookup differs for allowed=%v key=%q: off=%v bounded=%v", allowed, key[:min(len(key), 30)], want, got)
					}
				})
			}
		}
		for _, source := range []*store.SQLiteStore{repository, probe} {
			scope := &loopScope{s: &server{store: source}, workspace: workspace + "-missing", teams: map[string]bool{first: true}}
			if scope.teamKeyAllowed("DUP") {
				t.Fatal("team key lookup crossed a missing workspace binding")
			}
		}
		if after := scaleP21M3Fingerprint(t, repository, workspace, "teams"); after != before {
			t.Fatal("boolean loop scope lookup changed canonical team bytes")
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded loop scope lookup decoded complete metadata")
		}
	}
}
