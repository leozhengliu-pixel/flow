package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW202Data(data domain.Bootstrap, id, connection string, at time.Time) domain.Bootstrap {
	data.IntegrationDeliveries = []domain.IntegrationDelivery{
		{ID: "sw202-foreign", ConnectionID: "sw202-foreign", Status: "pending"},
		{ID: id, ConnectionID: connection, Status: "pending", Attempts: 1,
			Payload:        json.RawMessage(`{"id":"outbound-entity","body":"complete payload"}`),
			PreviousValues: json.RawMessage(`{"status":"before"}`), CreatedAt: at, UpdatedAt: at},
		{ID: id, ConnectionID: connection, Status: "failed", Attempts: 7,
			Payload: json.RawMessage(`{"untouched":"later duplicate"}`), CreatedAt: at, UpdatedAt: at},
	}
	data.IntegrationConnections = []domain.IntegrationConnection{
		{ID: "sw202-foreign", DeliveryAttempts: 99},
		{ID: connection, Provider: "fixture", Name: "First connection", Status: "connected",
			Config:           map[string]string{"deliveryURL": "https://example.invalid/first", "irrelevant": strings.Repeat("ignored body ", 20000)},
			DeliveryAttempts: 3, CreatedAt: at, UpdatedAt: at},
		{ID: connection, Provider: "fixture", Name: "Untouched duplicate", Status: "connected",
			Config: map[string]string{"deliveryURL": "https://example.invalid/later"}, DeliveryAttempts: 71, CreatedAt: at, UpdatedAt: at},
	}
	return data
}

func scaleP21SW202Mutation(id, event string, at time.Time) func(*domain.Bootstrap) error {
	return func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IntegrationDeliveries, func(value domain.IntegrationDelivery) bool { return value.ID == id })
		if index < 0 {
			return ErrIssueQuery
		}
		delivery := &data.IntegrationDeliveries[index]
		connectionIndex := slices.IndexFunc(data.IntegrationConnections, func(value domain.IntegrationConnection) bool { return value.ID == delivery.ConnectionID })
		if connectionIndex < 0 {
			return ErrIssueQuery
		}
		if event == "integration_delivery.claimed" {
			delivery.Status, delivery.UpdatedAt = "delivering", at
			return nil
		}
		connection := &data.IntegrationConnections[connectionIndex]
		delivery.Attempts++
		delivery.Status, delivery.LastError, delivery.UpdatedAt = "failed", "HTTP 502", at
		delivery.NextAttemptAt = new(at.Add(time.Duration(1<<min(delivery.Attempts, 6)) * time.Minute))
		connection.DeliveryAttempts++
		connection.LastDeliveryAt, connection.LastError = &at, delivery.LastError
		return nil
	}
}

func TestScaleP21SW202DeliveryProfileOrdinalPostimages(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view := scaleP21IssuePersistenceView(t, f, true, false)
	at := time.Date(2026, 11, 16, 2, 3, 4, 5, time.UTC)
	base, found := f.WorkspaceMetadata(f.data.Workspace.URLKey)
	if !found {
		t.Fatal("fixture metadata")
	}
	for _, shape := range []string{"duplicate", "empty", "overlong"} {
		for _, reload := range []bool{false, true} {
			for _, event := range []string{"integration_delivery.claimed", "integration_delivery.retry"} {
				t.Run(fmt.Sprintf("%s/reload=%t/%s", shape, reload, event), func(t *testing.T) {
					id, connection := "sw202-delivery", "sw202-connection"
					if shape == "empty" {
						id, connection = "", ""
					} else if shape == "overlong" {
						id, connection = strings.Repeat("delivery-", 40), strings.Repeat("connection-", 40)
					}
					data := scaleP21SW202Data(cloneBootstrap(base), id, connection, at)
					if event == "integration_delivery.retry" {
						data.IntegrationDeliveries[1].Status = "delivering"
					}
					if reload {
						raw, err := json.Marshal(data)
						if err != nil || json.Unmarshal(raw, &data) != nil {
							t.Fatal("reloaded producer", err)
						}
					}
					source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
					if err != nil {
						t.Fatal(err)
					}
					defer source.Release()
					profile, err := view.prepareIntegrationDeliveryProfile(t.Context(), source, id, event)
					if err != nil {
						t.Fatal(err)
					}
					defer profile.Close()
					literal := cloneBootstrap(data)
					mutate := scaleP21SW202Mutation(id, event, at.Add(time.Hour))
					if err := mutate(&literal); err != nil {
						t.Fatal(err)
					}
					if err := mutate(profile.Input()); err != nil {
						t.Fatal(err)
					}
					next, err := profile.Merge(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					actual, err := next.Select(t.Context(), MetadataRecordScope{Fields: []string{"integrationDeliveries", "integrationConnections"}}, 8<<20)
					next.Release()
					if err != nil || !reflect.DeepEqual(actual.IntegrationDeliveries, literal.IntegrationDeliveries) ||
						!reflect.DeepEqual(actual.IntegrationConnections, literal.IntegrationConnections) {
						t.Fatal("ordinal postimage changed source order, duplicates or untouched bodies", err)
					}
					profile.Close()
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("profile retained work after close")
					}
				})
			}
		}
	}
}

func TestScaleP21SW202DeliveryProfileRejectsUndeclaredEdits(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view := scaleP21IssuePersistenceView(t, f, true, false)
	data, _ := f.WorkspaceMetadata(f.data.Workspace.URLKey)
	data = scaleP21SW202Data(data, "sw202-target", "sw202-connection", time.Date(2026, 11, 16, 1, 2, 3, 4, time.UTC))
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, mode := range []string{"id", "payload", "connection", "count", "other-field", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			profile, err := view.prepareIntegrationDeliveryProfile(t.Context(), source, "sw202-target", "integration_delivery.claimed")
			if err != nil {
				t.Fatal(err)
			}
			defer profile.Close()
			input := profile.Input()
			input.IntegrationDeliveries[0].Status = "delivering"
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "id":
				input.IntegrationDeliveries[0].ID = "changed"
			case "payload":
				input.IntegrationDeliveries[0].Payload[0] = '['
			case "connection":
				input.IntegrationConnections[0].Status = "changed"
			case "count":
				input.IntegrationDeliveries = nil
			case "other-field":
				input.Workspace.Name = "changed"
			case "canceled":
				cancel()
			}
			next, err := profile.Merge(ctx)
			want := ErrBoundedMetadataCoverage
			if mode == "canceled" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || next != nil {
				t.Fatal("profile accepted an undeclared edit", mode, err)
			}
			profile.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("rejected profile retained work")
			}
		})
	}
}

func TestScaleP21SW202DeliveryProfileLargeRawPayload(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	view := scaleP21IssuePersistenceView(t, f, true, false)
	data, _ := f.WorkspaceMetadata(f.data.Workspace.URLKey)
	at := time.Date(2026, 11, 16, 1, 2, 3, 4, time.UTC)
	data = scaleP21SW202Data(data, "sw202-large", "sw202-large-connection", at)
	payload, err := json.Marshal(map[string]string{"body": strings.Repeat("large supported payload ", 180000)})
	if err != nil {
		t.Fatal(err)
	}
	data.IntegrationDeliveries[1].Payload = payload
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	profile, err := view.prepareIntegrationDeliveryProfile(t.Context(), source, "sw202-large", "integration_delivery.claimed")
	if err != nil {
		t.Fatal("legal raw payload refused by per-byte container accounting", err)
	}
	defer profile.Close()
	input := profile.Input()
	if len(input.IntegrationDeliveries[0].Payload) != len(payload) || len(input.IntegrationConnections[0].Config) != 0 {
		t.Fatal("profile truncated payload or materialized irrelevant connection configuration")
	}
	input.IntegrationDeliveries[0].Status, input.IntegrationDeliveries[0].UpdatedAt = "delivering", at.Add(time.Hour)
	next, err := profile.Merge(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next.Release()
	profile.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("large payload owner leaked")
	}
}

func TestScaleP21SW202ActualDeliveryWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, reload := range []bool{false, true} {
		for _, shadow := range []bool{false, true} {
			t.Run(fmt.Sprintf("reload=%t/shadow=%t", reload, shadow), func(t *testing.T) {
				f := scaleP09Database(t)
				workspace, id, connection := f.data.Workspace.URLKey, "sw202-actual", "sw202-actual-connection"
				at := time.Date(2026, 11, 16, 2, 3, 4, 5, time.UTC)
				type outcome struct {
					State                      []json.RawMessage
					Events, Webhooks, Realtime []any
					Result                     domain.IntegrationDelivery
				}
				var results []any
				for _, bounded := range []bool{false, true} {
					f.SetWebhookSink(nil)
					f.SetRealtimeSink(nil)
					if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw202_actual_seed", "", nil, func(data *domain.Bootstrap) error {
						*data = scaleP21SW202Data(*data, id, connection, at)
						data.Webhooks = []domain.Webhook{{ID: "sw202-hook", Enabled: true, URL: "https://example.invalid/hook"}}
						if shadow {
							data.Documents = []domain.Document{{
								ID: "sw202-shadow-document", ContentData: map[string]any{"id": id, "marker": "earlier aggregate occurrence"},
							}}
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					repo := scaleP21IssuePersistenceView(t, f, bounded, reload)
					prior := scaleP21SW63EventIDs(t, repo, workspace)
					got := outcome{}
					repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						got.Webhooks = append(got.Webhooks, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					})
					repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						got.Realtime = append(got.Realtime, []any{event.Type, event.AggregateID, event.ActorID, event.Payload})
					})
					var closes []func()
					bind := func(ctx context.Context) context.Context {
						return WithMetadataOutputOwner(ctx, func(close func()) bool {
							closes = append(closes, close)
							return true
						})
					}
					for index, event := range []string{"integration_delivery.claimed", "integration_delivery.retry"} {
						ctx := bind(t.Context())
						if index == 0 {
							actor := f.data.Viewer
							actor.Via = &domain.ActorBot{ID: "sw202-proxy", Name: "Claim attribution", Type: domain.ActorBotTypeOAuthApplication}
							ctx = ContextWithActor(ctx, actor)
						}
						mutate := scaleP21SW202Mutation(id, event, at.Add(time.Duration(index+1)*time.Hour))
						callback := func(data *domain.Bootstrap) error {
							if err := mutate(data); err != nil {
								return err
							}
							if index == 1 {
								got.Result = data.IntegrationDeliveries[slices.IndexFunc(data.IntegrationDeliveries, func(value domain.IntegrationDelivery) bool { return value.ID == id })]
							}
							return nil
						}
						var err error
						if bounded {
							err = repo.MutateIntegrationDelivery(ctx, workspace, id, event, callback)
						} else {
							err = repo.MutateWorkspace(ctx, workspace, event, id, nil, callback)
						}
						if err != nil {
							t.Fatal("actual delivery mutation", bounded, event, err)
						}
					}
					repo.SetWebhookSink(nil)
					repo.SetRealtimeSink(nil)
					got.State = []json.RawMessage{
						scaleP21SW193Field(t, repo, workspace, "integrationDeliveries"),
						scaleP21SW193Field(t, repo, workspace, "integrationConnections"),
					}
					got.Events = scaleP21SW63NewEvents(t, repo, workspace, prior)
					if len(got.Events) != 2 || len(got.Webhooks) != 2 || len(got.Realtime) != 2 {
						t.Fatal("delivery event/publication count changed", len(got.Events), len(got.Webhooks), len(got.Realtime))
					}
					results = append(results, scaleP21SW63JSON(t, got))
					if bounded && (len(closes) < 2 || repo.MetadataWorkUsedForTesting() == 0) {
						t.Fatal("delivery callback results escaped their containing output owner")
					}
					for _, close := range closes {
						close()
						close()
					}
					if bounded && (repo.MetadataWorkUsedForTesting() != 0 || repo.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("delivery writer retained work or decoded full metadata")
					}
				}
				if !reflect.DeepEqual(results[0], results[1]) {
					t.Fatal("literal/bounded delivery writer differs", scaleP21SW98FirstDiff(results[0], results[1], "delivery"))
				}
			})
		}
	}
}

func TestScaleP21SW202DeliveryEntrySourceAndPrecedence(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	data, _ := f.WorkspaceMetadata(workspace)
	data = scaleP21SW202Data(data, "sw202-entry", "sw202-entry-connection", time.Date(2026, 11, 16, 1, 2, 3, 4, time.UTC))
	data.IntegrationConnections[1].Config["irrelevant"] = strings.Repeat("unused connection body ", 180000)
	data.IntegrationDeliveries[1].Payload = json.RawMessage(`"` + strings.Repeat("unused entry payload ", 180000) + `"`)
	for _, mode := range []string{"normal", "hipaa", "status", "attempts", "connection-missing"} {
		t.Run(mode, func(t *testing.T) {
			current := cloneBootstrap(data)
			switch mode {
			case "hipaa":
				current.WorkspaceSettings.HIPAACompliance = true
			case "status":
				current.IntegrationDeliveries[1].Status = "delivering"
			case "attempts":
				current.IntegrationDeliveries[1].Attempts = 8
			case "connection-missing":
				current.IntegrationDeliveries[1].ConnectionID = "missing-connection"
			}
			f.mu.Lock()
			f.setWorkspaceLocked(workspace, current)
			f.mu.Unlock()
			view := scaleP21IssuePersistenceView(t, f, true, false)
			view.metadataResidency.work.limit = 2 << 20
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			read, found, err := view.BeginIntegrationDeliveryRead(ctx, workspace, "sw202-entry")
			if err != nil || !found || read == nil {
				t.Fatal("contextless entry capture changed original precedence", err)
			}
			if read.HIPAA != (mode == "hipaa") {
				t.Fatal("entry HIPAA generation changed")
			}
			if mode != "hipaa" && (!read.DeliveryFound || read.Delivery.ID != "sw202-entry" ||
				read.Delivery.Attempts != current.IntegrationDeliveries[1].Attempts ||
				read.Delivery.Status != current.IntegrationDeliveries[1].Status || len(read.Delivery.Payload) != 0) {
				t.Fatal("entry guard projection changed first occurrence")
			}
			if mode == "normal" && (!read.ConnectionFound || read.Connection.Config["deliveryURL"] != "https://example.invalid/first" ||
				len(read.Connection.Config) != 1) {
				t.Fatal("entry connection capture selected a later occurrence or retained irrelevant body")
			}
			if mode != "normal" && read.ConnectionFound {
				t.Fatal("entry selected connection ahead of a literal earlier refusal")
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("entry reader leaked work or decoded full metadata")
			}
		})
	}
}

func TestScaleP21SW202DeliveryClaimUsesCurrentConnection(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	data, _ := f.WorkspaceMetadata(workspace)
	data = scaleP21SW202Data(data, "sw202-rebound", "sw202-old-connection", time.Date(2026, 11, 16, 1, 2, 3, 4, time.UTC))
	f.mu.Lock()
	f.setWorkspaceLocked(workspace, data)
	f.mu.Unlock()
	view := scaleP21IssuePersistenceView(t, f, true, false)
	entry, found, err := view.BeginIntegrationDeliveryRead(t.Context(), workspace, "sw202-rebound")
	if err != nil || !found || !entry.ConnectionFound {
		t.Fatal(err)
	}
	defer entry.Close()
	current := cloneBootstrap(data)
	current.IntegrationDeliveries[1].ConnectionID = "sw202-new-connection"
	current.IntegrationConnections = append(current.IntegrationConnections, domain.IntegrationConnection{
		ID: "sw202-new-connection", DeliveryAttempts: 31, Config: map[string]string{"deliveryURL": "https://example.invalid/new"},
	})
	source, err := captureMetadataLogicalSource(t.Context(), current, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	profile, err := view.prepareIntegrationDeliveryProfile(t.Context(), source, "sw202-rebound", "integration_delivery.claimed")
	if err != nil {
		t.Fatal(err)
	}
	defer profile.Close()
	input := profile.Input()
	if len(input.IntegrationConnections) != 1 || input.IntegrationConnections[0].ID != "sw202-new-connection" ||
		input.IntegrationDeliveries[0].ConnectionID != "sw202-new-connection" ||
		entry.Connection.ID != "sw202-old-connection" || entry.Connection.Config["deliveryURL"] != "https://example.invalid/first" {
		t.Fatal("claim connection lookup replaced the independently captured entry endpoint")
	}
}
