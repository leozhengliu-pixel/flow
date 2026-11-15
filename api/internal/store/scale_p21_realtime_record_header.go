package store

import (
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// RealtimeRecordHeader is the JSON projection consumed by realtime rules.
type RealtimeRecordHeader struct {
	TeamID     string   `json:"teamId"`
	TeamIDs    []string `json:"teamIds"`
	Scope      string   `json:"scope"`
	OwnerID    string   `json:"ownerId"`
	ProjectID  string   `json:"projectId"`
	IssueID    string   `json:"issueId"`
	IssueIDs   []string `json:"issueIds"`
	PipelineID string   `json:"pipelineId"`
}

// RealtimeRecordRead owns the selected header through the caller's last use.
type RealtimeRecordRead struct {
	Data   RealtimeRecordHeader
	Found  bool
	source *InstalledMetadataRead
	owner  *ResourceRead
}

func (r *RealtimeRecordRead) Close() {
	if r == nil {
		return
	}
	r.Data, r.Found = RealtimeRecordHeader{}, false
	if r.source != nil {
		r.source.Close()
		r.source = nil
	}
	if r.owner != nil {
		r.owner.Close()
		r.owner = nil
	}
}

// BeginRealtimeRecordRead preserves MetadataRecord's exact collection and first
// nonempty ID lookup. Like that accessor it reads the current installed copy,
// independently of a caller's earlier logical generation.
func (s *SQLiteStore) BeginRealtimeRecordRead(ctx context.Context, workspace, field, id string) (_ *RealtimeRecordRead, workspaceFound bool, err error) {
	if id == "" {
		return nil, false, nil
	}
	ctx = context.WithValue(ctx, metadataLogicalReadKey{}, nil)
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	source, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &RealtimeRecordRead{source: source, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 128<<10); err != nil {
		return nil, true, err
	}
	index := -1
	for _, candidate := range persistedMetadataFields() {
		if candidate.name == field {
			index = candidate.index
			break
		}
	}
	if index < 0 {
		return read, true, nil
	}
	collection := source.source.fields[index]
	if collection.typ.Kind() != reflect.Slice || collection.typ.Elem().Kind() != reflect.Struct {
		return read, true, nil
	}
	idField, valid := collection.typ.Elem().FieldByName("ID")
	if !valid || idField.Type.Kind() != reflect.String || len(idField.Index) != 1 {
		return read, true, nil
	}
	view := &WebhookAggregateRead{store: s}
	defer func() {
		for _, release := range view.releases {
			release()
		}
	}()
	err = read.owner.nativeArray(ctx, collection, func(_ int, record metadataLogicalNode) error {
		cursor := metadataLogicalCursor{record, record.offset + 16, ctx}
		var identity metadataLogicalNode
		for position := 0; position <= idField.Index[0]; position++ {
			var childErr error
			identity, childErr = read.owner.nativeChild(&cursor, record.typ.Field(position).Type)
			if childErr != nil {
				return childErr
			}
		}
		matched, matchErr := read.owner.nativeEqual(ctx, identity, id)
		if matchErr != nil || !matched {
			return matchErr
		}
		// Marshal validates every encoded field before the caller decodes only
		// its header. Keep this validation independent of body projection.
		if validateErr := realtimeRecordJSONValid(ctx, view, webhookJSONValue{node: record}); validateErr != nil {
			return validateErr
		}
		plan, planErr := view.plan(ctx, record.typ)
		if planErr != nil {
			return planErr
		}
		var names []string
		header := reflect.TypeFor[RealtimeRecordHeader]()
		for _, candidate := range plan {
			for position := 0; position < header.NumField(); position++ {
				if strings.EqualFold(candidate.name, header.Field(position).Tag.Get("json")) {
					names = append(names, record.typ.Field(candidate.index[0]).Name)
					break
				}
			}
		}
		value, projectErr := read.owner.projectNode(ctx, record, names...)
		if projectErr != nil {
			return projectErr
		}
		raw, marshalErr := json.Marshal(value.Interface())
		if marshalErr != nil {
			return errWebhookJSONInvalid
		}
		if json.Unmarshal(raw, &read.Data) == nil {
			read.Found = true
		}
		return ErrMetadataIterationDone
	})
	if errors.Is(err, errWebhookJSONInvalid) || errors.Is(err, ErrMetadataIterationDone) {
		if !read.Found {
			read.Data = RealtimeRecordHeader{}
		}
		err = nil
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, true, err
	}
	return read, true, nil
}

// Validate the Marshal side only: unknown header fields may contain numbers
// outside float64's range, and every original map entry must be checked before
// JSON key normalization can make two keys collide.
func realtimeRecordJSONValid(ctx context.Context, view *WebhookAggregateRead, value webhookJSONValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value, err := view.unwrap(ctx, value)
	if err != nil || value.node.typ == nil {
		return err
	}
	node, typ := value.node, value.node.typ
	if typ == reflect.TypeFor[json.RawMessage]() {
		cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
		present, err := cursor.byte()
		if err != nil || present == 0 {
			return err
		}
		size, err := cursor.uint()
		if err != nil {
			return err
		}
		if size > uint64(node.offset+node.size-cursor.offset) {
			return ErrMetadataRecordQuery
		}
		err = validateMetadataCollectionJSON(ctx, io.NewSectionReader(node.source.file, cursor.offset, int64(size)))
		if err != nil && ctx.Err() == nil {
			return errWebhookJSONInvalid
		}
		return err
	}
	if webhookJSONCodec(typ) {
		var marshalErr error
		err := view.native(ctx, node, func(native reflect.Value) {
			_, marshalErr = json.Marshal(native.Interface())
		})
		if err != nil {
			return err
		}
		if marshalErr != nil {
			return errWebhookJSONInvalid
		}
		return nil
	}
	switch typ.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return nil
	case reflect.Array, reflect.Slice:
		if typ.Kind() == reflect.Slice && typ.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		return node.Children(ctx, func(_ reflect.Value, child metadataLogicalNode) error {
			return realtimeRecordJSONValid(ctx, view, webhookJSONValue{node: child})
		})
	case reflect.Struct:
		plan, err := view.plan(ctx, typ)
		if err != nil {
			return err
		}
		for _, field := range plan {
			child, err := view.field(ctx, value, field.index)
			if err != nil {
				return err
			}
			if child.typ == nil {
				continue
			}
			omit, err := view.omitted(ctx, child, field)
			if err != nil {
				return err
			}
			if !omit {
				if err := realtimeRecordJSONValid(ctx, view, webhookJSONValue{node: child, quoted: field.quoted}); err != nil {
					return err
				}
			}
		}
		return nil
	case reflect.Map:
		key := typ.Key()
		textKey := key.Implements(reflect.TypeFor[encoding.TextMarshaler]())
		if key.Kind() != reflect.String && (key.Kind() < reflect.Int || key.Kind() > reflect.Uintptr) && !textKey {
			return errWebhookJSONInvalid
		}
		return node.MapEntries(ctx, func(key, child metadataLogicalNode) error {
			if textKey {
				var marshalErr error
				if err := view.native(ctx, key, func(native reflect.Value) {
					if native.Kind() != reflect.Pointer || !native.IsNil() {
						_, marshalErr = native.Interface().(encoding.TextMarshaler).MarshalText()
					}
				}); err != nil {
					return err
				}
				if marshalErr != nil {
					return errWebhookJSONInvalid
				}
			}
			return realtimeRecordJSONValid(ctx, view, webhookJSONValue{node: child})
		})
	default:
		var marshalErr error
		err := view.native(ctx, node, func(native reflect.Value) {
			_, marshalErr = json.Marshal(native.Interface())
		})
		if err != nil {
			return err
		}
		if marshalErr != nil {
			return errWebhookJSONInvalid
		}
		return nil
	}
}
