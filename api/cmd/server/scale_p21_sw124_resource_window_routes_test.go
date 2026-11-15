package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW124Writer struct {
	*httptest.ResponseRecorder
	t       *testing.T
	source  *store.SQLiteStore
	held    int
	headers int
	writes  int
	started time.Time
	ended   time.Time
}

func (w *scaleP21SW124Writer) sample(code int) {
	work := w.source.MetadataWorkUsedForTesting()
	w.held = max(w.held, work)
	if w.source.BoundedMetadataEnabled() && code == http.StatusOK && work == 0 {
		w.t.Fatal("resource-window output lost its owner before encoding completed")
	}
}

func (w *scaleP21SW124Writer) WriteHeader(code int) {
	w.sample(code)
	w.headers++
	w.ResponseRecorder.WriteHeader(code)
}

func (w *scaleP21SW124Writer) Write(raw []byte) (int, error) {
	w.sample(w.Code)
	w.writes++
	return w.ResponseRecorder.Write(raw)
}

func scaleP21SW124Key(prefix string, user domain.User, key *domain.APIKey) *domain.APIKey {
	value := scaleP21SW104StoredKey(prefix, user, key)
	if value != nil {
		value.SecretHash = secretHash("sw124-" + value.ID)
	}
	return value
}

func scaleP21SW124Response(t *testing.T, writer *scaleP21SW124Writer, path string, development bool, viewerID string) []byte {
	t.Helper()
	core := strings.HasPrefix(path, "/api/sync/manifest?surface=resource-window")
	materialized := development && strings.HasPrefix(path, "/api/resource-windows/members?collection=members")
	if writer.Code != http.StatusOK || !core && !materialized {
		return writer.Body.Bytes()
	}
	decoder := json.NewDecoder(bytes.NewReader(writer.Body.Bytes()))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	clock := func(record map[string]any, field string) {
		t.Helper()
		raw, ok := record[field].(string)
		if !ok {
			t.Fatal("generated resource-window clock missing", field)
		}
		stamp, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || stamp.Before(writer.started.Add(-time.Millisecond)) || stamp.After(writer.ended.Add(time.Millisecond)) {
			t.Fatal("generated resource-window clock escaped its handler execution", field, raw, err)
		}
		record[field] = "<read-clock>"
	}
	// These roots are omitted by the literal ResourceWindow selection and
	// recreated with current clocks by its clone/normalization boundary.
	// All stored clocks, permission fields and collection order stay exact.
	if core {
		data, ok := value["core"].(map[string]any)
		if !ok {
			t.Fatal("resource-window manifest core missing")
		}
		statuses, ok := data["customerStatuses"].([]any)
		if !ok || len(statuses) == 0 {
			t.Fatal("generated customer statuses missing")
		}
		for _, raw := range statuses {
			status := raw.(map[string]any)
			clock(status, "createdAt")
			clock(status, "updatedAt")
		}
		preferences, ok := data["notificationPreferences"].(map[string]any)
		if !ok {
			t.Fatal("generated notification preferences missing")
		}
		for _, raw := range preferences {
			clock(raw.(map[string]any), "updatedAt")
		}
	}
	if materialized {
		items, ok := value["items"].([]any)
		if !ok {
			t.Fatal("development member page missing")
		}
		found := 0
		for _, raw := range items {
			member := raw.(map[string]any)["data"].(map[string]any)
			user := member["user"].(map[string]any)
			if user["id"] == viewerID {
				clock(member, "lastSeenAt")
				found++
			}
		}
		if found != 1 {
			t.Fatal("development viewer member missing or duplicated")
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScaleP21SW124ResourceWindowRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_SYNC_MANIFEST", "on")
	t.Setenv("FLOW_INDEXED_READS", "on")
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32))))
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW115Fixture(t, repo)
		workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw124-")
		at := time.Date(2026, time.November, 15, 12, 13, 14, 123456789, time.UTC)
		privateTeam, childTeam := "", ""
		if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw124_seed", "", nil, func(data *domain.Bootstrap) error {
			for _, item := range data.Projects {
				if item.ID == "m2-unrelated" {
					privateTeam = item.TeamIDs[0]
				}
			}
			for teamID, settings := range data.TeamSettings {
				if settings.ParentTeamID == privateTeam {
					childTeam = teamID
				}
			}
			if privateTeam == "" || childTeam == "" {
				t.Fatal("resource-window fixture teams missing")
			}
			actor := f.principals[1].user
			for index, teamID := range []string{f.public, privateTeam, childTeam} {
				id := prefix + []string{"public", "private", "inherited"}[index]
				project := domain.Project{
					ID: id + "-project", Name: id, SlugID: id + "-current", PreviousSlugIDs: []string{id + "-old"},
					TeamIDs: []string{teamID}, Lead: &actor, Creator: &actor, MemberIDs: []string{actor.ID},
					Description: strings.Repeat("SW124_EXCLUDED_BODY_project ", 1024),
					Comments:    []domain.Comment{{ID: id + "-comment", User: actor, Body: "SW124_EXCLUDED_BODY_comment"}},
					CreatedAt:   at.Add(time.Duration(index) * time.Second), UpdatedAt: at,
				}
				document := domain.Document{
					ID: id + "-document", Title: id, SlugID: id + "-document-current", PreviousSlugIDs: []string{id + "-document-old"},
					Creator: actor, TeamIDs: []string{teamID}, ProjectIDs: []string{project.ID},
					Content: strings.Repeat("SW124_EXCLUDED_BODY_document ", 1024), ContentState: "SW124_EXCLUDED_BODY_state",
					CreatedAt: at, UpdatedAt: at.Add(time.Duration(index) * time.Second), Version: int64(index + 1),
					Permissions: []domain.DocumentPermission{
						{ID: id + "-creator", SubjectType: "user", SubjectID: actor.ID, Role: "owner"},
						{ID: id + "-guest", SubjectType: "user", SubjectID: f.principals[3].user.ID, Role: "viewer"},
					},
					Revisions: []domain.DocumentRevision{{ID: id + "-revision", DocumentID: id + "-document", Author: actor, Content: "SW124_EXCLUDED_BODY_history", CreatedAt: at}},
				}
				if index == 0 {
					document.ProjectIDs = append(document.ProjectIDs, prefix+"private-project")
				}
				data.Projects = append(data.Projects, project)
				data.Documents = append(data.Documents, document)
				document.ID, document.SlugID = document.ID+"-deleted", document.SlugID+"-deleted"
				raw, err := json.Marshal(document)
				if err != nil {
					return err
				}
				data.Trash = append(data.Trash, domain.TrashEntry{
					ID: id + "-trash", ResourceType: "document", ResourceID: document.ID, Title: document.Title,
					TeamIDs: []string{teamID}, Payload: raw, DeletedBy: actor, DeletedAt: at, ExpiresAt: at.AddDate(0, 0, 30), Links: &domain.TrashLinks{},
				})
			}
			for _, principal := range f.principals {
				settings := data.UserSettings[principal.user.ID]
				settings.UpdatedAt = at
				data.UserSettings[principal.user.ID] = settings
				for _, key := range []domain.APIKey{
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "all"},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{}},
					{Scopes: []string{"read", "write", "admin"}, TeamRestriction: "selected", TeamIDs: []string{f.public}},
				} {
					data.APIKeys = append(data.APIKeys, *scaleP21SW124Key(prefix, principal.user, &key))
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		_, former := scaleP03Join(t, f.scaleP03PlatformFixture, "SW124 departed", prefix+"departed@example.test", "member", f.public)
		if err := repo.RemoveMember(t.Context(), f.data.Workspace.ID, former.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Invite(t.Context(), f.data.Workspace.ID, f.data.Viewer.ID, prefix+"invitation@example.test", "member", []string{f.public}); err != nil {
			t.Fatal(err)
		}
		other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW124 other", prefix+"other", "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
		for _, key := range []string{workspace, other.Workspace.URLKey} {
			if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, key); err != nil {
				t.Fatal(err)
			}
		}
		services := []*server{{store: repo}, {store: f.probe}}
		muxes := make([]*http.ServeMux, len(services))
		for index, service := range services {
			muxes[index] = http.NewServeMux()
			service.registerSyncRoutes(muxes[index])
			service.registerScaleP20Routes(muxes[index])
		}
		// Dispatch real P20 routes using authenticated fixture users and stored
		// credentials, as in the existing P20/SW115 harness. Usage telemetry and
		// Account/authenticate are outside this consumer boundary.
		call := func(t *testing.T, index int, user domain.User, key *domain.APIKey, target, path string, outer, development bool) *scaleP21SW124Writer {
			t.Helper()
			request := f.literalRequest(user, key, nil)
			request.Method = http.MethodGet
			var err error
			request.URL, err = url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("X-Workspace-Key", target)
			request.SetPathValue("workspaceKey", target)
			request.SetPathValue("workspace", target)
			closeOutput := func() {}
			if outer {
				var ctx context.Context
				ctx, closeOutput = beginMetadataOutputReads(request.Context())
				request = request.WithContext(ctx)
			} else if services[index].store.BoundedMetadataEnabled() {
				request, closeOutput = beginRequestResourceReads(request)
			}
			defer closeOutput()
			services[index].authDisabled = development
			beforeDecodes := services[index].store.WorkspaceMetadataDecodes()
			writer := &scaleP21SW124Writer{ResponseRecorder: httptest.NewRecorder(), t: t, source: services[index].store}
			writer.started = time.Now()
			muxes[index].ServeHTTP(writer, request)
			writer.ended = time.Now()
			if index == 1 && writer.Code == http.StatusOK &&
				(writer.held == 0 || writer.headers == 0 || writer.writes == 0 || f.probe.MetadataWorkUsedForTesting() == 0) {
				t.Fatal("resource-window read was not owned through headers and body output")
			}
			closeOutput()
			closeOutput()
			if services[index].store.MetadataWorkUsedForTesting() != 0 || services[index].store.WorkspaceMetadataDecodes() != beforeDecodes {
				t.Fatal("resource-window route leaked output work or decoded full workspace metadata")
			}
			return writer
		}
		compare := func(t *testing.T, user domain.User, key *domain.APIKey, target, path string, outer, development bool, code int) {
			t.Helper()
			old := call(t, 0, user, key, target, path, outer, development)
			bounded := call(t, 1, user, key, target, path, outer, development)
			oldBody := scaleP21SW124Response(t, old, path, development, user.ID)
			boundedBody := scaleP21SW124Response(t, bounded, path, development, user.ID)
			if old.Code != code || bounded.Code != old.Code || !reflect.DeepEqual(old.Header(), bounded.Header()) || !bytes.Equal(oldBody, boundedBody) {
				t.Fatalf("resource-window route differs path=%s outer=%t development=%t status=%d/%d want=%d\nliteral=%s\nbounded=%s",
					path, outer, development, old.Code, bounded.Code, code, old.Body, bounded.Body)
			}
			if code == http.StatusOK && bytes.Contains(bounded.Body.Bytes(), []byte("SW124_EXCLUDED_BODY_")) {
				t.Fatal("resource-window route exposed excluded bodies or restore payloads")
			}
		}
		routes := []struct{ name, path string }{
			{"core", "/api/sync/manifest?surface=resource-window"},
			{"projects", "/api/sync/records?surface=resource-window&kind=projects&limit=100"},
			{"documents", "/api/sync/records?surface=resource-window&kind=documents&limit=100"},
			{"trash", "/api/resource-windows/trash?limit=100"},
			{"users", "/api/resource-windows/members?collection=users&limit=100"},
			{"members", "/api/resource-windows/members?collection=members&limit=100"},
			{"teamMembers", "/api/resource-windows/members?collection=teamMembers&limit=100"},
			{"departedMembers", "/api/resource-windows/members?collection=departedMembers&limit=100"},
			{"invitations", "/api/resource-windows/members?collection=invitations&limit=100"},
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			key = scaleP21SW124Key(prefix, user, key)
			if key != nil {
				credential, err := repo.AuthenticateAPIKeyRecord(t.Context(), workspace, key.SecretHash, nil)
				if err != nil || credential.User.ID != user.ID || credential.Key.ID != key.ID {
					t.Fatal("resource-window key is not a live stored credential", err)
				}
				key = &credential.Key
			}
			for _, outer := range []bool{false, true} {
				for _, route := range routes {
					t.Run(route.name+map[bool]string{false: "/request-output", true: "/outer-output"}[outer], func(t *testing.T) {
						compare(t, user, key, workspace, route.path, outer, false, http.StatusOK)
					})
				}
			}
		})
		access, err := repo.ResolveAccess(t.Context(), workspace, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		revision, err := repo.ResourceWindowRevision(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		cursor, err := store.SealResourceWindowCursor(access, store.PageRequest{Kind: "users", Limit: 100, MaxBytes: store.MetadataRecordPageMaxBytes}, revision, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, outer := range []bool{false, true} {
			for _, edge := range []struct {
				name, path string
				code       int
			}{
				{"filtered-users", "/api/resource-windows/members?collection=users&q=admin&limit=100", http.StatusOK},
				{"continued-users", "/api/resource-windows/members?collection=users&limit=100&cursor=" + url.QueryEscape(cursor), http.StatusOK},
				{"invalid-limit", "/api/sync/records?surface=resource-window&kind=projects&limit=0", http.StatusBadRequest},
				{"invalid-cursor", "/api/resource-windows/members?collection=users&cursor=broken", http.StatusConflict},
				{"invalid-collection", "/api/resource-windows/members?collection=unknown", http.StatusBadRequest},
				{"invalid-kind", "/api/sync/records?surface=resource-window&kind=unknown", http.StatusBadRequest},
			} {
				t.Run(edge.name+map[bool]string{false: "/request-output", true: "/outer-output"}[outer], func(t *testing.T) {
					compare(t, f.data.Viewer, nil, workspace, edge.path, outer, false, edge.code)
				})
			}
			for _, route := range []int{0, 5} {
				t.Run("cross-workspace/"+routes[route].name+map[bool]string{false: "/request-output", true: "/outer-output"}[outer], func(t *testing.T) {
					compare(t, f.principals[2].user, nil, other.Workspace.URLKey, routes[route].path, outer, false, http.StatusForbidden)
				})
			}
			for _, route := range []int{0, 3, 5} {
				t.Run("development/"+routes[route].name+map[bool]string{false: "/request-output", true: "/outer-output"}[outer], func(t *testing.T) {
					compare(t, f.data.Viewer, nil, workspace, routes[route].path, outer, true, http.StatusOK)
				})
			}
		}
	}
}
