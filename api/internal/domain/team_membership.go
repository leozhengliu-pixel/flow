package domain

import "strings"

// Team membership origins say what created a membership. Removals can be
// scoped to an origin so a sync only ever removes the members it added.
const (
	// TeamMemberOriginManual: a person added the member in Flow (team
	// settings, joining a team, creating a team). Memberships from before
	// origins were tracked are manual too.
	TeamMemberOriginManual = "manual"
	// TeamMemberOriginInvitation: the member accepted an invitation that
	// named the team.
	TeamMemberOriginInvitation = "invitation"
	// TeamMemberOriginAPI: an API key or OAuth application added the member
	// without declaring an origin.
	TeamMemberOriginAPI = "api"
	// TeamMemberOriginSync: an external directory or HR sync added the
	// member and said so (origin "sync").
	TeamMemberOriginSync = "sync"
	// TeamMemberOriginSCIM: a SCIM group mapped to the team added the
	// member; SCIM removes it again when the user leaves the group.
	TeamMemberOriginSCIM = "scim"
)

// TeamMemberOrigins lists every origin, in documentation order.
var TeamMemberOrigins = []string{TeamMemberOriginManual, TeamMemberOriginInvitation, TeamMemberOriginAPI, TeamMemberOriginSync, TeamMemberOriginSCIM}

// TeamMemberOriginOrDefault returns origin, or manual when it is empty or
// unknown (rows written before origins were tracked).
func TeamMemberOriginOrDefault(origin string) string {
	origin = strings.ToLower(strings.TrimSpace(origin))
	for _, known := range TeamMemberOrigins {
		if origin == known {
			return origin
		}
	}
	return TeamMemberOriginManual
}

// ValidTeamMemberOrigin reports whether origin is one of TeamMemberOrigins.
func ValidTeamMemberOrigin(origin string) bool {
	origin = strings.ToLower(strings.TrimSpace(origin))
	for _, known := range TeamMemberOrigins {
		if origin == known {
			return true
		}
	}
	return false
}

// Team membership change actions (TeamMemberChange.Action). They are also the
// audit log actions of resource type "team_member".
const (
	TeamMemberAdded         = "added"
	TeamMemberRemoved       = "removed"
	TeamMemberRoleChanged   = "role_changed"
	TeamMemberOriginChanged = "origin_changed"
)

// TeamMemberChange is one team membership a write added, removed or changed
// (or, in a dry run, would).
type TeamMemberChange struct {
	TeamID   string `json:"teamId"`
	TeamKey  string `json:"teamKey,omitempty"`
	UserID   string `json:"userId"`
	UserName string `json:"userName,omitempty"`
	// Action is added, removed, role_changed or origin_changed.
	Action     string `json:"action" enum:"added,removed,role_changed,origin_changed"`
	RoleBefore string `json:"roleBefore,omitempty"`
	RoleAfter  string `json:"roleAfter,omitempty"`
	// Origin is the membership's origin (for a removal, the origin it had).
	Origin       string `json:"origin" enum:"manual,invitation,api,sync,scim"`
	OriginBefore string `json:"originBefore,omitempty"`
	// Inherited marks a parent-team membership added because the user
	// joined one of its sub-teams.
	Inherited bool `json:"inherited,omitempty"`
}

// Reasons a removal or sync keeps a member (TeamMemberKept.Reason).
const (
	TeamMemberKeptOwner          = "owner"
	TeamMemberKeptOriginMismatch = "origin_mismatch"
	TeamMemberKeptSCIMManaged    = "scim_managed"
	TeamMemberKeptLastOwner      = "last_owner"
	TeamMemberKeptSubTeamMember  = "sub_team_member"
	TeamMemberKeptNotMember      = "not_a_member"
	// TeamMemberSkippedUnknownUser: a sync listed someone who is not an
	// active workspace member, so they were not added.
	TeamMemberSkippedUnknownUser = "unknown_user"
)

// TeamMemberKept is a member a removal or sync left in place (or did not
// add), and why.
type TeamMemberKept struct {
	TeamID   string `json:"teamId"`
	UserID   string `json:"userId"`
	UserName string `json:"userName,omitempty"`
	Role     string `json:"role,omitempty"`
	Origin   string `json:"origin,omitempty"`
	// Reason is owner, origin_mismatch, scim_managed, last_owner,
	// sub_team_member, not_a_member or unknown_user.
	Reason string `json:"reason" enum:"owner,origin_mismatch,scim_managed,last_owner,sub_team_member,not_a_member,unknown_user"`
}

// TeamMemberInput adds a member to a team or changes their role. Origin is
// manual, api or sync (invitation and scim are set by Flow); on an existing
// membership a given origin replaces the stored one.
type TeamMemberInput struct {
	Role   string `json:"role,omitempty" enum:"member,owner"`
	Origin string `json:"origin,omitempty" enum:"manual,api,sync"`
}

// TeamMemberRemoval is the result of removing a member from a team. With
// dryRun nothing was written and Removed lists what would be removed.
type TeamMemberRemoval struct {
	DryRun  bool               `json:"dryRun"`
	Removed []TeamMemberChange `json:"removed"`
	Kept    []TeamMemberKept   `json:"kept"`
}

// TeamMemberSyncInput makes a team's membership match a directory. Members
// lists the user ids or emails that belong on the team. Users missing from
// the team are added with Origin (default sync); members not listed are
// removed only when their origin is in RemoveOrigins (default: Origin).
// Owners and SCIM-managed members are never removed, and manual members only
// when "manual" is listed explicitly.
type TeamMemberSyncInput struct {
	Members       []string `json:"members"`
	Origin        string   `json:"origin,omitempty" enum:"manual,api,sync"`
	RemoveOrigins []string `json:"removeOrigins,omitempty"`
	DryRun        bool     `json:"dryRun,omitempty"`
}

// TeamMemberSyncResult lists what a sync added, removed and kept (with
// dryRun: what it would).
type TeamMemberSyncResult struct {
	DryRun  bool               `json:"dryRun"`
	Added   []TeamMemberChange `json:"added"`
	Removed []TeamMemberChange `json:"removed"`
	Kept    []TeamMemberKept   `json:"kept"`
	Skipped []TeamMemberKept   `json:"skipped"`
}
