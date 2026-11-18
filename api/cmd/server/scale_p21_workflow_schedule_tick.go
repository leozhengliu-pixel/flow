package main

import (
	"context"
	"time"

	"flow/api/internal/store"
)

func (s *server) boundedWorkflowScheduleTick(ctx context.Context, now time.Time) {
	for _, key := range s.store.WorkspaceKeys() {
		func() {
			read, found, err := s.store.BeginWorkflowScheduleRead(ctx, key)
			if err != nil || !found {
				return
			}
			defer read.Close()
			_ = read.VisitDue(ctx, now, func(item store.WorkflowScheduleItem) error {
				runCtx, closeOutput := beginIndependentMetadataOutputReads(ctx)
				defer closeOutput()
				_, _ = s.runWorkflow(runCtx, key, item.WorkflowID, item.Trigger, item.ResourceType, item.ResourceID, true)
				return nil
			})
		}()
	}
}
