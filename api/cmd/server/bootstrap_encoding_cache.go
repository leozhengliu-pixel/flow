package main

import (
	"bufio"
	"container/list"
	"context"
	"io"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type bootstrapEncodingKey struct {
	generation store.IssueBootstrapGeneration
	principal  string
	credential string
	projection uint8
	field      int
	signed     bool
}

// A field can depend on current SQL rows as well as the generation. The
// projected value witnesses exactly what was encoded and is immutable.
type bootstrapEncoding struct {
	value any
	json  []byte
	bytes int64
}

type bootstrapEncodingCache interface {
	get(bootstrapEncodingKey) (bootstrapEncoding, bool)
	put(bootstrapEncodingKey, bootstrapEncoding) bool
}

type bootstrapEncodingEntry struct {
	key bootstrapEncodingKey
	bootstrapEncoding
}

type memoryBootstrapEncodings struct {
	mu      sync.Mutex
	entries map[bootstrapEncodingKey]*list.Element
	lru     list.List
	used    int64
	limit   int64
}

func (c *memoryBootstrapEncodings) get(key bootstrapEncodingKey) (bootstrapEncoding, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[key]
	if entry == nil {
		return bootstrapEncoding{}, false
	}
	c.lru.MoveToFront(entry)
	return entry.Value.(bootstrapEncodingEntry).bootstrapEncoding, true
}

func (c *memoryBootstrapEncodings) put(key bootstrapEncodingKey, encoded bootstrapEncoding) bool {
	encoded.bytes += int64(len(key.principal)+len(key.credential)+512) + int64(cap(encoded.json))
	if encoded.bytes > c.limit {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	remove := func(element *list.Element) {
		entry := element.Value.(bootstrapEncodingEntry)
		c.used -= entry.bytes
		delete(c.entries, entry.key)
		c.lru.Remove(element)
	}
	if old := c.entries[key]; old != nil {
		remove(old)
	}
	for encoded.bytes > c.limit-c.used {
		remove(c.lru.Back())
	}
	c.entries[key] = c.lru.PushFront(bootstrapEncodingEntry{key, encoded})
	c.used += encoded.bytes
	return true
}

type bootstrapEncodingRenderer struct {
	cache      bootstrapEncodingCache
	build      sync.Mutex
	buildLimit int
}

func newBootstrapEncodingRenderer(budget int64) *bootstrapEncodingRenderer {
	// Allow one best-effort producer per replica. A capture can briefly
	// hold two buffers while it grows, so reserve twice its maximum size.
	buildLimit := int(min(budget/4, 64<<20))
	return &bootstrapEncodingRenderer{
		cache:      &memoryBootstrapEncodings{entries: make(map[bootstrapEncodingKey]*list.Element), limit: budget - 2*int64(buildLimit)},
		buildLimit: buildLimit,
	}
}

var bootstrapEncodingHits, bootstrapEncodingStores atomic.Int64

func (c *bootstrapEncodingRenderer) write(w http.ResponseWriter, r *http.Request, read *store.IssueBootstrapRead) {
	if c == nil {
		writeStreamedJSON(r.Context(), w, http.StatusOK, read.Data)
		return
	}
	key := bootstrapEncodingKey{principal: authUser(r).ID}
	if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		key.credential = credential.ID
	}
	if projectListBootstrapRequested(r) {
		key.projection = 1
	} else if r.Header.Get("X-Flow-Projection") == "issue-detail" {
		key.projection = 2
	}
	writeStreamedJSONFields(r.Context(), w, http.StatusOK, read.Data, func(ctx context.Context, dst *bufio.Writer, field streamField, value reflect.Value, depth int) error {
		var name string
		switch value.Type() {
		case reflect.TypeFor[[]domain.Project]():
			name = "projects"
		case reflect.TypeFor[[]domain.Document]():
			name = "documents"
		case reflect.TypeFor[[]domain.Initiative]():
			name = "initiatives"
		default:
			return encodeHydratedJSON(ctx, dst, value, depth)
		}
		generation, ok := read.EncodingGeneration(name)
		if !ok {
			return encodeHydratedJSON(ctx, dst, value, depth)
		}
		key.generation = generation
		key.field = field.index
		return c.encode(ctx, dst, key, value, depth)
	})
}

func (c *bootstrapEncodingRenderer) encode(ctx context.Context, dst *bufio.Writer, key bootstrapEncodingKey, value reflect.Value, depth int) error {
	if value.Len() == 0 {
		return encodeHydratedJSON(ctx, dst, value, depth)
	}
	signing, _ := ctx.Value(hydratedURLSigningKey{}).(*hydratedURLSigning)
	key.signed = signing != nil && signing.key != nil
	projected := value.Interface()
	if cached, ok := c.cache.get(key); ok && reflect.DeepEqual(cached.value, projected) {
		bootstrapEncodingHits.Add(1)
		// Chunk at the same scale as the encoder so a detached response
		// can recover from its spool filling partway through the field.
		for start := 0; start < len(cached.json); start += 64 << 10 {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := dst.Write(cached.json[start:min(start+(64<<10), len(cached.json))]); err != nil {
				return err
			}
		}
		return nil
	}
	if c.buildLimit == 0 || !c.build.TryLock() {
		return encodeHydratedJSON(ctx, dst, value, depth)
	}
	defer c.build.Unlock()
	var permits uint64
	if key.signed {
		permits = signing.permits.Load()
	}
	capture := bootstrapEncodingCapture{limit: c.buildLimit}
	tee := bufio.NewWriterSize(io.MultiWriter(dst, &capture), 64<<10)
	if err := encodeHydratedJSON(ctx, tee, value, depth); err != nil {
		return err
	}
	if err := tee.Flush(); err != nil {
		return err
	}
	// Signed requests have a separate key. Any field that checked a file's
	// authority must run the signer again even if that check denied access.
	if capture.json != nil && ctx.Err() == nil && (!key.signed || permits == signing.permits.Load()) {
		// The compatibility download can recycle the top-level slice after
		// encoding. Own the slice while sharing its immutable nested values.
		witness := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		reflect.Copy(witness, value)
		owned := witness.Interface()
		if c.cache.put(key, bootstrapEncoding{value: owned, json: capture.json, bytes: store.BootstrapEncodingWitnessBytes(owned)}) {
			bootstrapEncodingStores.Add(1)
		}
	}
	return nil
}

type bootstrapEncodingCapture struct {
	json  []byte
	limit int
}

func (c *bootstrapEncodingCapture) Write(p []byte) (int, error) {
	if len(p) > c.limit-len(c.json) {
		c.json, c.limit = nil, 0
		return len(p), nil
	}
	if size := len(c.json) + len(p); size > cap(c.json) {
		next := make([]byte, len(c.json), min(c.limit, max(size, 2*cap(c.json))))
		copy(next, c.json)
		c.json = next
	}
	c.json = append(c.json, p...)
	return len(p), nil
}
