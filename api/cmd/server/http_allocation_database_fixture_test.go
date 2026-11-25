package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	users            []domain.User
	workspaces       []string
	ownsRepo         bool
}

const scaleP04Password = "p04-fixture-password"

func scaleP04DatabaseConfig(t *testing.T) store.DatabaseConfig {
	t.Helper()
	driver, databaseURL := os.Getenv("FLOW_TEST_DATABASE_DRIVER"), os.Getenv("FLOW_TEST_DATABASE_URL")
	if (driver == "") != (databaseURL == "") {
		t.Fatal("P04 tests require both FLOW_TEST_DATABASE_DRIVER and FLOW_TEST_DATABASE_URL")
	}
	config := store.DatabaseConfig{Driver: driver, URL: databaseURL, MaxOpenConns: 4}
	switch driver {
	case "":
		config.Driver, config.Path, config.MaxOpenConns = "sqlite", filepath.Join(t.TempDir(), "flow.db"), 1
	case "sqlite":
		config.Path, config.MaxOpenConns = databaseURL, 1
	case "mysql", "postgres", "postgresql":
	default:
		t.Fatalf("unsupported P04 test driver %q", driver)
	}
	return config
}

func newScaleP04Database(t *testing.T) *scaleP04Database {
	t.Helper()
	config := scaleP04DatabaseConfig(t)
	repo, err := store.OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	return newScaleP04DatabaseForStore(t, repo, config, true)
}

func newScaleP04DatabaseForStore(t *testing.T, repo *store.SQLiteStore, config store.DatabaseConfig, ownsRepo bool) *scaleP04Database {
	t.Helper()
	database := &scaleP04Database{repo: repo, config: config, key: idgen.New("p04-"), ownsRepo: ownsRepo}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if database.reader != nil {
			_ = database.reader.Close()
		}
		for i := len(database.workspaces) - 1; i >= 0; i-- {
			if err := database.repo.DeleteWorkspace(ctx, database.workspaces[i]); err != nil {
				t.Errorf("clean up owned workspace: %v", err)
			}
		}
		for _, user := range database.users {
			if err := database.repo.EraseAccount(ctx, user.ID); err != nil {
				t.Errorf("clean up owned account: %v", err)
			}
		}
		if database.ownsRepo {
			_ = database.repo.Close()
		}
	})
	fixtureRepo, err := store.OpenSQLiteTestFixture(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtureRepo.Bootstrap()
	fixtureRepo.Close()
	var replacements []string
	for _, original := range fixture.Users {
		user := database.register(t, original.Name)
		replacements = append(replacements, original.ID, user.ID, original.Email, user.Email)
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.NewReplacer(replacements...).Replace(string(raw))), &fixture); err != nil {
		t.Fatal(err)
	}
	ctx := store.ContextWithActor(context.Background(), fixture.Viewer)
	created, err := repo.CreateWorkspaceWith(ctx, "P04 differential", database.key, "us", func(data *domain.Bootstrap) error {
		workspace := data.Workspace
		*data = fixture
		data.Workspace = workspace
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	database.workspaceID = created.Workspace.ID
	database.workspaces = append(database.workspaces, database.key)
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
	return database
}

func (database *scaleP04Database) register(t *testing.T, name string) domain.User {
	t.Helper()
	user, token, err := database.repo.Register(t.Context(), name, idgen.New("p04-user-")+"@example.test", scaleP04Password)
	if err != nil {
		t.Fatal(err)
	}
	database.users = append(database.users, user)
	if err := database.repo.VerifyEmail(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	database.users[len(database.users)-1] = user
	return user
}

func (database *scaleP04Database) login(t *testing.T, baseURL string, user domain.User) *http.Client {
	t.Helper()
	client := authClient(t)
	authRequest[domain.AuthSession](t, client, http.MethodPost, baseURL+"/api/auth/login", map[string]string{"email": user.Email, "password": scaleP04Password}, "", http.StatusOK)
	return client
}

func (database *scaleP04Database) join(t *testing.T, baseURL string, admin *http.Client, name, role string, teams ...string) (*http.Client, domain.User) {
	t.Helper()
	user := database.register(t, name)
	client := database.login(t, baseURL, user)
	invites := authRequest[[]domain.Invitation](t, admin, http.MethodPost, baseURL+"/api/workspaces/"+database.key+"/invitations", map[string]any{"emails": []string{user.Email}, "role": role, "teamIds": teams}, "", http.StatusCreated)
	authRequest[domain.WorkspaceMembership](t, client, http.MethodPost, baseURL+"/api/invitations/accept", map[string]string{"token": invites[0].Token}, "", http.StatusOK)
	return client, user
}

func (database *scaleP04Database) handler(t *testing.T, authDisabled bool) http.Handler {
	// In-process script requests still use a real owned session. The global
	// development viewer can belong to an earlier shared-database fixture.
	var ownerToken string
	if authDisabled {
		_, token, err := database.repo.Login(t.Context(), database.users[0].Email, scaleP04Password)
		if err != nil {
			t.Fatal(err)
		}
		ownerToken = token
	}
	service := &server{store: database.repo, uploadPath: t.TempDir()}
	handler := newHandler(service)
	if err := service.stopDeliveryScheduler(t.Context()); err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ownerToken != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: ownerToken})
		}
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
