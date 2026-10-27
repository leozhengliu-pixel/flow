package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// Loop document writes: a run's save_document calls are checked against the
// stored document before and after, so that an append keeps the existing
// content, a repeated append (a retry, or a continuation of the run) is not
// written twice, and acceptance counts only writes that changed the document.

// loopDocumentWrite is a save_document call being prepared.
type loopDocumentWrite struct {
	record *domain.LoopRunDocumentWrite
	// applied, when set, means the call is not executed: the write already
	// happened in this run, and this is what the model is told.
	applied string
	// content and title are the document's before the write.
	content, title string
}

// loopDocumentAppendKey identifies an append within a run: the document and
// the text appended. A run is one attempt; a later run appends again.
func loopDocumentAppendKey(id, text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return id + ":" + hex.EncodeToString(sum[:8])
}

// loopAppendText is the text a save_document patch appends when every patch
// operation is an append.
func loopAppendText(args map[string]any) (string, bool) {
	patches, ok := args["patch"].([]any)
	if !ok || len(patches) == 0 {
		return "", false
	}
	var text strings.Builder
	for _, raw := range patches {
		patch, _ := raw.(map[string]any)
		if op, _ := patch["op"].(string); op != "append" {
			return "", false
		}
		value, _ := patch["text"].(string)
		text.WriteString(value)
	}
	return text.String(), true
}

func (s *server) loopFindDocument(workspace, ref string) (domain.Document, bool) {
	data, ok := s.store.WorkspaceMetadataFields(workspace, "documents")
	if !ok {
		return domain.Document{}, false
	}
	document, err := mcpFindDocument(data, ref)
	return document, err == nil
}

// loopPriorDocumentWrites are the document writes the run and its replies
// recorded before this call.
func loopPriorDocumentWrites(rec *loopRunRecorder) []domain.LoopRunToolCall {
	calls := slices.Clone(rec.run.ToolCalls)
	for _, reply := range rec.run.Replies {
		if rec.reply == nil || reply.ID != rec.reply.ID {
			calls = append(calls, reply.ToolCalls...)
		}
	}
	if rec.reply != nil {
		calls = append(calls, rec.reply.ToolCalls...)
	}
	return slices.DeleteFunc(calls, func(call domain.LoopRunToolCall) bool { return call.Document == nil })
}

// prepareLoopDocumentWrite checks a save_document call before it runs. A loop
// that appends (or updates an existing document) may not create a new one
// instead, nor replace the content it appends to; an append this run already
// made is not made again, including one whose call failed after the
// document was written.
func (s *server) prepareLoopDocumentWrite(rec *loopRunRecorder, expectation loopOutputExpectation, call domain.AgentToolCall) (*loopDocumentWrite, error) {
	args := loopToolArgs(call)
	op := ""
	if !expectation.Explicit && slices.Contains(expectation.Outputs, "document") {
		op = expectation.Operations["document"]
	}
	ref := loopStringArg(args, "id")
	if ref == "" {
		if op == loopOpAppend || op == loopOpUpdate {
			return nil, fmt.Errorf("This loop writes to an existing document: find it with search_documents or list_documents and call save_document with its id instead of creating a new document")
		}
		return &loopDocumentWrite{record: &domain.LoopRunDocumentWrite{Mode: loopOpCreate}}, nil
	}
	document, found := s.loopFindDocument(rec.workspace, ref)
	if !found {
		// The tool reports the missing document.
		return &loopDocumentWrite{record: &domain.LoopRunDocumentWrite{Mode: loopOpUpdate}}, nil
	}
	prepared := &loopDocumentWrite{record: &domain.LoopRunDocumentWrite{ID: document.ID, Title: document.Title, Mode: loopOpUpdate, VersionBefore: document.Version}, content: document.Content, title: document.Title}
	text, appendOnly := loopAppendText(args)
	if appendOnly {
		prepared.record.Mode, prepared.record.Key = loopOpAppend, loopDocumentAppendKey(document.ID, text)
	}
	if content, replaces := args["content"].(string); op == loopOpAppend && replaces && !strings.Contains(content, strings.TrimSpace(document.Content)) {
		return nil, fmt.Errorf("This loop appends to %q: call save_document with patch [{\"op\":\"append\",\"text\":\"...\"}] so its existing content stays; content replaces the whole document", document.Title)
	}
	if prepared.record.Key == "" || strings.TrimSpace(text) == "" {
		return prepared, nil
	}
	for _, prior := range loopPriorDocumentWrites(rec) {
		if prior.Document.Key != prepared.record.Key {
			continue
		}
		if prior.Status == "completed" && prior.Document.Changed {
			prepared.applied = fmt.Sprintf("This text was already appended to %q earlier in this run; it was not appended again. Continue with the steps that are not done yet.", document.Title)
			return prepared, nil
		}
		if prior.Status != "completed" && document.Version > prior.Document.VersionBefore && strings.Contains(document.Content, strings.TrimSpace(text)) {
			// The earlier call wrote the document but its result was lost.
			prepared.record.Changed, prepared.record.VersionAfter = true, document.Version
			prepared.applied = fmt.Sprintf("An earlier attempt in this run already appended this text to %q (its call reported an error after the write); it was not appended again.", document.Title)
			return prepared, nil
		}
	}
	return prepared, nil
}

// appliedResult is the tool result of a write that was not repeated.
func (prepared *loopDocumentWrite) appliedResult() []byte {
	raw, _ := json.Marshal(map[string]any{"ok": true, "alreadyApplied": true, "id": prepared.record.ID, "title": prepared.record.Title, "message": prepared.applied})
	return raw
}

// verify records what the write did to the stored document.
func (s *server) verifyLoopDocumentWrite(workspace string, prepared *loopDocumentWrite, result []byte) {
	record := prepared.record
	if record.Mode == loopOpCreate {
		var receipt struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(result, &receipt) != nil || receipt.ID == "" {
			return
		}
		if document, ok := s.loopFindDocument(workspace, receipt.ID); ok {
			record.ID, record.Title, record.VersionAfter, record.Changed = document.ID, document.Title, document.Version, true
		}
		return
	}
	if record.ID == "" {
		return
	}
	if document, ok := s.loopFindDocument(workspace, record.ID); ok {
		record.Title, record.VersionAfter = document.Title, document.Version
		record.Changed = document.Content != prepared.content || document.Title != prepared.title
	}
}

// loopDocumentOutputDone reports whether the run made the document write its
// loop asks for, as the stored document shows it.
func loopDocumentOutputDone(run *domain.LoopRun, op string) bool {
	for _, call := range run.ToolCalls {
		if call.Status != "completed" || call.Document == nil || !call.Document.Changed {
			continue
		}
		switch op {
		case loopOpAppend, loopOpUpdate:
			if call.Document.Mode != loopOpCreate {
				return true
			}
		case loopOpCreate:
			if call.Document.Mode == loopOpCreate {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// loopDocumentMissingNote explains a missing document write.
func loopDocumentMissingNote(run *domain.LoopRun, op string) string {
	created, unchanged := false, false
	for _, call := range run.ToolCalls {
		if call.Status != "completed" || call.Document == nil {
			continue
		}
		created = created || call.Document.Changed && call.Document.Mode == loopOpCreate
		unchanged = unchanged || !call.Document.Changed && call.Document.Mode != loopOpCreate
	}
	switch {
	case created && (op == loopOpAppend || op == loopOpUpdate):
		return " (the run created a new document instead of changing the existing one)"
	case unchanged:
		return " (save_document ran but did not change the document)"
	}
	return ""
}

// loopDocumentTool reports whether a tool changes a document named by id.
func loopDocumentTool(name string) bool {
	return slices.Contains([]string{"save_document", "delete_document", "restore_document", "restore_document_version", "save_document_permissions"}, strings.TrimPrefix(name, "mcp__flow."))
}
