package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"github.com/coder/websocket"
)

const (
	collaborationUpdateFrame    byte = 1
	collaborationAwarenessFrame byte = 2
	maxCollaborationFrameSize        = 2 << 20
)

type realtimeSocketMessage struct {
	binary bool
	data   []byte
	sent   chan error
}

type realtimeSocketClient struct {
	id             uint64
	clientID       string
	workspace      string
	documents      map[string]struct{}
	issueDocuments map[string]string
	// documentAccess caches what this socket may do on each joined
	// standalone document; it is re-checked on updates once stale.
	documentAccess map[string]*documentSocketAccess
	send           chan realtimeSocketMessage
	queuedBytes    atomic.Int64
	cancel         context.CancelFunc
}

const maxSocketQueueBytes = 8 << 20

func (client *realtimeSocketClient) enqueue(message realtimeSocketMessage, copyData bool) bool {
	size := int64(len(message.data))
	if client.queuedBytes.Add(size) > maxSocketQueueBytes {
		client.queuedBytes.Add(-size)
		client.cancel()
		return false
	}
	if copyData {
		message.data = slices.Clone(message.data)
	}
	select {
	case client.send <- message:
		return true
	default:
		client.queuedBytes.Add(-size)
		client.cancel()
		return false
	}
}

type collaborationEventPayload struct {
	DocumentID string `json:"documentId"`
	ClientID   string `json:"clientId"`
	UpdateID   string `json:"updateId,omitempty"`
	Data       string `json:"data"`
}

type collaborationSyncUpdate struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

type collaborationSyncMessage struct {
	Type         string                    `json:"type"`
	DocumentID   string                    `json:"documentId"`
	ContentState string                    `json:"contentState,omitempty"`
	Updates      []collaborationSyncUpdate `json:"updates"`
	More         bool                      `json:"more,omitempty"`
	// ReadOnly tells viewers and commenters the server rejects their edits.
	ReadOnly bool `json:"readOnly,omitempty"`
}

// documentSocketAccess is a joined document's cached authorization.
type documentSocketAccess struct {
	documentID string
	canEdit    bool
	checkedAt  time.Time
}

// documentAccessTTL bounds how long a socket keeps editing a document after
// its access changed without an explicit invalidation (e.g. a team change).
const documentAccessTTL = 5 * time.Second

func (h *realtimeHub) addSocket(workspace, clientID string, cancel context.CancelFunc) *realtimeSocketClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	client := &realtimeSocketClient{id: h.nextID, clientID: clientID, workspace: workspace, documents: map[string]struct{}{}, issueDocuments: map[string]string{}, documentAccess: map[string]*documentSocketAccess{}, send: make(chan realtimeSocketMessage, 256), cancel: cancel}
	if h.sockets[workspace] == nil {
		h.sockets[workspace] = map[uint64]*realtimeSocketClient{}
	}
	h.sockets[workspace][client.id] = client
	return client
}

func (h *realtimeHub) removeSocket(client *realtimeSocketClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sockets[client.workspace], client.id)
	if len(h.sockets[client.workspace]) == 0 {
		delete(h.sockets, client.workspace)
	}
}

func (h *realtimeHub) joinDocument(client *realtimeSocketClient, documentID string) {
	h.mu.Lock()
	client.documents[documentID] = struct{}{}
	h.mu.Unlock()
}

func (h *realtimeHub) joinedDocument(client *realtimeSocketClient, documentID string) bool {
	h.mu.Lock()
	_, joined := client.documents[documentID]
	h.mu.Unlock()
	return joined
}

// invalidateDocumentAccess makes every socket re-check its access to a
// document (by id or collaboration id) before its next update.
func (h *realtimeHub) invalidateDocumentAccess(workspace, documentID string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, client := range h.sockets[workspace] {
		for collaborationID, access := range client.documentAccess {
			if access.documentID == documentID || collaborationID == documentID {
				access.checkedAt = time.Time{}
			}
		}
	}
}

// broadcastDocumentConflict tells every editor joined to a replaced
// collaboration generation to reload the document.
func (h *realtimeHub) broadcastDocumentConflict(workspace, collaborationID string) {
	if h == nil || collaborationID == "" {
		return
	}
	raw, _ := json.Marshal(map[string]any{"type": "document.conflict", "documentId": collaborationID})
	h.broadcastDocument(workspace, collaborationID, 0, realtimeSocketMessage{data: raw})
	h.invalidateDocumentAccess(workspace, collaborationID)
}

func (h *realtimeHub) broadcastDocument(workspace, documentID string, excludedSocketID uint64, message realtimeSocketMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, client := range h.sockets[workspace] {
		if client.id == excludedSocketID {
			continue
		}
		if _, joined := client.documents[documentID]; !joined {
			continue
		}
		client.enqueue(message, true)
	}
}

func (s *server) realtimeSocket(w http.ResponseWriter, r *http.Request) {
	workspace, clientID := workspaceKey(r), strings.TrimSpace(r.URL.Query().Get("clientId"))
	if workspace == "" || clientID == "" || len(clientID) > 128 {
		writeError(w, http.StatusBadRequest, "workspace and clientId are required")
		return
	}
	if _, ok := s.store.WorkspaceMetadata(workspace); !ok {
		writeError(w, http.StatusNotFound, "Workspace not found")
		return
	}
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	connection.SetReadLimit(maxCollaborationFrameSize)
	ctx, cancel := context.WithCancel(context.Background())
	client := s.realtime.addSocket(workspace, clientID, cancel)
	defer func() {
		cancel()
		s.realtime.removeSocket(client)
		_ = connection.Close(websocket.StatusNormalClosure, "")
	}()

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case message := <-client.send:
				client.queuedBytes.Add(-int64(len(message.data)))
				messageType := websocket.MessageText
				if message.binary {
					messageType = websocket.MessageBinary
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
				err := connection.Write(writeCtx, messageType, message.data)
				writeCancel()
				if message.sent != nil {
					message.sent <- err
				}
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		messageType, message, err := connection.Read(ctx)
		if err != nil {
			break
		}
		switch messageType {
		case websocket.MessageText:
			if err := s.handleCollaborationCommand(r, client, message); err != nil {
				sendSocketJSON(client, map[string]any{"type": "error", "message": err.Error()})
			}
		case websocket.MessageBinary:
			if err := s.handleCollaborationFrame(r, client, message); err != nil {
				sendSocketJSON(client, map[string]any{"type": "error", "message": err.Error()})
			}
		}
	}
	cancel()
	<-writerDone
}

func (s *server) handleCollaborationCommand(r *http.Request, client *realtimeSocketClient, raw []byte) error {
	var command struct {
		Type       string `json:"type"`
		IssueID    string `json:"issueId"`
		DocumentID string `json:"documentId"`
	}
	if err := json.Unmarshal(raw, &command); err != nil || command.Type != "document.join" {
		return errors.New("unsupported collaboration command")
	}
	data, query, err := s.issueRecordsQuery(r)
	if err != nil {
		return err
	}
	if s.authDisabled && command.IssueID == "" {
		var ok bool
		data, ok = s.store.WorkspaceMetadata(query.Workspace)
		if !ok {
			return store.ErrAuthForbidden
		}
	}
	if !s.authDisabled {
		data, err = s.store.PagedWorkspaceMetadata(r.Context(), query.Workspace, authUser(r).ID)
		if err != nil {
			return err
		}
		filterBootstrapForAPIKey(&data, r)
	}
	var contentState string
	readOnly := false
	if command.IssueID != "" {
		query.Filter = store.IssueFilter{Field: "id", Values: []string{command.IssueID}}
		query.Archived = "all"
		query.Limit = 1
		page, err := s.store.QueryIssueRecords(r.Context(), query)
		if err != nil {
			return err
		}
		data.Issues = page.Items
		issueIndex := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == command.IssueID })
		if issueIndex < 0 {
			return errors.New("issue is outside your teams")
		}
		issue := data.Issues[issueIndex]
		expectedDocumentID := "document_content_" + issue.ID
		if issue.DocumentContent != nil && issue.DocumentContent.ID != "" {
			expectedDocumentID = issue.DocumentContent.ID
			contentState = issue.DocumentContent.ContentState
		}
		if command.DocumentID != expectedDocumentID {
			sendSocketJSON(client, map[string]any{"type": "document.conflict", "documentId": command.DocumentID})
			return nil
		}
		s.realtime.mu.Lock()
		client.issueDocuments[command.DocumentID] = issue.ID
		s.realtime.mu.Unlock()
	} else {
		// Standalone workspace documents use the same collaboration protocol as
		// issue descriptions, but are authorized by the document's role:
		// viewers and commenters join read-only.
		document, conflict, err := collaborationDocument(data, command.DocumentID)
		if err != nil || documentRole(s, data, document) == "none" {
			return errors.New("document is outside your teams")
		}
		if conflict {
			sendSocketJSON(client, map[string]any{"type": "document.conflict", "documentId": command.DocumentID})
			return nil
		}
		contentState = document.ContentState
		readOnly = !canEditDocument(documentRole(s, data, document))
		s.realtime.mu.Lock()
		client.documentAccess[command.DocumentID] = &documentSocketAccess{documentID: document.ID, canEdit: !readOnly, checkedAt: time.Now()}
		s.realtime.mu.Unlock()
	}
	s.realtime.joinDocument(client, command.DocumentID)
	message := collaborationSyncMessage{Type: "document.sync", DocumentID: command.DocumentID, ContentState: contentState, Updates: []collaborationSyncUpdate{}, ReadOnly: readOnly}
	bytes := len(contentState)
	flush := func(more bool) error {
		message.More = more
		raw, err := json.Marshal(message)
		if err != nil {
			return err
		}
		sent := make(chan error, 1)
		if !client.enqueue(realtimeSocketMessage{data: raw, sent: sent}, false) {
			return errors.New("collaboration queue exceeded")
		}
		timer := time.NewTimer(6 * time.Second)
		defer timer.Stop()
		select {
		case err := <-sent:
			message.ContentState = ""
			message.Updates = nil
			bytes = 0
			return err
		case <-timer.C:
			return errors.New("collaboration writer timed out")
		case <-r.Context().Done():
			return r.Context().Err()
		}
	}
	err = s.store.WalkDocumentUpdates(r.Context(), client.workspace, command.DocumentID, func(update store.DocumentCollaborationUpdate) error {
		if len(message.Updates) > 0 && (bytes+len(update.Data)*4/3 > 1<<20 || len(message.Updates) >= 128) {
			if err := flush(true); err != nil {
				return err
			}
		}
		encoded := base64.StdEncoding.EncodeToString(update.Data)
		message.Updates = append(message.Updates, collaborationSyncUpdate{ID: update.ID, Data: encoded})
		bytes += len(encoded)
		return nil
	})
	if err != nil {
		return err
	}
	if message.Updates == nil {
		message.Updates = []collaborationSyncUpdate{}
	}
	return flush(false)
}

func (s *server) handleCollaborationFrame(r *http.Request, client *realtimeSocketClient, raw []byte) error {
	ctx := r.Context()
	kind, documentID, requestedUpdateID, payload, err := decodeCollaborationFrame(raw)
	if err != nil {
		return err
	}
	if !s.realtime.joinedDocument(client, documentID) {
		return errors.New("join document before sending updates")
	}
	s.realtime.mu.Lock()
	issueID := client.issueDocuments[documentID]
	s.realtime.mu.Unlock()
	if issueID != "" {
		currentDocumentID, err := s.store.IssueDocumentID(ctx, client.workspace, issueID)
		if err != nil {
			return err
		}
		if documentID != currentDocumentID {
			sendSocketJSON(client, map[string]any{"type": "document.conflict", "documentId": documentID})
			return nil
		}
	}
	if kind == collaborationUpdateFrame {
		allowed, conflict, err := s.documentSocketCanEdit(r, client, documentID)
		if err != nil {
			return err
		}
		if conflict {
			sendSocketJSON(client, map[string]any{"type": "document.conflict", "documentId": documentID})
			return nil
		}
		if !allowed {
			// The update is dropped; the client switches its editor to
			// read-only and discards its local edits on reload.
			sendSocketJSON(client, map[string]any{"type": "document.readonly", "documentId": documentID})
			return nil
		}
	}
	if kind == collaborationAwarenessFrame {
		frame := encodeCollaborationFrame(kind, documentID, "", payload)
		s.realtime.broadcastDocument(client.workspace, documentID, client.id, realtimeSocketMessage{binary: true, data: frame})
		s.publishCollaborationEvent(client.workspace, "document.awareness", collaborationEventPayload{DocumentID: documentID, ClientID: client.clientID, Data: base64.StdEncoding.EncodeToString(payload)})
		return nil
	}
	if kind != collaborationUpdateFrame {
		return errors.New("unsupported collaboration frame")
	}
	updateID := strings.TrimSpace(requestedUpdateID)
	if updateID == "" {
		updateID = newCollaborationID()
	}
	if len(updateID) > 191 || !strings.HasPrefix(updateID, "collab_") {
		return errors.New("invalid collaboration update id")
	}
	created, err := s.store.AppendDocumentCollaborationUpdate(ctx, client.workspace, store.DocumentCollaborationUpdate{ID: updateID, DocumentID: documentID, ClientID: client.clientID, Data: slices.Clone(payload), CreatedAt: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("persist collaboration update: %w", err)
	}
	if !created {
		return nil
	}
	frame := encodeCollaborationFrame(kind, documentID, updateID, payload)
	// The sender receives the committed update ID so it can safely compact it later.
	s.realtime.broadcastDocument(client.workspace, documentID, 0, realtimeSocketMessage{binary: true, data: frame})
	s.publishCollaborationEvent(client.workspace, "document.update", collaborationEventPayload{DocumentID: documentID, ClientID: client.clientID, UpdateID: updateID, Data: base64.StdEncoding.EncodeToString(payload)})
	return nil
}

// collaborationDocument finds the standalone document a collaboration id
// names. conflict reports a join with the document's id or slug while its
// realtime state moved to a new generation (a restored version).
func collaborationDocument(data domain.Bootstrap, collaborationID string) (domain.Document, bool, error) {
	for _, document := range data.Documents {
		current := document.CollaborationID
		if current == "" {
			current = document.ID
		}
		if current == collaborationID {
			return document, false, nil
		}
		if document.ID == collaborationID || document.SlugID == collaborationID {
			return document, true, nil
		}
	}
	return domain.Document{}, false, errNotFound
}

// documentSocketCanEdit authorizes an update frame on a standalone document,
// re-reading the viewer's role once the cached decision is stale. Issue
// description documents are authorized by their issue on join.
func (s *server) documentSocketCanEdit(r *http.Request, client *realtimeSocketClient, collaborationID string) (bool, bool, error) {
	s.realtime.mu.Lock()
	access := client.documentAccess[collaborationID]
	fresh := access != nil && time.Since(access.checkedAt) < documentAccessTTL
	canEdit := access != nil && access.canEdit
	s.realtime.mu.Unlock()
	if access == nil || fresh {
		return access == nil || canEdit, false, nil
	}
	var data domain.Bootstrap
	if s.authDisabled {
		var ok bool
		if data, ok = s.store.WorkspaceMetadata(client.workspace); !ok {
			return false, false, store.ErrAuthForbidden
		}
	} else {
		var err error
		if data, err = s.store.PagedWorkspaceMetadata(r.Context(), client.workspace, authUser(r).ID); err != nil {
			return false, false, err
		}
		filterBootstrapForAPIKey(&data, r)
	}
	document, conflict, err := collaborationDocument(data, collaborationID)
	allowed := err == nil && !conflict && canEditDocument(documentRole(s, data, document))
	s.realtime.mu.Lock()
	access.canEdit, access.checkedAt = allowed, time.Now()
	s.realtime.mu.Unlock()
	return allowed, conflict || errors.Is(err, errNotFound), nil
}

func (s *server) publishCollaborationEvent(workspace, eventType string, payload collaborationEventPayload) {
	if s.coordinator == nil {
		return
	}
	raw, _ := json.Marshal(payload)
	event := domain.RealtimeEvent{ID: newCollaborationID(), Type: eventType, AggregateID: payload.DocumentID, ClientID: payload.ClientID, Payload: raw, CreatedAt: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.coordinator.Publish(ctx, workspace, event); err != nil {
		log.Printf("Redis publish collaboration workspace=%s event=%s: %v", workspace, eventType, err)
	}
}

func (s *server) receiveCollaborationEvent(workspace string, event domain.RealtimeEvent) bool {
	if event.Type != "document.update" && event.Type != "document.awareness" {
		return false
	}
	var payload collaborationEventPayload
	if json.Unmarshal(event.Payload, &payload) != nil || payload.DocumentID == "" {
		return true
	}
	data, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return true
	}
	kind := collaborationUpdateFrame
	if event.Type == "document.awareness" {
		kind = collaborationAwarenessFrame
	}
	frame := encodeCollaborationFrame(kind, payload.DocumentID, payload.UpdateID, data)
	s.realtime.broadcastDocument(workspace, payload.DocumentID, 0, realtimeSocketMessage{binary: true, data: frame})
	return true
}

func sendSocketJSON(client *realtimeSocketClient, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	client.enqueue(realtimeSocketMessage{data: raw}, false)
}

func encodeCollaborationFrame(kind byte, documentID, updateID string, payload []byte) []byte {
	document := []byte(documentID)
	update := []byte(updateID)
	size := 3 + len(document) + len(payload)
	if kind == collaborationUpdateFrame {
		size += 2 + len(update)
	}
	frame := make([]byte, size)
	frame[0] = kind
	binary.BigEndian.PutUint16(frame[1:3], uint16(len(document)))
	copy(frame[3:], document)
	offset := 3 + len(document)
	if kind == collaborationUpdateFrame {
		binary.BigEndian.PutUint16(frame[offset:offset+2], uint16(len(update)))
		offset += 2
		copy(frame[offset:], update)
		offset += len(update)
	}
	copy(frame[offset:], payload)
	return frame
}

func decodeCollaborationFrame(frame []byte) (kind byte, documentID, updateID string, payload []byte, err error) {
	if len(frame) < 4 {
		return 0, "", "", nil, errors.New("invalid collaboration frame")
	}
	kind = frame[0]
	documentLength := int(binary.BigEndian.Uint16(frame[1:3]))
	if documentLength == 0 || len(frame) < 3+documentLength+1 {
		return 0, "", "", nil, errors.New("invalid collaboration document")
	}
	documentID = string(frame[3 : 3+documentLength])
	offset := 3 + documentLength
	if kind == collaborationUpdateFrame {
		if len(frame) < offset+2 {
			return 0, "", "", nil, errors.New("invalid collaboration update")
		}
		updateLength := int(binary.BigEndian.Uint16(frame[offset : offset+2]))
		offset += 2
		if len(frame) < offset+updateLength+1 {
			return 0, "", "", nil, errors.New("invalid collaboration update")
		}
		updateID = string(frame[offset : offset+updateLength])
		offset += updateLength
	}
	if kind != collaborationUpdateFrame && kind != collaborationAwarenessFrame {
		return 0, "", "", nil, errors.New("invalid collaboration message type")
	}
	return kind, documentID, updateID, slices.Clone(frame[offset:]), nil
}

func newCollaborationID() string {
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	return fmt.Sprintf("collab_%d_%s", time.Now().UnixNano(), hex.EncodeToString(random))
}
