package store

import (
	"context"
	"database/sql"
	"reflect"

	"flow/api/internal/domain"
)

var issueDiscussionInputFields = []string{
	"users", "teams", "states", "labels", "projects", "teamSettings",
	"notificationPreferences", "pushSubscriptions", "threadSubscriptions", "issueSlas",
}

func (r *issueHotInputs) discussionCollectionShape(ctx context.Context, field string) error {
	index, _, found := metadataLogicalField(field)
	if !found {
		return ErrMetadataRecordQuery
	}
	nilValue, err := r.source.fields[index].isNil(ctx)
	if err != nil || nilValue {
		return err
	}
	target := reflect.ValueOf(&r.data).Elem().Field(index)
	if err := r.reserve(ctx, uint64(target.Type().Size())*2+128); err != nil {
		return err
	}
	switch target.Kind() {
	case reflect.Slice:
		target.Set(reflect.MakeSlice(target.Type(), 0, 0))
	case reflect.Map:
		target.Set(reflect.MakeMap(target.Type()))
	default:
		return ErrMetadataRecordQuery
	}
	return nil
}

func (r *issueHotInputs) discussionUsers(ctx context.Context, references, recipients map[string]bool) error {
	// A new comment's text is not available to this loader. Keep the ordered
	// mention/existence directory without retaining unrelated user bodies.
	if err := r.selectSlice(ctx, "users", func(metadataLogicalNode) (bool, error) {
		return true, nil
	}, "ID", "Email", "Name", "DisplayName"); err != nil {
		return err
	}
	headers := r.data.Users
	for _, user := range headers {
		if err := r.addKey(ctx, recipients, user.ID); err != nil {
			return err
		}
	}
	r.data.Users = nil
	if headers != nil {
		r.data.Users = []domain.User{}
	}
	if err := r.selectSlice(ctx, "users", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", references)
	}); err != nil {
		return err
	}
	full := r.data.Users
	selected := 0
	for index, user := range headers {
		if !references[user.ID] {
			continue
		}
		if selected >= len(full) || full[selected].ID != user.ID {
			return ErrBoundedMetadataCoverage
		}
		headers[index] = full[selected]
		selected++
	}
	if selected != len(full) {
		return ErrBoundedMetadataCoverage
	}
	// Replacing in place preserves both the first duplicate consulted by
	// userByID and the last duplicate chosen by newIssueReferences.
	r.data.Users = headers
	return nil
}

// loadIssueDiscussionInputs requires the caller's writeMu interval. The SQL
// issue/content preimage and live actor already belong to that caller; only
// read-only installed callback inputs are added here, after complete admission.
func (s *SQLiteStore) loadIssueDiscussionInputs(ctx context.Context, workspace string, data *domain.Bootstrap) (_ *issueHotInputs, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, found := s.storedWorkspace(workspace); !found {
		return nil, sql.ErrNoRows
	}
	read := &issueHotInputs{store: s}
	defer func() {
		if err != nil {
			read.close()
		}
	}()
	if err := read.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	read.source, err = s.retainMetadataLogicalSource(metadataLogicalUnpinnedContext(ctx), workspace)
	if err != nil {
		return nil, err
	}
	narrow := narrowMutationInputs()
	for _, field := range issueDiscussionInputFields {
		if field == "issueSlas" && narrow {
			continue
		}
		if err := read.discussionCollectionShape(ctx, field); err != nil {
			return nil, err
		}
	}
	users, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return nil, err
	}
	recipients, err := read.keys(ctx, data.Viewer.ID)
	if err != nil {
		return nil, err
	}
	teams, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	states, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	labels, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	issues, err := read.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, issue := range data.Issues {
		for _, pair := range []struct {
			keys map[string]bool
			id   string
		}{{issues, issue.ID}, {teams, issue.Team.ID}, {states, issue.State.ID}, {users, issue.Creator.ID}} {
			if err := read.addKey(ctx, pair.keys, pair.id); err != nil {
				return nil, err
			}
		}
		for _, user := range []*domain.User{issue.Assignee, issue.Delegate} {
			if user != nil {
				if err := read.addKey(ctx, users, user.ID); err != nil {
					return nil, err
				}
			}
		}
		for _, id := range issue.SubscriberIDs {
			if err := read.addKey(ctx, recipients, id); err != nil {
				return nil, err
			}
		}
		if issue.Project != nil {
			if err := read.addKey(ctx, projects, issue.Project.ID); err != nil {
				return nil, err
			}
		}
		for _, label := range issue.Labels {
			if err := read.addKey(ctx, labels, label.ID); err != nil {
				return nil, err
			}
		}
	}
	for issueID, comments := range data.Comments {
		if err := read.addKey(ctx, issues, issueID); err != nil {
			return nil, err
		}
		for _, comment := range comments {
			if err := read.addKey(ctx, users, comment.User.ID); err != nil {
				return nil, err
			}
			if err := read.addKey(ctx, recipients, comment.User.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, activities := range data.Activities {
		for _, activity := range activities {
			if err := read.addKey(ctx, users, activity.Actor.ID); err != nil {
				return nil, err
			}
		}
	}
	for _, notification := range data.Notifications {
		if err := read.addKey(ctx, users, notification.Actor.ID); err != nil {
			return nil, err
		}
		if err := read.addKey(ctx, recipients, notification.RecipientID); err != nil {
			return nil, err
		}
	}
	for _, delivery := range data.NotificationDeliveries {
		if err := read.addKey(ctx, recipients, delivery.RecipientID); err != nil {
			return nil, err
		}
	}
	if err := read.discussionUsers(ctx, users, recipients); err != nil {
		return nil, err
	}
	for _, selection := range []struct {
		field string
		keys  map[string]bool
		names []string
	}{
		{"teams", teams, nil},
		{"states", states, nil},
		{"labels", labels, nil},
		{"projects", projects, []string{"ID", "Name", "Icon", "Color"}},
	} {
		if err := read.selectSlice(ctx, selection.field, func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "ID", selection.keys)
		}, selection.names...); err != nil {
			return nil, err
		}
	}
	if err := read.selectMap(ctx, "teamSettings", teams, "ResolvedSummaries", "Timezone"); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "threadSubscriptions", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "IssueID", issues)
	}); err != nil {
		return nil, err
	}
	for _, subscription := range read.data.ThreadSubscriptions {
		if err := read.addKey(ctx, recipients, subscription.UserID); err != nil {
			return nil, err
		}
	}
	if err := read.selectMap(ctx, "notificationPreferences", recipients,
		"UserID", "Inbox", "Email", "Desktop", "Chat", "EmailFormat", "ImmediateUrgent", "DelayLowPriority"); err != nil {
		return nil, err
	}
	if err := read.selectSlice(ctx, "pushSubscriptions", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "UserID", recipients)
	}, "UserID", "Enabled"); err != nil {
		return nil, err
	}
	// The old narrow discussion input omits SLAs. Its full-input switch does
	// expose them to notificationUrgent, so retain that distinction.
	if !narrow {
		if err := read.selectSlice(ctx, "issueSlas", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "IssueID", issues)
		}); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input := reflect.ValueOf(read.data)
	target := reflect.ValueOf(data).Elem()
	for _, field := range issueDiscussionInputFields {
		index, _, _ := metadataLogicalField(field)
		target.Field(index).Set(input.Field(index))
	}
	return read, nil
}
