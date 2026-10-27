import { useCallback, useEffect, useRef, useState } from "react";
import { useLatestRef } from "@/hooks/use-latest-ref";
import { getAgentSession, resolveAgentApproval, stopAgentSession } from "@/lib/api";
import type { AgentMessagePart, AgentSession, AgentToolCall } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { agentReplyInProgress, agentReplySignature } from "./agent-reply-progress";

/** How often a chat surface re-reads a reply that runs on the server without it. */
export const AGENT_REPLY_FOLLOW_INTERVAL_MS = 2000;

/** Window event carrying a chat the realtime channel just re-read (`detail` is the AgentSession). */
export const AGENT_SESSION_PUSHED_EVENT = "flow:agent-session-pushed";

/** Tells every open chat surface that `session` changed on the server (a realtime `agent.message_*` push). */
export function pushAgentSession(session: AgentSession) {
  window.dispatchEvent(new CustomEvent<AgentSession>(AGENT_SESSION_PUSHED_EVENT, { detail: session }));
}

/**
 * Follows a chat's reply while it runs on the server without this surface streaming it: the surface was opened (or
 * the page reloaded) mid-reply, the reply was asked from another surface or tab, or this surface's own stream
 * dropped. The saved reply (status "streaming": text so far, tool calls, waiting approvals and connector questions)
 * is re-read every couple of seconds and on realtime pushes; `onUpdate` receives each newer copy until it finishes.
 * Pushes for the chat also arrive while nothing runs, so a reply started elsewhere is picked up and followed.
 *
 * `streaming` is true while this surface streams the reply itself (its stream is newer than any saved copy).
 */
export function useAgentReplyFollow({
  session,
  streaming,
  enabled = true,
  onUpdate,
  intervalMs = AGENT_REPLY_FOLLOW_INTERVAL_MS,
}: {
  session: AgentSession | undefined;
  streaming: boolean;
  enabled?: boolean;
  onUpdate: (latest: AgentSession) => void;
  intervalMs?: number;
}) {
  const id = session && !session.id.startsWith("pending-") ? session.id : undefined;
  const following = Boolean(enabled && id && !streaming && agentReplyInProgress(session));
  const listening = Boolean(enabled && id && !streaming);
  // Latest values for the timer and listeners, so a new callback or copy never restarts them.
  const latest = useLatestRef({ onUpdate, session, streaming });
  // The signature of the newest copy handed to `onUpdate` (or shown), updated at once so a duplicate push is ignored.
  const signatureRef = useLatestRef(session ? agentReplySignature(session) : "");
  const apply = useCallback((next: AgentSession) => {
    // A read that lands once this surface streams the reply itself, or shows another chat, is older than what it shows.
    if (latest.current.streaming || next.id !== latest.current.session?.id) return;
    const signature = agentReplySignature(next);
    if (signature === signatureRef.current || settledEarlier(latest.current.session, next)) return;
    signatureRef.current = signature;
    latest.current.onUpdate(next);
  }, [latest, signatureRef]);
  // Bumped by every read and push, so a read still in flight never replaces a newer copy.
  const generation = useRef(0);
  const read = useCallback(async (sessionId: string) => {
    const asked = ++generation.current;
    const next = await getAgentSession(sessionId);
    if (asked === generation.current) apply(next);
  }, [apply]);

  useEffect(() => {
    if (!following || !id) return;
    const timer = window.setInterval(() => void read(id).catch(() => undefined), intervalMs);
    return () => window.clearInterval(timer);
  }, [following, id, intervalMs, read]);

  useEffect(() => {
    if (!listening || !id) return;
    const pushed = (event: Event) => {
      const next = (event as CustomEvent<AgentSession>).detail;
      if (next?.id !== id) return;
      generation.current++;
      apply(next);
    };
    window.addEventListener(AGENT_SESSION_PUSHED_EVENT, pushed);
    return () => window.removeEventListener(AGENT_SESSION_PUSHED_EVENT, pushed);
  }, [apply, id, listening]);

  /** Re-reads the chat now (after an answer or a Stop, rather than at the next tick). */
  const refresh = useCallback(async () => {
    if (id) await read(id).catch(() => undefined);
  }, [id, read]);

  /** Stops the reply on the server (it runs there even when this surface streams it) and shows how it ended. */
  const stop = useCallback(async () => {
    if (!id) return;
    await stopAgentSession(id).catch(() => undefined);
    if (!streaming) await refresh();
  }, [id, refresh, streaming]);

  return { following, refresh, stop };
}

/**
 * Answers a reply's tool approvals once: after Approve or Reject the card's buttons stay off until the reply shows the
 * approval resolved (streamed here, or saved when the reply runs elsewhere), so a second click, or a second surface
 * showing a stale copy, never answers it twice. An answer the server no longer takes (answered elsewhere, expired)
 * re-reads the chat through `onStale` instead of failing loudly.
 */
export function useAgentApprovalAnswer({
  sessionId,
  parts,
  onError,
  onStale,
}: {
  sessionId: string | undefined;
  /** Every part the surface shows (saved messages and live stream parts). */
  parts: AgentMessagePart[];
  /** The answer failed (`undefined` clears an earlier error when answering again). */
  onError: (message: string | undefined) => void;
  onStale?: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [approvalBusy, setApprovalBusy] = useState<string>();
  const busyRef = useLatestRef<string | undefined>(approvalBusy);
  const callbacks = useLatestRef({ onError, onStale, t });
  const decideToolApproval = useCallback(async (call: AgentToolCall | undefined, decision: "approve" | "reject") => {
    if (!sessionId || !call?.approvalId || busyRef.current) return;
    const approvalId = call.approvalId;
    busyRef.current = approvalId;
    setApprovalBusy(approvalId);
    callbacks.current.onError(undefined);
    try {
      await resolveAgentApproval(sessionId, approvalId, decision);
    } catch (reason) {
      busyRef.current = undefined;
      setApprovalBusy(undefined);
      if (staleAnswer(reason) && callbacks.current.onStale) {
        await callbacks.current.onStale();
        return;
      }
      callbacks.current.onError(reason instanceof Error && reason.message ? reason.message : callbacks.current.t("Flow Agent is unavailable"));
    }
  }, [sessionId, busyRef, callbacks]);
  const waiting = Boolean(approvalBusy) && parts.some((part) => part.toolCall?.approvalId === approvalBusy && part.toolCall?.status === "pending");
  // Once the reply no longer shows the approval as pending, the card's buttons come back.
  if (approvalBusy && !waiting) setApprovalBusy(undefined);
  return { approvalBusy, decideToolApproval };
}

/** The parts of every message plus the live stream's (what an approval card can be shown from). */
export function agentShownParts(messages: { parts?: AgentMessagePart[] }[] | undefined, streamParts: AgentMessagePart[] = []) {
  return [...(messages ?? []).flatMap((message) => message.parts ?? []), ...streamParts];
}

/** `next` is an older read of a reply `shown` already has finished (a settled reply never runs again). */
function settledEarlier(shown: AgentSession | undefined, next: AgentSession) {
  const mine = shown?.messages.at(-1), theirs = next.messages.at(-1);
  return Boolean(mine && theirs && mine.id === theirs.id && mine.role === "assistant" && !mine.status && theirs.status === "streaming");
}

/** The approval was already answered (from another surface or tab) or expired: the server no longer holds it. */
function staleAnswer(reason: unknown) {
  const status = (reason as { status?: unknown } | null)?.status;
  return status === 404;
}
