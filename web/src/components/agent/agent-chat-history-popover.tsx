import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { useI18n } from "@/i18n/i18n";
import { listAgentSessions } from "@/lib/api";
import type { AgentSession } from "@/types/flow";
import {
  buildChatHistory,
  chatHistorySessions,
  chatHistoryTitle,
  type ChatHistoryEntity,
  type ChatHistorySection,
} from "./agent-chat-history-model";
import { AgentOriginBadge } from "./agent-origin-badge";
import { agentSessionUnread, formatAgentHistoryTime } from "./agent-read-state";
import styles from "./agent-chat-history-popover.module.css";

/** Gap between the history button and the popover above it. */
const ANCHOR_GAP = 4;

/**
 * The reference app's bottom-right "Chat history" popover: a search field over the chats grouped by time ("Today", "Last week",
 * "2 weeks ago", …), with "Related to this …" first on an issue, project, initiative or document page. It opens above
 * the history button and never navigates: choosing a chat hands it to `onSelect`, which opens it in the floating chat.
 */
export function AgentChatHistoryPopover({
  open,
  anchorRef,
  sessions,
  related,
  toolbarSessionIds,
  onSelect,
  onClose,
}: {
  open: boolean;
  /** The history button; the popover sits above it, right-aligned, and clicks on it do not count as outside clicks. */
  anchorRef: RefObject<HTMLElement | null>;
  sessions: AgentSession[] | undefined;
  /** The page entity, to list its chats first. */
  related?: ChatHistoryEntity;
  /** Chats currently open in the toolbar chat; they carry a "Toolbar" label. */
  toolbarSessionIds?: string[];
  onSelect: (session: AgentSession) => void;
  /** `restoreFocus` is set when the popover closed from the keyboard, so focus returns to the history button. */
  onClose: (options?: { restoreFocus?: boolean }) => void;
}) {
  if (!open) return null;
  return (
    <ChatHistoryDialog
      anchorRef={anchorRef}
      onClose={onClose}
      onSelect={onSelect}
      related={related}
      sessions={sessions}
      toolbarSessionIds={toolbarSessionIds}
    />
  );
}

function ChatHistoryDialog({
  anchorRef,
  sessions,
  related,
  toolbarSessionIds,
  onSelect,
  onClose,
}: Omit<Parameters<typeof AgentChatHistoryPopover>[0], "open">) {
  const { t } = useI18n();
  const listId = useId();
  const dialogRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const viewportRef = useRef<HTMLDivElement>(null);
  const [query, setQuery] = useState("");
  const [focusedId, setFocusedId] = useState<string>();
  const [position, setPosition] = useState<CSSProperties>();
  const [fade, setFade] = useState({ top: false, bottom: false });

  // Some pages load the workspace without the viewer's chats (project and issue pages), so ask for them on open.
  const [listed, setListed] = useState<AgentSession[]>();
  useEffect(() => {
    let active = true;
    void Promise.resolve()
      .then(() => listAgentSessions())
      .then((next) => { if (active && Array.isArray(next)) setListed(next); })
      .catch(() => undefined);
    return () => { active = false; };
  }, []);
  const sourceSessions = useMemo(() => {
    const byId = new Map((sessions ?? []).map((item) => [item.id, item]));
    for (const item of listed ?? []) {
      const known = byId.get(item.id);
      if (!known || Date.parse(item.updatedAt) > Date.parse(known.updatedAt)) byId.set(item.id, item);
    }
    return chatHistorySessions([...byId.values()]);
  }, [sessions, listed]);
  const newChat = t("New chat");
  const history = useMemo(
    () => buildChatHistory(sourceSessions, { query, related, fallbackTitle: newChat }),
    [sourceSessions, query, related, newChat],
  );
  const toolbarIds = useMemo(() => new Set(toolbarSessionIds), [toolbarSessionIds]);
  const rowId = useCallback((id: string) => `${listId}-${id}`, [listId]);

  // Right edge on the button's right edge, bottom edge a little above its top edge.
  useLayoutEffect(() => {
    const place = () => {
      const rect = anchorRef.current?.getBoundingClientRect();
      if (!rect) return;
      setPosition({
        right: Math.max(0, window.innerWidth - rect.right),
        bottom: Math.max(0, window.innerHeight - rect.top + ANCHOR_GAP),
      });
    };
    place();
    window.addEventListener("resize", place);
    return () => window.removeEventListener("resize", place);
  }, [anchorRef]);

  useEffect(() => {
    inputRef.current?.focus({ preventScroll: true });
  }, []);

  // Clicking anywhere outside closes it; the history button toggles it by itself.
  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (!target || dialogRef.current?.contains(target) || anchorRef.current?.contains(target)) return;
      onClose();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    return () => document.removeEventListener("pointerdown", onPointerDown, true);
  }, [anchorRef, onClose]);

  const updateFade = useCallback(() => {
    const viewport = viewportRef.current;
    if (!viewport) return;
    const top = viewport.scrollTop > 0;
    const bottom = viewport.scrollTop + viewport.clientHeight < viewport.scrollHeight - 1;
    setFade((current) => (current.top === top && current.bottom === bottom ? current : { top, bottom }));
  }, []);
  useLayoutEffect(updateFade, [updateFade, history, position]);

  const focusRow = useCallback((id: string | undefined, scroll: boolean) => {
    setFocusedId(id);
    if (!id || !scroll) return;
    const rows = dialogRef.current?.querySelectorAll<HTMLElement>("[data-row-id]") ?? [];
    Array.from(rows).find((element) => element.dataset.rowId === id)?.scrollIntoView?.({ block: "nearest" });
  }, []);

  const rows = history.rows;
  const move = (step: 1 | -1) => {
    if (!rows.length) return;
    const index = rows.findIndex((session) => session.id === focusedId);
    const next = index < 0 ? (step === 1 ? 0 : rows.length - 1) : (index + step + rows.length) % rows.length;
    focusRow(rows[next].id, true);
  };

  const choose = (session: AgentSession) => {
    onClose();
    onSelect(session);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.nativeEvent.isComposing) return;
    switch (event.key) {
      case "Escape":
        event.preventDefault();
        event.stopPropagation();
        onClose({ restoreFocus: true });
        break;
      case "ArrowDown":
        event.preventDefault();
        move(1);
        break;
      case "ArrowUp":
        event.preventDefault();
        move(-1);
        break;
      case "Tab":
        // Focus stays in the search field; Tab walks the list like the arrow keys.
        event.preventDefault();
        move(event.shiftKey ? -1 : 1);
        break;
      case "Home":
        if (!rows.length) break;
        event.preventDefault();
        focusRow(rows[0].id, true);
        break;
      case "End":
        if (!rows.length) break;
        event.preventDefault();
        focusRow(rows[rows.length - 1].id, true);
        break;
      case "Enter": {
        event.preventDefault();
        const session = rows.find((item) => item.id === focusedId);
        if (session) choose(session);
        break;
      }
    }
  };

  const sectionLabel = (section: ChatHistorySection) => {
    if (section.entity) return t(`Related to this ${section.entity}`);
    if (section.count != null) return t(section.label).replace("{count}", String(section.count));
    return t(section.label);
  };

  const renderRow = (session: AgentSession) => {
    const unread = agentSessionUnread(session);
    const focused = focusedId === session.id;
    const time = formatAgentHistoryTime(session.updatedAt, t);
    return (
      <div
        aria-selected={false}
        className={styles.row}
        data-focused={focused}
        data-row-id={session.id}
        data-unread={unread || undefined}
        id={rowId(session.id)}
        key={session.id}
        onClick={() => choose(session)}
        onMouseMove={() => { if (!focused) focusRow(session.id, false); }}
        role="option"
      >
        <span className={styles.rowBackground} />
        <span className={styles.rowTitle}>
          {unread && <span aria-hidden="true" className={styles.dot} />}
          {unread && <span className="sr-only">{t("Unread")}: </span>}
          <span className={styles.rowTitleText} data-i18n-ignore>{chatHistoryTitle(session, newChat)}</span>
          <AgentOriginBadge origin={session.origin} />
        </span>
        <span className={styles.rowMeta}>
          {toolbarIds.has(session.id) && <span>{t("Toolbar")}</span>}
          <time dateTime={session.updatedAt}>{time === t("now") || time === "now" ? t("just now") : time}</time>
        </span>
      </div>
    );
  };

  return createPortal(
    <div
      aria-label={t("Chat history")}
      className={styles.popover}
      data-flat={history.flat || undefined}
      onKeyDown={onKeyDown}
      ref={dialogRef}
      role="dialog"
      style={position}
    >
      <div className={styles.header}>
        <form onSubmit={(event) => event.preventDefault()}>
          <span className="sr-only" role="status">{t("Showing {count} items").replace("{count}", String(rows.length))}</span>
          <input
            aria-activedescendant={focusedId ? rowId(focusedId) : undefined}
            aria-autocomplete="list"
            aria-controls={listId}
            aria-expanded="true"
            aria-label={t("Chat history")}
            autoComplete="off"
            className={styles.input}
            onChange={(event) => {
              const value = event.target.value;
              setQuery(value);
              // A search puts the cursor on its best match; clearing it leaves nothing highlighted.
              const first = buildChatHistory(sourceSessions, { query: value, related, fallbackTitle: newChat }).rows[0];
              setFocusedId(value.trim() ? first?.id : undefined);
              viewportRef.current?.scrollTo?.({ top: 0 });
            }}
            placeholder={t("Chat history")}
            ref={inputRef}
            role="combobox"
            spellCheck={false}
            value={query}
          />
        </form>
      </div>
      <div className={styles.body}>
        <div className={styles.viewport} onScroll={updateFade} ref={viewportRef}>
          <div aria-hidden="true" className={styles.fadeTop} data-visible={fade.top} />
          <div aria-label={t("Chat history")} className={styles.list} id={listId} role="listbox">
            {rows.length === 0 ? (
              <div className={styles.empty}>{t("No matching history")}</div>
            ) : history.sections.map((section, index) => (
              <div className={styles.section} key={section.id} role="group" aria-label={history.flat ? undefined : sectionLabel(section)}>
                {index > 0 && <div className={styles.separator} role="separator" />}
                {!history.flat && <div aria-hidden="true" className={styles.groupHeader}>{sectionLabel(section)}</div>}
                {section.sessions.map(renderRow)}
              </div>
            ))}
          </div>
          <div aria-hidden="true" className={styles.fadeBottom} data-visible={fade.bottom} />
        </div>
      </div>
    </div>,
    document.body,
  );
}
