package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// The label settings routes migrated from the COLD facade to bounded
// readers and writers, served through the real cookie/Bearer authentication,
// authorization and route mux on the legacy store, a warm bounded view and a
// bounded view loaded from SQL:
//   - GET /api/labels and GET /api/teams/{id}/labels for every role, a
//     member outside the private team, a guest, team-restricted and plain
//     API keys, resource filters, labels used by visible, private and
//     archived issues, projects and initiatives (their issue counts);
//   - POST /api/labels, POST /api/teams/{id}/labels, POST /api/label-groups
//     (workspace and team groups, grouped labels, refusals);
//   - PATCH /api/labels/{id} and PATCH /api/label-groups/{id} (renames,
//     regrouping, archiving a group with its labels, unchanged edits,
//     missing and out-of-scope targets);
//   - DELETE /api/labels/{id}, DELETE /api/teams/{id}/labels/{labelId},
//     DELETE /api/label-groups/{id} and DELETE /api/teams/{id}/label-groups/{groupId}
//     (labels used by issues, suggestions, projects, views and favorites).
// Responses, every owned row (metadata records, root, issue rows, events,
// audit), webhooks, realtime publications and the installed label fields
// must agree byte for byte except generated IDs and clocks; bounded, no
// route decodes the whole workspace and the installed image equals a reload.

// The installed fields compared. Projects and initiatives are compared as
// rows (their legacy snapshot refreshes display copies a load does not).
var labelRoutesFields = []string{"labels", "labelGroups", "savedViews", "favorites", "issueTemplates", "issueSuggestions", "users"}

// labelRoutesImage is oauthMetadataImage without the labels' issueCount, which the
// legacy snapshot derives on every read and a bounded image keeps as stored.
func labelRoutesImage(t *testing.T, repo *store.SQLiteStore, workspace string, names ...string) map[string]json.RawMessage {
	t.Helper()
	image := oauthMetadataImage(t, repo, workspace, names...)
	if raw, ok := image["labels"]; ok {
		var labels []map[string]any
		if json.Unmarshal(raw, &labels) == nil {
			for _, label := range labels {
				delete(label, "issueCount")
			}
			image["labels"], _ = json.Marshal(labels)
		}
	}
	return image
}

func labelRoutesHandler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/labels", s.listWorkspaceLabels)
	mux.HandleFunc("POST /api/labels", s.createWorkspaceLabel)
	mux.HandleFunc("PATCH /api/labels/{id}", s.updateWorkspaceLabel)
	mux.HandleFunc("DELETE /api/labels/{id}", s.deleteWorkspaceLabel)
	mux.HandleFunc("POST /api/label-groups", s.createLabelGroup)
	mux.HandleFunc("PATCH /api/label-groups/{id}", s.updateLabelGroup)
	mux.HandleFunc("DELETE /api/label-groups/{id}", s.deleteLabelGroup)
	mux.HandleFunc("GET /api/teams/{id}/labels", s.listTeamLabels)
	mux.HandleFunc("POST /api/teams/{id}/labels", s.createTeamLabel)
	mux.HandleFunc("PATCH /api/teams/{id}/labels/{labelId}", s.updateTeamLabel)
	mux.HandleFunc("DELETE /api/teams/{id}/labels/{labelId}", s.deleteTeamLabel)
	mux.HandleFunc("DELETE /api/teams/{id}/label-groups/{groupId}", s.deleteTeamLabelGroup)
	return s.withColdMetadataRouteMux(mux, s.authenticate(mux))
}

func labelRoutesDo(s *server, method, target, body, workspace string, user domain.User, bearer string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	if workspace != "" {
		r.Header.Set("X-Workspace-Key", workspace)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	} else if user.ID != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: oAuthClientLookupSession(user)})
	}
	w := httptest.NewRecorder()
	labelRoutesHandler(s).ServeHTTP(w, r)
	return w
}

// labelRoutesRun is oauthClientLookupRun over the label fields, with the issue rows a label
// deletion rewrites; bounded, the call must not decode the whole workspace.
func (f scaleTeamCreateFixture) labelRoutesRun(t *testing.T, source string, fields []string, call func(*server) string) (oauthClientLookupOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.service(t, source)
	workspace := f.data.Workspace.URLKey
	got := oauthClientLookupOutcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, discussionRoutesEnvelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	})
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	before := labelRoutesImage(t, service.store, workspace, fields...)
	decodes := service.store.WorkspaceMetadataDecodes()
	got.Result = call(service)
	mu.Lock()
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	mu.Unlock()
	if service.store.BoundedMetadataEnabled() && service.store.WorkspaceMetadataDecodes() != decodes {
		t.Errorf("%s: the label routes decoded the whole workspace %d times", source, service.store.WorkspaceMetadataDecodes()-decodes)
	}
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	slices.SortFunc(events, func(a, b []any) int { return strings.Compare(fmt.Sprint(a...), fmt.Sprint(b...)) })
	slices.SortFunc(got.Webhooks, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	slices.SortFunc(got.Realtime, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	got.State.Events = events
	got.Extra = map[string][][]any{
		"issues":     f.rows(t, `SELECT id,version,archived,data FROM issue_records WHERE workspace_key=? ORDER BY id`, workspace),
		"issueLabel": f.rows(t, `SELECT issue_id,label_id FROM issue_label_records WHERE workspace_key=? ORDER BY issue_id,label_id`, workspace),
		"attributes": f.rows(t, `SELECT issue_id,field,value FROM issue_attribute_records WHERE workspace_key=? ORDER BY issue_id,field`, workspace),
	}
	if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("%s: retained metadata work %d", source, service.store.MetadataWorkUsedForTesting())
	}
	installed := labelRoutesImage(t, service.store, workspace, fields...)
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	fresh := labelRoutesImage(t, reloaded, workspace, fields...)
	if service.store.BoundedMetadataEnabled() {
		// The bounded image equals a bounded view loaded from SQL.
		view, err := reloaded.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		loaded := labelRoutesImage(t, view, workspace, fields...)
		for _, name := range fields {
			want, have := scaleTriageNormalize(t, loaded[name], started), scaleTriageNormalize(t, installed[name], started)
			if want != have {
				got.Stale = append(got.Stale, name)
				t.Errorf("%s: installed %s is not the reloaded one: %s", source, name, policyFirstDifference(want, have))
			}
		}
	}
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	got.Installed = labelRoutesDelta(t, fields, before, installed)
	got.Reloaded, _ = json.Marshal(fresh)
	return got, started
}

// labelRoutesDelta lists, per field, the installed entries a call added or
// changed, sorted (the legacy snapshot appends where a load orders).
func labelRoutesDelta(t *testing.T, fields []string, before, after map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	entries := func(raw json.RawMessage) map[string]bool {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		result := map[string]bool{}
		add := func(key string, item any) {
			encoded, _ := json.Marshal(item)
			result[key+"\t"+string(encoded)] = true
		}
		switch value := value.(type) {
		case []any:
			for index, item := range value {
				key := fmt.Sprint("#", index)
				if object, ok := item.(map[string]any); ok {
					if id, ok := object["id"].(string); ok {
						key = id
					}
				}
				add(key, item)
			}
		case map[string]any:
			for key, item := range value {
				add(key, item)
			}
		}
		return result
	}
	result := map[string][]string{}
	for _, name := range fields {
		old := entries(before[name])
		for entry := range entries(after[name]) {
			if !old[entry] {
				result[name] = append(result[name], entry)
			}
		}
		slices.Sort(result[name])
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// labelRoutesDifferential runs call on the legacy store, a warm bounded view and a
// bounded view loaded from SQL and compares their normalized outcomes.
func (f scaleTeamCreateFixture) labelRoutesDifferential(t *testing.T, name string, fields []string, setup func(*testing.T), call func(*server) string) []oauthClientLookupOutcome {
	t.Helper()
	prepare := f.oauthMetadataPrepare(setup)
	users := []domain.User{}
	for _, principal := range f.principals {
		users = append(users, principal.user)
	}
	f.scaleTriageWriterFixture.prepare = func(t *testing.T) {
		stamp := time.Now().UTC()
		for _, user := range users {
			f.exec(t, `DELETE FROM auth_sessions WHERE user_id=?`, user.ID)
			f.exec(t, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, secretHash(oAuthClientLookupSession(user)), user.ID,
				stamp.Add(24*time.Hour).Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano))
		}
		prepare(t)
	}
	defer func() { f.scaleTriageWriterFixture.prepare = nil }()
	var normalized []string
	var outcomes []oauthClientLookupOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.labelRoutesRun(t, source, fields, call)
		// A key's use stamp is written asynchronously; it is not the route's.
		for _, row := range got.State.Metadata {
			if fmt.Sprint(row[0]) == "apiKeys" {
				row[3] = nil
			}
		}
		got.Installed = dropKeyUse(t, got.Installed)
		outcomes = append(outcomes, got)
		text := oauthClientLookupNormalize(t, got, started)
		normalized = append(normalized, text)
		if dir := os.Getenv("FLOW_SW600_DUMP"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name+"-"+source+".json"), []byte(text), 0o600)
		}
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, policyFirstDifference(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func dropKeyUse(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var value map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return raw
	}
	delete(value, "apiKeys")
	result, _ := json.Marshal(value)
	return result
}

// labelRoutesLabels seeds the label fixture through the legacy store: workspace,
// team and private-team labels and groups, an archived label, labels on
// visible, private and archived issues, on live and archived projects and on
// an initiative, a label suggestion, a view filter and favorites, and API
// keys (plain and team-restricted).
func (f scaleTeamCreateFixture) labelRoutesLabels(t *testing.T) {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	owner, member, inside := f.principals[0].user, f.principals[2].user, f.principals[4].user
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	archived := at.Add(time.Hour)
	f.mutate(t, "scale_p21_sw600.labels", func(data *domain.Bootstrap) {
		data.WorkspaceSettings.LabelPermission = "members"
		data.LabelGroups = append(data.LabelGroups,
			domain.LabelGroup{ID: "sw600-g-ws", Name: "Kind", Color: "#111111", Scope: "Workspace", ResourceType: "issue", CreatedAt: at},
			domain.LabelGroup{ID: "sw600-g-project", Name: "Project kind", Scope: "Workspace", ResourceType: "project", CreatedAt: at},
			domain.LabelGroup{ID: "sw600-g-team", Name: "Team kind", Scope: f.public, ResourceType: "issue", CreatedAt: at},
			domain.LabelGroup{ID: "sw600-g-private", Name: "Private kind", Scope: f.private, ResourceType: "issue", CreatedAt: at})
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: "sw600-l-ws", Name: "Bug", Color: "#ff0000", Scope: "Workspace", ResourceType: "issue", GroupID: "sw600-g-ws", CreatorID: owner.ID, CreatedAt: at},
			domain.IssueLabel{ID: "sw600-l-ws-plain", Name: "Plain", Color: "#00ff00", Description: "No group", CreatedAt: at},
			domain.IssueLabel{ID: "sw600-l-ws-project", Name: "Roadmap", Scope: "workspace", ResourceType: "project", GroupID: "sw600-g-project", CreatedAt: at},
			domain.IssueLabel{ID: "sw600-l-ws-initiative", Name: "Bet", Scope: "Workspace", ResourceType: "initiative", CreatedAt: at},
			domain.IssueLabel{ID: "sw600-l-ws-archived", Name: "Old", Scope: "Workspace", ResourceType: "issue", CreatedAt: at, ArchivedAt: &archived},
			domain.IssueLabel{ID: "sw600-l-public", Name: "Frontend", Scope: f.public, ResourceType: "issue", GroupID: "sw600-g-team", CreatedAt: at},
			domain.IssueLabel{ID: "sw600-l-private", Name: "Secret", Scope: f.private, ResourceType: "issue", GroupID: "sw600-g-private", CreatedAt: at})
		data.Projects = append(data.Projects,
			domain.Project{ID: "sw600-p-public", Name: "Public labels", TeamIDs: []string{f.public}, LabelIDs: []string{"sw600-l-ws-project", "sw600-l-ws", "sw600-l-ws"}},
			domain.Project{ID: "sw600-p-archived", Name: "Archived labels", TeamIDs: []string{f.public}, LabelIDs: []string{"sw600-l-ws-project"}, ArchivedAt: &archived},
			domain.Project{ID: "sw600-p-private", Name: "Private labels", TeamIDs: []string{f.private}, LabelIDs: []string{"sw600-l-ws-project", "sw600-l-private"}})
		data.Initiatives = append(data.Initiatives,
			domain.Initiative{ID: "sw600-i", Name: "Labelled bet", ProjectIDs: []string{"sw600-p-public"}, LabelIDs: []string{"sw600-l-ws-initiative", "sw600-l-ws"}})
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "sw600-view", Name: "Bugs", Resource: "issues", Scope: "workspace", Filters: json.RawMessage(`{"labelIds":["sw600-l-ws","sw600-l-public"]}`)})
		data.Favorites = append(data.Favorites,
			domain.Favorite{ID: "sw600-fav-ws", UserID: member.ID, ResourceType: "label", ResourceID: "sw600-l-ws", CreatedAt: at},
			domain.Favorite{ID: "sw600-fav-group", UserID: member.ID, ResourceType: "label", ResourceID: "sw600-g-team", CreatedAt: at})
		data.IssueSuggestions = append(data.IssueSuggestions,
			domain.IssueSuggestion{ID: "sw600-s-label", IssueID: f.visible.ID, Type: "label", State: "active", SuggestedLabelID: "sw600-l-ws", StateChangedAt: at, CreatedAt: at, UpdatedAt: at})
		key := func(id, creator, secret string, change func(*domain.APIKey)) domain.APIKey {
			item := domain.APIKey{ID: id, Name: id, Prefix: "sw600_" + id, SecretHash: secretHash(secret), CreatorID: creator, CreatedAt: at, TeamIDs: []string{}, TeamRestriction: "all"}
			if change != nil {
				change(&item)
			}
			return item
		}
		data.APIKeys = append(data.APIKeys,
			key("sw600-key-member", member.ID, "sw600-secret-member", nil),
			key("sw600-key-selected", member.ID, "sw600-secret-selected", func(k *domain.APIKey) { k.TeamRestriction, k.TeamIDs = "selected", []string{f.public} }),
			key("sw600-key-inside", inside.ID, "sw600-secret-inside", func(k *domain.APIKey) { k.TeamRestriction, k.TeamIDs = "selected", []string{f.private} }),
			key("sw600-key-owner-none", owner.ID, "sw600-secret-owner-none", func(k *domain.APIKey) { k.TeamRestriction, k.TeamIDs = "selected", []string{} }))
	})
	labels := map[string]domain.IssueLabel{}
	if full, ok := f.repository.BootstrapFor(workspace); ok {
		for _, label := range full.Labels {
			labels[label.ID] = label
		}
	}
	for _, item := range []struct {
		id       string
		labels   []string
		archived bool
	}{
		{f.triage.ID, []string{"sw600-l-ws", "sw600-l-public", "sw600-l-ws-plain"}, false},
		{f.visible.ID, []string{"sw600-l-ws", "sw600-l-ws-archived", "sw600-l-public"}, true},
		{f.hidden.ID, []string{"sw600-l-ws", "sw600-l-private"}, false},
	} {
		if item.id == "" {
			continue
		}
		if err := f.repository.MutateWorkspace(store.WithIssueRecordMutations(t.Context(), item.id), workspace, "scale_p21_sw600.issue", item.id, nil, func(data *domain.Bootstrap) error {
			issue, err := issueByID(data, item.id)
			if err != nil {
				return err
			}
			issue.Labels = nil
			for _, id := range item.labels {
				issue.Labels = append(issue.Labels, labels[id])
			}
			if item.archived {
				issue.ArchivedAt = &archived
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLabelRoutes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleTeamCreateSetup(t)
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	if len(f.principals) < 5 {
		t.Fatalf("principals %d", len(f.principals))
	}
	// owner; admin (private team); member of the public team only (outside
	// the private team); guest (public team); member of the private team only.
	owner, admin, member, guest, inside := f.principals[0].user, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	type trace struct{ lines []string }
	type step func(*server, *trace)
	// current is the running case: steps fail it, not the parent test.
	current := t
	run := func(steps ...step) func(*server) string {
		return func(s *server) string {
			out := &trace{}
			for _, step := range steps {
				step(s, out)
			}
			return strings.Join(out.lines, "\n")
		}
	}
	do := func(name, method, target string, body any, user domain.User) step {
		text := ""
		if body != nil {
			raw, _ := json.Marshal(body)
			text = string(raw)
		}
		return func(s *server, out *trace) {
			out.lines = append(out.lines, oauthClientLookupResponse(name, labelRoutesDo(s, method, target, text, workspace, user, "")))
		}
	}
	bearer := func(name, target, secret string) step {
		return func(s *server, out *trace) {
			out.lines = append(out.lines, oauthClientLookupResponse(name, labelRoutesDo(s, http.MethodGet, target, "", workspace, domain.User{}, secret)))
			oAuthCOLDQuiet(current, s, workspace, secret)
		}
	}
	list := func(user domain.User, query string) step {
		return do("labels "+user.Name+" "+query, http.MethodGet, "/api/labels"+query, nil, user)
	}
	teamList := func(user domain.User, team string) step {
		return do("team-labels "+user.Name+" "+team, http.MethodGet, "/api/teams/"+team+"/labels", nil, user)
	}
	lists := []step{list(owner, ""), list(member, ""), list(inside, ""), teamList(owner, f.public), teamList(admin, f.private), teamList(member, f.public)}
	seed := f.labelRoutesLabels
	cases := []struct {
		name  string
		setup func(*testing.T)
		call  func(*server) string
	}{
		{"list-roles", seed, run(list(owner, ""), list(admin, ""), list(member, ""), list(guest, ""), list(inside, ""))},
		{"list-resource-types", seed, run(list(member, "?resourceType=project"), list(member, "?resourceType=initiative"), list(member, "?resourceType=issue"), list(owner, "?resourceType=other"))},
		{"list-team", seed, run(teamList(owner, f.public), teamList(owner, f.private), teamList(admin, f.private), teamList(member, f.public), teamList(member, f.private), teamList(guest, f.public), teamList(inside, f.private), teamList(owner, "sw600-missing-team"))},
		{"list-keys", seed, run(bearer("key member", "/api/labels", "sw600-secret-member"), bearer("key selected", "/api/labels", "sw600-secret-selected"), bearer("key inside", "/api/labels", "sw600-secret-inside"), bearer("key none", "/api/labels", "sw600-secret-owner-none"),
			bearer("key selected team", "/api/teams/"+f.public+"/labels", "sw600-secret-selected"), bearer("key inside team", "/api/teams/"+f.private+"/labels", "sw600-secret-inside"))},
		{"list-unseeded", nil, run(list(owner, ""), teamList(owner, f.public))},
		{"create-workspace", seed, run(append([]step{
			do("create plain", http.MethodPost, "/api/labels", map[string]any{"name": "  Fresh  ", "color": "#123456", "description": " described "}, member),
			do("create grouped", http.MethodPost, "/api/labels", map[string]any{"name": "Grouped", "groupId": " sw600-g-ws "}, owner),
			do("create project", http.MethodPost, "/api/labels", map[string]any{"name": "Project", "resourceType": "project", "groupId": "sw600-g-project"}, owner),
			do("create archived", http.MethodPost, "/api/labels", map[string]any{"name": "Archived", "archivedAt": "2026-01-02T03:04:05+02:00"}, owner),
		}, lists...)...)},
		{"create-workspace-refused", seed, run(
			do("create blank", http.MethodPost, "/api/labels", map[string]any{"name": " "}, owner),
			do("create team group", http.MethodPost, "/api/labels", map[string]any{"name": "X", "groupId": "sw600-g-team"}, owner),
			do("create wrong resource group", http.MethodPost, "/api/labels", map[string]any{"name": "X", "groupId": "sw600-g-project"}, owner),
			do("create missing group", http.MethodPost, "/api/labels", map[string]any{"name": "X", "groupId": "sw600-missing"}, owner),
			do("create bad resource", http.MethodPost, "/api/labels", map[string]any{"name": "X", "resourceType": "cycle"}, owner),
			do("create guest", http.MethodPost, "/api/labels", map[string]any{"name": "X"}, guest),
			list(owner, ""))},
		{"create-team", seed, run(append([]step{
			do("create team plain", http.MethodPost, "/api/teams/"+f.public+"/labels", map[string]any{"name": "Team fresh", "color": "#abcdef"}, member),
			do("create team grouped", http.MethodPost, "/api/teams/"+f.public+"/labels", map[string]any{"name": "Team grouped", "groupId": "sw600-g-team"}, member),
			do("create team ws group", http.MethodPost, "/api/teams/"+f.public+"/labels", map[string]any{"name": "Team ws grouped", "groupId": "sw600-g-ws"}, owner),
			do("create team private group", http.MethodPost, "/api/teams/"+f.public+"/labels", map[string]any{"name": "X", "groupId": "sw600-g-private"}, owner),
			do("create private outsider", http.MethodPost, "/api/teams/"+f.private+"/labels", map[string]any{"name": "X"}, member),
			do("create private inside", http.MethodPost, "/api/teams/"+f.private+"/labels", map[string]any{"name": "Inside", "resourceType": "project"}, inside),
		}, lists...)...)},
		{"create-groups", seed, run(append([]step{
			do("create group", http.MethodPost, "/api/label-groups", map[string]any{"name": " Area ", "color": "#222222", "description": " d "}, owner),
			do("create team group", http.MethodPost, "/api/label-groups", map[string]any{"name": "Team area", "scope": f.public, "resourceType": "project"}, member),
			do("create missing team group", http.MethodPost, "/api/label-groups", map[string]any{"name": "X", "scope": "sw600-missing-team"}, owner),
			do("create bad group", http.MethodPost, "/api/label-groups", map[string]any{"name": "X", "resourceType": "cycle"}, owner),
			do("create blank group", http.MethodPost, "/api/label-groups", map[string]any{"name": ""}, owner),
		}, lists...)...)},
		{"update-workspace", seed, run(append([]step{
			do("rename", http.MethodPatch, "/api/labels/sw600-l-ws", map[string]any{"name": " Defect ", "color": "#000001", "description": "d"}, member),
			do("regroup", http.MethodPatch, "/api/labels/sw600-l-ws-plain", map[string]any{"groupId": "sw600-g-ws"}, owner),
			do("ungroup", http.MethodPatch, "/api/labels/sw600-l-ws", map[string]any{"groupId": ""}, owner),
			do("archive", http.MethodPatch, "/api/labels/sw600-l-ws-plain", map[string]any{"archivedAt": "2026-02-03T04:05:06Z"}, owner),
			do("unarchive", http.MethodPatch, "/api/labels/sw600-l-ws-archived", map[string]any{"archivedAt": ""}, owner),
			do("unchanged", http.MethodPatch, "/api/labels/sw600-l-ws-plain", map[string]any{"description": "No group"}, owner),
		}, lists...)...)},
		{"update-workspace-refused", seed, run(
			do("team label", http.MethodPatch, "/api/labels/sw600-l-public", map[string]any{"name": "X"}, owner),
			do("missing", http.MethodPatch, "/api/labels/sw600-missing", map[string]any{"name": "X"}, owner),
			do("team group", http.MethodPatch, "/api/labels/sw600-l-ws", map[string]any{"groupId": "sw600-g-team"}, owner),
			do("resource change keeps group", http.MethodPatch, "/api/labels/sw600-l-ws", map[string]any{"resourceType": "project"}, owner),
			do("bad resource", http.MethodPatch, "/api/labels/sw600-l-ws", map[string]any{"resourceType": "cycle"}, owner),
			do("guest", http.MethodPatch, "/api/labels/sw600-l-ws", map[string]any{"name": "X"}, guest),
			do("same id as a team", http.MethodPatch, "/api/labels/"+f.public, map[string]any{"name": "X"}, owner),
			list(owner, ""))},
		{"update-groups", seed, run(append([]step{
			do("rename group", http.MethodPatch, "/api/label-groups/sw600-g-ws", map[string]any{"name": " Kinds ", "color": "#333333", "description": "x"}, owner),
			do("archive group", http.MethodPatch, "/api/label-groups/sw600-g-team", map[string]any{"archivedAt": "2026-02-03T04:05:06Z"}, member),
			do("unarchive group", http.MethodPatch, "/api/label-groups/sw600-g-team", map[string]any{"archivedAt": ""}, member),
			do("unchanged group", http.MethodPatch, "/api/label-groups/sw600-g-ws", map[string]any{"name": "Kinds"}, owner),
			do("bad time", http.MethodPatch, "/api/label-groups/sw600-g-ws", map[string]any{"archivedAt": "yesterday"}, owner),
			do("missing group", http.MethodPatch, "/api/label-groups/sw600-missing", map[string]any{"name": "X"}, owner),
		}, lists...)...)},
		{"delete-workspace", seed, run(append([]step{
			do("delete used", http.MethodDelete, "/api/labels/sw600-l-ws", nil, member),
			do("delete project label", http.MethodDelete, "/api/labels/sw600-l-ws-project", nil, owner),
			do("delete again", http.MethodDelete, "/api/labels/sw600-l-ws", nil, owner),
			do("delete team label via workspace", http.MethodDelete, "/api/labels/sw600-l-public", nil, owner),
			do("delete missing", http.MethodDelete, "/api/labels/sw600-missing", nil, owner),
			do("delete guest", http.MethodDelete, "/api/labels/sw600-l-ws-plain", nil, guest),
		}, lists...)...)},
		{"delete-team", seed, run(append([]step{
			do("delete team label", http.MethodDelete, "/api/teams/"+f.public+"/labels/sw600-l-public", nil, member),
			do("delete other team label", http.MethodDelete, "/api/teams/"+f.public+"/labels/sw600-l-private", nil, owner),
			do("delete private outsider", http.MethodDelete, "/api/teams/"+f.private+"/labels/sw600-l-private", nil, member),
			do("delete private inside", http.MethodDelete, "/api/teams/"+f.private+"/labels/sw600-l-private", nil, inside),
		}, lists...)...)},
		{"delete-groups", seed, run(append([]step{
			do("delete workspace group", http.MethodDelete, "/api/label-groups/sw600-g-ws", nil, owner),
			do("delete team group via workspace", http.MethodDelete, "/api/label-groups/sw600-g-team", nil, owner),
			do("delete team group", http.MethodDelete, "/api/teams/"+f.public+"/label-groups/sw600-g-team", nil, member),
			do("delete empty group", http.MethodDelete, "/api/label-groups/sw600-g-project", nil, owner),
			do("delete missing group", http.MethodDelete, "/api/label-groups/sw600-missing", nil, owner),
		}, lists...)...)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			current = t
			outcomes := f.labelRoutesDifferential(t, test.name, labelRoutesFields, test.setup, test.call)
			t.Logf("legacy result %s", outcomes[0].Result)
		})
	}
}
