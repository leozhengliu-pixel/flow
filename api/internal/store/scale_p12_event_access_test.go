package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP12IssueScopeGrantInheritanceAndCycles(t *testing.T) {
	source := &RealtimeIssueSource{workspace: "workspace", scopes: map[string]realtimeIssueScope{
		"root":    {team: "private", grants: []domain.IssuePermission{{SubjectType: "user", SubjectID: "guest", Role: "viewer"}, {SubjectType: "team", SubjectID: "shared", Role: "viewer"}}},
		"child":   {team: "private-child", parent: "root"},
		"cycle-a": {team: "private", parent: "cycle-b"}, "cycle-b": {team: "private", parent: "cycle-a"},
	}}
	for _, test := range []struct {
		name, id, user           string
		teams, allowed, archived []string
		admin, want              bool
	}{
		{name: "guest-inherited-grant", id: "child", user: "guest", want: true},
		{name: "team-grant", id: "child", user: "member", teams: []string{"shared"}, want: true},
		{name: "private-outsider", id: "child", user: "outside"},
		{name: "admin-outside-private", id: "child", user: "admin"},
		{name: "admin-content", id: "child", user: "admin", admin: true, want: true},
		{name: "selected-empty", id: "child", user: "guest", allowed: []string{}},
		{name: "selected-set", id: "child", user: "guest", allowed: []string{"private-child"}, want: true},
		{name: "archived-team", id: "child", user: "guest", archived: []string{"private-child"}},
		{name: "missing-record", id: "missing", user: "guest"},
		{name: "cyclic-ancestors", id: "cycle-a", user: "guest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := IssueRecordQuery{Workspace: "workspace", AllowedTeamIDs: test.allowed, Access: &IssueRecordAccess{UserID: test.user, WorkspaceID: "workspace-id", Admin: test.admin, VisibleTeamIDs: test.teams, ArchivedTeamIDs: test.archived}}
			if got := source.Visible(query, test.id); got != test.want {
				t.Fatalf("visible=%v want=%v", got, test.want)
			}
			query.Workspace = "foreign"
			if source.Visible(query, test.id) {
				t.Fatal("scope crossed workspace")
			}
		})
	}
}

func TestScaleP12SourceReferencesDoNotFetchProjectBodies(t *testing.T) {
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		workspace := fixture.data.Workspace.URLKey
		body := strings.Repeat("body", 1<<19)
		if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.reference", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = append(data.Projects, domain.Project{ID: "p12-reference", Name: "Referenced", Icon: "icon", Color: "color", Description: body})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		statements := []string{}
		ctx := WithSQLStatementObserver(t.Context(), func(query string) { statements = append(statements, query) })
		issue, err := s.resolveRealtimeIssueReferences(ctx, workspace, domain.Issue{ID: "issue", Project: &domain.ProjectSummary{ID: "p12-reference"}})
		if err != nil {
			t.Fatal(err)
		}
		if issue.Project == nil || issue.Project.Name != "Referenced" {
			t.Fatal("display reference was not resolved")
		}
		if len(statements) != 1 || !strings.Contains(statements[0], "CASE WHEN field='projects'") {
			t.Fatalf("reference SQL did not project its fields: %q", statements)
		}
		encoded, err := json.Marshal(issue)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > 4096 || strings.Contains(string(encoded), body[:100]) {
			t.Fatal("project body entered an issue reference")
		}
	}
}

func TestScaleP12CommentsNeverReturnAPartialReplacement(t *testing.T) {
	for _, fixture := range scaleP03Stores(t) {
		s := fixture.SQLiteStore
		workspace := fixture.data.Workspace.URLKey
		if err := s.MutateWorkspace(t.Context(), workspace, "scale_p12.comments", "document", nil, func(data *domain.Bootstrap) error {
			if data.Comments == nil {
				data.Comments = map[string][]domain.Comment{}
			}
			data.Comments["document"] = []domain.Comment{{ID: "a-small", Body: "Small", User: fixture.data.Viewer}, {ID: "z-large", Body: strings.Repeat("x", 300<<10), User: fixture.data.Viewer}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		comments, _, err := s.ReadRealtimeDocumentComments(t.Context(), workspace, "document")
		if !errors.Is(err, ErrEventSourceBudget) || comments != nil {
			t.Fatalf("overflow returned a partial comment list: count=%d err=%v", len(comments), err)
		}
	}
}

func TestScaleP12DocumentMarkersDecodeAndExpansionIsBounded(t *testing.T) {
	raw := json.RawMessage(`{"id":"document","content":"Current body","contentState":"state","revisions":[{"id":"revision","_offloaded":{"body":"document"}}]}`)
	document, err := DecodeRealtimeDocument(raw)
	if err != nil || len(document.Revisions) != 1 || document.Revisions[0].Content != "Current body" || document.Revisions[0].ContentState != "state" || len(document.Revisions[0].Offloaded) != 0 {
		t.Fatalf("current revision marker did not use the installed decoder: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["content"] = strings.Repeat("x", 64<<10)
	revisions := []map[string]any{}
	for range 50 {
		revisions = append(revisions, map[string]any{"id": "revision", "_offloaded": map[string]string{"body": "document"}})
	}
	fields["revisions"] = revisions
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRealtimeDocument(raw); !errors.Is(err, ErrEventSourceBudget) {
		t.Fatalf("marker expansion escaped its budget: %v", err)
	}
}
