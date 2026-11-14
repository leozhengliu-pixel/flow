package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW41PasskeyBaseline(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.seed", "", nil, func(data *domain.Bootstrap) error {
			data.Passkeys = []domain.Passkey{{ID: "sw41-baseline", UserID: f.data.Viewer.ID, Name: "Before", CredentialJSON: "private"}}
			data.Projects = append(data.Projects, domain.Project{ID: "sw41-baseline-unrelated", Description: strings.Repeat("unrelated ", 40000)})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		run := func(source *store.SQLiteStore, logSQL bool) int {
			t.Helper()
			statements := 0
			request := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"Before"}`)
			request.SetPathValue("id", "sw41-baseline")
			request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(query string) {
				statements++
				if logSQL {
					t.Logf("bounded passkey noop SQL %d: %s", statements, strings.Join(strings.Fields(query), " "))
				}
			}))
			response := httptest.NewRecorder()
			(&server{store: source}).updatePasskey(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("baseline status=%d body=%s", response.Code, response.Body.String())
			}
			return statements
		}
		run(repo, false)
		run(probe, false)
		for pair := range 3 {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				sql := run(source, false)
				runtime.ReadMemStats(&after)
				t.Logf("passkey noop pair=%d bounded=%v bytes=%d allocs=%d SQL=%d", pair, source == probe, after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs, sql)
			}
		}
		run(probe, true)
	}
}

func TestScaleP21SW41PasskeyPointDifferential(t *testing.T) {
	seed := func(data *domain.Bootstrap, actor domain.User) {
		data.Passkeys = []domain.Passkey{
			{ID: "sw41-passkey", UserID: actor.ID, Name: "Before", CredentialJSON: `{"private":"before"}`, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: "sw41-other-passkey", UserID: actor.ID + "-other", Name: "Retained", CredentialJSON: strings.Repeat("unrelated credential ", 20000)},
		}
	}
	scaleP21M3PointDifferential(t, []scaleP21M3PointOperation{
		{
			name: "sw41-passkey-update", kind: "passkeys", id: "sw41-passkey", body: `{"name":" After "}`,
			seed: seed, handler: (*server).updatePasskey,
			otherKeys: []store.RecordKey{{Kind: "passkeys", ID: "sw41-other-passkey"}},
		},
		{
			name: "sw41-passkey-noop", kind: "passkeys", id: "sw41-passkey", body: `{"name":"Before"}`,
			seed: seed, handler: (*server).updatePasskey,
			otherKeys: []store.RecordKey{{Kind: "passkeys", ID: "sw41-other-passkey"}},
		},
		{
			name: "sw41-passkey-invalid-name", kind: "passkeys", id: "sw41-passkey", body: `{"name":""}`,
			seed: seed, handler: (*server).updatePasskey,
			otherKeys: []store.RecordKey{{Kind: "passkeys", ID: "sw41-other-passkey"}},
		},
	})
}

func TestScaleP21SW41PasskeyDeletePermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		principals := scaleP21M3Principals(t, f)
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW41 foreign", idgen.New("sw41-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, principal := range principals {
			for _, credential := range scaleP21M3Credentials() {
				for _, foreignOwner := range []bool{false, true} {
					t.Run(principal.name+"/"+credential.name+map[bool]string{false: "/own", true: "/other"}[foreignOwner], func(t *testing.T) {
						var outcomes []any
						for _, current := range []*store.SQLiteStore{repo, probe} {
							repo.SetWebhookSink(nil)
							repo.SetRealtimeSink(nil)
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							owner := principal.user.ID
							if foreignOwner {
								owner += "-other"
							}
							if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.delete_seed", "", nil, func(data *domain.Bootstrap) error {
								data.Passkeys = []domain.Passkey{
									{ID: "sw41-delete", UserID: owner, Name: "Target", CredentialJSON: "private"},
									{ID: "sw41-delete-retained", UserID: principal.user.ID, Name: "Retained", CredentialJSON: strings.Repeat("unrelated credential ", 20000)},
								}
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
								t.Fatal(err)
							}
							var event domain.DomainEvent
							var realtime domain.RealtimeEvent
							current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value })
							current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value })
							r := scaleP21M3Request(workspace, principal.user, credential.key, `{}`)
							r.SetPathValue("id", "sw41-delete")
							started := time.Now().UTC()
							w := httptest.NewRecorder()
							scaleP21M3Call(&server{store: current}, w, r, (*server).deletePasskey)
							want := http.StatusNoContent
							if foreignOwner {
								want = http.StatusNotFound
							}
							if w.Code != want {
								t.Fatalf("bounded=%v status=%d want=%d body=%s", current == probe, w.Code, want, w.Body.String())
							}
							entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
							if err != nil {
								t.Fatal(err)
							}
							wantAudits := 1
							if foreignOwner {
								wantAudits = 0
							}
							if len(entries) != wantAudits {
								t.Fatalf("account audit count=%d want=%d", len(entries), wantAudits)
							}
							for index := range entries {
								if entries[index].ActorID != principal.user.ID || entries[index].Action != "passkey_removed" {
									t.Fatal("account audit subject attribution changed")
								}
								if entries[index].CreatedAt.Before(started) || entries[index].CreatedAt.After(time.Now().UTC()) {
									t.Fatal("account audit did not retain its operation timestamp")
								}
								entries[index].ID, entries[index].Hash = "", ""
								entries[index].Seq = 0
								entries[index].CreatedAt = time.Time{}
							}
							outcomes = append(outcomes, []any{
								w.Code, scaleP21M3JSON(t, w.Body.Bytes()),
								scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "passkeys", ID: "sw41-delete"}),
								scaleP21M3Fingerprint(t, repo, workspace, "passkeys"),
								scaleP21M3JSON(t, event.Payload), scaleP21M3JSON(t, event.PreviousValues),
								scaleP21M3JSON(t, event.Snapshot), scaleP21M3JSON(t, realtime.Payload), event.ActorID, realtime.ActorID, entries,
							})
						}
						if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
							t.Fatal(scaleP21M2Diff(outcomes[0], outcomes[1]))
						}
					})
				}
			}
		}
		member := principals[len(principals)-1].user
		published := 0
		for _, source := range []*store.SQLiteStore{repo, probe} {
			source.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
			source.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
		}
		for _, phase := range []string{"cross-workspace", "missing-workspace", "queued-revocation", "replay-revocation"} {
			key := workspace
			switch phase {
			case "cross-workspace":
				key = foreign.Workspace.URLKey
			case "missing-workspace":
				key = "sw41-missing-" + foreign.Workspace.URLKey
			case "queued-revocation":
				if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
					t.Fatal(err)
				}
			}
			before := scaleP21M3Fingerprint(t, repo, workspace, "passkeys")
			published = 0
			for _, credential := range scaleP21M3Credentials() {
				for _, source := range []*store.SQLiteStore{repo, probe} {
					r := scaleP21M3Request(key, member, credential.key, `{}`)
					r.SetPathValue("id", "sw41-delete")
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, r, (*server).deletePasskey)
					if w.Code != http.StatusForbidden {
						t.Fatalf("%s/%s bounded=%v status=%d", phase, credential.name, source == probe, w.Code)
					}
				}
			}
			if published != 0 || scaleP21M3Fingerprint(t, repo, workspace, "passkeys") != before {
				t.Fatal("failed authority changed passkeys or publication")
			}
		}
	}
}

func TestScaleP21SW41PasskeyAggregateSourceDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, operation := range []struct {
			name, body string
			handler    func(*server, http.ResponseWriter, *http.Request)
		}{
			{"rename", `{"name":"After"}`, (*server).updatePasskey},
			{"delete", `{}`, (*server).deletePasskey},
		} {
			for _, foreignOwner := range []bool{false, true} {
				var outcomes []any
				for _, current := range []*store.SQLiteStore{repo, probe} {
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					owner := f.data.Viewer.ID
					if foreignOwner {
						owner += "-other"
					}
					if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.reset", "", nil, func(data *domain.Bootstrap) error {
						data.Passkeys = []domain.Passkey{
							{ID: "sw41-collision", UserID: owner, Name: "Before\x00warm", CredentialJSON: "private"},
							{ID: "sw41-collision-retained", UserID: f.data.Viewer.ID, Name: "Retained", CredentialJSON: strings.Repeat("unrelated ", 40000)},
						}
						data.Documents = []domain.Document{{
							ID: "sw41-earlier-document", Title: "Earlier",
							ContentData: map[string]any{"id": "sw41-collision", "name": "Earlier\x00warm", "body": strings.Repeat("legal aggregate ", 20000)},
						}}
						data.Webhooks = []domain.Webhook{{ID: "sw41-hook", Enabled: true, URL: "https://example.test/sw41"}}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					unchanged := scaleP21M3Fingerprint(t, repo, workspace, "documents")
					var event domain.DomainEvent
					var realtime domain.RealtimeEvent
					published := 0
					current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value; published++ })
					current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value; published++ })
					r := scaleP21M3Request(workspace, f.data.Viewer, nil, operation.body)
					r.SetPathValue("id", "sw41-collision")
					w := httptest.NewRecorder()
					started := time.Now().UTC()
					operation.handler(&server{store: current}, w, r)
					want := http.StatusOK
					if operation.name == "delete" {
						want = http.StatusNoContent
					}
					if foreignOwner {
						want = http.StatusNotFound
					}
					if w.Code != want {
						t.Fatalf("%s foreign=%v bounded=%v status=%d body=%s", operation.name, foreignOwner, current == probe, w.Code, w.Body.String())
					}
					if !foreignOwner {
						if published != 2 || len(event.Snapshot) <= store.MetadataRecordPageMaxBytes {
							t.Fatalf("legal large aggregate publication missing: count=%d bytes=%d", published, len(event.Snapshot))
						}
						var snapshot struct {
							Entity map[string]any `json:"entity"`
						}
						if err := json.Unmarshal(event.Snapshot, &snapshot); err != nil || snapshot.Entity["name"] != "Earlier\x00warm" {
							t.Fatal("earlier warm nested aggregate did not win", err)
						}
					} else if published != 0 {
						t.Fatal("foreign owner published a mutation")
					}
					if scaleP21M3Fingerprint(t, repo, workspace, "documents") != unchanged {
						t.Fatal("passkey mutation changed its unrelated aggregate source")
					}
					entries, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
					if err != nil {
						t.Fatal(err)
					}
					for index := range entries {
						entries[index].ID, entries[index].Hash = "", ""
						entries[index].Seq = 0
						entries[index].CreatedAt = time.Time{}
					}
					outcomes = append(outcomes, []any{
						w.Code, scaleP21M3JSON(t, w.Body.Bytes()),
						scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "passkeys", ID: "sw41-collision"}),
						scaleP21M3Fingerprint(t, repo, workspace, "passkeys"),
						scaleP21M3JSON(t, event.Payload), scaleP21M3JSON(t, event.PreviousValues),
						scaleP21M3JSON(t, event.Snapshot), scaleP21M3JSON(t, realtime.Payload), realtime.ActorID, entries,
					})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("%s foreign=%v: %s", operation.name, foreignOwner, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("bounded passkey handlers fully hydrated metadata")
		}
	}
}

func TestScaleP21SW41PasskeyCancellationAndTargetBudget(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_sw41.large", "", nil, func(data *domain.Bootstrap) error {
			data.Passkeys = []domain.Passkey{{ID: "sw41-budget", UserID: f.data.Viewer.ID, Name: "Before", CredentialJSON: strings.Repeat("large credential ", 30000)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		before := scaleP21M3Fingerprint(t, repo, workspace, "passkeys")
		published := 0
		probe.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
		probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
		for _, handler := range []func(*server, http.ResponseWriter, *http.Request){
			(*server).updatePasskey, (*server).deletePasskey,
		} {
			for _, source := range []*store.SQLiteStore{repo, probe} {
				r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"After"}`)
				r.SetPathValue("id", "sw41-missing")
				w := httptest.NewRecorder()
				handler(&server{store: source}, w, r)
				if w.Code != http.StatusNotFound {
					t.Fatalf("missing target with unrelated large credential status=%d", w.Code)
				}
			}
		}
		for _, canceled := range []bool{false, true} {
			r := scaleP21M3Request(workspace, f.data.Viewer, nil, `{"name":"After"}`)
			r.SetPathValue("id", "sw41-budget")
			if canceled {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			(&server{store: probe}).updatePasskey(w, r)
			if w.Code >= 200 && w.Code < 300 {
				t.Fatalf("canceled=%v invalid work unexpectedly succeeded", canceled)
			}
			if scaleP21M3Fingerprint(t, repo, workspace, "passkeys") != before || published != 0 {
				t.Fatal("rejected work changed canonical state or publication")
			}
		}
	}
}
