package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// realtimePolicyFixture is a workspace with a private team (the admin and
// the insider are in it), a member outside it (outsider) and a guest of the
// public team, each holding a realtime stream.
type realtimePolicyFixture struct {
	srv      *server
	repo     *store.SQLiteStore
	host     *httptest.Server
	admin    *http.Client
	insider  *http.Client
	outsider *http.Client
	guest    *http.Client
	private  domain.Team

	adminSSE, insiderSSE, outsiderSSE, guestSSE *sseCollector
}

func newRealtimePolicyFixture(t *testing.T) realtimePolicyFixture {
	t.Helper()
	repo := pulseTestRepo(t)
	key := "test-workspace"
	srv := &server{store: repo, uploadPath: t.TempDir()}
	host := httptest.NewServer(newHandler(srv))
	t.Cleanup(host.Close)
	admin := authClient(t)
	authRequest[domain.AuthSession](t, admin, "POST", host.URL+"/api/auth/login", map[string]string{"email": "admin@example.test", "password": "test-password"}, "", http.StatusOK)
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/usr_member", map[string]string{"role": "member"}, "", http.StatusOK)
	private := authRequest[domain.Team](t, admin, "POST", host.URL+"/api/workspaces/"+key+"/teams", map[string]any{"name": "Private", "key": "PRV", "private": true}, "", http.StatusCreated)
	insiderClient, insider := verifiedAuthClient(t, host.URL, "Insider", "insider@example.test")
	if err := repo.EnsureWorkspaceMembership(context.Background(), "workspace_test", insider.ID); err != nil {
		t.Fatal(err)
	}
	authRequest[any](t, admin, "PUT", host.URL+"/api/workspaces/"+key+"/teams/"+private.ID+"/members/"+insider.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	guestClient, guest := verifiedAuthClient(t, host.URL, "Guest", "guest@example.test")
	if err := repo.EnsureWorkspaceMembership(context.Background(), "workspace_test", guest.ID); err != nil {
		t.Fatal(err)
	}
	authRequest[domain.WorkspaceMember](t, admin, "PATCH", host.URL+"/api/workspaces/"+key+"/members/"+guest.ID, map[string]string{"role": "guest"}, "", http.StatusOK)
	authRequest[any](t, admin, "PUT", host.URL+"/api/workspaces/"+key+"/teams/team_test/members/"+guest.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	outsider := authClient(t)
	authRequest[domain.AuthSession](t, outsider, "POST", host.URL+"/api/auth/login", map[string]string{"email": "member@example.test", "password": "test-password"}, "", http.StatusOK)
	return realtimePolicyFixture{
		srv: srv, repo: repo, host: host, admin: admin, insider: insiderClient, outsider: outsider, guest: guestClient, private: private,
		adminSSE:    collectSSE(t, admin, host.URL+"/api/realtime/events"),
		insiderSSE:  collectSSE(t, insiderClient, host.URL+"/api/realtime/events"),
		outsiderSSE: collectSSE(t, outsider, host.URL+"/api/realtime/events"),
		guestSSE:    collectSSE(t, guestClient, host.URL+"/api/realtime/events"),
	}
}

// realtimePolicySecrets are the names written into resources the outsider (and
// the guest) may not read; none may appear in anything their streams receive.
var realtimePolicySecrets = []string{"Insider personal view", "Private label secret", "Private state secret", "Private template secret", "Private team renamed", "hooks.example.test", "Private pipeline secret", "Private SLA secret", "secret-hash-value", "credential-json-value"}

type realtimePolicyIDs struct {
	view, label, state, template, webhook, customer, pipeline, sla, apiKey, passkey string
}

// writeRealtimePolicyResources writes one resource of each kind: a personal
// view, a private team's label, workflow state, issue template, release
// pipeline and SLA rule, a rename of the private team, a webhook and a
// customer.
func writeRealtimePolicyResources(t *testing.T, fixture realtimePolicyFixture) realtimePolicyIDs {
	t.Helper()
	key, base := "test-workspace", fixture.host.URL
	ids := realtimePolicyIDs{}
	ids.view = authRequest[domain.SavedView](t, fixture.insider, http.MethodPost, base+"/api/views", map[string]any{"name": "Insider personal view", "scope": "personal", "resource": "issues"}, key, http.StatusCreated).ID
	ids.label = authRequest[domain.IssueLabel](t, fixture.insider, http.MethodPost, base+"/api/teams/"+fixture.private.ID+"/labels", map[string]any{"name": "Private label secret"}, key, http.StatusCreated).ID
	ids.state = authRequest[domain.WorkflowState](t, fixture.admin, http.MethodPost, base+"/api/teams/"+fixture.private.ID+"/states", map[string]any{"name": "Private state secret", "type": "started"}, key, http.StatusCreated).ID
	ids.template = authRequest[domain.IssueTemplate](t, fixture.admin, http.MethodPost, base+"/api/teams/"+fixture.private.ID+"/templates", map[string]any{"name": "Private template secret", "title": "Private template secret"}, key, http.StatusCreated).ID
	authRequest[domain.Team](t, fixture.admin, http.MethodPatch, base+"/api/workspaces/"+key+"/teams/"+fixture.private.ID, map[string]any{"name": "Private team renamed"}, "", http.StatusOK)
	ids.webhook, ids.customer, ids.pipeline, ids.sla = "webhook_policy", "customer_policy", "pipeline_policy", "sla_policy"
	write := func(eventType, aggregate string, change func(data *domain.Bootstrap)) {
		if err := fixture.repo.MutateWorkspace(context.Background(), key, eventType, aggregate, map[string]any{"name": "input"}, func(data *domain.Bootstrap) error {
			change(data)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("webhook.created", ids.webhook, func(data *domain.Bootstrap) {
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: ids.webhook, Name: "Hook", URL: "https://hooks.example.test/private-path", ResourceTypes: []string{"Issue"}, TeamIDs: []string{}, Enabled: true, CreatorID: "usr_admin"})
	})
	ids.apiKey, ids.passkey = "apikey_policy", "passkey_policy"
	write("api_key.created", ids.apiKey, func(data *domain.Bootstrap) {
		data.APIKeys = append(data.APIKeys, domain.APIKey{ID: ids.apiKey, Name: "Key", CreatorID: "usr_admin", SecretHash: "secret-hash-value"})
	})
	write("passkey.created", ids.passkey, func(data *domain.Bootstrap) {
		data.Passkeys = append(data.Passkeys, domain.Passkey{ID: ids.passkey, UserID: "usr_admin", CredentialJSON: "credential-json-value"})
	})
	write("customer.created", ids.customer, func(data *domain.Bootstrap) {
		data.Customers = append(data.Customers, domain.Customer{ID: ids.customer, Name: "Customer secret", Status: "active"})
	})
	write("release_pipeline.updated", ids.pipeline, func(data *domain.Bootstrap) {
		data.ReleasePipelines = append(data.ReleasePipelines, domain.ReleasePipeline{ID: ids.pipeline, Name: "Private pipeline secret", TeamIDs: []string{fixture.private.ID}})
	})
	write("sla_rule.updated", ids.sla, func(data *domain.Bootstrap) {
		data.SLARules = append(data.SLARules, domain.SLARule{ID: ids.sla, Name: "Private SLA secret", TeamIDs: []string{fixture.private.ID}})
	})
	return ids
}

// assertRealtimeEventsHide fails when any event carries a hidden id or text.
func assertRealtimeEventsHide(t *testing.T, who string, events []domain.RealtimeEvent, ids []string, secrets []string) {
	t.Helper()
	for _, event := range events {
		raw := string(mustJSON(t, event))
		for _, id := range ids {
			if id != "" && event.AggregateID == id {
				t.Errorf("%s received %s for %s, which they cannot read: %s", who, event.Type, id, raw)
			}
		}
		for _, secret := range secrets {
			if strings.Contains(raw, secret) {
				t.Errorf("%s received %q in %s: %s", who, secret, event.Type, raw)
			}
		}
	}
}

func (c *sseCollector) snapshot() []domain.RealtimeEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]domain.RealtimeEvent{}, c.events...)
}

// Events outside the projected kinds (views, labels, states, templates,
// teams, webhooks, customers, pipelines, SLA rules) reach a viewer only as
// REST would show them: a member outside a private team, and a guest, never
// receive its resources or another member's personal view; customers are
// hidden from guests; members get a bare signal without the content.
func TestRealtimeDefaultEventsFollowReadAccess(t *testing.T) {
	fixture := newRealtimePolicyFixture(t)
	ids := writeRealtimePolicyResources(t, fixture)
	waitForEvents(t, func() bool {
		return fixture.insiderSSE.has("view.created", ids.view) && fixture.insiderSSE.has("issue_label.created", ids.label) &&
			fixture.insiderSSE.has("workflow_state.created", ids.state) && fixture.insiderSSE.has("release_pipeline.updated", ids.pipeline) &&
			fixture.outsiderSSE.has("customer.created", ids.customer) && fixture.adminSSE.has("webhook.created", ids.webhook) && fixture.adminSSE.has("api_key.created", ids.apiKey)
	})
	hidden := []string{ids.view, ids.label, ids.state, ids.template, ids.webhook, ids.pipeline, ids.sla, ids.apiKey, ids.passkey, fixture.private.ID}
	assertRealtimeEventsHide(t, "outsider", fixture.outsiderSSE.snapshot(), hidden, realtimePolicySecrets)
	assertRealtimeEventsHide(t, "guest", fixture.guestSSE.snapshot(), append(hidden, ids.customer), append(realtimePolicySecrets, "Customer secret"))
	// The outsider may know a customer changed, not what it holds.
	if customer := lastPayload(t, fixture.outsiderSSE, "customer.created", ids.customer); len(customer) != 0 {
		t.Errorf("outsider's customer.created carries content: %v", customer)
	}
	// The insider (who may read them all but the webhook) is told of each change, without other members' input.
	for _, want := range [][2]string{{"view.created", ids.view}, {"issue_label.created", ids.label}, {"workflow_state.created", ids.state}, {"issue_template.created", ids.template}, {"team.updated", fixture.private.ID}, {"sla_rule.updated", ids.sla}} {
		if !fixture.insiderSSE.has(want[0], want[1]) {
			t.Errorf("insider missed %s for %s", want[0], want[1])
		}
	}
	if fixture.insiderSSE.has("webhook.created", ids.webhook) {
		t.Error("a member received another member's webhook")
	}
	if hook := lastPayload(t, fixture.adminSSE, "webhook.created", ids.webhook); strings.Contains(string(mustJSON(t, hook)), "private-path") {
		t.Errorf("webhook.created carries the webhook: %v", hook)
	}
	// Credentials reach admins as a bare signal, never their secret; a passkey (no actor here) only its owner.
	assertRealtimeEventsHide(t, "admin", fixture.adminSSE.snapshot(), []string{ids.passkey}, []string{"secret-hash-value", "credential-json-value"})
}

// A change that can take a record out of a viewer's reach (a view moved into
// a private team, a team membership) reaches the viewers who cannot read it as
// an anonymous signal: their client reloads, but learns neither the record
// nor who changed it. Workspace members hear of directory changes; a guest
// only of their own. Administration and personal records stay with admins
// and their owner.
func TestRealtimeScopeChangesSignalWithoutNamingTheRecord(t *testing.T) {
	fixture := newRealtimePolicyFixture(t)
	key, base := "test-workspace", fixture.host.URL
	view := authRequest[domain.SavedView](t, fixture.admin, http.MethodPost, base+"/api/views", map[string]any{"name": "Shared view", "scope": "workspace", "resource": "issues"}, key, http.StatusCreated)
	authRequest[domain.SavedView](t, fixture.admin, http.MethodPatch, base+"/api/views/"+view.ID, map[string]any{"name": "Private team view", "scope": "team", "teamId": fixture.private.ID}, key, http.StatusOK)
	_, joiner := verifiedAuthClient(t, base, "Joiner", "joiner@example.test")
	if err := fixture.repo.EnsureWorkspaceMembership(context.Background(), "workspace_test", joiner.ID); err != nil {
		t.Fatal(err)
	}
	authRequest[any](t, fixture.admin, "PUT", base+"/api/workspaces/"+key+"/teams/"+fixture.private.ID+"/members/"+joiner.ID, map[string]any{"member": true, "role": "member"}, "", http.StatusNoContent)
	authRequest[domain.UserSettings](t, fixture.insider, http.MethodPatch, base+"/api/account/settings", map[string]any{"jobTitle": "Private title"}, key, http.StatusOK)
	waitForEvents(t, func() bool {
		return fixture.insiderSSE.has("view.updated", view.ID) && fixture.adminSSE.has("team_member.updated", fixture.private.ID) &&
			len(fixture.outsiderSSE.find("team_member.updated")) > 0 && fixture.outsiderSSE.has("workspace_member.joined", joiner.ID) &&
			len(fixture.insiderSSE.find("user_settings.updated")) > 0
	})
	for _, event := range fixture.outsiderSSE.find("view.updated") {
		if event.AggregateID != "" || event.ActorID != "" || len(event.Payload) != 0 {
			t.Errorf("outsider's view.updated names the view: %+v", event)
		}
	}
	if len(fixture.outsiderSSE.find("view.updated")) == 0 {
		t.Error("outsider was not told that a view they hold left their reach")
	}
	for _, event := range append(fixture.outsiderSSE.find("team_member.updated"), fixture.guestSSE.find("team_member.updated")...) {
		if event.AggregateID != "" || len(event.Payload) != 0 {
			t.Errorf("a private team's membership change names it: %+v", event)
		}
	}
	if fixture.guestSSE.has("workspace_member.joined", joiner.ID) {
		t.Error("a guest heard of another member's directory change")
	}
	for _, stream := range []*sseCollector{fixture.outsiderSSE, fixture.guestSSE, fixture.adminSSE} {
		if len(stream.find("user_settings.updated")) != 0 {
			t.Error("another member received the insider's settings")
		}
	}
	assertRealtimeEventsHide(t, "outsider", fixture.outsiderSSE.snapshot(), []string{fixture.private.ID}, []string{"Private team view", "Private title"})
}

// Every event type the server publishes is either projected per viewer or
// named by a read rule (realtime_event_policy.go), so none reaches subscribers
// by default.
func TestRealtimeEventTypesHaveAReadRule(t *testing.T) {
	literal := regexp.MustCompile(`(?:Mutate(?:Workspace|WorkspaceWithAggregate|LabelDeletion|NotificationDelivery|WithAggregate)?\([^"\n]*|(?:Realtime|Domain)Event\{[^}\n]*Type: |publishWorkspaceEvent\([^"\n]*|eventType = |eventType := )"([a-z_]+\.[a-z_.]+)"`)
	projected := []string{"issue.", "comment.", "attachment.", "document.", "project.", "project_update.", "initiative.", "initiative_update.", "notification.", "notifications.", "favorite.", "favorite_folder.", "subscription.", "draft.", "drafts.", "ai.", "agent_task.", "coding_session.", "loop_run.", "presence.", "workspace_preferences.", "test."}
	files, _ := filepath.Glob("*.go")
	stores, _ := filepath.Glob("../../internal/store/*.go")
	seen := map[string]bool{}
	for _, file := range append(files, stores...) {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range literal.FindAllStringSubmatch(string(raw), -1) {
			eventType := match[1]
			if seen[eventType] || slices.ContainsFunc(projected, func(prefix string) bool { return strings.HasPrefix(eventType, prefix) }) {
				continue
			}
			seen[eventType] = true
			if _, ok := realtimeRuleFor(eventType); !ok {
				t.Errorf("%s (%s) has no realtime read rule: it would reach only admins", eventType, file)
			}
		}
	}
	if len(seen) < 100 {
		t.Fatalf("found only %d event types; the scan is broken", len(seen))
	}
}

// Replays (the change log, a resumed stream from the durable log or the
// in-memory ring) and events from another instance pass the same filter.
func TestRealtimeReplayAndPeerEventsFollowReadAccess(t *testing.T) {
	fixture := newRealtimePolicyFixture(t)
	key, base := "test-workspace", fixture.host.URL
	// A public change first: its event id is the ring cursor the resumed stream starts after.
	public := authRequest[domain.IssueLabel](t, fixture.admin, http.MethodPost, base+"/api/teams/team_test/labels", map[string]any{"name": "Public label"}, key, http.StatusCreated)
	waitForEvents(t, func() bool { return fixture.outsiderSSE.has("issue_label.created", public.ID) })
	cursor := fixture.outsiderSSE.find("issue_label.created")[0].ID
	ids := writeRealtimePolicyResources(t, fixture)
	waitForEvents(t, func() bool { return fixture.insiderSSE.has("sla_rule.updated", ids.sla) })
	hidden := []string{ids.view, ids.label, ids.state, ids.template, ids.webhook, ids.pipeline, ids.sla, ids.apiKey, ids.passkey, fixture.private.ID}

	// GET /api/realtime/changes from the start of the log.
	request, _ := http.NewRequest(http.MethodGet, base+"/api/realtime/changes?since=0&limit=500", nil)
	request.Header.Set("X-Workspace-Key", key)
	response, err := fixture.outsider.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var changes struct {
		Events []domain.RealtimeEvent `json:"events"`
	}
	if err := json.Unmarshal(raw, &changes); err != nil {
		t.Fatalf("changes: %v: %s", err, raw)
	}
	if len(changes.Events) == 0 {
		t.Fatalf("the change log returned nothing: %s", raw)
	}
	assertRealtimeEventsHide(t, "outsider's change log", changes.Events, hidden, realtimePolicySecrets)

	// A stream resumed from the durable log, and one resumed from the ring.
	durable := collectSSE(t, fixture.outsider, base+"/api/realtime/events?since=0")
	ring := collectSSE(t, fixture.guest, base+"/api/realtime/events?since="+cursor)
	waitForEvents(t, func() bool { return len(durable.find("issue_label.created")) > 0 && len(ring.snapshot()) > 1 })
	assertRealtimeEventsHide(t, "outsider's resumed stream", durable.snapshot(), hidden, realtimePolicySecrets)
	assertRealtimeEventsHide(t, "guest's resumed stream", ring.snapshot(), append(hidden, ids.customer), append(realtimePolicySecrets, "Customer secret"))

	// An event from another instance carries its raw payload: it is filtered on delivery like a local one.
	peer, _ := json.Marshal(map[string]any{"entity": map[string]any{"id": ids.label, "name": "Private label secret", "scope": fixture.private.ID}})
	fixture.srv.realtime.publish(key, domain.RealtimeEvent{ID: "evt_peer_label", Type: "label.updated", AggregateID: ids.label, Payload: peer, CreatedAt: time.Now().UTC()})
	peerView, _ := json.Marshal(map[string]any{"entity": map[string]any{"id": ids.view, "name": "Insider personal view"}})
	fixture.srv.realtime.publish(key, domain.RealtimeEvent{ID: "evt_peer_view", Type: "view.updated", AggregateID: ids.view, Payload: peerView, CreatedAt: time.Now().UTC()})
	waitForEvents(t, func() bool {
		return fixture.insiderSSE.has("label.updated", ids.label) && fixture.insiderSSE.has("view.updated", ids.view)
	})
	assertRealtimeEventsHide(t, "outsider (peer events)", fixture.outsiderSSE.snapshot(), hidden, realtimePolicySecrets)
	if label := lastPayload(t, fixture.insiderSSE, "label.updated", ids.label); strings.Contains(string(mustJSON(t, label)), "scope") {
		t.Errorf("a peer event's raw entity reached the insider: %v", label)
	}
}
