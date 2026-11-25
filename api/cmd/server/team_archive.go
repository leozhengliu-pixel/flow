package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Deleted teams stay restorable for this long before they are purged.
const teamRestoreWindow = 30 * 24 * time.Hour

type deletedTeam struct {
	domain.Team
	ArchivedBy *domain.User `json:"archivedBy,omitempty"`
	PurgeAt    time.Time    `json:"purgeAt"`
	IssueCount int          `json:"issueCount"`
}

// deleteTeam moves a team into its restoration window. The team and its issues
// disappear for everyone; purgeExpiredTeams removes them for good later.
func (s *server) deleteTeam(w http.ResponseWriter, r *http.Request) {
	workspaceKey, teamID := r.PathValue("workspaceKey"), r.PathValue("teamId")
	viewerID := authUser(r).ID
	err := s.store.MutateWorkspace(r.Context(), workspaceKey, "team.archived", teamID, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
		if index < 0 || data.Teams[index].ArchivedAt != nil {
			return errNotFound
		}
		remaining := slices.ContainsFunc(data.Teams, func(team domain.Team) bool {
			return team.ID != teamID && team.ArchivedAt == nil && team.RetiredAt == nil
		})
		if !remaining {
			return store.ErrLastWorkspaceTeam
		}
		now := time.Now().UTC()
		if viewerID == "" {
			viewerID = data.Viewer.ID
		}
		data.Teams[index].ArchivedAt, data.Teams[index].ArchivedByID, data.Teams[index].UpdatedAt = &now, viewerID, &now
		// Sub-teams are detached, matching what a permanent delete does.
		for id, settings := range data.TeamSettings {
			if settings.ParentTeamID == teamID {
				settings.ParentTeamID = ""
				settings.InheritIssueEstimation, settings.InheritWorkflowStatuses = false, false
				settings.InheritProjectStatuses, settings.InheritCycles = false, false
				data.TeamSettings[id] = settings
			}
		}
		domain.RebuildTeamDirectory(data)
		appendAudit(data, "deleted", "team", teamID, map[string]any{"name": data.Teams[index].Name, "restorableUntil": now.Add(teamRestoreWindow)})
		return nil
	})
	if errors.Is(err, store.ErrLastWorkspaceTeam) {
		writeError(w, http.StatusConflict, "A workspace needs at least one active team")
		return
	}
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) listDeletedTeams(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.WorkspaceMetadata(r.PathValue("workspaceKey"))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	teams := []deletedTeam{}
	for _, team := range data.Teams {
		if team.ArchivedAt == nil {
			continue
		}
		item := deletedTeam{Team: team, PurgeAt: team.ArchivedAt.Add(teamRestoreWindow)}
		// Access is nil: this internal count must see the hidden team's issues.
		if page, err := s.store.QueryIssueRecords(r.Context(), store.IssueRecordQuery{Workspace: data.Workspace.URLKey, TeamIDs: []string{team.ID}, Archived: "all", Limit: 1, IncludeTotal: true}); err == nil {
			item.IssueCount = int(page.Total)
		}
		if user := userByID(&data, team.ArchivedByID); user != nil {
			item.ArchivedBy = user
		}
		teams = append(teams, item)
	}
	slices.SortFunc(teams, func(a, b deletedTeam) int { return b.ArchivedAt.Compare(*a.ArchivedAt) })
	writeJSON(w, http.StatusOK, teams)
}

func (s *server) restoreDeletedTeam(w http.ResponseWriter, r *http.Request) {
	workspaceKey, teamID := r.PathValue("workspaceKey"), r.PathValue("teamId")
	var restored domain.Team
	scope := store.MetadataRecordScope{
		Read:  []store.RecordKey{{Kind: "providerJobs", ID: teamID}, {Kind: "users", ID: teamID}},
		Write: []store.RecordKey{{Kind: "teams", ID: teamID}},
	}
	if teamID == "" || len(teamID) > 191 || strings.ContainsRune(teamID, '\x00') {
		scope = store.MetadataRecordScope{}
	} else {
		switch {
		case strings.HasPrefix(teamID, "team_resource_section_"):
			scope.Read = []store.RecordKey{{Kind: "teamResourceSections", ID: teamID}}
		case strings.HasPrefix(teamID, "team_resource_"):
			scope.Read = []store.RecordKey{{Kind: "teamPinnedResources", ID: teamID}}
		case strings.HasPrefix(teamID, "team_"):
			scope.Read = nil
		case strings.HasPrefix(teamID, "state_"):
			scope.Read = append(scope.Read, store.RecordKey{Kind: "states", ID: teamID})
		case strings.HasPrefix(teamID, "label_"):
			scope.Read = append(scope.Read, store.RecordKey{Kind: "labels", ID: teamID})
		case strings.HasPrefix(teamID, "project_"):
			scope.Read = append(scope.Read, store.RecordKey{Kind: "projects", ID: teamID})
		case strings.HasPrefix(teamID, "cycle_"):
			scope.Read = append(scope.Read, store.RecordKey{Kind: "cycles", ID: teamID})
		}
	}
	err := s.store.MutateWorkspaceResolved(r.Context(), workspaceKey, "team.unarchived", teamID, nil, store.MetadataResolvedScope{
		MetadataRecordScope: scope,
		ResolveRead:         teamMetadataProjectDisplayReads,
	}, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
		if index < 0 || data.Teams[index].ArchivedAt == nil {
			return errNotFound
		}
		now := time.Now().UTC()
		data.Teams[index].ArchivedAt, data.Teams[index].ArchivedByID, data.Teams[index].UpdatedAt = nil, "", &now
		domain.RebuildTeamDirectory(data)
		restored = data.Teams[index]
		appendAudit(data, "restored", "team", teamID, map[string]any{"name": restored.Name})
		return nil
	})
	respondMutation(w, err, http.StatusOK, restored)
}

// purgeExpiredTeams permanently deletes teams whose restoration window ended.
func (s *server) purgeExpiredTeams(ctx context.Context, workspaceKey string, now time.Time) error {
	if s.store.BoundedMetadataEnabled() {
		read, found, err := s.store.BeginExpiredTeamPolicyRead(ctx, workspaceKey, now, teamRestoreWindow)
		if err != nil || !found {
			return err
		}
		defer read.Close()
		return purgeExpiredTeamIDs(read.IDs(), func(id string) error {
			return s.purgeTeam(read.Context(), workspaceKey, id)
		})
	}
	data, ok := s.store.WorkspaceMetadataFields(workspaceKey, "teams")
	if !ok {
		return nil
	}
	for _, team := range data.Teams {
		if team.ArchivedAt == nil || now.Before(team.ArchivedAt.Add(teamRestoreWindow)) {
			continue
		}
		if err := s.purgeTeam(ctx, workspaceKey, team.ID); err != nil && !errors.Is(err, errNotFound) {
			return err
		}
	}
	return nil
}

func purgeExpiredTeamIDs(ids []string, purge func(string) error) error {
	for _, id := range ids {
		if err := purge(id); err != nil && !errors.Is(err, errNotFound) {
			return err
		}
	}
	return nil
}
