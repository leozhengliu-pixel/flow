package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	syncV2FrameBytes = 16 << 10
	syncV2DeltaBytes = 256 << 10
)

func syncV2Enabled() bool {
	return strings.TrimSpace(os.Getenv("FLOW_SYNC_V2")) == "" || syncFlagEnabled("FLOW_SYNC_V2")
}

type committedChangeReader interface {
	ReadCommittedChanges(ctx context.Context, workspaceID, after string, limit, maxBytes int) (store.CommittedPage, error)
}

func (s *server) syncV2Journal() (committedChangeReader, bool) {
	journal, ok := any(s.store).(committedChangeReader)
	return journal, ok && syncV2Enabled() && store.RecordJournalEnabled() && s.store.RecordJournalReady()
}

func syncV2SummaryRequest(r *http.Request) bool {
	return syncV2Enabled() && (r.Header.Get("X-Flow-Sync") == "summary" || r.URL.Query().Get("sync") == "2")
}

func (s *server) negotiatedSummaryRequest(r *http.Request) bool {
	_, ready := s.syncV2Journal()
	return ready && syncV2SummaryRequest(r)
}

type syncV2Key struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type syncV2Upsert struct {
	Key         syncV2Key       `json:"key"`
	Version     string          `json:"version"`
	Data        json.RawMessage `json:"data"`
	Projection  string          `json:"projection"`
	DetailStale bool            `json:"detailStale"`
}

type syncV2Removal struct {
	Key     syncV2Key `json:"key"`
	Version string    `json:"version"`
}

type syncV2Transaction struct {
	TxID          string          `json:"txId"`
	Seq           string          `json:"seq"`
	Upserts       []syncV2Upsert  `json:"upserts"`
	Removals      []syncV2Removal `json:"removals"`
	Invalidations []string        `json:"invalidations"`
}

type syncV2ChangesResponse struct {
	Next           string              `json:"next"`
	Head           string              `json:"head"`
	AuthzEpoch     string              `json:"authzEpoch"`
	Transactions   []syncV2Transaction `json:"transactions"`
	HasMore        bool                `json:"hasMore"`
	ResyncRequired bool                `json:"resyncRequired"`
}

func emptySyncTransaction(id, seq string) syncV2Transaction {
	return syncV2Transaction{TxID: id, Seq: seq, Upserts: []syncV2Upsert{}, Removals: []syncV2Removal{}, Invalidations: []string{}}
}

func syncCollection(eventType string) string {
	switch {
	case strings.HasPrefix(eventType, "issue."), strings.HasPrefix(eventType, "comment."), strings.HasPrefix(eventType, "attachment."):
		return "issues"
	case strings.HasPrefix(eventType, "document."):
		return "documents"
	case strings.HasPrefix(eventType, "project."), strings.HasPrefix(eventType, "project_update."):
		return "projects"
	case strings.HasPrefix(eventType, "initiative."), strings.HasPrefix(eventType, "initiative_update."):
		return "initiatives"
	}
	return "*"
}

// Summaries are explicit partial records. Body/history/comment/ACL fields are
// absent, so they can never clear a loaded detail when applied by a v2 client.
func syncRecordSummary(kind string, raw json.RawMessage) (json.RawMessage, string, string, error) {
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, "", "", err
	}
	var id string
	if json.Unmarshal(record["id"], &id) != nil || id == "" {
		return nil, "", "", errors.New("summary has no record identity")
	}
	fields := strings.Fields("id name title slugId previousSlugIds identifier number icon color priority priorityLabel sortOrder estimate dueDate createdAt updatedAt archivedAt team teamIds state status assignee delegate creator lead owner project projectIds issueId leadTeamId contributingTeamIds parentId parentInitiativeIds dependencyIds hasContent health progress startDate targetDate contentVersion")
	summary := map[string]json.RawMessage{}
	for _, field := range fields {
		if value, ok := record[field]; ok {
			summary[field] = value
		}
	}
	version := ""
	if value := record["version"]; len(value) > 0 {
		if json.Unmarshal(value, &version) != nil {
			var number json.Number
			if json.Unmarshal(value, &number) == nil {
				version = number.String()
			}
		}
		if version != "" {
			summary["version"], _ = json.Marshal(version)
		}
	}
	if kind == "issues" {
		summary["isSummary"] = json.RawMessage("true")
	}
	if value := record["contentVersion"]; len(value) > 0 {
		var number json.Number
		if json.Unmarshal(value, &number) == nil {
			summary["contentVersion"], _ = json.Marshal(number.String())
		}
	}
	encoded, err := json.Marshal(summary)
	return encoded, id, version, err
}

func summarizeEventFrame(frame EventFrame, epoch string) (EventFrame, error) {
	if !frame.visible {
		return frame, nil
	}
	event, err := frame.Event()
	if err != nil {
		return EventFrame{}, err
	}
	kind := syncCollection(event.Type)
	tx := emptySyncTransaction(event.ID, frameSequence(event))
	var payload struct {
		Entity       json.RawMessage   `json:"entity"`
		Issues       []json.RawMessage `json:"issues"`
		IssueIDs     []string          `json:"issueIds"`
		Removed      []string          `json:"removedIssueIds"`
		Deleted      bool              `json:"deleted"`
		ScopeChanged bool              `json:"scopeChanged"`
	}
	_ = json.Unmarshal(event.Payload, &payload)
	entities := payload.Issues
	if len(payload.Entity) > 0 {
		entities = append(entities, payload.Entity)
	}
	for _, raw := range entities {
		data, id, version, err := syncRecordSummary(kind, raw)
		if err != nil {
			return EventFrame{}, err
		}
		if version == "" {
			version = tx.Seq
		}
		tx.Upserts = append(tx.Upserts, syncV2Upsert{Key: syncV2Key{kind, id}, Version: version, Data: data, Projection: "summary", DetailStale: true})
	}
	removed := payload.Removed
	if payload.Deleted {
		removed = append(slices.Clone(removed), payload.IssueIDs...)
	}
	for _, id := range removed {
		tx.Removals = append(tx.Removals, syncV2Removal{Key: syncV2Key{kind, id}, Version: tx.Seq})
	}
	if payload.ScopeChanged || len(tx.Upserts) == 0 && len(tx.Removals) == 0 {
		tx.Invalidations = []string{kind}
	}
	encode := func() (json.RawMessage, error) {
		payload, err := json.Marshal(struct {
			Schema      int               `json:"schema"`
			AuthzEpoch  string            `json:"authzEpoch"`
			Transaction syncV2Transaction `json:"transaction"`
		}{2, epoch, tx})
		if err != nil {
			return nil, err
		}
		// v2 sequences are strings in the negotiated payload, including values
		// larger than JavaScript's integer precision.
		return json.Marshal(domain.RealtimeEvent{ID: event.ID, Type: "sync.delta", Payload: payload, CreatedAt: event.CreatedAt})
	}
	raw, err := encode()
	if err != nil {
		return EventFrame{}, err
	}
	if len(raw)+len(event.ID)+128 > syncV2FrameBytes {
		tx.Upserts, tx.Removals, tx.Invalidations = []syncV2Upsert{}, []syncV2Removal{}, []string{kind}
		raw, err = encode()
	}
	if len(raw)+len(event.ID)+128 > syncV2FrameBytes {
		return EventFrame{}, store.ErrEventSourceBudget
	}
	return EventFrame{encoded: raw, id: event.ID, visible: true}, err
}

// syncChanges is registered by the HTTP route owner. A mixed deployment with
// no complete journal returns 404, allowing clients to use legacy changes.
func (s *server) syncChanges(w http.ResponseWriter, r *http.Request) {
	if s.scaleP20Changes(w, r) {
		return
	}
	journal, ok := s.syncV2Journal()
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.syncChangesWithJournal(w, r, journal)
}

func (s *server) syncChangesWithJournal(w http.ResponseWriter, r *http.Request, journal committedChangeReader) {
	after := r.URL.Query().Get("after")
	if _, err := strconv.ParseUint(after, 10, 64); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid change cursor")
		return
	}
	limit := defaultChangesLimit
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, maxChangesLimit)
	}
	var result syncV2ChangesResponse
	for attempt := 1; ; attempt++ {
		access, err := s.resolveSyncAccess(r)
		if err != nil {
			issueRecordsErrorFor(w, r, err)
			return
		}
		pageLimit := limit
		if attempt == syncChangesAttempts {
			// After two source races, narrow the projection window. The journal
			// keeps the next cursor and hasMore tied to the complete transaction.
			pageLimit = 1
		}
		result, err = s.collectSyncChanges(r, access, journal, after, pageLimit)
		if err == nil {
			break
		}
		if attempt == syncChangesAttempts {
			var ok bool
			result, ok = s.syncChangesResyncAfterRace(r, access, journal, after, err)
			if ok {
				break
			}
		}
		if !s.syncChangesRaced(r, access, err) {
			if attempt > 1 {
				slog.WarnContext(r.Context(), "sync changes projected again", "request_id", requestIDFrom(r.Context()), "attempts", attempt)
			}
			s.logSyncChangesFailureClass(r, access, err)
			writeSyncChangesUnavailable(w, r, err)
			return
		}
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > syncV2DeltaBytes {
		writeError(w, http.StatusServiceUnavailable, "Changes are temporarily unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Flow-Sync", "delta")
	_, _ = w.Write(raw)
}

// syncChangesResyncAfterRace converts a verified source race on the final
// attempt into an empty reset page. No projected transaction is returned after
// its source proof has been lost; the client will rebuild from the current
// journal head instead.
func (s *server) syncChangesResyncAfterRace(r *http.Request, access store.EventAccess, journal committedChangeReader, after string, err error) (syncV2ChangesResponse, bool) {
	if !s.syncChangesResetAllowed(r, access, err) {
		return syncV2ChangesResponse{}, false
	}
	page, readErr := journal.ReadCommittedChanges(r.Context(), access.WorkspaceID, after, 1, syncV2DeltaBytes)
	// A concurrent writer can leave a short journal coverage gap even after
	// the head and authorization epoch are committed. The reset discards the
	// projected page and asks the client to rebuild from that authenticated
	// head, so a coverage error is safe to absorb; other read failures are not.
	if page.Head == "" || page.AuthzEpoch == "" || page.AuthzEpoch != access.AuthzEpoch ||
		readErr != nil && !errors.Is(readErr, store.ErrRecordJournalCoverage) {
		return syncV2ChangesResponse{}, false
	}
	return syncV2ChangesResponse{
		Next:           after,
		Head:           page.Head,
		AuthzEpoch:     access.AuthzEpoch,
		Transactions:   []syncV2Transaction{},
		HasMore:        false,
		ResyncRequired: true,
	}, true
}

func (s *server) syncChangesResetAllowed(r *http.Request, access store.EventAccess, err error) bool {
	if s.syncChangesRaced(r, access, err) {
		return true
	}
	// A source-cache flight can report a live stale sentinel after its
	// authority has already settled back to the request's version. The
	// projection proof is still lost, but an empty reset is safe when the
	// caller's credential and authorization epoch remain current.
	if !errors.Is(err, errLiveEventSourceStale) || r.Context().Err() != nil || access.CheckCredential() != nil {
		return false
	}
	ctx, liveErr := s.store.LiveEventContext(r.Context())
	if liveErr != nil {
		return false
	}
	authority, liveErr := s.readSyncAuthority(ctx, workspaceKey(r))
	return liveErr == nil && authority.epoch == access.AuthzEpoch
}

// syncChangesAttempts bounds how often one changes read is projected again
// after the workspace moved under it.
const syncChangesAttempts = 3

// syncChangesRaced reports that a page failed only because the workspace's
// source version moved while it was projected (any issue or metadata write):
// the principal's credential and authorization epoch are unchanged. The page
// is then projected again from fresh access, which is what the client's retry
// would read; a revoke or epoch change still fails the read as before.
func (s *server) syncChangesRaced(r *http.Request, access store.EventAccess, err error) bool {
	if !errors.Is(err, errRealtimeProjectionStale) && !errors.Is(err, store.ErrResourceAccessChanged) || r.Context().Err() != nil || access.CheckCredential() != nil {
		return false
	}
	ctx, err := s.store.LiveEventContext(r.Context())
	if err != nil {
		return false
	}
	// Sync retries need the authority after this request's projection. The
	// realtime delivery cohort intentionally shares a short authority read,
	// which can otherwise return the pre-race value and suppress a safe retry.
	authority, err := s.readSyncAuthority(ctx, workspaceKey(r))
	return err == nil && authority.version != access.SourceVersion() && authority.epoch == access.AuthzEpoch
}

// syncChangesProjectionError distinguishes a source moving underneath a
// projection from a real credential denial. Projection code historically
// returned bare ErrAuthForbidden for both cases; only attach the stale
// sentinel when a fresh authority read proves that the source moved while the
// caller's authorization epoch and credential remain valid.
func (s *server) syncChangesProjectionError(r *http.Request, access store.EventAccess, err error) error {
	if err == nil || !errors.Is(err, store.ErrAuthForbidden) || errors.Is(err, errRealtimeProjectionStale) ||
		r.Context().Err() != nil || access.CheckCredential() != nil {
		return err
	}
	ctx, liveErr := s.store.LiveEventContext(r.Context())
	if liveErr != nil {
		return err
	}
	authority, liveErr := s.readSyncAuthority(ctx, workspaceKey(r))
	if liveErr == nil && authority.version != access.SourceVersion() && authority.epoch == access.AuthzEpoch {
		return errors.Join(err, errRealtimeProjectionStale)
	}
	return err
}

func (s *server) readSyncAuthority(ctx context.Context, workspace string) (eventAuthority, error) {
	version, epoch, err := s.store.RealtimeSourceVersionAndEpoch(ctx, workspace)
	return eventAuthority{version: version, epoch: epoch}, err
}

// logSyncChangesFailureClass records the live authority split for a failed
// projection. A stale sentinel can mean either a source-only race or an
// authorization epoch change, so the sentinel alone is not safe reset proof.
func (s *server) logSyncChangesFailureClass(r *http.Request, access store.EventAccess, projectionErr error) {
	if !errors.Is(projectionErr, errRealtimeProjectionStale) && !errors.Is(projectionErr, store.ErrAuthForbidden) {
		return
	}
	ctx, err := s.store.LiveEventContext(r.Context())
	if err != nil {
		return
	}
	authority, err := s.readSyncAuthority(ctx, workspaceKey(r))
	if err != nil {
		slog.WarnContext(r.Context(), "sync changes failure authority unavailable", "request_id", requestIDFrom(r.Context()))
		return
	}
	slog.WarnContext(r.Context(), "sync changes failure classified",
		"request_id", requestIDFrom(r.Context()),
		"source_changed", authority.version != access.SourceVersion(),
		"authz_changed", authority.epoch != access.AuthzEpoch,
	)
}

// writeSyncChangesUnavailable answers a failed changes read: a retryable busy
// error keeps its own Retry-After, every other failure is the same 503 with a
// short one (a sync client re-polls).
func writeSyncChangesUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	retryAfter, busy := retryableBusy(err)
	slog.WarnContext(r.Context(), "sync changes unavailable", "request_id", serverRequestIDFrom(r.Context()),
		"cause", requestErrorCode(err), "denied", errors.Is(err, store.ErrAuthForbidden), "stale", errors.Is(err, errRealtimeProjectionStale), "budget", errors.Is(err, store.ErrEventSourceBudget))
	if busy {
		(&metadataReadGate{}).busy(w, retryAfter)
		return
	}
	w.Header().Set("Retry-After", "2")
	writeError(w, http.StatusServiceUnavailable, "Changes are temporarily unavailable")
}

func (s *server) collectSyncChanges(r *http.Request, access store.EventAccess, journal committedChangeReader, after string, limit int) (syncV2ChangesResponse, error) {
	ctx, err := s.store.LiveEventContext(r.Context())
	if err != nil {
		return syncV2ChangesResponse{}, err
	}
	r = r.WithContext(ctx)
	page, err := journal.ReadCommittedChanges(r.Context(), access.WorkspaceID, after, limit, syncV2DeltaBytes)
	if err != nil {
		return syncV2ChangesResponse{}, err
	}
	result := syncV2ChangesResponse{Next: after, Head: page.Head, AuthzEpoch: access.AuthzEpoch, Transactions: []syncV2Transaction{}, ResyncRequired: page.ResyncRequired}
	if requested := r.URL.Query().Get("authzEpoch"); requested != "" && requested != access.AuthzEpoch {
		result.ResyncRequired = true
		return result, nil
	}
	if page.ResyncRequired || page.AuthzEpoch != access.AuthzEpoch {
		result.ResyncRequired = true
		return result, nil
	}
	project := func(ctx context.Context) (syncV2ChangesResponse, error) {
		return s.projectSyncPage(r.WithContext(ctx), access, page, result)
	}
	if journal == s.store && s.coordinator.SharedCacheSyncPageEnabled() && !s.authDisabled && cacheableSyncPage(page) {
		result, err := s.sharedSyncPage(r.Context(), access, page, after, limit, project)
		return result, s.syncChangesProjectionError(r, access, err)
	}
	result, err = project(r.Context())
	return result, s.syncChangesProjectionError(r, access, err)
}

func (s *server) projectSyncPage(r *http.Request, access store.EventAccess, page store.CommittedPage, result syncV2ChangesResponse) (syncV2ChangesResponse, error) {
	header := result
	header.Next = page.Next
	headerRaw, err := json.Marshal(header)
	if err != nil {
		return result, err
	}
	used := len(headerRaw) + 64
	for index, change := range page.Transactions {
		tx, err := s.projectCommittedChange(r, access, change)
		if err != nil {
			return result, err
		}
		visible := len(tx.Upserts) > 0 || len(tx.Removals) > 0 || len(tx.Invalidations) > 0
		var encoded []byte
		if visible {
			encoded, err = json.Marshal(tx)
			if err != nil {
				return result, err
			}
			if len(encoded)+len(headerRaw)+64 > syncV2DeltaBytes {
				tx = invalidateCommittedChange(change)
				encoded, err = json.Marshal(tx)
				if err != nil {
					return result, err
				}
			}
			if used+len(encoded)+1 > syncV2DeltaBytes {
				result.HasMore = true
				break
			}
			result.Transactions = append(result.Transactions, tx)
			used += len(encoded) + 1
		}
		result.Next = change.Seq
		if index == len(page.Transactions)-1 {
			result.Next = page.Next
			result.HasMore = page.HasMore
		}
	}
	if len(page.Transactions) == 0 {
		result.Next = page.Next
		result.HasMore = page.HasMore
	}
	// A revoke while records were queued/read cancels the complete response.
	if err := s.store.ValidateEventAccess(r.Context(), access); err != nil {
		return result, err
	}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > syncV2DeltaBytes {
		return result, store.ErrEventSourceBudget
	}
	return result, nil
}

func invalidateCommittedChange(change store.CommittedChange) syncV2Transaction {
	tx := emptySyncTransaction(change.TxID, change.Seq)
	kinds := map[string]bool{}
	for _, record := range change.Records {
		if len(record.Key.Kind) > 191 || record.Key.Kind == "" {
			kinds["*"] = true
		} else {
			kinds[record.Key.Kind] = true
		}
		if len(kinds) > 32 {
			kinds = map[string]bool{"*": true}
			break
		}
	}
	for _, kind := range change.Invalidations {
		if len(kind) > 191 || kind == "" {
			kinds["*"] = true
		} else {
			kinds[kind] = true
		}
		if len(kinds) > 32 {
			kinds = map[string]bool{"*": true}
			break
		}
	}
	for kind := range kinds {
		tx.Invalidations = append(tx.Invalidations, kind)
	}
	if len(tx.Invalidations) == 0 {
		tx.Invalidations = []string{"*"}
	}
	slices.Sort(tx.Invalidations)
	return tx
}

func (s *server) projectCommittedChange(r *http.Request, access store.EventAccess, change store.CommittedChange) (syncV2Transaction, error) {
	tx := emptySyncTransaction(change.TxID, change.Seq)
	if len(change.Records) > 50 || len(change.Invalidations) > 32 {
		return invalidateCommittedChange(change), nil
	}
	tx.Invalidations = append(tx.Invalidations, change.Invalidations...)
	seq, err := strconv.ParseInt(change.Seq, 10, 64)
	if err != nil || seq < 0 {
		return tx, store.ErrIssueQuery
	}
	ids := []string{}
	hasDeleted := false
	for _, record := range change.Records {
		if record.Key.ID == "" || len(record.Key.ID) > 191 || record.Version == "" || len(record.Version) > 191 {
			return invalidateCommittedChange(change), nil
		}
		if record.Key.Kind != "issues" {
			continue
		}
		if record.Deleted {
			hasDeleted = true
		} else {
			ids = append(ids, record.Key.ID)
		}
	}
	entities := map[string]json.RawMessage{}
	if len(ids) > 0 {
		payload, _ := json.Marshal(map[string]any{"issueIds": ids})
		event := domain.RealtimeEvent{ID: change.TxID, Seq: seq, Type: "issue.batch_updated", Payload: payload}
		source, err := s.eventSourceForAuthority(r.Context(), workspaceKey(r), event, eventAuthority{access.SourceVersion(), access.AuthzEpoch})
		if errors.Is(err, store.ErrEventSourceBudget) {
			return invalidateCommittedChange(change), nil
		}
		if err != nil {
			return tx, err
		}
		frame, err := ProjectEvent(r.Context(), source, access)
		if err != nil {
			return tx, err
		}
		defer frame.Close()
		projected, err := frame.Event()
		if err != nil {
			return tx, err
		}
		var payloadView struct {
			Issues []json.RawMessage `json:"issues"`
		}
		if err := json.Unmarshal(projected.Payload, &payloadView); err != nil {
			return tx, err
		}
		for _, raw := range payloadView.Issues {
			var identity struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &identity); err != nil {
				return tx, err
			}
			entities[identity.ID] = raw
		}
	}
	deleted := map[string]bool{}
	if hasDeleted {
		source, err := s.committedDeletionSource(r, access, change, seq)
		if errors.Is(err, errEventSourceUnsupported) || errors.Is(err, store.ErrEventSourceBudget) {
			return invalidateCommittedChange(change), nil
		}
		if err != nil {
			return tx, err
		}
		query, err := access.RealtimeQuery()
		if err != nil {
			return tx, err
		}
		for _, id := range visibleDeletedIssueIDs(source.event, query) {
			deleted[id] = true
		}
	}
	for _, record := range change.Records {
		if record.Key.Kind != "issues" {
			if record.Deleted {
				return invalidateCommittedChange(change), nil
			}
			upsert, visible, err := s.projectCommittedMetadata(r, access, change, record)
			if errors.Is(err, store.ErrEventSourceBudget) || errors.Is(err, errEventSourceUnsupported) {
				return invalidateCommittedChange(change), nil
			}
			if err != nil {
				return tx, err
			}
			if visible {
				tx.Upserts = append(tx.Upserts, upsert)
			}
			continue
		}
		if record.Deleted {
			if deleted[record.Key.ID] {
				tx.Removals = append(tx.Removals, syncV2Removal{Key: syncV2Key{record.Key.Kind, record.Key.ID}, Version: record.Version})
			}
			continue
		}
		raw, visible := entities[record.Key.ID]
		if !visible {
			continue
		}
		data, id, version, err := syncRecordSummary("issues", raw)
		if err != nil {
			return tx, err
		}
		if store.RecordJournalEnabled() && s.store.RecordJournalReady() {
			var deleted bool
			version, deleted, err = s.store.RecordVersion(r.Context(), workspaceKey(r), record.Key)
			if err != nil {
				return tx, err
			}
			if deleted {
				return invalidateCommittedChange(change), nil
			}
		}
		if version != record.Version {
			return invalidateCommittedChange(change), nil
		}
		tx.Upserts = append(tx.Upserts, syncV2Upsert{Key: syncV2Key{record.Key.Kind, id}, Version: version, Data: data, Projection: "summary", DetailStale: true})
	}
	return tx, nil
}

func (s *server) projectCommittedMetadata(r *http.Request, access store.EventAccess, change store.CommittedChange, record store.RecordDelta) (syncV2Upsert, bool, error) {
	kind := ""
	switch record.Key.Kind {
	case "projects":
		kind = "project"
	case "initiatives":
		kind = "initiative"
	case "documents":
		kind = "document"
	default:
		return syncV2Upsert{}, false, errEventSourceUnsupported
	}
	seq, err := strconv.ParseInt(change.Seq, 10, 64)
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	// A change whose record has moved on is invalidated whatever the source
	// holds (the same version check follows the build): check it first and
	// skip the build. A catch-up page over old history then builds no source
	// for the records changed again since.
	current, err := s.store.RealtimeMetadataVersion(r.Context(), workspaceKey(r), record.Key)
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	if current == "" || current != record.Version {
		return syncV2Upsert{}, false, errEventSourceUnsupported
	}
	event := domain.RealtimeEvent{ID: change.TxID + "/" + record.Key.Kind + "/" + record.Key.ID, Seq: seq, Type: kind + ".updated", AggregateID: record.Key.ID, Payload: json.RawMessage(`{}`)}
	source, err := s.eventSourceForAuthority(r.Context(), workspaceKey(r), event, eventAuthority{access.SourceVersion(), access.AuthzEpoch})
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	if source.recordVersion == "" || source.recordVersion != record.Version || source.documentMissing {
		return syncV2Upsert{}, false, errEventSourceUnsupported
	}
	frame, err := ProjectEvent(r.Context(), source, access)
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	defer frame.Close()
	if !frame.Visible() {
		return syncV2Upsert{}, false, nil
	}
	projected, err := frame.Event()
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	var payload struct {
		Entity json.RawMessage `json:"entity"`
	}
	if err := json.Unmarshal(projected.Payload, &payload); err != nil {
		return syncV2Upsert{}, false, err
	}
	if len(payload.Entity) == 0 {
		return syncV2Upsert{}, false, errEventSourceUnsupported
	}
	data, id, _, err := syncRecordSummary(record.Key.Kind, payload.Entity)
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	return syncV2Upsert{Key: syncV2Key{record.Key.Kind, id}, Version: record.Version, Data: data, Projection: "summary", DetailStale: true}, true, nil
}

func (s *server) committedDeletionSource(r *http.Request, access store.EventAccess, change store.CommittedChange, seq int64) (*EventSource, error) {
	legacy, journaled, err := s.store.RecordJournalEventSeqForType(r.Context(), access.WorkspaceID, change.TxID, "issue.deleted")
	if err != nil {
		return nil, err
	}
	if journaled {
		if legacy == 0 {
			return nil, errEventSourceUnsupported
		}
		seq = legacy
	}
	version := access.SourceVersion()
	key := fmt.Sprintf("p12:deleted:%s:%s:%d:%d:%s", access.WorkspaceID, change.Seq, version.Metadata, version.Issues, access.AuthzEpoch)
	build := func() (*EventSource, error) {
		page, err := s.store.ReadRealtimeReplayPage(r.Context(), store.RealtimeReplayWindow{WorkspaceID: access.WorkspaceID, Head: seq, MaxRecordBytes: syncV2DeltaBytes}, seq-1, syncV2DeltaBytes)
		if err != nil {
			return nil, err
		}
		if page.ResyncRequired || len(page.Records) != 1 || page.Records[0].Seq != seq || page.Records[0].Event.Type != "issue.deleted" {
			return nil, errEventSourceUnsupported
		}
		record := page.Records[0]
		ctx := context.WithValue(r.Context(), eventSourceBuildKey{}, eventSourceBuildInput{epoch: access.AuthzEpoch})
		return s.BuildEventSource(ctx, workspaceKey(r), domain.RealtimeEvent{ID: record.Event.ID, Seq: seq, Type: record.Event.Type, AggregateID: record.Event.AggregateID, ActorID: record.ActorID, Payload: record.Event.Payload, CreatedAt: record.Event.CreatedAt})
	}
	if s.realtime != nil {
		return s.realtime.rules.sources.load(r.Context(), key, build)
	}
	return build()
}
