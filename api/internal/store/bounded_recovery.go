package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

const metadataRecoveryPageRows = 32

// Canonical policy projections remain admitted through their final consumer.
// This is separate from installed recovery, which must retain complete Teams.
type scaleP21SW23TeamPolicyRowsRead struct {
	rows    []metadataCacheRow
	release func()
}

func (r *scaleP21SW23TeamPolicyRowsRead) Close() {
	if r != nil && r.release != nil {
		r.rows = nil
		r.release()
		r.release = nil
	}
}

// Read only canonical Team policy fields within the caller's SQL snapshot.
// Installed recovery still retains complete Teams from its own source.
func (s *SQLiteStore) scaleP21SW23ReadTeamPolicyRows(ctx context.Context, tx *sqlTx, workspace string) (_ *scaleP21SW23TeamPolicyRowsRead, err error) {
	if tx == nil || tx.database != s.db || tx.dialect != s.dialect {
		return nil, ErrMetadataRecordQuery
	}
	// The 12 MiB independent allowance covers retained <=256 KiB rows,
	// bounded headers, marshal's 6x escaping/growth and temporary typed
	// copies. Each row also owns the projector's 4 MiB until encoded.
	ctx, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 12<<20)
	if err != nil {
		return nil, err
	}
	result := &scaleP21SW23TeamPolicyRowsRead{release: release}
	defer func() {
		if err != nil {
			result.Close()
		}
	}()
	byteLength, keyType := "LENGTH", "''"
	if s.dialect == "postgres" {
		byteLength = "OCTET_LENGTH"
	}
	keyLength, bodyLength := byteLength+"(record_key)", byteLength+"(data)"
	if s.dialect == "sqlite" {
		keyLength, bodyLength = "LENGTH(CAST(record_key AS BLOB))", "LENGTH(CAST(data AS BLOB))"
		keyType = "typeof(record_key)"
	}
	preflight := `SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' AND ` + keyLength + `>191 LIMIT 1`
	args := []any{workspace}
	var invalid int
	if check := tx.QueryRowContext(ctx, preflight, args...).Scan(&invalid); check == nil {
		return nil, ErrMetadataRecordQuery
	} else if !errors.Is(check, sql.ErrNoRows) {
		return nil, check
	}
	last, lastOrder, started := any(""), 0, false
	used := metadataPageOverhead
	budget := newMetadataHydrationBudget()
	for {
		where := "workspace_key=? AND field='teams'"
		args = []any{workspace}
		if started {
			where += " AND (collection_order>? OR (collection_order=? AND record_key>?))"
			args = append(args, lastOrder, lastOrder, last)
		}
		// Preserve the old column collation and SQLite TEXT/BLOB ordering.
		// The cursor and point read retain the physical SQLite storage class.
		rows, err := tx.QueryContext(ctx, `SELECT record_key,`+keyType+`,collection_order,`+bodyLength+` FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 32`, args...)
		if err != nil {
			return nil, err
		}
		type header struct {
			key, storage string
			order        int
			size         int64
		}
		page := make([]header, 0, 32)
		for rows.Next() {
			var item header
			if err := rows.Scan(&item.key, &item.storage, &item.order, &item.size); err != nil {
				rows.Close()
				return nil, err
			}
			if !validMetadataKey(RecordKey{"teams", item.key}) || item.size < 0 {
				rows.Close()
				return nil, ErrMetadataRecordQuery
			}
			page = append(page, item)
		}
		readErr, closeErr := rows.Err(), rows.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, err
		}
		if len(page) == 0 {
			return result, nil
		}
		for _, item := range page {
			input := &scaleP21SW23TeamPolicyReaderAt{
				ctx: ctx, tx: tx, workspace: workspace, key: item.key,
				blob: s.dialect == "mysql" || item.storage == "blob", size: item.size,
			}
			projected, err := s.scaleP21SW23BeginTeamProjection(ctx, MetadataArrayRecord{
				Reader: io.NewSectionReader(input, 0, item.size), EncodedBytes: item.size,
			})
			if err != nil {
				return nil, err
			}
			encoded, encodeErr := json.Marshal(projected.Team)
			projected.Close()
			if encodeErr != nil {
				return nil, encodeErr
			}
			cost := len(encoded) + len(item.key) + 128
			if cost > MetadataRecordPageMaxBytes-used {
				return nil, ErrMetadataPageBudget
			}
			used += cost
			if err := budget.consume(encoded); err != nil {
				return nil, err
			}
			result.rows = append(result.rows, metadataCacheRow{Field: "teams", Key: item.key, Order: item.order, Data: encoded})
		}
		tail := page[len(page)-1]
		last, lastOrder, started = tail.key, tail.order, true
		if s.dialect == "mysql" || tail.storage == "blob" {
			last = []byte(tail.key)
		}
	}
}

type scaleP21SW23TeamPolicyReaderAt struct {
	ctx            context.Context
	tx             *sqlTx
	workspace, key string
	blob           bool
	size           int64
}

func (r *scaleP21SW23TeamPolicyReaderAt) ReadAt(p []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	n := 0
	key := any(r.key)
	if r.blob {
		key = []byte(r.key)
	}
	body := "data"
	if r.tx.dialect == "sqlite" {
		body = "CAST(data AS BLOB)"
	}
	for n < len(p) && offset+int64(n) < r.size {
		size := min(int64(metadataRecordChunkBytes), int64(len(p)-n), r.size-offset-int64(n))
		var chunk []byte
		err := r.tx.QueryRowContext(r.ctx, `SELECT SUBSTR(`+body+`,?,?) FROM workspace_metadata_records
			WHERE workspace_key=? AND field='teams' AND record_key=?`,
			offset+int64(n)+1, size, r.workspace, key).Scan(&chunk)
		if err != nil {
			return n, err
		}
		if int64(len(chunk)) != size {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(p[n:], chunk)
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

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
	if err := ctx.Err(); err != nil {
		return err
	}
	keyOrder, keyBound, bodyLength := "record_key", "?", "LENGTH(data)"
	keyLength := "LENGTH(record_key)"
	if s.dialect == "sqlite" {
		keyOrder, keyBound = "CAST(record_key AS BLOB)", "CAST(? AS BLOB)"
		bodyLength = "LENGTH(CAST(data AS BLOB))"
		keyLength = "LENGTH(CAST(record_key AS BLOB))"
	} else if s.dialect == "postgres" {
		keyLength = "OCTET_LENGTH(record_key)"
	}
	structOrder := field == "workspaceSettings"
	if structOrder && s.dialect == "postgres" {
		keyOrder = `record_key COLLATE "C"`
	}
	// Validate byte-sized keys before transferring them. Distinct SQLite
	// storage classes must not alias the same string-keyed cache identity.
	preflight := `SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND ` + keyLength + `>191 LIMIT 1`
	preflightArgs := []any{workspace, field}
	if s.dialect == "sqlite" {
		preflight = `SELECT 1 WHERE EXISTS(SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND ` + keyLength + `>191)
			OR EXISTS(SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field=? GROUP BY ` + keyOrder + ` HAVING COUNT(*)>1)`
		preflightArgs = append(preflightArgs, workspace, field)
	}
	rows, err := reader.QueryContext(ctx, preflight, preflightArgs...)
	if err != nil {
		return err
	}
	invalid := rows.Next()
	readErr, closeErr := rows.Err(), rows.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if invalid {
		return ErrMetadataRecordQuery
	}
	last, lastOrder, started := "", 0, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		where, args := "workspace_key=? AND field=?", []any{workspace, field}
		if started {
			if structOrder {
				where += " AND (collection_order>? OR (collection_order=? AND " + keyOrder + ">" + keyBound + "))"
				args = append(args, lastOrder, lastOrder, last)
			} else {
				where += " AND " + keyOrder + ">" + keyBound
				args = append(args, last)
			}
		}
		orderBy := keyOrder
		if structOrder {
			orderBy = "collection_order," + keyOrder
		}
		rows, err := reader.QueryContext(ctx, `SELECT record_key,collection_order,`+bodyLength+` FROM workspace_metadata_records WHERE `+where+` ORDER BY `+orderBy+` LIMIT 32`, args...)
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
			clause, values := bindList(keyOrder, missing)
			if s.dialect == "sqlite" {
				for index, key := range missing {
					values[index] = []byte(key)
				}
			}
			args = append([]any{MetadataRecordPageMaxBytes - metadataPageOverhead, workspace, field}, values...)
			rows, err = reader.QueryContext(ctx, `SELECT record_key,collection_order,`+bodyLength+`,CASE WHEN `+bodyLength+`<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+` ORDER BY `+keyOrder, args...)
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
		// Repeated physical aliases can update the same typed struct field.
		// Apply them in the legacy decoder's order even on mixed cache hits.
		if structOrder {
			sortMetadataRecordRows(batch)
		}
		// Teams restore canonical order after decoding. Struct properties
		// already use canonical order because aliases can update one field.
		if err := visit(batch); err != nil {
			return err
		}
		last, lastOrder, started = keys[len(keys)-1], headers[len(keys)-1].order, true
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
		mode, ok := metadataResidencyFields()[name]
		if !ok {
			return nil, nil, fmt.Errorf("%w: unknown collection %q", ErrBoundedMetadataCoverage, name)
		}
		if mode == "core" {
			return nil, nil, fmt.Errorf("%w: core field %q must remain inline", ErrBoundedMetadataCoverage, name)
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
		if shapes[name] == "" && len(trimmed) > 2 && (trimmed[0] == '[' || trimmed[0] == '{') && mode != "core" && mode != "records" {
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
	if err := ctx.Err(); err != nil {
		return data, stamp, 0, err
	}
	var raw []byte
	var size int
	bodyLength := "LENGTH(data)"
	if s.dialect == "sqlite" {
		bodyLength = "LENGTH(CAST(data AS BLOB))"
	}
	if err := tx.QueryRowContext(ctx, `SELECT `+metadataStampColumns+`,`+bodyLength+`,CASE WHEN `+bodyLength+`<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?`, workspace, cacheKindMetadata, MetadataRecordPageMaxBytes, workspace).Scan(&stamp.workspaceID, &stamp.revision, &size, &raw); err != nil {
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
	inlineTeams := len(data.Teams)
	fields := []string{}
	for name, mode := range metadataResidencyFields() {
		if mode == "policy" && shapes[name] != "" {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	type teamOrder struct {
		key          string
		order, index int
	}
	teamOrders := []teamOrder{}
	for _, field := range fields {
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
				if err := appendResidentMetadataRow(&data, row, shapes[field]); err != nil {
					return err
				}
				if field == "teams" {
					teamOrders = append(teamOrders, teamOrder{row.Key, row.Order, len(data.Teams) - inlineTeams - 1})
				}
			}
			return nil
		})
		if err != nil {
			return data, stamp, 0, err
		}
	}
	// The typed decoder keeps inline Teams first, then appends split rows in
	// canonical order. Reorder only that suffix, without a second Team slice.
	slices.SortStableFunc(teamOrders, func(a, b teamOrder) int {
		if a.order < b.order {
			return -1
		}
		if a.order > b.order {
			return 1
		}
		return strings.Compare(a.key, b.key)
	})
	for index := range teamOrders {
		if teamOrders[index].index == index {
			continue
		}
		saved := data.Teams[inlineTeams+index]
		for current := index; ; {
			from := teamOrders[current].index
			teamOrders[current].index = current
			if from == index {
				data.Teams[inlineTeams+current] = saved
				break
			}
			data.Teams[inlineTeams+current] = data.Teams[inlineTeams+from]
			current = from
		}
	}
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
				if name == row.Key || strings.EqualFold(name, row.Key) {
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
			// Keep the original property spelling: encoding/json chooses its
			// exact-name match first and its own folded-name match otherwise.
			return json.Unmarshal(encoded, target.Addr().Interface())
		}
		return fmt.Errorf("%w: invalid resident shape %s/%s", ErrMetadataRecordQuery, row.Field, shape)
	}
	return fmt.Errorf("%w: unknown resident field %s", ErrBoundedMetadataCoverage, row.Field)
}

func (s *SQLiteStore) loadResidentWorkspaces(ctx context.Context) error {
	ctx = metadataLogicalUnpinnedContext(ctx)
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
	stamps, heads := map[string]copyStamp{}, map[string]int64{}
	s.metadataResidency.mu.Lock()
	buildLogical := s.metadataResidency.requireCoverage || len(s.metadataResidency.logical) != 0
	s.metadataResidency.mu.Unlock()
	sources := map[string]*metadataLogicalSource{}
	defer func() {
		for _, source := range sources {
			source.Release()
		}
	}()
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
			data, stamp, cost, err := s.readResidentWorkspace(ctx, tx, key)
			if err != nil {
				return fmt.Errorf("recover resident workspace %q: %w", key, err)
			}
			if buildLogical {
				var source *metadataLogicalSource
				current, currentErr := s.retainMetadataLogicalSource(ctx, key)
				if currentErr == nil && current.Stamp() == stamp {
					source = current
					data, cost, err = s.metadataLogicalPolicy(ctx, source)
				} else {
					if current != nil {
						current.Release()
					}
					data, stamp, cost, source, err = s.readMetadataLogicalWorkspace(ctx, tx, key)
				}
				if err != nil {
					if source != nil {
						source.Release()
					}
					return fmt.Errorf("recover logical workspace %q: %w", key, err)
				}
				sources[key] = source
			}
			if cost > s.metadataResidency.policyLimit-total {
				return ErrMetadataWorkBudget
			}
			total += cost
			workspaces[key], costs[key] = data, cost
			stamps[key] = stamp
			if heads[key], err = residentJournalHead(ctx, tx, data.Workspace.ID); err != nil {
				return err
			}
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
	s.metadataResidency.mu.Lock()
	s.metadataResidency.stamps, s.metadataResidency.applied = stamps, heads
	oldSources := s.metadataResidency.logical
	s.metadataResidency.logical = sources
	sources = nil
	s.metadataResidency.mu.Unlock()
	for _, source := range oldSources {
		source.Release()
	}
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
	ctx = metadataLogicalUnpinnedContext(ctx)
	ctx, release, err := s.metadataResidency.work.acquire(ctx, metadataPolicyBytes+2<<20)
	if err != nil {
		return err
	}
	defer release()
	s.metadataResidency.mu.Lock()
	hasLogical := s.metadataResidency.logical[workspace] != nil
	s.metadataResidency.mu.Unlock()
	if !s.metadataResidency.requireCoverage && !hasLogical {
		return s.reloadResidentPolicyWorkspace(ctx, workspace)
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	storedStamp, err := readMetadataStamp(ctx, tx, workspace)
	if err != nil {
		return err
	}
	if current, err := s.retainMetadataLogicalSource(ctx, workspace); err == nil {
		exact := current.Stamp() == storedStamp
		current.Release()
		if exact {
			return tx.Commit()
		}
	}
	data, stamp, cost, source, err := s.readMetadataLogicalWorkspace(ctx, tx, workspace)
	if err != nil {
		return err
	}
	defer func() {
		if source != nil {
			source.Release()
		}
	}()
	head, err := residentJournalHead(ctx, tx, data.Workspace.ID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.installResidentPolicy(workspace, data, stamp, head, cost, source); err != nil {
		return err
	}
	source = nil
	return nil
}

// Policy-only probes exercise the storage projection independently of the
// installed logical lifecycle. They cannot activate production coverage.
func (s *SQLiteStore) reloadResidentPolicyWorkspace(ctx context.Context, workspace string) error {
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	data, stamp, cost, err := s.readResidentWorkspace(ctx, tx, workspace)
	if err != nil {
		return err
	}
	head, err := residentJournalHead(ctx, tx, data.Workspace.ID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.installResidentPolicy(workspace, data, stamp, head, cost)
}

func (s *SQLiteStore) installResidentPolicy(workspace string, data domain.Bootstrap, stamp copyStamp, head int64, cost int, sources ...*metadataLogicalSource) error {
	return s.installResidentPolicySource(workspace, data, stamp, head, cost, false, sources...)
}

func (s *SQLiteStore) installResidentPolicySource(workspace string, data domain.Bootstrap, stamp copyStamp, head int64, cost int, last bool, sources ...*metadataLogicalSource) error {
	if len(sources) > 1 {
		return ErrMetadataRecordQuery
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
	if last {
		s.lastWorkspaceKey = workspace
	}
	s.forgetCopyRevisionLocked(workspace)
	s.metadataResidency.mu.Lock()
	s.metadataResidency.stamps[workspace], s.metadataResidency.applied[workspace] = stamp, head
	var previous *metadataLogicalSource
	if len(sources) == 1 {
		if s.metadataResidency.logical == nil {
			s.metadataResidency.logical = map[string]*metadataLogicalSource{}
		}
		previous = s.metadataResidency.logical[workspace]
		s.metadataResidency.logical[workspace] = sources[0]
	}
	s.metadataResidency.mu.Unlock()
	s.mu.Unlock()
	if previous != nil {
		previous.Release()
	}
	return nil
}

func residentJournalHead(ctx context.Context, tx *sqlTx, workspaceID string) (int64, error) {
	var head int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(head),0) FROM workspace_record_journal_heads WHERE workspace_id=?`, workspaceID).Scan(&head)
	return head, err
}
