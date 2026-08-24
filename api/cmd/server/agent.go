package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"flow/api/internal/domain"
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
	Message  string   `json:"message"`
	IssueIDs []string `json:"issueIds"`
	SkillIDs []string `json:"skillIds"`
	Location string   `json:"location"`
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

func (s *server) agentStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.agent.Enabled, "model": s.agent.Model})
}

func (s *server) agentChat(w http.ResponseWriter, r *http.Request) {
	if !s.agent.Enabled {
		writeError(w, http.StatusServiceUnavailable, "Flow Agent is not configured")
		return
	}
	var input agentChatInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" || len(input.Message) > maxAgentMessageBytes {
		writeError(w, http.StatusBadRequest, "message is required and must not exceed 3 MB")
		return
	}
	if len(input.IssueIDs) > 25 {
		writeError(w, http.StatusBadRequest, "no more than 25 issueIds are allowed")
		return
	}
	data := s.workspaceData(r)
	if data.Workspace.ID == "" {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	issues := selectedAgentIssues(data.Issues, input.IssueIDs)
	if len(issues) != len(uniqueAgentIDs(input.IssueIDs)) {
		writeError(w, http.StatusBadRequest, "one or more selected issues were not found")
		return
	}
	messages := []agentChatMessage{{Role: "system", Content: agentSystemPrompt(data.Workspace.Name, issues)}}
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
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": reply, "model": s.agent.Model})
}

func (s *server) listAgentSessions(w http.ResponseWriter, r *http.Request) {
	data := s.workspaceData(r)
	result := make([]domain.AgentSession, 0)
	for _, session := range data.AgentSessions {
		if session.UserID == data.Viewer.ID {
			result = append(result, session)
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
	writeJSON(w, http.StatusOK, session)
}

func (s *server) createAgentSession(w http.ResponseWriter, r *http.Request) {
	if !s.agent.Enabled {
		writeError(w, http.StatusServiceUnavailable, "Flow Agent is not configured")
		return
	}
	var input agentSessionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" || len(input.Message) > maxAgentMessageBytes {
		writeError(w, http.StatusBadRequest, "message is required and must not exceed 3 MB")
		return
	}
	if len(input.IssueIDs) > 25 {
		writeError(w, http.StatusBadRequest, "no more than 25 issueIds are allowed")
		return
	}
	if input.Location == "" {
		input.Location = "page"
	}
	if input.Location != "page" && input.Location != "toolbar" {
		writeError(w, http.StatusBadRequest, "location must be page or toolbar")
		return
	}
	now := time.Now().UTC()
	sessionID := fmt.Sprintf("agent_session_%d", now.UnixNano())
	title := agentSessionTitle(input.Message)
	session := domain.AgentSession{ID: sessionID, SlugID: agentSessionSlug(title, now), Title: title, Location: input.Location, IssueIDs: uniqueAgentIDs(input.IssueIDs), SkillIDs: uniqueAgentIDs(input.SkillIDs), Messages: []domain.AgentMessage{{ID: fmt.Sprintf("agent_message_%d", now.UnixNano()), Role: "user", Content: input.Message, CreatedAt: now}}, CreatedAt: now, UpdatedAt: now}
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_created", sessionID, input, func(data *domain.Bootstrap) error {
		if len(selectedAgentIssues(data.Issues, session.IssueIDs)) != len(session.IssueIDs) {
			return fmt.Errorf("%w: one or more selected issues were not found", errInvalid)
		}
		session.UserID = data.Viewer.ID
		if len(selectedAgentSkills(data.AgentSkills, session.SkillIDs, session.UserID)) != len(session.SkillIDs) {
			return fmt.Errorf("%w: one or more selected skills were not found", errInvalid)
		}
		data.AgentSessions = append(data.AgentSessions, session)
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusCreated, session)
		return
	}
	completed, err := s.completeAgentSession(r, sessionID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, completed)
}

func (s *server) createAgentSessionMessage(w http.ResponseWriter, r *http.Request) {
	if !s.agent.Enabled {
		writeError(w, http.StatusServiceUnavailable, "Flow Agent is not configured")
		return
	}
	var input struct {
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" || len(input.Message) > maxAgentMessageBytes {
		writeError(w, http.StatusBadRequest, "message is required and must not exceed 3 MB")
		return
	}
	id := r.PathValue("id")
	now := time.Now().UTC()
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_created", id, input, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		session.Messages = append(session.Messages, domain.AgentMessage{ID: fmt.Sprintf("agent_message_%d", now.UnixNano()), Role: "user", Content: input.Message, CreatedAt: now})
		session.UpdatedAt = now
		return nil
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	completed, err := s.completeAgentSession(r, id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, completed)
}

func (s *server) updateAgentSessionMessage(w http.ResponseWriter, r *http.Request) {
	if !s.agent.Enabled {
		writeError(w, http.StatusServiceUnavailable, "Flow Agent is not configured")
		return
	}
	var input struct {
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Message = strings.TrimSpace(input.Message)
	if input.Message == "" || len(input.Message) > maxAgentMessageBytes {
		writeError(w, http.StatusBadRequest, "message is required and must not exceed 3 MB")
		return
	}
	id, messageID := r.PathValue("id"), r.PathValue("messageId")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_updated", id, input, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		for index := range session.Messages {
			message := &session.Messages[index]
			if message.ID == messageID && message.Role == "user" {
				message.Content = input.Message
				session.Messages = session.Messages[:index+1]
				session.UpdatedAt = time.Now().UTC()
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		respondMutation(w, err, http.StatusOK, nil)
		return
	}
	completed, err := s.completeAgentSession(r, id)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, completed)
}

func (s *server) completeAgentSession(r *http.Request, id string) (domain.AgentSession, error) {
	data := s.workspaceData(r)
	session, err := ownedAgentSession(&data, id)
	if err != nil {
		return domain.AgentSession{}, err
	}
	issues := selectedAgentIssues(data.Issues, session.IssueIDs)
	skills := selectedAgentSkills(data.AgentSkills, session.SkillIDs, session.UserID)
	messages := []agentChatMessage{{Role: "system", Content: agentSystemPrompt(data.Workspace.Name, issues, skills)}}
	for _, message := range session.Messages {
		messages = append(messages, agentChatMessage{Role: message.Role, Content: message.Content})
	}
	if len(messages) > 21 {
		messages = append(messages[:1], messages[len(messages)-20:]...)
	}
	started := time.Now()
	reply, err := s.requestAgent(r, messages)
	if err != nil {
		return domain.AgentSession{}, err
	}
	now := time.Now().UTC()
	duration := time.Since(started).Milliseconds()
	var completed domain.AgentSession
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.message_completed", session.ID, nil, func(next *domain.Bootstrap) error {
		current, err := ownedAgentSession(next, session.ID)
		if err != nil {
			return err
		}
		current.Messages = append(current.Messages, domain.AgentMessage{ID: fmt.Sprintf("agent_message_%d", now.UnixNano()), Role: "assistant", Content: reply, DurationMS: duration, CreatedAt: now})
		current.UpdatedAt = now
		completed = *current
		return nil
	})
	return completed, err
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
		session.UpdatedAt = time.Now().UTC()
		updated = *session
		return nil
	})
	respondMutation(w, err, http.StatusOK, updated)
}

func (s *server) deleteAgentSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.store.MutateWorkspace(r.Context(), workspaceKey(r), "agent.session_deleted", id, nil, func(data *domain.Bootstrap) error {
		session, err := ownedAgentSession(data, id)
		if err != nil {
			return err
		}
		for index := range data.AgentSessions {
			if data.AgentSessions[index].ID == session.ID {
				data.AgentSessions = append(data.AgentSessions[:index], data.AgentSessions[index+1:]...)
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
	skill := domain.PersonalAgentSkill{ID: fmt.Sprintf("agent_skill_%d", now.UnixNano()), Name: strings.TrimSpace(input.Name), Instructions: strings.TrimSpace(input.Instructions), CreatedAt: now, UpdatedAt: now}
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
		if (session.ID == id || session.SlugID == id) && session.UserID == data.Viewer.ID {
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
	payload, err := json.Marshal(map[string]any{"model": s.agent.Model, "messages": messages})
	if err != nil {
		return "", fmt.Errorf("could not encode agent request")
	}
	endpoint := strings.TrimRight(s.agent.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("could not create agent request")
	}
	request.Header.Set("Content-Type", "application/json")
	if s.agent.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+s.agent.APIKey)
	}
	client := s.agentClient
	if client == nil {
		timeout := s.agent.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Flow Agent provider is unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("could not read Flow Agent response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Flow Agent provider returned status %d", response.StatusCode)
	}
	var decoded struct {
		Choices []struct {
			Message agentChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Choices) == 0 {
		return "", fmt.Errorf("Flow Agent provider returned an invalid response")
	}
	reply := strings.TrimSpace(decoded.Choices[0].Message.Content)
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

func agentSystemPrompt(workspace string, issues []domain.Issue, skills ...[]domain.PersonalAgentSkill) string {
	var prompt strings.Builder
	prompt.WriteString("You are Flow Agent. Answer questions about the selected issues using only the supplied workspace context. Be concise, distinguish facts from suggestions, and never invent issue state.\n\nWorkspace: ")
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
		prompt.WriteString("\n")
		if description := strings.TrimSpace(issue.Description); description != "" {
			if len(description) > 4000 {
				description = description[:4000]
			}
			prompt.WriteString("  Description: ")
			prompt.WriteString(description)
			prompt.WriteString("\n")
		}
	}
	if len(skills) > 0 && len(skills[0]) > 0 {
		prompt.WriteString("\nActive skills:\n")
		for _, skill := range skills[0] {
			fmt.Fprintf(&prompt, "- %s: %s\n", skill.Name, skill.Instructions)
		}
	}
	return prompt.String()
}
