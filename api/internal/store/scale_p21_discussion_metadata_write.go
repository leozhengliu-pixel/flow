package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// persistDiscussionMetadata prepares metadata publication inside the caller's
// issue/content transaction and writeMu interval. The caller commits, installs
// and closes the returned publication; this helper never owns the transaction.
// Actual core/policy edits need the resident-policy writer and are unsupported.
func (s *SQLiteStore) persistDiscussionMetadata(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource, before, next domain.Bootstrap, scope MetadataRecordScope, baseStamp copyStamp, exact bool) (_ *metadataLogicalPublication, err error) {
	if !s.BoundedMetadataEnabled() || source == nil || tx == nil || tx.database != s.db {
		return nil, ErrBoundedMetadataCoverage
	}
	if workspace == "" || before.Workspace.ID == "" || before.Workspace.ID != next.Workspace.ID ||
		before.Workspace.ID != source.Stamp().workspaceID || before.Workspace.ID != baseStamp.workspaceID {
		return nil, ErrMetadataRecordQuery
	}
	if err := validateMetadataRecordScope(scope); err != nil {
		return nil, err
	}
	for _, data := range []domain.Bootstrap{before, next} {
		if data.Issues != nil || data.Comments != nil || data.Activities != nil || data.Notifications != nil || data.NotificationDeliveries != nil {
			return nil, ErrMetadataRecordQuery
		}
	}
	for _, field := range changedMetadataFields(before, next) {
		if mode := metadataResidencyFields()[field.name]; mode == "core" || mode == "policy" {
			return nil, fmt.Errorf("%w: discussion metadata cannot publish %s policy", ErrBoundedMetadataCoverage, field.name)
		}
	}
	if issueID, _ := ctx.Value(discussionThreadTargetKey{}).(string); issueID != "" {
		for _, field := range changedMetadataFields(before, next) {
			if field.name != "threadSubscriptions" {
				return nil, ErrBoundedMetadataCoverage
			}
		}
		return s.prepareDiscussionThreadPublication(ctx, tx, workspace, source,
			before.ThreadSubscriptions, next.ThreadSubscriptions, issueID, baseStamp, exact)
	}
	work := &ResourceRead{store: s}
	defer work.Close()
	if err := work.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	for _, data := range []domain.Bootstrap{before, next} {
		cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(data), 0)
		if err != nil {
			return nil, err
		}
		if uint64(cost) > (metadataWorkBytes-(64<<10))/4 {
			return nil, ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, uint64(cost)*4+64<<10); err != nil {
			return nil, err
		}
		if err := validateMetadataLogicalSelection(data, scope); err != nil {
			return nil, err
		}
	}
	// These images remain immutable: canonical rebasing below must never
	// replace the native values later supplied to Fork.
	nativeBefore, nativeNext := before, next
	writeScope, appendOrders, err := s.prepareMetadataAppends(ctx, tx, workspace, nativeBefore, nativeNext, scope)
	if err != nil {
		return nil, err
	}
	if err := validateScopedMetadataChanges(nativeBefore, nativeNext, writeScope); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(nativeNext)
	if err != nil {
		return nil, err
	}
	root, shapes, _, err := splitWorkspaceMetadata(encoded)
	if err != nil {
		return nil, err
	}
	for _, name := range metadataRecordFields(writeScope) {
		value := root[name]
		if shapes[name] == "" && len(value) > 2 && (value[0] == '[' || value[0] == '{') && metadataResidencyFields()[name] != "core" {
			return nil, fmt.Errorf("%w: unsplittable discussion metadata %s", ErrMetadataRecordQuery, name)
		}
	}
	changes, _, err := scaleP16RecordChanges(nativeBefore, nativeNext)
	if err != nil {
		return nil, err
	}
	var completeFields []string
	for _, field := range changedMetadataFields(nativeBefore, nativeNext) {
		if slices.Contains(scope.Fields, field.name) {
			completeFields = append(completeFields, field.name)
		}
	}
	changes = slices.DeleteFunc(changes, func(change metadataRecordChange) bool {
		return slices.Contains(scope.Fields, change.field)
	})
	for _, change := range changes {
		if !metadataRecordSelected(writeScope, change.field, change.key, true) {
			return nil, fmt.Errorf("%w: undeclared discussion metadata %s/%s", ErrMetadataRecordQuery, change.field, change.key)
		}
		field, _, typed := metadataRecordType(change.field)
		isMap := typed && reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() == reflect.Map
		_, appended := appendOrders[metadataRecordKey{field: change.field, key: change.key}]
		if change.insert && !isMap && !appended {
			return nil, fmt.Errorf("%w: point creation requires an append contract or complete field", ErrMetadataRecordQuery)
		}
	}
	if err := s.discussionMetadataWriteWork(ctx, tx, workspace, work, completeFields, changes); err != nil {
		return nil, err
	}
	complete := cloneBootstrap(nativeNext)
	if len(completeFields) != 0 {
		canonicalBefore := cloneBootstrap(nativeBefore)
		left, right := reflect.ValueOf(&canonicalBefore).Elem(), reflect.ValueOf(&complete).Elem()
		for _, field := range persistedMetadataFields() {
			if field.name != "viewer" && !slices.Contains(scope.Fields, field.name) {
				right.Field(field.index).Set(left.Field(field.index))
			}
		}
		// Only complete fields reach the legacy metadata delta writer. Point
		// rows are merged once, separately, and no content collections load.
		if _, err := s.writeWorkspaceMetadataDelta(ctx, tx, workspace, canonicalBefore, &complete); err != nil {
			return nil, err
		}
	}
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	for _, change := range changes {
		if !change.insert {
			continue
		}
		field, _, _ := metadataRecordType(change.field)
		shape := "array"
		if reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() == reflect.Map {
			shape = "map"
		}
		if err := ensureOAuthMetadataShape(ctx, tx, workspace, change.field, shape, lock); err != nil {
			return nil, err
		}
	}
	writeCtx := ctx
	if len(appendOrders) != 0 {
		writeCtx = context.WithValue(ctx, metadataAppendOrdersKey{}, appendOrders)
	}
	if err := scaleP16WriteChanges(writeCtx, tx, workspace, newIssueReferences(nativeNext), changes); err != nil {
		return nil, err
	}
	exact = exact && source.Stamp().revision >= 0 && source.Stamp() == baseStamp
	publication, err := s.prepareMetadataLogicalDeltaPublication(ctx, tx, workspace, source,
		nativeBefore, nativeNext, writeScope, changes, appendOrders, baseStamp, exact, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			publication.Close()
		}
	}()
	if len(completeFields) == 0 {
		return publication, nil
	}
	candidate, err := metadataLogicalCompletePostimage(ctx, publication.candidate, nativeNext, complete, scope)
	if err != nil {
		return nil, err
	}
	publication.Close()
	publication, err = s.prepareMetadataLogicalPublication(ctx, tx, workspace, candidate, baseStamp, exact, false)
	if err != nil {
		candidate.Release()
		return nil, err
	}
	return publication, nil
}

// Admit the same canonical rows the existing merge/delta writers will read.
// Header locks use their existing metadata lock order after issue/content rows.
func (s *SQLiteStore) discussionMetadataWriteWork(ctx context.Context, tx *sqlTx, workspace string, work *ResourceRead, completeFields []string, changes []metadataRecordChange) error {
	clauses := make([]string, 0, len(completeFields)+len(changes))
	args := []any{workspace}
	for _, name := range completeFields {
		clauses = append(clauses, "field=?")
		args = append(args, name)
	}
	for _, change := range changes {
		clauses = append(clauses, "(field=? AND record_key=?)")
		args = append(args, change.field, change.key)
	}
	if len(clauses) == 0 {
		return nil
	}
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+work.byteLength("data")+","+work.byteLength("record_key")+
		" FROM workspace_metadata_records WHERE workspace_key=? AND ("+strings.Join(clauses, " OR ")+") ORDER BY field,record_key"+lock, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var size, keyBytes int64
		if err := rows.Scan(&size, &keyBytes); err != nil {
			rows.Close()
			return err
		}
		if size < 0 || keyBytes < 0 || uint64(size) > (metadataWorkBytes-8192)/128 ||
			uint64(keyBytes) > (metadataWorkBytes-8192-uint64(size)*128)/16 {
			rows.Close()
			return ErrMetadataWorkBudget
		}
		if err := work.reserve(ctx, uint64(size)*128+uint64(keyBytes)*16+8192); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var rootBytes int64
	if err := tx.QueryRowContext(ctx, "SELECT "+work.byteLength("data")+" FROM workspace_states WHERE workspace_key=?", workspace).Scan(&rootBytes); err != nil {
		return err
	}
	if rootBytes < 0 || rootBytes > MetadataRecordPageMaxBytes || uint64(rootBytes) > (metadataWorkBytes-(64<<10))/128 {
		return ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(rootBytes)*128+64<<10); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, "SELECT CASE WHEN "+work.byteLength("data")+"<=? THEN data ELSE NULL END FROM workspace_states WHERE workspace_key=?", rootBytes, workspace).Scan(&raw); err != nil {
		return err
	}
	if raw == nil {
		return ErrMetadataWorkBudget
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	var shapes map[string]string
	if manifest := root[metadataCollectionsKey]; manifest != nil {
		if err := json.Unmarshal(manifest, &shapes); err != nil {
			return err
		}
	}
	for _, change := range changes {
		field, _, ok := metadataRecordType(change.field)
		if !ok {
			return ErrMetadataRecordQuery
		}
		shape := "array"
		if reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Kind() == reflect.Map {
			shape = "map"
		}
		inline := root[change.field]
		nonemptyInline := len(inline) > 2 && (inline[0] == '[' || inline[0] == '{')
		if actual := shapes[change.field]; actual != "" && actual != shape || nonemptyInline {
			return fmt.Errorf("%w: discussion point write requires split %s", ErrBoundedMetadataCoverage, change.field)
		}
	}
	return nil
}
