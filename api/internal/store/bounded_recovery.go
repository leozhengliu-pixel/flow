package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

const metadataRecoveryPageRows = 32

// Catalog completion is necessary, but cannot certify an old writer's later
// changes. Verify trigger dirty keys and canonical/sidecar coverage together
// in the caller's snapshot before accepting residency.
func (s *SQLiteStore) validateResidentBackfill(ctx context.Context, tx *sqlTx, workspace string) error {
	if !s.db.journalReady.Load() || recordJournalMode() != "on" {
		return fmt.Errorf("%w: record journal is not authoritative", ErrBoundedMetadataCoverage)
	}
	if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
		return fmt.Errorf("%w: record journal authority: %w", ErrBoundedMetadataCoverage, err)
	}
	var incomplete int
	err := tx.QueryRowContext(ctx, `SELECT
		CASE WHEN EXISTS(SELECT 1 FROM record_journal_dirty WHERE workspace_key=?) THEN 1 ELSE 0 END +
		CASE WHEN EXISTS(SELECT 1 FROM workspace_metadata_records r
			LEFT JOIN record_versions v ON v.workspace_key=r.workspace_key AND v.field=r.field AND v.record_key=r.record_key
			LEFT JOIN metadata_catalog c ON c.workspace_key=r.workspace_key AND c.field=r.field AND c.record_key=r.record_key
			LEFT JOIN metadata_catalog_collections p ON p.workspace_key=r.workspace_key AND p.field=r.field
			WHERE r.workspace_key=? AND (v.record_key IS NULL OR v.deleted<>0 OR c.record_key IS NULL OR p.field IS NULL OR p.phase<>2)) THEN 1 ELSE 0 END +
		CASE WHEN EXISTS(SELECT 1 FROM metadata_catalog c LEFT JOIN workspace_metadata_records r ON r.workspace_key=c.workspace_key AND r.field=c.field AND r.record_key=c.record_key WHERE c.workspace_key=? AND r.record_key IS NULL) THEN 1 ELSE 0 END`, workspace, workspace, workspace).Scan(&incomplete)
	if err != nil {
		return err
	}
	if incomplete != 0 {
		return fmt.Errorf("%w: canonical records, catalog or old-writer repair is incomplete for %q", ErrBoundedMetadataCoverage, workspace)
	}
	return nil
}

// The caller supplies a repeatable-read transaction. Header pages never carry
// bodies; only the rows fitting the next byte-bounded page are fetched.
func (s *SQLiteStore) scanResidentMetadataField(ctx context.Context, reader metadataReader, workspace, field string, stamp copyStamp, visit func([]metadataCacheRow) error) error {
	if metadataResidencyFields()[field] != "policy" {
		return fmt.Errorf("%w: unregistered resident field %q", ErrBoundedMetadataCoverage, field)
	}
	last, started := "", false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		where, args := "workspace_key=? AND field=?", []any{workspace, field}
		if started {
			where += " AND record_key>?"
			args = append(args, last)
		}
		rows, err := reader.QueryContext(ctx, `SELECT record_key,collection_order,LENGTH(data) FROM workspace_metadata_records WHERE `+where+` ORDER BY record_key LIMIT 32`, args...)
		if err != nil {
			return err
		}
		type header struct {
			key         string
			order, size int
		}
		headers := []header{}
		for rows.Next() {
			var h header
			if err := rows.Scan(&h.key, &h.order, &h.size); err != nil {
				rows.Close()
				return err
			}
			if !validMetadataKey(RecordKey{field, h.key}) || h.size < 0 {
				rows.Close()
				return ErrMetadataRecordQuery
			}
			headers = append(headers, h)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(headers) == 0 {
			return nil
		}
		used, keys := metadataPageOverhead, []string{}
		for _, h := range headers {
			cost := h.size + len(h.key) + len(field) + 64
			if cost > MetadataRecordPageMaxBytes-used {
				if len(keys) == 0 {
					return ErrMetadataPageBudget
				}
				break
			}
			used += cost
			keys = append(keys, h.key)
		}
		batch := make([]metadataCacheRow, 0, len(keys))
		missing := []string{}
		for _, key := range keys {
			cacheKey := metadataResidentCacheKey{workspace, stamp.workspaceID, stamp.revision, field, key}
			if cached, ok := s.metadataResidency.cached(cacheKey); ok {
				batch = append(batch, cached)
			} else {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			clause, values := bindList("record_key", missing)
			args = append([]any{MetadataRecordPageMaxBytes - metadataPageOverhead, workspace, field}, values...)
			rows, err = reader.QueryContext(ctx, `SELECT record_key,collection_order,LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+` ORDER BY record_key`, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				row, size := metadataCacheRow{Field: field}, 0
				if err := rows.Scan(&row.Key, &row.Order, &size, &row.Data); err != nil {
					rows.Close()
					return err
				}
				if row.Data == nil || len(row.Data) != size {
					rows.Close()
					return ErrMetadataPageBudget
				}
				batch = append(batch, row)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		if len(batch) != len(keys) {
			return ErrConcurrentWrite
		}
		budget := newMetadataHydrationBudget()
		used = metadataPageOverhead
		for _, row := range batch {
			used += len(row.Data) + len(row.Key) + len(row.Field) + 64
			if used > MetadataRecordPageMaxBytes {
				return ErrMetadataPageBudget
			}
			if err := budget.consume(row.Data); err != nil {
				return err
			}
		}
		// Never cache an unvalidated or partly fetched page.
		for _, row := range batch {
			s.metadataResidency.cacheRow(metadataResidentCacheKey{workspace, stamp.workspaceID, stamp.revision, field, row.Key}, row)
		}
		// Canonical collection order is restored after decoding all policy
		// rows. Keyset pagination itself always follows the primary key.
		if err := visit(batch); err != nil {
			return err
		}
		last, started = keys[len(keys)-1], true
	}
}

func residentMetadataRoot(raw []byte) ([]byte, map[string]string, error) {
	if len(raw) > MetadataRecordPageMaxBytes || !metadataRootFieldsBounded(raw) {
		return nil, nil, ErrMetadataPageBudget
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, err
	}
	shapes := map[string]string{}
	if err := json.Unmarshal(root[metadataCollectionsKey], &shapes); err != nil {
		return nil, nil, fmt.Errorf("%w: split metadata backfill is required", ErrBoundedMetadataCoverage)
	}
	for name, shape := range shapes {
		if _, ok := metadataResidencyFields()[name]; !ok {
			return nil, nil, fmt.Errorf("%w: unknown collection %q", ErrBoundedMetadataCoverage, name)
		}
		if shape != "array" && shape != "map" && shape != "updates" {
			return nil, nil, ErrMetadataRecordQuery
		}
	}
	for name, value := range root {
		if name == metadataCollectionsKey {
			delete(root, name)
			continue
		}
		mode, known := metadataResidencyFields()[name]
		if !known {
			return nil, nil, fmt.Errorf("%w: unknown field %q", ErrBoundedMetadataCoverage, name)
		}
		trimmed := strings.TrimSpace(string(value))
		if shapes[name] == "" && len(trimmed) > 2 && (trimmed[0] == '[' || trimmed[0] == '{') && mode != "core" {
			return nil, nil, fmt.Errorf("%w: unsplit field %q", ErrBoundedMetadataCoverage, name)
		}
		if mode != "core" && mode != "policy" {
			delete(root, name)
		}
	}
	encoded, err := json.Marshal(root)
	return encoded, shapes, err
}

func (s *SQLiteStore) readResidentWorkspace(ctx context.Context, tx *sqlTx, workspace string) (domain.Bootstrap, copyStamp, int, error) {
	var data domain.Bootstrap
	var stamp copyStamp
	var raw []byte
	var size int
	if err := tx.QueryRowContext(ctx, `SELECT `+metadataStampColumns+`,LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`, workspace, cacheKindMetadata, MetadataRecordPageMaxBytes, workspace).Scan(&stamp.workspaceID, &stamp.revision, &size, &raw); err != nil {
		return data, stamp, 0, err
	}
	if size > MetadataRecordPageMaxBytes || raw == nil {
		return data, stamp, 0, ErrMetadataPageBudget
	}
	root, shapes, err := residentMetadataRoot(raw)
	if err != nil {
		return data, stamp, 0, err
	}
	knownFields := make([]string, 0, len(shapes))
	for field := range shapes {
		knownFields = append(knownFields, field)
	}
	slices.Sort(knownFields)
	clause, values := bindList("field", knownFields)
	var unknown string
	err = tx.QueryRowContext(ctx, `SELECT field FROM workspace_metadata_records WHERE workspace_key=? AND NOT (`+clause+`) ORDER BY field,record_key LIMIT 1`, append([]any{workspace}, values...)...).Scan(&unknown)
	if err == nil {
		return data, stamp, 0, fmt.Errorf("%w: canonical field %q is outside the collection manifest", ErrBoundedMetadataCoverage, unknown)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return data, stamp, 0, err
	}
	if s.metadataResidency.requireCoverage {
		if err := s.validateResidentBackfill(ctx, tx, workspace); err != nil {
			return data, stamp, 0, err
		}
	}
	cost, err := metadataResidentCost(root)
	if err != nil {
		return data, stamp, 0, err
	}
	if err := json.Unmarshal(root, &data); err != nil {
		return data, stamp, 0, err
	}
	if data.Workspace.ID != stamp.workspaceID {
		return data, stamp, 0, ErrMetadataRecordQuery
	}
	fields := []string{}
	for name, mode := range metadataResidencyFields() {
		if mode == "policy" && shapes[name] != "" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	orders := map[string]map[string]int{}
	for _, field := range fields {
		orders[field] = map[string]int{}
		err := s.scanResidentMetadataField(ctx, tx, workspace, field, stamp, func(rows []metadataCacheRow) error {
			for _, row := range rows {
				rowCost, err := metadataResidentCost(row.Data)
				if err != nil {
					return err
				}
				if rowCost > s.metadataResidency.policyLimit-cost {
					return ErrMetadataWorkBudget
				}
				cost += rowCost
				orders[field][row.Key] = row.Order
				if err := appendResidentMetadataRow(&data, row, shapes[field]); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return data, stamp, 0, err
		}
	}
	// Only Teams is an array in the resident policy. Preserve its persisted
	// order, including an ordinal tie's canonical record-key tiebreaker.
	slices.SortStableFunc(data.Teams, func(a, b domain.Team) int {
		left, right := orders["teams"][a.ID], orders["teams"][b.ID]
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	domain.RebuildTeamDirectory(&data)
	return data, stamp, cost, nil
}

func appendResidentMetadataRow(data *domain.Bootstrap, row metadataCacheRow, shape string) error {
	value := reflect.ValueOf(data).Elem()
	for _, field := range persistedMetadataFields() {
		if field.name != row.Field {
			continue
		}
		target := value.Field(field.index)
		switch {
		case shape == "array" && target.Kind() == reflect.Slice:
			item := reflect.New(target.Type().Elem())
			if err := json.Unmarshal(row.Data, item.Interface()); err != nil {
				return err
			}
			target.Set(reflect.Append(target, item.Elem()))
			return nil
		case shape == "map" && target.Kind() == reflect.Map && target.Type().Key().Kind() == reflect.String:
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			item := reflect.New(target.Type().Elem())
			if err := json.Unmarshal(row.Data, item.Interface()); err != nil {
				return err
			}
			target.SetMapIndex(reflect.ValueOf(row.Key).Convert(target.Type().Key()), item.Elem())
			return nil
		case shape == "map" && target.Kind() == reflect.Struct:
			known := false
			for i := 0; i < target.NumField(); i++ {
				name, _, _ := strings.Cut(target.Type().Field(i).Tag.Get("json"), ",")
				if name == row.Key {
					known = true
					break
				}
			}
			if !known {
				return fmt.Errorf("%w: unknown policy field %s/%s", ErrBoundedMetadataCoverage, row.Field, row.Key)
			}
			encoded, err := json.Marshal(map[string]json.RawMessage{row.Key: row.Data})
			if err != nil {
				return err
			}
			return json.Unmarshal(encoded, target.Addr().Interface())
		}
		return fmt.Errorf("%w: invalid resident shape %s/%s", ErrMetadataRecordQuery, row.Field, shape)
	}
	return fmt.Errorf("%w: unknown resident field %s", ErrBoundedMetadataCoverage, row.Field)
}

func (s *SQLiteStore) loadResidentWorkspaces(ctx context.Context) error {
	ctx, release, err := s.metadataResidency.work.acquire(ctx, metadataPolicyBytes+2<<20)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	workspaces, costs := map[string]domain.Bootstrap{}, map[string]int{}
	s.mu.RLock()
	lastWorkspace, viewer := s.lastWorkspaceKey, s.viewer
	initial := len(s.workspaces) == 0
	s.mu.RUnlock()
	if initial {
		var raw []byte
		var size int
		err := tx.QueryRowContext(ctx, `SELECT last_workspace_key,LENGTH(viewer),CASE WHEN LENGTH(viewer)<=? THEN viewer ELSE NULL END FROM account_state WHERE id=1`, MetadataRecordPageMaxBytes).Scan(&lastWorkspace, &size, &raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if raw == nil || size > MetadataRecordPageMaxBytes {
				return ErrMetadataPageBudget
			}
			cost, err := metadataResidentCost(raw)
			if err != nil {
				return err
			}
			costs[""] = cost
			if err := json.Unmarshal(raw, &viewer); err != nil {
				return err
			}
		}
	} else {
		s.metadataResidency.mu.Lock()
		costs[""] = s.metadataResidency.policy[""]
		s.metadataResidency.mu.Unlock()
	}
	last, started, total := "", false, 0
	total = costs[""]
	for {
		query, args := `SELECT workspace_key FROM workspace_states`, []any{}
		if started {
			query += ` WHERE workspace_key>?`
			args = append(args, last)
		}
		rows, err := tx.QueryContext(ctx, query+` ORDER BY workspace_key LIMIT 128`, args...)
		if err != nil {
			return err
		}
		keys := []string{}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			data, _, cost, err := s.readResidentWorkspace(ctx, tx, key)
			if err != nil {
				return fmt.Errorf("recover resident workspace %q: %w", key, err)
			}
			if cost > s.metadataResidency.policyLimit-total {
				return ErrMetadataWorkBudget
			}
			total += cost
			workspaces[key], costs[key] = data, cost
		}
		last, started = keys[len(keys)-1], true
	}
	if len(workspaces) == 0 {
		var legacy int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_state WHERE id=1`).Scan(&legacy); err != nil {
			return err
		}
		if legacy != 0 || strings.EqualFold(s.fixtureProfile, "test") {
			return fmt.Errorf("%w: seed or migrate legacy workspace state before bounded startup", ErrBoundedMetadataCoverage)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.metadataResidency.replacePolicyCosts(costs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workspaces, s.copyRevisions = workspaces, nil
	// Legacy copy revisions certify complete metadata. Policy-only copies
	// leave them unknown so native record readers use canonical SQL records.
	if _, ok := workspaces[lastWorkspace]; !ok {
		lastWorkspace = firstWorkspaceKey(workspaces)
	}
	if viewer.ID == "" {
		viewer = workspaces[lastWorkspace].Viewer
		if viewer.ID == "" {
			viewer = bootstrapViewer()
		}
	}
	s.lastWorkspaceKey, s.viewer = lastWorkspace, viewer
	return nil
}

func (s *SQLiteStore) reloadResidentWorkspace(ctx context.Context, workspace string) error {
	ctx, release, err := s.metadataResidency.work.acquire(ctx, metadataPolicyBytes+2<<20)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	data, _, cost, err := s.readResidentWorkspace(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.metadataResidency.mu.Lock()
	costs := make(map[string]int, len(s.metadataResidency.policy)+1)
	for key, value := range s.metadataResidency.policy {
		costs[key] = value
	}
	s.metadataResidency.mu.Unlock()
	costs[workspace] = cost
	if err := s.metadataResidency.replacePolicyCosts(costs); err != nil {
		return err
	}
	s.mu.Lock()
	s.workspaces[workspace] = data
	s.forgetCopyRevisionLocked(workspace)
	s.mu.Unlock()
	return nil
}
