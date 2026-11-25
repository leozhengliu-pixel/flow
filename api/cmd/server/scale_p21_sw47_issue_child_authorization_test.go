package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW47IssueHeader struct {
	ID, Identifier, Creator, Team, Parent, Role string
	Private                                     bool
	CommentActions                              bool
	Previous, Permissions                       []string
	Relations                                   []domain.IssueRelation
}

type scaleP21SW47Outcome struct {
	Allowed bool
	Path    string
	Headers []scaleP21SW47IssueHeader
	Comment [2]string
}

func scaleP21SW47Headers(service *server, data domain.Bootstrap) []scaleP21SW47IssueHeader {
	var result []scaleP21SW47IssueHeader
	for _, issue := range data.Issues {
		item := scaleP21SW47IssueHeader{
			ID: issue.ID, Identifier: issue.Identifier, Creator: issue.Creator.ID,
			Team: issue.Team.ID, Private: issue.Team.Private, Role: issueRole(service, data, issue),
			Previous:       slices.Clone(issue.PreviousIdentifiers),
			CommentActions: service.commentActionsAllowed(data, issue),
		}
		for _, relation := range issue.Relations {
			item.Relations = append(item.Relations, domain.IssueRelation{ID: relation.ID, IssueID: relation.IssueID, RelatedIssueID: relation.RelatedIssueID})
		}
		if issue.ParentID != nil {
			item.Parent = *issue.ParentID
		}
		for _, permission := range issue.Permissions {
			item.Permissions = append(item.Permissions, strings.Join([]string{permission.ID, permission.SubjectType, permission.SubjectID, permission.Role}, "\n"))
		}
		result = append(result, item)
	}
	return result
}

func scaleP21SW47Request(f scaleP21M2Fixture, user domain.User, key *domain.APIKey, method, path, ref, comment string, body any) *http.Request {
	r := f.literalRequest(user, key, body)
	r.Method, r.URL.Path = method, path
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", ref)
	if comment != "" {
		r.SetPathValue("commentId", comment)
	}
	if strings.Contains(path, "/relations/") {
		r.SetPathValue("relationId", "sw47-hidden-relation")
	}
	return r
}

func scaleP21SW47Call(t *testing.T, source *store.SQLiteStore, request *http.Request, workspace string, user domain.User, commentID string) (scaleP21SW47Outcome, func()) {
	t.Helper()
	scoped, closeScope := beginRequestResourceReads(request)
	*request = *scoped
	closeOutput := func() {
		closeScope()
		closeScope()
	}
	t.Cleanup(closeOutput)
	service := &server{store: source, agent: appconfig.AgentConfig{Enabled: true, ToolsEnabled: true, WriteTools: true}}
	statements := 0
	*request = *request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
	service.resolveIssuePathReference(request)
	if _, resolved, _, ok := issuePathReference(request.URL.Path); ok {
		request.SetPathValue("id", resolved)
	}
	result := scaleP21SW47Outcome{Allowed: service.resourceAllowedLegacy(request, workspace, user.ID), Path: request.URL.Path}
	cached, found := request.Context().Value(issueRecordAuthorizationKey{}).(*issueRecordAuthorization)
	if !found {
		if result.Allowed {
			t.Fatal("allowed child request did not leave an authorization handoff")
		}
		return result, closeOutput
	}
	if cached.taken.Load() {
		t.Fatal("middleware consumed the child handler's one-shot handoff")
	}
	beforeTake := statements
	data, err := service.issueRecordAuthorizationData(request)
	if err != nil || statements != beforeTake || !cached.taken.Load() || !reflect.DeepEqual(cached.data, domain.Bootstrap{}) {
		t.Fatal("child handoff reloaded, failed or remained shareable", err, beforeTake, statements)
	}
	result.Headers = scaleP21SW47Headers(service, data)
	if commentID != "" {
		for _, comment := range data.Comments[request.PathValue("id")] {
			if comment.ID == commentID {
				result.Comment = [2]string{comment.ID, comment.User.ID}
			}
		}
	}
	if source.BoundedMetadataEnabled() {
		if source.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("consuming child handoff released its still-used projection")
		}
		for _, issue := range data.Issues {
			if issue.Description != "" || issue.DocumentContent != nil {
				t.Fatal("authorization loaded a complete issue body")
			}
		}
		for _, comments := range data.Comments {
			for _, comment := range comments {
				if comment.Body != "" || comment.BodyData != nil {
					t.Fatal("authorization loaded a complete comment body")
				}
			}
		}
	}
	// A second consumer gets a fresh selection, rather than sharing the
	// previously handed mutable Bootstrap with its first consumer.
	callbackCalled := false
	service.issueRecordAlias(func(w http.ResponseWriter, callbackRequest *http.Request) {
		callbackCalled = true
		var again domain.Bootstrap
		if source.BoundedMetadataEnabled() {
			read, marked := callbackRequest.Context().Value(boundedIssueWriteAuthorizationContext{}).(*store.IssueWriteAuthorizationRead)
			if !marked || read == nil {
				t.Fatal("bounded child callback lost its authorization marker")
			}
			if callbackRequest.Context().Value(issueRecordAuthorizationContext{}) != nil {
				t.Fatal("bounded child callback advertised header data as a complete snapshot")
			}
			if ctx, err := read.Context(callbackRequest.Context()); err != nil || ctx == nil {
				t.Fatal("bounded child callback lost its open generation context", err)
			}
			again = read.Data
		} else {
			var complete bool
			again, complete = callbackRequest.Context().Value(issueRecordAuthorizationContext{}).(domain.Bootstrap)
			if !complete {
				t.Fatal("legacy child callback lost its complete snapshot context")
			}
		}
		if statements == beforeTake || !reflect.DeepEqual(result.Headers, scaleP21SW47Headers(service, again)) {
			t.Fatal("second child consumer reused or changed the first handoff")
		}
		w.WriteHeader(http.StatusNoContent)
	})(httptest.NewRecorder(), request)
	if !callbackCalled {
		t.Fatal("issue record alias did not invoke its child callback")
	}
	if request.Header.Get("X-SW47-Measure") != "" {
		ancestors, relations := 0, 0
		for _, issue := range data.Issues {
			if issue.ID != request.PathValue("id") {
				ancestors++
			}
			relations += len(issue.Relations)
		}
		t.Logf("child_comment bounded=%t auth_SQL=%d handoff_SQL=0 rebuild_SQL=%d requested_refs=1 issue_headers=%d ancestor_headers=%d relation_refs=%d team_headers=%d team_settings=%d memberships=%d",
			source.BoundedMetadataEnabled(), beforeTake, statements-beforeTake, len(data.Issues), ancestors, relations, len(data.Teams), len(data.TeamSettings), len(data.TeamMembers))
	}
	return result, closeOutput
}

func TestScaleP21SW47IssueChildAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		metadata, found := repo.WorkspaceMetadata(workspace)
		if !found {
			t.Fatal("owned fixture missing")
		}
		privateTeam := ""
		for _, project := range metadata.Projects {
			if project.ID == "m2-unrelated" {
				privateTeam = project.TeamIDs[0]
			}
		}
		privateAdmin := authClient(t)
		authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login", map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
		public := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "SW47 public", "teamId": f.public}, workspace, http.StatusCreated)
		parent := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "SW47 private parent", "teamId": privateTeam}, workspace, http.StatusCreated)
		child := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "SW47 inherited child", "teamId": privateTeam, "parentId": parent.ID}, workspace, http.StatusCreated)
		guest := f.principals[3].user
		setGrant := func(role string) {
			t.Helper()
			permissions := []map[string]string{}
			if role != "" {
				permissions = append(permissions, map[string]string{"subjectType": "user", "subjectId": guest.ID, "role": role})
			}
			authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+parent.ID+"/permissions", map[string]any{"permissions": permissions}, workspace, http.StatusOK)
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
		}
		setGrant("commenter")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "issues", "comments"), workspace, "scale_p21.sw47_seed", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Issues {
				issue := &data.Issues[index]
				if issue.ID == public.ID {
					issue.PreviousIdentifiers = []string{"SWOLD-47001"}
					issue.Creator = f.principals[2].user
					issue.Relations = []domain.IssueRelation{{ID: "sw47-hidden-relation", IssueID: public.ID, RelatedIssueID: parent.ID, Type: "related"}}
				}
				if issue.ID == public.ID || issue.ID == child.ID {
					issue.Description = strings.Repeat("ignored issue authorization body ", 10000)
				}
			}
			data.Comments[child.ID] = []domain.Comment{{ID: "sw47-comment", User: guest, Body: strings.Repeat("ignored comment authorization body ", 10000), BodyData: map[string]any{"ignored": true}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			name, method, ref, tail, comment string
			body                             any
		}{
			{"public-comment", http.MethodPost, public.ID, "/comments", "", map[string]any{"body": "comment"}},
			{"inherited-comment", http.MethodPost, child.ID, "/comments", "", map[string]any{"body": "comment"}},
			{"comment-author", http.MethodPatch, child.ID, "/comments/sw47-comment", "sw47-comment", map[string]any{"body": "edited"}},
			{"missing-comment", http.MethodDelete, child.ID, "/comments/missing", "missing", nil},
			{"comment-reaction", http.MethodPost, child.ID, "/comments/sw47-comment/reactions", "sw47-comment", map[string]any{"emoji": "like"}},
			{"issue-reaction", http.MethodPost, public.ID, "/reactions", "", map[string]any{"emoji": "like"}},
			{"attachment", http.MethodPost, public.ID, "/attachments", "", map[string]any{"name": "attachment"}},
			{"link", http.MethodPost, public.ID, "/links", "", map[string]any{"url": "https://example.test/link"}},
			{"hidden-relation-target", http.MethodPost, public.ID, "/relations", "", map[string]any{"relatedIssueId": parent.ID}},
			{"relation-identifier-body", http.MethodPost, public.ID, "/relations", "", map[string]any{"relatedIssueId": public.Identifier}},
			{"delete-relation", http.MethodDelete, public.ID, "/relations/sw47-hidden-relation", "", nil},
			{"old-identifier", http.MethodPost, "swold-47001", "/comments", "", map[string]any{"body": "alias comment"}},
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					for _, test := range tests {
						path := "/api/issue-records/" + test.ref + test.tail
						oldRequest := scaleP21SW47Request(f, user, key, test.method, path, test.ref, test.comment, test.body)
						newRequest := scaleP21SW47Request(f, user, key, test.method, path, test.ref, test.comment, test.body)
						if !reload && user.ID == f.data.Viewer.ID && key == nil && test.name == "public-comment" {
							oldRequest.Header.Set("X-SW47-Measure", "owner-comment")
							newRequest.Header.Set("X-SW47-Measure", "owner-comment")
						}
						old, closeOld := scaleP21SW47Call(t, repo, oldRequest, workspace, user, test.comment)
						bounded, closeNew := scaleP21SW47Call(t, f.probe, newRequest, workspace, user, test.comment)
						if !reflect.DeepEqual(old, bounded) {
							t.Fatalf("%s child decision/authorization handoff differs: old=%+v bounded=%+v", test.name, old, bounded)
						}
						closeOld()
						closeNew()
						if f.probe.MetadataWorkUsedForTesting() != 0 {
							t.Fatal("child authorization escaped its request output owner")
						}
					}
				})
			})
		}
		// Requests constructed while the grant existed must resolve authority
		// when dequeued. A replay uses a new request and cannot reuse a prior
		// operation's one-shot authorization data.
		path := "/api/issue-records/" + child.ID + "/comments"
		queued := []*http.Request{
			scaleP21SW47Request(f, guest, nil, http.MethodPost, path, child.ID, "", map[string]any{"body": "queued"}),
			scaleP21SW47Request(f, guest, nil, http.MethodPost, path, child.ID, "", map[string]any{"body": "queued"}),
		}
		setGrant("")
		for index, source := range []*store.SQLiteStore{repo, f.probe} {
			denied, closeDenied := scaleP21SW47Call(t, source, queued[index], workspace, guest, "")
			if denied.Allowed {
				t.Fatal("queued/replayed child request kept its removed ancestor grant")
			}
			replay, closeReplay := scaleP21SW47Call(t, source, scaleP21SW47Request(f, guest, nil, http.MethodPost, path, child.ID, "", map[string]any{"body": "queued"}), workspace, guest, "")
			if !reflect.DeepEqual(denied, replay) {
				t.Fatal("new replay request reused an earlier authorization")
			}
			closeDenied()
			closeReplay()
		}
		setGrant("commenter")
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			allowed, closeAllowed := scaleP21SW47Call(t, source, scaleP21SW47Request(f, guest, nil, http.MethodPost, path, child.ID, "", map[string]any{"body": "queued"}), workspace, guest, "")
			if !allowed.Allowed {
				t.Fatal("restored inherited commenter grant was not observed")
			}
			closeAllowed()
		}
		// Closing a request that never hands its data to the child must also
		// drop the cached projection. Cancellation cannot turn it into success.
		request := scaleP21SW47Request(f, guest, nil, http.MethodPost, path, child.ID, "", map[string]any{"body": "owned"})
		request, closeScope := beginRequestResourceReads(request)
		service := &server{store: f.probe}
		if !service.resourceAllowedLegacy(request, workspace, guest.ID) || f.probe.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("unconsumed child projection lacked an owned scope")
		}
		cached := request.Context().Value(issueRecordAuthorizationKey{}).(*issueRecordAuthorization)
		closeScope()
		if f.probe.MetadataWorkUsedForTesting() != 0 || !cached.taken.Load() || !reflect.DeepEqual(cached.data, domain.Bootstrap{}) {
			t.Fatal("unconsumed child handoff survived its closed request")
		}
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		request = scaleP21SW47Request(f, guest, nil, http.MethodPost, path, child.ID, "", map[string]any{"body": "canceled"}).WithContext(canceled)
		request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, guest))
		request, closeScope = beginRequestResourceReads(request)
		if service.resourceAllowedLegacy(request, workspace, guest.ID) {
			t.Fatal("canceled child authorization became success")
		}
		closeScope()
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("child authorization leaked work or decoded complete metadata")
		}
		// Authorization peeks must leave the exact request body available to
		// the pending child callback; no callback writer is invoked here.
		body := []byte(`{"body":"still available","parentId":"sw47-comment"}`)
		request = f.literalRequest(guest, nil, nil)
		request.Method, request.URL.Path = http.MethodPost, path
		request.Header.Set("Content-Type", "application/json")
		request.SetPathValue("id", child.ID)
		request.Body = io.NopCloser(bytes.NewReader(body))
		_, closeBody := scaleP21SW47Call(t, f.probe, request, workspace, guest, "")
		after, err := io.ReadAll(request.Body)
		if err != nil || !json.Valid(after) || !bytes.Equal(body, after) {
			t.Fatal("authorization consumed or rewrote the callback body", err)
		}
		closeBody()
	}
}
