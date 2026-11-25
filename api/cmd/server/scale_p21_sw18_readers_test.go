package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW18WebhookSelectionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_INTEGRATION_SW18_WEBHOOK", "environment-webhook")
	t.Setenv("FLOW_INTEGRATION_SW18_SLACK", "environment-slack")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		targetID := idgen.New("sw18-webhook-")
		for _, provider := range []string{"github", "gitlab", "slack"} {
			for _, envSecret := range []bool{false, true} {
				t.Run(provider+"/env="+strconv.FormatBool(envSecret), func(t *testing.T) {
					target := domain.IntegrationConnection{ID: targetID, Provider: provider, Status: "configured", Config: map[string]string{
						"webhookSecret": "null", "signingSecret": "null", "username": "MixedCase",
						"unrelated": strings.Repeat("large ignored config ", 25000),
					}}
					secret := "null"
					if envSecret {
						target.Config["webhookSecretEnv"], target.Config["signingSecretEnv"] = "FLOW_INTEGRATION_SW18_WEBHOOK", "FLOW_INTEGRATION_SW18_SLACK"
						secret = "environment-webhook"
						if provider == "slack" {
							secret = "environment-slack"
						}
					}
					connections := []domain.IntegrationConnection{
						{ID: idgen.New("sw18-case-provider-"), Provider: strings.ToUpper(provider), Status: "connected"},
						{ID: idgen.New("sw18-case-status-"), Provider: provider, Status: "Connected"},
						{ID: idgen.New("sw18-disabled-"), Provider: provider, Status: "disabled"}, target,
						{ID: idgen.New("sw18-later-"), Provider: provider, Status: "connected", Config: map[string]string{"webhookSecret": "later", "signingSecret": "later"}},
					}
					for range 65 {
						connections = append(connections, domain.IntegrationConnection{ID: idgen.New("sw18-unrelated-"), Provider: "unused", Status: "connected"})
					}
					if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections"), workspace, "integration.updated", "", nil, func(data *domain.Bootstrap) error {
						data.IntegrationConnections = connections
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					hash := scaleP21M3Fingerprint(t, repo, workspace, "integrationConnections")
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						var results []any
						for _, source := range []*store.SQLiteStore{repo, f.probe} {
							r := f.request(user, key, nil)
							credentials, release, err := (&server{store: source}).webhookConnection(r, provider)
							if err != nil || credentials == nil || credentials.ID != targetID || credentials.Username != "MixedCase" {
								release()
								t.Fatal("webhook selection changed", err)
							}
							body := []byte(`{"type":"url_verification","challenge":"sw18-challenge"}`)
							connection := integrationWebhookConnection(credentials)
							switch provider {
							case "github":
								mac := hmac.New(sha256.New, []byte(secret))
								_, _ = mac.Write(body)
								r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
							case "gitlab":
								r.Header.Set("X-Gitlab-Token", secret)
							case "slack":
								stamp := strconv.FormatInt(time.Now().Unix(), 10)
								mac := hmac.New(sha256.New, []byte(secret))
								_, _ = fmt.Fprintf(mac, "v0:%s:%s", stamp, body)
								r.Header.Set("X-Slack-Request-Timestamp", stamp)
								r.Header.Set("X-Slack-Signature", "v0="+hex.EncodeToString(mac.Sum(nil)))
							}
							verified := verifyCodeWebhook(provider, connection, r, body)
							if provider == "slack" {
								verified = verifySlackWebhook(connection, r, body)
							}
							if !verified {
								release()
								t.Fatal("selected credentials no longer verify their webhook")
							}
							results = append(results, *credentials)
							release()
							if provider == "slack" {
								r.Body = io.NopCloser(bytes.NewReader(body))
								w := httptest.NewRecorder()
								(&server{store: source}).slackWebhook(w, r)
								if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"sw18-challenge"`) {
									t.Fatalf("slack challenge changed: %d %s", w.Code, w.Body.String())
								}
							}
						}
						if !reflect.DeepEqual(results[0], results[1]) {
							t.Fatal("credential projection differs across representations")
						}
					})
					if scaleP21M3Fingerprint(t, repo, workspace, "integrationConnections") != hash || f.probe.WorkspaceMetadataDecodes() != 0 {
						t.Fatal("webhook selection changed records or decoded complete metadata")
					}
				})
			}
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW18 webhook foreign", idgen.New("sw18-hook-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r := f.request(f.data.Viewer, nil, nil)
			r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
			r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
			credentials, release, err := (&server{store: source}).webhookConnection(r, "slack")
			release()
			if err != nil || credentials != nil {
				t.Fatal("webhook credentials escaped their workspace")
			}
		}
		r := f.request(f.data.Viewer, nil, nil)
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		_, release, err := (&server{store: f.probe}).webhookConnection(r.WithContext(ctx), "slack")
		release()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled credential read was treated as missing", err)
		}
		r = r.WithContext(ctx)
		r.Body = io.NopCloser(strings.NewReader(`{"type":"url_verification","challenge":"sw18"}`))
		w := httptest.NewRecorder()
		(&server{store: f.probe}).slackWebhook(w, r)
		if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "invalid webhook signature") {
			t.Fatal("credential read failure became a missing/invalid signature result")
		}
		r = f.request(f.data.Viewer, nil, nil)
		_, release, err = (&server{store: f.probe}).webhookConnection(r, "slack")
		release()
		if err != nil {
			t.Fatal("credential read did not recover after cancellation", err)
		}
	}
}

func TestScaleP21SW18TeamResourcesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "sw18-test-key")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, teamID := f.data.Workspace.URLKey, f.public
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		sections := []domain.TeamResourceSection{}
		resources := []domain.TeamPinnedResource{}
		for i := range 111 {
			position := float64(i%5) - 2
			if i > 65 && i < 70 {
				position = math.Nextafter(1, 2)
			}
			sections = append(sections, domain.TeamResourceSection{ID: idgen.New("sw18-section-"), TeamID: teamID, Name: fmt.Sprint(i), Position: position, CreatedAt: stamp, UpdatedAt: stamp})
			resources = append(resources, domain.TeamPinnedResource{ID: idgen.New("sw18-resource-"), TeamID: teamID, Title: fmt.Sprint(i), ResourceType: "document", ResourceID: "private-reference", SectionID: sections[i].ID, URL: "https://example.test/<data>?a=1&b=2", Position: position, CreatedAt: stamp, UpdatedAt: stamp})
		}
		sections[4].Name = strings.Repeat("large legal section 数据 <>& ", 20000)
		resources[6].Title = strings.Repeat("large legal title 数据 <>& ", 20000)
		resources[7].URL = "/uploads/sw18-resource.png"
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teamResourceSections", "teamPinnedResources"), workspace, "team.resource_updated", "", nil, func(data *domain.Bootstrap) error {
			data.TeamResourceSections = append(slices.Clone(sections), domain.TeamResourceSection{ID: "sw18-unrelated-section", TeamID: "other", Name: strings.Repeat("unrelated ", 40000)})
			data.TeamPinnedResources = append(slices.Clone(resources), domain.TeamPinnedResource{ID: "sw18-unrelated-resource", TeamID: "other", Title: strings.Repeat("unrelated ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		expected := projectTeamResources(domain.Bootstrap{TeamResourceSections: sections, TeamPinnedResources: resources}, teamID)
		expected.TeamID = ""
		beforeSections := scaleP21M3Fingerprint(t, repo, workspace, "teamResourceSections")
		beforeResources := scaleP21M3Fingerprint(t, repo, workspace, "teamPinnedResources")
		for _, spoolBytes := range []string{"0", "1024", "268435456"} {
			t.Run("spool="+spoolBytes, func(t *testing.T) {
				t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", spoolBytes)
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					var results []teamResourcesRead
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						r.SetPathValue("id", teamID)
						(&server{store: source, authDisabled: true}).listTeamResources(w, r)
						if w.Code != http.StatusOK {
							t.Fatalf("team resource read status=%d body=%s", w.Code, w.Body.String())
						}
						var result teamResourcesRead
						if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if result.Sections == nil || result.Resources == nil {
							t.Fatal("resource response arrays became null")
						}
						results = append(results, result)
					}
					if !reflect.DeepEqual(results[0], expected) || !reflect.DeepEqual(results[0], results[1]) {
						t.Fatal("complete resources or stable order differs")
					}
				})
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					service := &server{store: source, authDisabled: true}
					r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
					r.SetPathValue("id", teamID)
					r.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
					service.signedFileURLs(http.HandlerFunc(service.listTeamResources)).ServeHTTP(w, r)
					var result teamResourcesRead
					if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
						t.Fatal("signed resource response failed")
					}
					index := slices.IndexFunc(result.Resources, func(value domain.TeamPinnedResource) bool { return value.ID == resources[7].ID })
					if index < 0 {
						t.Fatal("signed resource disappeared")
					}
					signed, err := url.Parse(result.Resources[index].URL)
					if err != nil || signed.Path != "/api/files/signed/sw18-resource.png" {
						t.Fatal("resource URL was not signed through spool fallback")
					}
					expires, err := strconv.ParseInt(signed.Query().Get("expires"), 10, 64)
					key, keyErr := service.fileSigningKey(r.Context())
					if err != nil || keyErr != nil || signed.Query().Get("signature") != fileURLSignature(key, "sw18-resource.png", expires) {
						t.Fatal("resource URL signature is invalid")
					}
					result.Resources[index].URL = "/uploads/sw18-resource.png"
					if !reflect.DeepEqual(result, expected) {
						t.Fatal("URL signing changed other resource fields")
					}
				}
			})
		}
		for _, failure := range []string{"disconnected", "cancelled"} {
			t.Run(failure, func(t *testing.T) {
				t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
				r := f.request(f.data.Viewer, nil, nil)
				r.SetPathValue("id", teamID)
				ctx, cancel := context.WithCancel(r.Context())
				defer cancel()
				r = r.WithContext(ctx)
				w := &scaleP15CountingWriter{header: make(http.Header), onWrite: func() error {
					if failure == "cancelled" {
						cancel()
						return nil
					}
					return errors.New("client disconnected")
				}}
				scaleP15ExpectAbort(t, func() {
					(&server{store: f.probe, authDisabled: true}).listTeamResources(w, r)
				})
				next, response := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				next.SetPathValue("id", teamID)
				(&server{store: f.probe, authDisabled: true}).listTeamResources(response, next)
				if response.Code != http.StatusOK {
					t.Fatal("stream cancellation/disconnection leaked read admission")
				}
			})
		}
		t.Run("unavailable-spool-volume", func(t *testing.T) {
			t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
			r, w := f.literalRequest(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", teamID)
			(&server{store: f.probe, authDisabled: true}).listTeamResources(w, r)
			var got teamResourcesRead
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got, expected) {
				t.Fatal("unavailable spool volume lost complete resource rows")
			}
		})
		// The normal route's authorized context is already projected. This
		// independently reviewed boundary must not regain hidden references.
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			projected := teamResourcesRead{TeamID: teamID, Sections: []domain.TeamResourceSection{}, Resources: []domain.TeamPinnedResource{{ID: "visible", TeamID: teamID, Title: "Visible"}}}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.request(user, key, nil)
				r.SetPathValue("id", teamID)
				r = r.WithContext(context.WithValue(r.Context(), teamResourcesReadKey{}, projected))
				w := httptest.NewRecorder()
				(&server{store: source}).listTeamResources(w, r)
				var got teamResourcesRead
				expected := projected
				expected.TeamID = ""
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || !reflect.DeepEqual(got, expected) {
					t.Fatal("preauthorized resources were reread or widened", err)
				}
			}
		})
		for _, selected := range []struct {
			workspace, team, message string
		}{
			{workspace + "-missing", teamID, "workspace not found"},
			{workspace, teamID + "-old-slug", "team not found"},
			{workspace, teamID + "\x00", "team not found"},
		} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.request(f.data.Viewer, nil, nil)
				r.Header.Set("X-Workspace-Key", selected.workspace)
				r.SetPathValue("workspaceKey", selected.workspace)
				r.SetPathValue("id", selected.team)
				w := httptest.NewRecorder()
				(&server{store: source, authDisabled: true}).listTeamResources(w, r)
				if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), selected.message) {
					t.Fatalf("missing resource identity: %d %s", w.Code, w.Body.String())
				}
			}
		}
		member := f.principals[2].user
		queued := []*http.Request{}
		for _, credential := range scaleP21M3Credentials() {
			r := f.request(member, credential.key, nil)
			r.SetPathValue("id", teamID)
			r = r.WithContext(context.WithValue(r.Context(), teamResourcesReadKey{}, teamResourcesRead{TeamID: teamID, Sections: []domain.TeamResourceSection{}, Resources: []domain.TeamPinnedResource{}}))
			queued = append(queued, r)
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replay"} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				for _, r := range queued {
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, r, (*server).listTeamResources)
					if w.Code != http.StatusForbidden {
						t.Fatalf("%s resource request reused revoked authorization: %d", phase, w.Code)
					}
				}
			}
		}
		if scaleP21M3Fingerprint(t, repo, workspace, "teamResourceSections") != beforeSections ||
			scaleP21M3Fingerprint(t, repo, workspace, "teamPinnedResources") != beforeResources ||
			f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("team resource reads changed canonical rows or decoded full metadata")
		}
	}
}
