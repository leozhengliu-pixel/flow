package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// NotificationVisibilityRead owns returned notification bodies through response
// encoding. Query and State each consume the snapshot. Query callers keep this
// handle alive through encoding; State returns only its small owned counter
// envelope and at most fifty changed IDs, as validated by its realtime caller.
type NotificationVisibilityRead struct {
	store      *SQLiteStore
	access     AccessContext
	ctx, live  context.Context
	tx         *sqlTx
	source     *InstalledMetadataRead
	owner      *ResourceRead
	policy     *resourceAccessPolicy
	installed  bool
	issueQuery IssueRecordQuery
	used       bool
}

var notificationVisibilityFields = map[string][]string{
	"projects":    {"ID", "TeamIDs"},
	"initiatives": {"ID", "LeadTeamID", "ContributingTeamIDs", "ProjectIDs"},
	"documents":   {"ID", "TeamIDs", "InheritedTeamIDs", "Permissions", "Creator", "CreatedAt"},
	"cycles":      {"ID", "TeamID"},
	"reviews":     {"ID", "SlugID", "IssueIDs"},
}

// BeginNotificationVisibilityRead is the shared selected-credential predicate
// for notification lists, unread recovery and changed-ID projection. It never
// hydrates the metadata root or unrelated resource bodies.
func (s *SQLiteStore) BeginNotificationVisibilityRead(ctx context.Context, access AccessContext) (_ *NotificationVisibilityRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := access.checkBinding(s); err != nil {
		return nil, err
	}
	if !access.policy.selected() {
		return nil, ErrAuthForbidden
	}
	live := context.WithValue(ctx, workspaceReadKey{}, workspaceRead{})
	work, release, err := s.tryAdmitAdditionalMetadataWork(live, 2<<20)
	if err != nil {
		return nil, err
	}
	r := &NotificationVisibilityRead{store: s, access: access, live: live, owner: &ResourceRead{store: s, release: []func(){release}}}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	r.tx, err = s.db.BeginTx(work, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	workspace := access.policy.workspace
	r.ctx = context.WithValue(work, workspaceReadKey{}, workspaceRead{database: s.db, workspace: workspace, reader: r.tx})
	current, err := s.ResolveAccess(r.ctx, workspace, access.PrincipalID, access.policy.key)
	if err != nil {
		return nil, err
	}
	if current.AuthzEpoch != access.AuthzEpoch {
		return nil, ErrAuthForbidden
	}
	p := current.policy
	r.policy = p
	r.source, _, err = s.BeginInstalledMetadataRead(r.ctx, workspace)
	if err != nil {
		return nil, err
	}
	if r.source == nil {
		return nil, ErrAuthForbidden
	}
	// BeginWorkspaceRead historically chose the installed M only when its
	// certified revision matched this SQL snapshot.
	r.installed = r.source.source.Stamp() == p.stamp
	visible := visibleIssueTeams(p.metadata, access.PrincipalID, p.metadata.ViewerRole)
	active := 0
	for _, team := range p.metadata.Teams {
		if team.ArchivedAt == nil {
			active++
		}
	}
	admin := isWorkspaceAdminRole(p.metadata.ViewerRole)
	issueAccess := &IssueRecordAccess{UserID: access.PrincipalID, WorkspaceID: access.WorkspaceID, Admin: admin && len(visible) == active, WorkspaceAdmin: admin, VisibleTeamIDs: visible, ArchivedTeamIDs: ArchivedTeamIDs(p.metadata)}
	allowed := slices.Clone(p.key.TeamIDs)
	if allowed == nil {
		allowed = []string{}
	}
	r.issueQuery = IssueRecordQuery{Workspace: workspace, Access: issueAccess, AllowedTeamIDs: allowed}
	return r, nil
}

func (r *NotificationVisibilityRead) Close() {
	if r == nil {
		return
	}
	if r.tx != nil {
		_ = r.tx.Rollback()
		r.tx = nil
	}
	if r.source != nil {
		r.source.Close()
		r.source = nil
	}
	if r.owner != nil {
		r.owner.Close()
		r.owner = nil
	}
	r.policy, r.ctx, r.live = nil, nil, nil
	r.access = AccessContext{}
}

func (r *NotificationVisibilityRead) finish() error {
	if r.tx == nil || r.used {
		return ErrMetadataRecordQuery
	}
	r.used = true
	err := r.tx.Rollback()
	r.tx = nil
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		return err
	}
	return r.store.ValidateAccess(r.live, r.access)
}

// Only matching identities enter decoded memory. A count discards each
// notification batch's reference closure before reading the next batch.
func (r *NotificationVisibilityRead) referenceHeaders(owner *ResourceRead, kind, ref string) ([]reflect.Value, error) {
	names := notificationVisibilityFields[kind]
	result := []reflect.Value{}
	if r.installed {
		err := r.source.source.VisitFields(r.ctx, func(name string, collection metadataLogicalNode) error {
			if name != kind {
				return nil
			}
			return collection.Children(r.ctx, func(_ reflect.Value, node metadataLogicalNode) error {
				matched := false
				err := node.Children(r.ctx, func(key reflect.Value, child metadataLogicalNode) error {
					if key.String() != "ID" && (kind != "reviews" || key.String() != "SlugID") {
						return nil
					}
					equal, err := child.StringEqual(r.ctx, ref)
					matched = matched || equal
					return err
				})
				if err != nil || !matched {
					return err
				}
				header, err := owner.projectNode(r.ctx, node, names...)
				if err == nil {
					result = append(result, header)
				}
				return err
			})
		})
		return result, err
	}
	field, _ := metadataFieldByName(kind)
	typ := reflect.TypeFor[domain.Bootstrap]().Field(field.index).Type.Elem()
	expressions := make([]string, 0, len(names))
	for _, name := range names {
		field, _ := typ.FieldByName(name)
		expressions = append(expressions, r.store.jsonText("data", jsonFieldName(field)))
	}
	where := "workspace_key=? AND field=? AND " + r.store.metadataRecordKeyEquals("record_key", "?")
	args := []any{r.access.policy.workspace, kind, ref}
	if kind == "reviews" {
		where = "workspace_key=? AND field=? AND (" + r.store.metadataRecordKeyEquals(r.store.jsonText("data", "id"), "?") + " OR " + r.store.metadataRecordKeyEquals(r.store.jsonText("data", "slugId"), "?") + ")"
		args = append(args, ref)
	}
	if err := owner.preflight(r.ctx, expressions, where, args...); err != nil {
		return nil, err
	}
	rows, err := r.store.db.QueryContext(r.ctx, "SELECT "+strings.Join(expressions, ",")+" FROM workspace_metadata_records WHERE "+where+" ORDER BY collection_order,record_key", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		values, scans := make([]sql.NullString, len(names)), make([]any, len(names))
		for index := range values {
			scans[index] = &values[index]
		}
		if err := rows.Scan(scans...); err != nil {
			return nil, err
		}
		header := reflect.New(typ).Elem()
		for index, name := range names {
			if !values[index].Valid {
				continue
			}
			target := header.FieldByName(name)
			if target.Kind() == reflect.String {
				target.SetString(values[index].String)
				continue
			}
			raw := []byte(values[index].String)
			if target.Type() == reflect.TypeFor[time.Time]() {
				raw, _ = json.Marshal(values[index].String)
			}
			work, err := metadataAggregatePayloadJSONWork(r.ctx, raw)
			if err != nil {
				return nil, err
			}
			if err := owner.reserve(r.ctx, work); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, target.Addr().Interface()); err != nil {
				return nil, err
			}
		}
		if kind == "documents" {
			// This is the only canonical normalization that changes a
			// consumed permission field; dates and content are not needed.
			document := header.Interface().(domain.Document)
			if document.Creator.ID != "" && !slices.ContainsFunc(document.Permissions, func(permission domain.DocumentPermission) bool {
				return permission.SubjectType == "user" && permission.SubjectID == document.Creator.ID
			}) {
				document.Permissions = append(document.Permissions, domain.DocumentPermission{SubjectType: "user", SubjectID: document.Creator.ID, Role: "owner"})
			}
			header = reflect.ValueOf(document)
		}
		result = append(result, header)
	}
	return result, rows.Err()
}

func (r *NotificationVisibilityRead) referenceVisible(kind, ref string) (bool, error) {
	p := r.policy
	selected := func(id string) bool { return slices.Contains(p.key.TeamIDs, id) }
	if kind == "team" {
		return slices.ContainsFunc(p.metadata.Teams, func(team domain.Team) bool {
			return team.ID == ref && team.RetiredAt == nil && p.teams.Teams[team.ID] && selected(team.ID)
		}), nil
	}
	owner := &ResourceRead{store: r.store}
	defer owner.Close()
	headers, err := r.referenceHeaders(owner, kind+"s", ref)
	if err != nil {
		return false, err
	}
	visible := false
	for _, header := range headers {
		switch kind {
		case "project":
			item := header.Interface().(domain.Project)
			visible = visible || p.projectVisible(item) && slices.ContainsFunc(item.TeamIDs, p.teamAllowed)
		case "initiative":
			item := header.Interface().(domain.Initiative)
			data := domain.Bootstrap{}
			for _, id := range item.ProjectIDs {
				projects, err := r.referenceHeaders(owner, "projects", id)
				if err != nil {
					return false, err
				}
				for _, project := range projects {
					data.Projects = append(data.Projects, project.Interface().(domain.Project))
				}
			}
			visible = visible || p.initiativeReadable(data, item)
		case "document":
			item := header.Interface().(domain.Document)
			if err := owner.reserve(r.ctx, uint64(len(p.metadata.Teams))*uint64(reflect.TypeFor[domain.Team]().Size()+512)*4+8192); err != nil {
				return false, err
			}
			data := domain.Bootstrap{Workspace: p.metadata.Workspace, Viewer: p.metadata.Viewer, ViewerRole: p.metadata.ViewerRole, Teams: slices.Clone(p.metadata.Teams), TeamSettings: p.metadata.TeamSettings, Documents: []domain.Document{item}}
			filterBootstrapTeams(&data, p.teams, p.metadata.ViewerRole == "guest")
			visible = visible || len(data.Documents) > 0 && (slices.ContainsFunc(item.TeamIDs, selected) || p.key.Actor == "app" && len(item.TeamIDs) == 0 && slices.ContainsFunc(item.InheritedTeamIDs, selected))
		case "cycle":
			visible = visible || p.teamAllowed(header.Interface().(domain.Cycle).TeamID)
		case "review":
			item := header.Interface().(domain.CodeReview)
			if len(item.IssueIDs) == 0 {
				visible = true
				continue
			}
			ids, err := r.store.VisibleIssueRecordIDs(r.ctx, r.issueQuery, item.IssueIDs)
			if err != nil {
				return false, err
			}
			visible = visible || len(ids) > 0
		}
	}
	return visible, nil
}

func (r *NotificationVisibilityRead) visible(items []domain.Notification) ([]domain.Notification, error) {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.IssueID != "" {
			ids = append(ids, item.IssueID)
		}
	}
	issues := map[string]bool{}
	if len(ids) > 0 {
		var err error
		issues, err = r.store.VisibleIssueRecordIDs(r.ctx, r.issueQuery, ids)
		if err != nil {
			return nil, err
		}
	}
	result := items[:0]
	known, decisions := map[RecordKey]bool{}, map[RecordKey]bool{}
	for _, item := range items {
		visible := true
		kind, ref := "", ""
		switch {
		case item.IssueID != "":
			visible = issues[item.IssueID]
		case item.ProjectID != "":
			kind, ref = "project", item.ProjectID
		case item.ReviewID != "":
			kind, ref = "review", item.ReviewID
		default:
			switch item.SourceType {
			case "project", "initiative", "document", "cycle", "team":
				kind, ref = item.SourceType, item.SourceID
			}
		}
		if kind != "" {
			key := RecordKey{Kind: kind, ID: ref}
			if !known[key] {
				var err error
				decisions[key], err = r.referenceVisible(kind, ref)
				if err != nil {
					return nil, err
				}
				known[key] = true
			}
			visible = decisions[key]
		}
		if visible {
			result = append(result, item)
		}
	}
	return result, nil
}

// headers scans at most fifty small reference projections. Payloads (including
// generated Pulse summaries) never enter counts or hidden-row filtering.
func (r *NotificationVisibilityRead) headers(q NotificationQuery, id string) ([]domain.Notification, error) {
	s := r.store
	where := "workspace_key=? AND kind='notification' AND owner_id=?"
	args := []any{r.access.policy.workspace, r.access.PrincipalID}
	if id != "" {
		where += " AND id=?"
		args = append(args, id)
	} else {
		if !q.IncludeArchived {
			where += " AND " + s.jsonText("data", "archivedAt") + " IS NULL"
		}
		if !q.IncludeDeleted {
			where += " AND " + s.jsonText("data", "deletedAt") + " IS NULL"
		}
		if !q.IncludeSnoozed {
			where += " AND (next_attempt_at='' OR next_attempt_at<=?)"
			args = append(args, time.Now().UTC().Format(issueRecordTimestamp))
		}
		if q.Read != nil {
			where += " AND " + s.jsonText("data", "readAt")
			if *q.Read {
				where += " IS NOT NULL"
			} else {
				where += " IS NULL"
			}
		}
		if q.Cursor != "" {
			raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
			var cursor contentCursor
			if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Date == "" || cursor.ID == "" {
				return nil, ErrIssueQuery
			}
			where += " AND (created_at<? OR (created_at=? AND id<?))"
			args = append(args, cursor.Date, cursor.Date, cursor.ID)
		}
	}
	columns := []string{"id", "created_at"}
	for _, name := range []string{"recipientId", "issueId", "projectId", "reviewId", "sourceType", "sourceId"} {
		columns = append(columns, "COALESCE("+s.jsonText("data", name)+",'')")
	}
	size := s.scaleP21SW22VisibilityLength(columns...)
	selected := []string{size}
	for _, column := range columns {
		selected = append(selected, "CASE WHEN ("+size+")<=65536 THEN "+column+" ELSE NULL END")
	}
	rows, err := s.db.QueryContext(r.ctx, "SELECT "+strings.Join(selected, ",")+" FROM workspace_content_records WHERE "+where+" ORDER BY created_at DESC,id DESC LIMIT 50", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]domain.Notification, 0, 50)
	for rows.Next() {
		var item domain.Notification
		var size int
		var created string
		err := rows.Scan(&size, &item.ID, &created, &item.RecipientID, &item.IssueID, &item.ProjectID, &item.ReviewID, &item.SourceType, &item.SourceID)
		if size > 65536 {
			return nil, ErrMetadataWorkBudget
		}
		if err != nil {
			return nil, err
		}
		if item.RecipientID != r.access.PrincipalID {
			return nil, fmt.Errorf("invalid notification owner index")
		}
		item.CreatedAt, err = time.Parse(issueRecordTimestamp, created)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *NotificationVisibilityRead) unread(q NotificationQuery) (int, error) {
	q.Cursor, q.Read = "", new(bool)
	count := 0
	for {
		items, err := r.headers(q, "")
		if err != nil || len(items) == 0 {
			return count, err
		}
		q.Cursor = NotificationCursor(items[len(items)-1])
		more := len(items) == 50
		items, err = r.visible(items)
		if err != nil {
			return 0, err
		}
		count += len(items)
		if !more {
			return count, nil
		}
	}
}

func (r *NotificationVisibilityRead) readBody(id string) (domain.Notification, error) {
	var item domain.Notification
	expression := "json_remove(data,'$.pulseSummary','$.summaryReports')"
	switch r.store.dialect {
	case "postgres":
		expression = "(convert_from(data,'UTF8')::jsonb - 'pulseSummary' - 'summaryReports')::text"
	case "mysql":
		expression = "JSON_REMOVE(CONVERT(data USING utf8mb4),'$.pulseSummary','$.summaryReports')"
	}
	raw, err := r.boundedBody("workspace_content_records", expression, "kind='notification' AND id=? AND owner_id=?", []any{id, r.access.PrincipalID})
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return item, err
	}
	if item.RecipientID != r.access.PrincipalID {
		return item, fmt.Errorf("invalid notification owner index")
	}
	item.PulseSummary, item.SummaryReports = nil, nil
	if item.Actor.ID != "" {
		raw, err := r.boundedBody("workspace_metadata_records", "data", "field='users' AND record_key=?", []any{item.Actor.ID})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return item, err
		}
		if err == nil {
			var actor domain.User
			if err := json.Unmarshal(raw, &actor); err != nil {
				return item, err
			}
			if actor.ID == item.Actor.ID {
				item.Actor = actor
			}
		}
	}
	RedactUserTeams(&item.Actor, func(id string) bool { return slices.Contains(r.access.policy.key.TeamIDs, id) })
	return item, nil
}

func (r *NotificationVisibilityRead) boundedBody(table, expression, where string, args []any) ([]byte, error) {
	length := r.store.scaleP21SW22VisibilityLength(expression)
	var size int64
	var raw []byte
	bindings := append([]any{r.access.policy.workspace}, args...)
	if err := r.store.db.QueryRowContext(r.ctx, "SELECT "+length+" FROM "+table+" WHERE workspace_key=? AND "+where, bindings...).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 || size > (metadataWorkBytes-4096)/32 {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(r.ctx, uint64(size)*32+4096); err != nil {
		return nil, err
	}
	err := r.store.db.QueryRowContext(r.ctx, "SELECT "+expression+" FROM "+table+" WHERE workspace_key=? AND "+where, bindings...).Scan(&raw)
	if err != nil {
		return nil, err
	}
	work, err := metadataAggregatePayloadJSONWork(r.ctx, raw)
	if err == nil {
		err = r.owner.reserve(r.ctx, work)
	}
	return raw, err
}

func (r *NotificationVisibilityRead) Query(q NotificationQuery) (NotificationPage, error) {
	result := NotificationPage{Notifications: []domain.Notification{}}
	if r.used || r.tx == nil || q.Workspace != r.access.policy.workspace || q.UserID != r.access.PrincipalID {
		return result, ErrAuthForbidden
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	scan := q
	for {
		items, err := r.headers(scan, "")
		if err != nil {
			return NotificationPage{}, err
		}
		more := len(items) == 50
		if len(items) > 0 {
			scan.Cursor = NotificationCursor(items[len(items)-1])
		}
		items, err = r.visible(items)
		if err != nil {
			return NotificationPage{}, err
		}
		for _, header := range items {
			if len(result.Notifications) == limit {
				result.HasMore = true
				result.NextCursor = NotificationCursor(result.Notifications[limit-1])
				break
			}
			item, err := r.readBody(header.ID)
			if err != nil {
				return NotificationPage{}, err
			}
			result.Notifications = append(result.Notifications, item)
		}
		if result.HasMore || !more {
			break
		}
	}
	var err error
	result.UnreadCount, err = r.unread(q)
	if err == nil {
		err = r.finish()
	}
	if err != nil {
		return NotificationPage{}, err
	}
	return result, nil
}

func (r *NotificationVisibilityRead) State(source *NotificationState) (NotificationState, error) {
	result := NotificationState{}
	if r.used || r.tx == nil {
		return result, ErrMetadataRecordQuery
	}
	if source != nil && len(source.ChangedIDs) > NotificationStateMaxChangedIDs {
		return result, ErrMetadataRecordQuery
	}
	var err error
	result.Revision, err = r.store.NotificationStateRevision(r.ctx, r.access.policy.workspace, r.access.PrincipalID)
	if err == nil {
		result.UnreadCount, err = r.unread(NotificationQuery{})
	}
	if err == nil && source != nil {
		result.Invalidated = source.Invalidated || source.Revision != result.Revision
		if !result.Invalidated {
			items := make([]domain.Notification, 0, len(source.ChangedIDs))
			for _, id := range source.ChangedIDs {
				if id == "" {
					result.Invalidated = true
					continue
				}
				var selected []domain.Notification
				selected, err = r.headers(NotificationQuery{}, id)
				if err != nil {
					break
				}
				if len(selected) == 0 {
					result.Invalidated = true
				} else {
					items = append(items, selected[0])
				}
			}
			if err == nil && !result.Invalidated {
				items, err = r.visible(items)
				for _, item := range items {
					result.ChangedIDs = append(result.ChangedIDs, item.ID)
				}
			}
		}
	}
	if err == nil {
		err = r.finish()
	}
	if err != nil {
		return NotificationState{}, err
	}
	return result, nil
}
