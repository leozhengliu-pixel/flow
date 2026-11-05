package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Project and initiative update cards. A group that hears updates gets the
// update itself: its health (the header colour and label), its author, when
// it was posted (in the team's time zone) and its body as card markdown, cut
// at a block boundary with a link to the update when it is long. A project
// update's card is recorded as a chat thread of the update
// (chatThreadResourceProjectUpdate), so an edit redraws the card and a
// deletion replaces it with a notice.

const (
	chatThreadResourceProjectUpdate = "project_update"
	// chatUpdateBodyLimit bounds the update text shown on a card, in runes.
	chatUpdateBodyLimit = 1800
)

// chatPulseUpdate is what an update card shows of a project or an
// initiative update.
type chatPulseUpdate struct {
	ID        string
	Body      string
	BodyData  map[string]any
	Health    string
	User      domain.User
	CreatedAt time.Time
	EditedAt  *time.Time
}

type chatUpdateCardInput struct {
	Language string
	// Heading is "project_update" or "initiative_update".
	Heading string
	// Name is the project's or initiative's name.
	Name string
	// Link opens the update itself.
	Link      string
	Origin    string
	Zone      string
	Directory []domain.User
	Update    chatPulseUpdate
}

func chatProjectPulseUpdate(update domain.ProjectUpdate) chatPulseUpdate {
	return chatPulseUpdate{ID: update.ID, Body: update.Body, BodyData: update.BodyData, Health: update.Health, User: update.User, CreatedAt: update.CreatedAt, EditedAt: update.EditedAt}
}

func chatInitiativePulseUpdate(update domain.InitiativeUpdate) chatPulseUpdate {
	return chatPulseUpdate{ID: update.ID, Body: update.Body, BodyData: update.BodyData, Health: update.Health, User: update.User, CreatedAt: update.CreatedAt, EditedAt: update.EditedAt}
}

// chatUpdateCard draws an update card.
func chatUpdateCard(input chatUpdateCardInput) map[string]any {
	language, update := input.Language, input.Update
	health, template := chatHealthText(language, update.Health)
	meta := []string{}
	if health != "" {
		meta = append(meta, "**"+chatDMMarkdownEscape(health)+"**")
	}
	if author := chatPersonLabel(&update.User, input.Directory); author != "" {
		meta = append(meta, chatDMMarkdownEscape(author))
	}
	if !update.CreatedAt.IsZero() {
		meta = append(meta, chatFormatTime(language, update.CreatedAt, input.Zone))
	}
	if update.EditedAt != nil {
		meta = append(meta, chatDMMarkdownEscape(chatT(language, "update_edited")))
	}
	elements := []map[string]any{}
	if len(meta) > 0 {
		elements = append(elements, chatMarkdownElement(strings.Join(meta, " · ")))
	}
	body, truncated := chatCardMarkdown(chatUpdateDocument(update), chatUpdateBodyLimit, chatMarkdownContext{origin: input.Origin, directory: input.Directory})
	if truncated && input.Link != "" {
		body += "\n\n" + chatDMMarkdownLink(chatT(language, "read_more"), input.Link)
	}
	if strings.TrimSpace(body) != "" {
		elements = append(elements, chatMarkdownElement(body))
	}
	if input.Link != "" {
		elements = append(elements, map[string]any{"tag": "action", "actions": []map[string]any{{"tag": "button", "text": chatPlain(chatT(language, "open_update")), "url": input.Link, "type": "default"}}})
	}
	card := chatCard(chatT(language, input.Heading)+" · "+input.Name, elements...)
	card["header"].(map[string]any)["template"] = template
	return card
}

// chatUpdateDeletedCard replaces the card of a deleted update.
func chatUpdateDeletedCard(language, heading, name string) map[string]any {
	card := chatCard(chatT(language, heading)+" · "+name, chatCardText(chatT(language, "update_deleted")))
	card["header"].(map[string]any)["template"] = "grey"
	return card
}

func chatMarkdownElement(content string) map[string]any {
	return map[string]any{"tag": "markdown", "content": content}
}

// chatUpdateDocument is the update's rich-text document: the editor's, or
// the Markdown body read into one.
func chatUpdateDocument(update chatPulseUpdate) map[string]any {
	if content, _ := update.BodyData["content"].([]any); len(content) > 0 {
		return update.BodyData
	}
	return mcpMarkdownDocument(update.Body)
}

type chatMarkdownContext struct {
	origin    string
	directory []domain.User
}

// chatCardMarkdown writes a rich-text document as the platform's card
// markdown: paragraphs, headings (as bold lines), bullet, ordered and task
// lists, quotes, code, bold/italic/strike/code marks and links; mentions are
// written as names. Text is escaped so it is never read as markup. A
// document longer than limit runes is cut after the last whole block that
// fits; truncated reports it.
func chatCardMarkdown(document map[string]any, limit int, context chatMarkdownContext) (string, bool) {
	blocks, _ := document["content"].([]any)
	parts := []string{}
	used := 0
	for _, raw := range blocks {
		node, _ := raw.(map[string]any)
		text := strings.TrimRight(context.block(node, ""), "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		size := utf8.RuneCountInString(text)
		if used+size > limit {
			if len(parts) == 0 {
				// One block longer than the card: its plain text, cut.
				plain := []rune(proseMirrorPlainText(map[string]any{"type": "doc", "content": []any{node}}))
				parts = append(parts, chatDMMarkdownEscape(string(plain[:min(len(plain), limit)]))+"…")
			}
			return strings.Join(parts, "\n\n"), true
		}
		parts = append(parts, text)
		used += size + 2
	}
	return strings.Join(parts, "\n\n"), false
}

func (c chatMarkdownContext) block(node map[string]any, indent string) string {
	kind, _ := node["type"].(string)
	content, _ := node["content"].([]any)
	switch kind {
	case "paragraph":
		return c.inline(content)
	case "heading":
		if text := c.inline(content); strings.TrimSpace(text) != "" {
			return "**" + text + "**"
		}
		return ""
	case "bulletList", "orderedList", "taskList":
		start := 1
		if attrs, _ := node["attrs"].(map[string]any); attrs != nil {
			if value, ok := attrs["start"].(float64); ok && value >= 1 {
				start = int(value)
			} else if value, ok := attrs["start"].(int); ok && value >= 1 {
				start = value
			}
		}
		lines := []string{}
		for index, raw := range content {
			item, _ := raw.(map[string]any)
			marker := "- "
			switch kind {
			case "orderedList":
				marker = strconv.Itoa(start+index) + ". "
			case "taskList":
				marker = "- ☐ "
				if attrs, _ := item["attrs"].(map[string]any); attrs != nil && attrs["checked"] == true {
					marker = "- ☑ "
				}
			}
			children, _ := item["content"].([]any)
			for childIndex, child := range children {
				childNode, _ := child.(map[string]any)
				childKind, _ := childNode["type"].(string)
				if childKind == "bulletList" || childKind == "orderedList" || childKind == "taskList" {
					lines = append(lines, c.block(childNode, indent+"    "))
					continue
				}
				text := strings.ReplaceAll(c.block(childNode, indent), "\n", "\n"+indent+"    ")
				if childIndex == 0 {
					lines = append(lines, indent+marker+text)
				} else {
					lines = append(lines, indent+"    "+text)
				}
			}
			if len(children) == 0 {
				lines = append(lines, indent+marker)
			}
		}
		return strings.Join(lines, "\n")
	case "blockquote":
		inner := []string{}
		for _, child := range content {
			childNode, _ := child.(map[string]any)
			inner = append(inner, c.block(childNode, ""))
		}
		return "> " + strings.ReplaceAll(strings.Join(inner, "\n"), "\n", "\n> ")
	case "codeBlock", "mathBlock":
		return "```\n" + strings.ReplaceAll(proseMirrorPlainText(node), "```", "ˋˋˋ") + "\n```"
	case "horizontalRule":
		return "———"
	case "table":
		rows := []string{}
		for _, raw := range content {
			row, _ := raw.(map[string]any)
			cells, _ := row["content"].([]any)
			values := []string{}
			for _, cell := range cells {
				values = append(values, chatDMMarkdownEscape(strings.TrimSpace(proseMirrorPlainText(cell.(map[string]any)))))
			}
			rows = append(rows, strings.Join(values, " ｜ "))
		}
		return strings.Join(rows, "\n")
	case "image", "video", "file":
		attrs, _ := node["attrs"].(map[string]any)
		src, _ := attrs["src"].(string)
		label, _ := attrs["alt"].(string)
		if label == "" {
			label, _ = attrs["name"].(string)
		}
		if label == "" {
			label = kind
		}
		if link := c.link(src); link != "" {
			return chatDMMarkdownLink(label, link)
		}
		return chatDMMarkdownEscape(label)
	}
	if len(content) > 0 {
		if _, isText := content[0].(map[string]any)["text"]; isText {
			return c.inline(content)
		}
		inner := []string{}
		for _, child := range content {
			childNode, _ := child.(map[string]any)
			inner = append(inner, c.block(childNode, indent))
		}
		return strings.Join(inner, "\n")
	}
	return ""
}

func (c chatMarkdownContext) inline(nodes []any) string {
	var builder strings.Builder
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		switch node["type"] {
		case "text":
			text, _ := node["text"].(string)
			builder.WriteString(c.marked(text, node["marks"]))
		case "hardBreak":
			builder.WriteString("\n")
		case "mention":
			builder.WriteString(c.mention(node))
		default:
			if children, _ := node["content"].([]any); len(children) > 0 {
				builder.WriteString(c.inline(children))
			} else if text := proseMirrorPlainText(node); text != "" {
				builder.WriteString(chatDMMarkdownEscape(text))
			}
		}
	}
	return builder.String()
}

func (c chatMarkdownContext) marked(text string, rawMarks any) string {
	marks, _ := rawMarks.([]any)
	code := slices.ContainsFunc(marks, func(mark any) bool { return chatMarkType(mark) == "code" })
	result := ""
	if code {
		result = "`" + strings.ReplaceAll(text, "`", "ˋ") + "`"
	} else {
		result = chatDMMarkdownEscape(text)
	}
	if strings.TrimSpace(text) == "" {
		return result
	}
	for _, mark := range marks {
		switch chatMarkType(mark) {
		case "bold":
			result = "**" + result + "**"
		case "italic":
			result = "*" + result + "*"
		case "strike":
			result = "~~" + result + "~~"
		}
	}
	for _, mark := range marks {
		if chatMarkType(mark) == "link" {
			attrs, _ := mark.(map[string]any)["attrs"].(map[string]any)
			href, _ := attrs["href"].(string)
			if link := c.link(href); link != "" {
				result = "[" + result + "](" + strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(link) + ")"
			}
		}
	}
	return result
}

func chatMarkType(mark any) string {
	value, _ := mark.(map[string]any)["type"].(string)
	return value
}

// mention writes a mention as the name it shows: a person's current display
// name (chatPersonLabel), otherwise the mention's label.
func (c chatMarkdownContext) mention(node map[string]any) string {
	attrs, _ := node["attrs"].(map[string]any)
	id, _ := attrs["id"].(string)
	label, _ := attrs["label"].(string)
	kind, _ := attrs["mentionType"].(string)
	if kind == "" || kind == "user" {
		if index := slices.IndexFunc(c.directory, func(user domain.User) bool { return user.ID == id }); index >= 0 {
			label = chatPersonLabel(&c.directory[index], nil)
		}
		if label == "" {
			return ""
		}
		return "@" + chatDMMarkdownEscape(strings.TrimPrefix(label, "@"))
	}
	if label == "" {
		label, _ = attrs["title"].(string)
	}
	href, _ := attrs["href"].(string)
	if link := c.link(href); link != "" && label != "" {
		return chatDMMarkdownLink(label, link)
	}
	return chatDMMarkdownEscape(label)
}

// link is an absolute web link for the card, or "" for anything else (a
// script, an app scheme).
func (c chatMarkdownContext) link(href string) string {
	href = strings.TrimSpace(href)
	if strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") && c.origin != "" {
		return c.origin + href
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return ""
	}
	return href
}

func (s *server) chatProjectUpdateLink(record domain.ChatIntegrationRecord, project domain.Project, updateID string) string {
	slug := firstNonEmpty(project.SlugID, project.ID)
	link := fmt.Sprintf("%s/%s/project/%s/overview", s.chatAppOrigin(), url.PathEscape(record.WorkspaceKey), url.PathEscape(slug))
	if updateID != "" {
		link += "#update-" + url.PathEscape(updateID)
	}
	return link
}

// chatSendProjectUpdate posts, redraws or retires the card of a project
// update in the binding's group, by the event the job runs for: a new
// update posts its card; an edit redraws the card it posted; a deletion
// replaces that card with a notice. A job queued before jobs named their
// update shows the project's update posted at the event.
func (s *server) chatSendProjectUpdate(ctx context.Context, record domain.ChatIntegrationRecord, binding domain.ChatBinding, job domain.ChatJob, payload chatNotificationJob) error {
	data, ok := s.store.WorkspaceMetadataFields(record.WorkspaceKey, "projects", "teams", "teamSettings", "workspaceSettings", "users")
	if !ok {
		return chatInvalid("project metadata unavailable")
	}
	if !chatProjectMatchesBinding(data, binding, payload.ProjectID) {
		return nil
	}
	index := slices.IndexFunc(data.Projects, func(project domain.Project) bool { return project.ID == payload.ProjectID })
	if index < 0 {
		return nil
	}
	project := data.Projects[index]
	language := chatGroupLanguage(record, binding)
	var update *domain.ProjectUpdate
	if feed, ok := s.store.PulseFeed(record.WorkspaceKey); ok {
		for _, item := range feed.ProjectUpdates[project.ID] {
			if payload.UpdateID != "" && item.ID != payload.UpdateID || payload.UpdateID == "" && !payload.EventAt.IsZero() && item.CreatedAt.After(payload.EventAt) {
				continue
			}
			update = &item
			break
		}
	}
	updateID := payload.UpdateID
	if updateID == "" && update != nil {
		updateID = update.ID
	}
	var thread *domain.ChatThread
	if updateID != "" {
		found, err := s.store.ChatThread(ctx, record.WorkspaceKey, record.ConnectionID, binding.ChatID, chatThreadResourceProjectUpdate, updateID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			thread = &found
		}
	}
	switch payload.Event {
	case "project.update_deleted":
		if thread == nil {
			return nil
		}
		err := s.chatUpdateCard(ctx, record, thread.RootMessageID, chatUpdateDeletedCard(language, "project_update", project.Name))
		if err != nil && !chatMessageGone(err) {
			return err
		}
		return s.store.DeleteChatThread(ctx, record.WorkspaceKey, record.ConnectionID, thread.ID)
	case "project.update_updated":
		if thread == nil || update == nil {
			return nil
		}
	default:
		if update == nil || thread != nil {
			return nil
		}
	}
	card := chatUpdateCard(chatUpdateCardInput{Language: language, Heading: "project_update", Name: project.Name, Link: s.chatProjectUpdateLink(record, project, update.ID), Origin: s.chatAppOrigin(), Zone: teamSettings(&data, binding.TeamID).Timezone, Directory: data.Users, Update: chatProjectPulseUpdate(*update)})
	if thread != nil {
		err := s.chatUpdateCard(ctx, record, thread.RootMessageID, card)
		if chatMessageGone(err) {
			return s.store.DeleteChatThread(ctx, record.WorkspaceKey, record.ConnectionID, thread.ID)
		}
		return err
	}
	content, _ := json.Marshal(card)
	result, err := s.chatPostMessage(ctx, record, "/open-apis/im/v1/messages?receive_id_type=chat_id", map[string]any{"receive_id": binding.ChatID, "msg_type": "interactive", "content": string(content), "uuid": chatKey(job.DedupKey, "project")[:32]})
	if err != nil {
		return err
	}
	_, err = s.store.PutChatThread(ctx, record.WorkspaceKey, domain.ChatThread{ConnectionID: record.ConnectionID, ChatID: binding.ChatID, ResourceType: chatThreadResourceProjectUpdate, ResourceID: update.ID, RootMessageID: result.Data.MessageID, PlatformThreadID: result.Data.ThreadID, BindingID: binding.ID})
	return err
}

// chatInitiativeUpdateCard draws the card of the initiative's update the
// job names (or, for a job that names none, the one posted at the event).
func (s *server) chatInitiativeUpdateCard(language string, record domain.ChatIntegrationRecord, data domain.Bootstrap, initiative domain.Initiative, link string, payload chatNotificationJob) map[string]any {
	update := chatPulseUpdate{Health: initiative.Health}
	if feed, ok := s.store.PulseFeed(record.WorkspaceKey); ok {
		for _, item := range feed.InitiativeUpdates[initiative.ID] {
			if payload.UpdateID != "" && item.ID != payload.UpdateID || payload.UpdateID == "" && !payload.EventAt.IsZero() && item.CreatedAt.After(payload.EventAt) {
				continue
			}
			update = chatInitiativePulseUpdate(item)
			break
		}
		if len(data.Users) == 0 {
			data.Users = feed.Users
		}
	}
	if update.ID != "" {
		link += "#update-" + url.PathEscape(update.ID)
	}
	return chatUpdateCard(chatUpdateCardInput{Language: language, Heading: "initiative_update", Name: initiative.Name, Link: link, Origin: s.chatAppOrigin(), Zone: teamSettings(&data, initiative.LeadTeamID).Timezone, Directory: data.Users, Update: update})
}
