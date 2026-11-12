package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

var ErrEventSourceBudget = errors.New("realtime source exceeds its budget")

const eventSourceMaxReferences = 4096

// EventSourceVersion includes legacy issue writers as well as metadata
// writers. A team move must retire a source even when no metadata row changed.
type EventSourceVersion struct {
	WorkspaceID      string
	Metadata, Issues int64
}

func (s *SQLiteStore) RealtimeSourceVersion(ctx context.Context, workspace string) (EventSourceVersion, error) {
	ctx, err := s.LiveEventContext(ctx)
	if err != nil {
		return EventSourceVersion{}, err
	}
	var version EventSourceVersion
	err = s.db.QueryRowContext(ctx, `SELECT workspace_id,
	 (SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=? AND kind='metadata'),
	 (SELECT COALESCE(SUM(revision),0) FROM workspace_cache_revisions WHERE workspace_key=? AND kind='issues')+
	 (SELECT COALESCE(SUM(revision),0) FROM issue_count_revisions WHERE workspace_key=?)
	 FROM workspace_states WHERE workspace_key=?`, workspace, workspace, workspace, workspace).Scan(&version.WorkspaceID, &version.Metadata, &version.Issues)
	return version, err
}

// EventAccess binds immutable P03 policy to a source snapshot. Cached policy
// must match live authority at delivery, never just when an event is queued.
type EventAccess struct {
	AccessContext
	version   EventSourceVersion
	source    *EventPolicySource
	expiresAt *time.Time
}

// EventPolicySource is the immutable workspace policy shared by an event's
// viewers. Principal contexts are validated by epoch at the delivery boundary.
type EventPolicySource struct {
	store     *SQLiteStore
	workspace string
	version   EventSourceVersion
	metadata  domain.Bootstrap
	bytes     int
}

type eventPolicySourceKey struct{}

func eventPolicySourceFor(ctx context.Context, s *SQLiteStore, workspace string, stamp copyStamp) (domain.Bootstrap, bool) {
	source, ok := ctx.Value(eventPolicySourceKey{}).(*EventPolicySource)
	if !ok || source.store != s || source.workspace != workspace || source.version.WorkspaceID != stamp.workspaceID || source.version.Metadata != stamp.revision {
		return domain.Bootstrap{}, false
	}
	return source.metadata, true
}

func (source *EventPolicySource) Bytes() int { return source.bytes }
func (source *EventPolicySource) HasTeam(id string) bool {
	return slices.ContainsFunc(source.metadata.Teams, func(team domain.Team) bool { return team.ID == id })
}
func (source *EventPolicySource) DocumentClosedTeams(document domain.Document) []string {
	return NewDocumentLinkScope(source.metadata.Teams, source.metadata.TeamSettings).ClosedTeams(document)
}

func (s *SQLiteStore) ReadEventPolicySource(ctx context.Context, workspace string, version EventSourceVersion) (*EventPolicySource, error) {
	s.mu.RLock()
	data, ok := s.workspaces[workspace]
	known, exact := s.copyRevisionLocked(workspace)
	current := ok && exact && known.workspaceID == version.WorkspaceID && known.revision == version.Metadata
	if current {
		data = resourcePolicyMetadata(data)
	}
	s.mu.RUnlock()
	if !current {
		var err error
		data, err = s.resourcePolicyRecords(ctx, workspace, version.WorkspaceID)
		if err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if len(raw) > 256<<10 {
		return nil, ErrEventSourceBudget
	}
	return &EventPolicySource{store: s, workspace: workspace, version: version, metadata: data, bytes: len(raw)*3 + 1024}, nil
}

func (a EventAccess) SourceVersion() EventSourceVersion { return a.version }
func (a EventAccess) WorkspaceKey() string {
	if a.policy == nil {
		return ""
	}
	return a.policy.workspace
}

func (a EventAccess) Validate(ctx context.Context) error {
	if a.policy == nil {
		return ErrAuthForbidden
	}
	return a.policy.store.ValidateEventAccess(ctx, a)
}

func (s *SQLiteStore) RefreshEventAccess(ctx context.Context, source *EventPolicySource, previous EventAccess) (EventAccess, error) {
	if err := previous.checkBinding(s); err != nil {
		return EventAccess{}, err
	}
	return s.ResolveEventAccessFromSource(ctx, source, previous.PrincipalID, previous.policy.key)
}

func (a EventAccess) PermissionFingerprint() string {
	if a.policy == nil {
		return ""
	}
	data := a.policy.metadata
	query, err := a.RealtimeQuery()
	if err != nil {
		return ""
	}
	rows, members := []string{}, []string{}
	for _, team := range data.Teams {
		if TeamRowVisible(data, team.ID, a.PrincipalID, data.ViewerRole) {
			rows = append(rows, team.ID)
		}
	}
	for _, member := range data.TeamMembers {
		members = append(members, member.TeamID)
	}
	slices.Sort(rows)
	slices.Sort(members)
	slices.Sort(query.Access.VisibleTeamIDs)
	slices.Sort(query.Access.ArchivedTeamIDs)
	raw, _ := json.Marshal(struct {
		Workspace, Principal, Credential, Role string
		Active, Deleted, App                   bool
		AppTeams, Members, Rows                []string
		Access                                 *IssueRecordAccess
	}{a.WorkspaceID, a.PrincipalID, a.CredentialScope, data.ViewerRole, data.Viewer.Active, data.Viewer.Deleted, data.Viewer.App, data.Viewer.AppTeamIDs, members, rows, query.Access})
	sum := sha256.Sum256(raw)
	return string(sum[:])
}

func (s *SQLiteStore) ResolveEventAccess(ctx context.Context, workspace, principal string, key *domain.APIKey) (EventAccess, error) {
	version, err := s.RealtimeSourceVersion(ctx, workspace)
	if err != nil {
		return EventAccess{}, err
	}
	source, err := s.ReadEventPolicySource(ctx, workspace, version)
	if err != nil {
		return EventAccess{}, err
	}
	return s.ResolveEventAccessFromSource(ctx, source, principal, key)
}

func (s *SQLiteStore) ResolveEventAccessFromSource(ctx context.Context, source *EventPolicySource, principal string, key *domain.APIKey) (EventAccess, error) {
	if source == nil || source.store != s || principal == "" {
		return EventAccess{}, ErrAuthForbidden
	}
	ctx, err := s.LiveEventContext(ctx)
	if err != nil {
		return EventAccess{}, err
	}
	workspace := source.workspace
	var expiresAt *time.Time
	if key != nil {
		var raw []byte
		if err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='apiKeys' AND record_key=?`, workspace, key.ID).Scan(&raw); err != nil {
			return EventAccess{}, err
		}
		var live domain.APIKey
		if err := json.Unmarshal(raw, &live); err != nil {
			return EventAccess{}, err
		}
		if live.RevokedAt != nil || live.ExpiresAt != nil && !live.ExpiresAt.After(time.Now()) || live.SubjectID() != principal || !reflect.DeepEqual(resourceAccessCredential(key), resourceAccessCredential(&live)) {
			return EventAccess{}, ErrAuthForbidden
		}
		if live.ExpiresAt != nil {
			deadline := *live.ExpiresAt
			expiresAt = &deadline
		}
	}
	ctx = context.WithValue(ctx, eventPolicySourceKey{}, source)
	access, err := s.ResolveAccess(ctx, workspace, principal, key)
	if err != nil {
		return EventAccess{}, err
	}
	if access.policy.stamp.workspaceID != source.version.WorkspaceID || access.policy.stamp.revision != source.version.Metadata {
		return EventAccess{}, ErrAuthForbidden
	}
	if !access.policy.metadata.Viewer.Active || access.policy.metadata.Viewer.Deleted {
		return EventAccess{}, ErrAuthForbidden
	}
	return EventAccess{AccessContext: access, version: source.version, source: source, expiresAt: expiresAt}, nil
}

func (s *SQLiteStore) ValidateEventAccess(ctx context.Context, access EventAccess) error {
	if err := access.checkBinding(s); err != nil {
		return err
	}
	p := access.policy
	version, err := s.RealtimeSourceVersion(ctx, p.workspace)
	if err != nil {
		return err
	}
	if version != access.version {
		return ErrAuthForbidden
	}
	if err := access.CheckCredential(); err != nil {
		return err
	}
	epoch, err := s.AuthzEpoch(ctx, access.WorkspaceID)
	if errors.Is(err, ErrRecordJournalUnavailable) {
		current, resolveErr := s.ResolveEventAccessFromSource(ctx, access.source, p.principal, p.key)
		if resolveErr != nil {
			return resolveErr
		}
		epoch, err = current.AuthzEpoch, nil
	}
	if err != nil {
		return err
	}
	if epoch != access.AuthzEpoch {
		return ErrAuthForbidden
	}
	return nil
}

// RealtimePolicy returns an owned small view; mutable policy maps never escape.
func (a EventAccess) RealtimePolicy() (domain.Bootstrap, IssueRecordQuery, error) {
	query, err := a.RealtimeQuery()
	if err != nil {
		return domain.Bootstrap{}, query, err
	}
	return cloneBootstrap(a.policy.metadata), query, nil
}

func (a EventAccess) RealtimeQuery() (IssueRecordQuery, error) {
	if a.policy == nil {
		return IssueRecordQuery{}, ErrAuthForbidden
	}
	if err := a.checkBinding(a.policy.store); err != nil {
		return IssueRecordQuery{}, err
	}
	data := a.policy.metadata
	visible := visibleIssueTeams(data, data.Viewer.ID, data.ViewerRole)
	active := 0
	for _, team := range data.Teams {
		if team.ArchivedAt == nil {
			active++
		}
	}
	admin := isWorkspaceAdminRole(data.ViewerRole)
	query := IssueRecordQuery{Workspace: a.policy.workspace, Access: &IssueRecordAccess{UserID: data.Viewer.ID, WorkspaceID: data.Workspace.ID, Admin: admin && len(visible) == active, WorkspaceAdmin: admin, VisibleTeamIDs: visible, ArchivedTeamIDs: ArchivedTeamIDs(data)}}
	if a.policy.selected() {
		query.AllowedTeamIDs = append([]string{}, a.policy.key.TeamIDs...)
	}
	return query, nil
}

type realtimeIssueScope struct {
	team, parent string
	grants       []domain.IssuePermission
}

// RealtimeIssueSource contains only the touched records, their referenced
// scopes and ancestor grants. Projection below does no SQL and owns its slices.
type RealtimeIssueSource struct {
	workspace string
	version   EventSourceVersion
	issues    map[string]domain.Issue
	scopes    map[string]realtimeIssueScope
	projects  map[string][]string
	labels    map[string]string
	bytes     int
}

func (source *RealtimeIssueSource) Bytes() int { return source.bytes }

func (source *RealtimeIssueSource) AuthorizationFingerprint() string {
	type scope struct {
		Team, Parent string
		Grants       []domain.IssuePermission
	}
	values := map[string]scope{}
	for id, item := range source.scopes {
		values[id] = scope{item.team, item.parent, item.grants}
	}
	type actors struct {
		Creator            domain.User
		Assignee, Delegate *domain.User
	}
	users := map[string]actors{}
	for id, item := range source.issues {
		users[id] = actors{item.Creator, item.Assignee, item.Delegate}
	}
	raw, _ := json.Marshal(struct {
		Scopes   map[string]scope
		Projects map[string][]string
		Labels   map[string]string
		Actors   map[string]actors
	}{values, source.projects, source.labels, users})
	sum := sha256.Sum256(raw)
	return string(sum[:])
}

func (s *SQLiteStore) ReadRealtimeIssueSource(ctx context.Context, workspace string, ids []string, version EventSourceVersion) (*RealtimeIssueSource, error) {
	return s.readRealtimeIssueSource(ctx, workspace, ids, version, true)
}

func (s *SQLiteStore) ReadRealtimeIssueAccess(ctx context.Context, workspace string, ids []string, version EventSourceVersion) (*RealtimeIssueSource, error) {
	return s.readRealtimeIssueSource(ctx, workspace, ids, version, false)
}

func (s *SQLiteStore) readRealtimeIssueSource(ctx context.Context, workspace string, ids []string, version EventSourceVersion, hydrate bool) (*RealtimeIssueSource, error) {
	if hydrate && len(ids) > 50 || len(ids) > eventSourceMaxReferences {
		return nil, ErrEventSourceBudget
	}
	source := &RealtimeIssueSource{workspace: workspace, version: version, issues: map[string]domain.Issue{}, scopes: map[string]realtimeIssueScope{}, projects: map[string][]string{}, labels: map[string]string{}}
	refs, projectIDs, labelIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		refs[id] = true
		if !hydrate {
			continue
		}
		if _, seen := source.issues[id]; seen {
			continue
		}
		var raw []byte
		var size int
		if err := s.db.QueryRowContext(ctx, `SELECT LENGTH(data),CASE WHEN LENGTH(data)<=? THEN data ELSE NULL END FROM issue_records WHERE workspace_key=? AND id=?`, (1<<20)-source.bytes, workspace, id).Scan(&size, &raw); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return nil, err
		}
		if raw == nil {
			return nil, ErrEventSourceBudget
		}
		issue, err := unmarshalIssueRecord(raw)
		if err != nil {
			return nil, err
		}
		// Dedicated progress events can change this state without advancing an
		// issue version. Keep the established live decoration for those issues.
		if issue.AgentSessionID != "" {
			return nil, ErrEventSourceBudget
		}
		source.bytes += size*2 + 512
		if source.bytes > 2<<20 {
			return nil, ErrEventSourceBudget
		}
		issue, err = s.resolveRealtimeIssueReferences(ctx, workspace, issue)
		if err != nil {
			return nil, err
		}
		source.issues[id] = issue
		refs[id] = true
		if issue.ParentID != nil {
			refs[*issue.ParentID] = true
		}
		for _, ref := range append(append(slices.Clone(issue.SubIssueIDs), issue.SuggestedDuplicateIDs...), issue.SuggestedRelatedIDs...) {
			refs[ref] = true
		}
		for _, relation := range issue.Relations {
			refs[relation.RelatedIssueID] = true
		}
		if issue.Project != nil {
			projectIDs[issue.Project.ID] = true
		}
		for _, ref := range issue.SuggestedProjectIDs {
			projectIDs[ref] = true
		}
		for _, label := range issue.Labels {
			labelIDs[label.ID] = true
		}
		for _, ref := range issue.SuggestedLabelIDs {
			labelIDs[ref] = true
		}
	}
	agentIDs := []string{}
	for _, issue := range source.issues {
		if issue.AgentSessionID != "" {
			agentIDs = append(agentIDs, issue.AgentSessionID)
		}
	}
	if len(agentIDs) > 0 {
		states, err := s.AgentSessionStates(ctx, workspace, agentIDs)
		if err != nil {
			return nil, err
		}
		for id, issue := range source.issues {
			if issue.AgentSessionID != "" {
				issue.AgentSessionState = states[issue.AgentSessionID]
				source.issues[id] = issue
			}
		}
	}
	seen := map[string]bool{}
	for len(refs) > 0 {
		keys := []string{}
		for id := range refs {
			if !seen[id] {
				keys = append(keys, id)
				seen[id] = true
			}
		}
		if len(keys) == 0 {
			break
		}
		if len(seen) > eventSourceMaxReferences {
			return nil, ErrEventSourceBudget
		}
		refs = map[string]bool{}
		for start := 0; start < len(keys); start += 200 {
			where, args := bindList("i.id", keys[start:min(start+200, len(keys))])
			rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.team_id,i.parent_id,p.subject_type,p.subject_id,p.role FROM issue_records i LEFT JOIN issue_permission_records p ON p.workspace_key=i.workspace_key AND p.issue_id=i.id WHERE i.workspace_key=? AND `+where+` LIMIT 4097`, append([]any{workspace}, args...)...)
			if err != nil {
				return nil, err
			}
			count := 0
			for rows.Next() {
				var id, team, parent string
				var kind, subject, role sql.NullString
				if err := rows.Scan(&id, &team, &parent, &kind, &subject, &role); err != nil {
					rows.Close()
					return nil, err
				}
				count++
				if count > eventSourceMaxReferences {
					rows.Close()
					return nil, ErrEventSourceBudget
				}
				scope := source.scopes[id]
				scope.team, scope.parent = team, parent
				if kind.Valid {
					scope.grants = append(scope.grants, domain.IssuePermission{SubjectType: kind.String, SubjectID: subject.String, Role: role.String})
				}
				source.scopes[id] = scope
				source.bytes += len(id) + len(team) + len(parent) + len(kind.String) + len(subject.String) + len(role.String) + 256
				if source.bytes > 2<<20 {
					rows.Close()
					return nil, ErrEventSourceBudget
				}
				if parent != "" && !seen[parent] {
					refs[parent] = true
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	for _, field := range []string{"projects", "labels"} {
		ids, property := projectIDs, "teamIds"
		if field == "labels" {
			ids, property = labelIDs, "scope"
		}
		keys := []string{}
		for id := range ids {
			keys = append(keys, id)
		}
		if len(keys) > eventSourceMaxReferences {
			return nil, ErrEventSourceBudget
		}
		for start := 0; start < len(keys); start += 200 {
			where, args := bindList("record_key", keys[start:min(start+200, len(keys))])
			rows, err := s.db.QueryContext(ctx, `SELECT record_key,`+s.jsonText("data", property)+` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND `+where, append([]any{workspace, field}, args...)...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				var value sql.NullString
				if err := rows.Scan(&id, &value); err != nil {
					rows.Close()
					return nil, err
				}
				source.bytes += len(id) + len(value.String)*2 + 128
				if source.bytes > 2<<20 {
					rows.Close()
					return nil, ErrEventSourceBudget
				}
				if field == "projects" {
					var teams []string
					if value.Valid {
						if err := json.Unmarshal([]byte(value.String), &teams); err != nil {
							rows.Close()
							return nil, err
						}
					}
					source.projects[id] = teams
				} else {
					source.labels[id] = value.String
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	// Detect changes during the source build, including a grant being removed.
	current, err := s.RealtimeSourceVersion(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if current != version {
		return nil, ErrAuthForbidden
	}
	return source, nil
}

func (source *RealtimeIssueSource) Visible(query IssueRecordQuery, id string) bool {
	scope, found := source.scopes[id]
	if !found {
		return false
	}
	access := query.Access
	if query.Workspace != source.workspace || query.AllowedTeamIDs != nil && !slices.Contains(query.AllowedTeamIDs, scope.team) {
		return false
	}
	if access == nil {
		return true
	}
	if slices.Contains(access.ArchivedTeamIDs, scope.team) {
		return false
	}
	if access.Admin || slices.Contains(access.VisibleTeamIDs, scope.team) {
		return true
	}
	seen := map[string]bool{}
	for current := id; current != "" && !seen[current]; {
		seen[current] = true
		ancestor, ok := source.scopes[current]
		if !ok {
			break
		}
		for _, grant := range ancestor.grants {
			if grant.SubjectType == "user" && grant.SubjectID == access.UserID || grant.SubjectType == "workspace" && (grant.SubjectID == "" || grant.SubjectID == access.WorkspaceID || grant.SubjectID == query.Workspace) || grant.SubjectType == "team" && slices.Contains(access.VisibleTeamIDs, grant.SubjectID) {
				return true
			}
		}
		current = ancestor.parent
	}
	return false
}

func (source *RealtimeIssueSource) Project(query IssueRecordQuery, ids []string) ([]json.RawMessage, error) {
	issues := source.project(query, ids)
	encoded := make([]json.RawMessage, 0, len(issues))
	for _, issue := range issues {
		raw, err := json.Marshal(issue)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, raw)
	}
	return encoded, nil
}

func (source *RealtimeIssueSource) project(query IssueRecordQuery, ids []string) []domain.Issue {
	issues := []domain.Issue{}
	allowed := func(team string) bool {
		return (query.Access == nil || query.Access.Admin || slices.Contains(query.Access.VisibleTeamIDs, team)) && (query.AllowedTeamIDs == nil || slices.Contains(query.AllowedTeamIDs, team))
	}
	projectAllowed := func(id string) bool {
		teams, found := source.projects[id]
		return found && (len(teams) == 0 && query.AllowedTeamIDs == nil || slices.ContainsFunc(teams, allowed))
	}
	labelAllowed := func(id string) bool {
		scope, found := source.labels[id]
		normalized := strings.ToLower(strings.TrimSpace(scope))
		return found && (normalized == "" || normalized == "workspace" || allowed(scope))
	}
	for _, id := range ids {
		issue, found := source.issues[id]
		if !found || !source.Visible(query, id) {
			continue
		}
		if (query.Access == nil || query.Access.Admin) && query.AllowedTeamIDs == nil {
			issues = append(issues, issue)
			continue
		}
		if issue.ParentID != nil && !source.Visible(query, *issue.ParentID) {
			issue.ParentID = nil
		}
		issue.SubIssueIDs = slices.DeleteFunc(slices.Clone(issue.SubIssueIDs), func(id string) bool { return !source.Visible(query, id) })
		issue.Relations = slices.DeleteFunc(slices.Clone(issue.Relations), func(relation domain.IssueRelation) bool { return !source.Visible(query, relation.RelatedIssueID) })
		issue.Labels = slices.DeleteFunc(slices.Clone(issue.Labels), func(label domain.IssueLabel) bool { return !labelAllowed(label.ID) })
		issue.SuggestedLabelIDs = slices.DeleteFunc(slices.Clone(issue.SuggestedLabelIDs), func(id string) bool { return !labelAllowed(id) })
		issue.SuggestedProjectIDs = slices.DeleteFunc(slices.Clone(issue.SuggestedProjectIDs), func(id string) bool { return !projectAllowed(id) })
		issue.SuggestedTeamIDs = slices.DeleteFunc(slices.Clone(issue.SuggestedTeamIDs), func(id string) bool { return !allowed(id) })
		issue.SuggestedDuplicateIDs = slices.DeleteFunc(slices.Clone(issue.SuggestedDuplicateIDs), func(id string) bool { return !source.Visible(query, id) })
		issue.SuggestedRelatedIDs = slices.DeleteFunc(slices.Clone(issue.SuggestedRelatedIDs), func(id string) bool { return !source.Visible(query, id) })
		if issue.Project != nil && !projectAllowed(issue.Project.ID) {
			issue.Project, issue.ProjectMilestoneID = nil, nil
		}
		issues = append(issues, issue)
	}
	return issues
}

func (source *RealtimeIssueSource) ProjectionKey(query IssueRecordQuery) string {
	decisions := []string{}
	for id := range source.scopes {
		if source.Visible(query, id) {
			decisions = append(decisions, "i:"+id)
		}
	}
	for id, teams := range source.projects {
		allowed := len(teams) == 0 && query.AllowedTeamIDs == nil
		for _, team := range teams {
			allowed = allowed || (query.Access == nil || query.Access.Admin || slices.Contains(query.Access.VisibleTeamIDs, team)) && (query.AllowedTeamIDs == nil || slices.Contains(query.AllowedTeamIDs, team))
		}
		if allowed {
			decisions = append(decisions, "p:"+id)
		}
	}
	for id, scope := range source.labels {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "" || normalized == "workspace" || (query.Access == nil || query.Access.Admin || slices.Contains(query.Access.VisibleTeamIDs, scope)) && (query.AllowedTeamIDs == nil || slices.Contains(query.AllowedTeamIDs, scope)) {
			decisions = append(decisions, "l:"+id)
		}
	}
	if (query.Access == nil || query.Access.Admin) && query.AllowedTeamIDs == nil {
		decisions = append(decisions, "full")
	}
	for _, issue := range source.issues {
		for _, id := range issue.SuggestedTeamIDs {
			if (query.Access == nil || query.Access.Admin || slices.Contains(query.Access.VisibleTeamIDs, id)) && (query.AllowedTeamIDs == nil || slices.Contains(query.AllowedTeamIDs, id)) {
				decisions = append(decisions, "t:"+id)
			}
		}
	}
	slices.Sort(decisions)
	raw, _ := json.Marshal(decisions)
	sum := sha256.Sum256(raw)
	return string(sum[:])
}
