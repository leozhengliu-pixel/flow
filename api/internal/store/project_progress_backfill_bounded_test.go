package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// Ignore random journal transaction IDs when comparing the durable results.
var projectProgressJournalIDs = regexp.MustCompile(`rj[0-9a-f]{32}`)

func TestProjectProgressStartupBackfillBoundedMatchesLegacy(t *testing.T) {
	t.Run("record-backed", func(t *testing.T) { testProjectProgressStartupBackfill(t, false) })
	t.Run("inline", func(t *testing.T) { testProjectProgressStartupBackfill(t, true) })
}

func testProjectProgressStartupBackfill(t *testing.T, inline bool) {
	t.Helper()
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	dir := t.TempDir()
	seed := filepath.Join(dir, "seed.db")
	open := func(path string, bounded bool) *SQLiteStore {
		t.Helper()
		value := "off"
		if bounded {
			value = "on"
		}
		t.Setenv("FLOW_BOUNDED_METADATA", value)
		repo, err := OpenDatabase(DatabaseConfig{Driver: "sqlite", Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if repo.BoundedMetadataEnabled() != bounded {
			t.Fatalf("bounded=%v store opened with bounded=%v", bounded, repo.BoundedMetadataEnabled())
		}
		return repo
	}
	ctx := t.Context()
	repo := open(seed, false)
	user, token, err := repo.Register(ctx, "Progress owner", idgen.New("progress-")+"@example.test", "progress-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	created, err := repo.CreateWorkspace(ContextWithActor(ctx, user), "Progress backfill", "progress-backfill", "us")
	if err != nil {
		t.Fatal(err)
	}
	workspace := created.Workspace.URLKey
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -3500).Format("2006-01-02")
	built := []domain.ProjectProgressHistoryPoint{{Date: now.AddDate(0, 0, -7).Truncate(24 * time.Hour), Value: 0.5}}
	if err := repo.MutateWorkspace(ctx, workspace, "test.progress_backfill_seed", "", nil, func(data *domain.Bootstrap) error {
		team := data.Teams[0]
		var started, done domain.WorkflowState
		for _, state := range data.States {
			if state.Type == "started" && started.ID == "" {
				started = state
			}
			if state.Type == "completed" && done.ID == "" {
				done = state
			}
		}
		status := data.ProjectStatuses[0]
		project := func(id, name string, history []domain.ProjectProgressHistoryPoint) domain.Project {
			return domain.Project{ID: id, Name: name, SlugID: id, Status: status, TeamIDs: []string{team.ID}, MemberIDs: []string{},
				StartDate: &start, CreatedAt: now.AddDate(0, 0, -21), ProgressHistory: history}
		}
		data.Projects = append(data.Projects, project("project_pending", "Pending history", nil),
			project("project_built", "Built history", built), project("project_second", "Second pending history", nil))
		empty := project("project_empty", "No issues or start date", nil)
		empty.StartDate = nil
		data.Projects = append(data.Projects, empty)
		if inline {
			// Duplicate IDs keep the list inline. Both occurrences are
			// visited independently, using their own start dates and order.
			duplicate := project("project_second", "Second occurrence", nil)
			duplicate.StartDate = nil
			data.Projects = append(data.Projects, duplicate)
		}
		countUnestimated := false
		settings := data.TeamSettings[team.ID]
		settings.EstimateCountUnestimated = &countUnestimated
		data.TeamSettings[team.ID] = settings
		if data.Activities == nil {
			data.Activities = map[string][]domain.ActivityEvent{}
		}
		for index, state := range []domain.WorkflowState{started, done, done} {
			number := data.NextIssueNumber + index
			for _, owner := range []string{"project_pending", "project_built"} {
				ref := domain.ProjectSummary{ID: owner}
				issueID := fmt.Sprintf("issue_progress_%s_%d", owner, index)
				data.Issues = append(data.Issues, domain.Issue{
					ID: issueID, Identifier: fmt.Sprintf("%s-%d", team.Key, number+len(owner)*10),
					Number: number + len(owner)*10, Title: "Progress issue", Team: team, State: state, Creator: user, Project: &ref,
					CreatedAt: now.AddDate(0, 0, -14), UpdatedAt: now.AddDate(0, 0, -14),
					SubscriberIDs: []string{}, Labels: []domain.IssueLabel{},
				})
				if owner == "project_pending" && index == 1 {
					data.Activities[issueID] = []domain.ActivityEvent{{
						ID: "progress_transition", Type: "issue.updated", CreatedAt: now.AddDate(0, 0, -4),
						Metadata: domain.ActivityMetadata{"stateBeforeId": started.ID, "estimateBefore": "3"},
					}}
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A bounded store starts once the metadata catalog covers every record.
	if err := scaleP21SW557CompleteCatalog(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	// The stored histories, read without opening a store (an open runs the
	// backfill).
	history := func(path string) map[string]string {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		result := map[string]string{}
		add := func(project domain.Project) {
			encoded, err := json.Marshal(project.ProgressHistory)
			if err != nil {
				t.Fatal(err)
			}
			result[project.Name] = string(encoded)
		}
		if inline {
			var raw []byte
			if err := db.QueryRowContext(ctx, `SELECT data FROM workspace_states WHERE workspace_key=?`, workspace).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var projects struct{ Projects []domain.Project }
			if err := json.Unmarshal(raw, &projects); err != nil {
				t.Fatal(err)
			}
			for _, project := range projects.Projects {
				add(project)
			}
			return result
		}
		rows, err := db.QueryContext(ctx, `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects'`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var project domain.Project
			if err := json.Unmarshal(raw, &project); err != nil {
				t.Fatal(err)
			}
			add(project)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	seeded := history(seed)
	if seeded["Pending history"] != "null" || seeded["Built history"] == "null" {
		t.Fatalf("seeded histories: %v", seeded)
	}
	dumps := map[bool]string{}
	for _, bounded := range []bool{false, true} {
		path := filepath.Join(dir, fmt.Sprintf("copy-%v.db", bounded))
		scaleP21SW557CopyDatabase(t, seed, path)
		beforeViews := ColdAccessorReadsForTesting()
		beforeFacades, _, _ := ColdHydrationForTesting()
		repo := open(path, bounded)
		if bounded {
			facades, _, _ := ColdHydrationForTesting()
			if beforeViews != ColdAccessorReadsForTesting() || beforeFacades != facades {
				t.Fatal("bounded startup decoded a legacy image")
			}
			stamp, err := readMetadataStamp(ctx, repo.db, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.backfillAllProjectProgress(ctx); err != nil {
				t.Fatal(err)
			}
			again, err := readMetadataStamp(ctx, repo.db, workspace)
			if err != nil || stamp != again || repo.MetadataWorkUsedForTesting() != 0 {
				t.Fatalf("second backfill was not a no-op: stamps %v and %v, error %v, work %d", stamp, again, err, repo.MetadataWorkUsedForTesting())
			}
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		stored := history(path)
		for _, name := range []string{"Pending history", "Second pending history"} {
			if stored[name] == "" || stored[name] == "null" {
				t.Errorf("bounded=%v open did not backfill %s", bounded, name)
			}
		}
		if stored["Built history"] != seeded["Built history"] || stored["No issues or start date"] != "null" {
			t.Errorf("bounded=%v open changed a project that needed no write", bounded)
		}
		if inline && stored["Second occurrence"] != "null" {
			t.Errorf("bounded=%v open applied the first occurrence's progress to the duplicate ID", bounded)
		}
		// Journal transaction ids are random (lines sorted once masked).
		lines := strings.Split(projectProgressJournalIDs.ReplaceAllString(scaleP21SW557Dump(t, path), "rj<id>"), "\n")
		slices.Sort(lines)
		dumps[bounded] = strings.Join(lines, "\n")
	}
	if dumps[false] != dumps[true] {
		legacy, bounded := strings.Split(dumps[false], "\n"), strings.Split(dumps[true], "\n")
		for _, line := range legacy {
			if !slices.Contains(bounded, line) {
				t.Errorf("only flag off: %.240s", line)
			}
		}
		for _, line := range bounded {
			if !slices.Contains(legacy, line) {
				t.Errorf("only flag on: %.240s", line)
			}
		}
		t.Fatal("bounded startup backfill wrote a different database than legacy startup")
	}
	if !inline {
		t.Run("stale-generation", func(t *testing.T) {
			path := filepath.Join(dir, "copy-true.db")
			// Load both copies before creating missing history. Reload only
			// the bounded peer at that revision, then fill it on the other.
			repo := open(path, true)
			defer repo.Close()
			peer := open(path, false)
			defer peer.Close()
			edit := func(history []domain.ProjectProgressHistoryPoint) {
				t.Helper()
				if err := peer.MutateWorkspace(t.Context(), workspace, "test.progress_backfill_peer", "", nil, func(data *domain.Bootstrap) error {
					index := slices.IndexFunc(data.Projects, func(p domain.Project) bool { return p.ID == "project_pending" })
					project := &data.Projects[index]
					project.IssueCountHistory, project.ScopeHistory = nil, nil
					project.CompletedScopeHistory, project.InProgressScopeHistory = nil, nil
					project.ProgressHistory = history
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			edit(nil)
			if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
				t.Fatal(err)
			}
			edit(built)
			stamp, err := readMetadataStamp(t.Context(), peer.db, workspace)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.backfillAllProjectProgress(t.Context()); err != nil {
				t.Fatal(err)
			}
			read, found, err := repo.BeginInstalledMetadataRead(t.Context(), workspace)
			if err != nil || !found {
				t.Fatalf("install after backfill: found=%t err=%v", found, err)
			}
			defer read.Close()
			project, err := read.Select(t.Context(), MetadataRecordScope{Read: []RecordKey{{"projects", "project_pending"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer project.Close()
			if len(project.Data.Projects) != 1 {
				t.Fatalf("read %d projects, want one", len(project.Data.Projects))
			}
			points := project.Data.Projects[0].ProgressHistory
			after, err := readMetadataStamp(t.Context(), repo.db, workspace)
			if err != nil || after != stamp || len(points) != 1 || !points[0].Date.Equal(built[0].Date) || points[0].Value != built[0].Value {
				t.Fatalf("backfill did not keep the peer's history: stamps %v and %v, error %v, history length %d", stamp, after, err, len(points))
			}
		})
	}
}
