package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// scaleP21SW293Logical returns the JSON of the named fields of the view's
// installed logical generation of workspace, with its stamp.
func scaleP21SW293Logical(t *testing.T, view *SQLiteStore, workspace string, fields ...string) (map[string]string, copyStamp) {
	t.Helper()
	source, err := view.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(t.Context()), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Release()
	return scaleP21SW293Fields(t, scaleP21SW28DecodeLogicalSource(t, source), fields...), source.Stamp()
}

func scaleP21SW293Fields(t *testing.T, data domain.Bootstrap, fields ...string) map[string]string {
	t.Helper()
	result := map[string]string{}
	value := reflect.ValueOf(data)
	for _, name := range fields {
		index, _, ok := metadataLogicalField(name)
		if !ok {
			t.Fatalf("unknown field %s", name)
		}
		raw, err := json.Marshal(value.Field(index).Interface())
		if err != nil {
			t.Fatal(err)
		}
		result[name] = string(raw)
	}
	return result
}

func scaleP21SW293Policy(t *testing.T, view *SQLiteStore, workspace string) string {
	t.Helper()
	view.mu.RLock()
	data := view.workspaces[workspace]
	view.mu.RUnlock()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func scaleP21SW293Row(t *testing.T, f scaleP09Fixture, workspace, field, key string) []byte {
	t.Helper()
	var raw []byte
	err := f.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestScaleP21SW293APIKeyUseDifferential compares API key telemetry on the
// legacy store (patched resident copy) with a bounded store (patched logical
// generation, policy copy untouched): warm state, canonical rows and the
// state reloaded from SQL must be identical, including revoked records, key
// or grant only, absent ids, duplicate ids stored inline (only the first is
// patched, no row changes) and rows whose minute predicate skips the SQL
// update while the resident copy still takes the use.
func TestScaleP21SW293APIKeyUseDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	recent, past := at.Add(-30*time.Second), at.Add(-time.Hour)
	key := func(id, name string) domain.APIKey {
		return domain.APIKey{ID: id, Name: name, CreatorID: f.data.Viewer.ID, Scopes: []string{"read"}, TeamIDs: []string{}, TeamRestriction: "all", CreatedAt: at.Add(-48 * time.Hour)}
	}
	grant := func(id string) domain.OAuthAuthorization {
		return domain.OAuthAuthorization{ID: id, ClientID: "sw293-client", UserID: f.data.Viewer.ID, Scopes: []string{"read"}, CreatedAt: at.Add(-48 * time.Hour)}
	}
	revokedKey, revokedGrant := key("sw293-key", "revoked"), grant("sw293-grant")
	revokedKey.RevokedAt, revokedGrant.RevokedAt = &past, &past
	throttledKey, throttledGrant := key("sw293-key", "throttled"), grant("sw293-grant")
	throttledKey.LastUsedAt, throttledGrant.LastUsedAt = &recent, &recent
	cases := []struct {
		name    string
		keys    []domain.APIKey
		grants  []domain.OAuthAuthorization
		use     apiKeyUse
		changed bool
	}{
		{"active", []domain.APIKey{key("sw293-other", "other"), key("sw293-key", "active")}, []domain.OAuthAuthorization{grant("sw293-grant")},
			apiKeyUse{workspace: workspace, keyID: "sw293-key", authorizationID: "sw293-grant", at: at}, true},
		{"revoked", []domain.APIKey{revokedKey}, []domain.OAuthAuthorization{revokedGrant},
			apiKeyUse{workspace: workspace, keyID: "sw293-key", authorizationID: "sw293-grant", at: at}, false},
		{"key-only", []domain.APIKey{key("sw293-key", "key only")}, []domain.OAuthAuthorization{grant("sw293-grant")},
			apiKeyUse{workspace: workspace, keyID: "sw293-key", at: at}, true},
		{"absent", []domain.APIKey{key("sw293-other", "absent")}, []domain.OAuthAuthorization{},
			apiKeyUse{workspace: workspace, keyID: "sw293-key", authorizationID: "sw293-grant", at: at}, false},
		{"duplicate-inline", []domain.APIKey{key("sw293-key", "first"), key("sw293-key", "second")}, []domain.OAuthAuthorization{grant("sw293-grant")},
			apiKeyUse{workspace: workspace, keyID: "sw293-key", authorizationID: "sw293-grant", at: at}, true},
		{"row-throttled", []domain.APIKey{throttledKey}, []domain.OAuthAuthorization{throttledGrant},
			apiKeyUse{workspace: workspace, keyID: "sw293-key", authorizationID: "sw293-grant", at: at}, true},
	}
	fields := []string{"apiKeys", "oauthAuthorizations"}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			seed := func() map[string]string {
				t.Helper()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw293_seed", "", nil, func(data *domain.Bootstrap) error {
					data.APIKeys, data.OAuthAuthorizations = slices.Clone(test.keys), slices.Clone(test.grants)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				data, _ := f.WorkspaceMetadata(workspace)
				return scaleP21SW293Fields(t, data, fields...)
			}
			rows := func() [][]byte {
				return [][]byte{scaleP21SW293Row(t, f, workspace, "apiKeys", "sw293-key"), scaleP21SW293Row(t, f, workspace, "oauthAuthorizations", "sw293-grant")}
			}
			reloaded := func() map[string]string {
				t.Helper()
				if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
					t.Fatal(err)
				}
				data, _ := f.WorkspaceMetadata(workspace)
				return scaleP21SW293Fields(t, data, fields...)
			}

			// Bounded: the logical generation takes the use, the policy copy
			// and the generation of an unchanged workspace stay.
			seeded := seed()
			view, err := f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer view.releaseMetadataLogicalSources()
			policy := scaleP21SW293Policy(t, view, workspace)
			before, beforeStamp := scaleP21SW293Logical(t, view, workspace, fields...)
			if !reflect.DeepEqual(before, seeded) {
				t.Fatalf("bounded view does not start from the seed: %v want %v", before, seeded)
			}
			if err := view.writeAPIKeyUse(t.Context(), test.use); err != nil {
				t.Fatal(err)
			}
			boundedWarm, stamp := scaleP21SW293Logical(t, view, workspace, fields...)
			boundedRows := rows()
			if scaleP21SW293Policy(t, view, workspace) != policy || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
				t.Fatal("bounded telemetry changed the policy copy, decoded the workspace or leaked work admission")
			}
			if test.changed != !reflect.DeepEqual(boundedWarm, before) {
				t.Fatalf("bounded change = %t, want %t: %v", !reflect.DeepEqual(boundedWarm, before), test.changed, boundedWarm)
			}
			if test.changed && stamp != (copyStamp{workspaceID: beforeStamp.workspaceID, revision: -1}) || !test.changed && stamp != beforeStamp {
				t.Fatalf("bounded stamp %+v from %+v (changed %t)", stamp, beforeStamp, test.changed)
			}
			if err := view.writeAPIKeyUse(t.Context(), apiKeyUse{workspace: "sw293-missing", keyID: "sw293-key", at: at}); err != nil {
				t.Fatal("a workspace the server has not installed is skipped", err)
			}
			boundedSQL := reloaded()
			fresh, err := f.BoundedMetadataViewForTesting(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.releaseMetadataLogicalSources()
			freshLogical, _ := scaleP21SW293Logical(t, fresh, workspace, fields...)
			if !reflect.DeepEqual(freshLogical, boundedSQL) {
				t.Fatalf("bounded SQL-reloaded generation differs from the stored state:\n%v\n%v", freshLogical, boundedSQL)
			}

			// Legacy: the resident copy takes the same use.
			if again := seed(); !reflect.DeepEqual(again, seeded) {
				t.Fatalf("reseed differs: %v want %v", again, seeded)
			}
			if err := f.writeAPIKeyUse(t.Context(), test.use); err != nil {
				t.Fatal(err)
			}
			legacy, _ := f.WorkspaceMetadata(workspace)
			legacyWarm, legacyRows := scaleP21SW293Fields(t, legacy, fields...), rows()
			legacySQL := reloaded()

			if !reflect.DeepEqual(legacyWarm, boundedWarm) {
				t.Fatalf("warm state differs\nlegacy=%v\nbounded=%v", legacyWarm, boundedWarm)
			}
			for index := range legacyRows {
				if !bytes.Equal(legacyRows[index], boundedRows[index]) {
					t.Fatalf("canonical row %d differs\nlegacy=%s\nbounded=%s", index, legacyRows[index], boundedRows[index])
				}
			}
			if !reflect.DeepEqual(legacySQL, boundedSQL) {
				t.Fatalf("stored state differs\nlegacy=%v\nbounded=%v", legacySQL, boundedSQL)
			}
			// The resident copy is the legacy one even where the row's
			// minute predicate or inline storage kept the row as it was.
			if inlineOrSkipped := test.name == "duplicate-inline" || test.name == "row-throttled"; inlineOrSkipped == reflect.DeepEqual(legacyWarm, legacySQL) {
				t.Fatalf("warm/stored divergence = %t, want %t\n%v\n%v", !reflect.DeepEqual(legacyWarm, legacySQL), inlineOrSkipped, legacyWarm, legacySQL)
			}
		})
	}
}

// scaleP21SW293PeerEdit rewrites one stored record as another server would.
func scaleP21SW293PeerEdit(t *testing.T, f scaleP09Fixture, workspace, field, key string, edit func(map[string]any)) {
	t.Helper()
	raw := scaleP21SW293Row(t, f, workspace, field, key)
	if raw == nil {
		t.Fatalf("no stored %s %s", field, key)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if edit == nil {
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`, workspace, field, key); err != nil {
			t.Fatal(err)
		}
		return
	}
	edit(record)
	next, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, next, workspace, field, key); err != nil {
		t.Fatal(err)
	}
}

func scaleP21SW293PeerEditRaw(t *testing.T, f scaleP09Fixture, workspace, field, key string, edit func([]byte) []byte) {
	t.Helper()
	raw := scaleP21SW293Row(t, f, workspace, field, key)
	if raw == nil {
		t.Fatalf("no stored %s %s", field, key)
	}
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_metadata_records SET data=? WHERE workspace_key=? AND field=? AND record_key=?`, edit(raw), workspace, field, key); err != nil {
		t.Fatal(err)
	}
}

// TestScaleP21SW293ConflictRefreshDifferential compares the conflict refresh
// (refreshConflictedMetadata) and the record refresh (RefreshMetadataRecord)
// of the legacy store with a bounded store after another server changed,
// deleted or renamed records: projects, a policy team, an agent session, a
// field the legacy refresh skips (projectUpdates) and an unknown field. The
// bounded store refreshes its logical generation (unknown stamp) and its
// policy copy only through the policy projection; it never copies records
// into the policy copy and never decodes the workspace.
func TestScaleP21SW293ConflictRefreshDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	team := f.data.Teams[0]
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw293_refresh_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects,
			domain.Project{ID: "sw293-project-1", Name: "First", TeamIDs: []string{team.ID}, CreatedAt: at},
			domain.Project{ID: "sw293-project-2", Name: "Second", TeamIDs: []string{team.ID}, CreatedAt: at},
			domain.Project{ID: "sw293-project-3", Name: "Third", TeamIDs: []string{team.ID}, CreatedAt: at})
		if data.ProjectUpdates == nil {
			data.ProjectUpdates = map[string][]domain.ProjectUpdate{}
		}
		data.ProjectUpdates["sw293-project-1"] = []domain.ProjectUpdate{{ID: "sw293-update", ProjectID: "sw293-project-1", Body: "Original", Health: "onTrack"}}
		data.AgentSessions = append(data.AgentSessions, domain.AgentSession{ID: "sw293-session", UserID: f.data.Viewer.ID, Title: "Original", IssueIDs: []string{}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fields := []string{"projects", "teams", "projectUpdates", "agentSessions"}
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	legacyBefore, _ := f.WorkspaceMetadata(workspace)
	if before, _ := scaleP21SW293Logical(t, view, workspace, fields...); !reflect.DeepEqual(before, scaleP21SW293Fields(t, legacyBefore, fields...)) {
		t.Fatal("bounded view does not start from the legacy copy")
	}
	// The stored state is what a legacy reload reads (after the legacy
	// copy's own comparison: the reload replaces that copy).
	stored := func() map[string]string {
		t.Helper()
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		data, _ := f.WorkspaceMetadata(workspace)
		return scaleP21SW293Fields(t, data, fields...)
	}

	scaleP21SW293PeerEdit(t, f, workspace, "projects", "sw293-project-1", func(record map[string]any) { record["name"] = "Peer renamed" })
	scaleP21SW293PeerEdit(t, f, workspace, "projects", "sw293-project-2", nil)
	scaleP21SW293PeerEdit(t, f, workspace, "teams", team.ID, func(record map[string]any) { record["name"] = "Peer team" })
	scaleP21SW293PeerEdit(t, f, workspace, "agentSessions", "sw293-session", func(record map[string]any) { record["title"] = "Peer title" })
	updateKey := metadataRecordKeyFor("projectUpdates", "sw293-project-1", "sw293-update")
	scaleP21SW293PeerEditRaw(t, f, workspace, "projectUpdates", updateKey, func(raw []byte) []byte { return bytes.Replace(raw, []byte("Original"), []byte("Peer body"), 1) })
	conflict := &metadataConflictError{keys: []metadataRecordKey{
		{"projects", "sw293-project-1"}, {"projects", "sw293-project-2"}, {"teams", team.ID},
		{"projectUpdates", updateKey}, {"sw293Unknown", "x"}, {"agentSessions", "sw293-session"}, {"projects", "sw293-missing"},
	}}
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		refresh := repo.refreshConflictedMetadata(t.Context(), workspace)
		if err := refresh(errors.New("not a conflict")); err != nil {
			t.Fatal(err)
		}
		if err := refresh(fmt.Errorf("wrapped: %w", conflict)); err != nil {
			t.Fatal(err)
		}
		if err := repo.refreshConflictedMetadata(t.Context(), "sw293-missing")(conflict); err != nil {
			t.Fatal("a workspace the server has not installed is skipped", err)
		}
	}
	legacy, _ := f.WorkspaceMetadata(workspace)
	legacyFields := scaleP21SW293Fields(t, legacy, fields...)
	boundedFields, stamp := scaleP21SW293Logical(t, view, workspace, fields...)
	if !reflect.DeepEqual(legacyFields, boundedFields) {
		t.Fatalf("refreshed records differ\nlegacy=%v\nbounded=%v", legacyFields, boundedFields)
	}
	if stamp.revision != -1 || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatalf("bounded refresh stamp %+v decodes %d work %d", stamp, view.WorkspaceMetadataDecodes(), view.metadataResidency.work.used)
	}
	view.mu.RLock()
	policy := view.workspaces[workspace]
	view.mu.RUnlock()
	if len(policy.Projects) != 0 || len(policy.AgentSessions) != 0 || len(policy.ProjectUpdates) != 0 {
		t.Fatal("bounded refresh copied records into the policy copy")
	}
	if policyTeams, _ := json.Marshal(policy.Teams); string(policyTeams) != legacyFields["teams"] {
		t.Fatalf("bounded policy teams %s want %s", policyTeams, legacyFields["teams"])
	}
	want := stored()
	for _, name := range []string{"projects", "teams", "agentSessions"} {
		if boundedFields[name] != want[name] {
			t.Fatalf("refreshed %s differs from the stored state\nrefreshed=%s\nstored=%s", name, boundedFields[name], want[name])
		}
	}
	if boundedFields["projectUpdates"] == want["projectUpdates"] {
		t.Fatal("the skipped projectUpdates field was refreshed")
	}

	// A single-record refresh (another server undid an agent reply).
	scaleP21SW293PeerEdit(t, f, workspace, "agentSessions", "sw293-session", func(record map[string]any) { record["title"] = "Peer again" })
	for _, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		if err := repo.RefreshMetadataRecord(t.Context(), workspace, "agentSessions", "sw293-session"); err != nil {
			t.Fatal(err)
		}
	}
	legacy, _ = f.WorkspaceMetadata(workspace)
	boundedFields, _ = scaleP21SW293Logical(t, view, workspace, fields...)
	if got, want := boundedFields["agentSessions"], scaleP21SW293Fields(t, legacy, "agentSessions")["agentSessions"]; got != want || got != stored()["agentSessions"] {
		t.Fatalf("record refresh differs\nbounded=%s\nlegacy=%s", got, want)
	}
}

// TestScaleP21SW293IssueUpdateConflictRetry runs a HOT issue update whose
// first attempt conflicts on a project another server renamed, on the legacy
// store and on a bounded store: both rerun once on the stored project and
// return the same issue; the bounded store's retry base is its refreshed
// logical generation, never a policy copy with records patched in.
func TestScaleP21SW293IssueUpdateConflictRetry(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_NARROW_MUTATION_INPUTS", "off")
	f := scaleP09Database(t)
	workspace := f.data.Workspace.URLKey
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	var issues []domain.Issue
	if err := f.MutateWorkspace(t.Context(), workspace, "scale_p21.sw293_retry_seed", "", nil, func(data *domain.Bootstrap) error {
		data.Projects = append(data.Projects, domain.Project{ID: "sw293-retry-project", Name: "Before", TeamIDs: []string{f.data.Teams[0].ID}, CreatedAt: at})
		for index := range 2 {
			issue := domain.Issue{ID: fmt.Sprint("sw293-retry-issue-", index), Identifier: fmt.Sprintf("%s-%d", f.data.Teams[0].Key, index+1), Number: index + 1, Version: 1,
				Creator: f.data.Viewer, Team: f.data.Teams[0], State: f.data.States[0], CreatedAt: at, UpdatedAt: at}
			issues = append(issues, issue)
			data.Issues = append(data.Issues, issue)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.BoundedMetadataViewForWorkspacesForTesting(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer view.releaseMetadataLogicalSources()
	policy := scaleP21SW293Policy(t, view, workspace)
	input := domain.IssueUpdateInput{Title: new("Retried")}
	var outputs []domain.Issue
	for index, repo := range []*SQLiteStore{f.SQLiteStore, view} {
		name := fmt.Sprint("Peer renamed ", index)
		scaleP21SW293PeerEdit(t, f, workspace, "projects", "sw293-retry-project", func(record map[string]any) { record["name"] = name })
		attempts := 0
		output, err := repo.UpdateIssueRecord(t.Context(), workspace, issues[index].ID, &issues[index].Version, IssueMutationScope{Payload: input}, func(data *domain.Bootstrap, current *domain.Issue) error {
			attempts++
			if attempts == 1 {
				current.Title = "First attempt"
				return &metadataConflictError{keys: []metadataRecordKey{{"projects", "sw293-retry-project"}}}
			}
			current.Title = *input.Title
			return nil
		})
		if err != nil || attempts != 2 {
			t.Fatalf("store %d: attempts=%d err=%v", index, attempts, err)
		}
		var projects string
		if index == 0 {
			legacy, _ := f.WorkspaceMetadata(workspace)
			projects = scaleP21SW293Fields(t, legacy, "projects")["projects"]
		} else {
			logical, _ := scaleP21SW293Logical(t, view, workspace, "projects")
			projects = logical["projects"]
		}
		if !bytes.Contains([]byte(projects), []byte(`"name":"`+name+`"`)) {
			t.Fatalf("store %d retried without the stored project: %s", index, projects)
		}
		output.ID, output.Identifier, output.Number, output.UpdatedAt = "", "", 0, time.Time{}
		outputs = append(outputs, output)
	}
	if !reflect.DeepEqual(outputs[0], outputs[1]) {
		t.Fatalf("legacy and bounded retries differ\n%+v\n%+v", outputs[0], outputs[1])
	}
	if scaleP21SW293Policy(t, view, workspace) != policy || view.WorkspaceMetadataDecodes() != 0 || view.metadataResidency.work.used != 0 {
		t.Fatal("bounded retry changed the policy copy, decoded the workspace or leaked work admission")
	}
}
