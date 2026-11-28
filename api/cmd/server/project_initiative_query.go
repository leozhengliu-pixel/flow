package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

const scaleCapability = "query-windows-v1"

type scaleDisplay struct {
	Layout               string `json:"layout"`
	Grouping             string `json:"grouping"`
	GroupOrder           string `json:"groupOrder"`
	SubGrouping          string `json:"subGrouping"`
	Ordering             string `json:"ordering"`
	OrderingDirection    string `json:"orderingDirection"`
	ShowClosed           string `json:"showClosed"`
	ShowEmptyGroups      bool   `json:"showEmptyGroups"`
	OnlyLeadTeamProjects bool   `json:"onlyLeadTeamProjects"`
}

type scaleQuery struct {
	Schema     int                            `json:"schema"`
	TeamIDs    []string                       `json:"teamIds,omitempty"`
	Filters    []store.ProjectDirectoryFilter `json:"filters,omitempty"`
	Search     string                         `json:"search,omitempty"`
	Display    scaleDisplay                   `json:"display"`
	LeadTeamID string                         `json:"leadTeamId,omitempty"`
	Insight    *struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"insight,omitempty"`
	Locale              string                     `json:"locale,omitempty"`
	DateLocale          string                     `json:"dateLocale,omitempty"`
	Now                 string                     `json:"now,omitempty"`
	LocalMonth          string                     `json:"localMonth,omitempty"`
	View                string                     `json:"view,omitempty"`
	InitiativeFilters   map[string]json.RawMessage `json:"initiativeFilters,omitempty"`
	FilterMode          string                     `json:"filterMode,omitempty"`
	Sort                string                     `json:"sort,omitempty"`
	Grouping            string                     `json:"grouping,omitempty"`
	ShowNested          bool                       `json:"showNested"`
	ShowParents         bool                       `json:"showParents"`
	ShowTeamInitiatives bool                       `json:"showTeamInitiatives"`
	ShowContributing    bool                       `json:"showContributing"`
	Collapsed           []string                   `json:"collapsed,omitempty"`
}

type scaleGroup struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	ParentID string           `json:"parentId,omitempty"`
	Total    int              `json:"total"`
	Projects []domain.Project `json:"-"`
}

type scaleAggregate struct {
	Projects      int  `json:"projects"`
	Completed     int  `json:"completed"`
	NeedingUpdate int  `json:"needingUpdate"`
	HasUpdates    bool `json:"hasUpdates"`
}

type scaleEntry struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	ID          string `json:"id,omitempty"`
	Label       string `json:"label,omitempty"`
	Count       int    `json:"count,omitempty"`
	EntityName  bool   `json:"entityName,omitempty"`
	Depth       int    `json:"depth"`
	ChildCount  int    `json:"childCount"`
	ContextOnly bool   `json:"contextOnly,omitempty"`
}

type scaleWindowPage struct {
	Schema            int                                      `json:"schema"`
	Items             any                                      `json:"items"`
	NextCursor        string                                   `json:"nextCursor,omitempty"`
	HasMore           bool                                     `json:"hasMore"`
	Total             int                                      `json:"total"`
	Offset            int                                      `json:"offset"`
	Revision          string                                   `json:"revision"`
	Versions          map[string]string                        `json:"versions"`
	AuthzEpoch        string                                   `json:"authzEpoch"`
	Groups            []scaleGroup                             `json:"groups,omitempty"`
	Entries           []scaleEntry                             `json:"entries"`
	Aggregates        map[string]scaleAggregate                `json:"aggregates"`
	Counts            map[string]map[string]int                `json:"counts"`
	Timeline          []string                                 `json:"timeline,omitempty"`
	CustomerSummaries map[string]*store.ProjectCustomerSummary `json:"customerSummaries,omitempty"`
	Customers         []domain.Customer                        `json:"customers,omitempty"`
	LatestUpdates     map[string]time.Time                     `json:"latestUpdates,omitempty"`
	ProjectRelations  []domain.ProjectRelation                 `json:"projectRelations,omitempty"`
	Insights          map[string][]scaleInsightRow             `json:"insights,omitempty"`
}

type scaleInsightRow struct {
	Value     string            `json:"value"`
	Label     string            `json:"label"`
	Count     int               `json:"count"`
	ClassName string            `json:"className,omitempty"`
	Lead      *scaleInsightLead `json:"lead,omitempty"`
}
type scaleInsightLead struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl,omitempty"`
}

// This is an opt-in extension of the sync page shape. Old requests never
// enter it. Filtering and aggregates consume the existing permission adapter.
func (s *server) scaleWindow(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	var query scaleQuery
	if len(values.Get("window")) > 64<<10 || json.Unmarshal([]byte(values.Get("window")), &query) != nil || query.Schema != 1 || !slices.Contains([]string{"projects", "initiatives"}, values.Get("kind")) {
		writeSyncError(w, store.ErrMetadataRecordQuery)
		return
	}
	if query.Now == "" {
		query.Now = time.Now().UTC().Format(time.RFC3339Nano)
	}
	limit, err := syncPageNumber(values.Get("limit"), 100, 1, 100)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	access, ok := s.syncAccess(w, r)
	if !ok {
		return
	}
	snapshot, ok := s.queryWindowMetadataScaleMetadata(w, r)
	if !ok {
		return
	}
	data := snapshot.Data
	revision := snapshot.Revision
	binding := values.Get("window") + ":" + revision
	position, err := s.store.OpenScaleCursor(r.Context(), access, values.Get("kind"), binding, values.Get("cursor"))
	if err != nil {
		writeSyncError(w, err)
		return
	}
	offset := 0
	if position != "" {
		offset, err = strconv.Atoi(position)
		if err != nil || offset < 0 {
			writeSyncError(w, store.ErrMetadataCursor)
			return
		}
	}
	page := scaleWindowPage{Schema: 1, Revision: revision, AuthzEpoch: access.AuthzEpoch, Offset: offset}
	if values.Get("kind") == "projects" {
		page, err = s.scaleProjects(r, data, query, page, offset, limit)
	} else {
		page, err = scaleInitiatives(data, query, page, offset, limit)
	}
	if err != nil {
		writeSyncError(w, err)
		return
	}
	page.Versions = map[string]string{}
	if values.Get("kind") == "projects" {
		for _, item := range page.Items.([]domain.Project) {
			page.Versions[item.ID] = snapshot.Versions["projects"][item.ID]
		}
	} else {
		for _, item := range page.Items.([]domain.Initiative) {
			page.Versions[item.ID] = snapshot.Versions["initiatives"][item.ID]
		}
	}
	for _, version := range page.Versions {
		if version == "" {
			writeSyncError(w, store.ErrRecordJournalUnavailable)
			return
		}
	}
	// Page size is a wire budget, independent of row count. Shrinking a page
	// never silently truncates a record or advances past an unreturned row.
	for {
		count := scalePageCount(page)
		if page.HasMore {
			page.NextCursor, err = s.store.SealScaleCursor(r.Context(), access, values.Get("kind"), binding, strconv.Itoa(offset+count))
			if err != nil {
				writeSyncError(w, err)
				return
			}
		}
		raw, _ := json.Marshal(page)
		if len(raw)+1 <= store.MetadataRecordPageMaxBytes {
			break
		}
		if count <= 1 {
			writeSyncError(w, store.ErrMetadataPageBudget)
			return
		}
		scaleTrimPage(&page, count/2)
	}
	if err := s.store.ValidateAccess(r.Context(), access); err != nil {
		writeSyncError(w, err)
		return
	}
	s.writeSyncJSON(w, r, page, store.MetadataRecordPageMaxBytes)
}

func scalePageCount(page scaleWindowPage) int {
	switch items := page.Items.(type) {
	case []domain.Project:
		return len(items)
	case []domain.Initiative:
		return len(page.Entries) - scaleGroupEntryCount(page.Entries)
	default:
		return 0
	}
}
func scaleGroupEntryCount(entries []scaleEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.Kind == "group" {
			count++
		}
	}
	return count
}
func scaleTrimPage(page *scaleWindowPage, count int) {
	page.HasMore = true
	switch items := page.Items.(type) {
	case []domain.Project:
		page.Items = items[:count]
		ids := map[string]bool{}
		for _, item := range items[:count] {
			ids[item.ID] = true
		}
		for id := range page.CustomerSummaries {
			if !ids[id] {
				delete(page.CustomerSummaries, id)
			}
		}
		for id := range page.LatestUpdates {
			if !ids[id] {
				delete(page.LatestUpdates, id)
			}
		}
	case []domain.Initiative:
		entries := []scaleEntry{}
		ids := map[string]bool{}
		n := 0
		for _, entry := range page.Entries {
			if entry.Kind == "initiative" {
				if n == count {
					break
				}
				n++
				ids[entry.ID] = true
			}
			entries = append(entries, entry)
		}
		for len(entries) > 0 && entries[len(entries)-1].Kind == "group" {
			entries = entries[:len(entries)-1]
		}
		page.Entries = entries
		page.Items = slices.DeleteFunc(slices.Clone(items), func(item domain.Initiative) bool { return !ids[item.ID] })
		for id := range page.Aggregates {
			if !ids[id] {
				delete(page.Aggregates, id)
			}
		}
	}
}

func scaleNow(query scaleQuery) time.Time {
	if now, err := time.Parse(time.RFC3339Nano, query.Now); err == nil {
		return now
	}
	return time.Now().UTC()
}
func scaleString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func scaleMonth(date time.Time, locales []string) string {
	if slices.Contains(locales, "zh-CN") {
		return fmt.Sprintf("%d年%d月", date.Year(), int(date.Month()))
	}
	return date.Format("Jan 2006")
}
func scaleCollator(query scaleQuery) *collate.Collator {
	tag, err := language.Parse(query.Locale)
	if err != nil || query.Locale == "" {
		tag = language.English
	}
	return collate.New(tag)
}

func (s *server) scaleProjects(r *http.Request, data domain.Bootstrap, query scaleQuery, page scaleWindowPage, offset, limit int) (scaleWindowPage, error) {
	customers := map[string]*store.ProjectCustomerSummary{}
	if enabled, present := data.WorkspaceSettings.FeatureFlags["customer-requests"]; (!present || enabled) && data.ViewerRole != "guest" {
		_, access, err := s.store.IssueQueryAccess(r.Context(), workspaceKey(r), authUser(r).ID)
		if err != nil {
			return page, err
		}
		q := store.IssueRecordQuery{Workspace: workspaceKey(r), Access: &access}
		if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
			if key.Actor == "app" && !apiKeyHasScope(key, "customer:read") && !apiKeyHasScope(key, "customer:write") {
				goto noCustomers
			}
			if apiKeyTeamRestrictionSelected(key) {
				q.AllowedTeamIDs = append([]string{}, key.TeamIDs...)
			}
		}
		customers, err = s.store.ScopedProjectCustomerSummaries(r.Context(), q)
		if err != nil {
			return page, err
		}
	}
noCustomers:
	projects := []domain.Project{}
	counts := map[string]map[string]int{}
	latest := map[string]time.Time{}
	leadRows := []scaleInsightRow{}
	leadIndex := map[string]int{}
	for _, project := range data.Projects {
		if len(query.TeamIDs) > 0 && !slices.ContainsFunc(project.TeamIDs, func(id string) bool { return slices.Contains(query.TeamIDs, id) }) {
			continue
		}
		if query.Display.OnlyLeadTeamProjects && query.LeadTeamID != "" && (len(project.TeamIDs) == 0 || project.TeamIDs[0] != query.LeadTeamID) {
			continue
		}
		baseQuery := query
		baseQuery.Insight = nil
		baseQuery.Display.ShowClosed = "All"
		if !scaleProjectMatches(project, baseQuery, customers[project.ID], data.ProjectUpdates[project.ID]) {
			continue
		}
		for field, values := range scaleProjectValues(project, query) {
			if field == "project" {
				continue
			}
			for _, value := range values {
				scaleIncrement(counts, field, value)
			}
		}
		leadID, label := "", "No lead"
		var lead *scaleInsightLead
		if project.Lead != nil {
			leadID, label = project.Lead.ID, project.Lead.DisplayName
			lead = &scaleInsightLead{ID: leadID, Name: label, AvatarURL: project.Lead.AvatarURL}
		}
		index, known := leadIndex[leadID]
		if !known {
			index = len(leadRows)
			leadIndex[leadID] = index
			leadRows = append(leadRows, scaleInsightRow{Value: leadID, Label: label, Lead: lead})
		}
		leadRows[index].Count++
		if !scaleProjectMatches(project, query, customers[project.ID], data.ProjectUpdates[project.ID]) {
			continue
		}
		project = store.ProjectListProjection(project)
		projects = append(projects, project)
		if updates := data.ProjectUpdates[project.ID]; len(updates) > 0 {
			latest[project.ID] = updates[0].CreatedAt
		}
	}
	page.Counts = counts
	page.Insights = map[string][]scaleInsightRow{"leads": leadRows, "health": {}}
	for _, row := range []scaleInsightRow{{Value: "no-update", Label: "No update expected", ClassName: "is-no-update"}, {Value: "on-track", Label: "On track", ClassName: "is-on-track"}, {Value: "at-risk", Label: "At risk", ClassName: "is-at-risk"}, {Value: "off-track", Label: "Off track", ClassName: "is-off-track"}} {
		row.Count = counts["health"][row.Value]
		if row.Count > 0 {
			page.Insights["health"] = append(page.Insights["health"], row)
		}
	}
	groups := scaleProjectGroups(projects, query.Display.Grouping, query.Display.ShowEmptyGroups, data, query)
	if query.Display.GroupOrder == "desc" {
		slices.Reverse(groups)
	}
	flat := []domain.Project{}
	for _, group := range groups {
		page.Groups = append(page.Groups, scaleGroup{ID: group.ID, Name: group.Name, Total: len(group.Projects)})
		if query.Display.SubGrouping != "" && query.Display.SubGrouping != "No grouping" && query.Display.SubGrouping != query.Display.Grouping {
			for _, sub := range scaleProjectGroups(group.Projects, query.Display.SubGrouping, query.Display.ShowEmptyGroups, data, query) {
				page.Groups = append(page.Groups, scaleGroup{ID: sub.ID, Name: sub.Name, ParentID: group.ID, Total: len(sub.Projects)})
				scaleSortProjects(sub.Projects, query, data, customers, latest)
				flat = append(flat, sub.Projects...)
			}
		} else {
			scaleSortProjects(group.Projects, query, data, customers, latest)
			flat = append(flat, group.Projects...)
		}
	}
	page.Total = len(flat)
	start, end := "", ""
	for _, project := range flat {
		dates := []string{}
		from, fromErr := time.Parse("2006-01-02", scaleString(project.StartDate))
		to, toErr := time.Parse("2006-01-02", scaleString(project.TargetDate))
		if fromErr == nil || toErr == nil {
			if fromErr != nil {
				from = to
			}
			if toErr != nil {
				to = from.AddDate(0, 0, 21)
			}
			if to.Before(from) {
				to = from
			}
			dates = append(dates, from.Format("2006-01-02"), to.Format("2006-01-02"))
		}
		for _, m := range project.Milestones {
			dates = append(dates, scaleString(m.TargetDate))
		}
		for _, date := range dates {
			if _, err := time.Parse("2006-01-02", date); err == nil {
				if start == "" || date < start {
					start = date
				}
				if date > end {
					end = date
				}
			}
		}
	}
	if start != "" {
		page.Timeline = []string{start, end}
	}
	page.Items = flat[min(offset, len(flat)):min(offset+limit, len(flat))]
	wantedGroups := map[string]bool{}
	for _, project := range page.Items.([]domain.Project) {
		id, _ := scaleProjectDescriptor(project, query.Display.Grouping, data, query.DateLocale)
		wantedGroups[id] = true
		sub, _ := scaleProjectDescriptor(project, query.Display.SubGrouping, data, query.DateLocale)
		wantedGroups[id+":"+sub] = true
	}
	page.Groups = slices.DeleteFunc(page.Groups, func(group scaleGroup) bool {
		key := group.ID
		if group.ParentID != "" {
			key = group.ParentID + ":" + key
		}
		return group.Total > 0 && !wantedGroups[key]
	})
	page.HasMore = offset+scalePageCount(page) < len(flat)
	page.CustomerSummaries = map[string]*store.ProjectCustomerSummary{}
	page.LatestUpdates = map[string]time.Time{}
	ids := map[string]bool{}
	for _, project := range page.Items.([]domain.Project) {
		if summary := customers[project.ID]; summary != nil {
			page.CustomerSummaries[project.ID] = summary
			for _, customer := range summary.Customers {
				ids[customer.ID] = true
			}
		}
		if value, ok := latest[project.ID]; ok {
			page.LatestUpdates[project.ID] = value
		}
	}
	for _, relation := range data.ProjectRelations {
		if slices.ContainsFunc(page.Items.([]domain.Project), func(project domain.Project) bool {
			return relation.ProjectID == project.ID || relation.RelatedProjectID == project.ID
		}) {
			page.ProjectRelations = append(page.ProjectRelations, relation)
		}
	}
	for _, customer := range data.Customers {
		if ids[customer.ID] {
			page.Customers = append(page.Customers, customer)
		}
	}
	return page, nil
}

func scaleIncrement(counts map[string]map[string]int, field, value string) {
	if counts[field] == nil {
		counts[field] = map[string]int{}
	}
	counts[field][value]++
}
func scaleProjectValues(p domain.Project, q scaleQuery) map[string][]string {
	lead := ""
	if p.Lead != nil {
		lead = p.Lead.ID
	}
	labels := p.LabelIDs
	if len(labels) == 0 {
		labels = []string{""}
	}
	dates := []string{"no-target"}
	if p.TargetDate != nil && *p.TargetDate != "" {
		dates = []string{"has-target"}
		if date, err := time.Parse("2006-01-02", *p.TargetDate); err == nil && date.Before(scaleNow(q)) {
			dates = append(dates, "overdue")
		}
	}
	milestones := []string{}
	if len(p.Milestones) > 0 {
		milestones = []string{p.Milestones[0].Name}
	}
	return map[string][]string{"status": {p.Status.Name}, "priority": {map[int]string{0: "none", 1: "urgent", 2: "high", 3: "medium", 4: "low"}[p.Priority]}, "lead": {lead}, "members": p.MemberIDs, "health": {map[string]string{"onTrack": "on-track", "atRisk": "at-risk", "offTrack": "off-track", "noUpdate": "no-update"}[p.Health]}, "dates": dates, "labels": labels, "teams": p.TeamIDs, "milestones": milestones, "project": {p.ID}}
}
func scaleProjectMatches(p domain.Project, q scaleQuery, customer *store.ProjectCustomerSummary, updates []domain.ProjectUpdate) bool {
	if q.Search != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Summary), strings.ToLower(q.Search)) {
		return false
	}
	values := scaleProjectValues(p, q)
	for _, filter := range q.Filters {
		matched := false
		if filter.Field == "customers" {
			var err error
			matched, err = store.MatchProjectCustomerFilter(customer, filter.Operator, filter.Values)
			if err != nil || !matched {
				return false
			}
			continue
		}
		if _, ok := values[filter.Field]; !ok {
			return false
		}
		for _, value := range filter.Values {
			if slices.Contains(values[filter.Field], value) {
				matched = true
			}
		}
		if filter.Operator == "isNot" {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	if q.Insight != nil {
		field := q.Insight.Kind
		if !slices.Contains(values[field], q.Insight.Value) {
			return false
		}
	}
	closed := p.Status.Type == "completed" || p.Status.Type == "canceled"
	if closed && q.Display.ShowClosed != "" && q.Display.ShowClosed != "All" {
		days := map[string]int{"Past week": 7, "Past month": 31, "Past 3 months": 92, "Past 6 months": 183}[q.Display.ShowClosed]
		updated := p.UpdatedAt
		if len(updates) > 0 {
			updated = updates[0].CreatedAt
		}
		if days == 0 || updated.Before(scaleNow(q).Add(-time.Duration(days)*24*time.Hour)) {
			return false
		}
	}
	return true
}

func scaleProjectDescriptor(p domain.Project, grouping string, data domain.Bootstrap, locales ...string) (string, string) {
	switch grouping {
	case "Status":
		status := scaleProjectStatus(p, data.ProjectStatuses)
		return "status-" + status.ID, status.Name
	case "Priority":
		return "priority-" + map[int]string{0: "none", 1: "urgent", 2: "high", 3: "medium", 4: "low"}[p.Priority], map[int]string{0: "No priority", 1: "Urgent", 2: "High", 3: "Medium", 4: "Low"}[p.Priority]
	case "Health":
		return "health-" + map[string]string{"onTrack": "on-track", "atRisk": "at-risk", "offTrack": "off-track", "noUpdate": "no-update"}[p.Health], map[string]string{"onTrack": "On track", "atRisk": "At risk", "offTrack": "Off track", "noUpdate": "No update"}[p.Health]
	case "Lead", "Member":
		prefix := strings.ToLower(grouping) + "-"
		if p.Lead != nil {
			return prefix + p.Lead.ID, p.Lead.DisplayName
		}
		if grouping == "Member" {
			return prefix + "none", "No members"
		}
		return prefix + "none", "No lead"
	case "Team":
		if len(p.TeamIDs) > 0 {
			for _, team := range data.Teams {
				if team.ID == p.TeamIDs[0] {
					return "team-" + team.ID, team.Name
				}
			}
		}
		return "team-none", "No team"
	case "Start date":
		date := scaleString(p.StartDate)
		if date == "" {
			return "start-none", "No start date"
		}
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return "start-" + date, date
		}
		label := scaleMonth(parsed, locales)
		return "start-" + label, label
	case "Target date":
		date := scaleString(p.TargetDate)
		if date == "" {
			return "target-none", "No target date"
		}
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return "target-" + date, date
		}
		label := scaleMonth(parsed, locales)
		return "target-" + label, label
	case "Initiative":
		return "initiative-none", "No initiative"
	case "Label":
		return "label-none", "No labels"
	}
	return "all-projects", "All projects"
}

func scaleProjectStatus(p domain.Project, statuses []domain.ProjectStatus) domain.ProjectStatus {
	for _, status := range statuses {
		if status.ID == p.Status.ID {
			return status
		}
	}
	for _, status := range statuses {
		if status.Name == p.Status.Name && (p.Status.Type == "" || status.Type == p.Status.Type) {
			return status
		}
	}
	return p.Status
}

func scaleProjectGroups(projects []domain.Project, grouping string, empty bool, data domain.Bootstrap, q scaleQuery) []scaleGroup {
	statuses := data.ProjectStatuses
	groups := []scaleGroup{}
	byID := map[string]int{}
	add := func(id, name string, p *domain.Project) {
		index, ok := byID[id]
		if !ok {
			index = len(groups)
			byID[id] = index
			groups = append(groups, scaleGroup{ID: id, Name: name, Projects: []domain.Project{}})
		}
		if p != nil {
			groups[index].Projects = append(groups[index].Projects, *p)
		}
	}
	for _, p := range projects {
		id, name := scaleProjectDescriptor(p, grouping, data, q.DateLocale)
		add(id, name, &p)
	}
	if empty {
		switch grouping {
		case "Status":
			for _, s := range statuses {
				add("status-"+s.ID, s.Name, nil)
			}
		case "Priority":
			for _, n := range []int{1, 2, 3, 4, 0} {
				id, name := scaleProjectDescriptor(domain.Project{Priority: n}, grouping, domain.Bootstrap{})
				add(id, name, nil)
			}
		case "Health":
			for _, h := range []string{"onTrack", "atRisk", "offTrack", "noUpdate"} {
				id, name := scaleProjectDescriptor(domain.Project{Health: h}, grouping, domain.Bootstrap{})
				add(id, name, nil)
			}
		case "Lead", "Member", "Team":
			id, name := scaleProjectDescriptor(domain.Project{}, grouping, domain.Bootstrap{})
			add(id, name, nil)
		}
	}
	if len(groups) == 0 && (grouping == "" || grouping == "No grouping") {
		add("all-projects", "All projects", nil)
	}
	statusOrder := scaleStatuses(statuses, q.Display.Layout)
	rank := func(g scaleGroup) int {
		if grouping == "Status" {
			for i, s := range statusOrder {
				if g.ID == "status-"+s.ID {
					return i
				}
			}
			return 1 << 30
		}
		if grouping == "Priority" {
			return slices.Index([]string{"Urgent", "High", "Medium", "Low", "No priority"}, g.Name)
		}
		if grouping == "Health" {
			return slices.Index([]string{"On track", "At risk", "Off track", "No update"}, g.Name)
		}
		return 0
	}
	c := scaleCollator(q)
	slices.SortStableFunc(groups, func(a, b scaleGroup) int {
		if rank(a) != rank(b) {
			return rank(a) - rank(b)
		}
		return c.CompareString(a.Name, b.Name)
	})
	return groups
}

func scaleStatuses(statuses []domain.ProjectStatus, layout string) []domain.ProjectStatus {
	result := slices.Clone(statuses)
	types := []string{"started", "planned", "backlog", "paused", "completed", "canceled"}
	if layout == "board" {
		types = []string{"backlog", "planned", "started", "paused", "completed", "canceled"}
	}
	slices.SortStableFunc(result, func(a, b domain.ProjectStatus) int {
		ai, bi := slices.Index(types, a.Type), slices.Index(types, b.Type)
		if ai != bi {
			return ai - bi
		}
		d := a.Position - b.Position
		if layout != "board" && a.Type != "completed" && a.Type != "canceled" {
			d = -d
		}
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
		return 0
	})
	return result
}

func scaleSortProjects(projects []domain.Project, q scaleQuery, data domain.Bootstrap, customers map[string]*store.ProjectCustomerSummary, latest map[string]time.Time) {
	statuses := scaleStatuses(data.ProjectStatuses, q.Display.Layout)
	c := scaleCollator(q)
	value := func(p domain.Project) (float64, string, bool) {
		switch q.Display.Ordering {
		case "", "Manual":
			return p.Position, "", true
		case "Priority":
			if p.Priority == 0 {
				return 4, "", true
			}
			return float64(p.Priority - 1), "", true
		case "Health", "Health updated":
			return float64(slices.Index([]string{"onTrack", "atRisk", "offTrack", "noUpdate"}, p.Health)), "", true
		case "Status":
			status := scaleProjectStatus(p, statuses)
			index := slices.IndexFunc(statuses, func(s domain.ProjectStatus) bool { return s.ID == status.ID })
			if index < 0 {
				return 9007199254740991, "", true
			}
			return float64(index), "", true
		case "Start date":
			date := scaleString(p.StartDate)
			if date == "" {
				date = "9999-12-31"
			}
			parsed, err := time.Parse("2006-01-02", date)
			if err == nil && date != "9999-12-31" {
				date = scaleMonth(parsed, []string{q.DateLocale})
			}
			return 0, date, false
		case "Target date":
			date := scaleString(p.TargetDate)
			if date == "" {
				date = "9999-12-31"
			}
			parsed, err := time.Parse("2006-01-02", date)
			if err == nil && date != "9999-12-31" {
				date = scaleMonth(parsed, []string{q.DateLocale})
			}
			return 0, date, false
		case "Customer count", "Customer revenue", "Important count":
			v := 0.0
			if summary := customers[p.ID]; summary != nil {
				for _, customer := range summary.Customers {
					if q.Display.Ordering == "Customer count" {
						v++
					} else if q.Display.Ordering == "Customer revenue" {
						v += customer.Revenue
					} else if customer.Important {
						v++
					}
				}
			}
			return -v, "", true
		case "Relevance":
			score := 0.0
			closed := p.Status.Type == "completed" || p.Status.Type == "canceled"
			if p.Lead != nil && p.Lead.ID == data.Viewer.ID {
				if closed {
					score += 0.25
				} else {
					score += 1.25
				}
			} else if slices.Contains(p.MemberIDs, data.Viewer.ID) {
				if closed {
					score += 0.2
				} else {
					score++
				}
			}
			if len(p.TeamIDs) > 0 && !slices.ContainsFunc(data.Teams, func(t domain.Team) bool { return slices.Contains(p.TeamIDs, t.ID) }) {
				score -= 0.3
			}
			if scaleNow(q).Sub(p.CreatedAt) <= 48*time.Hour {
				score += 0.15
			}
			if p.Status.Type == "started" {
				score += 0.25
			}
			if closed {
				score--
			}
			return -score, "", true
		}
		return 0, p.Name, false
	}
	updated := func(project domain.Project) time.Time {
		if at, ok := latest[project.ID]; ok {
			return at
		}
		return project.UpdatedAt
	}
	slices.SortStableFunc(projects, func(a, b domain.Project) int {
		switch q.Display.Ordering {
		case "Created":
			return a.CreatedAt.Compare(b.CreatedAt)
		case "Updated":
			return updated(a).Compare(updated(b))
		}
		av, as, num := value(a)
		bv, bs, _ := value(b)
		d := 0
		if num {
			if av < bv {
				d = -1
			} else if av > bv {
				d = 1
			}
			if d == 0 && q.Display.Ordering == "Relevance" {
				d = c.CompareString(a.Name, b.Name)
			}
		} else {
			d = c.CompareString(as, bs)
		}
		return d
	})
	if q.Display.OrderingDirection == "desc" && q.Display.Ordering != "Relevance" {
		slices.Reverse(projects)
	}
}
