package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21PulseSummaryCandidates(prefix string, count int, notification bool) []PulseSummaryCandidate {
	end := time.Date(2026, 11, 16, 6, 0, 0, 0, time.UTC)
	result := make([]PulseSummaryCandidate, count)
	for index := range result {
		id := fmt.Sprintf("%s-user-%04d", prefix, index)
		result[index] = PulseSummaryCandidate{UserID: id, Schedule: "daily", End: end}
		if notification {
			result[index].Notification = &domain.Notification{
				ID: fmt.Sprintf("pulse_%s_%d", id, end.Unix()), RecipientID: id, Type: "pulseSummary", SourceType: "pulse", SourceID: end.Format(time.RFC3339),
				Category: "pulse", OccurrenceCount: 2, Title: "Daily Pulse", Text: "Complete summary", Actor: domain.User{ID: "flow", Name: "Flow", DisplayName: "Flow"},
				CreatedAt: end.Add(4 * time.Hour), UpdatedAt: end.Add(4 * time.Hour),
				Payload: &domain.NotificationPayload{Schedule: "daily", WindowEnd: &end, Total: 2,
					UpdateIDs: []string{"first", "second"}, Updates: []domain.PulseUpdateRef{{ID: "first", Kind: "project", SourceID: "source", Source: "Complete source", At: end.Add(-time.Hour)}}},
			}
		}
	}
	return result
}

func scaleP21PulseSummaryPrepare(input PulseSummaryDispatchInput) (PulseSummaryDispatch, error) {
	prefs := input.Preferences
	if !input.HasPreferences {
		prefs.Inbox.Enabled, prefs.Email.Enabled, prefs.Desktop.Enabled = true, true, true
		prefs.EmailFormat = "digest"
	}
	category := func(channel domain.NotificationChannelPreferences) bool {
		enabled, exists := channel.Categories["pulse"]
		return channel.Enabled && (!exists || enabled)
	}
	result := PulseSummaryDispatch{Send: category(prefs.Inbox)}
	if !result.Send {
		return result, nil
	}
	appendDelivery := func(channel, status string) {
		item := domain.NotificationDelivery{
			ID: "delivery_" + input.Notification.ID + "_" + channel, NotificationID: input.Notification.ID, RecipientID: input.Notification.RecipientID,
			Channel: channel, Status: status, CreatedAt: input.Notification.UpdatedAt, UpdatedAt: input.Notification.UpdatedAt,
		}
		if status == "digest" {
			due := input.Notification.UpdatedAt.Add(time.Hour)
			item.NextAttemptAt = &due
		}
		result.Deliveries = append(result.Deliveries, item)
	}
	if category(prefs.Email) {
		status := "pending"
		if prefs.EmailFormat == "digest" {
			status = "digest"
		}
		appendDelivery("email", status)
	}
	if category(prefs.Desktop) {
		appendDelivery("desktop", "pending")
	}
	if prefs.Chat != nil && category(*prefs.Chat) {
		appendDelivery("chat", "pending")
	}
	if input.PushEnabled && category(prefs.Desktop) {
		appendDelivery("push", "pending")
	}
	return result, nil
}

func scaleP21PulseSummarySeed(t *testing.T, f scaleP09Fixture, candidates []PulseSummaryCandidate, cursorCount int) {
	t.Helper()
	err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings", "settings", "userSettings", "notificationPreferences", "pushSubscriptions", "projects"),
		f.data.Workspace.URLKey, "pulse.summary_scheduled", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["pulse"] = true
			data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule = "daily"
			cursors := map[string]time.Time{}
			for index := 0; index < cursorCount; index++ {
				cursors[fmt.Sprintf("retained-user-%08d", index)] = candidates[0].End.Add(-24 * time.Hour)
			}
			data.Settings["pulseDeliveryCursors"] = cursors
			data.Settings["summary-unrelated"] = strings.Repeat("unrelated settings ", 40000)
			data.Projects = append(data.Projects, domain.Project{ID: "summary-unrelated", Description: strings.Repeat("unrelated project ", 40000)})
			for _, candidate := range candidates {
				data.UserSettings[candidate.UserID] = domain.UserSettings{UserID: candidate.UserID, PulseSchedule: "default", AgentInstructions: strings.Repeat("ignored ", 1000)}
			}
			if len(candidates) > 1 {
				data.NotificationPreferences[candidates[0].UserID] = domain.NotificationPreferences{
					UserID: "different-native-map-key", Inbox: domain.NotificationChannelPreferences{Enabled: true},
					Email:   domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{}},
					Desktop: domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{"pulse": true, "unrelated": false}},
					Chat:    &domain.NotificationChannelPreferences{Enabled: true, Categories: map[string]bool{}}, EmailFormat: "immediate",
				}
				data.PushSubscriptions = append(data.PushSubscriptions, domain.PushSubscription{ID: "summary-push", UserID: candidates[0].UserID, Enabled: true, Endpoint: strings.Repeat("ignored endpoint ", 40000)})
			}
			data.Notifications = append(data.Notifications, domain.Notification{
				ID: "summary-unrelated-notification", RecipientID: "summary-unrelated-user", Actor: domain.User{ID: "flow"}, Title: "Retained", Text: strings.Repeat("unrelated content ", 40000),
				CreatedAt: candidates[0].End.Add(-48 * time.Hour), UpdatedAt: candidates[0].End.Add(-48 * time.Hour),
			})
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

func scaleP21PulseSummaryLegacy(ctx context.Context, f *SQLiteStore, workspace string, candidates []PulseSummaryCandidate, prepare func(PulseSummaryDispatchInput) (PulseSummaryDispatch, error)) error {
	ctx = WithMetadataFields(ctx, "settings", "workspaceSettings", "userSettings", "notificationPreferences", "pushSubscriptions")
	return f.MutateWorkspace(ctx, workspace, "pulse.summary_scheduled", "", nil, func(data *domain.Bootstrap) error {
		if enabled, configured := data.WorkspaceSettings.FeatureFlags["pulse"]; configured && !enabled {
			return ErrNoMutation
		}
		current := map[string]time.Time{}
		raw, _ := json.Marshal(data.Settings["pulseDeliveryCursors"])
		_ = json.Unmarshal(raw, &current)
		if current == nil {
			current = map[string]time.Time{}
		}
		changed := false
		for _, candidate := range candidates {
			if !current[candidate.UserID].Before(candidate.End) {
				continue
			}
			schedule := data.UserSettings[candidate.UserID].PulseSchedule
			if schedule == "" || schedule == "default" {
				schedule = data.WorkspaceSettings.FeatureSettings.PulseWorkspaceSchedule
			}
			if schedule != candidate.Schedule {
				continue
			}
			if candidate.Notification != nil {
				prefs, found := data.NotificationPreferences[candidate.UserID]
				push := false
				for _, subscription := range data.PushSubscriptions {
					push = push || subscription.UserID == candidate.UserID && subscription.Enabled
				}
				dispatch, err := prepare(PulseSummaryDispatchInput{Notification: *candidate.Notification, Preferences: prefs, HasPreferences: found, PushEnabled: push})
				if err != nil {
					return err
				}
				if dispatch.Send {
					data.Notifications = append(data.Notifications, *candidate.Notification)
					data.NotificationDeliveries = append(data.NotificationDeliveries, dispatch.Deliveries...)
				}
			}
			current[candidate.UserID] = candidate.End
			changed = true
		}
		if !changed {
			return ErrNoMutation
		}
		if data.Settings == nil {
			data.Settings = map[string]any{}
		}
		data.Settings["pulseDeliveryCursors"] = current
		return nil
	})
}

func scaleP21PulseSummaryRows(t *testing.T, f scaleP09Fixture) []string {
	t.Helper()
	rows, err := f.db.QueryContext(t.Context(), `SELECT kind,id,data,owner_id,parent_id,status,next_attempt_at FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,id`, f.data.Workspace.URLKey)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var kind, id, owner, parent, status, next string
		var raw []byte
		if err := rows.Scan(&kind, &id, &raw, &owner, &parent, &status, &next); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal([]any{kind, id, json.RawMessage(raw), owner, parent, status, next})
		result = append(result, string(value))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21PulseSummaryCursor(t *testing.T, repo *SQLiteStore, workspace string) []byte {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='settings' AND record_key='pulseDeliveryCursors'`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21PulseSummaryWriterDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	candidates := scaleP21PulseSummaryCandidates(idgen.New("summary-differential-"), 4, true)
	candidates[3].Notification = nil
	var outcomes []any
	for _, mode := range []string{"legacy", "warm", "reloaded"} {
		bounded := mode != "legacy"
		f := scaleP09Database(t)
		scaleP21PulseSummarySeed(t, f, candidates, 2)
		workspace := f.data.Workspace.URLKey
		repo := f.SQLiteStore
		if bounded {
			var err error
			repo, err = f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.releaseMetadataLogicalSources()
			if mode == "reloaded" {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
		}
		var effects []string
		repo.SetWebhookSink(func(_ string, event domain.DomainEvent) {
			value, _ := json.Marshal([]any{"webhook", event.Type, event.AggregateID, event.Payload, event.PreviousValues, event.Snapshot})
			effects = append(effects, string(value))
		})
		captureRealtime := func(_ string, event domain.RealtimeEvent) {
			value, _ := json.Marshal([]any{"realtime", event.Type, event.AggregateID, event.Payload})
			effects = append(effects, string(value))
		}
		// Commit hooks publish notification signals through the SQL owner's
		// sink; the bounded view publishes the summary event itself.
		f.SetRealtimeSink(captureRealtime)
		repo.SetRealtimeSink(captureRealtime)
		var err error
		if bounded {
			err = repo.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare)
		} else {
			err = scaleP21PulseSummaryLegacy(t.Context(), repo, workspace, candidates, scaleP21PulseSummaryPrepare)
		}
		if err != nil {
			t.Fatal("summary write failed", bounded, err)
		}
		firstEffects := len(effects)
		if bounded {
			err = repo.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare)
		} else {
			err = scaleP21PulseSummaryLegacy(t.Context(), repo, workspace, candidates, scaleP21PulseSummaryPrepare)
		}
		if err != nil || len(effects) != firstEffects || firstEffects != 5 {
			t.Fatal("repeat tick published or failed", bounded, err, firstEffects, len(effects))
		}
		var states []NotificationState
		for _, candidate := range candidates {
			state, err := f.NotificationState(t.Context(), workspace, candidate.UserID)
			if err != nil {
				t.Fatal(err)
			}
			states = append(states, state)
		}
		var audit int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM audit_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&audit); err != nil {
			t.Fatal(err)
		}
		outcomes = append(outcomes, []any{scaleP21PulseSummaryRows(t, f), string(scaleP21PulseSummaryCursor(t, repo, workspace)), effects, states, audit})
		if bounded {
			if repo.metadataResidency.work.used != 0 || repo.WorkspaceMetadataDecodes() != 0 {
				t.Fatal("bounded writer leaked work or hydrated workspace")
			}
			read, found, err := repo.BeginPulseScheduleRead(t.Context(), workspace)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			if err := read.LoadSchedule(t.Context()); err != nil {
				t.Fatal(err)
			}
			actual, _ := json.Marshal(read.Snapshot.Settings["pulseDeliveryCursors"])
			if !bytes.Equal(actual, scaleP21PulseSummaryCursor(t, repo, workspace)) {
				t.Fatal("committed cursor source is stale")
			}
			read.Close()
		}
	}
	for index := 1; index < len(outcomes); index++ {
		if !reflect.DeepEqual(outcomes[0], outcomes[index]) {
			t.Fatal("bounded Pulse summary rows/cursors/publication/state/audit differ", index)
		}
	}
}

func TestScaleP21PulseSummaryWriterRollbackRetryAndPublication(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	candidates := scaleP21PulseSummaryCandidates(idgen.New("summary-rollback-"), 2, true)
	scaleP21PulseSummarySeed(t, f, candidates, 2)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	rows, cursor := scaleP21PulseSummaryRows(t, f), scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)
	installed := view.metadataResidency.logical[workspace]
	published := 0
	view.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
	failure := errors.New("reject Pulse commit")
	previous := f.db.commitHooks.Load()
	defer f.db.commitHooks.Store(previous)
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) { tx.writeErr = failure }})
	if err := view.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); !errors.Is(err, failure) {
		t.Fatal("commit injection was not returned", err)
	}
	if !reflect.DeepEqual(rows, scaleP21PulseSummaryRows(t, f)) || !bytes.Equal(cursor, scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)) ||
		view.metadataResidency.logical[workspace] != installed || published != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("failed atomic write changed rows/source, published or leaked work")
	}
	f.db.commitHooks.Store(previous)
	if err := view.CommitPulseSummaries(t.Context(), workspace, candidates, func(PulseSummaryDispatchInput) (PulseSummaryDispatch, error) {
		return PulseSummaryDispatch{}, failure
	}); !errors.Is(err, failure) {
		t.Fatal("prepare failure was not returned", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := view.CommitPulseSummaries(ctx, workspace, candidates, scaleP21PulseSummaryPrepare); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was not returned", err)
	}
	commits := 0
	f.db.commitHooks.Store(&commitHooks{beforeCommit: func(tx *sqlTx) {
		commits++
		if commits == 1 {
			tx.writeErr = ErrConcurrentWrite
		}
	}})
	view.SetWebhookSink(func(_ string, event domain.DomainEvent) {
		published++
		if view.metadataResidency.work.used == 0 {
			t.Error("publication has no owned work")
		}
		read, found, err := view.BeginPulseScheduleRead(t.Context(), workspace)
		if err != nil || !found {
			t.Error("publication cannot read installed source", found, err)
			return
		}
		defer read.Close()
		if err := read.LoadSchedule(t.Context()); err != nil {
			t.Error(err)
			return
		}
		raw, _ := json.Marshal(read.Snapshot.Settings["pulseDeliveryCursors"])
		if !bytes.Equal(raw, scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)) {
			t.Error("publication observed stale source")
		}
	})
	if err := view.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); err != nil || commits != 2 || published != 1 {
		t.Fatal("retry did not publish one atomic batch", err, commits, published)
	}
	if view.metadataResidency.work.used != 0 {
		t.Fatal("retry/publication leaked work")
	}
}

func TestScaleP21PulseSummaryWriterLargeCursorAndPeerNoop(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	candidates := scaleP21PulseSummaryCandidates(idgen.New("summary-large-"), PulseSummaryBatchMaxCandidates, false)
	scaleP21PulseSummarySeed(t, f, candidates, 9000)
	workspace := f.data.Workspace.URLKey
	before := scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)
	if len(before) <= MetadataRecordPageMaxBytes {
		t.Fatal("large cursor fixture does not exceed generic page cap", len(before))
	}
	first, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer first.releaseMetadataLogicalSources()
	peer, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.releaseMetadataLogicalSources()
	published := 0
	for _, view := range []*SQLiteStore{first, peer} {
		view.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
	}
	limit := first.metadataResidency.work.limit
	first.metadataResidency.work.limit = 64 << 20
	if err := first.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("native cursor preimage was not independently admitted", err)
	}
	first.metadataResidency.work.limit = limit
	if published != 0 || !bytes.Equal(before, scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)) || first.metadataResidency.work.used != 0 {
		t.Fatal("native cursor quota failure changed state or leaked work")
	}
	if err := first.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); err != nil {
		t.Fatal("1000-recipient large cursor transaction failed", err)
	}
	after := scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)
	if len(after) <= len(before) || published != 1 {
		t.Fatal("large cursor did not advance as one batch", len(before), len(after), published)
	}
	limit = peer.metadataResidency.work.limit
	peer.metadataResidency.work.limit = 100 << 20
	if err := peer.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("canonical cursor preimage was not independently admitted", err)
	}
	peer.metadataResidency.work.limit = limit
	if published != 1 || !bytes.Equal(after, scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)) || peer.metadataResidency.work.used != 0 {
		t.Fatal("canonical cursor quota failure changed state or leaked work")
	}
	if err := peer.CommitPulseSummaries(t.Context(), workspace, candidates, scaleP21PulseSummaryPrepare); err != nil || published != 1 {
		t.Fatal("stale peer duplicated cursor batch instead of refreshing and skipping", err, published)
	}
	if !bytes.Equal(after, scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)) {
		t.Fatal("peer no-op changed cursor bytes")
	}
	for _, view := range []*SQLiteStore{first, peer} {
		if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("large cursor/peer path leaked work or hydrated full metadata")
		}
	}
	t.Logf("complete_cursor_bytes=%d -> %d recipients=%d publications=%d", len(before), len(after), len(candidates), published)
}

func TestScaleP21PulseSummaryWriterInputAndCancellationBounds(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	f := scaleP09Database(t)
	candidates := scaleP21PulseSummaryCandidates(idgen.New("summary-bounds-"), 2, true)
	scaleP21PulseSummarySeed(t, f, candidates, 2)
	workspace := f.data.Workspace.URLKey
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	beforeRows, beforeCursor := scaleP21PulseSummaryRows(t, f), scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)
	ctx, cancel := context.WithCancel(t.Context())
	if err := view.CommitPulseSummaries(ctx, workspace, candidates, func(input PulseSummaryDispatchInput) (PulseSummaryDispatch, error) {
		cancel()
		return scaleP21PulseSummaryPrepare(input)
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled delivery preparation reached commit", err)
	}
	invalid := []PulseSummaryCandidate{candidates[0], candidates[0]}
	if err := view.CommitPulseSummaries(t.Context(), workspace, invalid, scaleP21PulseSummaryPrepare); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("duplicate recipient accepted", err)
	}
	overflow := make([]PulseSummaryCandidate, PulseSummaryBatchMaxCandidates+1)
	if err := view.CommitPulseSummaries(t.Context(), workspace, overflow, scaleP21PulseSummaryPrepare); !errors.Is(err, ErrMetadataWorkBudget) {
		t.Fatal("tick bound exceeded", err)
	}
	if err := view.CommitPulseSummaries(t.Context(), workspace, candidates, func(input PulseSummaryDispatchInput) (PulseSummaryDispatch, error) {
		return PulseSummaryDispatch{Send: true, Deliveries: []domain.NotificationDelivery{{ID: "foreign-row"}}}, nil
	}); !errors.Is(err, ErrMetadataRecordQuery) {
		t.Fatal("unbound delivery row accepted", err)
	}
	if view.metadataResidency.work.used != 0 || !reflect.DeepEqual(beforeRows, scaleP21PulseSummaryRows(t, f)) ||
		!bytes.Equal(beforeCursor, scaleP21PulseSummaryCursor(t, f.SQLiteStore, workspace)) {
		t.Fatal("input/cancellation failure leaked work or changed state")
	}
}
