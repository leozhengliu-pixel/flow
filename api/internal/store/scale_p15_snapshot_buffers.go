package store

import (
	"context"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

const hydrationSnapshotBufferLimit = 416 << 20

type hydrationSnapshotBuffer struct {
	value reflect.Value
	bytes int64
}

type hydrationSnapshotBuffers struct {
	mu    sync.Mutex
	idle  []hydrationSnapshotBuffer
	bytes int64
}

var sharedHydrationSnapshotBuffers hydrationSnapshotBuffers

func (p *hydrationSnapshotBuffers) take(t reflect.Type, length int) reflect.Value {
	p.mu.Lock()
	defer p.mu.Unlock()
	best := -1
	for i, item := range p.idle {
		if item.value.Type() == t && item.value.Cap() >= length && item.value.Cap()-length <= length && (best < 0 || item.value.Cap() < p.idle[best].value.Cap()) {
			best = i
		}
	}
	if best < 0 {
		// Do not retain a different workspace's storage beside a new shape.
		p.idle = nil
		p.bytes = 0
		return reflect.MakeSlice(t, length, length)
	}
	item := p.idle[best]
	p.bytes -= item.bytes
	p.idle[best] = p.idle[len(p.idle)-1]
	p.idle[len(p.idle)-1] = hydrationSnapshotBuffer{}
	p.idle = p.idle[:len(p.idle)-1]
	return item.value.Slice(0, length)
}

func (p *hydrationSnapshotBuffers) put(value reflect.Value) {
	value = value.Slice(0, value.Cap())
	value.Clear()
	bytes := int64(value.Cap()) * int64(value.Type().Elem().Size())
	p.mu.Lock()
	defer p.mu.Unlock()
	if bytes <= hydrationSnapshotBufferLimit-p.bytes {
		p.idle = append(p.idle, hydrationSnapshotBuffer{value, bytes})
		p.bytes += bytes
	}
}

type hydrationSnapshotReuseKey struct{}
type hydrationSnapshotReuse struct {
	mu      sync.Mutex
	buffers []reflect.Value
	closed  bool
}

// BeginHydratedSnapshotReuse owns read snapshots until release. Use it only
// around a compatibility GET whose returned values cannot outlive the handler.
// Recycled buffers are cleared; no entity or permission projection is cached.
func BeginHydratedSnapshotReuse(ctx context.Context) (context.Context, func()) {
	if !RecordHydrationEnabled() || ctx.Value(hydrationSnapshotReuseKey{}) != nil {
		return ctx, func() {}
	}
	lease := &hydrationSnapshotReuse{}
	return context.WithValue(ctx, hydrationSnapshotReuseKey{}, lease), func() {
		lease.mu.Lock()
		defer lease.mu.Unlock()
		if lease.closed {
			return
		}
		lease.closed = true
		for _, value := range lease.buffers {
			sharedHydrationSnapshotBuffers.put(value)
		}
		lease.buffers = nil
	}
}

// CloneHydratedReadSnapshot retains cloneBootstrap's deep-copy isolation. Only
// large top-level slice storage is reused, for the explicit request lifetime.
func CloneHydratedReadSnapshot(ctx context.Context, data domain.Bootstrap) domain.Bootstrap {
	lease, _ := ctx.Value(hydrationSnapshotReuseKey{}).(*hydrationSnapshotReuse)
	if !RecordHydrationEnabled() || lease == nil {
		return cloneBootstrap(data)
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.closed {
		return cloneBootstrap(data)
	}
	copy := reflect.ValueOf(&data).Elem()
	plan := structPlanFor(copy.Type())
	for _, i := range plan.zero {
		copy.Field(i).SetZero()
	}
	for _, i := range plan.deep {
		field := copy.Field(i)
		if field.Kind() != reflect.Slice || int64(field.Len())*int64(field.Type().Elem().Size()) < 1<<20 {
			cloneInPlace(field)
			continue
		}
		buffer := sharedHydrationSnapshotBuffers.take(field.Type(), field.Len())
		reflect.Copy(buffer, field)
		if !typeCloneFlat(field.Type().Elem()) {
			if field.Type().Elem().Kind() == reflect.Struct {
				elementPlan := structPlanFor(field.Type().Elem())
				for index := 0; index < buffer.Len(); index++ {
					item := buffer.Index(index)
					for _, zero := range elementPlan.zero {
						item.Field(zero).SetZero()
					}
					for _, deep := range elementPlan.deep {
						nested := item.Field(deep)
						switch nested.Kind() {
						case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Interface:
							if nested.IsNil() {
								continue
							}
						}
						cloneInPlace(nested)
					}
				}
			} else {
				for index := 0; index < buffer.Len(); index++ {
					cloneInPlace(buffer.Index(index))
				}
			}
		}
		field.Set(buffer)
		lease.buffers = append(lease.buffers, buffer)
	}
	return data
}
