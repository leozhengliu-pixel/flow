// Package boundedcrdt provides the temporary, admitted CRDT documents used by
// bounded metadata callbacks. The legacy dependency remains unchanged.
package boundedcrdt

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"flow/api/internal/boundedcrdt/internal/doc"
	"flow/api/internal/boundedcrdt/internal/encoding"
	"flow/api/internal/boundedcrdt/internal/meter"
	"flow/api/internal/boundedcrdt/internal/types"
)

type Admission = meter.Admission

var ErrInvalidUpdate = errors.New("invalid collaboration update")
var ErrInvalidState = fmt.Errorf("%w: invalid collaboration state", ErrInvalidUpdate)
var ErrClosed = meter.ErrClosed

// CheckRuntime reports whether the process can admit the pinned engine's
// native map operations. Unsupported runtimes remain safe to import.
func CheckRuntime() error { return meter.RuntimeError() }

// Doc owns its admitted engine, decoder, and extracted trees until Close.
// Like a request callback, it is used by one goroutine at a time.
type Doc struct {
	engine *doc.Doc
	meter  *meter.Meter
	ctx    context.Context
	admit  Admission
}

func recoverError(err *error) {
	if value := recover(); value != nil {
		if abort, ok := value.(*meter.Abort); ok {
			*err = abort.Err
		} else {
			*err = ErrInvalidUpdate
		}
	}
}

func NewDoc(ctx context.Context, admit Admission) (_ *Doc, err error) {
	m, err := meter.NewMeter(ctx, admit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			m.Close()
		}
	}()
	defer recoverError(&err)
	engine := doc.NewDoc(m)
	return &Doc{engine: engine, meter: m, ctx: ctx, admit: admit}, nil
}

func (d *Doc) Close() {
	if d != nil {
		d.engine = nil
		d.meter.Close()
		d.ctx = nil
		d.admit = nil
	}
}

func (d *Doc) ApplyState(state string) (err error) {
	if d == nil || d.engine == nil {
		return ErrClosed
	}
	if state == "" {
		return nil
	}
	scratch, err := meter.NewMeter(d.ctx, d.admit)
	if err != nil {
		return err
	}
	defer scratch.Close()
	defer recoverError(&err)
	// DecodeString's output capacity and its possible input string conversion
	// are separate from the engine's subsequently admitted allocations.
	scratch.Reserve(meter.Add(64, meter.Add(uint64(base64.StdEncoding.DecodedLen(len(state))), uint64(len(state)))))
	raw, decodeErr := base64.StdEncoding.DecodeString(state)
	if decodeErr != nil {
		return ErrInvalidState
	}
	return d.ApplyUpdate(raw)
}

func (d *Doc) ApplyUpdate(raw []byte) (err error) {
	defer recoverError(&err)
	if d == nil || d.engine == nil {
		return ErrClosed
	}
	d.meter.Check()
	if err := encoding.ApplyUpdate(d.meter, d.engine, raw); err != nil {
		return ErrInvalidUpdate
	}
	d.meter.Check()
	return nil
}

func (d *Doc) MentionDocument() (_ map[string]any, err error) {
	defer recoverError(&err)
	if d == nil || d.engine == nil {
		return nil, ErrClosed
	}
	d.meter.Check()
	m := d.meter
	var children func(interface {
		Range(*meter.Meter, func(uint64, any) bool)
	}) []any
	children = func(parent interface {
		Range(*meter.Meter, func(uint64, any) bool)
	}) []any {
		m.Enter()
		defer m.Leave()
		result := meter.MakeSlice[[]any, any](m, 0, 0)
		parent.Range(m, func(_ uint64, child any) bool {
			m.Step()
			if element, ok := child.(*types.XmlElement); ok {
				attrs := meter.MakeMap[map[string]any, string, any](m, 0)
				for key, value := range element.Attributes(m) {
					m.Step()
					meter.Set(m, attrs, key, any(value))
				}
				node := meter.MakeMap[map[string]any, string, any](m, 3)
				meter.Set(m, node, "type", any(element.NodeName(m)))
				meter.Set(m, node, "attrs", any(attrs))
				meter.Set(m, node, "content", any(children(element)))
				result = meter.Append(m, result, any(node))
			}
			return true
		})
		return result
	}
	root := meter.MakeMap[map[string]any, string, any](m, 2)
	meter.Set(m, root, "type", any("doc"))
	fragment := types.NewXmlFragment(m, d.engine.Branch(m, "prosemirror"))
	meter.Set(m, root, "content", any(children(fragment)))
	return root, nil
}

// DecodeJSON preserves encoding/json's acceptance and values while keeping
// its output and parsing work owned until release is called.
func DecodeJSON(ctx context.Context, admit Admission, raw []byte) (value any, release func(), err error) {
	m, err := meter.NewMeter(ctx, admit)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			m.Close()
		}
	}()
	defer recoverError(&err)
	if err := meter.Unmarshal(m, raw, &value); err != nil {
		return nil, nil, ErrInvalidUpdate
	}
	return value, m.Close, nil
}
