package store

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"flow/api/internal/domain"
)

type ScaleP19Snapshot struct {
	Data     domain.Bootstrap
	Revision string
	Versions map[string]map[string]string
}

// ReadScaleP19Snapshot pairs the installed summary inputs and committed record
// versions at one SQL revision. An uncertified copy takes the existing reload
// recovery once; it cannot acquire newer versions for older row values.
func (s *SQLiteStore) ReadScaleP19Snapshot(ctx context.Context, workspace, principal string, unfiltered bool) (ScaleP19Snapshot, error) {
	for range 3 {
		tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return ScaleP19Snapshot{}, err
		}
		stamp, err := readMetadataStamp(ctx, tx, workspace)
		if err != nil {
			_ = tx.Rollback()
			return ScaleP19Snapshot{}, err
		}
		data, err := s.scaleP19MetadataAt(ctx, workspace, principal, unfiltered, tx, stamp)
		if errors.Is(err, ErrResourceAccessChanged) {
			_ = tx.Rollback()
			if err := s.ReloadWorkspace(ctx, workspace); err != nil {
				return ScaleP19Snapshot{}, err
			}
			continue
		}
		if err != nil {
			_ = tx.Rollback()
			return ScaleP19Snapshot{}, err
		}
		result := ScaleP19Snapshot{Data: data, Revision: strconv.FormatInt(stamp.revision, 10), Versions: map[string]map[string]string{"projects": {}, "initiatives": {}}}
		if s.db.journalReady.Load() {
			if err := checkRecordJournalCoverage(ctx, tx, workspace); err != nil {
				_ = tx.Rollback()
				return ScaleP19Snapshot{}, err
			}
			rows, err := tx.QueryContext(ctx, `SELECT field,record_key,version FROM record_versions WHERE workspace_key=? AND deleted=0 AND field IN ('projects','initiatives')`, workspace)
			if err != nil {
				_ = tx.Rollback()
				return ScaleP19Snapshot{}, err
			}
			for rows.Next() {
				var kind, id string
				var version int64
				if err = rows.Scan(&kind, &id, &version); err != nil {
					break
				}
				result.Versions[kind][id] = strconv.FormatInt(version, 10)
			}
			if err == nil {
				err = rows.Err()
			}
			_ = rows.Close()
			if err != nil {
				_ = tx.Rollback()
				return ScaleP19Snapshot{}, err
			}
		}
		_ = tx.Rollback()
		return result, nil
	}
	return ScaleP19Snapshot{}, ErrResourceAccessChanged
}

// ScaleP19Metadata preserves the established viewer projection while copying
// list inputs before bodies. Global ordering and hierarchy aggregates still
// scan summaries; they never copy unrelated descriptions or update histories.
func (s *SQLiteStore) ScaleP19Metadata(ctx context.Context, workspace, principal string, unfiltered bool) (domain.Bootstrap, error) {
	snapshot, err := s.ReadScaleP19Snapshot(ctx, workspace, principal, unfiltered)
	return snapshot.Data, err
}

func (s *SQLiteStore) scaleP19MetadataAt(ctx context.Context, workspace, principal string, unfiltered bool, tx *sqlTx, stamp copyStamp) (domain.Bootstrap, error) {
	s.mu.RLock()
	source, ok := s.workspaces[workspace]
	known, exact := s.copyRevisionLocked(workspace)
	if !ok || !exact || known.workspaceID != stamp.workspaceID || known.revision != stamp.revision {
		s.mu.RUnlock()
		return domain.Bootstrap{}, ErrResourceAccessChanged
	}
	projects := make([]domain.Project, 0, len(source.Projects))
	for _, project := range source.Projects {
		projects = append(projects, ProjectListProjection(project))
	}
	source.Projects = projects
	source.Initiatives = slices.Clone(source.Initiatives)
	for i := range source.Initiatives {
		item := &source.Initiatives[i]
		item.Description = ""
		item.DescriptionHistory = nil
		item.Resources = nil
		item.Comments = nil
		item.CommentAttachments = nil
	}
	projectUpdates := make(map[string][]domain.ProjectUpdate, len(source.ProjectUpdates))
	for id, updates := range source.ProjectUpdates {
		projectUpdates[id] = []domain.ProjectUpdate{}
		if len(updates) > 0 {
			u := updates[0]
			projectUpdates[id] = []domain.ProjectUpdate{{ID: u.ID, ProjectID: u.ProjectID, CreatedAt: u.CreatedAt, Health: u.Health}}
		}
	}
	source.ProjectUpdates = projectUpdates
	initiativeUpdates := make(map[string][]domain.InitiativeUpdate, len(source.InitiativeUpdates))
	for id, updates := range source.InitiativeUpdates {
		initiativeUpdates[id] = []domain.InitiativeUpdate{}
		if len(updates) > 0 {
			u := updates[0]
			initiativeUpdates[id] = []domain.InitiativeUpdate{{ID: u.ID, InitiativeID: u.InitiativeID, CreatedAt: u.CreatedAt, Health: u.Health}}
		}
	}
	source.InitiativeUpdates = initiativeUpdates
	source.Documents = slices.Clone(source.Documents)
	for i := range source.Documents {
		item := &source.Documents[i]
		item.Content = ""
		item.ContentState = ""
		item.ContentData = nil
		item.Revisions = nil
	}
	data := metadataFieldSnapshot(source, []string{"teams", "teamSettings", "cycleSettings", "workspaceSettings", "userSettings", "settings", "labels", "labelGroups", "projectStatuses", "projectTemplates", "projectRelations", "initiativeRelations", "projects", "initiatives", "projectUpdates", "initiativeUpdates", "savedViews", "favorites", "favoriteFolders", "subscriptions", "agentSkills", "customers", "customerStatuses", "customerTiers", "customerRequests", "cycles", "documents", "releases", "releasePipelines", "reviews"})
	directory := cloneBootstrap(domain.Bootstrap{Users: source.Users, Members: source.Members, TeamMembers: source.TeamMembers, Invitations: source.Invitations})
	data.Users, data.Members, data.TeamMembers, data.Invitations = directory.Users, directory.Members, directory.TeamMembers, directory.Invitations
	s.mu.RUnlock()
	normalizeStoredMetadata(&data)
	if unfiltered {
		data.ViewerRole = "admin"
		return data, nil
	}
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{s.db, workspace, tx, data})
	return s.PagedWorkspaceMetadata(ctx, workspace, principal)
}
