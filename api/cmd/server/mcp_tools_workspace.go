package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// invokeMCPRoute runs a Flow HTTP handler behind the same workspace
// authorization the router applies to REST calls: API-key scopes, workspace
// roles and permissions, team permissions, feature gates, guest restrictions
// and team-restricted resource visibility. Agent writes therefore cannot do
// anything the equivalent REST request from the same actor could not.
func (s *server) invokeMCPRoute(ctx context.Context, actor mcpActor, method, path string, pathValues map[string]string, input any, handler http.HandlerFunc) (any, error) {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	ctx = context.WithValue(ctx, authUserContextKey{}, actor.User)
	ctx = context.WithValue(ctx, apiKeyContextKey{}, actor.APIKey)
	ctx = context.WithValue(ctx, workspaceKeyContextKey{}, actor.WorkspaceKey)
	ctx = store.ContextWithActor(ctx, actor.User)
	request := httptest.NewRequest(method, "http://flow.internal"+path, body).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	// Keys minted by users carry an ID; the in-app agent's session actor uses
	// a synthetic read/write key whose scopes were already checked upstream.
	if actor.APIKey.ID != "" && !apiKeyAllowsRequest(request, actor.APIKey) {
		return nil, fmt.Errorf("this API key's scopes do not allow this action")
	}
	response := httptest.NewRecorder()
	if !s.authorizeWorkspaceRequest(response, request, actor.User) {
		return mcpHandlerResult(response)
	}
	handler(response, request)
	return mcpHandlerResult(response)
}

func mcpHandlerResult(response *httptest.ResponseRecorder) (any, error) {
	if response.Code >= 400 {
		var object map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &object)
		if message, ok := object["error"].(string); ok {
			return nil, fmt.Errorf("%s", message)
		}
		return nil, fmt.Errorf("Flow API returned HTTP %d", response.Code)
	}
	if response.Body.Len() == 0 {
		return map[string]any{"ok": true}, nil
	}
	var result any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func mcpWorkspaceURL(workspace string, args map[string]any, segments ...string) string {
	path := "/" + url.PathEscape(workspace)
	for _, segment := range segments {
		path += "/" + url.PathEscape(segment)
	}
	return strings.TrimRight(stringArg(args, "__flowBaseURL"), "/") + path
}

func pathID(value string) string { return url.PathEscape(value) }

// ---------------------------------------------------------------- teams

type mcpTeamState struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type mcpTeamLabel struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

func (s *server) saveMCPTeam(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	creating := id == ""
	var team domain.Team
	var err error
	if !creating {
		if team, err = mcpFindTeam(data, id); err != nil {
			return nil, err
		}
	} else {
		if stringArg(args, "name") == "" || stringArg(args, "key") == "" {
			return nil, fmt.Errorf("name and key are required when creating a team")
		}
		if apiKeyTeamRestrictionSelected(actor.APIKey) {
			return nil, fmt.Errorf("a team-restricted API key cannot create teams")
		}
	}
	// Resolve every reference before the first write so a typo cannot leave a
	// half-configured team behind.
	members := []string{}
	for _, query := range stringsArg(args, "members") {
		user, err := mcpFindUser(data, query)
		if err != nil {
			return nil, err
		}
		members = append(members, user.ID)
	}
	removeMembers := []string{}
	for _, query := range stringsArg(args, "removeMembers") {
		user, err := mcpFindUser(data, query)
		if err != nil {
			return nil, err
		}
		removeMembers = append(removeMembers, user.ID)
	}
	owners := []string{}
	for _, query := range stringsArg(args, "owners") {
		user, err := mcpFindUser(data, query)
		if err != nil {
			return nil, err
		}
		owners = append(owners, user.ID)
	}
	removeOwners := []string{}
	for _, query := range stringsArg(args, "removeOwners") {
		user, err := mcpFindUser(data, query)
		if err != nil {
			return nil, err
		}
		removeOwners = append(removeOwners, user.ID)
	}
	var states []mcpTeamState
	if raw, ok := args["states"]; ok {
		if err := jsonClone(raw, &states); err != nil {
			return nil, err
		}
		for _, state := range states {
			if strings.TrimSpace(state.Name) == "" || !validWorkflowType(state.Type) {
				return nil, fmt.Errorf("each state needs a name and a type of backlog, unstarted, started, completed, or canceled")
			}
		}
	}
	var labels []mcpTeamLabel
	if raw, ok := args["labels"]; ok {
		if err := jsonClone(raw, &labels); err != nil {
			return nil, err
		}
		for _, label := range labels {
			if strings.TrimSpace(label.Name) == "" {
				return nil, fmt.Errorf("each label needs a name")
			}
		}
	}
	parentID, copyFromID := "", ""
	if creating {
		if value := stringArg(args, "parentTeam"); value != "" {
			parent, err := mcpFindTeam(data, value)
			if err != nil {
				return nil, err
			}
			parentID = parent.ID
		}
		if value := stringArg(args, "copySettingsFrom"); value != "" {
			source, err := mcpFindTeam(data, value)
			if err != nil {
				return nil, err
			}
			copyFromID = source.ID
		}
	}
	ws := actor.WorkspaceKey
	if creating {
		input := map[string]any{"name": stringArg(args, "name"), "key": stringArg(args, "key"), "color": stringArg(args, "color"), "icon": stringArg(args, "icon"), "private": boolArg(args, "private"), "parentTeamId": parentID, "copyFromTeamId": copyFromID, "timezone": stringArg(args, "timezone")}
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/workspaces/"+pathID(ws)+"/teams", map[string]string{"workspaceKey": ws}, input, s.createTeam)
		if err != nil {
			return nil, err
		}
		if err := jsonClone(result, &team); err != nil {
			return nil, err
		}
	} else if hasAnyArg(args, "name", "key", "color", "icon", "private") {
		input := map[string]any{}
		for _, key := range []string{"name", "key", "color", "icon"} {
			if value, ok := args[key].(string); ok {
				input[key] = value
			}
		}
		if hasBoolArg(args, "private") {
			input["private"] = boolArg(args, "private")
		}
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/workspaces/"+pathID(ws)+"/teams/"+pathID(team.ID), map[string]string{"workspaceKey": ws, "teamId": team.ID}, input, s.updateTeam)
		if err != nil {
			return nil, err
		}
		if err := jsonClone(result, &team); err != nil {
			return nil, err
		}
	}
	teamPath := "/api/teams/" + pathID(team.ID)
	teamValues := map[string]string{"id": team.ID}
	settings := map[string]any{}
	if value, ok := args["description"].(string); ok {
		settings["description"] = value
	}
	if value := stringArg(args, "timezone"); value != "" && !creating {
		settings["timezone"] = value
	}
	if hasBoolArg(args, "triageEnabled") {
		settings["triageEnabled"] = boolArg(args, "triageEnabled")
	}
	if len(settings) > 0 {
		if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, teamPath+"/settings", teamValues, settings, s.updateStructuredTeamSettings); err != nil {
			return nil, err
		}
	}
	if hasBoolArg(args, "cyclesEnabled") || hasNumberArg(args, "cycleDurationWeeks") {
		cycles := domain.CycleSettingsMutationInput{}
		if hasBoolArg(args, "cyclesEnabled") {
			value := boolArg(args, "cyclesEnabled")
			cycles.Enabled = &value
		}
		if hasNumberArg(args, "cycleDurationWeeks") {
			value := intArg(args, "cycleDurationWeeks", 2)
			cycles.DurationWeeks = &value
		}
		if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, teamPath+"/cycle-settings", teamValues, cycles, s.updateCycleSettings); err != nil {
			return nil, err
		}
	}
	// `members` adds people: anyone already on the team (owners included)
	// keeps their role. Owner changes are explicit (`owners`/`removeOwners`),
	// so listing the current owner among the members never demotes them.
	roles := s.mcpTeamRoles(ctx, data, team.ID)
	setMembership := func(userID string, member bool, role string) error {
		path := "/api/workspaces/" + pathID(ws) + "/teams/" + pathID(team.ID) + "/members/" + pathID(userID)
		_, err := s.invokeMCPRoute(ctx, actor, http.MethodPut, path, map[string]string{"workspaceKey": ws, "teamId": team.ID, "userId": userID}, map[string]any{"member": member, "role": role}, s.updateTeamMember)
		if err == nil {
			if member {
				roles[userID] = role
			} else {
				delete(roles, userID)
			}
		}
		return err
	}
	addedMembers := []string{}
	for _, userID := range members {
		if _, ok := roles[userID]; ok || creating && userID == actor.User.ID {
			continue // already on the team (the creator is its owner)
		}
		if err := setMembership(userID, true, "member"); err != nil {
			return nil, err
		}
		addedMembers = append(addedMembers, userID)
	}
	// Promote before demoting so a hand-over from one owner to another
	// never leaves the team without an owner in between.
	for _, userID := range owners {
		if roles[userID] == "owner" || creating && userID == actor.User.ID {
			continue
		}
		if err := setMembership(userID, true, "owner"); err != nil {
			return nil, err
		}
	}
	for _, userID := range removeOwners {
		if roles[userID] != "owner" {
			continue
		}
		if err := setMembership(userID, true, "member"); err != nil {
			return nil, err
		}
	}
	for _, userID := range removeMembers {
		if err := setMembership(userID, false, "member"); err != nil {
			return nil, err
		}
	}
	createdStates := []any{}
	for _, state := range states {
		input := domain.WorkflowStateMutationInput{Name: &state.Name, Type: &state.Type}
		if state.Color != "" {
			input.Color = &state.Color
		}
		if state.Description != "" {
			input.Description = &state.Description
		}
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, teamPath+"/states", teamValues, input, s.createWorkflowState)
		if err != nil {
			return nil, err
		}
		createdStates = append(createdStates, result)
	}
	createdLabels := []any{}
	for _, label := range labels {
		input := domain.IssueLabelMutationInput{Name: &label.Name}
		if label.Color != "" {
			input.Color = &label.Color
		}
		if label.Description != "" {
			input.Description = &label.Description
		}
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, teamPath+"/labels", teamValues, input, s.createTeamLabel)
		if err != nil {
			return nil, err
		}
		createdLabels = append(createdLabels, result)
	}
	receipt := map[string]any{"id": team.ID, "key": team.Key, "name": team.Name, "created": creating, "url": mcpWorkspaceURL(ws, args, "team", team.Key, "all")}
	if len(settings) > 0 {
		receipt["settings"] = settings
	}
	if len(addedMembers) > 0 {
		receipt["addedMembers"] = addedMembers
	}
	if len(owners) > 0 {
		receipt["owners"] = owners
	}
	if len(removeOwners) > 0 {
		receipt["removedOwners"] = removeOwners
	}
	if len(removeMembers) > 0 {
		receipt["removedMembers"] = removeMembers
	}
	if len(createdStates) > 0 {
		receipt["states"] = createdStates
	}
	if len(createdLabels) > 0 {
		receipt["labels"] = createdLabels
	}
	return receipt, nil
}

// ---------------------------------------------------------------- labels

func mcpFindLabel(data domain.Bootstrap, query string) (domain.IssueLabel, error) {
	matches := []domain.IssueLabel{}
	for _, item := range data.Labels {
		if item.ID == query {
			return item, nil
		}
		if strings.EqualFold(item.Name, query) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return domain.IssueLabel{}, fmt.Errorf("several labels are named %q; pass the label id", query)
	}
	return domain.IssueLabel{}, fmt.Errorf("label %q not found", query)
}

func mcpLabelReceipt(result any) (any, error) {
	var label domain.IssueLabel
	if err := jsonClone(result, &label); err != nil {
		return nil, err
	}
	resource := label.ResourceType
	if resource == "" {
		resource = "issue"
	}
	receipt := map[string]any{"id": label.ID, "name": label.Name, "color": label.Color, "description": label.Description, "type": resource, "archived": label.ArchivedAt != nil}
	if !labelScopeIsWorkspace(label.Scope) {
		receipt["teamId"] = label.Scope
	}
	if label.GroupID != "" {
		receipt["groupId"] = label.GroupID
	}
	return receipt, nil
}

func (s *server) saveMCPLabel(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	archivedAt := func() *string {
		if !hasBoolArg(args, "archived") {
			return nil
		}
		value := ""
		if boolArg(args, "archived") {
			value = time.Now().UTC().Format(time.RFC3339)
		}
		return &value
	}
	optional := func(key string) *string {
		if value, ok := args[key].(string); ok {
			return &value
		}
		return nil
	}
	if id := stringArg(args, "id"); id != "" {
		label, err := mcpFindLabel(data, id)
		if err != nil {
			return nil, err
		}
		if hasAnyArg(args, "type", "team", "parent") {
			return nil, fmt.Errorf("type, team, and parent can only be set when creating a label")
		}
		var result any
		if labelScopeIsWorkspace(label.Scope) {
			input := labelInput{Name: optional("name"), Description: optional("description"), Color: optional("color"), ArchivedAt: archivedAt()}
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/labels/"+pathID(label.ID), map[string]string{"id": label.ID}, input, s.updateWorkspaceLabel)
		} else {
			input := domain.IssueLabelMutationInput{Name: optional("name"), Description: optional("description"), Color: optional("color"), ArchivedAt: archivedAt()}
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/teams/"+pathID(label.Scope)+"/labels/"+pathID(label.ID), map[string]string{"id": label.Scope, "labelId": label.ID}, input, s.updateTeamLabel)
		}
		if err != nil {
			return nil, err
		}
		return mcpLabelReceipt(result)
	}
	name := stringArg(args, "name")
	if name == "" {
		return nil, fmt.Errorf("name is required when creating a label")
	}
	resource := stringArg(args, "type")
	if resource == "" {
		resource = "issue"
	}
	groupID := ""
	if parent := stringArg(args, "parent"); parent != "" {
		for _, group := range data.LabelGroups {
			if equalFoldAny(parent, group.ID, group.Name) && group.ResourceType == resource && group.ArchivedAt == nil {
				groupID = group.ID
				break
			}
		}
		if groupID == "" {
			return nil, fmt.Errorf("parent label group %q not found", parent)
		}
	}
	var result any
	var err error
	if teamQuery := stringArg(args, "team"); teamQuery != "" {
		team, err := mcpFindTeam(data, teamQuery)
		if err != nil {
			return nil, err
		}
		if resource == "initiative" {
			return nil, fmt.Errorf("initiative labels are workspace-wide and cannot belong to a team")
		}
		input := domain.IssueLabelMutationInput{Name: &name, Description: optional("description"), Color: optional("color"), ResourceType: &resource}
		if groupID != "" {
			input.GroupID = &groupID
		}
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/teams/"+pathID(team.ID)+"/labels", map[string]string{"id": team.ID}, input, s.createTeamLabel)
		if err != nil {
			return nil, err
		}
	} else {
		input := labelInput{Name: &name, Description: optional("description"), Color: optional("color"), ResourceType: &resource}
		if groupID != "" {
			input.GroupID = &groupID
		}
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/labels", nil, input, s.createWorkspaceLabel)
		if err != nil {
			return nil, err
		}
	}
	return mcpLabelReceipt(result)
}

func (s *server) deleteMCPLabel(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	label, err := mcpFindLabel(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	if labelScopeIsWorkspace(label.Scope) {
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/labels/"+pathID(label.ID), map[string]string{"id": label.ID}, nil, s.deleteWorkspaceLabel)
	} else {
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/teams/"+pathID(label.Scope)+"/labels/"+pathID(label.ID), map[string]string{"id": label.Scope, "labelId": label.ID}, nil, s.deleteTeamLabel)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": label.ID, "name": label.Name}, nil
}

// ---------------------------------------------------------------- saved views

var mcpViewFilterLabels = map[string]string{
	"status": "Status", "assignee": "Assignee", "agent": "Agent", "agentSession": "Agent Session", "creator": "Creator",
	"priority": "Priority", "labels": "Labels", "relations": "Relations", "suggestedLabel": "Suggested label", "triageIntelligence": "Triage Intelligence", "dates": "Dates",
	"projectMilestone": "Project milestone", "project": "Project", "projectProperties": "Project properties", "initiative": "Initiative",
	"cycle": "Cycle", "addedToCycle": "Added to cycle", "releases": "Releases", "customers": "Customers", "subscribers": "Subscribers",
	"externalSource": "External source", "autoClosed": "Auto-closed", "content": "Content", "links": "Links", "template": "Template",
}

var mcpPriorityNames = []string{"No priority", "Urgent", "High", "Medium", "Low"}

func mcpNoneValue(value string) bool {
	return value == "" || strings.EqualFold(value, "none") || strings.EqualFold(value, "null")
}

// mcpViewFilterValue resolves one agent-provided filter value to the id and
// label the issue filter bar persists for that field.
func mcpViewFilterValue(data domain.Bootstrap, teamID, field, value string) ([][2]string, error) {
	switch field {
	case "status":
		matches := [][2]string{}
		for _, state := range data.States {
			if (state.TeamID == "" || teamID == "" || state.TeamID == teamID) && equalFoldAny(value, state.ID, state.Name) {
				return [][2]string{{state.ID, state.Name}}, nil
			}
		}
		for _, state := range data.States {
			if (state.TeamID == "" || teamID == "" || state.TeamID == teamID) && strings.EqualFold(value, state.Type) {
				matches = append(matches, [2]string{state.ID, state.Name})
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("issue status %q not found", value)
		}
		return matches, nil
	case "assignee", "creator", "subscribers", "agent":
		if mcpNoneValue(value) && field != "creator" && field != "subscribers" {
			return [][2]string{{"", "No " + strings.ToLower(mcpViewFilterLabels[field])}}, nil
		}
		user, err := mcpFindUser(data, value)
		if err != nil {
			return nil, err
		}
		name := user.DisplayName
		if name == "" {
			name = user.Name
		}
		return [][2]string{{user.ID, name}}, nil
	case "priority":
		for index, name := range mcpPriorityNames {
			if value == strconv.Itoa(index) || strings.EqualFold(value, name) || index == 0 && mcpNoneValue(value) {
				return [][2]string{{strconv.Itoa(index), name}}, nil
			}
		}
		return nil, fmt.Errorf("priority %q not found; use 0-4 or No priority, Urgent, High, Medium, Low", value)
	case "labels":
		for _, label := range data.Labels {
			if (label.ResourceType == "" || label.ResourceType == "issue") && equalFoldAny(value, label.ID, label.Name) {
				return [][2]string{{label.ID, label.Name}}, nil
			}
		}
		return nil, fmt.Errorf("label %q not found", value)
	case "project":
		if mcpNoneValue(value) {
			return [][2]string{{"", "No project"}}, nil
		}
		project, err := mcpFindProject(data, value)
		if err != nil {
			return nil, err
		}
		return [][2]string{{project.ID, project.Name}}, nil
	case "cycle":
		if mcpNoneValue(value) {
			return [][2]string{{"", "No cycle"}}, nil
		}
		for _, cycle := range data.Cycles {
			if equalFoldAny(value, cycle.ID, cycle.Name, strconv.Itoa(cycle.Number)) {
				label := cycle.Name
				if label == "" {
					label = "Cycle " + strconv.Itoa(cycle.Number)
				}
				return [][2]string{{cycle.ID, label}}, nil
			}
		}
		return nil, fmt.Errorf("cycle %q not found", value)
	case "initiative":
		initiative, err := mcpFindInitiative(data, value)
		if err != nil {
			return nil, err
		}
		return [][2]string{{initiative.ID, initiative.Name}}, nil
	default:
		return [][2]string{{value, value}}, nil
	}
}

func mcpViewFilters(data domain.Bootstrap, teamID string, raw any) ([]map[string]any, error) {
	var filters []struct {
		Field    string   `json:"field"`
		Operator string   `json:"operator"`
		Values   []string `json:"values"`
	}
	if err := jsonClone(raw, &filters); err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for index, filter := range filters {
		fieldLabel, ok := mcpViewFilterLabels[filter.Field]
		if !ok {
			return nil, fmt.Errorf("unsupported filter field %q", filter.Field)
		}
		operator, values, err := mcpViewFilterCondition(data, teamID, filter.Field, filter.Operator, filter.Values)
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": fmt.Sprintf("filter_%d_%s", index+1, filter.Field), "field": filter.Field, "fieldLabel": fieldLabel, "operator": operator, "value": values[0]["value"], "valueLabel": values[0]["valueLabel"], "values": values})
	}
	return result, nil
}

// mcpViewFilterCondition validates one condition's operator and resolves its
// values: is / isNot everywhere, includesAll / excludesAll for labels (Linear).
func mcpViewFilterCondition(data domain.Bootstrap, teamID, field, operator string, raw []string) (string, []map[string]any, error) {
	if operator == "" {
		operator = "is"
	}
	if operator != "is" && operator != "isNot" && !((operator == "includesAll" || operator == "excludesAll") && field == "labels") {
		return "", nil, fmt.Errorf("filter operator must be is or isNot (includesAll / excludesAll for labels)")
	}
	if len(raw) == 0 {
		return "", nil, fmt.Errorf("filter %q needs at least one value", field)
	}
	values := []map[string]any{}
	for _, value := range raw {
		resolved, err := mcpViewFilterValue(data, teamID, field, strings.TrimSpace(value))
		if err != nil {
			return "", nil, err
		}
		for _, pair := range resolved {
			values = append(values, map[string]any{"value": pair[0], "valueLabel": pair[1]})
		}
	}
	return operator, values, nil
}

type mcpAdvancedFilterNode struct {
	Conjunction string                  `json:"conjunction"`
	Items       []mcpAdvancedFilterNode `json:"items"`
	Field       string                  `json:"field"`
	Operator    string                  `json:"operator"`
	Values      []string                `json:"values"`
}

// mcpAdvancedViewFilters turns agent-provided filter trees into the web
// client's advanced-filter chips ({field: "advanced", tree}); at most three
// levels (top level, group, nested group), conditions use the same fields.
func mcpAdvancedViewFilters(data domain.Bootstrap, teamID string, raw any) ([]map[string]any, error) {
	var trees []mcpAdvancedFilterNode
	if err := jsonClone(raw, &trees); err != nil {
		return nil, err
	}
	sequence := 0
	var convert func(node mcpAdvancedFilterNode, depth int) (map[string]any, error)
	convert = func(node mcpAdvancedFilterNode, depth int) (map[string]any, error) {
		sequence++
		conjunction := node.Conjunction
		if conjunction == "" {
			conjunction = "and"
		}
		if conjunction != "and" && conjunction != "or" {
			return nil, fmt.Errorf("advanced filter conjunction must be and or or")
		}
		if depth > 3 {
			return nil, fmt.Errorf("advanced filters nest at most three levels")
		}
		items := []map[string]any{}
		for _, item := range node.Items {
			if item.Field == "" {
				group, err := convert(item, depth+1)
				if err != nil {
					return nil, err
				}
				items = append(items, group)
				continue
			}
			fieldLabel, ok := mcpViewFilterLabels[item.Field]
			if !ok {
				return nil, fmt.Errorf("unsupported filter field %q", item.Field)
			}
			operator, values, err := mcpViewFilterCondition(data, teamID, item.Field, item.Operator, item.Values)
			if err != nil {
				return nil, err
			}
			sequence++
			items = append(items, map[string]any{"id": fmt.Sprintf("condition_%d", sequence), "field": item.Field, "fieldLabel": fieldLabel, "operator": operator, "values": values})
		}
		return map[string]any{"id": fmt.Sprintf("group_%d", sequence), "conjunction": conjunction, "items": items}, nil
	}
	result := []map[string]any{}
	for index, tree := range trees {
		converted, err := convert(tree, 1)
		if err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"id": fmt.Sprintf("advanced_%d", index+1), "field": "advanced", "fieldLabel": "Advanced filter", "operator": "is", "value": "", "valueLabel": "", "values": []any{}, "tree": converted})
	}
	return result, nil
}

// mcpPlainFilters keeps a view's plain chips when an agent replaces only its
// advanced filters.
func mcpPlainFilters(raw json.RawMessage) []map[string]any {
	var chips []map[string]any
	if json.Unmarshal(raw, &chips) != nil {
		return []map[string]any{}
	}
	kept := []map[string]any{}
	for _, chip := range chips {
		if chip["field"] != "advanced" {
			kept = append(kept, chip)
		}
	}
	return kept
}

// mcpExistingAdvancedFilters keeps a view's advanced chips when an agent
// replaces only its plain filters.
func mcpExistingAdvancedFilters(raw json.RawMessage) []map[string]any {
	var chips []map[string]any
	if json.Unmarshal(raw, &chips) != nil {
		return nil
	}
	kept := []map[string]any{}
	for _, chip := range chips {
		if chip["field"] == "advanced" {
			kept = append(kept, chip)
		}
	}
	return kept
}

func mcpFindView(data domain.Bootstrap, query string) (domain.SavedView, error) {
	for _, view := range data.SavedViews {
		if view.Scope == "personal" && view.OwnerID != data.Viewer.ID || !mcpTeamVisible(data, view.TeamID) {
			continue
		}
		if equalFoldAny(query, view.ID, view.SlugID, view.Name) {
			return view, nil
		}
	}
	return domain.SavedView{}, fmt.Errorf("view %q not found", query)
}

func (s *server) saveMCPView(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.SavedView
	var err error
	if id != "" {
		if current, err = mcpFindView(data, id); err != nil {
			return nil, err
		}
		if current.Resource != "" && current.Resource != "issues" {
			return nil, fmt.Errorf("only issue views can be edited with save_view")
		}
	} else if stringArg(args, "name") == "" {
		return nil, fmt.Errorf("name is required when creating a view")
	}
	input := domain.SavedViewMutationInput{}
	for key, target := range map[string]**string{"name": &input.Name, "description": &input.Description, "icon": &input.Icon, "color": &input.Color} {
		if value, ok := args[key].(string); ok {
			*target = &value
		}
	}
	teamID := current.TeamID
	if value, present := nullableStringArg(args, "team"); present {
		teamID = ""
		if value != "" {
			team, err := mcpFindTeam(data, value)
			if err != nil {
				return nil, err
			}
			teamID = team.ID
		}
		input.TeamID = &teamID
	}
	scope := stringArg(args, "scope")
	if scope == "" && input.TeamID != nil {
		scope = "workspace"
		if teamID != "" {
			scope = "team"
		}
	}
	if scope == "" && id == "" {
		scope = "workspace"
	}
	if scope != "" {
		if scope == "team" && teamID == "" {
			return nil, fmt.Errorf("team is required for a team view")
		}
		input.Scope = &scope
	}
	if value, present := nullableStringArg(args, "project"); present {
		projectID := ""
		if value != "" {
			project, err := mcpFindProject(data, value)
			if err != nil {
				return nil, err
			}
			projectID = project.ID
		}
		input.ProjectID = &projectID
	}
	if layout := stringArg(args, "layout"); layout != "" {
		input.View = &layout
	} else if id == "" {
		layout := "all"
		input.View = &layout
	}
	if id == "" {
		resource, owner := "issues", actor.User.ID
		input.Resource, input.OwnerID = &resource, &owner
	}
	rawFilters, hasFilters := args["filters"]
	rawAdvanced, hasAdvanced := args["advancedFilters"]
	if hasFilters || hasAdvanced {
		filters := []map[string]any{}
		if hasFilters {
			if filters, err = mcpViewFilters(data, teamID, rawFilters); err != nil {
				return nil, err
			}
		} else if id != "" {
			filters = mcpPlainFilters(current.Filters)
		}
		if hasAdvanced {
			advanced, err := mcpAdvancedViewFilters(data, teamID, rawAdvanced)
			if err != nil {
				return nil, err
			}
			filters = append(filters, advanced...)
		} else if id != "" {
			filters = append(filters, mcpExistingAdvancedFilters(current.Filters)...)
		}
		if input.Filters, err = json.Marshal(filters); err != nil {
			return nil, err
		}
	} else if id == "" {
		input.Filters = json.RawMessage("[]")
	}
	var result any
	if id == "" {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/views", nil, input, s.createSavedView)
	} else {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/views/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateSavedView)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.SavedView
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	receipt := map[string]any{"id": saved.ID, "slugId": saved.SlugID, "name": saved.Name, "scope": saved.Scope, "layout": saved.View, "filters": saved.Filters}
	pathKey := saved.SlugID
	if pathKey == "" {
		pathKey = saved.ID
	}
	receipt["url"] = mcpWorkspaceURL(actor.WorkspaceKey, args, "view", pathKey)
	if saved.TeamID != "" {
		if team, err := mcpFindTeam(data, saved.TeamID); err == nil {
			receipt["team"] = map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}
			receipt["url"] = mcpWorkspaceURL(actor.WorkspaceKey, args, "team", team.Key, "view", pathKey)
		}
	}
	if saved.ProjectID != "" {
		receipt["projectId"] = saved.ProjectID
	}
	if hasBoolArg(args, "shared") {
		if boolArg(args, "shared") {
			shared, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/views/"+pathID(saved.ID)+"/share", map[string]string{"id": saved.ID}, map[string]any{}, s.shareSavedView)
			if err != nil {
				return nil, err
			}
			if object, ok := shared.(map[string]any); ok {
				receipt["shareUrl"] = strings.TrimRight(stringArg(args, "__flowBaseURL"), "/") + fmt.Sprint(object["url"])
			}
			receipt["shared"] = true
		} else {
			if _, err := s.invokeMCPRoute(ctx, actor, http.MethodDelete, "/api/views/"+pathID(saved.ID)+"/share", map[string]string{"id": saved.ID}, nil, s.unshareSavedView); err != nil {
				return nil, err
			}
			receipt["shared"] = false
		}
	} else {
		receipt["shared"] = saved.ShareToken != ""
	}
	return receipt, nil
}

// ---------------------------------------------------------------- issues

func (s *server) deleteMCPIssue(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	path := "/api/issue-records/" + pathID(issue.ID)
	if _, err := s.invokeMCPRoute(ctx, actor, http.MethodDelete, path, map[string]string{"id": issue.ID}, nil, s.issueRecordAlias(s.deleteIssue)); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "restorable": true}, nil
}

func (s *server) triageMCPIssue(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	issue, err := mcpFindIssue(data, stringArg(args, "id"))
	if err != nil {
		return nil, err
	}
	if !data.TeamSettings[issue.Team.ID].TriageEnabled {
		return nil, fmt.Errorf("team %s does not use triage", issue.Team.Key)
	}
	if issue.State.Type != "backlog" || issue.TriagedAt != nil || issue.ArchivedAt != nil {
		return nil, fmt.Errorf("issue %s is not in triage", issue.Identifier)
	}
	teamStates := slices.DeleteFunc(slices.Clone(data.States), func(state domain.WorkflowState) bool {
		return state.TeamID != "" && state.TeamID != issue.Team.ID
	})
	slices.SortStableFunc(teamStates, func(a, b domain.WorkflowState) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		}
		return 0
	})
	canceled := func(duplicate bool) *domain.WorkflowState {
		var fallback *domain.WorkflowState
		for index := range teamStates {
			state := &teamStates[index]
			if state.Type != "canceled" {
				continue
			}
			named := strings.Contains(strings.ToLower(state.Name), "duplicate")
			if named == duplicate {
				return state
			}
			if fallback == nil {
				fallback = state
			}
		}
		return fallback
	}
	input := domain.IssueUpdateInput{}
	var related domain.Issue
	action := stringArg(args, "action")
	switch action {
	case "accept":
		var target *domain.WorkflowState
		if query := stringArg(args, "state"); query != "" {
			stateID, err := resolveStateID(data, issue.Team.ID, query)
			if err != nil {
				return nil, err
			}
			for index := range teamStates {
				if teamStates[index].ID == stateID {
					target = &teamStates[index]
				}
			}
		} else {
			defaultID := data.TeamSettings[issue.Team.ID].DefaultStateID
			for _, kind := range []string{"default", "unstarted", "started", "any"} {
				for index := range teamStates {
					state := &teamStates[index]
					if state.Type != "backlog" && (kind == "default" && state.ID == defaultID || kind == state.Type || kind == "any") {
						target = state
						break
					}
				}
				if target != nil {
					break
				}
			}
		}
		if target == nil || target.Type == "backlog" {
			return nil, fmt.Errorf("accepting an issue moves it out of triage; choose a non-backlog state")
		}
		input.StateID = &target.ID
		if hasNumberArg(args, "priority") {
			value := intArg(args, "priority", 0)
			input.Priority = &value
		}
	case "decline":
		state := canceled(false)
		if state == nil {
			return nil, fmt.Errorf("team %s has no canceled state", issue.Team.Key)
		}
		input.StateID = &state.ID
	case "duplicate":
		if related, err = mcpFindIssue(data, stringArg(args, "duplicateOf")); err != nil {
			if stringArg(args, "duplicateOf") == "" {
				return nil, fmt.Errorf("duplicateOf is required to mark an issue as a duplicate")
			}
			return nil, err
		}
		if related.ID == issue.ID {
			return nil, fmt.Errorf("an issue cannot duplicate itself")
		}
		state := canceled(true)
		if state == nil {
			return nil, fmt.Errorf("team %s has no canceled state", issue.Team.Key)
		}
		input.StateID = &state.ID
	case "snooze":
		until, err := mcpFutureDate(stringArg(args, "snoozedUntil"))
		if err != nil || stringArg(args, "snoozedUntil") == "" {
			return nil, fmt.Errorf("snoozedUntil must be a future ISO date/time or ISO-8601 duration (e.g. P1W)")
		}
		value := until.UTC().Format(time.RFC3339)
		input.SnoozedUntil = &value
	default:
		return nil, fmt.Errorf("action must be accept, decline, duplicate, or snooze")
	}
	path := "/api/issue-records/" + pathID(issue.ID)
	values := map[string]string{"id": issue.ID}
	if action == "duplicate" {
		relation := map[string]string{"type": "duplicate", "relatedIssueId": related.ID}
		if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, path+"/relations", values, relation, s.issueRecordAlias(s.createRelation)); err != nil {
			return nil, err
		}
	}
	if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, path, values, input, s.updateIssueRecord); err != nil {
		return nil, err
	}
	if body := stringArg(args, "comment"); body != "" {
		if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, path+"/comments", values, domain.CommentCreateInput{Body: body}, s.issueRecordAlias(s.createComment)); err != nil {
			return nil, err
		}
	}
	updated, err := s.store.IssueRecord(ctx, data.Workspace.URLKey, issue.ID)
	if err != nil {
		return nil, err
	}
	receipt := map[string]any{"id": updated.ID, "identifier": updated.Identifier, "title": updated.Title, "action": action, "state": map[string]any{"id": updated.State.ID, "name": updated.State.Name, "type": updated.State.Type}, "priority": updated.Priority, "url": mcpIssueURL(data.Workspace.URLKey, updated.Identifier, args)}
	if updated.SnoozedUntil != nil {
		receipt["snoozedUntil"] = updated.SnoozedUntil
	}
	if action == "duplicate" {
		receipt["duplicateOf"] = map[string]any{"id": related.ID, "identifier": related.Identifier}
	}
	return receipt, nil
}

// ---------------------------------------------------------------- reactions

func (s *server) saveMCPReaction(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	emoji := stringArg(args, "emoji")
	if emoji == "" {
		return nil, fmt.Errorf("emoji is required")
	}
	targets := 0
	for _, key := range []string{"issueId", "commentId", "statusUpdateId"} {
		if stringArg(args, key) != "" {
			targets++
		}
	}
	if targets != 1 {
		return nil, fmt.Errorf("provide exactly one of issueId, commentId, or statusUpdateId")
	}
	for _, key := range []string{"issueId", "commentId"} {
		if value := stringArg(args, key); value != "" {
			if err := s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"id": value}); err != nil {
				return nil, err
			}
		}
	}
	var reactions map[string][]string
	var path, kind, targetID string
	values := map[string]string{}
	var handler http.HandlerFunc
	switch {
	case stringArg(args, "issueId") != "":
		issue, err := mcpFindIssue(data, stringArg(args, "issueId"))
		if err != nil {
			return nil, err
		}
		kind, targetID, reactions = "issue", issue.ID, issue.Reactions
		path, values["id"], handler = "/api/issue-records/"+pathID(issue.ID)+"/reactions", issue.ID, s.issueRecordAlias(s.toggleIssueReaction)
	case stringArg(args, "statusUpdateId") != "":
		id, only := stringArg(args, "statusUpdateId"), stringArg(args, "statusUpdateType")
		for projectID, updates := range data.ProjectUpdates {
			for _, update := range updates {
				if update.ID == id && only != "initiative" {
					if _, err := mcpFindProject(data, projectID); err != nil {
						return nil, fmt.Errorf("status update %q not found", id)
					}
					kind, targetID, reactions = "projectUpdate", id, update.Reactions
					path, handler = "/api/projects/"+pathID(projectID)+"/updates/"+pathID(id)+"/reactions", s.toggleProjectUpdateReaction
					values["id"], values["updateId"] = projectID, id
				}
			}
		}
		for initiativeID, updates := range data.InitiativeUpdates {
			for _, update := range updates {
				if update.ID == id && only != "project" && handler == nil {
					if _, err := mcpFindInitiative(data, initiativeID); err != nil {
						return nil, fmt.Errorf("status update %q not found", id)
					}
					kind, targetID, reactions = "initiativeUpdate", id, update.Reactions
					path, handler = "/api/initiatives/"+pathID(initiativeID)+"/updates/"+pathID(id)+"/reactions", s.toggleInitiativeUpdateReaction
					values["id"], values["updateId"] = initiativeID, id
				}
			}
		}
		if handler == nil {
			return nil, fmt.Errorf("status update %q not found", id)
		}
	default:
		id := stringArg(args, "commentId")
		for _, project := range data.Projects {
			for _, comment := range project.Comments {
				if comment.ID == id {
					kind, targetID, reactions = "projectComment", id, comment.Reactions
					path, handler = "/api/projects/"+pathID(project.ID)+"/comments/"+pathID(id)+"/reactions", s.toggleProjectCommentReaction
					values["id"], values["commentId"] = project.ID, id
				}
			}
		}
		for _, initiative := range data.Initiatives {
			for _, comment := range initiative.Comments {
				if comment.ID == id && handler == nil {
					kind, targetID, reactions = "initiativeComment", id, comment.Reactions
					path, handler = "/api/initiatives/"+pathID(initiative.ID)+"/comments/"+pathID(id)+"/reactions", s.toggleInitiativeCommentReaction
					values["id"], values["commentId"] = initiative.ID, id
				}
			}
		}
		for resource, comments := range data.Comments {
			index := slices.IndexFunc(comments, func(comment domain.Comment) bool { return comment.ID == id })
			if index < 0 || handler != nil {
				continue
			}
			if _, err := mcpFindIssue(data, resource); err == nil {
				kind, targetID, reactions = "issueComment", id, comments[index].Reactions
				path, handler = "/api/issue-records/"+pathID(resource)+"/comments/"+pathID(id)+"/reactions", s.issueRecordAlias(s.toggleCommentReaction)
			} else if slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == resource }) {
				kind, targetID, reactions = "documentComment", id, comments[index].Reactions
				path, handler = "/api/documents/"+pathID(resource)+"/comments/"+pathID(id)+"/reactions", s.toggleDocumentCommentReaction
			}
			values["id"], values["commentId"] = resource, id
		}
		if handler == nil {
			return nil, fmt.Errorf("comment %q not found or does not support reactions", id)
		}
	}
	want := !boolArg(args, "remove")
	if slices.Contains(reactions[emoji], actor.User.ID) != want {
		// Flow's reaction endpoints toggle; only call them when the state differs.
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, path, values, domain.ReactionInput{Emoji: emoji}, handler)
		if err != nil {
			return nil, err
		}
		var updated struct {
			Reactions map[string][]string `json:"reactions"`
		}
		if err := jsonClone(result, &updated); err == nil {
			reactions = updated.Reactions
		}
	}
	return map[string]any{"type": kind, "id": targetID, "emoji": emoji, "reacted": want, "reactions": reactions}, nil
}

// ---------------------------------------------------------------- subscriptions

func (s *server) saveMCPSubscription(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	if !hasBoolArg(args, "subscribed") {
		return nil, fmt.Errorf("subscribed is required")
	}
	subscribed := boolArg(args, "subscribed")
	keys := []string{"issueId", "projectId", "initiativeId", "documentId", "viewId", "teamId"}
	selected := ""
	for _, key := range keys {
		if stringArg(args, key) != "" {
			if selected != "" {
				return nil, fmt.Errorf("provide exactly one of %s", strings.Join(keys, ", "))
			}
			selected = key
		}
	}
	if selected == "" {
		return nil, fmt.Errorf("provide exactly one of %s", strings.Join(keys, ", "))
	}
	query := stringArg(args, selected)
	if selected == "issueId" {
		if err := s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"id": query}); err != nil {
			return nil, err
		}
		issue, err := mcpFindIssue(data, query)
		if err != nil {
			return nil, err
		}
		user := actor.User
		if value := stringArg(args, "user"); value != "" {
			if user, err = mcpFindUser(data, value); err != nil {
				return nil, err
			}
		}
		ids := slices.DeleteFunc(slices.Clone(issue.SubscriberIDs), func(id string) bool { return id == user.ID })
		if subscribed {
			ids = append(ids, user.ID)
		}
		input := domain.IssueUpdateInput{SubscriberIDs: &ids}
		if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/issue-records/"+pathID(issue.ID), map[string]string{"id": issue.ID}, input, s.updateIssueRecord); err != nil {
			return nil, err
		}
		return map[string]any{"type": "issue", "id": issue.ID, "identifier": issue.Identifier, "title": issue.Title, "user": mcpUserRef(&user), "subscribed": subscribed, "url": mcpIssueURL(data.Workspace.URLKey, issue.Identifier, args)}, nil
	}
	if stringArg(args, "user") != "" {
		return nil, fmt.Errorf("user can only be set for issue subscriptions; other resources subscribe the viewer")
	}
	var kind, id, name string
	switch selected {
	case "projectId":
		item, err := mcpFindProject(data, query)
		if err != nil {
			return nil, err
		}
		kind, id, name = "project", item.ID, item.Name
	case "initiativeId":
		item, err := mcpFindInitiative(data, query)
		if err != nil {
			return nil, err
		}
		kind, id, name = "initiative", item.ID, item.Name
	case "documentId":
		item, err := mcpFindDocument(data, query)
		if err != nil {
			return nil, err
		}
		kind, id, name = "document", item.ID, item.Title
	case "viewId":
		item, err := mcpFindView(data, query)
		if err != nil {
			return nil, err
		}
		kind, id, name = "view", item.ID, item.Name
	default:
		item, err := mcpFindTeam(data, query)
		if err != nil {
			return nil, err
		}
		kind, id, name = "team", item.ID, item.Name
	}
	path := "/api/subscriptions/" + kind + "/" + pathID(id)
	values := map[string]string{"type": kind, "id": id}
	var err error
	if subscribed {
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodPut, path, values, nil, s.addSubscription)
	} else {
		_, err = s.invokeMCPRoute(ctx, actor, http.MethodDelete, path, values, nil, s.removeSubscription)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": kind, "id": id, "name": name, "subscribed": subscribed}, nil
}

// ---------------------------------------------------------------- documents

func (s *server) saveMCPDocument(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.Document
	var err error
	if id != "" {
		if current, err = mcpFindDocument(data, id); err != nil {
			return nil, err
		}
	} else if stringArg(args, "title") == "" {
		return nil, fmt.Errorf("title is required when creating a document")
	}
	input := documentInput{}
	for key, target := range map[string]**string{"title": &input.Title, "icon": &input.Icon, "color": &input.Color} {
		if value, ok := args[key].(string); ok {
			*target = &value
		}
	}
	content, hasContent := args["content"].(string)
	if patches, ok := args["patch"].([]any); ok {
		if hasContent {
			return nil, fmt.Errorf("provide content or patch, not both")
		}
		if id == "" {
			return nil, fmt.Errorf("patch can only edit an existing document")
		}
		if content, err = applyTextPatches(current.Content, patches); err != nil {
			return nil, err
		}
		hasContent = true
	}
	if hasContent {
		// The editor renders contentData over the Markdown projection, so keep
		// both in step and drop any stale collaborative state.
		empty := ""
		input.Content, input.ContentData, input.ContentState = &content, mcpMarkdownDocument(content), &empty
	}
	if value, present := nullableStringArg(args, "project"); present {
		ids := []string{}
		if value != "" {
			project, err := mcpFindProject(data, value)
			if err != nil {
				return nil, err
			}
			ids = append(ids, project.ID)
		}
		input.ProjectIDs = &ids
	}
	if value, present := nullableStringArg(args, "team"); present {
		ids := []string{}
		if value != "" {
			team, err := mcpFindTeam(data, value)
			if err != nil {
				return nil, err
			}
			ids = append(ids, team.ID)
		}
		input.TeamIDs = &ids
	}
	if value, present := nullableStringArg(args, "issue"); present {
		issueID := ""
		if value != "" {
			if err := s.hydrateMCPIssueArguments(ctx, actor, &data, map[string]any{"id": value}); err != nil {
				return nil, err
			}
			issue, err := mcpFindIssue(data, value)
			if err != nil {
				return nil, err
			}
			issueID = issue.ID
		}
		input.IssueID = &issueID
	}
	if value := stringArg(args, "template"); value != "" {
		if id != "" {
			return nil, fmt.Errorf("template can only be applied when creating a document")
		}
		index := slices.IndexFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool {
			return equalFoldAny(value, item.ID, item.Name) && mcpTeamVisible(data, item.TeamID)
		})
		if index < 0 {
			return nil, fmt.Errorf("document template %q not found", value)
		}
		input.TemplateID = &data.DocumentTemplates[index].ID
	}
	if hasBoolArg(args, "archived") {
		if id == "" {
			return nil, fmt.Errorf("archived can only be set on an existing document")
		}
		value := boolArg(args, "archived")
		input.Archived = &value
	}
	var result any
	if id == "" {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/documents", nil, input, s.createDocument)
	} else {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/documents/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateDocument)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Document
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return map[string]any{"id": saved.ID, "slugId": saved.SlugID, "title": saved.Title, "projectIds": saved.ProjectIDs, "teamIds": saved.TeamIDs, "issueId": saved.IssueID, "archived": saved.ArchivedAt != nil, "updatedAt": saved.UpdatedAt, "url": mcpWorkspaceURL(actor.WorkspaceKey, args, "document", saved.SlugID)}, nil
}

// ---------------------------------------------------------------- templates

func (s *server) saveMCPTemplate(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	kind, id := stringArg(args, "type"), stringArg(args, "id")
	if id == "" && stringArg(args, "name") == "" {
		return nil, fmt.Errorf("name is required when creating a template")
	}
	optional := func(key string) *string {
		if value, ok := args[key].(string); ok {
			return &value
		}
		return nil
	}
	var teamID string
	if value := stringArg(args, "team"); value != "" {
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		teamID = team.ID
	}
	var result any
	var err error
	switch kind {
	case "issue":
		var current domain.IssueTemplate
		if id != "" {
			index := slices.IndexFunc(data.IssueTemplates, func(item domain.IssueTemplate) bool {
				return equalFoldAny(id, item.ID, item.Name) && mcpTeamVisible(data, item.TeamID) && mcpTeamVisible(data, item.VisibilityTeamID)
			})
			if index < 0 {
				return nil, fmt.Errorf("issue template %q not found", id)
			}
			current = data.IssueTemplates[index]
			if teamID != "" && teamID != current.TeamID {
				return nil, fmt.Errorf("an issue template's team cannot be changed")
			}
		}
		stateTeam := teamID
		if stateTeam == "" {
			stateTeam = current.TeamID
		}
		input := domain.IssueTemplateMutationInput{Name: optional("name"), Description: optional("description"), Title: optional("title"), Body: optional("body"), Icon: optional("icon"), Color: optional("color")}
		if value := stringArg(args, "state"); value != "" {
			if stateTeam == "" {
				return nil, fmt.Errorf("team is required to set a template state")
			}
			stateID, err := resolveStateID(data, stateTeam, value)
			if err != nil {
				return nil, err
			}
			input.StateID = &stateID
		}
		if hasNumberArg(args, "priority") {
			value := intArg(args, "priority", 0)
			input.Priority = &value
		}
		if input.AssigneeID, err = resolveNullableUserID(data, args, "assignee"); err != nil {
			return nil, err
		}
		if input.ProjectID, err = resolveNullableProjectID(data, args, "project"); err != nil {
			return nil, err
		}
		if _, present := args["labels"]; present {
			ids, err := resolveLabelIDs(data, stringsArg(args, "labels"), "issue")
			if err != nil {
				return nil, err
			}
			input.LabelIDs = &ids
		}
		switch {
		case id == "" && teamID != "":
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/teams/"+pathID(teamID)+"/templates", map[string]string{"id": teamID}, input, s.createIssueTemplate)
		case id == "":
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/issue-templates", nil, input, s.createWorkspaceIssueTemplate)
		case current.Scope != "workspace" && current.TeamID != "":
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/teams/"+pathID(current.TeamID)+"/templates/"+pathID(current.ID), map[string]string{"id": current.TeamID, "templateId": current.ID}, input, s.updateIssueTemplate)
		default:
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/issue-templates/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateWorkspaceIssueTemplate)
		}
	case "project":
		var current domain.ProjectTemplate
		if id != "" {
			index := slices.IndexFunc(data.ProjectTemplates, func(item domain.ProjectTemplate) bool {
				return equalFoldAny(id, item.ID, item.Name) && (len(item.TeamIDs) == 0 || slices.ContainsFunc(item.TeamIDs, func(team string) bool { return mcpTeamVisible(data, team) }))
			})
			if index < 0 {
				return nil, fmt.Errorf("project template %q not found", id)
			}
			current = data.ProjectTemplates[index]
		}
		input := projectTemplateInput{Name: optional("name"), ProjectName: optional("projectName"), TemplateDescription: optional("templateDescription"), Description: optional("description"), Summary: optional("summary"), Icon: optional("icon"), Color: optional("color")}
		if hasNumberArg(args, "priority") {
			value := intArg(args, "priority", 0)
			input.Priority = &value
		}
		if _, present := args["teams"]; present {
			ids := []string{}
			for _, value := range stringsArg(args, "teams") {
				team, err := mcpFindTeam(data, value)
				if err != nil {
					return nil, err
				}
				ids = append(ids, team.ID)
			}
			input.TeamIDs = &ids
		} else if teamID != "" {
			input.TeamIDs = &[]string{teamID}
		}
		if _, present := args["labels"]; present {
			ids, err := resolveLabelIDs(data, stringsArg(args, "labels"), "project")
			if err != nil {
				return nil, err
			}
			input.LabelIDs = &ids
		}
		if input.LeadID, err = resolveNullableUserID(data, args, "lead"); err != nil {
			return nil, err
		}
		if id == "" {
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/project-templates", nil, input, s.createProjectTemplate)
		} else {
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/project-templates/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateProjectTemplate)
		}
	case "document":
		var current domain.DocumentTemplate
		if id != "" {
			index := slices.IndexFunc(data.DocumentTemplates, func(item domain.DocumentTemplate) bool {
				return equalFoldAny(id, item.ID, item.Name) && mcpTeamVisible(data, item.TeamID)
			})
			if index < 0 {
				return nil, fmt.Errorf("document template %q not found", id)
			}
			current = data.DocumentTemplates[index]
		} else if teamID == "" {
			return nil, fmt.Errorf("team is required when creating a document template")
		}
		input := documentTemplateInput{Name: optional("name"), Description: optional("description"), Title: optional("title"), Icon: optional("icon")}
		if teamID != "" {
			input.TeamID = &teamID
		}
		if content, ok := args["content"].(string); ok {
			empty := ""
			input.Content, input.ContentData, input.ContentState = &content, mcpMarkdownDocument(content), &empty
		}
		if id == "" {
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/document-templates", nil, input, s.createDocumentTemplate)
		} else {
			result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/document-templates/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateDocumentTemplate)
		}
	default:
		return nil, fmt.Errorf("type must be issue, project, or document")
	}
	if err != nil {
		return nil, err
	}
	var saved struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		TeamID  string   `json:"teamId"`
		TeamIDs []string `json:"teamIds"`
		Title   string   `json:"title"`
	}
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	receipt := map[string]any{"id": saved.ID, "type": kind, "name": saved.Name, "created": id == ""}
	if saved.TeamID != "" {
		receipt["teamId"] = saved.TeamID
	}
	if len(saved.TeamIDs) > 0 {
		receipt["teamIds"] = saved.TeamIDs
	}
	if saved.Title != "" {
		receipt["title"] = saved.Title
	}
	return receipt, nil
}

// ---------------------------------------------------------------- inbox

func (s *server) updateMCPNotification(ctx context.Context, actor mcpActor, args map[string]any) (any, error) {
	if boolArg(args, "markAllRead") {
		if stringArg(args, "id") != "" {
			return nil, fmt.Errorf("use either id or markAllRead, not both")
		}
		result, err := s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/notifications/batch", nil, domain.NotificationBatchInput{Action: "markAllRead"}, s.batchNotifications)
		if err != nil {
			return nil, err
		}
		updated, _ := result.(map[string]any)
		return map[string]any{"markedAllRead": true, "updated": updated["updated"]}, nil
	}
	id := stringArg(args, "id")
	if id == "" {
		return nil, fmt.Errorf("id is required unless markAllRead is true")
	}
	input := map[string]any{}
	if hasBoolArg(args, "read") {
		input["read"] = boolArg(args, "read")
	}
	if hasBoolArg(args, "archived") {
		input["archived"] = boolArg(args, "archived")
	}
	if value, present := nullableStringArg(args, "snoozedUntil"); present {
		if value == "" {
			input["snoozedUntil"] = nil
		} else {
			until, err := mcpFutureDate(value)
			if err != nil {
				return nil, fmt.Errorf("snoozedUntil must be a future ISO date/time or ISO-8601 duration (e.g. PT3H), or null to unsnooze")
			}
			input["snoozedUntil"] = until.UTC().Format(time.RFC3339)
		}
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("provide read, archived, or snoozedUntil")
	}
	result, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/notifications/"+pathID(id), map[string]string{"id": id}, input, s.updateNotification)
	if err != nil {
		return nil, err
	}
	var saved domain.Notification
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return map[string]any{"id": saved.ID, "type": saved.Type, "read": saved.ReadAt != nil, "archivedAt": saved.ArchivedAt, "snoozedUntil": saved.SnoozedUntil}, nil
}

// ---------------------------------------------------------------- agent skills

func (s *server) saveMCPAgentSkill(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id, name, instructions := stringArg(args, "id"), stringArg(args, "name"), stringArg(args, "instructions")
	if teamQuery := stringArg(args, "team"); teamQuery != "" {
		team, err := mcpFindTeam(data, teamQuery)
		if err != nil {
			return nil, err
		}
		skills := slices.Clone(data.TeamSettings[team.ID].AgentSkills)
		index := -1
		if id != "" {
			if index = slices.IndexFunc(skills, func(item domain.TeamAgentSkill) bool { return equalFoldAny(id, item.ID, item.Name) }); index < 0 {
				return nil, fmt.Errorf("team skill %q not found", id)
			}
		}
		skill := domain.TeamAgentSkill{ID: fmt.Sprintf("skill_%d", time.Now().UnixMilli()), Enabled: true}
		if index >= 0 {
			skill = skills[index]
		}
		if name != "" {
			skill.Name = name
		}
		if instructions != "" {
			skill.Instructions = instructions
		}
		if hasBoolArg(args, "enabled") {
			skill.Enabled = boolArg(args, "enabled")
		}
		if strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.Instructions) == "" || len(skill.Name) > 80 || len(skill.Instructions) > 12000 {
			return nil, fmt.Errorf("skill name and instructions are required")
		}
		if index >= 0 {
			skills[index] = skill
		} else {
			skills = append(skills, skill)
		}
		if _, err := s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/teams/"+pathID(team.ID)+"/settings", map[string]string{"id": team.ID}, map[string]any{"agentSkills": skills}, s.updateStructuredTeamSettings); err != nil {
			return nil, err
		}
		return map[string]any{"id": skill.ID, "name": skill.Name, "instructions": skill.Instructions, "enabled": skill.Enabled, "scope": "team", "team": map[string]any{"id": team.ID, "key": team.Key, "name": team.Name}, "created": index < 0}, nil
	}
	if hasBoolArg(args, "enabled") {
		return nil, fmt.Errorf("enabled only applies to team skills")
	}
	var result any
	var err error
	if id == "" {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/agent/skills", nil, agentSkillInput{Name: name, Instructions: instructions}, s.createAgentSkill)
	} else {
		index := slices.IndexFunc(data.AgentSkills, func(item domain.PersonalAgentSkill) bool {
			return item.UserID == actor.User.ID && equalFoldAny(id, item.ID, item.Name)
		})
		if index < 0 {
			return nil, fmt.Errorf("agent skill %q not found", id)
		}
		current := data.AgentSkills[index]
		if name == "" {
			name = current.Name
		}
		if instructions == "" {
			instructions = current.Instructions
		}
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/agent/skills/"+pathID(current.ID), map[string]string{"id": current.ID}, agentSkillInput{Name: name, Instructions: instructions}, s.updateAgentSkill)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.PersonalAgentSkill
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return map[string]any{"id": saved.ID, "name": saved.Name, "instructions": saved.Instructions, "scope": "personal", "created": id == "", "updatedAt": saved.UpdatedAt, "url": mcpWorkspaceURL(actor.WorkspaceKey, args, "settings", "skill", saved.ID)}, nil
}

// ---------------------------------------------------------------- loops

func (s *server) saveMCPLoop(ctx context.Context, actor mcpActor, data domain.Bootstrap, args map[string]any) (any, error) {
	id := stringArg(args, "id")
	var current domain.Loop
	if id != "" {
		index := slices.IndexFunc(data.Loops, func(item domain.Loop) bool { return equalFoldAny(id, item.ID, item.Name) })
		if index < 0 {
			return nil, fmt.Errorf("loop %q not found", id)
		}
		current = data.Loops[index]
	} else if stringArg(args, "name") == "" && stringArg(args, "templateId") == "" {
		return nil, fmt.Errorf("name is required when creating a loop")
	}
	wasDraft := id == "" || current.Status == "draft"
	input := loopInput{}
	for key, target := range map[string]**string{"name": &input.Name, "description": &input.Description, "instructions": &input.Instructions, "icon": &input.Icon, "color": &input.Color, "level": &input.Level, "teamAccess": &input.TeamAccess, "codeAccess": &input.CodeAccess, "templateId": &input.TemplateID} {
		if value, ok := args[key].(string); ok {
			*target = &value
		}
	}
	for key, target := range map[string]**bool{"enabled": &input.Enabled, "allowChangesOutsideTrigger": &input.AllowChangesOutsideTrigger, "allowExternalSync": &input.AllowExternalSync, "webSearch": &input.WebSearch} {
		if hasBoolArg(args, key) {
			value := boolArg(args, key)
			*target = &value
		}
	}
	if status := stringArg(args, "status"); status != "" {
		input.Status = &status
	}
	if hasBoolArg(args, "publish") && boolArg(args, "publish") {
		status := "published"
		input.Status = &status
	}
	if value := stringArg(args, "team"); value != "" {
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		input.TeamID = &team.ID
		if input.Level == nil {
			level := "team"
			input.Level = &level
		}
	}
	if value := stringArg(args, "owner"); value != "" {
		user, err := mcpFindUser(data, value)
		if err != nil {
			return nil, err
		}
		input.OwnerID = &user.ID
	}
	triggerType := current.TriggerType
	if value := stringArg(args, "trigger"); value != "" {
		if !slices.Contains(loopTriggerTypes, value) {
			return nil, fmt.Errorf("trigger must be one of %s", strings.Join(loopTriggerTypes, ", "))
		}
		triggerType = value
		input.TriggerType = &value
	}
	if triggerType == "" {
		triggerType = "schedule"
		if template := loopTemplateByID(stringArg(args, "templateId")); template != nil && id == "" {
			triggerType = template.TriggerType
		}
	}
	scheduleKeys := []string{"interval", "unit", "time", "starting", "startDate", "timezone", "weekdays"}
	eventKeys := []string{"action", "teams", "event", "value", "filters"}
	if hasAnyArg(args, append(slices.Clone(scheduleKeys), eventKeys...)...) || input.TriggerType != nil {
		config := map[string]any{}
		if input.TriggerType == nil || *input.TriggerType == current.TriggerType {
			for key, value := range normalizeLoopTriggerConfig(triggerType, current.TriggerConfig) {
				config[key] = value
			}
		}
		if triggerType == "schedule" {
			if hasAnyArg(args, "action", "event", "value", "filters") {
				return nil, fmt.Errorf("event, value, filters and action apply to event triggers, not schedules")
			}
			// On a schedule, teams are the teams the loop may use (team access "selected").
			if _, present := args["teams"]; present {
				ids := []any{}
				for _, value := range stringsArg(args, "teams") {
					team, err := mcpFindTeam(data, value)
					if err != nil {
						return nil, err
					}
					ids = append(ids, team.ID)
				}
				config["teamIds"] = ids
			}
			if len(config) == 0 || id == "" && config["startDate"] == nil {
				for key, value := range defaultLoopTriggerConfig("schedule", time.Now().UTC()) {
					if _, ok := config[key]; !ok {
						config[key] = value
					}
				}
			}
			if hasNumberArg(args, "interval") {
				config["interval"] = min(max(intArg(args, "interval", 1), 1), 30)
			}
			if value := stringArg(args, "unit"); value != "" {
				if !slices.Contains([]string{"hour", "day", "week", "month"}, value) {
					return nil, fmt.Errorf("unit must be hour, day, week, or month")
				}
				config["unit"] = value
			}
			if value := stringArg(args, "time"); value != "" {
				if !validClock(value) {
					return nil, fmt.Errorf("time must be HH:MM")
				}
				config["time"] = value
			}
			if value := firstNonEmpty(stringArg(args, "startDate"), stringArg(args, "starting")); value != "" {
				if !validDate(value) {
					return nil, fmt.Errorf("startDate must be YYYY-MM-DD")
				}
				config["startDate"] = value
			}
			if value := stringArg(args, "timezone"); value != "" {
				if _, err := time.LoadLocation(value); err != nil {
					return nil, fmt.Errorf("timezone %q is not a valid IANA timezone", value)
				}
				config["timezone"] = value
			}
			if _, present := args["weekdays"]; present {
				days := []any{}
				for _, day := range stringsArg(args, "weekdays") {
					day = strings.ToLower(strings.TrimSpace(day))
					if len(day) > 3 {
						day = day[:3]
					}
					days = append(days, day)
				}
				config["weekdays"] = days
			}
		} else {
			if hasAnyArg(args, scheduleKeys...) {
				return nil, fmt.Errorf("interval, unit, time, startDate, weekdays and timezone apply to schedule triggers")
			}
			if value := stringArg(args, "action"); value != "" {
				if value != "created" && value != "created or updated" {
					return nil, fmt.Errorf("action must be \"created\" or \"created or updated\"")
				}
				config["event"] = map[string]string{"created": "created", "created or updated": "updated"}[value]
				if value == "created or updated" {
					config["includeCreated"] = true
				}
			}
			if value := stringArg(args, "event"); value != "" {
				if !slices.Contains(loopTriggerEvents[triggerType], value) {
					return nil, fmt.Errorf("event must be one of %s for %s triggers", strings.Join(loopTriggerEvents[triggerType], ", "), triggerType)
				}
				if value != config["event"] {
					delete(config, "value")
				}
				config["event"] = value
			}
			if raw, present := args["value"]; present {
				value, err := mcpLoopTriggerValue(data, fmt.Sprint(config["event"]), raw)
				if err != nil {
					return nil, err
				}
				config["value"] = value
			}
			if _, present := args["teams"]; present {
				ids := []any{}
				for _, value := range stringsArg(args, "teams") {
					team, err := mcpFindTeam(data, value)
					if err != nil {
						return nil, err
					}
					ids = append(ids, team.ID)
				}
				config["teamIds"] = ids
			}
			if raw, present := args["filters"]; present {
				items, _ := raw.([]any)
				filters := []any{}
				for _, item := range items {
					filter, ok := item.(map[string]any)
					if !ok {
						return nil, fmt.Errorf("each filter needs field, operator and value")
					}
					field, _ := filter["field"].(string)
					operator, _ := filter["operator"].(string)
					if operator == "" {
						operator = "is"
					}
					value, err := mcpLoopTriggerValue(data, field, filter["value"])
					if err != nil {
						return nil, err
					}
					filters = append(filters, map[string]any{"field": field, "operator": operator, "value": value})
				}
				config["filters"] = filters
			}
		}
		input.TriggerConfig = config
	}
	if input.Enabled != nil && *input.Enabled && wasDraft && (input.Status == nil || *input.Status == "draft") {
		// Drafts turn on when they are published.
		input.Enabled = nil
	}
	if err := validateLoopInput(input); err != nil {
		return nil, err
	}
	var result any
	var err error
	if id == "" {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPost, "/api/loops", nil, input, s.createLoop)
	} else {
		result, err = s.invokeMCPRoute(ctx, actor, http.MethodPatch, "/api/loops/"+pathID(current.ID), map[string]string{"id": current.ID}, input, s.updateLoop)
	}
	if err != nil {
		return nil, err
	}
	var saved domain.Loop
	if err := jsonClone(result, &saved); err != nil {
		return nil, err
	}
	return mcpLoopCard(actor.WorkspaceKey, data, args, saved, id == "", wasDraft && saved.Status == "published"), nil
}

// mcpLoopCard is the loop card the agent chat renders after save_loop.
func mcpLoopCard(workspace string, data domain.Bootstrap, args map[string]any, loop domain.Loop, created, published bool) map[string]any {
	teamName := ""
	for _, team := range data.Teams {
		if team.ID == loop.TeamID {
			teamName = team.Name
		}
	}
	link := mcpWorkspaceURL(workspace, args, "loop", loop.ID)
	if loop.Status == "draft" {
		link = mcpWorkspaceURL(workspace, args, "loops", "new") + "?draftId=" + url.QueryEscape(loop.ID)
	}
	return map[string]any{"id": loop.ID, "name": loop.Name, "description": loop.Description, "status": loop.Status, "trigger": loop.TriggerType, "triggerConfig": loop.TriggerConfig, "instructions": loop.Instructions, "level": loop.Level, "teamId": loop.TeamID, "teamName": teamName, "enabled": loop.Enabled, "runCount30d": loop.RunCount30d, "ownerId": loop.OwnerID, "created": created, "published": published, "url": link}
}

// mcpLoopTriggerValue resolves a trigger event or filter value given by name
// (a person, "me", "none") to the stored form; other values stay as given.
func mcpLoopTriggerValue(data domain.Bootstrap, field string, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	value := strings.TrimSpace(fmt.Sprint(raw))
	if number, ok := raw.(float64); ok {
		value = strconv.Itoa(int(number))
	}
	lower := strings.ToLower(value)
	if lower == "none" || lower == "no assignee" || lower == "unassigned" || lower == "no project" || lower == "null" {
		return nil, nil
	}
	switch field {
	case "assignee", "agent", "creator":
		if lower == "any" {
			return "any", nil
		}
		user, err := mcpFindUser(data, value)
		if err != nil {
			return nil, err
		}
		return user.ID, nil
	case "team":
		if lower == "any" {
			return "any", nil
		}
		team, err := mcpFindTeam(data, value)
		if err != nil {
			return nil, err
		}
		return team.ID, nil
	}
	return value, nil
}

// mcpTeamRoles returns the current role of every member of the team, from the
// persisted memberships (signed-in workspaces) or the workspace snapshot.
func (s *server) mcpTeamRoles(ctx context.Context, data domain.Bootstrap, teamID string) map[string]string {
	roles := map[string]string{}
	for _, member := range data.TeamMembers {
		if member.TeamID == teamID {
			roles[member.UserID] = firstNonEmpty(member.Role, "member")
		}
	}
	if persisted, err := s.store.ListTeamMembers(ctx, data.Workspace.ID); err == nil {
		for _, member := range persisted {
			if member.TeamID == teamID {
				roles[member.UserID] = firstNonEmpty(member.Role, "member")
			}
		}
	}
	return roles
}
