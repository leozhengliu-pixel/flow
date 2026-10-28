package store

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"flow/api/internal/domain"
)

// memberDirectoryMigrationVersion records the one-time backfill of members
// who joined before joining added them to the workspace's users.
const memberDirectoryMigrationVersion = 1008

// The workspace's users collection is the directory every write resolves a
// person against: an assignee, subscribers, a project lead or members,
// mentions, notification recipients and the display data issues carry.
// Memberships live in workspace_memberships, and a person used to enter the
// directory only when they first changed something themselves, so a member
// who had just accepted an invitation (or was provisioned by SSO or SCIM)
// could not be assigned, subscribed or notified.
//
// syncMemberDirectory adds the workspace's active members (all of them, or
// only userIDs) that the directory lacks. Members already in it are left as
// they are; profile edits reach it through their own cascade. Application
// users are kept by their installation, not here.
func (s *SQLiteStore) syncMemberDirectory(ctx context.Context, workspaceKey string, userIDs ...string) (int, error) {
	current, ok := s.storedWorkspace(workspaceKey)
	if !ok {
		return 0, nil
	}
	missing, err := s.membersMissingFromDirectory(ctx, current, userIDs)
	if err != nil || len(missing) == 0 {
		return 0, err
	}
	ids := make([]string, 0, len(missing))
	for _, user := range missing {
		ids = append(ids, user.ID)
	}
	ctx = WithMetadataFields(WithoutIssueRecordMutations(context.WithoutCancel(ctx)), "users")
	// The callback's users may already hold the joining member as the
	// request's actor (mutations add their actor), so it never reports
	// "no change": the write is what persists them.
	err = s.MutateWorkspace(ctx, workspaceKey, "workspace_member.joined", ids[0], map[string]any{"userIds": ids}, func(data *domain.Bootstrap) error {
		for _, user := range missing {
			if !slices.ContainsFunc(data.Users, func(item domain.User) bool { return item.ID == user.ID }) {
				data.Users = append(data.Users, user)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(missing), nil
}

// syncMemberDirectoryByID is syncMemberDirectory for flows that know the
// workspace by id. A failure is logged, not returned: the membership itself
// is committed, and the next join or startup repairs the directory.
func (s *SQLiteStore) syncMemberDirectoryByID(ctx context.Context, workspaceID string, userIDs ...string) {
	_, key, ok := s.workspaceByID(workspaceID)
	if !ok {
		return
	}
	s.syncMemberDirectoryLogged(ctx, key, userIDs...)
}

func (s *SQLiteStore) syncMemberDirectoryLogged(ctx context.Context, workspaceKey string, userIDs ...string) {
	if _, err := s.syncMemberDirectory(ctx, workspaceKey, userIDs...); err != nil {
		slog.Warn("add members to the workspace directory", "workspace", workspaceKey, "error", err)
	}
}

func (s *SQLiteStore) membersMissingFromDirectory(ctx context.Context, data domain.Bootstrap, userIDs []string) ([]domain.User, error) {
	known := make(map[string]bool, len(data.Users))
	for _, user := range data.Users {
		known[user.ID] = true
	}
	if len(userIDs) > 0 && !slices.ContainsFunc(userIDs, func(id string) bool { return !known[id] }) {
		// Every sign-in of an auto-provisioned account passes here.
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND status='active' AND role<>'app'`, data.Workspace.ID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if !known[id] && (len(userIDs) == 0 || slices.Contains(userIDs, id)) {
			ids = append(ids, id)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(ids)
	users := make([]domain.User, 0, len(ids))
	for _, id := range ids {
		user, err := s.authUserByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("member %s: %w", id, err)
		}
		if !user.App {
			users = append(users, user)
		}
	}
	return users, nil
}

// migrateMemberDirectory adds the members who joined before joining added
// them to the directory. It is idempotent: present members are skipped.
func (s *SQLiteStore) migrateMemberDirectory(ctx context.Context) error {
	total := 0
	for _, key := range s.WorkspaceKeys() {
		added, err := s.syncMemberDirectory(ctx, key)
		if err != nil {
			return fmt.Errorf("workspace %q: %w", key, err)
		}
		total += added
	}
	if total > 0 {
		slog.Info("added joined members to the workspace directory", "members", total)
	}
	return nil
}

// formerMembersQuery lists the workspace's former members: suspended
// memberships, and people who left or were removed (a departure without a
// membership; one who rejoined is a member again).
const formerMembersQuery = `SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND status<>'active'
	UNION SELECT d.user_id FROM workspace_departures d WHERE d.workspace_id=? AND NOT EXISTS (SELECT 1 FROM workspace_memberships m WHERE m.workspace_id=d.workspace_id AND m.user_id=d.user_id)`

// formerMemberLookup is Bootstrap.FormerMembers for one write. The users
// directory keeps people after they leave (history stays readable), so a
// write checks the people it newly references against the memberships, which
// are authoritative. It reads on the first call only, through q: the write's
// transaction when one is open (SQLite pools may hold one connection). A
// workspace without memberships (development without sign-in) keeps them in
// its snapshot, whose suspended members count. A read failure is logged and
// treated as "no former members": the write itself is not refused for it.
func (s *SQLiteStore) formerMemberLookup(ctx context.Context, q metadataReader, data *domain.Bootstrap) func() map[string]bool {
	workspaceID, members := data.Workspace.ID, data.Members
	var former map[string]bool
	return func() map[string]bool {
		if former == nil {
			var err error
			if former, err = readFormerMembers(ctx, q, workspaceID, members); err != nil {
				slog.Warn("read former workspace members", "workspace", workspaceID, "error", err)
			}
		}
		return former
	}
}

// FormerMembers is the workspace's former members (removed or suspended),
// read outside a write: request checks that resolve people against the
// viewer's directory, which leaves removed people out, use it to let the
// write answer with its own refusal.
func (s *SQLiteStore) FormerMembers(ctx context.Context, workspaceKey string) (map[string]bool, error) {
	data, ok := s.WorkspaceMetadataFields(workspaceKey, "workspace", "members")
	if !ok {
		return map[string]bool{}, nil
	}
	return readFormerMembers(ctx, s.db, data.Workspace.ID, data.Members)
}

// readFormerMembers reads the former members from the memberships; a
// workspace without any (development without sign-in) keeps its members in
// the snapshot, whose suspended ones count. It never returns nil.
func readFormerMembers(ctx context.Context, q metadataReader, workspaceID string, snapshot []domain.WorkspaceMember) (map[string]bool, error) {
	former := map[string]bool{}
	if workspaceID == "" {
		return former, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT 1 FROM workspace_memberships WHERE workspace_id=? LIMIT 1`, workspaceID)
	if err != nil {
		return former, err
	}
	hasMemberships := rows.Next()
	if err := rows.Close(); err != nil {
		return former, err
	}
	if !hasMemberships {
		for _, member := range snapshot {
			if member.Status == "suspended" {
				former[member.User.ID] = true
			}
		}
		return former, nil
	}
	rows, err = q.QueryContext(ctx, formerMembersQuery, workspaceID, workspaceID)
	if err != nil {
		return former, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return former, err
		}
		former[id] = true
	}
	return former, rows.Err()
}

// loadMemberAccess gives a signed-in write that may move issues to another
// team (see WithIssueRenumbering) the workspace's members and team
// memberships as the database holds them. Signed in, memberships live in
// workspace_memberships and team_memberships (the snapshot's copies are only
// kept up to date without sign-in), and a moved issue's notification goes only
// to the subscribers who can open it in its new team: that is decided on
// these. Callers read them before opening the write transaction (SQLite pools
// may hold one connection) and never persist them.
func (s *SQLiteStore) loadMemberAccess(ctx context.Context, data *domain.Bootstrap) error {
	if !issueRenumbering(ctx) {
		return nil
	}
	if _, ok := actorFromContext(ctx); !ok {
		return nil
	}
	members, err := s.ListMembers(ctx, data.Workspace.ID)
	if err != nil {
		return err
	}
	teamMembers, err := s.ListTeamMembers(ctx, data.Workspace.ID)
	if err != nil {
		return err
	}
	data.Members, data.TeamMembers = members, teamMembers
	return nil
}
