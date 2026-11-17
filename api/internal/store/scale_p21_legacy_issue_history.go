package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

type legacyIssueHistoryPreparedKey struct{}

// Physical admission flushes must retain the literal writer's history snapshot
// for all 64 target occurrences, including the shared Fields maps of duplicates.
type legacyRecordIssueHistory struct {
	base      *legacyRecordBase
	store     *SQLiteStore
	histories map[string][]issueFieldHistory
	loaded    map[string]bool
	owner     *ResourceRead
	mapOwners []*ResourceRead
	rowOwners []*ResourceRead
	rows      []contentRecordRow
}

func (h *legacyRecordIssueHistory) Close() {
	if h == nil {
		return
	}
	h.histories, h.loaded, h.rows = nil, nil, nil
	h.owner.Close()
	for _, owner := range h.mapOwners {
		owner.Close()
	}
	for _, owner := range h.rowOwners {
		owner.Close()
	}
	h.mapOwners, h.rowOwners = nil, nil
}

func (b *legacyRecordBase) newIssueHistory(ctx context.Context) (_ *legacyRecordIssueHistory, err error) {
	history := &legacyRecordIssueHistory{
		base:  b,
		store: b.store, histories: map[string][]issueFieldHistory{},
		loaded: map[string]bool{},
		owner:  &ResourceRead{store: b.store},
	}
	defer func() {
		if err != nil {
			history.Close()
		}
	}()
	if err := history.owner.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	return history, nil
}

// Freeze raw rows without decoding unchanged histories. Each input query retains
// the database's own resource-ID collation; the stage retains exact returned
// resource identity even when two target IDs are SQL aliases.
func (b *legacyRecordBase) issueHistoryPayloadBatch(ctx context.Context, tx *sqlTx, workspace string, headers []legacyRecordIssueHeader) error {
	if _, err := b.stage.exec(ctx, 4096, `DELETE FROM legacy_issue_history`); err != nil {
		return err
	}
	wire := &ResourceRead{store: b.store}
	defer wire.Close()
	if err := wire.reserve(ctx, 64<<10); err != nil {
		return err
	}
	seen := map[string]bool{}
	var inputs []string
	var args []any
	var maxSelected uint64
	for _, header := range headers {
		if seen[header.id] {
			continue
		}
		seen[header.id] = true
		inputs = append(inputs, "SELECT ? AS selected_id,resource_id,data,created_at,id FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=?")
		args = append(args, header.id, workspace, issueFieldHistoryKind, header.id)
		maxSelected = max(maxSelected, uint64(len(header.id)))
	}
	if len(inputs) == 0 {
		return nil
	}
	from := " FROM (" + strings.Join(inputs, " UNION ALL ") + ") h"
	length, idLength := wire.byteLength("h.data"), wire.byteLength("h.resource_id")
	var maximum, maxID int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0),COALESCE(MAX("+idLength+"),0)"+from, args...).Scan(&maximum, &maxID); err != nil {
		return err
	}
	if maximum < 0 || maxID < 0 || uint64(maximum) > metadataWorkBytes/4 || uint64(maxID) > metadataWorkBytes/16 || maxSelected > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maximum)*3+(uint64(maxID)+maxSelected)*8); err != nil {
		return err
	}
	query := "SELECT h.selected_id,CASE WHEN " + idLength + "<=? THEN h.resource_id ELSE NULL END,CASE WHEN " + length + "<=? THEN h.data ELSE NULL END," + length + from + " ORDER BY h.created_at,h.id"
	rows, err := tx.QueryContext(ctx, query, append([]any{maxID, maximum}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var selected string
		var resource sql.NullString
		var raw []byte
		var actual sql.NullInt64
		if err := rows.Scan(&selected, &resource, &raw, &actual); err != nil {
			return err
		}
		if !resource.Valid || !actual.Valid || actual.Int64 < 0 || actual.Int64 > maximum || int64(len(raw)) != actual.Int64 {
			return ErrConcurrentWrite
		}
		if _, err := b.stage.exec(ctx, int64(len(selected)+len(resource.String)+len(raw)+256),
			`INSERT INTO legacy_issue_history(selected,resource,data) VALUES(?,?,?)`, []byte(selected), []byte(resource.String), raw); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Decode only changed IDs, using the SQL selection captured before any flush.
func (h *legacyRecordIssueHistory) load(ctx context.Context, issueID string) (err error) {
	if h.loaded[issueID] {
		return nil
	}
	owner := &ResourceRead{store: h.store}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	if err := owner.reserve(ctx, uint64(len(issueID))*8+1024); err != nil {
		return err
	}
	wire := &ResourceRead{store: h.store}
	defer wire.Close()
	var maximum, maxID int64
	if err := h.base.stage.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(LENGTH(data)),0),COALESCE(MAX(LENGTH(resource)),0) FROM legacy_issue_history WHERE selected=?", []byte(issueID)).Scan(&maximum, &maxID); err != nil {
		return err
	}
	if maximum < 0 || maxID < 0 || uint64(maximum) > metadataWorkBytes/4 || uint64(maxID) > metadataWorkBytes/16 {
		return ErrMetadataWorkBudget
	}
	if err := wire.reserve(ctx, uint64(maximum)*3+uint64(maxID)*8+64<<10); err != nil {
		return err
	}
	rows, err := h.base.stage.db.QueryContext(ctx, "SELECT resource,data FROM legacy_issue_history WHERE selected=? ORDER BY seq", []byte(issueID))
	if err != nil {
		return err
	}
	defer rows.Close()
	var values []issueFieldHistory
	for rows.Next() {
		var id []byte
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		if int64(len(id)) > maxID || int64(len(raw)) > maximum {
			return ErrConcurrentWrite
		}
		cost, err := metadataAggregatePayloadJSONWork(ctx, raw)
		if err != nil {
			return err
		}
		if err := owner.reserve(ctx, cost+uint64(len(id))*8+1024); err != nil {
			return err
		}
		var value issueFieldHistory
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if string(id) == issueID {
			values = append(values, value)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	h.histories[issueID], h.loaded[issueID] = values, true
	h.mapOwners = append(h.mapOwners, owner)
	return nil
}

// The caller owns issue, previous and their equality/decode scratch. Encode
// each checkpoint before a later duplicate mutates its shared Fields map.
func (h *legacyRecordIssueHistory) append(ctx context.Context, workspace string, issue domain.Issue, previous []byte) (err error) {
	if err := h.load(ctx, issue.ID); err != nil {
		return err
	}
	rowOwner := &ResourceRead{store: h.store}
	defer func() {
		if err != nil {
			rowOwner.Close()
		}
	}()
	if err := rowOwner.reserve(ctx, 8<<10); err != nil {
		return err
	}
	normalizeIssueRecord(&issue)
	var before domain.Issue
	if len(previous) > 0 {
		if err := json.Unmarshal(previous, &before); err != nil {
			return err
		}
		normalizeIssueRecord(&before)
	}
	history := issueFieldHistory{ID: issue.ID, CreatedAt: issue.UpdatedAt, Version: issue.Version, BaseVersion: before.Version, Fields: map[string]int64{}}
	if before.ID == "" {
		history.BaseVersion = issue.Version
	}
	if stored := h.histories[issue.ID]; len(stored) == 1 && before.ID != "" && stored[0].Version == before.Version {
		history.BaseVersion, history.Fields = stored[0].BaseVersion, stored[0].Fields
		if history.Fields == nil {
			history.Fields = map[string]int64{}
		}
	}
	oldValues := issueCASValues(before)
	changes := map[string]int64{}
	for field, value := range issueCASValues(issue) {
		if !reflect.DeepEqual(oldValues[field], value) {
			changes[field] = issue.Version
			if issue.Version <= before.Version {
				history.BaseVersion = issue.Version
			}
		}
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(history), 0)
	if err != nil {
		return err
	}
	changesCost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(changes), 0)
	if err != nil {
		return err
	}
	if err := rowOwner.reserve(ctx, uint64(cost+changesCost)*2+1024); err != nil {
		return err
	}
	growthCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(changes), 0)
	if err != nil {
		return err
	}
	if err := h.owner.reserve(ctx, growthCost); err != nil {
		return err
	}
	// No admission denial after this point may retry a partially changed map.
	for field, version := range changes {
		history.Fields[field] = version
	}
	row, err := contentRecordRowFor(workspace, issueFieldHistoryKind, issue.ID, history)
	if err != nil {
		return err
	}
	h.rows = append(h.rows, row)
	h.rowOwners = append(h.rowOwners, rowOwner)
	return nil
}

func (h *legacyRecordIssueHistory) flush(ctx context.Context, tx *sqlTx) error {
	if err := writeContentRecordRows(ctx, tx, h.rows); err != nil {
		return err
	}
	h.rows = nil
	for _, owner := range h.rowOwners {
		owner.Close()
	}
	h.rowOwners = nil
	return nil
}
