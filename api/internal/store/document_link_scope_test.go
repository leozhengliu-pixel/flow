package store

import (
	"slices"
	"testing"

	"flow/api/internal/domain"
)

func TestDocumentLinkScopeClosedTeams(t *testing.T) {
	teams := []domain.Team{{ID: "public"}, {ID: "private", Private: true}, {ID: "restricted"}, {ID: "invite"}, {ID: "parent", Private: true}, {ID: "child"}}
	settings := map[string]domain.TeamSettings{
		"restricted": {Access: "restricted"},
		"invite":     {MembershipRestriction: "members"},
		"child":      {ParentTeamID: "parent"},
	}
	scope := NewDocumentLinkScope(teams, settings)
	document := domain.Document{InheritedTeamIDs: []string{"public", "private", "restricted", "invite", "child", "deleted-or-hidden"}}
	if got, want := scope.ClosedTeams(document), []string{"private", "restricted", "invite", "child", "deleted-or-hidden"}; !slices.Equal(got, want) {
		t.Fatalf("closed teams = %v, want %v", got, want)
	}
	if scope.ClosedTeams(domain.Document{}) != nil || !scope.Open(domain.Document{InheritedTeamIDs: []string{"public"}}, func(string) bool { return false }) {
		t.Fatal("a public team narrowed a document")
	}
	private := domain.Document{InheritedTeamIDs: []string{"public", "private"}}
	if scope.Open(private, func(id string) bool { return id == "public" }) {
		t.Fatal("reading the public team opened a document scoped to a private team")
	}
	if !scope.Open(private, func(id string) bool { return id == "private" }) {
		t.Fatal("a private team member was kept out")
	}
}

func TestFilterBootstrapTeamsScopesLinkedDocuments(t *testing.T) {
	workspaceGrant := []domain.DocumentPermission{{SubjectType: "workspace", Role: "editor"}}
	data := domain.Bootstrap{
		Viewer:     domain.User{ID: "viewer"},
		ViewerRole: "member",
		Teams:      []domain.Team{{ID: "public"}, {ID: "private", Private: true}},
		Documents: []domain.Document{
			{ID: "on-public", InheritedTeamIDs: []string{"public"}, Permissions: workspaceGrant},
			{ID: "on-private", InheritedTeamIDs: []string{"private"}, Permissions: workspaceGrant},
			{ID: "legacy-on-private", InheritedTeamIDs: []string{"private"}},
			{ID: "shared", InheritedTeamIDs: []string{"private"}, Permissions: append(slices.Clone(workspaceGrant), domain.DocumentPermission{SubjectType: "user", SubjectID: "viewer", Role: "viewer"})},
		},
	}
	filterBootstrapTeams(&data, sameTeamScope(map[string]bool{"public": true}), false)
	ids := []string{}
	for _, document := range data.Documents {
		ids = append(ids, document.ID)
	}
	slices.Sort(ids)
	if want := []string{"on-public", "shared"}; !slices.Equal(ids, want) {
		t.Fatalf("visible documents = %v, want %v", ids, want)
	}
}
