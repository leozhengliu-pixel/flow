package main

import (
	"context"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// workerLoopDefinition is the worker setup's trusted exact-ID input read.
// The recorder and prepareLoopRun retain their independent authorization.
func (s *server) workerLoopDefinition(ctx context.Context, workspace, id string) (*domain.Loop, bool, error) {
	scope := store.MetadataRecordScope{}
	if id != "" && len(id) <= 191 && !strings.ContainsRune(id, '\x00') {
		scope.Read = []store.RecordKey{{Kind: "loops", ID: id}}
	}
	data, ok, err := s.store.WorkspaceMetadataFieldsScoped(ctx, workspace, scope, "loops")
	if err != nil || !ok {
		return nil, ok, err
	}
	return loopByID(&data, id), true, nil
}
