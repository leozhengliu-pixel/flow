package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const applicationAccessMaxWork = 64 << 20

// ApplicationAccessRead owns the installation and every value shared with User.
// Keep it open while derived credentials, actors or response values are in use.
type ApplicationAccessRead struct {
	Installation domain.ApplicationInstallation
	User         domain.User
	store        *SQLiteStore
	work         MemberDirectoryRead
	once         sync.Once
}

func (r *ApplicationAccessRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Installation = domain.ApplicationInstallation{}
			r.User = domain.User{}
			r.work.Close()
		})
	}
}

func (r *ApplicationAccessRead) charge(ctx context.Context, cost int64) error {
	if cost < 0 || cost > applicationAccessMaxWork {
		return ErrMetadataWorkBudget
	}
	return r.work.charge(ctx, r.store, int(cost))
}

// BeginApplicationAccessRead preserves the installation JSON identities and the
// installed home/global policy generation. Projections are user, delivery, full.
// It borrows an enclosing read transaction so a pool-one caller never nests SQL.
func (s *SQLiteStore) BeginApplicationAccessRead(ctx context.Context, userID, projection string) (_ *ApplicationAccessRead, err error) {
	if projection != "user" && projection != "delivery" && projection != "full" {
		return nil, ErrMetadataRecordQuery
	}
	r := &ApplicationAccessRead{store: s}
	if !s.BoundedMetadataEnabled() {
		r.Installation, err = s.ApplicationByUser(ctx, userID)
		r.User = r.Installation.User()
		return r, err
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	defer func() {
		if err != nil {
			r.Close()
			recordMetadataOutputError(ctx, err)
		}
	}()
	if err = r.charge(ctx, 256<<10); err != nil {
		return nil, err
	}
	var tx *sqlTx
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if snapshot.reader == nil {
			return nil, ErrMetadataRecordQuery
		}
		tx = snapshot.reader
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	var size int64
	if err = tx.QueryRowContext(ctx, "SELECT "+s.scaleP21SW16AttachmentBytes("data")+" FROM application_installations WHERE user_id=?", userID).Scan(&size); err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, ErrMetadataRecordQuery
	}
	input := &memberDirectoryInstallationReader{ctx: ctx, store: s, tx: tx, id: userID, size: size}
	ranges, err := r.installation(ctx, input, size, projection)
	if err != nil {
		return nil, err
	}
	home, sources, err := r.sources(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, source := range sources {
			source.Release()
		}
	}()
	if err = r.teams(ctx, home); err != nil {
		return nil, err
	}
	found, err := r.global(ctx, sources)
	if err != nil {
		return nil, err
	}
	if !found {
		for _, field := range []string{"Name", "AvatarURL"} {
			if part, ok := ranges[field]; ok {
				if err = r.stringAt(ctx, input, part, reflect.ValueOf(&r.Installation).Elem().FieldByName(field)); err != nil {
					return nil, err
				}
			}
		}
		if strings.HasPrefix(r.Installation.ClientID, "flow_client_") {
			r.Installation.Active = false
		}
	}
	r.User = r.Installation.User()
	return r, ctx.Err()
}

// Strings retain the last non-null occurrence. Selected arrays decode every
// occurrence into their previous typed slice: null elements reuse old slots.
func (r *ApplicationAccessRead) installation(ctx context.Context, input io.ReaderAt, size int64, projection string) (map[string]metadataCollectionRange, error) {
	ranges := map[string]metadataCollectionRange{}
	typ := reflect.TypeFor[domain.ApplicationInstallation]()
	target := reflect.ValueOf(&r.Installation).Elem()
	err := sw17VisitCredentialFields(ctx, input, size, typ, func(index int, part metadataCollectionRange) error {
		field := typ.Field(index)
		reader := io.NewSectionReader(input, part.offset, part.size)
		selected := projection == "full" || !slices.Contains([]string{"ID", "InstalledBy", "WebhookURL", "CreatedAt", "UpdatedAt"}, field.Name)
		if projection == "delivery" && (field.Name == "ID" || field.Name == "WebhookURL") {
			selected = true
		}
		switch field.Type {
		case reflect.TypeFor[string]():
			null, err := VisitMetadataJSONStringChunks(ctx, reader, func([]byte) error { return nil })
			if err == nil && !null && selected {
				ranges[field.Name] = part
			}
			return err
		case reflect.TypeFor[bool]():
			if part.size > 5 {
				return ErrMetadataRecordQuery
			}
			return json.NewDecoder(reader).Decode(target.Field(index).Addr().Interface())
		case reflect.TypeFor[time.Time]():
			value, err := scaleP21SW23TeamTime(ctx, reader)
			if err == nil && selected && value != nil {
				target.Field(index).Set(reflect.ValueOf(*value))
			}
			return err
		case reflect.TypeFor[[]string]():
			scanner := sw17CredentialScanner(ctx, input, part.offset, part.size)
			first, err := scanner.peek()
			if err != nil {
				return err
			}
			if first == 'n' {
				if err := scanner.literal("null"); err != nil {
					return err
				}
			} else if first == '[' {
				if err := scanner.array(func(item metadataCollectionRange) error {
					_, err := VisitMetadataJSONStringChunks(ctx, io.NewSectionReader(input, item.offset, item.size), func([]byte) error { return nil })
					return err
				}, 0); err != nil {
					return err
				}
			} else {
				return ErrMetadataRecordQuery
			}
			return nil
		default:
			return ErrBoundedMetadataCoverage
		}
	})
	if err != nil {
		return nil, err
	}
	err = sw17VisitCredentialFields(ctx, input, size, typ, func(index int, part metadataCollectionRange) error {
		field := typ.Field(index)
		if field.Type != reflect.TypeFor[[]string]() || projection != "full" && field.Name == "TeamIDs" && r.Installation.AllTeams {
			return nil
		}
		if part.size > applicationAccessMaxWork/16 {
			return ErrMetadataWorkBudget
		}
		if err := r.charge(ctx, part.size*16+512); err != nil {
			return err
		}
		scanner := sw17CredentialScanner(ctx, input, part.offset, part.size)
		if first, err := scanner.peek(); err != nil {
			return err
		} else if first == '[' {
			if err := scanner.array(func(metadataCollectionRange) error { return r.charge(ctx, 128) }, 0); err != nil {
				return err
			}
		}
		return json.NewDecoder(io.NewSectionReader(input, part.offset, part.size)).Decode(target.Field(index).Addr().Interface())
	})
	if err != nil {
		return nil, err
	}
	for name, part := range ranges {
		if name != "Name" && name != "AvatarURL" {
			if err := r.stringAt(ctx, input, part, target.FieldByName(name)); err != nil {
				return nil, err
			}
		}
	}
	return ranges, nil
}

func (r *ApplicationAccessRead) stringAt(ctx context.Context, input io.ReaderAt, part metadataCollectionRange, target reflect.Value) error {
	if part.size > applicationAccessMaxWork/16 {
		return ErrMetadataWorkBudget
	}
	if err := r.charge(ctx, part.size*16+512); err != nil {
		return err
	}
	return json.NewDecoder(io.NewSectionReader(input, part.offset, part.size)).Decode(target.Addr().Interface())
}

func (r *ApplicationAccessRead) sources(ctx context.Context) (home *metadataLogicalSource, sources []*metadataLogicalSource, err error) {
	s := r.store
	s.mu.RLock()
	defer s.mu.RUnlock()
	resident := s.metadataResidency
	resident.mu.Lock()
	defer resident.mu.Unlock()
	if err := r.charge(ctx, int64(len(s.workspaces))*256); err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			for _, source := range sources {
				source.Release()
			}
		}
	}()
	for key := range s.workspaces {
		source := resident.logical[key]
		if source == nil {
			return nil, sources, ErrBoundedMetadataCoverage
		}
		if err := source.Retain(); err != nil {
			return nil, sources, err
		}
		sources = append(sources, source)
		if key == r.Installation.WorkspaceKey {
			home = source
		}
	}
	return home, sources, nil
}

func applicationAccessField(source *metadataLogicalSource, field string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(field)
	return source.fields[index]
}

func applicationAccessChild(ctx context.Context, record metadataLogicalNode, name string) (metadataLogicalNode, error) {
	var result metadataLogicalNode
	err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
		if key.String() == name {
			result = node
		}
		return nil
	})
	if err == nil && result.typ == nil {
		err = ErrBoundedMetadataCoverage
	}
	return result, err
}

func (r *ApplicationAccessRead) value(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	if node.cost > applicationAccessMaxWork/16 {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	if err := r.charge(ctx, int64(node.cost)*16+512); err != nil {
		return reflect.Value{}, err
	}
	return node.Value(ctx, int(node.cost))
}

func (r *ApplicationAccessRead) teams(ctx context.Context, source *metadataLogicalSource) error {
	teams := []domain.Team(nil)
	settings := map[string]domain.TeamSettings{}
	if source == nil {
		r.Installation.ResolveTeamAccess(teams, settings)
		return nil
	}
	err := applicationAccessField(source, "teams").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		if err := r.charge(ctx, 4096); err != nil {
			return err
		}
		var team domain.Team
		if err := record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
			switch key.String() {
			case "ID", "Private":
				value, err := r.value(ctx, node)
				if err != nil {
					return err
				}
				reflect.ValueOf(&team).Elem().FieldByName(key.String()).Set(value)
			case "ArchivedAt", "RetiredAt":
				return node.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error {
					value := new(time.Time)
					reflect.ValueOf(&team).Elem().FieldByName(key.String()).Set(reflect.ValueOf(value))
					return nil
				})
			}
			return nil
		}); err != nil {
			return err
		}
		teams = append(teams, team)
		return nil
	})
	if err != nil {
		return err
	}
	// Load only settings reachable from actual team IDs. Map keys remain framed,
	// including unrelated legal keys too large for generic Children.
	var load func(string) error
	load = func(id string) error {
		if id == "" {
			return nil
		}
		if _, found := settings[id]; found {
			return nil
		}
		if err := r.charge(ctx, int64(len(id))*16+4096); err != nil {
			return err
		}
		settings[id] = domain.TeamSettings{}
		var policy domain.TeamSettings
		err := applicationAccessField(source, "teamSettings").MapEntries(ctx, func(key, record metadataLogicalNode) error {
			equal, err := key.StringEqual(ctx, id)
			if err != nil || !equal {
				return err
			}
			return record.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				if key.String() != "ParentTeamID" && key.String() != "Access" {
					return nil
				}
				value, err := r.value(ctx, node)
				if err == nil {
					reflect.ValueOf(&policy).Elem().FieldByName(key.String()).Set(value)
				}
				return err
			})
		})
		if err != nil {
			return err
		}
		settings[id] = policy
		return load(policy.ParentTeamID)
	}
	if r.Installation.AllPublicTeams && !r.Installation.AllTeams {
		for _, team := range teams {
			if err := load(team.ID); err != nil {
				return err
			}
		}
	}
	r.Installation.ResolveTeamAccess(teams, settings)
	return nil
}

func (r *ApplicationAccessRead) global(ctx context.Context, sources []*metadataLogicalSource) (bool, error) {
	found := false
	var name, logo metadataLogicalNode
	for _, source := range sources {
		err := applicationAccessField(source, "oauthApplications").Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			client, err := applicationAccessChild(ctx, record, "ClientID")
			if err != nil {
				return err
			}
			equal, err := client.StringEqual(ctx, r.Installation.ClientID)
			if err != nil || !equal {
				return err
			}
			found = true
			name, err = applicationAccessChild(ctx, record, "Name")
			if err != nil {
				return err
			}
			logo, err = applicationAccessChild(ctx, record, "LogoURL")
			if err != nil {
				return err
			}
			allowed, err := applicationAccessChild(ctx, record, "Scopes")
			if err != nil {
				return err
			}
			// Clone on every match, as the original resolver does, including
			// non-nil empty slices after deleting the final allowed scope.
			if err := r.charge(ctx, int64(len(r.Installation.Scopes))*128+512); err != nil {
				return err
			}
			scopes := slices.Clone(r.Installation.Scopes)
			var matchErr error
			r.Installation.Scopes = slices.DeleteFunc(scopes, func(scope string) bool {
				contains := false
				matchErr = errors.Join(matchErr, allowed.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
					equal, err := node.StringEqual(ctx, scope)
					contains = contains || equal
					return err
				}))
				return !contains
			})
			return matchErr
		})
		if err != nil {
			return false, err
		}
	}
	if found {
		value, err := r.value(ctx, name)
		if err != nil {
			return false, err
		}
		r.Installation.Name = value.String()
		value, err = r.value(ctx, logo)
		if err != nil {
			return false, err
		}
		r.Installation.AvatarURL = value.String()
	}
	return found, nil
}

// ApplicationListRead owns the ordered, complete installation output.
type ApplicationListRead struct {
	Applications []domain.ApplicationInstallation
	reads        []*ApplicationAccessRead
	work         MemberDirectoryRead
	once         sync.Once
}

func (r *ApplicationListRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Applications = nil
			for _, read := range r.reads {
				read.Close()
			}
			r.reads = nil
			r.work.Close()
		})
	}
}

func (s *SQLiteStore) BeginApplicationListRead(ctx context.Context, workspace string) (_ *ApplicationListRead, err error) {
	r := &ApplicationListRead{}
	if !s.BoundedMetadataEnabled() {
		r.Applications, err = s.ListApplications(ctx, workspace)
		return r, err
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	defer func() {
		if err != nil {
			r.Close()
			recordMetadataOutputError(ctx, err)
		}
	}()
	var tx *sqlTx
	if snapshot, ok := ctx.Value(workspaceReadKey{}).(workspaceRead); ok && snapshot.database == s.db {
		if snapshot.reader == nil {
			return nil, ErrMetadataRecordQuery
		}
		tx = snapshot.reader
	} else {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		ctx = context.WithValue(ctx, workspaceReadKey{}, workspaceRead{database: s.db, reader: tx})
	}
	var count, bytes int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+s.scaleP21SW16AttachmentBytes("user_id")+"),0) FROM application_installations WHERE workspace_key=?", workspace).Scan(&count, &bytes); err != nil {
		return nil, err
	}
	if count < 0 || bytes < 0 || count > applicationAccessMaxWork/4096 || bytes > (applicationAccessMaxWork-count*4096)/16 {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.work.charge(ctx, s, int(count*4096+bytes*16)+64<<10); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT user_id FROM application_installations WHERE workspace_key=? ORDER BY id", workspace)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, int(count))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	r.Applications = make([]domain.ApplicationInstallation, 0, len(ids))
	for _, id := range ids {
		read, err := s.BeginApplicationAccessRead(ctx, id, "full")
		if err != nil {
			return nil, err
		}
		r.reads = append(r.reads, read)
		r.Applications = append(r.Applications, read.Installation)
	}
	return r, ctx.Err()
}
