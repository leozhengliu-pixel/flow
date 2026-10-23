import { describe, expect, it } from "vitest";
import type { AgentMessage, AgentSession } from "@/types/flow";
import { agentReplyInProgress, agentReplySignature } from "./agent-reply-progress";

const now = Date.parse("2026-10-22T12:00:00Z");
const session = (...messages: AgentMessage[]): AgentSession =>
  ({ id: "chat", slugId: "chat-1", userId: "u", title: "Chat", favorite: false, location: "page", issueIds: [], skillIds: [], messages, createdAt: "2026-10-22T11:00:00Z", updatedAt: "2026-10-22T11:00:00Z" }) as AgentSession;
const question: AgentMessage = { id: "q", role: "user", content: "Delete the branch", createdAt: "2026-10-22T11:59:00Z" };

describe("agentReplyInProgress", () => {
  it("waits on a recent question without a reply", () => {
    expect(agentReplyInProgress(session(question), now)).toBe(true);
    expect(agentReplyInProgress(session({ ...question, createdAt: "2026-10-22T11:00:00Z" }), now)).toBe(false);
  });

  it("follows a reply saved as streaming, however long it waits", () => {
    const streaming: AgentMessage = { id: "r", role: "assistant", content: "Half", status: "streaming", createdAt: "2026-10-22T10:00:00Z", updatedAt: "2026-10-22T11:59:30Z" };
    expect(agentReplyInProgress(session(question, streaming), now)).toBe(true);
    expect(agentReplyInProgress(session(question, { ...streaming, status: undefined }), now)).toBe(false);
    expect(agentReplyInProgress(undefined, now)).toBe(false);
  });
});

describe("agentReplySignature", () => {
  it("changes when the saved reply changes", () => {
    const streaming: AgentMessage = { id: "r", role: "assistant", content: "Half", status: "streaming", parts: [], createdAt: "2026-10-22T11:59:00Z", updatedAt: "2026-10-22T11:59:30Z" };
    const first = agentReplySignature(session(question, streaming));
    expect(agentReplySignature(session(question, streaming))).toBe(first);
    expect(agentReplySignature(session(question, { ...streaming, updatedAt: "2026-10-22T11:59:31Z" }))).not.toBe(first);
    expect(agentReplySignature(session(question, { ...streaming, status: undefined }))).not.toBe(first);
  });
});
