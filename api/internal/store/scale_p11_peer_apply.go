package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"flow/api/internal/domain"
)

var ErrPeerSnapshotRequired = errors.New("peer change requires a complete workspace snapshot")

type peerAppliedWorkspace struct {
	workspaceID string
	seq         int64
	copyEpoch   uint64
}

// ApplyCommittedPage installs an entire page atomically. Canonical records are
// fetched in one SQL snapshot; a version change, missing coverage, unsupported
// shape, or intervening local install requires the existing reload path.
func (s *SQLiteStore) ApplyCommittedPage(ctx context.Context, workspaceID string, page CommittedPage) (applied string, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	workspace, next, candidate, stamp, err := s.preparePeerPage(ctx, workspaceID, page)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if candidate != nil {
		s.workspaces[workspace] = *candidate
		s.setCopyRevisionLocked(workspace, stamp)
	}
	copy, ok := s.copyRevisionLocked(workspace)
	if !ok {
		return "", ErrPeerSnapshotRequired
	}
	if s.peerApplied == nil {
		s.peerApplied = map[string]peerAppliedWorkspace{}
	}
	if len(s.peerApplied) > len(s.workspaces)+8 {
		for key := range s.peerApplied {
			if _, present := s.workspaces[key]; !present {
				delete(s.peerApplied, key)
			}
		}
	}
	s.peerApplied[workspace] = peerAppliedWorkspace{workspaceID, next, copy.epoch}
	return strconv.FormatInt(next, 10), nil
}

// ShadowCommittedPage prepares the same candidate without exposing it, reloads
// through the compatibility path, and compares the resulting snapshots. The
// compatibility result remains installed even when the comparison differs.
func (s *SQLiteStore) ShadowCommittedPage(ctx context.Context, workspaceID string, page CommittedPage) (bool, error) {
	s.writeMu.Lock()
	workspace, _, candidate, _, err := s.preparePeerPage(ctx, workspaceID, page)
	s.writeMu.Unlock()
	if err != nil {
		return false, err
	}
	if err := s.ReloadWorkspace(ctx, workspace); err != nil {
		return false, err
	}
	s.mu.RLock()
	equal := candidate == nil || reflect.DeepEqual(*candidate, s.workspaces[workspace])
	s.mu.RUnlock()
	// Establish the compatibility snapshot as the next incremental baseline.
	_, err = s.ApplyCommittedPage(ctx, workspaceID, page)
	return equal, err
}

func (s *SQLiteStore) preparePeerPage(ctx context.Context, workspaceID string, page CommittedPage) (string, int64, *domain.Bootstrap, copyStamp, error) {
	fail := func(err error) (string, int64, *domain.Bootstrap, copyStamp, error) {
		return "", 0, nil, copyStamp{}, err
	}
	next, err := strconv.ParseInt(page.Next, 10, 64)
	if err != nil || next < 0 || page.Head != page.Next || page.HasMore || page.ResyncRequired || workspaceID == "" {
		return fail(ErrPeerSnapshotRequired)
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	var workspace string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_key FROM workspace_states WHERE workspace_id=?`, workspaceID).Scan(&workspace); err != nil {
		return fail(err)
	}
	stamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return fail(err)
	}
	var head sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT head FROM workspace_record_journal_heads WHERE workspace_id=?`, workspaceID).Scan(&head); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fail(err)
	}
	if head.Int64 != next {
		return fail(ErrPeerSnapshotRequired)
	}
	s.mu.RLock()
	current, present := s.workspaces[workspace]
	copy, known := s.copyRevisionLocked(workspace)
	s.mu.RUnlock()
	if !present || !known || copy.workspaceID != workspaceID {
		return fail(ErrPeerSnapshotRequired)
	}
	// A reload/local writer may already include this page (and later writes).
	// Replaying an older page must never replace a newer canonical record.
	if copy.revision == stamp.revision {
		return workspace, next, nil, stamp, nil
	}
	basis, ok := s.peerApplied[workspace]
	if !ok || basis.workspaceID != workspaceID || basis.copyEpoch != copy.epoch || basis.seq > next {
		return fail(ErrPeerSnapshotRequired)
	}
	var gaps int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal_dirty WHERE workspace_key=?`, workspace).Scan(&gaps); err != nil {
		return fail(err)
	}
	if gaps != 0 {
		return fail(ErrRecordJournalCoverage)
	}
	// A transaction may have committed without being sequenced yet. Its keys
	// cannot be inferred from an older head, including for permission changes.
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM record_journal WHERE workspace_id=? AND seq IS NULL`, workspaceID).Scan(&gaps); err != nil {
		return fail(err)
	}
	if gaps != 0 {
		return fail(ErrPeerSnapshotRequired)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,CASE WHEN running_bytes<=? THEN payload ELSE NULL END FROM (SELECT bounded.*,SUM(LENGTH(payload)+512) OVER (ORDER BY seq,id) AS running_bytes FROM (SELECT id,seq,payload FROM record_journal WHERE workspace_id=? AND seq>? AND seq<=? ORDER BY seq,id LIMIT 101) bounded) budgeted ORDER BY seq,id`, MetadataRecordPageMaxBytes, workspaceID, basis.seq, next)
	if err != nil {
		return fail(err)
	}
	transactions := map[string]CommittedChange{}
	for rows.Next() {
		var id string
		var seq int64
		var raw []byte
		if err := rows.Scan(&id, &seq, &raw); err != nil {
			rows.Close()
			return fail(err)
		}
		if len(transactions) >= 100 || len(raw) == 0 {
			rows.Close()
			return fail(ErrPeerSnapshotRequired)
		}
		var change CommittedChange
		if err := json.Unmarshal(raw, &change); err != nil {
			rows.Close()
			return fail(err)
		}
		change.Seq = strconv.FormatInt(seq, 10)
		transactions[id] = change
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fail(err)
	}
	keys := map[RecordKey]RecordDelta{}
	for _, change := range page.Transactions {
		committed, present := transactions[change.TxID]
		if !present || !reflect.DeepEqual(committed, change) || len(change.Invalidations) != 0 {
			return fail(ErrPeerSnapshotRequired)
		}
		delete(transactions, change.TxID)
		for _, delta := range change.Records {
			// Issue/content records are read directly from SQL. Unknown fields,
			// root shapes, ACL invalidations, creates and removals use reload.
			if delta.Key.Kind == "issues" {
				continue
			}
			if !peerReplacementKind(delta.Key.Kind) || delta.Deleted {
				return fail(ErrPeerSnapshotRequired)
			}
			keys[delta.Key] = delta
			if len(keys) > MetadataRecordBatchMaxKeys {
				return fail(ErrPeerSnapshotRequired)
			}
		}
	}
	if len(transactions) != 0 {
		return fail(ErrPeerSnapshotRequired)
	}
	root := reflect.ValueOf(&current).Elem()
	cloned := map[string]bool{}
	remaining := MetadataRecordPageMaxBytes
	for key, delta := range keys {
		field, _ := metadataFieldByName(key.Kind)
		target := root.Field(field.index)
		// Small record replacement only. A copied container also has a budget;
		// large collections keep the exact compatibility reload until P21.
		if !rebaseableMetadataField(field, target.Type()) || target.IsNil() || int64(target.Len())*int64(target.Type().Elem().Size()) > 1<<20 {
			return fail(ErrPeerSnapshotRequired)
		}
		if !cloned[key.Kind] {
			target.Set(cloneMetadataContainer(target))
			cloned[key.Kind] = true
		}
		collection := newMetadataCollection(field, target)
		old, exists := collection.record(key.ID)
		if !exists {
			return fail(ErrPeerSnapshotRequired)
		}
		var version int64
		var deleted, size int
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT v.version,v.deleted,LENGTH(r.data),CASE WHEN LENGTH(r.data)<=? THEN r.data ELSE NULL END FROM record_versions v JOIN workspace_metadata_records r ON r.workspace_key=v.workspace_key AND r.field=v.field AND r.record_key=v.record_key WHERE v.workspace_key=? AND v.field=? AND v.record_key=?`, remaining, workspace, key.Kind, key.ID).Scan(&version, &deleted, &size, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return fail(ErrPeerSnapshotRequired)
		}
		if err != nil {
			return fail(err)
		}
		if raw == nil || deleted != 0 || strconv.FormatInt(version, 10) != delta.Version {
			return fail(ErrPeerSnapshotRequired)
		}
		remaining -= size
		raw, err = resolveDocumentMarkers(key.Kind, raw)
		if err != nil {
			return fail(err)
		}
		record, err := decodeMetadataRecord(target.Type().Elem(), raw, old)
		if err != nil {
			return fail(fmt.Errorf("decode peer metadata record: %w", err))
		}
		// Reuse the record's existing load normalization, without walking or
		// mutating any of the shared, unrelated metadata collections.
		normalized, err := normalizedMetadataRecord(field, key.ID, record)
		if err != nil {
			return fail(err)
		}
		record, err = decodeMetadataRecord(target.Type().Elem(), normalized, old)
		if err != nil {
			return fail(err)
		}
		collection.set(key.ID, record)
	}
	for name := range cloned {
		field, _ := metadataFieldByName(name)
		target := root.Field(field.index)
		if target.Kind() != reflect.Slice {
			continue
		}
		// An ordinal-only write is also journaled. Replacing the same IDs
		// in the old order would diverge from the compatibility snapshot.
		rows, err := tx.QueryContext(ctx, `SELECT record_key FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key LIMIT ?`, workspace, name, target.Len()+1)
		if err != nil {
			return fail(err)
		}
		index := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fail(err)
			}
			if index >= target.Len() || target.Index(index).FieldByName("ID").String() != id {
				rows.Close()
				return fail(ErrPeerSnapshotRequired)
			}
			index++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fail(err)
		}
		if index != target.Len() {
			return fail(ErrPeerSnapshotRequired)
		}
	}
	if cloned["projects"] {
		// Refresh just the changed entities' display references. Walking the
		// shared collections would mutate snapshots held by other readers.
		for key := range keys {
			field, _ := metadataFieldByName(key.Kind)
			collection := newMetadataCollection(field, root.Field(field.index))
			record, _ := collection.record(key.ID)
			peerDisplayReferences(record, current)
		}
	}
	if cloned["teams"] {
		domain.RebuildTeamDirectory(&current)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return workspace, next, &current, stamp, nil
}

func peerReplacementKind(kind string) bool {
	switch kind {
	case "projects", "teams":
		return true
	}
	return false
}

func peerDisplayReferences(record reflect.Value, metadata domain.Bootstrap) {
	current := func(user domain.User) domain.User {
		for _, candidate := range metadata.Users {
			if candidate.ID == user.ID {
				return candidate
			}
		}
		return user
	}
	switch value := record.Interface().(type) {
	case domain.Project:
		for _, status := range metadata.ProjectStatuses {
			if status.ID == value.Status.ID {
				value.Status = status
				break
			}
		}
		if value.Lead != nil {
			user := current(*value.Lead)
			value.Lead = &user
		}
		if value.Creator != nil {
			user := current(*value.Creator)
			value.Creator = &user
		}
		record.Set(reflect.ValueOf(value))
	}
}
