package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
)

// The resident document write (bounded metadata on; design in
// docs/bounded-metadata-performance.md, "What document and project edits
// spend").
//
// The scoped image writer (mutateBoundedScopedImage) derives a document
// edit's callback inputs, its row comparison, its stored rows and its webhook
// and realtime payloads from the spool, streaming the document's body
// several times, all under the workspace write lock. This path derives all of
// them from one decoded pair instead: the edited records' values in the
// shared decoded image of the installed generation the writer pinned
// (residentImage), and the callback's result. Everything except the SQL write
// happens before the lock; under it the writer only checks that the stored
// metadata is still the pinned generation, writes the changed rows and the
// event, commits and installs the next generation.
//
// What the edit's outputs depend on besides the edited records, and how this
// path stays exact:
//
//   - Display refresh (refreshLegacyDisplayImage) rewrites the people embedded
//     in members, projects, initiatives, project and initiative updates and
//     the loaded comments, activities and notifications with the user
//     directory's current values, and the scoped writer persists whatever it
//     rewrote. This path runs only when that refresh changes nothing: the
//     image check is computed once per generation (residentDisplayCurrent)
//     and the loaded content is checked per edit.
//   - Actor substitution (prepareLegacyActorImage) replaces the viewer and
//     the actor's user record with the request's actor, and the scoped writer
//     persists both. This path runs only when the stored viewer and the
//     stored user equal the actor (Via aside, which is never stored).
//   - The callback reads the workspace's authorization collections, the user
//     directory, the document, its subscriptions and linked projects, the
//     document's comments, activities and notifications (SQL rows) and the
//     former-member and collaboration lookups (SQL). The metadata come from
//     the pinned generation, verified current under the lock by the stored
//     stamp; the content rows are read again under the lock and must be
//     unchanged; an edit whose callback read a lookup takes the scoped
//     writer.
//
// Anything else (favorites, notifications, issues, a document stored twice,
// a stale or unknown generation, no image) takes the scoped writer, which
// stays the reference: in tests (residentWriteVerify) every edit this path
// makes is also run through the scoped writer up to its commit, and its
// callback inputs, payloads, SQL rows and next generation must be identical.

// residentWritesOff disables the resident document write (tests).
var residentWritesOff atomic.Bool

// residentWriteVerify runs every resident write's reference comparison
// (residentWriteShadow). On in tests.
var residentWriteVerify = func() *atomic.Bool {
	b := &atomic.Bool{}
	b.Store(testing.Testing())
	return b
}()

// Resident write counters (load measurement and tests).
var (
	residentWritesMade     atomic.Int64
	residentWritesFellBack atomic.Int64
	residentWritesVerified atomic.Int64
	residentOrdinalWalks   atomic.Int64
	residentWriteReasons   sync.Map // reason -> *atomic.Int64
)

// ResidentWriteStatsForTesting reports the resident write counters, with the
// fallback reasons as "fallback:<reason>".
func ResidentWriteStatsForTesting() map[string]int64 {
	stats := map[string]int64{"made": residentWritesMade.Load(), "fellBack": residentWritesFellBack.Load(), "verified": residentWritesVerified.Load(), "ordinalWalks": residentOrdinalWalks.Load()}
	residentWriteReasons.Range(func(key, value any) bool {
		stats["fallback:"+key.(string)] = value.(*atomic.Int64).Load()
		return true
	})
	return stats
}

// residentWriteBeforeCommit runs between a resident write's preparation and
// its commit (tests).
var residentWriteBeforeCommit atomic.Pointer[func()]

// SetResidentWriteBeforeCommitForTesting installs hook to run between a
// resident write's preparation and its commit, and returns a restore
// function.
func SetResidentWriteBeforeCommitForTesting(hook func()) func() {
	if hook == nil {
		residentWriteBeforeCommit.Store(nil)
	} else {
		residentWriteBeforeCommit.Store(&hook)
	}
	return func() { residentWriteBeforeCommit.Store(nil) }
}

// SetResidentWritesForTesting turns the resident document write on or off
// and returns a restore function.
func SetResidentWritesForTesting(on bool) func() {
	previous := !residentWritesOff.Load()
	residentWritesOff.Store(!on)
	return func() { residentWritesOff.Store(!previous) }
}

// residentFallback is a reason the resident write leaves an edit to the
// scoped writer. It is never returned to callers.
type residentFallback string

func (r residentFallback) Error() string { return "resident write falls back: " + string(r) }

func residentFallbackReason(err error) (string, bool) {
	var fallback residentFallback
	if errors.As(err, &fallback) {
		return string(fallback), true
	}
	return "", false
}

func noteResidentFallback(reason string) {
	residentWritesFellBack.Add(1)
	counter, _ := residentWriteReasons.LoadOrStore(reason, &atomic.Int64{})
	counter.(*atomic.Int64).Add(1)
}

// residentWriteOutcome is what a committed resident write hands back to the
// scoped writer's caller.
type residentWriteOutcome struct {
	written  bool
	event    domain.DomainEvent
	realtime json.RawMessage
}

// residentRecordChange is one stored record the edit replaces.
type residentRecordChange struct {
	field         string
	id            string
	ordinal       int
	order         int
	before, after any
	raw, previous []byte
	// displayOnly: the rows differ only in display data, which the
	// scoped writer's array fast path does not write.
	displayOnly bool
}

// residentDocumentEdit is one prepared edit: everything the commit writes.
type residentDocumentEdit struct {
	store     *SQLiteStore
	workspace string
	eventType string
	stored    *metadataLogicalSource
	image     *coldDecodedImage
	owner     *ResourceRead
	profile   *documentUpdateImageProfile
	keys      []string
	content   domain.Bootstrap
	data      domain.Bootstrap
	before    domain.Bootstrap
	aggregate string
	changes   []residentRecordChange
	payload   json.RawMessage
	previous  json.RawMessage
	realtime  json.RawMessage
	users     []domain.User
	candidate *metadataLogicalSource
	next      *coldDecodedImage
	shadow    *residentWriteShadow
	// viewerChanged: the stored viewer is another principal; the edit
	// stores the actor as the viewer.
	viewerChanged bool
	actor         domain.User
}

func (e *residentDocumentEdit) release() {
	if e.candidate != nil {
		e.candidate.Release()
		e.candidate = nil
	}
}

// writeResidentDocument makes the document edit from the resident image when
// it can. handled is false when the edit is left to the scoped writer
// (nothing was written); otherwise err is the edit's outcome.
func (s *SQLiteStore) writeResidentDocument(ctx context.Context, workspace, eventType string, payload any, request *documentUpdateImageProfile, mutate func(*domain.Bootstrap) (string, error), webhookEnabled bool, outcome *residentWriteOutcome) (handled bool, err error) {
	if residentWritesOff.Load() || s.metadataResidency == nil || residentWriteShadowFrom(ctx) != nil ||
		ctx.Value(metadataTimestampOperationKey{}) != nil || len(request.scope.IssueIDs) != 0 || coldDecodeCacheBudget() <= 0 {
		return false, nil
	}
	for attempt := 1; ; attempt++ {
		handled, reason, err := s.writeResidentDocumentOnce(ctx, workspace, eventType, payload, request, mutate, webhookEnabled, outcome)
		if handled || attempt >= residentWriteAttempts || !residentRaceReason(reason) {
			return handled, err
		}
		// Another write installed or committed a generation while this edit
		// was prepared: the edit is prepared again on the new one (the
		// scoped writer would rerun the callback as well). A generation
		// behind the stored stamp (another server wrote) is reloaded first
		// where the scoped writer reloads it too (servers coordinated by a
		// workspace lock); a single server's scoped writer instead merges
		// its installed generation into the stored rows, so the edit is
		// left to it.
		if reason == residentBehind {
			if s.coordinator == nil {
				return false, nil
			}
			if err := s.ReloadWorkspace(ctx, workspace); err != nil {
				return false, nil
			}
		}
		traceMark(ctx, "retry")
	}
}

// residentWriteAttempts bounds the preparations of one resident edit that
// concurrent writes may invalidate before it is left to the scoped writer.
const residentWriteAttempts = 3

const residentBehind = "generation behind the stored stamp"

// residentRaceReason: the fallback was caused by a concurrent write, so the
// edit can be prepared again on the generation that write installed.
func residentRaceReason(reason string) bool {
	switch reason {
	case residentBehind, "generation changed during the callback", "stored stamp moved before the rebase",
		"generation replaced before the lock", "stored stamp moved", "concurrent commit",
		"generation replaced during the reference run":
		return true
	}
	return false
}

func (s *SQLiteStore) writeResidentDocumentOnce(ctx context.Context, workspace, eventType string, payload any, request *documentUpdateImageProfile, mutate func(*domain.Bootstrap) (string, error), webhookEnabled bool, outcome *residentWriteOutcome) (handled bool, reason string, err error) {
	edit := &residentDocumentEdit{store: s, workspace: workspace, eventType: eventType,
		profile: &documentUpdateImageProfile{request: request.request, scope: request.scope}}
	defer edit.release()
	traceStart(ctx, "resident")
	err = edit.prepare(ctx, payload, mutate, webhookEnabled)
	if errors.Is(err, ErrMetadataWorkBudget) {
		// The scoped writer can stream records the in-memory edit cannot
		// admit. Nothing has reached the write transaction yet.
		err = residentFallback("output work budget")
	}
	if err == nil {
		traceMark(ctx, "prepare")
		if residentWriteVerify.Load() {
			err = edit.verify(ctx, payload)
		}
	}
	if err == nil {
		if hook := residentWriteBeforeCommit.Load(); hook != nil {
			(*hook)()
		}
		err = edit.commit(ctx, outcome)
	}
	if reason, fallback := residentFallbackReason(err); fallback {
		// The scoped writer admits its own work and runs the callback
		// again: nothing of this attempt is kept.
		if edit.owner != nil {
			edit.owner.Close()
		}
		noteResidentFallback(reason)
		traceMark(ctx, "fallback:"+strings.ReplaceAll(reason, " ", "-"))
		return false, reason, nil
	}
	if err == nil && outcome.written {
		residentWritesMade.Add(1)
	}
	return true, "", err
}

// prepare pins the installed generation and its image, runs the callback on
// inputs taken from the image and derives every output of the edit.
func (e *residentDocumentEdit) prepare(ctx context.Context, payload any, mutate func(*domain.Bootstrap) (string, error), webhookEnabled bool) error {
	s := e.store
	stored, err := s.retainMetadataLogicalSource(ctx, e.workspace)
	if err != nil {
		return err
	}
	e.stored = stored
	// The generation is released when the edit's outputs are: the
	// callback's result may reference image values.
	owner := &ResourceRead{store: s, source: stored}
	e.owner = owner
	if !registerMetadataOutputClose(ctx, owner.Close) {
		owner.Close()
		return ErrBoundedMetadataCoverage
	}
	if stored.Stamp().revision < 0 || stored.behind.Load() {
		return residentFallback("generation stamp unknown")
	}
	if current, err := s.residentStampCurrent(ctx, nil, e.workspace, stored); err != nil {
		return err
	} else if !current {
		return residentFallback("generation behind the stored stamp")
	}
	if err := owner.reserve(ctx, 128<<10); err != nil {
		return err
	}
	image, ok, err := s.metadataResidency.residentImage(ctx, e.workspace, stored)
	if err != nil {
		return err
	}
	if !ok {
		return residentFallback("no resident image")
	}
	e.image = image
	if image.data.Workspace.ID == "" || image.data.Workspace.ID != stored.Stamp().workspaceID {
		return residentFallback("workspace identity")
	}
	if !image.displayCurrent() {
		return residentFallback("display refresh pending")
	}
	actor, hasActor := actorFromContext(ctx)
	actor = mutationInputActor(actor)
	if hasActor {
		user, viewer := residentActorState(image.data, actor)
		if !user {
			return residentFallback("actor differs from its stored user")
		}
		// Another principal's edit makes the actor the stored viewer, as
		// the scoped writer's actor substitution does.
		e.viewerChanged, e.actor = !viewer, actor
	}
	var role string
	var teams []string
	applyRole := false
	if hasActor {
		role, teams, applyRole, err = s.boundedLegacyActorDirectory(ctx, image.data.Workspace.ID, actor.ID, owner)
		if err != nil {
			return err
		}
	}
	selected, best := residentDocumentSelect(image.data.Documents, e.profile.request.Ref)
	if best >= 0 && e.profile.request.ReadExistingIssue && image.data.Documents[best].IssueID != "" {
		return residentFallback("linked issue scope")
	}
	resource := e.profile.request.Ref
	if best >= 0 {
		resource = selected.ID
	}
	e.keys = scopedResourceKeys(append(slices.Clone(e.profile.scope.Resources), resource), nil)

	// The content rows and lookups are read in their own read transaction;
	// the commit reads the rows again under the lock.
	reader, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer reader.Rollback()
	rows := s.newIssueChildContentRows(reader, e.workspace)
	defer rows.Close()
	loaded, err := rows.loadMutationScope(ctx, e.keys)
	if err != nil {
		return err
	}
	e.content = residentContentCopy(loaded)
	if !residentContentDisplayCurrent(e.content, image.data.Users) {
		return residentFallback("content display refresh pending")
	}

	data := e.inputs(image.data, actor, hasActor, selected, best)
	data.Issues = []domain.Issue{}
	data.Comments, data.Activities = e.content.Comments, e.content.Activities
	data.Notifications, data.NotificationDeliveries = e.content.Notifications, e.content.NotificationDeliveries
	if applyRole {
		data.ViewerRole = role
	}
	if hasActor {
		data.ViewerTeamIDs = teams
	}
	// The callback owns its inputs; the image is shared and never written.
	data = exactCopyValue(reflect.ValueOf(data)).Interface().(domain.Bootstrap)
	if err := reserveCycleScheduleClone(ctx, owner, data); err != nil {
		return err
	}
	e.before = cloneCallbackInput(data)
	lookups, err := newIssueEditorLookups(ctx, &issueHotInputs{store: s, source: stored}, reader, e.workspace, data.Workspace.ID,
		domain.Issue{DocumentContent: &domain.DocumentContent{ID: scopedImageEditorDocument(e.profile, data)}})
	if err != nil {
		return err
	}
	defer lookups.close()
	lookups.install(&data)
	if err := e.profile.reserveCallback(ctx, owner, data, payload); err != nil {
		return err
	}
	traceMark(ctx, "load")
	aggregate, callbackErr := mutate(&data)
	data.FormerMembers, data.CollaborationUpdates, data.CollaborationWork = nil, nil, nil
	if err := lookups.check(); err != nil {
		return err
	}
	if lookups.formerLoaded || lookups.updatesLoaded || len(lookups.documents) != 0 {
		return residentFallback("callback read a lookup")
	}
	// The read transaction ends before anything else touches the database
	// (SQLite may run it on the one connection).
	lookups.close()
	rows.Close()
	_ = reader.Rollback()
	traceMark(ctx, "callback")
	if callbackErr != nil {
		// The answer was computed on the pinned generation: it is the
		// answer only while that generation is still the stored one.
		if current, err := s.residentStampCurrent(ctx, nil, e.workspace, stored); err != nil {
			return err
		} else if !current {
			return residentFallback("generation changed during the callback")
		}
		return callbackErr
	}
	if err := scopedImageCheckAggregate(ctx, e.profile, e.before, data, aggregate); err != nil {
		return err
	}
	if err := e.profile.validate(e.before, data); err != nil {
		return err
	}
	e.data, e.aggregate = data, aggregate
	return e.outputs(ctx, payload, actor, hasActor, webhookEnabled)
}

// inputs is the document callback's input as documentUpdateAttempt's
// callbackInputs reads it from the displayed, actor-substituted source, here
// taken from the image (whose display refresh and actor substitution change
// nothing). Containers are shared with the image until the caller copies.
func (e *residentDocumentEdit) inputs(image domain.Bootstrap, actor domain.User, hasActor bool, selected domain.Document, best int) domain.Bootstrap {
	var data domain.Bootstrap
	source, target := reflect.ValueOf(image), reflect.ValueOf(&data).Elem()
	for _, name := range documentUpdateCallbackFields {
		index, _, _ := metadataLogicalField(name)
		target.Field(index).Set(source.Field(index))
	}
	users := image.Users
	if hasActor {
		data.Viewer = actor
		users = residentActorUsers(image.Users, actor)
	}
	e.users = users
	for _, user := range users {
		data.Users = append(data.Users, domain.User{ID: user.ID, Name: user.Name, Email: user.Email, App: user.App, Active: user.Active,
			AppTeamIDs: user.AppTeamIDs, AppScopes: user.AppScopes, AppAllPublicTeams: user.AppAllPublicTeams})
	}
	for _, subscription := range image.PushSubscriptions {
		data.PushSubscriptions = append(data.PushSubscriptions, domain.PushSubscription{UserID: subscription.UserID, Enabled: subscription.Enabled})
	}
	profile := e.profile
	profile.documentIndex = -1
	if image.Documents != nil {
		data.Documents = []domain.Document{}
	}
	if best >= 0 {
		for ordinal, document := range image.Documents {
			if document.ID != selected.ID {
				continue
			}
			if ordinal == best {
				profile.documentIndex = len(data.Documents)
			}
			data.Documents = append(data.Documents, document)
			profile.documentOrdinals = append(profile.documentOrdinals, ordinal)
		}
	}
	for _, subscription := range image.Subscriptions {
		if best >= 0 && subscription.ResourceType == "document" && subscription.ResourceID == selected.ID {
			data.Subscriptions = append(data.Subscriptions, domain.Subscription{ResourceType: subscription.ResourceType, ResourceID: subscription.ResourceID, UserID: subscription.UserID})
		}
	}
	wanted := map[string]bool{}
	for _, id := range profile.request.ProjectIDs {
		wanted[id] = true
	}
	if profile.documentIndex >= 0 {
		for _, id := range data.Documents[profile.documentIndex].ProjectIDs {
			wanted[id] = true
		}
	}
	if image.Projects != nil {
		data.Projects = []domain.Project{}
	}
	for ordinal, project := range image.Projects {
		match := wanted[project.ID]
		if !match && best >= 0 {
			match = slices.ContainsFunc(project.Resources, func(resource domain.ProjectResource) bool { return resource.ID == selected.ID })
		}
		if match {
			data.Projects = append(data.Projects, domain.Project{ID: project.ID, Name: project.Name, TeamIDs: project.TeamIDs, Resources: project.Resources})
			profile.projectOrdinals = append(profile.projectOrdinals, ordinal)
		}
	}
	return data
}

// outputs derives the changed rows, the payloads and the next generation from
// the image's records and the callback's result.
func (e *residentDocumentEdit) outputs(ctx context.Context, payload any, actor domain.User, hasActor, webhookEnabled bool) error {
	image, before, after := e.image.data, e.before, e.data
	if !reflect.DeepEqual(before.Favorites, after.Favorites) {
		return residentFallback("favorites changed")
	}
	if !reflect.DeepEqual(before.Issues, after.Issues) || !reflect.DeepEqual(before.Comments, after.Comments) ||
		!reflect.DeepEqual(before.Activities, after.Activities) || !reflect.DeepEqual(before.Notifications, after.Notifications) ||
		!reflect.DeepEqual(before.NotificationDeliveries, after.NotificationDeliveries) {
		return residentFallback("records beside the document changed")
	}
	profile := e.profile
	if len(profile.documentOrdinals) > 1 {
		return residentFallback("document stored more than once")
	}
	documents := slices.Clone(image.Documents)
	for index, ordinal := range profile.documentOrdinals {
		if reflect.DeepEqual(before.Documents[index], after.Documents[index]) {
			continue
		}
		next := after.Documents[index]
		documents[ordinal] = next
		e.changes = append(e.changes, residentRecordChange{field: "documents", id: next.ID, ordinal: ordinal, before: image.Documents[ordinal], after: next})
	}
	projects, cloned := image.Projects, false
	for index, ordinal := range profile.projectOrdinals {
		if reflect.DeepEqual(before.Projects[index].Resources, after.Projects[index].Resources) {
			continue
		}
		if !cloned {
			projects, cloned = slices.Clone(image.Projects), true
		}
		next := image.Projects[ordinal]
		next.Resources = after.Projects[index].Resources
		projects[ordinal] = next
		e.changes = append(e.changes, residentRecordChange{field: "projects", id: next.ID, ordinal: ordinal, before: image.Projects[ordinal], after: next})
	}
	if len(e.changes) == 0 {
		return residentFallback("no record changed")
	}
	raw, release, err := e.store.encodeMetadataMutationPayload(ctx, payload)
	if err != nil {
		return err
	}
	e.payload = raw
	e.owner.release = append(e.owner.release, release)
	// The aggregate's previous values and realtime entity, as flag-off
	// computes them: before is the stored generation with the loaded
	// content, after is the written generation with the callback's.
	beforeImage := image
	beforeImage.Issues = []domain.Issue{}
	beforeImage.Comments, beforeImage.Activities = e.content.Comments, e.content.Activities
	beforeImage.Notifications, beforeImage.NotificationDeliveries = e.content.Notifications, e.content.NotificationDeliveries
	afterImage := image
	afterImage.Documents, afterImage.Projects = documents, projects
	if hasActor {
		afterImage.Viewer, afterImage.Users = actor, e.users
	}
	afterImage.Issues, afterImage.Comments, afterImage.Activities = after.Issues, after.Comments, after.Activities
	afterImage.Notifications, afterImage.NotificationDeliveries = after.Notifications, after.NotificationDeliveries
	resolve := func(prior bool) (metadataAggregateNode, error) {
		data := afterImage
		if prior {
			data = beforeImage
		}
		if entity := nativeAggregateEntity(data, e.aggregate); entity != nil {
			return metadataAggregateNativeNode{value: reflect.ValueOf(entity)}, nil
		}
		return nil, ctx.Err()
	}
	publication, err := e.store.prepareMetadataAggregateResolvedOutputs(ctx, resolve, e.aggregate, webhookEnabled, e.payload, e.eventType, true)
	if err != nil {
		return err
	}
	e.previous, e.realtime = publication.PreviousValues, publication.RealtimePayload
	e.owner.release = append(e.owner.release, publication.Close)
	// The rows: each record rebased onto its stored row as the scoped
	// writer rebases it under its lock (rebaseMetadata). The stored rows are
	// read at the pinned generation's stamp; the commit proves the stamp
	// unchanged, so they are the rows it writes over.
	if err := e.rebase(ctx); err != nil {
		return err
	}
	// The next generation: the pinned one with the changed records
	// replaced (each field's changes are in ordinal order), every other
	// frame shared.
	nodes := map[string]*documentUpdateCollection{}
	for _, change := range e.changes {
		collection := nodes[change.field]
		if collection == nil {
			collection = &documentUpdateCollection{}
			nodes[change.field] = collection
		}
		collection.ordinals = append(collection.ordinals, change.ordinal)
		collection.nodes = append(collection.nodes, legacyMetadataWriteNativeRecord{value: reflect.ValueOf(change.after)})
	}
	next, err := e.stored.Transform(ctx, func(name string, original metadataLogicalNode) (metadataAggregateNode, bool, error) {
		if collection := nodes[name]; collection != nil {
			return documentUpdateCollection{base: original, ordinals: collection.ordinals, nodes: collection.nodes}, true, nil
		}
		if name == "viewer" && e.viewerChanged {
			// As prepareLegacyActorImage writes it.
			return legacyActorUserNode{value: reflect.ValueOf(e.actor)}, true, nil
		}
		return metadataAggregateStoredNode{value: original}, false, nil
	})
	if err != nil {
		return err
	}
	defer next.Release()
	if e.candidate, err = metadataLogicalInstalledCandidate(ctx, next); err != nil {
		return err
	}
	traceMark(ctx, "generation")
	return e.nextImage(ctx)
}

// nextImage builds the decoded image of the next generation before the lock:
// the pinned image's values for every frame the next generation shares with
// it, and the edit's own values for the records it replaced (seeded, never
// decoded again). The commit makes it the resident image once the
// generation is installed.
func (e *residentDocumentEdit) nextImage(ctx context.Context) error {
	seeds := coldDecodeSeeds{}
	for _, change := range e.changes {
		if seeds[change.field] == nil {
			seeds[change.field] = map[int]reflect.Value{}
		}
		// The image is shared and immutable: it owns its copy.
		seeds[change.field][change.ordinal] = exactCopyValue(reflect.ValueOf(change.after))
	}
	image, err := buildColdDecodedImage(withColdDecodeSeeds(ctx, seeds), e.candidate, e.image, nil, true)
	if err != nil {
		return err
	}
	if residentWriteVerify.Load() {
		fresh, err := buildColdDecodedImage(ctx, e.candidate, nil, nil, false)
		if err != nil {
			return err
		}
		if field := residentBootstrapDifference(image.data, fresh.data); field != "" {
			return fmt.Errorf("%w: the seeded image of the next generation differs from its decode in %s", ErrBoundedMetadataCoverage, field)
		}
	}
	e.next = image
	traceMark(ctx, "image")
	return nil
}

// commit writes the prepared edit when the stored metadata are still the
// pinned generation.
func (e *residentDocumentEdit) commit(ctx context.Context, outcome *residentWriteOutcome) error {
	s := e.store
	apply := func(ctx context.Context) error {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		traceLocked(ctx)
		defer traceUnlocked(ctx)
		// A workspace no longer installed (or installed anew) is left to
		// the scoped writer, which reports it.
		installed, err := s.retainMetadataLogicalSource(ctx, e.workspace)
		if err != nil {
			return residentFallback("workspace not installed")
		}
		same := installed == e.stored
		installed.Release()
		if !same {
			return residentFallback("generation replaced before the lock")
		}
		tx, err := s.db.BeginTx(ctx, s.recordTxOptions())
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if s.metadataResidency.requireCoverage {
			if err := checkRecordJournalCoverage(ctx, tx, e.workspace); err != nil {
				return err
			}
		}
		baseStamp, err := readMetadataStamp(ctx, tx, e.workspace)
		if err != nil {
			return err
		}
		if baseStamp != e.stored.Stamp() {
			return residentFallback("stored stamp moved")
		}
		if err := e.checkContent(ctx, tx); err != nil {
			return err
		}
		if err := e.writeRows(ctx, tx); err != nil {
			return err
		}
		if err := e.writeViewer(ctx, tx); err != nil {
			return err
		}
		traceMark(ctx, "records")
		if err := e.writeEvent(ctx, tx, baseStamp, outcome); err != nil {
			return err
		}
		candidate := e.candidate
		publication, err := s.prepareLegacyResidentPublication(ctx, tx, e.workspace, candidate, baseStamp, true, true)
		if err != nil {
			return err
		}
		e.candidate = nil
		defer publication.Close()
		if e.shadow != nil {
			if err := e.shadow.compareCommit(ctx, tx, e.workspace, candidate); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			if errors.Is(err, ErrConcurrentWrite) {
				// Another server committed metadata since the stamp was
				// read: the scoped writer rebases onto it.
				return residentFallback("concurrent commit")
			}
			return err
		}
		outcome.written = true
		if err := publication.Install(); err != nil {
			return err
		}
		if e.next != nil {
			// Kept only when the candidate is the installed generation.
			s.metadataResidency.storeColdImage(e.workspace, e.next, coldDecodeCacheBudget())
		}
		traceMark(ctx, "commit")
		return nil
	}
	if s.coordinator != nil {
		err := s.coordinator.WithWorkspaceLock(ctx, e.workspace, func() error { return apply(ctx) })
		return residentDeadlockFallback(err, outcome)
	}
	return residentDeadlockFallback(apply(ctx), outcome)
}

// residentDeadlockFallback turns a transaction the database rolled back to
// break a lock cycle into a race: nothing was written, and the edit is
// prepared again.
func residentDeadlockFallback(err error, outcome *residentWriteOutcome) error {
	if !outcome.written && databaseDeadlock(err) {
		return residentFallback("concurrent commit")
	}
	if !outcome.written && databaseLockWait(err) {
		return fmt.Errorf("%w: %w", ErrDatabaseWriteBusy, err)
	}
	return err
}

// checkContent reads the edit's content rows again in the write transaction:
// the stamp does not cover them.
func (e *residentDocumentEdit) checkContent(ctx context.Context, tx *sqlTx) error {
	rows := e.store.newIssueChildContentRows(tx, e.workspace)
	defer rows.Close()
	loaded, err := rows.loadMutationScope(ctx, e.keys)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(residentContentCopy(loaded), e.content) {
		return residentFallback("content rows changed")
	}
	return nil
}

// rebase replaces each change's before and after with what rebaseMetadata
// makes of them against the stored row, and serializes both.
func (e *residentDocumentEdit) rebase(ctx context.Context) error {
	s := e.store
	reader, err := s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return err
	}
	defer reader.Rollback()
	if current, err := s.residentStampCurrent(ctx, reader, e.workspace, e.stored); err != nil {
		return err
	} else if !current {
		return residentFallback("stored stamp moved before the rebase")
	}
	refs := newIssueReferences(domain.Bootstrap{Users: e.users})
	for index := range e.changes {
		if err := e.rebaseRecord(ctx, reader, refs, &e.changes[index]); err != nil {
			return err
		}
	}
	return nil
}

func (e *residentDocumentEdit) rebaseRecord(ctx context.Context, reader *sqlTx, refs issueReferences, change *residentRecordChange) error {
	field, kind, ok := metadataRecordType(change.field)
	if !ok || !utf8.ValidString(change.id) {
		return residentFallback("record not rebased by row")
	}
	work := &ResourceRead{store: e.store}
	defer work.Close()
	length := work.byteLength("data")
	const from = ` FROM workspace_metadata_records WHERE workspace_key=? AND field=? AND record_key=?`
	var size int64
	if err := reader.QueryRowContext(ctx, "SELECT "+length+from, e.workspace, change.field, change.id).Scan(&size); errors.Is(err, sql.ErrNoRows) {
		return residentFallback("record row missing")
	} else if err != nil {
		return err
	}
	if size < 0 || size > int64(metadataWorkBytes()/3) {
		return ErrMetadataWorkBudget
	}
	if err := work.reserve(ctx, uint64(size)*3+4096); err != nil {
		return err
	}
	var row []byte
	if err := reader.QueryRowContext(ctx, "SELECT CASE WHEN "+length+"<=? THEN data ELSE NULL END,collection_order"+from,
		size, e.workspace, change.field, change.id).Scan(&row, &change.order); err != nil {
		return err
	}
	if row == nil || int64(len(row)) != size {
		return residentFallback("stored stamp moved before the rebase")
	}
	base, releaseBase, err := e.store.encodeMetadataMutationPayload(ctx, change.before)
	if err != nil {
		return err
	}
	defer func() {
		if releaseBase != nil {
			releaseBase()
		}
	}()
	ours, releaseOurs, err := e.store.encodeMetadataMutationPayload(ctx, change.after)
	if err != nil {
		return err
	}
	defer func() {
		if releaseOurs != nil {
			releaseOurs()
		}
	}()
	// Display comparison parses member maps and may encode display-stripped
	// copies. The canonical read and the two typed encodings already own
	// their output buffers.
	if err := work.reserve(ctx, uint64(len(base)+len(ours))*8+uint64(size)*5+64<<10); err != nil {
		return err
	}
	same, err := storedRecordEqualOwned(ctx, work, reader, refs, change.field, row, base)
	if err != nil {
		return err
	}
	if !same {
		result, err := mergeWithStoredRecordOwned(ctx, work, refs, field, kind, change.id, base, ours, row)
		if err != nil {
			return err
		}
		if result.conflict {
			return residentFallback("record conflicts with its stored row")
		}
		if !result.unchanged {
			if err := e.adoptRebasedRecord(ctx, work, change, kind, result); err != nil {
				return err
			}
			releaseBase()
			releaseOurs()
			releaseBase, releaseOurs = nil, nil
			base, releaseBase, err = e.store.encodeMetadataMutationPayload(ctx, change.before)
			if err != nil {
				return err
			}
			ours, releaseOurs, err = e.store.encodeMetadataMutationPayload(ctx, change.after)
			if err != nil {
				return err
			}
		}
	}
	change.displayOnly = refs.equalDisplayData(base, ours)
	change.previous, change.raw = base, ours
	e.owner.release = append(e.owner.release, releaseBase, releaseOurs)
	releaseBase, releaseOurs = nil, nil
	return nil
}

func (e *residentDocumentEdit) adoptRebasedRecord(ctx context.Context, work *ResourceRead, change *residentRecordChange, kind reflect.Type, result storedMerge) error {
	for _, record := range []struct {
		raw    []byte
		target *any
	}{{result.theirs, &change.before}, {result.merged, &change.after}} {
		cost, err := largeRowTypedJSONWork(ctx, work, record.raw, kind)
		if err != nil {
			return err
		}
		if err := e.owner.reserve(ctx, cost); err != nil {
			return err
		}
		value, err := decodeMetadataRecord(kind, record.raw, reflect.ValueOf(*record.target))
		if err != nil {
			return err
		}
		*record.target = value.Interface()
	}
	return nil
}

// writeRows writes each changed record's row as the scoped writer's array
// fast path does (writeArray): at its stored order, skipped when only its
// display data differ (compared before the lock).
func (e *residentDocumentEdit) writeRows(ctx context.Context, tx *sqlTx) error {
	for _, change := range e.changes {
		if change.displayOnly {
			continue
		}
		if err := writeOwnedMetadataRecordRow(ctx, e.store, tx, e.workspace, metadataRecordKey{change.field, change.id}, metadataRecord{data: change.raw, order: change.order}, change.previous); err != nil {
			return err
		}
	}
	return nil
}

// writeViewer stores the actor as the workspace's viewer (a root field) when
// the edit changes it, as the scoped writer's metadata persistence does.
func (e *residentDocumentEdit) writeViewer(ctx context.Context, tx *sqlTx) error {
	if !e.viewerChanged {
		return nil
	}
	id := e.image.data.Workspace.ID
	before := domain.Bootstrap{Workspace: domain.Workspace{ID: id}, Viewer: e.image.data.Viewer}
	target := domain.Bootstrap{Workspace: domain.Workspace{ID: id}, Viewer: e.actor}
	_, err := e.store.writeLegacyMetadataSlowFields(ctx, tx, e.workspace, before, target, []string{"viewer"}, newIssueReferences(domain.Bootstrap{Users: e.users}))
	return err
}

func (e *residentDocumentEdit) writeEvent(ctx context.Context, tx *sqlTx, baseStamp copyStamp, outcome *residentWriteOutcome) error {
	s := e.store
	budget := &boundedDiscussionAttempt{owner: e.owner}
	if err := budget.reserveValue(ctx, s.viewer); err != nil {
		return err
	}
	viewerRaw, _ := json.Marshal(s.viewer)
	if len(viewerRaw) == 0 || string(viewerRaw) == "{}" {
		if err := budget.reserveValue(ctx, e.data.Viewer); err != nil {
			return err
		}
		viewerRaw, _ = json.Marshal(e.data.Viewer)
	}
	if err := writeAccountMetadata(ctx, tx, e.workspace, viewerRaw); err != nil {
		return err
	}
	event := domain.DomainEvent{ID: idgen.New("evt_"), Type: e.eventType, AggregateID: e.aggregate,
		Payload: e.payload, PreviousValues: e.previous, CreatedAt: time.Now().UTC()}
	scratch := &ResourceRead{store: s}
	defer scratch.Close()
	storedPrevious, err := storedEventPreviousValuesOwned(ctx, scratch, tx, e.workspace, event.Type, e.aggregate, e.previous)
	if err != nil {
		return err
	}
	if _, err := insertDomainEvent(ctx, tx, domainEventRow{
		ID: event.ID, Type: event.Type, AggregateID: event.AggregateID, Payload: event.Payload,
		PreviousValues: storedPrevious, CreatedAt: event.CreatedAt.Format(time.RFC3339Nano),
		WorkspaceID: baseStamp.workspaceID, WorkspaceKey: e.workspace,
	}); err != nil {
		return err
	}
	outcome.event, outcome.realtime = event, e.realtime
	return nil
}

// residentStampCurrent reports whether workspace's stored metadata stamp is
// still source's (read outside any write transaction when tx is nil).
func (s *SQLiteStore) residentStampCurrent(ctx context.Context, tx *sqlTx, workspace string, source *metadataLogicalSource) (bool, error) {
	var reader metadataRowReader = s.db
	if tx != nil {
		reader = tx
	}
	stamp, err := readMetadataStamp(ctx, reader, workspace)
	if err != nil {
		return false, err
	}
	return stamp == source.Stamp(), nil
}

// residentDocumentSelect is documentUpdateImageProfile.resolve's choice: the
// first document whose ID or slug is ref, else the first whose previous slugs
// hold it.
func residentDocumentSelect(documents []domain.Document, ref string) (domain.Document, int) {
	best, rank := -1, 0
	for ordinal, document := range documents {
		next := 0
		if document.ID == ref || document.SlugID == ref {
			next = 2
		} else if ref != "" && slices.Contains(document.PreviousSlugIDs, ref) {
			next = 1
		}
		if next > rank {
			best, rank = ordinal, next
		}
	}
	if best < 0 {
		return domain.Document{}, -1
	}
	return documents[best], best
}

// residentActorUsers is the user directory with the actor substituted as
// prepareLegacyActorImage substitutes it: the first record with its ID, or
// appended.
func residentActorUsers(users []domain.User, actor domain.User) []domain.User {
	index := slices.IndexFunc(users, func(user domain.User) bool { return user.ID == actor.ID })
	out := slices.Clone(users)
	if index < 0 {
		return append(out, actor)
	}
	out[index] = actor
	return out
}

// residentActorState reports what substituting actor (prepareLegacyActorImage)
// changes: user is whether the actor's stored user record stays as stored,
// viewer whether the stored viewer does. An actor with request attribution
// (Via) is never treated as unchanged.
func residentActorState(image domain.Bootstrap, actor domain.User) (user, viewer bool) {
	if actor.Via != nil {
		return false, false
	}
	index := slices.IndexFunc(image.Users, func(user domain.User) bool { return user.ID == actor.ID })
	user = index >= 0 && reflect.DeepEqual(image.Users[index], actor)
	return user, reflect.DeepEqual(image.Viewer, actor)
}

// residentDisplayChecks caches, per image, whether the display refresh of
// refreshLegacyDisplayImage changes nothing in it.
type residentDisplayChecks struct {
	once    sync.Once
	current bool
}

func (image *coldDecodedImage) displayCurrent() bool {
	image.display.once.Do(func() { image.display.current = residentDisplayCurrent(image.data) })
	return image.display.current
}

// residentDisplayCurrent reports whether refreshDisplayReferences (whose
// literal pass refreshLegacyDisplayImage reproduces) leaves data unchanged.
func residentDisplayCurrent(data domain.Bootstrap) bool {
	users := make(map[string]domain.User, len(data.Users))
	for _, user := range data.Users {
		users[user.ID] = user
	}
	same := func(value domain.User) bool {
		current, ok := users[value.ID]
		return !ok || reflect.DeepEqual(current, value)
	}
	for _, member := range data.Members {
		if !same(member.User) {
			return false
		}
	}
	for _, project := range data.Projects {
		if project.Lead != nil && !same(*project.Lead) || project.Creator != nil && !same(*project.Creator) {
			return false
		}
	}
	for _, initiative := range data.Initiatives {
		if !same(initiative.Creator) || initiative.Owner != nil && !same(*initiative.Owner) {
			return false
		}
	}
	for _, items := range data.ProjectUpdates {
		for _, item := range items {
			if !same(item.User) {
				return false
			}
		}
	}
	for _, items := range data.InitiativeUpdates {
		for _, item := range items {
			if !same(item.User) {
				return false
			}
		}
	}
	return residentContentDisplayCurrent(data, data.Users)
}

// residentContentDisplayCurrent: the display refresh changes none of the
// people embedded in data's comments, activities and notifications.
func residentContentDisplayCurrent(data domain.Bootstrap, directory []domain.User) bool {
	users := make(map[string]domain.User, len(directory))
	for _, user := range directory {
		users[user.ID] = user
	}
	same := func(value domain.User) bool {
		current, ok := users[value.ID]
		return !ok || reflect.DeepEqual(current, value)
	}
	for _, items := range data.Comments {
		for _, item := range items {
			if !same(item.User) {
				return false
			}
		}
	}
	for _, items := range data.Activities {
		for _, item := range items {
			if !same(item.Actor) {
				return false
			}
		}
	}
	for _, item := range data.Notifications {
		if !same(item.Actor) {
			return false
		}
	}
	return true
}

// residentContentCopy is an exact, owned copy of loaded content rows (the
// loader clears its results when closed).
func residentContentCopy(loaded domain.Bootstrap) domain.Bootstrap {
	content := domain.Bootstrap{Comments: loaded.Comments, Activities: loaded.Activities,
		Notifications: loaded.Notifications, NotificationDeliveries: loaded.NotificationDeliveries}
	return exactCopyValue(reflect.ValueOf(content)).Interface().(domain.Bootstrap)
}
