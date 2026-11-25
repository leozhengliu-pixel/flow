package store

import (
	"os"
	"testing"
)

func TestMetadataFeatureDefaultsByDatabaseDriver(t *testing.T) {
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	t.Setenv("FLOW_METADATA_CATALOG", "")
	// In-process callers can select a driver independent of the environment.
	t.Setenv("FLOW_DATABASE_DRIVER", "sqlite")
	for _, tc := range []struct {
		name, driver string
		defaultOn    bool
	}{
		{"implicit SQLite", "", false},
		{"SQLite", "sqlite", false},
		{"MySQL", "mysql", true},
		{"PostgreSQL", "postgres", true},
		{"PostgreSQL alias", " PostgreSQL ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"unset", "", "on", "off"} {
				for _, flag := range []string{"FLOW_BOUNDED_METADATA", "FLOW_RECORD_HYDRATION", "FLOW_RECORD_JOURNAL", "FLOW_SYNC_V2"} {
					t.Setenv(flag, mode)
					if mode == "unset" {
						if err := os.Unsetenv(flag); err != nil {
							t.Fatal(err)
						}
					}
				}
				want := mode == "on" || (mode == "" || mode == "unset") && tc.defaultOn
				repo := &SQLiteStore{dialect: normalizeDatabaseDriver(tc.driver)}
				ctx := repo.ContextWithDatabaseFeatures(t.Context())
				bounded := boundedMetadataRequested(tc.driver)
				for flag, got := range map[string]bool{
					"FLOW_BOUNDED_METADATA": bounded,
					"FLOW_RECORD_HYDRATION": RecordHydrationEnabled(ctx),
					"FLOW_RECORD_JOURNAL":   repo.RecordJournalEnabled(),
					"FLOW_SYNC_V2":          repo.SyncV2Enabled(),
					"FLOW_METADATA_CATALOG": metadataCatalogAutoEnabled(bounded),
				} {
					if got != want {
						t.Errorf("%s with setting %q = %t, want %t", flag, mode, got, want)
					}
				}
			}
			t.Setenv("FLOW_METADATA_CATALOG", "off")
			if metadataCatalogAutoEnabled(true) {
				t.Error("explicit catalog opt-out ignored with bounded metadata on")
			}
			t.Setenv("FLOW_METADATA_CATALOG", "on")
			if !metadataCatalogAutoEnabled(false) {
				t.Error("explicit catalog opt-in ignored with bounded metadata off")
			}
		})
	}
}

func TestImplicitBoundedMetadataRespectsDisabledDependencies(t *testing.T) {
	t.Setenv("FLOW_BOUNDED_METADATA", "")
	t.Setenv("FLOW_RECORD_HYDRATION", "on")
	t.Setenv("FLOW_RECORD_JOURNAL", "on")
	t.Setenv("FLOW_RECORD_PEER_SYNC", "")
	t.Setenv("FLOW_METADATA_CATALOG", "")
	if boundedMetadataRequested("sqlite") {
		t.Fatal("enabling dependencies alone must not enable bounded metadata on SQLite")
	}
	for flag, mode := range map[string]string{
		"FLOW_RECORD_HYDRATION": "off",
		"FLOW_RECORD_JOURNAL":   "off",
		"FLOW_RECORD_PEER_SYNC": "shadow",
		"FLOW_METADATA_CATALOG": "shadow",
	} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv(flag, mode)
			if boundedMetadataRequested("mysql") || boundedMetadataRequested("postgres") {
				t.Fatal("implicit bounded metadata ignored an explicit dependency setting")
			}
		})
	}
}
