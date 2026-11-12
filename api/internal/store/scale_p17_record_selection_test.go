package store

import (
	"encoding/json"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
)

func TestScaleP17SelectedWritesPreserveOmittedRecordsAndOrder(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORD_VERIFY", "off")
	repo, _ := scaleP04OwnedStore(t)
	initial := repo.Bootstrap()
	key := initial.Workspace.URLKey
	ctx := ContextWithActor(t.Context(), initial.Viewer)
	if err := repo.MutateWorkspace(ctx, key, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{{ID: "p17-other", Content: strings.Repeat("other body", 8192), ContentData: map[string]any{"nested": []any{"preserved"}}}, {ID: "p17-target", Content: "target", Creator: data.Viewer}, {ID: "p17-last", Content: "last"}}
		data.Trash = []domain.TrashEntry{{ID: "p17-previous", ResourceType: "document", Payload: json.RawMessage(`{"id":"p17-deleted","content":"preserved trash"}`)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.WorkspaceMetadata(key)
	other, _ := json.Marshal(stored.Documents[0])
	selection := func(domain.Bootstrap, string, any) (MetadataRecordScope, bool) {
		return MetadataRecordScope{Fields: []string{"users"}, Write: []RecordKey{{Kind: "documents", ID: "p17-target"}}}, true
	}
	selected := WithDocumentRecordSelection(WithMutationScope(ctx, MutationScope{Resources: []string{"p17-target"}}), "on", selection)
	if err := repo.MutateWorkspace(selected, key, "document.deleted", "p17-target", nil, func(data *domain.Bootstrap) error {
		if len(data.Documents) != 1 || data.Documents[0].ID != "p17-target" || len(data.Trash) != 0 {
			t.Fatal("callback hydrated unrelated document/trash payloads")
		}
		raw, _ := json.Marshal(data.Documents[0])
		data.Trash = append([]domain.TrashEntry{{ID: "p17-new", ResourceType: "document", ResourceID: "p17-target", Payload: raw}}, data.Trash...)
		data.Documents = data.Documents[:0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.WorkspaceMetadata(key)
	current, _ := json.Marshal(after.Documents[0])
	if string(other) != string(current) || len(after.Documents) != 2 || len(after.Trash) != 2 || after.Trash[0].ID != "p17-new" || after.Trash[1].ID != "p17-previous" {
		t.Fatal("deletion changed an omitted record or collection order")
	}
	selected = WithDocumentRecordSelection(WithMutationScope(ctx, MutationScope{}), "on", func(domain.Bootstrap, string, any) (MetadataRecordScope, bool) {
		return MetadataRecordScope{Write: []RecordKey{{Kind: "documents", ID: "p17-target"}, {Kind: "trash", ID: "p17-new"}}}, true
	})
	if err := repo.MutateWorkspace(selected, key, "trash.restored", "p17-new", nil, func(data *domain.Bootstrap) error {
		if len(data.Documents) != 0 || len(data.Trash) != 1 || data.Trash[0].ID != "p17-new" {
			t.Fatal("restore read unrelated payloads")
		}
		var document domain.Document
		if err := json.Unmarshal(data.Trash[0].Payload, &document); err != nil {
			return err
		}
		data.Documents = append([]domain.Document{document}, data.Documents...)
		data.Trash = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ = repo.WorkspaceMetadata(key)
	if after.Documents[0].ID != "p17-target" || after.Documents[1].ID != "p17-other" || len(after.Trash) != 1 || after.Trash[0].ID != "p17-previous" {
		t.Fatal("restore lost a record or the legacy prepend order")
	}
}

func TestScaleP17DirectoryBodyGrowthDoesNotIncreaseAllocations(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	initial := repo.Bootstrap()
	key := initial.Workspace.URLKey
	measure := func(size int) uint64 {
		if err := repo.MutateWorkspace(t.Context(), key, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
			data.Documents = []domain.Document{{ID: "p17-directory", Creator: data.Viewer, Content: strings.Repeat("x", size), ContentState: strings.Repeat("y", size), ContentData: map[string]any{"body": strings.Repeat("z", size)}, Revisions: []domain.DocumentRevision{{ID: "history", Content: strings.Repeat("h", size)}}}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		read := func() {
			directory, err := repo.DocumentDirectoryMetadata(t.Context(), key, "", nil, []string{"teams", "teamSettings"})
			if err != nil || len(directory.Data.Documents) != 1 {
				t.Fatalf("directory: %v", err)
			}
			header := directory.Data.Documents[0]
			if header.Content != "" || header.ContentState != "" || header.ContentData != nil || header.Revisions != nil {
				t.Fatal("directory fetched a body/history")
			}
		}
		read()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 10 {
			read()
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / 10
	}
	small, large := measure(16<<10), measure(160<<10)
	t.Logf("directory bytes/op: body1x=%d body10x=%d", small, large)
	if large > small+(64<<10) {
		t.Fatal("directory allocations grew with body/history size")
	}
	directory, err := repo.DocumentDirectoryMetadata(t.Context(), key, "", nil, []string{"teams", "teamSettings"})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok := directory.Load("p17-directory")
	if !ok || len(detail.Content) != 160<<10 || len(detail.Revisions) != 1 || len(detail.Revisions[0].Content) != 160<<10 {
		t.Fatal("legacy detail lost a legal large body/history")
	}
	if _, ok := directory.Load("missing"); ok {
		t.Fatal("directory loaded an undeclared record")
	}
}

func TestScaleP17DifferentialOracleChecksSideEffects(t *testing.T) {
	before := domain.Bootstrap{Documents: []domain.Document{{ID: "target"}, {ID: "hidden"}}, Subscriptions: []domain.Subscription{{ID: "subscription", ResourceID: "target"}}}
	full := cloneCallbackInput(before)
	narrow := cloneCallbackInput(before)
	narrow.Documents = slices.Clone(narrow.Documents[:1])
	callback := func(data *domain.Bootstrap) (string, error) {
		data.Subscriptions[0].Events = []string{strings.Repeat("x", len(data.Documents))}
		return "target", nil
	}
	a, _, _ := runVerifiedCallback(&narrow, nil, nil, nil, callback)
	b, _, _ := runVerifiedCallback(&full, nil, nil, nil, callback)
	a.Data.Documents = b.Data.Documents
	protected, _ := json.Marshal(before)
	if path := compareMutationCallbacks(a, b, protected); !strings.Contains(path, "subscriptions") {
		t.Fatalf("oracle missed an association side effect: %s", path)
	}
	a.Data.Subscriptions = b.Data.Subscriptions
	if path := compareMutationCallbacks(a, b, protected); path != "" || !reflect.DeepEqual(a.Data, b.Data) {
		t.Fatalf("oracle rejected equivalent data: %s", path)
	}
}

func TestScaleP17StaleReferencesAndAmbiguousRecordsKeepCanonicalInput(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	user := domain.User{ID: "owner", Name: "Current"}
	stale := user
	stale.Name = "Previous"
	for _, data := range []domain.Bootstrap{
		{Users: []domain.User{user}, Documents: []domain.Document{{ID: "target"}}, Projects: []domain.Project{{ID: "unrelated", Creator: &stale}}},
		{Users: []domain.User{user}, Documents: []domain.Document{{ID: "target"}, {ID: "target"}}},
	} {
		ctx := WithDocumentRecordSelection(t.Context(), "on", func(domain.Bootstrap, string, any) (MetadataRecordScope, bool) {
			return MetadataRecordScope{Write: []RecordKey{{Kind: "documents", ID: "target"}}}, true
		})
		_, _, selected := prepareDocumentRecordMutation(ctx, data, "document.updated", nil, func(*domain.Bootstrap) (string, error) { return "target", nil })
		if selected {
			t.Fatal("legacy repair/ambiguous records were given an incomplete callback")
		}
	}
}

func TestScaleP17CanonicalReadDoesNotDecodeOtherPayloads(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	initial := repo.Bootstrap()
	key := initial.Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), key, "p17.fixture", "", nil, func(data *domain.Bootstrap) error {
		data.Documents = []domain.Document{{ID: "target", Content: "complete target", Creator: data.Viewer}, {ID: "unrelated", Content: "other document", Creator: data.Viewer}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='documents' AND record_key='unrelated'`, []byte(`{"id":"unrelated","content":{"invalid":"document body"}}`), key); err != nil {
		t.Fatal(err)
	}
	data, err := repo.SelectedDocumentMetadata(t.Context(), key, "", nil, func(domain.Bootstrap) (MetadataRecordScope, bool) {
		return MetadataRecordScope{Read: []RecordKey{{Kind: "documents", ID: "target"}}}, true
	})
	if err != nil || len(data.Documents) != 1 || data.Documents[0].Content != "complete target" {
		t.Fatalf("target canonical read decoded an unrelated payload: %v", err)
	}
}

func TestScaleP17OracleKeepsCanonicalActorRefreshOrdering(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_DOCUMENT_RECORD_VERIFY", "on")
	storedUser := domain.User{ID: "owner", Name: "Stored actor"}
	requestUser := storedUser
	requestUser.Name = "Request actor"
	stored := domain.Bootstrap{Viewer: storedUser, Users: []domain.User{storedUser}, Projects: []domain.Project{{ID: "unrelated", Creator: &storedUser}}, Documents: []domain.Document{{ID: "target", Title: "Before"}}}
	scope := MetadataRecordScope{Fields: []string{"users"}, Write: []RecordKey{{Kind: "documents", ID: "target"}}}
	ctx := WithDocumentRecordSelection(t.Context(), "on", func(domain.Bootstrap, string, any) (MetadataRecordScope, bool) { return scope, true })
	ctx, callback, selected := prepareDocumentRecordMutation(ctx, stored, "document.updated", nil, func(data *domain.Bootstrap) (string, error) {
		data.Documents[0].Title = "After"
		return "target", nil
	})
	if !selected {
		t.Fatal("actor ordering fixture did not exercise the record oracle")
	}
	current := metadataRecordFieldSnapshot(ctx, stored, metadataRecordFields(scope))
	current.Viewer, current.Users[0] = requestUser, requestUser
	if _, err := callback(&current); err != nil {
		t.Fatal(err)
	}
	if current.Projects[0].Creator.Name != storedUser.Name || current.Users[0].Name != requestUser.Name || current.Documents[0].Title != "After" {
		t.Fatal("record oracle changed the canonical actor/reference refresh order")
	}
}
