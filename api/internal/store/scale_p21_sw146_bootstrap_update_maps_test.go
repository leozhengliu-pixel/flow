package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW146Fixture() domain.Bootstrap {
	actor := domain.User{ID: "actor", Name: "real actor"}
	at := time.Date(2026, 11, 15, 1, 2, 3, 4, time.UTC)
	comment := func(body string) domain.Comment {
		return domain.Comment{
			ID: "duplicate-comment", Body: body, BodyData: map[string]any{"text": body}, User: actor,
			QuotedText: "inspected quote", Reactions: map[string][]string{"reaction": {"actor"}}, CreatedAt: at,
		}
	}
	return domain.Bootstrap{
		ProjectUpdates: map[string][]domain.ProjectUpdate{
			"": {
				{ID: "duplicate", ProjectID: "same-project", Body: "first project body", BodyData: map[string]any{"text": "first data"},
					User: actor, Health: "on-track", CreatedAt: at, Comments: []domain.Comment{comment("first comment"), comment("second comment")},
					Snapshot: &domain.PulseSnapshot{CapturedAt: at, Status: "real snapshot", Milestones: []domain.PulseMilestoneSnapshot{{ID: "milestone", Name: "real milestone"}}},
					Diff:     &domain.PulseDiff{Status: &domain.PulseValueChange{From: "before", To: "after"}}},
				{ID: "duplicate", ProjectID: "same-project", Body: "second project body", User: actor, Comments: []domain.Comment{}},
			},
			" \x00\xffnative key ": {
				{ID: "duplicate", ProjectID: "same-project", Body: "other map entry", BodyData: map[string]any{}, Comments: nil},
			},
			"nil-slice": nil, "empty-slice": {},
			bootstrapBodyMarkerPrefix + strings.Repeat("f", 32) + ":123": {{ID: "duplicate", Body: "native marker-looking key"}},
		},
		InitiativeUpdates: map[string][]domain.InitiativeUpdate{
			"": {
				{ID: "duplicate", InitiativeID: "same-initiative", Body: "first initiative body", BodyData: map[string]any{"text": "initiative data"},
					User: actor, Health: "at-risk", Comments: []domain.Comment{comment("initiative comment")}, CreatedAt: at},
				{ID: "duplicate", InitiativeID: "same-initiative", Body: "second initiative body", Comments: nil},
			},
			"other-key": {{ID: "duplicate", Body: "", BodyData: map[string]any{}, Comments: []domain.Comment{}}},
		},
	}
}

func TestScaleP21SW146BootstrapUpdateMapDifferential(t *testing.T) {
	for _, mode := range []string{"identity", "rekey-reorder-duplicates", "prune", "new-native", "clear", "strip-snapshots", "empty", "nil"} {
		t.Run(mode, func(t *testing.T) {
			data := scaleP21SW146Fixture()
			view, source := scaleP21SW138Source(t, data)
			projection := scaleP21SW138Begin(t, view, source)
			if projection.Data.ProjectUpdates[""][0].User.Name != "real actor" ||
				projection.Data.ProjectUpdates[""][0].Comments[0].QuotedText != "inspected quote" ||
				projection.Data.InitiativeUpdates[""][0].Health != "at-risk" ||
				projection.Data.ProjectUpdates["nil-slice"] != nil || projection.Data.ProjectUpdates["empty-slice"] == nil {
				t.Fatal("inspected native fields or nil/empty map values changed")
			}
			transform := func(value domain.Bootstrap) domain.Bootstrap {
				value = cloneBootstrap(value)
				switch mode {
				case "rekey-reorder-duplicates":
					project := value.ProjectUpdates[""]
					project[0].Comments = []domain.Comment{project[0].Comments[1], project[0].Comments[0], project[0].Comments[1]}
					value.ProjectUpdates = map[string][]domain.ProjectUpdate{
						"rekeyed": {project[1], project[0], project[1]},
						"copied":  {project[0]},
					}
					initiative := value.InitiativeUpdates[""]
					value.InitiativeUpdates = map[string][]domain.InitiativeUpdate{"new-key": {initiative[1], initiative[0], initiative[0]}}
				case "prune":
					delete(value.ProjectUpdates, " \x00\xffnative key ")
					value.ProjectUpdates[""] = value.ProjectUpdates[""][1:]
					delete(value.InitiativeUpdates, "other-key")
				case "new-native":
					value.ProjectUpdates[""][0].Body = "new ordinary body"
					value.ProjectUpdates[""][0].BodyData = map[string]any{"new": []any{"ordinary", nil}}
					value.ProjectUpdates["new"] = []domain.ProjectUpdate{{ID: "new", Body: "new row"}}
					value.InitiativeUpdates[""][0].Comments[0].Body = "new comment"
				case "clear":
					value.ProjectUpdates[""][0].Body = ""
					value.ProjectUpdates[""][0].BodyData = nil
					value.ProjectUpdates[""][0].Comments = nil
					value.InitiativeUpdates[""][0].BodyData = map[string]any{}
					value.InitiativeUpdates[""][0].Comments[0].Body = ""
				case "strip-snapshots":
					for key, rows := range value.ProjectUpdates {
						for index := range rows {
							rows[index].Snapshot = nil
						}
						value.ProjectUpdates[key] = rows
					}
					for key, rows := range value.InitiativeUpdates {
						for index := range rows {
							rows[index].Snapshot = nil
						}
						value.InitiativeUpdates[key] = rows
					}
				case "empty":
					value.ProjectUpdates = map[string][]domain.ProjectUpdate{}
					value.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
				case "nil":
					value.ProjectUpdates, value.InitiativeUpdates = nil, nil
				}
				return value
			}
			scaleP21SW138Equal(t, projection, transform(projection.Data), transform(data))
			if view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("map projection decoded workspace metadata")
			}
			projection.Close()
			if view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("map projection retained work")
			}
		})
	}
}

func TestScaleP21SW146BootstrapUpdateMapSourceOrdinals(t *testing.T) {
	data := scaleP21SW146Fixture()
	view, source := scaleP21SW138Source(t, data)
	projection := scaleP21SW138Begin(t, view, source)
	for _, field := range []string{"projectUpdates", "initiativeUpdates"} {
		index, _, found := metadataLogicalField(field)
		if !found {
			t.Fatal("unknown field", field)
		}
		ordinal := 0
		err := source.fields[index].MapEntries(t.Context(), func(key, _ metadataLogicalNode) error {
			native, err := key.Value(t.Context(), int(key.cost))
			if err != nil {
				return err
			}
			root, token, body := "ProjectUpdates", "", ""
			if field == "projectUpdates" {
				if rows := projection.Data.ProjectUpdates[native.String()]; len(rows) > 0 {
					token, body = rows[0].Body, data.ProjectUpdates[native.String()][0].Body
				}
			} else {
				root = "InitiativeUpdates"
				if rows := projection.Data.InitiativeUpdates[native.String()]; len(rows) > 0 {
					token, body = rows[0].Body, data.InitiativeUpdates[native.String()][0].Body
				}
			}
			if body != "" {
				origin := projection.origins[bootstrapBodyStringIdentity(token)]
				if origin == nil || origin.path != fmt.Sprintf("%s{%d}[0].Body", root, ordinal) {
					t.Fatal("map provenance lost its original framed entry ordinal", root, ordinal)
				}
				if equal, err := origin.node.StringEqual(t.Context(), body); err != nil || !equal {
					t.Fatal("map origin points to another duplicate body", err)
				}
			}
			ordinal++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestScaleP21SW146BootstrapUpdateMapLargeKeys(t *testing.T) {
	key := strings.Repeat("large native map key ", 16000)
	data := domain.Bootstrap{ProjectUpdates: map[string][]domain.ProjectUpdate{
		key: {{ID: "duplicate", Body: "body", BodyData: map[string]any{"text": "data"},
			Comments: []domain.Comment{{Body: "comment"}}}},
	}}
	view, source := scaleP21SW138Source(t, data)
	projection := scaleP21SW138Begin(t, view, source)
	if len(key) <= 256<<10 || len(projection.Data.ProjectUpdates[key]) != 1 || view.MetadataWorkUsedForTesting() <= len(key)*8 {
		t.Fatal("large native key was truncated, skipped, or not admitted")
	}
	for _, origin := range projection.ranges {
		if len(origin.path) > 128 {
			t.Fatal("origin tracking copied a large native map key")
		}
	}
	buildWork := view.MetadataWorkUsedForTesting()
	scaleP21SW138Equal(t, projection, cloneBootstrap(projection.Data), data)
	projection.Close()
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	budget.limit = buildWork - 1
	refused, err := view.beginBootstrapBodyProjection(t.Context(), source)
	budget.limit = limit
	if refused != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("map key allocation escaped whole-graph admission", err)
	}
}

func TestScaleP21SW146BootstrapUpdateMapRejectsLostProvenance(t *testing.T) {
	cases := []struct {
		name string
		edit func(*domain.Bootstrap)
	}{
		{"copied-body", func(data *domain.Bootstrap) {
			data.ProjectUpdates[""][0].Body = strings.Clone(data.ProjectUpdates[""][0].Body)
		}},
		{"copied-comment", func(data *domain.Bootstrap) {
			data.InitiativeUpdates[""][0].Comments[0].Body = strings.Clone(data.InitiativeUpdates[""][0].Comments[0].Body)
		}},
		{"malformed-body-data", func(data *domain.Bootstrap) {
			data.ProjectUpdates[""][0].BodyData["extra"] = "new"
		}},
		{"wrong-field", func(data *domain.Bootstrap) {
			data.InitiativeUpdates[""][0].User.Name = data.InitiativeUpdates[""][0].Body
		}},
		{"map-key", func(data *domain.Bootstrap) {
			data.ProjectUpdates[data.ProjectUpdates[""][0].Body] = data.ProjectUpdates[""]
		}},
		{"unregistered-map", func(data *domain.Bootstrap) {
			data.Settings = map[string]any{"nested": data.ProjectUpdates[""][0].Body}
		}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			view, source := scaleP21SW138Source(t, scaleP21SW146Fixture())
			projection := scaleP21SW138Begin(t, view, source)
			data := cloneBootstrap(projection.Data)
			item.edit(&data)
			output, replacements, err := projection.bind(t.Context(), data)
			if output != nil || replacements != nil || !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("invalid map provenance escaped or leaked", err)
			}
		})
	}
}

func TestScaleP21SW146BootstrapUpdateMapCancellation(t *testing.T) {
	view, source := scaleP21SW138Source(t, scaleP21SW146Fixture())
	for _, after := range []int{1, 50, 200} {
		ctx := &scaleP21SW138CancelContext{Context: t.Context(), after: after}
		projection, err := view.beginBootstrapBodyProjection(ctx, source)
		if projection != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled map projection escaped or retained work", after, err)
		}
		projection = scaleP21SW138Begin(t, view, source)
		ctx = &scaleP21SW138CancelContext{Context: t.Context(), after: after}
		output, replacements, err := projection.bind(ctx, cloneBootstrap(projection.Data))
		if output != nil || replacements != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("canceled map bind escaped or retained work", after, err)
		}
	}
	if err := source.Retain(); err != nil {
		t.Fatal("map cancellation lost caller ownership", err)
	}
	source.Release()
}

func TestScaleP21SW146BootstrapUpdateMapLargeBodies(t *testing.T) {
	var baseWork int
	var baseOutput int64
	for _, scale := range []int{1, 10} {
		body := strings.Repeat("large map body ", 65536*scale)
		comment := domain.Comment{Body: body, BodyData: map[string]any{"text": body}, User: domain.User{ID: "actor"}}
		data := domain.Bootstrap{
			ProjectUpdates: map[string][]domain.ProjectUpdate{"project": {
				{ID: "duplicate", Body: body, BodyData: map[string]any{"text": body}, Comments: []domain.Comment{comment}},
			}},
			InitiativeUpdates: map[string][]domain.InitiativeUpdate{"initiative": {
				{ID: "duplicate", Body: body, BodyData: map[string]any{"text": body}, Comments: []domain.Comment{comment}},
			}},
		}
		view, source := scaleP21SW138Source(t, data)
		queries := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
		projection, err := view.beginBootstrapBodyProjection(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(projection.Close)
		output, replacements, err := projection.bind(ctx, cloneBootstrap(projection.Data))
		if err != nil || len(replacements) != 8 {
			t.Fatal("large body map bind changed", len(replacements), err)
		}
		var outputSize int64
		for _, field := range output.fields {
			outputSize += field.size
		}
		work := view.MetadataWorkUsedForTesting()
		if scale == 1 {
			baseWork, baseOutput = work, outputSize
		} else if work != baseWork || outputSize != baseOutput {
			t.Fatal("large update bodies increased header admission/output", baseWork, work, baseOutput, outputSize)
		}
		if queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("map projection read SQL/full metadata", queries)
		}
		if !reflect.DeepEqual(projection.Data.ProjectUpdates["project"][0].Comments[0].User, comment.User) {
			t.Fatal("inspected actor changed")
		}
		t.Logf("scale=%d body=%d admitted=%d output=%d", scale, len(body), work, outputSize)
		projection.Close()
		if view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("large update map retained work")
		}
	}
}
