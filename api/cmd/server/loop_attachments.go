package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"flow/api/internal/domain"
)

// Files attached to "Ask Flow to build the loop": uploaded first, then claimed
// by POST /api/loops (attachmentIds) and given to the loop builder's first
// message — images as image inputs, text files as extracted text.

const (
	loopAttachmentMaxBytes    = 20 << 20
	loopAttachmentUnusedTTL   = 24 * time.Hour
	loopAttachmentImageBytes  = 5 << 20
	loopAttachmentImages      = 4
	loopAttachmentTextBytes   = 100 << 10
	loopAttachmentTextTotal   = 200 << 10
	loopAttachmentPendingKeep = 200
)

func (s *server) uploadLoopAttachment(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, loopAttachmentMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(loopAttachmentMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid attachment")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	upload, policyErr := s.checkUploadPolicy(workspaceKey(r), header.Filename, file)
	if policyErr != nil {
		writeError(w, http.StatusForbidden, policyErr.Error())
		return
	}
	now := time.Now().UTC()
	attachmentID := fmt.Sprintf("loop_attachment_%d", now.UnixNano())
	name := filepath.Base(strings.TrimSpace(header.Filename))
	if name == "." || name == "/" || name == "" {
		name = "attachment"
	}
	objectKey := attachmentID + "_" + name
	contentType := header.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		if guessed := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); guessed != "" {
			contentType = guessed
		}
	}
	size, err := storage.Put(r.Context(), objectKey, io.LimitReader(upload, loopAttachmentMaxBytes+1), contentType)
	if err != nil || size > loopAttachmentMaxBytes {
		_ = storage.Delete(r.Context(), objectKey)
		if err == nil {
			writeError(w, http.StatusRequestEntityTooLarge, "attachment exceeds 20 MB")
		} else {
			writeError(w, http.StatusInternalServerError, "upload failed")
		}
		return
	}
	attachment := domain.LoopAttachment{ID: attachmentID, Name: name, ContentType: contentType, Size: size, URL: "/uploads/" + objectKey, CreatedAt: now}
	var expired []string
	err = s.store.MutateWorkspace(r.Context(), workspaceKey(r), "loop.attachment_uploaded", attachmentID, map[string]string{"attachmentId": attachmentID}, func(data *domain.Bootstrap) error {
		attachment.CreatorID = data.Viewer.ID
		if attachment.CreatorID == "" {
			attachment.CreatorID = authUser(r).ID
		}
		// Drop uploads that were never used for a loop.
		pending := 0
		data.LoopAttachments = slices.DeleteFunc(data.LoopAttachments, func(item domain.LoopAttachment) bool {
			if item.LoopID != "" {
				return false
			}
			pending++
			if now.Sub(item.CreatedAt) > loopAttachmentUnusedTTL || pending > loopAttachmentPendingKeep {
				expired = append(expired, strings.TrimPrefix(item.URL, "/uploads/"))
				return true
			}
			return false
		})
		data.LoopAttachments = append(data.LoopAttachments, attachment)
		return nil
	})
	if err != nil {
		_ = storage.Delete(r.Context(), objectKey)
	} else {
		for _, key := range expired {
			_ = storage.Delete(context.WithoutCancel(r.Context()), key)
		}
	}
	respondMutation(w, err, http.StatusCreated, attachment)
}

// claimLoopAttachments moves the viewer's unused uploads onto a loop.
func claimLoopAttachments(data *domain.Bootstrap, ids []string, loopID string) ([]domain.LoopAttachment, error) {
	claimed := []domain.LoopAttachment{}
	for _, id := range ids {
		index := slices.IndexFunc(data.LoopAttachments, func(item domain.LoopAttachment) bool {
			return item.ID == id && item.LoopID == "" && (data.Viewer.ID == "" || item.CreatorID == data.Viewer.ID)
		})
		if index < 0 {
			return nil, fmt.Errorf("%w: unknown attachment %s", errInvalid, id)
		}
		if slices.ContainsFunc(claimed, func(item domain.LoopAttachment) bool { return item.ID == id }) {
			continue
		}
		data.LoopAttachments[index].LoopID = loopID
		claimed = append(claimed, data.LoopAttachments[index])
	}
	return claimed, nil
}

// loopAttachmentVisible reports whether an upload belongs to a loop (or a
// pending loop upload) in the workspace.
func loopAttachmentVisible(data domain.Bootstrap, userID, url string) bool {
	for _, loop := range data.Loops {
		if slices.ContainsFunc(loop.Attachments, func(item domain.LoopAttachment) bool { return item.URL == url }) {
			return true
		}
	}
	return slices.ContainsFunc(data.LoopAttachments, func(item domain.LoopAttachment) bool {
		return item.URL == url && (item.LoopID != "" || item.CreatorID == userID)
	})
}

func loopAttachmentImageType(contentType string) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch strings.ToLower(mediaType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return strings.ToLower(mediaType)
	}
	return ""
}

func loopAttachmentIsText(attachment domain.LoopAttachment) bool {
	mediaType, _, _ := mime.ParseMediaType(attachment.ContentType)
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml" || mediaType == "application/x-yaml" || mediaType == "application/yaml" {
		return true
	}
	switch strings.ToLower(filepath.Ext(attachment.Name)) {
	case ".md", ".markdown", ".mdx", ".txt", ".csv", ".tsv", ".json", ".xml", ".yaml", ".yml", ".log", ".html", ".htm":
		return true
	}
	return false
}

// addLoopAttachmentInputs adds the attachments of the conversation's loop
// drafts to the first user message the model sees.
func (s *server) addLoopAttachmentInputs(ctx context.Context, data domain.Bootstrap, session domain.AgentSession, messages []agentProviderMessage) {
	attachments := []domain.LoopAttachment{}
	for _, loop := range sessionLoops(data, session) {
		attachments = append(attachments, loop.Attachments...)
	}
	if len(attachments) == 0 || len(session.Messages) == 0 || session.Messages[0].Role != "user" {
		return
	}
	// Only when the first user message is still in the provider history.
	target := -1
	for index, message := range messages {
		if message.Role == "user" && message.ToolResult == nil {
			if message.Content == session.Messages[0].Content {
				target = index
			}
			break
		}
	}
	if target < 0 {
		return
	}
	storage, err := s.storage()
	if err != nil {
		return
	}
	var notes strings.Builder
	textBytes := 0
	for _, attachment := range attachments {
		key := strings.TrimPrefix(attachment.URL, "/uploads/")
		read := func(limit int64) ([]byte, bool) {
			reader, _, _, err := storage.Open(ctx, key)
			if err != nil {
				return nil, false
			}
			defer reader.Close()
			content, err := io.ReadAll(io.LimitReader(reader, limit+1))
			if err != nil {
				return nil, false
			}
			return content, int64(len(content)) <= limit
		}
		if imageType := loopAttachmentImageType(attachment.ContentType); imageType != "" && attachment.Size <= loopAttachmentImageBytes && len(messages[target].Images) < loopAttachmentImages {
			if content, complete := read(loopAttachmentImageBytes); complete {
				messages[target].Images = append(messages[target].Images, agentProviderImage{MediaType: imageType, Data: base64.StdEncoding.EncodeToString(content), Name: attachment.Name})
				fmt.Fprintf(&notes, "\n\nAttached image %s (shown below).", attachment.Name)
				continue
			}
		}
		if loopAttachmentIsText(attachment) && textBytes < loopAttachmentTextTotal {
			limit := min(int64(loopAttachmentTextBytes), int64(loopAttachmentTextTotal-textBytes))
			if content, complete := read(limit); len(content) > 0 {
				if !complete {
					content = content[:limit]
				}
				text := strings.ToValidUTF8(string(content), "")
				if !utf8.ValidString(text) {
					continue
				}
				textBytes += len(text)
				suffix := ""
				if !complete {
					suffix = "\n[truncated]"
				}
				fmt.Fprintf(&notes, "\n\nAttached file %s:\n```\n%s%s\n```", attachment.Name, text, suffix)
				continue
			}
		}
		fmt.Fprintf(&notes, "\n\nAttached file %s (%s, %d bytes).", attachment.Name, firstNonEmpty(attachment.ContentType, "application/octet-stream"), attachment.Size)
	}
	messages[target].Content += notes.String()
}
