package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_SYNC_V2"))) {
	case "on", "1", "true":
		return true
	}
	return false
}

type committedChangeReader interface {
	ReadCommittedChanges(ctx context.Context, workspaceID, after string, limit, maxBytes int) (store.CommittedPage, error)
}

func (s *server) syncV2Journal() (committedChangeReader, bool) {
	journal, ok := any(s.store).(committedChangeReader)
	return journal, ok && syncV2Enabled() && strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_RECORD_JOURNAL")), "on")
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
	access, err := s.resolveSyncAccess(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	result, err := s.collectSyncChanges(r, access, journal, after, limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Changes are temporarily unavailable")
		return
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
		frame, err := ProjectEvent(source, access)
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
		if strings.EqualFold(strings.TrimSpace(os.Getenv("FLOW_RECORD_JOURNAL")), "on") {
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
	event := domain.RealtimeEvent{ID: change.TxID + "/" + record.Key.Kind + "/" + record.Key.ID, Seq: seq, Type: kind + ".updated", AggregateID: record.Key.ID, Payload: json.RawMessage(`{}`)}
	source, err := s.eventSourceForAuthority(r.Context(), workspaceKey(r), event, eventAuthority{access.SourceVersion(), access.AuthzEpoch})
	if err != nil {
		return syncV2Upsert{}, false, err
	}
	if source.recordVersion == "" || source.recordVersion != record.Version || source.documentMissing {
		return syncV2Upsert{}, false, errEventSourceUnsupported
	}
	frame, err := ProjectEvent(source, access)
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
		return s.BuildEventSource(context.WithValue(r.Context(), eventSourceEpochKey{}, access.AuthzEpoch), workspaceKey(r), domain.RealtimeEvent{ID: record.Event.ID, Seq: seq, Type: record.Event.Type, AggregateID: record.Event.AggregateID, ActorID: record.ActorID, Payload: record.Event.Payload, CreatedAt: record.Event.CreatedAt})
	}
	if s.realtime != nil {
		return s.realtime.rules.sources.load(r.Context(), key, build)
	}
	return build()
}
