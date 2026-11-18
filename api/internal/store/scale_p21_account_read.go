package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const accountReadMaxWork = 128 << 20

// AccountRead retains the literal installed account values and their encoding
// allowance. Native viewer and workspace references keep Account's ownership.
type AccountRead struct {
	Account domain.AccountBootstrap
	owner   *ResourceRead
	once    sync.Once
}

func (r *AccountRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Account = domain.AccountBootstrap{}
			r.owner.Close()
		})
	}
}

func (r *AccountRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// BeginAccountRead keeps the count query before the installed catalog lock.
// The complete SQL input is disk staged; only installed memberships enter RAM.
func (s *SQLiteStore) BeginAccountRead(ctx context.Context) (_ *AccountRead, err error) {
	r := &AccountRead{owner: &ResourceRead{store: s}}
	if !s.BoundedMetadataEnabled() {
		r.Account = s.Account()
		return r, nil
	}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	// Account's Background count query never used a caller's SQL snapshot.
	ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{})
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	if err = scratch.reserve(ctx, 4<<20); err != nil {
		return nil, err
	}
	stage, err := sw26NewMigrationStage(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, stage.Close()) }()
	if _, err = stage.exec(ctx, 4096, `CREATE TABLE account_counts(key BLOB PRIMARY KEY,total INTEGER NOT NULL)`); err != nil {
		return nil, err
	}
	valid, err := s.stageAccountCounts(ctx, stage, scratch)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cost, err := s.accountReadWork(ctx)
	if err != nil {
		return nil, err
	}
	if err = r.owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	r.Account = domain.AccountBootstrap{Viewer: s.viewer, Workspaces: []domain.WorkspaceMembership{}, LastWorkspaceKey: s.lastWorkspaceKey}
	for _, data := range s.workspaces {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		count := 0
		if valid {
			err = stage.db.QueryRowContext(ctx, `SELECT total FROM account_counts WHERE key=?`, []byte(data.Workspace.URLKey)).Scan(&count)
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
			}
			if err != nil {
				return nil, err
			}
		}
		joined := data.Workspace.CreatedAt
		if joined.IsZero() {
			joined = time.Now().UTC()
		}
		r.Account.Workspaces = append(r.Account.Workspaces, domain.WorkspaceMembership{Workspace: data.Workspace, Role: "Admin", JoinedAt: joined, IssueCount: count})
	}
	slices.SortFunc(r.Account.Workspaces, func(a, b domain.WorkspaceMembership) int {
		return strings.Compare(strings.ToLower(a.Workspace.Name), strings.ToLower(b.Workspace.Name))
	})
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *SQLiteStore) stageAccountCounts(ctx context.Context, stage *sw26MigrationStage, scratch *ResourceRead) (bool, error) {
	// The count's decimal text, as SQLite's CAST(total AS BLOB) measures it;
	// PostgreSQL has no OCTET_LENGTH(bigint), which failed the scan and staged
	// no counts there.
	total := "total"
	if s.dialect == "postgres" {
		total = "CAST(total AS TEXT)"
	}
	size := "COALESCE(" + scratch.byteLength("workspace_key") + ",0)+COALESCE(" + scratch.byteLength(total) + ",0)"
	rows, err := s.db.QueryContext(ctx, "SELECT "+size+",CASE WHEN "+size+
		"<=? THEN workspace_key ELSE NULL END,CASE WHEN "+size+
		"<=? THEN total ELSE NULL END FROM issue_collection_counts", MetadataRecordPageMaxBytes, MetadataRecordPageMaxBytes)
	if err != nil {
		return false, ctx.Err()
	}
	defer rows.Close()
	for rows.Next() {
		var bytes int64
		var workspace string
		var count int
		scanErr := rows.Scan(&bytes, &workspace, &count)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if bytes < 0 || bytes > MetadataRecordPageMaxBytes {
			return false, ErrMetadataWorkBudget
		}
		if scanErr != nil {
			return false, nil
		}
		if _, err := stage.exec(ctx, int64(len(workspace))+64,
			`INSERT INTO account_counts(key,total) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET total=excluded.total`,
			[]byte(workspace), count); err != nil {
			return false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	// The literal helper returns its accumulated map together with rows.Err,
	// and Account ignores that error. Preserve this staged prefix as well.
	return true, nil
}

func (s *SQLiteStore) accountReadWork(ctx context.Context) (uint64, error) {
	cost := uint64(128 << 10)
	add := func(value any) error {
		native, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(value), 0)
		if err != nil {
			return err
		}
		encoded, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(value), 0)
		if err != nil {
			return err
		}
		part := native + uint64(encoded)*2
		if part > accountReadMaxWork-cost {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	if err := add(s.viewer); err != nil {
		return 0, err
	}
	// These native references are borrowed even when absent from JSON or
	// beyond a slice's visible length.
	if err := add(s.viewer.Via); err != nil {
		return 0, err
	}
	for _, values := range [][]string{s.viewer.AppScopes, s.viewer.AppTeamIDs} {
		if err := add(values[:cap(values)]); err != nil {
			return 0, err
		}
	}
	if err := add(s.lastWorkspaceKey); err != nil {
		return 0, err
	}
	for _, data := range s.workspaces {
		membership := domain.WorkspaceMembership{Workspace: data.Workspace, Role: "Admin", JoinedAt: data.Workspace.CreatedAt}
		if err := add(membership); err != nil {
			return 0, err
		}
	}
	return cost, nil
}
