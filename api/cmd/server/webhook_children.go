package main

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"

	"flow/api/internal/domain"
)

// Project updates, project milestones and initiative updates live inside
// their parent aggregate, so a mutation's domain event names the project or
// initiative. Webhooks deliver the child itself, so handlers
// that create, edit or delete one name it in the event payload.

// webhookChild identifies the nested entity a mutation changed. Entity is set
// for deletions, where the entity no longer exists once the event is
// delivered; otherwise it is read from the committed state.
type webhookChild struct {
	ID     string `json:"id"`
	Entity any    `json:"entity,omitempty"`
}

// webhookChildKey is the reserved payload key that carries the webhookChild.
// It is removed from the version 1 webhook body, which keeps the plain
// mutation input as `data`.
const webhookChildKey = "webhookChild"

// childEventPayload is a mutation input plus the child it changed. Child may
// be filled in by the mutation (a created entity gets its id there); the
// payload is encoded after the mutation ran.
type childEventPayload struct {
	Input any
	Child *webhookChild
}

func (p childEventPayload) MarshalJSON() ([]byte, error) {
	fields := map[string]any{}
	if p.Input != nil {
		raw, err := json.Marshal(p.Input)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			fields = map[string]any{"input": p.Input}
		}
	}
	if p.Child != nil && p.Child.ID != "" {
		fields[webhookChildKey] = p.Child
	}
	return json.Marshal(fields)
}

// webhookChildOf reads the child marker from an event payload and returns the
// payload without it.
func webhookChildOf(payload json.RawMessage) (child webhookChild, found bool, stripped json.RawMessage) {
	var fields map[string]json.RawMessage
	if len(payload) == 0 || json.Unmarshal(payload, &fields) != nil {
		return child, false, payload
	}
	raw, ok := fields[webhookChildKey]
	if !ok {
		// Attachment events name the update they belong to.
		var id string
		if json.Unmarshal(fields["updateId"], &id) == nil && id != "" {
			return webhookChild{ID: id}, true, payload
		}
		return child, false, payload
	}
	delete(fields, webhookChildKey)
	stripped, _ = json.Marshal(fields)
	if json.Unmarshal(raw, &child) != nil || child.ID == "" {
		return webhookChild{}, false, stripped
	}
	return child, true, stripped
}

// webhookChildEntity returns the nested entity of a project_updates,
// milestones or initiative_updates event as JSON, and the page it lives on.
func (s *server) webhookChildEntity(ctx context.Context, workspace string, event domain.DomainEvent, resource string, child webhookChild, parent map[string]any, root string) (map[string]any, string) {
	var entity map[string]any
	toMap := func(value any) map[string]any {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var result map[string]any
		if json.Unmarshal(raw, &result) != nil {
			return nil
		}
		return result
	}
	switch {
	case child.Entity != nil:
		entity = toMap(child.Entity)
	case resource == "milestones":
		milestones, _ := parent["milestones"].([]any)
		for position, item := range milestones {
			if milestone, ok := item.(map[string]any); ok && milestone["id"] == child.ID {
				// A milestone's sort order is its position in the project.
				entity = map[string]any{}
				for key, value := range milestone {
					entity[key] = value
				}
				entity["sortOrder"] = position
			}
		}
	case resource == "project_updates" || resource == "initiative_updates":
		if feed, ok := s.store.PulseFeed(workspace); ok {
			if resource == "project_updates" {
				if index := slices.IndexFunc(feed.ProjectUpdates[event.AggregateID], func(update domain.ProjectUpdate) bool { return update.ID == child.ID }); index >= 0 {
					update := feed.ProjectUpdates[event.AggregateID][index]
					update.Snapshot = nil // stored for diffs only
					entity = toMap(update)
				}
			} else if index := slices.IndexFunc(feed.InitiativeUpdates[event.AggregateID], func(update domain.InitiativeUpdate) bool { return update.ID == child.ID }); index >= 0 {
				update := feed.InitiativeUpdates[event.AggregateID][index]
				update.Snapshot = nil
				entity = toMap(update)
			}
		}
	}
	if entity == nil {
		return nil, ""
	}
	if _, ok := entity["id"]; !ok {
		entity["id"] = child.ID
	}
	page := ""
	if slug, _ := parent["slugId"].(string); slug != "" && root != "" {
		switch resource {
		case "milestones":
			page = root + "/project/" + url.PathEscape(slug) + "/overview#" + url.PathEscape(child.ID)
		case "project_updates":
			page = root + "/project/" + url.PathEscape(slug) + "/activity#" + url.PathEscape(child.ID)
		case "initiative_updates":
			page = root + "/initiative/" + url.PathEscape(slug) + "/activity#" + url.PathEscape(child.ID)
		}
	}
	return entity, page
}

// milestoneURL is the page of a project milestone.
func milestoneURL(root string, project map[string]any, id string) string {
	slug, _ := project["slugId"].(string)
	if slug == "" || root == "" {
		return ""
	}
	return root + "/project/" + url.PathEscape(slug) + "/overview#" + url.PathEscape(id)
}

// describeMilestoneReorder turns the reordering of a project's milestones
// into one update event per milestone whose sort order (its position in the
// project) changed: data is the milestone and updatedFrom its
// previous sortOrder. Only current-format webhooks receive them; version 1
// webhooks keep the single project.milestones_reordered event.
func (s *server) describeMilestoneReorder(event domain.DomainEvent, base webhookEvent, parent map[string]any, root string) []webhookEvent {
	var previous struct {
		Milestones []struct {
			ID string `json:"id"`
		} `json:"milestones"`
	}
	if len(event.PreviousValues) == 0 || json.Unmarshal(event.PreviousValues, &previous) != nil {
		return nil
	}
	oldPosition := map[string]int{}
	for position, milestone := range previous.Milestones {
		oldPosition[milestone.ID] = position
	}
	milestones, _ := parent["milestones"].([]any)
	var events []webhookEvent
	for position, item := range milestones {
		milestone, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := milestone["id"].(string)
		before, existed := oldPosition[id]
		if !existed || before == position {
			continue
		}
		data := map[string]any{}
		for key, value := range milestone {
			data[key] = value
		}
		data["sortOrder"] = position
		raw, err := json.Marshal(data)
		if err != nil {
			continue
		}
		updated, _ := json.Marshal(map[string]any{"sortOrder": before})
		item := base
		item.legacy, item.version1Only, item.version2Only, item.noVersion1URL = "", false, true, false
		item.action, item.eventType, item.eventID = "update", "project.milestone_reordered", event.ID+":"+id
		item.data, item.updated, item.url = raw, updated, milestoneURL(root, parent, id)
		events = append(events, item)
	}
	return events
}
