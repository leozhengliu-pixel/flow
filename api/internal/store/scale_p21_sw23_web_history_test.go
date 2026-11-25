package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"runtime"
	"strings"
	"testing"
	"time"

	"flow/api/internal/idgen"
)

func TestScaleP21SW23HistoryPayloadDifferential(t *testing.T) {
	large := strings.Repeat(`large ignored \"value\" \u0041 `, 40000)
	cases := []string{
		`{}`, `null`, `true`, `false`, `[]`, `[{"agentWebAccess":true}]`,
		`{"agentWebSearch":true}`, `{"agentWebAccess":true}`, `{"agentWebSearch":false,"agentWebAccess":null}`,
		`{"agentWebSearch":true,"agentWebAccess":"wrong"}`, `{"agentWebSearch":"wrong","agentWebSearch":true}`,
		`{"agentWebSearch":true,"agentWebSearch":false}`, `{"agentWebAccess":true,"agentWebAccess":null}`,
		`{"AGENTWEBSEARCH":true}`, `{"agentWebSearch":true,"AgentWebSearch":false}`,
		`{"agentWeb\u0053earch":true}`, "{\"agentWebſearch\":true}",
		`{"":true,"agentWebAccess":true}`, `{"agentWebAccess":true,"nested":{"agentWebAccess":false}}`,
		`{"agentWebAccess":0}`, `{"agentWebAccess":[]}`, `{"agentWebAccess":{}}`,
		`{"agentWebSearch":true,"unknown":` + strings.Repeat("9", 30000) + `e999999}`,
		`{"agentWebSearch":true,"unknown":"` + large + `"}`,
		`{"` + strings.Repeat(`long\u006bey`, 3000) + `":false,"agentWebAccess":true}`,
		`{"agentWebAccess":true,"unknown":["a",0,{"escaped\u006bey":"ignored"}]}`,
		`{"agentWebAccess":true} false`, `{"agentWebAccess":true}{}`, `{"agentWebAccess":true`,
		`{"agentWebAccess":true,"bad":"\q"}`, `{"agentWebAccess":true,"bad":"\u00zz"}`,
		`{"agentWebAccess":true,"bad":01}`, `{"agentWebAccess":true,"bad":1.}`,
		`{"agentWebAccess":true,"bad":1e}`, `{"agentWebAccess":true,"bad":1e+}`,
		`{"agentWebAccess":true,"bad":-}`, `{"agentWebAccess":true,"bad":-.1}`,
		"{\"agentWebAccess\":true,\"bad\":1\x00}", "{\"agentWebAccess\":true,\"bad\":\"bad\ntext\"}",
		`{"agentWebAccess":true,"nested":` + strings.Repeat("[", 10001) + `0` + strings.Repeat("]", 10001) + `}`,
	}
	for index, payload := range cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			got, err := sw23HistoryEnabled(strings.NewReader(payload))
			if err != nil || got != agentWebSearchEnabledIn([]byte(payload)) {
				t.Fatalf("projected=%v err=%v legacy=%v bytes=%d", got, err, agentWebSearchEnabledIn([]byte(payload)), len(payload))
			}
		})
	}
	random := rand.New(rand.NewPCG(23, 1002))
	keys := []string{"agentWebAccess", "agentWebSearch", "AGENTWEBACCESS", "AgentWebSearch", "agentWebſearch", "unknown", "", `agentWeb\u0053earch`}
	values := []string{"true", "false", "null", `"wrong"`, "0", "1e999999", "01", `"\q"`, "[]", "{}", `[true,false,null]`, `{"nested":true}`}
	for index := range 10000 {
		var payload strings.Builder
		payload.WriteByte('{')
		for field := range 1 + random.IntN(7) {
			if field > 0 {
				payload.WriteByte(',')
			}
			fmt.Fprintf(&payload, `"%s":%s`, keys[random.IntN(len(keys))], values[random.IntN(len(values))])
		}
		payload.WriteByte('}')
		raw := payload.String()
		if index%4 == 0 {
			offset := random.IntN(len(raw))
			raw = raw[:offset] + string([]byte{byte(random.IntN(256))}) + raw[offset+1:]
		}
		got, err := sw23HistoryEnabled(strings.NewReader(raw))
		if err != nil || got != agentWebSearchEnabledIn([]byte(raw)) {
			t.Fatalf("case=%d projected=%v err=%v legacy=%v payload=%q", index, got, err, agentWebSearchEnabledIn([]byte(raw)), raw)
		}
	}
}

type sw23HistoryFailingReader struct {
	sent bool
	err  error
}

func (r *sw23HistoryFailingReader) Read(output []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(output, `{"agentWebAccess":true,"ignored":"`), nil
	}
	return 0, r.err
}

func TestScaleP21SW23HistoryBoundedMemoryAndErrors(t *testing.T) {
	before := recordSpoolBytes.Load()
	for _, err := range []error{context.Canceled, io.ErrUnexpectedEOF} {
		if _, got := sw23HistoryEnabled(&sw23HistoryFailingReader{err: err}); !errors.Is(got, err) {
			t.Fatalf("payload transport error was hidden: %v", got)
		}
	}
	measure := func(size int) uint64 {
		t.Helper()
		// The repeated body is generated outside the parser, without retaining
		// a second giant test string in the measured working set.
		payload := io.MultiReader(strings.NewReader(`{"agentWebAccess":true,"ignored":"`), io.LimitReader(sw23HistoryRepeatReader{}, int64(size)), strings.NewReader(`"}`))
		runtime.GC()
		var start, end runtime.MemStats
		runtime.ReadMemStats(&start)
		enabled, err := sw23HistoryEnabled(payload)
		runtime.ReadMemStats(&end)
		if err != nil || !enabled {
			t.Fatal("large ignored payload was not projected", err)
		}
		return end.TotalAlloc - start.TotalAlloc
	}
	small, large := measure(64<<10), measure(8<<20)
	t.Logf("history parser allocations:64KiB=%dbytes 8MiB=%dbytes", small, large)
	if large > small+(1<<20) || recordSpoolBytes.Load() != before {
		t.Fatal("ignored payload size grew retained parser work or leaked spool bytes")
	}
}

type sw23HistoryRepeatReader struct{}

func (sw23HistoryRepeatReader) Read(output []byte) (int, error) {
	for index := range output {
		output[index] = 'x'
	}
	return len(output), nil
}

func TestScaleP21SW23HistoryDatabaseDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	prefix := idgen.New("sw23-history-")
	owned := []string{}
	t.Cleanup(func() {
		for _, id := range owned {
			if _, err := f.db.ExecContext(context.Background(), `DELETE FROM domain_events WHERE id=?`, id); err != nil {
				t.Error(err)
			}
		}
	})
	stamp := time.Date(2026, 4, 3, 2, 1, 0, 0, time.UTC)
	insert := func(payload, created string) {
		t.Helper()
		id := fmt.Sprintf("%s-%d", prefix, len(owned))
		if _, err := f.db.ExecContext(t.Context(), `INSERT INTO domain_events(id,event_type,aggregate_id,payload,created_at,workspace_id) VALUES(?,?,?,?,?,?)`, id, "workspace_preferences.updated", "workspace", []byte(payload), created, f.data.Workspace.ID); err != nil {
			t.Fatal(err)
		}
		owned = append(owned, id)
	}
	insert(`{"agentWebAccess":true,"ignored":"`+strings.Repeat("ignored ", 100000)+`"}`, stamp.Format(time.RFC3339Nano))
	insert(`{"AGENTWEBSEARCH":true}`, stamp.Add(-time.Hour).Format(time.RFC3339Nano))
	insert(`{"agentWebSearch":true,"agentWebAccess":"wrong"}`, stamp.Add(-2*time.Hour).Format(time.RFC3339Nano))
	insert(`{"agentWebSearch":true,"agentWebSearch":false}`, stamp.Add(-3*time.Hour).Format(time.RFC3339Nano))
	insert(`{"agentWebAccess":true}`, "invalid-time")
	insert(`{"agentWebAccess":true}`, time.Time{}.Format(time.RFC3339Nano))
	insert(`{"agentWebAccess":true}`, stamp.In(time.FixedZone("offset", 7*3600)).Format(time.RFC3339Nano))
	for range 65 {
		insert(`{"ignored":true}`, stamp.Add(-48*time.Hour).Format(time.RFC3339Nano))
	}
	want, err := f.agentWebSearchFirstEnabled(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	spoolBefore := recordSpoolBytes.Load()
	got, err := view.agentWebSearchFirstEnabledBounded(t.Context())
	if err != nil || !got.Equal(want) {
		t.Fatalf("history database projection=%v legacy=%v err=%v", got, want, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := view.agentWebSearchFirstEnabledBounded(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled history scan: %v", err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
	if _, err := view.agentWebSearchFirstEnabledBounded(t.Context()); !errors.Is(err, ErrRecordSpoolBudget) {
		t.Fatalf("spool budget failure was hidden: %v", err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", fmt.Sprint(defaultRecordSpoolMaxBytes))
	if got, err := view.agentWebSearchFirstEnabledBounded(t.Context()); err != nil || !got.Equal(want) ||
		view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 || recordSpoolBytes.Load() != spoolBefore {
		t.Fatal("history scan did not recover or release bounded work", err)
	}
	var retained int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=? AND event_type=?`, f.data.Workspace.ID, "workspace_preferences.updated").Scan(&retained); err != nil || retained != len(owned) {
		t.Fatal("history scan changed owned event rows", retained, err)
	}
}
