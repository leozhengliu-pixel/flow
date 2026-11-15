package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW138Source(t *testing.T, data domain.Bootstrap) (*SQLiteStore, *metadataLogicalSource) {
	t.Helper()
	view := scaleP21HotLogoStore(t, nil)
	source, err := captureMetadataLogicalSource(t.Context(), data, copyStamp{revision: 138})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	return view, source
}

func scaleP21SW138Begin(t *testing.T, view *SQLiteStore, source *metadataLogicalSource) *bootstrapBodyProjection {
	t.Helper()
	projection, err := view.beginBootstrapBodyProjection(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(projection.Close)
	return projection
}

// Small fixtures only: rebuild native values using the returned offset map.
// Large-body tests inspect source nodes without materializing their bodies.
func scaleP21SW138Value(t *testing.T, node metadataLogicalNode, replacements map[int64]metadataLogicalNode) reflect.Value {
	t.Helper()
	if original, found := replacements[node.offset]; found {
		value, err := original.Value(t.Context(), int(original.cost))
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	value, err := node.Value(t.Context(), int(node.cost))
	if err != nil {
		t.Fatal(err)
	}
	if value.Type() == reflect.TypeFor[time.Time]() || value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
		return value
	}
	result := reflect.New(value.Type()).Elem()
	result.Set(value)
	if value.Kind() == reflect.Map {
		err = node.MapEntries(t.Context(), func(key, child metadataLogicalNode) error {
			native, err := key.Value(t.Context(), int(key.cost))
			if err != nil {
				return err
			}
			result.SetMapIndex(native, scaleP21SW138Value(t, child, replacements))
			return nil
		})
	} else {
		err = node.Children(t.Context(), func(key reflect.Value, child metadataLogicalNode) error {
			next := scaleP21SW138Value(t, child, replacements)
			switch result.Kind() {
			case reflect.Struct:
				result.FieldByName(key.String()).Set(next)
			case reflect.Slice, reflect.Array:
				result.Index(int(key.Int())).Set(next)
			case reflect.Pointer:
				result.Elem().Set(next)
			case reflect.Interface:
				result.Set(next)
			}
			return nil
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW138Materialize(t *testing.T, output *metadataLogicalSource, replacements map[int64]metadataLogicalNode) domain.Bootstrap {
	t.Helper()
	result := domain.Bootstrap{}
	value := reflect.ValueOf(&result).Elem()
	for index := range value.NumField() {
		value.Field(index).Set(scaleP21SW138Value(t, output.fields[index], replacements))
	}
	return result
}

func scaleP21SW138Equal(t *testing.T, projection *bootstrapBodyProjection, projected, want domain.Bootstrap) {
	t.Helper()
	output, replacements, err := projection.bind(t.Context(), projected)
	if err != nil {
		t.Fatal(err)
	}
	actual := scaleP21SW138Materialize(t, output, replacements)
	if !reflect.DeepEqual(actual, want) {
		t.Fatal("native projected values differ from literal transform")
	}
	before, beforeErr := json.Marshal(want)
	after, afterErr := json.Marshal(actual)
	if beforeErr != nil || afterErr != nil || string(before) != string(after) {
		t.Fatal("projected JSON changed", beforeErr, afterErr)
	}
}

func scaleP21SW138Fixture() domain.Bootstrap {
	at := time.Date(2026, 11, 14, 1, 2, 3, 456, time.UTC)
	actor := domain.User{ID: "actor", Name: "Original actor"}
	comment := domain.Comment{ID: "comment", Body: "comment body\xff", BodyData: map[string]any{"text": "comment body"}, User: actor,
		QuotedText: "inspected quote", ThreadSummary: &domain.CommentThreadSummary{Content: "inspected summary"},
		Reactions: map[string][]string{"reaction": {"actor"}}, CreatedAt: at}
	return domain.Bootstrap{
		Workspace: domain.Workspace{ID: "workspace", URLKey: "workspace"},
		Projects: []domain.Project{{ID: "duplicate", Name: "same header", Description: "first description", TeamIDs: []string{"private"},
			Lead: &actor, Creator: &actor, Comments: []domain.Comment{comment},
			DescriptionRevisions: []domain.ProjectDescriptionRevision{{ID: "revision", Description: "project revision", Author: actor, CreatedAt: at}}},
			{ID: "duplicate", Name: "same header", Description: "second description"},
			{ID: "", Description: "", Comments: []domain.Comment{{BodyData: map[string]any{}}}}},
		Documents: []domain.Document{{ID: "document", Content: "document content", ContentState: "document state", ContentData: map[string]any{"text": "document"},
			Creator: actor, TeamIDs: []string{"private"}, ProjectIDs: []string{}, PreviousSlugIDs: []string{"old-document"},
			Revisions: []domain.DocumentRevision{{ID: "revision", Content: "revision content", ContentState: "revision state",
				ContentData: map[string]any{"text": "revision"}, Author: actor, AuthorIDs: []string{"actor"}, CreatedAt: at, StartedAt: &at}}},
			{ID: "document", ContentData: map[string]any{}, Revisions: []domain.DocumentRevision{}},
			{ID: "", ContentData: nil, Revisions: nil}},
		Initiatives: []domain.Initiative{{ID: "initiative", Description: "initiative description", Creator: actor, Comments: []domain.Comment{comment},
			DescriptionHistory: []domain.InitiativeDescriptionRevision{{ID: "revision", Description: "initiative revision", Editor: actor, EditedAt: at}}}},
		Loops: []domain.Loop{{ID: "loop", Name: "Loop", Instructions: "loop instructions", Description: "real description",
			InstructionsData: map[string]any{"inspected": "instructions data"}, TriggerConfig: map[string]any{"event": "created"}}},
		Trash: []domain.TrashEntry{
			{ID: "duplicate", Title: "Same title", Payload: json.RawMessage(" {\"text\":\"first\"} \n"), DeletedBy: actor},
			{ID: "duplicate", Title: "Same title", Payload: json.RawMessage("[1, 2]"), DeletedBy: actor},
			{ID: "nil-payload", Payload: nil},
		},
	}
}

func TestScaleP21SW138BootstrapBodyProjectionLiteral(t *testing.T) {
	data := scaleP21SW138Fixture()
	view, source := scaleP21SW138Source(t, data)
	projection := scaleP21SW138Begin(t, view, source)
	if len(projection.origins) != 18 || len(projection.rawOrigins) != 2 {
		t.Fatal("opaque field coverage changed", len(projection.origins), len(projection.rawOrigins))
	}
	for _, origin := range projection.ranges {
		if origin.path == "" || !strings.HasPrefix(origin.token, projection.prefix) {
			t.Fatal("origin omitted exact ordinal provenance")
		}
	}
	if projection.Data.Projects[0].Name != data.Projects[0].Name ||
		projection.Data.Projects[0].Lead.Name != data.Projects[0].Lead.Name ||
		projection.Data.Projects[0].Comments[0].QuotedText != data.Projects[0].Comments[0].QuotedText ||
		projection.Data.Loops[0].TriggerConfig["event"] != "created" {
		t.Fatal("an inspected header or actor was replaced")
	}
	if projection.Data.Documents[1].ContentData == nil || projection.Data.Documents[2].ContentData != nil ||
		projection.Data.Documents[1].Revisions == nil || projection.Data.Documents[2].Revisions != nil ||
		projection.Data.Projects[2].Description != "" {
		t.Fatal("nil/empty native containers or strings changed")
	}
	node, found := projection.originalString(projection.Data.Loops[0].Instructions)
	if !found {
		t.Fatal("loop raw text source unavailable")
	}
	if equal, err := node.StringEqual(t.Context(), data.Loops[0].Instructions); err != nil || !equal {
		t.Fatal("raw loop text origin changed", err)
	}
	for _, text := range []string{"", "ordinary", strings.Clone(projection.Data.Loops[0].Instructions)} {
		if _, found := projection.originalString(text); found {
			t.Fatal("non-origin string gained authority")
		}
	}
	for token := range projection.Data.Documents[0].ContentData {
		if _, found := projection.originalString(token); found {
			t.Fatal("map marker claimed a string source")
		}
	}
	scaleP21SW138Equal(t, projection, cloneBootstrap(projection.Data), data)
	if view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("projection hydrated workspace metadata")
	}
	projection.Close()
	projection.Close()
	if !reflect.DeepEqual(projection.Data, domain.Bootstrap{}) || projection.input != nil || projection.output != nil ||
		projection.origins != nil || projection.replacements != nil || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("close retained projection ownership")
	}
	if _, found := projection.originalString("ordinary"); found {
		t.Fatal("closed projection retained authority")
	}
}

func TestScaleP21SW138BootstrapBodyProjectionTransforms(t *testing.T) {
	for _, mode := range []string{"reorder-duplicate", "clear", "new-native", "drop-all"} {
		t.Run(mode, func(t *testing.T) {
			data := scaleP21SW138Fixture()
			view, source := scaleP21SW138Source(t, data)
			projection := scaleP21SW138Begin(t, view, source)
			transform := func(value domain.Bootstrap) domain.Bootstrap {
				value = cloneBootstrap(value)
				switch mode {
				case "reorder-duplicate":
					value.Projects = []domain.Project{value.Projects[1], value.Projects[0], value.Projects[1]}
					value.Documents = []domain.Document{value.Documents[2], value.Documents[0], value.Documents[0]}
					value.Initiatives[0].Comments = append(value.Initiatives[0].Comments, value.Projects[1].Comments[0])
					value.Trash = []domain.TrashEntry{value.Trash[1], value.Trash[0], value.Trash[1], value.Trash[2]}
				case "clear":
					value.Projects[0].Description = ""
					value.Documents[0].ContentData = map[string]any{}
					value.Documents[0].Revisions[0].ContentState = ""
					value.Initiatives[0].Comments = nil
					value.Loops[0].Instructions = ""
					value.Loops[0].Description = ""
					value.Loops[0].InstructionsData = map[string]any{}
					value.Trash[0].Payload = nil
				case "new-native":
					value.Projects[0].Description = "a new description"
					value.Documents[0].ContentData = map[string]any{"text": "a new body", "raw": []byte("ordinary bytes")}
					value.Documents[0].ContentState = "new-state"
					value.Initiatives[0].Description = "new initiative"
					value.Trash[0].Payload = json.RawMessage(`{"new":true}`)
				case "drop-all":
					value.Projects, value.Documents, value.Initiatives, value.Loops = nil, nil, nil, nil
					value.Trash = nil
				}
				return value
			}
			scaleP21SW138Equal(t, projection, transform(projection.Data), transform(data))
		})
	}
}

func TestScaleP21SW138BootstrapBodyProjectionRejectsLostProvenance(t *testing.T) {
	cases := []struct {
		name string
		edit func(*domain.Bootstrap, *bootstrapBodyProjection)
	}{
		{"copied-string", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Projects[0].Description = strings.Clone(data.Projects[0].Description)
		}},
		{"substring", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Projects[0].Description = data.Projects[0].Description[1:]
		}},
		{"wrong-field", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Projects[0].Name = data.Projects[0].Description
		}},
		{"malformed-map", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Documents[0].ContentData["extra"] = "value"
		}},
		{"changed-map-value", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			for key := range data.Documents[0].ContentData {
				data.Documents[0].ContentData[key] = "changed"
			}
		}},
		{"copied-map-value", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			for key := range data.Documents[0].ContentData {
				data.Documents[0].ContentData[key] = strings.Clone(key)
			}
		}},
		{"raw-bytes", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Documents[0].ContentData = map[string]any{"raw": []byte(data.Documents[0].Content)}
		}},
		{"raw-array", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			var bytes [128]byte
			copy(bytes[:], data.Documents[0].Content)
			data.Documents[0].ContentData = map[string]any{"raw": bytes}
		}},
		{"unknown-current-marker", func(data *domain.Bootstrap, projection *bootstrapBodyProjection) {
			data.Projects[0].Description = projection.prefix + "123456789"
		}},
		{"nested-map", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Documents[0].ContentData = map[string]any{"nested": data.Documents[0].Content}
		}},
		{"raw-unknown-current-marker", func(data *domain.Bootstrap, projection *bootstrapBodyProjection) {
			data.Trash[0].Payload = json.RawMessage(`"` + projection.rawPrefix + `123456789"`)
		}},
		{"raw-malformed", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Trash[0].Payload = append(data.Trash[0].Payload, ' ')
		}},
		{"raw-substring", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Trash[0].Payload = data.Trash[0].Payload[1:]
		}},
		{"raw-wrong-field", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Documents[0].ContentData = map[string]any{"raw": data.Trash[0].Payload}
		}},
		{"raw-to-string", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Projects[0].Description = string(data.Trash[0].Payload)
		}},
		{"raw-to-bytes", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Documents[0].ContentData = map[string]any{"raw": []byte(data.Trash[0].Payload)}
		}},
		{"string-to-raw", func(data *domain.Bootstrap, _ *bootstrapBodyProjection) {
			data.Trash[0].Payload = json.RawMessage(data.Documents[0].Content)
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			view, source := scaleP21SW138Source(t, scaleP21SW138Fixture())
			projection := scaleP21SW138Begin(t, view, source)
			data := cloneBootstrap(projection.Data)
			item.edit(&data, projection)
			output, replacements, err := projection.bind(t.Context(), data)
			if output != nil || replacements != nil || !errors.Is(err, ErrBoundedMetadataCoverage) ||
				!projection.closed || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("invalid provenance escaped or retained ownership", err)
			}
			if err := source.Retain(); err != nil {
				t.Fatal("refused projection released caller's source", err)
			}
			source.Release()
		})
	}
}

func TestScaleP21SW138BootstrapBodyProjectionNativeMarkerText(t *testing.T) {
	literal := bootstrapBodyMarkerPrefix + strings.Repeat("f", 32) + ":4"
	data := scaleP21SW138Fixture()
	data.Projects[0].Name = literal
	data.Loops[0].TriggerConfig[literal] = literal
	data.Loops[0].TriggerConfig["almost"] = bootstrapBodyMarkerPrefix + strings.Repeat("x", 32) + ":4"
	view, source := scaleP21SW138Source(t, data)
	projection := scaleP21SW138Begin(t, view, source)
	scaleP21SW138Equal(t, projection, cloneBootstrap(projection.Data), data)
}

func TestScaleP21SW138BootstrapBodyProjectionDerivedForeignNonceLiteral(t *testing.T) {
	literal := bootstrapBodyMarkerPrefix + strings.Repeat("f", 32) + ":1"
	rawLiteral := `"` + bootstrapBodyRawMarkerPrefix + strings.Repeat("e", 32) + `:1"`
	for _, mode := range []string{"description", "bytes", "raw"} {
		t.Run(mode, func(t *testing.T) {
			data := domain.Bootstrap{
				Loops:     []domain.Loop{{ID: "loop", Instructions: literal}},
				Documents: []domain.Document{{ID: "document"}},
				Trash:     []domain.TrashEntry{{ID: "trash"}},
			}
			view, source := scaleP21SW138Source(t, data)
			projection := scaleP21SW138Begin(t, view, source)
			if strings.HasPrefix(literal, projection.prefix) || strings.Contains(rawLiteral, projection.rawPrefix) {
				t.Fatal("fixture nonce coincided with the fresh projection nonce")
			}
			node, found := projection.originalString(projection.Data.Loops[0].Instructions)
			if !found {
				t.Fatal("opaque instructions lost their source")
			}
			original, err := node.Value(t.Context(), int(node.cost))
			if err != nil {
				t.Fatal(err)
			}
			projected := cloneBootstrap(projection.Data)
			switch mode {
			case "description":
				// The route streams these opaque instruction bytes into a new
				// description string; their backing identity is deliberately new.
				derived := strings.Clone("Derived: " + original.String())
				projected.Loops[0].Description, data.Loops[0].Description = derived, derived
			case "bytes":
				derived := []byte(original.String())
				projected.Documents[0].ContentData = map[string]any{"derived": derived}
				data.Documents[0].ContentData = map[string]any{"derived": derived}
			case "raw":
				projected.Trash[0].Payload = json.RawMessage(rawLiteral)
				data.Trash[0].Payload = json.RawMessage(rawLiteral)
			}
			scaleP21SW138Equal(t, projection, projected, data)
		})
	}
}

func TestScaleP21SW138BootstrapBodyProjectionRawSemantics(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("null"), json.RawMessage(" \n { \"value\": 1 } \t"), json.RawMessage("{")} {
		data := domain.Bootstrap{Trash: []domain.TrashEntry{{ID: "raw", Payload: raw}}}
		view, source := scaleP21SW138Source(t, data)
		projection := scaleP21SW138Begin(t, view, source)
		projected := cloneBootstrap(projection.Data)
		if (projected.Trash[0].Payload == nil) != (raw == nil) || (len(projected.Trash[0].Payload) == 0) != (len(raw) == 0) {
			t.Fatal("raw nil/empty status changed")
		}
		output, replacements, err := projection.bind(t.Context(), projected)
		if err != nil {
			t.Fatal(err)
		}
		actual := scaleP21SW138Materialize(t, output, replacements)
		if !reflect.DeepEqual(data, actual) {
			t.Fatal("raw bytes changed, including whitespace/invalid content")
		}
		wantJSON, wantErr := json.Marshal(data)
		actualJSON, actualErr := json.Marshal(actual)
		if (wantErr == nil) != (actualErr == nil) || string(wantJSON) != string(actualJSON) ||
			wantErr != nil && wantErr.Error() != actualErr.Error() {
			t.Fatal("raw JSON behavior changed", wantErr, actualErr)
		}
		projection.Close()
	}
}

func TestScaleP21SW138BootstrapBodyProjectionLoopEmpty(t *testing.T) {
	data := domain.Bootstrap{Loops: []domain.Loop{
		{ID: "nil", Instructions: "", Description: "", InstructionsData: nil},
		{ID: "empty", Instructions: "", Description: "", InstructionsData: map[string]any{}},
		{ID: "whitespace", Instructions: " ", Description: " ", InstructionsData: map[string]any{"value": nil}},
	}}
	view, source := scaleP21SW138Source(t, data)
	projection := scaleP21SW138Begin(t, view, source)
	for index, loop := range data.Loops {
		header := projection.Data.Loops[index]
		if (header.Instructions == "") != (loop.Instructions == "") || (header.Description == "") != (loop.Description == "") ||
			(header.InstructionsData == nil) != (loop.InstructionsData == nil) || (len(header.InstructionsData) == 0) != (len(loop.InstructionsData) == 0) {
			t.Fatal("loop zero/nonzero or nil/empty status changed")
		}
	}
	scaleP21SW138Equal(t, projection, cloneBootstrap(projection.Data), data)
}

func TestScaleP21SW138BootstrapBodyProjectionOmittedRoots(t *testing.T) {
	names := []string{"issues", "comments", "activities", "notifications", "notificationDeliveries", "projectUpdates", "initiativeUpdates"}
	var baseWork int
	for _, scale := range []int{1, 10} {
		body := strings.Repeat("omitted legal body ", 32768)
		data := domain.Bootstrap{
			Workspace: domain.Workspace{ID: "retained", URLKey: "retained"},
			Projects:  []domain.Project{{ID: "retained", Description: "retained description"}},
			Comments:  map[string][]domain.Comment{}, Activities: map[string][]domain.ActivityEvent{},
			ProjectUpdates: map[string][]domain.ProjectUpdate{}, InitiativeUpdates: map[string][]domain.InitiativeUpdate{},
		}
		for range scale {
			data.Issues = append(data.Issues, domain.Issue{ID: "duplicate", Description: body})
			data.Comments["duplicate"] = append(data.Comments["duplicate"], domain.Comment{Body: body})
			data.Activities["duplicate"] = append(data.Activities["duplicate"], domain.ActivityEvent{ID: "duplicate", Type: body})
			data.Notifications = append(data.Notifications, domain.Notification{ID: "duplicate", Text: body})
			data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: "duplicate", Error: body})
			data.ProjectUpdates["duplicate"] = append(data.ProjectUpdates["duplicate"], domain.ProjectUpdate{Body: body})
			data.InitiativeUpdates["duplicate"] = append(data.InitiativeUpdates["duplicate"], domain.InitiativeUpdate{Body: body})
		}
		view, source := scaleP21SW138Source(t, data)
		projection, err := view.beginBootstrapBodyProjection(t.Context(), source, names...)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(projection.Close)
		projected := reflect.ValueOf(projection.Data)
		for _, name := range names {
			index, _, found := metadataLogicalField(name)
			if !found || !projected.Field(index).IsZero() {
				t.Fatal("omitted field did not remain its exact typed zero", name)
			}
		}
		if scale == 1 {
			baseWork = view.MetadataWorkUsedForTesting()
		} else if view.MetadataWorkUsedForTesting() != baseWork {
			t.Fatal("omitted collection growth consumed projection work", baseWork, view.MetadataWorkUsedForTesting())
		}
		data.Issues, data.Comments, data.Activities, data.Notifications, data.NotificationDeliveries, data.ProjectUpdates, data.InitiativeUpdates = nil, nil, nil, nil, nil, nil, nil
		scaleP21SW138Equal(t, projection, cloneBootstrap(projection.Data), data)
		projection.Close()
		if refused, err := view.beginBootstrapBodyProjection(t.Context(), source, "unknown-field"); refused != nil ||
			!errors.Is(err, ErrMetadataRecordQuery) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("unknown omission escaped or leaked", err)
		}
	}
}

type scaleP21SW138CancelContext struct {
	context.Context
	calls int
	after int
}

func (ctx *scaleP21SW138CancelContext) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.after {
		return context.Canceled
	}
	return ctx.Context.Err()
}

func TestScaleP21SW138BootstrapBodyProjectionAdmission(t *testing.T) {
	view, source := scaleP21SW138Source(t, scaleP21SW138Fixture())
	if projection, err := (&SQLiteStore{}).beginBootstrapBodyProjection(t.Context(), source); projection != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("disabled store enabled projection", err)
	}
	if projection, err := view.beginBootstrapBodyProjection(t.Context(), nil); projection != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("nil source enabled projection", err)
	}
	for _, after := range []int{1, 25, 200} {
		ctx := &scaleP21SW138CancelContext{Context: t.Context(), after: after}
		projection, err := view.beginBootstrapBodyProjection(ctx, source)
		if projection != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled build escaped or retained work", after, ctx.calls, err)
		}
	}
	projection := scaleP21SW138Begin(t, view, source)
	buildWork := view.MetadataWorkUsedForTesting()
	output, _, err := projection.bind(t.Context(), cloneBootstrap(projection.Data))
	if err != nil {
		t.Fatal(err)
	}
	bindWork := view.MetadataWorkUsedForTesting()
	if output == nil || bindWork <= buildWork {
		t.Fatal("binding had no independent admitted output")
	}
	if second, _, err := projection.bind(t.Context(), domain.Bootstrap{}); second != nil || !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("second binding accepted", err)
	}
	if _, err := output.fields[0].Value(t.Context(), int(output.fields[0].cost)); err != nil {
		t.Fatal("second bind invalidated borrowed output", err)
	}
	projection.Close()
	budget, originalLimit := view.metadataResidency.work, view.metadataResidency.work.limit
	for _, limit := range []int{1, 2 << 20, buildWork - 1} {
		budget.limit = limit
		projection, err := view.beginBootstrapBodyProjection(t.Context(), source)
		if projection != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial build refusal escaped or leaked", limit, err)
		}
	}
	budget.limit = originalLimit
	for _, after := range []int{1, 25, 200} {
		projection = scaleP21SW138Begin(t, view, source)
		ctx := &scaleP21SW138CancelContext{Context: t.Context(), after: after}
		output, replacements, err := projection.bind(ctx, cloneBootstrap(projection.Data))
		if output != nil || replacements != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled bind escaped or retained work", after, ctx.calls, err)
		}
	}
	projection = scaleP21SW138Begin(t, view, source)
	budget.limit = bindWork - 1
	if output, _, err := projection.bind(t.Context(), cloneBootstrap(projection.Data)); output != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("partial bind refusal escaped or leaked", err)
	}
	budget.limit = originalLimit
	if err := source.Retain(); err != nil {
		t.Fatal("refusal lost caller source", err)
	}
	source.Release()
}

func TestScaleP21SW138BootstrapBodyProjectionUnavailableSource(t *testing.T) {
	data := domain.Bootstrap{Projects: []domain.Project{{ID: "project", Description: "opaque body",
		UpdatedAt: time.Date(2026, 11, 14, 1, 2, 3, 4, time.FixedZone("unaudited-source", 7331))}}}
	view, source := scaleP21SW138Source(t, data)
	projection, err := view.beginBootstrapBodyProjection(t.Context(), source)
	if projection != nil || !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unavailable inspected native value escaped or leaked", err)
	}
}

func TestScaleP21SW138BootstrapBodyProjectionAuditedTimestamps(t *testing.T) {
	var data domain.Bootstrap
	if err := json.Unmarshal([]byte(`{"projects":[{"id":"project","description":"body","createdAt":"2026-11-14T01:02:03.000000004+05:30","comments":[{"body":"comment","createdAt":"2026-11-14T01:02:03+05:45"}]}]}`), &data); err != nil {
		t.Fatal(err)
	}
	builder, err := newMetadataLogicalBuilder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer builder.Close()
	value := reflect.ValueOf(data)
	for index := range value.NumField() {
		field := value.Type().Field(index)
		if field.Tag.Get("json") == "-" {
			continue
		}
		if err := builder.auditJSONLocations(value.Field(index)); err != nil {
			t.Fatal(err)
		}
		if err := builder.Field(jsonFieldName(field), value.Field(index)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := builder.Finish(copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Release)
	view := scaleP21HotLogoStore(t, nil)
	projection := scaleP21SW138Begin(t, view, source)
	scaleP21SW138Equal(t, projection, cloneBootstrap(projection.Data), data)
}

func TestScaleP21SW138BootstrapBodyProjectionIndependentLifetime(t *testing.T) {
	view := scaleP21HotLogoStore(t, nil)
	source, err := captureMetadataLogicalSource(t.Context(), scaleP21SW138Fixture(), copyStamp{})
	if err != nil {
		t.Fatal(err)
	}
	projection := scaleP21SW138Begin(t, view, source)
	source.Release()
	node, found := projection.originalString(projection.Data.Loops[0].Instructions)
	if !found {
		t.Fatal("source retain omitted")
	}
	if equal, err := node.StringEqual(t.Context(), "loop instructions"); err != nil || !equal {
		t.Fatal("caller release invalidated input", err)
	}
	output, _, err := projection.bind(t.Context(), cloneBootstrap(projection.Data))
	if err != nil {
		t.Fatal(err)
	}
	owned := output.fields[0]
	projection.Close()
	if _, err := owned.Value(t.Context(), int(owned.cost)); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("borrowed output survived owner close", err)
	}
	if _, err := node.StringEqual(t.Context(), "loop instructions"); !errors.Is(err, errMetadataLogicalClosed) {
		t.Fatal("input retain survived owner close", err)
	}
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("closed helper retained work")
	}
}

func TestScaleP21SW138BootstrapBodyProjectionLargeBodies(t *testing.T) {
	var firstWork int
	var firstOutput int64
	for _, scale := range []int{1, 10} {
		body := strings.Repeat("large legal body\xff", 65536*scale)
		raw := json.RawMessage(`{"text":"` + strings.Repeat("legal raw body ", 65536*scale) + `"}`)
		data := domain.Bootstrap{
			Projects:  []domain.Project{{ID: "project", Description: body, Comments: []domain.Comment{{Body: body, BodyData: map[string]any{"large": body}}}}},
			Documents: []domain.Document{{ID: "document", Content: body, ContentState: body, ContentData: map[string]any{"large": body}}},
			Loops:     []domain.Loop{{ID: "loop", Instructions: body, Description: body, InstructionsData: map[string]any{"large": body}}},
			Trash:     []domain.TrashEntry{{ID: "trash", Payload: raw}},
		}
		view, source := scaleP21SW138Source(t, data)
		statements := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { statements++ })
		projection, err := view.beginBootstrapBodyProjection(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(projection.Close)
		node, found := projection.originalString(projection.Data.Loops[0].Instructions)
		if !found || node.cost <= uint64(len(body)) {
			t.Fatal("large body source missing")
		}
		if equal, err := node.StringEqual(t.Context(), body); err != nil || !equal {
			t.Fatal("large body value changed", err)
		}
		output, replacements, err := projection.bind(ctx, cloneBootstrap(projection.Data))
		if err != nil || len(replacements) != 10 {
			t.Fatal("large body bind changed", len(replacements), err)
		}
		var outputSize int64
		for _, field := range output.fields {
			outputSize += field.size
		}
		work := view.MetadataWorkUsedForTesting()
		if scale == 1 {
			firstWork, firstOutput = work, outputSize
		} else if work != firstWork || outputSize != firstOutput {
			t.Fatal("opaque body growth increased header work/output", firstWork, work, firstOutput, outputSize)
		}
		if statements != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("body projection read SQL/full metadata", statements)
		}
		t.Logf("scale=%d body=%d admitted=%d output=%d", scale, len(body), work, outputSize)
		projection.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("large projection retained work")
		}
	}
}
