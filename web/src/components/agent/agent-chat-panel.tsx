import { useEffect, useRef, useState } from "react";
import {
  LoaderCircle,
  Maximize2,
  Minimize2,
  Minus,
  Send,
  X,
} from "lucide-react";
import {
  createAgentSession,
  createAgentSessionMessage,
  fetchAgentStatus,
} from "@/lib/api";
import type { AgentChatMessage, AgentSession, AgentStatus } from "@/types/flow";
import type { MyIssuesRowData } from "@/components/my-issues/my-issues-list";
import { StatusIcon } from "@/components/issue/issue-icons";
import { useI18n } from "@/i18n/i18n";
import styles from "./agent-chat-panel.module.css";

export function AgentChatPanel({
  initialSession,
  issues,
  onClose,
  onOpenFullPage,
  onSessionChange,
  open,
}: {
  initialSession?: AgentSession;
  issues: MyIssuesRowData[];
  onClose: () => void;
  onOpenFullPage?: (session?: AgentSession) => void;
  onSessionChange?: (session: AgentSession) => void;
  open: boolean;
}) {
  const { t } = useI18n();
  const [messages, setMessages] = useState<AgentChatMessage[]>([]);
  const [session, setSession] = useState<AgentSession>();
  const [input, setInput] = useState("");
  const [status, setStatus] = useState<AgentStatus>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [minimized, setMinimized] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (!open) return;
    let active = true;
    setError(undefined);
    fetchAgentStatus()
      .then((next) => {
        if (active) {
          setStatus(next);
          if (!next.enabled) setError(t("Flow Agent is not configured"));
        }
      })
      .catch((reason) => {
        if (active)
          setError(
            reason instanceof Error
              ? reason.message
              : t("Flow Agent is unavailable"),
          );
      });
    requestAnimationFrame(() => inputRef.current?.focus());
    return () => {
      active = false;
    };
  }, [open, t]);
  useEffect(() => {
    if (!open || !initialSession) return;
    setSession(initialSession);
    setMessages(
      initialSession.messages.map(({ role, content }) => ({ role, content })),
    );
  }, [initialSession, open]);
  if (!open) return null;
  const close = () => {
    setMessages([]);
    setSession(undefined);
    setInput("");
    setStatus(undefined);
    setError(undefined);
    setLoading(false);
    setMinimized(false);
    setFullscreen(false);
    onClose();
  };
  const submit = async () => {
    const message = input.trim();
    if (!message || loading || !status?.enabled) return;
    setMessages((current) => [...current, { role: "user", content: message }]);
    setInput("");
    setError(undefined);
    setLoading(true);
    try {
      const next = session
        ? await createAgentSessionMessage(session.id, message)
        : await createAgentSession({
            message,
            issueIds: issues.map((issue) => issue.id),
            location: "toolbar",
          });
      setSession(next);
      onSessionChange?.(next);
      setMessages(
        next.messages.map(({ role, content }) => ({ role, content })),
      );
    } catch (reason) {
      setError(
        reason instanceof Error
          ? reason.message
          : t("Flow Agent is unavailable"),
      );
    } finally {
      setLoading(false);
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  };
  return (
    <section
      aria-label={t("Flow Agent chat")}
      aria-modal="false"
      className={`${styles.panel}${minimized ? " " + styles.minimized : ""}${fullscreen ? " " + styles.fullscreen : ""}`}
      role="dialog"
    >
      <header>
        <strong data-i18n-ignore={Boolean(session) || undefined}>
          {session?.title ?? t("New chat")}
        </strong>
        <span />
        <button
          aria-label={t(minimized ? "Restore chat" : "Minimize chat")}
          onClick={() => setMinimized((value) => !value)}
          type="button"
        >
          {minimized ? <Minimize2 /> : <Minus />}
        </button>
        <button
          aria-label={t(fullscreen ? "Exit full page" : "Open full page")}
          onClick={() => {
            if (onOpenFullPage) {
              onOpenFullPage(session);
              return;
            }
            setFullscreen((value) => !value);
            setMinimized(false);
          }}
          type="button"
        >
          {fullscreen ? <Minimize2 /> : <Maximize2 />}
        </button>
        <button aria-label={t("Close chat")} onClick={close} type="button">
          <X />
        </button>
      </header>
      {!minimized && (
        <>
          <div
            aria-label={t("Agent conversation")}
            className={styles.conversation}
            role="log"
            aria-live="polite"
          >
            {!messages.length && (
              <div className={styles.empty}>
                <span>{t("Ask Flow about the selected issues")}</span>
              </div>
            )}
            {messages.map((message, index) => (
              <article
                className={
                  message.role === "user"
                    ? styles.userMessage
                    : styles.agentMessage
                }
                key={`${message.role}-${index}`}
              >
                <strong>
                  {message.role === "user" ? t("You") : t("Flow Agent")}
                </strong>
                <p>{message.content}</p>
              </article>
            ))}
            {loading && (
              <div className={styles.thinking}>
                <LoaderCircle />
                {t("Thinking…")}
              </div>
            )}
          </div>
          <div className={styles.composer}>
            <div className={styles.context}>
              {issues.map((issue) => (
                <span data-i18n-ignore key={issue.id}>
                  <StatusIcon state={issue.state} size={14} />
                  <small>{issue.identifier}</small>
                  <b>{issue.title}</b>
                </span>
              ))}
            </div>
            <textarea
              ref={inputRef}
              aria-label={t("Send a message to Flow Agent")}
              disabled={!status?.enabled || loading}
              placeholder={
                status?.enabled
                  ? t("Ask a question…")
                  : t("Flow Agent is not configured")
              }
              rows={2}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  void submit();
                }
              }}
            />
            <footer>
              {error ? <span role="alert">{error}</span> : <span />}
              <button
                aria-label={t("Send message")}
                disabled={!input.trim() || loading || !status?.enabled}
                onClick={() => void submit()}
                type="button"
              >
                {loading ? <LoaderCircle /> : <Send />}
              </button>
            </footer>
          </div>
        </>
      )}
    </section>
  );
}
