package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

type recurringNormalizationTimezone struct {
	value string
	owner *ResourceRead
}

// RecurringNormalizationRead owns the normalizer's sparse index IDs and its
// first installed timezone lookup for each exact physical team-settings key.
type RecurringNormalizationRead struct {
	IDs       []string
	store     *SQLiteStore
	workspace string
	owner     *ResourceRead
	timezones map[string]recurringNormalizationTimezone
	mu        sync.Mutex
	closed    bool
}

func (r *RecurringNormalizationRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	r.IDs = nil
	for _, timezone := range r.timezones {
		timezone.owner.Close()
	}
	r.timezones = nil
	r.workspace = ""
	r.owner.Close()
}

// BeginRecurringNormalizationRead preserves RecurringIssueIDs with no due
// cutoff and the original 10000-row limit. Length admission and ID transfer
// share one repeatable SQL snapshot.
func (s *SQLiteStore) BeginRecurringNormalizationRead(ctx context.Context, workspace string) (_ *RecurringNormalizationRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	if uint64(len(workspace)) > (metadataWorkBytes-(128<<10))/8 {
		return nil, ErrMetadataWorkBudget
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	read := &RecurringNormalizationRead{store: s, owner: &ResourceRead{store: s}}
	defer func() {
		if err != nil {
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			}
			read.Close()
		}
	}()
	if err = read.owner.reserve(ctx, 128<<10+uint64(len(workspace))*8); err != nil {
		return nil, err
	}
	read.workspace = strings.Clone(workspace)
	read.timezones = map[string]recurringNormalizationTimezone{}
	selection := &IssueWriteAuthorizationRead{owner: read.owner, query: IssueRecordQuery{Workspace: read.workspace}}
	ctx, tx, closeSnapshot, err := selection.rowSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer closeSnapshot()
	query := recurringIssueIDQuery(false)
	args := []any{read.workspace, 10000}
	var count, size int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+read.owner.byteLength("issue_id")+"),0) FROM ("+query+") recurring_normalization_ids", args...).Scan(&count, &size); err != nil {
		return nil, err
	}
	if count < 0 || count > 10000 || size < 0 || size > metadataWorkBytes/8 ||
		uint64(count)*64+uint64(size)*8 > metadataWorkBytes-(64<<10) {
		return nil, ErrMetadataWorkBudget
	}
	if err = read.owner.reserve(ctx, uint64(count)*64+uint64(size)*8+64<<10); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	read.IDs = make([]string, 0, count)
	var transferred int64
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		transferred += int64(len(id))
		if int64(len(read.IDs)) >= count || transferred > size {
			_ = rows.Close()
			return nil, ErrMetadataWorkBudget
		}
		read.IDs = append(read.IDs, id)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if int64(len(read.IDs)) != count || transferred != size {
		return nil, ErrMetadataRecordQuery
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

// Page owns the original trusted first summary page, with no principal or
// policy restrictions. The returned headers have an independent lifetime.
func (r *RecurringNormalizationRead) Page(ctx context.Context, ids []string) (_ *IssueWriteAuthorizationRead, err error) {
	if r == nil {
		return nil, errMetadataLogicalClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, errMetadataLogicalClosed
	}
	if len(ids) > 200 {
		return nil, ErrIssueQuery
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read := &IssueWriteAuthorizationRead{
		owner: &ResourceRead{store: r.store},
		query: IssueRecordQuery{
			Workspace: r.workspace, RestrictToIssueIDs: true,
			Archived: "all", Summary: true, Limit: 500,
		},
		recurrenceTiming: true,
	}
	defer func() {
		if err != nil {
			if canceled := ctx.Err(); canceled != nil {
				err = canceled
			}
			read.Close()
		}
	}()
	cost := uint64(len(ids))*32 + 128<<10
	if uint64(len(r.workspace)) > (metadataWorkBytes-cost)/8 {
		return nil, ErrMetadataWorkBudget
	}
	cost += uint64(len(r.workspace)) * 8
	for _, id := range ids {
		if uint64(len(id)) > (metadataWorkBytes-cost)/8 {
			return nil, ErrMetadataWorkBudget
		}
		cost += uint64(len(id)) * 8
	}
	if err = read.owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	read.query.Workspace = strings.Clone(read.query.Workspace)
	read.query.IssueIDs = slices.Clone(ids)
	var found bool
	read.installed, found, err = r.store.BeginInstalledMetadataRead(ctx, r.workspace)
	if err != nil {
		return nil, err
	}
	if found {
		read.owner.source, err = read.installed.retainSource(ctx, read.installed.workspace)
		if err != nil {
			return nil, err
		}
	}
	if err = read.loadIssueHeaders(ctx); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return read, nil
}

// TeamTimezone preserves TeamSettingsFor's fresh contextless lookup. Cache
// entries, projected strings, source owners and location-processing work stay
// admitted until the normalizer closes, including cached missing keys.
func (r *RecurringNormalizationRead) TeamTimezone(teamID string) (_ string, err error) {
	if r == nil {
		return "", errMetadataLogicalClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", errMetadataLogicalClosed
	}
	if cached, found := r.timezones[teamID]; found {
		return cached.value, nil
	}
	if uint64(len(teamID)) > (metadataWorkBytes-(128<<10))/8 {
		return "", ErrMetadataWorkBudget
	}
	ctx := context.Background()
	owner := &ResourceRead{store: r.store}
	defer func() {
		if err != nil {
			owner.Close()
		}
	}()
	// Covers the cache key/map entry and the caller's parsed location/scratch.
	if err = owner.reserve(ctx, 128<<10+uint64(len(teamID))*8); err != nil {
		return "", err
	}
	installed, found, err := r.store.BeginInstalledMetadataRead(ctx, r.workspace)
	if err != nil {
		return "", err
	}
	defer installed.Close()
	timezone := ""
	if found {
		owner.source, err = installed.retainSource(ctx, installed.workspace)
		if err != nil {
			return "", err
		}
		index, _, _ := metadataLogicalField("teamSettings")
		directory := &DocumentDirectoryRead{owner: owner}
		err = owner.source.fields[index].MapEntries(ctx, func(key, row metadataLogicalNode) error {
			match, err := key.StringEqual(ctx, teamID)
			if err != nil || !match {
				return err
			}
			value, err := directory.header(ctx, row, []string{"Timezone"})
			if err == nil {
				timezone = value.Interface().(domain.TeamSettings).Timezone
			}
			return err
		})
		if err != nil {
			return "", err
		}
	}
	r.timezones[strings.Clone(teamID)] = recurringNormalizationTimezone{value: timezone, owner: owner}
	return timezone, nil
}
