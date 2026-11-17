package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"

	"flow/api/internal/domain"
)

// Large metadata rows (a release with many issue IDs, loop instructions, a
// long description) keep the original row kernels' exact semantics without
// the generic kernels' whole-row native reservations (64 copies of both
// images). The large-row kernels stream every complete image through
// admitted disk spools, compare it member by member in fixed chunks, and
// decode only a typed header: every large top-level string the writer did
// not change is framed straight from its JSON token, never made a Go string.
// Rows at or below largeRowNodeCost keep the original kernels unchanged.
const (
	largeRowNodeCost = 256 << 10
	// A string field above this native cost that the writer left unchanged
	// is framed instead of decoded.
	largeRowBodyCost = 32 << 10
	// The display-user members compared in memory are tiny records; a
	// larger member leaves the decision to the exact header kernel.
	largeRowUserMemberBytes = 64 << 10
)

// Two distinct placeholders stand for a framed body in a typed header. They
// are never the value of a framed body (those are above largeRowBodyCost).
// The second proves that load normalization neither reads nor rewrites them.
var largeRowPlaceholders = [2]string{"flow-large-row-body-a", "flow-large-row-body-b"}

// largeRowDisplayUsers are the members canonicalDisplayData re-encodes.
var largeRowDisplayUsers = []string{"user", "actor", "lead", "creator", "owner"}

type largeRowBody struct {
	field, key string // Go field and JSON member name
	// changed: the writer replaced this body (rebase only).
	changed bool
}

// largeRowKind is the record type of an entity-list field whose rows are
// keyed by ID; update maps keep their own wrapped-row kernels.
func largeRowKind(f *legacyMetadataWriteField) (reflect.Type, bool) {
	if f == nil || updateMetadataField(f.field.name) || f.before.typ == nil || f.before.typ.Kind() != reflect.Slice {
		return nil, false
	}
	kind := f.before.typ.Elem()
	if kind.Kind() != reflect.Struct {
		return nil, false
	}
	if id, ok := kind.FieldByName("ID"); !ok || id.Type.Kind() != reflect.String {
		return nil, false
	}
	return kind, true
}

func largeRowSelected(nodes ...metadataLogicalNode) bool {
	for _, node := range nodes {
		if node.typ != nil && node.cost > largeRowNodeCost {
			return true
		}
	}
	return false
}

// largeRowBodies selects the top-level plain string fields above
// largeRowBodyCost in either image; changed marks those next replaced. Types
// with embedded fields keep every member in the typed header.
func largeRowBodies(ctx context.Context, kind reflect.Type, before, next metadataLogicalNode) ([]largeRowBody, error) {
	for i := 0; i < kind.NumField(); i++ {
		if kind.Field(i).Anonymous {
			return nil, nil
		}
	}
	var bodies []largeRowBody
	err := before.Children(ctx, func(key reflect.Value, left metadataLogicalNode) error {
		info, ok := kind.FieldByName(key.String())
		if !ok || info.PkgPath != "" || len(info.Index) != 1 || info.Type != reflect.TypeFor[string]() {
			return nil
		}
		tag := info.Tag.Get("json")
		if tag == "-" {
			return nil
		}
		parts := strings.Split(tag, ",")
		for _, option := range parts[1:] {
			if option == "string" {
				return nil
			}
		}
		name := parts[0]
		if name == "" {
			name = info.Name
		}
		right, err := issueHotField(ctx, next, key.String())
		if err != nil {
			return err
		}
		if left.cost <= largeRowBodyCost && right.cost <= largeRowBodyCost {
			return nil
		}
		same, err := legacyMetadataWriteEqual(ctx, left, right, 0)
		if err != nil {
			return err
		}
		bodies = append(bodies, largeRowBody{field: info.Name, key: name, changed: !same})
		return nil
	})
	return bodies, err
}

func largeRowBodyIndex(bodies []largeRowBody, field string) int {
	for index, body := range bodies {
		if body.field == field {
			return index
		}
	}
	return -1
}

// largeRowHeader decodes a native record except its framed bodies, which
// become placeholder in the returned value and are returned as nodes.
func largeRowHeader(ctx context.Context, owner *ResourceRead, node metadataLogicalNode, bodies []largeRowBody, placeholder string) (reflect.Value, []metadataLogicalNode, error) {
	cost := uint64(node.typ.Size())*2 + 128 + uint64(len(bodies))*(uint64(len(placeholder))+64)
	if err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if largeRowBodyIndex(bodies, key.String()) >= 0 {
			return nil
		}
		return bootstrapBodyAddWork(&cost, child.cost)
	}); err != nil {
		return reflect.Value{}, nil, err
	}
	if err := owner.reserve(ctx, cost); err != nil {
		return reflect.Value{}, nil, err
	}
	value := reflect.New(node.typ).Elem()
	nodes := make([]metadataLogicalNode, len(bodies))
	err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if index := largeRowBodyIndex(bodies, key.String()); index >= 0 {
			nodes[index] = child
			value.FieldByName(key.String()).SetString(placeholder)
			return nil
		}
		decoded, err := child.Value(ctx, int(child.cost))
		if err == nil {
			value.FieldByName(key.String()).Set(decoded)
		}
		return err
	})
	return value, nodes, err
}

// largeRowCanonicalHeader is legacyProjectCanonicalHeaderFor for any record
// kind and any set of bodies: every string or null occurrence of a body
// field (exact or case-folded member name) is framed with encoding/json's
// repeated-member rules and replaced by placeholder. stored receives each
// body's last exact member occurrence (offset -1 when absent), which is the
// value storedRecordEqual's raw map comparison sees.
func largeRowCanonicalHeader(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, input io.ReaderAt, size int64, kind reflect.Type, bodies []largeRowBody, placeholder string) ([]byte, []metadataLogicalNode, []metadataCollectionRange, error) {
	framed := make([]metadataLogicalNode, len(bodies))
	stored := make([]metadataCollectionRange, len(bodies))
	empty, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return nil, nil, nil, err
	}
	for index := range bodies {
		framed[index], stored[index] = empty, metadataCollectionRange{offset: -1}
	}
	quoted, err := json.Marshal(placeholder)
	if err != nil {
		return nil, nil, nil, err
	}
	output := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := metadataCollectionScanner{
		ctx: ctx, input: bufio.NewReaderSize(io.NewSectionReader(input, 0, size), metadataRecordChunkBytes),
	}
	if err := scanner.spaces(); err != nil {
		return nil, framed, stored, err
	}
	if prefix, err := scanner.peek(); err != nil {
		return nil, framed, stored, err
	} else if prefix == 'n' {
		if err := scanner.literal("null"); err != nil {
			return nil, framed, stored, err
		}
		return []byte("null"), framed, stored, scanner.end()
	}
	if _, err := output.Write([]byte("{")); err != nil {
		return nil, framed, stored, err
	}
	first := true
	err = scanner.object(func(key, value metadataCollectionRange) error {
		if err := owner.reserve(ctx, uint64(key.size)*8+128); err != nil {
			return err
		}
		rawKey := make([]byte, int(key.size))
		if _, err := input.ReadAt(rawKey, key.offset); err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(rawKey, &name); err != nil {
			return err
		}
		for index, body := range bodies {
			if name == body.key {
				stored[index] = value
			}
		}
		if !first {
			if _, err := output.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		if _, err := output.Write(rawKey); err != nil {
			return err
		}
		if _, err := output.Write([]byte(":")); err != nil {
			return err
		}
		if field, found := resourceWindowJSONField(kind, name); found {
			if index := largeRowBodyIndex(bodies, field.Name); index >= 0 {
				var prefix [1]byte
				if _, err := input.ReadAt(prefix[:], value.offset); err != nil {
					return err
				}
				if prefix[0] == '"' || prefix[0] == 'n' {
					framed[index], err = legacyProjectFrameString(ctx, frames, io.NewSectionReader(input, value.offset, value.size), framed[index])
					if err != nil {
						return err
					}
					_, err = output.Write(quoted)
					return err
				}
			}
		}
		// Other members, including mistyped body members, reach the
		// original typed decoder unchanged.
		_, err := io.CopyBuffer(output, io.NewSectionReader(input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
		return err
	}, 0)
	if err != nil {
		return nil, framed, stored, err
	}
	if err := scanner.end(); err != nil {
		return nil, framed, stored, err
	}
	if _, err := output.Write([]byte("}")); err != nil {
		return nil, framed, stored, err
	}
	raw := output.data.Bytes()
	typed, err := resourceWindowTypedJSONWork(ctx, raw, kind)
	if err != nil {
		return nil, framed, stored, err
	}
	if err := owner.reserve(ctx, typed); err != nil {
		return nil, framed, stored, err
	}
	return raw, framed, stored, nil
}

// largeRowNode is a typed header whose framed bodies replace the header's
// placeholders.
type largeRowNode struct {
	header metadataLogicalNode
	bodies map[string]metadataLogicalNode
}

func (n largeRowNode) nodeType() reflect.Type { return n.header.typ }
func (n largeRowNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n largeRowNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if body, ok := n.bodies[key.String()]; ok {
			node = body
		}
		return visit(key, metadataAggregateStoredNode{node})
	})
}

func (p *legacyMetadataWritePlan) stageLargeRow(value reflect.Value, bodies []largeRowBody, nodes []metadataLogicalNode) (metadataLogicalNode, error) {
	if err := p.frames.auditJSONLocations(value); err != nil {
		return metadataLogicalNode{}, err
	}
	header, err := p.frames.writeValue(value, 0, false)
	if err != nil || len(bodies) == 0 {
		return header, err
	}
	replaced := make(map[string]metadataLogicalNode, len(bodies))
	for index, body := range bodies {
		replaced[body.field] = nodes[index]
	}
	return p.frames.writeAggregateNode(largeRowNode{header, replaced}, 0)
}

// largeRowNormalized is normalizedMetadataRecord for a typed header. It
// normalizes the header twice, with each placeholder, and reports false when
// the two results differ in anything but the placeholders themselves, i.e.
// when load normalization reads or rewrites a framed body.
func largeRowNormalized(owner *ResourceRead, ctx context.Context, field metadataField, key string, header reflect.Value, bodies []largeRowBody) ([]byte, bool, error) {
	var outputs [2][]byte
	for index, placeholder := range largeRowPlaceholders {
		value := reflect.New(header.Type()).Elem()
		value.Set(header)
		for _, body := range bodies {
			target := value.FieldByName(body.field)
			if target.String() == largeRowPlaceholders[0] {
				target.SetString(placeholder)
			}
		}
		encoded, err := normalizedMetadataRecord(field, key, value)
		if err != nil {
			return nil, false, err
		}
		if err := owner.reserve(ctx, uint64(len(encoded))*2+1024); err != nil {
			return nil, false, err
		}
		outputs[index] = encoded
	}
	quoted := [2][]byte{}
	for index, placeholder := range largeRowPlaceholders {
		quoted[index], _ = json.Marshal(placeholder)
	}
	if !bytes.Equal(bytes.ReplaceAll(outputs[1], quoted[1], quoted[0]), outputs[0]) ||
		bytes.Count(outputs[0], quoted[0]) != bytes.Count(outputs[1], quoted[1]) {
		return nil, false, nil
	}
	return outputs[0], true, nil
}

// largeRowDirectoryUser returns the directory's current user for id.
func largeRowDirectoryUser(ctx context.Context, directory *metadataLogicalSource, owner *ResourceRead, id string) (domain.User, bool, error) {
	found, err := metadataAggregateLookupUser(ctx, directory, metadataAggregateNativeNode{value: reflect.ValueOf(id)})
	if err != nil || found == nil {
		return domain.User{}, false, err
	}
	node := found.(metadataAggregateStoredNode).value
	if node.cost > metadataWorkBytes/4 {
		return domain.User{}, false, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, node.cost*4+1024); err != nil {
		return domain.User{}, false, err
	}
	value, err := node.Value(ctx, int(node.cost))
	if err != nil {
		return domain.User{}, false, err
	}
	return value.Interface().(domain.User), true, nil
}

// largeRowDisplayMember is one display-user member as canonicalDisplayData
// writes it: re-encoded from the directory when it names a known user, then
// compacted as json.Marshal compacts a RawMessage map value.
func largeRowDisplayMember(ctx context.Context, owner *ResourceRead, directory *metadataLogicalSource, part legacySessionJSONPart) ([]byte, bool, error) {
	if part.size > largeRowUserMemberBytes {
		return nil, false, nil
	}
	if err := owner.reserve(ctx, uint64(part.size)*8+4096); err != nil {
		return nil, false, err
	}
	raw := make([]byte, int(part.size))
	if _, err := part.input.ReadAt(raw, part.start); err != nil {
		return nil, false, err
	}
	var user domain.User
	if json.Unmarshal(raw, &user) == nil {
		current, found, err := largeRowDirectoryUser(ctx, directory, owner, user.ID)
		if err != nil {
			return nil, false, err
		}
		if found {
			if raw, err = json.Marshal(current); err != nil {
				return nil, false, err
			}
		}
	}
	compact, err := json.Marshal(json.RawMessage(raw))
	return compact, err == nil, err
}

// largeRowDisplayEqual decides refs.equalDisplayData(left, right) for two
// encoded records whose display users come from directory, without loading
// either record: byte equality, then the canonical member maps (last exact
// occurrence, RawMessage compaction, display users re-encoded). decided is
// false for shapes the exact in-memory comparison must decide: a
// non-object, an update wrapper (parent plus item) or an oversized user.
func largeRowDisplayEqual(ctx context.Context, owner *ResourceRead, directory *metadataLogicalSource, left, right legacySessionJSONPart) (equal, decided bool, err error) {
	if left.size == 0 {
		return false, false, nil
	}
	if same, err := legacySessionRawEqual(ctx, left, right); err != nil || same {
		return same, err == nil, err
	}
	var fields [2]map[string]legacySessionJSONPart
	for index, part := range []legacySessionJSONPart{left, right} {
		var prefix [1]byte
		reader := bufio.NewReader(part.reader())
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return false, false, nil
			}
			if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
				prefix[0] = b
				break
			}
		}
		if prefix[0] != '{' {
			return false, false, nil
		}
		parsed, err := legacySessionJSONObject(ctx, owner, part)
		if err != nil {
			if errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errMetadataLogicalClosed) {
				return false, false, err
			}
			return false, false, nil
		}
		if _, wrapped := parsed["parent"]; wrapped {
			if item, ok := parsed["item"]; ok && item.size > 0 {
				return false, false, nil
			}
		}
		fields[index] = parsed
	}
	if len(fields[0]) != len(fields[1]) {
		return false, true, nil
	}
	for name, a := range fields[0] {
		b, found := fields[1][name]
		if !found {
			return false, true, nil
		}
		display := false
		for _, user := range largeRowDisplayUsers {
			display = display || name == user
		}
		if !display {
			same, err := legacySessionPartEqual(ctx, owner, a, b)
			if err != nil || !same {
				return false, err == nil, err
			}
			continue
		}
		var members [2][]byte
		for index, part := range []legacySessionJSONPart{a, b} {
			member, ok, err := largeRowDisplayMember(ctx, owner, directory, part)
			if err != nil || !ok {
				return false, false, err
			}
			members[index] = member
		}
		if !bytes.Equal(members[0], members[1]) {
			return false, true, nil
		}
	}
	return true, true, ctx.Err()
}
