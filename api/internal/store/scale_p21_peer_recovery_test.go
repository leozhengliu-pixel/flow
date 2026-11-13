package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"

	"github.com/alicebob/miniredis/v2"
)

func TestScaleP21RedisRecoveryRechecksPolicyWithoutFullDecode(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	probe := scaleP21ResidentProbe(f.SQLiteStore)
	if err := probe.loadResidentWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	before, err := probe.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	redis := miniredis.RunT(t)
	config := coordination.Config{Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: idgen.New("scale-p21-"), ConnectTimeout: time.Second}
	client, err := coordination.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	publisher, err := coordination.Open(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	ctx, cancel := context.WithCancel(t.Context())
	ready, recovered, stopped := make(chan struct{}, 1), make(chan error, 1), make(chan error, 1)
	go func() {
		stopped <- client.Listen(ctx, func(event coordination.EventEnvelope) {
			if event.Workspace == workspace {
				recovered <- probe.ReloadWorkspace(ctx, workspace)
			}
		}, func(bool) { ready <- struct{}{} })
	}()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("owned Redis listener did not stop")
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("owned Redis listener did not subscribe")
	}
	team := f.data.Teams[0].ID
	if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "teams", "teamSettings"), workspace, "team.settings_updated", team, map[string]string{"access": "private"}, func(data *domain.Bootstrap) error {
		value := data.TeamSettings[team]
		value.Access = "private"
		data.TeamSettings[team] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(t.Context(), workspace, domain.RealtimeEvent{ID: idgen.New("p21-wake-"), Type: "team.settings_updated", AggregateID: team}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-recovered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Redis wake did not trigger bounded recovery")
	}
	if err := probe.ValidateAccess(t.Context(), before); !errors.Is(err, ErrAuthForbidden) {
		t.Fatalf("queued access survived peer policy change: %v", err)
	}
	current, _ := probe.storedWorkspace(workspace)
	if current.TeamSettings[team].Access != "private" || len(current.Projects) != 0 || probe.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("peer recovery lost policy or installed full metadata")
	}
	// A lost wake still converges through the same bounded catch-up entry.
	if err := probe.ReloadAllWorkspaces(t.Context()); err != nil {
		t.Fatal(err)
	}
	probe.metadataResidency.work.limit = 1
	if err := probe.ReloadWorkspace(t.Context(), workspace); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatalf("internal peer reload bypassed admission: %v", err)
	}
}
