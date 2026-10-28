package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"flow/api/internal/domain"
)

// documentInheritedScopeMigrationVersion records the one-time derivation of
// the teams documents inherit from their issue and projects.
const documentInheritedScopeMigrationVersion = 1006

// documentScopeMigrationBatch bounds the documents rewritten per transaction
// and the issue ids looked up per query.
var documentScopeMigrationBatch = 500

// migrateDocumentInheritedScope gives documents stored before inherited
// scope existed their InheritedTeamIDs and IssueTeamID: the team of the issue
// each is attached to and the teams of its projects, as attaching it today
// records them (domain.InheritedDocumentTeams). Until then a document on a
// private team's issue stayed readable across the workspace until its links
// changed. Only those two derived fields change: grants, the document's own
// teams, its version and update time stay as they are. Deleted documents in
// "Recently deleted" are migrated too, so a restore brings the scope back.
//
// Issues are not held in memory, so their teams are read from issue_records
// in batches, and the documents are written in batches through the metadata
// delta writer. A document whose derived fields already match is left alone,
// which makes the migration idempotent.
func (s *SQLiteStore) migrateDocumentInheritedScope(ctx context.Context) error {
	documents, deleted := 0, 0
	for _, key := range s.WorkspaceKeys() {
		live, trashed, err := s.migrateWorkspaceDocumentScope(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		documents += live
		deleted += trashed
	}
	if documents+deleted > 0 {
		slog.Info("derived inherited document scope", "documents", documents, "deleted_documents", deleted)
	}
	return nil
}

// documentScopeLinks are the fields of a live or deleted document the
// inherited scope derives from and is written to.
type documentScopeLinks struct {
	IssueID          string   `json:"issueId"`
	ProjectIDs       []string `json:"projectIds"`
	InheritedTeamIDs []string `json:"inheritedTeamIds"`
	IssueTeamID      string   `json:"issueTeamId"`
}

// trashedDocumentLinks decodes a deleted document's links; ok is false for a
// payload restore would skip too.
func trashedDocumentLinks(payload json.RawMessage) (documentScopeLinks, bool) {
	var links documentScopeLinks
	if len(payload) == 0 || payload[0] != '{' || json.Unmarshal(payload, &links) != nil {
		return documentScopeLinks{}, false
	}
	return links, true
}

// derivedDocumentScope returns the inherited teams and issue team for links,
// and whether they differ from the stored ones. issueTeams maps issue ids to
// their team; an issue missing from it keeps the recorded issue team, as the
// server does for an issue it did not load.
func derivedDocumentScope(data domain.Bootstrap, links documentScopeLinks, issueTeams map[string]string) ([]string, string, bool) {
	issueTeamID := links.IssueTeamID
	if links.IssueID == "" {
		issueTeamID = ""
	} else if teamID, ok := issueTeams[links.IssueID]; ok {
		issueTeamID = teamID
	}
	inherited := domain.InheritedDocumentTeams(issueTeamID, links.ProjectIDs, data.Projects, data.Teams)
	return inherited, issueTeamID, issueTeamID != links.IssueTeamID || !slices.Equal(inherited, links.InheritedTeamIDs)
}

func (s *SQLiteStore) migrateWorkspaceDocumentScope(ctx context.Context, key string) (int, int, error) {
	stored, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	issueIDs, seen := []string{}, map[string]bool{}
	addIssue := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			issueIDs = append(issueIDs, id)
		}
	}
	for _, document := range stored.Documents {
		addIssue(document.IssueID)
	}
	for _, entry := range stored.Trash {
		if links, ok := trashedDocumentLinks(entry.Payload); ok && entry.ResourceType == "document" {
			addIssue(links.IssueID)
		}
	}
	issueTeams, err := s.issueTeamsByID(ctx, key, stored.Issues, issueIDs)
	if err != nil {
		return 0, 0, err
	}
	targets := []documentAccessTarget{}
	for index, document := range stored.Documents {
		if _, _, changed := derivedDocumentScope(stored, documentScopeLinks{IssueID: document.IssueID, ProjectIDs: document.ProjectIDs, InheritedTeamIDs: document.InheritedTeamIDs, IssueTeamID: document.IssueTeamID}, issueTeams); changed {
			targets = append(targets, documentAccessTarget{index: index})
		}
	}
	for index, entry := range stored.Trash {
		if entry.ResourceType != "document" {
			continue
		}
		if links, ok := trashedDocumentLinks(entry.Payload); ok {
			if _, _, changed := derivedDocumentScope(stored, links, issueTeams); changed {
				targets = append(targets, documentAccessTarget{trash: true, index: index})
			}
		}
	}
	live, trashed := 0, 0
	for start := 0; start < len(targets); start += documentScopeMigrationBatch {
		batch := targets[start:min(start+documentScopeMigrationBatch, len(targets))]
		l, t, err := s.migrateDocumentScopeBatch(ctx, key, batch, issueTeams)
		if err != nil {
			return live, trashed, err
		}
		live += l
		trashed += t
	}
	if live+trashed > 0 {
		s.dropMetadataCache(ctx, key)
		s.mu.Lock()
		if !slices.Contains(s.startupCacheDrops, key) {
			s.startupCacheDrops = append(s.startupCacheDrops, key)
		}
		s.mu.Unlock()
	}
	return live, trashed, nil
}

// issueTeamsByID reads the teams of the given issues from issue_records,
// documentScopeMigrationBatch ids per query, falling back to issues still
// held in the metadata snapshot.
func (s *SQLiteStore) issueTeamsByID(ctx context.Context, key string, loaded []domain.Issue, ids []string) (map[string]string, error) {
	teams := make(map[string]string, len(ids))
	if len(loaded) > 0 {
		wanted := make(map[string]bool, len(ids))
		for _, id := range ids {
			wanted[id] = true
		}
		for _, issue := range loaded {
			if wanted[issue.ID] {
				teams[issue.ID] = issue.Team.ID
			}
		}
	}
	for start := 0; start < len(ids); start += documentScopeMigrationBatch {
		chunk := ids[start:min(start+documentScopeMigrationBatch, len(ids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, key)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id,team_id FROM issue_records WHERE workspace_key=? AND id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("read issue teams: %w", err)
		}
		for rows.Next() {
			var id, teamID string
			if err := rows.Scan(&id, &teamID); err != nil {
				rows.Close()
				return nil, err
			}
			teams[id] = teamID
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return teams, nil
}

func (s *SQLiteStore) migrateDocumentScopeBatch(ctx context.Context, key string, batch []documentAccessTarget, issueTeams map[string]string) (int, int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	before, ok := s.storedWorkspace(key)
	if !ok {
		return 0, 0, nil
	}
	after := before
	documentsCloned, trashCloned := false, false
	live, trashed := 0, 0
	for _, target := range batch {
		if !target.trash {
			if target.index >= len(before.Documents) {
				continue
			}
			document := before.Documents[target.index]
			inherited, issueTeamID, changed := derivedDocumentScope(before, documentScopeLinks{IssueID: document.IssueID, ProjectIDs: document.ProjectIDs, InheritedTeamIDs: document.InheritedTeamIDs, IssueTeamID: document.IssueTeamID}, issueTeams)
			if !changed {
				continue
			}
			if !documentsCloned {
				after.Documents, documentsCloned = slices.Clone(before.Documents), true
			}
			after.Documents[target.index].InheritedTeamIDs = inherited
			after.Documents[target.index].IssueTeamID = issueTeamID
			live++
			continue
		}
		if target.index >= len(before.Trash) {
			continue
		}
		payload, changed, err := trashedDocumentScope(before, before.Trash[target.index].Payload, issueTeams)
		if err != nil {
			return 0, 0, err
		}
		if !changed {
			continue
		}
		if !trashCloned {
			after.Trash, trashCloned = slices.Clone(before.Trash), true
		}
		after.Trash[target.index].Payload = payload
		trashed++
	}
	if live+trashed == 0 {
		return 0, 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := s.persistMetadataTx(ctx, tx, key, before, &after); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	s.installWorkspace(key, after, false)
	return live, trashed, nil
}

// trashedDocumentScope writes the derived scope into a deleted document's
// payload. Only those two members change; every other member keeps its
// stored encoding.
func trashedDocumentScope(data domain.Bootstrap, payload json.RawMessage, issueTeams map[string]string) (json.RawMessage, bool, error) {
	links, ok := trashedDocumentLinks(payload)
	if !ok {
		return payload, false, nil
	}
	inherited, issueTeamID, changed := derivedDocumentScope(data, links, issueTeams)
	if !changed {
		return payload, false, nil
	}
	root := map[string]json.RawMessage{}
	if json.Unmarshal(payload, &root) != nil {
		return payload, false, nil
	}
	// The document's own encoding omits empty values.
	if len(inherited) == 0 {
		delete(root, "inheritedTeamIds")
	} else {
		encoded, err := json.Marshal(inherited)
		if err != nil {
			return payload, false, err
		}
		root["inheritedTeamIds"] = encoded
	}
	if issueTeamID == "" {
		delete(root, "issueTeamId")
	} else {
		encoded, err := json.Marshal(issueTeamID)
		if err != nil {
			return payload, false, err
		}
		root["issueTeamId"] = encoded
	}
	next, err := json.Marshal(root)
	if err != nil {
		return payload, false, err
	}
	return next, true, nil
}
