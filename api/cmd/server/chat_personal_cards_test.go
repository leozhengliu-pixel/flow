package main

import (
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A personal notification is written from the notification in the member's
// language: the actor by display name, the issue's status as its team names
// it and the time in the member's zone; the email summary stays English.
func TestChatPersonalSummaryIsInTheMembersLanguage(t *testing.T) {
	actor := domain.User{ID: "user_9c1", Name: "user_9c1", DisplayName: "王小明"}
	issue := domain.Issue{ID: "issue_1", Identifier: "ENG-7", Title: "支付回调失败", State: domain.WorkflowState{ID: "state_review", Name: "待评审"}}
	data := &domain.Bootstrap{Issues: []domain.Issue{issue}, Users: []domain.User{actor}}
	at := time.Date(2026, 11, 4, 8, 30, 0, 0, time.UTC)
	root := "https://flow.example/acme"
	for _, tc := range []struct {
		notification domain.Notification
		language     string
		want         []string
	}{
		{domain.Notification{Type: "assignment", IssueID: issue.ID, Actor: actor, UpdatedAt: at}, chatLanguageChinese, []string{"王小明 将此事项指派给了你", "2026年11月4日 16:30（UTC+08:00）"}},
		{domain.Notification{Type: "activity", Category: "statusChanges", IssueID: issue.ID, Actor: actor, UpdatedAt: at}, chatLanguageChinese, []string{"王小明 将状态改为「待评审」"}},
		{domain.Notification{Type: "comment", IssueID: issue.ID, Actor: actor, UpdatedAt: at}, chatLanguageEnglish, []string{"王小明 commented on the issue", "Nov 4, 2026, 4:30 PM (UTC+08:00)"}},
		{domain.Notification{Type: agentReplyNotificationType, IssueID: issue.ID, Actor: domain.User{ID: "agent", Name: "Flow"}, Payload: &domain.NotificationPayload{AgentActivity: "finished", Excerpt: "已修复"}, UpdatedAt: at}, chatLanguageChinese, []string{"Flow 已完成：已修复"}},
		{domain.Notification{Type: notificationDocumentNewComment, Title: "发布手册", Text: "王小明 commented on the document", Actor: actor, UpdatedAt: at}, chatLanguageChinese, []string{"王小明 评论了文档"}},
		{domain.Notification{Type: "pulseSummary", Title: "Weekly summary", Text: "", UpdatedAt: at}, chatLanguageChinese, []string{"请打开 Flow 查看。"}},
	} {
		item, err := chatPersonalSummary(tc.language, "Asia/Shanghai", data, root, tc.notification)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(item.Line, want) {
				t.Errorf("%s/%s: line %q lacks %q", tc.notification.Type, tc.language, item.Line, want)
			}
		}
		if strings.Contains(item.Line, "user_9c1") {
			t.Errorf("line names the account id: %q", item.Line)
		}
		if tc.language == chatLanguageChinese && strings.Contains(item.Line, "commented") {
			t.Errorf("Chinese line keeps English text: %q", item.Line)
		}
	}
	// The email summary is unchanged.
	email, _ := notificationSummary(data, root, domain.Notification{Type: "assignment", IssueID: issue.ID, Actor: actor})
	if !strings.HasSuffix(email.Line, " assigned the issue to you") {
		t.Fatalf("email summary changed: %q", email.Line)
	}
}
