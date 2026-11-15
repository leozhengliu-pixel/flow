package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// When an installed copy has no certified revision, retain policy and the
// target's reference closure. Settings are scanned in fixed SQL chunks;
// unrelated guidance, resource bodies and trash payloads are not retained.
func (s *SQLiteStore) resourcePolicyRecords(ctx context.Context, workspace, workspaceID string) (domain.Bootstrap, error) {
	if data, ok := s.ownedResourcePolicy(ctx, workspace, workspaceID); ok {
		return data, nil
	}
	if s.metadataResidency != nil {
		return s.readResidentResourcePolicy(ctx, workspace, workspaceID)
	}
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); !ok || snapshot.database != s.db {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return domain.Bootstrap{}, err
		}
		defer tx.Rollback()
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: tx})
	} else if snapshot.reader == nil {
		return domain.Bootstrap{}, ErrMetadataRecordQuery
	}
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: workspaceID, URLKey: workspace}, TeamSettings: map[string]domain.TeamSettings{}}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='teams' ORDER BY collection_order,record_key`, workspace)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var raw []byte
		var team domain.Team
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return data, err
		}
		if err := json.Unmarshal(raw, &team); err != nil {
			rows.Close()
			return data, err
		}
		data.Teams = append(data.Teams, team)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	err = s.readLegacyTeamPolicySettings(ctx, workspace, data.TeamSettings)
	return resourcePolicyMetadata(data), err
}

func (s *SQLiteStore) resourceReadRecords(ctx context.Context, p *resourceAccessPolicy, kind, ref string) (domain.Bootstrap, error) {
	if read, ok := ctx.Value(resourceOwnedReadKey{}).(*ResourceRead); ok && read.store == s {
		return read.records(ctx, p, kind, ref)
	}
	data := domain.Bootstrap{}
	if kind != "projects" && kind != "initiatives" {
		return data, sql.ErrNoRows
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, kind, strings.TrimSpace(ref)).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return data, err
	}
	add := func(raw []byte) error {
		candidate := domain.Bootstrap{}
		if kind == "projects" {
			var item domain.Project
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			candidate.Projects = []domain.Project{item}
		} else {
			var item domain.Initiative
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			candidate.Initiatives = []domain.Initiative{item}
		}
		if err := s.resourceReadCompatibility(ctx, p, &candidate); err != nil {
			return err
		}
		data.Projects = append(data.Projects, candidate.Projects...)
		data.Initiatives = append(data.Initiatives, candidate.Initiatives...)
		return nil
	}
	if err == nil {
		if err := add(raw); err != nil {
			return data, err
		}
		if err := s.resourceReferenceRecords(ctx, p, &data); err != nil {
			return data, err
		}
		// An exact, visible record that survives credential filtering wins.
		// Otherwise aliases must be resolved after the corresponding filters.
		if kind == "projects" && data.Projects[0].ID == strings.TrimSpace(ref) && p.projectVisible(data.Projects[0]) && (!p.selected() || slices.ContainsFunc(data.Projects[0].TeamIDs, p.teamAllowed)) {
			return data, nil
		}
		if kind == "initiatives" && data.Initiatives[0].ID == strings.TrimSpace(ref) && p.initiativeVisible(data, data.Initiatives[0]) && (!p.selected() || slices.ContainsFunc(data.Initiatives[0].ProjectIDs, func(id string) bool { return p.projectByID(data, id, true) })) {
			return data, nil
		}
	}
	// Slug lookup reads identities only; P05's catalog can replace this scan
	// without changing matching precedence or the ownership of the result.
	rows, err := s.db.QueryContext(ctx, "SELECT record_key,"+s.jsonText("data", "id")+","+s.jsonText("data", "slugId")+","+s.jsonText("data", "previousSlugIds")+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? ORDER BY collection_order,record_key`, p.workspace, kind)
	if err != nil {
		return data, err
	}
	ids := []string{}
	for rows.Next() {
		var key string
		var id, slug, previous sql.NullString
		if err := rows.Scan(&key, &id, &slug, &previous); err != nil {
			rows.Close()
			return data, err
		}
		var old []string
		if previous.Valid {
			if err := json.Unmarshal([]byte(previous.String), &old); err != nil {
				rows.Close()
				return data, err
			}
		}
		if domain.MatchSlugRef(ref, id.String, slug.String, old) != domain.SlugNoMatch {
			ids = append(ids, key)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	data = domain.Bootstrap{}
	for _, id := range ids {
		if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, kind, id).Scan(&raw); err != nil {
			return data, err
		}
		if err := add(raw); err != nil {
			return data, err
		}
	}
	err = s.resourceReferenceRecords(ctx, p, &data)
	return data, err
}

func (s *SQLiteStore) resourceReadCompatibility(ctx context.Context, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	if read, ok := ctx.Value(resourceOwnedReadKey{}).(*ResourceRead); ok && read.store == s {
		return read.compatibility(ctx, p, data)
	}
	var retry func() (bool, error)
	if snapshot, pinned := ctx.Value(workspaceReadKey{}).(workspaceRead); pinned && snapshot.database == s.db {
		if captured, ok := ctx.Value(resourceReadCompatibilityKey{}).(resourceReadCompatibilitySnapshot); ok && captured.database == s.db &&
			captured.workspace == p.workspace && captured.data.Workspace.ID == p.stamp.workspaceID {
			retry = func() (bool, error) { return captured.matches(ctx, *data) }
			matches, err := retry()
			if err != nil {
				return err
			}
			if matches {
				return nil
			}
			data.Viewer = cloneBootstrap(domain.Bootstrap{Viewer: captured.data.Viewer}).Viewer
		} else if snapshot.workspace == p.workspace && snapshot.metadata.Workspace.ID == p.stamp.workspaceID {
			_, scoped := metadataRecordScopeFromContext(ctx)
			if !scoped {
				retry = func() (bool, error) { return resourceReadMatchesInstalled(snapshot.metadata, *data), nil }
				if matches, _ := retry(); matches {
					return nil
				}
			}
			data.Viewer = cloneBootstrap(domain.Bootstrap{Viewer: snapshot.metadata.Viewer}).Viewer
		} else {
			// A low-level transaction can have no owned metadata. The scalar
			// root carries the installed/default viewer, not this principal.
			var raw []byte
			if err := s.db.QueryRowContext(ctx, `SELECT `+s.jsonText("data", "viewer")+` FROM workspace_states WHERE workspace_key=? AND workspace_id=?`, p.workspace, p.stamp.workspaceID).Scan(&raw); err != nil {
				return err
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &data.Viewer); err != nil {
					return err
				}
			}
		}
	} else {
		retry = func() (bool, error) {
			s.mu.RLock()
			defer s.mu.RUnlock()
			return resourceReadMatchesInstalled(s.workspaces[p.workspace], *data), nil
		}
		s.mu.RLock()
		source := s.workspaces[p.workspace]
		matches := resourceReadMatchesInstalled(source, *data)
		if !matches {
			data.Viewer = cloneBootstrap(domain.Bootstrap{Viewer: source.Viewer}).Viewer
		}
		s.mu.RUnlock()
		if matches {
			return nil
		}
	}
	// Installed legacy metadata receives load-time defaults and refreshed
	// display references. Reuse that normalizer with only the target and its
	// indexed actor/status rows, preserving freshly written records verbatim.
	ids := []string{}
	for _, item := range data.Projects {
		if item.Lead != nil {
			ids = append(ids, item.Lead.ID)
		}
		if item.Creator != nil {
			ids = append(ids, item.Creator.ID)
		}
	}
	for _, item := range data.Initiatives {
		ids = append(ids, item.Creator.ID)
		if item.Owner != nil {
			ids = append(ids, item.Owner.ID)
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var raw []byte
		err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='users' AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, id).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var user domain.User
		if err := json.Unmarshal(raw, &user); err != nil {
			return err
		}
		data.Users = append(data.Users, user)
	}
	// Display references can advance without rewriting a canonical body.
	// Retry the witness before applying unrelated load-time defaults.
	refreshDisplayReferences(data)
	if retry != nil {
		matches, err := retry()
		if err != nil || matches {
			return err
		}
	}
	for _, item := range data.Projects {
		var raw []byte
		var ordinal int64
		err := s.db.QueryRowContext(ctx, `SELECT chosen.data,(SELECT COUNT(*) FROM workspace_metadata_records prior_status
			WHERE prior_status.workspace_key=chosen.workspace_key AND prior_status.field=chosen.field
			AND (prior_status.collection_order<chosen.collection_order OR
				(prior_status.collection_order=chosen.collection_order AND prior_status.record_key<chosen.record_key)))
			FROM workspace_metadata_records chosen WHERE chosen.workspace_key=? AND chosen.field='projectStatuses' AND `+
			s.metadataRecordKeyEquals("chosen.record_key", "?"), p.workspace, item.Status.ID).Scan(&raw, &ordinal)
		if errors.Is(err, sql.ErrNoRows) {
			// A missing target status is different from an empty catalog:
			// only the latter receives the legacy canonical defaults.
			err = s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projectStatuses' ORDER BY collection_order,record_key LIMIT 1`, p.workspace).Scan(&raw)
			ordinal = 0
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var status domain.ProjectStatus
			if err := json.Unmarshal(raw, &status); err != nil {
				return err
			}
			if status.Position == 0 && ordinal > 0 {
				status.Position = float64(ordinal)
			}
			data.ProjectStatuses = append(data.ProjectStatuses, status)
		}
	}
	normalizeStoredMetadata(data)
	return nil
}

func resourceReadMatchesInstalled(source, data domain.Bootstrap) bool {
	for _, item := range data.Projects {
		for _, cached := range source.Projects {
			if item.ID == cached.ID && resourceWireEqual(item, cached) {
				return true
			}
		}
	}
	for _, item := range data.Initiatives {
		for _, cached := range source.Initiatives {
			if item.ID == cached.ID && resourceWireEqual(item, cached) {
				return true
			}
		}
	}
	return false
}

func resourceWireEqual(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func (s *SQLiteStore) resourceReferenceRecords(ctx context.Context, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	projects, initiatives := map[string]bool{}, map[string]bool{}
	projectIDs, parentIDs := []string{}, []string{}
	labelIDs := []string{}
	for _, item := range data.Projects {
		projects[item.ID] = true
		projectIDs = append(projectIDs, item.DependencyIDs...)
		labelIDs = append(labelIDs, item.LabelIDs...)
		if p.selected() || p.key != nil && p.key.Actor == "app" {
			parentIDs = append(parentIDs, item.Initiatives...)
		}
	}
	for _, item := range data.Initiatives {
		initiatives[item.ID] = true
		projectIDs = append(projectIDs, item.ProjectIDs...)
		labelIDs = append(labelIDs, item.LabelIDs...)
		parentIDs = append(parentIDs, item.ParentInitiativeIDs...)
		rows, err := s.db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='initiativeRelations' AND `+s.jsonText("data", "initiativeId")+`=? ORDER BY collection_order,record_key`, p.workspace, item.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw []byte
			var relation domain.InitiativeRelation
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			if err := json.Unmarshal(raw, &relation); err != nil {
				rows.Close()
				return err
			}
			data.InitiativeRelations = append(data.InitiativeRelations, relation)
			if relation.Type == "parent" {
				parentIDs = append(parentIDs, relation.RelatedInitiativeID)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for _, id := range parentIDs {
		if initiatives[id] {
			continue
		}
		initiatives[id] = true
		var lead, contributing, linked sql.NullString
		err := s.db.QueryRowContext(ctx, "SELECT "+s.jsonText("data", "leadTeamId")+","+s.jsonText("data", "contributingTeamIds")+","+s.jsonText("data", "projectIds")+` FROM workspace_metadata_records WHERE workspace_key=? AND field='initiatives' AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, id).Scan(&lead, &contributing, &linked)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		item := domain.Initiative{ID: id, LeadTeamID: lead.String}
		if contributing.Valid {
			if err := json.Unmarshal([]byte(contributing.String), &item.ContributingTeamIDs); err != nil {
				return err
			}
		}
		if linked.Valid {
			if err := json.Unmarshal([]byte(linked.String), &item.ProjectIDs); err != nil {
				return err
			}
		}
		projectIDs = append(projectIDs, item.ProjectIDs...)
		data.Initiatives = append(data.Initiatives, item)
	}
	if p.selected() {
		for _, id := range labelIDs {
			var scope sql.NullString
			err := s.db.QueryRowContext(ctx, "SELECT "+s.jsonText("data", "scope")+` FROM workspace_metadata_records WHERE workspace_key=? AND field='labels' AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, id).Scan(&scope)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			data.Labels = append(data.Labels, domain.IssueLabel{ID: id, Scope: scope.String})
		}
	}
	for _, id := range projectIDs {
		if projects[id] {
			continue
		}
		projects[id] = true
		var teams sql.NullString
		err := s.db.QueryRowContext(ctx, "SELECT "+s.jsonText("data", "teamIds")+` FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, id).Scan(&teams)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		item := domain.Project{ID: id}
		if teams.Valid {
			if err := json.Unmarshal([]byte(teams.String), &item.TeamIDs); err != nil {
				return err
			}
		}
		data.Projects = append(data.Projects, item)
	}
	return nil
}
