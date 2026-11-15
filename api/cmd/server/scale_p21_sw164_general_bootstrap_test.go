package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func scaleP21SW164Seed(t *testing.T, repo *store.SQLiteStore) scaleP21SW145Fixture {
	t.Helper()
	f := scaleP21SW145Seed(t, repo)
	at := time.Date(2026, 11, 15, 12, 13, 14, 123456789, time.UTC)
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p21.sw164_seed", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Projects {
			data.Projects[index].UpdateSchedule = &domain.ProjectUpdateSchedule{Mode: "never"}
			if data.Projects[index].ID == f.project {
				data.Projects[index].Comments = []domain.Comment{{ID: f.project + "-comment", Body: "SW164_PROJECT_COMMENT /uploads/project-comment.png", User: data.Viewer, CreatedAt: at}}
			}
		}
		for key, settings := range data.CycleSettings {
			settings.Enabled, settings.AutoCreate = false, false
			data.CycleSettings[key] = settings
		}
		data.IssueSLAs = nil
		data.Settings["projectUpdates"] = map[string]any{"cadenceDays": 0, "reminders": false, "missingNotifications": false}
		for index := range data.Issues {
			if data.Issues[index].ID == f.issue {
				data.Issues[index].Description = "SW164_ISSUE_BEFORE /uploads/large-issue.png " + strings.Repeat("i", (2<<20)+4096)
			}
		}
		data.Comments[f.issue] = []domain.Comment{{
			ID: f.issue + "-comment", Version: 3, User: data.Viewer, CreatedAt: at,
			Body:     "SW164_COMMENT_BEFORE /uploads/large-comment.png " + strings.Repeat("c", (2<<20)+4096),
			BodyData: map[string]any{"integer": int64(9007199254740993), "empty": []any{}, "url": "/uploads/comment-data.png"},
		}}
		data.Activities[f.issue] = []domain.ActivityEvent{{ID: f.issue + "-activity", Type: "updated", Actor: data.Viewer, CreatedAt: at, Metadata: domain.ActivityMetadata{"tag": "SW164_ACTIVITY_BEFORE"}}}
		data.Notifications[0].Title, data.NotificationDeliveries[0].Error = "SW164_NOTIFICATION_BEFORE", "SW164_DELIVERY_BEFORE"
		data.ProjectUpdates[f.project] = []domain.ProjectUpdate{{
			ID: f.project + "-update", ProjectID: f.project, User: data.Viewer, CreatedAt: at, Health: "onTrack",
			Body: "SW164_PROJECT_UPDATE /uploads/project-update.png", Snapshot: &domain.PulseSnapshot{},
			Comments: []domain.Comment{{ID: f.project + "-update-comment", Body: "SW164_UPDATE_COMMENT", User: data.Viewer, CreatedAt: at}},
		}}
		initiative := f.project + "-initiative"
		data.Initiatives = append(data.Initiatives, domain.Initiative{
			ID: initiative, Name: "SW164 initiative", SlugID: initiative + "-current", PreviousSlugIDs: []string{initiative + "-old"},
			Description: "SW164_INITIATIVE_DESCRIPTION /uploads/initiative.png", Creator: data.Viewer, Owner: &data.Viewer,
			LeadTeamID: f.public, ContributingTeamIDs: []string{f.public}, ProjectIDs: []string{f.project}, CreatedAt: at, UpdatedAt: at,
			Comments: []domain.Comment{{ID: initiative + "-comment", Body: "SW164_INITIATIVE_COMMENT", User: data.Viewer, CreatedAt: at}},
		})
		if data.InitiativeUpdates == nil {
			data.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
		}
		data.InitiativeUpdates[initiative] = []domain.InitiativeUpdate{{
			ID: initiative + "-update", InitiativeID: initiative, User: data.Viewer, CreatedAt: at, Health: "atRisk",
			Body: "SW164_INITIATIVE_UPDATE", Snapshot: &domain.PulseSnapshot{},
		}}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type scaleP21SW164Case struct {
	scaleP21SW145Case
	cancelBefore, cancelAtHeader bool
	synthesizedMembers           bool
	sourceClass                  string
	atHeader                     func()
}

type scaleP21SW164Trace struct {
	scaleP21SW145Trace
	headerSeen, aborted         bool
	contentBefore, contentAfter int
	issueAfter, statements      int
	started, ended              time.Time
}

type scaleP21SW164Producer struct {
	*scaleP21SW145Producer
	trace *scaleP21SW164Trace
	hook  func()
}

func (w *scaleP21SW164Producer) WriteHeader(code int) {
	if code == http.StatusOK && !w.trace.headerSeen {
		if w.hook != nil && w.trace.contentBefore != 0 {
			w.t.Fatal("snapshot/cancel hook followed a content-body query")
		}
		w.trace.headerSeen = true
		if w.hook != nil {
			w.hook()
		}
	}
	w.scaleP21SW145Producer.WriteHeader(code)
}

type scaleP21SW164Network struct {
	*scaleP21SW145Network
	t      *testing.T
	source *store.SQLiteStore
}

func (w *scaleP21SW164Network) Write(raw []byte) (int, error) {
	if w.trace.producerDone && w.source.BoundedMetadataEnabled() && w.source.MetadataWorkUsedForTesting() != 0 {
		w.t.Fatal("bootstrap transport replay retained producer metadata")
	}
	return w.scaleP21SW145Network.Write(raw)
}

func scaleP21SW164Serve(t *testing.T, f scaleP21SW145Fixture, source *store.SQLiteStore, test scaleP21SW164Case) (scaleP21SW145Response, scaleP21SW164Trace) {
	t.Helper()
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(test.spool))
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	service := &server{store: source, authDisabled: test.development}
	request := f.literalRequest(test.user, test.key, nil)
	request.Method, request.URL.Path = http.MethodGet, "/api/bootstrap"
	request.Header.Set("X-Workspace-Key", test.workspace)
	request.SetPathValue("workspaceKey", test.workspace)
	request.SetPathValue("workspace", test.workspace)
	request.Header.Set("X-Flow-Projection", test.projection)
	if test.signed {
		request.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
	}
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	trace := scaleP21SW164Trace{}
	ctx = store.WithSQLStatementObserver(ctx, func(query string) {
		trace.statements++
		if strings.Contains(query, "workspace_content_records") {
			if trace.headerSeen {
				trace.contentAfter++
			} else {
				trace.contentBefore++
			}
		}
		if trace.headerSeen && strings.Contains(query, "issue_records") {
			trace.issueAfter++
		}
	})
	request, closeOutput := beginRequestResourceReads(request.WithContext(ctx))
	defer closeOutput()
	trace.scope, _ = request.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
	writer := &scaleP21SW164Network{
		scaleP21SW145Network: &scaleP21SW145Network{ResponseRecorder: httptest.NewRecorder(), trace: &trace.scaleP21SW145Trace},
		t:                    t, source: source,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/bootstrap", serializeLegacyBootstrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signing, _ := r.Context().Value(hydratedURLSigningKey{}).(*hydratedURLSigning); signing != nil {
			signing.expires = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		}
		if test.cancelBefore {
			cancel()
		}
		hook := test.atHeader
		if test.cancelAtHeader {
			hook = cancel
		}
		producer := &scaleP21SW164Producer{
			scaleP21SW145Producer: &scaleP21SW145Producer{ResponseWriter: w, t: t, source: source, trace: &trace.scaleP21SW145Trace},
			trace:                 &trace, hook: hook,
		}
		defer func() { trace.producerDone = true }()
		service.bootstrap(producer, r)
	})))
	beforeDecodes := source.WorkspaceMetadataDecodes()
	trace.started = time.Now()
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered != http.ErrAbortHandler {
					panic(recovered)
				}
				trace.aborted = true
			}
		}()
		service.signedFileURLs(mux).ServeHTTP(writer, request)
	}()
	trace.ended = time.Now()
	closeOutput()
	closeOutput()
	if !trace.scope.closed || source.MetadataWorkUsedForTesting() != 0 ||
		source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() != beforeDecodes {
		t.Fatal("bootstrap retained operation work or hydrated full bounded metadata")
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("bootstrap retained spool files", files, err)
	}
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "1")
	spool, err := store.NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := spool.Write([]byte("x"))
	if closeErr := spool.Close(); writeErr != nil || closeErr != nil {
		t.Fatal("bootstrap leaked spool allowance", writeErr, closeErr)
	}
	header := writer.Header().Clone()
	for index, value := range header["Server-Timing"] {
		header["Server-Timing"][index] = scaleP21SW145TimingDuration.ReplaceAllString(value, "${1}<duration>")
	}
	return scaleP21SW145Response{writer.Code, header, bytes.Clone(writer.Body.Bytes())}, trace
}

func scaleP21SW164Decode(t *testing.T, raw []byte) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal("invalid bootstrap/error JSON", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		t.Fatal("bootstrap contains trailing output", err)
	}
	return value
}

func scaleP21SW164DevelopmentClock(t *testing.T, value any, trace scaleP21SW164Trace) {
	t.Helper()
	root := value.(map[string]any)
	viewerID := root["viewer"].(map[string]any)["id"]
	found := 0
	for _, raw := range root["members"].([]any) {
		member := raw.(map[string]any)
		seen, present := member["lastSeenAt"]
		if member["user"].(map[string]any)["id"] != viewerID {
			if present && seen != nil {
				t.Fatal("development synthesis generated a non-viewer clock", seen)
			}
			continue
		}
		text, ok := seen.(string)
		stamp, err := time.Parse(time.RFC3339Nano, text)
		if !ok || err != nil || stamp.Before(trace.started) || stamp.After(trace.ended) {
			t.Fatal("synthesized viewer clock escaped its own request interval", seen, trace.started, trace.ended, err)
		}
		// materializeDevelopmentMembers generates only this native clock.
		// Every other decoded field and timestamp remains an exact comparison.
		member["lastSeenAt"] = "<validated-synthesized-viewer-clock>"
		found++
	}
	if found != 1 {
		t.Fatal("development synthesis did not generate exactly one viewer clock", found)
	}
}

func scaleP21SW164Members(t *testing.T, repo, view *store.SQLiteStore, workspace string, synthesized bool) {
	t.Helper()
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw164_members", "", nil, func(data *domain.Bootstrap) error {
		data.Members = nil
		if synthesized {
			return nil
		}
		stamp := time.Date(2026, 11, 15, 12, 13, 14, 123456789, time.UTC)
		for _, user := range data.Users {
			member := domain.WorkspaceMember{User: user, Role: "member", Status: "active", JoinedAt: data.Workspace.CreatedAt}
			if user.ID == data.Viewer.ID {
				member.Role, member.LastSeenAt = "admin", &stamp
			}
			data.Members = append(data.Members, member)
		}
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW164GeneralBootstrapHTTP(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "sw164-fixed-signing-fixture")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	f := scaleP21SW164Seed(t, repo)
	if err := f.service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	workspace, pairs := f.data.Workspace.URLKey, 0
	view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
	if err != nil {
		t.Fatal(err)
	}
	compare := func(t *testing.T, test scaleP21SW164Case) (scaleP21SW145Response, scaleP21SW164Trace) {
		t.Helper()
		legacyCase := test
		legacyCase.atHeader = nil
		old, oldTrace := scaleP21SW164Serve(t, f, repo, legacyCase)
		bounded, boundedTrace := scaleP21SW164Serve(t, f, view, test)
		if old.status != bounded.status || !reflect.DeepEqual(old.header, bounded.header) || oldTrace.aborted != boundedTrace.aborted {
			t.Fatalf("actual /api/bootstrap status/headers/error differ: status=%d/%d headers=%v/%v aborted=%t/%t legacy_prefix=%s bounded_prefix=%s",
				old.status, bounded.status, old.header, bounded.header, oldTrace.aborted, boundedTrace.aborted,
				old.body[:min(240, len(old.body))], bounded.body[:min(1000, len(bounded.body))])
		}
		if oldTrace.aborted {
			if !test.cancelAtHeader && !test.cancelBefore || !bytes.Equal(old.body, bounded.body) {
				t.Fatalf("canceled bootstrap output/error prefix differs: legacy=%s bounded=%s", old.body[:min(300, len(old.body))], bounded.body[:min(300, len(bounded.body))])
			}
		} else {
			oldJSON, boundedJSON := scaleP21SW164Decode(t, old.body), scaleP21SW164Decode(t, bounded.body)
			if test.synthesizedMembers {
				scaleP21SW164DevelopmentClock(t, oldJSON, oldTrace)
				scaleP21SW164DevelopmentClock(t, boundedJSON, boundedTrace)
				t.Log("development synthesis: only viewer member.LastSeenAt is request-interval validated before substitution; all other fields and timestamps compare exactly")
			}
			if !reflect.DeepEqual(oldJSON, boundedJSON) {
				t.Fatal("complete decoded bootstrap/error output differs", scaleP21M2Diff(oldJSON, boundedJSON))
			}
		}
		if test.cancelBefore || test.cancelAtHeader || strings.HasSuffix(test.workspace, "-missing") {
			if !oldTrace.aborted && old.status < 400 {
				t.Fatal("error witness returned a successful bootstrap", old.status)
			}
		} else {
			if old.status != http.StatusOK || boundedTrace.held == 0 || boundedTrace.producerWrites == 0 {
				t.Fatal("general bootstrap did not encode through owned output", old.status, boundedTrace.held)
			}
			if _, err := strconv.ParseInt(bounded.header.Get(changeCursorHeader), 10, 64); err != nil {
				t.Fatal("bootstrap omitted its exact snapshot cursor", err)
			}
			var data domain.Bootstrap
			if err := json.Unmarshal(bounded.body, &data); err != nil || data.IssueCollectionPaged {
				t.Fatal("general route became the separately proved paged bootstrap", err)
			}
			if test.projection == "lean" {
				if len(data.Comments) != 0 || len(data.Activities) != 0 || bytes.Contains(bounded.body, []byte("SW164_ISSUE_BEFORE")) {
					t.Fatal("lean bootstrap retained full issue/content fields")
				}
			} else if test.key == nil && test.user.ID == f.data.Viewer.ID {
				if !bytes.Contains(bounded.body, []byte("SW164_ISSUE_BEFORE")) || !bytes.Contains(bounded.body, []byte("SW164_COMMENT_BEFORE")) || len(bounded.body) <= 4<<20 {
					t.Fatal("full general bootstrap did not exercise legal >2MiB issue and content bodies")
				}
			}
			for _, updates := range data.ProjectUpdates {
				for _, update := range updates {
					if update.Snapshot != nil {
						t.Fatal("general bootstrap retained an internal pulse snapshot")
					}
				}
			}
			for _, updates := range data.InitiativeUpdates {
				for _, update := range updates {
					if update.Snapshot != nil {
						t.Fatal("general bootstrap retained an internal initiative snapshot")
					}
				}
			}
			for _, loop := range data.Loops {
				if loop.ID == f.loop && loop.RunCount30d != 1 {
					t.Fatal("bootstrap lost literal loop counts", loop.RunCount30d)
				}
			}
			if test.signed && (bytes.Contains(bounded.body, []byte("/uploads/")) || !bytes.Contains(bounded.body, []byte("/api/files/"))) {
				t.Fatal("bootstrap signatures or nested signed references differ")
			}
			if test.signed && test.spool < 1024*1024 && (oldTrace.direct == 0 || boundedTrace.direct == 0) {
				t.Fatal("bootstrap response did not exercise direct spool fallback")
			}
			if test.key == nil && test.user.ID == f.data.Viewer.ID {
				t.Logf("source=%q projection=%q signed=%t spool=%d legacy_sql=%d bounded_sql=%d legacy_content_sql=%d+%d bounded_content_sql=%d+%d legacy_post_header_issue_sql=%d bounded_post_header_issue_sql=%d",
					test.sourceClass, test.projection, test.signed, test.spool, oldTrace.statements, boundedTrace.statements,
					oldTrace.contentBefore, oldTrace.contentAfter, boundedTrace.contentBefore, boundedTrace.contentAfter, oldTrace.issueAfter, boundedTrace.issueAfter)
			}
		}
		pairs++
		return bounded, boundedTrace
	}
	f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
		for _, projection := range []string{"", "lean"} {
			t.Run("projection="+projection, func(t *testing.T) {
				compare(t, scaleP21SW164Case{scaleP21SW145Case: scaleP21SW145Case{workspace: workspace, projection: projection, user: user, key: key, spool: 32 << 20}, sourceClass: "initial-unknown-stamp-canonical-fallback"})
			})
		}
	})
	for _, projection := range []string{"", "lean"} {
		for _, mode := range []string{"development-synthesis", "development", "app-empty", "signed", "reloaded", "installed-warm"} {
			t.Run(mode+"/projection="+projection, func(t *testing.T) {
				development := mode == "development" || mode == "development-synthesis"
				if development {
					scaleP21SW164Members(t, repo, view, workspace, mode == "development-synthesis")
				}
				if mode == "reloaded" {
					if err := repo.ForgetInstalledMetadataRevisionForTesting(workspace); err != nil {
						t.Fatal(err)
					}
					if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := view.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
						t.Fatal(err)
					}
				}
				test := scaleP21SW164Case{scaleP21SW145Case: scaleP21SW145Case{workspace: workspace, projection: projection, user: f.data.Viewer, spool: 32 << 20, development: development, signed: mode == "signed"}, synthesizedMembers: mode == "development-synthesis", sourceClass: "fixture-current"}
				if development {
					test.user = domain.User{}
				}
				if mode == "reloaded" || mode == "installed-warm" {
					test.sourceClass = "certified-installed-after-reload"
				}
				if mode == "app-empty" {
					test.key = &domain.APIKey{ID: "sw164-app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}
				}
				compare(t, test)
			})
		}
	}
	for _, spool := range []int{0, 1024} {
		t.Run(fmt.Sprintf("signed-spool-%d", spool), func(t *testing.T) {
			compare(t, scaleP21SW164Case{scaleP21SW145Case: scaleP21SW145Case{workspace: workspace, user: f.data.Viewer, signed: true, spool: spool}})
		})
	}
	for _, mode := range []string{"missing-workspace", "cancel-before", "cancel-at-header"} {
		t.Run(mode, func(t *testing.T) {
			test := scaleP21SW164Case{scaleP21SW145Case: scaleP21SW145Case{workspace: workspace, user: f.data.Viewer, spool: 0}, cancelBefore: mode == "cancel-before", cancelAtHeader: mode == "cancel-at-header"}
			if mode == "missing-workspace" {
				test.workspace += "-missing"
			}
			compare(t, test)
		})
	}
	t.Run("snapshot-across-outline-and-five-walks", func(t *testing.T) {
		peer, err := store.OpenDatabase(config)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = peer.Close() })
		if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		changed := false
		test := scaleP21SW164Case{scaleP21SW145Case: scaleP21SW145Case{workspace: workspace, user: f.data.Viewer, spool: 0}}
		test.atHeader = func() {
			changed = true
			restore := store.ForceFullMutationsForTesting(true)
			err := peer.MutateWorkspace(t.Context(), workspace, "scale_p21.sw164_peer", "", nil, func(data *domain.Bootstrap) error {
				for index := range data.Issues {
					if data.Issues[index].ID == f.issue {
						data.Issues[index].Description = "SW164_ISSUE_AFTER"
					}
				}
				data.Comments[f.issue][0].Body = "SW164_COMMENT_AFTER"
				data.Activities[f.issue][0].Metadata["tag"] = "SW164_ACTIVITY_AFTER"
				data.Notifications[0].Title, data.NotificationDeliveries[0].Error = "SW164_NOTIFICATION_AFTER", "SW164_DELIVERY_AFTER"
				return nil
			})
			restore()
			if err != nil {
				t.Fatal("peer snapshot interleave failed", err)
			}
		}
		response, trace := compare(t, test)
		cursor, _ := strconv.ParseInt(response.header.Get(changeCursorHeader), 10, 64)
		head, err := peer.WorkspaceChangeHead(t.Context(), workspace)
		if err != nil || !changed || trace.contentBefore != 0 || trace.contentAfter < 4 || trace.issueAfter == 0 || head <= cursor {
			t.Fatal("snapshot witness did not precede all body walks or publish a newer cursor", changed, trace.contentBefore, trace.contentAfter, trace.issueAfter, cursor, head, err)
		}
		t.Logf("snapshot_cursor=%d committed_head=%d post_header_content_sql=%d post_header_issue_sql=%d", cursor, head, trace.contentAfter, trace.issueAfter)
	})
	t.Logf("actual /api/bootstrap pairs=%d; M matrix/full/lean/development; only synthesized viewer clock is interval-validated; signatures/cursor/errors/counts/body-spool ownership preserved", pairs)
}
