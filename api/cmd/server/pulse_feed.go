package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

const (
	pulseFeedDefaultLimit = 30
	pulseFeedMaxLimit     = 100
	// pulseUnreadCap bounds the unread scan; the sidebar shows "99+" anyway.
	pulseUnreadCap = 100
	// pulseUnreadLookback applies when the viewer never opened Pulse.
	pulseUnreadLookback = 7 * 24 * time.Hour
)

type pulseSource struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon,omitempty"`
	Color string `json:"color,omitempty"`
	URL   string `json:"url"`
}

type pulseFeedItem struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Update     any               `json:"update"`
	Source     pulseSource       `json:"source"`
	Reasons    []pulseReason     `json:"reasons"`
	Diff       *domain.PulseDiff `json:"diff,omitempty"`
	Subscribed bool              `json:"subscribed"`
	CreatedAt  time.Time         `json:"createdAt"`
	// ProjectUpdatesSubscribed (initiative items only): the viewer follows
	// the initiative's project updates.
	ProjectUpdatesSubscribed *bool `json:"projectUpdatesSubscribed,omitempty"`
}

type pulseFeedResponse struct {
	Items       []pulseFeedItem `json:"items"`
	NextCursor  string          `json:"nextCursor,omitempty"`
	UnreadCount int             `json:"unreadCount"`
	LastSeenAt  *time.Time      `json:"lastSeenAt,omitempty"`
}

type pulseFilter struct {
	ID       string   `json:"id,omitempty"`
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type pulseFilterConfig struct {
	Filters []pulseFilter `json:"filters"`
	Match   string        `json:"match"`
}

var pulseFilterFields = map[string]bool{"author": true, "team": true, "createdDate": true, "updateType": true, "health": true, "initiative": true, "project": true, "projectMember": true, "projectStatus": true, "projectStatusType": true, "projectLabel": true}

type pulseFeedQuery struct {
	View    string
	Search  string
	Filters []pulseFilterConfig
	Cursor  string
	Limit   int
	Now     time.Time
}

type pulseCursor struct {
	View  string  `json:"v"`
	Score float64 `json:"s,omitempty"`
	At    int64   `json:"t"`
	ID    string  `json:"i"`
}

func encodePulseCursor(view string, entry *store.PulseFeedEntry) string {
	raw, _ := json.Marshal(pulseCursor{View: view, Score: entry.Score, At: entry.CreatedAt.UnixNano(), ID: entry.ItemID()})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePulseCursor(value, view string) (*store.PulseFeedEntry, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	var cursor pulseCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.View != view || cursor.ID == "" || math.IsNaN(cursor.Score) {
		return nil, errInvalid
	}
	kind, id, _ := strings.Cut(cursor.ID, ":")
	return &store.PulseFeedEntry{Kind: kind, UpdateID: id, Score: cursor.Score, CreatedAt: time.Unix(0, cursor.At).UTC()}, nil
}

// parsePulseFilterConfig accepts {filters, match} or a bare filter array.
func parsePulseFilterConfig(raw, match string) (pulseFilterConfig, error) {
	config := pulseFilterConfig{Match: match}
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if strings.HasPrefix(raw, "[") {
			if err := json.Unmarshal([]byte(raw), &config.Filters); err != nil {
				return config, errInvalid
			}
		} else if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return config, errInvalid
		}
		if match != "" {
			config.Match = match
		}
	}
	if config.Match == "" {
		config.Match = "all"
	}
	if config.Match != "all" && config.Match != "any" || len(config.Filters) > 50 {
		return config, errInvalid
	}
	for _, filter := range config.Filters {
		if !pulseFilterFields[filter.Field] || filter.Operator != "is" && filter.Operator != "isNot" || len(filter.Values) > 200 {
			return config, errInvalid
		}
	}
	return config, nil
}

func unique(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	return result
}

func pulseDateBuckets(createdAt, now time.Time) []string {
	age := now.Sub(createdAt)
	result := []string{}
	for _, bucket := range []struct {
		id   string
		days int
	}{{"past-day", 1}, {"past-week", 7}, {"past-month", 30}, {"past-quarter", 90}} {
		if age <= time.Duration(bucket.days)*24*time.Hour {
			result = append(result, bucket.id)
		}
	}
	return result
}

// pulseFilterValues mirrors the web filter definitions: initiatives answer
// project fields with the values of their projects.
func (rules *pulseRules) pulseFilterValues(entry *store.PulseFeedEntry, field string, now time.Time) []string {
	project := rules.projects[entry.SourceID]
	initiative := rules.initiatives[entry.SourceID]
	if entry.Kind == "initiative" {
		project = nil
	} else {
		initiative = nil
	}
	initiativeProjects := func(visit func(*domain.Project) []string) []string {
		values := []string{}
		if initiative != nil {
			for _, id := range rules.initiativeProjects[initiative.ID] {
				if item := rules.projects[id]; item != nil {
					values = append(values, visit(item)...)
				}
			}
		}
		return unique(values)
	}
	switch field {
	case "author":
		return []string{entry.AuthorID}
	case "team":
		if project != nil {
			return project.TeamIDs
		}
		if initiative != nil {
			return unique(append([]string{initiative.LeadTeamID}, initiative.ContributingTeamIDs...))
		}
	case "createdDate":
		return pulseDateBuckets(entry.CreatedAt, now)
	case "updateType":
		return []string{entry.Kind}
	case "health":
		return []string{entry.Health}
	case "initiative":
		if initiative != nil {
			return []string{initiative.ID}
		}
		return rules.projectInitiatives[entry.SourceID]
	case "project":
		if project != nil {
			return []string{project.ID}
		}
		if initiative != nil {
			return rules.initiativeProjects[initiative.ID]
		}
	case "projectMember":
		members := func(item *domain.Project) []string {
			values := slices.Clone(item.MemberIDs)
			if item.Lead != nil {
				values = append(values, item.Lead.ID)
			}
			return values
		}
		if project != nil {
			return unique(members(project))
		}
		return initiativeProjects(members)
	case "projectStatus":
		if project != nil {
			return []string{project.Status.ID}
		}
		return initiativeProjects(func(item *domain.Project) []string { return []string{item.Status.ID} })
	case "projectStatusType":
		if project != nil {
			return []string{project.Status.Type}
		}
		return initiativeProjects(func(item *domain.Project) []string { return []string{item.Status.Type} })
	case "projectLabel":
		if project != nil {
			return project.LabelIDs
		}
		return initiativeProjects(func(item *domain.Project) []string { return item.LabelIDs })
	}
	return nil
}

func (rules *pulseRules) pulseFiltersMatch(entry *store.PulseFeedEntry, config pulseFilterConfig, now time.Time) bool {
	if len(config.Filters) == 0 {
		return true
	}
	for _, filter := range config.Filters {
		values := rules.pulseFilterValues(entry, filter.Field, now)
		matched := slices.ContainsFunc(filter.Values, func(value string) bool { return slices.Contains(values, value) })
		if filter.Operator == "isNot" {
			matched = !matched
		}
		if config.Match == "any" && matched {
			return true
		}
		if config.Match != "any" && !matched {
			return false
		}
	}
	return config.Match != "any"
}

func (rules *pulseRules) pulseSearchMatch(entry *store.PulseFeedEntry, search string) bool {
	return search == "" || strings.Contains(entry.Search, search) || strings.Contains(strings.ToLower(rules.sourceName(entry.Kind, entry.SourceID)), search)
}

func pulseCompare(view string) func(a, b *store.PulseFeedEntry) int {
	if view == "popular" {
		return store.ComparePulsePopular
	}
	return store.ComparePulseRecent
}

// pulseFeedPage walks the ordered index from the cursor and keeps the first
// limit entries that pass visibility, the view, filters and search.
func (rules *pulseRules) pulseFeedPage(viewer pulseViewer, query pulseFeedQuery) ([]*store.PulseFeedEntry, map[*store.PulseFeedEntry][]pulseReason, map[*store.PulseFeedEntry]bool, string, error) {
	entries := rules.snapshot.Recent
	if query.View == "popular" {
		entries = rules.snapshot.Popular
	}
	compare := pulseCompare(query.View)
	start := 0
	cursor, err := decodePulseCursor(query.Cursor, query.View)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if cursor != nil {
		start, _ = slices.BinarySearchFunc(entries, cursor, compare)
		if start < len(entries) && compare(entries[start], cursor) == 0 {
			start++
		}
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	page := []*store.PulseFeedEntry{}
	reasons := map[*store.PulseFeedEntry][]pulseReason{}
	subscribed := map[*store.PulseFeedEntry]bool{}
	next := ""
	for _, entry := range entries[start:] {
		if !rules.sourceShown(viewer, entry) {
			continue
		}
		if query.View == "created" && entry.AuthorID != viewer.ID {
			continue
		}
		if !rules.pulseSearchMatch(entry, search) || slices.ContainsFunc(query.Filters, func(config pulseFilterConfig) bool { return !rules.pulseFiltersMatch(entry, config, query.Now) }) {
			continue
		}
		why, follows := rules.forMeReasons(viewer, entry)
		if query.View == "following" && len(why) == 0 {
			continue
		}
		if len(page) == query.Limit {
			next = encodePulseCursor(query.View, page[len(page)-1])
			break
		}
		page = append(page, entry)
		subscribed[entry] = follows
		if query.View == "following" {
			reasons[entry] = why
		}
	}
	return page, reasons, subscribed, next, nil
}

func pulseSourceURL(workspace, kind, slug, id string) string {
	if slug == "" {
		slug = id
	}
	if kind == "project" {
		return "/" + url.PathEscape(workspace) + "/project/" + url.PathEscape(slug) + "/overview"
	}
	return "/" + url.PathEscape(workspace) + "/initiative/" + url.PathEscape(slug)
}

func (rules *pulseRules) pulseSourceOf(kind, id string) pulseSource {
	workspace := rules.snapshot.Workspace.URLKey
	if kind == "project" {
		if project := rules.projects[id]; project != nil {
			return pulseSource{ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color, URL: pulseSourceURL(workspace, kind, project.SlugID, project.ID)}
		}
	} else if initiative := rules.initiatives[id]; initiative != nil {
		return pulseSource{ID: initiative.ID, Name: initiative.Name, Icon: initiative.Icon, Color: initiative.Color, URL: pulseSourceURL(workspace, kind, initiative.SlugID, initiative.ID)}
	}
	return pulseSource{ID: id}
}

// pulseUpdateOf returns a shallow copy of the update without its stored
// snapshot (comments, reactions and attachments stay shared and read-only).
func (rules *pulseRules) pulseUpdateOf(entry *store.PulseFeedEntry) (any, *domain.PulseDiff, bool) {
	if entry.Kind == "project" {
		for _, update := range rules.snapshot.ProjectUpdates[entry.SourceID] {
			if update.ID == entry.UpdateID {
				update.Snapshot = nil
				return update, update.Diff, true
			}
		}
		return nil, nil, false
	}
	for _, update := range rules.snapshot.InitiativeUpdates[entry.SourceID] {
		if update.ID == entry.UpdateID {
			update.Snapshot = nil
			return update, update.Diff, true
		}
	}
	return nil, nil, false
}

func (rules *pulseRules) pulseItem(entry *store.PulseFeedEntry, reasons []pulseReason, subscribed bool) (pulseFeedItem, bool) {
	update, diff, ok := rules.pulseUpdateOf(entry)
	if !ok {
		return pulseFeedItem{}, false
	}
	if reasons == nil {
		reasons = []pulseReason{}
	}
	return pulseFeedItem{ID: entry.ItemID(), Kind: entry.Kind, Update: update, Source: rules.pulseSourceOf(entry.Kind, entry.SourceID), Reasons: reasons, Diff: diff, Subscribed: subscribed, CreatedAt: entry.CreatedAt}, true
}

func pulseLastSeen(snapshot *store.PulseFeedSnapshot, userID string) *time.Time {
	raw := strings.TrimSpace(snapshot.UserSettings[userID].FeedLastSeenTime)
	if raw == "" {
		return nil
	}
	if value, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		value = value.UTC()
		return &value
	}
	if millis, err := strconv.ParseInt(raw, 10, 64); err == nil {
		value := time.UnixMilli(millis).UTC()
		return &value
	}
	return nil
}

// pulseUnread counts For-me updates newer than the viewer's last visit,
// excluding the viewer's own. The scan stops at the last-seen time.
func (rules *pulseRules) pulseUnread(viewer pulseViewer, lastSeen *time.Time, now time.Time) (int, *time.Time) {
	since := now.Add(-pulseUnreadLookback)
	if lastSeen != nil {
		since = *lastSeen
	}
	count := 0
	var latest *time.Time
	for _, entry := range rules.snapshot.Recent {
		if !entry.CreatedAt.After(since) || count >= pulseUnreadCap {
			break
		}
		if entry.AuthorID == viewer.ID || !rules.sourceShown(viewer, entry) {
			continue
		}
		if reasons, _ := rules.forMeReasons(viewer, entry); len(reasons) == 0 {
			continue
		}
		if latest == nil {
			at := entry.CreatedAt
			latest = &at
		}
		count++
	}
	return count, latest
}

// pulseRequestViewer resolves the requesting member's teams and visibility
// with indexed reads (no workspace projection).
func (s *server) pulseRequestViewer(r *http.Request, snapshot *store.PulseFeedSnapshot) (pulseViewer, error) {
	var restrict []string
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok && apiKeyTeamRestrictionSelected(key) {
		restrict = slices.Clone(key.TeamIDs)
		if restrict == nil {
			restrict = []string{}
		}
	}
	if s.authDisabled {
		viewer := requestActor(s, r)
		memberships, err := s.store.ListTeamMembers(r.Context(), snapshot.Workspace.ID)
		if err != nil {
			return pulseViewer{}, err
		}
		return newPulseViewer(snapshot, viewer.ID, "admin", memberships, restrict), nil
	}
	metadata, _, err := s.requestIssueQueryAccess(r)
	if err != nil {
		return pulseViewer{}, err
	}
	if metadata.ViewerRole == "guest" {
		return pulseViewer{}, store.ErrAuthForbidden
	}
	return newPulseViewer(snapshot, authUser(r).ID, metadata.ViewerRole, metadata.TeamMembers, restrict), nil
}

func (s *server) pulseRequestContext(w http.ResponseWriter, r *http.Request) (*store.PulseFeedSnapshot, *pulseRules, pulseViewer, bool) {
	snapshot, ok := s.store.PulseFeed(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return nil, nil, pulseViewer{}, false
	}
	if !workspaceFeatureEnabled(snapshot.WorkspaceSettings, "pulse") {
		writeError(w, http.StatusForbidden, "This workspace feature is disabled")
		return nil, nil, pulseViewer{}, false
	}
	viewer, err := s.pulseRequestViewer(r, snapshot)
	if err != nil {
		if errors.Is(err, store.ErrAuthForbidden) {
			writeError(w, http.StatusForbidden, "Guests cannot access Pulse")
		} else {
			issueRecordsError(w, err)
		}
		return nil, nil, pulseViewer{}, false
	}
	return snapshot, newPulseRules(snapshot), viewer, true
}

func (s *server) getPulseFeed(w http.ResponseWriter, r *http.Request) {
	snapshot, rules, viewer, ok := s.pulseRequestContext(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	query := pulseFeedQuery{View: values.Get("view"), Search: values.Get("q"), Cursor: values.Get("cursor"), Now: time.Now().UTC()}
	if query.View == "" {
		query.View = "all"
	}
	if !slices.Contains([]string{"following", "popular", "all", "created"}, query.View) {
		writeError(w, http.StatusBadRequest, "view must be following, popular, all or created")
		return
	}
	query.Limit = pulseFeedDefaultLimit
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		query.Limit = min(limit, pulseFeedMaxLimit)
	}
	config, err := parsePulseFilterConfig(values.Get("filter"), values.Get("match"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid pulse filter")
		return
	}
	if len(config.Filters) > 0 {
		query.Filters = append(query.Filters, config)
	}
	if viewID := values.Get("viewId"); viewID != "" {
		index := slices.IndexFunc(snapshot.SavedViews, func(view domain.SavedView) bool {
			return (view.ID == viewID || view.SlugID == viewID) && view.Resource == "pulse" && (view.Scope != "personal" || view.OwnerID == viewer.ID)
		})
		if index < 0 {
			writeError(w, http.StatusNotFound, "view not found")
			return
		}
		saved := snapshot.SavedViews[index]
		var display struct {
			Match string `json:"match"`
		}
		_ = json.Unmarshal(saved.Display, &display)
		viewConfig, err := parsePulseFilterConfig(string(saved.Filters), display.Match)
		if err == nil && len(viewConfig.Filters) > 0 {
			query.Filters = append(query.Filters, viewConfig)
		}
	}
	page, reasons, subscribed, next, err := rules.pulseFeedPage(viewer, query)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	response := pulseFeedResponse{Items: make([]pulseFeedItem, 0, len(page)), NextCursor: next}
	for _, entry := range page {
		if item, ok := rules.pulseItem(entry, reasons[entry], subscribed[entry]); ok {
			if item.Kind == "initiative" {
				following := viewer.InitiativeProjectUpdates[entry.SourceID]
				item.ProjectUpdatesSubscribed = &following
			}
			response.Items = append(response.Items, item)
		}
	}
	response.LastSeenAt = pulseLastSeen(snapshot, viewer.ID)
	response.UnreadCount, _ = rules.pulseUnread(viewer, response.LastSeenAt, query.Now)
	writeJSON(w, http.StatusOK, response)
}

func (s *server) getPulseUnread(w http.ResponseWriter, r *http.Request) {
	snapshot, rules, viewer, ok := s.pulseRequestContext(w, r)
	if !ok {
		return
	}
	count, latest := rules.pulseUnread(viewer, pulseLastSeen(snapshot, viewer.ID), time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"count": count, "latestAt": latest})
}

// postPulseSeen moves the viewer's Pulse last-seen time forward. It writes
// only the viewer's own settings record and never moves the time backwards.
func (s *server) postPulseSeen(w http.ResponseWriter, r *http.Request) {
	var input struct {
		At *time.Time `json:"at"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	snapshot, _, viewer, ok := s.pulseRequestContext(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC()
	at := now
	if input.At != nil {
		at = input.At.UTC()
		if at.After(now) {
			at = now
		}
	}
	current := pulseLastSeen(snapshot, viewer.ID)
	if current != nil && !at.After(*current) {
		writeJSON(w, http.StatusOK, map[string]any{"lastSeenAt": current})
		return
	}
	value := at.Format(time.RFC3339Nano)
	saved := at
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "user_settings.updated", viewer.ID, map[string]string{"feedLastSeenTime": value}, func(data *domain.Bootstrap) error {
		settings, ok := data.UserSettings[viewer.ID]
		if !ok {
			settings = domain.UserSettings{UserID: viewer.ID, PulseSchedule: "default"}
		}
		if previous, err := time.Parse(time.RFC3339Nano, settings.FeedLastSeenTime); err == nil && !at.After(previous) {
			saved = previous.UTC()
			return store.ErrNoMutation
		}
		settings.UserID, settings.FeedLastSeenTime, settings.UpdatedAt = viewer.ID, value, now
		data.UserSettings[viewer.ID] = settings
		return nil
	})
	respondMutation(w, err, http.StatusOK, map[string]any{"lastSeenAt": saved})
}

func validPulseSubscriptionType(kind string) bool {
	return kind == "project" || kind == "initiative" || kind == "team"
}

func (s *server) pulseSubscriptionTarget(w http.ResponseWriter, r *http.Request) (*pulseRules, pulseViewer, string, string, bool) {
	kind, id := r.PathValue("type"), r.PathValue("id")
	if !validPulseSubscriptionType(kind) {
		writeError(w, http.StatusBadRequest, "type must be project, initiative or team")
		return nil, pulseViewer{}, "", "", false
	}
	_, rules, viewer, ok := s.pulseRequestContext(w, r)
	if !ok {
		return nil, pulseViewer{}, "", "", false
	}
	visible := false
	switch kind {
	case "project":
		visible = rules.projectVisible(viewer, rules.projects[id])
	case "initiative":
		visible = rules.initiativeVisible(viewer, rules.initiatives[id])
	case "team":
		visible = viewer.Visible[id]
	}
	if !visible {
		writeError(w, http.StatusNotFound, "resource not found")
		return nil, pulseViewer{}, "", "", false
	}
	return rules, viewer, kind, id, true
}

func pulseSubscribedState(rules *pulseRules, viewer pulseViewer, kind, id string) bool {
	if kind == "team" {
		subscribed, explicit := viewer.Explicit["team:"+id]
		return explicit && subscribed || !explicit && viewer.Teams[id]
	}
	return rules.pulseIsFollowing(viewer, kind, id)
}

func (s *server) getPulseSubscription(w http.ResponseWriter, r *http.Request) {
	rules, viewer, kind, id, ok := s.pulseSubscriptionTarget(w, r)
	if !ok {
		return
	}
	subscribed, explicit := viewer.Explicit[kind+":"+id]
	result := map[string]any{"subscribed": pulseSubscribedState(rules, viewer, kind, id), "explicit": explicit, "reasons": []pulseReason{}}
	if explicit {
		result["explicitSubscribed"] = subscribed
	}
	if kind != "team" {
		if reasons := rules.subscriptionReasons(viewer, kind, id); reasons != nil {
			result["reasons"] = reasons
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// setPulseSubscription records an explicit Pulse subscription or opt-out on
// the viewer's subscription record for the resource. Opt-outs are kept even
// when the record has no other events, so defaults stay overridden.
func (s *server) setPulseSubscription(subscribe bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, viewer, kind, id, ok := s.pulseSubscriptionTarget(w, r)
		if !ok {
			return
		}
		ctx := store.WithMetadataFields(r.Context(), "subscriptions")
		eventType := "subscription.added"
		if !subscribe {
			eventType = "subscription.removed"
		}
		err := s.store.MutateWorkspace(ctx, workspaceKey(r), eventType, id, map[string]any{"type": kind, "pulse": subscribe}, func(data *domain.Bootstrap) error {
			index := slices.IndexFunc(data.Subscriptions, func(item domain.Subscription) bool {
				return item.UserID == viewer.ID && item.ResourceType == kind && item.ResourceID == id
			})
			if index < 0 {
				now := time.Now().UTC()
				data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: fmt.Sprintf("subscription_%d", now.UnixNano()), UserID: viewer.ID, ResourceType: kind, ResourceID: id, CreatedAt: now})
				index = len(data.Subscriptions) - 1
			}
			item := &data.Subscriptions[index]
			item.Events = slices.DeleteFunc(slices.Clone(item.Events), func(event string) bool { return event == pulseSubscriptionEvent })
			item.OptOutEvents = slices.DeleteFunc(slices.Clone(item.OptOutEvents), func(event string) bool { return event == pulseSubscriptionEvent })
			if subscribe {
				item.Events = append(item.Events, pulseSubscriptionEvent)
			} else {
				item.OptOutEvents = append(item.OptOutEvents, pulseSubscriptionEvent)
			}
			if len(item.OptOutEvents) == 0 {
				item.OptOutEvents = nil
			}
			return nil
		})
		respondMutation(w, err, http.StatusOK, map[string]bool{"subscribed": subscribe})
	}
}

// initiativeProjectUpdatesTarget resolves the initiative of a project-updates
// subscription request ("Subscribe to {initiative}'s project updates").
func (s *server) initiativeProjectUpdatesTarget(w http.ResponseWriter, r *http.Request) (pulseViewer, string, bool) {
	_, rules, viewer, ok := s.pulseRequestContext(w, r)
	if !ok {
		return pulseViewer{}, "", false
	}
	id := r.PathValue("id")
	if !rules.initiativeVisible(viewer, rules.initiatives[id]) {
		writeError(w, http.StatusNotFound, "resource not found")
		return pulseViewer{}, "", false
	}
	return viewer, id, true
}

// getInitiativeProjectUpdatesSubscription reports whether the viewer follows
// every project update of an initiative (and its sub-initiatives).
func (s *server) getInitiativeProjectUpdatesSubscription(w http.ResponseWriter, r *http.Request) {
	viewer, id, ok := s.initiativeProjectUpdatesTarget(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"subscribed": viewer.InitiativeProjectUpdates[id]})
}

// setInitiativeProjectUpdatesSubscription subscribes the viewer to (or
// unsubscribes them from) an initiative's project updates in Pulse: its own
// subscription record, so it never reads as a subscription to the initiative.
func (s *server) setInitiativeProjectUpdatesSubscription(subscribe bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		viewer, id, ok := s.initiativeProjectUpdatesTarget(w, r)
		if !ok {
			return
		}
		ctx := store.WithMetadataFields(r.Context(), "subscriptions")
		eventType := "subscription.added"
		if !subscribe {
			eventType = "subscription.removed"
		}
		err := s.store.MutateWorkspace(ctx, workspaceKey(r), eventType, id, map[string]any{"type": pulseInitiativeProjectUpdatesType, "pulse": subscribe}, func(data *domain.Bootstrap) error {
			matches := func(item domain.Subscription) bool {
				return item.UserID == viewer.ID && item.ResourceType == pulseInitiativeProjectUpdatesType && item.ResourceID == id
			}
			exists := slices.ContainsFunc(data.Subscriptions, matches)
			if !subscribe {
				if !exists {
					return store.ErrNoMutation
				}
				data.Subscriptions = slices.DeleteFunc(data.Subscriptions, matches)
				return nil
			}
			if exists {
				return store.ErrNoMutation
			}
			now := time.Now().UTC()
			data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: fmt.Sprintf("subscription_%d", now.UnixNano()), UserID: viewer.ID, ResourceType: pulseInitiativeProjectUpdatesType, ResourceID: id, Events: []string{pulseSubscriptionEvent}, CreatedAt: now})
			return nil
		})
		if errors.Is(err, store.ErrNoMutation) {
			err = nil
		}
		respondMutation(w, err, http.StatusOK, map[string]bool{"subscribed": subscribe})
	}
}
