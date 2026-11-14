package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
)

const memberDirectoryMaxWork = 64 << 20

// MemberDirectoryRead owns only the display scalars used by membership audits
// and responses. Users/Teams and strings copied from them must not outlive Close.
// This trusted directory does not authorize its caller.
type MemberDirectoryRead struct {
	Users    map[string]domain.User
	Teams    map[string]domain.Team
	used     int
	reserved int
	releases []func()
	once     sync.Once
}

func (r *MemberDirectoryRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Users, r.Teams = nil, nil
			for _, release := range r.releases {
				release()
			}
			r.releases = nil
		})
	}
}

func (r *MemberDirectoryRead) charge(ctx context.Context, s *SQLiteStore, cost int) error {
	if cost < 0 || cost > memberDirectoryMaxWork-r.used {
		return ErrMetadataWorkBudget
	}
	wanted := r.used + cost
	if wanted > r.reserved {
		additional := min(memberDirectoryMaxWork-r.reserved, max(128<<10, wanted-r.reserved))
		_, release, err := s.tryAdmitAdditionalMetadataWork(ctx, additional)
		if err != nil {
			return err
		}
		r.releases = append(r.releases, release)
		r.reserved += additional
	}
	r.used = wanted
	return nil
}

// BeginMemberDirectoryRead projects the exact installed user/team generation.
// An optional membership overlay has ListMembers' all-or-none semantics:
// SQL/installation decoding failures preserve the installed directory unchanged.
// Capacity/coverage failures are explicit errors, never successful truncation.
func (s *SQLiteStore) BeginMemberDirectoryRead(ctx context.Context, workspace string, overlayMembers bool) (_ *MemberDirectoryRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, false, ErrBoundedMetadataCoverage
	}
	owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	defer owner.Close()
	source, err := owner.retainSource(ctx, owner.workspace)
	if err != nil {
		return nil, true, err
	}
	defer source.Release()
	read := &MemberDirectoryRead{}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.charge(ctx, s, 4096); err != nil {
		return nil, true, err
	}
	read.Users, read.Teams = map[string]domain.User{}, map[string]domain.Team{}
	stringField := func(record metadataLogicalNode, name string) (string, error) {
		node, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, name)
		if err != nil || node == nil {
			if err == nil {
				err = ErrBoundedMetadataCoverage
			}
			return "", err
		}
		part := node.(metadataAggregateStoredNode).value
		if part.typ.Kind() != reflect.String || part.cost > memberDirectoryMaxWork/16 {
			return "", ErrMetadataWorkBudget
		}
		if err := read.charge(ctx, s, int(part.cost)*16); err != nil {
			return "", err
		}
		value, err := part.Value(ctx, int(part.cost))
		if err != nil {
			return "", err
		}
		return value.String(), nil
	}
	workspaceIndex, _, _ := metadataLogicalField("workspace")
	workspaceID, err := stringField(source.fields[workspaceIndex], "ID")
	if err != nil {
		return nil, true, err
	}
	for _, kind := range []string{"users", "teams"} {
		index, _, _ := metadataLogicalField(kind)
		err = source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			if err := read.charge(ctx, s, 1024); err != nil {
				return err
			}
			id, err := stringField(record, "ID")
			if err != nil {
				return err
			}
			name, err := stringField(record, "Name")
			if err != nil {
				return err
			}
			if kind == "teams" {
				key, err := stringField(record, "Key")
				if err != nil {
					return err
				}
				read.Teams[id] = domain.Team{ID: id, Name: name, Key: key}
				return nil
			}
			display, err := stringField(record, "DisplayName")
			if err != nil {
				return err
			}
			email, err := stringField(record, "Email")
			if err != nil {
				return err
			}
			read.Users[id] = domain.User{ID: id, Name: name, DisplayName: display, Email: email}
			return nil
		})
		if err != nil {
			return nil, true, err
		}
	}
	if overlayMembers {
		if err := s.memberDirectoryOverlay(ctx, workspaceID, read); err != nil {
			return nil, true, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	return read, true, nil
}

func (s *SQLiteStore) memberDirectoryOverlay(ctx context.Context, workspaceID string, read *MemberDirectoryRead) error {
	// These columns have precisely ListMembers' row order and NULL/Scan
	// behavior. Unused strings are projected to empty strings before transfer;
	// their content and parsed timestamps never affect the naming overlay.
	empty := func(column string) string { return "CASE WHEN " + column + " IS NULL THEN NULL ELSE '' END" }
	length := func(column string) string {
		if s.dialect == "sqlite" {
			return "LENGTH(CAST(" + column + " AS BLOB))"
		}
		return "OCTET_LENGTH(" + column + ")"
	}
	from := ` FROM workspace_memberships m JOIN auth_users u ON u.id=m.user_id WHERE m.workspace_id=?`
	rowBytes := "COALESCE(" + length("u.id") + ",0)+COALESCE(" + length("u.email") + ",0)+COALESCE(" + length("u.name") + ",0)+COALESCE(" + length("u.display_name") + ",0)"
	var count, size int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM("+rowBytes+"),0)"+from, workspaceID).Scan(&count, &size)
	if err != nil {
		return nil // ListMembers errors discard its entire overlay.
	}
	if count < 0 || size < 0 || count > memberDirectoryMaxWork/1024 || size > memberDirectoryMaxWork/32 {
		return ErrMetadataWorkBudget
	}
	if err := read.charge(ctx, s, int(count)*1024+int(size)*32); err != nil {
		return err
	}
	// Guard each transferred scalar as well as the aggregate preflight:
	// membership/profile changes may race the two statements.
	scalar := func(column string) string {
		return "CASE WHEN " + length(column) + ">? THEN '' ELSE " + column + " END"
	}
	oversized := "CASE WHEN " + rowBytes + ">? THEN 1 ELSE 0 END"
	identity := `COALESCE((SELECT i.subject FROM auth_identities i WHERE i.user_id=u.id AND (i.provider IN ('oidc','saml') OR (i.provider='scim' AND i.issuer=m.workspace_id)) ORDER BY CASE WHEN i.provider='scim' THEN 0 ELSE 1 END,i.created_at,i.id LIMIT 1),'')`
	rows, err := s.db.QueryContext(ctx, "SELECT "+scalar("u.id")+","+scalar("u.email")+","+scalar("u.name")+","+scalar("u.display_name")+","+
		empty("u.avatar_url")+","+empty("u.email_verified_at")+",u.active,"+empty("u.deleted_at")+","+empty("m.role")+","+empty("m.status")+","+empty("m.joined_at")+","+empty("m.last_seen_at")+","+empty(identity)+",CASE WHEN "+scimManagedMemberSQL+" THEN 1 ELSE 0 END,"+oversized+from+" ORDER BY lower(u.display_name)",
		size, size, size, size, size, workspaceID)
	if err != nil {
		return nil
	}
	staged := make([]domain.User, 0, int(count))
	var appIndices []int
	actual, actualBytes := int64(0), int64(0)
	err = func() error {
		defer rows.Close()
		for rows.Next() {
			var user domain.User
			var email, verified, deleted, lastSeen, identity sql.NullString
			var avatar, role, status, joined string
			var active, managed, oversized int
			if err := rows.Scan(&user.ID, &email, &user.Name, &user.DisplayName, &avatar, &verified, &active, &deleted, &role, &status, &joined, &lastSeen, &identity, &managed, &oversized); err != nil {
				return err
			}
			if oversized != 0 {
				return ErrMetadataWorkBudget
			}
			user.Email = email.String
			actual++
			actualBytes += int64(len(user.ID) + len(user.Email) + len(user.Name) + len(user.DisplayName))
			if actual > count || actualBytes > size {
				return ErrMetadataWorkBudget
			}
			if strings.HasPrefix(user.ID, "app_") {
				appIndices = append(appIndices, len(staged))
			}
			staged = append(staged, user)
		}
		return rows.Err()
	}()
	if err != nil {
		if errors.Is(err, ErrMetadataWorkBudget) {
			return err
		}
		return nil
	}
	var sources []*metadataLogicalSource
	if len(appIndices) != 0 {
		sources, err = s.memberDirectoryOAuthSources(ctx, read)
		if err != nil {
			return err
		}
		defer func() {
			for _, source := range sources {
				source.Release()
			}
		}()
	}
	// No membership row may be overlaid until all application rows validate.
	// A bad installation belonging to an otherwise unrelated member still
	// prevents every auth-user override, exactly as ListMembers does.
	for _, index := range appIndices {
		user, err := s.memberDirectoryApplication(ctx, staged[index].ID, read, sources)
		if err != nil {
			if errors.Is(err, ErrMetadataWorkBudget) || errors.Is(err, ErrBoundedMetadataCoverage) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return nil
		}
		staged[index] = user
	}
	// ApplicationInstallation.User uses the JSON userId, which need not equal
	// the lookup column. Preserve that key rather than "repairing" its identity.
	for _, user := range staged {
		read.Users[user.ID] = user
	}
	return nil
}

func (s *SQLiteStore) memberDirectoryOAuthSources(ctx context.Context, read *MemberDirectoryRead) (sources []*metadataLogicalSource, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r := s.metadataResidency
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := read.charge(ctx, s, len(s.workspaces)*256); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			for _, source := range sources {
				source.Release()
			}
		}
	}()
	// Keep the installed registry's traversal and each application's slice
	// order. Client IDs are global: the installation's home is not a filter.
	for key := range s.workspaces {
		source := r.logical[key]
		if source == nil {
			return sources, ErrBoundedMetadataCoverage
		}
		if err := source.Retain(); err != nil {
			return sources, err
		}
		sources = append(sources, source)
	}
	return sources, nil
}

type memberDirectoryInstallationReader struct {
	ctx   context.Context
	store *SQLiteStore
	tx    *sqlTx
	id    string
	size  int64
}

func (r *memberDirectoryInstallationReader) ReadAt(output []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, ErrMetadataRecordQuery
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	data := "data"
	if r.store.dialect == "sqlite" {
		data = "CAST(data AS BLOB)"
	}
	n := 0
	for n < len(output) && offset+int64(n) < r.size {
		size := min(int64(metadataRecordChunkBytes), int64(len(output)-n), r.size-offset-int64(n))
		var chunk []byte
		if err := r.tx.QueryRowContext(r.ctx, "SELECT SUBSTR("+data+",?,?) FROM application_installations WHERE user_id=?", offset+int64(n)+1, size, r.id).Scan(&chunk); err != nil {
			return n, err
		}
		if int64(len(chunk)) != size {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(output[n:], chunk)
	}
	if n < len(output) {
		return n, io.EOF
	}
	return n, nil
}

func (s *SQLiteStore) memberDirectoryApplication(ctx context.Context, id string, read *MemberDirectoryRead, sources []*metadataLogicalSource) (domain.User, error) {
	work, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 4<<20)
	if err != nil {
		return domain.User{}, err
	}
	defer release()
	tx, err := s.db.BeginTx(work, metadataReadOptions(s.dialect))
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	var size int64
	length := "OCTET_LENGTH(data)"
	if s.dialect == "sqlite" {
		length = "LENGTH(CAST(data AS BLOB))"
	}
	if err := tx.QueryRowContext(work, "SELECT "+length+" FROM application_installations WHERE user_id=?", id).Scan(&size); err != nil {
		return domain.User{}, err
	}
	input := &memberDirectoryInstallationReader{ctx: work, store: s, tx: tx, id: id, size: size}
	application, err := memberDirectoryProjectInstallation(work, input, size,
		func(cost int) error { return read.charge(ctx, s, cost) },
		func(clientID string) (string, bool, error) {
			return s.memberDirectoryApplicationName(ctx, clientID, read, sources)
		})
	if err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: application.UserID, Name: application.Name, DisplayName: application.Name}, nil
}

func (s *SQLiteStore) memberDirectoryApplicationName(ctx context.Context, clientID string, read *MemberDirectoryRead, sources []*metadataLogicalSource) (string, bool, error) {
	index, _, _ := metadataLogicalField("oauthApplications")
	var selected metadataLogicalNode
	for _, source := range sources {
		err := source.fields[index].Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			client, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, "ClientID")
			if err != nil || client == nil {
				return errors.Join(err, ErrBoundedMetadataCoverage)
			}
			equal, err := client.(metadataAggregateStoredNode).value.StringEqual(ctx, clientID)
			if err != nil || !equal {
				return err
			}
			name, err := metadataAggregateChild(ctx, metadataAggregateStoredNode{record}, "Name")
			if err != nil || name == nil {
				return errors.Join(err, ErrBoundedMetadataCoverage)
			}
			selected = name.(metadataAggregateStoredNode).value
			return nil
		})
		if err != nil {
			return "", false, err
		}
	}
	if selected.typ == nil {
		return "", false, nil
	}
	if selected.cost > memberDirectoryMaxWork/16 {
		return "", false, ErrMetadataWorkBudget
	}
	if err := read.charge(ctx, s, int(selected.cost)*16); err != nil {
		return "", false, err
	}
	value, err := selected.Value(ctx, int(selected.cost))
	if err != nil {
		return "", false, err
	}
	return value.String(), true, nil
}

// Validate the complete typed ApplicationInstallation while retaining only its
// three name-selection strings. Duplicate/null fields retain encoding/json's
// merge behavior; ignored large strings/arrays are scanned with fixed scratch.
func memberDirectoryProjectInstallation(ctx context.Context, input io.ReaderAt, size int64, charge func(int) error, nameLookup func(string) (string, bool, error)) (domain.ApplicationInstallation, error) {
	var result domain.ApplicationInstallation
	section := io.NewSectionReader(input, 0, size)
	scanner := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(section, metadataRecordChunkBytes)}
	if err := scanner.spaces(); err != nil {
		return result, err
	}
	first, err := scanner.peek()
	if err != nil {
		return result, err
	}
	if first == 'n' {
		if err := scanner.literal("null"); err != nil {
			return result, err
		}
		return result, scanner.end()
	}
	if first != '{' {
		return result, ErrMetadataRecordQuery
	}
	typ := reflect.TypeFor[domain.ApplicationInstallation]()
	if reflect.PointerTo(typ).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return result, ErrBoundedMetadataCoverage
	}
	selectedRanges := map[string]metadataCollectionRange{}
	err = scanner.object(func(key, value metadataCollectionRange) error {
		if key.size > 6*int64(len("withoutRealtime"))+2 {
			return nil
		}
		var name string
		if err := json.NewDecoder(io.NewSectionReader(input, key.offset, key.size)).Decode(&name); err != nil {
			return err
		}
		var field reflect.StructField
		found := false
		for i := 0; i < typ.NumField(); i++ {
			candidate := typ.Field(i)
			tag := strings.Split(candidate.Tag.Get("json"), ",")[0]
			if tag != "-" && (tag == name || strings.EqualFold(tag, name)) {
				field, found = candidate, true
				break
			}
		}
		if !found {
			return nil
		}
		part := io.NewSectionReader(input, value.offset, value.size)
		switch field.Type {
		case reflect.TypeFor[string]():
			retain := field.Name == "UserID" || field.Name == "ClientID" || field.Name == "Name"
			null, err := VisitMetadataJSONStringChunks(ctx, part, func([]byte) error { return nil })
			if err != nil || null || !retain {
				return err
			}
			selectedRanges[field.Name] = value
		case reflect.TypeFor[bool]():
			if value.size > 5 {
				return ErrMetadataRecordQuery
			}
			var flag bool
			return json.NewDecoder(part).Decode(&flag)
		case reflect.TypeFor[time.Time]():
			_, err := scaleP21SW23TeamTime(ctx, part)
			return err
		case reflect.TypeFor[[]string]():
			values := metadataCollectionScanner{ctx: ctx, input: bufio.NewReaderSize(part, metadataRecordChunkBytes)}
			first, err := values.peek()
			if err != nil {
				return err
			}
			if first == 'n' {
				return values.literal("null")
			}
			if first != '[' {
				return ErrMetadataRecordQuery
			}
			return values.array(func(item metadataCollectionRange) error {
				_, err := VisitMetadataJSONStringChunks(ctx, io.NewSectionReader(input, value.offset+item.offset, item.size), func([]byte) error { return nil })
				return err
			}, 0)
		default:
			return ErrBoundedMetadataCoverage
		}
		return nil
	}, 0)
	if err != nil {
		return result, err
	}
	if err := scanner.end(); err != nil {
		return result, err
	}
	readSelected := func(name string) (string, error) {
		part, found := selectedRanges[name]
		if !found {
			return "", nil
		}
		var selected strings.Builder
		_, err := VisitMetadataJSONStringChunks(ctx, io.NewSectionReader(input, part.offset, part.size), func(chunk []byte) error {
			if err := charge(len(chunk) * 32); err != nil {
				return err
			}
			_, err := selected.Write(chunk)
			return err
		})
		return selected.String(), err
	}
	result.UserID, err = readSelected("UserID")
	if err != nil {
		return result, err
	}
	result.ClientID, err = readSelected("ClientID")
	if err != nil {
		return result, err
	}
	if nameLookup != nil {
		name, found, err := nameLookup(result.ClientID)
		if err != nil {
			return result, err
		}
		if found {
			result.Name = name
			return result, nil
		}
	}
	result.Name, err = readSelected("Name")
	return result, err
}
