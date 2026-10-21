package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"flow/api/internal/domain"
)

// Members provisioned by an active SCIM connection are managed by the
// identity provider: their workspace role, name, email and status (suspend,
// activate, remove) change only through SCIM. Admins who need to manage them
// in Flow turn SCIM provisioning off (or revoke every SCIM token) first.
// SCIM-managed team memberships are protected separately
// (store.ErrManagedTeamMembership).

const scimManagedMemberMessage = "This member is managed by SCIM provisioning. Change their role, name, email or status in your identity provider, or turn off SCIM provisioning (Settings › Security › identity provider) to manage them in Flow."

// auditSCIMSettings validates the deprovisioning setting of a workspace
// settings update and audits changes to it and to SCIM being turned on or
// off (the escape hatch that hands SCIM-managed members back to admins).
func auditSCIMSettings(data *domain.Bootstrap, next *domain.WorkspaceSettings) error {
	action := strings.ToLower(strings.TrimSpace(next.SCIMDeprovisionAction))
	if action != "" && action != scimDeprovisionSuspend && action != scimDeprovisionRemove {
		return fmt.Errorf("%w: scimDeprovisionAction must be suspend or remove", errInvalid)
	}
	next.SCIMDeprovisionAction = action
	previous := data.WorkspaceSettings
	effective := func(value string) string {
		if value == "" {
			return scimDeprovisionSuspend
		}
		return value
	}
	if effective(previous.SCIMDeprovisionAction) != effective(action) {
		appendAudit(data, "deprovision_action_changed", "scim_settings", "scimDeprovisionAction", map[string]any{"before": effective(previous.SCIMDeprovisionAction), "after": effective(action)})
	}
	if previous.SCIMEnabled != next.SCIMEnabled {
		event := "disabled"
		if next.SCIMEnabled {
			event = "enabled"
		}
		appendAudit(data, event, "scim_settings", "scimEnabled", nil)
	}
	return nil
}

// scimManagesMember reports whether SCIM provisioning manages userID in the
// workspace. AUTH_DISABLED development workspaces have no SCIM members.
func (s *server) scimManagesMember(ctx context.Context, workspaceID, userID string) bool {
	if s.authDisabled || workspaceID == "" || userID == "" {
		return false
	}
	managed, err := s.store.IsSCIMManagedMember(ctx, workspaceID, userID)
	if err != nil {
		log.Printf("SCIM managed check workspace=%s user=%s: %v", workspaceID, userID, err)
		return false
	}
	return managed
}

// refuseSCIMManagedMember answers 403 SCIM_MANAGED for a manual change to a
// SCIM-managed member and reports whether it did.
func (s *server) refuseSCIMManagedMember(w http.ResponseWriter, r *http.Request, workspaceID, userID string) bool {
	if !s.scimManagesMember(r.Context(), workspaceID, userID) {
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": scimManagedMemberMessage, "code": "SCIM_MANAGED", "managedBy": "scim"})
	return true
}
