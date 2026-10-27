package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
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
	// claim, when set, is the occurrence claim this call made for its
	// append; it is settled once the call ends.
	claim *store.LoopDocumentAppend
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
	if content, replaces := args["content"].(string); !appendOnly && replaces && strings.TrimSpace(document.Content) != "" && strings.HasPrefix(content, document.Content) {
		// The whole body with text added at the end is an append too.
		text, appendOnly = content[len(document.Content):], true
	}
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
	if rec.run.Occurrence == "" || rec.run.LoopID == "" {
		return prepared, nil
	}
	return prepared, s.claimLoopOccurrenceAppend(rec, prepared, document, text, op)
}

// loopRunOccurrence is the schedule occurrence a run of a scheduled loop is
// for (LoopRun.Occurrence): the scheduled time of a schedule trigger, or now
// for a manual run, as a date in the loop's timezone, with the hour for
// hourly schedules. Event runs have none.
func loopRunOccurrence(loop domain.Loop, trigger loopTrigger, now time.Time) string {
	if loop.TriggerType != "schedule" || trigger.Kind == "event" {
		return ""
	}
	config := normalizeLoopTriggerConfig("schedule", loop.TriggerConfig)
	at := now
	if !trigger.ScheduledAt.IsZero() {
		at = trigger.ScheduledAt
	}
	at = at.In(loopScheduleLocation(config))
	if unit, _ := config["unit"].(string); unit == "hour" {
		return at.Format("2006-01-02T15")
	}
	return at.Format("2006-01-02")
}

// loopScheduleLocation is the timezone of a normalized schedule (UTC when it
// names none or an unknown one).
func loopScheduleLocation(config map[string]any) *time.Location {
	if name, ok := config["timezone"].(string); ok && name != "" {
		if loaded, err := time.LoadLocation(name); err == nil {
			return loaded
		}
	}
	return time.UTC
}

// loopToday is the prompt's date: in the loop's timezone for scheduled loops.
func loopToday(loop domain.Loop, now time.Time) string {
	if loop.TriggerType != "schedule" {
		return now.UTC().Format("Monday, 2006-01-02")
	}
	location := loopScheduleLocation(normalizeLoopTriggerConfig("schedule", loop.TriggerConfig))
	return now.In(location).Format("Monday, 2006-01-02") + " (" + location.String() + ")"
}

// loopAppendProbeBytes is how much of an append's text a claim keeps to find
// it in the document later.
const loopAppendProbeBytes = 512

func loopAppendProbe(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > loopAppendProbeBytes {
		text = strings.ToValidUTF8(text[:loopAppendProbeBytes], "")
	}
	return text
}

// claimLoopOccurrenceAppend makes sure the loop appends to the document once
// per occurrence, across runs and servers: the append claims (loop,
// occurrence, document). When an earlier run holds the claim and appended
// (or is appending now), the call is refused with an error that says the
// occurrence's section exists, and the record names that run so acceptance
// counts the existing append as the run's output. A claim whose run ended
// without appending is taken over.
func (s *server) claimLoopOccurrenceAppend(rec *loopRunRecorder, prepared *loopDocumentWrite, document domain.Document, text, op string) error {
	record := prepared.record
	record.Occurrence = rec.run.Occurrence
	claim := store.LoopDocumentAppend{LoopID: rec.run.LoopID, Occurrence: rec.run.Occurrence, DocumentID: document.ID, RunID: rec.run.ID, TextKey: record.Key, TextProbe: loopAppendProbe(text), VersionBefore: document.Version}
	ctx := context.Background()
	for attempt := 0; attempt < 3; attempt++ {
		existing, claimed, err := s.store.ClaimLoopDocumentAppend(ctx, rec.workspace, claim)
		if err != nil {
			return fmt.Errorf("Could not check whether this loop already appended to %q for %s (%v); the text was not appended. Try the call again", document.Title, claim.Occurrence, err)
		}
		if claimed {
			prepared.claim = &claim
			return nil
		}
		if existing.RunID == rec.run.ID {
			// This run's own append for the occurrence (another section, or
			// a reply on the run): the checks above cover repeats.
			return nil
		}
		appended, running := s.loopOccurrenceAppendState(rec.workspace, existing, document)
		if appended || running {
			record.ExistingRunID = existing.RunID
			if running {
				return fmt.Errorf("Another run of this loop (%s) is appending this loop's section for %s to %q right now, so this run did not append it again. Do not append it; finish the run and say the section for %s is written by that run", existing.RunID, claim.Occurrence, document.Title, claim.Occurrence)
			}
			next := "The instructions ask to append, not to rewrite: do not append or change that section; finish the run and say the section for " + claim.Occurrence + " already exists"
			if op == loopOpUpdate {
				next = "If the section needs changes, update it with a patch that replaces its text (replace or replace_range) instead of appending"
			}
			return fmt.Errorf("This loop already appended its section for %s to %q (run %s), so it was not appended again: the document keeps one section per date. %s", claim.Occurrence, document.Title, existing.RunID, next)
		}
		// The claiming run ended without appending: this run takes over.
		if ok, err := s.store.TakeOverLoopDocumentAppend(ctx, rec.workspace, claim, existing.RunID); err == nil && ok {
			prepared.claim = &claim
			return nil
		}
	}
	return fmt.Errorf("Could not check whether this loop already appended to %q for %s; the text was not appended. Try the call again", document.Title, claim.Occurrence)
}

// loopOccurrenceAppendState reads another run's claim: whether its append is
// in the document, or whether that run is still working (and may append).
// A claim left pending by a run that ended (interrupted, or its result was
// lost) counts as appended when the document shows the claimed text.
func (s *server) loopOccurrenceAppendState(workspace string, existing store.LoopDocumentAppend, document domain.Document) (appended, running bool) {
	if existing.Status == store.LoopAppendAppended {
		return true, false
	}
	if existing.TextProbe != "" && document.Version > existing.VersionBefore && strings.Contains(document.Content, existing.TextProbe) {
		if err := s.store.SettleLoopDocumentAppend(context.Background(), workspace, existing, true, document.Version); err != nil {
			log.Printf("Loop document append settle workspace=%s run=%s: %v", workspace, existing.RunID, err)
		}
		return true, false
	}
	run, err := s.store.LoopRun(context.Background(), workspace, existing.RunID)
	if err != nil {
		return false, false
	}
	busy := run.Status == "running"
	for _, reply := range run.Replies {
		busy = busy || reply.Status == "running"
	}
	// A run whose server stopped heartbeating is about to be interrupted.
	stale := run.HeartbeatAt != nil && time.Since(*run.HeartbeatAt) > s.loopLimits().LeaseTTL
	return false, busy && !stale
}

// settleLoopDocumentClaim records the outcome of an append that claimed its
// occurrence: appended when the stored document changed, otherwise the claim
// is released so a later run of the occurrence can append.
func (s *server) settleLoopDocumentClaim(workspace string, prepared *loopDocumentWrite, callErr error) {
	if prepared.claim == nil {
		return
	}
	changed, version := prepared.record.Changed, prepared.record.VersionAfter
	if callErr != nil {
		// The call failed; it may still have written the document.
		if document, ok := s.loopFindDocument(workspace, prepared.claim.DocumentID); ok && document.Content != prepared.content {
			changed, version = true, document.Version
		}
	}
	if err := s.store.SettleLoopDocumentAppend(context.Background(), workspace, *prepared.claim, changed, version); err != nil {
		log.Printf("Loop document append settle workspace=%s run=%s: %v", workspace, prepared.claim.RunID, err)
	}
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
		if call.Document != nil && call.Document.ExistingRunID != "" && op != loopOpCreate {
			// An earlier run already appended this occurrence's section.
			return true
		}
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
