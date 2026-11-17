package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	"flow/api/internal/domain"
)

// AgentReplyNotification declares the agent_task.replied callback: the anchor
// it notifies on and the people the server already resolved as recipients
// (the session's creator, its prompters and its coding-session subscribers).
// The writer adds every participant it can reach from the anchor's thread
// (comment authors and thread subscribers) to that audience.
type AgentReplyNotification struct {
	Kind     string
	Resource string
	UserIDs  []string
}

type agentReplyNotificationKey struct{}

// ContextWithAgentReplyNotification routes a bounded agent_task.replied
// mutation to its closed writer: the issue anchor keeps the issue-record scope
// (WithIssueRecordMutations), documents and projects their resource scope.
func ContextWithAgentReplyNotification(ctx context.Context, reply AgentReplyNotification) context.Context {
	return context.WithValue(ctx, agentReplyNotificationKey{}, reply)
}

func agentReplyNotificationFromContext(ctx context.Context) (AgentReplyNotification, bool) {
	reply, ok := ctx.Value(agentReplyNotificationKey{}).(AgentReplyNotification)
	return reply, ok && reply.Resource != "" && (reply.Kind == "issue" || reply.Kind == "document" || reply.Kind == "project")
}

func agentReplyNotificationMutation(ctx context.Context) bool {
	_, ok := agentReplyNotificationFromContext(ctx)
	return ok
}

// agentReplyAudience is the declared audience plus the thread participants of
// the anchor, read from the callback's own comment and subscription inputs.
func agentReplyAudience(ctx context.Context, inputs *issueHotInputs, reply AgentReplyNotification, comments []domain.Comment, subscriptions []domain.ThreadSubscription) (map[string]bool, error) {
	audience, err := inputs.keys(ctx, reply.UserIDs...)
	if err != nil {
		return nil, err
	}
	for _, comment := range comments {
		if err := inputs.addKey(ctx, audience, comment.User.ID); err != nil {
			return nil, err
		}
	}
	for _, subscription := range subscriptions {
		if err := inputs.addKey(ctx, audience, subscription.UserID); err != nil {
			return nil, err
		}
	}
	return audience, nil
}

// reserveAgentReplyOutput charges, before the callback, one notification and
// four deliveries per possible recipient and the copies of both collections.
func reserveAgentReplyOutput(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, audience map[string]bool) error {
	unit := uint64(reflect.TypeFor[domain.Notification]().Size())*4 +
		uint64(reflect.TypeFor[domain.NotificationDelivery]().Size())*16 +
		uint64(reflect.TypeFor[domain.NotificationPayload]().Size())*2 + 8192
	recipients := uint64(len(audience)) + 1
	if recipients > (metadataWorkBytes-(64<<10))/unit {
		return ErrMetadataWorkBudget
	}
	slots := uint64(len(data.Notifications))*uint64(reflect.TypeFor[domain.Notification]().Size())*2 +
		uint64(len(data.NotificationDeliveries))*uint64(reflect.TypeFor[domain.NotificationDelivery]().Size())*2
	if slots > metadataWorkBytes-recipients*unit-(64<<10) {
		return ErrMetadataWorkBudget
	}
	return owner.reserve(ctx, recipients*unit+slots+64<<10)
}

// validateAgentReplyNotifications accepts only notification and delivery
// edits addressed to the declared audience. Every other callback input,
// including issues, discussion content and metadata, must be unchanged.
func validateAgentReplyNotifications(before, after domain.Bootstrap, audience map[string]bool) error {
	refuse := func(what string) error {
		return fmt.Errorf("%w: agent reply callback changed %s", ErrBoundedMetadataCoverage, what)
	}
	left, right := reflect.ValueOf(before), reflect.ValueOf(after)
	for index := range left.NumField() {
		field := left.Type().Field(index)
		// Unpersisted callback fields (json "-": funcs, lookups, caches) are
		// not callback output.
		if field.Name == "Notifications" || field.Name == "NotificationDeliveries" || field.Tag.Get("json") == "-" {
			continue
		}
		if !reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface()) {
			return refuse(field.Name)
		}
	}
	if len(after.Notifications) < len(before.Notifications) || before.Notifications != nil && after.Notifications == nil {
		return refuse("its notification scope")
	}
	for index, notification := range after.Notifications {
		if index < len(before.Notifications) && reflect.DeepEqual(before.Notifications[index], notification) {
			continue
		}
		if !audience[notification.RecipientID] {
			return refuse("a notification outside its audience")
		}
		if index < len(before.Notifications) && before.Notifications[index].ID != notification.ID {
			return refuse("a notification identity")
		}
	}
	if len(after.NotificationDeliveries) < len(before.NotificationDeliveries) || before.NotificationDeliveries != nil && after.NotificationDeliveries == nil {
		return refuse("its delivery scope")
	}
	for index, delivery := range after.NotificationDeliveries {
		if index < len(before.NotificationDeliveries) {
			original := before.NotificationDeliveries[index]
			if reflect.DeepEqual(original, delivery) {
				continue
			}
			original.Status, original.UpdatedAt, original.NextAttemptAt = delivery.Status, delivery.UpdatedAt, delivery.NextAttemptAt
			if !reflect.DeepEqual(original, delivery) {
				return refuse("a delivery identity")
			}
		}
		if !audience[delivery.RecipientID] {
			return refuse("a delivery outside its audience")
		}
	}
	return nil
}

// loadAgentReplyIssueInputs completes the discussion inputs of the issue
// anchor: complete user records for the audience (the discussion directory
// keeps headers for everyone else, and the callback reads App), and the
// target's SLAs, which the full issue-scope input always carries.
func (s *SQLiteStore) loadAgentReplyIssueInputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, data *domain.Bootstrap) (map[string]bool, error) {
	reply, ok := agentReplyNotificationFromContext(ctx)
	if !ok || reply.Kind != "issue" || source == nil {
		return nil, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	inputs := &issueHotInputs{store: s, source: source}
	owner.mu.Lock()
	owner.release = append(owner.release, inputs.close)
	owner.mu.Unlock()
	if err := inputs.reserve(ctx, 64<<10); err != nil {
		return nil, err
	}
	audience, err := agentReplyAudience(ctx, inputs, reply, data.Comments[reply.Resource], data.ThreadSubscriptions)
	if err != nil {
		return nil, err
	}
	if err := inputs.selectSlice(ctx, "users", func(record metadataLogicalNode) (bool, error) {
		return issueHotMatches(ctx, record, "ID", audience)
	}); err != nil {
		return nil, err
	}
	selected := 0
	for index, user := range data.Users {
		if !audience[user.ID] {
			continue
		}
		if selected >= len(inputs.data.Users) || inputs.data.Users[selected].ID != user.ID {
			return nil, ErrBoundedMetadataCoverage
		}
		data.Users[index] = inputs.data.Users[selected]
		selected++
	}
	if selected != len(inputs.data.Users) {
		return nil, ErrBoundedMetadataCoverage
	}
	if narrowMutationInputs() {
		index, _, _ := metadataLogicalField("issueSlas")
		absent, err := source.fields[index].isNil(ctx)
		if err != nil {
			return nil, err
		}
		if !absent {
			inputs.data.IssueSLAs = []domain.IssueSLA{}
		}
		issues, err := inputs.keys(ctx, reply.Resource)
		if err != nil {
			return nil, err
		}
		if err := inputs.selectSlice(ctx, "issueSlas", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "IssueID", issues)
		}); err != nil {
			return nil, err
		}
		data.IssueSLAs = inputs.data.IssueSLAs
	}
	if err := reserveAgentReplyOutput(ctx, owner, *data, audience); err != nil {
		return nil, err
	}
	return audience, ctx.Err()
}

// agentReplyImageProfile is the document and project anchor of
// agent_task.replied on the scoped image writer (the bounded resource scope):
// the anchor, its thread subscriptions, the audience's users, preferences,
// push subscriptions and, for documents, the access inputs of documentRoleFor.
type agentReplyImageProfile struct{ reply AgentReplyNotification }

func (agentReplyImageProfile) kind() scopedImageWriteKind { return scopedImageAgentReply }

func (agentReplyImageProfile) extraFields() []string { return nil }

func (p agentReplyImageProfile) resolve(ctx context.Context, _ *ResourceRead, _ *metadataLogicalSource) (MutationScope, error) {
	reply, ok := agentReplyNotificationFromContext(ctx)
	scope, found := mutationScopeFromContext(ctx)
	if !ok || !found || !reflect.DeepEqual(reply, p.reply) || reply.Kind == "issue" || len(scope.Resources) != 1 || scope.Resources[0] != reply.Resource {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	checked := scope
	checked.Resources = nil
	if !reflect.DeepEqual(checked, MutationScope{}) {
		return MutationScope{}, ErrBoundedMetadataCoverage
	}
	return scope, nil
}

// agentReplyDocumentFields are every document field the callback and
// documentRoleFor read; the native document stays intact in the image.
var agentReplyDocumentFields = []string{"ID", "SlugID", "PreviousSlugIDs", "Title", "Creator", "Permissions", "TeamIDs", "InheritedTeamIDs"}

func agentReplySelectNodes(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource, field string, match func(metadataLogicalNode) (bool, error), names ...string) (reflect.Value, error) {
	index, _, _ := metadataLogicalField(field)
	node := source.fields[index]
	target := reflect.Zero(node.typ)
	absent, err := node.isNil(ctx)
	if err != nil {
		return target, err
	}
	if !absent {
		target = reflect.MakeSlice(node.typ, 0, 0)
	}
	err = node.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
		wanted, err := match(record)
		if err != nil || !wanted {
			return err
		}
		if err := owner.reserve(ctx, uint64(record.typ.Size())*2+64); err != nil {
			return err
		}
		var value reflect.Value
		if len(names) == 0 {
			value, err = cycleScheduleNativeValue(ctx, owner, record)
		} else {
			value, err = cycleScheduleProjectNode(ctx, owner, record, names...)
		}
		if err == nil {
			target = reflect.Append(target, value)
		}
		return err
	})
	return target, err
}

func agentReplyHeader(ctx context.Context, owner *ResourceRead, record metadataLogicalNode, names ...string) (reflect.Value, error) {
	scratch := &ResourceRead{store: owner.store}
	defer scratch.Close()
	return cycleScheduleProjectNode(ctx, scratch, record, names...)
}

func (p agentReplyImageProfile) inputs(ctx context.Context, owner *ResourceRead, source *metadataLogicalSource) (domain.Bootstrap, error) {
	var data domain.Bootstrap
	reply := p.reply
	if reply.Kind != "document" && reply.Kind != "project" || reply.Resource == "" {
		return data, ErrBoundedMetadataCoverage
	}
	if err := source.Retain(); err != nil {
		return data, err
	}
	inputs := &issueHotInputs{store: owner.store, source: source}
	owner.mu.Lock()
	owner.release = append(owner.release, inputs.close)
	owner.mu.Unlock()
	if err := inputs.reserve(ctx, 64<<10); err != nil {
		return data, err
	}
	for _, name := range []string{"workspace", "viewer", "viewerRole"} {
		index, _, _ := metadataLogicalField(name)
		value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
		if err != nil {
			return data, err
		}
		reflect.ValueOf(&data).Elem().Field(index).Set(value)
	}
	var comments []domain.Comment
	// The scoped content of the resource key is already in this image.
	commentsIndex, _, _ := metadataLogicalField("comments")
	if err := source.fields[commentsIndex].MapEntries(ctx, func(_, values metadataLogicalNode) error {
		return values.Children(ctx, func(_ reflect.Value, record metadataLogicalNode) error {
			if err := owner.reserve(ctx, uint64(reflect.TypeFor[domain.Comment]().Size())*2+64); err != nil {
				return err
			}
			header, err := agentReplyHeader(ctx, owner, record, "User")
			if err == nil {
				comments = append(comments, header.Interface().(domain.Comment))
			}
			return err
		})
	}); err != nil {
		return data, err
	}
	anchors, err := inputs.keys(ctx)
	if err != nil {
		return data, err
	}
	if reply.Kind == "document" {
		documents, err := agentReplySelectNodes(ctx, owner, source, "documents", func(record metadataLogicalNode) (bool, error) {
			header, err := agentReplyHeader(ctx, owner, record, "ID", "SlugID", "PreviousSlugIDs")
			if err != nil {
				return false, err
			}
			document := header.Interface().(domain.Document)
			return document.ID == reply.Resource || document.SlugID == reply.Resource || slices.Contains(document.PreviousSlugIDs, reply.Resource), nil
		}, agentReplyDocumentFields...)
		if err != nil {
			return data, err
		}
		data.Documents = documents.Interface().([]domain.Document)
		for _, document := range data.Documents {
			if err := inputs.addKey(ctx, anchors, document.ID); err != nil {
				return data, err
			}
		}
		subscriptions, err := agentReplySelectNodes(ctx, owner, source, "threadSubscriptions", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "IssueID", anchors)
		})
		if err != nil {
			return data, err
		}
		data.ThreadSubscriptions = subscriptions.Interface().([]domain.ThreadSubscription)
	} else {
		if err := inputs.addKey(ctx, anchors, reply.Resource); err != nil {
			return data, err
		}
		projects, err := agentReplySelectNodes(ctx, owner, source, "projects", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "ID", anchors)
		}, "ID", "Name", "Comments")
		if err != nil {
			return data, err
		}
		data.Projects = projects.Interface().([]domain.Project)
		for _, project := range data.Projects {
			comments = append(comments, project.Comments...)
		}
		subscriptions, err := agentReplySelectNodes(ctx, owner, source, "threadSubscriptions", func(record metadataLogicalNode) (bool, error) {
			return issueHotMatches(ctx, record, "ProjectID", anchors)
		})
		if err != nil {
			return data, err
		}
		data.ThreadSubscriptions = subscriptions.Interface().([]domain.ThreadSubscription)
	}
	audience, err := agentReplyAudience(ctx, inputs, reply, comments, data.ThreadSubscriptions)
	if err != nil {
		return data, err
	}
	inAudience := func(name string) func(metadataLogicalNode) (bool, error) {
		return func(record metadataLogicalNode) (bool, error) { return issueHotMatches(ctx, record, name, audience) }
	}
	users, err := agentReplySelectNodes(ctx, owner, source, "users", inAudience("ID"))
	if err != nil {
		return data, err
	}
	data.Users = users.Interface().([]domain.User)
	if err := inputs.selectMap(ctx, "notificationPreferences", audience); err != nil {
		return data, err
	}
	data.NotificationPreferences = inputs.data.NotificationPreferences
	push, err := agentReplySelectNodes(ctx, owner, source, "pushSubscriptions", inAudience("UserID"), "UserID", "Enabled")
	if err != nil {
		return data, err
	}
	data.PushSubscriptions = push.Interface().([]domain.PushSubscription)
	if reply.Kind == "document" {
		for _, name := range []string{"teams", "teamSettings"} {
			index, _, _ := metadataLogicalField(name)
			value, err := cycleScheduleNativeValue(ctx, owner, source.fields[index])
			if err != nil {
				return data, err
			}
			reflect.ValueOf(&data).Elem().Field(index).Set(value)
		}
		teamMembers, err := agentReplySelectNodes(ctx, owner, source, "teamMembers", inAudience("UserID"))
		if err != nil {
			return data, err
		}
		data.TeamMembers = teamMembers.Interface().([]domain.TeamMember)
		members, err := agentReplySelectNodes(ctx, owner, source, "members", func(record metadataLogicalNode) (bool, error) {
			header, err := agentReplyHeader(ctx, owner, record, "User")
			if err != nil {
				return false, err
			}
			return audience[header.Interface().(domain.WorkspaceMember).User.ID], nil
		})
		if err != nil {
			return data, err
		}
		data.Members = members.Interface().([]domain.WorkspaceMember)
	}
	return data, ctx.Err()
}

// The audience is recomputed from the callback input: it is a pure function
// of the declared users and the selected thread inputs.
func agentReplyImageAudience(data domain.Bootstrap, reply AgentReplyNotification) map[string]bool {
	audience := map[string]bool{}
	for _, id := range reply.UserIDs {
		audience[id] = true
	}
	for _, comments := range data.Comments {
		for _, comment := range comments {
			audience[comment.User.ID] = true
		}
	}
	for _, project := range data.Projects {
		for _, comment := range project.Comments {
			audience[comment.User.ID] = true
		}
	}
	for _, subscription := range data.ThreadSubscriptions {
		audience[subscription.UserID] = true
	}
	return audience
}

func (p agentReplyImageProfile) validate(before, after domain.Bootstrap) error {
	if len(after.Issues) != 0 || len(before.Issues) != 0 {
		return ErrBoundedMetadataCoverage
	}
	return validateAgentReplyNotifications(before, after, agentReplyImageAudience(before, p.reply))
}

func (p agentReplyImageProfile) image(ctx context.Context, _ *ResourceRead, source *metadataLogicalSource, before, after domain.Bootstrap) (*metadataLogicalSource, error) {
	if err := p.validate(before, after); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := source.Retain(); err != nil {
		return nil, err
	}
	return source, nil
}

func (p agentReplyImageProfile) reserveCallback(ctx context.Context, owner *ResourceRead, data domain.Bootstrap, payload any) error {
	reply := p.reply
	cost, err := metadataAggregatePayloadEncodingWork(ctx, reflect.ValueOf(payload), 0)
	if err != nil {
		return err
	}
	if err := owner.reserve(ctx, uint64(cost)); err != nil {
		return err
	}
	return reserveAgentReplyOutput(ctx, owner, data, agentReplyImageAudience(data, reply))
}
