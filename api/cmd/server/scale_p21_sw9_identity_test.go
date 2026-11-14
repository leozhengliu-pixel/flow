package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/outbound"
	"flow/api/internal/store"
)

const scaleP21SW9Issuer = "https://example.com/issuer"

type scaleP21SW9Discovery struct {
	mode   string
	calls  int
	before func(*http.Request)
}

func scaleP21SW9Outbound(t *testing.T) *scaleP21SW9Discovery {
	t.Helper()
	fixture := &scaleP21SW9Discovery{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "example.com" {
			t.Error("unexpected outbound host in SW9 fixture")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/issuer/.well-known/openid-configuration" && r.URL.Path != "/issuer///" {
			t.Errorf("unexpected discovery request in SW9 fixture: %s", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.calls++
		if fixture.before != nil {
			before := fixture.before
			fixture.before = nil
			before(r)
		}
		if err := r.Context().Err(); err != nil {
			return
		}
		if fixture.mode == "transport-error" {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		status := http.StatusOK
		if fixture.mode == "unavailable" {
			status = http.StatusBadGateway
		}
		metadata := map[string]any{
			"issuer":                                scaleP21SW9Issuer,
			"authorization_endpoint":                scaleP21SW9Issuer + "/authorize",
			"token_endpoint":                        scaleP21SW9Issuer + "/token",
			"jwks_uri":                              scaleP21SW9Issuer + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		switch fixture.mode {
		case "different-issuer":
			metadata["issuer"] = scaleP21SW9Issuer + "/different"
		case "unsafe-authorization":
			metadata["authorization_endpoint"] = "http://example.com/authorize"
		case "unsafe-token":
			metadata["token_endpoint"] = "https://127.0.0.1/token"
		}
		raw, err := json.Marshal(metadata)
		if err != nil {
			t.Fatal(err)
		}
		if fixture.mode == "invalid-json" {
			raw = []byte("{")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(upstream.Close)
	useOutboundPolicy(t, outbound.Config{}, intranetDNS{"example.com": "93.184.216.34"}, upstream)
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	outboundPolicy = outboundPolicy.WithRoots(roots)
	return fixture
}

func scaleP21SW9Value(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	var normalize func(any)
	normalize = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for key, value := range item {
				if (key == "updatedAt" || key == "lastVerifiedAt") && value != nil {
					item[key] = "<generated-clock>"
				} else {
					normalize(value)
				}
			}
		case []any:
			for _, value := range item {
				normalize(value)
			}
		}
	}
	normalize(result)
	return result
}

func scaleP21SW9Hash(t *testing.T, repo *store.SQLiteStore, workspace, kind, exclude string) [32]byte {
	t.Helper()
	hash := sha256.New()
	if err := repo.StreamMetadataRecords(t.Context(), workspace, kind, func(key store.RecordKey, reader io.Reader) error {
		if key.ID == exclude {
			return nil
		}
		if _, err := io.WriteString(hash, key.ID+"\x00"); err != nil {
			return err
		}
		_, err := io.Copy(hash, reader)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

func scaleP21SW9Providers(t *testing.T, repo *store.SQLiteStore, workspace string) domain.IdentityProvider {
	t.Helper()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	target := domain.IdentityProvider{
		ID: idgen.New("idp_sw9_"), Type: "oidc", Name: "SW9 provider",
		Issuer: scaleP21SW9Issuer + "///", ClientID: "sw9-client",
		Enabled: true, DiscoveryStatus: "verified", Scopes: []string{"profile"},
		Domains: []string{"example.test"}, DefaultRole: "member",
		RoleMapping: map[string]string{"developer": "member"},
		CreatedAt:   at, UpdatedAt: at,
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "identityProviders"), workspace, "scale_p21_sw9.seed", target.ID, nil, func(data *domain.Bootstrap) error {
		data.IdentityProviders = []domain.IdentityProvider{target}
		for range 75 {
			data.IdentityProviders = append(data.IdentityProviders, domain.IdentityProvider{
				ID: idgen.New("idp_sw9_other_"), Type: "oidc", Name: "Unrelated",
				Issuer: "https://other.example.test", ClientID: "other-client",
			})
		}
		data.IdentityProviders[len(data.IdentityProviders)-1].Name = strings.Repeat("unrelated provider ", 25000)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return target
}

func scaleP21SW9Reset(t *testing.T, repo *store.SQLiteStore, workspace string, target domain.IdentityProvider) {
	t.Helper()
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "identityProviders"), workspace, "scale_p21_sw9.reset", target.ID, nil, func(data *domain.Bootstrap) error {
		for i := range data.IdentityProviders {
			if data.IdentityProviders[i].ID == target.ID {
				data.IdentityProviders[i] = target
				return nil
			}
		}
		data.IdentityProviders = append(data.IdentityProviders, target)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW9EnterpriseOIDCClientDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_IDP_SW9_FIXTURE", "sw9-fake-client-secret")
	t.Setenv("FLOW_IDP_SW9_MISSING", "")
	upstream := scaleP21SW9Outbound(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		original := scaleP21SW9Providers(t, repo, workspace)
		unrelated := scaleP21SW9Hash(t, repo, workspace, "identityProviders", original.ID)
		for _, spec := range []struct {
			name, discovery, wantError string
			change                     func(*domain.IdentityProvider)
			missing                    bool
		}{
			{name: "verified"},
			{name: "secret", change: func(p *domain.IdentityProvider) { p.ClientSecretEnv = "FLOW_IDP_SW9_FIXTURE" }},
			{name: "disabled", wantError: errNotFound.Error(), change: func(p *domain.IdentityProvider) { p.Enabled = false }},
			{name: "disabled-before-config", wantError: errNotFound.Error(), change: func(p *domain.IdentityProvider) {
				p.Enabled, p.Issuer, p.ClientSecretEnv = false, "http://sw9-idp.example.test/issuer", "FLOW_IDP_SW9_MISSING"
			}},
			{name: "unverified", wantError: errNotFound.Error(), change: func(p *domain.IdentityProvider) { p.DiscoveryStatus = "unverified" }},
			{name: "saml", wantError: errNotFound.Error(), change: func(p *domain.IdentityProvider) { p.Type = "saml" }},
			{name: "missing", wantError: errNotFound.Error(), missing: true},
			{name: "unsafe", wantError: "unsafe identity provider issuer", change: func(p *domain.IdentityProvider) { p.Issuer = "http://sw9-idp.example.test/issuer" }},
			{name: "unsafe-before-secret", wantError: "unsafe identity provider issuer", change: func(p *domain.IdentityProvider) {
				p.Issuer, p.ClientSecretEnv = "http://sw9-idp.example.test/issuer", "FLOW_IDP_SW9_MISSING"
			}},
			{name: "missing-secret", wantError: "identity provider client secret is not configured", change: func(p *domain.IdentityProvider) { p.ClientSecretEnv = "FLOW_IDP_SW9_MISSING" }},
			{name: "secret-prefix", wantError: "identity provider client secret is not configured", change: func(p *domain.IdentityProvider) { p.ClientSecretEnv = "SW9_UNTRUSTED_ENV" }},
			{name: "discovery-error", discovery: "transport-error"},
			{name: "discovery-unavailable", discovery: "unavailable"},
			{name: "discovery-invalid", discovery: "invalid-json"},
			{name: "issuer-mismatch", discovery: "different-issuer"},
		} {
			t.Run(spec.name, func(t *testing.T) {
				target := original
				if spec.change != nil {
					spec.change(&target)
				}
				scaleP21SW9Reset(t, repo, workspace, target)
				upstream.mode = spec.discovery
				f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						id := target.ID
						if spec.missing {
							id += "-missing"
						}
						before := upstream.calls
						client, provider, err := (&server{store: source, allowedOrigin: "https://flow.example.test"}).enterpriseOIDCClient(f.request(user, key, nil).Context(), workspace, id)
						message := ""
						if err != nil {
							message = err.Error()
						}
						if spec.wantError != "" && message != spec.wantError {
							t.Fatalf("provider error precedence changed: %q", message)
						}
						if spec.wantError != "" && upstream.calls != before {
							t.Fatal("unavailable provider reached outbound discovery")
						}
						if spec.discovery != "" && err == nil {
							t.Fatal("invalid discovery was accepted")
						}
						var config any
						if client != nil {
							wantSecret := ""
							if spec.name == "secret" {
								wantSecret = "sw9-fake-client-secret"
							}
							if client.oauth.ClientSecret != wantSecret || client.verifier == nil {
								t.Fatal("OIDC secret or verifier configuration changed")
							}
							config = []any{client.name, client.issuer, client.oauth.ClientID, client.oauth.RedirectURL, client.oauth.Endpoint, client.oauth.Scopes}
						}
						if spec.wantError == "" && spec.discovery == "" && (client == nil || err != nil) {
							t.Fatal("verified OIDC provider was not discovered")
						}
						outcomes = append(outcomes, []any{config, provider, message})
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatal("trusted provider configuration differs across metadata representations")
					}
				})
			})
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			_, _, err := (&server{store: source}).enterpriseOIDCClient(t.Context(), workspace+"-missing", original.ID)
			if !errors.Is(err, errNotFound) {
				t.Fatalf("missing workspace: %v", err)
			}
		}
		scaleP21SW9Reset(t, repo, workspace, original)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		before := upstream.calls
		if _, _, err := (&server{store: f.probe}).enterpriseOIDCClient(ctx, workspace, original.ID); !errors.Is(err, context.Canceled) || upstream.calls != before {
			t.Fatalf("cancelled point read reached discovery: %v", err)
		}
		upstream.mode = ""
		client, owned, err := (&server{store: f.probe}).enterpriseOIDCClient(t.Context(), workspace, original.ID)
		if err != nil {
			t.Fatal(err)
		}
		beforeRows := scaleP21SW9Hash(t, repo, workspace, "identityProviders", "")
		owned.Domains[0], owned.Scopes[0], owned.RoleMapping["developer"] = "changed.example.test", "changed", "guest"
		client.oauth.Scopes[0] = "changed"
		if scaleP21SW9Hash(t, repo, workspace, "identityProviders", "") != beforeRows {
			t.Fatal("returned provider configuration aliases canonical data")
		}
		large := original
		large.Name = strings.Repeat("large selected provider ", 18000)
		scaleP21SW9Reset(t, repo, workspace, large)
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		if _, _, err := (&server{store: f.probe}).enterpriseOIDCClient(t.Context(), workspace, original.ID); !errors.Is(err, store.ErrMetadataPageBudget) {
			t.Fatalf("large selected provider was silently omitted: %v", err)
		}
		if scaleP21SW9Hash(t, repo, workspace, "identityProviders", original.ID) != unrelated || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("provider lookup changed unrelated rows or decoded complete metadata")
		}
	}
}

func TestScaleP21SW9ProviderVerificationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	upstream := scaleP21SW9Outbound(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		original := scaleP21SW9Providers(t, repo, workspace)
		unrelated := scaleP21SW9Hash(t, repo, workspace, "identityProviders", original.ID)
		projects := scaleP21SW9Hash(t, repo, workspace, "projects", "")
		settings := scaleP21SW9Hash(t, repo, workspace, "settings", "")
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, discovery string
				providerType    string
				status          int
				missing         bool
				unsafe          bool
				disabled        bool
			}{
				{name: "oidc", status: http.StatusOK},
				{name: "saml", providerType: "saml", status: http.StatusOK},
				{name: "disabled", disabled: true, status: http.StatusOK},
				{name: "missing", missing: true, status: http.StatusNotFound},
				{name: "unsafe-issuer", unsafe: true, status: http.StatusUnprocessableEntity},
				{name: "unavailable", discovery: "unavailable", status: http.StatusBadGateway},
				{name: "transport-error", discovery: "transport-error", status: http.StatusBadGateway},
				{name: "invalid-json", discovery: "invalid-json", status: http.StatusUnprocessableEntity},
				{name: "issuer-mismatch", discovery: "different-issuer", status: http.StatusUnprocessableEntity},
				{name: "unsafe-authorization", discovery: "unsafe-authorization", status: http.StatusUnprocessableEntity},
				{name: "unsafe-token", discovery: "unsafe-token", status: http.StatusUnprocessableEntity},
			} {
				t.Run(spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						target := original
						target.DiscoveryStatus = "unverified"
						target.Enabled = !spec.disabled
						if spec.providerType != "" {
							target.Type = spec.providerType
						}
						if spec.unsafe {
							target.Issuer = "http://sw9-idp.example.test/issuer"
						}
						scaleP21SW9Reset(t, repo, workspace, target)
						upstream.mode = spec.discovery
						var webhook domain.DomainEvent
						var realtime domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = event })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = event })
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						id := target.ID
						if spec.missing {
							id += "-missing"
						}
						r.SetPathValue("id", id)
						started := time.Now().UTC()
						scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
						if w.Code != spec.status {
							t.Fatalf("verification status=%d want=%d body=%s", w.Code, spec.status, w.Body.String())
						}
						hydrated, err := f.probe.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{
							Read: []store.RecordKey{{Kind: "identityProviders", ID: target.ID}, {Kind: "users", ID: user.ID}},
						})
						data := hydrated.Data
						if err != nil || len(data.IdentityProviders) != 1 {
							t.Fatal("verification canonical target missing", err)
						}
						if spec.status == http.StatusOK && (data.IdentityProviders[0].LastVerifiedAt == nil || data.IdentityProviders[0].DiscoveryStatus != "verified") {
							t.Fatal("verification did not persist its result")
						}
						entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
						if err != nil || len(entries) != 0 {
							t.Fatal("verification changed audit semantics", err)
						}
						outcomes = append(outcomes, scaleP21SW9Value(t, []any{
							w.Code, scaleP21M3JSON(t, w.Body.Bytes()), data,
							webhook.Type, webhook.AggregateID, webhook.Payload, webhook.PreviousValues, webhook.Snapshot,
							realtime.Type, realtime.AggregateID, realtime.ActorID, realtime.Payload, entries,
						}))
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("verification differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		if scaleP21SW9Hash(t, repo, workspace, "identityProviders", original.ID) != unrelated ||
			scaleP21SW9Hash(t, repo, workspace, "projects", "") != projects ||
			scaleP21SW9Hash(t, repo, workspace, "settings", "") != settings ||
			f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("verification changed unrelated rows or decoded complete metadata")
		}
	}
}

func TestScaleP21SW9ProviderVerificationRacesAndBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	upstream := scaleP21SW9Outbound(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		original := scaleP21SW9Providers(t, repo, workspace)
		unrelated := scaleP21SW9Hash(t, repo, workspace, "identityProviders", original.ID)
		actor := f.principals[1].user
		resume := func() {
			t.Helper()
			_, status, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, actor.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status == "suspended" {
				if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, actor.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, spec := range []struct {
			name   string
			status int
		}{
			{"updated", http.StatusInternalServerError},
			{"issuer", http.StatusInternalServerError},
			{"deleted", http.StatusNotFound},
			{"actor-revoked", http.StatusOK},
			{"cancelled-outbound", http.StatusBadGateway},
		} {
			t.Run(spec.name, func(t *testing.T) {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					resume()
					target := original
					target.DiscoveryStatus = "unverified"
					scaleP21SW9Reset(t, repo, workspace, target)
					r, w := f.request(actor, nil, nil), httptest.NewRecorder()
					r.SetPathValue("id", target.ID)
					ctx, cancel := context.WithCancel(r.Context())
					r = r.WithContext(ctx)
					upstream.mode = ""
					upstream.before = func(*http.Request) {
						switch spec.name {
						case "actor-revoked":
							if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, actor.ID); err != nil {
								t.Fatal(err)
							}
						case "cancelled-outbound":
							cancel()
						default:
							// Both phases mutate the handler's own installed source.
							// A write on the separate fixture owner would instead
							// model a stale peer, which the literal old writer also
							// keeps until its normal freshness/reload boundary.
							if err := source.MutateWorkspaceScoped(store.WithMetadataFields(t.Context(), "identityProviders"), workspace, "scale_p21_sw9.race", target.ID, nil,
								store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "identityProviders", ID: target.ID}}}, func(data *domain.Bootstrap) error {
									for i := range data.IdentityProviders {
										if data.IdentityProviders[i].ID != target.ID {
											continue
										}
										switch spec.name {
										case "updated":
											data.IdentityProviders[i].UpdatedAt = original.UpdatedAt.Add(time.Hour)
										case "issuer":
											data.IdentityProviders[i].Issuer = scaleP21SW9Issuer + "/changed"
										case "deleted":
											data.IdentityProviders = append(data.IdentityProviders[:i], data.IdentityProviders[i+1:]...)
										}
										return nil
									}
									return errNotFound
								}); err != nil {
								t.Fatal(err)
							}
						}
					}
					var webhook domain.DomainEvent
					var realtime domain.RealtimeEvent
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						if event.Type == "identity_provider.verified" {
							webhook = event
						}
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						if event.Type == "identity_provider.verified" {
							realtime = event
						}
					})
					scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
					cancel()
					if w.Code != spec.status {
						t.Fatalf("held verification status=%d want=%d body=%s", w.Code, spec.status, w.Body.String())
					}
					batch, err := f.probe.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{
						{Kind: "identityProviders", ID: target.ID}, {Kind: "users", ID: actor.ID},
					}, "detail")
					if err != nil {
						t.Fatal(err)
					}
					rows := []any{}
					for _, item := range batch.Items {
						if item.Omitted {
							t.Fatal("small race target was omitted")
						}
						rows = append(rows, []any{item.Key, item.Missing, item.Data})
					}
					outcomes = append(outcomes, scaleP21SW9Value(t, []any{
						w.Code, scaleP21M3JSON(t, w.Body.Bytes()), rows,
						webhook.Type, webhook.AggregateID, webhook.Payload, webhook.PreviousValues, webhook.Snapshot,
						realtime.Type, realtime.AggregateID, realtime.ActorID, realtime.Payload,
					}))
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("held verification differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
		resume()
		scaleP21SW9Reset(t, repo, workspace, original)
		withoutAdmin := &domain.APIKey{ID: "sw9-read-key", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, withoutAdmin, nil), httptest.NewRecorder()
			r.SetPathValue("id", original.ID)
			before := upstream.calls
			scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
			if w.Code != http.StatusNotFound || upstream.calls != before {
				t.Fatalf("key without admin scope exposed provider: %d %s", w.Code, w.Body.String())
			}
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("id", original.Name)
			before := upstream.calls
			scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
			if w.Code != http.StatusNotFound || upstream.calls != before {
				t.Fatal("provider name was treated as a canonical ID")
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("verification decoded full metadata before the cross-workspace fixture")
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW9 foreign", idgen.New("sw9-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		foreignProvider := scaleP21SW9Providers(t, repo, foreign.Workspace.URLKey)
		f.probe, err = repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			for _, binding := range []struct{ workspace, provider string }{
				{workspace, foreignProvider.ID}, {foreign.Workspace.URLKey, original.ID},
			} {
				before := upstream.calls
				if _, _, err := (&server{store: source}).enterpriseOIDCClient(t.Context(), binding.workspace, binding.provider); !errors.Is(err, errNotFound) {
					t.Fatalf("provider escaped its workspace: %v", err)
				}
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.Header.Set("X-Workspace-Key", binding.workspace)
				r.SetPathValue("workspaceKey", binding.workspace)
				r.SetPathValue("workspace", binding.workspace)
				r.SetPathValue("id", binding.provider)
				scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
				if w.Code != http.StatusNotFound || upstream.calls != before {
					t.Fatalf("cross-workspace provider verification: %d %s", w.Code, w.Body.String())
				}
			}
		}
		member := f.principals[2].user
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, credential := range scaleP21M3Credentials() {
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					r, w := f.request(member, credential.key, nil), httptest.NewRecorder()
					r.SetPathValue("id", original.ID)
					before := upstream.calls
					scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
					if w.Code != http.StatusForbidden || upstream.calls != before {
						t.Fatalf("%s verification reused revoked authority: %d %s", phase, w.Code, w.Body.String())
					}
				}
			}
		}
		large := original
		large.Name = strings.Repeat("large verification provider ", 18000)
		scaleP21SW9Reset(t, repo, workspace, large)
		beforeRows := scaleP21SW9Hash(t, repo, workspace, "identityProviders", "")
		before := upstream.calls
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			r, w := f.request(f.data.Viewer, withoutAdmin, nil), httptest.NewRecorder()
			r.SetPathValue("id", original.ID)
			scaleP21M3Call(&server{store: source}, w, r, (*server).verifyIdentityProvider)
			if w.Code != http.StatusNotFound || upstream.calls != before {
				t.Fatalf("restricted key learned an oversized provider exists: %d %s", w.Code, w.Body.String())
			}
		}
		r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", original.ID)
		scaleP21M3Call(&server{store: f.probe}, w, r, (*server).verifyIdentityProvider)
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
			upstream.calls != before || scaleP21SW9Hash(t, repo, workspace, "identityProviders", "") != beforeRows {
			t.Fatalf("oversized selected provider did not fail before external work: %d %s", w.Code, w.Body.String())
		}
		scaleP21SW9Reset(t, repo, workspace, original)
		r, w = f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
		r.SetPathValue("id", original.ID)
		scaleP21M3Call(&server{store: f.probe}, w, r, (*server).verifyIdentityProvider)
		if w.Code != http.StatusOK {
			t.Fatalf("request after budget failure did not recover: %d %s", w.Code, w.Body.String())
		}
		if scaleP21SW9Hash(t, repo, workspace, "identityProviders", original.ID) != unrelated || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("race verification changed unrelated providers or decoded full metadata")
		}
	}
}
