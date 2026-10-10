import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchAgentStatus, getAgentSession, listAgentSessions, resolveAgentApproval } from "@/lib/api";
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from "@/lib/agent-stream";
import { AgentPanel } from "@/components/agent/agent-panel";
import { EntityAgentThread } from "@/components/agent/entity-agent-thread";
import { clearEntityThreadDraft } from "@/components/agent/entity-thread-draft";
import { useI18n } from "@/i18n/i18n";
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus, BootstrapData } from "@/types/flow";
import { isPublishCall, isPublishedResult, loopToolResult, type LoopToolResult } from "./loop-data";
import { LoopBuilderMessage, type LoopVisual } from "./loop-builder-message";

export { LoopToolCard } from "./loop-builder-message";

/** The message the panel sends for a template draft (loopBuilderFirstMessage); Linear shows no bubble for it. */
const TEMPLATE_SETUP_MESSAGE = /^Set up this loop from the .+ template$/;
const POLL_INTERVAL_MS = 1500;
/** Stop polling a turn that never finishes (about three minutes). */
const MAX_POLLS = 120;

/**
 * Loop-builder agent docked on the right of the loop editor (and of the loop page after publishing). It attaches the
 * loop (`loopIds`), can send its first message on open, reports `save_loop` completions so the editor refetches, and
 * hands off publishing. A turn that is still running on the server (the stream dropped when the page navigated to the
 * published loop) is polled until the reply is saved.
 */
export function LoopAgentPanel({
  data,
  loopId,
  title,
  visual,
  open,
  autoMessage,
  onClose,
  onLoopSaved,
  onPublished,
  onNavigateLoop,
}: {
  data: BootstrapData;
  loopId: string;
  title?: string;
  /** Loop icon for the tool cards; a `templateId` also hides the auto-sent "Set up this loop from the … template". */
  visual?: LoopVisual;
  open: boolean;
  /** Sent automatically when no conversation exists for this loop yet. */
  autoMessage?: string;
  onClose: () => void;
  onLoopSaved: (result: LoopToolResult | undefined) => void;
  /** Omitted on the loop page, where the loop is already published. */
  onPublished?: (result: LoopToolResult) => void;
  onNavigateLoop?: (result: LoopToolResult) => void;
}) {
  const { t } = useI18n();
  const [messages, setMessages] = useState<AgentMessage[]>([]);
  const [session, setSession] = useState<AgentSession>();
  const [input, setInput] = useState("");
  const [status, setStatus] = useState<AgentStatus>();
  const [loading, setLoading] = useState(false);
  const [hydrated, setHydrated] = useState(false);
  const [streamParts, setStreamParts] = useState<AgentMessagePart[]>([]);
  const [error, setError] = useState<string>();
  const [approvalBusy, setApprovalBusy] = useState<string>();
  const [polling, setPolling] = useState(false);
  const [streamMessageId, setStreamMessageId] = useState<string>();
  const abortRef = useRef<AbortController | undefined>(undefined);
  const autoSent = useRef(false);
  const published = useRef(false);
  const draftKey = `loop:${loopId}:${session?.id ?? "new"}`;
  const callbacks = useRef({ onLoopSaved, onPublished });
  callbacks.current = { onLoopSaved, onPublished };

  useEffect(() => {
    if (!open) return;
    let active = true;
    fetchAgentStatus()
      .then((next) => {
        if (!active) return;
        setStatus(next);
        if (!next.enabled) setError(t("Flow Agent is not configured"));
      })
      .catch((reason) => active && setError(reason instanceof Error ? reason.message : t("Flow Agent is unavailable")));
    return () => {
      active = false;
    };
  }, [open, t]);

  // Resume the loop's conversation when one exists.
  useEffect(() => {
    if (!open) return;
    let active = true;
    setHydrated(false);
    listAgentSessions()
      .then(async (sessions) => {
        const match = (Array.isArray(sessions) ? sessions : [])
          .filter((item) => item.loopIds?.includes(loopId))
          .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))[0];
        if (!match || !active) return;
        const full = match.messages?.length ? match : await getAgentSession(match.id);
        if (!active) return;
        setSession(full);
        setMessages(full.messages ?? []);
      })
      .catch(() => undefined)
      .finally(() => active && setHydrated(true));
    return () => {
      active = false;
    };
  }, [loopId, open]);

  const handleToolPart = useCallback((part: AgentMessagePart | undefined) => {
    const call = part?.toolCall;
    if (!call || call.name.replace(/^mcp__flow\./, "") !== "save_loop" || call.status !== "completed") return;
    const result = loopToolResult(call);
    callbacks.current.onLoopSaved(result);
    if (result && (isPublishedResult(result) || isPublishCall(call)) && !published.current) {
      published.current = true;
      callbacks.current.onPublished?.(result);
    }
  }, []);

  const submit = useCallback(
    async (text?: string) => {
      const message = (text ?? input).trim();
      if (!message || loading || !status?.enabled) return;
      setMessages((current) => [...current, { id: `pending-${Date.now()}`, role: "user", content: message, createdAt: new Date().toISOString() }]);
      if (text === undefined) setInput("");
      setError(undefined);
      setLoading(true);
      setStreamParts([]);
      setStreamMessageId(undefined);
      const controller = new AbortController();
      abortRef.current = controller;
      const onEvent = (event: AgentStreamEvent) => {
        if (event.session) setSession(event.session);
        if (event.messageId) setStreamMessageId(event.messageId);
        if (event.type === "session.started" && event.session) setMessages(event.session.messages);
        if (event.type === "text.replaced")
          setMessages((current) => (current.at(-1)?.role === "assistant" ? current.map((item, index) => (index === current.length - 1 ? { ...item, content: event.delta ?? "" } : item)) : current));
        if (event.type === "text.delta")
          setMessages((current) =>
            current.at(-1)?.role === "assistant"
              ? current.map((item, index) => (index === current.length - 1 ? { ...item, content: item.content + (event.delta ?? "") } : item))
              : [...current, { id: event.messageId ?? `stream-${Date.now()}`, role: "assistant", content: event.delta ?? "", createdAt: new Date().toISOString() }],
          );
        if ((event.type.startsWith("tool.") || event.type.startsWith("elicitation.") || event.type === "reasoning.delta") && event.part) {
          const next = event.part;
          setStreamParts((current) => {
            const index = current.findIndex((part) => part.id === next.id);
            return index >= 0 ? current.map((part, itemIndex) => (itemIndex === index ? next : part)) : [...current, next];
          });
          if (event.type === "tool.completed") handleToolPart(next);
        }
        if (event.type === "session.completed" && event.session) {
          setMessages(event.session.messages);
          setStreamParts([]);
          // Catch save_loop results the stream did not report individually.
          const last = event.session.messages.filter((item) => item.role === "assistant").at(-1);
          for (const part of last?.parts ?? []) handleToolPart(part);
        }
      };
      try {
        const next = session
          ? await streamAgentSessionMessage(session.id, message, onEvent, controller.signal, { loopIds: [loopId] })
          : await streamNewAgentSession({ message, loopIds: [loopId], location: "toolbar" }, onEvent, controller.signal);
        if (next) {
          setSession(next);
          setMessages(next.messages);
        }
        if (text === undefined) clearEntityThreadDraft(draftKey);
      } catch (reason) {
        if (reason instanceof DOMException && reason.name === "AbortError") {
          setStreamParts((current) => current.map((part) => (part.status === "running" ? { ...part, status: "error" } : part)));
          return;
        }
        setError(reason instanceof Error ? reason.message : t("Flow Agent is unavailable"));
      } finally {
        abortRef.current = undefined;
        setLoading(false);
      }
    },
    [draftKey, handleToolPart, input, loading, loopId, session, status?.enabled, t],
  );

  // Template and prompt drafts start the conversation as soon as the panel is ready.
  useEffect(() => {
    if (!open || !hydrated || autoSent.current || !autoMessage || !status?.enabled || session || messages.length) return;
    autoSent.current = true;
    void submit(autoMessage);
  }, [autoMessage, hydrated, messages.length, open, session, status?.enabled, submit]);

  useEffect(() => () => abortRef.current?.abort(), []);

  const decide = async (call: AgentMessagePart["toolCall"] | undefined, decision: "approve" | "reject") => {
    if (!session || !call?.approvalId || approvalBusy) return;
    setApprovalBusy(call.approvalId);
    try {
      await resolveAgentApproval(session.id, call.approvalId, decision);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("Flow Agent is unavailable"));
    } finally {
      setApprovalBusy(undefined);
    }
  };

  // A turn still running on the server: the session ends with the user's message until the reply is saved.
  const lastRole = messages.at(-1)?.role;
  const sessionId = session?.id;
  useEffect(() => {
    if (!open || !hydrated || loading || !sessionId || lastRole !== "user") return;
    let active = true;
    let attempts = 0;
    setPolling(true);
    const timer = window.setInterval(() => {
      attempts += 1;
      if (attempts > MAX_POLLS) {
        window.clearInterval(timer);
        setPolling(false);
        return;
      }
      getAgentSession(sessionId)
        .then((next) => {
          if (!active || next.messages?.at(-1)?.role !== "assistant") return;
          setSession(next);
          setMessages(next.messages);
          for (const part of next.messages.at(-1)?.parts ?? []) handleToolPart(part);
        })
        .catch(() => undefined);
    }, POLL_INTERVAL_MS);
    return () => {
      active = false;
      window.clearInterval(timer);
      setPolling(false);
    };
  }, [handleToolPart, hydrated, lastRole, loading, open, sessionId]);

  const templateId = visual?.templateId;
  const visibleMessages = useMemo(
    () => (templateId ? messages.filter((message, index) => !(index === 0 && message.role === "user" && TEMPLATE_SETUP_MESSAGE.test(message.content.trim()))) : messages),
    [messages, templateId],
  );
  // Live questions and tool rows belong to the reply being streamed, so they render in its order.
  const displayedMessages = useMemo(() => {
    if (!loading || !streamParts.length) return visibleMessages;
    const last = visibleMessages.at(-1);
    if (last?.role === "assistant" && (!streamMessageId || last.id === streamMessageId)) {
      const known = new Set(streamParts.map((part) => part.id));
      return [...visibleMessages.slice(0, -1), { ...last, parts: [...(last.parts ?? []).filter((part) => !known.has(part.id)), ...streamParts] }];
    }
    return [...visibleMessages, { id: streamMessageId ?? "loop-builder-stream", role: "assistant" as const, content: "", createdAt: new Date().toISOString(), parts: streamParts }];
  }, [loading, streamMessageId, streamParts, visibleMessages]);

  const sessionTitle = session?.title?.trim();
  const heading = sessionTitle && sessionTitle !== "New chat" && !TEMPLATE_SETUP_MESSAGE.test(sessionTitle) ? sessionTitle : (title ?? t("New chat"));

  return (
    <AgentPanel
      aria-label={t("Loop agent")}
      dock="right"
      loading={!hydrated}
      open={open}
      onRequestClose={onClose}
      title={heading}
      variant="sidebar"
    >
      <EntityAgentThread
        approvalBusy={approvalBusy}
        conversationDraftKey={draftKey}
        emptyLabel={t("Describe what this loop should do")}
        enabled={Boolean(status?.enabled)}
        error={error}
        input={input}
        loading={loading || polling}
        mentionData={data}
        messages={displayedMessages}
        onInputChange={setInput}
        onSendSuggestion={(message) => void submit(message)}
        onStop={() => abortRef.current?.abort()}
        onSubmit={() => void submit()}
        onToolApproval={(call, decision) => void decide(call, decision)}
        placeholder={status && !status.enabled ? t("Flow Agent is not configured") : t("Describe what this loop should do…")}
        renderAssistantBody={(message, { streaming }) => (
          <LoopBuilderMessage
            approvalBusy={approvalBusy}
            entityData={data}
            message={message}
            streaming={streaming}
            teams={data.teams}
            visual={visual}
            onOpenLoop={onNavigateLoop}
            onToolApproval={(call, decision) => void decide(call, decision)}
          />
        )}
      />
    </AgentPanel>
  );
}
