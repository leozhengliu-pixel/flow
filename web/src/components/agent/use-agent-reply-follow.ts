import { useCallback, useEffect, useRef, useState } from "react";
import { useLatestRef } from "@/hooks/use-latest-ref";
import { getAgentSession, resolveAgentApproval, stopAgentSession } from "@/lib/api";
import type { AgentMessagePart, AgentSession, AgentToolCall } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { agentReplyInProgress, agentReplySignature } from "./agent-reply-progress";

/** How often a chat surface re-reads a reply that runs on the server without it. */
export const AGENT_REPLY_FOLLOW_INTERVAL_MS = 2000;

/**
 * Reads in a row that may fail before a surface stops re-reading a reply on its own. The reply still shows as running
 * (a connection problem changes nothing in the message; the workspace's Offline indicator says the rest), and it is
 * read again when the browser comes back online, the tab is shown again, or a realtime push arrives.
 */
export const AGENT_REPLY_FOLLOW_MAX_FAILURES = 5;

/** Re-reading a chat's reply stopped: its reads kept failing ("failed", paused), or the chat is gone ("gone"). */
export type AgentReplyFollowLost = { sessionId: string; kind: "failed" | "gone" };

/** The notice for a chat that is gone (deleted, or no longer the viewer's) while its reply was followed. */
export const AGENT_CHAT_GONE_NOTICE = "This chat is no longer available.";

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
  // Reads that failed in a row for one chat; at AGENT_REPLY_FOLLOW_MAX_FAILURES (or a chat that is gone) following stops.
  const failures = useRef<{ sessionId?: string; count: number }>({ count: 0 });
  const [lostState, setLost] = useState<AgentReplyFollowLost>();
  const lost = lostState && lostState.sessionId === id ? lostState : undefined;
  const following = Boolean(enabled && id && !streaming && lost?.kind !== "gone" && agentReplyInProgress(session));
  // Failed reads pause the timer (bounded); the reply keeps its running state until a read says how it ended.
  const polling = following && !lost;
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
    let next: AgentSession;
    try {
      next = await getAgentSession(sessionId);
    } catch (reason) {
      // Only the latest read counts; a chat that is gone, or reads failing again and again, stop the following (bounded).
      if (asked !== generation.current) return;
      if (failures.current.sessionId !== sessionId) failures.current = { sessionId, count: 0 };
      if (chatGone(reason)) setLost({ sessionId, kind: "gone" });
      else if (++failures.current.count >= AGENT_REPLY_FOLLOW_MAX_FAILURES) setLost({ sessionId, kind: "failed" });
      return;
    }
    failures.current = { sessionId, count: 0 };
    setLost(current => current?.sessionId === sessionId ? undefined : current);
    if (asked === generation.current && next) apply(next);
  }, [apply]);

  useEffect(() => {
    if (!polling || !id) return;
    const timer = window.setInterval(() => void read(id), intervalMs);
    return () => window.clearInterval(timer);
  }, [polling, id, intervalMs, read]);

  // Paused after failed reads: back online, or the tab shown again, reads the reply once more (and resumes on success).
  const paused = following && lost?.kind === "failed";
  useEffect(() => {
    if (!paused || !id) return;
    const resume = () => {
      if (document.visibilityState === "hidden") return;
      failures.current = { sessionId: id, count: 0 };
      void read(id);
    };
    window.addEventListener("online", resume);
    document.addEventListener("visibilitychange", resume);
    return () => {
      window.removeEventListener("online", resume);
      document.removeEventListener("visibilitychange", resume);
    };
  }, [id, paused, read]);

  useEffect(() => {
    if (!listening || !id) return;
    const pushed = (event: Event) => {
      const next = (event as CustomEvent<AgentSession>).detail;
      if (next?.id !== id) return;
      generation.current++;
      // News from the server: re-reading works again.
      setLost(current => current?.sessionId === id && current.kind === "failed" ? undefined : current);
      apply(next);
    };
    window.addEventListener(AGENT_SESSION_PUSHED_EVENT, pushed);
    return () => window.removeEventListener(AGENT_SESSION_PUSHED_EVENT, pushed);
  }, [apply, id, listening]);

  /** Re-reads the chat now (after an answer or a Stop, rather than at the next tick). */
  const refresh = useCallback(async () => {
    if (id) await read(id);
  }, [id, read]);

  /**
   * Reads the chat's saved state now and follows it again: after this surface's own stream ended without the reply
   * finishing (a dropped connection, not a Stop). It only reads the conversation; nothing is sent again. Call it once
   * the surface no longer streams (a read is ignored while it does).
   */
  const recover = useCallback(async (sessionId = id) => {
    if (!sessionId || sessionId !== latest.current.session?.id) return;
    failures.current = { sessionId, count: 0 };
    await read(sessionId);
  }, [id, latest, read]);

  /** Stops the reply on the server (it runs there even when this surface streams it) and shows how it ended. */
  const stop = useCallback(async () => {
    if (!id) return;
    await stopAgentSession(id).catch(() => undefined);
    if (!streaming) await refresh();
  }, [id, refresh, streaming]);

  return { following, lost, paused, recover, refresh, stop };
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
  const [failedApproval, setFailedApproval] = useState<{ sessionId: string; call: AgentToolCall; decision: "approve" | "reject" }>();
  const busyRef = useLatestRef<string | undefined>(approvalBusy);
  const callbacks = useLatestRef({ onError, onStale, t });
  const decideToolApproval = useCallback(async (call: AgentToolCall | undefined, decision: "approve" | "reject") => {
    if (!sessionId || !call?.approvalId || busyRef.current) return;
    const approvalId = call.approvalId;
    busyRef.current = approvalId;
    setApprovalBusy(approvalId);
    setFailedApproval(undefined);
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
      setFailedApproval({ sessionId, call, decision });
      callbacks.current.onError(reason instanceof Error && reason.message ? reason.message : callbacks.current.t("Flow Agent is unavailable"));
    }
  }, [sessionId, busyRef, callbacks]);
  const waiting = Boolean(approvalBusy) && parts.some((part) => part.toolCall?.approvalId === approvalBusy && part.toolCall?.status === "pending");
  // Once the reply no longer shows the approval as pending, the card's buttons come back.
  if (approvalBusy && !waiting) setApprovalBusy(undefined);
  const retryApproval = failedApproval && failedApproval.sessionId === sessionId && parts.some(part => part.toolCall?.approvalId === failedApproval.call.approvalId && part.toolCall?.status === "pending")
    ? () => void decideToolApproval(failedApproval.call, failedApproval.decision) : undefined;
  return { approvalBusy, decideToolApproval, retryApproval };
}

/** The parts of every message plus the live stream's (what an approval card can be shown from). */
export function agentShownParts(messages: { parts?: AgentMessagePart[] }[] | undefined, streamParts: AgentMessagePart[] = []) {
  return [...(messages ?? []).flatMap((message) => message.parts ?? []), ...streamParts];
}

/**
 * `next` is an older read of a reply `shown` already has finished (a settled reply never runs again). Any status but
 * "streaming" is settled, so an outcome the server names (failed, interrupted, stopped, completed) counts too.
 */
function settledEarlier(shown: AgentSession | undefined, next: AgentSession) {
  const mine = shown?.messages.at(-1), theirs = next.messages.at(-1);
  return Boolean(mine && theirs && mine.id === theirs.id && mine.role === "assistant" && mine.status !== "streaming" && theirs.status === "streaming");
}

/** The chat was deleted or is no longer the viewer's: reading it again cannot help. */
function chatGone(reason: unknown) {
  const status = (reason as { status?: unknown } | null)?.status;
  return status === 403 || status === 404;
}

/** The approval was already answered (from another surface or tab) or expired: the server no longer holds it. */
function staleAnswer(reason: unknown) {
  const status = (reason as { status?: unknown } | null)?.status;
  return status === 404;
}
