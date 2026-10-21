package codingstore

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
	"flow/api/internal/codehost/gitlab"
	"flow/api/internal/coding"
	"flow/api/internal/config"
	"flow/api/internal/domain"
)

// Two Flow replicas (two coding.Service instances, each with its own GitLab
// code host) on one store, against a fake self-managed GitLab: project
// access tokens with GitLab's create/rotate/revoke semantics (rotation
// revokes the old value at once) and git smart-HTTP Basic auth. A session is
// stopped on one replica and continued in a new sandbox whose credential
// requests alternate between the replicas.

const connectionKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes, test only

type fakeProjectToken struct {
	ID     int64
	Name   string
	Value  string
	Active bool
}

type labGitLab struct {
	*httptest.Server
	mu      sync.Mutex
	nextID  int64
	tokens  []*fakeProjectToken
	created int
	rotated int
	gitAuth []string // "<status> fp=<sha256 prefix>" per git request
}

func newLabGitLab(t *testing.T) *labGitLab {
	lab := &labGitLab{nextID: 100}
	lab.Server = httptest.NewServer(http.HandlerFunc(lab.serve))
	t.Cleanup(lab.Close)
	return lab
}

func (l *labGitLab) host() string { parsed, _ := url.Parse(l.URL); return parsed.Host }

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func tokenView(token *fakeProjectToken, withValue bool) map[string]any {
	view := map[string]any{"id": token.ID, "name": token.Name, "active": token.Active, "revoked": !token.Active, "expires_at": time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02"), "created_at": time.Now().UTC().Format(time.RFC3339)}
	if withValue {
		view["token"] = token.Value
	}
	return view
}

func (l *labGitLab) serve(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/acme/app.git/") {
		_, password, _ := r.BasicAuth()
		for _, token := range l.tokens {
			if token.Active && token.Value == password {
				l.gitAuth = append(l.gitAuth, "200 fp="+codehost.CredentialFingerprint(password)[:12])
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		l.gitAuth = append(l.gitAuth, "401 fp="+codehost.CredentialFingerprint(password)[:12])
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("HTTP Basic: Access denied"))
		return
	}
	if r.Header.Get("PRIVATE-TOKEN") != "glpat-connection" {
		reply(w, http.StatusUnauthorized, map[string]any{"message": "401 Unauthorized"})
		return
	}
	const prefix = "/api/v4/projects/acme%2Fapp/access_tokens"
	path := r.URL.EscapedPath()
	if !strings.HasPrefix(path, prefix) {
		reply(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
		return
	}
	segments := strings.Split(strings.Trim(strings.TrimPrefix(path, prefix), "/"), "/")
	find := func(raw string) *fakeProjectToken {
		id, _ := strconv.ParseInt(raw, 10, 64)
		for _, token := range l.tokens {
			if token.ID == id {
				return token
			}
		}
		return nil
	}
	mint := func(name string) *fakeProjectToken {
		l.nextID++
		token := &fakeProjectToken{ID: l.nextID, Name: name, Value: fmt.Sprintf("glpat-lab-%d", l.nextID), Active: true}
		l.tokens = append(l.tokens, token)
		return token
	}
	switch {
	case segments[0] == "" && r.Method == http.MethodGet:
		list := []map[string]any{}
		for _, token := range l.tokens {
			if token.Active {
				list = append(list, tokenView(token, false))
			}
		}
		reply(w, http.StatusOK, list)
	case segments[0] == "" && r.Method == http.MethodPost:
		var body struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
			Level  int      `json:"access_level"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Level != 30 || strings.Join(body.Scopes, ",") != "read_repository,write_repository,read_api" {
			reply(w, http.StatusBadRequest, map[string]any{"message": "least privilege: Developer with repository scopes only"})
			return
		}
		l.created++
		reply(w, http.StatusCreated, tokenView(mint(body.Name), true))
	case len(segments) == 2 && segments[1] == "rotate" && r.Method == http.MethodPost:
		old := find(segments[0])
		if old == nil || !old.Active {
			reply(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
			return
		}
		old.Active = false // GitLab revokes the rotated token at once
		l.rotated++
		reply(w, http.StatusOK, tokenView(mint(old.Name), true))
	case len(segments) == 1 && r.Method == http.MethodGet:
		if token := find(segments[0]); token != nil {
			reply(w, http.StatusOK, tokenView(token, false))
			return
		}
		reply(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	case len(segments) == 1 && r.Method == http.MethodDelete:
		if token := find(segments[0]); token != nil && token.Active {
			token.Active = false
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reply(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	default:
		reply(w, http.StatusNotFound, map[string]any{"message": "404 Not Found"})
	}
}

// git runs the auth check of one git network operation (ls-remote, fetch,
// push) with the credential the helper got.
func (l *labGitLab) git(t *testing.T, password string) int {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, l.URL+"/acme/app.git/info/refs?service=git-receive-pack", nil)
	request.SetBasicAuth("flow-cs", password)
	resp, err := l.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (l *labGitLab) activeTokens() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, token := range l.tokens {
		if token.Active {
			n++
		}
	}
	return n
}

type labSource struct{ lab *labGitLab }

func (s labSource) GitLabConnections(context.Context, string) ([]gitlab.Connection, error) {
	return []gitlab.Connection{{ID: "gl1", WorkspaceKey: workspace, BaseURL: s.lab.URL, HostName: s.lab.host(), Kind: gitlab.ConnectionToken, Username: "flow-bot", Scopes: []string{"api"}}}, nil
}
func (s labSource) GitLabAPIToken(context.Context, string, string) (string, error) {
	return "glpat-connection", nil
}
func (s labSource) GitLabOAuthToken(context.Context, string, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}
func (s labSource) GitLabHTTPClient(context.Context, string) (*http.Client, error) {
	return s.lab.Client(), nil
}
func (s labSource) GitLabDeploymentHosts() []string { return nil }

// mergeRequests is the MR side of the fake (shared by both replicas).
type mergeRequests struct {
	mu      sync.Mutex
	head    string // the branch head on GitLab (what the last push left)
	created int
	open    *codehost.ChangeRequest
}

// replicaHost is a replica's real GitLab host; MR calls go to mergeRequests.
type replicaHost struct {
	*gitlab.Host
	mrs *mergeRequests
}

func (r replicaHost) CheckUserAccess(context.Context, string, codehost.RepoRef, codehost.UserIdentity) error {
	return nil
}
func (r replicaHost) BranchHead(context.Context, string, codehost.RepoRef, string) (string, error) {
	r.mrs.mu.Lock()
	defer r.mrs.mu.Unlock()
	return r.mrs.head, nil
}
func (r replicaHost) FindChangeRequest(context.Context, string, codehost.RepoRef, string) (*codehost.ChangeRequest, error) {
	r.mrs.mu.Lock()
	defer r.mrs.mu.Unlock()
	return r.mrs.open, nil
}
func (r replicaHost) CreateChangeRequest(_ context.Context, _ string, input codehost.CreateChangeRequestInput) (codehost.ChangeRequest, error) {
	r.mrs.mu.Lock()
	defer r.mrs.mu.Unlock()
	if r.mrs.open != nil {
		return *r.mrs.open, nil
	}
	r.mrs.created++
	change := codehost.ChangeRequest{Repo: input.Repo, Number: 42, URL: "https://gitlab.example/acme/app/-/merge_requests/42", Title: input.Title, State: "open", Draft: input.Draft, HeadBranch: input.Head, BaseBranch: input.Base}
	r.mrs.open = &change
	return change, nil
}

// replica builds one Flow replica: its own GitLab host (own process memory),
// the shared store and lock.
func (h *harness) replica(lab *labGitLab, mrs *mergeRequests, lock gitlab.TokenLocker) *coding.Service {
	built, err := gitlab.New(codehost.Dependencies{Config: config.CodingConfig{GitLab: config.CodingGitLabConfig{CredentialMode: gitlab.ModeProjectToken}}})
	if err != nil {
		h.t.Fatal(err)
	}
	host := built.(*gitlab.Host)
	host.SetConnectionSource(labSource{lab})
	host.SetSharedTokens(h.repo, lock)
	envs := fakeEnvs{envs: []domain.CodingEnvironment{{ID: "env1", Name: "App", CodeHost: domain.CodeHostGitLab, HostName: lab.host(), Repository: "acme/app", DefaultBranch: "main"}}}
	return coding.NewService(coding.Options{Config: h.cfg, Store: h.store, Anchors: h.store, Workspaces: harnessWorkspaces{h}, Environments: envs, Hosts: map[string]codehost.Host{domain.CodeHostGitLab: replicaHost{Host: host, mrs: mrs}}})
}

func TestGitLabSessionResumesAfterStopAcrossReplicas(t *testing.T) {
	cases := []struct {
		name   string
		pushed bool
		mr     bool
	}{
		{"unpushed branch", false, false},
		{"pushed branch", true, false},
		{"branch with a draft MR", true, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FLOW_CONNECTOR_SECRET_KEY", connectionKey)
			h := newHarness(t)
			lab := newLabGitLab(t)
			mrs := &mergeRequests{}
			var mu sync.Mutex
			lock := func(_ context.Context, _ string, action func() error) error {
				mu.Lock()
				defer mu.Unlock()
				return action()
			}
			replicaA, replicaB := h.replica(lab, mrs, lock), h.replica(lab, mrs, lock)
			ctx := context.Background()
			gitRequest := coding.CredentialRequest{Protocol: "https", Host: lab.host(), Path: "acme/app.git", Purpose: "git"}
			fetch := func(svc *coding.Service, token string) string {
				t.Helper()
				response, err := svc.Credentials(ctx, token, gitRequest)
				if err != nil || response.Password == "" {
					t.Fatalf("credentials: %v", err)
				}
				return response.Password
			}

			// First run, served by replica A: clone, develop, push, draft MR.
			h.svc = replicaA
			session := h.start("Resume " + test.name)
			session = h.drive(session.ID, domain.CodingPhaseStarting)
			token := h.tokens[session.SandboxID]
			boot, err := replicaA.Bootstrap(ctx, token, 0)
			if err != nil || boot.Turn == nil {
				t.Fatalf("bootstrap: %v", err)
			}
			first := fetch(replicaA, token)
			if lab.git(t, first) != http.StatusOK {
				t.Fatal("first clone")
			}
			if _, err := replicaA.Heartbeat(ctx, token, coding.HeartbeatRequest{Phase: "running"}); err != nil {
				t.Fatal(err)
			}
			wip := "3333333333333333333333333333333333333333"
			if test.mr {
				mrs.head = wip
				if _, err := replicaA.CompleteTurn(ctx, token, boot.Turn.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: true, HeadSHA: wip, Commits: []domain.CodingCommit{{SHA: wip, Message: "Fix"}}}); err != nil {
					t.Fatal(err)
				}
				if session = h.drive(session.ID, domain.CodingPhaseIdle); session.LatestPullRequest == nil || mrs.created != 1 {
					t.Fatalf("draft MR: %+v", session.LatestPullRequest)
				}
			}

			// Stop, handled by replica B (it never served this session's credentials).
			h.svc = replicaB
			if _, err := replicaB.Stop(ctx, workspace, session.ID, "user1", false); err != nil {
				t.Fatal(err)
			}
			if !test.mr {
				h.drive(session.ID, domain.CodingPhaseStopping)
				if _, err := replicaB.CompleteTurn(ctx, token, boot.Turn.ID, coding.TurnCompleteRequest{Status: coding.TurnStopped, Pushed: test.pushed, HeadSHA: wip, Commits: []domain.CodingCommit{{SHA: wip, Message: "WIP"}}}); err != nil {
					t.Fatal(err)
				}
			}
			h.exec(`UPDATE coding_sandboxes SET desired='stopped' WHERE id=?`, session.SandboxID)
			session = h.drive(session.ID, domain.CodingPhaseDone)
			if lab.git(t, first) != http.StatusUnauthorized || lab.activeTokens() != 0 {
				t.Fatalf("the stop on replica B must revoke the token replica A minted (active=%d)", lab.activeTokens())
			}

			// Continue: a new sandbox; its git operations reach both replicas.
			h.svc = replicaA
			if _, err := replicaA.Prompt(ctx, workspace, session.ID, coding.PromptInput{Prompt: "Continue", AuthorID: "user1"}); err != nil {
				t.Fatal(err)
			}
			session = h.drive(session.ID, domain.CodingPhaseStarting)
			resumeToken := h.tokens[session.SandboxID]
			resumed, err := replicaB.Bootstrap(ctx, resumeToken, 0)
			if err != nil || resumed.Turn == nil {
				t.Fatalf("resume bootstrap: %v", err)
			}
			if resumed.Repo.ExistingBranch != test.pushed || resumed.Repo.BranchName != boot.Repo.BranchName {
				t.Fatalf("resume repo: %+v", resumed.Repo)
			}
			var current string
			for i, svc := range []*coding.Service{replicaA, replicaB, replicaA, replicaB, replicaB, replicaA} {
				password := fetch(svc, resumeToken)
				if status := lab.git(t, password); status != http.StatusOK {
					t.Fatalf("resume git operation %d: %d; git log %v", i, status, lab.gitAuth)
				}
				if current != "" && password != current {
					t.Fatalf("resume git operation %d got a different token than the other replica", i)
				}
				current = password
			}
			if current == first || lab.activeTokens() != 1 || lab.rotated != 0 {
				t.Fatalf("resume: active=%d rotated=%d", lab.activeTokens(), lab.rotated)
			}

			// Development continues on the original branch and MR.
			if _, err := replicaA.Heartbeat(ctx, resumeToken, coding.HeartbeatRequest{Phase: "running"}); err != nil {
				t.Fatal(err)
			}
			next := "5555555555555555555555555555555555555555"
			mrs.head = next
			if _, err := replicaB.CompleteTurn(ctx, resumeToken, resumed.Turn.ID, coding.TurnCompleteRequest{Status: coding.TurnSuccess, Pushed: true, HeadSHA: next, Commits: []domain.CodingCommit{{SHA: next, Message: "More"}}}); err != nil {
				t.Fatal(err)
			}
			session = h.drive(session.ID, domain.CodingPhaseIdle)
			if session.LatestPullRequest == nil || session.LatestPullRequest.Number != 42 || mrs.created != 1 || session.BranchName != boot.Repo.BranchName {
				t.Fatalf("after the resumed turn: MR %+v created=%d branch %s", session.LatestPullRequest, mrs.created, session.BranchName)
			}
		})
	}
}
