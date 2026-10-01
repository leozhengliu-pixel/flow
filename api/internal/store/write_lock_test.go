package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A copy-on-write writer holds only the writer lock while its callback and
// transaction run: snapshot readers and the post-write sinks must not queue
// behind it, other writers must, and the result is visible once it returns.
func TestCopyOnWriteMutationDoesNotBlockReaders(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	const workspace = "test-workspace"
	inside, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ctx := WithMetadataFields(context.Background(), "workspaceSettings")
		done <- repo.MutateWorkspace(ctx, workspace, "workspace.agent_guidance_updated", "", nil, func(data *domain.Bootstrap) error {
			close(inside)
			<-release
			data.WorkspaceSettings.WelcomeMessageTitle = "Held writer"
			return nil
		})
	}()
	<-inside
	read := make(chan string, 1)
	go func() {
		data, _ := repo.WorkspaceMetadataFields(workspace, "workspaceSettings", "users")
		_ = repo.webhook()
		_ = repo.realtime()
		read <- data.WorkspaceSettings.WelcomeMessageTitle
	}()
	select {
	case guidance := <-read:
		if guidance == "Held writer" {
			t.Fatal("reader saw an uncommitted write")
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("reader waited for an in-flight copy-on-write writer")
	}
	writer := make(chan error, 1)
	go func() {
		writer <- repo.MutateWorkspace(WithMetadataFields(context.Background(), "workspaceSettings"), workspace, "workspace.agent_guidance_updated", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.WelcomeMessageTitle = "Second writer"
			return nil
		})
	}()
	select {
	case <-writer:
		t.Fatal("a second writer ran while the first held the writer lock")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-writer; err != nil {
		t.Fatal(err)
	}
	if data, _ := repo.WorkspaceMetadataFields(workspace, "workspaceSettings"); data.WorkspaceSettings.WelcomeMessageTitle != "Second writer" {
		t.Fatalf("writes applied out of order: %q", data.WorkspaceSettings.WelcomeMessageTitle)
	}
}
