package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW85Oracle(t *testing.T, literal, view *SQLiteStore, ctx context.Context, workspace, field, id string) int {
	t.Helper()
	var want RealtimeRecordHeader
	raw, found := literal.MetadataRecord(workspace, field, id)
	found = found && json.Unmarshal(raw, &want) == nil
	if !found {
		want = RealtimeRecordHeader{}
	}
	read, workspaceFound, err := view.BeginRealtimeRecordRead(ctx, workspace, field, id)
	key := workspace
	if key == "" {
		key = literal.lastWorkspaceKey
	}
	_, wantWorkspace := literal.workspaces[key]
	if id == "" {
		wantWorkspace = false
	}
	if err != nil || workspaceFound != wantWorkspace {
		t.Fatalf("%q/%q/%q workspace=%t want=%t error=%v", workspace, field, id, workspaceFound, wantWorkspace, err)
	}
	if !workspaceFound {
		if read != nil {
			t.Fatal("missing workspace returned a reader")
		}
		return 0
	}
	if read == nil || read.Found != found || !reflect.DeepEqual(read.Data, want) {
		t.Fatalf("%q/%q/%q got=%+v want=%+v found=%t", workspace, field, id, read, want, found)
	}
	held := view.MetadataWorkUsedForTesting()
	if held == 0 {
		t.Fatal("header has no processing/output ownership")
	}
	read.Close()
	read.Close()
	if read.Found || !reflect.DeepEqual(read.Data, RealtimeRecordHeader{}) {
		t.Fatal("closed reader retained its output")
	}
	return held
}

func TestScaleP21SW85RealtimeRecordLiteralTable(t *testing.T) {
	data := domain.Bootstrap{}
	root := reflect.ValueOf(&data).Elem()
	// Cover every literal persisted slice-of-struct family, including those
	// without any authorization header fields and those without an ID.
	var fields []string
	for _, field := range persistedMetadataFields() {
		target := root.Field(field.index)
		if target.Kind() != reflect.Slice || target.Type().Elem().Kind() != reflect.Struct {
			continue
		}
		fields = append(fields, field.name)
		rows := reflect.MakeSlice(target.Type(), 4, 4)
		for ordinal := range rows.Len() {
			row := rows.Index(ordinal)
			for index := range row.NumField() {
				value := row.Field(index)
				if !value.CanSet() {
					continue
				}
				switch value.Kind() {
				case reflect.String:
					value.SetString("first\x00value")
				case reflect.Slice:
					if value.Type() == reflect.TypeFor[[]string]() {
						switch ordinal {
						case 0:
							value.Set(reflect.ValueOf([]string{"one", "", "one", "two\x00id"}))
						case 1:
							value.Set(reflect.ValueOf([]string{"later"}))
						case 2:
							value.Set(reflect.ValueOf([]string{}))
						}
					}
				}
			}
			if id := row.FieldByName("ID"); id.IsValid() && id.Kind() == reflect.String {
				switch ordinal {
				case 0, 1:
					id.SetString("duplicate")
				case 2:
					id.SetString("empty-slices")
				case 3:
					id.SetString("nil-slices")
				}
			}
			if slug := row.FieldByName("SlugID"); slug.IsValid() && slug.Kind() == reflect.String {
				slug.SetString("alias-only")
			}
		}
		target.Set(rows)
	}
	values := map[string]domain.Bootstrap{"physical\x00key": data, "other": {SavedViews: []domain.SavedView{{ID: "duplicate", TeamID: "other-team"}}}}
	literal := &SQLiteStore{workspaces: values, lastWorkspaceKey: "physical\x00key"}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = literal.lastWorkspaceKey
	statements := 0
	ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			for _, id := range []string{"duplicate", "empty-slices", "nil-slices", "alias-only", "missing", "", " duplicate", "DUPLICATE", "duplicate\x00"} {
				scaleP21SW85Oracle(t, literal, view, ctx, "", field, id)
			}
		})
	}
	for _, field := range []string{"SavedViews", "savedviews", "unknown", "settings", "workspace", "issues", "comments", "projectUpdates"} {
		scaleP21SW85Oracle(t, literal, view, ctx, "", field, "duplicate")
	}
	for _, workspace := range []string{"other", "missing", " physical\x00key", "PHYSICAL\x00KEY"} {
		scaleP21SW85Oracle(t, literal, view, ctx, workspace, "savedViews", "duplicate")
	}
	if statements != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("literal table performed SQL, decoded metadata, or leaked work", statements)
	}
}

func TestScaleP21SW85RealtimeRecordFullEncoding(t *testing.T) {
	badTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	type fixture struct {
		name, field string
		data        domain.Bootstrap
	}
	cases := []fixture{
		{"invalid-selected-time", "customers", domain.Bootstrap{Customers: []domain.Customer{{ID: "selected", OwnerID: "owner", CreatedAt: badTime}}}},
		{"invalid-selected-float", "customers", domain.Bootstrap{Customers: []domain.Customer{{ID: "selected", OwnerID: "owner", AnnualRevenue: math.NaN()}}}},
		{"invalid-selected-raw", "savedViews", domain.Bootstrap{SavedViews: []domain.SavedView{{ID: "selected", TeamID: "team", Filters: json.RawMessage(`{"broken"`)}}}},
		{"empty-selected-raw", "savedViews", domain.Bootstrap{SavedViews: []domain.SavedView{{ID: "selected", TeamID: "team", Filters: json.RawMessage{}}}}},
		{"duplicate-raw-key", "savedViews", domain.Bootstrap{SavedViews: []domain.SavedView{{ID: "selected", TeamID: "team", Filters: json.RawMessage(`{"teamId":"one","teamId":null}`)}}}},
		{"invalid-unselected", "customers", domain.Bootstrap{Customers: []domain.Customer{{ID: "unselected", AnnualRevenue: math.NaN()}, {ID: "selected", OwnerID: "owner"}, {ID: "selected", AnnualRevenue: math.NaN()}}, SavedViews: []domain.SavedView{{ID: "unrelated", Filters: json.RawMessage(`broken`)}}}},
		{"invalid-first-duplicate", "customers", domain.Bootstrap{Customers: []domain.Customer{{ID: "selected", AnnualRevenue: math.NaN()}, {ID: "selected", OwnerID: "later"}}}},
		{"overflow-unknown-number", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", TeamIDs: []string{"team"}, ContentData: map[string]any{"number": json.Number("1e999")}}}}},
		{"invalid-map-key-type", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", ContentData: map[string]any{"invalid": map[bool]string(nil)}}}}},
		{"invalid-number", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", ContentData: map[string]any{"number": json.Number("broken")}}}}},
		{"normalized-key-collision", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", ContentData: map[string]any{"\xff": math.NaN(), "\xfe": "valid"}}}}},
		{"supported-tool-codec", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", TeamIDs: []string{"team"}, ContentData: map[string]any{"call": domain.AgentToolCall{Arguments: json.RawMessage(`partial`), Result: json.RawMessage(`partial`)}}}}}},
		{"supported-activity-codec-invalid", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", ContentData: map[string]any{"activity": domain.ActivityMetadata{"addedLabelIds": `broken`}}}}}},
		{"supported-activity-codec-valid", "documents", domain.Bootstrap{Documents: []domain.Document{{ID: "selected", ContentData: map[string]any{"activity": domain.ActivityMetadata{"addedLabelIds": `["one","two"]`}}}}}},
		{"invalid-utf8-header", "savedViews", domain.Bootstrap{SavedViews: []domain.SavedView{{ID: "selected", TeamID: "team\xff", OwnerID: "owner\xfe", Scope: "\x00scope"}}}},
		{"exact-nul-id", "savedViews", domain.Bootstrap{SavedViews: []domain.SavedView{{ID: "selected\x00", TeamID: "wrong"}, {ID: "selected", TeamID: "right"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]domain.Bootstrap{"physical": test.data}
			literal := &SQLiteStore{workspaces: values}
			view := scaleP21HotLogoStore(t, values)
			scaleP21SW85Oracle(t, literal, view, t.Context(), "physical", test.field, "selected")
			if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("encoding case leaked or hydrated metadata")
			}
		})
	}
}

func TestScaleP21SW85RealtimeRecordOwnershipAndCurrentGeneration(t *testing.T) {
	values := map[string]domain.Bootstrap{"physical": {SavedViews: []domain.SavedView{{ID: "selected", TeamID: "old"}}}}
	view := scaleP21HotLogoStore(t, values)
	parent, _, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next := domain.Bootstrap{SavedViews: []domain.SavedView{{ID: "selected", TeamID: "current"}}}
	source, err := captureMetadataLogicalSource(t.Context(), next, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource("physical", source)
	literal := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": next}}
	base := view.MetadataWorkUsedForTesting()
	scaleP21SW85Oracle(t, literal, view, pinned, "physical", "savedViews", "selected")
	if view.MetadataWorkUsedForTesting() != base {
		t.Fatal("current generation read leaked work")
	}
	parent.Close()
	// An expired caller pin is equally irrelevant to the literal accessor.
	scaleP21SW85Oracle(t, literal, view, pinned, "physical", "savedViews", "selected")
	read, _, err := view.BeginRealtimeRecordRead(t.Context(), "physical", "savedViews", "selected")
	if err != nil {
		t.Fatal(err)
	}
	view.releaseMetadataLogicalSources()
	if read.Data.TeamID != "current" || !read.Found || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("selected output did not own its generation")
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("selected output leaked")
	}
	view = scaleP21HotLogoStore(t, values)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if r, _, err := view.BeginRealtimeRecordRead(canceled, "physical", "savedViews", "selected"); r != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("cancellation lost or leaked", err)
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = 1
	if r, found, err := view.BeginRealtimeRecordRead(canceled, "physical", "savedViews", ""); r != nil || found || err != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("empty ID acquired coverage or work", found, err)
	}
	for _, budget := range []int{1, 100 << 10, 194 << 10, 230 << 10} {
		view.metadataResidency.work.limit = budget
		r, _, err := view.BeginRealtimeRecordRead(t.Context(), "physical", "savedViews", "selected")
		if r != nil {
			r.Close()
		}
		if !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial admission failed to release", budget, err)
		}
	}
	view.metadataResidency.work.limit = limit
	values["physical"] = domain.Bootstrap{Documents: []domain.Document{{ID: "selected", TeamIDs: []string{strings.Repeat("large", 2000000)}}}}
	large := scaleP21HotLogoStore(t, values)
	large.metadataResidency.work.limit = 1 << 20
	if r, _, err := large.BeginRealtimeRecordRead(t.Context(), "physical", "documents", "selected"); r != nil || !errors.Is(err, ErrMetadataWorkBudget) || large.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("oversized selected header was not refused and released", err)
	}
}

func TestScaleP21SW85RealtimeRecordWarmReloadBoundedBodies(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	var expectedWork int
	for _, size := range []int{1000, 10000} {
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "savedViews", "documents"), workspace, "scale_p21.sw85", "", nil, func(data *domain.Bootstrap) error {
			data.SavedViews = []domain.SavedView{{ID: "selected", TeamID: "team", Scope: "team", OwnerID: "owner", Description: strings.Repeat("ignored body ", size), Filters: json.RawMessage(`{"body":"` + strings.Repeat("raw body ", size) + `"}`)}}
			data.Documents = []domain.Document{{ID: "unrelated", Content: strings.Repeat("unrelated workspace body ", size)}}
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
			statements := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
			held := scaleP21SW85Oracle(t, f.SQLiteStore, view, ctx, workspace, "savedViews", "selected")
			if expectedWork == 0 {
				expectedWork = held
			}
			if held != expectedWork || statements != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("ignored body growth changed admitted work or hydrated metadata", held, expectedWork, statements)
			}
			t.Logf("body_repetitions=%d reload=%t held_work=%d", size, reload, held)
		}
	}
	// The selected record and another workspace exceed a request's entire
	// work allowance. Their ordinary string/raw bodies remain streaming input.
	body := strings.Repeat("x", 10<<20)
	values := map[string]domain.Bootstrap{
		"physical":  {SavedViews: []domain.SavedView{{ID: "selected", TeamID: "team", Description: body, Filters: json.RawMessage(`"` + body + `"`)}}},
		"unrelated": {Documents: []domain.Document{{ID: "other", Content: body}}},
	}
	literal := &SQLiteStore{workspaces: values}
	oversized := scaleP21HotLogoStore(t, values)
	scaleP21SW85Oracle(t, literal, oversized, t.Context(), "physical", "savedViews", "selected")
	if oversized.MetadataWorkUsedForTesting() != 0 || oversized.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("oversized unrelated bodies leaked or hydrated metadata")
	}
}
