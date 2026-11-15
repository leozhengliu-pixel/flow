package store

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"sync"

	"flow/api/internal/domain"
)

// IssueBootstrapRead owns the HTTP paged bootstrap's projected headers and
// streamed body references. Data is an intermediate projection, not a public
// response: opaque body tokens must pass through WriteJSON. The handler's dead
// project/initiative updates, provider jobs, comment actions and loop history
// fields are omitted before materialization; the handler supplies their final
// nil/empty response shapes.
type IssueBootstrapRead struct {
	Data      domain.Bootstrap
	body      *bootstrapBodyProjection
	owner     *ResourceRead
	access    AccessContext
	principal string
	borrowed  bool
	once      sync.Once
}

func (r *IssueBootstrapRead) Close() {
	if r != nil {
		r.once.Do(func() {
			r.Data = domain.Bootstrap{}
			r.access = AccessContext{}
			r.principal = ""
			if r.body != nil {
				r.body.Close()
				r.body = nil
			}
			if r.owner != nil {
				r.owner.Close()
				r.owner = nil
			}
		})
	}
}

func (r *IssueBootstrapRead) TransferOutput(ctx context.Context) error {
	if r == nil || !registerMetadataOutputClose(ctx, r.Close) {
		r.Close()
		return recordMetadataOutputError(ctx, ErrBoundedMetadataCoverage)
	}
	return nil
}

// StreamString borrows its chunk buffer only until visit returns. It accepts
// unchanged body tokens and newly generated literal strings.
func (r *IssueBootstrapRead) StreamString(ctx context.Context, value string, visit func([]byte) error) error {
	if r == nil || r.body == nil || r.owner == nil || visit == nil {
		return ErrBoundedMetadataCoverage
	}
	if node, found := r.body.originalString(value); found {
		if node.typ.Kind() != reflect.String {
			return ErrBoundedMetadataCoverage
		}
		cursor := metadataLogicalCursor{node, node.offset + 16, ctx}
		length, err := r.owner.nativeUint(&cursor)
		if err != nil {
			return err
		}
		for length > 0 {
			size := min(length, uint64(len(r.owner.buffer)))
			if err := cursor.read(r.owner.buffer[:size]); err != nil {
				return err
			}
			if err := visit(r.owner.buffer[:size]); err != nil {
				return err
			}
			length -= size
		}
		return ctx.Err()
	}
	for len(value) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(len(value), len(r.owner.buffer))
		copy(r.owner.buffer[:], value[:size])
		if err := visit(r.owner.buffer[:size]); err != nil {
			return err
		}
		value = value[size:]
	}
	return ctx.Err()
}

// BeginIssueBootstrapRead preserves the paged accessor's source distinction:
// authenticated explicit snapshots retain all handed collections; ordinary
// and development reads clear the five heavy collections before projection.
func (s *SQLiteStore) BeginIssueBootstrapRead(ctx context.Context, workspace, userID string, key *domain.APIKey) (_ *IssueBootstrapRead, err error) {
	if !s.BoundedMetadataEnabled() {
		return nil, ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(metadataLogicalUnpinnedContext(ctx), metadataWorkLeaseKey{}, nil)
	read := &IssueBootstrapRead{owner: &ResourceRead{store: s}, principal: userID}
	defer func() {
		if err != nil {
			read.Close()
		}
	}()
	if err := read.owner.reserve(ctx, 2<<20); err != nil {
		return nil, err
	}
	held, borrowed := ctx.Value(workspaceReadKey{}).(workspaceRead)
	borrowed = borrowed && held.database == s.db
	if borrowed && held.reader == nil {
		return nil, ErrMetadataRecordQuery
	}
	read.borrowed = borrowed
	handed := borrowed && userID != "" && (workspace == "" || workspace == held.workspace)
	if handed {
		workspace = held.workspace
		stamp, _, stampErr := s.accessStampAndEpoch(ctx, workspace)
		if stampErr != nil {
			return nil, stampErr
		}
		read.owner.source, err = captureMetadataLogicalSource(ctx, held.metadata, stamp)
	} else {
		installed, found, beginErr := s.BeginInstalledMetadataRead(ctx, workspace)
		if beginErr != nil {
			return nil, beginErr
		}
		if !found {
			if userID == "" {
				return nil, sql.ErrNoRows
			}
			return nil, ErrAuthForbidden
		}
		defer installed.Close()
		workspace = installed.workspace
		read.owner.source, err = installed.retainSource(ctx, workspace)
	}
	if err != nil {
		return nil, err
	}
	omitted := []string{
		"projectUpdates", "initiativeUpdates", "providerJobs", "commentActions",
		"loopRuns", "loopVersions", "loopAttachments",
	}
	if !handed {
		omitted = append(omitted, "issues", "comments", "activities", "notifications", "notificationDeliveries")
	}
	read.body, err = s.beginBootstrapBodyProjection(ctx, read.owner.source, omitted...)
	if err != nil {
		return nil, err
	}
	read.Data = read.body.Data
	if !handed {
		read.Data.Issues = []domain.Issue{}
		read.Data.Comments = map[string][]domain.Comment{}
		read.Data.Activities = map[string][]domain.ActivityEvent{}
		read.Data.Notifications = []domain.Notification{}
		read.Data.NotificationDeliveries = []domain.NotificationDelivery{}
	}
	snapshot := read.owner.metadataOutputContext(ctx)
	if userID == "" {
		read.Data.ViewerRole = "admin"
		if read.Data.Workspace.URLKey == "" || read.Data.Viewer.ID == "" {
			return read, ctx.Err()
		}
		tx := held.reader
		if !borrowed {
			tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
		}
		snapshot = context.WithValue(snapshot, workspaceReadKey{}, workspaceRead{
			database: s.db, workspace: workspace, reader: tx, metadata: read.Data,
		})
		if err := read.favoriteWork(snapshot); err != nil {
			return nil, err
		}
		if err := s.ApplyTeamDefaultFavorites(snapshot, &read.Data, read.Data.Viewer.ID); err != nil {
			return nil, err
		}
		return read, ctx.Err()
	}
	tx := held.reader
	if !borrowed {
		tx, err = s.db.BeginTx(ctx, metadataReadOptions(s.dialect))
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
	}
	snapshot = context.WithValue(snapshot, workspaceReadKey{}, workspaceRead{
		database: s.db, workspace: workspace, reader: tx, metadata: read.Data,
	})
	// Access is retained for the encoding boundary. Its memo owns policy
	// values until this read closes, including after the SQL snapshot ends.
	memoContext, _ := withResourcePolicyMemo(snapshot, read.owner)
	document := &DocumentRead{Data: read.Data, owner: read.owner}
	if err := document.documentProjectionWork(memoContext, read.Data.Workspace.ID, userID); err != nil {
		return nil, err
	}
	read.access, err = s.ResolveAccess(memoContext, workspace, userID, key)
	if err != nil {
		return nil, err
	}
	read.Data, err = s.PagedWorkspaceMetadata(memoContext, workspace, userID)
	if outputErr := metadataOutputError(snapshot); outputErr != nil {
		return nil, outputErr
	}
	if err != nil {
		return nil, err
	}
	read.Data.ProjectUpdates, read.Data.InitiativeUpdates = nil, nil
	read.Data.ProviderJobs, read.Data.CommentActions = nil, nil
	read.Data.LoopRuns, read.Data.LoopVersions, read.Data.LoopAttachments = nil, nil, nil
	if !borrowed {
		if err := tx.Rollback(); err != nil {
			return nil, err
		}
		if err := s.ValidateAccess(ctx, read.access); err != nil {
			return nil, err
		}
	}
	return read, ctx.Err()
}

func (r *IssueBootstrapRead) favoriteWork(ctx context.Context) error {
	document := &DocumentRead{Data: r.Data, owner: r.owner}
	if err := document.documentProjectionRows(ctx,
		[]string{"d.id", "d.team_id", "d.resource_type", "d.resource_id", "d.position", "d.created_at", "d.updated_at"},
		`FROM team_default_favorites d JOIN team_memberships tm ON tm.team_id=d.team_id AND tm.workspace_id=? AND tm.user_id=? JOIN workspace_memberships m ON m.workspace_id=tm.workspace_id AND m.user_id=tm.user_id AND m.status='active' WHERE d.workspace_key=?`,
		r.Data.Workspace.ID, r.Data.Viewer.ID, r.Data.Workspace.URLKey); err != nil {
		return err
	}
	return document.documentProjectionRows(ctx, []string{"resource_type", "resource_id"},
		`FROM team_default_favorite_overrides WHERE workspace_key=? AND user_id=? AND hidden=1`,
		r.Data.Workspace.URLKey, r.Data.Viewer.ID)
}

// WriteJSON is the only public encoding boundary for Data. Binding finishes
// before the first write, and the encoder retains exact opaque source nodes.
func (r *IssueBootstrapRead) WriteJSON(ctx context.Context, dst io.Writer, rewrite ...MetadataJSONRewriter) (err error) {
	if r == nil || r.body == nil || r.owner == nil || dst == nil || len(rewrite) > 1 {
		return ErrBoundedMetadataCoverage
	}
	// A handed read keeps the caller's authority snapshot, as the original
	// paged accessor does. A separate live read also cannot use its sole SQL
	// connection while that caller still owns the transaction.
	if r.principal != "" && !r.borrowed {
		if err := r.owner.store.ValidateAccess(ctx, r.access); err != nil {
			return err
		}
	}
	output, replacements, err := r.body.bind(ctx, r.Data)
	if err != nil {
		return err
	}
	if len(output.stores) != 1 {
		return ErrBoundedMetadataCoverage
	}
	ctx = context.WithValue(ctx, metadataWorkLeaseKey{}, nil)
	ctx = metadataLogicalSourceScratchContext(ctx, output)
	scratch := &ResourceRead{store: r.owner.store}
	defer scratch.Close()
	if err := scratch.reserve(ctx, logicalJSONStreamWork); err != nil {
		return err
	}
	stream := &logicalJSONStream{
		ctx: ctx, owner: scratch, view: &WebhookAggregateRead{store: r.owner.store},
		out: bufio.NewWriterSize(dst, 64<<10),
	}
	if len(rewrite) == 1 {
		stream.rewrite = rewrite[0]
	}
	stream.replace = func(node metadataLogicalNode) (metadataLogicalNode, error) {
		if node.source != output.stores[0] {
			return node, nil
		}
		if original, found := replacements[node.offset]; found {
			return original, nil
		}
		return node, nil
	}
	defer func() {
		for _, release := range stream.view.releases {
			release()
		}
		if stream.stage != nil {
			err = errors.Join(err, stream.stage.Close())
		}
	}()
	plan, err := stream.view.plan(ctx, reflect.TypeFor[domain.Bootstrap]())
	if err != nil {
		return err
	}
	if err := stream.text("{"); err != nil {
		return err
	}
	first := true
	for _, field := range plan {
		if len(field.index) != 1 {
			return ErrBoundedMetadataCoverage
		}
		node := output.fields[field.index[0]]
		omit, err := stream.view.omitted(ctx, node, field)
		if err != nil {
			return err
		}
		if omit {
			continue
		}
		if !first {
			if err := stream.text(","); err != nil {
				return err
			}
		}
		first = false
		if err := stream.stringString(field.name); err != nil {
			return err
		}
		if err := stream.text(":"); err != nil {
			return err
		}
		if err := stream.write(webhookJSONValue{node: node, quoted: field.quoted}, 1); err != nil {
			return err
		}
	}
	if err := stream.text("}\n"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return stream.out.Flush()
}
