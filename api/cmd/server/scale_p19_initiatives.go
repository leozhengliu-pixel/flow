package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"flow/api/internal/domain"
)

func scaleP19Initiatives(data domain.Bootstrap, q scaleP19Query, page scaleP19WindowPage, offset, limit int) (scaleP19WindowPage, error) {
	byID := map[string]domain.Initiative{}
	children := map[string][]string{}
	for _, item := range data.Initiatives {
		byID[item.ID] = item
	}
	for _, item := range data.Initiatives {
		for _, parent := range item.ParentInitiativeIDs {
			if parent != item.ID {
				if _, ok := byID[parent]; ok && !slices.Contains(children[parent], item.ID) {
					children[parent] = append(children[parent], item.ID)
				}
			}
		}
	}
	walk := func(id string, parents bool) []string {
		seen := map[string]bool{id: true}
		stack := []string{id}
		result := []string{}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			next := children[current]
			if parents {
				next = byID[current].ParentInitiativeIDs
			}
			for _, id := range next {
				if _, ok := byID[id]; !ok || seen[id] {
					continue
				}
				seen[id] = true
				result = append(result, id)
				stack = append(stack, id)
			}
		}
		return result
	}
	projectIDs := func(id string) map[string]bool {
		ids := map[string]bool{}
		for _, id := range append([]string{id}, walk(id, false)...) {
			for _, project := range byID[id].ProjectIDs {
				ids[project] = true
			}
		}
		return ids
	}
	teamParents := func(id string) map[string]bool {
		result := map[string]bool{}
		for id != "" && !result[id] {
			if !slices.ContainsFunc(data.Teams, func(t domain.Team) bool { return t.ID == id }) {
				break
			}
			result[id] = true
			id = data.TeamSettings[id].ParentTeamID
		}
		return result
	}
	scope := map[string]bool{}
	if q.LeadTeamID != "" {
		for _, team := range data.Teams {
			if teamParents(team.ID)[q.LeadTeamID] {
				scope[team.ID] = true
			}
		}
	}
	scoped := []domain.Initiative{}
	for _, item := range data.Initiatives {
		if q.LeadTeamID != "" {
			led := scope[item.LeadTeamID]
			related := false
			if q.ShowContributing && !led {
				for _, team := range item.ContributingTeamIDs {
					related = related || scope[team]
				}
				for _, id := range append([]string{item.ID}, walk(item.ID, true)...) {
					for team := range teamParents(byID[id].LeadTeamID) {
						related = related || scope[team]
					}
				}
				ids := projectIDs(item.ID)
				for _, project := range data.Projects {
					if ids[project.ID] {
						for _, team := range project.TeamIDs {
							related = related || scope[team]
						}
					}
				}
			}
			if !led && !related {
				continue
			}
		}
		scoped = append(scoped, item)
	}
	page.Counts = map[string]map[string]int{}
	// Filter-menu counts retain the legacy whole-catalogue denominator.
	for _, item := range data.Initiatives {
		for field, values := range scaleP19InitiativeValues(item, q) {
			for _, value := range values {
				scaleP19Increment(page.Counts, field, value)
			}
		}
	}
	visible := []domain.Initiative{}
	visibleIDs := map[string]bool{}
	for _, item := range scoped {
		if q.View == "active" && item.Status != "active" || q.View == "planned" && item.Status != "planned" && item.Status != "proposed" {
			continue
		}
		if q.LeadTeamID == "" && !q.ShowTeamInitiatives && item.LeadTeamID != "" {
			continue
		}
		values := scaleP19InitiativeValues(item, q)
		matches := len(q.InitiativeFilters) == 0 || q.FilterMode != "any"
		for field, raw := range q.InitiativeFilters {
			var v any
			if json.Unmarshal(raw, &v) != nil {
				return page, nil
			}
			value := ""
			switch v := v.(type) {
			case string:
				value = v
			case float64:
				value = strconv.FormatFloat(v, 'f', -1, 64)
			}
			matched := slices.Contains(values[field], value)
			if q.FilterMode == "any" {
				matches = matches || matched
			} else {
				matches = matches && matched
			}
		}
		if matches {
			visible = append(visible, item)
			visibleIDs[item.ID] = true
		}
	}
	c := scaleP19Collator(q)
	slices.SortStableFunc(visible, func(a, b domain.Initiative) int {
		switch q.Sort {
		case "", "manual":
			if a.Position < b.Position {
				return -1
			}
			if a.Position > b.Position {
				return 1
			}
			return 0
		case "priority":
			return a.Priority - b.Priority
		case "status":
			order := []string{"proposed", "planned", "active", "completed", "canceled"}
			return slices.Index(order, a.Status) - slices.Index(order, b.Status)
		case "target":
			return c.CompareString(scaleP19String(a.TargetDate), scaleP19String(b.TargetDate))
		case "health":
			return c.CompareString(a.Health, b.Health)
		case "created":
			return c.CompareString(a.CreatedAt.Format(time.RFC3339Nano), b.CreatedAt.Format(time.RFC3339Nano))
		case "updated":
			return c.CompareString(a.UpdatedAt.Format(time.RFC3339Nano), b.UpdatedAt.Format(time.RFC3339Nano))
		}
		return c.CompareString(a.Name, b.Name)
	})
	page.Aggregates = map[string]scaleP19Aggregate{}
	// Sidebar counts use the filtered visible set, before contextual parents.
	for _, item := range visible {
		for field, values := range scaleP19InitiativeValues(item, q) {
			for _, value := range values {
				scaleP19Increment(page.Counts, "visible:"+field, value)
			}
		}
	}
	items := slices.Clone(visible)
	included := map[string]bool{}
	for _, item := range items {
		included[item.ID] = true
	}
	if q.ShowParents && q.ShowNested && (q.Grouping == "" || q.Grouping == "none") {
		for _, item := range visible {
			for _, id := range walk(item.ID, true) {
				if !included[id] {
					items = append(items, byID[id])
					included[id] = true
				}
			}
		}
	}
	type group struct {
		id, label string
		items     []domain.Initiative
	}
	groups := []group{}
	buckets := map[string]int{}
	add := func(id, label string, item domain.Initiative) {
		index, ok := buckets[id]
		if !ok {
			index = len(groups)
			buckets[id] = index
			groups = append(groups, group{id: id, label: label})
		}
		groups[index].items = append(groups[index].items, item)
	}
	for _, item := range items {
		switch q.Grouping {
		case "", "none":
			add("all", "", item)
		case "owner":
			if item.Owner != nil {
				add(item.Owner.ID, item.Owner.DisplayName, item)
			} else {
				add("none", "No owner", item)
			}
		case "leadTeam":
			index := slices.IndexFunc(data.Teams, func(t domain.Team) bool { return t.ID == item.LeadTeamID })
			if index >= 0 {
				add(data.Teams[index].ID, data.Teams[index].Name, item)
			} else {
				add("none", "No lead team", item)
			}
		case "contributingTeam":
			found := false
			for _, team := range data.Teams {
				if slices.Contains(item.ContributingTeamIDs, team.ID) {
					add(team.ID, team.Name, item)
					found = true
				}
			}
			if !found {
				add("none", "No contributing team", item)
			}
		case "label":
			found := false
			for _, label := range data.Labels {
				if slices.Contains(item.LabelIDs, label.ID) {
					add(label.ID, label.Name, item)
					found = true
				}
			}
			if !found {
				add("none", "No labels", item)
			}
		case "health":
			add(item.Health, map[string]string{"onTrack": "On track", "atRisk": "At risk", "offTrack": "Off track", "noUpdate": "No updates"}[item.Health], item)
		case "status":
			label := item.Status
			if len(label) > 0 {
				label = string(label[0]-32) + label[1:]
			}
			add(item.Status, label, item)
		case "priority":
			add(strconv.Itoa(item.Priority), item.PriorityLabel, item)
		}
	}
	entries := []scaleP19Entry{}
	for _, g := range groups {
		if q.Grouping != "" && q.Grouping != "none" {
			entries = append(entries, scaleP19Entry{Key: "group:" + g.id, Kind: "group", Label: g.label, Count: len(g.items), EntityName: slices.Contains([]string{"owner", "leadTeam", "contributingTeam", "label"}, q.Grouping)})
		}
		appendRow := func(item domain.Initiative, depth, childCount int) {
			entries = append(entries, scaleP19Entry{Key: "initiative:" + g.id + ":" + item.ID, Kind: "initiative", ID: item.ID, Depth: depth, ChildCount: childCount, ContextOnly: !visibleIDs[item.ID]})
		}
		if !q.ShowNested {
			for _, item := range g.items {
				appendRow(item, 0, 0)
			}
			continue
		}
		rank := map[string]int{}
		for i, item := range g.items {
			rank[item.ID] = i
		}
		visited := map[string]bool{}
		var visit func(string, int)
		visit = func(id string, depth int) {
			if visited[id] {
				return
			}
			visited[id] = true
			ids := []string{}
			for _, child := range children[id] {
				if _, ok := rank[child]; ok {
					ids = append(ids, child)
				}
			}
			slices.SortStableFunc(ids, func(a, b string) int { return rank[a] - rank[b] })
			appendRow(byID[id], depth, len(ids))
			if !slices.Contains(q.Collapsed, id) {
				for _, child := range ids {
					visit(child, depth+1)
				}
			}
		}
		roots := []string{}
		covered := map[string]bool{}
		for _, item := range g.items {
			if !slices.ContainsFunc(item.ParentInitiativeIDs, func(parent string) bool { _, ok := rank[parent]; return ok }) {
				roots = append(roots, item.ID)
				covered[item.ID] = true
				for _, id := range walk(item.ID, false) {
					covered[id] = true
				}
			}
		}
		for _, id := range roots {
			visit(id, 0)
		}
		for _, item := range g.items {
			if !covered[item.ID] {
				visit(item.ID, 0)
			}
		}
	}
	total := len(entries) - scaleP19GroupEntryCount(entries)
	page.Total = total
	page.Entries = []scaleP19Entry{}
	position := 0
	var heading *scaleP19Entry
	for _, entry := range entries {
		if entry.Kind == "group" {
			copy := entry
			heading = &copy
			continue
		}
		if position >= offset && position < offset+limit {
			if heading != nil {
				page.Entries = append(page.Entries, *heading)
				heading = nil
			}
			page.Entries = append(page.Entries, entry)
		}
		position++
	}
	page.HasMore = offset+scaleP19PageCount(scaleP19WindowPage{Items: []domain.Initiative{}, Entries: page.Entries}) < total
	result := []domain.Initiative{}
	emitted := map[string]bool{}
	for _, entry := range page.Entries {
		if entry.Kind != "initiative" || emitted[entry.ID] {
			continue
		}
		emitted[entry.ID] = true
		item := byID[entry.ID]
		ids := projectIDs(item.ID)
		aggregate := scaleP19Aggregate{HasUpdates: len(data.InitiativeUpdates[item.ID]) > 0 || item.Health != "noUpdate"}
		for _, project := range data.Projects {
			if !ids[project.ID] {
				continue
			}
			aggregate.Projects++
			if project.Status.Type == "completed" {
				aggregate.Completed++
			}
			if project.Status.Type != "completed" && project.Status.Type != "canceled" {
				updates, loaded := data.ProjectUpdates[project.ID]
				has := project.Health != "noUpdate"
				if loaded {
					has = len(updates) > 0
				}
				if !has {
					aggregate.NeedingUpdate++
				}
			}
		}
		page.Aggregates[item.ID] = aggregate
		// Relationships and bodies belong to detail; the row's aggregates above
		// are complete even when an initiative references thousands of projects.
		item.Description = ""
		item.DescriptionHistory = nil
		item.Resources = nil
		item.Comments = nil
		item.CommentAttachments = nil
		item.ProjectIDs = nil
		result = append(result, item)
	}
	page.Items = result
	return page, nil
}

func scaleP19InitiativeValues(item domain.Initiative, q scaleP19Query) map[string][]string {
	owner := ""
	if item.Owner != nil {
		owner = item.Owner.ID
	}
	dates := []string{}
	now := scaleP19Now(q)
	if !item.CreatedAt.Before(now.Add(-7 * 24 * time.Hour)) {
		dates = append(dates, "created7")
	}
	if !item.UpdatedAt.Before(now.Add(-7 * 24 * time.Hour)) {
		dates = append(dates, "updated7")
	}
	if item.Status == "completed" {
		dates = append(dates, "completed")
	}
	month := q.LocalMonth
	if _, err := time.Parse("2006-01", month); err != nil {
		month = now.Format("2006-01")
	}
	if target, err := time.Parse("2006-01-02", scaleP19String(item.TargetDate)); err == nil && target.Format("2006-01") == month {
		dates = append(dates, "targetMonth")
	}
	return map[string][]string{"ownerId": {owner}, "creatorId": {item.Creator.ID}, "leadTeamId": {item.LeadTeamID}, "teamId": item.ContributingTeamIDs, "status": {item.Status}, "priority": {strconv.Itoa(item.Priority)}, "health": {item.Health}, "labelId": item.LabelIDs, "date": dates}
}
