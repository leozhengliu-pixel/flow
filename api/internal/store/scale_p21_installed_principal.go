package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
)

// InstalledPrincipalRead proves only active workspace membership. It does not
// authorize any team/content projection and never borrows source policy.
type InstalledPrincipalRead struct {
	mu                  sync.Mutex
	read                *InstalledMetadataRead
	workspaceID         string
	principal, viewerID string
	epoch               string
	key                 *domain.APIKey
	releases            []func()
	output              *OwnedMetadataReduction
	closed, used        bool
}

type PreparedPrincipalProjection struct{ owner *InstalledPrincipalRead }

// InstalledPrincipalIdentity distinguishes the original filter parameter
// from the live SQL viewer identity. Both strings share the owner's lifetime.
type InstalledPrincipalIdentity struct {
	PrincipalID string
	ViewerID    string
}

func metadataPrincipalCredentialWork(key *domain.APIKey) (int, error) {
	cost := uint64(64 << 10)
	add := func(length int) error {
		part := uint64(length)*8 + 256
		if part > metadataWorkBytes || cost > metadataWorkBytes-part {
			return ErrMetadataWorkBudget
		}
		cost += part
		return nil
	}
	if key != nil {
		for _, value := range []string{key.ID, key.Actor, key.TeamRestriction} {
			if err := add(len(value)); err != nil {
				return 0, err
			}
		}
		for _, values := range [][]string{key.Scopes, key.TeamIDs} {
			for _, value := range values {
				if err := add(len(value)); err != nil {
					return 0, err
				}
			}
		}
	}
	return int(cost), nil
}

func metadataPrincipalCredentialClone(key *domain.APIKey) *domain.APIKey {
	if key == nil {
		return nil
	}
	result := resourceAccessCredential(key)
	result.ID, result.Actor, result.TeamRestriction = strings.Clone(result.ID), strings.Clone(result.Actor), strings.Clone(result.TeamRestriction)
	for _, values := range [][]string{result.Scopes, result.TeamIDs} {
		for i := range values {
			values[i] = strings.Clone(values[i])
		}
	}
	return result
}

func metadataPrincipalCredentialEqual(a, b *domain.APIKey) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Actor == b.Actor && a.TeamRestriction == b.TeamRestriction &&
		(a.Scopes == nil) == (b.Scopes == nil) && (a.TeamIDs == nil) == (b.TeamIDs == nil) &&
		slices.Equal(a.Scopes, b.Scopes) && slices.Equal(a.TeamIDs, b.TeamIDs)
}

func metadataPrincipalByteLength(s *SQLiteStore, column string) string {
	if s.dialect == "sqlite" {
		return "LENGTH(CAST(" + column + " AS BLOB))"
	}
	return "OCTET_LENGTH(" + column + ")"
}

func (s *SQLiteStore) BeginInstalledPrincipalRead(ctx context.Context, workspace, principal string, key *domain.APIKey) (_ *InstalledPrincipalRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() || principal == "" {
		return nil, false, ErrMetadataRecordQuery
	}
	if strings.HasPrefix(principal, "app_") {
		return nil, true, ErrBoundedMetadataCoverage
	}
	if !s.db.journalReady.Load() {
		return nil, true, ErrRecordJournalUnavailable
	}
	read, found, err := s.retainInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	r := &InstalledPrincipalRead{read: read}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	cost, err := metadataPrincipalCredentialWork(key)
	if err != nil || len(principal) > (metadataWorkBytes-cost)/8 {
		return nil, true, ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, cost+len(principal)*8)
	if err != nil {
		return nil, true, err
	}
	r.releases = append(r.releases, release)
	r.principal, r.key = strings.Clone(principal), metadataPrincipalCredentialClone(key)
	stamp, epoch, err := s.accessStampAndEpoch(ctx, read.workspace)
	if err != nil {
		return nil, true, err
	}
	source, err := read.retainSource(ctx, read.workspace)
	if err != nil {
		return nil, true, err
	}
	origin := metadataLogicalOrigin{workspace: read.workspace, stamp: source.Stamp()}
	source.Release()
	if err := s.validateMetadataLogicalOrigin(ctx, metadataLogicalOrigin{workspace: read.workspace, stamp: stamp}, origin.stamp.workspaceID); err != nil {
		return nil, true, err
	}
	err = read.Child(ctx, []string{"workspace", "id"}, func(ctx context.Context, shape InstalledMetadataShape, ref InstalledMetadataRef) error {
		if !shape.Found {
			return ErrResourceAccessChanged
		}
		return ref.WithValue(ctx, 0, func(value any) error {
			return s.validateMetadataLogicalOrigin(ctx, origin, value.(string))
		})
	})
	if err != nil {
		return nil, true, err
	}
	// Only scalar lengths are read before reserving the returned identity.
	// This path deliberately never joins or materializes team memberships.
	const membership = ` FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=? AND m.user_id=?`
	var idBytes, statusBytes int64
	err = s.db.QueryRowContext(ctx, `SELECT `+metadataPrincipalByteLength(s, "u.id")+`,`+metadataPrincipalByteLength(s, "m.status")+membership,
		stamp.workspaceID, principal).Scan(&idBytes, &statusBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, true, ErrAuthForbidden
	}
	if err != nil {
		return nil, true, err
	}
	if idBytes < 0 || statusBytes < 0 || idBytes > metadataWorkBytes/8 || statusBytes > metadataWorkBytes/8-idBytes {
		return nil, true, ErrMetadataWorkBudget
	}
	_, release, err = s.tryAdmitAdditionalMetadataWork(ctx, int(idBytes+statusBytes)*8+64<<10)
	if err != nil {
		return nil, true, err
	}
	r.releases = append(r.releases, release)
	var actualID, actualStatus sql.NullString
	query := `SELECT CASE WHEN ` + metadataPrincipalByteLength(s, "u.id") + `<=? THEN u.id END,CASE WHEN ` +
		metadataPrincipalByteLength(s, "m.status") + `<=? THEN m.status END` + membership
	if err := s.db.QueryRowContext(ctx, query, idBytes, statusBytes, stamp.workspaceID, principal).Scan(&actualID, &actualStatus); err != nil {
		return nil, true, err
	}
	if !actualID.Valid || !actualStatus.Valid {
		return nil, true, ErrResourceAccessChanged
	}
	r.viewerID = actualID.String
	if actualStatus.String != "active" || r.viewerID == "" {
		return nil, true, ErrAuthForbidden
	}
	r.workspaceID, r.epoch = stamp.workspaceID, epoch
	if err := r.validate(ctx); err != nil {
		return nil, true, err
	}
	return r, true, nil
}

func (r *InstalledPrincipalRead) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	epoch, err := r.read.store.AuthzEpoch(ctx, r.workspaceID)
	if err != nil {
		return err
	}
	if epoch != r.epoch {
		return ErrAuthForbidden
	}
	return nil
}

// Prepare is trusted storage work under this active-principal lifetime.
// The callback must preserve the caller's original owner filtering and may
// not emit, retain refs, or close the supplied read. Source policy is absent.
// Its principal is the original parameter, not a SQL-normalized replacement.
// This authority primitive does not reproduce BootstrapForUser's unrelated
// hydration, display decoration or favorite-merging failure boundaries.
func (r *InstalledPrincipalRead) Prepare(ctx context.Context, resultWorkBytes int, prepare func(context.Context, *InstalledMetadataRead, string) (any, error)) (*PreparedPrincipalProjection, error) {
	if prepare == nil {
		return nil, ErrMetadataRecordQuery
	}
	return r.PrepareWithIdentity(ctx, resultWorkBytes, func(ctx context.Context, read *InstalledMetadataRead, identity InstalledPrincipalIdentity) (any, error) {
		return prepare(ctx, read, identity.PrincipalID)
	})
}

// PrepareWithIdentity supports original consumers that filter by Viewer.ID
// rather than the principal parameter. It does not equate the two identities.
func (r *InstalledPrincipalRead) PrepareWithIdentity(ctx context.Context, resultWorkBytes int, prepare func(context.Context, *InstalledMetadataRead, InstalledPrincipalIdentity) (any, error)) (*PreparedPrincipalProjection, error) {
	if r == nil || prepare == nil {
		return nil, ErrMetadataRecordQuery
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.output != nil {
		return nil, errMetadataLogicalClosed
	}
	if err := r.validate(ctx); err != nil {
		return nil, err
	}
	output, err := r.read.Reduce(ctx, resultWorkBytes, func(ctx context.Context) (any, error) {
		return prepare(ctx, r.read, InstalledPrincipalIdentity{PrincipalID: r.principal, ViewerID: r.viewerID})
	})
	if err != nil {
		return nil, err
	}
	r.output = output
	return &PreparedPrincipalProjection{owner: r}, nil
}

func (p *PreparedPrincipalProjection) Publish(ctx context.Context, workspace, principal string, key *domain.APIKey, emit func(context.Context, any) error) error {
	if p == nil || p.owner == nil || emit == nil {
		return ErrMetadataRecordQuery
	}
	r := p.owner
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.output == nil || r.used {
		return errMetadataLogicalClosed
	}
	r.used = true
	if workspace != r.read.workspace || principal != r.principal || !metadataPrincipalCredentialEqual(r.key, key) {
		return ErrAuthForbidden
	}
	if err := r.validate(ctx); err != nil {
		return err
	}
	return emit(ctx, r.output.Data)
}

func (p *PreparedPrincipalProjection) Close() {
	if p != nil && p.owner != nil {
		p.owner.Close()
	}
}

func (r *InstalledPrincipalRead) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	read, output, releases := r.read, r.output, r.releases
	r.read, r.output, r.releases, r.key = nil, nil, nil, nil
	r.workspaceID, r.principal, r.viewerID, r.epoch = "", "", "", ""
	r.mu.Unlock()
	if output != nil {
		output.Close()
	}
	if read != nil {
		read.Close()
	}
	for _, release := range releases {
		release()
	}
}
