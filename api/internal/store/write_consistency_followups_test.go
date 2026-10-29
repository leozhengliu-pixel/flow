package store

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestMetadataOperationsDoNotExhaustTheRetryBudget(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			id := "retry_counter_" + raceStamp()
			metadataEdit(t, first, workspace, "import.previewed", id, func(data *domain.Bootstrap) error {
				data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: id, Status: "queued", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			})
			reloadPeers(t, second)
			increment := func(data *domain.Bootstrap) {
				at := slices.IndexFunc(data.ImportJobs, func(job domain.ImportJob) bool { return job.ID == id })
				data.ImportJobs[at].Imported++
			}
			calls := 0
			err := first.MutateWorkspace(WithMetadataFields(t.Context(), "importJobs"), workspace, "import.resumed", id, nil, func(data *domain.Bootstrap) error {
				calls++
				// The peer moves the same value after every load, including
				// every rerun if this writer incorrectly treats it as scalar.
				if err := staleMetadataWrite(t, second, workspace, increment); err != nil {
					return err
				}
				increment(data)
				return nil
			})
			if err != nil || calls != 1 {
				t.Fatalf("commutative write reran %d times: %v", calls, err)
			}
		})
	}
}

func TestMetadataMembershipOperationsMergeADeletedMapKey(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			project := raceProject(t, first, workspace)
			id := "membership_" + raceStamp()
			metadataEdit(t, first, workspace, "project_update.created", id, func(data *domain.Bootstrap) error {
				data.ProjectUpdates[project] = append(data.ProjectUpdates[project], domain.ProjectUpdate{ID: id, ProjectID: project, Body: "Status", CreatedAt: time.Now().UTC(), Reactions: map[string][]string{"+1": {"remove"}}})
				return nil
			})
			reloadPeers(t, second)
			metadataEdit(t, first, workspace, "project_update.reacted", id, func(data *domain.Bootstrap) error {
				at := slices.IndexFunc(data.ProjectUpdates[project], func(update domain.ProjectUpdate) bool { return update.ID == id })
				data.ProjectUpdates[project][at].Reactions["+1"] = []string{"remove", "keep"}
				return nil
			})
			if err := staleMetadataWrite(t, second, workspace, func(data *domain.Bootstrap) {
				at := slices.IndexFunc(data.ProjectUpdates[project], func(update domain.ProjectUpdate) bool { return update.ID == id })
				delete(data.ProjectUpdates[project][at].Reactions, "+1")
			}); err != nil {
				t.Fatal(err)
			}
			updates := storedState(t, first, workspace).ProjectUpdates[project]
			at := slices.IndexFunc(updates, func(update domain.ProjectUpdate) bool { return update.ID == id })
			if !slices.Equal(updates[at].Reactions["+1"], []string{"keep"}) {
				t.Fatalf("membership removal lost peer addition: %v", updates[at].Reactions)
			}
		})
	}
}

func TestMetadataOperationAndScalarBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, base, ours, theirs, want string
		conflict                       bool
	}{
		{"counter", `{"imported":10}`, `{"imported":11}`, `{"imported":11}`, `{"imported":12}`, false},
		{"nested counter", `{"job":{"imported":10}}`, `{"job":{"imported":11}}`, `{"job":{"imported":11}}`, `{"job":{"imported":12}}`, false},
		{"entry counter", `{"items":[{"id":"a","retryCount":1}]}`, `{"items":[{"id":"a","retryCount":2}]}`, `{"items":[{"id":"a","retryCount":2}]}`, `{"items":[{"id":"a","retryCount":3}]}`, false},
		{"counter reset is scalar", `{"imported":10}`, `{"imported":0}`, `{"imported":11}`, ``, true},
		{"priority remains scalar", `{"priority":1}`, `{"priority":2}`, `{"priority":3}`, ``, true},
		{"cursor remains scalar", `{"cursor":1}`, `{"cursor":2}`, `{"cursor":2}`, ``, true},
		{"id appends", `{"items":[]}`, `{"items":[{"id":"a"}]}`, `{"items":[{"id":"b"}]}`, `{"items":[{"id":"b"},{"id":"a"}]}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, conflict, err := mergeMetadataRecord([]byte(test.base), []byte(test.ours), []byte(test.theirs), []byte(test.theirs))
			if err != nil || conflict != test.conflict {
				t.Fatalf("merge %s conflict=%v error=%v", got, conflict, err)
			}
			if !conflict {
				var actual, want any
				if json.Unmarshal(got, &actual) != nil || json.Unmarshal([]byte(test.want), &want) != nil {
					t.Fatal("invalid JSON")
				}
				left, _ := json.Marshal(actual)
				right, _ := json.Marshal(want)
				if string(left) != string(right) {
					t.Fatalf("got %s, want %s", left, right)
				}
			}
		})
	}
}

func TestMetadataCounterOperationsUnderSustainedContention(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			id := "counter_" + raceStamp()
			metadataEdit(t, first, workspace, "import.created", id, func(data *domain.Bootstrap) error {
				data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: id, Status: "queued", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()})
				return nil
			})
			reloadPeers(t, second)
			increment := func(data *domain.Bootstrap) {
				index := slices.IndexFunc(data.ImportJobs, func(job domain.ImportJob) bool { return job.ID == id })
				data.ImportJobs[index].Imported++
				data.ImportJobs[index].RetryCount++
			}
			// Keep the second server behind for more writes than the retry budget.
			for range fullMutationAttempts + 3 {
				if err := staleMetadataWrite(t, first, workspace, increment); err != nil {
					t.Fatal(err)
				}
				if err := staleMetadataWrite(t, second, workspace, increment); err != nil {
					t.Fatal(err)
				}
			}
			data := storedState(t, first, workspace)
			index := slices.IndexFunc(data.ImportJobs, func(job domain.ImportJob) bool { return job.ID == id })
			if got := data.ImportJobs[index]; got.Imported != 2*(fullMutationAttempts+3) || got.RetryCount != got.Imported {
				t.Fatalf("increments lost: %+v", got)
			}
		})
	}
}

func TestTeamWriterLearnsDeletedRecordsWithoutReload(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, _ := racePair(t, pair)
			id := raceProject(t, first, workspace)
			reloadPeers(t, second)
			tx, err := first.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := lockMetadataRecords(t.Context(), tx, workspace, "projects", []string{id}); err != nil {
				t.Fatal(err)
			}
			if err := deleteMetadataRecordRow(t.Context(), tx, workspace, metadataRecordKey{"projects", id}); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			// Disable peer reloads to exercise the writer's stale in-memory base.
			second.SetWorkspaceCoordinator(nil)
			edit := func(data *domain.Bootstrap) error {
				index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
				if index >= 0 {
					data.Projects[index].Name = "Must not return"
				}
				return nil
			}
			metadataEdit(t, second, workspace, "alm.projects_imported", id, edit)
			data, _ := second.WorkspaceMetadataFields(workspace, "projects")
			if slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == id }) {
				t.Fatal("writer still serves the deleted project")
			}
			metadataEdit(t, second, workspace, "alm.projects_imported", id, edit)
			if slices.ContainsFunc(storedState(t, first, workspace).Projects, func(project domain.Project) bool { return project.ID == id }) {
				t.Fatal("deleted project resurrected")
			}
		})
	}
}

func TestTeamWriterDeepBaseKeepsInPlaceNestedSliceEdit(t *testing.T) {
	for name, pair := range peerStoreSets(t) {
		t.Run(name, func(t *testing.T) {
			first, second, workspace, template := racePair(t, pair)
			team := template.Team.ID
			metadataEdit(t, first, workspace, "team.settings_updated", team, func(data *domain.Bootstrap) error {
				settings := data.TeamSettings[team]
				settings.TriageActionUserIDs = []string{"old", "keep"}
				data.TeamSettings[team] = settings
				return nil
			})
			reloadPeers(t, second)
			second.SetWorkspaceCoordinator(nil)
			metadataEdit(t, first, workspace, "team.settings_updated", team, func(data *domain.Bootstrap) error {
				settings := data.TeamSettings[team]
				settings.Description = "Other server"
				data.TeamSettings[team] = settings
				return nil
			})
			metadataEdit(t, second, workspace, "team.settings_updated", team, func(data *domain.Bootstrap) error {
				data.TeamSettings[team].TriageActionUserIDs[0] = "new"
				return nil
			})
			got := storedState(t, first, workspace).TeamSettings[team]
			if got.Description != "Other server" || !slices.Equal(got.TriageActionUserIDs, []string{"keep", "new"}) && !slices.Equal(got.TriageActionUserIDs, []string{"new", "keep"}) {
				t.Fatalf("nested edit or peer change lost: %+v", got)
			}
			id := raceProject(t, first, workspace)
			metadataEdit(t, first, workspace, "project.updated", id, func(data *domain.Bootstrap) error {
				at := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
				data.Projects[at].MemberIDs = []string{"old", "keep"}
				return nil
			})
			reloadPeers(t, second)
			metadataEdit(t, first, workspace, "project.updated", id, func(data *domain.Bootstrap) error {
				at := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
				data.Projects[at].Name = "Peer project"
				return nil
			})
			metadataEdit(t, second, workspace, "alm.projects_imported", id, func(data *domain.Bootstrap) error {
				at := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == id })
				data.Projects[at].MemberIDs[0] = "new"
				return nil
			})
			project := storedProject(t, first, workspace, id)
			if project.Name != "Peer project" || !slices.Contains(project.MemberIDs, "new") || slices.Contains(project.MemberIDs, "old") {
				t.Fatalf("catalog nested edit or peer change lost: %+v", project)
			}
		})
	}
}
