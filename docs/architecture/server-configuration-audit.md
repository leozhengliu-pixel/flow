# Server configuration audit

This inventory covers Flow environment names used by `api/cmd/server` and its
internal packages, including the configuration loaders, tagged server code
and the startup subcommands. It covers the keys accepted by the YAML parser
and the computed environment name families. See the [deployment guide](../configuration.md)
for supported values and precedence.

## Kept, needs decision

The cleanup found no earlier-phase switch that was both absent from the
deployment guide and fixed to a single effective value across supported
deployments. Defaults apply when an operator has supplied no value. Several
tests and rollout runbooks select the earlier modes explicitly. Retiring a
mode requires its own rollout decision and checks with bounded metadata on
and off.

## Earlier rollout controls

| Controls | Reason to keep |
| --- | --- |
| `FLOW_BOUNDED_METADATA`, `FLOW_RECORD_HYDRATION`, `FLOW_RECORD_JOURNAL` | The deployment guide documents these together. Bounded activation and the flag-off rollback path depend on their selected modes. |
| `FLOW_METADATA_CATALOG`, `FLOW_RECORD_PEER_SYNC`, `FLOW_RECORD_ACCESS`, `FLOW_INDEXED_READS`, `FLOW_SYNC_V2`, `FLOW_SYNC_MANIFEST`, `FLOW_PRESENCE_MINIMAL` | Startup, peer, read and sync readers still inspect the deployment environment. The catalog override inside `server metadata-prepare` applies only to that command. |
| `FLOW_NARROW_MUTATION_INPUTS`, `FLOW_NARROW_MUTATION_VERIFY`, `FLOW_DOCUMENT_RECORD_VERIFY`, `FLOW_DOCUMENT_RECORDS`, `FLOW_TRASH_RECORDS`, `FLOW_PROJECT_RECORD_MUTATIONS`, `FLOW_INITIATIVE_RECORD_MUTATIONS` | These select read, write or verification paths at runtime, including comparisons and flag-off paths. |
| `FLOW_INTERNAL_CHAT_RECORDS`, `FLOW_INTERNAL_CONVERSATION_RECORDS`, `FLOW_MCP_AGENT_RECORDS`, `FLOW_MCP_ISSUE_RECORDS`, `FLOW_MCP_PROJECT_RECORDS`, `FLOW_MCP_WRITE_RECORDS` | Tool and worker dispatch can select these independently. |
| COLD and SQLite snapshot settings, metadata budgets and observation intervals | These can affect admission, performance, or diagnostics even when routing and responses match the defaults. |

## Environment Inventory

An exact-name match in the deployment guide includes mentions in prose as well as dedicated table entries.
Names that lack an exact match may appear in another runbook, in a wildcard family or in a code comment.
Every listed setting remains available for a separate product decision.

### Coding and sandbox

**Named in the deployment guide**

- `FLOW_CODING_APPROVAL_TIMEOUT`, `FLOW_CODING_ARCHIVE_MAX_BYTES`, `FLOW_CODING_ARCHIVE_PREFIX`, `FLOW_CODING_ARTIFACT_MAX_BYTES`, `FLOW_CODING_BRANCH_PREFIX`
- `FLOW_CODING_BROWSER`, `FLOW_CODING_CPU_REQUEST_RATIO`, `FLOW_CODING_DEBUG_TERMINAL`, `FLOW_CODING_DEBUG_TERMINAL_TTL`, `FLOW_CODING_DEFAULT_REGION`
- `FLOW_CODING_DOCKER_HOST`, `FLOW_CODING_DOCKER_NETWORK`, `FLOW_CODING_DOCKER_RUNTIME`, `FLOW_CODING_EGRESS_ACL_SECRET`, `FLOW_CODING_EGRESS_ALLOWLIST`
- `FLOW_CODING_EGRESS_DEFAULT_ALLOWLIST`, `FLOW_CODING_EGRESS_NO_PROXY`, `FLOW_CODING_EGRESS_PROXY_URL`, `FLOW_CODING_ENABLED`, `FLOW_CODING_GC_INTERVAL`
- `FLOW_CODING_GITLAB_CREDENTIAL_MODE`, `FLOW_CODING_GITLAB_TOKEN_SCOPES`, `FLOW_CODING_HEARTBEAT_TIMEOUT`, `FLOW_CODING_IDLE_TTL`, `FLOW_CODING_IMAGE`
- `FLOW_CODING_IMAGE_PULL_POLICY`, `FLOW_CODING_IMAGE_PULL_SECRETS`, `FLOW_CODING_INSTANCE_ID`, `FLOW_CODING_K8S_BURST`, `FLOW_CODING_K8S_EXTRA_ANNOTATIONS`
- `FLOW_CODING_K8S_EXTRA_LABELS`, `FLOW_CODING_K8S_FLOW_NAMESPACE`, `FLOW_CODING_K8S_FLOW_POD_SELECTOR`, `FLOW_CODING_K8S_HOST_USERS`, `FLOW_CODING_K8S_KUBECONFIG`
- `FLOW_CODING_K8S_NAMESPACE`, `FLOW_CODING_K8S_NETWORK_POLICY`, `FLOW_CODING_K8S_NODE_SELECTOR`, `FLOW_CODING_K8S_PRIORITY_CLASS`, `FLOW_CODING_K8S_PROXY_NAMESPACE`
- `FLOW_CODING_K8S_PROXY_POD_SELECTOR`, `FLOW_CODING_K8S_QPS`, `FLOW_CODING_K8S_RUNTIME_CLASS`, `FLOW_CODING_K8S_SERVICE_ACCOUNT`, `FLOW_CODING_K8S_STORAGE_CLASS`
- `FLOW_CODING_K8S_TOLERATIONS`, `FLOW_CODING_K8S_WORKSPACE_VOLUME`, `FLOW_CODING_LLM_ANTHROPIC_API_KEY`, `FLOW_CODING_LLM_ANTHROPIC_BASE_URL`, `FLOW_CODING_LLM_OPENAI_API_KEY`
- `FLOW_CODING_LLM_OPENAI_BASE_URL`, `FLOW_CODING_LLM_OPENAI_WIRE_API`, `FLOW_CODING_LLM_OPENSOURCE_API_KEY`, `FLOW_CODING_LLM_OPENSOURCE_BASE_URL`, `FLOW_CODING_LLM_TIMEOUT`
- `FLOW_CODING_LOCAL_COMMAND`, `FLOW_CODING_LOCAL_ROOT`, `FLOW_CODING_MAX_SESSIONS`, `FLOW_CODING_MAX_SESSION_DURATION`, `FLOW_CODING_MCP_SERVERS`
- `FLOW_CODING_MODELS_CLAUDE_CODE`, `FLOW_CODING_MODELS_CODEX`, `FLOW_CODING_MODELS_OPEN_SOURCE`, `FLOW_CODING_MODELS_PI`, `FLOW_CODING_MODEL_INFO`
- `FLOW_CODING_MODEL_PRICES`, `FLOW_CODING_OPENSANDBOX_API_KEY`, `FLOW_CODING_OPENSANDBOX_CA_FILE`, `FLOW_CODING_OPENSANDBOX_CREATE_TIMEOUT`, `FLOW_CODING_OPENSANDBOX_EGRESS_ALLOW`
- `FLOW_CODING_OPENSANDBOX_EGRESS_POLICY`, `FLOW_CODING_OPENSANDBOX_EPHEMERAL_STORAGE`, `FLOW_CODING_OPENSANDBOX_EXECD_ACCESS`, `FLOW_CODING_OPENSANDBOX_EXECD_TOKEN`, `FLOW_CODING_OPENSANDBOX_EXPIRY_WINDOW`
- `FLOW_CODING_OPENSANDBOX_EXTENSIONS`, `FLOW_CODING_OPENSANDBOX_EXTRA_METADATA`, `FLOW_CODING_OPENSANDBOX_IMAGE_PASSWORD`, `FLOW_CODING_OPENSANDBOX_IMAGE_USERNAME`, `FLOW_CODING_OPENSANDBOX_INSECURE_SKIP_VERIFY`
- `FLOW_CODING_OPENSANDBOX_POOLS`, `FLOW_CODING_OPENSANDBOX_REQUEST_TIMEOUT`, `FLOW_CODING_OPENSANDBOX_RETRIES`, `FLOW_CODING_OPENSANDBOX_RUN_AS_UID`, `FLOW_CODING_OPENSANDBOX_TOKEN_PATH`
- `FLOW_CODING_OPENSANDBOX_URL`, `FLOW_CODING_ORPHAN_GRACE`, `FLOW_CODING_PROVIDER`, `FLOW_CODING_PROVISION_RETRIES`, `FLOW_CODING_PROVISION_TIMEOUT`
- `FLOW_CODING_PR_MENTION`, `FLOW_CODING_PUBLISH_RETRIES`, `FLOW_CODING_PUBLISH_TIMEOUT`, `FLOW_CODING_QUEUE_TIMEOUT`, `FLOW_CODING_RECONCILE_INTERVAL`
- `FLOW_CODING_REGIONS`, `FLOW_CODING_REGION_LABEL`, `FLOW_CODING_RUNTIME_ADDR`, `FLOW_CODING_RUNTIME_PRICES`, `FLOW_CODING_RUNTIME_URL`
- `FLOW_CODING_SECRET_KEY`, `FLOW_CODING_SETUP_TIMEOUT`, `FLOW_CODING_SIZE_LARGE`, `FLOW_CODING_SIZE_MEDIUM`, `FLOW_CODING_SIZE_SMALL`
- `FLOW_CODING_SNAPSHOT_REFRESH_INTERVAL`, `FLOW_CODING_SNAPSHOT_RETAIN`, `FLOW_CODING_SNAPSHOT_RETRIES`, `FLOW_CODING_SNAPSHOT_STRATEGY`, `FLOW_CODING_SNAPSHOT_TIMEOUT`
- `FLOW_CODING_SNAPSHOT_TTL`, `FLOW_CODING_START_RETRIES`, `FLOW_CODING_START_TIMEOUT`, `FLOW_CODING_STOP_GRACE`, `FLOW_CODING_TOKEN_GRACE`
- `FLOW_CODING_TURN_TIMEOUT`, `FLOW_CODING_VOLUME_SNAPSHOT_CLASS`, `FLOW_CODING_WARM_POOL`, `FLOW_CODING_WARM_POOL_MAX_AGE`, `FLOW_CODING_WORKSPACE_SESSIONS`
- `FLOW_CODING_WORKSPACE_SIZE`, `FLOW_EGRESS_ACL_TIMEOUT`, `FLOW_EGRESS_FLOW_URL`, `FLOW_GITHUB_APP_API_URL`, `FLOW_GITHUB_APP_CLIENT_ID`
- `FLOW_GITHUB_APP_CLIENT_SECRET`, `FLOW_GITHUB_APP_HOST`, `FLOW_GITHUB_APP_ID`, `FLOW_GITHUB_APP_PRIVATE_KEY`, `FLOW_GITHUB_APP_SLUG`
- `FLOW_GITHUB_APP_WEBHOOK_SECRET`

**Without an exact name in the deployment guide**

- `FLOW_CODING_EGRESS_ACL_SECRET_FILE`, `FLOW_CODING_LLM_ANTHROPIC_API_KEY_FILE`, `FLOW_CODING_LLM_OPENAI_API_KEY_FILE`, `FLOW_CODING_LLM_OPENSOURCE_API_KEY_FILE`, `FLOW_CODING_OPENSANDBOX_API_KEY_FILE`
- `FLOW_CODING_OPENSANDBOX_EXECD_TOKEN_FILE`, `FLOW_CODING_OPENSANDBOX_IMAGE_PASSWORD_FILE`, `FLOW_CODING_OPENSANDBOX_SIZE_LARGE`, `FLOW_CODING_OPENSANDBOX_SIZE_MEDIUM`, `FLOW_CODING_OPENSANDBOX_SIZE_SMALL`
- `FLOW_CODING_SECRET_KEY_FILE`, `FLOW_GITHUB_APP_CLIENT_SECRET_FILE`, `FLOW_GITHUB_APP_PRIVATE_KEY_FILE`, `FLOW_GITHUB_APP_WEBHOOK_SECRET_FILE`

### Metadata storage and coordination

**Named in the deployment guide**

- `FLOW_BOUNDED_METADATA`, `FLOW_CONTENT_CACHE_BYTES`, `FLOW_DATABASE_CONN_MAX_LIFETIME`, `FLOW_DATABASE_DRIVER`, `FLOW_DATABASE_MAX_IDLE_CONNS`
- `FLOW_DATABASE_MAX_OPEN_CONNS`, `FLOW_DATABASE_MAX_TRANSACTION_BYTES`, `FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT`, `FLOW_DATABASE_PATH`, `FLOW_DATABASE_URL`
- `FLOW_METADATA_MEMORY_BYTES`, `FLOW_RECORD_HYDRATION`, `FLOW_RECORD_JOURNAL`, `FLOW_REDIS_ADDRS`, `FLOW_REDIS_CACHE`
- `FLOW_REDIS_CACHE_MAX_BYTES`, `FLOW_REDIS_CACHE_MAX_ENTRY_BYTES`, `FLOW_REDIS_CACHE_TIMEOUT`, `FLOW_REDIS_CACHE_TTL`, `FLOW_REDIS_CONNECT_TIMEOUT`
- `FLOW_REDIS_DB`, `FLOW_REDIS_DIAL_TIMEOUT`, `FLOW_REDIS_LOCK_TTL`, `FLOW_REDIS_LOCK_WAIT`, `FLOW_REDIS_MIN_IDLE_CONNS`
- `FLOW_REDIS_MODE`, `FLOW_REDIS_PASSWORD`, `FLOW_REDIS_PASSWORD_FILE`, `FLOW_REDIS_POOL_SIZE`, `FLOW_REDIS_PREFIX`
- `FLOW_REDIS_READ_TIMEOUT`, `FLOW_REDIS_TLS`, `FLOW_REDIS_URL`, `FLOW_REDIS_USERNAME`, `FLOW_REDIS_WRITE_TIMEOUT`
- `FLOW_SEARCH_INDEX_MAX_CHARS`, `FLOW_WORKSPACE_STATE_MAX_BYTES`

**Without an exact name in the deployment guide**

- `FLOW_BOUNDED_METADATA_STATS_INTERVAL`, `FLOW_COLD_DECODE_CACHE_BYTES`, `FLOW_COLD_DECODE_CACHE_IDLE`, `FLOW_COLD_PROJECTION_CONCURRENCY`, `FLOW_COLD_QUEUE_LIMIT`
- `FLOW_COLD_QUEUE_TIMEOUT`, `FLOW_COLD_SLOTS`, `FLOW_COLD_VIEW_LANES`, `FLOW_DATABASE_URL_FILE`, `FLOW_DOCUMENT_RECORDS`
- `FLOW_DOCUMENT_RECORD_VERIFY`, `FLOW_INDEXED_READS`, `FLOW_INITIATIVE_RECORD_MUTATIONS`, `FLOW_INTERNAL_CHAT_RECORDS`, `FLOW_INTERNAL_CONVERSATION_RECORDS`
- `FLOW_MCP_AGENT_RECORDS`, `FLOW_MCP_ISSUE_RECORDS`, `FLOW_MCP_PROJECT_RECORDS`, `FLOW_MCP_WRITE_RECORDS`, `FLOW_METADATA_CATALOG`
- `FLOW_METADATA_POLICY_BYTES`, `FLOW_METADATA_READ_CONCURRENCY`, `FLOW_METADATA_READ_QUEUE_LIMIT`, `FLOW_METADATA_READ_QUEUE_TIMEOUT`, `FLOW_METADATA_READ_WRITE_TIMEOUT`
- `FLOW_METADATA_SOURCE_BYTES`, `FLOW_METADATA_SOURCE_DISK_BYTES`, `FLOW_METADATA_WORK_BYTES`, `FLOW_NARROW_MUTATION_INPUTS`, `FLOW_NARROW_MUTATION_VERIFY`
- `FLOW_PRESENCE_MINIMAL`, `FLOW_PROJECT_RECORD_MUTATIONS`, `FLOW_REALTIME_BUDGET_BYTES`, `FLOW_RECORD_ACCESS`, `FLOW_RECORD_PEER_SYNC`
- `FLOW_RECORD_SPOOL_MAX_BYTES`, `FLOW_REDIS_URL_FILE`, `FLOW_REDIS_USERNAME_FILE`, `FLOW_RESOURCE_READ_CONCURRENCY`, `FLOW_RESOURCE_READ_QUEUE_LIMIT`
- `FLOW_RESOURCE_READ_QUEUE_TIMEOUT`, `FLOW_SQLITE_SNAPSHOT_CONNS`, `FLOW_SQLITE_SNAPSHOT_POOL`, `FLOW_SYNC_MANIFEST`, `FLOW_SYNC_V2`
- `FLOW_TRASH_RECORDS`

### Authentication and identity

**Named in the deployment guide**

- `FLOW_AUTH_ALLOWED_DOMAINS`, `FLOW_AUTH_AUTO_PROVISION`, `FLOW_AUTH_DISABLED`, `FLOW_AUTH_EMAIL_ENABLED`, `FLOW_AUTH_GOOGLE_ENABLED`
- `FLOW_AUTH_OIDC_ENABLED`, `FLOW_AUTH_SAML_ENABLED`, `FLOW_COOKIE_SECURE`, `FLOW_DEV_AUTH_TOKENS`, `FLOW_GOOGLE_CALENDAR_SYNC_INTERVAL`
- `FLOW_GOOGLE_CLIENT_ID`, `FLOW_GOOGLE_CLIENT_SECRET`, `FLOW_GOOGLE_OAUTH_CLIENT_ID`, `FLOW_GOOGLE_OAUTH_CLIENT_SECRET`, `FLOW_GOOGLE_REDIRECT_URL`
- `FLOW_OIDC_CLIENT_ID`, `FLOW_OIDC_CLIENT_SECRET`, `FLOW_OIDC_DEFAULT_ROLE`, `FLOW_OIDC_DISPLAY_NAME`, `FLOW_OIDC_IDENTITY_CLAIM`
- `FLOW_OIDC_ISSUER_URL`, `FLOW_OIDC_REDIRECT_URL`, `FLOW_OIDC_ROLE_CLAIM`, `FLOW_OIDC_ROLE_MAPPING`, `FLOW_OIDC_SCOPES`
- `FLOW_SAML_ACS_URL`, `FLOW_SAML_DISPLAY_NAME`, `FLOW_SAML_ENTITY_ID`, `FLOW_SAML_METADATA_URL`, `FLOW_SAML_METADATA_XML`
- `FLOW_SAML_METADATA_XML_FILE`, `FLOW_SAML_SP_CERTIFICATE`, `FLOW_SAML_SP_PRIVATE_KEY`

**Without an exact name in the deployment guide**

- `FLOW_GOOGLE_CLIENT_ID_FILE`, `FLOW_GOOGLE_CLIENT_SECRET_FILE`, `FLOW_OIDC_CLIENT_ID_FILE`, `FLOW_OIDC_CLIENT_SECRET_FILE`, `FLOW_SAML_SP_CERTIFICATE_FILE`
- `FLOW_SAML_SP_PRIVATE_KEY_FILE`, `FLOW_SESSION_TOUCH_INTERVAL`

### Agent, MCP, loops and search providers

**Named in the deployment guide**

- `FLOW_AGENT_ANTHROPIC_VERSION`, `FLOW_AGENT_API_KEY`, `FLOW_AGENT_BASE_URL`, `FLOW_AGENT_ENABLED`, `FLOW_AGENT_MAX_OUTPUT_TOKENS`
- `FLOW_AGENT_MAX_TOOL_CALLS`, `FLOW_AGENT_MAX_TOOL_TURNS`, `FLOW_AGENT_MEDIA_API_KEY`, `FLOW_AGENT_MEDIA_BASE_URL`, `FLOW_AGENT_MEDIA_TIMEOUT`
- `FLOW_AGENT_MEDIA_TRANSCRIBE_MODEL`, `FLOW_AGENT_MEDIA_VIDEO_MODEL`, `FLOW_AGENT_MODEL`, `FLOW_AGENT_NATIVE_WEB_SEARCH`, `FLOW_AGENT_PROTOCOL`
- `FLOW_AGENT_REASONING_EFFORT`, `FLOW_AGENT_RUNTIME`, `FLOW_AGENT_TIMEOUT`, `FLOW_AGENT_TOOLS_ENABLED`, `FLOW_AGENT_TRANSCRIPT_OFFLOAD_BYTES`
- `FLOW_AGENT_WORKER_CLAIM_TIMEOUT`, `FLOW_AGENT_WORKSPACE_CONCURRENCY`, `FLOW_AGENT_WRITE_TOOLS`, `FLOW_AI_INCLUDED_CREDITS_USD`, `FLOW_LOOP_LEASE_TTL`
- `FLOW_LOOP_MAX_CONCURRENT_RUNS`, `FLOW_LOOP_MAX_CONCURRENT_RUNS_PER_WORKSPACE`, `FLOW_LOOP_MAX_CONTEXT_BYTES`, `FLOW_LOOP_MAX_TOOL_CALLS`, `FLOW_LOOP_MAX_TOOL_OUTPUT_BYTES`
- `FLOW_LOOP_MAX_TURNS`, `FLOW_LOOP_RETRY_ATTEMPTS`, `FLOW_LOOP_RETRY_BACKOFF`, `FLOW_LOOP_RUN_TIMEOUT`, `FLOW_MCP_CREDENTIALS`
- `FLOW_MCP_OAUTH_CLIENTS`, `FLOW_TOOL_CALL_CONCURRENCY`, `FLOW_TTS_API_KEY`, `FLOW_TTS_BASE_URL`, `FLOW_TTS_ENABLED`
- `FLOW_TTS_MODEL`, `FLOW_TTS_TIMEOUT`, `FLOW_TTS_VOICE`, `FLOW_WEB_SEARCH_PROVIDER`, `FLOW_WEB_SEARCH_URL`

**Without an exact name in the deployment guide**

- `FLOW_AGENT_API_KEY_FILE`, `FLOW_AGENT_MEDIA_API_KEY_FILE`, `FLOW_AGENT_WORKER_SECRET`, `FLOW_AGENT_WORKER_SECRET_FILE`, `FLOW_LOOP_CONNECTOR_POLICY`
- `FLOW_TTS_API_KEY_FILE`, `FLOW_WEB_SEARCH_API_KEY`, `FLOW_WEB_SEARCH_API_KEY_FILE`

### Integrations and communications

**Named in the deployment guide**

- `FLOW_CHAT_PLATFORM`, `FLOW_CONNECTOR_SECRET_KEY`, `FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS`, `FLOW_INTEGRATION_GITHUB_ACCESS_TOKEN`, `FLOW_INTEGRATION_GITHUB_API_URL`
- `FLOW_INTEGRATION_GITLAB_HOST`, `FLOW_NOTION_CONNECTION_URL`, `FLOW_REVIEW_IMPORT`, `FLOW_REVIEW_IMPORT_INTERVAL`, `FLOW_SMTP_FROM`
- `FLOW_SMTP_HOST`, `FLOW_SMTP_PORT`, `FLOW_SMTP_USERNAME`

**Without an exact name in the deployment guide**

- `FLOW_CONNECTOR_SECRET_KEY_FILE`, `FLOW_CONNECTOR_SECRET_PREVIOUS_KEYS_FILE`, `FLOW_EMAIL_INTAKE_DOMAIN`, `FLOW_INTEGRATION_SLACK_SIGNING_SECRET`, `FLOW_LINEAR_API_URL`
- `FLOW_ONBOARDING_EMAILS`, `FLOW_ONBOARDING_MCP_DELAY`, `FLOW_SLACK_API_URL`, `FLOW_SMTP_PASSWORD`, `FLOW_SUPPORT_EMAIL`

### Service and deployment

**Named in the deployment guide**

- `FLOW_API_MAX_COMPLEXITY`, `FLOW_API_RATE_LIMIT`, `FLOW_API_RATE_LIMIT_AGENT_RUN_REQUESTS`, `FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_OVERRIDES`, `FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_REQUESTS`
- `FLOW_API_RATE_LIMIT_API_KEY_REQUESTS`, `FLOW_API_RATE_LIMIT_OAUTH_REQUESTS`, `FLOW_API_RATE_LIMIT_SESSION_REQUESTS`, `FLOW_API_RATE_LIMIT_UNAUTHENTICATED_REQUESTS`, `FLOW_APP_URL`
- `FLOW_ASSET_FALLBACK_URL`, `FLOW_ASSET_PEERS`, `FLOW_ASSET_PREVIOUS_PATH`, `FLOW_ASSET_SHARED_KEEP_RELEASES`, `FLOW_ASSET_SHARED_PATH`
- `FLOW_AUDIT_LOG_IP`, `FLOW_AUDIT_LOG_RETENTION_DAYS`, `FLOW_CHANGE_LOG_MIN_ROWS`, `FLOW_CONFIG_FILE`, `FLOW_DB_PATH`
- `FLOW_DEFAULT_LANGUAGE`, `FLOW_DEV_ALLOW_LOOPBACK_CODE_HOSTS`, `FLOW_DEV_GOOGLE_BASE_URL`, `FLOW_DOMAIN_EVENT_RETENTION_DAYS`, `FLOW_ENV`
- `FLOW_ENVIRONMENT`, `FLOW_FILE_URL_SIGNING_KEY`, `FLOW_HTTP_ADDR`, `FLOW_LOG_FORMAT`, `FLOW_LOG_LEVEL`
- `FLOW_S3_ACCESS_KEY_ID`, `FLOW_S3_BUCKET`, `FLOW_S3_CONTENT_PREFIX`, `FLOW_S3_ENDPOINT`, `FLOW_S3_PATH_STYLE`
- `FLOW_S3_PREFIX`, `FLOW_S3_REGION`, `FLOW_S3_SECRET_ACCESS_KEY`, `FLOW_S3_SESSION_TOKEN`, `FLOW_S3_VALIDATE_ON_START`
- `FLOW_SECURITY_CSP`, `FLOW_SECURITY_CSP_EXTRA_SOURCES`, `FLOW_SECURITY_CSP_REPORT_URI`, `FLOW_SECURITY_FRAME_ANCESTORS`, `FLOW_SECURITY_HEADERS`
- `FLOW_SECURITY_HEADERS_SKIP`, `FLOW_SECURITY_HSTS`, `FLOW_SECURITY_HSTS_INCLUDE_SUBDOMAINS`, `FLOW_SECURITY_HSTS_MAX_AGE`, `FLOW_SECURITY_HSTS_PRELOAD`
- `FLOW_SECURITY_PERMISSIONS_POLICY`, `FLOW_SECURITY_REFERRER_POLICY`, `FLOW_SLOW_MUTATION_MS`, `FLOW_STATIC_PATH`, `FLOW_STORAGE_DRIVER`
- `FLOW_STORAGE_LOCAL_PATH`, `FLOW_TELEMETRY_ENABLED`, `FLOW_TRASH_RETENTION_DAYS`, `FLOW_TRUSTED_CA_FILE`, `FLOW_TRUSTED_PRIVATE_ENDPOINTS`
- `FLOW_TRUSTED_PRIVATE_NETWORKS`, `FLOW_TRUSTED_PROXIES`, `FLOW_TRUST_PROXY_HEADERS`, `FLOW_WORKSPACE_DEFAULT_REGION`, `FLOW_WORKSPACE_DELETION_GRACE_DAYS`
- `FLOW_WORKSPACE_REGION_SELECTOR_ENABLED`, `FLOW_WORKSPACE_TUTORIAL`

**Without an exact name in the deployment guide**

- `FLOW_API_RATE_LIMIT_AGENT_RUN_COMPLEXITY`, `FLOW_API_RATE_LIMIT_AGENT_WORKSPACE_COMPLEXITY`, `FLOW_API_RATE_LIMIT_API_KEY_COMPLEXITY`, `FLOW_API_RATE_LIMIT_OAUTH_COMPLEXITY`, `FLOW_API_RATE_LIMIT_SESSION_COMPLEXITY`
- `FLOW_API_RATE_LIMIT_UNAUTHENTICATED_COMPLEXITY`, `FLOW_HEALTHCHECK_URL`, `FLOW_LOADRIG_METRICS_ADDR`, `FLOW_S3_ACCESS_KEY_ID_FILE`, `FLOW_S3_SECRET_ACCESS_KEY_FILE`
- `FLOW_S3_SESSION_TOKEN_FILE`, `FLOW_SECURITY_FIXTURE_ORIGIN`, `FLOW_UPLOAD_PATH`

### Computed names

| Family | Expansion or selector |
| --- | --- |
| `FLOW_INTEGRATION_<PROVIDER>_<SUFFIX>` | The OAuth reader uppercases the provider and uses `AUTHORIZATION_URL`, `TOKEN_URL`, `REVOKE_URL`, `CLIENT_ID`, `CLIENT_SECRET` or `REDIRECT_URI`. Review settings use `ACCESS_TOKEN` and `API_URL`. Both delivery lanes try `DELIVERY_SECRET` and `WEBHOOK_SECRET`. |
| `FLOW_INTEGRATION_*` | Connection fields `clientSecretEnv`, `signingSecretEnv`, `webhookSecretEnv`, `deliverySecretEnv` and `deliveryTokenEnv` can name a permitted variable dynamically. |
| `FLOW_IDP_*` | Identity provider `clientSecretEnv` selects the variable. |
| `FLOW_API_RATE_LIMIT_<CATEGORY>_REQUESTS` and `FLOW_API_RATE_LIMIT_<CATEGORY>_COMPLEXITY` | Six categories in `api_rate_limit.go`. The inventory lists all twelve names. |
| `FLOW_CODING_OPENSANDBOX_SIZE_<SIZE>` | `SMALL`, `MEDIUM` and `LARGE` in `coding_opensandbox.go`. |
| `<NAME>_FILE` | `secret` and `readSecretEnv` read file-path alternatives for the corresponding names. The inventory includes the supported `FLOW_*` alternatives. |

`FLOW_LOADRIG_METRICS_ADDR` is read in `loadtest`, and `FLOW_SECURITY_FIXTURE_ORIGIN` is read in `securitye2e`.
`FLOW_P21_INVENTORY_REPORT`, `FLOW_SW550_WRITE` and `FLOW_TEST_*` belong to tests.
The server sets runner and sandbox environment names `FLOW_ARCHIVE_URL`, `FLOW_ENVIRONMENT_ID`, `FLOW_LOCAL_HOME_DIR`, `FLOW_LOCAL_TOKEN_FILE`, `FLOW_LOCAL_WORKSPACE_DIR`, `FLOW_PIDS_LIMIT`, `FLOW_RUNNER_MODE`, `FLOW_RUNTIME_URL`, `FLOW_SANDBOX`, `FLOW_SANDBOX_ID`, `FLOW_SANDBOX_SIZE`, `FLOW_SNAPSHOT`, `FLOW_TOKEN_FILE`. The store also reads `FLOW_ENVIRONMENT_ID` from saved grant state.

## YAML keys

These are the leaves of `fileSchema` in `api/internal/config/file.go` and the nested keys its custom parsers recognize.
`--config` or `FLOW_CONFIG_FILE` selects the file. A deployment environment setting takes precedence over a file-provided environment setting.
The `server.env` mapping accepts non-secret `FLOW_*` or `OTEL_*` entries subject to the configuration parser's validation.

| Key | Environment setting or purpose |
| --- | --- |
| `defaults.team.autoArchiveMonths` | Creation default |
| `defaults.team.autoClose.parentIssues` | Creation default |
| `defaults.team.autoClose.staleIssues` | Creation default |
| `defaults.team.autoClose.staleMonths` | Creation default |
| `defaults.team.autoClose.subIssues` | Creation default |
| `defaults.team.cycles.autoAddCompleted` | Creation default |
| `defaults.team.cycles.autoAddStarted` | Creation default |
| `defaults.team.cycles.autoCreate` | Creation default |
| `defaults.team.cycles.cooldownWeeks` | Creation default |
| `defaults.team.cycles.durationWeeks` | Creation default |
| `defaults.team.cycles.enabled` | Creation default |
| `defaults.team.cycles.moveUnfinished` | Creation default |
| `defaults.team.cycles.startDay` | Creation default |
| `defaults.team.cycles.upcomingCycles` | Creation default |
| `defaults.team.defaultPriority` | Creation default |
| `defaults.team.detailedHistory` | Creation default |
| `defaults.team.estimates.allowZero` | Creation default |
| `defaults.team.estimates.countUnestimated` | Creation default |
| `defaults.team.estimates.extended` | Creation default |
| `defaults.team.estimates.type` | Creation default |
| `defaults.team.timezone` | Creation default |
| `defaults.team.triage.enabled` | Creation default |
| `defaults.team.triage.requirePriority` | Creation default |
| `defaults.user.displayNames` | Creation default |
| `defaults.user.firstDayOfWeek` | Creation default |
| `defaults.user.fontSize` | Creation default |
| `defaults.user.homeView` | Creation default |
| `defaults.user.language` | `FLOW_DEFAULT_LANGUAGE` |
| `defaults.user.notifications.desktop` | Creation default |
| `defaults.user.notifications.email` | Creation default |
| `defaults.user.notifications.emailFormat` | Creation default |
| `defaults.user.theme` | Creation default |
| `defaults.workspace.allowedDomains` | Creation default |
| `defaults.workspace.features` | Creation default |
| `defaults.workspace.fiscalMonth` | Creation default |
| `defaults.workspace.guestsAllowed` | Creation default |
| `defaults.workspace.inviteLinksEnabled` | Creation default |
| `defaults.workspace.invitePermission` | Creation default |
| `defaults.workspace.labelGroups` | Creation default |
| `defaults.workspace.labels` | Creation default |
| `defaults.workspace.region` | `FLOW_WORKSPACE_DEFAULT_REGION` |
| `defaults.workspace.sessionDurationDays` | Creation default |
| `defaults.workspace.teamCreatePermission` | Creation default |
| `defaults.workspace.tutorial` | `FLOW_WORKSPACE_TUTORIAL` |
| `defaults.workspace.workflowStates` | Creation default |
| `server.agent.anthropicVersion` | `FLOW_AGENT_ANTHROPIC_VERSION` |
| `server.agent.baseUrl` | `FLOW_AGENT_BASE_URL` |
| `server.agent.enabled` | `FLOW_AGENT_ENABLED` |
| `server.agent.maxOutputTokens` | `FLOW_AGENT_MAX_OUTPUT_TOKENS` |
| `server.agent.maxToolCalls` | `FLOW_AGENT_MAX_TOOL_CALLS` |
| `server.agent.maxToolTurns` | `FLOW_AGENT_MAX_TOOL_TURNS` |
| `server.agent.media.baseUrl` | `FLOW_AGENT_MEDIA_BASE_URL` |
| `server.agent.media.timeout` | `FLOW_AGENT_MEDIA_TIMEOUT` |
| `server.agent.media.transcribeModel` | `FLOW_AGENT_MEDIA_TRANSCRIBE_MODEL` |
| `server.agent.media.videoModel` | `FLOW_AGENT_MEDIA_VIDEO_MODEL` |
| `server.agent.model` | `FLOW_AGENT_MODEL` |
| `server.agent.nativeWebSearch` | `FLOW_AGENT_NATIVE_WEB_SEARCH` |
| `server.agent.protocol` | `FLOW_AGENT_PROTOCOL` |
| `server.agent.reasoningEffort` | `FLOW_AGENT_REASONING_EFFORT` |
| `server.agent.runtime` | `FLOW_AGENT_RUNTIME` |
| `server.agent.timeout` | `FLOW_AGENT_TIMEOUT` |
| `server.agent.toolsEnabled` | `FLOW_AGENT_TOOLS_ENABLED` |
| `server.agent.workerClaimTimeout` | `FLOW_AGENT_WORKER_CLAIM_TIMEOUT` |
| `server.agent.workspaceConcurrency` | `FLOW_AGENT_WORKSPACE_CONCURRENCY` |
| `server.agent.writeTools` | `FLOW_AGENT_WRITE_TOOLS` |
| `server.appUrl` | `FLOW_APP_URL` |
| `server.assets.fallbackUrls` | `FLOW_ASSET_FALLBACK_URL` |
| `server.assets.peers` | `FLOW_ASSET_PEERS` |
| `server.assets.previousPath` | `FLOW_ASSET_PREVIOUS_PATH` |
| `server.assets.sharedKeepReleases` | `FLOW_ASSET_SHARED_KEEP_RELEASES` |
| `server.assets.sharedPath` | `FLOW_ASSET_SHARED_PATH` |
| `server.auditLog.ip` | `FLOW_AUDIT_LOG_IP` |
| `server.auth.allowedDomains` | `FLOW_AUTH_ALLOWED_DOMAINS` |
| `server.auth.autoProvision` | `FLOW_AUTH_AUTO_PROVISION` |
| `server.auth.disabled` | `FLOW_AUTH_DISABLED` |
| `server.auth.email.enabled` | `FLOW_AUTH_EMAIL_ENABLED` |
| `server.auth.google.clientId` | `FLOW_GOOGLE_CLIENT_ID` |
| `server.auth.google.enabled` | `FLOW_AUTH_GOOGLE_ENABLED` |
| `server.auth.google.redirectUrl` | `FLOW_GOOGLE_REDIRECT_URL` |
| `server.auth.oidc.clientId` | `FLOW_OIDC_CLIENT_ID` |
| `server.auth.oidc.defaultRole` | `FLOW_OIDC_DEFAULT_ROLE` |
| `server.auth.oidc.displayName` | `FLOW_OIDC_DISPLAY_NAME` |
| `server.auth.oidc.enabled` | `FLOW_AUTH_OIDC_ENABLED` |
| `server.auth.oidc.identityClaim` | `FLOW_OIDC_IDENTITY_CLAIM` |
| `server.auth.oidc.issuerUrl` | `FLOW_OIDC_ISSUER_URL` |
| `server.auth.oidc.redirectUrl` | `FLOW_OIDC_REDIRECT_URL` |
| `server.auth.oidc.roleClaim` | `FLOW_OIDC_ROLE_CLAIM` |
| `server.auth.oidc.roleMapping` | `FLOW_OIDC_ROLE_MAPPING` |
| `server.auth.oidc.scopes` | `FLOW_OIDC_SCOPES` |
| `server.auth.saml.acsUrl` | `FLOW_SAML_ACS_URL` |
| `server.auth.saml.displayName` | `FLOW_SAML_DISPLAY_NAME` |
| `server.auth.saml.enabled` | `FLOW_AUTH_SAML_ENABLED` |
| `server.auth.saml.entityId` | `FLOW_SAML_ENTITY_ID` |
| `server.auth.saml.metadataUrl` | `FLOW_SAML_METADATA_URL` |
| `server.database.agentTranscriptOffloadBytes` | `FLOW_AGENT_TRANSCRIPT_OFFLOAD_BYTES` |
| `server.database.connMaxLifetime` | `FLOW_DATABASE_CONN_MAX_LIFETIME` |
| `server.database.contentCacheBytes` | `FLOW_CONTENT_CACHE_BYTES` |
| `server.database.driver` | `FLOW_DATABASE_DRIVER` |
| `server.database.maxIdleConns` | `FLOW_DATABASE_MAX_IDLE_CONNS` |
| `server.database.maxOpenConns` | `FLOW_DATABASE_MAX_OPEN_CONNS` |
| `server.database.maxTransactionBytes` | `FLOW_DATABASE_MAX_TRANSACTION_BYTES` |
| `server.database.migrationLockTimeout` | `FLOW_DATABASE_MIGRATION_LOCK_TIMEOUT` |
| `server.database.path` | `FLOW_DATABASE_PATH` |
| `server.database.searchIndexMaxChars` | `FLOW_SEARCH_INDEX_MAX_CHARS` |
| `server.database.workspaceStateMaxBytes` | `FLOW_WORKSPACE_STATE_MAX_BYTES` |
| `server.env` | Deployment environment map |
| `server.environment` | `FLOW_ENV` |
| `server.httpAddr` | `FLOW_HTTP_ADDR` |
| `server.log.format` | `FLOW_LOG_FORMAT` |
| `server.log.level` | `FLOW_LOG_LEVEL` |
| `server.outbound.trustedCaFile` | `FLOW_TRUSTED_CA_FILE` |
| `server.outbound.trustedPrivateEndpoints` | `FLOW_TRUSTED_PRIVATE_ENDPOINTS` |
| `server.outbound.trustedPrivateNetworks` | `FLOW_TRUSTED_PRIVATE_NETWORKS` |
| `server.proxy.trustedProxies` | `FLOW_TRUSTED_PROXIES` |
| `server.proxy.trustHeaders` | `FLOW_TRUST_PROXY_HEADERS` |
| `server.redis.addrs` | `FLOW_REDIS_ADDRS` |
| `server.redis.cache.enabled` | `FLOW_REDIS_CACHE` |
| `server.redis.cache.maxBytes` | `FLOW_REDIS_CACHE_MAX_BYTES` |
| `server.redis.cache.maxEntryBytes` | `FLOW_REDIS_CACHE_MAX_ENTRY_BYTES` |
| `server.redis.cache.timeout` | `FLOW_REDIS_CACHE_TIMEOUT` |
| `server.redis.cache.ttl` | `FLOW_REDIS_CACHE_TTL` |
| `server.redis.connectTimeout` | `FLOW_REDIS_CONNECT_TIMEOUT` |
| `server.redis.db` | `FLOW_REDIS_DB` |
| `server.redis.dialTimeout` | `FLOW_REDIS_DIAL_TIMEOUT` |
| `server.redis.lockTtl` | `FLOW_REDIS_LOCK_TTL` |
| `server.redis.lockWait` | `FLOW_REDIS_LOCK_WAIT` |
| `server.redis.minIdleConns` | `FLOW_REDIS_MIN_IDLE_CONNS` |
| `server.redis.mode` | `FLOW_REDIS_MODE` |
| `server.redis.poolSize` | `FLOW_REDIS_POOL_SIZE` |
| `server.redis.prefix` | `FLOW_REDIS_PREFIX` |
| `server.redis.readTimeout` | `FLOW_REDIS_READ_TIMEOUT` |
| `server.redis.tls` | `FLOW_REDIS_TLS` |
| `server.redis.writeTimeout` | `FLOW_REDIS_WRITE_TIMEOUT` |
| `server.retention.auditLogDays` | `FLOW_AUDIT_LOG_RETENTION_DAYS` |
| `server.retention.changeLogMinRows` | `FLOW_CHANGE_LOG_MIN_ROWS` |
| `server.retention.domainEventDays` | `FLOW_DOMAIN_EVENT_RETENTION_DAYS` |
| `server.retention.trashDays` | `FLOW_TRASH_RETENTION_DAYS` |
| `server.retention.workspaceDeletionDays` | `FLOW_WORKSPACE_DELETION_GRACE_DAYS` |
| `server.smtp.from` | `FLOW_SMTP_FROM` |
| `server.smtp.host` | `FLOW_SMTP_HOST` |
| `server.smtp.port` | `FLOW_SMTP_PORT` |
| `server.smtp.username` | `FLOW_SMTP_USERNAME` |
| `server.staticPath` | `FLOW_STATIC_PATH` |
| `server.storage.driver` | `FLOW_STORAGE_DRIVER` |
| `server.storage.localPath` | `FLOW_STORAGE_LOCAL_PATH` |
| `server.storage.s3.bucket` | `FLOW_S3_BUCKET` |
| `server.storage.s3.contentPrefix` | `FLOW_S3_CONTENT_PREFIX` |
| `server.storage.s3.endpoint` | `FLOW_S3_ENDPOINT` |
| `server.storage.s3.pathStyle` | `FLOW_S3_PATH_STYLE` |
| `server.storage.s3.prefix` | `FLOW_S3_PREFIX` |
| `server.storage.s3.region` | `FLOW_S3_REGION` |
| `server.storage.s3.validateOnStart` | `FLOW_S3_VALIDATE_ON_START` |
| `server.telemetry.enabled` | `FLOW_TELEMETRY_ENABLED` |
| `server.telemetry.endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT` |
| `server.telemetry.environment` | `FLOW_ENVIRONMENT` |
| `server.telemetry.metricsEndpoint` | `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` |
| `server.telemetry.serviceName` | `OTEL_SERVICE_NAME` |
| `server.telemetry.tracesEndpoint` | `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` |
| `server.tts.baseUrl` | `FLOW_TTS_BASE_URL` |
| `server.tts.enabled` | `FLOW_TTS_ENABLED` |
| `server.tts.model` | `FLOW_TTS_MODEL` |
| `server.tts.timeout` | `FLOW_TTS_TIMEOUT` |
| `server.tts.voice` | `FLOW_TTS_VOICE` |
| `server.webSearch.provider` | `FLOW_WEB_SEARCH_PROVIDER` |
| `server.webSearch.url` | `FLOW_WEB_SEARCH_URL` |
| `server.workspaces.regionSelector` | `FLOW_WORKSPACE_REGION_SELECTOR_ENABLED` |
| `version` | Configuration schema version |

| Custom or map key | Nested keys |
| --- | --- |
| `server.auth.oidc.roleMapping.<claim>` | Operator-defined claim key mapped to a role |
| `server.env.<name>` | Permitted environment variable name |
| `defaults.workspace.features.<feature>` | `ai`, `ai-agent`, `asks`, `audit-log`, `coding-sessions`, `customer-requests`, `dashboards`, `documents`, `emojis`, `initiatives`, `loops`, `pulse`, `recently-deleted`, `releases`, `triage-intelligence` |
| `defaults.workspace.workflowStates[]` | `name`, `type`, `color`, `description`, `position`, `default` |
| `defaults.workspace.labels[]` | `name`, `color`, `description`, `resourceType` |
| `defaults.workspace.labelGroups[]` | `name`, `color`, `description`, `resourceType`, `labels` |
| `defaults.workspace.labelGroups[].labels[]` | `name`, `color`, `description` |
