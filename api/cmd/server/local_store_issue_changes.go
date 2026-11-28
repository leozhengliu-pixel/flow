package main

import (
	"errors"
	"net/http"
	"strconv"

	"flow/api/internal/store"
)

type localStoreIssueCheckpoint struct {
	Revision    string `json:"revision"`
	EventCursor string `json:"eventCursor"`
}

type localStoreIssueDelta struct {
	syncV2ChangesResponse
	localStoreIssueCheckpoint
}

const localStorePulseCursor = "pulseDeliveryCursors"

func (s *server) localStoreIssueCheckpoint(r *http.Request, access store.EventAccess) (localStoreIssueCheckpoint, error) {
	cursor, err := s.store.WorkspaceChangeHead(r.Context(), workspaceKey(r))
	return localStoreIssueCheckpoint{localStoreIssueRevision(access), strconv.FormatInt(cursor, 10)}, err
}

func (s *server) localStoreIssueManifest(w http.ResponseWriter, r *http.Request) {
	access, ok := s.localStoreIssueAccess(w, r)
	if !ok {
		return
	}
	checkpoint, err := s.localStoreIssueCheckpoint(r, access)
	var head string
	if err == nil {
		head, err = s.store.RecordJournalHead(r.Context(), access.WorkspaceID)
	}
	var count *int64
	if err == nil && r.URL.Query().Get("counts") == "1" {
		var page store.IssueRecordPage
		page, err = s.readLocalStoreIssues(r, access, store.IssueRecordQuery{Limit: 1, IncludeTotal: true})
		count = &page.Total
	}
	if err == nil {
		err = s.validateLocalStoreIssueRead(r, access)
	}
	if err != nil {
		writeSyncError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	s.writeSyncJSON(w, r, struct {
		localStoreStamp
		localStoreIssueCheckpoint
		Schema       int      `json:"schema"`
		Capabilities []string `json:"capabilities"`
		IssueCount   *int64   `json:"issueCount,omitempty"`
	}{localStoreStamp{access.PrincipalID, access.WorkspaceID, access.AuthzEpoch, head}, checkpoint, 2, []string{localStoreIssueCapability, localStoreOutboxCapability}, count}, 4096)
}

func (s *server) localStoreIssueChanges(w http.ResponseWriter, r *http.Request, journal committedChangeReader, workspaceID string) {
	q := r.URL.Query()
	after := q.Get("after")
	seq, err := strconv.ParseInt(after, 10, 64)
	if err != nil || seq < 0 || strconv.FormatInt(seq, 10) != after {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	limit, err := syncPageNumber(q.Get("limit"), 100, 1, 100)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	for attempt := 1; ; attempt++ {
		access, err := s.resolveSyncAccess(r)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		result, err := s.readLocalStoreIssueChanges(r, journal, access, workspaceID, q.Get("authzEpoch"), after, limit)
		if err == nil {
			s.writeSyncJSON(w, r, result, syncV2DeltaBytes)
			return
		}
		if attempt == syncChangesAttempts || !errors.Is(err, store.ErrMetadataCursor) {
			writeSyncError(w, err)
			return
		}
	}
}

func (s *server) readLocalStoreIssueChanges(r *http.Request, journal committedChangeReader, access store.EventAccess, workspaceID, epoch, after string, limit int) (localStoreIssueDelta, error) {
	result := localStoreIssueDelta{syncV2ChangesResponse: syncV2ChangesResponse{Next: after, Head: "0", AuthzEpoch: access.AuthzEpoch, Transactions: []syncV2Transaction{}}}
	if workspaceID != access.WorkspaceID || epoch != access.AuthzEpoch {
		result.ResyncRequired = true
		return result, nil
	}
	checkpoint, err := s.localStoreIssueCheckpoint(r, access)
	if err != nil {
		return result, err
	}
	result.localStoreIssueCheckpoint = checkpoint
	page, err := journal.ReadCommittedChanges(r.Context(), workspaceID, after, limit, syncV2DeltaBytes)
	if errors.Is(err, store.ErrRecordJournalCoverage) {
		result.ResyncRequired = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Head = page.Head
	if page.ResyncRequired || page.AuthzEpoch != access.AuthzEpoch {
		result.ResyncRequired = true
		return result, nil
	}
	used := int64(2048)
	for _, change := range page.Transactions {
		tx, err := s.projectLocalStoreChange(r, access, change)
		if err != nil {
			return result, err
		}
		if len(tx.Upserts)+len(tx.Removals)+len(tx.Invalidations) > 0 {
			size, err := tx.jsonBytes()
			if err != nil {
				return result, err
			}
			if used+size+1 > syncV2DeltaBytes {
				if result.Next == after {
					// No partial transaction can be committed by the client.
					result.ResyncRequired = true
				} else {
					result.HasMore = true
				}
				return result, s.validateLocalStoreIssueRead(r, access)
			}
			used += size + 1
			result.Transactions = append(result.Transactions, tx)
		}
		result.Next = change.Seq
	}
	result.Next, result.HasMore = page.Next, page.HasMore
	return result, s.validateLocalStoreIssueRead(r, access)
}

func (s *server) projectLocalStoreChange(r *http.Request, access store.EventAccess, change store.CommittedChange) (syncV2Transaction, error) {
	tx := emptySyncTransaction(change.TxID, change.Seq)
	for _, kind := range change.Invalidations {
		if !localStoreIgnoresIssueKind(kind) {
			tx.Invalidations = append(tx.Invalidations, kind)
		}
	}
	pulseCursor := false
	ids := make([]string, 0, min(len(change.Records), store.MetadataRecordBatchMaxKeys))
	for _, record := range change.Records {
		if record.Key.Kind == "settings" && record.Key.ID == localStorePulseCursor {
			pulseCursor = true
			continue
		}
		if localStoreIgnoresIssueKind(record.Key.Kind) {
			continue
		}
		if record.Key.Kind != localStoreIssueKind || record.Deleted || len(ids) == cap(ids) {
			return invalidateCommittedChange(change), nil
		}
		ids = append(ids, record.Key.ID)
	}
	if len(tx.Invalidations) > 0 {
		return tx, nil
	}
	if pulseCursor {
		// The cursor advances the metadata revision but isn't part of the directory.
		tx.Invalidations = append(tx.Invalidations, localStorePulseCursor)
	}
	if len(ids) == 0 {
		return tx, nil
	}
	page, err := s.readLocalStoreIssues(r, access, store.IssueRecordQuery{
		IssueIDs: ids, RestrictToIssueIDs: true, Limit: len(ids), Archived: "all",
	})
	if err != nil {
		return tx, err
	}
	if page.HasMore || len(page.Items) != len(ids) {
		// A journal key may now belong to a private team. Invalidate the
		// baseline without disclosing the key or skipping a cached removal.
		tx.Invalidations = []string{localStoreIssueKind}
		return tx, nil
	}
	items, err := s.localStoreIssueRecords(r.Context(), workspaceKey(r), page.Items)
	if err != nil {
		return tx, err
	}
	for i, item := range items {
		key := syncV2Key{Kind: item.Key.Kind, ID: item.Key.ID}
		if page.Items[i].ArchivedAt != nil {
			tx.Removals = append(tx.Removals, syncV2Removal{Key: key, Version: item.Version})
		} else {
			// Serve the current authorized summary even if a second edit
			// superseded this transaction while the browser was closed.
			tx.Upserts = append(tx.Upserts, syncV2Upsert{Key: key, Version: item.Version, Data: item.Data, Projection: "summary", DetailStale: true})
		}
	}
	return tx, nil
}

func localStoreIgnoresIssueKind(kind string) bool {
	// The issue baseline contains neither the inbox nor its delivery queue.
	// A single issue edit can write both, without changing the directory.
	if kind == "notifications" || kind == "notificationDeliveries" {
		return true
	}
	return kind != "userSettings" && kind != "settings" && localStoreIgnoresKind(kind)
}
