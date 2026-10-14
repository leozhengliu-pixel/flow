package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// A workspace created from the app starts with a "Getting started" tour: a
// "Welcome to Flow" issue whose sub-issues each cover one part of Flow with
// the matching product-demo clip (docs/media/<clip>.gif, which the web build
// copies to /tour/<clip>.gif so it ships with every deployment), a few steps
// to try and the shortcuts worth knowing. They are written in the creator's
// language, assigned to the creator and filed in the first team, the way
// The reference app seeds its onboarding issues. Only POST /api/workspaces seeds them,
// inside the workspace's first write: imports and migrations write into
// existing workspaces and never add them, a client creating a workspace to
// import into passes "tutorial": false, and FLOW_WORKSPACE_TUTORIAL=off turns
// them off for the whole server.

const (
	tutorialLocaleEnglish = "en-US"
	tutorialLocaleChinese = "zh-CN"
	// tutorialMediaPath is where the web build serves the tour clips.
	tutorialMediaPath = "/tour/"
)

// tutorialLocale picks the tutorial language: the locale the app sent, else
// the browser's first Accept-Language entry. Flow ships English and
// Simplified Chinese; any Chinese tag gets Chinese, as the web app does.
func tutorialLocale(requested, acceptLanguage string) string {
	candidates := append([]string{requested}, strings.Split(acceptLanguage, ",")...)
	for _, candidate := range candidates {
		tag, _, _ := strings.Cut(candidate, ";")
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || tag == "*" {
			continue
		}
		if strings.HasPrefix(tag, "zh") {
			return tutorialLocaleChinese
		}
		return tutorialLocaleEnglish
	}
	return tutorialLocaleEnglish
}

// tutorialPlaceholder marks a reference to another tutorial issue ({{clip}},
// or {{welcome}} for the parent) in the tutorial text.
var tutorialPlaceholder = regexp.MustCompile(`\{\{([a-z-]+)\}\}`)

// tutorialKeys turns ‹key› into inline code; Go raw strings cannot hold
// backticks.
var tutorialKeys = strings.NewReplacer("‹", "`", "›", "`")

// seedWorkspaceTutorial adds the tutorial issues (and their "Getting started"
// label) to a new workspace's defaults. data.Viewer is the creator.
func seedWorkspaceTutorial(data *domain.Bootstrap, locale string) error {
	if len(data.Teams) == 0 || data.Viewer.ID == "" {
		return nil
	}
	chinese := locale == tutorialLocaleChinese
	text := func(english, chineseText string) string {
		if chinese {
			return chineseText
		}
		return english
	}
	now := time.Now().UTC()
	team := data.Teams[0]
	creator := data.Viewer
	todo := stateForTeamByType(data, team.ID, "unstarted")
	backlog := stateForTeamByType(data, team.ID, "backlog")
	if todo == nil || backlog == nil {
		return fmt.Errorf("team %s has no Todo or Backlog status", team.Key)
	}
	label := domain.IssueLabel{
		ID: fmt.Sprintf("label_%d_getting_started", now.UnixNano()), Name: text("Getting started", "新手入门"), Color: "#4CB782",
		Description: text("Flow's tutorial issues", "Flow 的教程事项"), Scope: "Workspace", ResourceType: "issue", CreatedAt: now,
	}
	data.Labels = append(data.Labels, label)

	entries := tutorialEntries()
	number := max(data.NextIssueNumber, nextIssueNumber(data.Issues))
	issues := make([]domain.Issue, 0, len(entries)+1)
	byName := map[string]int{}
	newIssue := func(name, title string, state domain.WorkflowState) {
		created := now.Add(-time.Duration(len(issues)) * time.Second) // the welcome issue is the newest, the tour runs in order below it
		issue := domain.Issue{
			ID: fmt.Sprintf("issue_%d", number), Version: 1, Identifier: fmt.Sprintf("%s-%d", team.Key, number), Number: number,
			Title: title, PriorityLabel: priorityLabel(0), SortOrder: float64(number), CreatedAt: created, UpdatedAt: created,
			Team: team, State: state, Assignee: &creator, Creator: creator, Labels: []domain.IssueLabel{label},
			SubscriberIDs: []string{creator.ID}, Reactions: map[string][]string{}, SubIssueIDs: []string{},
			Relations: []domain.IssueRelation{}, Attachments: []domain.Attachment{}, SuggestedLabelIDs: []string{},
		}
		byName[name] = len(issues)
		issues = append(issues, issue)
		number++
	}
	newIssue("welcome", text("Welcome to Flow", "欢迎使用 Flow"), *todo)
	for _, entry := range entries {
		state := *backlog
		if entry.section == tutorialSectionCore {
			state = *todo
		}
		newIssue(entry.clip, text(entry.title, entry.titleZH), state)
	}
	welcome := &issues[0]
	for index := 1; index < len(issues); index++ {
		issues[index].ParentID = stringPointer(welcome.ID)
		welcome.SubIssueIDs = append(welcome.SubIssueIDs, issues[index].ID)
	}

	resolver := newMentionResolver(data, issues)
	var missing []string
	references := func(body string) string {
		return tutorialPlaceholder.ReplaceAllStringFunc(body, func(match string) string {
			name := tutorialPlaceholder.FindStringSubmatch(match)[1]
			index, ok := byName[name]
			if !ok {
				missing = append(missing, name)
				return match
			}
			// The editor stores an issue mention as a link to the issue in
			// Markdown; the document below turns it back into a mention.
			attrs := resolver.issueAttrs(issues[index])
			return "[" + attrs.Label + "](" + attrs.Href + ")"
		})
	}
	describe := func(issue *domain.Issue, clip, body string) {
		body = references(tutorialKeys.Replace(strings.TrimSpace(body)))
		document := resolver.markdownDocument(body)
		markdown := body
		if clip != "" {
			src := tutorialMediaPath + clip + ".gif"
			markdown = "![" + issue.Title + "](" + src + ")\n\n" + body
			content, _ := document["content"].([]any)
			image := map[string]any{"type": "image", "attrs": map[string]any{"src": src, "alt": issue.Title, "title": nil, "width": nil, "height": nil}}
			document["content"] = append([]any{image}, content...)
		}
		issue.Description = markdown
		issue.DocumentContent = &domain.DocumentContent{ID: "document_content_" + newCollaborationID(), Version: 1, Content: markdown, ContentData: document, UpdatedAt: now}
	}
	describe(welcome, "", tutorialWelcome(entries, chinese))
	for _, entry := range entries {
		describe(&issues[byName[entry.clip]], entry.clip, text(entry.body, entry.bodyZH))
	}
	if len(missing) > 0 {
		return fmt.Errorf("tutorial text references unknown issues %v", missing)
	}

	if data.Activities == nil {
		data.Activities = map[string][]domain.ActivityEvent{}
	}
	for index, issue := range issues {
		data.Activities[issue.ID] = append(data.Activities[issue.ID], domain.ActivityEvent{
			ID: fmt.Sprintf("activity_%d_%d", now.UnixNano(), index), Type: "issue.created", CreatedAt: issue.CreatedAt, Actor: creator,
			Metadata: map[string]string{"stateId": issue.State.ID, "state": issue.State.Name},
		})
	}
	data.Issues = append(issues, data.Issues...)
	data.NextIssueNumber = number
	return nil
}

// tutorialWelcome is the parent issue's text: what the tour is and its
// sub-issues grouped like the feature tour.
func tutorialWelcome(entries []tutorialEntry, chinese bool) string {
	var builder strings.Builder
	if chinese {
		builder.WriteString(tutorialWelcomeIntroZH)
	} else {
		builder.WriteString(tutorialWelcomeIntro)
	}
	for _, section := range tutorialSections {
		builder.WriteString("\n\n### ")
		if chinese {
			builder.WriteString(section.titleZH)
		} else {
			builder.WriteString(section.title)
		}
		builder.WriteString("\n\n")
		for _, entry := range entries {
			if entry.section == section.id {
				builder.WriteString("- {{" + entry.clip + "}}\n")
			}
		}
	}
	builder.WriteString("\n")
	if chinese {
		builder.WriteString(tutorialWelcomeTipZH)
	} else {
		builder.WriteString(tutorialWelcomeTip)
	}
	return builder.String()
}
