package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

const metadataLogicalExternalWork = 128 << 20
const metadataLogicalExternalRecord = 14 << 20
const metadataLogicalExternalReadChunk = 1 << 20

type metadataLogicalExternal struct {
	store     *SQLiteStore
	ctx       context.Context
	tx        *sqlTx
	workspace string
	stage     *sw26MigrationStage
	builder   *metadataLogicalBuilder
}

// hydrateMetadataLogicalSource supplies the same missing external collections
// as mutateFull, without installing those transient collections. Its caller
// owns the repeatable-read transaction and the 128 MiB parser work lease.
func (s *SQLiteStore) hydrateMetadataLogicalSource(ctx context.Context, tx *sqlTx, source *metadataLogicalSource, workspace string) (_ *metadataLogicalSource, err error) {
	ctx = metadataLogicalSourceScratchContext(ctx, source)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tx == nil || tx.database != s.db || source == nil {
		return nil, ErrMetadataRecordQuery
	}
	if s.metadataResidency != nil {
		lease, ok := ctx.Value(metadataWorkLeaseKey{}).(*metadataWorkLease)
		if !ok || lease.budget != s.metadataResidency.work {
			return nil, ErrMetadataWorkBudget
		}
		lease.budget.mu.Lock()
		admitted := lease.live && lease.bytes >= metadataLogicalExternalWork
		lease.budget.mu.Unlock()
		if !admitted {
			return nil, ErrMetadataWorkBudget
		}
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	defer source.Release()
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer stage.Close()
	for _, query := range []string{
		`CREATE TABLE external_headers(seq INTEGER PRIMARY KEY,kind TEXT NOT NULL,resource BLOB NOT NULL,id BLOB NOT NULL,physical INTEGER NOT NULL,size INTEGER NOT NULL,payload BLOB)`,
		`CREATE INDEX external_headers_group ON external_headers(kind,resource,seq)`,
	} {
		if _, err := stage.exec(ctx, 4096, query); err != nil {
			return nil, err
		}
	}
	builder, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer builder.Close()
	external := &metadataLogicalExternal{s, ctx, tx, workspace, stage, builder}
	fields := map[string]metadataLogicalNode{}
	if err := source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		if name != "-" {
			fields[name] = node
		}
		return nil
	}); err != nil {
		return nil, err
	}
	startField := func() error {
		part, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return err
		}
		builder.source.stores = append(builder.source.stores, part.storage)
		builder.storage, builder.offset = part.storage, 0
		part.source.stores = nil
		part.source.Release()
		return nil
	}
	hydrateField := func(name, kind string) error {
		node := fields[name]
		nilValue, err := node.isNil(ctx)
		if err != nil || !nilValue {
			return err
		}
		if err := startField(); err != nil {
			return err
		}
		if err := external.headers(kind); err != nil {
			return err
		}
		return external.collection(name, kind, node.typ)
	}
	// Preserve the literal reader's error ordering: issues first, then both
	// input kinds, then activity/comment/notification/delivery.
	if err := hydrateField("issues", "issue"); err != nil {
		return nil, err
	}
	for _, kind := range []string{"import_input", "migration_input"} {
		if err := external.headers(kind); err != nil {
			return nil, err
		}
		if err := external.validateInputs(kind); err != nil {
			return nil, err
		}
	}
	for _, name := range []string{"importJobs", "migrationJobs"} {
		if err := startField(); err != nil {
			return nil, err
		}
		if err := external.importJobs(name, fields[name]); err != nil {
			return nil, err
		}
	}
	for _, item := range [][2]string{
		{"activities", "activity"}, {"comments", "comment"},
		{"notifications", "notification"}, {"notificationDeliveries", "delivery"},
	} {
		if err := hydrateField(item[0], item[1]); err != nil {
			return nil, err
		}
	}
	err = source.VisitFields(ctx, func(name string, node metadataLogicalNode) error {
		index, _, ok := metadataLogicalField(name)
		if !ok || name == "-" || builder.source.fields[index].typ != nil {
			return nil
		}
		if !slices.Contains(builder.source.stores, node.source) {
			if err := node.source.Retain(); err != nil {
				return err
			}
			builder.source.stores = append(builder.source.stores, node.source)
		}
		builder.source.fields[index] = node
		return nil
	})
	if err != nil {
		return nil, err
	}
	return builder.Finish(source.Stamp())
}

func (e *metadataLogicalExternal) headers(kind string) error {
	bodyLength := "LENGTH(data)"
	idLength, resourceLength := "LENGTH(id)", "LENGTH(resource_id)"
	physical := "0"
	if e.store.dialect == "sqlite" {
		bodyLength, idLength, resourceLength = "LENGTH(CAST(data AS BLOB))", "LENGTH(CAST(id AS BLOB))", "LENGTH(CAST(resource_id AS BLOB))"
		physical = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END+CASE WHEN typeof(resource_id)='blob' THEN 2 ELSE 0 END"
	} else if e.store.dialect == "postgres" {
		bodyLength, idLength, resourceLength = "OCTET_LENGTH(data)", "OCTET_LENGTH(id)", "OCTET_LENGTH(resource_id)"
	}
	query := `SELECT CASE WHEN ` + resourceLength + `<=? THEN resource_id ELSE NULL END,CASE WHEN ` + idLength + `<=? THEN id ELSE NULL END,` + physical + `,` + bodyLength + ` FROM workspace_content_records WHERE workspace_key=? AND kind=? ORDER BY created_at,id`
	args := []any{metadataLogicalExternalRecord, metadataLogicalExternalRecord, e.workspace, kind}
	if kind == "issue" {
		if e.store.dialect == "sqlite" {
			physical = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END"
		}
		query = `SELECT '',CASE WHEN ` + idLength + `<=? THEN id ELSE NULL END,` + physical + `,` + bodyLength + ` FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`
		args = []any{metadataLogicalExternalRecord, e.workspace}
	}
	rows, err := e.tx.QueryContext(e.ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var resource, id string
		var physical int
		var size int64
		if err := rows.Scan(&resource, &id, &physical, &size); err != nil {
			return err
		}
		if size < 0 || size > metadataLogicalExternalRecord {
			return ErrMetadataWorkBudget
		}
		if _, err := e.stage.exec(e.ctx, int64(len(resource)+len(id)+128),
			`INSERT INTO external_headers(kind,resource,id,physical,size) VALUES(?,?,?,?,?)`,
			kind, []byte(resource), []byte(id), physical, size); err != nil {
			return err
		}
	}
	return rows.Err()
}

type metadataLogicalExternalHeader struct {
	sequence int64
	resource string
	id       string
	physical int
	size     int64
}

func (e *metadataLogicalExternal) walk(kind string, grouped bool, visit func(metadataLogicalExternalHeader) error) error {
	started, sequence, resource := false, int64(0), ""
	for {
		query := `SELECT seq,resource,id,physical,size FROM external_headers WHERE kind=?`
		args := []any{kind}
		if grouped && started {
			query += ` AND (resource>CAST(? AS BLOB) OR (resource=CAST(? AS BLOB) AND seq>?))`
			args = append(args, resource, resource, sequence)
		} else if started {
			query += ` AND seq>?`
			args = append(args, sequence)
		}
		if grouped {
			query += ` ORDER BY resource,seq LIMIT 1`
		} else {
			query += ` ORDER BY seq LIMIT 1`
		}
		var header metadataLogicalExternalHeader
		if err := e.stage.db.QueryRowContext(e.ctx, query, args...).Scan(&header.sequence, &header.resource, &header.id, &header.physical, &header.size); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		if err := visit(header); err != nil {
			return err
		}
		started, sequence, resource = true, header.sequence, header.resource
	}
}

func (e *metadataLogicalExternal) body(kind string, header metadataLogicalExternalHeader) ([]byte, error) {
	if header.size < 0 || header.size > metadataLogicalExternalRecord {
		return nil, ErrMetadataWorkBudget
	}
	data := "data"
	if e.store.dialect == "sqlite" {
		data = "CAST(data AS BLOB)"
	}
	id, resource := any(header.id), any(header.resource)
	if e.store.dialect == "sqlite" {
		if header.physical&1 != 0 {
			id = []byte(header.id)
		}
		if header.physical&2 != 0 {
			resource = []byte(header.resource)
		}
	}
	result := make([]byte, int(header.size))
	for offset := int64(0); offset < header.size; {
		// This parser already owns 128 MiB and one complete capped record.
		// A 1 MiB transfer chunk avoids a SQL round trip for every 32 KiB
		// without changing the detached streaming reader's smaller chunks.
		size := min(int64(metadataLogicalExternalReadChunk), header.size-offset)
		query := `SELECT SUBSTR(` + data + `,?,?) FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?`
		args := []any{offset + 1, size, e.workspace, kind, resource, id}
		if kind == "issue" {
			query = `SELECT SUBSTR(` + data + `,?,?) FROM issue_records WHERE workspace_key=? AND id=?`
			args = []any{offset + 1, size, e.workspace, id}
		}
		var chunk []byte
		if err := e.tx.QueryRowContext(e.ctx, query, args...).Scan(&chunk); err != nil {
			return nil, err
		}
		if int64(len(chunk)) != size {
			return nil, errors.New("logical external record changed inside snapshot")
		}
		copy(result[offset:], chunk)
		offset += size
	}
	return result, nil
}

func metadataLogicalExternalDecode(raw []byte, destination any, remaining ...int) error {
	work := metadataLogicalExternalWork - 64<<10
	if len(remaining) > 0 {
		work = min(work, remaining[0])
	}
	budget := metadataHydrationBudget{bytes: metadataLogicalExternalRecord, work: work}
	if err := budget.consume(raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, destination)
}

func (e *metadataLogicalExternal) validateInputs(kind string) error {
	return e.walk(kind, false, func(header metadataLogicalExternalHeader) error {
		raw, err := e.body(kind, header)
		if err != nil {
			return err
		}
		var input importInput
		if err := metadataLogicalExternalDecode(raw, &input); err != nil {
			return err
		}
		_, err = e.stage.exec(e.ctx, int64(len(raw)), `UPDATE external_headers SET payload=? WHERE seq=?`, raw, header.sequence)
		return err
	})
}

func (e *metadataLogicalExternal) collection(name, kind string, typ reflect.Type) error {
	if err := e.builder.BeginCollection(name, false); err != nil {
		return err
	}
	grouped := kind != "issue"
	var group string
	var groupNode metadataLogicalNode
	var count uint64
	endGroup := func() error {
		if groupNode.typ == nil {
			return nil
		}
		var data [8]byte
		binary.LittleEndian.PutUint64(data[:], count)
		if _, err := e.builder.storage.file.WriteAt(data[:], groupNode.offset+17); err != nil {
			return err
		}
		groupNode.size = e.builder.offset - groupNode.offset
		if err := e.builder.patch(groupNode); err != nil {
			return err
		}
		e.builder.cost += groupNode.cost + uint64(groupNode.typ.Size())*2
		e.builder.unavailable = e.builder.unavailable || groupNode.unavailable
		e.builder.count++
		return nil
	}
	err := e.walk(kind, grouped, func(header metadataLogicalExternalHeader) error {
		if typ.Kind() == reflect.Map && (groupNode.typ == nil || group != header.resource) {
			if err := endGroup(); err != nil {
				return err
			}
			key, err := e.builder.writeValue(reflect.ValueOf(header.resource).Convert(typ.Key()), 0, true)
			if err != nil {
				return err
			}
			e.builder.cost += key.cost + 128
			e.builder.unavailable = e.builder.unavailable || key.unavailable
			group = header.resource
			groupNode = metadataLogicalNode{source: e.builder.storage, offset: e.builder.offset, cost: uint64(typ.Elem().Size())*2 + 128, typ: typ.Elem()}
			count = 0
			if err := e.builder.write(make([]byte, 16)); err != nil {
				return err
			}
			if err := e.builder.write([]byte{1}); err != nil {
				return err
			}
			if err := e.builder.writeUint(0); err != nil {
				return err
			}
		}
		raw, err := e.body(kind, header)
		if err != nil {
			return err
		}
		element := typ.Elem()
		if typ.Kind() == reflect.Map {
			element = element.Elem()
		}
		value := reflect.New(element).Elem()
		if err := metadataLogicalExternalDecode(raw, value.Addr().Interface()); err != nil {
			return err
		}
		if err := e.builder.auditJSONLocations(value); err != nil {
			return err
		}
		if kind == "issue" {
			normalizeIssueRecord(value.Addr().Interface().(*domain.Issue))
		}
		if typ.Kind() != reflect.Map {
			return e.builder.Append(reflect.Value{}, value)
		}
		node, err := e.builder.writeValue(value, 0, false)
		groupNode.cost += node.cost + uint64(element.Size())*2 + 64
		groupNode.unavailable = groupNode.unavailable || node.unavailable
		count++
		return err
	})
	if err != nil {
		return err
	}
	if err := endGroup(); err != nil {
		return err
	}
	return e.builder.EndCollection()
}

func (e *metadataLogicalExternal) importJobs(name string, original metadataLogicalNode) error {
	if err := e.builder.inheritLocations(original.source); err != nil {
		return err
	}
	nilValue, err := original.isNil(e.ctx)
	if err != nil {
		return err
	}
	if err := e.builder.BeginCollection(name, nilValue); err != nil {
		return err
	}
	returnErr := original.Children(e.ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		value, err := node.Value(e.ctx, metadataLogicalExternalWork-metadataLogicalExternalRecord-64<<10)
		if err != nil {
			return err
		}
		kind, id := "", ""
		if name == "importJobs" {
			job := value.Addr().Interface().(*domain.ImportJob)
			if job.Rows != nil || job.Status == "completed" {
				return e.builder.Append(reflect.Value{}, value)
			}
			kind, id = "import_input", job.ID
		} else {
			job := value.Addr().Interface().(*domain.MigrationJob)
			if job.Bundle != nil {
				return e.builder.Append(reflect.Value{}, value)
			}
			kind, id = "migration_input", job.ID
		}
		var raw []byte
		err = e.stage.db.QueryRowContext(e.ctx, `SELECT payload FROM external_headers WHERE kind=? AND resource=CAST(? AS BLOB) ORDER BY seq LIMIT 1`, kind, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return e.builder.Append(reflect.Value{}, value)
		}
		if err != nil {
			return err
		}
		var input importInput
		if err := metadataLogicalExternalDecode(raw, &input, metadataLogicalExternalWork-int(node.cost)-64<<10); err != nil {
			return err
		}
		if name == "importJobs" {
			value.Addr().Interface().(*domain.ImportJob).Rows = input.Rows
		} else {
			value.Addr().Interface().(*domain.MigrationJob).Bundle = input.Bundle
		}
		return e.builder.Append(reflect.Value{}, value)
	})
	if returnErr != nil {
		return fmt.Errorf("hydrate %s: %w", name, returnErr)
	}
	return e.builder.EndCollection()
}
