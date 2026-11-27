package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const labelWorkflowFrom = "label_workflow_from"
const labelWorkflowTo = "label_workflow_to"

type labelWorkflowFixture struct {
	repo      *store.SQLiteStore
	handler   http.Handler
	workspace string
	reopen    func() (*store.SQLiteStore, error)
	events    *[]domain.RealtimeEvent
}

func newLabelWorkflowFixture(t *testing.T, bounded bool, seed func(*domain.Bootstrap)) labelWorkflowFixture {
	t.Helper()
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	path := filepath.Join(t.TempDir(), "labels.db")
	repo, err := store.OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	f := seedLabelWorkflowFixture(t, repo, bounded, seed)
	f.reopen = func() (*store.SQLiteStore, error) { return store.OpenSQLiteTestFixture(path) }
	return f
}

func seedLabelWorkflowFixture(t *testing.T, repo *store.SQLiteStore, bounded bool, seed func(*domain.Bootstrap)) labelWorkflowFixture {
	t.Helper()
	workspace := repo.Bootstrap().Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "test.label_workflow_setup", "", nil, func(data *domain.Bootstrap) error {
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: labelWorkflowFrom, Name: "Source label", Scope: "Workspace", ResourceType: "issue"},
			domain.IssueLabel{ID: labelWorkflowTo, Name: "Target label", Scope: "Workspace", ResourceType: "issue"})
		seed(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bounded {
		var err error
		repo, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}
	service := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	disableBackgroundWorkersForTest(service)
	handler := newHandler(service)
	var events []domain.RealtimeEvent
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { events = append(events, event) })
	t.Cleanup(func() { repo.SetRealtimeSink(nil) })
	return labelWorkflowFixture{repo: repo, handler: handler, workspace: workspace, events: &events}
}

func (f labelWorkflowFixture) check(t *testing.T, eventType string, check func(domain.Bootstrap)) {
	t.Helper()
	if len(*f.events) != 1 || (*f.events)[0].Type != eventType {
		t.Fatalf("expected one %s event, got %#v", eventType, *f.events)
	}
	read := func(repo *store.SQLiteStore) domain.Bootstrap {
		data, ok := repo.WorkspaceMetadataFields(f.workspace, "labels", "workflowDefinitions", "loops")
		if !ok {
			t.Fatal("automation metadata missing")
		}
		return data
	}
	data := read(f.repo)
	check(data)
	if f.reopen != nil {
		reloaded, err := f.reopen()
		if err != nil {
			t.Fatal(err)
		}
		defer reloaded.Close()
		fresh := read(reloaded)
		if !sameJSON(data.WorkflowDefinitions, fresh.WorkflowDefinitions) || !sameJSON(data.Loops, fresh.Loops) {
			t.Fatal("installed automations differ from the reloaded records")
		}
	}
}

func labelWorkflow(id, teamID, labelID string, conditions map[string]string) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		ID: id, Name: id, TeamID: teamID, Trigger: "issueCreated", Enabled: true,
		Conditions: conditions,
		Actions: []domain.WorkflowAction{
			{Type: "addIssueLabel", Config: map[string]string{"labelId": labelID}},
			{Type: "removeIssueLabel", Config: map[string]string{"labelId": labelID}},
			{Type: "notify", Config: map[string]string{"message": labelWorkflowFrom}},
		},
	}
}

func labelWorkflowByID(t *testing.T, data domain.Bootstrap, id string) domain.WorkflowDefinition {
	t.Helper()
	if index := slices.IndexFunc(data.WorkflowDefinitions, func(item domain.WorkflowDefinition) bool { return item.ID == id }); index >= 0 {
		return data.WorkflowDefinitions[index]
	}
	t.Fatalf("workflow %s missing", id)
	return domain.WorkflowDefinition{}
}

func labelLoopByID(t *testing.T, data domain.Bootstrap, id string) domain.Loop {
	t.Helper()
	if index := slices.IndexFunc(data.Loops, func(item domain.Loop) bool { return item.ID == id }); index >= 0 {
		return data.Loops[index]
	}
	t.Fatalf("loop %s missing", id)
	return domain.Loop{}
}

func checkLabelActions(t *testing.T, workflow domain.WorkflowDefinition, labelID string) {
	t.Helper()
	want := labelWorkflow(workflow.ID, "", labelID, nil).Actions
	if labelID == "" {
		want = want[2:]
	}
	if !reflect.DeepEqual(workflow.Actions, want) {
		t.Fatalf("workflow %s actions = %v, want %v", workflow.ID, workflow.Actions, want)
	}
}

func checkLabelWorkflowMatch(t *testing.T, workflow domain.WorkflowDefinition, issue domain.Issue, want bool) {
	t.Helper()
	data := domain.Bootstrap{Issues: []domain.Issue{issue}}
	if got := workflowConditionsMatch(&data, workflow, "issue", issue.ID); got != want {
		t.Fatalf("workflow %s matched = %t, want %t", workflow.ID, got, want)
	}
}

func checkLabelLoopMatch(t *testing.T, loop domain.Loop, issue domain.Issue, want bool) {
	t.Helper()
	event := loopEvent{EntityType: "issue", Kind: "created", Issue: &issue}
	if got, _ := matchLoopEvent(domain.Bootstrap{}, loop, event); got != want {
		t.Fatalf("loop %s matched = %t, want %t", loop.ID, got, want)
	}
}

func TestLabelWorkflowReferencesMerge(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run(fmt.Sprintf("bounded=%t", bounded), func(t *testing.T) {
			checkLabelWorkflowMerge(t, func(seed func(*domain.Bootstrap)) labelWorkflowFixture {
				return newLabelWorkflowFixture(t, bounded, seed)
			})
		})
	}
	t.Run("full fallback", func(t *testing.T) {
		checkLabelWorkflowMerge(t, func(seed func(*domain.Bootstrap)) labelWorkflowFixture {
			f := newLabelWorkflowFixture(t, false, seed)
			t.Cleanup(store.ForceFullMutationsForTesting(true))
			return f
		})
	})
}

func checkLabelWorkflowMerge(t *testing.T, fixture func(func(*domain.Bootstrap)) labelWorkflowFixture) {
	t.Helper()
	unrelated := labelWorkflow("unrelated-workflow", "", labelWorkflowFrom+"-other", nil)
	keepLoop := domain.Loop{ID: "unrelated-loop", TriggerType: "issue", TriggerConfig: map[string]any{"event": "labels", "value": labelWorkflowFrom + "-other"}, Instructions: labelWorkflowFrom}
	f := fixture(func(data *domain.Bootstrap) {
		data.WorkflowDefinitions = append(data.WorkflowDefinitions, unrelated,
			labelWorkflow("merged-workflow", "", labelWorkflowFrom, map[string]string{
				"labelId": labelWorkflowFrom,
				"filters": `{"and":[{"or":[{"field":"labelId","operator":"in","values":["` + labelWorkflowFrom + `","` + labelWorkflowTo + `"]},{"field":"priority","value":"2"}]}]}`,
			}))
		data.Loops = append(data.Loops, keepLoop,
			domain.Loop{ID: "merged-loop", TriggerType: "issue", TriggerConfig: map[string]any{
				"event": "labels", "value": labelWorkflowFrom, "filter": true, "filterField": "label", "filterValue": labelWorkflowFrom,
			}})
	})
	requestJSON[domain.IssueLabel](t, f.handler, http.MethodPost, "/api/labels/merge", labelMergeInput{ToLabelID: labelWorkflowTo, FromLabelIDs: []string{labelWorkflowFrom}}, http.StatusOK)
	f.check(t, "label.merged", func(data domain.Bootstrap) {
		workflow := labelWorkflowByID(t, data, "merged-workflow")
		checkLabelActions(t, workflow, labelWorkflowTo)
		if workflow.Conditions["labelId"] != labelWorkflowTo || strings.Count(workflow.Conditions["filters"], labelWorkflowTo) != 1 || strings.Contains(workflow.Conditions["filters"], labelWorkflowFrom) {
			t.Fatalf("merged conditions = %v", workflow.Conditions)
		}
		loop := labelLoopByID(t, data, "merged-loop")
		if loop.TriggerConfig["value"] != labelWorkflowTo || loop.TriggerConfig["filterValue"] != labelWorkflowTo {
			t.Fatalf("merged trigger = %v", loop.TriggerConfig)
		}
		issue := domain.Issue{ID: "issue", Labels: []domain.IssueLabel{{ID: labelWorkflowTo}}}
		checkLabelWorkflowMatch(t, workflow, issue, true)
		checkLabelLoopMatch(t, loop, issue, true)
		if !sameJSON(labelWorkflowByID(t, data, unrelated.ID), unrelated) || !sameJSON(labelLoopByID(t, data, keepLoop.ID), keepLoop) {
			t.Fatal("rewrite changed a record that only mentioned the label in text")
		}
	})
}

func TestWorkflowLabelExcludesAllAfterMerge(t *testing.T) {
	const otherLabel = "other-label"
	workflow := domain.WorkflowDefinition{ID: "excludes-all", Conditions: map[string]string{
		"filters": `{"field":"labels","operator":"excludesAll","values":[{"id":"label:` + labelWorkflowFrom + `","value":"` + labelWorkflowFrom + `"},"` + labelWorkflowTo + `","` + otherLabel + `"]}`,
	}}
	workflow, changed := remapWorkflowLabels(workflow, func(id string) (string, bool) {
		return labelWorkflowTo, id == labelWorkflowFrom
	})
	want := `{"field":"labels","operator":"excludesAll","values":[{"id":"label:` + labelWorkflowTo + `","value":"` + labelWorkflowTo + `"},"` + otherLabel + `"]}`
	if !changed || workflow.Conditions["filters"] != want {
		t.Fatalf("merged filter = %s, want %s", workflow.Conditions["filters"], want)
	}
	issue := domain.Issue{ID: "issue", Labels: []domain.IssueLabel{{ID: labelWorkflowTo}}}
	checkLabelWorkflowMatch(t, workflow, issue, true)
	issue.Labels = append(issue.Labels, domain.IssueLabel{ID: otherLabel})
	checkLabelWorkflowMatch(t, workflow, issue, false)
}

func TestLabelWorkflowReferencesDelete(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprintf("group=%t", group), func(t *testing.T) {
			checkLabelWorkflowDelete(t, group, func(seed func(*domain.Bootstrap)) labelWorkflowFixture {
				return newLabelWorkflowFixture(t, group, seed)
			})
		})
	}
}

func checkLabelWorkflowDelete(t *testing.T, group bool, fixture func(func(*domain.Bootstrap)) labelWorkflowFixture) {
	t.Helper()
	f := fixture(func(data *domain.Bootstrap) {
		if group {
			group := domain.LabelGroup{ID: "label_group_workflow", Scope: "Workspace", ResourceType: "issue"}
			data.LabelGroups = append(data.LabelGroups, group)
			for i := range data.Labels {
				if data.Labels[i].ID == labelWorkflowFrom || data.Labels[i].ID == labelWorkflowTo {
					data.Labels[i].GroupID = group.ID
				}
			}
		}
		data.WorkflowDefinitions = append(data.WorkflowDefinitions,
			labelWorkflow("required-workflow", "", labelWorkflowFrom, map[string]string{"labelId": labelWorkflowFrom}),
			labelWorkflow("either-workflow", "", labelWorkflowFrom, map[string]string{
				"filters": `{"and":[{"or":[{"field":"labelId","value":"` + labelWorkflowFrom + `"},{"field":"labelId","value":"` + labelWorkflowTo + `"}]}]}`,
			}))
		data.Loops = append(data.Loops,
			domain.Loop{ID: "deleted-trigger", TriggerType: "issue", TriggerConfig: map[string]any{"event": "labels", "value": labelWorkflowFrom}},
			domain.Loop{ID: "deleted-legacy-filter", TriggerType: "issue", TriggerConfig: map[string]any{"event": "created", "filter": true, "filterField": "label", "filterOperator": "isNot", "filterValue": labelWorkflowFrom}},
			domain.Loop{ID: "required-nested-filter", TriggerType: "issue", TriggerConfig: map[string]any{
				"event": "created", "filters": map[string]any{"conjunction": "or", "items": []any{
					map[string]any{"and": []any{
						map[string]any{"field": "label", "operator": "in", "values": []any{labelWorkflowFrom, labelWorkflowTo}},
						map[string]any{"field": "priority", "operator": "is", "value": "2"},
					}},
					map[string]any{"field": "priority", "operator": "is", "value": "1"},
				}},
			}})
	})
	event, path := "label.deleted", "/api/labels/"+labelWorkflowFrom
	if group {
		event, path = "label_group.deleted", "/api/label-groups/label_group_workflow"
	}
	requestJSON[any](t, f.handler, http.MethodDelete, path, nil, http.StatusNoContent)
	f.check(t, event, func(data domain.Bootstrap) {
		for _, item := range []struct {
			id        string
			wantMatch bool
		}{{"required-workflow", false}, {"either-workflow", !group}} {
			w := labelWorkflowByID(t, data, item.id)
			checkLabelActions(t, w, "")
			if !w.Enabled || strings.Contains(fmt.Sprint(w.Conditions), labelWorkflowFrom) {
				t.Fatalf("workflow %s retained the retired condition operand or was switched off", w.ID)
			}
			checkLabelWorkflowMatch(t, w, domain.Issue{ID: "issue", Labels: []domain.IssueLabel{{ID: labelWorkflowTo}}}, item.wantMatch)
			checkLabelWorkflowMatch(t, w, domain.Issue{ID: "issue"}, false)
		}
		issue := domain.Issue{ID: "issue", Priority: 2, Labels: []domain.IssueLabel{{ID: labelWorkflowTo}}}
		checkLabelLoopMatch(t, labelLoopByID(t, data, "deleted-trigger"), issue, false)
		checkLabelLoopMatch(t, labelLoopByID(t, data, "deleted-legacy-filter"), issue, false)
		loop := labelLoopByID(t, data, "required-nested-filter")
		checkLabelLoopMatch(t, loop, issue, !group)
		issue.Labels = nil
		checkLabelLoopMatch(t, loop, issue, false)
		issue.Priority = 1
		checkLabelLoopMatch(t, loop, issue, true)
		for _, loop := range data.Loops {
			if strings.Contains(fmt.Sprint(loop.TriggerConfig), labelWorkflowFrom) || group && strings.Contains(fmt.Sprint(loop.TriggerConfig), labelWorkflowTo) {
				t.Fatalf("loop %s still refers to a retired label", loop.ID)
			}
		}
	})
}

func TestLabelWorkflowReferencesMove(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprintf("group=%t", group), func(t *testing.T) {
			checkLabelWorkflowMove(t, group, func(seed func(*domain.Bootstrap)) labelWorkflowFixture {
				return newLabelWorkflowFixture(t, !group, seed)
			})
		})
	}
}

func checkLabelWorkflowMove(t *testing.T, group bool, fixture func(func(*domain.Bootstrap)) labelWorkflowFixture) {
	t.Helper()
	const (
		parent       = "test-label-parent"
		child        = "test-label-child"
		private      = "test-label-private"
		privateChild = "test-label-private-child"
	)
	f := fixture(func(data *domain.Bootstrap) {
		data.Teams = append(data.Teams,
			domain.Team{ID: parent, Name: "Parent", Key: "LWP"},
			domain.Team{ID: child, Name: "Child", Key: "LWC"},
			domain.Team{ID: private, Name: "Private", Key: "LWS", Private: true},
			domain.Team{ID: privateChild, Name: "Private child", Key: "LWSC"})
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: parent}
		data.TeamSettings[privateChild] = domain.TeamSettings{TeamID: privateChild, ParentTeamID: private}
		if group {
			data.LabelGroups = append(data.LabelGroups, domain.LabelGroup{ID: "label_group_workflow", Name: "Workflow group", Scope: "Workspace", ResourceType: "issue"})
			for i := range data.Labels {
				if data.Labels[i].ID == labelWorkflowFrom || data.Labels[i].ID == labelWorkflowTo {
					data.Labels[i].GroupID = "label_group_workflow"
				}
			}
		}
		conditions := func(teamID string) map[string]string {
			result := map[string]string{"labelId": labelWorkflowFrom}
			if teamID != "" {
				result["teamId"] = teamID
			}
			return result
		}
		data.WorkflowDefinitions = append(data.WorkflowDefinitions,
			labelWorkflow("parent-workflow", parent, labelWorkflowFrom, conditions("")),
			labelWorkflow("child-workflow", child, labelWorkflowFrom, conditions("")),
			labelWorkflow("private-workflow", private, labelWorkflowFrom, conditions("")),
			labelWorkflow("narrow-workspace", "", labelWorkflowFrom, conditions(child)),
			labelWorkflow("wide-workspace", "", labelWorkflowFrom, conditions("")),
			labelWorkflow("private-workspace", "", labelWorkflowFrom, conditions(privateChild)))
		data.Loops = append(data.Loops,
			domain.Loop{ID: "child-loop", Level: "team", TeamID: child, TriggerType: "issue", TriggerConfig: map[string]any{
				"event": "labels", "value": labelWorkflowFrom,
			}},
			domain.Loop{ID: "narrow-loop", Level: "workspace", TriggerType: "issue", TriggerConfig: map[string]any{
				"event": "labels", "value": labelWorkflowFrom, "filters": map[string]any{"or": []any{
					map[string]any{"teamId": child},
					map[string]any{"field": "team", "operator": "is", "value": "LWC"},
				}},
			}},
			domain.Loop{ID: "wide-loop", Level: "workspace", TriggerType: "issue", TriggerConfig: map[string]any{
				"event": "labels", "value": labelWorkflowFrom, "teamIds": []string{child, parent},
			}},
			domain.Loop{ID: "private-loop", Level: "workspace", TriggerType: "issue", TeamAccess: "selected", TriggerConfig: map[string]any{
				"event": "labels", "value": labelWorkflowFrom, "teamIds": []string{privateChild},
			}})
	})
	event, path := "label.moved_to_teams", "/api/labels/"+labelWorkflowFrom+"/move-to-teams"
	if group {
		event, path = "label_group.moved_to_teams", "/api/label-groups/label_group_workflow/move-to-teams"
	}
	requestJSON[any](t, f.handler, http.MethodPost, path, nil, http.StatusOK)
	f.check(t, event, func(data domain.Bootstrap) {
		pub := teamLabelNamed(t, data.Labels, parent, "Source label")
		hidden := teamLabelNamed(t, data.Labels, private, "Source label")
		for _, label := range data.Labels {
			if label.ID == labelWorkflowFrom || label.Name == "Source label" && (label.Scope == child || label.Scope == privateChild) {
				t.Fatalf("move kept the old label or made a redundant child copy: %#v", label)
			}
		}
		for id, want := range map[string]string{
			"parent-workflow": pub.ID, "child-workflow": pub.ID, "private-workflow": hidden.ID,
			"narrow-workspace": pub.ID, "wide-workspace": "", "private-workspace": "",
		} {
			w := labelWorkflowByID(t, data, id)
			checkLabelActions(t, w, want)
			if w.Conditions["labelId"] != want {
				t.Fatalf("workflow %s condition = %q, want %q", id, w.Conditions["labelId"], want)
			}
		}
		for id, want := range map[string]any{"child-loop": pub.ID, "narrow-loop": pub.ID, "wide-loop": nil, "private-loop": nil} {
			loop := labelLoopByID(t, data, id)
			if value, set := loop.TriggerConfig["value"]; !set || value != want {
				t.Fatalf("loop %s label trigger = %v, want %v", id, value, want)
			}
		}
	})
}

func TestWorkspaceLoopLabelMoveRequiresTeamRestriction(t *testing.T) {
	team := domain.Team{ID: "team-only", Key: "ONLY"}
	data := domain.Bootstrap{Teams: []domain.Team{team}}
	loop := domain.Loop{Level: "workspace", TriggerType: "issue", TriggerConfig: map[string]any{"event": "labels", "value": labelWorkflowFrom}}
	if got := loopLabelTeam(&data, loop); got != "" {
		t.Fatalf("unrestricted loop selected %q", got)
	}
	loop.TriggerConfig["teamIds"] = []string{team.ID}
	if got := loopLabelTeam(&data, loop); got != team.ID {
		t.Fatalf("single-team loop selected %q, want %q", got, team.ID)
	}
}

func TestLabelWorkflowReferencesLoopVersionRestore(t *testing.T) {
	f := newLabelWorkflowFixture(t, false, func(data *domain.Bootstrap) {
		loop := domain.Loop{
			ID: "loop_label_version", Name: "Versioned", Status: "published", Level: "workspace",
			OwnerID: data.Viewer.ID, TriggerType: "issue", Instructions: "Summarize.",
			TriggerConfig: map[string]any{"event": "labels", "value": labelWorkflowFrom},
		}
		recordLoopVersion(data, nil, &loop, data.Workspace.CreatedAt, 0)
		data.Loops = append(data.Loops, loop)
	})
	path := "/api/loops/loop_label_version/versions/loop_label_version_v1"
	requestJSON[any](t, f.handler, http.MethodDelete, "/api/labels/"+labelWorkflowFrom, nil, http.StatusNoContent)
	version := requestJSON[domain.LoopVersion](t, f.handler, http.MethodGet, path, nil, http.StatusOK)
	if version.Definition.TriggerConfig["value"] != labelWorkflowFrom {
		t.Fatal("label delete rewrote the published snapshot")
	}
	loop := requestJSON[domain.Loop](t, f.handler, http.MethodPost, path+"/restore", nil, http.StatusOK)
	if value, present := loop.TriggerConfig["value"]; !present || value != nil {
		t.Fatalf("restored trigger = %v", loop.TriggerConfig)
	}
	checkLabelLoopMatch(t, loop, domain.Issue{Labels: []domain.IssueLabel{{ID: labelWorkflowTo}}}, false)
}

func TestExternalLabelWorkflowReferences(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver != "mysql" && driver != "postgres" || url == "" {
		t.Skip("set FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL for a disposable database")
	}
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := store.DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	for _, operation := range []string{"merge", "delete-label", "delete-group", "move-label", "move-group"} {
		t.Run(operation, func(t *testing.T) {
			if _, err := repo.CreateWorkspace(t.Context(), "Label automation test", "label-refs-"+operation, "us"); err != nil {
				t.Fatal(err)
			}
			fixture := func(seed func(*domain.Bootstrap)) labelWorkflowFixture {
				f := seedLabelWorkflowFixture(t, repo, true, seed)
				f.reopen = func() (*store.SQLiteStore, error) { return store.OpenDatabase(config) }
				return f
			}
			switch operation {
			case "merge":
				checkLabelWorkflowMerge(t, fixture)
			case "delete-label", "delete-group":
				checkLabelWorkflowDelete(t, operation == "delete-group", fixture)
			case "move-label", "move-group":
				checkLabelWorkflowMove(t, operation == "move-group", fixture)
			}
		})
	}
}
