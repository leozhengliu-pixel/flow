package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW37Packed(t *testing.T, data domain.Bootstrap) *metadataLogicalSource {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	packed, err := metadataLogicalUnitsImport(t.Context(), source)
	source.Release()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(packed.Release)
	return packed
}

func scaleP21SW37Compare(t *testing.T, image metadataAggregateImage, id string) any {
	t.Helper()
	original, err := metadataAggregateResolveWalk(t.Context(), image, id)
	if err != nil {
		t.Fatal("literal typed walker", err)
	}
	want, err := metadataAggregateJSONNode(t.Context(), original, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	got, err := metadataAggregateJSONValue(t.Context(), image, id, 8<<20)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed aggregate %q changed exact precedence: got=%#v want=%#v error=%v", id, got, want, err)
	}
	return got
}

func TestScaleP21SW37PrefixNestedDuplicateAndMapPrecedence(t *testing.T) {
	for _, entry := range []struct{ prefix, field string }{
		{"team_resource_section_", "teamResourceSections"}, {"team_resource_", "teamPinnedResources"},
		{"team_", "teams"}, {"state_", "states"}, {"usr_", "users"},
		{"label_", "labels"}, {"issue_", "issues"}, {"project_", "projects"}, {"cycle_", "cycles"},
	} {
		t.Run(entry.field, func(t *testing.T) {
			id := entry.prefix + "sw37"
			data := domain.Bootstrap{
				IssueTemplates: []domain.IssueTemplate{{ID: "template-unrelated", FormFields: []domain.TemplateFormField{{ID: id, Label: "Earlier nested"}}}},
			}
			field, _, ok := metadataLogicalField(entry.field)
			if !ok {
				t.Fatal("missing typed prefix field")
			}
			value := reflect.ValueOf(&data).Elem().Field(field)
			item := reflect.New(value.Type().Elem()).Elem()
			item.FieldByName("ID").SetString(id)
			value.Set(reflect.Append(value, item))
			image := metadataAggregateImage{Source: scaleP21SW37Packed(t, data)}
			got := scaleP21SW37Compare(t, image, id)
			if !reflect.DeepEqual(got, aggregateJSONValue(data, id)) {
				t.Fatal("prefix fast path disagreed with the original full oracle")
			}
		})
	}
	id := "nested-sw37"
	pointer := id
	data := domain.Bootstrap{
		Documents: []domain.Document{
			{ID: "earlier", ContentData: map[string]any{"id": id, "name": "nested first"}},
			{ID: id, Title: "Later root"},
			{ID: "project_nested", ContentData: map[string]any{"id": "project_missing", "name": "ordinary fallback"}},
			{ID: "terminal", ContentData: map[string]any{"id": "team_resource_missing", "name": "must stay absent"}},
		},
		CustomEmojis: []domain.CustomEmoji{{ID: "same", Name: "first"}, {ID: "same", Name: "second"}},
		Settings: map[string]any{
			"id": "root-map", "child": map[string]any{"id": "root-map", "name": "child loses"},
			"pointer": map[string]any{"id": &pointer}, "typed": map[string]string{"id": "typed-map", "name": "typed"},
			"left": map[string]any{"id": "map-tie", "name": "left"}, "right": map[string]any{"id": "map-tie", "name": "right"},
		},
		ImportJobs: []domain.ImportJob{{ID: "import-sw37", Rows: []map[string]string{{"id": "upload-row"}}}},
	}
	image := metadataAggregateImage{Source: scaleP21SW37Packed(t, data)}
	for _, key := range []string{id, "same", "project_missing", "team_resource_missing", "root-map", "typed-map", "map-tie", "import-sw37", "upload-row", "absent"} {
		scaleP21SW37Compare(t, image, key)
	}
}

func TestScaleP21SW37SelectedExcludedAndAppendedPrecedence(t *testing.T) {
	const id = "target-sw37"
	data := domain.Bootstrap{
		IssueTemplates: []domain.IssueTemplate{{ID: id, Name: "first"}, {ID: id, Name: "duplicate"}},
		Documents: []domain.Document{
			{ID: "doc-sw37", ContentData: map[string]any{"id": id, "name": "later"}},
			{ID: "doc-second", ContentData: map[string]any{"id": "new-nested", "name": "base"}},
		},
		Settings: map[string]any{"id": "map-root", "nested": map[string]any{"id": "map-child", "name": "before"}, "later": map[string]any{"id": id, "name": "settings"}},
	}
	source := scaleP21SW37Packed(t, data)
	for _, test := range []struct {
		name     string
		selected domain.Bootstrap
		scope    MetadataRecordScope
		excluded []string
	}{
		{"replacement-first-duplicate", domain.Bootstrap{IssueTemplates: []domain.IssueTemplate{{ID: id, Name: "selected"}}}, MetadataRecordScope{Write: []RecordKey{{"issueTemplates", id}}}, nil},
		{"deletion-reveals-later-nested", domain.Bootstrap{}, MetadataRecordScope{Write: []RecordKey{{"issueTemplates", id}}}, nil},
		{"excluded-reveals-later-nested", domain.Bootstrap{}, MetadataRecordScope{}, []string{"issueTemplates"}},
		{"complete-field-replacement", domain.Bootstrap{IssueTemplates: []domain.IssueTemplate{{ID: "new-template", FormFields: []domain.TemplateFormField{{ID: id, Label: "replacement nested"}}}}}, MetadataRecordScope{Fields: []string{"issueTemplates"}}, nil},
		{"point-nested-replacement", domain.Bootstrap{Documents: []domain.Document{{ID: "doc-sw37", ContentData: map[string]any{"id": "new-nested", "name": "replacement wins"}}}}, MetadataRecordScope{Write: []RecordKey{{"documents", "doc-sw37"}}}, []string{"issueTemplates"}},
		{"append-before-later-field", domain.Bootstrap{Documents: []domain.Document{{ID: "new-doc", ContentData: map[string]any{"id": "appended", "name": "first append"}}, {ID: "second-new-doc", ContentData: map[string]any{"id": "appended", "name": "second append"}}}}, MetadataRecordScope{Write: []RecordKey{{"documents", "second-new-doc"}, {"documents", "new-doc"}}}, nil},
		{"map-key-replacement", domain.Bootstrap{Settings: map[string]any{"nested": map[string]any{"id": "new-map-child", "name": "after"}}}, MetadataRecordScope{Write: []RecordKey{{"settings", "nested"}}}, nil},
		{"map-own-id-replacement", domain.Bootstrap{Settings: map[string]any{"id": "new-map-root"}}, MetadataRecordScope{Write: []RecordKey{{"settings", "id"}}}, nil},
		{"map-own-id-tombstone", domain.Bootstrap{Settings: map[string]any{}}, MetadataRecordScope{Write: []RecordKey{{"settings", "id"}}}, nil},
		{"policy-scalar-overlay", domain.Bootstrap{WorkspaceSettings: domain.WorkspaceSettings{SessionDurationDays: 47}}, MetadataRecordScope{Write: []RecordKey{{"workspaceSettings", "sessionDurationDays"}}}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := metadataAggregateImage{Source: source, Selected: &test.selected, Scope: test.scope, ExcludedFields: test.excluded}
			for _, key := range []string{id, "new-nested", "appended", "map-root", "new-map-root", "map-child", "new-map-child", "absent"} {
				scaleP21SW37Compare(t, image, key)
			}
		})
	}
}

func TestScaleP21SW37IndexNegativeLookupWork(t *testing.T) {
	for _, count := range []int{32, 320} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			data := domain.Bootstrap{Workspace: domain.Workspace{ID: "negative-sw37"}}
			for i := 0; i < count; i++ {
				data.Documents = append(data.Documents, domain.Document{ID: fmt.Sprintf("doc-%d", i), ContentData: map[string]any{"id": fmt.Sprintf("nested-%d", i)}})
			}
			source := scaleP21SW37Packed(t, data)
			arena := source.units.arena
			for _, id := range []string{"absent-sw37", fmt.Sprintf("nested-%d", count-1), "project_absent-sw37"} {
				before := arena.index.reads
				start := time.Now()
				legacy, err := metadataAggregateResolveWalk(t.Context(), metadataAggregateImage{Source: source}, id)
				oldDuration := time.Since(start)
				oldReads := arena.index.reads - before
				if err != nil {
					t.Fatal(err)
				}
				before = arena.index.reads
				start = time.Now()
				indexed, err := metadataAggregateResolve(t.Context(), metadataAggregateImage{Source: source}, id)
				newDuration := time.Since(start)
				newReads := arena.index.reads - before
				if err != nil || (legacy == nil) != (indexed == nil) {
					t.Fatal("indexed negative/positive lookup changed presence", err)
				}
				if newReads > 256 || newReads >= oldReads {
					t.Fatalf("lookup did not use bounded index work: old=%d new=%d", oldReads, newReads)
				}
				t.Logf("records=%d id=%s original_index_reads=%d packed_index_reads=%d original_latency=%s packed_latency=%s", count, id, oldReads, newReads, oldDuration, newDuration)
			}
		})
	}
}

func TestScaleP21SW37PairCursorVirtualFileAndCleanup(t *testing.T) {
	data := domain.Bootstrap{
		Projects:       []domain.Project{{ID: "one", Name: "before"}, {ID: "two", Name: "unchanged"}},
		ProjectUpdates: map[string][]domain.ProjectUpdate{"parent": {{ID: "update", Body: "before"}}},
	}
	full, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Release()
	packed, err := metadataLogicalUnitsImport(t.Context(), full)
	if err != nil {
		t.Fatal(err)
	}
	defer packed.Release()
	for _, name := range []string{"projects", "projectUpdates"} {
		field, _, _ := metadataLogicalField(name)
		for _, pair := range [][2]metadataLogicalNode{{full.fields[field], packed.fields[field]}, {packed.fields[field], full.fields[field]}, {packed.fields[field], packed.fields[field]}} {
			count := 0
			err := metadataLogicalPairs(t.Context(), pair[0], pair[1], func(parent, before, after metadataLogicalNode) error {
				a, err := before.Value(t.Context(), 2<<20)
				if err != nil {
					return err
				}
				b, err := after.Value(t.Context(), 2<<20)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(a.Interface(), b.Interface()) {
					t.Fatal("virtual/FILE pair changed order or values")
				}
				if name == "projectUpdates" {
					value, err := parent.Value(t.Context(), 1024)
					if err != nil || value.String() != "parent" {
						t.Fatal("virtual map parent key changed", err)
					}
				}
				count++
				return nil
			})
			if err != nil || count != len(data.Projects) && name == "projects" || count != 1 && name == "projectUpdates" {
				t.Fatal("virtual/FILE pair traversal", count, err)
			}
		}
	}
	disk := metadataLogicalDiskUsed.Load()
	descriptors := metadataLogicalDescriptorsUsed.Load()
	field, _, _ := metadataLogicalField("projects")
	stop := errors.New("stop paired visit")
	for i := 0; i < 8; i++ {
		if err := metadataLogicalPairs(t.Context(), packed.fields[field], packed.fields[field], func(_, _, _ metadataLogicalNode) error { return stop }); !errors.Is(err, stop) {
			t.Fatal("pair callback failure was lost", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		err := metadataLogicalPairs(ctx, packed.fields[field], packed.fields[field], func(_, _, _ metadataLogicalNode) error {
			cancel()
			return ctx.Err()
		})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("pair cancellation was lost", err)
		}
	}
	path := packed.units.arena.index.stage.path
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("pair cursor leaked admission")
	}
	packed.Release()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pair cursor retained arena after final owner release", err)
	}
}

func TestScaleP21SW37AggregateCancellationAndLargeUnrelatedValues(t *testing.T) {
	data := domain.Bootstrap{
		Documents: []domain.Document{
			{ID: "large", Content: strings.Repeat("private", 1<<19)},
			{ID: "wanted", Title: "Small winner"},
		},
	}
	source := scaleP21SW37Packed(t, data)
	image := metadataAggregateImage{Source: source}
	if got := scaleP21SW37Compare(t, image, "wanted"); got == nil {
		t.Fatal("unrelated legal large record hid small winner")
	}
	disk := metadataLogicalDiskUsed.Load()
	descriptors := metadataLogicalDescriptorsUsed.Load()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, id := range []string{"", "wanted", "absent", "project_absent"} {
		if _, err := metadataAggregateResolve(ctx, image, id); !errors.Is(err, context.Canceled) {
			t.Fatal("indexed resolution suppressed cancellation", id, err)
		}
	}
	if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
		t.Fatal("cancelled aggregate lookup leaked admission")
	}
}

func TestScaleP21SW37AggregatePlanOwnsAndReleasesWinnerRoot(t *testing.T) {
	for _, mode := range []string{"values", "cancelled", "abandoned"} {
		t.Run(mode, func(t *testing.T) {
			disk := metadataLogicalDiskUsed.Load()
			descriptors := metadataLogicalDescriptorsUsed.Load()
			source := scaleP21SW37Packed(t, domain.Bootstrap{CustomEmojis: []domain.CustomEmoji{{ID: "owned-sw37", Name: "winner"}}})
			path := source.units.arena.index.stage.path
			image := metadataAggregateImage{Source: source}
			plan, err := planMetadataAggregate(t.Context(), image, image, "owned-sw37", true, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			if len(plan.sources) != 1 {
				t.Fatal("plan did not deduplicate its owned source pin")
			}
			source.Release()
			if _, err := os.Stat(path); err != nil {
				t.Fatal("plan lost its winner before materialization", err)
			}
			switch mode {
			case "values":
				previous, entity, err := plan.Values(t.Context())
				if err != nil || len(previous) != 0 || entity == nil {
					t.Fatal("owned plan lost exact same-generation values", string(previous), entity, err)
				}
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, _, err := plan.Values(ctx); !errors.Is(err, context.Canceled) {
					t.Fatal("owned plan suppressed cancellation", err)
				}
			case "abandoned":
				plan.Close()
			}
			plan.Close()
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("completed/failed/abandoned plan retained winner arena", err)
			}
			if metadataLogicalDiskUsed.Load() != disk || metadataLogicalDescriptorsUsed.Load() != descriptors {
				t.Fatal("aggregate plan leaked root admission")
			}
		})
	}
}

func TestScaleP21SW37OccurrenceMaskKeepsDuplicateSiblings(t *testing.T) {
	const id = "duplicate-sw37"
	first := domain.CustomEmoji{ID: id, Name: "first"}
	second := domain.CustomEmoji{ID: id, Name: "second"}
	data := domain.Bootstrap{CustomEmojis: []domain.CustomEmoji{{ID: "gap"}, first, second}}
	source := scaleP21SW37Packed(t, data)
	before := domain.Bootstrap{CustomEmojis: data.CustomEmojis[:1]}
	sparse, err := source.Fork(t.Context(), before, domain.Bootstrap{}, MetadataRecordScope{Write: []RecordKey{{"customEmojis", "gap"}}}, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer sparse.Release()
	for _, test := range []struct {
		name    string
		ordinal uint64
		next    []domain.CustomEmoji
		want    string
	}{
		{"first-only", 1, []domain.CustomEmoji{{ID: id, Name: "updated"}}, "updated"},
		{"later-only-keeps-first", 2, []domain.CustomEmoji{{ID: id, Name: "updated"}}, "first"},
		{"first-tombstone-exposes-second", 1, nil, "second"},
		{"nonmatching-occurrence", 99, []domain.CustomEmoji{{ID: id, Name: "updated"}}, "first"},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected := domain.Bootstrap{CustomEmojis: test.next}
			image := metadataAggregateImage{
				Source: sparse, Selected: &selected, Scope: MetadataRecordScope{Write: []RecordKey{{"customEmojis", id}}},
				PointOccurrence: func(field, key string, ordinal uint64) bool {
					return field == "customEmojis" && key == id && ordinal == test.ordinal
				},
			}
			got := scaleP21SW37Compare(t, image, id)
			if got.(map[string]any)["name"] != test.want {
				t.Fatal("occurrence filter changed untouched duplicate precedence", got)
			}
		})
	}
}

func TestScaleP21SW37IndexedUserRefreshKeepsLastDuplicate(t *testing.T) {
	const id = "duplicate-user-sw37"
	longID := strings.Repeat("long\x00user", 1<<16)
	data := domain.Bootstrap{Settings: map[string]any{"long": longID}}
	for i := 0; i < 64; i++ {
		data.Users = append(data.Users, domain.User{ID: fmt.Sprint(i), Name: "Unrelated"})
	}
	data.Users = append(data.Users, domain.User{ID: id, Name: "first"}, domain.User{ID: id, Name: "last"}, domain.User{ID: longID, Name: "long"})
	full, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Release()
	source, err := metadataLogicalUnitsImport(t.Context(), full)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	field, _, _ := metadataLogicalField("settings")
	storedID, err := metadataAggregateStoredNode{source.fields[field]}.mapValue(t.Context(), "long")
	if err != nil {
		t.Fatal(err)
	}
	storedID, err = metadataAggregateUnwrap(t.Context(), storedID, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []metadataAggregateNode{
		metadataAggregateNativeNode{reflect.ValueOf(id)}, metadataAggregateNativeNode{reflect.ValueOf("absent")}, storedID,
	} {
		want, err := metadataAggregateLookupUser(t.Context(), full, request)
		if err != nil {
			t.Fatal(err)
		}
		got, err := metadataAggregateLookupUser(t.Context(), source, request)
		if err != nil || (got == nil) != (want == nil) {
			t.Fatal("indexed user lookup changed presence", err)
		}
		if got != nil {
			a, err := got.nodeValue(t.Context(), 8<<20)
			if err != nil {
				t.Fatal(err)
			}
			b, err := want.nodeValue(t.Context(), 8<<20)
			if err != nil || !reflect.DeepEqual(a.Interface(), b.Interface()) {
				t.Fatal("indexed user lookup changed last duplicate/native long ID", err)
			}
		}
	}
}

func TestScaleP21SW37SelectedDisplayRefreshMatchesWholeTransform(t *testing.T) {
	current := domain.User{ID: "directory-user", Name: "last directory value"}
	stale := domain.User{ID: current.ID, Name: "stale copied reference"}
	missing := domain.User{ID: "no-directory-entry", Name: "retained"}
	data := domain.Bootstrap{
		Users:             []domain.User{{ID: current.ID, Name: "earlier directory value"}, current},
		Members:           []domain.WorkspaceMember{{User: stale}},
		Projects:          []domain.Project{{ID: "project-selected", Lead: &stale, Creator: &missing}},
		Initiatives:       []domain.Initiative{{ID: "initiative-selected", Creator: stale, Owner: &stale}},
		ProjectUpdates:    map[string][]domain.ProjectUpdate{"parent": {{ID: "project-update", User: stale}}},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{"parent": {{ID: "initiative-update", User: stale}}},
		Comments:          map[string][]domain.Comment{"parent": {{ID: "comment-selected", User: stale}}},
		Activities:        map[string][]domain.ActivityEvent{"parent": {{ID: "activity-selected", Actor: stale}}},
		Notifications:     []domain.Notification{{ID: "notification-selected", Actor: stale}},
	}
	source := scaleP21SW37Packed(t, data)
	for _, scope := range []MetadataRecordScope{
		{Read: []RecordKey{{"projects", "project-selected"}}},
		{Fields: []string{"members", "projects", "initiatives", "projectUpdates", "initiativeUpdates", "comments", "activities", "notifications"}},
	} {
		for _, fieldScoped := range []bool{false, true} {
			legacyFields := []string{"projects", "users"}
			expectedSource, err := metadataAggregateDisplaySource(t.Context(), source, legacyFields, fieldScoped)
			if err != nil {
				t.Fatal(err)
			}
			want, err := expectedSource.Select(t.Context(), scope, 2<<20)
			expectedSource.Release()
			if err != nil {
				t.Fatal(err)
			}
			actual, err := source.Select(t.Context(), scope, 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			if err := refreshSelectedMetadataLogicalReferences(t.Context(), source, &actual, scope, legacyFields, fieldScoped, 2<<20); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatal("selected callback reference refresh differs from literal whole-source transform")
			}
		}
	}
}

func TestScaleP21SW37DeclinedWriteKeepsStaleWarmReferences(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	if err := f.MutateWorkspace(t.Context(), workspace, "integration.adapter_configured", "", nil, func(data *domain.Bootstrap) error {
		stale := data.Users[0]
		stale.Name = "stale warm reference"
		for i := 0; i < 64; i++ {
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("sw37-project-%d", i), Lead: &stale})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	source, err := view.retainMetadataLogicalSource(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	if source.units == nil {
		t.Fatal("declined-write test did not use the packed producer")
	}
	projects := MetadataRecordScope{Fields: []string{"projects"}}
	before, err := source.Select(t.Context(), projects, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("declined before commit")
	for _, callbackError := range []error{ErrNoMutation, stop} {
		scope := MetadataRecordScope{Read: []RecordKey{{"projects", "sw37-project-0"}}}
		writes := source.units.arena.index.writes
		called := false
		err := view.MutateWorkspaceScoped(t.Context(), workspace, "integration.adapter_configured", "", nil, scope, func(data *domain.Bootstrap) error {
			called = true
			if len(data.Projects) != 1 || data.Projects[0].Lead == nil || data.Projects[0].Lead.Name == "stale warm reference" {
				t.Fatal("declined callback did not receive the full directory's refreshed selected reference")
			}
			return callbackError
		})
		if !called || callbackError == ErrNoMutation && err != nil || callbackError == stop && !errors.Is(err, stop) {
			t.Fatal("declined callback changed the literal error boundary", called, err)
		}
		if source.units.arena.index.writes != writes {
			t.Fatal("declined callback performed the whole-field reference transform")
		}
		after, err := source.Select(t.Context(), projects, 2<<20)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("declined callback published refreshed warm state", err)
		}
		if view.metadataResidency.work.used != 0 {
			t.Fatal("declined callback retained admission")
		}
	}
}
