package main

import (
	"context"
	"net/http"
	"sync"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

type requestResourceReadsKey struct{}

type requestResourceReads struct {
	mu      sync.Mutex
	reads   map[*resourceReadHandoff]struct{}
	closers []func()
	closed  bool
}

func beginRequestResourceReads(r *http.Request) (*http.Request, func()) {
	if _, ok := r.Context().Value(requestResourceReadsKey{}).(*requestResourceReads); ok {
		return r, func() {}
	}
	scope := &requestResourceReads{reads: map[*resourceReadHandoff]struct{}{}}
	return r.WithContext(context.WithValue(r.Context(), requestResourceReadsKey{}, scope)), scope.close
}

func (s *requestResourceReads) add(read *resourceReadHandoff) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.reads[read] = struct{}{}
	read.scope = s
	return true
}

// Other owned projections can share the outer response lifetime without
// transferring their decoded values into the resource handoff.
func registerRequestReadClose(ctx context.Context, close func()) bool {
	scope, ok := ctx.Value(requestResourceReadsKey{}).(*requestResourceReads)
	if !ok || scope == nil || close == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.closed {
		return false
	}
	scope.closers = append(scope.closers, close)
	return true
}

func (s *requestResourceReads) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	reads := s.reads
	closers := s.closers
	s.reads = nil
	s.closers = nil
	s.mu.Unlock()
	for read := range reads {
		read.release()
	}
	for i := len(closers) - 1; i >= 0; i-- {
		closers[i]()
	}
}

func (r *resourceReadHandoff) release() {
	if r.owner != nil {
		r.owner.Close()
		r.owner = nil
	}
	if r.bounded {
		r.data = domain.Bootstrap{}
		r.access = store.AccessContext{}
		r.allowed = false
		r.released = true
	}
}

// Direct authorization/handler invocations have no outer HTTP owner. They
// release locally instead of leaving an admitted detail in a test or worker
// context. A later consumer resolves a fresh handoff.
func (r *resourceReadHandoff) releaseUnscoped() {
	if r.scope == nil {
		r.release()
	}
}
