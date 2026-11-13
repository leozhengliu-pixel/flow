package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21M2Principal struct {
	name string
	user domain.User
}

type scaleP21M2Fixture struct {
	scaleP03PlatformFixture
	probe      *store.SQLiteStore
	principals []scaleP21M2Principal
	public     string
}

func scaleP21M2Setup(t *testing.T, repo *store.SQLiteStore) scaleP21M2Fixture {
	t.Helper()
	f := scaleP03Fixture(t, repo)
	repo.SetWebhookSink(func(string, domain.DomainEvent) {})
	repo.SetRealtimeSink(func(string, domain.RealtimeEvent) {})
	public, private, child := idgen.New("m2-public-"), idgen.New("m2-private-"), idgen.New("m2-child-")
	if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_m2.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: public, Key: public}, domain.Team{ID: private, Key: private, Private: true}, domain.Team{ID: child, Key: child})
		data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
		data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private}
		data.Projects = append(data.Projects, domain.Project{ID: "m2-unrelated", Name: "Hidden", SlugID: "renamed-000000000021", PreviousSlugIDs: []string{"old-name"}, TeamIDs: []string{private}, Description: strings.Repeat("unrelated body ", 30000), IssueCount: 99, Lead: &data.Viewer})
		data.Settings["m2-unrelated"] = strings.Repeat("unrelated settings ", 20000)
		data.WorkspaceSettings.InviteLinksEnabled = true
		data.WorkspaceSettings.SCIMEnabled = true
		data.WorkspaceInviteLink = &domain.WorkspaceInviteLink{Token: "m2-original", Enabled: true}
		data.Webhooks = append(data.Webhooks, domain.Webhook{ID: "m2-hook", Enabled: true, URL: "https://example.test/hook"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	principals := []scaleP21M2Principal{{"admin-private-outsider", f.data.Viewer}}
	var privateAdmin *http.Client
	for _, spec := range []struct {
		name, role string
		teams      []string
	}{
		{"admin", "admin", []string{private}},
		{"member-private-outsider", "member", []string{public}},
		{"guest-explicit", "guest", []string{public}},
		{"sub-team-inheritance", "member", []string{private}},
	} {
		client, user := scaleP03Join(t, f, spec.name, idgen.New("m2-user-")+"@example.test", spec.role, spec.teams...)
		if spec.name == "admin" {
			privateAdmin = client
		}
		principals = append(principals, scaleP21M2Principal{spec.name, user})
	}
	issue := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "M2 private grant", "teamId": private}, f.data.Workspace.URLKey, http.StatusCreated)
	authRequest[[]domain.IssuePermission](t, privateAdmin, http.MethodPut, f.host.URL+"/api/issues/"+issue.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": principals[3].user.ID, "role": "viewer"}}}, f.data.Workspace.URLKey, http.StatusOK)
	probe, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unrelated := scaleP21M2UnrelatedHash(t, t.Context(), repo, f.data.Workspace.URLKey)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if after := scaleP21M2UnrelatedHash(t, ctx, repo, f.data.Workspace.URLKey); after != unrelated {
			t.Error("unrelated canonical settings bytes changed")
		}
	})
	return scaleP21M2Fixture{f, probe, principals, public}
}

func scaleP21M2UnrelatedHash(t *testing.T, ctx context.Context, repo *store.SQLiteStore, workspace string) [32]byte {
	t.Helper()
	digest := sha256.New()
	found := false
	if err := repo.StreamMetadataRecords(ctx, workspace, "settings", func(key store.RecordKey, reader io.Reader) error {
		if key.ID == "m2-unrelated" {
			found = true
			_, err := io.Copy(digest, reader)
			return err
		}
		return nil
	}); err != nil || !found {
		t.Fatal("read unrelated canonical bytes", err)
	}
	return [32]byte(digest.Sum(nil))
}

func (f scaleP21M2Fixture) request(user domain.User, key *domain.APIKey, body any) *http.Request {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/scale-p21-m2", bytes.NewReader(raw))
	r.Header.Set("X-Workspace-Key", f.data.Workspace.URLKey)
	r.SetPathValue("workspaceKey", f.data.Workspace.URLKey)
	r.SetPathValue("workspace", f.data.Workspace.URLKey)
	ctx := context.WithValue(r.Context(), authUserContextKey{}, user)
	ctx = store.ContextWithActor(ctx, user)
	if key != nil {
		ctx = context.WithValue(ctx, apiKeyContextKey{}, *key)
	}
	return r.WithContext(ctx)
}

func (f scaleP21M2Fixture) matrix(t *testing.T, visit func(*testing.T, domain.User, *domain.APIKey)) {
	t.Helper()
	for _, principal := range f.principals {
		for _, credential := range []struct {
			name string
			key  *domain.APIKey
		}{
			{"session", nil},
			{"unrestricted", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"}},
			{"selected-empty", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{"selected-set", &domain.APIKey{ID: "m2-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
		} {
			t.Run(principal.name+"/"+credential.name, func(t *testing.T) { visit(t, principal.user, credential.key) })
		}
	}
}

// Only generated clocks and the invite link's random token are normalized.
// Actor, role, reference, count, permission and collection shapes stay intact.
func scaleP21M2JSON(t *testing.T, value any, invite bool) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var normalize func(any)
	normalize = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				switch key {
				case "updatedAt", "createdAt", "reviewedAt", "lastTestAt", "archivedAt", "at":
					if value != nil {
						item[key] = "<clock>"
					}
				case "token":
					if invite {
						item[key] = "<generated-invite>"
					}
				default:
					normalize(value)
				}
			}
		case []any:
			for _, value := range item {
				normalize(value)
			}
		}
	}
	normalize(decoded)
	return decoded
}

func scaleP21M2Response(t *testing.T, w *httptest.ResponseRecorder, invite bool) any {
	t.Helper()
	if w.Body.Len() == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return scaleP21M2JSON(t, value, invite)
}

func scaleP21M2Canonical(t *testing.T, repo *store.SQLiteStore, workspace string, keys ...store.RecordKey) any {
	t.Helper()
	batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, keys, "detail")
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{}
	for _, item := range batch.Items {
		if item.Omitted {
			t.Fatal("fixture's small canonical row was omitted", item.Key)
		}
		rows = append(rows, []any{item.Key, item.Missing, item.Data})
	}
	// Row versions are fingerprints of JSON that includes generated clocks.
	return scaleP21M2JSON(t, rows, false)
}

func scaleP21M2Diff(before, after any) string {
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	at := 0
	for at < len(a) && at < len(b) && a[at] == b[at] {
		at++
	}
	start := max(0, at-60)
	return fmt.Sprintf("JSON byte %d: legacy=%s bounded=%s", at, a[min(start, len(a)):min(at+150, len(a))], b[min(start, len(b)):min(at+150, len(b))])
}

func TestScaleP21M2PolicyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		services := []*server{{store: repo}, {store: f.probe}}
		unrelated, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "settings", ID: "m2-unrelated"}}, "summary")
		if err != nil {
			t.Fatal(err)
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []struct {
				name   string
				invite bool
				body   any
				run    func(*server, http.ResponseWriter, *http.Request)
			}{
				{"admin", false, nil, func(s *server, w http.ResponseWriter, r *http.Request) {
					if s.requireWorkspaceAdmin(w, r) {
						w.WriteHeader(http.StatusNoContent)
					}
				}},
				{"tokens", false, nil, (*server).listSCIMTokens},
				{"customer-policy", false, nil, func(s *server, w http.ResponseWriter, r *http.Request) {
					if s.allowManualCustomerEdit(w, r) {
						w.WriteHeader(http.StatusNoContent)
					}
				}},
				{"invite-read", false, nil, (*server).getWorkspaceInviteLink},
				{"invite-rotate", true, nil, (*server).createOrRotateWorkspaceInviteLink},
				{"invite-disable", false, nil, (*server).disableWorkspaceInviteLink},
				{"sync-request", false, map[string]string{"status": "approved", "teamId": f.public}, (*server).decideOAuthSyncGroupRequest},
			} {
				t.Run(operation.name, func(t *testing.T) {
					var responses []any
					var effects []any
					for _, s := range services {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset", "", nil, func(data *domain.Bootstrap) error {
							data.WorkspaceInviteLink = &domain.WorkspaceInviteLink{Token: "m2-original", Enabled: true}
							data.OAuthSyncGroupRequests = []domain.OAuthSyncGroupRequest{{ID: "m2-request", ApplicationID: "m2-app", Status: "pending"}, {ID: "m2-other-request", ApplicationID: "other", Status: "pending"}}
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						var event domain.DomainEvent
						var realtime domain.RealtimeEvent
						s.store.SetWebhookSink(func(_ string, e domain.DomainEvent) { event = e })
						s.store.SetRealtimeSink(func(_ string, e domain.RealtimeEvent) { realtime = e })
						r := f.request(user, key, operation.body)
						r.SetPathValue("id", "m2-app")
						r.SetPathValue("requestId", "m2-request")
						w := httptest.NewRecorder()
						operation.run(s, w, r)
						responses = append(responses, []any{w.Code, scaleP21M2Response(t, w, operation.invite)})
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
						data, _ := repo.WorkspaceMetadata(workspace)
						effects = append(effects, scaleP21M2JSON(t, []any{data.WorkspaceInviteLink, data.OAuthSyncGroupRequests, event.Type, event.AggregateID, event.Payload, event.PreviousValues, realtime.Payload}, operation.invite))
					}
					if !reflect.DeepEqual(responses[0], responses[1]) || !reflect.DeepEqual(effects[0], effects[1]) {
						t.Fatalf("response: %s; effects: %s", scaleP21M2Diff(responses[0], responses[1]), scaleP21M2Diff(effects[0], effects[1]))
					}
				})
			}
		})
		for _, s := range services {
			token, err := repo.CreateSCIMToken(t.Context(), f.data.Workspace.ID, idgen.New("m2-token-"))
			if err != nil {
				t.Fatal(err)
			}
			r := f.request(f.data.Viewer, nil, nil)
			r.Header.Set("Authorization", "Bearer "+token.Secret)
			w := httptest.NewRecorder()
			if _, _, ok := s.authenticateSCIMRequest(w, r); !ok {
				t.Fatalf("valid token rejected: %s", w.Body.String())
			}
			r.SetPathValue("workspace", "m2-missing")
			w = httptest.NewRecorder()
			if _, _, ok := s.authenticateSCIMRequest(w, r); ok || w.Code != http.StatusForbidden {
				t.Fatal("cross-workspace token accepted")
			}
			if err := repo.RevokeSCIMToken(t.Context(), f.data.Workspace.ID, token.ID); err != nil {
				t.Fatal(err)
			}
		}
		after, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: "settings", ID: "m2-unrelated"}}, "summary")
		if err != nil || !reflect.DeepEqual(unrelated.Items, after.Items) {
			t.Fatal("unrelated canonical row changed")
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded policy handler decoded full metadata")
		}
		// The exact off-mode read remains the allocation baseline.
		old := testing.AllocsPerRun(3, func() { _, _ = repo.WorkspaceMetadata(workspace) })
		bounded := testing.AllocsPerRun(3, func() {
			if _, _, err := f.probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{}); err != nil {
				t.Fatal(err)
			}
		})
		t.Logf("identity metadata allocations/op: legacy=%.0f bounded=%.0f", old, bounded)
		member := f.principals[2].user
		stale, err := f.probe.ResolveAccess(t.Context(), workspace, member.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replay"} {
			t.Run(phase, func(t *testing.T) {
				if err := f.probe.ValidateAccess(t.Context(), stale); err == nil {
					t.Fatal("revoked authority accepted")
				}
			})
		}
	}
}

func TestScaleP21M2SCIMProfileAndAudit(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, userID := f.data.Workspace.URLKey, f.data.Viewer.ID
		var effects []any
		for _, service := range []*server{{store: repo}, {store: f.probe}} {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_m2.reset_profile", "", nil, func(data *domain.Bootstrap) error {
				data.UserSettings[userID] = domain.UserSettings{UserID: userID, JobTitle: "Before"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx := store.ContextWithActor(t.Context(), f.data.Viewer)
			var event domain.DomainEvent
			var realtime domain.RealtimeEvent
			service.store.SetWebhookSink(func(_ string, e domain.DomainEvent) { event = e })
			service.store.SetRealtimeSink(func(_ string, e domain.RealtimeEvent) { realtime = e })
			service.applySCIMProfileSettings(ctx, workspace, userID, store.SCIMProfile{Title: "Before"}, store.SCIMProfile{Title: "After", Timezone: "UTC"})
			data, ok, err := repo.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{})
			if err != nil || !ok {
				t.Fatal(err)
			}
			// The legacy owner is refreshed after the other representation writes.
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			data, _ = repo.WorkspaceMetadata(workspace)
			if data.UserSettings[userID].JobTitle != "After" || data.UserSettings[userID].Timezone != "UTC" {
				t.Fatal("SCIM profile callback did not update the owned record")
			}
			effects = append(effects, scaleP21M2JSON(t, []any{scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "userSettings", ID: userID}), event.Type, event.Payload, event.PreviousValues, realtime.Payload}, false))
			service.recordSCIMAudit(f.request(f.data.Viewer, nil, nil), workspace, []scimAuditEntry{{action: "m2-profile", resourceType: "workspace_member", resourceID: userID}})
		}
		if !reflect.DeepEqual(effects[0], effects[1]) {
			t.Fatalf("profile state/hooks changed: %s", scaleP21M2Diff(effects[0], effects[1]))
		}
		audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{ResourceID: userID})
		if err != nil {
			t.Fatal(err)
		}
		var recorded []any
		for _, audit := range audits {
			if audit.Action == "m2-profile" {
				audit.ID, audit.CreatedAt, audit.Seq, audit.Hash = "", time.Time{}, 0, ""
				recorded = append(recorded, scaleP21M2JSON(t, audit, false))
			}
		}
		if len(recorded) != 2 || !reflect.DeepEqual(recorded[0], recorded[1]) {
			t.Fatalf("audit differential: %v", recorded)
		}
		chain, err := repo.VerifyAuditChain(t.Context(), f.data.Workspace.ID)
		if err != nil || !chain.Valid {
			t.Fatal("audit chain invalid", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := f.probe.WorkspaceMetadataScoped(ctx, workspace, store.MetadataRecordScope{}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("profile update decoded full metadata")
		}
	}
}

func TestScaleP21M2CredentialsAndUploadDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		var results []any
		for _, s := range []*server{{store: repo}, {store: f.probe}} {
			var outcomes []any
			r := f.request(f.data.Viewer, nil, map[string]string{"name": "M2 issued"})
			w := httptest.NewRecorder()
			s.createSCIMToken(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("SCIM create: status %d", w.Code)
			}
			var token store.SCIMToken
			if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
				t.Fatal(err)
			}
			outcomes = append(outcomes, []any{w.Code, token.Name, token.WorkspaceID, token.Secret != ""})
			r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", token.ID)
			s.rotateSCIMToken(w, r)
			if w.Code != http.StatusCreated {
				t.Fatalf("SCIM rotate: status %d", w.Code)
			}
			if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil {
				t.Fatal(err)
			}
			outcomes = append(outcomes, []any{w.Code, token.Name, token.WorkspaceID, token.Secret != ""})
			r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", token.ID)
			s.revokeSCIMToken(w, r)
			outcomes = append(outcomes, w.Code)
			if w.Code != http.StatusNoContent {
				t.Fatalf("SCIM revoke: %d %s", w.Code, w.Body.String())
			}
			r, w = f.request(f.data.Viewer, nil, map[string]any{"filename": "report.txt", "contentType": "text/plain", "size": 3}), httptest.NewRecorder()
			s.createFileUpload(w, r)
			var upload fileUploadPayload
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &upload) != nil {
				t.Fatalf("upload: %d %s", w.Code, w.Body.String())
			}
			uploadURL, err := url.Parse(upload.UploadURL)
			if err != nil {
				t.Fatal(err)
			}
			uploadToken := strings.TrimPrefix(uploadURL.Path, "/api/files/uploads/")
			record, err := repo.FileUpload(t.Context(), secretHash(uploadToken))
			if err != nil || record.WorkspaceKey != f.data.Workspace.URLKey || record.UserID != f.data.Viewer.ID {
				t.Fatal("upload binding changed")
			}
			outcomes = append(outcomes, []any{w.Code, upload.Filename, upload.ContentType, upload.Size, record.Purpose, record.Status})
			results = append(results, outcomes)
			if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(results[0], results[1]) {
			t.Fatalf("credential/upload differential: %v", results)
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("credential/upload operations decoded metadata")
		}
		// Oversized policy is rejected before the invite callback can replace
		// the link. This is an explicit budget error, with no full-read fallback.
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21_m2.large_policy", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.TrustedSourcesAllowlist = []string{strings.Repeat("x", 300<<10)}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		(&server{store: f.probe}).createOrRotateWorkspaceInviteLink(w, r)
		if w.Code < 400 {
			t.Fatal("oversized policy silently truncated")
		}
		if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		data, _ := repo.WorkspaceMetadata(f.data.Workspace.URLKey)
		if data.WorkspaceInviteLink.Token != "m2-original" {
			t.Fatal("budget rejection wrote an invite")
		}
	}
}
