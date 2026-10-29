package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"flow/api/internal/domain"
	"flow/api/internal/store"
)

// Quick commands of direct messages with the bot (chat_dm_agent.go). Each
// runs as the member, after chatDMAuthorize, and shows only what they can open
// in Flow.

// chatDMComment adds the member's reply to a personal notification card as a
// comment on its issue.
func (s *server) chatDMComment(ctx context.Context, dm chatDMRequest, thread chatDMThread, text string) error {
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(dm.authContext(ctx), dm.target.Workspace, dm.identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(thread.IssueID), nil, &issue); err != nil {
		return s.chatDMCommandFailure(ctx, dm, err, chatDMText(dm.zh, "You can no longer open this issue in Flow.", "你已无法在 Flow 中打开这个事项。"))
	}
	if strings.TrimSpace(text) == "" {
		return s.chatDMNotice(ctx, dm, chatDMText(dm.zh, "Reply with text to comment on "+issue.Identifier+".", "回复文字即可评论 "+issue.Identifier+"。"))
	}
	commentCtx := store.WithCommentCreationKey(dm.authContext(ctx), "comment_chat_"+chatKey(dm.record.ConnectionID, dm.command.MessageID))
	var comment domain.Comment
	if err := s.chatAuthorizedMutation(commentCtx, dm.target.Workspace, dm.identity.UserID, http.MethodPost, "/api/issue-records/"+url.PathEscape(issue.ID)+"/comments", domain.CommentCreateInput{Body: text}, &comment); err != nil {
		return s.chatDMCommandFailure(ctx, dm, err, chatDMText(dm.zh, "Flow could not add your comment to "+issue.Identifier+".", "Flow 无法在 "+issue.Identifier+" 上添加你的评论。"))
	}
	link := s.chatDMWorkspaceURL(dm.target.Workspace, "/issue/"+url.PathEscape(issue.Identifier))
	card := chatDMCard(chatDMText(dm.zh, "Comment added", "已添加评论"), "green", chatCardText(chatDMRedact(issue.Identifier+" "+issue.Title, dm.zh)), chatDMButtons(chatDMLinkButton(chatDMText(dm.zh, "Open in Flow", "在 Flow 中打开"), link, false)))
	_, _, err := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":comment", card)
	return err
}

// chatDMCommandFailure answers a refused command with message (or the
// platform's own reason for a refused write); other errors retry.
func (s *server) chatDMCommandFailure(ctx context.Context, dm chatDMRequest, err error, message string) error {
	var failure *chatMutationError
	if !errors.As(err, &failure) || failure.Status < 400 || failure.Status >= 500 {
		return err
	}
	if failure.Message == chatReconnectMessage || failure.Status == http.StatusUnauthorized {
		return s.chatDMFailure(ctx, dm, err)
	}
	if failure.Status == http.StatusBadRequest && failure.Message != "" {
		message += " " + failure.Message
	}
	return s.chatDMNotice(ctx, dm, message)
}

// chatDMNewIssueForm replies with a form that creates an issue: team, title,
// description, assignee and priority.
func (s *server) chatDMNewIssueForm(ctx context.Context, dm chatDMRequest, title string) error {
	teams := []map[string]any{}
	teamIDs := []string{}
	for _, team := range dm.metadata.Teams {
		if len(teams) == 100 {
			break
		}
		if team.ArchivedAt != nil || team.RetiredAt != nil || !teamContentVisible(dm.metadata, team.ID) {
			continue
		}
		teams = append(teams, chatOption(team.ID, team.Name))
		teamIDs = append(teamIDs, team.ID)
	}
	if len(teamIDs) == 0 {
		return s.chatDMNotice(ctx, dm, chatDMText(dm.zh, "You have no team in this workspace to create issues in.", "你在这个工作区没有可以创建事项的团队。"))
	}
	assignees, assigneeIDs := chatAssigneeOptions(dm.metadata, dm.identity.UserID)
	if len(assignees) > 0 {
		assignees[0] = chatOption("__unassigned", chatDMText(dm.zh, "Unassigned", "未分配"))
	}
	actionID := "chat_action_" + chatKey(dm.record.ConnectionID, dm.job.DedupKey, "create")
	action := chatDMAction{Kind: "dm_create", OpenID: dm.command.OpenID, WorkspaceKey: dm.target.Workspace, TeamIDs: teamIDs, AssigneeIDs: assigneeIDs, Chinese: dm.zh}
	titleInput := chatInput("title", chatDMText(dm.zh, "Issue title", "事项标题"), title, 1000)
	card := chatDMCard(chatDMText(dm.zh, "New issue", "新建事项"), "blue",
		chatCardText(chatDMText(dm.zh, "Workspace: ", "工作区：")+dm.target.Name),
		chatForm(
			chatSelect("teamId", chatDMText(dm.zh, "Team", "团队"), teamIDs[0], teams),
			titleInput,
			chatDMOptionalInput("description", chatDMText(dm.zh, "Description (optional)", "描述（可选）"), true),
			chatSelect("assigneeId", chatDMText(dm.zh, "Assignee", "负责人"), "__unassigned", assignees),
			chatSelect("priority", chatDMText(dm.zh, "Priority", "优先级"), "0", chatDMPriorityOptions(dm.zh)),
			chatSubmit(actionID, chatDMText(dm.zh, "Create issue", "创建事项")),
		))
	_, err := s.chatDMSendWithActions(ctx, dm, dm.job.DedupKey+":create", card, []chatDMPendingAction{{ID: actionID, Action: action}}, chatDMFormLifetime)
	return err
}

// chatDMCreateIssue creates the issue the form describes, as the member.
func (s *server) chatDMCreateIssue(ctx context.Context, dm chatDMRequest, action chatDMAction) error {
	if handled, err := s.chatDMAuthorize(ctx, &dm); handled {
		return err
	}
	values := dm.command.Values
	teamID := chatString(values, "teamId")
	title := chatString(values, "title")
	invalid := func() error {
		return s.chatDMNotice(ctx, dm, chatDMText(dm.zh, "The form has invalid fields. Send “new issue” and try again.", "表单内容无效。请重新发送“新建事项”再试。"))
	}
	if !slices.Contains(action.TeamIDs, teamID) || title == "" || utf8.RuneCountInString(title) > 1000 {
		return invalid()
	}
	input := domain.IssueCreateInput{Title: title, TeamID: teamID, Description: chatString(values, "description")}
	if assigneeID := chatString(values, "assigneeId"); assigneeID != "" && assigneeID != "__unassigned" {
		if !slices.Contains(action.AssigneeIDs, assigneeID) {
			return invalid()
		}
		input.AssigneeID = &assigneeID
	}
	if raw := chatString(values, "priority"); raw != "" {
		priority, err := strconv.Atoi(raw)
		if err != nil || priority < 0 || priority > 4 {
			return invalid()
		}
		input.Priority = &priority
	}
	if used, err := s.store.ConsumeChatAction(ctx, dm.record.WorkspaceKey, dm.record.ConnectionID, dm.command.ActionID); err != nil || !used {
		return err
	}
	createCtx := store.WithIssueCreationKey(dm.authContext(ctx), "issue_chat_"+chatKey(dm.record.ConnectionID, dm.command.ActionID))
	var issue domain.Issue
	if err := s.chatAuthorizedMutation(createCtx, dm.target.Workspace, dm.identity.UserID, http.MethodPost, "/api/issue-records", input, &issue); err != nil {
		return s.chatDMCommandFailure(ctx, dm, err, chatDMText(dm.zh, "Flow could not create the issue.", "Flow 无法创建这个事项。"))
	}
	card := s.chatDMIssueCard(dm.target.Workspace, issue, dm.zh)
	card["header"].(map[string]any)["template"] = "green"
	card["elements"] = append([]map[string]any{chatCardText(chatDMText(dm.zh, "Created ", "已创建 ") + issue.Identifier)}, card["elements"].([]map[string]any)...)
	return s.chatDMUpdate(ctx, dm.record, dm.command.MessageID, card)
}

// chatDMMyIssues lists the member's open assigned issues, most recently
// updated first.
func (s *server) chatDMMyIssues(ctx context.Context, dm chatDMRequest) error {
	_, access, err := s.store.IssueQueryAccess(ctx, dm.target.Workspace, dm.identity.UserID)
	if errors.Is(err, store.ErrAuthForbidden) {
		return s.chatDMFailure(ctx, dm, &chatMutationError{Status: http.StatusForbidden})
	}
	if err != nil {
		return err
	}
	page, err := s.store.QueryIssueRecords(ctx, store.IssueRecordQuery{Workspace: dm.target.Workspace, Access: &access, Sort: "updatedAt", Direction: "desc", Limit: 10, Filter: store.IssueFilter{And: []store.IssueFilter{{Field: "assignee", Values: []string{dm.identity.UserID}}, {Field: "statusType", Operator: "isNot", Values: []string{"completed", "canceled"}}}}})
	if err != nil {
		return err
	}
	lines := make([]string, 0, len(page.Items))
	for _, issue := range page.Items {
		lines = append(lines, "- "+chatDMRedact(chatDMIssueLine(s, dm.target.Workspace, issue), dm.zh))
	}
	card := chatDMListCard(chatDMText(dm.zh, "My issues", "我的事项"), lines, chatDMText(dm.zh, "Nothing open is assigned to you.", "没有分配给你的未完成事项。"), chatDMText(dm.zh, "Open My issues", "打开我的事项"), s.chatDMWorkspaceURL(dm.target.Workspace, "/my-issues"))
	_, _, err = s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":my-issues", card)
	return err
}

// chatDMInbox lists the member's latest notifications, unread first marked,
// leaving out those about issues they can no longer open.
func (s *server) chatDMInbox(ctx context.Context, dm chatDMRequest) error {
	data, ok := s.store.WorkspaceMetadata(dm.target.Workspace)
	if !ok {
		return chatInvalid("workspace unavailable")
	}
	_, access, err := s.store.IssueQueryAccess(ctx, dm.target.Workspace, dm.identity.UserID)
	if errors.Is(err, store.ErrAuthForbidden) {
		return s.chatDMFailure(ctx, dm, &chatMutationError{Status: http.StatusForbidden})
	}
	if err != nil {
		return err
	}
	page, err := s.store.QueryNotifications(ctx, store.NotificationQuery{Workspace: dm.target.Workspace, UserID: dm.identity.UserID, Limit: 10})
	if err != nil {
		return err
	}
	issueIDs := []string{}
	for _, notification := range page.Notifications {
		if notification.IssueID != "" && notification.ReviewID == "" {
			issueIDs = append(issueIDs, notification.IssueID)
		}
	}
	visible := map[string]bool{}
	if len(issueIDs) > 0 {
		if visible, err = s.store.VisibleIssueRecordIDs(ctx, store.IssueRecordQuery{Workspace: dm.target.Workspace, Access: &access}, issueIDs); err != nil {
			return err
		}
		for _, id := range issueIDs {
			if visible[id] && !slices.ContainsFunc(data.Issues, func(issue domain.Issue) bool { return issue.ID == id }) {
				if issue, err := s.store.IssueRecord(ctx, dm.target.Workspace, id); err == nil {
					data.Issues = append(data.Issues, issue)
				}
			}
		}
	}
	root := s.chatDMWorkspaceURL(dm.target.Workspace, "")
	lines := []string{}
	for _, notification := range page.Notifications {
		if notification.IssueID != "" && notification.ReviewID == "" && !visible[notification.IssueID] {
			continue
		}
		item, err := notificationSummary(&data, root, notification)
		if err != nil {
			continue
		}
		line := "- "
		if notification.ReadAt == nil {
			line += "**●** "
		}
		line += chatDMMarkdownLink(item.Title, item.URL)
		if item.Line != "" {
			line += " — " + chatDMMarkdownEscape(item.Line)
		}
		lines = append(lines, chatDMRedact(line, dm.zh))
	}
	title := chatDMText(dm.zh, "Inbox", "收件箱")
	if page.UnreadCount > 0 {
		title += " (" + strconv.Itoa(page.UnreadCount) + chatDMText(dm.zh, " unread)", " 条未读)")
	}
	card := chatDMListCard(title, lines, chatDMText(dm.zh, "Your inbox is empty.", "收件箱是空的。"), chatDMText(dm.zh, "Open Inbox", "打开收件箱"), root+"/inbox")
	_, _, err = s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":inbox", card)
	return err
}

// chatDMLink is a pasted link to a Flow issue or project.
type chatDMLink struct {
	Workspace, Kind, Ref string
}

func (s *server) chatDMFlowLink(text string) (chatDMLink, bool) {
	text = strings.Trim(strings.TrimSpace(text), "<>")
	if text == "" || strings.ContainsAny(text, " \t\n") {
		return chatDMLink{}, false
	}
	link, err := url.Parse(text)
	origin, originErr := url.Parse(s.chatAppOrigin())
	if err != nil || originErr != nil || !strings.EqualFold(link.Scheme, origin.Scheme) || !strings.EqualFold(link.Host, origin.Host) {
		return chatDMLink{}, false
	}
	parts := strings.Split(strings.Trim(link.Path, "/"), "/")
	if len(parts) < 3 || parts[0] == "" || parts[2] == "" || parts[1] != "issue" && parts[1] != "project" {
		return chatDMLink{}, false
	}
	return chatDMLink{Workspace: parts[0], Kind: parts[1], Ref: parts[2]}, true
}

// chatDMLinkPreview shows what a pasted link is, when the member can open it.
func (s *server) chatDMLinkPreview(ctx context.Context, dm chatDMRequest, link chatDMLink) error {
	// With the app's link preview configured, the platform already shows a
	// preview of the link (chat_unfurl.go); a second card would repeat it.
	if s.chatLinkPreviewConfigured(ctx, dm.record) {
		return nil
	}
	noAccess := chatDMText(dm.zh, "You can't open this link in Flow, or it no longer exists.", "你无法在 Flow 中打开这个链接，或它已不存在。")
	if link.Workspace != dm.target.Workspace {
		targets, err := s.chatDMTargets(ctx, dm.record, dm.identity)
		if err != nil {
			return err
		}
		target, ok := chatDMFindTarget(targets, link.Workspace)
		if !ok {
			return s.chatDMNotice(ctx, dm, noAccess)
		}
		dm.target = target
		if handled, err := s.chatDMAuthorize(ctx, &dm); handled {
			return err
		}
	}
	var card map[string]any
	switch link.Kind {
	case "issue":
		var issue domain.Issue
		if err := s.chatAuthorizedMutation(dm.authContext(ctx), dm.target.Workspace, dm.identity.UserID, http.MethodGet, "/api/issue-records/"+url.PathEscape(link.Ref), nil, &issue); err != nil {
			return s.chatDMCommandFailure(ctx, dm, err, noAccess)
		}
		card = s.chatDMIssueCard(dm.target.Workspace, issue, dm.zh)
	case "project":
		index := slices.IndexFunc(dm.metadata.Projects, func(project domain.Project) bool {
			return project.SlugID == link.Ref || project.ID == link.Ref || slices.Contains(project.PreviousSlugIDs, link.Ref)
		})
		if index < 0 {
			return s.chatDMNotice(ctx, dm, noAccess)
		}
		card = s.chatDMProjectCard(dm.target.Workspace, dm.metadata.Projects[index], dm.zh)
	}
	_, _, err := s.chatDMSend(ctx, dm.record, dm.command.OpenID, dm.command.MessageID, dm.job.DedupKey+":preview", card)
	return err
}
