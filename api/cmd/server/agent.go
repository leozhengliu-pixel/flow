package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/idgen"
	"flow/api/internal/store"
)

type agentChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agentChatInput struct {
	Message  string             `json:"message"`
	IssueIDs []string           `json:"issueIds"`
	History  []agentChatMessage `json:"history"`
}

type agentSessionInput struct {
	Message     string   `json:"message"`
	IssueIDs    []string `json:"issueIds"`
	ProjectIDs  []string `json:"projectIds"`
	DocumentIDs []string `json:"documentIds"`
	// InitiativeIDs puts initiatives in context (the initiative page a chat opens on, or @-mentions).
	InitiativeIDs []string              `json:"initiativeIds"`
	UserIDs       []string              `json:"userIds"`
	SkillIDs      []string              `json:"skillIds"`
	LoopIDs       []string              `json:"loopIds"`
	Location      string                `json:"location"`
	Mentions      []domain.AgentMention `json:"mentions"`
	// Attachments are files sent with the first message (see agent_attachments.go).
	Attachments []loopRunReplyAttachmentInput `json:"attachments"`
}

// agentMessageInput is a follow-up message plus anything @-mentioned in it.
type agentMessageInput struct {
	Message       string                `json:"message"`
	IssueIDs      []string              `json:"issueIds"`
	ProjectIDs    []string              `json:"projectIds"`
	DocumentIDs   []string              `json:"documentIds"`
	InitiativeIDs []string              `json:"initiativeIds"`
	UserIDs       []string              `json:"userIds"`
	LoopIDs       []string              `json:"loopIds"`
	Mentions      []domain.AgentMention `json:"mentions"`
	// Answer picks an option of the reply's pending question (ask_user); the message carries its label.
	Answer *agentQuestionAnswer `json:"answer,omitempty"`
	// Attachments are files sent with the message (see agent_attachments.go).
	Attachments []loopRunReplyAttachmentInput `json:"attachments"`
}

type agentSessionUpdate struct {
	Title    *string `json:"title,omitempty"`
	Favorite *bool   `json:"favorite,omitempty"`
	Location *string `json:"location,omitempty"`
}

type agentSkillInput struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
}

const maxAgentMessageBytes = 3 << 20

func (s *server) requireAgent(w http.ResponseWriter) bool {
	if s.agent.Enabled {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "Flow Agent is not configured")
	return false
}

func validAgentMessage(w http.ResponseWriter, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxAgentMessageBytes {
		writeError(w, http.StatusBadRequest, "message is required and must not exceed 3 MB")
		return "", false
	}
	return value, true
}

// agentMessageEdit is an edited (or retried) user message; attachments, when sent, replace the message's files.
type agentMessageEdit struct {
	Message     string                        `json:"message"`
	Attachments []loopRunReplyAttachmentInput `json:"attachments"`
}

func decodeAgentMessage(w http.ResponseWriter, r *http.Request) (agentMessageEdit, bool) {
	var input agentMessageEdit
	if !decodeJSON(w, r, &input) {
		return input, false
	}
	var ok bool
	input.Message, ok = validAgentMessage(w, input.Message)
	return input, ok
}

// decodeAgentMessageInput reads a follow-up message with its mentions and checks their access.
func (s *server) decodeAgentMessageInput(w http.ResponseWriter, r *http.Request) (agentMessageInput, bool) {
	var input agentMessageInput
	if !decodeJSON(w, r, &input) {
		return input, false
	}
	var ok bool
	if input.Message, ok = validAgentMessage(w, input.Message); !ok {
		return input, false
	}
	if !validAgentMentionCounts(w, input.IssueIDs, input.ProjectIDs, input.DocumentIDs, input.InitiativeIDs) || !validAgentMentionList(w, input.Mentions, input.UserIDs) {
		return input, false
	}
	if err := s.checkAgentMentions(r, agentMentionIDs{Issues: input.IssueIDs, Projects: input.ProjectIDs, Documents: input.DocumentIDs, Initiatives: input.InitiativeIDs, Users: input.UserIDs}); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return input, false
	}
	return input, true
}

func validAgentMentionCounts(w http.ResponseWriter, issueIDs, projectIDs, documentIDs, initiativeIDs []string) bool {
	if !validAgentIssueCount(w, issueIDs) {
		return false
	}
	if len(projectIDs) > 25 || len(documentIDs) > 25 || len(initiativeIDs) > 25 {
		writeError(w, http.StatusBadRequest, "no more than 25 projectIds, documentIds or initiativeIds are allowed")
		return false
	}
	return true
}

// agentMentionIDs are the resources a chat request puts in context.
type agentMentionIDs struct {
	Issues, Projects, Documents, Initiatives, Users []string
}

// checkAgentMentions confirms the viewer can read every mentioned issue, project, document and initiative.
// The workspace data is already narrowed to what the viewer can see, so a hidden resource reads as not found.
func (s *server) checkAgentMentions(r *http.Request, ids agentMentionIDs) error {
	if _, err := s.agentIssueContext(r, ids.Issues); err != nil {
		return err
	}
	if len(ids.Projects) == 0 && len(ids.Documents) == 0 && len(ids.Initiatives) == 0 && len(ids.Users) == 0 {
		return nil
	}
	data := s.workspaceData(r)
	if len(selectedAgentProjects(data.Projects, ids.Projects)) != len(uniqueAgentIDs(ids.Projects)) {
		return fmt.Errorf("%w: one or more mentioned projects were not found", errInvalid)
	}
	if len(selectedAgentDocuments(data.Documents, ids.Documents)) != len(uniqueAgentIDs(ids.Documents)) {
		return fmt.Errorf("%w: one or more mentioned documents were not found", errInvalid)
	}
	if len(selectedAgentInitiatives(data.Initiatives, ids.Initiatives)) != len(uniqueAgentIDs(ids.Initiatives)) {
		return fmt.Errorf("%w: one or more mentioned initiatives were not found", errInvalid)
	}
	if len(selectedAgentUsers(data.Users, ids.Users)) != len(uniqueAgentIDs(ids.Users)) {
		return fmt.Errorf("%w: one or more mentioned people were not found", errInvalid)
	}
	return nil
}

// agentMentionListError bounds the mention list stored with a message.
func agentMentionListError(mentions []domain.AgentMention, userIDs []string) error {
	if len(mentions) > 50 || len(userIDs) > 25 {
		return fmt.Errorf("%w: too many mentions", errInvalid)
	}
	for _, mention := range mentions {
		switch mention.Type {
		case "issue", "project", "document", "initiative", "user":
		default:
			return fmt.Errorf("%w: mention type must be issue, project, document, initiative or user", errInvalid)
		}
		if mention.ID == "" || len(mention.Label) > 200 {
			return fmt.Errorf("%w: mentions need an id and a short label", errInvalid)
		}
	}
	return nil
}

func validAgentMentionList(w http.ResponseWriter, mentions []domain.AgentMention, userIDs []string) bool {
	if err := agentMentionListError(mentions, userIDs); err != nil {
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errInvalid.Error()+": "))
		return false
	}
	return true
}

func selectedAgentUsers(users []domain.User, ids []string) []domain.User {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []domain.User{}
	for _, user := range users {
		if wanted[user.ID] {
			selected = append(selected, user)
		}
	}
	return selected
}

func selectedAgentProjects(projects []domain.Project, ids []string) []domain.Project {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []domain.Project{}
	for _, project := range projects {
		if wanted[project.ID] && project.ArchivedAt == nil {
			selected = append(selected, project)
		}
	}
	return selected
}

func selectedAgentDocuments(documents []domain.Document, ids []string) []domain.Document {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	selected := []domain.Document{}
	for _, document := range documents {
		if wanted[document.ID] {
			selected = append(selected, document)
		}
	}
	return selected
}

// mergeAgentIDs appends ids not already present, keeping order.
func mergeAgentIDs(current, extra []string) []string {
	return uniqueAgentIDs(append(append([]string{}, current...), extra...))
}

// agentMentionPrompt describes @-mentioned projects and documents for the model.
func agentMentionPrompt(projects []domain.Project, documents []domain.Document, users []domain.User) string {
	if len(projects) == 0 && len(documents) == 0 && len(users) == 0 {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("\nResources the user mentioned:\n")
	for _, project := range projects {
		fmt.Fprintf(&prompt, "- Project %q (id %s, status %s)", project.Name, project.ID, project.Status.Name)
		if project.TargetDate != nil {
			fmt.Fprintf(&prompt, ", target %s", *project.TargetDate)
		}
		if project.Lead != nil {
			fmt.Fprintf(&prompt, ", lead %s", project.Lead.DisplayName)
		}
		prompt.WriteString("\n")
		if summary := strings.TrimSpace(project.Summary); summary != "" {
			fmt.Fprintf(&prompt, "  Summary: %s\n", truncateSettingsText(summary, 500))
		}
		if description := strings.TrimSpace(project.Description); description != "" {
			fmt.Fprintf(&prompt, "  Description: %s\n", truncateSettingsText(description, 2000))
		}
	}
	for _, user := range users {
		fmt.Fprintf(&prompt, "- Person %s (@%s, id %s)\n", user.DisplayName, user.Name, user.ID)
	}
	for _, document := range documents {
		fmt.Fprintf(&prompt, "- Document %q (id %s)\n", documentDisplayTitle(document.Title), document.ID)
		if content := strings.TrimSpace(document.Content); content != "" {
			fmt.Fprintf(&prompt, "  Content: %s\n", truncateSettingsText(content, 4000))
		}
	}
	return prompt.String()
}

func validAgentIssueCount(w http.ResponseWriter, issueIDs []string) bool {
	if len(issueIDs) <= 25 {
		return true
	}
	writeError(w, http.StatusBadRequest, "no more than 25 issueIds are allowed")
	return false
}

// withAgentBodies loads the offloaded parts (long replies, tool results) of a
// session sent whole: the conversation view and the model's history. Lists
// send sessions without them (agentSessionSummary).
func (s *server) withAgentBodies(r *http.Request, session domain.AgentSession) domain.AgentSession {
	if !store.AgentSessionHasOffloaded(session) {
		return session
	}
	loaded, err := s.store.LoadAgentSessionBodies(r.Context(), workspaceKey(r), session)
	if err != nil {
		log.Printf("load agent session %s: %v", session.ID, err)
	}
	return loaded
}

// agentSessionSummary is how lists send a session whose transcript is partly
// offloaded: without messages, so the app loads the conversation
// (GET /api/agent/sessions/{id}) when it is opened.
func agentSessionSummary(session domain.AgentSession) domain.AgentSession {
	if store.AgentSessionHasOffloaded(session) {
		session.Messages = []domain.AgentMessage{}
	}
	return session
}

func (s *server) completeAgentSessionResponse(w http.ResponseWriter, r *http.Request, id string, status int) {
	completed, err := s.completeAgentSession(r, id)
	if err != nil {
		writeErrorFor(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, status, s.withAgentBodies(r, completed))
}

func (s *server) agentStatus(w http.ResponseWriter, _ *http.Request) {
	protocol := s.agent.Protocol
	if protocol == "" {
		protocol = "openai-chat-completions"
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.agent.Enabled, "model": s.agent.Model, "protocol": protocol, "toolsEnabled": s.agent.ToolsEnabled, "writeTools": s.agent.WriteTools})
}

func (s *server) agentChat(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	var input agentChatInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var ok bool
	if input.Message, ok = validAgentMessage(w, input.Message); !ok {
		return
	}
	if !validAgentIssueCount(w, input.IssueIDs) {
		return
	}
	data, contextErr := s.agentIssueContext(r, input.IssueIDs)
	if contextErr != nil {
		writeErrorFor(w, r, http.StatusBadRequest, contextErr)
		return
	}
	if data.Workspace.ID == "" {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	issues := selectedAgentIssues(data.Issues, input.IssueIDs)
	if len(issues) != len(uniqueAgentIDs(input.IssueIDs)) {
		writeError(w, http.StatusBadRequest, "one or more selected issues were not found")
		return
	}
	messages := []agentChatMessage{{Role: "system", Content: workspaceAgentSystemPrompt(data, issues, nil)}}
	for _, message := range input.History {
		message.Role = strings.ToLower(strings.TrimSpace(message.Role))
		message.Content = strings.TrimSpace(message.Content)
		if (message.Role == "user" || message.Role == "assistant") && message.Content != "" && len(message.Content) <= maxAgentMessageBytes {
			messages = append(messages, message)
		}
	}
	if len(messages) > 21 {
		messages = append(messages[:1], messages[len(messages)-20:]...)
	}
	messages = append(messages, agentChatMessage{Role: "user", Content: input.Message})
	reply, err := s.requestAgent(r, messages)
	if err != nil {
		writeErrorFor(w, r, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": reply, "model": s.agent.Model})
}

func (s *server) listAgentSessions(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := make([]domain.AgentSession, 0)
	for _, session := range data.AgentSessions {
		if session.UserID == data.Viewer.ID && session.DeletedAt == nil {
			result = append(result, agentSessionSummary(session))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) getAgentSession(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	session, err := ownedAgentSession(&data, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "agent chat not found")
		return
	}
	writeJSON(w, http.StatusOK, s.withAgentBodies(r, *session))
}

func (s *server) createAgentSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	var input agentSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	var ok bool
	if input.Message, ok = validAgentMessage(w, input.Message); !ok {
		return
	}
	if !validAgentMentionCounts(w, input.IssueIDs, input.ProjectIDs, input.DocumentIDs, input.InitiativeIDs) {
		return
	}
	if input.Location == "" {
		input.Location = "page"
	}
	if input.Location != "page" && input.Location != "toolbar" {
		writeError(w, http.StatusBadRequest, "location must be page or toolbar")
		return
	}
	session, err := s.beginAgentSession(r, input)
	if err != nil {
		respondMutation(w, err, http.StatusCreated, session)
		return
	}
	s.completeAgentSessionResponse(w, r, session.ID, http.StatusCreated)
}

func (s *server) beginAgentSession(r *http.Request, input agentSessionInput) (domain.AgentSession, error) {
	if err := agentMentionListError(input.Mentions, input.UserIDs); err != nil {
		return domain.AgentSession{}, err
	}
	if err := s.checkAgentMentions(r, agentMentionIDs{Issues: input.IssueIDs, Projects: input.ProjectIDs, Documents: input.DocumentIDs, Initiatives: input.InitiativeIDs, Users: input.UserIDs}); err != nil {
		return domain.AgentSession{}, err
	}
	attachments, err := s.saveAgentAttachments(r, input.Attachments)
	if err != nil {
		return domain.AgentSession{}, err
	}
	images := s.withStoredSizes(r.Context(), agentMessageUploads(r, input.Message))
	attachments = append(attachments, images...)
	now := time.Now().UTC()
	sessionID := idgen.New("agent_session_")
	title := agentSessionTitle(firstNonEmpty(withoutAgentUploads(input.Message, images), agentUploadsTitle(images)))
	session := domain.AgentSession{ID: sessionID, SlugID: agentSessionSlug(title, now), Title: title, Location: input.Location, IssueIDs: uniqueAgentIDs(input.IssueIDs), ProjectIDs: uniqueAgentIDs(input.ProjectIDs), DocumentIDs: uniqueAgentIDs(input.DocumentIDs), InitiativeIDs: uniqueAgentIDs(input.InitiativeIDs), UserIDs: uniqueAgentIDs(input.UserIDs), LoopIDs: uniqueAgentIDs(input.LoopIDs), SkillIDs: uniqueAgentIDs(input.SkillIDs), Messages: []domain.AgentMessage{{ID: idgen.New("agent_message_"), Role: "user", Content: input.Message, Mentions: input.Mentions, Attachments: attachments, CreatedAt: now}}, CreatedAt: now, UpdatedAt: now, LastReadAt: agentReadStamp(now)}
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_created", sessionID, input, func(data *domain.Bootstrap) error {
		session.UserID = data.Viewer.ID
		if len(selectedAgentSkills(data.AgentSkills, session.SkillIDs, session.UserID)) != len(session.SkillIDs) {
			return fmt.Errorf("%w: one or more selected skills were not found", errInvalid)
		}
		if err := checkAgentLoops(data, session.LoopIDs); err != nil {
			return err
		}
		data.AgentSessions = append(data.AgentSessions, session)
		return nil
	})
	if err != nil {
		s.deleteAgentAttachments(r.Context(), agentUploads(attachments, false))
	}
	return session, err
}

func (s *server) createAgentSessionMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	input, ok := s.decodeAgentMessageInput(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	err := s.appendAgentSessionMessage(r, id, input)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.completeAgentSessionResponse(w, r, id, http.StatusOK)
}

func (s *server) appendAgentSessionMessage(r *http.Request, id string, input agentMessageInput) error {
	message := input.Message
	attachments, err := s.saveAgentAttachments(r, input.Attachments)
	if err != nil {
		return err
	}
	attachments = append(attachments, s.withStoredSizes(r.Context(), agentMessageUploads(r, message))...)
	now := time.Now().UTC()
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_created", id, input, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		session.IssueIDs = mergeAgentIDs(session.IssueIDs, input.IssueIDs)
		session.ProjectIDs = mergeAgentIDs(session.ProjectIDs, input.ProjectIDs)
		session.DocumentIDs = mergeAgentIDs(session.DocumentIDs, input.DocumentIDs)
		session.InitiativeIDs = mergeAgentIDs(session.InitiativeIDs, input.InitiativeIDs)
		session.UserIDs = mergeAgentIDs(session.UserIDs, input.UserIDs)
		session.LoopIDs = mergeAgentIDs(session.LoopIDs, input.LoopIDs)
		if err := checkAgentLoops(data, session.LoopIDs); err != nil {
			return err
		}
		if len(session.IssueIDs) > 25 || len(session.ProjectIDs) > 25 || len(session.DocumentIDs) > 25 || len(session.InitiativeIDs) > 25 || len(session.UserIDs) > 25 {
			return fmt.Errorf("%w: a conversation can reference at most 25 issues, projects, documents and initiatives each", errInvalid)
		}
		// The next message answers a question the agent stopped on: a picked option or free text.
		answerTo, err := answerAgentQuestion(session, message, input.Answer)
		if err != nil {
			return err
		}
		session.Messages = append(session.Messages, domain.AgentMessage{ID: idgen.New("agent_message_"), Role: "user", Content: message, Mentions: input.Mentions, Attachments: attachments, AnswerTo: answerTo, CreatedAt: now})
		data.AgentActivities = append(data.AgentActivities, domain.AgentActivity{ID: idgen.New("agent_activity_"), SessionID: id, Type: "message", Status: "completed", Body: message, CreatedAt: now, UpdatedAt: now})
		session.UpdatedAt = now
		session.LastReadAt = agentReadStamp(now) // the owner is in the conversation they just wrote to
		return nil
	})
	if err != nil {
		s.deleteAgentAttachments(r.Context(), agentUploads(attachments, false))
	}
	return err
}

func (s *server) updateAgentSessionMessage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	message, ok := decodeAgentMessage(w, r)
	if !ok {
		return
	}
	id, messageID := r.PathValue("id"), r.PathValue("messageId")
	err := s.replaceAgentSessionMessage(r, id, messageID, message)
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	s.completeAgentSessionResponse(w, r, id, http.StatusOK)
}

func (s *server) replaceAgentSessionMessage(r *http.Request, id, messageID string, edit agentMessageEdit) error {
	attachments, err := s.saveAgentAttachments(r, edit.Attachments)
	if err != nil {
		return err
	}
	// Read outside the mutation: sizes come from storage.
	images := s.withStoredSizes(r.Context(), agentMessageUploads(r, edit.Message))
	input := map[string]any{"message": edit.Message, "attachments": edit.Attachments}
	// Files of the replaced attachments and of the dropped later messages, removed once the edit is saved.
	var removed []domain.AgentMessageAttachment
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_updated", id, input, func(data *domain.Bootstrap) error {
		removed = nil
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		for index := range session.Messages {
			message := &session.Messages[index]
			if message.ID == messageID && message.Role == "user" {
				message.Content = edit.Message
				// Sent files stay unless the edit sends new ones (a retry resends the text alone); inline images follow the text.
				files := agentUploads(message.Attachments, false)
				if len(attachments) > 0 {
					removed = append(removed, files...)
					files = attachments
				}
				for _, old := range agentUploads(message.Attachments, true) {
					if !slices.ContainsFunc(images, func(item domain.AgentMessageAttachment) bool { return item.URL == old.URL }) {
						removed = append(removed, old)
					}
				}
				message.Attachments = append(files, images...)
				removed = append(removed, messageAttachments(session.Messages[index+1:])...)
				// A later message's upload pasted again into the edit stays.
				removed = slices.DeleteFunc(removed, func(item domain.AgentMessageAttachment) bool {
					return slices.ContainsFunc(message.Attachments, func(kept domain.AgentMessageAttachment) bool { return kept.URL == item.URL })
				})
				session.Messages = session.Messages[:index+1]
				session.UpdatedAt = time.Now().UTC()
				session.LastReadAt = agentReadStamp(session.UpdatedAt)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		s.deleteAgentAttachments(r.Context(), attachments)
		return err
	}
	s.deleteAgentAttachments(r.Context(), removed)
	return nil
}

func (s *server) completeAgentSession(r *http.Request, id string) (domain.AgentSession, error) {
	return s.runAgentSession(r, id, nil)
}

func (s *server) updateAgentSession(w http.ResponseWriter, r *http.Request) {
	var input agentSessionUpdate
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Location != nil && *input.Location != "page" && *input.Location != "toolbar" {
		writeError(w, http.StatusBadRequest, "location must be page or toolbar")
		return
	}
	var updated domain.AgentSession
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, r.PathValue("id"))
		if err != nil {
			return err
		}
		before := *session
		if input.Title != nil {
			title := strings.TrimSpace(*input.Title)
			if title == "" || len(title) > 120 {
				return fmt.Errorf("%w: title must not be empty or exceed 120 characters", errInvalid)
			}
			session.Title = title
			session.SlugID = agentSessionSlug(title, session.CreatedAt)
		}
		if input.Favorite != nil {
			session.Favorite = *input.Favorite
		}
		if input.Location != nil {
			session.Location = *input.Location
		}
		if unchangedEdit(before, *session) {
			updated = before
			return store.ErrNoMutation
		}
		// The owner's own rename, favorite or move never makes the chat unread.
		wasRead := agentSessionRead(*session)
		session.UpdatedAt = time.Now().UTC()
		if wasRead {
			session.LastReadAt = agentReadStamp(session.UpdatedAt)
		}
		updated = *session
		return nil
	})
	respondMutation(w, err, http.StatusOK, s.withAgentBodies(r, updated))
}

// markAgentSessionRead records that the owner has seen the conversation up to
// its latest change. It never moves UpdatedAt, so history order is unchanged.
func (s *server) markAgentSessionRead(w http.ResponseWriter, r *http.Request) {
	var updated domain.AgentSession
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_read", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, r.PathValue("id"))
		if err != nil {
			return err
		}
		readAt := time.Now().UTC()
		if session.UpdatedAt.After(readAt) {
			readAt = session.UpdatedAt
		}
		session.LastReadAt = agentReadStamp(readAt)
		updated = *session
		return nil
	})
	respondMutation(w, err, http.StatusOK, s.withAgentBodies(r, updated))
}

type agentFeedbackInput struct {
	Rating  *string `json:"rating"`
	Comment *string `json:"comment"`
}

const maxAgentFeedbackComment = 4000

// onlyRemoves is true for a request that records nothing: it clears a rating and carries no note.
func (input agentFeedbackInput) onlyRemoves() bool {
	return input.Rating != nil && *input.Rating == "" && (input.Comment == nil || strings.TrimSpace(*input.Comment) == "")
}

func (input agentFeedbackInput) validate() error {
	if input.Rating != nil && *input.Rating != "" && *input.Rating != "positive" && *input.Rating != "negative" {
		return fmt.Errorf("%w: rating must be positive, negative or empty", errInvalid)
	}
	if input.Comment != nil && len(*input.Comment) > maxAgentFeedbackComment {
		return fmt.Errorf("%w: feedback must not exceed %d characters", errInvalid, maxAgentFeedbackComment)
	}
	return nil
}

// updateAgentMessageFeedback rates one assistant reply (the reference app's thumbs up / "Leave feedback…"):
// a rating replaces the previous one, an empty rating removes it, and a comment is kept with it.
// Rating never changes the conversation's read state or order.
func (s *server) updateAgentMessageFeedback(w http.ResponseWriter, r *http.Request) {
	var input agentFeedbackInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := input.validate(); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	id, messageID := r.PathValue("id"), r.PathValue("messageId")
	var updated domain.AgentSession
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_feedback", id, input, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		// With usage feedback turned off nothing new is recorded; taking back a rating already given still works.
		if !usageFeedbackEnabled(data.WorkspaceSettings) && !input.onlyRemoves() {
			return store.ErrAuthForbidden
		}
		for index := range session.Messages {
			message := &session.Messages[index]
			if message.ID != messageID {
				continue
			}
			if message.Role != "assistant" {
				return fmt.Errorf("%w: only agent replies can be rated", errInvalid)
			}
			next := domain.AgentFeedback{UpdatedAt: time.Now().UTC()}
			if message.Feedback != nil {
				next.Rating, next.Comment = message.Feedback.Rating, message.Feedback.Comment
			}
			if input.Rating != nil {
				next.Rating = *input.Rating
				if next.Rating == "" {
					next.Comment = ""
				}
			}
			if input.Comment != nil {
				next.Comment = strings.TrimSpace(*input.Comment)
			}
			previous := message.Feedback
			if next.Rating == "" && next.Comment == "" {
				message.Feedback = nil
			} else {
				message.Feedback = &next
			}
			updated = *session
			if previous == nil && message.Feedback == nil || previous != nil && message.Feedback != nil && previous.Rating == next.Rating && previous.Comment == next.Comment {
				// The same rating and comment again write nothing.
				message.Feedback = previous
				updated = *session
				return store.ErrNoMutation
			}
			return nil
		}
		return errNotFound
	})
	respondMutation(w, err, http.StatusOK, s.withAgentBodies(r, updated))
}

// createAgentSessionFeedback records a note about the whole conversation (the chat menu's "Leave feedback…").
func (s *server) createAgentSessionFeedback(w http.ResponseWriter, r *http.Request) {
	var input agentFeedbackInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := input.validate(); err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	comment := ""
	if input.Comment != nil {
		comment = strings.TrimSpace(*input.Comment)
	}
	if comment == "" {
		respondMutation(w, fmt.Errorf("%w: feedback must not be empty", errInvalid), http.StatusOK, nil)
		return
	}
	id := r.PathValue("id")
	var updated domain.AgentSession
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_feedback", id, input, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		if !usageFeedbackEnabled(data.WorkspaceSettings) {
			return store.ErrAuthForbidden
		}
		session.Feedback = append(session.Feedback, domain.AgentFeedback{Comment: comment, UpdatedAt: time.Now().UTC()})
		updated = *session
		return nil
	})
	respondMutation(w, err, http.StatusCreated, s.withAgentBodies(r, updated))
}

// agentReadStamp copies the time so LastReadAt never aliases another field.
func agentReadStamp(at time.Time) *time.Time {
	return &at
}

// agentSessionRead reports whether the owner has seen the conversation's latest change.
func agentSessionRead(session domain.AgentSession) bool {
	return session.LastReadAt == nil || !session.UpdatedAt.After(*session.LastReadAt)
}

// deleteAgentSession hides the chat; its files go when the retention purge removes it.
func (s *server) deleteAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_deleted", id, nil, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		session.DeletedAt = &now
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restoreAgentSession brings back a deleted chat that has not been purged.
func (s *server) restoreAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var restored domain.AgentSession
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_restored", id, nil, func(data *domain.Bootstrap) error {
		// An exact ID wins over a slug shared with another deleted chat.
		for _, byID := range []bool{true, false} {
			for index := range data.AgentSessions {
				session := &data.AgentSessions[index]
				if (byID && session.ID == id || !byID && session.SlugID == id) && session.UserID == data.Viewer.ID && session.DeletedAt != nil {
					session.DeletedAt = nil
					restored = *session
					return nil
				}
			}
		}
		return errNotFound
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, s.withAgentBodies(r, restored))
}

// purgeDeletedAgentSessions permanently removes chats deleted before cutoff,
// with the files sent in them.
func (s *server) purgeDeletedAgentSessions(ctx context.Context, key string, cutoff time.Time) (int, error) {
	data, ok := s.store.WorkspaceMetadataFields(key, "agentSessions")
	if !ok {
		return 0, nil
	}
	expired := func(session domain.AgentSession) bool {
		return session.DeletedAt != nil && session.DeletedAt.Before(cutoff)
	}
	if !slices.ContainsFunc(data.AgentSessions, expired) {
		return 0, nil
	}
	var removed []domain.AgentMessageAttachment
	purged := 0
	err := s.store.MutateWorkspace(ctx, key, "agent.sessions_purged", "agent_sessions", nil, func(next *domain.Bootstrap) error {
		for _, session := range next.AgentSessions {
			if expired(session) {
				removed = append(removed, messageAttachments(session.Messages)...)
			}
		}
		before := len(next.AgentSessions)
		next.AgentSessions = slices.DeleteFunc(next.AgentSessions, expired)
		if purged = before - len(next.AgentSessions); purged == 0 {
			return store.ErrNoMutation
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	s.deleteAgentAttachments(ctx, removed)
	return purged, nil
}

func (s *server) listAgentSkillsHTTP(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := make([]domain.PersonalAgentSkill, 0)
	for _, skill := range data.AgentSkills {
		if skill.UserID == data.Viewer.ID {
			result = append(result, skill)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createAgentSkill(w http.ResponseWriter, r *http.Request) {
	var input agentSkillInput
	if !decodeJSON(w, r, &input) || !validateAgentSkill(w, input) {
		return
	}
	now := time.Now().UTC()
	skill := domain.PersonalAgentSkill{ID: idgen.New("agent_skill_"), Name: strings.TrimSpace(input.Name), Instructions: strings.TrimSpace(input.Instructions), CreatedAt: now, UpdatedAt: now}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.skill_created", skill.ID, input, func(data *domain.Bootstrap) error {
		skill.UserID = data.Viewer.ID
		data.AgentSkills = append(data.AgentSkills, skill)
		return nil
	})
	respondMutation(w, err, http.StatusCreated, skill)
}

func (s *server) updateAgentSkill(w http.ResponseWriter, r *http.Request) {
	var input agentSkillInput
	if !decodeJSON(w, r, &input) || !validateAgentSkill(w, input) {
		return
	}
	var updated domain.PersonalAgentSkill
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.skill_updated", r.PathValue("id"), input, func(data *domain.Bootstrap) error {
		for index := range data.AgentSkills {
			skill := &data.AgentSkills[index]
			if skill.ID == r.PathValue("id") && skill.UserID == data.Viewer.ID {
				if skill.Name == strings.TrimSpace(input.Name) && skill.Instructions == strings.TrimSpace(input.Instructions) {
					updated = *skill
					return store.ErrNoMutation
				}
				skill.Name = strings.TrimSpace(input.Name)
				skill.Instructions = strings.TrimSpace(input.Instructions)
				skill.UpdatedAt = time.Now().UTC()
				updated = *skill
				return nil
			}
		}
		return errNotFound
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteAgentSkill(w http.ResponseWriter, r *http.Request) {
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.skill_deleted", r.PathValue("id"), nil, func(data *domain.Bootstrap) error {
		for index, skill := range data.AgentSkills {
			if skill.ID == r.PathValue("id") && skill.UserID == data.Viewer.ID {
				data.AgentSkills = append(data.AgentSkills[:index], data.AgentSkills[index+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		respondMutation(w, err, http.StatusNoContent, nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateAgentSkill(w http.ResponseWriter, input agentSkillInput) bool {
	if strings.TrimSpace(input.Name) == "" || len(strings.TrimSpace(input.Name)) > 80 || strings.TrimSpace(input.Instructions) == "" || len(strings.TrimSpace(input.Instructions)) > 12000 {
		writeError(w, http.StatusBadRequest, "skill name and instructions are required")
		return false
	}
	return true
}

func ownedAgentSession(data *domain.Bootstrap, id string) (*domain.AgentSession, error) {
	for index := range data.AgentSessions {
		session := &data.AgentSessions[index]
		if (session.ID == id || session.SlugID == id) && session.UserID == data.Viewer.ID && session.DeletedAt == nil {
			return session, nil
		}
	}
	return nil, errNotFound
}

func agentSessionTitle(message string) string {
	title := strings.TrimSpace(strings.Split(message, "\n")[0])
	runes := []rune(title)
	if len(runes) > 60 {
		title = string(runes[:60])
	}
	return title
}

func agentSessionSlug(title string, createdAt time.Time) string {
	base := slug(title)
	if base == "" {
		base = "chat"
	}
	return fmt.Sprintf("%s-%x", base, createdAt.UnixNano()&0xffffffffffff)
}

func (s *server) requestAgent(r *http.Request, messages []agentChatMessage) (string, error) {
	providerMessages := make([]agentProviderMessage, 0, len(messages))
	for _, message := range messages {
		providerMessages = append(providerMessages, agentProviderMessage{Role: message.Role, Content: message.Content})
	}
	turn, err := s.requestAgentTurnWithoutTools(r.Context(), providerMessages)
	if err != nil {
		return "", err
	}
	reply := strings.TrimSpace(turn.Text)
	if reply == "" {
		return "", fmt.Errorf("Flow Agent provider returned an empty response")
	}
	return reply, nil
}

func selectedAgentIssues(issues []domain.Issue, ids []string) []domain.Issue {
	wanted := uniqueAgentIDs(ids)
	result := make([]domain.Issue, 0, len(wanted))
	for _, id := range wanted {
		for _, issue := range issues {
			if issue.ID == id {
				result = append(result, issue)
				break
			}
		}
	}
	return result
}

func selectedAgentSkills(skills []domain.PersonalAgentSkill, ids []string, userID string) []domain.PersonalAgentSkill {
	wanted := uniqueAgentIDs(ids)
	result := make([]domain.PersonalAgentSkill, 0, len(wanted))
	for _, id := range wanted {
		for _, skill := range skills {
			if skill.ID == id && skill.UserID == userID {
				result = append(result, skill)
				break
			}
		}
	}
	return result
}

func uniqueAgentIDs(ids []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	return result
}

// agentProgressRule asks for report_progress narration. Interactive chat swaps it for agentChatToolRule: a
// progress call on a turn of its own costs a whole model round trip.
const agentProgressRule = "- When a request needs tool lookups, also call report_progress in the same turn with a short title for that phase (and, on the first call only, a one-sentence plan); never call it on its own or for trivial answers, and never write its JSON into your reply text.\n"

// agentPromptRulesHead and agentPromptRulesTail are the static Flow Agent instructions around the tool rule. They
// come first in the prompt so the provider can cache them; per-request context follows.
const agentPromptRulesHead = "You are Flow Agent. Help with workspace tasks, including drafting projects when requested, using only the supplied workspace context. Be concise, distinguish facts from suggestions, and never invent issue or project state.\n\nGrounding rules:\n- Look facts up with the Flow tools before answering questions about issues, projects, people, cycles, or documents; if a tool result does not contain a value, say it is not set or unknown instead of guessing.\n- For questions whose answer lives in workspace documents (specs, processes, criteria, decisions, how-tos), call search_documents with short keyword sets: the key nouns without question words, plus synonyms or the other language as extra queries, and new keywords if nothing fits. Open the best match with get_document, answer from its text (quote the deciding lines), and cite the document as a link; when several documents disagree, say which one says what. If no document covers it, say so instead of guessing.\n- Refer to issues by identifier and title (for example ENG-12 Fix login), never by internal ids such as issue_1. Link it once as [ENG-12 Fix login](url) when a url is available and continue the sentence without repeating the title; the app renders it as a chip.\n- Priority values are 0 = No priority, 1 = Urgent, 2 = High, 3 = Medium, 4 = Low; prefer the priorityLabel field when present.\n- Reply in the language the user writes in.\n- When the user asks you to create or change something and you have enough to go on — or tells you to decide — pick sensible defaults, state them in one or two lines, and call the write tool right away; changes apply immediately without an approval step. Deleted items go to the trash — say they can be restored. Do not repeat a setup checklist or ask for details you can reasonably choose.\n- Never ask for permission to call a tool in text. Flow writes, deletes included, run when you call them; a connector tool its owner set to always ask is held by the app, which asks the user with Approve and Reject and resumes you with the result. Just call the tool, and do not call it again while it waits.\n- In tool arguments pass ids, slugs, or bare identifiers such as DEV-42 — never a title, chip text, or a link.\n- For bulk requests (many people, issues, or items), pass the whole list where a tool takes one (save_team members, get_user queries) instead of looking each item up first, and make independent calls together in one step rather than one per step.\n- After writes, your answer names every item you created or changed (linked) and every action that failed with the error the tool returned. Report only what the tool results show (for example the status name they return), and never claim a change that did not succeed. Never say a change failed or is not permitted unless you called its write tool and the result says so: reads such as list_teams do not show what the user may change, so call the write tool and report the error it returns. When the reply only reports actions, keep the prose to one short line — the app shows a card for each created or changed item — but still name every item and every failure.\n- When a value you need is genuinely ambiguous and you cannot reasonably choose it yourself (for example which of several matching issues they mean, or a priority the user asked to be asked about), call ask_user with the question and two to six short options instead of asking in plain text. Do not use ask_user for choices you can make with sensible defaults.\n- Answer style: lead with the conclusion in one or two sentences, then a few short paragraphs, using bold lead-ins such as **History:** or **Similar issues:** instead of headings; use bullets only for real lists, and stay under about 150 words unless the user asks for more.\n"

// agentChatSuggestionsRule asks for follow-up chips. Only the chat panel renders them; comment threads, loops and
// other surfaces have no suggestion chips (like the reference app's threads), so the rule is added for chat only.
const agentChatSuggestionsRule = "- When the user might want to dig deeper (investigations, summaries, comparisons), end with a fenced block tagged suggestions (```suggestions ... ```) holding one or two short follow-up requests they could send next, one per line, in the user's language; omit it for drafts, confirmations, and simple factual answers.\n"

const agentPromptRulesTail = "- Selected issues listed below are already loaded; only call a tool for them when you need fields that are not shown.\n- Translate raw enum values (for example noUpdate, onTrack, atRisk, unstarted) into plain words.\n- Resolve relative dates such as \"in two weeks\" or \"next Friday\" against Today below.\n- When asked to write a project update: look up the project, its issues, milestones, recent comments, and earlier updates with the tools; then reply with one short sentence about what you drafted, followed by the update body in a fenced block tagged update (```update ... ```). Keep the body to a few short paragraphs or bullets covering progress, risks or blockers, and next steps; state only facts found in the workspace and say plainly when there is little to report.\n\nWhen drafting a project, do not create or mutate it without an explicit user request; return a concise explanation followed by a JSON object with optional keys name, summary, description, status, priority, startDate, targetDate, milestones, team, lead, members, initiatives, labels, and dependencies. Use display names for people and resources, use YYYY-MM-DD for dates, and give priority as one of No priority, Urgent, High, Medium, or Low.\n"

func agentPromptRules(toolRule string) string {
	if toolRule == agentChatToolRule {
		return agentPromptRulesHead + agentChatSuggestionsRule + toolRule + agentPromptRulesTail
	}
	return agentPromptRulesHead + toolRule + agentPromptRulesTail
}

func agentSystemPrompt(workspace string, issues []domain.Issue, skills ...[]domain.PersonalAgentSkill) string {
	var active []domain.PersonalAgentSkill
	if len(skills) > 0 {
		active = skills[0]
	}
	return agentPromptRules(agentProgressRule) + agentPromptContext(workspace, issues, active)
}

// agentPromptContext is the per-request part of the instructions: today, the workspace, loaded issues, skills.
func agentPromptContext(workspace string, issues []domain.Issue, skills []domain.PersonalAgentSkill) string {
	var prompt strings.Builder
	prompt.WriteString("\nToday: ")
	prompt.WriteString(time.Now().Format("2006-01-02 (Monday)"))
	prompt.WriteString("\nWorkspace: ")
	prompt.WriteString(workspace)
	prompt.WriteString("\nSelected issues:\n")
	for _, issue := range issues {
		fmt.Fprintf(&prompt, "- %s: %s\n  Status: %s; Priority: %s; Team: %s", issue.Identifier, issue.Title, issue.State.Name, issue.PriorityLabel, issue.Team.Name)
		if issue.Assignee != nil {
			fmt.Fprintf(&prompt, "; Assignee: %s", issue.Assignee.DisplayName)
		}
		if issue.Project != nil {
			fmt.Fprintf(&prompt, "; Project: %s", issue.Project.Name)
		}
		if len(issue.Labels) > 0 {
			names := make([]string, 0, len(issue.Labels))
			for _, label := range issue.Labels {
				names = append(names, label.Name)
			}
			fmt.Fprintf(&prompt, "; Labels: %s", strings.Join(names, ", "))
		}
		if issue.DueDate != nil && *issue.DueDate != "" {
			fmt.Fprintf(&prompt, "; Due: %s", *issue.DueDate)
		}
		prompt.WriteString("\n")
		if description := strings.TrimSpace(issue.Description); description != "" {
			description = truncateSettingsText(description, 4000)
			prompt.WriteString("  Description: ")
			prompt.WriteString(description)
			prompt.WriteString("\n")
		}
	}
	if len(skills) > 0 {
		prompt.WriteString("\nActive skills:\n")
		for _, skill := range skills {
			fmt.Fprintf(&prompt, "- %s: %s\n", skill.Name, skill.Instructions)
		}
	}
	return prompt.String()
}

// checkAgentLoops confirms the loops a conversation builds exist.
func checkAgentLoops(data *domain.Bootstrap, ids []string) error {
	if len(ids) > 10 {
		return fmt.Errorf("%w: a conversation can build at most 10 loops", errInvalid)
	}
	for _, id := range ids {
		if loopByID(data, id) == nil {
			return fmt.Errorf("%w: loop not found", errInvalid)
		}
	}
	return nil
}
