package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func TestScaleP21SW28AggregateLegacyPrecedence(t *testing.T) {
	const id = "aggregate_sw28"
	collision := map[string]any{"id": id, "marker": "nested"}
	cases := []struct {
		name string
		id   string
		data domain.Bootstrap
		want any
	}{
		{
			name: "bootstrap-field-order", id: id,
			data: domain.Bootstrap{
				IssueTemplates: []domain.IssueTemplate{{ID: "template_sw28", FormFields: []domain.TemplateFormField{{ID: id, Label: "Earlier field"}}}},
				Documents:      []domain.Document{{ID: id, Title: "Later document"}},
			},
			want: domain.TemplateFormField{ID: id, Label: "Earlier field"},
		},
		{
			name: "earlier-element-nested-before-later-root", id: id,
			data: domain.Bootstrap{Documents: []domain.Document{
				{ID: "document_first", ContentData: collision}, {ID: id, Title: "Later root"},
			}},
			want: collision,
		},
		{
			name: "own-struct-id-before-child", id: id,
			data: domain.Bootstrap{Documents: []domain.Document{{ID: id, ContentData: collision}}},
			want: domain.Document{ID: id, ContentData: collision},
		},
		{
			name: "positive-prefix-shortcut", id: "project_sw28",
			data: domain.Bootstrap{
				Documents: []domain.Document{{ID: "document_sw28", ContentData: map[string]any{"id": "project_sw28", "marker": "earlier"}}},
				Projects:  []domain.Project{{ID: "project_sw28", Name: "Shortcut"}},
			},
			want: domain.Project{ID: "project_sw28", Name: "Shortcut"},
		},
		{
			name: "ordinary-prefix-miss-falls-through", id: "usr_sw28",
			data: domain.Bootstrap{Documents: []domain.Document{{ID: "document_sw28", ContentData: map[string]any{"id": "usr_sw28", "marker": "fallback"}}}},
			want: map[string]any{"id": "usr_sw28", "marker": "fallback"},
		},
		{
			name: "missing-resource-terminates", id: "team_resource_sw28",
			data: domain.Bootstrap{Documents: []domain.Document{{ID: "document_sw28", ContentData: map[string]any{"id": "team_resource_sw28"}}}},
		},
		{
			name: "missing-section-terminates", id: "team_resource_section_sw28",
			data: domain.Bootstrap{Documents: []domain.Document{{ID: "document_sw28", ContentData: map[string]any{"id": "team_resource_section_sw28"}}}},
		},
		{
			name: "typed-raw-json-is-not-searchable", id: id,
			data: domain.Bootstrap{ProjectDisplayDefault: json.RawMessage(`{"id":"aggregate_sw28"}`)},
		},
		{
			name: "typed-map-string-id", id: id,
			data: domain.Bootstrap{Settings: map[string]any{"typed": map[string]string{"id": id, "marker": "typed"}}},
			want: map[string]string{"id": id, "marker": "typed"},
		},
		{
			name: "raw-message-map-id-does-not-match", id: id,
			data: domain.Bootstrap{Settings: map[string]any{"typed": map[string]json.RawMessage{"id": json.RawMessage(`"aggregate_sw28"`)}}},
		},
		{
			name: "import-rows-compacted", id: id,
			data: domain.Bootstrap{ImportJobs: []domain.ImportJob{{ID: "import_sw28", Rows: []map[string]string{{"id": id}}}}},
		},
		{
			name: "matched-import-omits-uploaded-rows", id: id,
			data: domain.Bootstrap{ImportJobs: []domain.ImportJob{{ID: id, Filename: "source.csv", Rows: []map[string]string{{"id": "nested_sw28"}}}}},
			want: domain.ImportJob{ID: id, Filename: "source.csv"},
		},
		{
			name: "matched-migration-omits-uploaded-bundle", id: id,
			data: domain.Bootstrap{MigrationJobs: []domain.MigrationJob{{ID: id, Filename: "source.json", Bundle: json.RawMessage(`{"id":"nested_sw28"}`)}}},
			want: domain.MigrationJob{ID: id, Filename: "source.json"},
		},
		{
			name: "root-map-before-children", id: id,
			data: domain.Bootstrap{Settings: map[string]any{"id": id, "child": collision, "body": strings.Repeat("large ", 50000)}},
			want: map[string]any{"id": id, "child": collision, "body": strings.Repeat("large ", 50000)},
		},
		{
			name: "content-record-map-searches-typed-comments", id: id,
			data: domain.Bootstrap{Comments: map[string][]domain.Comment{"issue_parent": {{ID: "comment_sw28", BodyData: collision}}}},
			want: collision,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			want := scaleP21SW28JSONValue(t, test.want)
			if got := aggregateJSONValue(test.data, test.id); !reflect.DeepEqual(got, want) {
				t.Fatalf("legacy oracle got %#v, want %#v", got, want)
			}
			source, err := captureMetadataLogicalSource(t.Context(), test.data, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, test.id, 4<<20)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("bounded closure got %#v err=%v, want %#v", got, err, want)
			}
		})
	}
}

func scaleP21SW28JSONValue(t *testing.T, value any) any {
	t.Helper()
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestScaleP21SW28AggregateLegacyDeletionDiff(t *testing.T) {
	const id = "template_sw28_deleted"
	first := domain.IssueTemplate{ID: id, Name: "Before"}
	later := map[string]any{"id": id, "name": "Later", "nested": true}
	before := domain.Bootstrap{
		IssueTemplates: []domain.IssueTemplate{first},
		Documents:      []domain.Document{{ID: "document_sw28", ContentData: later}},
	}
	after := before
	after.IssueTemplates = nil
	if got := aggregateJSONValue(after, id); !reflect.DeepEqual(got, later) {
		t.Fatalf("deletion did not expose later nested object: %#v", got)
	}
	var previous map[string]any
	if err := json.Unmarshal(aggregatePreviousValues(before, after, id), &previous); err != nil {
		t.Fatal(err)
	}
	if previous["name"] != "Before" || previous["id"] != nil {
		t.Fatalf("top-level previous diff changed: %#v", previous)
	}
}

func TestScaleP21SW28AggregateMapAmbiguity(t *testing.T) {
	const id = "ambiguous_sw28"
	data := domain.Bootstrap{Settings: map[string]any{
		"first":  map[string]any{"id": id, "marker": "first"},
		"second": map[string]any{"id": id, "marker": "second"},
	}}
	for range 100 {
		entity, ok := aggregateJSONValue(data, id).(map[string]any)
		if !ok || entity["id"] != id || entity["marker"] != "first" && entity["marker"] != "second" {
			t.Fatalf("map traversal returned outside the legacy permissible set: %#v", entity)
		}
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for range 10 {
		value, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, id, 64<<10)
		entity, ok := value.(map[string]any)
		if err != nil || !ok || entity["id"] != id || entity["marker"] != "first" && entity["marker"] != "second" {
			t.Fatalf("bounded traversal returned outside the legacy permissible set: %#v err=%v", value, err)
		}
	}
}

func TestScaleP21SW28AggregateLargeAndCancellation(t *testing.T) {
	const id = "aggregate_sw28_large"
	whole := domain.Bootstrap{Settings: map[string]any{"id": id, "body": strings.Repeat("whole map ", 40000)}}
	source, err := captureMetadataLogicalSource(t.Context(), whole, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	image := metadataAggregateImage{Source: source}
	got, err := metadataAggregateJSONValue(t.Context(), image, id, 4<<20)
	if err != nil || !reflect.DeepEqual(got, aggregateJSONValue(whole, id)) {
		t.Fatalf("large whole-map winner changed: err=%v", err)
	}
	if _, err := metadataAggregateJSONValue(t.Context(), image, id, 1024); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("whole-map winner exceeded declared ownership budget: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := metadataAggregateJSONValue(ctx, image, id, 4<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup returned %v", err)
	}
	if _, err := metadataAggregatePreviousValues(ctx, image, image, id, 4<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled previous-value lookup returned %v", err)
	}
}

func TestScaleP21SW28AggregateRejectLargeUnrelatedID(t *testing.T) {
	const id = "aggregate_sw28_after_large"
	data := domain.Bootstrap{
		Documents: []domain.Document{{
			ID: strings.Repeat("unrelated-id", 40000), Content: strings.Repeat("unrelated body ", 40000),
			ContentData: map[string]any{
				strings.Repeat("unrelated-key", 40000): "large unrelated key",
				"nested":                               map[string]any{"id": id, "marker": "small winner"},
			},
		}},
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, id, 64<<10)
	if err != nil || !reflect.DeepEqual(got, aggregateJSONValue(data, id)) {
		t.Fatalf("legal giant unrelated ID/body prevented small nested winner: got=%#v err=%v", got, err)
	}
}

func TestScaleP21SW28AggregateOverlayDifferential(t *testing.T) {
	const id = "aggregate_sw28_overlay"
	base := domain.Bootstrap{
		Documents: []domain.Document{
			{ID: "document_first", ContentData: map[string]any{"id": id, "marker": "first nested"}},
			{ID: "document_second", ContentData: map[string]any{"id": id, "marker": "second nested"}},
		},
		CustomEmojis: []domain.CustomEmoji{{ID: id, Name: "Late emoji"}},
		Settings:     map[string]any{"preserved": "before", "changing": "before"},
	}
	for _, operation := range []string{"point-update", "point-delete", "complete-replace", "complete-empty", "point-append", "map-root", "excluded-field"} {
		t.Run(operation, func(t *testing.T) {
			expected := cloneBootstrap(base)
			selected := domain.Bootstrap{}
			scope := MetadataRecordScope{}
			excluded := []string(nil)
			switch operation {
			case "point-update":
				expected.Documents[0].ContentData["marker"] = "replaced"
				selected.Documents = []domain.Document{expected.Documents[0]}
				scope.Write = []RecordKey{{Kind: "documents", ID: "document_first"}}
			case "point-delete":
				expected.Documents = expected.Documents[1:]
				scope.Write = []RecordKey{{Kind: "documents", ID: "document_first"}}
			case "complete-replace":
				expected.Documents = []domain.Document{{ID: id, Title: "Complete replacement"}}
				selected.Documents = expected.Documents
				scope.Fields = []string{"documents"}
			case "complete-empty":
				expected.Documents = []domain.Document{}
				selected.Documents = []domain.Document{}
				scope.Fields = []string{"documents"}
			case "point-append":
				added := domain.Document{ID: id, Title: "Appended after nested winner"}
				expected.Documents = append(expected.Documents, added)
				selected.Documents = []domain.Document{added}
				scope.Write = []RecordKey{{Kind: "documents", ID: id}}
			case "map-root":
				expected.Settings["id"], expected.Settings["changing"] = id, "after"
				selected.Settings = map[string]any{"id": id, "changing": "after"}
				scope.Write = []RecordKey{{Kind: "settings", ID: "id"}, {Kind: "settings", ID: "changing"}}
				expected.Documents, expected.CustomEmojis = nil, nil
				excluded = []string{"documents", "customEmojis"}
			case "excluded-field":
				expected.Documents = nil
				excluded = []string{"documents"}
			}
			source, err := captureMetadataLogicalSource(t.Context(), base, copyStamp{})
			if err != nil {
				t.Fatal(err)
			}
			defer source.Release()
			after := metadataAggregateImage{Source: source, Selected: &selected, Scope: scope, ExcludedFields: excluded}
			got, err := metadataAggregateJSONValue(t.Context(), after, id, 4<<20)
			want := aggregateJSONValue(expected, id)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("overlay lookup got %#v err=%v, want %#v", got, err, want)
			}
			before := metadataAggregateImage{Source: source}
			gotPrevious, err := metadataAggregatePreviousValues(t.Context(), before, after, id, 4<<20)
			wantPrevious := aggregatePreviousValues(base, expected, id)
			if err != nil || !jsonEqual(gotPrevious, wantPrevious) {
				t.Fatalf("overlay previous=%s err=%v, want %s", gotPrevious, err, wantPrevious)
			}
		})
	}
}

func TestScaleP21SW28AggregateUpdatesMapShape(t *testing.T) {
	const id = "update_sw28"
	base := domain.Bootstrap{
		ProjectUpdates: map[string][]domain.ProjectUpdate{
			"project_parent": {{ID: id, ProjectID: "project_parent", Body: "Before"}},
		},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{
			"initiative_parent": {{ID: id, InitiativeID: "initiative_parent", Body: "Later"}},
		},
	}
	source, err := captureMetadataLogicalSource(t.Context(), base, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, operation := range []string{"stored", "replace", "delete"} {
		t.Run(operation, func(t *testing.T) {
			expected := cloneBootstrap(base)
			selected := domain.Bootstrap{}
			image := metadataAggregateImage{Source: source}
			if operation != "stored" {
				image.Selected = &selected
				image.Scope.Fields = []string{"projectUpdates"}
				expected.ProjectUpdates = nil
				if operation == "replace" {
					expected.ProjectUpdates = map[string][]domain.ProjectUpdate{
						"project_other_parent": {{ID: id, ProjectID: "project_other_parent", Body: "After"}},
					}
				}
				selected.ProjectUpdates = expected.ProjectUpdates
			}
			got, err := metadataAggregateJSONValue(t.Context(), image, id, 1<<20)
			want := aggregateJSONValue(expected, id)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("updates map lookup got %#v err=%v, want %#v", got, err, want)
			}
			if object, ok := got.(map[string]any); !ok || object["parent"] != nil || object["item"] != nil {
				t.Fatal("canonical update wrapper escaped into legacy aggregate")
			}
		})
	}
}

func TestScaleP21SW28AggregateLegacyContentUniverse(t *testing.T) {
	const id = "issue_sw28_universe"
	data := domain.Bootstrap{
		Issues:    []domain.Issue{{ID: id, Title: "Hydrated issue"}},
		Documents: []domain.Document{{ID: "document_sw28", ContentData: map[string]any{"id": id, "marker": "metadata-only fallback"}}},
	}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	for _, metadataOnly := range []bool{false, true} {
		image := metadataAggregateImage{Source: source}
		expected := data
		if metadataOnly {
			image.ExcludedFields = []string{"issues", "comments", "activities", "notifications", "notificationDeliveries"}
			expected.Issues = nil
		}
		got, err := metadataAggregateJSONValue(t.Context(), image, id, 1<<20)
		if want := aggregateJSONValue(expected, id); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("metadataOnly=%t got=%#v err=%v, want=%#v", metadataOnly, got, err, want)
		}
	}
}

func TestScaleP21SW28AggregateCompactsLargeUploadedInput(t *testing.T) {
	const id = "import_sw28_large"
	data := domain.Bootstrap{ImportJobs: []domain.ImportJob{{
		ID: id, Filename: "source.csv", Rows: []map[string]string{{"body": strings.Repeat("uploaded source ", 50000)}},
	}}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, id, 16<<10)
	if err != nil || !reflect.DeepEqual(got, aggregateJSONValue(data, id)) {
		t.Fatalf("compacted import winner decoded uploaded rows: got=%#v err=%v", got, err)
	}
}

func TestScaleP21SW28AggregateAllPrefixBranches(t *testing.T) {
	for _, test := range []struct {
		prefix, field string
		terminalMiss  bool
	}{
		{"team_resource_section_", "TeamResourceSections", true},
		{"team_resource_", "TeamPinnedResources", true},
		{"team_", "Teams", false},
		{"state_", "States", false},
		{"usr_", "Users", false},
		{"label_", "Labels", false},
		{"issue_", "Issues", false},
		{"project_", "Projects", false},
		{"cycle_", "Cycles", false},
	} {
		t.Run(test.prefix, func(t *testing.T) {
			id := test.prefix + "sw28"
			nested := map[string]any{"id": id, "marker": "nested"}
			data := domain.Bootstrap{Documents: []domain.Document{{ID: "document_sw28", ContentData: nested}}}
			want := any(nested)
			if test.terminalMiss {
				want = nil
			}
			if got := aggregateJSONValue(data, id); !reflect.DeepEqual(got, want) {
				t.Fatalf("missing prefix result %#v, want %#v", got, want)
			}
			checkBounded := func(want any) {
				t.Helper()
				source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
				if err != nil {
					t.Fatal(err)
				}
				defer source.Release()
				got, err := metadataAggregateJSONValue(t.Context(), metadataAggregateImage{Source: source}, id, 1<<20)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("bounded prefix result %#v err=%v, want %#v", got, err, want)
				}
			}
			checkBounded(want)
			field := reflect.ValueOf(&data).Elem().FieldByName(test.field)
			item := reflect.New(field.Type().Elem()).Elem()
			item.FieldByName("ID").SetString(id)
			field.Set(reflect.Append(field, item, item))
			if got, want := aggregateJSONValue(data, id), scaleP21SW28JSONValue(t, item.Interface()); !reflect.DeepEqual(got, want) {
				t.Fatalf("positive prefix result %#v, want %#v", got, want)
			}
			checkBounded(scaleP21SW28JSONValue(t, item.Interface()))
		})
	}
}

func TestScaleP21SW28AggregateScopedProducerDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, operation := range []string{"emoji-update", "template-delete", "emoji-update-large-winner"} {
		t.Run(operation, func(t *testing.T) {
			f := scaleP09Database(t)
			workspace := f.data.Workspace.URLKey
			id, documentID := idgen.New("sw28_target_"), idgen.New("document_sw28_")
			stamp := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
			type outcome struct {
				event    domain.DomainEvent
				realtime domain.RealtimeEvent
				target   string
				foreign  string
			}
			reset := func() {
				t.Helper()
				f.SetWebhookSink(nil)
				f.SetRealtimeSink(nil)
				err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents", "customEmojis", "issueTemplates", "webhooks"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
					data.Documents = []domain.Document{{ID: documentID, Title: "Hidden unrelated source", ContentData: map[string]any{"id": id, "marker": "nested source"}, Creator: f.data.Viewer}}
					if operation == "emoji-update-large-winner" {
						data.Documents[0].ContentData["body"] = strings.Repeat("large logical winner ", 20000)
					}
					data.CustomEmojis = []domain.CustomEmoji{{ID: id, Name: "Before", ImageURL: "before"}}
					data.IssueTemplates = nil
					if operation == "template-delete" {
						data.IssueTemplates = []domain.IssueTemplate{{ID: id, Name: "Before", Creator: f.data.Viewer, CreatedAt: stamp, UpdatedAt: stamp}}
					}
					data.Webhooks = []domain.Webhook{{ID: "hook_sw28", Enabled: true, URL: "https://example.test/sw28"}}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			row := func(field, key string) string {
				t.Helper()
				var raw []byte
				err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key).Scan(&raw)
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
			var got []outcome
			for _, bounded := range []bool{false, true} {
				reset()
				repo := f.SQLiteStore
				if bounded {
					var err error
					repo, err = f.BoundedMetadataViewForTesting(t.Context())
					if err != nil {
						t.Fatal(err)
					}
				}
				result := outcome{}
				repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { result.event = event })
				repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { result.realtime = event })
				field, event := "customEmojis", "custom_emoji.updated"
				if operation == "template-delete" {
					field, event = "issueTemplates", "issue_template.deleted"
				}
				beforeForeign := row("documents", documentID)
				err := repo.MutateWorkspaceScoped(context.Background(), workspace, event, id, map[string]any{"name": "After"}, MetadataRecordScope{Write: []RecordKey{{Kind: field, ID: id}}}, func(data *domain.Bootstrap) error {
					if operation == "template-delete" {
						data.IssueTemplates = nil
					} else {
						data.CustomEmojis[0].Name = "After"
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if operation == "template-delete" {
					var count int
					if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, id).Scan(&count); err != nil || count != 0 {
						t.Fatalf("delete count=%d err=%v", count, err)
					}
				} else {
					result.target = row(field, id)
				}
				result.foreign = row("documents", documentID)
				if beforeForeign != result.foreign {
					t.Fatal("aggregate source lookup rewrote unrelated document")
				}
				if bounded && (repo.WorkspaceMetadataDecodes() != 0 || repo.metadataResidency.work.used != 0) {
					t.Fatal("bounded producer decoded full metadata or leaked work")
				}
				got = append(got, result)
			}
			if got[0].target != got[1].target || got[0].foreign != got[1].foreign {
				t.Fatal("canonical producer outputs differ")
			}
			if !jsonEqual(got[0].event.PreviousValues, got[1].event.PreviousValues) ||
				!jsonEqual(got[0].event.Snapshot, got[1].event.Snapshot) ||
				!jsonEqual(got[0].realtime.Payload, got[1].realtime.Payload) {
				t.Fatalf("aggregate producer effects differ: previous=%s / %s snapshotBytes=%d/%d realtimeBytes=%d/%d",
					got[0].event.PreviousValues, got[1].event.PreviousValues,
					len(got[0].event.Snapshot), len(got[1].event.Snapshot),
					len(got[0].realtime.Payload), len(got[1].realtime.Payload))
			}
		})
	}
}

func TestScaleP21SW28AggregateCanceledProducer(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, id := f.data.Workspace.URLKey, idgen.New("emoji_sw28_cancel_")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "customEmojis"), workspace, "application_policy.updated", "", nil, func(data *domain.Bootstrap) error {
		data.CustomEmojis = []domain.CustomEmoji{{ID: id, Name: "Before"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='customEmojis' AND record_key=?`, workspace, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	probe, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	published := 0
	probe.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
	probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = probe.MutateWorkspaceScoped(ctx, workspace, "custom_emoji.updated", id, nil, MetadataRecordScope{Write: []RecordKey{{Kind: "customEmojis", ID: id}}}, func(data *domain.Bootstrap) error {
		data.CustomEmojis[0].Name = "Must roll back"
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled callback returned %v", err)
	}
	if err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='customEmojis' AND record_key=?`, workspace, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || published != 0 || probe.metadataResidency.work.used != 0 {
		t.Fatalf("canceled callback changed rows=%t, publications=%d, work=%d", !reflect.DeepEqual(before, after), published, probe.metadataResidency.work.used)
	}
}

func TestScaleP21SW28AggregatePublicationAdmission(t *testing.T) {
	const id = "aggregate_sw28_admission"
	data := domain.Bootstrap{Settings: map[string]any{
		"id": id, "body": strings.Repeat("retained publication ", 20000),
	}}
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	image := metadataAggregateImage{Source: source}
	plan, err := planMetadataAggregate(t.Context(), image, image, id, true, 2)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RequiredBytes <= 256<<10 || plan.WinnerBytes <= 256<<10 {
		t.Fatalf("large publication unaccounted: reservation=%d winner=%d", plan.RequiredBytes, plan.WinnerBytes)
	}
	probe := &SQLiteStore{metadataResidency: newMetadataResidency()}
	_, release, err := probe.tryAdmitAdditionalMetadataWork(t.Context(), plan.RequiredBytes)
	if err != nil {
		t.Fatal(err)
	}
	previous, entity, err := plan.Values(t.Context())
	if err != nil || len(previous) != 0 || !reflect.DeepEqual(entity, aggregateJSONValue(data, id)) {
		release()
		t.Fatalf("admitted publication changed values: previous=%s err=%v", previous, err)
	}
	if probe.metadataResidency.work.used != plan.RequiredBytes {
		release()
		t.Fatal("publication did not retain its allocation reservation")
	}
	release()
	if probe.metadataResidency.work.used != 0 {
		t.Fatal("publication release leaked work")
	}
	// The nonblocking child reservation must fail before allocation if all
	// process work is already held, rather than wait while holding SQL locks.
	_, exhaust, err := probe.tryAdmitAdditionalMetadataWork(t.Context(), metadataWorkBytes)
	if err != nil {
		t.Fatal(err)
	}
	_, _, blocked := probe.tryAdmitAdditionalMetadataWork(t.Context(), plan.RequiredBytes)
	exhaust()
	if !errors.Is(blocked, ErrMetadataWorkBudget) || probe.metadataResidency.work.used != 0 {
		t.Fatalf("exhausted publication admission err=%v used=%d", blocked, probe.metadataResidency.work.used)
	}
	missing, err := planMetadataAggregate(t.Context(), image, image, "missing_sw28", true, 0)
	if err != nil || missing.RequiredBytes != 128<<10 || missing.WinnerBytes != 0 {
		t.Fatalf("missing aggregate admission should be constant: plan=%+v err=%v", missing, err)
	}
}

func BenchmarkScaleP21SW28AggregateClosure(b *testing.B) {
	for _, bodyMiB := range []int{1, 10} {
		b.Run(fmt.Sprintf("unrelated-%dMiB", bodyMiB), func(b *testing.B) {
			const id = "aggregate_sw28_benchmark"
			data := domain.Bootstrap{Documents: []domain.Document{{
				ID: "document_sw28_benchmark", Content: strings.Repeat("x", bodyMiB<<20),
				ContentData: map[string]any{"id": id, "marker": "small winner"},
			}}}
			source, err := captureMetadataLogicalSource(b.Context(), data, copyStamp{})
			if err != nil {
				b.Fatal(err)
			}
			defer source.Release()
			b.Run("legacy-installed-search", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if aggregateJSONValue(data, id) == nil {
						b.Fatal("legacy lost winner")
					}
				}
			})
			b.Run("bounded-source-search", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					value, err := metadataAggregateJSONValue(b.Context(), metadataAggregateImage{Source: source}, id, 64<<10)
					if err != nil || value == nil {
						b.Fatalf("bounded lost winner: %v", err)
					}
				}
			})
			b.Run("capture-source", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					source, err := captureMetadataLogicalSource(b.Context(), data, copyStamp{})
					if err != nil {
						b.Fatal(err)
					}
					source.Release()
				}
			})
		})
	}
}
