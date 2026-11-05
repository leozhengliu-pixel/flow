package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

type chatPreviewCase struct {
	name, link, chatID string
}

// chatPreviewCases builds one workspace with every kind of Flow link and
// returns the links: a member's own context unless chatID names a group.
func chatPreviewCases(t *testing.T, f *chatWorkflowFixture) []chatPreviewCase {
	t.Helper()
	f.s.allowedOrigin = "https://flow.example.test"
	data := f.s.store.Bootstrap()
	issue := data.Issues[0]
	team := issue.Team
	base := "https://flow.example.test/" + f.record.WorkspaceKey
	at := time.Date(2026, 11, 2, 9, 15, 0, 0, time.UTC)
	due := "2026-11-20"
	target := "2026-12-15"
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.preview_links", issue.ID, nil, func(data *domain.Bootstrap) error {
		for index := range data.Users {
			if data.Users[index].ID == data.Viewer.ID {
				data.Users[index].DisplayName = "王小明"
			}
		}
		viewer := data.Viewer
		viewer.DisplayName = "王小明"
		project := &data.Projects[0]
		project.Name, project.SlugID, project.Health, project.Progress, project.TargetDate, project.Lead = "支付改版", "payments-0123456789ab", "atRisk", 0.42, &target, &viewer
		project.Summary = "Rebuild checkout payments"
		if !containsString(project.TeamIDs, team.ID) {
			project.TeamIDs = append(project.TeamIDs, team.ID)
		}
		data.ProjectUpdates[project.ID] = append([]domain.ProjectUpdate{{ID: "project_update_preview", ProjectID: project.ID, Body: "本周完成 **支付** 接入\n\n- 回调重试\n- 对账报表", Health: "atRisk", CreatedAt: at, User: viewer, Comments: []domain.Comment{}, Reactions: map[string][]string{}, Attachments: []domain.Attachment{}}}, data.ProjectUpdates[project.ID]...)
		for index := range data.Issues {
			if data.Issues[index].ID != issue.ID {
				continue
			}
			item := &data.Issues[index]
			item.Title, item.Priority, item.DueDate, item.Assignee = "支付回调偶发失败，需要重试机制与告警", 1, &due, &viewer
			item.Project = &domain.ProjectSummary{ID: project.ID, Name: project.Name}
			item.Labels = []domain.IssueLabel{{ID: "label_bug", Name: "Bug", Color: "#eb5757"}, {ID: "label_pay", Name: "支付", Color: "#4ea7fc"}, {ID: "label_p0", Name: "P0", Color: "#f2c94c"}, {ID: "label_extra", Name: "Backend", Color: "#bb87fc"}}
		}
		data.Documents = append(data.Documents,
			domain.Document{ID: "doc_preview", SlugID: "spec-0123456789ab", Title: "支付回调设计", Content: "# 背景\n\n回调失败时需要**重试**并记录。\n\n第二段内容。", Creator: viewer, TeamIDs: []string{team.ID}, ProjectIDs: []string{project.ID}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, CreatedAt: at, UpdatedAt: at},
			domain.Document{ID: "doc_restricted", SlugID: "secret-0123456789ab", Title: "Restricted plan", Content: "private", Creator: viewer, TeamIDs: []string{team.ID}, ProjectIDs: []string{}, SubscriberIDs: []string{}, Revisions: []domain.DocumentRevision{}, Permissions: []domain.DocumentPermission{{ID: "perm_1", DocumentID: "doc_restricted", SubjectType: "user", SubjectID: viewer.ID, Role: "editor", CreatedAt: at, UpdatedAt: at}}, CreatedAt: at, UpdatedAt: at},
		)
		data.Initiatives = append(data.Initiatives, domain.Initiative{ID: "initiative_preview", SlugID: "growth-0123456789ab", Name: "增长计划", Summary: "Grow the base", Status: "active", Health: "onTrack", Owner: &viewer, LeadTeamID: team.ID})
		data.Cycles = append(data.Cycles, domain.Cycle{ID: "cycle_preview", Number: 42, Name: "", TeamID: team.ID, StartsAt: at, EndsAt: at.Add(14 * 24 * time.Hour)})
		data.SavedViews = append(data.SavedViews, domain.SavedView{ID: "view_preview", SlugID: "payments-bugs-0123456789ab", Name: "支付缺陷", Description: "Open payment bugs", TeamID: team.ID, SharedAt: &at})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	comment := f.addComment(t, issue.ID, "已复现：回调超时 30 秒后失败")
	slug := "/issue/" + issue.Identifier + "/fix"
	return []chatPreviewCase{
		{"issue", base + slug, ""},
		{"issue_group", base + slug, f.binding.ChatID},
		{"comment", base + slug + "#comment-" + comment.ID, ""},
		{"project", base + "/project/payments-0123456789ab/overview", ""},
		{"project_update", base + "/project/payments-0123456789ab/overview#update-project_update_preview", ""},
		{"initiative", base + "/initiative/growth-0123456789ab/overview", ""},
		{"document", base + "/document/spec-0123456789ab", ""},
		{"cycle", base + "/team/" + team.Key + "/cycle/42", ""},
		{"view", base + "/view/payments-bugs-0123456789ab", ""},
		{"team", base + "/team/" + team.Key + "/active", ""},
		{"workspace", base, ""},
		{"unknown", base + "/settings/account", ""},
		{"missing", base + "/issue/MISSING-1", ""},
		{"forbidden", base + "/document/secret-0123456789ab", f.binding.ChatID},
	}
}

func (f *chatWorkflowFixture) setViewerLanguage(t *testing.T, language, zone string) {
	t.Helper()
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "test.preview_language", "", nil, func(data *domain.Bootstrap) error {
		settings := data.UserSettings[f.identity.UserID]
		settings.UserID, settings.Language, settings.Timezone = f.identity.UserID, language, zone
		data.UserSettings[f.identity.UserID] = settings
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// chatPreviewVolatile matches the parts of a preview that change from run
// to run: generated IDs and the times of records the fixture writes now.
var chatPreviewVolatile = regexp.MustCompile(`comment_[0-9]+_[a-z0-9]+|\d{4}年\d{1,2}月\d{1,2}日 \d{2}:\d{2}（UTC[+-]\d{2}:\d{2}）|[A-Z][a-z]{2} \d{1,2}, \d{4}, \d{1,2}:\d{2} [AP]M \(UTC[+-]\d{2}:\d{2}\)`)

// Link previews per link kind and viewer language, against golden files
// (FLOW_UPDATE_GOLDEN=1 rewrites them; FLOW_UNFURL_CAPTURE=<dir> also
// writes the raw answers there).
func TestChatLinkPreviewGolden(t *testing.T) {
	f := newChatWorkflowFixture(t)
	cases := chatPreviewCases(t, f)
	capture := os.Getenv("FLOW_UNFURL_CAPTURE")
	for _, language := range []string{"zh-CN", "en-US"} {
		f.setViewerLanguage(t, language, "Asia/Shanghai")
		for _, tc := range cases {
			result := f.preview(t, f.identity.OpenID, tc.link, tc.chatID)
			raw, _ := json.MarshalIndent(result, "", "  ")
			if capture != "" {
				if err := os.WriteFile(filepath.Join(capture, tc.name+"_"+language+".json"), raw, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			normalized := chatPreviewVolatile.ReplaceAll(raw, []byte("<volatile>"))
			golden := filepath.Join("testdata", "chat_previews", tc.name+"_"+language+".json")
			if os.Getenv("FLOW_UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, append(normalized, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%s: %v (run with FLOW_UPDATE_GOLDEN=1)", golden, err)
			}
			if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(normalized)) {
				t.Errorf("%s %s preview differs from %s:\n%s", tc.name, language, golden, normalized)
			}
		}
	}
}

// A previewed issue that changes has its previews requested again.
func TestChatLinkPreviewRefreshOnChange(t *testing.T) {
	f := newChatWorkflowFixture(t)
	f.s.allowedOrigin = "https://flow.example.test"
	issue := f.s.store.Bootstrap().Issues[0]
	refreshes := []map[string]any{}
	inner := http.DefaultTransport
	http.DefaultTransport = chatWorkflowTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/im/v2/url_previews/batch_update") {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			refreshes = append(refreshes, body)
			return chatTestResponse(200, `{"code":0}`), nil
		}
		return inner.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = inner })
	f.followEvents(t)
	envelope := map[string]any{
		"schema": "2.0",
		"header": map[string]string{"app_id": f.record.AppID, "tenant_key": f.record.TenantKey, "token": f.credentials.VerificationToken, "event_id": "evt_preview_token", "event_type": "url.preview.get"},
		"event": map[string]any{
			"operator": map[string]string{"tenant_key": f.record.TenantKey, "open_id": f.identity.OpenID},
			"context":  map[string]string{"url": "https://flow.example.test/" + f.record.WorkspaceKey + "/issue/" + issue.Identifier, "preview_token": "pt_issue_1", "open_chat_id": ""},
		},
	}
	if w := f.callback(t, envelope, time.Now(), true); w.Code != http.StatusOK {
		t.Fatalf("preview: %d", w.Code)
	}
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "issue.updated", issue.ID, nil, func(data *domain.Bootstrap) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.syncEvents(t)
	if len(refreshes) != 1 || !strings.Contains(chatPreviewText(refreshes[0]), "pt_issue_1") || !strings.Contains(chatPreviewText(refreshes[0]), f.identity.OpenID) {
		t.Fatalf("preview not refreshed: %v", refreshes)
	}
	// A change to an item nobody previewed asks for nothing.
	other := f.s.store.Bootstrap().Issues[1]
	if err := f.s.store.MutateWorkspace(t.Context(), f.record.WorkspaceKey, "issue.updated", other.ID, nil, func(data *domain.Bootstrap) error { return nil }); err != nil {
		t.Fatal(err)
	}
	f.syncEvents(t)
	if len(refreshes) != 1 {
		t.Fatalf("unpreviewed item refreshed: %v", refreshes)
	}
}

// A preview never shows the other language, an account ID, or anything of
// an item the viewer or group may not see; long titles are cut.
func TestChatLinkPreviewLanguageAndLeaks(t *testing.T) {
	f := newChatWorkflowFixture(t)
	cases := chatPreviewCases(t, f)
	byName := map[string]chatPreviewCase{}
	for _, tc := range cases {
		byName[tc.name] = tc
	}
	f.setViewerLanguage(t, "zh-CN", "Asia/Shanghai")
	for _, tc := range cases {
		text := chatPreviewText(f.preview(t, f.identity.OpenID, tc.link, tc.chatID))
		for _, english := range []string{"Open in Flow", "Status", "Priority", "Assignee", "Unassigned"} {
			if strings.Contains(text, english) {
				t.Errorf("%s: Chinese preview shows %q: %s", tc.name, english, text)
			}
		}
		if strings.Contains(text, f.identity.UserID) {
			t.Errorf("%s: preview names an account id: %s", tc.name, text)
		}
	}
	forbidden := chatPreviewText(f.preview(t, f.identity.OpenID, byName["forbidden"].link, byName["forbidden"].chatID))
	if strings.Contains(forbidden, "Restricted plan") || strings.Contains(forbidden, `"card"`) || !strings.Contains(forbidden, chatT(chatLanguageChinese, "preview_neutral")) {
		t.Fatalf("forbidden preview: %s", forbidden)
	}
	long := chatLinkPreviewTitle(strings.Repeat("很长的标题", 60))
	if runes := []rune(long); len(runes) > chatPreviewTitleLimit || !strings.HasSuffix(long, "…") {
		t.Fatalf("long title not cut: %d runes", len(runes))
	}
}
