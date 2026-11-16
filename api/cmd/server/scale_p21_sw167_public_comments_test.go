package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW167PublicCommentsHTTPDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw167-")
	at := time.Date(2025, 4, 5, 6, 7, 8, 0, time.UTC)
	editedAt := at.Add(2 * time.Minute)
	project, initiative, document, privateDocument := prefix+"project", prefix+"initiative", prefix+"document", prefix+"private-document"
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "SW167 public comments", "teamId": f.public}, workspace, http.StatusCreated).ID
	private, privateIssue := "", ""
	comments := func(kind string) []domain.Comment {
		return []domain.Comment{
			{ID: prefix + kind + "-first", Body: "First <&> comment", User: f.data.Viewer, CreatedAt: at},
			{ID: prefix + kind + "-second", Body: "Second comment", User: f.data.Viewer, CreatedAt: at.Add(time.Minute), EditedAt: &editedAt, Resolved: true},
			{ID: prefix + kind + "-third", Body: "Third comment", User: f.data.Viewer, CreatedAt: at.Add(2 * time.Minute)},
		}
	}
	restore := store.ForceFullMutationsForTesting(true)
	err = repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw167_seed", "", nil, func(data *domain.Bootstrap) error {
		for _, team := range data.Teams {
			if strings.HasPrefix(team.ID, "m2-private-") {
				private = team.ID
			}
		}
		for _, item := range data.Issues {
			if item.Team.ID == private {
				privateIssue = item.ID
			}
		}
		data.Projects = append(data.Projects, domain.Project{
			ID: project, SlugID: prefix + "project-current", PreviousSlugIDs: []string{prefix + "project-old"},
			Name: "Comment project", TeamIDs: []string{f.public}, Comments: comments("project"),
		})
		data.Initiatives = append(data.Initiatives, domain.Initiative{
			ID: initiative, SlugID: prefix + "initiative-current", PreviousSlugIDs: []string{prefix + "initiative-old"},
			Name: "Comment initiative", ProjectIDs: []string{project}, Creator: data.Viewer, Comments: comments("initiative"),
		})
		data.Documents = append(data.Documents,
			domain.Document{ID: document, SlugID: prefix + "document-current", PreviousSlugIDs: []string{prefix + "document-old", prefix + "document-precedence"}, Title: "Comment document", Content: strings.Repeat("Unread document body ", 20000), Creator: data.Viewer, TeamIDs: []string{f.public}},
			domain.Document{ID: privateDocument, SlugID: prefix + "document-precedence", Title: "Private issue-linked document", Creator: f.principals[1].user, IssueID: privateIssue, IssueTeamID: private, InheritedTeamIDs: []string{private}, Permissions: []domain.DocumentPermission{{SubjectType: "workspace", SubjectID: data.Workspace.ID, Role: "viewer"}}},
		)
		data.Comments[document] = comments("document")
		data.Comments[privateDocument] = comments("private-document")
		data.Comments[issue] = comments("issue")
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if private == "" || privateIssue == "" {
		t.Fatal("private issue fixture missing")
	}
	type requestCase struct {
		name, query, workspace string
		user                   domain.User
		key                    *domain.APIKey
		development, handed    bool
	}
	keys := []*domain.APIKey{
		nil,
		{ID: prefix + "all", Scopes: []string{"read"}},
		{ID: prefix + "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
		{ID: prefix + "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
		{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
		{ID: prefix + "app-initiative", Actor: "app", Scopes: []string{"read", "initiative:read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
		{ID: prefix + "app-initiative-empty", Actor: "app", Scopes: []string{"read", "initiative:read"}, TeamRestriction: "selected", TeamIDs: []string{}},
	}
	queries := []string{
		"projectId=" + project,
		"projectId=" + prefix + "project-old&first=1",
		"projectId=" + project + "&filter=" + url.QueryEscape(`{"resolved":{"eq":true}}`),
		"projectId=" + project + "&orderBy=updatedAt&last=1",
		"initiativeId=" + prefix + "initiative-old&first=1",
		"initiativeId=" + initiative + "&filter=" + url.QueryEscape(`{"body":{"contains":"Second"}}`),
		"documentId=" + document,
		"documentId=" + prefix + "document-old&first=1",
		"documentId=" + prefix + "document-precedence",
		"documentId=" + privateDocument,
		"documentId=" + document + "&last=1",
		"documentId=" + document + "&filter=" + url.QueryEscape(`{"body":{"eq":"x"}}`),
		"issueId=" + issue + "&first=1",
		"projectId=missing",
		"initiativeId=missing",
		"documentId=missing",
		"documentId=missing&first=0",
		"projectId=" + project + "&initiativeId=" + initiative,
		"",
	}
	cursor := (pageKey{At: at.Add(time.Minute), ID: prefix + "project-second"}).cursor("createdAt")
	queries = append(queries, "projectId="+project+"&first=1&after="+url.QueryEscape(cursor))
	documentCursor := (pageKey{At: at.Add(time.Minute), ID: prefix + "document-second"}).cursor("createdAt")
	queries = append(queries, "documentId="+document+"&last=1&before="+url.QueryEscape(documentCursor))
	pairs, legacySQL, boundedSQL := 0, 0, 0
	compare := func(state string, test requestCase) {
		t.Helper()
		serve := func(selected *store.SQLiteStore) (*scaleP21PublicResourceRecordWriter, int) {
			service := &server{store: selected, authDisabled: test.development}
			r := f.literalRequest(test.user, test.key, nil)
			r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/comments", test.query
			r.Header.Set("X-Workspace-Key", test.workspace)
			r.SetPathValue("workspaceKey", test.workspace)
			r.SetPathValue("workspace", test.workspace)
			if selected.BoundedMetadataEnabled() {
				var closeReads func()
				r, closeReads = beginRequestResourceReads(r)
				defer closeReads()
			}
			sqlCount := 0
			r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(string) { sqlCount++ }))
			if test.handed {
				var data domain.Bootstrap
				if selected.BoundedMetadataEnabled() {
					kind, ref := "projects", project
					if r.URL.Query().Get("initiativeId") != "" {
						kind, ref = "initiatives", initiative
					}
					if r.URL.Query().Get("documentId") != "" {
						read, err := selected.BeginDocumentRead(r.Context(), workspace, test.user.ID, nil, store.DocumentReadRequest{Ref: document, ReadOnly: true, ProjectionFallback: true})
						if err != nil || !registerRequestReadClose(r.Context(), read.Close) {
							t.Fatal("document handoff owner", err)
						}
						data = read.Data
					} else {
						access, err := selected.ResolveAccess(r.Context(), workspace, test.user.ID, nil)
						if err != nil {
							t.Fatal(err)
						}
						read, err := selected.BeginProjectedInstalledResourcePointRead(r.Context(), access, kind, ref)
						if err != nil || !registerRequestReadClose(r.Context(), read.Close) {
							t.Fatal("resource handoff owner", err)
						}
						data = read.Data
					}
				} else {
					var err error
					data, err = selected.PagedWorkspaceMetadata(r.Context(), workspace, test.user.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				for index := range data.Projects {
					if data.Projects[index].ID == project {
						data.Projects[index].Comments[0].Body = "Earlier handed-off project comment"
					}
				}
				for index := range data.Initiatives {
					if data.Initiatives[index].ID == initiative {
						data.Initiatives[index].Comments[0].Body = "Earlier handed-off initiative comment"
					}
				}
				for index := range data.Documents {
					if data.Documents[index].ID == document {
						data.Documents[index].SlugID = prefix + "handed-document"
					}
				}
				handOffPagedMetadata(r, workspace, test.user.ID, data)
			}
			w := &scaleP21PublicResourceRecordWriter{ResponseRecorder: httptest.NewRecorder(), repo: selected}
			service.listCommentsConnection(w, r)
			return w, sqlCount
		}
		want, beforeSQL := serve(repo)
		got, afterSQL := serve(f.probe)
		if got.Code != want.Code || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") || got.Header().Get("Retry-After") != want.Header().Get("Retry-After") || got.Body.String() != want.Body.String() {
			t.Fatalf("pair=%d state=%s case=%s query=%q key=%+v\nlegacy=%d %s\nbounded=%d %s", pairs+1, state, test.name, test.query, test.key, want.Code, want.Body.String(), got.Code, got.Body.String())
		}
		if got.Code == http.StatusOK && !strings.Contains(test.query, "issueId=") && got.held == 0 {
			t.Fatal("public comment output released before encoding")
		}
		if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("public comments retained work or hydrated all metadata")
		}
		if test.name == "admin-private-outsider" && test.key == nil {
			legacySQL, boundedSQL = legacySQL+beforeSQL, boundedSQL+afterSQL
			if test.query == "projectId="+project || test.query == "documentId="+document || test.query == "initiativeId="+prefix+"initiative-old&first=1" || test.query == "issueId="+issue+"&first=1" {
				t.Logf("state=%s query=%s SQL_legacy=%d SQL_bounded=%d", state, test.query, beforeSQL, afterSQL)
			}
		}
		// The recorder must retain complete JSON after every read owner closes.
		var retained connection[domain.Comment]
		if got.Code == http.StatusOK && json.Unmarshal(got.Body.Bytes(), &retained) != nil {
			t.Fatal("retained output is not a comment connection")
		}
		pairs++
	}
	for _, state := range []string{"warm", "stale-installed", "unknown-installed", "reloaded"} {
		switch state {
		case "stale-installed":
			peer, err := store.OpenDatabase(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
				_ = peer.Close()
				t.Fatal(err)
			}
			restore := store.ForceFullMutationsForTesting(true)
			err = peer.MutateWorkspace(t.Context(), workspace, "scale_p21.sw167_peer", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Projects {
					if data.Projects[index].ID == project {
						data.Projects[index].Comments[0].Body = "Later canonical project comment"
					}
				}
				for index := range data.Documents {
					if data.Documents[index].ID == document {
						data.Documents[index].SlugID = prefix + "later-canonical-document"
					}
				}
				return nil
			})
			restore()
			_ = peer.Close()
			if err != nil {
				t.Fatal(err)
			}
		case "unknown-installed":
			if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
				t.Fatal(err)
			}
		case "reloaded":
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		for _, principal := range f.principals {
			for _, key := range keys {
				for _, query := range queries {
					compare(state, requestCase{name: principal.name, workspace: workspace, user: principal.user, key: key, query: query})
				}
			}
		}
		for _, query := range []string{"projectId=" + project, "initiativeId=" + initiative, "documentId=" + prefix + "document-old"} {
			compare(state, requestCase{name: "development", workspace: workspace, user: f.data.Viewer, query: query, development: true})
		}
	}
	for _, query := range []string{"projectId=" + project, "initiativeId=" + initiative, "documentId=" + prefix + "handed-document"} {
		compare("handed", requestCase{name: "explicit-projection", workspace: workspace, user: f.data.Viewer, query: query, handed: true})
	}
	for _, binding := range []string{"", prefix + "missing-workspace"} {
		for _, query := range []string{"projectId=missing", "initiativeId=missing", "documentId=missing", "documentId=missing&first=0"} {
			compare("workspace-errors", requestCase{name: "missing-binding", workspace: binding, user: f.data.Viewer, query: query})
		}
	}
	t.Logf("public comments HTTP exact pairs=%d; sampled admin SQL legacy=%d bounded=%d; warm/stale/unknown/reloaded, M principals/keys, aliases, filters, pages, explicit handoffs and retained output", pairs, legacySQL, boundedSQL)
}
