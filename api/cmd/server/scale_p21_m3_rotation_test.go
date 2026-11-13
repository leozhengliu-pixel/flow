package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP21M3CredentialRotationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace := f.data.Workspace.URLKey
		principals := scaleP21M3Principals(t, f)
		probe, err := repo.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		unrelated := scaleP21M3Fingerprint(t, probe, workspace, "projects")
		for index, principal := range principals {
			for _, credential := range scaleP21M3Credentials() {
				for _, owned := range []bool{false, true} {
					t.Run(principal.name+"/"+credential.name+map[bool]string{true: "/own", false: "/other"}[owned], func(t *testing.T) {
						var results []any
						for _, current := range []*store.SQLiteStore{repo, probe} {
							owner := principals[(index+1)%len(principals)].user.ID
							if owned {
								owner = principal.user.ID
							}
							const oldSecret = "flow_api_m3_fixture_predecessor"
							target := domain.APIKey{
								ID: "m3-rotate", CreatorID: owner, Name: "Target", SecretHash: secretHash(oldSecret), Prefix: "fixture-prefix",
								Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{},
								CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
							}
							if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
								t.Fatal(err)
							}
							if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.reset", "", nil, func(data *domain.Bootstrap) error {
								data.APIKeys = []domain.APIKey{target, {ID: "m3-retained-key", CreatorID: owner, Name: "Retained", SecretHash: "fixture-retained-hash", Scopes: []string{"read", "write"}}}
								return nil
							}); err != nil {
								t.Fatal(err)
							}
							if _, err := current.AuthenticateAPIKeyRecord(t.Context(), workspace, secretHash(oldSecret), nil); err != nil {
								t.Fatal("fixture predecessor does not authenticate")
							}
							otherKey := []store.RecordKey{{Kind: "apiKeys", ID: "m3-retained-key"}}
							before, err := current.ReadCanonicalRecords(t.Context(), workspace, otherKey, "detail")
							if err != nil {
								t.Fatal(err)
							}
							var webhook domain.DomainEvent
							var realtime domain.RealtimeEvent
							current.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhook = event })
							current.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = event })
							r := scaleP21M3Request(workspace, principal.user, credential.key, `{}`)
							r.SetPathValue("id", target.ID)
							started := time.Now().UTC()
							w := httptest.NewRecorder()
							(&server{store: current}).rotateAPIKeySecret(w, r)
							want := http.StatusNotFound
							if owned || principal.name == "admin" || principal.name == "admin-private-outsider" {
								want = http.StatusOK
							}
							if w.Code != want {
								t.Fatalf("rotation permission status=%d want=%d", w.Code, want)
							}
							row, ok, err := probe.WorkspaceMetadataScoped(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "apiKeys", ID: target.ID}}})
							if err != nil || !ok || len(row.APIKeys) != 1 {
								t.Fatal("rotation lost the target credential")
							}
							var response struct {
								Key    domain.APIKey `json:"key"`
								Secret string        `json:"secret"`
							}
							if w.Code == http.StatusOK {
								if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Secret) < 17 {
									t.Fatal("rotation did not return a usable secret")
								}
								if response.Key.SecretHash != "" || response.Key.ClientSecretHash != "" || response.Key.Prefix != response.Secret[:17] || row.APIKeys[0].SecretHash != secretHash(response.Secret) || row.APIKeys[0].Prefix != response.Key.Prefix {
									t.Fatal("replacement credential binding or public stripping changed")
								}
								live, err := current.AuthenticateAPIKeyRecord(t.Context(), workspace, secretHash(response.Secret), nil)
								if err != nil || live.Key.ID != target.ID || live.Key.SubjectID() != owner || !reflect.DeepEqual(live.Key.Scopes, target.Scopes) || live.Key.TeamRestriction != target.TeamRestriction || !reflect.DeepEqual(live.Key.TeamIDs, target.TeamIDs) {
									t.Fatal("replacement changed live authority")
								}
								if _, err := current.AuthenticateAPIKeyRecord(t.Context(), workspace, secretHash(oldSecret), nil); !errors.Is(err, store.ErrAuthForbidden) {
									t.Fatal("predecessor remained usable")
								}
							} else if _, err := current.AuthenticateAPIKeyRecord(t.Context(), workspace, secretHash(oldSecret), nil); err != nil {
								t.Fatal("refused rotation changed authority")
							}
							after, err := current.ReadCanonicalRecords(t.Context(), workspace, otherKey, "detail")
							if err != nil || !reflect.DeepEqual(before, after) {
								t.Fatal("rotation changed another credential")
							}
							audits, err := current.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started})
							if err != nil {
								t.Fatal(err)
							}
							for index := range audits {
								audits[index].ID, audits[index].Hash = "", ""
								audits[index].CreatedAt, audits[index].Seq = time.Time{}, 0
								if w.Code == http.StatusOK && audits[index].Metadata["prefix"] != response.Key.Prefix {
									t.Fatal("audit prefix does not identify the generated credential")
								}
							}
							value := scaleP21M3PointJSON(t, []any{w.Code, scaleP21M3JSON(t, w.Body.Bytes()), row.APIKeys, audits, webhook.Type, webhook.AggregateID, webhook.Payload, webhook.PreviousValues, realtime.Type, realtime.Payload})
							// The random credential was authenticated above. Normalize
							// only its generated bytes, hash and display prefix.
							var normalize func(any)
							normalize = func(value any) {
								replace := func(value any) any {
									if response.Secret == "" {
										return value
									}
									if value == response.Secret {
										return "generated-credential"
									}
									if value == secretHash(response.Secret) {
										return "generated-hash"
									}
									if value == response.Key.Prefix {
										return "generated-prefix"
									}
									return value
								}
								switch item := value.(type) {
								case map[string]any:
									for key, value := range item {
										if _, ok := value.(string); ok {
											item[key] = replace(value)
										} else {
											normalize(value)
										}
									}
								case []any:
									for index, value := range item {
										if _, ok := value.(string); ok {
											item[index] = replace(value)
										} else {
											normalize(value)
										}
									}
								}
							}
							normalize(value)
							results = append(results, value)
						}
						if !reflect.DeepEqual(results[0], results[1]) {
							t.Fatal("rotation response, canonical state, audit or event semantics changed")
						}
					})
				}
			}
		}
		if probe.WorkspaceMetadataDecodes() != 0 || scaleP21M3Fingerprint(t, probe, workspace, "projects") != unrelated {
			t.Fatal("rotation decoded or changed unrelated metadata")
		}
	}
}
