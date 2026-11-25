package main

import (
	"net/http"
	"strconv"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21BoundedPeerActivation(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "on")
	t.Setenv("FLOW_METADATA_CATALOG", "off")
	t.Setenv("FLOW_SYNC_V2", "on")
	config := scaleP03DatabaseConfig(t)
	open := func() *store.SQLiteStore {
		t.Helper()
		repo, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.Close() })
		return repo
	}
	seed := open()
	fixture := scaleP03Fixture(t, seed)
	workspace, user, team := fixture.data.Workspace.URLKey, fixture.data.Viewer, fixture.data.Teams[0].ID
	project := idgen.New("peer-project-")
	if err := seed.MutateWorkspace(t.Context(), workspace, "test.peer_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: project, Name: "Before", TeamIDs: []string{team}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, err := seed.PrepareBoundedMetadata(t.Context())
	if err != nil || !report.Ready {
		t.Fatalf("metadata preparation failed: ready=%t, error=%v", report.Ready, err)
	}
	_, token, err := seed.Login(t.Context(), user.Email, "p03-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	fixture.host.Close()

	t.Setenv("FLOW_BOUNDED_METADATA", "on")
	writer, peer := open(), open()
	receiver := &server{store: peer, realtime: newRealtimeHub()}
	if !writer.BoundedMetadataEnabled() || !peer.BoundedMetadataEnabled() ||
		receiver.recordPeerSyncMode() != "on" || !receiver.store.SyncV2Enabled() {
		t.Fatal("replicas did not activate the full flag set")
	}
	if err := receiver.syncPeerWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	var published domain.RealtimeEvent
	writer.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { published = event })
	f := scaleP21M2Fixture{scaleP03PlatformFixture: fixture, testingT: t}
	w := scaleP21ProjectUpdateServe(f, writer, token, user, nil, project, map[string]string{"name": "After"})
	if w.Code != http.StatusOK || published.Type != "project.updated" {
		t.Fatalf("writer did not publish the project edit: status=%d", w.Code)
	}
	state, err := peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil || state.Current {
		t.Fatalf("peer should start behind the writer: current=%t error=%v", state.Current, err)
	}
	cursor, err := writer.RecordJournalEventSeq(t.Context(), published.ID)
	if err != nil {
		t.Fatal(err)
	}
	head, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || head <= 0 {
		t.Fatal("project edit did not have a journal head")
	}
	receiver.remoteEvents.push(workspace, domain.RealtimeEvent{ID: published.ID, Seq: head, Type: peerHeadEventType}, true, func(string) {})
	receiver.applyRemoteEvents(workspace)
	if !receiver.peerEventPublished(workspace, published.ID) {
		t.Fatal("peer did not publish the durable event")
	}
	state, err = peer.WorkspacePeerState(t.Context(), workspace)
	if err != nil || !state.Current {
		t.Fatalf("peer did not become current: current=%t error=%v", state.Current, err)
	}
	read, found, err := peer.BeginInstalledMetadataRead(t.Context(), workspace)
	if err != nil || !found {
		t.Fatal("peer could not open its installed generation", err)
	}
	defer read.Close()
	selection, err := read.Select(t.Context(), store.MetadataRecordScope{Read: []store.RecordKey{{Kind: "projects", ID: project}}})
	if err != nil {
		t.Fatal(err)
	}
	if projects := selection.Data.Projects; len(projects) != 1 || projects[0].Name != "After" {
		t.Fatal("peer published the edit with the old installed generation")
	}
}
