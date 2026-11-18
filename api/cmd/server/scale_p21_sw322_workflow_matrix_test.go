package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

// Workflow administration is intentionally admin-only. The actual route gates
// all M principals; only authorized admins enter the issue-retry primitive.
// Due-only scheduler claiming already has SW201's separate exact proof.
func TestScaleP21SW322WorkflowPermissionAndOutputMatrix(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f := scaleP21SW10Setup(t)
	repo, workspace := f.repository, f.data.Workspace.URLKey
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	foreign := scaleP03Fixture(t, repo)
	if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, foreign.data.Workspace.URLKey); err != nil {
		t.Fatal(err)
	}
	prefix := idgen.New("sw322-")
	priorRunID := prefix + "prior"
	f.reader.repo, f.reader.key, f.reader.workspaceID = repo, workspace, f.data.Workspace.ID
	metadata, found := repo.WorkspaceMetadata(workspace)
	if !found {
		t.Fatal("owned M workspace missing")
	}
	private, inherited := "", ""
	for _, project := range metadata.Projects {
		if project.ID == "m2-unrelated" && len(project.TeamIDs) == 1 {
			private = project.TeamIDs[0]
		}
	}
	for team, settings := range metadata.TeamSettings {
		if settings.ParentTeamID == private {
			inherited = team
		}
	}
	if private == "" || inherited == "" {
		t.Fatal("existing private/inherited M teams missing")
	}
	privateAdmin := authClient(t)
	authRequest[domain.AuthSession](t, privateAdmin, http.MethodPost, f.host.URL+"/api/auth/login",
		map[string]string{"email": f.principals[1].user.Email, "password": "p03-fixture-password"}, "", http.StatusOK)
	child := authRequest[domain.Issue](t, privateAdmin, http.MethodPost, f.host.URL+"/api/issue-records",
		map[string]any{"title": "SW322 inherited retry", "teamId": inherited}, workspace, http.StatusCreated)
	baseline, found := repo.BootstrapFor(workspace)
	if !found {
		t.Fatal("owned complete workflow baseline missing")
	}
	privateIssue := domain.Issue{}
	for _, issue := range baseline.Issues {
		if issue.Team.ID == private && slices.ContainsFunc(issue.Permissions, func(permission domain.IssuePermission) bool {
			return permission.SubjectType == "user" && permission.SubjectID == f.principals[3].user.ID && permission.Role == "viewer"
		}) {
			privateIssue = issue
		}
	}
	if privateIssue.ID == "" || child.ID == "" {
		t.Fatal("private explicit-grant and inherited issue controls missing")
	}
	saved, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, principal := range f.principals {
		_, token, err := repo.Login(t.Context(), principal.user.Email, "p03-fixture-password")
		if err != nil {
			t.Fatal(err)
		}
		tokens[principal.user.ID] = token
	}
	type workflowCase struct {
		name, issueID    string
		foreign, escaped bool
	}
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// The real retry endpoint does not require the scheduler's due check.
	// Keep this stored retry out of the fixture server's background tick.
	future := time.Now().UTC().Add(24 * time.Hour)
	reset := func(t *testing.T, cell workflowCase, workflowID string) {
		t.Helper()
		for _, source := range []*store.SQLiteStore{repo, f.probe} {
			source.SetWebhookSink(nil)
			source.SetRealtimeSink(nil)
		}
		if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		restore := store.ForceFullMutationsForTesting(true)
		err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw322_fixture", "", nil, func(data *domain.Bootstrap) error {
			var original domain.Bootstrap
			if err := json.Unmarshal(saved, &original); err != nil {
				return err
			}
			*data = original
			actions := []domain.WorkflowAction{{Type: "notify", Config: map[string]string{"recipientId": f.principals[2].user.ID}}}
			if cell.issueID != "" {
				actions = append([]domain.WorkflowAction{{Type: "setIssuePriority", Config: map[string]string{"priority": "1"}}}, actions...)
				for index := range data.Issues {
					if data.Issues[index].ID == cell.issueID {
						data.Issues[index].Priority, data.Issues[index].PriorityLabel = 4, priorityLabel(4)
					}
				}
			}
			data.WorkflowDefinitions = []domain.WorkflowDefinition{{
				ID: workflowID, Name: "SW322 exact workflow", Trigger: "manual", Enabled: true,
				MaxAttempts: 8, CreatorID: baseline.Viewer.ID, CreatedAt: stamp, UpdatedAt: stamp, Actions: actions,
			}}
			data.WorkflowRuns = []domain.WorkflowRun{}
			if cell.issueID != "" {
				data.WorkflowRuns = []domain.WorkflowRun{{
					ID: priorRunID, WorkflowID: workflowID, ResourceType: "issue", ResourceID: cell.issueID,
					Status: "failed", Attempt: 3, NextRetryAt: &future, ScheduledAt: stamp, StartedAt: stamp,
				}}
			}
			return nil
		})
		restore()
		if err != nil {
			t.Fatal(err)
		}
	}
	pairs, successes, refusals := 0, 0, 0
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		role, status, err := repo.WorkspaceRole(t.Context(), f.data.Workspace.ID, user.ID)
		if err != nil || status != "active" {
			t.Fatal("M principal lost its active membership", err, role, status)
		}
		admin := workspaceAdminRole(role)
		cells := []workflowCase{{name: "actual-manual-route"}, {name: "escaped-manual-route", escaped: true}, {name: "foreign-workspace", foreign: true}}
		if admin {
			cells = append(cells,
				workflowCase{name: "authorized-private-issue-retry", issueID: privateIssue.ID},
				workflowCase{name: "authorized-inherited-issue-retry", issueID: child.ID})
		}
		for _, cell := range cells {
			t.Run(cell.name, func(t *testing.T) {
				workflowID := prefix + "workflow"
				if cell.escaped {
					workflowID += "/escaped"
				}
				var expected any
				legacySQL := 0
				for _, mode := range []string{"legacy-warm", "bounded-warm", "legacy-reloaded", "bounded-reloaded"} {
					reset(t, cell, workflowID)
					if strings.HasSuffix(mode, "reloaded") {
						scaleP21SW201ReloadSource(t, f.reader, repo)
					}
					source := repo
					if strings.HasPrefix(mode, "bounded") {
						if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
							t.Fatal(err)
						}
						source = f.probe
						if strings.HasSuffix(mode, "reloaded") {
							scaleP21SW201ReloadSource(t, f.reader, source)
						}
					}
					service := &server{store: source, realtime: newRealtimeHub()}
					before := scaleP21SW59SQL(t, f)
					beforeMetadata := scaleP21SW201Metadata(t, f.reader)
					foreignReader := &scaleP04Database{config: f.reader.config, key: foreign.data.Workspace.URLKey, reader: f.reader.reader}
					var beforeForeign any
					if cell.foreign {
						beforeForeign = scaleP21SW201Metadata(t, foreignReader)
					}
					prior, err := repo.Events(t.Context(), workflowID)
					if err != nil {
						t.Fatal(err)
					}
					priorIDs := map[string]bool{}
					for _, event := range prior {
						priorIDs[event.ID] = true
					}
					got := scaleP21SW59Outcome{}
					var run domain.WorkflowRun
					allowed := false
					statements, peakWork := 0, 0
					started := time.Now().UTC()
					func() {
						request := f.literalRequest(user, key, nil)
						request.Method, request.URL.Path = http.MethodPost, "/api/workflows/"+workflowID+"/run"
						request.URL.RawPath = "/api/workflows/" + url.PathEscape(workflowID) + "/run"
						request.SetPathValue("id", workflowID)
						request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tokens[user.ID]})
						if cell.foreign {
							request.Header.Set("X-Workspace-Key", foreign.data.Workspace.URLKey)
						}
						request, release := beginRequestResourceReads(request)
						defer release()
						request = request.WithContext(store.WithSQLStatementObserver(request.Context(), func(string) {
							statements++
							peakWork = max(peakWork, source.MetadataWorkUsedForTesting())
						}))
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) {
							got.Webhooks = append(got.Webhooks, scaleP21SW106Envelope(t, event))
						})
						captureRealtime := func(_ string, event domain.RealtimeEvent) {
							got.Realtime = append(got.Realtime, scaleP21SW106Envelope(t, event))
						}
						source.SetRealtimeSink(captureRealtime)
						repo.SetRealtimeSink(captureRealtime)
						defer source.SetWebhookSink(nil)
						defer source.SetRealtimeSink(nil)
						defer repo.SetRealtimeSink(nil)
						recorder := httptest.NewRecorder()
						writer := &scaleP21SW59ResponseWriter{ResponseRecorder: recorder, t: t, source: source, requireHeld: admin && !cell.foreign}
						allowed = service.authorizeWorkspaceRequest(writer, request, user)
						if allowed {
							if cell.issueID == "" {
								service.runWorkflowDefinition(writer, request)
							} else {
								// The same authorized primitive called by retryWorkflowRun.
								run, err = service.executeWorkflow(request.Context(), workspace, workflowID, "retry", "issue", cell.issueID)
								respondMutation(writer, err, http.StatusAccepted, run)
							}
						}
						got.Code, got.Header = recorder.Code, recorder.Header().Clone()
						got.Response = append(json.RawMessage(nil), recorder.Body.Bytes()...)
						if allowed && json.Unmarshal(got.Response, &run) != nil {
							t.Fatal("successful workflow response is not its complete run", string(got.Response))
						}
					}()
					finished := time.Now().UTC()
					required := admin && !cell.foreign
					if allowed != required || required && (got.Code != http.StatusAccepted || run.Status != "succeeded") || !required && got.Code != http.StatusForbidden {
						t.Fatalf("required workflow permission/success refused: mode=%s required=%t allowed=%t status=%d run=%q body=%s", mode, required, allowed, got.Code, run.Status, got.Response)
					}
					if source.BoundedMetadataEnabled() && (source.MetadataWorkUsedForTesting() != 0 || source.WorkspaceMetadataDecodes() != 0) {
						t.Fatal("workflow output leaked work or hydrated full metadata", source.MetadataWorkUsedForTesting(), source.WorkspaceMetadataDecodes())
					}
					got.Rows = scaleP21SW59SQL(t, f)
					afterMetadata := scaleP21SW201Metadata(t, f.reader)
					events, err := repo.Events(t.Context(), workflowID)
					if err != nil {
						t.Fatal(err)
					}
					for _, event := range events {
						if !priorIDs[event.ID] {
							got.Events = append(got.Events, event)
						}
					}
					if required {
						wantAttempt := 1
						if cell.issueID != "" {
							wantAttempt = 4
							issue, err := repo.IssueRecord(t.Context(), workspace, cell.issueID)
							if err != nil || issue.Priority != 1 {
								t.Fatal("authorized workflow lost its actual private/inherited issue action", err, issue.Priority)
							}
						}
						if run.Attempt != wantAttempt || len(got.Events) != 1 || len(got.Webhooks) == 0 || len(got.Realtime) == 0 {
							t.Fatal("successful workflow lost its atomic run/publication effects", run.Attempt, wantAttempt, len(got.Events), len(got.Webhooks), len(got.Realtime))
						}
						notices := 0
						for _, record := range got.Rows.Content {
							if record.Kind != "notification" {
								continue
							}
							var notice domain.Notification
							if err := json.Unmarshal(record.Data, &notice); err != nil {
								t.Fatal(err)
							}
							if notice.SourceType != "workflow" || notice.SourceID != workflowID {
								continue
							}
							notices++
							if notice.RecipientID != f.principals[2].user.ID || notice.Actor.ID != user.ID || notice.IssueID != cell.issueID {
								t.Fatal("workflow notice lost its recipient/private reference/actual actor", notice.RecipientID, notice.Actor.ID, notice.IssueID)
							}
						}
						if notices != 1 {
							t.Fatal("successful workflow did not persist exactly one complete notice", notices)
						}
					} else if !reflect.DeepEqual(before, got.Rows) || !reflect.DeepEqual(beforeMetadata, afterMetadata) ||
						len(got.Events) != 0 || len(got.Webhooks) != 0 || len(got.Realtime) != 0 {
						t.Fatal("forbidden workflow changed business rows or publication effects")
					}
					if cell.foreign && !reflect.DeepEqual(beforeForeign, scaleP21SW201Metadata(t, foreignReader)) {
						t.Fatal("cross-workspace refusal changed the foreign tenant")
					}
					got.Response, err = json.Marshal(map[string]any{"response": json.RawMessage(got.Response), "metadata": afterMetadata})
					if err != nil {
						t.Fatal(err)
					}
					normalized := scaleP21SW201Normalize(t, scaleP21SW59Normalized(t, got, before, started, finished), run.ID, started, finished)
					if strings.HasPrefix(mode, "legacy") {
						expected, legacySQL = normalized, statements
					} else {
						pairs++
						if required {
							successes++
						} else {
							refusals++
						}
						if !reflect.DeepEqual(expected, normalized) {
							t.Fatalf("workflow M output/rows/actors/effects differ: mode=%s case=%s", mode, cell.name)
						}
						t.Logf("workflow M case=%s mode=%s SQL legacy/bounded=%d/%d increase=%d peak-work=%d", cell.name, mode, legacySQL, statements, statements-legacySQL, peakWork)
					}
				}
			})
		}
	})
	if pairs != 152 || successes != 64 || refusals != 88 {
		t.Fatal("workflow M table lost required permission/output cells", pairs, successes, refusals)
	}
	t.Logf("workflow M pairs=%d required-successes=%d exact-refusals=%d; five principals/four credentials, warm and real SQL reload", pairs, successes, refusals)
}
