package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

// ErrMetadataFieldInline reports a record-row write to a field the stored
// root keeps inline (splitWorkspaceMetadata could not give every entry its
// own row: a shared, empty or over-long ID or key). Writing a row there
// would be invisible to the next load, and a missing row is no deletion, so
// the write is refused before it changes anything. Writers with the
// complete field persist it inline instead (writeInlineMetadataFields).
var ErrMetadataFieldInline = fmt.Errorf("%w: metadata field is stored inline", ErrBoundedMetadataCoverage)

type metadataRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// metadataFieldStoredInline reports whether the stored root holds a non-null
// value for field without listing it as kept in rows of its kind (see
// metadataFieldRecordBacked). A missing or null field is not inline: record
// writers may give it its first row.
func metadataFieldStoredInline(ctx context.Context, q metadataRowQuerier, dialect, workspace, field string) (bool, error) {
	meta, ok := metadataFieldByName(field)
	if !ok {
		return false, nil
	}
	kind := reflect.TypeFor[domain.Bootstrap]().Field(meta.index).Type
	want := "map"
	switch {
	case updateMetadataField(field):
		want = "updates"
	case kind.Kind() == reflect.Slice:
		want = "array"
	}
	text := (&SQLiteStore{dialect: dialect}).jsonText
	var shape sql.NullString
	var present int
	err := q.QueryRowContext(ctx, `SELECT `+text("data", metadataCollectionsKey+"."+field)+`,CASE WHEN `+text("data", field)+` IS NULL THEN 0 ELSE 1 END FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&shape, &present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return present == 1 && shape.String != want, nil
}

// inlineMetadataFields returns the fields among names the stored root keeps
// inline, in names order.
func (s *SQLiteStore) inlineMetadataFields(ctx context.Context, q metadataRowQuerier, workspace string, names ...string) ([]string, error) {
	var inline []string
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		stored, err := metadataFieldStoredInline(ctx, q, s.dialect, workspace, name)
		if err != nil {
			return nil, err
		}
		if stored {
			inline = append(inline, name)
		}
	}
	return inline, nil
}

// writeInlineMetadataFields persists the named fields of after exactly as the
// legacy delta writer persists a changed field: the whole field, kept
// inline unless its entries can now be split into rows. after must hold the
// complete fields and its workspace.
func (s *SQLiteStore) writeInlineMetadataFields(ctx context.Context, tx *sqlTx, workspace string, after domain.Bootstrap, fields []string) error {
	if len(fields) == 0 {
		return nil
	}
	var before, target domain.Bootstrap
	before.Workspace, target.Workspace = after.Workspace, after.Workspace
	source, destination := reflect.ValueOf(after), reflect.ValueOf(&target).Elem()
	for _, name := range fields {
		field, ok := metadataFieldByName(name)
		if !ok {
			return ErrMetadataRecordQuery
		}
		value := source.Field(field.index)
		destination.Field(field.index).Set(value)
		if (value.Kind() == reflect.Slice || value.Kind() == reflect.Map) && value.IsNil() {
			// A nil field still replaces the stored inline value.
			if value.Kind() == reflect.Map {
				reflect.ValueOf(&before).Elem().Field(field.index).Set(reflect.MakeMap(value.Type()))
			} else {
				reflect.ValueOf(&before).Elem().Field(field.index).Set(reflect.MakeSlice(value.Type(), 0, 0))
			}
		}
	}
	if _, err := s.writeWorkspaceMetadataDelta(ctx, tx, workspace, before, &target); err != nil {
		return err
	}
	tx.metadataWritten = true
	return nil
}
