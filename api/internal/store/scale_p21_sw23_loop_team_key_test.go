package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW23LegacyTeamKeyAllowed(repository *SQLiteStore, workspace, key string, allowed map[string]bool) bool {
	data, ok := repository.WorkspaceMetadataFields(workspace, "teams")
	if !ok {
		return false
	}
	for _, team := range data.Teams {
		if strings.EqualFold(team.Key, key) || strings.EqualFold(team.Name, key) {
			return allowed[team.ID]
		}
	}
	return false
}

func TestScaleP21SW23LoopTeamKeyInstalledDifferential(t *testing.T) {
	retired := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	data := domain.Bootstrap{
		Teams: []domain.Team{
			{ID: "denied-first", Key: "DUP", Name: "First alias", Private: true, RetiredAt: &retired},
			{ID: "allowed-later", Key: "dup", Name: "Later alias"},
			{ID: "unicode", Key: "K", Name: "\u017f"},
			{ID: "nul", Key: "nul\x00key", Name: "nul\x00name"},
			{ID: "spaced", Key: " with spaces ", Name: "Name"},
		},
		Settings: map[string]any{"unrelated": strings.Repeat("private unrelated body ", 400000)},
	}
	repository := &SQLiteStore{
		workspaces:        map[string]domain.Bootstrap{"workspace": data, "Case": data, "trail ": data, "a\x00b": data},
		lastWorkspaceKey:  "workspace",
		metadataResidency: newMetadataResidency(),
	}
	for _, role := range []string{"admin", "member", "guest", "app"} {
		data.ViewerRole = role
		repository.workspaces["workspace"] = data
		for index, allowed := range []map[string]bool{
			nil, {},
			{"denied-first": false, "allowed-later": true},
			{"denied-first": true},
			{"unicode": true, "nul": true, "spaced": true},
			{"denied-first": true, "allowed-later": true, "unicode": true, "nul": true, "spaced": true},
		} {
			for _, workspace := range []string{"workspace", "", "Case", "case", "trail ", "trail", "a\x00b", "ab", "missing"} {
				for _, key := range []string{"dup", "DUP", "first ALIAS", "later alias", "K", "\u212a", "s", "\u017f", "nul\x00key", "nulkey", "nul\x00name", " with spaces ", "with spaces", "", "missing"} {
					want := scaleP21SW23LegacyTeamKeyAllowed(repository, workspace, key, allowed)
					got := repository.LoopTeamKeyAllowed(workspace, key, allowed)
					if got != want {
						t.Fatalf("installed lookup differs role=%s set=%d workspace=%q key=%q want=%v got=%v", role, index, workspace, key, want, got)
					}
				}
			}
		}
	}
	if repository.LoopTeamKeyAllowed("workspace", "dup", map[string]bool{"allowed-later": true}) {
		t.Fatal("a denied first collision fell through to a later allowed team")
	}
	if !repository.LoopTeamKeyAllowed("workspace", "dup", map[string]bool{"denied-first": true}) {
		t.Fatal("lookup added an active/private filter absent from the old predicate")
	}
	// Unknown copy/resident stamps must not select newer canonical state.
	repository.metadataResidency.stamps["workspace"] = copyStamp{workspaceID: "stale", revision: -1}
	data.Teams[0].Name = "warm\x00alias"
	repository.workspaces["workspace"] = data
	if !repository.LoopTeamKeyAllowed("workspace", "warm\x00alias", map[string]bool{"denied-first": true}) {
		t.Fatal("lookup lost actual installed source after its stamp became stale")
	}
	if repository.WorkspaceMetadataDecodes() != 0 || repository.metadataResidency.work.used != 0 {
		t.Fatal("boolean resident lookup decoded metadata or retained body work")
	}
}

func TestScaleP21SW23LoopTeamKeyConstantAllocation(t *testing.T) {
	for _, size := range []int{64 << 10, 8 << 20} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			name := strings.Repeat("x", size)
			repository := &SQLiteStore{
				workspaces:        map[string]domain.Bootstrap{"workspace": {Teams: []domain.Team{{ID: "team", Key: "key", Name: name}}}},
				metadataResidency: newMetadataResidency(),
			}
			allowed := map[string]bool{"team": true}
			before := testing.AllocsPerRun(10, func() {
				if !scaleP21SW23LegacyTeamKeyAllowed(repository, "workspace", name, allowed) {
					panic("missing legacy fixture team")
				}
			})
			after := testing.AllocsPerRun(10, func() {
				if !repository.LoopTeamKeyAllowed("workspace", name, allowed) {
					panic("missing installed fixture team")
				}
			})
			if after != 0 || before <= after {
				t.Fatalf("lookup allocations did not remain constant: before=%g after=%g", before, after)
			}
			t.Logf("team name bytes=%d allocations/op legacy=%g native=%g", size, before, after)
		})
	}
}
