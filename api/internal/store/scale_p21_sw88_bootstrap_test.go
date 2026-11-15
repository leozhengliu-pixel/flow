package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW88JSONOracle(t *testing.T, view *SQLiteStore, data domain.Bootstrap, field string, workspaces ...string) int {
	t.Helper()
	workspace := "physical"
	if len(workspaces) > 0 {
		workspace = workspaces[0]
	}
	index, _, found := metadataLogicalField(field)
	if !found {
		t.Fatal("unknown oracle field", field)
	}
	want, wantErr := json.Marshal(reflect.ValueOf(data).Field(index).Interface())
	read, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("installed source", found, err)
	}
	defer read.Close()
	base := view.MetadataWorkUsedForTesting()
	disk := metadataLogicalDiskUsed.Load()
	var output bytes.Buffer
	peak := 0
	writer := &scaleP21SW88Writer{write: func(raw []byte) (int, error) {
		held := view.MetadataWorkUsedForTesting()
		if held <= base {
			t.Error("field output lost independent scratch ownership")
		}
		peak = max(peak, held)
		return output.Write(raw)
	}}
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	err = read.StreamFieldJSON(ctx, field, writer)
	if wantErr != nil {
		if err == nil {
			t.Fatalf("literal Marshal failed but stream succeeded: field=%s error=%v", field, wantErr)
		}
	} else if err != nil || !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("literal JSON changed: field=%s error=%v bytes=%d/%d prefixes=%q/%q", field, err, output.Len(), len(want), output.Bytes()[:min(output.Len(), 256)], want[:min(len(want), 256)])
	}
	if view.MetadataWorkUsedForTesting() != base || metadataLogicalDiskUsed.Load() != disk ||
		statements != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("field stream leaked scratch/disk or read SQL/full metadata", statements, base, view.MetadataWorkUsedForTesting())
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("installed owner leaked")
	}
	return peak
}

type scaleP21SW88Writer struct {
	write func([]byte) (int, error)
}

func (w *scaleP21SW88Writer) Write(raw []byte) (int, error) {
	return w.write(raw)
}

func TestScaleP21SW88LogicalJSONLiteral(t *testing.T) {
	type quoted struct {
		Text string  `json:"text,string"`
		Num  int64   `json:"num,string"`
		Real float32 `json:"real,string"`
		Flag bool    `json:"flag,string"`
	}
	type Embedded struct {
		TeamID string `json:"teamId"`
	}
	type folded struct {
		Embedded
		Other string `json:"other,omitempty"`
	}
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.FixedZone("wire", 7*3600+123))
	special := "\x00\x01\b\f\n\r\t\\\"<>&\u2028\u2029\ufffd\xff\xfe"
	badTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	var nilTime *time.Time
	var nilRaw *json.RawMessage
	var nilTool *domain.AgentToolCall
	rawPointer := json.RawMessage(`{"pointer":"<>&"}`)
	numberPointer := json.Number("1e999")
	toolPointer := domain.AgentToolCall{Arguments: json.RawMessage(`broken`), Result: json.RawMessage(`broken`)}
	activityPointer := domain.ActivityMetadata{"addedLabelIds": `["pointer"]`}
	seven, fortyTwo := 7, 42
	optionalPointer := domain.OptionalInt{Set: true, Value: &seven}
	cases := []struct {
		name  string
		value any
	}{
		{"nil", nil},
		{"string", special},
		{"bytes", []byte(special)},
		{"nil-bytes", []byte(nil)},
		{"empty-bytes", []byte{}},
		{"array", [3]string{"one", special, ""}},
		{"nil-slice", []string(nil)},
		{"empty-slice", []string{}},
		{"heterogeneous", []any{int64(9007199254740993), false, 1.25, "", nil, []any{}, map[string]any{}}},
		{"map-raw-byte-order", map[string]any{"": "empty first", "z": "last", "a\x00": "nul", "\xff": special, "\xfe": "collision after JSON normalization", "\ufffd": "valid rune", "nested": map[string]any{"z": []any{1, 2}, "a": nil}}},
		{"integer-map-keys", map[int]string{-2: "minus", 0: "zero", 2: "two", 10: "ten"}},
		{"time-map-keys", map[time.Time]string{at: "one", at.Add(time.Hour): "two"}},
		{"unsupported-map-key", map[bool]string{true: "no"}},
		{"nil-unsupported-map-key", map[bool]string(nil)},
		{"quoted", quoted{Text: special, Num: 1234567890, Real: 1.25, Flag: true}},
		{"embedded-fields", folded{Embedded: Embedded{TeamID: "team"}}},
		{"number-empty", json.Number("")},
		{"number-overflow", json.Number("1e999")},
		{"number-pointer", &numberPointer},
		{"number-spaces", json.Number("1 ")},
		{"number-invalid", json.Number("broken")},
		{"time", at},
		{"time-pointer", &at},
		{"time-nil", nilTime},
		{"time-invalid", badTime},
		{"optional-unset", domain.OptionalInt{}},
		{"optional-set", domain.OptionalInt{Set: true, Value: &fortyTwo}},
		{"optional-pointer", &optionalPointer},
		{"float-invalid", math.NaN()},
		{"float-infinity", math.Inf(1)},
		{"raw", json.RawMessage(" { \"x\" : \"<>&\u2028\u2029\\\\\\\"\" , \"n\": 1.00e3, \"duplicate\":1,\"duplicate\":null } ")},
		{"raw-nil", json.RawMessage(nil)},
		{"raw-nil-pointer", nilRaw},
		{"raw-pointer", &rawPointer},
		{"raw-empty", json.RawMessage{}},
		{"raw-invalid", json.RawMessage(`{"broken"`)},
		{"tool-empty", domain.AgentToolCall{ID: "tool"}},
		{"tool-partial", domain.AgentToolCall{ID: "partial", Arguments: json.RawMessage(`{"partial":`), Result: json.RawMessage(special)}},
		{"tool-valid", domain.AgentToolCall{ID: "valid", Arguments: json.RawMessage(`{"number":1e999}`), Result: json.RawMessage(`{"key":"<>&"}`), Change: &domain.AgentChange{EntityID: "entity", Before: json.RawMessage(`{"n":1}`)}}},
		{"tool-nil-pointer", nilTool},
		{"tool-pointer", &toolPointer},
		{"activity", domain.ActivityMetadata{"ordinary": special, "addedLabelIds": `["one",null,"\u003c", "\ud800"]`, "removedLabelIds": `null`}},
		{"activity-empty", domain.ActivityMetadata{}},
		{"activity-nil", domain.ActivityMetadata(nil)},
		{"activity-pointer", &activityPointer},
		{"activity-invalid", domain.ActivityMetadata{"addedLabelIds": `["good",123]`}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			data := domain.Bootstrap{Settings: map[string]any{"value": test.value}}
			view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
			scaleP21SW88JSONOracle(t, view, data, "settings")
		})
	}
}

func TestScaleP21SW88LogicalJSONEveryField(t *testing.T) {
	data := domain.Bootstrap{
		Workspace: domain.Workspace{ID: "native\x00workspace", URLKey: "physical"},
		SavedViews: []domain.SavedView{
			{ID: "duplicate", TeamID: "first", Filters: json.RawMessage(`{"x":1,"x":2}`)},
			{ID: "duplicate", TeamID: "second"},
			{ID: "", TeamID: "empty ID"},
		},
		Documents: []domain.Document{{ID: "document", TeamIDs: []string{}, ContentData: map[string]any{"native": int64(9007199254740993)}}},
		Settings:  map[string]any{"native": int64(9007199254740993)},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	typ := reflect.TypeFor[domain.Bootstrap]()
	for index := range typ.NumField() {
		field, _, _ := strings.Cut(typ.Field(index).Tag.Get("json"), ",")
		if field != "" && field != "-" {
			t.Run(field, func(t *testing.T) {
				scaleP21SW88JSONOracle(t, view, data, field)
			})
		}
	}
}

func TestScaleP21SW88LogicalJSONDuplicateTextKeyRefusal(t *testing.T) {
	first := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("first", 7*3600+1))
	second := time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("second", 7*3600+2))
	left, leftErr := first.MarshalText()
	right, rightErr := second.MarshalText()
	if leftErr != nil || rightErr != nil || first == second || !bytes.Equal(left, right) {
		t.Fatal("fixture needs distinct keys with identical text encoding")
	}
	data := domain.Bootstrap{Settings: map[string]any{"value": map[time.Time]string{first: "first", second: "second"}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	read, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	base, disk := view.MetadataWorkUsedForTesting(), metadataLogicalDiskUsed.Load()
	if err := read.StreamFieldJSON(t.Context(), "settings", io.Discard); !errors.Is(err, ErrBoundedMetadataCoverage) ||
		view.MetadataWorkUsedForTesting() != base || metadataLogicalDiskUsed.Load() != disk {
		t.Fatal("ambiguous text-key order was accepted or leaked", err)
	}
}

func TestScaleP21SW88LogicalJSONLifetimeAndFailure(t *testing.T) {
	data := domain.Bootstrap{Settings: map[string]any{"body": strings.Repeat("body <>& ", 100000)}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
	for _, outcome := range []string{"owner-close", "cancel", "writer-error", "short-write", "quota"} {
		t.Run(outcome, func(t *testing.T) {
			read, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
			if err != nil {
				t.Fatal(err)
			}
			defer read.Close()
			source := read.source
			base, disk := view.MetadataWorkUsedForTesting(), metadataLogicalDiskUsed.Load()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sentinel := errors.New("destination refused")
			calls := 0
			writer := &scaleP21SW88Writer{write: func(raw []byte) (int, error) {
				calls++
				if view.MetadataWorkUsedForTesting() <= base {
					t.Error("stream scratch was released before publication")
				}
				switch outcome {
				case "owner-close":
					read.Close()
					source.mu.Lock()
					retained := source.refs >= 2
					source.mu.Unlock()
					if !retained {
						t.Error("stream borrowed its parent's source lifetime")
					}
					return len(raw), nil
				case "cancel":
					cancel()
					return len(raw), nil
				case "writer-error":
					return 0, sentinel
				case "short-write":
					return len(raw) - 1, nil
				default:
					return len(raw), nil
				}
			}}
			limit := view.metadataResidency.work.limit
			if outcome == "quota" {
				view.metadataResidency.work.limit = base + logicalJSONStreamWork - 1
			}
			err = read.StreamFieldJSON(ctx, "settings", writer)
			view.metadataResidency.work.limit = limit
			switch outcome {
			case "owner-close":
				if err != nil || calls == 0 {
					t.Fatal("stream did not retain its own source", calls, err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) || calls != 1 {
					t.Fatal("cancellation was lost or continued publication", calls, err)
				}
			case "writer-error":
				if !errors.Is(err, sentinel) {
					t.Fatal("destination error changed", err)
				}
			case "short-write":
				if !errors.Is(err, io.ErrShortWrite) {
					t.Fatal("short write was accepted", err)
				}
			case "quota":
				if !errors.Is(err, ErrMetadataWorkBudget) || calls != 0 {
					t.Fatal("scratch was allocated/published without admission", calls, err)
				}
			}
			read.Close()
			if view.MetadataWorkUsedForTesting() != 0 || metadataLogicalDiskUsed.Load() != disk {
				t.Fatal("failed stream retained scratch, map stage or generation")
			}
			if err := read.StreamFieldJSON(t.Context(), "settings", io.Discard); !errors.Is(err, errMetadataLogicalClosed) {
				t.Fatal("closed source accepted a stream", err)
			}
		})
	}
	read, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	base := view.MetadataWorkUsedForTesting()
	if err := read.StreamFieldJSON(t.Context(), "unknown", io.Discard); !errors.Is(err, ErrMetadataRecordQuery) || view.MetadataWorkUsedForTesting() != base {
		t.Fatal("unknown field changed or leaked", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := read.StreamFieldJSON(canceled, "settings", io.Discard); !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != base {
		t.Fatal("pre-canceled stream changed or leaked", err)
	}
	largeKey := domain.Bootstrap{Settings: map[string]any{strings.Repeat("key", logicalJSONMapKeyBytes/3+1): "body"}}
	keyView := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": largeKey})
	keyRead, _, err := keyView.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	keyBase, keyDisk := keyView.MetadataWorkUsedForTesting(), metadataLogicalDiskUsed.Load()
	if err := keyRead.StreamFieldJSON(t.Context(), "settings", io.Discard); !errors.Is(err, ErrMetadataWorkBudget) ||
		keyView.MetadataWorkUsedForTesting() != keyBase || metadataLogicalDiskUsed.Load() != keyDisk {
		t.Fatal("oversized map key was materialized or leaked stage/work", err)
	}
	keyRead.Close()
}

func TestScaleP21SW88LogicalJSONWarmReloadAndLargeBodies(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	var peak int
	for _, repetitions := range []int{10000, 100000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "savedViews"), workspace, "scale_p21.sw88", "", nil, func(data *domain.Bootstrap) error {
			data.SavedViews = []domain.SavedView{{ID: "streamed", TeamID: "team", Description: strings.Repeat("body <>& ", repetitions), Filters: json.RawMessage(`{"raw":"` + strings.Repeat("raw body ", repetitions) + `"}`)}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), f.SQLiteStore, workspace); err != nil {
				t.Fatal(err)
			}
			f.mu.RLock()
			literal := f.workspaces[workspace]
			f.mu.RUnlock()
			gotPeak := scaleP21SW88JSONOracle(t, view, literal, "savedViews", workspace)
			if peak == 0 {
				peak = gotPeak
			}
			if gotPeak != peak {
				t.Fatal("10x body growth changed stream scratch", gotPeak, peak)
			}
			t.Logf("body_repetitions=%d reload=%t peak_work=%d", repetitions, reload, gotPeak)
		}
	}
	var bodyPeak int
	for _, scale := range []int{1, 10} {
		body := strings.Repeat("large <>& \xff ", scale*100000)
		values := map[string]any{
			"body":     body,
			"binary":   []byte(body),
			"raw":      json.RawMessage(`{"body":"` + strings.Repeat("r", scale*1000000) + `"}`),
			"tool":     domain.AgentToolCall{Arguments: json.RawMessage(`{"partial":`), Result: json.RawMessage(body)},
			"activity": domain.ActivityMetadata{"addedLabelIds": `["` + strings.Repeat("label", scale*100000) + `"]`},
		}
		for index := range 100 {
			values[fmt.Sprintf("map-%03d", index)] = map[string]any{"nested": fmt.Sprintf("small-%03d", index)}
		}
		data := domain.Bootstrap{Settings: values}
		native := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": data})
		gotPeak := scaleP21SW88JSONOracle(t, native, data, "settings")
		if bodyPeak == 0 {
			bodyPeak = gotPeak
		}
		if gotPeak != bodyPeak {
			t.Fatal("large nested bodies changed scratch", gotPeak, bodyPeak)
		}
		t.Logf("native_body_scale=%d peak_work=%d", scale, gotPeak)
	}
}
