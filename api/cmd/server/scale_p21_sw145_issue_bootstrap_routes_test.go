package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW145Fixture struct {
	scaleP21M2Fixture
	other, project, document, issue, loop string
}

func scaleP21SW145Seed(t *testing.T, repo *store.SQLiteStore) scaleP21SW145Fixture {
	t.Helper()
	f := scaleP21M2Setup(t, repo)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw145-")
	result := scaleP21SW145Fixture{scaleP21M2Fixture: f, project: prefix + "project", document: prefix + "document", issue: prefix + "issue", loop: prefix + "loop"}
	stamp := time.Date(2026, time.November, 15, 12, 13, 14, 123456789, time.UTC)
	restore := store.ForceFullMutationsForTesting(true)
	err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw145_seed", "", nil, func(data *domain.Bootstrap) error {
		owner := f.data.Viewer
		owner.AvatarURL = "/uploads/sw145-actor.png"
		public := domain.Team{ID: f.public, Key: "SW145", Name: "Public"}
		for _, team := range data.Teams {
			if team.ID == f.public {
				public = team
			}
		}
		hiddenIssue := ""
		for _, issue := range data.Issues {
			if issue.Team.Private {
				hiddenIssue = issue.ID
				break
			}
		}
		issue := domain.Issue{
			ID: result.issue, Identifier: "SW145-901", Number: 901, Version: 1,
			Title: "SW145_HAND_ISSUE", Description: "Issue body /uploads/sw145-issue.png",
			Team: public, State: data.States[0], Creator: owner, CreatedAt: stamp, UpdatedAt: stamp,
		}
		data.Issues = append(data.Issues, issue)
		data.Comments[issue.ID] = []domain.Comment{{ID: prefix + "comment", Body: "SW145_HAND_COMMENT /uploads/sw145-comment.png", User: owner, CreatedAt: stamp, Version: 1}}
		data.Activities[issue.ID] = []domain.ActivityEvent{{ID: "SW145_HAND_ACTIVITY", Type: "created", Actor: owner, CreatedAt: stamp}}
		data.Notifications = []domain.Notification{{
			ID: prefix + "notification", RecipientID: owner.ID, Type: "comment", Category: "comments",
			SourceType: "comment", SourceID: prefix + "comment", IssueID: issue.ID,
			Title: "SW145_HAND_NOTIFICATION", Actor: owner, CreatedAt: stamp, UpdatedAt: stamp,
		}}
		data.NotificationDeliveries = []domain.NotificationDelivery{{
			ID: prefix + "delivery", NotificationID: prefix + "notification", RecipientID: owner.ID,
			Channel: "desktop", Status: "pending", Error: "SW145_HAND_DELIVERY", CreatedAt: stamp, UpdatedAt: stamp,
		}}
		data.Projects = append(data.Projects, domain.Project{
			ID: result.project, Name: "Visible\x00SW145_NATIVE /uploads/sw145-project-title.png",
			SlugID: prefix + "current", PreviousSlugIDs: []string{prefix + "old"},
			TeamIDs: []string{f.public}, DependencyIDs: []string{"m2-unrelated"},
			Description: "![first](/uploads/sw145-before.png) " + strings.Repeat("whole bootstrap body ", 3000) + " /uploads/sw145-after.png",
			Lead:        &owner, Creator: &owner, MemberIDs: []string{owner.ID}, IssueCount: 71,
			CreatedAt: stamp, UpdatedAt: stamp,
		})
		data.Documents = append(data.Documents, domain.Document{
			ID: result.document, SlugID: prefix + "document-current", PreviousSlugIDs: []string{prefix + "document-old"},
			Title: "Visible document", Content: "![document](/uploads/sw145-document.png) " + strings.Repeat("document body ", 2000),
			ContentData: map[string]any{"url": "/uploads/sw145-data.png", "nested": []any{true, 9007199254740992}},
			Creator:     owner, ProjectIDs: []string{result.project, "m2-unrelated"}, TeamIDs: []string{f.public},
			IssueID: hiddenIssue, CreatedAt: stamp, UpdatedAt: stamp,
		})
		data.Releases = append(data.Releases, domain.Release{
			ID: prefix + "release", Name: "Visible release", Description: "/uploads/sw145-release.png",
			ProjectIDs: []string{result.project, "m2-unrelated"}, IssueIDs: []string{issue.ID, hiddenIssue},
			Creator: owner, CreatedAt: stamp, UpdatedAt: stamp,
		})
		data.Loops = append(data.Loops, domain.Loop{
			ID: result.loop, SlugID: "stale-loop-slug", Name: "Visible loop", Status: "published",
			Instructions:     "# Deliver:\n**Read** /uploads/sw145-loop.png. " + strings.Repeat("kept loop body ", 1000),
			InstructionsData: map[string]any{"url": "/uploads/sw145-loop-data.png"},
			TeamID:           f.public, Level: "team", TeamAccess: "selected", OwnerID: owner.ID,
			Creator: owner, CreatedAt: stamp, UpdatedAt: stamp, PublishedAt: &stamp,
			OutputContract: &domain.LoopOutputContract{Source: loopContractPerson, InstructionsHash: "older", ReadOnly: true, ReadOnlyEvidence: "/uploads/sw145-evidence.png"},
		})
		data.ProjectUpdates[result.project] = []domain.ProjectUpdate{{ID: prefix + "update", ProjectID: result.project, Body: "OMITTED_PULSE_BODY", User: owner, CreatedAt: stamp}}
		for index := range data.Users {
			data.Users[index].AvatarURL = "/uploads/sw145-user.png"
		}
		for _, principal := range f.principals {
			settings := data.UserSettings[principal.user.ID]
			settings.UserID, settings.UpdatedAt = principal.user.ID, stamp
			data.UserSettings[principal.user.ID] = settings
			preferences := data.NotificationPreferences[principal.user.ID]
			preferences.UserID, preferences.UpdatedAt = principal.user.ID, stamp
			data.NotificationPreferences[principal.user.ID] = preferences
			data.Favorites = append(data.Favorites,
				domain.Favorite{ID: prefix + principal.user.ID + "-project", UserID: principal.user.ID, ResourceType: "project", ResourceID: result.project, CreatedAt: stamp},
				domain.Favorite{ID: prefix + principal.user.ID + "-old", UserID: principal.user.ID, ResourceType: "project", ResourceID: prefix + "old", CreatedAt: stamp},
				domain.Favorite{ID: prefix + principal.user.ID + "-issue", UserID: principal.user.ID, ResourceType: "issue", ResourceID: issue.ID, CreatedAt: stamp},
				domain.Favorite{ID: prefix + principal.user.ID + "-document", UserID: principal.user.ID, ResourceType: "document", ResourceID: result.document, CreatedAt: stamp},
			)
			data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: prefix + principal.user.ID + "-subscription", UserID: principal.user.ID, ResourceType: "issue", ResourceID: hiddenIssue, CreatedAt: stamp})
		}
		data.WorkspaceSettings.UpdatedAt = stamp
		return nil
	})
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateLoopRun(t.Context(), workspace, domain.LoopRun{
		ID: prefix + "run", LoopID: result.loop, Status: "completed", StartedAt: time.Now().UTC().Add(-time.Hour),
	}, store.LoopRunLease{}); err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.principals[2].user), "SW145 other", idgen.New("sw145-other-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
	result.other = other.Workspace.URLKey
	return result
}

type scaleP21SW145Case struct {
	name, workspace, projection  string
	user                         domain.User
	key                          *domain.APIKey
	development, handoff, signed bool
	spool                        int
	status                       int
}

type scaleP21SW145Trace struct {
	producerDone                   bool
	producerWrites, replay, direct int
	held                           int
	scope                          *requestResourceReads
}

type scaleP21SW145Producer struct {
	http.ResponseWriter
	t      *testing.T
	source *store.SQLiteStore
	trace  *scaleP21SW145Trace
	status int
}

func (w *scaleP21SW145Producer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *scaleP21SW145Producer) sample(code int) {
	work := w.source.MetadataWorkUsedForTesting()
	w.trace.held = max(w.trace.held, work)
	if w.source.BoundedMetadataEnabled() && code == http.StatusOK &&
		(work == 0 || w.trace.scope == nil || w.trace.scope.closed) {
		w.t.Fatal("bootstrap producer lost its read or request scope during encoding")
	}
}
func (w *scaleP21SW145Producer) WriteHeader(code int) {
	w.status = code
	w.sample(code)
	w.ResponseWriter.WriteHeader(code)
}
func (w *scaleP21SW145Producer) Write(body []byte) (int, error) {
	code := w.status
	if code == 0 {
		code = http.StatusOK
	}
	w.sample(code)
	w.trace.producerWrites++
	return w.ResponseWriter.Write(body)
}

type scaleP21SW145Network struct {
	*httptest.ResponseRecorder
	trace *scaleP21SW145Trace
}

func (w *scaleP21SW145Network) Write(body []byte) (int, error) {
	if w.trace.producerDone {
		w.trace.replay++
	} else {
		w.trace.direct++
	}
	return w.ResponseRecorder.Write(body)
}

type scaleP21SW145Response struct {
	status int
	header http.Header
	body   []byte
}

var scaleP21SW145TimingDuration = regexp.MustCompile(`(;[ \t]*dur=)[0-9.]+`)

func scaleP21SW145Serve(t *testing.T, f scaleP21SW145Fixture, source *store.SQLiteStore, test scaleP21SW145Case) (scaleP21SW145Response, scaleP21SW145Trace) {
	t.Helper()
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", strconv.Itoa(test.spool))
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	service := &server{store: source, authDisabled: test.development}
	request := f.literalRequest(test.user, test.key, nil)
	request.Method, request.URL.Path = http.MethodGet, "/api/issue-records/bootstrap"
	request.Header.Set("X-Workspace-Key", test.workspace)
	request.SetPathValue("workspaceKey", test.workspace)
	request.SetPathValue("workspace", test.workspace)
	request.Header.Set("X-Flow-Projection", test.projection)
	if test.signed {
		request.Header.Set("X-Flow-File-Urls-Expire-In", "3600")
	}
	releaseSnapshot := func() {}
	if test.handoff {
		ctx, release, err := f.repository.BeginWorkspaceRead(request.Context(), test.workspace)
		if err != nil {
			t.Fatal(err)
		}
		releaseSnapshot = release
		request = request.WithContext(ctx)
	}
	defer releaseSnapshot()
	request, closeOutput := beginRequestResourceReads(request)
	defer closeOutput()
	trace := scaleP21SW145Trace{}
	trace.scope, _ = request.Context().Value(requestResourceReadsKey{}).(*requestResourceReads)
	writer := &scaleP21SW145Network{ResponseRecorder: httptest.NewRecorder(), trace: &trace}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/issue-records/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		// Exercise the actual signing wrapper and registered handler, fixing
		// only its generated expiry before either encoder inspects it.
		if signing, _ := r.Context().Value(hydratedURLSigningKey{}).(*hydratedURLSigning); signing != nil {
			signing.expires = time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
		}
		producer := &scaleP21SW145Producer{ResponseWriter: w, t: t, source: source, trace: &trace}
		service.issueRecordsBootstrap(producer, r)
		if source.BoundedMetadataEnabled() && source.MetadataWorkUsedForTesting() != 0 {
			t.Fatal("bootstrap producer retained work after encoding and before transport replay")
		}
		trace.producerDone = true
	})
	beforeDecodes := source.WorkspaceMetadataDecodes()
	service.signedFileURLs(mux).ServeHTTP(writer, request)
	closeOutput()
	closeOutput()
	releaseSnapshot()
	if !trace.scope.closed || source.MetadataWorkUsedForTesting() != 0 ||
		source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() != beforeDecodes {
		t.Fatal("bootstrap transport retained request work or decoded full bounded metadata")
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("bootstrap transport retained temporary files", files, err)
	}
	// Check the shared spool counter as well as removal of its files.
	t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "1")
	spool, err := store.NewRecordSpool()
	if err != nil {
		t.Fatal(err)
	}
	_, err = spool.Write([]byte("x"))
	closeErr := spool.Close()
	if err != nil || closeErr != nil {
		t.Fatal("bootstrap transport leaked its spool allowance", err, closeErr)
	}
	header := writer.Header().Clone()
	for index, value := range header["Server-Timing"] {
		header["Server-Timing"][index] = scaleP21SW145TimingDuration.ReplaceAllString(value, "${1}<duration>")
	}
	return scaleP21SW145Response{writer.Code, header, bytes.Clone(writer.Body.Bytes())}, trace
}

func TestScaleP21SW145IssueBootstrapRegisteredRoutes(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_FILE_URL_SIGNING_KEY", "sw145-fixed-signing-fixture")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW145Seed(t, repo)
		workspace, pairs := f.data.Workspace.URLKey, 0
		compare := func(t *testing.T, view *store.SQLiteStore, test scaleP21SW145Case) {
			t.Helper()
			old, oldTrace := scaleP21SW145Serve(t, f, repo, test)
			bounded, boundedTrace := scaleP21SW145Serve(t, f, view, test)
			if old.status != test.status || bounded.status != old.status || !reflect.DeepEqual(old.header, bounded.header) || !bytes.Equal(old.body, bounded.body) {
				offset := 0
				for offset < min(len(old.body), len(bounded.body)) && old.body[offset] == bounded.body[offset] {
					offset++
				}
				t.Fatalf("registered bootstrap differs at byte=%d status=%d/%d want=%d headers=%v/%v lengths=%d/%d\nold=%s\nbounded=%s",
					offset, old.status, bounded.status, test.status, old.header, bounded.header, len(old.body), len(bounded.body),
					old.body[max(0, offset-100):min(len(old.body), offset+300)], bounded.body[max(0, offset-100):min(len(bounded.body), offset+300)])
			}
			if test.status == http.StatusOK {
				var data domain.Bootstrap
				if err := json.Unmarshal(bounded.body, &data); err != nil {
					t.Fatal(err)
				}
				if !data.IssueCollectionPaged || boundedTrace.held == 0 || boundedTrace.producerWrites == 0 {
					t.Fatal("bounded bootstrap lost paging or encoding ownership")
				}
				if len(data.ProjectUpdates) != 0 || len(data.InitiativeUpdates) != 0 {
					t.Fatal("bootstrap retained paged pulse updates")
				}
				if !test.handoff && (len(data.Issues) != 0 || len(data.Comments) != 0 || len(data.Activities) != 0 ||
					len(data.Notifications) != 0 || len(data.NotificationDeliveries) != 0) {
					t.Fatal("ordinary bootstrap retained one of the five heavy collections")
				}
				if test.signed {
					if bytes.Contains(bounded.body, []byte("/uploads/")) || !bytes.Contains(bounded.body, []byte("/api/files/")) {
						t.Fatal("signed bootstrap left upload references or produced no signed URLs")
					}
					if test.spool >= 4<<20 {
						if oldTrace.replay == 0 || boundedTrace.replay == 0 || oldTrace.direct != 0 || boundedTrace.direct != 0 {
							t.Fatal("full bootstrap spool did not detach and replay both encoders", oldTrace, boundedTrace)
						}
					} else if oldTrace.direct == 0 || boundedTrace.direct == 0 {
						t.Fatal("small bootstrap spool did not exercise direct overflow", oldTrace, boundedTrace)
					}
				}
			}
			pairs++
		}
		for _, reload := range []bool{false, true} {
			if reload {
				if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
			}
			view, err := repo.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace, f.other)
			if err != nil {
				t.Fatal(err)
			}
			credentials := []struct {
				name string
				user domain.User
				key  *domain.APIKey
			}{
				{"admin-private-outsider", f.principals[0].user, nil},
				{"private-admin", f.principals[1].user, nil},
				{"member", f.principals[2].user, nil},
				{"guest-explicit", f.principals[3].user, nil},
				{"inherited-selected", f.principals[4].user, &domain.APIKey{ID: "sw145-selected", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.public}}},
				{"app-selected-empty", f.principals[0].user, &domain.APIKey{ID: "sw145-app", Actor: "app", Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}}},
			}
			for _, credential := range credentials {
				for _, projection := range []string{"", "project-list", "issue-detail"} {
					test := scaleP21SW145Case{name: credential.name, workspace: workspace, projection: projection, user: credential.user, key: credential.key, spool: 4 << 20, status: http.StatusOK}
					t.Run(fmt.Sprintf("reload=%t/%s/projection=%s", reload, test.name, projection), func(t *testing.T) { compare(t, view, test) })
				}
			}
			for _, projection := range []string{"", "project-list", "issue-detail"} {
				for _, mode := range []string{"development", "public-sql-snapshot"} {
					test := scaleP21SW145Case{name: mode, workspace: workspace, projection: projection, user: f.data.Viewer, development: mode == "development", handoff: mode == "public-sql-snapshot", spool: 4 << 20, status: http.StatusOK}
					t.Run(fmt.Sprintf("reload=%t/%s/projection=%s", reload, mode, projection), func(t *testing.T) { compare(t, view, test) })
				}
			}
			for _, test := range []scaleP21SW145Case{
				{name: "cross-workspace", workspace: f.other, user: f.data.Viewer, spool: 4 << 20, status: http.StatusForbidden},
				{name: "development-missing", workspace: idgen.New("sw145-absent-"), user: f.data.Viewer, development: true, spool: 4 << 20, status: http.StatusNotFound},
			} {
				t.Run(fmt.Sprintf("reload=%t/%s", reload, test.name), func(t *testing.T) { compare(t, view, test) })
			}
			if !reload {
				for _, budget := range []int{0, 1024, 4 << 20} {
					test := scaleP21SW145Case{name: "signed", workspace: workspace, user: f.principals[1].user, signed: true, spool: budget, status: http.StatusOK}
					t.Run(fmt.Sprintf("signed/spool=%d", budget), func(t *testing.T) { compare(t, view, test) })
				}
			}
		}
		t.Logf("exact registered bootstrap pairs=%d; only Server-Timing durations excluded; preference clocks and URL expiry fixed", pairs)
	}
}
