package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SW334 covers issue/customer-request uploads. This table covers the other
// actual producers that preserve a literal percent-slash filename in their URL.
func TestScaleP21SW346ResidualProducerUploadAuthorization(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	foreign := scaleP03Fixture(t, repo)
	prefix := idgen.New("sw346-")
	documentID, projectID, initiativeID, updateID := prefix+"document", prefix+"project", prefix+"initiative", prefix+"update"
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW346 MCP upload", "teamId": f.public}, workspace, http.StatusCreated)
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw346_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{
			ID: documentID, Title: "SW346 document", Version: 1, Creator: f.data.Viewer, TeamIDs: []string{f.public},
		})
		data.Projects = append(data.Projects, domain.Project{ID: projectID, Name: "SW346 project", TeamIDs: []string{f.public}})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiativeID, Name: "SW346 initiative", ProjectIDs: []string{projectID}, ContributingTeamIDs: []string{f.public}})
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		if data.InitiativeUpdates == nil {
			data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
		}
		data.ProjectUpdates[projectID] = []domain.ProjectUpdate{{ID: updateID, ProjectID: projectID, Body: "SW346 project update", User: f.data.Viewer, CreatedAt: stamp}}
		data.InitiativeUpdates[initiativeID] = []domain.InitiativeUpdate{{ID: updateID, InitiativeID: initiativeID, Body: "SW346 initiative update", User: f.data.Viewer, CreatedAt: stamp}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	producer := &server{store: repo, uploadPath: t.TempDir(), realtime: newRealtimeHub()}
	specs := []struct{ kind, prefix string }{
		{"document", "document_attachment_"},
		{"project-update", "update_attachment_"},
		{"initiative-update", "update_attachment_"},
		{"project-comment", "project_comment_attachment_"},
		{"initiative-comment", "initiative_comment_attachment_"},
		{"loop", "loop_attachment_"},
		{"agent-retained", "agent_attachment_"},
		{"mcp-issue", "mcp_"},
	}
	produce := func(t *testing.T, kind, filename string) string {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, err := form.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		content := []byte("SW346 producer body")
		if _, err := part.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		request := f.literalRequest(f.data.Viewer, nil, nil)
		request.Method = http.MethodPost
		request.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
		request.ContentLength = int64(body.Len())
		request.Header.Set("Content-Type", form.FormDataContentType())
		request, release := beginRequestResourceReads(request)
		defer release()
		recorder := httptest.NewRecorder()
		switch kind {
		case "document":
			request.URL.Path = "/api/documents/" + documentID + "/attachments"
			request.SetPathValue("id", documentID)
			producer.createDocumentAttachment(recorder, request)
		case "project-update", "initiative-update":
			parent, family := projectID, "project"
			if kind == "initiative-update" {
				parent, family = initiativeID, "initiative"
			}
			request.URL.Path = "/api/" + family + "s/" + parent + "/updates/" + updateID + "/attachments"
			request.SetPathValue("id", parent)
			request.SetPathValue("updateId", updateID)
			producer.createPulseUpdateAttachment(family)(recorder, request)
		case "project-comment":
			request.URL.Path = "/api/projects/" + projectID + "/comment-attachments"
			request.SetPathValue("id", projectID)
			producer.createProjectCommentAttachment(recorder, request)
		case "initiative-comment":
			request.URL.Path = "/api/initiatives/" + initiativeID + "/comment-attachments"
			request.SetPathValue("id", initiativeID)
			producer.createInitiativeCommentAttachment(recorder, request)
		case "loop":
			request.URL.Path = "/api/loops/attachments"
			producer.uploadLoopAttachment(recorder, request)
		case "agent-retained":
			files, err := producer.saveAgentAttachments(request, []loopRunReplyAttachmentInput{{
				Name: filename, ContentType: "text/plain", Content: string(content),
			}})
			if err != nil || len(files) != 1 || files[0].Name != filename || files[0].URL == "" {
				t.Fatal("real retained-agent producer did not preserve the percent filename", err, files)
			}
			return files[0].URL
		case "mcp-issue":
			data, found := repo.BootstrapFor(workspace)
			if !found {
				t.Fatal("real MCP producer baseline missing")
			}
			result, err := producer.createMCPAttachment(request.Context(), mcpActor{WorkspaceKey: workspace, User: f.data.Viewer}, data, map[string]any{
				"issue": issue.ID, "filename": filename, "contentType": "text/plain", "insert": "none",
				"base64Content": base64.StdEncoding.EncodeToString(content), "sha256": fmt.Sprintf("%x", sha256.Sum256(content)),
			})
			if err != nil {
				t.Fatal("real legacy MCP issue producer", err)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var attachment domain.Attachment
			if err := json.Unmarshal(raw, &attachment); err != nil || attachment.Title != filename || attachment.URL == "" {
				t.Fatal("real MCP producer lost the raw filename URL", err, string(raw))
			}
			return attachment.URL
		default:
			t.Fatal("unknown real producer", kind)
		}
		if recorder.Code != http.StatusCreated {
			t.Fatalf("real %s producer failed: status=%d body=%s", kind, recorder.Code, recorder.Body.String())
		}
		var result struct {
			URL         string              `json:"url"`
			Title       string              `json:"title"`
			Name        string              `json:"name"`
			Attachments []domain.Attachment `json:"attachments"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(kind, "-update") {
			for _, attachment := range result.Attachments {
				if attachment.Title == filename {
					return attachment.URL
				}
			}
			t.Fatal("real update producer did not return its complete attachment")
		}
		if result.URL == "" || result.Title != filename && result.Name != filename {
			t.Fatal("real producer lost its original percent filename", kind, recorder.Body.String())
		}
		return result.URL
	}
	type uploadCase struct {
		kind, filename, rawURL, gate string
	}
	var cases []uploadCase
	for _, spec := range specs {
		path := produce(t, spec.kind, "report%2Fpart.txt")
		if !strings.HasPrefix(path, "/uploads/"+spec.prefix) {
			t.Fatal("real producer key namespace changed", spec.kind, path)
		}
		cases = append(cases, uploadCase{spec.kind, "report%2Fpart.txt", path, ""})
	}
	for _, control := range []struct{ filename, gate string }{
		{"report%2Fdefault-favorites", "default-favorites"},
		{"report%2Fmembers%2Fchild", "members"},
	} {
		cases = append(cases, uploadCase{"document", control.filename, produce(t, "document", control.filename), control.gate})
	}
	for _, row := range cases {
		parsed, err := url.Parse(row.rawURL)
		if err != nil || !strings.Contains(row.rawURL, row.filename) {
			t.Fatal("supported producer URL did not preserve its literal percent filename", row, err)
		}
		name := strings.TrimPrefix(parsed.Path, "/uploads/")
		if filepath.Base(name) == name || uploadNameFromURL(row.rawURL) != "" {
			t.Fatal("producer URL no longer exercises the decoded basename rejection", row.rawURL)
		}
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	f.reader.key = workspace
	tokens := map[string]string{}
	admin, member := f.principals[0].user, f.principals[2].user
	for _, user := range []domain.User{admin, member} {
		_, token, err := repo.Login(t.Context(), user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[user.ID] = token
	}
	type outcome struct {
		Allowed             bool
		Status              int
		Body, Pattern, Name string
	}
	type sqlCounts struct {
		pairs, legacy, bounded, maxLegacy, maxBounded, increased int
	}
	counts := map[string]*sqlCounts{}
	pairs, leaf404s, refusals := 0, 0, 0
	runPair := func(t *testing.T, row uploadCase, binding, method, phase string, user domain.User, key *domain.APIKey) {
		t.Helper()
		var want outcome
		legacyStatements := 0
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			if source.BoundedMetadataEnabled() {
				if err := source.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "reloaded" {
				scaleP21SW201ReloadSource(t, f.reader, source)
			}
			request := f.literalRequest(user, key, nil)
			var err error
			request.URL, err = url.Parse(row.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			request.Method = method
			if binding == "missing" {
				request.Header.Del("X-Workspace-Key")
			} else if binding == "foreign" {
				request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
			}
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
			request, release := beginRequestResourceReads(request)
			service := &server{store: source}
			statements := 0
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { statements++ }))
			recorder := httptest.NewRecorder()
			allowed := service.authorizeWorkspaceRequest(recorder, request, user)
			pattern, name := "", ""
			if allowed {
				mux := http.NewServeMux()
				mux.HandleFunc("GET /uploads/{name}", func(w http.ResponseWriter, r *http.Request) {
					name = r.PathValue("name")
					service.serveUploadOrAsset(w, r)
				})
				_, pattern = mux.Handler(request)
				mux.ServeHTTP(recorder, request)
			}
			got := outcome{allowed, recorder.Code, recorder.Body.String(), pattern, name}
			isAdmin := user.ID == f.principals[0].user.ID || user.ID == f.principals[1].user.ID
			required := binding != "foreign" && row.gate != "default-favorites" && (row.gate != "members" || isAdmin)
			if allowed != required || required && (got.Status != http.StatusNotFound || got.Body != "404 page not found\n" ||
				got.Pattern != "GET /uploads/{name}" || got.Name != strings.TrimPrefix(request.URL.Path, "/uploads/")) ||
				!required && got.Status != http.StatusForbidden {
				release()
				t.Fatalf("supported producer policy/leaf precedence changed: required=%t got=%+v", required, got)
			}
			if !source.BoundedMetadataEnabled() {
				want, legacyStatements = got, statements
			} else {
				pairs++
				if allowed {
					leaf404s++
				} else {
					refusals++
				}
				if !reflect.DeepEqual(want, got) {
					release()
					t.Fatalf("residual producer upload differs: literal=%+v bounded=%+v", want, got)
				}
				if request.Context().Value(pagedMetadataHandoffKey{}) != nil {
					release()
					t.Fatal("residual producer auth created a full paged handoff")
				}
				label := row.kind + "/" + row.gate + "/" + binding
				if counts[label] == nil {
					counts[label] = &sqlCounts{}
				}
				count := counts[label]
				count.pairs++
				count.legacy, count.bounded = count.legacy+legacyStatements, count.bounded+statements
				count.maxLegacy, count.maxBounded = max(count.maxLegacy, legacyStatements), max(count.maxBounded, statements)
				if statements > legacyStatements {
					count.increased++
				}
			}
			release()
			release()
			if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
				t.Fatal("residual producer auth leaked work or decoded full metadata")
			}
		}
	}
	producerPairs, boundaryPairs := 0, 0
	for index, row := range cases[:len(specs)] {
		t.Run(row.kind, func(t *testing.T) {
			t.Setenv("FLOW_RECORD_ACCESS", []string{"off", "shadow", "on"}[index%3])
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, phase := range []string{"warm", "reloaded"} {
					t.Run(method+"/"+phase, func(t *testing.T) {
						runPair(t, row, "own", method, phase, admin, nil)
						producerPairs++
					})
				}
			}
		})
	}
	t.Run("representative-boundaries", func(t *testing.T) {
		t.Setenv("FLOW_RECORD_ACCESS", "on")
		selectedEmpty := &domain.APIKey{
			ID: "sw346-selected-empty", Scopes: []string{"read", "write", "admin"},
			TeamRestriction: "selected", TeamIDs: []string{},
		}
		for _, row := range []struct {
			name, binding string
			upload        uploadCase
			user          domain.User
			key           *domain.APIKey
		}{
			{"own-selected-empty", "own", cases[0], member, selectedEmpty},
			{"missing-workspace", "missing", cases[0], member, selectedEmpty},
			{"foreign-workspace", "foreign", cases[0], member, selectedEmpty},
			{"default-favorites-refusal", "own", cases[len(specs)], admin, nil},
			{"member-gate-refusal", "own", cases[len(specs)+1], member, nil},
			{"member-gate-admin-success", "own", cases[len(specs)+1], admin, nil},
		} {
			t.Run(row.name, func(t *testing.T) {
				runPair(t, row.upload, row.binding, http.MethodGet, "warm", row.user, row.key)
				boundaryPairs++
			})
		}
	})
	if producerPairs != 32 || boundaryPairs != 6 || pairs != 38 || leaf404s != 35 || refusals != 3 {
		t.Fatal("residual producer table lost required success/boundary controls", producerPairs, boundaryPairs, pairs, leaf404s, refusals)
	}
	for label, count := range counts {
		t.Logf("producer=%s pairs=%d SQL literal/bounded=%d/%d max=%d/%d increased-pairs=%d", label, count.pairs, count.legacy, count.bounded, count.maxLegacy, count.maxBounded, count.increased)
	}
	t.Logf("SW346 producer-pairs=%d boundary-pairs=%d exact-leaf404=%d earlier-refusals=%d", producerPairs, boundaryPairs, leaf404s, refusals)
}
