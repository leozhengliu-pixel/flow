package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"

	"flow/api/internal/domain"
)

func scaleP16ReadDocumentScope(ctx context.Context, reader metadataRowReader, workspace, id, lock string) (domain.Document, int, []byte, error) {
	var raw []byte
	var order int
	err := reader.QueryRowContext(ctx, `SELECT collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' AND record_key=?`+lock, workspace, id).Scan(&order, &raw)
	if err != nil {
		return domain.Document{}, 0, nil, err
	}
	var header struct {
		ID               string   `json:"id"`
		IssueID          string   `json:"issueId"`
		IssueTeamID      string   `json:"issueTeamId"`
		ProjectIDs       []string `json:"projectIds"`
		InheritedTeamIDs []string `json:"inheritedTeamIds"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return domain.Document{}, 0, nil, err
	}
	return domain.Document{ID: header.ID, IssueID: header.IssueID, IssueTeamID: header.IssueTeamID, ProjectIDs: header.ProjectIDs, InheritedTeamIDs: header.InheritedTeamIDs}, order, raw, nil
}

// Scope changes preserve current bodies and version markers byte for byte.
// Only one canonical document body is resident at a time, including when a
// current revision refers back to the document's body.
func (s *SQLiteStore) scaleP16CascadeDocumentScopes(ctx context.Context, tx *sqlTx, workspace string, source *domain.Bootstrap, plan ProjectRecordCascade) error {
	lock := ""
	if s.dialect != "sqlite" {
		lock = " FOR UPDATE"
	}
	for cursor, pageNumber := 0, 1; ; pageNumber++ {
		keys, next, err := scaleP16CascadeKeys(source, plan, cursor)
		if err != nil || len(keys) == 0 {
			return err
		}
		for _, id := range keys {
			document, order, raw, err := scaleP16ReadDocumentScope(ctx, tx, workspace, id, lock)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			page := domain.Bootstrap{Documents: []domain.Document{document}}
			if err := plan.Apply(source, &page); err != nil {
				return err
			}
			after := page.Documents[0]
			if document.IssueTeamID == after.IssueTeamID && reflect.DeepEqual(document.InheritedTeamIDs, after.InheritedTeamIDs) {
				continue
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			header, err := json.Marshal(after)
			if err != nil {
				return err
			}
			var patch map[string]json.RawMessage
			if err := json.Unmarshal(header, &patch); err != nil {
				return err
			}
			for _, field := range []string{"issueTeamId", "inheritedTeamIds"} {
				if value, present := patch[field]; present {
					fields[field] = value
				} else {
					delete(fields, field)
				}
			}
			updated, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			if err := writeMetadataRecordRow(ctx, tx, workspace, metadataRecordKey{"documents", id}, metadataRecord{data: updated, order: order}, raw); err != nil {
				return err
			}
		}
		if visit, ok := ctx.Value(projectRecordPageObserverKey{}).(func(string, int) error); ok {
			if err := visit("documents", pageNumber); err != nil {
				return err
			}
		}
		cursor = next
	}
}

func (s *SQLiteStore) scaleP16InstallDocumentScopes(ctx context.Context, workspace string, source *domain.Bootstrap, plan ProjectRecordCascade) error {
	for cursor := 0; ; {
		keys, next, err := scaleP16CascadeKeys(source, plan, cursor)
		if err != nil || len(keys) == 0 {
			return err
		}
		for index := cursor; index < next; index++ {
			if !plan.Match(source, index) {
				continue
			}
			document := &source.Documents[index]
			header, _, _, err := scaleP16ReadDocumentScope(ctx, s.db, workspace, document.ID, "")
			if err != nil {
				return err
			}
			document.IssueTeamID, document.InheritedTeamIDs = header.IssueTeamID, header.InheritedTeamIDs
		}
		cursor = next
	}
}
