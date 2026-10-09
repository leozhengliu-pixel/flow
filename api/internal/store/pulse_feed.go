package store

import (
	"encoding/json"
	"hash/maphash"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"flow/api/internal/domain"
)

// PulseFeedEntry is one project or initiative update in the Pulse index. The
// index holds only what ordering, For-me rules, filters and search need, so a
// feed page never decodes or copies update bodies it does not return.
type PulseFeedEntry struct {
	Kind      string // project | initiative
	SourceID  string
	UpdateID  string
	CreatedAt time.Time
	AuthorID  string
	Health    string
	Comments  int
	Reactions int
	Score     float64
	Mentions  []string
	// Search is the lowercased body and author name.
	Search string
}

// ItemID is the feed item identity ("project:<id>" or "initiative:<id>").
func (entry *PulseFeedEntry) ItemID() string { return entry.Kind + ":" + entry.UpdateID }

// PulseFeedSnapshot is a read-only view of the workspace's update records and
// the metadata the feed rules need. Every slice and map is shared with the
// committed workspace snapshot, which writers replace instead of editing, so
// callers must never modify them.
type PulseFeedSnapshot struct {
	Workspace           domain.Workspace
	WorkspaceSettings   domain.WorkspaceSettings
	Recent              []*PulseFeedEntry // newest first
	Popular             []*PulseFeedEntry // highest score first
	Projects            []domain.Project
	Initiatives         []domain.Initiative
	InitiativeRelations []domain.InitiativeRelation
	ProjectUpdates      map[string][]domain.ProjectUpdate
	InitiativeUpdates   map[string][]domain.InitiativeUpdate
	Subscriptions       []domain.Subscription
	Teams               []domain.Team
	TeamSettings        map[string]domain.TeamSettings
	UserSettings        map[string]domain.UserSettings
	Users               []domain.User
	ProjectStatuses     []domain.ProjectStatus
	SavedViews          []domain.SavedView
	Settings            map[string]any
}

// pulsePopularEpoch anchors Linear's Popular ordering:
// ln(comments + reactions + 1) + (createdAt - 2024-09-06) / 1 day.
var pulsePopularEpoch = time.Date(2024, 9, 6, 0, 0, 0, 0, time.UTC)

// PulsePopularScore is the Popular tab score of an update.
func PulsePopularScore(comments, reactions int, createdAt time.Time) float64 {
	return math.Log(float64(comments+reactions+1)) + createdAt.Sub(pulsePopularEpoch).Seconds()/86400
}

// pulseIndex is a workspace's ordered update index. It is keyed by a
// fingerprint of everything the index is built from (update identity, dates,
// author, health, comment and reaction counts, body, users' handles), so it
// survives writes that copy the workspace without changing updates, and it
// holds no reference to the update collections it was built from.
type pulseIndex struct {
	fingerprint     uint64
	recent, popular []*PulseFeedEntry
	// ready is closed once recent and popular are set; concurrent requests
	// for the same fingerprint wait for the one build.
	ready chan struct{}
}

type pulseIndexCache struct {
	mu      sync.Mutex
	entries map[string]*pulseIndex
	builds  atomic.Int64
}

var pulseSeed = maphash.MakeSeed()

// pulseIndexFingerprint hashes the index inputs. Map order is random, so
// per-update hashes are combined with a sum.
func pulseIndexFingerprint(projectUpdates map[string][]domain.ProjectUpdate, initiativeUpdates map[string][]domain.InitiativeUpdate, users []domain.User) uint64 {
	var total uint64
	var hash maphash.Hash
	hash.SetSeed(pulseSeed)
	item := func(kind, sourceID, id, body string, bodyData map[string]any, health string, createdAt time.Time, editedAt *time.Time, author domain.User, comments int, reactions map[string][]string) {
		hash.Reset()
		for _, value := range []string{kind, sourceID, id, health, author.ID, author.Name, author.DisplayName, body} {
			hash.WriteString(value)
			hash.WriteByte(0)
		}
		count := 0
		for _, users := range reactions {
			count += len(users)
		}
		edited := int64(0)
		if editedAt != nil {
			edited = editedAt.UnixNano()
		}
		var numbers [5]uint64
		numbers[0], numbers[1], numbers[2], numbers[3], numbers[4] = uint64(createdAt.UnixNano()), uint64(edited), uint64(comments), uint64(count), uint64(len(bodyData))
		for _, number := range numbers {
			var raw [8]byte
			for i := range raw {
				raw[i] = byte(number >> (8 * i))
			}
			hash.Write(raw[:])
		}
		total += hash.Sum64()
	}
	for sourceID, updates := range projectUpdates {
		for _, update := range updates {
			item("project", sourceID, update.ID, update.Body, update.BodyData, update.Health, update.CreatedAt, update.EditedAt, update.User, len(update.Comments), update.Reactions)
		}
	}
	for sourceID, updates := range initiativeUpdates {
		for _, update := range updates {
			item("initiative", sourceID, update.ID, update.Body, update.BodyData, update.Health, update.CreatedAt, update.EditedAt, update.User, len(update.Comments), update.Reactions)
		}
	}
	hash.Reset()
	for _, user := range users {
		for _, value := range []string{user.ID, user.Email, user.Name, user.DisplayName, user.Username} {
			hash.WriteString(value)
			hash.WriteByte(0)
		}
	}
	return total ^ hash.Sum64()
}

// PulseSources is PulseFeed without the ordered update index (Recent and
// Popular are nil): visibility checks on projects and initiatives, such as
// filtering realtime events per connection, need no fingerprint or index.
func (s *SQLiteStore) PulseSources(workspace string) (*PulseFeedSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	if !ok {
		return nil, false
	}
	return pulseSnapshotOf(data), true
}

func pulseSnapshotOf(data domain.Bootstrap) *PulseFeedSnapshot {
	return &PulseFeedSnapshot{
		Workspace: data.Workspace, WorkspaceSettings: data.WorkspaceSettings, Projects: data.Projects, Initiatives: data.Initiatives,
		InitiativeRelations: data.InitiativeRelations, ProjectUpdates: data.ProjectUpdates, InitiativeUpdates: data.InitiativeUpdates,
		Subscriptions: data.Subscriptions, Teams: data.Teams, TeamSettings: data.TeamSettings, UserSettings: data.UserSettings, Users: data.Users,
		ProjectStatuses: data.ProjectStatuses, SavedViews: data.SavedViews, Settings: data.Settings,
	}
}

// PulseIndexBuilds counts index rebuilds (tests check the cache with it).
func (s *SQLiteStore) PulseIndexBuilds() int64 { return s.pulseIndexes.builds.Load() }

// PulseFeed returns the Pulse view of a workspace. The ordered update index
// is rebuilt only when its inputs changed; other requests (and writes that
// only copied the workspace) reuse it, and concurrent requests share one
// rebuild.
func (s *SQLiteStore) PulseFeed(workspace string) (*PulseFeedSnapshot, bool) {
	s.mu.RLock()
	if workspace == "" {
		workspace = s.lastWorkspaceKey
	}
	data, ok := s.workspaces[workspace]
	s.mu.RUnlock()
	cache := &s.pulseIndexes
	if !ok {
		cache.mu.Lock()
		delete(cache.entries, workspace)
		cache.mu.Unlock()
		return nil, false
	}
	snapshot := pulseSnapshotOf(data)
	fingerprint := pulseIndexFingerprint(data.ProjectUpdates, data.InitiativeUpdates, data.Users)
	cache.mu.Lock()
	if cache.entries == nil {
		cache.entries = map[string]*pulseIndex{}
	}
	cached := cache.entries[workspace]
	build := cached == nil || cached.fingerprint != fingerprint
	if build {
		cached = &pulseIndex{fingerprint: fingerprint, ready: make(chan struct{})}
		cache.entries[workspace] = cached
	}
	cache.mu.Unlock()
	if build {
		cached.recent, cached.popular = buildPulseIndex(data.ProjectUpdates, data.InitiativeUpdates, data.Users)
		cache.builds.Add(1)
		close(cached.ready)
		s.prunePulseIndexes()
	} else {
		<-cached.ready
	}
	snapshot.Recent, snapshot.Popular = cached.recent, cached.popular
	return snapshot, true
}

// prunePulseIndexes drops indexes of workspaces the store no longer holds.
func (s *SQLiteStore) prunePulseIndexes() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.pulseIndexes.mu.Lock()
	defer s.pulseIndexes.mu.Unlock()
	for key := range s.pulseIndexes.entries {
		if _, ok := s.workspaces[key]; !ok {
			delete(s.pulseIndexes.entries, key)
		}
	}
}

var mentionTokenPattern = regexp.MustCompile(`@([\p{L}\p{N}._%+\-@]+)`)

func buildPulseIndex(projectUpdates map[string][]domain.ProjectUpdate, initiativeUpdates map[string][]domain.InitiativeUpdate, users []domain.User) ([]*PulseFeedEntry, []*PulseFeedEntry) {
	handles := make(map[string]string, len(users)*3)
	for _, user := range users {
		for _, handle := range []string{user.Email, user.Name, strings.ReplaceAll(user.DisplayName, " ", ""), user.Username} {
			if handle = strings.ToLower(strings.TrimSpace(handle)); handle != "" {
				handles[handle] = user.ID
			}
		}
	}
	entries := []*PulseFeedEntry{}
	add := func(kind, sourceID, id, body string, bodyData map[string]any, health string, createdAt time.Time, author domain.User, comments int, reactions map[string][]string) {
		count := 0
		for _, users := range reactions {
			count += len(users)
		}
		entry := &PulseFeedEntry{Kind: kind, SourceID: sourceID, UpdateID: id, CreatedAt: createdAt, AuthorID: author.ID, Health: health, Comments: comments, Reactions: count}
		entry.Score = PulsePopularScore(comments, count, createdAt)
		entry.Mentions = PulseMentions(body, bodyData, handles)
		entry.Search = strings.ToLower(body + "\n" + author.DisplayName + "\n" + author.Name)
		entries = append(entries, entry)
	}
	for sourceID, updates := range projectUpdates {
		for _, update := range updates {
			add("project", sourceID, update.ID, update.Body, update.BodyData, update.Health, update.CreatedAt, update.User, len(update.Comments), update.Reactions)
		}
	}
	for sourceID, updates := range initiativeUpdates {
		for _, update := range updates {
			add("initiative", sourceID, update.ID, update.Body, update.BodyData, update.Health, update.CreatedAt, update.User, len(update.Comments), update.Reactions)
		}
	}
	recent := slices.Clone(entries)
	slices.SortFunc(recent, ComparePulseRecent)
	popular := entries
	slices.SortFunc(popular, ComparePulsePopular)
	return recent, popular
}

// ComparePulseRecent orders newest first with the item id as tiebreaker.
func ComparePulseRecent(a, b *PulseFeedEntry) int {
	if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
		return c
	}
	return strings.Compare(b.ItemID(), a.ItemID())
}

// ComparePulsePopular orders by score, then newest first, then item id.
func ComparePulsePopular(a, b *PulseFeedEntry) int {
	if a.Score != b.Score {
		if a.Score > b.Score {
			return -1
		}
		return 1
	}
	return ComparePulseRecent(a, b)
}

// PulseMentions lists users mentioned in an update body: structured mention
// nodes plus @handles that name a workspace member.
func PulseMentions(body string, bodyData map[string]any, handles map[string]string) []string {
	result := []string{}
	collectStructuredMentions(bodyData, &result)
	for _, match := range mentionTokenPattern.FindAllStringSubmatch(body, -1) {
		token := strings.ToLower(strings.TrimRight(match[1], ".,;:!?"))
		if id, ok := handles[token]; ok && !slices.Contains(result, id) {
			result = append(result, id)
		}
	}
	return result
}

func collectStructuredMentions(value any, result *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		attrs, _ := typed["attrs"].(map[string]any)
		// A `mention` node names any workspace resource; only user mentions carry a person's id.
		if typed["type"] == "mention" && domain.IsUserMention(attrs) || typed["type"] == "userMention" || typed["type"] == "user" {
			for _, source := range []map[string]any{typed, attrs} {
				for _, key := range []string{"userId", "id"} {
					if id, ok := source[key].(string); ok && id != "" && !slices.Contains(*result, id) {
						*result = append(*result, id)
					}
				}
			}
		}
		for _, child := range typed {
			collectStructuredMentions(child, result)
		}
	case []any:
		for _, child := range typed {
			collectStructuredMentions(child, result)
		}
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(typed, &decoded) == nil {
			collectStructuredMentions(decoded, result)
		}
	}
}

// VisibleTeamIDsFor lists the teams a member can see, matching the issue and
// project visibility rules. memberships may hold every member's rows.
func VisibleTeamIDsFor(teams []domain.Team, settings map[string]domain.TeamSettings, memberships []domain.TeamMember, userID, role string) []string {
	return visibleIssueTeams(domain.Bootstrap{Teams: teams, TeamSettings: settings, TeamMembers: memberships}, userID, role)
}
