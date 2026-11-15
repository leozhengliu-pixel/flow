package store

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	jsoniter "github.com/json-iterator/go"
)

const resourceWindowMaxWork = 128 << 20
const resourceWindowKeyBytes = 8 << 10

// ResourceWindowRead owns the complete admitted window summary graph. Global
// window projection still scales with summaries; this is not a 2 MiB page.
type ResourceWindowRead struct {
	Data     domain.Bootstrap
	Revision string
	owner    *ResourceRead
	work     uint64
}

func (r *ResourceWindowRead) Close() {
	if r != nil {
		r.Data, r.Revision = domain.Bootstrap{}, ""
		r.owner.Close()
	}
}

func (r *ResourceWindowRead) reserve(ctx context.Context, cost uint64) error {
	if cost > resourceWindowMaxWork-r.work {
		return ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return err
	}
	r.work += cost
	return nil
}

// BeginResourceWindowRead preserves the original current-installed choice,
// canonical fallback, projection and final revision check. Canonical core reads
// still precede the separately opened collection snapshot.
func (s *SQLiteStore) BeginResourceWindowRead(ctx context.Context, workspace, userID string, key *domain.APIKey, kind string) (_ *ResourceWindowRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return nil, err
	}
	r := &ResourceWindowRead{owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	if err = r.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	s.mu.RLock()
	_, exists := s.workspaces[workspace]
	s.metadataResidency.mu.Lock()
	source := s.metadataResidency.logical[workspace]
	current := exists && source != nil && source.Stamp() == stamp
	if current {
		err = source.Retain()
	}
	s.metadataResidency.mu.Unlock()
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if current {
		r.owner.source = source
		err = r.installed(ctx, kind)
	} else {
		err = r.canonical(ctx, workspace, kind)
	}
	if err != nil {
		return nil, err
	}
	output := r.owner.metadataOutputContext(ctx)
	r.Data, r.Revision, err = s.resourceWindowProject(output, workspace, userID, key, r.Data, stamp)
	if err == nil {
		err = metadataOutputError(output)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

func resourceWindowCollections(kind string) ([]string, error) {
	switch kind {
	case "core", "documents", "projects", "initiatives", "cycles":
		return []string{"documents", "projects", "initiatives", "cycles"}, nil
	case "trash":
		return []string{"documents", "projects", "initiatives", "cycles", "trash"}, nil
	case "users", "members", "teamMembers", "departedMembers", "invitations":
		return nil, nil
	}
	return nil, ErrMetadataRecordQuery
}

func resourceWindowNativeFields(typ reflect.Type) []string {
	fields := []string{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if (metadataSummaryFields[name] || name == "resources") && name != "milestones" {
			fields = append(fields, field.Name)
		}
	}
	return fields
}

func (r *ResourceWindowRead) installed(ctx context.Context, kind string) error {
	collections, err := resourceWindowCollections(kind)
	if err != nil {
		return err
	}
	fields := append([]string{"workspace", "viewer", "viewerRole"}, resourceWindowCoreFields...)
	fields = append(fields, collections...)
	nextField, _ := reflect.TypeFor[domain.Bootstrap]().FieldByName("NextIssueNumber")
	nextNode := r.owner.source.fields[nextField.Index[0]]
	projected := uint64(4096) + nextNode.cost*8 + 128<<10
	for _, name := range fields {
		index, _, _ := metadataLogicalField(name)
		node := r.owner.source.fields[index]
		var cost uint64
		if slices.Contains(collections, name) {
			excluded := resourceWindowNativeFields(node.typ.Elem())
			if name == "trash" {
				excluded = directoryOutputFields(node.typ.Elem(), "Payload", "Links")
			}
			err = r.owner.nativeArray(ctx, node, func(_ int, child metadataLogicalNode) error {
				part, err := p19SnapshotHeaderWork(ctx, child, excluded)
				if err != nil {
					return err
				}
				if part > resourceWindowMaxWork-cost {
					return ErrMetadataWorkBudget
				}
				cost += part
				return nil
			})
		} else if node.cost > (resourceWindowMaxWork-(128<<10))/8 {
			return ErrMetadataWorkBudget
		} else {
			cost = node.cost*8 + 128<<10
		}
		if err != nil {
			return err
		}
		if cost > resourceWindowMaxWork-projected {
			return ErrMetadataWorkBudget
		}
		projected += cost
	}
	if projected > (resourceWindowMaxWork-r.work)/4 {
		return ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, projected*3); err != nil {
		return err
	}
	r.work += projected * 4
	target := reflect.ValueOf(&r.Data).Elem()
	header := &DocumentDirectoryRead{owner: r.owner}
	for _, name := range fields {
		index, field, _ := metadataLogicalField(name)
		node := r.owner.source.fields[index]
		if !slices.Contains(collections, name) {
			value, err := r.owner.node(ctx, node, false)
			if err != nil {
				return err
			}
			target.Field(field.Index[0]).Set(value)
			continue
		}
		value := target.Field(field.Index[0])
		value.Set(reflect.MakeSlice(value.Type(), 0, 0))
		selected := resourceWindowNativeFields(node.typ.Elem())
		if name == "trash" {
			selected = directoryOutputFields(node.typ.Elem(), "Payload", "Links")
		}
		if err := r.owner.nativeArray(ctx, node, func(_ int, child metadataLogicalNode) error {
			item, err := header.header(ctx, child, selected)
			if err != nil {
				return err
			}
			if name == "trash" {
				payload, err := r.trashPayload(ctx, child)
				if err != nil {
					return err
				}
				entry := item.Interface().(domain.TrashEntry)
				entry.Payload = payload
				item = reflect.ValueOf(entry)
			}
			value.Set(reflect.Append(value, item))
			return nil
		}); err != nil {
			return err
		}
	}
	next, err := r.owner.node(ctx, nextNode, false)
	if err != nil {
		return err
	}
	r.Data.NextIssueNumber = int(next.Int())
	// This is the same clone boundary as the literal warm path, including its
	// intentional preservation of root Viewer.Via.
	selected := append(slices.Clone(resourceWindowCoreFields), collections...)
	r.Data = metadataRecordSnapshot(r.Data, MetadataRecordScope{Fields: selected}, selected)
	return ctx.Err()
}

type resourceWindowTrashHeader struct {
	ID              string   `json:"id"`
	SlugID          string   `json:"slugId"`
	PreviousSlugIDs []string `json:"previousSlugIds,omitempty"`
	Title           string   `json:"title"`
	Icon            string   `json:"icon,omitempty"`
	Color           string   `json:"color,omitempty"`
	TeamIDs         []string `json:"teamIds"`
	ProjectIDs      []string `json:"projectIds"`
}

func (r *ResourceWindowRead) trashPayload(ctx context.Context, trash metadataLogicalNode) (json.RawMessage, error) {
	var node metadataLogicalNode
	if err := trash.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() == "Payload" {
			node = child
		}
		return nil
	}); err != nil {
		return nil, err
	}
	cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
	present, err := cursor.byte()
	if err != nil {
		return nil, err
	}
	if present == 0 {
		return nil, ErrResourceAccessChanged
	}
	length, err := cursor.uint()
	if err != nil || length > uint64(node.size) || cursor.offset+int64(length) > node.offset+node.size {
		return nil, ErrMetadataRecordQuery
	}
	offset := cursor.offset
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(io.NewSectionReader(node.source.file, offset, int64(length)), metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		return nil, err
	}
	var result resourceWindowTrashHeader
	first, err := scanner.peek()
	if err == nil && first == 'n' {
		err = scanner.literal("null")
	} else if err == nil {
		err = scanner.object(func(key, value metadataCollectionRange) error {
			// Every case-folded alias of these ASCII fields fits this bound,
			// including JSON unicode escape spellings.
			if key.size > 256 {
				return nil
			}
			raw := make([]byte, int(key.size))
			if _, err := node.source.file.ReadAt(raw, offset+key.offset); err != nil {
				return err
			}
			var name string
			if err := json.Unmarshal(raw, &name); err != nil {
				return err
			}
			field, ok := resourceWindowJSONField(reflect.TypeFor[resourceWindowTrashHeader](), name)
			if !ok {
				return nil
			}
			if value.size < 0 || uint64(value.size) > resourceWindowMaxWork/4 {
				return ErrMetadataWorkBudget
			}
			if err := r.reserve(ctx, uint64(value.size)*4+2048); err != nil {
				return err
			}
			raw = make([]byte, int(value.size))
			if _, err := node.source.file.ReadAt(raw, offset+value.offset); err != nil {
				return err
			}
			if err := r.admitJSON(ctx, raw, field.Type); err != nil {
				return err
			}
			return json.Unmarshal(raw, reflect.ValueOf(&result).Elem().Field(field.Index[0]).Addr().Interface())
		}, 0)
	}
	if err == nil {
		err = scanner.end()
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, ErrMetadataWorkBudget) {
			return nil, err
		}
		return nil, ErrResourceAccessChanged
	}
	if err := r.admitEncoding(ctx, result); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func resourceWindowJSONField(typ reflect.Type, name string) (reflect.StructField, bool) {
	var folded reflect.StructField
	found := false
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" || field.Tag.Get("json") == "-" {
			continue
		}
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" {
			key = field.Name
		}
		if key == name {
			return field, true
		}
		if !found && strings.EqualFold(key, name) {
			folded, found = field, true
		}
	}
	return folded, found
}

// Count typed values before json.Unmarshal, not just JSON bytes or objects.
// For example [null,null] still allocates two full User structs in []User.
func resourceWindowTypedJSONWork(ctx context.Context, raw []byte, typ reflect.Type) (uint64, error) {
	if !json.Valid(raw) {
		var value struct{}
		return 0, json.Unmarshal(raw, &value)
	}
	base, err := metadataAggregatePayloadJSONWork(ctx, raw)
	if err != nil {
		return 0, err
	}
	if base > resourceWindowMaxWork {
		return 0, ErrMetadataWorkBudget
	}
	var readers [metadataLogicalDepth]*bufio.Reader
	var walk func([]byte, reflect.Type, int) error
	walk = func(raw []byte, typ reflect.Type, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth >= metadataLogicalDepth {
			return ErrMetadataWorkBudget
		}
		cost := uint64(typ.Size())*2 + 128
		if cost > resourceWindowMaxWork-base {
			return ErrMetadataWorkBudget
		}
		base += cost
		if typ.Kind() == reflect.Pointer {
			return walk(raw, typ.Elem(), depth+1)
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || typ.Kind() == reflect.Interface {
			return nil
		}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			if raw[0] != '[' {
				return nil
			}
		case reflect.Struct:
			if typ == reflect.TypeFor[time.Time]() || raw[0] != '{' {
				return nil
			}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.Anonymous && strings.Split(field.Tag.Get("json"), ",")[0] == "" {
					return ErrBoundedMetadataCoverage
				}
			}
		case reflect.Map:
			if raw[0] != '{' {
				return nil
			}
		default:
			return nil
		}
		if readers[depth] == nil {
			readers[depth] = bufio.NewReaderSize(bytes.NewReader(raw), 256)
		} else {
			readers[depth].Reset(bytes.NewReader(raw))
		}
		scanner := metadataCollectionScanner{ctx: ctx, input: readers[depth]}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			return scanner.array(func(value metadataCollectionRange) error {
				return walk(raw[value.offset:value.offset+value.size], typ.Elem(), depth+1)
			}, depth)
		case reflect.Struct:
			return scanner.object(func(key, value metadataCollectionRange) error {
				var name string
				if err := json.Unmarshal(raw[key.offset:key.offset+key.size], &name); err != nil {
					return err
				}
				field, found := resourceWindowJSONField(typ, name)
				if !found {
					return nil
				}
				return walk(raw[value.offset:value.offset+value.size], field.Type, depth+1)
			}, depth)
		case reflect.Map:
			return scanner.object(func(_, value metadataCollectionRange) error {
				part := uint64(typ.Key().Size())*2 + 128
				if part > resourceWindowMaxWork-base {
					return ErrMetadataWorkBudget
				}
				base += part
				return walk(raw[value.offset:value.offset+value.size], typ.Elem(), depth+1)
			}, depth)
		}
		return nil
	}
	if err := walk(raw, typ, 0); err != nil {
		return 0, err
	}
	return base, nil
}

func (r *ResourceWindowRead) admitJSON(ctx context.Context, raw []byte, typ reflect.Type) error {
	cost, err := resourceWindowTypedJSONWork(ctx, raw, typ)
	if err != nil {
		return err
	}
	if cost > (resourceWindowMaxWork-r.work)/3 {
		return ErrMetadataWorkBudget
	}
	return r.reserve(ctx, cost*3+1)
}

func (r *ResourceWindowRead) admitEncoding(ctx context.Context, value any) error {
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
	if err != nil {
		return err
	}
	return r.reserve(ctx, uint64(cost)+4096)
}

func (r *ResourceWindowRead) canonical(ctx context.Context, workspace, kind string) error {
	collections, err := resourceWindowCollections(kind)
	if err != nil {
		return err
	}
	// Fixed scratch covers bounded SQL key/value rows, the original 2 MiB
	// core raw accumulation and its active JSON preflight keys/codec buffers.
	if err := r.reserve(ctx, 16<<20); err != nil {
		return err
	}
	r.Data, err = r.canonicalCore(ctx, workspace)
	if err != nil {
		return err
	}
	tx, err := r.owner.store.db.BeginTx(ctx, metadataReadOptions(r.owner.store.dialect))
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, collection := range collections {
		for offset := 0; ; offset += 128 {
			rows, err := tx.QueryContext(ctx, `SELECT LENGTH(record_key),CASE WHEN LENGTH(record_key)<=? THEN record_key ELSE NULL END,LENGTH(data) FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key LIMIT 128 OFFSET ?`, resourceWindowKeyBytes/4, workspace, collection, offset)
			if err != nil {
				return err
			}
			type candidate struct {
				id   string
				size int64
			}
			candidates := []candidate{}
			for rows.Next() {
				var keyBytes, size int64
				var id sql.NullString
				if err := rows.Scan(&keyBytes, &id, &size); err != nil {
					rows.Close()
					return err
				}
				if keyBytes < 0 || keyBytes > resourceWindowKeyBytes/4 || !id.Valid {
					rows.Close()
					return ErrMetadataWorkBudget
				}
				candidates = append(candidates, candidate{id: id.String, size: size})
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, item := range candidates {
				raw, err := r.canonicalSummary(ctx, tx, workspace, RecordKey{Kind: collection, ID: item.id}, item.size)
				if err != nil {
					return err
				}
				_, field, _ := metadataLogicalField(collection)
				target := reflect.ValueOf(&r.Data).Elem().Field(field.Index[0])
				if err := r.admitJSON(ctx, raw, target.Type().Elem()); err != nil {
					return err
				}
				value := reflect.New(target.Type().Elem())
				if err := json.Unmarshal(raw, value.Interface()); err != nil {
					return err
				}
				target.Set(reflect.Append(target, value.Elem()))
			}
			if len(candidates) < 128 {
				break
			}
		}
	}
	normalizeStoredMetadata(&r.Data)
	return nil
}

func (r *ResourceWindowRead) canonicalCore(ctx context.Context, workspace string) (domain.Bootstrap, error) {
	s := r.owner.store
	root := map[string]json.RawMessage{}
	fields := append([]string{"workspace", "viewer", "viewerRole", metadataCollectionsKey}, resourceWindowCoreFields...)
	remaining := 2 << 20
	for _, field := range fields {
		expression := s.jsonText("data", field)
		var size sql.NullInt64
		var value sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT LENGTH(`+expression+`),CASE WHEN LENGTH(`+expression+`)<=? THEN `+expression+` ELSE NULL END FROM workspace_states WHERE workspace_key=?`, MetadataRecordPageMaxBytes, workspace).Scan(&size, &value); err != nil {
			return domain.Bootstrap{}, err
		}
		if size.Valid && size.Int64 > MetadataRecordPageMaxBytes {
			return domain.Bootstrap{}, ErrMetadataPageBudget
		}
		if !value.Valid {
			continue
		}
		raw := json.RawMessage(value.String)
		if field == "viewerRole" {
			raw, _ = json.Marshal(value.String)
		}
		remaining -= len(raw)
		if remaining < 0 {
			return domain.Bootstrap{}, ErrMetadataPageBudget
		}
		root[field] = raw
	}
	var collections map[string]string
	if raw := root[metadataCollectionsKey]; len(raw) > 0 {
		if err := r.admitJSON(ctx, raw, reflect.TypeFor[map[string]string]()); err != nil {
			return domain.Bootstrap{}, err
		}
		if err := json.Unmarshal(raw, &collections); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	delete(root, metadataCollectionsKey)
	for _, field := range resourceWindowCoreFields {
		kind := collections[field]
		if kind == "" {
			continue
		}
		array := []json.RawMessage{}
		object := map[string]json.RawMessage{}
		for offset := 0; ; offset += 128 {
			rows, err := s.db.QueryContext(ctx, `SELECT LENGTH(record_key),CASE WHEN LENGTH(record_key)<=? THEN record_key ELSE NULL END,LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key LIMIT 128 OFFSET ?`, resourceWindowKeyBytes/4, MetadataRecordPageMaxBytes, workspace, field, offset)
			if err != nil {
				return domain.Bootstrap{}, err
			}
			count := 0
			for rows.Next() {
				var keyBytes int64
				var id sql.NullString
				var size int
				var raw []byte
				if err := rows.Scan(&keyBytes, &id, &size, &raw); err != nil {
					rows.Close()
					return domain.Bootstrap{}, err
				}
				if keyBytes < 0 || keyBytes > resourceWindowKeyBytes/4 || !id.Valid {
					rows.Close()
					return domain.Bootstrap{}, ErrMetadataWorkBudget
				}
				remaining -= size
				if size > MetadataRecordPageMaxBytes || raw == nil || remaining < 0 {
					rows.Close()
					return domain.Bootstrap{}, ErrMetadataPageBudget
				}
				if err := r.reserve(ctx, uint64(len(id.String))*8+uint64(len(raw))*4+1024); err != nil {
					rows.Close()
					return domain.Bootstrap{}, err
				}
				if kind == "array" {
					array = append(array, raw)
				} else {
					object[id.String] = raw
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return domain.Bootstrap{}, err
			}
			if count < 128 {
				break
			}
		}
		var value any = object
		if kind == "array" {
			value = array
		}
		if err := r.admitEncoding(ctx, value); err != nil {
			return domain.Bootstrap{}, err
		}
		root[field], _ = json.Marshal(value)
	}
	if err := r.admitEncoding(ctx, root); err != nil {
		return domain.Bootstrap{}, err
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	if err := r.admitJSON(ctx, raw, reflect.TypeFor[domain.Bootstrap]()); err != nil {
		return domain.Bootstrap{}, err
	}
	var data domain.Bootstrap
	err = json.Unmarshal(raw, &data)
	return data, err
}

func (r *ResourceWindowRead) canonicalSummary(ctx context.Context, tx *sqlTx, workspace string, key RecordKey, size int64) (json.RawMessage, error) {
	reader := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: key, size: size}
	iter := jsoniter.Parse(jsoniter.ConfigCompatibleWithStandardLibrary, reader, 4096)
	used := 0
	var project func(bool) map[string]json.RawMessage
	project = func(payload bool) map[string]json.RawMessage {
		result := map[string]json.RawMessage{}
		for iter.Error == nil {
			reader.boundedCapture, reader.captureLeft = true, metadataSummaryMaxBytes-used
			field := iter.ReadObject()
			reader.boundedCapture = false
			if field == "" {
				break
			}
			if field == "payload" && !payload {
				raw, _ := json.Marshal(project(true))
				result[field] = raw
			} else if (metadataSummaryFields[field] || field == "resources") && field != "milestones" {
				reader.boundedCapture, reader.captureLeft = true, metadataSummaryMaxBytes-used
				raw := iter.SkipAndReturnBytes()
				reader.boundedCapture = false
				used += len(raw) + len(field) + 4
				if used > metadataSummaryMaxBytes {
					break
				}
				result[field] = raw
			} else {
				iter.Skip()
			}
		}
		return result
	}
	result := project(false)
	if iter.Error != nil || used > metadataSummaryMaxBytes || reader.captureExceeded {
		return nil, ErrMetadataPageBudget
	}
	return json.Marshal(result)
}
