import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { fetchAgentStatus, getAgentSession, listAgentSessions, resolveAgentApproval } from "@/lib/api";
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from "@/lib/agent-stream";
import { AgentPanel } from "@/components/agent/agent-panel";
import { EntityAgentThread, clearEntityThreadDraft } from "@/components/agent/entity-agent-thread";
import { useI18n } from "@/i18n/i18n";
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus, BootstrapData } from "@/types/flow";
import { LoopGlyph } from "./loop-glyph";
import { isPublishedResult, loopToolResult, type LoopToolResult } from "./loop-data";

/**
 * Loop-builder agent docked on the right of the loop editor. It attaches the loop (`loopIds`), can send its first
 * message on open, reports `save_loop` completions so the editor refetches, and hands off publishing.
 */
export function LoopAgentPanel({
  data,
  loopId,
  title,
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
  open: boolean;
  /** Sent automatically when no conversation exists for this loop yet. */
  autoMessage?: string;
  onClose: () => void;
  onLoopSaved: (result: LoopToolResult | undefined) => void;
  onPublished: (result: LoopToolResult) => void;
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
    if (!call || call.name !== "save_loop" || call.status !== "completed") return;
    const result = loopToolResult(call);
    callbacks.current.onLoopSaved(result);
    if (result && isPublishedResult(result) && !published.current) {
      published.current = true;
      callbacks.current.onPublished(result);
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
      const controller = new AbortController();
      abortRef.current = controller;
      const onEvent = (event: AgentStreamEvent) => {
        if (event.session) setSession(event.session);
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

  const cards = useMemo(
    () =>
      Object.fromEntries(
        messages.map((message) => [
          message.id,
          (message.parts ?? [])
            .filter((part) => part.toolCall?.name === "save_loop" && part.toolCall.status === "completed")
            .map((part) => ({ id: part.id, title: part.toolCall?.title, result: loopToolResult(part.toolCall) })),
        ]),
      ),
    [messages],
  );

  return (
    <AgentPanel
      aria-label={t("Loop agent")}
      dock="right"
      loading={!hydrated}
      open={open}
      onRequestClose={onClose}
      title={session?.title ?? title ?? t("New chat")}
      variant="sidebar"
    >
      <EntityAgentThread
        approvalBusy={approvalBusy}
        conversationDraftKey={draftKey}
        emptyLabel={t("Describe what this loop should do")}
        enabled={Boolean(status?.enabled)}
        error={error}
        input={input}
        loading={loading}
        mentionData={data}
        messages={messages}
        onInputChange={setInput}
        onSendSuggestion={(message) => void submit(message)}
        onStop={() => abortRef.current?.abort()}
        onSubmit={() => void submit()}
        onToolApproval={(call, decision) => void decide(call, decision)}
        placeholder={status && !status.enabled ? t("Flow Agent is not configured") : t("Describe what this loop should do…")}
        renderMessageAttachment={(message) => {
          const items = cards[message.id] ?? [];
          if (!items.length) return null;
          return (
            <div className="loops-agent-cards">
              {items.map((item) => (
                <LoopToolCard key={item.id} title={item.title} result={item.result} onOpen={item.result && isPublishedResult(item.result) && onNavigateLoop ? () => onNavigateLoop(item.result!) : undefined} />
              ))}
            </div>
          );
        }}
        streamParts={streamParts}
      />
    </AgentPanel>
  );
}

/** "Updated workflow definition draft" / "Created automation" card under a loop-builder reply. */
export function LoopToolCard({ title, result, onOpen }: { title?: string; result?: LoopToolResult; onOpen?: () => void }) {
  const { t } = useI18n();
  const published = isPublishedResult(result);
  const heading = title ?? (published ? "Created automation" : "Updated workflow definition draft");
  const body = (
    <>
      <span className="loops-agent-card-icon" style={{ color: result?.color || undefined }}>
        <LoopGlyph icon={result?.icon || "Automation"} size={14} />
      </span>
      <span className="loops-agent-card-copy">
        <strong data-i18n-ignore>{result?.name || t("Untitled loop")}</strong>
        {published ? (
          <small>
            {t("Ran")} {result?.runCount30d ?? 0} {t(result?.runCount30d === 1 ? "time (30d)" : "times (30d)")}
            {result?.teamName && (
              <>
                {" · "}
                <span data-i18n-ignore>{result.teamName}</span>
              </>
            )}
          </small>
        ) : (
          result?.description && <small data-i18n-ignore>{result.description}</small>
        )}
      </span>
    </>
  );
  return (
    <div className="loops-agent-card">
      <span className="loops-agent-card-title">{t(heading)}</span>
      {onOpen ? (
        <button className="loops-agent-card-body" type="button" onClick={onOpen}>
          {body}
        </button>
      ) : (
        <div className="loops-agent-card-body">{body}</div>
      )}
    </div>
  );
}
