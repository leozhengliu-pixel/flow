package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

func scaleP21SW64PresenceHeader(document domain.Document) domain.Document {
	result := domain.Document{ID: document.ID, SlugID: document.SlugID, TeamIDs: document.TeamIDs,
		InheritedTeamIDs: document.InheritedTeamIDs, Creator: domain.User{ID: document.Creator.ID}}
	if document.Permissions != nil {
		result.Permissions = []domain.DocumentPermission{}
		for _, permission := range document.Permissions {
			result.Permissions = append(result.Permissions, domain.DocumentPermission{
				SubjectType: permission.SubjectType, SubjectID: permission.SubjectID, Role: permission.Role,
			})
		}
	}
	return result
}

func TestScaleP21SW64PresenceDocumentNative(t *testing.T) {
	large := strings.Repeat("Unrelated content and profile body. ", 20000)
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "organization", URLKey: "logical"}, Documents: []domain.Document{
		{ID: "first", SlugID: "collision", PreviousSlugIDs: []string{"old-slug", "old-000000000064"}, Title: large, Content: large,
			ContentData: map[string]any{"native": int64(7)}, Creator: domain.User{ID: "creator\x00id", Name: large, JobTitle: large},
			TeamIDs: []string{"private", "private", ""}, InheritedTeamIDs: []string{"inherited", "inherited"},
			Permissions: []domain.DocumentPermission{
				{ID: large, DocumentID: "other", SubjectType: "user", SubjectID: "member", Role: "editor"},
				{SubjectType: "team", SubjectID: "team\x00id", Role: "viewer"},
				{SubjectType: "user", SubjectID: "member", Role: "owner"},
				{SubjectType: "", SubjectID: "", Role: ""},
			}},
		{ID: "collision", SlugID: "later-exact-id", TeamIDs: []string{"later"}, Creator: domain.User{ID: "later"}},
		{ID: "first", SlugID: "later-duplicate", TeamIDs: []string{"later-duplicate"}},
		{ID: "", SlugID: "", TeamIDs: nil, InheritedTeamIDs: []string{}, Permissions: nil},
		{ID: "empty-slug", SlugID: "", TeamIDs: []string{"later-empty"}},
		{ID: "nil-shapes", SlugID: "nil-current"},
		{ID: "empty-shapes", SlugID: "empty-current", TeamIDs: []string{}, InheritedTeamIDs: []string{}, Permissions: []domain.DocumentPermission{}},
		{ID: "doc\x00id", SlugID: "slug\x00current", PreviousSlugIDs: []string{"old\x00slug"}, Creator: domain.User{ID: "native\x00creator"}, TeamIDs: []string{"team\x00id"}},
	}}
	physical := "physical\x00workspace"
	values := map[string]domain.Bootstrap{physical: data}
	literal := &SQLiteStore{workspaces: values, lastWorkspaceKey: physical}
	view := scaleP21HotLogoStore(t, values)
	view.lastWorkspaceKey = physical
	for _, workspace := range []string{physical, ""} {
		for _, ref := range []string{"first", "collision", "later-exact-id", "later-duplicate", "", "empty-slug", "nil-shapes", "empty-shapes", "doc\x00id", "slug\x00current", "old-slug", "old-000000000064", "000000000064", "old\x00slug", " first ", "FIRST", "missing"} {
			document, documentFound := literal.DocumentByID(workspace, ref)
			queries := 0
			ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
			read, workspaceFound, err := view.BeginPresenceDocumentRead(ctx, workspace, ref)
			if err != nil || !workspaceFound || read == nil || read.Found != documentFound || !reflect.DeepEqual(read.Document, scaleP21SW64PresenceHeader(document)) {
				t.Fatal("first installed ID/current-slug header differs", workspace, ref, documentFound, workspaceFound, err)
			}
			if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() == 0 || read.source == nil {
				t.Fatal("presence header lost its owner or consulted SQL/full metadata", queries)
			}
			if len(read.Document.TeamIDs) > 0 {
				read.Document.TeamIDs[0] = "caller replacement"
			}
			if len(read.Document.Permissions) > 0 {
				read.Document.Permissions[0].Role = "caller replacement"
			}
			read.Close()
			read.Close()
			if read.Found || !reflect.DeepEqual(read.Document, domain.Document{}) || read.source != nil || view.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("presence close retained source, header or admission")
			}
		}
	}
	for _, workspace := range []string{"logical", "physical", physical + " ", "missing"} {
		if read, found, err := view.BeginPresenceDocumentRead(t.Context(), workspace, "first"); read != nil || found || err != nil {
			t.Fatal("missing physical workspace changed", workspace, found, err)
		}
	}
	if read, _, err := (&SQLiteStore{}).BeginPresenceDocumentRead(t.Context(), "", "first"); read != nil || !errors.Is(err, ErrBoundedMetadataCoverage) {
		t.Fatal("literal store activated a bounded reader", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if read, _, err := view.BeginPresenceDocumentRead(ctx, "", "first"); read != nil || !errors.Is(err, context.Canceled) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("canceled read escaped output or leaked", err)
	}
	read, _, err := view.BeginPresenceDocumentRead(t.Context(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	held := view.MetadataWorkUsedForTesting()
	read.Close()
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	for _, ceiling := range []int{1, 128 << 10, held - 1} {
		budget.limit = ceiling
		read, _, err := view.BeginPresenceDocumentRead(t.Context(), "", "first")
		if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("partial presence admission escaped or leaked", ceiling, err)
		}
	}
	budget.limit = limit
	ctx, cancel = context.WithCancel(t.Context())
	read, found, err := view.BeginPresenceDocumentRead(ctx, "", "first")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	cancel()
	if _, err := json.Marshal(read.Document); err != nil || !read.Found || view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("cancellation released a still-used presence header", err)
	}
	read.Close()
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 3 {
				read, found, err := view.BeginPresenceDocumentRead(t.Context(), "", "collision")
				document, matched := literal.DocumentByID("", "collision")
				if err != nil || !found || read == nil {
					t.Error(found, err)
					return
				}
				if read.Found != matched || !reflect.DeepEqual(read.Document, scaleP21SW64PresenceHeader(document)) {
					t.Error("concurrent first-match header differs")
				}
				read.Close()
			}
		})
	}
	workers.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("concurrent presence reads leaked")
	}
}

func TestScaleP21SW64PresenceDocumentCurrentGeneration(t *testing.T) {
	physical, other := "physical", "other"
	before := domain.Bootstrap{Documents: []domain.Document{{ID: "target", SlugID: "current", TeamIDs: []string{"before"}, Creator: domain.User{ID: "before"}}}}
	after := domain.Bootstrap{Documents: []domain.Document{{ID: "target", SlugID: "current", TeamIDs: []string{"after"}, Creator: domain.User{ID: "after"}}}}
	otherData := domain.Bootstrap{Documents: []domain.Document{{ID: "target", SlugID: "current", TeamIDs: []string{"other"}}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{physical: before, other: otherData})
	view.lastWorkspaceKey = physical
	old, _, err := view.BeginPresenceDocumentRead(t.Context(), physical, "target")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), physical)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
	if err != nil {
		t.Fatal(err)
	}
	view.installMetadataLogicalSource(physical, source)
	for _, closedParent := range []bool{false, true} {
		if closedParent {
			parent.Close()
		}
		read, found, err := view.BeginPresenceDocumentRead(pinned, physical, "target")
		if err != nil || !found || !read.Found || !reflect.DeepEqual(read.Document, scaleP21SW64PresenceHeader(after.Documents[0])) {
			t.Fatal("presence inherited an incidental old/closed installed pin", closedParent, found, err)
		}
		read.Close()
	}
	view.mu.Lock()
	view.lastWorkspaceKey = other
	view.mu.Unlock()
	read, found, err := view.BeginPresenceDocumentRead(pinned, "", "target")
	if err != nil || !found || !read.Found || !reflect.DeepEqual(read.Document, scaleP21SW64PresenceHeader(otherData.Documents[0])) {
		t.Fatal("presence inherited a pinned default instead of current default", found, err)
	}
	read.Close()
	if !old.Found || !reflect.DeepEqual(old.Document, scaleP21SW64PresenceHeader(before.Documents[0])) {
		t.Fatal("registry replacement changed an already-owned header")
	}
	old.Close()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("current-generation reads leaked or hydrated metadata")
	}
}

func TestScaleP21SW64PresenceDocumentWarmReloadBodyBound(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	expected := map[bool]int{}
	for _, size := range []int{2000, 20000} {
		unused := strings.Repeat("Unused selected or unrelated document body. ", size)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.sw64_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{
				{ID: "sw64-target", SlugID: "sw64-current", PreviousSlugIDs: []string{"sw64-old"}, Title: unused, Content: unused,
					ContentState: unused, ContentData: map[string]any{"body": unused}, Creator: domain.User{ID: "creator", Name: unused, JobTitle: unused},
					TeamIDs: []string{data.Teams[0].ID}, InheritedTeamIDs: []string{},
					Permissions: []domain.DocumentPermission{{ID: "permission", DocumentID: "other", SubjectType: "user", SubjectID: "member", Role: "viewer"}},
					Revisions:   []domain.DocumentRevision{{Content: unused}}},
				{ID: "sw64-unrelated", SlugID: "unrelated", Title: unused, Content: unused, ContentData: map[string]any{"body": unused}},
			}
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
			for _, ref := range []string{"sw64-target", "sw64-current", "sw64-old", "missing"} {
				document, matched := f.DocumentByID(workspace, ref)
				queries := 0
				read, found, err := view.BeginPresenceDocumentRead(WithSQLStatementObserver(t.Context(), func(string) { queries++ }), workspace, ref)
				if err != nil || !found || read.Found != matched || !reflect.DeepEqual(read.Document, scaleP21SW64PresenceHeader(document)) {
					t.Fatal("warm/reloaded literal document header differs", size, reload, ref, err)
				}
				if ref == "sw64-target" {
					held := view.MetadataWorkUsedForTesting()
					if expected[reload] == 0 {
						expected[reload] = held
					}
					if held != expected[reload] {
						t.Fatal("10x excluded bodies increased presence work", reload, held, expected[reload])
					}
				}
				read.Close()
				if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("warm/reloaded presence used SQL/full decode or leaked", queries)
				}
			}
		}
	}
}
