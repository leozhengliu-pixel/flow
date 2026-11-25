package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestIssueMutationLoadsHistoryOnlyForTouchedIssues(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(t.TempDir() + "/history-scope.db")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	workspace, target := peerWorkspace(t, repo)
	child := createPeerIssue(t, repo, workspace, "history_scope_child_"+raceStamp(), target)
	parentID := target.ID
	if _, err := repo.UpdateIssueRecord(t.Context(), workspace, child.ID, nil, IssueMutationScope{NewParentID: parentID, IncludeFamily: true}, func(data *domain.Bootstrap, issue *domain.Issue) error {
		issue.ParentID = &parentID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Backdate the target (past the creator grace window) before the probe.
	// Moving the project's earliest issue to the previous UTC day rebuilds
	// its progress history from every member's activity, so doing it inside
	// the probed edit read the malformed head between 00:00 and 01:00 UTC.
	if _, err := repo.UpdateIssueRecord(t.Context(), workspace, target.ID, nil, IssueMutationScope{IncludeFamily: true}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
		issue.CreatedAt = time.Now().UTC().Add(-time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// An invalid, untouched head makes an accidental family/workspace scan
	// fail loudly. The target's lazy head lookup must never inspect it.
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO workspace_content_records(workspace_key,kind,resource_id,id,created_at,data,owner_id,parent_id,status,next_attempt_at,record_version) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, workspace, "activity", child.ID, "malformed-history", time.Now().UTC().Format(time.RFC3339Nano), []byte("{"), "", "", "", "", 1); err != nil {
		t.Fatal(err)
	}
	heads := 0
	ctx := WithSQLStatementObserver(t.Context(), func(query string) {
		if strings.Contains(query, "workspace_content_records") && strings.Contains(query, "ORDER BY created_at DESC,id DESC LIMIT 1") {
			heads++
		}
	})
	_, err = repo.UpdateIssueRecord(ctx, workspace, target.ID, nil, IssueMutationScope{IncludeFamily: true}, func(data *domain.Bootstrap, issue *domain.Issue) error {
		before := issue.Title
		issue.Title = "history scope target"
		RecordIssueActivity(data, issue.ID, domain.ActivityEvent{ID: "history-scope-event", Type: "issue.updated", CreatedAt: time.Now().UTC(), Actor: data.Viewer, Metadata: domain.ActivityMetadata{"titleBefore": before, "title": "history scope target"}})
		return nil
	})
	if err != nil {
		t.Fatalf("target update read an untouched issue head: %v", err)
	}
	if heads != 2 {
		t.Fatalf("one touched issue read %d history heads, want 2", heads)
	}
	var count int
	if err := repo.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM workspace_content_records WHERE workspace_key=? AND kind='activity' AND resource_id=? AND id=?`, workspace, child.ID, "malformed-history").Scan(&count); err != nil || count != 1 {
		t.Fatalf("untouched malformed head changed: count=%d err=%v", count, err)
	}
	cascade := createPeerIssue(t, repo, workspace, "history_cascade_"+raceStamp(), target)
	_, err = repo.UpdateIssueRecord(t.Context(), workspace, cascade.ID, nil, IssueMutationScope{NewParentID: parentID, IncludeFamily: true}, func(_ *domain.Bootstrap, issue *domain.Issue) error {
		issue.ParentID = &parentID
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	heads = 0
	_, err = repo.UpdateIssueRecord(ctx, workspace, target.ID, nil, IssueMutationScope{IncludeFamily: true}, func(data *domain.Bootstrap, issue *domain.Issue) error {
		for i := range data.Issues {
			item := &data.Issues[i]
			if item.ID != issue.ID && item.ID != cascade.ID {
				continue
			}
			before := item.Title
			item.Title, item.CreatedAt = "Cascaded title", time.Now().UTC().Add(-time.Hour)
			RecordIssueActivity(data, item.ID, domain.ActivityEvent{ID: "cascade-" + item.ID, Type: "issue.updated", CreatedAt: time.Now().UTC(), Actor: data.Viewer, Metadata: domain.ActivityMetadata{"titleBefore": before, "title": item.Title}})
		}
		return nil
	})
	if err != nil || heads != 4 {
		t.Fatalf("target and modified child: heads=%d want=4 err=%v", heads, err)
	}
	_, err = repo.UpdateIssueRecord(t.Context(), workspace, child.ID, nil, IssueMutationScope{Payload: domain.IssueUpdateInput{Title: new("Must roll back")}}, func(data *domain.Bootstrap, issue *domain.Issue) error {
		before := issue.Title
		issue.Title, issue.CreatedAt = "Must roll back", time.Now().UTC().Add(-time.Hour)
		RecordIssueActivity(data, issue.ID, domain.ActivityEvent{ID: "failed-head-read", Type: "issue.updated", CreatedAt: time.Now().UTC(), Actor: data.Viewer, Metadata: domain.ActivityMetadata{"titleBefore": before, "title": issue.Title}})
		return nil
	})
	if err == nil {
		t.Fatal("a failed touched-head read committed the issue edit")
	}
	unchanged, err := repo.IssueRecord(t.Context(), workspace, child.ID)
	if err != nil || unchanged.Title != child.Title {
		t.Fatalf("failed head read did not roll back: title=%q err=%v", unchanged.Title, err)
	}
}

func TestIssueActivityRulesPersistAcrossStores(t *testing.T) {
	for _, mode := range []string{"on", "off", "verify", "records"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
			switch mode {
			case "verify":
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
				t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "1")
			case "records":
				t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
				t.Setenv("FLOW_RECORD_JOURNAL", "on")
				t.Setenv("FLOW_RECORD_HYDRATION", "on")
				t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
				t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", "on")
			}
			before, differences := NarrowMutationVerificationCounts()
			testIssueActivityRulesPersistAcrossStores(t)
			after, changed := NarrowMutationVerificationCounts()
			if changed != differences || mode != "off" && after == before {
				t.Fatalf("verification comparisons=%d mismatches=%d", after-before, changed-differences)
			}
			t.Logf("mode=%s verification comparisons=%d mismatches=%d", mode, after-before, changed-differences)
		})
	}
}

func testIssueActivityRulesPersistAcrossStores(t *testing.T) {
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
		{"cycle burst", []edit{{at: 5 * time.Minute, metadata: map[string]string{"cycleBefore": "", "cycle": "cycle-burst-a"}}, {at: 6 * time.Minute, metadata: map[string]string{"cycleBefore": "cycle-burst-a", "cycle": "cycle-burst-b"}}}, 1, "", ""},
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
						_, err := writer.UpdateIssueRecord(t.Context(), workspace, issue.ID, nil, IssueMutationScope{Payload: domain.IssueUpdateInput{Title: new("activity test")}}, func(data *domain.Bootstrap, current *domain.Issue) error {
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
					if test.name == "cycle burst" {
						kept, err := reader.CycleHistoryIssueIDs(t.Context(), workspace, "cycle-burst-b")
						if err != nil || !slices.Contains(kept, issue.ID) {
							t.Fatalf("coalesced activity lost its cycle reference: %v %v", kept, err)
						}
						dropped, err := reader.CycleHistoryIssueIDs(t.Context(), workspace, "cycle-burst-a")
						if err != nil || slices.Contains(dropped, issue.ID) {
							t.Fatalf("coalesced activity kept an intermediate cycle reference: %v %v", dropped, err)
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
