import type { AgentSession } from "@/types/flow";

/** The page entity the bottom-right history popover lists "Related to this …" chats for. */
export type ChatHistoryEntity = { type: "issue" | "project" | "initiative" | "document"; id: string };

export type ChatHistorySection = {
  id: string;
  /** English source label; `count` fills "{count} weeks ago". */
  label: string;
  count?: number;
  /** Set on the "Related to this …" section. */
  entity?: ChatHistoryEntity["type"];
  sessions: AgentSession[];
};

const DAY = 86_400_000;
/** Older than this many calendar weeks the chats land in "Older". */
const MAX_WEEKS_AGO = 8;

/** Chats the popover can open: saved ones (they have a slug), minus loop builder conversations. */
export function chatHistorySessions(sessions: AgentSession[] | undefined) {
  return (sessions ?? [])
    .filter((session) => session.slugId && !session.loopIds?.length)
    .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt));
}

export function isChatRelatedTo(session: AgentSession, entity: ChatHistoryEntity) {
  const ids = ({
    issue: session.issueIds,
    project: session.projectIds,
    initiative: session.initiativeIds,
    document: session.documentIds,
  } as const)[entity.type];
  return Boolean(ids?.includes(entity.id));
}

/** Local calendar day number, so DST shifts never move a chat across a day boundary. */
function dayNumber(date: Date) {
  return Math.floor(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) / DAY);
}

/**
 * The reference app's chat history groups by calendar week: "Today", "Yesterday", "This week" (earlier days of the current
 * Monday-first week), "Last week", then "2 weeks ago", "3 weeks ago", …
 */
export function chatHistoryGroup(updatedAt: string, now = new Date()): Pick<ChatHistorySection, "id" | "label" | "count"> {
  const date = new Date(updatedAt);
  if (Number.isNaN(date.getTime())) return { id: "older", label: "Older" };
  const today = dayNumber(now);
  const days = Math.max(0, today - dayNumber(date));
  if (days === 0) return { id: "today", label: "Today" };
  if (days === 1) return { id: "yesterday", label: "Yesterday" };
  const sinceMonday = (now.getDay() + 6) % 7;
  // `days` counts back from today; the current week began `sinceMonday` days ago.
  const week = days <= sinceMonday ? 0 : Math.ceil((days - sinceMonday) / 7);
  if (week === 0) return { id: "this-week", label: "This week" };
  if (week === 1) return { id: "last-week", label: "Last week" };
  if (week <= MAX_WEEKS_AGO) return { id: `weeks-${week}`, label: "{count} weeks ago", count: week };
  return { id: "older", label: "Older" };
}

/** Plain chat title for the list, falling back to the empty-chat name. */
export function chatHistoryTitle(session: AgentSession, fallback: string) {
  return session.title?.trim() || fallback;
}

/**
 * The popover's rows: with no query, "Related to this …" first (when the page is an entity) then the time groups
 * newest first; with a query one flat list ranked by where the query first appears in the title (earlier first, newest
 * first on ties), without headings. Related chats are not repeated in the time groups.
 */
export function buildChatHistory(
  sessions: AgentSession[],
  options: { query?: string; related?: ChatHistoryEntity; now?: Date; fallbackTitle?: string } = {},
): { sections: ChatHistorySection[]; flat: boolean; rows: AgentSession[] } {
  const query = options.query?.trim().toLowerCase() ?? "";
  const fallback = options.fallbackTitle ?? "New chat";
  if (query) {
    const ranked = sessions
      .map((session, order) => ({ session, order, at: chatHistoryTitle(session, fallback).toLowerCase().indexOf(query) }))
      .filter((entry) => entry.at >= 0)
      .sort((a, b) => a.at - b.at || a.order - b.order)
      .map((entry) => entry.session);
    return { sections: [{ id: "results", label: "", sessions: ranked }], flat: true, rows: ranked };
  }
  const sections: ChatHistorySection[] = [];
  const related = options.related ? sessions.filter((session) => isChatRelatedTo(session, options.related!)) : [];
  if (related.length && options.related) sections.push({ id: "related", label: "Related to this {type}", entity: options.related.type, sessions: related });
  const relatedIds = new Set(related.map((session) => session.id));
  for (const session of sessions) {
    if (relatedIds.has(session.id)) continue;
    const group = chatHistoryGroup(session.updatedAt, options.now);
    const last = sections[sections.length - 1];
    if (last && last.id === group.id) last.sessions.push(session);
    else sections.push({ ...group, sessions: [session] });
  }
  return { sections, flat: false, rows: sections.flatMap((section) => section.sessions) };
}
