package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func scaleP21SW105FirstGrant(document domain.Document, userID string) (domain.DocumentPermission, bool) {
	for _, permission := range document.Permissions {
		if permission.SubjectType == "user" && permission.SubjectID == userID {
			return permission, true
		}
	}
	return domain.DocumentPermission{}, false
}

func TestScaleP21SW105PresenceDocumentNativeValue(t *testing.T) {
	now := time.Now()
	large := strings.Repeat("Selected native document body. ", 20000)
	data := domain.Bootstrap{Documents: []domain.Document{
		{ID: "first", SlugID: "collision", PreviousSlugIDs: []string{"old"},
			Title: large, Content: large, ContentState: "state", ContentData: map[string]any{"integer": int64(7), "nested": []any{true, "value"}},
			Creator: domain.User{ID: "creator", Name: "Native creator"}, TeamIDs: []string{"private", "private", ""},
			InheritedTeamIDs: []string{}, ProjectIDs: []string{"project"}, SubscriberIDs: []string{"member"},
			ArchivedAt: &now, CreatedAt: now, UpdatedAt: now, Version: 7, ContentVersion: 3, CollaborationID: "collab",
			Revisions: []domain.DocumentRevision{{ID: "revision", Content: large}},
			Permissions: []domain.DocumentPermission{
				{ID: strings.Repeat("Ignored grant body.", 20000), SubjectType: "team", SubjectID: "member", Role: "owner"},
				{ID: "wrong-case", SubjectType: "User", SubjectID: "member", Role: "owner"},
				{ID: "first-member", DocumentID: "different-document", SubjectType: "user", SubjectID: "member", Role: "viewer", CreatedAt: now, UpdatedAt: now},
				{ID: "later-member", SubjectType: "user", SubjectID: "member", Role: "owner"},
				{ID: "empty-subject", SubjectType: "user", SubjectID: "", Role: ""},
				{ID: "native-subject", SubjectType: "user", SubjectID: "user\x00id", Role: "editor"},
			}},
		{ID: "collision", SlugID: "later-exact-id", Title: "later ID loses"},
		{ID: "first", SlugID: "duplicate", Title: "later duplicate loses"},
		{ID: "", SlugID: "", Permissions: nil},
		{ID: "empty", SlugID: "empty-permissions", Permissions: []domain.DocumentPermission{}},
	}}
	literal := &SQLiteStore{workspaces: map[string]domain.Bootstrap{"physical": data}, lastWorkspaceKey: "physical"}
	view := scaleP21HotLogoStore(t, literal.workspaces)
	view.lastWorkspaceKey = "physical"
	for _, ref := range []string{"first", "collision", "later-exact-id", "duplicate", "", "empty", "old", "missing"} {
		expected, matched := literal.DocumentByID("", ref)
		queries := 0
		ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
		read, found, err := view.BeginPresenceDocumentRead(ctx, "", ref)
		if err != nil || !found || read.Found != matched {
			t.Fatal(ref, found, err)
		}
		header := scaleP21SW64PresenceHeader(expected)
		for _, userID := range []string{"member", "", "user\x00id", "MEMBER", " member ", "missing"} {
			want, wantFound := scaleP21SW105FirstGrant(expected, userID)
			got, gotFound, err := read.FirstUserPermission(ctx, userID)
			if err != nil || gotFound != wantFound || !reflect.DeepEqual(got, want) {
				t.Fatal("first full native user grant differs", ref, userID, gotFound, wantFound, err)
			}
		}
		got, err := read.LoadDocument(ctx)
		if err != nil || !reflect.DeepEqual(got, expected) || !reflect.DeepEqual(read.Document, header) {
			t.Fatal("full native first document differs or changed header", ref, err)
		}
		if len(got.TeamIDs) != 0 {
			got.TeamIDs[0] = "caller mutation"
			got.ContentData["integer"] = "caller mutation"
			got.Permissions[0].Role = "caller mutation"
			again, err := read.LoadDocument(ctx)
			if err != nil || !reflect.DeepEqual(again, expected) || !reflect.DeepEqual(read.Document, header) {
				t.Fatal("native value aliased source/header", err)
			}
		}
		if queries != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("native point values consulted SQL/full metadata", queries)
		}
		read.Close()
		read.Close()
		if view.MetadataWorkUsedForTesting() != 0 || read.documentNode.source != nil {
			t.Fatal("native value close retained source/work")
		}
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			read, found, err := view.BeginPresenceDocumentRead(t.Context(), "", "collision")
			if err != nil || !found {
				t.Error(found, err)
				return
			}
			defer read.Close()
			document, err := read.LoadDocument(t.Context())
			grant, matched, grantErr := read.FirstUserPermission(t.Context(), "member")
			wantGrant, _ := scaleP21SW105FirstGrant(data.Documents[0], "member")
			if err != nil || grantErr != nil || !matched || !reflect.DeepEqual(document, data.Documents[0]) || !reflect.DeepEqual(grant, wantGrant) {
				t.Error("concurrent independent native handle differs", err, grantErr)
			}
		})
	}
	workers.Wait()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("concurrent native handles leaked")
	}
}

func TestScaleP21SW105PresenceDocumentPinnedGeneration(t *testing.T) {
	before := domain.Bootstrap{Documents: []domain.Document{{ID: "target", SlugID: "current", Content: "before",
		Permissions: []domain.DocumentPermission{{ID: "before", SubjectType: "user", SubjectID: "member", Role: "viewer"}}}}}
	after := domain.Bootstrap{Documents: []domain.Document{{ID: "target", SlugID: "current", Content: "after",
		Permissions: []domain.DocumentPermission{{ID: "after", SubjectType: "user", SubjectID: "member", Role: "owner"}}}}}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": before})
	old, _, err := view.BeginPresenceDocumentRead(t.Context(), "physical", "target")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), "physical")
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
	view.installMetadataLogicalSource("physical", source)
	for _, closeParent := range []bool{false, true} {
		if closeParent {
			parent.Close()
		}
		current, found, err := view.BeginPresenceDocumentRead(pinned, "physical", "current")
		if err != nil || !found {
			t.Fatal(found, err)
		}
		for _, pair := range []struct {
			read *PresenceDocumentRead
			want domain.Document
		}{{old, before.Documents[0]}, {current, after.Documents[0]}} {
			document, err := pair.read.LoadDocument(pinned)
			permission, matched, permissionErr := pair.read.FirstUserPermission(pinned, "member")
			if err != nil || permissionErr != nil || !matched || !reflect.DeepEqual(document, pair.want) ||
				!reflect.DeepEqual(permission, pair.want.Permissions[0]) {
				t.Fatal("owned occurrence changed or lookup inherited caller pin", closeParent, err, permissionErr)
			}
		}
		current.Close()
	}
	old.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("generation reads leaked")
	}
}

func TestScaleP21SW105PresenceDocumentWarmReloadBodyBound(t *testing.T) {
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
	expectedWork := map[bool]int{}
	stamp := time.Date(2026, 11, 15, 12, 34, 56, 789, time.UTC)
	for _, size := range []int{2000, 20000} {
		body := strings.Repeat("Unrelated document body. ", size)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.sw105_seed", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{
				{ID: "sw105-target", SlugID: "sw105-current", PreviousSlugIDs: []string{"sw105-old"}, Content: "selected",
					TeamIDs: []string{data.Teams[0].ID}, Creator: domain.User{ID: data.Viewer.ID},
					Permissions: []domain.DocumentPermission{{ID: "first", DocumentID: "other", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "viewer", CreatedAt: stamp, UpdatedAt: stamp},
						{ID: "later", SubjectType: "user", SubjectID: data.Viewer.ID, Role: "owner"}}},
				{ID: "sw105-current", Content: body, ContentData: map[string]any{"body": body}},
				{ID: "sw105-target", Content: body},
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
			for _, ref := range []string{"sw105-target", "sw105-current", "sw105-old", "missing"} {
				want, matched := f.DocumentByID(workspace, ref)
				queries := 0
				ctx := WithSQLStatementObserver(t.Context(), func(string) { queries++ })
				read, found, err := view.BeginPresenceDocumentRead(ctx, workspace, ref)
				if err != nil || !found || read.Found != matched {
					t.Fatal(size, reload, ref, found, err)
				}
				got, err := read.LoadDocument(ctx)
				grant, grantFound, grantErr := read.FirstUserPermission(ctx, f.data.Viewer.ID)
				wantGrant, wantGrantFound := scaleP21SW105FirstGrant(want, f.data.Viewer.ID)
				if err != nil || grantErr != nil || !reflect.DeepEqual(got, want) ||
					grantFound != wantGrantFound || !reflect.DeepEqual(grant, wantGrant) {
					t.Fatal("warm/reloaded full native document or grant differs", size, reload, ref, err, grantErr)
				}
				if ref == "sw105-target" {
					held := view.MetadataWorkUsedForTesting()
					if expectedWork[reload] == 0 {
						expectedWork[reload] = held
					}
					if held != expectedWork[reload] {
						t.Fatal("10x ignored bodies increased admitted work", size, reload, held, expectedWork[reload])
					}
					t.Log("ignored body size/admitted work", size, reload, held)
				}
				read.Close()
				if queries != 0 || view.WorkspaceMetadataDecodes() != 0 || view.MetadataWorkUsedForTesting() != 0 {
					t.Fatal("warm/reload values queried SQL, hydrated full workspace or leaked", queries)
				}
			}
		}
	}
}

func TestScaleP21SW105PresenceDocumentFailuresAndRelease(t *testing.T) {
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{"physical": {Documents: []domain.Document{
		{ID: "target", Content: strings.Repeat("Legal selected body. ", 100000),
			Permissions: []domain.DocumentPermission{{ID: "grant", SubjectType: "user", SubjectID: "member", Role: "editor"}}},
	}}})
	read, found, err := view.BeginPresenceDocumentRead(t.Context(), "physical", "target")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer read.Close()
	held := view.MetadataWorkUsedForTesting()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := read.LoadDocument(ctx); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, domain.Document{}) {
		t.Fatal("canceled full value escaped", err)
	}
	if got, found, err := read.FirstUserPermission(ctx, "member"); !errors.Is(err, context.Canceled) || found || !reflect.DeepEqual(got, domain.DocumentPermission{}) {
		t.Fatal("canceled grant escaped", found, err)
	}
	budget, limit := view.metadataResidency.work, view.metadataResidency.work.limit
	budget.limit = held
	if got, err := read.LoadDocument(t.Context()); !errors.Is(err, ErrMetadataWorkBudget) || !reflect.DeepEqual(got, domain.Document{}) {
		t.Fatal("unadmitted full value escaped", err)
	}
	if got, found, err := read.FirstUserPermission(t.Context(), "member"); !errors.Is(err, ErrMetadataWorkBudget) || found || !reflect.DeepEqual(got, domain.DocumentPermission{}) {
		t.Fatal("unadmitted grant escaped", found, err)
	}
	if _, found, err := read.FirstUserPermission(t.Context(), "missing"); err != nil || found {
		t.Fatal("permission miss demanded full-document admission", found, err)
	}
	budget.limit = limit
	if view.MetadataWorkUsedForTesting() != held {
		t.Fatal("canceled/refused value leaked work")
	}
	cost := read.documentNode.cost
	read.documentNode.cost = metadataWorkBytes
	if _, err := read.LoadDocument(t.Context()); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("oversized selected node escaped explicit bound", err)
	}
	read.documentNode.cost = cost
	document, err := read.LoadDocument(t.Context())
	if err != nil || len(document.Content) != len(strings.Repeat("Legal selected body. ", 100000)) ||
		view.MetadataWorkUsedForTesting() <= held {
		t.Fatal("legal large selected body failed or lacked retained admission", err)
	}
	if read.Document.Content != "" {
		t.Fatal("full value changed partial header")
	}
	read.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("close leaked value work")
	}
	for _, closed := range []*PresenceDocumentRead{nil, read} {
		if _, err := closed.LoadDocument(t.Context()); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed full value read succeeded", err)
		}
		if _, _, err := closed.FirstUserPermission(t.Context(), "member"); !errors.Is(err, errMetadataLogicalClosed) {
			t.Fatal("closed permission read succeeded", err)
		}
	}
}
