package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// labelMoveConflict refuses a move whose copies would clash with what a
// receiving team already has. It answers 409 with a fixed message.
type labelMoveConflict string

func (e labelMoveConflict) Error() string { return string(e) }
func (labelMoveConflict) Unwrap() error   { return errConflict }

const (
	errLabelMoveNameTaken labelMoveConflict = "A team that uses these labels already has a different label with the same name; rename one of them and try again"
	errLabelMoveGroupType labelMoveConflict = "A team that uses this group already has a label group with the same name but a different type; rename one of them and try again"
)

// moveWorkspaceLabelGroupToTeams moves a workspace issue label group, with its
// labels, to the teams that use them (the design reference's "Move to
// teams"); see moveLabelsToTeams for what moves where. The group's team labels
// stay in their own teams. Like moving a single label, it fails when no team
// uses the group.
func (s *server) moveWorkspaceLabelGroupToTeams(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var moved []domain.LabelGroup
	var movedLabels map[string]struct{}
	// The mutation loads the issues carrying one of the group's labels, read
	// from the locked snapshot so a label added meanwhile is rewritten too.
	ctx := store.WithMutationScope(r.Context(), store.MutationScope{Resolve: func(data domain.Bootstrap) store.MutationScope {
		ids := []string{}
		for _, label := range data.Labels {
			if label.GroupID == id {
				ids = append(ids, label.ID)
			}
		}
		return store.MutationScope{LabelIssues: ids}
	}})
	err := s.store.MutateWorkspaceWithAggregate(ctx, workspaceKey(r), "label_group.moved_to_teams", id, func(data *domain.Bootstrap) (string, error) {
		index := slices.IndexFunc(data.LabelGroups, func(group domain.LabelGroup) bool {
			return group.ID == id && labelScopeIsWorkspace(group.Scope)
		})
		if index < 0 {
			return "", errNotFound
		}
		source := data.LabelGroups[index]
		if source.ResourceType != "" && source.ResourceType != "issue" {
			return "", errInvalid
		}
		members := []domain.IssueLabel{}
		for _, label := range data.Labels {
			if label.GroupID == id && labelScopeIsWorkspace(label.Scope) {
				members = append(members, label)
			}
		}
		move, err := moveLabelsToTeams(data, members, &source)
		if err != nil {
			return "", err
		}
		data.LabelGroups = slices.DeleteFunc(data.LabelGroups, func(group domain.LabelGroup) bool { return group.ID == id })
		appendAudit(data, "moved_to_teams", "label_group", id, map[string]any{"name": source.Name, "teamIds": move.receiving, "labels": len(members)})
		moved, movedLabels = move.holders(), move.moving
		return id, nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.pruneMovedLabelSuggestions(r, movedLabels)
	writeJSON(w, http.StatusOK, moved)
}

// pruneMovedLabelSuggestions drops label suggestions for moved labels, as a
// label deletion does: suggestions are recomputed, never re-pointed.
func (s *server) pruneMovedLabelSuggestions(r *http.Request, moved map[string]struct{}) {
	if len(moved) == 0 {
		return
	}
	s.pruneIssueSuggestionTargets(r.Context(), workspaceKey(r), func(item domain.IssueSuggestion) bool {
		_, stale := moved[item.SuggestedLabelID]
		return item.Type == "label" && stale
	})
}

// labelTeamMove is a workspace label (or workspace label group) being moved
// to the teams that use it.
type labelTeamMove struct {
	// labels are the moving workspace labels by ID, in collection order.
	labels []domain.IssueLabel
	moving map[string]struct{}
	// group is the moving workspace group; nil when one label moves.
	group *domain.LabelGroup
	// receiving are the teams holding the copies, in team order: the topmost
	// team of each subtree that uses the labels (its sub-teams inherit them).
	receiving []string
	// copies is, per receiving team, each moving label's label there.
	copies map[string]map[string]domain.IssueLabel
	// groups is, per receiving team, the group holding its copies.
	groups map[string]domain.LabelGroup
	// ownGroups hold the moving group's team labels in teams that receive
	// no copies.
	ownGroups []domain.LabelGroup
}

// moveLabelsToTeams moves workspace labels to the teams that use them: a team
// uses a label when one of its issues carries it or its templates, triage
// rules, single-team SLA rules or team views name it. The topmost using team
// of each subtree gets a copy (or reuses a label it already has with the same
// name and group); its sub-teams inherit it. With group, every receiving team
// gets the group too (or reuses a same-named group of the same type), and the
// group's team labels move into their own team's group.
//
// References follow the copies: issues (and trashed issues), suggested
// labels, team templates and their sub-issues, project template issues,
// triage rules, SLA rules, team views and label favorites and subscriptions
// switch to the label of their team. References outside any team that got a
// copy (workspace templates, workspace views, projects) lose the label as a
// deletion removes it; a multi-team SLA rule matches the label by name, which
// every copy keeps. The moving labels are then deleted.
func moveLabelsToTeams(data *domain.Bootstrap, labels []domain.IssueLabel, group *domain.LabelGroup) (*labelTeamMove, error) {
	move := &labelTeamMove{labels: labels, moving: make(map[string]struct{}, len(labels)), group: group, copies: map[string]map[string]domain.IssueLabel{}, groups: map[string]domain.LabelGroup{}}
	for _, label := range labels {
		move.moving[label.ID] = struct{}{}
	}
	used := move.usingTeams(data)
	receiving := map[string]bool{}
	for _, team := range data.Teams {
		if !used[team.ID] {
			continue
		}
		top := team.ID
		for _, ancestor := range teamAncestorIDs(data, team.ID) {
			if used[ancestor] {
				top = ancestor
			}
		}
		receiving[top] = true
	}
	for _, team := range data.Teams {
		if receiving[team.ID] {
			move.receiving = append(move.receiving, team.ID)
		}
	}
	teamLabels := group != nil && slices.ContainsFunc(data.Labels, func(label domain.IssueLabel) bool {
		return label.GroupID == group.ID && !labelScopeIsWorkspace(label.Scope)
	})
	if len(move.receiving) == 0 && !teamLabels {
		return nil, errInvalid
	}
	stamp := idgen.Stamp()
	if err := move.placeCopies(data, stamp); err != nil {
		return nil, err
	}
	if group != nil {
		if err := move.rehomeTeamLabels(data, stamp); err != nil {
			return nil, err
		}
	}
	move.rewriteReferences(data)
	data.Labels = slices.DeleteFunc(data.Labels, func(label domain.IssueLabel) bool { return move.moves(label.ID) })
	return move, nil
}

func (m *labelTeamMove) moves(id string) bool {
	_, ok := m.moving[id]
	return ok
}

func (m *labelTeamMove) movesAny(ids []string) bool {
	return slices.ContainsFunc(ids, m.moves)
}

// usingTeams are the teams whose issues or team-owned configuration carry a
// moving label.
func (m *labelTeamMove) usingTeams(data *domain.Bootstrap) map[string]bool {
	used := map[string]bool{}
	add := func(teamID string) {
		if teamID != "" {
			used[teamID] = true
		}
	}
	for _, issue := range data.Issues {
		if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return m.moves(label.ID) }) {
			add(issue.Team.ID)
		}
	}
	for _, template := range data.IssueTemplates {
		if m.movesAny(template.LabelIDs) {
			add(template.TeamID)
		}
		for _, child := range template.SubIssues {
			if m.movesAny(child.LabelIDs) {
				add(templateSubIssueTeam(template, child))
			}
		}
	}
	for _, template := range data.ProjectTemplates {
		for _, item := range template.Issues {
			if m.movesAny(item.LabelIDs) {
				add(templateIssueLabelTeam(template, item))
			}
		}
	}
	for _, rule := range data.TriageRoutingRules {
		if m.movesAny(rule.LabelIDs) || m.moves(rule.Conditions["labelId"]) {
			add(rule.TeamID)
		}
	}
	for _, rule := range data.SLARules {
		if value, ok := rule.Filters["label"].(string); ok && m.moves(value) && len(rule.TeamIDs) == 1 {
			add(rule.TeamIDs[0])
		}
	}
	for _, view := range data.SavedViews {
		if view.TeamID != "" && labelReferencesInJSON(view.Filters, m.moving) {
			add(view.TeamID)
		}
	}
	return used
}

func templateSubIssueTeam(template domain.IssueTemplate, child domain.TemplateSubIssue) string {
	if child.TeamID != "" {
		return child.TeamID
	}
	return template.TeamID
}

// templateIssueLabelTeam is the team a project template issue is created in:
// its own, else the template's first team (see projectTemplateIssueTeam).
func templateIssueLabelTeam(template domain.ProjectTemplate, item domain.ProjectTemplateIssue) string {
	if item.TeamID != "" {
		return item.TeamID
	}
	if len(template.TeamIDs) > 0 {
		return template.TeamIDs[0]
	}
	return ""
}

// placeCopies gives every receiving team its labels (and group). A label the
// team, an ancestor or a sub-team already has under the same name is reused
// when it is the same label (in the receiving group, available to the team)
// and refused otherwise: sub-teams inherit their ancestors' labels, so names
// stay unique along a subtree.
func (m *labelTeamMove) placeCopies(data *domain.Bootstrap, stamp string) error {
	for teamNumber, teamID := range m.receiving {
		available := append([]string{teamID}, teamAncestorIDs(data, teamID)...)
		reach := append(slices.Clone(available), teamDescendantIDs(data, teamID)...)
		groupID, scope := "", teamID
		if m.group != nil {
			group, err := m.teamGroup(data, teamID, available, fmt.Sprintf("label_group_%s_%d", stamp, teamNumber))
			if err != nil {
				return err
			}
			m.groups[teamID] = group
			groupID, scope = group.ID, group.Scope
			if group.Scope == teamID {
				m.moveTeamLabels(data, teamID, group.ID)
			}
		}
		copies := make(map[string]domain.IssueLabel, len(m.labels))
		for labelNumber, label := range m.labels {
			existing := slices.IndexFunc(data.Labels, func(item domain.IssueLabel) bool {
				return !m.moves(item.ID) && slices.Contains(reach, item.Scope) &&
					labelResourceType(item) == labelResourceType(label) && strings.EqualFold(item.Name, label.Name)
			})
			if existing >= 0 {
				item := data.Labels[existing]
				if item.GroupID != groupID || !slices.Contains(available, item.Scope) {
					return errLabelMoveNameTaken
				}
				copies[label.ID] = item
				continue
			}
			copied := label
			copied.ID = fmt.Sprintf("label_%s_%d_%d", stamp, teamNumber, labelNumber)
			copied.Scope, copied.GroupID = scope, groupID
			data.Labels = append(data.Labels, copied)
			copies[label.ID] = copied
		}
		m.copies[teamID] = copies
	}
	return nil
}

// teamGroup is the group a team's copies go to: a non-archived group of the
// same name and resource type the team (or an ancestor) already has, or a new
// copy of the moving group.
func (m *labelTeamMove) teamGroup(data *domain.Bootstrap, teamID string, available []string, newID string) (domain.LabelGroup, error) {
	resource := labelGroupResourceType(*m.group)
	for _, group := range data.LabelGroups {
		if group.ID == m.group.ID || group.ArchivedAt != nil || !slices.Contains(available, group.Scope) ||
			labelGroupResourceType(group) != resource || !strings.EqualFold(group.Name, m.group.Name) {
			continue
		}
		if group.MultiSelect != m.group.MultiSelect {
			return domain.LabelGroup{}, errLabelMoveGroupType
		}
		return group, nil
	}
	group := *m.group
	group.ID, group.Scope = newID, teamID
	data.LabelGroups = append(data.LabelGroups, group)
	return group, nil
}

func labelGroupResourceType(group domain.LabelGroup) string {
	if group.ResourceType == "" {
		return "issue"
	}
	return group.ResourceType
}

// moveTeamLabels puts the moving group's labels owned by teamID into groupID.
func (m *labelTeamMove) moveTeamLabels(data *domain.Bootstrap, teamID, groupID string) {
	for index := range data.Labels {
		if label := &data.Labels[index]; label.GroupID == m.group.ID && label.Scope == teamID {
			label.GroupID = groupID
			cascadeLabel(data, *label)
		}
	}
}

// rehomeTeamLabels handles the moving group's team labels whose team got no
// group of its own: they stay in their team, in a team copy of the group, or
// ungrouped under a team that inherits its copies (a team label cannot join
// an ancestor's group, and a second same-named group would be confusing).
func (m *labelTeamMove) rehomeTeamLabels(data *domain.Bootstrap, stamp string) error {
	created := map[string]string{}
	for index := 0; index < len(data.Labels); index++ {
		label := data.Labels[index]
		if label.GroupID != m.group.ID || labelScopeIsWorkspace(label.Scope) {
			continue
		}
		groupID, done := created[label.Scope]
		if !done {
			if m.holder(data, label.Scope) == "" && teamExists(data, label.Scope) {
				group, err := m.teamGroup(data, label.Scope, []string{label.Scope}, fmt.Sprintf("label_group_%s_t%d", stamp, len(created)))
				if err != nil {
					return err
				}
				groupID = group.ID
				m.ownGroups = append(m.ownGroups, group)
			}
			created[label.Scope] = groupID
		}
		m.moveTeamLabels(data, label.Scope, groupID)
	}
	return nil
}

// holder is the receiving team whose copies teamID uses: the team itself or
// the ancestor that received them. Empty when no copy reaches the team.
func (m *labelTeamMove) holder(data *domain.Bootstrap, teamID string) string {
	if teamID == "" {
		return ""
	}
	if _, ok := m.copies[teamID]; ok {
		return teamID
	}
	for _, ancestor := range teamAncestorIDs(data, teamID) {
		if _, ok := m.copies[ancestor]; ok {
			return ancestor
		}
	}
	return ""
}

// teamLabel is the label a moving label becomes for teamID.
func (m *labelTeamMove) teamLabel(data *domain.Bootstrap, teamID, id string) (domain.IssueLabel, bool) {
	holder := m.holder(data, teamID)
	if holder == "" {
		return domain.IssueLabel{}, false
	}
	label, ok := m.copies[holder][id]
	return label, ok
}

// remap re-points moving label IDs to teamID's labels and drops those with
// none there. Unchanged lists are returned as they are.
func (m *labelTeamMove) remap(data *domain.Bootstrap, teamID string, ids []string) []string {
	if !m.movesAny(ids) {
		return ids
	}
	next := make([]string, 0, len(ids))
	for _, id := range ids {
		if m.moves(id) {
			label, ok := m.teamLabel(data, teamID, id)
			if !ok {
				continue
			}
			id = label.ID
		}
		if !slices.Contains(next, id) {
			next = append(next, id)
		}
	}
	return next
}

// moveIssueLabels switches an issue to its team's labels; it reports whether
// anything changed.
func (m *labelTeamMove) moveIssueLabels(data *domain.Bootstrap, issue *domain.Issue) bool {
	changed := false
	if slices.ContainsFunc(issue.Labels, func(label domain.IssueLabel) bool { return m.moves(label.ID) }) {
		labels := make([]domain.IssueLabel, 0, len(issue.Labels))
		for _, label := range issue.Labels {
			if m.moves(label.ID) {
				next, ok := m.teamLabel(data, issue.Team.ID, label.ID)
				if !ok {
					continue
				}
				label = next
			}
			if !slices.ContainsFunc(labels, func(item domain.IssueLabel) bool { return item.ID == label.ID }) {
				labels = append(labels, label)
			}
		}
		issue.Labels, changed = labels, true
	}
	if m.movesAny(issue.SuggestedLabelIDs) {
		issue.SuggestedLabelIDs, changed = m.remap(data, issue.Team.ID, issue.SuggestedLabelIDs), true
	}
	return changed
}

func (m *labelTeamMove) rewriteReferences(data *domain.Bootstrap) {
	for index := range data.Issues {
		m.moveIssueLabels(data, &data.Issues[index])
	}
	m.moveTrashedIssues(data)
	dropMoving := func(ids []string) []string {
		if !m.movesAny(ids) {
			return ids
		}
		return slices.DeleteFunc(slices.Clone(ids), m.moves)
	}
	for index := range data.Projects {
		data.Projects[index].LabelIDs = dropMoving(data.Projects[index].LabelIDs)
	}
	for index := range data.Initiatives {
		data.Initiatives[index].LabelIDs = dropMoving(data.Initiatives[index].LabelIDs)
	}
	for index := range data.IssueTemplates {
		template := &data.IssueTemplates[index]
		template.LabelIDs = m.remap(data, template.TeamID, template.LabelIDs)
		for child := range template.SubIssues {
			template.SubIssues[child].LabelIDs = m.remap(data, templateSubIssueTeam(*template, template.SubIssues[child]), template.SubIssues[child].LabelIDs)
		}
	}
	for index := range data.ProjectTemplates {
		template := &data.ProjectTemplates[index]
		template.LabelIDs = dropMoving(template.LabelIDs)
		for item := range template.Issues {
			template.Issues[item].LabelIDs = m.remap(data, templateIssueLabelTeam(*template, template.Issues[item]), template.Issues[item].LabelIDs)
		}
	}
	for index := range data.TriageRoutingRules {
		rule := &data.TriageRoutingRules[index]
		rule.LabelIDs = m.remap(data, rule.TeamID, rule.LabelIDs)
		if id := rule.Conditions["labelId"]; m.moves(id) {
			if label, ok := m.teamLabel(data, rule.TeamID, id); ok {
				rule.Conditions["labelId"] = label.ID
			}
		}
	}
	for index := range data.SLARules {
		rule := &data.SLARules[index]
		value, ok := rule.Filters["label"].(string)
		if !ok || !m.moves(value) {
			continue
		}
		if len(rule.TeamIDs) == 1 {
			if label, found := m.teamLabel(data, rule.TeamIDs[0], value); found {
				rule.Filters["label"] = label.ID
				continue
			}
		}
		// SLA label filters also match by name, which every copy keeps.
		index := slices.IndexFunc(m.labels, func(label domain.IssueLabel) bool { return label.ID == value })
		rule.Filters["label"] = m.labels[index].Name
	}
	for index := range data.SavedViews {
		view := &data.SavedViews[index]
		holder := m.holder(data, view.TeamID)
		if labelReferencesInJSON(view.Filters, m.moving) {
			if holder != "" {
				copies := m.copies[holder]
				view.Filters = remapLabelReferencesInJSON(view.Filters, func(id string) (string, bool) {
					label, ok := copies[id]
					return label.ID, ok
				})
			} else {
				view.Filters = removeLabelReferencesFromJSON(view.Filters, m.moving)
			}
		}
		if m.group != nil {
			target := ""
			if holder != "" {
				target = m.groups[holder].ID
			}
			view.Display = moveDisplayLabelGroup(view.Display, m.group.ID, target)
		}
	}
	m.movePreferences(data)
}

// moveTrashedIssues rewrites trashed issues carrying a moving label, so a
// restore brings back their team's label.
func (m *labelTeamMove) moveTrashedIssues(data *domain.Bootstrap) {
	rewriteTrashedIssueLabels(data, m.moving, func(issue *domain.Issue) bool { return m.moveIssueLabels(data, issue) })
}

// rewriteTrashedIssueLabels applies rewrite to the trashed issues whose
// payload names one of ids; rewrite reports whether it changed the issue.
func rewriteTrashedIssueLabels(data *domain.Bootstrap, ids map[string]struct{}, rewrite func(*domain.Issue) bool) {
	quoted := make([][]byte, 0, len(ids))
	for id := range ids {
		if encoded, err := json.Marshal(id); err == nil {
			quoted = append(quoted, encoded)
		}
	}
	for index := range data.Trash {
		entry := &data.Trash[index]
		if entry.ResourceType != "issue" || !slices.ContainsFunc(quoted, func(id []byte) bool { return bytes.Contains(entry.Payload, id) }) {
			continue
		}
		var payload map[string]json.RawMessage
		var issue domain.Issue
		if json.Unmarshal(entry.Payload, &payload) != nil || json.Unmarshal(payload["issue"], &issue) != nil || !rewrite(&issue) {
			continue
		}
		encoded, err := json.Marshal(issue)
		if err != nil {
			continue
		}
		payload["issue"] = encoded
		if next, err := json.Marshal(payload); err == nil {
			entry.Payload = next
		}
	}
}

// movePreferences re-points label (and group) favorites and subscriptions to
// the copies in the receiving teams the user belongs to (directly or through
// a sub-team); a user in none of them loses them, as with a deletion.
func (m *labelTeamMove) movePreferences(data *domain.Bootstrap) {
	moving := func(kind, id string) bool {
		return kind == "label" && (m.moves(id) || m.group != nil && id == m.group.ID)
	}
	if !slices.ContainsFunc(data.Favorites, func(item domain.Favorite) bool { return moving(item.ResourceType, item.ResourceID) }) &&
		!slices.ContainsFunc(data.Subscriptions, func(item domain.Subscription) bool { return moving(item.ResourceType, item.ResourceID) }) {
		return
	}
	reach := map[string][]string{}
	for _, teamID := range m.receiving {
		reach[teamID] = append([]string{teamID}, teamDescendantIDs(data, teamID)...)
	}
	targets := func(userID, id string) []string {
		result := []string{}
		for _, teamID := range m.receiving {
			if !slices.ContainsFunc(data.TeamMembers, func(member domain.TeamMember) bool {
				return member.UserID == userID && slices.Contains(reach[teamID], member.TeamID)
			}) {
				continue
			}
			target := ""
			if m.group != nil && id == m.group.ID {
				target = m.groups[teamID].ID
			} else if label, ok := m.copies[teamID][id]; ok {
				target = label.ID
			}
			if target != "" && !slices.Contains(result, target) {
				result = append(result, target)
			}
		}
		return result
	}
	favorites := make([]domain.Favorite, 0, len(data.Favorites))
	for _, item := range data.Favorites {
		if !moving(item.ResourceType, item.ResourceID) {
			favorites = append(favorites, item)
			continue
		}
		keepID := true
		for _, target := range targets(item.UserID, item.ResourceID) {
			same := func(other domain.Favorite) bool {
				return other.UserID == item.UserID && other.ResourceType == "label" && other.ResourceID == target && other.Tab == item.Tab
			}
			if slices.ContainsFunc(data.Favorites, same) || slices.ContainsFunc(favorites, same) {
				continue
			}
			next := item
			next.ResourceID = target
			if !keepID {
				next.ID = idgen.New("favorite_")
			}
			keepID = false
			favorites = append(favorites, next)
		}
	}
	data.Favorites = favorites
	subscriptions := make([]domain.Subscription, 0, len(data.Subscriptions))
	for _, item := range data.Subscriptions {
		if !moving(item.ResourceType, item.ResourceID) {
			subscriptions = append(subscriptions, item)
			continue
		}
		keepID := true
		for _, target := range targets(item.UserID, item.ResourceID) {
			same := func(other domain.Subscription) bool {
				return other.UserID == item.UserID && other.ResourceType == "label" && other.ResourceID == target
			}
			if slices.ContainsFunc(data.Subscriptions, same) || slices.ContainsFunc(subscriptions, same) {
				continue
			}
			next := item
			next.ResourceID = target
			next.Events, next.OptOutEvents = slices.Clone(item.Events), slices.Clone(item.OptOutEvents)
			if !keepID {
				next.ID = idgen.New("subscription_")
			}
			keepID = false
			subscriptions = append(subscriptions, next)
		}
	}
	data.Subscriptions = subscriptions
}

// holders are the label groups now holding the moved group's labels.
func (m *labelTeamMove) holders() []domain.LabelGroup {
	result := []domain.LabelGroup{}
	for _, teamID := range m.receiving {
		if group, ok := m.groups[teamID]; ok && !slices.ContainsFunc(result, func(item domain.LabelGroup) bool { return item.ID == group.ID }) {
			result = append(result, group)
		}
	}
	for _, group := range m.ownGroups {
		if !slices.ContainsFunc(result, func(item domain.LabelGroup) bool { return item.ID == group.ID }) {
			result = append(result, group)
		}
	}
	return result
}

// labelReferencesInJSON reports whether saved filters name one of ids (bare
// or "label:"-style prefixed).
func labelReferencesInJSON(raw json.RawMessage, ids map[string]struct{}) bool {
	if len(raw) == 0 {
		return false
	}
	mentioned := false
	for id := range ids {
		if bytes.Contains(raw, []byte(id)) {
			mentioned = true
			break
		}
	}
	var value any
	if !mentioned || json.Unmarshal(raw, &value) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch typed := value.(type) {
		case string:
			return deletedLabelReference(typed, ids)
		case []any:
			return slices.ContainsFunc(typed, walk)
		case map[string]any:
			for _, item := range typed {
				if walk(item) {
					return true
				}
			}
		}
		return false
	}
	return walk(value)
}

// moveDisplayLabelGroup points a view's label group grouping at to, or drops
// it (the view then groups by its first label group) when to is empty.
func moveDisplayLabelGroup(raw json.RawMessage, from, to string) json.RawMessage {
	if len(raw) == 0 || !bytes.Contains(raw, []byte(from)) {
		return raw
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	changed := false
	for _, key := range []string{"labelGroupId", "subLabelGroupId"} {
		if value[key] != from {
			continue
		}
		if to == "" {
			delete(value, key)
		} else {
			value[key] = to
		}
		changed = true
	}
	if !changed {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}
