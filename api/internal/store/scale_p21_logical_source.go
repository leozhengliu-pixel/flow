package store

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

const metadataLogicalDepth = 128
const metadataLogicalTypes = 256
const metadataLogicalDiskBytes = 1 << 30
const metadataLogicalDescriptorBytes = 128 << 10
const metadataLogicalDescriptorLimit = 64 << 20

var errMetadataLogicalClosed = errors.New("installed logical metadata source is closed")
var metadataLogicalDiskUsed atomic.Int64
var metadataLogicalDescriptorsUsed atomic.Int64
var metadataLogicalProcessLocal = time.Local

// A source owns a process-local immutable typed spool. Its generation is
// independent of the current SQL rows: retaining it preserves request handoffs
// across later writes and reloads.
type metadataLogicalSource struct {
	mu         sync.Mutex
	refs       int
	stamp      copyStamp
	fields     []metadataLogicalNode
	stores     []*metadataLogicalStorage
	units      *metadataLogicalUnitsRoot
	generation [16]byte
}

// Storage owners contain no parent-generation links, so sharing an unchanged
// field never retains an unbounded chain of obsolete source descriptors.
type metadataLogicalStorage struct {
	mu            sync.Mutex
	file          metadataLogicalFile
	refs          int
	types         []reflect.Type
	times         []time.Time
	locations     []*time.Location
	locationBytes int64
	bytes         int64
	unitRoot      *metadataLogicalUnitsRoot
}

type metadataLogicalNode struct {
	source      *metadataLogicalStorage
	offset      int64
	size        int64
	cost        uint64
	typ         reflect.Type
	unavailable bool
	blocked     bool
	collection  *metadataLogicalUnitsCollection
}

type metadataLogicalBuilder struct {
	ctx         context.Context
	source      *metadataLogicalSource
	storage     *metadataLogicalStorage
	offset      int64
	active      int
	count       uint64
	header      int64
	cost        uint64
	collection  reflect.Type
	unavailable bool
	path        [metadataLogicalDepth]metadataLogicalAncestor
	buffer      [32 << 10]byte
	finished    bool
	closed      bool
}

type metadataLogicalAncestor struct {
	kind reflect.Kind
	ptr  uintptr
	len  int
}

func newMetadataLogicalBuilder(ctx context.Context) (*metadataLogicalBuilder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !reserveMetadataLogicalBytes(&metadataLogicalDescriptorsUsed, metadataLogicalDescriptorBytes, metadataLogicalDescriptorLimit) {
		return nil, ErrMetadataWorkBudget
	}
	config, _ := ctx.Value(metadataLogicalScratchKey{}).(metadataLogicalScratch)
	file, err := os.CreateTemp(config.directory, "flow-p21-logical-*")
	if err != nil {
		metadataLogicalDescriptorsUsed.Add(-metadataLogicalDescriptorBytes)
		return nil, err
	}
	t := reflect.TypeFor[domain.Bootstrap]()
	storage := &metadataLogicalStorage{file: file, refs: 1}
	source := &metadataLogicalSource{refs: 1, fields: make([]metadataLogicalNode, t.NumField()), stores: []*metadataLogicalStorage{storage}}
	return &metadataLogicalBuilder{ctx: ctx, source: source, storage: storage, active: -1}, nil
}

func reserveMetadataLogicalBytes(used *atomic.Int64, size, limit int64) bool {
	for {
		current := used.Load()
		if size < 0 || size > limit-current {
			return false
		}
		if used.CompareAndSwap(current, current+size) {
			return true
		}
	}
}

// Close abandons an unfinished builder. Finished builders transfer this
// ownership to their source and must not be closed separately.
func (b *metadataLogicalBuilder) Close() {
	if !b.finished && !b.closed {
		b.closed = true
		b.source.Release()
	}
}

func captureMetadataLogicalSource(ctx context.Context, data domain.Bootstrap, stamp copyStamp) (_ *metadataLogicalSource, err error) {
	b, err := newMetadataLogicalBuilder(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			b.source.Release()
		}
	}()
	if err := b.inheritTimestampInputs(ctx); err != nil {
		return nil, err
	}
	value := reflect.ValueOf(data)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		v := value.Field(i)
		if field.Tag.Get("json") == "-" {
			v = reflect.Zero(v.Type())
		}
		node, err := b.writeValue(v, 0, false)
		if err != nil {
			return nil, err
		}
		b.source.fields[i] = node
	}
	return b.Finish(stamp)
}

func metadataLogicalField(name string) (int, reflect.StructField, bool) {
	t := reflect.TypeFor[domain.Bootstrap]()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if strings.Split(f.Tag.Get("json"), ",")[0] == name {
			return i, f, true
		}
	}
	return 0, reflect.StructField{}, false
}

func (b *metadataLogicalBuilder) Field(name string, value reflect.Value) error {
	i, f, ok := metadataLogicalField(name)
	if !ok || b.finished || b.closed || b.active >= 0 || !value.IsValid() || value.Type() != f.Type {
		return ErrMetadataRecordQuery
	}
	node, err := b.writeValue(value, 0, false)
	if err == nil {
		b.source.fields[i] = node
	}
	return err
}

// BeginCollection/Append/EndCollection let cold recovery supply one typed
// record at a time. A nil key denotes an array item; map keys retain their type.
func (b *metadataLogicalBuilder) BeginCollection(name string, nilValue bool) error {
	i, f, ok := metadataLogicalField(name)
	if !ok || b.finished || b.closed || b.active >= 0 || f.Type.Kind() != reflect.Map && f.Type.Kind() != reflect.Slice ||
		f.Type.Kind() == reflect.Slice && f.Type.Elem().Kind() == reflect.Uint8 {
		return ErrMetadataRecordQuery
	}
	b.active, b.collection, b.count, b.header = i, f.Type, 0, b.offset
	b.cost = uint64(f.Type.Size())*2 + 128
	b.unavailable = false
	if err := b.write(make([]byte, 16)); err != nil {
		return err
	}
	flag := byte(1)
	if nilValue {
		flag = 0
	}
	if err := b.write([]byte{flag}); err != nil {
		return err
	}
	return b.writeUint(0)
}

func (b *metadataLogicalBuilder) Append(key, value reflect.Value) error {
	if b.finished || b.closed || b.active < 0 || !value.IsValid() || value.Type() != b.collection.Elem() {
		return ErrMetadataRecordQuery
	}
	var cost uint64
	if b.collection.Kind() == reflect.Map {
		if !key.IsValid() || key.Type() != b.collection.Key() {
			return ErrMetadataRecordQuery
		}
		node, err := b.writeValue(key, 0, true)
		if err != nil {
			return err
		}
		cost += node.cost + 128
		b.unavailable = b.unavailable || node.unavailable
	}
	node, err := b.writeValue(value, 0, false)
	if err != nil {
		return err
	}
	b.cost += cost + node.cost + uint64(value.Type().Size())*2
	b.unavailable = b.unavailable || node.unavailable
	b.count++
	return nil
}

func (b *metadataLogicalBuilder) EndCollection() error {
	if b.finished || b.closed || b.active < 0 {
		return ErrMetadataRecordQuery
	}
	if b.count > 0 {
		if _, err := b.storage.file.WriteAt([]byte{1}, b.header+16); err != nil {
			return err
		}
	}
	var count [8]byte
	binary.LittleEndian.PutUint64(count[:], b.count)
	if _, err := b.storage.file.WriteAt(count[:], b.header+17); err != nil {
		return err
	}
	node := metadataLogicalNode{source: b.storage, offset: b.header, size: b.offset - b.header, cost: b.cost, typ: b.collection, unavailable: b.unavailable}
	if err := b.patch(node); err != nil {
		return err
	}
	b.source.fields[b.active] = node
	b.active, b.collection = -1, nil
	return nil
}

func (b *metadataLogicalBuilder) Finish(stamp copyStamp) (*metadataLogicalSource, error) {
	if b.finished || b.closed || b.active >= 0 {
		return nil, ErrMetadataRecordQuery
	}
	for i, node := range b.source.fields {
		if node.typ == nil {
			f := reflect.TypeFor[domain.Bootstrap]().Field(i)
			if f.Tag.Get("json") != "-" {
				return nil, fmt.Errorf("%w: incomplete installed logical source field %s", ErrBoundedMetadataCoverage, f.Name)
			}
			zero, err := b.writeValue(reflect.Zero(f.Type), 0, false)
			if err != nil {
				return nil, err
			}
			b.source.fields[i] = zero
		}
	}
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	b.source.stamp = stamp
	if _, err := rand.Read(b.source.generation[:]); err != nil {
		return nil, err
	}
	b.finished = true
	return b.source, nil
}

func (s *metadataLogicalSource) Retain() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs == 0 {
		return errMetadataLogicalClosed
	}
	s.refs++
	return nil
}

func (s *metadataLogicalSource) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs == 0 {
		return
	}
	s.refs--
	if s.refs == 0 {
		if s.units != nil {
			s.units.Release()
			s.units = nil
		}
		for _, storage := range s.stores {
			storage.Release()
		}
		s.stores, s.fields = nil, nil
	}
}

func (s *metadataLogicalStorage) Retain() error {
	if s.unitRoot != nil {
		return s.unitRoot.Retain()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs == 0 {
		return errMetadataLogicalClosed
	}
	s.refs++
	return nil
}

func (s *metadataLogicalStorage) Release() {
	if s.unitRoot != nil {
		s.unitRoot.Release()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refs == 0 {
		return
	}
	s.refs--
	if s.refs == 0 {
		name := s.file.Name()
		_ = s.file.Close()
		// An orphan still occupies the temporary volume and keeps its charge.
		if err := os.Remove(name); err == nil || errors.Is(err, os.ErrNotExist) {
			metadataLogicalDiskUsed.Add(-s.bytes)
		}
		metadataLogicalDescriptorsUsed.Add(-metadataLogicalDescriptorBytes - s.locationBytes)
		s.times, s.types, s.locations = nil, nil, nil
	}
}

func (s *metadataLogicalSource) Stamp() copyStamp { return s.stamp }

func (s *metadataLogicalSource) VisitFields(ctx context.Context, visit func(string, metadataLogicalNode) error) error {
	if err := s.Retain(); err != nil {
		return err
	}
	defer s.Release()
	t := reflect.TypeFor[domain.Bootstrap]()
	for i, node := range s.fields {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if err := visit(name, node); err != nil {
			return err
		}
	}
	return nil
}

func (n metadataLogicalNode) Type() reflect.Type { return n.typ }

func (b *metadataLogicalBuilder) write(data []byte) error {
	if b.finished || b.closed {
		return errMetadataLogicalClosed
	}
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if int64(len(data)) > metadataLogicalDiskBytes-b.offset {
		return ErrMetadataWorkBudget
	}
	if !reserveMetadataLogicalBytes(&metadataLogicalDiskUsed, int64(len(data)), metadataLogicalDiskBytes) {
		return ErrMetadataWorkBudget
	}
	count, err := b.storage.file.Write(data)
	b.offset += int64(count)
	b.storage.bytes += int64(count)
	metadataLogicalDiskUsed.Add(int64(count) - int64(len(data)))
	if err == nil && count != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func (b *metadataLogicalBuilder) writeUint(value uint64) error {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], value)
	return b.write(data[:])
}

func (b *metadataLogicalBuilder) patch(n metadataLogicalNode) error {
	var data [16]byte
	binary.LittleEndian.PutUint64(data[:8], uint64(n.size))
	cost := n.cost
	if n.unavailable {
		cost |= uint64(1) << 63
	}
	if n.blocked {
		cost |= uint64(1) << 62
	}
	binary.LittleEndian.PutUint64(data[8:], cost)
	_, err := b.storage.file.WriteAt(data[:], n.offset)
	return err
}

func (b *metadataLogicalBuilder) writeValue(v reflect.Value, depth int, key bool) (node metadataLogicalNode, err error) {
	if depth >= metadataLogicalDepth {
		return node, ErrMetadataWorkBudget
	}
	node = metadataLogicalNode{source: b.storage, offset: b.offset, cost: uint64(v.Type().Size())*2 + 128, typ: v.Type()}
	if err = b.write(make([]byte, 16)); err != nil {
		return node, err
	}
	defer func() {
		if err == nil {
			node.size = b.offset - node.offset
			err = b.patch(node)
		}
	}()
	if v.Type() == reflect.TypeFor[time.Time]() {
		return b.writeTime(node, v.Interface().(time.Time))
	}
	child := func(value reflect.Value, isKey bool) error {
		n, e := b.writeValue(value, depth+1, isKey)
		node.cost += n.cost
		node.unavailable = node.unavailable || n.unavailable
		return e
	}
	switch v.Kind() {
	case reflect.Bool:
		x := byte(0)
		if v.Bool() {
			x = 1
		}
		err = b.write([]byte{x})
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		err = b.writeUint(uint64(v.Int()))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		err = b.writeUint(v.Uint())
	case reflect.Float32, reflect.Float64:
		err = b.writeUint(math.Float64bits(v.Float()))
	case reflect.Complex64, reflect.Complex128:
		err = b.writeUint(math.Float64bits(real(v.Complex())))
		if err == nil {
			err = b.writeUint(math.Float64bits(imag(v.Complex())))
		}
	case reflect.String:
		node.cost += uint64(v.Len())*2 + 64
		err = b.writeUint(uint64(v.Len()))
		if err == nil {
			_, err = io.CopyBuffer(metadataLogicalWriter{b}, strings.NewReader(v.String()), b.buffer[:])
		}
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		if v.IsNil() {
			err = b.write([]byte{0})
			break
		}
		if key && v.Kind() == reflect.Pointer {
			node.unavailable, node.blocked = true, true
			return node, nil
		}
		err = b.write([]byte{1})
		if err != nil {
			break
		}
		if v.Kind() == reflect.Interface {
			t := v.Elem().Type()
			index := -1
			for i, known := range b.storage.types {
				if known == t {
					index = i
					break
				}
			}
			if index < 0 {
				if len(b.storage.types) >= metadataLogicalTypes {
					return node, ErrMetadataWorkBudget
				}
				if err := validateMetadataLogicalTypes(append(slices.Clone(b.storage.types), t)); err != nil {
					return node, err
				}
				index = len(b.storage.types)
				b.storage.types = append(b.storage.types, t)
			}
			if err = b.writeUint(uint64(index)); err == nil {
				err = child(v.Elem(), key)
			}
			break
		}
		ptr := v.Pointer
		address := uintptr(0)
		if v.Kind() == reflect.Map {
			address = uintptr(v.UnsafePointer())
		} else {
			address = ptr()
		}
		length := 0
		if v.Kind() == reflect.Slice {
			length = v.Len()
		}
		for _, a := range b.path[:depth] {
			if a.kind == v.Kind() && a.ptr == address && a.len == length {
				node.unavailable, node.blocked = true, true
				return node, nil
			}
		}
		b.path[depth] = metadataLogicalAncestor{v.Kind(), address, length}
		defer func() { b.path[depth] = metadataLogicalAncestor{} }()
		if v.Kind() == reflect.Pointer {
			err = child(v.Elem(), key)
			break
		}
		err = b.writeUint(uint64(v.Len()))
		if err != nil {
			break
		}
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			node.cost += uint64(v.Len()) * 2
			for start := 0; start < v.Len() && err == nil; start += 32 << 10 {
				end := min(start+32<<10, v.Len())
				chunk := b.buffer[:end-start]
				for i := range chunk {
					chunk[i] = byte(v.Index(start + i).Uint())
				}
				err = b.write(chunk)
			}
		} else if v.Kind() == reflect.Slice {
			node.cost += uint64(v.Len()) * (uint64(v.Type().Elem().Size())*2 + 64)
			for i := 0; i < v.Len() && err == nil; i++ {
				err = child(v.Index(i), key)
			}
		} else {
			node.cost += uint64(v.Len()) * (uint64(v.Type().Elem().Size())*2 + 64)
			iter := v.MapRange()
			for iter.Next() && err == nil {
				err = child(iter.Key(), true)
				if err == nil {
					err = child(iter.Value(), key)
				}
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField() && err == nil; i++ {
			f := v.Type().Field(i)
			if f.PkgPath != "" {
				node.unavailable, node.blocked = true, true
				return node, nil
			}
			value := v.Field(i)
			if f.Tag.Get("json") == "-" && !key {
				value = reflect.Zero(value.Type())
			}
			err = child(value, key)
		}
	case reflect.Array:
		for i := 0; i < v.Len() && err == nil; i++ {
			err = child(v.Index(i), key)
		}
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if !v.IsNil() {
			node.unavailable, node.blocked = true, true
		}
	default:
		node.unavailable, node.blocked = true, true
	}
	return node, err
}

func validateMetadataLogicalTypes(types []reflect.Type) error {
	seen := map[reflect.Type]bool{}
	text := 0
	var visit func(reflect.Type, int) error
	visit = func(t reflect.Type, depth int) error {
		if seen[t] {
			return nil
		}
		if depth >= metadataLogicalDepth || len(seen) >= 256 {
			return ErrMetadataWorkBudget
		}
		seen[t] = true
		text += len(t.Name()) + len(t.PkgPath())
		if text > 32<<10 {
			return ErrMetadataWorkBudget
		}
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
			return visit(t.Elem(), depth+1)
		case reflect.Map:
			if err := visit(t.Key(), depth+1); err != nil {
				return err
			}
			return visit(t.Elem(), depth+1)
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				text += len(field.Name) + len(field.PkgPath) + len(field.Tag)
				if text > 32<<10 {
					return ErrMetadataWorkBudget
				}
				if err := visit(field.Type, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, t := range types {
		if err := visit(t, 0); err != nil {
			return err
		}
	}
	return nil
}

type metadataLogicalWriter struct{ b *metadataLogicalBuilder }

func (w metadataLogicalWriter) Write(data []byte) (int, error) {
	err := w.b.write(data)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w metadataLogicalWriter) WriteString(value string) (int, error) {
	written := 0
	for len(value) > 0 {
		size := copy(w.b.buffer[:], value)
		if err := w.b.write(w.b.buffer[:size]); err != nil {
			return written, err
		}
		written += size
		value = value[size:]
	}
	return written, nil
}

func (b *metadataLogicalBuilder) writeTime(node metadataLogicalNode, value time.Time) (metadataLogicalNode, error) {
	location := value.Location()
	receipt := slices.Index(b.storage.locations, location)
	if location != time.UTC && location != metadataLogicalProcessLocal && receipt < 0 {
		node.unavailable, node.blocked = true, true
		return node, nil
	}
	plain := time.Unix(value.Unix(), int64(value.Nanosecond())).In(location)
	if value == plain {
		if err := b.write([]byte{0}); err != nil {
			return node, err
		}
		if err := b.writeUint(uint64(value.Unix())); err != nil {
			return node, err
		}
		if err := b.writeUint(uint64(value.Nanosecond())); err != nil {
			return node, err
		}
		flag := uint64(0)
		if location == metadataLogicalProcessLocal {
			flag = 1
		} else if receipt >= 0 {
			flag = uint64(receipt + 2)
		}
		return node, b.writeUint(flag)
	}
	// Keep one monotonic anchor per process-owned Location, rather than one
	// opaque receipt per timestamp. Add preserves the private monotonic state.
	index := -1
	var delta time.Duration
	for i, base := range b.storage.times {
		if base.Location() == location {
			d := value.Sub(base)
			if base.Add(d) == value {
				index, delta = i, d
				break
			}
		}
	}
	if index < 0 {
		if len(b.storage.times) >= 32 {
			return node, ErrMetadataWorkBudget
		}
		index = len(b.storage.times)
		b.storage.times = append(b.storage.times, value)
	}
	if err := b.write([]byte{1}); err != nil {
		return node, err
	}
	if err := b.writeUint(uint64(index)); err != nil {
		return node, err
	}
	return node, b.writeUint(uint64(delta))
}
