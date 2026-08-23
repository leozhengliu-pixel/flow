package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsAndBackendValidation(t *testing.T) {
	for _, key := range []string{"FLOW_DATABASE_DRIVER", "FLOW_DATABASE_URL", "FLOW_STORAGE_DRIVER", "FLOW_S3_BUCKET", "FLOW_S3_REGION", "FLOW_AUTH_GOOGLE_ENABLED", "FLOW_AUTH_OIDC_ENABLED", "FLOW_AUTH_SAML_ENABLED", "FLOW_TELEMETRY_ENABLED", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(key, "")
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Database.Driver != "sqlite" || loaded.Storage.Driver != "local" || loaded.Telemetry.Enabled {
		t.Fatalf("unexpected defaults: %#v", loaded)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", "postgres")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_DATABASE_URL") {
		t.Fatalf("postgres without URL error = %v", err)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOW_STORAGE_DRIVER", "s3")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_S3_BUCKET") {
		t.Fatalf("S3 without bucket error = %v", err)
	}
}

func TestLoadAuthAndTelemetryValidation(t *testing.T) {
	t.Setenv("FLOW_AUTH_GOOGLE_ENABLED", "true")
	t.Setenv("FLOW_GOOGLE_CLIENT_ID", "")
	t.Setenv("FLOW_GOOGLE_CLIENT_SECRET", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_GOOGLE_CLIENT_ID") {
		t.Fatalf("Google validation error = %v", err)
	}
	t.Setenv("FLOW_AUTH_GOOGLE_ENABLED", "false")
	t.Setenv("FLOW_TELEMETRY_ENABLED", "true")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Fatalf("telemetry validation error = %v", err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	loaded, err := Load()
	if err != nil || !loaded.Telemetry.Enabled || loaded.Telemetry.Endpoint == "" {
		t.Fatalf("telemetry config = %#v, %v", loaded.Telemetry, err)
	}
}
