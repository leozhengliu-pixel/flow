package store

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

type labelAutomationScopeKey struct{}

// WithLabelAutomationScope selects the workflow and loop records a label
// mutation may edit. Resolve runs against the metadata under the write lock.
// The callback may change those records in place.
func WithLabelAutomationScope(ctx context.Context, resolve func(domain.Bootstrap) []string) context.Context {
	return context.WithValue(ctx, labelAutomationScopeKey{}, resolve)
}

func labelAutomationScope(ctx context.Context) func(domain.Bootstrap) []string {
	resolve, _ := ctx.Value(labelAutomationScopeKey{}).(func(domain.Bootstrap) []string)
	return resolve
}

var labelAutomationFields = []string{"loops", "workflowDefinitions"}

// labelAutomationLoopsNeedWebhook reads webhookNeeded's loop input from the
// loop rows when a label mutation's COLD facade decoded no loops
// (decodeColdMetadataRead): an event-triggered loop needs previous values
// there exactly as on the full path. A failed read assumes it does.
func (s *SQLiteStore) labelAutomationLoopsNeedWebhook(ctx context.Context, workspace string) bool {
	if s.coldParent == nil || s.coldParent.metadataResidency == nil || labelAutomationScope(ctx) == nil {
		return false
	}
	if scoped, _ := ctx.Value(coldLoopContractReadKey{}).(bool); scoped {
		// The loop-contract facade decodes the loops.
		return false
	}
	enabled := s.jsonText("data", "enabled") + ` IN ('1','true')`
	if s.dialect == "sqlite" {
		enabled = s.jsonText("data", "enabled") + `=1`
	}
	var needed int
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field='loops' AND `+enabled+
		` AND COALESCE(`+s.jsonText("data", "status")+`,'')<>'draft' AND COALESCE(`+s.jsonText("data", "triggerType")+`,'') NOT IN ('','schedule')) THEN 1 ELSE 0 END`, workspace).Scan(&needed)
	return err != nil || needed != 0
}

type labelAutomationProjection struct {
	before domain.Bootstrap
	rows   map[metadataRecordKey]metadataRecord
}

func loadLabelAutomations(ctx context.Context, tx *sqlTx, workspace string, next *domain.Bootstrap) (labelAutomationProjection, error) {
	selected := labelAutomationProjection{rows: map[metadataRecordKey]metadataRecord{}}
	err := visitLabelReferenceRecords(ctx, tx, workspace, labelAutomationFields, labelAutomationScope(ctx)(*next),
		func(key metadataRecordKey, row metadataRecord) error {
			selected.rows[key] = row
			return appendLabelProjection(&selected.before, key.field, row.data)
		})
	next.WorkflowDefinitions = slices.Clone(selected.before.WorkflowDefinitions)
	next.Loops = slices.Clone(selected.before.Loops)
	return selected, err
}

// writeLabelAutomations patches just the selected rows. The normal metadata
// delta must still see the original full fields (or the COLD facade's omitted
// fields), so it cannot mistake the projection for a collection replacement.
func writeLabelAutomations(ctx context.Context, tx *sqlTx, workspace string, selected labelAutomationProjection, after *domain.Bootstrap, stored domain.Bootstrap) (map[string]map[string]json.RawMessage, error) {
	changes := map[string]map[string]json.RawMessage{}
	for _, field := range labelAutomationFields {
		before, err := labelProjectionField(&selected.before, field)
		if err != nil {
			return nil, err
		}
		next, err := labelProjectionField(after, field)
		if err != nil {
			return nil, err
		}
		if before.Len() != next.Len() {
			return nil, ErrMetadataRecordQuery
		}
		for i := 0; i < before.Len(); i++ {
			old, item := before.Index(i), next.Index(i)
			id := old.FieldByName("ID").String()
			if id != item.FieldByName("ID").String() {
				return nil, ErrMetadataRecordQuery
			}
			if reflect.DeepEqual(old.Interface(), item.Interface()) {
				continue
			}
			raw, err := marshalMetadataRecord(item.Interface())
			if err != nil {
				return nil, err
			}
			raw = StripJSONNUL(raw)
			key := metadataRecordKey{field: field, key: id}
			row, ok := selected.rows[key]
			if !ok {
				return nil, ErrMetadataRecordQuery
			}
			if err := writeMetadataRecordRow(ctx, tx, workspace, key, metadataRecord{order: row.order, data: raw}, row.data); err != nil {
				return nil, err
			}
			if changes[field] == nil {
				changes[field] = map[string]json.RawMessage{}
			}
			changes[field][id] = raw
		}
	}
	after.WorkflowDefinitions, after.Loops = stored.WorkflowDefinitions, stored.Loops
	return changes, nil
}
