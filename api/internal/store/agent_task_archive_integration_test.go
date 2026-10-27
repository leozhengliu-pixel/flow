//go:build integration

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// TestExternalDatabaseAgentSessionArchive archives agent sessions on MySQL or
// PostgreSQL (FLOW_TEST_DATABASE_DRIVER / FLOW_TEST_DATABASE_URL): the archive
// columns are added again without error, two store instances archiving the
// same session while a run streams into it agree on one archive, the run's
// later writes are refused, lists leave the session out, and a repeat archive
// writes nothing.
func TestExternalDatabaseAgentSessionArchive(t *testing.T) {
	driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if driver == "" || url == "" {
		t.Skip("external database not configured")
	}
	open := func() *SQLiteStore {
		repo, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, MaxOpenConns: 4, MaxIdleConns: 2})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { repo.Close() })
		return repo
	}
	first, second := open(), open()
	if err := first.ensureApplicationAgents(t.Context()); err != nil {
		t.Fatalf("repeat schema: %v", err)
	}
	ctx := t.Context()
	workspace := fmt.Sprintf("agent-archive-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), "DELETE FROM application_agent_activities WHERE session_id LIKE ?", "mention_"+workspace+"%")
		_, _ = first.db.ExecContext(context.Background(), "DELETE FROM application_agent_tasks WHERE workspace_key=?", workspace)
	})
	create := func(id string) domain.AgentTask {
		t.Helper()
		if err := first.CreateMentionTask(ctx, domain.AgentTask{ID: id, WorkspaceKey: workspace, IssueID: "issue-1", ResourceType: "issue", ResourceID: "issue-1", AppUserID: "app", CreatorID: "person"}); err != nil {
			t.Fatal(err)
		}
		task, err := first.AgentTask(ctx, workspace, id)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	streaming := create("mention_" + workspace + "_stream")
	finished := create("mention_" + workspace + "_done")
	if _, err := second.AppendAgentActivity(ctx, workspace, finished.ID, finished.Version, domain.AgentActivity{ActorID: "app", Type: "response", Body: "Report"}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	var runErr error
	var run sync.WaitGroup
	run.Add(1)
	go func() {
		defer run.Done()
		current, wrote := streaming, 0
		for attempt := 0; attempt < 5000; attempt++ {
			next, err := second.AppendAgentActivity(ctx, workspace, streaming.ID, current.Version, domain.AgentActivity{ActorID: "app", Type: "output", Body: "chunk "})
			if err == nil {
				current, wrote = next, wrote+1
				if wrote == 3 {
					close(started)
				}
				continue
			}
			if errors.Is(err, ErrIssueVersion) {
				if current, err = second.AgentTask(ctx, workspace, streaming.ID); err == nil {
					continue
				}
			}
			runErr = err
			return
		}
	}()
	<-started
	outcomes := make([]AgentTaskArchive, 4)
	errs := make([]error, 4)
	var archivers sync.WaitGroup
	for index := range outcomes {
		archivers.Add(1)
		go func() {
			defer archivers.Done()
			repo := first
			if index%2 == 1 {
				repo = second
			}
			outcomes[index], errs[index] = repo.ArchiveAgentTask(ctx, workspace, streaming.ID, "person")
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
	if winners != 1 || !errors.Is(runErr, ErrAgentTaskArchived) {
		t.Fatalf("winners=%d run=%v", winners, runErr)
	}
	log, err := first.AgentActivities(ctx, workspace, streaming.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	for len(log)%100 == 0 && len(log) > 0 {
		more, err := first.AgentActivities(ctx, workspace, streaming.ID, log[len(log)-1].ID)
		if err != nil || len(more) == 0 {
			break
		}
		log = append(log, more...)
	}
	if stop := log[len(log)-1]; stop.Type != "canceled" || stop.ID != fmt.Sprintf("%020d", archived.Version) {
		t.Fatalf("log ends with %+v, want the archive's stop at version %d", stop, archived.Version)
	}
	stored, err := second.AgentTask(ctx, workspace, streaming.ID)
	if err != nil || stored.Status != "canceled" || stored.ArchivedAt == nil || stored.ArchivedBy != "person" || stored.Version != archived.Version {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	visible, err := second.ListAgentTasks(ctx, workspace, "issue-1", "")
	if err != nil || len(visible) != 1 || visible[0].ID != finished.ID {
		t.Fatalf("visible: %+v %v", visible, err)
	}
	all, err := second.ListAgentTasksIncludingArchived(ctx, workspace, "issue-1", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all: %+v %v", all, err)
	}
	if _, err := first.AppendAgentActivity(ctx, workspace, streaming.ID, stored.Version, domain.AgentActivity{ActorID: "person", Type: "retry"}); !errors.Is(err, ErrAgentTaskArchived) {
		t.Fatalf("retry on an archived session: %v", err)
	}
	again, err := second.ArchiveAgentTask(ctx, workspace, streaming.ID, "admin")
	if err != nil || again.Archived || again.Task.Version != stored.Version || again.Task.ArchivedBy != "person" {
		t.Fatalf("repeat archive: %+v %v", again, err)
	}
	// The finished session archives without a stop and keeps its report.
	done, err := first.ArchiveAgentTask(ctx, workspace, finished.ID, "admin")
	if err != nil || !done.Archived || done.Stopped || done.Task.Status != "complete" {
		t.Fatalf("finished session: %+v %v", done, err)
	}
	if report, err := first.AgentActivities(ctx, workspace, finished.ID, ""); err != nil || len(report) != 1 || report[0].Body != "Report" {
		t.Fatalf("report: %+v %v", report, err)
	}
}
