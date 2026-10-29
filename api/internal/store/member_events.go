package store

import "context"

// Member status changes made through the membership tables (suspend,
// resume, remove, SCIM deprovisioning) commit no workspace event of their
// own, so open sessions kept offering a suspended or removed person in their
// pickers until a reload. PublishMemberStatus sends the realtime notice the
// clients refresh their member directory on.

// memberStatusEvents maps a membership status to its realtime event type.
var memberStatusEvents = map[string]string{
	"suspended": "workspace_member.suspended",
	"active":    "workspace_member.resumed",
	"removed":   "workspace_member.removed",
}

// PublishMemberStatus tells the workspace's open sessions that userID is now
// suspended, active again or removed. It only notifies: the change itself is
// already committed.
func (s *SQLiteStore) PublishMemberStatus(ctx context.Context, workspaceID, userID, status string) {
	eventType := memberStatusEvents[status]
	if eventType == "" || userID == "" {
		return
	}
	_, workspaceKey, ok := s.workspaceByID(workspaceID)
	if !ok {
		return
	}
	s.publishWorkspaceEvent(ctx, workspaceKey, eventType, userID, map[string]string{"userId": userID, "status": status})
}
