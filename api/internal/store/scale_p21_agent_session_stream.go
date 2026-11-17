package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type agentSessionLifecycleWriteKey struct{}

// Each part borrows an admitted spool or fixed input. Bodies remain on disk;
// maps contain only object keys and byte ranges.
type legacySessionJSONPart struct {
	input io.ReaderAt
	start int64
	size  int64
}

func (p legacySessionJSONPart) reader() io.Reader {
	return io.NewSectionReader(p.input, p.start, p.size)
}

func legacySessionSpoolPart(spool *RecordSpool) legacySessionJSONPart {
	return legacySessionJSONPart{input: spool, size: spool.bytes}
}

func legacySessionJSONScanner(ctx context.Context, part legacySessionJSONPart) *metadataCollectionScanner {
	return &metadataCollectionScanner{
		ctx: ctx, offset: part.start,
		input: bufio.NewReaderSize(part.reader(), metadataRecordChunkBytes),
	}
}

func legacySessionJSONBytes(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart) ([]byte, error) {
	// One selected message is the unit of recursive legacy JSON merging.
	// This allowance owns raw fragments, decoded maps/lists, merge copies
	// and returned encoder bytes together; callers release it per message.
	if part.size < 0 || uint64(part.size) > (metadataWorkBytes-(128<<10))/32 {
		return nil, ErrMetadataWorkBudget
	}
	if err := owner.reserve(ctx, uint64(part.size)*32+128<<10); err != nil {
		return nil, err
	}
	raw := make([]byte, int(part.size))
	_, err := part.input.ReadAt(raw, part.start)
	return raw, err
}

func legacySessionJSONObject(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart) (map[string]legacySessionJSONPart, error) {
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return nil, err
	}
	fields := map[string]legacySessionJSONPart{}
	err := scanner.object(func(key, value metadataCollectionRange) error {
		if key.size < 0 || uint64(key.size) > (metadataWorkBytes-1024)/8 {
			return ErrMetadataWorkBudget
		}
		if err := owner.reserve(ctx, uint64(key.size)*8+1024); err != nil {
			return err
		}
		raw := make([]byte, int(key.size))
		if _, err := part.input.ReadAt(raw, key.offset); err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(raw, &name); err != nil {
			return err
		}
		// encoding/json map decoding keeps the last exact property name.
		fields[name] = legacySessionJSONPart{part.input, value.offset, value.size}
		return nil
	}, 0)
	if err == nil {
		err = scanner.end()
	}
	return fields, err
}

func legacySessionJSONArray(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart) ([]legacySessionJSONPart, error) {
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return nil, err
	}
	next, err := scanner.peek()
	if err != nil {
		return nil, err
	}
	if next == 'n' {
		_, err := scanner.value(0)
		if err == nil {
			err = scanner.end()
		}
		return nil, err
	}
	var items []legacySessionJSONPart
	err = scanner.array(func(value metadataCollectionRange) error {
		if err := owner.reserve(ctx, 128); err != nil {
			return err
		}
		items = append(items, legacySessionJSONPart{part.input, value.offset, value.size})
		return nil
	}, 0)
	if err == nil {
		err = scanner.end()
	}
	return items, err
}

func legacySessionCopyJSON(ctx context.Context, owner *ResourceRead, part legacySessionJSONPart, output io.Writer) error {
	input := io.NewSectionReader(part.input, part.start, part.size)
	compact, err := newMetadataJSONStringSource(ctx, owner, input, part.size, metadataJSONStringCompactRaw)
	if err != nil {
		return err
	}
	defer compact.Close()
	_, err = io.CopyBuffer(output, io.NewSectionReader(compact, 0, compact.Size()), make([]byte, metadataRecordChunkBytes))
	return err
}

func legacySessionWriteJSONObject(ctx context.Context, owner *ResourceRead, fields map[string]legacySessionJSONPart, output io.Writer) error {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	if _, err := io.WriteString(output, "{"); err != nil {
		return err
	}
	for index, name := range names {
		if index != 0 {
			if _, err := io.WriteString(output, ","); err != nil {
				return err
			}
		}
		key, err := json.Marshal(name)
		if err != nil {
			return err
		}
		if _, err := output.Write(key); err != nil {
			return err
		}
		if _, err := io.WriteString(output, ":"); err != nil {
			return err
		}
		if err := legacySessionCopyJSON(ctx, owner, fields[name], output); err != nil {
			return err
		}
	}
	_, err := io.WriteString(output, "}")
	return err
}

func legacySessionPartEqual(ctx context.Context, owner *ResourceRead, left, right legacySessionJSONPart) (bool, error) {
	if left.input == nil || right.input == nil {
		return left.input == nil && right.input == nil, nil
	}
	var sources [2]*metadataJSONStringSource
	for index, part := range []legacySessionJSONPart{left, right} {
		source, err := newMetadataJSONStringSource(ctx, owner, io.NewSectionReader(part.input, part.start, part.size), part.size, metadataJSONStringCompactRaw)
		if err != nil {
			return false, err
		}
		defer source.Close()
		sources[index] = source
	}
	if sources[0].Size() != sources[1].Size() {
		return false, nil
	}
	var buffers [2][metadataRecordChunkBytes]byte
	for offset := int64(0); offset < sources[0].Size(); offset += metadataRecordChunkBytes {
		size := min(int64(metadataRecordChunkBytes), sources[0].Size()-offset)
		for index := range sources {
			if _, err := sources[index].ReadAt(buffers[index][:size], offset); err != nil {
				return false, err
			}
		}
		if !bytes.Equal(buffers[0][:size], buffers[1][:size]) {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func legacySessionValueEqual(ctx context.Context, owner *ResourceRead, left, right legacySessionJSONPart) (bool, error) {
	same, err := legacySessionPartEqual(ctx, owner, left, right)
	if err != nil || same {
		return same, err
	}
	return legacySessionEmptyPart(left) && legacySessionEmptyPart(right), nil
}

func legacySessionEmptyPart(part legacySessionJSONPart) bool {
	if part.input == nil || part.size == 0 {
		return true
	}
	if part.size > 16 {
		return false
	}
	var raw [16]byte
	if _, err := part.input.ReadAt(raw[:part.size], part.start); err != nil {
		return false
	}
	return emptyMember(raw[:part.size])
}

func legacySessionObjectsEqual(ctx context.Context, owner *ResourceRead, left, right map[string]legacySessionJSONPart) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	for name, a := range left {
		b, found := right[name]
		if !found {
			return false, nil
		}
		same, err := legacySessionPartEqual(ctx, owner, a, b)
		if err != nil || !same {
			return same, err
		}
	}
	return true, nil
}

func legacySessionNodeSpool(ctx context.Context, owner *ResourceRead, node metadataLogicalNode) (_ *RecordSpool, err error) {
	scratch := &ResourceRead{store: owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, logicalJSONStreamWork); err != nil {
		return nil, err
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			spool.Close()
		}
	}()
	if err := legacyProjectStreamNode(ctx, scratch, node, spool); err != nil {
		return nil, err
	}
	return spool, nil
}

type legacySessionMessageNode struct {
	header, content metadataLogicalNode
}

func (n legacySessionMessageNode) nodeType() reflect.Type { return n.header.typ }
func (n legacySessionMessageNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacySessionMessageNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() == "Content" {
			child = n.content
		}
		return visit(key, metadataAggregateStoredNode{value: child})
	})
}

type legacySessionMessagesNode struct {
	shape metadataLogicalNode
	items []metadataLogicalNode
}

func (n legacySessionMessagesNode) nodeType() reflect.Type           { return n.shape.typ }
func (n legacySessionMessagesNode) logicalBase() metadataLogicalNode { return n.shape }
func (n legacySessionMessagesNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacySessionMessagesNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	for index, item := range n.items {
		if err := visit(reflect.ValueOf(index), metadataAggregateStoredNode{value: item}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

type legacySessionCanonicalNode struct {
	header, messages metadataLogicalNode
}

func (n legacySessionCanonicalNode) nodeType() reflect.Type { return n.header.typ }
func (n legacySessionCanonicalNode) nodeValue(ctx context.Context, limit int) (reflect.Value, error) {
	return metadataAggregateMaterializeChildren(ctx, n, limit)
}
func (n legacySessionCanonicalNode) nodeChildren(ctx context.Context, visit func(reflect.Value, metadataAggregateNode) error) error {
	return n.header.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if key.String() == "Messages" {
			child = n.messages
		}
		return visit(key, metadataAggregateStoredNode{value: child})
	})
}

func legacySessionCanonicalMessage(ctx context.Context, owner *ResourceRead, frames *metadataLogicalBuilder, part legacySessionJSONPart, previous metadataLogicalNode) (metadataLogicalNode, error) {
	empty, err := frames.writeValue(reflect.ValueOf(""), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	content := empty
	var message domain.AgentMessage
	if previous.typ != nil {
		if err := owner.reserve(ctx, 4096); err != nil {
			return metadataLogicalNode{}, err
		}
		fields := make([]string, 0, previous.typ.NumField()-1)
		for index := 0; index < previous.typ.NumField(); index++ {
			if name := previous.typ.Field(index).Name; name != "Content" {
				fields = append(fields, name)
			}
		}
		value, err := owner.projectNode(ctx, previous, fields...)
		if err != nil {
			return metadataLogicalNode{}, err
		}
		message = value.Interface().(domain.AgentMessage)
		content, err = issueHotField(ctx, previous, "Content")
		if err != nil {
			return metadataLogicalNode{}, err
		}
	}
	var one [1]byte
	if _, err := part.input.ReadAt(one[:], part.start); err != nil {
		return metadataLogicalNode{}, err
	}
	if one[0] == 'n' {
		if previous.typ != nil {
			return previous, nil
		}
		return frames.writeValue(reflect.ValueOf(domain.AgentMessage{}), 0, false)
	}
	header := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return metadataLogicalNode{}, err
	}
	if _, err := header.Write([]byte("{")); err != nil {
		return metadataLogicalNode{}, err
	}
	first := true
	err = scanner.object(func(key, value metadataCollectionRange) error {
		rawKey, err := legacySessionJSONBytes(ctx, owner, legacySessionJSONPart{part.input, key.offset, key.size})
		if err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(rawKey, &name); err != nil {
			return err
		}
		if !first {
			if _, err := header.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		if _, err := header.Write(rawKey); err != nil {
			return err
		}
		if _, err := header.Write([]byte(":")); err != nil {
			return err
		}
		field, known := resourceWindowJSONField(reflect.TypeFor[domain.AgentMessage](), name)
		if known && field.Name == "Content" {
			if _, err := part.input.ReadAt(one[:], value.offset); err != nil {
				return err
			}
			if one[0] == '"' || one[0] == 'n' {
				content, err = legacyProjectFrameString(ctx, frames, io.NewSectionReader(part.input, value.offset, value.size), content)
				if err != nil {
					return err
				}
				_, err = header.Write([]byte(`""`))
				return err
			}
		}
		_, err = io.CopyBuffer(header, io.NewSectionReader(part.input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
		return err
	}, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := scanner.end(); err != nil {
		return metadataLogicalNode{}, err
	}
	if _, err := header.Write([]byte("}")); err != nil {
		return metadataLogicalNode{}, err
	}
	work, err := resourceWindowTypedJSONWork(ctx, header.data.Bytes(), reflect.TypeFor[domain.AgentMessage]())
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := json.Unmarshal(header.data.Bytes(), &message); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(reflect.ValueOf(message)); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(reflect.ValueOf(message), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return frames.writeAggregateNode(legacySessionMessageNode{node, content}, 0)
}

func legacySessionCanonicalRecord(ctx context.Context, store *SQLiteStore, frames *metadataLogicalBuilder, part legacySessionJSONPart) (metadataLogicalNode, error) {
	owner := &ResourceRead{store: store}
	defer owner.Close()
	if err := owner.reserve(ctx, 2<<20); err != nil {
		return metadataLogicalNode{}, err
	}
	var emptyMessages []domain.AgentMessage
	messages, err := frames.writeValue(reflect.ValueOf(emptyMessages), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	header := &legacyProjectAdmittedBuffer{ctx: ctx, owner: owner}
	scanner := legacySessionJSONScanner(ctx, part)
	if err := scanner.spaces(); err != nil {
		return metadataLogicalNode{}, err
	}
	if _, err := header.Write([]byte("{")); err != nil {
		return metadataLogicalNode{}, err
	}
	first := true
	// encoding/json reuses existing slice elements when an object contains
	// Messages more than once. Truncation retains old backing-array values;
	// only null and an empty array discard them. Keep those typed frames
	// without materializing any previous Content body.
	var messageNodes []metadataLogicalNode
	err = scanner.object(func(key, value metadataCollectionRange) error {
		rawKey, err := legacySessionJSONBytes(ctx, owner, legacySessionJSONPart{part.input, key.offset, key.size})
		if err != nil {
			return err
		}
		var name string
		if err := json.Unmarshal(rawKey, &name); err != nil {
			return err
		}
		if !first {
			if _, err := header.Write([]byte(",")); err != nil {
				return err
			}
		}
		first = false
		if _, err := header.Write(rawKey); err != nil {
			return err
		}
		if _, err := header.Write([]byte(":")); err != nil {
			return err
		}
		field, known := resourceWindowJSONField(reflect.TypeFor[domain.AgentSession](), name)
		if known && field.Name == "Messages" {
			messagePart := legacySessionJSONPart{part.input, value.offset, value.size}
			var prefix [1]byte
			if _, err := part.input.ReadAt(prefix[:], value.offset); err != nil {
				return err
			}
			if prefix[0] == '[' || prefix[0] == 'n' {
				parts, err := legacySessionJSONArray(ctx, owner, messagePart)
				if err != nil {
					return err
				}
				shapeValue := emptyMessages
				if prefix[0] == '[' {
					shapeValue = []domain.AgentMessage{}
				}
				shape, err := frames.writeValue(reflect.ValueOf(shapeValue), 0, false)
				if err != nil {
					return err
				}
				if prefix[0] == 'n' {
					messageNodes = nil
				} else if len(parts) == 0 {
					messageNodes = []metadataLogicalNode{}
				}
				for index, message := range parts {
					work := &ResourceRead{store: store}
					var previous metadataLogicalNode
					if index < len(messageNodes) {
						previous = messageNodes[index]
					}
					node, err := legacySessionCanonicalMessage(ctx, work, frames, message, previous)
					work.Close()
					if err != nil {
						return err
					}
					if index < len(messageNodes) {
						messageNodes[index] = node
					} else {
						if err := owner.reserve(ctx, 256); err != nil {
							return err
						}
						messageNodes = append(messageNodes, node)
					}
				}
				messages, err = frames.writeAggregateNode(legacySessionMessagesNode{shape, messageNodes[:len(parts)]}, 0)
				if err != nil {
					return err
				}
				_, err = header.Write([]byte("null"))
				return err
			}
		}
		_, err = io.CopyBuffer(header, io.NewSectionReader(part.input, value.offset, value.size), make([]byte, metadataRecordChunkBytes))
		return err
	}, 0)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := scanner.end(); err != nil {
		return metadataLogicalNode{}, err
	}
	if _, err := header.Write([]byte("}")); err != nil {
		return metadataLogicalNode{}, err
	}
	work, err := resourceWindowTypedJSONWork(ctx, header.data.Bytes(), reflect.TypeFor[domain.AgentSession]())
	if err != nil {
		return metadataLogicalNode{}, err
	}
	if err := owner.reserve(ctx, work); err != nil {
		return metadataLogicalNode{}, err
	}
	var session domain.AgentSession
	if err := json.Unmarshal(header.data.Bytes(), &session); err != nil {
		return metadataLogicalNode{}, err
	}
	if err := frames.auditJSONLocations(reflect.ValueOf(session)); err != nil {
		return metadataLogicalNode{}, err
	}
	node, err := frames.writeValue(reflect.ValueOf(session), 0, false)
	if err != nil {
		return metadataLogicalNode{}, err
	}
	return frames.writeAggregateNode(legacySessionCanonicalNode{node, messages}, 0)
}

// Detect the same raw marker test as storedRecordEqual without loading a row.
func legacySessionHasMarker(ctx context.Context, part legacySessionJSONPart) (bool, error) {
	reader := bufio.NewReaderSize(part.reader(), metadataRecordChunkBytes)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		prefix, err := reader.Peek(len(offloadMarkerBytes))
		if bytes.Equal(prefix, offloadMarkerBytes) {
			return true, nil
		}
		if _, readErr := reader.ReadByte(); readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return false, nil
			}
			return false, readErr
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
	}
}
