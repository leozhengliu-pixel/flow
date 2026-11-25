package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestScaleP15UnloadedMutationKeepsStoredValues(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	initial := repo.Bootstrap()
	workspace := initial.Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p15-read", Name: "Read only", Description: "keep read body"}, domain.Project{ID: "p15-write", Name: "Editable", Description: "clear this body"})
		data.Settings["p15"] = "before"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := dumpWorkspaceMetadata(t, repo, workspace)
	scope := MetadataRecordScope{Fields: []string{"settings"}, Read: []RecordKey{{"projects", "p15-read"}}, Write: []RecordKey{{"projects", "p15-write"}, {"projects", "p15-new"}}}
	var published []domain.DomainEvent
	repo.SetWebhookSink(func(_ string, event domain.DomainEvent) { published = append(published, event) })
	ctx := WithMetadataRecords(t.Context(), scope)
	auditID := "p15-audit-" + initial.Workspace.ID
	if err := repo.MutateWorkspace(ctx, workspace, "project.updated", "p15-write", map[string]any{"description": ""}, func(data *domain.Bootstrap) error {
		if len(data.Projects) != 2 || len(data.Documents) != 0 || len(data.Trash) != 0 {
			t.Fatalf("unexpected callback hydration: projects=%d documents=%d trash=%d", len(data.Projects), len(data.Documents), len(data.Trash))
		}
		for i := range data.Projects {
			if data.Projects[i].ID == "p15-read" {
				data.Projects[i].Description = "must be discarded"
			} else {
				data.Projects[i].Description = ""
			}
		}
		data.Projects = append(data.Projects, domain.Project{ID: "p15-new", Name: "Created"}, domain.Project{ID: "outside-write-set", Name: "must be discarded"})
		data.Documents, data.Trash = nil, nil
		data.Settings["p15"] = "after"
		data.AuditLog = append([]domain.AuditLogEntry{{ID: auditID, Actor: data.Viewer, Action: "project.updated", ResourceType: "project", ResourceID: "p15-write", CreatedAt: time.Now().UTC()}}, data.AuditLog...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after := dumpWorkspaceMetadata(t, repo, workspace)
	for key, raw := range before.records {
		if key == "projects/p15-write" || key == "settings/p15" {
			continue
		}
		if got := after.records[key]; got != raw {
			t.Fatalf("unloaded/read-only row changed: %s", key)
		}
		if after.search[key] != before.search[key] {
			t.Fatalf("unloaded search row changed: %s", key)
		}
	}
	if _, ok := after.records["projects/outside-write-set"]; ok {
		t.Fatal("undeclared record was inserted")
	}
	if _, ok := after.records["projects/p15-new"]; !ok {
		t.Fatal("declared missing record was not created")
	}
	current, _ := repo.WorkspaceMetadata(workspace)
	for _, project := range current.Projects {
		if project.ID == "p15-write" && project.Description != "" {
			t.Fatal("loaded empty value did not clear the body")
		}
		if project.ID == "p15-read" && project.Description != "keep read body" {
			t.Fatal("read-only record changed")
		}
	}
	if current.Settings["p15"] != "after" {
		t.Fatal("complete field change was lost")
	}
	var action string
	if err := repo.db.QueryRowContext(t.Context(), `SELECT action FROM audit_events WHERE workspace_id=? AND id=?`, initial.Workspace.ID, auditID).Scan(&action); err != nil || action != "project.updated" {
		t.Fatalf("scoped callback lost its transactional audit: %v", err)
	}
	if len(published) != 1 || published[0].Type != "project.updated" || !bytes.Contains(published[0].Payload, []byte(`"description":""`)) {
		t.Fatalf("webhook hook changed: %+v", published)
	}
	if err := repo.MutateWorkspace(WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"projects", "p15-new"}}}), workspace, "project.updated", "p15-new", nil, func(data *domain.Bootstrap) error {
		data.Projects = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deleted := dumpWorkspaceMetadata(t, repo, workspace)
	if _, ok := deleted.records["projects/p15-new"]; ok {
		t.Fatal("loaded write record was not deleted")
	}
}

func TestScaleP15RecordScopeNilEmptyAndIsolation(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	stored := domain.Bootstrap{Projects: []domain.Project{{ID: "read", MemberIDs: []string{"old"}}, {ID: "write", Description: "body"}}, Settings: map[string]any{"read": []any{"old"}, "write": "old"}, Documents: nil, Trash: []domain.TrashEntry{}}
	scope := MetadataRecordScope{Read: []RecordKey{{"projects", "read"}, {"settings", "read"}}, Write: []RecordKey{{"projects", "write"}, {"settings", "write"}, {"documents", "missing"}}}
	ctx := WithMetadataRecords(t.Context(), scope)
	scope.Read[0].ID = "changed-after-context"
	selected := metadataRecordFieldSnapshot(ctx, stored, []string{"projects", "settings", "documents"})
	selected.Projects[0].MemberIDs[0] = "mutated copy"
	selected.Settings["read"].([]any)[0] = "mutated copy"
	selected.Projects = selected.Projects[:1]
	delete(selected.Settings, "write")
	restoreMetadataRecordFields(ctx, &selected, stored, nil)
	if len(selected.Projects) != 1 || selected.Projects[0].MemberIDs[0] != "old" || selected.Settings["read"].([]any)[0] != "old" {
		t.Fatal("read-only record was not preserved")
	}
	if _, ok := selected.Settings["write"]; ok {
		t.Fatal("loaded map key deletion was lost")
	}
	if selected.Documents != nil || selected.Trash == nil || stored.Projects[0].MemberIDs[0] != "old" {
		t.Fatal("unloaded nil/empty shape or snapshot isolation changed")
	}
}

func TestScaleP15HydratesOnlyDeclaredRecords(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	initial := repo.Bootstrap()
	workspace := initial.Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p15-small", Name: "Small"}, domain.Project{ID: "p15-huge", Description: strings.Repeat("x", 3<<20)})
		data.Settings["p15-empty"] = []any{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scope := MetadataRecordScope{Read: []RecordKey{{"projects", "p15-small"}, {"projects", "missing"}, {"settings", "p15-empty"}}}
	hydrated, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated.Data.Projects) != 1 || hydrated.Data.Projects[0].ID != "p15-small" || hydrated.Data.Settings["p15-empty"] == nil || hydrated.FieldLoaded("projects") || !hydrated.RecordLoaded(RecordKey{"projects", "missing"}) || hydrated.RecordLoaded(RecordKey{"projects", "p15-huge"}) {
		t.Fatalf("completeness changed: %+v", hydrated.Scope)
	}
	allocation := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			if _, err := repo.HydrateWorkspaceMetadata(context.Background(), workspace, scope); err != nil {
				b.Fatal(err)
			}
		}
	})
	if allocation.AllocedBytesPerOp() > 2<<20 {
		t.Fatalf("scoped read exceeded the working page budget: %d B/op", allocation.AllocedBytesPerOp())
	}
	t.Logf("scoped hydration beside a 3 MiB unrelated record: %d B/op", allocation.AllocedBytesPerOp())
	for _, scope := range []MetadataRecordScope{{Read: []RecordKey{{"projects", "p15-huge"}}}, {Fields: []string{"projects"}}} {
		if _, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, scope); !errors.Is(err, ErrMetadataPageBudget) {
			t.Fatalf("huge hydration did not report omission: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repo.HydrateWorkspaceMetadata(ctx, workspace, scope); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled hydration = %v", err)
	}
}

func TestScaleP15FlagOffRestoresFullCallback(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	repo, _ := scaleP04OwnedStore(t)
	data := repo.Bootstrap()
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"projects", "missing"}}})
	if err := repo.MutateWorkspace(ctx, data.Workspace.URLKey, "project.updated", "", nil, func(got *domain.Bootstrap) error {
		if len(got.Projects) != len(data.Projects) || len(got.Documents) != len(data.Documents) {
			t.Fatal("disabled flag did not restore legacy callback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type scaleP15BlobSink struct {
	bytes, max int
	onWrite    func()
}

func (w *scaleP15BlobSink) Write(p []byte) (int, error) {
	w.bytes += len(p)
	w.max = max(w.max, len(p))
	if w.onWrite != nil {
		w.onWrite()
	}
	return len(p), nil
}

func TestScaleP15OffloadedBodyStreamAndFailures(t *testing.T) {
	repo, _ := scaleP04OwnedStore(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	body := []byte(`{"content":"` + strings.Repeat("history <>&", 1<<19) + `"}`)
	compressed := gzipBody(body)
	key := "document-revisions/p15-large"
	if _, err := repo.db.ExecContext(t.Context(), `INSERT INTO metadata_blobs(workspace_key,blob_key,size,data,created_at) VALUES(?,?,?,?,?)`, workspace, key, len(body), compressed, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	ref := domain.ContentRef{Key: key}
	sink := &scaleP15BlobSink{}
	if err := repo.StreamOffloadedBody(t.Context(), workspace, ref, sink); err != nil || sink.bytes != len(body) || sink.max > 32<<10 {
		t.Fatalf("streamed body: bytes=%d chunk=%d error=%v", sink.bytes, sink.max, err)
	}
	if _, cached := repo.db.content.cache.get(workspace + "/" + key); cached {
		t.Fatal("stream filled the whole-body cache")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := repo.StreamOffloadedBody(ctx, workspace, ref, &scaleP15BlobSink{onWrite: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream = %v", err)
	}
	if _, err := repo.db.ExecContext(t.Context(), `UPDATE metadata_blobs SET data=NULL WHERE workspace_key=? AND blob_key=?`, workspace, key); err != nil {
		t.Fatal(err)
	}
	objects := &memoryObjects{objects: map[string][]byte{offloadedObjectKey(workspace, key): compressed}}
	repo.db.content.objects = objects
	var output bytes.Buffer
	if err := repo.StreamOffloadedBody(t.Context(), workspace, ref, &output); err != nil || !bytes.Equal(output.Bytes(), body) {
		t.Fatalf("object stream differs: %v", err)
	}
	objects.objects[offloadedObjectKey(workspace, key)] = []byte("corrupt gzip")
	if err := repo.StreamOffloadedBody(t.Context(), workspace, ref, io.Discard); err == nil {
		t.Fatal("corrupt object became an empty body")
	}
	delete(objects.objects, offloadedObjectKey(workspace, key))
	if err := repo.StreamOffloadedBody(t.Context(), workspace, ref, io.Discard); err == nil {
		t.Fatal("missing object became an empty body")
	}
	if err := repo.StreamOffloadedBody(t.Context(), workspace, domain.ContentRef{Key: "missing"}, io.Discard); err == nil {
		t.Fatal("missing SQL body became an empty body")
	}
}

func TestScaleP15RejectsUnknownScopes(t *testing.T) {
	for _, scope := range []MetadataRecordScope{{Fields: []string{"unknown"}}, {Write: []RecordKey{{"issues", "issue"}}}, {Read: []RecordKey{{"projectUpdates", "split-key"}}}, {Write: []RecordKey{{"workspaceSettings", "key"}}}, {Read: []RecordKey{{"teamMembers", "compound-key"}}}} {
		if err := validateMetadataRecordScope(scope); err == nil {
			t.Fatalf("invalid scope accepted: %+v", scope)
		}
	}
	if err := validateMetadataRecordSelection(domain.Bootstrap{Projects: []domain.Project{{ID: "duplicate"}, {ID: "duplicate"}}}, MetadataRecordScope{Write: []RecordKey{{"projects", "duplicate"}}}); err == nil {
		t.Fatal("ambiguous legacy record identities were accepted")
	}
}

func TestScaleP15ScopePreservesRecordOrder(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	data := domain.Bootstrap{}
	for i := range 100 {
		data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprint(i)})
	}
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"projects", "1"}, {"projects", "90"}, {"projects", "new"}}})
	selected := metadataRecordFieldSnapshot(ctx, data, []string{"projects"})
	slices.Reverse(selected.Projects)
	selected.Projects = append(selected.Projects, domain.Project{ID: "new"})
	restoreMetadataRecordFields(ctx, &selected, data, nil)
	if !reflect.DeepEqual(selected.Projects[:100], data.Projects) || selected.Projects[100].ID != "new" {
		t.Fatal("partial writes reordered unrelated records")
	}
}

func TestScaleP15OldAndScopedWritersKeepBothChanges(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	repo, config := scaleP04OwnedStore(t)
	initial := repo.Bootstrap()
	workspace, projectID := initial.Workspace.URLKey, initial.Projects[0].ID
	peer, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	if err := peer.MutateWorkspace(WithMetadataFields(t.Context(), "projects"), workspace, "project.updated", projectID, nil, func(data *domain.Bootstrap) error {
		for i := range data.Projects {
			if data.Projects[i].ID == projectID {
				data.Projects[i].Description = "Written by the legacy replica"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"projects", projectID}}})
	if err := repo.MutateWorkspace(ctx, workspace, "project.updated", projectID, nil, func(data *domain.Bootstrap) error {
		if len(data.Projects) != 1 {
			t.Fatal("callback did not receive a single record")
		}
		data.Projects[0].Name = "Written by the scoped replica"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hydrated, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{"projects", projectID}}})
	if err != nil || len(hydrated.Data.Projects) != 1 || hydrated.Data.Projects[0].Name != "Written by the scoped replica" || hydrated.Data.Projects[0].Description != "Written by the legacy replica" {
		t.Fatalf("interleaved writes lost a stored field: %v", err)
	}
}

func TestScaleP15CanonicalLargeRecordStream(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	repo, _ := scaleP04OwnedStore(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "p15-stream", Description: strings.Repeat("x", 3<<20)})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	key := RecordKey{"projects", "p15-stream"}
	var stored []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, key.Kind, key.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := repo.StreamCanonicalMetadataRecord(t.Context(), workspace, key, &output); err != nil || !bytes.Equal(output.Bytes(), stored) {
		t.Fatalf("canonical record bytes changed: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := &scaleP15BlobSink{onWrite: func() {
		if repo.db.Stats().InUse != 0 {
			t.Fatal("canonical record stream pinned SQL while writing")
		}
		cancel()
	}}
	if err := repo.StreamCanonicalMetadataRecord(ctx, workspace, key, sink); !errors.Is(err, context.Canceled) || sink.max > 32<<10 {
		t.Fatalf("canonical stream cancellation: chunk=%d error=%v", sink.max, err)
	}
}

func TestScaleP15RecordMutationPreservesUnloadedRevisionBodies(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "off")
	repo, _ := scaleP04OwnedStore(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	document := offloadTestDocument(time.Now().UTC())
	if err := repo.MutateWorkspace(t.Context(), workspace, "document.created", document.ID, nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, document)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"documents", document.ID}}})
	if err := repo.MutateWorkspace(ctx, workspace, "document.updated", document.ID, nil, func(data *domain.Bootstrap) error {
		if len(data.Documents) != 1 {
			t.Fatal("document scope was not narrow")
		}
		data.Documents[0].Title = "Renamed while history is unloaded"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hydrated, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{{"documents", document.ID}}})
	if err != nil || len(hydrated.Data.Documents) != 1 {
		t.Fatalf("document hydration: %v", err)
	}
	got := hydrated.Data.Documents[0]
	if got.Title != "Renamed while history is unloaded" || len(got.Revisions) != len(document.Revisions) {
		t.Fatal("record mutation lost document metadata")
	}
	if err := repo.LoadDocumentRevisionBodies(t.Context(), workspace, got.Revisions); err != nil {
		t.Fatal(err)
	}
	for i := range got.Revisions {
		if got.Revisions[i].Content != document.Revisions[i].Content || got.Revisions[i].ContentState != document.Revisions[i].ContentState {
			t.Fatalf("unloaded revision %s was cleared", got.Revisions[i].ID)
		}
	}
}

func TestScaleP15HydrationDefaultIsOff(t *testing.T) {
	for _, value := range []string{"", "off", "0", "false", "unknown", "shadow"} {
		t.Setenv("FLOW_RECORD_HYDRATION", value)
		if RecordHydrationEnabled() {
			t.Fatalf("hydration unexpectedly enabled for %q", value)
		}
	}
}

func TestScaleP15ScopeFailureIsAtomic(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	data := repo.Bootstrap()
	workspace, projectID := data.Workspace.URLKey, data.Projects[0].ID
	before := dumpWorkspaceMetadata(t, repo, workspace)
	published := 0
	repo.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Write: []RecordKey{{"projects", projectID}}})
	want := errors.New("cancel scoped callback")
	err := repo.MutateWorkspace(ctx, workspace, "project.updated", projectID, nil, func(current *domain.Bootstrap) error {
		current.Projects[0].Name = "must roll back"
		current.AuditLog = append(current.AuditLog, domain.AuditLogEntry{ID: "p15-failed-" + data.Workspace.ID, Action: "project.updated", ResourceType: "project", ResourceID: projectID, Actor: data.Viewer, CreatedAt: time.Now().UTC()})
		return want
	})
	if !errors.Is(err, want) || published != 0 || !reflect.DeepEqual(before, dumpWorkspaceMetadata(t, repo, workspace)) {
		t.Fatalf("failed scoped mutation committed state or side effects: %v", err)
	}
	var audits int
	if err := repo.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=? AND id=?`, data.Workspace.ID, "p15-failed-"+data.Workspace.ID).Scan(&audits); err != nil || audits != 0 {
		t.Fatalf("failed scope committed its staged audit: %v", err)
	}
}

func TestScaleP15PointCallbacksRejectUnavailableRequirements(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo := &SQLiteStore{}
	ctx := WithMetadataRecords(t.Context(), MetadataRecordScope{Read: []RecordKey{{"projects", "required"}}})
	for _, event := range []string{"loop.output_contract", "workspace_preferences.updated"} {
		called := false
		err := repo.MutateWorkspace(ctx, "workspace", event, "", map[string]any{"featureFlags": map[string]bool{"documents": true}}, func(data *domain.Bootstrap) error {
			called = true
			return nil
		})
		if !errors.Is(err, ErrMetadataRecordQuery) || called {
			t.Fatalf("point writer accepted unavailable callback requirements: event=%s error=%v", event, err)
		}
	}
}

func TestScaleP15DecodedContainersAreBudgeted(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, _ := scaleP04OwnedStore(t)
	workspace := repo.Bootstrap().Workspace.URLKey
	items := make([]map[string]any, 6000)
	for i := range items {
		items[i] = map[string]any{}
	}
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p15.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Settings["p15-containers"] = items
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	key := RecordKey{"settings", "p15-containers"}
	if _, err := repo.HydrateWorkspaceMetadata(t.Context(), workspace, MetadataRecordScope{Read: []RecordKey{key}}); !errors.Is(err, ErrMetadataPageBudget) {
		t.Fatalf("small encoded JSON bypassed the decoded working budget: %v", err)
	}
	if repo.db.Stats().InUse != 0 {
		t.Fatal("rejected hydration retained its SQL snapshot")
	}
	want, _ := json.Marshal(items)
	var got bytes.Buffer
	if err := repo.StreamCanonicalMetadataRecord(t.Context(), workspace, key, &got); err != nil || !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("budget rejection lost the complete streaming alternative: %v", err)
	}
	budget := newMetadataHydrationBudget()
	quoted, _ := json.Marshal(strings.Repeat(`{}[],:\"`, 1000))
	if err := budget.consume(quoted); err != nil {
		t.Fatal("string punctuation was charged as decoded containers")
	}
	root := map[string]int{}
	for i := range 300 {
		root[fmt.Sprint(i)] = i
	}
	raw, _ := json.Marshal(root)
	if metadataRootFieldsBounded(raw) {
		t.Fatal("unbounded root field count was admitted")
	}
}
