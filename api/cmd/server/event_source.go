package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

var errEventSourceUnsupported = errors.New("event uses its dedicated realtime channel")
var errRealtimeProjectionStale = errors.New("realtime projection authorization changed")

// EventSource owns the viewer-independent records read for an event. A source
// is immutable after construction; its bounded encoding cache is synchronized.
type EventSource struct {
	event           domain.RealtimeEvent
	version         store.EventSourceVersion
	policy          *store.EventPolicySource
	issueIDs        []string
	issues          *store.RealtimeIssueSource
	document        *domain.Document
	documentMissing bool
	comments        []domain.Comment
	pulse           *store.PulseFeedSnapshot
	rules           *pulseRules
	policyRecord    realtimeRecord
	policyFound     bool
	kind            string
	bytes           int
	frames          *realtimePayloadCache
	budget          *realtimeBudget
	cacheID         string
	recordVersion   string
	owner           *server
	authorization   string
	authzEpoch      string
}

// EventFrame contains one immutable, validated JSON envelope. Callers receive
// copies through Event; transport writes the shared encoding directly.
type EventFrame struct {
	encoded       json.RawMessage
	id            string
	visible       bool
	release       func()
	validate      func(context.Context) error
	durableCursor bool
	noCursor      bool
}

func (frame EventFrame) Visible() bool { return frame.visible }
func (frame EventFrame) Size() int     { return len(frame.encoded) }
func (frame EventFrame) Close() {
	if frame.release != nil {
		frame.release()
	}
}
func (frame EventFrame) Validate(ctx context.Context) error {
	if frame.validate != nil {
		return frame.validate(ctx)
	}
	return nil
}
func (frame EventFrame) Event() (domain.RealtimeEvent, error) {
	var event domain.RealtimeEvent
	if !frame.visible {
		return event, nil
	}
	err := json.Unmarshal(frame.encoded, &event)
	return event, err
}

func (frame EventFrame) WriteSSE(w http.ResponseWriter, node string) bool {
	if !frame.visible {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := frame.Validate(ctx); err != nil {
		return false
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(realtimeWriteTimeout))
	id := frame.id
	if node != "" && !frame.durableCursor && !strings.HasPrefix(id, "connected_") {
		id += "~" + node
	}
	if bounded, ok := w.(*realtimeBudgetWriter); ok {
		lease := domain.RealtimeEvent{ID: frame.id, Type: "sync.frame", Payload: frame.encoded}
		if !bounded.budget.retainEvent(lease) {
			return false
		}
		bounded.frames = append(bounded.frames, eventKey(lease))
		if !bounded.reserveEncoding(int64(len(id) + 128)) {
			return false
		}
	}
	var err error
	if frame.noCursor {
		_, err = fmt.Fprintf(w, "data: %s\n\n", frame.encoded)
	} else {
		_, err = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", id, frame.encoded)
	}
	return err == nil
}

// BuildEventSource performs the source reads once. Delivery uses a versioned
// byte-bounded singleflight cache, so a distinct ACL does not add source SQL.
func (s *server) BuildEventSource(ctx context.Context, workspace string, event domain.RealtimeEvent) (*EventSource, error) {
	ctx, err := s.store.LiveEventContext(ctx)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s.realtime != nil {
		release, err := acquireRealtimeWork(ctx, s.realtime.budget, 2<<20)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	version, err := s.store.RealtimeSourceVersion(ctx, workspace)
	if err != nil {
		return nil, err
	}
	epoch, _ := ctx.Value(eventSourceEpochKey{}).(string)
	if epoch == "" {
		epoch, err = s.store.AuthzEpoch(ctx, version.WorkspaceID)
		if err != nil {
			return nil, err
		}
	}
	source := &EventSource{event: event, version: version, owner: s, authzEpoch: epoch}
	source.policy, err = s.sharedEventPolicy(ctx, workspace, version)
	if err != nil {
		return nil, err
	}
	if len(event.Payload) > 256<<10 {
		return nil, store.ErrEventSourceBudget
	}
	source.event.Payload = slices.Clone(event.Payload)
	source.bytes = len(event.Payload)*2 + source.policy.Bytes() + 1024
	if s.realtime != nil {
		source.frames = &s.realtime.payloads
		source.budget = s.realtime.budget
	}
	switch {
	case domain.RoutineCredentialEvent(event.Type):
		source.kind = "hidden"
	case event.Type == "issue.deleted":
		source.kind = "deleted"
	case event.Type == "issue.batch_updated":
		source.kind = "batch"
		var input struct {
			IssueIDs []string `json:"issueIds"`
		}
		if err := json.Unmarshal(event.Payload, &input); err != nil {
			return nil, err
		}
		source.issueIDs = input.IssueIDs
	case strings.HasPrefix(event.Type, "issue."), strings.HasPrefix(event.Type, "comment."), strings.HasPrefix(event.Type, "attachment."), event.Type == "agent_task.replied":
		source.kind = "issue"
		source.issueIDs = []string{event.AggregateID}
	case strings.HasPrefix(event.Type, "document."):
		source.kind = "document"
		id := event.AggregateID
		if event.Type == "document.draft_published" {
			var input struct {
				DocumentID string `json:"documentId"`
			}
			_ = json.Unmarshal(event.Payload, &input)
			id = input.DocumentID
		}
		if !strings.HasPrefix(event.Type, "document.draft_") || event.Type == "document.draft_published" && id != "" {
			installed, installedOK := s.store.DocumentByID(workspace, id)
			if installedOK {
				id = installed.ID
			}
			batch, err := s.store.ReadCanonicalRecords(ctx, workspace, []store.RecordKey{{Kind: "documents", ID: id}}, "detail")
			if err != nil {
				return nil, err
			}
			if len(batch.Items) == 0 || batch.Items[0].Missing {
				source.documentMissing = true
			} else {
				if batch.Items[0].Omitted {
					return nil, store.ErrEventSourceBudget
				}
				document, err := store.DecodeRealtimeDocument(batch.Items[0].Data)
				if err != nil {
					return nil, err
				}
				if !installedOK {
					return nil, errEventSourceUnsupported
				}
				// The installed read model adds canonical owner grants and link
				// scopes. Keep the same read policy as DocumentByID.
				if len(document.Permissions) == 0 && slices.ContainsFunc(installed.Permissions, func(permission domain.DocumentPermission) bool {
					return permission.SubjectType == "user" && permission.SubjectID == document.Creator.ID
				}) {
					for _, permission := range installed.Permissions {
						if permission.SubjectType == "user" && permission.SubjectID == document.Creator.ID {
							document.Permissions = append(document.Permissions, permission)
							break
						}
					}
				}
				document.InheritedTeamIDs, document.IssueTeamID, err = s.store.RealtimeDocumentInheritedScope(ctx, workspace, document, source.policy)
				if err != nil {
					return nil, err
				}
				for _, permission := range document.Permissions {
					source.bytes += len(permission.ID) + len(permission.SubjectID) + len(permission.SubjectType) + len(permission.Role) + 256
				}
				source.document = &document
				source.bytes += len(batch.Items[0].Data) * 3
				if strings.HasPrefix(event.Type, "document.comment_") {
					comments, cost, err := s.store.ReadRealtimeDocumentComments(ctx, workspace, document.ID)
					if errors.Is(err, store.ErrEventSourceBudget) {
						return nil, err
					}
					if err == nil {
						source.comments = comments
						source.bytes += cost
					}
				}
			}
		}
	case strings.HasPrefix(event.Type, "project."), strings.HasPrefix(event.Type, "project_update."), strings.HasPrefix(event.Type, "initiative."), strings.HasPrefix(event.Type, "initiative_update."):
		source.kind = "pulse"
		if err := s.buildPulseEventSource(ctx, workspace, source); err != nil {
			return nil, err
		}
	default:
		if strings.HasPrefix(source.event.Type, "integration.") {
			source.event.Payload = redactIntegrationEvent(source.event.Payload)
		}
		if err := s.buildPolicyEventSource(ctx, workspace, source); err != nil {
			return nil, err
		}
	}
	if len(source.issueIDs) > 0 {
		source.issues, err = s.store.ReadRealtimeIssueSource(ctx, workspace, source.issueIDs, version)
		if err != nil {
			return nil, err
		}
		source.bytes += source.issues.Bytes()
	}
	if source.kind == "pulse" || source.kind == "document" {
		kind := syncCollection(event.Type)
		id := event.AggregateID
		if source.document != nil {
			id = source.document.ID
		}
		source.recordVersion, err = s.store.RealtimeMetadataVersion(ctx, workspace, store.RecordKey{Kind: kind, ID: id})
		if err != nil {
			return nil, err
		}
	}
	current, err := s.store.RealtimeSourceVersion(ctx, workspace)
	if err != nil {
		return nil, err
	}
	if current != version {
		return nil, store.ErrAuthForbidden
	}
	if source.bytes > eventSourceCacheBytes {
		return nil, store.ErrEventSourceBudget
	}
	source.cacheID = source.cacheKey()
	source.authorization = source.authorizationFingerprint()
	return source, nil
}

// ProjectEvent performs no reads. Its access must be resolved for the current
// delivery batch and validated again before the frame is written.
func ProjectEvent(source *EventSource, access store.EventAccess) (EventFrame, error) {
	if source == nil || source.version != access.SourceVersion() || source.version.WorkspaceID != access.WorkspaceID || source.authzEpoch != "" && source.authzEpoch != access.AuthzEpoch {
		return EventFrame{}, store.ErrAuthForbidden
	}
	work := int64(source.bytes*4 + 16<<10)
	if source.budget != nil && !source.budget.acquire(work) {
		return EventFrame{}, store.ErrEventSourceBudget
	}
	var once sync.Once
	release := func() { once.Do(func() { source.budget.release(work) }) }
	keep := false
	defer func() {
		if !keep {
			release()
		}
	}()
	query, err := access.RealtimeQuery()
	if err != nil {
		return EventFrame{}, err
	}
	var data domain.Bootstrap
	if source.kind == "document" || source.kind == "pulse" || source.kind == "policy" {
		data, _, err = access.RealtimePolicy()
		if err != nil {
			return EventFrame{}, err
		}
	}
	event := source.event
	var projected domain.RealtimeEvent
	visible := false
	var buildErr error
	key := ""
	if source.issues != nil {
		key = source.issues.ProjectionKey(query)
	}
	if source.kind == "pulse" || source.kind == "policy" {
		key = source.metadataProjectionKey(data, query)
	}
	// The source id binds mutation-input shape as well as entity versions.
	cacheKey := source.cacheKey() + "|" + key
	build := func() json.RawMessage {
		switch source.kind {
		case "policy":
			var visible bool
			projected, visible = source.policyProjection(data, query)
			if !visible {
				return nil
			}
		case "pulse":
			var visible bool
			projected, visible = source.pulseProjection(data, query)
			if !visible {
				return nil
			}
		case "hidden":
			return nil
		case "deleted":
			ids := visibleDeletedIssueIDs(event, query)
			if len(ids) == 0 {
				return nil
			}
			projected = pagedIssueIDsEvent(event, ids, nil, map[string]any{"deleted": true})
		case "issue", "batch":
			shown, hidden := []string{}, []string{}
			for _, id := range source.issueIDs {
				if source.issues.Visible(query, id) {
					shown = append(shown, id)
				} else {
					hidden = append(hidden, id)
				}
			}
			if source.kind == "batch" {
				extra := map[string]any{}
				if len(shown) > 0 && len(shown) <= maxBatchProjection {
					issues, err := source.issues.Project(query, shown)
					if err != nil {
						buildErr = err
						return nil
					}
					extra["issues"] = issues
				}
				projected = pagedIssueIDsEvent(event, shown, hidden, extra)
			} else if len(shown) == 0 {
				if !pagedIssuePermissionEvent(event.Type) {
					return nil
				}
				projected = pagedIssueIDsEvent(event, nil, source.issueIDs, nil)
			} else if event.Type == "agent_task.replied" {
				projected = event
			} else {
				issues, err := source.issues.Project(query, shown)
				if err != nil {
					buildErr = err
					return nil
				}
				if len(issues) != 1 {
					return nil
				}
				var payload map[string]any
				_ = json.Unmarshal(event.Payload, &payload)
				if payload == nil {
					payload = map[string]any{}
				}
				payload["entity"] = issues[0]
				if pagedIssuePermissionEvent(event.Type) {
					payload = map[string]any{"entity": issues[0], "issueIds": shown}
				}
				if _, ok := payload["issue"]; ok {
					payload["issue"] = issues[0]
				}
				projected = event
				projected.Payload, _ = json.Marshal(payload)
			}
		case "document":
			author := event.ActorID != "" && event.ActorID == data.Viewer.ID
			if strings.HasPrefix(event.Type, "document.draft_") && (event.Type != "document.draft_published" || source.document == nil && !source.documentMissing) {
				if !author {
					return nil
				}
				projected = event
				break
			}
			if source.documentMissing {
				projected = anonymousRealtimeEvent(event)
				break
			}
			if source.document == nil {
				return nil
			}
			if documentRole(&server{}, data, *source.document) == "none" {
				if strings.Contains(event.Type, "permission") || realtimeScopeChanged(event) {
					projected = anonymousRealtimeEvent(event)
					break
				}
				return nil
			}
			if strings.Contains(event.Type, "permission") {
				projected = bareRealtimeEvent(event, nil)
				break
			}
			document := *source.document
			document.Favorite = false
			extra := map[string]any{"documentId": document.ID}
			if event.Type == "document.draft_published" {
				extra["draftId"] = event.AggregateID
			}
			if source.comments != nil {
				extra["comments"] = source.comments
			}
			projected = event
			projected.Payload = realtimeEntityPayload(event, document, extra)
		default:
			return nil
		}
		raw, err := json.Marshal(projected)
		if err != nil {
			buildErr = err
			return nil
		}
		return raw
	}
	// Deleted grants and document ACLs depend on principal identity, not just
	// the set of visible issue references.
	if source.kind == "deleted" || source.kind == "document" {
		cacheKey += "|" + access.AuthzEpoch + "|" + access.PrincipalID + "|" + access.CredentialScope + "|" + access.PermissionFingerprint()
	}
	var raw json.RawMessage
	if source.frames != nil {
		raw = source.frames.load(cacheKey, build)
	} else {
		raw = build()
	}
	if buildErr != nil {
		return EventFrame{}, buildErr
	}
	visible = len(raw) > 0
	release()
	release = func() {}
	if visible && source.budget != nil {
		lease := domain.RealtimeEvent{ID: event.ID, Type: "sync.frame", Payload: raw}
		if !source.budget.retainEvent(lease) {
			return EventFrame{}, store.ErrEventSourceBudget
		}
		var closed sync.Once
		release = func() { closed.Do(func() { source.budget.releaseEvent(eventKey(lease)) }) }
	}
	keep = true
	validate := access.Validate
	if source.owner != nil {
		validate = func(ctx context.Context) error { return source.owner.validateEventProjection(ctx, source, access) }
	}
	return EventFrame{encoded: raw, id: event.ID, visible: visible, release: release, validate: validate}, nil
}

func (source *EventSource) cacheKey() string {
	if source.cacheID != "" {
		return source.cacheID
	}
	hash := sha256.New()
	identity := source.event
	identity.Payload = nil
	raw, _ := json.Marshal(identity)
	_, _ = hash.Write(raw)
	// Persisted event IDs bind their immutable mutation input. Enriched live
	// envelopes and durable replay therefore share the same source. Personal
	// reply signals retain their distinct legacy payloads.
	if source.event.ID == "" || source.event.Type == "agent_task.replied" {
		_, _ = hash.Write(source.event.Payload)
	}
	_, _ = io.WriteString(hash, fmt.Sprintf("|%s|%d|%d|%s", source.version.WorkspaceID, source.version.Metadata, source.version.Issues, source.authzEpoch))
	return fmt.Sprintf("p12:%x", hash.Sum(nil))
}

const (
	eventSourceCacheEntries = 32
	eventSourceCacheBytes   = 4 << 20
)

type eventSourceCache struct {
	mu      sync.Mutex
	entries map[string]*EventSource
	order   []string
	bytes   int
}

func (cache *eventSourceCache) load(ctx context.Context, key string, build func() (*EventSource, error)) (*EventSource, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source := cache.entries[key]; source != nil {
		return source, nil
	}
	source, err := build()
	if err != nil {
		return nil, err
	}
	if source.bytes > eventSourceCacheBytes {
		return nil, store.ErrEventSourceBudget
	}
	if cache.entries == nil {
		cache.entries = map[string]*EventSource{}
	}
	for len(cache.order) >= eventSourceCacheEntries || cache.bytes+source.bytes > eventSourceCacheBytes {
		oldest := cache.order[0]
		cache.order = cache.order[1:]
		cache.bytes -= cache.entries[oldest].bytes
		delete(cache.entries, oldest)
	}
	cache.entries[key] = source
	cache.order = append(cache.order, key)
	cache.bytes += source.bytes
	return source, nil
}

func (s *server) sharedEventFrame(r *http.Request, event domain.RealtimeEvent) (EventFrame, bool, error) {
	if syncV2Enabled() && !s.authDisabled {
		ctx, err := s.store.LiveEventContext(r.Context())
		if err != nil {
			return EventFrame{}, true, err
		}
		r = r.WithContext(ctx)
		r = s.eventDeliveryRequest(r, event)
		if delivery, _ := r.Context().Value(eventDeliveryKey{}).(eventDelivery); delivery.viewers == 1 && !s.negotiatedSummaryRequest(r) {
			return EventFrame{}, false, nil
		}
	}
	for range 8 {
		frame, handled, err := s.sharedEventFrameOnce(r, event)
		if handled && (errors.Is(err, errRealtimeProjectionStale) || errors.Is(err, store.ErrAuthForbidden)) && r.Context().Err() == nil {
			frame.Close()
			r = r.WithContext(context.WithValue(r.Context(), eventDeliveryKey{}, eventDelivery{}))
			r = r.WithContext(context.WithValue(r.Context(), eventAccessRefreshKey{}, true))
			continue
		}
		return frame, handled, err
	}
	return EventFrame{}, true, errRealtimeProjectionStale
}

func (s *server) sharedEventFrameOnce(r *http.Request, event domain.RealtimeEvent) (EventFrame, bool, error) {
	if !syncV2Enabled() || s.authDisabled {
		return EventFrame{}, false, nil
	}
	if domain.RoutineCredentialEvent(event.Type) {
		return EventFrame{}, true, nil
	}
	if strings.HasPrefix(event.Type, "document.draft_") && event.Type != "document.draft_published" {
		return EventFrame{}, false, nil
	}
	// Dedicated personal/progress channels keep their existing authorization.
	if !sharedEventType(event.Type) {
		return EventFrame{}, false, nil
	}
	var key *domain.APIKey
	if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		key = &credential
	}
	readAuthority := s.readEventSourceVersion
	if refresh, _ := r.Context().Value(eventAccessRefreshKey{}).(bool); refresh {
		readAuthority = s.readEventProjectionAuthority
	}
	authority, err := readAuthority(r.Context(), workspaceKey(r))
	if err != nil {
		return EventFrame{}, true, err
	}
	policy, err := s.sharedEventPolicy(r.Context(), workspaceKey(r), authority.version)
	if err != nil {
		if errors.Is(err, store.ErrEventSourceBudget) && !s.negotiatedSummaryRequest(r) {
			return EventFrame{}, false, nil
		}
		return EventFrame{}, true, err
	}
	// Resolve principal policy before reading source records. The final epoch
	// check then covers both this policy and every cached source built under it.
	access, err := s.cachedEventAccess(r.Context(), policy, authority, authUser(r).ID, key)
	if err != nil {
		return EventFrame{}, true, err
	}
	authority.epoch = access.AuthzEpoch
	source, err := s.eventSourceForAuthority(r.Context(), workspaceKey(r), event, authority)
	if errors.Is(err, store.ErrEventSourceBudget) && s.negotiatedSummaryRequest(r) {
		raw, encodeErr := json.Marshal(anonymousRealtimeEvent(event))
		if encodeErr != nil {
			return EventFrame{}, true, encodeErr
		}
		frame, encodeErr := summarizeEventFrame(EventFrame{encoded: raw, id: event.ID, visible: true}, access.AuthzEpoch)
		frame.validate = access.Validate
		if encodeErr == nil {
			encodeErr = access.Validate(r.Context())
		}
		return frame, true, encodeErr
	}
	if errors.Is(err, errEventSourceUnsupported) || errors.Is(err, store.ErrEventSourceBudget) {
		return EventFrame{}, false, nil
	}
	if err != nil {
		return EventFrame{}, true, err
	}
	frame, err := ProjectEvent(source, access)
	if err != nil {
		return EventFrame{}, true, err
	}
	if s.negotiatedSummaryRequest(r) && event.Type != "agent_task.replied" && event.Type != "workspace.resync_required" && event.Type != "connected" {
		summary, summaryErr := summarizeEventFrame(frame, access.AuthzEpoch)
		if summaryErr != nil {
			frame.Close()
			return EventFrame{}, true, summaryErr
		}
		summary.release = frame.release
		summary.validate = frame.validate
		frame = summary
	}
	if err == nil {
		err = frame.Validate(r.Context())
	}
	if err != nil {
		frame.Close()
	}
	return frame, true, err
}

func (s *server) validateEventProjection(ctx context.Context, source *EventSource, access store.EventAccess) error {
	authority, err := s.readEventAuthority(ctx, access.WorkspaceKey())
	if err != nil {
		return err
	}
	if authority.epoch != access.AuthzEpoch || source.authzEpoch != "" && source.authzEpoch != authority.epoch {
		return errRealtimeProjectionStale
	}
	if authority.version == source.version && authority.epoch == access.AuthzEpoch {
		return access.CheckCredential()
	}
	current, err := s.eventSourceForAuthority(ctx, access.WorkspaceKey(), source.event, authority)
	if err != nil {
		return err
	}
	fresh, err := access.ForSource(current.policy, authority.epoch)
	if err != nil {
		return err
	}
	if source.authorization != current.authorization || access.PermissionFingerprint() != fresh.PermissionFingerprint() {
		return errRealtimeProjectionStale
	}
	return fresh.Validate(ctx)
}

func (s *server) writeSharedEventFrame(w http.ResponseWriter, r *http.Request, event domain.RealtimeEvent) (bool, bool) {
	frame, handled, err := s.sharedEventFrame(r, event)
	if !handled {
		return false, false
	}
	if err != nil {
		frame.Close()
		slog.Warn("realtime projection stopped", "event_type", event.Type, "error_type", fmt.Sprintf("%T", err), "denied", errors.Is(err, store.ErrAuthForbidden), "stale", errors.Is(err, errRealtimeProjectionStale), "budget", errors.Is(err, store.ErrEventSourceBudget), "missing", errors.Is(err, sql.ErrNoRows))
		var typed *json.UnmarshalTypeError
		if errors.As(err, &typed) {
			slog.Warn("realtime record field type", "field", typed.Field, "expected", typed.Type.String())
		}
		return true, false
	}
	// sharedEventFrame just validated this delivery. Retained EventFrames keep
	// their live validator; this immediate write consumes only this proof.
	frame.validate = nil
	if sidechannel, _ := r.Context().Value(syncV2SidechannelKey{}).(bool); sidechannel {
		frame.noCursor = true
	}
	ok := frame.WriteSSE(w, s.realtime.node) && flushRealtime(w)
	frame.Close()
	return true, ok
}

func (s *server) eventSourceForAuthority(ctx context.Context, workspace string, event domain.RealtimeEvent, authority eventAuthority) (*EventSource, error) {
	identity := &EventSource{event: event, version: authority.version, authzEpoch: authority.epoch}
	build := func() (*EventSource, error) {
		source, err := s.BuildEventSource(context.WithValue(ctx, eventSourceEpochKey{}, authority.epoch), workspace, event)
		if err != nil {
			return nil, err
		}
		if source.version != authority.version {
			return nil, errRealtimeProjectionStale
		}
		return source, nil
	}
	if s.realtime != nil {
		return s.realtime.rules.sources.load(ctx, identity.cacheKey(), build)
	}
	return build()
}

func (s *server) sharedEventPolicy(ctx context.Context, workspace string, version store.EventSourceVersion) (*store.EventPolicySource, error) {
	key := fmt.Sprintf("p12:policy:%s:%d:%d", version.WorkspaceID, version.Metadata, version.Issues)
	build := func() (*EventSource, error) {
		if s.realtime != nil {
			release, err := acquireRealtimeWork(ctx, s.realtime.budget, 1<<20)
			if err != nil {
				return nil, err
			}
			defer release()
		}
		policy, err := s.store.ReadEventPolicySource(ctx, workspace, version)
		if err != nil {
			return nil, err
		}
		return &EventSource{kind: "policy", version: version, policy: policy, bytes: policy.Bytes()}, nil
	}
	var source *EventSource
	var err error
	if s.realtime != nil {
		source, err = s.realtime.rules.policies.load(ctx, key, build)
	} else {
		source, err = build()
	}
	if err != nil {
		return nil, err
	}
	return source.policy, nil
}

func (s *server) resolveSyncAccess(r *http.Request) (store.EventAccess, error) {
	ctx, err := s.store.LiveEventContext(r.Context())
	if err != nil {
		return store.EventAccess{}, err
	}
	r = r.WithContext(ctx)
	authority, err := s.readEventAuthority(r.Context(), workspaceKey(r))
	if err != nil {
		return store.EventAccess{}, err
	}
	policy, err := s.sharedEventPolicy(r.Context(), workspaceKey(r), authority.version)
	if err != nil {
		return store.EventAccess{}, err
	}
	var key *domain.APIKey
	if credential, ok := r.Context().Value(apiKeyContextKey{}).(domain.APIKey); ok {
		key = &credential
	}
	return s.cachedEventAccess(r.Context(), policy, authority, authUser(r).ID, key)
}

func frameSequence(event domain.RealtimeEvent) string { return strconv.FormatInt(event.Seq, 10) }
