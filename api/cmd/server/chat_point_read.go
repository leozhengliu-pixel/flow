package main

import (
	"bytes"
	"context"
	"net/http"
	"sync"

	"flow/api/internal/store"
)

type chatPointReadKey struct{}

// Point reads retain their buffer, decoded result and derived errors through
// the caller's operation. Writes need output admission before persistence.
func (s *server) chatAuthorizedPointRead(ctx context.Context, workspace, userID, path string, result any) error {
	if !s.store.BoundedMetadataEnabled() {
		return s.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, path, nil, result)
	}
	return s.chatAuthorizedMutation(context.WithValue(ctx, chatPointReadKey{}, true), workspace, userID, http.MethodGet, path, nil, result)
}

type chatPointReadOutput struct {
	ctx        context.Context
	work       *store.MetadataJSONRead
	closeScope func()
	response   *chatMutationResponse
	once       sync.Once
}

func (s *server) beginChatPointReadOutput(ctx context.Context, response *chatMutationResponse) (*chatPointReadOutput, error) {
	work, err := s.store.BeginMetadataJSONRead(ctx)
	if err != nil {
		return nil, err
	}
	owned, closeScope := beginIndependentMetadataOutputReads(ctx)
	output := &chatPointReadOutput{work: work, closeScope: closeScope, response: response}
	output.ctx = work.Context(owned)
	if !registerRequestReadClose(ctx, output.Close) {
		output.Close()
		return nil, store.ErrBoundedMetadataCoverage
	}
	return output, nil
}

func (o *chatPointReadOutput) Close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		if o.closeScope != nil {
			o.closeScope()
		}
		if o.response != nil {
			o.response.body = bytes.Buffer{}
		}
		o.work.Close()
	})
}

// JSON response producers call this before Encoder creates its byte buffer.
func (w *chatMutationResponse) prepareJSONOutput(value any) error {
	if w.owned == nil {
		return nil
	}
	return w.owned.work.ReserveEncoding(w.owned.ctx, value)
}

func prepareJSONOutput(w http.ResponseWriter, value any) error {
	for w != nil {
		if owned, ok := w.(interface{ prepareJSONOutput(any) error }); ok {
			return owned.prepareJSONOutput(value)
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil
		}
		w = unwrapper.Unwrap()
	}
	return nil
}
