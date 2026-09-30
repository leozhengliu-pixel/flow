package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Replies continue a finished run's agent conversation from the run page
// (Linear's "Reply…" composer). The agent answers with the loop's current
// instructions and permissions, acting as the loop owner like the run did.

const (
	loopReplyMaxLength = 8000
	// Like the Agent page composer: up to 8 files of at most 2 MB each.
	loopReplyAttachmentMax      = 8
	loopReplyAttachmentMaxBytes = 2 << 20
)

type loopRunReplyInput struct {
	Body string `json:"body"`
	// SkillIDs are the replier's skills (the composer's Skills picker).
	SkillIDs []string `json:"skillIds"`
	// Attachments are the composer's files: text files as text, images as
	// data URLs, anything else by name only.
	Attachments []loopRunReplyAttachmentInput `json:"attachments"`
}

type loopRunReplyAttachmentInput struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Content     string `json:"content"`
}

var errLoopReplyBusy = errors.New("the agent is still working on this run")

// replyLoopRun records the viewer's reply and starts the agent's answer.
func (s *server) replyLoopRun(w http.ResponseWriter, r *http.Request) {
	var input loopRunReplyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	body := strings.TrimSpace(input.Body)
	if body == "" {
		writeError(w, http.StatusBadRequest, "Write a reply first")
		return
	}
	if utf8.RuneCountInString(body) > loopReplyMaxLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Replies are limited to %d characters", loopReplyMaxLength))
		return
	}
	attachments, err := loopReplyAttachments(input.Attachments)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	skillIDs := uniqueAgentIDs(input.SkillIDs)
	workspace := workspaceKey(r)
	id, runID := r.PathValue("id"), r.PathValue("runId")
	metadata, ok := s.store.WorkspaceMetadata(workspace)
	if !ok {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	loop := loopByID(&metadata, id)
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	if enabled, ok := metadata.WorkspaceSettings.FeatureFlags["loops"]; ok && !enabled {
		writeError(w, http.StatusConflict, "Loops are disabled for this workspace")
		return
	}
	if !s.agent.Enabled {
		writeError(w, http.StatusConflict, "Flow Agent is not configured on this server")
		return
	}
	guardKey := "reply|" + runID
	if !loopGuards.claim(guardKey, false, time.Now()) {
		writeError(w, http.StatusConflict, errLoopReplyBusy.Error())
		return
	}
	now := time.Now().UTC()
	reply := domain.LoopRunReply{ID: fmt.Sprintf("loop_reply_%d", now.UnixNano()), Body: body, Status: "running", CreatedAt: now, Attachments: attachments.meta}
	if len(skillIDs) > 0 {
		reply.SkillIDs = skillIDs
	}
	var run domain.LoopRun
	var current domain.Loop
	var skills []domain.PersonalAgentSkill
	err = s.store.MutateWorkspace(r.Context(), workspace, "loop.run_replied", id, map[string]any{"runId": runID, "replyId": reply.ID}, func(data *domain.Bootstrap) error {
		item := loopByID(data, id)
		if item == nil {
			return errNotFound
		}
		if err := s.checkLoopEditor(data, *item); err != nil {
			return err
		}
		index := slices.IndexFunc(data.LoopRuns, func(item domain.LoopRun) bool { return item.ID == runID && item.LoopID == id })
		if index < 0 {
			return errNotFound
		}
		stored := &data.LoopRuns[index]
		if stored.Status == "running" || slices.ContainsFunc(stored.Replies, func(item domain.LoopRunReply) bool { return item.Status == "running" }) {
			return errLoopReplyBusy
		}
		reply.UserID = data.Viewer.ID
		// The replier's own skills, checked the way new agent chats check them.
		skills = selectedAgentSkills(data.AgentSkills, skillIDs, reply.UserID)
		if len(skills) != len(skillIDs) {
			return fmt.Errorf("%w: one or more selected skills were not found", errInvalid)
		}
		stored.Replies = append(stored.Replies, reply)
		run, current = *stored, *item
		run.Replies = slices.Clone(stored.Replies)
		return nil
	})
	if err != nil {
		loopGuards.release(guardKey)
		if errors.Is(err, errLoopReplyBusy) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		respondLoopMutation(w, err, http.StatusAccepted, nil)
		return
	}
	history := loopReplyHistory(run, reply.ID)
	if last := len(history) - 1; last >= 0 {
		history[last].Content += attachments.notes
		history[last].Images = attachments.images
	}
	trigger := loopTrigger{Kind: run.Trigger, EventType: run.EventType, EntityType: run.EntityType, EntityID: run.EntityID, Label: run.TriggerLabel}
	go func() {
		defer loopGuards.release(guardKey)
		ctx, cancel := context.WithTimeout(context.Background(), loopRunTimeout)
		defer cancel()
		answer := reply
		recorder := &loopRunRecorder{s: s, workspace: workspace, run: run, reply: &answer, skills: skills}
		runErr := s.executeLoopRun(ctx, workspace, current, trigger, recorder, history...)
		s.finishLoopRunReply(workspace, run.LoopID, run.ID, answer, runErr)
	}()
	writeJSON(w, http.StatusAccepted, presentLoopRun(run, firstNonEmpty(authUser(r).ID, reply.UserID)))
}

// loopReplyHistory is the conversation after the run's opening prompt: the
// run's answer, then each earlier reply and its answer, then the new reply.
func loopReplyHistory(run domain.LoopRun, replyID string) []agentProviderMessage {
	answer := strings.TrimSpace(run.Output)
	if answer == "" && run.Error != "" {
		answer = "The run stopped with an error: " + run.Error
	}
	if answer == "" {
		answer = "The run finished without a summary."
	}
	history := []agentProviderMessage{{Role: "assistant", Content: answer}}
	for _, reply := range run.Replies {
		content := reply.Body
		if reply.ID != replyID && len(reply.Attachments) > 0 {
			// Earlier replies' files went with their own turn; keep their names.
			names := make([]string, 0, len(reply.Attachments))
			for _, item := range reply.Attachments {
				names = append(names, item.Name)
			}
			content += "\n\n(Attached: " + strings.Join(names, ", ") + ")"
		}
		history = append(history, agentProviderMessage{Role: "user", Content: content})
		if reply.ID == replyID {
			break
		}
		if text := strings.TrimSpace(reply.Output); text != "" {
			history = append(history, agentProviderMessage{Role: "assistant", Content: text})
		} else if reply.Error != "" {
			history = append(history, agentProviderMessage{Role: "assistant", Content: "I could not answer: " + reply.Error})
		}
	}
	return history
}

// loopReplySkillsPrompt adds the replier's selected skills to the loop's
// system prompt, in the same form agent chats use.
func loopReplySkillsPrompt(skills []domain.PersonalAgentSkill) string {
	if len(skills) == 0 {
		return ""
	}
	var prompt strings.Builder
	prompt.WriteString("\n\nActive skills (the person replying selected these; follow them for this reply):\n")
	for _, skill := range skills {
		fmt.Fprintf(&prompt, "- %s: %s\n", skill.Name, skill.Instructions)
	}
	return prompt.String()
}

type loopReplyAttachmentTurn struct {
	notes  string
	images []agentProviderImage
	meta   []domain.LoopRunReplyAttachment
}

// loopReplyAttachments turns the composer's files into what the agent reads
// with the reply: images as image inputs, text files as text (within the
// loop attachment limits), anything else by name, type and size.
func loopReplyAttachments(inputs []loopRunReplyAttachmentInput) (loopReplyAttachmentTurn, error) {
	turn := loopReplyAttachmentTurn{}
	if len(inputs) > loopReplyAttachmentMax {
		return turn, fmt.Errorf("Attach up to %d files", loopReplyAttachmentMax)
	}
	var notes strings.Builder
	textBytes := 0
	for _, input := range inputs {
		name := strings.TrimSpace(filepath.Base(strings.TrimSpace(input.Name)))
		if name == "" || name == "." || name == "/" {
			return turn, fmt.Errorf("Attachments need a file name")
		}
		contentType := strings.TrimSpace(input.ContentType)
		if contentType == "" {
			contentType = firstNonEmpty(mime.TypeByExtension(strings.ToLower(filepath.Ext(name))), "application/octet-stream")
		}
		if input.Size < 0 || input.Size > loopReplyAttachmentMaxBytes {
			return turn, fmt.Errorf("%s is larger than 2 MB", name)
		}
		turn.meta = append(turn.meta, domain.LoopRunReplyAttachment{Name: name, ContentType: contentType, Size: input.Size})
		if header, data, ok := strings.Cut(input.Content, ","); ok && strings.HasPrefix(contentType, "image/") && strings.HasPrefix(header, "data:") && strings.HasSuffix(header, ";base64") {
			imageType := loopAttachmentImageType(strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"))
			if imageType != "" && base64.StdEncoding.DecodedLen(len(data)) <= loopReplyAttachmentMaxBytes+3 && len(turn.images) < loopAttachmentImages {
				if raw, err := base64.StdEncoding.DecodeString(data); err == nil && len(raw) <= loopReplyAttachmentMaxBytes {
					turn.images = append(turn.images, agentProviderImage{MediaType: imageType, Data: data, Name: name})
					fmt.Fprintf(&notes, "\n\nAttached image %s (shown below).", name)
					continue
				}
			}
		} else if input.Content != "" && loopAttachmentIsText(domain.LoopAttachment{Name: name, ContentType: contentType}) && textBytes < loopAttachmentTextTotal {
			if len(input.Content) > loopReplyAttachmentMaxBytes {
				return turn, fmt.Errorf("%s is larger than 2 MB", name)
			}
			limit := min(loopAttachmentTextBytes, loopAttachmentTextTotal-textBytes)
			text, suffix := strings.ToValidUTF8(input.Content, ""), ""
			if len(text) > limit {
				text, suffix = strings.ToValidUTF8(text[:limit], ""), "\n[truncated]"
			}
			textBytes += len(text)
			fmt.Fprintf(&notes, "\n\nAttached file %s:\n```\n%s%s\n```", name, text, suffix)
			continue
		}
		fmt.Fprintf(&notes, "\n\nAttached file %s (%s, %d bytes).", name, contentType, input.Size)
	}
	turn.notes = notes.String()
	return turn, nil
}

// saveReply writes the answer in progress onto its run.
func (rec *loopRunRecorder) saveReply() {
	snapshot := *rec.reply
	snapshot.Steps = slices.Clone(rec.reply.Steps)
	snapshot.ToolCalls = slices.Clone(rec.reply.ToolCalls)
	err := rec.s.store.MutateWorkspace(context.Background(), rec.workspace, "loop.run_progress", rec.run.LoopID, map[string]any{"runId": rec.run.ID, "replyId": snapshot.ID}, func(data *domain.Bootstrap) error {
		stored := loopRunReplyByID(data, rec.run.ID, snapshot.ID)
		if stored == nil {
			return errNotFound
		}
		if stored.Status != "running" {
			return nil
		}
		*stored = snapshot
		return nil
	})
	if err != nil && !errors.Is(err, errNotFound) {
		log.Printf("Loop reply progress workspace=%s run=%s: %v", rec.workspace, rec.run.ID, err)
	}
}

func (s *server) finishLoopRunReply(workspace, loopID, runID string, reply domain.LoopRunReply, runErr error) {
	now := time.Now().UTC()
	reply.FinishedAt = &now
	reply.Status = "completed"
	if runErr != nil {
		reply.Status, reply.Error = "failed", runErr.Error()
	}
	reply.Output = strings.TrimSpace(reply.Output)
	for index := range reply.ToolCalls {
		if reply.ToolCalls[index].Status == "running" {
			reply.ToolCalls[index].Status = "error"
		}
	}
	err := s.store.MutateWorkspace(context.Background(), workspace, "loop.run_reply_finished", loopID, map[string]any{"runId": runID, "replyId": reply.ID, "status": reply.Status}, func(data *domain.Bootstrap) error {
		stored := loopRunReplyByID(data, runID, reply.ID)
		if stored == nil {
			return errNotFound
		}
		*stored = reply
		return nil
	})
	if err != nil {
		log.Printf("Loop reply finish workspace=%s loop=%s run=%s: %v", workspace, loopID, runID, err)
	}
}

func loopRunReplyByID(data *domain.Bootstrap, runID, replyID string) *domain.LoopRunReply {
	for runIndex := range data.LoopRuns {
		if data.LoopRuns[runIndex].ID != runID {
			continue
		}
		for replyIndex := range data.LoopRuns[runIndex].Replies {
			if data.LoopRuns[runIndex].Replies[replyIndex].ID == replyID {
				return &data.LoopRuns[runIndex].Replies[replyIndex]
			}
		}
	}
	return nil
}
