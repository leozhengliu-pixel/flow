package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
)

type discussionRowsOwned struct {
	comments           []map[string][]domain.Comment
	commentSlices      [][]domain.Comment
	notifications      []map[string][]domain.Notification
	notificationSlices [][]domain.Notification
	deliveries         []*[]domain.NotificationDelivery
	raw                [][]byte
}

func (r *discussionRowsOwned) close() {
	if r == nil {
		return
	}
	for _, result := range r.comments {
		for _, comments := range result {
			clear(comments)
		}
		clear(result)
	}
	for _, comments := range r.commentSlices {
		clear(comments)
	}
	for _, result := range r.notifications {
		for _, notifications := range result {
			clear(notifications)
		}
		clear(result)
	}
	for _, notifications := range r.notificationSlices {
		clear(notifications)
	}
	for _, result := range r.deliveries {
		clear(*result)
		*result = nil
	}
	for _, raw := range r.raw {
		clear(raw)
	}
	*r = discussionRowsOwned{}
}

type discussionRowHeader struct {
	id, resource         string
	size                 int64
	idBlob, resourceBlob bool
}

// The caller holds r.mu throughout each stage, including all borrowed-tx SQL.
func (r *issueMutationRows) beginDiscussion(ctx context.Context, inputs ...string) error {
	if r.owner == nil {
		return errMetadataLogicalClosed
	}
	if r.owner.store == nil || r.tx == nil {
		return ErrBoundedMetadataCoverage
	}
	cost := uint64(64 << 10)
	for _, input := range inputs {
		if uint64(len(input)) > (metadataWorkBytes-cost)/8 {
			return ErrMetadataWorkBudget
		}
		cost += uint64(len(input)) * 8
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return err
	}
	if r.discussion == nil {
		r.discussion = &discussionRowsOwned{}
	}
	return nil
}

func (r *issueMutationRows) discussionHeaders(ctx context.Context, where string, args []any, order string, limit int) ([]discussionRowHeader, error) {
	selection := " FROM workspace_content_records WHERE " + where + order
	if limit > 0 {
		selection += " LIMIT " + strconv.Itoa(limit)
	}
	idLength, resourceLength := r.owner.byteLength("id"), r.owner.byteLength("resource_id")
	var count, idSize, resourceSize int64
	query := "SELECT COUNT(*),COALESCE(MAX(" + idLength + "),0),COALESCE(MAX(" + resourceLength + "),0) FROM (SELECT id,resource_id" + selection + ") discussion_selection"
	if err := r.tx.QueryRowContext(ctx, query, args...).Scan(&count, &idSize, &resourceSize); err != nil {
		return nil, err
	}
	if count < 0 || idSize < 0 || resourceSize < 0 || count > metadataWorkBytes/512 || idSize > metadataWorkBytes/8 || resourceSize > metadataWorkBytes/8 {
		return nil, ErrMetadataWorkBudget
	}
	slots := uint64(count) + 1
	perRow := uint64(512) + uint64(idSize)*8 + uint64(resourceSize)*8
	if slots > (metadataWorkBytes-(64<<10))/perRow {
		return nil, ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, slots*perRow+64<<10); err != nil {
		return nil, err
	}
	// A concurrent growth can return only one extra bounded header before the
	// count check fails. Every locator is also capped before driver allocation.
	scanLimit := count + 1
	if limit > 0 {
		scanLimit = min(scanLimit, int64(limit))
	}
	storageID, storageResource := "0", "0"
	if r.owner.store.dialect == "sqlite" {
		storageID = "CASE WHEN typeof(id)='blob' THEN 1 ELSE 0 END"
		storageResource = "CASE WHEN typeof(resource_id)='blob' THEN 1 ELSE 0 END"
	}
	query = "SELECT CASE WHEN " + idLength + "<=? THEN id ELSE NULL END,CASE WHEN " + resourceLength + "<=? THEN resource_id ELSE NULL END," + r.owner.byteLength("data") + "," + storageID + "," + storageResource +
		" FROM workspace_content_records WHERE " + where + order + " LIMIT " + strconv.FormatInt(scanLimit, 10)
	values := append([]any{idSize, resourceSize}, args...)
	rows, err := r.tx.QueryContext(ctx, query, values...)
	if err != nil {
		return nil, err
	}
	headers := make([]discussionRowHeader, 0, int(count))
	for rows.Next() {
		var id, resource sql.NullString
		var size sql.NullInt64
		var header discussionRowHeader
		if err := rows.Scan(&id, &resource, &size, &header.idBlob, &header.resourceBlob); err != nil {
			rows.Close()
			return nil, err
		}
		if int64(len(headers)) >= count || !id.Valid || !resource.Valid || !size.Valid || int64(len(id.String)) > idSize || int64(len(resource.String)) > resourceSize || size.Int64 < 0 {
			rows.Close()
			return nil, io.ErrUnexpectedEOF
		}
		if size.Int64 > metadataWorkBytes/16 {
			rows.Close()
			return nil, ErrMetadataWorkBudget
		}
		header.id, header.resource, header.size = id.String, resource.String, size.Int64
		headers = append(headers, header)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if int64(len(headers)) != count {
		return nil, io.ErrUnexpectedEOF
	}
	return headers, nil
}

func (r *issueMutationRows) discussionPayloads(ctx context.Context, kind string, headers []discussionRowHeader, size int64, visit func([]byte) error) error {
	if len(headers) == 0 || len(headers) > issueMutationRowsBatchSize || size < 0 || uint64(size) > (metadataWorkBytes-(64<<10)-uint64(len(headers))*512)/3 {
		return ErrMetadataWorkBudget
	}
	if err := r.owner.reserve(ctx, uint64(size)*3+uint64(len(headers))*512+64<<10); err != nil {
		return err
	}
	locator := func(value string, blob bool) any {
		if r.owner.store.dialect == "mysql" || r.owner.store.dialect == "sqlite" && blob {
			return []byte(value)
		}
		return value
	}
	queries, args := make([]string, 0, len(headers)), make([]any, 0, len(headers)*5)
	for index, header := range headers {
		queries = append(queries, "SELECT "+strconv.Itoa(index)+" AS ordinal,CASE WHEN "+r.owner.byteLength("data")+"<=? THEN data ELSE NULL END AS data,"+r.owner.byteLength("data")+" AS row_bytes FROM workspace_content_records WHERE workspace_key=? AND kind=? AND resource_id=? AND id=?")
		args = append(args, header.size, r.workspace, kind, locator(header.resource, header.resourceBlob), locator(header.id, header.idBlob))
	}
	rows, err := r.tx.QueryContext(ctx, strings.Join(queries, " UNION ALL ")+" ORDER BY ordinal", args...)
	if err != nil {
		return err
	}
	bodies := make([][]byte, 0, len(headers))
	for rows.Next() {
		var ordinal int
		var body []byte
		var size sql.NullInt64
		if err := rows.Scan(&ordinal, &body, &size); err != nil {
			rows.Close()
			return err
		}
		if ordinal != len(bodies) || ordinal >= len(headers) || !size.Valid || size.Int64 != headers[ordinal].size || int64(len(body)) != headers[ordinal].size {
			rows.Close()
			return io.ErrUnexpectedEOF
		}
		bodies = append(bodies, body)
		r.discussion.raw = append(r.discussion.raw, body)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(bodies) != len(headers) {
		return io.ErrUnexpectedEOF
	}
	for _, body := range bodies {
		if err := visit(body); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func discussionSelectedRows[T any](ctx context.Context, r *issueMutationRows, kind, where string, args []any, order string, limit int, visit func(T) error) error {
	headers, err := r.discussionHeaders(ctx, where, args, order, limit)
	if err != nil {
		return err
	}
	for start := 0; start < len(headers); {
		end, size := start+1, headers[start].size
		for end < len(headers) && end-start < issueMutationRowsBatchSize && size <= issueMutationRowsBatchBytes && headers[end].size <= issueMutationRowsBatchBytes-size {
			size += headers[end].size
			end++
		}
		if err := r.discussionPayloads(ctx, kind, headers[start:end], size, func(raw []byte) error {
			work, err := metadataAggregatePayloadJSONWork(ctx, raw)
			if err != nil {
				return err
			}
			fixed := uint64(reflect.TypeFor[T]().Size())*4 + 4096
			if fixed > metadataWorkBytes || work > metadataWorkBytes-fixed {
				return ErrMetadataWorkBudget
			}
			if err := r.owner.reserve(ctx, work+fixed); err != nil {
				return err
			}
			var value T
			if err := json.Unmarshal(raw, &value); err != nil {
				return err
			}
			return visit(value)
		}); err != nil {
			return err
		}
		start = end
	}
	return ctx.Err()
}

func (r *issueMutationRows) discussionComment(ctx context.Context, issue, id string) (*domain.Comment, error) {
	var selected *domain.Comment
	err := discussionSelectedRows(ctx, r, "comment", "workspace_key=? AND kind='comment' AND resource_id=? AND id=?", []any{r.workspace, issue, id}, "", 1, func(comment domain.Comment) error {
		selected = &comment
		return nil
	})
	return selected, err
}

func (r *issueMutationRows) loadDiscussionComment(ctx context.Context, issue, commentID string) (map[string][]domain.Comment, error) {
	if r == nil {
		return nil, errMetadataLogicalClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.beginDiscussion(ctx, r.workspace, issue, commentID); err != nil {
		return nil, err
	}
	issue = strings.Clone(issue)
	result := map[string][]domain.Comment{}
	r.discussion.comments = append(r.discussion.comments, result)
	if commentID == "" {
		return result, nil
	}
	target, err := r.discussionComment(ctx, issue, commentID)
	if err != nil || target == nil {
		return result, err
	}
	root := *target
	seen := map[string]bool{root.ID: true}
	ancestors := []domain.Comment{}
	for depth := 0; root.ParentID != nil && *root.ParentID != "" && depth < 16; depth++ {
		parent, err := r.discussionComment(ctx, issue, *root.ParentID)
		if err != nil {
			return result, err
		}
		if parent == nil || seen[parent.ID] {
			break
		}
		if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Comment]().Size())*4+1024); err != nil {
			return result, err
		}
		ancestors = append(ancestors, root)
		seen[parent.ID] = true
		root = *parent
	}
	comments := append([]domain.Comment{root}, ancestors...)
	frontier := []string{root.ID}
	for len(frontier) > 0 && len(comments) < 1000 {
		parentID := frontier[0]
		frontier = frontier[1:]
		err := discussionSelectedRows(ctx, r, "comment", "workspace_key=? AND kind='comment' AND resource_id=? AND parent_id=?", []any{r.workspace, issue, parentID}, " ORDER BY created_at,id", 1000, func(comment domain.Comment) error {
			if !seen[comment.ID] {
				if err := r.owner.reserve(ctx, uint64(reflect.TypeFor[domain.Comment]().Size())*4+1024); err != nil {
					return err
				}
				seen[comment.ID] = true
				comments = append(comments, comment)
				frontier = append(frontier, comment.ID)
			} else if comment.ID != root.ID {
				if err := r.owner.reserve(ctx, 512); err != nil {
					return err
				}
				frontier = append(frontier, comment.ID)
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	if !seen[target.ID] {
		comments = append(comments, *target)
	}
	result[issue] = comments
	r.discussion.commentSlices = append(r.discussion.commentSlices, comments)
	return result, nil
}

func (r *issueMutationRows) activeDiscussionNotifications(ctx context.Context, issue string) (map[string][]domain.Notification, error) {
	if r == nil {
		return nil, errMetadataLogicalClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.beginDiscussion(ctx, r.workspace, issue); err != nil {
		return nil, err
	}
	issue = strings.Clone(issue)
	result := map[string][]domain.Notification{}
	r.discussion.notifications = append(r.discussion.notifications, result)
	defer func() {
		for _, notifications := range result {
			r.discussion.notificationSlices = append(r.discussion.notificationSlices, notifications)
		}
	}()
	where := "workspace_key=? AND kind='notification' AND resource_id=? AND status='unread' AND " + r.owner.store.jsonText("data", "updatedAt") + ">=?"
	cutoff := time.Now().UTC().Add(-6 * time.Hour).Format(time.RFC3339Nano)
	err := discussionSelectedRows(ctx, r, "notification", where, []any{r.workspace, issue, cutoff}, " ORDER BY created_at,id", 0, func(notification domain.Notification) error {
		result[issue] = append(result[issue], notification)
		return nil
	})
	return result, err
}

func (r *issueMutationRows) issueNotificationDeliveries(ctx context.Context, notifications []domain.Notification) ([]domain.NotificationDelivery, error) {
	if r == nil {
		return nil, errMetadataLogicalClosed
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.beginDiscussion(ctx, r.workspace); err != nil {
		return nil, err
	}
	if uint64(len(notifications)) > metadataWorkBytes/64 {
		return nil, ErrMetadataWorkBudget
	}
	cost := uint64(len(notifications))*64 + 4096
	for _, notification := range notifications {
		if cost > metadataWorkBytes || uint64(len(notification.ID)) > (metadataWorkBytes-cost)/8 {
			return nil, ErrMetadataWorkBudget
		}
		cost += uint64(len(notification.ID)) * 8
	}
	if err := r.owner.reserve(ctx, cost); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(notifications))
	for _, notification := range notifications {
		ids = append(ids, strings.Clone(notification.ID))
	}
	result := []domain.NotificationDelivery{}
	r.discussion.deliveries = append(r.discussion.deliveries, &result)
	for start := 0; start < len(ids); start += 500 {
		where, args := bindList("parent_id", ids[start:min(start+500, len(ids))])
		args = append([]any{r.workspace}, args...)
		err := discussionSelectedRows(ctx, r, "delivery", "workspace_key=? AND kind='delivery' AND "+where, args, "", 0, func(delivery domain.NotificationDelivery) error {
			result = append(result, delivery)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
