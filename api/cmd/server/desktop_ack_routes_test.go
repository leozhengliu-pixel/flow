package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW135DesktopAcknowledgementRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 1, 1, 2, 3, 0, time.UTC)
	prefix := idgen.New("sw135-")
	selected, unselected := prefix+"selected", prefix+"unselected"
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw135_seed", "", nil, func(data *domain.Bootstrap) error {
		data.NotificationDeliveries = []domain.NotificationDelivery{}
		for _, principal := range f.principals {
			for index, record := range []struct{ channel, notification, status string }{
				{"desktop", selected, "pending"},
				{"desktop", selected, "failed"},
				{"chat", selected, "pending"},
				{"desktop", unselected, "pending-disabled"},
				{"desktop", "", "delivered"},
			} {
				data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{
					ID: fmt.Sprintf("%s%s-%d", prefix, principal.user.ID, index), NotificationID: record.notification,
					RecipientID: principal.user.ID, Channel: record.channel, Status: record.status, Attempts: index,
					Error: fmt.Sprintf("Preserved delivery detail %d", index), CreatedAt: at, UpdatedAt: at,
				})
			}
		}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("desktop route fixture missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	reset := func(t *testing.T) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		var data domain.Bootstrap
		if err := json.Unmarshal(saved, &data); err != nil {
			t.Fatal(err)
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw135_reset", "", nil, func(next *domain.Bootstrap) error {
			*next = data
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
	}
	visit := func(t *testing.T, user domain.User, key *domain.APIKey, authDisabled bool) {
		viewer := user.ID
		if authDisabled {
			viewer = baseline.Viewer.ID
		}
		for _, ids := range [][]string{{selected, selected, "missing"}, {}, {""}} {
			t.Run(fmt.Sprint(ids), func(t *testing.T) {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(t)
					before := scaleP21SW59SQL(t, f)
					prior, err := repo.Events(t.Context(), viewer)
					if err != nil {
						t.Fatal(err)
					}
					existing := map[string]bool{}
					for _, event := range prior {
						existing[event.ID] = true
					}
					got := scaleP21SW59Outcome{}
					checkOwner := func() {
						t.Helper()
						if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
							t.Error("desktop acknowledgement publication escaped output owner")
						}
					}
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						checkOwner()
						got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						checkOwner()
						got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
					})
					request := f.literalRequest(user, key, map[string]any{"notificationIds": ids})
					request.Method, request.URL.Path = http.MethodPost, "/api/desktop-notifications/ack"
					started := time.Now().UTC()
					func() {
						request, closeOutput := beginRequestResourceReads(request)
						defer closeOutput()
						service := &server{store: source, authDisabled: authDisabled}
						mux := http.NewServeMux()
						mux.HandleFunc("POST /api/desktop-notifications/ack", service.acknowledgeDesktopNotifications)
						response := &scaleP21SW59ResponseWriter{ResponseRecorder: httptest.NewRecorder(), t: t, source: source, requireHeld: true}
						mux.ServeHTTP(response, request)
						got.Code, got.Header = response.Code, response.Header().Clone()
						got.Response = slices.Clone(response.Body.Bytes())
					}()
					finished := time.Now().UTC()
					source.SetWebhookSink(nil)
					source.SetRealtimeSink(nil)
					got.Rows = scaleP21SW59SQL(t, f)
					events, err := repo.Events(t.Context(), viewer)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if !existing[event.ID] {
							got.Events = append(got.Events, event)
						}
					}
					if got.Code != http.StatusNoContent || len(got.Response) != 0 || len(got.Events) != 1 || len(got.Webhooks) != 1 || len(got.Realtime) != 1 {
						t.Fatal("desktop route response/event count changed", source.BoundedMetadataEnabled(), got.Code, string(got.Response), len(got.Events), len(got.Webhooks), len(got.Realtime))
					}
					changed := 0
					for index, row := range got.Rows.Content {
						if row.Kind != "delivery" {
							continue
						}
						var delivery domain.NotificationDelivery
						if err := json.Unmarshal(row.Data, &delivery); err != nil {
							t.Fatal(err)
						}
						if delivery.RecipientID != viewer || delivery.Channel != "desktop" || !slices.Contains(ids, delivery.NotificationID) {
							continue
						}
						if delivery.Status != "delivered" || delivery.AcknowledgedAt == nil || delivery.DeliveredAt == nil ||
							!delivery.AcknowledgedAt.Equal(*delivery.DeliveredAt) || !delivery.UpdatedAt.Equal(*delivery.DeliveredAt) ||
							delivery.UpdatedAt.Before(started) || delivery.UpdatedAt.After(finished) {
							t.Fatal("desktop route lost exact matching-delivery clock/status")
						}
						// Only this callback's three assigned clock fields are
						// replaced after validating equality and request bounds.
						delivery.AcknowledgedAt, delivery.DeliveredAt, delivery.UpdatedAt = &at, &at, at
						got.Rows.Content[index].Data, err = json.Marshal(delivery)
						if err != nil {
							t.Fatal(err)
						}
						changed++
					}
					want := 0
					if slices.Contains(ids, selected) {
						want += 2
					}
					if slices.Contains(ids, "") {
						want++
					}
					if changed != want {
						t.Fatal("desktop route selected another recipient/channel or skipped duplicates", changed, want)
					}
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("desktop route leaked work or hydrated full metadata")
					}
					outcomes = append(outcomes, scaleP21SW59Normalized(t, got, before, started, finished))
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatal("desktop route permission/key differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
	}
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		visit(t, user, key, false)
	})
	scaleP21SW10WriteRevocation(t, f, func(service *server, w http.ResponseWriter, r *http.Request) {
		service.acknowledgeDesktopNotifications(w, r)
	}, func(user domain.User, key *domain.APIKey) *http.Request {
		return f.request(user, key, map[string]any{"notificationIds": []string{selected}})
	})
	t.Run("auth-disabled-keeps-installed-viewer", func(t *testing.T) {
		visit(t, f.principals[2].user, nil, true)
	})
}
