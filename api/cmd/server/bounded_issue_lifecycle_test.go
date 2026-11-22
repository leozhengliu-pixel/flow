package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	issueDeleteRoot       = "issue_delete_root"
	issueDeleteChild      = "issue_delete_child"
	issueDeleteSibling    = "issue_delete_sibling"
	issueDeleteGrandchild = "issue_delete_grandchild"
	issueDeleteRelated    = "issue_delete_related"
	issueDeleteIncoming   = "issue_delete_incoming"
	issueDeleteDocument   = "delete-custom-collaboration"
)

func seedIssueDelete(t *testing.T, x *sw530Sweep) {
	t.Helper()
	owner, member, inherited := x.principals[0].user, x.principals[2].user, x.principals[4].user
	workspaceID := x.fx.f.data.Workspace.ID
	if err := x.legacy.store.SetTeamMembership(t.Context(), workspaceID, x.ids["private-team"], inherited.ID, "owner", true); err != nil {
		t.Fatal(err)
	}
	x.membership["team_memberships"] = x.rows(t, `SELECT * FROM team_memberships WHERE workspace_id=?`, workspaceID)
	at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := x.legacy.store.MutateWorkspace(t.Context(), x.workspace, "issue-delete.fixture", "", nil, func(data *domain.Bootstrap) error {
		team := func(name string) domain.Team {
			return data.Teams[slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == x.ids[name] })]
		}
		issue := func(id, identifier, group string, n int) domain.Issue {
			return domain.Issue{ID: id, Identifier: identifier, Number: n, Title: id, Team: team(group), Creator: owner,
				State: data.States[0], CreatedAt: at, UpdatedAt: at, Version: 1}
		}
		parent := issue(issueDeleteRoot, "DEL-8101", "child-team", 8101)
		parent.PreviousIdentifiers = []string{"OLD-8101"}
		parent.SubIssueIDs = []string{issueDeleteSibling, issueDeleteChild}
		parent.DocumentContent = &domain.DocumentContent{ID: issueDeleteDocument, Version: 1, UpdatedAt: at}
		parent.Attachments = []domain.Attachment{{ID: "delete-attachment", IssueID: parent.ID, URL: "https://example.test/attachment", CreatedAt: at}}
		parent.Relations = []domain.IssueRelation{{ID: "delete-relation-out", IssueID: parent.ID, RelatedIssueID: issueDeleteRelated, Type: "related"}}
		parent.Permissions = []domain.IssuePermission{
			{ID: "delete-grant-member", IssueID: parent.ID, SubjectType: "user", SubjectID: member.ID, Role: "editor"},
			{ID: "delete-grant-owner", IssueID: parent.ID, SubjectType: "user", SubjectID: owner.ID, Role: "editor"},
		}
		project := data.Projects[0]
		parent.Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name}
		child := issue(issueDeleteChild, "DEL-8102", "private-team", 8102)
		child.ParentID, child.SubIssueIDs = &parent.ID, []string{issueDeleteGrandchild}
		sibling := issue(issueDeleteSibling, "DEL-8106", "teams", 8106)
		sibling.ParentID = &parent.ID
		grandchild := issue(issueDeleteGrandchild, "DEL-8103", "child-team", 8103)
		grandchild.ParentID = &child.ID
		related := issue(issueDeleteRelated, "DEL-8104", "teams", 8104)
		related.Relations = []domain.IssueRelation{{ID: "delete-relation-in", IssueID: related.ID, RelatedIssueID: parent.ID, Type: "related"}}
		incoming := issue(issueDeleteIncoming, "DEL-8105", "teams", 8105)
		incoming.Relations = []domain.IssueRelation{{ID: "delete-incoming-only", IssueID: incoming.ID, RelatedIssueID: parent.ID, Type: "related"}}
		data.Issues = append(data.Issues, parent, child, sibling, grandchild, related, incoming)
		data.Comments[parent.ID] = []domain.Comment{{ID: "delete-comment", User: member, Body: "A comment", CreatedAt: at}}
		data.Activities[parent.ID] = []domain.ActivityEvent{{ID: "delete-activity", Actor: owner, CreatedAt: at}}
		data.Favorites = append(data.Favorites, domain.Favorite{ID: "delete-favorite", UserID: member.ID, ResourceType: "issue", ResourceID: parent.ID, CreatedAt: at})
		data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "delete-subscription", UserID: member.ID, ResourceType: "issue", ResourceID: parent.ID, CreatedAt: at})
		data.Drafts = append(data.Drafts, domain.Draft{ID: "delete-draft", UserID: member.ID, Type: "comment", ResourceID: parent.ID, CreatedAt: at})
		data.Documents = append(data.Documents, domain.Document{ID: "delete-document", Title: "Linked doc", IssueID: parent.ID, IssueTeamID: parent.Team.ID, Creator: owner, CreatedAt: at, UpdatedAt: at})
		data.Releases = append(data.Releases, domain.Release{ID: "delete-release", Name: "Linked release", IssueIDs: []string{parent.ID, related.ID}, CreatedAt: at, UpdatedAt: at})
		data.ProjectTemplates = append(data.ProjectTemplates, domain.ProjectTemplate{ID: "delete-template", Name: "Linked template", IssueIDs: []string{parent.ID}})
		data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: "delete-request", IssueID: parent.ID, CreatedAt: at, UpdatedAt: at})
		data.Asks = append(data.Asks, domain.Ask{ID: "delete-ask", IssueID: parent.ID, CreatedAt: at, UpdatedAt: at})
		data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "delete-sla", IssueID: parent.ID})
		data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: "delete-sla-event", IssueID: parent.ID})
		data.IssueSuggestions = append(data.IssueSuggestions,
			domain.IssueSuggestion{ID: "delete-owned-suggestion", IssueID: parent.ID, Type: "relatedIssue", State: "active", SuggestedIssueID: related.ID, CreatedAt: at, UpdatedAt: at},
			domain.IssueSuggestion{ID: "delete-incoming-suggestion", IssueID: related.ID, Type: "relatedIssue", State: "active", SuggestedIssueID: parent.ID, CreatedAt: at, UpdatedAt: at},
		)
		data.Notifications = append(data.Notifications,
			domain.Notification{ID: "delete-notification", IssueID: parent.ID, RecipientID: member.ID, SourceType: "issue", SourceID: parent.ID, CreatedAt: at, UpdatedAt: at},
			domain.Notification{ID: "delete-source-notification", IssueID: related.ID, RecipientID: member.ID, SourceType: "issue", SourceID: parent.ID, CreatedAt: at, UpdatedAt: at},
		)
		data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "delete-delivery", NotificationID: "delete-notification", RecipientID: member.ID, CreatedAt: at})
		for i := range data.APIKeys {
			if data.APIKeys[i].CreatorID == member.ID {
				data.APIKeys[i].TeamRestriction, data.APIKeys[i].TeamIDs = "selected", []string{x.ids["teams"]}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scaleP21SW201ReloadSource(t, x.fx.f.reader, x.legacy.store)
	reloadIssueDeleteBaseline(t, x)
	x.reset(t)
}

func reloadIssueDeleteBaseline(t *testing.T, x *sw530Sweep) {
	t.Helper()
	if err := x.legacy.store.ReloadWorkspace(t.Context(), x.workspace); err != nil {
		t.Fatal(err)
	}
	data, found := x.legacy.store.BootstrapFor(x.workspace)
	if !found {
		t.Fatal("missing issue delete fixture")
	}
	var err error
	x.baseline, err = json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	x.numbers = x.rows(t, `SELECT last_number FROM issue_number_sequences WHERE workspace_key=?`, x.workspace)
	x.probeStale = true
}

func issueDeleteRequest(family, ref, query, body string, principal *sw530Principal, bearer bool) sw530Request {
	return sw530Request{pattern: "DELETE /api/" + family + "/{id}", method: http.MethodDelete,
		path: "/api/" + family + "/" + ref + query, body: body, principal: principal, bearer: bearer}
}

func checkIssueDelete(t *testing.T, x *sw530Sweep, request sw530Request, expected int, children ...string) {
	t.Helper()
	slots, views := x.totalSlots, x.totalViews
	got := map[string]sw530Outcome{}
	started := time.Now().UTC()
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		// Reset writes a hydrated bootstrap with derived project counts. Use
		// the record-backed SQL preimage for the comparison in every mode.
		scaleP21SW201ReloadSource(t, x.fx.f.reader, x.legacy.store)
		x.probeStale = true
		outcome, dirty := x.run(t, source, request, started)
		got[source] = outcome
		if dirty {
			x.reset(t)
		}
	}
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		outcome := got[source]
		if outcome.TimedOut || outcome.Status != expected {
			t.Errorf("%s: status %d, want %d (timeout=%t)", source, outcome.Status, expected, outcome.TimedOut)
		}
		if kind, want, actual := sw530Differences(got["legacy"], outcome); kind != "" {
			t.Errorf("%s differs in %s%s", source, kind, issueDeleteDifferenceFields(kind, want, actual))
		}
		if expected == http.StatusNoContent {
			if len(outcome.Events) < 2 || len(outcome.Webhooks) == 0 || len(outcome.Realtime) == 0 {
				t.Errorf("%s did not publish audit, domain event, webhook and realtime", source)
			}
			deleted := append(slices.Clone(children), issueDeleteRoot)
			event := strings.Join(outcome.Realtime, "\n")
			raw, err := json.Marshal(deleted)
			if err != nil || !strings.Contains(event, `"issueIds":`+string(raw)) {
				t.Errorf("%s has incorrect issue deletion order", source)
			}
			checkIssueDeleteWebhook(t, source, outcome)
		} else if len(outcome.Rows)+len(outcome.Events)+len(outcome.Webhooks)+len(outcome.Realtime) != 0 {
			t.Errorf("%s failed deletion produced side effects", source)
		}
	}
	if slots != x.totalSlots || views != x.totalViews || x.bounded.store.MetadataWorkUsedForTesting() != 0 {
		t.Errorf("issue delete used %d COLD slots, %d legacy views or retained metadata work", x.totalSlots-slots, x.totalViews-views)
	}
}

func checkIssueDeleteWebhook(t *testing.T, source string, outcome sw530Outcome) {
	t.Helper()
	var count int
	for _, raw := range outcome.Webhooks {
		var event struct {
			Type           string          `json:"type"`
			Payload        json.RawMessage `json:"payload"`
			PreviousValues json.RawMessage `json:"previousValues"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "issue.deleted" {
			count++
			if len(event.PreviousValues) != 0 && string(event.PreviousValues) != "null" ||
				string(withoutRealtimeOnlyFields(event.Type, event.Payload)) != `{"id":"`+issueDeleteRoot+`"}` {
				t.Errorf("%s changed the deletion webhook contract", source)
			}
		}
	}
	if count != 1 {
		t.Errorf("%s published %d issue deletion webhooks", source, count)
	}
}

func issueDeleteDifferenceFields(kind, a, b string) string {
	if kind != "rows" {
		return ""
	}
	var firstA, firstB string
	for a != b {
		firstA, a, _ = strings.Cut(a, "\n")
		firstB, b, _ = strings.Cut(b, "\n")
		if firstA != firstB {
			break
		}
	}
	key, rowA, ok := strings.Cut(firstA, " = ")
	keyB, rowB, match := strings.Cut(firstB, " = ")
	if !ok || !match || key != keyB || !strings.HasPrefix(key, "metadata|projects|") {
		return ""
	}
	fields := func(row string) map[string]json.RawMessage {
		var tuple []json.RawMessage
		var data string
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(row), &tuple) == nil && len(tuple) == 2 && json.Unmarshal(tuple[1], &data) == nil {
			_ = json.Unmarshal([]byte(data), &fields)
		}
		return fields
	}
	old, next := fields(rowA), fields(rowB)
	var changed []string
	for _, field := range []string{"issueCount", "progressHistory", "scopeHistory", "completedIssueCount", "updatedAt", "lead"} {
		if string(old[field]) != string(next[field]) {
			changed = append(changed, field)
		}
	}
	return fmt.Sprintf(" (project fields %v)", changed)
}

func TestBoundedIssueLifecycleDifferential(t *testing.T) {
	t.Setenv("FLOW_API_RATE_LIMIT", "off")
	x := sw530Setup(t)
	seedIssueDelete(t, x)
	owner, member, guest, inherited, suspended := x.principals[0], x.principals[2], x.principals[3], x.principals[4], x.principals[5]
	children := []string{issueDeleteSibling, issueDeleteChild}
	for _, tc := range []struct {
		name, family, ref, query, body string
		principal                      *sw530Principal
		bearer                         bool
		status                         int
		children                       []string
	}{
		{"records-cascade", "issue-records", issueDeleteRoot, "", "", owner, false, 204, children},
		{"records-keep-old-identifier", "issue-records", "OLD-8101", "?subIssues=keep", "", owner, false, 204, nil},
		{"records-body-family", "issue-records", "DEL-8101", "", `{"issueIds":["issue_delete_child"]}`, owner, false, 204, append([]string{issueDeleteGrandchild}, children...)},
		{"records-explicit-editor", "issue-records", issueDeleteRoot, "", "", member, false, 204, children},
		{"records-inherited-team", "issue-records", issueDeleteRoot, "", "", inherited, false, 204, children},
		{"records-key-restriction", "issue-records", "OLD-8101", "", "", member, true, 403, nil},
		{"records-guest", "issue-records", issueDeleteRoot, "", "", guest, false, 403, nil},
		{"records-suspended", "issue-records", issueDeleteRoot, "", "", suspended, false, 403, nil},
		{"records-missing", "issue-records", "missing", "", "", owner, false, 403, nil},
		{"legacy-alias-member", "issues", issueDeleteRoot, "", "", member, false, 204, children},
		{"legacy-alias-owner", "issues", "OLD-8101", "?subIssues=keep", "", owner, false, 204, nil},
		{"legacy-alias-key-restriction", "issues", "OLD-8101", "", "", member, true, 403, nil},
		{"legacy-alias-missing", "issues", "missing", "", "", owner, false, 403, nil},
		{"legacy-alias-batch-shape", "issues", "batch", "", `{}`, owner, false, 404, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkIssueDelete(t, x, issueDeleteRequest(tc.family, tc.ref, tc.query, tc.body, tc.principal, tc.bearer), tc.status, tc.children...)
		})
	}
	t.Run("old-document-and-collaboration", func(t *testing.T) {
		checkIssueDeleteCollaboration(t, x, owner)
	})
	t.Run("restore-on-peer", func(t *testing.T) {
		request := issueDeleteRequest("issue-records", issueDeleteRoot, "", `{"issueIds":["issue_delete_child"]}`, owner, false)
		got, _ := x.run(t, "reloaded", request, time.Now().UTC())
		if got.Status != http.StatusNoContent {
			t.Fatalf("delete status=%d", got.Status)
		}
		if err := x.legacy.store.ReloadWorkspace(t.Context(), x.workspace); err != nil {
			t.Fatal(err)
		}
		deleted, _ := x.legacy.store.BootstrapFor(x.workspace)
		index := slices.IndexFunc(deleted.Trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == issueDeleteRoot })
		if index < 0 {
			t.Fatal("missing issue trash entry on peer")
		}
		entry := deleted.Trash[index]
		var payload deletedIssuePayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil || len(payload.Comments) != 1 ||
			len(payload.Activities) != 1 || len(payload.Issue.Attachments) != 1 || entry.Links == nil || len(entry.Links.InverseRelations) != 1 {
			t.Fatal("missing typed issue preimage or captured links")
		}
		w, timeout := x.call(x.legacy.handler, http.MethodPost, "/api/trash/"+entry.ID+"/restore", "", owner, false)
		if timeout || w.Code != http.StatusOK {
			t.Fatalf("peer restoration status=%d (timeout=%t)", w.Code, timeout)
		}
		restored, _ := x.legacy.store.BootstrapFor(x.workspace)
		parent, child, grandchild := issueIndexByID(&restored, issueDeleteRoot), issueIndexByID(&restored, issueDeleteChild), issueIndexByID(&restored, issueDeleteGrandchild)
		sibling := issueIndexByID(&restored, issueDeleteSibling)
		if parent < 0 || child < 0 || grandchild < 0 || sibling < 0 ||
			restored.Issues[sibling].ParentID == nil || *restored.Issues[sibling].ParentID != issueDeleteRoot ||
			restored.Issues[child].ParentID == nil || *restored.Issues[child].ParentID != issueDeleteRoot ||
			restored.Issues[grandchild].ParentID == nil || *restored.Issues[grandchild].ParentID != issueDeleteChild ||
			len(restored.Issues[parent].Attachments) != 1 || len(restored.Comments[issueDeleteRoot]) != 1 ||
			!slices.ContainsFunc(restored.Documents, func(doc domain.Document) bool { return doc.ID == "delete-document" && doc.IssueID == issueDeleteRoot }) ||
			!slices.ContainsFunc(restored.Notifications, func(n domain.Notification) bool { return n.ID == "delete-notification" }) ||
			!slices.ContainsFunc(restored.Favorites, func(item domain.Favorite) bool { return item.ID == "delete-favorite" }) ||
			!slices.ContainsFunc(restored.Subscriptions, func(item domain.Subscription) bool { return item.ID == "delete-subscription" }) {
			t.Fatal("peer restoration lost issue records or links")
		}
		x.reset(t)
	})
	t.Run("large-family", func(t *testing.T) {
		checkIssueDeleteLargeFamily(t, x, owner, children)
	})
	t.Run("development-alias", func(t *testing.T) {
		viewer := x.legacy.store.DefaultViewer()
		if len(x.rows(t, `SELECT id FROM auth_users WHERE id=?`, viewer.ID)) == 0 {
			at := time.Now().UTC().Format(time.RFC3339Nano)
			x.exec(t, `INSERT INTO auth_users(id,name,display_name,password_hash,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
				viewer.ID, viewer.Name, viewer.DisplayName, "", 1, at, at)
		}
		for _, source := range []*sw530Source{&x.legacy, &x.bounded} {
			if err := x.legacy.store.EnsureWorkspaceMembership(t.Context(), x.fx.f.data.Workspace.ID, source.store.DefaultViewer().ID); err != nil {
				t.Fatal(err)
			}
			source.handler = issueDeleteDevHandler(t, x, source.store)
		}
		x.membership["workspace_memberships"] = x.rows(t, `SELECT * FROM workspace_memberships WHERE workspace_id=?`, x.fx.f.data.Workspace.ID)
		reloadIssueDeleteBaseline(t, x)
		for _, tc := range []struct {
			name, ref, query string
			status           int
			children         []string
		}{
			{"cascade", issueDeleteRoot, "", 204, children},
			{"keep", "OLD-8101", "?subIssues=keep", 204, nil},
			{"missing", "missing", "", 404, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				checkIssueDelete(t, x, issueDeleteRequest("issues", tc.ref, tc.query, "", owner, false), tc.status, tc.children...)
			})
		}
	})
}

func checkIssueDeleteCollaboration(t *testing.T, x *sw530Sweep, owner *sw530Principal) {
	t.Helper()
	rows := x.rows(t, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' AND record_key='delete-document'`, x.workspace)
	if len(rows) != 1 {
		t.Fatal("missing linked document fixture")
	}
	var document domain.Document
	if err := json.Unmarshal([]byte(rows[0][0].(string)), &document); err != nil {
		t.Fatal(err)
	}
	document.Permissions = nil
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	documents := []string{issueDeleteDocument, "document_content_" + issueDeleteChild, "document_content_" + issueDeleteGrandchild}
	started := time.Now().UTC()
	got := map[string]sw530Outcome{}
	slots, views := x.totalSlots, x.totalViews
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		x.exec(t, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='documents' AND record_key='delete-document'`, raw, x.workspace)
		scaleP21SW201ReloadSource(t, x.fx.f.reader, x.legacy.store)
		x.probeStale = true
		for _, id := range documents {
			update := store.DocumentCollaborationUpdate{ID: "update_" + id, DocumentID: id, Data: []byte{1, 2, 3}, CreatedAt: started}
			if _, err := x.legacy.store.AppendDocumentCollaborationUpdate(t.Context(), x.workspace, update); err != nil {
				t.Fatal(err)
			}
		}
		got[source], _ = x.run(t, source, issueDeleteRequest("issue-records", issueDeleteRoot, "", "", owner, false), started)
		if got[source].Status != http.StatusNoContent {
			t.Errorf("%s collaboration fixture status %d", source, got[source].Status)
		}
		for i, id := range documents {
			updates, err := x.legacy.store.DocumentCollaborationUpdates(t.Context(), x.workspace, id)
			if err != nil || len(updates) != i/2 {
				t.Errorf("%s collaboration cleanup changed the wrong rows", source)
			}
		}
		if kind, _, _ := sw530Differences(got["legacy"], got[source]); kind != "" {
			t.Errorf("%s old-document deletion differs in %s", source, kind)
		}
		x.reset(t)
	}
	if x.totalSlots != slots || x.totalViews != views {
		t.Error("collaboration cleanup reached the COLD path")
	}
}

func checkIssueDeleteLargeFamily(t *testing.T, x *sw530Sweep, owner *sw530Principal, children []string) {
	t.Helper()
	const largeRoot = "issue_delete_large_root"
	err := x.legacy.store.MutateWorkspace(t.Context(), x.workspace, "issue-delete.large-fixture", "", nil, func(data *domain.Bootstrap) error {
		parent := data.Issues[issueIndexByID(data, issueDeleteRoot)]
		parent.ID, parent.Identifier, parent.Number = largeRoot, "DEL-8200", 8200
		parent.SubIssueIDs, parent.Relations, parent.PreviousIdentifiers = nil, nil, nil
		data.Issues = append(data.Issues, parent)
		for i := range 1000 {
			child := domain.Issue{ID: fmt.Sprintf("issue_delete_large_%04d", i), Identifier: fmt.Sprintf("DEL-%d", 8201+i),
				Number: 8201 + i, ParentID: &parent.ID, Team: parent.Team, State: parent.State, Creator: parent.Creator,
				Title: "Large issue family", CreatedAt: parent.CreatedAt, UpdatedAt: parent.UpdatedAt, Version: 1}
			data.Issues = append(data.Issues, child)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reloadIssueDeleteBaseline(t, x)
	x.reset(t)
	checkIssueDelete(t, x, issueDeleteRequest("issue-records", largeRoot, "", "", owner, false), 500)
	checkIssueDelete(t, x, issueDeleteRequest("issues", issueDeleteRoot, "", "", owner, false), 204, children...)
}

func issueDeleteDevHandler(t *testing.T, x *sw530Sweep, repo *store.SQLiteStore) http.Handler {
	t.Helper()
	s := &server{store: repo, uploadPath: t.TempDir(), authDisabled: true}
	for _, started := range []interface{ Store(bool) }{&s.socketAccessSweepStarted, &s.workflowSchedulerStarted, &s.deliverySchedulerStarted, &s.loopReconcilerStarted, &s.agentRunReconcilerStarted, &s.agentLiveSweepStarted, &s.coordinationStarted} {
		started.Store(true)
	}
	handler := newHandler(s)
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.webhooks = append(x.webhooks, string(scaleP21SW106Envelope(t, event)))
	})
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.realtime = append(x.realtime, string(scaleP21SW106Envelope(t, event)))
	})
	return handler
}
