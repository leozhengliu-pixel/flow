package main

import (
	"context"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) chatCardMetadata(ctx context.Context, workspace string, request store.ChatDisplayRequest, workflow bool) (domain.Bootstrap, bool, error) {
	if !s.store.BoundedMetadataEnabled() {
		return domain.Bootstrap{}, false, nil
	}
	read, found, err := s.store.BeginChatDisplayRead(ctx, workspace, request)
	if err != nil || !found {
		return domain.Bootstrap{}, true, err
	}
	if workflow {
		if err := read.LoadWorkflow(ctx, request.TeamID); err != nil {
			read.Close()
			return domain.Bootstrap{}, true, err
		}
	}
	if err := read.TransferOutput(ctx); err != nil {
		return domain.Bootstrap{}, true, err
	}
	return read.Data, true, nil
}
