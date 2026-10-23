package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const presenceTTL = 45 * time.Second

type realtimeHub struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[string]map[uint64]chan domain.RealtimeEvent
	// history keeps a short per-workspace replay window for reconnecting SSE
	// clients. It is intentionally bounded: clients that fall outside the
	// window receive workspace.resync_required and fetch a fresh snapshot.
	history      map[string][]domain.RealtimeEvent
	historyBytes map[string]int
	historyTotal int
	presence     map[string]map[string]domain.Presence
	sockets      map[string]map[uint64]*realtimeSocketClient
	// node tags this instance's stream ids when several instances share
	// events (Redis coordination). Each instance has its own replay window, in
	// its own arrival order, so a cursor from another instance cannot be
	// resumed here: its ids are unknown and the client resyncs.
	node string
	// ordered delivers change-log events (Seq > 0) in Seq order. Several
	// instances commit to one workspace and a peer's event reaches this one
	// later than its own: without ordering a subscriber here could apply a
	// newer change first and keep the older state.
	ordered bool
	order   map[string]*realtimeSeqOrder
}

// realtimeSeqOrder is one workspace's delivery position in the change log.
type realtimeSeqOrder struct {
	last    int64
	pending map[int64]domain.RealtimeEvent
	timer   *time.Timer
}

// realtimeReorderWait bounds how long a change waits for an earlier one still
// on its way from a peer. A change that never comes (logged but not pushed)
// delays the next push by at most this; one that comes after a later change
// was delivered is replaced by a resync.
const realtimeReorderWait = 200 * time.Millisecond

func newRealtimeHub() *realtimeHub {
	return &realtimeHub{subscribers: map[string]map[uint64]chan domain.RealtimeEvent{}, history: map[string][]domain.RealtimeEvent{}, presence: map[string]map[string]domain.Presence{}, sockets: map[string]map[uint64]*realtimeSocketClient{}}
}

func (h *realtimeHub) subscribe(workspace string) (<-chan domain.RealtimeEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	if h.subscribers[workspace] == nil {
		h.subscribers[workspace] = map[uint64]chan domain.RealtimeEvent{}
	}
	channel := make(chan domain.RealtimeEvent, 64)
	h.subscribers[workspace][id] = channel
	return channel, func() {
		h.mu.Lock()
		if subscribers := h.subscribers[workspace]; subscribers != nil {
			delete(subscribers, id)
			if len(subscribers) == 0 {
				delete(h.subscribers, workspace)
			}
		}
		h.mu.Unlock()
	}
}

// subscribeSince subscribes to live events and replays events published after
// sinceID while holding the same lock used by publish. This closes the small
// reconnect race between registering a listener and receiving the first live
// event. A missing cursor means the replay window has rolled over, so callers
// can safely fall back to a full bootstrap.
func (h *realtimeHub) subscribeSince(workspace, sinceID string) (<-chan domain.RealtimeEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	if h.subscribers[workspace] == nil {
		h.subscribers[workspace] = map[uint64]chan domain.RealtimeEvent{}
	}
	history := h.history[workspace]
	index := -1
	if sinceID != "" {
		// A numeric cursor is a change-log seq (RealtimeEvent.Seq); anything
		// else is the event id older clients remember.
		seq, _ := strconv.ParseInt(sinceID, 10, 64)
		index = slices.IndexFunc(history, func(event domain.RealtimeEvent) bool {
			if seq > 0 {
				return event.Seq == seq
			}
			return event.ID == sinceID
		})
	}
	var replay []domain.RealtimeEvent
	if index >= 0 {
		replay = history[index+1:]
	}
	// Room for the whole replay plus the live headroom: the stream drains the
	// channel only after this returns, so a buffer smaller than the replay
	// window would turn every longer replay into a resync.
	channel := make(chan domain.RealtimeEvent, 64+len(replay))
	h.subscribers[workspace][id] = channel
	if sinceID != "" && index < 0 {
		channel <- domain.RealtimeEvent{ID: fmt.Sprintf("resync_%d", time.Now().UnixNano()), Type: "workspace.resync_required", CreatedAt: time.Now().UTC()}
	}
	for _, event := range replay {
		if !domain.RoutineCredentialEvent(event.Type) {
			channel <- event
		}
	}
	return channel, func() {
		h.mu.Lock()
		if subscribers := h.subscribers[workspace]; subscribers != nil {
			delete(subscribers, id)
			if len(subscribers) == 0 {
				delete(h.subscribers, workspace)
			}
		}
		h.mu.Unlock()
	}
}

func (h *realtimeHub) publish(workspace string, event domain.RealtimeEvent) {
	if domain.RoutineCredentialEvent(event.Type) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ordered && event.Seq > 0 {
		h.publishInOrderLocked(workspace, event)
		return
	}
	h.deliverLocked(workspace, event)
}

// publishInOrderLocked delivers a change-log event after every earlier one.
// Seqs are gap-free per workspace, so a gap means an earlier change is still
// on its way from a peer (or is logged but never pushed): later changes wait
// for it up to realtimeReorderWait. A change that arrives after a later one
// was delivered cannot be put back in order, so subscribers resync instead.
func (h *realtimeHub) publishInOrderLocked(workspace string, event domain.RealtimeEvent) {
	if h.order == nil {
		h.order = map[string]*realtimeSeqOrder{}
	}
	order := h.order[workspace]
	if order == nil {
		order = &realtimeSeqOrder{pending: map[int64]domain.RealtimeEvent{}}
		h.order[workspace] = order
	}
	switch {
	case order.last == 0 || event.Seq == order.last+1:
		order.last = event.Seq
		h.deliverLocked(workspace, event)
		h.releaseInOrderLocked(workspace, order, false)
	case event.Seq <= order.last:
		h.deliverLocked(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("resync_%d", time.Now().UnixNano()), Type: "workspace.resync_required", CreatedAt: event.CreatedAt})
	default:
		order.pending[event.Seq] = event
		if order.timer == nil {
			h.awaitGapLocked(workspace, order)
		}
	}
}

// awaitGapLocked gives the oldest gap realtimeReorderWait to fill before the
// events behind it go out without it.
func (h *realtimeHub) awaitGapLocked(workspace string, order *realtimeSeqOrder) {
	// The callback takes h.mu, held here, so timer is set before it reads it.
	var timer *time.Timer
	timer = time.AfterFunc(realtimeReorderWait, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if order.timer != timer {
			return // stopped after it fired: the gap was filled
		}
		order.timer = nil
		h.releaseInOrderLocked(workspace, order, true)
	})
	order.timer = timer
}

// releaseInOrderLocked delivers the waiting events that are next in line;
// skipGap first gives up on the oldest gap. A gap still open behind them gets
// a full wait of its own.
func (h *realtimeHub) releaseInOrderLocked(workspace string, order *realtimeSeqOrder, skipGap bool) {
	progressed := false
	for len(order.pending) > 0 {
		next, ok := order.pending[order.last+1]
		if !ok {
			if !skipGap {
				break
			}
			skipGap = false
			lowest := int64(0)
			for seq := range order.pending {
				if lowest == 0 || seq < lowest {
					lowest = seq
				}
			}
			next = order.pending[lowest]
		}
		delete(order.pending, next.Seq)
		order.last = next.Seq
		h.deliverLocked(workspace, next)
		progressed = true
	}
	if order.timer != nil && (progressed || len(order.pending) == 0) {
		order.timer.Stop()
		order.timer = nil
	}
	if len(order.pending) > 0 && order.timer == nil {
		h.awaitGapLocked(workspace, order)
	}
}

// resyncAll tells every workspace's subscribers, and every client resuming
// from before now, to reload: events were lost (this instance missed peer
// events while its Redis subscription was down). Waiting events go out first
// and delivery order starts over, since the missed seqs never arrive.
func (h *realtimeHub) resyncAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	workspaces := map[string]bool{}
	for workspace := range h.history {
		workspaces[workspace] = true
	}
	for workspace := range h.subscribers {
		workspaces[workspace] = true
	}
	for workspace, order := range h.order {
		for len(order.pending) > 0 {
			h.releaseInOrderLocked(workspace, order, true)
		}
		if order.timer != nil {
			order.timer.Stop()
			order.timer = nil
		}
		delete(h.order, workspace)
	}
	now := time.Now().UTC()
	for workspace := range workspaces {
		h.deliverLocked(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("resync_%d", now.UnixNano()), Type: "workspace.resync_required", CreatedAt: now})
	}
}

// deliverLocked appends an event to the replay window and fans it out.
func (h *realtimeHub) deliverLocked(workspace string, event domain.RealtimeEvent) {
	if len(event.Payload) > 256<<10 {
		event = domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", ClientID: event.ClientID, ActorID: event.ActorID, CreatedAt: event.CreatedAt}
	}
	history := append(h.history[workspace], event)
	if h.historyBytes == nil {
		h.historyBytes = map[string]int{}
	}
	previousBytes := h.historyBytes[workspace]
	bytes := previousBytes + len(event.Payload) + 256
	for len(history) > 256 || bytes > 8<<20 {
		bytes -= len(history[0].Payload) + 256
		history[0] = domain.RealtimeEvent{}
		history = history[1:]
	}
	h.history[workspace] = history
	h.historyBytes[workspace] = bytes
	h.historyTotal += bytes - previousBytes
	for h.historyTotal > 64<<20 {
		oldest := ""
		var date time.Time
		for key, items := range h.history {
			if len(items) > 0 && (oldest == "" || items[len(items)-1].CreatedAt.Before(date)) {
				oldest = key
				date = items[len(items)-1].CreatedAt
			}
		}
		if oldest == "" {
			break
		}
		h.historyTotal -= h.historyBytes[oldest]
		delete(h.history, oldest)
		delete(h.historyBytes, oldest)
	}
	for _, channel := range h.subscribers[workspace] {
		select {
		case channel <- event:
		default:
			for len(channel) > 0 {
				<-channel
			}
			select {
			case channel <- domain.RealtimeEvent{ID: event.ID, Type: "workspace.resync_required", CreatedAt: event.CreatedAt}:
			default:
			}
		}
	}
}

func (h *realtimeHub) updatePresence(workspace, clientID string, user domain.User, issueID, route string) []domain.Presence {
	return h.updatePresenceWithDocument(workspace, clientID, user, issueID, "", route)
}

func (h *realtimeHub) updatePresenceWithDocument(workspace, clientID string, user domain.User, issueID, documentID, route string) []domain.Presence {
	h.mu.Lock()
	if h.presence[workspace] == nil {
		h.presence[workspace] = map[string]domain.Presence{}
	}
	h.presence[workspace][clientID] = domain.Presence{ClientID: clientID, User: user, IssueID: issueID, DocumentID: documentID, Route: route, LastSeenAt: time.Now().UTC()}
	presence := h.presenceLocked(workspace, time.Now())
	h.mu.Unlock()
	h.publishPresence(workspace, presence)
	return presence
}

func (h *realtimeHub) removePresence(workspace, clientID string) []domain.Presence {
	h.mu.Lock()
	delete(h.presence[workspace], clientID)
	presence := h.presenceLocked(workspace, time.Now())
	h.mu.Unlock()
	h.publishPresence(workspace, presence)
	return presence
}

func (h *realtimeHub) snapshotPresence(workspace string) []domain.Presence {
	h.mu.Lock()
	presence := h.presenceLocked(workspace, time.Now())
	h.mu.Unlock()
	return presence
}

func (h *realtimeHub) cleanupPresence(workspace string) {
	h.mu.Lock()
	before := len(h.presence[workspace])
	presence := h.presenceLocked(workspace, time.Now())
	changed := before != len(presence)
	h.mu.Unlock()
	if changed {
		h.publishPresence(workspace, presence)
	}
}

func (h *realtimeHub) presenceLocked(workspace string, now time.Time) []domain.Presence {
	for clientID, item := range h.presence[workspace] {
		if now.Sub(item.LastSeenAt) > presenceTTL {
			delete(h.presence[workspace], clientID)
		}
	}
	result := make([]domain.Presence, 0, len(h.presence[workspace]))
	for _, item := range h.presence[workspace] {
		result = append(result, item)
	}
	slices.SortFunc(result, func(left, right domain.Presence) int { return left.LastSeenAt.Compare(right.LastSeenAt) })
	return result
}

func (h *realtimeHub) publishPresence(workspace string, presence []domain.Presence) {
	payload, _ := json.Marshal(map[string]any{"presence": presence})
	h.publish(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("presence_%d", time.Now().UnixNano()), Type: "presence.updated", Payload: payload, CreatedAt: time.Now().UTC()})
}

func (s *server) realtimeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusNotImplemented, "Streaming is unavailable")
		return
	}
	workspace := workspaceKey(r)
	metadata, _, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	presence, err := s.snapshotPresence(r.Context(), workspace)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	since := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if since == "" {
		since = strings.TrimSpace(r.URL.Query().Get("since"))
	}
	// The handshake marker is not part of the replay ring. Treat it as an
	// initial connection rather than forcing a needless resync on reload.
	if strings.HasPrefix(since, "connected_") {
		since = ""
	}
	since = s.realtime.localCursor(since)
	// Read the head before subscribing: events committed in between are then
	// delivered live too, so a client never skips one.
	head, _ := s.store.WorkspaceChangeHead(r.Context(), workspace)
	// A numeric cursor is a change-log seq: replay from the durable log, which
	// outlives the in-memory ring (restarts, eviction). Subscribe first so
	// nothing committed during the replay is missed; live copies at or below
	// the replayed cursor are dropped as duplicates.
	replaySeq, numeric := strconv.ParseInt(since, 10, 64)
	durable := numeric == nil && replaySeq >= 0
	var channel <-chan domain.RealtimeEvent
	var unsubscribe func()
	if durable {
		channel, unsubscribe = s.realtime.subscribe(workspace)
	} else {
		channel, unsubscribe = s.realtime.subscribeSince(workspace, since)
	}
	defer unsubscribe()
	presence, err = s.pagedPresence(r, presence)
	if err != nil {
		issueRecordsError(w, err)
		return
	}
	// cursor is the workspace's change-log head when the stream opened. A
	// client advances its own cursor only from event.seq of events it applied.
	initial, _ := json.Marshal(map[string]any{"presence": presence, "cursor": head})
	if !writeSSE(w, domain.RealtimeEvent{ID: fmt.Sprintf("connected_%d", time.Now().UnixNano()), Type: "connected", Payload: initial, CreatedAt: time.Now().UTC()}, "") {
		return
	}
	flusher.Flush()
	if durable {
		cursor, ok := s.replayChanges(w, r, metadata.Workspace.ID, replaySeq, flusher)
		if !ok {
			return
		}
		replaySeq = cursor
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-channel:
			if durable && event.Seq > 0 && event.Seq <= replaySeq {
				continue
			}
			projected, visible, err := s.pagedRealtimeEvent(r, event)
			if err != nil {
				return
			}
			if visible {
				if !writeSSE(w, projected, s.realtime.node) {
					return
				}
				flusher.Flush()
			}
		case <-ticker.C:
			if s.coordinator != nil {
				presence, changed, err := s.coordinator.CleanupPresence(r.Context(), workspace, presenceTTL)
				if err == nil && changed {
					s.publishPresence(workspace, presence)
				}
			} else {
				s.realtime.cleanupPresence(workspace)
			}
			// A named event (not a comment): browsers expose it to the page,
			// which reconnects a stream that stops sending it (a proxy can keep
			// a dead upstream's connection open). It has no id, so it never
			// moves the resume cursor, and onmessage ignores it.
			if _, err := fmt.Fprint(w, "event: keepalive\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *server) documentPresenceAllowed(r *http.Request, documentID string) bool {
	data, _, err := s.pagedRealtimeMetadata(r)
	if err != nil {
		return false
	}
	document, ok := s.store.DocumentByID(workspaceKey(r), documentID)
	if !ok {
		return false
	}
	return documentRole(s, data, document) != "none"
}

func filterPresenceForViewer(data domain.Bootstrap, values []domain.Presence) []domain.Presence {
	return slices.DeleteFunc(slices.Clone(values), func(item domain.Presence) bool {
		if item.IssueID != "" && !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == item.IssueID }) {
			return true
		}
		if item.DocumentID != "" && len(data.Documents) > 0 && !slices.ContainsFunc(data.Documents, func(document domain.Document) bool {
			return document.ID == item.DocumentID || document.SlugID == item.DocumentID
		}) {
			return true
		}
		return false
	})
}

func realtimeEventVisible(data domain.Bootstrap, event domain.RealtimeEvent) bool {
	if domain.RoutineCredentialEvent(event.Type) {
		return false
	}
	if strings.HasPrefix(event.Type, "agent.") {
		return event.ActorID != "" && event.ActorID == data.Viewer.ID
	}
	if event.Type == "user_settings.updated" {
		return event.AggregateID == data.Viewer.ID && data.Viewer.ID != ""
	}
	if event.Type == "integration.job_updated" {
		return event.ActorID != "" && event.ActorID == data.Viewer.ID
	}
	if event.Type == "application_policy.updated" {
		if workspaceAdminRole(data.ViewerRole) || event.ActorID != "" && event.ActorID == data.Viewer.ID {
			return true
		}
		for _, policy := range applicationPolicies(&data) {
			if policy.ID == event.AggregateID {
				return policy.Shared || policy.OwnerID == data.Viewer.ID
			}
		}
		return false
	}
	if event.Type == "connected" || event.Type == "presence.updated" || strings.HasPrefix(event.Type, "workspace.") {
		return true
	}
	id := event.AggregateID
	if id == "" {
		return true
	}
	if event.Type == "agent_task.replied" {
		// An agent replied in an issue thread (its Inbox notifications changed).
		return slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == id })
	}
	if strings.HasPrefix(event.Type, "issue.") || strings.HasPrefix(event.Type, "comment.") || strings.HasPrefix(event.Type, "attachment.") {
		visible := slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == id }) || slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == id })
		return visible || strings.HasSuffix(event.Type, ".created") && realtimeEntityVisible(data, event)
	}
	if strings.HasPrefix(event.Type, "project.") || strings.HasPrefix(event.Type, "project_update.") || strings.HasPrefix(event.Type, "release.") {
		visible := slices.ContainsFunc(data.Projects, func(project domain.Project) bool { return project.ID == id }) || slices.ContainsFunc(data.Releases, func(release domain.Release) bool { return release.ID == id })
		return visible || strings.HasSuffix(event.Type, ".created") && realtimeEntityVisible(data, event)
	}
	if strings.HasPrefix(event.Type, "initiative.") || strings.HasPrefix(event.Type, "initiative_update.") {
		return slices.ContainsFunc(data.Initiatives, func(initiative domain.Initiative) bool { return initiative.ID == id })
	}
	if strings.HasPrefix(event.Type, "document.") {
		return slices.ContainsFunc(data.Documents, func(document domain.Document) bool { return document.ID == id || document.SlugID == id })
	}
	if strings.HasPrefix(event.Type, "cycle.") {
		return slices.ContainsFunc(data.Cycles, func(cycle domain.Cycle) bool { return cycle.ID == id })
	}
	if strings.HasPrefix(event.Type, "customer_request.") {
		return slices.ContainsFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == id })
	}
	if strings.HasPrefix(event.Type, "ask.") {
		return slices.ContainsFunc(data.Asks, func(item domain.Ask) bool { return item.ID == id })
	}
	return true
}

// realtimeEntityVisible authorizes create events whose aggregate is not in the
// viewer's snapshot yet. The entity is supplied only on the transient realtime
// envelope, so this does not alter persisted event payloads.
func realtimeEntityVisible(data domain.Bootstrap, event domain.RealtimeEvent) bool {
	var payload struct {
		Entity struct {
			Team struct {
				ID string `json:"id"`
			} `json:"team"`
			TeamID  string   `json:"teamId"`
			TeamIDs []string `json:"teamIds"`
		} `json:"entity"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil {
		return false
	}
	teamIDs := append([]string{}, payload.Entity.TeamIDs...)
	if payload.Entity.Team.ID != "" {
		teamIDs = append(teamIDs, payload.Entity.Team.ID)
	}
	if payload.Entity.TeamID != "" {
		teamIDs = append(teamIDs, payload.Entity.TeamID)
	}
	if len(teamIDs) == 0 {
		return false
	}
	return slices.ContainsFunc(teamIDs, func(id string) bool { return teamContentVisible(data, id) })
}

// writeSSE writes one event. With a node tag (several instances), the stream
// id is "<event id>~<node>": clients resume with it, and only this instance
// can resume it (see realtimeHub.node).
func writeSSE(w http.ResponseWriter, event domain.RealtimeEvent, node string) bool {
	raw, err := json.Marshal(event)
	if err != nil {
		return false
	}
	id := event.ID
	if node != "" && !strings.HasPrefix(id, "connected_") {
		id += "~" + node
	}
	_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", id, raw)
	return err == nil
}

// localCursor returns the event id of a stream cursor this instance wrote. A
// cursor tagged by another instance, or by any instance when this one does
// not tag, is returned unchanged: no event has that id, so the stream answers
// with a resync.
func (h *realtimeHub) localCursor(since string) string {
	id, node, tagged := strings.Cut(since, "~")
	if tagged && h.node != "" && node == h.node {
		return id
	}
	return since
}

func (s *server) updatePresence(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ClientID   string `json:"clientId"`
		IssueID    string `json:"issueId"`
		DocumentID string `json:"documentId"`
		Route      string `json:"route"`
		Active     *bool  `json:"active"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ClientID, input.IssueID, input.DocumentID, input.Route = strings.TrimSpace(input.ClientID), strings.TrimSpace(input.IssueID), strings.TrimSpace(input.DocumentID), strings.TrimSpace(input.Route)
	if input.ClientID == "" || len(input.ClientID) > 128 || len(input.Route) > 500 {
		writeError(w, http.StatusBadRequest, "clientId is required")
		return
	}
	if input.Active != nil && !*input.Active {
		workspace := workspaceKey(r)
		if s.coordinator != nil {
			presence, err := s.coordinator.RemovePresence(r.Context(), workspace, input.ClientID, presenceTTL)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
				return
			}
			s.publishPresence(workspace, presence)
			s.writePresenceResponse(w, r, presence)
			return
		}
		s.writePresenceResponse(w, r, s.realtime.removePresence(workspace, input.ClientID))
		return
	}
	if input.IssueID != "" {
		visible := false
		if pagedRealtimeRequest(r) {
			visible = s.pagedIssueVisible(r, input.IssueID)
		} else {
			data := s.workspaceData(r)
			visible = slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == input.IssueID })
		}
		if !visible {
			writeError(w, http.StatusForbidden, "Issue is outside your teams")
			return
		}
	}
	if input.DocumentID != "" {
		if !s.documentPresenceAllowed(r, input.DocumentID) {
			writeError(w, http.StatusForbidden, "Document is outside your permissions")
			return
		}
	}
	workspace := workspaceKey(r)
	actor := requestActor(s, r)
	if s.coordinator != nil {
		presence, err := s.coordinator.UpdatePresence(r.Context(), workspace, input.ClientID, domain.Presence{ClientID: input.ClientID, User: actor, IssueID: input.IssueID, DocumentID: input.DocumentID, Route: input.Route, LastSeenAt: time.Now().UTC()}, presenceTTL)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
			return
		}
		s.publishPresence(workspace, presence)
		s.writePresenceResponse(w, r, presence)
		return
	}
	s.writePresenceResponse(w, r, s.realtime.updatePresenceWithDocument(workspace, input.ClientID, actor, input.IssueID, input.DocumentID, input.Route))
}

func (s *server) listPresence(w http.ResponseWriter, r *http.Request) {
	workspace := workspaceKey(r)
	presence, err := s.snapshotPresence(r.Context(), workspace)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Presence is temporarily unavailable")
		return
	}
	issueID := strings.TrimSpace(r.URL.Query().Get("issueId"))
	documentID := strings.TrimSpace(r.URL.Query().Get("documentId"))
	route := strings.TrimSpace(r.URL.Query().Get("route"))
	if issueID != "" {
		visible := false
		if pagedRealtimeRequest(r) {
			visible = s.pagedIssueVisible(r, issueID)
		} else {
			data := s.workspaceData(r)
			visible = slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == issueID })
		}
		if !visible {
			writeError(w, http.StatusForbidden, "Issue is outside your teams")
			return
		}
	}
	if documentID != "" {
		if !s.documentPresenceAllowed(r, documentID) {
			writeError(w, http.StatusForbidden, "Document is outside your permissions")
			return
		}
	}
	if issueID != "" || documentID != "" || route != "" {
		presence = slices.DeleteFunc(presence, func(item domain.Presence) bool {
			return issueID != "" && item.IssueID != issueID || documentID != "" && item.DocumentID != documentID || route != "" && item.Route != route
		})
	}
	s.writePresenceResponse(w, r, presence)
}

func (s *server) snapshotPresence(ctx context.Context, workspace string) ([]domain.Presence, error) {
	if s.coordinator != nil {
		return s.coordinator.Presence(ctx, workspace, presenceTTL)
	}
	return s.realtime.snapshotPresence(workspace), nil
}

func (s *server) publishPresence(workspace string, presence []domain.Presence) {
	payload, _ := json.Marshal(map[string]any{"presence": presence})
	s.publishRealtime(workspace, domain.RealtimeEvent{ID: fmt.Sprintf("presence_%d", time.Now().UnixNano()), Type: "presence.updated", Payload: payload, CreatedAt: time.Now().UTC()})
}
