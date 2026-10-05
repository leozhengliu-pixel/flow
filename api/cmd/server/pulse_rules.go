package main

import (
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Pulse reason types, in the order the "Why am I seeing this?" dialog lists them.
const (
	pulseReasonMentioned               = "mentioned"
	pulseReasonAuthor                  = "author"
	pulseReasonInitiativeOwner         = "initiativeOwner"
	pulseReasonInitiativeProjectMember = "initiativeProjectMember"
	pulseReasonProjectMember           = "projectMember"
	pulseReasonSubscribed              = "subscribed"
	pulseReasonTeamProjectUpdates      = "teamProjectUpdates"
)

// pulseSubscriptionEvent is the subscription event that follows a project,
// initiative or team in Pulse ("A team project update is posted" for teams).
const pulseSubscriptionEvent = "pulse"

// pulseInitiativeProjectUpdatesType is the subscription resource type of
// "Subscribe to {initiative}'s project updates": a record of this type (event
// "pulse", resource id = the initiative) follows every project of the
// initiative and its sub-initiatives. It is its own record so it never reads
// as a subscription to the initiative itself.
const pulseInitiativeProjectUpdatesType = "initiativeProjectUpdates"

type pulseReason struct {
	Type        string   `json:"type"`
	SourceIDs   []string `json:"sourceIds"`
	SourceNames []string `json:"sourceNames"`
	// InitiativeIDs/InitiativeNames: for teamProjectUpdates, the initiatives
	// whose project updates the viewer subscribed to (SourceIDs stay teams).
	InitiativeIDs   []string `json:"initiativeIds,omitempty"`
	InitiativeNames []string `json:"initiativeNames,omitempty"`
}

// pulseViewer is who a feed or summary is computed for.
type pulseViewer struct {
	ID   string
	Role string
	// Teams are the viewer's team memberships; Visible are the teams whose
	// projects the viewer can read (already including admin rules).
	Teams   map[string]bool
	Visible map[string]bool
	// Explicit holds the viewer's explicit Pulse choices keyed by
	// "<type>:<id>": true subscribed, false unsubscribed.
	Explicit map[string]bool
	// InitiativeProjectUpdates are initiatives whose project updates the
	// viewer subscribed to.
	InitiativeProjectUpdates map[string]bool
}

func newPulseViewer(snapshot *store.PulseFeedSnapshot, userID, role string, memberships []domain.TeamMember, restrictTeams []string) pulseViewer {
	own := []domain.TeamMember{}
	for _, membership := range memberships {
		if membership.UserID == userID {
			own = append(own, membership)
		}
	}
	subscriptions := []domain.Subscription{}
	for _, subscription := range snapshot.Subscriptions {
		if subscription.UserID == userID {
			subscriptions = append(subscriptions, subscription)
		}
	}
	return newPulseViewerFrom(snapshot, userID, role, own, subscriptions, restrictTeams)
}

// newPulseViewerFrom builds a viewer from the user's own memberships and
// subscription records (already filtered to the user).
func newPulseViewerFrom(snapshot *store.PulseFeedSnapshot, userID, role string, memberships []domain.TeamMember, subscriptions []domain.Subscription, restrictTeams []string) pulseViewer {
	viewer := pulseViewer{ID: userID, Role: role, Teams: map[string]bool{}, Visible: map[string]bool{}, Explicit: map[string]bool{}, InitiativeProjectUpdates: map[string]bool{}}
	for _, membership := range memberships {
		viewer.Teams[membership.TeamID] = true
	}
	for _, id := range store.VisibleTeamIDsFor(snapshot.Teams, snapshot.TeamSettings, memberships, userID, role) {
		if restrictTeams == nil || slices.Contains(restrictTeams, id) {
			viewer.Visible[id] = true
		}
	}
	for _, subscription := range subscriptions {
		if subscription.ResourceType == pulseInitiativeProjectUpdatesType {
			if slices.Contains(subscription.Events, pulseSubscriptionEvent) {
				viewer.InitiativeProjectUpdates[subscription.ResourceID] = true
			}
			continue
		}
		key := subscription.ResourceType + ":" + subscription.ResourceID
		if slices.Contains(subscription.OptOutEvents, pulseSubscriptionEvent) {
			viewer.Explicit[key] = false
		} else if slices.Contains(subscription.Events, pulseSubscriptionEvent) {
			viewer.Explicit[key] = true
		}
	}
	return viewer
}

// pulseRules indexes projects and initiatives for the visibility and For-me
// rules. It only references the shared snapshot.
type pulseRules struct {
	snapshot           *store.PulseFeedSnapshot
	projects           map[string]*domain.Project
	initiatives        map[string]*domain.Initiative
	projectInitiatives map[string][]string // project -> initiatives that contain it
	// initiativeProjects lists an initiative's projects from both link
	// directions: initiative.ProjectIDs and project.Initiatives.
	initiativeProjects map[string][]string
	initiativeParents  map[string][]string
	initiativeChildren map[string][]string
	teamNames          map[string]string
}

func newPulseRules(snapshot *store.PulseFeedSnapshot) *pulseRules {
	rules := &pulseRules{snapshot: snapshot, projects: make(map[string]*domain.Project, len(snapshot.Projects)), initiatives: make(map[string]*domain.Initiative, len(snapshot.Initiatives)), projectInitiatives: map[string][]string{}, initiativeProjects: map[string][]string{}, initiativeChildren: map[string][]string{}, teamNames: make(map[string]string, len(snapshot.Teams))}
	for index := range snapshot.Projects {
		project := &snapshot.Projects[index]
		rules.projects[project.ID] = project
		for _, initiativeID := range project.Initiatives {
			rules.projectInitiatives[project.ID] = appendUnique(rules.projectInitiatives[project.ID], initiativeID)
		}
	}
	for index := range snapshot.Initiatives {
		initiative := &snapshot.Initiatives[index]
		rules.initiatives[initiative.ID] = initiative
		for _, projectID := range initiative.ProjectIDs {
			rules.projectInitiatives[projectID] = appendUnique(rules.projectInitiatives[projectID], initiative.ID)
			rules.initiativeProjects[initiative.ID] = appendUnique(rules.initiativeProjects[initiative.ID], projectID)
		}
	}
	for index := range snapshot.Projects {
		project := &snapshot.Projects[index]
		for _, initiativeID := range project.Initiatives {
			rules.initiativeProjects[initiativeID] = appendUnique(rules.initiativeProjects[initiativeID], project.ID)
		}
	}
	rules.initiativeParents = domain.InitiativeParents(&domain.Bootstrap{Initiatives: snapshot.Initiatives, InitiativeRelations: snapshot.InitiativeRelations})
	for child, parents := range rules.initiativeParents {
		for _, parent := range parents {
			rules.initiativeChildren[parent] = appendUnique(rules.initiativeChildren[parent], child)
		}
	}
	for _, team := range snapshot.Teams {
		rules.teamNames[team.ID] = team.Name
	}
	return rules
}

func (rules *pulseRules) projectVisible(viewer pulseViewer, project *domain.Project) bool {
	if project == nil {
		return false
	}
	if len(project.TeamIDs) == 0 {
		return viewer.Role != "guest"
	}
	return slices.ContainsFunc(project.TeamIDs, func(id string) bool { return viewer.Visible[id] })
}

// initiativeVisible mirrors the bootstrap projection: a lead team the viewer
// cannot see hides the initiative; otherwise any visible lead, contributing
// team or project reveals it, and unscoped initiatives are workspace-wide.
func (rules *pulseRules) initiativeVisible(viewer pulseViewer, initiative *domain.Initiative) bool {
	if initiative == nil || viewer.Role == "guest" {
		return false
	}
	if initiative.LeadTeamID != "" {
		return viewer.Visible[initiative.LeadTeamID]
	}
	projects := rules.initiativeProjects[initiative.ID]
	if len(initiative.ContributingTeamIDs) == 0 && len(projects) == 0 {
		return true
	}
	if slices.ContainsFunc(initiative.ContributingTeamIDs, func(id string) bool { return viewer.Visible[id] }) {
		return true
	}
	return slices.ContainsFunc(projects, func(id string) bool { return rules.projectVisible(viewer, rules.projects[id]) })
}

// sourceShown reports whether an update's project or initiative is visible
// to the viewer and not archived.
func (rules *pulseRules) sourceShown(viewer pulseViewer, entry *store.PulseFeedEntry) bool {
	if entry.Kind == "project" {
		project := rules.projects[entry.SourceID]
		return project != nil && project.ArchivedAt == nil && rules.projectVisible(viewer, project)
	}
	return rules.initiativeVisible(viewer, rules.initiatives[entry.SourceID])
}

func (rules *pulseRules) sourceName(kind, id string) string {
	if kind == "project" {
		if project := rules.projects[id]; project != nil {
			return project.Name
		}
		return ""
	}
	if initiative := rules.initiatives[id]; initiative != nil {
		return initiative.Name
	}
	return ""
}

// initiativeAncestors returns the initiatives containing id, transitively.
func (rules *pulseRules) initiativeAncestors(ids []string) []string {
	result := []string{}
	stack := slices.Clone(ids)
	seen := map[string]bool{}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
		stack = append(stack, rules.initiativeParents[id]...)
	}
	return result
}

func (rules *pulseRules) initiativeDescendants(id string) []string {
	result := []string{}
	stack := []string{id}
	seen := map[string]bool{}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[current] {
			continue
		}
		seen[current] = true
		result = append(result, current)
		stack = append(stack, rules.initiativeChildren[current]...)
	}
	return result
}

func projectMember(project *domain.Project, userID string) bool {
	return project != nil && (project.Lead != nil && project.Lead.ID == userID || slices.Contains(project.MemberIDs, userID))
}

// subscriptionReasons applies the default subscription rules and explicit
// choices to one project or initiative. An explicit unsubscribe overrides
// every default rule.
func (rules *pulseRules) subscriptionReasons(viewer pulseViewer, kind, sourceID string) []pulseReason {
	key := kind + ":" + sourceID
	if subscribed, explicit := viewer.Explicit[key]; explicit && !subscribed {
		return nil
	}
	reasons := []pulseReason{}
	add := func(kind string, ids []string) {
		reason := pulseReason{Type: kind, SourceIDs: ids, SourceNames: make([]string, 0, len(ids))}
		for _, id := range ids {
			switch kind {
			case pulseReasonTeamProjectUpdates:
				reason.SourceNames = append(reason.SourceNames, rules.teamNames[id])
			case pulseReasonInitiativeOwner:
				reason.SourceNames = append(reason.SourceNames, rules.sourceName("initiative", id))
			default:
				reason.SourceNames = append(reason.SourceNames, rules.sourceName("project", id))
			}
		}
		reasons = append(reasons, reason)
	}
	if kind == "project" {
		project := rules.projects[sourceID]
		if project == nil {
			return nil
		}
		owned := []string{}
		for _, initiativeID := range rules.initiativeAncestors(rules.projectInitiatives[sourceID]) {
			if initiative := rules.initiatives[initiativeID]; initiative != nil && initiative.Owner != nil && initiative.Owner.ID == viewer.ID {
				owned = append(owned, initiativeID)
			}
		}
		if len(owned) > 0 {
			add(pulseReasonInitiativeOwner, owned)
		}
		if projectMember(project, viewer.ID) {
			add(pulseReasonProjectMember, []string{project.ID})
		}
		if viewer.Explicit[key] {
			add(pulseReasonSubscribed, []string{project.ID})
		}
		teams := []string{}
		for _, teamID := range project.TeamIDs {
			subscribed, explicit := viewer.Explicit["team:"+teamID]
			if explicit && subscribed || !explicit && viewer.Teams[teamID] {
				teams = append(teams, teamID)
			}
		}
		followed := []string{}
		for _, initiativeID := range rules.initiativeAncestors(rules.projectInitiatives[sourceID]) {
			if viewer.InitiativeProjectUpdates[initiativeID] && rules.initiatives[initiativeID] != nil {
				followed = append(followed, initiativeID)
			}
		}
		if len(teams) > 0 || len(followed) > 0 {
			add(pulseReasonTeamProjectUpdates, teams)
			if len(followed) > 0 {
				reason := &reasons[len(reasons)-1]
				reason.InitiativeIDs = followed
				for _, id := range followed {
					reason.InitiativeNames = append(reason.InitiativeNames, rules.sourceName("initiative", id))
				}
			}
		}
		return reasons
	}
	initiative := rules.initiatives[sourceID]
	if initiative == nil {
		return nil
	}
	if initiative.Owner != nil && initiative.Owner.ID == viewer.ID {
		add(pulseReasonInitiativeOwner, []string{initiative.ID})
	}
	memberOf := []string{}
	for _, id := range rules.initiativeDescendants(initiative.ID) {
		if child := rules.initiatives[id]; child != nil {
			for _, projectID := range rules.initiativeProjects[child.ID] {
				if projectMember(rules.projects[projectID], viewer.ID) && !slices.Contains(memberOf, projectID) {
					memberOf = append(memberOf, projectID)
				}
			}
		}
	}
	if len(memberOf) > 0 {
		add(pulseReasonInitiativeProjectMember, memberOf)
	}
	if viewer.Explicit[key] {
		add(pulseReasonSubscribed, []string{initiative.ID})
	}
	return reasons
}

// forMeReasons lists why an update is in the viewer's For me feed; an empty
// result means it is not. subscribed reports the subscription rules only.
func (rules *pulseRules) forMeReasons(viewer pulseViewer, entry *store.PulseFeedEntry) (reasons []pulseReason, subscribed bool) {
	source := rules.subscriptionReasons(viewer, entry.Kind, entry.SourceID)
	subscribed = len(source) > 0
	name := rules.sourceName(entry.Kind, entry.SourceID)
	if slices.Contains(entry.Mentions, viewer.ID) {
		reasons = append(reasons, pulseReason{Type: pulseReasonMentioned, SourceIDs: []string{entry.SourceID}, SourceNames: []string{name}})
	}
	if entry.AuthorID == viewer.ID {
		reasons = append(reasons, pulseReason{Type: pulseReasonAuthor, SourceIDs: []string{entry.SourceID}, SourceNames: []string{name}})
	}
	return append(reasons, source...), subscribed
}

// pulseIsFollowing reports whether the viewer follows a source by the
// subscription rules (used for the "subscribed" flag of the subscription API).
func (rules *pulseRules) pulseIsFollowing(viewer pulseViewer, kind, sourceID string) bool {
	return len(rules.subscriptionReasons(viewer, kind, sourceID)) > 0
}

// pulseForMeSince lists the viewer's For-me entries created in (start, end],
// newest first, excluding the viewer's own updates.
func (rules *pulseRules) pulseForMeSince(viewer pulseViewer, start, end time.Time, limit int) ([]*store.PulseFeedEntry, int) {
	result := []*store.PulseFeedEntry{}
	total := 0
	for _, entry := range rules.snapshot.Recent {
		if entry.CreatedAt.After(end) {
			continue
		}
		if !entry.CreatedAt.After(start) {
			break
		}
		if entry.AuthorID == viewer.ID || !rules.sourceShown(viewer, entry) {
			continue
		}
		if reasons, _ := rules.forMeReasons(viewer, entry); len(reasons) == 0 {
			continue
		}
		total++
		if limit <= 0 || len(result) < limit {
			result = append(result, entry)
		}
	}
	return result, total
}
