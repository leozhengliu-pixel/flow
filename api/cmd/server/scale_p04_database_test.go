package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"

	"github.com/go-sql-driver/mysql"
)

type scaleP04Database struct {
	repo             *store.SQLiteStore
	config           store.DatabaseConfig
	key, workspaceID string
	reader           *sql.DB
}

func newScaleP04Database(t *testing.T) *scaleP04Database {
	t.Helper()
	fixtureRepo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtureRepo.Bootstrap()
	fixtureRepo.Close()
	config := store.DatabaseConfig{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "flow.db"), FixtureProfile: "test", FixturePassword: "test-password", MaxOpenConns: 1}
	if driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL"); driver != "" && databaseURL != "" {
		config.Driver, config.URL, config.MaxOpenConns = driver, databaseURL, 4
	}
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	database := &scaleP04Database{repo: repo, config: config, key: idgen.New("p04-")}
	ctx := store.ContextWithActor(context.Background(), fixture.Viewer)
	created, err := repo.CreateWorkspaceWith(ctx, "P04 differential", database.key, "us", func(data *domain.Bootstrap) error {
		workspace := data.Workspace
		*data = fixture
		data.Workspace = workspace
		return nil
	})
	if err != nil {
		repo.Close()
		t.Fatal(err)
	}
	database.workspaceID = created.Workspace.ID
	for _, user := range fixture.Users {
		if err := repo.EnsureWorkspaceMembership(ctx, created.Workspace.ID, user.ID); err != nil {
			t.Fatal(err)
		}
		role := "member"
		if user.ID == fixture.Viewer.ID {
			role = "owner"
		}
		if err := repo.SetTeamMembership(ctx, created.Workspace.ID, fixture.Teams[0].ID, user.ID, role, true); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if database.reader != nil {
			database.reader.Close()
		}
		_ = database.repo.DeleteWorkspace(context.Background(), database.key)
		database.repo.Close()
	})
	return database
}

func (database *scaleP04Database) handler(t *testing.T, authDisabled bool) http.Handler {
	handler := newHandler(&server{store: database.repo, authDisabled: authDisabled, uploadPath: t.TempDir()})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("X-Workspace-Key"); key == "" || key == "test-workspace" {
			r.Header.Set("X-Workspace-Key", database.key)
		}
		r.URL.Path = strings.Replace(r.URL.Path, "/api/workspaces/test-workspace", "/api/workspaces/"+database.key, 1)
		handler.ServeHTTP(w, r)
	})
}

func (database *scaleP04Database) reopen(t *testing.T) {
	t.Helper()
	if err := database.repo.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := store.OpenDatabase(database.config)
	if err != nil {
		t.Fatal(err)
	}
	database.repo = repo
}

func (database *scaleP04Database) query(t *testing.T, query string, arg any) *sql.Rows {
	t.Helper()
	if database.reader == nil {
		driver, dsn := database.config.Driver, database.config.URL
		switch driver {
		case "sqlite":
			dsn = database.config.Path
		case "postgres", "postgresql":
			driver = "pgx"
		case "mysql":
			if strings.Contains(dsn, "://") {
				parsed, err := url.Parse(dsn)
				if err != nil {
					t.Fatal("invalid test database URL")
				}
				config := mysql.NewConfig()
				config.User = parsed.User.Username()
				config.Passwd, _ = parsed.User.Password()
				config.Net, config.Addr, config.DBName = "tcp", parsed.Host, strings.TrimPrefix(parsed.Path, "/")
				dsn = config.FormatDSN()
			}
		}
		reader, err := sql.Open(driver, dsn)
		if err != nil {
			t.Fatal(err)
		}
		database.reader = reader
	}
	if database.config.Driver == "postgres" || database.config.Driver == "postgresql" {
		query = strings.Replace(query, "?", "$1", 1)
	}
	rows, err := database.reader.Query(query, arg)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
