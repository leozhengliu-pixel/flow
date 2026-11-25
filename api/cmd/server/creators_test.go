package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

func scaleP21SW6GeneratedJSON(t *testing.T, value any, generated map[string]string) any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	var replace func(any) any
	replace = func(value any) any {
		switch item := value.(type) {
		case string:
			if normalized, ok := generated[item]; ok {
				return normalized
			}
		case []any:
			for i, value := range item {
				item[i] = replace(value)
			}
		case map[string]any:
			for key, value := range item {
				item[key] = replace(value)
			}
		}
		return value
	}
	return replace(decoded)
}

func scaleP21SW6CreationState(t *testing.T, repo *store.SQLiteStore, workspace, kind string, baseline []string) ([]string, [32]byte) {
	t.Helper()
	existing := make(map[string]bool, len(baseline))
	for _, id := range baseline {
		existing[id] = true
	}
	hash := sha256.New()
	ids := []string{}
	err := repo.StreamMetadataRecords(t.Context(), workspace, kind, func(key store.RecordKey, reader io.Reader) error {
		ids = append(ids, key.ID)
		if !existing[key.ID] {
			return nil
		}
		_, _ = hash.Write([]byte(key.ID))
		_, err := io.Copy(hash, reader)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids, [32]byte(hash.Sum(nil))
}

func TestScaleP21SW6CreatorDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21M2Setup(t, repo)
		workspace := f.data.Workspace.URLKey
		conversationID, releaseID := idgen.New("ai_conversation_"), idgen.New("release_")
		now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		baseline := map[string][]string{}
		kinds := []string{"aiPromptProgress", "releaseHistory", "releaseNotes"}
		progress := []domain.AIPromptProgress{}
		notes := []domain.ReleaseNote{}
		history := []domain.ReleaseHistory{}
		conversations := []domain.AIConversation{}
		releases := []domain.Release{}
		for range 55 {
			progressID, noteID, historyID := idgen.New("sw6-progress-existing-"), idgen.New("sw6-note-existing-"), idgen.New("sw6-history-existing-")
			baseline["aiPromptProgress"] = append(baseline["aiPromptProgress"], progressID)
			baseline["releaseNotes"] = append(baseline["releaseNotes"], noteID)
			baseline["releaseHistory"] = append(baseline["releaseHistory"], historyID)
			progress = append(progress, domain.AIPromptProgress{ID: progressID, ConversationID: "unrelated", Phase: "before", Progress: 13, CreatedAt: now, UpdatedAt: now})
			notes = append(notes, domain.ReleaseNote{ID: noteID, ReleaseID: "unrelated", Title: "Before", Body: "Keep", Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now})
			history = append(history, domain.ReleaseHistory{ID: historyID, ReleaseID: "unrelated", Actor: f.data.Viewer, Action: "before", Metadata: map[string]any{"keep": true}, CreatedAt: now})
			conversations = append(conversations, domain.AIConversation{ID: idgen.New("sw6-unrelated-ai-"), UserID: f.data.Viewer.ID, Title: "Unrelated", Status: "active", CreatedAt: now, UpdatedAt: now})
			releases = append(releases, domain.Release{ID: idgen.New("sw6-unrelated-release-"), Name: "Unrelated", Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now})
		}
		large := strings.Repeat("unrelated metadata body ", 15000)
		progress[0].Message = large
		notes[0].Body = large
		history[0].Metadata["body"] = large
		conversations[0].Context = map[string]any{"body": large}
		releases[0].Description = large
		reset := func(owner string) {
			t.Helper()
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			f.probe.SetWebhookSink(nil)
			f.probe.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "aiPromptProgress", "releaseNotes", "releaseHistory", "aiConversations", "releases"), workspace, "agent.activity_updated", "", nil, func(data *domain.Bootstrap) error {
				data.AIPromptProgress = slices.Clone(progress)
				data.ReleaseNotes = slices.Clone(notes)
				data.ReleaseHistory = slices.Clone(history)
				data.AIConversations = append(slices.Clone(conversations), domain.AIConversation{
					ID: conversationID, UserID: owner, Title: "Target", Status: "active",
					Context: map[string]any{"reference": "private-reference", "count": 37, "actorId": f.data.Viewer.ID}, CreatedAt: now, UpdatedAt: now,
				})
				data.Releases = append(slices.Clone(releases), domain.Release{
					ID: releaseID, Name: "Target", Creator: f.data.Viewer, ProjectIDs: []string{"private-project-reference"}, IssueIDs: []string{"private-issue-reference"},
					IssueCount: 37, CompletedCount: 13, CreatedAt: now, UpdatedAt: now,
				})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		reset(f.data.Viewer.ID)
		hashes := map[string][32]byte{}
		for kind, ids := range baseline {
			_, hashes[kind] = scaleP21SW6CreationState(t, repo, workspace, kind, ids)
		}
		unrelatedProject := scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated")
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW6 foreign creators", idgen.New("sw6-creators-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		foreignConversationID, foreignReleaseID := idgen.New("ai_conversation_"), idgen.New("release_")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "aiConversations", "releases"), foreign.Workspace.URLKey, "agent.activity_updated", "", nil, func(data *domain.Bootstrap) error {
			data.AIConversations = []domain.AIConversation{{ID: foreignConversationID, UserID: f.data.Viewer.ID, Title: "Foreign", CreatedAt: now, UpdatedAt: now}}
			data.Releases = []domain.Release{{ID: foreignReleaseID, Name: "Foreign", Creator: f.data.Viewer, CreatedAt: now, UpdatedAt: now}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		foreignConversation := scaleP21SW6RecordHash(t, repo, foreign.Workspace.URLKey, "aiConversations", foreignConversationID)
		foreignRelease := scaleP21SW6RecordHash(t, repo, foreign.Workspace.URLKey, "releases", foreignReleaseID)
		foreignRefs := map[string]string{"prompt-progress": foreignConversationID, "release-note": foreignReleaseID}

		operations := []struct {
			name  string
			kinds []string
			run   func(*server, http.ResponseWriter, *http.Request)
			body  func(int) any
		}{
			{
				name: "prompt-progress", kinds: []string{"aiPromptProgress"}, run: (*server).createAIPromptProgress,
				body: func(i int) any {
					value := -5
					if i == 1 {
						value = 150
					}
					return map[string]any{"id": "client-progress", "conversationId": conversationID, "phase": "answer", "message": "Progress", "progress": value}
				},
			},
			{
				name: "release-note", kinds: []string{"releaseNotes", "releaseHistory"}, run: (*server).createReleaseNote,
				body: func(int) any {
					return map[string]any{"id": "client-note", "releaseId": "client-release", "title": "Note", "body": "Body", "bodyData": map[string]any{"reference": "private-reference", "count": 41}, "creator": map[string]any{"id": "client-creator"}, "publishedAt": now}
				},
			},
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						reset(user.ID)
						generated := map[string]string{}
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						responses := []any{}
						for i := range 2 {
							r := f.request(user, key, operation.body(i))
							r.SetPathValue("id", releaseID)
							r = r.WithContext(store.ContextWithRealtimeClient(r.Context(), "sw6-create-client"))
							w := httptest.NewRecorder()
							operation.run(&server{store: source}, w, r)
							if w.Code != http.StatusCreated {
								t.Fatalf("creation status=%d body=%s", w.Code, w.Body.String())
							}
							var response map[string]any
							if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
								t.Fatal(err)
							}
							id, _ := response["id"].(string)
							if id == "" || id == "client-note" || id == "client-progress" {
								t.Fatalf("client supplied ID was not replaced: %q", id)
							}
							generated[id] = operation.name + "-" + string(rune('0'+i))
							for _, field := range []string{"createdAt", "updatedAt"} {
								stamp, _ := response[field].(string)
								if stamp == "" {
									t.Fatalf("created record lacks %s", field)
								}
								generated[stamp] = "<generated-clock>"
							}
							if operation.name == "prompt-progress" {
								want := float64(0)
								if i == 1 {
									want = 100
								}
								if response["progress"] != want || response["conversationId"] != conversationID {
									t.Fatal("progress clamp or conversation binding changed", response)
								}
							} else {
								creator, _ := response["creator"].(map[string]any)
								if creator["id"] != user.ID || response["releaseId"] != releaseID {
									t.Fatal("release-note actor or release binding changed", response)
								}
							}
							responses = append(responses, response)
						}
						if len(events) != 2 || len(realtime) != 2 {
							t.Fatalf("create publications=%d/%d", len(events), len(realtime))
						}
						rows, order := []any{}, map[string][]string{}
						for _, kind := range kinds {
							ids := baseline[kind]
							current, hash := scaleP21SW6CreationState(t, repo, workspace, kind, ids)
							if hash != hashes[kind] || len(current) < len(ids) || !slices.Equal(current[:len(ids)], ids) {
								t.Fatal("creator changed existing collection bytes or order", kind)
							}
							want := 0
							if slices.Contains(operation.kinds, kind) {
								want = 2
							}
							if len(current)-len(ids) != want {
								t.Fatal("creator changed an undeclared collection or lost an append", kind)
							}
							order[kind] = current
							for i, id := range current[len(ids):] {
								if kind == "releaseHistory" {
									generated[id] = "release-history-" + string(rune('0'+i))
								}
								batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, []store.RecordKey{{Kind: kind, ID: id}}, "detail")
								if err != nil || len(batch.Items) != 1 || batch.Items[0].Missing || batch.Items[0].Omitted {
									t.Fatal("read created canonical record", err)
								}
								rows = append(rows, []any{kind, batch.Items[0].Data})
							}
						}
						effects := []any{}
						for _, event := range events {
							effects = append(effects, []any{event.Type, event.AggregateID, event.Payload, event.PreviousValues})
						}
						for _, event := range realtime {
							effects = append(effects, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
						}
						if scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated") != unrelatedProject {
							t.Fatal("creator rewrote unrelated private project bytes")
						}
						outcomes = append(outcomes, scaleP21SW6GeneratedJSON(t, []any{responses, rows, order, effects}, generated))
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("creator differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		for _, operation := range operations {
			for _, spec := range []struct {
				name string
				id   string
			}{
				{"missing", idgen.New("sw6-missing-")},
				{"cross-workspace", foreignRefs[operation.name]},
				{"long", strings.Repeat("missing", 30)},
				{"nul", "missing\x00reference"},
			} {
				t.Run(operation.name+"/"+spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						reset(f.data.Viewer.ID)
						var events int
						source.SetWebhookSink(func(string, domain.DomainEvent) { events++ })
						source.SetRealtimeSink(func(string, domain.RealtimeEvent) { events++ })
						body := operation.body(0)
						if operation.name == "prompt-progress" {
							body.(map[string]any)["conversationId"] = spec.id
						}
						r := f.request(f.data.Viewer, nil, body)
						r.SetPathValue("id", spec.id)
						w := httptest.NewRecorder()
						operation.run(&server{store: source}, w, r)
						if w.Code != http.StatusNotFound || events != 0 {
							t.Fatalf("missing input status=%d publications=%d body=%s", w.Code, events, w.Body.String())
						}
						outcomes = append(outcomes, []any{w.Code, scaleP21M2Response(t, w, false)})
						for kind, ids := range baseline {
							current, hash := scaleP21SW6CreationState(t, repo, workspace, kind, ids)
							if hash != hashes[kind] || !slices.Equal(current, ids) {
								t.Fatal("failed creation changed existing rows", kind)
							}
						}
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatal("missing input error changed")
					}
				})
			}
		}
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			reset(f.principals[1].user.ID)
			r := f.request(f.data.Viewer, nil, operations[0].body(0))
			w := httptest.NewRecorder()
			(&server{store: source}).createAIPromptProgress(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatal("another owner's conversation accepted progress", w.Code)
			}
		}
		for _, operation := range operations {
			reset(f.data.Viewer.ID)
			var publications int
			f.probe.SetWebhookSink(func(string, domain.DomainEvent) { publications++ })
			f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { publications++ })
			body := operation.body(0).(map[string]any)
			if operation.name == "prompt-progress" {
				body["message"] = strings.Repeat("oversized output ", 20000)
			} else {
				body["body"] = strings.Repeat("oversized output ", 20000)
			}
			r := f.request(f.data.Viewer, nil, body)
			r.SetPathValue("id", releaseID)
			w := httptest.NewRecorder()
			operation.run(&server{store: f.probe}, w, r)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) || publications != 0 {
				t.Fatalf("oversized creation was not atomic: status=%d publications=%d body=%s", w.Code, publications, w.Body.String())
			}
			for kind, ids := range baseline {
				current, hash := scaleP21SW6CreationState(t, repo, workspace, kind, ids)
				if hash != hashes[kind] || !slices.Equal(current, ids) {
					t.Fatal("oversized creation changed canonical state", kind)
				}
			}
			r = f.request(f.data.Viewer, nil, operation.body(0))
			r.SetPathValue("id", releaseID)
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			r = r.WithContext(ctx)
			w = httptest.NewRecorder()
			operation.run(&server{store: f.probe}, w, r)
			if w.Code != http.StatusInternalServerError || publications != 0 {
				t.Fatal("cancelled creation published an event", w.Code, publications)
			}
			r = f.request(f.data.Viewer, nil, operation.body(0))
			r.SetPathValue("id", releaseID)
			w = httptest.NewRecorder()
			operation.run(&server{store: f.probe}, w, r)
			if w.Code != http.StatusCreated || publications != 2 {
				t.Fatal("failed creation prevented the next valid append", w.Code, publications)
			}
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("creator decoded complete workspace metadata")
		}
		if scaleP21SW6RecordHash(t, repo, foreign.Workspace.URLKey, "aiConversations", foreignConversationID) != foreignConversation ||
			scaleP21SW6RecordHash(t, repo, foreign.Workspace.URLKey, "releases", foreignReleaseID) != foreignRelease {
			t.Fatal("creator changed foreign workspace records")
		}
	}
}
