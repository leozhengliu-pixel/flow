package main

import (
	"context"
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

// SW602: PATCH /api/account/settings migrated from the COLD facade to the
// bounded personal settings writer (MutateUserSettings), served through the
// real cookie authentication (or development mode), the route mux and its
// temperature dispatch on the legacy store, a warm bounded view and a
// bounded view loaded from SQL. The reference is the legacy warm writer: a
// patch without username is the point writer (the stored row as its base,
// the written entry installed as written, which a load would normalize), one
// with username the metadata-only full writer. Responses, every owned row,
// events, webhooks, realtime publications and the installed userSettings,
// users and members entries (added, changed and removed) must agree; bounded,
// no request decodes the whole workspace.

var sw602Fields = []string{"userSettings", "users", "members"}

func sw602Handler(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/account/settings", s.getUserSettings)
	mux.HandleFunc("PATCH /api/account/settings", s.updateUserSettings)
	return s.withColdMetadataRouteMux(mux, s.authenticate(mux))
}

func sw602Do(s *server, method, body, workspace string, user domain.User) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "/api/account/settings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, "/api/account/settings", nil)
	}
	r.Header.Set("X-Workspace-Key", workspace)
	if s.authDisabled {
		// Development mode: the handler under the actor and viewer the
		// development middleware attaches (the workspace's viewer).
		r.SetPathValue("workspaceKey", workspace)
		ctx := store.ContextWithActor(context.WithValue(r.Context(), authUserContextKey{}, user), user)
		r, closeOutput := beginRequestResourceReads(r.WithContext(ctx))
		defer closeOutput()
		w := httptest.NewRecorder()
		if method == http.MethodPatch {
			s.updateUserSettings(w, r)
		} else {
			s.getUserSettings(w, r)
		}
		return w
	}
	if user.ID != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sw410Session(user)})
	}
	w := httptest.NewRecorder()
	sw602Handler(s).ServeHTTP(w, r)
	return w
}

// sw602Delta lists, per field, the installed entries a call added, changed
// or removed (by ID or map key), sorted.
func sw602Delta(t *testing.T, before, after map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	entries := func(raw json.RawMessage) map[string]string {
		var value any
		if len(raw) != 0 {
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
		}
		result := map[string]string{}
		add := func(key string, item any) {
			encoded, _ := json.Marshal(item)
			result[key] = string(encoded)
		}
		switch value := value.(type) {
		case []any:
			for index, item := range value {
				key := fmt.Sprint("#", index)
				if object, ok := item.(map[string]any); ok {
					if id, ok := object["id"].(string); ok {
						key = id
					} else if user, ok := object["user"].(map[string]any); ok {
						key, _ = user["id"].(string)
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
	for _, name := range sw602Fields {
		old, current := entries(before[name]), entries(after[name])
		for key, value := range current {
			if previous, ok := old[key]; !ok || previous != value {
				result[name] = append(result[name], "set "+key+"\t"+value)
			}
		}
		for key := range old {
			if _, ok := current[key]; !ok {
				result[name] = append(result[name], "removed "+key)
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

func (f scaleP21TeamCreateFixture) sw602Run(t *testing.T, source string, development bool, call func(*server) string) (sw410Outcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.service(t, source)
	service.authDisabled = development
	workspace := f.data.Workspace.URLKey
	got := sw410Outcome{}
	var mu sync.Mutex
	service.store.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
		}))
	})
	service.store.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		mu.Lock()
		defer mu.Unlock()
		got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
			"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
			"clientId": event.ClientID, "payload": event.Payload,
		}))
	})
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	before := scaleP21SW294Image(t, service.store, workspace, sw602Fields...)
	decodes := service.store.WorkspaceMetadataDecodes()
	got.Result = call(service)
	mu.Lock()
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	mu.Unlock()
	if service.store.BoundedMetadataEnabled() && service.store.WorkspaceMetadataDecodes() != decodes {
		t.Errorf("%s: account settings decoded the whole workspace %d times", source, service.store.WorkspaceMetadataDecodes()-decodes)
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
	if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("%s: retained metadata work %d", source, service.store.MetadataWorkUsedForTesting())
	}
	got.Installed = sw602Delta(t, before, scaleP21SW294Image(t, service.store, workspace, sw602Fields...))
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	got.Reloaded, _ = json.Marshal(scaleP21SW294Image(t, reloaded, workspace, sw602Fields...))
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	return got, started
}

func (f scaleP21TeamCreateFixture) sw602Differential(t *testing.T, name string, development bool, setup func(*testing.T), call func(*server) string) {
	t.Helper()
	prepare := f.sw294Prepare(setup)
	users := []domain.User{}
	for _, principal := range f.principals {
		users = append(users, principal.user)
	}
	f.scaleP21TriageWriterFixture.prepare = func(t *testing.T) {
		stamp := time.Now().UTC()
		for _, user := range users {
			f.exec(t, `DELETE FROM auth_sessions WHERE user_id=?`, user.ID)
			f.exec(t, `INSERT INTO auth_sessions(token_hash,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,?,?)`, secretHash(sw410Session(user)), user.ID,
				stamp.Add(24*time.Hour).Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano), stamp.Format(time.RFC3339Nano))
		}
		prepare(t)
	}
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	var normalized []string
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.sw602Run(t, source, development, call)
		text := sw410Normalize(t, got, started)
		normalized = append(normalized, text)
		if dir := os.Getenv("FLOW_SW602_DUMP"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, name+"-"+source+".json"), []byte(text), 0o600)
		}
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
		if source == "legacy" {
			t.Logf("legacy result %s", got.Result)
		}
	}
}

func TestScaleP21SW602AccountSettings(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	oldSlots := webhookDeliverySlots
	webhookDeliverySlots = make(chan struct{})
	t.Cleanup(func() { webhookDeliverySlots = oldSlots })
	workspace := f.data.Workspace.URLKey
	if len(f.principals) < 5 {
		t.Fatalf("principals %d", len(f.principals))
	}
	owner, admin, member, guest, inside := f.principals[0].user, f.principals[1].user, f.principals[2].user, f.principals[3].user, f.principals[4].user
	type step func(*server, *[]string)
	run := func(steps ...step) func(*server) string {
		return func(s *server) string {
			var lines []string
			for _, step := range steps {
				step(s, &lines)
			}
			return strings.Join(lines, "\n")
		}
	}
	patch := func(name string, body any, user domain.User) step {
		raw, _ := json.Marshal(body)
		return func(s *server, lines *[]string) {
			*lines = append(*lines, sw410Response(name, sw602Do(s, http.MethodPatch, string(raw), workspace, user)))
		}
	}
	get := func(user domain.User) step {
		return func(s *server, lines *[]string) {
			*lines = append(*lines, sw410Response("get "+user.Name, sw602Do(s, http.MethodGet, "", workspace, user)))
		}
	}
	settings := func(change func(map[string]domain.UserSettings)) func(*testing.T) {
		return func(t *testing.T) {
			f.mutate(t, "scale_p21_sw602.settings", func(data *domain.Bootstrap) {
				if data.UserSettings == nil {
					data.UserSettings = map[string]domain.UserSettings{}
				}
				change(data.UserSettings)
			})
		}
	}
	at := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	// A stored row a load normalizes: an old version without the git and
	// code review defaults, an unset pulse schedule.
	unnormalized := settings(func(all map[string]domain.UserSettings) {
		all[member.ID] = domain.UserSettings{UserID: member.ID, Language: "en-US", InterfaceTheme: "Light", UpdatedAt: at}
	})
	rowless := settings(func(all map[string]domain.UserSettings) { delete(all, admin.ID) })
	// A member outside the users directory without settings: no installed
	// entry at all.
	unlisted := func(t *testing.T) {
		f.mutate(t, "scale_p21_sw602.unlisted", func(data *domain.Bootstrap) {
			data.Users = slices.DeleteFunc(data.Users, func(user domain.User) bool { return user.ID == inside.ID })
			delete(data.UserSettings, inside.ID)
		})
	}
	handles := settings(func(all map[string]domain.UserSettings) {
		value := all[guest.ID]
		value.Username = "sw602-taken"
		all[guest.ID] = value
	})
	cases := []struct {
		name        string
		development bool
		setup       func(*testing.T)
		call        func(*server) string
	}{
		{"fields", false, nil, run(
			patch("theme", map[string]any{"interfaceTheme": "Dark", "fontSize": "Large", "timezone": " Europe/Paris ", "pulseSchedule": "weekly"}, owner),
			patch("coding tools", map[string]any{"enabledCodingTools": []string{"cursor", "cursor"}, "customDeepLinkUrlTemplate": " https://example.test/{issue} "}, member),
			patch("empty schedule", map[string]any{"pulseSchedule": ""}, guest),
			patch("ignored keys", map[string]any{"userId": "someone", "personalSettingsVersion": 9, "commitSigningKey": "x", "homeView": "Inbox"}, admin),
			get(owner), get(member), get(guest), get(admin))},
		{"unchanged-and-invalid", false, nil, run(
			patch("unchanged", map[string]any{"language": "en-US"}, owner),
			patch("bad schedule", map[string]any{"pulseSchedule": "hourly"}, owner),
			patch("bad timezone", map[string]any{"timezone": "Mars/Olympus"}, member),
			patch("bad tool", map[string]any{"enabledCodingTools": []string{"teleport"}}, member),
			patch("bad link", map[string]any{"customDeepLinkUrlTemplate": "ftp://x"}, member),
			patch("bad json", []int{1}, member),
			get(owner), get(member))},
		{"unnormalized-row", false, unnormalized, run(
			patch("unnormalized", map[string]any{"darkTheme": "Midnight"}, member),
			patch("again", map[string]any{"mergeStrategy": "Rebase"}, member),
			get(member))},
		{"rowless-entry", false, rowless, run(
			patch("rowless", map[string]any{"fontSize": "Small"}, admin),
			get(admin),
			patch("rowless again", map[string]any{"fontSize": "Large"}, admin),
			get(admin))},
		{"unlisted-member", false, unlisted, run(
			patch("unlisted", map[string]any{"emoticons": false}, inside),
			patch("unlisted again", map[string]any{"emoticons": true}, inside),
			get(inside))},
		{"username", false, handles, run(
			patch("set", map[string]any{"username": " SW602.Member "}, member),
			patch("same", map[string]any{"username": "sw602.member"}, member),
			patch("taken", map[string]any{"username": "sw602-taken"}, member),
			patch("derived taken", map[string]any{"username": member.Name}, admin),
			patch("invalid", map[string]any{"username": "no spaces allowed"}, member),
			patch("with fields", map[string]any{"username": "sw602-admin", "interfaceTheme": "Dark"}, admin),
			patch("clear", map[string]any{"username": ""}, member),
			get(member), get(admin))},
		{"username-unnormalized", false, unnormalized, run(
			patch("unnormalized handle", map[string]any{"username": "sw602-fresh"}, member),
			get(member))},
		{"development", true, nil, run(
			patch("dev theme", map[string]any{"interfaceTheme": "Dark"}, f.data.Viewer),
			patch("dev username", map[string]any{"username": "sw602-dev"}, f.data.Viewer),
			get(f.data.Viewer))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.sw602Differential(t, test.name, test.development, test.setup, test.call)
		})
	}
}
