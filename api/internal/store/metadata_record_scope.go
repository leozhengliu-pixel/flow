package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// MetadataRecordScope declares complete fields and individual canonical
// records needed by a callback. Read records are immutable callback inputs;
// only Write records and complete Fields can change. An absent Write record
// may be created. Removing a loaded Write record deletes it. Omitted fields
// and records always keep their stored values, including nil versus empty.
// Kind is the Bootstrap JSON field name; ID is the canonical record key.
// Split update collections must currently be declared as complete Fields.
type MetadataRecordScope struct {
	Fields []string
	Read   []RecordKey
	Write  []RecordKey
}

type metadataRecordScopeKey struct{}

// These point writers hydrate their own fixed callback inputs, rather than a
// complete metadata snapshot. Their owners must extend those requirements
// before accepting record scopes; filtering an incomplete input would make a
// declared but unavailable record look absent.
func metadataRecordPointMutation(ctx context.Context, event string, payload any) bool {
	return event == "loop.output_contract" || event == "workspace_preferences.updated" && featureFlagsOnly(payload) ||
		userSettingsRecordMutation(event, payload) || standaloneFavoriteMutation(event, payload) || UsesIssueRecordMutations(ctx)
}

// RecordHydrationEnabled defaults off until the permission differential and
// the business handlers that declare record requirements are rolled out.
func RecordHydrationEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_RECORD_HYDRATION"))) {
	case "1", "true", "on":
		return true
	}
	return false
}

// WithMetadataRecords does not authorize a read or mutation. The existing
// callback authorization still runs, using the declared policy inputs.
// Fixed-input point mutations reject these scopes until their requirements
// are migrated; generic metadata and MutationScope callbacks accept them.
func WithMetadataRecords(ctx context.Context, scope MetadataRecordScope) context.Context {
	scope.Fields = slices.Clone(scope.Fields)
	scope.Read = slices.Clone(scope.Read)
	scope.Write = slices.Clone(scope.Write)
	return context.WithValue(ctx, metadataRecordScopeKey{}, scope)
}

func metadataRecordScopeFromContext(ctx context.Context) (MetadataRecordScope, bool) {
	if !RecordHydrationEnabled() || fullMutationsForced.Load() {
		return MetadataRecordScope{}, false
	}
	scope, ok := ctx.Value(metadataRecordScopeKey{}).(MetadataRecordScope)
	return scope, ok
}

func metadataRecordFields(scope MetadataRecordScope) []string {
	fields := []string{}
	for _, field := range scope.Fields {
		if !slices.Contains(fields, field) {
			fields = append(fields, field)
		}
	}
	for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
		if !slices.Contains(fields, key.Kind) {
			fields = append(fields, key.Kind)
		}
	}
	return fields
}

func metadataRecordSelected(scope MetadataRecordScope, field, key string, writing bool) bool {
	if slices.Contains(scope.Fields, field) {
		return true
	}
	return slices.ContainsFunc(scope.Write, func(k RecordKey) bool { return k.Kind == field && k.ID == key }) ||
		!writing && slices.ContainsFunc(scope.Read, func(k RecordKey) bool { return k.Kind == field && k.ID == key })
}

func metadataRecordValueKey(value reflect.Value) (string, bool) {
	if value.Kind() == reflect.Struct {
		id := value.FieldByName("ID")
		if id.IsValid() && id.Kind() == reflect.String {
			return id.String(), true
		}
	}
	return "", false
}

func selectMetadataRecordValue(value reflect.Value, field string, scope MetadataRecordScope) reflect.Value {
	if slices.Contains(scope.Fields, field) {
		return cloneSnapshotValue(value)
	}
	switch value.Kind() {
	case reflect.Slice:
		if value.Type().Elem().Kind() != reflect.Struct {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeSlice(value.Type(), 0, 0)
		for i := 0; i < value.Len(); i++ {
			key, ok := metadataRecordValueKey(value.Index(i))
			if ok && metadataRecordSelected(scope, field, key, false) {
				result = reflect.Append(result, cloneSnapshotValue(value.Index(i)))
			}
		}
		return result
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String || field == "projectUpdates" || field == "initiativeUpdates" {
			return reflect.Zero(value.Type())
		}
		result := reflect.MakeMap(value.Type())
		for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
			if key.Kind == field {
				k := reflect.ValueOf(key.ID).Convert(value.Type().Key())
				if item := value.MapIndex(k); item.IsValid() {
					result.SetMapIndex(k, cloneSnapshotValue(item))
				}
			}
		}
		return result
	}
	return reflect.Zero(value.Type())
}

func metadataRecordFieldSnapshot(ctx context.Context, data domain.Bootstrap, fields []string) domain.Bootstrap {
	if metadataRecordVerificationEnabled(ctx) {
		return metadataFieldSnapshot(data, fields)
	}
	scope, ok := metadataRecordScopeFromContext(ctx)
	if !ok {
		return metadataFieldSnapshot(data, fields)
	}
	return metadataRecordSnapshot(data, scope, fields)
}

func metadataRecordSnapshot(data domain.Bootstrap, scope MetadataRecordScope, fields []string) domain.Bootstrap {
	result := metadataFieldSnapshot(data, nil)
	source, target := reflect.ValueOf(&data).Elem(), reflect.ValueOf(&result).Elem()
	for _, field := range persistedMetadataFields() {
		if slices.Contains(fields, field.name) {
			target.Field(field.index).Set(selectMetadataRecordValue(source.Field(field.index), field.name, scope))
		}
	}
	return result
}

func metadataRecordMutationAdapter(ctx context.Context, mutate func(*domain.Bootstrap) (string, error)) func(*domain.Bootstrap) (string, error) {
	scope, _ := metadataRecordScopeFromContext(ctx)
	fields := metadataRecordFields(scope)
	return func(data *domain.Bootstrap) (string, error) {
		if err := validateMetadataRecordSelection(*data, scope); err != nil {
			return "", err
		}
		selected := metadataRecordSnapshot(*data, scope, fields)
		// Issue/discussion requirements remain owned by MutationScope. This
		// adapter narrows only workspace metadata, never its loaded records.
		selected.Issues, selected.Comments, selected.Activities = data.Issues, data.Comments, data.Activities
		selected.Notifications, selected.NotificationDeliveries = data.Notifications, data.NotificationDeliveries
		selected.ViewerTeamIDs, selected.FormerMembers, selected.CollaborationUpdates = data.ViewerTeamIDs, data.FormerMembers, data.CollaborationUpdates
		aggregate, err := mutate(&selected)
		if err != nil {
			return aggregate, err
		}
		// AuditLog stages transaction side effects; collectMutationAudit
		// consumes it outside this adapter before metadata persistence.
		audit := selected.AuditLog
		restoreMetadataRecordFields(ctx, &selected, *data, fields)
		selected.AuditLog = audit
		*data = selected
		return aggregate, nil
	}
}

func validateMetadataRecordSelection(data domain.Bootstrap, scope MetadataRecordScope) error {
	value := reflect.ValueOf(data)
	for _, field := range persistedMetadataFields() {
		items := value.Field(field.index)
		if slices.Contains(scope.Fields, field.name) || items.Kind() != reflect.Slice || items.Type().Elem().Kind() != reflect.Struct {
			continue
		}
		seen := map[string]bool{}
		for i := 0; i < items.Len(); i++ {
			key, hasID := metadataRecordValueKey(items.Index(i))
			if hasID && metadataRecordSelected(scope, field.name, key, false) {
				if seen[key] {
					return fmt.Errorf("%w: ambiguous record in %s", ErrMetadataRecordQuery, field.name)
				}
				seen[key] = true
			}
		}
	}
	return nil
}

// Merge only declared writes into the full stored value. Unloaded/read-only
// records keep their original storage; no nil/empty inference is involved.
func restoreMetadataRecordFields(ctx context.Context, data *domain.Bootstrap, stored domain.Bootstrap, fields []string) {
	if metadataRecordVerificationEnabled(ctx) {
		restoreUnlistedMetadata(data, stored, fields)
		return
	}
	scope, ok := metadataRecordScopeFromContext(ctx)
	if !ok {
		restoreUnlistedMetadata(data, stored, fields)
		return
	}
	source, target := reflect.ValueOf(&stored).Elem(), reflect.ValueOf(data).Elem()
	for _, field := range persistedMetadataFields() {
		before, after := source.Field(field.index), target.Field(field.index)
		if slices.Contains(scope.Fields, field.name) {
			continue
		}
		switch before.Kind() {
		case reflect.Slice:
			if before.Type().Elem().Kind() != reflect.Struct {
				after.Set(before)
				continue
			}
			pending := map[string]reflect.Value{}
			for i := 0; i < after.Len(); i++ {
				if key, ok := metadataRecordValueKey(after.Index(i)); ok && metadataRecordSelected(scope, field.name, key, true) {
					pending[key] = after.Index(i)
				}
			}
			hasWrites := slices.ContainsFunc(scope.Write, func(k RecordKey) bool { return k.Kind == field.name })
			if !hasWrites {
				after.Set(before)
				continue
			}
			if before.IsNil() && after.Len() == 0 {
				after.Set(before)
				continue
			}
			result := reflect.MakeSlice(before.Type(), 0, before.Len()+len(pending))
			for i := 0; i < before.Len(); i++ {
				key, _ := metadataRecordValueKey(before.Index(i))
				if !metadataRecordSelected(scope, field.name, key, true) {
					result = reflect.Append(result, before.Index(i))
				} else if item, ok := pending[key]; ok {
					result = reflect.Append(result, item)
					delete(pending, key)
				}
			}
			for i := 0; i < after.Len(); i++ {
				key, _ := metadataRecordValueKey(after.Index(i))
				if item, ok := pending[key]; ok {
					result = reflect.Append(result, item)
					delete(pending, key)
				}
			}
			after.Set(result)
		case reflect.Map:
			if before.Type().Key().Kind() != reflect.String || !slices.ContainsFunc(scope.Write, func(k RecordKey) bool { return k.Kind == field.name }) {
				after.Set(before)
				continue
			}
			if before.IsNil() && after.Len() == 0 {
				after.Set(before)
				continue
			}
			result := reflect.MakeMapWithSize(before.Type(), before.Len())
			iter := before.MapRange()
			for iter.Next() {
				result.SetMapIndex(iter.Key(), iter.Value())
			}
			for _, key := range scope.Write {
				if key.Kind == field.name {
					k := reflect.ValueOf(key.ID).Convert(before.Type().Key())
					result.SetMapIndex(k, after.MapIndex(k))
				}
			}
			after.Set(result)
		default:
			after.Set(before)
		}
	}
}

func validateMetadataRecordScope(scope MetadataRecordScope) error {
	if len(scope.Read)+len(scope.Write) > MetadataRecordBatchMaxKeys {
		return ErrMetadataPageBudget
	}
	for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
		if !validMetadataKey(key) || key.Kind == "projectUpdates" || key.Kind == "initiativeUpdates" {
			return ErrMetadataRecordQuery
		}
		found := false
		for _, field := range persistedMetadataFields() {
			if field.name == key.Kind {
				t := reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type
				found = t.Kind() == reflect.Map && t.Key().Kind() == reflect.String
				if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct {
					id, hasID := t.Elem().FieldByName("ID")
					found = hasID && id.Type.Kind() == reflect.String
				}
			}
		}
		if !found {
			return fmt.Errorf("%w: field %s needs a complete field scope", ErrMetadataRecordQuery, key.Kind)
		}
	}
	for _, name := range scope.Fields {
		if !slices.ContainsFunc(persistedMetadataFields(), func(f metadataField) bool { return f.name == name }) {
			return fmt.Errorf("%w: unknown field %s", ErrMetadataRecordQuery, name)
		}
	}
	return nil
}

// MetadataHydration explicitly records completeness. An empty but loaded field
// is distinct from a field outside Scope. Missing records remain in Scope.
// Offloaded bodies retain their ContentRef; loading a metadata record does not
// load its history bodies or a conversation's offloaded transcript.
type MetadataHydration struct {
	Data  domain.Bootstrap
	Scope MetadataRecordScope
}

func (h MetadataHydration) FieldLoaded(field string) bool {
	return slices.Contains(h.Scope.Fields, field)
}

func (h MetadataHydration) RecordLoaded(key RecordKey) bool {
	return metadataRecordSelected(h.Scope, key.Kind, key.ID, false)
}

// HydrateWorkspaceMetadata is a trusted, bounded storage read. Authorization
// and reference redaction belong to the caller, as for WorkspaceMetadata.
func (s *SQLiteStore) HydrateWorkspaceMetadata(ctx context.Context, workspace string, scope MetadataRecordScope) (MetadataHydration, error) {
	scope.Fields, scope.Read, scope.Write = slices.Clone(scope.Fields), slices.Clone(scope.Read), slices.Clone(scope.Write)
	if err := validateMetadataRecordScope(scope); err != nil {
		return MetadataHydration{}, err
	}
	ctx = WithMetadataRecords(ctx, scope)
	ctx, release, err := s.BeginWorkspaceRead(ctx, workspace)
	if err != nil {
		return MetadataHydration{}, err
	}
	defer release()
	_, data, ok := s.workspaceReadSource(ctx, workspace)
	if !ok {
		return MetadataHydration{}, ErrMetadataRecordQuery
	}
	if _, scoped := metadataRecordScopeFromContext(ctx); !scoped {
		data = metadataRecordSnapshot(data, scope, metadataRecordFields(scope))
	}
	return MetadataHydration{Data: data, Scope: scope}, nil
}

// scopedMetadataRoot keeps the scalar identity and explicitly selected fields.
// The canonical root is small; large unsplit roots retain the legacy path only.
func scopedMetadataRoot(raw []byte, scope MetadataRecordScope) ([]byte, error) {
	if len(raw) > MetadataRecordPageMaxBytes || !metadataRootFieldsBounded(raw) {
		return nil, ErrMetadataPageBudget
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	for key := range root {
		if key != metadataCollectionsKey && key != "workspace" && key != "viewer" && key != "viewerRole" && !slices.Contains(metadataRecordFields(scope), key) {
			delete(root, key)
		}
	}
	return json.Marshal(root)
}

func (s *SQLiteStore) loadScopedMetadataRecordRows(ctx context.Context, workspace string, reader metadataReader, collections map[string]string, scope MetadataRecordScope, budget *metadataHydrationBudget) ([]metadataCacheRow, error) {
	if err := validateMetadataRecordScope(scope); err != nil {
		return nil, err
	}
	result := []metadataCacheRow{}
	remaining := budget.bytes
	for _, field := range metadataRecordFields(scope) {
		if _, split := collections[field]; !split {
			continue
		}
		where, args := "workspace_key=? AND field=?", []any{workspace, field}
		if !slices.Contains(scope.Fields, field) {
			keys := []string{}
			for _, key := range append(slices.Clone(scope.Read), scope.Write...) {
				if key.Kind == field && !slices.Contains(keys, key.ID) {
					keys = append(keys, key.ID)
				}
			}
			clause, values := bindList("record_key", keys)
			where += " AND " + clause
			args = append(args, values...)
		}
		// Check size in SQL before the driver transfers a large body. Both
		// count and bytes are bounded even for a complete-field declaration.
		args = append([]any{remaining}, args...)
		rows, err := reader.QueryContext(ctx, `SELECT record_key,collection_order,LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 51`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			row := metadataCacheRow{Field: field}
			var size int
			if err := rows.Scan(&row.Key, &row.Order, &size, &row.Data); err != nil {
				rows.Close()
				return nil, err
			}
			remaining -= size
			if remaining < 0 || row.Data == nil || len(result) == MetadataRecordBatchMaxKeys {
				rows.Close()
				return nil, ErrMetadataPageBudget
			}
			if err := budget.consume(row.Data); err != nil {
				rows.Close()
				return nil, err
			}
			result = append(result, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, s.inlineOffloadedRows(ctx, workspace, reader, result)
}
