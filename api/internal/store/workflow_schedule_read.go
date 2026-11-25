package store

import (
	"context"
	"reflect"
	"time"

	"flow/api/internal/domain"
)

// WorkflowScheduleItem is one call the legacy scheduler makes, in native
// definition order followed by native retry order. It is borrowed until visit
// returns; callers must not retain its strings after that point.
type WorkflowScheduleItem struct {
	WorkflowID, Trigger, ResourceType, ResourceID string
}

type WorkflowScheduleRead struct {
	store  *SQLiteStore
	source *InstalledMetadataRead
}

func (r *WorkflowScheduleRead) Close() {
	if r != nil && r.source != nil {
		r.source.Close()
		r.source = nil
	}
}

// BeginWorkflowScheduleRead retains the same generation for both scheduler
// loops, including while earlier calls advance the workspace's live source.
func (s *SQLiteStore) BeginWorkflowScheduleRead(ctx context.Context, workspace string) (*WorkflowScheduleRead, bool, error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	source, found, err := s.BeginInstalledMetadataRead(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil || !found {
		return nil, found, err
	}
	return &WorkflowScheduleRead{store: s, source: source}, true, nil
}

func (r *WorkflowScheduleRead) VisitDue(ctx context.Context, now time.Time, visit func(WorkflowScheduleItem) error) error {
	if r == nil || r.source == nil || r.source.source == nil || visit == nil {
		return ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	for _, field := range []string{"workflowDefinitions", "workflowRuns"} {
		index, _, _ := metadataLogicalField(field)
		err := r.source.source.fields[index].Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			scratch := &ResourceRead{store: r.store}
			defer scratch.Close()
			if field == "workflowDefinitions" {
				// Exclude IDs and actions until the tiny due predicate matches.
				value, err := scratch.projectNode(ctx, node, "Enabled", "Trigger", "NextRunAt")
				if err != nil {
					return err
				}
				header := value.Interface().(domain.WorkflowDefinition)
				if !header.Enabled || header.Trigger != "schedule" || header.NextRunAt == nil || header.NextRunAt.After(now) {
					return nil
				}
				value, err = scratch.projectNode(ctx, node, "ID")
				if err != nil {
					return err
				}
				header = value.Interface().(domain.WorkflowDefinition)
				return visit(WorkflowScheduleItem{WorkflowID: header.ID, Trigger: "schedule"})
			}
			value, err := scratch.projectNode(ctx, node, "Status", "NextRetryAt")
			if err != nil {
				return err
			}
			header := value.Interface().(domain.WorkflowRun)
			if header.Status != "failed" || header.NextRetryAt == nil || header.NextRetryAt.After(now) {
				return nil
			}
			value, err = scratch.projectNode(ctx, node, "WorkflowID", "ResourceType", "ResourceID")
			if err != nil {
				return err
			}
			header = value.Interface().(domain.WorkflowRun)
			return visit(WorkflowScheduleItem{WorkflowID: header.WorkflowID, Trigger: "retry", ResourceType: header.ResourceType, ResourceID: header.ResourceID})
		})
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}
