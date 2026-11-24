package store

import (
	"context"
	"os"
	"strings"
)

func normalizeDatabaseDriver(driver string) string {
	switch driver = strings.ToLower(strings.TrimSpace(driver)); driver {
	case "":
		return "sqlite"
	case "postgresql":
		return "postgres"
	default:
		return driver
	}
}

func metadataFeatureSetting(setting, driver string) string {
	if value := strings.ToLower(strings.TrimSpace(os.Getenv(setting))); value != "" {
		return value
	}
	switch normalizeDatabaseDriver(driver) {
	case "mysql", "postgres":
		return "on"
	default:
		return "off"
	}
}

func metadataFeatureEnabled(setting, driver string) bool {
	switch metadataFeatureSetting(setting, driver) {
	case "on", "1", "true":
		return true
	default:
		return false
	}
}

func (s *SQLiteStore) databaseDriver() string {
	if s == nil {
		return ""
	}
	return s.dialect
}

type databaseFeatureDriverKey struct{}

// ContextWithDatabaseFeatures binds flag defaults for helpers that do not
// otherwise have a reference to the database serving the request.
func (s *SQLiteStore) ContextWithDatabaseFeatures(ctx context.Context) context.Context {
	driver := s.databaseDriver()
	current, _ := ctx.Value(databaseFeatureDriverKey{}).(string)
	if current == driver || current == "" && driver == "sqlite" {
		return ctx
	}
	return context.WithValue(ctx, databaseFeatureDriverKey{}, driver)
}

func recordHydrationEnabled(driver string) bool {
	return metadataFeatureEnabled("FLOW_RECORD_HYDRATION", driver)
}

// RecordHydrationEnabled reports whether helpers can use selected record
// inputs. A context without a database uses the SQLite default.
func RecordHydrationEnabled(ctx context.Context) bool {
	driver, _ := ctx.Value(databaseFeatureDriverKey{}).(string)
	return recordHydrationEnabled(driver)
}

// SyncV2Enabled reports the setting. Serving also requires journal coverage.
func (s *SQLiteStore) SyncV2Enabled() bool {
	return metadataFeatureEnabled("FLOW_SYNC_V2", s.databaseDriver())
}
