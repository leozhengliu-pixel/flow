package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type scaleP21SW15Labels struct {
	scaleP21M2Fixture
	labels  []domain.IssueLabel
	groups  []domain.LabelGroup
	ids     []string
	groupID string
	private string
	child   string
}

func scaleP21SW15LabelSetup(t *testing.T, repo *store.SQLiteStore) scaleP21SW15Labels {
	t.Helper()
	f := scaleP21SW15Labels{scaleP21M2Fixture: scaleP21M2Setup(t, repo), groupID: idgen.New("sw15-group-")}
	data, ok := repo.WorkspaceMetadata(f.data.Workspace.URLKey)
	if !ok {
		t.Fatal("missing label fixture workspace")
	}
	for _, team := range data.Teams {
		if strings.HasPrefix(team.ID, "m2-private-") {
			f.private = team.ID
		}
		if strings.HasPrefix(team.ID, "m2-child-") {
			f.child = team.ID
		}
	}
	if f.private == "" || f.child == "" {
		t.Fatal("label fixture lacks private/inherited teams")
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for range 60 {
		id := idgen.New("label_sw15_retained_")
		f.ids = append(f.ids, id)
		f.labels = append(f.labels, domain.IssueLabel{ID: id, Name: "Retained", Scope: f.public, ResourceType: "issue", CreatedAt: now})
		f.groups = append(f.groups, domain.LabelGroup{ID: idgen.New("sw15-unrelated-group-"), Name: "Retained", Scope: f.public, ResourceType: "issue", CreatedAt: now})
	}
	f.labels[0].Description = strings.Repeat("unrelated label body ", 18000)
	f.groups[0].Description = strings.Repeat("unrelated group body ", 18000)
	f.reset(t, nil)
	return f
}

func (f scaleP21SW15Labels) reset(t *testing.T, group *domain.LabelGroup) {
	t.Helper()
	repo, workspace := f.repository, f.data.Workspace.URLKey
	repo.SetWebhookSink(nil)
	repo.SetRealtimeSink(nil)
	f.probe.SetWebhookSink(nil)
	f.probe.SetRealtimeSink(nil)
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "labels", "labelGroups"), workspace, "issue_label.updated", "", nil, func(data *domain.Bootstrap) error {
		data.Labels = slices.Clone(f.labels)
		data.LabelGroups = slices.Clone(f.groups)
		if group != nil {
			data.LabelGroups = append(data.LabelGroups, *group)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW15Canonical(t *testing.T, repo *store.SQLiteStore, workspace string, keys ...store.RecordKey) any {
	t.Helper()
	batch, err := repo.ReadCanonicalRecords(t.Context(), workspace, keys, "detail")
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{}
	for _, item := range batch.Items {
		if item.Omitted {
			t.Fatal("small selected label or actor was omitted", item.Key)
		}
		rows = append(rows, []any{item.Key, item.Missing, item.Data})
	}
	return rows
}

func TestScaleP21SW15LabelCreationMeasurement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW15LabelSetup(t, repo)
		sources := []*store.SQLiteStore{repo, f.probe}
		if os.Getenv("FLOW_SCALE_P21_SW15_BASELINE") == "1" {
			sources = sources[:1]
		}
		for _, source := range sources {
			var bytes, allocs uint64
			for range 5 {
				f.reset(t, nil)
				r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "Measured"}), httptest.NewRecorder()
				r.SetPathValue("id", f.public)
				runtime.GC()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				(&server{store: source}).createTeamLabel(w, r)
				runtime.ReadMemStats(&after)
				if w.Code != http.StatusCreated {
					t.Fatal(w.Code, w.Body.String())
				}
				bytes += after.TotalAlloc - before.TotalAlloc
				allocs += after.Mallocs - before.Mallocs
			}
			t.Logf("bounded=%t bytes/op=%d allocs/op=%d", source.BoundedMetadataEnabled(), bytes/5, allocs/5)
		}
	}
}

func TestScaleP21SW15TeamLabelCreationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW15LabelSetup(t, repo)
		workspace := f.data.Workspace.URLKey
		_, baseline := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
		otherGroup := scaleP21SW6RecordHash(t, repo, workspace, "labelGroups", f.groups[0].ID)
		otherProject := scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated")
		group := domain.LabelGroup{ID: f.groupID, Name: "Target", Scope: f.public, ResourceType: "issue", CreatedAt: f.groups[0].CreatedAt}
		type scenario struct {
			name, team, resource, groupID string
			group                         *domain.LabelGroup
			nameValue                     string
			status                        int
			cancel                        bool
		}
		archived := group
		archived.ArchivedAt = &group.CreatedAt
		wrongResource := group
		wrongResource.ResourceType = "project"
		workspaceGroup := group
		workspaceGroup.Scope = "Workspace"
		emptyScopeGroup := group
		emptyScopeGroup.Scope = ""
		foreignGroup := group
		foreignGroup.Scope = "sw15-foreign-team"
		scenarios := []scenario{
			{name: "ungrouped", team: f.public, status: http.StatusCreated},
			{name: "private-team", team: f.private, status: http.StatusCreated},
			{name: "inherited-team", team: f.child, status: http.StatusCreated},
			{name: "team-group", team: f.public, groupID: group.ID, group: &group, status: http.StatusCreated},
			{name: "workspace-group", team: f.public, groupID: group.ID, group: &workspaceGroup, status: http.StatusCreated},
			{name: "empty-scope-group", team: f.public, groupID: group.ID, group: &emptyScopeGroup, status: http.StatusCreated},
			{name: "project-label", team: f.public, resource: "project", groupID: group.ID, group: &wrongResource, status: http.StatusCreated},
			{name: "archived-group", team: f.public, groupID: group.ID, group: &archived, status: http.StatusBadRequest},
			{name: "wrong-resource-group", team: f.public, groupID: group.ID, group: &wrongResource, status: http.StatusBadRequest},
			{name: "foreign-team-group", team: f.public, groupID: group.ID, group: &foreignGroup, status: http.StatusBadRequest},
			{name: "missing-group", team: f.public, groupID: group.ID, status: http.StatusNotFound},
			{name: "group-name-not-id", team: f.public, groupID: group.Name, group: &group, status: http.StatusNotFound},
			{name: "missing-team", team: "sw15-missing-team", status: http.StatusNotFound},
			{name: "long-team", team: strings.Repeat("t", 192), status: http.StatusNotFound},
			{name: "nul-team", team: "team\x00missing", status: http.StatusNotFound},
			{name: "long-group", team: f.public, groupID: strings.Repeat("g", 192), status: http.StatusNotFound},
			{name: "nul-group", team: f.public, groupID: "group\x00missing", status: http.StatusNotFound},
			{name: "invalid-resource-before-group", team: f.public, resource: "other", groupID: strings.Repeat("g", 192), status: http.StatusBadRequest},
			{name: "blank-name", team: f.public, nameValue: " ", status: http.StatusBadRequest},
			{name: "cancelled", team: f.public, cancel: true, status: http.StatusInternalServerError},
		}
		f.matrix(t, func(t *testing.T, user domain.User, key *domain.APIKey) {
			for _, spec := range scenarios {
				t.Run(spec.name, func(t *testing.T) {
					var outcomes []any
					for _, source := range []*store.SQLiteStore{repo, f.probe} {
						f.reset(t, spec.group)
						var events []domain.DomainEvent
						var realtime []domain.RealtimeEvent
						source.SetWebhookSink(func(_ string, event domain.DomainEvent) { events = append(events, event) })
						source.SetRealtimeSink(func(_ string, event domain.RealtimeEvent) { realtime = append(realtime, event) })
						name := spec.nameValue
						if name == "" {
							name = "  Created label  "
						}
						body := map[string]any{"name": name, "description": "  Description  ", "color": "client-color"}
						if spec.resource != "" {
							body["resourceType"] = spec.resource
						}
						if spec.groupID != "" {
							body["groupId"] = spec.groupID
						}
						r, w := f.request(user, key, body), httptest.NewRecorder()
						r.SetPathValue("id", spec.team)
						r = r.WithContext(store.ContextWithRealtimeClient(r.Context(), "sw15-label-client"))
						if spec.cancel {
							ctx, cancel := context.WithCancel(r.Context())
							cancel()
							r = r.WithContext(ctx)
						}
						(&server{store: source}).createTeamLabel(w, r)
						if w.Code != spec.status {
							t.Fatalf("status=%d want=%d body=%s", w.Code, spec.status, w.Body.String())
						}
						ids, fingerprint := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
						if fingerprint != baseline || !slices.Equal(ids[:len(f.ids)], f.ids) {
							t.Fatal("creation changed existing label bytes/order")
						}
						generated := map[string]string{}
						var canonical any
						if spec.status == http.StatusCreated {
							if len(ids) != len(f.ids)+1 || len(events) != 1 || len(realtime) != 1 {
								t.Fatal("creation lost an append or publication")
							}
							var created domain.IssueLabel
							if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
								t.Fatal(err)
							}
							if ids[len(ids)-1] != created.ID || created.Name != "Created label" || created.Description != "Description" ||
								created.Scope != spec.team || created.Color != "client-color" || created.GroupID != spec.groupID {
								t.Fatal("created label fields or order changed")
							}
							generated[created.ID], generated[created.CreatedAt.Format(time.RFC3339Nano)] = "<created-id>", "<created-clock>"
							canonical = scaleP21SW15Canonical(t, repo, workspace, store.RecordKey{Kind: "labels", ID: created.ID})
						} else if len(ids) != len(f.ids) || len(events) != 0 || len(realtime) != 0 {
							t.Fatal("failed creation changed canonical rows or published")
						}
						effects := []any{}
						for _, event := range events {
							effects = append(effects, []any{event.Type, event.AggregateID, event.ActorID, event.Payload, event.PreviousValues, event.Snapshot})
						}
						for _, event := range realtime {
							effects = append(effects, []any{event.Type, event.AggregateID, event.ActorID, event.ClientID, event.Payload})
						}
						outcomes = append(outcomes, scaleP21SW6GeneratedJSON(t, []any{
							w.Code, scaleP21M3JSON(t, w.Body.Bytes()), canonical, ids, effects,
							scaleP21SW15Canonical(t, repo, workspace, store.RecordKey{Kind: "users", ID: user.ID}),
						}, generated))
					}
					if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
						t.Fatalf("label creator differential: %s", scaleP21M2Diff(outcomes[0], outcomes[1]))
					}
				})
			}
		})
		if scaleP21SW6RecordHash(t, repo, workspace, "labelGroups", f.groups[0].ID) != otherGroup ||
			scaleP21SW6RecordHash(t, repo, workspace, "projects", "m2-unrelated") != otherProject || f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("label creation touched unrelated bodies or decoded complete metadata")
		}
	}
}

func TestScaleP21SW15TeamLabelCreationBoundaries(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	for _, repo := range scaleP03ServerTestStores(t) {
		f := scaleP21SW15LabelSetup(t, repo)
		workspace := f.data.Workspace.URLKey
		group := domain.LabelGroup{ID: f.groupID, Name: "Target", Scope: f.public, ResourceType: "issue", CreatedAt: f.groups[0].CreatedAt}
		foreign, err := repo.CreateWorkspace(store.ContextWithActor(t.Context(), f.data.Viewer), "SW15 other workspace", idgen.New("sw15-other-workspace-"), "us")
		if err != nil {
			t.Fatal(err)
		}
		f.resources.workspaces = append(f.resources.workspaces, foreign.Workspace.URLKey)
		foreignTeam, foreignGroup := idgen.New("team_sw15_other_"), idgen.New("sw15-other-group-")
		if err := repo.MutateWorkspace(store.WithMetadataFields(t.Context(), "teams", "labelGroups"), foreign.Workspace.URLKey, "issue_label.updated", "", nil, func(data *domain.Bootstrap) error {
			data.Teams = append(data.Teams, domain.Team{ID: foreignTeam, Key: "SW15"})
			data.LabelGroups = append(data.LabelGroups, domain.LabelGroup{ID: foreignGroup, Name: "Other", Scope: "Workspace", ResourceType: "issue", CreatedAt: group.CreatedAt})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, spec := range []struct{ team, group string }{{foreignTeam, ""}, {f.public, foreignGroup}} {
			for _, source := range []*store.SQLiteStore{repo, f.probe} {
				f.reset(t, &group)
				before, _ := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
				r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "Foreign", "groupId": spec.group}), httptest.NewRecorder()
				r.SetPathValue("id", spec.team)
				(&server{store: source}).createTeamLabel(w, r)
				after, _ := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
				if w.Code != http.StatusNotFound || !slices.Equal(before, after) {
					t.Fatal("label creation accepted foreign-workspace inputs", w.Code, w.Body.String())
				}
			}
		}
		member := f.principals[2].user
		queued := []*http.Request{}
		for _, credential := range scaleP21M3Credentials() {
			r := f.request(member, credential.key, map[string]string{"name": "Queued"})
			r.SetPathValue("id", f.public)
			queued = append(queued, r)
		}
		if err := repo.SuspendMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"queued", "replayed"} {
			for _, request := range queued {
				for _, source := range []*store.SQLiteStore{repo, f.probe} {
					f.reset(t, &group)
					r := request.Clone(request.Context())
					w := httptest.NewRecorder()
					scaleP21M3Call(&server{store: source}, w, r, (*server).createTeamLabel)
					ids, _ := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
					if w.Code != http.StatusForbidden || len(ids) != len(f.ids) {
						t.Fatalf("%s creation crossed revoked membership: %d %s", phase, w.Code, w.Body.String())
					}
				}
			}
		}
		if err := repo.ResumeMember(t.Context(), f.data.Workspace.ID, member.ID); err != nil {
			t.Fatal(err)
		}
		for _, spec := range []struct {
			name, description string
			group             domain.LabelGroup
		}{
			{"selected-group", "", func() domain.LabelGroup {
				large := group
				large.Description = strings.Repeat("large selected group ", 20000)
				return large
			}()},
			{"output", strings.Repeat("large label output ", 20000), group},
		} {
			t.Run(spec.name, func(t *testing.T) {
				f.reset(t, &spec.group)
				before, fingerprint := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
				publications := 0
				f.probe.SetWebhookSink(func(string, domain.DomainEvent) { publications++ })
				f.probe.SetRealtimeSink(func(string, domain.RealtimeEvent) { publications++ })
				for range 2 {
					r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "Budget", "groupId": group.ID, "description": spec.description}), httptest.NewRecorder()
					r.SetPathValue("id", f.public)
					(&server{store: f.probe}).createTeamLabel(w, r)
					after, afterFingerprint := scaleP21SW6CreationState(t, repo, workspace, "labels", f.ids)
					if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), store.ErrMetadataPageBudget.Error()) ||
						!slices.Equal(before, after) || fingerprint != afterFingerprint || publications != 0 {
						t.Fatal("budget failure mutated or published label state", w.Code, w.Body.String())
					}
				}
				f.reset(t, &group)
				r, w := f.request(f.data.Viewer, nil, map[string]string{"name": "Recovered", "groupId": group.ID}), httptest.NewRecorder()
				r.SetPathValue("id", f.public)
				(&server{store: f.probe}).createTeamLabel(w, r)
				if w.Code != http.StatusCreated {
					t.Fatal("creation after budget error did not recover", w.Code, w.Body.String())
				}
			})
		}
		if f.probe.WorkspaceMetadataDecodes() != 0 {
			t.Fatal("label creation decoded full metadata")
		}
	}
}
