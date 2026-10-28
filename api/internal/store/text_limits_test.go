package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// textLimitStores is a SQLite fixture and, when FLOW_TEST_DATABASE_DRIVER
// and FLOW_TEST_DATABASE_URL name a throwaway MySQL or Postgres database,
// that database too.
func textLimitStores(t *testing.T) map[string]*SQLiteStore {
	t.Helper()
	stores := map[string]*SQLiteStore{}
	sqlite, err := OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "limits.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlite.Close() })
	stores["sqlite"] = sqlite
	if driver, url := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && url != "" {
		external, err := OpenDatabase(DatabaseConfig{Driver: driver, URL: url, FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 4})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { external.Close() })
		stores[driver] = external
	}
	return stores
}

func TestStorableTextCutsOnCharacterBoundaries(t *testing.T) {
	for _, test := range []struct {
		in   string
		max  int
		want string
	}{
		{"héllo", 2, "h"}, // é is two bytes: never half of it
		{"日本語", 4, "日"},   // three-byte runes
		{"a\xffb", 10, "a�b"},
		{"nul\x00byte", 20, "nulbyte"},
		{"short", 10, "short"},
	} {
		got := storableText(test.in, test.max)
		if got != test.want || !utf8.ValidString(got) || len(got) > test.max {
			t.Errorf("storableText(%q, %d) = %q, want %q", test.in, test.max, got, test.want)
		}
	}
	if got := TruncateRunes("界界界", 2); got != "界界" {
		t.Errorf("TruncateRunes = %q", got)
	}
	long := strings.Repeat("x@example.com,", 40)
	key := eventAggregateKey(long)
	if len(key) > 191 || key == eventAggregateKey(long+"y") || eventAggregateKey("issue_1") != "issue_1" {
		t.Errorf("eventAggregateKey(%d bytes) = %q (%d bytes)", len(long), key, len(key))
	}
}

// A receiver's response that is not UTF-8, or is cut mid-character at the
// stored limit, is stored (strict MySQL used to refuse it with error 1366).
func TestWebhookDeliveryStoresNonUTF8Response(t *testing.T) {
	for name, repo := range textLimitStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC()
			id := fmt.Sprintf("delivery_limits_%d", now.UnixNano())
			delivery := domain.WebhookDelivery{ID: id, WorkspaceKey: "test-workspace", WebhookID: "webhook_limits", SecretRef: "secret_limits", Kind: "event", EventID: "event_" + id, EventType: "Issue", Action: "create", URL: "https://hooks.example.test/flow", Payload: []byte(`{}`), MaxAttempts: 1, CreatedAt: now}
			if _, err := repo.EnqueueWebhookDelivery(ctx, delivery); err != nil {
				t.Fatal(err)
			}
			claimed, ok, err := repo.ClaimWebhookDelivery(ctx, id, now.Add(time.Second), time.Minute)
			if err != nil || !ok {
				t.Fatalf("claim = %v %v", ok, err)
			}
			// 2047 ASCII bytes then a two-byte é: a byte cut at 2048 splits it.
			body := "\xff\xfe" + strings.Repeat("a", domain.WebhookDeliveryBodyLimit-3) + "é tail"
			outcome := WebhookDeliveryOutcome{ResponseStatus: 500, ResponseBody: body, Error: "receiver said \xc3\x28 and \x00", At: now.Add(2 * time.Second)}
			if _, err := repo.CompleteWebhookDeliveryAttempt(ctx, claimed, outcome); err != nil {
				t.Fatalf("complete with a non-UTF-8 response: %v", err)
			}
			stored, err := repo.WebhookDelivery(ctx, "test-workspace", id)
			if err != nil {
				t.Fatal(err)
			}
			if !utf8.ValidString(stored.ResponseBody) || len(stored.ResponseBody) > domain.WebhookDeliveryBodyLimit || !strings.HasPrefix(stored.ResponseBody, "�aaa") || !utf8.ValidString(stored.Error) || strings.ContainsRune(stored.Error, 0) {
				t.Fatalf("stored body %q (%d bytes) error %q", stored.ResponseBody[:16], len(stored.ResponseBody), stored.Error)
			}
		})
	}
}

// Search history is history, not content: a long query is kept cut to the
// column on a character boundary instead of failing the search.
func TestRecordSearchKeepsLongQueriesCut(t *testing.T) {
	for name, repo := range textLimitStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			data := repo.Bootstrap()
			query := strings.Repeat("界", domain.SearchQueryMaxLength+100) + "\xff"
			if err := repo.RecordSearch(ctx, data.Workspace.ID, data.Viewer.ID, query); err != nil {
				t.Fatalf("record a long query: %v", err)
			}
			history, err := repo.SearchHistory(ctx, data.Workspace.ID, data.Viewer.ID, 50)
			if err != nil {
				t.Fatal(err)
			}
			want := strings.Repeat("界", domain.SearchQueryMaxLength)
			found := false
			for _, entry := range history {
				found = found || entry.Query == want
			}
			if !found {
				t.Fatalf("history %v lacks the cut query", history)
			}
		})
	}
}

// On MySQL and Postgres the store refuses an over-long issue title with a
// typed 400 error before the write; the boundary length fits.
func TestIssueTitleColumnLimit(t *testing.T) {
	for name, repo := range textLimitStores(t) {
		t.Run(name, func(t *testing.T) {
			issue := domain.Issue{Title: strings.Repeat("界", domain.IssueTitleMaxLength)}
			if err := repo.issueRecordFits(issue); err != nil {
				t.Fatalf("boundary title refused: %v", err)
			}
			issue.Title += "界"
			err := repo.issueRecordFits(issue)
			if name == "sqlite" {
				if err != nil {
					t.Fatalf("SQLite refused a legacy long title: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "2048") {
				t.Fatalf("long title = %v", err)
			}
		})
	}
}

// The SCIM checks run before any write on every dialect.
func TestSCIMValueLimits(t *testing.T) {
	for name, repo := range textLimitStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			workspace := repo.Bootstrap().Workspace.ID
			suffix := fmt.Sprint(time.Now().UnixNano())
			exact := suffix + strings.Repeat("ü", domain.SCIMValueMaxLength-len(suffix))
			if _, err := repo.CreateSCIMUser(ctx, workspace, SCIMUserInput{UserName: exact + "x", Active: true}); err == nil || !strings.Contains(err.Error(), "userName must be 320") {
				t.Fatalf("long userName = %v", err)
			}
			if _, err := repo.CreateSCIMUser(ctx, workspace, SCIMUserInput{UserName: "limits-" + suffix, ExternalID: exact + "x", Active: true}); err == nil || !strings.Contains(err.Error(), "externalId") {
				t.Fatalf("long externalId = %v", err)
			}
			created, err := repo.CreateSCIMUser(ctx, workspace, SCIMUserInput{UserName: exact, ExternalID: exact, DisplayName: exact, Active: true})
			if err != nil {
				t.Fatalf("boundary userName refused: %v", err)
			}
			if created.UserName != exact {
				t.Fatalf("stored userName %q", created.UserName)
			}
			if _, err := repo.CreateSCIMGroup(ctx, workspace, "", exact+"x", ""); err == nil || !strings.Contains(err.Error(), "displayName") {
				t.Fatalf("long group displayName = %v", err)
			}
			if _, err := repo.CreateSCIMGroup(ctx, workspace, exact+"x", "Limits "+suffix, ""); err == nil || !strings.Contains(err.Error(), "externalId") {
				t.Fatalf("long group externalId = %v", err)
			}
			if _, err := repo.CreateSCIMGroup(ctx, workspace, exact, exact, ""); err != nil {
				t.Fatalf("boundary group refused: %v", err)
			}
			var users int
			if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_identities WHERE provider='scim' AND (username=? OR subject=?)`, exact+"x", exact+"x").Scan(&users); err != nil && err != sql.ErrNoRows {
				t.Fatal(err)
			}
			if users != 0 {
				t.Fatalf("a refused SCIM user was written (%d rows)", users)
			}
		})
	}
}

// Text with NUL that reaches the store without passing a JSON API body (a
// CSV cell, a query string, an external payload) is stored without it on
// PostgreSQL instead of failing with SQLSTATE 22021, and the JSON columns
// that SQL parses (issue descriptions) carry no \u0000 on any dialect.
func TestNULTextReachesTheStore(t *testing.T) {
	for name, repo := range textLimitStores(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			data := repo.Bootstrap()
			workspace := data.Workspace.URLKey
			issueID := data.Issues[0].ID
			suffix := fmt.Sprint(time.Now().UnixNano())
			if err := repo.MutateWorkspace(WithIssueRecordMutations(ctx, issueID), workspace, "test.nul_text", issueID, nil, func(next *domain.Bootstrap) error {
				for i := range next.Issues {
					if next.Issues[i].ID == issueID {
						next.Issues[i].Title = "Nul\x00 title " + suffix
						next.Issues[i].Description = "nul\x00 description"
						next.Issues[i].Version++
					}
				}
				return nil
			}); err != nil {
				t.Fatalf("write an issue with NUL: %v", err)
			}
			var title string
			var raw []byte
			if err := repo.db.QueryRowContext(ctx, `SELECT title,data FROM issue_records WHERE workspace_key=? AND id=?`, workspace, issueID).Scan(&title, &raw); err != nil {
				t.Fatal(err)
			}
			if title != "Nul title "+suffix || bytes.Contains(raw, []byte(`\u0000`)) {
				t.Fatalf("stored title %q, data has \\u0000: %v", title, bytes.Contains(raw, []byte(`\u0000`)))
			}
			// The description is read from the issue JSON in SQL.
			page, err := repo.QueryIssueRecords(ctx, IssueRecordQuery{Workspace: workspace, Summary: true, IncludeDescription: true, Limit: 500, Archived: "all"})
			if err != nil {
				t.Fatalf("query with descriptions: %v", err)
			}
			if len(page.Items) == 0 {
				t.Fatal("no issues")
			}
			if err := repo.RecordSearch(ctx, data.Workspace.ID, data.Viewer.ID, "nul\x00 query "+suffix); err != nil {
				t.Fatalf("record a search with NUL: %v", err)
			}
			// A lookup by a value with NUL is a miss, not an error.
			var count int
			if err := repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_records WHERE workspace_key=? AND title=?`, workspace, "no\x00such"+suffix).Scan(&count); err != nil || count != 0 {
				t.Fatalf("lookup with NUL = %d, %v", count, err)
			}
			if _, err := repo.CreateSCIMUser(ctx, data.Workspace.ID, SCIMUserInput{UserName: "nul\x00-" + suffix, DisplayName: "Nul\x00 Person", Active: true}); err != nil {
				t.Fatalf("SCIM user with NUL: %v", err)
			}
		})
	}
}

func TestStripJSONNUL(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{`{"a":"x\u0000y"}`, `{"a":"xy"}`},
		{`{"a":"\\u0000"}`, `{"a":"\\u0000"}`},
		{`{"a":"\\\u0000","b":"\"\u0000"}`, `{"a":"\\","b":"\""}`},
		{`{"a":"plain"}`, `{"a":"plain"}`},
	} {
		if got := string(StripJSONNUL([]byte(test.in))); got != test.want {
			t.Errorf("StripJSONNUL(%s) = %s, want %s", test.in, got, test.want)
		}
	}
	if args := textArgs("postgres", []any{"a\x00b", []byte("c\x00d"), 3}); args[0] != "ab" || string(args[1].([]byte)) != "c\x00d" {
		t.Errorf("textArgs = %q", args)
	}
	if args := textArgs("mysql", []any{"a\x00b"}); args[0] != "a\x00b" {
		t.Errorf("textArgs on MySQL = %q", args)
	}
}
