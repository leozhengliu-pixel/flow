package store

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
)

// Offloaded content keeps large bodies out of workspace metadata rows and
// out of memory: the content of older document versions (also inside deleted
// documents in the trash), and long assistant replies and tool results in
// agent conversations. A stored record names each body with a reference under
// "_offloaded"; the body is kept gzip-compressed in object storage
// (FLOW_STORAGE_DRIVER=s3) or in the metadata_blobs table.
//
// Loading a workspace reads no bodies: the references stay on the loaded
// values (domain.OffloadedContent) and the fields they name are empty. Code
// that serves or uses a body loads it on demand (LoadDocumentRevisionBodies,
// LoadAgentSessionBodies), through a byte-bounded in-process cache. A field
// that holds a value again (an edited reply, a version restored into place)
// replaces its reference on the next write.
//
// Bodies are content-addressed (the key ends in a hash of the body), so a key
// always names the same bytes: writers never overwrite a body, and a body no
// record references any more is deleted by SweepOffloadedContent after a grace
// period.

const (
	offloadMarker = "_offloaded"
	// revisionOffloadMinBytes keeps short versions inline.
	revisionOffloadMinBytes   = 1024
	defaultAgentOffloadBytes  = 8 << 10
	defaultContentCacheBytes  = 128 << 20
	offloadSummaryChars       = 200
	offloadedFetchConcurrency = 16
)

// offloadFields are the metadata collections whose records can hold
// offloaded bodies.
var offloadFields = map[string]bool{"documents": true, "trash": true, "agentSessions": true}

var (
	offloadMarkerBytes  = []byte(`"` + offloadMarker + `"`)
	documentMarkerBytes = []byte(`"body":"document"`)
	offloadedKeyPrefix  = regexp.MustCompile(`"key":"((?:document-revisions|agent-transcripts)/[^"\\]+)"`)
	// revisionBodyFields are the parts of a document version stored as its body.
	revisionBodyFields = []string{"content", "contentState", "contentData"}
)

type contentBlob struct {
	key  string
	body []byte
}

type contentOffload struct {
	objects    objectstore.Store
	agentBytes int
	cache      *contentCache
	// Bodies read from the database and from object storage (not the cache).
	databaseReads, objectReads atomic.Int64
}

func newContentOffload(config DatabaseConfig) *contentOffload {
	agentBytes := config.AgentOffloadBytes
	if agentBytes <= 0 {
		agentBytes = defaultAgentOffloadBytes
	}
	cacheBytes := config.ContentCacheBytes
	if cacheBytes <= 0 {
		cacheBytes = defaultContentCacheBytes
	}
	return &contentOffload{objects: config.ContentStore, agentBytes: agentBytes, cache: newContentCache(cacheBytes)}
}

// OffloadedContentReads reports how many offloaded bodies this store has read
// from the database and from object storage (cache hits are not counted).
func (s *SQLiteStore) OffloadedContentReads() (database, objects int64) {
	return s.db.content.databaseReads.Load(), s.db.content.objectReads.Load()
}

func (s *SQLiteStore) ensureOffloadedContent(ctx context.Context) error {
	blob := "BLOB"
	if s.dialect == "mysql" {
		blob = "LONGBLOB"
	} else if s.dialect == "postgres" {
		blob = "BYTEA"
	}
	statements := []string{
		// data is NULL when the body lives in object storage.
		`CREATE TABLE IF NOT EXISTS metadata_blobs(workspace_key VARCHAR(191) NOT NULL,blob_key VARCHAR(191) NOT NULL,size BIGINT NOT NULL,data ` + blob + `,created_at BIGINT NOT NULL,orphaned_at BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(workspace_key,blob_key))`,
		`CREATE TABLE IF NOT EXISTS metadata_blob_migrations(workspace_key VARCHAR(191) NOT NULL,field VARCHAR(191) NOT NULL,last_key VARCHAR(191) NOT NULL,complete INTEGER NOT NULL,PRIMARY KEY(workspace_key,field))`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- stored form

// storedForm returns raw as it is stored: offloadable bodies replaced by
// references, plus the bodies to save. Records without offloadable bodies
// come back unchanged.
func (c *contentOffload) storedForm(field string, raw []byte) ([]byte, []contentBlob, error) {
	if c == nil || !offloadFields[field] || len(raw) == 0 || raw[0] != '{' {
		return raw, nil, nil
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		return raw, nil, nil
	}
	var blobs []contentBlob
	changed := false
	var err error
	switch field {
	case "documents":
		changed, err = offloadDocument(record, &blobs)
	case "trash":
		if jsonString(record["resourceType"]) != "document" {
			return raw, nil, nil
		}
		var document map[string]json.RawMessage
		if json.Unmarshal(record["payload"], &document) != nil || document == nil {
			return raw, nil, nil
		}
		if changed, err = offloadDocument(document, &blobs); changed && err == nil {
			record["payload"], err = json.Marshal(document)
		}
	case "agentSessions":
		changed, err = c.offloadTranscript(record, &blobs)
	}
	if err != nil || !changed {
		return raw, nil, err
	}
	stored, err := json.Marshal(record)
	return stored, blobs, err
}

// offloadDocument moves version bodies out of a document: a version equal to
// the document itself (usually the newest) refers to it, and longer older
// versions go to a body named by the document and version ids. The newest
// version is the one edits update in place, so it is never offloaded. A
// version that is not loaded keeps its reference.
func offloadDocument(document map[string]json.RawMessage, blobs *[]contentBlob) (bool, error) {
	var revisions []map[string]json.RawMessage
	if json.Unmarshal(document["revisions"], &revisions) != nil || len(revisions) == 0 {
		return false, nil
	}
	documentID := jsonString(document["id"])
	changed := false
	for index, revision := range revisions {
		if revision == nil {
			continue
		}
		body := map[string]json.RawMessage{}
		size := 0
		same := true
		for _, name := range revisionBodyFields {
			value, ok := revision[name]
			if ok {
				body[name] = value
				size += len(value)
			}
			if current, has := document[name]; ok != has || !bytes.Equal(value, current) {
				same = false
			}
		}
		if len(body) == 0 {
			continue
		}
		// A version that is not loaded serializes an empty content: it keeps
		// its reference, stored without the empty fields.
		if revision[offloadMarker] != nil && emptyRevisionBody(body) {
			for name := range body {
				delete(revision, name)
			}
			changed = true
			continue
		}
		// The body fields hold the version's value; any earlier reference is
		// replaced.
		var marker any
		switch {
		case same:
			marker = map[string]string{"body": "document"}
		case index > 0 && size >= revisionOffloadMinBytes:
			encoded, err := json.Marshal(body)
			if err != nil {
				return false, err
			}
			key := revisionBlobKey(documentID, jsonString(revision["id"]), encoded)
			*blobs = append(*blobs, contentBlob{key: key, body: encoded})
			marker = map[string]domain.ContentRef{"body": {Key: key, Size: len(encoded)}}
		default:
			if _, had := revision[offloadMarker]; had {
				delete(revision, offloadMarker)
				changed = true
			}
			continue
		}
		for _, name := range revisionBodyFields {
			delete(revision, name)
		}
		revision[offloadMarker], _ = json.Marshal(marker)
		changed = true
	}
	if !changed {
		return false, nil
	}
	encoded, err := json.Marshal(revisions)
	document["revisions"] = encoded
	return true, err
}

// offloadTranscript moves long assistant replies (message content and text
// parts) and tool results out of an agent session, keeping a short summary.
// User messages and tool arguments stay inline: titles, context and undo read
// them. Fields that are not loaded keep their references.
func (c *contentOffload) offloadTranscript(session map[string]json.RawMessage, blobs *[]contentBlob) (bool, error) {
	var messages []map[string]json.RawMessage
	if json.Unmarshal(session["messages"], &messages) != nil || len(messages) == 0 {
		return false, nil
	}
	sessionID := jsonString(session["id"])
	offload := func(object map[string]json.RawMessage, names ...string) bool {
		if object == nil {
			return false
		}
		refs := map[string]json.RawMessage{}
		if marker, ok := object[offloadMarker]; ok {
			_ = json.Unmarshal(marker, &refs)
		}
		changed := false
		for _, name := range names {
			value, ok := object[name]
			_, had := refs[name]
			if !ok {
				continue
			}
			// A field that is not loaded serializes empty (message content has
			// no omitempty): it keeps its reference, stored without the field.
			if had && emptyJSONValue(value) {
				delete(object, name)
				changed = true
				continue
			}
			if had {
				delete(refs, name)
				changed = true
			}
			if len(value) < c.agentBytes {
				continue
			}
			key := transcriptBlobKey(sessionID, value)
			*blobs = append(*blobs, contentBlob{key: key, body: value})
			refs[name], _ = json.Marshal(domain.ContentRef{Key: key, Size: len(value), Summary: offloadSummary(value)})
			delete(object, name)
			changed = true
		}
		if !changed {
			return false
		}
		if len(refs) == 0 {
			delete(object, offloadMarker)
		} else {
			object[offloadMarker], _ = json.Marshal(refs)
		}
		return true
	}
	changed := false
	for _, message := range messages {
		if message == nil || jsonString(message["role"]) != "assistant" {
			continue
		}
		var parts []map[string]json.RawMessage
		if json.Unmarshal(message["parts"], &parts) == nil && len(parts) > 0 {
			partsChanged := false
			for _, part := range parts {
				if part == nil {
					continue
				}
				// Parts with references are re-encoded too, so a part that is
				// not loaded is stored exactly as when it was offloaded.
				if offload(part, "text") || part[offloadMarker] != nil {
					partsChanged = true
				}
				var call map[string]json.RawMessage
				if json.Unmarshal(part["toolCall"], &call) == nil && (offload(call, "result") || call != nil && call[offloadMarker] != nil) {
					part["toolCall"], _ = json.Marshal(call)
					partsChanged = true
				}
			}
			if partsChanged {
				message["parts"], _ = json.Marshal(parts)
				changed = true
			}
		}
		if offload(message, "content") {
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	encoded, err := json.Marshal(messages)
	session["messages"] = encoded
	return true, err
}

func emptyJSONValue(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte(`""`)) || bytes.Equal(trimmed, []byte("null"))
}

func emptyRevisionBody(body map[string]json.RawMessage) bool {
	for _, value := range body {
		if !emptyJSONValue(value) {
			return false
		}
	}
	return true
}

func revisionBlobKey(documentID, revisionID string, body []byte) string {
	hash := contentHash(body)
	key := "document-revisions/" + documentID + "/" + revisionID + "/" + hash
	if len(key) > 191 || strings.ContainsAny(documentID+revisionID, "\"\\") {
		key = "document-revisions/" + contentHash([]byte(documentID+"/"+revisionID)) + "/" + hash
	}
	return key
}

func transcriptBlobKey(sessionID string, body []byte) string {
	hash := contentHash(body)
	key := "agent-transcripts/" + sessionID + "/" + hash
	if len(key) > 191 || strings.ContainsAny(sessionID, "\"\\") {
		key = "agent-transcripts/" + contentHash([]byte(sessionID)) + "/" + hash
	}
	return key
}

func contentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:16])
}

func offloadSummary(value json.RawMessage) string {
	text := string(value)
	var decoded string
	if json.Unmarshal(value, &decoded) == nil {
		text = decoded
	}
	if utf8.RuneCountInString(text) <= offloadSummaryChars {
		return text
	}
	return string([]rune(text)[:offloadSummaryChars]) + "…"
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

// ---------------------------------------------------------------- loading

// resolveDocumentMarkers fills the versions that refer to their document
// (no body is read); offloaded versions keep their references.
func resolveDocumentMarkers(field string, raw []byte) ([]byte, error) {
	if (field != "documents" && field != "trash") || !bytes.Contains(raw, documentMarkerBytes) {
		return raw, nil
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		return raw, err
	}
	resolve := func(document map[string]json.RawMessage) (bool, error) {
		var revisions []map[string]json.RawMessage
		if json.Unmarshal(document["revisions"], &revisions) != nil {
			return false, nil
		}
		changed := false
		for _, revision := range revisions {
			if revision == nil || revision[offloadMarker] == nil {
				continue
			}
			var refs map[string]json.RawMessage
			if err := json.Unmarshal(revision[offloadMarker], &refs); err != nil {
				return false, err
			}
			if jsonString(refs["body"]) != "document" {
				continue
			}
			for _, name := range revisionBodyFields {
				if value, ok := document[name]; ok {
					revision[name] = value
				}
			}
			delete(revision, offloadMarker)
			changed = true
		}
		if changed {
			encoded, err := json.Marshal(revisions)
			if err != nil {
				return false, err
			}
			document["revisions"] = encoded
		}
		return changed, nil
	}
	if field == "documents" {
		if _, err := resolve(record); err != nil {
			return raw, err
		}
		return json.Marshal(record)
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(record["payload"], &document) != nil || document == nil {
		return raw, nil
	}
	changed, err := resolve(document)
	if err != nil || !changed {
		return raw, err
	}
	// The payload is served as raw JSON: re-encode it as a document so its
	// fields keep the order they were deleted with.
	encoded, err := json.Marshal(document)
	if err != nil {
		return raw, err
	}
	var restored domain.Document
	if err := json.Unmarshal(encoded, &restored); err != nil {
		return raw, err
	}
	if record["payload"], err = json.Marshal(restored); err != nil {
		return raw, err
	}
	return json.Marshal(record)
}

// offloadedKeys lists the body keys a stored record refers to.
func offloadedKeys(raw []byte, into map[string]bool) {
	if !bytes.Contains(raw, offloadMarkerBytes) {
		return
	}
	for _, match := range offloadedKeyPrefix.FindAllSubmatch(raw, -1) {
		into[string(match[1])] = true
	}
}

// inlineOffloadedRows prepares loaded metadata rows: versions that refer to
// their document get its content. Offloaded bodies are not read.
func (s *SQLiteStore) inlineOffloadedRows(_ context.Context, _ string, _ metadataReader, rows []metadataCacheRow) error {
	for index, row := range rows {
		if !offloadFields[row.Field] {
			continue
		}
		resolved, err := resolveDocumentMarkers(row.Field, row.Data)
		if err != nil {
			return fmt.Errorf("load %s record %s: %w", row.Field, row.Key, err)
		}
		rows[index].Data = resolved
	}
	return nil
}

// LoadDocumentRevisionBodies loads the offloaded bodies of revisions in
// place. Pass a copy of a document's revisions: the store's own snapshot must
// keep its references.
func (s *SQLiteStore) LoadDocumentRevisionBodies(ctx context.Context, workspace string, revisions []domain.DocumentRevision) error {
	workspace = s.offloadWorkspace(workspace)
	keys := map[string]bool{}
	for _, revision := range revisions {
		if ref, ok := revision.Offloaded["body"]; ok {
			keys[ref.Key] = true
		}
	}
	if len(keys) == 0 {
		return nil
	}
	bodies, err := s.db.content.fetch(ctx, s.db, workspace, keys)
	if err != nil {
		return err
	}
	missing := 0
	for index := range revisions {
		ref, ok := revisions[index].Offloaded["body"]
		if !ok {
			continue
		}
		body, ok := bodies[ref.Key]
		if !ok {
			missing++
			continue
		}
		var fields struct {
			Content      string         `json:"content"`
			ContentState string         `json:"contentState"`
			ContentData  map[string]any `json:"contentData"`
		}
		if err := json.Unmarshal(body, &fields); err != nil {
			return fmt.Errorf("document version %s: %w", revisions[index].ID, err)
		}
		revisions[index].Content, revisions[index].ContentState, revisions[index].ContentData = fields.Content, fields.ContentState, fields.ContentData
		revisions[index].Offloaded = nil
	}
	if missing > 0 {
		slog.Warn("offloaded document versions are missing", "workspace", workspace, "missing", missing)
	}
	return nil
}

// LoadAgentSessionBodies returns session with its offloaded transcript parts
// loaded. Messages, parts and tool calls that change are copied, so the
// store's snapshot keeps its references.
func (s *SQLiteStore) LoadAgentSessionBodies(ctx context.Context, workspace string, session domain.AgentSession) (domain.AgentSession, error) {
	workspace = s.offloadWorkspace(workspace)
	keys := map[string]bool{}
	for _, message := range session.Messages {
		for _, ref := range message.Offloaded {
			keys[ref.Key] = true
		}
		for _, part := range message.Parts {
			for _, ref := range part.Offloaded {
				keys[ref.Key] = true
			}
			if part.ToolCall != nil {
				for _, ref := range part.ToolCall.Offloaded {
					keys[ref.Key] = true
				}
			}
		}
	}
	if len(keys) == 0 {
		return session, nil
	}
	bodies, err := s.db.content.fetch(ctx, s.db, workspace, keys)
	if err != nil {
		return session, err
	}
	// fill returns the fields still not loaded and sets loaded ones.
	fill := func(refs domain.OffloadedContent, set func(name string, body []byte)) domain.OffloadedContent {
		var left domain.OffloadedContent
		for name, ref := range refs {
			if body, ok := bodies[ref.Key]; ok {
				set(name, body)
				continue
			}
			if left == nil {
				left = domain.OffloadedContent{}
			}
			left[name] = ref
		}
		return left
	}
	text := func(body []byte) string {
		var value string
		_ = json.Unmarshal(body, &value)
		return value
	}
	messages := make([]domain.AgentMessage, len(session.Messages))
	copy(messages, session.Messages)
	for index := range messages {
		message := &messages[index]
		if len(message.Offloaded) > 0 {
			message.Offloaded = fill(message.Offloaded, func(name string, body []byte) {
				if name == "content" {
					message.Content = text(body)
				}
			})
		}
		if len(message.Parts) == 0 {
			continue
		}
		parts := make([]domain.AgentMessagePart, len(message.Parts))
		copy(parts, message.Parts)
		for partIndex := range parts {
			part := &parts[partIndex]
			if len(part.Offloaded) > 0 {
				part.Offloaded = fill(part.Offloaded, func(name string, body []byte) {
					if name == "text" {
						part.Text = text(body)
					}
				})
			}
			if part.ToolCall != nil && len(part.ToolCall.Offloaded) > 0 {
				call := *part.ToolCall
				call.Offloaded = fill(call.Offloaded, func(name string, body []byte) {
					if name == "result" {
						call.Result = json.RawMessage(body)
					}
				})
				part.ToolCall = &call
			}
		}
		message.Parts = parts
	}
	session.Messages = messages
	return session, nil
}

// offloadWorkspace names the workspace bodies are stored under; "" is the
// default workspace, as for the store's other readers.
func (s *SQLiteStore) offloadWorkspace(workspace string) string {
	if workspace != "" {
		return workspace
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastWorkspaceKey
}

// LoadOffloadedJSON returns raw with the offloaded bodies it names put back,
// at any depth: a document and its versions, a trash payload, a conversation.
// Event payloads (previous values, webhook snapshots) carry them this way.
func (s *SQLiteStore) LoadOffloadedJSON(ctx context.Context, workspace string, raw json.RawMessage) (json.RawMessage, error) {
	keys := map[string]bool{}
	offloadedKeys(raw, keys)
	if len(keys) == 0 {
		return raw, nil
	}
	bodies, err := s.db.content.fetch(ctx, s.db, s.offloadWorkspace(workspace), keys)
	if err != nil {
		return raw, err
	}
	return inlineOffloadedJSON(raw, bodies), nil
}

func inlineOffloadedJSON(raw json.RawMessage, bodies map[string][]byte) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || !bytes.Contains(trimmed, offloadMarkerBytes) {
		return raw
	}
	switch trimmed[0] {
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(trimmed, &items) != nil {
			return raw
		}
		for index := range items {
			items[index] = inlineOffloadedJSON(items[index], bodies)
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			return raw
		}
		return encoded
	case '{':
		var object map[string]json.RawMessage
		if json.Unmarshal(trimmed, &object) != nil {
			return raw
		}
		for name, value := range object {
			if name != offloadMarker {
				object[name] = inlineOffloadedJSON(value, bodies)
			}
		}
		if marker, ok := object[offloadMarker]; ok {
			var refs map[string]domain.ContentRef
			if json.Unmarshal(marker, &refs) == nil {
				left := map[string]domain.ContentRef{}
				for name, ref := range refs {
					body, found := bodies[ref.Key]
					if !found {
						left[name] = ref
						continue
					}
					if name != "body" {
						object[name] = body
						continue
					}
					var fields map[string]json.RawMessage
					if json.Unmarshal(body, &fields) == nil {
						for field, value := range fields {
							object[field] = value
						}
					}
				}
				if len(left) == 0 {
					delete(object, offloadMarker)
				} else {
					object[offloadMarker], _ = json.Marshal(left)
				}
			}
		}
		encoded, err := json.Marshal(object)
		if err != nil {
			return raw
		}
		return encoded
	}
	return raw
}

// AgentSessionHasOffloaded reports whether some of session's transcript is
// not loaded.
func AgentSessionHasOffloaded(session domain.AgentSession) bool {
	for _, message := range session.Messages {
		if len(message.Offloaded) > 0 {
			return true
		}
		for _, part := range message.Parts {
			if len(part.Offloaded) > 0 || part.ToolCall != nil && len(part.ToolCall.Offloaded) > 0 {
				return true
			}
		}
	}
	return false
}

// fetch returns the bodies for keys that exist.
func (c *contentOffload) fetch(ctx context.Context, reader metadataReader, workspace string, keys map[string]bool) (map[string][]byte, error) {
	bodies := make(map[string][]byte, len(keys))
	wanted := make([]string, 0, len(keys))
	for key := range keys {
		if body, ok := c.cache.get(workspace + "/" + key); ok {
			bodies[key] = body
		} else {
			wanted = append(wanted, key)
		}
	}
	remote := []string{}
	for start := 0; start < len(wanted); start += 200 {
		chunk := wanted[start:min(start+200, len(wanted))]
		clause, args := bindList("blob_key", chunk)
		rows, err := reader.QueryContext(ctx, `SELECT blob_key,data FROM metadata_blobs WHERE workspace_key=? AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key string
			var data []byte
			if err := rows.Scan(&key, &data); err != nil {
				rows.Close()
				return nil, err
			}
			if data == nil {
				remote = append(remote, key)
				continue
			}
			c.databaseReads.Add(1)
			body, err := gunzipBody(data)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("offloaded content %s: %w", key, err)
			}
			bodies[key] = body
			c.cache.put(workspace+"/"+key, body)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(remote) == 0 {
		return bodies, nil
	}
	if c.objects == nil {
		return bodies, nil
	}
	var mu sync.Mutex
	var firstErr error
	jobs := make(chan string)
	var wait sync.WaitGroup
	for range min(offloadedFetchConcurrency, len(remote)) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for key := range jobs {
				c.objectReads.Add(1)
				body, err := c.readObject(ctx, workspace, key)
				mu.Lock()
				if err != nil {
					if firstErr == nil && !errors.Is(err, errOffloadedObjectMissing) {
						firstErr = fmt.Errorf("offloaded content %s: %w", key, err)
					}
				} else {
					bodies[key] = body
				}
				mu.Unlock()
				if err == nil {
					c.cache.put(workspace+"/"+key, body)
				}
			}
		}()
	}
	for _, key := range remote {
		jobs <- key
	}
	close(jobs)
	wait.Wait()
	return bodies, firstErr
}

var errOffloadedObjectMissing = errors.New("offloaded object is missing")

func offloadedObjectKey(workspace, key string) string {
	return workspace + "/" + key + ".json.gz"
}

func (c *contentOffload) readObject(ctx context.Context, workspace, key string) ([]byte, error) {
	reader, _, _, err := c.objects.Open(ctx, offloadedObjectKey(workspace, key))
	if err != nil {
		message := strings.ToLower(err.Error())
		if errors.Is(err, fs.ErrNotExist) || strings.Contains(message, "nosuchkey") || strings.Contains(message, "not found") {
			return nil, errOffloadedObjectMissing
		}
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return gunzipBody(data)
}

func gzipBody(body []byte) []byte {
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = writer.Write(body)
	_ = writer.Close()
	return buffer.Bytes()
}

func gunzipBody(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// ---------------------------------------------------------------- writing

// offloadRecord saves raw's offloadable bodies and returns the record as it
// is stored. It runs inside the transaction that writes the record row.
func offloadRecord(ctx context.Context, tx *sqlTx, workspace, field string, raw []byte) ([]byte, error) {
	if tx.database == nil || tx.database.content == nil || !offloadFields[field] {
		return raw, nil
	}
	stored, blobs, err := tx.database.content.storedForm(field, raw)
	if err != nil || len(blobs) == 0 {
		return stored, err
	}
	return stored, tx.database.content.save(ctx, tx, workspace, blobs)
}

// storedEventPreviousValues returns a document event's previous values as
// they are stored. The previous values of a content edit list every version
// of the document the server holds in memory, each with its whole body, so
// inline they grow with the document's history (up to 100 versions) on
// every edit. Stored, each version body of at least revisionOffloadMinBytes
// is an offloaded body instead: content-addressed, so a body the document's
// own record already offloaded is not written again, and kept while the
// event refers to it (offloadedEventReferences). The pre-edit fields of the
// document itself stay inline. LoadOffloadedJSON returns the full values;
// webhooks, loops and other sinks get the in-memory event, which is never
// changed.
func storedEventPreviousValues(ctx context.Context, tx *sqlTx, workspace, eventType, documentID string, previous []byte) ([]byte, error) {
	if tx.database == nil || tx.database.content == nil || !strings.HasPrefix(eventType, "document.") || !bytes.Contains(previous, []byte(`"revisions"`)) {
		return previous, nil
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(previous, &values) != nil {
		return previous, nil
	}
	var revisions []map[string]json.RawMessage
	if json.Unmarshal(values["revisions"], &revisions) != nil || len(revisions) == 0 {
		return previous, nil
	}
	var blobs []contentBlob
	for _, revision := range revisions {
		if revision == nil {
			continue
		}
		body := map[string]json.RawMessage{}
		size := 0
		for _, name := range revisionBodyFields {
			if value, ok := revision[name]; ok {
				body[name] = value
				size += len(value)
			}
		}
		// Short bodies stay inline; a version that is not loaded has an
		// empty body and keeps its reference.
		if size < revisionOffloadMinBytes {
			continue
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		key := revisionBlobKey(documentID, jsonString(revision["id"]), encoded)
		blobs = append(blobs, contentBlob{key: key, body: encoded})
		for name := range body {
			delete(revision, name)
		}
		revision[offloadMarker], _ = json.Marshal(map[string]domain.ContentRef{"body": {Key: key, Size: len(encoded)}})
	}
	if len(blobs) == 0 {
		return previous, nil
	}
	if err := tx.database.content.save(ctx, tx, workspace, blobs); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(revisions)
	if err != nil {
		return nil, err
	}
	values["revisions"] = encoded
	return json.Marshal(values)
}

// storedRecordEqual reports whether stored (a row as read from the database)
// is how raw would be stored, so an unchanged record is not rewritten.
func storedRecordEqual(tx *sqlTx, refs issueReferences, field string, stored, raw []byte) bool {
	if refs.equalDisplayData(stored, raw) {
		return true
	}
	if tx.database == nil || tx.database.content == nil || !offloadFields[field] || !bytes.Contains(stored, offloadMarkerBytes) {
		return false
	}
	form, _, err := tx.database.content.storedForm(field, raw)
	return err == nil && refs.equalDisplayData(stored, form)
}

// save stores the bodies the database does not already hold. A body already
// stored is skipped unless the sweeper marked it unreferenced; it is then
// written again, which also clears the mark. The row is written before the
// object, so a concurrent sweep (which locks the row) cannot delete the object
// after this write put it back.
func (c *contentOffload) save(ctx context.Context, tx *sqlTx, workspace string, blobs []contentBlob) error {
	unique := map[string][]byte{}
	keys := []string{}
	for _, blob := range blobs {
		if _, seen := unique[blob.key]; !seen {
			unique[blob.key] = blob.body
			keys = append(keys, blob.key)
		}
	}
	for start := 0; start < len(keys); start += 200 {
		chunk := keys[start:min(start+200, len(keys))]
		clause, args := bindList("blob_key", chunk)
		rows, err := tx.QueryContext(ctx, `SELECT blob_key FROM metadata_blobs WHERE workspace_key=? AND orphaned_at=0 AND `+clause, append([]any{workspace}, args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			delete(unique, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	now := time.Now().UnixMilli()
	for _, key := range keys {
		body, ok := unique[key]
		if !ok {
			continue
		}
		compressed := gzipBody(body)
		var data any = compressed
		if c.objects != nil {
			data = nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO metadata_blobs(workspace_key,blob_key,size,data,created_at,orphaned_at) VALUES(?,?,?,?,?,0) ON CONFLICT(workspace_key,blob_key) DO UPDATE SET orphaned_at=0`, workspace, key, len(body), data, now); err != nil {
			return err
		}
		if c.objects != nil {
			if _, err := c.objects.Put(ctx, offloadedObjectKey(workspace, key), bytes.NewReader(compressed), "application/gzip"); err != nil {
				return fmt.Errorf("store offloaded content: %w", err)
			}
		}
		c.cache.put(workspace+"/"+key, body)
	}
	return nil
}

// ---------------------------------------------------------------- migration

// MigrateOffloadedContent rewrites, in small batches, metadata records that
// were stored before offloading (or before a threshold change) so their bodies
// move out of the row. Progress is kept per workspace and collection, so an
// interrupted migration resumes where it stopped. It returns the number of
// records rewritten and whether every collection is done.
func (s *SQLiteStore) MigrateOffloadedContent(ctx context.Context, batch int) (int, bool, error) {
	if s.db.content == nil {
		return 0, true, nil
	}
	if batch <= 0 {
		batch = 16
	}
	workspaces := []string{}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace_key FROM workspace_metadata_records WHERE field IN ('documents','trash','agentSessions')`)
	if err != nil {
		return 0, false, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, false, err
		}
		workspaces = append(workspaces, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, false, err
	}
	rewritten := 0
	for _, workspace := range workspaces {
		for _, field := range []string{"documents", "trash", "agentSessions"} {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO metadata_blob_migrations(workspace_key,field,last_key,complete) VALUES(?,?,'',0) ON CONFLICT DO NOTHING`, workspace, field); err != nil {
				return rewritten, false, err
			}
			var last string
			var complete int
			if err := s.db.QueryRowContext(ctx, `SELECT last_key,complete FROM metadata_blob_migrations WHERE workspace_key=? AND field=?`, workspace, field).Scan(&last, &complete); err != nil {
				return rewritten, false, err
			}
			if complete != 0 {
				continue
			}
			count, next, done, err := s.migrateOffloadedBatch(ctx, workspace, field, last, batch)
			rewritten += count
			if err != nil {
				return rewritten, false, err
			}
			flag := 0
			if done {
				flag = 1
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE metadata_blob_migrations SET last_key=?,complete=? WHERE workspace_key=? AND field=?`, next, flag, workspace, field); err != nil {
				return rewritten, false, err
			}
			// One batch per call keeps each call short; callers loop.
			return rewritten, false, nil
		}
	}
	return rewritten, true, nil
}

func (s *SQLiteStore) migrateOffloadedBatch(ctx context.Context, workspace, field, after string, batch int) (int, string, bool, error) {
	// Keys first: the rows themselves can be large, so each one is read and
	// rewritten on its own.
	rows, err := s.db.QueryContext(ctx, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key>? ORDER BY record_key LIMIT ?`, workspace, field, after, batch)
	if err != nil {
		return 0, after, false, err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, after, false, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, after, false, err
	}
	rewritten := 0
	for _, key := range keys {
		changed, err := s.migrateOffloadedRecord(ctx, workspace, field, key)
		if err != nil {
			return rewritten, after, false, err
		}
		if changed {
			rewritten++
		}
		after = key
	}
	return rewritten, after, len(keys) < batch, nil
}

func (s *SQLiteStore) migrateOffloadedRecord(ctx context.Context, workspace, field, key string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// The row lock keeps writers out until the rewrite commits (SQLite has a
	// single writer).
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var order int
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`+lock, workspace, field, key).Scan(&order, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	stored, err := offloadRecord(ctx, tx, workspace, field, raw)
	if err != nil {
		return false, err
	}
	if bytes.Equal(stored, raw) {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, stored, workspace, field, key); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.cacheMetadataUpserts(ctx, workspace, []metadataRecordChange{{field: field, key: key, order: order, raw: stored}})
	return true, nil
}

// ---------------------------------------------------------------- sweeping

// SweepOffloadedContent deletes offloaded bodies no record refers to. A body
// found unreferenced is first marked; it is deleted on a later sweep once it
// has stayed unreferenced for grace, so a write that re-uses a body between
// the two sweeps keeps it. It returns how many bodies it deleted.
func (s *SQLiteStore) SweepOffloadedContent(ctx context.Context, now time.Time, grace time.Duration) (int, error) {
	if s.db.content == nil {
		return 0, nil
	}
	workspaces := []string{}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT workspace_key FROM metadata_blobs`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return 0, err
		}
		workspaces = append(workspaces, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	// Retained events' previous values name bodies too; they are kept until
	// the events are pruned.
	eventReferences, err := s.offloadedEventReferences(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, workspace := range workspaces {
		count, err := s.sweepWorkspaceContent(ctx, workspace, now, grace, eventReferences)
		deleted += count
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

// offloadedEventReferences lists the body keys domain events refer to (the
// previous values of document, trash and agent conversation changes).
func (s *SQLiteStore) offloadedEventReferences(ctx context.Context) (map[string]bool, error) {
	referenced := map[string]bool{}
	last := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id,previous_values FROM domain_events WHERE (event_type LIKE 'document.%' OR event_type LIKE 'trash.%' OR event_type LIKE 'agent.%') AND id>? ORDER BY id LIMIT 500`, last)
		if err != nil {
			return nil, err
		}
		count := 0
		for rows.Next() {
			var previous []byte
			if err := rows.Scan(&last, &previous); err != nil {
				rows.Close()
				return nil, err
			}
			offloadedKeys(previous, referenced)
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if count < 500 {
			return referenced, nil
		}
	}
}

func (s *SQLiteStore) sweepWorkspaceContent(ctx context.Context, workspace string, now time.Time, grace time.Duration, eventReferences map[string]bool) (int, error) {
	started := now.UnixMilli()
	referenced := map[string]bool{}
	for key := range eventReferences {
		referenced[key] = true
	}
	last := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field IN ('documents','trash','agentSessions') AND record_key>? ORDER BY record_key LIMIT 64`, workspace, last)
		if err != nil {
			return 0, err
		}
		count := 0
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&last, &data); err != nil {
				rows.Close()
				return 0, err
			}
			offloadedKeys(data, referenced)
			count++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if count < 64 {
			break
		}
	}
	type stored struct {
		key      string
		created  int64
		orphaned int64
	}
	blobs := []stored{}
	rows, err := s.db.QueryContext(ctx, `SELECT blob_key,created_at,orphaned_at FROM metadata_blobs WHERE workspace_key=?`, workspace)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var blob stored
		if err := rows.Scan(&blob.key, &blob.created, &blob.orphaned); err != nil {
			rows.Close()
			return 0, err
		}
		blobs = append(blobs, blob)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, blob := range blobs {
		if ctx.Err() != nil {
			return deleted, ctx.Err()
		}
		switch {
		case referenced[blob.key]:
			if blob.orphaned != 0 {
				if _, err := s.db.ExecContext(ctx, `UPDATE metadata_blobs SET orphaned_at=0 WHERE workspace_key=? AND blob_key=?`, workspace, blob.key); err != nil {
					return deleted, err
				}
			}
		case blob.orphaned == 0:
			// Bodies written after the scan began may belong to a record that
			// was not committed yet when it was read.
			if blob.created < started {
				if _, err := s.db.ExecContext(ctx, `UPDATE metadata_blobs SET orphaned_at=? WHERE workspace_key=? AND blob_key=? AND orphaned_at=0`, started, workspace, blob.key); err != nil {
					return deleted, err
				}
			}
		case blob.orphaned < now.Add(-grace).UnixMilli():
			removed, err := s.deleteOffloadedBody(ctx, workspace, blob.key, blob.orphaned)
			if err != nil {
				return deleted, err
			}
			if removed {
				deleted++
			}
		}
	}
	return deleted, nil
}

// deleteOffloadedBody deletes a body still marked as unreferenced since
// orphaned. The row lock keeps a writer that re-uses the body waiting until
// the delete commits; it then stores the body again.
func (s *SQLiteStore) deleteOffloadedBody(ctx context.Context, workspace, key string, orphaned int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var current int64
	if err := tx.QueryRowContext(ctx, `SELECT orphaned_at FROM metadata_blobs WHERE workspace_key=? AND blob_key=?`+lock, workspace, key).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if current != orphaned {
		return false, nil
	}
	if s.db.content.objects != nil {
		if err := s.db.content.objects.Delete(ctx, offloadedObjectKey(workspace, key)); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM metadata_blobs WHERE workspace_key=? AND blob_key=?`, workspace, key); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	s.db.content.cache.remove(workspace + "/" + key)
	return true, nil
}

// ---------------------------------------------------------------- cache

// contentCache is a byte-bounded LRU of offloaded bodies. Keys name immutable
// content, so entries never go stale.
type contentCache struct {
	mu      sync.Mutex
	limit   int
	size    int
	order   *list.List
	entries map[string]*list.Element
}

type contentCacheEntry struct {
	key  string
	body []byte
}

func newContentCache(limit int) *contentCache {
	return &contentCache{limit: limit, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *contentCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(element)
	return element.Value.(*contentCacheEntry).body, true
}

func (c *contentCache) put(key string, body []byte) {
	if len(body) > c.limit/4 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(&contentCacheEntry{key: key, body: body})
	c.size += len(body)
	for c.size > c.limit {
		oldest := c.order.Back()
		entry := oldest.Value.(*contentCacheEntry)
		c.order.Remove(oldest)
		delete(c.entries, entry.key)
		c.size -= len(entry.body)
	}
}

func (c *contentCache) remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		c.order.Remove(element)
		delete(c.entries, key)
		c.size -= len(element.Value.(*contentCacheEntry).body)
	}
}
