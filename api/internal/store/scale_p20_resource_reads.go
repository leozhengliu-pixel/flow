package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
	jsoniter "github.com/json-iterator/go"
)

var resourceWindowCoreFields = []string{"teams", "teamSettings", "teamParents", "workspaceSettings", "userSettings", "settings", "favorites", "favoriteFolders", "subscriptions", "states", "labels", "cycleSettings", "projectStatuses", "teamResourceSections", "teamPinnedResources", "users", "members", "teamMembers", "invitations", "departedMembers"}

func (s *SQLiteStore) ResourceWindowRevision(ctx context.Context, workspace string) (string, error) {
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	return strconv.FormatInt(stamp.revision, 10), err
}

// ResourceWindowMetadata uses the P17 immutable record snapshot and projection.
// Only directory headers are copied; unrelated bodies and restore payloads stay
// in the canonical records. Membership projection retains the legacy policy.
func (s *SQLiteStore) ResourceWindowMetadata(ctx context.Context, workspace, userID string, key *domain.APIKey, kind string) (domain.Bootstrap, string, error) {
	stamp, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return domain.Bootstrap{}, "", err
	}
	s.mu.RLock()
	source, exists := s.workspaces[workspace]
	known, exact := s.copyRevisionLocked(workspace)
	if !exists || !exact || known.workspaceID != stamp.workspaceID || known.revision != stamp.revision {
		s.mu.RUnlock()
		data, err := s.resourceWindowCanonicalMetadata(ctx, workspace, kind)
		if err != nil {
			return domain.Bootstrap{}, "", err
		}
		return s.resourceWindowProject(ctx, workspace, userID, key, data, stamp)
	}
	fields := slices.Clone(resourceWindowCoreFields)
	value := reflect.ValueOf(&source).Elem()
	for _, field := range persistedMetadataFields() {
		if slices.Contains([]string{"core", "documents", "projects", "initiatives", "cycles", "trash"}, kind) && slices.Contains([]string{"documents", "projects", "initiatives", "cycles"}, field.name) {
			fields = append(fields, field.name)
			value.Field(field.index).Set(resourceWindowHeaders(value.Field(field.index)))
		}
	}
	switch kind {
	case "core", "users", "members", "teamMembers", "departedMembers", "invitations":
	case "documents", "projects", "initiatives", "cycles":
	case "trash":
		fields = append(fields, "trash")
		entries := make([]domain.TrashEntry, len(source.Trash))
		for index, entry := range source.Trash {
			entry.Links = nil
			// The directory needs the saved resource's identity and parent, never
			// its body. Decode only those fields, even for a very large payload.
			var header struct {
				ID              string   `json:"id"`
				SlugID          string   `json:"slugId"`
				PreviousSlugIDs []string `json:"previousSlugIds,omitempty"`
				Title           string   `json:"title"`
				Icon            string   `json:"icon,omitempty"`
				Color           string   `json:"color,omitempty"`
				TeamIDs         []string `json:"teamIds"`
				ProjectIDs      []string `json:"projectIds"`
			}
			if json.Unmarshal(entry.Payload, &header) != nil {
				s.mu.RUnlock()
				return domain.Bootstrap{}, "", ErrResourceAccessChanged
			}
			entry.Payload, _ = json.Marshal(header)
			entries[index] = entry
		}
		source.Trash = entries
	default:
		s.mu.RUnlock()
		return domain.Bootstrap{}, "", ErrMetadataRecordQuery
	}
	scope := MetadataRecordScope{Fields: fields}
	data := metadataRecordSnapshot(source, scope, fields)
	s.mu.RUnlock()
	return s.resourceWindowProject(ctx, workspace, userID, key, data, stamp)
}

func (s *SQLiteStore) resourceWindowProject(ctx context.Context, workspace, userID string, key *domain.APIKey, data domain.Bootstrap, stamp copyStamp) (domain.Bootstrap, string, error) {
	var err error
	if userID != "" {
		data, err = s.projectDocumentRecordMetadata(ctx, workspace, userID, key, data)
		if err != nil {
			return domain.Bootstrap{}, "", err
		}
	} else {
		stripTrashLinks(&data)
		data.ViewerRole = "admin"
	}
	current, err := readMetadataStamp(ctx, s.db, workspace)
	if err != nil {
		return domain.Bootstrap{}, "", err
	}
	if current != stamp {
		return domain.Bootstrap{}, "", errors.Join(ErrAuthForbidden, ErrResourceAccessChanged)
	}
	return data, strconv.FormatInt(stamp.revision, 10), nil
}

func (s *SQLiteStore) resourceWindowCanonicalMetadata(ctx context.Context, workspace, kind string) (domain.Bootstrap, error) {
	if !slices.Contains([]string{"core", "documents", "projects", "initiatives", "cycles", "trash", "users", "members", "teamMembers", "departedMembers", "invitations"}, kind) {
		return domain.Bootstrap{}, ErrMetadataRecordQuery
	}
	data, err := s.resourceWindowCoreRecords(ctx, workspace)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return domain.Bootstrap{}, err
	}
	defer tx.Rollback()
	collections := []string{}
	if slices.Contains([]string{"core", "documents", "projects", "initiatives", "cycles", "trash"}, kind) {
		collections = append(collections, "documents", "projects", "initiatives", "cycles")
	}
	if kind == "trash" {
		collections = append(collections, kind)
	}
	for _, collection := range collections {
		for offset := 0; ; offset += 128 {
			rows, err := tx.QueryContext(ctx, `SELECT record_key,LENGTH(data) FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key LIMIT 128 OFFSET ?`, workspace, collection, offset)
			if err != nil {
				return domain.Bootstrap{}, err
			}
			type candidate struct {
				id   string
				size int64
			}
			candidates := []candidate{}
			for rows.Next() {
				var item candidate
				if err := rows.Scan(&item.id, &item.size); err != nil {
					rows.Close()
					return domain.Bootstrap{}, err
				}
				candidates = append(candidates, item)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return domain.Bootstrap{}, err
			}
			for _, item := range candidates {
				key := RecordKey{Kind: collection, ID: item.id}
				var raw json.RawMessage
				raw, err = s.resourceWindowRecordSummary(ctx, tx, workspace, key, item.size)
				if err != nil {
					return domain.Bootstrap{}, err
				}
				switch collection {
				case "documents":
					var item domain.Document
					err = json.Unmarshal(raw, &item)
					data.Documents = append(data.Documents, item)
				case "projects":
					var item domain.Project
					err = json.Unmarshal(raw, &item)
					data.Projects = append(data.Projects, item)
				case "initiatives":
					var item domain.Initiative
					err = json.Unmarshal(raw, &item)
					data.Initiatives = append(data.Initiatives, item)
				case "cycles":
					var item domain.Cycle
					err = json.Unmarshal(raw, &item)
					data.Cycles = append(data.Cycles, item)
				case "trash":
					var item domain.TrashEntry
					err = json.Unmarshal(raw, &item)
					data.Trash = append(data.Trash, item)
				}
				if err != nil {
					return domain.Bootstrap{}, err
				}
			}
			if len(candidates) < 128 {
				break
			}
		}
	}
	normalizeStoredMetadata(&data)
	return data, nil
}

// Old roots can contain unrelated inline collections. Extract only the core
// fields in SQL, then read their split records in bounded batches. This avoids
// decoding an unsplit legacy workspace just to obtain its directory policy.
func (s *SQLiteStore) resourceWindowCoreRecords(ctx context.Context, workspace string) (domain.Bootstrap, error) {
	root := map[string]json.RawMessage{}
	fields := append([]string{"workspace", "viewer", "viewerRole", metadataCollectionsKey}, resourceWindowCoreFields...)
	remaining := 2 << 20
	for _, field := range fields {
		expression := s.jsonText("data", field)
		var size sql.NullInt64
		var value sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT LENGTH(`+expression+`),CASE WHEN LENGTH(`+expression+`)<=? THEN `+expression+` ELSE NULL END FROM workspace_states WHERE workspace_key=?`, MetadataRecordPageMaxBytes, workspace).Scan(&size, &value); err != nil {
			return domain.Bootstrap{}, err
		}
		if size.Valid && size.Int64 > MetadataRecordPageMaxBytes {
			return domain.Bootstrap{}, ErrMetadataPageBudget
		}
		if !value.Valid {
			continue
		}
		raw := json.RawMessage(value.String)
		if field == "viewerRole" {
			raw, _ = json.Marshal(value.String)
		}
		remaining -= len(raw)
		if remaining < 0 {
			return domain.Bootstrap{}, ErrMetadataPageBudget
		}
		root[field] = raw
	}
	var collections map[string]string
	if raw := root[metadataCollectionsKey]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &collections); err != nil {
			return domain.Bootstrap{}, err
		}
	}
	delete(root, metadataCollectionsKey)
	for _, field := range resourceWindowCoreFields {
		kind := collections[field]
		if kind == "" {
			continue
		}
		array := []json.RawMessage{}
		object := map[string]json.RawMessage{}
		for offset := 0; ; offset += 128 {
			rows, err := s.db.QueryContext(ctx, `SELECT record_key,LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key LIMIT 128 OFFSET ?`, MetadataRecordPageMaxBytes, workspace, field, offset)
			if err != nil {
				return domain.Bootstrap{}, err
			}
			count := 0
			for rows.Next() {
				var id string
				var size int
				var raw []byte
				if err := rows.Scan(&id, &size, &raw); err != nil {
					rows.Close()
					return domain.Bootstrap{}, err
				}
				remaining -= size
				if size > MetadataRecordPageMaxBytes || raw == nil || remaining < 0 {
					rows.Close()
					return domain.Bootstrap{}, ErrMetadataPageBudget
				}
				if kind == "array" {
					array = append(array, raw)
				} else {
					object[id] = raw
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return domain.Bootstrap{}, err
			}
			if count < 128 {
				break
			}
		}
		if kind == "array" {
			root[field], _ = json.Marshal(array)
		} else {
			root[field], _ = json.Marshal(object)
		}
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return domain.Bootstrap{}, err
	}
	var data domain.Bootstrap
	err = json.Unmarshal(raw, &data)
	return data, err
}

func (s *SQLiteStore) resourceWindowRecordSummary(ctx context.Context, tx *sqlTx, workspace string, key RecordKey, size int64) (json.RawMessage, error) {
	r := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: key, size: size}
	iter := jsoniter.Parse(jsoniter.ConfigCompatibleWithStandardLibrary, r, 4096)
	used := 0
	var project func(bool) map[string]json.RawMessage
	project = func(payload bool) map[string]json.RawMessage {
		result := map[string]json.RawMessage{}
		for iter.Error == nil {
			field := iter.ReadObject()
			if field == "" {
				break
			}
			if field == "payload" && !payload {
				raw, _ := json.Marshal(project(true))
				result[field] = raw
			} else if (metadataSummaryFields[field] || field == "resources") && field != "milestones" {
				r.boundedCapture, r.captureLeft = true, metadataSummaryMaxBytes-used
				raw := iter.SkipAndReturnBytes()
				r.boundedCapture = false
				used += len(raw) + len(field) + 4
				if used > metadataSummaryMaxBytes {
					break
				}
				result[field] = raw
			} else {
				iter.Skip()
			}
		}
		return result
	}
	result := project(false)
	if iter.Error != nil || used > metadataSummaryMaxBytes || r.captureExceeded {
		return nil, ErrMetadataPageBudget
	}
	return json.Marshal(result)
}

func resourceWindowHeaders(value reflect.Value) reflect.Value {
	result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	for i := range value.Len() {
		item, target := value.Index(i), result.Index(i)
		for field := range item.NumField() {
			name := strings.Split(item.Type().Field(field).Tag.Get("json"), ",")[0]
			if (metadataSummaryFields[name] || name == "resources") && name != "milestones" {
				target.Field(field).Set(item.Field(field))
			}
		}
	}
	return result
}

// ResourceWindowCursor binds a continuation to its principal, credentials,
// permission epoch, query and snapshot. A mixed replica cannot continue a page
// after either metadata or access changed.
func ResourceWindowCursor(access AccessContext, q PageRequest, revision string) (int, error) {
	q.Filter = revision + ":" + q.Filter
	raw, err := openRecordCursor(access, q)
	if err != nil || raw == "" {
		return 0, err
	}
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 {
		return 0, ErrMetadataCursor
	}
	return index, nil
}

func SealResourceWindowCursor(access AccessContext, q PageRequest, revision string, index int) (string, error) {
	q.Filter = revision + ":" + q.Filter
	return sealRecordCursor(access, q, strconv.Itoa(index))
}
