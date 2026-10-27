package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Open collaboration sockets follow access changes.
//
// A socket is authorized when it joins a document. Every change that can take
// a joined document away from its member (team membership or privacy, the
// member's role or status, an issue's team or permissions, a document's
// permissions, a revoked session or key) re-checks the sockets it affects as
// soon as it is applied on this server — the writer's own change, or a peer
// server's after its reload — and a member who lost access leaves the
// document at once (document.revoked): no further edits reach them and theirs
// are refused. Account-level changes (sessions) reach every server through a
// Redis signal. A sweep and a check on stale update frames bound how long a
// change that raised no event keeps a member in.

const (
	// socketAccessSweepInterval re-checks every joined socket's session and
	// document access (session expiry raises no event).
	socketAccessSweepInterval = 30 * time.Second
	// accountAccessSignal is the Redis signal for account-level changes; it
	// travels under accountAccessWorkspace, which names no workspace.
	accountAccessSignal    = "collaboration.account_access"
	accountAccessWorkspace = "__accounts__"
)

// issueSocketAccess is a joined issue description's last access check.
type issueSocketAccess struct {
	teamID string
	// canEdit: the member may change the description (an editor); viewers
	// and commenters follow it read-only.
	canEdit   bool
	checkedAt time.Time
	// generation counts invalidations: a check that started before the
	// latest one may have read the old access and leaves the entry stale.
	generation uint64
}

// socketAccessScope says which joined sockets a re-check covers.
type socketAccessScope struct {
	// all: every joined document and every socket's credential.
	all bool
	// issues: issue id → the team the change left it in ("" unknown, always
	// re-check); "*" covers every joined issue description.
	issues map[string]string
	// documents: standalone documents by id or collaboration id; "*" all.
	documents map[string]bool
	// users: re-check these users' credentials and joined documents.
	users map[string]bool
}

func (scope *socketAccessScope) merge(other socketAccessScope) {
	scope.all = scope.all || other.all
	for id, team := range other.issues {
		if scope.issues == nil {
			scope.issues = map[string]string{}
		}
		if current, ok := scope.issues[id]; ok && current != team {
			team = ""
		}
		scope.issues[id] = team
	}
	for id := range other.documents {
		if scope.documents == nil {
			scope.documents = map[string]bool{}
		}
		scope.documents[id] = true
	}
	for id := range other.users {
		if scope.users == nil {
			scope.users = map[string]bool{}
		}
		scope.users[id] = true
	}
}

func (scope socketAccessScope) empty() bool {
	return !scope.all && len(scope.issues) == 0 && len(scope.documents) == 0 && len(scope.users) == 0
}

// socketAccessQueue coalesces re-checks per workspace; each workspace has at
// most one worker.
type socketAccessQueue struct {
	mu      sync.Mutex
	pending map[string]*socketAccessScope
	running map[string]bool
}

func (q *socketAccessQueue) push(workspace string, scope socketAccessScope, run func(string)) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending, q.running = map[string]*socketAccessScope{}, map[string]bool{}
	}
	if q.pending[workspace] == nil {
		q.pending[workspace] = &socketAccessScope{}
	}
	q.pending[workspace].merge(scope)
	start := !q.running[workspace]
	q.running[workspace] = true
	q.mu.Unlock()
	if start {
		go run(workspace)
	}
}

func (q *socketAccessQueue) take(workspace string) *socketAccessScope {
	q.mu.Lock()
	defer q.mu.Unlock()
	scope := q.pending[workspace]
	delete(q.pending, workspace)
	if scope == nil {
		delete(q.running, workspace)
	}
	return scope
}

// collaborationAccessScope maps an applied change to the joined sockets
// whose access it may have changed.
func collaborationAccessScope(event domain.RealtimeEvent) (socketAccessScope, bool) {
	kind := event.Type
	switch {
	case strings.HasPrefix(kind, "team.resource"):
		return socketAccessScope{}, false
	case strings.HasPrefix(kind, "team.") || strings.HasPrefix(kind, "team_member.") || strings.HasPrefix(kind, "workspace_member."),
		kind == "workspace.updated", kind == "workspace.settings_updated", kind == "workspace.deleted", kind == "workspace.resync_required",
		kind == "user.updated", strings.HasPrefix(kind, "account."), strings.HasPrefix(kind, "api_key."), strings.HasPrefix(kind, "oauth"),
		strings.HasPrefix(kind, "application."):
		return socketAccessScope{all: true}, true
	case kind == "issue.batch_updated", kind == "issues.deleted", kind == "issue.suggestion_accepted":
		// Batch updates and accepted team suggestions can move issues to
		// other teams without naming them in the aggregate.
		return socketAccessScope{issues: map[string]string{"*": ""}}, true
	case kind == "issue.updated":
		if event.AggregateID == "" {
			return socketAccessScope{issues: map[string]string{"*": ""}}, true
		}
		// Most updates leave the issue in its team; the joined sockets are
		// re-checked when its team changed (or the payload does not say).
		var payload struct {
			Entity *struct {
				Team struct {
					ID string `json:"id"`
				} `json:"team"`
			} `json:"entity"`
		}
		team := ""
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Entity != nil {
			team = payload.Entity.Team.ID
		}
		if issueUpdateChangedParent(event.Payload) {
			// Sub-issues inherit their parents' grants: re-parenting can
			// change who may read or edit the issue and its descendants.
			return socketAccessScope{issues: map[string]string{"*": ""}}, true
		}
		return socketAccessScope{issues: map[string]string{event.AggregateID: team}}, true
	case kind == "issue.relation_removed":
		// Removing a parent relation detaches the sub-issue from its parent's grants.
		return socketAccessScope{issues: map[string]string{"*": ""}}, true
	case kind == "issue.relation_added":
		var payload struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil && payload.Type != "parent_of" && payload.Type != "sub_issue_of" {
			return socketAccessScope{}, false
		}
		// A parent relation re-parents an issue.
		return socketAccessScope{issues: map[string]string{"*": ""}}, true
	case strings.HasPrefix(kind, "issue.permission"):
		// Sub-issues inherit their parents' permissions: a grant changed on
		// one issue can change who may read or edit its descendants.
		return socketAccessScope{issues: map[string]string{"*": ""}}, true
	case kind == "issue.deleted", kind == "issue.archived", kind == "issue.unshared":
		if event.AggregateID == "" {
			return socketAccessScope{issues: map[string]string{"*": ""}}, true
		}
		return socketAccessScope{issues: map[string]string{event.AggregateID: ""}}, true
	case strings.HasPrefix(kind, "document."):
		if event.AggregateID == "" {
			return socketAccessScope{documents: map[string]bool{"*": true}}, true
		}
		return socketAccessScope{documents: map[string]bool{event.AggregateID: true}}, true
	case kind == "project.updated", kind == "project.deleted", strings.HasPrefix(kind, "project_member"):
		// Project documents follow the project's teams and members.
		return socketAccessScope{documents: map[string]bool{"*": true}}, true
	}
	return socketAccessScope{}, false
}

// issueUpdateChangedParent reports whether an issue.updated payload changes
// the issue's parent: the update input names parentId (at the top level, as
// the records path stores it, or under changes/input).
func issueUpdateChangedParent(payload json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return false
	}
	if _, ok := fields["parentId"]; ok {
		return true
	}
	for _, key := range []string{"changes", "input"} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(fields[key], &nested) == nil {
			if _, ok := nested["parentId"]; ok {
				return true
			}
		}
	}
	return false
}

// queueSocketAccessCheck re-checks the joined sockets an applied change may
// affect (this server's own change, or a peer's once reloaded). The access
// they cached is invalidated first, before this returns: until the queued
// check has run, their next update frame is re-checked before it is applied.
func (s *server) queueSocketAccessCheck(workspace string, event domain.RealtimeEvent) {
	if s.realtime == nil || !s.realtime.hasSockets(workspace) {
		return
	}
	if scope, ok := collaborationAccessScope(event); ok {
		s.realtime.socketAccessWork(workspace, scope)
		s.socketAccess.push(workspace, scope, s.runSocketAccessChecks)
	}
}

// applyIssueAccessChange re-checks every joined issue description on this
// server now (issue grants are inherited by sub-issues), so a permission
// change is in force when its request returns: a downgraded member is
// read-only, or out, before the response. Peers apply it as the change's
// event reaches them (queueSocketAccessCheck).
func (s *server) applyIssueAccessChange(workspace string) {
	if s.realtime == nil || !s.realtime.hasSockets(workspace) {
		return
	}
	s.checkSocketAccess(workspace, socketAccessScope{issues: map[string]string{"*": ""}})
}

// queueAllSocketAccessChecks re-checks every joined socket on this server.
func (s *server) queueAllSocketAccessChecks() {
	if s.realtime == nil {
		return
	}
	for _, workspace := range s.realtime.socketWorkspaces() {
		s.socketAccess.push(workspace, socketAccessScope{all: true}, s.runSocketAccessChecks)
	}
}

// accountAccessChanged re-checks a user's sockets on every server after an
// account-level change (store.SetAccountAccessSink).
func (s *server) accountAccessChanged(userID string) {
	s.queueAccountSocketChecks(userID)
	if s.coordinator == nil {
		return
	}
	raw, _ := json.Marshal(map[string]string{"userId": userID})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	event := domain.RealtimeEvent{ID: newCollaborationID(), Type: accountAccessSignal, Payload: raw, CreatedAt: time.Now().UTC()}
	if err := s.coordinator.Publish(ctx, accountAccessWorkspace, event); err != nil {
		log.Printf("Redis publish account access change: %v", err)
	}
}

func (s *server) queueAccountSocketChecks(userID string) {
	if s.realtime == nil || userID == "" {
		return
	}
	for _, workspace := range s.realtime.socketWorkspaces() {
		s.socketAccess.push(workspace, socketAccessScope{users: map[string]bool{userID: true}}, s.runSocketAccessChecks)
	}
}

// receiveAccountAccessSignal handles a peer's accountAccessSignal.
func (s *server) receiveAccountAccessSignal(event domain.RealtimeEvent) {
	var payload struct {
		UserID string `json:"userId"`
	}
	if json.Unmarshal(event.Payload, &payload) == nil {
		s.queueAccountSocketChecks(payload.UserID)
	}
}

func (s *server) runSocketAccessChecks(workspace string) {
	for {
		scope := s.socketAccess.take(workspace)
		if scope == nil {
			return
		}
		if scope.empty() {
			continue
		}
		// A peer's change may not be applied here yet when the check runs
		// for another reason; read the committed state.
		s.ensureCurrentWorkspace(context.Background(), workspace)
		s.checkSocketAccess(workspace, *scope)
	}
}

type socketAccessTarget struct {
	documentID string
	issueID    string // "" for a standalone document
}

type socketAccessWork struct {
	client     *realtimeSocketClient
	credential bool
	targets    []socketAccessTarget
}

func (s *server) checkSocketAccess(workspace string, scope socketAccessScope) {
	hub := s.realtime
	for _, item := range hub.socketAccessWork(workspace, scope) {
		if item.credential && item.client.checkCredential != nil && !item.client.checkCredential() {
			hub.revokeSocketCredential(item.client)
			continue
		}
		for _, target := range item.targets {
			if target.issueID != "" {
				_, _ = s.recheckIssueDocument(item.client, target.documentID, target.issueID)
			} else {
				hub.recheckStandaloneDocument(item.client, target.documentID)
			}
		}
	}
}

// socketAccessWork lists the joined documents (and credentials) scope covers
// and marks their cached access stale, so an update frame on one of them is
// re-checked before it is applied until a check has read the new access.
func (h *realtimeHub) socketAccessWork(workspace string, scope socketAccessScope) []socketAccessWork {
	hub := h
	var work []socketAccessWork
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for _, client := range hub.sockets[workspace] {
		user := scope.users[client.userID]
		item := socketAccessWork{client: client, credential: scope.all || user}
		for documentID, issueID := range client.issueDocuments {
			if _, joined := client.documents[documentID]; !joined {
				continue
			}
			team, listed := scope.issues[issueID]
			_, every := scope.issues["*"]
			access := client.issueAccess[documentID]
			if scope.all || user || every || listed && (team == "" || access == nil || access.teamID != team) {
				if access != nil {
					access.checkedAt = time.Time{}
					access.generation++
				}
				item.targets = append(item.targets, socketAccessTarget{documentID: documentID, issueID: issueID})
			}
		}
		for collaborationID, access := range client.documentAccess {
			if scope.all || user || scope.documents["*"] || scope.documents[collaborationID] || scope.documents[access.documentID] {
				access.checkedAt = time.Time{}
				item.targets = append(item.targets, socketAccessTarget{documentID: collaborationID})
			}
		}
		if item.credential || len(item.targets) > 0 {
			work = append(work, item)
		}
	}
	return work
}

// issueDocumentAccessFresh reports whether a joined issue description was
// checked within documentAccessTTL.
func (s *server) issueDocumentAccessFresh(client *realtimeSocketClient, documentID string) bool {
	s.realtime.mu.Lock()
	defer s.realtime.mu.Unlock()
	access := client.issueAccess[documentID]
	return access != nil && time.Since(access.checkedAt) < documentAccessTTL
}

// recheckIssueDocument re-reads the socket user's access to a joined issue
// description and removes it from the socket when the user lost it. It
// reports whether the socket may still use the document.
func (s *server) recheckIssueDocument(client *realtimeSocketClient, documentID, issueID string) (bool, error) {
	if client.checkIssue == nil {
		return true, nil
	}
	s.realtime.mu.Lock()
	var generation uint64
	if access := client.issueAccess[documentID]; access != nil {
		generation = access.generation
	}
	s.realtime.mu.Unlock()
	issue, visible, canEdit, err := client.checkIssue(issueID)
	if err != nil && !errors.Is(err, store.ErrAuthForbidden) {
		return false, err
	}
	if !visible {
		s.realtime.revokeDocument(client, documentID, issueID)
		return false, nil
	}
	s.realtime.mu.Lock()
	if access := client.issueAccess[documentID]; access != nil {
		access.teamID = issue.Team.ID
		// Invalidated while this check ran: it may have read the access
		// before that change, so the next update frame checks again.
		if access.generation == generation {
			access.checkedAt = time.Now()
		}
	}
	s.realtime.mu.Unlock()
	s.realtime.setEditAccess(client, documentID, canEdit)
	return true, nil
}

// issueDocumentEditable reports whether a joined issue description accepts
// the socket's edits.
func (s *server) issueDocumentEditable(client *realtimeSocketClient, documentID string) bool {
	s.realtime.mu.Lock()
	defer s.realtime.mu.Unlock()
	access := client.issueAccess[documentID]
	return access != nil && access.canEdit
}

// setEditAccess records whether a socket may edit a joined document (an issue
// description or a standalone document) and, when that changed, tells its
// client (document.access) so its editor turns read-only, or editable again,
// without reconnecting.
func (h *realtimeHub) setEditAccess(client *realtimeSocketClient, documentID string, canEdit bool) {
	h.mu.Lock()
	changed, issueID := false, client.issueDocuments[documentID]
	if access := client.issueAccess[documentID]; access != nil && issueID != "" {
		changed = access.canEdit != canEdit
		access.canEdit = canEdit
	} else if access := client.documentAccess[documentID]; access != nil {
		changed = access.canEdit != canEdit
		access.canEdit, access.checkedAt = canEdit, time.Now()
	}
	_, joined := client.documents[documentID]
	h.mu.Unlock()
	if !changed || !joined {
		return
	}
	message := map[string]any{"type": "document.access", "documentId": documentID, "readOnly": !canEdit}
	if issueID != "" {
		message["issueId"] = issueID
	}
	sendSocketJSON(client, message)
}

// recheckStandaloneDocument re-reads the socket user's role on a joined
// standalone document and removes it when the user lost access.
func (h *realtimeHub) recheckStandaloneDocument(client *realtimeSocketClient, collaborationID string) {
	if client.checkDocument == nil {
		return
	}
	role, conflict, err := client.checkDocument(collaborationID)
	if err != nil && !errors.Is(err, errNotFound) && !errors.Is(err, store.ErrAuthForbidden) || conflict {
		return
	}
	if err == nil && role != "none" {
		// Still a reader: follow an upgrade or a downgrade of the role.
		h.setEditAccess(client, collaborationID, canEditDocument(role))
		return
	}
	h.revokeDocument(client, collaborationID, "")
}

// revokeDocument removes a document from a socket and tells its client.
func (h *realtimeHub) revokeDocument(client *realtimeSocketClient, documentID, issueID string) {
	h.mu.Lock()
	_, joined := client.documents[documentID]
	delete(client.documents, documentID)
	delete(client.issueDocuments, documentID)
	delete(client.issueAccess, documentID)
	delete(client.documentAccess, documentID)
	h.mu.Unlock()
	if !joined {
		return
	}
	message := map[string]any{"type": "document.revoked", "documentId": documentID}
	if issueID != "" {
		message["issueId"] = issueID
	}
	sendSocketJSON(client, message)
}

// revokeSocketCredential ends a socket whose session or key was revoked: its
// documents are removed (the client is told) and the socket closes.
func (h *realtimeHub) revokeSocketCredential(client *realtimeSocketClient) {
	h.mu.Lock()
	client.credentialRevoked = true
	joined := map[string]string{}
	for documentID := range client.documents {
		joined[documentID] = client.issueDocuments[documentID]
	}
	h.mu.Unlock()
	for documentID, issueID := range joined {
		h.revokeDocument(client, documentID, issueID)
	}
	// Leave the writer a moment to deliver the notices.
	time.AfterFunc(time.Second, client.cancel)
}

func (h *realtimeHub) hasSockets(workspace string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.sockets[workspace]) > 0
}

func (h *realtimeHub) socketWorkspaces() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	workspaces := make([]string, 0, len(h.sockets))
	for workspace := range h.sockets {
		workspaces = append(workspaces, workspace)
	}
	return workspaces
}

// startSocketAccessSweep re-checks every open socket periodically.
func (s *server) startSocketAccessSweep() {
	if !s.socketAccessSweepStarted.CompareAndSwap(false, true) {
		return
	}
	done := context.Background().Done()
	if s.coordinationContext != nil {
		done = s.coordinationContext.Done()
	}
	go func() {
		ticker := time.NewTicker(socketAccessSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				s.queueAllSocketAccessChecks()
			}
		}
	}()
}

// socketCredentialValid reports whether the session or API key a socket
// connected with is still valid for its user.
func (s *server) socketCredentialValid(r *http.Request) bool {
	if s.authDisabled {
		return true
	}
	user := authUser(r)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			return false
		}
		auth, err := s.store.AuthenticateAPIKeyRecord(ctx, workspaceKey(r), secretHash(strings.TrimSpace(header[len("Bearer "):])), applicationApproved)
		return err == nil && auth.Key.ID == key.ID && auth.User.ID == user.ID
	}
	cookie, err := r.Cookie(sessionCookieName)
	return err == nil && s.store.SessionValid(ctx, cookie.Value, user.ID)
}
