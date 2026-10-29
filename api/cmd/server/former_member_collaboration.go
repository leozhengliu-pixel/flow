package main

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"flow/api/internal/domain"

	"github.com/Deln0r/ygo"
)

// Inspect the resulting tree, not raw bytes: attributes can arrive in separate
// updates, and deleted mentions in the CRDT history are not new mentions.
func collaborationMentionDocument(doc *ygo.Doc) map[string]any {
	var children func(interface{ Range(func(uint64, any) bool) }) []any
	children = func(parent interface{ Range(func(uint64, any) bool) }) []any {
		result := []any{}
		parent.Range(func(_ uint64, child any) bool {
			if element, ok := child.(*ygo.XmlElement); ok {
				attrs := map[string]any{}
				for key, value := range element.Attributes() {
					attrs[key] = value
				}
				result = append(result, map[string]any{"type": element.NodeName(), "attrs": attrs, "content": children(element)})
			}
			return true
		})
		return result
	}
	return map[string]any{"type": "doc", "content": children(ygo.NewXmlFragment(doc, "prosemirror"))}
}

func applyCollaborationState(doc *ygo.Doc, state string) error {
	if state == "" {
		return nil
	}
	update, err := base64.StdEncoding.DecodeString(state)
	if err != nil {
		return fmt.Errorf("%w: invalid collaboration state", errInvalid)
	}
	return applyMentionUpdate(doc, update)
}

func applyMentionUpdate(doc *ygo.Doc, update []byte) (err error) {
	// The decoder handles untrusted client input; malformed updates must not
	// take down the request goroutine even if the CRDT library panics.
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("%w: invalid collaboration update", errInvalid)
		}
	}()
	if err := ygo.ApplyUpdate(doc, update); err != nil {
		return fmt.Errorf("%w: invalid collaboration update", errInvalid)
	}
	return nil
}

func refuseFormerMemberStateMentions(data *domain.Bootstrap, beforeData any, beforeState, afterState string, documentID string) error {
	if afterState == "" || data.FormerMembers == nil || len(data.FormerMembers()) == 0 {
		return nil
	}
	after := ygo.NewDoc()
	if err := applyCollaborationState(after, afterState); err != nil {
		return err
	}
	return refuseFormerMemberRichMentions(data, beforeData, beforeState, collaborationMentionDocument(after), documentID)
}

func refuseFormerMemberRichMentions(data *domain.Bootstrap, beforeData any, beforeState string, after any, documentID string) error {
	mentioned := []string{}
	collectMentionIDs(after, &mentioned)
	if len(mentioned) == 0 {
		return nil
	}
	if data.FormerMembers == nil || len(data.FormerMembers()) == 0 {
		return nil
	}
	before := ygo.NewDoc()
	// An unreadable legacy state is no evidence of a retained mention, but
	// must not prevent replacing that state with valid content.
	if applyCollaborationState(before, beforeState) != nil {
		before = ygo.NewDoc()
	}
	if data.CollaborationUpdates != nil && documentID != "" {
		updates, err := data.CollaborationUpdates(documentID)
		if err != nil {
			return err
		}
		for _, update := range updates {
			if err := applyMentionUpdate(before, update); err != nil {
				return err
			}
		}
	}
	beforeData = []any{beforeData, collaborationMentionDocument(before)}
	return refuseNewFormerMemberMentions(data, beforeData, after)
}

func (s *server) refuseFormerMemberCollaborationUpdate(r *http.Request, client *realtimeSocketClient, documentID string, update []byte) error {
	former, err := s.store.FormerMembers(r.Context(), client.workspace)
	if err != nil || len(former) == 0 {
		return err
	}
	data := domain.Bootstrap{FormerMembers: func() map[string]bool { return former }}
	s.realtime.mu.Lock()
	issueID := client.issueDocuments[documentID]
	s.realtime.mu.Unlock()
	state := ""
	var content any
	if issueID != "" {
		issue, err := s.store.IssueRecord(r.Context(), client.workspace, issueID)
		if err != nil {
			return err
		}
		if issue.DocumentContent != nil {
			state, content = issue.DocumentContent.ContentState, issue.DocumentContent.ContentData
		}
	} else {
		workspace, err := s.socketWorkspaceMetadata(r, client.workspace)
		if err != nil {
			return err
		}
		document, _, err := collaborationDocument(workspace, documentID)
		if err != nil {
			return err
		}
		state, content = document.ContentState, document.ContentData
	}
	doc := ygo.NewDoc()
	if err := applyCollaborationState(doc, state); err != nil {
		return err
	}
	updates, err := s.store.DocumentCollaborationUpdates(r.Context(), client.workspace, documentID)
	if err != nil {
		return err
	}
	for _, pending := range updates {
		if err := applyMentionUpdate(doc, pending.Data); err != nil {
			return err
		}
	}
	before := []any{content, collaborationMentionDocument(doc)}
	if err := applyMentionUpdate(doc, update); err != nil {
		return err
	}
	return refuseNewFormerMemberMentions(&data, before, collaborationMentionDocument(doc))
}
