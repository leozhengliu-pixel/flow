package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

func TestScaleP16EnabledPermissionsAndSideEffects(t *testing.T) {
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	t.Run("permissions", TestScaleP16PermissionDifferential)
	t.Run("sql-events-webhooks", TestScaleP16MutationSideEffectsDifferential)
	t.Run("queued-and-replayed-revocation", TestScaleP16QueuedAndReplayRevocation)
}

func scaleP16CascadeFixture(t *testing.T, db *scaleP04Database) (string, string, string) {
	t.Helper()
	data, _ := db.repo.BootstrapFor(db.key)
	projectID, initiativeID := data.Projects[0].ID, "p16-cascade-initiative"
	teamID := data.Teams[0].ID
	if err := db.repo.MutateWorkspace(t.Context(), db.key, "scale_p16.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "p16-move-team", Name: "Move", Key: "P16M"})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: initiativeID, Name: "Cascade", LeadTeamID: teamID, ProjectIDs: []string{projectID}})
		for i := range 70 {
			id := fmt.Sprintf("p16-linked-%03d", i)
			data.Projects = append(data.Projects, domain.Project{ID: id, Name: id, TeamIDs: []string{teamID}, DependencyIDs: []string{projectID}, Initiatives: []string{initiativeID}, Description: strings.Repeat("body ", 100)})
			data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative-" + id, Name: id, ParentInitiativeIDs: []string{initiativeID}, LeadTeamID: teamID})
			data.Documents = append(data.Documents, domain.Document{ID: "document-" + id, Title: id, ProjectIDs: []string{projectID, id}, InheritedTeamIDs: []string{teamID}, Content: strings.Repeat("body ", 200)})
			data.ProjectRelations = append(data.ProjectRelations, domain.ProjectRelation{ID: "relation-" + id, ProjectID: projectID, RelatedProjectID: id, Type: "related"})
			data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: "initiative-relation-" + id, InitiativeID: "initiative-" + id, RelatedInitiativeID: initiativeID, Type: "parent"})
			for _, resource := range []struct{ kind, id string }{{"project", projectID}, {"initiative", initiativeID}} {
				key := resource.kind + "-" + id
				data.Favorites = append(data.Favorites, domain.Favorite{ID: "favorite-" + key, UserID: data.Viewer.ID, ResourceType: resource.kind, ResourceID: resource.id})
				data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "subscription-" + key, UserID: data.Viewer.ID, ResourceType: resource.kind, ResourceID: resource.id})
				data.Drafts = append(data.Drafts, domain.Draft{ID: "draft-" + key, UserID: data.Viewer.ID, Type: resource.kind, ResourceID: resource.id, Body: "keep on restore"})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var issues []domain.Issue
	for i := range 70 {
		issue := data.Issues[0]
		issue.ID, issue.Identifier, issue.Title = fmt.Sprintf("p16-issue-%03d", i), fmt.Sprintf("P16-%d", i+1), "Cascade issue"
		issue.Project = &domain.ProjectSummary{ID: projectID}
		issue.ArchivedAt = nil
		issues = append(issues, issue)
	}
	if err := db.repo.ImportIssues(t.Context(), db.key, issues); err != nil {
		t.Fatal(err)
	}
	return projectID, initiativeID, "p16-move-team"
}

func scaleP16DatabaseState(t *testing.T, db *scaleP04Database) map[string]string {
	t.Helper()
	replace := []string{db.key, "fixture-workspace-key", db.workspaceID, "fixture-workspace"}
	for i, user := range db.users {
		replace = append(replace, user.ID, fmt.Sprintf("fixture-user-%d", i), user.Email, fmt.Sprintf("fixture-user-%d@example.test", i))
	}
	identity := strings.NewReplacer(replace...)
	result := map[string]string{}
	for name, query := range map[string]string{
		"metadata": "SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key",
		"issues":   "SELECT id,data FROM issue_records WHERE workspace_key=? ORDER BY id",
		"content":  "SELECT kind,resource_id,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id",
		"search":   "SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=? ORDER BY field,record_key",
		"events":   "SELECT event_type,aggregate_id,payload,previous_values FROM domain_events WHERE workspace_id=? ORDER BY created_at,id",
		"audit":    "SELECT actor_type,actor_id,user_id,actor,action,resource_type,resource_id,result,metadata FROM audit_events WHERE workspace_id=? ORDER BY seq",
	} {
		arg := db.key
		if name == "events" || name == "audit" {
			arg = db.workspaceID
		}
		rows := db.query(t, query, arg)
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var records []string
		for rows.Next() {
			values := make([]sql.NullString, len(columns))
			args := make([]any, len(values))
			for i := range values {
				args[i] = &values[i]
			}
			if err := rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			var record []string
			for _, value := range values {
				record = append(record, scaleP04Normalize([]byte(identity.Replace(value.String))))
			}
			if name == "metadata" && (values[0].String == "projectUpdates" || values[0].String == "initiativeUpdates") {
				var update struct {
					Parent string `json:"parent"`
					Item   struct {
						ID string `json:"id"`
					} `json:"item"`
				}
				if err := json.Unmarshal([]byte(values[3].String), &update); err != nil {
					t.Fatal(err)
				}
				key := []byte("parent:" + update.Parent)
				if update.Item.ID != "" {
					key, _ = json.Marshal([]string{update.Parent, update.Item.ID})
				}
				if values[1].String != fmt.Sprintf("%x", sha256.Sum256(key)) {
					t.Fatal("invalid split update identity")
				}
				record[1] = scaleP04Normalize([]byte(identity.Replace(string(key))))
			}
			raw, _ := json.Marshal(record)
			records = append(records, string(raw))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if name == "metadata" || name == "search" {
			slices.Sort(records)
		}
		raw, _ := json.Marshal(records)
		result[name] = string(raw)
	}
	return result
}

func TestScaleP16PaginatedCascadesDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	var want map[string]string
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
		t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", mode)
		db := newScaleP04Database(t)
		project, initiative, team := scaleP16CascadeFixture(t, db)
		base := db.handler(t, true)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Request-ID", "scale-p16-cascade")
			base.ServeHTTP(w, r)
		})
		requestJSON[any](t, handler, "PATCH", "/api/projects/"+project, map[string]any{"archived": true}, 200)
		requestJSON[any](t, handler, "PATCH", "/api/projects/"+project, map[string]any{"archived": false, "teamIds": []string{team}}, 200)
		requestJSON[any](t, handler, "PATCH", "/api/initiatives/"+initiative, map[string]any{"name": "Reconcile links"}, 200)
		requestJSON[any](t, handler, "DELETE", "/api/initiatives/"+initiative, nil, 204)
		requestJSON[any](t, handler, "DELETE", "/api/projects/"+project, nil, 204)
		got := scaleP16DatabaseState(t, db)
		if mode == "off" {
			want = got
		} else {
			for kind, value := range want {
				if got[kind] != value {
					i := 0
					for i < len(value) && i < len(got[kind]) && value[i] == got[kind][i] {
						i++
					}
					t.Errorf("%s differs at %d: old %.350s new %.350s", kind, i, value[i:], got[kind][i:])
				}
			}
		}
	}
}

func TestScaleP16CascadeCrashResume(t *testing.T) {
	if workspace := os.Getenv("FLOW_P16_CRASH_WORKSPACE"); workspace != "" {
		repo, err := store.OpenDatabase(scaleP04DatabaseConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		_, token, err := repo.Login(t.Context(), os.Getenv("FLOW_P16_CRASH_USER"), scaleP04Password)
		if err != nil {
			t.Fatal(err)
		}
		service := &server{store: repo, uploadPath: t.TempDir()}
		handler := newHandler(service)
		if err := service.stopDeliveryScheduler(t.Context()); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("FLOW_P16_CRASH_WARMUP") == "1" {
			return
		}
		request := httptest.NewRequest("DELETE", "/api/projects/"+os.Getenv("FLOW_P16_CRASH_PROJECT"), nil)
		request.Header.Set("X-Workspace-Key", workspace)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		ctx := store.WithProjectRecordPageObserver(request.Context(), func(field string, page int) error {
			if field == "issues" && page == 2 {
				os.Exit(71)
			}
			return nil
		})
		handler.ServeHTTP(httptest.NewRecorder(), request.WithContext(ctx))
		t.Fatal("crash probe did not reach the second issue page")
	}
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
	t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	db := newScaleP04Database(t)
	project, _, _ := scaleP16CascadeFixture(t, db)
	_ = db.handler(t, true)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScaleP16CascadeCrashResume$", "-test.v")
	url := db.config.URL
	if db.config.Driver == "sqlite" {
		url = db.config.Path
	}
	command.Env = append(os.Environ(), "FLOW_TEST_DATABASE_DRIVER="+db.config.Driver, "FLOW_TEST_DATABASE_URL="+url, "FLOW_P16_CRASH_WORKSPACE="+db.key, "FLOW_P16_CRASH_PROJECT="+project, "FLOW_P16_CRASH_USER="+db.users[0].Email)
	warmup := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScaleP16CascadeCrashResume$")
	warmup.Env = append(slices.Clone(command.Env), "FLOW_P16_CRASH_WARMUP=1")
	if output, err := warmup.CombinedOutput(); err != nil {
		t.Fatalf("crash probe warmup: %v %.1000s", err, output)
	}
	before := scaleP16DatabaseState(t, db)
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 71 {
		t.Fatalf("expected process interruption 71: %v %.1000s", err, output)
	}
	afterCrash := scaleP16DatabaseState(t, db)
	if !reflect.DeepEqual(before, afterCrash) {
		scaleP16SaveDifferential(t, "crash-before", before)
		scaleP16SaveDifferential(t, "crash-after", afterCrash)
		for name := range before {
			if before[name] != afterCrash[name] {
				t.Errorf("interrupted transaction changed %s", name)
			}
		}
		t.FailNow()
	}
	db.reopen(t)
	handler := db.handler(t, true)
	var committedAt time.Time
	db.repo.SetRealtimeSink(func(workspace string, event domain.RealtimeEvent) {
		if workspace == db.key && event.Type == "project.deleted" {
			committedAt = event.CreatedAt
		}
	})
	requestJSON[any](t, handler, "DELETE", "/api/projects/"+project, nil, 204)
	data, _ := db.repo.BootstrapFor(db.key)
	for _, issue := range data.Issues {
		if strings.HasPrefix(issue.ID, "p16-issue-") && (issue.Project != nil || issue.ArchivedAt == nil) {
			t.Fatal("resumed cascade missed an issue")
		}
	}
	count := 0
	for _, entry := range data.Trash {
		if entry.ResourceID == project {
			count++
			if entry.Links == nil || len(entry.Links.ProjectIssues) < 70 {
				t.Fatal("resumed cascade lost restore links")
			}
		}
	}
	if count != 1 {
		t.Fatalf("resumed cascade created %d trash entries", count)
	}
	for id, activities := range data.Activities {
		if strings.HasPrefix(id, "p16-issue-") {
			for _, activity := range activities {
				if activity.CreatedAt.After(committedAt) {
					t.Fatal("cascade event precedes its activities")
				}
			}
		}
	}
}

func TestScaleP16UpdateNotificationsDifferential(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	var want map[string]string
	for _, mode := range []string{"off", "on"} {
		t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", mode)
		t.Setenv("FLOW_INITIATIVE_RECORD_MUTATIONS", mode)
		db := newScaleP04Database(t)
		data, _ := db.repo.BootstrapFor(db.key)
		base := db.handler(t, false)
		host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Request-ID", "p16-update-notifications")
			base.ServeHTTP(w, r)
		}))
		t.Cleanup(host.Close)
		owner, commenter := db.login(t, host.URL, db.users[0]), db.login(t, host.URL, db.users[1])
		initiative := authRequest[domain.Initiative](t, owner, "POST", host.URL+"/api/initiatives", map[string]any{"name": "Updates", "leadTeamId": data.Teams[0].ID, "projectIds": []string{data.Projects[0].ID}}, db.key, 201)
		for _, path := range []string{"/api/projects/" + data.Projects[0].ID, "/api/initiatives/" + initiative.ID} {
			update := authRequest[map[string]any](t, owner, "POST", host.URL+path+"/updates", map[string]string{"body": "Owner update", "health": "onTrack"}, db.key, 201)
			id, _ := update["id"].(string)
			authRequest[map[string]any](t, commenter, "POST", host.URL+path+"/updates/"+id+"/comments", map[string]string{"body": "Member reply"}, db.key, 201)
			authRequest[map[string]any](t, commenter, "POST", host.URL+path+"/updates/"+id+"/reactions", map[string]string{"emoji": "+1"}, db.key, 200)
		}
		data, _ = db.repo.BootstrapFor(db.key)
		comments, reactions := 0, 0
		for _, item := range data.Notifications {
			if item.RecipientID != db.users[0].ID {
				continue
			}
			if item.Type == notificationUpdateComment {
				comments++
			}
			if item.Type == notificationUpdateReaction {
				reactions++
			}
		}
		if comments != 2 || reactions != 2 {
			t.Fatalf("%s: missing author notifications: comments=%d reactions=%d", mode, comments, reactions)
		}
		got := scaleP16DatabaseState(t, db)
		if mode == "off" {
			want = got
		} else {
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s notification differential differs", key)
				}
			}
		}
	}
}

func TestScaleP16MixedReplicaWrites(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	db := newScaleP04Database(t)
	data, _ := db.repo.BootstrapFor(db.key)
	other, err := store.OpenDatabase(db.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	_, token, err := other.Login(t.Context(), db.users[0].Email, scaleP04Password)
	if err != nil {
		t.Fatal(err)
	}
	service := &server{store: other, uploadPath: t.TempDir()}
	second := newHandler(service)
	if err := service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	handlers := []http.Handler{db.handler(t, true), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Workspace-Key", db.key)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		second.ServeHTTP(w, r)
	})}
	path := "/api/projects/" + data.Projects[0].ID
	for i, step := range []struct {
		mode  string
		input any
	}{
		{"off", map[string]string{"name": "Legacy writer"}},
		{"on", map[string]string{"description": "Record writer"}},
		{"off", map[string]int{"priority": 3}},
		{"on", map[string]string{"color": "#123456"}},
	} {
		t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", step.mode)
		requestJSON[domain.Project](t, handlers[i%2], "PATCH", path, step.input, 200)
	}
	rows := db.query(t, "SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects' AND record_key='project_aut'", db.key)
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("mixed writer lost the project")
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	if project.Name != "Legacy writer" || project.Description != "Record writer" || project.Priority != 3 || project.Color != "#123456" {
		t.Fatalf("mixed writers lost fields: %+v", project)
	}
}

func TestScaleP16TeamMoveWithEarlierValidationRecord(t *testing.T) {
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_PROJECT_RECORD_MUTATIONS", "on")
	t.Setenv("FLOW_NARROW_MUTATION_VERIFY", "off")
	db := newScaleP04Database(t)
	data, _ := db.repo.BootstrapFor(db.key)
	ctx := store.ContextWithActor(t.Context(), db.users[0])
	if err := db.repo.MutateWorkspace(ctx, db.key, "scale_p16.seed", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: "p16-move-private", Key: "P16MV", Name: "Private", Private: true})
		data.TeamSettings["p16-move-private"] = domain.TeamSettings{TeamID: "p16-move-private", Access: "private"}
		data.Projects = append(data.Projects, domain.Project{ID: "p16-last-project", Name: "Last", TeamIDs: []string{data.Teams[0].ID}})
		data.Documents = append(data.Documents, domain.Document{ID: "p16-last-document", Title: "Preserved", Version: 1, ProjectIDs: []string{"p16-last-project"}, Content: "Body retained", InheritedTeamIDs: []string{data.Teams[0].ID}, Revisions: []domain.DocumentRevision{{ID: "p16-version", Content: "Body retained"}}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.repo.SetTeamMembership(ctx, db.workspaceID, "p16-move-private", db.users[0].ID, "member", true); err != nil {
		t.Fatal(err)
	}
	requestJSON[domain.Project](t, db.handler(t, true), "PATCH", "/api/projects/p16-last-project", map[string]any{"teamIds": []string{"p16-move-private"}, "dependencyIds": []string{data.Projects[0].ID}}, 200)
	rows := db.query(t, "SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='documents' AND record_key='p16-last-document'", db.key)
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("team move lost its document")
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var document struct {
		Content          string                       `json:"content"`
		InheritedTeamIDs []string                     `json:"inheritedTeamIds"`
		Revisions        []map[string]json.RawMessage `json:"revisions"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Content != "Body retained" || !slices.Equal(document.InheritedTeamIDs, []string{"p16-move-private"}) || len(document.Revisions) != 1 || document.Revisions[0]["_offloaded"] == nil {
		t.Fatal("team move used a validation record's teams or rewrote document history")
	}
}

func scaleP16SaveDifferential(t *testing.T, name string, data map[string]string) {
	t.Helper()
	if dir := os.Getenv("FLOW_P16_DIFFERENTIAL_DIR"); dir != "" {
		raw, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/"+name+".json", raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
