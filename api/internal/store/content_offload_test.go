package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type memoryObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (m *memoryObjects) Put(_ context.Context, key string, reader io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return int64(len(data)), nil
}

func (m *memoryObjects) Open(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, "", 0, errors.New("NoSuchKey: not found")
	}
	return io.NopCloser(bytes.NewReader(data)), "application/gzip", int64(len(data)), nil
}

func (m *memoryObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

func offloadTestDocument(now time.Time) domain.Document {
	long := func(word string) string { return strings.Repeat(word+" ", 400) }
	content := long("current")
	data := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": content}}}}}
	author := domain.User{ID: "usr_admin", Name: "Admin"}
	started := now.Add(-time.Hour)
	revisions := []domain.DocumentRevision{
		{ID: "revision_3", DocumentID: "document_offload", Title: "Plan", Content: content, ContentData: data, Author: author, CreatedAt: now, StartedAt: &started},
		{ID: "revision_2", DocumentID: "document_offload", Title: "Plan", Content: long("second"), ContentState: strings.Repeat("QUJD", 300), Author: author, CreatedAt: now.Add(-2 * time.Hour), StartedAt: &started},
		{ID: "revision_1", DocumentID: "document_offload", Title: "Plan", Content: long("first"), ContentData: map[string]any{"type": "doc"}, Author: author, CreatedAt: now.Add(-3 * time.Hour), StartedAt: &started},
		{ID: "revision_0", DocumentID: "document_offload", Title: "Plan", Content: "short", Author: author, CreatedAt: now.Add(-4 * time.Hour)},
	}
	return domain.Document{ID: "document_offload", SlugID: "plan-offload", Title: "Plan", Content: content, ContentData: data, Creator: author, TeamIDs: []string{}, ProjectIDs: []string{}, SubscriberIDs: []string{}, CreatedAt: now, UpdatedAt: now, Revisions: revisions, Permissions: []domain.DocumentPermission{{ID: "document_permission_usr_admin", DocumentID: "document_offload", SubjectType: "user", SubjectID: "usr_admin", Role: "owner", CreatedAt: now, UpdatedAt: now}}}
}

func storedMetadataRow(t *testing.T, s *SQLiteStore, field, key string) string {
	t.Helper()
	var raw []byte
	if err := s.db.QueryRowContext(context.Background(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, "test-workspace", field, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func countBlobs(t *testing.T, s *SQLiteStore) int {
	t.Helper()
	var count int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM metadata_blobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func documentJSON(t *testing.T, s *SQLiteStore, id string) string {
	t.Helper()
	data, ok := s.WorkspaceMetadataFields("test-workspace", "documents")
	if !ok {
		t.Fatal("workspace not loaded")
	}
	for _, document := range data.Documents {
		if document.ID == id {
			raw, _ := json.Marshal(document)
			return string(raw)
		}
	}
	t.Fatalf("document %s not loaded", id)
	return ""
}

func TestDocumentRevisionBodiesAreStoredOutsideTheRecord(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	s, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	document := offloadTestDocument(time.Now().UTC().Truncate(time.Second))
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.created", document.ID, nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, document)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := storedMetadataRow(t, s, "documents", document.ID)
	// The newest revision refers to the document; the long older ones are
	// offloaded; the short one stays inline. The document itself is untouched.
	if strings.Contains(row, "second second") || strings.Contains(row, "first first") || strings.Contains(row, "QUJDQUJD") {
		t.Fatalf("revision bodies stayed in the row: %.300s", row)
	}
	if !strings.Contains(row, `"body":"document"`) || !strings.Contains(row, `"content":"short"`) || strings.Count(row, `"current current`) != 2 {
		t.Fatalf("unexpected stored document: %s", strings.ReplaceAll(row, "current ", ""))
	}
	if got := countBlobs(t, s); got != 2 {
		t.Fatalf("stored bodies = %d, want 2", got)
	}
	// Rewriting the document stores no new bodies.
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.updated", document.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == document.ID {
				data.Documents[index].Title = "Plan v2"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := countBlobs(t, s); got != 2 {
		t.Fatalf("stored bodies after an edit = %d, want 2", got)
	}
	want := loadedDocumentJSON(t, s, document.ID)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	// Loading reads no bodies: the older versions keep their references.
	if database, objects := reopened.OffloadedContentReads(); database != 0 || objects != 0 {
		t.Fatalf("loading read %d+%d bodies", database, objects)
	}
	loaded := loadedDocument(t, reopened, document.ID, false)
	// The loaded value is stored exactly as the row is, so unchanged
	// documents are not rewritten.
	encoded, _ := json.Marshal(loaded)
	if form, blobs, err := reopened.db.content.storedForm("documents", encoded); err != nil || len(blobs) != 0 || string(form) != storedMetadataRow(t, reopened, "documents", document.ID) {
		t.Fatalf("stored form of the loaded document differs from its row (%d bodies, %v)", len(blobs), err)
	}
	if loaded.Revisions[0].Content != document.Content || loaded.Revisions[1].Content != "" || loaded.Revisions[1].Offloaded["body"].Key == "" || loaded.Revisions[3].Content != "short" {
		t.Fatalf("unexpected loaded versions: %+v", loaded.Revisions[1])
	}
	// Writing the document while its versions are not loaded keeps them.
	if err := reopened.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.updated", document.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == document.ID {
				data.Documents[index].Title = "Plan v3"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want = strings.Replace(want, `"title":"Plan v2"`, `"title":"Plan v3"`, 1)
	if got := loadedDocumentJSON(t, reopened, document.ID); got != want {
		t.Fatalf("loaded document differs\n got %.400s\nwant %.400s", got, want)
	}
	if database, _ := reopened.OffloadedContentReads(); database != 2 {
		t.Fatalf("loading the versions read %d bodies, want 2", database)
	}
	// A second load is served from the cache.
	loadedDocumentJSON(t, reopened, document.ID)
	if database, _ := reopened.OffloadedContentReads(); database != 2 {
		t.Fatalf("a cached load read %d bodies", database-2)
	}
}

func loadedDocument(t *testing.T, s *SQLiteStore, id string, bodies bool) domain.Document {
	t.Helper()
	data, _ := s.WorkspaceMetadataFields("test-workspace", "documents")
	for _, document := range data.Documents {
		if document.ID == id {
			document.Revisions = slices.Clone(document.Revisions)
			if bodies {
				if err := s.LoadDocumentRevisionBodies(context.Background(), "test-workspace", document.Revisions); err != nil {
					t.Fatal(err)
				}
			}
			return document
		}
	}
	t.Fatalf("document %s not loaded", id)
	return domain.Document{}
}

func loadedDocumentJSON(t *testing.T, s *SQLiteStore, id string) string {
	t.Helper()
	raw, _ := json.Marshal(loadedDocument(t, s, id, true))
	return string(raw)
}

func TestDeletedDocumentsInTheTrashShareRevisionBodies(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	s, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	document := offloadTestDocument(now)
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.created", document.ID, nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, document)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := loadedDocumentJSON(t, s, document.ID)
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.deleted", document.ID, nil, func(data *domain.Bootstrap) error {
		payload, _ := json.Marshal(data.Documents[len(data.Documents)-1])
		data.Documents = data.Documents[:len(data.Documents)-1]
		data.Trash = append([]domain.TrashEntry{{ID: "trash_offload", ResourceType: "document", ResourceID: document.ID, Title: document.Title, Payload: payload, DeletedAt: now, ExpiresAt: now.AddDate(0, 0, 30)}}, data.Trash...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := storedMetadataRow(t, s, "trash", "trash_offload")
	if strings.Contains(row, "second second") || !strings.Contains(row, offloadMarker) {
		t.Fatalf("trashed document kept its revision bodies: %.300s", row)
	}
	if got := countBlobs(t, s); got != 2 {
		t.Fatalf("stored bodies = %d, want the document's 2", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	trash, _ := reopened.WorkspaceMetadataFields("test-workspace", "trash")
	var restored domain.Document
	if err := json.Unmarshal(trash.Trash[0].Payload, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Revisions[1].Content != "" || restored.Revisions[1].Offloaded["body"].Key == "" {
		t.Fatal("the trashed document's older versions were loaded")
	}
	// Restoring the document keeps the references; the bodies load on demand.
	if err := reopened.LoadDocumentRevisionBodies(ctx, "test-workspace", restored.Revisions); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(restored)
	if string(got) != want {
		t.Fatal("restorable document differs from the document before deletion")
	}
}

func TestOffloadMigrationRewritesLegacyRecordsInBatches(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	s, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	documents := []domain.Document{}
	for _, id := range []string{"document_a", "document_b", "document_c"} {
		document := offloadTestDocument(time.Now().UTC().Truncate(time.Second))
		document.ID = id
		document.SlugID = id
		document.Permissions[0].DocumentID = id
		documents = append(documents, document)
	}
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.created", "documents", nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, documents...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	// Put the rows back the way earlier versions stored them.
	for _, document := range documents {
		want[document.ID] = documentJSON(t, s, document.ID)
		if _, err := s.db.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field='documents' AND record_key=?`, []byte(want[document.ID]), "test-workspace", document.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM metadata_blobs`); err != nil {
		t.Fatal(err)
	}
	total, calls := 0, 0
	for done := false; !done; calls++ {
		var count int
		count, done, err = s.MigrateOffloadedContent(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		total += count
		if calls > 20 {
			t.Fatal("migration did not finish")
		}
	}
	if total != 3 {
		t.Fatalf("migrated %d records, want 3", total)
	}
	for _, document := range documents {
		if row := storedMetadataRow(t, s, "documents", document.ID); strings.Contains(row, "second second") {
			t.Fatalf("legacy row %s was not rewritten", document.ID)
		}
	}
	// Finished collections are not scanned again.
	if count, done, err := s.MigrateOffloadedContent(ctx, 2); err != nil || !done || count != 0 {
		t.Fatalf("second run = %d %v %v", count, done, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteTestFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, document := range documents {
		if got := loadedDocumentJSON(t, reopened, document.ID); got != want[document.ID] {
			t.Fatalf("migrated document %s differs after reload", document.ID)
		}
	}
}

func TestSweepDeletesBodiesNoRecordRefersTo(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	document := offloadTestDocument(time.Now().UTC().Truncate(time.Second))
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.created", document.ID, nil, func(data *domain.Bootstrap) error {
		data.Documents = append(data.Documents, document)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Drop the oldest long revision (as the 100-version cap does).
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.updated", document.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Documents {
			if data.Documents[index].ID == document.ID {
				data.Documents[index].Revisions = data.Documents[index].Revisions[:2]
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	if deleted, err := s.SweepOffloadedContent(ctx, now, 24*time.Hour); err != nil || deleted != 0 {
		t.Fatalf("first sweep deleted %d (%v); it only marks", deleted, err)
	}
	if deleted, err := s.SweepOffloadedContent(ctx, now.Add(23*time.Hour), 24*time.Hour); err != nil || deleted != 0 {
		t.Fatalf("sweep within the grace period deleted %d (%v)", deleted, err)
	}
	if deleted, err := s.SweepOffloadedContent(ctx, now.Add(25*time.Hour), 24*time.Hour); err != nil || deleted != 1 {
		t.Fatalf("sweep after the grace period deleted %d (%v), want 1", deleted, err)
	}
	if got := countBlobs(t, s); got != 1 {
		t.Fatalf("bodies left = %d, want the referenced one", got)
	}
}

func TestSweepKeepsBodiesReferencedAgain(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	document := offloadTestDocument(time.Now().UTC().Truncate(time.Second))
	add := func() {
		if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.created", document.ID, nil, func(data *domain.Bootstrap) error {
			data.Documents = append(data.Documents, document)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	add()
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "document.deleted", document.ID, nil, func(data *domain.Bootstrap) error {
		data.Documents = data.Documents[:len(data.Documents)-1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	if _, err := s.SweepOffloadedContent(ctx, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	// The same history comes back before the marked bodies are deleted.
	add()
	if deleted, err := s.SweepOffloadedContent(ctx, now.Add(2*time.Hour), time.Hour); err != nil || deleted != 0 {
		t.Fatalf("sweep deleted %d re-used bodies (%v)", deleted, err)
	}
	var marked int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metadata_blobs WHERE orphaned_at<>0`).Scan(&marked); err != nil || marked != 0 {
		t.Fatalf("re-used bodies still marked: %d (%v)", marked, err)
	}
}

func TestAgentTranscriptsOffloadLargeParts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "flow.db")
	objects := &memoryObjects{objects: map[string][]byte{}}
	config := DatabaseConfig{Driver: "sqlite", Path: path, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1, ContentStore: objects, AgentOffloadBytes: 4096}
	s, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	result, _ := json.Marshal(map[string]any{"items": []string{strings.Repeat("issue body ", 1000)}})
	reply := strings.Repeat("A long answer. ", 600)
	session := domain.AgentSession{ID: "agent_session_offload", SlugID: "chat", UserID: "usr_admin", Title: "Chat", Location: "agent", IssueIDs: []string{}, SkillIDs: []string{}, CreatedAt: now, UpdatedAt: now, Messages: []domain.AgentMessage{
		{ID: "message_user", Role: "user", Content: "Find the sync issues", CreatedAt: now},
		{ID: "message_reply", Role: "assistant", Content: reply, CreatedAt: now, Parts: []domain.AgentMessagePart{
			{ID: "part_tool", Type: "tool", ToolCall: &domain.AgentToolCall{ID: "call_1", Name: "search_issues", Arguments: json.RawMessage(`{"query":"sync"}`), Result: result, Status: "done"}},
			{ID: "part_text", Type: "text", Text: reply},
		}},
	}}
	if err := s.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "agent.session_created", session.ID, nil, func(data *domain.Bootstrap) error {
		data.AgentSessions = append(data.AgentSessions, session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	row := storedMetadataRow(t, s, "agentSessions", session.ID)
	if strings.Contains(row, strings.Repeat("issue body ", 100)) || strings.Contains(row, strings.Repeat("A long answer. ", 20)) {
		t.Fatalf("large transcript parts stayed in the row: %.300s", row)
	}
	if !strings.Contains(row, `"summary":"A long answer.`) || !strings.Contains(row, `Find the sync issues`) || !strings.Contains(row, `{\"query\":\"sync\"}`) && !strings.Contains(row, `{"query":"sync"}`) {
		t.Fatalf("summaries or short parts missing: %.600s", row)
	}
	// The reply text and message content are the same body.
	var stored int
	var inline int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),SUM(CASE WHEN data IS NULL THEN 0 ELSE 1 END) FROM metadata_blobs`).Scan(&stored, &inline); err != nil {
		t.Fatal(err)
	}
	if stored != 2 || inline != 0 || len(objects.objects) != 2 {
		t.Fatalf("bodies = %d (inline %d), objects = %d; want 2 in object storage", stored, inline, len(objects.objects))
	}
	loaded, _ := s.WorkspaceMetadataFields("test-workspace", "agentSessions")
	original, err := s.LoadAgentSessionBodies(ctx, "test-workspace", loaded.AgentSessions[len(loaded.AgentSessions)-1])
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(original)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, objectReads := reopened.OffloadedContentReads(); objectReads != 0 {
		t.Fatalf("loading read %d objects", objectReads)
	}
	loaded, _ = reopened.WorkspaceMetadataFields("test-workspace", "agentSessions")
	lazy := loaded.AgentSessions[len(loaded.AgentSessions)-1]
	encodedLazy, _ := json.Marshal(lazy)
	if form, blobs, err := reopened.db.content.storedForm("agentSessions", encodedLazy); err != nil || len(blobs) != 0 || string(form) != storedMetadataRow(t, reopened, "agentSessions", session.ID) {
		t.Fatalf("stored form of the loaded session differs from its row (%d bodies, %v)\n%s\n%s", len(blobs), err, form, storedMetadataRow(t, reopened, "agentSessions", session.ID))
	}
	if !AgentSessionHasOffloaded(lazy) || lazy.Messages[1].Content != "" || lazy.Messages[1].Parts[0].ToolCall.Result != nil || lazy.Messages[0].Content != "Find the sync issues" {
		t.Fatal("the transcript was loaded with the workspace")
	}
	// An edit while the transcript is not loaded keeps it.
	if err := reopened.MutateWorkspace(WithColdWork(ctx, "workspace fixture"), "test-workspace", "agent.session_read", session.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.AgentSessions {
			if data.AgentSessions[index].ID == session.ID {
				data.AgentSessions[index].Title = "Renamed chat"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	loaded, _ = reopened.WorkspaceMetadataFields("test-workspace", "agentSessions")
	full, err := reopened.LoadAgentSessionBodies(ctx, "test-workspace", loaded.AgentSessions[len(loaded.AgentSessions)-1])
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(full)
	if expected := strings.Replace(string(want), `"title":"Chat"`, `"title":"Renamed chat"`, 1); string(got) != expected {
		t.Fatalf("loaded session differs\n got %.300s\nwant %.300s", got, expected)
	}
	if _, objectReads := reopened.OffloadedContentReads(); objectReads != 2 {
		t.Fatalf("loading the transcript read %d objects, want 2", objectReads)
	}
	// The snapshot keeps its references.
	loaded, _ = reopened.WorkspaceMetadataFields("test-workspace", "agentSessions")
	if !AgentSessionHasOffloaded(loaded.AgentSessions[len(loaded.AgentSessions)-1]) {
		t.Fatal("loading the transcript changed the workspace snapshot")
	}
}

func TestStoredFormIsStableAndInlinesBack(t *testing.T) {
	offload := newContentOffload(DatabaseConfig{})
	raw, _ := json.Marshal(offloadTestDocument(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)))
	stored, blobs, err := offload.storedForm("documents", raw)
	if err != nil || len(blobs) != 2 {
		t.Fatalf("stored form: %d bodies, %v", len(blobs), err)
	}
	again, _, _ := offload.storedForm("documents", raw)
	if !bytes.Equal(stored, again) {
		t.Fatal("stored form is not deterministic")
	}
	if form, more, _ := offload.storedForm("documents", stored); !bytes.Equal(form, stored) || len(more) != 0 {
		t.Fatal("an already stored record changed")
	}
	bodies := map[string][]byte{}
	for _, blob := range blobs {
		bodies[blob.key] = blob.body
		if !strings.HasPrefix(blob.key, "document-revisions/document_offload/revision_") {
			t.Fatalf("body key %q is not named by document and revision", blob.key)
		}
	}
	resolved, err := resolveDocumentMarkers("documents", stored)
	if err != nil {
		t.Fatal(err)
	}
	var got domain.Document
	if err := json.Unmarshal(resolved, &got); err != nil {
		t.Fatal(err)
	}
	for index := range got.Revisions {
		if ref, ok := got.Revisions[index].Offloaded["body"]; ok {
			if index == 0 {
				t.Fatal("the newest version was offloaded")
			}
			_ = json.Unmarshal(bodies[ref.Key], &got.Revisions[index])
			got.Revisions[index].Offloaded = nil
		}
	}
	var want domain.Document
	_ = json.Unmarshal(raw, &want)
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("inlined document differs\n got %.300s\nwant %.300s", gotJSON, wantJSON)
	}
}

// A writer's native value must match what the row stores: references of
// fields written inline again are dropped, references of fields still not
// loaded are kept, and the input message is not modified.
func TestWithoutReplacedAgentBodyRefsMatchesStoredForm(t *testing.T) {
	ref := func(key string) domain.ContentRef { return domain.ContentRef{Key: key, Size: 42, Summary: "old"} }
	message := domain.AgentMessage{
		ID: "reply", Role: "assistant", Content: "new reply",
		Offloaded: domain.OffloadedContent{"content": ref("old-content")},
		Parts: []domain.AgentMessagePart{
			{ID: "replaced", Type: "text", Text: "new text", Offloaded: domain.OffloadedContent{"text": ref("old-text")}},
			{ID: "unloaded", Type: "text", Offloaded: domain.OffloadedContent{"text": ref("still-offloaded")}},
			{ID: "call", Type: "toolCall", ToolCall: &domain.AgentToolCall{ID: "call", Result: json.RawMessage(`{"ok":true}`),
				Offloaded: domain.OffloadedContent{"result": ref("old-result")}}},
			{ID: "null-call", Type: "toolCall", ToolCall: &domain.AgentToolCall{ID: "null-call", Result: json.RawMessage(`null`),
				Offloaded: domain.OffloadedContent{"result": ref("null-result")}}},
		},
	}
	original, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := WithoutReplacedAgentBodyRefs(message)
	if after, _ := json.Marshal(message); !bytes.Equal(after, original) {
		t.Fatal("the input message was modified")
	}
	if cleaned.Offloaded != nil || cleaned.Parts[0].Offloaded != nil || cleaned.Parts[2].ToolCall.Offloaded != nil {
		t.Fatalf("replaced references kept: %+v", cleaned)
	}
	if _, ok := cleaned.Parts[1].Offloaded["text"]; !ok {
		t.Fatal("the reference of a text that is not loaded was dropped")
	}
	if _, ok := cleaned.Parts[3].ToolCall.Offloaded["result"]; !ok {
		t.Fatal("the reference of a null result was dropped")
	}
	offload := &contentOffload{agentBytes: 1 << 20}
	stored := func(message domain.AgentMessage) string {
		raw, err := json.Marshal(domain.AgentSession{ID: "session", Messages: []domain.AgentMessage{message}})
		if err != nil {
			t.Fatal(err)
		}
		form, blobs, err := offload.storedForm("agentSessions", raw)
		if err != nil || len(blobs) != 0 {
			t.Fatal("stored form", err, len(blobs))
		}
		// A re-encoded record orders its keys; compare the values.
		var value any
		if err := json.Unmarshal(form, &value); err != nil {
			t.Fatal(err)
		}
		canonical, _ := json.Marshal(value)
		return string(canonical)
	}
	if got, want := stored(cleaned), stored(message); got != want {
		t.Fatalf("cleaned message stores differently:\n%s\n%s", got, want)
	}
	if !strings.Contains(stored(message), "still-offloaded") || strings.Contains(stored(message), "old-text") {
		t.Fatal("the stored form no longer drops only replaced references")
	}
	user := domain.AgentMessage{ID: "user", Role: "user", Content: "question", Offloaded: domain.OffloadedContent{"content": ref("user")}}
	if got := WithoutReplacedAgentBodyRefs(user); len(got.Offloaded) != 1 {
		t.Fatal("a user message is not offloaded and keeps its fields")
	}
}

// A row the offload stored form re-encoded (sorted keys) is the stored form
// of the same session installed from it (field order, nothing to drop): a
// conflict refresh must not take the key order for another server's commit.
func TestInstalledSessionRowStoredIgnoresKeyOrder(t *testing.T) {
	repo := streamStore(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	session := domain.AgentSession{ID: "session", UserID: "owner", Title: "Chat", CreatedAt: at, UpdatedAt: at, Messages: []domain.AgentMessage{{
		ID: "reply", Role: "assistant", Content: "reply", CreatedAt: at, Status: "streaming",
		Parts: []domain.AgentMessagePart{{ID: "text", Type: "text", Text: "new text", Offloaded: domain.OffloadedContent{"text": {Key: "old", Size: 3}}}},
	}}}
	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := (&contentOffload{agentBytes: 1 << 20}).storedForm("agentSessions", raw)
	if err != nil {
		t.Fatal(err)
	}
	session.Messages[0] = WithoutReplacedAgentBodyRefs(session.Messages[0])
	installed, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(row, installed) {
		t.Fatal("the fixture needs a row whose keys the stored form re-encoded")
	}
	part := func(value []byte) legacySessionJSONPart {
		return legacySessionJSONPart{bytes.NewReader(value), 0, int64(len(value))}
	}
	check := func(installed, row []byte) bool {
		owner := &ResourceRead{store: repo}
		defer owner.Close()
		stored, err := legacyInstalledRecordValueStored(t.Context(), owner, "agentSessions", session.ID, part(installed), part(row))
		if err != nil {
			t.Fatal(err)
		}
		return stored
	}
	if !check(installed, row) {
		t.Fatal("the same session in another key order counts as another server's commit")
	}
	changed := bytes.Replace(row, []byte(`"new text"`), []byte(`"peer text"`), 1)
	if check(installed, changed) {
		t.Fatal("a peer's changed text counts as this server's stored form")
	}
	if check(installed, append(slices.Clone(row), bytes.Repeat([]byte(" "), MetadataRecordPageMaxBytes)...)) {
		t.Fatal("a large row is not conservatively another server's commit")
	}
	if repo.MetadataWorkUsedForTesting() != 0 {
		t.Fatal("the comparison leaked metadata work")
	}
}
