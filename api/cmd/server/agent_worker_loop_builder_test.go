package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"flow/api/internal/domain"
)

// A loop builder conversation runs on the worker as in-process: the prompt carries the draft, the reply starts with
// the loops tools, the chip question (ask_question, served through /mcp while the draft exists and the person follows
// the reply) shows on the reply and the call waits for the answer, the words before the question stay in the reply,
// and save_loop rows are labelled as in-process.
func TestWorkerLoopBuilderConversation(t *testing.T) {
	f := newWorkerChatFixture(t)
	draft := requestJSON[domain.Loop](t, f.handler, http.MethodPost, "/api/loops", map[string]any{"level": "team", "teamId": f.data.Teams[0].ID, "templateId": "autofix-bugs"}, http.StatusCreated)
	events := f.stream("/api/agent/sessions/stream", fmt.Sprintf(`{"message":"Set this loop up","location":"toolbar","loopIds":[%q]}`, draft.ID))
	sessionID := nextEvent(t, events, "session.started").Session.ID
	run := f.mustClaim(t)
	payload := f.payload(t, run)
	if !payload.Chat.LoopBuilder || !payload.Chat.LoopQuestions || !slices.Contains(payload.Chat.Groups, "loops") || !strings.Contains(payload.Context.System, "Loop builder:") || !strings.Contains(payload.Context.System, draft.ID) {
		t.Fatalf("payload chat = %+v", payload.Chat)
	}
	token := f.token(t, run)
	if tools := listedRunTools(t, f.handler, token); tools[loopQuestionTool] == nil || tools["save_loop"] == nil {
		t.Fatal("ask_question or save_loop is not offered to the loop builder run")
	}

	f.post(t, run, map[string]any{"seq": 1, "type": "text.delta", "body": "I've opened a draft and written the instructions for you."})
	f.post(t, run, map[string]any{"seq": 2, "type": "action", "callId": "q1", "content": map[string]any{"action": loopQuestionTool, "parameter": map[string]any{"question": "Which bugs?", "options": []string{"All bugs", "Only P1"}}}})
	done := make(chan map[string]any, 1)
	go func() {
		done <- callRunTool(t, f.handler, token, loopQuestionTool, map[string]any{"question": "Which bugs?", "options": []string{"All bugs", "Only P1"}}, nil)
	}()
	asked := nextEvent(t, events, "elicitation.requested")
	if asked.Part == nil || asked.Part.Elicitation == nil || asked.Part.Elicitation.Message != "Which bugs?" {
		t.Fatalf("question = %+v", asked.Part)
	}
	f.waitReply(t, sessionID, func(message domain.AgentMessage) bool {
		return slices.ContainsFunc(message.Parts, func(part domain.AgentMessagePart) bool { return part.Type == "elicitation" && part.Status == "pending" })
	})
	if status, body := postAgentJSON(t, f.api.URL, "/api/agent/sessions/"+sessionID+"/elicitations/"+asked.Part.ID, `{"action":"accept","content":{"answer":"Only P1"}}`); status != http.StatusOK {
		t.Fatalf("answer = %d %s", status, body)
	}
	select {
	case result := <-done:
		if result["isError"] == true || !strings.Contains(runToolText(result), `"answer":"Only P1"`) {
			t.Fatalf("ask_question result = %v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question did not return the answer")
	}
	f.post(t, run, map[string]any{"seq": 3, "type": "action.result", "callId": "q1", "content": map[string]any{"action": loopQuestionTool, "result": `{"answer":"Only P1"}`}})
	f.post(t, run, map[string]any{"seq": 4, "type": "text.delta", "body": "Applying your answer."})
	saveArgs := map[string]any{"id": draft.ID, "publish": true}
	f.post(t, run, map[string]any{"seq": 5, "type": "action", "callId": "s1", "content": map[string]any{"action": "save_loop", "parameter": saveArgs}})
	f.post(t, run, map[string]any{"seq": 6, "type": "action.result", "callId": "s1", "content": map[string]any{"action": "save_loop", "parameter": saveArgs, "result": `{"id":"` + draft.ID + `","status":"active","published":true}`}})
	f.post(t, run, map[string]any{"seq": 7, "type": "response", "body": "The loop is live."})
	f.complete(t, run, "done")
	drain(t, events)

	final := f.reply(t, sessionID)
	types := partTypes(final.Parts)
	if !slices.Equal(types, []string{"text", "elicitation", "reasoning", "toolCall"}) {
		t.Fatalf("finished reply parts = %v %+v", types, final.Parts)
	}
	want := "I've opened a draft and written the instructions for you.\n\nThe loop is live."
	if final.Content != want || final.Parts[0].Text != want {
		t.Fatalf("reply text = %q / %q", final.Content, final.Parts[0].Text)
	}
	if question := final.Parts[1]; question.Status != "completed" || question.Text != "Only P1" || question.ToolCall == nil || string(question.ToolCall.Result) != `{"answer":"Only P1"}` {
		t.Fatalf("answered question = %+v %+v", question, question.ToolCall)
	}
	if final.Parts[2].Text != "Applying your answer." || final.Parts[3].ToolCall.Title != "Created automation" {
		t.Fatalf("work = %+v / %+v", final.Parts[2], final.Parts[3].ToolCall)
	}

	// A reply nobody streams (the JSON route) asks no questions, as in-process.
	other := requestJSON[domain.Loop](t, f.handler, http.MethodPost, "/api/loops", map[string]any{"level": "team", "teamId": f.data.Teams[0].ID, "templateId": "autofix-bugs"}, http.StatusCreated)
	answered := make(chan int, 1)
	go func() {
		status, _ := postAgentJSON(t, f.api.URL, "/api/agent/sessions", fmt.Sprintf(`{"message":"Set this loop up","location":"toolbar","loopIds":[%q]}`, other.ID))
		answered <- status
	}()
	quiet := f.mustClaim(t)
	if chat := f.payload(t, quiet).Chat; !chat.LoopBuilder || chat.LoopQuestions {
		t.Fatalf("JSON route payload = %+v", chat)
	}
	if tools := listedRunTools(t, f.handler, f.token(t, quiet)); tools[loopQuestionTool] != nil {
		t.Fatal("a reply nobody streams is offered ask_question")
	}
	f.post(t, quiet, map[string]any{"seq": 1, "type": "response", "body": "Done."})
	f.complete(t, quiet, "done")
	if status := <-answered; status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("JSON route = %d", status)
	}
}
