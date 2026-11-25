package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW174States struct {
	Options []map[string]any
	IDs     []string
	Default string
}

type scaleP21SW174Default struct {
	Action chatCardAction
	Error  string
}

type scaleP21SW174Output struct {
	Error                        string
	Languages                    []string
	Creatable                    []map[string]any
	States                       map[string]scaleP21SW174States
	Assignees                    []map[string]any
	AssigneeIDs                  []string
	Defaults                     map[string]scaleP21SW174Default
	TemplatePermitted            map[string]bool
	ViewerTeamIDs                []string
	DefaultPriority              int
	RequestState, RequestProject *string
	ProjectSelect                map[string]any
	OfferedProjects              []string
	ProjectsLabel                string
	Roles                        map[string]string
}

func scaleP21SW174Evaluate(service *server, data domain.Bootstrap, workspace, prefix, public, private, child, userID string, creator domain.User, guestID string) scaleP21SW174Output {
	result := scaleP21SW174Output{
		Languages: []string{chatLanguage(data, userID, "lark"), chatLanguage(data, userID, "feishu")},
		States:    map[string]scaleP21SW174States{}, Defaults: map[string]scaleP21SW174Default{},
		TemplatePermitted: map[string]bool{}, Roles: map[string]string{},
		ViewerTeamIDs: slices.Clone(data.ViewerTeamIDs),
	}
	for _, team := range chatCreatableTeams(data) {
		result.Creatable = append(result.Creatable, chatOption(team.ID, team.Name))
	}
	for _, teamID := range []string{public, private, child, prefix + "fallback"} {
		options, ids, selected := chatStateOptions(data, teamID)
		result.States[teamID] = scaleP21SW174States{options, ids, selected}
	}
	result.Assignees, result.AssigneeIDs = chatAssigneeOptions(result.Languages[0], data, userID)
	binding := domain.ChatBinding{
		ID: prefix + "binding", ChatID: prefix + "chat", TeamID: public,
		ProjectIDs: []string{prefix + "project", prefix + "second-project"},
		Requests:   &domain.ChatBindingRequests{Enabled: true, TemplateID: prefix + "template"},
	}
	for _, name := range []string{"", "template", "form-fields", "form-type", "foreign", "duplicate"} {
		template := ""
		if name != "" {
			template = prefix + name
		}
		current := binding
		current.TemplateID = template
		action, err := chatCreateDefaults(data, current, "")
		failure := ""
		if err != nil {
			failure = err.Error()
		}
		result.Defaults[name] = scaleP21SW174Default{action, failure}
		result.TemplatePermitted[name] = chatBindingTemplatePermitted(data, binding, template)
	}
	result.DefaultPriority = teamSettings(&data, public).DefaultPriority
	result.RequestState = chatRequestState(data, public)
	result.RequestProject = chatRequestProject(data, binding)
	result.ProjectSelect, result.OfferedProjects = chatCreateProjectSelect(result.Languages[0], data, binding, prefix+"project")
	result.ProjectsLabel = chatProjectsLabel(result.Languages[0], data, binding)
	parentID := prefix + "handed-parent"
	for name, issue := range map[string]domain.Issue{
		"direct-guest": {
			ID: prefix + "direct", Creator: creator, Team: domain.Team{ID: public},
			Permissions: []domain.IssuePermission{{SubjectType: "user", SubjectID: guestID, Role: "commenter"}},
		},
		"workspace-grant": {
			ID: prefix + "workspace-grant", Creator: creator, Team: domain.Team{ID: public},
			Permissions: []domain.IssuePermission{{SubjectType: "workspace", SubjectID: workspace, Role: "viewer"}},
		},
		"handed-child": {
			ID: prefix + "handed-child", Creator: creator, Team: domain.Team{ID: public}, ParentID: &parentID,
		},
		"private": {
			ID: prefix + "private-issue", Creator: creator, Team: domain.Team{ID: private, Private: true},
		},
	} {
		result.Roles[name] = issueRole(service, data, issue)
	}
	return result
}

func scaleP21SW174Call(t *testing.T, source *store.SQLiteStore, ctx context.Context, workspace, userID, prefix, public, private, child string, creator domain.User, guestID string) json.RawMessage {
	t.Helper()
	ctx, closeOutput := beginMetadataOutputReads(ctx)
	defer closeOutput()
	service := &server{store: source}
	data := domain.Bootstrap{ViewerRole: "unchanged-destination"}
	var err error
	if source.BoundedMetadataEnabled() {
		err = service.chatAuthorizedWorkflowHeaders(ctx, workspace, userID, &data)
	} else {
		err = service.chatAuthorizedMutation(ctx, workspace, userID, http.MethodGet, "/api/issue-records/bootstrap", nil, &data)
	}
	result := scaleP21SW174Output{Error: scaleP21ChatPointError(err)}
	if err != nil {
		if data.ViewerRole != "unchanged-destination" {
			t.Fatal("refused workflow headers changed their destination")
		}
	} else {
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() == 0 {
			t.Fatal("workflow header values lost their containing output owner")
		}
		result = scaleP21SW174Evaluate(service, data, workspace, prefix, public, private, child, userID, creator, guestID)
	}
	// Freeze the small observable result while the real headers are owned.
	// The test oracle does not keep borrowed card strings after Close.
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	closeOutput()
	if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
		t.Fatal("workflow header operation leaked work or decoded full metadata")
	}
	return encoded
}

func TestScaleP21SW174WorkflowHeadersDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21SW10Setup(t)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo, workspace, prefix := f.repository, f.data.Workspace.URLKey, idgen.New("sw174-")
	native, found := repo.WorkspaceMetadataFields(workspace, "projects", "teams", "teamSettings")
	if !found {
		t.Fatal("workflow fixture workspace missing")
	}
	private, child := "", ""
	for _, project := range native.Projects {
		if project.ID == "m2-unrelated" && len(project.TeamIDs) > 0 {
			private = project.TeamIDs[0]
		}
	}
	for id, settings := range native.TeamSettings {
		if settings.ParentTeamID == private {
			child = id
		}
	}
	if private == "" || child == "" {
		t.Fatal("workflow private/inherited fixture teams missing")
	}
	creator, guest := f.principals[1].user, f.principals[3].user
	body := strings.Repeat("Unrelated legal workflow bootstrap body <>&. ", 50000)
	at := time.Date(2026, 11, 15, 1, 2, 3, 456, time.UTC)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw174_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: prefix + "fallback", Name: "Global workflow fallback"})
		data.TeamSettings[f.public] = domain.TeamSettings{
			TeamID: f.public, Access: "public", DefaultStateID: prefix + "reserved",
			DefaultPriority: 4, TriageEnabled: true,
		}
		data.TeamSettings[prefix+"fallback"] = domain.TeamSettings{TeamID: prefix + "fallback", Access: "public"}
		inherited := data.TeamSettings[child]
		inherited.InheritWorkflowStatuses, inherited.ParentTeamID = true, private
		data.TeamSettings[child] = inherited
		data.States = []domain.WorkflowState{
			{ID: prefix + "global", Name: "Global fallback", Type: "backlog", Default: true},
			{ID: prefix + "reserved", TeamID: f.public, Name: "Reserved backlog", Type: "backlog", Position: 0, Reserved: true},
			{ID: prefix + "backlog", TeamID: f.public, Name: "Normal backlog", Type: "backlog", Position: 1},
			{ID: prefix + "started", TeamID: f.public, Name: "Started", Type: "started", Position: 2, Default: true},
			{ID: prefix + "private-state", TeamID: private, Name: "Inherited private state", Type: "started", Default: true},
		}
		data.Projects = append(data.Projects,
			domain.Project{ID: prefix + "project", Name: "Project option <>&", TeamIDs: []string{f.public}, Description: body},
			domain.Project{ID: prefix + "second-project", Name: "Second project option", TeamIDs: []string{f.public}},
			domain.Project{ID: prefix + "archived-project", Name: "Archived must not be offered", TeamIDs: []string{f.public}, ArchivedAt: &at},
		)
		data.Documents = append(data.Documents, domain.Document{
			ID: prefix + "unrelated-document", Title: "Unrelated body", TeamIDs: []string{f.public},
			Creator: creator, Content: body, ContentData: map[string]any{"body": body}, CreatedAt: at,
		})
		data.IssueTemplates = []domain.IssueTemplate{
			{ID: prefix + "template", TeamID: f.public, Name: "Template fallback", Title: "Exact template title",
				StateID: prefix + "backlog", Priority: 3, AssigneeID: f.principals[2].user.ID, ProjectID: prefix + "project",
				Description: strings.Repeat("Unused template description ", 1000)},
			{ID: prefix + "form-fields", TeamID: f.public, Name: "Nonempty form", FormFields: []domain.TemplateFormField{{ID: prefix + "field", Type: "text", Label: "Not consumed as a body", Description: "Unconsumed form-field description"}}},
			{ID: prefix + "form-type", TeamID: f.public, Name: "Form type", TemplateType: "form"},
			{ID: prefix + "foreign", TeamID: f.public, VisibilityTeamID: private, Name: "Outside binding scope"},
			{ID: prefix + "duplicate", TeamID: f.public, Name: "First matching duplicate"},
			{ID: prefix + "duplicate", TeamID: f.public, Name: "Later matching duplicate form", TemplateType: "form"},
		}
		for index, principal := range f.principals {
			settings := data.UserSettings[principal.user.ID]
			settings.UserID = principal.user.ID
			settings.Language = []string{"zh-CN", "en", "auto", "system", "zh"}[index]
			data.UserSettings[principal.user.ID] = settings
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pair := func(t *testing.T, ctx context.Context, userID string) scaleP21SW174Output {
		t.Helper()
		var outputs []json.RawMessage
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			outputs = append(outputs, scaleP21SW174Call(t, source, ctx, workspace, userID, prefix, f.public, private, child, creator, guest.ID))
		}
		if !bytes.Equal(outputs[0], outputs[1]) {
			t.Fatalf("workflow options/validation differ:\nlegacy=%s\nbounded=%s", outputs[0], outputs[1])
		}
		var result scaleP21SW174Output
		if err := json.Unmarshal(outputs[1], &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, records := range []string{"off", "on"} {
		t.Run("records="+records, func(t *testing.T) {
			t.Setenv("FLOW_INTERNAL_CHAT_RECORDS", records)
			for _, reloaded := range []bool{false, true} {
				t.Run(fmt.Sprint("reloaded=", reloaded), func(t *testing.T) {
					if reloaded {
						if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
							t.Fatal(err)
						}
					}
					if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
					f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
						ctx := scaleP21ChatPointIdentity(f.literalRequest(user, key, nil).Context(), user.ID)
						pair(t, ctx, user.ID)
					})
				})
			}
		})
	}
	t.Run("nonempty-success-witnesses", func(t *testing.T) {
		result := pair(t, scaleP21ChatPointIdentity(t.Context(), f.data.Viewer.ID), f.data.Viewer.ID)
		if result.Error != "" || result.DefaultPriority != 4 || result.RequestState == nil || *result.RequestState != prefix+"backlog" ||
			result.RequestProject != nil || result.TemplatePermitted["form-fields"] || result.TemplatePermitted["form-type"] || result.TemplatePermitted["duplicate"] ||
			!result.TemplatePermitted["template"] || result.Defaults["template"].Action.Title != "Exact template title" ||
			len(result.OfferedProjects) != 2 || result.States[f.public].Default != prefix+"reserved" ||
			!slices.Contains(result.States[prefix+"fallback"].IDs, prefix+"global") ||
			!strings.Contains(result.ProjectsLabel, "Project option <>&") {
			t.Fatal("workflow test did not reach exact defaults/form refusal/triage/fallback/project branches", result)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(scaleP21ChatPointIdentity(t.Context(), f.data.Viewer.ID))
		cancel()
		pair(t, ctx, f.data.Viewer.ID)
	})
	for _, state := range []string{"missing", "closed"} {
		t.Run("owner="+state, func(t *testing.T) {
			ctx := scaleP21ChatPointIdentity(t.Context(), f.data.Viewer.ID)
			if state == "closed" {
				var closeOutput func()
				ctx, closeOutput = beginMetadataOutputReads(ctx)
				closeOutput()
			}
			data := domain.Bootstrap{ViewerRole: "unchanged-destination"}
			err := (&server{store: f.probe}).chatAuthorizedWorkflowHeaders(ctx, workspace, f.data.Viewer.ID, &data)
			if !errors.Is(err, store.ErrBoundedMetadataCoverage) || data.ViewerRole != "unchanged-destination" || f.probe.MetadataWorkUsedForTesting() != 0 {
				t.Fatal("workflow headers escaped missing/closed ownership", state, err)
			}
		})
	}
	t.Run("handed-parent-grant-chain", func(t *testing.T) {
		// Use a real literal read snapshot carrying an inline issue chain.
		// This avoids a test-only context key or a new production handoff API.
		parentID := prefix + "handed-parent"
		issues := []domain.Issue{
			{ID: parentID, Creator: creator, Team: domain.Team{ID: f.public},
				Permissions: []domain.IssuePermission{{SubjectType: "user", SubjectID: guest.ID, Role: "commenter"}}},
			{ID: prefix + "handed-child", Creator: creator, Team: domain.Team{ID: f.public}, ParentID: &parentID},
		}
		rows := f.reader.query(t, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace)
		if !rows.Next() {
			t.Fatal("handed metadata root missing", rows.Err())
		}
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("handed metadata root is not singular", rows.Err())
		}
		rows.Close()
		var root map[string]json.RawMessage
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		encodedIssues, err := json.Marshal(issues)
		if err != nil {
			t.Fatal(err)
		}
		root["issues"] = encodedIssues
		raw, err = json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		update := `UPDATE workspace_states SET data=? WHERE workspace_key=?`
		revision := `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=? AND kind='metadata'`
		if f.reader.config.Driver == "postgres" {
			update = `UPDATE workspace_states SET data=$1 WHERE workspace_key=$2`
			revision = `UPDATE workspace_cache_revisions SET revision=revision+1 WHERE workspace_key=$1 AND kind='metadata'`
		}
		tx, err := f.reader.reader.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(t.Context(), update, string(raw), workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), revision, workspace); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		ctx, closeSnapshot, err := repo.BeginWorkspaceRead(scaleP21ChatPointIdentity(t.Context(), guest.ID), workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer closeSnapshot()
		result := pair(t, ctx, guest.ID)
		if result.Error != "" || result.Roles["handed-child"] != "commenter" {
			t.Fatal("handed bootstrap parent grant was not used by real issueRole", result.Error, result.Roles)
		}
		if !reflect.DeepEqual(result.Languages, []string{"en", "zh"}) {
			t.Fatal("handed language fallback changed", result.Languages)
		}
	})
}
