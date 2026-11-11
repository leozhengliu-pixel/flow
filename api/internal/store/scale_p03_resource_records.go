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

// When an installed copy has no certified revision, read only policy columns
// and the target's reference closure. In particular, team guidance, unrelated
// resource bodies and trash restore payloads never enter this fallback.
func (s *SQLiteStore) resourcePolicyRecords(ctx context.Context, workspace, workspaceID string) (domain.Bootstrap, error) {
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
	columns := []string{"parentTeamId", "access", "membershipRestriction", "settingsPermission", "labelPermission", "memberPermission"}
	query := "SELECT record_key"
	for _, column := range columns {
		query += "," + s.jsonText("data", column)
	}
	rows, err = s.db.QueryContext(ctx, query+` FROM workspace_metadata_records WHERE workspace_key=? AND field='teamSettings'`, workspace)
	if err != nil {
		return data, err
	}
	for rows.Next() {
		var id string
		var parent, access, restriction, settings, labels, members sql.NullString
		if err := rows.Scan(&id, &parent, &access, &restriction, &settings, &labels, &members); err != nil {
			rows.Close()
			return data, err
		}
		data.TeamSettings[id] = domain.TeamSettings{ParentTeamID: parent.String, Access: access.String, MembershipRestriction: restriction.String, SettingsPermission: settings.String, LabelPermission: labels.String, MemberPermission: members.String}
	}
	err = rows.Err()
	rows.Close()
	return resourcePolicyMetadata(data), err
}

func (s *SQLiteStore) resourceReadRecords(ctx context.Context, p *resourceAccessPolicy, kind, ref string) (domain.Bootstrap, error) {
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
	s.mu.RLock()
	source := s.workspaces[p.workspace]
	for _, item := range data.Projects {
		for _, cached := range source.Projects {
			if item.ID == cached.ID && resourceWireEqual(item, cached) {
				s.mu.RUnlock()
				return nil
			}
		}
	}
	for _, item := range data.Initiatives {
		for _, cached := range source.Initiatives {
			if item.ID == cached.ID && resourceWireEqual(item, cached) {
				s.mu.RUnlock()
				return nil
			}
		}
	}
	data.Viewer = cloneBootstrap(domain.Bootstrap{Viewer: source.Viewer}).Viewer
	s.mu.RUnlock()
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
		var raw []byte
		err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projectStatuses' AND `+s.metadataRecordKeyEquals("record_key", "?"), p.workspace, item.Status.ID).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			var status domain.ProjectStatus
			if err := json.Unmarshal(raw, &status); err != nil {
				return err
			}
			data.ProjectStatuses = append(data.ProjectStatuses, status)
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
	normalizeStoredMetadata(data)
	return nil
}

func resourceWireEqual(a, b any) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func (s *SQLiteStore) resourceReferenceRecords(ctx context.Context, p *resourceAccessPolicy, data *domain.Bootstrap) error {
	projects, initiatives := map[string]bool{}, map[string]bool{}
	projectIDs, parentIDs := []string{}, []string{}
	for _, item := range data.Projects {
		projects[item.ID] = true
		projectIDs = append(projectIDs, item.DependencyIDs...)
	}
	for _, item := range data.Initiatives {
		initiatives[item.ID] = true
		projectIDs = append(projectIDs, item.ProjectIDs...)
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
