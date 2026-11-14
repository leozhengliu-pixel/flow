package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type scaleP21SW42IntegrationOperation struct {
	name string
	run  func(*server, *http.Request, string, string)
}

func scaleP21SW42ArrayFingerprint(t *testing.T, repo *store.SQLiteStore, workspace, kind string) string {
	t.Helper()
	digest := sha256.New()
	shape, err := repo.StreamMetadataArray(t.Context(), workspace, kind, func(_ context.Context, record store.MetadataArrayRecord) error {
		if _, err := fmt.Fprintf(digest, "%d:%d:", record.Ordinal, record.EncodedBytes); err != nil {
			return err
		}
		_, err := io.Copy(digest, record.Reader)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(digest, "%#v", shape); err != nil {
		t.Fatal(err)
	}
	return string(digest.Sum(nil))
}

func scaleP21SW42IntegrationOperations() []scaleP21SW42IntegrationOperation {
	return []scaleP21SW42IntegrationOperation{
		{"test-error", func(s *server, r *http.Request, provider, id string) {
			persistIntegrationTestError(s, r, provider, id, "sw42 failure")
		}},
		{"test-success", func(s *server, r *http.Request, provider, id string) {
			persistIntegrationTestSuccess(s, r, provider, id, time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC))
		}},
		{"oauth-error", func(s *server, r *http.Request, provider, id string) {
			persistOAuthIntegrationError(s, r, provider, id, "sw42 oauth failure")
		}},
	}
}

func scaleP21SW42ResetIntegration(t *testing.T, repo *store.SQLiteStore, workspace string, large bool) {
	t.Helper()
	repo.SetWebhookSink(nil)
	repo.SetRealtimeSink(nil)
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections", "documents", "settings"), workspace, "scale_p21_sw42.reset", "", nil, func(data *domain.Bootstrap) error {
		data.IntegrationConnections = []domain.IntegrationConnection{
			{ID: "sw42-connection", Provider: "gitlab", Name: "warm\x00target", Status: "connected", LastError: "before", Config: map[string]string{"native": "warm\x00config"}},
		}
		for index := range 61 {
			data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{
				ID: fmt.Sprintf("sw42-other-%03d", index), Provider: "github", Name: "retained", Config: map[string]string{"retained": "sibling"},
			})
		}
		body := "nested\x00warm"
		if large {
			body += strings.Repeat("legal large aggregate ", 20000)
		}
		data.Documents = []domain.Document{{
			ID: "doc_sw42_collision", Title: "Earlier aggregate", Creator: data.Viewer,
			ContentData: map[string]any{"id": "sw42-connection", "body": body, "hiddenActor": map[string]any{"id": "sw42-hidden"}},
		}}
		data.Settings["sw42-native"] = int64(9007199254740993)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW42IntegrationOutcome(t *testing.T, f scaleP21M2Fixture, current *store.SQLiteStore, user domain.User, key *domain.APIKey, operation scaleP21SW42IntegrationOperation, variant, source string) any {
	t.Helper()
	workspace := f.data.Workspace.URLKey
	scaleP21SW42ResetIntegration(t, f.repository, workspace, source == "large")
	if source == "reloaded" {
		if err := f.repository.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), f.repository, workspace); err != nil {
		t.Fatal(err)
	}
	if source == "peer" {
		peer, err := f.repository.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		if err := peer.MutateWorkspaceScoped(t.Context(), workspace, "integration.updated", "sw42-connection", nil,
			store.MetadataRecordScope{Write: []store.RecordKey{{Kind: "integrationConnections", ID: "sw42-connection"}}}, func(data *domain.Bootstrap) error {
				data.IntegrationConnections[0].Config["peer"] = "committed"
				return nil
			}); err != nil {
			t.Fatal(err)
		}
	}
	beforeDocument := scaleP21M3Fingerprint(t, f.repository, workspace, "documents")
	beforeSibling := scaleP21SW6RecordHash(t, f.repository, workspace, "integrationConnections", "sw42-other-060")
	event, realtime := domain.DomainEvent{}, domain.RealtimeEvent{}
	webhooks, broadcasts := 0, 0
	current.SetWebhookSink(func(_ string, value domain.DomainEvent) { event = value; webhooks++ })
	current.SetRealtimeSink(func(_ string, value domain.RealtimeEvent) { realtime = value; broadcasts++ })
	defer current.SetWebhookSink(nil)
	defer current.SetRealtimeSink(nil)
	provider, id := "gitlab", "sw42-connection"
	r := f.literalRequest(user, key, nil)
	switch variant {
	case "missing":
		id = "sw42-missing"
	case "wrong-provider":
		provider = "github"
	case "missing-workspace":
		r.Header.Set("X-Workspace-Key", "sw42-missing-workspace")
		r.SetPathValue("workspaceKey", "sw42-missing-workspace")
		r.SetPathValue("workspace", "sw42-missing-workspace")
	case "cancelled":
		ctx, cancel := context.WithCancel(r.Context())
		cancel()
		r = r.WithContext(ctx)
	}
	operation.run(&server{store: current}, r, provider, id)
	want := 1
	if variant != "valid" {
		want = 0
	}
	if webhooks != want || broadcasts != want {
		t.Fatalf("%s/%s/%s publication count=%d/%d want=%d", operation.name, source, variant, webhooks, broadcasts, want)
	}
	if beforeDocument != scaleP21M3Fingerprint(t, f.repository, workspace, "documents") ||
		beforeSibling != scaleP21SW6RecordHash(t, f.repository, workspace, "integrationConnections", "sw42-other-060") {
		t.Fatal("read-only collision or unrelated canonical row changed")
	}
	if want != 0 {
		entity := snapshotEntity(event)
		if entity["id"] != "sw42-connection" || entity["body"] == nil {
			t.Fatal("legacy nested aggregate precedence was not exercised", entity)
		}
	}
	data, found, err := current.WorkspaceMetadataFieldsScoped(t.Context(), workspace,
		store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "integrationConnections", ID: "sw42-connection"}, {Kind: "settings", ID: "sw42-native"}}},
		"integrationConnections", "settings")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if source != "reloaded" && reflect.TypeOf(data.Settings["sw42-native"]) != reflect.TypeFor[int64]() {
		t.Fatal("unrelated native installed value changed type")
	}
	if current == f.probe && current.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("bounded writer decoded full metadata")
	}
	var target *domain.IntegrationConnection
	for _, item := range data.IntegrationConnections {
		if item.ID == "sw42-connection" {
			target = &item
			break
		}
	}
	return scaleP21M2JSON(t, []any{
		scaleP21M2Canonical(t, f.repository, workspace, store.RecordKey{Kind: "integrationConnections", ID: "sw42-connection"}),
		target, data.Settings["sw42-native"],
		event.Type, event.AggregateID, event.Payload, event.PreviousValues, event.Snapshot, realtime.Type, realtime.Payload,
		webhooks, broadcasts,
	}, false)
}

func TestScaleP21SW42IntegrationWritersMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range scaleP21SW42IntegrationOperations() {
				for _, variant := range []string{"valid", "missing", "wrong-provider"} {
					t.Run(operation.name+"/"+variant, func(t *testing.T) {
						old := scaleP21SW42IntegrationOutcome(t, f, repo, user, key, operation, variant, "warm")
						bounded := scaleP21SW42IntegrationOutcome(t, f, f.probe, user, key, operation, variant, "warm")
						if !reflect.DeepEqual(old, bounded) {
							t.Fatal(scaleP21M2Diff(old, bounded))
						}
					})
				}
			}
		})
	}
}

func TestScaleP21SW42IntegrationWriterSourcesAndCancellation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		for _, operation := range scaleP21SW42IntegrationOperations() {
			for _, source := range []string{"reloaded", "peer", "large"} {
				t.Run(operation.name+"/"+source, func(t *testing.T) {
					old := scaleP21SW42IntegrationOutcome(t, f, repo, f.data.Viewer, nil, operation, "valid", source)
					bounded := scaleP21SW42IntegrationOutcome(t, f, f.probe, f.data.Viewer, nil, operation, "valid", source)
					if !reflect.DeepEqual(old, bounded) {
						t.Fatal(scaleP21M2Diff(old, bounded))
					}
				})
			}
			for _, variant := range []string{"missing-workspace", "cancelled"} {
				old := scaleP21SW42IntegrationOutcome(t, f, repo, f.data.Viewer, nil, operation, variant, "warm")
				bounded := scaleP21SW42IntegrationOutcome(t, f, f.probe, f.data.Viewer, nil, operation, variant, "warm")
				if !reflect.DeepEqual(old, bounded) {
					t.Fatal(variant, scaleP21M2Diff(old, bounded))
				}
			}
		}
	}
}

func TestScaleP21SW42IntegrationDuplicateSourceHold(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		for _, operation := range scaleP21SW42IntegrationOperations() {
			for _, current := range []*store.SQLiteStore{repo, f.probe} {
				func() {
					restore := store.ForceFullMutationsForTesting(true)
					defer restore()
					scaleP21SW42ResetIntegration(t, repo, workspace, false)
				}()
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "integrationConnections"), workspace, "scale_p21_sw42.duplicate", "", nil, func(data *domain.Bootstrap) error {
					data.IntegrationConnections = append(data.IntegrationConnections, domain.IntegrationConnection{
						ID: "sw42-connection", Provider: "gitlab", Name: "second occurrence", Status: "connected",
					})
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				_, _, err := f.probe.WorkspaceMetadataScoped(t.Context(), workspace,
					store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "integrationConnections", ID: "sw42-connection"}}})
				if !errors.Is(err, store.ErrMetadataRecordQuery) {
					installed, _ := repo.WorkspaceMetadataFields(workspace, "integrationConnections")
					count := 0
					for _, item := range installed.IntegrationConnections {
						if item.ID == "sw42-connection" {
							count++
						}
					}
					t.Fatalf("duplicate-target witness operation=%s bounded=%v literal-occurrences=%d err=%v", operation.name, current == f.probe, count, err)
				}
				events := 0
				current.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
				current.SetRealtimeSink(func(string, domain.RealtimeEvent) {})
				before := scaleP21SW42ArrayFingerprint(t, repo, workspace, "integrationConnections")
				operation.run(&server{store: current}, f.literalRequest(f.data.Viewer, nil, nil), "gitlab", "sw42-connection")
				current.SetWebhookSink(nil)
				current.SetRealtimeSink(nil)
				if current == repo {
					if events != 1 {
						t.Fatal("legacy first-occurrence writer did not succeed", operation.name, events)
					}
				} else if events != 0 || before != scaleP21SW42ArrayFingerprint(t, repo, workspace, "integrationConnections") {
					t.Fatal("bounded duplicate-target hold changed", operation.name, events)
				}
			}
		}
		t.Log("all three writers remain guarded: legal duplicate target IDs succeed via legacy first-match logic but bounded point selection rejects them")
	}
}
