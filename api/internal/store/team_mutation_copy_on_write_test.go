package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

// heldTeamView is what a reader that took the installed snapshot outside the
// store lock (ChangeTeamMembership's workspaceByID) keeps reading.
type heldTeamView struct {
	Teams         []domain.Team
	Users         []domain.User
	TeamSettings  map[string]domain.TeamSettings
	CycleSettings map[string]domain.CycleSettings
	TeamByID      map[string]int
	TeamByKey     map[string]string
	TeamChildren  map[string][]string
	LabelIndex    map[string][]int
	States        []domain.WorkflowState
	Labels        []domain.IssueLabel
	Cycles        []domain.Cycle
	Templates     []domain.IssueTemplate
	Projects      []domain.Project
	TeamMembers   []domain.TeamMember
}

func heldTeamViewOf(data domain.Bootstrap) heldTeamView {
	return heldTeamView{data.Teams, data.Users, data.TeamSettings, data.CycleSettings, data.TeamByID, data.TeamByKey,
		data.TeamChildren, data.LabelIndex, data.States, data.Labels, data.Cycles, data.IssueTemplates, data.Projects, data.TeamMembers}
}

func cloneHeldTeamView(view heldTeamView) heldTeamView {
	return cloneSnapshotValue(reflect.ValueOf(view)).Interface().(heldTeamView)
}

// A team write works on its own copy: whether it commits or rolls back, a
// snapshot a reader already holds never changes under it (in place writes to
// the installed maps were a concurrent map read/write with such readers).
func TestTeamMutationLeavesHeldSnapshotUntouched(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	ctx := WithColdWork(context.Background(), "team copy-on-write")
	for _, test := range []struct {
		name    string
		event   string
		id      string
		fail    bool
		payload any
		mutate  func(*domain.Bootstrap) error
	}{
		{name: "created rolled back", event: "team.created", id: "cow-new", fail: true, mutate: func(next *domain.Bootstrap) error {
			next.Teams = append(next.Teams, domain.Team{ID: "cow-new", Name: "New", Key: "COWN"})
			next.TeamSettings["cow-new"] = bulkTeamSettings("cow-new")
			next.CycleSettings["cow-new"] = domain.CycleSettings{DurationWeeks: 1}
			return nil
		}},
		{name: "created", event: "team.created", id: "cow-new", mutate: func(next *domain.Bootstrap) error {
			next.Teams = append(next.Teams, domain.Team{ID: "cow-new", Name: "New", Key: "COWN"})
			next.TeamSettings["cow-new"] = bulkTeamSettings("cow-new")
			next.CycleSettings["cow-new"] = domain.CycleSettings{DurationWeeks: 1}
			return nil
		}},
		{name: "updated", event: "team.updated", id: "bulk-team-00001", payload: map[string]string{"name": "Renamed"}, mutate: func(next *domain.Bootstrap) error {
			next.Teams[domain.TeamIndex(next, "bulk-team-00001")].Name = "Renamed"
			return nil
		}},
		{name: "settings rolled back", event: "team.settings_updated", id: "bulk-team-00002", fail: true, payload: map[string]string{"parentTeamId": "bulk-team-00000"}, mutate: func(next *domain.Bootstrap) error {
			settings := next.TeamSettings["bulk-team-00002"]
			settings.ParentTeamID = "bulk-team-00000"
			settings.SlackNotifications["#alerts"] = true
			next.TeamSettings["bulk-team-00002"] = settings
			next.CycleSettings["bulk-team-00002"] = domain.CycleSettings{DurationWeeks: 3}
			return nil
		}},
		{name: "settings", event: "team.settings_updated", id: "bulk-team-00002", payload: map[string]string{"parentTeamId": "bulk-team-00000"}, mutate: func(next *domain.Bootstrap) error {
			settings := next.TeamSettings["bulk-team-00002"]
			settings.ParentTeamID = "bulk-team-00000"
			settings.SlackNotifications["#alerts"] = true
			next.TeamSettings["bulk-team-00002"] = settings
			return nil
		}},
		{name: "deleted", event: "team.deleted", id: "bulk-team-00003", mutate: func(next *domain.Bootstrap) error {
			return ApplyTeamDeletion(next, "bulk-team-00003")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			key := seedBulkTeams(t, repo, 6)
			workspaceID := repo.Bootstrap().Workspace.ID
			held, _, ok := repo.workspaceByID(workspaceID)
			if !ok {
				t.Fatal("workspace missing")
			}
			want := cloneHeldTeamView(heldTeamViewOf(held))
			abort := errors.New("abort")
			err = repo.MutateWorkspace(ctx, key, test.event, test.id, test.payload, func(next *domain.Bootstrap) error {
				if err := test.mutate(next); err != nil {
					return err
				}
				if !reflect.DeepEqual(cloneHeldTeamView(heldTeamViewOf(held)), want) {
					t.Error("the write changed a snapshot a reader holds")
				}
				if test.fail {
					return abort
				}
				return nil
			})
			if test.fail != errors.Is(err, abort) || (!test.fail && err != nil) {
				t.Fatalf("mutation error = %v", err)
			}
			if !reflect.DeepEqual(cloneHeldTeamView(heldTeamViewOf(held)), want) {
				t.Fatal("the write or its rollback changed a snapshot a reader holds")
			}
			after, _, _ := repo.workspaceByID(workspaceID)
			_, created := after.TeamSettings["cow-new"]
			if test.event == "team.created" && created == test.fail {
				t.Fatalf("installed copy has created settings = %v after fail = %v", created, test.fail)
			}
			if test.fail && !reflect.DeepEqual(cloneHeldTeamView(heldTeamViewOf(after)).TeamSettings, want.TeamSettings) {
				t.Fatal("a rolled back write changed the installed settings")
			}
		})
	}
}

// Same-key team creations race the creator-membership read of the snapshot
// (createTeam then ChangeTeamMembership): the losers' rollback deleted
// their settings from the installed map while the reader walked it. Run
// under -race.
func TestTeamCreateRollbackRacesMembershipReaders(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	key := seedBulkTeams(t, repo, 4)
	data := repo.Bootstrap()
	workspaceID, viewer := data.Workspace.ID, data.Viewer.ID
	unsettled := len(data.Teams) - len(data.TeamSettings)
	ctx := WithColdWork(context.Background(), "same-key team creation")
	duplicate := errors.New("team key already exists")
	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			held, _, ok := repo.workspaceByID(workspaceID)
			if !ok {
				t.Error("workspace missing")
				return
			}
			for id := range held.TeamSettings {
				_ = held.TeamSettings[id].ParentTeamID
				_ = held.CycleSettings[id]
				_ = held.TeamByID[id]
			}
			for _, team := range held.Teams {
				_ = held.TeamByKey[team.Key]
			}
		}
	}()
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := repo.ChangeTeamMembership(ctx, TeamMembershipChangeRequest{WorkspaceID: workspaceID, TeamID: "bulk-team-00001", UserID: viewer, Member: true, Role: "owner", RetryConcurrentWrites: true}); err != nil && !errors.Is(err, ErrConcurrentWrite) {
				t.Error(err)
				return
			}
		}
	}()
	var writers sync.WaitGroup
	for writer := range 3 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for attempt := range 15 {
				id := fmt.Sprintf("same-key-%d-%d", writer, attempt)
				err := repo.MutateWorkspace(ctx, key, "team.created", id, nil, func(next *domain.Bootstrap) error {
					for _, team := range next.Teams {
						if team.Key == "SAME" {
							return duplicate
						}
					}
					next.Teams = append(next.Teams, domain.Team{ID: id, Name: id, Key: "SAME"})
					next.TeamSettings[id] = bulkTeamSettings(id)
					next.CycleSettings[id] = domain.CycleSettings{DurationWeeks: 2}
					if writer > 0 {
						// The duplicate is found after the settings were written.
						return duplicate
					}
					return nil
				})
				if err != nil && !errors.Is(err, duplicate) {
					t.Error(err)
					return
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	after, _, _ := repo.workspaceByID(workspaceID)
	same := 0
	for _, team := range after.Teams {
		if team.Key == "SAME" {
			same++
		}
	}
	if same != 1 || len(after.Teams)-len(after.TeamSettings) != unsettled {
		t.Fatalf("teams with the key = %d, settings %d for %d teams", same, len(after.TeamSettings), len(after.Teams))
	}
}
