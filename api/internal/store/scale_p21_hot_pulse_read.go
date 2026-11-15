package store

import (
	"bufio"
	"context"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// PulseRead owns the installed source, selected values, rule indexes and response
// encoding work. Values preserve JSON and instant comparisons, not native time
// Location identity, and must never become mutation preimages. Keep the read
// open until the response has been written.
type PulseRead struct {
	Snapshot  *PulseFeedSnapshot
	store     *SQLiteStore
	workspace string
	owner     *InstalledMetadataRead
	source    *metadataLogicalSource
	releases  []func()
	search    map[*PulseFeedEntry]pulseSearchNodes
	once      sync.Once
}

func (r *PulseRead) Close() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.Snapshot = nil
		for _, release := range r.releases {
			release()
		}
		if r.source != nil {
			r.source.Release()
		}
		if r.owner != nil {
			r.owner.Close()
		}
		r.releases, r.source, r.owner, r.search = nil, nil, nil, nil
	})
}

// TransferOutput retains selected values through the containing operation.
func (r *PulseRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

func (r *PulseRead) reserve(ctx context.Context, cost uint64) (func(), error) {
	if cost > metadataWorkBytes {
		return nil, ErrMetadataWorkBudget
	}
	_, release, err := r.store.tryAdmitAdditionalMetadataWork(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), int(cost))
	return release, err
}

func (r *PulseRead) value(ctx context.Context, node metadataLogicalNode) (reflect.Value, error) {
	if node.cost > (metadataWorkBytes-256)/8 {
		return reflect.Value{}, ErrMetadataWorkBudget
	}
	release, err := r.reserve(ctx, node.cost*8+256)
	if err != nil {
		return reflect.Value{}, err
	}
	value, err := node.WireValue(ctx, int(node.cost))
	if err != nil {
		release()
		return reflect.Value{}, err
	}
	r.releases = append(r.releases, release)
	return value, nil
}

// BeginPulseRead preserves the exact installed PulseFeed/PulseSources source.
// indexed=false skips all update records. It is a trusted read, not authority.
func (s *SQLiteStore) BeginPulseRead(ctx context.Context, workspace string, indexed bool) (_ *PulseRead, found bool, err error) {
	return s.beginPulseRead(ctx, workspace, indexed, false)
}

// BeginPulseScheduleRead first loads only identity and feature settings. A
// disabled scheduler never visits per-user settings or update collections.
func (s *SQLiteStore) BeginPulseScheduleRead(ctx context.Context, workspace string) (*PulseRead, bool, error) {
	return s.beginPulseRead(ctx, workspace, false, true)
}

func (s *SQLiteStore) beginPulseRead(ctx context.Context, workspace string, indexed, schedule bool) (_ *PulseRead, found bool, err error) {
	if !s.BoundedMetadataEnabled() {
		var snapshot *PulseFeedSnapshot
		if schedule {
			var data domain.Bootstrap
			data, found = s.WorkspaceMetadataFields(workspace, "workspaceSettings")
			snapshot = pulseSnapshotOf(data)
		} else if indexed {
			snapshot, found = s.PulseFeed(workspace)
		} else {
			snapshot, found = s.PulseSources(workspace)
		}
		if !found {
			return nil, false, nil
		}
		return &PulseRead{Snapshot: snapshot, store: s, workspace: workspace}, true, nil
	}
	owner, found, err := s.BeginInstalledMetadataRead(ctx, workspace)
	if err != nil || !found {
		return nil, found, err
	}
	read := &PulseRead{Snapshot: &PulseFeedSnapshot{}, store: s, owner: owner, workspace: workspace}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	read.source, err = owner.retainSource(ctx, owner.workspace)
	if err != nil {
		return nil, true, err
	}
	release, err := read.reserve(ctx, 128<<10)
	if err != nil {
		return nil, true, err
	}
	read.releases = append(read.releases, release)
	if schedule {
		for _, item := range []struct {
			name   string
			target any
		}{
			{"workspace", &read.Snapshot.Workspace}, {"workspaceSettings", &read.Snapshot.WorkspaceSettings},
		} {
			value, err := read.value(ctx, read.field(item.name))
			if err != nil {
				return nil, true, err
			}
			reflect.ValueOf(item.target).Elem().Set(value)
		}
	} else {
		if err := read.sources(ctx); err != nil {
			return nil, true, err
		}
	}
	if indexed {
		if err := read.index(ctx); err != nil {
			return nil, true, err
		}
	}
	return read, true, nil
}

// LoadSchedule adds only scheduler-consumed settings. The settings map is
// limited to the exact pulseDeliveryCursors key, preserving its native value.
func (r *PulseRead) LoadSchedule(ctx context.Context) error {
	if r.Snapshot == nil {
		return errMetadataLogicalClosed
	}
	if r.source == nil {
		data, found := r.store.WorkspaceMetadataFields(r.workspace, "workspaceSettings", "settings", "userSettings", "teamSettings")
		if !found {
			return ErrResourceAccessChanged
		}
		r.Snapshot = pulseSnapshotOf(data)
		return nil
	}
	for _, name := range []string{"settings", "userSettings", "teamSettings"} {
		if err := r.field(name).MapEntries(ctx, func(key, node metadataLogicalNode) error {
			if name == "settings" {
				match, err := key.StringEqual(ctx, "pulseDeliveryCursors")
				if err != nil || !match {
					return err
				}
				value, err := r.value(ctx, node)
				if err == nil {
					r.Snapshot.Settings = map[string]any{"pulseDeliveryCursors": value.Interface()}
				}
				return err
			}
			value, err := r.value(ctx, key)
			if err != nil {
				return err
			}
			id := value.String()
			if name == "userSettings" {
				var settings domain.UserSettings
				if err := r.project(ctx, node, reflect.ValueOf(&settings).Elem(), strings.Fields("PulseSchedule Timezone")); err != nil {
					return err
				}
				if r.Snapshot.UserSettings == nil {
					r.Snapshot.UserSettings = map[string]domain.UserSettings{}
				}
				r.Snapshot.UserSettings[id] = settings
			} else {
				var settings domain.TeamSettings
				if err := r.project(ctx, node, reflect.ValueOf(&settings).Elem(), []string{"Timezone"}); err != nil {
					return err
				}
				if r.Snapshot.TeamSettings == nil {
					r.Snapshot.TeamSettings = map[string]domain.TeamSettings{}
				}
				r.Snapshot.TeamSettings[id] = settings
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// LoadFeed adds source rules and ordered entries to a scheduler read.
func (r *PulseRead) LoadFeed(ctx context.Context) error {
	if r.Snapshot == nil {
		return errMetadataLogicalClosed
	}
	if r.source == nil {
		snapshot, found := r.store.PulseFeed(r.workspace)
		if !found {
			return ErrResourceAccessChanged
		}
		r.Snapshot = snapshot
		return nil
	}
	if err := r.sources(ctx); err != nil {
		return err
	}
	return r.index(ctx)
}

func (r *PulseRead) field(name string) metadataLogicalNode {
	index, _, _ := metadataLogicalField(name)
	return r.source.fields[index]
}

func (r *PulseRead) project(ctx context.Context, node metadataLogicalNode, target reflect.Value, fields []string) error {
	return node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
		if !slices.Contains(fields, key.String()) {
			return nil
		}
		if child.typ == reflect.TypeFor[*domain.User]() {
			return child.Children(ctx, func(_ reflect.Value, user metadataLogicalNode) error {
				value := reflect.New(reflect.TypeFor[domain.User]())
				if err := r.project(ctx, user, value.Elem(), strings.Fields("ID Name DisplayName")); err != nil {
					return err
				}
				target.FieldByName(key.String()).Set(value)
				return nil
			})
		}
		if key.String() == "Milestones" {
			return child.Children(ctx, func(_ reflect.Value, milestone metadataLogicalNode) error {
				var value domain.ProjectMilestone
				if err := r.project(ctx, milestone, reflect.ValueOf(&value).Elem(), strings.Fields("ID Name TargetDate")); err != nil {
					return err
				}
				field := target.FieldByName("Milestones")
				field.Set(reflect.Append(field, reflect.ValueOf(value)))
				return nil
			})
		}
		value, err := r.value(ctx, child)
		if err == nil {
			target.FieldByName(key.String()).Set(value)
		}
		return err
	})
}

var pulseProjectFields = strings.Fields("ID Name SlugID PreviousSlugIDs Icon Color Priority PriorityLabel Status Lead MemberIDs LabelIDs TeamIDs DependencyIDs Initiatives Milestones StartDate TargetDate ArchivedAt")
var pulseInitiativeFields = strings.Fields("ID Name SlugID PreviousSlugIDs Icon Color Status Owner LeadTeamID ContributingTeamIDs ParentInitiativeIDs ProjectIDs TargetDate")

func (r *PulseRead) sources(ctx context.Context) error {
	for _, item := range []struct {
		name   string
		target any
	}{
		{"workspace", &r.Snapshot.Workspace},
		{"workspaceSettings", &r.Snapshot.WorkspaceSettings},
		{"initiativeRelations", &r.Snapshot.InitiativeRelations},
		{"subscriptions", &r.Snapshot.Subscriptions},
		{"savedViews", &r.Snapshot.SavedViews},
	} {
		value, err := r.value(ctx, r.field(item.name))
		if err != nil {
			return err
		}
		reflect.ValueOf(item.target).Elem().Set(value)
	}
	if err := r.field("teams").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		var team domain.Team
		if err := r.project(ctx, node, reflect.ValueOf(&team).Elem(), strings.Fields("ID Name Private ArchivedAt")); err != nil {
			return err
		}
		r.Snapshot.Teams = append(r.Snapshot.Teams, team)
		return nil
	}); err != nil {
		return err
	}
	if err := r.field("teamSettings").MapEntries(ctx, func(key, node metadataLogicalNode) error {
		value, err := r.value(ctx, key)
		if err != nil {
			return err
		}
		var settings domain.TeamSettings
		if err := r.project(ctx, node, reflect.ValueOf(&settings).Elem(), strings.Fields("ParentTeamID Access MembershipRestriction")); err != nil {
			return err
		}
		if r.Snapshot.TeamSettings == nil {
			r.Snapshot.TeamSettings = map[string]domain.TeamSettings{}
		}
		r.Snapshot.TeamSettings[value.String()] = settings
		return nil
	}); err != nil {
		return err
	}
	for _, kind := range []string{"projects", "initiatives"} {
		err := r.field(kind).Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			if kind == "projects" {
				var project domain.Project
				if err := r.project(ctx, node, reflect.ValueOf(&project).Elem(), pulseProjectFields); err != nil {
					return err
				}
				r.Snapshot.Projects = append(r.Snapshot.Projects, project)
			} else {
				var initiative domain.Initiative
				if err := r.project(ctx, node, reflect.ValueOf(&initiative).Elem(), pulseInitiativeFields); err != nil {
					return err
				}
				r.Snapshot.Initiatives = append(r.Snapshot.Initiatives, initiative)
			}
			release, err := r.reserve(ctx, 2048)
			if err == nil {
				r.releases = append(r.releases, release)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// LoadUserSettings selects only the exact installed map key. It deliberately
// does not substitute a canonical user-settings row or normalize defaults.
func (r *PulseRead) LoadUserSettings(ctx context.Context, userID string) error {
	if r.Snapshot == nil {
		return errMetadataLogicalClosed
	}
	if r.source == nil {
		return nil
	}
	return r.field("userSettings").MapEntries(ctx, func(key, node metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, userID)
		if err != nil || !match {
			return err
		}
		var settings domain.UserSettings
		err = r.project(ctx, node, reflect.ValueOf(&settings).Elem(), []string{"FeedLastSeenTime"})
		if err == nil {
			if r.Snapshot.UserSettings == nil {
				r.Snapshot.UserSettings = map[string]domain.UserSettings{}
			}
			r.Snapshot.UserSettings[userID] = settings
		}
		return err
	})
}

// LoadLatestSnapshot visits history without decoding update details and retains
// the first nonnil snapshot, matching the legacy composer preview.
func (r *PulseRead) LoadLatestSnapshot(ctx context.Context, kind, sourceID string) (*domain.PulseSnapshot, error) {
	if r.Snapshot == nil {
		return nil, errMetadataLogicalClosed
	}
	if r.source == nil {
		if kind == "project" {
			for _, update := range r.Snapshot.ProjectUpdates[sourceID] {
				if update.Snapshot != nil {
					return update.Snapshot, nil
				}
			}
		} else {
			for _, update := range r.Snapshot.InitiativeUpdates[sourceID] {
				if update.Snapshot != nil {
					return update.Snapshot, nil
				}
			}
		}
		return nil, nil
	}
	if kind != "project" && kind != "initiative" {
		return nil, ErrMetadataRecordQuery
	}
	var result *domain.PulseSnapshot
	err := r.field(kind+"Updates").MapEntries(ctx, func(key, updates metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, sourceID)
		if err != nil || !match {
			return err
		}
		return updates.Children(ctx, func(_ reflect.Value, update metadataLogicalNode) error {
			if result != nil {
				return nil
			}
			return update.Children(ctx, func(key reflect.Value, node metadataLogicalNode) error {
				if key.String() != "Snapshot" {
					return nil
				}
				value, err := r.value(ctx, node)
				if err == nil {
					result = value.Interface().(*domain.PulseSnapshot)
				}
				return err
			})
		})
	})
	return result, err
}

func (r *PulseRead) index(ctx context.Context) error {
	r.Snapshot.Recent = []*PulseFeedEntry{}
	r.search = map[*PulseFeedEntry]pulseSearchNodes{}
	// Handles are the only user fields used by PulseMentions.
	handles := map[string]string{}
	if err := r.field("users").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		var user domain.User
		if err := r.project(ctx, node, reflect.ValueOf(&user).Elem(), strings.Fields("ID Email Name DisplayName Username")); err != nil {
			return err
		}
		for _, handle := range []string{user.Email, user.Name, strings.ReplaceAll(user.DisplayName, " ", ""), user.Username} {
			if handle = strings.ToLower(strings.TrimSpace(handle)); handle != "" {
				handles[handle] = user.ID
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, kind := range []string{"project", "initiative"} {
		field := kind + "Updates"
		if err := r.field(field).MapEntries(ctx, func(key, updates metadataLogicalNode) error {
			source, err := r.value(ctx, key)
			if err != nil {
				return err
			}
			sourceID := source.String()
			return updates.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
				// The summary decoder's temporary work is released after this
				// entry. Detail comments, attachments and snapshots are skipped.
				start := len(r.releases)
				var update domain.ProjectUpdate
				var search pulseSearchNodes
				comments, reactions := 0, 0
				err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
					switch key.String() {
					case "Comments":
						return child.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error { comments++; return nil })
					case "Reactions":
						return child.MapEntries(ctx, func(_, users metadataLogicalNode) error {
							return users.Children(ctx, func(_ reflect.Value, _ metadataLogicalNode) error { reactions++; return nil })
						})
					case "ID", "Body", "BodyData", "Health", "CreatedAt":
						if key.String() == "Body" {
							search.body = child
						}
						value, err := r.value(ctx, child)
						if err == nil {
							reflect.ValueOf(&update).Elem().FieldByName(key.String()).Set(value)
						}
						return err
					case "User":
						if err := child.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
							switch key.String() {
							case "DisplayName":
								search.display = value
							case "Name":
								search.name = value
							}
							return nil
						}); err != nil {
							return err
						}
						return r.project(ctx, child, reflect.ValueOf(&update.User).Elem(), strings.Fields("ID Name DisplayName"))
					}
					return nil
				})
				var entry *PulseFeedEntry
				if err == nil {
					entry = &PulseFeedEntry{Kind: kind, SourceID: sourceID, UpdateID: update.ID, CreatedAt: update.CreatedAt, AuthorID: update.User.ID, Health: update.Health, Comments: comments, Reactions: reactions}
					entry.Score = PulsePopularScore(comments, reactions, update.CreatedAt)
					entry.Mentions = PulseMentions(update.Body, update.BodyData, handles)
					var release func()
					cost := uint64(4096 + 8*(len(entry.UpdateID)+len(entry.AuthorID)+len(entry.Health)))
					for _, id := range entry.Mentions {
						cost += uint64(256 + len(id)*8)
					}
					release, err = r.reserve(ctx, cost)
					if err == nil {
						// Release temporary decodes before retaining the entry lease.
						for _, temporary := range r.releases[start:] {
							temporary()
						}
						r.releases = append(r.releases[:start], release)
						r.Snapshot.Recent = append(r.Snapshot.Recent, entry)
						r.search[entry] = search
						return nil
					}
				}
				for _, temporary := range r.releases[start:] {
					temporary()
				}
				r.releases = r.releases[:start]
				return err
			})
		}); err != nil {
			return err
		}
	}
	slices.SortFunc(r.Snapshot.Recent, ComparePulseRecent)
	r.Snapshot.Popular = slices.Clone(r.Snapshot.Recent)
	slices.SortFunc(r.Snapshot.Popular, ComparePulsePopular)
	return nil
}

type pulseSearchNodes struct {
	body, display, name metadataLogicalNode
}

// MatchSearch streams the exact lowercase body + newline + display name +
// newline + name predicate used by the legacy index. search is already
// normalized by the caller. No body, author text or lowercase copy is retained.
func (r *PulseRead) MatchSearch(ctx context.Context, entry *PulseFeedEntry, search string) (bool, error) {
	if r.Snapshot == nil {
		return false, errMetadataLogicalClosed
	}
	if r.source == nil {
		return search == "" || strings.Contains(entry.Search, search), nil
	}
	nodes, found := r.search[entry]
	if !found {
		return false, ErrMetadataRecordQuery
	}
	if search == "" {
		return true, ctx.Err()
	}
	if len(search) > (metadataWorkBytes-64<<10)/16 {
		return false, ErrMetadataWorkBudget
	}
	release, err := r.reserve(ctx, uint64(64<<10+len(search)*16))
	if err != nil {
		return false, err
	}
	defer release()
	// KMP retains only the query, so even a match straddling field or UTF-8
	// reader boundaries does not require retaining any source text.
	prefix := make([]int, len(search))
	for i, j := 1, 0; i < len(search); i++ {
		for j > 0 && search[i] != search[j] {
			j = prefix[j-1]
		}
		if search[i] == search[j] {
			j++
		}
		prefix[i] = j
	}
	matched, position := false, 0
	accept := func(value byte) {
		for position > 0 && value != search[position] {
			position = prefix[position-1]
		}
		if value == search[position] {
			position++
		}
		if position == len(search) {
			matched = true
		}
	}
	for index, node := range []metadataLogicalNode{nodes.body, nodes.display, nodes.name} {
		if index > 0 {
			accept('\n')
			if matched {
				return true, nil
			}
		}
		err := node.StreamBytes(ctx, func(input io.Reader) error {
			reader := bufio.NewReaderSize(input, 32<<10)
			var encoded [utf8.UTFMax]byte
			for {
				value, _, err := reader.ReadRune()
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
				size := utf8.EncodeRune(encoded[:], unicode.ToLower(value))
				for _, value := range encoded[:size] {
					accept(value)
					if matched {
						return nil
					}
				}
			}
		})
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

// LoadUpdates adds complete updates in installed slice order. nil ids loads the
// complete history; a nonnil empty ids slice loads nothing. No summaries replace
// large details: exceeding admission returns an explicit error.
func (r *PulseRead) LoadUpdates(ctx context.Context, kind, sourceID string, ids []string) error {
	if r.Snapshot == nil {
		return errMetadataLogicalClosed
	}
	if r.source == nil {
		return nil
	}
	if kind != "project" && kind != "initiative" {
		return ErrMetadataRecordQuery
	}
	return r.field(kind+"Updates").MapEntries(ctx, func(key, node metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, sourceID)
		if err != nil || !match {
			return err
		}
		return node.Children(ctx, func(_ reflect.Value, update metadataLogicalNode) error {
			wanted := ids == nil
			if !wanted {
				if err := update.Children(ctx, func(key reflect.Value, value metadataLogicalNode) error {
					if key.String() == "ID" {
						for _, id := range ids {
							match, err := value.StringEqual(ctx, id)
							if err != nil {
								return err
							}
							wanted = wanted || match
						}
					}
					return nil
				}); err != nil {
					return err
				}
			}
			if !wanted {
				return nil
			}
			value, err := r.value(ctx, update)
			if err != nil {
				return err
			}
			if kind == "project" {
				if r.Snapshot.ProjectUpdates == nil {
					r.Snapshot.ProjectUpdates = map[string][]domain.ProjectUpdate{}
				}
				r.Snapshot.ProjectUpdates[sourceID] = append(r.Snapshot.ProjectUpdates[sourceID], value.Interface().(domain.ProjectUpdate))
			} else {
				if r.Snapshot.InitiativeUpdates == nil {
					r.Snapshot.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
				}
				r.Snapshot.InitiativeUpdates[sourceID] = append(r.Snapshot.InitiativeUpdates[sourceID], value.Interface().(domain.InitiativeUpdate))
			}
			return nil
		})
	})
}

// VisitUpdates lends one complete installed update at a time. The visitor must
// finish encoding/processing it before returning and must not retain its value.
// The pinned generation survives the whole traversal, but decoded bodies do not.
func (r *PulseRead) VisitUpdates(ctx context.Context, kind, sourceID string, visit func(any) error) error {
	if r.Snapshot == nil {
		return errMetadataLogicalClosed
	}
	if visit == nil || kind != "project" && kind != "initiative" {
		return ErrMetadataRecordQuery
	}
	if r.source == nil {
		if kind == "project" {
			for _, update := range r.Snapshot.ProjectUpdates[sourceID] {
				if err := visit(update); err != nil {
					return err
				}
			}
		} else {
			for _, update := range r.Snapshot.InitiativeUpdates[sourceID] {
				if err := visit(update); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return r.field(kind+"Updates").MapEntries(ctx, func(key, updates metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, sourceID)
		if err != nil || !match {
			return err
		}
		return updates.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			start := len(r.releases)
			defer func() {
				for _, release := range r.releases[start:] {
					release()
				}
				r.releases = r.releases[:start]
			}()
			value, err := r.value(ctx, node)
			if err != nil {
				return err
			}
			return visit(value.Interface())
		})
	})
}

// LoadUpdatePage selects a native slice window without decoding other details.
// total counts the entire installed source, including duplicate update IDs.
// The caller binds any public cursor to the owning installed generation.
func (r *PulseRead) LoadUpdatePage(ctx context.Context, kind, sourceID string, offset, limit int) (total int, err error) {
	if r.Snapshot == nil {
		return 0, errMetadataLogicalClosed
	}
	if offset < 0 || limit < 1 || limit > 100 || kind != "project" && kind != "initiative" {
		return 0, ErrMetadataRecordQuery
	}
	if r.source == nil {
		if kind == "project" {
			updates := r.Snapshot.ProjectUpdates[sourceID]
			total = len(updates)
			if total > 0 {
				start := min(offset, total)
				r.Snapshot.ProjectUpdates = maps.Clone(r.Snapshot.ProjectUpdates)
				r.Snapshot.ProjectUpdates[sourceID] = slices.Clone(updates[start : start+min(limit, total-start)])
			}
			return total, nil
		}
		updates := r.Snapshot.InitiativeUpdates[sourceID]
		total = len(updates)
		if total > 0 {
			start := min(offset, total)
			r.Snapshot.InitiativeUpdates = maps.Clone(r.Snapshot.InitiativeUpdates)
			r.Snapshot.InitiativeUpdates[sourceID] = slices.Clone(updates[start : start+min(limit, total-start)])
		}
		return total, nil
	}
	err = r.field(kind+"Updates").MapEntries(ctx, func(key, updates metadataLogicalNode) error {
		match, err := key.StringEqual(ctx, sourceID)
		if err != nil || !match {
			return err
		}
		return updates.Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
			current := total
			total++
			if current < offset || current-offset >= limit {
				return nil
			}
			value, err := r.value(ctx, node)
			if err != nil {
				return err
			}
			if kind == "project" {
				if r.Snapshot.ProjectUpdates == nil {
					r.Snapshot.ProjectUpdates = map[string][]domain.ProjectUpdate{}
				}
				r.Snapshot.ProjectUpdates[sourceID] = append(r.Snapshot.ProjectUpdates[sourceID], value.Interface().(domain.ProjectUpdate))
			} else {
				if r.Snapshot.InitiativeUpdates == nil {
					r.Snapshot.InitiativeUpdates = map[string][]domain.InitiativeUpdate{}
				}
				r.Snapshot.InitiativeUpdates[sourceID] = append(r.Snapshot.InitiativeUpdates[sourceID], value.Interface().(domain.InitiativeUpdate))
			}
			return nil
		})
	})
	return total, err
}

// LoadSource replaces matching compact rule records with the complete installed
// aggregate for a legacy realtime payload. Duplicates preserve installed order.
func (r *PulseRead) LoadSource(ctx context.Context, kind, id string) error {
	if r.Snapshot == nil {
		return errMetadataLogicalClosed
	}
	if r.source == nil {
		return nil
	}
	if kind != "project" && kind != "initiative" {
		return ErrMetadataRecordQuery
	}
	index := 0
	return r.field(kind+"s").Children(ctx, func(_ reflect.Value, node metadataLogicalNode) error {
		current := index
		index++
		match := false
		if err := node.Children(ctx, func(key reflect.Value, child metadataLogicalNode) error {
			if key.String() == "ID" {
				var err error
				match, err = child.StringEqual(ctx, id)
				return err
			}
			return nil
		}); err != nil || !match {
			return err
		}
		value, err := r.value(ctx, node)
		if err == nil {
			if kind == "project" {
				r.Snapshot.Projects[current] = value.Interface().(domain.Project)
			} else {
				r.Snapshot.Initiatives[current] = value.Interface().(domain.Initiative)
			}
		}
		return err
	})
}
