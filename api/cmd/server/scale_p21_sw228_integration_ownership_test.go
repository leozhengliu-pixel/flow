package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21SW228IntegrationEnvelopeFallbacksAndOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), f.data.Workspace.URLKey)
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: view}
		for index, test := range []struct {
			payload  json.RawMessage
			previous json.RawMessage
		}{
			{nil, nil}, {json.RawMessage{}, nil}, {[]byte("null"), []byte("null")},
			{[]byte(`{"id":"first","id":"last","n":9007199254740993,"x":1e-7}`), []byte(`{"body":"<>&\u2028"}`)},
			{[]byte(`{"id":"valid"}`), []byte(`{"invalid"`)},
			{[]byte(`{"n":1e400,"n":2}`), []byte(`null`)},
			{[]byte(`[1,2]`), []byte(`{"unused":true}`)},
			{[]byte(`{"broken"`), []byte(`true`)},
			{[]byte(`{"deep":` + strings.Repeat("[", 160) + `null` + strings.Repeat("]", 160) + `}`), []byte(`{"before":true}`)},
		} {
			t.Run(fmt.Sprint(index), func(t *testing.T) {
				delivery := domain.IntegrationDelivery{Payload: test.payload, PreviousValues: test.previous}
				want := integrationDeliveryPayload(delivery)
				payload, close, err := service.prepareBoundedIntegrationEnvelope(t.Context(), delivery)
				if err != nil || !bytes.Equal(payload, want) || (payload == nil) != (want == nil) {
					t.Fatal("legacy envelope fallback, null/empty or numeric behavior changed", err)
				}
				if view.MetadataWorkUsedForTesting() == 0 {
					t.Fatal("network result escaped output ownership")
				}
				payload = nil
				close()
				close()
				if view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("fallback/decoded/encoded lifetimes leaked")
				}
			})
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, close, err := service.prepareBoundedIntegrationEnvelope(ctx, domain.IntegrationDelivery{
			Payload: json.RawMessage(`{"body":"pure"}`), PreviousValues: json.RawMessage(`{"before":true}`),
		})
		if close != nil {
			close()
		}
		if !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("preclaim cancellation lost precedence or retained work", err)
		}
	}
}

// TestScaleP21SW228IntegrationWorkerNearLimit delivers a 16,711,695-byte
// payload (16,711,732-byte envelope) through the literal and bounded workers.
//
// The background retry write runs under the production 5s deadline in both
// workers. That deadline is environment-bound for this witness: on a loaded
// development machine the literal split-row write alone took 3.8-29s wall
// (it missed the deadline at 5.004s), almost all of it in the record journal's
// catalog summary, which re-reads the 16 MiB row in 16 KiB SUBSTR pieces. The
// deadline is therefore lifted here (one shared value, asserted unchanged) and
// both workers must still succeed with identical outputs.
//
// Lifting it must not hide a bounded-only slowdown. Wall time on a shared
// machine varies several-fold between consecutive legs, so each bounded leg's
// process CPU over the same window is held to stated margins against the
// literal legs of the same run:
//   - against the literal split leg (the ordinary unique-ID row shape, where
//     the deadline binds the literal worker today): at most 1.25x + 250ms, in
//     both shapes;
//   - against the literal leg of its own shape: at most 1.25x + 250ms for split
//     rows and 4x + 1s for the inline shape. Inline collections arise only from
//     duplicate, empty or overlong IDs; there the literal worker rewrites one
//     root without a catalog summary while the bounded writer streams, scans
//     and validates the inline root around the patch (2.0-3.2x literal CPU
//     measured on a loaded machine).
//
// Margins are not judged under the race detector.
func TestScaleP21SW228IntegrationWorkerNearLimit(t *testing.T) {
	if integrationDeliveryPersistTimeout() != 5*time.Second {
		t.Fatal("production retry persistence deadline changed")
	}
	integrationDeliveryPersistOverride.Store(int64(10 * time.Minute))
	defer integrationDeliveryPersistOverride.Store(0)
	var mu sync.Mutex
	cpu := map[string]time.Duration{}
	scaleP21SW202DeliveryWorker(t, []string{
		"near-limit-no-shadow-split", "near-limit-no-shadow-inline",
	}, func(bounded bool, mode string, reload bool, elapsed, used time.Duration) {
		t.Logf("near-limit persistence bounded=%t mode=%s reload=%t wall=%s cpu=%s", bounded, mode, reload, elapsed.Round(time.Millisecond), used.Round(time.Millisecond))
		mu.Lock()
		cpu[fmt.Sprintf("%t/%s/%t", bounded, mode, reload)] = used
		mu.Unlock()
	})
	if t.Failed() || raceDetector {
		return
	}
	within := func(value, limit time.Duration, factor float64, slack time.Duration) bool {
		return value <= time.Duration(float64(limit)*factor)+slack
	}
	for _, reload := range []bool{false, true} {
		key := func(bounded bool, shape string) string {
			return fmt.Sprintf("%t/near-limit-no-shadow-%s/%t", bounded, shape, reload)
		}
		literalSplit, literalInline := cpu[key(false, "split")], cpu[key(false, "inline")]
		boundedSplit, boundedInline := cpu[key(true, "split")], cpu[key(true, "inline")]
		if literalSplit == 0 || literalInline == 0 || boundedSplit == 0 || boundedInline == 0 {
			t.Fatal("near-limit persistence was not measured", cpu)
		}
		if !within(boundedSplit, literalSplit, 1.25, 250*time.Millisecond) ||
			!within(boundedInline, literalSplit, 1.25, 250*time.Millisecond) ||
			!within(boundedInline, literalInline, 4, time.Second) {
			t.Fatalf("bounded near-limit persistence CPU exceeds its stated margin (reload=%t): literal split=%s inline=%s, bounded split=%s inline=%s",
				reload, literalSplit, literalInline, boundedSplit, boundedInline)
		}
	}
}

type scaleP21SW228Coordinator func(context.Context, string, func() error) error

func (run scaleP21SW228Coordinator) WithWorkspaceLock(ctx context.Context, workspace string, apply func() error) error {
	return run(ctx, workspace, apply)
}

func TestScaleP21SW228ResultAdmissionFailureBeforeRetryCommit(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		workspace, id := f.data.Workspace.URLKey, idgen.New("sw228-denial-")
		payload, err := json.Marshal(map[string]string{
			"body": strings.Repeat("<>&", ((16<<20)-(64<<10))/18),
		})
		if err != nil {
			t.Fatal(err)
		}
		var requests atomic.Int32
		destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
			}
			requests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer destination.Close()
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw228_denial_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = nil
			data.WorkspaceSettings.HIPAACompliance = false
			data.IntegrationDeliveries = []domain.IntegrationDelivery{{
				ID: id, ConnectionID: id, EventType: "issue.updated", Status: "pending",
				Payload: payload, PreviousValues: json.RawMessage(`{"before":true}`),
			}}
			data.IntegrationConnections = []domain.IntegrationConnection{{
				ID: id, Provider: "fixture", Config: map[string]string{"deliveryURL": destination.URL},
			}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		var pressure *store.MetadataJSONRead
		defer func() {
			if pressure != nil {
				pressure.Close()
			}
		}()
		var before domain.Bootstrap
		var beforeEvents []domain.DomainEvent
		locks, pressureHeld := 0, 0
		view.SetWorkspaceCoordinator(scaleP21SW228Coordinator(func(ctx context.Context, key string, apply func() error) error {
			locks++
			if key != workspace {
				t.Fatal("worker changed its workspace")
			}
			if locks == 2 {
				// Claim/network inputs have been released. The independent
				// owner leaves enough room for the retry profile, then
				// refuses its later exact result-encoding preflight.
				scaleP21SW202ReloadOracle(t, repo, workspace)
				before, _ = repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
				beforeEvents, err = repo.Events(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				usedBeforePressure := view.MetadataWorkUsedForTesting()
				pressure, err = view.BeginMetadataJSONRead(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := pressure.ReserveEncoded(ctx, 36<<20); err != nil {
					t.Fatal("independent retry pressure failed before the callback", err)
				}
				pressureHeld = view.MetadataWorkUsedForTesting() - usedBeforePressure
			}
			return apply()
		}))
		defer view.SetWorkspaceCoordinator(nil)
		ctx, closeOutput := beginMetadataOutputReads(t.Context())
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer closeOutput()
		result, err := (&server{store: view, authDisabled: true}).processIntegrationDelivery(ctx, workspace, id)
		if !errors.Is(err, store.ErrMetadataWorkBudget) || locks != 2 || requests.Load() != 1 {
			t.Fatal("result preflight did not refuse the one retry attempt", err, locks, requests.Load())
		}
		if result.Attempts != 1 || len(result.Payload) <= 15<<20 {
			// This fixture starts at attempts zero; the captured failed
			// callback increment proves denial happened after result=*d.
			t.Fatal("denial happened before the result encoding preflight")
		}
		scaleP21SW202ReloadOracle(t, repo, workspace)
		after, _ := repo.WorkspaceMetadataFields(workspace, "integrationDeliveries", "integrationConnections")
		afterEvents, err := repo.Events(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if len(before.IntegrationDeliveries) != 1 || before.IntegrationDeliveries[0].Status != "delivering" ||
			before.IntegrationDeliveries[0].Attempts != 0 || len(beforeEvents) != 1 {
			t.Fatal("fixture did not capture the durable claimed preimage")
		}
		if !reflect.DeepEqual(before.IntegrationDeliveries, after.IntegrationDeliveries) ||
			!reflect.DeepEqual(before.IntegrationConnections, after.IntegrationConnections) ||
			!reflect.DeepEqual(beforeEvents, afterEvents) {
			t.Fatal("retry admission failure committed row, counters, timestamps or event")
		}
		if view.MetadataWorkUsedForTesting() <= pressureHeld {
			t.Fatal("failed callback result escaped its parent output lifetime")
		}
		result = domain.IntegrationDelivery{}
		closeOutput()
		if view.MetadataWorkUsedForTesting() != pressureHeld {
			t.Fatal("parent output close released independent pressure or retained native results")
		}
		pressure.Close()
		if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("failed retry retained admission or decoded full metadata")
		}
	}
}
