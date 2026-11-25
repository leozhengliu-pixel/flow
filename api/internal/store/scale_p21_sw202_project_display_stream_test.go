package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW202DisplayInput(t *testing.T, bounded *SQLiteStore, before domain.Bootstrap) domain.Bootstrap {
	t.Helper()
	source, err := captureMetadataLogicalSource(t.Context(), before, copyStamp{workspaceID: before.Workspace.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	display, err := bounded.refreshLegacyDisplayImage(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer display.Release()
	owner := &ResourceRead{store: bounded}
	defer owner.Close()
	if err := owner.reserve(t.Context(), 512<<10); err != nil {
		t.Fatal(err)
	}
	next := cloneBootstrap(before)
	index, _, _ := metadataLogicalField("projects")
	if err := display.fields[index].Children(t.Context(), func(ordinal reflect.Value, node metadataLogicalNode) error {
		value, body, err := legacyProjectProgressHeader(t.Context(), owner, node)
		if err != nil {
			return err
		}
		project := value.Interface().(domain.Project)
		original := before.Projects[int(ordinal.Int())]
		same, err := body.StringEqual(t.Context(), original.Description)
		if err != nil {
			return err
		}
		if !same {
			t.Fatal("literal display refresh changed an opaque description")
		}
		project.Description = original.Description
		next.Projects[int(ordinal.Int())] = project
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	literal := cloneBootstrap(before)
	refreshDisplayReferences(&literal)
	if !reflect.DeepEqual(next.Projects, literal.Projects) {
		t.Fatal("display-profile fixture did not enter the actual proven refresh helper")
	}
	return next
}

func TestScaleP21SW202ProjectDisplayFieldsClosed(t *testing.T) {
	for _, changed := range []string{"Lead", "Creator", "LeadID", "CreatorNil", "Name", "Description", "IssueCount", "UpdatedAt"} {
		before := domain.Bootstrap{Projects: []domain.Project{{
			ID: "sw202", Lead: &domain.User{ID: "lead", Name: "Old lead"},
			Creator: &domain.User{ID: "creator", Name: "Old creator"},
		}}}
		next := cloneBootstrap(before)
		switch changed {
		case "Lead":
			next.Projects[0].Lead.Name = "Changed display"
		case "Creator":
			next.Projects[0].Creator.Name = "Changed display"
		case "LeadID":
			next.Projects[0].Lead.ID = "another-lead"
		case "CreatorNil":
			next.Projects[0].Creator = nil
		case "Name":
			next.Projects[0].Name = "Changed"
		case "Description":
			next.Projects[0].Description = "Changed"
		case "IssueCount":
			next.Projects[0].IssueCount++
		case "UpdatedAt":
			next.Projects[0].UpdatedAt = next.Projects[0].UpdatedAt.AddDate(0, 0, 1)
		}
		plan, field := scaleP21SW196HeaderPlan(t, before, next, "projects")
		if err := plan.headers(t.Context(), field); err != nil {
			t.Fatal(err)
		}
		got, err := legacyProjectBodyPatchOnly(t.Context(), field.rows[0].before, field.rows[0].next, false)
		if err != nil || got != (changed == "Lead" || changed == "Creator") {
			t.Fatal("display profile accepted a field outside the literal refresh pass", changed, got, err)
		}
	}
}

func TestScaleP21SW202ProjectDisplayLegacyDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	fixture := scaleP09Database(t)
	bounded, err := fixture.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.releaseMetadataLogicalSources()
	for _, reload := range []bool{false, true} {
		for _, mode := range []string{"display-refresh", "display-actor", "display-canonical-name", "display-actor-canonical-name", "display-canonical-body", "display-canonical-reference", "display-missing-lead"} {
			smallPeak := 0
			for _, size := range []int{32, 5 << 20} {
				name := mode + map[bool]string{false: "/warm", true: "/reloaded"}[reload]
				if size > 32 {
					name += "/large"
				}
				t.Run(name, func(t *testing.T) {
					before := cloneBootstrap(fixture.data)
					current := cloneSnapshotValue(reflect.ValueOf(before.Viewer)).Interface().(domain.User)
					current.Name, current.JobTitle = "Current directory actor", "Current directory title"
					first := cloneSnapshotValue(reflect.ValueOf(current)).Interface().(domain.User)
					first.Name = "Earlier duplicate directory entry"
					stale := cloneSnapshotValue(reflect.ValueOf(current)).Interface().(domain.User)
					stale.Name, stale.JobTitle = "Stored stale project actor", "Stored stale title"
					lead := stale
					if mode == "display-missing-lead" {
						lead.ID += "-missing"
					}
					before.Users = []domain.User{first, current}
					if strings.Contains(mode, "actor") {
						before.Users = []domain.User{current}
					}
					before.Projects = []domain.Project{{
						ID: "sw202-" + before.Workspace.ID, Name: "Display project",
						Description: strings.Repeat("x", size) + "<tag>&\u2028",
						Lead:        &lead, Creator: &stale,
					}}
					if reload {
						raw, err := json.Marshal(before)
						if err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(raw, &before); err != nil {
							t.Fatal(err)
						}
					}
					next := scaleP21SW202DisplayInput(t, bounded, before)
					if strings.Contains(mode, "actor") {
						actor := cloneSnapshotValue(reflect.ValueOf(current)).Interface().(domain.User)
						actor.Name, actor.JobTitle = "Later request actor", "Later request title"
						next.Viewer, next.Users[len(next.Users)-1] = actor, actor
					}
					want, _ := scaleP21SW197Run(t, fixture, bounded, before, next, mode, false)
					got, peak := scaleP21SW197Run(t, fixture, bounded, before, next, mode, true)
					// A canonical name edit forces the literal rebase. Its
					// stale -> current display refresh then conflicts with the
					// later request actor used as canonical display authority.
					wantConflict := mode == "display-actor-canonical-name"
					validErrors := want.failure == nil && got.failure == nil
					if wantConflict {
						validErrors = errors.Is(want.failure, ErrConcurrentWrite) && errors.Is(got.failure, ErrConcurrentWrite)
					}
					literalErr, boundedErr := want.failure, got.failure
					want.failure, got.failure = nil, nil
					if !validErrors || !reflect.DeepEqual(got, want) || want.failed != wantConflict || got.failed != wantConflict {
						t.Fatal("display streaming changed stale actor source choice, native/SQL/search output or literal conflict", mode, reload, size, "literal error", literalErr, "bounded error", boundedErr)
					}
					if smallPeak == 0 {
						smallPeak = peak
					}
					if peak > smallPeak+(2<<20) {
						t.Fatal("display reference refresh admitted the opaque description", smallPeak, peak)
					}
					t.Logf("mode=%s reloaded=%t description_bytes=%d peak_work=%d", mode, reload, size, peak)
				})
			}
		}
	}
}
