package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// Issue writes (creation, scoped issue writes, comments) run in their own
// record transactions without the workspace lock and write the workspace
// metadata they change (project progress, document scopes, release issue
// lists, subscriptions, triage cursors) from their server's in-memory copy.
// These tests run two servers on one database and check that a metadata
// change committed on one server survives a record write on the other.

func raceStamp() string { return fmt.Sprintf("%d", time.Now().UnixNano()) }

// raceProject adds an empty project through a metadata-only write on repo
// and returns its id.
func raceProject(t *testing.T, repo *SQLiteStore, workspace string) string {
	t.Helper()
	id := "race_project_" + raceStamp()
	err := repo.MutateWorkspace(t.Context(), workspace, "project.created", id, domain.ProjectMutationInput{}, func(data *domain.Bootstrap) error {
		if len(data.Projects) == 0 {
			return errors.New("fixture has no project to copy")
		}
		project := data.Projects[0]
		project.ID, project.SlugID, project.Name = id, id, "Race "+id
		project.StartDate = nil
		project.IssueCountHistory, project.ScopeHistory, project.CompletedScopeHistory, project.InProgressScopeHistory, project.ProgressHistory = nil, nil, nil, nil, nil
		data.Projects = append(data.Projects, project)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// storedProject reads a project as the database holds it.
func storedProject(t *testing.T, repo *SQLiteStore, workspace, id string) domain.Project {
	t.Helper()
	data, err := repo.loadWorkspaceState(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
	if index < 0 {
		t.Fatalf("project %s is missing", id)
	}
	return data.Projects[index]
}

func progressScope(project domain.Project) int {
	if len(project.ProgressHistory) == 0 {
		return -1
	}
	return project.ProgressHistory[len(project.ProgressHistory)-1].ScopeCount
}

func projectIssue(template domain.Issue, project domain.Project) domain.Issue {
	template.Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name}
	return template
}

func reloadPeers(t *testing.T, repos ...*SQLiteStore) {
	t.Helper()
	for _, repo := range repos {
		if err := repo.ReloadWorkspace(t.Context(), repoWorkspace(repo)); err != nil {
			t.Fatal(err)
		}
	}
}

var raceWorkspaces sync.Map

func repoWorkspace(repo *SQLiteStore) string {
	workspace, _ := raceWorkspaces.Load(repo)
	return workspace.(string)
}

func racePair(t *testing.T, pair [2]*SQLiteStore) (first, second *SQLiteStore, workspace string, template domain.Issue) {
	t.Helper()
	first, second = pair[0], pair[1]
	workspace, template = peerWorkspace(t, second)
	raceWorkspaces.Store(first, workspace)
	raceWorkspaces.Store(second, workspace)
	reloadPeers(t, first, second)
	return first, second, workspace, template
}

// Two issues created in one project on two servers both count in the
// project's progress: the second creation's server had not seen the first.
func TestIssueCreationsOnTwoServersBothCountInProjectProgress(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			projectID := raceProject(t, first, workspace)
			reloadPeers(t, second)
			project := storedProject(t, first, workspace, projectID)
			// The first issue builds the history; both servers then hold it.
			createPeerIssue(t, second, workspace, "race-progress-a-"+raceStamp(), projectIssue(template, project))
			reloadPeers(t, first)
			createPeerIssue(t, second, workspace, "race-progress-b-"+raceStamp(), projectIssue(template, project))
			createPeerIssue(t, first, workspace, "race-progress-c-"+raceStamp(), projectIssue(template, project))
			if got := progressScope(storedProject(t, first, workspace, projectID)); got != 3 {
				t.Fatalf("project progress counts %d issues, want 3", got)
			}
		})
	}
}

// A project edit on one server and an issue creation in the project on the
// other keep both the edit and the issue's progress, in either order.
func TestProjectEditAndIssueCreationOnTwoServersKeepBoth(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			projectID := raceProject(t, first, workspace)
			reloadPeers(t, second)
			project := storedProject(t, first, workspace, projectID)
			createPeerIssue(t, second, workspace, "race-edit-a-"+raceStamp(), projectIssue(template, project))
			reloadPeers(t, first)
			rename := func(repo *SQLiteStore, name string) func(callback func(*domain.Bootstrap) error) error {
				return func(callback func(*domain.Bootstrap) error) error {
					return repo.MutateWorkspace(t.Context(), workspace, "project.updated", projectID, map[string]string{"name": name}, callback)
				}
			}
			setName := func(name string) func(*domain.Bootstrap) error {
				return func(data *domain.Bootstrap) error {
					index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == projectID })
					if index < 0 {
						return errors.New("project missing")
					}
					data.Projects[index].Name = name
					return nil
				}
			}

			// The rename commits first; the issue's server still holds the old name.
			if err := rename(first, "Renamed on the first server")(setName("Renamed on the first server")); err != nil {
				t.Fatal(err)
			}
			createPeerIssue(t, second, workspace, "race-edit-b-"+raceStamp(), projectIssue(template, project))
			stored := storedProject(t, first, workspace, projectID)
			if stored.Name != "Renamed on the first server" || progressScope(stored) != 2 {
				t.Fatalf("after rename then issue: name %q, progress %d; want the rename and 2", stored.Name, progressScope(stored))
			}

			// The rename loads the project, the issue commits, then the rename writes.
			reloadPeers(t, second)
			release, wait := pausedMutation(t, rename(first, "Renamed again"), setName("Renamed again"))
			createPeerIssue(t, second, workspace, "race-edit-c-"+raceStamp(), projectIssue(template, project))
			release()
			if _, err := wait(); err != nil {
				t.Fatal(err)
			}
			stored = storedProject(t, first, workspace, projectID)
			if stored.Name != "Renamed again" || progressScope(stored) != 3 {
				t.Fatalf("after issue during rename: name %q, progress %d; want the rename and 3", stored.Name, progressScope(stored))
			}
		})
	}
}

// Moving an issue to another team updates the inherited scope of its
// document; a title edit another server committed on the document stays.
func TestIssueTeamMoveKeepsADocumentEditFromAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			issue := createPeerIssue(t, first, workspace, "race-doc-issue-"+raceStamp(), template)
			// The store does not check the destination team; the scope
			// fields are what matter.
			team := issue.Team
			team.ID, team.Name = "race_team_"+raceStamp(), "Race team"
			documentID := "race_document_" + raceStamp()
			if err := first.MutateWorkspace(t.Context(), workspace, "document.permissions_updated", documentID, nil, func(data *domain.Bootstrap) error {
				data.Documents = append(data.Documents, domain.Document{ID: documentID, SlugID: documentID, Title: "Attached", Creator: data.Viewer, ProjectIDs: []string{}, TeamIDs: []string{}, IssueID: issue.ID, IssueTeamID: issue.Team.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			reloadPeers(t, second)
			if err := first.MutateWorkspace(t.Context(), workspace, "document.permissions_updated", documentID, nil, func(data *domain.Bootstrap) error {
				index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == documentID })
				data.Documents[index].Title = "Edited on the first server"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			expected := issue.Version
			if _, err := second.UpdateIssueRecord(t.Context(), workspace, issue.ID, &expected, IssueMutationScope{}, func(data *domain.Bootstrap, moved *domain.Issue) error {
				moved.Team = team
				index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == documentID })
				if index < 0 {
					return errors.New("document missing")
				}
				data.Documents[index].IssueTeamID = team.ID
				data.Documents[index].InheritedTeamIDs = []string{team.ID}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			data, err := first.loadWorkspaceState(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(data.Documents, func(document domain.Document) bool { return document.ID == documentID })
			if index < 0 {
				t.Fatal("document deleted")
			}
			if document := data.Documents[index]; document.Title != "Edited on the first server" || document.IssueTeamID != team.ID {
				t.Fatalf("document title %q, issue team %q; want the edit and %q", document.Title, document.IssueTeamID, team.ID)
			}
		})
	}
}

// scopedIssueWrite runs a scoped issue write (as attachment, release and
// subscription edits on an issue do) whose callback edits metadata.
func scopedIssueWrite(t *testing.T, repo *SQLiteStore, workspace, issueID string, edit func(*domain.Bootstrap) error) {
	t.Helper()
	if err := repo.MutateWorkspace(WithIssueRecordMutations(t.Context(), issueID), workspace, "issue.releases_updated", issueID, nil, edit); err != nil {
		t.Fatal(err)
	}
}

// Two servers add different issues to one release through scoped issue
// writes; the release keeps both.
func TestReleaseIssuesAddedOnTwoServersAreBothKept(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			a := createPeerIssue(t, first, workspace, "race-release-a-"+raceStamp(), template)
			b := createPeerIssue(t, first, workspace, "race-release-b-"+raceStamp(), template)
			releaseID := "race_release_" + raceStamp()
			if err := first.MutateWorkspace(t.Context(), workspace, "release.reordered", releaseID, nil, func(data *domain.Bootstrap) error {
				data.Releases = append(data.Releases, domain.Release{ID: releaseID, SlugID: releaseID, Name: "Race release", IssueIDs: []string{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			reloadPeers(t, second)
			add := func(issueID string) func(*domain.Bootstrap) error {
				return func(data *domain.Bootstrap) error {
					index := slices.IndexFunc(data.Releases, func(release domain.Release) bool { return release.ID == releaseID })
					if index < 0 {
						return errors.New("release missing")
					}
					data.Releases[index].IssueIDs = append(slices.Clone(data.Releases[index].IssueIDs), issueID)
					return nil
				}
			}
			scopedIssueWrite(t, first, workspace, a.ID, add(a.ID))
			scopedIssueWrite(t, second, workspace, b.ID, add(b.ID))
			data, err := first.loadWorkspaceState(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(data.Releases, func(release domain.Release) bool { return release.ID == releaseID })
			if index < 0 {
				t.Fatal("release deleted")
			}
			if issues := data.Releases[index].IssueIDs; !slices.Contains(issues, a.ID) || !slices.Contains(issues, b.ID) {
				t.Fatalf("release issues %v, want %s and %s", issues, a.ID, b.ID)
			}
		})
	}
}

// A scoped issue write that adds a subscription on a server that has not
// seen another server remove one subscription and add another neither
// restores the removed one nor deletes the added one.
func TestSubscriptionWriteKeepsSubscriptionChangesFromAnotherServer(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			issue := createPeerIssue(t, first, workspace, "race-subscription-"+raceStamp(), template)
			stamp := raceStamp()
			subscription := func(suffix string) domain.Subscription {
				return domain.Subscription{ID: "race_subscription_" + suffix + "_" + stamp, UserID: "user_" + suffix, ResourceType: "project", ResourceID: "race_resource_" + stamp, CreatedAt: time.Now().UTC()}
			}
			kept, removed, added, ours := subscription("kept"), subscription("removed"), subscription("added"), subscription("ours")
			edit := func(repo *SQLiteStore, change func([]domain.Subscription) []domain.Subscription) {
				if err := repo.MutateWorkspace(t.Context(), workspace, "subscription.added", kept.ID, nil, func(data *domain.Bootstrap) error {
					data.Subscriptions = change(slices.Clone(data.Subscriptions))
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			edit(first, func(items []domain.Subscription) []domain.Subscription { return append(items, kept, removed) })
			reloadPeers(t, second)
			edit(first, func(items []domain.Subscription) []domain.Subscription {
				items = slices.DeleteFunc(items, func(item domain.Subscription) bool { return item.ID == removed.ID })
				return append(items, added)
			})
			scopedIssueWrite(t, second, workspace, issue.ID, func(data *domain.Bootstrap) error {
				data.Subscriptions = append(slices.Clone(data.Subscriptions), ours)
				return nil
			})
			data, err := first.loadWorkspaceState(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			has := func(id string) bool {
				return slices.ContainsFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.ID == id })
			}
			if !has(kept.ID) || !has(added.ID) || !has(ours.ID) || has(removed.ID) {
				t.Fatalf("subscriptions kept=%v added=%v ours=%v removed=%v; want the removal and every addition", has(kept.ID), has(added.ID), has(ours.ID), has(removed.ID))
			}
		})
	}
}

// Round-robin triage advances the responsibility's cursor on issue
// creation; two creations on two servers advance it twice.
func TestTriageCursorAdvancesOnceForEachServersIssue(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			responsibilityID := "race_triage_" + raceStamp()
			if err := first.MutateWorkspace(t.Context(), workspace, "triage_responsibility.created", responsibilityID, nil, func(data *domain.Bootstrap) error {
				data.TriageResponsibilities = append(data.TriageResponsibilities, domain.TriageResponsibility{ID: responsibilityID, TeamID: template.Team.ID, Name: "Race", Mode: "roundRobin", UserIDs: []string{"a", "b", "c", "d"}, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			reloadPeers(t, second)
			assignees := []string{}
			var mu sync.Mutex
			create := func(repo *SQLiteStore, id string) {
				err := repo.MutateWorkspaceWithAggregate(WithIssueRecordMutations(t.Context()), workspace, "issue.created", map[string]any{}, func(data *domain.Bootstrap) (string, error) {
					index := slices.IndexFunc(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool { return item.ID == responsibilityID })
					if index < 0 {
						return "", errors.New("responsibility missing")
					}
					responsibility := &data.TriageResponsibilities[index]
					assignee := responsibility.UserIDs[responsibility.Cursor%len(responsibility.UserIDs)]
					responsibility.Cursor = (responsibility.Cursor + 1) % len(responsibility.UserIDs)
					issue := template
					issue.ID, issue.Identifier, issue.Number, issue.Version = id, "PEER-"+id, data.NextIssueNumber, 1
					issue.ParentID, issue.SubIssueIDs, issue.ArchivedAt = nil, []string{}, nil
					issue.CreatedAt, issue.UpdatedAt = time.Now().UTC(), time.Now().UTC()
					data.Issues = append(data.Issues, issue)
					mu.Lock()
					assignees = append(assignees, assignee)
					mu.Unlock()
					return id, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			create(first, "race-triage-a-"+raceStamp())
			create(second, "race-triage-b-"+raceStamp())
			data, err := first.loadWorkspaceState(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(data.TriageResponsibilities, func(item domain.TriageResponsibility) bool { return item.ID == responsibilityID })
			if cursor := data.TriageResponsibilities[index].Cursor; cursor != 2 {
				t.Fatalf("triage cursor %d after two issues (assignees %v), want 2", cursor, assignees)
			}
			if last := assignees[len(assignees)-1]; last != "b" {
				t.Fatalf("second issue went to %q (assignees %v), want b", last, assignees)
			}
		})
	}
}

// Issue writes on two servers that touch the same projects in opposite
// orders, interleaved with project edits, finish without deadlocks, and
// every issue counts in its project's progress.
func TestConcurrentIssueAndProjectWritesOnTwoServers(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			if name == "sqlite" {
				// One SQLite file serves one server: two writing instances
				// fail with SQLITE_BUSY instead of waiting for each other.
				t.Skip("several servers need MySQL or PostgreSQL")
			}
			first, second, workspace, template := racePair(t, pair)
			projects := []string{raceProject(t, first, workspace), raceProject(t, first, workspace)}
			reloadPeers(t, second)
			stored := []domain.Project{storedProject(t, first, workspace, projects[0]), storedProject(t, first, workspace, projects[1])}
			// Histories exist on both servers before the race.
			moving := []domain.Issue{
				createPeerIssue(t, first, workspace, "race-move-a-"+raceStamp(), projectIssue(template, stored[0])),
				createPeerIssue(t, first, workspace, "race-move-b-"+raceStamp(), projectIssue(template, stored[1])),
			}
			reloadPeers(t, first, second)
			const rounds = 8
			var wg sync.WaitGroup
			errs := make(chan error, 64)
			repos := []*SQLiteStore{first, second}
			for worker := range 2 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					repo := repos[worker]
					for round := range rounds {
						issue := projectIssue(template, stored[(worker+round)%2])
						issue.ID = fmt.Sprintf("race-concurrent-%d-%d-%s", worker, round, raceStamp())
						err := repo.MutateWorkspaceWithAggregate(WithIssueRecordMutations(context.Background()), workspace, "issue.created", map[string]any{}, func(data *domain.Bootstrap) (string, error) {
							created := issue
							created.Identifier, created.Number, created.Version = "PEER-"+created.ID, data.NextIssueNumber, 1
							created.ParentID, created.SubIssueIDs, created.ArchivedAt = nil, []string{}, nil
							created.CreatedAt, created.UpdatedAt = time.Now().UTC(), time.Now().UTC()
							data.Issues = append(data.Issues, created)
							return created.ID, nil
						})
						if err != nil {
							errs <- fmt.Errorf("create on server %d: %w", worker, err)
						}
						// Move one issue between the projects (both project rows).
						move := moving[worker]
						current, err := repo.IssueRecord(context.Background(), workspace, move.ID)
						if err != nil {
							errs <- err
							continue
						}
						to := stored[(worker+round+1)%2]
						if _, err := repo.UpdateIssueRecord(context.Background(), workspace, move.ID, nil, IssueMutationScope{}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
							issue.Project = &domain.ProjectSummary{ID: to.ID, Name: to.Name}
							return nil
						}); err != nil {
							errs <- fmt.Errorf("move on server %d (from %v): %w", worker, current.Project, err)
						}
						rename := fmt.Sprintf("Edited %d-%d", worker, round)
						target := projects[(worker+round)%2]
						if err := repo.MutateWorkspace(context.Background(), workspace, "project.updated", target, map[string]string{"name": rename}, func(data *domain.Bootstrap) error {
							index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == target })
							if index < 0 {
								return errors.New("project missing")
							}
							data.Projects[index].Description = rename
							return nil
						}); err != nil {
							errs <- fmt.Errorf("edit on server %d: %w", worker, err)
						}
					}
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Error(err)
			}
			for _, id := range projects {
				var count int
				if err := first.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND project_id=?`, workspace, id).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if got := progressScope(storedProject(t, first, workspace, id)); got != count {
					t.Errorf("project %s progress counts %d issues, the project has %d", id, got, count)
				}
			}
		})
	}
}
