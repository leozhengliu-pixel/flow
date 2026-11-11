package main

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Realtime events that are not projected per viewer (issues, comments,
// documents, projects, initiatives, notifications and the viewer's own
// records, see pagedRealtimeEvent) reach a stream by the read rule of the
// record they name, as the REST API and the bootstrap projection apply it:
//
//   - a viewer who may read the record gets a bare signal (type, id, actor):
//     clients reload the small resource the event names. A few kinds keep the
//     fields clients patch locally (a label's or status's new name, the
//     deleted label ids), and team events keep their payload for viewers who
//     see the team row;
//   - a viewer who may not read it gets nothing, unless the change can take
//     the record out of their reach (its team or scope changed, a team
//     membership changed): then an anonymous signal (type only, no id or
//     actor) makes their client reload what it holds;
//   - a record that is gone (deleted), or an event that names no record,
//     reaches the kind's audience as an anonymous signal: who could read it is
//     no longer known.
//
// Credentials, integrations, webhooks, identity providers and other workspace
// administration reach workspace admins (and the person who made the change)
// only, as bare signals. Personal records (settings, passkeys, push
// subscriptions, imports) reach their owner only. An event type no rule names
// reaches admins and its actor as a bare signal.
//
// The record is read from the workspace cache by the aggregate id at delivery,
// so live events, replays from the ring or the change log, and events from
// another instance are decided alike; the payload a peer or the log carries
// is never forwarded as it is.

// realtimeAudience is who may learn that a record of a kind changed at all.
type realtimeAudience uint8

const (
	realtimeNobody   realtimeAudience = iota
	realtimeActor                     // the actor's own streams
	realtimeSubject                   // the user the event is about (its aggregate), and the actor
	realtimeAdmins                    // workspace admins, and the actor
	realtimeMembers                   // active members other than guests
	realtimeEveryone                  // every active member, guests included
)

// realtimeRecord is what the read rules need of a record.
type realtimeRecord struct {
	TeamID     string   `json:"teamId"`
	TeamIDs    []string `json:"teamIds"`
	Scope      string   `json:"scope"`
	OwnerID    string   `json:"ownerId"`
	ProjectID  string   `json:"projectId"`
	IssueID    string   `json:"issueId"`
	IssueIDs   []string `json:"issueIds"`
	PipelineID string   `json:"pipelineId"`
}

type realtimeRule struct {
	audience realtimeAudience
	// fields are the metadata collections the aggregate id may name; with
	// teamAggregate the aggregate may also be a team id (the record is the team).
	fields        []string
	teamAggregate bool
	// payloadTeam reads the record's team from the event payload, which the
	// server builds for these kinds (a deleted status, whose ids teams share).
	payloadTeam bool
	// visible is the record's read rule for a viewer in the audience; nil
	// when the audience alone decides.
	visible func(v *realtimeViewer, record realtimeRecord) bool
	// keep are payload keys kept for viewers who may read the record;
	// keepAll keeps the payload; entityTeam sends {entity: {id, teamId}}.
	keep       []string
	keepAll    bool
	entityTeam bool
	// scope are input keys whose change can take the record out of a
	// viewer's reach; signalOutsiders: every change can.
	scope           []string
	signalOutsiders bool
	// anonymous: the aggregate id itself is private (an invitation token,
	// e-mail addresses), so even the audience gets the type only.
	anonymous bool
	// guestSelf: guests hear of it only when it is about them.
	guestSelf bool
}

type realtimeRuleEntry struct {
	match string // a prefix when it ends in "." or "_", else the exact type
	rule  realtimeRule
}

var (
	realtimeAdminRule   = realtimeRule{audience: realtimeAdmins}
	realtimeActorRule   = realtimeRule{audience: realtimeActor}
	realtimeMembersRule = realtimeRule{audience: realtimeMembers}
	// Kinds paged streams never carried: their records (cycles, customer
	// requests, asks, releases) are not in the stream's metadata, so the
	// visibility check always failed. Unchanged here.
	realtimeNobodyRule = realtimeRule{audience: realtimeNobody}
)

var realtimeRules = []realtimeRuleEntry{
	// Personal records.
	{"user_settings.", realtimeRule{audience: realtimeSubject, keepAll: true}},
	{"notification_preferences.", realtimeRule{audience: realtimeSubject, keepAll: true}},
	{"notification.desktop_acknowledged", realtimeRule{audience: realtimeSubject}},
	{"notification.delivery_retried", realtimeActorRule},
	{"integration.job_updated", realtimeRule{audience: realtimeActor, keepAll: true}},
	{"integration.member_oauth_", realtimeActorRule},
	{"agent.", realtimeRule{audience: realtimeActor, keepAll: true}},
	{"agent_session.", realtimeActorRule},
	{"agent_run.", realtimeActorRule},
	{"push_subscription.", realtimeActorRule},
	{"passkey.", realtimeActorRule},
	{"commit_signing_key.", realtimeActorRule},
	{"personal_connection.", realtimeActorRule},
	{"code_host_account.", realtimeActorRule},
	{"account.", realtimeActorRule},
	{"pulse.", realtimeActorRule},
	// Dashboards, posts and meetings are read from their own endpoints by
	// their own rules; clients do not act on these events.
	{"dashboard.", realtimeActorRule},
	{"post.", realtimeActorRule},
	{"meeting.", realtimeActorRule},
	{"agent_reply.", realtimeNobodyRule},
	{"worker.", realtimeNobodyRule},

	// Workspace administration: admins (and whoever made the change).
	{"import.", realtimeAdminRule},
	{"export.", realtimeAdminRule},
	{"trash.", realtimeAdminRule},
	{"api_key.", realtimeAdminRule},
	{"webhook.", realtimeAdminRule},
	{"oauth_application.", realtimeAdminRule},
	{"oauth_authorization.", realtimeAdminRule},
	{"oauth_token.", realtimeAdminRule},
	{"oauth_sync_group.", realtimeAdminRule},
	{"application.", realtimeAdminRule},
	{"application_policy.", realtimeAdminRule},
	{"identity_provider.", realtimeAdminRule},
	{"scim.", realtimeAdminRule},
	{"integration_delivery.", realtimeAdminRule},
	{"audit.", realtimeAdminRule},
	{"migration.", realtimeAdminRule},
	{"git_automation.", realtimeAdminRule},
	{"target_branch.", realtimeAdminRule},
	{"chat_binding.", realtimeAdminRule},
	{"chat_initiative_binding.", realtimeAdminRule},
	{"chat_tenant.", realtimeAdminRule},
	{"jira.", realtimeAdminRule},
	{"slack.", realtimeAdminRule},
	{"gitlab.", realtimeAdminRule},
	{"loadtest.", realtimeAdminRule},
	{"workspace_invitation.", realtimeRule{audience: realtimeAdmins, anonymous: true}},
	{"workspace_invitations.", realtimeRule{audience: realtimeAdmins, anonymous: true}},
	{"workspace_invite_link.", realtimeRule{audience: realtimeAdmins, anonymous: true}},

	// Workspace-wide records every member (or every non-guest) reads.
	{"workspace.", realtimeRule{audience: realtimeEveryone}},
	{"workspace_member.", realtimeRule{audience: realtimeEveryone, keep: []string{"userId"}, guestSelf: true}},
	{"user.", realtimeRule{audience: realtimeEveryone, guestSelf: true}},
	{"integration.", realtimeMembersRule},
	{"custom_emoji.", realtimeMembersRule},
	{"customer.", realtimeMembersRule},
	{"customer_taxonomy.", realtimeMembersRule},
	{"project_status.", realtimeMembersRule},
	{"project_display_default.", realtimeMembersRule},
	{"project_update_settings.", realtimeMembersRule},
	{"sla_settings.", realtimeMembersRule},
	{"settings.", realtimeMembersRule},
	{"schedules.", realtimeMembersRule},
	{"cycles.", realtimeMembersRule},
	{"coding_environment.", realtimeMembersRule},
	// Loops and automations are listed to every member (/api/loops, /api/workflows).
	{"loop.", realtimeRule{audience: realtimeEveryone}},
	{"workflow_definition.", realtimeMembersRule},
	{"workflow_run.", realtimeMembersRule},
	{"coding.approval_requested", realtimeRule{audience: realtimeEveryone}},

	// Records with a read rule of their own.
	{"customer_need.", realtimeRule{audience: realtimeMembers, fields: []string{"customerRequests"}, visible: realtimeCustomerRequestVisible}},
	{"view.", realtimeRule{audience: realtimeMembers, fields: []string{"savedViews"}, visible: realtimeViewVisible, scope: []string{"scope", "teamId", "ownerId", "projectId"}}},
	{"label.merged", realtimeRule{audience: realtimeEveryone, fields: []string{"labels"}, visible: realtimeLabelVisible, keep: []string{"label", "toLabelId"}}},
	{"label.moved_to_teams", realtimeRule{audience: realtimeEveryone, fields: []string{"labels"}, visible: realtimeLabelVisible, keep: []string{"label"}, signalOutsiders: true}},
	{"label.", realtimeRule{audience: realtimeEveryone, fields: []string{"labels"}, visible: realtimeLabelVisible, keep: []string{"label", "labelIds"}, scope: []string{"scope"}}},
	{"issue_label.", realtimeRule{audience: realtimeEveryone, fields: []string{"labels"}, visible: realtimeLabelVisible, keep: []string{"label", "labelIds"}, scope: []string{"scope"}}},
	{"label_group.", realtimeRule{audience: realtimeEveryone, fields: []string{"labelGroups"}, visible: realtimeLabelVisible, keep: []string{"labelIds"}, scope: []string{"scope"}}},
	{"workflow_state.", realtimeRule{audience: realtimeEveryone, fields: []string{"states"}, payloadTeam: true, visible: realtimeTeamRowVisible, keep: []string{"state", "teamId", "stateId", "replacementStateId"}}},
	{"workflow_states.", realtimeRule{audience: realtimeEveryone, teamAggregate: true, visible: realtimeTeamRowVisible}},
	{"issue_template.", realtimeRule{audience: realtimeEveryone, fields: []string{"issueTemplates"}, visible: realtimeTeamRowVisible, scope: []string{"teamId"}}},
	{"document_template.", realtimeRule{audience: realtimeEveryone, fields: []string{"documentTemplates"}, visible: realtimeTeamRowVisible, scope: []string{"teamId"}}},
	{"project_template.", realtimeRule{audience: realtimeEveryone, fields: []string{"projectTemplates"}, visible: realtimeAnyTeamRowVisible, scope: []string{"teamIds"}}},
	{"release_pipeline.", realtimeRule{audience: realtimeEveryone, fields: []string{"releasePipelines"}, visible: realtimeAnyTeamContentVisible, scope: []string{"teamIds"}}},
	{"sla_rule.", realtimeRule{audience: realtimeEveryone, fields: []string{"slaRules"}, visible: realtimeAnyTeamRowVisible, scope: []string{"teamIds"}}},
	{"triage_rule.", realtimeRule{audience: realtimeEveryone, fields: []string{"triageRoutingRules"}, visible: realtimeTeamRowVisible, scope: []string{"teamId"}}},
	{"triage_responsibility.", realtimeRule{audience: realtimeEveryone, fields: []string{"triageResponsibilities"}, visible: realtimeTeamRowVisible, scope: []string{"teamId"}}},
	{"email_intake.", realtimeRule{audience: realtimeMembers, fields: []string{"emailIntakeAddresses"}, visible: realtimeTeamRowVisible, scope: []string{"teamId"}}},
	{"review.", realtimeRule{audience: realtimeEveryone, fields: []string{"reviews"}, visible: realtimeReviewVisible}},
	{"code_review.", realtimeRule{audience: realtimeEveryone, fields: []string{"reviews"}, visible: realtimeReviewVisible}},
	// Teams: the team row's readers get the event as it is; a change of the
	// team's access, archive state or membership reaches everyone else as an
	// anonymous signal, since it can hide the team from them.
	{"team.resource_", realtimeRule{audience: realtimeEveryone, teamAggregate: true, fields: []string{"teamPinnedResources", "teamResourceSections"}, visible: realtimeTeamContentVisible, entityTeam: true}},
	{"team.archived", realtimeRule{audience: realtimeEveryone, teamAggregate: true, visible: realtimeTeamRowVisible, keepAll: true, signalOutsiders: true}},
	{"team.unarchived", realtimeRule{audience: realtimeEveryone, teamAggregate: true, visible: realtimeTeamRowVisible, keepAll: true, signalOutsiders: true}},
	{"team.deleted", realtimeRule{audience: realtimeEveryone, teamAggregate: true, visible: realtimeTeamRowVisible, keepAll: true, signalOutsiders: true}},
	{"team.", realtimeRule{audience: realtimeEveryone, teamAggregate: true, visible: realtimeTeamRowVisible, keepAll: true, scope: []string{"private", "access", "retired", "parentTeamId", "membershipRestriction"}}},
	{"team_member.", realtimeRule{audience: realtimeEveryone, teamAggregate: true, visible: realtimeTeamRowVisible, signalOutsiders: true}},

	{"customer_request.", realtimeNobodyRule},
	{"cycle.", realtimeNobodyRule},
	{"ask.", realtimeNobodyRule},
	{"release.", realtimeNobodyRule},
}

// realtimeRuleFor is the rule of an event type; false for a type no rule names.
func realtimeRuleFor(eventType string) (realtimeRule, bool) {
	for _, entry := range realtimeRules {
		if strings.HasSuffix(entry.match, ".") || strings.HasSuffix(entry.match, "_") {
			if strings.HasPrefix(eventType, entry.match) {
				return entry.rule, true
			}
		} else if eventType == entry.match {
			return entry.rule, true
		}
	}
	return realtimeAdminRule, false
}

// realtimeViewer is a stream's viewer as the read rules need it. Team sets
// are computed on first use.
type realtimeViewer struct {
	s     *server
	r     *http.Request
	data  domain.Bootstrap
	query store.IssueRecordQuery
	event domain.RealtimeEvent
	rows  map[string]bool
}

func (v *realtimeViewer) id() string { return v.data.Viewer.ID }

func (v *realtimeViewer) admin() bool {
	return v.s.authDisabled || workspaceAdminRole(v.data.ViewerRole)
}

func (v *realtimeViewer) actor() bool {
	return v.event.ActorID != "" && v.event.ActorID == v.id() || v.s.authDisabled && v.event.ActorID == ""
}

func (v *realtimeViewer) inAudience(rule realtimeRule) bool {
	switch rule.audience {
	case realtimeActor:
		return v.actor()
	case realtimeSubject:
		return v.actor() || v.event.AggregateID != "" && v.event.AggregateID == v.id()
	case realtimeAdmins:
		return v.admin() || v.actor()
	case realtimeMembers:
		return v.data.ViewerRole != "guest"
	case realtimeEveryone:
		return !rule.guestSelf || v.data.ViewerRole != "guest" || v.event.AggregateID == v.id()
	}
	return false
}

// keyAllowed applies an API key's team restriction.
func (v *realtimeViewer) keyAllowed(teamID string) bool {
	return v.query.AllowedTeamIDs == nil || slices.Contains(v.query.AllowedTeamIDs, teamID)
}

// row reports whether the viewer sees a team's row (its labels, statuses,
// templates and settings), as the bootstrap projection keeps it.
func (v *realtimeViewer) row(teamID string) bool {
	if v.s.authDisabled {
		return true
	}
	if v.rows == nil {
		v.rows = map[string]bool{}
		for _, team := range v.data.Teams {
			v.rows[team.ID] = store.TeamRowVisible(v.data, team.ID, v.id(), v.data.ViewerRole) && v.keyAllowed(team.ID)
		}
	}
	return v.rows[teamID]
}

// content reports whether the viewer reads a team's content (issues,
// projects, views, pipelines), admins of private teams they have not joined
// excluded, as the issue queries apply it.
func (v *realtimeViewer) content(teamID string) bool {
	access := v.query.Access
	if access != nil && slices.Contains(access.ArchivedTeamIDs, teamID) {
		return false
	}
	return (access == nil || access.Admin || slices.Contains(access.VisibleTeamIDs, teamID)) && v.keyAllowed(teamID)
}

func (v *realtimeViewer) projectVisible(id string) bool {
	project, ok := v.s.store.ProjectByID(workspaceKey(v.r), id)
	return ok && (len(project.TeamIDs) == 0 || slices.ContainsFunc(project.TeamIDs, v.content))
}

func (v *realtimeViewer) anyIssueVisible(ids []string) bool {
	visible, err := v.s.store.VisibleIssueRecordIDs(v.r.Context(), v.query, ids)
	return err == nil && slices.ContainsFunc(ids, func(id string) bool { return visible[id] })
}

func realtimeTeamRowVisible(v *realtimeViewer, record realtimeRecord) bool {
	return record.TeamID == "" || v.row(record.TeamID)
}

func realtimeTeamContentVisible(v *realtimeViewer, record realtimeRecord) bool {
	return record.TeamID == "" || v.content(record.TeamID)
}

func realtimeAnyTeamRowVisible(v *realtimeViewer, record realtimeRecord) bool {
	return len(record.TeamIDs) == 0 || slices.ContainsFunc(record.TeamIDs, v.row)
}

func realtimeAnyTeamContentVisible(v *realtimeViewer, record realtimeRecord) bool {
	return len(record.TeamIDs) == 0 || slices.ContainsFunc(record.TeamIDs, v.content)
}

// realtimeLabelVisible: a workspace label (or group) is everyone's, a team's
// follows the team row.
func realtimeLabelVisible(v *realtimeViewer, record realtimeRecord) bool {
	scope := strings.TrimSpace(record.Scope)
	return workspaceScope(scope) || v.row(scope)
}

// realtimeViewVisible: a personal view is its owner's, a team view follows
// the team's content, and a project's view the project.
func realtimeViewVisible(v *realtimeViewer, record realtimeRecord) bool {
	switch record.Scope {
	case "personal":
		if record.OwnerID != v.id() {
			return false
		}
	case "team":
		if !v.content(record.TeamID) {
			return false
		}
	}
	return record.ProjectID == "" || v.projectVisible(record.ProjectID)
}

// realtimeReviewVisible: a review linked to issues follows them.
func realtimeReviewVisible(v *realtimeViewer, record realtimeRecord) bool {
	return len(record.IssueIDs) == 0 || v.anyIssueVisible(record.IssueIDs)
}

func realtimeCustomerRequestVisible(v *realtimeViewer, record realtimeRecord) bool {
	return (record.IssueID == "" || v.anyIssueVisible([]string{record.IssueID})) && (record.ProjectID == "" || v.projectVisible(record.ProjectID))
}

// realtimeRecordFor reads the record an event names, once per event (the
// result does not depend on the viewer). found is false when no record has
// the aggregate id: it is gone, or the event names none.
func (s *server) realtimeRecordFor(r *http.Request, data domain.Bootstrap, rule realtimeRule, event domain.RealtimeEvent) (realtimeRecord, bool) {
	type cached struct {
		Found  bool           `json:"found"`
		Record realtimeRecord `json:"record"`
	}
	raw := s.realtime.cachedPayload(event.ID+"|record", func() json.RawMessage {
		result := cached{}
		if rule.payloadTeam {
			var payload struct {
				TeamID string `json:"teamId"`
				State  struct {
					TeamID string `json:"teamId"`
				} `json:"state"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			if teamID := cmp.Or(payload.TeamID, payload.State.TeamID); teamID != "" {
				result = cached{Found: true, Record: realtimeRecord{TeamID: teamID}}
			}
		}
		if !result.Found && rule.teamAggregate && slices.ContainsFunc(data.Teams, func(team domain.Team) bool { return team.ID == event.AggregateID }) {
			result = cached{Found: true, Record: realtimeRecord{TeamID: event.AggregateID}}
		}
		for _, field := range rule.fields {
			if result.Found {
				break
			}
			if record, ok := s.store.MetadataRecord(workspaceKey(r), field, event.AggregateID); ok {
				result.Found = json.Unmarshal(record, &result.Record) == nil
			}
		}
		encoded, _ := json.Marshal(result)
		return encoded
	})
	var result cached
	_ = json.Unmarshal(raw, &result)
	return result.Record, result.Found
}

// anonymousRealtimeEvent tells a viewer that something of the event's kind
// changed, without naming it or who changed it.
func anonymousRealtimeEvent(event domain.RealtimeEvent) domain.RealtimeEvent {
	return domain.RealtimeEvent{ID: event.ID, Seq: event.Seq, Type: event.Type, ClientID: event.ClientID, CreatedAt: event.CreatedAt}
}

// realtimeInputChanged reports whether the event's input sets one of keys
// (any letter case: some inputs are encoded with Go field names).
func realtimeInputChanged(event domain.RealtimeEvent, keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(event.Payload, &payload) != nil {
		return false
	}
	for key := range payload {
		if slices.ContainsFunc(keys, func(want string) bool { return strings.EqualFold(key, want) }) {
			return true
		}
	}
	return false
}

// realtimeSignal is the event a viewer who may read its record receives.
func realtimeSignal(rule realtimeRule, event domain.RealtimeEvent, record realtimeRecord) domain.RealtimeEvent {
	if rule.keepAll {
		return event
	}
	var input map[string]json.RawMessage
	_ = json.Unmarshal(event.Payload, &input)
	payload := map[string]any{}
	for _, key := range rule.keep {
		if value, ok := input[key]; ok {
			payload[key] = value
		}
	}
	// A label merged into a team label merges labels of that team only; into
	// a workspace label it may merge other teams' labels, whose ids stay out.
	if value, ok := input["fromLabelIds"]; ok && event.Type == "label.merged" && !workspaceScope(strings.TrimSpace(record.Scope)) {
		payload["fromLabelIds"] = value
	}
	if rule.entityTeam && record.TeamID != "" {
		payload["entity"] = map[string]string{"id": event.AggregateID, "teamId": record.TeamID}
	}
	if strings.HasSuffix(event.Type, ".deleted") {
		payload["deleted"] = true
	}
	return bareRealtimeEvent(event, payload)
}

// pagedPolicyEvent decides an event no projection handles (see the top of
// this file).
func (s *server) pagedPolicyEvent(r *http.Request, data domain.Bootstrap, query store.IssueRecordQuery, event domain.RealtimeEvent) (domain.RealtimeEvent, bool) {
	if event.Type == "connected" || event.Type == "workspace.resync_required" {
		return event, true
	}
	rule, _ := realtimeRuleFor(event.Type)
	viewer := &realtimeViewer{s: s, r: r, data: data, query: query, event: event}
	if !viewer.inAudience(rule) {
		return event, false
	}
	if rule.anonymous {
		return anonymousRealtimeEvent(event), true
	}
	if event.Type == "coding.approval_requested" && !s.codingSessionRealtimeVisible(r, event.AggregateID) {
		return event, false
	}
	if rule.visible == nil {
		return realtimeSignal(rule, event, realtimeRecord{}), true
	}
	record, found := s.realtimeRecordFor(r, data, rule, event)
	if !found {
		return anonymousRealtimeEvent(event), true
	}
	if rule.visible(viewer, record) {
		return realtimeSignal(rule, event, record), true
	}
	if rule.signalOutsiders || realtimeInputChanged(event, rule.scope) {
		return anonymousRealtimeEvent(event), true
	}
	return event, false
}
