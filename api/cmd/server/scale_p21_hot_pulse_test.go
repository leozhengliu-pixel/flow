package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func TestScaleP21HotPulseSeenDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		seen := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
		reset := func(userID string, exists, enabled bool, instructions string) {
			t.Helper()
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			f.probe.SetWebhookSink(nil)
			f.probe.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21_hot_pulse.reset", "", nil, func(data *domain.Bootstrap) error {
				data.WorkspaceSettings.FeatureFlags["pulse"] = enabled
				delete(data.UserSettings, userID)
				if exists {
					data.UserSettings[userID] = domain.UserSettings{
						UserID: userID, FeedLastSeenTime: seen.Add(-time.Hour).Format(time.RFC3339Nano),
						PulseSchedule: "weekly", Timezone: "Asia/Tokyo", JobTitle: "Retained",
						AgentInstructions: instructions, EnabledCodingTools: []string{"retained"},
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
				t.Fatal(err)
			}
		}
		var sqlSink func(string)
		call := func(source *store.SQLiteStore, user domain.User, key *domain.APIKey, at time.Time) *httptest.ResponseRecorder {
			t.Helper()
			r := f.literalRequest(user, key, map[string]time.Time{"at": at})
			r.URL.Path = "/api/pulse/seen"
			if sqlSink != nil {
				r = r.WithContext(store.WithSQLStatementObserver(r.Context(), sqlSink))
			}
			w := httptest.NewRecorder()
			(&server{store: source}).postPulseSeen(w, r)
			return w
		}
		var sqlCounts [2][2]int
		for mode, source := range []*store.SQLiteStore{repo, f.probe} {
			reset(f.data.Viewer.ID, true, true, "Retained instructions")
			for step, at := range []time.Time{seen, seen.Add(-time.Hour)} {
				operations := map[string]int{}
				sqlSink = func(query string) {
					sqlCounts[mode][step]++
					if words := strings.Fields(query); len(words) > 0 {
						operations[words[0]]++
					}
				}
				w := call(source, f.data.Viewer, nil, at)
				sqlSink = nil
				if w.Code != http.StatusOK || sqlCounts[mode][step] == 0 {
					t.Fatalf("SQL measurement failed: bounded=%t step=%d status=%d body=%s", source.BoundedMetadataEnabled(), step, w.Code, w.Body.String())
				}
				phase := "first-success"
				if step == 1 {
					phase = "no-op"
				}
				t.Logf("Pulse seen bounded=%t %s SQL statements=%d operations=%v", source.BoundedMetadataEnabled(), phase, sqlCounts[mode][step], operations)
			}
		}
		if sqlCounts[1][0] > sqlCounts[0][0] {
			t.Log("Bounded successful acknowledgement performs the scoped writer's locking root and selected-record reads plus bounded normalization/journal publication; the off-mode writer uses its installed complete image. Counts describe this one-record fixture.")
		}
		if sqlCounts[1][1] > sqlCounts[0][1] {
			t.Fatalf("owned installed last-seen precheck added SQL to the no-op: legacy=%d bounded=%d", sqlCounts[0][1], sqlCounts[1][1])
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, exists := range []bool{false, true} {
				var outcomes []any
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					reset(user.ID, exists, true, "Retained instructions")
					unrelated := scaleP21M3Fingerprint(t, repo, workspace, "projects")
					settings := scaleP21M3Fingerprint(t, repo, workspace, "settings")
					var effects []any
					source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
						effects = append(effects, []any{"webhook", event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
					})
					source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) {
						effects = append(effects, []any{"realtime", event.Type, event.AggregateID, event.ActorID, event.Payload})
					})
					var responses []any
					for _, at := range []time.Time{seen, seen.Add(-time.Hour), seen.Add(time.Hour)} {
						before := len(effects)
						w := call(source, user, key, at)
						want := http.StatusOK
						if user.ID == f.principals[3].user.ID {
							want = http.StatusForbidden
						}
						if w.Code != want {
							t.Fatalf("seen exists=%t status=%d body=%s", exists, w.Code, w.Body.String())
						}
						response := scaleP21M2Response(t, w, false)
						if want == http.StatusOK {
							expected := at
							if at.Before(seen) {
								expected = seen
							}
							if response.(map[string]any)["lastSeenAt"] != expected.Format(time.RFC3339Nano) {
								t.Fatalf("last seen changed: %s", w.Body.String())
							}
						}
						responses = append(responses, []any{w.Code, response})
						if at.Before(seen) && len(effects) != before {
							t.Fatal("backward acknowledgement published a mutation")
						}
					}
					persisted := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "userSettings", ID: user.ID})
					outcomes = append(outcomes, scaleP21M2JSON(t, []any{responses, persisted, effects}, false))
					if unrelated != scaleP21M3Fingerprint(t, repo, workspace, "projects") ||
						settings != scaleP21M3Fingerprint(t, repo, workspace, "settings") {
						t.Fatal("Pulse acknowledgement changed unrelated canonical rows")
					}
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatalf("seen exists=%t differential: %s", exists, scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			}
		})
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			reset(f.data.Viewer.ID, true, false, "")
			w := call(source, f.data.Viewer, nil, seen)
			if w.Code != http.StatusForbidden {
				t.Fatalf("disabled Pulse accepted acknowledgement: %d %s", w.Code, w.Body.String())
			}
			reset(f.data.Viewer.ID, true, true, "")
			before := scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "userSettings", ID: f.data.Viewer.ID})
			r := f.literalRequest(f.data.Viewer, nil, map[string]time.Time{"at": seen})
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			w = httptest.NewRecorder()
			(&server{store: source}).postPulseSeen(w, r.WithContext(ctx))
			if w.Code < http.StatusBadRequest ||
				!reflect.DeepEqual(before, scaleP21M2Canonical(t, repo, workspace, store.RecordKey{Kind: "userSettings", ID: f.data.Viewer.ID})) {
				t.Fatalf("cancelled acknowledgement changed state: %d %s", w.Code, w.Body.String())
			}
			// A canceled request must leave the next acknowledgement usable.
			if w := call(source, f.data.Viewer, nil, seen); w.Code != http.StatusOK {
				t.Fatalf("acknowledgement after cancellation failed: %d %s", w.Code, w.Body.String())
			}
		}
		// An oversized selected record must fail explicitly, retaining its full
		// canonical value; the oversized unrelated project in the M fixture is
		// ignored throughout every normal acknowledgement above.
		reset(f.data.Viewer.ID, true, true, strings.Repeat("large instructions ", 20000))
		before := scaleP21M3Fingerprint(t, repo, workspace, "userSettings")
		w := call(f.probe, f.data.Viewer, nil, seen)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
			before != scaleP21M3Fingerprint(t, repo, workspace, "userSettings") {
			t.Fatalf("oversized selected settings silently changed: %d %s", w.Code, w.Body.String())
		}
		reset(f.data.Viewer.ID, true, true, "")
		if w := call(f.probe, f.data.Viewer, nil, seen); w.Code != http.StatusOK {
			t.Fatalf("acknowledgement after budget failure failed: %d %s", w.Code, w.Body.String())
		}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "Pulse foreign", idgen.New("p21-pulse-foreign-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		if err := repo.MutateWorkspace(t.Context(), foreign.Workspace.URLKey, "scale_p21_hot_pulse.foreign", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.FeatureFlags["pulse"] = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		member := f.principals[len(f.principals)-1].user
		for _, phase := range []string{"cross-workspace", "queued-revocation", "replay-revocation"} {
			key := foreign.Workspace.URLKey
			if phase != "cross-workspace" {
				key = workspace
				if phase == "queued-revocation" {
					if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, key); err != nil {
				t.Fatal(err)
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r := f.literalRequest(member, nil, map[string]time.Time{"at": seen})
				r.Header.Set("X-Workspace-Key", key)
				r.SetPathValue("workspace", key)
				r.SetPathValue("workspaceKey", key)
				w := httptest.NewRecorder()
				(&server{store: source}).postPulseSeen(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("%s accepted acknowledgement: %d %s", phase, w.Code, w.Body.String())
				}
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("Pulse acknowledgement decoded full workspace metadata")
		}
	}
}
