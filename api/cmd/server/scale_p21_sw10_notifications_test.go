package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW10Fixture struct {
	scaleP21M2Fixture
	reader *scaleP04Database
}

func scaleP21SW10Setup(t *testing.T) scaleP21SW10Fixture {
	t.Helper()
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21M2Setup(t, repo)
	reader := &scaleP04Database{config: config}
	t.Cleanup(func() {
		if reader.reader != nil {
			_ = reader.reader.Close()
		}
	})
	return scaleP21SW10Fixture{f, reader}
}

func scaleP21SW10JSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW10Response(t *testing.T, w *httptest.ResponseRecorder, defaultClock bool) any {
	t.Helper()
	if w.Body.Len() == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if defaultClock {
		if object, ok := value.(map[string]any); ok {
			object["updatedAt"] = "generated-default-time"
		}
	}
	return value
}

func scaleP21SW10Rows(t *testing.T, repo *store.SQLiteStore, workspace string, keys ...store.RecordKey) any {
	t.Helper()
	result := []any{}
	for _, projection := range []string{"detail", "summary"} {
		batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, keys, projection)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range batch.Items {
			if projection == "detail" && item.Omitted {
				t.Fatalf("small owned record was omitted: %+v", item.Key)
			}
			result = append(result, []any{projection, item.Key, item.Missing, item.Omitted, item.Data})
		}
	}
	return scaleP21SW10JSON(t, result)
}

func scaleP21SW10Events(t *testing.T, events []domain.DomainEvent, realtime []domain.RealtimeEvent) any {
	t.Helper()
	result := []any{}
	for _, event := range events {
		result = append(result, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
	}
	for _, event := range realtime {
		result = append(result, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
	}
	return scaleP21SW10JSON(t, result)
}

func (f scaleP21SW10Fixture) issueRowsHash(t *testing.T) [32]byte {
	t.Helper()
	hash := sha256.New()
	rows := f.reader.query(t, `SELECT id,version,collection_order,data FROM issue_records WHERE workspace_key=? ORDER BY id`, f.data.Workspace.URLKey)
	count := 0
	for rows.Next() {
		var id string
		var version, order int64
		var raw []byte
		if err := rows.Scan(&id, &version, &order, &raw); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s:%d:%d:", id, version, order)
		_, _ = hash.Write(raw)
		count++
	}
	err := rows.Err()
	rows.Close()
	if err != nil || count == 0 {
		t.Fatal("owned issue rows are missing", err)
	}
	rows = f.reader.query(t, `SELECT issue_id,label_id FROM issue_label_records WHERE workspace_key=? ORDER BY issue_id,label_id`, f.data.Workspace.URLKey)
	for rows.Next() {
		var issueID, labelID string
		if err := rows.Scan(&issueID, &labelID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s:%s:", issueID, labelID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

func scaleP21SW10WriteRevocation(t *testing.T, f scaleP21SW10Fixture, handler func(*server, http.ResponseWriter, *http.Request), request func(domain.User, *domain.APIKey) *http.Request) {
	t.Helper()
	member := f.principals[2].user
	queued := []*http.Request{}
	for _, credential := range scaleP21M3Credentials() {
		queued = append(queued, request(member, credential.key))
	}
	if err := f.repository.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.repository.ResumeMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
	}()
	for _, phase := range []string{"queued", "replay"} {
		for _, r := range queued {
			var responses []any
			for _, source := range []*store.SQLiteStore{f.repository, f.probe} {
				w := httptest.NewRecorder()
				scaleP21M3Call(&server{store: source}, w, r, handler)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s write crossed revoked membership: %d %s", phase, w.Code, w.Body.String())
				}
				responses = append(responses, scaleP21SW10Response(t, w, false))
			}
			if !reflect.DeepEqual(responses[0], responses[1]) {
				t.Fatal("revoked write boundary changed")
			}
		}
	}
}

func TestScaleP21SW10PreferencesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	otherID := idgen.New("sw10-preference-other-")
	preferences := map[string]domain.NotificationPreferences{
		otherID: {UserID: otherID, EmailFormat: "digest", DesktopPermission: strings.Repeat("unrelated preference ", 20000), UpdatedAt: now},
	}
	for _, principal := range f.principals {
		preferences[principal.user.ID] = domain.NotificationPreferences{
			UserID: principal.user.ID, EmailFormat: "immediate", DesktopPermission: "denied", UpdatedAt: now,
			Inbox: domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"mentions": true, "private-reference-count": false}},
			Email: domain.NotificationChannelPreferences{Enabled: false, Categories: nil},
			Desktop: domain.NotificationChannelPreferences{
				Enabled: true, Categories: map[string]bool{},
			},
			Chat: &domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"comments": true}},
		}
	}
	for range 51 {
		id := idgen.New("sw10-retained-preferences-")
		preferences[id] = domain.NotificationPreferences{UserID: id, EmailFormat: "digest", UpdatedAt: now}
	}
	reset := func(missing string, oversized bool) {
		t.Helper()
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		ctx := store.WithMetadataFields(store.ContextWithActor(t.Context(), f.data.Viewer), "notificationPreferences")
		if err := repo.MutateWorkspace(ctx, workspace, "notification_preferences.updated", f.data.Viewer.ID, nil, func(data *domain.Bootstrap) error {
			data.NotificationPreferences = map[string]domain.NotificationPreferences{}
			for id, preference := range preferences {
				if id != missing {
					data.NotificationPreferences[id] = preference
				}
			}
			if oversized {
				preference := data.NotificationPreferences[f.data.Viewer.ID]
				preference.DesktopPermission = strings.Repeat("large owned preference ", 20000)
				data.NotificationPreferences[f.data.Viewer.ID] = preference
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reset("", false)
	otherHash := scaleP21SW6RecordHash(t, repo, workspace, "notificationPreferences", otherID)
	projectHash := scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated")
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					absent := ""
					if missing {
						absent = user.ID
					}
					reset(absent, false)
					rows := scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "notificationPreferences", ID: user.ID})
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					(&server{store: source}).getNotificationPreferences(w, r)
					if w.Code != http.StatusOK {
						t.Fatalf("preferences status=%d body=%s", w.Code, w.Body.String())
					}
					response := scaleP21SW10Response(t, w, missing)
					expected := preferences[user.ID]
					if missing {
						expected = defaultPreferences(user.ID)
						expected.UpdatedAt = time.Time{}
						encoded := scaleP21SW10JSON(t, expected).(map[string]any)
						encoded["updatedAt"] = "generated-default-time"
						if !reflect.DeepEqual(response, encoded) {
							t.Fatal("process notification defaults changed")
						}
					} else if !reflect.DeepEqual(response, scaleP21SW10JSON(t, expected)) {
						t.Fatal("stored preference values or nil/empty shapes changed")
					}
					if !reflect.DeepEqual(rows, scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "notificationPreferences", ID: user.ID})) {
						t.Fatal("preference GET changed its canonical record")
					}
					results = append(results, response)
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("preference differential: %s", scaleP21M2Diff(results[0], results[1]))
				}
			})
		}
	})
	for _, missing := range []bool{false, true} {
		var results []any
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			absent := ""
			if missing {
				absent = f.data.Viewer.ID
			}
			reset(absent, false)
			r := f.request(f.principals[len(f.principals)-1].user, &domain.APIKey{TeamRestriction: "selected", TeamIDs: []string{}}, nil)
			w := httptest.NewRecorder()
			(&server{store: source, authDisabled: true}).getNotificationPreferences(w, r)
			var actual domain.NotificationPreferences
			if err := json.Unmarshal(w.Body.Bytes(), &actual); err != nil || actual.UserID != f.data.Viewer.ID {
				t.Fatal("development-mode read did not retain stored Viewer identity", err)
			}
			results = append(results, []any{w.Code, scaleP21SW10Response(t, w, missing)})
		}
		if !reflect.DeepEqual(results[0], results[1]) {
			t.Fatal("development-mode preference differential")
		}
	}
	reset("", false)
	member := f.principals[2].user
	access, err := f.probe.ResolveAccess(t.Context(), workspace, member.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	queued := []*http.Request{}
	for _, credential := range scaleP21M3Credentials() {
		queued = append(queued, f.request(member, credential.key, nil))
	}
	if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.probe.ValidateAccess(t.Context(), access); err == nil {
		t.Fatal("retained read authority survived revocation")
	}
	for _, phase := range []string{"queued", "replay"} {
		for _, r := range queued {
			w := httptest.NewRecorder()
			(&server{store: f.probe}).getNotificationPreferences(w, r)
			if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "emailFormat") {
				t.Fatalf("%s read disclosed revoked preferences: %d %s", phase, w.Code, w.Body.String())
			}
		}
	}
	if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW10 other workspace", idgen.New("sw10-foreign-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
	for _, key := range []string{foreign.Workspace.URLKey, workspace + "-missing"} {
		var responses []any
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r := f.request(member, nil, nil)
			r.Header.Set("X-Workspace-Key", key)
			r.SetPathValue("workspaceKey", key)
			r.SetPathValue("workspace", key)
			w := httptest.NewRecorder()
			scaleP21M3Call(&server{store: source}, w, r, (*server).getNotificationPreferences)
			if w.Code != http.StatusForbidden {
				t.Fatal("foreign or missing workspace read crossed the live boundary")
			}
			responses = append(responses, []any{w.Code, scaleP21SW10Response(t, w, false)})
		}
		if !reflect.DeepEqual(responses[0], responses[1]) {
			t.Fatal("missing-workspace authorization boundary changed")
		}
	}
	r := f.request(f.data.Viewer, nil, nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	(&server{store: f.probe}).getNotificationPreferences(w, r.WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatal("cancelled preference read wrote a response body")
	}
	reset("", true)
	before := scaleP21SW6RecordHash(t, repo, workspace, "notificationPreferences", f.data.Viewer.ID)
	w = httptest.NewRecorder()
	(&server{store: f.probe}).getNotificationPreferences(w, f.request(f.data.Viewer, nil, nil))
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "emailFormat") {
		t.Fatal("oversized owned preference became a default or truncated success")
	}
	if before != scaleP21SW6RecordHash(t, repo, workspace, "notificationPreferences", f.data.Viewer.ID) {
		t.Fatal("budget failure changed canonical preferences")
	}
	reset("", false)
	w = httptest.NewRecorder()
	(&server{store: f.probe}).getNotificationPreferences(w, f.request(f.data.Viewer, nil, nil))
	if w.Code != http.StatusOK {
		t.Fatal("budget failure prevented a later preference read")
	}
	if scaleP21SW6RecordHash(t, repo, workspace, "notificationPreferences", otherID) != otherHash ||
		scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated") != projectHash ||
		f.probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("preference reads changed unrelated bytes or decoded full metadata")
	}
}

func TestScaleP21SW10TeamLabelDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	targetID, otherID := idgen.New("sw10-label-"), idgen.New("sw10-other-label-")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	target := domain.IssueLabel{ID: targetID, Scope: f.public, Name: "SW10 before", Color: "#112233", ResourceType: "issue", CreatorID: f.data.Viewer.ID, IssueCount: 73, CreatedAt: now, LastAppliedAt: &now}
	labels := []domain.IssueLabel{target, {ID: otherID, Scope: f.public, Name: "Unrelated", ResourceType: "issue", Description: strings.Repeat("unrelated label ", 25000), CreatedAt: now}}
	for range 51 {
		labels = append(labels, domain.IssueLabel{ID: idgen.New("sw10-retained-label-"), Scope: f.public, Name: "Retained", ResourceType: "issue", CreatedAt: now})
	}
	reset := func() {
		t.Helper()
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "labels"), workspace, "issue_label.updated", targetID, nil, func(data *domain.Bootstrap) error {
			data.Labels = labels
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "SW10 linked label", "teamId": f.public, "labelIds": []string{targetID}}, workspace, http.StatusCreated)
	issueHash := f.issueRowsHash(t)
	otherHash := scaleP21SW6RecordHash(t, repo, workspace, "labels", otherID)
	projectHash := scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated")
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		for _, spec := range []struct {
			name      string
			body      any
			id        string
			team      string
			code      int
			nameAfter string
			events    int
		}{
			{"changed", map[string]any{"name": "SW10 after", "description": "Updated", "color": "#445566", "archivedAt": now.Format(time.RFC3339)}, targetID, f.public, http.StatusOK, "SW10 after", 1},
			{"noop", map[string]any{}, targetID, f.public, http.StatusOK, target.Name, 0},
			{"missing", map[string]any{"name": "No alias"}, targetID + "-old-slug", f.public, http.StatusNotFound, target.Name, 0},
			{"foreign-scope", map[string]any{"name": "No foreign write"}, targetID, f.public + "-other", http.StatusNotFound, target.Name, 0},
			{"invalid-name", map[string]any{"name": " "}, targetID, f.public, http.StatusBadRequest, target.Name, 0},
			{"invalid-clock", map[string]any{"archivedAt": "not-a-clock"}, targetID, f.public, http.StatusBadRequest, target.Name, 0},
		} {
			t.Run(spec.name, func(t *testing.T) {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset()
					var events []domain.DomainEvent
					var realtime []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
					r := f.request(user, key, spec.body)
					r.SetPathValue("id", spec.team)
					r.SetPathValue("labelId", spec.id)
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, r, (*server).updateTeamLabel)
					if w.Code != spec.code || len(events) != spec.events || len(realtime) != spec.events {
						t.Fatalf("label status=%d events=%d/%d body=%s", w.Code, len(events), len(realtime), w.Body.String())
					}
					current, err := repo.IssueRecord(t.Context(), workspace, issue.ID)
					if err != nil || len(current.Labels) != 1 || current.Labels[0].Name != spec.nameAfter {
						t.Fatalf("issue read lost resolved label display: %+v err=%v", current.Labels, err)
					}
					if f.issueRowsHash(t) != issueHash {
						t.Fatal("metadata-only label edit rewrote issue SQL rows or label links")
					}
					results = append(results, []any{
						w.Code, scaleP21SW10Response(t, w, false),
						scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "labels", ID: targetID}, store.RecordKey{Kind: "users", ID: user.ID}),
						scaleP21SW10Events(t, events, realtime),
					})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("label differential: %s", scaleP21M2Diff(results[0], results[1]))
				}
			})
		}
	})
	reset()
	before := scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "labels", ID: targetID})
	r := f.request(f.data.Viewer, nil, map[string]any{"description": strings.Repeat("oversized target ", 20000)})
	r.SetPathValue("id", f.public)
	r.SetPathValue("labelId", targetID)
	w := httptest.NewRecorder()
	(&server{store: f.probe}).updateTeamLabel(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
		!reflect.DeepEqual(before, scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "labels", ID: targetID})) {
		t.Fatal("oversized label output was not an explicit atomic budget error")
	}
	for _, source := range []*store.SQLiteStore{repo, f.probe} {
		r := f.request(f.data.Viewer, nil, map[string]any{"name": "Cancelled"})
		r.SetPathValue("id", f.public)
		r.SetPathValue("labelId", targetID)
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		w := httptest.NewRecorder()
		(&server{store: source}).updateTeamLabel(w, r.WithContext(ctx))
		if w.Code != http.StatusInternalServerError || !reflect.DeepEqual(before, scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "labels", ID: targetID})) {
			t.Fatal("cancelled label edit changed its target")
		}
	}
	scaleP21SW10WriteRevocation(t, f, (*server).updateTeamLabel, func(user domain.User, key *domain.APIKey) *http.Request {
		r := f.request(user, key, map[string]any{"name": "Revoked"})
		r.SetPathValue("id", f.public)
		r.SetPathValue("labelId", targetID)
		return r
	})
	if f.issueRowsHash(t) != issueHash || scaleP21SW6RecordHash(t, repo, workspace, "labels", otherID) != otherHash ||
		scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated") != projectHash ||
		f.probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("label mutation changed unrelated bytes or decoded full metadata")
	}
}

func TestScaleP21SW10IssueTemplateDeletionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	targetID, otherID := idgen.New("sw10-template-"), idgen.New("sw10-other-template-")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	target := domain.IssueTemplate{ID: targetID, TeamID: f.public, Name: "Owned", Body: "Template body", LabelIDs: []string{}, Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now}
	other := domain.IssueTemplate{ID: otherID, TeamID: f.public, Name: "Unrelated", Body: strings.Repeat("unrelated template ", 20000), LabelIDs: []string{}, Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now}
	reset := func(template domain.IssueTemplate) {
		t.Helper()
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "issueTemplates"), workspace, "issue_template.updated", targetID, nil, func(data *domain.Bootstrap) error {
			data.IssueTemplates = []domain.IssueTemplate{template, other}
			for range 51 {
				data.IssueTemplates = append(data.IssueTemplates, domain.IssueTemplate{ID: idgen.New("sw10-retained-template-"), TeamID: f.public, Name: "Retained", LabelIDs: []string{}, Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reset(target)
	otherHash := scaleP21SW6RecordHash(t, repo, workspace, "issueTemplates", otherID)
	projectHash := scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated")
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		for _, spec := range []struct {
			name, id, scope, visibility, team string
			code                              int
		}{
			{"team-owned", targetID, "", "", f.public, http.StatusNoContent},
			{"visibility-owned", targetID, "workspace", f.public, f.public + "-other", http.StatusNoContent},
			{"workspace-default-only", targetID, "workspace", "", f.public, http.StatusNotFound},
			{"other-visibility", targetID, "", f.public + "-other", f.public, http.StatusNotFound},
			{"other-team", targetID, "", "", f.public + "-other", http.StatusNotFound},
			{"missing", targetID + "-old-slug", "", "", f.public, http.StatusNotFound},
		} {
			t.Run(spec.name, func(t *testing.T) {
				var results []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					seed := target
					seed.Scope, seed.VisibilityTeamID, seed.TeamID = spec.scope, spec.visibility, spec.team
					reset(seed)
					var events []domain.DomainEvent
					var realtime []domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
					r := f.request(user, key, nil)
					r.SetPathValue("id", f.public)
					r.SetPathValue("templateId", spec.id)
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, r, (*server).deleteIssueTemplate)
					wantEvents := 0
					if spec.code == http.StatusNoContent {
						wantEvents = 1
					}
					if w.Code != spec.code || len(events) != wantEvents || len(realtime) != wantEvents {
						t.Fatalf("template status=%d events=%d/%d body=%s", w.Code, len(events), len(realtime), w.Body.String())
					}
					results = append(results, []any{
						w.Code, scaleP21SW10Response(t, w, false),
						scaleP21SW10Rows(t, repo, workspace, store.RecordKey{Kind: "issueTemplates", ID: targetID}, store.RecordKey{Kind: "users", ID: user.ID}),
						scaleP21SW10Events(t, events, realtime),
					})
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatalf("template differential: %s", scaleP21M2Diff(results[0], results[1]))
				}
			})
		}
	})
	large := target
	large.Body = strings.Repeat("oversized owned template ", 20000)
	reset(large)
	before := scaleP21SW6RecordHash(t, repo, workspace, "issueTemplates", targetID)
	r := f.request(f.data.Viewer, nil, nil)
	r.SetPathValue("id", f.public)
	r.SetPathValue("templateId", targetID)
	w := httptest.NewRecorder()
	(&server{store: f.probe}).deleteIssueTemplate(w, r)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
		scaleP21SW6RecordHash(t, repo, workspace, "issueTemplates", targetID) != before {
		t.Fatal("oversized template deletion did not preserve the target on an explicit budget failure")
	}
	reset(target)
	before = scaleP21SW6RecordHash(t, repo, workspace, "issueTemplates", targetID)
	for _, source := range []*store.SQLiteStore{repo, f.probe} {
		r := f.request(f.data.Viewer, nil, nil)
		r.SetPathValue("id", f.public)
		r.SetPathValue("templateId", targetID)
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		w := httptest.NewRecorder()
		(&server{store: source}).deleteIssueTemplate(w, r.WithContext(ctx))
		if w.Code != http.StatusInternalServerError || scaleP21SW6RecordHash(t, repo, workspace, "issueTemplates", targetID) != before {
			t.Fatal("cancelled deletion changed its target")
		}
	}
	scaleP21SW10WriteRevocation(t, f, (*server).deleteIssueTemplate, func(user domain.User, key *domain.APIKey) *http.Request {
		r := f.request(user, key, nil)
		r.SetPathValue("id", f.public)
		r.SetPathValue("templateId", targetID)
		return r
	})
	if scaleP21SW6RecordHash(t, repo, workspace, "issueTemplates", otherID) != otherHash ||
		scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated") != projectHash ||
		f.probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("template deletion changed unrelated bytes or decoded full metadata")
	}
}
