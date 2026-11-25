package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP04MutationSideEffectsDifferential(t *testing.T) {
	for _, script := range []struct {
		name string
		run  func(*testing.T) mutationScriptResult
	}{
		{"hotWrites", scaleP04HotWriteScript},
		{"scopedWrites", func(t *testing.T) mutationScriptResult { return runMutationScript(t, false) }},
		{"teamAdministration", func(t *testing.T) mutationScriptResult { return runTeamAdministrationScript(t, true, false) }},
	} {
		t.Run(script.name, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
			legacy := script.run(t)
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "on")
			narrow := script.run(t)
			compareMutationLists(t, "realtime", narrow.events, legacy.events)
			compareMutationLists(t, "webhook", narrow.webhooks, legacy.webhooks)
			if !reflect.DeepEqual(narrow.state, legacy.state) {
				for key, value := range legacy.state {
					if narrow.state[key] != value {
						other := narrow.state[key]
						i := 0
						for i < len(value) && i < len(other) && value[i] == other[i] {
							i++
						}
						t.Errorf("state %s differs at byte %d:\nlegacy %.1000s\nnarrow %.1000s", key, i, value[i:], other[i:])
					}
				}
			}
		})
	}
}

func scaleP04HotWriteScript(t *testing.T) mutationScriptResult {
	t.Helper()
	database := newScaleP04Database(t)
	repo := database.repo
	handler := database.handler(t, true)
	result := mutationScriptResult{state: map[string]string{}}
	data, _ := repo.BootstrapFor(database.key)
	// The fixture replaces its initial generated team with team_test but
	// retains that team's settings. Keep its ID distinct from teams created
	// by this script; collapsing both map keys would discard one at random.
	var fixtureTeamIDs, substitutions []string
	for id := range data.TeamSettings {
		if mutationNanoID.MatchString(id) {
			fixtureTeamIDs = append(fixtureTeamIDs, id)
		}
	}
	slices.Sort(fixtureTeamIDs)
	for i, id := range fixtureTeamIDs {
		substitutions = append(substitutions, id, fmt.Sprintf("fixture_team_%d", i))
	}
	for i, user := range database.users {
		substitutions = append(substitutions, user.ID, fmt.Sprintf("fixture_user_%d", i), user.Email, fmt.Sprintf("fixture-user-%d@example.test", i))
	}
	fixtureIDs := strings.NewReplacer(substitutions...)
	normalizeIdentity := func(raw []byte) string { return scaleP04Normalize([]byte(fixtureIDs.Replace(string(raw)))) }
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		result.events = append(result.events, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeIdentity(event.Payload))
	})
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		result.webhooks = append(result.webhooks, event.Type+" "+mutationNanoID.ReplaceAllString(event.AggregateID, "<n>")+" "+normalizeIdentity(event.Payload)+" "+normalizeIdentity(event.PreviousValues))
	})
	team, recipient := data.Teams[0], data.Users[1]
	requestJSON[any](t, handler, http.MethodPost, "/api/loops", map[string]any{"name": "P04 watcher", "status": "published", "triggerType": "issue", "triggerConfig": map[string]any{"event": "updated"}, "instructions": "Watch"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPatch, "/api/teams/"+team.ID+"/settings", map[string]any{"autoCloseSubIssues": true, "autoCloseParents": true, "progressOrder": "last"}, http.StatusOK)
	parent := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "P04 parent", "teamId": team.ID, "projectId": data.Projects[0].ID, "subscriberIds": []string{recipient.ID}}, http.StatusCreated)
	child := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": "P04 child", "teamId": team.ID, "parentId": parent.ID}, http.StatusCreated)
	issuePath := "/api/issue-records/" + parent.ID
	requestJSON[domain.Issue](t, handler, http.MethodPatch, issuePath, map[string]any{"title": "P04 renamed", "assigneeId": recipient.ID, "priority": 1}, http.StatusOK)
	mention := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "mention", "userId": recipient.ID}}}
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, issuePath+"/comments", map[string]any{"body": "P04 mention", "bodyData": mention}, http.StatusCreated)
	requestJSON[domain.Comment](t, handler, http.MethodPost, issuePath+"/comments/"+comment.ID+"/reactions", map[string]any{"emoji": "+1"}, http.StatusOK)
	requestJSON[domain.Comment](t, handler, http.MethodPatch, issuePath+"/comments/"+comment.ID, map[string]any{"body": "P04 edited", "bodyData": mention}, http.StatusOK)
	requestJSON[domain.Comment](t, handler, http.MethodPost, issuePath+"/comments", map[string]any{"body": "P04 reply", "parentId": comment.ID, "bodyData": mention}, http.StatusCreated)
	states := requestJSON[[]domain.WorkflowState](t, handler, http.MethodGet, "/api/teams/"+team.ID+"/states", nil, http.StatusOK)
	completed := slices.IndexFunc(states, func(state domain.WorkflowState) bool { return state.Type == "completed" })
	if completed < 0 {
		t.Fatal("no completed state")
	}
	requestJSON[domain.Issue](t, handler, http.MethodPatch, issuePath, map[string]any{"stateId": states[completed].ID}, http.StatusOK)
	closed := requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+child.ID, nil, http.StatusOK)
	if closed.State.Type != "completed" {
		t.Fatal("status automation did not close the child")
	}
	linked := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "P04 linked document", "issueId": parent.ID, "projectIds": []string{data.Projects[0].ID}}, http.StatusCreated)
	target := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "P04 move target", "key": "PMV"}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, issuePath, map[string]any{"teamId": target.ID}, http.StatusOK)
	linked = requestJSON[domain.Document](t, handler, http.MethodGet, "/api/documents/"+linked.ID, nil, http.StatusOK)
	if !slices.Contains(linked.InheritedTeamIDs, target.ID) || !slices.Contains(linked.InheritedTeamIDs, team.ID) {
		t.Fatalf("document lost an inherited issue or project team: %v", linked.InheritedTeamIDs)
	}
	requestJSON[domain.Team](t, handler, http.MethodPatch, "/api/workspaces/test-workspace/teams/"+team.ID, map[string]any{"name": "P04 renamed team"}, http.StatusOK)
	database.reopen(t)
	repo = database.repo
	data, _ = repo.BootstrapFor(database.key)
	normalize := func(raw []byte) string {
		for _, document := range data.Documents {
			if document.SlugID != "" {
				raw = bytes.ReplaceAll(raw, []byte(document.SlugID), []byte("<slug:"+document.ID+">"))
			}
		}
		return scaleP04Normalize([]byte(fixtureIDs.Replace(string(raw))))
	}
	if !slices.ContainsFunc(data.Notifications, func(n domain.Notification) bool {
		return n.IssueID == parent.ID && n.Category == "mentions" && n.RecipientID == recipient.ID
	}) {
		t.Fatal("mention notification did not persist")
	}
	if len(data.NotificationDeliveries) == 0 || len(result.webhooks) == 0 {
		t.Fatal("missing deliveries or webhooks")
	}
	for key, value := range map[string]any{
		"issues": data.Issues, "comments": data.Comments, "activities": data.Activities,
		"notifications": data.Notifications, "notificationDeliveries": data.NotificationDeliveries,
		"teams": data.Teams, "teamSettings": data.TeamSettings, "auditLog": data.AuditLog,
		"documents": data.Documents,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result.state[key] = normalize(raw)
	}
	for name, query := range map[string]string{
		"rootSQL":     "SELECT data FROM workspace_states WHERE workspace_key=?",
		"metadataSQL": "SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key",
		"issueSQL":    "SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY id",
		"contentSQL":  "SELECT kind,resource_id,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id",
		"searchSQL":   "SELECT issue_id,content FROM issue_search_documents WHERE workspace_key=? ORDER BY issue_id",
		"eventsSQL":   "SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY seq,id",
	} {
		var arg any = database.key
		if name == "eventsSQL" {
			arg = database.workspaceID
		}
		rows := database.query(t, query, arg)
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var records [][]string
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			args := make([]any, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			record := make([]string, len(columns))
			for i, value := range values {
				record[i] = normalize([]byte(value.String))
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		raw, _ := json.Marshal(records)
		result.state[name] = string(raw)
	}
	return result
}

// Preserve counts, histories, nil/empty shapes and previous values. Only
// clocks and generated identifiers may differ between independent fixtures.
func scaleP04Normalize(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return mutationNanoID.ReplaceAllString(string(raw), "<n>")
	}
	var walk func(any, string) any
	walk = func(value any, key string) any {
		switch v := value.(type) {
		case map[string]any:
			result := map[string]any{}
			for field, child := range v {
				if field != "slugId" {
					result[mutationNanoID.ReplaceAllString(field, "<n>")] = walk(child, field)
				}
			}
			return result
		case []any:
			for i := range v {
				v[i] = walk(v[i], key)
			}
			if key == "notifications" || key == "notificationDeliveries" || key == "auditLog" {
				sort.Slice(v, func(i, j int) bool {
					a, _ := json.Marshal(v[i])
					b, _ := json.Marshal(v[j])
					return string(a) < string(b)
				})
			}
			return v
		case string:
			if mutationTimestamp.MatchString(v) {
				return "<time>"
			}
			return mutationNanoID.ReplaceAllString(v, "<n>")
		}
		return value
	}
	encoded, _ := json.Marshal(walk(value, ""))
	return string(encoded)
}

func TestScaleP04PermissionMatrix(t *testing.T) {
	for _, mode := range []string{"off", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
			for _, item := range []struct {
				name string
				run  func(*testing.T)
			}{
				{"adminPrivateOutsiderAndMember", TestPrivateTeamAccessSplitsAdminSettingsFromContent},
				{"explicitGrant", TestPrivateIssueExplicitShareAndTeamOwnerInheritance},
				{"guestWriteAndRevoke", scaleP04GuestWriteAndRevoke},
				{"apiKeysAndHiddenReferences", TestIssueDetailHTTPHistoryAndVisibilityHonorSharesAndAPIKeyScopes},
				{"apiKeyEmptyAndSelected", TestAPIKeyScopesAndTeamProjection},
				{"inheritance", TestSubTeamInheritsSettingsLabelsAndCycles},
				{"oldIdentifierAndTeamMove", TestIssuePatchMovesIssueToAnotherTeam},
				{"notificationAudience", TestIssueMoveNotifiesInvitedSubscribersWhoCanOpenIt},
				{"queuedRevocation", TestCollaborationIssueAccessRevocationReachesOpenSockets},
				{"replay", TestRealtimeReplayAndPeerEventsFollowReadAccess},
				{"workspaceIsolation", TestWorkspaceAccessStatusBranches},
			} {
				t.Run(item.name, item.run)
			}
		})
	}
}

func TestScaleP04RequestAttribution(t *testing.T) {
	for _, mode := range []string{"off", "on"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FLOW_NARROW_MUTATION_INPUTS", mode)
			t.Run("chat", TestAgentChatToolCallsAttributeHistoryToFlowOnBehalfOfTheUser)
			t.Run("thread", TestAgentThreadToolCallsAttributeHistoryToFlowOnBehalfOfTheInvoker)
			t.Run("worker", TestAgentRunTokenActsLikeTheInProcessChatAgent)
			t.Run("oauth", TestOAuthViaApplicationAttribution)
			t.Run("appMentions", TestMCPSaveCommentAppMentions)
		})
	}
}

func scaleP04GuestWriteAndRevoke(t *testing.T) {
	t.Helper()
	t.Setenv("FLOW_DEV_AUTH_TOKENS", "true")
	database := newScaleP04Database(t)
	repo := database.repo
	host := httptest.NewServer(database.handler(t, false))
	defer host.Close()
	admin := database.login(t, host.URL, database.users[0])
	guest, user := database.join(t, host.URL, admin, "P04 Guest", "guest", "team_test")
	team := authRequest[domain.Team](t, admin, http.MethodPost, host.URL+"/api/workspaces/test-workspace/teams", map[string]any{"name": "P04 private", "key": "PVT", "private": true}, "", http.StatusCreated)
	issue := authRequest[domain.Issue](t, admin, http.MethodPost, host.URL+"/api/issues", map[string]any{"title": "Private P04 issue", "teamId": team.ID}, "test-workspace", http.StatusCreated)
	path := host.URL + "/api/issues/" + issue.ID
	authRequest[any](t, guest, http.MethodPatch, path, map[string]string{"title": "Blocked"}, "test-workspace", http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPut, path+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": user.ID, "role": "editor"}}}, "test-workspace", http.StatusOK)
	authRequest[domain.Issue](t, guest, http.MethodPatch, path, map[string]string{"title": "Granted guest edit"}, "test-workspace", http.StatusOK)
	authRequest[domain.Comment](t, guest, http.MethodPost, path+"/comments", map[string]string{"body": "Granted guest comment"}, "test-workspace", http.StatusCreated)
	foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), database.users[0]), "P04 other workspace", idgen.New("p04-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.DeleteWorkspace(context.Background(), foreign.Workspace.URLKey) })
	authRequest[any](t, guest, http.MethodPatch, path, map[string]string{"title": "Cross workspace edit"}, foreign.Workspace.URLKey, http.StatusForbidden)
	authRequest[any](t, admin, http.MethodPut, path+"/permissions", map[string]any{"permissions": []any{}}, "test-workspace", http.StatusOK)
	authRequest[any](t, guest, http.MethodPatch, path, map[string]string{"title": "Revoked edit"}, "test-workspace", http.StatusForbidden)
}
