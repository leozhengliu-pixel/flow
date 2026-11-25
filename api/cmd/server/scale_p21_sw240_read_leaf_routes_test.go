package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW240Result struct {
	code    int
	header  http.Header
	body    []byte
	queries int
}

func scaleP21SW240Route(t *testing.T, source *store.SQLiteStore, request *http.Request, dev, shared, requireOwnership bool) scaleP21SW240Result {
	t.Helper()
	owned, release := beginRequestResourceReads(request)
	defer release()
	statements := 0
	owned = owned.WithContext(store.WithSQLStatementObserver(owned.Context(), func(string) { statements++ }))
	writer := &scaleP21SW59ResponseWriter{
		ResponseRecorder: httptest.NewRecorder(), t: t, source: source,
		requireHeld: source.BoundedMetadataEnabled() && requireOwnership,
	}
	service := &server{store: source, authDisabled: dev}
	if shared {
		service.getSharedIssue(writer, owned)
	} else {
		service.filterSuggestions(writer, owned)
	}
	if source.MetadataWorkUsedForTesting() != 0 || source.BoundedMetadataEnabled() && source.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("route retained work or decoded the full metadata graph")
	}
	return scaleP21SW240Result{writer.Code, writer.Header().Clone(), append([]byte(nil), writer.Body.Bytes()...), statements}
}

func scaleP21SW240Compare(t *testing.T, name string, old, bounded scaleP21SW240Result) {
	t.Helper()
	if old.code != bounded.code || !bytes.Equal(old.body, bounded.body) || !reflect.DeepEqual(old.header, bounded.header) {
		t.Fatalf("%s differs:\nliteral=%d %v %s\nbounded=%d %v %s", name, old.code, old.header, old.body, bounded.code, bounded.header, bounded.body)
	}
}

// scaleP21SW240Stored digests every canonical row the two routes could
// touch, plus the global event log. Both routes are read-only.
func scaleP21SW240Stored(t *testing.T, database *scaleP04Database, workspace string) [32]byte {
	t.Helper()
	digest := sha256.New()
	for _, query := range []string{
		`SELECT id,CAST(version AS CHAR(32)),data FROM issue_records WHERE workspace_key=? ORDER BY collection_order,id`,
		`SELECT kind,id,data FROM workspace_content_records WHERE workspace_key=? ORDER BY kind,resource_id,created_at,id`,
		`SELECT field,record_key,data FROM workspace_metadata_records WHERE workspace_key=? ORDER BY field,record_key`,
		`SELECT event_type,aggregate_id,payload FROM domain_events WHERE event_type<>? ORDER BY id`,
	} {
		arg := workspace
		if strings.Contains(query, "domain_events") {
			arg = "sw240-never-an-event"
		}
		rows := database.query(t, query, arg)
		for rows.Next() {
			var a, b, raw []byte
			if err := rows.Scan(&a, &b, &raw); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(digest, "%q %q %d ", a, b, len(raw))
			digest.Write(raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return [32]byte(digest.Sum(nil))
}

func TestScaleP21SW240ReadLeafRoutesDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	config := scaleP03DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	database := &scaleP04Database{config: config}
	t.Cleanup(func() {
		if database.reader != nil {
			_ = database.reader.Close()
		}
	})
	other := scaleP03Fixture(t, repo)
	f := scaleP21M2Setup(t, repo)
	var events atomic.Int64
	repo.SetWebhookSink(func(string, domain.DomainEvent) { events.Add(1) })
	repo.SetRealtimeSink(func(string, domain.RealtimeEvent) { events.Add(1) })
	f.probe.SetWebhookSink(func(string, domain.DomainEvent) { events.Add(1) })
	f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { events.Add(1) })
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw240-")
	empty, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW240 empty", idgen.New("sw240-empty-"), "us")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.DeleteWorkspace(context.Background(), empty.Workspace.URLKey) })
	token, emptyToken, revokedToken := prefix+"public-token", prefix+"empty-token", prefix+"revoked-token"
	selected, archivedTeam, orphanTeam := prefix+"selected", prefix+"archived-team", prefix+"orphan-team"
	at := time.Date(2026, 11, 16, 7, 8, 9, 10, time.UTC)
	edited := at.Add(time.Minute)
	restore := store.ForceFullMutationsForTesting(true)
	err = repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_routes", "", nil, func(data *domain.Bootstrap) error {
		for index := range data.Teams {
			data.Teams[index].Name = "Team " + data.Teams[index].ID
		}
		for index := range data.States {
			data.States[index].Name = "Status " + data.States[index].ID
		}
		for index := range data.Users {
			data.Users[index].DisplayName = "Assignee " + data.Users[index].ID
		}
		for index := range data.Projects {
			data.Projects[index].Name = "Project " + data.Projects[index].ID
		}
		for index := range data.Labels {
			data.Labels[index].Name = "Existing " + data.Labels[index].ID
		}
		for index := range data.Cycles {
			data.Cycles[index].Name = "Cycle " + data.Cycles[index].ID
		}
		var public, private domain.Team
		for _, team := range data.Teams {
			if team.ID == f.public {
				public = team
			} else if team.Private {
				private = team
			}
		}
		data.Teams = append(data.Teams, domain.Team{ID: archivedTeam, Key: "SWARCH", Name: "Archived team", ArchivedAt: &at})
		for index := range 73 {
			data.Labels = append(data.Labels, domain.IssueLabel{ID: fmt.Sprintf("%slabel-%02d", prefix, index), Name: fmt.Sprintf("Facet %02d", index), Color: "#123456", IssueCount: 999})
		}
		// The viewer projection names a label from its last visible entry;
		// development reads the last entry.
		data.Labels = append(data.Labels,
			domain.IssueLabel{ID: prefix + "dup-label", Name: "Duplicate visible", Scope: "workspace"},
			domain.IssueLabel{ID: prefix + "dup-label", Name: "Duplicate private", Scope: private.ID},
			domain.IssueLabel{ID: "", Name: "Empty label ID"},
		)
		projectID, cycleID := prefix+"project", prefix+"cycle"
		data.Projects = append(data.Projects,
			domain.Project{ID: projectID, Name: "Project first", TeamIDs: []string{f.public}, LabelIDs: []string{prefix + "label-00", prefix + "label-00", ""}},
			domain.Project{ID: projectID, Name: "Project last hidden display", TeamIDs: []string{private.ID}},
			domain.Project{ID: prefix + "archived-project", Name: "Archived project", ArchivedAt: &at, LabelIDs: []string{prefix + "label-00"}},
		)
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: prefix + "initiative", Name: "Counted initiative", Creator: data.Viewer, LabelIDs: []string{prefix + "label-00", prefix + "label-01", prefix + "label-01"}})
		data.Cycles = append(data.Cycles,
			domain.Cycle{ID: cycleID, Name: "Cycle private first", TeamID: private.ID},
			domain.Cycle{ID: cycleID, Name: "Cycle public", TeamID: public.ID},
			domain.Cycle{ID: cycleID, Name: "Cycle duplicate", TeamID: public.ID},
		)
		delegate := data.Users[len(data.Users)-1]
		for index := range 91 {
			label := domain.IssueLabel{ID: fmt.Sprintf("%slabel-%02d", prefix, index%73), Name: "Stale embedded facet"}
			item := domain.Issue{
				ID: fmt.Sprintf("%sissue-%03d", prefix, index), Identifier: fmt.Sprintf("SW240-%d", index+1), Number: index + 1,
				Title: "Facet source", Description: strings.Repeat("ignored issue body ", 80),
				Version: 1, Team: public, State: data.States[index%len(data.States)], Creator: data.Viewer,
				Assignee: &data.Users[index%len(data.Users)], Labels: []domain.IssueLabel{label, label, {ID: prefix + "dup-label", Name: "Embedded dup"}},
				Project: &domain.ProjectSummary{ID: projectID}, CycleID: &cycleID, CreatedAt: at, UpdatedAt: at,
			}
			if index%7 == 0 {
				item.Team = private
			}
			if index%11 == 0 {
				item.ArchivedAt = &at
			}
			if index%13 == 5 {
				item.Labels = append(item.Labels, domain.IssueLabel{ID: prefix + "unknown-label", Name: "Embedded unknown"})
				item.Assignee = &domain.User{ID: prefix + "unknown-user", DisplayName: "Embedded assignee"}
			}
			switch index {
			case 0:
				item.ID, item.ShareToken, item.SharedAt = selected, token, &at
				item.Description = strings.Repeat("complete shared issue ", 18000)
				item.Delegate = &delegate
				item.Labels = append(item.Labels, domain.IssueLabel{ID: "", Name: "Embedded empty"}, domain.IssueLabel{ID: prefix + "unknown-label", Name: "Embedded unknown", IssueCount: 7})
				reply := prefix + "comment-root"
				data.Comments[item.ID] = []domain.Comment{
					{
						ID: reply, Body: strings.Repeat("complete comment ", 18000) + " @mention ![image](https://example.test/a.png)",
						BodyData: map[string]any{"type": "doc", "content": []any{
							map[string]any{"type": "mention", "attrs": map[string]any{"id": data.Viewer.ID, "label": "Viewer"}},
							map[string]any{"type": "image", "attrs": map[string]any{"src": "https://example.test/a.png"}},
						}},
						User: data.Viewer, CreatedAt: at, Reactions: map[string][]string{"like": {data.Viewer.ID}, "eyes": {}},
					},
					{ID: prefix + "comment-reply", Body: "Reply", ParentID: &reply, User: delegate, CreatedAt: at.Add(time.Second), EditedAt: &edited, Resolved: true, Reactions: map[string][]string{}},
					{ID: prefix + "comment-stale-user", Body: "Stale user", User: domain.User{ID: data.Viewer.ID, Email: "stale@example.test", DisplayName: "Stale"}, CreatedAt: at.Add(2 * time.Second)},
				}
			case 1:
				item.ShareToken = token // A later duplicate is never selected.
			case 2:
				item.ShareToken = emptyToken
				item.Team = public
				item.ArchivedAt = nil
			case 3:
				item.ShareToken = revokedToken
			case 4:
				// The literal filter loses an archived parent's grant once
				// earlier issues have overwritten its slot.
				item.Team = domain.Team{ID: archivedTeam, Name: "Archived embedded"}
				item.Permissions = []domain.IssuePermission{{SubjectType: "workspace", SubjectID: data.Workspace.ID, Role: "viewer"}}
			case 9:
				item.Team, item.ParentID = private, new(fmt.Sprintf("%sissue-%03d", prefix, 4))
				item.ArchivedAt = nil
			case 10:
				item.Team = domain.Team{ID: orphanTeam, Name: "Orphan embedded"}
			}
			data.Issues = append(data.Issues, item)
		}
		return nil
	})
	if err == nil {
		// Revoke one token after it was issued.
		err = repo.MutateWorkspace(t.Context(), workspace, "scale_p21.sw240_revoke", "", nil, func(data *domain.Bootstrap) error {
			for index := range data.Issues {
				if data.Issues[index].ShareToken == revokedToken {
					data.Issues[index].ShareToken, data.Issues[index].SharedAt = "", nil
				}
			}
			return nil
		})
	}
	restore()
	if err != nil {
		t.Fatal(err)
	}
	facetParameters := []string{
		"", "field=label", "field=LABEL", "field=label&q=FACET-0", "field=label&q=duplicate", "field=team", "field=status",
		"field=assignee", "field=project", "field=cycle", "field=missing", "q=not-present", "q=embedded", "q=%20team%20",
	}
	for _, reload := range []bool{false, true} {
		if reload {
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.probe.RefreshInstalledMetadataViewForTesting(t.Context(), repo, empty.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		before := scaleP21SW240Stored(t, database, workspace)
		eventsBefore := events.Load()
		t.Run(fmt.Sprintf("reload=%t", reload), func(t *testing.T) {
			legacySQL, boundedSQL := 0, 0
			full := false
			f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
				for _, parameters := range facetParameters {
					old := f.literalRequest(user, key, nil)
					old.Method, old.URL.Path, old.URL.RawQuery = http.MethodGet, "/api/search/filter-suggestions", parameters
					next := old.Clone(old.Context())
					literal := scaleP21SW240Route(t, repo, old, false, false, true)
					bounded := scaleP21SW240Route(t, f.probe, next, false, false, true)
					scaleP21SW240Compare(t, "facets "+parameters, literal, bounded)
					if parameters == "field=label" && user.ID == f.data.Viewer.ID && key == nil {
						var values []domain.FilterSuggestion
						if json.Unmarshal(bounded.body, &values) != nil || len(values) != 50 || values[0].Count <= values[49].Count {
							t.Fatal("fixture did not exercise full-count top-50 ordering", len(values))
						}
						full = true
						legacySQL, boundedSQL = literal.queries, bounded.queries
					}
				}
				for _, target := range []string{other.data.Workspace.URLKey, empty.Workspace.URLKey, "missing-workspace"} {
					foreign := f.literalRequest(user, key, nil)
					foreign.Method, foreign.URL.Path = http.MethodGet, "/api/search/filter-suggestions"
					foreign.Header.Set("X-Workspace-Key", target)
					scaleP21SW240Compare(t, "workspace facets "+target,
						scaleP21SW240Route(t, repo, foreign, false, false, false),
						scaleP21SW240Route(t, f.probe, foreign.Clone(foreign.Context()), false, false, false))
				}
			})
			if !full {
				t.Fatal("top-50 fixture not reached")
			}
			t.Logf("facet_SQL literal=%d bounded=%d", legacySQL, boundedSQL)
			type sharedSpec struct{ workspace, token string }
			specs := []sharedSpec{
				{workspace, token}, {" " + workspace + " ", token}, {workspace, emptyToken}, {workspace, revokedToken},
				{workspace, token + "-missing"}, {workspace, strings.ToUpper(token)}, {other.data.Workspace.URLKey, token},
				{empty.Workspace.URLKey, token}, {"missing-workspace", token}, {"", token}, {"  ", token},
			}
			for _, principal := range []domain.User{f.data.Viewer, f.principals[3].user, {}} {
				for _, spec := range specs {
					old := f.literalRequest(principal, nil, nil)
					old.Method, old.URL.Path = http.MethodGet, "/api/shared/issues/"+spec.token
					old.URL.RawQuery = url.Values{"workspace": {spec.workspace}}.Encode()
					old.SetPathValue("token", spec.token)
					next := old.Clone(old.Context())
					owned := strings.TrimSpace(spec.workspace) == workspace
					literal := scaleP21SW240Route(t, repo, old, false, true, owned)
					bounded := scaleP21SW240Route(t, f.probe, next, false, true, owned)
					scaleP21SW240Compare(t, "shared "+spec.workspace+" "+spec.token, literal, bounded)
					if spec.workspace == workspace && spec.token == token {
						var result struct {
							Issue    domain.Issue
							Comments []domain.Comment
						}
						if json.Unmarshal(bounded.body, &result) != nil || result.Issue.ID != selected ||
							result.Issue.ShareToken != "" || result.Issue.SharedAt != nil || result.Issue.Delegate != nil ||
							!reflect.DeepEqual(result.Issue.Team, domain.Team{}) || result.Issue.Creator.Email != "" ||
							len(result.Comments) != 3 || result.Comments[0].User.Email != "" || result.Issue.ArchivedAt == nil {
							t.Fatal("shared first occurrence or redactions changed")
						}
					}
					if spec.workspace == workspace && spec.token == emptyToken && !bytes.Contains(bounded.body, []byte(`"comments":[]`)) {
						t.Fatal("shared issue without comments lost its empty list")
					}
				}
			}
			for _, target := range []string{workspace, empty.Workspace.URLKey, "missing-workspace", ""} {
				for _, parameters := range facetParameters {
					old := f.literalRequest(domain.User{}, nil, nil)
					old.Method, old.URL.Path, old.URL.RawQuery = http.MethodGet, "/api/search/filter-suggestions", parameters
					old.Header.Set("X-Workspace-Key", target)
					next := old.Clone(context.Background())
					scaleP21SW240Compare(t, "development facets "+target+" "+parameters,
						scaleP21SW240Route(t, repo, old, true, false, target == workspace),
						scaleP21SW240Route(t, f.probe, next, true, false, target == workspace))
				}
			}
			shared := f.literalRequest(domain.User{}, nil, nil)
			shared.Method, shared.URL.Path = http.MethodGet, "/api/shared/issues/"+token
			shared.URL.RawQuery = url.Values{"workspace": {workspace}}.Encode()
			shared.SetPathValue("token", token)
			scaleP21SW240Compare(t, "development shared",
				scaleP21SW240Route(t, repo, shared, true, true, true),
				scaleP21SW240Route(t, f.probe, shared.Clone(context.Background()), true, true, true))
			anonymous := f.literalRequest(domain.User{}, nil, nil)
			anonymous.Method, anonymous.URL.Path = http.MethodGet, "/api/search/filter-suggestions"
			scaleP21SW240Compare(t, "missing-principal facets",
				scaleP21SW240Route(t, repo, anonymous, false, false, false),
				scaleP21SW240Route(t, f.probe, anonymous.Clone(anonymous.Context()), false, false, false))
		})
		if after := scaleP21SW240Stored(t, database, workspace); after != before || events.Load() != eventsBefore {
			t.Fatal("read leaves changed stored state or published events")
		}
	}
}
