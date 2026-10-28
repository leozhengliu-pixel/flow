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
	userID         string
	documents      map[string]struct{}
	issueDocuments map[string]string
	// issueAccess caches each joined issue description's last access check
	// (see collaboration_access.go).
	issueAccess map[string]*issueSocketAccess
	// checkIssue re-reads whether the socket user may open an issue (and
	// edit its description); checkCredential whether its session or API key
	// is still valid.
	checkIssue      func(issueID string) (issue domain.Issue, visible, canEdit bool, err error)
	checkCredential func() bool
	// credentialRevoked: the session or key was revoked; it joins nothing.
	credentialRevoked bool
	// documentAccess caches what this socket may do on each joined
	// standalone document; it is re-checked on updates once stale.
	documentAccess map[string]*documentSocketAccess
	// checkDocument re-reads the socket user's role on a standalone
	// document (by collaboration id) when its access changes.
	checkDocument func(collaborationID string) (role string, conflict bool, err error)
	send          chan realtimeSocketMessage
	queuedBytes   atomic.Int64
	cancel        context.CancelFunc
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
	client := &realtimeSocketClient{id: h.nextID, clientID: clientID, workspace: workspace, documents: map[string]struct{}{}, issueDocuments: map[string]string{}, issueAccess: map[string]*issueSocketAccess{}, documentAccess: map[string]*documentSocketAccess{}, send: make(chan realtimeSocketMessage, 256), cancel: cancel}
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
// document (by id or collaboration id) before its next update. A socket whose
// user can no longer open the document leaves it at once, so it stops
// receiving the document's edits, and is told with document.revoked.
func (h *realtimeHub) invalidateDocumentAccess(workspace, documentID string) {
	if h == nil {
		return
	}
	type joined struct {
		client          *realtimeSocketClient
		collaborationID string
	}
	var affected []joined
	h.mu.Lock()
	for _, client := range h.sockets[workspace] {
		for collaborationID, access := range client.documentAccess {
			if access.documentID == documentID || collaborationID == documentID {
				access.checkedAt = time.Time{}
				if client.checkDocument != nil {
					affected = append(affected, joined{client, collaborationID})
				}
			}
		}
	}
	h.mu.Unlock()
	for _, item := range affected {
		h.recheckStandaloneDocument(item.client, item.collaborationID)
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
	connection, err := websocket.Accept(w, r, webSocketAcceptOptions(r))
	if err != nil {
		return
	}
	connection.SetReadLimit(maxCollaborationFrameSize)
	ctx, cancel := context.WithCancel(context.Background())
	client := s.realtime.addSocket(workspace, clientID, cancel)
	s.realtime.mu.Lock()
	client.userID = authUser(r).ID
	client.checkDocument = func(collaborationID string) (string, bool, error) {
		return s.socketDocumentRole(r, workspace, collaborationID)
	}
	// Access checks run on other goroutines (access changes, the sweep);
	// each reads through its own copy of the request.
	client.checkIssue = func(issueID string) (domain.Issue, bool, bool, error) {
		return s.socketIssue(r, issueID)
	}
	client.checkCredential = func() bool { return s.socketCredentialValid(r) }
	s.realtime.mu.Unlock()
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
				sendSocketJSON(client, map[string]any{"type": "error", "message": publicErrorMessage(r.Context(), err)})
			}
		case websocket.MessageBinary:
			if err := s.handleCollaborationFrame(r, client, message); err != nil {
				sendSocketJSON(client, map[string]any{"type": "error", "message": publicErrorMessage(r.Context(), err)})
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
	s.realtime.mu.Lock()
	revoked := client.credentialRevoked
	s.realtime.mu.Unlock()
	if revoked || client.checkCredential != nil && !client.checkCredential() {
		sendSocketJSON(client, map[string]any{"type": "document.revoked", "documentId": command.DocumentID, "issueId": command.IssueID})
		return nil
	}
	var contentState string
	readOnly := false
	if command.IssueID != "" {
		issue, visible, canEdit, err := s.socketIssue(r, command.IssueID)
		if err != nil && !errors.Is(err, store.ErrAuthForbidden) {
			return err
		}
		if !visible {
			// The issue is gone or no longer visible to this member: the
			// editor shows its no-access state instead of retrying the join.
			sendSocketJSON(client, map[string]any{"type": "document.revoked", "documentId": command.DocumentID, "issueId": command.IssueID})
			return nil
		}
		expectedDocumentID := "document_content_" + issue.ID
		if issue.DocumentContent != nil && issue.DocumentContent.ID != "" {
			expectedDocumentID = issue.DocumentContent.ID
			contentState = issue.DocumentContent.ContentState
		}
		if command.DocumentID != expectedDocumentID {
			sendSocketJSON(client, map[string]any{"type": "document.conflict", "documentId": command.DocumentID})
			return nil
		}
		// Viewers and commenters follow the description live but cannot
		// change it (the same rule as saving it through the issue API).
		readOnly = !canEdit
		s.realtime.mu.Lock()
		client.issueDocuments[command.DocumentID] = issue.ID
		client.issueAccess[command.DocumentID] = &issueSocketAccess{teamID: issue.Team.ID, canEdit: canEdit, checkedAt: time.Now()}
		s.realtime.mu.Unlock()
	} else {
		data, err := s.socketWorkspaceMetadata(r, client.workspace)
		if err != nil {
			return err
		}
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
		readOnly = !canEditDocument(documentRole(s, data, document)) || !socketCredentialMayWrite(r, "/api/documents/"+document.ID)
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
	err := s.store.WalkDocumentUpdates(r.Context(), client.workspace, command.DocumentID, func(update store.DocumentCollaborationUpdate) error {
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
		// Access changes re-check joined sockets as they happen; this bounds
		// how long a missed one lets the member keep editing.
		if !s.issueDocumentAccessFresh(client, documentID) {
			if allowed, err := s.recheckIssueDocument(client, documentID, issueID); err != nil || !allowed {
				return err
			}
		}
		if kind == collaborationUpdateFrame && !s.issueDocumentEditable(client, documentID) {
			// Not applied, stored or relayed; the client turns read-only.
			sendSocketJSON(client, map[string]any{"type": "document.readonly", "documentId": documentID, "issueId": issueID})
			return nil
		}
	}
	if kind == collaborationUpdateFrame && issueID == "" {
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
// description documents are authorized by their issue (issueDocumentEditable).
func (s *server) documentSocketCanEdit(r *http.Request, client *realtimeSocketClient, collaborationID string) (bool, bool, error) {
	s.realtime.mu.Lock()
	access := client.documentAccess[collaborationID]
	fresh := access != nil && time.Since(access.checkedAt) < documentAccessTTL
	canEdit := access != nil && access.canEdit
	s.realtime.mu.Unlock()
	if access == nil || fresh {
		return access == nil || canEdit, false, nil
	}
	role, conflict, err := s.socketDocumentRole(r, client.workspace, collaborationID)
	if err != nil && !errors.Is(err, errNotFound) {
		return false, false, err
	}
	allowed := err == nil && !conflict && canEditDocument(role)
	s.realtime.setEditAccess(client, collaborationID, allowed)
	return allowed, conflict || errors.Is(err, errNotFound), nil
}

// socketDocumentRole is the socket user's role on the standalone document a
// collaboration id names (errNotFound when it is gone). A credential that may
// not change documents through the API (a read-only API key) only reads it.
func (s *server) socketDocumentRole(r *http.Request, workspace, collaborationID string) (string, bool, error) {
	data, err := s.socketWorkspaceMetadata(r, workspace)
	if err != nil {
		return "", false, err
	}
	document, conflict, err := collaborationDocument(data, collaborationID)
	if err != nil {
		return "", false, err
	}
	role := documentRole(s, data, document)
	if canEditDocument(role) && !socketCredentialMayWrite(r, "/api/documents/"+document.ID) {
		role = "viewer"
	}
	return role, conflict, nil
}

// socketWorkspaceMetadata is the socket user's projection of the workspace.
func (s *server) socketWorkspaceMetadata(r *http.Request, workspace string) (domain.Bootstrap, error) {
	if s.authDisabled {
		data, ok := s.store.WorkspaceMetadata(workspace)
		if !ok {
			return data, store.ErrAuthForbidden
		}
		return data, nil
	}
	data, err := s.store.PagedWorkspaceMetadata(r.Context(), workspace, authUser(r).ID)
	if err != nil {
		return data, err
	}
	filterBootstrapForAPIKey(&data, r)
	return data, nil
}

// socketIssue reads an issue as the socket user may see it (visible false:
// gone, or outside the user's access) and whether the user may edit its
// description. canEdit is the rule that authorizes saving the description
// through PATCH /api/issues/{id}: the same authorization data (the viewer's
// projection with the issue and its ancestors), an editor or owner issue role
// (issueRole: team membership, issue permissions inherited from parents,
// workspace admin, creator) and a credential allowed to write issues.
// Access checks run on other goroutines, so each reads through its own copy
// of the socket's request.
func (s *server) socketIssue(r *http.Request, issueID string) (domain.Issue, bool, bool, error) {
	// The socket lives on; never reuse an access evaluation cached on its
	// request (requestIssueQueryAccess caches per request for other paths).
	request := r.Clone(context.WithValue(r.Context(), issueRequestAccessKey{}, struct{}{}))
	path := *r.URL
	path.Path, path.RawPath = "/api/issue-records/"+issueID, ""
	request.URL = &path
	data, err := s.issueRecordAuthorizationData(request)
	if err != nil {
		return domain.Issue{}, false, false, err
	}
	index := slices.IndexFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID })
	if index < 0 {
		return domain.Issue{}, false, false, nil
	}
	issue := data.Issues[index]
	canEdit := s.authDisabled || issuePermissionRank(issueRole(s, data, issue)) >= issuePermissionRank("editor") && socketCredentialMayWrite(r, path.Path)
	return issue, true, canEdit, nil
}

// socketCredentialMayWrite reports whether the socket's credential may make
// a PATCH to path through the API: sessions always may; an API key needs a
// scope that allows it (apiKeyAllowsRequest).
func socketCredentialMayWrite(r *http.Request, path string) bool {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok {
		return true
	}
	request := r.Clone(r.Context())
	target := *r.URL
	target.Path, target.RawPath, target.RawQuery = path, "", ""
	request.URL, request.Method = &target, http.MethodPatch
	return apiKeyAllowsRequest(request, key)
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
	if event.Type == accountAccessSignal {
		if workspace == accountAccessWorkspace {
			s.receiveAccountAccessSignal(event)
		}
		return true
	}
	if workspace == accountAccessWorkspace {
		return true
	}
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
