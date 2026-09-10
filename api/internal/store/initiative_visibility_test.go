package store

import (
	"flow/api/internal/domain"
	"testing"
)

func TestInitiativeLeadTeamControlsPrivateVisibility(t *testing.T) {
	data := domain.Bootstrap{ViewerRole: "member", Teams: []domain.Team{{ID: "public"}, {ID: "private", Private: true}}, Projects: []domain.Project{{ID: "project", TeamIDs: []string{"public"}}}, Initiatives: []domain.Initiative{
		{ID: "hidden", LeadTeamID: "private", ContributingTeamIDs: []string{"public"}, ProjectIDs: []string{"project"}},
		{ID: "visible", LeadTeamID: "public", ParentInitiativeIDs: []string{"hidden"}},
		{ID: "legacy", LeadTeamID: "public"},
	}, InitiativeRelations: []domain.InitiativeRelation{{ID: "edge", InitiativeID: "visible", RelatedInitiativeID: "hidden", Type: "parent"}, {ID: "legacy-edge", InitiativeID: "legacy", RelatedInitiativeID: "visible", Type: "parent"}}, InitiativeUpdates: map[string][]domain.InitiativeUpdate{"hidden": {{ID: "private-update"}}}}
	filterBootstrapTeams(&data, map[string]bool{"public": true}, false)
	if len(data.Initiatives) != 2 || len(data.InitiativeRelations) != 1 {
		t.Fatal("Private initiative leaked through a public project or relation")
	}
	for _, item := range data.Initiatives {
		if item.ID == "visible" && len(item.ParentInitiativeIDs) != 0 {
			t.Fatal("Hidden parent ID leaked")
		}
		if item.ID == "legacy" && (len(item.ParentInitiativeIDs) != 1 || item.ParentInitiativeIDs[0] != "visible") {
			t.Fatal("Legacy parent relation was not projected")
		}
	}
	if _, exists := data.InitiativeUpdates["hidden"]; exists {
		t.Fatal("Private initiative updates leaked")
	}
}
