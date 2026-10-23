import type { AgentSession } from "@/types/flow";

/** A question without a reply after this long is not waited on any more (the reply was lost). */
const QUESTION_WAIT_MS = 10 * 60_000;

/**
 * Whether the chat's latest reply is still running on the server while this page does not stream it (the page was
 * reloaded, or the chat is open in another tab): the last question has no reply yet, or the reply is saved as
 * "streaming" (its text, tool calls and any approval or question waiting for the owner so far).
 */
export function agentReplyInProgress(session: AgentSession | undefined, now = Date.now()): boolean {
  const last = session?.messages.at(-1);
  if (!last) return false;
  if (last.role === "assistant") return last.status === "streaming";
  return last.role === "user" && now - Date.parse(last.createdAt) < QUESTION_WAIT_MS;
}

/** Changes whenever the saved reply does, so polling only re-renders on news. */
export function agentReplySignature(session: AgentSession): string {
  const last = session.messages.at(-1);
  if (!last) return `${session.id}:empty`;
  return [last.id, last.role, last.status ?? "", last.updatedAt ?? "", last.parts?.length ?? 0, last.content.length].join(":");
}
