package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/codehost"
	"flow/api/internal/config"
)

// fakeGitLab is an in-memory GitLab REST API v4 covering what the host uses:
// projects, branches, members, users, project access tokens, merge
// requests, notes and pipelines.
type fakeGitLab struct {
	*httptest.Server
	t *testing.T

	mu        sync.Mutex
	apiToken  string // accepted connection token (PRIVATE-TOKEN or Bearer)
	projects  map[string]*fakeProject
	users     map[string]int64
	nextID    int64
	requests  []string
	bodies    map[string][]map[string]any // "METHOD path" → decoded JSON bodies
	statusFor map[string]int              // "METHOD path-prefix" → forced status
	// mrConflict makes the next MR creation insert an MR and answer 409
	// (another request won the race).
	mrConflict bool
}

type fakeProject struct {
	ID            int64
	Path          string
	DefaultBranch string
	AccessLevel   int
	Branches      map[string]string
	Members       map[int64]int
	Tokens        []*fakeToken
	MRs           []map[string]any
	Notes         map[int][]string
	Pipelines     []map[string]any
	FailedJobs    map[int64][]map[string]any
	// Compare answers repository/compare by "from...to".
	Compare map[string]map[string]any
}

type fakeToken struct {
	ID        int64
	Name      string
	Token     string
	Active    bool
	Revoked   bool
	Scopes    []string
	Level     int
	ExpiresAt string
	CreatedAt time.Time
}

func newFakeGitLab(t *testing.T) *fakeGitLab {
	t.Helper()
	fake := &fakeGitLab{t: t, apiToken: "glpat-connection", projects: map[string]*fakeProject{}, users: map[string]int64{}, nextID: 100, bodies: map[string][]map[string]any{}, statusFor: map[string]int{}}
	fake.addProject(7, "acme/platform/api", "main", 40)
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (f *fakeGitLab) addProject(id int64, path, defaultBranch string, level int) *fakeProject {
	project := &fakeProject{ID: id, Path: path, DefaultBranch: defaultBranch, AccessLevel: level, Branches: map[string]string{defaultBranch: strings.Repeat("a", 40)}, Members: map[int64]int{}, Notes: map[int][]string{}, FailedJobs: map[int64][]map[string]any{}}
	f.projects[path] = project
	return project
}

func (f *fakeGitLab) project(path string) *fakeProject {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.projects[path]
}

func (f *fakeGitLab) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, request := range f.requests {
		if strings.HasPrefix(request, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeGitLab) lastBody(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	bodies := f.bodies[key]
	if len(bodies) == 0 {
		return nil
	}
	return bodies[len(bodies)-1]
}

func (f *fakeGitLab) id() int64 { f.nextID++; return f.nextID }

func write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (f *fakeGitLab) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4")
	key := r.Method + " " + path
	f.requests = append(f.requests, key)
	if r.Body != nil {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) == nil {
			f.bodies[key] = append(f.bodies[key], body)
		}
	}
	token := r.Header.Get("PRIVATE-TOKEN")
	if bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); bearer != "" && token == "" {
		token = bearer
	}
	if token != f.apiToken {
		write(w, http.StatusUnauthorized, map[string]any{"message": "401 Unauthorized"})
		return
	}
	for prefix, status := range f.statusFor {
		if strings.HasPrefix(key, prefix) {
			write(w, status, map[string]any{"message": fmt.Sprintf("forced %d for %s", status, token)})
			return
		}
	}
	query := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && path == "/projects":
		page, _ := strconv.Atoi(query.Get("page"))
		perPage, _ := strconv.Atoi(query.Get("per_page"))
		all := []map[string]any{}
		for _, project := range f.sortedProjects() {
			if search := query.Get("search"); search != "" && !strings.Contains(project.Path, search) {
				continue
			}
			all = append(all, f.projectJSON(project))
		}
		start, end := (page-1)*perPage, page*perPage
		if start > len(all) {
			start = len(all)
		}
		if end >= len(all) {
			end = len(all)
		} else {
			w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		}
		write(w, http.StatusOK, all[start:end])
		return
	case r.Method == http.MethodGet && path == "/users":
		id, ok := f.users[query.Get("username")]
		if !ok {
			write(w, http.StatusOK, []any{})
			return
		}
		write(w, http.StatusOK, []map[string]any{{"id": id, "username": query.Get("username")}})
		return
	}
	if !strings.HasPrefix(path, "/projects/") {
		write(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	rest := strings.TrimPrefix(path, "/projects/")
	rawID, sub, _ := strings.Cut(rest, "/")
	projectID, _ := url.PathUnescape(rawID)
	project := f.projects[projectID]
	if project == nil {
		for _, candidate := range f.projects {
			if strconv.FormatInt(candidate.ID, 10) == projectID {
				project = candidate
			}
		}
	}
	if project == nil {
		write(w, http.StatusNotFound, map[string]any{"message": "404 Project Not Found"})
		return
	}
	f.serveProject(w, r, project, sub, query)
}

func (f *fakeGitLab) sortedProjects() []*fakeProject {
	projects := []*fakeProject{}
	for _, project := range f.projects {
		projects = append(projects, project)
	}
	for i := range projects {
		for j := i + 1; j < len(projects); j++ {
			if projects[j].ID < projects[i].ID {
				projects[i], projects[j] = projects[j], projects[i]
			}
		}
	}
	return projects
}

func (f *fakeGitLab) projectJSON(project *fakeProject) map[string]any {
	return map[string]any{
		"id": project.ID, "path_with_namespace": project.Path, "default_branch": project.DefaultBranch,
		"http_url_to_repo": f.URL + "/" + project.Path + ".git", "web_url": f.URL + "/" + project.Path,
		"visibility": "private", "archived": false,
		"permissions": map[string]any{"project_access": map[string]any{"access_level": project.AccessLevel}},
	}
}

func (f *fakeGitLab) body(r *http.Request) map[string]any {
	key := r.Method + " " + strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4")
	bodies := f.bodies[key]
	if len(bodies) == 0 {
		return map[string]any{}
	}
	return bodies[len(bodies)-1]
}

func tokenJSON(token *fakeToken, withValue bool) map[string]any {
	value := map[string]any{"id": token.ID, "name": token.Name, "active": token.Active, "revoked": token.Revoked, "scopes": token.Scopes, "access_level": token.Level, "expires_at": token.ExpiresAt, "created_at": token.CreatedAt.Format(time.RFC3339)}
	if withValue {
		value["token"] = token.Token
	}
	return value
}

func (f *fakeGitLab) serveProject(w http.ResponseWriter, r *http.Request, project *fakeProject, sub string, query url.Values) {
	segments := strings.Split(sub, "/")
	switch {
	case sub == "" && r.Method == http.MethodGet:
		write(w, http.StatusOK, f.projectJSON(project))
	case len(segments) == 3 && segments[0] == "repository" && segments[1] == "branches":
		branch, _ := url.PathUnescape(segments[2])
		sha, ok := project.Branches[branch]
		if !ok {
			write(w, http.StatusNotFound, map[string]any{"message": "404 Branch Not Found"})
			return
		}
		write(w, http.StatusOK, map[string]any{"name": branch, "commit": map[string]any{"id": sha}})
	case len(segments) == 3 && segments[0] == "members" && segments[1] == "all":
		id, _ := strconv.ParseInt(segments[2], 10, 64)
		level, ok := project.Members[id]
		if !ok {
			write(w, http.StatusNotFound, map[string]any{"message": "404 Not found"})
			return
		}
		write(w, http.StatusOK, map[string]any{"id": id, "access_level": level})
	case len(segments) == 2 && segments[0] == "repository" && segments[1] == "compare":
		result, ok := project.Compare[query.Get("from")+"..."+query.Get("to")]
		if !ok || query.Get("straight") != "false" {
			write(w, http.StatusNotFound, map[string]any{"message": "404 Ref Not Found"})
			return
		}
		write(w, http.StatusOK, result)
	case segments[0] == "access_tokens":
		f.serveTokens(w, r, project, segments, query)
	case segments[0] == "merge_requests":
		f.serveMergeRequests(w, r, project, segments, query)
	case segments[0] == "pipelines" && len(segments) == 1:
		matches := []map[string]any{}
		for i := len(project.Pipelines) - 1; i >= 0; i-- {
			pipeline := project.Pipelines[i]
			if (query.Get("ref") != "" && pipeline["ref"] == query.Get("ref")) || (query.Get("sha") != "" && pipeline["sha"] == query.Get("sha")) {
				matches = append(matches, pipeline)
			}
		}
		if len(matches) > 1 {
			matches = matches[:1]
		}
		write(w, http.StatusOK, matches)
	case segments[0] == "pipelines" && len(segments) == 3 && segments[2] == "jobs":
		id, _ := strconv.ParseInt(segments[1], 10, 64)
		write(w, http.StatusOK, project.FailedJobs[id])
	default:
		write(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	}
}

func (f *fakeGitLab) serveTokens(w http.ResponseWriter, r *http.Request, project *fakeProject, segments []string, query url.Values) {
	if project.AccessLevel < 40 {
		write(w, http.StatusForbidden, map[string]any{"message": "403 Forbidden"})
		return
	}
	find := func(raw string) *fakeToken {
		id, _ := strconv.ParseInt(raw, 10, 64)
		for _, token := range project.Tokens {
			if token.ID == id {
				return token
			}
		}
		return nil
	}
	switch {
	case len(segments) == 1 && r.Method == http.MethodGet:
		list := []map[string]any{}
		for _, token := range project.Tokens {
			if query.Get("state") == "active" && !token.Active {
				continue
			}
			list = append(list, tokenJSON(token, false))
		}
		write(w, http.StatusOK, list)
	case len(segments) == 1 && r.Method == http.MethodPost:
		body := f.body(r)
		id := f.id()
		scopes := []string{}
		for _, scope := range body["scopes"].([]any) {
			scopes = append(scopes, scope.(string))
		}
		token := &fakeToken{ID: id, Name: body["name"].(string), Token: fmt.Sprintf("glpat-project-%d", id), Active: true, Scopes: scopes, Level: int(body["access_level"].(float64)), ExpiresAt: body["expires_at"].(string), CreatedAt: time.Now().UTC()}
		project.Tokens = append(project.Tokens, token)
		write(w, http.StatusCreated, tokenJSON(token, true))
	case len(segments) == 3 && segments[2] == "rotate" && r.Method == http.MethodPost:
		old := find(segments[1])
		if old == nil || !old.Active {
			write(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
			return
		}
		old.Active, old.Revoked = false, true
		id := f.id()
		token := &fakeToken{ID: id, Name: old.Name, Token: fmt.Sprintf("glpat-project-%d", id), Active: true, Scopes: old.Scopes, Level: old.Level, ExpiresAt: f.body(r)["expires_at"].(string), CreatedAt: time.Now().UTC()}
		project.Tokens = append(project.Tokens, token)
		write(w, http.StatusOK, tokenJSON(token, true))
	case len(segments) == 2 && r.Method == http.MethodDelete:
		token := find(segments[1])
		if token == nil || !token.Active {
			write(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
			return
		}
		token.Active, token.Revoked = false, true
		w.WriteHeader(http.StatusNoContent)
	default:
		write(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	}
}

func (f *fakeGitLab) newMR(project *fakeProject, body map[string]any) map[string]any {
	id := f.id()
	iid := len(project.MRs) + 1
	mr := map[string]any{
		"id": id, "iid": iid, "project_id": project.ID, "source_project_id": project.ID, "target_project_id": project.ID,
		"title": body["title"], "description": body["description"], "state": "opened",
		"source_branch": body["source_branch"], "target_branch": body["target_branch"],
		"sha": project.Branches[fmt.Sprint(body["source_branch"])], "web_url": fmt.Sprintf("%s/%s/-/merge_requests/%d", f.URL, project.Path, iid),
		"author": map[string]any{"username": "flow-bot"},
	}
	mr["draft"] = strings.HasPrefix(fmt.Sprint(body["title"]), "Draft:")
	project.MRs = append(project.MRs, mr)
	return mr
}

func (f *fakeGitLab) serveMergeRequests(w http.ResponseWriter, r *http.Request, project *fakeProject, segments []string, query url.Values) {
	switch {
	case len(segments) == 1 && r.Method == http.MethodGet:
		list := []map[string]any{}
		for _, mr := range project.MRs {
			if mr["source_branch"] == query.Get("source_branch") && (query.Get("state") == "" || mr["state"] == query.Get("state")) {
				list = append(list, mr)
			}
		}
		write(w, http.StatusOK, list)
	case len(segments) == 1 && r.Method == http.MethodPost:
		body := f.body(r)
		if f.mrConflict {
			f.mrConflict = false
			f.newMR(project, body)
			write(w, http.StatusConflict, map[string]any{"message": []string{"Another open merge request already exists for this source branch: !1"}})
			return
		}
		write(w, http.StatusCreated, f.newMR(project, body))
	case len(segments) >= 2:
		iid, _ := strconv.Atoi(segments[1])
		if iid < 1 || iid > len(project.MRs) {
			write(w, http.StatusNotFound, map[string]any{"message": "404 Not found"})
			return
		}
		mr := project.MRs[iid-1]
		switch {
		case len(segments) == 2 && r.Method == http.MethodGet:
			write(w, http.StatusOK, mr)
		case len(segments) == 2 && r.Method == http.MethodPut:
			body := f.body(r)
			if body["state_event"] == "close" {
				mr["state"] = "closed"
			} else {
				mr["description"] = body["description"]
			}
			write(w, http.StatusOK, mr)
		case len(segments) == 3 && segments[2] == "notes" && r.Method == http.MethodPost:
			project.Notes[iid] = append(project.Notes[iid], fmt.Sprint(f.body(r)["body"]))
			write(w, http.StatusCreated, map[string]any{"id": f.id(), "body": f.body(r)["body"]})
		default:
			write(w, http.StatusNotFound, map[string]any{"message": "404 Not found"})
		}
	default:
		write(w, http.StatusNotFound, map[string]any{"message": "404 Not found"})
	}
}

// fakeSource is a ConnectionSource over the fake.
type fakeSource struct {
	fake        *fakeGitLab
	connections []Connection
	oauthToken  string
	oauthExpiry time.Time
	// staleToken, when set, is returned instead of the fake's valid token.
	staleToken string
}

func (s *fakeSource) GitLabConnections(context.Context, string) ([]Connection, error) {
	return s.connections, nil
}

func (s *fakeSource) GitLabAPIToken(_ context.Context, _, id string) (string, error) {
	for _, connection := range s.connections {
		if connection.ID == id {
			if connection.Kind == ConnectionOAuth {
				return s.oauthToken, nil
			}
			if s.staleToken != "" {
				return s.staleToken, nil
			}
			return s.fake.apiToken, nil
		}
	}
	return "", fmt.Errorf("unknown connection %s", id)
}

func (s *fakeSource) GitLabOAuthToken(context.Context, string, string) (string, time.Time, error) {
	return s.oauthToken, s.oauthExpiry, nil
}

func (s *fakeSource) GitLabHTTPClient(context.Context, string) (*http.Client, error) {
	return s.fake.Client(), nil
}

func (s *fakeSource) GitLabDeploymentHosts() []string {
	return []string{"https://gitlab.internal.example"}
}

func fakeConnection(fake *fakeGitLab, kind string) Connection {
	parsed, _ := url.Parse(fake.URL)
	return Connection{ID: "integration_gitlab_1", WorkspaceKey: "ws", BaseURL: fake.URL, HostName: parsed.Host, Kind: kind, Username: "flow-bot", Scopes: []string{"api"}}
}

// newTestHost builds a host in mode against the fake, with a fixed clock.
func newTestHost(t *testing.T, fake *fakeGitLab, mode string, now time.Time) (*Host, *fakeSource) {
	t.Helper()
	built, err := New(codehost.Dependencies{Config: config.CodingConfig{GitLab: config.CodingGitLabConfig{CredentialMode: mode, TokenScopes: []string{"read_repository", "write_repository", "read_api"}}}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	host := built.(*Host)
	source := &fakeSource{fake: fake, connections: []Connection{fakeConnection(fake, ConnectionToken)}}
	host.SetConnectionSource(source)
	return host, source
}

func testRepo(fake *fakeGitLab) codehost.RepoRef {
	parsed, _ := url.Parse(fake.URL)
	return codehost.RepoRef{CodeHost: "gitlab", HostName: parsed.Host, FullName: "acme/platform/api"}
}
