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
}

type realtimeSocketClient struct {
	id        uint64
	clientID  string
	workspace string
	documents map[string]struct{}
	send      chan realtimeSocketMessage
	cancel    context.CancelFunc
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
}

func (h *realtimeHub) addSocket(workspace, clientID string, cancel context.CancelFunc) *realtimeSocketClient {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	client := &realtimeSocketClient{id: h.nextID, clientID: clientID, workspace: workspace, documents: map[string]struct{}{}, send: make(chan realtimeSocketMessage, 256), cancel: cancel}
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
		copyOfMessage := realtimeSocketMessage{binary: message.binary, data: slices.Clone(message.data)}
		select {
		case client.send <- copyOfMessage:
		default:
			client.cancel()
		}
	}
}

func (s *server) realtimeSocket(w http.ResponseWriter, r *http.Request) {
	workspace, clientID := workspaceKey(r), strings.TrimSpace(r.URL.Query().Get("clientId"))
	if workspace == "" || clientID == "" || len(clientID) > 128 {
		writeError(w, http.StatusBadRequest, "workspace and clientId are required")
		return
	}
	if _, ok := s.store.BootstrapFor(workspace); !ok {
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
				messageType := websocket.MessageText
				if message.binary {
					messageType = websocket.MessageBinary
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 5*time.Second)
				err := connection.Write(writeCtx, messageType, message.data)
				writeCancel()
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
			if err := s.handleCollaborationFrame(r.Context(), client, message); err != nil {
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
	data := s.workspaceData(r)
	var contentState string
	if command.IssueID != "" {
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
			return errors.New("document does not belong to issue")
		}
	} else {
		// Standalone workspace documents use the same collaboration protocol as
		// issue descriptions, but are authorized by the document's team scope.
		index := slices.IndexFunc(data.Documents, func(document domain.Document) bool {
			return document.ID == command.DocumentID || document.SlugID == command.DocumentID
		})
		if index < 0 {
			return errors.New("document is outside your teams")
		}
		document := data.Documents[index]
		if !s.authDisabled && len(document.TeamIDs) > 0 && !slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool {
			return member.UserID == data.Viewer.ID && slices.Contains(document.TeamIDs, member.TeamID)
		}) {
			return errors.New("document is outside your teams")
		}
		contentState = document.ContentState
	}
	updates, err := s.store.DocumentCollaborationUpdates(r.Context(), client.workspace, command.DocumentID)
	if err != nil {
		return fmt.Errorf("load collaboration updates: %w", err)
	}
	message := collaborationSyncMessage{Type: "document.sync", DocumentID: command.DocumentID, Updates: make([]collaborationSyncUpdate, 0, len(updates))}
	message.ContentState = contentState
	for _, update := range updates {
		message.Updates = append(message.Updates, collaborationSyncUpdate{ID: update.ID, Data: base64.StdEncoding.EncodeToString(update.Data)})
	}
	s.realtime.joinDocument(client, command.DocumentID)
	sendSocketJSON(client, message)
	return nil
}

func (s *server) handleCollaborationFrame(ctx context.Context, client *realtimeSocketClient, raw []byte) error {
	kind, documentID, requestedUpdateID, payload, err := decodeCollaborationFrame(raw)
	if err != nil {
		return err
	}
	if !s.realtime.joinedDocument(client, documentID) {
		return errors.New("join document before sending updates")
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
	select {
	case client.send <- realtimeSocketMessage{data: raw}:
	default:
		client.cancel()
	}
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
