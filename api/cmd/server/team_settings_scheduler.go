package main

import (
	"context"
	"errors"
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func (s *server) maintainTeamSettings(ctx context.Context, key string, now time.Time) error {
	metadata, ok := s.store.WorkspaceMetadataFields(key, "teams", "teamSettings")
	if !ok {
		return nil
	}
	// Teams sharing a mode and age limit are checked together, five per
	// indexed query (the page limit keeps up to 100 candidates per team), so
	// an idle sweep costs a fifth of the per-team queries.
	type sweep struct {
		mode   string
		months int
	}
	groups := map[sweep][]string{}
	order := []sweep{}
	for _, team := range metadata.Teams {
		settings := metadata.TeamSettings[team.ID]
		for _, mode := range []string{"stale", "archive"} {
			months := settings.AutoArchiveMonths
			if mode == "stale" {
				if !settings.AutoCloseStale {
					continue
				}
				months = settings.StaleMonths
			}
			if months <= 0 {
				continue
			}
			key := sweep{mode, months}
			if _, known := groups[key]; !known {
				order = append(order, key)
			}
			groups[key] = append(groups[key], team.ID)
		}
	}
	for _, group := range order {
		mode, teamIDs := group.mode, groups[group]
		status := store.IssueFilter{Field: "statusType", Values: []string{"completed", "canceled"}}
		if mode == "stale" {
			status.Operator = "isNot"
		}
		cutoff := now.AddDate(0, -group.months, 0)
		for start := 0; start < len(teamIDs); start += 5 {
			batch := teamIDs[start:min(start+5, len(teamIDs))]
			query := store.IssueRecordQuery{Workspace: key, TeamIDs: batch, Limit: 100 * len(batch), Sort: "updatedAt", Direction: "asc", Filter: store.IssueFilter{And: []store.IssueFilter{status, {Field: "updatedAt", Operator: "before", Values: []string{cutoff.Format(time.RFC3339)}}}}}
			page, err := s.store.QueryIssueRecords(ctx, query)
			if err != nil {
				return err
			}
			perTeam := map[string]int{}
			for _, candidate := range page.Items {
				team := domain.Team{ID: candidate.Team.ID}
				if perTeam[team.ID]++; perTeam[team.ID] > 100 || !slices.Contains(batch, team.ID) {
					continue
				}
				settings := metadata.TeamSettings[team.ID]
				_, err := s.store.UpdateIssueRecord(ctx, key, candidate.ID, &candidate.Version, store.IssueMutationScope{IncludeFamily: mode == "stale", Payload: map[string]string{"automation": mode}}, func(data *domain.Bootstrap, issue *domain.Issue) error {
					if issue.ArchivedAt != nil || !issue.UpdatedAt.Before(cutoff) {
						return store.ErrNoMutation
					}
					if mode == "archive" {
						if issue.State.Type != "completed" && issue.State.Type != "canceled" {
							return store.ErrNoMutation
						}
						issue.ArchivedAt = &now
						issue.UpdatedAt = now
						appendActivity(data, issue.ID, "issue.archived", data.Viewer, map[string]string{"automation": "autoArchive"})
						return nil
					}
					if issue.State.Type == "completed" || issue.State.Type == "canceled" {
						return store.ErrNoMutation
					}
					stateID := settings.StaleStatusID
					if stateID == "" {
						for _, state := range statesForTeam(data, team.ID) {
							if state.Type == "canceled" {
								stateID = state.ID
								break
							}
						}
					}
					state := stateForTeam(data, team.ID, stateID)
					if state == nil || (state.Type != "completed" && state.Type != "canceled") {
						return store.ErrNoMutation
					}
					changes, err := applyUpdate(data, issue, domain.IssueUpdateInput{StateID: &stateID})
					if err != nil {
						return store.ErrNoMutation
					}
					issue.AutoClosed = true
					issue.UpdatedAt = now
					issue.StatusChangedAt = &now
					if state.Type == "completed" {
						issue.CompletedAt = &now
					} else {
						issue.CanceledAt = &now
					}
					changes["automation"] = "autoCloseStale"
					activity := appendActivity(data, issue.ID, "issue.updated", data.Viewer, changes)
					appendIssueNotifications(data, *issue, activity, nil)
					return nil
				})
				if err != nil && !errors.Is(err, store.ErrNoMutation) && !errors.Is(err, store.ErrIssueVersion) {
					return err
				}
			}
		}
	}
	eligible := func(project domain.Project) bool {
		if project.ArchivedAt != nil || (project.Status.Type != "completed" && project.Status.Type != "canceled") || len(project.TeamIDs) == 0 {
			return false
		}
		months := 0
		for _, id := range project.TeamIDs {
			value := metadata.TeamSettings[id].AutoArchiveMonths
			if value <= 0 {
				return false
			}
			if value > months {
				months = value
			}
		}
		return project.UpdatedAt.Before(now.AddDate(0, -months, 0))
	}
	needsUpdate := false
	for _, project := range metadata.Projects {
		if eligible(project) {
			needsUpdate = true
			break
		}
	}
	if !needsUpdate {
		return nil
	}
	return s.store.MutateWorkspace(ctx, key, "settings.project_archive", "workspace", nil, func(data *domain.Bootstrap) error {
		for i := range data.Projects {
			if eligible(data.Projects[i]) {
				data.Projects[i].ArchivedAt = &now
				data.Projects[i].UpdatedAt = now
			}
		}
		return nil
	})
}
