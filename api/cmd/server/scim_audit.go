package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// SCIM provisioning accountability: user provisioning, profile updates,
// suspensions, restores, deprovisioning, role changes and group changes are
// audit log entries (metadata.source "scim", attributed to the SCIM token
// that made them). Team membership changes from linked groups keep their own
// team_member entries.

const (
	scimAuditMemberResource = "workspace_member"
	scimAuditGroupResource  = "scim_group"
	scimDeprovisionSuspend  = "suspend"
	scimDeprovisionRemove   = "remove"
	scimAuditListCap        = 100
)

type scimAuditEntry struct {
	action       string
	resourceType string
	resourceID   string
	metadata     map[string]any
}

// recordSCIMAudit appends entries (in order, newest last) to the workspace
// audit log. A failure is logged: the provisioning change is committed.
func (s *server) recordSCIMAudit(r *http.Request, workspaceKey string, entries []scimAuditEntry) {
	if len(entries) == 0 {
		return
	}
	actor := s.scimTeamMemberActor(r, "", "")
	ctx := store.WithMetadataFields(context.WithoutCancel(r.Context()), "auditLog")
	err := s.store.MutateWorkspace(ctx, workspaceKey, "scim.audited", entries[0].resourceID, map[string]any{"auditEntries": len(entries)}, func(data *domain.Bootstrap) error {
		now := time.Now().UTC()
		built := make([]domain.AuditLogEntry, 0, len(entries))
		for _, entry := range entries {
			metadata := map[string]any{"source": teamMemberSourceSCIM}
			for key, value := range actor.meta {
				metadata[key] = value
			}
			for key, value := range entry.metadata {
				metadata[key] = value
			}
			built = append(built, domain.AuditLogEntry{ID: idgen.New("audit_"), Actor: actor.actor, BotActor: actor.bot, Action: entry.action, ResourceType: entry.resourceType, ResourceID: entry.resourceID, Metadata: metadata, CreatedAt: now})
		}
		slices.Reverse(built)
		data.AuditLog = append(built, data.AuditLog...)
		if len(data.AuditLog) > 1000 {
			data.AuditLog = data.AuditLog[:1000]
		}
		return nil
	})
	if err != nil {
		log.Printf("SCIM audit workspace=%s: %v", workspaceKey, err)
	}
}

// scimUserAuditSubject names the member an entry is about. Values the IdP
// sends as secrets do not exist in SCIM user resources (Flow never accepts
// passwords), so only identifying attributes are recorded.
func scimUserAuditSubject(item store.SCIMUser) map[string]any {
	metadata := map[string]any{"userId": item.User.ID, "scimUserName": item.UserName}
	if name := item.User.DisplayName; name != "" {
		metadata["userName"] = name
	}
	if item.User.Email != "" {
		metadata["userEmail"] = item.User.Email
	}
	if !item.ExternalIDDefaulted && item.ExternalID != "" {
		metadata["externalId"] = item.ExternalID
	}
	return metadata
}

func scimUserAudit(action string, item store.SCIMUser, extra map[string]any) scimAuditEntry {
	metadata := scimUserAuditSubject(item)
	for key, value := range extra {
		metadata[key] = value
	}
	return scimAuditEntry{action: action, resourceType: scimAuditMemberResource, resourceID: item.User.ID, metadata: metadata}
}

// scimChangedAttributes lists the attributes (by name, without values) that
// differ between two renderings of a resource.
func scimChangedAttributes(before, after map[string]any) []string {
	left, right := scimFilterable(before), scimFilterable(after)
	changed := []string{}
	seen := map[string]bool{}
	for _, object := range []map[string]any{left, right} {
		for key := range object {
			if seen[key] || key == "meta" || key == "schemas" || key == "id" || key == "active" {
				continue
			}
			seen[key] = true
			if !reflect.DeepEqual(left[key], right[key]) {
				name := key
				if key == scimSchemaEnterpriseUser {
					name = "enterprise"
				}
				changed = append(changed, name)
			}
		}
	}
	slices.Sort(changed)
	return changed
}

// scimRoleChangeAudits compares membership roles before and after a write
// and returns one role_changed entry per member whose role changed.
func (s *server) scimRoleChangeAudits(ctx context.Context, workspaceID string, before, after map[string]store.SCIMMemberState, extra map[string]any) []scimAuditEntry {
	ids := make([]string, 0, len(after))
	for userID := range after {
		ids = append(ids, userID)
	}
	slices.Sort(ids)
	entries := []scimAuditEntry{}
	for _, userID := range ids {
		previous, ok := before[userID]
		if !ok || previous.Role == after[userID].Role {
			continue
		}
		metadata := map[string]any{"userId": userID, "roleBefore": previous.Role, "roleAfter": after[userID].Role}
		if item, err := s.store.SCIMUser(ctx, workspaceID, userID); err == nil {
			metadata = scimUserAuditSubject(item)
			metadata["roleBefore"], metadata["roleAfter"] = previous.Role, after[userID].Role
		}
		for key, value := range extra {
			metadata[key] = value
		}
		entries = append(entries, scimAuditEntry{action: "role_changed", resourceType: scimAuditMemberResource, resourceID: userID, metadata: metadata})
	}
	return entries
}

func (s *server) scimMemberStates(ctx context.Context, workspaceID string, userIDs ...string) map[string]store.SCIMMemberState {
	states, err := s.store.SCIMMemberStates(ctx, workspaceID, userIDs)
	if err != nil {
		log.Printf("SCIM member states workspace=%s: %v", workspaceID, err)
		return map[string]store.SCIMMemberState{}
	}
	return states
}

func scimGroupAuditSubject(group store.SCIMGroup) map[string]any {
	metadata := map[string]any{"scimGroupId": group.ID, "scimGroupName": group.DisplayName}
	if group.ExternalID != "" {
		metadata["externalId"] = group.ExternalID
	}
	if group.Role != "" {
		metadata["role"] = group.Role
	}
	return metadata
}

func scimCappedList(values []string) []string {
	if len(values) > scimAuditListCap {
		return values[:scimAuditListCap]
	}
	return values
}

// scimGroupMemberAudit is a members_changed entry for a group's net member
// change (user ids, at most scimAuditListCap of each listed).
func scimGroupMemberAudit(group store.SCIMGroup, before []store.SCIMGroupMember) (scimAuditEntry, bool) {
	previous, next := map[string]bool{}, map[string]bool{}
	for _, member := range before {
		previous[member.UserID] = true
	}
	added, removed := []string{}, []string{}
	for _, member := range group.Members {
		next[member.UserID] = true
		if !previous[member.UserID] {
			added = append(added, member.UserID)
		}
	}
	for _, member := range before {
		if !next[member.UserID] {
			removed = append(removed, member.UserID)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return scimAuditEntry{}, false
	}
	metadata := scimGroupAuditSubject(group)
	metadata["added"], metadata["addedCount"] = scimCappedList(added), len(added)
	metadata["removed"], metadata["removedCount"] = scimCappedList(removed), len(removed)
	metadata["memberCount"] = len(group.Members)
	return scimAuditEntry{action: "members_changed", resourceType: scimAuditGroupResource, resourceID: group.ID, metadata: metadata}, true
}

func scimGroupMemberIDs(members ...[]store.SCIMGroupMember) []string {
	seen := map[string]bool{}
	ids := []string{}
	for _, list := range members {
		for _, member := range list {
			if !seen[member.UserID] {
				seen[member.UserID] = true
				ids = append(ids, member.UserID)
			}
		}
	}
	return ids
}

// scimDeprovisionAction is the workspace's deprovisioning setting.
func (s *server) scimDeprovisionAction(workspaceKey string) string {
	if strings.EqualFold(strings.TrimSpace(s.scimWorkspaceSettings(workspaceKey).SCIMDeprovisionAction), scimDeprovisionRemove) {
		return scimDeprovisionRemove
	}
	return scimDeprovisionSuspend
}

// removeSCIMMember removes a deprovisioned member from the workspace the way
// an admin's removal does: team memberships end (audited as team_member
// removals), the departure is recorded and assignee suggestions for them are
// dropped. The account and its work stay. It reports whether a membership
// was removed.
func (s *server) removeSCIMMember(r *http.Request, workspaceKey, workspaceID, userID string) (bool, error) {
	changes, err := s.store.RemoveMemberWithChanges(r.Context(), workspaceID, userID)
	if errors.Is(err, store.ErrAuthForbidden) {
		return false, nil // not a member (already removed)
	}
	if err != nil {
		return false, err
	}
	if err := s.store.DetachSCIMUserTeams(r.Context(), workspaceID, userID); err != nil {
		return true, err
	}
	s.recordTeamMemberAudit(r.Context(), workspaceKey, s.scimTeamMemberActor(r, "", ""), teamMemberReasonWorkspaceMemberRemoved, changes, nil)
	s.pruneSuggestedAssignee(r, workspaceKey, userID)
	return true, nil
}

// resyncSCIMUserTeams re-adds a restored member to the teams linked to their
// SCIM groups.
func (s *server) resyncSCIMUserTeams(r *http.Request, workspaceKey, workspaceID, userID string) error {
	groups, err := s.store.SCIMUserGroupIDs(r.Context(), workspaceID, userID)
	if err != nil {
		return err
	}
	for _, groupID := range groups {
		group, err := s.store.SCIMGroup(r.Context(), workspaceID, groupID)
		if err != nil {
			continue
		}
		if err := s.syncSCIMGroupTeamIfLinked(r, workspaceKey, workspaceID, group); err != nil {
			return err
		}
	}
	return nil
}

// applySCIMProfileSettings maps SCIM profile attributes onto the member's
// Flow profile: title becomes the job title (the directory is its source),
// and timezone fills the Pulse delivery zone when the member has none.
func (s *server) applySCIMProfileSettings(ctx context.Context, workspaceKey, userID string, before, after store.SCIMProfile) {
	titleChanged := before.Title != after.Title
	zone := ""
	if after.Timezone != "" && after.Timezone != before.Timezone {
		if _, err := time.LoadLocation(after.Timezone); err == nil {
			zone = after.Timezone
		}
	}
	if !titleChanged && zone == "" {
		return
	}
	ctx = store.WithMetadataFields(context.WithoutCancel(ctx), "userSettings")
	err := s.store.MutateWorkspace(ctx, workspaceKey, "user_settings.scim_profile", userID, nil, func(data *domain.Bootstrap) error {
		if data.UserSettings == nil {
			data.UserSettings = map[string]domain.UserSettings{}
		}
		settings := data.UserSettings[userID]
		changed := false
		// A job title the member set themselves is replaced only when the
		// directory sends a title; clearing it in the directory clears only
		// the title it set.
		if titleChanged && (after.Title != "" || settings.JobTitle == before.Title) && settings.JobTitle != after.Title {
			settings.JobTitle, changed = after.Title, true
		}
		if zone != "" && settings.Timezone == "" {
			settings.Timezone, changed = zone, true
		}
		if !changed {
			return nil
		}
		settings.UserID, settings.UpdatedAt = userID, time.Now().UTC()
		data.UserSettings[userID] = settings
		return nil
	})
	if err != nil {
		log.Printf("SCIM profile settings workspace=%s user=%s: %v", workspaceKey, userID, err)
	}
}
