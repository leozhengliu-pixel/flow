package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

func scaleP21SW17CodingRows(t *testing.T, repo *SQLiteStore, workspace string) []any {
	t.Helper()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT id,name,code_host,host_name,repository,active_repo_key,archived_at,version,data,created_at,updated_at FROM coding_environments WHERE workspace_key=? ORDER BY id`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := []any{}
	for rows.Next() {
		var id, name, codeHost, host, repository string
		var active sql.NullString
		var archived, version, created, updated int64
		var raw []byte
		if err := rows.Scan(&id, &name, &codeHost, &host, &repository, &active, &archived, &version, &raw, &created, &updated); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{id, name, codeHost, host, repository, active, archived, version, string(raw), created, updated})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func scaleP21SW17CodingSettings(t *testing.T, repo *SQLiteStore, workspace string) []byte {
	t.Helper()
	var raw []byte
	if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='codingAgentSettings'`, workspace).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func scaleP21SW17CodingUnrelated(t *testing.T, repo *SQLiteStore, workspace string) [32]byte {
	t.Helper()
	hash := sha256.New()
	rows, err := repo.db.QueryContext(t.Context(), `SELECT field,record_key,collection_order,data FROM workspace_metadata_records WHERE workspace_key=? AND NOT (field='workspaceSettings' AND record_key='codingAgentSettings') ORDER BY field,record_key`, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var field, key string
		var order int64
		var raw []byte
		if err := rows.Scan(&field, &key, &order, &raw); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(hash, "%s:%s:%d:", field, key, order)
		_, _ = hash.Write(raw)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return [32]byte(hash.Sum(nil))
}

func scaleP21SW17SeedCoding(t *testing.T, repo *SQLiteStore, workspace string, legacy []domain.CodingAgentEnvironment) {
	t.Helper()
	if _, err := repo.db.ExecContext(t.Context(), `DELETE FROM coding_environments WHERE workspace_key=?`, workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.ReloadWorkspace(t.Context(), workspace); err != nil {
		t.Fatal(err)
	}
	if err := repo.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw17.coding_seed", "", nil, func(data *domain.Bootstrap) error {
		data.WorkspaceSettings.UpdatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		data.WorkspaceSettings.CodingAgentSettings = domain.CodingAgentSettings{
			CommitSigningEnabled: true, Harness: "claudeCode", Model: "before", Environments: slices.Clone(legacy),
			ReasoningEffort: "high", TrustedExternalSources: []string{"github:original", "email"}, LoopRepositoryAccess: "selected", LoopRepositories: []string{"retained-reference"},
		}
		data.WorkspaceSettings.WelcomeMessage = "Retained policy beside coding settings"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestScaleP21SW17CodingMigrationDifferential(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw17-env-")
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	legacy := []domain.CodingAgentEnvironment{
		{ID: prefix + "-large", Name: "Escaped \u4e2d\u6587 script", Repository: "acme/" + prefix + "-large", SetupCommand: strings.Repeat("\x01", 55<<10)},
		{ID: prefix + "-duplicate", Name: "Duplicate repository", Repository: "acme/" + prefix + "-large"},
		{ID: prefix + "-archived", Name: "Archived", Repository: "git@gitlab.example.test:group/" + prefix + "-lab.git", Archived: true},
		{ID: prefix + "-invalid", Name: "Broken", Repository: "not a repository"},
		{ID: prefix + "-existing", Name: "Must not replace existing", Repository: "acme/" + prefix + "-ignored"},
		{Name: "", Repository: "acme/" + prefix + "-generated", SetupCommand: "echo before\r\necho after"},
		{ID: strings.Repeat("long-imported-id-", 20), Name: "Long ID fallback", Repository: "acme/" + prefix + "-long-id"},
	}
	for index := range 65 {
		legacy = append(legacy, domain.CodingAgentEnvironment{ID: fmt.Sprintf("%s-%03d", prefix, index), Name: "Retained", Repository: fmt.Sprintf("acme/%s-%03d", prefix, index)})
	}
	existing, valid := legacyCodingEnvironment(workspace, 0, domain.CodingAgentEnvironment{ID: prefix + "-existing", Name: "Preexisting native", Repository: "acme/" + prefix + "-preexisting"}, time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC))
	if !valid {
		t.Fatal("invalid existing fixture")
	}
	type outcome struct {
		Count    int
		Rows     []any
		Settings string
	}
	var outcomes []outcome
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		scaleP21SW17SeedCoding(t, f.SQLiteStore, workspace, legacy)
		if len(scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)) <= MetadataRecordPageMaxBytes {
			t.Fatal("fixture must exceed a selected metadata page")
		}
		if err := f.insertCodingEnvironment(t.Context(), f.db, existing); err != nil {
			t.Fatal(err)
		}
		before := scaleP21SW17CodingUnrelated(t, f.SQLiteStore, workspace)
		published := 0
		source.SetWebhookSink(func(string, domain.DomainEvent) { published++ })
		source.SetRealtimeSink(func(string, domain.RealtimeEvent) { published++ })
		var eventCount int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&eventCount); err != nil {
			t.Fatal(err)
		}
		count, err := source.migrateWorkspaceCodingEnvironments(t.Context(), workspace)
		if err != nil || count != len(legacy)-2 {
			t.Fatalf("migrated=%d want=%d err=%v", count, len(legacy)-2, err)
		}
		rows := scaleP21SW17CodingRows(t, f.SQLiteStore, workspace)
		settings := scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)
		large, err := f.CodingEnvironment(t.Context(), workspace, prefix+"-large")
		if err != nil || large.PrepareScript != legacy[0].SetupCommand || large.Name != legacy[0].Name {
			t.Fatal("migration truncated or changed a legal large escaped script", err)
		}
		var decoded domain.CodingAgentSettings
		if err := json.Unmarshal(settings, &decoded); err != nil || len(decoded.Environments) != 0 || decoded.Model != "before" || !decoded.CommitSigningEnabled {
			t.Fatal("migration cleared other coding settings", err)
		}
		var afterEvents int
		if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM domain_events WHERE workspace_id=?`, f.data.Workspace.ID).Scan(&afterEvents); err != nil || afterEvents != eventCount || published != 0 {
			t.Fatal("migration added a domain event or publication", err)
		}
		if scaleP21SW17CodingUnrelated(t, f.SQLiteStore, workspace) != before {
			t.Fatal("coding migration rewrote unrelated canonical metadata")
		}
		if count, err := source.migrateWorkspaceCodingEnvironments(t.Context(), workspace); err != nil || count != 0 || !reflect.DeepEqual(rows, scaleP21SW17CodingRows(t, f.SQLiteStore, workspace)) || !bytes.Equal(settings, scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)) {
			t.Fatal("migration is not idempotent", err)
		}
		if source.BoundedMetadataEnabled() {
			policy, ok := source.WorkspaceSettingsMetadata(workspace)
			source.mu.RLock()
			resident := source.workspaces[workspace]
			source.mu.RUnlock()
			if !ok || policy.WorkspaceSettings.CodingAgentSettings.Model != "before" || policy.WorkspaceSettings.WelcomeMessage != "Retained policy beside coding settings" || len(resident.Teams) != len(f.data.Teams) {
				t.Fatal("migration installed an incomplete resident policy")
			}
			if source.WorkspaceMetadataDecodes() != 0 || source.metadataResidency.work.used != 0 {
				t.Fatal("coding migration decoded complete metadata or leaked admission")
			}
		}
		outcomes = append(outcomes, outcome{Count: count, Rows: rows, Settings: string(settings)})
		source.SetWebhookSink(nil)
		source.SetRealtimeSink(nil)
	}
	if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
		t.Fatal("native row data, indexes, order or remaining settings differ between migration modes")
	}
}

func TestScaleP21SW17CodingMigrationZeroCreatedAt(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw17-env-zero-")
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	legacy := []domain.CodingAgentEnvironment{
		{ID: prefix + "-first", Repository: "acme/" + prefix},
		{ID: prefix + "-duplicate", Repository: "acme/" + prefix},
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		scaleP21SW17SeedCoding(t, f.SQLiteStore, workspace, legacy)
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw17.zero_time", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.UpdatedAt = time.Time{}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		before := time.Now().UTC().Truncate(time.Millisecond)
		count, err := source.migrateWorkspaceCodingEnvironments(t.Context(), workspace)
		after := time.Now().UTC().Add(time.Millisecond)
		if err != nil || count != 2 {
			t.Fatalf("zero-time migration count=%d err=%v", count, err)
		}
		first, err := f.CodingEnvironment(t.Context(), workspace, legacy[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		second, err := f.CodingEnvironment(t.Context(), workspace, legacy[1].ID)
		if err != nil || first.CreatedAt.Before(before) || first.CreatedAt.After(after) ||
			!first.UpdatedAt.Equal(first.CreatedAt) || !second.CreatedAt.Equal(first.CreatedAt) ||
			second.ArchivedAt == nil || second.ArchivedAt.UnixMilli() != first.CreatedAt.UnixMilli() {
			t.Fatal("zero-time fallback changed shared creation or archive timestamps", err)
		}
	}
}

func TestScaleP21SW17CodingMigrationFailures(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw17-env-failure-")
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	legacy := []domain.CodingAgentEnvironment{}
	for index := range 4 {
		legacy = append(legacy, domain.CodingAgentEnvironment{ID: fmt.Sprintf("%s-%d", prefix, index), Name: "Failure fixture", Repository: fmt.Sprintf("acme/%s-%d", prefix, index)})
	}
	for _, phase := range []string{"native-insert", "metadata-write"} {
		t.Run(phase, func(t *testing.T) {
			var outcomes []any
			for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
				scaleP21SW17SeedCoding(t, f.SQLiteStore, workspace, legacy)
				original := scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)
				cancelled, cancel := context.WithCancel(t.Context())
				inserts := 0
				ctx := WithSQLStatementObserver(cancelled, func(query string) {
					if strings.HasPrefix(query, "INSERT INTO coding_environments(") {
						inserts++
						if phase == "native-insert" && inserts == 3 {
							cancel()
						}
					}
					if phase == "metadata-write" && strings.HasPrefix(query, "INSERT INTO workspace_metadata_records(") {
						cancel()
					}
				})
				count, err := source.migrateWorkspaceCodingEnvironments(ctx, workspace)
				cancel()
				want := 2
				if phase == "metadata-write" {
					want = len(legacy)
				}
				rows := scaleP21SW17CodingRows(t, f.SQLiteStore, workspace)
				if !errors.Is(err, context.Canceled) || count != want || len(rows) != want || !bytes.Equal(original, scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)) {
					t.Fatalf("partial migration count=%d rows=%d want=%d err=%v", count, len(rows), want, err)
				}
				outcomes = append(outcomes, []any{count, rows})
				if remaining, err := source.migrateWorkspaceCodingEnvironments(t.Context(), workspace); err != nil || remaining != len(legacy)-want {
					t.Fatalf("retry count=%d err=%v", remaining, err)
				}
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("late failure changed partial native inserts or their indexes")
			}
		})
	}
	t.Run("spool-budget", func(t *testing.T) {
		scaleP21SW17SeedCoding(t, f.SQLiteStore, workspace, legacy)
		before := scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)
		used := recordSpoolBytes.Load()
		t.Setenv("FLOW_RECORD_SPOOL_MAX_BYTES", "0")
		if count, err := view.migrateWorkspaceCodingEnvironments(t.Context(), workspace); count != 0 || !errors.Is(err, ErrRecordSpoolBudget) || len(scaleP21SW17CodingRows(t, f.SQLiteStore, workspace)) != 0 || !bytes.Equal(before, scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)) || recordSpoolBytes.Load() != used {
			t.Fatalf("spool failure mutated metadata or leaked disk budget: count=%d err=%v", count, err)
		}
	})
	t.Run("policy-candidate-budget", func(t *testing.T) {
		scaleP21SW17SeedCoding(t, f.SQLiteStore, workspace, legacy)
		before := scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)
		limit := view.metadataResidency.policyLimit
		view.metadataResidency.policyLimit = 1
		count, err := view.migrateWorkspaceCodingEnvironments(t.Context(), workspace)
		view.metadataResidency.policyLimit = limit
		if count != len(legacy) || !errors.Is(err, ErrMetadataWorkBudget) || !bytes.Equal(before, scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)) || len(scaleP21SW17CodingRows(t, f.SQLiteStore, workspace)) != len(legacy) {
			t.Fatalf("candidate failure did not roll back only metadata: count=%d err=%v", count, err)
		}
		if count, err := view.migrateWorkspaceCodingEnvironments(t.Context(), workspace); count != 0 || err != nil {
			t.Fatal("candidate failure did not recover without duplicate native inserts", err)
		}
	})
	t.Run("cancelled-and-missing", func(t *testing.T) {
		scaleP21SW17SeedCoding(t, f.SQLiteStore, workspace, legacy)
		before := scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if count, err := view.migrateWorkspaceCodingEnvironments(ctx, workspace); count != 0 || !errors.Is(err, context.Canceled) || !bytes.Equal(before, scaleP21SW17CodingSettings(t, f.SQLiteStore, workspace)) {
			t.Fatalf("cancelled migration count=%d err=%v", count, err)
		}
		if count, err := view.migrateWorkspaceCodingEnvironments(t.Context(), idgen.New("sw17-missing-")); count != 0 || err != nil {
			t.Fatal("missing workspace changed no-op semantics", err)
		}
	})
	if view.metadataResidency.work.used != 0 || view.WorkspaceMetadataDecodes() != 0 {
		t.Fatal("failed coding migration decoded complete metadata or leaked admission")
	}
}

func TestScaleP21SW17CodingMigrationConcurrentMetadata(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	repo, config := scaleP04OwnedStore(t)
	repo.mu.RLock()
	workspace := repo.lastWorkspaceKey
	repo.mu.RUnlock()
	view, err := repo.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := OpenDatabase(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	prefix := idgen.New("sw17-env-peer-")
	legacy := []domain.CodingAgentEnvironment{}
	for index := range 3 {
		legacy = append(legacy, domain.CodingAgentEnvironment{ID: fmt.Sprintf("%s-%d", prefix, index), Name: "Peer fixture", Repository: fmt.Sprintf("acme/%s-%d", prefix, index), SetupCommand: "echo before"})
	}
	for _, phase := range []string{"addition", "edit", "delete", "delete-all", "model-only", "duplicate-addition", "unidentified-addition"} {
		t.Run(phase, func(t *testing.T) {
			var outcomes []any
			for _, source := range []*SQLiteStore{repo, view} {
				scaleP21SW17SeedCoding(t, repo, workspace, legacy)
				changed := false
				ctx := WithSQLStatementObserver(t.Context(), func(query string) {
					if changed || !strings.HasPrefix(query, "INSERT INTO coding_environments(") {
						return
					}
					changed = true
					if err := peer.ReloadWorkspace(t.Context(), workspace); err != nil {
						t.Fatal(err)
					}
					if err := peer.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw17.peer_change", "", nil, func(data *domain.Bootstrap) error {
						settings := &data.WorkspaceSettings.CodingAgentSettings
						settings.Model = "peer-model"
						data.WorkspaceSettings.WelcomeMessage = "Peer changed another settings property"
						addition := domain.CodingAgentEnvironment{ID: prefix + "-added", Name: "Concurrent addition", Repository: "acme/" + prefix + "-added"}
						switch phase {
						case "addition", "duplicate-addition", "unidentified-addition":
							if phase == "unidentified-addition" {
								addition.ID = ""
							}
							settings.Environments = append(settings.Environments, addition)
							if phase == "duplicate-addition" {
								addition.Name = "Last duplicate"
								settings.Environments = append(settings.Environments, addition)
							}
						case "edit":
							settings.Environments[1].SetupCommand = "peer edited a removed environment"
						case "delete":
							settings.Environments = slices.Delete(settings.Environments, 1, 2)
						case "delete-all":
							settings.Environments = nil
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				})
				count, err := source.migrateWorkspaceCodingEnvironments(ctx, workspace)
				conflict := errors.Is(err, ErrConcurrentWrite)
				if count != len(legacy) || err != nil {
					t.Fatalf("peer migration count=%d conflict=%t err=%v", count, conflict, err)
				}
				settings := scaleP21SW17CodingSettings(t, repo, workspace)
				var decoded domain.CodingAgentSettings
				if err := json.Unmarshal(settings, &decoded); err != nil {
					t.Fatal(err)
				}
				// WorkspaceSettings is not a rebaseable collection. The legacy
				// migration replaces this changed property from its original
				// snapshot, even when a peer edits/adds legacy environments.
				if decoded.Model != "before" || len(decoded.Environments) != 0 {
					t.Fatal("migration changed existing last-writer coding-settings behavior")
				}
				var welcome []byte
				if err := repo.db.QueryRowContext(t.Context(), `SELECT data FROM workspace_metadata_records WHERE workspace_key=? AND field='workspaceSettings' AND record_key='welcomeMessage'`, workspace).Scan(&welcome); err != nil || string(welcome) != `"Peer changed another settings property"` {
					t.Fatal("migration overwrote an unchanged settings property", err)
				}
				outcomes = append(outcomes, []any{count, conflict, scaleP21SW17CodingRows(t, repo, workspace), string(settings), string(welcome)})
			}
			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Fatal("two-store concurrent metadata effects differ")
			}
		})
	}
}

func TestScaleP21SW17CodingMigrationMeasurement(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "off")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	f := scaleP09Database(t)
	workspace, prefix := f.data.Workspace.URLKey, idgen.New("sw17-env-measure-")
	view, err := f.BoundedMetadataViewForTesting(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []*SQLiteStore{f.SQLiteStore, view} {
		if _, err := f.db.ExecContext(t.Context(), `DELETE FROM coding_environments WHERE workspace_key=?`, workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.ReloadWorkspace(t.Context(), workspace); err != nil {
			t.Fatal(err)
		}
		if err := f.MutateWorkspace(WithMetadataFields(t.Context(), "workspaceSettings"), workspace, "scale_p21_sw17.legacy_environments", "", nil, func(data *domain.Bootstrap) error {
			data.WorkspaceSettings.UpdatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			data.WorkspaceSettings.CodingAgentSettings.Environments = nil
			for index := range 120 {
				data.WorkspaceSettings.CodingAgentSettings.Environments = append(data.WorkspaceSettings.CodingAgentSettings.Environments, domain.CodingAgentEnvironment{
					ID: fmt.Sprintf("%s-%03d", prefix, index), Name: "Legacy environment",
					Repository: fmt.Sprintf("acme/sw17-environment-%03d", index), SetupCommand: strings.Repeat("echo setup\n", 1000),
				})
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		migrated, err := source.migrateWorkspaceCodingEnvironments(t.Context(), workspace)
		runtime.ReadMemStats(&after)
		if err != nil || migrated != 120 {
			t.Fatalf("measurement migrated=%d err=%v", migrated, err)
		}
		t.Logf("bounded=%t migration 120 rows bytes/op=%d allocs/op=%d", source.BoundedMetadataEnabled(), after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs)
	}
}
