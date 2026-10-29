package store

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

func TestMemberStatusEventsAreDurable(t *testing.T) {
	for name, repository := range openCodingEnvironmentStores(t) {
		t.Run(name, func(t *testing.T) {
			data := repository.Bootstrap()
			member := originTestMember(t, repository, data, fmt.Sprintf("Event Member %d", time.Now().UnixNano()))
			repository.syncMemberDirectoryByID(t.Context(), data.Workspace.ID, member.ID)
			var realtime []domain.RealtimeEvent
			var webhooks []domain.DomainEvent
			repository.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
			repository.SetWebhookSink(func(_ string, event domain.DomainEvent) { webhooks = append(webhooks, event) })
			t.Cleanup(func() { repository.SetRealtimeSink(nil); repository.SetWebhookSink(nil) })
			start, err := repository.WorkspaceChanges(t.Context(), data.Workspace.ID, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range []struct {
				status string
				apply  func() error
			}{
				{"suspended", func() error { return repository.SuspendMember(t.Context(), data.Workspace.ID, member.ID) }},
				{"active", func() error { return repository.ResumeMember(t.Context(), data.Workspace.ID, member.ID) }},
				{"removed", func() error { return repository.RemoveMember(t.Context(), data.Workspace.ID, member.ID) }},
			} {
				if err := change.apply(); err != nil {
					t.Fatal(err)
				}
				events, _, err := repository.EventsPage(t.Context(), data.Workspace.ID, member.ID, "", 100)
				if err != nil {
					t.Fatal(err)
				}
				kind := memberStatusEvents[change.status]
				index := slices.IndexFunc(events, func(event domain.DomainEvent) bool { return event.Type == kind })
				if index < 0 {
					t.Fatalf("missing durable %s: %+v", kind, events)
				}
				event := events[index]
				var payload map[string]string
				if json.Unmarshal(event.Payload, &payload) != nil || payload["userId"] != member.ID || payload["status"] != change.status {
					t.Fatalf("status payload = %s", event.Payload)
				}
				if !slices.ContainsFunc(webhooks, func(hook domain.DomainEvent) bool { return hook.ID == event.ID }) || !slices.ContainsFunc(realtime, func(notice domain.RealtimeEvent) bool {
					return notice.ID == event.ID && string(notice.Payload) == string(event.Payload)
				}) {
					t.Fatalf("event %s did not reach both sinks", kind)
				}
			}
			page, err := repository.WorkspaceChanges(t.Context(), data.Workspace.ID, start.Head, 100)
			if err != nil || len(page.Records) != 3 {
				t.Fatalf("change log = %+v, err = %v", page, err)
			}
			if err := repository.ResumeMember(t.Context(), data.Workspace.ID, member.ID); err == nil {
				t.Fatal("removed member resumed")
			}
			if len(webhooks) != 3 || len(realtime) != 3 {
				t.Fatal("failed mutation published an event")
			}
		})
	}
}
