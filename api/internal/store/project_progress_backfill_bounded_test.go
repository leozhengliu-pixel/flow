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

// The startup project progress backfill (backfillAllProjectProgress) builds
// the history of every project that has none. A bounded image holds no
// projects, so with bounded metadata on the pass found nothing to backfill and
// a project created since activation never got a progress graph. A database
// holding such a project (and one whose history is already built) is copied
// twice; one copy opens flag off, the other flag on (the production bounded
// store): both opens must backfill the project and end with the same rows in
// every table.
var projectProgressJournalIDs = regexp.MustCompile(`rj[0-9a-f]{32}`)

func TestProjectProgressStartupBackfillBoundedMatchesLegacy(t *testing.T) {
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
	start := time.Now().UTC().AddDate(0, 0, -21).Format("2006-01-02")
	built := []domain.ProjectProgressHistoryPoint{{Date: time.Now().UTC().AddDate(0, 0, -7).Truncate(24 * time.Hour), Value: 0.5}}
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
				StartDate: &start, CreatedAt: time.Now().UTC().AddDate(0, 0, -21), ProgressHistory: history}
		}
		data.Projects = append(data.Projects, project("project_pending", "Pending history", nil), project("project_built", "Built history", built))
		for index, state := range []domain.WorkflowState{started, done, done} {
			number := data.NextIssueNumber + index
			for _, owner := range []string{"project_pending", "project_built"} {
				ref := domain.ProjectSummary{ID: owner}
				data.Issues = append(data.Issues, domain.Issue{
					ID: fmt.Sprintf("issue_progress_%s_%d", owner, index), Identifier: fmt.Sprintf("%s-%d", team.Key, number+len(owner)*10),
					Number: number + len(owner)*10, Title: "Progress issue", Team: team, State: state, Creator: user, Project: &ref,
					CreatedAt: time.Now().UTC().AddDate(0, 0, -14), UpdatedAt: time.Now().UTC().AddDate(0, 0, -14),
					SubscriberIDs: []string{}, Labels: []domain.IssueLabel{},
				})
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
		rows, err := db.QueryContext(ctx, `SELECT record_key,data FROM workspace_metadata_records WHERE workspace_key=? AND field='projects'`, workspace)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var key string
			var raw []byte
			if err := rows.Scan(&key, &raw); err != nil {
				t.Fatal(err)
			}
			var project domain.Project
			if err := json.Unmarshal(raw, &project); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(project.ProgressHistory)
			result[key] = string(encoded)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	seeded := history(seed)
	if seeded["project_pending"] != "null" || seeded["project_built"] == "null" {
		t.Fatalf("seeded histories: %v", seeded)
	}
	dumps := map[bool]string{}
	for _, bounded := range []bool{false, true} {
		path := filepath.Join(dir, fmt.Sprintf("copy-%v.db", bounded))
		scaleP21SW557CopyDatabase(t, seed, path)
		repo := open(path, bounded)
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		stored := history(path)
		if stored["project_pending"] == "null" {
			t.Errorf("bounded=%v open did not backfill the project's progress history", bounded)
		}
		if stored["project_built"] != seeded["project_built"] {
			t.Errorf("bounded=%v open rebuilt a project that had a history: %s", bounded, stored["project_built"])
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
				t.Errorf("only flag off: %s", line)
			}
		}
		for _, line := range bounded {
			if !slices.Contains(legacy, line) {
				t.Errorf("only flag on: %s", line)
			}
		}
		t.Fatal("bounded startup backfill wrote a different database than legacy startup")
	}
}
