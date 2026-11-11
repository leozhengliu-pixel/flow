package store

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueActivityRulesPersistAcrossStores(t *testing.T) {
	type edit struct {
		at       time.Duration
		actor    string
		kind     string
		metadata map[string]string
	}
	state := func(from, to string) map[string]string {
		return map[string]string{"stateBefore": from, "stateBeforeId": from, "state": to, "stateId": to}
	}
	first := edit{at: 5 * time.Minute, metadata: state("In Progress", "Done")}
	tests := []struct {
		name  string
		edits []edit
		count int
		from  string
		to    string
	}{
		{"round trip", []edit{first, {at: 6 * time.Minute, metadata: state("Done", "In Progress")}}, 0, "", ""},
		{"status burst", []edit{first, {at: 6 * time.Minute, metadata: state("Done", "Todo")}}, 1, "In Progress", "Todo"},
		{"comment barrier", []edit{first, {at: 6 * time.Minute, kind: "comment"}, {at: 7 * time.Minute, metadata: state("Done", "Todo")}}, 2, "", ""},
		{"other actor", []edit{first, {at: 6 * time.Minute, actor: "other", metadata: state("Done", "Todo")}}, 2, "", ""},
		{"61 minute gap", []edit{first, {at: 66 * time.Minute, metadata: state("Done", "Todo")}}, 2, "", ""},
		{"60 minute boundary", []edit{first, {at: 65 * time.Minute, metadata: state("Done", "Todo")}}, 2, "", ""},
		{"rolling window", []edit{first, {at: 50 * time.Minute, metadata: state("Done", "Todo")}, {at: 90 * time.Minute, metadata: state("Todo", "Canceled")}}, 1, "In Progress", "Canceled"},
		{"creator grace", []edit{{at: 2*time.Minute + 59*time.Second, metadata: state("Todo", "Done")}}, 0, "", ""},
		{"other actor during grace", []edit{{at: time.Minute, actor: "other", metadata: state("Todo", "Done")}}, 1, "Todo", "Done"},
		{"grace boundary", []edit{{at: 3 * time.Minute, metadata: state("Todo", "Done")}}, 1, "Todo", "Done"},
		{"label round trip", []edit{{at: 5 * time.Minute, metadata: map[string]string{"labels": "bug", "addedLabelIds": `["bug"]`, "removedLabelIds": `[]`}}, {at: 6 * time.Minute, metadata: map[string]string{"labels": "", "addedLabelIds": `[]`, "removedLabelIds": `["bug"]`}}}, 0, "", ""},
		{"label remove and restore", []edit{{at: 5 * time.Minute, metadata: map[string]string{"labels": "", "addedLabelIds": `[]`, "removedLabelIds": `["bug"]`}}, {at: 6 * time.Minute, metadata: map[string]string{"labels": "bug", "addedLabelIds": `["bug"]`, "removedLabelIds": `[]`}}}, 0, "", ""},
		{"estimate round trip", []edit{{at: 5 * time.Minute, metadata: map[string]string{"estimateBefore": "", "estimate": "2"}}, {at: 6 * time.Minute, metadata: map[string]string{"estimateBefore": "2", "estimate": ""}}}, 0, "", ""},
		{"priority round trip", []edit{{at: 5 * time.Minute, metadata: map[string]string{"priorityBefore": "No priority", "priority": "High"}}, {at: 6 * time.Minute, metadata: map[string]string{"priorityBefore": "High", "priority": "No priority"}}}, 0, "", ""},
		{"cycle round trip", []edit{{at: 5 * time.Minute, metadata: map[string]string{"cycleBefore": "", "cycle": "cycle-activity"}}, {at: 6 * time.Minute, metadata: map[string]string{"cycleBefore": "cycle-activity", "cycle": ""}}}, 0, "", ""},
		{"relation barrier", []edit{first, {at: 6 * time.Minute, kind: "issue.relation_added", metadata: map[string]string{"relationType": "related", "relatedIssueId": "target"}}, {at: 7 * time.Minute, metadata: state("Done", "Todo")}}, 3, "", ""},
		{"link barrier", []edit{first, {at: 6 * time.Minute, kind: "issue.link_created"}, {at: 7 * time.Minute, metadata: state("Done", "Todo")}}, 3, "", ""},
		{"duplicate suppression", []edit{{at: 5 * time.Minute, metadata: map[string]string{"stateBefore": "Todo", "state": "Duplicate", "viaDuplicate": "true", "priorityBefore": "Low", "priority": "High"}}}, 1, "", ""},
	}
	for driver, pair := range peerStoreSets(t) {
		t.Run(driver, func(t *testing.T) {
			writer, reader := pair[0], pair[1]
			workspace, template := peerWorkspace(t, writer)
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					issue := createPeerIssue(t, writer, workspace, "activity_"+raceStamp(), template)
					var firstID string
					for index, change := range test.edits {
						if change.kind == "comment" {
							err := writer.MutateWorkspace(WithIssueRecordMutations(t.Context(), issue.ID), workspace, "comment.created", issue.ID, nil, func(data *domain.Bootstrap) error {
								data.Comments[issue.ID] = append(data.Comments[issue.ID], domain.Comment{ID: "comment_" + issue.ID, Body: "Interrupt", User: data.Viewer, CreatedAt: issue.CreatedAt.Add(change.at)})
								return nil
							})
							if err != nil {
								t.Fatal(err)
							}
							continue
						}
						_, err := writer.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, IssueMutationScope{}, func(data *domain.Bootstrap, current *domain.Issue) error {
							current.Creator = data.Viewer
							at := issue.CreatedAt.Add(change.at)
							kind := change.kind
							if kind == "" {
								kind = "issue.updated"
							}
							actor := data.Viewer
							if change.actor != "" {
								actor.ID = change.actor
							}
							event := domain.ActivityEvent{ID: fmt.Sprintf("%s_%d", issue.ID, index), Type: kind, Actor: actor, CreatedAt: at, Metadata: change.metadata}
							if index == 0 {
								firstID = event.ID
							}
							RecordIssueActivity(data, issue.ID, event)
							return nil
						})
						if err != nil {
							t.Fatal(err)
						}
					}
					history, err := reader.IssueHistoryPage(t.Context(), workspace, issue.ID, "", "")
					if err != nil {
						t.Fatal(err)
					}
					var events []domain.ActivityEvent
					for _, event := range history.Activities {
						if event.Type != "issue.created" {
							events = append(events, event)
						}
					}
					if len(events) != test.count {
						t.Fatalf("events = %+v, want %d", events, test.count)
					}
					if test.from != "" && (events[0].Metadata["stateBefore"] != test.from || events[0].Metadata["state"] != test.to) {
						t.Fatalf("unexpected status endpoints: %+v", events[0])
					}
					if test.name == "status burst" || test.name == "rolling window" {
						event := events[0]
						last := test.edits[len(test.edits)-1]
						if event.ID != firstID || !event.CreatedAt.Equal(issue.CreatedAt.Add(first.at)) || event.UpdatedAt == nil || !event.UpdatedAt.Equal(issue.CreatedAt.Add(last.at)) {
							t.Fatalf("burst identity/timestamps: %+v", event)
						}
					}
					if test.name == "duplicate suppression" {
						if _, exists := events[0].Metadata["state"]; exists {
							t.Fatalf("duplicate state leaked: %+v", events[0])
						}
					}
					if test.name == "cycle round trip" {
						ids, err := reader.CycleHistoryIssueIDs(t.Context(), workspace, "cycle-activity")
						if err != nil || slices.Contains(ids, issue.ID) {
							t.Fatalf("canceled activity left a cycle reference: %v %v", ids, err)
						}
					}
				})
			}
		})
	}
}

func TestIssueActivityMergeFieldsAndLabels(t *testing.T) {
	data := domain.Bootstrap{}
	at := time.Now().UTC()
	appendEdit := func(id string, changes map[string]string) {
		RecordIssueActivity(&data, "issue", domain.ActivityEvent{ID: id, Type: "issue.updated", Actor: domain.User{ID: "actor"}, CreatedAt: at, Metadata: changes})
		at = at.Add(time.Minute)
	}
	appendEdit("first", map[string]string{"titleBefore": "A", "title": "B", "priorityBefore": "Low", "priority": "High", "labels": "a,b", "addedLabelIds": `["b"]`, "removedLabelIds": `[]`})
	appendEdit("second", map[string]string{"titleBefore": "B", "title": "A", "dueDateBefore": "", "dueDate": "2026-11-11", "labels": "b,c", "addedLabelIds": `["c"]`, "removedLabelIds": `["a"]`})
	event := data.Activities["issue"][0]
	if len(data.Activities["issue"]) != 1 || event.Metadata["title"] != "" || event.Metadata["priorityBefore"] != "Low" || event.Metadata["dueDateBefore"] != "" || event.Metadata["labels"] != "b,c" {
		t.Fatalf("merged fields: %+v", event)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wire.Metadata["addedLabelIds"], []any{"b", "c"}) || !reflect.DeepEqual(wire.Metadata["removedLabelIds"], []any{"a"}) {
		t.Fatalf("label wire arrays: %s", raw)
	}
}

func TestIssueActivityLegacyBeforeValuesStayUnknown(t *testing.T) {
	merged := mergeIssueActivityMetadata(domain.ActivityMetadata{"priority": "High"}, domain.ActivityMetadata{"priorityBefore": "High", "priority": "Low"})
	if _, exists := merged["priorityBefore"]; exists || merged["priority"] != "Low" {
		t.Fatalf("fabricated old before-value: %+v", merged)
	}
}
