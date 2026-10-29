package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
//   - ask_question, while a loop builder conversation holds a draft and the person follows the reply (loop_builder.go):
//     the call shows the chip question on the reply and waits for the answer (or a Stop), outside the tool budget.
//   - fetch_url, and web_search with a configured search provider, for a chat or a comment-thread run while the
//     workspace lets Flow Agent use the web (loop_web.go; a loop run has them as its loop's tools).
//
// The worker offers whatever /mcp lists of these, so Flow alone decides when a run has them.

// agentRunSessionToolDefinitions are the session tools as /mcp publishes them, schemas resolved for checking.
var agentRunSessionToolDefinitions = sync.OnceValue(func() map[string]flowMCPTool {
	tools := map[string]flowMCPTool{}
	for _, definition := range append([]agentProviderTool{agentFileToolDefinitions[readFileTool], agentFileToolDefinitions[transcribeMediaTool], loopQuestionToolDefinition}, webToolDefinitions...) {
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

// agentRunSessionToolContext is ctx with what the run's session gives its session tools (the conversation's files,
// the web).
func (s *server) agentRunSessionToolContext(ctx context.Context, run store.AgentRun) context.Context {
	if session, ok := s.workerChatSession(run); ok {
		ctx = withAgentSessionFiles(ctx, session)
	}
	if s.agentRunWebAccess(run) {
		ctx = withAgentWebTools(ctx)
	}
	return ctx
}

// agentRunWebAccess reports whether a chat or built-in session run may use the web now: the workspace lets Flow
// Agent use it (checked at every call, as an admin may turn it off).
func (s *server) agentRunWebAccess(run store.AgentRun) bool {
	_, chat := workerChatInfo(run)
	_, thread := workerThreadInfo(run)
	if !chat && !thread {
		return false
	}
	metadata, ok := s.store.WorkspaceSettingsMetadata(run.WorkspaceKey)
	return ok && agentWebAccess(metadata.WorkspaceSettings)
}

// agentRunHostedWebSearch reports whether a run should search with its model provider's hosted search, as an
// in-process turn does without a configured search provider (FLOW_AGENT_NATIVE_WEB_SEARCH); the worker adds the
// hosted tool when its protocol has one (OpenAI Responses, Anthropic Messages).
func (s *server) agentRunHostedWebSearch(webAccess bool) bool {
	return webAccess && s.agent.NativeWebSearch && !s.webSearchAvailable() && !s.agentNativeWebSearchOff.Load()
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
	if s.workerChatAsksLoopQuestions(run) {
		tools = append(tools, definitions[loopQuestionTool])
	}
	if agentWebToolsEnabled(ctx) {
		for _, tool := range s.agentWebToolList() {
			tools = append(tools, definitions[tool.Name])
		}
	}
	return tools
}

// workerChatAsksLoopQuestions reports whether a chat run may ask the loop builder's chip questions: its reply was
// asked for by a request that follows it (as in-process, only a streamed reply asks) and the conversation still
// builds a draft loop.
func (s *server) workerChatAsksLoopQuestions(run store.AgentRun) bool {
	info, chat := workerChatInfo(run)
	if !chat || !info.LoopQuestions {
		return false
	}
	session, ok := s.workerChatSession(run)
	if !ok || len(session.LoopIDs) == 0 {
		return false
	}
	data, ok := s.store.WorkspaceMetadataFields(run.WorkspaceKey, "loops")
	return ok && sessionHasDraftLoop(data, session)
}

// callMCPRunLoopQuestion answers an ask_question call of a chat run: the question is shown on the reply (as a
// connector's question is) and the call returns the answer, a skip, or a cancel when the reply is stopped.
func (s *server) callMCPRunLoopQuestion(w http.ResponseWriter, r *http.Request, id any, grant *agentRunGrant, args map[string]any) {
	arguments, err := json.Marshal(args)
	if err != nil {
		s.writeMCPToolResult(w, r, id, nil, fmt.Errorf("invalid arguments"))
		return
	}
	ctx, cancel := s.agentRunStopContext(r.Context(), grant.run.ID)
	defer cancel()
	call := domain.AgentToolCall{ID: "run_" + grant.run.ID, Name: loopQuestionTool, Arguments: arguments}
	content, isError := s.askLoopQuestionIn(ctx, grant.run.WorkspaceKey, grant.run.SessionID, grant.principal.UserID, call, func(part domain.AgentMessagePart, kind string) error {
		s.workerChatElicitationPart(context.WithoutCancel(ctx), grant.run, part, kind)
		return nil
	})
	if isError {
		s.writeMCPToolResult(w, r, id, nil, errors.New(content))
		return
	}
	var result any = content
	_ = json.Unmarshal([]byte(content), &result)
	s.writeMCPToolResult(w, r, id, result, nil)
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
