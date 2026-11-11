package domain

import (
	"reflect"
	"slices"
	"testing"
)

func TestScaleP04CompactTeamParentsPreserveMemberships(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		settings := map[string]TeamSettings{
			"child":  {TeamID: "child", ParentTeamID: "parent"},
			"parent": {TeamID: "parent", ParentTeamID: "root"},
			"root":   {TeamID: "root"},
			"new":    {TeamID: "new", ParentTeamID: "child"},
		}
		if cycle {
			settings["root"] = TeamSettings{TeamID: "root", ParentTeamID: "child"}
		}
		members := []WorkspaceMember{{User: User{ID: "guest"}, Role: "guest"}}
		teamMembers := []TeamMember{{TeamID: "child", UserID: "member", Role: "owner"}, {TeamID: "child", UserID: "guest", Role: "member"}, {TeamID: "new", UserID: "creator", Role: "owner"}}
		full := Bootstrap{TeamSettings: settings, Members: members, TeamMembers: slices.Clone(teamMembers)}
		narrow := Bootstrap{TeamSettings: map[string]TeamSettings{"new": settings["new"]}, TeamParents: map[string]string{}, Members: members, TeamMembers: slices.Clone(teamMembers)}
		for id, value := range settings {
			narrow.TeamParents[id] = value.ParentTeamID
		}
		SyncTeamAncestorMembers(&full)
		SyncTeamAncestorMembers(&narrow)
		if !reflect.DeepEqual(full.TeamMembers, narrow.TeamMembers) {
			t.Fatalf("cycle=%v: compact hierarchy changed inherited membership/order:\nfull=%+v\nnarrow=%+v", cycle, full.TeamMembers, narrow.TeamMembers)
		}
	}
}

func TestScaleP04ExplicitTeamSettingsOverrideParentHints(t *testing.T) {
	data := Bootstrap{TeamSettings: map[string]TeamSettings{"detached": {TeamID: "detached"}}, TeamParents: map[string]string{"detached": "former-parent"}, TeamMembers: []TeamMember{{TeamID: "detached", UserID: "member"}}}
	SyncTeamAncestorMembers(&data)
	if len(data.TeamMembers) != 1 {
		t.Fatal("parent hint overrode an explicitly detached team")
	}
}
