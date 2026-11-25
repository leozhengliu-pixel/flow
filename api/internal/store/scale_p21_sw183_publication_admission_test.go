package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

type scaleP21SW183Installed struct {
	policy domain.Bootstrap
	costs  map[string]int
	stamp  copyStamp
}

func scaleP21SW183Before(t *testing.T, f scaleP21SW95Fixture) scaleP21SW183Installed {
	t.Helper()
	policy, found := f.view.storedWorkspace(f.workspace)
	if !found {
		t.Fatal("publication policy fixture is missing")
	}
	policy = cloneBootstrap(policy)
	domain.RebuildTeamDirectory(&policy)
	before := scaleP21SW183Installed{policy: policy, costs: map[string]int{}}
	f.view.metadataResidency.mu.Lock()
	for key, cost := range f.view.metadataResidency.policy {
		before.costs[key] = cost
	}
	before.stamp = f.view.metadataResidency.stamps[f.workspace]
	f.view.metadataResidency.mu.Unlock()
	return before
}

func scaleP21SW183Stage(t *testing.T, f scaleP21SW95Fixture, tx *sqlTx) {
	t.Helper()
	actor := scaleP21SW95Actor(f)
	image, err := f.view.persistLegacyActorImage(t.Context(), tx, f.workspace, f.prior, &actor)
	if err != nil {
		t.Fatal(err)
	}
	image.Close()
	scaleP21SW58StageDiscussionRows(t, f.f, tx)
}

func scaleP21SW183PolicyTransfer(query string) bool {
	return strings.Contains(query, "THEN data ELSE NULL END") &&
		(strings.Contains(query, "FROM workspace_states") || strings.Contains(query, "FROM workspace_metadata_records"))
}

func TestScaleP21SW183PublicationActualAdmission(t *testing.T) {
	for _, reload := range []bool{false, true} {
		name := "warm"
		if reload {
			name = "reloaded"
		}
		t.Run(name, func(t *testing.T) {
			f := scaleP21SW95Setup(t, reload)
			before := scaleP21SW183Before(t, f)
			candidate, _ := scaleP21SW95Candidate(t, f)
			owned := true
			defer func() {
				if owned {
					candidate.Release()
				}
			}()
			native, nativeCost, err := f.view.metadataLogicalPolicy(t.Context(), candidate)
			if err != nil {
				t.Fatal(err)
			}
			f.view.writeMu.Lock()
			defer f.view.writeMu.Unlock()
			tx := scaleP21SW76Begin(t, f.f)
			scaleP21SW183Stage(t, f, tx)
			probe := &SQLiteStore{db: f.view.db, dialect: f.view.dialect, metadataResidency: newMetadataResidency()}
			probe.metadataResidency.requireCoverage = false
			canonical, canonicalStamp, canonicalCost, err := probe.readResidentWorkspace(t.Context(), tx, f.workspace)
			if err != nil || canonicalStamp.workspaceID != f.base.workspaceID {
				t.Fatal("canonical policy fixture could not be read", err)
			}
			if reflect.DeepEqual(canonical.Viewer, native.Viewer) {
				t.Fatal("existing actor image no longer distinguishes native policy from canonical JSON")
			}
			const held = 100 << 20
			_, releaseHeld, err := f.view.metadataResidency.work.acquire(t.Context(), held)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseHeld()
			if held+metadataPolicyBytes+(2<<20) <= f.view.metadataResidency.work.limit {
				t.Fatal("headroom fixture would not reject the old fixed reservation")
			}
			if used := f.view.MetadataWorkUsedForTesting(); used != held {
				t.Fatal("publication fixture retained unrelated work", used)
			}
			rootTransfers, rowTransfers, peak := 0, 0, 0
			ctx := WithSQLStatementObserver(t.Context(), func(query string) {
				used := f.view.MetadataWorkUsedForTesting() - held
				peak = max(peak, used)
				if !scaleP21SW183PolicyTransfer(query) {
					return
				}
				if used <= 0 {
					t.Error("canonical policy data transfer preceded work admission")
				}
				if strings.Contains(query, "FROM workspace_states") {
					rootTransfers++
				} else {
					rowTransfers++
				}
			})
			publication, err := f.view.prepareLegacyResidentPublication(ctx, tx, f.workspace, candidate, f.base, true, false)
			if err != nil || publication == nil {
				t.Fatal("small publication falsely exhausted work with 100 MiB pre-held", err)
			}
			owned = false
			defer publication.Close()
			retained := f.view.MetadataWorkUsedForTesting() - held
			peak = max(peak, retained)
			if rootTransfers == 0 || rowTransfers == 0 {
				t.Fatal("admission witness missed canonical root or split-policy transfer", rootTransfers, rowTransfers)
			}
			if retained < nativeCost || publication.cost != nativeCost || !reflect.DeepEqual(publication.policy, native) {
				t.Fatal("retained native policy lost its own admission or canonical/native distinction", retained, nativeCost)
			}
			scaleP21SW95Unchanged(t, f, before.policy, before.costs, before.stamp)
			if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("uncommitted publication installed", err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := publication.Install(); err != nil {
				t.Fatal(err)
			}
			if used := f.view.MetadataWorkUsedForTesting(); used != held+retained {
				t.Fatal("Install released publication admission before its owner closed", used)
			}
			installed, found := f.view.storedWorkspace(f.workspace)
			if !found || !reflect.DeepEqual(installed.Viewer, native.Viewer) || !reflect.DeepEqual(installed.TeamSettings, native.TeamSettings) {
				t.Fatal("Install substituted canonical policy for the native candidate")
			}
			publication.Close()
			publication.Close()
			if used := f.view.MetadataWorkUsedForTesting(); used != held {
				t.Fatal("publication close released competing work or retained its own", used)
			}
			releaseHeld()
			if f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("publication admission leaked work or decoded full metadata")
			}
			t.Logf("canonical_policy_cost=%d native_policy_cost=%d publication_peak=%d retained=%d held=%d", canonicalCost, nativeCost, peak, retained, held)
		})
	}
}

func TestScaleP21SW183PublicationAdmissionFailureOwnership(t *testing.T) {
	for _, mode := range []string{"work-limit", "cancel-before", "cancel-during-probe", "rollback", "cancel-after-prepare-rollback"} {
		t.Run(mode, func(t *testing.T) {
			f := scaleP21SW95Setup(t, false)
			before := scaleP21SW183Before(t, f)
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			candidate, expected := scaleP21SW95Candidate(t, f)
			owned := true
			defer func() {
				if owned {
					candidate.Release()
				}
				if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Error("failed publication retained logical storage or descriptors")
				}
			}()
			f.view.writeMu.Lock()
			defer f.view.writeMu.Unlock()
			tx := scaleP21SW76Begin(t, f.f)
			scaleP21SW183Stage(t, f, tx)
			staged := scaleP21SW76MetadataImage(t, tx, f.workspace)
			held := 100 << 20
			if mode == "work-limit" {
				held = 0
			}
			releaseHeld := func() {}
			if held != 0 {
				var err error
				_, releaseHeld, err = f.view.metadataResidency.work.acquire(t.Context(), held)
				if err != nil {
					t.Fatal(err)
				}
			}
			defer releaseHeld()
			limit := f.view.metadataResidency.work.limit
			defer func() { f.view.metadataResidency.work.limit = limit }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			transfers := 0
			ctx = WithSQLStatementObserver(ctx, func(query string) {
				if scaleP21SW183PolicyTransfer(query) {
					transfers++
					if mode == "cancel-during-probe" {
						if f.view.MetadataWorkUsedForTesting() <= held {
							t.Error("canceled policy transfer lacked admission")
						}
						cancel()
					}
				}
			})
			switch mode {
			case "work-limit":
				f.view.metadataResidency.work.limit = 1
			case "cancel-before":
				cancel()
			}
			publication, err := f.view.prepareLegacyResidentPublication(ctx, tx, f.workspace, candidate, f.base, true, true)
			f.view.metadataResidency.work.limit = limit
			prepared := mode == "rollback" || mode == "cancel-after-prepare-rollback"
			if prepared {
				if err != nil || publication == nil {
					t.Fatal("rollback fixture could not prepare with actual headroom", err)
				}
				owned = false
				defer publication.Close()
				admitted := f.view.MetadataWorkUsedForTesting()
				if admitted <= held {
					t.Fatal("prepared policy has no retained admission")
				}
				if mode == "cancel-after-prepare-rollback" {
					cancel()
					if f.view.MetadataWorkUsedForTesting() != admitted {
						t.Fatal("cancellation released retained publication values early")
					}
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if !errors.Is(publication.Install(), ErrBoundedMetadataCoverage) {
					t.Fatal("rolled-back publication installed policy")
				}
				publication.Close()
				publication.Close()
			} else {
				want := error(context.Canceled)
				if mode == "work-limit" {
					want = ErrMetadataWorkBudget
				}
				if publication != nil {
					owned = false
					publication.Close()
					t.Fatal("failed preparation returned owned output")
				}
				if !errors.Is(err, want) {
					t.Fatal("publication refusal changed", err, want)
				}
				if mode == "cancel-during-probe" && transfers == 0 {
					t.Fatal("cancellation did not reach an admitted policy transfer")
				}
				if mode == "work-limit" && transfers != 0 {
					t.Fatal("work rejection occurred after canonical data transfer")
				}
				if got := scaleP21SW76MetadataImage(t, tx, f.workspace); !reflect.DeepEqual(got, staged) {
					t.Fatal("failed preparation changed caller transaction metadata")
				}
				scaleP21SW58AssertDiscussionRows(t, tx, f.workspace, true)
				scaleP21SW70Usable(t, tx)
				data, selectErr := candidate.Select(t.Context(), MetadataRecordScope{Fields: []string{"users"}}, 2<<20)
				if selectErr != nil || !reflect.DeepEqual(data.Viewer, expected.Viewer) {
					t.Fatal("failed preparation consumed caller candidate", selectErr)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			scaleP21SW95Unchanged(t, f, before.policy, before.costs, before.stamp)
			scaleP21SW58AssertDiscussionRows(t, f.f.db, f.workspace, false)
			if used := f.view.MetadataWorkUsedForTesting(); used != held {
				t.Fatal("publication failure leaked work or released its competitor", used, held)
			}
			releaseHeld()
			if f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failed publication leaked work or decoded full metadata")
			}
		})
	}
}

func TestScaleP21SW183PublicationPredecodeAdmission(t *testing.T) {
	f := scaleP21SW95Setup(t, false)
	candidate, _ := scaleP21SW95Candidate(t, f)
	defer candidate.Release()
	f.view.writeMu.Lock()
	defer f.view.writeMu.Unlock()
	tx := scaleP21SW76Begin(t, f.f)
	var raw []byte
	if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, f.workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	root, _, err := residentMetadataRoot(raw)
	if err != nil {
		t.Fatal(err)
	}
	var typedRoot domain.Bootstrap
	if err := json.Unmarshal(root, &typedRoot); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"physical-root", "typed-root", "first-row", "native-policy"} {
		t.Run(stage, func(t *testing.T) {
			owner := &ResourceRead{store: f.view}
			defer owner.Close()
			denied := errors.New("sw183 denied before materialization")
			calls, stop := 0, 1
			switch stage {
			case "typed-root":
				stop = 2
			case "first-row":
				stop = 3
			}
			reserve := func(cost uint64) error {
				calls++
				if calls == stop {
					return denied
				}
				return owner.reserve(t.Context(), cost)
			}
			var data domain.Bootstrap
			var err error
			if stage == "native-policy" {
				data, _, err = f.view.metadataLogicalPolicyAdmitted(t.Context(), candidate, reserve)
			} else {
				probe := &SQLiteStore{db: f.view.db, dialect: f.view.dialect, metadataResidency: newMetadataResidency()}
				probe.metadataResidency.requireCoverage = false
				probe.metadataResidency.cacheLimit = 0
				data, _, _, err = probe.readResidentWorkspaceAdmitted(t.Context(), tx, f.workspace, reserve)
			}
			if !errors.Is(err, denied) || calls != stop {
				t.Fatal("materialization did not honor its preceding admission", err, calls, stop)
			}
			if stage == "first-row" {
				if !reflect.DeepEqual(data, typedRoot) {
					t.Fatal("first policy row was appended before its admission")
				}
			} else if !reflect.DeepEqual(data, domain.Bootstrap{}) {
				t.Fatal("denied root/native decode exposed materialized policy")
			}
			owner.Close()
			if f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("predecode refusal retained earlier incremental reservations")
			}
		})
	}
}

func TestScaleP21SW183PublicationInvalidUTF8DirectoryAdmission(t *testing.T) {
	f := scaleP21SW95Setup(t, false)
	key := strings.Repeat("A\xff", 2048)
	if err := f.f.MutateWorkspace(WithMetadataFields(t.Context(), "teams"), f.workspace, "scale_p21.sw183_team_key", "", nil, func(data *domain.Bootstrap) error {
		data.Teams[0].Key = key
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.view.RefreshInstalledMetadataViewForTesting(t.Context(), f.f.SQLiteStore, f.workspace); err != nil {
		t.Fatal(err)
	}
	var found bool
	f.native, found = f.f.WorkspaceMetadata(f.workspace)
	if !found || f.native.Teams[0].Key != key {
		t.Fatal("typed team producer did not preserve the native key")
	}
	var err error
	f.base, err = readMetadataStamp(t.Context(), f.f.db, f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	f.prior, err = f.view.retainMetadataLogicalSource(t.Context(), f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer f.prior.Release()
	candidate, _ := scaleP21SW95Candidate(t, f)
	defer candidate.Release()
	owner := &ResourceRead{store: f.view}
	defer owner.Close()
	var admissions []uint64
	policy, _, err := f.view.metadataLogicalPolicyAdmitted(t.Context(), candidate, func(cost uint64) error {
		admissions = append(admissions, cost)
		return owner.reserve(t.Context(), cost)
	})
	if err != nil || len(admissions) != 2 {
		t.Fatal("native policy did not separately admit its derived directory", err, admissions)
	}
	mapped := strings.ToLower(key)
	if len(mapped) <= len(key) || policy.Teams[0].Key != key || policy.TeamByKey[mapped] != policy.Teams[0].ID {
		t.Fatal("native invalid-UTF8 key or expanded directory identity changed")
	}
	ascii := policy
	ascii.Teams = append([]domain.Team(nil), policy.Teams...)
	ascii.Teams[0].Key = strings.Repeat("a", len(key))
	var asciiCost uint64
	if err := reserveMetadataPolicyDirectory(t.Context(), ascii, func(cost uint64) error {
		asciiCost = cost
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if admissions[1] < asciiCost+uint64(len(mapped)-len(key)) {
		t.Fatal("derived directory admission ignored invalid-UTF8 key expansion", admissions[1], asciiCost)
	}
	owner.Close()
	if f.view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("native directory admission leaked work")
	}
	t.Logf("native_key_bytes=%d mapped_key_bytes=%d directory_work=%d ascii_directory_work=%d", len(key), len(mapped), admissions[1], asciiCost)
}

func TestScaleP21SW183PublicationLegacyInlineTeams(t *testing.T) {
	for _, shape := range []string{"duplicate-id", "empty-id", "long-id"} {
		t.Run(shape, func(t *testing.T) {
			f := scaleP21SW95Setup(t, false)
			if err := f.f.MutateWorkspace(WithMetadataFields(t.Context(), "teams"), f.workspace, "scale_p21.sw183_inline_teams", "", nil, func(data *domain.Bootstrap) error {
				team := domain.Team{Key: "SW183INLINE", Name: "Legal legacy inline team"}
				switch shape {
				case "duplicate-id":
					team.ID = data.Teams[0].ID
				case "long-id":
					team.ID = strings.Repeat("long", 48)
				}
				data.Teams = append(data.Teams, team)
				return nil
			}); err != nil {
				t.Fatal("legacy team producer rejected its inline-compatible identity", err)
			}
			if err := f.view.RefreshInstalledMetadataViewForTesting(t.Context(), f.f.SQLiteStore, f.workspace); err != nil {
				t.Fatal(err)
			}
			var found bool
			f.native, found = f.f.WorkspaceMetadata(f.workspace)
			if !found {
				t.Fatal("legacy producer lost its native workspace")
			}
			var err error
			f.base, err = readMetadataStamp(t.Context(), f.f.db, f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			f.prior, err = f.view.retainMetadataLogicalSource(t.Context(), f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer f.prior.Release()
			before := scaleP21SW183Before(t, f)
			candidate, _ := scaleP21SW95Candidate(t, f)
			owned := true
			defer func() {
				if owned {
					candidate.Release()
				}
			}()
			native, nativeCost, err := f.view.metadataLogicalPolicy(t.Context(), candidate)
			if err != nil {
				t.Fatal(err)
			}
			f.view.writeMu.Lock()
			defer f.view.writeMu.Unlock()
			tx := scaleP21SW76Begin(t, f.f)
			scaleP21SW183Stage(t, f, tx)
			var raw []byte
			if err := tx.QueryRowContext(t.Context(), `SELECT data FROM workspace_states WHERE workspace_key=?`, f.workspace).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var root map[string]json.RawMessage
			if err := json.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			var shapes map[string]string
			if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
				t.Fatal(err)
			}
			expectedTeams, err := json.Marshal(f.native.Teams)
			if err != nil || shapes["teams"] != "" || !bytes.Equal(root["teams"], expectedTeams) {
				t.Fatal("real splitter did not preserve the entire literal team array inline", err)
			}
			if _, _, err := residentMetadataRoot(raw); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("strict resident root accepted legacy-only inline policy", err)
			}
			probe := &SQLiteStore{db: f.view.db, dialect: f.view.dialect, metadataResidency: newMetadataResidency()}
			probe.metadataResidency.requireCoverage = false
			if _, _, _, err := probe.readResidentWorkspace(t.Context(), tx, f.workspace); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("strict resident recovery accepted legacy-only inline policy", err)
			}
			limit := f.view.metadataResidency.work.limit
			f.view.metadataResidency.work.limit = 1
			rejected, err := f.view.prepareLegacyResidentPublication(t.Context(), tx, f.workspace, candidate, f.base, true, false)
			f.view.metadataResidency.work.limit = limit
			if rejected != nil {
				owned = false
				rejected.Close()
				t.Fatal("unadmitted inline publication returned owned output")
			}
			if !errors.Is(err, ErrMetadataWorkBudget) || f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("inline publication quota refusal leaked admission", err)
			}
			scaleP21SW95Unchanged(t, f, before.policy, before.costs, before.stamp)
			const held = 100 << 20
			_, releaseHeld, err := f.view.metadataResidency.work.acquire(t.Context(), held)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseHeld()
			publication, err := f.view.prepareLegacyResidentPublication(t.Context(), tx, f.workspace, candidate, f.base, true, false)
			if err != nil || publication == nil {
				t.Fatal("publication rejected legal legacy inline team identities", err)
			}
			owned = false
			defer publication.Close()
			if publication.cost != nativeCost || !reflect.DeepEqual(publication.policy, native) ||
				!reflect.DeepEqual(publication.policy.Teams, f.native.Teams) || f.view.MetadataWorkUsedForTesting() <= held {
				t.Fatal("inline publication lost native rows, order or retained policy admission")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := publication.Install(); err != nil {
				t.Fatal(err)
			}
			resident, found := f.view.storedWorkspace(f.workspace)
			if !found || !reflect.DeepEqual(resident.Teams, f.native.Teams) || !reflect.DeepEqual(resident.Viewer, native.Viewer) {
				t.Fatal("inline publication changed the installed native policy")
			}
			publication.Close()
			publication.Close()
			if f.view.MetadataWorkUsedForTesting() != held {
				t.Fatal("inline publication released competing work or leaked its reservation")
			}
			releaseHeld()
			if f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("inline publication leaked work or hydrated complete metadata")
			}
			t.Logf("inline_team_rows=%d native_policy_cost=%d held=%d", len(f.native.Teams), nativeCost, held)
		})
	}
}
