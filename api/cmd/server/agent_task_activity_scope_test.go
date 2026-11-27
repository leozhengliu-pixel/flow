package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// agentTaskActivityStack is the authentication and workspace authorization
// the router applies, around the activity handler as it is now (original
// false) or with the pre-fix whole-projection authorization (original true).
// For a write, resourceAllowedLegacy took the same projection as for the
// detail read and had no agent-task rule either (agentTaskDetailOriginal).
func agentTaskActivityStack(s *server, original bool) http.Handler {
	mux := http.NewServeMux()
	handler := s.appendApplicationAgentActivity
	if original {
		handler = s.agentTaskDetailOriginal(handler)
	}
	mux.HandleFunc("POST /api/agent-tasks/{id}/activities", handler)
	return s.projectionGate.holdProjections(s.authenticate(mux))
}

func agentTaskActivityRequest(ctx context.Context, f agentTaskDetailFixture, principal agentTaskDetailPrincipal, id string, body map[string]any) *http.Request {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/agent-tasks/"+id+"/activities", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	for _, cookie := range principal.cookies {
		r.AddCookie(cookie)
	}
	if principal.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+principal.bearer)
	}
	return r
}

func agentTaskActivityPrompts(t *testing.T, repo *store.SQLiteStore, workspace, id string) int {
	t.Helper()
	activities, err := repo.AgentActivities(context.Background(), workspace, id, "")
	if err != nil {
		t.Fatal(err)
	}
	prompts := 0
	for _, activity := range activities {
		if activity.Type == "prompt" {
			prompts++
		}
	}
	return prompts
}

// A follow-up in an issue agent thread is saved while every projection slot
// is taken: the activity POST takes no projection admission. The pre-fix
// authorization queued the write behind the slot with no time limit (writes
// wait for capacity or cancellation), so behind a gateway it timed out and
// the reply was never saved.
func TestAgentTaskActivityPostSavesWithoutWorkspaceProjection(t *testing.T) {
	agentTaskDetailLegacyEnv(t)
	for name, repo := range scaleServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := agentTaskDetailSetup(t, repo)
			workspace := f.data.Workspace.URLKey
			const wait = 100 * time.Millisecond
			const held = 300 * time.Millisecond
			gate := newMetadataReadGate(1, 8, wait, 0)
			service := agentTaskDetailService(repo, gate, t)
			handler := newHandler(service)
			original := agentTaskActivityStack(service, true)
			member := f.principals[2]
			id := f.tasks["member-reply"]
			task, err := repo.AgentTask(t.Context(), workspace, id)
			if err != nil || task.Status != "complete" {
				t.Fatalf("fixture session: %v %q", err, task.Status)
			}
			counters := func() [3]int64 {
				return [3]int64{gate.served.Load(), gate.rejected.Load(), gate.cancelled.Load()}
			}
			followUp := func(version int64) map[string]any {
				return map[string]any{"type": "prompt", "body": "One more question", "expectedVersion": version}
			}

			// Saturated gate: the only slot is held by a wide projection.
			gate.slots <- struct{}{}
			released := false
			defer func() {
				if !released {
					<-gate.slots
				}
			}()

			// Pre-fix: the write stays queued until its client leaves.
			before := counters()
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan *httptest.ResponseRecorder, 1)
			started := time.Now()
			go func() {
				w := httptest.NewRecorder()
				original.ServeHTTP(w, agentTaskActivityRequest(ctx, f, member, id, followUp(task.Version)))
				done <- w
			}()
			select {
			case w := <-done:
				cancel()
				t.Fatalf("original follow-up with a saturated gate answered HTTP %d after %s; want it queued", w.Code, time.Since(started))
			case <-time.After(held):
			}
			cancel()
			<-done
			after := counters()
			if after[0] != before[0] || after[1] != before[1] || after[2] != before[2]+1 {
				t.Fatalf("original follow-up: admissions %v -> %v, want one cancellation", before, after)
			}
			unchanged, err := repo.AgentTask(t.Context(), workspace, id)
			if err != nil || unchanged.Version != task.Version || unchanged.Status != "complete" || agentTaskActivityPrompts(t, repo, workspace, id) != 0 {
				t.Fatalf("original follow-up changed the session: %v version %d status %q", err, unchanged.Version, unchanged.Status)
			}
			t.Logf("saturated   original follow-up: still queued after %s, cancelled; version %d, prompts 0", held, unchanged.Version)

			// Fixed: the router saves the follow-up at once, with no admission.
			// The deadline only keeps a regression from hanging the test.
			before = counters()
			ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			started = time.Now()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, agentTaskActivityRequest(ctx, f, member, id, followUp(task.Version)))
			elapsed := time.Since(started)
			after = counters()
			saved, err := repo.AgentTask(t.Context(), workspace, id)
			if err != nil || w.Code != http.StatusOK || after != before || elapsed >= held || saved.Version != task.Version+1 || saved.Status != "pending" || agentTaskActivityPrompts(t, repo, workspace, id) != 1 {
				t.Fatalf("follow-up with a saturated gate: HTTP %d after %s, admissions %v -> %v, version %d status %q: %s", w.Code, elapsed, before, after, saved.Version, saved.Status, w.Body.String())
			}
			t.Logf("saturated   router follow-up:   HTTP %d in %s, admissions=%d rejected=%d cancelled=%d", w.Code, elapsed.Round(time.Millisecond), after[0]-before[0], after[1]-before[1], after[2]-before[2])

			// Released gate: the pre-fix path saves too, after one admission.
			<-gate.slots
			released = true
			before = counters()
			started = time.Now()
			w = httptest.NewRecorder()
			original.ServeHTTP(w, agentTaskActivityRequest(t.Context(), f, member, id, followUp(saved.Version)))
			after = counters()
			if w.Code != http.StatusOK || after[0] != before[0]+1 || agentTaskActivityPrompts(t, repo, workspace, id) != 2 {
				t.Fatalf("original follow-up with a free gate: HTTP %d, admissions %v -> %v: %s", w.Code, before, after, w.Body.String())
			}
			t.Logf("free gate   original follow-up: HTTP %d in %s, admissions=%d", w.Code, time.Since(started).Round(time.Millisecond), after[0]-before[0])
		})
	}
}

// agentTaskActivityAppCredential installs an application that may post its
// own activities (app:assignable) and returns its installation and bearer.
func agentTaskActivityAppCredential(t *testing.T, repo *store.SQLiteStore, workspace, installer, name string, teams []string) (domain.ApplicationInstallation, string) {
	t.Helper()
	ctx := t.Context()
	client := domain.OAuthClient{ClientID: "activity-app-" + name, ClientName: "Activity test", RedirectURIs: []string{"https://app.example.test/callback"}}
	if err := repo.RegisterOAuthClient(ctx, client); err != nil {
		t.Fatal("could not register activity application")
	}
	scopes := []string{"read", "app:assignable"}
	app, err := repo.InstallApplication(ctx, workspace, domain.ApplicationInstallation{
		ClientID: client.ClientID, Name: client.ClientName, Active: true, InstalledBy: installer, TeamIDs: teams, Scopes: scopes,
	}, "")
	if err != nil {
		t.Fatal("could not install activity application")
	}
	code := "activity-app-code-" + name
	if _, err = repo.CreateOAuthAuthorizationGrant(ctx, code, domain.OAuthAuthorizationCode{
		ClientID: client.ClientID, Actor: "app", InstallerID: installer, UserID: installer, WorkspaceKey: workspace,
		TeamIDs: teams, Scopes: scopes, ExpiresAt: time.Now().Add(time.Hour),
	}, domain.OAuthAuthorization{ID: "activity-app-grant-" + name}, nil); err != nil {
		t.Fatal("could not create activity authorization")
	}
	secret, err := randomSecret("activity-app-")
	if err != nil {
		t.Fatal("could not generate activity credential")
	}
	if _, err = repo.ExchangeOAuthGrant(ctx, "authorization_code", code, client.ClientID, "activity-app-refresh-"+name,
		domain.APIKey{ID: "activity-app-key-" + name, SecretHash: secretHash(secret), CreatedAt: time.Now()}, func(domain.OAuthAuthorizationCode) bool { return true }, nil); err != nil {
		t.Fatal("could not exchange activity authorization")
	}
	return app, secret
}

// agentTaskActivityCase is one session shape and the activity posted to it.
type agentTaskActivityCase struct {
	name string
	// kind, ref and creator anchor the session; private puts it on the
	// private team's issue.
	kind, creator, coding string
	private, otherApp     bool
	// state brings the session to a status before the request: "complete"
	// (a saved reply), "active" (still working) or "awaiting" (a tool
	// approval is pending).
	state string
	// body builds the request from the session's version and reply id.
	body    func(version int64, reply string) map[string]any
	missing bool
}

var agentTaskActivityTimes = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})`)

// The scoped authorization answers every viewer, credential and session for
// POST /api/agent-tasks/{id}/activities exactly as the pre-fix
// whole-projection authorization did, in both metadata modes: the same
// status and body, the same saved activities, the same session version and
// status, and the same realtime events. Each request runs on its own fresh
// copy of the session, so every stack starts from the same state.
func TestAgentTaskActivityScopedAuthorizationDifferential(t *testing.T) {
	agentTaskDetailLegacyEnv(t)
	for name, repo := range scaleServerTestStores(t) {
		t.Run(name, func(t *testing.T) {
			f := agentTaskDetailSetup(t, repo)
			ctx := t.Context()
			workspace, owner, public := f.data.Workspace.URLKey, f.data.Viewer, f.data.Teams[0].ID
			prefix := idgen.New("ata-")
			// The fixture's sessions name the member, the guest, the private
			// team's issue and the document.
			users := map[string]string{"owner": owner.ID}
			sessions := map[string]domain.AgentTask{}
			for _, name := range []string{"member-reply", "guest-own-coding", "owner-private", "document"} {
				session, err := repo.AgentTask(ctx, workspace, f.tasks[name])
				if err != nil {
					t.Fatal(err)
				}
				sessions[name] = session
			}
			users["member"], users["guest"] = sessions["member-reply"].CreatorID, sessions["guest-own-coding"].CreatorID
			privateIssue, document := sessions["owner-private"].IssueID, sessions["document"].ResourceID
			agentApp, agentSecret := agentTaskActivityAppCredential(t, repo, workspace, owner.ID, prefix, []string{public})

			// Keys that may write; the fixture's keys only read.
			principals := slices.Clone(f.principals)
			for _, spec := range []struct {
				name, creator string
				teams         []string
				revoked       bool
			}{
				{"owner-write-key", owner.ID, nil, false},
				{"member-write-key", users["member"], nil, false},
				{"guest-write-key", users["guest"], nil, false},
				{"selected-write-key", owner.ID, []string{public}, false},
				{"selected-empty-write-key", owner.ID, []string{}, false},
				{"revoked-write-key", owner.ID, nil, true},
			} {
				key := domain.APIKey{ID: prefix + "-" + spec.name, Name: spec.name, SecretHash: secretHash(prefix + "-secret-" + spec.name), CreatorID: spec.creator, Scopes: []string{"read", "write"}, CreatedAt: time.Now().UTC()}
				if spec.teams != nil {
					key.TeamRestriction, key.TeamIDs = "selected", spec.teams
				}
				if spec.revoked {
					at := time.Now().UTC().Add(-time.Minute)
					key.RevokedAt = &at
				}
				if err := repo.MutateWorkspace(ctx, workspace, "api_key.created", key.ID, nil, func(data *domain.Bootstrap) error {
					data.APIKeys = append(data.APIKeys, key)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				principals = append(principals, agentTaskDetailPrincipal{name: spec.name, bearer: prefix + "-secret-" + spec.name})
			}
			principals = append(principals, agentTaskDetailPrincipal{name: "agent-app", bearer: agentSecret})
			// A member outside the public team reads its issues but may not
			// edit them: the activity's edit-access rule.
			origin, _ := url.Parse(f.host.URL)
			outsider, _ := resourceAccessScaleJoin(t, f.scalePlatformFixture, "Activity team outsider", prefix+"-outsider@example.test", "member")
			principals = append(principals, agentTaskDetailPrincipal{name: "team-outsider", cookies: outsider.Jar.Cookies(origin)})
			read := httptest.NewRecorder()
			newHandler(agentTaskDetailService(repo, newMetadataReadGate(4, 8, 5*time.Second, 0), t)).ServeHTTP(read, agentTaskDetailRequest(f, principals[len(principals)-1], "/api/agent-tasks/"+f.tasks["member-reply"]))
			if read.Code != http.StatusOK {
				t.Fatalf("the team outsider cannot read the session: HTTP %d", read.Code)
			}

			prompt := func(text string) func(int64, string) map[string]any {
				return func(version int64, _ string) map[string]any {
					return map[string]any{"type": "prompt", "body": text, "expectedVersion": version}
				}
			}
			cases := []agentTaskActivityCase{
				{name: "follow-up", kind: "issue", creator: "member", state: "complete", body: prompt("One more question")},
				{name: "stale-version", kind: "issue", creator: "member", state: "complete", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "prompt", "body": "One more question", "expectedVersion": version - 1}
				}},
				{name: "private-issue", kind: "issue", creator: "owner", private: true, state: "complete", body: prompt("One more question")},
				{name: "legacy-anchor", kind: "", creator: "member", state: "complete", body: prompt("One more question")},
				{name: "approval", kind: "issue", creator: "member", state: "awaiting", body: prompt("yes")},
				{name: "approval-explicit-reject", kind: "issue", creator: "member", state: "awaiting", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "prompt", "body": "Not this one", "approve": false, "expectedVersion": version}
				}},
				{name: "stop", kind: "issue", creator: "member", state: "active", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "canceled", "expectedVersion": version}
				}},
				{name: "retry", kind: "issue", creator: "member", state: "complete", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "retry", "expectedVersion": version}
				}},
				{name: "feedback", kind: "issue", creator: "member", state: "complete", body: func(version int64, reply string) map[string]any {
					return map[string]any{"type": "feedback", "body": "helpful", "activityId": reply, "enabled": true, "expectedVersion": version}
				}},
				{name: "delete-reply", kind: "issue", creator: "member", state: "complete", body: func(version int64, reply string) map[string]any {
					return map[string]any{"type": "deleted", "activityId": reply, "expectedVersion": version}
				}},
				{name: "empty-reply", kind: "issue", creator: "member", state: "complete", body: prompt("  ")},
				{name: "agent-response", kind: "issue", creator: "member", state: "active", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "response", "body": "Here is the answer", "expectedVersion": version}
				}},
				{name: "agent-thought", kind: "issue", creator: "member", state: "active", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "thought", "body": "Still reading", "ephemeral": true, "expectedVersion": version}
				}},
				{name: "other-app", kind: "issue", creator: "member", otherApp: true, state: "active", body: func(version int64, _ string) map[string]any {
					return map[string]any{"type": "response", "body": "Here is the answer", "expectedVersion": version}
				}},
				{name: "guest-own-coding", kind: "issue", creator: "guest", coding: "coding", state: "complete", body: prompt("One more question")},
				{name: "owner-coding", kind: "issue", creator: "owner", coding: "coding", state: "complete", body: prompt("One more question")},
				{name: "chat", kind: codingAnchorAgentChat, creator: "member", coding: "chat", state: "complete", body: prompt("One more question")},
				{name: "document", kind: "document", creator: "owner", state: "complete", body: prompt("One more question")},
				{name: "missing", missing: true, body: prompt("One more question")},
			}

			// create makes a fresh copy of a case's session and returns its id,
			// version and the id of the agent's saved reply.
			sequence := 0
			create := func(c agentTaskActivityCase) (string, int64, string) {
				sequence++
				id := fmt.Sprintf("%s-%s-%d", prefix, c.name, sequence)
				if c.missing {
					return id, 1, ""
				}
				appUser := agentApp.UserID
				if c.otherApp {
					appUser = prefix + "-other-app"
				}
				task := domain.AgentTask{ID: id, WorkspaceKey: workspace, ResourceType: c.kind, CreatorID: users[c.creator], AppUserID: appUser, Prompt: "Investigate"}
				switch c.kind {
				case "issue", "":
					task.ResourceID, task.IssueID = f.issueID, f.issueID
					if c.private {
						task.ResourceID, task.IssueID = privateIssue, privateIssue
					}
				case "document":
					task.ResourceID = document
				default:
					task.ResourceID = id + "-anchor"
				}
				if c.coding != "" {
					task.CodingSessionID = id + "-coding"
				}
				created, err := repo.CreateAgentInitiatedTask(ctx, task)
				if err != nil {
					t.Fatalf("%s: %v", c.name, err)
				}
				activities := []domain.AgentActivity{{ActorID: appUser, Type: "thought", Body: "Reading the issue"}}
				switch c.state {
				case "complete":
					activities = append(activities, domain.AgentActivity{ActorID: appUser, Type: "response", Body: f.reply})
				case "awaiting":
					activities = append(activities, domain.AgentActivity{ActorID: appUser, Type: "elicitation", Body: "May I update the issue?", ToolCall: &domain.AgentToolCall{ID: "call-1", Name: "update_issue", Status: "pending"}})
				}
				reply := ""
				for _, activity := range activities {
					if created, err = repo.AppendAgentActivity(ctx, workspace, id, created.Version, activity); err != nil {
						t.Fatalf("%s %s: %v", c.name, activity.Type, err)
					}
					reply = paddedVersion(created.Version)
				}
				return id, created.Version, reply
			}

			bounded, err := repo.BoundedMetadataViewForTesting(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []*store.SQLiteStore{repo, bounded} {
				t.Run(fmt.Sprintf("bounded=%t", source.BoundedMetadataEnabled()), func(t *testing.T) {
					var mu sync.Mutex
					events := map[string][]string{}
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						mu.Lock()
						defer mu.Unlock()
						var payload map[string]any
						_ = json.Unmarshal(event.Payload, &payload)
						id, _ := payload["taskId"].(string)
						events[id] = append(events[id], fmt.Sprintf("%s %s %s %v", event.Type, event.AggregateID, payload["resourceType"], payload["version"]))
					})
					defer source.SetRealtimeSink(nil)
					service := agentTaskDetailService(source, newMetadataReadGate(4, 8, 5*time.Second, 0), t)
					stacks := []struct {
						name    string
						handler http.Handler
					}{{"original", agentTaskActivityStack(service, true)}, {"scoped", agentTaskActivityStack(service, false)}, {"router", newHandler(service)}}
					statuses := map[int]int{}
					compared := 0
					outcomes := map[string]int{}
					for _, principal := range principals {
						for _, c := range cases {
							results := map[string]string{}
							for _, stack := range stacks {
								id, version, reply := create(c)
								mu.Lock()
								delete(events, id)
								mu.Unlock()
								w := httptest.NewRecorder()
								stack.handler.ServeHTTP(w, agentTaskActivityRequest(ctx, f, principal, id, c.body(version, reply)))
								state := "none"
								saved := "none"
								if !c.missing {
									task, err := repo.AgentTask(ctx, workspace, id)
									if err != nil {
										t.Fatal(err)
									}
									state = fmt.Sprintf("v%d %s prompt=%q pending=%v", task.Version, task.Status, task.Prompt, task.PendingTool)
									activities, err := repo.AgentActivities(ctx, workspace, id, "")
									if err != nil {
										t.Fatal(err)
									}
									raw, _ := json.Marshal(activities)
									saved = string(raw)
								}
								mu.Lock()
								published := strings.Join(events[id], "; ")
								mu.Unlock()
								result := fmt.Sprintf("HTTP %d %s\nstate %s\nactivities %s\nevents [%s]", w.Code, agentTaskDetailBody(w.Body.Bytes()), state, saved, published)
								result = agentTaskActivityTimes.ReplaceAllString(strings.ReplaceAll(result, id, "<task>"), "<time>")
								results[stack.name] = result
								if stack.name == "original" {
									statuses[w.Code]++
									outcomes[principal.name+" "+c.name] = w.Code
								}
							}
							if results["original"] != results["scoped"] || results["original"] != results["router"] {
								t.Errorf("%s posting %s:\noriginal %s\nscoped   %s\nrouter   %s", principal.name, c.name, results["original"], results["scoped"], results["router"])
							}
							compared++
						}
					}
					// The matrix must reach every rule it is meant to compare.
					for key, want := range agentTaskActivityExpected {
						if got := outcomes[key]; got != want {
							t.Errorf("%s: original HTTP %d, want %d", key, got, want)
						}
					}
					for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict} {
						if statuses[status] == 0 {
							t.Errorf("no request answered HTTP %d: %v", status, statuses)
						}
					}
					t.Logf("compared %d posts across %d principals and %d session cases; original statuses %v", compared, len(principals), len(cases), statuses)
				})
			}
		})
	}
}

// agentTaskActivityExpected pins outcomes that show the matrix exercises
// each rule: edit access to the issue (team members, private team, guests),
// the guest and coding-session rules, the application identity, approvals
// and reply deletion (requester or administrator), the expected version, key
// scopes, team restrictions and revocation.
var agentTaskActivityExpected = map[string]int{
	"member-private-outsider follow-up":      200,
	"owner follow-up":                        200,
	"guest follow-up":                        200,
	"team-outsider follow-up":                404,
	"member-private-outsider stale-version":  409,
	"owner private-issue":                    200,
	"private-member private-issue":           200,
	"admin-outside-private private-issue":    404,
	"member-private-outsider private-issue":  404,
	"member-private-outsider approval":       200,
	"admin-outside-private approval":         200,
	"private-member approval":                403,
	"guest approval":                         403,
	"private-member delete-reply":            403,
	"member-private-outsider delete-reply":   200,
	"member-private-outsider agent-response": 403,
	"member-private-outsider empty-reply":    400,
	"guest guest-own-coding":                 200,
	"guest owner-coding":                     404,
	"guest chat":                             404,
	"agent-app agent-response":               200,
	"agent-app agent-thought":                200,
	"agent-app follow-up":                    403,
	"agent-app other-app":                    404,
	"app follow-up":                          401,
	"owner-key follow-up":                    401,
	"owner-write-key follow-up":              200,
	"member-write-key private-issue":         404,
	"selected-write-key follow-up":           200,
	"selected-write-key private-issue":       404,
	"selected-empty-write-key follow-up":     404,
	"revoked-write-key follow-up":            401,
	"owner missing":                          404,
	"member-private-outsider document":       404,
	"owner document":                         200,
	"member-private-outsider chat":           200,
	"owner-write-key owner-coding":           200,
}
