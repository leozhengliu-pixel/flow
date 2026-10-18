package store

import (
	"context"
	"strings"
)

// Coding sessions schema (docs/coding-sessions-design.md §6). One ensure stage
// creates every table on SQLite, PostgreSQL and MySQL; the store methods live
// in coding_*.go files owned by the tracks listed in the design.
//
// Conventions shared with agent_runs: ids are VARCHAR(191); timestamps are
// BIGINT unix milliseconds; JSON documents are BLOB/BYTEA/LONGBLOB in `data`;
// leases are (lease_owner, lease_until) CAS updates so several API replicas
// can reconcile without Redis; token tables store only SHA-256 hashes.
//
// Tables:
//   - coding_environments: one row per environment; active_repo_key is the
//     repo key while not archived and NULL when archived, so the unique index
//     enforces one active environment per repository.
//   - coding_environment_secrets: AES-GCM ciphertext (FLOW_CODING_SECRET_KEY),
//     AAD = environment id + name; never returned, never updated.
//   - coding_environment_snapshots: snapshot builds and their storage refs.
//   - coding_sessions: the session state machine (state, phase, deadlines,
//     lease); `data` holds the domain.CodingSession document.
//   - coding_session_inbox: follow-up prompts and signals for the runner.
//   - coding_sandboxes: desired vs observed sandboxes (session, snapshot,
//     debug, warm); the sandbox reconciler converges the provider to them.
//   - coding_sandbox_tokens: sandbox runtime tokens (hash, expiry, revocation).
//   - coding_session_pull_requests: PRs/MRs linked to sessions.
//   - coding_session_commits: pushed commits keyed by turn activity.
//   - coding_session_artifacts: screenshots and other session files.
//   - coding_usage_records: metered LLM tokens and sandbox runtime.
//   - code_host_installations: GitHub App installations per workspace.
//   - coding_signing_keys: users' encrypted commit-signing private keys.
func (s *SQLiteStore) ensureCodingSessions(ctx context.Context) error {
	blob, text := "BLOB", "TEXT"
	switch s.dialect {
	case "mysql":
		blob, text = "LONGBLOB", "MEDIUMTEXT"
	case "postgres":
		blob = "BYTEA"
	}
	id := "VARCHAR(191)"
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS coding_environments(id ` + id + ` PRIMARY KEY,workspace_key ` + id + ` NOT NULL,name VARCHAR(255) NOT NULL,code_host VARCHAR(16) NOT NULL,host_name ` + id + ` NOT NULL,repository VARCHAR(512) NOT NULL,active_repo_key ` + id + ` NULL,archived_at BIGINT NOT NULL DEFAULT 0,version BIGINT NOT NULL DEFAULT 1,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS coding_environment_secrets(environment_id ` + id + ` NOT NULL,name ` + id + ` NOT NULL,workspace_key ` + id + ` NOT NULL,ciphertext ` + blob + ` NOT NULL,created_by ` + id + ` NOT NULL DEFAULT '',created_at BIGINT NOT NULL,PRIMARY KEY(environment_id,name))`,
		`CREATE TABLE IF NOT EXISTS coding_environment_snapshots(id ` + id + ` PRIMARY KEY,workspace_key ` + id + ` NOT NULL,environment_id ` + id + ` NOT NULL,status VARCHAR(16) NOT NULL,strategy VARCHAR(16) NOT NULL,config_hash VARCHAR(128) NOT NULL,storage_ref VARCHAR(512) NOT NULL DEFAULT '',logs_key VARCHAR(512) NOT NULL DEFAULT '',attempts INTEGER NOT NULL DEFAULT 0,cancel_requested INTEGER NOT NULL DEFAULT 0,lease_owner ` + id + ` NOT NULL DEFAULT '',lease_until BIGINT NOT NULL DEFAULT 0,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,finished_at BIGINT NOT NULL DEFAULT 0,expires_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS coding_sessions(id ` + id + ` PRIMARY KEY,workspace_key ` + id + ` NOT NULL,agent_session_id ` + id + ` NOT NULL,issue_id ` + id + ` NOT NULL DEFAULT '',environment_id ` + id + ` NOT NULL DEFAULT '',creator_id ` + id + ` NOT NULL,source VARCHAR(32) NOT NULL,source_id ` + id + ` NOT NULL DEFAULT '',idempotency_key ` + id + ` NULL,state VARCHAR(16) NOT NULL,phase VARCHAR(32) NOT NULL,phase_started_at BIGINT NOT NULL,phase_deadline BIGINT NOT NULL DEFAULT 0,attempts INTEGER NOT NULL DEFAULT 0,stop_requested INTEGER NOT NULL DEFAULT 0,error_code VARCHAR(64) NOT NULL DEFAULT '',lease_owner ` + id + ` NOT NULL DEFAULT '',lease_until BIGINT NOT NULL DEFAULT 0,next_reconcile_at BIGINT NOT NULL DEFAULT 0,deadline_at BIGINT NOT NULL DEFAULT 0,cost_micros BIGINT NOT NULL DEFAULT 0,version BIGINT NOT NULL DEFAULT 1,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,ended_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS coding_session_inbox(session_id ` + id + ` NOT NULL,seq BIGINT NOT NULL,workspace_key ` + id + ` NOT NULL,kind VARCHAR(16) NOT NULL,payload ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,delivered_at BIGINT NOT NULL DEFAULT 0,PRIMARY KEY(session_id,seq))`,
		`CREATE TABLE IF NOT EXISTS coding_sandboxes(id ` + id + ` PRIMARY KEY,workspace_key ` + id + ` NOT NULL,session_id ` + id + ` NOT NULL DEFAULT '',snapshot_id ` + id + ` NOT NULL DEFAULT '',kind VARCHAR(16) NOT NULL,provider VARCHAR(32) NOT NULL,provider_ref ` + id + ` NOT NULL DEFAULT '',desired VARCHAR(16) NOT NULL,phase VARCHAR(16) NOT NULL,reason VARCHAR(64) NOT NULL DEFAULT '',size VARCHAR(16) NOT NULL,region ` + id + ` NOT NULL DEFAULT '',attempts INTEGER NOT NULL DEFAULT 0,lease_owner ` + id + ` NOT NULL DEFAULT '',lease_until BIGINT NOT NULL DEFAULT 0,heartbeat_at BIGINT NOT NULL DEFAULT 0,deadline_at BIGINT NOT NULL DEFAULT 0,billed_seconds BIGINT NOT NULL DEFAULT 0,logs_key VARCHAR(512) NOT NULL DEFAULT '',data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,started_at BIGINT NOT NULL DEFAULT 0,ended_at BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS coding_sandbox_tokens(token_hash ` + id + ` PRIMARY KEY,sandbox_id ` + id + ` NOT NULL,session_id ` + id + ` NOT NULL DEFAULT '',workspace_key ` + id + ` NOT NULL,setup_secrets_used INTEGER NOT NULL DEFAULT 0,expires_at BIGINT NOT NULL,revoked_at BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS coding_session_pull_requests(session_id ` + id + ` NOT NULL,code_host VARCHAR(16) NOT NULL,host_name ` + id + ` NOT NULL,repository VARCHAR(512) NOT NULL,number INTEGER NOT NULL,workspace_key ` + id + ` NOT NULL,state VARCHAR(16) NOT NULL,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL,PRIMARY KEY(session_id,code_host,host_name,number))`,
		`CREATE TABLE IF NOT EXISTS coding_session_commits(session_id ` + id + ` NOT NULL,sha VARCHAR(64) NOT NULL,workspace_key ` + id + ` NOT NULL,turn_activity_id ` + id + ` NOT NULL DEFAULT '',data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(session_id,sha))`,
		`CREATE TABLE IF NOT EXISTS coding_session_artifacts(id ` + id + ` PRIMARY KEY,session_id ` + id + ` NOT NULL,workspace_key ` + id + ` NOT NULL,object_key VARCHAR(512) NOT NULL,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS coding_usage_records(id ` + id + ` PRIMARY KEY,workspace_key ` + id + ` NOT NULL,session_id ` + id + ` NOT NULL DEFAULT '',sandbox_id ` + id + ` NOT NULL DEFAULT '',user_id ` + id + ` NOT NULL DEFAULT '',loop_id ` + id + ` NOT NULL DEFAULT '',kind VARCHAR(16) NOT NULL,model ` + id + ` NOT NULL DEFAULT '',input_tokens BIGINT NOT NULL DEFAULT 0,output_tokens BIGINT NOT NULL DEFAULT 0,cache_read_tokens BIGINT NOT NULL DEFAULT 0,cache_write_tokens BIGINT NOT NULL DEFAULT 0,runtime_seconds BIGINT NOT NULL DEFAULT 0,size VARCHAR(16) NOT NULL DEFAULT '',cost_micros BIGINT NOT NULL DEFAULT 0,created_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS code_host_installations(id ` + id + ` PRIMARY KEY,workspace_key ` + id + ` NOT NULL,code_host VARCHAR(16) NOT NULL,host_name ` + id + ` NOT NULL,installation_id ` + id + ` NOT NULL,suspended_at BIGINT NOT NULL DEFAULT 0,data ` + blob + ` NOT NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS coding_signing_keys(user_id ` + id + ` PRIMARY KEY,format VARCHAR(16) NOT NULL,ciphertext ` + blob + ` NOT NULL,fingerprint VARCHAR(255) NOT NULL DEFAULT '',error_text ` + text + ` NULL,created_at BIGINT NOT NULL,updated_at BIGINT NOT NULL)`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS coding_env_active_repo ON coding_environments(workspace_key,active_repo_key)`,
		`CREATE INDEX IF NOT EXISTS coding_env_workspace ON coding_environments(workspace_key,archived_at)`,
		`CREATE INDEX IF NOT EXISTS coding_snapshot_env ON coding_environment_snapshots(workspace_key,environment_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS coding_snapshot_lease ON coding_environment_snapshots(status,lease_until)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS coding_session_anchor ON coding_sessions(workspace_key,agent_session_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS coding_session_idempotency ON coding_sessions(workspace_key,idempotency_key)`,
		`CREATE INDEX IF NOT EXISTS coding_session_due ON coding_sessions(phase,next_reconcile_at)`,
		`CREATE INDEX IF NOT EXISTS coding_session_issue ON coding_sessions(workspace_key,issue_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS coding_session_state ON coding_sessions(workspace_key,state)`,
		`CREATE INDEX IF NOT EXISTS coding_sandbox_session ON coding_sandboxes(workspace_key,session_id)`,
		`CREATE INDEX IF NOT EXISTS coding_sandbox_due ON coding_sandboxes(desired,phase,lease_until)`,
		`CREATE INDEX IF NOT EXISTS coding_sandbox_warm ON coding_sandboxes(kind,size,region,session_id)`,
		`CREATE INDEX IF NOT EXISTS coding_sandbox_token_sandbox ON coding_sandbox_tokens(sandbox_id)`,
		`CREATE INDEX IF NOT EXISTS coding_sandbox_token_expiry ON coding_sandbox_tokens(expires_at)`,
		`CREATE INDEX IF NOT EXISTS coding_pr_lookup ON coding_session_pull_requests(code_host,host_name,repository,number)`,
		`CREATE INDEX IF NOT EXISTS coding_artifact_session ON coding_session_artifacts(workspace_key,session_id)`,
		`CREATE INDEX IF NOT EXISTS coding_usage_workspace ON coding_usage_records(workspace_key,created_at)`,
		`CREATE INDEX IF NOT EXISTS coding_usage_user ON coding_usage_records(workspace_key,user_id,created_at)`,
		`CREATE INDEX IF NOT EXISTS coding_usage_session ON coding_usage_records(session_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS code_host_installation_key ON code_host_installations(code_host,host_name,installation_id,workspace_key)`,
		`CREATE INDEX IF NOT EXISTS code_host_installation_workspace ON code_host_installations(workspace_key,code_host)`,
	} {
		if s.dialect == "mysql" {
			statement = strings.Replace(statement, " IF NOT EXISTS", "", 1)
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !(s.dialect == "mysql" && isDuplicateIndex(err)) {
			return err
		}
	}
	return nil
}
