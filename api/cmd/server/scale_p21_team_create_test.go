package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// POST /api/workspaces/{key}/teams through the bounded team creation writer,
// compared with the legacy team writer: top-level, private and sub-teams
// (inherited statuses on and off, copied settings, cycle settings and
// statuses), duplicate keys and names, hierarchy permissions (team owners
// from the membership tables or the stale root list), development members,
// inline statuses, a workspace with many teams, labels and statuses,
// cancellation and concurrent creators. Responses, every metadata row and
// its order, the root, memberships, audit rows, account state, domain events,
// webhooks, realtime and the installed image must agree byte for byte except
// generated IDs and clocks.

type scaleP21TeamCreateState struct {
	Metadata, Root, Memberships, Members, Audit, Account, Installations, Events [][]any
}

type scaleP21TeamCreateOutcome struct {
	Code                int
	Body                json.RawMessage
	State               scaleP21TeamCreateState
	Webhooks, Realtime  []json.RawMessage
	Installed, Reloaded json.RawMessage
}

type scaleP21TeamCreateFixture struct {
	scaleP21TeamMoveFixture
	baselineTeams map[string]bool
	installations map[string]bool
	auditSeq      int64
	auditHead     []any
	// wrap, when set, extends each creation request's context.
	wrap func(context.Context) context.Context
}

type scaleP21TeamCreateCase struct {
	name        string
	user        domain.User
	body        map[string]any
	development bool
	cancelled   bool
	agent       bool
	setup       func(*testing.T)
	code        int
	check       func(*testing.T, scaleP21TeamCreateOutcome)
}

func scaleP21TeamCreateSetup(t *testing.T) scaleP21TeamCreateFixture {
	t.Helper()
	f := scaleP21TeamCreateFixture{scaleP21TeamMoveFixture: scaleP21TeamMoveSetup(t), baselineTeams: map[string]bool{}, installations: map[string]bool{}}
	for _, row := range f.rows(t, `SELECT id FROM application_installations WHERE workspace_key=?`, f.data.Workspace.URLKey) {
		f.installations[fmt.Sprint(row[0])] = true
	}
	for _, row := range f.rows(t, `SELECT DISTINCT team_id FROM team_memberships WHERE workspace_id=?`, f.data.Workspace.ID) {
		f.baselineTeams[fmt.Sprint(row[0])] = true
	}
	if rows := f.rows(t, `SELECT COALESCE(MAX(seq),0) FROM audit_events WHERE workspace_id=?`, f.data.Workspace.ID); len(rows) == 1 {
		f.auditSeq = scaleP21TeamMoveInt(t, rows[0][0])
	}
	if rows := f.rows(t, `SELECT seq,hash,pruned_seq,pruned_hash FROM audit_chain_heads WHERE workspace_id=?`, f.data.Workspace.ID); len(rows) == 1 {
		f.auditHead = rows[0]
	}
	return f
}

// prepare seeds the team-move teams, removes what earlier runs created
// outside the workspace document (memberships, audit rows) and applies the
// case's setup.
func (f scaleP21TeamCreateFixture) prepare(setup func(*testing.T)) func(*testing.T) {
	seed := f.scaleP21TeamMoveFixture.prepare(scaleP21TeamMoveOptions{})
	return func(t *testing.T) {
		seed(t)
		workspaceID := f.data.Workspace.ID
		for _, row := range f.rows(t, `SELECT DISTINCT team_id FROM team_memberships WHERE workspace_id=?`, workspaceID) {
			if team := fmt.Sprint(row[0]); !f.baselineTeams[team] {
				f.exec(t, `DELETE FROM team_memberships WHERE workspace_id=? AND team_id=?`, workspaceID, team)
			}
		}
		for _, row := range f.rows(t, `SELECT id,user_id FROM application_installations WHERE workspace_key=?`, f.data.Workspace.URLKey) {
			if id, user := fmt.Sprint(row[0]), fmt.Sprint(row[1]); !f.installations[id] {
				f.exec(t, `DELETE FROM team_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, user)
				f.exec(t, `DELETE FROM workspace_memberships WHERE workspace_id=? AND user_id=?`, workspaceID, user)
				f.exec(t, `DELETE FROM application_installations WHERE id=?`, id)
			}
		}
		f.exec(t, `DELETE FROM audit_events WHERE workspace_id=? AND seq>?`, workspaceID, f.auditSeq)
		if f.auditHead != nil {
			f.exec(t, `UPDATE audit_chain_heads SET seq=?,hash=?,pruned_seq=?,pruned_hash=? WHERE workspace_id=?`, f.auditHead[0], f.auditHead[1], f.auditHead[2], f.auditHead[3], workspaceID)
		} else {
			f.exec(t, `DELETE FROM audit_chain_heads WHERE workspace_id=?`, workspaceID)
		}
		if setup != nil {
			setup(t)
		}
	}
}

func scaleP21TeamCreateDigest(rows [][]any) [][]any {
	for _, row := range rows {
		for index, value := range row {
			if text, ok := value.(string); ok && len(text) > 4096 {
				sum := sha256.Sum256([]byte(text))
				row[index] = "sha256:" + hex.EncodeToString(sum[:])
			}
		}
	}
	return rows
}

// scaleP21TeamCreateRoot lists the stored root's members in key order, each
// large value by its digest.
func scaleP21TeamCreateRoot(t *testing.T, rows [][]any) [][]any {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("workspace root rows %d", len(rows))
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(fmt.Sprint(rows[0][0])), &root); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var result [][]any
	for _, key := range keys {
		value := string(root[key])
		if len(value) > 1<<20 {
			sum := sha256.Sum256(root[key])
			value = "sha256:" + hex.EncodeToString(sum[:])
		}
		result = append(result, []any{key, value})
	}
	return result
}

func (f scaleP21TeamCreateFixture) state(t *testing.T) scaleP21TeamCreateState {
	t.Helper()
	workspace, workspaceID := f.data.Workspace.URLKey, f.data.Workspace.ID
	return scaleP21TeamCreateState{
		Metadata:      scaleP21TeamCreateDigest(f.rows(t, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,collection_order,record_key`, workspace)),
		Root:          scaleP21TeamCreateRoot(t, f.rows(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)),
		Memberships:   f.rows(t, `SELECT team_id,user_id,role,origin,joined_at FROM team_memberships WHERE workspace_id=? ORDER BY team_id,user_id`, workspaceID),
		Members:       f.rows(t, `SELECT user_id,role,status,joined_at FROM workspace_memberships WHERE workspace_id=? ORDER BY user_id`, workspaceID),
		Audit:         f.rows(t, `SELECT seq,actor_type,actor_id,user_id,actor,action,resource_type,resource_id,result,metadata FROM audit_events WHERE workspace_id=? ORDER BY seq`, workspaceID),
		Account:       f.rows(t, `SELECT last_workspace_key,viewer FROM account_state WHERE id=?`, 1),
		Installations: f.rows(t, `SELECT id,client_id,user_id,data FROM application_installations WHERE workspace_key=? ORDER BY id`, workspace),
		Events:        f.rows(t, `SELECT id,event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id`, workspaceID),
	}
}

var scaleP21TeamCreateInstalledFields = []string{"teams", "teamSettings", "cycleSettings", "states", "cycles", "members", "teamMembers", "users"}

// installed reads the fields a team creation writes from repo's installed
// image: the legacy snapshot or a bounded view's installed logical source.
func (f scaleP21TeamCreateFixture) installed(t *testing.T, repo *store.SQLiteStore) json.RawMessage {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	result := map[string]json.RawMessage{}
	if !repo.BoundedMetadataEnabled() {
		full, ok := repo.BootstrapFor(workspace)
		if !ok {
			t.Fatal("workspace missing")
		}
		raw, err := json.Marshal(full)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		for _, name := range scaleP21TeamCreateInstalledFields {
			result[name] = fields[name]
		}
	} else {
		read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
		if err != nil || !found {
			t.Fatal("installed read", err)
		}
		for _, name := range scaleP21TeamCreateInstalledFields {
			var raw strings.Builder
			if err := read.StreamFieldJSON(t.Context(), name, &raw); err != nil {
				t.Fatal(err)
			}
			result[name] = json.RawMessage(raw.String())
		}
		read.Close()
	}
	for name, value := range result {
		// Compare decoded values: an empty collection's spelling is not state.
		var decoded any
		if len(value) != 0 {
			if err := json.Unmarshal(value, &decoded); err != nil {
				t.Fatal(name, err)
			}
		}
		if items, ok := decoded.([]any); ok && len(items) == 0 || decoded == nil {
			result[name] = json.RawMessage("null")
			continue
		}
		if items, ok := decoded.(map[string]any); ok && len(items) == 0 {
			result[name] = json.RawMessage("null")
			continue
		}
		normalized, _ := json.Marshal(decoded)
		result[name] = normalized
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21TeamCreateEntries(t *testing.T, raw json.RawMessage) map[string][]string {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	nul := strings.NewReplacer(`\u0000`, "")
	result := map[string][]string{}
	for name, value := range fields {
		var entries []string
		add := func(key string, item any) {
			encoded, _ := json.Marshal(item)
			entries = append(entries, key+"\t"+nul.Replace(string(encoded)))
		}
		switch value := value.(type) {
		case []any:
			for _, item := range value {
				key := ""
				if object, ok := item.(map[string]any); ok {
					key, _ = object["id"].(string)
				}
				if key == "" {
					encoded, _ := json.Marshal(item)
					key = string(encoded)
				}
				add(key, item)
			}
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			for _, key := range keys {
				add(key, value[key])
			}
		}
		result[name] = entries
	}
	return result
}

// scaleP21TeamCreateInstalledDelta lists, per field, the installed entries a
// call added or changed, in installed order. The load normalizes some values
// (team and cycle settings defaults) that both writers install as written, so
// the deltas are compared between sources and SQL by the reload.
func scaleP21TeamCreateInstalledDelta(t *testing.T, before, after json.RawMessage) json.RawMessage {
	t.Helper()
	previous, current := scaleP21TeamCreateEntries(t, before), scaleP21TeamCreateEntries(t, after)
	result := map[string][]string{}
	for _, name := range scaleP21TeamCreateInstalledFields {
		old := map[string]string{}
		for _, entry := range previous[name] {
			key, value, _ := strings.Cut(entry, "\t")
			old[key] = value
		}
		for _, entry := range current[name] {
			key, value, _ := strings.Cut(entry, "\t")
			// EnsureBuiltinApplication's legacy OAuth transaction installs
			// the app user only into the full legacy snapshot (a separate,
			// still unmigrated store writer); SQL is compared by the rows.
			if name == "users" && strings.HasPrefix(key, "app_") {
				continue
			}
			if existing, found := old[key]; !found || existing != value {
				result[name] = append(result[name], entry)
			}
		}
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f scaleP21TeamCreateFixture) create(service *server, user domain.User, body map[string]any, development, cancelled bool, agent ...bool) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	workspace := f.data.Workspace.URLKey
	r := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+workspace+"/teams", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Workspace-Key", workspace)
	r.SetPathValue("workspaceKey", workspace)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	if !development {
		ctx = store.ContextWithActor(ctx, user)
	}
	if cancelled {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		cancel()
	}
	if f.wrap != nil {
		ctx = f.wrap(ctx)
	}
	r, closeOutput := beginRequestResourceReads(r.WithContext(ctx))
	defer closeOutput()
	// Concurrent callers share the service: write its flags only on change.
	if service.authDisabled != development {
		service.authDisabled = development
	}
	if enabled := len(agent) != 0 && agent[0]; service.agent.Enabled != enabled {
		service.agent.Enabled = enabled
	}
	w := httptest.NewRecorder()
	service.createTeam(w, r)
	return w
}

// service restores the fixture, then serves source from a store loaded from
// SQL, as a running server holds it: the legacy snapshot, a warm bounded view
// of it, or a bounded view loaded afresh. (The fixture's own store keeps the
// reset callback's values, which a load normalizes.)
func (f scaleP21TeamCreateFixture) service(t *testing.T, source string) *server {
	t.Helper()
	f.reset(t, "legacy")
	owner, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	switch source {
	case "legacy":
		return &server{store: owner}
	}
	view, err := owner.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if source == "warm" {
		// A view that already held the workspace takes the owner's generation.
		if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), owner, f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
	}
	return &server{store: view}
}

func (f scaleP21TeamCreateFixture) run(t *testing.T, source string, call func(*server) *httptest.ResponseRecorder) (scaleP21TeamCreateOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	service := f.service(t, source)
	got := scaleP21TeamCreateOutcome{}
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
	before := f.installed(t, service.store)
	decodes := service.store.WorkspaceMetadataDecodes()
	if w := call(service); w != nil {
		got.Code, got.Body = w.Code, json.RawMessage(w.Body.String())
	}
	service.store.SetWebhookSink(nil)
	service.store.SetRealtimeSink(nil)
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	got.State.Events = events
	if service.store.BoundedMetadataEnabled() && (service.store.WorkspaceMetadataDecodes() != decodes || service.store.MetadataWorkUsedForTesting() != 0) {
		t.Fatalf("%s: bounded team creation decoded full metadata (%d) or retained work (%d)", source,
			service.store.WorkspaceMetadataDecodes(), service.store.MetadataWorkUsedForTesting())
	}
	got.Installed = f.installed(t, service.store)
	reloaded, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	got.Reloaded = f.installed(t, reloaded)
	if err := reloaded.Close(); err != nil {
		t.Fatal(err)
	}
	got.Installed = scaleP21TeamCreateInstalledDelta(t, before, got.Installed)
	return got, started
}

func (f scaleP21TeamCreateFixture) differential(t *testing.T, name string, call func(*server) *httptest.ResponseRecorder) []scaleP21TeamCreateOutcome {
	t.Helper()
	var normalized []string
	var outcomes []scaleP21TeamCreateOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.run(t, source, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			if dir := os.Getenv("FLOW_TEAM_CREATE_DUMP"); dir != "" {
				_ = os.WriteFile(dir+"/"+name+"-legacy.json", []byte(normalized[0]), 0o600)
				_ = os.WriteFile(dir+"/"+name+"-"+source+".json", []byte(normalized[len(normalized)-1]), 0o600)
			}
			t.Fatalf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func (f scaleP21TeamCreateFixture) mutate(t *testing.T, event string, change func(*domain.Bootstrap)) {
	t.Helper()
	if err := f.repository.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, event, "", nil, func(data *domain.Bootstrap) error {
		change(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21TeamCreateTeam(t *testing.T, outcome scaleP21TeamCreateOutcome) domain.Team {
	t.Helper()
	var team domain.Team
	if err := json.Unmarshal(outcome.Body, &team); err != nil || team.ID == "" {
		t.Fatalf("created team body %s: %v", outcome.Body, err)
	}
	return team
}

func scaleP21TeamCreateRecords(t *testing.T, outcome scaleP21TeamCreateOutcome, field, prefix string) []string {
	t.Helper()
	var keys []string
	for _, row := range outcome.State.Metadata {
		if row[0] == field && strings.HasPrefix(fmt.Sprint(row[1]), prefix) {
			keys = append(keys, fmt.Sprint(row[1]))
		}
	}
	return keys
}

func TestScaleP21TeamCreateDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	owner, admin, member, guest := f.data.Viewer, f.principals[1].user, f.principals[2].user, f.principals[3].user
	workspaceID := f.data.Workspace.ID
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	teamOwner := func(user domain.User, team string) func(*testing.T) {
		return func(t *testing.T) {
			f.exec(t, `INSERT INTO team_memberships(workspace_id,team_id,user_id,role,joined_at) VALUES(?,?,?,?,?)`, workspaceID, team, user.ID, "owner", at.Format(time.RFC3339Nano))
		}
	}
	emptyRootOwner := func(team string) func(*testing.T) {
		return func(t *testing.T) {
			teamOwner(member, team)(t)
			// Empty the stored list itself (root value and any rows).
			workspace := f.data.Workspace.URLKey
			rows := f.rows(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
			var root map[string]json.RawMessage
			if err := json.Unmarshal([]byte(fmt.Sprint(rows[0][0])), &root); err != nil {
				t.Fatal(err)
			}
			root["teamMembers"] = json.RawMessage("[]")
			var shapes map[string]string
			if json.Unmarshal(root["_flowCollections"], &shapes) == nil && shapes != nil {
				delete(shapes, "teamMembers")
				root["_flowCollections"], _ = json.Marshal(shapes)
			}
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			f.exec(t, `UPDATE workspace_states SET data=? WHERE workspace_key=?`, raw, workspace)
			f.exec(t, `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field='teamMembers'`, workspace)
		}
	}
	created := func(code int) func(*testing.T, scaleP21TeamCreateOutcome) {
		return func(t *testing.T, outcome scaleP21TeamCreateOutcome) {
			if outcome.Code != code {
				t.Fatalf("legacy status %d, want %d: %s", outcome.Code, code, outcome.Body)
			}
		}
	}
	statesOf := func(want int) func(*testing.T, scaleP21TeamCreateOutcome) {
		return func(t *testing.T, outcome scaleP21TeamCreateOutcome) {
			team := scaleP21TeamCreateTeam(t, outcome)
			if got := scaleP21TeamCreateRecords(t, outcome, "states", team.ID); len(got) != want {
				t.Fatalf("copied statuses %v, want %d", got, want)
			}
		}
	}
	cases := []scaleP21TeamCreateCase{
		{name: "top-level", user: owner, body: map[string]any{"name": "Created", "key": "crt"}, check: statesOf(0)},
		{name: "top-level-member", user: member, body: map[string]any{"name": "Member team", "key": "MBR"}},
		{name: "top-level-guest", user: guest, body: map[string]any{"name": "Guest team", "key": "GST"}},
		{name: "private", user: admin, body: map[string]any{"name": "Private", "key": "PRV", "private": true}},
		{name: "presentation", user: owner, body: map[string]any{"name": "  Styled  ", "key": " sty ", "color": "#000000", "icon": "rocket", "timezone": "Asia/Shanghai"}},
		{name: "sub-team-inherits-statuses", user: owner, body: map[string]any{"name": "Sub", "key": "SUB", "parentTeamId": "tm-parent"}, check: statesOf(4)},
		{name: "sub-team-of-inheriting-team", user: owner, body: map[string]any{"name": "Sub sub", "key": "SSB", "parentTeamId": "tm-sub"}, check: statesOf(4)},
		{name: "sub-team-of-private", user: admin, body: map[string]any{"name": "Private sub", "key": "PSB", "parentTeamId": f.private, "private": true}},
		{name: "copy-without-inheritance", user: owner, body: map[string]any{"name": "Copy", "key": "CPY", "copyFromTeamId": "tm-parent"}, check: statesOf(4)},
		{name: "copy-tied-positions-without-settings", user: owner, body: map[string]any{"name": "Copy bare", "key": "CPB", "copyFromTeamId": "tm-bare"}, check: statesOf(2)},
		{name: "copy-shared-statuses", user: owner, body: map[string]any{"name": "Copy shared", "key": "CPS", "copyFromTeamId": f.public}},
		{name: "parent-wins-over-copy", user: owner, body: map[string]any{"name": "Both", "key": "BTH", "parentTeamId": "tm-triage", "copyFromTeamId": "tm-parent"}, check: statesOf(2)},
		{name: "copy-cycle-settings", user: owner, body: map[string]any{"name": "Cycles copied", "key": "CYC", "copyFromTeamId": "tm-parent"}, setup: func(t *testing.T) {
			f.mutate(t, "scale_p21.team_create_cycles", func(data *domain.Bootstrap) {
				data.CycleSettings["tm-parent"] = domain.CycleSettings{Enabled: true, AutoCreate: true, DurationWeeks: 3, UpcomingCount: 4}
			})
		}},
		{name: "default-cycles-started", user: owner, body: map[string]any{"name": "Cycles on", "key": "CYO"}, setup: func(t *testing.T) {
			enabled, weeks := true, 1
			domain.SetCreationDefaults(domain.CreationDefaults{Team: domain.TeamCreationDefaults{Cycles: domain.CycleCreationDefaults{Enabled: &enabled, DurationWeeks: &weeks}}})
			t.Cleanup(func() { domain.SetCreationDefaults(domain.CreationDefaults{}) })
		}, check: func(t *testing.T, outcome scaleP21TeamCreateOutcome) {
			team := scaleP21TeamCreateTeam(t, outcome)
			cycles := 0
			for _, row := range outcome.State.Metadata {
				if row[0] == "cycles" && strings.Contains(fmt.Sprint(row[3]), team.ID) {
					cycles++
				}
			}
			if cycles != 3 {
				t.Fatalf("started %d cycles, want 3", cycles)
			}
		}},
		{name: "duplicate-key", user: owner, body: map[string]any{"name": "Taken", "key": "tmp"}, code: http.StatusBadRequest},
		{name: "duplicate-name", user: owner, body: map[string]any{"name": "Move parent", "key": "MVP"}},
		{name: "missing-parent", user: owner, body: map[string]any{"name": "Orphan", "key": "ORP", "parentTeamId": "tm-missing"}, code: http.StatusBadRequest},
		{name: "missing-copy-source", user: owner, body: map[string]any{"name": "Orphan", "key": "ORC", "copyFromTeamId": "tm-missing"}, code: http.StatusBadRequest},
		{name: "retired-parent", user: owner, body: map[string]any{"name": "Retired", "key": "RTD", "parentTeamId": "tm-other"}, code: http.StatusBadRequest, setup: func(t *testing.T) {
			f.mutate(t, "scale_p21.team_create_retired", func(data *domain.Bootstrap) {
				data.Teams[domain.TeamIndex(data, "tm-other")].RetiredAt = &at
			})
		}},
		{name: "too-deep", user: owner, body: map[string]any{"name": "Deep", "key": "DEP", "parentTeamId": "tm-deep-5"}, code: http.StatusBadRequest, setup: func(t *testing.T) {
			f.mutate(t, "scale_p21.team_create_deep", func(data *domain.Bootstrap) {
				parent := ""
				for level := range 6 {
					id := fmt.Sprintf("tm-deep-%d", level)
					data.Teams = append(data.Teams, domain.Team{ID: id, Name: id, Key: fmt.Sprintf("DP%d", level)})
					data.TeamSettings[id] = domain.TeamSettings{TeamID: id, ParentTeamID: parent}
					parent = id
				}
			})
		}},
		{name: "member-sub-team-refused", user: member, body: map[string]any{"name": "Refused", "key": "RFS", "parentTeamId": "tm-parent"}, code: http.StatusForbidden},
		{name: "guest-sub-team-refused", user: guest, body: map[string]any{"name": "Refused", "key": "RFG", "parentTeamId": f.public}, code: http.StatusForbidden},
		// The stored root list (not the membership tables) decides while it
		// is not empty; an empty one is filled from the tables and written.
		{name: "team-owner-hidden-by-root-list", user: member, body: map[string]any{"name": "Owned", "key": "OWN", "parentTeamId": "tm-parent"}, code: http.StatusForbidden, setup: teamOwner(member, "tm-parent")},
		{name: "empty-root-team-owner", user: member, body: map[string]any{"name": "Owned", "key": "OWE", "parentTeamId": "tm-parent"}, setup: emptyRootOwner("tm-parent"), code: -1},
		{name: "empty-root-team-owner-of-ancestor", user: member, body: map[string]any{"name": "Owned", "key": "OWA", "parentTeamId": "tm-sub"}, setup: emptyRootOwner("tm-parent"), code: -1},
		{name: "empty-root-member-refused", user: member, body: map[string]any{"name": "Refused", "key": "OWR", "parentTeamId": "tm-triage"}, code: http.StatusForbidden, setup: emptyRootOwner("tm-parent")},
		{name: "stale-root-team-owner", user: member, body: map[string]any{"name": "Root owner", "key": "ROO", "parentTeamId": "tm-parent"}, setup: func(t *testing.T) {
			f.mutate(t, "scale_p21.team_create_root_members", func(data *domain.Bootstrap) {
				data.TeamMembers = []domain.TeamMember{
					{TeamID: "tm-parent", UserID: guest.ID, Role: "owner", JoinedAt: at},
					{TeamID: "tm-parent", UserID: member.ID, Role: "owner", JoinedAt: at},
					{TeamID: "tm-parent", UserID: member.ID, Role: "member", JoinedAt: at},
				}
			})
		}},
		{name: "stale-root-other-members", user: member, body: map[string]any{"name": "Root others", "key": "ROT", "parentTeamId": "tm-parent"}, code: http.StatusForbidden, setup: func(t *testing.T) {
			teamOwner(member, "tm-parent")(t)
			f.mutate(t, "scale_p21.team_create_root_members", func(data *domain.Bootstrap) {
				data.TeamMembers = []domain.TeamMember{{TeamID: "tm-parent", UserID: guest.ID, Role: "owner", JoinedAt: at}}
			})
		}},
		{name: "inline-statuses", user: owner, body: map[string]any{"name": "Inline", "key": "INL", "parentTeamId": "tm-parent"}, setup: func(t *testing.T) {
			f.mutate(t, "scale_p21.team_create_inline", func(data *domain.Bootstrap) {
				data.States = append(data.States, domain.WorkflowState{ID: "tmp-done", TeamID: "tm-other", Name: "Duplicate", Type: "completed", Position: 9})
			})
		}, check: func(t *testing.T, outcome scaleP21TeamCreateOutcome) {
			// Inline statuses have no rows: the root holds them whole.
			team := scaleP21TeamCreateTeam(t, outcome)
			var delta map[string][]string
			if err := json.Unmarshal(outcome.Installed, &delta); err != nil {
				t.Fatal(err)
			}
			copied := 0
			for _, entry := range delta["states"] {
				if strings.HasPrefix(entry, team.ID+"_") {
					copied++
				}
			}
			if copied != 4 || len(scaleP21TeamCreateRecords(t, outcome, "states", team.ID)) != 0 {
				t.Fatalf("inline copied statuses %d", copied)
			}
		}},
		{name: "many-teams-labels-statuses", user: owner, body: map[string]any{"name": "Large", "key": "LRG", "parentTeamId": "tm-bulk-150"}, setup: func(t *testing.T) {
			f.mutate(t, "scale_p21.team_create_bulk", func(data *domain.Bootstrap) {
				for index := range 300 {
					id := fmt.Sprintf("tm-bulk-%d", index)
					data.Teams = append(data.Teams, domain.Team{ID: id, Name: "Bulk " + id, Key: fmt.Sprintf("BK%d", index)})
					data.TeamSettings[id] = domain.TeamSettings{TeamID: id, DefaultStateID: id + "-todo", EstimateType: "fibonacci"}
					for position, kind := range []string{"backlog", "unstarted", "started", "completed"} {
						data.States = append(data.States, domain.WorkflowState{ID: fmt.Sprintf("%s-%s", id, kind), TeamID: id, Name: kind, Type: kind, Position: float64(4 - position)})
					}
					data.States[len(data.States)-3].ID = id + "-todo"
					data.Labels = append(data.Labels, domain.IssueLabel{ID: id + "-label", Name: "Label " + id, Scope: id, CreatedAt: at})
				}
			})
		}, check: statesOf(4)},
		{name: "builtin-agent-installed", user: owner, agent: true, body: map[string]any{"name": "Agent team", "key": "AGT"}},
		{name: "builtin-agent-extended", user: owner, agent: true, body: map[string]any{"name": "Agent team", "key": "AGX"}, setup: func(t *testing.T) {
			if _, err := f.repository.EnsureBuiltinApplication(t.Context(), f.data.Workspace.URLKey, true); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "development", user: owner, development: true, body: map[string]any{"name": "Development", "key": "DEV"}},
		{name: "development-sub-team", user: owner, development: true, body: map[string]any{"name": "Development sub", "key": "DVS", "parentTeamId": "tm-parent"}},
		{name: "overlong-key", user: owner, body: map[string]any{"name": "Bad", "key": strings.Repeat("K", 300)}, code: http.StatusBadRequest},
		{name: "missing-name", user: owner, body: map[string]any{"key": "NON"}, code: http.StatusBadRequest},
		{name: "cancelled", user: owner, cancelled: true, body: map[string]any{"name": "Cancelled", "key": "CNL"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			f.scaleP21TriageWriterFixture.prepare = f.prepare(test.setup)
			defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
			outcomes := f.differential(t, test.name, func(service *server) *httptest.ResponseRecorder {
				return f.create(service, test.user, test.body, test.development, test.cancelled, test.agent)
			})
			legacy := outcomes[0]
			t.Logf("legacy status %d", legacy.Code)
			code := test.code
			if code == 0 && !test.cancelled {
				code = http.StatusCreated
			}
			if code > 0 {
				created(code)(t, legacy)
			}
			if legacy.Code != http.StatusCreated && len(legacy.State.Events) != 0 {
				t.Fatalf("legacy refusal published %d events", len(legacy.State.Events))
			}
			if test.check != nil {
				test.check(t, legacy)
			}
		})
	}
}

func TestScaleP21TeamCreateConcurrentDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	f.scaleP21TriageWriterFixture.prepare = f.prepare(nil)
	for _, test := range []struct {
		name  string
		keys  []string
		codes []int
	}{
		{"distinct-keys", []string{"CA1", "CA2", "CA3", "CA4"}, []int{http.StatusCreated, http.StatusCreated, http.StatusCreated, http.StatusCreated}},
		{"same-key", []string{"CSK", "CSK", "csk"}, []int{http.StatusCreated, http.StatusBadRequest, http.StatusBadRequest}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var expected string
			for _, source := range []string{"legacy", "warm", "reloaded"} {
				var codes []int
				got, started := f.run(t, source, func(service *server) *httptest.ResponseRecorder {
					var group sync.WaitGroup
					var mu sync.Mutex
					for _, key := range test.keys {
						group.Add(1)
						create := func() {
							defer group.Done()
							// Copies (not sub-teams): the creator's inherited ancestor
							// membership would go to whichever creation commits first.
							body := map[string]any{"name": "Concurrent " + strings.ToUpper(key), "key": key, "copyFromTeamId": "tm-parent"}
							// No client retry: every first answer is final. A
							// committed creation answers 201 even when its
							// creator membership lost a write race (SW280).
							w := f.create(service, f.data.Viewer, body, false, false)
							if w.Code != http.StatusCreated {
								t.Logf("%s %s: %d %s", source, key, w.Code, w.Body.String())
							}
							mu.Lock()
							codes = append(codes, w.Code)
							mu.Unlock()
						}
						go create()
					}
					group.Wait()
					return nil
				})
				slices.Sort(codes)
				if !slices.Equal(codes, test.codes) {
					t.Fatalf("%s %s codes %v, want %v", source, test.name, codes, test.codes)
				}
				// Concurrent creators commit in any order: created rows keep
				// their content but not their relative position or sequence.
				for _, row := range got.State.Metadata {
					if scaleP21TriageStamp.MatchString(fmt.Sprint(row[1])) {
						row[2] = "<order>"
					}
				}
				for _, row := range got.State.Audit {
					row[0] = "<seq>"
				}
				for _, rows := range []*[][]any{&got.State.Metadata, &got.State.Memberships, &got.State.Audit, &got.State.Events} {
					slices.SortFunc(*rows, func(a, b []any) int {
						left, _ := json.Marshal(a)
						right, _ := json.Marshal(b)
						return strings.Compare(scaleP21IssueUpdateNormalize(t, json.RawMessage(left), started), scaleP21IssueUpdateNormalize(t, json.RawMessage(right), started))
					})
				}
				got.Webhooks = scaleP21IssueUpdateSorted(t, got.Webhooks, started)
				got.Realtime = scaleP21IssueUpdateSorted(t, got.Realtime, started)
				// The installed images were each compared with their SQL reload.
				got.Installed, got.Reloaded = nil, nil
				normalized := scaleP21IssueUpdateNormalize(t, map[string]any{"codes": codes, "outcome": got}, started)
				if expected != "" && expected != normalized {
					if dir := os.Getenv("FLOW_TEAM_CREATE_DUMP"); dir != "" {
						_ = os.WriteFile(dir+"/concurrent-"+test.name+"-expected.json", []byte(expected), 0o600)
						_ = os.WriteFile(dir+"/concurrent-"+test.name+"-"+source+".json", []byte(normalized), 0o600)
					}
					t.Fatalf("%s concurrent creation changed: %s", source, scaleP21M2Diff(expected, normalized))
				}
				expected = normalized
			}
		})
	}
}

type scaleP21TeamCreateCoordinator struct{ mu *sync.Mutex }

func (c scaleP21TeamCreateCoordinator) WithWorkspaceLock(_ context.Context, _ string, run func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return run()
}

// Two servers share the database under a workspace lock. Each creation must
// first take the other server's committed teams, statuses and keys (peer
// refresh by metadata revision), as the legacy writer's copy refresh does.
func TestScaleP21TeamCreatePeerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	f.scaleP21TriageWriterFixture.prepare = f.prepare(nil)
	var expected string
	for _, bounded := range []bool{false, true} {
		f.reset(t, "legacy")
		lock := &sync.Mutex{}
		var peers [2]*server
		for index := range peers {
			owner, err := store.OpenDatabase(f.reader.config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = owner.Close() })
			repo := owner
			if bounded {
				if repo, err = owner.BoundedMetadataViewForTesting(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			repo.SetWorkspaceCoordinator(scaleP21TeamCreateCoordinator{lock})
			peers[index] = &server{store: repo}
		}
		prior := map[any]bool{}
		for _, event := range f.state(t).Events {
			prior[event[0]] = true
		}
		decodes := [2]int64{peers[0].store.WorkspaceMetadataDecodes(), peers[1].store.WorkspaceMetadataDecodes()}
		started := time.Now().UTC()
		var responses []any
		step := func(peer int, body map[string]any) domain.Team {
			w := f.create(peers[peer], f.data.Viewer, body, false, false)
			responses = append(responses, []any{peer, w.Code, json.RawMessage(w.Body.String())})
			var team domain.Team
			_ = json.Unmarshal(w.Body.Bytes(), &team)
			return team
		}
		first := step(0, map[string]any{"name": "Peer A", "key": "PRA", "copyFromTeamId": "tm-parent"})
		second := step(1, map[string]any{"name": "Peer B", "key": "PRB", "parentTeamId": first.ID})
		step(1, map[string]any{"name": "Peer duplicate", "key": "pra"})
		step(0, map[string]any{"name": "Peer C", "key": "PRC", "parentTeamId": second.ID})
		step(0, map[string]any{"name": "Peer duplicate", "key": "prb"})
		step(1, map[string]any{"name": "Peer D", "key": "PRD", "parentTeamId": first.ID})
		state := f.state(t)
		var events [][]any
		for _, event := range state.Events {
			if !prior[event[0]] {
				events = append(events, event[1:])
			}
		}
		state.Events = events
		for index, peer := range peers {
			if peer.store.BoundedMetadataEnabled() && (peer.store.WorkspaceMetadataDecodes() != decodes[index] || peer.store.MetadataWorkUsedForTesting() != 0) {
				t.Fatalf("bounded peer %d decoded full metadata or retained work", index)
			}
		}
		normalized := scaleP21IssueUpdateNormalize(t, map[string]any{"responses": responses, "state": state}, started)
		if expected != "" && expected != normalized {
			t.Fatalf("bounded peers changed: %s", scaleP21M2Diff(expected, normalized))
		}
		expected = normalized
	}
}
