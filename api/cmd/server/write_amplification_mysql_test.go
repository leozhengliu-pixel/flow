package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Opt-in only: requires a fresh, isolated MySQL database and ROW/FULL binlogs.
// No persistent demonstration data is shipped with the application.
func TestMySQLImportWriteAmplification(t *testing.T) {
	dsn := os.Getenv("FLOW_TEST_WRITE_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var logging int
	var format, image string
	if err := db.QueryRow("SELECT @@log_bin,@@binlog_format,@@binlog_row_image").Scan(&logging, &format, &image); err != nil {
		t.Fatal(err)
	}
	if logging != 1 || format != "ROW" || image != "FULL" {
		t.Fatal("binlog regression requires binary logging with ROW/FULL")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("write amplification test requires an empty database")
	}
	repo, err := store.OpenDatabase(store.DatabaseConfig{Driver: "mysql", URL: dsn, FixtureProfile: "test", FixturePassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	ctx := context.Background()
	data := repo.Bootstrap()
	key := data.Workspace.URLKey
	position := func() int64 {
		t.Helper()
		rows, err := db.Query("SHOW BINARY LOGS")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, _ := rows.Columns()
		var sum int64
		for rows.Next() {
			values := make([]sql.RawBytes, len(columns))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			var size int64
			if _, err := fmt.Sscan(string(values[1]), &size); err != nil {
				t.Fatal(err)
			}
			sum += size
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return sum
	}
	measure := func(name string, run func()) int64 {
		t.Helper()
		before := position()
		start := time.Now()
		run()
		bytes := position() - before
		t.Logf("BINLOG phase=%s bytes=%d duration=%s", name, bytes, time.Since(start))
		return bytes
	}
	issues := make([]domain.Issue, 500)
	for i := range issues {
		issue := data.Issues[0]
		issue.ID = fmt.Sprintf("write-%04d", i)
		issue.Identifier = fmt.Sprintf("WRITE-%d", i+100)
		issue.Number = i + 100
		issue.Description = strings.Repeat("x", 1024)
		issue.SubscriberIDs = []string{data.Viewer.ID}
		issues[i] = issue
	}
	measure("insert_500", func() {
		if err := repo.ImportIssues(ctx, key, issues); err != nil {
			t.Fatal(err)
		}
	})
	replay := measure("replay_500", func() {
		if err := repo.ImportIssues(ctx, key, issues); err != nil {
			t.Fatal(err)
		}
	})
	issues[0].Title = "A changed title"
	update := measure("one_title", func() {
		if err := repo.ImportIssues(ctx, key, issues[:1]); err != nil {
			t.Fatal(err)
		}
	})
	if err := repo.MutateWorkspace(ctx, key, "import.previewed", "test-import", nil, func(data *domain.Bootstrap) error {
		for i := 0; i < 300; i++ {
			project := data.Projects[0]
			project.ID = fmt.Sprintf("meta-%04d", i)
			project.Description = strings.Repeat("p", 2048)
			data.Projects = append(data.Projects, project)
		}
		rows := make([]map[string]string, 2000)
		for i := range rows {
			rows[i] = map[string]string{"Title": fmt.Sprint(i), "Description": strings.Repeat("r", 256)}
		}
		data.ImportJobs = append(data.ImportJobs, domain.ImportJob{ID: "test-import", UserID: data.Viewer.ID, Rows: rows, Status: "mapping", CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{store: repo, authDisabled: true, uploadPath: t.TempDir(), deliverySchedulerInterval: time.Hour}
	handler := newHandler(s)
	defer s.stopDeliveryScheduler(context.Background())
	// Materialize derived counts for the synthetic projects before measuring
	// steady-state imports; their copied read models initially describe no rows.
	requestJSON[domain.Issue](t, handler, "POST", "/api/issues", map[string]string{"title": "Initialize derived metadata"}, 201)
	create := measure("api_create_20_with_large_metadata", func() {
		for i := 0; i < 20; i++ {
			requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issues", map[string]any{"title": fmt.Sprintf("API import %d", i), "labelIds": []string{data.Issues[0].Labels[0].ID}}, 201)
		}
	})
	rowCreate := measure("row_api_create_20_with_large_metadata", func() {
		for i := 0; i < 20; i++ {
			requestJSON[domain.Issue](t, handler, http.MethodPost, "/api/issue-records", map[string]any{"title": fmt.Sprintf("Row API import %d", i), "labelIds": []string{data.Issues[0].Labels[0].ID}}, 201)
		}
	})
	csv := measure("csv_commit_2000", func() {
		requestJSON[domain.ImportJob](t, handler, "POST", "/api/imports/test-import/commit", map[string]any{"teamId": data.Teams[0].ID, "mapping": map[string]string{"title": "Title", "description": "Description"}}, 202)
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			metadata, _ := repo.WorkspaceMetadata(key)
			for _, job := range metadata.ImportJobs {
				if job.ID == "test-import" && job.Status == "completed" {
					if job.Imported != 2000 {
						t.Fatalf("imported %d rows", job.Imported)
					}
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("CSV import did not complete")
	})
	var raw json.RawMessage
	if err := db.QueryRow("SELECT data FROM workspace_states WHERE workspace_key=?", key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	t.Logf("BINLOG workspace_root_bytes=%d", len(raw))
	if os.Getenv("FLOW_ASSERT_WRITE_BOUNDS") == "1" {
		if replay != 0 {
			t.Errorf("identical retry wrote %d binlog bytes", replay)
		}
		if update > 32<<10 {
			t.Errorf("one title wrote %d binlog bytes", update)
		}
		if create > 512<<10 || rowCreate > 512<<10 {
			t.Errorf("create path rewrote unrelated collections: legacy=%d row=%d", create, rowCreate)
		}
		if csv > 8<<20 {
			t.Errorf("CSV import wrote %d bytes for 2000 rows", csv)
		}
		if len(raw) > 32<<10 {
			t.Errorf("large collections remain in root metadata: %d", len(raw))
		}
	}
	// Check that the compatibility reader still returns the complete import input.
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest("GET", "/api/bootstrap", nil))
	if r.Code != 200 {
		t.Fatal(r.Code)
	}
}
