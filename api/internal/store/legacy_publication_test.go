package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

type scaleP21SW95Fixture struct {
	f         scaleP09Fixture
	view      *SQLiteStore
	prior     *metadataLogicalSource
	native    domain.Bootstrap
	base      copyStamp
	workspace string
	other     string
}

func scaleP21SW95Setup(t *testing.T, reload bool) scaleP21SW95Fixture {
	t.Helper()
	f, _, _ := scaleP21SW27WarmLogicalFixture(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "users"), workspace, "scale_p21.sw95_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Viewer.Name = "SW95 resident preimage"
		data.Users = []domain.User{data.Viewer, {ID: "sw95-untouched-user", Name: "Untouched directory actor", AppScopes: []string{}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if reload {
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	native, found := f.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("native publication fixture is missing")
	}
	base, err := readMetadataStamp(t.Context(), f.db, workspace)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.CreateWorkspace(ContextWithActor(t.Context(), f.data.Viewer), "SW95 other policy", idgen.New("sw95-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.DeleteWorkspace(context.Background(), other.Workspace.URLKey); err != nil {
			t.Error(err)
		}
	})
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, other.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(view.releaseMetadataLogicalSources)
	prior, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(prior.Release)
	view.mu.Lock()
	view.lastWorkspaceKey = other.Workspace.URLKey
	view.mu.Unlock()
	f.db.SetMaxOpenConns(1)
	return scaleP21SW95Fixture{f, view, prior, native, base, workspace, other.Workspace.URLKey}
}

func scaleP21SW95Actor(f scaleP21SW95Fixture) domain.User {
	actor := scaleP21SW69OwnedActor(f.native.Viewer)
	actor.Name = "SW95 native\x00actor"
	actor.AppScopes, actor.AppTeamIDs = []string{}, nil
	at := time.Date(2026, 11, 13, 12, 34, 56, 789, time.UTC)
	actor.OutOfOfficeUntil = &at
	actor.Via = &domain.ActorBot{ID: "sw95-native-app", Name: "Native actor provenance"}
	return actor
}

func scaleP21SW95Candidate(t *testing.T, f scaleP21SW95Fixture) (*metadataLogicalSource, domain.Bootstrap) {
	t.Helper()
	actor := scaleP21SW95Actor(f)
	next := scaleP21SW69LiteralActorStep(cloneBootstrap(f.native), &actor)
	image, err := f.view.prepareLegacyActorImage(t.Context(), f.prior, &actor)
	if err != nil {
		t.Fatal(err)
	}
	candidate := image.Source()
	if err := candidate.Retain(); err != nil {
		image.Close()
		t.Fatal(err)
	}
	image.Close()
	return candidate, next
}

func scaleP21SW95Unchanged(t *testing.T, f scaleP21SW95Fixture, policy domain.Bootstrap, costs map[string]int, stamp copyStamp) {
	t.Helper()
	scaleP21SW58AssertInstalled(t, f.view, f.workspace, f.prior, true).Release()
	current, found := f.view.storedWorkspace(f.workspace)
	if !found || !reflect.DeepEqual(current, policy) {
		t.Fatal("publication changed resident policy before successful Install")
	}
	f.view.metadataResidency.mu.Lock()
	defer f.view.metadataResidency.mu.Unlock()
	total := 0
	for _, cost := range costs {
		total += cost
	}
	if !reflect.DeepEqual(f.view.metadataResidency.policy, costs) || f.view.metadataResidency.policyBytes != total ||
		f.view.metadataResidency.stamps[f.workspace] != stamp || f.view.lastWorkspaceKey != f.other {
		t.Fatal("publication changed resident policy charges or commit stamp before Install")
	}
}

func TestScaleP21SW95LegacyResidentPublication(t *testing.T) {
	for _, test := range []struct {
		name                string
		reload, exact, last bool
	}{
		{"warm-exact-keep-default", false, true, false},
		{"warm-unknown-set-default", false, false, true},
		{"reload-exact-set-default", true, true, true},
		{"reload-unknown-keep-default", true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := scaleP21SW95Setup(t, test.reload)
			policy, _ := f.view.storedWorkspace(f.workspace)
			policy = cloneBootstrap(policy)
			domain.RebuildTeamDirectory(&policy)
			otherPolicy, _ := f.view.storedWorkspace(f.other)
			otherPolicy = cloneBootstrap(otherPolicy)
			domain.RebuildTeamDirectory(&otherPolicy)
			otherSource, err := f.view.retainMetadataLogicalSource(t.Context(), f.other)
			if err != nil {
				t.Fatal(err)
			}
			defer otherSource.Release()
			costs := map[string]int{}
			f.view.metadataResidency.mu.Lock()
			for key, value := range f.view.metadataResidency.policy {
				costs[key] = value
			}
			oldStamp := f.view.metadataResidency.stamps[f.workspace]
			f.view.metadataResidency.mu.Unlock()
			actor := scaleP21SW95Actor(f)
			candidate, expected := scaleP21SW95Candidate(t, f)
			owned := true
			defer func() {
				if owned {
					candidate.Release()
				}
			}()
			_, expectedCost, err := f.view.metadataLogicalPolicy(t.Context(), candidate)
			if err != nil {
				t.Fatal(err)
			}
			f.view.writeMu.Lock()
			defer f.view.writeMu.Unlock()
			tx := scaleP21SW76Begin(t, f.f)
			image, err := f.view.persistLegacyActorImage(t.Context(), tx, f.workspace, f.prior, &actor)
			if err != nil {
				t.Fatal(err)
			}
			image.Close()
			scaleP21SW58StageDiscussionRows(t, f.f, tx)
			publication, err := f.view.prepareLegacyResidentPublication(t.Context(), tx, f.workspace, candidate, f.base, test.exact, test.last)
			if err != nil || publication == nil {
				t.Fatal("native resident publication preparation failed", err)
			}
			owned = false
			defer publication.Close()
			if publication.cost != expectedCost || publication.policy.ViewerRole != expected.ViewerRole ||
				!reflect.DeepEqual(publication.policy.Viewer, actor) || f.view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("preparation lost native policy or its retained admission")
			}
			if err := candidate.setStamp(candidate.Stamp()); err != nil {
				t.Fatal("preparation published an extra candidate reference", err)
			}
			if err := publication.Install(); !errors.Is(err, ErrBoundedMetadataCoverage) {
				t.Fatal("uncommitted publication installed", err)
			}
			scaleP21SW95Unchanged(t, f, policy, costs, oldStamp)
			scaleP21SW58AssertDiscussionRows(t, tx, f.workspace, true)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			final, err := readMetadataStamp(t.Context(), f.f.db, f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			if final.workspaceID != f.base.workspaceID || final.revision <= f.base.revision {
				t.Fatal("publication fixture did not capture the actual advanced commit stamp", f.base, final)
			}
			scaleP21SW95Unchanged(t, f, policy, costs, oldStamp)
			if err := publication.Install(); err != nil {
				t.Fatal(err)
			}
			if publication.publication.candidate != nil {
				t.Fatal("Install retained transferred candidate ownership")
			}
			publication.Close()
			publication.Close()
			if publication.owner != nil || !reflect.DeepEqual(publication.policy, domain.Bootstrap{}) ||
				!errors.Is(publication.Install(), ErrBoundedMetadataCoverage) || f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("publication release retained policy, source, or temporary admission")
			}
			installed, err := f.view.retainMetadataLogicalSource(t.Context(), f.workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer installed.Release()
			wantStamp := final
			if !test.exact {
				wantStamp.revision = -1
			}
			if installed != candidate || installed.Stamp() != wantStamp {
				t.Fatal("committed source identity or exactness changed", installed.Stamp(), wantStamp)
			}
			resident, found := f.view.storedWorkspace(f.workspace)
			if !found || resident.ViewerRole != expected.ViewerRole || !reflect.DeepEqual(resident.Viewer, actor) {
				t.Fatal("resident actor policy was rebuilt from canonical SQL instead of native candidate")
			}
			// Reading again without another commit must see the same native
			// actor/role in policy and logical source at this committed revision.
			for range 2 {
				read, found, err := f.view.BeginInstalledMetadataRead(t.Context(), f.workspace)
				if err != nil || !found {
					t.Fatal(err)
				}
				selection, err := read.Select(t.Context(), MetadataRecordScope{Fields: []string{"users", "teams", "teamSettings", "teamParents", "workspaceSettings", "settings"}})
				if err != nil {
					read.Close()
					t.Fatal(err)
				}
				if !reflect.DeepEqual(selection.Data.Viewer, resident.Viewer) || selection.Data.ViewerRole != resident.ViewerRole ||
					!reflect.DeepEqual(selection.Data.Users, expected.Users) || !reflect.DeepEqual(selection.Data.Teams, resident.Teams) ||
					!reflect.DeepEqual(selection.Data.Settings, expected.Settings) {
					t.Fatal("same-version native policy and logical source disagree")
				}
				selection.Close()
				read.Close()
			}
			f.view.metadataResidency.mu.Lock()
			total := 0
			for _, cost := range f.view.metadataResidency.policy {
				total += cost
			}
			charged, stamp := f.view.metadataResidency.policy[f.workspace], f.view.metadataResidency.stamps[f.workspace]
			otherCost, totalCost := f.view.metadataResidency.policy[f.other], f.view.metadataResidency.policyBytes
			f.view.metadataResidency.mu.Unlock()
			wantDefault := f.other
			if test.last {
				wantDefault = f.workspace
			}
			if charged != expectedCost || otherCost != costs[f.other] || totalCost != total ||
				stamp != final || f.view.lastWorkspaceKey != wantDefault {
				t.Fatal("publication changed policy accounting, final commit stamp, or default namespace")
			}
			defaultRead, found, err := f.view.BeginInstalledMetadataRead(t.Context(), "")
			if err != nil || !found {
				t.Fatal(err)
			}
			if defaultRead.workspace != wantDefault {
				t.Fatal("default namespace did not select the installed policy/source pair")
			}
			defaultRead.Close()
			otherCurrent, _ := f.view.storedWorkspace(f.other)
			otherInstalled, err := f.view.retainMetadataLogicalSource(t.Context(), f.other)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(otherCurrent, otherPolicy) || otherInstalled != otherSource {
				t.Fatal("publication replaced another workspace's native policy/source")
			}
			otherInstalled.Release()
			scaleP21SW58AssertDiscussionRows(t, f.f.db, f.workspace, true)
			before, err := f.prior.Select(t.Context(), MetadataRecordScope{Fields: []string{"users", "settings"}}, 2<<20)
			if err != nil || !reflect.DeepEqual(before.Viewer, f.native.Viewer) || !reflect.DeepEqual(before.Users, f.native.Users) {
				t.Fatal("Install mutated retained native preimage", err)
			}
			if f.view.WorkspaceMetadataDecodes() != 0 || f.view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("publication hydrated full metadata or leaked temporary work")
			}
		})
	}
}

func TestScaleP21SW95LegacyResidentPublicationFailures(t *testing.T) {
	f := scaleP21SW95Setup(t, false)
	policy, _ := f.view.storedWorkspace(f.workspace)
	policy = cloneBootstrap(policy)
	domain.RebuildTeamDirectory(&policy)
	costs := map[string]int{}
	f.view.metadataResidency.mu.Lock()
	for key, value := range f.view.metadataResidency.policy {
		costs[key] = value
	}
	stamp := f.view.metadataResidency.stamps[f.workspace]
	f.view.metadataResidency.mu.Unlock()
	for _, mode := range []string{
		"cancel-before", "cancel-after-start", "work-quota", "aggregate-policy-quota",
		"foreign-transaction", "foreign-candidate", "foreign-root", "canonical-binding",
		"shared-candidate", "rollback", "commit-rejected",
	} {
		t.Run(mode, func(t *testing.T) {
			disk, descriptors := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load()
			candidate, expected := scaleP21SW95Candidate(t, f)
			if mode == "foreign-candidate" || mode == "foreign-root" {
				beforeRoot := domain.Bootstrap{Workspace: expected.Workspace}
				expected.Workspace.ID = "sw95-foreign-workspace"
				foreignStamp := f.base
				if mode == "foreign-candidate" {
					foreignStamp.workspaceID = expected.Workspace.ID
				}
				foreign, err := candidate.Fork(t.Context(), beforeRoot, domain.Bootstrap{Workspace: expected.Workspace}, MetadataRecordScope{}, foreignStamp)
				if err != nil {
					candidate.Release()
					t.Fatal(err)
				}
				candidate.Release()
				candidate = foreign
			}
			owned, shared := true, mode == "shared-candidate"
			defer func() {
				if shared {
					candidate.Release()
				}
				if owned {
					candidate.Release()
				}
				if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
					t.Error("failed publication retained candidate storage/descriptors")
				}
			}()
			if shared {
				if err := candidate.Retain(); err != nil {
					t.Fatal(err)
				}
			}
			f.view.writeMu.Lock()
			defer f.view.writeMu.Unlock()
			tx := scaleP21SW76Begin(t, f.f)
			scaleP21SW58StageDiscussionRows(t, f.f, tx)
			imageBefore := scaleP21SW76MetadataImage(t, tx, f.workspace)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := error(ErrMetadataRecordQuery)
			if mode == "cancel-before" {
				cancel()
				want = context.Canceled
			}
			if mode == "cancel-after-start" {
				ctx = WithSQLStatementObserver(ctx, func(query string) {
					if len(query) > 0 {
						cancel()
					}
				})
				want = context.Canceled
			}
			workLimit, policyLimit := f.view.metadataResidency.work.limit, f.view.metadataResidency.policyLimit
			if mode == "work-quota" {
				f.view.metadataResidency.work.limit, want = 1, ErrMetadataWorkBudget
			}
			if mode == "aggregate-policy-quota" {
				_, cost, err := f.view.metadataLogicalPolicy(t.Context(), candidate)
				if err != nil {
					t.Fatal(err)
				}
				f.view.metadataResidency.policyLimit, want = cost, ErrMetadataWorkBudget
			}
			defer func() {
				f.view.metadataResidency.work.limit, f.view.metadataResidency.policyLimit = workLimit, policyLimit
			}()
			usedTx, workspace := tx, f.workspace
			if mode == "foreign-transaction" {
				pool, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer pool.Close()
				foreign := &sqlDatabase{DB: pool, dialect: "sqlite"}
				usedTx, err = foreign.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer usedTx.Rollback()
				want = ErrBoundedMetadataCoverage
			}
			if mode == "canonical-binding" {
				workspace = f.other
			}
			publication, err := f.view.prepareLegacyResidentPublication(ctx, usedTx, workspace, candidate, f.base, true, true)
			f.view.metadataResidency.work.limit, f.view.metadataResidency.policyLimit = workLimit, policyLimit
			prepared := mode == "rollback" || mode == "commit-rejected"
			if !prepared {
				if publication != nil {
					owned = false
					publication.Close()
					t.Fatal("failed preparation exposed a publication")
				}
				if !errors.Is(err, want) {
					t.Fatal("publication preparation error changed", mode, err)
				}
				before, selectErr := candidate.Select(t.Context(), MetadataRecordScope{Fields: []string{"users", "settings"}}, 2<<20)
				if selectErr != nil || !reflect.DeepEqual(before.Users, expected.Users) || !reflect.DeepEqual(before.Viewer, expected.Viewer) {
					t.Fatal("failed preparation consumed or mutated caller-owned candidate", selectErr)
				}
			} else {
				if err != nil || publication == nil {
					t.Fatal(err)
				}
				owned = false
				defer publication.Close()
				if mode == "rollback" {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				} else {
					rejected := errors.New("sw95 rejected prepared commit")
					tx.writeErr = rejected
					if err := tx.Commit(); !errors.Is(err, rejected) {
						t.Fatal("publication changed commit rejection", err)
					}
				}
				if !errors.Is(publication.Install(), ErrBoundedMetadataCoverage) {
					t.Fatal("rolled-back or rejected transaction installed policy/source")
				}
				publication.Close()
			}
			scaleP21SW95Unchanged(t, f, policy, costs, stamp)
			if !prepared {
				if got := scaleP21SW76MetadataImage(t, tx, f.workspace); !reflect.DeepEqual(got, imageBefore) {
					t.Fatal("preparation changed caller transaction metadata")
				}
				scaleP21SW58AssertDiscussionRows(t, tx, f.workspace, true)
				scaleP21SW70Usable(t, tx)
				if err := tx.Rollback(); err != nil {
					t.Fatal("preparation consumed caller transaction", err)
				}
			}
			scaleP21SW58AssertDiscussionRows(t, f.f.db, f.workspace, false)
			if f.view.lastWorkspaceKey != f.other || f.view.MetadataWorkUsedForTesting() != 0 || f.view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failed publication changed default namespace or leaked/hydrated metadata")
			}
		})
	}
}
