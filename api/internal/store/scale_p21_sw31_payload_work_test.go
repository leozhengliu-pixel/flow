package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW31DensePayload(count int) []byte {
	raw := make([]byte, 1, count*3+1)
	raw[0] = '['
	raw = append(raw, bytes.Repeat([]byte("{},"), count-1)...)
	raw = append(raw, '{', '}', ']')
	return raw
}

func TestScaleP21SW31PayloadWorkDenseJSON(t *testing.T) {
	raw := scaleP21SW31DensePayload(100000)
	work, err := metadataAggregatePayloadJSONWork(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if work <= uint64(len(raw)*16+100000*1024) {
		t.Fatalf("dense JSON lacks container accounting: raw=%d work=%d", len(raw), work)
	}
	plan, err := planMetadataAggregatePayload(t.Context(), metadataAggregateImage{}, metadataAggregateImage{}, "", false, raw)
	if err != nil || plan.RequiredBytes < int(work) {
		t.Fatalf("payload plan work=%d plan=%+v err=%v", work, plan, err)
	}
	if _, err := metadataAggregatePayloadJSONWork(t.Context(), scaleP21SW31DensePayload(250000)); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("process-sized decoded graph was admitted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := metadataAggregatePayloadJSONWork(ctx, raw); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled JSON accounting: %v", err)
	}
}

func TestScaleP21SW31PayloadWorkEncodingBounds(t *testing.T) {
	var binaryArray [4096]byte
	for i := range binaryArray {
		binaryArray[i] = 0xff
	}
	for _, payload := range []any{
		map[string]any{"escaped": strings.Repeat("\x00", 128000)},
		json.RawMessage(scaleP21SW31DensePayload(10000)),
		json.RawMessage(`"` + strings.Repeat("<", 128000) + `"`),
		bytes.Repeat([]byte{0xff}, 1<<20),
		binaryArray,
	} {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		work, err := metadataAggregatePayloadEncodingWork(t.Context(), reflect.ValueOf(payload), 0)
		if err != nil || work < len(raw)*3 {
			t.Fatalf("encoding work=%d encoded=%d err=%v", work, len(raw), err)
		}
	}
}

func TestScaleP21SW31PayloadWorkProducerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, payload := range []any{
		map[string]any{"escaped": strings.Repeat("\x00", 128000)},
		json.RawMessage(scaleP21SW31DensePayload(10000)),
	} {
		f := scaleP09Database(t)
		workspace := f.data.Workspace.URLKey
		type outcome struct {
			previous, snapshot, realtime json.RawMessage
		}
		var results []outcome
		for _, bounded := range []bool{false, true} {
			repo := f.SQLiteStore
			if bounded {
				var err error
				repo, err = f.BoundedMetadataViewForTesting(t.Context())
				if err != nil {
					t.Fatal(err)
				}
			}
			got := outcome{}
			held := func() int {
				repo.metadataResidency.work.mu.Lock()
				defer repo.metadataResidency.work.mu.Unlock()
				return repo.metadataResidency.work.used
			}
			repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
				got.previous, got.snapshot = event.PreviousValues, event.Snapshot
				if bounded {
					work, err := metadataAggregatePayloadJSONWork(t.Context(), event.Payload)
					if err != nil || held() < int(work) {
						t.Errorf("webhook lacks payload ownership: held=%d work=%d err=%v", held(), work, err)
					}
				}
			})
			repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
				got.realtime = event.Payload
				if bounded && held() == 0 {
					t.Error("realtime publication released event ownership")
				}
			})
			if err := repo.MutateWorkspaceScoped(context.Background(), workspace, "application_policy.updated", "sw31-payload", payload,
				MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw31-payload"}}}, func(data *domain.Bootstrap) error {
					data.Settings["sw31-payload"] = "after"
					return nil
				}); err != nil {
				t.Fatalf("bounded=%t: %v", bounded, err)
			}
			if bounded && held() != 0 {
				t.Fatalf("event work leaked: %d", held())
			}
			results = append(results, got)
		}
		if !jsonEqual(results[0].previous, results[1].previous) || !jsonEqual(results[0].snapshot, results[1].snapshot) || !jsonEqual(results[0].realtime, results[1].realtime) {
			t.Fatal("payload publication differs from literal original")
		}
	}
}

type scaleP21SW31PayloadMarshalProbe struct {
	Body  string
	calls *int
}

func (p scaleP21SW31PayloadMarshalProbe) MarshalJSON() ([]byte, error) {
	*p.calls++
	return []byte(`{"ok":true}`), nil
}

func TestScaleP21SW31PayloadWorkPreflight(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	repo, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	payload := scaleP21SW31PayloadMarshalProbe{Body: strings.Repeat("x", 12<<20), calls: &calls}
	emitted := false
	repo.SetWebhookSink(func(string, domain.DomainEvent) { emitted = true })
	err = repo.MutateWorkspaceScoped(context.Background(), f.data.Workspace.URLKey, "application_policy.updated", "sw31-preflight", payload,
		MetadataRecordScope{Write: []RecordKey{{Kind: "settings", ID: "sw31-preflight"}}}, func(data *domain.Bootstrap) error {
			data.Settings["sw31-preflight"] = "must roll back"
			return nil
		})
	if !errors.Is(err, ErrMetadataWorkBudget) || calls != 0 || emitted {
		t.Fatalf("encoding happened before admission: err=%v calls=%d emitted=%t", err, calls, emitted)
	}
	var count int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='sw31-preflight'`, f.data.Workspace.URLKey).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed encoding admission committed target: count=%d err=%v", count, err)
	}
	if repo.metadataResidency.work.used != 0 {
		t.Fatal("failed encoding admission leaked ownership")
	}
}
