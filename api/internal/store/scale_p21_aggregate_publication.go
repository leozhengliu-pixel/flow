package store

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"

	"flow/api/internal/boundedcrdt"
)

// metadataAggregatePublication owns only the final bytes once construction
// returns. Callers retain it until their synchronous publication sinks return.
// Normalization graphs, diff maps and encoding scratch have shorter lifetimes.
type metadataAggregatePublication struct {
	PreviousValues  json.RawMessage
	RealtimePayload json.RawMessage
	releases        []func()
	once            sync.Once
}

func (p *metadataAggregatePublication) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		p.PreviousValues, p.RealtimePayload = nil, nil
		for _, release := range p.releases {
			release()
		}
		p.releases = nil
	})
}

// prepareMetadataAggregatePublication preserves the legacy winner, normalization,
// previous-values and realtime contracts without materializing the typed winners
// or reserving fixed multiples of their full native sizes. Every memory owner is
// admitted before its allocation, independently of transaction/request leases.
func (s *SQLiteStore) prepareMetadataAggregatePublication(
	ctx context.Context, before, after metadataAggregateImage, id string,
	previous bool, payload json.RawMessage, eventType string,
) (*metadataAggregatePublication, error) {
	return s.prepareMetadataAggregateOutputs(ctx, before, after, id, previous, payload, eventType, true)
}

// Some writers compute previous-values before refreshing resource counts, and
// the realtime entity after. Keep those two source images distinct.
func (s *SQLiteStore) prepareMetadataAggregatePrevious(
	ctx context.Context, before, after metadataAggregateImage, id string,
) (*metadataAggregatePublication, error) {
	return s.prepareMetadataAggregateOutputs(ctx, before, after, id, true, nil, "", false)
}

func (s *SQLiteStore) prepareMetadataAggregateOutputs(
	ctx context.Context, before, after metadataAggregateImage, id string,
	previous bool, payload json.RawMessage, eventType string, realtime bool,
) (publication *metadataAggregatePublication, err error) {
	publication = &metadataAggregatePublication{}
	owner := publication
	defer func() {
		if err != nil {
			owner.Close()
			publication = nil
		}
	}()
	admit := func(size uint64) (func(), error) {
		if size > metadataWorkBytes {
			return nil, ErrMetadataWorkBudget
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size))
		return release, err
	}
	var oldValue any
	releaseOld := func() {}
	if previous && strings.TrimSpace(id) != "" {
		node, err := metadataAggregateResolve(ctx, before, id)
		if err != nil {
			return nil, err
		}
		oldValue, releaseOld, err = s.metadataAggregateNormalizeOwned(ctx, node)
		if err != nil {
			return nil, err
		}
		defer releaseOld()
	}
	node, err := metadataAggregateResolve(ctx, after, id)
	if err != nil {
		return nil, err
	}
	nextValue, releaseNext, err := s.metadataAggregateNormalizeOwned(ctx, node)
	if err != nil {
		return nil, err
	}
	defer releaseNext()
	if oldValue != nil {
		diff, releaseDiff, err := metadataAggregateOwnedDiff(ctx, admit, oldValue, nextValue)
		if err != nil {
			return nil, err
		}
		if diff != nil {
			raw, release, encodeErr := s.metadataAggregateEncodeNormalizedOwned(ctx, diff)
			releaseDiff()
			if encodeErr != nil {
				return nil, encodeErr
			}
			publication.PreviousValues = raw
			publication.releases = append(publication.releases, release)
		} else {
			releaseDiff()
		}
	}
	oldValue = nil
	releaseOld()
	if !realtime {
		return publication, ctx.Err()
	}
	fields, releaseFields, err := metadataAggregateRealtimeFields(ctx, admit, payload, nextValue, eventType)
	if err != nil {
		return nil, err
	}
	defer releaseFields()
	raw, release, err := s.metadataAggregateEncodeNormalizedOwned(ctx, fields)
	if err != nil {
		return nil, err
	}
	publication.RealtimePayload = raw
	publication.releases = append(publication.releases, release)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return publication, nil
}

// The admitted decoder copies JSON strings, so the input byte owner can be
// released immediately after normalization. The returned release owns the
// complete generic graph, decoder growth and Unicode-unquoting scratch.
func (s *SQLiteStore) metadataAggregateNormalizeOwned(ctx context.Context, node metadataAggregateNode) (any, func(), error) {
	if node == nil {
		return nil, func() {}, ctx.Err()
	}
	raw, releaseRaw, err := s.metadataAggregateEncodeOwned(ctx, node)
	if err != nil {
		var syntax *json.SyntaxError
		var unsupportedType *json.UnsupportedTypeError
		var unsupportedValue *json.UnsupportedValueError
		var marshaler *json.MarshalerError
		var codec *metadataAggregateCodecError
		if errors.As(err, &syntax) || errors.As(err, &unsupportedType) || errors.As(err, &unsupportedValue) ||
			errors.As(err, &marshaler) || errors.As(err, &codec) {
			// aggregateJSONValue treats an unencodable entity as absent.
			return nil, func() {}, ctx.Err()
		}
		return nil, nil, err
	}
	defer releaseRaw()
	value, release, err := boundedcrdt.DecodeJSON(ctx, func(size uint64) (func(), error) {
		if size > metadataWorkBytes {
			return nil, ErrMetadataWorkBudget
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size))
		return release, err
	}, raw)
	if errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
		return nil, func() {}, ctx.Err()
	}
	return value, release, err
}

// metadataAggregateEncodeOwned uses the existing exact JSON streamer and a
// logical frame, both backed by the process disk allowance. It allocates the
// final byte slice only after the exact encoded length is known. The fixed
// scratch allowance covers frame/stream buffers, stack and map-sort machinery;
// it is released before the result escapes.
func (s *SQLiteStore) metadataAggregateEncodeOwned(ctx context.Context, node metadataAggregateNode) (
	raw json.RawMessage, release func(), err error,
) {
	defer func() {
		if err != nil && release != nil {
			release()
			raw, release = nil, nil
		}
	}()
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	if err := scratch.reserve(ctx, logicalJSONStreamWork); err != nil {
		return nil, nil, err
	}
	var framed metadataLogicalNode
	if stored, ok := node.(metadataAggregateStoredNode); ok {
		// The caller retains this immutable source through encoding. Reuse
		// its frame and type registry without copying the complete winner.
		framed = stored.value
	} else {
		frames, err := newMetadataLogicalBuilder(ctx)
		if err != nil {
			return nil, nil, err
		}
		defer frames.Close()
		framed, err = frames.writeAggregateNode(node, 0)
		if err != nil {
			return nil, nil, err
		}
	}
	spool, err := newMetadataLogicalRecordSpool(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, spool.Close()) }()
	stream := &logicalJSONStream{
		ctx: ctx, owner: scratch, view: &WebhookAggregateRead{store: s},
		out:                 bufio.NewWriterSize(spool, 64<<10),
		classifyCodecErrors: true,
	}
	defer func() {
		for _, release := range stream.view.releases {
			release()
		}
		if stream.stage != nil {
			err = errors.Join(err, stream.stage.Close())
		}
	}()
	if err := stream.write(webhookJSONValue{node: framed}, 0); err != nil {
		return nil, nil, err
	}
	if err := stream.out.Flush(); err != nil {
		return nil, nil, err
	}
	if spool.bytes < 0 || spool.bytes > metadataWorkBytes {
		return nil, nil, ErrMetadataWorkBudget
	}
	// Go's large byte allocations round to 8 KiB pages. Rounding every byte
	// slice to that width also covers all small-object allocation classes.
	cost := ((uint64(spool.bytes) + 8191) &^ uint64(8191)) + 64
	_, release, err = s.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return nil, nil, err
	}
	raw = make(json.RawMessage, int(spool.bytes))
	if _, err := io.ReadFull(io.NewSectionReader(spool, 0, spool.bytes), raw); err != nil {
		return nil, release, err
	}
	if err := ctx.Err(); err != nil {
		return nil, release, err
	}
	return raw, release, nil
}

func metadataAggregateOwnedDiff(ctx context.Context, admit boundedcrdt.Admission, oldValue, nextValue any) (any, func(), error) {
	if nextValue == nil {
		return oldValue, func() {}, ctx.Err()
	}
	oldMap, oldOK := oldValue.(map[string]any)
	nextMap, nextOK := nextValue.(map[string]any)
	if !oldOK || !nextOK {
		return oldValue, func() {}, ctx.Err()
	}
	work, err := beginMetadataAggregateTraversal(ctx, admit)
	if err != nil {
		return nil, nil, err
	}
	defer work.Close()
	// Keys and values are borrowed from the admitted normalization graph.
	// Only a shallow map is added: 512 bytes per entry covers the Swiss-map
	// directory, two table generations, control bytes and string/interface
	// slots, plus a 1 KiB empty-map/stack allowance.
	release, err := metadataAggregateShallowMapAdmission(admit, len(oldMap))
	if err != nil {
		return nil, nil, err
	}
	diff := make(map[string]any, len(oldMap))
	for key, value := range oldMap {
		if err := ctx.Err(); err != nil {
			release()
			return nil, nil, err
		}
		next, ok := nextMap[key]
		equal := false
		if ok {
			equal, err = metadataAggregateNormalizedEqual(ctx, value, next, work)
			if err != nil {
				release()
				return nil, nil, err
			}
		}
		if !ok || !equal {
			diff[key] = value
		}
	}
	if len(diff) == 0 {
		release()
		return nil, func() {}, nil
	}
	return diff, release, nil
}

func metadataAggregateRealtimeFields(ctx context.Context, admit boundedcrdt.Admission, payload []byte, entity any, eventType string) (
	fields map[string]any, release func(), err error,
) {
	releasePayload := func() {}
	defer func() {
		if err != nil && releasePayload != nil {
			releasePayload()
		}
	}()
	var input any
	if len(payload) != 0 {
		input, releasePayload, err = boundedcrdt.DecodeJSON(ctx, admit, payload)
		if errors.Is(err, boundedcrdt.ErrInvalidUpdate) {
			// The literal typed-map decode can retain a partial object on
			// a float64 overflow, although its later generic decode fails.
			// Preserve that stdlib behavior with independently admitted
			// parsing work; invalid syntax leaves the typed map nil.
			input, releasePayload, err = metadataAggregatePartialPayloadObject(ctx, admit, payload)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	base, object := input.(map[string]any)
	size := len(base) + 2
	releaseMap, err := metadataAggregateShallowMapAdmission(admit, size)
	if err != nil {
		return nil, nil, err
	}
	release = func() { releaseMap(); releasePayload() }
	fields = make(map[string]any, size)
	if object {
		for key, value := range base {
			fields[key] = value
		}
	} else if input != nil {
		fields["input"] = input
	}
	if entity != nil {
		fields["entity"] = entity
	}
	if strings.HasSuffix(eventType, ".deleted") {
		fields["deleted"] = true
	}
	return fields, release, nil
}

func metadataAggregatePartialPayloadObject(ctx context.Context, admit boundedcrdt.Admission, payload []byte) (map[string]any, func(), error) {
	work, err := metadataAggregatePayloadJSONWork(ctx, payload)
	if err != nil {
		return nil, nil, err
	}
	release, err := admit(work)
	if err != nil {
		return nil, nil, err
	}
	var fields map[string]any
	// Deliberately retain fields on an UnmarshalTypeError, exactly as
	// enrichRealtimePayload. Syntax is validated before any map assignment.
	_ = json.Unmarshal(payload, &fields)
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	return fields, release, nil
}

func metadataAggregateShallowMapAdmission(admit boundedcrdt.Admission, entries int) (func(), error) {
	if entries < 0 || uint64(entries) > (metadataWorkBytes-1024)/512 {
		return nil, ErrMetadataWorkBudget
	}
	return admit(uint64(entries)*512 + 1024)
}

// The streamer's typed frame has a depth bound of 128. RawMessage can legally
// normalize into deeper JSON, so those generic graphs use encoding/json itself.
// A sizing walk over JSON-only values decides that path without encoding a full
// copy. The shallow/common path continues to stream directly into its spool.
func (s *SQLiteStore) metadataAggregateEncodeNormalizedOwned(ctx context.Context, value any) (json.RawMessage, func(), error) {
	work, err := beginMetadataAggregateTraversal(ctx, func(bytes uint64) (func(), error) {
		if bytes > metadataWorkBytes {
			return nil, ErrMetadataWorkBudget
		}
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(bytes))
		return release, err
	})
	if err != nil {
		return nil, nil, err
	}
	defer work.Close()
	size, depth, entries, err := metadataAggregateNormalizedSize(ctx, value, work)
	if err != nil {
		return nil, nil, err
	}
	if depth < metadataLogicalDepth/2-4 {
		return s.metadataAggregateEncodeOwned(ctx, metadataAggregateNativeNode{value: reflect.ValueOf(value)})
	}
	// Marshal's string append may hold an old buffer and a replacement, its
	// encodeState may simultaneously hold an old buffer and a replacement,
	// and Marshal returns one independent copy. Six rounded encoded widths
	// cover those five allocations plus their capacity slack. Map sorting
	// adds one string/reflect-value pair per entry; 64 bytes per entry cover
	// those slots and allocation rounding. The traversal owner retains its
	// sizing stack high-water through Marshal; decoded graphs own no scratch.
	if err := work.encoding(depth); err != nil {
		return nil, nil, err
	}
	width := (size + 8191) &^ uint64(8191)
	if width > metadataWorkBytes/6 || entries > (metadataWorkBytes-width*6)/64 {
		return nil, nil, ErrMetadataWorkBudget
	}
	_, retain, err := s.tryAdmitAdditionalMetadataWork(ctx, int(width+64))
	if err != nil {
		return nil, nil, err
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(width*5+entries*64+(64<<10)))
	if err != nil {
		retain()
		return nil, nil, err
	}
	defer release()
	raw, err := json.Marshal(value)
	if err != nil {
		retain()
		return nil, nil, err
	}
	if uint64(len(raw)) != size {
		retain()
		return nil, nil, ErrBoundedMetadataCoverage
	}
	if err := ctx.Err(); err != nil {
		retain()
		return nil, nil, err
	}
	// After Marshal, only its returned copy remains live; the separate
	// temporary allowance is released before that byte owner escapes.
	return raw, retain, nil
}

func metadataAggregateNormalizedSize(ctx context.Context, value any, work *metadataAggregateTraversal) (size uint64, depth int, entries uint64, err error) {
	if err := work.enter(); err != nil {
		return 0, 0, 0, err
	}
	defer work.leave()
	add := func(part uint64) error {
		if part > metadataWorkBytes || size > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		size += part
		return nil
	}
	child := func(value any) error {
		bytes, nested, keys, err := metadataAggregateNormalizedSize(ctx, value, work)
		if err != nil {
			return err
		}
		depth = max(depth, nested+1)
		entries += keys
		return add(bytes)
	}
	switch value := value.(type) {
	case nil:
		return 4, 0, 0, nil
	case string:
		work, err := metadataAggregateStringEncodingWork(ctx, value)
		return work / 4, 0, 0, err
	case bool, float64:
		raw, err := json.Marshal(value)
		return uint64(len(raw)), 0, 0, err
	case []any:
		size, depth = 2, 1
		for i, value := range value {
			if i > 0 {
				if err := add(1); err != nil {
					return 0, 0, 0, err
				}
			}
			if err := child(value); err != nil {
				return 0, 0, 0, err
			}
		}
	case map[string]any:
		size, depth, entries = 2, 1, uint64(len(value))
		index := 0
		for key, value := range value {
			if index > 0 {
				if err := add(1); err != nil {
					return 0, 0, 0, err
				}
			}
			index++
			work, err := metadataAggregateStringEncodingWork(ctx, key)
			if err != nil {
				return 0, 0, 0, err
			}
			if err := add(work/4 + 1); err != nil {
				return 0, 0, 0, err
			}
			if err := child(value); err != nil {
				return 0, 0, 0, err
			}
		}
	default:
		return 0, 0, 0, ErrMetadataRecordQuery
	}
	return size, depth, entries, nil
}

type metadataAggregateCodecError struct{ err error }

func (e *metadataAggregateCodecError) Error() string { return e.err.Error() }
func (e *metadataAggregateCodecError) Unwrap() error { return e.err }

// The limited reader distinguishes syntactically truncated JSON from a short
// backing file. Exhausting the declared raw bytes is a codec failure; a backing
// read ending before that boundary remains an IO failure.
func metadataAggregateCodecValidation(input io.Reader, validate func(io.Reader) error) error {
	err := validate(input)
	if errors.Is(err, errMetadataCollectionJSON) {
		return &metadataAggregateCodecError{err}
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		if limited, ok := input.(*io.LimitedReader); ok && limited.N == 0 {
			return &metadataAggregateCodecError{err}
		}
	}
	return err
}

// Raw JSON bypasses the typed frame's depth bound. Account for the existing
// scanner's recursive stack before invoking it; this lexical pass retains no
// tokens and delegates all syntax acceptance to that scanner.
func metadataAggregateRawValidationWork(ctx context.Context, node metadataLogicalNode) (uint64, error) {
	var depth, high uint64
	var quoted, escaped bool
	var buffer [32 << 10]byte
	err := node.StreamBytes(ctx, func(input io.Reader) error {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := input.Read(buffer[:])
			for _, char := range buffer[:n] {
				if quoted {
					if escaped {
						escaped = false
					} else if char == '\\' {
						escaped = true
					} else if char == '"' {
						quoted = false
					}
					continue
				}
				switch char {
				case '"':
					quoted = true
				case '[', '{':
					depth++
					high = max(high, depth)
					if high > 10000 {
						return &metadataAggregateCodecError{errMetadataCollectionJSON}
					}
				case ']', '}':
					if depth > 0 {
						depth--
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	})
	return high*(8<<10) + (64 << 10), err
}

// Normalization produces acyclic JSON-only graphs. Comparing their native
// container types avoids reflect.DeepEqual's allocation of a visited-pair map
// while preserving its exact scalar, empty-array and empty-object semantics.
func metadataAggregateNormalizedEqual(ctx context.Context, left, right any, work *metadataAggregateTraversal) (bool, error) {
	if err := work.enter(); err != nil {
		return false, err
	}
	defer work.leave()
	switch left := left.(type) {
	case nil:
		return right == nil, nil
	case bool:
		value, ok := right.(bool)
		return ok && left == value, nil
	case float64:
		value, ok := right.(float64)
		return ok && left == value, nil
	case string:
		value, ok := right.(string)
		return ok && left == value, nil
	case []any:
		value, ok := right.([]any)
		if !ok || len(left) != len(value) {
			return false, nil
		}
		for i, child := range left {
			equal, err := metadataAggregateNormalizedEqual(ctx, child, value[i], work)
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, nil
	case map[string]any:
		value, ok := right.(map[string]any)
		if !ok || len(left) != len(value) {
			return false, nil
		}
		for key, child := range left {
			other, ok := value[key]
			if !ok {
				return false, nil
			}
			equal, err := metadataAggregateNormalizedEqual(ctx, child, other, work)
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, nil
	default:
		return false, ErrMetadataRecordQuery
	}
}
