import { useEffect, useRef } from "react";
import { shortRelativeTime } from "@/components/customer-detail/customer-page-model";
import { markAgentSessionRead } from "@/lib/api";
import type { AgentSession } from "@/types/flow";

/** Linear's unread dot: something changed (usually a reply) after the owner last looked. Chats from before read tracking count as read. */
export function agentSessionUnread(session: Pick<AgentSession, "updatedAt" | "lastReadAt">) {
  if (!session.lastReadAt) return false;
  return Date.parse(session.updatedAt) > Date.parse(session.lastReadAt);
}

/** Linear's short history time ("now", "3h", "8d", "1w") through the translation templates ("{count}d"). */
export function formatAgentHistoryTime(value: string, t: (source: string) => string, now = Date.now()) {
  if (!Number.isFinite(Date.parse(value))) return "";
  const short = shortRelativeTime(value, now);
  const match = /^(\d+)(\D+)$/.exec(short);
  return match ? t(`{count}${match[2]}`).replace("{count}", match[1]) : t(short);
}

/** Linear's chat history groups (Today, Yesterday, Last week, Older), newest chats first within each. */
export function groupAgentHistory(sessions: AgentSession[]) {
  const start = new Date();
  start.setHours(0, 0, 0, 0);
  const groups = new Map<string, AgentSession[]>();
  for (const session of sessions) {
    const days = Math.max(0, Math.floor((start.getTime() - new Date(session.updatedAt).setHours(0, 0, 0, 0)) / 86_400_000));
    const label = days === 0 ? "Today" : days === 1 ? "Yesterday" : days < 7 ? "Last week" : "Older";
    groups.set(label, [...(groups.get(label) ?? []), session]);
  }
  return ["Today", "Yesterday", "Last week", "Older"]
    .filter(label => groups.has(label))
    .map(label => ({ label, sessions: groups.get(label)! }));
}

/**
 * Marks the chat on screen read once its reply has settled, so the history list and the chat switcher drop
 * its unread dot. Runs again only when the chat changes after it was marked (e.g. the next reply lands).
 */
export function useMarkAgentSessionRead(
  session: AgentSession | undefined,
  paused: boolean,
  onRead: (session: AgentSession, lastReadAt: string) => void,
) {
  const marked = useRef("");
  const onReadRef = useRef(onRead);
  useEffect(() => {
    onReadRef.current = onRead;
  });
  const id = session?.id;
  const stale = Boolean(session && (!session.lastReadAt || agentSessionUnread(session)));
  const key = session ? `${session.id}:${session.updatedAt}` : "";
  useEffect(() => {
    // Optimistic chats (no slug yet) don't exist on the server.
    if (!id || paused || !stale || !session?.slugId || marked.current === key) return;
    marked.current = key;
    void Promise.resolve()
      .then(() => markAgentSessionRead(id))
      .then((next) => {
        if (next?.lastReadAt) onReadRef.current(next, next.lastReadAt);
      })
      .catch(() => {
        // Try again the next time the chat is shown.
        if (marked.current === key) marked.current = "";
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- keyed on the chat and its latest change
  }, [id, key, paused, stale]);
}
