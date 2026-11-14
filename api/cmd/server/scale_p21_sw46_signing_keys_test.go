package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"golang.org/x/crypto/ssh"
)

func scaleP21SW46SigningInput(t *testing.T) (string, domain.CommitSigningKey) {
	t.Helper()
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{46}, ed25519.SeedSize))
	block, err := ssh.MarshalPrivateKey(private, "p21-sw46-fixture")
	if err != nil {
		t.Fatal(err)
	}
	raw := string(encodePrivateKeyPEM(block))
	metadata, err := parseCommitSigningKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, metadata
}

func scaleP21SW46SigningJSON(t *testing.T, value any) any {
	t.Helper()
	result := scaleP21M2JSON(t, value, false)
	var normalize func(any)
	normalize = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			for name, child := range item {
				if name == "addedAt" && child != nil {
					item[name] = "<generated-clock>"
				} else {
					normalize(child)
				}
			}
		case []any:
			for _, child := range item {
				normalize(child)
			}
		}
	}
	normalize(result)
	return result
}

func TestScaleP21SW46SigningAddDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CODING_SECRET_KEY_FILE", "")
	t.Setenv("FLOW_CODING_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{46}, 32)))
	private, expected := scaleP21SW46SigningInput(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		f.matrix(t, func(t *testing.T, user domain.User, credential *domain.APIKey) {
			for _, operation := range []string{"add", "replace", "invalid", "missing-workspace", "cancelled"} {
				t.Run(operation, func(t *testing.T) {
					var outcomes []any
					var statements []int
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						repo.SetWebhookSink(nil)
						repo.SetRealtimeSink(nil)
						if err := repo.DeleteSigningKeyMaterial(t.Context(), user.ID); err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { _ = repo.DeleteSigningKeyMaterial(context.Background(), user.ID) })
						if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "userSettings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
							data.Viewer = f.data.Viewer
							settings := domain.UserSettings{UserID: user.ID, Language: "warm\x00language", CodingPromptTemplate: "keep this prompt"}
							if operation == "replace" {
								settings.CommitSigningKey = &domain.CommitSigningKey{Name: "Before", Fingerprint: "before", Type: "ssh", AddedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
							}
							data.UserSettings[user.ID] = settings
							return nil
						}); err != nil {
							t.Fatal(err)
						}
						var event domain.DomainEvent
						var realtime domain.RealtimeEvent
						webhooks, broadcasts := 0, 0
						source.SetWebhookSink(func(_ string, value domain.DomainEvent) { event, webhooks = value, webhooks+1 })
						source.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime, broadcasts = value, broadcasts+1 })
						name := "SW46 signing"
						if operation == "invalid" {
							name = ""
						}
						request := f.request(user, credential, map[string]string{"name": name, "privateKey": private})
						queries := 0
						request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) { queries++ }))
						if operation == "missing-workspace" {
							request.Header.Set("X-Workspace-Key", workspace+"-missing")
							request.SetPathValue("workspaceKey", workspace+"-missing")
							request.SetPathValue("workspace", workspace+"-missing")
						}
						if operation == "cancelled" {
							ctx, cancel := context.WithCancel(request.Context())
							cancel()
							request = request.WithContext(ctx)
						}
						response := httptest.NewRecorder()
						(&server{store: source}).addCommitSigningKey(response, request)
						statements = append(statements, queries)
						success := operation == "add" || operation == "replace"
						if success && response.Code != http.StatusCreated {
							t.Fatalf("signing metadata write failed bounded=%v status=%d", source.BoundedMetadataEnabled(), response.Code)
						}
						if !success && response.Code < 400 {
							t.Fatalf("invalid/cancelled/missing write unexpectedly succeeded: %s", operation)
						}
						hasMaterial, err := repo.HasSigningKeyMaterial(t.Context(), user.ID)
						if err != nil || hasMaterial != success {
							t.Fatalf("encrypted material compensation differs: operation=%s present=%v err=%v", operation, hasMaterial, err)
						}
						materialMatches := false
						if hasMaterial {
							format, material, err := repo.SigningKeyMaterial(t.Context(), user.ID)
							if err != nil {
								t.Fatal(err)
							}
							materialMatches = format == "ssh" && bytes.Equal(material, []byte(private))
						}
						state, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "userSettings", ID: user.ID}}})
						if err != nil {
							t.Fatal(err)
						}
						key := state.Data.UserSettings[user.ID].CommitSigningKey
						if success && (key == nil || key.Fingerprint != expected.Fingerprint || key.Name != name) {
							t.Fatal("canonical signing metadata differs")
						}
						if success && (webhooks != 1 || broadcasts != 1) || !success && (webhooks != 0 || broadcasts != 0) {
							t.Fatalf("publication count differs: operation=%s webhook=%d realtime=%d", operation, webhooks, broadcasts)
						}
						var body any
						if response.Body.Len() != 0 {
							if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
								t.Fatal(err)
							}
						}
						outcomes = append(outcomes, scaleP21SW46SigningJSON(t, []any{
							response.Code, body, state.Data.UserSettings[user.ID], materialMatches,
							event.Type, event.AggregateID, event.Payload, event.PreviousValues, event.Snapshot, realtime.Payload,
						}))
					}
					if operation == "missing-workspace" {
						old, bounded := outcomes[0].([]any), outcomes[1].([]any)
						if old[0] != bounded[0] || reflect.DeepEqual(old[1], bounded[1]) || !reflect.DeepEqual(old[2:], bounded[2:]) {
							t.Fatal("missing installed-source error hold changed; review workspace failure semantics before certification")
						}
					} else if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatal("signing add response/canonical/material/publication differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
					t.Logf("request primary SQL old=%d bounded=%d", statements[0], statements[1])
				})
			}
		})
		repo.SetWebhookSink(nil)
		repo.SetRealtimeSink(nil)
		f.probe.SetWebhookSink(nil)
		f.probe.SetRealtimeSink(nil)
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("signing point write decoded full metadata")
		}
	}
}

func TestScaleP21SW46SigningRemovalAccountSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_CODING_SECRET_KEY_FILE", "")
	t.Setenv("FLOW_CODING_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{46}, 32)))
	private, metadata := scaleP21SW46SigningInput(t)
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace, user := f.data.Workspace.URLKey, f.data.Viewer
		var retained []bool
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			metadata.Name, metadata.AddedAt = "Existing", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "userSettings"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.UserSettings[user.ID] = domain.UserSettings{UserID: user.ID, CommitSigningKey: &metadata}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.PutCodingSigningKey(t.Context(), user.ID, "ssh", metadata.Fingerprint, []byte(private)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.DeleteSigningKeyMaterial(context.Background(), user.ID) })
			request := f.request(user, nil, nil)
			response := httptest.NewRecorder()
			(&server{store: source}).removeCommitSigningKey(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("remove status bounded=%v status=%d", source.BoundedMetadataEnabled(), response.Code)
			}
			hasMaterial, err := repo.HasSigningKeyMaterial(t.Context(), user.ID)
			if err != nil {
				t.Fatal(err)
			}
			retained = append(retained, hasMaterial)
			state, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "userSettings", ID: user.ID}}})
			if err != nil || state.Data.UserSettings[user.ID].CommitSigningKey != nil {
				t.Fatal("metadata removal control", err)
			}
		}
		if retained[0] || !retained[1] {
			t.Fatal("AccountCollections source hold changed; review scalar predicate and removal before certification")
		}
	}
}
