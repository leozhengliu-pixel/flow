package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
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

func scaleP19Options(t *testing.T) {
	t.Helper()
	t.Setenv("FLOW_CONNECTOR_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{19}, 32)))
	for _, flag := range []string{"FLOW_INDEXED_READS", "FLOW_SYNC_MANIFEST", "FLOW_SYNC_V2", "FLOW_RECORD_JOURNAL"} {
		t.Setenv(flag, "on")
	}
}
func scaleP19Path(kind string, query scaleP19Query, cursor string, limit int) string {
	raw, _ := json.Marshal(query)
	values := url.Values{"kind": {kind}, "window": {string(raw)}, "limit": {fmt.Sprint(limit)}}
	if cursor != "" {
		values.Set("cursor", cursor)
	}
	return "/api/sync/records?" + values.Encode()
}
func scaleP19ProjectQuery() scaleP19Query {
	return scaleP19Query{Schema: 1, Locale: "en-US", Now: "2026-11-12T12:00:00Z", ShowTeamInitiatives: true, ShowContributing: true, ShowNested: true, ShowParents: true, Display: scaleP19Display{Layout: "list", Grouping: "No grouping", SubGrouping: "No grouping", Ordering: "Manual", ShowClosed: "All"}}
}

func TestScaleP19EnableWindowsAndCursorContract(t *testing.T) {
	scaleP19Options(t)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			err := repo.MutateWorkspace(t.Context(), workspace, "scale_p19.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = nil
				data.Initiatives = nil
				for i := range 205 {
					start, target := "2026-01-01", "2026-12-31"
					data.Projects = append(data.Projects, domain.Project{ID: fmt.Sprintf("p19-%03d", i), Name: fmt.Sprintf("Project %03d", 204-i), TeamIDs: []string{data.Teams[0].ID}, Position: float64(i), Priority: i % 5, Status: data.ProjectStatuses[i%len(data.ProjectStatuses)], Health: "noUpdate", StartDate: &start, TargetDate: &target, CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)})
				}
				for i := range 3 {
					ids := []string{}
					for j := i; j < 205; j += 3 {
						ids = append(ids, fmt.Sprintf("p19-%03d", j))
					}
					parents := []string{}
					if i == 1 {
						parents = []string{"p19-i-0"}
					}
					data.Initiatives = append(data.Initiatives, domain.Initiative{ID: fmt.Sprintf("p19-i-%d", i), Name: fmt.Sprintf("Initiative %d", i), Status: "active", ProjectIDs: ids, ParentInitiativeIDs: parents, Health: "noUpdate", Creator: data.Viewer})
				}
				data.ProjectUpdates = map[string][]domain.ProjectUpdate{"p19-000": {}}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			query := scaleP19ProjectQuery()
			query.Display.Ordering = "Name"
			read := func(kind string, q scaleP19Query, cursor string, limit int) (scaleP19WindowPage, []domain.Project) {
				w := scaleP03Read(t, f, f.admin, "", http.MethodGet, scaleP19Path(kind, q, cursor, limit), workspace)
				if w.Code != 200 {
					t.Fatalf("%s status %d %.500s", kind, w.Code, w.Body.String())
				}
				if w.Body.Len() > 256<<10 {
					t.Fatal("unbounded page")
				}
				var page scaleP19WindowPage
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				var rows struct {
					Items []domain.Project `json:"items"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &rows)
				return page, rows.Items
			}
			page, rows := read("projects", query, "", 100)
			version, deleted, err := repo.RecordVersion(t.Context(), workspace, store.RecordKey{Kind: "projects", ID: rows[0].ID})
			if err != nil || deleted || page.Versions[rows[0].ID] != version {
				t.Fatal("window used a collection counter as a record version", err)
			}
			if page.Total != 205 || len(rows) != 100 || rows[0].ID != "p19-204" || !page.HasMore || !reflect.DeepEqual(page.Timeline, []string{"2026-01-01", "2026-12-31"}) {
				t.Fatalf("global order/count/range: %+v first=%s", page, rows[0].ID)
			}
			second, rows := read("projects", query, page.NextCursor, 100)
			if second.Offset != 100 || rows[0].ID != "p19-104" {
				t.Fatal("cursor skipped/repeated rows")
			}
			changed := query
			changed.Display.OrderingDirection = "desc"
			if w := scaleP03Read(t, f, f.admin, "", http.MethodGet, scaleP19Path("projects", changed, page.NextCursor, 100), workspace); w.Code != 409 {
				t.Fatal("cursor not bound to ordering", w.Code)
			}
			query.Display.Grouping = "Priority"
			page, rows = read("projects", query, "", 10)
			if page.Groups[0].ID != "priority-urgent" || page.Groups[0].Total != 41 || len(rows) != 10 {
				t.Fatal("global grouping/counts differ", page.Groups)
			}
			query.Display.Grouping = "No grouping"
			query.Filters = []store.ProjectDirectoryFilter{{Field: "priority", Operator: "is", Values: []string{"urgent"}}}
			page, _ = read("projects", query, "", 100)
			if page.Total != 41 {
				t.Fatal("filter total differs", page.Total)
			}
			query = scaleP19ProjectQuery()
			query.View = "active"
			page, _ = read("initiatives", query, "", 100)
			if len(page.Entries) != 3 || page.Entries[1].Depth != 1 || page.Aggregates["p19-i-0"].Projects != 137 {
				t.Fatal("hierarchy/aggregate differs", page.Entries, page.Aggregates)
			}
			query.Collapsed = []string{"p19-i-0"}
			page, _ = read("initiatives", query, "", 100)
			if page.Total != 2 {
				t.Fatal("collapse did not change total", page.Total)
			}
			manifest := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/sync/manifest?surface=initiatives-page", workspace)
			if manifest.Code != 200 || manifest.Body.Len() > 256<<10 || !strings.Contains(manifest.Body.String(), scaleP19Capability) {
				t.Fatalf("surface manifest %d %.500s", manifest.Code, manifest.Body.String())
			}
			t.Setenv("FLOW_CONNECTOR_SECRET_KEY", "")
			if got := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/sync/manifest?surface=projects-page", workspace); got.Code != http.StatusNotImplemented {
				t.Fatal("manifest advertised cursors without a shared key", got.Code)
			}
		})
	}
}

func TestScaleP19EnableBrowserCalendarMonth(t *testing.T) {
	target := "2026-10-31"
	values := scaleP19InitiativeValues(domain.Initiative{TargetDate: &target}, scaleP19Query{Now: "2026-11-01T00:30:00Z", LocalMonth: "2026-10"})
	if !slices.Contains(values["date"], "targetMonth") {
		t.Fatal("UTC month replaced the browser's calendar month")
	}
}

func TestScaleP19EnableRemovedAndReplacedStatuses(t *testing.T) {
	current := domain.ProjectStatus{ID: "current", Name: "Awaiting", Type: "planned"}
	data := domain.Bootstrap{ProjectStatuses: []domain.ProjectStatus{current}}
	replaced := domain.Project{ID: "replaced", Status: domain.ProjectStatus{ID: "previous", Name: "Awaiting", Type: "planned"}}
	removed := domain.Project{ID: "removed", Status: domain.ProjectStatus{ID: "removed-status", Name: "Removed", Type: "backlog"}}
	query := scaleP19ProjectQuery()
	query.Display.Ordering = "Status"
	projects := []domain.Project{removed, replaced}
	scaleP19SortProjects(projects, query, data, nil, nil)
	if projects[0].ID != replaced.ID {
		t.Fatal("a removed status sorted ahead of the current status catalogue")
	}
	id, name := scaleP19ProjectDescriptor(replaced, "Status", data)
	if id != "status-current" || name != "Awaiting" {
		t.Fatal("status name fallback differs from the legacy view", id, name)
	}
}

func TestScaleP19EnableLargeInitiativeAggregate(t *testing.T) {
	data := domain.Bootstrap{ProjectUpdates: map[string][]domain.ProjectUpdate{}}
	ids := make([]string, 23210)
	for i := range ids {
		ids[i] = fmt.Sprintf("project-%d", i)
		status := domain.ProjectStatus{Type: "started"}
		if i%10 == 0 {
			status.Type = "completed"
		}
		data.Projects = append(data.Projects, domain.Project{ID: ids[i], Status: status, Health: "noUpdate"})
	}
	data.Initiatives = []domain.Initiative{
		{ID: "parent", ProjectIDs: ids, Status: "active", Health: "noUpdate"},
		{ID: "child", ProjectIDs: ids[:100], ParentInitiativeIDs: []string{"parent"}, Status: "active", Health: "noUpdate"},
	}
	page, err := scaleP19Initiatives(data, scaleP19ProjectQuery(), scaleP19WindowPage{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Aggregates["parent"]; got.Projects != 23210 || got.Completed != 2321 || got.NeedingUpdate != 20889 || got.HasUpdates {
		t.Fatal("large aggregate double-counted descendants or lost project state", got)
	}
	for _, item := range page.Items.([]domain.Initiative) {
		if len(item.ProjectIDs) != 0 {
			t.Fatal("window transferred the initiative's full project membership")
		}
	}
	raw, err := json.Marshal(page)
	if err != nil || len(raw) > 256<<10 {
		t.Fatal("large initiative exceeded the wire budget", len(raw), err)
	}
	if len(data.Initiatives[0].ProjectIDs) != 23210 {
		t.Fatal("query mutated canonical initiative memberships")
	}
}

func TestScaleP19EnableUpdateCursorRejectsChangedValuesAtSameHead(t *testing.T) {
	first := domain.ProjectUpdate{ID: "first", Body: "Original"}
	second := domain.ProjectUpdate{ID: "second", Body: "Second"}
	binding := func(values ...domain.ProjectUpdate) string {
		records := make([]scaleP19UpdateRecord, len(values))
		for i, value := range values {
			records[i] = scaleP19UpdateRecord{id: value.ID, value: value}
		}
		result, err := scaleP19UpdateBinding("projects", "project", "unchanged-head", records)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	original := binding(first, second)
	first.Body = "Changed before the journal was sequenced"
	if binding(first, second) == original || binding(second, first) == original || binding(second) == original {
		t.Fatal("an edited, reordered or removed update retained the old continuation binding")
	}
}

func TestScaleP19EnableUpdateTransferAndLegacyBytes(t *testing.T) {
	scaleP19Options(t)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			id := "p19-updates"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p19.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Projects = append(data.Projects, domain.Project{ID: id, Name: "Updates", TeamIDs: []string{data.Teams[0].ID}, Status: data.ProjectStatuses[0]})
				for i := range 45 {
					body := strings.Repeat("u", 2048)
					if i == 22 {
						body = strings.Repeat("项目", 100000)
					}
					data.ProjectUpdates[id] = append(data.ProjectUpdates[id], domain.ProjectUpdate{ID: fmt.Sprintf("p19-u-%02d", i), ProjectID: id, Body: body, User: data.Viewer, Health: "onTrack", CreatedAt: time.Now().UTC().Add(-time.Duration(i) * time.Hour)})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			path := "/api/projects/" + id + "/updates"
			read := func(path string) []byte {
				w := scaleP03Read(t, f, f.admin, "", http.MethodGet, path, workspace)
				if w.Code != 200 {
					t.Fatalf("%s %d %.500s", path, w.Code, w.Body.String())
				}
				if strings.Contains(path, "window=") && w.Body.Len() > 256<<10 {
					t.Fatal("update page exceeds budget")
				}
				return w.Body.Bytes()
			}
			legacy := read(path)
			var expected []json.RawMessage
			if err := json.Unmarshal(legacy, &expected); err != nil {
				t.Fatal(err)
			}
			var actual []json.RawMessage
			cursor := ""
			fragments := 0
			for pages := 0; ; pages++ {
				if pages > 10 {
					t.Fatal("nonterminating update cursor")
				}
				raw := read(path + "?window=1&limit=20&cursor=" + url.QueryEscape(cursor))
				var page scaleP19UpdatePage
				if err := json.Unmarshal(raw, &page); err != nil {
					t.Fatal(err)
				}
				for _, item := range page.Items {
					var key struct {
						ID string `json:"id"`
					}
					_ = json.Unmarshal(item, &key)
					if page.Details[key.ID] {
						var reconstructed []byte
						next := ""
						for {
							raw := read(path + "?window=1&update=" + key.ID + "&cursor=" + url.QueryEscape(next))
							var part struct {
								Data   string `json:"data"`
								Offset int    `json:"offset"`
								Next   string `json:"nextCursor"`
								More   bool   `json:"hasMore"`
							}
							_ = json.Unmarshal(raw, &part)
							if part.Offset != len(reconstructed) {
								t.Fatal("fragment offset mismatch")
							}
							chunk, err := base64.StdEncoding.DecodeString(part.Data)
							if err != nil {
								t.Fatal(err)
							}
							reconstructed = append(reconstructed, chunk...)
							fragments++
							if !part.More {
								break
							}
							next = part.Next
						}
						item = reconstructed
					}
					actual = append(actual, item)
				}
				if !page.HasMore {
					break
				}
				cursor = page.NextCursor
			}
			if len(actual) != len(expected) || fragments < 2 {
				t.Fatal("lost updates or missing oversize coverage")
			}
			for i := range actual {
				if !bytes.Equal(actual[i], expected[i]) {
					t.Fatal("update differs from legacy", i)
				}
			}
			if !bytes.Equal(legacy, read(path)) {
				t.Fatal("negotiated reads changed legacy response bytes")
			}
		})
	}
}

func TestScaleP19EnablePermissionDifferential(t *testing.T) {
	scaleP19Options(t)
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := scaleP03Fixture(t, repo)
			workspace := f.data.Workspace.URLKey
			suffix := idgen.New("p19-")
			private, child := suffix+"private", suffix+"child"
			public := f.data.Teams[0].ID
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p19.seeded", "", nil, func(data *domain.Bootstrap) error {
				data.Teams = append(data.Teams, domain.Team{ID: private, Key: suffix + "P", Private: true}, domain.Team{ID: child, Key: suffix + "C"})
				data.TeamSettings[private] = domain.TeamSettings{TeamID: private, Access: "private"}
				data.TeamSettings[child] = domain.TeamSettings{TeamID: child, ParentTeamID: private, Access: "public"}
				data.Projects = []domain.Project{{ID: "p19-open", Name: "Open", SlugID: "renamed-000000000019", PreviousSlugIDs: []string{"old-p19"}, TeamIDs: []string{public}, DependencyIDs: []string{"p19-hidden"}, Initiatives: []string{"p19-hidden-i"}, Status: data.ProjectStatuses[0], IssueCount: 99}, {ID: "p19-hidden", Name: "Hidden", TeamIDs: []string{private}, Status: data.ProjectStatuses[0]}, {ID: "p19-child", Name: "Child", TeamIDs: []string{child}, Status: data.ProjectStatuses[0]}}
				data.Initiatives = []domain.Initiative{{ID: "p19-visible-i", Name: "Visible", Status: "active", ProjectIDs: []string{"p19-open", "p19-hidden"}, ParentInitiativeIDs: []string{"p19-hidden-i"}, Creator: data.Viewer}, {ID: "p19-hidden-i", Name: "Hidden", Status: "active", ProjectIDs: []string{"p19-hidden"}, Creator: data.Viewer}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, f.data.Viewer.ID, "member", true); err != nil {
				t.Fatal(err)
			}
			member, _ := scaleP03Join(t, f, "P19 member", suffix+"member@example.test", "member", public)
			outside, _ := scaleP03Join(t, f, "P19 outside admin", suffix+"admin@example.test", "admin", public)
			guest, _ := scaleP03Join(t, f, "P19 guest", suffix+"guest@example.test", "guest", public, private)
			inherited, _ := scaleP03Join(t, f, "P19 inherited", suffix+"inherited@example.test", "member", private)
			unrestricted := scaleP03Key(t, f, []string{"read"}, nil)
			selected := scaleP03Key(t, f, []string{"read"}, []string{public})
			empty := suffix + "empty"
			if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p19.key", "", nil, func(data *domain.Bootstrap) error {
				data.APIKeys = append(data.APIKeys, domain.APIKey{ID: suffix + "empty-key", Name: "Selected empty", CreatorID: f.data.Viewer.ID, SecretHash: secretHash(empty), Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{}})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			principals := []struct {
				name   string
				client *http.Client
				token  string
			}{{"admin", f.admin, ""}, {"admin-private-outsider", outside, ""}, {"member-private-outsider", member, ""}, {"guest-explicit-grants", guest, ""}, {"subteam-inheritance", inherited, ""}, {"key-unrestricted", nil, unrestricted}, {"key-selected-set", nil, selected}, {"key-selected-empty", nil, empty}}
			for _, principal := range principals {
				t.Run(principal.name, func(t *testing.T) {
					legacy := scaleP03Read(t, f, principal.client, principal.token, http.MethodGet, "/api/bootstrap?issues=none", workspace)
					if legacy.Code != 200 {
						t.Fatalf("legacy %d %.200s", legacy.Code, legacy.Body.String())
					}
					var data domain.Bootstrap
					_ = json.Unmarshal(legacy.Body.Bytes(), &data)
					q := scaleP19ProjectQuery()
					w := scaleP03Read(t, f, principal.client, principal.token, http.MethodGet, scaleP19Path("projects", q, "", 100), workspace)
					if w.Code != 200 {
						t.Fatalf("window %d %.300s", w.Code, w.Body.String())
					}
					var page struct {
						Items []domain.Project `json:"items"`
						Total int              `json:"total"`
					}
					_ = json.Unmarshal(w.Body.Bytes(), &page)
					want := slices.Clone(data.Projects)
					for i := range want {
						want[i] = store.ProjectListProjection(want[i])
					}
					if page.Total != len(want) || !reflect.DeepEqual(page.Items, want) {
						t.Fatalf("project fields/count/reference differ: got=%+v want=%+v", page.Items, want)
					}
					w = scaleP03Read(t, f, principal.client, principal.token, http.MethodGet, scaleP19Path("initiatives", q, "", 100), workspace)
					if w.Code != 200 {
						t.Fatalf("initiative window %d %.300s", w.Code, w.Body.String())
					}
					var ip struct {
						Items      []domain.Initiative          `json:"items"`
						Aggregates map[string]scaleP19Aggregate `json:"aggregates"`
					}
					_ = json.Unmarshal(w.Body.Bytes(), &ip)
					for _, item := range ip.Items {
						expected := slices.IndexFunc(data.Initiatives, func(i domain.Initiative) bool { return i.ID == item.ID })
						if expected < 0 {
							t.Fatal("hidden initiative exposed")
						}
						source := data.Initiatives[expected]
						source.Description = ""
						source.DescriptionHistory = nil
						source.Resources = nil
						source.Comments = nil
						source.CommentAttachments = nil
						source.ProjectIDs = nil
						if !reflect.DeepEqual(item, source) {
							gotJSON, _ := json.Marshal(item)
							wantJSON, _ := json.Marshal(source)
							t.Fatalf("initiative actors/references differ %s\ngot %s\nwant %s", item.ID, gotJSON, wantJSON)
						}
						if ip.Aggregates[item.ID].Projects > len(data.Projects) {
							t.Fatal("hidden aggregate count exposed")
						}
					}
				})
			}
			q := scaleP19ProjectQuery()
			q.Filters = []store.ProjectDirectoryFilter{{Field: "project", Operator: "is", Values: []string{"old-p19"}}}
			// Old-slug matching remains the legacy UI's exact-id predicate; REST
			// slug reads continue through the existing resource handler unchanged.
			if w := scaleP03Read(t, f, f.admin, "", http.MethodGet, "/api/projects/old-p19", workspace); w.Code != 200 {
				t.Fatal("old slug stopped resolving", w.Code)
			}
			other := scaleP03Fixture(t, repo)
			if w := scaleP03Read(t, f, member, "", http.MethodGet, scaleP19Path("projects", scaleP19ProjectQuery(), "", 1), other.data.Workspace.URLKey); w.Code != 403 {
				t.Fatal("cross-workspace read accepted", w.Code)
			}
			first := scaleP03Read(t, f, f.admin, "", http.MethodGet, scaleP19Path("projects", scaleP19ProjectQuery(), "", 1), workspace)
			var page scaleP19WindowPage
			_ = json.Unmarshal(first.Body.Bytes(), &page)
			if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, private, f.data.Viewer.ID, "member", false); err != nil {
				t.Fatal(err)
			}
			for _, batch := range []string{"queued", "replay"} {
				t.Run(batch, func(t *testing.T) {
					w := scaleP03Read(t, f, f.admin, "", http.MethodGet, scaleP19Path("projects", scaleP19ProjectQuery(), page.NextCursor, 1), workspace)
					if w.Code != 409 && w.Code != 403 {
						t.Fatal("revoked cursor replayed", w.Code)
					}
				})
			}
		})
	}
}
