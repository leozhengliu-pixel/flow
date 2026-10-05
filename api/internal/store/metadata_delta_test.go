package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type metadataDump struct {
	root    string
	records map[string]string
	search  map[string]string
}

func dumpWorkspaceMetadata(t *testing.T, repo *SQLiteStore, workspace string) metadataDump {
	t.Helper()
	ctx := context.Background()
	dump := metadataDump{records: map[string]string{}, search: map[string]string{}}
	var root []byte
	if err := repo.db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	dump.root = string(root)
	rows, err := repo.db.QueryContext(ctx, `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=?`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var field, key string
		var order int
		var data []byte
		if err := rows.Scan(&field, &key, &order, &data); err != nil {
			t.Fatal(err)
		}
		dump.records[field+"/"+key] = fmt.Sprintf("%d:%s", order, data)
	}
	rows.Close()
	rows, err = repo.db.QueryContext(ctx, `SELECT field,record_key,content FROM metadata_search_documents WHERE workspace_key=?`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var field, key, content string
		if err := rows.Scan(&field, &key, &content); err != nil {
			t.Fatal(err)
		}
		dump.search[field+"/"+key] = content
	}
	rows.Close()
	return dump
}

func writeFullMetadata(t *testing.T, repo *SQLiteStore, workspace string, data domain.Bootstrap) {
	t.Helper()
	ctx := context.Background()
	raw, err := repo.encodeWorkspaceMetadata(data)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := writeWorkspaceMetadata(ctx, tx, workspace, data.Workspace.ID, raw); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The delta writer must leave exactly the rows and root document the full
// metadata write produces.
func TestWorkspaceMetadataDeltaMatchesFullWrite(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source.db")
	seed, err := OpenSQLiteTestFixture(source)
	if err != nil {
		t.Fatal(err)
	}
	const workspace = "test-workspace"
	seed.mu.RLock()
	base := collectionMetadata(seed.workspaces[workspace])
	seed.mu.RUnlock()
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	// Per-project and per-initiative update lists for the update delta cases.
	updates := func(parent string, count int) []domain.ProjectUpdate {
		result := []domain.ProjectUpdate{}
		for i := range count {
			id := fmt.Sprintf("%s_update_%d", parent, i)
			result = append(result, domain.ProjectUpdate{ID: id, ProjectID: parent, Body: "Update " + id, Health: "onTrack", CreatedAt: now.Add(-time.Duration(i) * time.Hour), Comments: []domain.Comment{{ID: id + "_c", Body: "Comment", Reactions: map[string][]string{}}}, Reactions: map[string][]string{"+1": {"usr_admin"}}, Attachments: []domain.Attachment{}})
		}
		return result
	}
	if base.ProjectUpdates == nil {
		base.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	}
	base.ProjectUpdates["delta_p1"], base.ProjectUpdates["delta_p2"] = updates("delta_p1", 4), updates("delta_p2", 3)
	if base.InitiativeUpdates == nil {
		base.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
	}
	base.InitiativeUpdates["delta_i1"] = []domain.InitiativeUpdate{{ID: "delta_i1_update", InitiativeID: "delta_i1", Body: "Initiative", Health: "onTrack", CreatedAt: now, Comments: []domain.Comment{}, Reactions: map[string][]string{}, Attachments: []domain.Attachment{}}}
	cases := map[string]func(*domain.Bootstrap){
		"no change": func(*domain.Bootstrap) {},
		"prepend project update": func(data *domain.Bootstrap) {
			data.ProjectUpdates["delta_p1"] = append(updates("delta_p1_new", 1), data.ProjectUpdates["delta_p1"]...)
		},
		"comment on old project update": func(data *domain.Bootstrap) {
			data.ProjectUpdates["delta_p1"][2].Comments = append(data.ProjectUpdates["delta_p1"][2].Comments, domain.Comment{ID: "late", Body: "Late", Reactions: map[string][]string{}})
		},
		"react to project update": func(data *domain.Bootstrap) {
			data.ProjectUpdates["delta_p2"][0].Reactions["rocket"] = []string{"usr_admin"}
		},
		"delete middle project update": func(data *domain.Bootstrap) {
			data.ProjectUpdates["delta_p1"] = slices.Delete(data.ProjectUpdates["delta_p1"], 1, 2)
		},
		"reorder project updates": func(data *domain.Bootstrap) { slices.Reverse(data.ProjectUpdates["delta_p1"]) },
		"remove update parent":    func(data *domain.Bootstrap) { delete(data.ProjectUpdates, "delta_p2") },
		"new update parent": func(data *domain.Bootstrap) {
			data.ProjectUpdates["delta_p3"] = updates("delta_p3", 2)
		},
		"nil and empty update parents": func(data *domain.Bootstrap) {
			data.ProjectUpdates["delta_p4"], data.ProjectUpdates["delta_p2"] = nil, []domain.ProjectUpdate{}
		},
		"initiative update": func(data *domain.Bootstrap) {
			data.InitiativeUpdates["delta_i1"] = append([]domain.InitiativeUpdate{{ID: "delta_i1_new", InitiativeID: "delta_i1", Body: "New", Comments: []domain.Comment{}, Reactions: map[string][]string{}, Attachments: []domain.Attachment{}}}, data.InitiativeUpdates["delta_i1"]...)
		},
		"workspace name": func(data *domain.Bootstrap) { data.Workspace.Name = "Renamed" },
		"append project": func(data *domain.Bootstrap) {
			project := data.Projects[0]
			project.ID, project.Name, project.MemberIDs = "project_new", "New project", []string{"usr_admin"}
			data.Projects = append(data.Projects, project)
		},
		"edit and reorder projects": func(data *domain.Bootstrap) {
			data.Projects[0].Icon = "Rocket"
			slices.Reverse(data.Projects)
		},
		"delete label": func(data *domain.Bootstrap) { data.Labels = data.Labels[1:] },
		"empty cycles": func(data *domain.Bootstrap) { data.Cycles = []domain.Cycle{} },
		"nil cycles":   func(data *domain.Bootstrap) { data.Cycles = nil },
		"team settings map": func(data *domain.Bootstrap) {
			settings := data.TeamSettings["team_test"]
			settings.Timezone = "Asia/Shanghai"
			data.TeamSettings["team_test"] = settings
		},
		"drop settings key": func(data *domain.Bootstrap) { delete(data.TeamSettings, "team_test") },
		"prepend audit": func(data *domain.Bootstrap) {
			data.AuditLog = append([]domain.AuditLogEntry{{ID: "audit_new", Action: "deleted", ResourceType: "team", CreatedAt: now}}, data.AuditLog...)
		},
		"project updates": func(data *domain.Bootstrap) {
			if data.ProjectUpdates == nil {
				data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
			}
			data.ProjectUpdates["project_aut"] = append(data.ProjectUpdates["project_aut"], domain.ProjectUpdate{ID: "update_new", Body: "On track", Health: "onTrack", CreatedAt: now})
		},
		"duplicate ids fall back to root": func(data *domain.Bootstrap) {
			data.Favorites = []domain.Favorite{{ID: "dup", UserID: "usr_admin"}, {ID: "dup", UserID: "usr_member"}}
		},
		"remove middle label": func(data *domain.Bootstrap) {
			data.Labels = append(slices.Clone(data.Labels[:1]), data.Labels[2:]...)
		},
		"edit append and prepend": func(data *domain.Bootstrap) {
			data.Labels[1].Color = "#abcdef"
			first, extra := data.Labels[0], data.Labels[0]
			first.ID, first.Name = "label_head", "Head"
			extra.ID, extra.Name = "label_tail", "Tail"
			data.Labels = append(append([]domain.IssueLabel{first}, data.Labels[1:]...), extra)
		},
		"insert in the middle": func(data *domain.Bootstrap) {
			middle := data.Labels[0]
			middle.ID, middle.Name = "label_middle", "Middle"
			data.Labels = append(append(slices.Clone(data.Labels[:1]), middle), data.Labels[1:]...)
		},
		"remove all labels": func(data *domain.Bootstrap) { data.Labels = []domain.IssueLabel{} },
		"replace all labels": func(data *domain.Bootstrap) {
			data.Labels = []domain.IssueLabel{{ID: "label_only", Name: "Only", Scope: "Workspace", CreatedAt: now}}
		},
		"settings any":     func(data *domain.Bootstrap) { data.Settings = map[string]any{"nested": map[string]any{"value": 1.0}} },
		"next issue":       func(data *domain.Bootstrap) { data.NextIssueNumber += 7 },
		"users and viewer": func(data *domain.Bootstrap) { data.Users[0].DisplayName = "Renamed admin"; data.Viewer.Name = "Viewer" },
		"many fields": func(data *domain.Bootstrap) {
			data.Workspace.Name = "Many"
			data.Labels[0].Color = "#000000"
			data.Subscriptions = append(data.Subscriptions, domain.Subscription{ID: "sub_new", UserID: "usr_admin", ResourceType: "team", ResourceID: "team_test", CreatedAt: now})
			data.Teams[0].Name = "Renamed team"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			open := func(label string) *SQLiteStore {
				path := filepath.Join(t.TempDir(), label+".db")
				raw, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				repo, err := OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { repo.Close() })
				writeFullMetadata(t, repo, workspace, base)
				return repo
			}
			delta, full := open("delta"), open("full")
			initial := dumpWorkspaceMetadata(t, delta, workspace)
			after := cloneBootstrap(base)
			mutate(&after)
			ctx := context.Background()
			tx, err := delta.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := delta.writeWorkspaceMetadataDelta(ctx, tx, workspace, base, after); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			writeFullMetadata(t, full, workspace, after)
			got, want := dumpWorkspaceMetadata(t, delta, workspace), dumpWorkspaceMetadata(t, full, workspace)
			if got.root != want.root {
				t.Fatalf("root differs:\ndelta %s\nfull  %s", got.root, want.root)
			}
			for key, value := range want.records {
				if got.records[key] != value {
					t.Fatalf("record %s differs:\ndelta %.300s\nfull  %.300s", key, got.records[key], value)
				}
			}
			if len(got.records) != len(want.records) {
				t.Fatalf("record count %d, want %d", len(got.records), len(want.records))
			}
			if !reflect.DeepEqual(got.search, want.search) {
				t.Fatal("metadata search documents differ")
			}
			if unchanged := reflect.DeepEqual(got, initial); unchanged != (name == "no change" || name == "next issue") {
				t.Fatalf("case changed stored metadata = %v", !unchanged)
			}
		})
	}
}

func TestChangedMetadataFieldsDetectsNestedEdits(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "changed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	base := collectionMetadata(repo.Bootstrap())
	paused := time.Now().UTC()
	base.IssueSLAs = []domain.IssueSLA{{ID: "sla", PausedAt: &paused}}
	base.Subscriptions = []domain.Subscription{{ID: "sub", Events: []string{"a"}}}
	base.SLAEvents = []domain.SLAEvent{{ID: "event", Type: "breached"}}
	names := func(fields []metadataField) []string {
		result := []string{}
		for _, field := range fields {
			result = append(result, field.name)
		}
		return result
	}
	if changed := changedMetadataFields(base, cloneBootstrap(base)); len(changed) != 0 {
		t.Fatalf("an unmodified clone reported changes: %v", names(changed))
	}
	cases := map[string]func(*domain.Bootstrap){
		"issueSlas":     func(data *domain.Bootstrap) { *data.IssueSLAs[0].PausedAt = paused.Add(time.Minute) },
		"subscriptions": func(data *domain.Bootstrap) { data.Subscriptions[0].Events[0] = "b" },
		"slaEvents":     func(data *domain.Bootstrap) { data.SLAEvents[0].Type = "completed" },
		"teams":         func(data *domain.Bootstrap) { data.Teams[0].Name = "Changed" },
		"teamSettings": func(data *domain.Bootstrap) {
			settings := data.TeamSettings["team_test"]
			settings.Timezone = "Asia/Tokyo"
			data.TeamSettings["team_test"] = settings
		},
		"projects": func(data *domain.Bootstrap) { data.Projects[0].TeamIDs[0] = "team_other" },
	}
	for field, mutate := range cases {
		after := cloneBootstrap(base)
		mutate(&after)
		if got := names(changedMetadataFields(base, after)); !slices.Equal(got, []string{field}) {
			t.Errorf("%s edit reported %v", field, got)
		}
	}
	// Equal content in a different string allocation is not a change.
	after := cloneBootstrap(base)
	after.SLAEvents[0].Type = string([]byte("breached"))
	if changed := changedMetadataFields(base, after); len(changed) != 0 {
		t.Fatalf("equal strings reported changes: %v", names(changed))
	}
}

// High-frequency settings, catalog and team administration writes must not
// load every issue and discussion record. Adding a cascade to one of these
// callbacks means moving it to a scoped path, not dropping it from this list.
func TestHighFrequencyEventsAvoidFullWorkspacePath(t *testing.T) {
	events := []struct {
		event   string
		payload any
	}{
		{"project_display_default.updated", nil}, {"workspace.settings_updated", nil}, {"workspace_preferences.updated", nil},
		{"notification_preferences.updated", nil}, {"account.profile_updated", nil}, {"user_settings.updated", nil},
		{"team.created", nil}, {"team.settings_updated", nil}, {"team.deleted", nil}, {"team.archived", nil}, {"team.unarchived", nil},
		{"team.updated", map[string]any{"name": "x"}}, {"team_member.updated", nil},
		{"workspace_member.updated", nil}, {"workspace_member.suspended", nil}, {"workspace_member.resumed", nil}, {"workspace_member.removed", nil},
		{"workspace_member.username_updated", nil}, {"workspace_member.identity_cascaded", nil},
		{"workspace_invitations.created", nil}, {"workspace_invitation.revoked", nil}, {"workspace_invite_link.rotated", nil},
		{"view.created", nil}, {"view.updated", nil}, {"view.deleted", nil}, {"dashboard.created", nil}, {"dashboard.updated", nil}, {"dashboard.deleted", nil},
		{"label.created", nil}, {"label.updated", map[string]any{"name": "x"}}, {"issue_label.updated", map[string]any{"color": "#fff"}}, {"label_group.created", nil}, {"label_group.updated", map[string]any{"name": "x"}},
		{"issue_template.created", nil}, {"issue_template.updated", nil}, {"document_template.created", nil}, {"document_template.updated", nil},
		{"project_status.created", nil}, {"project_status.updated", nil}, {"project.updated", nil}, {"project.update_updated", nil},
		{"loop.created", nil}, {"loop.updated", nil}, {"customer.created", nil}, {"customer.updated", nil}, {"customer_taxonomy.updated", nil},
		{"release.deleted", nil}, {"release.reordered", nil}, {"release.note_created", nil}, {"release_pipeline.updated", nil},
		{"favorite.added", nil}, {"favorite.removed", nil}, {"post.updated", nil}, {"ask.created", nil}, {"ask.updated", nil},
	}
	for _, item := range events {
		if !metadataOnlyMutation(item.event, item.payload) && !metadataTeamMutation(item.event, item.payload) {
			t.Errorf("%s takes the full workspace path", item.event)
		}
	}
}

// A comment on one project's update rewrites only that project's update rows:
// another project's rows are neither read for payloads nor rewritten, and the
// record group pass stays linear when every project changes.
func TestUpdatesDeltaTouchesOnlyChangedParentAndStaysLinear(t *testing.T) {
	repo, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "flow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	const workspace, parents, perParent = "test-workspace", 120, 12
	now := time.Now().UTC()
	repo.mu.RLock()
	base := cloneBootstrap(collectionMetadata(repo.workspaces[workspace]))
	repo.mu.RUnlock()
	base.ProjectUpdates = map[string][]domain.ProjectUpdate{}
	for p := range parents {
		parent := fmt.Sprintf("linear_p%d", p)
		for u := range perParent {
			base.ProjectUpdates[parent] = append(base.ProjectUpdates[parent], domain.ProjectUpdate{ID: fmt.Sprintf("%s_u%d", parent, u), ProjectID: parent, Body: "Body", CreatedAt: now, Comments: []domain.Comment{}, Reactions: map[string][]string{}, Attachments: []domain.Attachment{}})
		}
	}
	writeFullMetadata(t, repo, workspace, base)
	// Mark a row of another project; a delta that rewrote it would reset it.
	key := metadataRecordKeyFor("projectUpdates", "linear_p1", "linear_p1_u3")
	if _, err := repo.db.Exec(`UPDATE workspace_metadata_records SET collection_order=987 WHERE workspace_key=? AND field='projectUpdates' AND record_key=?`, workspace, key); err != nil {
		t.Fatal(err)
	}
	write := func(before, after domain.Bootstrap) {
		t.Helper()
		tx, err := repo.db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := repo.writeWorkspaceMetadataDelta(context.Background(), tx, workspace, before, after); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	after := cloneBootstrap(base)
	after.ProjectUpdates["linear_p0"][5].Comments = append(after.ProjectUpdates["linear_p0"][5].Comments, domain.Comment{ID: "comment", Body: "Hi", Reactions: map[string][]string{}})
	evaluations := metadataGroupEvaluations.Load()
	write(base, after)
	if got := metadataGroupEvaluations.Load() - evaluations; got > 2*(perParent+1) {
		t.Fatalf("one-project comment evaluated %d record groups, want at most %d", got, 2*(perParent+1))
	}
	var order int
	if err := repo.db.QueryRow(`SELECT collection_order FROM workspace_metadata_records WHERE workspace_key=? AND field='projectUpdates' AND record_key=?`, workspace, key).Scan(&order); err != nil || order != 987 {
		t.Fatalf("another project's row was rewritten: order %d (%v)", order, err)
	}
	var stored []byte
	if err := repo.db.QueryRow(`SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projectUpdates' AND record_key=?`, workspace, metadataRecordKeyFor("projectUpdates", "linear_p0", "linear_p0_u5")).Scan(&stored); err != nil || !strings.Contains(string(stored), `"id":"comment"`) {
		t.Fatalf("commented update not written: %s (%v)", stored, err)
	}
	// Every project gains an update: each record's group is evaluated a
	// bounded number of times, not once per (group, record) pair.
	next := cloneBootstrap(after)
	for parent := range next.ProjectUpdates {
		next.ProjectUpdates[parent] = append([]domain.ProjectUpdate{{ID: parent + "_new", ProjectID: parent, Body: "New", CreatedAt: now, Comments: []domain.Comment{}, Reactions: map[string][]string{}, Attachments: []domain.Attachment{}}}, next.ProjectUpdates[parent]...)
	}
	evaluations = metadataGroupEvaluations.Load()
	write(after, next)
	records := parents * (perParent + 2)
	if got := metadataGroupEvaluations.Load() - evaluations; got > int64(2*records) {
		t.Fatalf("all-project write evaluated %d record groups for %d records (quadratic)", got, records)
	}
	if got := len(repo.mustExpandProjectUpdates(t, workspace)["linear_p7"]); got != perParent+1 {
		t.Fatalf("linear_p7 has %d updates after the write, want %d", got, perParent+1)
	}
}

func metadataRecordKeyFor(field, parent, id string) string {
	records, _ := splitUpdateMetadata(field, map[string]json.RawMessage{parent: json.RawMessage(`[{"id":"` + id + `"}]`)})
	for key, value := range records {
		if update, item := metadataUpdateParent(value.data); item && update == parent {
			return key.key
		}
	}
	return ""
}

func (s *SQLiteStore) mustExpandProjectUpdates(t *testing.T, workspace string) map[string][]json.RawMessage {
	t.Helper()
	var root []byte
	if err := s.db.QueryRow(`SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&root); err != nil {
		t.Fatal(err)
	}
	expanded, err := s.expandWorkspaceMetadata(context.Background(), workspace, root)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ProjectUpdates map[string][]json.RawMessage `json:"projectUpdates"`
	}
	if err := json.Unmarshal(expanded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.ProjectUpdates
}

func TestMetadataUpdateParentReadsThePrefix(t *testing.T) {
	for _, test := range []struct {
		value  metadataUpdate
		parent string
		item   bool
	}{
		{metadataUpdate{Parent: "project_1", Item: json.RawMessage(`{"id":"u"}`)}, "project_1", true},
		{metadataUpdate{Parent: "project_1"}, "project_1", false},
		{metadataUpdate{Parent: "project_1", Null: true}, "project_1", false},
		{metadataUpdate{Parent: `quoted "<parent>"`, Item: json.RawMessage(`{"id":"u"}`)}, `quoted "<parent>"`, true},
	} {
		raw, _ := json.Marshal(test.value)
		if parent, item := metadataUpdateParent(raw); parent != test.parent || item != test.item {
			t.Errorf("metadataUpdateParent(%s) = %q, %v", raw, parent, item)
		}
	}
}
