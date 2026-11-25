package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW226IntegrationPeerShapeTransitions(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	view := &SQLiteStore{db: fixture.db, dialect: fixture.dialect, maxStateBytes: fixture.maxStateBytes, metadataResidency: newMetadataResidency()}
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, fields := range []string{"delivery", "connection", "both"} {
		for _, direction := range []string{"split-to-inline", "inline-to-split"} {
			for _, size := range []int{32, 1 << 20} {
				t.Run(fmt.Sprintf("%s/%s/body%d", fields, direction, size), func(t *testing.T) {
					body := strings.Repeat("x", size)
					before := cloneBootstrap(fixture.data)
					before.IntegrationDeliveries = []domain.IntegrationDelivery{{
						ID: "sw226-shape-delivery", ConnectionID: "sw226-shape-connection",
						Status: "delivering", Attempts: 1, Payload: json.RawMessage(` { "body" : "` + body + `" } `),
						PreviousValues: json.RawMessage(`{"before":true}`), CreatedAt: at, UpdatedAt: at,
					}}
					before.IntegrationConnections = []domain.IntegrationConnection{{
						ID: "sw226-shape-connection", Config: map[string]string{"body": body},
						Scopes: []string{}, Channels: []string{}, DeliveryAttempts: 3, CreatedAt: at, UpdatedAt: at,
					}}
					changeDelivery := fields == "delivery" || fields == "both"
					changeConnection := fields == "connection" || fields == "both"
					if direction == "inline-to-split" {
						if changeDelivery {
							later := before.IntegrationDeliveries[0]
							later.Status, later.Attempts, later.Payload = "delivered", 71, json.RawMessage(`{"later":true}`)
							before.IntegrationDeliveries = append(before.IntegrationDeliveries, later)
						}
						if changeConnection {
							later := before.IntegrationConnections[0]
							later.Config, later.DeliveryAttempts = map[string]string{"later": "preserve occurrence"}, 71
							before.IntegrationConnections = append(before.IntegrationConnections, later)
						}
					}
					requested := scaleP21SW226Clone(before)
					delivery, connection := &requested.IntegrationDeliveries[0], &requested.IntegrationConnections[0]
					delivery.Attempts++
					delivery.Status, delivery.LastError, delivery.UpdatedAt = "failed", "HTTP 502", at.Add(time.Hour)
					delivery.NextAttemptAt = new(delivery.UpdatedAt.Add(4 * time.Minute))
					connection.DeliveryAttempts++
					connection.LastDeliveryAt, connection.LastError = &delivery.UpdatedAt, delivery.LastError
					shapes := map[string]string{}
					wantShape := ""
					if direction == "inline-to-split" {
						wantShape = "array"
					}
					if changeDelivery {
						shapes["integrationDeliveries"] = wantShape
					}
					if changeConnection {
						shapes["integrationConnections"] = wantShape
					}
					peer := scaleP21SW226PeerMutation{
						shapes: shapes,
						mutate: func(data *domain.Bootstrap) error {
							if direction == "inline-to-split" {
								if changeDelivery {
									data.IntegrationDeliveries = append([]domain.IntegrationDelivery{}, data.IntegrationDeliveries[:1]...)
								}
								if changeConnection {
									data.IntegrationConnections = append([]domain.IntegrationConnection{}, data.IntegrationConnections[:1]...)
								}
								return nil
							}
							if changeDelivery {
								later := data.IntegrationDeliveries[0]
								later.Status, later.Attempts, later.Payload = "delivered", 71, json.RawMessage(`{"later":true}`)
								data.IntegrationDeliveries = append(data.IntegrationDeliveries, later)
							}
							if changeConnection {
								later := data.IntegrationConnections[0]
								later.Config, later.DeliveryAttempts = map[string]string{"later": "preserve occurrence"}, 71
								data.IntegrationConnections = append(data.IntegrationConnections, later)
							}
							return nil
						},
					}
					literal := scaleP21SW226Execute(t, fixture, view, before, requested, nil, "", false, "", peer)
					bounded := scaleP21SW226Execute(t, fixture, view, before, requested, nil, "", true, "", peer)
					// These are legal native scalar edits over a separately
					// committed collection-shape change. Success is required;
					// matching budget refusals would not prove compatibility.
					if literal.conflict || bounded.conflict ||
						!bytes.Equal(literal.delivery, bounded.delivery) || !bytes.Equal(literal.connection, bounded.connection) ||
						!reflect.DeepEqual(literal.payloads, bounded.payloads) || !reflect.DeepEqual(literal.previous, bounded.previous) ||
						literal.secret != bounded.secret || !bytes.Equal(literal.root, bounded.root) || !reflect.DeepEqual(literal.rows, bounded.rows) {
						t.Fatal("peer collection shape transition changed literal native or durable results", literal.conflicts, bounded.conflicts)
					}
					if view.MetadataWorkUsedForTesting() != 0 {
						t.Fatal("peer shape transition retained native work")
					}
					if fields == "both" && bounded.rootWrites > 1 {
						t.Fatal("combined full-field transition wrote the original root more than once", bounded.rootWrites)
					}
					t.Logf("fields=%s direction=%s body_bytes=%d bounded_SQL=%d root_updates=%d peak_work=%d", fields, direction, size, bounded.sql, bounded.rootWrites, bounded.peak)
				})
			}
		}
	}
}

func TestScaleP21SW226IntegrationCanonicalIORelease(t *testing.T) {
	repo := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {}})
	sentinel := errors.New("sw226 canonical input failed")
	for _, kind := range []reflect.Type{reflect.TypeFor[domain.IntegrationDelivery](), reflect.TypeFor[domain.IntegrationConnection]()} {
		t.Run(kind.Name(), func(t *testing.T) {
			disk, descriptors, spools := metadataLogicalDiskUsed.Load(), metadataLogicalDescriptorsUsed.Load(), recordSpoolBytes.Load()
			frames, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			raw := `{"id":"delivery","payload":{"body":"` + strings.Repeat("large body ", 100000) + `"}}`
			if kind == reflect.TypeFor[domain.IntegrationConnection]() {
				raw = `{"id":"connection","config":{"body":"` + strings.Repeat("large config ", 100000) + `"},"scopes":["retained"]}`
			}
			input, readBytes := strings.NewReader(raw), 0
			reader := scaleP21SW226ReadAt(func(dst []byte, offset int64) (int, error) {
				if readBytes >= 64<<10 {
					return 0, sentinel
				}
				n, err := input.ReadAt(dst, offset)
				readBytes += n
				return n, err
			})
			node, runErr := integrationCanonicalRecord(t.Context(), repo, frames, legacySessionJSONPart{reader, 0, int64(len(raw))}, kind)
			frames.Close()
			frames.Close()
			if !errors.Is(runErr, sentinel) || node != (metadataLogicalNode{}) || readBytes < 64<<10 {
				t.Fatal("canonical integration hid an input failure or exposed a partial node", runErr, readBytes)
			}
			if repo.MetadataWorkUsedForTesting() != 0 ||
				metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors || recordSpoolBytes.Load() != spools {
				t.Fatal("canonical integration IO failure retained ownership")
			}
		})
	}
}
