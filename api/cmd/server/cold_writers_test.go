package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Background writers found failing with bounded metadata on by the load rig:
//   - the delivery scheduler's once-a-minute pass stopped at the Pulse
//     summary step ("bounded metadata coverage is incomplete") whenever the
//     workspace had an application member: ListMembers decorates app users
//     from owned installation reads, and the tick had no output owner;
//   - the loop output contract store ran on context.Background(), which is
//     not COLD work, so the bounded dispatch refused it ("loop.output_contract"
//     has no migrated point writer) instead of running the loop contract
//     writer on a COLD facade.
// Each is compared legacy vs a warm bounded view vs a bounded view loaded from
// SQL: the call's result, every metadata/content row, events, publications,
// and (bounded) the installed fields equal to a reload.

type scaleP21ColdWritersOutcome struct {
	Result             string
	State              scaleP21TeamCreateState
	Content            [][]any
	Webhooks, Realtime []json.RawMessage
	Installed          map[string]json.RawMessage
}

func (f scaleP21TeamCreateFixture) coldWritersRun(t *testing.T, source string, fields []string, call func(*server) string) (scaleP21ColdWritersOutcome, time.Time) {
	t.Helper()
	started := time.Now().UTC()
	// f.service, keeping the owner: a test view publishes recipient signals
	// (notification.created) through the store it was opened from, as one
	// running server's store would.
	f.reset(t, "legacy")
	owner, err := store.OpenDatabase(f.reader.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	service := &server{store: owner}
	if source != "legacy" {
		view, err := owner.BoundedMetadataViewForTesting(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if source == "warm" {
			if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), owner, f.data.Workspace.URLKey); err != nil {
				t.Fatal(err)
			}
		}
		service = &server{store: view}
	}
	workspace := f.data.Workspace.URLKey
	got := scaleP21ColdWritersOutcome{}
	var mu sync.Mutex
	sinks := []*store.SQLiteStore{service.store}
	if owner != service.store {
		sinks = append(sinks, owner)
	}
	for _, sink := range sinks {
		sink.SetWebhookSink(func(_ string, event domain.DomainEvent) {
			mu.Lock()
			defer mu.Unlock()
			got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, map[string]any{
				"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
				"payload": event.Payload, "previousValues": event.PreviousValues, "snapshot": event.Snapshot,
			}))
		})
		sink.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
			mu.Lock()
			defer mu.Unlock()
			got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, map[string]any{
				"type": event.Type, "aggregateId": event.AggregateID, "actorId": event.ActorID,
				"clientId": event.ClientID, "payload": event.Payload,
			}))
		})
	}
	prior := map[any]bool{}
	for _, event := range f.state(t).Events {
		prior[event[0]] = true
	}
	got.Result = call(service)
	mu.Lock()
	for _, sink := range sinks {
		sink.SetWebhookSink(nil)
		sink.SetRealtimeSink(nil)
	}
	mu.Unlock()
	got.State = f.state(t)
	var events [][]any
	for _, event := range got.State.Events {
		if !prior[event[0]] {
			events = append(events, event[1:])
		}
	}
	got.State.Events = events
	slices.SortFunc(got.Webhooks, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	slices.SortFunc(got.Realtime, func(a, b json.RawMessage) int { return strings.Compare(string(a), string(b)) })
	got.Content = f.rows(t, `SELECT kind,resource_id,id,owner_id,status,data FROM workspace_content_records WHERE workspace_key=? AND kind LIKE 'notification%' ORDER BY kind,resource_id,id`, workspace)
	if service.store.BoundedMetadataEnabled() && service.store.MetadataWorkUsedForTesting() != 0 {
		t.Fatalf("%s: retained metadata work %d", source, service.store.MetadataWorkUsedForTesting())
	}
	got.Installed = scaleP21SW294Image(t, service.store, workspace, fields...)
	if service.store.BoundedMetadataEnabled() {
		reloaded, err := store.OpenDatabase(f.reader.config)
		if err != nil {
			t.Fatal(err)
		}
		fresh := scaleP21SW294Image(t, reloaded, workspace, fields...)
		if err := reloaded.Close(); err != nil {
			t.Fatal(err)
		}
		for _, name := range fields {
			want, have := scaleP21TriageNormalize(t, fresh[name], started), scaleP21TriageNormalize(t, got.Installed[name], started)
			if want != have {
				t.Errorf("%s: installed %s is not the reloaded one: %s", source, name, scaleP21M2Diff(want, have))
			}
		}
	}
	return got, started
}

func (f scaleP21TeamCreateFixture) coldWritersDifferential(t *testing.T, name string, fields []string, setup func(*testing.T), call func(*server) string) []scaleP21ColdWritersOutcome {
	t.Helper()
	f.scaleP21TriageWriterFixture.prepare = f.prepare(setup)
	defer func() { f.scaleP21TriageWriterFixture.prepare = nil }()
	var normalized []string
	var outcomes []scaleP21ColdWritersOutcome
	for _, source := range []string{"legacy", "warm", "reloaded"} {
		got, started := f.coldWritersRun(t, source, fields, call)
		outcomes = append(outcomes, got)
		normalized = append(normalized, scaleP21TriageNormalize(t, got, started))
		if len(normalized) > 1 && normalized[len(normalized)-1] != normalized[0] {
			t.Errorf("%s: %s changed: %s", name, source, scaleP21M2Diff(normalized[0], normalized[len(normalized)-1]))
		}
	}
	return outcomes
}

func TestScaleP21ColdWritersPulseSchedulerWithApplicationMembers(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	repo, workspace, user := f.repository, f.data.Workspace.URLKey, f.data.Viewer
	if len(f.data.Teams) == 0 {
		t.Fatal("fixture has no team")
	}
	const client = "cold-writers-client"
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	// An installed application: its app user is an active workspace member.
	installApplication := func(t *testing.T) {
		f.mutate(t, "scale_p21.cold_writers_app", func(data *domain.Bootstrap) {
			data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
				ID: "cold-writers-app", ClientID: client, ClientSecret: "cold-writers-secret", Name: "Cold Writers App", Scopes: []string{"read", "write", "app:assignable"},
				SupportsClientCredentials: true, CreatorID: user.ID, RedirectURIs: []string{"https://example.test/callback"}, CreatedAt: at, UpdatedAt: at,
			})
		})
		if _, err := repo.InstallApplication(t.Context(), workspace, domain.ApplicationInstallation{ClientID: client, Name: "Cold Writers App", AvatarURL: "https://example.test/app.png",
			InstalledBy: user.ID, Scopes: []string{"read", "write"}, TeamIDs: []string{f.data.Teams[0].ID}, Active: true}, ""); err != nil {
			t.Fatal(err)
		}
		if len(f.rows(t, `SELECT user_id FROM workspace_memberships WHERE workspace_id=? AND role='app' AND status='active'`, f.data.Workspace.ID)) == 0 {
			t.Fatal("the installed application has no active member")
		}
	}
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
	notificationID := "pulse_" + user.ID + "_" + strconv.FormatInt(end.Unix(), 10)
	projectID := "cold-writers-pulse-" + user.ID
	setup := func(schedule string) func(*testing.T) {
		return func(t *testing.T) {
			installApplication(t)
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.cold_writers_pulse", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.FeatureFlags["pulse"] = true
				data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
				settings := data.UserSettings[user.ID]
				settings.PulseSchedule, settings.Timezone = schedule, "UTC"
				data.UserSettings[user.ID] = settings
				data.Projects = slices.DeleteFunc(data.Projects, func(project domain.Project) bool { return project.ID == projectID })
				data.Projects = append(data.Projects, domain.Project{
					ID: projectID, Name: "Cold writers project", TeamIDs: []string{f.data.Teams[0].ID}, MemberIDs: []string{user.ID},
				})
				data.ProjectUpdates[projectID] = []domain.ProjectUpdate{{
					ID: "cold-writers-update", ProjectID: projectID, Body: "Scheduler update",
					User: domain.User{ID: "cold-writers-writer", Name: "Writer", DisplayName: "Writer"}, CreatedAt: now.Add(-5 * time.Hour),
				}}
				if data.Settings == nil {
					data.Settings = map[string]any{}
				}
				data.Settings["pulseDeliveryCursors"] = map[string]time.Time{user.ID: end.Add(-24 * time.Hour)}
				data.Notifications = slices.DeleteFunc(data.Notifications, func(item domain.Notification) bool { return item.ID == notificationID })
				data.NotificationDeliveries = slices.DeleteFunc(data.NotificationDeliveries, func(item domain.NotificationDelivery) bool {
					return item.NotificationID == notificationID
				})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	tick := func(s *server) string {
		ctx := store.WithColdWork(t.Context(), "delivery-scheduler")
		first := s.preparePulseSummaries(ctx, workspace, now)
		// A second pass of the same window writes nothing.
		second := s.preparePulseSummaries(ctx, workspace, now)
		return fmt.Sprint(first, " ", second)
	}
	for _, schedule := range []string{"daily", "never"} {
		outcomes := f.coldWritersDifferential(t, "pulse-"+schedule, []string{"settings", "userSettings"}, setup(schedule), tick)
		if outcomes[0].Result != "<nil> <nil>" {
			t.Fatalf("pulse-%s: legacy scheduler pass failed: %s", schedule, outcomes[0].Result)
		}
		written := false
		for _, row := range outcomes[0].Content {
			written = written || fmt.Sprint(row[2]) == notificationID
		}
		if written != (schedule == "daily") {
			t.Fatalf("pulse-%s: summary written %v", schedule, written)
		}
	}
}

func TestScaleP21ColdWritersLoopContractStore(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	// Stored contracts carry UTC times, as the server writes them.
	scaleP21ColdWritersLoopContractStore(t, scaleP21TeamCreateSetup(t), time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
}

// A stored contract time with another RFC 3339 offset (older data, or a
// database driver's rendering) decodes on the COLD facade as the legacy
// decode reads it: the contract store reads the person contract's time.
func TestScaleP21ColdWritersLoopContractStoreOffsetTimes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP21TeamCreateSetup(t)
	for _, offset := range []int{0, 8 * 3600, -(3*3600 + 30*60), 5*3600 + 45*60} {
		t.Run(strconv.Itoa(offset), func(t *testing.T) {
			scaleP21ColdWritersLoopContractStore(t, f, time.Date(2026, 11, 13, 12, 0, 0, 0, time.FixedZone("", offset)))
		})
	}
}

func scaleP21ColdWritersLoopContractStore(t *testing.T, f scaleP21TeamCreateFixture, extracted time.Time) {
	t.Helper()
	repo, workspace := f.repository, f.data.Workspace.URLKey
	const loopID, otherID = "cold-writers-loop", "cold-writers-other"
	base := domain.Loop{ID: loopID, Name: "Contract loop", Instructions: "Summarize the open bugs", Status: "active", TriggerType: "manual", ConnectorIDs: []string{}}
	hash := loopContractHashOf(base)
	model := &domain.LoopOutputContract{Source: loopContractModel, InstructionsHash: hash, ReadOnly: true, Outputs: []domain.LoopContractOutput{}, ExtractedAt: &extracted}
	person := &domain.LoopOutputContract{Source: loopContractPerson, InstructionsHash: "older", Outputs: []domain.LoopContractOutput{}, ExtractedAt: &extracted}
	setup := func(loop domain.Loop, versions ...domain.LoopVersion) func(*testing.T) {
		return func(t *testing.T) {
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.cold_writers_loop", "", nil, func(data *domain.Bootstrap) error {
				drop := func(id string) bool { return id == loopID || id == otherID }
				data.Loops = slices.DeleteFunc(data.Loops, func(item domain.Loop) bool { return drop(item.ID) })
				data.LoopVersions = slices.DeleteFunc(data.LoopVersions, func(item domain.LoopVersion) bool { return drop(item.LoopID) })
				if loop.ID != "" {
					data.Loops = append(data.Loops, loop)
				}
				data.Loops = append(data.Loops, domain.Loop{ID: otherID, Name: "Unrelated loop", Instructions: "unrelated\x00instructions", Status: "active", TriggerType: "manual"})
				data.LoopVersions = append(data.LoopVersions, versions...)
				data.LoopVersions = append(data.LoopVersions, domain.LoopVersion{ID: otherID + "-v1", LoopID: otherID, Version: 1, Current: true,
					Definition: domain.LoopDefinition{Name: "Unrelated loop", Instructions: "unrelated\x00instructions"}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	current := domain.LoopVersion{ID: loopID + "-v2", LoopID: loopID, Version: 2, Current: true, Definition: domain.LoopDefinition{Name: base.Name, Instructions: base.Instructions}}
	older := domain.LoopVersion{ID: loopID + "-v1", LoopID: loopID, Version: 1, Definition: domain.LoopDefinition{Name: base.Name, Instructions: "older text"}}
	personal := base
	personal.OutputContract = person
	for _, test := range []struct {
		name     string
		setup    func(*testing.T)
		contract *domain.LoopOutputContract
		changed  bool
	}{
		{"model-contract", setup(base, older, current), model, true},
		{"person-outputs-kept", setup(personal, current), model, true},
		{"stale-hash", setup(func() domain.Loop { loop := base; loop.Instructions = "edited since"; return loop }(), current), model, true},
		{"fallback", setup(base, current), &domain.LoopOutputContract{Source: loopContractFallback, InstructionsHash: hash, Error: "no model", Outputs: []domain.LoopContractOutput{}, ExtractedAt: &extracted}, true},
		{"missing-loop", setup(domain.Loop{}), model, false},
	} {
		outcomes := f.coldWritersDifferential(t, test.name, []string{"loops", "loopVersions"}, test.setup, func(s *server) string {
			s.storeLoopContract(workspace, loopID, hash, test.contract)
			return "stored"
		})
		if (len(outcomes[0].State.Events) != 0) != test.changed {
			t.Fatalf("%s: legacy store wrote events %v, want a write %v", test.name, outcomes[0].State.Events, test.changed)
		}
	}
}
