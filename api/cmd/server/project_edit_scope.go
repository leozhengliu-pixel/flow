package main

import (
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// projectEditCallback is PATCH /api/projects/{id}'s workspace callback. Both
// writers run this one callback: the literal MutateWorkspace dispatch and,
// with bounded metadata on, the scoped metadata writer for the edits
// boundedProjectEditScope admits.
func projectEditCallback(id string, input domain.ProjectMutationInput, updated *domain.Project) func(*domain.Bootstrap) error {
	return func(data *domain.Bootstrap) error {
		project, err := fullProjectByID(data, id)
		if err != nil {
			return err
		}
		before := *project
		var relationsBefore []domain.ProjectRelation
		editsRelations := input.DependencyIDs != nil || input.DependencyRelations != nil
		if editsRelations {
			relationsBefore = projectRelationsOf(data, id)
		}
		if input.Description != nil && *input.Description != project.Description {
			project.DescriptionRevisions = append([]domain.ProjectDescriptionRevision{{ID: idgen.New("project_description_revision_"), ProjectID: id, Description: project.Description, Author: data.Viewer, CreatedAt: time.Now().UTC()}}, project.DescriptionRevisions...)
		}
		previous := projectTrackedProperties(*project)
		if err := applyProjectUpdate(data, project, input); err != nil {
			return err
		}
		if unchangedEdit(before, *project) && (!editsRelations || unchangedEdit(relationsBefore, projectRelationsOf(data, id))) {
			*updated = before
			return store.ErrNoMutation
		}
		project.UpdatedAt = time.Now().UTC()
		appendProjectPropertyAudit(data, previous, *project)
		*updated = *project
		return nil
	}
}

// boundedProjectEditScope routes a project edit with bounded metadata on. It
// answers the scoped writer's declared inputs and true for an edit whose
// callback reads and writes metadata records only: the project record, the
// workspace users, settings and project relations, and the statuses, labels,
// teams and dependency projects its input names. The portfolio image handles
// edits with further effects:
//
//   - updateSchedule: "never" archives the project's reminder notifications
//     and clears its updates' due state (content records);
//   - startDate: rebuilds the project's progress history from its issues;
//   - teamIds: re-derives the scope of every document attached to the
//     project (refreshProjectDocuments reads all documents);
//   - an ID the record key cannot hold (empty, over 191 bytes, NUL), which
//     the image resolves by native project ID.
func boundedProjectEditScope(id string, input domain.ProjectMutationInput) (store.MetadataRecordScope, bool) {
	if id == "" || len(id) > 191 || strings.ContainsRune(id, '\x00') ||
		input.UpdateSchedule != nil || input.StartDate != nil || input.TeamIDs != nil ||
		input.Archived != nil && *input.Archived {
		return store.MetadataRecordScope{}, false
	}
	scope := projectRecordRequirements("project", "edit", id, input)
	// Only dependency edits need the relation collection. Reading it for
	// a title or archive edit can exceed the record-page limit.
	scope.Fields = slices.DeleteFunc(scope.Fields, func(field string) bool {
		switch field {
		case "workspaceSettings":
			return len(input.Initiatives) == 0 && len(input.Customers) == 0
		case "projectRelations":
			return input.DependencyIDs == nil && input.DependencyRelations == nil
		}
		return false
	})
	return scope, true
}
