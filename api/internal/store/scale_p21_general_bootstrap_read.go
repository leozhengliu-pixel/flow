package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// GeneralBootstrapRead keeps the outline, metadata and streamed bodies in the
// original repeatable SQL snapshot. Data contains opaque body references and
// SQL-field placeholders; the HTTP owner supplies those fields to
// WriteJSONWithFields rather than encoding Data directly.
type GeneralBootstrapRead struct {
	*IssueBootstrapRead
	tx         *sqlTx
	workspace  string
	references domain.Bootstrap
	once       sync.Once
}

func (r *GeneralBootstrapRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.references = domain.Bootstrap{}
			if r.tx != nil {
				_ = r.tx.Rollback()
				r.tx = nil
			}
			r.IssueBootstrapRead.Close()
			r.workspace = ""
		})
	}
}

func (r *GeneralBootstrapRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// Context binds SQL and raw reference inputs without replacing the caller's
// cancellation, principal, output ownership or signing context.
func (r *GeneralBootstrapRead) Context(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	if r == nil || r.IssueBootstrapRead == nil || r.owner == nil {
		return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{})
	}
	return context.WithValue(ctx, workspaceReadKey{}, workspaceRead{
		database: r.owner.store.db, workspace: r.workspace, reader: r.tx, metadata: r.references,
	})
}

func (r *GeneralBootstrapRead) ReserveOutputWork(ctx context.Context, bytes uint64) error {
	if r == nil || r.IssueBootstrapRead == nil || r.owner == nil || r.tx == nil {
		return ErrBoundedMetadataCoverage
	}
	return r.owner.reserve(context.WithValue(ctx, metadataWorkLeaseKey{}, nil), bytes)
}

func (r *GeneralBootstrapRead) WalkIssueRecords(ctx context.Context, q IssueRecordQuery, visit func(domain.Issue) error) error {
	if r == nil || r.IssueBootstrapRead == nil || r.owner == nil || r.tx == nil || q.Workspace != r.workspace {
		return ErrBoundedMetadataCoverage
	}
	return r.owner.store.walkBoundedBootstrapIssueRecords(r.Context(ctx), q, visit)
}

func (r *GeneralBootstrapRead) WalkContentRecords(ctx context.Context, workspace, kind string, visit func(string, json.RawMessage) error) error {
	if r == nil || r.IssueBootstrapRead == nil || r.owner == nil || r.tx == nil || workspace != r.workspace {
		return ErrBoundedMetadataCoverage
	}
	return r.owner.store.walkBoundedBootstrapContentRecords(r.Context(ctx), workspace, kind, visit)
}

// BeginGeneralBootstrapRead preserves BeginWorkspaceRead's source distinction:
// an exact installed revision supplies native values; an unknown/stale copy
// decodes and normalizes the canonical SQL metadata in this snapshot instead.
func (s *SQLiteStore) BeginGeneralBootstrapRead(ctx context.Context, workspace, userID string, key *domain.APIKey, beforeOutline ...func(context.Context)) (_ *GeneralBootstrapRead, err error) {
	if !s.BoundedMetadataEnabled() || len(beforeOutline) > 1 {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read := &GeneralBootstrapRead{IssueBootstrapRead: &IssueBootstrapRead{
		owner: &ResourceRead{store: s},
		// The legacy general response uses its held SQL authority through the
		// final write; do not introduce a trailing live authority read.
		principal: userID, borrowed: true,
	}}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	bound, release, err := s.tryAdmitAdditionalMetadataWork(ctx, 2<<20)
	if err != nil {
		return nil, err
	}
	read.owner.release = append(read.owner.release, release)
	ctx = bound
	if workspace == "" {
		s.mu.RLock()
		workspace = s.lastWorkspaceKey
		s.mu.RUnlock()
	}
	read.workspace = workspace
	read.tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
	if err != nil {
		return nil, err
	}
	stamp, err := readMetadataStamp(ctx, read.tx, workspace)
	if err != nil {
		return nil, err
	}
	source, retainErr := s.retainMetadataLogicalSource(ctx, workspace)
	if retainErr == nil && source.Stamp() != stamp {
		source.Release()
		source = nil
	}
	if source == nil {
		raw, readErr := s.readRawMetadataLogicalSource(ctx, read.tx, workspace)
		if readErr != nil {
			return nil, fmt.Errorf("general bootstrap canonical source: %w", readErr)
		}
		source, err = s.normalizeMetadataLogicalSource(ctx, raw)
		raw.Release()
		if err != nil {
			return nil, fmt.Errorf("general bootstrap canonical normalization: %w", err)
		}
	}
	defer source.Release()
	read.owner.source, err = cloneMetadataLogicalSnapshot(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("general bootstrap snapshot clone: %w", err)
	}
	read.body, err = s.beginBootstrapBodyProjectionWithOptions(ctx, read.owner.source, bootstrapBodyProjectionOptions{
		OmittedRootFields: []string{
			"issues", "comments", "activities", "notifications", "notificationDeliveries",
			"providerJobs", "commentActions", "loopRuns", "loopVersions", "loopAttachments",
		},
		StripUpdateSnapshots: true,
	})
	if err != nil {
		return nil, fmt.Errorf("general bootstrap header projection: %w", err)
	}
	read.Data = read.body.Data
	stripTrashLinks(&read.Data)
	// Keep pre-projection reference precedence for WalkIssueRecords. Viewer
	// projection mutates its slices/maps, so these inputs have their own copy.
	references := domain.Bootstrap{
		Workspace: read.Data.Workspace, Teams: read.Data.Teams, States: read.Data.States,
		Users: read.Data.Users, Labels: read.Data.Labels,
	}
	cost, err := metadataAggregateNativeCost(ctx, reflect.ValueOf(references), 0)
	if err != nil {
		return nil, err
	}
	if cost > (metadataWorkBytes-4096)/8 {
		return nil, ErrMetadataWorkBudget
	}
	if err := read.owner.reserve(ctx, cost*8+4096); err != nil {
		return nil, err
	}
	read.references = CloneHydratedReadSnapshot(ctx, references)
	for _, project := range read.Data.Projects {
		cost := uint64(len(project.ID)+len(project.Name)+len(project.Icon)+len(project.Color))*8 +
			uint64(reflect.TypeFor[domain.Project]().Size())*4 + 512
		if err := read.owner.reserve(ctx, cost); err != nil {
			return nil, err
		}
		read.references.Projects = append(read.references.Projects, domain.Project{
			ID: project.ID, Name: project.Name, Icon: project.Icon, Color: project.Color,
		})
	}
	snapshot := read.Context(ctx)
	snapshot = read.owner.metadataOutputContext(snapshot)
	if len(beforeOutline) == 1 && beforeOutline[0] != nil {
		beforeOutline[0](snapshot)
	}
	read.Data.Issues = []domain.Issue{}
	if err := s.walkBoundedBootstrapIssueOutlines(snapshot, workspace, func(issue domain.Issue) error {
		cost, err := metadataAggregateNativeCost(snapshot, reflect.ValueOf(issue), 0)
		if err != nil {
			return err
		}
		if cost > (metadataWorkBytes-4096)/8 {
			return ErrMetadataWorkBudget
		}
		// The returned outline and its reference overlays survive every row's
		// temporary admission, plus the later permission and wire projections.
		if err := read.owner.reserve(snapshot, cost*8+4096); err != nil {
			return err
		}
		read.Data.Issues = append(read.Data.Issues, issue)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := reserveBootstrapIssueReferences(snapshot, read.owner, read.Data); err != nil {
		return nil, err
	}
	refreshIssueReferences(&read.Data)
	read.Data.Comments, read.Data.Activities = map[string][]domain.Comment{}, map[string][]domain.ActivityEvent{}
	read.Data.Notifications, read.Data.NotificationDeliveries = []domain.Notification{}, []domain.NotificationDelivery{}
	if userID == "" {
		read.Data.ViewerRole = "admin"
		refreshResourceCounts(&read.Data)
		return read, ctx.Err()
	}
	memoContext, _ := withResourcePolicyMemo(snapshot, read.owner)
	document := &DocumentRead{Data: read.Data, owner: read.owner}
	if err := document.documentProjectionWork(memoContext, read.Data.Workspace.ID, userID); err != nil {
		return nil, err
	}
	// BootstrapOutline itself is not credential-scoped; the server applies
	// that filter afterward in its literal original order.
	_ = key
	var ok bool
	read.Data, ok, err = s.projectBootstrapForUser(memoContext, read.Data, userID)
	if outputErr := metadataOutputError(snapshot); outputErr != nil {
		return nil, outputErr
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAuthForbidden
	}
	return read, ctx.Err()
}
