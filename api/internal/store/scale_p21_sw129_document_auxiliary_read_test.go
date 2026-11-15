package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW129Document(data domain.Bootstrap, ref string) (domain.Document, bool) {
	for _, item := range data.Documents {
		if item.ID == ref || item.SlugID == ref {
			return item, true
		}
	}
	if ref != "" {
		for _, item := range data.Documents {
			if slices.Contains(item.PreviousSlugIDs, ref) {
				return item, true
			}
		}
	}
	return domain.Document{}, false
}

func scaleP21SW129History(data domain.Bootstrap, ref string) ([]domain.DocumentRevision, bool) {
	if document, found := scaleP21SW129Document(data, ref); found {
		return slices.Clone(document.Revisions), true
	}
	for _, entry := range data.Trash {
		var document domain.Document
		if entry.ResourceType == "document" && json.Unmarshal(entry.Payload, &document) == nil &&
			(document.ID == ref || document.SlugID == ref || slices.Contains(document.PreviousSlugIDs, ref)) {
			return slices.Clone(document.Revisions), true
		}
	}
	return nil, false
}

func TestScaleP21SW129DocumentAuxiliaryTrashJSONHeaders(t *testing.T) {
	repo := &SQLiteStore{metadataResidency: newMetadataResidency()}
	read := &DocumentRead{owner: &ResourceRead{store: repo}}
	defer read.Close()
	for _, raw := range []string{
		`null`,
		`{"ID":"case","SLUGID":"case-slug","PREVIOUSSLUGIDS":["case-old"]}`,
		`{"\u0049\u0044":"escaped","\u0053lugID":"escaped-slug"}`,
		`{"ID":"first","id":null,"iD":"last","SLUGID":"before","slugId":null,"PREVIOUSSLUGIDS":["old"],"previousSlugIds":null}`,
		`{"id":"valid","previousSlugIds":[null,"old"],"ignored":{"body":"unused"}}`,
		`{"id":"valid"} {"id":"trailing"}`,
		`{"id":"valid","previousSlugIds":42}`,
		`{"id":"valid","id":42,"id":"after"}`,
		`{"id":`,
		``,
		`[]`,
		`42`,
		`"text"`,
	} {
		var expected domain.Document
		oracleErr := json.Unmarshal([]byte(raw), &expected)
		visits := 0
		var got domain.Document
		err := read.documentJSONHeader(t.Context(), bytes.NewReader([]byte(raw)), 0, int64(len(raw)), func(header domain.Document) error {
			visits++
			got = header
			return nil
		})
		if oracleErr == nil {
			if err != nil || visits != 1 || !reflect.DeepEqual(got, expected) {
				t.Fatal("literal trash JSON header differs", raw, expected, got, visits, err)
			}
		} else if visits != 0 || err != nil && !documentAuxiliaryInvalidJSON(err) {
			t.Fatal("invalid literal trash JSON became a match or an infrastructure failure", raw, visits, err)
		}
		if repo.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("trash header scratch escaped its visitor", raw)
		}
	}
}

func TestScaleP21SW129DocumentAuxiliaryLiteralAndOwnership(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw129-")
	stamp := time.Date(2026, time.November, 15, 3, 4, 5, 123, time.UTC)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents", "documentContentDrafts", "trash"), workspace, "scale_p21.sw129_seed", "", nil, func(data *domain.Bootstrap) error {
		revisions := []domain.DocumentRevision{
			{ID: prefix + "first", DocumentID: prefix + "live", Title: "First stored", Content: "first", ContentData: map[string]any{"integer": int64(9007199254740993), "at": stamp}, Author: data.Viewer, AuthorIDs: []string{data.Viewer.ID, data.Viewer.ID, ""}, CreatedAt: stamp},
			{ID: prefix + "second", DocumentID: prefix + "live", Title: "Second stored", Content: strings.Repeat("selected history body ", 10000), Author: data.Viewer, CreatedAt: stamp.Add(time.Hour)},
			{ID: prefix + "third", DocumentID: prefix + "live", Title: "Third stored", Content: "third", Author: data.Viewer, CreatedAt: stamp.Add(-time.Hour)},
		}
		data.Documents = append(data.Documents,
			domain.Document{ID: prefix + "live", SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old", prefix + "collision"}, Creator: data.Viewer, Content: "current body", Revisions: revisions},
			domain.Document{ID: prefix + "second-live", SlugID: prefix + "collision", Creator: data.Viewer, Revisions: []domain.DocumentRevision{{ID: prefix + "collision-version", Title: "Current beats earlier previous slug", Author: data.Viewer, CreatedAt: stamp}}},
			domain.Document{ID: prefix + "nil-history", Creator: data.Viewer, Revisions: nil},
			domain.Document{ID: prefix + "empty-history", Creator: data.Viewer, Revisions: []domain.DocumentRevision{}},
		)
		data.DocumentContentDrafts = []domain.DocumentContentDraft{
			{ID: prefix + "draft-first", DocumentID: prefix + "live", UserID: data.Viewer.ID, Content: "first draft", ContentData: map[string]any{"integer": int64(9007199254740993), "at": stamp, "mutable": []any{"original"}}, Version: 7, CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour)},
			{ID: prefix + "draft-other-user", DocumentID: prefix + "live", UserID: prefix + "other-user", Content: strings.Repeat("excluded other-user draft ", 10000), Version: 8, CreatedAt: stamp},
			{ID: prefix + "draft-second", DocumentID: prefix + "live", UserID: data.Viewer.ID, Content: "second draft", ContentData: map[string]any{}, Version: 3, CreatedAt: stamp.Add(-time.Hour)},
			{ID: prefix + "draft-old-ref", DocumentID: prefix + "old", UserID: data.Viewer.ID, Content: "raw slug draft", CreatedAt: stamp},
			{ID: prefix + "unrelated", DocumentID: prefix + "elsewhere", UserID: data.Viewer.ID, Content: strings.Repeat("excluded unrelated draft ", 10000)},
		}
		deleted := domain.Document{ID: prefix + "deleted", SlugID: prefix + "deleted-current", PreviousSlugIDs: []string{prefix + "deleted-old"}, Creator: data.Viewer, Revisions: revisions}
		raw, err := json.Marshal(deleted)
		if err != nil {
			return err
		}
		data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "trash", ResourceType: "document", ResourceID: deleted.ID, Payload: raw, DeletedBy: data.Viewer, DeletedAt: stamp, Links: &domain.TrashLinks{}})
		raw, err = json.Marshal(map[string]any{
			"ID": prefix + "casefold", "SLUGID": prefix + "casefold-current", "PREVIOUSSLUGIDS": []string{prefix + "casefold-old"}, "REVISIONS": revisions,
		})
		if err != nil {
			return err
		}
		data.Trash = append(data.Trash, domain.TrashEntry{ID: prefix + "casefold-trash", ResourceType: "document", ResourceID: prefix + "casefold", Payload: raw, DeletedBy: data.Viewer, DeletedAt: stamp})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "nil-drafts", "empty-drafts", "reloaded", "unknown-revision"} {
		t.Run(state, func(t *testing.T) {
			switch state {
			case "nil-drafts", "empty-drafts":
				f.mu.Lock()
				data := f.workspaces[workspace]
				data.DocumentContentDrafts = nil
				if state == "empty-drafts" {
					data.DocumentContentDrafts = []domain.DocumentContentDraft{}
				}
				f.workspaces[workspace] = data
				f.mu.Unlock()
			case "reloaded":
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			case "unknown-revision":
				f.mu.Lock()
				f.forgetCopyRevisionLocked(workspace)
				f.mu.Unlock()
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			pairs, sqlStatements, maximumWork := 0, 0, 0
			for _, ref := range []string{prefix + "live", prefix + "current", prefix + "old", prefix + "collision", prefix + "nil-history", prefix + "empty-history", prefix + "deleted", prefix + "deleted-current", prefix + "deleted-old", prefix + "casefold", prefix + "casefold-current", prefix + "casefold-old", prefix + "trash", prefix + "missing"} {
				for _, drafts := range []bool{true, false} {
					want, err := f.PagedWorkspaceMetadata(t.Context(), workspace, f.data.Viewer.ID)
					if err != nil {
						t.Fatal(err)
					}
					observed := WithSQLStatementObserver(t.Context(), func(string) { sqlStatements++ })
					read, err := view.BeginDocumentAuxiliaryRead(observed, workspace, f.data.Viewer.ID, nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: drafts, History: !drafts})
					if err != nil {
						t.Fatal("begin auxiliary read", ref, drafts, err)
					}
					pairs++
					maximumWork = max(maximumWork, view.MetadataWorkUsedForTesting())
					if drafts {
						expected := slices.DeleteFunc(slices.Clone(want.DocumentContentDrafts), func(item domain.DocumentContentDraft) bool {
							return item.DocumentID != ref || item.UserID != want.Viewer.ID
						})
						if !reflect.DeepEqual(read.Data.DocumentContentDrafts, expected) {
							t.Fatalf("native draft shape/order/fields differ ref=%s\nwant=%#v\ngot=%#v", ref, expected, read.Data.DocumentContentDrafts)
						}
						if state == "warm" && ref == prefix+"live" {
							if _, ok := read.Data.DocumentContentDrafts[0].ContentData["integer"].(int64); !ok {
								t.Fatal("warm draft dynamic integer type was normalized")
							}
							read.Data.DocumentContentDrafts[0].ContentData["mutable"].([]any)[0] = "caller"
						}
					} else {
						expected, found := scaleP21SW129History(want, ref)
						got, gotFound := scaleP21SW129History(read.Data, ref)
						if found != gotFound {
							t.Fatal("live/trash auxiliary resolution differs", ref, found, gotFound)
						}
						if len(expected) != len(got) {
							t.Fatal("auxiliary revision count differs", ref, len(expected), len(got))
						}
						start, end := min(1, len(expected)), min(2, len(expected))
						if err := f.LoadDocumentRevisionBodies(t.Context(), workspace, expected[start:end]); err != nil {
							t.Fatal(err)
						}
						if err := read.LoadRevisionBodies(t.Context(), got[start:end]); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(expected, got) {
							t.Fatalf("native selected-page history differs ref=%s\nwant=%#v\ngot=%#v", ref, expected, got)
						}
					}
					if view.MetadataWorkUsedForTesting() == 0 || !read.document.owner.SourceRetainedForTesting() {
						t.Fatal("auxiliary values lost their source or work owner")
					}
					read.Close()
					read.Close()
					if view.MetadataWorkUsedForTesting() != 0 || !reflect.DeepEqual(read.Data, domain.Bootstrap{}) {
						t.Fatal("auxiliary close retained output/work")
					}
					if !errors.Is(read.LoadRevisionBodies(t.Context(), nil), errMetadataLogicalClosed) {
						t.Fatal("closed auxiliary read remained usable")
					}
				}
			}
			development, found := f.WorkspaceMetadata(workspace)
			if !found {
				t.Fatal("development oracle workspace missing")
			}
			expectedDrafts := slices.DeleteFunc(slices.Clone(development.DocumentContentDrafts), func(item domain.DocumentContentDraft) bool {
				return item.DocumentID != prefix+"live"
			})
			statements := 0
			native, err := view.BeginDocumentAuxiliaryRead(WithSQLStatementObserver(t.Context(), func(string) { statements++ }), workspace, "", nil, DocumentAuxiliaryReadRequest{Ref: prefix + "live", Drafts: true})
			if err != nil {
				t.Fatal(err)
			}
			if statements != 0 || !reflect.DeepEqual(native.Data.DocumentContentDrafts, expectedDrafts) || native.Data.ViewerRole != "admin" {
				t.Fatal("development auxiliary source/owner filtering changed", statements)
			}
			native.Close()
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if read, err := view.BeginDocumentAuxiliaryRead(canceled, workspace, f.data.Viewer.ID, nil, DocumentAuxiliaryReadRequest{Ref: prefix + "live", Drafts: true}); read != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("auxiliary cancellation lost", err)
			}
			limit := view.metadataResidency.work.limit
			view.metadataResidency.work.limit = 1
			failed, err := view.BeginDocumentAuxiliaryRead(t.Context(), workspace, f.data.Viewer.ID, nil, DocumentAuxiliaryReadRequest{Ref: prefix + "live", Drafts: true})
			view.metadataResidency.work.limit = limit
			if failed != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("auxiliary admission failure leaked or decoded full metadata", err)
			}
			t.Logf("state=%s literal_pairs=%d authenticated_selection_sql=%d maximum_retained_work=%d development_selection_sql=%d", state, pairs, sqlStatements, maximumWork, statements)
		})
	}
}

func TestScaleP21SW129DocumentAuxiliaryTypedTrashAdmission(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, ref := f.data.Workspace.URLKey, idgen.New("sw129-typed-trash-")
	count := resourceWindowMaxWork/(int(reflect.TypeFor[domain.DocumentRevision]().Size())*2+128) + 1
	id, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"id":` + string(id) + `,"revisions":[` + strings.Repeat("null,", count) + `null]}`)
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "trash"), workspace, "scale_p21.sw129_typed_trash", "", nil, func(data *domain.Bootstrap) error {
		data.Trash = append(data.Trash, domain.TrashEntry{ID: ref + "-trash", ResourceType: "document", ResourceID: ref, Payload: payload, DeletedBy: data.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"warm", "reloaded"} {
		t.Run(state, func(t *testing.T) {
			if state == "reloaded" {
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			read, err := view.BeginDocumentAuxiliaryRead(t.Context(), workspace, "", nil, DocumentAuxiliaryReadRequest{Ref: ref, History: true})
			if read != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("dense typed revision expansion escaped admission or leaked", err)
			}
		})
	}
}

func TestScaleP21SW129DocumentAuxiliaryInstalledLifetimeAndGrowth(t *testing.T) {
	const workspace, ref = "physical", "sw129-lifetime"
	before := domain.Bootstrap{
		Workspace:             domain.Workspace{URLKey: workspace},
		Documents:             []domain.Document{{ID: ref}},
		DocumentContentDrafts: []domain.DocumentContentDraft{{ID: "selected", DocumentID: ref, Content: "before", ContentData: map[string]any{"mutable": []any{"original"}}}},
	}
	view := scaleP21HotLogoStore(t, map[string]domain.Bootstrap{workspace: before})
	parent, found, err := view.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("missing parent generation", err)
	}
	defer parent.Close()
	pinned, err := parent.Context(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	held, err := view.BeginDocumentAuxiliaryRead(t.Context(), workspace, "", nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	baseline := 0
	for _, count := range []int{100, 1000} {
		after := cloneBootstrap(before)
		after.DocumentContentDrafts[0].Content = "after"
		for range count {
			after.DocumentContentDrafts = append(after.DocumentContentDrafts, domain.DocumentContentDraft{DocumentID: "unrelated", Content: strings.Repeat("excluded", 1000), ContentData: map[string]any{"body": strings.Repeat("excluded dynamic ", 1000)}})
		}
		source, err := captureMetadataLogicalSource(t.Context(), after, copyStamp{revision: -1})
		if err != nil {
			t.Fatal(err)
		}
		view.installMetadataLogicalSource(workspace, source)
		previous := view.MetadataWorkUsedForTesting()
		read, err := view.BeginDocumentAuxiliaryRead(pinned, workspace, "", nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true})
		if err != nil {
			t.Fatal(err)
		}
		work := view.MetadataWorkUsedForTesting() - previous
		if len(read.Data.DocumentContentDrafts) != 1 || read.Data.DocumentContentDrafts[0].Content != "after" || held.Data.DocumentContentDrafts[0].Content != "before" {
			t.Fatal("auxiliary source choice inherited an old pin or replaced held output")
		}
		if baseline != 0 && work != baseline {
			t.Fatal("unrelated draft expansion changed retained work", baseline, work)
		}
		baseline = work
		read.Data.DocumentContentDrafts[0].ContentData["mutable"].([]any)[0] = "caller"
		read.Close()
		if view.MetadataWorkUsedForTesting() != previous || !held.document.owner.SourceRetainedForTesting() {
			t.Fatal("independent auxiliary close released another generation")
		}
		t.Logf("unrelated_drafts=%d retained_work=%d development_sql=0", count, work)
	}
	parent.Close()
	held.Close()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("old generation ownership leaked")
	}
	limit := view.metadataResidency.work.limit
	view.metadataResidency.work.limit = baseline - 1
	failed, err := view.BeginDocumentAuxiliaryRead(t.Context(), workspace, "", nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true})
	view.metadataResidency.work.limit = limit
	if failed != nil || !errors.Is(err, ErrMetadataWorkBudget) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("partial auxiliary admission escaped or leaked", err)
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			read, err := view.BeginDocumentAuxiliaryRead(t.Context(), workspace, "", nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true})
			if err != nil {
				t.Error(err)
				return
			}
			if len(read.Data.DocumentContentDrafts) != 1 || read.Data.DocumentContentDrafts[0].ContentData["mutable"].([]any)[0] != "original" {
				t.Error("concurrent auxiliary output shared caller mutations")
			}
			var closers sync.WaitGroup
			for range 2 {
				closers.Go(read.Close)
			}
			closers.Wait()
		})
	}
	workers.Wait()
	if view.MetadataWorkUsedForTesting() != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("concurrent auxiliary output leaked or decoded full metadata")
	}
}

func TestScaleP21SW129DocumentAuxiliaryCallerAndOutputOwner(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace, ref := f.data.Workspace.URLKey, idgen.New("sw129-caller-")
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "documents"), workspace, "scale_p21.sw129_caller", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, domain.Document{ID: ref, Creator: data.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	ctx, release, err := f.BeginWorkspaceRead(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	held := ctx.Value(workspaceReadKey{}).(workspaceRead)
	held.metadata.DocumentContentDrafts = []domain.DocumentContentDraft{{ID: ref + "-draft", DocumentID: ref, UserID: f.data.Viewer.ID, ContentData: map[string]any{"caller": int64(23)}}}
	ctx = context.WithValue(ctx, workspaceReadKey{}, held)
	read, err := view.BeginDocumentAuxiliaryRead(ctx, workspace, f.data.Viewer.ID, nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true})
	if err != nil || len(read.Data.DocumentContentDrafts) != 1 || read.Data.DocumentContentDrafts[0].ContentData["caller"] != int64(23) {
		t.Fatal("auxiliary read ignored its explicit caller snapshot", err)
	}
	var closeOutput func()
	owned := WithMetadataOutputOwner(ctx, func(close func()) bool {
		closeOutput = close
		return true
	})
	if err := read.TransferOutput(owned); err != nil || closeOutput == nil {
		t.Fatal("auxiliary output transfer failed", err)
	}
	if view.MetadataWorkUsedForTesting() == 0 {
		t.Fatal("transferring auxiliary output released its values")
	}
	closeOutput()
	if view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("auxiliary output owner did not release")
	}
	unowned, err := view.BeginDocumentAuxiliaryRead(ctx, workspace, f.data.Viewer.ID, nil, DocumentAuxiliaryReadRequest{Ref: ref, Drafts: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := unowned.TransferOutput(ctx); !errors.Is(err, ErrBoundedMetadataCoverage) || view.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("unowned auxiliary output succeeded or leaked", err)
	}
}
