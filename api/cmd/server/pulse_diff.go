package main

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func optionalDate(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func userName(user *domain.User) (string, string) {
	if user == nil {
		return "", ""
	}
	return user.ID, firstNonEmpty(user.DisplayName, user.Name)
}

// projectPulseSnapshot captures a project's state for the update being
// posted. Milestone progress comes from one grouped count over the project's
// indexed issue rows; no issue documents are read.
func (s *server) projectPulseSnapshot(ctx context.Context, workspace string, project domain.Project, now time.Time) *domain.PulseSnapshot {
	priority := project.Priority
	snapshot := &domain.PulseSnapshot{CapturedAt: now, StatusID: project.Status.ID, Status: project.Status.Name, Priority: &priority, PriorityLabel: project.PriorityLabel, StartDate: optionalDate(project.StartDate), TargetDate: optionalDate(project.TargetDate)}
	snapshot.LeadID, snapshot.Lead = userName(project.Lead)
	summary, err := s.store.QueryIssueRecordSummary(ctx, store.IssueRecordQuery{Workspace: workspace, ProjectIDs: []string{project.ID}})
	if err != nil {
		return snapshot
	}
	progress := 0.0
	if summary.Total > 0 {
		progress = float64(summary.Completed) / float64(summary.Total)
	}
	snapshot.Progress = &progress
	for _, milestone := range project.Milestones {
		totals := summary.Milestones[milestone.ID]
		value := 0.0
		if totals.Total > 0 {
			value = float64(totals.Completed) / float64(totals.Total)
		}
		snapshot.Milestones = append(snapshot.Milestones, domain.PulseMilestoneSnapshot{ID: milestone.ID, Name: milestone.Name, Progress: value, Total: totals.Total, Completed: totals.Completed, TargetDate: optionalDate(milestone.TargetDate)})
	}
	return snapshot
}

func initiativePulseSnapshot(data *domain.Bootstrap, initiative domain.Initiative, now time.Time) *domain.PulseSnapshot {
	snapshot := &domain.PulseSnapshot{CapturedAt: now, Status: initiative.Status, StatusID: initiative.Status, TargetDate: optionalDate(initiative.TargetDate), Projects: []domain.PulseRef{}}
	snapshot.LeadID, snapshot.Lead = userName(initiative.Owner)
	// Projects linked from either side: the initiative's list or a project's
	// initiatives (PATCH /api/projects/{id} {initiatives}).
	ids := slices.Clone(initiative.ProjectIDs)
	for _, project := range data.Projects {
		if slices.Contains(project.Initiatives, initiative.ID) && !slices.Contains(ids, project.ID) {
			ids = append(ids, project.ID)
		}
	}
	for _, id := range ids {
		name := id
		if project, err := fullProjectByID(data, id); err == nil {
			name = project.Name
		}
		snapshot.Projects = append(snapshot.Projects, domain.PulseRef{ID: id, Name: name})
	}
	children := []domain.PulseRef{}
	for child, parents := range domain.InitiativeParents(&domain.Bootstrap{Initiatives: data.Initiatives, InitiativeRelations: data.InitiativeRelations}) {
		if slices.Contains(parents, initiative.ID) {
			name := child
			for _, item := range data.Initiatives {
				if item.ID == child {
					name = item.Name
				}
			}
			children = append(children, domain.PulseRef{ID: child, Name: name})
		}
	}
	slices.SortFunc(children, func(a, b domain.PulseRef) int { return strings.Compare(a.ID, b.ID) })
	snapshot.Initiatives = &children
	return snapshot
}

func pulseChange(from, to, fromID, toID string) *domain.PulseValueChange {
	if fromID == toID && from == to {
		return nil
	}
	return &domain.PulseValueChange{From: from, To: to, FromID: fromID, ToID: toID}
}

// pulseDiffBetween compares the previous update's snapshot with the new one.
// It returns nil when there is no previous snapshot or nothing changed.
func pulseDiffBetween(previous, next *domain.PulseSnapshot) *domain.PulseDiff {
	if previous == nil || next == nil {
		return nil
	}
	diff := domain.PulseDiff{}
	diff.Status = pulseChange(previous.Status, next.Status, previous.StatusID, next.StatusID)
	if previous.Priority != nil && next.Priority != nil && *previous.Priority != *next.Priority {
		diff.Priority = &domain.PulseValueChange{From: previous.PriorityLabel, To: next.PriorityLabel, FromID: strconv.Itoa(*previous.Priority), ToID: strconv.Itoa(*next.Priority)}
	}
	diff.Lead = pulseChange(previous.Lead, next.Lead, previous.LeadID, next.LeadID)
	if diff.Lead != nil && previous.LeadID == next.LeadID {
		diff.Lead = nil // only the display name changed
	}
	diff.StartDate = pulseChange(previous.StartDate, next.StartDate, "", "")
	diff.TargetDate = pulseChange(previous.TargetDate, next.TargetDate, "", "")
	before := map[string]domain.PulseMilestoneSnapshot{}
	for _, milestone := range previous.Milestones {
		before[milestone.ID] = milestone
	}
	for _, milestone := range next.Milestones {
		old, existed := before[milestone.ID]
		change := domain.PulseMilestoneChange{ID: milestone.ID, Name: milestone.Name, To: milestone.Progress, Completed: milestone.Progress >= 1, TargetDate: milestone.TargetDate}
		if !existed {
			change.Added = true
			diff.Milestones = append(diff.Milestones, change)
		} else if old.Progress != milestone.Progress {
			from := old.Progress
			change.From = &from
			diff.Milestones = append(diff.Milestones, change)
		}
	}
	if previous.Progress != nil && next.Progress != nil && (*previous.Progress != *next.Progress || len(diff.Milestones) > 0) {
		diff.ProgressSince = &domain.PulseProgressChange{Date: previous.CapturedAt, From: *previous.Progress, To: *next.Progress}
	}
	// Initiative snapshots always record their project list, but an empty
	// list is omitted when stored: a nil previous list there means none.
	if next.Projects != nil && (previous.Projects != nil || previous.Milestones == nil && previous.Progress == nil) {
		diff.Projects = pulseRefChange(previous.Projects, next.Projects)
	}
	if previous.Initiatives != nil && next.Initiatives != nil {
		diff.Initiatives = pulseRefChange(*previous.Initiatives, *next.Initiatives)
	}
	if diff.Empty() {
		return nil
	}
	return &diff
}

// pulseRefChange lists the references added and removed between two sets,
// or nil when they hold the same ids.
func pulseRefChange(previous, next []domain.PulseRef) *domain.PulseProjectsChange {
	change := domain.PulseProjectsChange{Added: []domain.PulseRef{}, Removed: []domain.PulseRef{}}
	for _, item := range next {
		if !slices.ContainsFunc(previous, func(other domain.PulseRef) bool { return other.ID == item.ID }) {
			change.Added = append(change.Added, item)
		}
	}
	for _, item := range previous {
		if !slices.ContainsFunc(next, func(other domain.PulseRef) bool { return other.ID == item.ID }) {
			change.Removed = append(change.Removed, item)
		}
	}
	if len(change.Added) == 0 && len(change.Removed) == 0 {
		return nil
	}
	return &change
}

// latestProjectSnapshot is the snapshot of the newest update that has one.
func latestProjectSnapshot(updates []domain.ProjectUpdate) *domain.PulseSnapshot {
	for _, update := range updates {
		if update.Snapshot != nil {
			return update.Snapshot
		}
	}
	return nil
}

func latestInitiativeSnapshot(updates []domain.InitiativeUpdate) *domain.PulseSnapshot {
	for _, update := range updates {
		if update.Snapshot != nil {
			return update.Snapshot
		}
	}
	return nil
}

func pulseProjectIn(snapshot *store.PulseFeedSnapshot, id string) *domain.Project {
	for index := range snapshot.Projects {
		if snapshot.Projects[index].ID == id {
			return &snapshot.Projects[index]
		}
	}
	return nil
}

func pulseInitiativeIn(snapshot *store.PulseFeedSnapshot, id string) *domain.Initiative {
	for index := range snapshot.Initiatives {
		if snapshot.Initiatives[index].ID == id {
			return &snapshot.Initiatives[index]
		}
	}
	return nil
}

// projectUpdateDiffPreview returns the diff the next project update would
// record, for the composer. It reads the shared snapshot without copying it.
func (s *server) projectUpdateDiffPreview(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.store.PulseFeed(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	project := pulseProjectIn(snapshot, id)
	if project == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	next := s.projectPulseSnapshot(r.Context(), snapshot.Workspace.URLKey, *project, time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"diff": pulseDiffBetween(latestProjectSnapshot(snapshot.ProjectUpdates[id]), next), "snapshot": next})
}

func (s *server) initiativeUpdateDiffPreview(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.store.PulseFeed(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	initiative := pulseInitiativeIn(snapshot, id)
	if initiative == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	next := initiativePulseSnapshot(&domain.Bootstrap{Projects: snapshot.Projects, Initiatives: snapshot.Initiatives, InitiativeRelations: snapshot.InitiativeRelations}, *initiative, time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{"diff": pulseDiffBetween(latestInitiativeSnapshot(snapshot.InitiativeUpdates[id]), next), "snapshot": next})
}

// listProjectUpdates serves a project's updates (newest first) for paged
// clients, which no longer receive updates in the workspace bootstrap.
func (s *server) listProjectUpdates(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.store.PulseFeed(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	if pulseProjectIn(snapshot, id) == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	updates := append([]domain.ProjectUpdate{}, snapshot.ProjectUpdates[id]...)
	for index := range updates {
		updates[index].Snapshot = nil
	}
	writeJSON(w, http.StatusOK, updates)
}

func (s *server) listInitiativeUpdates(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := s.store.PulseFeed(workspaceKey(r))
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	id := r.PathValue("id")
	if pulseInitiativeIn(snapshot, id) == nil {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	updates := append([]domain.InitiativeUpdate{}, snapshot.InitiativeUpdates[id]...)
	for index := range updates {
		updates[index].Snapshot = nil
	}
	writeJSON(w, http.StatusOK, updates)
}

// withoutPulseSnapshot drops the stored snapshot from an update value before
// it leaves the server; snapshots only feed the next update's diff.
func withoutPulseSnapshot(value any) any {
	switch update := value.(type) {
	case domain.ProjectUpdate:
		update.Snapshot = nil
		return update
	case domain.InitiativeUpdate:
		update.Snapshot = nil
		return update
	}
	return value
}

// stripPulseSnapshots replaces the update collections of an outgoing
// bootstrap with copies whose updates carry no stored snapshot. The maps and
// slices may be shared with the store, so they are never edited in place.
func stripPulseSnapshots(data *domain.Bootstrap) {
	if len(data.ProjectUpdates) > 0 {
		projects := make(map[string][]domain.ProjectUpdate, len(data.ProjectUpdates))
		for id, updates := range data.ProjectUpdates {
			copied := slices.Clone(updates)
			for index := range copied {
				copied[index].Snapshot = nil
			}
			projects[id] = copied
		}
		data.ProjectUpdates = projects
	}
	if len(data.InitiativeUpdates) > 0 {
		initiatives := make(map[string][]domain.InitiativeUpdate, len(data.InitiativeUpdates))
		for id, updates := range data.InitiativeUpdates {
			copied := slices.Clone(updates)
			for index := range copied {
				copied[index].Snapshot = nil
			}
			initiatives[id] = copied
		}
		data.InitiativeUpdates = initiatives
	}
}
