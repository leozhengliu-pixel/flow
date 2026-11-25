package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"flow/api/internal/domain"

	segmentjson "github.com/segmentio/encoding/json"
)

type recordJournalProjection struct {
	Kind, ID, Version, Created, Updated string
	Order, Bytes                        int64
	Summary                             json.RawMessage
	Omitted, OldOmitted, Archived       int
	Deleted                             bool
	Rewrite                             bool
	Relations                           []recordJournalRelation
}

type recordJournalRelation struct{ Name, Hash, Value string }

// SQLite's one writer already serializes these sidecars. One prepared
// statement avoids repeated driver calls for a small commit.
// The input view stores no rows or extra WAL frames. Canonical triggers
// contain no application functions and remain executable by old replicas.
func (s *SQLiteStore) ensureSQLiteRecordJournalFlush(ctx context.Context) error {
	if err := s.ensureSQLiteRecordJournalProjection(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `CREATE VIEW IF NOT EXISTS record_journal_commit_v3 AS SELECT CAST(NULL AS TEXT) AS tx_id,CAST(NULL AS BLOB) AS data WHERE 0`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `CREATE TRIGGER IF NOT EXISTS flow_rj_commit_v3 INSTEAD OF INSERT ON record_journal_commit_v3 BEGIN
		INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision)
			SELECT json_extract(q.value,'$.Workspace'),json_extract(q.value,'$.Kind'),abs(random()%16),1
			FROM json_each(NEW.data,'$.QueuedRevisions') q WHERE json_extract(q.value,'$.Flush')
			ON CONFLICT(workspace_key,kind,shard) DO UPDATE SET revision=workspace_cache_revisions.revision+1;
		INSERT INTO workspace_cache_revisions(workspace_key,kind,shard,revision)
			SELECT DISTINCT d.workspace_key,CASE WHEN d.source IN ('workspace_metadata_records','workspace_states') THEN 'metadata' ELSE 'issues' END,0,1 FROM (
				SELECT workspace_key,source FROM record_journal_dirty WHERE tx_id=NEW.tx_id
				UNION SELECT json_extract(k.value,'$.workspace'),json_extract(k.value,'$.source') FROM record_journal_context i,json_each(i.keys) k WHERE i.id=1
			) d WHERE d.source IN ('workspace_metadata_records','workspace_states','issue_records','workspace_content_records') AND json_extract(NEW.data,'$.CacheRevisions')=1
			AND NOT EXISTS(SELECT 1 FROM json_each(NEW.data,'$.QueuedRevisions') q WHERE json_extract(q.value,'$.Workspace')=d.workspace_key AND json_extract(q.value,'$.Kind')=CASE WHEN d.source IN ('workspace_metadata_records','workspace_states') THEN 'metadata' ELSE 'issues' END)
			ON CONFLICT(workspace_key,kind,shard) DO UPDATE SET revision=workspace_cache_revisions.revision+1;
		UPDATE metadata_catalog_collections SET
			revision=NEW.tx_id,
			omissions=omissions+COALESCE((SELECT SUM(json_extract(c.value,'$.Omitted')-json_extract(c.value,'$.OldOmitted'))
				FROM json_each(NEW.data,'$.Workspaces') w,json_each(w.value,'$.Catalog') c
				WHERE json_extract(w.value,'$.Key')=workspace_key AND json_extract(c.value,'$.Kind')=field
				AND (phase>=1 OR (phase=0 AND started=1 AND last_record_key>=json_extract(c.value,'$.ID')))),0)
			WHERE (workspace_key,field) IN(SELECT json_extract(w.value,'$.Key'),json_extract(c.value,'$.Kind') FROM json_each(NEW.data,'$.Workspaces') w,json_each(w.value,'$.Catalog') c);
		INSERT INTO record_journal_projection_v3(workspace_key,field,record_key,data)
			SELECT json_extract(w.value,'$.Key'),json_extract(c.value,'$.Kind'),json_extract(c.value,'$.ID'),jsonb(c.value)
			FROM json_each(NEW.data,'$.Workspaces') w,json_each(w.value,'$.Catalog') c;
		INSERT INTO workspace_record_journal_heads(workspace_id,head,pruned)
			SELECT json_extract(value,'$.ID'),1,0 FROM json_each(NEW.data,'$.Workspaces') WHERE 1=1
			ON CONFLICT(workspace_id) DO UPDATE SET head=workspace_record_journal_heads.head+1;
		INSERT INTO record_journal(id,workspace_id,seq,authz_epoch,payload,created_at)
			SELECT json_extract(w.value,'$.Change.txId'),json_extract(w.value,'$.ID'),h.head,json_extract(w.value,'$.Change.authzEpoch'),json_extract(w.value,'$.Change'),json_extract(NEW.data,'$.Created')
			FROM json_each(NEW.data,'$.Workspaces') w JOIN workspace_record_journal_heads h ON h.workspace_id=json_extract(w.value,'$.ID');
		INSERT INTO record_journal_events(event_id,journal_id,workspace_id)
			SELECT e.value,json_extract(w.value,'$.Change.txId'),json_extract(w.value,'$.ID') FROM json_each(NEW.data,'$.Workspaces') w,json_each(w.value,'$.Events') e
			WHERE json_array_length(w.value,'$.Events')<>1 OR json_extract(w.value,'$.Change.txId')<>'rje:'||e.value;
		DELETE FROM record_journal_dirty WHERE tx_id=NEW.tx_id;
		DELETE FROM record_journal_event_pending WHERE tx_id=NEW.tx_id;
		UPDATE record_journal_context SET keys='[]',events='[]' WHERE id=1;
		UPDATE record_journal_context SET tx_id='' WHERE id=1;
	END`)
	if err != nil {
		return err
	}
	return s.ensureSQLiteRecordJournalCommit(ctx)
}

func projectRecordJournalSummary(raw []byte, row *recordJournalProjection) error {
	row.Version = metadataFingerprint(raw)
	used := 0
	var project func([]byte, bool) (map[string]json.RawMessage, error)
	project = func(input []byte, milestone bool) (map[string]json.RawMessage, error) {
		// The source is bounded and owned by this flush. Borrow names and
		// values only until encoding the summary; no borrowed data escapes.
		fields := make(map[string]json.RawMessage)
		if _, err := segmentjson.Parse(input, &fields, segmentjson.ZeroCopy); err != nil {
			return nil, err
		}
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
		for field, value := range fields {
			if !metadataSummaryFields[field] || milestone && field == "milestones" {
				delete(fields, field)
				continue
			}
			if field == "milestones" && len(bytes.TrimSpace(value)) > 0 && bytes.TrimSpace(value)[0] == '[' {
				var rawItems []json.RawMessage
				if _, err := segmentjson.Parse(value, &rawItems, segmentjson.ZeroCopy); err != nil {
					return nil, err
				}
				items := []map[string]json.RawMessage{}
				for _, item := range rawItems {
					projected, err := project(item, true)
					if err != nil {
						return nil, err
					}
					items = append(items, projected)
				}
				value, _ = json.Marshal(items)
			}
			used += len(field) + len(value) + 4
			fields[field] = value
		}
		return fields, nil
	}
	var fields map[string]json.RawMessage
	var err error
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
		fields, err = project(raw, false)
	} else {
		row.Omitted = 1
	}
	if err != nil {
		return fmt.Errorf("metadata %s/%s: invalid canonical JSON: %w", row.Kind, row.ID, err)
	}
	if used > metadataSummaryMaxBytes || row.Omitted != 0 {
		row.Omitted = 1
		id, _ := json.Marshal(row.ID)
		fields = map[string]json.RawMessage{"id": id}
	}
	row.Summary, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	var dates struct {
		CreatedAt  time.Time  `json:"createdAt"`
		UpdatedAt  time.Time  `json:"updatedAt"`
		ArchivedAt *time.Time `json:"archivedAt"`
	}
	if err := json.Unmarshal(row.Summary, &dates); err != nil {
		return fmt.Errorf("metadata %s/%s: invalid canonical timestamps", row.Kind, row.ID)
	}
	row.Created = dates.CreatedAt.UTC().Format(issueRecordTimestamp)
	row.Updated = dates.UpdatedAt.UTC().Format(issueRecordTimestamp)
	if dates.UpdatedAt.IsZero() {
		row.Updated = row.Created
	}
	if dates.ArchivedAt != nil {
		row.Archived = 1
	}
	// Decode only present relations. The P05 extractor deliberately accepts
	// heterogeneous fields; invoking it on absent fields allocates an error
	// for each possible relation on every small write.
	for name, values := range recordJournalSummaryRelations(fields) {
		seen := map[string]bool{}
		for _, value := range values {
			if !seen[value] {
				row.Relations = append(row.Relations, recordJournalRelation{name, metadataFingerprint([]byte(value)), value})
				seen[value] = true
			}
		}
	}
	return nil
}

func recordJournalSummaryRelations(fields map[string]json.RawMessage) map[string][]string {
	result := map[string][]string{}
	for name, raw := range fields {
		switch name {
		case "teamIds", "memberIds", "labelIds", "dependencyIds", "initiatives", "customers", "projectIds", "inheritedTeamIds", "contributingTeamIds", "parentInitiativeIds", "subscriberIds", "previousSlugIds", "domains", "externalIds":
			var items []string
			if json.Unmarshal(raw, &items) == nil {
				result[name] = items
			}
		case "id", "slugId", "issueId", "issueTeamId", "leadTeamId", "creatorId", "ownerId", "resourceType", "resourceId", "pipelineId", "releaseId", "customerId", "projectId", "userId", "scope", "resource", "groupId", "parentTeamId":
			var value string
			if json.Unmarshal(raw, &value) == nil {
				result[name] = []string{value}
				if name == "slugId" && domain.SlugSuffix(value) != "" {
					result["slugSuffix"] = []string{domain.SlugSuffix(value)}
				}
			}
		case "lead", "owner", "creator", "status":
			var value struct{ ID, Name, Type string }
			if json.Unmarshal(raw, &value) == nil {
				result[name] = []string{value.ID}
				if name == "status" {
					result[name] = append(result[name], value.Name, value.Type)
				}
			}
		case "milestones":
			var items []struct{ ID, Name string }
			if json.Unmarshal(raw, &items) == nil {
				for _, value := range items {
					result[name] = append(result[name], value.ID, value.Name)
				}
			}
		}
	}
	return result
}
