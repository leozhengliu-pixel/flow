package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP08TestStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P08 database tests require both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	config := store.DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	switch driver {
	case "":
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p08.db")
	case "sqlite":
		config.Path = databaseURL
	case "mysql", "postgres", "postgresql":
	default:
		t.Fatalf("unsupported P08 database driver %q", driver)
	}
	repository, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

func scaleP08OwnedWorkspace(t *testing.T, repository *store.SQLiteStore) (domain.User, domain.Bootstrap) {
	t.Helper()
	user, token, err := repository.Register(t.Context(), "P08 owner", idgen.New("scale-p08-owner-")+"@example.test", "p08-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := repository.EraseAccount(ctx, user.ID); err != nil {
			t.Errorf("clean up owned P08 account: %v", err)
		}
	})
	if err := repository.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	data, err := repository.CreateWorkspace(store.ContextWithActor(t.Context(), user), "P08 isolated", idgen.New("scale-p08-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := repository.DeleteWorkspace(ctx, data.Workspace.URLKey); err != nil {
			t.Errorf("clean up owned P08 workspace: %v", err)
		}
	})
	return user, data
}

func TestScaleP08AuthDisabledRealtimeRetainsViewer(t *testing.T) {
	repository := scaleP08TestStore(t)
	viewer, data := scaleP08OwnedWorkspace(t, repository)
	now := time.Now().UTC()
	notificationID := fmt.Sprintf("scale-p08-notification-%d", now.UnixNano())
	if err := repository.MutateWorkspace(t.Context(), data.Workspace.URLKey, "scale_p08.fixture", notificationID, nil, func(next *domain.Bootstrap) error {
		next.Notifications = append(next.Notifications, domain.Notification{ID: notificationID, RecipientID: viewer.ID, Type: "issueReminder", Actor: viewer, CreatedAt: now, UpdatedAt: now})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repository, authDisabled: true}
	request := httptest.NewRequest("GET", "/api/realtime/events?workspace="+data.Workspace.URLKey, nil)
	request = request.WithContext(context.WithValue(request.Context(), authUserContextKey{}, viewer))
	metadata, _, err := s.requestIssueQueryAccess(request)
	if err != nil || metadata.Viewer.ID != viewer.ID {
		t.Fatalf("realtime viewer=%q, want authenticated viewer %q: %v", metadata.Viewer.ID, viewer.ID, err)
	}
	for _, event := range []domain.RealtimeEvent{
		{ID: "scale-p08-notification", Type: "notification.unsnoozed", AggregateID: notificationID, ActorID: viewer.ID},
		{ID: "scale-p08-agent", Type: "agent.message_created", AggregateID: "scale-p08-session", ActorID: viewer.ID},
	} {
		if _, visible, err := s.pagedRealtimeEvent(request, event); err != nil || !visible {
			t.Errorf("own %s event visible=%v: %v", event.Type, visible, err)
		}
	}
}

func TestScaleP08FixturePreservesExistingState(t *testing.T) {
	repository := scaleP08TestStore(t)
	existing, data := scaleP08OwnedWorkspace(t, repository)
	var owned domain.Bootstrap
	t.Run("isolated-fixture", func(t *testing.T) {
		viewer, workspace := scaleP08OwnedWorkspace(t, repository)
		owned = workspace
		if viewer.ID == existing.ID || workspace.Workspace.ID == data.Workspace.ID {
			t.Fatal("P08 fixture reused an existing principal or workspace")
		}
	})
	if repository.HasWorkspace(owned.Workspace.URLKey) {
		t.Fatal("P08 fixture left its owned workspace behind")
	}
	if preserved, ok := repository.BootstrapFor(data.Workspace.URLKey); !ok || preserved.Workspace.ID != data.Workspace.ID {
		t.Fatal("P08 fixture changed an existing workspace")
	}
	if session, _, err := repository.Login(t.Context(), existing.Email, "p08-fixture-password"); err != nil || session.User.ID != existing.ID {
		t.Fatalf("P08 fixture changed existing credentials: %v", err)
	}
}
