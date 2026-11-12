package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"flow/api/internal/domain"
)

// InternalProjectIDUnambiguous preserves MCP's earlier friendly-name match
// when a project is named after another project's canonical ID.
func (s *SQLiteStore) InternalProjectIDUnambiguous(access AccessContext, ref string) bool {
	if access.checkBinding(s) != nil {
		return false
	}
	p := access.policy
	s.mu.RLock()
	defer s.mu.RUnlock()
	source, ok := s.workspaces[p.workspace]
	stamp, certified := s.copyRevisionLocked(p.workspace)
	if !ok || !certified || stamp.workspaceID != p.stamp.workspaceID || stamp.revision != p.stamp.revision {
		return false
	}
	for _, project := range source.Projects {
		if !p.projectVisible(project) || !p.projectAllowed(project) || p.selected() && !slices.ContainsFunc(project.TeamIDs, p.teamAllowed) {
			continue
		}
		if strings.EqualFold(ref, project.ID) || strings.EqualFold(ref, project.Name) || strings.EqualFold(ref, project.SlugID) {
			return ref == project.ID
		}
	}
	return false
}

// InternalProjectFilterMetadata supplies identities for friendly filters,
// without cloning initiative descriptions or project bodies.
func (s *SQLiteStore) InternalProjectFilterMetadata(ctx context.Context, access AccessContext) (domain.Bootstrap, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.Bootstrap{}, err
	}
	p := access.policy
	s.mu.RLock()
	source, ok := s.workspaces[p.workspace]
	stamp, certified := s.copyRevisionLocked(p.workspace)
	if !ok || !certified || stamp.workspaceID != p.stamp.workspaceID || stamp.revision != p.stamp.revision {
		s.mu.RUnlock()
		return domain.Bootstrap{}, ErrResourceAccessChanged
	}
	data := cloneBootstrap(domain.Bootstrap{Viewer: source.Viewer, Users: source.Users})
	data.Viewer = p.metadata.Viewer
	for _, team := range source.Teams {
		if p.teamAllowed(team.ID) {
			data.Teams = append(data.Teams, domain.Team{ID: team.ID, Name: team.Name, Key: team.Key})
		}
	}
	for _, item := range source.Initiatives {
		if p.initiativeVisible(source, item) && (!p.selected() && (p.key == nil || p.key.Actor != "app") || p.initiativeReadable(source, item)) {
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: item.ID, Name: item.Name, SlugID: item.SlugID})
		}
	}
	s.mu.RUnlock()
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.Bootstrap{}, err
	}
	return data, nil
}

// InternalProjectPage retains the MCP collection order and offset cursor.
// Only the selected details are copied. match sees the same redacted filter
// fields as the HTTP detail; hidden references cannot select a record.
// Uncertified copies use canonical identity pages. Legacy unsplit roots and
// concurrent changes require the complete compatibility adapter.
func (s *SQLiteStore) InternalProjectPage(ctx context.Context, access AccessContext, start, limit int, match func(domain.Project) bool) ([]domain.Project, string, error) {
	if err := access.checkBinding(s); err != nil {
		return nil, "", err
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return nil, "", err
	}
	p := access.policy
	s.mu.RLock()
	source, ok := s.workspaces[p.workspace]
	stamp, certified := s.copyRevisionLocked(p.workspace)
	if !ok || !certified || stamp.workspaceID != p.stamp.workspaceID || stamp.revision != p.stamp.revision {
		s.mu.RUnlock()
		return s.internalCanonicalProjectPage(ctx, access, start, limit, match)
	}
	items := []domain.Project{}
	if source.Projects == nil {
		items = nil
	}
	next, seen := "", 0
	limit = min(max(limit, 1), 250)
	if start >= 0 {
		for index, project := range source.Projects {
			if index%64 == 0 && ctx.Err() != nil {
				s.mu.RUnlock()
				return nil, "", ctx.Err()
			}
			if !p.projectVisible(project) || !p.projectAllowed(project) || p.selected() && !slices.ContainsFunc(project.TeamIDs, p.teamAllowed) {
				continue
			}
			if match != nil {
				filter := domain.Project{ID: project.ID, Name: project.Name, Summary: project.Summary, Description: project.Description, Status: project.Status, TeamIDs: project.TeamIDs, MemberIDs: project.MemberIDs, Initiatives: project.Initiatives}
				filter.TeamIDs = slices.DeleteFunc(slices.Clone(filter.TeamIDs), func(id string) bool { return !p.teams.Content[id] || p.selected() && !p.teamAllowed(id) })
				if p.selected() || p.key != nil && p.key.Actor == "app" {
					filter.Initiatives = slices.DeleteFunc(slices.Clone(filter.Initiatives), func(id string) bool {
						return !slices.ContainsFunc(source.Initiatives, func(item domain.Initiative) bool { return item.ID == id && p.initiativeReadable(source, item) })
					})
				}
				if !match(filter) {
					continue
				}
			}
			seen++
			if seen <= start {
				continue
			}
			if len(items) == limit {
				next = strconv.Itoa(start + limit)
				break
			}
			projected, _, err := resourceReadMetadata(p, source, "projects", project.ID)
			if err != nil {
				s.mu.RUnlock()
				return nil, "", err
			}
			items = append(items, projected.Projects[0])
		}
	}
	s.mu.RUnlock()
	if err := s.ValidateAccess(ctx, access); err != nil {
		return nil, "", err
	}
	return items, next, nil
}

func (s *SQLiteStore) internalCanonicalProjectPage(ctx context.Context, access AccessContext, start, limit int, match func(domain.Project) bool) ([]domain.Project, string, error) {
	items := []domain.Project{}
	p := access.policy
	var shape sql.NullString
	var present int
	err := s.db.QueryRowContext(ctx, `SELECT `+s.jsonText("data", metadataCollectionsKey+".projects")+`,CASE WHEN `+s.jsonText("data", "projects")+` IS NULL THEN 0 ELSE 1 END FROM workspace_states WHERE workspace_key=?`, p.workspace).Scan(&shape, &present)
	if err != nil {
		return nil, "", err
	}
	if shape.String != "array" {
		if present == 0 {
			return nil, "", nil
		}
		// Legacy unsplit roots need the complete compatibility adapter.
		return nil, "", ErrResourceAccessChanged
	}
	if start < 0 {
		return items, "", nil
	}
	limit, seen := min(max(limit, 1), 250), 0
	var ordinal int64
	last, next := "", ""
	for {
		where, args := `workspace_key=? AND field='projects'`, []any{p.workspace}
		if last != "" {
			where += ` AND (collection_order>? OR (collection_order=? AND record_key>?))`
			args = append(args, ordinal, ordinal, last)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT record_key,collection_order,`+s.jsonText("data", "teamIds")+` FROM workspace_metadata_records WHERE `+where+` ORDER BY collection_order,record_key LIMIT 64`, args...)
		if err != nil {
			return nil, "", err
		}
		type identity struct {
			project domain.Project
			order   int64
		}
		page := []identity{}
		for rows.Next() {
			var item identity
			var teams sql.NullString
			if err := rows.Scan(&item.project.ID, &item.order, &teams); err != nil {
				rows.Close()
				return nil, "", err
			}
			if teams.Valid {
				if err := json.Unmarshal([]byte(teams.String), &item.project.TeamIDs); err != nil {
					rows.Close()
					return nil, "", err
				}
			}
			page = append(page, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, "", err
		}
		for _, candidate := range page {
			project := candidate.project
			last, ordinal = project.ID, candidate.order
			if !p.projectVisible(project) || !p.projectAllowed(project) || p.selected() && !slices.ContainsFunc(project.TeamIDs, p.teamAllowed) {
				continue
			}
			var detail domain.Project
			if match != nil {
				detail, err = s.AuthorizedProjectRecord(ctx, access, project.ID)
				if err != nil {
					return nil, "", err
				}
				if !match(detail) {
					continue
				}
			}
			seen++
			if seen <= start {
				continue
			}
			if len(items) == limit {
				next = strconv.Itoa(start + limit)
				break
			}
			if match == nil {
				detail, err = s.AuthorizedProjectRecord(ctx, access, project.ID)
				if err != nil {
					return nil, "", err
				}
			}
			items = append(items, detail)
		}
		if next != "" || len(page) < 64 {
			break
		}
	}
	stamp, err := readMetadataStamp(ctx, s.db, p.workspace)
	if err != nil {
		return nil, "", err
	}
	if stamp != p.stamp {
		return nil, "", ErrResourceAccessChanged
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return nil, "", err
	}
	return items, next, nil
}
