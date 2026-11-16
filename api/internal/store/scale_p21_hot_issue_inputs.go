package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

// issueHotInputs owns only the installed records the supported issue update
// callbacks can inspect. It is trusted storage input, not authorization.
type issueHotInputs struct {
	data    domain.Bootstrap
	store   *SQLiteStore
	source  *metadataLogicalSource
	release []func()
	// move is the label closure of an update that moves the target into
	// moveTeam (issueTeamMove: labels named like the target's), selected
	// with the referenced labels in one pass.
	move     *issueTeamMove
	moveTeam string
}

func (r *issueHotInputs) close() {
	r.data = domain.Bootstrap{}
	if r.source != nil {
		r.source.Release()
		r.source = nil
	}
	for _, release := range r.release {
		release()
	}
	r.release = nil
}

func (r *issueHotInputs) reserve(ctx context.Context, cost uint64) error {
	if cost > metadataWorkBytes {
		return ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err == nil {
		r.release = append(r.release, release)
	}
	return err
}

func (r *issueHotInputs) keys(ctx context.Context, values ...string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, value := range values {
		if err := r.addKey(ctx, result, value); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *issueHotInputs) addKey(ctx context.Context, keys map[string]bool, value string) error {
	if keys[value] {
		return nil
	}
	if err := r.reserve(ctx, 512); err != nil {
		return err
	}
	keys[value] = true
	return nil
}

func issueHotField(ctx context.Context, record metadataLogicalNode, name string) (metadataLogicalNode, error) {
	var result metadataLogicalNode
	err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if key.String() == name {
			result = node
		}
		return nil
	})
	return result, err
}

func issueHotMatches(ctx context.Context, record metadataLogicalNode, name string, keys map[string]bool) (bool, error) {
	node, err := issueHotField(ctx, record, name)
	if err != nil {
		return false, err
	}
	for key := range keys {
		match, err := node.StringEqual(ctx, key)
		if err != nil || match {
			return match, err
		}
	}
	return false, nil
}

func issueHotProjectionCost(ctx context.Context, record metadataLogicalNode, names []string) (uint64, error) {
	if len(names) == 0 {
		if record.cost > (metadataWorkBytes-4096)/8 {
			return 0, ErrMetadataWorkBudget
		}
		return record.cost*8 + 4096, nil
	}
	cost := uint64(4096)
	err := record.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if slices.Contains(names, key.String()) {
			if child.cost > (metadataWorkBytes-cost)/8 {
				return ErrMetadataWorkBudget
			}
			cost += child.cost * 8
		}
		return nil
	})
	return cost, err
}

func issueHotProjection(ctx context.Context, record metadataLogicalNode, names []string, cost uint64) (reflect.Value, error) {
	if len(names) == 0 {
		return record.Value(ctx, int(record.cost))
	}
	result := reflect.New(record.typ).Elem()
	err := record.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !slices.Contains(names, key.String()) {
			return nil
		}
		value, err := child.Value(ctx, int(cost))
		if err == nil {
			result.FieldByName(key.String()).Set(value)
		}
		return err
	})
	return result, err
}

func (r *issueHotInputs) withHeader(ctx context.Context, record metadataLogicalNode, names []string, visit func(reflect.Value) (bool, error)) (bool, error) {
	cost, err := issueHotProjectionCost(ctx, record, names)
	if err != nil {
		return false, err
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(ctx, int(cost))
	if err != nil {
		return false, err
	}
	defer release()
	value, err := issueHotProjection(ctx, record, names, cost)
	if err != nil {
		return false, err
	}
	return visit(value)
}

func (r *issueHotInputs) selectSlice(ctx context.Context, field string, match func(metadataLogicalNode) (bool, error), names ...string) error {
	index, _, _ := metadataLogicalField(field)
	target := reflect.ValueOf(&r.data).Elem().Field(index)
	return r.source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		wanted, err := match(record)
		if err != nil || !wanted {
			return err
		}
		cost, err := issueHotProjectionCost(ctx, record, names)
		if err != nil {
			return err
		}
		if err := r.reserve(ctx, cost); err != nil {
			return err
		}
		value, err := issueHotProjection(ctx, record, names, cost)
		if err == nil {
			target.Set(reflect.Append(target, value))
		}
		return err
	})
}

func (r *issueHotInputs) selectMap(ctx context.Context, field string, keys map[string]bool, names ...string) error {
	index, _, _ := metadataLogicalField(field)
	target := reflect.ValueOf(&r.data).Elem().Field(index)
	return r.source.fields[index].MapEntries(ctx, func(key, record metadataLogicalNode) error {
		for id := range keys {
			wanted, err := key.StringEqual(ctx, id)
			if err != nil {
				return err
			}
			if !wanted {
				continue
			}
			cost, err := issueHotProjectionCost(ctx, record, names)
			if err != nil {
				return err
			}
			if err := r.reserve(ctx, cost); err != nil {
				return err
			}
			value, err := issueHotProjection(ctx, record, names, cost)
			if err != nil {
				return err
			}
			if target.IsNil() {
				target.Set(reflect.MakeMap(target.Type()))
			}
			target.SetMapIndex(reflect.ValueOf(id), value)
			return nil
		}
		return nil
	})
}

// issueHotMentionIDs follows collectMentionIDs for the supported description
// input producers. RawMessage retains the same standard JSON interpretation.
func issueHotMentionIDs(value any, add func(string) error) error {
	switch value := value.(type) {
	case map[string]any:
		attrs, _ := value["attrs"].(map[string]any)
		mention := value["type"] == "mention" && domain.IsUserMention(attrs) || value["type"] == "userMention" || value["type"] == "user"
		if mention {
			for _, node := range []map[string]any{value, attrs} {
				for _, key := range []string{"userId", "id"} {
					if id, ok := node[key].(string); ok && id != "" {
						if err := add(id); err != nil {
							return err
						}
					}
				}
			}
		}
		for _, child := range value {
			if err := issueHotMentionIDs(child, add); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := issueHotMentionIDs(child, add); err != nil {
				return err
			}
		}
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(value, &decoded) == nil {
			return issueHotMentionIDs(decoded, add)
		}
	}
	return nil
}

func (s *SQLiteStore) loadIssueHotInputs(ctx context.Context, workspace string, data *domain.Bootstrap, target domain.Issue, input domain.IssueUpdateInput) (_ *issueHotInputs, err error) {
	if !issueHotInputsSupported(data.Issues, target, input) {
		return nil, ErrBoundedMetadataCoverage
	}
	return s.loadIssueSelectedInputs(ctx, workspace, data, target, input)
}

func (s *SQLiteStore) loadIssueSelectedInputs(ctx context.Context, workspace string, data *domain.Bootstrap, target domain.Issue, input domain.IssueUpdateInput) (_ *issueHotInputs, err error) {
	if _, found := s.storedWorkspace(workspace); !found {
		return nil, sql.ErrNoRows
	}
	read := &issueHotInputs{store: s}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	if err := read.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	read.source, err = s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return nil, err
	}
	users, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return nil, err
	}
	teams, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	labels, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range data.Issues {
		if err := read.addKey(ctx, teams, issue.Team.ID); err != nil {
			return nil, err
		}
		for _, user := range []*domain.User{&issue.Creator, issue.Assignee, issue.Delegate} {
			if user != nil {
				if err := read.addKey(ctx, users, user.ID); err != nil {
					return nil, err
				}
			}
		}
		for _, id := range issue.SubscriberIDs {
			if err := read.addKey(ctx, users, id); err != nil {
				return nil, err
			}
		}
		for _, label := range issue.Labels {
			if err := read.addKey(ctx, labels, label.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, id := range []*string{input.AssigneeID, input.DelegateID} {
		if id != nil {
			if err := read.addKey(ctx, users, *id); err != nil {
				return nil, err
			}
		}
	}
	for _, pair := range []struct {
		keys map[string]bool
		id   *string
	}{{teams, input.TeamID}} {
		if pair.id != nil {
			if err := read.addKey(ctx, pair.keys, *pair.id); err != nil {
				return nil, err
			}
		}
	}
	for _, pair := range []struct {
		keys map[string]bool
		ids  *[]string
	}{{users, input.SubscriberIDs}, {labels, input.LabelIDs}} {
		if pair.ids != nil {
			for _, id := range *pair.ids {
				if err := read.addKey(ctx, pair.keys, id); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := issueHotMentionIDs(input.DescriptionData, func(id string) error { return read.addKey(ctx, users, id) }); err != nil {
		return nil, err
	}
	if input.TeamID != nil && *input.TeamID != target.Team.ID {
		// moveIssueToTeam replaces each label the destination cannot use by
		// the first same-named label it can: select every label named like
		// one the target carries (issueTeamMove), in collection order.
		read.moveTeam = *input.TeamID
		if read.move, err = read.prepareIssueTeamMove(ctx, []domain.Issue{target}, nil, []string{*input.TeamID}, nil, false); err != nil {
			return nil, err
		}
	}
	changedDescription := input.Description != nil || input.DescriptionData != nil || input.DescriptionState != nil || input.ContentState != nil
	text := ""
	if changedDescription {
		text = target.Description
		if input.Description != nil {
			text = *input.Description
		}
	}
	if uint64(len(text)) > (metadataWorkBytes-4096)/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := read.reserve(ctx, uint64(len(text))*8+4096); err != nil {
		return nil, err
	}
	lower := strings.ToLower(text)
	err = read.selectSlice(ctx, "users", func(record metadataLogicalNode) (bool, error) {
		matched, err := issueHotMatches(ctx, record, "ID", users)
		if err != nil || matched || !changedDescription {
			return matched, err
		}
		return read.withHeader(ctx, record, []string{"Name", "Email", "DisplayName"}, func(value reflect.Value) (bool, error) {
			user := value.Interface().(domain.User)
			return strings.Contains(lower, "@"+strings.ToLower(user.Email)) ||
				strings.Contains(lower, "@"+strings.ToLower(user.Name)) ||
				strings.Contains(lower, "@"+strings.ToLower(strings.ReplaceAll(user.DisplayName, " ", ""))), nil
		})
	})
	if err != nil {
		return nil, err
	}
	// Newly found text mentions also need preferences and push state.
	for _, user := range read.data.Users {
		if err := read.addKey(ctx, users, user.ID); err != nil {
			return nil, err
		}
	}
	if err := read.selectMap(ctx, "notificationPreferences", users); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "pushSubscriptions", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "UserID", users)
	}, "UserID", "Enabled"); err != nil {
		return nil, err
	}
	viewer, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return nil, err
	}
	if err := read.selectMap(ctx, "userSettings", viewer, "AssignStarted"); err != nil {
		return nil, err
	}
	if err := read.selectMap(ctx, "cycleSettings", teams); err != nil {
		return nil, err
	}
	cycles, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	if input.CycleID != nil {
		if err := read.addKey(ctx, cycles, *input.CycleID); err != nil {
			return nil, err
		}
	}
	if err := read.selectSlice(ctx, "cycles", func(record metadataLogicalNode) (bool, error) {
		matched, err := issueHotMatches(ctx, record, "ID", cycles)
		if err != nil || matched {
			return matched, err
		}
		matched, err = issueHotMatches(ctx, record, "TeamID", teams)
		if err != nil || !matched {
			return false, err
		}
		state, err := issueHotField(ctx, record, "Status")
		if err != nil {
			return false, err
		}
		return state.StringEqual(ctx, "current")
	}, "ID", "TeamID", "Status", "StartsAt", "EndsAt"); err != nil {
		return nil, err
	}
	// Further reference families are selected before this owner is installed
	// into the callback, so an unavailable input cannot become an empty one.
	if err := read.loadReferences(ctx, data, target, users, labels); err != nil {
		return nil, err
	}
	return read, ctx.Err()
}

func issueHotInputsSupported(issues []domain.Issue, target domain.Issue, input domain.IssueUpdateInput) bool {
	if input.ProjectID != nil || input.ProjectMilestoneID != nil || input.TeamID != nil && *input.TeamID != target.Team.ID ||
		input.ContentState != nil || input.DescriptionState != nil {
		return false
	}
	for _, issue := range issues {
		if issue.Project != nil {
			return false
		}
	}
	return true
}

func (r *issueHotInputs) loadReferences(ctx context.Context, data *domain.Bootstrap, target domain.Issue, users, labels map[string]bool) error {
	// Its only suggestion operation, here or in a team move, removes every
	// target suggestion before clearing the target's derived arrays.
	// Suggestion and SLA-event metadata changes are discarded by this writer.
	if err := r.selectSlice(ctx, "labels", func(record metadataLogicalNode) (bool, error) {
		matched, err := issueHotMatches(ctx, record, "ID", labels)
		if err != nil || matched || r.move == nil {
			return matched, err
		}
		return r.move.label(ctx, r, record)
	}); err != nil {
		return err
	}
	groups, err := r.keys(ctx)
	if err != nil {
		return err
	}
	for _, label := range r.data.Labels {
		if err := r.addKey(ctx, groups, label.GroupID); err != nil {
			return err
		}
	}
	if err := r.selectSlice(ctx, "labelGroups", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", groups)
	}, "ID", "ArchivedAt"); err != nil {
		return err
	}
	sla, err := r.keys(ctx, "sla")
	if err != nil {
		return err
	}
	if err := r.selectMap(ctx, "settings", sla); err != nil {
		return err
	}
	issues, err := r.keys(ctx)
	if err != nil {
		return err
	}
	for _, issue := range data.Issues {
		if err := r.addKey(ctx, issues, issue.ID); err != nil {
			return err
		}
	}
	if err := r.selectSlice(ctx, "issueSlas", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "IssueID", issues)
	}); err != nil {
		return err
	}
	rules, err := r.keys(ctx)
	if err != nil {
		return err
	}
	for _, sla := range r.data.IssueSLAs {
		if sla.IssueID == target.ID {
			if err := r.addKey(ctx, rules, sla.RuleID); err != nil {
				return err
			}
		}
	}
	if err := r.selectSlice(ctx, "slaRules", func(record metadataLogicalNode) (bool, error) {
		matched, err := issueHotMatches(ctx, record, "ID", rules)
		if err != nil || matched {
			return matched, err
		}
		return r.withHeader(ctx, record, []string{"Enabled", "TeamIDs"}, func(value reflect.Value) (bool, error) {
			rule := value.Interface().(domain.SLARule)
			// A moved issue is matched in its destination team.
			return rule.Enabled && (len(rule.TeamIDs) == 0 || slices.Contains(rule.TeamIDs, target.Team.ID) ||
				r.moveTeam != "" && slices.Contains(rule.TeamIDs, r.moveTeam)), nil
		})
	}); err != nil {
		return err
	}
	// Only relevant suspended members matter to the no-SQL-memberships
	// development fallback. A team move replaces them with the live member
	// and team-member rows of these users (issueMoveAudience).
	return r.selectSlice(ctx, "members", func(record metadataLogicalNode) (bool, error) {
		user, err := issueHotField(ctx, record, "User")
		if err != nil {
			return false, err
		}
		return issueHotMatches(ctx, user, "ID", users)
	}, "User", "Status")
}

func (r *issueHotInputs) install(data, baseline *domain.Bootstrap) {
	input := reflect.ValueOf(r.data)
	target := reflect.ValueOf(data).Elem()
	stored := reflect.ValueOf(baseline).Elem()
	for _, name := range []string{
		"users", "notificationPreferences", "pushSubscriptions", "userSettings",
		"cycleSettings", "cycles", "labels", "labelGroups", "settings",
		"issueSlas", "slaRules", "members",
	} {
		index, _, _ := metadataLogicalField(name)
		value := input.Field(index)
		target.Field(index).Set(value)
		stored.Field(index).Set(cloneSnapshotValue(value))
	}
}
