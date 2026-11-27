package main

import (
	"context"
	"encoding/json"
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

func TestRestoreTeamDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleServerTestStores(t) {
		f := setupPolicyDifferential(t, repo)
		workspace := f.data.Workspace.URLKey
		targetID := idgen.New("team_sw20_")
		documentID := idgen.New("doc_sw20_team_collision_")
		stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		saved := []domain.Team{}
		original, ok := repo.WorkspaceMetadata(workspace)
		if !ok {
			t.Fatal("missing workspace")
		}
		saved = append(saved, original.Teams...)
		for range 60 {
			saved = append(saved, domain.Team{ID: idgen.New("team_sw20_other_"), Key: idgen.New("sw20-"), Name: "Retained"})
		}
		reset := func(archived bool) {
			t.Helper()
			repo.SetWebhookSink(nil)
			repo.SetRealtimeSink(nil)
			f.probe.SetWebhookSink(nil)
			f.probe.SetRealtimeSink(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "teamSettings", "documents"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
				target := domain.Team{ID: targetID, Key: "SW20", Name: "Restorable", Private: true}
				if archived {
					target.ArchivedAt, target.UpdatedAt = &stamp, &stamp
					target.ArchivedByID = f.principals[1].user.ID
				}
				data.Teams = append(slices.Clone(saved), target)
				data.TeamSettings[targetID] = domain.TeamSettings{TeamID: targetID, ParentTeamID: saved[0].ID, Access: "private"}
				data.Documents = []domain.Document{{ID: documentID, Title: "Earlier collision", ContentData: map[string]any{"id": targetID, "retained": "team fast-prefix wins"}, Creator: f.data.Viewer}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		reset(true)
		otherHash := recordHash(t, repo, workspace, "teams", saved[len(saved)-1].ID)
		settingsHash := recordHash(t, repo, workspace, "teamSettings", targetID)
		projectHash := recordHash(t, repo, workspace, "projects", "m2-unrelated")
		documentHash := recordHash(t, repo, workspace, "documents", documentID)
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range []struct {
				name, id string
				archived bool
				cancel   bool
				status   int
				publish  int
			}{
				{"restored", targetID, true, false, http.StatusOK, 1},
				{"not-archived", targetID, false, false, http.StatusNotFound, 0},
				{"missing", targetID + "-old-key", true, false, http.StatusNotFound, 0},
				{"empty-id", "", true, false, http.StatusNotFound, 0},
				{"long-id", strings.Repeat("x", 192), true, false, http.StatusNotFound, 0},
				{"nul-id", targetID + "\x00", true, false, http.StatusNotFound, 0},
				{"cancelled", targetID, true, true, http.StatusInternalServerError, 0},
			} {
				t.Run(spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						reset(spec.archived)
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						r, w := f.request(user, key, nil), httptest.NewRecorder()
						r.SetPathValue("teamId", spec.id)
						started := time.Now().UTC()
						if spec.cancel {
							ctx, cancel := context.WithCancel(r.Context())
							cancel()
							(&server{store: source}).restoreDeletedTeam(w, r.WithContext(ctx))
						} else {
							callPointSettingsHandler(&server{store: source}, w, r, (*server).restoreDeletedTeam)
						}
						if w.Code != spec.status || len(events) != spec.publish || len(realtime) != spec.publish {
							t.Fatalf("restore status=%d events=%d/%d want=%d/%d: %s", w.Code, len(events), len(realtime), spec.status, spec.publish, w.Body.String())
						}
						var response any
						generated := map[string]string{}
						if w.Body.Len() > 0 {
							if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
								t.Fatal(err)
							}
							if spec.publish == 1 {
								object := response.(map[string]any)
								if object["archivedAt"] != nil || object["archivedById"] != nil {
									t.Fatal("restored team retained archive metadata")
								}
								// Audit and resource clocks may coincide. A single
								// token keeps normalization independent of equality.
								generated[object["updatedAt"].(string)] = "<generated-clock>"
								policy, ok := source.IssueAccessMetadata(t.Context(), workspace)
								index := slices.IndexFunc(policy.Teams, func(team domain.Team) bool { return team.ID == targetID })
								if !ok || index < 0 || policy.Teams[index].ArchivedAt != nil ||
									policy.TeamSettings[targetID].ParentTeamID != saved[0].ID {
									t.Fatal("restoration did not install complete live team policy")
								}
							}
						}
						if recordHash(t, repo, workspace, "teams", saved[len(saved)-1].ID) != otherHash ||
							recordHash(t, repo, workspace, "teamSettings", targetID) != settingsHash ||
							recordHash(t, repo, workspace, "documents", documentID) != documentHash ||
							recordHash(t, repo, workspace, "projects", "m2-unrelated") != projectHash {
							t.Fatal("restoration changed unrelated canonical data")
						}
						rows := helpersRows(t, repo, workspace, store.RecordKey{Kind: "teams", ID: targetID}, store.RecordKey{Kind: "users", ID: user.ID})
						audits, err := repo.ListAuditEntries(t.Context(), f.data.Workspace.ID, store.AuditQuery{Since: started, ResourceTypes: []string{"team"}, ResourceID: targetID})
						if err != nil || len(audits) != spec.publish {
							t.Fatal("team restoration audit count changed", len(audits), err)
						}
						for i := range audits {
							generated[audits[i].ID] = "<restored-audit>"
							generated[audits[i].CreatedAt.Format(time.RFC3339Nano)] = "<generated-clock>"
							audits[i].Seq, audits[i].Hash = 0, ""
						}
						outcomes = append(outcomes, generatedJSON(t, []any{w.Code, response, rows, audits, helpersEffects(events, realtime)}, generated))
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("restore differential: %s", policyFirstDifference(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		t.Run("workspace-and-revocation", func(t *testing.T) {
			reset(true)
			before := recordHash(t, repo, workspace, "teams", targetID)
			foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW20 foreign", idgen.New("sw20-workspace-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
			// The foreign workspace was created after the bounded view.
			// Install its literal preimage before comparing tenant binding.
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.Workspace.URLKey); err != nil {
				t.Fatal(err)
			}
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.Header.Set("X-Workspace-Key", foreign.Workspace.URLKey)
				r.SetPathValue("workspaceKey", foreign.Workspace.URLKey)
				r.SetPathValue("workspace", foreign.Workspace.URLKey)
				r.SetPathValue("teamId", targetID)
				callPointSettingsHandler(&server{store: source}, w, r, (*server).restoreDeletedTeam)
				if w.Code != http.StatusNotFound {
					t.Fatal("team restore escaped workspace binding", w.Code, w.Body.String())
				}
			}
			member := f.principals[2].user
			requests := []*http.Request{}
			for _, credential := range pointSettingsCredentialCases() {
				r := f.request(member, credential.key, nil)
				r.SetPathValue("teamId", targetID)
				requests = append(requests, r)
			}
			if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = repo.ResumeMember(t.Context(), f.data.Workspace.ID, member.ID) }()
			for _, phase := range []string{"queued", "replayed"} {
				for _, r := range requests {
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						w := httptest.NewRecorder()
						callPointSettingsHandler(&server{store: source}, w, r, (*server).restoreDeletedTeam)
						if w.Code != http.StatusForbidden {
							t.Fatal(phase, "team restore reused revoked membership", w.Code)
						}
					}
				}
			}
			if recordHash(t, repo, workspace, "teams", targetID) != before {
				t.Fatal("cross-workspace or revoked restore changed its target")
			}
		})
		t.Run("budget-and-recovery", func(t *testing.T) {
			reset(true)
			if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams"), workspace, "team.updated", "", nil, func(data *domain.Bootstrap) error {
				for i := range data.Teams {
					if data.Teams[i].ID == targetID {
						data.Teams[i].ExternalSource = strings.Repeat("large selected source ", 15000)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := recordHash(t, repo, workspace, "teams", targetID)
			published := 0
			f.probe.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
			f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
			for range 2 {
				r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
				r.SetPathValue("teamId", targetID)
				(&server{store: f.probe}).restoreDeletedTeam(w, r)
				if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
					published != 0 || recordHash(t, repo, workspace, "teams", targetID) != before {
					t.Fatal("oversized restore did not reject without publication", w.Code, w.Body.String())
				}
			}
			reset(true)
			r, w := f.request(f.data.Viewer, nil, nil), httptest.NewRecorder()
			r.SetPathValue("teamId", targetID)
			(&server{store: f.probe}).restoreDeletedTeam(w, r)
			if w.Code != http.StatusOK {
				t.Fatal("restore did not recover after budget rejection", w.Code)
			}
		})
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("team restore decoded full metadata")
		}
	}
}
