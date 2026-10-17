package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"flow/api/internal/domain"
)

// Chat tools for files attached to a message. The model receives images directly; every other attachment is a
// reference in the message text (name, type, size and an id), and the model reads it on demand:
//   - read_file: documents (PDF, XLSX, DOCX, PPTX, CSV/TSV, text and code) as text;
//   - transcribe_media: audio and video as a transcript or description.
// Both only open attachments recorded on messages of the session being answered, never arbitrary keys or URLs.

const (
	readFileTool        = "read_file"
	transcribeMediaTool = "transcribe_media"
	// agentFileParseTimeout bounds parsing one file.
	agentFileParseTimeout = 30 * time.Second
)

var agentFileToolDefinitions = map[string]agentProviderTool{
	readFileTool: {
		Name:        readFileTool,
		Description: "Read a file attached to this conversation as text: PDF (text per page), Excel .xlsx (a table per sheet), Word .docx, PowerPoint .pptx (text per slide), CSV/TSV (a table), and text or code files. Pass the file's id (or its exact name) from the attachment note in the message. Large files are cut off with a [truncated] note. Audio and video are not read here: use transcribe_media.",
		Parameters:  json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string","description":"The attachment id from the message's attachment note, or the file name"}},"additionalProperties":false}`),
		Access:      "read",
	},
	transcribeMediaTool: {
		Name:        transcribeMediaTool,
		Description: "Transcribe an audio or video file attached to this conversation. Audio returns a transcript with [mm:ss] timestamps; video returns a description of what is visible, including on-screen text. You cannot hear or see attached media yourself, so use this before answering questions about it. Pass the file's id (or its exact name) from the attachment note in the message.",
		Parameters:  json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string","description":"The attachment id from the message's attachment note, or the file name"}},"additionalProperties":false}`),
		Access:      "read",
	},
}

type agentSessionFilesKey struct{}

// withAgentSessionFiles records the stored attachments of the session's messages, which is all the file tools open.
func withAgentSessionFiles(ctx context.Context, session domain.AgentSession) context.Context {
	var files []domain.AgentMessageAttachment
	for _, attachment := range messageAttachments(session.Messages) {
		if attachment.URL != "" && !slices.ContainsFunc(files, func(item domain.AgentMessageAttachment) bool { return item.URL == attachment.URL }) {
			files = append(files, attachment)
		}
	}
	if len(files) == 0 {
		return ctx
	}
	return context.WithValue(ctx, agentSessionFilesKey{}, files)
}

func agentSessionFiles(ctx context.Context) []domain.AgentMessageAttachment {
	files, _ := ctx.Value(agentSessionFilesKey{}).([]domain.AgentMessageAttachment)
	return files
}

func isAgentFileTool(name string) bool {
	name = strings.TrimPrefix(name, "mcp__flow.")
	return name == readFileTool || name == transcribeMediaTool
}

// agentMediaFile reports whether an attachment is audio or video, which only transcribe_media handles.
func agentMediaFile(attachment domain.AgentMessageAttachment) bool {
	mediaType, _, _ := mime.ParseMediaType(attachment.ContentType)
	mediaType = strings.ToLower(mediaType)
	return strings.HasPrefix(mediaType, "audio/") || strings.HasPrefix(mediaType, "video/") || agentAudioFile(mediaType, attachment.Name)
}

func agentVideoFile(attachment domain.AgentMessageAttachment) bool {
	return strings.HasPrefix(strings.ToLower(attachment.ContentType), "video/")
}

// agentFileToolList is the file tools a chat turn gets: read_file when the conversation has a stored attachment that
// is not an image, transcribe_media when it has audio or video.
func agentFileToolList(ctx context.Context) []agentProviderTool {
	var tools []agentProviderTool
	files := agentSessionFiles(ctx)
	if slices.ContainsFunc(files, func(item domain.AgentMessageAttachment) bool {
		return loopAttachmentImageType(item.ContentType) == "" && !agentMediaFile(item)
	}) {
		tools = append(tools, agentFileToolDefinitions[readFileTool])
	}
	if slices.ContainsFunc(files, agentMediaFile) {
		tools = append(tools, agentFileToolDefinitions[transcribeMediaTool])
	}
	return tools
}

// resolveAgentFile finds the session attachment a file argument names: its id (stored URL or key) or exact name.
func resolveAgentFile(ctx context.Context, reference string) (domain.AgentMessageAttachment, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return domain.AgentMessageAttachment{}, fmt.Errorf("file is required")
	}
	files := agentSessionFiles(ctx)
	key := strings.TrimPrefix(strings.TrimPrefix(reference, "/"), "uploads/")
	for _, file := range files {
		if file.URL == reference || strings.TrimPrefix(file.URL, "/uploads/") == key {
			return file, nil
		}
	}
	var named []domain.AgentMessageAttachment
	for _, file := range files {
		if strings.EqualFold(file.Name, reference) || strings.EqualFold(file.Name, filepath.Base(reference)) {
			named = append(named, file)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return domain.AgentMessageAttachment{}, fmt.Errorf("no attachment in this conversation matches %q; use the id from the attachment note", reference)
	}
	ids := make([]string, len(named))
	for index, file := range named {
		ids[index] = file.URL
	}
	return domain.AgentMessageAttachment{}, fmt.Errorf("several attachments are named %q; use one of these ids: %s", reference, strings.Join(ids, ", "))
}

// agentFileToolTitle is the work-log label of a finished file tool call.
func agentFileToolTitle(ctx context.Context, call domain.AgentToolCall) string {
	switch strings.TrimPrefix(call.Name, "mcp__flow.") {
	case transcribeMediaTool:
		return "Transcribed media"
	case readFileTool:
		var args struct {
			File string `json:"file"`
		}
		_ = json.Unmarshal(call.Arguments, &args)
		if file, err := resolveAgentFile(ctx, args.File); err == nil {
			return "Read file " + file.Name
		}
		return "Read file"
	}
	return ""
}

// executeAgentFileTool runs read_file or transcribe_media. Unreadable or unsupported files are a normal result the
// model can relay; only a bad argument or an unreachable store is an error.
func (s *server) executeAgentFileTool(ctx context.Context, call domain.AgentToolCall) ([]byte, error) {
	var args struct {
		File string `json:"file"`
	}
	if len(call.Arguments) > 0 && json.Unmarshal(call.Arguments, &args) != nil {
		return nil, fmt.Errorf("invalid arguments for %s", call.Name)
	}
	file, err := resolveAgentFile(ctx, args.File)
	if err != nil {
		return nil, err
	}
	reply := func(text string) ([]byte, error) {
		return json.Marshal(map[string]any{"file": file.Name, "contentType": file.ContentType, "text": text})
	}
	media := agentMediaFile(file)
	switch tool := strings.TrimPrefix(call.Name, "mcp__flow."); {
	case tool == readFileTool && media:
		return reply(fmt.Sprintf("%s is an audio or video file. Use transcribe_media to get its content.", file.Name))
	case tool == transcribeMediaTool && !media:
		return reply(fmt.Sprintf("%s is not an audio or video file. Use read_file to read it.", file.Name))
	case tool == transcribeMediaTool:
		if !s.agentMediaConfigured(file) {
			return reply(agentMediaNotEnabled)
		}
	}
	data, err := s.readAgentAttachment(ctx, file)
	if err != nil {
		if errors.Is(err, errAgentFileTooLarge) {
			return reply(fmt.Sprintf("%s is larger than %d MB and cannot be read.", file.Name, agentReadFileMaxBytes>>20))
		}
		return nil, err
	}
	if len(data) == 0 {
		return reply(file.Name + " is empty.")
	}
	var text string
	if media {
		text, err = s.transcribeAgentMedia(ctx, file, data)
	} else {
		text, err = parseAgentFileWithin(ctx, file, data)
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return reply(fmt.Sprintf("Could not read %s: %s.", file.Name, strings.TrimRight(err.Error(), ".")))
	}
	return reply(text)
}

// readAgentAttachment loads a stored attachment, at most agentReadFileMaxBytes.
func (s *server) readAgentAttachment(ctx context.Context, file domain.AgentMessageAttachment) ([]byte, error) {
	key := strings.TrimPrefix(file.URL, "/uploads/")
	if key == file.URL || !strings.HasPrefix(key, "agent_attachment_") && !strings.HasPrefix(key, "agent_upload_") {
		return nil, fmt.Errorf("%s is not a stored attachment", file.Name)
	}
	storage, err := s.storage()
	if err != nil {
		return nil, fmt.Errorf("storage unavailable")
	}
	reader, _, size, err := storage.Open(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%s is no longer available", file.Name)
	}
	defer reader.Close()
	if size > agentReadFileMaxBytes {
		return nil, errAgentFileTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(reader, agentReadFileMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("could not read %s", file.Name)
	}
	if len(data) > agentReadFileMaxBytes {
		return nil, errAgentFileTooLarge
	}
	return data, nil
}

// agentFileParseSlots bounds concurrent parses, including ones abandoned after a timeout.
var agentFileParseSlots = make(chan struct{}, 4)

// parseAgentFileWithin parses off the request goroutine so a hostile file cannot hold the turn past the timeout.
func parseAgentFileWithin(ctx context.Context, file domain.AgentMessageAttachment, data []byte) (string, error) {
	type result struct {
		text string
		err  error
	}
	// A timed-out parse keeps running, so cap how many may run at once.
	select {
	case agentFileParseSlots <- struct{}{}:
	default:
		return "", fmt.Errorf("too many files are being read, try again shortly")
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-agentFileParseSlots }()
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- result{err: fmt.Errorf("the file could not be parsed")}
			}
		}()
		text, err := parseAgentFile(file.Name, file.ContentType, data)
		done <- result{text, err}
	}()
	timer := time.NewTimer(agentFileParseTimeout)
	defer timer.Stop()
	select {
	case outcome := <-done:
		return outcome.text, outcome.err
	case <-timer.C:
		return "", fmt.Errorf("reading took too long")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
