package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestWorkspaceSettingsPersistence(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	want := map[string]any{
		"values": map[string]any{"firstDay": "Sunday", "emoticons": false},
		"lists":  map[string]any{"apiKeys": []any{map[string]any{"id": "key_1", "name": "Test"}}},
	}
	requestJSON[map[string]any](t, handler, http.MethodPut, "/api/workspace/settings", want, http.StatusOK)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	values, ok := bootstrap.Settings["values"].(map[string]any)
	if !ok || values["firstDay"] != "Sunday" || values["emoticons"] != false {
		t.Fatalf("settings did not survive bootstrap round trip: %#v", bootstrap.Settings)
	}
}

func TestDevelopmentMemberLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Members) != len(bootstrap.Users) || len(bootstrap.TeamMembers) == 0 {
		t.Fatalf("development member projection missing: members=%d users=%d teamMembers=%d", len(bootstrap.Members), len(bootstrap.Users), len(bootstrap.TeamMembers))
	}
	target := bootstrap.Users[1]
	updated := requestJSON[domain.WorkspaceMember](t, handler, http.MethodPatch, "/api/workspaces/test-workspace/members/"+target.ID, map[string]any{"role": "admin", "displayName": "Updated member", "username": "updated.member", "email": "updated.member@example.com"}, http.StatusOK)
	if updated.Role != "admin" || updated.User.DisplayName != "Updated member" || updated.User.Name != "updated.member" || updated.User.Email != "updated.member@example.com" {
		t.Fatalf("member update failed: %#v", updated)
	}
	requestJSON[any](t, handler, http.MethodPut, "/api/workspaces/test-workspace/teams/"+bootstrap.Teams[0].ID+"/members/"+target.ID, map[string]any{"member": false, "role": "member"}, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodPost, "/api/workspaces/test-workspace/members/"+target.ID+"/suspend", nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodPost, "/api/workspaces/test-workspace/members/"+target.ID+"/resume", nil, http.StatusNoContent)
	invitations := requestJSON[[]domain.Invitation](t, handler, http.MethodPost, "/api/workspaces/test-workspace/invitations", map[string]any{"emails": []string{"new.member@example.com"}, "role": "member", "teamIds": []string{bootstrap.Teams[0].ID}}, http.StatusCreated)
	if len(invitations) != 1 || invitations[0].Token == "" {
		t.Fatalf("development invitation failed: %#v", invitations)
	}
	preview := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/invitations/preview/"+invitations[0].Token, nil, http.StatusOK)
	if preview["email"] != "new.member@example.com" {
		t.Fatalf("invitation preview = %#v", preview)
	}
	resent := requestJSON[domain.Invitation](t, handler, http.MethodPost, "/api/workspaces/test-workspace/invitations/"+invitations[0].ID+"/resend", nil, http.StatusOK)
	if resent.Token == "" || resent.Token == invitations[0].Token {
		t.Fatalf("invitation token was not rotated: %#v", resent)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/invitations/"+invitations[0].ID, nil, http.StatusNoContent)
}

func TestTeamCreationHierarchyCopyAndDelete(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	source := bootstrap.Teams[0]
	requestJSON[domain.TeamSettings](t, handler, http.MethodPatch, "/api/teams/"+source.ID+"/settings", map[string]any{"timezone": "Asia/Shanghai", "progressOrder": "last"}, http.StatusOK)
	parent := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Platform", "key": "PLT", "private": true, "copyFromTeamId": source.ID, "timezone": "Europe/London"}, http.StatusCreated)
	parent = requestJSON[domain.Team](t, handler, http.MethodPatch, "/api/workspaces/test-workspace/teams/"+parent.ID, map[string]any{"icon": "🚀", "color": "#d758fc"}, http.StatusOK)
	if parent.Icon != "🚀" || parent.Color != "#d758fc" {
		t.Fatalf("team visual settings were not persisted: %#v", parent)
	}
	child := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Runtime", "key": "RUN", "parentTeamId": parent.ID}, http.StatusCreated)
	afterCreate := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if afterCreate.TeamSettings[parent.ID].Timezone != "Europe/London" || afterCreate.TeamSettings[parent.ID].ProgressOrder != "last" || afterCreate.TeamSettings[child.ID].ParentTeamID != parent.ID {
		t.Fatalf("team settings were not copied: parent=%#v child=%#v", afterCreate.TeamSettings[parent.ID], afterCreate.TeamSettings[child.ID])
	}
	if !slices.ContainsFunc(afterCreate.States, func(state domain.WorkflowState) bool { return state.TeamID == child.ID }) || !slices.ContainsFunc(afterCreate.TeamMembers, func(member domain.TeamMember) bool {
		return member.TeamID == child.ID && member.UserID == afterCreate.Viewer.ID && member.Role == "owner"
	}) {
		t.Fatal("team workflow or owner membership was not copied")
	}
	parentIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Retirement coverage", "teamId": parent.ID}, http.StatusCreated)
	requestJSON[domain.Team](t, handler, http.MethodPatch, "/api/workspaces/test-workspace/teams/"+parent.ID, map[string]any{"retired": true}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Blocked on retired team", "teamId": parent.ID}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/"+parentIssue.ID, map[string]any{"priority": 2}, http.StatusBadRequest)
	requestJSON[domain.Team](t, handler, http.MethodPatch, "/api/workspaces/test-workspace/teams/"+parent.ID, map[string]any{"retired": false}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodPatch, "/api/teams/"+parent.ID+"/settings", map[string]any{"parentTeamId": child.ID}, http.StatusBadRequest)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Deleted with team", "teamId": child.ID}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/teams/"+child.ID, nil, http.StatusNoContent)
	afterDelete := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(afterDelete.Teams, func(team domain.Team) bool { return team.ID == child.ID }) || slices.ContainsFunc(afterDelete.Issues, func(item domain.Issue) bool { return item.ID == issue.ID }) || slices.ContainsFunc(afterDelete.TeamMembers, func(member domain.TeamMember) bool { return member.TeamID == child.ID }) {
		t.Fatal("team deletion left owned resources behind")
	}
}

func TestWorkspaceRegionSelectorCanBeDisabled(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, workspaceRegionSelectorEnabled: false, workspaceDefaultRegion: "eu"})
	account := requestJSON[domain.AccountBootstrap](t, handler, http.MethodGet, "/api/account/bootstrap", nil, http.StatusOK)
	if account.WorkspaceRegionSelectorEnabled || account.WorkspaceDefaultRegion != "eu" {
		t.Fatalf("account region config = %#v", account)
	}
	created := requestJSON[domain.Bootstrap](t, handler, http.MethodPost, "/api/workspaces", map[string]string{"name": "Configured region", "urlKey": "configured-region", "region": "us"}, http.StatusCreated)
	if created.Workspace.Region != "eu" {
		t.Fatalf("client bypassed configured region: %#v", created.Workspace)
	}
}

func TestWorkspaceCreationSupportsUnicodeNamesAndKeys(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	created := requestJSON[domain.Bootstrap](t, handler, http.MethodPost, "/api/workspaces", map[string]string{"name": "研发平台", "urlKey": "研发 平台", "region": "asia"}, http.StatusCreated)
	if created.Workspace.Name != "研发平台" || created.Workspace.URLKey != "研发-平台" {
		t.Fatalf("unicode workspace was normalized incorrectly: %#v", created.Workspace)
	}
	if len(created.Teams) != 1 || len(created.Teams[0].Key) != 3 || created.Teams[0].Key[0] != 'W' {
		t.Fatalf("unicode workspace generated an invalid default team key: %#v", created.Teams)
	}
	if got := normalizeWorkspaceKey("研发 Platform_二期"); got != "研发-platform-二期" {
		t.Fatalf("normalizeWorkspaceKey() = %q", got)
	}
}

func TestWorkspaceLogoUploadAndRemoval(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="logo.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nflow"))
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-workspace/logo", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("logo upload status=%d body=%s", response.Code, response.Body.String())
	}
	var updated domain.Bootstrap
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(updated.Workspace.LogoURL, "/uploads/workspace_logo_") {
		t.Fatalf("logo URL = %q", updated.Workspace.LogoURL)
	}
	removed := requestJSON[domain.Bootstrap](t, handler, http.MethodDelete, "/api/workspaces/test-workspace/logo", nil, http.StatusOK)
	if removed.Workspace.LogoURL != "" {
		t.Fatalf("logo was not removed: %q", removed.Workspace.LogoURL)
	}
	upload := func(filename, contentType, content string, wantStatus int) *httptest.ResponseRecorder {
		t.Helper()
		var uploadBody bytes.Buffer
		uploadWriter := multipart.NewWriter(&uploadBody)
		uploadHeader := textproto.MIMEHeader{}
		uploadHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
		uploadHeader.Set("Content-Type", contentType)
		uploadPart, createErr := uploadWriter.CreatePart(uploadHeader)
		if createErr != nil {
			t.Fatal(createErr)
		}
		_, _ = uploadPart.Write([]byte(content))
		_ = uploadWriter.Close()
		uploadRequest := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-workspace/logo", &uploadBody)
		uploadRequest.Header.Set("Content-Type", uploadWriter.FormDataContentType())
		uploadResponse := httptest.NewRecorder()
		handler.ServeHTTP(uploadResponse, uploadRequest)
		if uploadResponse.Code != wantStatus {
			t.Fatalf("%s upload status=%d body=%s", filename, uploadResponse.Code, uploadResponse.Body.String())
		}
		return uploadResponse
	}
	safeSVGResponse := upload("logo.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><path d="M0 0h16v16H0z"/></svg>`, http.StatusOK)
	var safeSVGWorkspace domain.Bootstrap
	if err := json.Unmarshal(safeSVGResponse.Body.Bytes(), &safeSVGWorkspace); err != nil {
		t.Fatal(err)
	}
	inlineSVG := httptest.NewRecorder()
	handler.ServeHTTP(inlineSVG, httptest.NewRequest(http.MethodGet, safeSVGWorkspace.Workspace.LogoURL, nil))
	if inlineSVG.Code != http.StatusOK || inlineSVG.Header().Get("Content-Type") != "image/svg+xml" || inlineSVG.Header().Get("Content-Disposition") != "" {
		t.Fatalf("safe SVG response headers = %#v", inlineSVG.Header())
	}
	upload("unsafe.svg", "image/svg+xml", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, http.StatusBadRequest)
}

func TestIssueStateTransitionsPersistInsightTimestamps(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Issues) == 0 {
		t.Fatal("seed must include an issue")
	}
	findState := func(kind string) string {
		for _, state := range bootstrap.States {
			if state.Type == kind {
				return state.ID
			}
		}
		t.Fatalf("seed must include a %s state", kind)
		return ""
	}
	issueID := bootstrap.Issues[0].ID
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issueID, map[string]any{"stateId": findState("unstarted")}, http.StatusOK)
	started := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issueID, map[string]any{"stateId": findState("started")}, http.StatusOK)
	if started.StartedAt == nil || started.StatusChangedAt == nil || started.CompletedAt != nil || started.CanceledAt != nil {
		t.Fatalf("started timestamps are inconsistent: %#v", started)
	}
	completed := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issueID, map[string]any{"stateId": findState("completed")}, http.StatusOK)
	if completed.CompletedAt == nil || completed.StatusChangedAt == nil || completed.CanceledAt != nil {
		t.Fatalf("completed timestamps are inconsistent: %#v", completed)
	}
	reopened := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issueID, map[string]any{"stateId": findState("started")}, http.StatusOK)
	if reopened.CompletedAt != nil || reopened.CanceledAt != nil || reopened.StartedAt == nil {
		t.Fatalf("reopened timestamps are inconsistent: %#v", reopened)
	}
}

func TestDeleteAllDraftsOnlyRemovesViewerDrafts(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	requestJSON[domain.Draft](t, handler, http.MethodPost, "/api/drafts", map[string]any{"type": "issue", "title": "First"}, http.StatusCreated)
	requestJSON[domain.Draft](t, handler, http.MethodPost, "/api/drafts", map[string]any{"type": "document", "title": "Second"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/drafts", nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Drafts, func(item domain.Draft) bool { return item.UserID == bootstrap.Viewer.ID }) {
		t.Fatalf("viewer drafts survived discard all: %#v", bootstrap.Drafts)
	}
}

func TestLabelGroupArchiveCascadesToChildLabels(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{
		"name": "Delivery stage", "resourceType": "issue",
	}, http.StatusCreated)
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Ready for QA", "resourceType": "issue", "groupId": group.ID,
	}, http.StatusCreated)
	archivedAt := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	group = requestJSON[domain.LabelGroup](t, handler, http.MethodPatch, "/api/label-groups/"+group.ID, map[string]any{
		"archivedAt": archivedAt,
	}, http.StatusOK)
	if group.ArchivedAt == nil {
		t.Fatal("group was not archived")
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	labelIndex := slices.IndexFunc(bootstrap.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID })
	if labelIndex < 0 || bootstrap.Labels[labelIndex].ArchivedAt == nil {
		t.Fatalf("child label was not archived with its group: %#v", bootstrap.Labels)
	}

	group = requestJSON[domain.LabelGroup](t, handler, http.MethodPatch, "/api/label-groups/"+group.ID, map[string]any{
		"archivedAt": "",
	}, http.StatusOK)
	if group.ArchivedAt != nil {
		t.Fatal("group was not restored")
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	labelIndex = slices.IndexFunc(bootstrap.Labels, func(item domain.IssueLabel) bool { return item.ID == label.ID })
	if labelIndex < 0 || bootstrap.Labels[labelIndex].ArchivedAt != nil {
		t.Fatalf("child label was not restored with its group: %#v", bootstrap.Labels)
	}
}

func TestWorkspaceLabelMovesBetweenGroups(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	first := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{
		"name": "First group", "resourceType": "issue",
	}, http.StatusCreated)
	second := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{
		"name": "Second group", "resourceType": "issue",
	}, http.StatusCreated)
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Movable label", "resourceType": "issue", "groupId": first.ID,
	}, http.StatusCreated)

	label = requestJSON[domain.IssueLabel](t, handler, http.MethodPatch, "/api/labels/"+label.ID, map[string]any{
		"groupId": second.ID,
	}, http.StatusOK)
	if label.GroupID != second.ID {
		t.Fatalf("label was not moved to the target group: %#v", label)
	}

	label = requestJSON[domain.IssueLabel](t, handler, http.MethodPatch, "/api/labels/"+label.ID, map[string]any{
		"groupId": "",
	}, http.StatusOK)
	if label.GroupID != "" {
		t.Fatalf("label was not removed from its group: %#v", label)
	}
}

func TestDeletingLabelGroupDeletesChildrenAndReferences(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	group := requestJSON[domain.LabelGroup](t, handler, http.MethodPost, "/api/label-groups", map[string]any{
		"name": "Disposable group", "resourceType": "issue",
	}, http.StatusCreated)
	first := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "First child", "resourceType": "issue", "groupId": group.ID,
	}, http.StatusCreated)
	second := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Second child", "resourceType": "issue", "groupId": group.ID,
	}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPost, "/api/issue-templates", map[string]any{
		"name": "Invalid grouped defaults", "labelIds": []string{first.ID, second.ID},
	}, http.StatusBadRequest)
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Label group deletion coverage", "labelIds": []string{first.ID},
	}, http.StatusCreated)
	view := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{
		"name": "Disposable label view", "resource": "issues", "scope": "workspace",
		"filters": []map[string]any{{"id": "label-filter", "field": "labels", "operator": "is", "values": []map[string]any{{"id": first.ID, "label": first.Name}}}},
	}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/label-groups/"+group.ID, nil, http.StatusNoContent)

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.LabelGroups, func(item domain.LabelGroup) bool { return item.ID == group.ID }) {
		t.Fatal("deleted label group is still present")
	}
	if slices.ContainsFunc(bootstrap.Labels, func(item domain.IssueLabel) bool { return item.ID == first.ID || item.ID == second.ID }) {
		t.Fatalf("group children survived deletion: %#v", bootstrap.Labels)
	}
	updatedIssue := findIssue(t, bootstrap.Issues, issue.ID)
	if slices.ContainsFunc(updatedIssue.Labels, func(item domain.IssueLabel) bool { return item.ID == first.ID }) {
		t.Fatalf("deleted group label survived on issue: %#v", updatedIssue.Labels)
	}
	updatedViewIndex := slices.IndexFunc(bootstrap.SavedViews, func(item domain.SavedView) bool { return item.ID == view.ID })
	if updatedViewIndex < 0 {
		t.Fatal("saved view disappeared after label deletion")
	}
	if strings.Contains(string(bootstrap.SavedViews[updatedViewIndex].Filters), first.ID) {
		t.Fatalf("deleted group label survived in saved view filters: %s", bootstrap.SavedViews[updatedViewIndex].Filters)
	}
}

func TestMoveWorkspaceLabelToTeamsPreservesIssueAssignments(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	before := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(before.Issues) < 1 {
		t.Fatal("seed must include issues")
	}
	first := before.Issues[0]
	secondTeam := requestJSON[domain.Team](t, handler, http.MethodPost, "/api/workspaces/test-workspace/teams", map[string]any{"name": "Second team", "key": "SEC"}, http.StatusCreated)
	second := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Second team issue", "description": "Move label coverage", "teamId": secondTeam.ID}, http.StatusCreated)
	label := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Shared test label", "resourceType": "issue",
	}, http.StatusCreated)
	for _, issue := range []domain.Issue{first, second} {
		labelIDs := []string{label.ID}
		for _, existing := range issue.Labels {
			labelIDs = append(labelIDs, existing.ID)
		}
		requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"labelIds": labelIDs}, http.StatusOK)
	}
	sourceID := label.ID
	before = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	usedByTeam := map[string]bool{}
	affectedIssues := map[string]string{}
	for _, issue := range before.Issues {
		if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return label.ID == sourceID }) {
			usedByTeam[issue.Team.ID] = true
			affectedIssues[issue.ID] = issue.Team.ID
		}
	}
	if len(usedByTeam) != 2 {
		t.Fatalf("seed label must cover multiple teams, got %#v", usedByTeam)
	}

	moved := requestJSON[[]domain.IssueLabel](t, handler, http.MethodPost, "/api/labels/"+sourceID+"/move-to-teams", map[string]any{}, http.StatusOK)
	if len(moved) != len(usedByTeam) {
		t.Fatalf("moved labels = %d, want %d: %#v", len(moved), len(usedByTeam), moved)
	}
	for _, label := range moved {
		if !usedByTeam[label.Scope] || label.ID == sourceID || label.GroupID != "" {
			t.Fatalf("invalid team label: %#v", label)
		}
	}

	after := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(after.Labels, func(label domain.IssueLabel) bool { return label.ID == sourceID }) {
		t.Fatal("workspace label still exists after moving to teams")
	}
	for issueID, teamID := range affectedIssues {
		issue := findIssue(t, after.Issues, issueID)
		if !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool {
			return label.Name == "Shared test label" && label.Scope == teamID && label.ID != sourceID
		}) {
			t.Fatalf("issue %s did not receive its team label: %#v", issueID, issue.Labels)
		}
	}
}

func TestIssueLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	s := &server{store: repository, uploadPath: t.TempDir(), authDisabled: true}
	handler := newHandler(s)

	created := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Issue engine test", "description": "Initial", "stateId": "state_todo", "priority": 2, "estimate": 5, "recurrence": "weekly", "nextOccurrenceAt": "2026-09-08T00:00:00Z",
		"assigneeId": "usr_admin", "projectId": "project_cruise", "dueDate": "2026-09-01", "labelIds": []string{"label_type_defect"},
		"descriptionState": `{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Initial"}]}]}`,
		"descriptionData":  map[string]any{"type": "doc", "content": []any{map[string]any{"type": "heading", "attrs": map[string]any{"level": 2}}}}, "contentState": "CREATE_STATE",
	}, http.StatusCreated)
	if created.ID == "" || created.State.ID != "state_todo" || created.Priority != 2 || created.Estimate == nil || *created.Estimate != 5 || created.Recurrence != "weekly" || created.NextOccurrenceAt == nil || created.Project == nil || len(created.Labels) != 1 || created.DescriptionState == "" || created.DocumentContent == nil || created.DocumentContent.ContentState != "CREATE_STATE" || created.DocumentContent.ContentData["type"] != "doc" {
		t.Fatalf("create did not persist properties: %#v", created)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"labelIds": []string{"label_type_requirement", "label_type_defect"}}, http.StatusBadRequest)
	for _, stateID := range []string{"state_canceled", "state_duplicate"} {
		updatedState := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"stateId": stateID}, http.StatusOK)
		if updatedState.State.ID != stateID {
			t.Fatalf("status update = %q, want %q", updatedState.State.ID, stateID)
		}
		bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
		if got := findIssue(t, bootstrap.Issues, created.ID).State.ID; got != stateID {
			t.Fatalf("persisted status = %q, want %q", got, stateID)
		}
	}
	bootstrapAfterStateChanges := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrapAfterStateChanges.Activities[created.ID], func(item domain.ActivityEvent) bool {
		return item.Metadata["state"] == "Canceled" && item.Metadata["stateBefore"] == "Todo"
	}) || !slices.ContainsFunc(bootstrapAfterStateChanges.Activities[created.ID], func(item domain.ActivityEvent) bool {
		return item.Metadata["state"] == "Duplicate" && item.Metadata["stateBefore"] == "Canceled"
	}) {
		t.Fatalf("state history did not retain previous states: %#v", bootstrapAfterStateChanges.Activities[created.ID])
	}

	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{
		"title": "Autosaved title", "description": "Autosaved description", "estimate": 8, "subscriberIds": []string{"usr_admin", "usr_member"},
		"descriptionState": `{"type":"doc","content":[{"type":"paragraph"}]}`,
		"descriptionData":  map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}, "contentState": "AQID",
	}, http.StatusOK)
	if updated.Title != "Autosaved title" || updated.Estimate == nil || *updated.Estimate != 8 || updated.DescriptionState == "" || len(updated.SubscriberIDs) != 2 || updated.DocumentContent == nil || updated.DocumentContent.Version != created.DocumentContent.Version+1 || updated.DocumentContent.ContentState != "AQID" || updated.DocumentContent.Content != "Autosaved description" || updated.DocumentContent.ContentData["type"] != "doc" {
		t.Fatalf("update failed: %#v", updated)
	}
	clearedEstimate := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"estimate": 0}, http.StatusOK)
	if clearedEstimate.Estimate != nil {
		t.Fatalf("estimate was not cleared: %#v", clearedEstimate.Estimate)
	}
	conflict := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{
		"description": "Stale snapshot", "descriptionData": map[string]any{"type": "doc"}, "contentState": "STALE", "expectedDocumentVersion": created.DocumentContent.Version,
	}, http.StatusConflict)
	if conflict["code"] != "VERSION_CONFLICT" {
		t.Fatalf("stale document snapshot conflict = %#v", conflict)
	}
	bootstrapAfterSave := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	persisted := findIssue(t, bootstrapAfterSave.Issues, created.ID)
	if persisted.Description != "Autosaved description" || persisted.DescriptionState != updated.DescriptionState || persisted.DocumentContent == nil || persisted.DocumentContent.ContentState != "AQID" || persisted.DocumentContent.ContentData["type"] != "doc" {
		t.Fatalf("editor content did not survive bootstrap round trip: %#v", persisted)
	}
	reactedIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/reactions", map[string]string{"emoji": "🎉"}, http.StatusOK)
	if len(reactedIssue.Reactions["🎉"]) != 1 {
		t.Fatalf("issue reaction = %#v", reactedIssue.Reactions)
	}
	bootstrapAfterReaction := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(findIssue(t, bootstrapAfterReaction.Issues, created.ID).Reactions["🎉"]) != 1 {
		t.Fatal("issue reaction did not survive bootstrap round trip")
	}
	reactedIssue = requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/reactions", map[string]string{"emoji": "🎉"}, http.StatusOK)
	if len(reactedIssue.Reactions) != 0 {
		t.Fatalf("issue reaction toggle off = %#v", reactedIssue.Reactions)
	}

	child := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Child issue", "parentId": created.ID,
	}, http.StatusCreated)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	parent := findIssue(t, bootstrap.Issues, created.ID)
	if child.ParentID == nil || *child.ParentID != created.ID || !contains(parent.SubIssueIDs, child.ID) {
		t.Fatalf("parent linkage missing: parent=%#v child=%#v", parent.SubIssueIDs, child.ParentID)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/"+created.ID, map[string]any{"parentId": child.ID}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPost, "/api/issues/"+child.ID+"/relations", map[string]any{
		"type": "parent_of", "relatedIssueId": created.ID,
	}, http.StatusBadRequest)

	relation := requestJSON[domain.IssueRelation](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/relations", map[string]any{
		"type": "blocks", "relatedIssueId": child.ID,
	}, http.StatusCreated)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	parent = findIssue(t, bootstrap.Issues, created.ID)
	child = findIssue(t, bootstrap.Issues, child.ID)
	if len(parent.Relations) != 1 || len(child.Relations) != 1 || child.Relations[0].Type != "blocked_by" {
		t.Fatalf("inverse relation missing: parent=%#v child=%#v", parent.Relations, child.Relations)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+child.ID+"/relations/"+relation.ID, nil, http.StatusNoContent)

	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/comments", map[string]string{"body": "A persisted comment"}, http.StatusCreated)
	if comment.Body != "A persisted comment" {
		t.Fatalf("comment = %#v", comment)
	}
	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/issues/"+created.ID+"/comments/"+comment.ID, map[string]any{"body": "Edited comment", "bodyData": map[string]any{"type": "doc"}}, http.StatusOK)
	if edited.Body != "Edited comment" || edited.EditedAt == nil {
		t.Fatalf("edited comment = %#v", edited)
	}
	reacted := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/comments/"+comment.ID+"/reactions", map[string]string{"emoji": "👍"}, http.StatusOK)
	if len(reacted.Reactions["👍"]) != 1 {
		t.Fatalf("reaction = %#v", reacted.Reactions)
	}
	reply := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+created.ID+"/comments", map[string]any{"body": "Reply", "parentId": comment.ID}, http.StatusCreated)
	if reply.ParentID == nil || *reply.ParentID != comment.ID {
		t.Fatalf("reply = %#v", reply)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+created.ID+"/comments/"+comment.ID, nil, http.StatusNoContent)

	attachment := upload(t, handler, created.ID, "proof.txt", []byte("proof"))
	if attachment.Size != 5 {
		t.Fatalf("attachment = %#v", attachment)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+created.ID+"/attachments/"+attachment.ID, nil, http.StatusNoContent)

	batch := requestJSON[[]domain.Issue](t, handler, http.MethodPost, "/api/issues/batch", map[string]any{
		"issueIds": []string{created.ID, child.ID}, "update": map[string]any{"priority": 4, "archived": true},
	}, http.StatusOK)
	if len(batch) != 2 || batch[0].Priority != 4 || batch[0].ArchivedAt == nil {
		t.Fatalf("batch update failed: %#v", batch)
	}

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	if len(events) == 0 || events[0].AggregateID != created.ID {
		t.Fatalf("events missing aggregate: %#v", events)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+created.ID, nil, http.StatusNoContent)
}

func TestIssueOptionsPersistence(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]
	previousDescription, previousState := issue.Description, issue.DescriptionState

	nextOccurrence := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second)
	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"description": "Description with history", "descriptionState": `{"type":"doc","content":[]}`,
		"descriptionData": map[string]any{"type": "doc", "content": []any{}}, "contentState": "HISTORY_STATE",
		"recurrence": "weekly", "nextOccurrenceAt": nextOccurrence.Format(time.RFC3339),
	}, http.StatusOK)
	if updated.Recurrence != "weekly" || updated.NextOccurrenceAt == nil || !updated.NextOccurrenceAt.Equal(nextOccurrence) {
		t.Fatalf("recurrence was not returned: %#v", updated)
	}

	link := requestJSON[domain.Attachment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/links", map[string]any{
		"url": "https://example.test/docs", "title": "Reference docs",
	}, http.StatusCreated)
	if link.ContentType != "text/uri-list" || link.Title != "Reference docs" || link.URL != "https://example.test/docs" {
		t.Fatalf("issue link = %#v", link)
	}

	remindAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/reminders", map[string]any{
		"remindAt": remindAt.Format(time.RFC3339),
	}, http.StatusCreated)
	if reminder.IssueID != issue.ID || reminder.Category != "reminders" || reminder.SnoozedUntil == nil || !reminder.SnoozedUntil.Equal(remindAt) {
		t.Fatalf("issue reminder = %#v", reminder)
	}

	loop := requestJSON[domain.Ask](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/loop-runs", map[string]any{
		"prompt": "Check the acceptance criteria",
	}, http.StatusCreated)
	if loop.IssueID != issue.ID || loop.Source != "loop" || loop.Status != "approved" || loop.Body != "Check the acceptance criteria" {
		t.Fatalf("issue loop run = %#v", loop)
	}

	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	persisted := findIssue(t, bootstrap.Issues, issue.ID)
	if persisted.Recurrence != "weekly" || persisted.NextOccurrenceAt == nil || !persisted.NextOccurrenceAt.Equal(nextOccurrence) || !slices.ContainsFunc(persisted.Attachments, func(item domain.Attachment) bool { return item.ID == link.ID }) {
		t.Fatalf("issue menu changes did not survive bootstrap: %#v", persisted)
	}
	if !slices.ContainsFunc(bootstrap.Activities[issue.ID], func(item domain.ActivityEvent) bool {
		return item.Metadata["descriptionBefore"] == previousDescription && item.Metadata["descriptionStateBefore"] == previousState
	}) {
		t.Fatalf("description history did not retain the previous version: %#v", bootstrap.Activities[issue.ID])
	}
	if !slices.ContainsFunc(bootstrap.Notifications, func(item domain.Notification) bool { return item.ID == reminder.ID }) {
		t.Fatal("issue reminder did not survive bootstrap")
	}
	if !slices.ContainsFunc(bootstrap.Asks, func(item domain.Ask) bool { return item.ID == loop.ID }) {
		t.Fatal("loop run did not survive bootstrap")
	}
	restored := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"description": previousDescription, "descriptionState": previousState,
	}, http.StatusOK)
	if restored.Description != previousDescription || restored.DocumentContent == nil || restored.DocumentContent.Content != previousDescription || restored.DocumentContent.ContentData != nil {
		t.Fatalf("description restore left stale structured content: %#v", restored.DocumentContent)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID+"/attachments/"+link.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(findIssue(t, bootstrap.Issues, issue.ID).Attachments, func(item domain.Attachment) bool { return item.ID == link.ID }) {
		t.Fatal("URL attachment was not deleted")
	}

	now := time.Now().UTC()
	err = repository.MutateWorkspace(context.Background(), bootstrap.Workspace.URLKey, "test.issue_delete_dependencies", issue.ID, nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "delivery-delete-test", NotificationID: reminder.ID, RecipientID: data.Viewer.ID, Channel: "desktop", Status: "pending", CreatedAt: now, UpdatedAt: now})
		data.Favorites = append(data.Favorites, domain.Favorite{ID: "favorite-delete-test", UserID: data.Viewer.ID, ResourceType: "issue", ResourceID: issue.ID, CreatedAt: now})
		data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "subscription-delete-test", UserID: data.Viewer.ID, ResourceType: "issue", ResourceID: issue.ID, CreatedAt: now})
		data.Drafts = append(data.Drafts, domain.Draft{ID: "draft-delete-test", UserID: data.Viewer.ID, Type: "issue", ResourceID: issue.ID, CreatedAt: now, UpdatedAt: now})
		data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: "issue-sla-delete-test", IssueID: issue.ID, RuleID: "rule-delete-test", StartedAt: now, DueAt: now.Add(time.Hour), Status: "active"})
		data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: "sla-event-delete-test", IssueID: issue.ID, SLAID: "issue-sla-delete-test", Type: "started", CreatedAt: now})
		data.Releases = append(data.Releases, domain.Release{ID: "release-delete-test", Name: "Delete test", IssueIDs: []string{issue.ID}, CreatedAt: now, UpdatedAt: now})
		data.ProjectTemplates = append(data.ProjectTemplates, domain.ProjectTemplate{ID: "project-template-delete-test", Name: "Delete test", IssueIDs: []string{issue.ID}, CreatedAt: now, UpdatedAt: now})
		data.CustomerRequests = append(data.CustomerRequests, domain.CustomerRequest{ID: "customer-request-delete-test", IssueID: issue.ID, CreatedAt: now, UpdatedAt: now})
		data.Documents = append(data.Documents, domain.Document{ID: "document-delete-test", IssueID: issue.ID, CreatedAt: now, UpdatedAt: now})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+issue.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Notifications, func(item domain.Notification) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.NotificationID == reminder.ID }) ||
		slices.ContainsFunc(bootstrap.Asks, func(item domain.Ask) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.IssueSLAs, func(item domain.IssueSLA) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.SLAEvents, func(item domain.SLAEvent) bool { return item.IssueID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.Drafts, func(item domain.Draft) bool { return item.Type == "issue" && item.ResourceID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "issue" && item.ResourceID == issue.ID }) ||
		slices.ContainsFunc(bootstrap.Subscriptions, func(item domain.Subscription) bool {
			return item.ResourceType == "issue" && item.ResourceID == issue.ID
		}) {
		t.Fatal("deleting an issue left issue-owned records behind")
	}
	if slices.ContainsFunc(bootstrap.Releases, func(item domain.Release) bool { return slices.Contains(item.IssueIDs, issue.ID) }) ||
		slices.ContainsFunc(bootstrap.ProjectTemplates, func(item domain.ProjectTemplate) bool { return slices.Contains(item.IssueIDs, issue.ID) }) {
		t.Fatal("deleting an issue left collection references behind")
	}
	if !slices.ContainsFunc(bootstrap.CustomerRequests, func(item domain.CustomerRequest) bool {
		return item.ID == "customer-request-delete-test" && item.IssueID == ""
	}) ||
		!slices.ContainsFunc(bootstrap.Documents, func(item domain.Document) bool { return item.ID == "document-delete-test" && item.IssueID == "" }) {
		t.Fatal("deleting an issue did not preserve and unlink related content")
	}
	trashIndex := slices.IndexFunc(bootstrap.Trash, func(item domain.TrashEntry) bool { return item.ResourceType == "issue" && item.ResourceID == issue.ID })
	if trashIndex < 0 || !slices.Contains(bootstrap.Trash[trashIndex].TeamIDs, issue.Team.ID) {
		t.Fatalf("deleted issue was not scoped to its team archive: %#v", bootstrap.Trash)
	}
}

func TestSearchHistoryRecentResourcesAndConcurrentIssuePatches(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]

	search := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search?q="+issue.Identifier, nil, http.StatusOK)
	if len(search.Results) == 0 || search.Results[0].ID != issue.ID || search.Results[0].Type != "issue" {
		t.Fatalf("issue search = %#v", search.Results)
	}
	if len(search.History) == 0 || search.History[0].Query != issue.Identifier {
		t.Fatalf("search history = %#v", search.History)
	}
	requestJSON[any](t, handler, http.MethodPost, "/api/recent", map[string]string{"type": "issue", "id": issue.ID}, http.StatusNoContent)
	recent := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search", nil, http.StatusOK)
	if len(recent.Results) == 0 || recent.Results[0].ID != issue.ID {
		t.Fatalf("recent resources = %#v", recent.Results)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/search/history", nil, http.StatusNoContent)
	cleared := requestJSON[domain.SearchResponse](t, handler, http.MethodGet, "/api/search", nil, http.StatusOK)
	if len(cleared.History) != 0 {
		t.Fatalf("search history was not cleared: %#v", cleared.History)
	}

	updated := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"priority": 1, "expectedVersion": issue.Version}, http.StatusOK)
	if updated.Version != issue.Version+1 {
		t.Fatalf("issue version = %d, want %d", updated.Version, issue.Version+1)
	}
	merged := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{"title": "Stale writer", "expectedVersion": issue.Version}, http.StatusOK)
	if merged.Title != "Stale writer" || merged.Priority != 1 || merged.Version != issue.Version+2 {
		t.Fatalf("concurrent issue patch did not merge by field: %#v", merged)
	}

	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/issues/"+issue.ID+"/comments", map[string]string{"body": "Versioned"}, http.StatusCreated)
	afterComment := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(afterComment.Notifications, func(notification domain.Notification) bool {
		return notification.CommentID == comment.ID && notification.RecipientID != afterComment.Viewer.ID && notification.ReadAt == nil
	}) {
		t.Fatalf("comment did not create an unread recipient notification: %#v", afterComment.Notifications)
	}
	edited := requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+comment.ID, map[string]any{"body": "First edit", "expectedVersion": comment.Version}, http.StatusOK)
	if edited.Version != comment.Version+1 {
		t.Fatalf("comment version = %d, want %d", edited.Version, comment.Version+1)
	}
	commentConflict := requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/issues/"+issue.ID+"/comments/"+comment.ID, map[string]any{"body": "Stale edit", "expectedVersion": comment.Version}, http.StatusConflict)
	if commentConflict["code"] != "VERSION_CONFLICT" || commentConflict["current"] == nil {
		t.Fatalf("comment conflict payload = %#v", commentConflict)
	}
}

func TestWorkspaceSearchIndexesUnifiedResourceTypes(t *testing.T) {
	now := time.Now().UTC()
	data := domain.Bootstrap{
		Documents:  []domain.Document{{ID: "doc-1", Title: "Launch handbook", Icon: "BookOpen", Color: "#eb5757", Content: "Customer rollout", UpdatedAt: now}},
		Customers:  []domain.Customer{{ID: "customer-1", Name: "Northstar Labs", Domains: []string{"northstar.example"}, Status: "active", UpdatedAt: now}},
		Releases:   []domain.Release{{ID: "release-1", Name: "Summer launch", Version: "v2.0", Status: "planned", UpdatedAt: now}},
		SavedViews: []domain.SavedView{{ID: "view-1", Name: "Escalated requests", Description: "High priority customer work", Resource: "issues", Scope: "workspace", UpdatedAt: now}},
	}

	for query, expected := range map[string]string{
		"Launch handbook":    "document",
		"northstar.example":  "customer",
		"Summer launch":      "release",
		"Escalated requests": "view",
	} {
		results := buildSearchResults(data, query, searchTypes(""))
		if len(results) != 1 || results[0].Type != expected {
			t.Fatalf("search %q = %#v, want one %s", query, results, expected)
		}
		if !searchResourceVisible(data, results[0].Type, results[0].ID) {
			t.Fatalf("search result %s:%s is not recordable as recent", results[0].Type, results[0].ID)
		}
		if expected == "document" && (results[0].Icon != "BookOpen" || results[0].Color != "#eb5757") {
			t.Fatalf("document search visual = %#v", results[0])
		}
	}
}

func TestSemanticSearchIndexesAllResourceTypes(t *testing.T) {
	handler, repository := newContentFeatureHandler(t)
	defer repository.Close()
	viewer := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Viewer
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Semantic initiative"}, http.StatusCreated)
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Semantic customer"}, http.StatusCreated)
	release := requestJSON[domain.Release](t, handler, http.MethodPost, "/api/releases", map[string]any{"name": "Semantic release"}, http.StatusCreated)
	view := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{"name": "Semantic saved view", "resource": "issues", "scope": "workspace"}, http.StatusCreated)

	queries := map[string]struct {
		query    string
		typeName string
	}{
		"initiative": {query: initiative.Name, typeName: "initiative"},
		"member":     {query: viewer.DisplayName, typeName: "member"},
		"customer":   {query: customer.Name, typeName: "customer"},
		"release":    {query: release.Name, typeName: "release"},
		"view":       {query: view.Name, typeName: "view"},
	}
	for name, item := range queries {
		result := requestJSON[struct {
			Results []semanticResult `json:"results"`
			Total   int              `json:"total"`
		}](t, handler, http.MethodGet, "/api/search/semantic?q="+url.QueryEscape(item.query)+"&types="+item.typeName, nil, http.StatusOK)
		if result.Total == 0 || len(result.Results) == 0 || result.Results[0].Type != item.typeName {
			t.Fatalf("semantic %s search = %#v, want a %s result", name, result, item.typeName)
		}
	}
}

func TestRealtimeHubWorkspaceIsolationAndPresence(t *testing.T) {
	hub := newRealtimeHub()
	testWorkspace, unsubscribeTestWorkspace := hub.subscribe("test-workspace")
	defer unsubscribeTestWorkspace()
	other, unsubscribeOther := hub.subscribe("other")
	defer unsubscribeOther()
	event := domain.RealtimeEvent{ID: "event_1", Type: "issue.updated", AggregateID: "issue_1", CreatedAt: time.Now().UTC()}
	hub.publish("test-workspace", event)
	select {
	case received := <-testWorkspace:
		if received.ID != event.ID {
			t.Fatalf("received event = %#v", received)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace subscriber did not receive event")
	}
	select {
	case leaked := <-other:
		t.Fatalf("event leaked across workspaces: %#v", leaked)
	case <-time.After(20 * time.Millisecond):
	}

	viewer := domain.User{ID: "user_1", DisplayName: "Viewer"}
	presence := hub.updatePresence("test-workspace", "client_1", viewer, "issue_1", "/test-workspace/issue/TST-1")
	if len(presence) != 1 || presence[0].ClientID != "client_1" || presence[0].IssueID != "issue_1" {
		t.Fatalf("presence = %#v", presence)
	}
	if got := hub.snapshotPresence("other"); len(got) != 0 {
		t.Fatalf("presence leaked across workspaces: %#v", got)
	}
	if got := hub.removePresence("test-workspace", "client_1"); len(got) != 0 {
		t.Fatalf("presence was not removed: %#v", got)
	}
}

func TestPulsePreferencesViewsAndUpdateAttachments(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	settings := bootstrap.UserSettings[bootstrap.Viewer.ID]
	settings.PulseSchedule = "weekly"
	updatedSettings := requestJSON[domain.UserSettings](t, handler, http.MethodPatch, "/api/account/settings", settings, http.StatusOK)
	if updatedSettings.PulseSchedule != "weekly" {
		t.Fatalf("pulse schedule=%q", updatedSettings.PulseSchedule)
	}

	name, resource, scope, ownerID, view := "Project health", "pulse", "workspace", "usr_other", "all"
	saved := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", domain.SavedViewMutationInput{Name: &name, Resource: &resource, Scope: &scope, OwnerID: &ownerID, View: &view, Filters: json.RawMessage(`[{"id":"health","field":"health","operator":"is","values":["atRisk"]}]`), Display: json.RawMessage(`{"match":"all"}`)}, http.StatusCreated)
	if saved.Resource != "pulse" || saved.Scope != "personal" || saved.OwnerID != bootstrap.Viewer.ID {
		t.Fatalf("pulse view ownership=%#v", saved)
	}

	project := bootstrap.Projects[0]
	update := requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/updates", map[string]any{"body": "Structured Pulse update", "bodyData": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}, "health": "atRisk"}, http.StatusCreated)
	if update.BodyData["type"] != "doc" {
		t.Fatalf("update body data=%#v", update.BodyData)
	}
	update = uploadPulseAttachmentForTest(t, handler, "/api/projects/"+project.ID+"/updates/"+update.ID+"/attachments", "pulse.txt", "pulse attachment")
	if len(update.Attachments) != 1 || update.Attachments[0].Title != "pulse.txt" {
		t.Fatalf("update attachments=%#v", update.Attachments)
	}
	update = requestJSON[domain.ProjectUpdate](t, handler, http.MethodDelete, "/api/projects/"+project.ID+"/updates/"+update.ID+"/attachments/"+update.Attachments[0].ID, nil, http.StatusOK)
	if len(update.Attachments) != 0 {
		t.Fatalf("attachment was not deleted: %#v", update.Attachments)
	}

	persisted := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if persisted.UserSettings[persisted.Viewer.ID].PulseSchedule != "weekly" || !slices.ContainsFunc(persisted.SavedViews, func(item domain.SavedView) bool { return item.ID == saved.ID }) {
		t.Fatal("Pulse preferences or view did not persist")
	}
}

func TestProjectScopedSavedViewPersistsProjectIdentity(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Projects) == 0 {
		t.Fatal("seed must include a project")
	}
	name, resource, scope, projectID := "Project work", "issues", "workspace", bootstrap.Projects[0].ID
	view := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", domain.SavedViewMutationInput{Name: &name, Resource: &resource, Scope: &scope, ProjectID: &projectID}, http.StatusCreated)
	if view.ProjectID != projectID || view.Resource != resource {
		t.Fatalf("project view identity was not persisted: %#v", view)
	}
	updated := requestJSON[domain.SavedView](t, handler, http.MethodPatch, "/api/views/"+view.ID, domain.SavedViewMutationInput{ProjectID: &projectID}, http.StatusOK)
	if updated.ProjectID != projectID {
		t.Fatalf("project view identity was lost during update: %#v", updated)
	}
	invalid := "missing-project"
	requestJSON[any](t, handler, http.MethodPost, "/api/views", domain.SavedViewMutationInput{Name: &name, Resource: &resource, ProjectID: &invalid}, http.StatusBadRequest)
}

func uploadPulseAttachmentForTest(t *testing.T, handler http.Handler, path, name, contents string) domain.ProjectUpdate {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST %s status %d: %s", path, recorder.Code, recorder.Body.String())
	}
	var update domain.ProjectUpdate
	if err = json.Unmarshal(recorder.Body.Bytes(), &update); err != nil {
		t.Fatal(err)
	}
	return update
}

func TestCyclePlanningAndRollover(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Cycles) < 3 || !bootstrap.CycleSettings["team_test"].Enabled {
		t.Fatalf("cycle bootstrap missing: cycles=%#v settings=%#v", bootstrap.Cycles, bootstrap.CycleSettings)
	}
	var current, upcoming domain.Cycle
	for _, cycle := range bootstrap.Cycles {
		if cycle.Status == "current" {
			current = cycle
		}
		if cycle.Status == "upcoming" && (upcoming.ID == "" || cycle.StartsAt.Before(upcoming.StartsAt)) {
			upcoming = cycle
		}
	}
	if current.ID == "" || upcoming.ID == "" {
		t.Fatalf("current/upcoming cycle missing: %#v", bootstrap.Cycles)
	}
	updated := requestJSON[domain.Cycle](t, handler, http.MethodPatch, "/api/cycles/"+upcoming.ID, map[string]any{"name": "Release cycle", "capacity": 8, "favorite": true}, http.StatusOK)
	if updated.Name != "Release cycle" || updated.Capacity != 8 || !updated.Favorite {
		t.Fatalf("cycle update failed: %#v", updated)
	}
	issue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": "Rollover candidate", "stateId": "state_todo", "cycleId": current.ID}, http.StatusCreated)
	if issue.CycleID == nil || *issue.CycleID != current.ID {
		t.Fatalf("issue cycle assignment failed: %#v", issue.CycleID)
	}
	started := requestJSON[domain.Cycle](t, handler, http.MethodPost, "/api/cycles/"+upcoming.ID+"/start", nil, http.StatusOK)
	if started.Status != "current" {
		t.Fatalf("started cycle = %#v", started)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	rolled := findIssue(t, bootstrap.Issues, issue.ID)
	if rolled.CycleID == nil || *rolled.CycleID != upcoming.ID {
		t.Fatalf("issue did not roll over: %#v", rolled.CycleID)
	}
	settings := requestJSON[domain.CycleSettings](t, handler, http.MethodPatch, "/api/teams/team_test/cycle-settings", map[string]any{"durationWeeks": 3, "upcomingCount": 3, "autoAddStarted": true}, http.StatusOK)
	if settings.DurationWeeks != 3 || settings.UpcomingCount != 3 || !settings.AutoAddStarted {
		t.Fatalf("cycle settings update failed: %#v", settings)
	}
	requestJSON[domain.Cycle](t, handler, http.MethodPost, "/api/cycles/"+upcoming.ID+"/complete", nil, http.StatusOK)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	currentCount, upcomingCount := 0, 0
	for _, cycle := range bootstrap.Cycles {
		if cycle.Status == "current" {
			currentCount++
		}
		if cycle.Status == "upcoming" {
			upcomingCount++
		}
	}
	if currentCount != 1 || upcomingCount < 3 {
		t.Fatalf("cycle recurrence failed: current=%d upcoming=%d cycles=%#v", currentCount, upcomingCount, bootstrap.Cycles)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+upcoming.ID, nil, http.StatusOK)
	if len(events) < 3 || events[0].Type != "cycle.updated" || events[1].Type != "cycle.started" || events[2].Type != "cycle.completed" {
		t.Fatalf("cycle events = %#v", events)
	}
}

func TestProjectLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Project API test", "summary": "Initial", "priority": 2, "health": "onTrack",
		"leadId": "usr_admin", "teamIds": []string{"team_test"},
	}, http.StatusCreated)
	if created.ID == "" || created.Name != "Project API test" || created.Icon != "Project" || created.Priority != 2 || created.Lead == nil {
		t.Fatalf("project create failed: %#v", created)
	}
	createdWithMilestone := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Project milestone create test", "teamIds": []string{"team_test"},
		"milestones": []string{"Launch", "  ", "Ship"}, "targetDate": "2026-09-30", "targetDateResolution": "month",
	}, http.StatusCreated)
	if len(createdWithMilestone.Milestones) != 2 || createdWithMilestone.Milestones[0].Name != "Launch" || createdWithMilestone.Milestones[1].Name != "Ship" || createdWithMilestone.TargetDateResolution != "month" {
		t.Fatalf("project create milestones failed: %#v", createdWithMilestone.Milestones)
	}

	updated := requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"name": "Updated project", "health": "atRisk", "targetDate": "2026-09-30", "statusId": "ps_planned",
		"memberIds": []string{"usr_admin", "usr_member"}, "labelIds": []string{"label_delivery"}, "description": "First project description", "updateCadence": "weekly",
	}, http.StatusOK)
	if updated.Name != "Updated project" || updated.Health != "atRisk" || updated.Status.ID != "ps_planned" || updated.TargetDate == nil || *updated.TargetDate != "2026-09-30" || !slices.Equal(updated.MemberIDs, []string{"usr_admin", "usr_member"}) || !slices.Equal(updated.LabelIDs, []string{"label_delivery"}) {
		t.Fatalf("project update failed: %#v", updated)
	}
	updated = requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{"description": "Second project description"}, http.StatusOK)
	if updated.UpdateCadence != "weekly" || len(updated.DescriptionRevisions) < 1 || updated.DescriptionRevisions[0].Description != "First project description" {
		t.Fatalf("project cadence or description history was not persisted: %#v", updated)
	}
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/reminders", map[string]any{"remindAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}, http.StatusCreated)
	if reminder.ProjectID != created.ID || reminder.Type != "projectReminder" || reminder.SnoozedUntil == nil {
		t.Fatalf("project reminder was not created: %#v", reminder)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{"labelIds": []string{"label_type_defect"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/issue_33", map[string]any{"labelIds": []string{"label_product"}}, http.StatusBadRequest)

	resource := requestJSON[domain.ProjectResource](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/resources", map[string]any{
		"type": "link", "title": "Launch brief", "url": "https://example.com/brief",
	}, http.StatusCreated)
	if resource.ID == "" || resource.ProjectID != created.ID || resource.Title != "Launch brief" {
		t.Fatalf("project resource create failed: %#v", resource)
	}
	resource = requestJSON[domain.ProjectResource](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/resources/"+resource.ID, map[string]any{
		"title": "Edited brief", "url": "https://example.com/edited", "pinnedTeamIds": []string{"team_test"},
	}, http.StatusOK)
	if resource.Title != "Edited brief" || resource.URL != "https://example.com/edited" || !slices.Equal(resource.PinnedTeamIDs, []string{"team_test"}) {
		t.Fatalf("project resource update failed: %#v", resource)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/resources/"+resource.ID, map[string]any{"pinnedTeamIds": []string{"missing-team"}}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/resources/"+resource.ID, nil, http.StatusNoContent)

	milestone := requestJSON[domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/milestones", map[string]any{
		"name": "Public beta", "description": "Invite the first customer cohort.", "targetDate": "2026-08-30",
	}, http.StatusCreated)
	if milestone.ID == "" || milestone.ProjectID != created.ID || milestone.Description != "Invite the first customer cohort." || milestone.TargetDate == nil || *milestone.TargetDate != "2026-08-30" {
		t.Fatalf("project milestone create failed: %#v", milestone)
	}
	milestone = requestJSON[domain.ProjectMilestone](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/milestones/"+milestone.ID, map[string]any{
		"name": "General availability", "description": "Launch to every workspace.", "targetDate": "2026-09-15",
	}, http.StatusOK)
	if milestone.Name != "General availability" || milestone.Description != "Launch to every workspace." || milestone.TargetDate == nil || *milestone.TargetDate != "2026-09-15" {
		t.Fatalf("project milestone update failed: %#v", milestone)
	}
	milestoneIssue := requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{
		"title": "Milestone-scoped work", "projectId": created.ID, "projectMilestoneId": milestone.ID,
	}, http.StatusCreated)
	if milestoneIssue.ProjectMilestoneID == nil || *milestoneIssue.ProjectMilestoneID != milestone.ID {
		t.Fatalf("issue milestone assignment failed: %#v", milestoneIssue)
	}
	requestJSON[any](t, handler, http.MethodPatch, "/api/issues/issue_33", map[string]any{"projectMilestoneId": milestone.ID}, http.StatusBadRequest)
	secondMilestone := requestJSON[domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/milestones", map[string]any{
		"name": "Public launch", "targetDate": "2026-10-01",
	}, http.StatusCreated)
	reordered := requestJSON[[]domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/milestones/reorder", map[string]any{
		"ids": []string{secondMilestone.ID, milestone.ID},
	}, http.StatusOK)
	if len(reordered) != 2 || reordered[0].ID != secondMilestone.ID || reordered[1].ID != milestone.ID {
		t.Fatalf("project milestone reorder failed: %#v", reordered)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/milestones/"+milestone.ID, nil, http.StatusNoContent)
	bootstrapAfterMilestoneDelete := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if assigned := findIssue(t, bootstrapAfterMilestoneDelete.Issues, milestoneIssue.ID).ProjectMilestoneID; assigned != nil {
		t.Fatalf("deleted milestone remained assigned to issue: %q", *assigned)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/milestones/"+secondMilestone.ID, nil, http.StatusNoContent)

	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/comments", map[string]any{
		"body": "Project-level discussion persists.",
	}, http.StatusCreated)
	if comment.ID == "" || comment.Body != "Project-level discussion persists." || comment.User.ID != "usr_admin" {
		t.Fatalf("project comment create failed: %#v", comment)
	}
	bootstrapAfterDetails := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, persisted := range bootstrapAfterDetails.Projects {
		if persisted.ID == created.ID && (len(persisted.Resources) != 0 || len(persisted.Milestones) != 0 || len(persisted.Comments) != 1) {
			t.Fatalf("project detail collections did not persist: %#v", persisted)
		}
	}

	projectUpdate := requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/updates", map[string]any{
		"body": "The API-backed update stream persists.", "health": "onTrack",
	}, http.StatusCreated)
	if projectUpdate.ID == "" || projectUpdate.ProjectID != created.ID || projectUpdate.Health != "onTrack" {
		t.Fatalf("project update create failed: %#v", projectUpdate)
	}
	projectUpdate = requestJSON[domain.ProjectUpdate](t, handler, http.MethodPatch, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID, map[string]any{
		"body": "The persisted update was edited.", "health": "offTrack",
	}, http.StatusOK)
	if projectUpdate.Body != "The persisted update was edited." || projectUpdate.Health != "offTrack" || projectUpdate.EditedAt == nil {
		t.Fatalf("project update edit failed: %#v", projectUpdate)
	}
	projectUpdate = requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID+"/comments", map[string]any{
		"body": "Update comment",
	}, http.StatusCreated)
	if len(projectUpdate.Comments) != 1 || projectUpdate.Comments[0].Body != "Update comment" {
		t.Fatalf("project update comment failed: %#v", projectUpdate)
	}
	projectUpdate = requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID+"/reactions", map[string]any{
		"emoji": "👍",
	}, http.StatusOK)
	if !slices.Equal(projectUpdate.Reactions["👍"], []string{"usr_admin"}) {
		t.Fatalf("project update reaction failed: %#v", projectUpdate)
	}
	bootstrapAfterUpdate := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrapAfterUpdate.ProjectStatuses) != 5 {
		t.Fatalf("project status dictionary missing: %#v", bootstrapAfterUpdate.ProjectStatuses)
	}
	if len(bootstrapAfterUpdate.ProjectUpdates[created.ID]) != 1 {
		t.Fatalf("project update was not persisted: %#v", bootstrapAfterUpdate.ProjectUpdates)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID+"/updates/"+projectUpdate.ID, nil, http.StatusNoContent)
	bootstrapAfterDelete := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, project := range bootstrapAfterDelete.Projects {
		if project.ID == created.ID && project.Health != "noUpdate" {
			t.Fatalf("project health after deleting last update = %s", project.Health)
		}
	}

	viewDefault := requestJSON[map[string]any](t, handler, http.MethodPut, "/api/workspace/project-display-default", map[string]any{
		"display": map[string]any{"layout": "board", "grouping": "Status", "properties": []string{"Summary", "Priority"}},
	}, http.StatusOK)
	if viewDefault["layout"] != "board" || viewDefault["grouping"] != "Status" {
		t.Fatalf("project display default = %#v", viewDefault)
	}
	bootstrapWithDefault := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	var persistedDefault map[string]any
	if err := json.Unmarshal(bootstrapWithDefault.ProjectDisplayDefault, &persistedDefault); err != nil || persistedDefault["layout"] != "board" {
		t.Fatalf("project display default did not persist: %s (%v)", bootstrapWithDefault.ProjectDisplayDefault, err)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+created.ID, nil, http.StatusNoContent)
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	for _, project := range bootstrap.Projects {
		if project.ID == created.ID {
			t.Fatalf("deleted project remains: %s", project.ID)
		}
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	expectedEvents := []string{"project.created", "project.updated", "project.updated", "project.reminder_created", "project.resource_created", "project.resource_updated", "project.resource_deleted", "project.milestone_created", "project.milestone_updated", "project.milestone_created", "project.milestones_reordered", "project.milestone_deleted", "project.milestone_deleted", "project.commented", "project.update_created", "project.update_updated", "project.update_commented", "project.update_reaction_toggled", "project.update_deleted", "project.deleted"}
	if len(events) != len(expectedEvents) {
		t.Fatalf("project events = %#v", events)
	}
	for index, eventType := range expectedEvents {
		if events[index].Type != eventType {
			t.Fatalf("project event %d = %q, want %q", index, events[index].Type, eventType)
		}
	}
}

func TestProjectDirectionalDependencies(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	blocker := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Dependency blocker", "teamIds": []string{"team_test"},
	}, http.StatusCreated)
	blocked := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Dependency blocked", "teamIds": []string{"team_test"},
	}, http.StatusCreated)
	created := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{
		"name": "Directional dependency project", "teamIds": []string{"team_test"},
		"dependencyRelations": []map[string]any{
			{"projectId": blocker.ID, "type": "blocked_by"},
			{"projectId": blocked.ID, "type": "blocks"},
			{"projectId": blocker.ID, "type": "blocked_by"},
		},
	}, http.StatusCreated)
	if !slices.Equal(created.DependencyIDs, []string{blocker.ID}) {
		t.Fatalf("blocked-by compatibility ids = %#v", created.DependencyIDs)
	}

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	relations := make([]domain.ProjectRelation, 0, 2)
	for _, relation := range bootstrap.ProjectRelations {
		if relation.ProjectID == created.ID {
			relations = append(relations, relation)
		}
	}
	if len(relations) != 2 || !slices.ContainsFunc(relations, func(relation domain.ProjectRelation) bool {
		return relation.RelatedProjectID == blocker.ID && relation.Type == "blocked_by"
	}) || !slices.ContainsFunc(relations, func(relation domain.ProjectRelation) bool {
		return relation.RelatedProjectID == blocked.ID && relation.Type == "blocks"
	}) {
		t.Fatalf("directional create relations = %#v", relations)
	}

	updated := requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"dependencyRelations": []map[string]any{{"projectId": blocker.ID, "type": "blocks"}},
	}, http.StatusOK)
	if len(updated.DependencyIDs) != 0 {
		t.Fatalf("directional replacement retained blocked-by ids: %#v", updated.DependencyIDs)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	relations = relations[:0]
	for _, relation := range bootstrap.ProjectRelations {
		if relation.ProjectID == created.ID {
			relations = append(relations, relation)
		}
	}
	if len(relations) != 1 || relations[0].RelatedProjectID != blocker.ID || relations[0].Type != "blocks" {
		t.Fatalf("directional replacement relations = %#v", relations)
	}

	updated = requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"dependencyIds": []string{blocked.ID},
	}, http.StatusOK)
	if !slices.Equal(updated.DependencyIDs, []string{blocked.ID}) {
		t.Fatalf("legacy dependency ids = %#v", updated.DependencyIDs)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	relations = relations[:0]
	for _, relation := range bootstrap.ProjectRelations {
		if relation.ProjectID == created.ID {
			relations = append(relations, relation)
		}
	}
	if len(relations) != 2 || !slices.ContainsFunc(relations, func(relation domain.ProjectRelation) bool {
		return relation.RelatedProjectID == blocker.ID && relation.Type == "blocks"
	}) || !slices.ContainsFunc(relations, func(relation domain.ProjectRelation) bool {
		return relation.RelatedProjectID == blocked.ID && relation.Type == "blocked_by"
	}) {
		t.Fatalf("legacy update did not preserve directional blocks relation: %#v", relations)
	}

	updated = requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"dependencyRelations": []map[string]any{},
	}, http.StatusOK)
	if len(updated.DependencyIDs) != 0 {
		t.Fatalf("cleared directional dependencies retained ids: %#v", updated.DependencyIDs)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.ProjectRelations, func(relation domain.ProjectRelation) bool {
		return relation.ProjectID == created.ID && (relation.Type == "blocked_by" || relation.Type == "blocks")
	}) {
		t.Fatalf("cleared directional dependencies retained relations: %#v", bootstrap.ProjectRelations)
	}

	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"dependencyRelations": []map[string]any{
			{"projectId": blocker.ID, "type": "blocked_by"},
			{"projectId": blocker.ID, "type": "blocks"},
		},
	}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"dependencyRelations": []map[string]any{{"projectId": "missing-project", "type": "blocked_by"}},
	}, http.StatusBadRequest)
	requestJSON[any](t, handler, http.MethodPatch, "/api/projects/"+created.ID, map[string]any{
		"dependencyRelations": []map[string]any{{"projectId": blocker.ID, "type": "related"}},
	}, http.StatusBadRequest)
}

func TestProjectDeletionRemovesDependencies(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	blocker := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Delete blocker", "teamIds": []string{"team_test"}}, http.StatusCreated)
	blocked := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Delete blocked", "teamIds": []string{"team_test"}, "dependencyRelations": []map[string]any{{"projectId": blocker.ID, "type": "blocked_by"}}}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+blocker.ID, nil, http.StatusNoContent)

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.ProjectRelations, func(relation domain.ProjectRelation) bool {
		return relation.ProjectID == blocked.ID || relation.RelatedProjectID == blocker.ID
	}) {
		t.Fatalf("project deletion retained dependency relations: %#v", bootstrap.ProjectRelations)
	}
	for _, project := range bootstrap.Projects {
		if project.ID == blocked.ID && slices.Contains(project.DependencyIDs, blocker.ID) {
			t.Fatalf("project deletion retained legacy dependency id: %#v", project.DependencyIDs)
		}
	}
}

func TestCustomerLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{
		"name": "Customer API test", "ownerId": "usr_admin", "status": "active", "tier": "Enterprise",
		"annualRevenue": 250000, "size": 120, "domains": []string{"example.com", "example.org"},
	}, http.StatusCreated)
	if created.ID == "" || created.Name != "Customer API test" || created.OwnerID != "usr_admin" || created.Status != "active" || created.Tier != "Enterprise" || created.AnnualRevenue != 250000 || created.Size != 120 || !slices.Equal(created.Domains, []string{"example.com", "example.org"}) {
		t.Fatalf("customer create failed: %#v", created)
	}

	updated := requestJSON[domain.Customer](t, handler, http.MethodPatch, "/api/customers/"+created.ID, map[string]any{
		"name": "Updated customer", "status": "inactive", "tier": "Mid-market", "domains": []string{"updated.example.com"},
	}, http.StatusOK)
	if updated.Name != "Updated customer" || updated.Status != "inactive" || updated.Tier != "Mid-market" || !slices.Equal(updated.Domains, []string{"updated.example.com"}) {
		t.Fatalf("customer update failed: %#v", updated)
	}

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(bootstrap.Customers, func(customer domain.Customer) bool {
		return customer.ID == created.ID && customer.Name == updated.Name && customer.Status == updated.Status
	}) {
		t.Fatalf("customer did not survive bootstrap round trip: %#v", bootstrap.Customers)
	}

	requestJSON[any](t, handler, http.MethodDelete, "/api/customers/"+created.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if slices.ContainsFunc(bootstrap.Customers, func(customer domain.Customer) bool { return customer.ID == created.ID }) {
		t.Fatalf("deleted customer remains: %#v", bootstrap.Customers)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	expected := []string{"customer.created", "customer.updated", "customer.deleted"}
	if len(events) != len(expected) {
		t.Fatalf("customer events = %#v", events)
	}
	for index, eventType := range expected {
		if events[index].Type != eventType {
			t.Fatalf("customer event %d = %q, want %q", index, events[index].Type, eventType)
		}
	}
}

func TestInitiativeLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	created := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{
		"name": "Initiative API test", "summary": "Initial", "status": "planned", "priority": 2,
		"ownerId": "usr_admin", "targetDate": "2026-12-31", "projectIds": []string{"project_cruise"}, "leadTeamId": "team_test", "contributingTeamIds": []string{"team_test"},
	}, http.StatusCreated)
	if created.ID == "" || created.Status != "planned" || created.Priority != 2 || created.Owner == nil || created.LeadTeamID != "team_test" || !slices.Equal(created.ContributingTeamIDs, []string{"team_test"}) || !slices.Equal(created.ProjectIDs, []string{"project_cruise"}) {
		t.Fatalf("initiative create failed: %#v", created)
	}
	initiativeLabel := requestJSON[domain.IssueLabel](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Initiative label", "resourceType": "initiative",
	}, http.StatusCreated)
	if initiativeLabel.ResourceType != "initiative" || initiativeLabel.GroupID != "" || !labelScopeIsWorkspace(initiativeLabel.Scope) {
		t.Fatalf("initiative label contract failed: %#v", initiativeLabel)
	}
	requestJSON[map[string]any](t, handler, http.MethodPost, "/api/labels", map[string]any{
		"name": "Invalid grouped initiative label", "resourceType": "initiative", "groupId": "label_group_work_type",
	}, http.StatusBadRequest)
	requestJSON[map[string]any](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID, map[string]any{
		"labelIds": []string{"label_type_defect"},
	}, http.StatusBadRequest)
	updated := requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID, map[string]any{
		"name": "Updated initiative", "description": "Persistent description", "status": "active", "health": "atRisk",
		"labelIds": []string{initiativeLabel.ID}, "favorite": true, "subscribed": true,
		"notificationRules": map[string]any{"descriptionChanges": true, "newUpdate": false, "allProjectUpdates": true},
		"updateSchedule":    map[string]any{"cadence": "weekly", "weekday": 2, "timeRange": "09:00-12:00"},
	}, http.StatusOK)
	if updated.Name != "Updated initiative" || updated.Status != "active" || updated.Health != "atRisk" || !updated.Favorite || !updated.Subscribed || len(updated.DescriptionHistory) != 1 || updated.NotificationRules.NewUpdate || updated.UpdateSchedule.Cadence != "weekly" || !slices.Equal(updated.LabelIDs, []string{initiativeLabel.ID}) {
		t.Fatalf("initiative update failed: %#v", updated)
	}
	resource := requestJSON[domain.InitiativeResource](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/resources", map[string]any{"type": "link", "title": "Strategy", "url": "https://example.com/strategy"}, http.StatusCreated)
	if resource.ID == "" || resource.InitiativeID != created.ID {
		t.Fatalf("initiative resource failed: %#v", resource)
	}
	resource = requestJSON[domain.InitiativeResource](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID+"/resources/"+resource.ID, map[string]any{"title": "Updated strategy", "url": "https://example.com/updated-strategy"}, http.StatusOK)
	if resource.Title != "Updated strategy" || resource.URL != "https://example.com/updated-strategy" {
		t.Fatalf("initiative resource update failed: %#v", resource)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID+"/resources/"+resource.ID, nil, http.StatusNoContent)
	comment := requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/comments", map[string]string{"body": "Persistent initiative comment"}, http.StatusCreated)
	if comment.Body != "Persistent initiative comment" {
		t.Fatalf("initiative comment failed: %#v", comment)
	}
	comment = requestJSON[domain.Comment](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID+"/comments/"+comment.ID, map[string]string{"body": "Edited initiative comment"}, http.StatusOK)
	if comment.Body != "Edited initiative comment" || comment.EditedAt == nil {
		t.Fatalf("initiative comment update failed: %#v", comment)
	}
	comment = requestJSON[domain.Comment](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/comments/"+comment.ID+"/reactions", map[string]string{"emoji": "thumbs-up"}, http.StatusOK)
	if !slices.Equal(comment.Reactions["thumbs-up"], []string{"usr_admin"}) {
		t.Fatalf("initiative comment reaction failed: %#v", comment)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID+"/comments/"+comment.ID, nil, http.StatusNoContent)
	initiativeUpdate := requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/updates", map[string]string{"body": "Delivery remains on plan", "health": "onTrack"}, http.StatusCreated)
	initiativeUpdate = requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPatch, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID, map[string]string{"body": "Edited delivery update", "health": "atRisk"}, http.StatusOK)
	if initiativeUpdate.Body != "Edited delivery update" || initiativeUpdate.EditedAt == nil {
		t.Fatalf("initiative update edit failed: %#v", initiativeUpdate)
	}
	initiativeUpdate = requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID+"/comments", map[string]string{"body": "Pulse initiative comment"}, http.StatusCreated)
	if len(initiativeUpdate.Comments) != 1 || initiativeUpdate.Comments[0].Body != "Pulse initiative comment" {
		t.Fatalf("initiative update comment failed: %#v", initiativeUpdate)
	}
	initiativeUpdate = requestJSON[domain.InitiativeUpdate](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID+"/reactions", map[string]string{"emoji": "thumbs-up"}, http.StatusOK)
	if !slices.Equal(initiativeUpdate.Reactions["thumbs-up"], []string{"usr_admin"}) {
		t.Fatalf("initiative update reaction failed: %#v", initiativeUpdate)
	}
	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.InitiativeUpdates[created.ID]) != 1 || len(bootstrap.InitiativeUpdates[created.ID][0].Comments) != 1 || !slices.Equal(bootstrap.InitiativeUpdates[created.ID][0].Reactions["thumbs-up"], []string{"usr_admin"}) || !slices.ContainsFunc(bootstrap.Projects, func(project domain.Project) bool {
		return project.ID == "project_cruise" && slices.Contains(project.Initiatives, created.ID)
	}) {
		t.Fatalf("initiative bootstrap projection failed: %#v", bootstrap.InitiativeUpdates[created.ID])
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID+"/updates/"+initiativeUpdate.ID, nil, http.StatusNoContent)
	reminder := requestJSON[domain.Notification](t, handler, http.MethodPost, "/api/initiatives/"+created.ID+"/reminders", map[string]string{"remindAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}, http.StatusCreated)
	if reminder.SourceType != "initiative" || reminder.SourceID != created.ID || reminder.SnoozedUntil == nil {
		t.Fatalf("initiative reminder failed: %#v", reminder)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+created.ID, nil, http.StatusNoContent)
	deletedBootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	trashIndex := slices.IndexFunc(deletedBootstrap.Trash, func(entry domain.TrashEntry) bool {
		return entry.ResourceType == "initiative" && entry.ResourceID == created.ID
	})
	if trashIndex < 0 {
		t.Fatalf("deleted initiative was not retained in trash: %#v", deletedBootstrap.Trash)
	}
	restored := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/trash/"+deletedBootstrap.Trash[trashIndex].ID+"/restore", nil, http.StatusOK)
	if restored.ID != created.ID || restored.LeadTeamID != "team_test" {
		t.Fatalf("initiative restore failed: %#v", restored)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	expected := []string{"initiative.created", "initiative.updated", "initiative.resource_created", "initiative.resource_updated", "initiative.resource_deleted", "initiative.commented", "initiative.comment_updated", "initiative.comment_reaction_toggled", "initiative.comment_deleted", "initiative.update_created", "initiative.update_updated", "initiative.update_commented", "initiative.update_reaction_toggled", "initiative.update_deleted", "initiative.reminder_created", "initiative.deleted"}
	if len(events) != len(expected) {
		t.Fatalf("initiative events = %#v", events)
	}
	for index, eventType := range expected {
		if events[index].Type != eventType {
			t.Fatalf("initiative event %d = %q, want %q", index, events[index].Type, eventType)
		}
	}
}

func TestIssueBoardOrderAndSavedViewLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	issue := bootstrap.Issues[0]
	updatedIssue := requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+issue.ID, map[string]any{
		"stateId": "state_progress", "sortOrder": 14.5,
	}, http.StatusOK)
	if updatedIssue.State.ID != "state_progress" || updatedIssue.SortOrder != 14.5 {
		t.Fatalf("board move = state %q order %v", updatedIssue.State.ID, updatedIssue.SortOrder)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	persistedIssue := findIssue(t, bootstrap.Issues, issue.ID)
	if persistedIssue.State.ID != "state_progress" || persistedIssue.SortOrder != 14.5 {
		t.Fatalf("persisted board move = state %q order %v", persistedIssue.State.ID, persistedIssue.SortOrder)
	}

	created := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{
		"name": "Board triage", "description": "Urgent work", "resource": "projects", "scope": "team", "teamId": "team_test", "view": "active",
		"icon": "Rocket", "color": "#26b5ce",
		"filters":  []any{map[string]any{"field": "priority", "values": []any{map[string]any{"id": "1", "label": "Urgent"}}}},
		"display":  map[string]any{"layout": "board", "grouping": "status", "properties": []string{"id", "priority"}},
		"insights": map[string]any{"measure": "issueCount", "slice": "status", "segment": "priority"},
	}, http.StatusCreated)
	if created.ID == "" || created.Resource != "projects" || created.Scope != "team" || created.View != "active" || created.Icon != "Rocket" || created.Color != "#26b5ce" || string(created.Filters) == "[]" || string(created.Display) == "{}" || string(created.Insights) == "{}" {
		t.Fatalf("saved view create = %#v", created)
	}
	initiativeView := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{
		"name": "Initiative roadmap", "resource": "initiativeProjects", "scope": "workspace", "view": "all",
		"filters": []any{map[string]any{"field": "initiative", "operator": "is", "values": []string{"initiative_1"}}},
		"display": map[string]any{"zoom": "Quarter", "query": "mobile", "properties": map[string]bool{"health": true, "priority": false, "lead": true}},
	}, http.StatusCreated)
	if initiativeView.Resource != "initiativeProjects" || string(initiativeView.Filters) == "[]" || string(initiativeView.Display) == "{}" {
		t.Fatalf("initiative saved view create = %#v", initiativeView)
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/views/"+initiativeView.ID, nil, http.StatusNoContent)
	updatedView := requestJSON[domain.SavedView](t, handler, http.MethodPatch, "/api/views/"+created.ID, map[string]any{
		"name": "Board triage updated", "description": "Current urgent work", "scope": "personal", "teamId": "", "ownerId": "usr_member", "favorite": true, "subscribed": true,
		"icon": "Face", "color": "#eb5757", "insights": map[string]any{"measure": "issueCount", "slice": "project", "segment": "none"},
	}, http.StatusOK)
	if updatedView.Name != "Board triage updated" || updatedView.Description != "Current urgent work" || updatedView.Icon != "Face" || updatedView.Color != "#eb5757" || updatedView.Scope != "personal" || updatedView.TeamID != "" || updatedView.OwnerID != "usr_member" || !updatedView.Favorite || !updatedView.Subscribed || string(updatedView.Display) != string(created.Display) || string(updatedView.Insights) == string(created.Insights) {
		t.Fatalf("saved view update = %#v", updatedView)
	}
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.SavedViews) != 1 || bootstrap.SavedViews[0].Name != updatedView.Name {
		t.Fatalf("saved view bootstrap = %#v", bootstrap.SavedViews)
	}
	requestJSON[domain.Favorite](t, handler, http.MethodPut, "/api/favorites/view/"+created.ID, nil, http.StatusOK)
	requestJSON[domain.Subscription](t, handler, http.MethodPut, "/api/subscriptions/view/"+created.ID, nil, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/views/"+created.ID, nil, http.StatusNoContent)
	bootstrap = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.SavedViews) != 0 {
		t.Fatalf("deleted view remains = %#v", bootstrap.SavedViews)
	}
	if slices.ContainsFunc(bootstrap.Favorites, func(item domain.Favorite) bool { return item.ResourceType == "view" && item.ResourceID == created.ID }) {
		t.Fatalf("deleted view favorite remains = %#v", bootstrap.Favorites)
	}
	if slices.ContainsFunc(bootstrap.Subscriptions, func(item domain.Subscription) bool {
		return item.ResourceType == "view" && item.ResourceID == created.ID
	}) {
		t.Fatalf("deleted view subscription remains = %#v", bootstrap.Subscriptions)
	}
	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+created.ID, nil, http.StatusOK)
	if len(events) != 5 || events[0].Type != "view.created" || events[1].Type != "view.updated" || events[2].Type != "favorite.added" || events[3].Type != "subscription.added" || events[4].Type != "view.deleted" {
		t.Fatalf("saved view events = %#v", events)
	}
}

func TestSavedViewShareLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	name := "Shared roadmap"
	view := requestJSON[domain.SavedView](t, handler, http.MethodPost, "/api/views", map[string]any{"name": name, "resource": "projects", "scope": "workspace"}, http.StatusCreated)
	shared := requestJSON[map[string]any](t, handler, http.MethodPost, "/api/views/"+view.ID+"/share", nil, http.StatusOK)
	token, _ := shared["token"].(string)
	if token == "" {
		t.Fatal("share token missing")
	}
	public := requestJSON[map[string]any](t, handler, http.MethodGet, "/api/shared/views/"+url.PathEscape(token)+"?workspace=test-workspace", nil, http.StatusOK)
	publicView, _ := public["view"].(map[string]any)
	if publicView["id"] != view.ID {
		t.Fatalf("public view id = %#v", publicView["id"])
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/views/"+view.ID+"/share", nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodGet, "/api/shared/views/"+url.PathEscape(token)+"?workspace=test-workspace", nil, http.StatusNotFound)
}

func TestInboxNotificationLifecycle(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})

	bootstrap := requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if len(bootstrap.Notifications) == 0 {
		t.Fatal("bootstrap did not project inbox notifications")
	}
	notification := bootstrap.Notifications[0]
	if notification.ID == "" || notification.IssueID == "" || notification.SourceID == "" || notification.SourceType == "" {
		t.Fatalf("notification source mapping incomplete: %#v", notification)
	}

	listed := requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if len(listed.Notifications) != len(bootstrap.Notifications) || listed.UnreadCount != len(bootstrap.Notifications) {
		t.Fatalf("default inbox list = %#v", listed)
	}

	updated := requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"read": true}, http.StatusOK)
	if updated.ReadAt == nil {
		t.Fatalf("notification was not marked read: %#v", updated)
	}
	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"favorite": true}, http.StatusOK)
	if !updated.Favorite || updated.FavoritedAt == nil {
		t.Fatalf("notification was not favorited: %#v", updated)
	}

	snoozedUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"snoozedUntil": snoozedUntil}, http.StatusOK)
	if updated.SnoozedUntil == nil {
		t.Fatalf("notification was not snoozed: %#v", updated)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("snoozed notification remained in default list: %#v", listed)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications?includeSnoozed=true&read=true", nil, http.StatusOK)
	if !notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("snoozed notification missing with includeSnoozed: %#v", listed)
	}
	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"snoozedUntil": nil}, http.StatusOK)
	if updated.SnoozedUntil != nil {
		t.Fatalf("notification snooze was not cleared: %#v", updated)
	}

	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"archived": true}, http.StatusOK)
	if updated.ArchivedAt == nil {
		t.Fatalf("notification was not archived: %#v", updated)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications", nil, http.StatusOK)
	if notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("archived notification remained in default list: %#v", listed)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications?includeArchived=true", nil, http.StatusOK)
	if !notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("archived notification missing with includeArchived: %#v", listed)
	}

	updated = requestJSON[domain.Notification](t, handler, http.MethodPatch, "/api/notifications/"+notification.ID, map[string]any{"deleted": true}, http.StatusOK)
	if updated.DeletedAt == nil {
		t.Fatalf("notification was not deleted: %#v", updated)
	}
	listed = requestJSON[domain.NotificationList](t, handler, http.MethodGet, "/api/notifications?includeArchived=true&includeDeleted=true", nil, http.StatusOK)
	if !notificationInList(listed.Notifications, notification.ID) {
		t.Fatalf("deleted notification missing with includeDeleted: %#v", listed)
	}

	events := requestJSON[[]domain.DomainEvent](t, handler, http.MethodGet, "/api/events?aggregateId="+notification.ID, nil, http.StatusOK)
	if len(events) != 6 || events[0].Type != "notification.read" || events[1].Type != "notification.favorited" || events[2].Type != "notification.snoozed" || events[3].Type != "notification.unsnoozed" || events[4].Type != "notification.archived" || events[5].Type != "notification.deleted" {
		t.Fatalf("notification events = %#v", events)
	}
}

func requestJSON[T any](t *testing.T, handler http.Handler, method, path string, input any, wantStatus int) T {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, body)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != wantStatus {
		t.Fatalf("%s %s status %d, want %d: %s", method, path, recorder.Code, wantStatus, recorder.Body.String())
	}
	var result T
	if recorder.Code != http.StatusNoContent {
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func upload(t *testing.T, handler http.Handler, issueID, name string, contents []byte) domain.Attachment {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/issues/"+issueID+"/attachments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status %d: %s", recorder.Code, recorder.Body.String())
	}
	var attachment domain.Attachment
	if err := json.Unmarshal(recorder.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	return attachment
}

func findIssue(t *testing.T, issues []domain.Issue, id string) domain.Issue {
	t.Helper()
	for _, issue := range issues {
		if issue.ID == id {
			return issue
		}
	}
	t.Fatalf("issue %s not found", id)
	return domain.Issue{}
}

func notificationInList(notifications []domain.Notification, id string) bool {
	for _, notification := range notifications {
		if notification.ID == id {
			return true
		}
	}
	return false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
