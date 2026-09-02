package config

import (
	"strings"
	"testing"
)

func TestLoadDefaultsAndBackendValidation(t *testing.T) {
	for _, key := range []string{"FLOW_DATABASE_DRIVER", "FLOW_DATABASE_URL", "FLOW_WORKSPACE_STATE_MAX_BYTES", "FLOW_REDIS_MODE", "FLOW_REDIS_URL", "FLOW_REDIS_ADDRS", "FLOW_STORAGE_DRIVER", "FLOW_S3_BUCKET", "FLOW_S3_REGION", "FLOW_AUTH_GOOGLE_ENABLED", "FLOW_AUTH_OIDC_ENABLED", "FLOW_AUTH_SAML_ENABLED", "FLOW_AGENT_ENABLED", "FLOW_AGENT_PROTOCOL", "FLOW_AGENT_BASE_URL", "FLOW_AGENT_MODEL", "FLOW_AGENT_MAX_OUTPUT_TOKENS", "FLOW_TELEMETRY_ENABLED", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(key, "")
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Database.Driver != "sqlite" || loaded.Database.MaxStateBytes != 64<<20 || loaded.Storage.Driver != "local" || loaded.Agent.Enabled || loaded.Agent.Protocol != "openai-responses" || loaded.Agent.MaxOutputTokens != 4096 || !loaded.Agent.ToolsEnabled || loaded.Telemetry.Enabled || loaded.Auth.OIDC.IdentityClaim != "sub" {
		t.Fatalf("unexpected defaults: %#v", loaded)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", "postgres")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_DATABASE_URL") {
		t.Fatalf("postgres without URL error = %v", err)
	}
	t.Setenv("FLOW_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOW_WORKSPACE_STATE_MAX_BYTES", "1024")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_WORKSPACE_STATE_MAX_BYTES") {
		t.Fatalf("workspace state size validation error = %v", err)
	}
	t.Setenv("FLOW_WORKSPACE_STATE_MAX_BYTES", "67108864")
	t.Setenv("FLOW_OIDC_IDENTITY_CLAIM", "employeeNumber")
	if loaded, err := Load(); err != nil || loaded.Auth.OIDC.IdentityClaim != "employeeNumber" {
		t.Fatalf("OIDC identity claim config = %#v, %v", loaded.Auth.OIDC.IdentityClaim, err)
	}
	t.Setenv("FLOW_STORAGE_DRIVER", "s3")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_S3_BUCKET") {
		t.Fatalf("S3 without bucket error = %v", err)
	}
}

func TestWorkspaceRegionSelectorConfig(t *testing.T) {
	t.Setenv("FLOW_WORKSPACE_REGION_SELECTOR_ENABLED", "false")
	t.Setenv("FLOW_WORKSPACE_DEFAULT_REGION", "eu")
	loaded, err := Load()
	if err != nil || loaded.WorkspaceRegionSelectorEnabled || loaded.WorkspaceDefaultRegion != "eu" {
		t.Fatalf("workspace region config = enabled=%v region=%q err=%v", loaded.WorkspaceRegionSelectorEnabled, loaded.WorkspaceDefaultRegion, err)
	}
	t.Setenv("FLOW_WORKSPACE_DEFAULT_REGION", "invalid")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_WORKSPACE_DEFAULT_REGION") {
		t.Fatalf("invalid default region error = %v", err)
	}
}

func TestLoadRedisClusterValidation(t *testing.T) {
	t.Setenv("FLOW_REDIS_MODE", "cluster")
	t.Setenv("FLOW_REDIS_ADDRS", "redis-1:6379, redis-2:6379,redis-3:6379")
	t.Setenv("FLOW_DATABASE_DRIVER", "postgres")
	t.Setenv("FLOW_DATABASE_URL", "postgres://flow:flow@postgres/flow")
	loaded, err := Load()
	if err != nil || loaded.Redis.Mode != "cluster" || len(loaded.Redis.Addrs) != 3 || loaded.Redis.PoolSize != 40 {
		t.Fatalf("Redis cluster config = %#v, %v", loaded.Redis, err)
	}

	t.Setenv("FLOW_REDIS_DB", "1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_REDIS_DB") {
		t.Fatalf("cluster DB validation error = %v", err)
	}
	t.Setenv("FLOW_REDIS_DB", "0")
	t.Setenv("FLOW_DATABASE_DRIVER", "sqlite")
	t.Setenv("FLOW_DATABASE_URL", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("Redis with SQLite validation error = %v", err)
	}
}

func TestLoadAgentValidation(t *testing.T) {
	t.Setenv("FLOW_AGENT_ENABLED", "true")
	t.Setenv("FLOW_AGENT_BASE_URL", "http://agent.example/v1")
	t.Setenv("FLOW_AGENT_MODEL", "flow-test")
	loaded, err := Load()
	if err != nil || !loaded.Agent.Enabled || loaded.Agent.Model != "flow-test" || loaded.Agent.Protocol != "openai-responses" {
		t.Fatalf("agent config = %#v, %v", loaded.Agent, err)
	}
	t.Setenv("FLOW_AGENT_PROTOCOL", "anthropic-messages")
	if loaded, err := Load(); err != nil || loaded.Agent.Protocol != "anthropic-messages" {
		t.Fatalf("Anthropic agent config = %#v, %v", loaded.Agent, err)
	}
	t.Setenv("FLOW_AGENT_PROTOCOL", "invalid")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_AGENT_PROTOCOL") {
		t.Fatalf("invalid agent protocol error = %v", err)
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

func TestLoadOIDCRoleMapping(t *testing.T) {
	t.Setenv("FLOW_OIDC_ROLE_CLAIM", "groups")
	t.Setenv("FLOW_OIDC_ROLE_MAPPING", "staff=admin,contractors:guest")
	t.Setenv("FLOW_OIDC_DEFAULT_ROLE", "member")
	loaded, err := Load()
	if err != nil || loaded.Auth.OIDC.RoleClaim != "groups" || loaded.Auth.OIDC.RoleMapping["staff"] != "admin" || loaded.Auth.OIDC.RoleMapping["contractors"] != "guest" || loaded.Auth.OIDC.DefaultRole != "member" {
		t.Fatalf("OIDC role mapping config = %#v, %v", loaded.Auth.OIDC, err)
	}
	t.Setenv("FLOW_OIDC_DEFAULT_ROLE", "superuser")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "FLOW_OIDC_DEFAULT_ROLE") {
		t.Fatalf("invalid OIDC default role error = %v", err)
	}
}
