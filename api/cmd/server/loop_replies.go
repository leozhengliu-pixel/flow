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
	"flow/api/internal/store"
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
	id, runID := r.PathValue("id"), r.PathValue("runId")
	data, err := s.loopViewerData(r)
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusForbidden, "You don't have permission to perform this action")
		return
	}
	workspace := data.Workspace.URLKey
	loop := loopByID(&data, id)
	if loop == nil {
		writeError(w, http.StatusNotFound, "loop not found")
		return
	}
	if enabled, ok := data.WorkspaceSettings.FeatureFlags["loops"]; ok && !enabled {
		writeError(w, http.StatusConflict, "Loops are disabled for this workspace")
		return
	}
	if !s.agent.Enabled {
		writeError(w, http.StatusConflict, "Flow Agent is not configured on this server")
		return
	}
	if err := s.checkLoopEditor(&data, *loop); err != nil {
		respondLoopMutation(w, err, http.StatusAccepted, nil)
		return
	}
	userID := data.Viewer.ID
	// The replier's own skills, checked the way new agent chats check them.
	skills := selectedAgentSkills(data.AgentSkills, skillIDs, userID)
	if len(skills) != len(skillIDs) {
		writeError(w, http.StatusBadRequest, "one or more selected skills were not found")
		return
	}
	guardKey := "reply|" + runID
	if !loopGuards.claim(guardKey, false, time.Now()) {
		writeError(w, http.StatusConflict, errLoopReplyBusy.Error())
		return
	}
	limits := s.loopLimits()
	registry := s.loopRunRegistry()
	if err := registry.reserve(workspace, limits); err != nil {
		loopGuards.release(guardKey)
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	}
	abort := func(status int, message string) {
		registry.unreserve(workspace)
		loopGuards.release(guardKey)
		writeError(w, status, message)
	}
	now := time.Now().UTC()
	reply := domain.LoopRunReply{ID: fmt.Sprintf("loop_reply_%d", now.UnixNano()), UserID: userID, Body: body, Status: "running", CreatedAt: now, Attachments: attachments.meta}
	if len(skillIDs) > 0 {
		reply.SkillIDs = skillIDs
	}
	existing, err := s.store.LoopRun(r.Context(), workspace, runID)
	if err != nil || existing.LoopID != id {
		abort(http.StatusNotFound, "loop run not found")
		return
	}
	if existing.Status == "running" || slices.ContainsFunc(existing.Replies, func(item domain.LoopRunReply) bool { return item.Status == "running" }) {
		abort(http.StatusConflict, errLoopReplyBusy.Error())
		return
	}
	// The answer runs under the run's lease, like the run itself.
	owner := s.loopInstanceID()
	if err := s.store.ClaimLoopRunLease(r.Context(), workspace, runID, store.LoopRunLease{Owner: owner, ExpiresAt: now.Add(limits.LeaseTTL)}); err != nil {
		if _, readErr := s.store.LoopRun(r.Context(), workspace, runID); errors.Is(readErr, store.ErrLoopRunNotFound) {
			abort(http.StatusNotFound, "loop run not found")
			return
		}
		abort(http.StatusConflict, errLoopReplyBusy.Error())
		return
	}
	run, err := s.store.UpdateLoopRun(r.Context(), workspace, runID, []store.LoopRunEvent{loopRunEvent("reply_created", map[string]string{"replyId": reply.ID, "userId": userID})}, func(stored *domain.LoopRun) error {
		if stored.LoopID != id {
			return store.ErrLoopRunNotFound
		}
		if stored.Status == "running" || slices.ContainsFunc(stored.Replies, func(item domain.LoopRunReply) bool { return item.Status == "running" }) {
			return errLoopReplyBusy
		}
		stored.Replies = append(stored.Replies, reply)
		return nil
	})
	if err != nil {
		_ = s.store.ReleaseLoopRunLease(context.Background(), workspace, runID, owner)
		switch {
		case errors.Is(err, store.ErrLoopRunNotFound):
			abort(http.StatusNotFound, "loop run not found")
		case errors.Is(err, errLoopReplyBusy):
			abort(http.StatusConflict, err.Error())
		default:
			abort(http.StatusInternalServerError, "Could not save the reply")
		}
		return
	}
	history := loopReplyHistory(run, reply.ID)
	if last := len(history) - 1; last >= 0 {
		history[last].Content += attachments.notes
		history[last].Images = attachments.images
	}
	trigger := loopTrigger{Kind: run.Trigger, EventType: run.EventType, EntityType: run.EntityType, EntityID: run.EntityID, Label: run.TriggerLabel, Reason: loopTriggerReason{Code: run.TriggerReason, Value: run.TriggerValue}}
	current := *loop
	go func() {
		defer loopGuards.release(guardKey)
		answer := reply
		recorder := &loopRunRecorder{s: s, workspace: workspace, run: run, reply: &answer, skills: skills}
		s.runLoopWork(workspace, run.ID, limits, func(ctx context.Context) error {
			return s.executeLoopRun(ctx, workspace, current, trigger, recorder, history...)
		}, func(ctx context.Context, runErr error) {
			s.finishLoopRunReply(ctx, workspace, recorder, runErr)
		})
	}()
	writeJSON(w, http.StatusAccepted, presentLoopRun(run, userID))
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
func (rec *loopRunRecorder) saveReply(events []store.LoopRunEvent) {
	snapshot := *rec.reply
	snapshot.Steps = slices.Clone(rec.reply.Steps)
	snapshot.ToolCalls = slices.Clone(rec.reply.ToolCalls)
	updated, err := rec.s.store.UpdateLoopRun(context.Background(), rec.workspace, rec.run.ID, events, func(run *domain.LoopRun) error {
		stored := loopRunReplyByID(run, snapshot.ID)
		if stored == nil || stored.Status != "running" {
			return store.ErrNoMutation
		}
		*stored = snapshot
		return nil
	})
	if err != nil {
		if !errors.Is(err, store.ErrLoopRunNotFound) {
			log.Printf("Loop reply progress workspace=%s run=%s: %v", rec.workspace, rec.run.ID, err)
		}
		return
	}
	rec.publishProgress(updated)
}

// finishLoopRunReply records how the agent's answer to a reply ended.
func (s *server) finishLoopRunReply(ctx context.Context, workspace string, rec *loopRunRecorder, runErr error) {
	reply := *rec.reply
	now := time.Now().UTC()
	reply.FinishedAt = &now
	reply.Status = "completed"
	if runErr != nil || ctx.Err() != nil {
		if runErr == nil {
			runErr = ctx.Err()
		}
		reply.Status, reply.FailureReason, reply.Error = classifyLoopRunFailure(ctx, runErr, s.loopLimits())
	}
	reply.Output = strings.TrimSpace(reply.Output)
	reply.Steps, reply.ToolCalls = slices.Clone(reply.Steps), slices.Clone(reply.ToolCalls)
	closeRunningToolCalls(reply.ToolCalls)
	events := append(rec.events, loopRunEvent("reply_finished", map[string]string{"replyId": reply.ID, "status": reply.Status, "reason": reply.FailureReason}))
	rec.events = nil
	run, err := s.store.UpdateLoopRun(context.Background(), workspace, rec.run.ID, events, func(run *domain.LoopRun) error {
		stored := loopRunReplyByID(run, reply.ID)
		if stored == nil || stored.Status != "running" {
			return store.ErrNoMutation
		}
		*stored = reply
		return nil
	})
	if err != nil {
		log.Printf("Loop reply finish workspace=%s loop=%s run=%s: %v", workspace, rec.run.LoopID, rec.run.ID, err)
	}
	if err := s.store.ReleaseLoopRunLease(context.Background(), workspace, rec.run.ID, s.loopInstanceID()); err != nil {
		log.Printf("Loop reply lease release workspace=%s run=%s: %v", workspace, rec.run.ID, err)
	}
	if err == nil {
		s.publishLoopRunEvent(workspace, "loop_run.finished", run)
	}
}

func loopRunReplyByID(run *domain.LoopRun, replyID string) *domain.LoopRunReply {
	for index := range run.Replies {
		if run.Replies[index].ID == replyID {
			return &run.Replies[index]
		}
	}
	return nil
}
