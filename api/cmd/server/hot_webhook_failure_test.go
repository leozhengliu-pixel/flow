package main

import (
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotWebhookFailureAdapter(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, err := store.OpenDatabase(scaleP03DatabaseConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP03Fixture(t, repo)
	workspace, target := f.data.Workspace.URLKey, idgen.New("webhook-failure-adapter-")
	probe, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 1, 2, 3, 0, time.UTC)
	type result struct {
		Failures []domain.WebhookFailureEvent
		Payload  json.RawMessage
		Count    int
	}
	for _, item := range []struct {
		status int
		text   string
	}{
		{503, strings.Repeat("response ", 1000)},
		{-1, " \t\n "},
	} {
		var want result
		for _, bounded := range []bool{false, true} {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "webhooks", "webhookFailureEvents"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
				data.Webhooks = []domain.Webhook{{ID: target, Enabled: true, URL: "https://example.test/canonical"}}
				data.WebhookFailureEvents = nil
				for index := range 55 {
					data.WebhookFailureEvents = append(data.WebhookFailureEvents, domain.WebhookFailureEvent{ID: idgen.New("whfail_fixture_"), WebhookID: target, ExecutionID: "old", CreatedAt: at.Add(time.Duration(index) * time.Minute)})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			source := repo
			if bounded {
				if err := probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
					t.Fatal(err)
				}
				source = probe
			}
			got := result{}
			source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				got.Payload = event.Payload
				got.Count++
			})
			s := &server{store: source}
			s.recordWebhookFailure(workspace, domain.Webhook{ID: target, URL: "https://example.test/attempt"}, flowWebhookEnvelope{ID: "execution"}, item.status, item.text)
			source.SetWebhookSink(nil)
			err := repo.StreamMetadataRecords(t.Context(), workspace, "webhookFailureEvents", func(_ store.RecordKey, input io.Reader) error {
				var failure domain.WebhookFailureEvent
				if err := json.NewDecoder(input).Decode(&failure); err != nil {
					return err
				}
				failure.ID = "generated"
				if failure.ExecutionID == "execution" {
					failure.CreatedAt = at
				}
				got.Failures = append(got.Failures, failure)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Count != 1 || len(got.Failures) != 50 {
				t.Fatal("failure adapter did not persist one exact retention event", bounded, got.Count, len(got.Failures))
			}
			last := got.Failures[len(got.Failures)-1]
			if last.ExecutionID != "execution" || last.URL != "https://example.test/attempt" || item.status > 0 && (last.HTTPStatus == nil || *last.HTTPStatus != item.status) || item.status <= 0 && last.HTTPStatus != nil {
				t.Fatal("failure adapter status/attempt URL changed", bounded)
			}
			if item.status > 0 && len(last.ResponseOrError) != 4096 || item.status < 0 && last.ResponseOrError != "delivery failed" {
				t.Fatal("failure adapter response normalization changed", bounded)
			}
			if !bounded {
				want = got
			} else if !reflect.DeepEqual(got, want) || source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("failure adapter flag differential changed")
			}
			if !slices.IsSortedFunc(got.Failures[:49], func(a, b domain.WebhookFailureEvent) int { return a.CreatedAt.Compare(b.CreatedAt) }) {
				t.Fatal("retained failure order changed")
			}
		}
	}
}
