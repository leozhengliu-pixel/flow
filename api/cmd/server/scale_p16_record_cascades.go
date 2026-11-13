package main

import (
	"slices"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func projectRecordCascades(kind, operation, id string, input any) store.ProjectRecordCascadePlan {
	return func(source, before, next *domain.Bootstrap) []store.ProjectRecordCascade {
		var pages []store.ProjectRecordCascade
		links := func() *domain.TrashLinks {
			if len(next.Trash) == 0 {
				return nil
			}
			if next.Trash[0].Links == nil {
				next.Trash[0].Links = &domain.TrashLinks{}
			}
			return next.Trash[0].Links
		}
		if operation == "edit" && kind == "initiative" {
			initiative, err := initiativeByID(next, id)
			if err != nil {
				return nil
			}
			pages = append(pages, store.ProjectRecordCascade{Field: "projects", Match: func(data *domain.Bootstrap, index int) bool {
				project := &data.Projects[index]
				return slices.Contains(project.Initiatives, id) || slices.Contains(initiative.ProjectIDs, project.ID)
			}, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
				syncInitiativeProjects(page, id, nil, initiative.ProjectIDs)
				return nil
			}})
		}
		if operation == "edit" && kind == "project" {
			value, ok := input.(domain.ProjectMutationInput)
			if !ok || value.TeamIDs == nil {
				return pages
			}
			updatedProject, err := fullProjectByID(next, id)
			if err != nil {
				return pages
			}
			pages = append(pages, store.ProjectRecordCascade{Field: "documents", Match: func(data *domain.Bootstrap, index int) bool {
				return slices.Contains(data.Documents[index].ProjectIDs, id)
			}, Apply: func(source *domain.Bootstrap, page *domain.Bootstrap) error {
				for index := range page.Documents {
					document := &page.Documents[index]
					policy := domain.Bootstrap{Teams: source.Teams}
					for _, project := range source.Projects {
						if !slices.Contains(document.ProjectIDs, project.ID) {
							continue
						}
						if project.ID == id {
							project = *updatedProject
						}
						policy.Projects = append(policy.Projects, domain.Project{ID: project.ID, TeamIDs: project.TeamIDs})
					}
					inheritDocumentTeams(&policy, document)
				}
				return nil
			}})
		}
		if operation != "delete" {
			return pages
		}
		if kind == "initiative" && len(before.Initiatives) > 0 {
			next.Trash[0].TeamIDs = trashTeamIDs(source, deletedInitiativePayload{Initiative: before.Initiatives[0]})
		}
		if kind == "project" {
			pages = append(pages, store.ProjectRecordCascade{Field: "projects", Match: func(data *domain.Bootstrap, index int) bool {
				return data.Projects[index].ID != id && slices.Contains(data.Projects[index].DependencyIDs, id)
			}, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
				for index := range page.Projects {
					project := &page.Projects[index]
					links().DependentProjectIDs = append(links().DependentProjectIDs, project.ID)
					project.DependencyIDs = removeString(project.DependencyIDs, id)
				}
				return nil
			}}, store.ProjectRecordCascade{Field: "issues", ProjectID: id, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
				for index := range page.Issues {
					issue := &page.Issues[index]
					link := domain.TrashProjectIssue{IssueID: issue.ID, Archived: issue.ArchivedAt == nil}
					if issue.ProjectMilestoneID != nil {
						link.MilestoneID = *issue.ProjectMilestoneID
					}
					links().ProjectIssues = append(links().ProjectIssues, link)
					issue.Project, issue.ProjectMilestoneID = nil, nil
					if issue.ArchivedAt == nil {
						now := time.Now().UTC()
						issue.ArchivedAt, issue.UpdatedAt = &now, now
						appendActivity(page, issue.ID, "issue.updated", next.Viewer, map[string]string{"archived": "true", "project": id})
					}
				}
				return nil
			}})
		} else {
			pages = append(pages, store.ProjectRecordCascade{Field: "projects", Match: func(data *domain.Bootstrap, index int) bool {
				return slices.Contains(data.Projects[index].Initiatives, id)
			}, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
				for index := range page.Projects {
					project := &page.Projects[index]
					links().ProjectIDs = append(links().ProjectIDs, project.ID)
					project.Initiatives = removeString(project.Initiatives, id)
				}
				return nil
			}}, store.ProjectRecordCascade{Field: "initiatives", Match: func(data *domain.Bootstrap, index int) bool {
				return data.Initiatives[index].ID != id && slices.Contains(data.Initiatives[index].ParentInitiativeIDs, id)
			}, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
				for index := range page.Initiatives {
					initiative := &page.Initiatives[index]
					links().ChildInitiativeIDs = append(links().ChildInitiativeIDs, initiative.ID)
					initiative.ParentInitiativeIDs = removeString(initiative.ParentInitiativeIDs, id)
				}
				return nil
			}})
		}
		pages = append(pages, store.ProjectRecordCascade{Field: kind + "Relations", Match: func(data *domain.Bootstrap, index int) bool {
			if kind == "project" {
				item := data.ProjectRelations[index]
				return item.ProjectID == id || item.RelatedProjectID == id
			}
			item := data.InitiativeRelations[index]
			return item.InitiativeID == id || item.RelatedInitiativeID == id
		}, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
			if kind == "project" {
				links().ProjectRelations = append(links().ProjectRelations, page.ProjectRelations...)
				page.ProjectRelations = []domain.ProjectRelation{}
			} else {
				links().InitiativeRelations = append(links().InitiativeRelations, page.InitiativeRelations...)
				page.InitiativeRelations = []domain.InitiativeRelation{}
			}
			return nil
		}})
		for _, field := range []string{"favorites", "subscriptions", "drafts"} {
			pages = append(pages, store.ProjectRecordCascade{Field: field, Match: func(data *domain.Bootstrap, index int) bool {
				switch field {
				case "favorites":
					item := data.Favorites[index]
					return item.ResourceType == kind && item.ResourceID == id
				case "subscriptions":
					item := data.Subscriptions[index]
					return item.ResourceType == kind && item.ResourceID == id
				default:
					return draftBelongsToResource(data.Drafts[index], kind, id)
				}
			}, Apply: func(_ *domain.Bootstrap, page *domain.Bootstrap) error {
				captureResourcePreferences(page, kind, id, links())
				removeResourcePreferences(page, kind, id)
				page.Drafts = slices.DeleteFunc(page.Drafts, func(item domain.Draft) bool { return draftBelongsToResource(item, kind, id) })
				return nil
			}})
		}
		_ = before
		return pages
	}
}
