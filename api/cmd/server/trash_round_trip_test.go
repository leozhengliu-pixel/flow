package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Each trashable resource must come back from the trash exactly as it was:
// every link the delete detached (issues in a project, sub-initiatives, a
// customer's requests, ...) is re-attached on restore. The script runs on the
// scoped mutation path and on the full path.
func TestTrashRoundTripRestoresLinks(t *testing.T) {
	for _, forceFull := range []bool{false, true} {
		t.Run(fmt.Sprintf("full=%v", forceFull), func(t *testing.T) {
			restore := store.ForceFullMutationsForTesting(forceFull)
			defer restore()
			runTrashRoundTrip(t)
		})
	}
}

func runTrashRoundTrip(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	boot := func() domain.Bootstrap {
		return requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	}
	data := boot()
	teamID := data.Teams[0].ID
	issueByID := func(id string) domain.Issue {
		t.Helper()
		return requestJSON[domain.Issue](t, handler, http.MethodGet, "/api/issue-records/"+id, nil, http.StatusOK)
	}
	newIssue := func(title string) domain.Issue {
		return requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": title, "teamId": teamID}, http.StatusCreated)
	}
	restoreEntry := func(resourceType, resourceID string) {
		t.Helper()
		for _, entry := range boot().Trash {
			if entry.ResourceType == resourceType && entry.ResourceID == resourceID {
				if entry.Links != nil {
					t.Fatalf("trash links leaked to the bootstrap: %#v", entry.Links)
				}
				requestJSON[any](t, handler, http.MethodPost, "/api/trash/"+entry.ID+"/restore", nil, http.StatusOK)
				return
			}
		}
		t.Fatalf("%s %s is not in the trash", resourceType, resourceID)
	}

	// Project: issues (with their milestone), dependencies, relations,
	// favorites, updates, documents and initiative links come back.
	project := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Trash project", "teamIds": []string{teamID}}, http.StatusCreated)
	other := requestJSON[domain.Project](t, handler, http.MethodPost, "/api/projects", map[string]any{"name": "Dependent project", "teamIds": []string{teamID}}, http.StatusCreated)
	milestone := requestJSON[domain.ProjectMilestone](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/milestones", map[string]any{"name": "M1"}, http.StatusCreated)
	inMilestone, inProject := newIssue("In milestone"), newIssue("In project")
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+inMilestone.ID, map[string]any{"projectId": project.ID, "projectMilestoneId": milestone.ID}, http.StatusOK)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+inProject.ID, map[string]any{"projectId": project.ID}, http.StatusOK)
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+other.ID, map[string]any{"dependencyIds": []string{project.ID}}, http.StatusOK)
	relation := requestJSON[domain.ProjectRelation](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/relations", map[string]any{"relatedProjectId": other.ID, "type": "related"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPut, "/api/favorites/project/"+project.ID, nil, http.StatusOK)
	requestJSON[domain.ProjectUpdate](t, handler, http.MethodPost, "/api/projects/"+project.ID+"/updates", map[string]any{"body": "On track", "health": "onTrack"}, http.StatusCreated)
	projectDoc := requestJSON[domain.Document](t, handler, http.MethodPost, "/api/documents", map[string]any{"title": "Project doc", "projectIds": []string{project.ID}}, http.StatusCreated)
	initiative := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Trash initiative", "projectIds": []string{project.ID}}, http.StatusCreated)

	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+project.ID, nil, http.StatusNoContent)
	if issue := issueByID(inMilestone.ID); issue.Project != nil || issue.ProjectMilestoneID != nil || issue.ArchivedAt == nil {
		t.Fatalf("deleted project's issue not archived and unlinked: %#v %#v %v", issue.Project, issue.ProjectMilestoneID, issue.ArchivedAt)
	}
	restoreEntry("project", project.ID)
	if issue := issueByID(inMilestone.ID); issue.Project == nil || issue.Project.ID != project.ID || issue.ProjectMilestoneID == nil || *issue.ProjectMilestoneID != milestone.ID {
		t.Fatalf("restored project lost its milestone issue: %#v %#v", issue.Project, issue.ProjectMilestoneID)
	}
	if issue := issueByID(inProject.ID); issue.Project == nil || issue.Project.ID != project.ID || issue.ArchivedAt != nil {
		t.Fatalf("restored project lost its issue or left it archived: %#v %v", issue.Project, issue.ArchivedAt)
	}
	data = boot()
	otherIndex := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == other.ID })
	projectIndex := slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if projectIndex < 0 || !slices.Contains(data.Projects[otherIndex].DependencyIDs, project.ID) {
		t.Fatalf("project dependency not restored: %#v", data.Projects[otherIndex].DependencyIDs)
	}
	if !slices.ContainsFunc(data.ProjectRelations, func(item domain.ProjectRelation) bool { return item.ID == relation.ID }) {
		t.Fatal("project relation not restored")
	}
	if !slices.ContainsFunc(data.Favorites, func(item domain.Favorite) bool {
		return item.ResourceType == "project" && item.ResourceID == project.ID
	}) {
		t.Fatal("project favorite not restored")
	}
	if len(data.ProjectUpdates[project.ID]) != 1 || !slices.Contains(data.Projects[projectIndex].Initiatives, initiative.ID) {
		t.Fatalf("project updates or initiative link lost: %d %#v", len(data.ProjectUpdates[project.ID]), data.Projects[projectIndex].Initiatives)
	}
	if !slices.ContainsFunc(data.Documents, func(item domain.Document) bool {
		return item.ID == projectDoc.ID && slices.Contains(item.ProjectIDs, project.ID)
	}) {
		t.Fatal("project document link lost")
	}

	// Initiative: projects, sub-initiatives, parent and relations come back.
	parent := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Parent initiative"}, http.StatusCreated)
	child := requestJSON[domain.Initiative](t, handler, http.MethodPost, "/api/initiatives", map[string]any{"name": "Sub initiative", "parentInitiativeIds": []string{initiative.ID}}, http.StatusCreated)
	requestJSON[domain.Initiative](t, handler, http.MethodPatch, "/api/initiatives/"+initiative.ID, map[string]any{"parentInitiativeIds": []string{parent.ID}}, http.StatusOK)
	related := requestJSON[domain.InitiativeRelation](t, handler, http.MethodPost, "/api/initiatives/"+initiative.ID+"/relations", map[string]any{"relatedInitiativeId": parent.ID, "type": "related"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+initiative.ID, nil, http.StatusNoContent)
	data = boot()
	if slices.ContainsFunc(data.Initiatives, func(item domain.Initiative) bool {
		return item.ID == child.ID && slices.Contains(item.ParentInitiativeIDs, initiative.ID)
	}) {
		t.Fatal("deleted initiative still parents its sub-initiative")
	}
	restoreEntry("initiative", initiative.ID)
	data = boot()
	restoredInitiative, _ := initiativeByID(&data, initiative.ID)
	restoredChild, _ := initiativeByID(&data, child.ID)
	projectIndex = slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if restoredInitiative == nil || !slices.Contains(restoredInitiative.ProjectIDs, project.ID) || !slices.Contains(data.Projects[projectIndex].Initiatives, initiative.ID) {
		t.Fatal("restored initiative lost its project")
	}
	if !slices.Contains(restoredInitiative.ParentInitiativeIDs, parent.ID) || restoredChild == nil || !slices.Contains(restoredChild.ParentInitiativeIDs, initiative.ID) {
		t.Fatalf("restored initiative hierarchy lost: parents %#v child %#v", restoredInitiative.ParentInitiativeIDs, restoredChild)
	}
	if !slices.ContainsFunc(data.InitiativeRelations, func(item domain.InitiativeRelation) bool { return item.ID == related.ID }) {
		t.Fatal("initiative relation not restored")
	}
	// A sub-initiative deleted alone goes back under its parent.
	requestJSON[any](t, handler, http.MethodDelete, "/api/initiatives/"+child.ID, nil, http.StatusNoContent)
	restoreEntry("initiative", child.ID)
	data = boot()
	if restoredChild, _ = initiativeByID(&data, child.ID); restoredChild == nil || !slices.Contains(restoredChild.ParentInitiativeIDs, initiative.ID) {
		t.Fatal("restored sub-initiative lost its parent")
	}

	// Issue: sub-issues, parent, relations, labels, project and customer
	// requests come back.
	parentIssue, sub, blocked := newIssue("Parent issue"), newIssue("Sub issue"), newIssue("Blocked issue")
	label := data.Labels[0].ID
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+sub.ID, map[string]any{"parentId": parentIssue.ID, "labelIds": []string{label}}, http.StatusOK)
	grandchild := newIssue("Grandchild")
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+grandchild.ID, map[string]any{"parentId": sub.ID}, http.StatusOK)
	requestJSON[domain.IssueRelation](t, handler, http.MethodPost, "/api/issues/"+sub.ID+"/relations", map[string]any{"type": "blocks", "relatedIssueId": blocked.ID}, http.StatusCreated)
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+sub.ID, map[string]any{"projectId": project.ID}, http.StatusOK)
	customer := requestJSON[domain.Customer](t, handler, http.MethodPost, "/api/customers", map[string]any{"name": "Trash customer"}, http.StatusCreated)
	request := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": customer.ID, "body": "Need it", "issueId": sub.ID}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodPut, "/api/subscriptions/issue/"+sub.ID, nil, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+sub.ID, nil, http.StatusNoContent)
	// Like the reference app, the sub-issue goes to the trash with its parent.
	requestJSON[any](t, handler, http.MethodGet, "/api/issue-records/"+grandchild.ID, nil, http.StatusNotFound)
	if !slices.ContainsFunc(boot().Trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == grandchild.ID }) {
		t.Fatal("sub-issue not trashed with its parent")
	}
	if issue := issueByID(blocked.ID); len(issue.Relations) != 0 {
		t.Fatal("relation still on the related issue")
	}
	restoreEntry("issue", sub.ID)
	restoredSub := issueByID(sub.ID)
	if restoredSub.ParentID == nil || *restoredSub.ParentID != parentIssue.ID || !slices.Contains(issueByID(parentIssue.ID).SubIssueIDs, sub.ID) {
		t.Fatal("restored issue lost its parent")
	}
	if issue := issueByID(grandchild.ID); issue.ParentID == nil || *issue.ParentID != sub.ID || !slices.Contains(restoredSub.SubIssueIDs, grandchild.ID) {
		t.Fatal("restored issue did not bring back its sub-issue")
	}
	if slices.ContainsFunc(boot().Trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == grandchild.ID }) {
		t.Fatal("sub-issue restored with its parent is still in the trash")
	}
	if issue := issueByID(blocked.ID); !slices.ContainsFunc(issue.Relations, func(item domain.IssueRelation) bool { return item.IssueID == sub.ID || item.RelatedIssueID == sub.ID }) || len(restoredSub.Relations) != 1 {
		t.Fatalf("restored issue lost its relation: %#v %#v", issue.Relations, restoredSub.Relations)
	}
	if len(restoredSub.Labels) != 1 || restoredSub.Labels[0].ID != label || restoredSub.Project == nil || restoredSub.Project.ID != project.ID {
		t.Fatalf("restored issue lost labels or project: %#v %#v", restoredSub.Labels, restoredSub.Project)
	}
	data = boot()
	if !slices.ContainsFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.ID == request.ID && item.IssueID == sub.ID }) {
		t.Fatal("restored issue lost its customer request")
	}
	if !slices.ContainsFunc(data.Subscriptions, func(item domain.Subscription) bool { return item.ResourceType == "issue" && item.ResourceID == sub.ID }) {
		t.Fatal("restored issue lost its subscription")
	}

	// Converting an issue to a project deletes it but keeps its sub-issues.
	converted, kept := newIssue("Converted"), newIssue("Kept child")
	requestJSON[domain.Issue](t, handler, http.MethodPatch, "/api/issues/"+kept.ID, map[string]any{"parentId": converted.ID}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+converted.ID+"?subIssues=keep", nil, http.StatusNoContent)
	if issue := issueByID(kept.ID); issue.ParentID != nil {
		t.Fatal("kept sub-issue still points at the deleted issue")
	}

	// Customer:, deleting it is permanent and takes its
	// requests (including one already in the trash) with it.
	trashedRequest := requestJSON[domain.CustomerRequest](t, handler, http.MethodPost, "/api/customer-requests", map[string]any{"customerId": customer.ID, "body": "Second need"}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/customer-requests/"+trashedRequest.ID, nil, http.StatusNoContent)
	requestJSON[domain.Project](t, handler, http.MethodPatch, "/api/projects/"+project.ID, map[string]any{"customers": []string{customer.ID}}, http.StatusOK)
	requestJSON[any](t, handler, http.MethodDelete, "/api/customers/"+customer.ID, nil, http.StatusNoContent)
	data = boot()
	projectIndex = slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if slices.ContainsFunc(data.CustomerRequests, func(item domain.CustomerRequest) bool { return item.CustomerID == customer.ID }) || slices.Contains(data.Projects[projectIndex].Customers, customer.ID) {
		t.Fatal("deleted customer's requests or project link remain")
	}
	if slices.ContainsFunc(data.Trash, func(entry domain.TrashEntry) bool {
		return entry.ResourceID == customer.ID || entry.ResourceID == trashedRequest.ID
	}) {
		t.Fatal("permanently deleted customer (or its trashed request) is in the trash")
	}

	// Document: the project resource row (with its pins) comes back.
	data = boot()
	projectIndex = slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if !slices.ContainsFunc(data.Projects[projectIndex].Resources, func(item domain.ProjectResource) bool { return item.ID == projectDoc.ID }) {
		t.Fatal("project document has no resource row")
	}
	requestJSON[any](t, handler, http.MethodDelete, "/api/documents/"+projectDoc.ID, nil, http.StatusNoContent)
	restoreEntry("document", projectDoc.ID)
	data = boot()
	projectIndex = slices.IndexFunc(data.Projects, func(item domain.Project) bool { return item.ID == project.ID })
	if !slices.ContainsFunc(data.Projects[projectIndex].Resources, func(item domain.ProjectResource) bool { return item.ID == projectDoc.ID }) {
		t.Fatal("restored document lost its project resource")
	}

	// An issue restored while its project is in the trash rejoins the project
	// when the project is restored.
	requestJSON[any](t, handler, http.MethodDelete, "/api/issues/"+inProject.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodDelete, "/api/projects/"+project.ID, nil, http.StatusNoContent)
	restoreEntry("issue", inProject.ID)
	if issue := issueByID(inProject.ID); issue.Project != nil {
		t.Fatal("issue restored into a deleted project")
	}
	restoreEntry("project", project.ID)
	if issue := issueByID(inProject.ID); issue.Project == nil || issue.Project.ID != project.ID {
		t.Fatal("restoring the project did not bring back the issue restored before it")
	}
}

// A deleted loop is gone for good with its versions and run history (like
// The reference app). A deleted release pipeline comes back with the releases deleted
// with it.
func TestTrashRoundTripLoopsAndPipelines(t *testing.T) {
	repository, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	handler := newHandler(&server{store: repository, uploadPath: t.TempDir(), authDisabled: true})
	workspace := repository.Bootstrap().Workspace.URLKey
	trashEntry := func(resourceType, resourceID string) domain.TrashEntry {
		t.Helper()
		for _, entry := range requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK).Trash {
			if entry.ResourceType == resourceType && entry.ResourceID == resourceID {
				return entry
			}
		}
		t.Fatalf("%s %s is not in the trash", resourceType, resourceID)
		return domain.TrashEntry{}
	}

	loop := requestJSON[domain.Loop](t, handler, http.MethodPost, "/api/loops", map[string]any{
		"name": "Trash loop", "triggerType": "issue", "triggerConfig": map[string]any{"action": "created"}, "instructions": "Triage.", "status": "published",
	}, http.StatusCreated)
	versions := requestJSON[[]domain.LoopVersion](t, handler, http.MethodGet, "/api/loops/"+loop.ID+"/versions", nil, http.StatusOK)
	if len(versions) == 0 {
		t.Fatal("published loop has no versions")
	}
	now := time.Now().UTC()
	if err := repository.CreateLoopRun(t.Context(), workspace, domain.LoopRun{ID: "loop_run_trash", LoopID: loop.ID, Status: "completed", StartedAt: now, FinishedAt: &now}, store.LoopRunLease{}); err != nil {
		t.Fatal(err)
	}
	// Like the reference app, deleting a loop is permanent: versions and runs go too.
	requestJSON[any](t, handler, http.MethodDelete, "/api/loops/"+loop.ID, nil, http.StatusNoContent)
	if runs, err := repository.ListLoopRuns(t.Context(), workspace, loop.ID, 10); err != nil || len(runs) != 0 {
		t.Fatalf("deleted loop kept its runs: %v %d", err, len(runs))
	}
	data := repository.Bootstrap()
	if slices.ContainsFunc(data.LoopVersions, func(item domain.LoopVersion) bool { return item.LoopID == loop.ID }) || slices.ContainsFunc(data.Trash, func(entry domain.TrashEntry) bool { return entry.ResourceID == loop.ID }) {
		t.Fatal("deleted loop kept versions or a trash entry")
	}

	teamID := repository.Bootstrap().Teams[0].ID
	pipeline := requestJSON[domain.ReleasePipeline](t, handler, http.MethodPost, "/api/release-pipelines", map[string]any{"name": "Trash pipeline", "teamIds": []string{teamID}}, http.StatusCreated)
	release := requestJSON[domain.Release](t, handler, http.MethodPost, "/api/releases", map[string]any{"name": "v1", "pipelineId": pipeline.ID}, http.StatusCreated)
	requestJSON[any](t, handler, http.MethodDelete, "/api/release-pipelines/"+pipeline.ID, nil, http.StatusNoContent)
	requestJSON[any](t, handler, http.MethodPost, "/api/trash/"+trashEntry("release_pipeline", pipeline.ID).ID+"/restore", nil, http.StatusOK)
	data = requestJSON[domain.Bootstrap](t, handler, http.MethodGet, "/api/bootstrap", nil, http.StatusOK)
	if !slices.ContainsFunc(data.Releases, func(item domain.Release) bool { return item.ID == release.ID }) {
		t.Fatal("restored pipeline did not bring back its release")
	}
	if slices.ContainsFunc(data.Trash, func(item domain.TrashEntry) bool { return item.ResourceID == release.ID }) {
		t.Fatal("restored release is still in the trash")
	}
}
