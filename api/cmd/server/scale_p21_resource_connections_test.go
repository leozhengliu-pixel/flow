package main

import (
	"bytes"
	"context"
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

func resourceConnectionHandler(service *server, kind string) http.HandlerFunc {
	switch kind {
	case "projects":
		return service.listProjectsConnection
	case "projectMilestones":
		return service.listProjectMilestonesConnection
	case "initiatives":
		return service.listInitiativesConnection
	case "labels":
		return service.listIssueLabelsConnection
	default:
		return service.listDocumentsConnection
	}
}

func TestScaleP21ResourceConnectionsHTTPTable(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("resource-connection-")
		at := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
		private := ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.resource_connections", "", nil, func(data *domain.Bootstrap) error {
			for _, team := range data.Teams {
				if strings.HasPrefix(team.ID, "m2-private-") {
					private = team.ID
				}
			}
			actor := data.Viewer
			actor.Name, actor.DisplayName, actor.AppTeamIDs = "Stored Actor", "Stored Display", []string{f.public, private}
			data.LabelGroups = []domain.LabelGroup{{ID: prefix + "group", Scope: f.public}, {ID: prefix + "hidden-group", Scope: private}}
			data.Labels = []domain.IssueLabel{
				{ID: prefix + "label", Name: "First label", Description: "Full label description", Color: "#123456", Scope: f.public, ResourceType: "project", GroupID: prefix + "hidden-group", IssueCount: 91, CreatedAt: at},
				{ID: prefix + "workspace-label", Name: "Workspace label", Scope: " WorkSpace ", ResourceType: "issue", IssueCount: 92, CreatedAt: at},
				{ID: prefix + "private-label", Name: "Private label", Scope: private, ResourceType: "project", CreatedAt: at},
				{ID: prefix + "archived-label", Name: "Archived label", Scope: f.public, ResourceType: "project", ArchivedAt: &at, CreatedAt: at},
				{ID: prefix + "label", Name: "Last label", Description: "Other occurrence", Scope: f.public, ResourceType: "project", GroupID: prefix + "group", CreatedAt: at},
			}
			data.Projects = []domain.Project{
				{ID: prefix + "project", Name: "First project", SlugID: "first-project", Description: strings.Repeat("Full project body ", 3000), Summary: "Keep summary", Lead: &actor, Creator: &actor, TeamIDs: []string{f.public, private}, DependencyIDs: []string{prefix + "private-project"}, LabelIDs: []string{prefix + "label", prefix + "label", prefix + "private-label"}, MemberIDs: []string{actor.ID}, Initiatives: []string{prefix + "initiative", prefix + "private-initiative"}, Customers: []string{"stored-customer"}, Status: domain.ProjectStatus{ID: "active", Name: "Active", Type: "started"}, Health: "onTrack", Priority: 2, IssueCount: 81, Progress: 0.6, CreatedAt: at, UpdatedAt: at,
					Resources:  []domain.ProjectResource{{ID: "resource", Title: "Stored resource", URL: "https://example.test/resource", PinnedTeamIDs: []string{f.public, private}}},
					Milestones: []domain.ProjectMilestone{{ID: prefix + "milestone", ProjectID: prefix + "project", Name: "First milestone", Description: "Keep milestone description", CreatedAt: at, UpdatedAt: at, DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: "excluded nested history"}}}, {ID: prefix + "milestone", ProjectID: prefix + "private-project", Name: "Other milestone occurrence", Description: "Other body", CreatedAt: at, UpdatedAt: at}},
					Comments:   []domain.Comment{{Body: "excluded"}}, DescriptionRevisions: []domain.ProjectDescriptionRevision{{Description: "excluded"}}, ProgressHistory: []domain.ProjectProgressHistoryPoint{{}}},
				{ID: prefix + "second", Name: "Second project", SlugID: "second-project", TeamIDs: []string{f.public}, LabelIDs: []string{prefix + "label"}, CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(time.Hour), Milestones: []domain.ProjectMilestone{}},
				{ID: prefix + "private-project", Name: "Private project", TeamIDs: []string{private}, LabelIDs: []string{prefix + "label"}, CreatedAt: at.Add(time.Hour)},
				{ID: prefix + "archived-project", Name: "Archived project", TeamIDs: []string{f.public}, LabelIDs: []string{prefix + "label"}, ArchivedAt: &at, CreatedAt: at},
				{ID: prefix + "project", Name: "Last project", SlugID: "last-project", Description: "Last duplicate body", TeamIDs: []string{f.public}, LabelIDs: []string{prefix + "label"}, CreatedAt: at, UpdatedAt: at},
			}
			data.Initiatives = []domain.Initiative{
				{ID: prefix + "initiative", Name: "First initiative", SlugID: "first-initiative", Description: "Keep initiative body", Creator: actor, Owner: &actor, LeadTeamID: f.public, ContributingTeamIDs: []string{f.public, private}, ProjectIDs: []string{prefix + "project", prefix + "private-project"}, LabelIDs: []string{prefix + "label", prefix + "label", prefix + "private-label"}, ParentInitiativeIDs: []string{prefix + "private-initiative"}, Status: "planned", Health: "onTrack", CreatedAt: at, UpdatedAt: at, CommentAttachments: []domain.Attachment{{ID: "keep-comment-attachment"}}, Comments: []domain.Comment{{Body: "excluded"}}, DescriptionHistory: []domain.InitiativeDescriptionRevision{{Description: "excluded"}}},
				{ID: prefix + "private-initiative", Name: "Private initiative", LeadTeamID: private, ProjectIDs: []string{prefix + "private-project"}, LabelIDs: []string{prefix + "label"}, Creator: actor, CreatedAt: at.Add(time.Hour)},
				{ID: prefix + "initiative", Name: "Last initiative", SlugID: "last-initiative", Description: "Last initiative body", ProjectIDs: []string{prefix + "second"}, LabelIDs: []string{prefix + "label"}, Creator: actor, CreatedAt: at, UpdatedAt: at},
			}
			data.Documents = []domain.Document{
				{ID: prefix + "document", Title: "First document", SlugID: "first-document", Content: "searchword body old occurrence", ContentState: "state-only", ContentData: map[string]any{"data-only": true}, Creator: actor, TeamIDs: []string{f.public}, ProjectIDs: []string{prefix + "project", prefix + "private-project"}, CreatedAt: at, UpdatedAt: at, Attachments: []domain.Attachment{{ID: "keep-document-attachment"}}, Revisions: []domain.DocumentRevision{{Content: "excluded"}}},
				{ID: prefix + "second-document", Title: "Second document", Content: "searchword another result", Creator: actor, TeamIDs: []string{f.public}, CreatedAt: at.Add(-time.Hour), UpdatedAt: at.Add(time.Hour)},
				{ID: prefix + "document", Title: "Last document", SlugID: "last-document", Content: "searchword final duplicate \u51c6\u51fa\u6807\u51c6", Creator: actor, TeamIDs: []string{f.public}, CreatedAt: at, UpdatedAt: at},
				{ID: prefix + "private-document", Title: "Private document", Content: "searchword private", Creator: actor, TeamIDs: []string{private}, CreatedAt: at.Add(time.Hour), UpdatedAt: at},
				{ID: prefix + "inherited-document", Title: "Inherited document", Content: "searchword inherited", Creator: actor, InheritedTeamIDs: []string{private}, CreatedAt: at, UpdatedAt: at},
				{ID: prefix + "grant-document", Title: "Granted document", Content: "searchword granted", Creator: actor, InheritedTeamIDs: []string{private}, Permissions: []domain.DocumentPermission{{SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer"}}, CreatedAt: at, UpdatedAt: at},
				{ID: prefix + "archived-document", Title: "Archived document", Content: "searchword archived", Creator: actor, TeamIDs: []string{f.public}, ArchivedAt: &at, CreatedAt: at, UpdatedAt: at},
			}
			for _, kind := range []string{"document", "initiative"} {
				data.Favorites = append(data.Favorites, domain.Favorite{ID: prefix + kind + "-favorite", UserID: data.Viewer.ID, ResourceType: kind, ResourceID: prefix + kind})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		keys := []*domain.APIKey{nil,
			{ID: prefix + "all", Scopes: []string{"read"}},
			{ID: prefix + "empty", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}},
			{ID: prefix + "selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
			{ID: prefix + "app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
		}
		cases := 0
		for _, state := range []string{"warm", "reloaded", "unknown"} {
			if state == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if state == "unknown" {
				if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"projects", "projectMilestones", "initiatives", "labels", "documents"} {
				cursorID := prefix + "project"
				switch kind {
				case "projectMilestones":
					cursorID = prefix + "milestone"
				case "initiatives":
					cursorID = prefix + "initiative"
				case "labels":
					cursorID = prefix + "label"
				case "documents":
					cursorID = prefix + "document"
				}
				cursor := (pageKey{At: at, ID: cursorID}).cursor("createdAt")
				queries := []string{"", "first=1", "last=1", "first=2&after=" + cursor, "last=2&before=" + cursor, "includeArchived=TRUE", "first=0", "filter=" + url.QueryEscape(`{"unknown":{"eq":"x"}}`)}
				switch kind {
				case "projects":
					queries = append(queries, "orderBy=updatedAt&first=2", "teamId="+f.public, "includeArchived=true", "filter="+url.QueryEscape(`{"name":{"contains":"Last"}}`), "filter="+url.QueryEscape(`{"lead":{"displayName":{"eq":"Stored Display"}},"status":{"name":{"eq":"Active"}}}`))
				case "projectMilestones":
					queries = append(queries, "projectId=first-project", "projectId=last-project", "filter="+url.QueryEscape(`{"project":{"name":{"eq":"Last project"}}}`), "filter="+url.QueryEscape(`{"name":{"contains":"Other"}}`))
				case "initiatives":
					queries = append(queries, "orderBy=updatedAt", "filter="+url.QueryEscape(`{"name":{"contains":"Last"}}`), "filter="+url.QueryEscape(`{"creator":{"name":{"eq":"Stored Actor"}}}`))
				case "labels":
					queries = append(queries, "resourceType=all&includeArchived=true", "resourceType=project&teamId="+f.public+"&includeWorkspace=false", "resourceType=project&filter="+url.QueryEscape(`{"name":{"contains":"Last"}}`))
				case "documents":
					queries = append(queries, "query=searchword", "query=searchword&first=1", "query="+url.QueryEscape("\u51c6\u51fa\u6807\u51c6\u662f\u4ec0\u4e48"), "query=state-only", "query=searchword&filter="+url.QueryEscape(`{"title":{"eq":"Last document"},"searchSnippet":{"contains":"final"}}`), "filter="+url.QueryEscape(`{"creator":{"name":{"eq":"Stored Actor"}}}`), "includeArchived=true&teamId="+f.public)
				}
				for _, principal := range f.principals {
					for _, key := range keys {
						for _, query := range queries {
							serve := func(service *server) *httptest.ResponseRecorder {
								r := f.literalRequest(principal.user, key, nil)
								r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/"+kind, query
								w := httptest.NewRecorder()
								resourceConnectionHandler(service, kind)(w, r)
								return w
							}
							want, got := serve(&server{store: repo}), serve(&server{store: f.probe})
							var before, after any
							e1, e2 := json.Unmarshal(want.Body.Bytes(), &before), json.Unmarshal(got.Body.Bytes(), &after)
							a, _ := json.Marshal(before)
							b, _ := json.Marshal(after)
							if want.Code != got.Code || e1 != nil || e2 != nil || string(a) != string(b) {
								t.Fatalf("kind=%s state=%s principal=%s key=%+v query=%s status=%d/%d\nliteral=%s\nbounded=%s", kind, state, principal.name, key, query, want.Code, got.Code, a, b)
							}
							if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
								t.Fatal("connection leaked work or hydrated complete metadata")
							}
							cases++
						}
					}
				}
			}
		}
		// Development and explicit projected handoff retain their literal inputs.
		for _, kind := range []string{"projects", "projectMilestones", "initiatives", "labels", "documents"} {
			for _, mode := range []string{"dev", "handoff", "unknown-dev", "missing-workspace"} {
				serve := func(service *server) *httptest.ResponseRecorder {
					r := f.literalRequest(f.data.Viewer, keys[3], nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/"+kind
					service.authDisabled = mode == "dev" || mode == "unknown-dev"
					switch mode {
					case "unknown-dev":
						r.Header.Set("X-Workspace-Key", prefix+"missing")
					case "missing-workspace":
						r.Header.Del("X-Workspace-Key")
					case "handoff":
						data, err := repo.PagedWorkspaceMetadata(r.Context(), workspace, f.data.Viewer.ID)
						if err != nil {
							t.Fatal(err)
						}
						handOffPagedMetadata(r, workspace, f.data.Viewer.ID, data)
					}
					w := httptest.NewRecorder()
					resourceConnectionHandler(service, kind)(w, r)
					return w
				}
				want, got := serve(&server{store: repo}), serve(&server{store: f.probe})
				var a, b any
				_ = json.Unmarshal(want.Body.Bytes(), &a)
				_ = json.Unmarshal(got.Body.Bytes(), &b)
				before, _ := json.Marshal(a)
				after, _ := json.Marshal(b)
				if want.Code != got.Code || string(before) != string(after) {
					t.Fatalf("%s/%s literal=%d %s bounded=%d %s", kind, mode, want.Code, before, got.Code, after)
				}
				cases++
			}
		}
		t.Logf("five resource connections exact HTTP cases=%d", cases)
	}
}

func TestScaleP21ResourceConnectionsEncodingLifetime(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.connection_encoder", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{{ID: idgen.New("connection-encoder-"), Title: "Owned", Content: strings.Repeat("full body ", 10000), Creator: data.Viewer, TeamIDs: []string{f.public}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		for _, spool := range []string{"0", "10485760"} {
			for _, exit := range []string{"normal", "canceled", "panic"} {
				t.Run(spool+"/"+exit, func(t *testing.T) {
					t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", spool)
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/documents"
					ctx, cancel := context.WithCancel(r.Context())
					defer cancel()
					r = r.WithContext(ctx)
					w := &scaleP21ResourceRequestWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
					if exit == "canceled" {
						w.err = ctx.Err
					}
					if exit == "panic" {
						w.panicAt = "connection encoder panic"
					}
					done := make(chan any, 1)
					go func() {
						defer func() { done <- recover() }()
						(&server{store: f.probe}).listDocumentsConnection(w, r)
					}()
					select {
					case <-w.entered:
					case result := <-done:
						t.Fatalf("connection did not reach writer: %v status=%d", result, w.Code)
					case <-time.After(20 * time.Second):
						t.Fatal("connection writer did not start")
					}
					work := f.probe.MetadataWorkUsedForTesting()
					if spool == "0" && work == 0 || spool != "0" && work != 0 {
						close(w.resume)
						<-done
						t.Fatalf("producer/transport ownership differs: spool=%s work=%d", spool, work)
					}
					if exit == "canceled" {
						cancel()
					}
					close(w.resume)
					<-done
					if f.probe.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("connection retained work after encoder completion")
					}
				})
			}
		}
	}
}

func TestScaleP21ResourceConnectionsSigningFallback(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "resource-connection-signing-fixture")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.connection_signing", "", nil, func(data *domain.Bootstrap) error {
			body := "![before](/uploads/before.png) " + strings.Repeat("large legal body ", 10000) + " /uploads/after.png"
			data.Projects = []domain.Project{{ID: "signing-project", Name: "/uploads/name.png", Description: body, TeamIDs: []string{f.public}, Resources: []domain.ProjectResource{{ID: "signing-resource", URL: "/uploads/resource.png"}}}}
			data.Documents = []domain.Document{{ID: "signing-document", Title: "/uploads/title.png", Content: body, Creator: data.Viewer, TeamIDs: []string{f.public}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		for _, spool := range []string{"0", "1024", "4194304"} {
			t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", spool)
			for _, kind := range []string{"projects", "documents"} {
				request := func() *http.Request {
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/"+kind
					r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
					return r
				}
				literal := httptest.NewRecorder()
				resourceConnectionHandler(&server{store: repo}, kind)(literal, request())
				got := httptest.NewRecorder()
				var signing *hydratedURLSigning
				service := &server{store: f.probe}
				service.signedFileURLs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					signing, _ = r.Context().Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
					resourceConnectionHandler(service, kind)(w, r)
				})).ServeHTTP(got, request())
				if signing == nil || signing.key == nil {
					t.Fatal("connection signing context unavailable")
				}
				want := rewriteUploadURLs(literal.Body.Bytes(), signing.key, signing.base, signing.expires)
				if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), want) {
					t.Fatalf("%s spool=%s changed signed connection: status=%d", kind, spool, got.Code)
				}
				if f.probe.MetadataWorkUsedForTesting() != 0 || f.probe.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("signed connection retained work or decoded full metadata")
				}
			}
		}
	}
}

func TestScaleP21ResourceConnectionsSQLAndDevelopmentContext(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.connection_sql", "", nil, func(data *domain.Bootstrap) error {
			data.Projects = []domain.Project{{ID: "sql-project", Name: "SQL project", TeamIDs: []string{f.public}, LabelIDs: []string{"sql-label"}}}
			data.Documents = []domain.Document{{ID: "sql-document", Title: "SQL document", Content: "searchword", Creator: data.Viewer, TeamIDs: []string{f.public}}}
			data.Labels = []domain.IssueLabel{{ID: "sql-label", Name: "SQL label", Scope: f.public}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		for _, dev := range []bool{false, true} {
			for _, kind := range []string{"projects", "documents", "labels"} {
				counts := []int{}
				var bodies [][]byte
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					statements, canonicalPolicyReads, principalReads := 0, 0, 0
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path = http.MethodGet, "/api/"+kind
					r = r.WithContext(store.WithSQLStatementObserver(r.Context(), func(query string) {
						statements++
						if strings.Contains(query, "field='teams'") && strings.Contains(query, ">191 LIMIT 1") {
							canonicalPolicyReads++
						}
						if strings.HasPrefix(query, "SELECT u.id,u.email") && strings.Contains(query, "LEFT JOIN team_memberships") {
							principalReads++
						}
					}))
					w := httptest.NewRecorder()
					resourceConnectionHandler(&server{store: source, authDisabled: dev}, kind)(w, r)
					if w.Code != http.StatusOK {
						t.Fatal(kind, dev, source.BoundedMetadataEnabled(), w.Code, w.Body.String())
					}
					if source.BoundedMetadataEnabled() && !dev && (canonicalPolicyReads != 1 || principalReads != 2) {
						t.Fatal("canonical memo did not reuse once while preserving both live principal checks", kind, canonicalPolicyReads, principalReads)
					}
					counts = append(counts, statements)
					bodies = append(bodies, w.Body.Bytes())
				}
				if !bytes.Equal(bodies[0], bodies[1]) || dev && (counts[0] != 0 || counts[1] != 0) {
					t.Fatal("SQL proof changed output or trusted no-SQL boundary", kind, dev, counts)
				}
				t.Logf("connection=%s dev=%t legacy_SQL=%d bounded_SQL=%d", kind, dev, counts[0], counts[1])
			}
		}
		for _, kind := range []string{"projects", "projectMilestones", "initiatives", "labels", "documents"} {
			for _, query := range []string{"", "query=searchword"} {
				if query != "" && kind != "documents" {
					continue
				}
				var responses []*httptest.ResponseRecorder
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r := f.literalRequest(f.data.Viewer, nil, nil)
					r.Method, r.URL.Path, r.URL.RawQuery = http.MethodGet, "/api/"+kind, query
					ctx, cancel := context.WithCancel(r.Context())
					cancel()
					w := httptest.NewRecorder()
					resourceConnectionHandler(&server{store: source, authDisabled: true}, kind)(w, r.WithContext(ctx))
					responses = append(responses, w)
				}
				if responses[0].Code != responses[1].Code || !bytes.Equal(responses[0].Body.Bytes(), responses[1].Body.Bytes()) {
					t.Fatalf("dev cancellation %s/%s differs: literal=%d %s bounded=%d %s", kind, query, responses[0].Code, responses[0].Body.String(), responses[1].Code, responses[1].Body.String())
				}
			}
		}
	}
}
