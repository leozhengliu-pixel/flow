package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21PulseSummaryResponse struct {
	*httptest.ResponseRecorder
	t       *testing.T
	bounded *store.SQLiteStore
}

func (w *scaleP21PulseSummaryResponse) Write(value []byte) (int, error) {
	if w.bounded != nil && w.Code == http.StatusOK && w.bounded.MetadataWorkUsedForTesting() == 0 {
		w.t.Error("summary/audio output outlived its admitted Pulse source")
	}
	return w.ResponseRecorder.Write(value)
}

func TestScaleP21PulseSummaryReadDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("pulse-summary-read-")
		public, private, initiative := prefix+"public", prefix+"private", prefix+"initiative"
		at := time.Date(2026, 11, 14, 6, 0, 0, 0, time.UTC)
		cached := &domain.PulseSummary{Title: "Cached exact summary", GeneratedAt: at, AI: true,
			Sections: []domain.PulseSummarySection{{Kind: "project", Items: []domain.PulseSummaryItem{{UpdateID: "cached", SourceName: "Cached source", Summary: "Cached item"}}}}, Text: "Cached exact text."}
		retry := at.AddDate(10, 0, 0)
		fallback := &domain.PulseSummary{Title: "Cached fallback", GeneratedAt: at, Text: "Cached fallback text.", RetryAt: &retry, Attempts: 2}
		scenarios := []struct {
			name                              string
			principal                         int
			key                               *domain.APIKey
			cached                            *domain.PulseSummary
			agent, failAI, hipaa, aiOff       bool
			pulseOff, noAudio, missing        bool
			development, defaultKey, wrongKey bool
		}{
			{name: "extractive"},
			{name: "private-admin", principal: 1},
			{name: "private-outsider", principal: 2},
			{name: "guest", principal: 3},
			{name: "inherited-member", principal: 4},
			{name: "selected-empty", key: &domain.APIKey{ID: "summary-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			{name: "selected-set", key: &domain.APIKey{ID: "summary-key", Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
			{name: "cached-ai", cached: cached, agent: true},
			{name: "cached-fallback", cached: fallback, agent: true},
			{name: "ai-generation", agent: true},
			{name: "ai-failure", agent: true, failAI: true},
			{name: "hipaa", agent: true, hipaa: true},
			{name: "ai-off", cached: cached, agent: true, aiOff: true},
			{name: "feature-off", pulseOff: true},
			{name: "audio-disabled", noAudio: true},
			{name: "missing-notification", missing: true},
			{name: "wrong-workspace", wrongKey: true},
			{name: "default-workspace", development: true, defaultKey: true},
		}
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.pulse_summary_fixture", "", nil, func(data *domain.Bootstrap) error {
			privateTeam := ""
			for _, project := range data.Projects {
				if project.ID == "m2-unrelated" {
					privateTeam = project.TeamIDs[0]
				}
			}
			data.Projects = append(data.Projects,
				domain.Project{ID: public, Name: strings.Repeat("Complete source name ", 2000), TeamIDs: []string{f.public}},
				domain.Project{ID: private, Name: "Private source", TeamIDs: []string{privateTeam}},
			)
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiative, Name: "Initiative source", ProjectIDs: []string{public}})
			author := domain.User{ID: prefix + "author", Name: "Writer", DisplayName: "Complete writer"}
			data.ProjectUpdates[public] = []domain.ProjectUpdate{
				{ID: "summary-public", ProjectID: public, Body: "First sentence. Second sentence. " + strings.Repeat("Complete selected body. ", 14000), User: author, Health: "atRisk", CreatedAt: at},
				{ID: "summary-unused", ProjectID: public, Body: strings.Repeat("Unrequested body ", 50000), User: author, CreatedAt: at},
			}
			data.ProjectUpdates[private] = []domain.ProjectUpdate{{ID: "summary-private", ProjectID: private, Body: "Private selected body.", User: author, CreatedAt: at}}
			data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{{ID: "summary-initiative", InitiativeID: initiative, Body: "Initiative selected body.", User: author, CreatedAt: at}}
			for _, scenario := range scenarios {
				if scenario.missing {
					continue
				}
				data.Notifications = append(data.Notifications, domain.Notification{
					ID: prefix + scenario.name, RecipientID: f.principals[scenario.principal].user.ID, Type: "pulseSummary", Category: "pulse", Title: "Daily Pulse",
					Actor: author, CreatedAt: at, UpdatedAt: at, Payload: &domain.NotificationPayload{Schedule: "daily", Updates: []domain.PulseUpdateRef{
						{ID: "summary-public", Kind: "project", SourceID: public, At: at},
						{ID: "summary-private", Kind: "project", SourceID: private, At: at},
						{ID: "deleted", Kind: "project", SourceID: public, At: at},
						{ID: "summary-initiative", Kind: "initiative", SourceID: initiative, At: at},
						{ID: "summary-public", Kind: "project", SourceID: public, At: at},
					}},
				})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		var failAI atomic.Bool
		var aiCalls, audioCalls atomic.Int32
		var providerMu sync.Mutex
		var aiRequests, audioRequests []any
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var request any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/audio/speech") {
				audioCalls.Add(1)
				providerMu.Lock()
				audioRequests = append(audioRequests, request)
				providerMu.Unlock()
				_, _ = w.Write([]byte("ID3-owned-summary-audio"))
				return
			}
			aiCalls.Add(1)
			providerMu.Lock()
			aiRequests = append(aiRequests, request)
			providerMu.Unlock()
			if failAI.Load() {
				http.Error(w, "unavailable", http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{
				"content": `{"summaries":[{"index":1,"updateId":"summary-public","summary":"Provider summary."}]}`,
			}}}})
		}))
		defer provider.Close()
		normalize := func(value any) any {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var result any
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			var clocks func(any)
			clocks = func(value any) {
				switch value := value.(type) {
				case map[string]any:
					for key, child := range value {
						if key == "generatedAt" || key == "retryAt" || key == "createdAt" {
							value[key] = "<generated-clock>"
						} else {
							clocks(child)
						}
					}
				case []any:
					for _, child := range value {
						clocks(child)
					}
				}
			}
			clocks(result)
			return result
		}
		for _, scenario := range scenarios {
			t.Run(scenario.name, func(t *testing.T) {
				user, notificationID := f.principals[scenario.principal].user, prefix+scenario.name
				if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "workspace.settings_updated", "", nil, func(data *domain.Bootstrap) error {
					data.WorkspaceSettings.FeatureFlags["pulse"], data.WorkspaceSettings.FeatureFlags["ai"] = !scenario.pulseOff, !scenario.aiOff
					data.WorkspaceSettings.HIPAACompliance = scenario.hipaa
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				var outcomes []any
				for _, bounded := range []bool{false, true} {
					if !scenario.missing {
						if _, err := repo.UpdateNotificationRecord(t.Context(), workspace, user.ID, notificationID, "notification.pulse_summary_generated", nil, func(item *domain.Notification) {
							item.PulseSummary, item.SummaryReports = scenario.cached, nil
						}); err != nil {
							t.Fatal(err)
						}
					}
					source := repo
					if bounded {
						var err error
						source, err = repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
						if err != nil {
							t.Fatal(err)
						}
					}
					s := &server{store: source, uploadPath: t.TempDir(), authDisabled: scenario.development}
					s.agent.Enabled, s.agent.Protocol, s.agent.BaseURL, s.agent.Model = scenario.agent, "openai-chat-completions", provider.URL+"/v1", "test-model"
					s.agent.Timeout = 5 * time.Second
					s.tts.Enabled, s.tts.BaseURL, s.tts.Model, s.tts.Voice = !scenario.noAudio, provider.URL+"/v1", "test-speech", "test-voice"
					failAI.Store(scenario.failAI)
					beforeAI, beforeAudio := aiCalls.Load(), audioCalls.Load()
					providerMu.Lock()
					aiRequests, audioRequests = nil, nil
					providerMu.Unlock()
					responses := []any{}
					for _, endpoint := range []struct {
						name    string
						handler func(*server, http.ResponseWriter, *http.Request)
						body    any
					}{
						{"capabilities", (*server).getPulseCapabilities, nil},
						{"summary", (*server).getPulseSummary, nil},
						{"summary-cached", (*server).getPulseSummary, nil},
						{"audio", (*server).getPulseSummaryAudio, nil},
						{"report", (*server).reportPulseSummary, map[string]string{"reason": "Exact report", "updateId": "summary-public"}},
					} {
						r := f.literalRequest(user, scenario.key, endpoint.body)
						r.Method, r.URL.Path = http.MethodGet, "/api/pulse/summaries/"+notificationID
						r.SetPathValue("id", notificationID)
						if endpoint.body != nil {
							r.Method = http.MethodPost
						}
						if scenario.defaultKey {
							r.Header.Del("X-Workspace-Key")
						}
						if scenario.wrongKey {
							r.Header.Set("X-Workspace-Key", prefix+"absent")
						}
						w := &scaleP21PulseSummaryResponse{ResponseRecorder: httptest.NewRecorder(), t: t}
						if bounded {
							w.bounded = source
						}
						scaleP21CallWithOutputOwner(s, w, r, endpoint.handler)
						var body any
						if strings.HasPrefix(w.Header().Get("Content-Type"), "audio/") {
							body = w.Body.String()
						} else if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
							t.Fatal("summary response is not JSON", endpoint.name, w.Code, err)
						}
						responses = append(responses, []any{endpoint.name, w.Code, w.Header().Clone(), normalize(body)})
						if bounded && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
							t.Fatal("summary endpoint leaked work or decoded full metadata", endpoint.name)
						}
					}
					var saved any
					if !scenario.missing {
						record, err := repo.NotificationRecord(t.Context(), workspace, user.ID, notificationID)
						if err != nil {
							t.Fatal(err)
						}
						saved = normalize([]any{record.PulseSummary, record.SummaryReports})
					}
					providerMu.Lock()
					requests := []any{append([]any(nil), aiRequests...), append([]any(nil), audioRequests...)}
					providerMu.Unlock()
					outcomes = append(outcomes, []any{responses, saved, aiCalls.Load() - beforeAI, audioCalls.Load() - beforeAudio, requests})
				}
				if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
					t.Fatal("summary/capabilities/audio/report/cache differential", scaleP21M2Diff(outcomes[0], outcomes[1]))
				}
			})
		}
	}
}
