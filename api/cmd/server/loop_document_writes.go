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
	// arguments, when set, replace the call's: the contract's title for the
	// run and its team.
	arguments json.RawMessage
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

// prepareLoopDocumentWrite checks a save_document call against the run's
// output contract before it runs: a new document only when the contract
// creates one (with the contract's title for this run and its team), a
// change only to a document the contract targets or this run created — a
// loop that creates a new document each run changes no other document. An
// append may not replace the content it appends to; an append this run
// already made is not made again, including one whose call failed after the
// document was written. A reply on the run page (rec.answering) is not held
// to the contract.
func (s *server) prepareLoopDocumentWrite(rec *loopRunRecorder, data domain.Bootstrap, contract *domain.LoopOutputContract, call domain.AgentToolCall) (*loopDocumentWrite, error) {
	args := loopToolArgs(call)
	gate := contract != nil && !rec.answering
	ref := loopStringArg(args, "id")
	if ref == "" {
		prepared := &loopDocumentWrite{record: &domain.LoopRunDocumentWrite{Mode: loopOpCreate}}
		if gate {
			_, next, err := loopContractCreate(rec, data, contract, args)
			if err != nil {
				return nil, err
			}
			if !sameJSON(next, args) {
				prepared.arguments, _ = json.Marshal(next)
			}
		}
		return prepared, nil
	}
	document, found := s.loopFindDocument(rec.workspace, ref)
	if !found {
		// The tool reports the missing document.
		return &loopDocumentWrite{record: &domain.LoopRunDocumentWrite{Mode: loopOpUpdate}}, nil
	}
	op := ""
	if gate {
		output, err := loopContractDocumentTarget(rec, contract, document)
		if err != nil {
			return nil, err
		}
		if output != nil {
			op = output.Operation
		}
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
// for (LoopRun.Occurrence), the key of its once-per-occurrence appends. It
// follows the schedule's period, in the loop's timezone: the hour for hourly
// schedules ("2026-10-27T09"), the date for daily ones ("2026-10-27"), the
// ISO week for weekly ones ("2026-W44") and the month for monthly ones
// ("2026-10"). A schedule trigger is for its scheduled time. A manual run of
// a daily or hourly loop is for now; of a weekly or monthly loop, for the
// period of the most recent scheduled occurrence (a Friday report run again
// on Monday is still that week's), or now's before the first one. Event runs
// have none.
func loopRunOccurrence(loop domain.Loop, trigger loopTrigger, now time.Time) string {
	if loop.TriggerType != "schedule" || trigger.Kind == "event" {
		return ""
	}
	config := normalizeLoopTriggerConfig("schedule", loop.TriggerConfig)
	unit, _ := config["unit"].(string)
	at := now
	switch {
	case !trigger.ScheduledAt.IsZero():
		at = trigger.ScheduledAt
	case unit == "week" || unit == "month":
		if previous, ok := previousLoopRun(loop.TriggerConfig, loop.CreatedAt, now); ok {
			at = previous
		}
	}
	at = at.In(loopScheduleLocation(config))
	switch unit {
	case "hour":
		return at.Format("2006-01-02T15")
	case "week":
		year, week := at.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	case "month":
		return at.Format("2006-01")
	}
	return at.Format("2006-01-02")
}

// previousLoopRun is the most recent scheduled time at or before now, if the
// schedule had one in the last period: occurrences are found with
// nextLoopRun from one period (plus a week of slack) back.
func previousLoopRun(config map[string]any, created, now time.Time) (time.Time, bool) {
	normalized := normalizeLoopTriggerConfig("schedule", config)
	interval := loopScheduleInterval(normalized)
	span := time.Duration(7*interval+7) * 24 * time.Hour
	if unit, _ := normalized["unit"].(string); unit == "month" {
		span = time.Duration(31*interval+7) * 24 * time.Hour
	}
	var previous time.Time
	probe := now.Add(-span)
	for range 1000 {
		next := nextLoopRun(config, created, probe)
		if next.After(now) || !next.After(probe) {
			break
		}
		previous, probe = next, next
	}
	return previous, !previous.IsZero()
}

// loopOccurrenceLabel names an occurrence for the run's prompt and errors:
// a week with its dates ("week 2026-W44, 2026-10-26 to 2026-11-01"), a month
// ("month 2026-10"); dates and hours as they are.
func loopOccurrenceLabel(occurrence string) string {
	var year, week int
	if n, err := fmt.Sscanf(occurrence, "%d-W%d", &year, &week); err == nil && n == 2 && len(occurrence) == len("2026-W44") {
		// ISO week 1 holds January 4; its Monday starts the year's weeks.
		january4 := time.Date(year, time.January, 4, 0, 0, 0, 0, time.UTC)
		monday := january4.AddDate(0, 0, -((int(january4.Weekday())+6)%7)+7*(week-1))
		return fmt.Sprintf("week %s, %s to %s", occurrence, monday.Format("2006-01-02"), monday.AddDate(0, 0, 6).Format("2006-01-02"))
	}
	if len(occurrence) == len("2026-10") {
		return "month " + occurrence
	}
	return occurrence
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
	label := loopOccurrenceLabel(claim.Occurrence)
	ctx := context.Background()
	for attempt := 0; attempt < 3; attempt++ {
		existing, claimed, err := s.store.ClaimLoopDocumentAppend(ctx, rec.workspace, claim)
		if err != nil {
			return fmt.Errorf("Could not check whether this loop already appended to %q for %s (%w); the text was not appended. Try the call again", document.Title, label, err)
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
				return fmt.Errorf("Another run of this loop (%s) is appending this loop's section for %s to %q right now, so this run did not append it again. Do not append it; finish the run and say the section for %s is written by that run", existing.RunID, label, document.Title, label)
			}
			next := "The instructions ask to append, not to rewrite: do not append or change that section; finish the run and say the section for " + label + " already exists"
			if op == loopOpUpdate {
				next = "If the section needs changes, update it with a patch that replaces its text (replace or replace_range) instead of appending"
			}
			return fmt.Errorf("This loop already appended its section for %s to %q (run %s), so it was not appended again: the document keeps one section per occurrence. %s", label, document.Title, existing.RunID, next)
		}
		// The claiming run ended without appending: this run takes over.
		if ok, err := s.store.TakeOverLoopDocumentAppend(ctx, rec.workspace, claim, existing.RunID); err == nil && ok {
			prepared.claim = &claim
			return nil
		}
	}
	return fmt.Errorf("Could not check whether this loop already appended to %q for %s; the text was not appended. Try the call again", document.Title, label)
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
			record.TeamIDs = slices.Clone(document.TeamIDs)
		}
		return
	}
	if record.ID == "" {
		return
	}
	if document, ok := s.loopFindDocument(workspace, record.ID); ok {
		record.Title, record.VersionAfter, record.TeamIDs = document.Title, document.Version, slices.Clone(document.TeamIDs)
		record.Changed = document.Content != prepared.content || document.Title != prepared.title
	}
}

// loopDocumentMissingNote explains a missing document output.
func loopDocumentMissingNote(run *domain.LoopRun, output domain.LoopContractOutput) string {
	created, unchanged, misnamed := false, false, ""
	for _, call := range run.ToolCalls {
		if call.Status != "completed" || call.Document == nil {
			continue
		}
		if call.Document.Changed && call.Document.Mode == loopOpCreate {
			created = true
			if rendered := output.Target.RenderedTitle; rendered != "" && !loopTitleMatches(rendered, call.Document.Title) {
				misnamed = call.Document.Title
			} else if teamID := output.Target.TeamID; teamID != "" && !slices.Contains(call.Document.TeamIDs, teamID) {
				misnamed = call.Document.Title
			}
		}
		unchanged = unchanged || !call.Document.Changed && call.Document.Mode != loopOpCreate
	}
	switch {
	case created && (output.Operation == loopOpAppend || output.Operation == loopOpUpdate):
		return " (the run created a new document instead of changing the existing one)"
	case output.Operation == loopOpCreate && misnamed != "":
		return fmt.Sprintf(" (the run created %q, which does not have the contract's title or team)", misnamed)
	case unchanged:
		return " (save_document ran but did not change the document)"
	}
	return ""
}

// loopDocumentTool reports whether a tool changes a document named by id.
func loopDocumentTool(name string) bool {
	return slices.Contains([]string{"save_document", "delete_document", "restore_document", "restore_document_version", "save_document_permissions"}, strings.TrimPrefix(name, "mcp__flow."))
}
