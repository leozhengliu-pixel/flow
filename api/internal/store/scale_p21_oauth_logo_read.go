package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
)

type oauthLogoWorkspace struct {
	key    string
	source *metadataLogicalSource
}

// VisitOAuthLogoInstallations detaches the original installation order before
// callbacks perform membership SQL, including with a one-connection pool.
func (s *SQLiteStore) VisitOAuthLogoInstallations(ctx context.Context, clientID string, visit func(string) bool) (bool, error) {
	if visit == nil {
		return false, ErrMetadataRecordQuery
	}
	length := "OCTET_LENGTH(workspace_key)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(workspace_key AS BLOB))"
	}
	tx, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var size int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX("+length+"),0) FROM application_installations WHERE client_id=?", clientID).Scan(&size); err != nil {
		return false, err
	}
	if size < 0 || size > (metadataWorkBytes-(128<<10))/4 {
		return false, ErrMetadataWorkBudget
	}
	_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, int(size)*4+128<<10)
	if err != nil {
		return false, err
	}
	defer release()
	spool, err := newMetadataLogicalRecordSpool(metadataLogicalSourceScratchContext(ctx, nil))
	if err != nil {
		return false, err
	}
	defer spool.Close()
	rows, err := tx.QueryContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN workspace_key ELSE NULL END FROM application_installations WHERE client_id=? ORDER BY workspace_key,id", size, clientID)
	if err != nil {
		return false, err
	}
	err = func() error {
		defer rows.Close()
		var header [8]byte
		for rows.Next() {
			var workspace sql.NullString
			if err := rows.Scan(&workspace); err != nil {
				return err
			}
			if !workspace.Valid {
				return ErrMetadataWorkBudget
			}
			binary.LittleEndian.PutUint64(header[:], uint64(len(workspace.String)))
			if _, err := spool.Write(header[:]); err != nil {
				return err
			}
			if _, err := io.WriteString(spool, workspace.String); err != nil {
				return err
			}
		}
		return rows.Err()
	}()
	if err != nil {
		return false, err
	}
	if err := tx.Rollback(); err != nil {
		return false, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	var header [8]byte
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(spool, header[:]); errors.Is(err, io.EOF) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		length := binary.LittleEndian.Uint64(header[:])
		if length > uint64(size) {
			return false, ErrMetadataRecordQuery
		}
		key := make([]byte, int(length))
		if _, err := io.ReadFull(spool, key); err != nil {
			return false, err
		}
		if visit(string(key)) {
			return true, nil
		}
	}
}

// OAuthApplicationLogoMatch is borrowed only during its visitor callback.
// ClientID is decoded only when the permission decision needs it.
type OAuthApplicationLogoMatch struct {
	WorkspaceKey  string
	PublicEnabled bool
	store         *SQLiteStore
	client        metadataLogicalNode
	live          *atomic.Bool
	ctx           context.Context
}

func (m OAuthApplicationLogoMatch) WithClientID(visit func(string) error) error {
	if visit == nil || m.live == nil || !m.live.Load() {
		return ErrMetadataRecordQuery
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	if m.client.cost > metadataWorkBytes-(64<<10) {
		return ErrMetadataWorkBudget
	}
	_, release, err := m.store.tryAdmitAdditionalMetadataWork(m.ctx, int(m.client.cost)+64<<10)
	if err != nil {
		return err
	}
	defer release()
	value, err := m.client.Value(m.ctx, int(m.client.cost))
	if err != nil {
		return err
	}
	if !m.live.Load() || value.Kind() != reflect.String {
		return ErrMetadataRecordQuery
	}
	if err := visit(value.String()); err != nil {
		return err
	}
	return m.ctx.Err()
}

// VisitOAuthApplicationLogos captures one atomic installed registry generation.
// It visits exact native logo matches in each original collection's order,
// including duplicates. A nil workspace selects every physical registry key.
// The callback returns true to stop. Nothing from match may escape the callback.
func (s *SQLiteStore) VisitOAuthApplicationLogos(ctx context.Context, logoURL string, workspace *string, visit func(OAuthApplicationLogoMatch) (bool, error)) (matched bool, err error) {
	if !s.BoundedMetadataEnabled() || visit == nil {
		return false, ErrMetadataRecordQuery
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	var sources []oauthLogoWorkspace
	var release func()
	err = func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()
		r := s.metadataResidency
		r.mu.Lock()
		defer r.mu.Unlock()
		cost, count := 64<<10, 0
		for key := range s.workspaces {
			if workspace != nil && key != *workspace {
				continue
			}
			if len(key) > (metadataWorkBytes-cost-256)/8 {
				return ErrMetadataWorkBudget
			}
			cost += 256 + len(key)*8
			count++
		}
		var err error
		_, release, err = s.tryAdmitAdditionalMetadataWork(ctx, cost)
		if err != nil {
			return err
		}
		sources = make([]oauthLogoWorkspace, 0, count)
		for key := range s.workspaces {
			if err := ctx.Err(); err != nil {
				return err
			}
			if workspace != nil && key != *workspace {
				continue
			}
			source := r.logical[key]
			if source == nil {
				return ErrBoundedMetadataCoverage
			}
			if err := source.Retain(); err != nil {
				return err
			}
			sources = append(sources, oauthLogoWorkspace{strings.Clone(key), source})
		}
		return nil
	}()
	defer func() {
		for _, item := range sources {
			item.source.Release()
		}
		if release != nil {
			release()
		}
	}()
	if err != nil {
		return false, err
	}
	_, releaseWork, err := s.tryAdmitAdditionalMetadataWork(ctx, 128<<10)
	if err != nil {
		return false, err
	}
	defer releaseWork()
	field, _, _ := metadataLogicalField("oauthApplications")
	stop := errors.New("oauth logo visitor stopped")
	for _, item := range sources {
		err = item.source.fields[field].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			var logo, client metadataLogicalNode
			public := false
			err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				switch key.String() {
				case "LogoURL":
					logo = node
				case "ClientID":
					client = node
				case "PublicEnabled":
					value, err := node.Value(ctx, 1024)
					if err != nil {
						return err
					}
					public = value.Bool()
				}
				return nil
			})
			if err != nil {
				return err
			}
			if logo.typ == nil || client.typ == nil {
				return ErrBoundedMetadataCoverage
			}
			equal, err := logo.StringEqual(ctx, logoURL)
			if err != nil || !equal {
				return err
			}
			matched = true
			live := &atomic.Bool{}
			live.Store(true)
			defer live.Store(false)
			done, err := visit(OAuthApplicationLogoMatch{WorkspaceKey: item.key, PublicEnabled: public, store: s, client: client, live: live, ctx: ctx})
			if err != nil {
				return err
			}
			if done {
				return stop
			}
			return nil
		})
		if errors.Is(err, stop) {
			return matched, nil
		}
		if err != nil {
			return matched, err
		}
	}
	return matched, ctx.Err()
}
