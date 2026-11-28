package store

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/jsonwire"
)

const metadataJSONLargeBody = 16 << 10

type metadataJSONBodyField struct {
	index int
	key   []byte
}

var metadataJSONBodyFields = map[reflect.Type][]metadataJSONBodyField{
	reflect.TypeFor[domain.Issue]():            metadataJSONBodiesFor[domain.Issue]("Description", "DescriptionState", "DocumentContent"),
	reflect.TypeFor[domain.IssueCreateInput](): metadataJSONBodiesFor[domain.IssueCreateInput]("Description", "DescriptionState", "ContentState"),
	reflect.TypeFor[domain.IssueUpdateInput](): metadataJSONBodiesFor[domain.IssueUpdateInput]("Description", "DescriptionState", "ContentState"),
	reflect.TypeFor[domain.Document]():         metadataJSONBodiesFor[domain.Document]("Content", "ContentState"),
	reflect.TypeFor[domain.DocumentInput]():    metadataJSONBodiesFor[domain.DocumentInput]("Content", "ContentState"),
	reflect.TypeFor[domain.DocumentContent]():  metadataJSONBodiesFor[domain.DocumentContent]("Content", "ContentState"),
	reflect.TypeFor[domain.DocumentRevision](): metadataJSONBodiesFor[domain.DocumentRevision]("Content", "ContentState"),
	reflect.TypeFor[domain.Comment]():          metadataJSONBodiesFor[domain.Comment]("Body"),
	reflect.TypeFor[domain.Project]():          metadataJSONBodiesFor[domain.Project]("Description"),
	reflect.TypeFor[domain.ProjectUpdate]():    metadataJSONBodiesFor[domain.ProjectUpdate]("Body"),
	reflect.TypeFor[domain.Initiative]():       metadataJSONBodiesFor[domain.Initiative]("Description"),
	reflect.TypeFor[domain.InitiativeUpdate](): metadataJSONBodiesFor[domain.InitiativeUpdate]("Body"),
	reflect.TypeFor[domain.Notification]():     metadataJSONBodiesFor[domain.Notification]("Text"),
	reflect.TypeFor[domain.Loop]():             metadataJSONBodiesFor[domain.Loop]("Instructions"),
	reflect.TypeFor[domain.Release]():          metadataJSONBodiesFor[domain.Release]("Description"),
}

var metadataJSONDocumentRevisions = metadataJSONBodyFor[domain.Document]("Revisions")

var metadataJSONEmptyDocumentContent = func() string {
	raw, err := json.Marshal(domain.DocumentContent{})
	if err != nil {
		panic(err)
	}
	return string(raw)
}()

func metadataJSONBodyFor[T any](name string) metadataJSONBodyField {
	field, ok := reflect.TypeFor[T]().FieldByName(name)
	if !ok || len(field.Index) != 1 || field.Index[0] >= 64 {
		panic("invalid metadata JSON field: " + name)
	}
	wire, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	return metadataJSONBodyField{index: field.Index[0], key: []byte(`"` + wire + `":`)}
}

func metadataJSONBodiesFor[T any](names ...string) []metadataJSONBodyField {
	fields := make([]metadataJSONBodyField, 0, len(names))
	for _, name := range names {
		fields = append(fields, metadataJSONBodyFor[T](name))
	}
	slices.SortFunc(fields, func(a, b metadataJSONBodyField) int { return a.index - b.index })
	return fields
}

func metadataJSONLargeBodies(value reflect.Value) (fields []metadataJSONBodyField, selected uint64) {
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil, 0
	}
	fields = metadataJSONBodyFields[value.Type()]
	for _, field := range fields {
		item := reflect.Indirect(value.Field(field.index))
		if !item.IsValid() {
			continue
		}
		large := false
		if item.Kind() == reflect.String {
			large = item.Len() >= metadataJSONLargeBody
		} else {
			_, nested := metadataJSONLargeBodies(item)
			large = nested != 0
		}
		if large {
			selected |= uint64(1) << field.index
		}
	}
	return fields, selected
}

func metadataJSONLargeRevisions(value reflect.Value) bool {
	if !value.IsValid() || value.Type() != reflect.TypeFor[domain.Document]() {
		return false
	}
	items := value.Field(metadataJSONDocumentRevisions.index)
	for i := 0; i < items.Len(); i++ {
		if _, selected := metadataJSONLargeBodies(items.Index(i)); selected != 0 {
			return true
		}
	}
	return false
}

// marshalMetadataJSON lets encoding/json write the record's keys and every
// ordinary field. Large text bodies go directly into a pre-sized result.
func marshalMetadataJSON(value any) ([]byte, error) {
	switch value := value.(type) {
	case string:
		if len(value) >= metadataJSONLargeBody {
			width, err := metadataJSONStringWidth(context.Background(), value, math.MaxInt)
			if err != nil {
				return nil, err
			}
			return jsonwire.AppendString(make([]byte, 0, int(width)), value), nil
		}
	case json.RawMessage:
		return marshalMetadataRawJSON(value)
	case map[string]json.RawMessage:
		return marshalMetadataRawObject(value)
	case []json.RawMessage:
		return marshalMetadataRawList(value)
	}
	v := reflect.ValueOf(value)
	fields, selected := metadataJSONLargeBodies(v)
	revisions := metadataJSONLargeRevisions(v)
	if selected == 0 && !revisions {
		return json.Marshal(value)
	}
	shell := reflect.New(v.Type()).Elem()
	shell.Set(v)
	marker := "-"
	const quotedMarker = `"-"`
	for _, field := range fields {
		if selected&(uint64(1)<<field.index) != 0 {
			// Keep optional fields in their original position as well.
			item := shell.Field(field.index)
			switch {
			case item.Type() == reflect.TypeFor[*domain.DocumentContent]():
				item.Set(reflect.ValueOf(&domain.DocumentContent{}))
			case item.Kind() == reflect.Pointer:
				item.Set(reflect.ValueOf(&marker))
			default:
				item.SetString(marker)
			}
		}
	}
	if revisions {
		shell.Field(metadataJSONDocumentRevisions.index).SetZero()
	}
	raw, err := json.Marshal(shell.Interface())
	if err != nil {
		return nil, err
	}
	width := uint64(len(raw))
	var nested []byte
	for _, field := range fields {
		if selected&(uint64(1)<<field.index) != 0 {
			item := reflect.Indirect(v.Field(field.index))
			placeholder := quotedMarker
			var bodyWidth uint64
			if item.Kind() == reflect.String {
				bodyWidth, err = metadataJSONStringWidth(context.Background(), item.String(), math.MaxInt)
			} else {
				nested, err = marshalMetadataJSON(item.Interface())
				bodyWidth = uint64(len(nested))
				placeholder = metadataJSONEmptyDocumentContent
			}
			if err != nil {
				return nil, err
			}
			width -= uint64(len(placeholder))
			if bodyWidth > math.MaxInt-width {
				return nil, ErrMetadataWorkBudget
			}
			width += bodyWidth
		}
	}
	itemsAt := len(raw)
	var items [][]byte
	if revisions {
		itemsAt = metadataJSONTopLevelMember(raw, metadataJSONDocumentRevisions.key, "null")
		if itemsAt < 0 {
			return nil, ErrBoundedMetadataCoverage
		}
		list := v.Field(metadataJSONDocumentRevisions.index)
		items = make([][]byte, list.Len())
		width += uint64(list.Len() + 1)
		width -= 4 // replace null with brackets, commas and the items
		for i := range items {
			items[i], err = marshalMetadataJSON(list.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			if uint64(len(items[i])) > math.MaxInt-width {
				return nil, ErrMetadataWorkBudget
			}
			width += uint64(len(items[i]))
		}
	}
	out := make([]byte, 0, int(width))
	copied := 0
	for _, field := range fields {
		if selected&(uint64(1)<<field.index) == 0 {
			continue
		}
		item := reflect.Indirect(v.Field(field.index))
		placeholder := quotedMarker
		if item.Kind() != reflect.String {
			placeholder = metadataJSONEmptyDocumentContent
		}
		start := metadataJSONTopLevelMember(raw, field.key, placeholder)
		if start < copied || start >= itemsAt {
			return nil, ErrBoundedMetadataCoverage
		}
		out = append(out, raw[copied:start]...)
		if item.Kind() == reflect.String {
			out = jsonwire.AppendString(out, item.String())
		} else {
			out = append(out, nested...)
		}
		copied = start + len(placeholder)
	}
	if revisions {
		out = append(out, raw[copied:itemsAt]...)
		out = append(out, '[')
		for i, item := range items {
			if i != 0 {
				out = append(out, ',')
			}
			out = append(out, item...)
		}
		out = append(out, ']')
		copied = itemsAt + 4
	}
	return append(out, raw[copied:]...), nil
}

// MarshalIssueComparisonJSON encodes the issue as the mutation callback sees
// it. Unlike the SQL columns, this comparison includes read decorations.
func MarshalIssueComparisonJSON(issue *domain.Issue) ([]byte, error) {
	if issue == nil || max(len(issue.Description), len(issue.DescriptionState)) < metadataJSONLargeBody &&
		(issue.DocumentContent == nil || max(len(issue.DocumentContent.Content), len(issue.DocumentContent.ContentState)) < metadataJSONLargeBody) {
		return json.Marshal(issue)
	}
	return marshalMetadataJSON(*issue)
}

// raw is a JSON object already produced by Marshal. A matching string
// followed by a colon at depth one can only be an outer member name.
func metadataJSONTopLevelMember(raw, key []byte, placeholder string) int {
	depth := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case '"':
			if depth == 1 && bytes.HasPrefix(raw[i:], key) {
				start := i + len(key)
				if bytes.HasPrefix(raw[start:], []byte(placeholder)) {
					return start
				}
				return -1
			}
			for i++; i < len(raw) && raw[i] != '"'; i++ {
				if raw[i] == '\\' {
					i++
				}
			}
		}
	}
	return -1
}

// metadataRecordEncodingWork is paired with marshalMetadataJSON. The shell
// uses the regular Marshal allowance, with room to copy it and round the
// returned allocation. The body only needs the final slice (and a separate
// NUL-stripped copy where a SQL JSON column requires one).
func metadataRecordEncodingWork(ctx context.Context, value reflect.Value) (int, error) {
	if value.IsValid() && value.Kind() == reflect.String && value.Type() == reflect.TypeFor[string]() && value.Len() >= metadataJSONLargeBody {
		work, err := metadataAggregateStringEncodingWork(ctx, value.String())
		if work > uint64(metadataWorkBytes())-8192 {
			return 0, ErrMetadataWorkBudget
		}
		return int(work) + 8192, err
	}
	if value.IsValid() && value.Type() == metadataAggregateRawPayloadType && value.Len() >= metadataJSONLargeBody {
		work, err := metadataRawEncodingWork(ctx, value.Bytes(), true)
		if work > uint64(metadataWorkBytes()) {
			return 0, ErrMetadataWorkBudget
		}
		return int(work), err
	}
	if value.IsValid() && value.Type() == reflect.TypeFor[map[string]json.RawMessage]() {
		fields := value.Interface().(map[string]json.RawMessage)
		if metadataJSONLargeRawObject(fields) {
			return metadataRawObjectEncodingWork(ctx, fields)
		}
	}
	_, selected := metadataJSONLargeBodies(value)
	return metadataPayloadEncodingWork(ctx, value, 0, selected, metadataJSONLargeRevisions(value))
}

// Bounded writers can put a large record and its input in the same event.
// Keep the literal envelope's decoded semantics and sorted keys, but avoid
// growing a second full-size encoding buffer around the record.
func enrichMetadataRealtimePayload(payload json.RawMessage, entity any, eventType string) json.RawMessage {
	value := reflect.ValueOf(entity)
	_, selected := metadataJSONLargeBodies(value)
	if len(payload) < metadataJSONLargeBody && selected == 0 && !metadataJSONLargeRevisions(value) {
		return enrichRealtimePayload(payload, entity, eventType)
	}
	fields := realtimePayloadFields(payload, entity, eventType)
	encoded := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		raw, err := marshalMetadataJSON(value)
		if err != nil {
			return payload
		}
		encoded[key] = raw
	}
	raw, err := marshalMetadataRawObject(encoded)
	if err != nil {
		return payload
	}
	return raw
}

func (s *SQLiteStore) encodeMetadataRealtimePayload(ctx context.Context, owner *ResourceRead, payload json.RawMessage, entity any, eventType string) ([]byte, error) {
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	// Unmarshal first tries an object and then the generic shape. Each
	// attempt allocates at most an unquoted buffer and decoded string per
	// quoted value, with separate work for the generic containers.
	decode, err := issueRecordDecodeWork(ctx, payload)
	if err != nil {
		return nil, err
	}
	if err := scratch.reserve(ctx, 2*decode); err != nil {
		return nil, err
	}
	fields := realtimePayloadFields(payload, entity, eventType)
	value := reflect.ValueOf(entity)
	_, selected := metadataJSONLargeBodies(value)
	if len(payload) < metadataJSONLargeBody && selected == 0 && !metadataJSONLargeRevisions(value) {
		return encodeOwnedMetadataValue(ctx, owner, fields)
	}
	if err := scratch.reserve(ctx, 256*uint64(len(fields))+1024); err != nil {
		return nil, err
	}
	encoded := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		raw, err := encodeOwnedMetadataValue(ctx, scratch, value)
		if err != nil {
			return nil, err
		}
		encoded[key] = raw
	}
	return encodeOwnedMetadataValue(ctx, owner, encoded)
}

func encodeOwnedMetadataValue(ctx context.Context, owner *ResourceRead, value any) ([]byte, error) {
	if owner == nil {
		return marshalMetadataJSON(value)
	}
	raw, release, err := owner.store.encodeMetadataMutationPayload(ctx, value)
	if err != nil {
		return nil, err
	}
	owner.release = append(owner.release, release)
	return raw, nil
}

func encodeOwnedMetadataRecord(ctx context.Context, owner *ResourceRead, value reflect.Value) ([]byte, error) {
	work, err := metadataRecordEncodingWork(ctx, value)
	if err != nil {
		return nil, err
	}
	if err := owner.reserve(ctx, uint64(work)); err != nil {
		return nil, err
	}
	return marshalMetadataJSON(value.Interface())
}

// Comparison phases retain serialized records without taking another
// encoding allowance for each record already in hand.
func encodeRetainedMetadataRecord(ctx context.Context, owner *ResourceRead, value reflect.Value) ([]byte, error) {
	raw, release, err := owner.store.encodeMetadataJSONRetained(ctx, value, marshalMetadataRecord)
	if err != nil {
		return nil, err
	}
	owner.release = append(owner.release, release)
	return raw, nil
}

// marshalMetadataRawJSON matches Marshal(RawMessage). Large messages are
// validated and compacted directly into one correctly sized allocation.
func marshalMetadataRawJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) < metadataJSONLargeBody {
		return json.Marshal(raw)
	}
	if err := validateMetadataRawJSON(raw); err != nil {
		return nil, err
	}
	size, _ := jsonwire.Compact(nil, raw)
	_, out := jsonwire.Compact(make([]byte, 0, size), raw)
	return out, nil
}

func validateMetadataRawJSON(raw json.RawMessage) error {
	if raw == nil || json.Valid(raw) {
		return nil
	}
	var discarded json.RawMessage
	err := json.Unmarshal(raw, &discarded)
	return &json.MarshalerError{Type: metadataAggregateRawPayloadType, Err: err}
}

// Raw member maps are used by merge, marker resolution and comparison. Their
// values are already JSON. Write each directly after measuring the result,
// preserving encoding/json's ordering and spelling.
func marshalMetadataRawObject(fields map[string]json.RawMessage) ([]byte, error) {
	if !metadataJSONLargeRawObject(fields) {
		return json.Marshal(fields)
	}
	keys := metadataRawObjectKeys(fields)
	size, err := metadataRawObjectBytes(fields, keys)
	if err != nil {
		return nil, err
	}
	return appendMetadataRawObject(make([]byte, 0, size), fields, keys), nil
}

func metadataJSONLargeRawObject(fields map[string]json.RawMessage) bool {
	total := 0
	for key, value := range fields {
		total += len(key) + len(value)
		if total >= metadataJSONLargeBody {
			return true
		}
	}
	return false
}

func metadataRawObjectEncodingWork(ctx context.Context, fields map[string]json.RawMessage) (int, error) {
	cost := uint64(8192) + 128*uint64(len(fields))
	for key, raw := range fields {
		keyWork, err := metadataAggregateStringEncodingWork(ctx, key)
		if err != nil {
			return 0, err
		}
		width, expand, scanner, _, err := metadataRawJSONSize(ctx, raw)
		if err != nil {
			return 0, err
		}
		cost += keyWork + (width+expand)*2 + scanner
		if cost > uint64(metadataWorkBytes()) {
			return 0, ErrMetadataWorkBudget
		}
	}
	return int(cost), nil
}

func metadataRawObjectKeys(fields map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func metadataRawObjectBytes(fields map[string]json.RawMessage, keys []string) (int, error) {
	if fields == nil {
		return 4, nil
	}
	ctx := context.Background()
	limit := uint64(math.MaxInt)
	size := uint64(max(0, len(keys)-1) + 2)
	for _, key := range keys {
		raw := fields[key]
		if err := validateMetadataRawJSON(raw); err != nil {
			return 0, err
		}
		width, err := metadataJSONStringWidth(ctx, key, limit)
		if err != nil {
			return 0, err
		}
		valueWidth := 4 // null
		if raw != nil {
			valueWidth, _ = jsonwire.Compact(nil, raw)
		}
		size += width + 1 + uint64(valueWidth)
		if size > limit {
			return 0, ErrMetadataWorkBudget
		}
	}
	return int(size), nil
}

func appendMetadataRawObject(out []byte, fields map[string]json.RawMessage, keys []string) []byte {
	if fields == nil {
		return append(out, "null"...)
	}
	out = append(out, '{')
	for i, key := range keys {
		if i != 0 {
			out = append(out, ',')
		}
		out = jsonwire.AppendString(out, key)
		out = append(out, ':')
		if raw := fields[key]; raw != nil {
			_, out = jsonwire.Compact(out, raw)
		} else {
			out = append(out, "null"...)
		}
	}
	return append(out, '}')
}

func marshalMetadataRawList(items []json.RawMessage) ([]byte, error) {
	total := 0
	for _, item := range items {
		total += len(item) + 1
		if total >= metadataJSONLargeBody {
			break
		}
	}
	if total < metadataJSONLargeBody {
		return json.Marshal(items)
	}
	size := len(items) + 1 // commas and brackets
	for _, item := range items {
		if err := validateMetadataRawJSON(item); err != nil {
			return nil, err
		}
		width := 4
		if item != nil {
			width, _ = jsonwire.Compact(nil, item)
		}
		if width > math.MaxInt-size {
			return nil, ErrMetadataWorkBudget
		}
		size += width
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for i, item := range items {
		if i != 0 {
			out = append(out, ',')
		}
		if item == nil {
			out = append(out, "null"...)
		} else {
			_, out = jsonwire.Compact(out, item)
		}
	}
	return append(out, ']'), nil
}

func marshalMetadataRawObjects(items []map[string]json.RawMessage) ([]byte, error) {
	total := len(items)
	for _, item := range items {
		for key, value := range item {
			total += len(key) + len(value)
		}
		if total >= metadataJSONLargeBody {
			break
		}
	}
	if total < metadataJSONLargeBody {
		return json.Marshal(items)
	}
	// Assemble directly into the list buffer. Many small maps would
	// otherwise allocate a Marshal result each and keep them all live.
	keys := make([][]string, len(items))
	size := len(items) + 1
	for i, item := range items {
		keys[i] = metadataRawObjectKeys(item)
		width, err := metadataRawObjectBytes(item, keys[i])
		if err != nil {
			return nil, err
		}
		if width > math.MaxInt-size {
			return nil, ErrMetadataWorkBudget
		}
		size += width
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for i, item := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = appendMetadataRawObject(out, item, keys[i])
	}
	return append(out, ']'), nil
}

func marshalMetadataUpdate(update metadataUpdate) ([]byte, error) {
	if len(update.Parent)+len(update.Item) < metadataJSONLargeBody {
		return json.Marshal(update)
	}
	if len(update.Item) > 0 {
		if err := validateMetadataRawJSON(update.Item); err != nil {
			return nil, err
		}
	}
	const parentKey, itemKey, nullKey = `{"parent":`, `,"item":`, `,"null":true`
	width, _ := jsonwire.Compact(nil, update.Item)
	size := int64(len(parentKey)+1) + jsonwire.StringBytes(update.Parent)
	if len(update.Item) > 0 {
		size += int64(len(itemKey) + width)
	}
	if update.Null {
		size += int64(len(nullKey))
	}
	if size > math.MaxInt {
		return nil, ErrMetadataWorkBudget
	}
	out := append(make([]byte, 0, int(size)), parentKey...)
	out = jsonwire.AppendString(out, update.Parent)
	if len(update.Item) > 0 {
		out = append(out, itemKey...)
		_, out = jsonwire.Compact(out, update.Item)
	}
	if update.Null {
		out = append(out, nullKey...)
	}
	return append(out, '}'), nil
}

func encodeOwnedMetadataUpdate(ctx context.Context, owner *ResourceRead, update metadataUpdate) ([]byte, error) {
	parent, err := metadataRecordEncodingWork(ctx, reflect.ValueOf(update.Parent))
	if err != nil {
		return nil, err
	}
	item, err := metadataRawEncodingWork(ctx, update.Item, len(update.Parent)+len(update.Item) >= metadataJSONLargeBody)
	if err != nil {
		return nil, err
	}
	if err := owner.reserve(ctx, uint64(parent)+item+8192); err != nil {
		return nil, err
	}
	return marshalMetadataUpdate(update)
}

func metadataJSONTag(tag string) (omitEmpty, quoted bool) {
	_, options, _ := strings.Cut(tag, ",")
	for option := range strings.SplitSeq(options, ",") {
		omitEmpty = omitEmpty || option == "omitempty"
		quoted = quoted || option == "string"
	}
	return
}
