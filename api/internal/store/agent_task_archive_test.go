package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"flow/api/internal/domain"
)

type archiveTestRepo struct {
	*SQLiteStore
	workspace string
	mu        sync.Mutex
	events    []domain.RealtimeEvent
}

func newArchiveTestRepo(t *testing.T) *archiveTestRepo {
	t.Helper()
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repo.Close() })
	r := &archiveTestRepo{SQLiteStore: repo, workspace: repo.Bootstrap().Workspace.URLKey}
	repo.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, event)
	})
	return r
}

func (r *archiveTestRepo) eventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *archiveTestRepo) session(t *testing.T, id string, activities ...domain.AgentActivity) domain.AgentTask {
	t.Helper()
	ctx := context.Background()
	if err := r.CreateMentionTask(ctx, domain.AgentTask{ID: id, WorkspaceKey: r.workspace, IssueID: "issue_1", ResourceType: "issue", ResourceID: "issue_1", AppUserID: "app", CreatorID: "usr_member", CommentID: "comment_" + id, Trigger: "mention"}); err != nil {
		t.Fatal(err)
	}
	task, err := r.AgentTask(ctx, r.workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, activity := range activities {
		if task, err = r.AppendAgentActivity(ctx, r.workspace, id, task.Version, activity); err != nil {
			t.Fatalf("%s: %v", activity.Type, err)
		}
	}
	return task
}

func (r *archiveTestRepo) activities(t *testing.T, id string) []domain.AgentActivity {
	t.Helper()
	items, err := r.AgentActivities(context.Background(), r.workspace, id, "")
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func listed(tasks []domain.AgentTask, id string) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}

// Archiving a running session stops it in the same commit, takes it out of the
// resource's session list and keeps its log; archiving again writes nothing.
func TestArchiveAgentTaskStopsRunningSessionAndIsIdempotent(t *testing.T) {
	r := newArchiveTestRepo(t)
	ctx := context.Background()
	running := r.session(t, "mention_running", domain.AgentActivity{ActorID: "app", Type: "thought", Body: "Looking"}, domain.AgentActivity{ActorID: "app", Type: "action", Body: "list_issues"})
	kept := r.session(t, "mention_kept", domain.AgentActivity{ActorID: "app", Type: "response", Body: "Report: all good"})
	before := len(r.activities(t, running.ID))
	events := r.eventCount()

	outcome, err := r.ArchiveAgentTask(ctx, r.workspace, running.ID, "usr_member")
	if err != nil {
		t.Fatal(err)
	}
	task := outcome.Task
	if !outcome.Archived || !outcome.Stopped || outcome.PreviousStatus != "active" || task.Status != "canceled" || task.Version != running.Version+1 || task.ArchivedAt == nil || task.ArchivedBy != "usr_member" {
		t.Fatalf("archive outcome: %+v", outcome)
	}
	stored, err := r.AgentTask(ctx, r.workspace, running.ID)
	if err != nil || stored.ArchivedAt == nil || stored.ArchivedBy != "usr_member" || stored.Status != "canceled" || stored.Version != task.Version {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	log := r.activities(t, running.ID)
	if len(log) != before+1 {
		t.Fatalf("log after archive: %d activities, want %d", len(log), before+1)
	}
	if stop := log[len(log)-1]; stop.Type != "canceled" || stop.ActorID != "usr_member" || stop.Signal != "stop" || stop.Metadata["reason"] != "archived" || stop.ID != paddedTestVersion(task.Version) {
		t.Fatalf("stop activity: %+v", stop)
	}
	if got := r.eventCount() - events; got != 1 {
		t.Fatalf("archive published %d events, want 1", got)
	}
	last := r.events[len(r.events)-1]
	var payload map[string]any
	_ = json.Unmarshal(last.Payload, &payload)
	if last.Type != "agent_task.updated" || last.AggregateID != "issue_1" || payload["archived"] != true || payload["taskId"] != running.ID {
		t.Fatalf("event: %+v %v", last, payload)
	}

	visible, err := r.ListAgentTasks(ctx, r.workspace, "issue_1", "")
	if err != nil || listed(visible, running.ID) || !listed(visible, kept.ID) {
		t.Fatalf("visible sessions: %+v %v", visible, err)
	}
	all, err := r.ListAgentTasksIncludingArchived(ctx, r.workspace, "issue_1", "")
	if err != nil || !listed(all, running.ID) || !listed(all, kept.ID) {
		t.Fatalf("all sessions: %+v %v", all, err)
	}
	if pending, _ := r.PendingApplicationTasks(ctx); listed(pending, running.ID) {
		t.Fatal("an archived session is still delivered")
	}
	if claimed, _ := r.ClaimApplicationTask(ctx, running.ID, task.Version); claimed {
		t.Fatal("an archived session was claimed")
	}

	// Again: no write, no event, the same stamp.
	events = r.eventCount()
	again, err := r.ArchiveAgentTask(ctx, r.workspace, running.ID, "usr_admin")
	if err != nil || again.Archived || again.Stopped || again.Task.Version != task.Version || again.Task.ArchivedBy != "usr_member" || !again.Task.ArchivedAt.Equal(*stored.ArchivedAt) {
		t.Fatalf("second archive: %+v %v", again, err)
	}
	if r.eventCount() != events || len(r.activities(t, running.ID)) != len(log) {
		t.Fatal("the second archive wrote or published")
	}

	// The other session, its report and its log are untouched.
	untouched, err := r.AgentTask(ctx, r.workspace, kept.ID)
	if err != nil || untouched.ArchivedAt != nil || untouched.Status != "complete" || untouched.Version != kept.Version || len(r.activities(t, kept.ID)) != 1 {
		t.Fatalf("kept session: %+v %v", untouched, err)
	}
	if _, err := r.ArchiveAgentTask(ctx, r.workspace, "mention_missing", "usr_member"); !IsAgentTaskMissing(err) {
		t.Fatalf("missing session: %v", err)
	}
}

func paddedTestVersion(version int64) string { return fmt.Sprintf("%020d", version) }

// A finished, failed or canceled session keeps its status: only the archive
// stamp and the version change.
func TestArchiveAgentTaskKeepsTerminalStatus(t *testing.T) {
	r := newArchiveTestRepo(t)
	ctx := context.Background()
	for status, activities := range map[string][]domain.AgentActivity{
		"complete": {{ActorID: "app", Type: "response", Body: "Done"}},
		"error":    {{ActorID: "app", Type: "error", Body: "Model failed"}},
		"canceled": {{ActorID: "usr_member", Type: "canceled"}},
	} {
		task := r.session(t, "mention_"+status, activities...)
		before := len(r.activities(t, task.ID))
		outcome, err := r.ArchiveAgentTask(ctx, r.workspace, task.ID, "usr_admin")
		if err != nil || !outcome.Archived || outcome.Stopped || outcome.Task.Status != status || outcome.PreviousStatus != status || outcome.Task.Version != task.Version+1 {
			t.Fatalf("%s: %+v %v", status, outcome, err)
		}
		if len(r.activities(t, task.ID)) != before {
			t.Fatalf("%s: archiving a finished session added an activity", status)
		}
	}
}

// Nothing reopens or changes an archived session: a late agent activity, a
// person's retry or reply, feedback, a reply deletion, a plan update or a
// delivery stamp is refused and publishes nothing, and a coding session cannot
// reuse it as its anchor.
func TestArchivedAgentTaskRefusesLateWrites(t *testing.T) {
	r := newArchiveTestRepo(t)
	ctx := context.Background()
	task := r.session(t, "mention_late", domain.AgentActivity{ActorID: "app", Type: "error", Body: "Failed"})
	errorID := r.activities(t, task.ID)[0].ID
	outcome, err := r.ArchiveAgentTask(ctx, r.workspace, task.ID, "usr_member")
	if err != nil {
		t.Fatal(err)
	}
	archived := outcome.Task
	events := r.eventCount()
	for _, activity := range []domain.AgentActivity{
		{ActorID: "app", Type: "response", Body: "Late answer"},
		{ActorID: "app", Type: "thought", Body: "Still thinking"},
		{ActorID: "usr_member", Type: "retry"},
		{ActorID: "usr_member", Type: "prompt", Body: "Try again"},
		{ActorID: "usr_member", Type: "feedback", Body: "👍", Metadata: map[string]any{"activityId": errorID, "enabled": true}},
		{ActorID: "usr_member", Type: "deleted", Metadata: map[string]any{"activityId": errorID}},
	} {
		if _, err := r.AppendAgentActivity(ctx, r.workspace, task.ID, archived.Version, activity); !errors.Is(err, ErrAgentTaskArchived) {
			t.Fatalf("%s on an archived session: %v", activity.Type, err)
		}
		// An activity written at the version read before the archive loses the race too.
		if _, err := r.AppendAgentActivity(ctx, r.workspace, task.ID, task.Version, activity); err == nil {
			t.Fatalf("%s at the pre-archive version was accepted", activity.Type)
		}
	}
	if _, err := r.UpdateAgentTaskSession(ctx, r.workspace, task.ID, func(task *domain.AgentTask) error {
		task.Plan = []domain.AgentPlanStep{{Content: "Revive", Status: "inProgress"}}
		return nil
	}); !errors.Is(err, ErrAgentTaskArchived) {
		t.Fatalf("plan update: %v", err)
	}
	if err := r.MarkAgentTaskDelivered(ctx, r.workspace, task.ID, archived.Version, archived.UpdatedAt); !errors.Is(err, ErrAgentTaskArchived) {
		t.Fatalf("delivery stamp: %v", err)
	}
	if _, err := r.endedCodingAnchor(ctx, domain.AgentTask{ID: task.ID, WorkspaceKey: r.workspace, AppUserID: "app"}); !errors.Is(err, ErrCodingAnchorRevoked) {
		t.Fatalf("coding anchor reuse: %v", err)
	}
	if r.eventCount() != events {
		t.Fatal("a refused write published an update")
	}
	stored, err := r.AgentTask(ctx, r.workspace, task.ID)
	if err != nil || stored.Version != archived.Version || stored.Status != "error" || stored.ArchivedAt == nil || len(stored.Plan) != 0 {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	if len(r.activities(t, task.ID)) != 1 {
		t.Fatal("a refused write reached the log")
	}

	// A writer that replaces the task JSON without the archive stamp (an older
	// node, a stale read) cannot unarchive it: the columns decide.
	stale, _ := json.Marshal(domain.AgentTask{ID: task.ID, WorkspaceKey: r.workspace, IssueID: "issue_1", AppUserID: "app", CreatorID: "usr_member", Status: "pending"})
	if _, err := r.db.ExecContext(ctx, `UPDATE application_agent_tasks SET data=? WHERE id=?`, stale, task.ID); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.AgentTask(ctx, r.workspace, task.ID); err != nil || stored.ArchivedAt == nil || stored.ArchivedBy != "usr_member" {
		t.Fatalf("after a data overwrite: %+v %v", stored, err)
	}
	if visible, _ := r.ListAgentTasks(ctx, r.workspace, "issue_1", ""); listed(visible, task.ID) {
		t.Fatal("a data overwrite brought the session back")
	}
}

// A run streaming into a session while a person archives it: every write
// after the archive is refused, the log ends with the archive's stop, the run
// learns the session is archived, and concurrent archives agree on one stamp
// and one event.
func TestArchiveAgentTaskRacesAStreamingRun(t *testing.T) {
	r := newArchiveTestRepo(t)
	ctx := context.Background()
	task := r.session(t, "mention_stream", domain.AgentActivity{ActorID: "app", Type: "thought", Body: "Start"})
	started := make(chan struct{})
	var runErr error
	var wrote int
	var run sync.WaitGroup
	run.Add(1)
	go func() {
		defer run.Done()
		current := task
		for attempt := 0; attempt < 10000; attempt++ {
			next, err := r.AppendAgentActivity(ctx, r.workspace, task.ID, current.Version, domain.AgentActivity{ActorID: "app", Type: "output", Body: "chunk "})
			if err == nil {
				current, wrote = next, wrote+1
				if wrote == 5 {
					close(started)
				}
				continue
			}
			if errors.Is(err, ErrIssueVersion) {
				// The built-in run's retry: reread and continue at the new version.
				if current, err = r.AgentTask(ctx, r.workspace, task.ID); err == nil {
					continue
				}
			}
			runErr = err
			return
		}
	}()
	<-started
	events := r.eventCount()
	outcomes := make([]AgentTaskArchive, 4)
	errs := make([]error, 4)
	var archivers sync.WaitGroup
	for index := range outcomes {
		archivers.Add(1)
		go func() {
			defer archivers.Done()
			outcomes[index], errs[index] = r.ArchiveAgentTask(ctx, r.workspace, task.ID, "usr_member")
		}()
	}
	archivers.Wait()
	run.Wait()
	winners := 0
	var archived domain.AgentTask
	for index, outcome := range outcomes {
		if errs[index] != nil {
			t.Fatalf("archive %d: %v", index, errs[index])
		}
		if outcome.Archived {
			winners++
			archived = outcome.Task
		}
	}
	if winners != 1 {
		t.Fatalf("%d archives won, want 1", winners)
	}
	if !errors.Is(runErr, ErrAgentTaskArchived) {
		t.Fatalf("the run ended with %v, want ErrAgentTaskArchived", runErr)
	}
	log := r.activities(t, task.ID)
	stop := log[len(log)-1]
	if stop.Type != "canceled" || stop.ID != paddedTestVersion(archived.Version) {
		t.Fatalf("the log does not end with the archive's stop: %+v", stop)
	}
	for _, activity := range log {
		if activity.ID > stop.ID {
			t.Fatalf("activity %s written after the archive", activity.ID)
		}
	}
	stored, err := r.AgentTask(ctx, r.workspace, task.ID)
	if err != nil || stored.Status != "canceled" || stored.Version != archived.Version {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	// One archive event, whatever the run published before it.
	archiveEvents := 0
	r.mu.Lock()
	for _, event := range r.events[events:] {
		var payload map[string]any
		_ = json.Unmarshal(event.Payload, &payload)
		if payload["archived"] == true {
			archiveEvents++
		}
	}
	r.mu.Unlock()
	if archiveEvents != 1 {
		t.Fatalf("%d archive events, want 1", archiveEvents)
	}
}
