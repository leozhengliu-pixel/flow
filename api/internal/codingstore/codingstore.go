// Package codingstore is the ONE adapter between *store.SQLiteStore and the
// coding-sessions contracts: coding.CoreStore and coding.AnchorStore (session
// core, track B), sandbox.ReconcilerStore, sandbox.MaintenanceStore and
// sandbox.TokenRevoker (sandbox reconciler, warm pool and GC, track A), and
// coding.SandboxRecordStore (environment sandboxes, track K). The store
// package cannot import internal/coding or internal/sandbox (coding → config
// → store would be an import cycle), so it speaks store-local row types
// (store.CodingSandbox, store.CodingSandboxGrant, ...) and this package
// converts them. Errors need no translation: store and coding both alias the
// domain.ErrCoding* sentinels.
//
// Store embeds *store.SQLiteStore, so every store method whose signature has
// no coding/sandbox types (environments, snapshots, usage, artifacts, pull
// requests, signing keys) is promoted unchanged; cmd/server hands this value
// to every coding hook that needs a store (see codingStore() there).
package codingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"flow/api/internal/coding"
	"flow/api/internal/domain"
	"flow/api/internal/sandbox"
	"flow/api/internal/store"
)

// Store is the coding view of the SQL store.
type Store struct {
	*store.SQLiteStore
}

var (
	_ coding.CoreStore          = Store{}
	_ coding.AnchorStore        = Store{}
	_ coding.SandboxRecordStore = Store{}
	_ sandbox.ReconcilerStore   = Store{}
	_ sandbox.MaintenanceStore  = Store{}
	_ sandbox.TokenRevoker      = Store{}
)

// New wraps a SQL store.
func New(repository *store.SQLiteStore) Store { return Store{SQLiteStore: repository} }

// mapErr only maps a stray sql.ErrNoRows: store and coding errors are the
// same domain.ErrCoding* values.
func mapErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errors.Join(coding.ErrNotFound, err)
	}
	return err
}

func (s Store) CreateCodingSession(ctx context.Context, session domain.CodingSession, anchor domain.AgentTask, idempotencyKey string) (domain.CodingSession, bool, error) {
	saved, created, err := s.SQLiteStore.CreateCodingSession(ctx, session, anchor, idempotencyKey)
	return saved, created, mapErr(err)
}

func (s Store) CodingSession(ctx context.Context, workspaceKey, id string) (domain.CodingSession, error) {
	session, err := s.SQLiteStore.CodingSession(ctx, workspaceKey, id)
	return session, mapErr(err)
}

func (s Store) CodingSessionByAgentSession(ctx context.Context, workspaceKey, agentSessionID string) (domain.CodingSession, error) {
	session, err := s.SQLiteStore.CodingSessionByAgentSession(ctx, workspaceKey, agentSessionID)
	return session, mapErr(err)
}

func (s Store) ListCodingSessions(ctx context.Context, workspaceKey string, filter coding.SessionFilter) ([]domain.CodingSession, error) {
	states := make([]string, 0, len(filter.States))
	for _, state := range filter.States {
		states = append(states, string(state))
	}
	sessions, err := s.SQLiteStore.ListCodingSessions(ctx, workspaceKey, store.CodingSessionFilter{IssueID: filter.IssueID, CreatorID: filter.CreatorID, States: states, Source: filter.Source, Limit: filter.Limit})
	return sessions, mapErr(err)
}

func (s Store) UpdateCodingSession(ctx context.Context, workspaceKey, id string, mutate func(*domain.CodingSession) error) (domain.CodingSession, error) {
	session, err := s.SQLiteStore.UpdateCodingSession(ctx, workspaceKey, id, mutate)
	return session, mapErr(err)
}

func (s Store) LeaseCodingSessions(ctx context.Context, owner string, lease time.Duration, limit int) ([]domain.CodingSession, error) {
	sessions, err := s.SQLiteStore.LeaseCodingSessions(ctx, owner, lease, limit)
	return sessions, mapErr(err)
}

func (s Store) ReleaseCodingSession(ctx context.Context, owner, id string, next time.Time) error {
	return mapErr(s.SQLiteStore.ReleaseCodingSession(ctx, owner, id, next))
}

type inboxPayload struct {
	Turn   *coding.Turn   `json:"turn,omitempty"`
	Signal string         `json:"signal,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
}

func (s Store) EnqueueCodingInbox(ctx context.Context, workspaceKey, sessionID string, item coding.InboxItem) (int64, error) {
	payload, err := json.Marshal(inboxPayload{Turn: item.Turn, Signal: item.Signal, Data: item.Data})
	if err != nil {
		return 0, err
	}
	seq, err := s.SQLiteStore.EnqueueCodingInbox(ctx, workspaceKey, sessionID, item.Kind, payload)
	return seq, mapErr(err)
}

func inboxItems(rows []store.CodingInboxItem, err error) ([]coding.InboxItem, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	items := make([]coding.InboxItem, 0, len(rows))
	for _, row := range rows {
		var payload inboxPayload
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			return nil, err
		}
		items = append(items, coding.InboxItem{Seq: row.Seq, Kind: row.Kind, Turn: payload.Turn, Signal: payload.Signal, Data: payload.Data})
	}
	return items, nil
}

func (s Store) CodingInbox(ctx context.Context, sessionID string, after int64) ([]coding.InboxItem, error) {
	return inboxItems(s.SQLiteStore.CodingInbox(ctx, sessionID, after))
}

func (s Store) UndeliveredCodingInbox(ctx context.Context, sessionID string) ([]coding.InboxItem, error) {
	return inboxItems(s.SQLiteStore.UndeliveredCodingInbox(ctx, sessionID))
}

// SandboxRecord converts a coding_sandboxes row.
func SandboxRecord(row store.CodingSandbox) sandbox.Record {
	return sandbox.Record{
		ID: row.ID, WorkspaceKey: row.WorkspaceKey, SessionID: row.SessionID, SnapshotID: row.SnapshotID,
		Kind: sandbox.Kind(row.Kind), Desired: sandbox.Desired(row.Desired), Phase: sandbox.Phase(row.Phase), Reason: sandbox.Reason(row.Reason),
		Message: row.Message, Image: row.Image, Size: row.Size, Region: row.Region,
		Workspace: sandbox.VolumeSource{VolumeID: row.VolumeID, ArchiveURL: row.ArchiveURL}, Env: row.Env,
		DeadlineAt: row.DeadlineAt, Attempts: row.Attempts, ProviderRef: row.ProviderRef, CreatedAt: row.CreatedAt,
		StartedAt: row.StartedAt, EndedAt: row.EndedAt, HeartbeatAt: row.HeartbeatAt,
		UserID: row.UserID, LoopID: row.LoopID, BilledSeconds: row.BilledSeconds, LogsKey: row.LogsKey, KeepVolume: row.KeepVolume,
	}
}

// SandboxRow converts a sandbox record to a coding_sandboxes row.
func SandboxRow(record sandbox.Record) store.CodingSandbox {
	return store.CodingSandbox{
		ID: record.ID, WorkspaceKey: record.WorkspaceKey, SessionID: record.SessionID, SnapshotID: record.SnapshotID,
		Kind: string(record.Kind), Desired: string(record.Desired), Phase: string(record.Phase), Reason: string(record.Reason),
		Message: record.Message, Image: record.Image, Size: record.Size, Region: record.Region,
		VolumeID: record.Workspace.VolumeID, ArchiveURL: record.Workspace.ArchiveURL, Env: record.Env,
		DeadlineAt: record.DeadlineAt, Attempts: record.Attempts, ProviderRef: record.ProviderRef, CreatedAt: record.CreatedAt,
		StartedAt: record.StartedAt, EndedAt: record.EndedAt, HeartbeatAt: record.HeartbeatAt,
		UserID: record.UserID, LoopID: record.LoopID, BilledSeconds: record.BilledSeconds, LogsKey: record.LogsKey, KeepVolume: record.KeepVolume,
	}
}

func sandboxRecords(rows []store.CodingSandbox, err error) ([]sandbox.Record, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	records := make([]sandbox.Record, len(rows))
	for i, row := range rows {
		records[i] = SandboxRecord(row)
	}
	return records, nil
}

func sandboxRecord(row store.CodingSandbox, err error) (sandbox.Record, error) {
	if err != nil {
		return sandbox.Record{}, mapErr(err)
	}
	return SandboxRecord(row), nil
}

func observation(status sandbox.SandboxStatus) store.CodingSandboxObservation {
	return store.CodingSandboxObservation{
		Phase: string(status.Phase), Reason: string(status.Reason), Message: status.Message, ProviderRef: status.ProviderRef,
		StartedAt: status.StartedAt, FinishedAt: status.FinishedAt,
	}
}

func (s Store) CreateCodingSandbox(ctx context.Context, record sandbox.Record) error {
	return mapErr(s.SQLiteStore.CreateCodingSandbox(ctx, SandboxRow(record)))
}

func (s Store) CodingSandboxes(ctx context.Context, workspaceKey, sessionID string) ([]sandbox.Record, error) {
	return sandboxRecords(s.SQLiteStore.CodingSandboxes(ctx, workspaceKey, sessionID))
}

// CodingSandbox reads one sandbox by id (coding.CoreStore and
// coding.SandboxRecordStore; the latter checks the workspace itself).
func (s Store) CodingSandbox(ctx context.Context, id string) (sandbox.Record, error) {
	return sandboxRecord(s.SQLiteStore.CodingSandbox(ctx, id))
}

func (s Store) SetCodingSandboxDesired(ctx context.Context, id string, desired sandbox.Desired) error {
	return mapErr(s.SQLiteStore.SetCodingSandboxDesired(ctx, id, string(desired)))
}

// BindWarmSandbox claims a running warm sandbox for a session. Its idle time
// is marked as already billed (warm sandboxes are an operator cost), so both
// runtime billers (the sandbox reconciler and the session Meter, which CAS the
// same billed_seconds) start counting at the bind.
func (s Store) BindWarmSandbox(ctx context.Context, size, region, sessionID string) (sandbox.Record, error) {
	return sandboxRecord(s.SQLiteStore.BindWarmCodingSandbox(ctx, size, region, sessionID))
}

// Sandbox reconciler, warm pool and GC (sandbox.ReconcilerStore,
// sandbox.MaintenanceStore). Methods without sandbox types (KnownSandboxIDs,
// WarmSandboxes, AddSandboxBilledSeconds, RecycleWarmSandboxes,
// DeleteExpiredSandboxTokens, SnapshotStates, RevokeSandboxTokens) are
// promoted from the store.

func (s Store) LeaseSandboxes(ctx context.Context, owner string, lease time.Duration, limit int) ([]sandbox.Record, error) {
	return sandboxRecords(s.SQLiteStore.LeaseSandboxes(ctx, owner, lease, limit))
}

func (s Store) SaveSandboxObservation(ctx context.Context, owner, id string, status sandbox.SandboxStatus, attempts int) error {
	return mapErr(s.SQLiteStore.SaveSandboxObservation(ctx, owner, id, observation(status), attempts))
}

func (s Store) SaveSandboxObservationAt(ctx context.Context, owner, id string, status sandbox.SandboxStatus, attempts int, next time.Time) error {
	return mapErr(s.SQLiteStore.SaveSandboxObservationAt(ctx, owner, id, observation(status), attempts, next))
}

func (s Store) MintSandboxToken(ctx context.Context, record sandbox.Record, ttl time.Duration) (string, error) {
	return s.SQLiteStore.MintSandboxToken(ctx, SandboxRow(record), ttl)
}

func (s Store) CreateWarmSandbox(ctx context.Context, record sandbox.Record) error {
	return mapErr(s.SQLiteStore.CreateWarmSandbox(ctx, SandboxRow(record)))
}

func (s Store) SandboxCounts(ctx context.Context) (map[sandbox.Kind]map[sandbox.Phase]int, error) {
	raw, err := s.SQLiteStore.SandboxCounts(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[sandbox.Kind]map[sandbox.Phase]int{}
	for kind, phases := range raw {
		counts[sandbox.Kind(kind)] = map[sandbox.Phase]int{}
		for phase, n := range phases {
			counts[sandbox.Kind(kind)][sandbox.Phase(phase)] = n
		}
	}
	return counts, nil
}

func (s Store) AuthenticateSandboxToken(ctx context.Context, token string) (coding.SandboxGrant, error) {
	grant, err := s.SQLiteStore.AuthenticateSandboxToken(ctx, token)
	if err != nil {
		return coding.SandboxGrant{}, mapErr(err)
	}
	return coding.SandboxGrant{WorkspaceKey: grant.WorkspaceKey, SandboxID: grant.SandboxID, SessionID: grant.SessionID, SnapshotID: grant.SnapshotID, Kind: sandbox.Kind(grant.Kind), ExpiresAt: grant.ExpiresAt, EnvironmentID: grant.EnvironmentID}, nil
}

func (s Store) AddCodingCommits(ctx context.Context, sessionID string, commits []domain.CodingCommit) error {
	return mapErr(s.SQLiteStore.AddCodingCommits(ctx, sessionID, commits))
}

func (s Store) CodingSpendMicros(ctx context.Context, query coding.SpendQuery) (int64, error) {
	total, err := s.SQLiteStore.CodingSpendMicros(ctx, query.WorkspaceKey, query.UserID, query.LoopID, query.Since)
	return total, mapErr(err)
}

func (s Store) RecordCodingUsage(ctx context.Context, record domain.CodingUsageRecord) error {
	return mapErr(s.SQLiteStore.RecordCodingUsage(ctx, record))
}

func (s Store) CodingSandboxBilledSeconds(ctx context.Context, sandboxID string) (int64, error) {
	billed, err := s.SQLiteStore.CodingSandboxBilledSeconds(ctx, sandboxID)
	return billed, mapErr(err)
}
