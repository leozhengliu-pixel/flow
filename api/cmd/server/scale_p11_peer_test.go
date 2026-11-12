package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/coordination"
	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/alicebob/miniredis/v2"
)

type scaleP11Fixture struct {
	writer, peer *store.SQLiteStore
	service      *server
	workspace    string
	owner        domain.User
	redis        *miniredis.Miniredis
	config       store.DatabaseConfig
}

func scaleP11NewFixture(t *testing.T, records int) scaleP11Fixture {
	t.Helper()
	config := store.DatabaseConfig{Driver: os.Getenv("FLOW_TEST_DATABASE_DRIVER"), URL: os.Getenv("FLOW_TEST_DATABASE_URL"), MaxOpenConns: 4}
	if (config.Driver == "") != (config.URL == "") {
		t.Fatal("both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL are required")
	}
	if config.Driver == "" {
		config.Driver, config.Path = "sqlite", filepath.Join(t.TempDir(), "p11.db")
	} else if config.Driver == "sqlite" {
		config.Path = config.URL
	}
	writer, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	owner, _, err := writer.Register(t.Context(), "P11 owner", idgen.New("p11-")+"@example.test", "p11-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	workspace := idgen.New("p11-")
	ctx := store.ContextWithActor(t.Context(), owner)
	if _, err := writer.CreateWorkspace(ctx, "P11 workspace", workspace, "us"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := writer.DeleteWorkspace(ctx, workspace); err != nil {
			t.Error(err)
		}
		if err := writer.EraseAccount(ctx, owner.ID); err != nil {
			t.Error(err)
		}
	})
	if records > 0 {
		body := strings.Repeat("p11 canonical body ", 512)
		if err := writer.MutateWorkspace(ctx, workspace, "test.p11_seed", "", nil, func(data *domain.Bootstrap) error {
			for i := range records {
				data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p11-project-%d", i), Name: "P11 project", Description: body})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	peer, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	redis := miniredis.RunT(t)
	coordinator, err := coordination.Open(t.Context(), coordination.Config{Mode: "standalone", Addrs: []string{redis.Addr()}, Prefix: idgen.New("p11"), ConnectTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	service := &server{store: peer, coordinator: coordinator, realtime: newRealtimeHub()}
	if recordPeerSyncMode() != "off" {
		if err := service.syncPeerWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		state, err := peer.WorkspacePeerState(t.Context(), workspace)
		if err != nil || service.remoteEvents.consumer(workspace).cursor != state.Head {
			t.Fatalf("initial peer cursor=%d SQL head=%d error=%v", service.remoteEvents.consumer(workspace).cursor, state.Head, err)
		}
	}
	return scaleP11Fixture{writer, peer, service, workspace, owner, redis, config}
}

// Only the receiving replica's work is measured. Writer cost and fixture setup
// are excluded; the canonical body bytes and measurement method stay fixed.
func TestScaleP11PeerAllocation(t *testing.T) {
	f := scaleP11NewFixture(t, 256)
	var allocated uint64
	var elapsed time.Duration
	beforeDecodes := f.peer.WorkspaceMetadataDecodes()
	for i := range 5 {
		var event domain.RealtimeEvent
		f.writer.SetRealtimeSink(func(_ string, e domain.RealtimeEvent) { event = e })
		if err := f.writer.MutateWorkspace(store.ContextWithActor(t.Context(), f.owner), f.workspace, "project.updated", "p11-project-0", map[string]string{"name": fmt.Sprint(i)}, func(data *domain.Bootstrap) error {
			data.Projects[0].Name = fmt.Sprint(i)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		event.Seq = f.writer.DomainEventSeq(t.Context(), event.ID)
		f.service.remoteEvents.push(f.workspace, event, func(string) {})
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		started := time.Now()
		f.service.applyRemoteEvents(f.workspace)
		elapsed += time.Since(started)
		runtime.ReadMemStats(&after)
		allocated += after.TotalAlloc - before.TotalAlloc
	}
	t.Logf("peer: records=256 bodyBytesEach=%d operations=5 bytes/op=%d duration/op=%s fullDecodes=%d", len(strings.Repeat("p11 canonical body ", 512)), allocated/5, elapsed/5, f.peer.WorkspaceMetadataDecodes()-beforeDecodes)
	if recordPeerSyncMode() == "on" {
		if allocated/5 > 2<<20 || f.peer.WorkspaceMetadataDecodes() != beforeDecodes {
			t.Fatal("small peer replacement exceeded 2 MiB or decoded the full workspace")
		}
	}
}

func scaleP11Rename(t *testing.T, f scaleP11Fixture, name string) domain.RealtimeEvent {
	t.Helper()
	var event domain.RealtimeEvent
	f.writer.SetRealtimeSink(func(_ string, e domain.RealtimeEvent) { event = e })
	if err := f.writer.MutateWorkspace(store.ContextWithActor(t.Context(), f.owner), f.workspace, "project.updated", "p11-project-0", map[string]string{"name": name}, func(data *domain.Bootstrap) error {
		data.Projects[0].Name = name
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	event.Seq = f.writer.DomainEventSeq(t.Context(), event.ID)
	return event
}

func scaleP11Name(t *testing.T, f scaleP11Fixture) string {
	t.Helper()
	data, ok := f.peer.WorkspaceMetadata(f.workspace)
	if !ok || len(data.Projects) == 0 {
		t.Fatal("peer has no project")
	}
	return data.Projects[0].Name
}

func TestScaleP11QueueBoundsAndCoalescesHeads(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	var q remoteEventQueue
	payload := []byte(strings.Repeat("p", 1<<20))
	for i := range 10000 {
		q.push("w", domain.RealtimeEvent{Seq: int64(i + 1), Payload: payload}, func(string) {})
	}
	q.push("w", domain.RealtimeEvent{Seq: 1, Payload: payload}, func(string) {})
	batch := q.take("w")
	if len(batch) != 1 || batch[0].Seq != 10000 || len(batch[0].Payload) != 0 {
		t.Fatal("durable queue retained payloads or moved its head backwards")
	}
	for range 1000 {
		q.push("w", domain.RealtimeEvent{Type: "agent_task.updated", Payload: payload}, func(string) {})
	}
	if q.bytes["w"] > remoteEventQueueMaxBytes || len(q.pending["w"]) > remoteEventQueueMaxEvents {
		t.Fatal("side-channel queue exceeded its budget")
	}
	batch = q.take("w")
	if len(batch) != 1 || batch[0].Type != "workspace.resync_required" || len(batch[0].Payload) != 0 {
		t.Fatal("overflow did not replace the incomplete batch with a small resync")
	}
}

func TestScaleP11RedisLossDuplicationAndReordering(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	f := scaleP11NewFixture(t, 1)
	events := []domain.RealtimeEvent{scaleP11Rename(t, f, "first"), scaleP11Rename(t, f, "second"), scaleP11Rename(t, f, "third")}
	for i := range events {
		events[i].ClientID = fmt.Sprintf("p11-client-%d", i)
	}
	channel, stop := f.service.realtime.subscribe(f.workspace)
	defer stop()
	for _, index := range []int{2, 0, 2, 1, 0} {
		f.service.remoteEvents.push(f.workspace, events[index], func(string) {})
	}
	f.service.applyRemoteEvents(f.workspace)
	if scaleP11Name(t, f) != "third" {
		t.Fatal("peer did not converge to the latest committed record")
	}
	got := drainRealtime(channel)
	if len(got) != len(events) {
		t.Fatalf("got %d events, want %d complete persisted changes", len(got), len(events))
	}
	for i := range events {
		if got[i].ID != events[i].ID || got[i].Seq != events[i].Seq || got[i].ClientID != events[i].ClientID {
			t.Fatal("peer published duplicated or reordered events")
		}
	}
	// No Redis notification at all; polling still installs and publishes.
	lost := scaleP11Rename(t, f, "lost publish")
	f.redis.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); f.service.pollPeerChanges(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case event := <-channel:
		if event.ID != lost.ID && event.Type != "workspace.resync_required" || scaleP11Name(t, f) != "lost publish" {
			t.Fatal("poll published before installing the complete transaction")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("missed publish did not converge through SQL polling")
	}
}

func TestScaleP11FreshnessWithoutRedisAndFailureClosed(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "shadow")
	f := scaleP11NewFixture(t, 1)
	scaleP11Rename(t, f, "commit without Redis")
	f.redis.Close()
	if err := f.service.ensureCurrentWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "commit without Redis" {
		t.Fatalf("SQL freshness during Redis outage failed: %v", err)
	}
	if err := f.peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ensureCurrentWorkspace(t.Context(), f.workspace); err == nil {
		t.Fatal("freshness silently accepted an unavailable authority")
	}
}

func TestScaleP11ReloadWaitReturnsTimeoutAndSharedError(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(fmt.Sprint(timeout), func(t *testing.T) {
			load := &workspaceReload{done: make(chan struct{}), err: errors.New("failed snapshot")}
			s := &server{workspaceVersions: workspaceVersions{loading: map[string]*workspaceReload{"w": load}}}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			if !timeout {
				close(load.done)
			}
			err := s.reloadWorkspaceTo(ctx, "w", 0)
			if timeout && !errors.Is(err, context.DeadlineExceeded) || !timeout && !errors.Is(err, load.err) {
				t.Fatalf("reload wait returned %v", err)
			}
		})
	}
}

func TestScaleP11OldWriterAndTombstoneFallback(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	f := scaleP11NewFixture(t, 1)
	t.Setenv("FLOW_RECORD_JOURNAL", "off")
	scaleP11Rename(t, f, "old writer")
	t.Setenv("FLOW_RECORD_JOURNAL", "shadow")
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "old writer" {
		t.Fatalf("old writer was skipped: %v", err)
	}
	if err := f.writer.MutateWorkspace(t.Context(), f.workspace, "project.deleted", "p11-project-0", nil, func(data *domain.Bootstrap) error {
		data.Projects = slices.DeleteFunc(data.Projects, func(p domain.Project) bool { return p.ID == "p11-project-0" })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil {
		t.Fatal(err)
	}
	data, ok := f.peer.WorkspaceMetadata(f.workspace)
	if !ok || len(data.Projects) != 0 {
		t.Fatal("deleted project survived fallback")
	}
	// Delayed old notifications are hints; they cannot resurrect the record.
	f.service.remoteEvents.push(f.workspace, domain.RealtimeEvent{Seq: 1, Type: "project.updated"}, func(string) {})
	f.service.applyRemoteEvents(f.workspace)
	data, _ = f.peer.WorkspaceMetadata(f.workspace)
	if len(data.Projects) != 0 {
		t.Fatal("old head resurrected a tombstone")
	}
}

func TestScaleP11FlagDefaultsToShadow(t *testing.T) {
	for value, want := range map[string]string{"": "shadow", "unknown": "shadow", "off": "off", "0": "off", "false": "off", " on ": "on", "shadow": "shadow"} {
		t.Setenv("FLOW_RECORD_PEER_SYNC", value)
		if recordPeerSyncMode() != want {
			t.Fatalf("mode %q != %q", recordPeerSyncMode(), want)
		}
	}
}

func TestScaleP11LargeCollectionKeepsExactFallback(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	f := scaleP11NewFixture(t, 0)
	count := (1<<20)/int(reflect.TypeFor[domain.Project]().Size()) + 1
	if err := f.writer.MutateWorkspace(t.Context(), f.workspace, "test.p11_large", "", nil, func(data *domain.Bootstrap) error {
		for i := range count {
			data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p11-project-%d", i), Name: "before", Description: "small body"})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil {
		t.Fatal(err)
	}
	before := f.peer.WorkspaceMetadataDecodes()
	scaleP11Rename(t, f, "large collection fallback")
	if err := f.service.syncPeerWorkspace(t.Context(), f.workspace); err != nil || scaleP11Name(t, f) != "large collection fallback" {
		t.Fatalf("large collection lost the committed replacement: %v", err)
	}
	if f.peer.WorkspaceMetadataDecodes() <= before {
		t.Fatal("large collection unexpectedly bypassed the compatibility snapshot")
	}
}

func TestScaleP11ClientHintsAreBounded(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	var queue remoteEventQueue
	for i := range 10000 {
		queue.push("w", domain.RealtimeEvent{ID: fmt.Sprint(i), Seq: int64(i + 1), ClientID: "p11-client", Payload: []byte("discarded")}, func(string) {})
	}
	if queue.clientBytes["w"] > peerClientHintMaxBytes || len(queue.clients["w"]) > peerClientHintMaxEvents || !queue.hintOverflow["w"] {
		t.Fatal("client correlation grew without a bound or hid its overflow")
	}
	if batch := queue.take("w"); len(batch) != 1 || len(batch[0].Payload) != 0 {
		t.Fatal("client correlation retained an event payload queue")
	}
}
