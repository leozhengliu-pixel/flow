package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func tutorialTestHandler(t *testing.T, enabled bool) (http.Handler, *store.SQLiteStore) {
	t.Helper()
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true, workspaceTutorial: enabled}), repository
}

func tutorialIssues(issues []domain.Issue) []domain.Issue {
	return slices.DeleteFunc(slices.Clone(issues), func(issue domain.Issue) bool {
		return !slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return strings.HasSuffix(label.ID, "_getting_started") })
	})
}

// documentNodes collects the nodes of one type in a rich-text document.
func documentNodes(node any, kind string) []map[string]any {
	found := []map[string]any{}
	switch typed := node.(type) {
	case map[string]any:
		if typed["type"] == kind {
			found = append(found, typed)
		}
		found = append(found, documentNodes(typed["content"], kind)...)
	case []any:
		for _, child := range typed {
			found = append(found, documentNodes(child, kind)...)
		}
	}
	return found
}

func TestNewWorkspaceGetsTutorialIssuesInCreatorLanguage(t *testing.T) {
	clips := []string{}
	for _, entry := range tutorialEntries() {
		clips = append(clips, entry.clip)
		if _, err := os.Stat(filepath.Join("..", "..", "..", "docs", "media", entry.clip+".gif")); err != nil {
			t.Errorf("tutorial clip %s has no GIF in docs/media: %v", entry.clip, err)
		}
	}
	for _, test := range []struct {
		name, locale, acceptLanguage, welcome, firstTitle, label string
	}{
		{name: "english", locale: "en-US", welcome: "Welcome to Flow", firstTitle: "Work with issues in a list or on a board", label: "Getting started"},
		{name: "chinese", locale: "zh-CN", welcome: "欢迎使用 Flow", firstTitle: "在列表和看板中处理事项", label: "新手入门"},
		{name: "browser language", acceptLanguage: "zh-TW,zh;q=0.9,en;q=0.8", welcome: "欢迎使用 Flow", firstTitle: "在列表和看板中处理事项", label: "新手入门"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, repository := tutorialTestHandler(t, true)
			body, _ := json.Marshal(map[string]string{"name": "Tutorial " + test.name, "urlKey": "tutorial", "locale": test.locale})
			request := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			if test.acceptLanguage != "" {
				request.Header.Set("Accept-Language", test.acceptLanguage)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("create workspace: %d %s", response.Code, response.Body.String())
			}
			var created domain.Bootstrap
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			team, viewer := created.Teams[0], created.Viewer
			issues := tutorialIssues(created.Issues)
			if len(issues) != len(clips)+1 || len(created.Issues) != len(issues) {
				t.Fatalf("new workspace has %d tutorial issues of %d, want %d", len(issues), len(created.Issues), len(clips)+1)
			}
			labelIndex := slices.IndexFunc(created.Labels, func(label domain.IssueLabel) bool { return strings.HasSuffix(label.ID, "_getting_started") })
			if labelIndex < 0 || created.Labels[labelIndex].Name != test.label {
				t.Fatalf("tutorial label = %+v", created.Labels)
			}
			welcome := issues[0]
			if welcome.Title != test.welcome || welcome.ParentID != nil || welcome.Number != 1 || welcome.State.Type != "unstarted" {
				t.Fatalf("welcome issue = %q parent=%v number=%d state=%s", welcome.Title, welcome.ParentID, welcome.Number, welcome.State.Name)
			}
			if issues[1].Title != test.firstTitle {
				t.Fatalf("first tutorial title = %q, want %q", issues[1].Title, test.firstTitle)
			}
			if mentions := documentNodes(welcome.DocumentContent.ContentData, "mention"); len(mentions) != len(clips) {
				t.Fatalf("welcome issue mentions %d tutorial issues, want %d", len(mentions), len(clips))
			}
			for index, issue := range issues {
				if issue.Team.ID != team.ID || issue.Assignee == nil || issue.Assignee.ID != viewer.ID || issue.Creator.ID != viewer.ID || issue.Priority != 0 || !slices.Contains(issue.SubscriberIDs, viewer.ID) {
					t.Fatalf("%s: team=%s assignee=%v creator=%s priority=%d", issue.Identifier, issue.Team.ID, issue.Assignee, issue.Creator.ID, issue.Priority)
				}
				if issue.Identifier != team.Key+"-"+strconv.Itoa(index+1) {
					t.Fatalf("issue %d identifier = %s", index, issue.Identifier)
				}
				if issue.DocumentContent == nil || issue.DocumentContent.Content != issue.Description || strings.Contains(issue.Description, "{{") || strings.Contains(issue.Description, "‹") {
					t.Fatalf("%s description and document disagree or keep placeholders:\n%s", issue.Identifier, issue.Description)
				}
				if index == 0 {
					continue
				}
				clip := clips[index-1]
				src := "/tour/" + clip + ".gif"
				if issue.ParentID == nil || *issue.ParentID != welcome.ID || !slices.Contains(welcome.SubIssueIDs, issue.ID) {
					t.Fatalf("%s is not a sub-issue of the welcome issue", issue.Identifier)
				}
				if !strings.HasPrefix(issue.Description, "![") || !strings.Contains(strings.SplitN(issue.Description, "\n", 2)[0], "("+src+")") {
					t.Fatalf("%s description does not start with its clip:\n%s", issue.Identifier, issue.Description)
				}
				content, _ := issue.DocumentContent.ContentData["content"].([]any)
				first, _ := content[0].(map[string]any)
				attrs, _ := first["attrs"].(map[string]any)
				if first["type"] != "image" || attrs["src"] != src || attrs["alt"] != issue.Title {
					t.Fatalf("%s document does not start with its clip: %#v", issue.Identifier, first)
				}
				if len(documentNodes(issue.DocumentContent.ContentData, "orderedList")) != 1 {
					t.Fatalf("%s has no list of steps", issue.Identifier)
				}
				mentions := documentNodes(issue.DocumentContent.ContentData, "mention")
				if len(mentions) < 2 {
					t.Fatalf("%s links %d related tutorial issues", issue.Identifier, len(mentions))
				}
				for _, mention := range mentions {
					attrs, _ := mention["attrs"].(map[string]any)
					if attrs["mentionType"] != "issue" || !slices.ContainsFunc(issues, func(other domain.Issue) bool { return other.ID == attrs["id"] && other.ID != issue.ID }) {
						t.Fatalf("%s mentions %#v, not another tutorial issue", issue.Identifier, attrs)
					}
				}
				wantState := "backlog"
				if index <= 5 {
					wantState = "unstarted" // core workflow
				}
				if issue.State.Type != wantState {
					t.Fatalf("%s state = %s, want %s", issue.Identifier, issue.State.Type, wantState)
				}
			}

			// The issues were persisted with the workspace, and new issues are
			// numbered after them.
			stored, err := repository.IssueRecord(t.Context(), created.Workspace.URLKey, issues[3].ID)
			if err != nil || stored.Title != issues[3].Title || stored.ParentID == nil || stored.DocumentContent == nil || stored.Description != issues[3].Description {
				t.Fatalf("stored tutorial issue = %+v, %v", stored, err)
			}
			_, activities, err := repository.IssueContent(t.Context(), created.Workspace.URLKey, issues[3].ID)
			if err != nil || len(activities) != 1 || activities[0].Type != "issue.created" {
				t.Fatalf("tutorial issue activity = %+v, %v", activities, err)
			}
			raw, _ := json.Marshal(map[string]string{"title": "First real issue", "teamId": team.ID})
			next := httptest.NewRequest(http.MethodPost, "/api/issues", bytes.NewReader(raw))
			next.Header.Set("Content-Type", "application/json")
			next.Header.Set("X-Workspace-Key", created.Workspace.URLKey)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, next)
			var issue domain.Issue
			if recorder.Code != http.StatusCreated || json.Unmarshal(recorder.Body.Bytes(), &issue) != nil || issue.Number != len(issues)+1 {
				t.Fatalf("next issue: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestTutorialIssuesOnlySeedNewAppWorkspaces(t *testing.T) {
	t.Run("existing workspaces are untouched", func(t *testing.T) {
		handler, repository := tutorialTestHandler(t, true)
		before, _ := repository.BootstrapFor("test-workspace")
		created := requestJSON[domain.Bootstrap](t, handler, http.MethodPost, "/api/workspaces", map[string]string{"name": "Fresh", "urlKey": "fresh"}, http.StatusCreated)
		if len(tutorialIssues(created.Issues)) == 0 {
			t.Fatal("new workspace has no tutorial issues")
		}
		after, _ := repository.BootstrapFor("test-workspace")
		if len(after.Labels) != len(before.Labels) {
			t.Fatalf("existing workspace gained labels: %d before, %d after", len(before.Labels), len(after.Labels))
		}
		// The fixture holds issues 1, 2, 33, … but no issue 3.
		if _, err := repository.IssueRecord(t.Context(), "test-workspace", "issue_3"); err == nil {
			t.Fatal("tutorial issue landed in the existing workspace")
		}
	})
	for _, test := range []struct {
		name    string
		enabled bool
		input   map[string]any
	}{
		{name: "FLOW_WORKSPACE_TUTORIAL=off", enabled: false, input: map[string]any{"name": "Off", "urlKey": "off"}},
		{name: "workspace created to import into", enabled: true, input: map[string]any{"name": "Import", "urlKey": "import", "tutorial": false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, repository := tutorialTestHandler(t, test.enabled)
			created := requestJSON[domain.Bootstrap](t, handler, http.MethodPost, "/api/workspaces", test.input, http.StatusCreated)
			if len(created.Issues) != 0 || slices.ContainsFunc(created.Labels, func(label domain.IssueLabel) bool { return strings.HasSuffix(label.ID, "_getting_started") }) {
				t.Fatalf("workspace got tutorial content: %d issues, labels %+v", len(created.Issues), created.Labels)
			}
			if _, err := repository.IssueRecord(t.Context(), created.Workspace.URLKey, "issue_1"); err == nil {
				t.Fatal("tutorial issue was stored")
			}
		})
	}
	t.Run("store-created workspaces", func(t *testing.T) {
		_, repository := tutorialTestHandler(t, true)
		created, err := repository.CreateWorkspace(t.Context(), "Direct", "direct", "us")
		if err != nil || len(created.Issues) != 0 {
			t.Fatalf("store workspace issues = %d, %v", len(created.Issues), err)
		}
	})
}

func TestTutorialLocale(t *testing.T) {
	for _, test := range []struct{ requested, accept, want string }{
		{"", "", "en-US"},
		{"zh-CN", "en-US", "zh-CN"},
		{"en-US", "zh-CN", "en-US"},
		{"", "zh-Hans-CN,zh;q=0.9", "zh-CN"},
		{"", "*, zh;q=0.5", "zh-CN"},
		{"", "fr-FR,zh;q=0.5", "en-US"},
	} {
		if got := tutorialLocale(test.requested, test.accept); got != test.want {
			t.Errorf("tutorialLocale(%q, %q) = %q, want %q", test.requested, test.accept, got, test.want)
		}
	}
}
