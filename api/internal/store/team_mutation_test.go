package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func bulkTeamSettings(id string) domain.TeamSettings {
	return domain.TeamSettings{TeamID: id, Timezone: "Etc/UTC", EstimateType: "notUsed", Access: "public",
		MembershipRestriction: "open", SettingsPermission: "allMembers", LabelPermission: "allMembers",
		TemplatePermission: "allMembers", AgentSkillPermission: "allMembers", LoopPermission: "allMembers",
		MemberPermission: "allMembers", SlackNotifications: map[string]bool{}, PRAutomations: map[string]string{},
		StaleMonths: 6, AutoArchiveMonths: 6, ProgressOrder: "first", TriageAction: "none",
		ReleaseAutomations: []domain.TeamAutomationRule{}, TriageRules: []domain.TeamAutomationRule{},
		AgentSkills: []domain.TeamAgentSkill{}, ResolvedSummaries: true, ShowInitiatives: true}
}

// seedBulkTeams grows a workspace to the requested size through the ordinary
// mutation path so tests can measure the scoped team path at scale.
func seedBulkTeams(tb testing.TB, repo *SQLiteStore, teams int) string {
	tb.Helper()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	baseStates := make([]domain.WorkflowState, 0, len(data.States))
	for _, state := range data.States {
		if state.TeamID == "" {
			baseStates = append(baseStates, state)
		}
	}
	extraTeams := make([]domain.Team, 0, teams)
	states := make([]domain.WorkflowState, 0, teams*len(baseStates))
	settings := make(map[string]domain.TeamSettings, teams)
	cycles := make(map[string]domain.CycleSettings, teams)
	for i := 0; i < teams; i++ {
		id := fmt.Sprintf("bulk-team-%05d", i)
		extraTeams = append(extraTeams, domain.Team{ID: id, Name: id, Key: fmt.Sprintf("BT%04d", i)})
		settings[id] = bulkTeamSettings(id)
		cycles[id] = domain.CycleSettings{DurationWeeks: 2, StartsOn: 1, UpcomingCount: 2, Capacity: 4}
		for j, state := range baseStates {
			clone := state
			clone.ID = fmt.Sprintf("%s-state-%d", id, j)
			clone.TeamID = id
			states = append(states, clone)
		}
	}
	err := repo.MutateWorkspace(ctx, key, "test.bulk_teams", "seed", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, extraTeams...)
		for id, value := range settings {
			next.TeamSettings[id] = value
		}
		for id, value := range cycles {
			next.CycleSettings[id] = value
		}
		next.States = append(next.States, states...)
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return key
}

func createScopedTeam(t *testing.T, repo *SQLiteStore, workspace, id string) {
	t.Helper()
	err := repo.MutateWorkspace(context.Background(), workspace, "team.created", id, nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: id, Name: id, Key: id})
		next.TeamSettings[id] = bulkTeamSettings(id)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func patchTeamParent(t *testing.T, repo *SQLiteStore, workspace, teamID, parentID string) {
	t.Helper()
	err := repo.MutateWorkspace(context.Background(), workspace, "team.settings_updated", teamID, map[string]string{"parentTeamId": parentID}, func(next *domain.Bootstrap) error {
		settings := next.TeamSettings[teamID]
		settings.ParentTeamID = parentID
		next.TeamSettings[teamID] = settings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTeamCreatedMutationWritesOnlyChangedRecords(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := seedBulkTeams(t, repo, 120)
	writes := auditWrites(t, repo)
	createScopedTeam(t, repo, key, "scoped-team")
	if changes := writes(); !reflect.DeepEqual(changes, map[string]int{"workspace_metadata_records": 2}) {
		t.Fatalf("team created amplified writes: %+v", changes)
	}
	metadata, _ := repo.WorkspaceMetadata(key)
	if len(metadata.Teams) != 122 || metadata.Teams[len(metadata.Teams)-1].ID != "scoped-team" {
		t.Fatal("created team missing from the snapshot")
	}
	if err := repo.ReloadAllWorkspaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reloaded := repo.Bootstrap(); len(reloaded.Teams) != 122 {
		t.Fatal("reload lost the created team")
	}
}

func TestTeamSettingsFieldMutationWritesOneRecord(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := seedBulkTeams(t, repo, 120)
	child := "bulk-team-00000"
	writes := auditWrites(t, repo)
	err = repo.MutateWorkspace(context.Background(), key, "team.settings_updated", child, map[string]string{"timezone": "Asia/Shanghai"}, func(next *domain.Bootstrap) error {
		settings := next.TeamSettings[child]
		settings.Timezone = "Asia/Shanghai"
		next.TeamSettings[child] = settings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if changes := writes(); !reflect.DeepEqual(changes, map[string]int{"workspace_metadata_records": 1}) {
		t.Fatalf("settings patch amplified writes: %+v", changes)
	}
	if err := repo.ReloadAllWorkspaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.Bootstrap().TeamSettings[child].Timezone != "Asia/Shanghai" {
		t.Fatal("timezone was not persisted")
	}
}

func TestTeamSettingsNoopDoesNotWrite(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := seedBulkTeams(t, repo, 80)
	child := "bulk-team-00000"
	current := repo.Bootstrap().TeamSettings[child].Timezone
	writes := auditWrites(t, repo)
	err = repo.MutateWorkspace(context.Background(), key, "team.settings_updated", child, map[string]string{"timezone": current}, func(next *domain.Bootstrap) error {
		settings := next.TeamSettings[child]
		settings.Timezone = current
		next.TeamSettings[child] = settings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if changes := writes(); len(changes) != 0 {
		t.Fatalf("no-op settings patch wrote rows: %+v", changes)
	}
}

func TestTeamSettingsParentMutationWritesOneRecordAndSyncsAncestors(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	key := seedBulkTeams(t, repo, 120)
	data := repo.Bootstrap()
	child, parent := "bulk-team-00000", "bulk-team-00001"
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?)`,
		data.Workspace.ID, child, data.Viewer.ID, "member", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	err = repo.MutateWorkspace(ctx, key, "team.settings_updated", child, map[string]string{"parentTeamId": parent}, func(next *domain.Bootstrap) error {
		settings := next.TeamSettings[child]
		settings.ParentTeamID = parent
		next.TeamSettings[child] = settings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if changes := writes(); !reflect.DeepEqual(changes, map[string]int{"workspace_metadata_records": 1}) {
		t.Fatalf("parent patch amplified writes: %+v", changes)
	}
	var propagated int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`,
		data.Workspace.ID, parent, data.Viewer.ID).Scan(&propagated); err != nil {
		t.Fatal(err)
	}
	if propagated != 1 {
		t.Fatal("ancestor membership was not propagated")
	}
	if err := repo.ReloadAllWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
	reloaded := repo.Bootstrap()
	if reloaded.TeamSettings[child].ParentTeamID != parent {
		t.Fatal("reload lost the parent assignment")
	}
}

// Reparenting C must still see grandchild G; a wrong Teams scan in descendantTeamIDs drops G.
func TestTeamSettingsParentPatchKeepsDescendant(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	key := repo.Bootstrap().Workspace.URLKey
	parent, child, grandchild, other := "team-p", "team-c", "team-g", "team-t"
	createScopedTeam(t, repo, key, parent)
	createScopedTeam(t, repo, key, child)
	createScopedTeam(t, repo, key, grandchild)
	createScopedTeam(t, repo, key, other)
	patchTeamParent(t, repo, key, child, parent)
	patchTeamParent(t, repo, key, grandchild, child)
	data := repo.Bootstrap()
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?)`,
		data.Workspace.ID, grandchild, data.Viewer.ID, "member", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	writes := auditWrites(t, repo)
	patchTeamParent(t, repo, key, child, other)
	if changes := writes(); !reflect.DeepEqual(changes, map[string]int{"workspace_metadata_records": 1}) {
		t.Fatalf("parent patch amplified writes: %+v", changes)
	}
	live, _ := repo.WorkspaceMetadata(key)
	if live.TeamSettings[child].ParentTeamID != other || live.TeamSettings[grandchild].ParentTeamID != child {
		t.Fatal("parent patch lost the P/C/G chain")
	}
	if got := domain.TeamDescendantIDs(&live, child); !slices.Contains(got, grandchild) {
		t.Fatalf("TeamDescendantIDs(%s)=%v, want %s", child, got, grandchild)
	}
	if got := descendantTeamIDs(&live, child); !slices.Contains(got, grandchild) {
		t.Fatalf("descendantTeamIDs(%s)=%v, want %s", child, got, grandchild)
	}
	var propagated int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE workspace_id=? AND team_id=? AND user_id=?`,
		data.Workspace.ID, other, data.Viewer.ID).Scan(&propagated); err != nil {
		t.Fatal(err)
	}
	if propagated != 1 {
		t.Fatal("membership sync did not follow grandchild after parent patch")
	}
}

func TestTeamMetadataMutationKeepsSnapshotAndStorageInSync(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	key := seedBulkTeams(t, repo, 120)
	err = repo.MutateWorkspace(ctx, key, "team.created", "sync-team", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "sync-team", Name: "sync-team", Key: "SCP"})
		settings := bulkTeamSettings("sync-team")
		settings.SlackNotifications = map[string]bool{"#general": true}
		next.TeamSettings["sync-team"] = settings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.MutateWorkspace(ctx, key, "team.updated", "sync-team", map[string]string{"name": "Renamed"}, func(next *domain.Bootstrap) error {
		for index := range next.Teams {
			if next.Teams[index].ID == "sync-team" {
				next.Teams[index].Name = "Renamed"
				next.Teams[index].Color = "#112233"
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = repo.MutateWorkspace(ctx, key, "team.settings_updated", "sync-team", map[string]string{"parentTeamId": "bulk-team-00000"}, func(next *domain.Bootstrap) error {
		settings := next.TeamSettings["sync-team"]
		settings.ParentTeamID = "bulk-team-00000"
		settings.SlackNotifications["#alerts"] = true
		next.TeamSettings["sync-team"] = settings
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	live, _ := repo.WorkspaceMetadata(key)
	if live.TeamSettings["sync-team"].SlackNotifications["#alerts"] != true || live.TeamSettings["sync-team"].ParentTeamID != "bulk-team-00000" {
		t.Fatal("nested settings change was not applied")
	}
	if err := repo.ReloadAllWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.WorkspaceMetadata(key)
	if !reflect.DeepEqual(live.Teams, stored.Teams) || !reflect.DeepEqual(live.TeamSettings, stored.TeamSettings) || !reflect.DeepEqual(live.States, stored.States) {
		t.Fatal("reload diverged from the in-memory snapshot")
	}
}

func TestTeamMetadataMutationRollsBackOnCallbackError(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	key := seedBulkTeams(t, repo, 40)
	before, _ := repo.WorkspaceMetadata(key)
	err = repo.MutateWorkspace(ctx, key, "team.created", "rolled-back", nil, func(next *domain.Bootstrap) error {
		next.Teams = append(next.Teams, domain.Team{ID: "rolled-back", Name: "Rolled back", Key: "RBK"})
		next.TeamSettings["rolled-back"] = bulkTeamSettings("rolled-back")
		return errors.New("abort")
	})
	if err == nil {
		t.Fatal("failed callback reported success")
	}
	after, _ := repo.WorkspaceMetadata(key)
	if len(after.Teams) != len(before.Teams) {
		t.Fatal("rollback leaked into the live snapshot")
	}
	if _, leaked := after.TeamSettings["rolled-back"]; leaked {
		t.Fatal("rollback leaked settings into the live snapshot")
	}
	var persisted int
	if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND record_key=?`, key, "rolled-back").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 0 {
		t.Fatal("rollback persisted a record")
	}
}

// Creation shares slice backing arrays with the live snapshot; appends write
// past the exposed length. Run under -race to prove concurrent readers cannot
// observe a torn snapshot.
func TestTeamMetadataMutationConcurrentSnapshotReaders(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := seedBulkTeams(t, repo, 40)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, ok := repo.BootstrapFor(key)
			if !ok || len(data.Teams) < 40 {
				t.Error("snapshot reader lost the workspace")
				return
			}
		}
	}()
	for index := 0; index < 40; index++ {
		createScopedTeam(t, repo, key, fmt.Sprintf("team_concurrent%02d", index))
	}
	close(stop)
	readers.Wait()
	if data, _ := repo.BootstrapFor(key); len(data.Teams) != 81 {
		t.Fatal("concurrent mutations lost created teams")
	}
}

func TestTeamMetadataWriteCountStaysConstantAsWorkspaceGrows(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := seedBulkTeams(t, repo, 60)
	writes := auditWrites(t, repo)
	createScopedTeam(t, repo, key, "scale-small")
	small := writes()
	seedBulkTeams(t, repo, 540)
	writes()
	createScopedTeam(t, repo, key, "scale-large")
	large := writes()
	if !reflect.DeepEqual(small, large) {
		t.Fatalf("write count grew with workspace size: small=%+v large=%+v", small, large)
	}
}

func TestTeamCreateAllocsDoNotScaleWithTeamCount(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds thousands of teams")
	}
	if raceDetector {
		t.Skip("persisting 4k teams is too slow under the race detector")
	}
	measure := func(teams int) float64 {
		t.Helper()
		repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		previousStateBytes, previousTransactionBytes := repo.maxStateBytes, repo.db.maxTransactionBytes
		repo.maxStateBytes, repo.db.maxTransactionBytes = 1<<30, 0
		key := seedBulkTeams(t, repo, teams)
		repo.maxStateBytes, repo.db.maxTransactionBytes = previousStateBytes, previousTransactionBytes
		ctx := context.Background()
		var n int
		return testing.AllocsPerRun(5, func() {
			n++
			id := fmt.Sprintf("alloc-team-%d-%d", teams, n)
			if err := repo.MutateWorkspace(ctx, key, "team.created", id, nil, func(next *domain.Bootstrap) error {
				next.Teams = append(next.Teams, domain.Team{ID: id, Name: id, Key: "ALC"})
				next.TeamSettings[id] = bulkTeamSettings(id)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	small := measure(1000)
	large := measure(4000)
	if small <= 0 || large/small >= 3 {
		t.Fatalf("team.created allocs scaled with directory size: 1k=%.0f 4k=%.0f", small, large)
	}
}
