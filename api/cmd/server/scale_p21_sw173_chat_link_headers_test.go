package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW173LinkedChatPreviewHeadersDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		prefix, workspace := idgen.New("sw173-"), f.data.Workspace.URLKey
		other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Linked preview space", prefix+"space", "us")
		if err != nil {
			t.Fatal(err)
		}
		foreign := other.Workspace.URLKey
		f.resources.workspaces = append(f.resources.workspaces, foreign)
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "Foreign issue preview", "teamId": other.Teams[0].ID}, foreign, http.StatusCreated)
		date := "2027-02-03"
		project := prefix + "project"
		if err := repo.MutateWorkspace(t.Context(), foreign, "scale_p21.sw173_seed", "", nil, func(data *domain.Bootstrap) error {
			// Later policy mutations refresh embedded actors; keep this reference current.
			lead := data.Viewer
			data.Projects = []domain.Project{
				{ID: project, Name: "Earlier alias project", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old", prefix + "collision"}, Summary: "Exact summary <&> " + strings.Repeat("retained preview ", 45), Status: domain.ProjectStatus{Name: "In Progress"}, Lead: &lead, TargetDate: &date, TeamIDs: []string{other.Teams[0].ID}},
				{ID: prefix + "exact", Name: "Later exact slug project", SlugID: prefix + "collision", Summary: "Must not replace the earlier alias", TeamIDs: []string{other.Teams[0].ID}},
				{ID: project, Name: "Later duplicate project", SlugID: prefix + "last", TeamIDs: []string{other.Teams[0].ID}},
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		assertSeed := func(stage string) {
			t.Helper()
			data, ok := repo.WorkspaceMetadata(foreign)
			found := false
			for _, item := range data.Projects {
				found = found || item.ID == project && item.SlugID == prefix+"current" && item.Name == "Earlier alias project"
			}
			if !ok || !found {
				for _, item := range data.Projects {
					t.Logf("%s project id=%q name=%q slug=%q old=%q teams=%q", stage, item.ID, item.Name, item.SlugID, item.PreviousSlugIDs, item.TeamIDs)
				}
				t.Fatalf("%s lost seeded project %q workspace=%q viewer=%q role=%q", stage, project, data.Workspace.URLKey, data.Viewer.ID, data.ViewerRole)
			}
		}
		assertSeed("after seed")
		record := domain.ChatIntegrationRecord{WorkspaceKey: workspace, Provider: "lark", ConnectionID: prefix + "connection", AppID: prefix + "app", TenantKey: prefix + "tenant", Connected: true}
		linked := domain.ChatIntegrationRecord{WorkspaceKey: foreign, Provider: "lark", ConnectionID: prefix + "linked", AppID: prefix + "other-app", TenantKey: record.TenantKey, Connected: true}
		for _, item := range []domain.ChatIntegrationRecord{record, linked} {
			if err := repo.PutChatIntegration(t.Context(), item); err != nil {
				t.Fatal(err)
			}
		}
		credentials, _ := json.Marshal(chatCredentials{AppSecret: "sw173-test-secret"})
		if err := repo.PutConnectorSecret(t.Context(), chatCredentialID(record), credentials, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for _, item := range []domain.ChatIntegrationRecord{record, linked} {
				if err := repo.DeleteChatIntegration(context.Background(), item.WorkspaceKey, item.ConnectionID); err != nil {
					t.Error(err)
				}
			}
			if err := repo.DeleteConnectorSecret(context.Background(), chatCredentialID(record)); err != nil {
				t.Error(err)
			}
		})
		var sent []map[string]any
		var selected *store.SQLiteStore
		failPlatform, requireHeld := false, false
		mockChatTransport(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(request.URL.Path, "tenant_access_token"):
				return chatTestResponse(http.StatusOK, `{"code":0,"tenant_access_token":"sw173-token","expire":7200}`), nil
			case strings.Contains(request.URL.Path, "/im/v1/messages"):
				if requireHeld && selected.BoundedMetadataEnabled() && selected.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("linked preview headers released before platform send")
				}
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				sent = append(sent, map[string]any{"method": request.Method, "path": request.URL.Path, "query": request.URL.RawQuery, "body": payload})
				if failPlatform {
					return nil, errors.New("sw173 platform unavailable")
				}
				return chatTestResponse(http.StatusOK, `{"code":0,"data":{"message_id":"sw173-message","chat_id":"sw173-chat"}}`), nil
			}
			t.Fatalf("unexpected linked-preview request %s", request.URL.Path)
			return nil, nil
		})
		type outcome struct {
			Error string
			Sent  []map[string]any
		}
		cases := []struct {
			name, kind, ref, witness string
			key                      *domain.APIKey
			hipaa, staleAuth, denied bool
			unlinked, zh, platform   bool
		}{
			{name: "project-id", kind: "project", ref: project, witness: "Earlier alias project"},
			{name: "project-current-slug", kind: "project", ref: prefix + "current", witness: "Earlier alias project"},
			{name: "project-old-slug", kind: "project", ref: prefix + "old", witness: "Earlier alias project"},
			{name: "project-first-old-before-current", kind: "project", ref: prefix + "collision", witness: "Earlier alias project"},
			{name: "project-later-slug", kind: "project", ref: prefix + "last", witness: "Later duplicate project"},
			{name: "issue-id", kind: "issue", ref: issue.ID, witness: "Foreign issue preview"},
			{name: "issue-identifier", kind: "issue", ref: issue.Identifier, witness: "Foreign issue preview"},
			{name: "project-missing", kind: "project", ref: "missing", witness: "can't open this link"},
			{name: "issue-missing", kind: "issue", ref: "missing", witness: "can't open this link"},
			{name: "project-selected-empty", kind: "project", ref: project, key: &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}, witness: "can't open this link"},
			{name: "project-selected-set", kind: "project", ref: project, key: &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{other.Teams[0].ID}}, witness: "Earlier alias project"},
			{name: "issue-selected-empty", kind: "issue", ref: issue.ID, key: &domain.APIKey{Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}, witness: "can't open this link"},
			{name: "hipaa-project", kind: "project", ref: project, hipaa: true, witness: "keeps its content out of chat"},
			{name: "authorization-reconnect", kind: "project", ref: project, staleAuth: true, witness: "expired"},
			{name: "authorization-membership-denied", kind: "issue", ref: issue.ID, denied: true, witness: "access to this workspace has changed"},
			{name: "disconnected-unlinked", kind: "project", ref: project, unlinked: true, witness: "can't open this link"},
			{name: "project-chinese", kind: "project", ref: prefix + "old", zh: true, witness: "Earlier alias project"},
			{name: "project-platform-failure", kind: "project", ref: project, platform: true, witness: "Earlier alias project"},
		}
		for _, fixture := range cases {
			if err := repo.MutateWorkspace(t.Context(), foreign, "scale_p21.sw173_policy", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.HIPAACompliance = fixture.hipaa
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			assertSeed("after policy " + fixture.name)
			user := f.data.Viewer
			if fixture.denied {
				user = f.principals[2].user
			}
			identity := domain.ChatIdentity{UserID: user.ID, OpenID: prefix + "open-" + user.ID, TenantKey: record.TenantKey, AuthProvider: "password", AuthenticatedAt: time.Now().UTC()}
			otherIdentity := identity
			otherIdentity.OpenID = prefix + "foreign-open-" + user.ID
			if fixture.staleAuth {
				otherIdentity.AuthProvider, otherIdentity.AuthenticatedAt = "", time.Time{}
			}
			linked.Connected = true
			if err := repo.PutChatIntegration(t.Context(), linked); err != nil {
				t.Fatal(err)
			}
			if err := repo.PutChatIdentity(t.Context(), foreign, linked.ConnectionID, otherIdentity); err != nil {
				t.Fatal(err)
			}
			if fixture.unlinked {
				linked.Connected = false
				if err := repo.PutChatIntegration(t.Context(), linked); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{workspace, foreign} {
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, key); err != nil {
					t.Fatal(err)
				}
			}
			var outcomes []outcome
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				selected, sent, failPlatform = source, nil, fixture.platform
				requireHeld = !fixture.denied && !fixture.staleAuth && !fixture.unlinked
				dm := chatDMRequest{
					record: record, identity: identity, target: chatDMTarget{Workspace: workspace, Identity: identity},
					command: chatInboundCommand{OpenID: identity.OpenID, MessageID: prefix + fixture.name},
					job:     domain.ChatJob{DedupKey: prefix + fixture.name}, zh: fixture.zh,
					metadata: domain.Bootstrap{Projects: []domain.Project{{ID: project, Name: "Wrong original-workspace project"}}},
				}
				ctx := f.literalRequest(user, fixture.key, nil).Context()
				err := (&server{store: source, allowedOrigin: "https://flow.example.test"}).chatDMLinkPreview(ctx, dm, chatDMLink{Workspace: foreign, Kind: fixture.kind, Ref: fixture.ref})
				result := outcome{Sent: append([]map[string]any(nil), sent...)}
				if err != nil {
					result.Error = err.Error()
				}
				outcomes = append(outcomes, result)
				if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
					t.Fatal("linked preview retained work or hydrated metadata", fixture.name)
				}
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatalf("linked preview %s changed:\n%s", fixture.name, scaleP21M2Diff(outcomes[0], outcomes[1]))
			}
			raw, err := json.Marshal(outcomes[0])
			if err != nil || len(outcomes[0].Sent) == 0 || fixture.witness != "" && !bytes.Contains(raw, []byte(fixture.witness)) {
				if data, ok := repo.WorkspaceMetadata(foreign); ok {
					for _, item := range data.Projects {
						t.Logf("stored project id=%q name=%q slug=%q old=%q teams=%q", item.ID, item.Name, item.SlugID, item.PreviousSlugIDs, item.TeamIDs)
					}
				}
				t.Fatalf("linked preview %s missed actual branch witness %q: %s (%v)", fixture.name, fixture.witness, raw, err)
			}
			if fixture.platform {
				if outcomes[0].Error != "Chat platform request failed (HTTP 0, code 0)" {
					t.Fatalf("linked preview %s missed platform failure: %s", fixture.name, raw)
				}
			} else if outcomes[0].Error != "" {
				t.Fatalf("linked preview %s returned an unexpected error: %s", fixture.name, raw)
			}
		}
		t.Logf("linked foreign-workspace previews exact rows=%d; project ID/current/old/first-occurrence precedence, issue preview, credential restrictions, HIPAA, authorization/refusal, locale and platform errors", len(cases))
	}
}
