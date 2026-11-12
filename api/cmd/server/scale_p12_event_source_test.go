package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP12Principal struct {
	name string
	user domain.User
	key  *domain.APIKey
}

func (p scaleP12Principal) request(workspace string) *http.Request {
	r := scaleP12Request(workspace, p.user)
	if p.key != nil {
		r = r.WithContext(context.WithValue(r.Context(), apiKeyContextKey{}, *p.key))
	}
	return r
}

type scaleP12Fixture struct {
	scaleP03PlatformFixture
	private, child           string
	open, secret, descendant domain.Issue
	principals               []scaleP12Principal
}

func newScaleP12Fixture(t *testing.T, repo *store.SQLiteStore) scaleP12Fixture {
	t.Helper()
	f := scaleP12Fixture{scaleP03PlatformFixture: scaleP03Fixture(t, repo), private: idgen.New("p12-private-"), child: idgen.New("p12-child-")}
	workspace := f.data.Workspace.URLKey
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p12.seeded", "", nil, func(data *domain.Bootstrap) error {
		data.Teams = append(data.Teams, domain.Team{ID: f.private, Key: "P12", Private: true}, domain.Team{ID: f.child, Key: "P12C"})
		data.TeamSettings[f.private] = domain.TeamSettings{TeamID: f.private, Access: "private"}
		data.TeamSettings[f.child] = domain.TeamSettings{TeamID: f.child, Access: "public", ParentTeamID: f.private}
		data.Projects = append(data.Projects, domain.Project{ID: "p12-open", Name: "Open", TeamIDs: []string{data.Teams[0].ID}, SlugID: "renamed-000000000012", PreviousSlugIDs: []string{"p12-old-slug"}, DependencyIDs: []string{"p12-secret"}, IssueCount: 42}, domain.Project{ID: "p12-secret", Name: "Hidden", TeamIDs: []string{f.private}})
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "p12-initiative", Name: "Mixed initiative", Creator: data.Viewer, ProjectIDs: []string{"p12-open", "p12-secret"}, ContributingTeamIDs: []string{data.Teams[0].ID, f.private}, Favorite: true, Subscribed: true}, domain.Initiative{ID: "p12-parent", Name: "Public parent", ProjectIDs: []string{"p12-open"}}, domain.Initiative{ID: "p12-hidden-parent", Name: "Hidden parent", LeadTeamID: f.private})
		data.InitiativeRelations = append(data.InitiativeRelations, domain.InitiativeRelation{ID: "p12-relation-open", Type: "parent", InitiativeID: "p12-initiative", RelatedInitiativeID: "p12-parent"}, domain.InitiativeRelation{ID: "p12-relation-hidden", Type: "parent", InitiativeID: "p12-initiative", RelatedInitiativeID: "p12-hidden-parent"})
		data.Documents = append(data.Documents, domain.Document{ID: "p12-document", SlugID: "p12-document-slug", Title: "Shared document", Content: "Body stays intact", TeamIDs: []string{data.Teams[0].ID}, Creator: data.Viewer}, domain.Document{ID: "p12-hidden-document", Title: "Private document", Content: "Private body", TeamIDs: []string{f.private}, Creator: data.Viewer})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, f.data.Viewer.ID, "member", true); err != nil {
		t.Fatal(err)
	}
	join := func(name, role string, teams ...string) domain.User {
		_, user := scaleP03Join(t, f.scaleP03PlatformFixture, name, idgen.New("p12-person-")+"@example.test", role, teams...)
		return user
	}
	f.principals = []scaleP12Principal{{name: "admin", user: f.data.Viewer}, {name: "admin-private-outsider", user: join("P12 outside admin", "admin", f.data.Teams[0].ID)}, {name: "member-private-outsider", user: join("P12 member", "member", f.data.Teams[0].ID)}, {name: "guest-explicit-grant", user: join("P12 guest", "guest", f.data.Teams[0].ID)}, {name: "subteam-inheritance", user: join("P12 inherited", "member", f.private)}}
	create := func(title, team, parent string) domain.Issue {
		input := map[string]any{"title": title, "teamId": team}
		if parent != "" {
			input["parentId"] = parent
		}
		return authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", input, workspace, http.StatusCreated)
	}
	f.open = create("Public issue", f.data.Teams[0].ID, "")
	f.secret = create("Private issue", f.private, "")
	f.descendant = create("Inherited grant", f.child, f.secret.ID)
	authRequest[[]domain.IssuePermission](t, f.admin, http.MethodPut, f.host.URL+"/api/issues/"+f.secret.ID+"/permissions", map[string]any{"permissions": []map[string]string{{"subjectType": "user", "subjectId": f.principals[3].user.ID, "role": "viewer"}}}, workspace, http.StatusOK)
	if err := repo.MutateWorkspace(t.Context(), workspace, "scale_p12.references", "", nil, func(data *domain.Bootstrap) error {
		for i := range data.Issues {
			if data.Issues[i].ID != f.open.ID {
				continue
			}
			issue := &data.Issues[i]
			issue.Project = &domain.ProjectSummary{ID: "p12-secret", Name: "Hidden project"}
			milestone := "hidden-milestone"
			issue.ProjectMilestoneID = &milestone
			issue.SuggestedProjectIDs = []string{"p12-open", "p12-secret"}
			issue.SuggestedTeamIDs = []string{f.data.Teams[0].ID, f.private}
			issue.SuggestedDuplicateIDs = []string{f.secret.ID}
			issue.SuggestedRelatedIDs = []string{f.secret.ID}
			issue.Relations = []domain.IssueRelation{{RelatedIssueID: f.secret.ID, Type: "related"}}
		}
		for _, scope := range []string{"unrestricted", "selected-empty", "selected-set"} {
			key := domain.APIKey{ID: idgen.New("p12-key-"), Name: "P12 " + scope, CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}}
			if scope != "unrestricted" {
				key.TeamRestriction = "selected"
				key.TeamIDs = []string{}
			}
			if scope == "selected-set" {
				key.TeamIDs = []string{f.data.Teams[0].ID}
			}
			data.APIKeys = append(data.APIKeys, key)
			copy := key
			f.principals = append(f.principals, scaleP12Principal{name: "key-" + scope, user: f.data.Viewer, key: &copy})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	return f
}

func scaleP12EqualEvent(t *testing.T, s *server, r *http.Request, event domain.RealtimeEvent) {
	t.Helper()
	old, oldVisible, oldErr := s.legacyPagedRealtimeEvent(r, event)
	got, visible, err := s.pagedRealtimeEvent(r, event)
	if (err != nil) != (oldErr != nil) || visible != oldVisible {
		t.Fatalf("%s: visibility old=%v new=%v errors old=%v new=%v", event.Type, oldVisible, visible, oldErr, err)
	}
	if !visible {
		return
	}
	var wantJSON, gotJSON any
	if err := json.Unmarshal(mustJSON(t, old), &wantJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustJSON(t, got), &gotJSON); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantJSON, gotJSON) {
		t.Fatalf("%s projection differs: old=%s new=%s", event.Type, mustJSON(t, old), mustJSON(t, got))
	}
}

func TestScaleP12PermissionDifferential(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for driver, repo := range scaleP03ServerTestStores(t) {
		t.Run(driver, func(t *testing.T) {
			f := newScaleP12Fixture(t, repo)
			deletion, _ := json.Marshal(map[string]any{"issueAccess": []deletedIssueAccess{{ID: f.secret.ID, TeamID: f.private, Grants: []deletedIssueSubject{{Type: "user", ID: f.principals[3].user.ID}}}, {ID: f.open.ID, TeamID: f.data.Teams[0].ID}}})
			for _, principal := range f.principals {
				t.Run(principal.name, func(t *testing.T) {
					request := principal.request(f.data.Workspace.URLKey)
					events := []domain.RealtimeEvent{
						{Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{"title":"Edited","issue":{"ignored":true}}`)},
						{Type: "issue.updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{"title":"Secret"}`)},
						{Type: "issue.updated", AggregateID: f.descendant.ID, Payload: json.RawMessage(`{}`)},
						{Type: "comment.created", AggregateID: f.open.ID, Payload: json.RawMessage(`{"commentId":"comment"}`)},
						{Type: "issue.permissions_updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{"permissions":[{"subjectId":"hidden-subject"}]}`)},
						{Type: "issue.deleted", AggregateID: f.secret.ID, Payload: deletion},
						{Type: "issue.batch_updated", Payload: mustJSON(t, map[string]any{"issueIds": []string{f.open.ID, f.secret.ID, f.descendant.ID}})},
						{Type: "document.updated", AggregateID: "p12-document", Payload: json.RawMessage(`{"content":"Changed"}`)},
						{Type: "document.updated", AggregateID: "p12-document-slug", Payload: json.RawMessage(`{}`)},
						{Type: "document.updated", AggregateID: "p12-hidden-document", Payload: json.RawMessage(`{"teamIds":["hidden"]}`)},
						{Type: "document.permissions_updated", AggregateID: "p12-hidden-document", Payload: json.RawMessage(`{"subjectId":"hidden-user"}`)},
						{Type: "document.draft_updated", AggregateID: "draft", Payload: json.RawMessage(`{"content":"Private draft"}`)},
						{Type: "project.updated", AggregateID: "p12-open", Payload: json.RawMessage(`{"name":"Renamed"}`)},
						{Type: "project.updated", AggregateID: "p12-old-slug", Payload: json.RawMessage(`{}`)},
						{Type: "initiative.updated", AggregateID: "p12-initiative", Payload: json.RawMessage(`{"name":"Renamed"}`)},
						{Type: "team.updated", AggregateID: f.private, Payload: json.RawMessage(`{"private":true}`)},
						{Type: "api_key.used", AggregateID: "credential", Payload: json.RawMessage(`{}`)},
					}
					for i, event := range events {
						event.ID = fmt.Sprintf("p12-matrix-%d", i)
						event.ActorID = f.data.Viewer.ID
						event.Seq = int64(i + 1)
						event.CreatedAt = time.Unix(1, 0).UTC()
						scaleP12EqualEvent(t, f.service, request, event)
					}
				})
			}
			other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "P12 other", idgen.New("p12-other-"), "us")
			if err != nil {
				t.Fatal(err)
			}
			f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
			scaleP12EqualEvent(t, f.service, f.principals[2].request(other.Workspace.URLKey), domain.RealtimeEvent{ID: "foreign", Type: "issue.updated", AggregateID: f.open.ID})
		})
	}
}

func TestScaleP12SourceFramesAreImmutableAndRevocationIsFresh(t *testing.T) {
	t.Setenv("FLOW_SYNC_V2", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		member := f.principals[4]
		request := member.request(f.data.Workspace.URLKey)
		event := domain.RealtimeEvent{ID: "p12-queued", Type: "issue.updated", AggregateID: f.secret.ID, Payload: json.RawMessage(`{}`)}
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, member.user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		source, err := f.service.BuildEventSource(t.Context(), f.data.Workspace.URLKey, event)
		if err != nil {
			t.Fatal(err)
		}
		frame, err := ProjectEvent(source, access)
		if err != nil || !frame.Visible() {
			t.Fatalf("initial frame: %v", err)
		}
		defer frame.Close()
		first, err := frame.Event()
		if err != nil {
			t.Fatal(err)
		}
		first.Payload[0] = '!'
		second, err := frame.Event()
		if err != nil || !json.Valid(second.Payload) {
			t.Fatal("frame exposed mutable storage")
		}
		queue, stop := f.service.realtime.subscribe(f.data.Workspace.URLKey)
		defer stop()
		f.service.realtime.publish(f.data.Workspace.URLKey, event)
		if err := repo.SetTeamMembership(t.Context(), f.data.Workspace.ID, f.private, member.user.ID, "member", false); err != nil {
			t.Fatal(err)
		}
		if err := repo.ValidateEventAccess(t.Context(), access); err == nil {
			t.Fatal("queued authorization survived revocation")
		}
		if err := frame.Validate(t.Context()); err == nil {
			t.Fatal("retained frame survived revocation")
		}
		scaleP12EqualEvent(t, f.service, request, <-queue)
		if _, visible, err := f.service.pagedRealtimeEvent(request, event); err != nil || visible {
			t.Fatalf("same stream after revoke visible=%v err=%v", visible, err)
		}
		// A retained source cannot be paired with another workspace's policy.
		other, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "P12 binding", idgen.New("p12-binding-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, other.Workspace.URLKey)
		foreign, err := repo.ResolveEventAccess(t.Context(), other.Workspace.URLKey, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ProjectEvent(source, foreign); err == nil {
			t.Fatal("source crossed workspace boundary")
		}
	}
}

func TestScaleP12TwoThousandUniqueACLsHaveBoundedFramesAndSourceSQL(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP03Fixture(t, repo)
		issue := authRequest[domain.Issue](t, f.admin, http.MethodPost, f.host.URL+"/api/issue-records", map[string]any{"title": "P12 ACL fanout", "teamId": f.data.Teams[0].ID}, f.data.Workspace.URLKey, http.StatusCreated)
		teams := make([]string, 11)
		for i := range teams {
			teams[i] = idgen.New("p12-acl-team-")
		}
		keys := make([]domain.APIKey, 2000)
		if err := repo.MutateWorkspace(t.Context(), f.data.Workspace.URLKey, "scale_p12.acls", "", nil, func(data *domain.Bootstrap) error {
			for _, id := range teams {
				data.Teams = append(data.Teams, domain.Team{ID: id, Key: "ACL"})
				data.TeamSettings[id] = domain.TeamSettings{TeamID: id, Access: "public"}
			}
			for i := range data.Issues {
				if data.Issues[i].ID == issue.ID {
					data.Issues[i].SuggestedTeamIDs = slices.Clone(teams)
				}
			}
			for i := range keys {
				key := domain.APIKey{ID: idgen.New("p12-unique-"), CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamRestriction: "selected", TeamIDs: []string{f.data.Teams[0].ID}}
				for bit, id := range teams {
					if i&(1<<bit) != 0 {
						key.TeamIDs = append(key.TeamIDs, id)
					}
				}
				keys[i] = key
				data.APIKeys = append(data.APIKeys, key)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.ReloadWorkspace(t.Context(), f.data.Workspace.URLKey); err != nil {
			t.Fatal(err)
		}
		sqlCount := 0
		ctx := store.WithSQLStatementObserver(t.Context(), func(string) { sqlCount++ })
		source, err := f.service.BuildEventSource(ctx, f.data.Workspace.URLKey, domain.RealtimeEvent{ID: "p12-2000", Type: "issue.updated", AggregateID: issue.ID, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		buildSQL := sqlCount
		seen := map[string]bool{}
		for _, key := range keys {
			access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, &key)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := ProjectEvent(source, access)
			if err != nil || !frame.Visible() {
				t.Fatalf("unique ACL projection visible=%v err=%v", frame.Visible(), err)
			}
			seen[string(frame.encoded)] = true
			frame.Close()
		}
		if len(seen) != 2000 || sqlCount != buildSQL {
			t.Fatalf("distinct=%d source SQL before=%d after=%d", len(seen), buildSQL, sqlCount)
		}
		if len(f.service.realtime.payloads.entries) > realtimePayloadCacheEntries || f.service.realtime.payloads.bytes > realtimePayloadCacheBytes || f.service.realtime.budget.used.Load() != 0 {
			t.Fatalf("unbounded cache/leases: entries=%d bytes=%d leases=%d", len(f.service.realtime.payloads.entries), f.service.realtime.payloads.bytes, f.service.realtime.budget.used.Load())
		}
		t.Logf("2000 unique ACLs: source SQL=%d retained frame cache=%d bytes entries=%d", buildSQL, f.service.realtime.payloads.bytes, len(f.service.realtime.payloads.entries))
	}
}

func TestScaleP12ConcurrentProjection(t *testing.T) {
	for _, repo := range scaleP03ServerTestStores(t) {
		f := newScaleP12Fixture(t, repo)
		access, err := repo.ResolveEventAccess(t.Context(), f.data.Workspace.URLKey, f.data.Viewer.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		source, err := f.service.BuildEventSource(t.Context(), f.data.Workspace.URLKey, domain.RealtimeEvent{ID: "p12-concurrent", Type: "issue.updated", AggregateID: f.open.ID, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		var workers sync.WaitGroup
		for range 16 {
			workers.Go(func() {
				for range 20 {
					frame, err := ProjectEvent(source, access)
					if err != nil {
						t.Error(err)
						return
					}
					frame.Close()
				}
			})
		}
		workers.Wait()
		if f.service.realtime.budget.used.Load() != 0 {
			t.Fatal("projection leaked a lease")
		}
	}
}

func TestScaleP12FlagsAndFallback(t *testing.T) {
	for value, want := range map[string]bool{"": false, "off": false, "shadow": false, "invalid": false, "on": true, "true": true, "1": true} {
		t.Setenv("FLOW_SYNC_V2", value)
		if syncV2Enabled() != want {
			t.Fatalf("flag %q", value)
		}
	}
	t.Setenv("FLOW_SYNC_V2", "off")
	if syncV2SummaryRequest(scaleP12Request("workspace", domain.User{})) {
		t.Fatal("summary negotiation bypassed rollback")
	}
	if strings.Contains(string(mustJSON(t, emptySyncTransaction("tx", "9007199254740993"))), `"seq":9007199254740993`) {
		t.Fatal("sequence lost its string wire type")
	}
}
