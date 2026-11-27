package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// The startup work runs with bounded metadata on exactly as it does
// with it off. A database seeded flag off (an OAuth application whose app
// user and installation carry a stale name and logo, and whose icon has no
// recorded owner) and then given a workspace created with bounded metadata
// on is copied twice; one copy opens flag off, the other flag on (the
// production bounded store: coverage required), and each runs the startup
// syncs main runs (SyncAllApplicationIdentities, BackfillUploadOwners; flag
// on under store.WithColdWork as main passes it). Before this change the
// bounded identity sync and icon backfill found no application (the bounded
// image does not hold the field) and did nothing. Both copies must end with
// the same rows in every table (timestamps and generated ids masked), the
// app user renamed and the icon owner recorded: the OpenDatabase startup
// stages and both syncs write the same thing in both modes.
func TestBoundedStartupMatchesLegacy(t *testing.T) {
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
	user, token, err := repo.Register(ctx, "SW557 owner", idgen.New("sw557-")+"@example.test", "sw557-fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	user.EmailVerified = true
	created, err := repo.CreateWorkspace(ContextWithActor(ctx, user), "SW557 legacy", "sw557-legacy", "us")
	if err != nil {
		t.Fatal(err)
	}
	icon := "oauth_app_icon_sw557.png"
	client := idgen.New("sw557-client-")
	if err := repo.MutateWorkspace(WithMetadataFields(ctx, "oauthApplications"), created.Workspace.URLKey, "scale_p21_sw557.application", "", nil, func(data *domain.Bootstrap) error {
		data.OAuthApplications = append(data.OAuthApplications, domain.OAuthApplication{
			ID: idgen.New("sw557-application-"), ClientID: client, Name: "Current identity", LogoURL: "/uploads/" + icon,
			CreatorID: user.ID, Scopes: []string{"read"},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	installed, err := repo.InstallApplication(ctx, created.Workspace.URLKey, domain.ApplicationInstallation{
		ClientID: client, Name: "Stale identity", AvatarURL: "https://example.test/stale.png",
		Scopes: []string{"read"}, TeamIDs: []string{created.Teams[0].ID}, InstalledBy: user.ID, Active: true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	// A bounded store starts once the metadata catalog covers every record.
	if err := completeCatalog(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	// A workspace created with bounded metadata on.
	repo = open(seed, true)
	if _, err := repo.CreateWorkspace(ContextWithActor(WithColdWork(ctx, "sw557"), user), "SW557 bounded", "sw557-bounded", "us"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	dumps := map[bool]string{}
	for _, bounded := range []bool{false, true} {
		path := filepath.Join(dir, fmt.Sprintf("copy-%v.db", bounded))
		copyDatabase(t, seed, path)
		repo := open(path, bounded)
		run := ctx
		if bounded {
			run = WithColdWork(ctx, "startup")
		}
		if err := repo.SyncAllApplicationIdentities(run); err != nil {
			t.Fatalf("bounded=%v identity sync: %v", bounded, err)
		}
		recorded, err := repo.BackfillUploadOwners(run)
		if err != nil || recorded != 1 {
			t.Fatalf("bounded=%v upload owner backfill recorded %d: %v", bounded, recorded, err)
		}
		var name, avatar string
		if err := repo.db.QueryRowContext(ctx, `SELECT name,avatar_url FROM auth_users WHERE id=?`, installed.UserID).Scan(&name, &avatar); err != nil || name != "Current identity" || avatar != "/uploads/"+icon {
			t.Fatalf("bounded=%v app user not synced: %q %q %v", bounded, name, avatar, err)
		}
		if err := repo.Close(); err != nil {
			t.Fatal(err)
		}
		dumps[bounded] = boundedStartupDump(t, path)
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
		t.Fatal("bounded startup wrote a different database than legacy startup")
	}
}

// completeCatalog runs the metadata catalog backfill over every
// collection to completion (what the load rig does before a bounded start).
func completeCatalog(ctx context.Context, s *SQLiteStore) error {
	cursor := metadataCollectionCursor{}
	for {
		var err error
		cursor, err = s.nextMetadataCatalogCollection(ctx, cursor)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		next, nextKind := cursor.Workspace, cursor.Field
		status, err := s.MetadataCatalogStatus(ctx, next, nextKind)
		if err != nil {
			return err
		}
		if status.Complete {
			if err := s.ResetMetadataCatalogBackfill(ctx, next, nextKind); err != nil {
				return err
			}
		}
		for {
			progress, err := s.BackfillMetadataCatalog(ctx, next, nextKind)
			if err != nil && !errors.Is(err, ErrConcurrentWrite) {
				return err
			}
			if err == nil && progress.Complete {
				break
			}
		}
	}
}

func copyDatabase(t *testing.T, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source, err := os.Open(from + suffix)
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.Create(to + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(target, source); err != nil {
			t.Fatal(err)
		}
		_ = source.Close()
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

var (
	boundedStartupTimes = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	boundedStartupIDs   = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[a-z0-9_-]+-[0-9a-f]{16,}|[a-z0-9]*_?\d{16,}_[a-z0-9]+|\b\d{13,}\b`)
)

// dump renders every row of every table, timestamps and
// generated ids masked, sorted.
func boundedStartupDump(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	var lines []string
	for _, table := range tables {
		query := `SELECT * FROM "` + table + `"`
		if table == "workspace_cache_revisions" {
			// The shard a write bumps is random: compare the sums.
			query = `SELECT workspace_key,kind,SUM(revision) FROM workspace_cache_revisions GROUP BY workspace_key,kind`
		}
		rows, err := db.QueryContext(context.Background(), query)
		if err != nil {
			continue // a virtual table without a readable shadow
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			cells := make([]string, len(values))
			for i, value := range values {
				if raw, ok := value.([]byte); ok {
					value = string(raw)
				}
				cells[i] = columns[i] + "=" + fmt.Sprint(value)
			}
			line := table + ": " + strings.Join(cells, " | ")
			line = boundedStartupTimes.ReplaceAllString(line, "<time>")
			line = boundedStartupIDs.ReplaceAllString(line, "<id>")
			lines = append(lines, line)
		}
		rows.Close()
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}
