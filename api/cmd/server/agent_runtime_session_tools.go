package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"flow/api/internal/domain"
	"flow/api/internal/store"
	"github.com/google/jsonschema-go/jsonschema"
)

// Session tools of a worker run: the tools the in-process agent adds to a turn from what the session holds rather
// than from Flow's tool inventory. /mcp lists them to a run token only while the session has them, and runs them
// with the context the in-process turn gives them:
//
//   - read_file and transcribe_media, while the conversation holds a stored file (agent_file_tools.go): they open
//     only the files sent with the conversation's messages.
//
// The worker offers whatever /mcp lists of these, so Flow alone decides when a run has them.

// agentRunSessionToolDefinitions are the session tools as /mcp publishes them, schemas resolved for checking.
var agentRunSessionToolDefinitions = sync.OnceValue(func() map[string]flowMCPTool {
	tools := map[string]flowMCPTool{}
	for _, definition := range []agentProviderTool{agentFileToolDefinitions[readFileTool], agentFileToolDefinitions[transcribeMediaTool]} {
		var schema jsonschema.Schema
		if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
			panic(fmt.Sprintf("session tool %s: %v", definition.Name, err))
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			panic(fmt.Sprintf("session tool %s: %v", definition.Name, err))
		}
		tools[definition.Name] = flowMCPTool{Name: definition.Name, Access: definition.Access, Description: definition.Description, InputSchema: definition.Parameters, validator: resolved, scalars: map[string]string{}}
	}
	return tools
})

// agentRunSessionToolContext is ctx with what the run's session gives its session tools (the conversation's files).
func (s *server) agentRunSessionToolContext(ctx context.Context, run store.AgentRun) context.Context {
	if session, ok := s.workerChatSession(run); ok {
		ctx = withAgentSessionFiles(ctx, session)
	}
	return ctx
}

// agentRunSessionTools are the session tools the run has now, by name.
func (s *server) agentRunSessionTools(ctx context.Context, run store.AgentRun) []flowMCPTool {
	ctx = s.agentRunSessionToolContext(ctx, run)
	definitions := agentRunSessionToolDefinitions()
	var tools []flowMCPTool
	for _, tool := range agentFileToolList(ctx) {
		if definition, ok := definitions[tool.Name]; ok {
			tools = append(tools, definition)
		}
	}
	return tools
}

// agentRunSessionTool is the session tool name names, when the run has it now.
func (s *server) agentRunSessionTool(ctx context.Context, run store.AgentRun, name string) (flowMCPTool, bool) {
	if _, known := agentRunSessionToolDefinitions()[name]; !known {
		return flowMCPTool{}, false
	}
	tools := s.agentRunSessionTools(ctx, run)
	index := slices.IndexFunc(tools, func(tool flowMCPTool) bool { return tool.Name == name })
	if index < 0 {
		return flowMCPTool{}, false
	}
	return tools[index], true
}

// workerChatSession is the conversation a chat run answers, as stored (its owner's).
func (s *server) workerChatSession(run store.AgentRun) (domain.AgentSession, bool) {
	if _, chat := workerChatInfo(run); !chat {
		return domain.AgentSession{}, false
	}
	principal, err := decodeAgentRunPrincipal(run)
	if err != nil {
		return domain.AgentSession{}, false
	}
	data, ok := s.store.WorkspaceMetadataFields(run.WorkspaceKey, "agentSessions")
	if !ok {
		return domain.AgentSession{}, false
	}
	for _, session := range data.AgentSessions {
		if session.ID == run.SessionID && session.UserID == principal.UserID {
			return session, true
		}
	}
	return domain.AgentSession{}, false
}

// inlineAgentRunImages reads the image inputs of a run's conversation into the payload, as the in-process turn reads
// them: up to each image's limit and, for a file sent with a message, only bytes that really are that image type.
// An image that can no longer be read is left out.
func (s *server) inlineAgentRunImages(ctx context.Context, prompt *agentRunPromptContext) {
	storage, err := s.storage()
	for index := range prompt.Messages {
		message := &prompt.Messages[index]
		images := message.Images[:0]
		for _, image := range message.Images {
			if err != nil || image.Source == "" || image.MaxBytes <= 0 {
				continue
			}
			reader, _, _, openErr := storage.Open(ctx, strings.TrimPrefix(image.Source, "/uploads/"))
			if openErr != nil {
				continue
			}
			content, readErr := io.ReadAll(io.LimitReader(reader, image.MaxBytes+1))
			reader.Close()
			if readErr != nil || int64(len(content)) > image.MaxBytes || image.Sniffed && loopAttachmentImageType(http.DetectContentType(content)) != image.MediaType {
				continue
			}
			images = append(images, agentRunPromptImage{Name: image.Name, MediaType: image.MediaType, Data: base64.StdEncoding.EncodeToString(content)})
		}
		message.Images = images
		if len(images) == 0 {
			message.Images = nil
		}
	}
}
