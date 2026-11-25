package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// RecordReadCore deliberately has no empty stand-ins for unloaded collections.
// A client must retain its legacy initialization until it supports the omitted
// surfaces and the server advertises the complete sync capability.
type RecordReadCore struct {
	Workspace  RecordReadWorkspace `json:"workspace"`
	Viewer     domain.User         `json:"viewer"`
	ViewerRole string              `json:"viewerRole"`
	Omitted    []string            `json:"omitted"`
}

type RecordReadWorkspace struct {
	ID     string `json:"id"`
	URLKey string `json:"urlKey"`
}

func (s *SQLiteStore) ReadRecordCore(ctx context.Context, access AccessContext) (RecordReadCore, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return RecordReadCore{}, err
	}
	p := access.policy
	viewer := cloneSnapshotValue(reflect.ValueOf(p.metadata.Viewer)).Interface().(domain.User)
	if p.selected() {
		RedactUserTeams(&viewer, p.teamAllowed)
	}
	return RecordReadCore{
		Workspace: RecordReadWorkspace{ID: p.metadata.Workspace.ID, URLKey: p.metadata.Workspace.URLKey}, Viewer: viewer, ViewerRole: p.metadata.ViewerRole,
		Omitted: []string{"teams", "favorites", "people", "settings"},
	}, nil
}

func recordReadKind(kind string) bool {
	return kind == "projects" || kind == "initiatives"
}

// ReadRecordStatus combines writer coverage with the shared cursor-key
// prerequisite. It is safe to expose as a capability, never as a row count.
func (s *SQLiteStore) ReadRecordStatus(ctx context.Context, access AccessContext, kind string) (MetadataCatalogStatus, error) {
	if err := access.checkBinding(s); err != nil {
		return MetadataCatalogStatus{}, err
	}
	if !recordReadKind(kind) {
		return MetadataCatalogStatus{}, ErrMetadataRecordQuery
	}
	status, err := s.MetadataCatalogStatus(ctx, access.policy.workspace, kind)
	if status.Ready {
		if _, keyErr := connectorCipher(); keyErr != nil {
			status.Ready = false
		}
	}
	return status, err
}

func (s *SQLiteStore) recordReadStatus(ctx context.Context, access AccessContext, kind string) (MetadataCatalogStatus, error) {
	status, err := s.ReadRecordStatus(ctx, access, kind)
	if err == nil && !status.Ready {
		err = ErrMetadataCatalogGap
	}
	return status, err
}

// ReadRecordPage serves only a catalog certified by its transactional writer.
// A completed P05 shadow pass alone is never sufficient coverage.
func (s *SQLiteStore) ReadRecordPage(ctx context.Context, access AccessContext, q PageRequest) (RecordPage, error) {
	if s.metadataResidency != nil {
		admitted, release, err := s.admitMetadataWork(ctx, 2<<20)
		if err != nil {
			return RecordPage{}, err
		}
		defer release()
		ctx = admitted
	}
	status, err := s.recordReadStatus(ctx, access, q.Kind)
	if err != nil {
		return RecordPage{}, err
	}
	page, err := s.readAccessRecordPage(ctx, access, q)
	if err == nil {
		after, statusErr := s.recordReadStatus(ctx, access, q.Kind)
		if statusErr != nil {
			err = statusErr
		} else if after.Revision != status.Revision || page.Revision != status.Revision {
			err = ErrMetadataCursor
		}
	}
	if err != nil {
		return RecordPage{}, err
	}
	return page, nil
}

func recordPageBinding(access AccessContext, q PageRequest) []byte {
	raw, _ := json.Marshal([]string{"flow-sync-page", "2", access.WorkspaceID, access.PrincipalID, access.CredentialScope, access.AuthzEpoch, q.Kind, q.Filter, q.Order})
	return raw
}

func openRecordCursor(access AccessContext, q PageRequest) (string, error) {
	if q.Cursor == "" {
		// Both replicas must have the shared key before issuing any pages.
		_, err := connectorCipher()
		return "", err
	}
	if len(q.Cursor) > 8192 {
		return "", ErrMetadataCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	if err != nil {
		return "", ErrMetadataCursor
	}
	plain, _, err := openSecret(raw, recordPageBinding(access, q))
	if err != nil {
		return "", ErrMetadataCursor
	}
	return string(plain), nil
}

func sealRecordCursor(access AccessContext, q PageRequest, cursor string) (string, error) {
	raw, err := sealSecret([]byte(cursor), recordPageBinding(access, q))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// This adapter is also the shadow comparison seam. It reads summaries before
// authorizing references; it never hydrates a candidate's canonical body.
func (s *SQLiteStore) readAccessRecordPage(ctx context.Context, access AccessContext, q PageRequest) (RecordPage, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return RecordPage{}, err
	}
	if !recordReadKind(q.Kind) || len(q.Filter) > 64<<10 || !slices.Contains([]string{"", "collection", "createdAt", "updatedAt"}, q.Order) {
		return RecordPage{}, ErrMetadataRecordQuery
	}
	_, budget, err := metadataPageLimits(q)
	if err != nil {
		return RecordPage{}, err
	}
	cursor, err := openRecordCursor(access, q)
	if err != nil {
		return RecordPage{}, err
	}
	query := q
	query.Cursor = cursor
	var page RecordPage
	var filter ProjectRecordQuery
	if q.Kind == "projects" {
		if len(q.Filter) > 64<<10 || q.Filter != "" && json.Unmarshal([]byte(q.Filter), &filter) != nil {
			return RecordPage{}, ErrMetadataRecordQuery
		}
		// The principal, credentials and policy always come from AccessContext.
		filter.Workspace, filter.Admin, filter.Cursor = access.policy.workspace, false, ""
		filter.AllowedTeamIDs = []string{}
		for id := range access.policy.teams.Content {
			if access.policy.teamAllowed(id) {
				filter.AllowedTeamIDs = append(filter.AllowedTeamIDs, id)
			}
		}
		slices.Sort(filter.AllowedTeamIDs)
		if slices.ContainsFunc(filter.Filters, func(f ProjectDirectoryFilter) bool { return f.Field == "customers" }) {
			p := access.policy
			if p.key != nil && p.key.Actor == "app" && !resourceKeyHasScope(*p.key, "customer:read") && !resourceKeyHasScope(*p.key, "customer:write") {
				return RecordPage{}, ErrMetadataRecordQuery
			}
			settings, ok := s.WorkspaceSettingsMetadata(filter.Workspace)
			if !ok || access.policy.metadata.ViewerRole == "guest" {
				return RecordPage{}, ErrMetadataRecordQuery
			}
			if enabled, present := settings.WorkspaceSettings.FeatureFlags["customer-requests"]; present && !enabled {
				return RecordPage{}, ErrMetadataRecordQuery
			}
			_, issueAccess, accessErr := s.IssueQueryAccess(ctx, p.workspace, p.principal)
			if accessErr != nil {
				return RecordPage{}, accessErr
			}
			var allowed []string
			if p.selected() {
				allowed = append([]string{}, p.key.TeamIDs...)
			}
			filter.CustomerSummaries, err = s.ScopedProjectCustomerSummaries(ctx, IssueRecordQuery{Workspace: p.workspace, Access: &issueAccess, AllowedTeamIDs: allowed})
			if err != nil {
				return RecordPage{}, err
			}
		}
		catalogFilter := filter
		catalogFilter.Filters, catalogFilter.TeamIDs, catalogFilter.Search = nil, nil, ""
		page, err = s.ReadMetadataCatalogProjectPage(ctx, catalogFilter, query)
	} else if q.Filter != "" {
		return RecordPage{}, ErrMetadataRecordQuery
	} else {
		page, err = s.ReadMetadataCatalogPage(ctx, access.policy.workspace, query)
	}
	if err != nil {
		return RecordPage{}, err
	}
	items := make([]MetadataRecord, 0, len(page.Items))
	remaining := budget - metadataPageOverhead
	for _, item := range page.Items {
		if item.Omitted {
			// An omitted policy field cannot be interpreted as an empty grant.
			return RecordPage{}, ErrMetadataCatalogGap
		}
		redacted, visible, err := s.redactReadRecord(ctx, access, item, true)
		if err != nil {
			return RecordPage{}, err
		}
		if !visible {
			continue
		}
		if q.Kind == "projects" {
			var project domain.Project
			if err := json.Unmarshal(redacted.Data, &project); err != nil {
				return RecordPage{}, err
			}
			filter.Cursor, filter.Limit, filter.IncludeTotal = "", 1, false
			if len(filterProjectDirectory([]domain.Project{project}, filter).Items) == 0 {
				continue
			}
		}
		raw, err := json.Marshal(redacted)
		if err != nil {
			return RecordPage{}, err
		}
		if len(raw)+1 > remaining {
			redacted.Data, redacted.Omitted, redacted.Detail = nil, true, &redacted.Key
			raw, _ = json.Marshal(redacted)
		}
		remaining -= len(raw) + 1
		if remaining < 0 {
			return RecordPage{}, ErrMetadataPageBudget
		}
		items = append(items, redacted)
	}
	page.Items, page.AuthzEpoch = items, access.AuthzEpoch
	if p := access.policy; p.selected() && !slices.ContainsFunc(p.key.TeamIDs, p.teamAllowed) {
		page.Items, page.Next, page.HasMore = []MetadataRecord{}, "", false
	}
	if page.HasMore {
		page.Next, err = sealRecordCursor(access, q, page.Next)
		if err != nil {
			return RecordPage{}, err
		}
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return RecordPage{}, err
	}
	return page, nil
}

// ReadRecords authorizes bounded summaries before fetching any requested body.
// Hidden and absent keys have the same missing shape, with no version/actor.
func (s *SQLiteStore) ReadRecords(ctx context.Context, access AccessContext, keys []RecordKey, projection string) (RecordBatch, error) {
	if s.metadataResidency != nil {
		admitted, release, err := s.admitMetadataWork(ctx, 2<<20)
		if err != nil {
			return RecordBatch{}, err
		}
		defer release()
		ctx = admitted
	}
	if len(keys) > MetadataRecordBatchMaxKeys || projection != "summary" && projection != "detail" {
		return RecordBatch{}, ErrMetadataRecordQuery
	}
	statuses := map[string]MetadataCatalogStatus{}
	for _, key := range keys {
		if !validMetadataKey(key) || key.ID == "" {
			return RecordBatch{}, ErrMetadataRecordQuery
		}
		if _, ok := statuses[key.Kind]; !ok {
			status, err := s.recordReadStatus(ctx, access, key.Kind)
			if err != nil {
				return RecordBatch{}, err
			}
			statuses[key.Kind] = status
		}
	}
	batch, err := s.readAccessRecords(ctx, access, keys, projection)
	if err != nil {
		return RecordBatch{}, err
	}
	for kind, status := range statuses {
		after, err := s.recordReadStatus(ctx, access, kind)
		if err != nil {
			return RecordBatch{}, err
		}
		if after.Revision != status.Revision {
			return RecordBatch{}, ErrMetadataCursor
		}
	}
	return batch, nil
}

func (s *SQLiteStore) readAccessRecords(ctx context.Context, access AccessContext, keys []RecordKey, projection string) (RecordBatch, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return RecordBatch{}, err
	}
	batch := RecordBatch{Items: []MetadataRecord{}, AuthzEpoch: access.AuthzEpoch}
	allowed := []RecordKey{}
	positions := []int{}
	for _, key := range keys {
		item := MetadataRecord{Key: key}
		var omitted int
		err := s.db.QueryRowContext(ctx, `SELECT version,summary,omitted FROM metadata_catalog WHERE workspace_key=? AND field=? AND record_key=?`, access.policy.workspace, key.Kind, key.ID).Scan(&item.Version, &item.Data, &omitted)
		if errors.Is(err, sql.ErrNoRows) {
			item = MetadataRecord{Key: key, Missing: true}
		} else if err != nil {
			return RecordBatch{}, err
		} else if omitted != 0 {
			return RecordBatch{}, ErrMetadataCatalogGap
		} else {
			redacted, visible, err := s.redactReadRecord(ctx, access, item, true)
			if err != nil {
				return RecordBatch{}, err
			}
			if !visible {
				item = MetadataRecord{Key: key, Missing: true}
			} else {
				item = redacted
				positions, allowed = append(positions, len(batch.Items)), append(allowed, key)
			}
		}
		batch.Items = append(batch.Items, item)
	}
	if projection == "detail" && len(allowed) > 0 {
		canonical, err := s.ReadCanonicalRecords(ctx, access.policy.workspace, allowed, "detail")
		if err != nil {
			return RecordBatch{}, err
		}
		for i, item := range canonical.Items {
			position := positions[i]
			if item.Missing {
				return RecordBatch{}, ErrMetadataCatalogGap
			}
			item.Version = batch.Items[position].Version
			redacted, visible, err := s.redactReadRecord(ctx, access, item, item.Omitted)
			if err != nil {
				return RecordBatch{}, err
			}
			if !visible {
				return RecordBatch{}, ErrAuthForbidden
			}
			batch.Items[position] = redacted
		}
	}
	remaining := MetadataRecordPageMaxBytes - metadataPageOverhead
	for i := range batch.Items {
		item := &batch.Items[i]
		raw, err := json.Marshal(item)
		if err != nil {
			return RecordBatch{}, err
		}
		if len(raw)+1 > remaining {
			item.Data, item.Omitted, item.Detail = nil, true, &item.Key
			raw, _ = json.Marshal(item)
		}
		remaining -= len(raw) + 1
		if remaining < 0 {
			return RecordBatch{}, ErrMetadataPageBudget
		}
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return RecordBatch{}, err
	}
	return batch, nil
}

func (s *SQLiteStore) redactReadRecord(ctx context.Context, access AccessContext, item MetadataRecord, summary bool) (MetadataRecord, bool, error) {
	p := access.policy
	data := domain.Bootstrap{}
	switch item.Key.Kind {
	case "projects":
		var project domain.Project
		if err := json.Unmarshal(item.Data, &project); err != nil {
			return item, false, err
		}
		if !p.projectAllowed(project) || p.selected() && !slices.ContainsFunc(project.TeamIDs, p.teamAllowed) {
			return item, false, nil
		}
		if summary {
			project = ProjectListProjection(project)
		}
		data.Projects = []domain.Project{project}
	case "initiatives":
		var initiative domain.Initiative
		if err := json.Unmarshal(item.Data, &initiative); err != nil {
			return item, false, err
		}
		if summary {
			initiative.Description, initiative.DescriptionHistory, initiative.Resources, initiative.Comments = "", nil, nil, nil
		}
		data.Initiatives = []domain.Initiative{initiative}
	default:
		return item, false, ErrMetadataRecordQuery
	}
	if err := s.resourceReferenceRecords(ctx, p, &data); err != nil {
		return item, false, err
	}
	projected, allowed, err := resourceReadMetadata(p, data, item.Key.Kind, item.Key.ID)
	if err != nil || !allowed {
		return item, false, err
	}
	if item.Key.Kind == "projects" {
		if len(projected.Projects) == 0 {
			return item, false, nil
		}
		item.Data, err = json.Marshal(projected.Projects[0])
	} else {
		if len(projected.Initiatives) == 0 {
			return item, false, nil
		}
		initiative := &projected.Initiatives[0]
		var favorite int
		err = s.db.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS (
			SELECT 1 FROM workspace_metadata_records WHERE workspace_key=? AND field='favorites' AND `+s.jsonText("data", "userId")+`=? AND `+s.jsonText("data", "resourceType")+`='initiative' AND `+s.jsonText("data", "resourceId")+`=?
		) OR EXISTS (
			SELECT 1 FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=?
			WHERE d.workspace_key=? AND d.resource_type='initiative' AND d.resource_id=? AND NOT EXISTS (
			SELECT 1 FROM team_default_favorite_overrides o WHERE o.workspace_key=d.workspace_key AND o.user_id=tm.user_id AND o.resource_type=d.resource_type AND o.resource_id=d.resource_id AND o.hidden=1)
		) THEN 1 ELSE 0 END`, p.workspace, access.PrincipalID, initiative.ID, access.WorkspaceID, access.PrincipalID, p.workspace, initiative.ID).Scan(&favorite)
		if err == nil {
			initiative.Favorite = favorite == 1
			item.Data, err = json.Marshal(initiative)
		}
	}
	if summary {
		item.Omitted, item.Detail = true, &item.Key
	}
	return item, true, err
}

func indexedReadsMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("FLOW_INDEXED_READS"))) {
	case "on", "1", "true":
		return "on"
	case "shadow":
		return "shadow"
	default:
		return "off"
	}
}
