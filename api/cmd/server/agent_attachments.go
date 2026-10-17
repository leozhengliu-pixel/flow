package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"flow/api/internal/domain"
	"flow/api/internal/objectstore"
)

// Files sent with an agent chat message (Agent page, floating chat). The composer sends each file's content inline,
// as loop replies do; images and text files are kept in object storage and read from there on every turn, so a
// follow-up or a retry still sees them. The message itself keeps only what the user wrote.

const (
	agentAttachmentMax      = 8
	agentAttachmentMaxBytes = 2 << 20
	// Composer uploads: images are shrunk by the browser first; any other file may be larger.
	agentUploadMaxBytes = 20 << 20
	// Images given to the model per request, newest first; older ones are named only, to bound each turn's cost.
	agentAttachmentImages = 8
)

// saveAgentAttachments stores the readable files of a message and returns what the message records. Content is
// cleared from inputs so the request logged with the mutation does not carry it.
func (s *server) saveAgentAttachments(r *http.Request, inputs []loopRunReplyAttachmentInput) ([]domain.AgentMessageAttachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if len(inputs) > agentAttachmentMax {
		return nil, fmt.Errorf("%w: attach up to %d files", errInvalid, agentAttachmentMax)
	}
	type pending struct {
		attachment domain.AgentMessageAttachment
		content    []byte
	}
	files := make([]pending, 0, len(inputs))
	for index := range inputs {
		input := &inputs[index]
		name := strings.TrimSpace(filepath.Base(strings.TrimSpace(input.Name)))
		if name == "" || name == "." || name == "/" {
			return nil, fmt.Errorf("%w: attachments need a file name", errInvalid)
		}
		contentType := strings.TrimSpace(input.ContentType)
		if contentType == "" {
			contentType = firstNonEmpty(mime.TypeByExtension(strings.ToLower(filepath.Ext(name))), "application/octet-stream")
		}
		content, err := agentAttachmentContent(name, contentType, input.Content)
		if err != nil {
			return nil, err
		}
		size := input.Size
		if content != nil {
			size = int64(len(content))
		}
		if size < 0 || size > agentAttachmentMaxBytes {
			return nil, fmt.Errorf("%w: %s is larger than 2 MB", errInvalid, name)
		}
		files = append(files, pending{attachment: domain.AgentMessageAttachment{Name: name, ContentType: contentType, Size: size}, content: content})
		input.Content = ""
	}
	var storage objectstore.Store
	saved := make([]domain.AgentMessageAttachment, 0, len(files))
	now := time.Now().UTC().UnixNano()
	for index, file := range files {
		if file.content != nil {
			if storage == nil {
				var err error
				if storage, err = s.storage(); err != nil {
					s.deleteAgentAttachments(r.Context(), saved)
					return nil, fmt.Errorf("storage unavailable")
				}
			}
			upload, err := s.checkUploadPolicy(workspaceKey(r), file.attachment.Name, bytes.NewReader(file.content))
			if err != nil {
				s.deleteAgentAttachments(r.Context(), saved)
				return nil, fmt.Errorf("%w: %s", errInvalid, err.Error())
			}
			key := fmt.Sprintf("agent_attachment_%d_%d_%s", now, index, file.attachment.Name)
			if _, err := storage.Put(r.Context(), key, upload, file.attachment.ContentType); err != nil {
				s.deleteAgentAttachments(r.Context(), saved)
				return nil, fmt.Errorf("could not save %s", file.attachment.Name)
			}
			file.attachment.URL = "/uploads/" + key
		}
		saved = append(saved, file.attachment)
	}
	return saved, nil
}

// agentAttachmentContent decodes what is worth keeping: an image's data URL, or a text file's text. Other files are
// recorded by name, type and size only (nil content).
func agentAttachmentContent(name, contentType, content string) ([]byte, error) {
	if content == "" {
		return nil, nil
	}
	if header, data, ok := strings.Cut(content, ","); ok && strings.HasPrefix(header, "data:") && strings.HasSuffix(header, ";base64") {
		if loopAttachmentImageType(contentType) == "" || loopAttachmentImageType(strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")) == "" {
			return nil, nil
		}
		if base64.StdEncoding.DecodedLen(len(data)) > agentAttachmentMaxBytes+3 {
			return nil, fmt.Errorf("%w: %s is larger than 2 MB", errInvalid, name)
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("%w: %s is not a valid image", errInvalid, name)
		}
		return raw, nil
	}
	if loopAttachmentIsText(domain.LoopAttachment{Name: name, ContentType: contentType}) {
		if len(content) > agentAttachmentMaxBytes {
			return nil, fmt.Errorf("%w: %s is larger than 2 MB", errInvalid, name)
		}
		return []byte(content), nil
	}
	return nil, nil
}

// deleteAgentAttachments removes stored files of messages that no longer exist (best effort).
func (s *server) deleteAgentAttachments(ctx context.Context, attachments []domain.AgentMessageAttachment) {
	if len(attachments) == 0 {
		return
	}
	storage, err := s.storage()
	if err != nil {
		return
	}
	for _, attachment := range attachments {
		for _, prefix := range []string{"agent_attachment_", "agent_upload_"} {
			if key, ok := strings.CutPrefix(attachment.URL, "/uploads/"+prefix); ok {
				_ = storage.Delete(context.WithoutCancel(ctx), prefix+key)
			}
		}
	}
}

// agentUploads keeps the attachments that are composer uploads (inline images) or, with keep false, the others.
func agentUploads(attachments []domain.AgentMessageAttachment, keep bool) []domain.AgentMessageAttachment {
	var result []domain.AgentMessageAttachment
	for _, attachment := range attachments {
		if strings.HasPrefix(attachment.URL, "/uploads/agent_upload_") == keep {
			result = append(result, attachment)
		}
	}
	return result
}

// messageAttachments lists the stored files of messages.
func messageAttachments(messages []domain.AgentMessage) []domain.AgentMessageAttachment {
	var attachments []domain.AgentMessageAttachment
	for _, message := range messages {
		attachments = append(attachments, message.Attachments...)
	}
	return attachments
}

// addAgentAttachmentInputs gives the model the files sent with user messages: the newest images as image inputs,
// text files as text within the loop attachment limits, anything else by name, type and size.
func (s *server) addAgentAttachmentInputs(ctx context.Context, messages []agentProviderMessage) {
	storage, err := s.storage()
	if err != nil {
		storage = nil
	}
	// size is the stored size, for files whose message did not say.
	size := func(attachment domain.AgentMessageAttachment) int64 {
		if attachment.Size > 0 || storage == nil || attachment.URL == "" {
			return attachment.Size
		}
		reader, _, stored, err := storage.Open(ctx, strings.TrimPrefix(attachment.URL, "/uploads/"))
		if err != nil {
			return attachment.Size
		}
		reader.Close()
		return max(stored, 0)
	}
	read := func(url string, limit int64) ([]byte, bool) {
		if storage == nil || url == "" {
			return nil, false
		}
		reader, _, _, err := storage.Open(ctx, strings.TrimPrefix(url, "/uploads/"))
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
	images, textBytes := 0, 0
	for index := len(messages) - 1; index >= 0; index-- {
		message := &messages[index]
		if len(message.attachments) == 0 {
			continue
		}
		var notes strings.Builder
		for _, attachment := range message.attachments {
			kind := "file"
			if strings.HasPrefix(strings.ToLower(attachment.ContentType), "video/") {
				kind = "video"
			}
			if imageType := loopAttachmentImageType(attachment.ContentType); imageType != "" && attachment.URL != "" {
				if images >= agentAttachmentImages {
					fmt.Fprintf(&notes, "\n\nAttached image %s (sent earlier; no longer shown).", attachment.Name)
					continue
				}
				// The token's type is client-supplied: only send bytes that really are that image.
				if content, complete := read(attachment.URL, agentAttachmentMaxBytes); complete && loopAttachmentImageType(http.DetectContentType(content)) == imageType {
					message.Images = append(message.Images, agentProviderImage{MediaType: imageType, Data: base64.StdEncoding.EncodeToString(content), Name: attachment.Name})
					images++
					fmt.Fprintf(&notes, "\n\nAttached image %s (shown below).", attachment.Name)
					continue
				}
			}
			if attachment.URL != "" && loopAttachmentIsText(domain.LoopAttachment{Name: attachment.Name, ContentType: attachment.ContentType}) && textBytes < loopAttachmentTextTotal {
				limit := min(int64(loopAttachmentTextBytes), int64(loopAttachmentTextTotal-textBytes))
				if content, complete := read(attachment.URL, limit); len(content) > 0 {
					if !complete {
						content = content[:limit]
					}
					text, suffix := strings.ToValidUTF8(string(content), ""), ""
					if !complete {
						suffix = "\n[truncated]"
					}
					textBytes += len(text)
					fmt.Fprintf(&notes, "\n\nAttached file %s:\n```\n%s%s\n```", attachment.Name, text, suffix)
					continue
				}
			}
			fmt.Fprintf(&notes, "\n\nAttached %s %s (%s, %d bytes).", kind, attachment.Name, attachment.ContentType, size(attachment))
		}
		message.Content += notes.String()
	}
}

// Files pasted or dropped into the composer are uploaded at once (as the reference app does) and the message carries
// them as Markdown: ![name](url) for images, ::video[name](url) and ::file[name](url){size="" type=""} for the rest.
// An upload's key names its uploader, so only they can open it or send it; on send the message's own uploads become
// its attachments and reach the model (images as image inputs, text files inline, others by name).

var (
	agentUploadToken    = regexp.MustCompile(`(!|::video|::file)\[([^\]\n]*)\]\((/uploads/(agent_upload_[0-9a-f]{16}_[^)\s]+))\)(\{[^}\n]*\})?`)
	agentUploadSize     = regexp.MustCompile(`size="(\d{1,12})"`)
	agentUploadType     = regexp.MustCompile(`type="([A-Za-z0-9][A-Za-z0-9!#$&^_.+-]*/[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]*)"`)
	agentUploadNameChar = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	extraBlankLines     = regexp.MustCompile(`\n{3,}`)
)

// agentUploadOwner keys a user's composer uploads without putting their id in a URL.
func agentUploadOwner(userID string) string {
	sum := sha256.Sum256([]byte("agent-upload:" + userID))
	return hex.EncodeToString(sum[:8])
}

func agentAudioFile(contentType, name string) bool {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
		return true
	}
	if mediaType != "" && mediaType != "application/octet-stream" {
		return false
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".wav", ".m4a", ".aac", ".ogg", ".flac":
		return true
	}
	return false
}

func (s *server) uploadAgentFile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAgent(w) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, agentUploadMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "files must be 20 MB or smaller")
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
	name := strings.TrimSpace(filepath.Base(strings.TrimSpace(header.Filename)))
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	declared, _, _ := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if agentAudioFile(declared, name) {
		writeError(w, http.StatusBadRequest, "audio files cannot be attached")
		return
	}
	contentType := strings.ToLower(declared)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = firstNonEmpty(mime.TypeByExtension(strings.ToLower(filepath.Ext(name))), "application/octet-stream")
		if contentType == "application/octet-stream" {
			preview := make([]byte, 512)
			n, _ := io.ReadFull(file, preview)
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				writeError(w, http.StatusBadRequest, "could not read file")
				return
			}
			if sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(preview[:n])); sniffed != "" && sniffed != "text/plain" {
				contentType = sniffed
			}
		}
	}
	if strings.HasPrefix(contentType, "audio/") {
		writeError(w, http.StatusBadRequest, "audio files cannot be attached")
		return
	}
	limit := int64(agentUploadMaxBytes)
	if loopAttachmentImageType(contentType) != "" {
		limit = agentAttachmentMaxBytes
	}
	if header.Size > limit {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("this file must be %d MB or smaller", limit>>20))
		return
	}
	storage, err := s.storage()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage unavailable")
		return
	}
	upload, err := s.checkUploadPolicy(workspaceKey(r), name, file)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	// Keys keep to characters a Markdown link and a URL path carry as they are.
	safe := strings.Trim(agentUploadNameChar.ReplaceAllString(name, "-"), "-")
	if safe == "" {
		safe = "file"
	}
	key := fmt.Sprintf("agent_upload_%s_%d_%s", agentUploadOwner(authUser(r).ID), time.Now().UTC().UnixNano(), safe)
	size, err := storage.Put(r.Context(), key, io.LimitReader(upload, limit+1), contentType)
	if err != nil || size > limit {
		_ = storage.Delete(context.WithoutCancel(r.Context()), key)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("this file must be %d MB or smaller", limit>>20))
		return
	}
	writeJSON(w, http.StatusCreated, domain.AgentMessageAttachment{Name: name, ContentType: contentType, Size: size, URL: "/uploads/" + key})
}

// agentUploadVisible reports whether a composer upload belongs to the user.
func agentUploadVisible(userID, url string) bool {
	rest, ok := strings.CutPrefix(url, "/uploads/agent_upload_")
	return ok && strings.HasPrefix(rest, agentUploadOwner(userID)+"_")
}

// agentMessageUploads lists the user's own uploads a message embeds as images, videos or file cards.
func agentMessageUploads(r *http.Request, message string) []domain.AgentMessageAttachment {
	owner := agentUploadOwner(authUser(r).ID)
	var uploads []domain.AgentMessageAttachment
	for _, match := range agentUploadToken.FindAllStringSubmatch(message, -1) {
		kind, url, key, attrs := match[1], match[3], match[4], match[5]
		if !strings.HasPrefix(strings.TrimPrefix(key, "agent_upload_"), owner+"_") || slices.ContainsFunc(uploads, func(item domain.AgentMessageAttachment) bool { return item.URL == url }) {
			continue
		}
		byExtension := mime.TypeByExtension(strings.ToLower(filepath.Ext(key)))
		if kind == "::video" || byExtension == "" {
			byExtension = map[string]string{".webm": "video/webm", ".mp4": "video/mp4", ".mov": "video/quicktime", ".m4v": "video/x-m4v", ".ogv": "video/ogg"}[strings.ToLower(filepath.Ext(key))]
		}
		upload := domain.AgentMessageAttachment{Name: strings.TrimSpace(match[2]), URL: url}
		switch kind {
		case "!":
			upload.ContentType = "image/png"
			if strings.HasPrefix(byExtension, "image/") {
				upload.ContentType = byExtension
			}
			upload.Name = firstNonEmpty(upload.Name, "image")
		case "::video":
			upload.ContentType = "video/mp4"
			if strings.HasPrefix(byExtension, "video/") {
				upload.ContentType = byExtension
			}
			upload.Name = firstNonEmpty(upload.Name, "video")
		default:
			upload.ContentType = firstNonEmpty(byExtension, "application/octet-stream")
			if found := agentUploadType.FindStringSubmatch(attrs); found != nil {
				upload.ContentType = found[1]
			}
			if found := agentUploadSize.FindStringSubmatch(attrs); found != nil {
				upload.Size, _ = strconv.ParseInt(found[1], 10, 64)
			}
			upload.Name = firstNonEmpty(upload.Name, "file")
		}
		uploads = append(uploads, upload)
		if len(uploads) == agentAttachmentMax {
			break
		}
	}
	return uploads
}

// withStoredSizes records each upload's real size: the size in a ::file token is only the client's word.
func (s *server) withStoredSizes(ctx context.Context, uploads []domain.AgentMessageAttachment) []domain.AgentMessageAttachment {
	storage, err := s.storage()
	if err != nil {
		return uploads
	}
	for index := range uploads {
		if reader, _, size, err := storage.Open(ctx, strings.TrimPrefix(uploads[index].URL, "/uploads/")); err == nil {
			reader.Close()
			uploads[index].Size = size
		}
	}
	return uploads
}

// withoutAgentUploads drops a message's embedded uploads from its text: the model gets them as inputs or notes instead.
func withoutAgentUploads(content string, attachments []domain.AgentMessageAttachment) string {
	if !strings.Contains(content, "](/uploads/agent_upload_") {
		return content
	}
	stripped := agentUploadToken.ReplaceAllStringFunc(content, func(match string) string {
		parts := agentUploadToken.FindStringSubmatch(match)
		if !slices.ContainsFunc(attachments, func(item domain.AgentMessageAttachment) bool { return item.URL == parts[3] }) {
			return match
		}
		// Only a file card owns its trailing attributes.
		if parts[1] != "::file" {
			return parts[5]
		}
		return ""
	})
	return strings.TrimSpace(extraBlankLines.ReplaceAllString(stripped, "\n\n"))
}

// agentUploadsTitle names a chat that opened with uploads alone.
func agentUploadsTitle(uploads []domain.AgentMessageAttachment) string {
	title := ""
	for _, upload := range uploads {
		kind := "File"
		switch {
		case strings.HasPrefix(upload.ContentType, "image/"):
			kind = "Image"
		case strings.HasPrefix(upload.ContentType, "video/"):
			kind = "Video"
		}
		if title != "" && title != kind {
			return "File"
		}
		title = kind
	}
	return firstNonEmpty(title, "File")
}
