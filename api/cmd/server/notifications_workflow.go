package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var teamIdentifierPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,4}$`)

var notificationCategories = []string{
	"assignments", "statusChanges", "comments", "mentions", "reactions", "subscriptions",
	"documents", "updates", "reminders", "loops", "integrations", "customerRequests", "triage",
}

func defaultPreferences(userID string) domain.NotificationPreferences {
	categoryMap := func() map[string]bool {
		result := make(map[string]bool, len(notificationCategories))
		for _, category := range notificationCategories {
			result[category] = true
		}
		return result
	}
	return domain.NotificationPreferences{
		UserID:      userID,
		Inbox:       domain.NotificationChannelPreferences{Enabled: true, Categories: categoryMap()},
		Email:       domain.NotificationChannelPreferences{Enabled: true, Categories: categoryMap()},
		Desktop:     domain.NotificationChannelPreferences{Enabled: true, Categories: categoryMap()},
		EmailFormat: "digest", DelayLowPriority: true, ImmediateUrgent: true, SoundEnabled: true, UpdatedAt: time.Now().UTC(),
	}
}

func (s *server) getNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	preferences, ok := data.NotificationPreferences[data.Viewer.ID]
	if !ok {
		preferences = defaultPreferences(data.Viewer.ID)
	}
	writeJSON(w, http.StatusOK, preferences)
}

func (s *server) updateNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	var input domain.NotificationPreferences
	if !decodeJSON(w, r, &input) {
		return
	}
	viewerID := authUser(r).ID
	if s.authDisabled {
		viewerID = s.workspaceData(r).Viewer.ID
	}
	var updated domain.NotificationPreferences
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "notification_preferences.updated", viewerID, input, func(data *domain.Bootstrap) error {
		current, ok := data.NotificationPreferences[viewerID]
		if !ok {
			current = defaultPreferences(viewerID)
		}
		mergeNotificationPreferences(&current, input)
		if !slices.Contains([]string{"immediate", "digest"}, current.EmailFormat) {
			return errInvalid
		}
		current.UserID = viewerID
		current.UpdatedAt = time.Now().UTC()
		if data.NotificationPreferences == nil {
			data.NotificationPreferences = map[string]domain.NotificationPreferences{}
		}
		data.NotificationPreferences[viewerID] = current
		updated = current
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func mergeNotificationPreferences(current *domain.NotificationPreferences, input domain.NotificationPreferences) {
	current.Inbox = mergeChannelPreferences(current.Inbox, input.Inbox)
	current.Email = mergeChannelPreferences(current.Email, input.Email)
	current.Desktop = mergeChannelPreferences(current.Desktop, input.Desktop)
	if input.EmailFormat != "" {
		current.EmailFormat = input.EmailFormat
	}
	current.DelayLowPriority = input.DelayLowPriority
	current.ImmediateUrgent = input.ImmediateUrgent
	current.SoundEnabled = input.SoundEnabled
	if input.DesktopPermission != "" {
		current.DesktopPermission = input.DesktopPermission
	}
}

func mergeChannelPreferences(current, input domain.NotificationChannelPreferences) domain.NotificationChannelPreferences {
	current.Enabled = input.Enabled
	if current.Categories == nil {
		current.Categories = map[string]bool{}
	}
	for key, value := range input.Categories {
		if slices.Contains(notificationCategories, key) {
			current.Categories[key] = value
		}
	}
	return current
}

func (s *server) batchNotifications(w http.ResponseWriter, r *http.Request) {
	var input domain.NotificationBatchInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !slices.Contains([]string{"delete", "deleteAll", "deleteRead", "deleteReadCompleted", "markRead", "markAllRead", "markUnread", "archive", "archiveAll", "unarchive", "snooze", "snoozeAll", "unsnooze"}, input.Action) {
		writeError(w, http.StatusBadRequest, "invalid notification batch action")
		return
	}
	selectedAction := slices.Contains([]string{"delete", "markRead", "markUnread", "archive", "unarchive", "snooze", "unsnooze"}, input.Action)
	if selectedAction && len(input.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "notification ids are required")
		return
	}
	var snoozedUntil *time.Time
	if input.Action == "snooze" || input.Action == "snoozeAll" {
		if input.SnoozedUntil == nil {
			writeError(w, http.StatusBadRequest, "snoozedUntil is required")
			return
		}
		parsed, err := time.Parse(time.RFC3339, *input.SnoozedUntil)
		if err != nil || !parsed.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "snoozedUntil must be a future RFC3339 timestamp")
			return
		}
		snoozedUntil = &parsed
	}
	viewerID := s.workspaceData(r).Viewer.ID
	var updated int
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "notifications.batch_updated", viewerID, input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		ids := make(map[string]struct{}, len(input.IDs))
		for _, id := range input.IDs {
			ids[id] = struct{}{}
		}
		completed := map[string]bool{}
		for _, issue := range data.Issues {
			completed[issue.ID] = issue.State.Type == "completed" || issue.State.Type == "canceled"
		}
		for index := range data.Notifications {
			notification := &data.Notifications[index]
			if notification.RecipientID != viewerID {
				continue
			}
			_, selected := ids[notification.ID]
			if len(ids) > 0 && !selected {
				continue
			}
			switch input.Action {
			case "delete":
				if selected {
					notification.DeletedAt = &now
					updated++
				}
			case "deleteAll":
				notification.DeletedAt = &now
				updated++
			case "deleteRead":
				if notification.ReadAt != nil {
					notification.DeletedAt = &now
					updated++
				}
			case "deleteReadCompleted":
				if notification.ReadAt != nil && completed[notification.IssueID] {
					notification.DeletedAt = &now
					updated++
				}
			case "markRead":
				notification.ReadAt = &now
				updated++
			case "markAllRead":
				notification.ReadAt = &now
				updated++
			case "markUnread":
				notification.ReadAt = nil
				updated++
			case "archive", "archiveAll":
				notification.ArchivedAt = &now
				updated++
			case "unarchive":
				notification.ArchivedAt = nil
				updated++
			case "snooze", "snoozeAll":
				notification.SnoozedUntil = snoozedUntil
				updated++
			case "unsnooze":
				notification.SnoozedUntil = nil
				updated++
			}
			notification.UpdatedAt = now
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, map[string]int{"updated": updated})
}

func (s *server) listNotificationDeliveries(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := []domain.NotificationDelivery{}
	for _, delivery := range data.NotificationDeliveries {
		if delivery.RecipientID == data.Viewer.ID {
			result = append(result, delivery)
		}
	}
	slices.SortFunc(result, func(a, b domain.NotificationDelivery) int { return b.CreatedAt.Compare(a.CreatedAt) })
	writeJSON(w, http.StatusOK, result)
}

func (s *server) acknowledgeDesktopNotifications(w http.ResponseWriter, r *http.Request) {
	var input struct {
		NotificationIDs []string `json:"notificationIds"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	viewerID := s.workspaceData(r).Viewer.ID
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "notification.desktop_acknowledged", viewerID, input, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		for index := range data.NotificationDeliveries {
			delivery := &data.NotificationDeliveries[index]
			if delivery.RecipientID == viewerID && delivery.Channel == "desktop" && slices.Contains(input.NotificationIDs, delivery.NotificationID) {
				delivery.Status, delivery.AcknowledgedAt, delivery.DeliveredAt, delivery.UpdatedAt = "delivered", &now, &now, now
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusNoContent, nil)
}

func (s *server) retryNotificationDelivery(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	viewerID := s.workspaceData(r).Viewer.ID
	var updated domain.NotificationDelivery
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "notification.delivery_retried", id, nil, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.ID == id && item.RecipientID == viewerID })
		if index < 0 {
			return errNotFound
		}
		delivery := &data.NotificationDeliveries[index]
		if delivery.Status != "failed" && delivery.Status != "pending-disabled" && delivery.Status != "pending" {
			return fmt.Errorf("%w: delivery cannot be retried in its current state", errConflict)
		}
		delivery.Status, delivery.Error, delivery.NextAttemptAt, delivery.UpdatedAt = "pending", "", nil, time.Now().UTC()
		updated = *delivery
		return nil
	})
	if err == nil && updated.Channel == "email" {
		s.dispatchNotificationEmails(r.Context(), workspaceKey(r))
	}
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) dispatchNotificationEmails(ctx context.Context, key string) {
	data, ok := s.store.BootstrapFor(key)
	if !ok {
		return
	}
	for _, delivery := range data.NotificationDeliveries {
		if delivery.Channel != "email" || delivery.Status != "pending" {
			continue
		}
		claimed := false
		claimErr := s.store.MutateWorkspace(ctx, key, "notification.delivery_claimed", delivery.ID, nil, func(next *domain.Bootstrap) error {
			index := slices.IndexFunc(next.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.ID == delivery.ID })
			if index < 0 || next.NotificationDeliveries[index].Status != "pending" {
				return nil
			}
			next.NotificationDeliveries[index].Status = "delivering"
			next.NotificationDeliveries[index].UpdatedAt = time.Now().UTC()
			delivery, claimed = next.NotificationDeliveries[index], true
			return nil
		})
		if claimErr != nil || !claimed {
			continue
		}
		status, message := "delivered", ""
		if s.mailer == nil {
			status = "pending-disabled"
		} else {
			notificationIndex := slices.IndexFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == delivery.NotificationID })
			recipient := userByID(&data, delivery.RecipientID)
			issue, issueErr := issueByID(&data, func() string {
				if notificationIndex >= 0 {
					return data.Notifications[notificationIndex].IssueID
				}
				return ""
			}())
			if notificationIndex < 0 || recipient == nil || issueErr != nil {
				status, message = "failed", "notification source is unavailable"
			} else {
				notification := data.Notifications[notificationIndex]
				body := notification.Actor.DisplayName + " updated " + issue.Identifier
				if notification.Type == "assignment" {
					body = notification.Actor.DisplayName + " assigned the issue to you"
				}
				if notification.Type == "mention" {
					body = notification.Actor.DisplayName + " mentioned you"
				}
				if notification.Type == "comment" {
					body = notification.Actor.DisplayName + " commented on the issue"
				}
				if err := s.mailer.sendNotification(recipient.Email, key, issue.Identifier, issue.Title, body); err != nil {
					status, message = "failed", err.Error()
				}
			}
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.store.MutateWorkspace(persistCtx, key, "notification.delivery_updated", delivery.ID, map[string]string{"status": status}, func(next *domain.Bootstrap) error {
			index := slices.IndexFunc(next.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.ID == delivery.ID })
			if index < 0 || next.NotificationDeliveries[index].Status != "delivering" {
				return nil
			}
			now := time.Now().UTC()
			item := &next.NotificationDeliveries[index]
			item.Attempts++
			item.Status, item.Error, item.UpdatedAt = status, message, now
			if status == "delivered" {
				item.DeliveredAt = &now
			} else if status == "failed" {
				retry := now.Add(time.Duration(1<<min(item.Attempts, 6)) * time.Minute)
				item.NextAttemptAt = &retry
			}
			return nil
		})
		cancel()
	}
}

func (s *server) listWorkflowStates(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	states := statesForTeam(&data, teamID)
	writeJSON(w, http.StatusOK, states)
}

func (s *server) createWorkflowState(w http.ResponseWriter, r *http.Request) {
	var input domain.WorkflowStateMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" || input.Type == nil {
		writeError(w, http.StatusBadRequest, "name and type are required")
		return
	}
	teamID := r.PathValue("id")
	var created domain.WorkflowState
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "workflow_state.created", input, func(data *domain.Bootstrap) (string, error) {
		if !teamExists(data, teamID) || !validWorkflowType(*input.Type) {
			return "", errInvalid
		}
		materializeTeamStates(data, teamID)
		states := statesForTeam(data, teamID)
		position := float64(len(states))
		created = domain.WorkflowState{ID: fmt.Sprintf("state_%d", time.Now().UnixNano()), TeamID: teamID, Name: strings.TrimSpace(*input.Name), Type: *input.Type, Color: "#6B6F76", Position: position}
		if input.Description != nil {
			created.Description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil {
			created.Color = *input.Color
		}
		if input.Position != nil {
			created.Position = *input.Position
		}
		if input.Default != nil {
			created.Default = *input.Default
		}
		if created.Default {
			clearDefaultState(data, teamID)
		}
		data.States = append(data.States, created)
		normalizeWorkflowStatePositions(data, teamID)
		if normalized := stateForTeam(data, teamID, created.ID); normalized != nil {
			created = *normalized
		}
		if created.Default {
			settings := teamSettings(data, teamID)
			settings.DefaultStateID = created.ID
			data.TeamSettings[teamID] = settings
		}
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateWorkflowState(w http.ResponseWriter, r *http.Request) {
	var input domain.WorkflowStateMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, stateID := r.PathValue("id"), r.PathValue("stateId")
	var updated domain.WorkflowState
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workflow_state.updated", stateID, input, func(data *domain.Bootstrap) error {
		materializeTeamStates(data, teamID)
		state := stateForTeam(data, teamID, stateID)
		if state == nil {
			state = stateForTeam(data, teamID, teamID+"_"+stateID)
		}
		if state == nil {
			return errNotFound
		}
		if state.Reserved && (input.Name != nil || input.Type != nil || input.Default != nil) {
			return fmt.Errorf("%w: reserved status cannot be changed", errInvalid)
		}
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" {
				return errInvalid
			}
			state.Name = name
		}
		if input.Description != nil {
			state.Description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil {
			state.Color = *input.Color
		}
		if input.Type != nil {
			if !validWorkflowType(*input.Type) {
				return errInvalid
			}
			if countStatesOfType(data, teamID, state.Type) <= 1 {
				return fmt.Errorf("%w: each workflow type needs at least one status", errInvalid)
			}
			state.Type = *input.Type
		}
		if input.Position != nil {
			state.Position = *input.Position
		}
		if input.Default != nil && *input.Default {
			clearDefaultState(data, teamID)
			state.Default = true
			settings := teamSettings(data, teamID)
			settings.DefaultStateID = state.ID
			data.TeamSettings[teamID] = settings
		}
		updated = *state
		for index := range data.Issues {
			if data.Issues[index].Team.ID == teamID && data.Issues[index].State.ID == updated.ID {
				data.Issues[index].State = updated
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteWorkflowState(w http.ResponseWriter, r *http.Request) {
	teamID, stateID := r.PathValue("id"), r.PathValue("stateId")
	var input domain.WorkflowStateMutationInput
	if r.Body != nil && r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workflow_state.deleted", stateID, input, func(data *domain.Bootstrap) error {
		materializeTeamStates(data, teamID)
		state := stateForTeam(data, teamID, stateID)
		if state == nil {
			state = stateForTeam(data, teamID, teamID+"_"+stateID)
		}
		if state == nil {
			return errNotFound
		}
		resolvedStateID := state.ID
		if state.Reserved {
			return fmt.Errorf("%w: reserved status cannot be deleted", errInvalid)
		}
		if state.Default {
			return fmt.Errorf("%w: default status cannot be deleted", errInvalid)
		}
		if countStatesOfType(data, teamID, state.Type) <= 1 {
			return fmt.Errorf("%w: each workflow type needs at least one status", errInvalid)
		}
		inUse := slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.Team.ID == teamID && issue.State.ID == resolvedStateID })
		var replacement *domain.WorkflowState
		if input.ReplacementStateID != "" {
			replacement = stateForTeam(data, teamID, input.ReplacementStateID)
			if replacement == nil {
				replacement = stateForTeam(data, teamID, teamID+"_"+input.ReplacementStateID)
			}
		}
		if inUse && replacement == nil {
			return fmt.Errorf("%w: replacementStateId is required for a status in use", errInvalid)
		}
		if replacement != nil {
			for index := range data.Issues {
				if data.Issues[index].Team.ID == teamID && data.Issues[index].State.ID == resolvedStateID {
					data.Issues[index].State = *replacement
				}
			}
		}
		data.States = slices.DeleteFunc(data.States, func(item domain.WorkflowState) bool { return item.TeamID == teamID && item.ID == resolvedStateID })
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) reorderWorkflowStates(w http.ResponseWriter, r *http.Request) {
	var input domain.WorkflowStateReorderInput
	if !decodeJSON(w, r, &input) || len(input.StateIDs) == 0 {
		writeError(w, http.StatusBadRequest, "stateIds are required")
		return
	}
	teamID := r.PathValue("id")
	var updated []domain.WorkflowState
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "workflow_states.reordered", teamID, input, func(data *domain.Bootstrap) error {
		materializeTeamStates(data, teamID)
		states := statesForTeam(data, teamID)
		if len(input.StateIDs) != len(states) || !allUniqueStrings(input.StateIDs) {
			return errInvalid
		}
		lastRank := -1
		for position, id := range input.StateIDs {
			state := stateForTeam(data, teamID, id)
			if state == nil {
				return errInvalid
			}
			rank := workflowStateRank(*state)
			if rank < lastRank {
				return errInvalid
			}
			lastRank = rank
			state.Position = float64(position)
		}
		updated = statesForTeam(data, teamID)
		for issueIndex := range data.Issues {
			if data.Issues[issueIndex].Team.ID != teamID {
				continue
			}
			if state := stateForTeam(data, teamID, data.Issues[issueIndex].State.ID); state != nil {
				data.Issues[issueIndex].State = *state
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) getTeamSettings(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	settings := teamSettings(&data, r.PathValue("id"))
	writeJSON(w, http.StatusOK, settings)
}

func (s *server) updateStructuredTeamSettings(w http.ResponseWriter, r *http.Request) {
	var input domain.TeamSettingsMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID := r.PathValue("id")
	var persistedTeamMembers []domain.TeamMember
	if current, ok := s.store.BootstrapFor(workspaceKey(r)); ok {
		persistedTeamMembers, _ = s.store.ListTeamMembers(r.Context(), current.Workspace.ID)
	}
	var updated domain.TeamSettings
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "team.settings_updated", teamID, input, func(data *domain.Bootstrap) error {
		if len(data.TeamMembers) == 0 && len(persistedTeamMembers) > 0 {
			data.TeamMembers = slices.Clone(persistedTeamMembers)
		}
		if !teamExists(data, teamID) {
			return errNotFound
		}
		settings := teamSettings(data, teamID)
		if input.Description != nil {
			settings.Description = strings.TrimSpace(*input.Description)
		}
		if input.Timezone != nil {
			if strings.TrimSpace(*input.Timezone) == "" {
				return errInvalid
			}
			settings.Timezone = strings.TrimSpace(*input.Timezone)
		}
		if input.EstimateType != nil {
			if !slices.Contains([]string{"notUsed", "exponential", "fibonacci", "flow"}, *input.EstimateType) {
				return errInvalid
			}
			settings.EstimateType = *input.EstimateType
		}
		if input.DefaultStateID != nil {
			requestedID := *input.DefaultStateID
			if stateForTeam(data, teamID, requestedID) == nil {
				return errInvalid
			}
			materializeTeamStates(data, teamID)
			state := stateForTeam(data, teamID, requestedID)
			if state == nil {
				state = stateForTeam(data, teamID, teamID+"_"+requestedID)
			}
			if state == nil {
				return errInvalid
			}
			clearDefaultState(data, teamID)
			state.Default = true
			settings.DefaultStateID = state.ID
		}
		if input.DefaultPriority != nil {
			if *input.DefaultPriority < 0 || *input.DefaultPriority > 4 {
				return errInvalid
			}
			settings.DefaultPriority = *input.DefaultPriority
		}
		if input.IssueEmailEnabled != nil {
			settings.IssueEmailEnabled = *input.IssueEmailEnabled
		}
		if input.DetailedHistory != nil {
			settings.DetailedHistory = *input.DetailedHistory
		}
		permissionValues := []string{"allMembers", "teamMembers", "owners"}
		if input.Access != nil {
			if !slices.Contains([]string{"public", "private", "restricted"}, *input.Access) {
				return errInvalid
			}
			settings.Access = *input.Access
			for index := range data.Teams {
				if data.Teams[index].ID == teamID {
					data.Teams[index].Private = strings.EqualFold(*input.Access, "private")
					break
				}
			}
		}
		if input.MembershipRestriction != nil {
			if !slices.Contains([]string{"open", "members", "owners"}, *input.MembershipRestriction) {
				return errInvalid
			}
			settings.MembershipRestriction = *input.MembershipRestriction
		}
		for value, target := range map[*string]*string{
			input.SettingsPermission:   &settings.SettingsPermission,
			input.LabelPermission:      &settings.LabelPermission,
			input.TemplatePermission:   &settings.TemplatePermission,
			input.AgentSkillPermission: &settings.AgentSkillPermission,
			input.LoopPermission:       &settings.LoopPermission,
			input.MemberPermission:     &settings.MemberPermission,
		} {
			if value != nil {
				if !slices.Contains(permissionValues, *value) {
					return errInvalid
				}
				*target = *value
			}
		}
		if input.SlackChannelID != nil {
			settings.SlackChannelID = strings.TrimSpace(*input.SlackChannelID)
		}
		if input.SlackChannelName != nil {
			settings.SlackChannelName = strings.TrimSpace(*input.SlackChannelName)
		}
		if input.SlackNotifications != nil {
			settings.SlackNotifications = maps.Clone(*input.SlackNotifications)
		}
		if input.PRAutomations != nil {
			settings.PRAutomations = maps.Clone(*input.PRAutomations)
		}
		if input.AutoCloseParents != nil {
			settings.AutoCloseParents = *input.AutoCloseParents
		}
		if input.AutoCloseSubIssues != nil {
			settings.AutoCloseSubIssues = *input.AutoCloseSubIssues
		}
		if input.AutoCloseStale != nil {
			settings.AutoCloseStale = *input.AutoCloseStale
		}
		if input.StaleMonths != nil {
			if *input.StaleMonths < 1 || *input.StaleMonths > 24 {
				return errInvalid
			}
			settings.StaleMonths = *input.StaleMonths
		}
		if input.StaleStatusID != nil {
			if *input.StaleStatusID != "" && stateForTeam(data, teamID, *input.StaleStatusID) == nil {
				return errInvalid
			}
			settings.StaleStatusID = *input.StaleStatusID
		}
		if input.AutoArchiveMonths != nil {
			if *input.AutoArchiveMonths < 1 || *input.AutoArchiveMonths > 24 {
				return errInvalid
			}
			settings.AutoArchiveMonths = *input.AutoArchiveMonths
		}
		if input.ProgressOrder != nil {
			if !slices.Contains([]string{"first", "last", "noAction"}, *input.ProgressOrder) {
				return errInvalid
			}
			settings.ProgressOrder = *input.ProgressOrder
		}
		if input.ReleaseAutomations != nil {
			settings.ReleaseAutomations = slices.Clone(*input.ReleaseAutomations)
		}
		if input.TriageEnabled != nil {
			settings.TriageEnabled = *input.TriageEnabled
		}
		if input.TriageRequirePriority != nil {
			settings.TriageRequirePriority = *input.TriageRequirePriority
		}
		if input.TriageAction != nil {
			settings.TriageAction = strings.TrimSpace(*input.TriageAction)
		}
		if input.TriageRules != nil {
			settings.TriageRules = slices.Clone(*input.TriageRules)
		}
		if input.AgentSkills != nil {
			settings.AgentSkills = slices.Clone(*input.AgentSkills)
		}
		if input.ProjectUpdatePrompt != nil {
			settings.ProjectUpdatePrompt = strings.TrimSpace(*input.ProjectUpdatePrompt)
		}
		if input.ResolvedSummaries != nil {
			settings.ResolvedSummaries = *input.ResolvedSummaries
		}
		if input.ShowInitiatives != nil {
			settings.ShowInitiatives = *input.ShowInitiatives
		}
		if input.ParentTeamID != nil {
			if *input.ParentTeamID == teamID || (*input.ParentTeamID != "" && (!teamExists(data, *input.ParentTeamID) || teamParentCreatesCycle(data, teamID, *input.ParentTeamID))) {
				return errInvalid
			}
			settings.ParentTeamID = *input.ParentTeamID
		}
		if input.Identifier != nil {
			identifier := strings.ToUpper(strings.TrimSpace(*input.Identifier))
			if !teamIdentifierPattern.MatchString(identifier) {
				return fmt.Errorf("%w: identifier must be 2-5 uppercase letters or numbers", errInvalid)
			}
			for _, team := range data.Teams {
				if team.ID != teamID && strings.EqualFold(team.Key, identifier) {
					return fmt.Errorf("%w: identifier is already in use", errInvalid)
				}
			}
			for index := range data.Teams {
				if data.Teams[index].ID == teamID {
					data.Teams[index].Key = identifier
				}
			}
		}
		previousAccess := ""
		if previous, exists := data.TeamSettings[teamID]; exists {
			previousAccess = strings.ToLower(strings.TrimSpace(previous.Access))
		}
		data.TeamSettings[teamID] = settings
		if strings.EqualFold(settings.Access, "private") && previousAccess != "private" {
			memberIDs := map[string]bool{}
			for _, member := range data.TeamMembers {
				if member.TeamID == teamID {
					memberIDs[member.UserID] = true
				}
			}
			for index := range data.Issues {
				if data.Issues[index].Team.ID != teamID {
					continue
				}
				if data.Issues[index].Assignee != nil && !memberIDs[data.Issues[index].Assignee.ID] {
					data.Issues[index].Assignee = nil
				}
				data.Issues[index].SubscriberIDs = slices.DeleteFunc(data.Issues[index].SubscriberIDs, func(id string) bool { return !memberIDs[id] })
			}
		}
		updated = settings
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func teamParentCreatesCycle(data *domain.Bootstrap, teamID, parentID string) bool {
	seen := map[string]bool{teamID: true}
	for parentID != "" {
		if seen[parentID] {
			return true
		}
		seen[parentID] = true
		parentID = data.TeamSettings[parentID].ParentTeamID
	}
	return false
}

func (s *server) listIssueTemplates(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	result := []domain.IssueTemplate{}
	for _, template := range data.IssueTemplates {
		if template.TeamID == teamID {
			result = append(result, template)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createIssueTemplate(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueTemplateMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	teamID := r.PathValue("id")
	var created domain.IssueTemplate
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "issue_template.created", input, func(data *domain.Bootstrap) (string, error) {
		if !teamExists(data, teamID) {
			return "", errNotFound
		}
		now := time.Now().UTC()
		created = domain.IssueTemplate{ID: fmt.Sprintf("template_%d", now.UnixNano()), TeamID: teamID, LabelIDs: []string{}, Creator: data.Viewer, CreatedAt: now, UpdatedAt: now}
		if err := applyIssueTemplate(data, &created, input); err != nil {
			return "", err
		}
		data.IssueTemplates = append(data.IssueTemplates, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateIssueTemplate(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueTemplateMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, templateID := r.PathValue("id"), r.PathValue("templateId")
	var updated domain.IssueTemplate
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue_template.updated", templateID, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == templateID && item.TeamID == teamID })
		if index < 0 {
			return errNotFound
		}
		if err := applyIssueTemplate(data, &data.IssueTemplates[index], input); err != nil {
			return err
		}
		data.IssueTemplates[index].UpdatedAt = time.Now().UTC()
		updated = data.IssueTemplates[index]
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteIssueTemplate(w http.ResponseWriter, r *http.Request) {
	teamID, templateID := r.PathValue("id"), r.PathValue("templateId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue_template.deleted", templateID, nil, func(data *domain.Bootstrap) error {
		before := len(data.IssueTemplates)
		data.IssueTemplates = slices.DeleteFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool { return item.ID == templateID && item.TeamID == teamID })
		if len(data.IssueTemplates) == before {
			return errNotFound
		}
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listTeamLabels(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	teamID := r.PathValue("id")
	result := []domain.IssueLabel{}
	for _, label := range data.Labels {
		if label.Scope == teamID {
			result = append(result, label)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createTeamLabel(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueLabelMutationInput
	if !decodeJSON(w, r, &input) || input.Name == nil || strings.TrimSpace(*input.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	teamID := r.PathValue("id")
	var created domain.IssueLabel
	err := s.store.MutateWorkspaceWithAggregate(r.Context(), workspaceKey(r), "issue_label.created", input, func(data *domain.Bootstrap) (string, error) {
		if !teamExists(data, teamID) {
			return "", errNotFound
		}
		created = domain.IssueLabel{ID: fmt.Sprintf("label_%d", time.Now().UnixNano()), Name: strings.TrimSpace(*input.Name), Color: "#5E6AD2", Scope: teamID, ResourceType: "issue", CreatedAt: time.Now().UTC()}
		if input.Color != nil {
			created.Color = *input.Color
		}
		if input.Description != nil {
			created.Description = strings.TrimSpace(*input.Description)
		}
		data.Labels = append(data.Labels, created)
		return created.ID, nil
	})
	respondMutation(w, err, http.StatusCreated, created)
}

func (s *server) updateTeamLabel(w http.ResponseWriter, r *http.Request) {
	var input domain.IssueLabelMutationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	teamID, labelID := r.PathValue("id"), r.PathValue("labelId")
	var updated domain.IssueLabel
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue_label.updated", labelID, input, func(data *domain.Bootstrap) error {
		index := slices.IndexFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == labelID && item.Scope == teamID })
		if index < 0 {
			return errNotFound
		}
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" {
				return errInvalid
			}
			data.Labels[index].Name = name
		}
		if input.Description != nil {
			data.Labels[index].Description = strings.TrimSpace(*input.Description)
		}
		if input.Color != nil {
			data.Labels[index].Color = *input.Color
		}
		if input.ArchivedAt != nil {
			value := strings.TrimSpace(*input.ArchivedAt)
			if value == "" {
				data.Labels[index].ArchivedAt = nil
			} else if parsed, parseErr := time.Parse(time.RFC3339, value); parseErr == nil {
				data.Labels[index].ArchivedAt = &parsed
			} else {
				return errInvalid
			}
		}
		updated = data.Labels[index]
		for issueIndex := range data.Issues {
			for issueLabelIndex := range data.Issues[issueIndex].Labels {
				if data.Issues[issueIndex].Labels[issueLabelIndex].ID == labelID {
					data.Issues[issueIndex].Labels[issueLabelIndex] = updated
				}
			}
		}
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteTeamLabel(w http.ResponseWriter, r *http.Request) {
	teamID, labelID := r.PathValue("id"), r.PathValue("labelId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "issue_label.deleted", labelID, nil, func(data *domain.Bootstrap) error {
		before := len(data.Labels)
		data.Labels = slices.DeleteFunc(data.Labels, func(item domain.IssueLabel) bool { return item.ID == labelID && item.Scope == teamID })
		if len(data.Labels) == before {
			return errNotFound
		}
		removeLabelReferences(data, map[string]struct{}{labelID: {}})
		removeResourcePreferences(data, "label", labelID)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func applyIssueTemplate(data *domain.Bootstrap, template *domain.IssueTemplate, input domain.IssueTemplateMutationInput) error {
	if input.TeamID != nil {
		if *input.TeamID != "" && !teamExists(data, *input.TeamID) {
			return errInvalid
		}
		template.TeamID = *input.TeamID
	}
	if input.VisibilityTeamID != nil {
		if *input.VisibilityTeamID != "" && !teamExists(data, *input.VisibilityTeamID) {
			return errInvalid
		}
		template.VisibilityTeamID = *input.VisibilityTeamID
		template.Scope = "workspace"
		if *input.VisibilityTeamID != "" {
			template.Scope = "team"
		}
	}
	if input.Icon != nil {
		template.Icon = strings.TrimSpace(*input.Icon)
	}
	if input.Color != nil {
		template.Color = strings.TrimSpace(*input.Color)
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" {
			return errInvalid
		}
		template.Name = name
	}
	if input.Title != nil {
		template.Title = strings.TrimSpace(*input.Title)
	}
	if input.Description != nil {
		template.Description = strings.TrimSpace(*input.Description)
	}
	if input.Body != nil {
		template.Body = *input.Body
	}
	if input.StateID != nil {
		if stateForTeam(data, template.TeamID, *input.StateID) == nil {
			return errInvalid
		}
		template.StateID = *input.StateID
	}
	if input.Priority != nil {
		if *input.Priority < 0 || *input.Priority > 4 {
			return errInvalid
		}
		template.Priority = *input.Priority
	}
	if input.AssigneeID != nil {
		if *input.AssigneeID != "" && userByID(data, *input.AssigneeID) == nil {
			return errInvalid
		}
		template.AssigneeID = *input.AssigneeID
	}
	if input.ProjectID != nil {
		if *input.ProjectID != "" && projectByID(data, *input.ProjectID) == nil {
			return errInvalid
		}
		template.ProjectID = *input.ProjectID
	}
	if input.LabelIDs != nil {
		labels := labelsByIDForResource(data, *input.LabelIDs, "issue")
		if len(labels) != len(*input.LabelIDs) || !validLabelGroupSelection(labels) || !labelsAvailableToTeam(labels, template.TeamID) {
			return errInvalid
		}
		template.LabelIDs = slices.Clone(*input.LabelIDs)
	}
	if input.TemplateType != nil {
		if !slices.Contains([]string{"standard", "customForm"}, *input.TemplateType) {
			return errInvalid
		}
		template.TemplateType = *input.TemplateType
	}
	if input.FormFields != nil {
		for _, field := range *input.FormFields {
			if !slices.Contains([]string{"text", "longText", "dropdown", "checkboxes", "date", "upload", "instructions", "title", "labelGroup", "priority", "dueDate"}, field.Type) {
				return errInvalid
			}
			if !slices.Contains([]string{"instructions", "title", "labelGroup", "priority", "dueDate"}, field.Type) && strings.TrimSpace(field.Label) == "" {
				return errInvalid
			}
		}
		template.FormFields = slices.Clone(*input.FormFields)
	}
	if input.SubIssues != nil {
		for index := range *input.SubIssues {
			item := &(*input.SubIssues)[index]
			if strings.TrimSpace(item.Title) == "" || item.Priority < 0 || item.Priority > 4 {
				return errInvalid
			}
			if item.TeamID != "" && !teamExists(data, item.TeamID) {
				return errInvalid
			}
			if item.AssigneeID != "" && userByID(data, item.AssigneeID) == nil {
				return errInvalid
			}
			labels := labelsByIDForResource(data, item.LabelIDs, "issue")
			if len(labels) != len(item.LabelIDs) || !validLabelGroupSelection(labels) || !labelsAvailableToTeam(labels, item.TeamID) {
				return errInvalid
			}
			if item.ID == "" {
				item.ID = fmt.Sprintf("template_sub_issue_%d_%d", time.Now().UnixNano(), index)
			}
		}
		template.SubIssues = slices.Clone(*input.SubIssues)
	}
	return nil
}

func labelsAvailableToTeam(labels []domain.IssueLabel, teamID string) bool {
	return !slices.ContainsFunc(labels, func(label domain.IssueLabel) bool {
		return !labelScopeIsWorkspace(label.Scope) && label.Scope != teamID
	})
}

func validWorkflowType(value string) bool {
	return slices.Contains([]string{"backlog", "unstarted", "started", "completed", "canceled"}, value)
}
func teamExists(data *domain.Bootstrap, teamID string) bool {
	return slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == teamID })
}
func teamSettings(data *domain.Bootstrap, teamID string) domain.TeamSettings {
	settings := data.TeamSettings[teamID]
	if settings.TeamID == "" {
		settings = domain.TeamSettings{
			TeamID: teamID, Timezone: "Etc/UTC", EstimateType: "notUsed", Access: "public",
			MembershipRestriction: "open", SettingsPermission: "allMembers", LabelPermission: "allMembers",
			TemplatePermission: "allMembers", AgentSkillPermission: "allMembers", LoopPermission: "allMembers",
			MemberPermission: "allMembers", SlackNotifications: map[string]bool{}, PRAutomations: map[string]string{},
			StaleMonths: 6, AutoArchiveMonths: 6, ProgressOrder: "first", TriageAction: "none",
			ReleaseAutomations: []domain.TeamAutomationRule{}, TriageRules: []domain.TeamAutomationRule{},
			AgentSkills: []domain.TeamAgentSkill{}, ResolvedSummaries: true, ShowInitiatives: true,
		}
		states := statesForTeam(data, teamID)
		if len(states) > 0 {
			settings.DefaultStateID = states[0].ID
			for _, state := range states {
				if state.Default {
					settings.DefaultStateID = state.ID
				}
			}
		}
	}
	if settings.Access == "" {
		settings.Access = "public"
	}
	if settings.MembershipRestriction == "" {
		settings.MembershipRestriction = "open"
	}
	if settings.SettingsPermission == "" {
		settings.SettingsPermission = "allMembers"
	}
	if settings.LabelPermission == "" {
		settings.LabelPermission = "allMembers"
	}
	if settings.TemplatePermission == "" {
		settings.TemplatePermission = "allMembers"
	}
	if settings.AgentSkillPermission == "" {
		settings.AgentSkillPermission = "allMembers"
	}
	if settings.LoopPermission == "" {
		settings.LoopPermission = "allMembers"
	}
	if settings.MemberPermission == "" {
		settings.MemberPermission = "allMembers"
	}
	if settings.SlackNotifications == nil {
		settings.SlackNotifications = map[string]bool{}
	}
	if settings.PRAutomations == nil {
		settings.PRAutomations = map[string]string{}
	}
	if settings.StaleMonths == 0 {
		settings.StaleMonths = 6
	}
	if settings.AutoArchiveMonths == 0 {
		settings.AutoArchiveMonths = 6
	}
	if settings.ProgressOrder == "" {
		settings.ProgressOrder = "first"
	}
	if settings.TriageAction == "" {
		settings.TriageAction = "none"
	}
	if settings.ReleaseAutomations == nil {
		settings.ReleaseAutomations = []domain.TeamAutomationRule{}
	}
	if settings.TriageRules == nil {
		settings.TriageRules = []domain.TeamAutomationRule{}
	}
	if settings.AgentSkills == nil {
		settings.AgentSkills = []domain.TeamAgentSkill{}
	}
	return settings
}
func statesForTeam(data *domain.Bootstrap, teamID string) []domain.WorkflowState {
	result := []domain.WorkflowState{}
	specific := slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID })
	for _, state := range data.States {
		if specific && state.TeamID == teamID || !specific && state.TeamID == "" {
			result = append(result, state)
		}
	}
	slices.SortFunc(result, func(a, b domain.WorkflowState) int {
		if a.Position < b.Position {
			return -1
		}
		if a.Position > b.Position {
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result
}
func stateForTeam(data *domain.Bootstrap, teamID, stateID string) *domain.WorkflowState {
	specific := slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID })
	for index := range data.States {
		state := &data.States[index]
		if state.ID == stateID && (state.TeamID == teamID || !specific && state.TeamID == "") {
			return state
		}
	}
	return nil
}
func countStatesOfType(data *domain.Bootstrap, teamID, stateType string) int {
	count := 0
	for _, state := range statesForTeam(data, teamID) {
		if state.Type == stateType && !state.Reserved {
			count++
		}
	}
	return count
}

func workflowStateRank(state domain.WorkflowState) int {
	if state.Reserved {
		return 5
	}
	return map[string]int{"backlog": 0, "unstarted": 1, "started": 2, "completed": 3, "canceled": 4}[state.Type]
}

func normalizeWorkflowStatePositions(data *domain.Bootstrap, teamID string) {
	indexes := make([]int, 0)
	for index := range data.States {
		if data.States[index].TeamID == teamID {
			indexes = append(indexes, index)
		}
	}
	slices.SortStableFunc(indexes, func(left, right int) int {
		leftState, rightState := data.States[left], data.States[right]
		if rank := workflowStateRank(leftState) - workflowStateRank(rightState); rank != 0 {
			return rank
		}
		if leftState.Position < rightState.Position {
			return -1
		}
		if leftState.Position > rightState.Position {
			return 1
		}
		return strings.Compare(leftState.Name, rightState.Name)
	})
	for position, index := range indexes {
		data.States[index].Position = float64(position)
	}
}

func allUniqueStrings(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
func clearDefaultState(data *domain.Bootstrap, teamID string) {
	for index := range data.States {
		if data.States[index].TeamID == teamID {
			data.States[index].Default = false
		}
	}
}

func materializeTeamStates(data *domain.Bootstrap, teamID string) {
	if slices.ContainsFunc(data.States, func(state domain.WorkflowState) bool { return state.TeamID == teamID }) {
		return
	}
	idMap := map[string]domain.WorkflowState{}
	for _, source := range data.States {
		if source.TeamID != "" {
			continue
		}
		clone := source
		clone.ID = teamID + "_" + source.ID
		clone.TeamID = teamID
		idMap[source.ID] = clone
		data.States = append(data.States, clone)
	}
	for index := range data.Issues {
		if data.Issues[index].Team.ID == teamID {
			if state, ok := idMap[data.Issues[index].State.ID]; ok {
				data.Issues[index].State = state
			}
		}
	}
	for index := range data.IssueTemplates {
		if data.IssueTemplates[index].TeamID == teamID {
			if state, ok := idMap[data.IssueTemplates[index].StateID]; ok {
				data.IssueTemplates[index].StateID = state.ID
			}
		}
	}
	settings := teamSettings(data, teamID)
	if state, ok := idMap[settings.DefaultStateID]; ok {
		settings.DefaultStateID = state.ID
	}
	data.TeamSettings[teamID] = settings
}

func currentCycle(data *domain.Bootstrap, teamID string) *domain.Cycle {
	for index := range data.Cycles {
		if data.Cycles[index].TeamID == teamID && data.Cycles[index].Status == "current" {
			return &data.Cycles[index]
		}
	}
	return nil
}

func applyCycleAutomation(data *domain.Bootstrap, issue *domain.Issue) {
	if issue.CycleID != nil {
		return
	}
	settings := data.CycleSettings[issue.Team.ID]
	if !settings.Enabled {
		return
	}
	cycle := currentCycle(data, issue.Team.ID)
	if cycle == nil {
		return
	}
	active := issue.State.Type == "started"
	completed := issue.State.Type == "completed"
	dueInCycle := false
	if issue.DueDate != nil {
		if due, err := time.Parse("2006-01-02", *issue.DueDate); err == nil {
			dueInCycle = !due.Before(cycle.StartsAt) && !due.After(cycle.EndsAt)
		}
	}
	if settings.AutoAddActive && active || settings.AutoAddStarted && active || settings.AutoAddCompleted && completed || settings.AutoAddDueDate && dueInCycle {
		issue.CycleID = stringPointer(cycle.ID)
	}
}

func (s *server) maintainCycleSchedule(ctx context.Context, key string) {
	data, ok := s.store.BootstrapFor(key)
	if !ok {
		return
	}
	now := time.Now().UTC()
	needed := false
	for _, team := range data.Teams {
		settings := data.CycleSettings[team.ID]
		if !settings.Enabled || !settings.AutoCreate {
			continue
		}
		current := currentCycle(&data, team.ID)
		if current == nil || current.EndsAt.Before(now) || countUpcomingCycles(&data, team.ID) < max(1, settings.UpcomingCount) {
			needed = true
			break
		}
	}
	if !needed {
		return
	}
	_ = s.store.MutateWorkspace(ctx, key, "cycles.automatically_maintained", "cycles", nil, func(next *domain.Bootstrap) error {
		for _, team := range next.Teams {
			settings := next.CycleSettings[team.ID]
			if !settings.Enabled || !settings.AutoCreate {
				continue
			}
			current := currentCycle(next, team.ID)
			if current == nil {
				target := earliestUpcomingCycle(next, team.ID)
				if target == nil {
					target = appendFutureCycle(next, team.ID, latestCycle(next, team.ID))
				}
				if err := transitionToCycle(next, target, now); err != nil {
					return err
				}
			} else if current.EndsAt.Before(now) {
				target := earliestUpcomingCycle(next, team.ID)
				if target == nil {
					target = appendFutureCycle(next, team.ID, current)
				}
				if err := transitionToCycle(next, target, now); err != nil {
					return err
				}
			} else {
				ensureUpcomingCycles(next, current)
			}
		}
		return nil
	})
}

func appendIssueNotifications(data *domain.Bootstrap, issue domain.Issue, activity domain.ActivityEvent, comment *domain.Comment) {
	recipients := map[string]string{}
	if newAssignee := activity.Metadata["assignee"]; newAssignee != "" && newAssignee != activity.Metadata["previousAssignee"] {
		recipients[newAssignee] = "assignments"
	}
	baseCategory := "subscriptions"
	if comment != nil {
		baseCategory = "comments"
	} else if activity.Metadata["state"] != "" {
		baseCategory = "statusChanges"
	}
	if activity.Type != "comment.updated" {
		for _, subscriberID := range issue.SubscriberIDs {
			if _, assigned := recipients[subscriberID]; !assigned {
				recipients[subscriberID] = baseCategory
			}
		}
	}
	mentionIDs := mentionedUserIDs(data, issue, activity, comment)
	for _, userID := range mentionIDs {
		recipients[userID] = "mentions"
	}
	for recipientID, category := range recipients {
		if recipientID == "" || recipientID == activity.Actor.ID || userByID(data, recipientID) == nil {
			continue
		}
		preferences, ok := data.NotificationPreferences[recipientID]
		if !ok {
			preferences = defaultPreferences(recipientID)
		}
		if !preferences.Inbox.Enabled || !categoryEnabled(preferences.Inbox, category) {
			continue
		}
		kind := notificationType(category)
		sourceType, sourceID, commentID := "activity", activity.ID, ""
		if comment != nil {
			sourceType, sourceID, commentID = "comment", comment.ID, comment.ID
		}
		groupKey := recipientID + ":" + issue.ID + ":" + category
		notification := domain.Notification{ID: "notification_" + activity.ID + "_" + recipientID + "_" + category, RecipientID: recipientID, Type: kind, Category: category, GroupKey: groupKey, OccurrenceCount: 1, LatestActorIDs: []string{activity.Actor.ID}, SourceType: sourceType, SourceID: sourceID, IssueID: issue.ID, CommentID: commentID, ActivityID: activity.ID, Actor: activity.Actor, CreatedAt: activity.CreatedAt, UpdatedAt: activity.CreatedAt}
		if existing := aggregatableNotification(data, groupKey, activity.CreatedAt); existing != nil {
			existing.OccurrenceCount++
			existing.Actor = activity.Actor
			existing.LatestActorIDs = appendUnique(existing.LatestActorIDs, activity.Actor.ID)
			if len(existing.LatestActorIDs) > 3 {
				existing.LatestActorIDs = existing.LatestActorIDs[len(existing.LatestActorIDs)-3:]
			}
			existing.SourceType, existing.SourceID, existing.CommentID, existing.ActivityID, existing.UpdatedAt = sourceType, sourceID, commentID, activity.ID, activity.CreatedAt
			notification = *existing
		} else {
			data.Notifications = append(data.Notifications, notification)
		}
		enqueueNotificationDeliveries(data, notification, preferences)
	}
}

func notificationType(category string) string {
	switch category {
	case "assignments":
		return "assignment"
	case "mentions":
		return "mention"
	case "comments":
		return "comment"
	default:
		return "activity"
	}
}
func categoryEnabled(channel domain.NotificationChannelPreferences, category string) bool {
	enabled, ok := channel.Categories[category]
	return !ok || enabled
}
func aggregatableNotification(data *domain.Bootstrap, groupKey string, now time.Time) *domain.Notification {
	for index := len(data.Notifications) - 1; index >= 0; index-- {
		item := &data.Notifications[index]
		if item.GroupKey == groupKey && item.ReadAt == nil && item.DeletedAt == nil && item.ArchivedAt == nil && now.Sub(item.UpdatedAt) <= 6*time.Hour {
			return item
		}
	}
	return nil
}

func enqueueNotificationDeliveries(data *domain.Bootstrap, notification domain.Notification, preferences domain.NotificationPreferences) {
	now := notification.UpdatedAt
	appendDelivery := func(channel, status string) {
		id := "delivery_" + notification.ID + "_" + channel
		index := slices.IndexFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool { return item.ID == id })
		if index >= 0 {
			data.NotificationDeliveries[index].Status, data.NotificationDeliveries[index].UpdatedAt = status, now
			return
		}
		data.NotificationDeliveries = append(data.NotificationDeliveries, domain.NotificationDelivery{ID: id, NotificationID: notification.ID, RecipientID: notification.RecipientID, Channel: channel, Status: status, CreatedAt: now, UpdatedAt: now})
	}
	if preferences.Email.Enabled && categoryEnabled(preferences.Email, notification.Category) {
		status := "pending"
		if preferences.EmailFormat == "digest" && !(preferences.ImmediateUrgent && notification.Category == "assignments") {
			status = "digest"
		}
		appendDelivery("email", status)
	}
	if preferences.Desktop.Enabled && categoryEnabled(preferences.Desktop, notification.Category) {
		appendDelivery("desktop", "pending")
	}
	if slices.ContainsFunc(data.PushSubscriptions, func(item domain.PushSubscription) bool {
		return item.UserID == notification.RecipientID && item.Enabled
	}) && preferences.Desktop.Enabled && categoryEnabled(preferences.Desktop, notification.Category) {
		appendDelivery("push", "pending")
	}
}

func mentionedUserIDs(data *domain.Bootstrap, issue domain.Issue, activity domain.ActivityEvent, comment *domain.Comment) []string {
	text := ""
	var structured map[string]any
	if comment != nil {
		text, structured = comment.Body, comment.BodyData
	} else if activity.Metadata["description"] != "" || activity.Metadata["documentContent"] != "" {
		text = issue.Description
		if issue.DocumentContent != nil {
			structured = issue.DocumentContent.ContentData
		}
	}
	result := []string{}
	collectMentionIDs(structured, &result)
	lower := strings.ToLower(text)
	for _, user := range data.Users {
		if strings.Contains(lower, "@"+strings.ToLower(user.Email)) || strings.Contains(lower, "@"+strings.ToLower(user.Name)) || strings.Contains(lower, "@"+strings.ToLower(strings.ReplaceAll(user.DisplayName, " ", ""))) {
			result = appendUnique(result, user.ID)
		}
	}
	return result
}

func collectMentionIDs(value any, result *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		mentionLike := typed["type"] == "mention" || typed["type"] == "userMention" || typed["type"] == "user"
		if mentionLike {
			for _, key := range []string{"userId", "id"} {
				if id, ok := typed[key].(string); ok && id != "" {
					*result = appendUnique(*result, id)
				}
			}
			if attrs, ok := typed["attrs"].(map[string]any); ok {
				for _, key := range []string{"userId", "id"} {
					if id, ok := attrs[key].(string); ok && id != "" {
						*result = appendUnique(*result, id)
					}
				}
			}
		}
		for _, child := range typed {
			collectMentionIDs(child, result)
		}
	case []any:
		for _, child := range typed {
			collectMentionIDs(child, result)
		}
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(typed, &decoded) == nil {
			collectMentionIDs(decoded, result)
		}
	}
}
