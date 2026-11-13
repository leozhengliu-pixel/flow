package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	appconfig "flow/api/internal/config"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW22Effects(t *testing.T, webhooks []domain.DomainEvent, realtime []domain.RealtimeEvent) any {
	t.Helper()
	result := []any{}
	for _, event := range webhooks {
		result = append(result, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
	}
	for _, event := range realtime {
		result = append(result, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
	}
	return scaleP21M2JSON(t, result, false)
}

func TestScaleP21SW22ApplicationWebhookDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	enableConnectorSecrets(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		app := scaleP21SW12Applications(t, repo, workspace, f.data.Viewer, "")
		app.WebhookURL, app.WebhookEnabled, app.WebhookSecretPrefix = "https://example.test/sw22", true, "before-prefix"
		app.WebhookResourceTypes = nil
		scaleP21SW12Reset(t, repo, workspace, app)
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "webhooks"), workspace, "webhook.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Webhooks = []domain.Webhook{
				{ID: idgen.New("sw22-matching-"), ManagedBy: "application", ApplicationClientID: app.ClientID, Enabled: true, URL: "https://example.test/hook"},
				{ID: idgen.New("sw22-duplicate-"), ManagedBy: "application", ApplicationClientID: app.ClientID},
			}
			for range 75 {
				data.Webhooks = append(data.Webhooks, domain.Webhook{ID: idgen.New("sw22-hook-"), ManagedBy: "application", ApplicationClientID: app.ClientID + "-other"})
			}
			data.Webhooks[len(data.Webhooks)-1].Name = strings.Repeat("unrelated large hook ", 20000)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		unrelated := scaleP21SW12OtherApplications(t, repo, workspace, app.ID)
		hooks := scaleP21M3Fingerprint(t, repo, workspace, "webhooks")
		reset := func() {
			t.Helper()
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			f.probe.SetWebhookSink(nil)
			f.probe.SetRealtimeSink(nil)
			scaleP21SW12Reset(t, repo, workspace, app)
			if err := repo.DeleteWebhookSigningSecret(t.Context(), store.ApplicationWebhookSecretRef(app.ClientID)); err != nil {
				t.Fatal(err)
			}
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range []struct {
				name    string
				handler func(*server, http.ResponseWriter, *http.Request)
			}{
				{"read", (*server).getApplicationWebhook},
			} {
				for _, missing := range []bool{false, true} {
					t.Run(operation.name+map[bool]string{false: "/found", true: "/missing"}[missing], func(t *testing.T) {
						var outcomes []any
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							reset()
							var webhooks []domain.DomainEvent
							var realtime []domain.RealtimeEvent
							source.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhooks = append(webhooks, event) })
							source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
							r, w := f.request(user, key, nil), httptest.NewRecorder()
							target := app.ID
							if missing {
								target += "-missing"
							}
							r.SetPathValue("id", target)
							scaleP21M3Call(&server{store: source}, w, r, operation.handler)
							if missing {
								if w.Code != http.StatusNotFound {
									t.Fatalf("missing application status=%d body=%s", w.Code, w.Body.String())
								}
							} else if w.Code != http.StatusOK {
								t.Fatalf("application webhook status=%d body=%s", w.Code, w.Body.String())
							}
							var result applicationWebhookSettings
							if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
								t.Fatal(err)
							}
							if !missing && (result.Installations != 1 || result.ResourceTypes == nil) {
								t.Fatal("installation count or empty resource shape changed")
							}
							secret, signing, err := repo.WebhookSigningSecret(t.Context(), store.ApplicationWebhookSecretRef(app.ClientID))
							if err != nil {
								t.Fatal(err)
							}
							if signing || secret != "" || result.Secret != "" || len(webhooks) != 0 || len(realtime) != 0 {
								t.Fatal("read or missing application created a signing secret")
							}
							outcomes = append(outcomes, []any{
								w.Code, scaleP21M3JSON(t, w.Body.Bytes()), signing,
								scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "oauthApplications", ID: app.ID}, store.RecordKey{Kind: "users", ID: user.ID}),
								scaleP21SW22Effects(t, webhooks, realtime),
							})
						}
						if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
							t.Fatalf("application webhook differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
						}
					})
				}
			}
		})
		t.Run("auth-disabled", func(t *testing.T) {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				reset()
				r, w := f.request(f.data.Viewer, &domain.APIKey{Scopes: []string{"read"}}, nil), httptest.NewRecorder()
				r.SetPathValue("id", app.ID)
				(&server{store: source, authDisabled: true}).getApplicationWebhook(w, r)
				if w.Code != http.StatusOK {
					t.Fatal("auth-disabled helper changed the original credential bypass", w.Code, w.Body.String())
				}
			}
		})
		t.Run("hidden-large-target", func(t *testing.T) {
			large := app
			large.Description = strings.Repeat("large target application ", 18000)
			reset()
			scaleP21SW12Reset(t, repo, workspace, large)
			before := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(f.data.Viewer, &domain.APIKey{ID: "sw22-read", Scopes: []string{"read"}}, nil), httptest.NewRecorder()
				r.SetPathValue("id", app.ID)
				(&server{store: source}).getApplicationWebhook(w, r)
				if w.Code != http.StatusNotFound {
					t.Fatal("non-admin credential learned a hidden application's size", w.Code, w.Body.String())
				}
			}
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", app.ID)
			(&server{store: f.probe}).getApplicationWebhook(w, r)
			if w.Code < 400 || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) || before != scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") {
				t.Fatal("large selected application was silently omitted or changed state", w.Code, w.Body.String())
			}
		})
		reset()
		for _, id := range []string{app.Name, "", strings.Repeat("x", 192), app.ID + "\x00"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.SetPathValue("id", id)
				(&server{store: source}).getApplicationWebhook(w, r)
				if w.Code != http.StatusNotFound {
					t.Fatal("application name or malformed reference became a record", w.Code, w.Body.String())
				}
			}
		}
		t.Run("ownership-cancellation-and-recovery", func(t *testing.T) {
			before := scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications")
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", app.ID)
			service := &server{store: f.probe}
			data, ok := service.applicationWebhookDataScoped(w, r)
			if !ok || len(data.OAuthApplications) != 1 || len(data.OAuthApplications[0].Scopes) == 0 {
				t.Fatal("application point data missing")
			}
			data.OAuthApplications[0].Name = "Caller-owned"
			data.OAuthApplications[0].Scopes[0] = "Caller-owned"
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			service.getApplicationWebhook(httptest.NewRecorder(), r.WithContext(ctx))
			w = httptest.NewRecorder()
			service.getApplicationWebhook(w, r)
			if w.Code != http.StatusOK || before != scaleP21M3Fingerprint(t, repo, workspace, "oauthApplications") {
				t.Fatal("owned return mutation, cancellation or recovery changed canonical application")
			}
		})
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW22 foreign", idgen.New("sw22-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
			r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
			r.SetPathValue("id", app.ID)
			scaleP21M3Call(&server{store: source}, w, r, (*server).getApplicationWebhook)
			if w.Code != http.StatusNotFound {
				t.Fatal("application crossed its workspace binding", w.Code, w.Body.String())
			}
		}
		requests := []*http.Request{}
		member := f.principals[2].user
		for _, credential := range scaleP21M3Credentials() {
			r := f.request(member, credential.key, nil)
			r.SetPathValue("id", app.ID)
			requests = append(requests, r)
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			for _, r := range requests {
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, r, (*server).getApplicationWebhook)
					if w.Code != http.StatusForbidden {
						t.Fatal("queued/replayed webhook read retained revoked authority")
					}
				}
			}
		}
		if _, signing, err := repo.WebhookSigningSecret(t.Context(), store.ApplicationWebhookSecretRef(app.ClientID)); err != nil || signing {
			t.Fatal("revoked webhook read created a signing secret", err)
		}
		if unrelated != scaleP21SW12OtherApplications(t, repo, workspace, app.ID) || hooks != scaleP21M3Fingerprint(t, repo, workspace, "webhooks") || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("webhook helpers changed unrelated application/hook rows or decoded full metadata")
		}
	}
}

func TestScaleP21SW22CodingArtifactWorkspaceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		id := idgen.New("sw22-coding-")
		previous := codingArtifacts
		t.Cleanup(func() { codingArtifacts = previous })
		calls := 0
		codingArtifacts.sessions = func(*server) codingSessionReader {
			return fakeSessionReader{workspace + "/" + id: {ID: id, CreatorID: f.data.Viewer.ID}}
		}
		codingArtifacts.authorize = func(s *server, r *http.Request, session domain.CodingSession) error {
			calls++
			if session.CreatorID != authUser(r).ID {
				return store.ErrAuthForbidden
			}
			_, status, err := s.store.WorkspaceRole(r.Context(), f.data.Workspace.ID, authUser(r).ID)
			if err != nil || status != "active" {
				return store.ErrAuthForbidden
			}
			return nil
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, workspace, id string
				enabled, ready      bool
			}{
				{"found", workspace, id, true, true},
				{"missing-session", workspace, id + "-missing", true, true},
				{"missing-workspace", workspace + "-missing", id, true, true},
				{"disabled", workspace, id, false, true},
				{"unready", workspace, id, true, false},
			} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(user, key, nil), httptest.NewRecorder()
					r.Header.Set("X-Workspace-Key", spec.workspace)
					r.SetPathValue("workspaceKey", spec.workspace)
					r.SetPathValue("id", spec.id)
					service := &server{store: source, coding: &codingServices{config: appconfig.CodingConfig{Enabled: spec.enabled}}}
					if spec.ready {
						codingArtifacts.sessions = func(*server) codingSessionReader {
							return fakeSessionReader{workspace + "/" + id: {ID: id, CreatorID: f.data.Viewer.ID}}
						}
					} else {
						codingArtifacts.sessions = func(*server) codingSessionReader { return nil }
					}
					before := calls
					bound, session, ok := service.codingArtifactSession(w, r)
					var response any
					if w.Body.Len() > 0 {
						response = scaleP21M3JSON(t, w.Body.Bytes())
					}
					outcomes = append(outcomes, []any{w.Code, response, bound, session, ok, calls - before})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("%s artifact session differential: %s", spec.name, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, f.principals[2].user.ID); err != nil {
			t.Fatal(err)
		}
		codingArtifacts.sessions = func(*server) codingSessionReader {
			return fakeSessionReader{workspace + "/" + id: {ID: id, CreatorID: f.data.Viewer.ID}}
		}
		for range 2 {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(f.principals[2].user, nil, nil), httptest.NewRecorder()
				r.SetPathValue("id", id)
				_, _, ok := (&server{store: source, coding: &codingServices{config: appconfig.CodingConfig{Enabled: true}}}).codingArtifactSession(w, r)
				if ok || w.Code != http.StatusNotFound {
					t.Fatal("queued/replayed artifact lookup retained revoked ownership")
				}
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("artifact lookup decoded complete metadata")
		}
	}
}
