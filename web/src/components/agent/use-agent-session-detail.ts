import { useCallback, useEffect, useState } from "react";
import { useLatestRef } from "@/hooks/use-latest-ref";
import { getAgentSession } from "@/lib/api";
import type { AgentSession } from "@/types/flow";

/** Waits before the automatic re-reads of a chat whose read failed; after the last one the surface says so. */
export const AGENT_SESSION_DETAIL_RETRY_MS: readonly number[] = [1000, 3000, 8000];

/** The chat could not be read: reads kept failing, or it is gone (deleted, or no longer the viewer's). */
export type AgentSessionDetailFailure = { sessionId: string; kind: "failed" | "gone" };

/**
 * Reads chat `sessionId` whole (GET /api/agent/sessions/{id}) while it is set; `version` (the summary's updatedAt)
 * reads it again when it changes. A read that fails is retried a few times (AGENT_SESSION_DETAIL_RETRY_MS), then
 * `failure` says so until `retry()`. A read made for another chat or an older version is dropped when it lands late.
 */
export function useAgentSessionDetail({
  sessionId,
  version,
  onLoaded,
  retryDelays = AGENT_SESSION_DETAIL_RETRY_MS,
}: {
  sessionId: string | undefined;
  version?: string;
  onLoaded: (session: AgentSession) => void;
  retryDelays?: readonly number[];
}) {
  const [failureState, setFailure] = useState<AgentSessionDetailFailure>();
  const [attempt, setAttempt] = useState(0);
  const loaded = useLatestRef(onLoaded);
  useEffect(() => {
    if (!sessionId) return;
    let cancelled = false;
    let timer: number | undefined;
    const read = (tries: number) => {
      getAgentSession(sessionId).then((full) => {
        if (cancelled) return;
        if (full?.id !== sessionId || !Array.isArray(full.messages)) throw new Error("Unexpected chat response");
        setFailure((current) => (current?.sessionId === sessionId ? undefined : current));
        loaded.current(full);
      }).catch((reason: unknown) => {
        if (cancelled) return;
        if (chatGone(reason)) setFailure({ sessionId, kind: "gone" });
        else if (tries < retryDelays.length) timer = window.setTimeout(() => read(tries + 1), retryDelays[tries]);
        else setFailure({ sessionId, kind: "failed" });
      });
    };
    read(0);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [attempt, loaded, retryDelays, sessionId, version]);
  const failure = failureState && failureState.sessionId === sessionId ? failureState : undefined;
  /** Reads the chat again after the automatic retries gave up. */
  const retry = useCallback(() => {
    setFailure(undefined);
    setAttempt((value) => value + 1);
  }, []);
  return { failure, retry };
}

/** The notice for a chat that could not be read. */
export function agentSessionDetailNotice(failure: AgentSessionDetailFailure) {
  return failure.kind === "gone" ? "This chat is no longer available." : "Couldn't load this chat.";
}

function chatGone(reason: unknown) {
  const status = (reason as { status?: unknown } | null)?.status;
  return status === 403 || status === 404;
}
