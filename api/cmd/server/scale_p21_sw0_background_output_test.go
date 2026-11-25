package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW0BackgroundOutputScope(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	ctx, close := beginMetadataOutputReads(ctx)
	count := 0
	if !registerRequestReadClose(ctx, func() { count++ }) {
		t.Fatal("background output did not acquire a lifetime")
	}
	nested, closeNested := beginMetadataOutputReads(ctx)
	if nested != ctx {
		t.Fatal("nested output replaced the operation owner")
	}
	closeNested()
	cancel()
	if count != 0 {
		t.Fatal("nested completion or cancellation released parent output")
	}
	close()
	close()
	if count != 1 || registerRequestReadClose(ctx, func() { count++ }) {
		t.Fatal("completion was not final or idempotent", count)
	}
	request, closeRequest := beginRequestResourceReads(httptest.NewRequest(http.MethodGet, "/", nil))
	defer closeRequest()
	nested, closeNested = beginMetadataOutputReads(request.Context())
	if !registerRequestReadClose(nested, func() { count++ }) {
		t.Fatal("background helper did not borrow request owner")
	}
	closeNested()
	if count != 1 {
		t.Fatal("borrowed scope released enclosing request")
	}
	closeRequest()
	if count != 2 {
		t.Fatal("request completion lost borrowed output")
	}
}

func TestScaleP21SW0WebhookChildOutputFailure(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, project := f.data.Workspace.URLKey, idgen.New("child-output-")
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.child_output", "", nil, func(data *domain.Bootstrap) error {
			data.ProjectUpdates[project] = []domain.ProjectUpdate{{ID: "child", ProjectID: project, Body: "Complete child"}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		event := domain.DomainEvent{
			ID: idgen.New("child-output-event-"), Type: "project_update.updated", AggregateID: project,
			Payload:  json.RawMessage(`{"webhookChild":{"id":"child"}}`),
			Snapshot: json.RawMessage(`{"entity":{"id":"parent","slugId":"parent"}}`),
		}
		service := &server{store: view}
		for _, closed := range []bool{false, true} {
			ctx, close := beginMetadataOutputReads(t.Context())
			owner := &webhookDeliveryReadOwner{}
			ctx = context.WithValue(ctx, webhookDeliveryReadKey{}, owner)
			if closed {
				close()
			}
			result := service.describeWebhookEvent(ctx, workspace, event, "project_updates", "update", "")
			if closed {
				if !errors.Is(result.metadataErr, store.ErrBoundedMetadataCoverage) {
					t.Fatal("child output failure was hidden by event projection", result.metadataErr)
				}
				queries := 0
				service.enqueueWebhookEvent(store.WithSQLStatementObserver(ctx, func(string) { queries++ }), workspace,
					[]domain.Webhook{{ID: "hook", Enabled: true, URL: "https://example.test"}}, result)
				if queries != 0 {
					t.Fatal("failed child projection reached enqueue", queries)
				}
			} else if result.metadataErr != nil || result.version1Only || view.MetadataWorkUsedForTesting() == 0 {
				t.Fatal("child output was omitted or released before use", result.metadataErr)
			}
			close()
			owner.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("event operation leaked child output")
			}
		}
	}
}
