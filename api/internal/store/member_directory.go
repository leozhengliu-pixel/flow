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
