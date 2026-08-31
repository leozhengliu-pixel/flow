import { useEffect, useRef, useState } from "react";
import {
  Check,
  ChevronRight,
  LoaderCircle,
  Maximize2,
  Minimize2,
  Minus,
  Send,
  X,
} from "lucide-react";
import {
  fetchAgentStatus,
} from "@/lib/api";
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from "@/lib/agent-stream";
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus } from "@/types/flow";
import type { MyIssuesRowData } from "@/components/my-issues/my-issues-list";
import { StatusIcon } from "@/components/issue/issue-icons";
import { useI18n } from "@/i18n/i18n";
import { AgentRichText } from "./agent-rich-text";
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
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [session, setSession] = useState<AgentSession>();
  const [input, setInput] = useState("");
  const [status, setStatus] = useState<AgentStatus>();
  const [loading, setLoading] = useState(false);
  const [streamParts, setStreamParts] = useState<AgentMessagePart[]>([]);
  const [error, setError] = useState<string>();
  const [minimized, setMinimized] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const abortRef = useRef<AbortController | undefined>(undefined);
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
    setMessages(initialSession.messages);
  }, [initialSession, open]);
  if (!open) return null;
  const close = () => {
    setMessages([]);
    setSession(undefined);
    setInput("");
    setStatus(undefined);
    setError(undefined);
    setLoading(false);
    setStreamParts([]);
    setMinimized(false);
    setFullscreen(false);
    onClose();
  };
  const submit = async () => {
    const message = input.trim();
    if (!message || loading || !status?.enabled) return;
    setMessages((current) => [...current, { id: `pending-${Date.now()}`, role: "user", content: message, createdAt: new Date().toISOString() }]);
    setInput("");
    setError(undefined);
    setLoading(true);
    setStreamParts([]);
    try {
      const controller = new AbortController();
      abortRef.current = controller;
      let next = session;
      const onEvent = (event: AgentStreamEvent) => {
        if (event.session) {
          next = event.session;
          setSession(event.session);
        }
        if (event.type === "session.started" && event.session) {
          setMessages(event.session.messages);
        }
        if (event.type === "text.delta") {
          setMessages(current => current.at(-1)?.role === "assistant"
            ? current.map((item, index) => index === current.length - 1 ? { ...item, content: item.content + (event.delta ?? "") } : item)
            : [...current, { id: event.messageId ?? `stream-${Date.now()}`, role: "assistant", content: event.delta ?? "", createdAt: new Date().toISOString() }]);
        }
        if ((event.type.startsWith("tool.") || event.type === "reasoning.delta") && event.part) {
          const nextPart = event.part;
          setStreamParts(current => {
            const index = current.findIndex(part => part.id === nextPart.id);
            return index >= 0 ? current.map((part, itemIndex) => itemIndex === index ? nextPart : part) : [...current, nextPart];
          });
        }
        if (event.type === "session.completed" && event.session) {
          setMessages(event.session.messages);
          setStreamParts([]);
        }
      };
      next = session
        ? await streamAgentSessionMessage(session.id, message, onEvent, controller.signal)
        : await streamNewAgentSession({ message, issueIds: issues.map(issue => issue.id), location: "toolbar" }, onEvent, controller.signal);
      if (!next) return;
      setSession(next);
      onSessionChange?.(next);
      setMessages(next.messages);
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === "AbortError") {
        setStreamParts(current => current.map(part => part.status === "running" ? { ...part, status: "error" } : part));
        return;
      }
      setError(
        reason instanceof Error
          ? reason.message
          : t("Flow Agent is unavailable"),
      );
    } finally {
	  abortRef.current = undefined;
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
                key={message.id || `${message.role}-${index}`}
              >
                <strong>
                  {message.role === "user" ? t("You") : t("Flow Agent")}
                </strong>
                {message.role === "assistant" && <PanelMessageActivity parts={message.parts ?? []}/>}
                {message.content && <AgentRichText ariaLabel={message.role === "user" ? t("Your message") : t("AI message")} className={styles.messageDocument} content={message.content}/>}
              </article>
            ))}
            {loading && (
              <div className={styles.thinking}>
                <LoaderCircle />
                {t("Thinking…")}
              </div>
            )}
            {streamParts.map(part => <div className={styles.streamPart} key={part.id}>{part.type === "toolCall" ? `${part.status === "completed" ? "✓" : "…"} ${part.toolCall?.name.replaceAll("_", " ")}` : part.type === "reasoning" ? `Thinking: ${part.text ?? ""}` : part.text}</div>)}
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
              {loading ? <button aria-label={t("Stop generating")} onClick={() => abortRef.current?.abort()} type="button"><X /></button> : <button
                aria-label={t("Send message")}
                disabled={!input.trim() || !status?.enabled}
                onClick={() => void submit()}
                type="button"
              ><Send /></button>}
            </footer>
          </div>
        </>
      )}
    </section>
  );
}

function PanelMessageActivity({ parts }: { parts: AgentMessagePart[] }) {
  const { t } = useI18n()
  const work = parts.filter(part => part.type === 'reasoning' || part.type === 'toolCall')
  if (!work.length) return null
  const running = work.some(part => part.status === 'running' || part.status === 'pending' || part.toolCall?.status === 'running' || part.toolCall?.status === 'pending')
  return <details className={styles.messageActivity} open={running || undefined}>
    <summary>{running ? <LoaderCircle/> : <Check/>}<span>{running ? t('Thinking…') : t('Work completed')}</span><ChevronRight/></summary>
    <div>{work.map(part => <div key={part.id}>{part.type === 'reasoning' ? <><strong>{t('Reasoning')}</strong><p>{part.text}</p></> : <span>{part.toolCall?.name.replaceAll('_', ' ')}</span>}</div>)}</div>
  </details>
}
