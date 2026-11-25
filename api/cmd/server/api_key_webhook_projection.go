package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"flow/api/internal/domain"
)

func webhookWithinKeyTeams(item domain.Webhook, key domain.APIKey) bool {
	return item.TeamRestriction == "selected" && len(item.TeamIDs) > 0 && !slices.ContainsFunc(item.TeamIDs, func(id string) bool { return !slices.Contains(key.TeamIDs, id) })
}

// Outbound subscriptions authorize their own snapshots. Reading a stored
// delivery through a narrower credential must project that snapshot again,
// including previous values, rather than return the subscription's authority.
func (s *server) projectAPIKeyWebhookDelivery(r *http.Request, delivery *domain.WebhookDelivery) error {
	key, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey)
	if !ok || !apiKeyTeamRestrictionSelected(key) {
		return nil
	}
	data, ok, err := s.store.BootstrapForUser(r.Context(), workspaceKey(r), authUser(r).ID)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound
	}
	partialProjects := map[string]bool{}
	for _, item := range data.Projects {
		partialProjects[item.ID] = slices.ContainsFunc(item.TeamIDs, func(id string) bool { return !slices.Contains(key.TeamIDs, id) })
	}
	filterBootstrapForAPIKey(&data, r)
	var envelope map[string]any
	if err := json.Unmarshal(delivery.Payload, &envelope); err != nil {
		return errNotFound
	}
	resource, _ := canonicalWebhookResourceType(delivery.EventType)
	kind := strings.TrimSuffix(resource, "s")
	partial := false
	if entity, ok := envelope["data"].(map[string]any); ok {
		id, _ := entity["projectId"].(string)
		if kind == "project" {
			id, _ = entity["id"].(string)
		}
		partial = partialProjects[id]
	}
	visible := func(kind, id string) bool { return id == "" || resourceExists(&data, kind, id) }
	if entity, ok := envelope["data"].(map[string]any); ok {
		if id, ok := entity["id"].(string); ok {
			allowed := visible(kind, id)
			switch kind {
			case "issue", "project", "initiative", "document", "cycle", "team", "label", "customer":
			default:
				raw, err := json.Marshal(data)
				if err != nil {
					return err
				}
				var root any
				if err := json.Unmarshal(raw, &root); err != nil {
					return err
				}
				_, allowed = webhookObjectByID(root, id)
			}
			if !allowed {
				return errNotFound
			}
		}
	}
	scalar := map[string]string{"teamId": "team", "leadTeamId": "team", "issueTeamId": "team", "projectId": "project", "relatedProjectId": "project", "initiativeId": "initiative", "relatedInitiativeId": "initiative", "documentId": "document", "cycleId": "cycle", "issueId": "issue", "relatedIssueId": "issue"}
	arrays := map[string]string{"teamIds": "team", "appTeamIds": "team", "contributingTeamIds": "team", "inheritedTeamIds": "team", "pinnedTeamIds": "team", "projectIds": "project", "projects": "project", "dependencyIds": "project", "initiatives": "initiative", "parentInitiativeIds": "initiative", "issueIds": "issue", "subIssueIds": "issue", "labelIds": "label", "customers": "customer", "suggestedProjectIds": "project", "suggestedTeamIds": "team", "suggestedDuplicateIds": "issue", "suggestedRelatedIds": "issue"}
	if kind == "issue" {
		scalar["parentId"] = "issue"
	}
	var project func(any)
	project = func(value any) {
		switch item := value.(type) {
		case []any:
			for _, child := range item {
				project(child)
			}
		case map[string]any:
			for field, value := range item {
				if partial && field == "progressSince" {
					delete(item, field)
					continue
				}
				if partial && field == "progress" {
					item[field] = 0
					continue
				}
				if target, ok := scalar[field]; ok {
					if id, ok := value.(string); ok && !visible(target, id) {
						delete(item, field)
						if field == "projectId" {
							delete(item, "projectMilestoneId")
						}
					}
					continue
				}
				if target, ok := arrays[field]; ok {
					outside := func(value any) bool {
						if id, ok := value.(string); ok {
							return !visible(target, id)
						}
						if ref, ok := value.(map[string]any); ok {
							if id, ok := ref["id"].(string); ok {
								return !visible(target, id)
							}
						}
						return false
					}
					if ids, ok := value.([]any); ok {
						item[field] = slices.DeleteFunc(ids, outside)
					}
					if change, ok := value.(map[string]any); ok {
						for _, direction := range []string{"added", "removed"} {
							if refs, ok := change[direction].([]any); ok {
								change[direction] = slices.DeleteFunc(refs, outside)
							}
						}
					}
					continue
				}
				if field == "project" || field == "cycle" {
					if child, ok := value.(map[string]any); ok {
						if id, ok := child["id"].(string); ok && !visible(field, id) {
							delete(item, field)
							delete(item, "projectMilestoneId")
							continue
						}
					}
				}
				if field == "relations" {
					if relations, ok := value.([]any); ok {
						item[field] = slices.DeleteFunc(relations, func(value any) bool {
							relation, _ := value.(map[string]any)
							for field, value := range relation {
								if target, ok := scalar[field]; ok {
									if id, ok := value.(string); ok && !visible(target, id) {
										return true
									}
								}
							}
							return false
						})
					}
				}
				if field == "issueCount" {
					item[field] = 0
					id, _ := item["id"].(string)
					for _, p := range data.Projects {
						if p.ID == id {
							item[field] = p.IssueCount
						}
					}
					for _, label := range data.Labels {
						if label.ID == id {
							item[field] = label.IssueCount
						}
					}
				}
				if strings.HasSuffix(field, "History") && kind == "project" && slices.Contains([]string{"issueCountHistory", "scopeHistory", "completedScopeHistory", "inProgressScopeHistory", "progressHistory"}, field) {
					delete(item, field)
					continue
				}
				project(value)
			}
		}
	}
	project(envelope)
	delivery.Payload, err = json.Marshal(envelope)
	return err
}
