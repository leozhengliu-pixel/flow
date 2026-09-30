package store

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// largeMetadataSnapshot builds an in-memory workspace with ~200k metadata
// records spread like a long-lived tenant.
func largeMetadataSnapshot() domain.Bootstrap {
	now := time.Now().UTC()
	data := domain.Bootstrap{Workspace: domain.Workspace{ID: "workspace_bench", URLKey: "bench"}}
	admin := domain.User{ID: "usr_admin", Name: "Admin", Email: "admin@example.test", Active: true}
	for i := 0; i < 300; i++ {
		data.Users = append(data.Users, domain.User{ID: fmt.Sprintf("usr_%d", i), Name: fmt.Sprintf("User %d", i), Email: fmt.Sprintf("u%d@example.test", i), Active: true})
	}
	data.TeamSettings = map[string]domain.TeamSettings{}
	for i := 0; i < 400; i++ {
		id := fmt.Sprintf("team_%d", i)
		data.Teams = append(data.Teams, domain.Team{ID: id, Name: id, Key: fmt.Sprintf("T%03d", i), CreatedAt: &now})
		data.TeamSettings[id] = domain.TeamSettings{TeamID: id, Timezone: "Etc/UTC", SlackNotifications: map[string]bool{}, PRAutomations: map[string]string{}}
		for j := 0; j < 6; j++ {
			data.States = append(data.States, domain.WorkflowState{ID: fmt.Sprintf("%s_state_%d", id, j), TeamID: id, Name: "State"})
		}
	}
	for i := 0; i < 64000; i++ {
		at := now.Add(-time.Duration(i) * time.Minute)
		data.IssueSLAs = append(data.IssueSLAs, domain.IssueSLA{ID: fmt.Sprintf("sla_%d", i), IssueID: fmt.Sprintf("issue_%d", i), StartedAt: at, DueAt: at, CompletedAt: &at, Status: "completed"})
	}
	for i := 0; i < 45000; i++ {
		data.SLAEvents = append(data.SLAEvents, domain.SLAEvent{ID: fmt.Sprintf("sla_event_%d", i), IssueID: fmt.Sprintf("issue_%d", i), Type: "completed", CreatedAt: now})
		data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: fmt.Sprintf("sub_%d", i), UserID: "usr_1", ResourceType: "issue", ResourceID: fmt.Sprintf("issue_%d", i), Events: []string{"all"}, CreatedAt: now})
	}
	for i := 0; i < 13000; i++ {
		data.Favorites = append(data.Favorites, domain.Favorite{ID: fmt.Sprintf("fav_%d", i), UserID: "usr_1", ResourceType: "issue", ResourceID: fmt.Sprintf("issue_%d", i), CreatedAt: now})
		data.IntegrationDeliveries = append(data.IntegrationDeliveries, domain.IntegrationDelivery{ID: fmt.Sprintf("delivery_%d", i), EventType: "issue.updated", Payload: []byte(`{"title":"x"}`), Status: "delivered", CreatedAt: now, UpdatedAt: now})
	}
	for i := 0; i < 1000; i++ {
		data.AuditLog = append(data.AuditLog, domain.AuditLogEntry{ID: fmt.Sprintf("audit_%d", i), Actor: admin, Action: "updated", Metadata: map[string]any{"field": "title"}, CreatedAt: now})
	}
	for i := 0; i < 7000; i++ {
		data.Trash = append(data.Trash, domain.TrashEntry{ID: fmt.Sprintf("trash_%d", i), ResourceType: "document", Payload: []byte(`{}`), DeletedBy: admin, DeletedAt: now})
	}
	return data
}

func BenchmarkLargeMetadataClone(b *testing.B) {
	data := largeMetadataSnapshot()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cloneBootstrap(data)
	}
}

func BenchmarkLargeMetadataReferenceClone(b *testing.B) {
	data := largeMetadataSnapshot()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = referenceCloneSnapshotValue(reflect.ValueOf(data))
	}
}

func BenchmarkLargeMetadataChangedFields(b *testing.B) {
	data := largeMetadataSnapshot()
	clone := cloneBootstrap(data)
	clone.Teams[3].Name = "Changed"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(changedMetadataFields(data, clone)) != 1 {
			b.Fatal("expected one changed field")
		}
	}
}
