package store

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"

	"github.com/alicebob/miniredis/v2"
)

// These tests run two servers on one database. One commits a metadata change;
// the other, whose copy has not caught up, then writes the same record. The
// second write must keep the first's change (merged field by field), rerun on
// the stored record, or fail with ErrConcurrentWrite, never silently undo it.

// staleMetadataWrite persists edit from repo's in-memory copy, as every
// metadata writer does after its load: the copy may lack what another server
// committed since.
func staleMetadataWrite(t *testing.T, repo *SQLiteStore, workspace string, edit func(*domain.Bootstrap)) error {
	t.Helper()
	repo.writeMu.Lock()
	defer repo.writeMu.Unlock()
	before, ok := repo.storedWorkspace(workspace)
	if !ok {
		t.Fatal("workspace missing")
	}
	after := cloneBootstrap(before)
	edit(&after)
	if err := repo.persistWorkspaceOnto(t.Context(), workspace, &before, &after, nil, nil); err != nil {
		return err
	}
	// Writers install what they wrote, merged records included.
	repo.installWorkspace(workspace, collectionMetadata(after), false)
	return nil
}

func metadataEdit(t *testing.T, repo *SQLiteStore, workspace, event, id string, edit func(*domain.Bootstrap) error) {
	t.Helper()
	if err := repo.MutateWorkspace(t.Context(), workspace, event, id, nil, edit); err != nil {
		t.Fatal(err)
	}
}

func storedState(t *testing.T, repo *SQLiteStore, workspace string) domain.Bootstrap {
	t.Helper()
	data, err := repo.loadWorkspaceState(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A comment and reactions added to one project update on two servers are
// all kept: updates are merged per update, comments by id and reactions key
// by key.
func TestProjectUpdateCommentsAndReactionsFromTwoServersAreKept(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			projectID := raceProject(t, first, workspace)
			updateID := "race_update_" + raceStamp()
			metadataEdit(t, first, workspace, "project_update.created", updateID, func(data *domain.Bootstrap) error {
				if data.ProjectUpdates == nil {
					data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
				}
				data.ProjectUpdates[projectID] = append(slices.Clone(data.ProjectUpdates[projectID]), domain.ProjectUpdate{ID: updateID, ProjectID: projectID, Body: "Status", Health: "onTrack", CreatedAt: time.Now().UTC(), User: data.Viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{}, Attachments: []domain.Attachment{}})
				return nil
			})
			reloadPeers(t, second)
			comment := func(id string) domain.Comment {
				return domain.Comment{ID: id, Body: id, CreatedAt: time.Now().UTC(), Reactions: map[string][]string{}}
			}
			edit := func(commentID, reaction, user string) func(*domain.Bootstrap) {
				return func(data *domain.Bootstrap) {
					updates := slices.Clone(data.ProjectUpdates[projectID])
					index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == updateID })
					updates[index].Comments = append(slices.Clone(updates[index].Comments), comment(commentID))
					reactions := maps.Clone(updates[index].Reactions)
					reactions[reaction] = append(slices.Clone(reactions[reaction]), user)
					updates[index].Reactions = reactions
					data.ProjectUpdates = maps.Clone(data.ProjectUpdates)
					data.ProjectUpdates[projectID] = updates
				}
			}
			metadataEdit(t, first, workspace, "project_update.comment_created", updateID, func(data *domain.Bootstrap) error {
				edit("comment_first", "+1", "user_first")(data)
				return nil
			})
			// The second server has not seen the first comment.
			if err := staleMetadataWrite(t, second, workspace, func(data *domain.Bootstrap) {
				edit("comment_second", "+1", "user_second")(data)
				edit("comment_second_b", "tada", "user_second")(data)
			}); err != nil {
				t.Fatal(err)
			}
			updates := storedState(t, first, workspace).ProjectUpdates[projectID]
			index := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == updateID })
			if index < 0 {
				t.Fatal("update deleted")
			}
			update := updates[index]
			ids := []string{}
			for _, comment := range update.Comments {
				ids = append(ids, comment.ID)
			}
			if !slices.Contains(ids, "comment_first") || !slices.Contains(ids, "comment_second") || !slices.Contains(ids, "comment_second_b") {
				t.Fatalf("comments %v, want both servers' comments", ids)
			}
			if plus := update.Reactions["+1"]; !slices.Contains(plus, "user_first") || !slices.Contains(plus, "user_second") || len(update.Reactions["tada"]) != 1 {
				t.Fatalf("reactions %v, want +1 from both users and tada", update.Reactions)
			}
		})
	}
}

// An import job's progress written on one server survives another server's
// edit of the job from an older copy, and the job keeps its input rows.
func TestImportJobProgressFromAnotherServerIsKept(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			jobID := "race_import_" + raceStamp()
			metadataEdit(t, first, workspace, "import.created", jobID, func(data *domain.Bootstrap) error {
				data.ImportJobs = append(slices.Clone(data.ImportJobs), domain.ImportJob{ID: jobID, UserID: data.Viewer.ID, Filename: "race.csv", Format: "csv", Status: "queued", Headers: []string{"title"}, Rows: []map[string]string{{"title": "a"}, {"title": "b"}}, Errors: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			})
			reloadPeers(t, second)
			job := func(data *domain.Bootstrap) *domain.ImportJob {
				data.ImportJobs = slices.Clone(data.ImportJobs)
				index := slices.IndexFunc(data.ImportJobs, func(job domain.ImportJob) bool { return job.ID == jobID })
				return &data.ImportJobs[index]
			}
			metadataEdit(t, first, workspace, "import.progressed", jobID, func(data *domain.Bootstrap) error {
				job(data).Progress = 50
				job(data).Imported = 1
				return nil
			})
			if err := staleMetadataWrite(t, second, workspace, func(data *domain.Bootstrap) {
				job(data).Mapping = map[string]string{"title": "title"}
			}); err != nil {
				t.Fatal(err)
			}
			stored := storedState(t, first, workspace)
			index := slices.IndexFunc(stored.ImportJobs, func(job domain.ImportJob) bool { return job.ID == jobID })
			if index < 0 {
				t.Fatal("import job deleted")
			}
			if got := stored.ImportJobs[index]; got.Progress != 50 || got.Imported != 1 || got.Mapping["title"] != "title" {
				t.Fatalf("import job progress %d imported %d mapping %v; want both servers' changes", got.Progress, got.Imported, got.Mapping)
			}
			for index, repo := range []*SQLiteStore{first, second} {
				data, _ := repo.BootstrapForContext(t.Context(), workspace)
				at := slices.IndexFunc(data.ImportJobs, func(job domain.ImportJob) bool { return job.ID == jobID })
				if at < 0 || len(data.ImportJobs[at].Rows) != 2 || data.ImportJobs[at].Progress != 50 {
					t.Fatalf("server %d serves the import job without its input rows or progress: at %d %+v", index, at, data.ImportJobs)
				}
			}
		})
	}
}

// A project field both servers changed is not overwritten silently: the
// write from the older copy fails with ErrConcurrentWrite.
func TestStaleWriteOfAFieldAnotherServerChangedConflicts(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			projectID := raceProject(t, first, workspace)
			reloadPeers(t, second)
			rename := func(name string) func(*domain.Bootstrap) {
				return func(data *domain.Bootstrap) {
					data.Projects = slices.Clone(data.Projects)
					index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == projectID })
					data.Projects[index].Name = name
				}
			}
			metadataEdit(t, first, workspace, "project.updated", projectID, func(data *domain.Bootstrap) error {
				rename("First server")(data)
				return nil
			})
			err := staleMetadataWrite(t, second, workspace, rename("Second server"))
			if !errors.Is(err, ErrConcurrentWrite) {
				t.Fatalf("stale rename error %v, want ErrConcurrentWrite", err)
			}
			if got := storedProject(t, first, workspace, projectID).Name; got != "First server" {
				t.Fatalf("project name %q, want the first server's", got)
			}
		})
	}
}

// Two servers adding different keys to one nested map (a team's chat
// notification switches) keep both.
func TestNestedMapKeysAddedOnTwoServersAreBothKept(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			teamID := template.Team.ID
			toggle := func(key string) func(*domain.Bootstrap) {
				return func(data *domain.Bootstrap) {
					data.TeamSettings = maps.Clone(data.TeamSettings)
					settings := data.TeamSettings[teamID]
					settings.SlackNotifications = maps.Clone(settings.SlackNotifications)
					if settings.SlackNotifications == nil {
						settings.SlackNotifications = map[string]bool{}
					}
					settings.SlackNotifications[key] = true
					data.TeamSettings[teamID] = settings
				}
			}
			stamp := raceStamp()
			if err := staleMetadataWrite(t, first, workspace, toggle("first_"+stamp)); err != nil {
				t.Fatal(err)
			}
			if err := staleMetadataWrite(t, second, workspace, toggle("second_"+stamp)); err != nil {
				t.Fatal(err)
			}
			got := storedState(t, first, workspace).TeamSettings[teamID].SlackNotifications
			if !got["first_"+stamp] || !got["second_"+stamp] {
				t.Fatalf("notification switches %v, want both servers' keys", got)
			}
		})
	}
}

// A team rename loads the team, another server's unlocked write changes the
// team's color, then the rename writes: both changes are kept.
func TestTeamRenameKeepsAColorChangedMeanwhile(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			teamID := template.Team.ID
			color := "#" + raceStamp()[len(raceStamp())-6:]
			newName := "Renamed " + raceStamp()
			run := func(callback func(*domain.Bootstrap) error) error {
				return second.MutateWorkspace(t.Context(), workspace, "team.updated", teamID, map[string]string{"name": newName}, callback)
			}
			release, wait := pausedMutation(t, run, func(data *domain.Bootstrap) error {
				index := domain.TeamIndex(data, teamID)
				data.Teams[index].Name = newName
				return nil
			})
			if err := staleMetadataWrite(t, first, workspace, func(data *domain.Bootstrap) {
				data.Teams = slices.Clone(data.Teams)
				index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
				data.Teams[index].Color = color
			}); err != nil {
				t.Fatal(err)
			}
			release()
			if _, err := wait(); err != nil {
				t.Fatal(err)
			}
			teams := storedState(t, first, workspace).Teams
			index := slices.IndexFunc(teams, func(team domain.Team) bool { return team.ID == teamID })
			if teams[index].Name != newName || teams[index].Color != color {
				t.Fatalf("team name %q color %q; want %q and %q", teams[index].Name, teams[index].Color, newName, color)
			}
			copy, _ := second.storedWorkspace(workspace)
			if index := domain.TeamIndex(&copy, teamID); copy.Teams[index].Color != color {
				t.Fatalf("the renaming server installed color %q, want %q", copy.Teams[index].Color, color)
			}
		})
	}
}

// A startup migration running from an older copy does not overwrite a value
// another server set meanwhile: it reruns on the stored record.
func TestMigrationRerunsInsteadOfOverwritingAnotherServersValue(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			cycleID := "race_cycle_" + raceStamp()
			metadataEdit(t, first, workspace, "cycle.created", cycleID, func(data *domain.Bootstrap) error {
				data.Cycles = append(slices.Clone(data.Cycles), domain.Cycle{ID: cycleID, TeamID: template.Team.ID, Name: "Race", Number: 999, StartsAt: time.Now().UTC(), EndsAt: time.Now().UTC().Add(7 * 24 * time.Hour), Capacity: 9})
				return nil
			})
			reloadPeers(t, second)
			metadataEdit(t, first, workspace, "cycle.updated", cycleID, func(data *domain.Bootstrap) error {
				data.Cycles = slices.Clone(data.Cycles)
				index := slices.IndexFunc(data.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == cycleID })
				override := 7
				data.Cycles[index].CapacityOverride = &override
				return nil
			})
			if err := second.migrateCycleCapacityOverrides(t.Context()); err != nil {
				t.Fatal(err)
			}
			cycles := storedState(t, first, workspace).Cycles
			index := slices.IndexFunc(cycles, func(cycle domain.Cycle) bool { return cycle.ID == cycleID })
			if override := cycles[index].CapacityOverride; override == nil || *override != 7 {
				t.Fatalf("capacity override %v, want the 7 set on the first server", override)
			}
		})
	}
}

// An issue write whose metadata change conflicts with another server's
// reruns its callback on the stored record instead of keeping its own value.
func TestIssueRecordWriteRerunsOnAMetadataConflict(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			issue := createPeerIssue(t, first, workspace, "race-rerun-issue-"+raceStamp(), template)
			documentID := "race_rerun_document_" + raceStamp()
			metadataEdit(t, first, workspace, "document.permissions_updated", documentID, func(data *domain.Bootstrap) error {
				data.Documents = append(data.Documents, domain.Document{ID: documentID, SlugID: documentID, Title: "Attached", Creator: data.Viewer, ProjectIDs: []string{}, TeamIDs: []string{}, IssueID: issue.ID, IssueTeamID: issue.Team.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			})
			reloadPeers(t, second)
			metadataEdit(t, first, workspace, "document.permissions_updated", documentID, func(data *domain.Bootstrap) error {
				index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == documentID })
				data.Documents[index].IssueTeamID = "team_elsewhere"
				return nil
			})
			team := issue.Team
			team.ID, team.Name = "race_team_"+raceStamp(), "Race team"
			seen := []string{}
			if _, err := second.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, IssueMutationScope{}, func(data *domain.Bootstrap, moved *domain.Issue) error {
				moved.Team = team
				index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == documentID })
				seen = append(seen, data.Documents[index].IssueTeamID)
				data.Documents[index].IssueTeamID = team.ID
				data.Documents[index].InheritedTeamIDs = []string{team.ID}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(seen) < 2 || seen[len(seen)-1] != "team_elsewhere" {
				t.Fatalf("callback saw issue teams %v; want a rerun that saw the first server's change", seen)
			}
		})
	}
}

// A full mutation that changes an issue's progress while another server
// creates issues in the same project, once during every attempt, counts all
// of them: progress is computed in the write transaction from the committed
// issue rows.
func TestFullMutationProgressCountsIssuesCreatedOnAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			projectID := raceProject(t, first, workspace)
			reloadPeers(t, second)
			project := storedProject(t, first, workspace, projectID)
			moving := createPeerIssue(t, first, workspace, "race-full-progress-"+raceStamp(), projectIssue(template, project))
			reloadPeers(t, first, second)
			stored, _ := first.storedWorkspace(workspace)
			completed := slices.IndexFunc(stored.States, func(state domain.WorkflowState) bool {
				return state.Type == "completed" && (state.TeamID == "" || state.TeamID == moving.Team.ID)
			})
			if completed < 0 {
				t.Fatal("no completed state")
			}
			state := stored.States[completed]
			var calls atomic.Int32
			err := first.MutateWorkspace(t.Context(), workspace, "issue.updated", moving.ID, map[string]string{"stateId": state.ID}, func(data *domain.Bootstrap) error {
				// Another server's issue creation commits after this attempt
				// loaded the issues.
				createPeerIssue(t, second, workspace, "race-full-other-"+raceStamp(), projectIssue(template, project))
				calls.Add(1)
				index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == moving.ID })
				data.Issues[index].State = state
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err := first.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND project_id=?`, workspace, projectID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if got := progressScope(storedProject(t, first, workspace, projectID)); got != count {
				t.Fatalf("project progress counts %d issues after %d attempts, the project has %d", got, calls.Load(), count)
			}
			history := storedProject(t, first, workspace, projectID).ProgressHistory
			if done := history[len(history)-1].CompletedIssueCount; done != 1 {
				t.Fatalf("project progress counts %d completed issues, want 1", done)
			}
		})
	}
}

// pausingCache holds metadata cache stores at chosen points so two fills
// can be interleaved deterministically.
type pausingCache struct {
	*coordination.Redis
	stores atomic.Int32
	hold   func(store int32)
}

func (cache *pausingCache) CacheHSetMap(ctx context.Context, key string, fields map[string][]byte) error {
	if cache.hold != nil {
		cache.hold(cache.stores.Add(1))
	}
	return cache.Redis.CacheHSetMap(ctx, key, fields)
}

// Two cache fills at one stamp never leave a ready but partial copy: the
// second fill's reset no longer removes fields the first wrote before it
// marked the copy ready.
func TestConcurrentMetadataCacheFillsNeverServeAPartialCopy(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(t.TempDir() + "/fill.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	redisServer := miniredis.RunT(t)
	redis, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redisServer.Addr()}, Prefix: "fill", ConnectTimeout: time.Second, LockTTL: 5 * time.Second, LockWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { redis.Close() })
	cache := &pausingCache{Redis: redis}
	repo.SetWorkspaceCoordinator(cache)
	workspace := repo.WorkspaceKeys()[0]
	stamp, ok := repo.metadataStamp(t.Context(), repo.db, workspace)
	if !ok {
		t.Fatal("no metadata stamp")
	}
	rows := []metadataCacheRow{{Field: "teams", Key: "a", Data: []byte(`{"id":"a"}`)}, {Field: "labels", Key: "b", Data: []byte(`{"id":"b"}`)}}
	firstPaused, releaseFirst := make(chan struct{}), make(chan struct{})
	secondReached, releaseSecond := make(chan struct{}), make(chan struct{})
	cache.hold = func(store int32) {
		switch store {
		case 2: // the first fill, between its two fields
			close(firstPaused)
			<-releaseFirst
		case 3: // the second fill, after its reset
			close(secondReached)
			<-releaseSecond
		}
	}
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(firstDone)
		repo.fillMetadataRecordsCacheAt(context.Background(), workspace, rows, stamp)
	}()
	<-firstPaused
	go func() {
		defer close(secondDone)
		repo.fillMetadataRecordsCacheAt(context.Background(), workspace, rows, stamp)
	}()
	select {
	case <-secondReached:
	case <-secondDone:
	case <-time.After(10 * time.Second):
		t.Fatal("second fill stuck")
	}
	close(releaseFirst)
	<-firstDone
	cached, ok := repo.metadataRecordsFromCache(t.Context(), workspace, stamp, map[string]string{"teams": "array", "labels": "array"})
	close(releaseSecond)
	<-secondDone
	if ok && len(cached) != len(rows) {
		t.Fatalf("a ready metadata copy holds %d of %d rows", len(cached), len(rows))
	}
	if !ok {
		t.Fatal("the completed fill's copy is not served")
	}
}
