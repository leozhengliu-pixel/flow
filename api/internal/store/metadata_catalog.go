package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"flow/api/internal/domain"

	jsoniter "github.com/json-iterator/go"
)

// This is a summary allowlist, not a replacement serialization of a record.
// Bodies, restore payloads, histories and collaboration state are never kept.
var metadataSummaryFields = func() map[string]bool {
	fields := strings.Fields(`id name title slugId previousSlugIds summary icon color priority position priorityLabel progress health status lead creator creatorId owner ownerId botActor memberIds labelIds teamIds dependencyIds initiatives customers milestones updateCadence startDate startDateResolution targetDate targetDateResolution slackChannelId slackChannelName issueCount archivedAt createdAt updatedAt projectIds issueId inheritedTeamIds issueTeamId subscriberIds version contentVersion collaborationId deletedAt resourceType resourceId deletedBy expiresAt leadTeamId contributingTeamIds parentInitiativeIds favorite subscribed displayName email avatarUrl active key scope resource groupId type access parentTeamId permissions domains externalIds tier revenue size pipelineId releaseId customerId projectId userId`)
	result := make(map[string]bool, len(fields))
	for _, field := range fields {
		result[field] = true
	}
	return result
}()

type metadataCatalogRow struct {
	key              RecordKey
	order, bodyBytes int64
	version          string
	summary          json.RawMessage
	omitted          bool
	created, updated string
	archived         int
}

// projectCanonicalMetadata uses the existing streaming JSON parser to skip
// large values without materializing them. Capture itself is bounded too:
// an oversized title or relationship array becomes an explicit omission.
func (s *SQLiteStore) projectCanonicalMetadata(ctx context.Context, tx *sqlTx, workspace string, key RecordKey, size int64) (metadataCatalogRow, error) {
	row := metadataCatalogRow{key: key, bodyBytes: size}
	r := &canonicalMetadataReader{ctx: ctx, tx: tx, workspace: workspace, key: key, size: size}
	hash := sha256.New()
	source := io.TeeReader(r, hash)
	iter := jsoniter.Parse(jsoniter.ConfigCompatibleWithStandardLibrary, source, 4096)
	used := 0
	var projectObject func(bool) map[string]json.RawMessage
	projectObject = func(milestone bool) map[string]json.RawMessage {
		result := map[string]json.RawMessage{}
		for iter.Error == nil {
			r.boundedCapture, r.captureLeft = true, metadataSummaryMaxBytes-used
			field := iter.ReadObject()
			r.boundedCapture = false
			if field == "" || iter.Error != nil {
				break
			}
			if !metadataSummaryFields[field] || milestone && field == "milestones" {
				iter.Skip()
				continue
			}
			var raw []byte
			if field == "milestones" && iter.WhatIsNext() == jsoniter.ArrayValue {
				items := []map[string]json.RawMessage{}
				for iter.ReadArray() && iter.Error == nil {
					items = append(items, projectObject(true))
					if used > metadataSummaryMaxBytes {
						break
					}
				}
				raw, _ = json.Marshal(items)
			} else {
				r.boundedCapture, r.captureLeft = true, metadataSummaryMaxBytes-used
				raw = iter.SkipAndReturnBytes()
				r.boundedCapture = false
			}
			used += len(field) + len(raw) + 4
			if used > metadataSummaryMaxBytes {
				row.omitted = true
				break
			}
			result[field] = raw
		}
		return result
	}
	var summary map[string]json.RawMessage
	if iter.WhatIsNext() == jsoniter.ObjectValue {
		summary = projectObject(false)
	} else {
		iter.Skip()
		row.omitted = true
	}
	if iter.Error != nil {
		// The iterator decorates reader errors; the exhausted capture budget
		// distinguishes omission from corrupt canonical JSON or SQL failure.
		if r.captureExceeded {
			row.omitted = true
		} else {
			if err := ctx.Err(); err != nil {
				return row, err
			}
			return row, fmt.Errorf("metadata %s/%s: invalid canonical JSON", key.Kind, key.ID)
		}
	}
	r.boundedCapture = false
	if _, err := io.Copy(io.Discard, source); err != nil {
		return row, err
	}
	row.version = hex.EncodeToString(hash.Sum(nil))
	if row.omitted {
		summary = map[string]json.RawMessage{}
		id, _ := json.Marshal(key.ID)
		summary["id"] = id
	}
	var err error
	row.summary, err = json.Marshal(summary)
	if err != nil {
		return row, err
	}
	var dates struct {
		CreatedAt  time.Time  `json:"createdAt"`
		UpdatedAt  time.Time  `json:"updatedAt"`
		ArchivedAt *time.Time `json:"archivedAt"`
	}
	if err := json.Unmarshal(row.summary, &dates); err != nil {
		return row, fmt.Errorf("metadata %s/%s: invalid canonical timestamps", key.Kind, key.ID)
	}
	row.created = dates.CreatedAt.UTC().Format(issueRecordTimestamp)
	row.updated = dates.UpdatedAt.UTC().Format(issueRecordTimestamp)
	if dates.UpdatedAt.IsZero() {
		row.updated = row.created
	}
	if dates.ArchivedAt != nil {
		row.archived = 1
	}
	return row, nil
}

func metadataCatalogRelations(raw []byte) map[string][]string {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	result := map[string][]string{}
	for _, name := range strings.Fields(`teamIds memberIds labelIds dependencyIds initiatives customers projectIds inheritedTeamIds contributingTeamIds parentInitiativeIds subscriberIds previousSlugIds domains externalIds`) {
		var items []string
		if json.Unmarshal(value[name], &items) == nil {
			result[name] = items
		}
	}
	for _, name := range strings.Fields(`id slugId issueId issueTeamId leadTeamId creatorId ownerId resourceType resourceId pipelineId releaseId customerId projectId userId scope resource groupId parentTeamId`) {
		var item string
		if json.Unmarshal(value[name], &item) == nil {
			result[name] = []string{item}
		}
	}
	for _, name := range []string{"lead", "owner", "creator", "status"} {
		var item struct{ ID, Name, Type string }
		if json.Unmarshal(value[name], &item) == nil {
			result[name] = []string{item.ID}
			if name == "status" {
				result[name] = append(result[name], item.Name, item.Type)
			}
		}
	}
	var slug string
	if json.Unmarshal(value["slugId"], &slug) == nil && domain.SlugSuffix(slug) != "" {
		result["slugSuffix"] = []string{domain.SlugSuffix(slug)}
	}
	var milestones []struct{ ID, Name string }
	if json.Unmarshal(value["milestones"], &milestones) == nil {
		for _, item := range milestones {
			result["milestones"] = append(result["milestones"], item.ID, item.Name)
		}
	}
	return result
}

// MetadataCatalogStatus deliberately never declares writer coverage. A pass
// is useful for shadow comparison, but old replicas and direct SQL can change
// already-scanned rows. P09 owns the transactional coverage required to serve.
type MetadataCatalogStatus struct {
	Complete bool
	Shadow   bool
	Ready    bool
	Revision string
}

// The existing customer summary intentionally hides filter-only fields from
// legacy JSON. The trusted shadow adapter carries those fields explicitly.
type metadataCatalogCustomer struct {
	ID, OwnerID, Status, Tier string
	Important                 bool
	Revenue                   float64
	Size                      int64
}

type metadataCatalogCustomers struct {
	Customers       []metadataCatalogCustomer
	UnknownCustomer bool
}

func (s *SQLiteStore) ReadMetadataCatalogProjectPage(ctx context.Context, query ProjectRecordQuery, q PageRequest) (RecordPage, error) {
	filter := struct {
		ProjectRecordQuery
		CustomerDigest string
	}{ProjectRecordQuery: query, CustomerDigest: metadataCatalogCustomerDigest(query.CustomerSummaries)}
	filter.Cursor = ""
	raw, err := json.Marshal(filter)
	if err != nil {
		return RecordPage{}, err
	}
	q.Kind, q.Filter = "projects", string(raw)
	return s.readMetadataCatalogPage(ctx, query.Workspace, q, &query)
}

func metadataCatalogCustomerDigest(summaries map[string]*ProjectCustomerSummary) string {
	var sum [sha256.Size]byte
	for id, summary := range summaries {
		hash := sha256.New()
		encoder := json.NewEncoder(hash)
		_ = encoder.Encode(id)
		if summary != nil {
			_ = encoder.Encode(summary.UnknownCustomer)
			for _, c := range summary.Customers {
				_ = encoder.Encode(metadataCatalogCustomer{ID: c.ID, OwnerID: c.OwnerID, Status: c.Status, Tier: c.Tier, Important: c.Important, Revenue: c.Revenue, Size: c.Size})
			}
		}
		// Addition makes map iteration order irrelevant without sorting or
		// copying the caller's directory of customer summaries.
		var carry uint16
		for i, b := range hash.Sum(nil) {
			carry += uint16(sum[i]) + uint16(b)
			sum[i], carry = byte(carry), carry>>8
		}
	}
	return hex.EncodeToString(sum[:])
}

func (s *SQLiteStore) MetadataCatalogStatus(ctx context.Context, workspace, kind string) (MetadataCatalogStatus, error) {
	status := MetadataCatalogStatus{Shadow: true}
	var phase int
	err := s.db.QueryRowContext(ctx, `SELECT phase,revision FROM metadata_catalog_collections WHERE workspace_key=? AND field=?`, workspace, kind).Scan(&phase, &status.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return status, nil
	}
	status.Complete = phase == 2
	return status, err
}

// ReadMetadataCatalogPage is for trusted shadow comparisons only. Filter is
// a JSON ProjectRecordQuery for projects; it reuses the canonical predicates,
// including customer summaries supplied by the trusted caller. Other kinds
// currently support unfiltered summary pages. No canonical body is fetched.
// Public access-bound tokens and field redaction belong to P03/P10.
func (s *SQLiteStore) ReadMetadataCatalogPage(ctx context.Context, workspace string, q PageRequest) (RecordPage, error) {
	return s.readMetadataCatalogPage(ctx, workspace, q, nil)
}

func (s *SQLiteStore) readMetadataCatalogPage(ctx context.Context, workspace string, q PageRequest, projectQuery *ProjectRecordQuery) (RecordPage, error) {
	page := RecordPage{Items: []MetadataRecord{}}
	limit, budget, err := metadataPageLimits(q)
	if err != nil {
		return page, err
	}
	if workspace == "" || !validMetadataKey(RecordKey{Kind: q.Kind}) || len(q.Filter) > 64<<10 {
		return page, ErrMetadataRecordQuery
	}
	query := ProjectRecordQuery{Workspace: workspace, Admin: true}
	if projectQuery != nil {
		query = *projectQuery
	} else if q.Filter != "" {
		if q.Kind != "projects" || json.Unmarshal([]byte(q.Filter), &query) != nil {
			return page, ErrMetadataRecordQuery
		}
		var extra struct {
			CustomerSummaries map[string]metadataCatalogCustomers `json:"customerSummaries"`
		}
		if err := json.Unmarshal([]byte(q.Filter), &extra); err != nil {
			return page, ErrMetadataRecordQuery
		}
		query.CustomerSummaries = map[string]*ProjectCustomerSummary{}
		for id, summary := range extra.CustomerSummaries {
			value := &ProjectCustomerSummary{UnknownCustomer: summary.UnknownCustomer}
			for _, customer := range summary.Customers {
				value.Customers = append(value.Customers, ProjectCustomer{ID: customer.ID, OwnerID: customer.OwnerID, Status: customer.Status, Tier: customer.Tier, Important: customer.Important, Revenue: customer.Revenue, Size: customer.Size})
			}
			query.CustomerSummaries[id] = value
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var phase int
	var omissions int64
	journal := recordJournalMode() == "on" && s.db.journalReady.Load()
	if journal {
		if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
			return page, err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT phase,revision,omissions FROM metadata_catalog_collections WHERE workspace_key=? AND field=?`, workspace, q.Kind).Scan(&phase, &page.Revision, &omissions)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (phase != 2 || q.Kind == "projects" && omissions > 0) {
		return page, ErrMetadataCatalogGap
	}
	if err != nil {
		return page, err
	}
	binding := catalogCursorBinding(workspace, q)
	cursor, err := decodeMetadataCatalogCursor(q.Cursor, binding, page.Revision)
	if err != nil {
		return page, err
	}
	statement, args, err := metadataCatalogPageStatement(workspace, q, query, cursor, journal)
	if err != nil {
		return page, err
	}
	// Rows stream through one bounded summary at a time. Sparse filters may
	// scan more IDs, but never retain the collection or consult canonical data.
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	remaining := budget - metadataPageOverhead
	last := cursor
	scanned, scannedBytes := 0, 0
	for rows.Next() {
		if scanned == MetadataCatalogBackfillBatch {
			page.HasMore = true
			break
		}
		scanned++
		var row metadataCatalogRow
		var omitted int
		row.key.Kind = q.Kind
		if err := rows.Scan(&row.key.ID, &row.order, &row.version, &row.summary, &omitted, &row.created, &row.updated); err != nil {
			return page, err
		}
		if journal && row.version == "0" {
			return page, ErrRecordJournalCoverage
		}
		if scannedBytes > 0 && scannedBytes+len(row.summary) > MetadataRecordPageMaxBytes {
			page.HasMore = true
			break
		}
		scannedBytes += len(row.summary)
		position := metadataCatalogCursor{Schema: 2, Binding: binding, Revision: page.Revision, Ordinal: row.order, ID: row.key.ID, Sort: row.created}
		if q.Order == "updatedAt" {
			position.Sort = row.updated
		}
		if q.Kind == "projects" {
			if omitted != 0 {
				return page, ErrMetadataCatalogGap
			}
			var project domain.Project
			if err := json.Unmarshal(row.summary, &project); err != nil {
				return page, err
			}
			// The canonical helper is the oracle for nil/empty team scopes,
			// legacy slugs, negation, milestone names and customer filters.
			query.Cursor, query.Limit, query.IncludeTotal = "", 1, false
			if len(filterProjectDirectory([]domain.Project{project}, query).Items) == 0 {
				last = position
				continue
			}
		}
		item := MetadataRecord{Key: row.key, Version: row.version, Data: row.summary, Omitted: omitted != 0}
		if item.Omitted {
			item.Detail = &item.Key
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return page, err
		}
		if len(page.Items) >= limit || len(encoded)+1 > remaining && len(page.Items) > 0 {
			page.HasMore = true
			break
		}
		if len(encoded)+1 > remaining {
			item.Data, item.Omitted, item.Detail = nil, true, &item.Key
			encoded, _ = json.Marshal(item)
			if len(encoded)+1 > remaining {
				return page, ErrMetadataPageBudget
			}
		}
		remaining -= len(encoded) + 1
		page.Items = append(page.Items, item)
		last = position
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.HasMore {
		raw, _ := json.Marshal(last)
		page.Next = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

func metadataCatalogPageStatement(workspace string, q PageRequest, query ProjectRecordQuery, cursor metadataCatalogCursor, journal bool) (string, []any, error) {
	column, descending := "collection_order", false
	switch q.Order {
	case "", "collection":
	case "createdAt", "updatedAt":
		column, descending = map[string]string{"createdAt": "created_at", "updatedAt": "updated_at"}[q.Order], true
	default:
		return "", nil, ErrMetadataRecordQuery
	}
	where, args := `workspace_key=? AND field=?`, []any{workspace, q.Kind}
	if cursor.Schema != 0 {
		if journal {
			// A tuple seek keeps version probes on one ordered index scan.
			comparison := ">"
			var value any = cursor.Ordinal
			if descending {
				comparison, value = "<", cursor.Sort
			}
			where += ` AND (` + column + `,record_key)` + comparison + `(?,?)`
			args = append(args, value, cursor.ID)
		} else if descending {
			where += ` AND (` + column + `<? OR (` + column + `=? AND record_key<?))`
			args = append(args, cursor.Sort, cursor.Sort, cursor.ID)
		} else {
			where += ` AND (collection_order>? OR (collection_order=? AND record_key>?))`
			args = append(args, cursor.Ordinal, cursor.Ordinal, cursor.ID)
		}
	}
	if q.Kind == "projects" && query.Archived != "all" {
		where += ` AND archived=?`
		archived := 0
		if query.Archived == "true" {
			archived = 1
		}
		args = append(args, archived)
	}
	if q.Kind == "projects" {
		teams := `SELECT 1 FROM metadata_catalog_relations r WHERE r.workspace_key=metadata_catalog.workspace_key AND r.field=metadata_catalog.field AND r.record_key=metadata_catalog.record_key AND r.relation='teamIds'`
		teamMatch := func(ids []string) string {
			if len(ids) == 0 {
				return "0=1"
			}
			hashes := make([]string, len(ids))
			for i, id := range ids {
				hashes[i] = metadataFingerprint([]byte(id))
			}
			clause, values := bindList("r.value_hash", hashes)
			args = append(args, values...)
			return "EXISTS (" + teams + " AND " + clause + ")"
		}
		if len(query.TeamIDs) > 0 {
			where += " AND " + teamMatch(query.TeamIDs)
		}
		if !query.Admin && query.AllowedTeamIDs != nil {
			where += " AND (NOT EXISTS (" + teams + ") OR " + teamMatch(query.AllowedTeamIDs) + ")"
		}
	}
	order := column + ",record_key"
	if descending {
		order = column + " DESC,record_key DESC"
	}
	version := "version"
	if journal {
		version = `COALESCE((SELECT v.version FROM record_versions v WHERE v.workspace_key=metadata_catalog.workspace_key AND v.field=metadata_catalog.field AND v.record_key=metadata_catalog.record_key AND v.deleted=0),0)`
	}
	return `SELECT record_key,collection_order,` + version + `,summary,omitted,created_at,updated_at FROM metadata_catalog WHERE ` + where + ` ORDER BY ` + order + ` LIMIT 129`, args, nil
}
