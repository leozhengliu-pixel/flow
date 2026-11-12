package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"flow/api/internal/domain"
)

// AgentConversationRecord reads one owner's transcript, including offloaded
// replies when requested. Coding sessions, worker cursors, outcomes and Loop
// progress retain their dedicated persistence and recovery protocols.
func (s *SQLiteStore) AgentConversationRecord(ctx context.Context, access AccessContext, ref string, bodies bool) (domain.AgentSession, error) {
	return s.agentConversationRecord(ctx, access, ref, bodies, false)
}

// AgentConversationByID keeps worker recovery's exact-ID and deleted-session
// semantics separate from a browser's active slug lookup.
func (s *SQLiteStore) AgentConversationByID(ctx context.Context, access AccessContext, id string, bodies bool) (domain.AgentSession, error) {
	return s.agentConversationRecord(ctx, access, id, bodies, true)
}

func (s *SQLiteStore) agentConversationRecord(ctx context.Context, access AccessContext, ref string, bodies, exact bool) (domain.AgentSession, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.AgentSession{}, err
	}
	p := access.policy
	where := s.metadataRecordKeyEquals("record_key", "?")
	args := []any{p.workspace, ref}
	if !exact {
		where = "(" + where + " OR " + s.jsonText("data", "slugId") + "=?) AND " + s.jsonText("data", "deletedAt") + " IS NULL"
		args = append(args, ref)
	}
	args = append(args, access.PrincipalID)
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='agentSessions' AND `+where+` AND `+s.jsonText("data", "userId")+`=? ORDER BY collection_order,record_key LIMIT 1`, args...).Scan(&raw)
	if err != nil {
		return domain.AgentSession{}, err
	}
	var session domain.AgentSession
	if err := json.Unmarshal(raw, &session); err != nil {
		return domain.AgentSession{}, err
	}
	if session.UserID != access.PrincipalID || exact && session.ID != ref || !exact && (session.DeletedAt != nil || session.ID != ref && session.SlugID != ref) {
		return domain.AgentSession{}, sql.ErrNoRows
	}
	if bodies {
		session, err = s.LoadAgentSessionBodies(ctx, p.workspace, session)
		if err != nil {
			return domain.AgentSession{}, err
		}
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.AgentSession{}, err
	}
	return session, nil
}

// AIConversationRecord keeps a long context local to the requested record.
// A workspace admin cannot read another member's private conversation.
func (s *SQLiteStore) AIConversationRecord(ctx context.Context, access AccessContext, id string) (domain.AIConversation, error) {
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.AIConversation{}, err
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='aiConversations' AND `+s.metadataRecordKeyEquals("record_key", "?")+` AND `+s.jsonText("data", "userId")+`=?`, access.policy.workspace, id, access.PrincipalID).Scan(&raw)
	if err != nil {
		return domain.AIConversation{}, err
	}
	var conversation domain.AIConversation
	if err := json.Unmarshal(raw, &conversation); err != nil {
		return domain.AIConversation{}, err
	}
	if err := s.ValidateAccess(ctx, access); err != nil {
		return domain.AIConversation{}, err
	}
	return conversation, nil
}
