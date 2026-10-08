import { useMemo, type MouseEvent } from "react";
import { toast } from "sonner";
import {
  LinearContextMenuPortal,
  LinearContextMenuRoot,
  LinearContextMenuTrigger,
  LinearMenuContent,
  LinearMenuItem,
  LinearMenuSeparator,
} from "@/components/ui/row-context-menu";
import { useI18n } from "@/i18n/i18n";
import type { AgentSession } from "@/types/flow";
import { agentSessionUnread, formatAgentHistoryTime } from "./agent-read-state";
import styles from "./agent-page.module.css";

/** The most chats the empty Agent page lists under the composer (newest first); older ones stay in the chat switcher. */
export const AGENT_HISTORY_LIMIT = 50;

/**
 * Linear's "Recent agent chats" under the new-chat composer: the newest chats with an unread dot, title and short
 * age. A row opens its chat; right-click offers Copy link, Open in new tab and Open in toolbar.
 */
export function AgentHistoryList({
  sessions,
  hrefFor,
  onOpen,
  onOpenInToolbar,
}: {
  sessions: AgentSession[];
  hrefFor: (session: AgentSession) => string;
  onOpen: (session: AgentSession) => void;
  onOpenInToolbar: (session: AgentSession) => void;
}) {
  const { t } = useI18n();
  const recent = useMemo(
    () => sessions
      .filter((session) => session.slugId)
      .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
      .slice(0, AGENT_HISTORY_LIMIT),
    [sessions],
  );
  if (!recent.length) return null;
  const copyLink = (session: AgentSession) => {
    const url = new URL(hrefFor(session), window.location.origin).toString();
    void navigator.clipboard?.writeText(url).then(
      () => toast.success(t("Copied to clipboard")),
      () => toast.error(t("Could not copy to clipboard")),
    );
  };
  return (
    <div className={styles.history}>
      <nav aria-label={t("Recent agent chats")}>
        <div className={styles.historyScroll}>
          <div className={styles.historyList}>
            {recent.map((session) => {
              const unread = agentSessionUnread(session);
              const href = hrefFor(session);
              return (
                <LinearContextMenuRoot key={session.id}>
                  <LinearContextMenuTrigger asChild>
                    <a
                      className={styles.historyRow}
                      data-unread={unread || undefined}
                      draggable={false}
                      href={href}
                      onClick={(event: MouseEvent<HTMLAnchorElement>) => {
                        // Modified clicks keep the browser's own new-tab / new-window behaviour.
                        if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
                        event.preventDefault();
                        onOpen(session);
                      }}
                    >
                      {unread && <>
                        <span aria-hidden="true" className={styles.historyDot} />
                        <span className="sr-only">{t("Unread")}: </span>
                      </>}
                      <span className={styles.historyTitle} data-i18n-ignore>{session.title}</span>
                      <time className={styles.historyTime} dateTime={session.updatedAt}>{formatAgentHistoryTime(session.updatedAt, t)}</time>
                    </a>
                  </LinearContextMenuTrigger>
                  <LinearContextMenuPortal>
                    <LinearMenuContent label={t("Chat options")}>
                      <LinearMenuItem icon={<HistoryMenuIcon paths={LINK_ICON} />} label="Copy link" onSelect={() => copyLink(session)} />
                      <LinearMenuSeparator />
                      <LinearMenuItem icon={<HistoryMenuIcon paths={NEW_TAB_ICON} />} label="Open in new tab" onSelect={() => window.open(href, "_blank", "noopener")} />
                      <LinearMenuItem icon={<HistoryMenuIcon paths={TOOLBAR_ICON} />} label="Open in toolbar" onSelect={() => onOpenInToolbar(session)} />
                    </LinearMenuContent>
                  </LinearContextMenuPortal>
                </LinearContextMenuRoot>
              );
            })}
          </div>
        </div>
      </nav>
    </div>
  );
}

/** Linear's menu glyphs for the history row menu (16px, currentColor; `e:` paths use the even-odd rule). */
function HistoryMenuIcon({ paths }: { paths: string[] }) {
  return (
    <svg aria-hidden="true" focusable="false" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
      {paths.map((path) => path.startsWith("e:")
        ? <path key={path} d={path.slice(2)} fillRule="evenodd" clipRule="evenodd" />
        : <path key={path} d={path} />)}
    </svg>
  );
}

const LINK_ICON = [
  "M9.31 10.21C9.57 10.47 9.59 10.89 9.37 11.18L9.31 11.26L6.85 13.72C5.59 14.98 3.54 14.98 2.28 13.72C1.07 12.5 1.02 10.55 2.16 9.29L2.28 9.15L4.74 6.69C5.03 6.4 5.5 6.4 5.79 6.69C6.06 6.96 6.08 7.38 5.86 7.67L5.79 7.75L3.34 10.21C2.66 10.88 2.66 11.99 3.34 12.66C3.98 13.31 5.01 13.34 5.69 12.76L5.79 12.66L8.25 10.21C8.54 9.92 9.01 9.92 9.31 10.21ZM9.83 6.17C10.12 6.46 10.12 6.93 9.83 7.22L7.35 9.7C7.06 10 6.59 10 6.3 9.7C6 9.41 6 8.94 6.3 8.65L8.78 6.17C9.07 5.88 9.54 5.88 9.83 6.17ZM13.72 2.28C14.93 3.5 14.98 5.45 13.84 6.71L13.72 6.85L11.26 9.31C10.97 9.6 10.5 9.6 10.21 9.31C9.94 9.04 9.92 8.62 10.14 8.33L10.21 8.25L12.66 5.79C13.34 5.12 13.34 4.01 12.66 3.34C12.02 2.69 10.99 2.66 10.31 3.24L10.21 3.34L7.75 5.79C7.46 6.08 6.99 6.08 6.69 5.79C6.43 5.53 6.41 5.11 6.63 4.82L6.69 4.74L9.15 2.28C10.41 1.02 12.46 1.02 13.72 2.28Z",
];
const NEW_TAB_ICON = [
  "M3.75 3C2.23 3 1 4.23 1 5.75V7.75C1 9.27 2.23 10.5 3.75 10.5H5.75C6.16 10.5 6.5 10.16 6.5 9.75C6.5 9.34 6.16 9 5.75 9H3.75C3.06 9 2.5 8.44 2.5 7.75V5.75C2.5 5.06 3.06 4.5 3.75 4.5H12.25C12.94 4.5 13.5 5.06 13.5 5.75V6.25C13.5 6.66 13.84 7 14.25 7C14.66 7 15 6.66 15 6.25V5.75C15 4.23 13.77 3 12.25 3H3.75Z",
  "M11.75 7.25C11.75 6.84 11.41 6.5 11 6.5C10.59 6.5 10.25 6.84 10.25 7.25V9H8.5C8.09 9 7.75 9.34 7.75 9.75C7.75 10.16 8.09 10.5 8.5 10.5H10.25V12.25C10.25 12.66 10.59 13 11 13C11.41 13 11.75 12.66 11.75 12.25V10.5H13.5C13.91 10.5 14.25 10.16 14.25 9.75C14.25 9.34 13.91 9 13.5 9H11.75V7.25Z",
];
const TOOLBAR_ICON = [
  "M11.25 9.5C11.66 9.5 12 9.84 12 10.25C12 10.66 11.66 11 11.25 11H4.75C4.34 11 4 10.66 4 10.25C4 9.84 4.34 9.5 4.75 9.5H11.25Z",
  "e:M9.45 2C10.28 2 10.94 2 11.48 2.04C12.03 2.09 12.51 2.18 12.95 2.41C13.66 2.77 14.23 3.34 14.59 4.05C14.82 4.49 14.91 4.97 14.96 5.52C15 6.06 15 6.72 15 7.55V8.45C15 9.28 15 9.94 14.96 10.48C14.91 11.03 14.82 11.51 14.59 11.95C14.23 12.66 13.66 13.23 12.95 13.59C12.51 13.82 12.03 13.91 11.48 13.96C10.94 14 10.28 14 9.45 14H6.55C5.72 14 5.06 14 4.52 13.96C3.97 13.91 3.49 13.82 3.05 13.59C2.34 13.23 1.77 12.66 1.41 11.95C1.18 11.51 1.09 11.03 1.04 10.48C1 9.94 1 9.28 1 8.45V7.55C1 6.72 1 6.06 1.04 5.52C1.09 4.97 1.18 4.49 1.41 4.05C1.77 3.34 2.34 2.77 3.05 2.41C3.49 2.18 3.97 2.09 4.52 2.04C5.06 2 5.72 2 6.55 2H9.45ZM6.55 3.5C5.7 3.5 5.1 3.5 4.64 3.54C4.19 3.58 3.93 3.64 3.73 3.75C3.31 3.96 2.96 4.31 2.75 4.73C2.64 4.93 2.58 5.19 2.54 5.64C2.5 6.1 2.5 6.7 2.5 7.55V8.45C2.5 9.3 2.5 9.9 2.54 10.36C2.58 10.81 2.64 11.07 2.75 11.27C2.96 11.69 3.31 12.04 3.73 12.25C3.93 12.36 4.19 12.42 4.64 12.46C5.1 12.5 5.7 12.5 6.55 12.5H9.45C10.3 12.5 10.9 12.5 11.36 12.46C11.81 12.42 12.07 12.36 12.27 12.25C12.69 12.04 13.04 11.69 13.25 11.27C13.36 11.07 13.42 10.81 13.46 10.36C13.5 9.9 13.5 9.3 13.5 8.45V7.55C13.5 6.7 13.5 6.1 13.46 5.64C13.42 5.19 13.36 4.93 13.25 4.73C13.04 4.31 12.69 3.96 12.27 3.75C12.07 3.64 11.81 3.58 11.36 3.54C10.9 3.5 10.3 3.5 9.45 3.5H6.55Z",
];
