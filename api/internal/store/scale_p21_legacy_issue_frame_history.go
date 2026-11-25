package store

import (
	"context"
	"reflect"
)

// This entry receives the canonical preimage captured for the current original
// 64-occurrence target batch. It must never re-read SQL after an early flush.
func (h *legacyRecordIssueHistory) appendFrame(ctx context.Context, workspace string, frames *metadataLogicalBuilder, next, previous metadataLogicalNode) (err error) {
	scratch := &ResourceRead{store: h.store}
	defer scratch.Close()
	issue, err := legacyIssueFrameCASInputs(ctx, scratch, frames, next)
	if err != nil {
		return err
	}
	before, err := legacyIssueFrameCASInputs(ctx, scratch, frames, previous)
	if err != nil {
		return err
	}
	if err := h.load(ctx, issue.ID); err != nil {
		return err
	}
	rowOwner := &ResourceRead{store: h.store}
	defer func() {
		if err != nil {
			rowOwner.Close()
		}
	}()
	if err := rowOwner.reserve(ctx, 8<<10); err != nil {
		return err
	}
	history := issueFieldHistory{ID: issue.ID, CreatedAt: issue.UpdatedAt, Version: issue.Version, BaseVersion: before.Version, Fields: map[string]int64{}}
	if before.ID == "" {
		history.BaseVersion = issue.Version
	}
	if stored := h.histories[issue.ID]; len(stored) == 1 && before.ID != "" && stored[0].Version == before.Version {
		history.BaseVersion, history.Fields = stored[0].BaseVersion, stored[0].Fields
		if history.Fields == nil {
			history.Fields = map[string]int64{}
		}
	}
	oldValues := issueCASValues(before)
	changes := map[string]int64{}
	sameDescription, err := legacyIssueFrameDescriptionEqual(ctx, frames, previous, next)
	if err != nil {
		return err
	}
	for field, value := range issueCASValues(issue) {
		same := reflect.DeepEqual(oldValues[field], value)
		if field == "description" {
			same = sameDescription
		}
		if !same {
			changes[field] = issue.Version
			if issue.Version <= before.Version {
				history.BaseVersion = issue.Version
			}
		}
	}
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(history), 0)
	if err != nil {
		return err
	}
	changesCost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(changes), 0)
	if err != nil {
		return err
	}
	if err := rowOwner.reserve(ctx, uint64(cost+changesCost)*2+1024); err != nil {
		return err
	}
	growthCost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(changes), 0)
	if err != nil {
		return err
	}
	if err := h.owner.reserve(ctx, growthCost); err != nil {
		return err
	}
	// Keep append's final admission checkpoint and per-occurrence encoding.
	for field, version := range changes {
		history.Fields[field] = version
	}
	row, err := contentRecordRowFor(workspace, issueFieldHistoryKind, issue.ID, history)
	if err != nil {
		return err
	}
	h.rows = append(h.rows, row)
	h.rowOwners = append(h.rowOwners, rowOwner)
	return nil
}
