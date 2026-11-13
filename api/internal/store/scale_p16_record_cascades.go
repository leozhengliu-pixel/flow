package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"flow/api/internal/domain"
)

const scaleP16CascadePageSize = 32

// Match inspects immutable record headers under mu; Apply owns only one page.
// SQL rows are locked and all pages publish in the parent's transaction.
type ProjectRecordCascade struct {
	Field, ProjectID string
	Match            func(*domain.Bootstrap, int) bool
	Apply            func(source *domain.Bootstrap, page *domain.Bootstrap) error
}
type ProjectRecordCascadePlan func(source, before, after *domain.Bootstrap) []ProjectRecordCascade
type projectRecordCascadesKey struct{}

func WithProjectRecordCascades(ctx context.Context, plan ProjectRecordCascadePlan) context.Context {
	return context.WithValue(ctx, projectRecordCascadesKey{}, plan)
}

type projectRecordPageObserverKey struct{}

// The observer runs before commit, permitting cancellation and failure probes.
func WithProjectRecordPageObserver(ctx context.Context, visit func(string, int) error) context.Context {
	return context.WithValue(ctx, projectRecordPageObserverKey{}, visit)
}

func scaleP16CascadeField(data *domain.Bootstrap, name string) (reflect.Value, error) {
	for _, field := range persistedMetadataFields() {
		if field.name == name {
			return reflect.ValueOf(data).Elem().Field(field.index), nil
		}
	}
	return reflect.Value{}, fmt.Errorf("unknown cascade field %s", name)
}

func scaleP16CascadeKeys(source *domain.Bootstrap, plan ProjectRecordCascade, start int) ([]string, int, error) {
	value, err := scaleP16CascadeField(source, plan.Field)
	if err != nil {
		return nil, 0, err
	}
	keys := make([]string, 0, scaleP16CascadePageSize)
	index := start
	for ; index < value.Len(); index++ {
		if !plan.Match(source, index) {
			continue
		}
		id, ok := metadataRecordValueKey(value.Index(index))
		if !ok {
			return nil, 0, ErrMetadataRecordQuery
		}
		keys = append(keys, id)
		if len(keys) == scaleP16CascadePageSize {
			index++
			break
		}
	}
	return keys, index, nil
}

func scaleP16ReadCascadePage(ctx context.Context, reader metadataReader, workspace, field string, keys []string, lock string) (domain.Bootstrap, error) {
	page := domain.Bootstrap{}
	value, err := scaleP16CascadeField(&page, field)
	if err != nil {
		return page, err
	}
	value.Set(reflect.MakeSlice(value.Type(), 0, len(keys)))
	clause, args := bindList("record_key", keys)
	rows, err := reader.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+clause+` ORDER BY collection_order,record_key`+lock, append([]any{workspace, field}, args...)...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return page, err
		}
		record := reflect.New(value.Type().Elem())
		if err := json.Unmarshal(raw, record.Interface()); err != nil {
			return page, err
		}
		value.Set(reflect.Append(value, record.Elem()))
	}
	return page, rows.Err()
}

func (s *SQLiteStore) scaleP16RunCascade(ctx context.Context, tx *sqlTx, workspace string, source *domain.Bootstrap, plan ProjectRecordCascade) error {
	if plan.Field == "issues" {
		return s.scaleP16CascadeIssues(ctx, tx, workspace, source, plan)
	}
	if plan.Field == "documents" {
		return s.scaleP16CascadeDocumentScopes(ctx, tx, workspace, source, plan)
	}
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	for cursor, pageNumber := 0, 1; ; pageNumber++ {
		keys, next, err := scaleP16CascadeKeys(source, plan, cursor)
		if err != nil || len(keys) == 0 {
			return err
		}
		page, err := scaleP16ReadCascadePage(ctx, tx, workspace, plan.Field, keys, lock)
		if err != nil {
			return err
		}
		before := cloneCallbackInput(page)
		if err := plan.Apply(source, &page); err != nil {
			return err
		}
		changes, root, err := scaleP16RecordChanges(before, page)
		if err != nil {
			return err
		}
		if len(root) != 0 {
			return ErrMetadataRecordQuery
		}
		if err := scaleP16WriteChanges(ctx, tx, workspace, newIssueReferences(domain.Bootstrap{Users: source.Users}), changes); err != nil {
			return err
		}
		if visit, ok := ctx.Value(projectRecordPageObserverKey{}).(func(string, int) error); ok {
			if err := visit(plan.Field, pageNumber); err != nil {
				return err
			}
		}
		cursor = next
	}
}

func (s *SQLiteStore) scaleP16CascadeIssues(ctx context.Context, tx *sqlTx, workspace string, source *domain.Bootstrap, plan ProjectRecordCascade) error {
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	var cursorID string
	var cursorOrder int64
	for pageNumber := 1; ; pageNumber++ {
		where, args := "workspace_key=? AND project_id=?", []any{workspace, plan.ProjectID}
		if cursorID != "" {
			where += " AND (collection_order>? OR (collection_order=? AND id>?))"
			args = append(args, cursorOrder, cursorOrder, cursorID)
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,collection_order,data FROM issue_records WHERE `+where+` ORDER BY collection_order,id LIMIT 32`+lock, args...)
		if err != nil {
			return err
		}
		page := domain.Bootstrap{Activities: map[string][]domain.ActivityEvent{}}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&cursorID, &cursorOrder, &raw); err != nil {
				rows.Close()
				return err
			}
			var issue domain.Issue
			if err := json.Unmarshal(raw, &issue); err != nil {
				rows.Close()
				return err
			}
			page.Issues = append(page.Issues, issue)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(page.Issues) == 0 {
			return err
		}
		if err := plan.Apply(source, &page); err != nil {
			return err
		}
		for _, issue := range page.Issues {
			if err := s.writeIssueRecord(ctx, tx, workspace, issue); err != nil {
				return err
			}
		}
		var content []contentRecordRow
		for resource, activities := range page.Activities {
			for _, activity := range activities {
				row, err := contentRecordRowFor(workspace, "activity", resource, activity)
				if err != nil {
					return err
				}
				content = append(content, row)
			}
		}
		if err := writeContentRecordRows(ctx, tx, content); err != nil {
			return err
		}
		if visit, ok := ctx.Value(projectRecordPageObserverKey{}).(func(string, int) error); ok {
			if err := visit(plan.Field, pageNumber); err != nil {
				return err
			}
		}
	}
}

func (s *SQLiteStore) scaleP16InstallCascade(ctx context.Context, workspace string, source *domain.Bootstrap, plan ProjectRecordCascade) error {
	if plan.Field == "issues" {
		return nil
	}
	if plan.Field == "documents" {
		return s.scaleP16InstallDocumentScopes(ctx, workspace, source, plan)
	}
	for cursor := 0; ; {
		keys, next, err := scaleP16CascadeKeys(source, plan, cursor)
		if err != nil || len(keys) == 0 {
			return err
		}
		page, err := scaleP16ReadCascadePage(ctx, s.db, workspace, plan.Field, keys, "")
		if err != nil {
			return err
		}
		value, _ := scaleP16CascadeField(&page, plan.Field)
		present := map[string]json.RawMessage{}
		for index := 0; index < value.Len(); index++ {
			id, _ := metadataRecordValueKey(value.Index(index))
			raw, err := json.Marshal(value.Index(index).Interface())
			if err != nil {
				return err
			}
			present[id] = raw
		}
		changes := make([]metadataRecordChange, 0, len(keys))
		dropped := 0
		for _, id := range keys {
			raw, ok := present[id]
			changes = append(changes, metadataRecordChange{field: plan.Field, key: id, raw: raw, drop: !ok})
			if !ok {
				dropped++
			}
		}
		if err := scaleP16InstallChanges(source, *source, changes); err != nil {
			return err
		}
		cursor = next - dropped
	}
}
