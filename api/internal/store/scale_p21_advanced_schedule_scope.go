package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
)

// The original SLA scope admits raw candidate occurrences before deduplication.
// Direct active-SLA IDs bypass the rule predicate; SQL-filtered IDs do not.
func (advancedScheduleImageProfile) selectIssueIDs(ctx context.Context, tx *sqlTx, workspace string, scope MutationScope, owner *ResourceRead) ([]string, error) {
	checked := scope
	checked.IssueIDs, checked.Resources, checked.IssueColumns, checked.LabelIssues, checked.IssueFilter = nil, nil, nil, nil, nil
	if !reflect.DeepEqual(checked, MutationScope{}) {
		return nil, ErrBoundedMetadataCoverage
	}
	if len(scope.IssueIDs) > maxScopedIssues {
		return nil, errScopeTooLarge
	}
	if err := owner.reserve(ctx, uint64(len(scope.IssueIDs))*256+64<<10); err != nil {
		return nil, err
	}
	ids := slices.Clone(scope.IssueIDs)
	collect := func(table, idColumn, column string, values []string) error {
		if err := owner.reserve(ctx, uint64(len(values))*64+4096); err != nil {
			return err
		}
		values = slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == "" })
		for start := 0; start < len(values); start += 500 {
			where, args := bindList(column, values[start:min(start+500, len(values))])
			from := " FROM " + table + " WHERE workspace_key=? AND " + where
			args = append([]any{workspace}, args...)
			var count, size int64
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+owner.byteLength(idColumn)+"),0)"+from, args...).Scan(&count, &size); err != nil {
				return err
			}
			if count < 0 || size < 0 {
				return ErrMetadataRecordQuery
			}
			if count > int64(maxScopedIssues-len(ids)) {
				return errScopeTooLarge
			}
			if uint64(size) > (metadataWorkBytes-(64<<10)-uint64(count)*512)/8 {
				return ErrMetadataWorkBudget
			}
			if err := owner.reserve(ctx, uint64(size)*8+uint64(count)*512+64<<10); err != nil {
				return err
			}
			rows, err := tx.QueryContext(ctx, "SELECT "+idColumn+from, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
				if len(ids) > maxScopedIssues {
					rows.Close()
					return errScopeTooLarge
				}
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
		}
		return nil
	}
	for _, column := range scope.IssueColumns {
		if !slices.Contains([]string{"team_id", "state_id", "project_id"}, column.Column) {
			return nil, ErrBoundedMetadataCoverage
		}
		if err := collect("issue_records", "id", column.Column, column.Values); err != nil {
			return nil, err
		}
	}
	if err := collect("issue_label_records", "issue_id", "label_id", scope.LabelIssues); err != nil {
		return nil, err
	}
	return ids, nil
}

func (advancedScheduleImageProfile) filterIssueRecords(scope MutationScope, records map[string]storedIssueRecord) {
	for id, record := range records {
		if !slices.Contains(scope.IssueIDs, id) && scope.IssueFilter != nil && !scope.IssueFilter(record.issue) {
			delete(records, id)
		}
	}
}
