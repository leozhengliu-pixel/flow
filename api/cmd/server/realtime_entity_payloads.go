package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// Realtime entity payloads let a client apply a document, project or
// initiative change locally instead of reloading the workspace metadata (tens
// of MiB in a large workspace) on every event. The stream's viewer gets the
// entity as their own bootstrap would show it:
//
//   - a document they may open, as stored (its favorite flag is per viewer and
//     left out), plus the document's comments for a comment event;
//   - a project or initiative they may see, with team, dependency, project and
//     parent links narrowed to the ones they may see, and no issue count (a
//     private team's backlog size);
//   - for an update event, the project's or initiative's updates as their
//     updates endpoint lists them;
//   - nothing else from the mutation input, which can name hidden teams.
//
// A change that moves a project or initiative between teams (or rewires its
// links) can change what the viewer sees beyond the entity: the payload says
// scopeChanged and the client reloads its metadata once. A document's access
// is the document's own, so a document moved out of a viewer's reach goes to
// that viewer as a bare removal.

// realtimeEntityPayloadKeys are the mutation input fields kept beside the
// entity: identifiers of the changed child, never other resources' links.
var realtimeEntityPayloadKeys = []string{"commentId", "parentId", "updateId", "projectId", "initiativeId", "milestoneId", "resourceId", "id", "deleted", "state"}

// realtimeScopeFields are the input fields of an update that change which
// resources the entity links to (and so who sees it, or what they see).
var realtimeScopeFields = map[string][]string{
	"project.updated":    {"teamIds", "initiatives", "dependencyIds", "dependencyRelations"},
	"initiative.updated": {"leadTeamId", "contributingTeamIds", "projectIds", "parentInitiativeIds"},
	"document.updated":   {"teamIds", "projectIds", "issueId"},
}

func realtimeScopeChanged(event domain.RealtimeEvent) bool {
	fields := realtimeScopeFields[event.Type]
	if len(fields) == 0 {
		return false
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(event.Payload, &payload) != nil {
		return false
	}
	return slices.ContainsFunc(fields, func(field string) bool { _, ok := payload[field]; return ok })
}

// realtimeEntityPayload builds {entity, kept input fields, extra}.
func realtimeEntityPayload(event domain.RealtimeEvent, entity any, extra map[string]any) json.RawMessage {
	var input map[string]json.RawMessage
	_ = json.Unmarshal(event.Payload, &input)
	payload := map[string]any{}
	for _, key := range realtimeEntityPayloadKeys {
		if value, ok := input[key]; ok {
			payload[key] = value
		}
	}
	for key, value := range extra {
		payload[key] = value
	}
	if entity != nil {
		payload["entity"] = entity
	}
	encoded, _ := json.Marshal(payload)
	return encoded
}

// bareRealtimeEvent keeps only the event's identity and the given payload.
func bareRealtimeEvent(event domain.RealtimeEvent, payload map[string]any) domain.RealtimeEvent {
	bare := domain.RealtimeEvent{ID: event.ID, Seq: event.Seq, Type: event.Type, AggregateID: event.AggregateID, ClientID: event.ClientID, ActorID: event.ActorID, CreatedAt: event.CreatedAt}
	if len(payload) > 0 {
		bare.Payload, _ = json.Marshal(payload)
	}
	return bare
}

func keepStrings(values []string, keep func(string) bool) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if keep(value) {
			kept = append(kept, value)
		}
	}
	return kept
}

// realtimeProjectView is a project as the viewer's paged bootstrap shows it.
// It copies the slices it narrows: the project belongs to the shared snapshot.
func realtimeProjectView(rules *pulseRules, viewer pulseViewer, project domain.Project) domain.Project {
	project.TeamIDs = keepStrings(project.TeamIDs, func(id string) bool { return viewer.Visible[id] })
	project.DependencyIDs = keepStrings(project.DependencyIDs, func(id string) bool { return rules.projectVisible(viewer, rules.projects[id]) })
	project.IssueCount = 0
	return project
}

// realtimeInitiativeView is an initiative as the viewer's bootstrap shows it
// (filterBootstrapTeams): links to hidden projects, teams and initiatives are
// removed and the parents come from the initiative relations.
func realtimeInitiativeView(rules *pulseRules, viewer pulseViewer, initiative domain.Initiative) domain.Initiative {
	initiative.ProjectIDs = keepStrings(initiative.ProjectIDs, func(id string) bool { return rules.projectVisible(viewer, rules.projects[id]) })
	initiative.ParentInitiativeIDs = keepStrings(rules.initiativeParents[initiative.ID], func(id string) bool {
		return rules.initiativeVisible(viewer, rules.initiatives[id])
	})
	if !viewer.Visible[initiative.LeadTeamID] {
		initiative.LeadTeamID = ""
	}
	initiative.ContributingTeamIDs = keepStrings(initiative.ContributingTeamIDs, func(id string) bool { return viewer.Visible[id] })
	// Per-viewer flags: the client keeps its own.
	initiative.Favorite, initiative.Subscribed = false, false
	return initiative
}

// pagedPulseSourceEvent authorizes and projects a project or initiative event
// for a paged client, whose request metadata carries no projects or
// initiatives: the aggregate's visibility is checked against the shared
// project/initiative snapshot with the viewer's teams, and the payload is
// replaced by the viewer's projection of the aggregate. decided is false for
// events of other kinds and for aggregates that are not a project or
// initiative (a relation, an update).
func (s *server) pagedPulseSourceEvent(r *http.Request, data domain.Bootstrap, event domain.RealtimeEvent) (projected domain.RealtimeEvent, visible, decided bool) {
	kind := ""
	switch {
	case strings.HasPrefix(event.Type, "project.") || strings.HasPrefix(event.Type, "project_update."):
		kind = "project"
	case strings.HasPrefix(event.Type, "initiative.") || strings.HasPrefix(event.Type, "initiative_update."):
		kind = "initiative"
	default:
		return event, false, false
	}
	if event.AggregateID == "" {
		return event, false, false
	}
	snapshot, ok := s.store.PulseSources(workspaceKey(r))
	if !ok {
		return event, false, true
	}
	// One index of the projects and initiatives per installed workspace copy, shared by every stream.
	rules := s.realtime.pulseRulesFor(workspaceKey(r), snapshot)
	role := data.ViewerRole
	if s.authDisabled && role == "" {
		role = "admin"
	}
	viewer := newPulseViewer(snapshot, data.Viewer.ID, role, data.TeamMembers, pulseRestrictTeams(r))
	scopeChanged := s.realtime.scopeChanged(event)
	extra := map[string]any{}
	if scopeChanged {
		extra["scopeChanged"] = true
	}
	if kind == "project" {
		project := rules.projects[event.AggregateID]
		if project == nil {
			if event.Type == "project.deleted" {
				// Gone: who could see it is no longer known. The bare signal
				// lets clients that hold it drop it.
				return bareRealtimeEvent(event, map[string]any{"deleted": true}), true, true
			}
			return event, false, false
		}
		if !rules.projectVisible(viewer, project) {
			if scopeChanged {
				// It may have just left this viewer's reach: they reload.
				return bareRealtimeEvent(event, extra), true, true
			}
			return event, false, true
		}
		view := realtimeProjectView(rules, viewer, *project)
		key := event.ID + "|" + strings.Join(view.TeamIDs, ",") + "|" + strings.Join(view.DependencyIDs, ",")
		event.Payload = s.realtime.cachedPayload(key, func() json.RawMessage {
			if strings.HasPrefix(event.Type, "project.update_") {
				// The project's updates as GET /api/projects/{id}/updates lists them.
				updates := append([]domain.ProjectUpdate{}, snapshot.ProjectUpdates[project.ID]...)
				for index := range updates {
					updates[index].Snapshot = nil
				}
				extra["updates"] = updates
			}
			return realtimeEntityPayload(event, view, extra)
		})
		return event, true, true
	}
	initiative := rules.initiatives[event.AggregateID]
	if initiative == nil {
		if event.Type == "initiative.deleted" {
			return bareRealtimeEvent(event, map[string]any{"deleted": true}), true, true
		}
		return event, false, true
	}
	if !rules.initiativeVisible(viewer, initiative) {
		if scopeChanged {
			return bareRealtimeEvent(event, extra), true, true
		}
		return event, false, true
	}
	view := realtimeInitiativeView(rules, viewer, *initiative)
	key := event.ID + "|" + view.LeadTeamID + "|" + strings.Join(view.ContributingTeamIDs, ",") + "|" + strings.Join(view.ProjectIDs, ",") + "|" + strings.Join(view.ParentInitiativeIDs, ",")
	event.Payload = s.realtime.cachedPayload(key, func() json.RawMessage {
		if strings.HasPrefix(event.Type, "initiative.update_") {
			updates := append([]domain.InitiativeUpdate{}, snapshot.InitiativeUpdates[initiative.ID]...)
			for index := range updates {
				updates[index].Snapshot = nil
			}
			extra["updates"] = updates
		}
		return realtimeEntityPayload(event, view, extra)
	})
	return event, true, true
}

// pagedDocumentEvent authorizes a document event against the document itself, since the realtime metadata carries no
// documents: the viewer gets it when they may open the document (a new one included, which no snapshot of theirs holds
// yet), with the stored document as its entity. A document that is gone (deleted or purged) sends only the bare signal
// (type and id) for clients to drop it; a document moved out of the viewer's reach sends them a bare removal. Drafts
// are their author's alone; a published draft changes the document for everyone who may open it.
func (s *server) pagedDocumentEvent(r *http.Request, data domain.Bootstrap, event domain.RealtimeEvent) (domain.RealtimeEvent, bool) {
	documentID := event.AggregateID
	if strings.HasPrefix(event.Type, "document.draft_") {
		author := s.authDisabled || event.ActorID != "" && event.ActorID == data.Viewer.ID
		if event.Type != "document.draft_published" {
			return event, author
		}
		var input struct {
			DocumentID string `json:"documentId"`
		}
		_ = json.Unmarshal(event.Payload, &input)
		if input.DocumentID == "" {
			return event, author
		}
		documentID = input.DocumentID
	}
	document, ok := s.store.DocumentByID(workspaceKey(r), documentID)
	if !ok {
		return bareRealtimeEvent(event, nil), true
	}
	scopeChanged := s.realtime.scopeChanged(event)
	if documentRole(s, data, document) == "none" {
		if scopeChanged {
			return bareRealtimeEvent(event, map[string]any{"documentId": document.ID, "removed": true}), true
		}
		return event, false
	}
	event.Payload = s.realtime.cachedPayload(event.ID+"|document", func() json.RawMessage {
		extra := map[string]any{"documentId": document.ID}
		if event.Type == "document.draft_published" {
			extra["draftId"] = event.AggregateID
		}
		if strings.HasPrefix(event.Type, "document.comment_") {
			// Comments follow the document's access: one read serves every viewer.
			comments, _, err := s.store.IssueContent(r.Context(), data.Workspace.URLKey, document.ID)
			if err == nil {
				if comments == nil {
					comments = []domain.Comment{}
				}
				extra["comments"] = comments
			}
		}
		document.Favorite = false
		return realtimeEntityPayload(event, document, extra)
	})
	return event, true
}

// realtimePayloadCache shares a projected payload between the streams of
// viewers who get the same projection of one event: a document edit sent to
// every member builds and encodes the document once instead of once per
// stream. Every stream handles an event at about the same moment, so the
// first one builds and the others wait for its result instead of all building
// at once. It is small and bounded; an evicted entry is just built again.
type realtimePayloadCache struct {
	mu      sync.Mutex
	entries map[string]*realtimePayloadEntry
	order   []string
	bytes   int
}

type realtimePayloadEntry struct {
	ready   chan struct{}
	payload json.RawMessage
}

const (
	realtimePayloadCacheEntries = 256
	realtimePayloadCacheBytes   = 32 << 20
)

// cachedPayload is load on the hub's cache; a server without a hub (tests) builds every time.
func (h *realtimeHub) cachedPayload(key string, build func() json.RawMessage) json.RawMessage {
	if h == nil {
		return build()
	}
	return h.payloads.load(key, build)
}

func (c *realtimePayloadCache) load(key string, build func() json.RawMessage) json.RawMessage {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		c.mu.Unlock()
		<-entry.ready
		return entry.payload
	}
	if c.entries == nil {
		c.entries = map[string]*realtimePayloadEntry{}
	}
	entry := &realtimePayloadEntry{ready: make(chan struct{})}
	c.entries[key] = entry
	c.order = append(c.order, key)
	c.mu.Unlock()
	var payload json.RawMessage
	defer func() {
		// Also on a panic in build: the waiters get nil and build for themselves next time.
		c.mu.Lock()
		entry.payload = payload
		if c.entries[key] == entry {
			if payload == nil {
				delete(c.entries, key)
			}
			c.bytes += len(payload)
		}
		for len(c.order) > realtimePayloadCacheEntries || c.bytes > realtimePayloadCacheBytes {
			oldest := c.order[0]
			c.order = c.order[1:]
			if evicted, ok := c.entries[oldest]; ok {
				c.bytes -= len(evicted.payload)
				delete(c.entries, oldest)
			}
		}
		c.mu.Unlock()
		close(entry.ready)
	}()
	payload = build()
	return payload
}
