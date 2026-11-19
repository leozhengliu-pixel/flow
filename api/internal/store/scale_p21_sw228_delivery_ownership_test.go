package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP21SW228RawMessageNativeOwnership(t *testing.T) {
	rawCapacity := make(json.RawMessage, 2, 4<<20)
	copy(rawCapacity, "{}")
	for _, input := range []json.RawMessage{
		nil, {}, []byte("null"), []byte(`{"exact":" <>& \u003c "}`), {0, 255, '{'},
		rawCapacity, json.RawMessage(`{"body":"` + strings.Repeat("<>&", 1<<20) + `"}`),
	} {
		t.Run(fmt.Sprintf("nil-%t/len-%d/cap-%d", input == nil, len(input), cap(input)), func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			builder, err := newMetadataLogicalBuilder(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer builder.Close()
			node, err := builder.writeValue(reflect.ValueOf(input), 0, false)
			if err != nil {
				t.Fatal(err)
			}
			owner := &ResourceRead{store: repo}
			defer owner.Close()
			actual, err := owner.integrationDeliveryRawMessage(t.Context(), node)
			if err != nil || !reflect.DeepEqual(actual, input) || cap(actual) != len(actual) {
				t.Fatal("direct native bytes changed content, nil presence or capacity", err)
			}
			wantWork := 0
			if input != nil {
				wantWork = int(((uint64(len(input)) + 8191) &^ uint64(8191)) + 64)
			}
			if repo.MetadataWorkUsedForTesting() != wantWork {
				t.Fatal("exact native allocation was not retained", repo.MetadataWorkUsedForTesting(), wantWork)
			}
			actual = nil
			owner.Close()
			owner.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("native raw byte owner leaked")
			}
		})
	}
	t.Run("backing-io-remains-an-error", func(t *testing.T) {
		repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
		builder, err := newMetadataLogicalBuilder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer builder.Close()
		node, err := builder.writeValue(reflect.ValueOf(json.RawMessage(`{"body":"complete"}`)), 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.storage.file.Truncate(node.offset + node.size - 1); err != nil {
			t.Fatal(err)
		}
		owner := &ResourceRead{store: repo}
		value, err := owner.integrationDeliveryRawMessage(t.Context(), node)
		if !errors.Is(err, io.ErrUnexpectedEOF) || value != nil {
			t.Fatal("short backing source was swallowed as invalid JSON or a partial value", err)
		}
		owner.Close()
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("backing read failure retained native work")
		}
	})
}

func TestScaleP21SW228OwnershipCancellationAndBudgetRecovery(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	raw := json.RawMessage(`{"body":"` + strings.Repeat("<>&", 1<<20) + `"}`)
	node, err := builder.writeValue(reflect.ValueOf(raw), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	repo.metadataResidency.work.limit = 4096
	owner := &ResourceRead{store: repo}
	if value, err := owner.integrationDeliveryRawMessage(t.Context(), node); !errors.Is(err, ErrMetadataWorkBudget) || value != nil {
		t.Fatal("unadmitted native allocation escaped", err)
	}
	owner.Close()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("native admission refusal leaked")
	}
	repo.metadataResidency.work.limit = metadataWorkBytes
	for _, target := range []string{"before", "metadataLogicalByteReader).Read"} {
		t.Run(target, func(t *testing.T) {
			cancelled, cancel := context.WithCancel(t.Context())
			defer cancel()
			var ctx context.Context = cancelled
			if target == "before" {
				cancel()
			} else {
				ctx = &scaleP21SW228CancelContext{Context: cancelled, cancel: cancel, target: target}
			}
			owner := &ResourceRead{store: repo}
			value, err := owner.integrationDeliveryRawMessage(ctx, node)
			if !errors.Is(err, context.Canceled) || value != nil {
				t.Fatal("native read cancellation changed error or returned partial bytes", err)
			}
			owner.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("cancelled native read retained work")
			}
		})
	}
	owner = &ResourceRead{store: repo}
	value, err := owner.integrationDeliveryRawMessage(t.Context(), node)
	if err != nil || !bytes.Equal(value, raw) {
		t.Fatal("same native operation did not recover after denial/cancellation", err)
	}
	value = nil
	owner.Close()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("successful recovery retained work")
	}
}

func TestScaleP21SW228TypedResultExactEncodingPreflight(t *testing.T) {
	payload, err := json.Marshal(map[string]string{
		"body": strings.Repeat("<>&", ((16<<20)-(64<<10))/18),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := domain.IntegrationDelivery{
		ID: "sw228-preflight", Status: "delivered", Attempts: 2, Payload: payload,
		PreviousValues: json.RawMessage(`{"before":true}`),
	}
	literal, err := json.Marshal(result)
	if err != nil || len(literal) <= 15<<20 || len(literal) >= 16<<20 {
		t.Fatal("wrong typed result witness", err, len(literal))
	}
	const page = 64 << 10
	want := ((len(literal)+page-1)/page*page)*6 + (64 << 10)
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	read, err := repo.BeginMetadataJSONRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	baseline := repo.MetadataWorkUsedForTesting()
	repo.metadataResidency.work.limit = baseline + want - 1
	if err := read.ReserveIntegrationDeliveryEncoding(t.Context(), result); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("six-width result reservation escaped its exact boundary", err)
	}
	if repo.MetadataWorkUsedForTesting() != baseline {
		t.Fatal("failed preflight retained temporary exact-encoding bytes")
	}
	read.Close()
	repo.metadataResidency.work.limit = baseline + want
	read, err = repo.BeginMetadataJSONRead(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := read.ReserveIntegrationDeliveryEncoding(t.Context(), result); err != nil {
		t.Fatal("legal typed large result refused at exact admitted width", err)
	}
	if repo.MetadataWorkUsedForTesting() != baseline+want {
		t.Fatal("streamed preflight changed six-width encoding accounting")
	}
	encoded, err := json.Marshal(result)
	if err != nil || !bytes.Equal(encoded, literal) {
		t.Fatal("reserved ordinary result encoding changed legacy bytes", err)
	}
	encoded, literal = nil, nil
	read.Close()
	read.Close()
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("typed result preflight leaked")
	}
	repo.metadataResidency.work.limit = metadataWorkBytes
}

func TestScaleP21SW228ComparisonCloneReleasedAfterMerge(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("unsupported-edit-%t", fail), func(t *testing.T) {
			repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
			raw := json.RawMessage(`{"body":"` + strings.Repeat("x", 1<<20) + `"}`)
			data := domain.Bootstrap{
				Workspace:              domain.Workspace{ID: "sw228-workspace"},
				IntegrationDeliveries:  []domain.IntegrationDelivery{{ID: "sw228", ConnectionID: "sw228", Status: "pending", Payload: raw}},
				IntegrationConnections: []domain.IntegrationConnection{{ID: "sw228"}},
			}
			source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			owned, err := repo.prepareIntegrationDeliveryProfile(t.Context(), source, "sw228", "integration_delivery.claimed")
			if err != nil {
				t.Fatal(err)
			}
			profile := owned.(*integrationDeliveryProfile)
			defer profile.Close()
			held := repo.MetadataWorkUsedForTesting()
			profile.data.IntegrationDeliveries[0].Status = "delivering"
			profile.data.IntegrationDeliveries[0].UpdatedAt = time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
			captured := profile.data.IntegrationDeliveries[0]
			bodyAt := bytes.Index(captured.Payload, []byte(`"body":"`)) + len(`"body":"`)
			if bodyAt < len(`"body":"`) || captured.Payload[bodyAt] != 'x' {
				t.Fatal("wrong retained-byte fixture")
			}
			if fail {
				// The independent comparison must remain owned if Merge
				// rejects, while captured callback data stays available.
				captured.Payload[bodyAt] = 'y'
			}
			merged, err := profile.Merge(t.Context())
			if fail {
				node, sourceErr := issueHotField(t.Context(), profile.deliveryNode, "Payload")
				equal, compareErr := integrationDeliveryRawFrameEqual(t.Context(), node, raw)
				if !errors.Is(err, ErrBoundedMetadataCoverage) || merged != nil ||
					profile.comparison == nil || len(profile.original.IntegrationDeliveries) != 1 ||
					sourceErr != nil || compareErr != nil || !equal ||
					captured.Payload[bodyAt] != 'y' {
					t.Fatal("failed merge released comparisons or captured native data", err)
				}
				captured = domain.IntegrationDelivery{}
				profile.Close()
				if repo.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("failed profile retained work")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer merged.Release()
			if profile.comparison != nil || !reflect.DeepEqual(profile.original, domain.Bootstrap{}) {
				t.Fatal("successful merge retained the private comparison clone")
			}
			if used := repo.MetadataWorkUsedForTesting(); used == 0 || used >= held {
				t.Fatal("comparison release removed native output ownership or retained header comparisons", used, held)
			}
			if !bytes.Equal(captured.Payload, raw) {
				t.Fatal("comparison release changed captured first-callback native bytes")
			}
			captured.Payload[bodyAt] = 'y'
			index, _, _ := metadataLogicalField("integrationDeliveries")
			var written bytes.Buffer
			if err := merged.fields[index].Children(t.Context(), func(_ reflect.Value, node metadataLogicalNode) error {
				payload, err := issueHotField(t.Context(), node, "Payload")
				if err != nil {
					return err
				}
				return payload.StreamBytes(t.Context(), func(reader io.Reader) error {
					_, err := io.Copy(&written, reader)
					return err
				})
			}); err != nil || !bytes.Equal(written.Bytes(), raw) {
				t.Fatal("finished delta/transform retained a native before/after byte reference", err)
			}
			captured = domain.IntegrationDelivery{}
			profile.Close()
			profile.Close()
			if repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("captured output owner retained work")
			}
		})
	}
}

func TestScaleP21SW228IntegrationProfileNearLimitSourceModes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	plain := strings.Repeat("<>&", ((16<<20)-(64<<10))/18)
	payload, err := json.Marshal(map[string]string{"body": plain})
	if err != nil || len(payload) <= 15<<20 || len(payload) >= 16<<20 {
		t.Fatal("near-limit witness has wrong raw JSON width", err, len(payload))
	}
	for _, shape := range []string{"split", "duplicate-inline"} {
		for _, reload := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reload-%t", shape, reload), func(t *testing.T) {
				at := time.Date(2026, 11, 16, 1, 2, 3, 0, time.UTC)
				want := domain.IntegrationDelivery{
					ID: "sw228-delivery", ConnectionID: "sw228-connection", Status: "pending",
					Payload: payload, PreviousValues: json.RawMessage(`{"n":9007199254740993}`),
					CreatedAt: at, UpdatedAt: at,
				}
				restore := ForceFullMutationsForTesting(true)
				err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw228_fixture", "", nil, func(data *domain.Bootstrap) error {
					data.IntegrationDeliveries = []domain.IntegrationDelivery{want}
					if shape == "duplicate-inline" {
						data.IntegrationDeliveries = append(data.IntegrationDeliveries, domain.IntegrationDelivery{ID: want.ID, Status: "failed"})
					}
					data.IntegrationConnections = []domain.IntegrationConnection{{ID: want.ConnectionID}}
					return nil
				})
				restore()
				if err != nil {
					t.Fatal(err)
				}
				view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				if reload {
					if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`, workspace); err != nil {
						t.Fatal(err)
					}
					if err := view.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal("actual bounded SQL reload", err)
					}
				}
				source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				profile, err := view.prepareIntegrationDeliveryProfile(t.Context(), source, want.ID, "integration_delivery.claimed")
				if err != nil {
					t.Fatal("legal escaped near-limit profile refused", err)
				}
				defer profile.Close()
				delivery := &profile.Input().IntegrationDeliveries[0]
				if !reflect.DeepEqual(*delivery, want) || cap(delivery.Payload) != len(delivery.Payload) {
					t.Fatal("near-limit native profile changed exact SQL/native input")
				}
				native := profile.(*integrationDeliveryProfile)
				node, sourceErr := issueHotField(t.Context(), native.deliveryNode, "Payload")
				equal, compareErr := integrationDeliveryRawFrameEqual(t.Context(), node, delivery.Payload)
				if len(delivery.Payload) == 0 || native.original.IntegrationDeliveries[0].Payload != nil ||
					sourceErr != nil || compareErr != nil || !equal {
					t.Fatal("independent original frames differ from callback bytes or retained a second byte clone")
				}
				if view.MetadataWorkUsedForTesting() == 0 || view.WorkspaceMetadataDecodes() != 0 {
					t.Fatal("native profile escaped ownership or decoded full metadata")
				}
				t.Logf("decoded_string_bytes=%d encoded_payload_bytes=%d held_work=%d",
					len(plain), len(payload), view.MetadataWorkUsedForTesting())
				profile.Close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("near-limit profile retained native ownership")
				}
			})
		}
	}
}
