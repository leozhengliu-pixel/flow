package main

import (
	"encoding/json"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP16PermissionSideEffectsDifferential(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	scaleP16PermissionMatrix(t, true)
}

type scaleP16PermissionProbe struct {
	repo                   *store.SQLiteStore
	workspace, workspaceID string
	baseline               []byte
	events, webhooks       []json.RawMessage
	auditSeq               int64
}

func newScaleP16PermissionProbe(t *testing.T, repo *store.SQLiteStore, workspace string, enabled bool) *scaleP16PermissionProbe {
	t.Helper()
	if !enabled {
		return nil
	}
	data, _ := repo.WorkspaceMetadataFields(workspace, "projects", "initiatives", "favorites")
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	p := &scaleP16PermissionProbe{repo: repo, workspace: workspace, workspaceID: data.Workspace.ID, baseline: raw}
	repo.SetRealtimeSink(func(key string, event domain.RealtimeEvent) {
		if key != workspace {
			return
		}
		raw, _ := json.Marshal([]any{event.Type, event.AggregateID, event.ActorID, event.Payload})
		p.events = append(p.events, raw)
	})
	repo.SetWebhookSink(func(key string, event domain.DomainEvent) {
		if key != workspace {
			return
		}
		raw, _ := json.Marshal([]any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
		p.webhooks = append(p.webhooks, raw)
	})
	return p
}

func (p *scaleP16PermissionProbe) reset(t *testing.T) {
	t.Helper()
	var data domain.Bootstrap
	if err := json.Unmarshal(p.baseline, &data); err != nil {
		t.Fatal(err)
	}
	if err := p.repo.MutateWorkspace(workspaceFixtureContext(t.Context()), p.workspace, "scale_p16.reset", "", nil, func(next *domain.Bootstrap) error {
		next.Projects, next.Initiatives, next.Favorites = data.Projects, data.Initiatives, data.Favorites
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.events, p.webhooks = nil, nil
	entries, err := p.repo.ListAuditEntries(t.Context(), p.workspaceID, store.AuditQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	p.auditSeq = 0
	if len(entries) > 0 {
		p.auditSeq = entries[0].Seq
	}
}

func (p *scaleP16PermissionProbe) result(t *testing.T) map[string]string {
	t.Helper()
	data, _ := p.repo.BootstrapFor(p.workspace)
	entries, err := p.repo.ListAuditEntries(t.Context(), p.workspaceID, store.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var audit []any
	for _, entry := range entries {
		if entry.Seq <= p.auditSeq {
			continue
		}
		audit = append(audit, []any{entry.ActorType, entry.ActorID, entry.Actor, entry.Action, entry.ResourceType, entry.ResourceID, entry.Result, entry.Metadata})
	}
	result := make(map[string]string, 8)
	for name, value := range map[string]any{
		"projects": data.Projects, "initiatives": data.Initiatives, "favorites": data.Favorites,
		"notifications": data.Notifications, "deliveries": data.NotificationDeliveries,
		"audit": audit, "realtime": p.events, "webhooks": p.webhooks,
	} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		result[name] = scaleP04Normalize(raw)
	}
	return result
}
