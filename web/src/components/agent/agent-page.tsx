import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import * as Popover from "@radix-ui/react-popover";
import { AlertCircle, Box, Plus, Search, Workflow, X, type LucideIcon } from "lucide-react";
import { useLatestRef } from "@/hooks/use-latest-ref";
import { usePhoneLayout } from "@/hooks/use-phone-layout";
import {
  fetchAgentStatus,
  getAgentSession,
  updateAgentSession,
} from "@/lib/api";
import { streamAgentSessionMessage, streamAgentSessionMessageEdit, streamNewAgentSession, type AgentStreamEvent } from "@/lib/agent-stream";
import { createAgentStreamEventCoalescer } from "@/lib/agent-stream-coalescer";
import { agentPath } from "@/lib/app-routes";
import type { AgentMessage, AgentMessageAttachment, AgentMessagePart, AgentSession, AgentStatus, AgentToolCall, BootstrapData, Project } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { usePropertyCommand } from "@/components/property/use-property-command";
import {
  AgentAttachIcon,
  AgentChevronDownIcon,
  AgentStopIcon,
  AgentSubmitIcon,
  AgentWatermarkIcon,
} from "./agent-icons";
import { AgentRichText } from "./agent-rich-text";
import { AgentDraftCard } from "./agent-draft-card";
import { HealthGlyph } from "@/components/project-detail/health-glyph";
import { healthColor } from "@/components/project-detail/health-color";
import { splitAgentDraft } from "./agent-draft";
import { AgentAnswerText, AgentReferencedResources, AgentSuggestionChips } from "./agent-answer";
import { AgentChangeCards } from "./agent-change-cards";
import { agentChangeEntityKeys } from "./agent-change-model";
import { parseAgentAnswer, splitAgentSuggestions } from "./agent-answer-content";
import { AgentWorkGroup } from "./agent-work-group";
import { AgentQuestionPrompt } from "./agent-approval-question";
import { formatAgentTime, shouldShowAgentTime } from "./agent-time";
import { clearAgentDraft, readAgentDraft, writeAgentDraft } from "./agent-drafts";
import styles from "./agent-page.module.css";
import { AgentMentionInput, type AgentMention } from "./agent-mention-input";
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { agentFileAttachment } from './agent-attachments'
import { AgentMessageAttachments } from './agent-message-attachments'
import { AgentContentWithBlocks } from './agent-content-blocks'
import { splitAgentBlocks } from './agent-content-model'
import { fillAgentComposer } from './agent-composer-dom'
import { uploadAgentFile } from '@/lib/api'
import { AgentSkillsPicker } from './agent-skills-picker'
import { applyAgentStreamEvent, markAgentSessionFailed, markAgentSessionStopped } from './agent-stream-state'
import { isBlankStoppedReply, settleStoppedAgentReply } from './agent-stopped-reply'
import { clearLiveAgentSession, liveAgentSession, setLiveAgentSession, useLiveAgentSessionsVersion } from './agent-live-sessions'
import { agentShownParts, useAgentApprovalAnswer, useAgentReplyFollow } from './use-agent-reply-follow'
import { AgentElicitation } from './agent-elicitation';
import {
  asPersistedConversation,
  useDeferredHydratedConversation,
} from '@/hooks/use-deferred-hydrated-conversation';
import { AgentElicitationResponseQueue } from './agent-elicitation-response-queue';
import { summarizeElicitationQueue } from './agent-elicitation-queue-model';
import { FlowLogo } from '@/components/ui/flow-logo';
import { AgentHistoryList } from './agent-history-list';
import { AgentErrorDetail } from './agent-error-detail';
import { agentSessionUnread, formatAgentHistoryTime, groupAgentHistory, useMarkAgentSessionRead } from './agent-read-state';
import { buildChatHistory } from './agent-chat-history-model';
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { AgentToolbarGlyph } from '@/components/ui/agent-glyph'
import { AgentReplyActions, AgentSessionFeedbackDialog, AgentUserMessageActions } from './agent-message-actions'
import { toast } from '@/lib/toast'
import { AgentChatMenu, AgentRenamePalette } from './agent-chat-actions'
import { deleteAgentChat, useAgentChatHotkeys } from './agent-chat-commands'
import { AGENT_PAGE_ASK_EVENT, type AgentPageAskDetail } from './agent-ask-request'
// coding sessions (track G)
import { ChatCodingSessionCards } from '@/components/coding-sessions/chat-coding-session-cards'

export function AgentPage({
  chatSlug,
  data,
  onNavigate,
  onOpenSidebar,
  onSessionChange,
}: {
  chatSlug?: string;
  data: BootstrapData;
  onNavigate: (href: string) => void;
  onOpenSidebar: () => void;
  /** Saves a chat the page changed into the workspace data (no session removes it), without reloading the workspace. */
  onSessionChange: (id: string, session?: AgentSession) => void;
}) {
  const { t } = useI18n();
  const [sessions, setSessions] = useState(data.agentSessions ?? []),
    [status, setStatus] = useState<AgentStatus>(),
    [input, setInput] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<string>(),
    [historyOpen, setHistoryOpen] = useState(false),
    [examplesDismissed, setExamplesDismissed] = useState(() => {
      try { return window.localStorage.getItem("flow:agent-examples-dismissed") === "1"; } catch { return false; }
    }),
    [mentions, setMentions] = useState<AgentMention[]>([]),
    [selectedSkills, setSelectedSkills] = useState<string[]>([]),
    [renameOpen, setRenameOpen] = useState(false),
    [editingId, setEditingId] = useState<string>(),
    [activeStreamId, setActiveStreamId] = useState<string>(),
    // The message just sent, shown with a working indicator until the server's stream catches up.
    [pending, setPending] = useState<AgentSession>(),
    [attachments, setAttachments] = useState<File[]>([]),
    [awayFromLatest, setAwayFromLatest] = useState(false),
    [scrollRequest, setScrollRequest] = useState(0),
    [sessionFeedbackOpen, setSessionFeedbackOpen] = useState(false);
  const scrollToLatestRef = useRef<(() => void) | undefined>(undefined);
  const editorRef = useRef<HTMLDivElement>(null);
  const insertFilesRef = useRef<((files: File[]) => void) | null>(null);
  // Pasted images upload at once; the message waits until they are in.
  const [filesUploading, setFilesUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const streamAbortRef = useRef<AbortController | undefined>(undefined);
  // Chats already re-read from the server since this page opened.
  const [hydratedIds, setHydratedIds] = useState<ReadonlySet<string>>(() => new Set());
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  useLiveAgentSessionsVersion();
  const historyRequested = new URLSearchParams(window.location.search).get("history") === "1";
  const agentDraftKey = `flow:agent-draft:${data.workspace.id}`;
  const restoredAgentDraft = useRef(readAgentDraft(agentDraftKey));
  const [seenAgentSessions, setSeenAgentSessions] = useState(data.agentSessions);
  if (seenAgentSessions !== data.agentSessions) {
    setSeenAgentSessions(data.agentSessions);
    setSessions(data.agentSessions ?? []);
  }
  useEffect(() => {
    if (chatSlug || !restoredAgentDraft.current?.input) return;
    const restored = restoredAgentDraft.current;
    setInput(restored.input);
    setSelectedSkills(restored.skillIds);
    requestAnimationFrame(() => {
      writeInputToEditor(editorRef, restored.input);
      if (document.activeElement === editorRef.current) placeCaretAtEnd(editorRef.current);
    });
  }, [chatSlug]);
  const historyRequestKey = `${chatSlug ?? ""}|${historyRequested}`;
  const [seenHistoryRequestKey, setSeenHistoryRequestKey] = useState<string>();
  if (seenHistoryRequestKey !== historyRequestKey) {
    setSeenHistoryRequestKey(historyRequestKey);
    if (historyRequested) setHistoryOpen(true);
  }
  useEffect(() => {
    let active = true;
    fetchAgentStatus()
      .then((next) => active && setStatus(next))
      .catch(() => active && setStatus({ enabled: false, model: "" }));
    return () => {
      active = false;
    };
  }, []);
  // Leaving a chat for the Agent landing (sidebar "Agent") shows the empty page, not the chat just sent from here.
  const previousChatSlugRef = useRef(chatSlug);
  useEffect(() => {
    if (previousChatSlugRef.current && !chatSlug) setActiveStreamId(undefined);
    previousChatSlugRef.current = chatSlug;
  }, [chatSlug]);
  const currentSummary = useMemo(
    () =>
      chatSlug
        ? sessions.find(
            (item) => item.slugId === chatSlug || item.id === chatSlug,
          )
        : sessions.find((item) => item.id === activeStreamId),
    [activeStreamId, chatSlug, sessions],
  );
  // A reply still streaming from an earlier visit to this page.
  const live = busy ? undefined : liveAgentSession(chatSlug ?? activeStreamId);
  const deferredConversation = useMemo(() => {
    if (!currentSummary) return currentSummary;
    // Opening a chat re-reads it from the server once, so a reply that
    // finished while the page was away (or in another tab) shows up.
    if (hydratedIds.has(currentSummary.id)) return currentSummary;
    return asPersistedConversation(currentSummary, async (id) => {
      const full = await getAgentSession(id);
      setHydratedIds((ids) => new Set(ids).add(id));
      // A reply streamed meanwhile (an answer sent right after opening) is newer than this read: keep it.
      setSessions((list) =>
        list.map((item) => (item.id === full.id && Date.parse(full.updatedAt) >= Date.parse(item.updatedAt) ? full : item)),
      );
      return full;
    });
  }, [currentSummary, hydratedIds]);
  const hydratedCurrent = useDeferredHydratedConversation(deferredConversation) ?? undefined;
  const current = live ?? hydratedCurrent;
  // The reply is still running on the server while this page does not stream it (the page that asked was reloaded or
  // closed, or it was asked from another tab or chat surface): show its saved progress, approvals and questions
  // included, and follow it until it finishes.
  const follow = useAgentReplyFollow({
    session: current,
    streaming: busy || Boolean(live),
    onUpdate: (latest) => {
      setSessions((list) => list.map((item) => (item.id === latest.id ? latest : item)));
      onSessionChange(latest.id, latest);
    },
  });
  const awaitingReply = follow.following;
  const replyRunning = busy || Boolean(live) || awaitingReply;
  const attachShortcut = isMacPlatform() ? "⌘⇧U" : "Ctrl Shift U";
  // Seeing a chat (once its reply has settled) clears its unread dot here and in the history list.
  useMarkAgentSessionRead(current, replyRunning, (read, lastReadAt) => {
    const listed = sessions.find((item) => item.id === read.id);
    setSessions((list) => list.map((item) => (item.id === read.id ? { ...item, lastReadAt } : item)));
    onSessionChange(read.id, listed ? { ...listed, lastReadAt } : read);
  });
  // Until the reply's first part arrives the conversation ends with the user's message: keep the working indicator
  // under it while a reply is running (streaming here, or on the server after the page was reopened).
  const shown = pending ?? (replyRunning && current?.messages.at(-1)?.role === "user" ? withReplyPlaceholder(current) : current);
  const shownParts = useMemo(() => agentShownParts(shown?.messages), [shown?.messages]);
  useEffect(() => {
    if (chatSlug || current || !input.trim()) {
      if (!chatSlug && !current && !input.trim()) clearAgentDraft(agentDraftKey);
      return;
    }
    const timer = window.setTimeout(() => writeAgentDraft(agentDraftKey, { input, skillIds: selectedSkills }), 250);
    return () => window.clearTimeout(timer);
  }, [agentDraftKey, chatSlug, current, input, selectedSkills]);
  // The skills picked follow the chat shown (and what it saved).
  const [skillsFor, setSkillsFor] = useState<{ id?: string; skillIds?: string[] }>();
  if (!skillsFor || skillsFor.id !== current?.id || skillsFor.skillIds !== current?.skillIds) {
    setSkillsFor({ id: current?.id, skillIds: current?.skillIds });
    setSelectedSkills(current?.skillIds ?? []);
  }
  const writeInput = (value: string) => {
    setInput(value);
    writeInputToEditor(editorRef, value);
  };
  const saveSession = (next: AgentSession) => {
    setSessions((list) => [
      next,
      ...list.filter((item) => item.id !== next.id),
    ]);
    onSessionChange(next.id, next);
  };
  const commitSession = (next: AgentSession) => {
    saveSession(next);
    setSelectedSkills(next.skillIds);
    onNavigate(agentPath(data.workspace.urlKey, next.slugId));
  };
  // The generated title can land after the reply: re-read just this chat (never the whole workspace) until it does.
  const refreshTitle = (sent: AgentSession) => {
    let attempt = 0;
    const poll = () => void Promise.resolve().then(() => getAgentSession(sent.id)).then((latest) => {
      if (latest.title !== sent.title) {
        setSessions((list) => list.map((item) => (item.id === latest.id ? { ...item, title: latest.title } : item)));
        onSessionChange(latest.id, latest);
      } else if (++attempt < titleRetryDelays.length) window.setTimeout(poll, titleRetryDelays[attempt]);
    }).catch(() => undefined);
    window.setTimeout(poll, titleRetryDelays[0]);
  };
  /**
   * Sends `message`, or with `retryOf` runs the user message that already failed again: the server keeps that message,
   * drops the failed reply after it and answers again, so the chat never gets a second copy of the question.
   */
  const send = async (message = input, retryOf?: AgentMessage, answer?: { partId: string; value: string }) => {
    message = message.trim();
    if (!message || replyRunning || !status?.enabled || (!retryOf && !answer && filesUploading)) return;
    const retrying = Boolean(retryOf && current);
    setBusy(true);
    setScrollRequest(value => value + 1);
    setError(undefined);
    // The reference app shows the sent message and its working state at once; the stream replaces this copy when it starts.
    // Picking a question's option shows it as the chosen pill straight away, not as a message bubble.
    setPending(optimisticAgentTurn(current, message, retrying ? retryOf!.id : editingId, retrying ? retryOf!.mentions : mentions, answer, retrying ? retryOf!.attachments : attachments.length ? attachments.map(({ name, type, size }) => ({ name, contentType: type, size })) : current?.messages.find((item) => item.id === editingId)?.attachments));
    let streamed = current, started = false, failedInChat = false;
    let coalescer: ReturnType<typeof createAgentStreamEventCoalescer> | undefined;
    try {
      // Files go with the message for the server to keep; a retry resends the text alone and the server keeps its files.
      const sentAttachments = retrying ? [] : await Promise.all(attachments.map(agentFileAttachment));
      const controller = new AbortController();
      streamAbortRef.current = controller;
      const handleEvent = (event: AgentStreamEvent) => {
          // The server saved the failed turn with its error; show the same row now instead of only a banner.
          if (event.type === "error" && streamed && started) {
            failedInChat = true;
            streamed = markAgentSessionFailed(streamed, event.error || t("Flow Agent is unavailable"));
          } else streamed = applyAgentStreamEvent(streamed, event);
          if (!streamed) return;
          const next = streamed;
          if (event.type === "session.completed") clearLiveAgentSession(next.id);
          else setLiveAgentSession(next);
          setPending(undefined);
          setSessions((list) => [next, ...list.filter((item) => item.id !== next.id)]);
          if (event.type === "session.started") {
            started = true;
            setActiveStreamId(next.id);
            if (mountedRef.current) onNavigate(agentPath(data.workspace.urlKey, next.slugId));
          }
          if (event.type === "session.completed") {
            // The completion event contains the final answer. Stop the live chrome now;
            // the request can finish persisting metadata/title work in the background.
            setBusy(false);
            onSessionChange(next.id, next);
            // Don't pull someone back to the chat if they've left the page.
            if (mountedRef.current) onNavigate(agentPath(data.workspace.urlKey, next.slugId));
            if (next.messages.length <= 2) refreshTitle(next);
          }
      };
      coalescer = createAgentStreamEventCoalescer(handleEvent);
      const onEvent = coalescer.push;
      const mentioned = {
        issueIds: mentions.filter((item) => item.type === "issue").map((item) => item.id),
        projectIds: mentions.filter((item) => item.type === "project").map((item) => item.id),
        documentIds: mentions.filter((item) => item.type === "document").map((item) => item.id),
        initiativeIds: mentions.filter((item) => item.type === "initiative").map((item) => item.id),
        userIds: mentions.filter((item) => item.type === "user").map((item) => item.id),
        mentions,
        ...(answer ? { answer } : {}),
        ...(sentAttachments.length ? { attachments: sentAttachments } : {}),
      };
      // The reference app clears the composer as soon as the message is sent; restore it below if sending fails.
      if (!retrying && !answer) {
        setMentions([]);
        writeInput("");
        setAttachments([]);
      }
      if (current && retryOf) await streamAgentSessionMessageEdit(current.id, retryOf.id, message, onEvent, controller.signal);
      else if (current && editingId) await streamAgentSessionMessageEdit(current.id, editingId, message, onEvent, controller.signal, sentAttachments);
      else if (current) await streamAgentSessionMessage(current.id, message, onEvent, controller.signal, mentioned);
      else await streamNewAgentSession({ message, ...mentioned, skillIds: selectedSkills, location: "page" }, onEvent, controller.signal);
      coalescer.flush();
      if (!retrying) {
        clearAgentDraft(agentDraftKey);
        setEditingId(undefined);
      }
    } catch (reason) {
      setPending(undefined);
      // When the server reported the failure it had already saved the question and its error row (shown with Retry),
      // so putting the text back in the composer would only invite a second copy of it.
      // A stopped reply leaves the message in the chat and the composer empty; only a send that never
      // reached the server (nothing to show yet) puts the text back.
      const stopped = reason instanceof DOMException && reason.name === "AbortError";
      if (!retrying && !answer && !failedInChat && !(stopped && started)) writeInput(message);
      if (stopped) {
        // Keep the stopped chat (the server already saved the message) in the workspace data too.
        if (started && streamed) saveSession(markAgentSessionStopped(streamed));
        return;
      }
      if (failedInChat && streamed) {
        saveSession(streamed);
        return;
      }
      setError(
        reason instanceof Error
          ? reason.message
          : t("Flow Agent is unavailable"),
      );
    } finally {
      coalescer?.flush();
      clearLiveAgentSession(streamed?.id);
	  streamAbortRef.current = undefined;
      setBusy(false);
      requestAnimationFrame(() => editorRef.current?.focus());
    }
  };
  // ⌘K "Ask Flow" (Tab) on this page sends the typed question in the chat shown here.
  const sendRef = useLatestRef(send);
  useEffect(() => {
    const ask = (event: Event) => {
      const prompt = (event as CustomEvent<AgentPageAskDetail>).detail?.prompt;
      if (prompt?.trim()) void sendRef.current(prompt);
    };
    window.addEventListener(AGENT_PAGE_ASK_EVENT, ask);
    return () => window.removeEventListener(AGENT_PAGE_ASK_EVENT, ask);
  }, [sendRef]);
  // The buttons stay off until the reply shows the approval resolved (streamed here, or saved when the reply runs
  // without this page), so a second click never answers it twice.
  const { approvalBusy, decideToolApproval, retryApproval } = useAgentApprovalAnswer({ sessionId: current?.id, parts: shownParts, onError: setError, onStale: follow.refresh });
  const phone = usePhoneLayout();
  const [historyInteracting, setHistoryInteracting] = useState(false);
  const historyListRef = useRef<HTMLDivElement>(null);
  const historyOptions = useMemo(
    () => [
      ...(!phone ? [{ id: "__new__", label: t("New chat") }] : []),
      ...[...sessions]
        .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
        .map((item) => ({ id: item.id, label: item.title })),
    ],
    [sessions, t, phone],
  );
  const {
    activeId: historyActiveId,
    choose: chooseHistory,
    filteredOptions: historyFilteredOptions,
    inputRef: historyInputRef,
    onKeyDown: onHistoryKeyDown,
    onQueryChange: onHistoryQueryChange,
    query: historyQuery,
    setActiveId: setHistoryActiveId,
  } = usePropertyCommand({
    open: historyOpen,
    options: historyOptions,
    selectedIds: current ? [current.id] : ["__new__"],
    onOpenChange: setHistoryOpen,
    onSelect: (option) => {
      if (option.id === "__new__") {
        setHistoryOpen(false);
        setActiveStreamId(undefined);
        writeInput("");
        setSelectedSkills([]);
        onNavigate(agentPath(data.workspace.urlKey));
        return;
      }
      const session = sessions.find((item) => item.id === option.id);
      if (session) onNavigate(agentPath(data.workspace.urlKey, session.slugId));
    },
  });
  const historyGroups = useMemo(
    () => {
      const filtered = historyFilteredOptions
      .filter((option) => option.id !== "__new__")
      .map((option) => sessions.find((session) => session.id === option.id))
      .filter((session): session is AgentSession => Boolean(session));
      const groups = phone
        ? (historyQuery.trim() ? [{ id: "results", label: "", count: undefined, sessions: filtered }] : buildChatHistory(filtered).sections)
          .filter((section) => section.sessions.length > 0)
          .map((section) => ({ ...section, label: section.count == null ? t(section.label) : t(section.label).replace("{count}", String(section.count)) }))
        : groupAgentHistory(filtered).map((group) => ({ ...group, id: group.label }));
      const headingHeight = phone && historyQuery.trim() ? 0 : 30;
      return groups.map((group, index) => {
        const top = 6 + groups.slice(0, index).reduce((offset, previous) => offset + headingHeight + previous.sessions.length * 46 + 12, 0);
        return { ...group, top, headingHeight };
      });
    },
    [historyFilteredOptions, sessions, phone, historyQuery, t],
  );
  const historyListHeight = historyGroups.reduce((height, group) => group.top + group.headingHeight + group.sessions.length * 46 + 6, 12);
  useEffect(() => {
    if (phone && historyOpen && historyInteracting) historyListRef.current?.querySelector('[aria-selected="true"]')?.scrollIntoView?.({ block: "nearest" });
  }, [phone, historyOpen, historyInteracting, historyActiveId]);
  // Like the reference app, the empty new-chat page opens ready to type: focus lands in the composer on arrival and whenever
  // the page goes back to a new chat. It never takes focus from another text field or an open dialog/menu.
  const emptyPage = !chatSlug && !shown;
  useEffect(() => {
    if (!emptyPage) return;
    const editor = editorRef.current;
    if (!editor || document.activeElement === editor || typingElsewhere(editor) || document.querySelector(openOverlaySelector)) return;
    editor.focus({ preventScroll: true });
    placeCaretAtEnd(editor);
  }, [emptyPage]);
  const newChat = () => {
    setHistoryOpen(false);
    setActiveStreamId(undefined);
    writeInput("");
    clearAgentDraft(agentDraftKey);
    setSelectedSkills([]);
    onNavigate(agentPath(data.workspace.urlKey));
  };
  // Deleting is immediate (no confirmation); the toast's Undo restores the chat and returns to it.
  const deleteCurrent = () => {
    if (!current) return;
    const deleted = current;
    void deleteAgentChat(deleted, {
      t,
      onDeleted: () => {
        setSessions((list) => list.filter((item) => item.id !== deleted.id));
        onSessionChange(deleted.id);
        newChat();
      },
      onRestored: commitSession,
    });
  };
  useAgentChatHotkeys(Boolean(current) && !current?.id.startsWith("pending-"), () => setRenameOpen(true), deleteCurrent);
  return (
    <main className={`${styles.page}${shown ? "" : ` ${styles.emptyPage}`}`}>
      <header>
        <button
          className={styles.mobileMenu}
          aria-label={t("Open navigation")}
          data-sidebar-trigger onClick={onOpenSidebar}
          type="button"
        />
        <Popover.Root open={historyOpen} onOpenChange={(open) => { setHistoryOpen(open); setHistoryInteracting(false); }}>
          <Popover.Trigger asChild>
            <button
              aria-expanded={historyOpen}
              aria-label={t("Switch agent chat")}
              data-agent-chat-switcher
              className={styles.switcher}
              type="button"
            >
              <h2 data-i18n-ignore={Boolean(shown) || undefined}>
                {shown?.title ?? t("New chat")}
              </h2>
              <AgentChevronDownIcon />
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content data-flow-motion="floating"
              data-phone-popover
              data-testid="agent-chat-switcher-popover"
              align="start"
              alignOffset={8}
              className={styles.historyMenu}
              side="bottom"
              sideOffset={3}
              onOpenAutoFocus={(event) => event.preventDefault()}
              onKeyDown={(event) => { if (event.key !== "Escape") setHistoryInteracting(true); onHistoryKeyDown(event); }}
            >
              <input
                ref={historyInputRef}
                autoFocus
                aria-label={t("Chat history")}
                placeholder={phone ? t("Chat history") : undefined}
                className={styles.historySearch}
                value={historyQuery}
                onChange={(event) => { setHistoryInteracting(Boolean(event.target.value.trim())); onHistoryQueryChange(event.target.value); }}
              />
              <div role="listbox" aria-label={t("Chat history")} ref={historyListRef}>
                {phone && <div className={styles.historyListSpacer} style={{ height: historyListHeight }} />}
                {historyFilteredOptions.some(({ id }) => id === "__new__") && <button
                  aria-selected={historyActiveId === "__new__"}
                  className={styles.newChat}
                  onMouseMove={() => setHistoryActiveId("__new__")}
                  onClick={() => chooseHistory(historyOptions[0])}
                  role="option"
                  type="button"
                >
                  <Plus />
                  <span>{t("New chat")}</span>
                </button>}
                {historyGroups.map((group, groupIndex) => (
                  <Fragment key={group.label}>
                    {(!phone || groupIndex > 0) && <div className={styles.historySeparator} role="separator" style={phone ? { top: group.top - 12 } : undefined}>
                      <span data-testid="agent-chat-switcher-divider" />
                    </div>}
                    {(!phone || group.headingHeight > 0) && <div aria-label={t(group.label)} data-history-group={group.id} className={styles.historyGroup} role="group" style={phone ? { top: group.top } : undefined}>{t(group.label)}</div>}
                    {group.sessions.map((item, rowIndex) => {
                      const option = historyFilteredOptions.find(({ id }) => id === item.id)!;
                      return <button
                        aria-selected={(!phone || historyInteracting) && historyActiveId === item.id}
                        data-history-section={group.id}
                        style={phone ? { top: group.top + group.headingHeight + rowIndex * 46 } : undefined}
                        key={item.id}
                        onMouseMove={() => { setHistoryInteracting(true); setHistoryActiveId(item.id); }}
                        onClick={() => chooseHistory(option)}
                        role="option"
                        type="button"
                      >
                        <i aria-hidden="true" data-unread={agentSessionUnread(item) || undefined} />
                        <strong data-i18n-ignore>{item.title}</strong>
                        <span>{current?.id === item.id ? t("Current") : ""}</span>
                        <time>{formatAgentHistoryTime(item.updatedAt, t, undefined, phone)}</time>
                      </button>;
                    })}
                  </Fragment>
                ))}
                {!historyGroups.length && (
                  <span className={styles.noHistory}>{t("No history")}</span>
                )}
              </div>
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
        {current && (
          <>
            <button
              className={styles.headerIcon}
              aria-label={t(
                current.favorite ? "Remove from favorites" : "Add to favorites",
              )}
              aria-checked={current.favorite}
              role="switch"
              onClick={() =>
                void updateAgentSession(current.id, {
                  favorite: !current.favorite,
                }).then(commitSession)
              }
              type="button"
            >
              <FlowGlyph name="favorite" size={14} />
            </button>
            <AgentChatMenu
              align="end"
              className={styles.headerIcon}
              unsaved={current.id.startsWith("pending-")}
              onCopy={() =>
                void navigator.clipboard.writeText(markdown(current)).then(
                  () => toast.info(t("Copied conversation"), { description: t("Conversation copied to clipboard as Markdown") }),
                  () => toast.error(t("Could not copy to clipboard")),
                )
              }
              onDelete={deleteCurrent}
              onFeedback={() => setSessionFeedbackOpen(true)}
              onRename={() => setRenameOpen(true)}
            />
          </>
        )}
        <span className={styles.headerSpacer} />
        {current && <button
          className={styles.headerIcon}
          aria-label={t("Move to toolbar")}
          onClick={() =>
            void updateAgentSession(current.id, {
              location: "toolbar",
            }).then((next) => {
              saveSession(next);
              newChat();
            })
          }
          type="button"
        >
          <AgentToolbarGlyph size={14} />
        </button>}
      </header>
      <section className={`${styles.body}${shown ? ` ${styles.hasConversation}` : ""}`}>
        {shown ? (
          <Conversation
            busy={replyRunning}
            data={data}
            onSuggestion={status?.enabled ? (message) => void send(message) : undefined}
            onAnswer={status?.enabled ? (part, choice) => void send(choice.label, undefined, { partId: part.id, value: choice.value }) : undefined}
            draftProject={data.projects.find((project) => shown.projectIds?.includes(project.id))}
            draftContext={data.projects.find((project) => shown.projectIds?.includes(project.id))?.name ?? shown.title}
            session={shown}
            editingId={editingId}
            onRetry={(message) => void send(message.content, message)}
            onToolApproval={decideToolApproval}
            approvalBusy={approvalBusy}
            onSessionChange={(next) => {
              setSessions((list) => list.map((item) => (item.id === next.id ? next : item)));
              onSessionChange(next.id, next);
            }}
            onLatestChange={setAwayFromLatest}
            latestRef={scrollToLatestRef}
            scrollRequest={scrollRequest}
            onEdit={(message) => {
              setEditingId(message.id);
              writeInput(message.content);
              requestAnimationFrame(() => editorRef.current?.focus());
            }}
          />
        ) : null}
        {/* Before a chat starts, the reference app stacks its faint mark, the composer and the recent chats in one centred column. */}
        <div className={shown ? styles.stagePassthrough : styles.emptyStage}>
        <div className={shown ? styles.stagePassthrough : styles.emptyColumn}>
        <div className={shown ? styles.stagePassthrough : styles.emptyComposerSlot}>
        {!shown && (phone ? <AgentWatermarkIcon className={styles.emptyLogo} /> : <FlowLogo className={styles.emptyLogo} variant="outline" />)}
        <div
          className={`${styles.composer}${shown ? ` ${styles.conversationComposer}` : ""}`}
          data-agent-composer=""
          data-editing={editingId ? "" : undefined}
          data-tooltip-elevated=""
          // The reference app's ⌘⇧U (Ctrl Shift U) opens the file picker from the composer.
          onKeyDown={(event) => {
            if (event.key.toLowerCase() === "u" && event.shiftKey && (isMacPlatform() ? event.metaKey : event.ctrlKey) && !event.altKey) {
              event.preventDefault();
              fileInputRef.current?.click();
            }
          }}
          // The editor is one line tall inside a taller box: a press on the box's empty space focuses it too.
          onMouseDown={(event) => {
            const editor = editorRef.current;
            const target = event.target as HTMLElement;
            if (event.button !== 0 || !editor || editor.getAttribute("contenteditable") !== "true" || editor.contains(target) || target.closest(composerControlSelector)) return;
            event.preventDefault();
            editor.focus({ preventScroll: true });
            placeCaretAtEnd(editor);
          }}
        >
          {editingId && (
            <div className={styles.editingBanner}>
              <span>
                <FlowGlyph name="edit" size={12} />
                <span>{t("Editing message")}</span>
              </span>
              <FlowTooltip label={t("Cancel editing")} sideOffset={8}>
                <button
                  aria-label={t("Cancel editing")}
                  onClick={() => {
                    setEditingId(undefined);
                    writeInput("");
                  }}
                  type="button"
                >
                  <FlowGlyph name="closeChat" size={14} />
                </button>
              </FlowTooltip>
            </div>
          )}
          {shown && !editingId && awayFromLatest && (
            <button
              aria-label={t("Latest — Scroll to latest")}
              className={styles.latestButton}
              onClick={() => scrollToLatestRef.current?.()}
              type="button"
            >
              <FlowGlyph name="arrowDown" size={14} />
              <span>{t("Latest")}</span>
            </button>
          )}
          {attachments.length > 0 && (
            <div className={styles.attachments}>
              {attachments.map((file, index) => (
                <span key={`${file.name}-${file.lastModified}`}>
                  <b>{file.name}</b>
                  <AttachmentRemoveButton
                    label={`${t("Remove attachment")} ${file.name}`}
                    onClick={() =>
                      setAttachments((items) =>
                        items.filter((_, itemIndex) => itemIndex !== index),
                      )
                    }
                  />
                </span>
              ))}
            </div>
          )}
          <div className={styles.editorScroll}>
            <AgentMentionInput
              editorRef={editorRef}
              className={styles.editor}
              ariaLabel={t("Send a message to Flow AI")}
              data={data}
              disabled={replyRunning}
              placeholder={shown ? t(offersChoices(shown, replyRunning) ? "Something else…" : "Reply…") : t("Ask Flow…")}
              value={input}
              onChange={(value, next) => { setInput(value); setMentions(next); }}
              onSubmit={() => void send()}
              onUploadFile={file => uploadAgentFile(file)}
              insertFilesRef={insertFilesRef}
              onBusyChange={setFilesUploading}
            />
          </div>
          <footer>
            <AgentSkillsPicker data={data} selectedIds={selectedSkills} onChange={setSelectedSkills} onNavigate={onNavigate} />
            <span />
            <FlowTooltip label={t("Attach images, files, or videos")} shortcut={attachShortcut} sideOffset={8}>
              <button
                aria-label={t("Attach images, files, or videos")}
                className={styles.attachButton}
                data-agent-attach=""
                onClick={() => fileInputRef.current?.click()}
                type="button"
              >
                <AgentAttachIcon />
              </button>
            </FlowTooltip>
            <input
              ref={fileInputRef}
              className={styles.fileInput}
              multiple
              onChange={(event) => {
                // Every file goes into the message like a pasted one.
                insertFilesRef.current?.([...(event.target.files ?? [])]);
                event.target.value = "";
              }}
              type="file"
            />
            {replyRunning ? <FlowTooltip label={t("Stop responding")} sideOffset={8}><button
              aria-label={t("Stop responding")}
              className={styles.sendButton}
              data-state="working"
              onClick={() => {
                // The reply runs on the server even without this page, so stop it there too.
                void follow.stop();
                streamAbortRef.current?.abort();
              }}
              type="button"
            ><AgentStopIcon /></button></FlowTooltip> : <FlowTooltip label={t("Send message")} shortcut="Enter" sideOffset={8}><button
              aria-label={t("Submit comment")}
              className={styles.sendButton}
              data-agent-send=""
              // Empty chats keep the accent send control visible; submitting an empty draft is a no-op.
              data-state={!status?.enabled ? "unavailable" : phone && !input.trim() ? "empty" : "ready"}
              disabled={filesUploading || !status?.enabled || (phone && !input.trim())}
              onClick={() => void send()}
              type="button"
            ><AgentSubmitIcon /></button></FlowTooltip>}
          </footer>
          {error && (
            <span className={styles.error} role="alert">
              <AgentErrorDetail error={error} onRetry={retryApproval ?? (() => void send())} />
            </span>
          )}
        </div>
        </div>
        {!shown && <AgentHistoryList
          sessions={sessions}
          hrefFor={(session) => agentPath(data.workspace.urlKey, session.slugId)}
          onOpen={(session) => onNavigate(agentPath(data.workspace.urlKey, session.slugId))}
          onOpenInToolbar={(session) => void updateAgentSession(session.id, { location: "toolbar" }).then(saveSession)}
        />}
        {!shown && !sessions.length && status?.enabled && !examplesDismissed && <section className={styles.examples} aria-label={t("Get started with some examples")}>
          <p>{t("Get started with some examples")}</p>
          <button
            aria-label={t("Dismiss examples")}
            className={styles.examplesDismiss}
            onClick={() => {
              setExamplesDismissed(true);
              try { window.localStorage.setItem("flow:agent-examples-dismissed", "1"); } catch { /* storage unavailable */ }
            }}
            type="button"
          ><X size={14} /></button>
          <div className={styles.exampleGrid}>
            {([
              { Icon: Box, title: "Summarize the latest project updates", description: "Pull together a concise update from recent project activity." },
              { Icon: Search, title: "Research a topic for my team", description: "Find useful context and turn it into clear next steps." },
              { Icon: Workflow, title: "Build a workflow for recurring work", description: "Describe a repetitive process and shape it into a Loop." },
            ] satisfies { Icon: LucideIcon; title: string; description: string }[]).map(({ Icon, title, description }) => <button className={styles.exampleCard} key={title} type="button" onClick={() => writeInput(t(title))}>
              <Icon className={styles.exampleIcon} aria-hidden="true" size={14} />
              <strong>{t(title)}</strong>
              <span>{t(description)}</span>
            </button>)}
          </div>
        </section>}
        </div>
        </div>
      </section>
      {current && (
        <AgentSessionFeedbackDialog
          open={sessionFeedbackOpen}
          onOpenChange={setSessionFeedbackOpen}
          sessionId={current.id}
          onSessionChange={(next) => {
            setSessions((list) => list.map((item) => (item.id === next.id ? next : item)));
            onSessionChange(next.id, next);
          }}
        />
      )}
      {current && (
        <AgentRenamePalette
          open={renameOpen}
          title={current.title}
          onOpenChange={setRenameOpen}
          onRename={(title) =>
            void updateAgentSession(current.id, { title }).then(
              (next) => {
                // The slug follows the title, so the address moves to the renamed chat's new one.
                setSessions((list) => list.map((item) => (item.id === next.id ? { ...item, title: next.title, slugId: next.slugId, updatedAt: next.updatedAt } : item)));
                onSessionChange(next.id, { ...current, title: next.title, slugId: next.slugId, updatedAt: next.updatedAt });
                onNavigate(agentPath(data.workspace.urlKey, next.slugId));
              },
              (reason) => toast.error(reason instanceof Error ? reason.message : t("Could not rename chat")),
            )
          }
        />
      )}
    </main>
  );
}

function Conversation({
  busy,
  data,
  onSuggestion,
  draftContext,
  draftProject,
  editingId,
  onEdit,
  onRetry,
  onToolApproval,
  approvalBusy,
  onAnswer,
  onSessionChange,
  onLatestChange,
  latestRef,
  scrollRequest,
  session,
}: {
  busy: boolean;
  /** Answers the reply's pending question with one of its options. */
  onAnswer?: (part: AgentMessagePart, choice: { label: string; value: string }) => void;
  /** Whether the newest message is scrolled out of view (shows the "Latest" pill). */
  onLatestChange?: (away: boolean) => void;
  /** Receives a function that scrolls to the newest message. */
  latestRef?: RefObject<(() => void) | undefined>;
  /** Incremented when a new user message is sent, even if the reader was scrolled up. */
  scrollRequest?: number;
  /** A reply's rating changed on the server. */
  onSessionChange: (session: AgentSession) => void;
  data: BootstrapData;
  /** Sends a follow-up suggestion chip as the next message. */
  onSuggestion?: (message: string) => void;
  draftContext: string;
  draftProject?: Project;
  editingId?: string;
  onEdit: (message: AgentSession["messages"][number]) => void;
  onRetry: (message: AgentMessage) => void;
  onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void;
  approvalBusy?: string;
  session: AgentSession;
}) {
  const { t } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);
  const followLatestRef = useRef(true);
  const newestIsUser = session.messages.at(-1)?.role === "user";
  useEffect(() => {
    const viewport = scrollRef.current;
    if (!viewport) return;
    if (newestIsUser) followLatestRef.current = true;
    if (!followLatestRef.current) return;
    requestAnimationFrame(() => {
      // Phones keep the top of a conversation in view when its overflow is only the transcript's bottom padding,
      // like the reference app; a longer transcript still opens at the newest message.
      if (window.matchMedia?.("(max-width: 767px)").matches && viewport.scrollHeight - viewport.clientHeight <= 48) return;
      if (typeof viewport.scrollTo === "function") viewport.scrollTo({ top: viewport.scrollHeight });
      else viewport.scrollTop = viewport.scrollHeight;
    });
  }, [newestIsUser, session.messages.length, session.updatedAt, scrollRequest]);
  useEffect(() => {
    if (!scrollRequest) return;
    // A send is an explicit request to follow the new bubble and its reply;
    // streaming updates after this still respect a later manual scroll.
    followLatestRef.current = true;
    const viewport = scrollRef.current;
    if (!viewport) return;
    const frame = requestAnimationFrame(() => { viewport.scrollTop = viewport.scrollHeight; });
    return () => cancelAnimationFrame(frame);
  }, [scrollRequest]);
  // The reference app's "Latest" pill shows once the newest message is out of view.
  useEffect(() => {
    const viewport = scrollRef.current;
    if (!viewport) return;
    const update = () => onLatestChange?.(viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight > 48);
    const trackFollow = () => { followLatestRef.current = viewport.scrollHeight - viewport.scrollTop - viewport.clientHeight <= 48; };
    // The reference app follows the newest message while a phone transcript grows (an expanded step list),
    // unless the reader had scrolled away from the bottom.
    const grow = () => {
      if (!window.matchMedia?.("(max-width: 767px)").matches || !followLatestRef.current) return;
      if (viewport.scrollHeight - viewport.clientHeight <= 48) return;
      viewport.scrollTop = viewport.scrollHeight;
    };
    update();
    trackFollow();
    viewport.addEventListener("scroll", update, { passive: true });
    viewport.addEventListener("scroll", trackFollow, { passive: true });
    const observer = typeof ResizeObserver === "function" ? new ResizeObserver(() => { update(); grow(); }) : undefined;
    observer?.observe(viewport);
    if (viewport.firstElementChild) observer?.observe(viewport.firstElementChild);
    return () => {
      viewport.removeEventListener("scroll", update);
      viewport.removeEventListener("scroll", trackFollow);
      observer?.disconnect();
      onLatestChange?.(false);
    };
  }, [onLatestChange]);
  useEffect(() => {
    if (!latestRef) return;
    latestRef.current = () => {
      const viewport = scrollRef.current;
      if (viewport && typeof viewport.scrollTo === "function") viewport.scrollTo({ top: viewport.scrollHeight, behavior: "smooth" });
      else if (viewport) viewport.scrollTop = viewport.scrollHeight;
    };
    return () => { latestRef.current = undefined; };
  }, [latestRef]);
  const latestAssistantIndex = session.messages.findLastIndex((message) => message.role === "assistant");
  // The files a reply's read_file / transcribe_media rows name, collected once per conversation update.
  const sessionAttachments = useMemo(() => session.messages.flatMap((item) => item.attachments ?? []), [session.messages]);
  return (
    <div
      ref={scrollRef}
      aria-label={t("Agent conversation")}
      className={styles.conversation}
      role="group"
    >
      <div className={styles.conversationInner}>
        {visibleAgentMessages(session.messages, editingId).map((savedMessage, index) => {
          // A stopped reply keeps only its work: no partial text, "stopped" row, Retry or rating.
          const message = settleStoppedAgentReply(savedMessage);
          // Retry runs the question again, so only the newest reply offers it (never an older error in the history).
          const retryTarget = message.role === "assistant" ? lastUserMessage(session.messages, index) : undefined;
          const waiting = busy && index === session.messages.length - 1 && message.role === "assistant" && !message.content && !message.parts?.length;
          const answer: AnswerProps = message.role === "assistant"
            ? { data, onSuggestion: index === latestAssistantIndex && !busy ? onSuggestion : undefined, streaming: busy && index === session.messages.length - 1 }
            : {};
          // An option picked on a question shows as the chosen pill there, not as a separate message.
          if (message.role === "user" && message.answerTo) return null;
          if (message.role === "assistant" && isBlankStoppedReply(savedMessage)) return null;
          return (
          <Fragment key={message.id}>
            {shouldShowAgentTime(session.messages, index) && (
              <time className={styles.messageTime} dateTime={message.createdAt}>
                {formatAgentTime(message.createdAt || session.createdAt, t("Today"))}
              </time>
            )}
            <article
              className={`${message.role === "user" ? styles.user : styles.assistant}${editingId === message.id ? ` ${styles.isEditing}` : ""}`}
              data-message-id={message.id}
            >
              <div className={styles.messageContent}>
                {waiting
                  ? <div aria-live="polite" className={styles.thinkingPlaceholder}><span className={styles.workShimmer}>{t("Thinking…")}</span></div>
                  : message.parts?.length
                    ? <AgentMessageParts {...answer} attachments={sessionAttachments} changes={{ sessionId: session.id.startsWith("pending-") ? undefined : session.id, onSessionChange }} onAnswer={index === latestAssistantIndex ? onAnswer : undefined} answerDisabled={busy} draftContext={draftContext} draftProject={draftProject} message={message} onRetry={retryTarget && !busy && index === session.messages.length - 1 ? () => onRetry(retryTarget) : undefined} onToolApproval={onToolApproval} approvalBusy={approvalBusy}/>
                    : message.role === "user"
                      ? <AgentContentWithBlocks content={message.content} renderText={(text) => <AgentMessageText {...answer} autoLinkEmails={false} content={text} draftContext={draftContext} draftProject={draftProject}/>}/>
                      : <AgentMessageText {...answer} content={message.content} draftContext={draftContext} draftProject={draftProject}/>}
                {message.role === "user" && <AgentMessageAttachments attachments={message.attachments?.filter((item) => !item.url || !message.content.includes(`](${item.url})`))}/>}
              </div>
              {message.role === "user"
                ? <AgentUserMessageActions className={styles.messageActions} content={message.content} onEdit={message.id.startsWith("pending-") ? undefined : () => onEdit(message)} />
                : !waiting && !(busy && index === session.messages.length - 1) && !message.parts?.some(part => part.type === "question" && part.question) && (message.content.trim() || message.parts?.some(part => part.type === "text" && part.text?.trim())) && (
                  <AgentReplyActions
                    className={`${styles.replyActions}${index === latestAssistantIndex ? ` ${styles.latestReplyActions}` : ""}`}
                    content={splitAgentSuggestions(message.parts?.filter(part => part.type === "text").map(part => part.text ?? "").join("") || message.content).prose}
                    message={message}
                    onSessionChange={onSessionChange}
                    sessionId={session.id.startsWith("pending-") ? undefined : session.id}
                  />
                )}
            </article>
          </Fragment>
        )})}
      </div>
    </div>
  );
}

/** Answer chrome for an assistant reply: entity chips need `data`; suggestion chips show only when `onSuggestion` is set. */
type AnswerProps = { data?: BootstrapData; onSuggestion?: (message: string) => void; streaming?: boolean };

function AgentMessageParts({ attachments, changes, draftContext, draftProject, message, onRetry, onToolApproval, approvalBusy, onAnswer, answerDisabled, ...answer }: AnswerProps & { attachments?: AgentMessageAttachment[]; changes?: { sessionId?: string; onSessionChange?: (session: AgentSession) => void }; draftContext: string; draftProject?: Project; message: AgentMessage; onRetry?: () => void; onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string; onAnswer?: (part: AgentMessagePart, choice: { label: string; value: string }) => void; answerDisabled?: boolean }) {
  const { t } = useI18n();
  const question = message.parts?.find(part => part.type === "question" && part.question);
  // A reply that stopped on a question keeps its text in the text part; its content also ends with the question.
  const text = message.parts?.filter(part => part.type === "text").map(part => part.text ?? "").join("") || (question ? "" : message.content);
  const work = message.parts?.filter(part => part.type === "reasoning" || part.type === "step" || part.type === "toolCall") ?? [];
  const other = message.parts?.filter(part => !["text", "reasoning", "step", "toolCall", "question"].includes(part.type)) ?? [];
  const queue = summarizeElicitationQueue(other);
  const submitting = other.some(part => part.type === "elicitation" && part.status === "running");
  // The reference app's result cards for what the reply did ("Created DEV-50", "Updated project" with Undo).
  const changeCards = <AgentChangeCards data={answer.data} message={message} onSessionChange={changes?.onSessionChange} sessionId={changes?.sessionId}/>;
  const changedKeys = useMemo(() => agentChangeEntityKeys(message), [message]);
  return <div className={styles.messageParts}>
    {/* Like the reference app, the work folds to "Worked for N seconds ▸" once the answer starts. */}
    {work.length > 0 && <AgentWorkGroup attachments={attachments} message={message} parts={work} onToolApproval={onToolApproval} approvalBusy={approvalBusy} running={Boolean(answer.streaming) && !text} collapseWhenDone/>}
    <AgentElicitationResponseQueue answeredCount={queue.answeredCount} elicitationCount={queue.elicitationCount} isSubmitting={submitting} />
    {other.map(part => part.type === "elicitation" ? <AgentElicitation key={part.id} part={part}/> : part.type === "error"
      ? <div className={styles.partError} key={part.id} role="alert"><AlertCircle/><AgentErrorDetail error={part.text ?? ""}/>{onRetry && <button onClick={onRetry} type="button">{t("Retry")}</button>}</div>
      : <div className={styles.eventPart} key={part.id}><span>{part.text}</span></div>)}
    {text ? <AgentMessageText {...answer} changeCards={changeCards} changedKeys={changedKeys} onSuggestion={question ? undefined : answer.onSuggestion} content={text} draftContext={draftContext} draftProject={draftProject}/> : changeCards}
    {/* coding sessions (track G) */}
    <ChatCodingSessionCards message={message} data={answer.data}/>
    {question?.question && <div className={styles.question}>
      <AgentQuestionPrompt className={`${styles.messageDocument} agent-chat-document`} data={answer.data} disabled={Boolean(answerDisabled) || question.status !== "pending"} onSelect={choice => onAnswer?.(question, choice)} part={question}/>
    </div>}
  </div>;
}

/**
 * Message text with any ```update block shown as the reference app's "Created draft" card instead of raw code. Assistant replies
 * (`data` set) also get the reference app's answer chrome: inline entity chips, referenced issues and follow-up suggestions.
 */
function AgentMessageText({ autoLinkEmails = true, changeCards, changedKeys, content, data, draftContext, draftProject, onSuggestion, streaming = false }: AnswerProps & { autoLinkEmails?: boolean; changeCards?: ReactNode; changedKeys?: string[]; content: string; draftContext: string; draftProject?: Project }) {
  const { prose, draft } = splitAgentDraft(content, "update");
  const answer = useMemo(() => data ? parseAgentAnswer(prose, data) : undefined, [data, prose]);
  const draftCard = draft && <AgentDraftCard context={draftContext} draft={draft} icon={draftProject ? <span style={{ color: healthColor(draftProject.health), display: "inline-flex" }}><HealthGlyph health={draftProject.health}/></span> : undefined} title="Update draft"/>;
  if (!answer) return <>
    {prose && <AgentRichText autoLinkEmails={autoLinkEmails} className={`${styles.messageDocument} agent-chat-document`} content={prose}/>}
    {changeCards}
    {draftCard}
  </>;
  return <>
    {answer.markdown && <AgentAnswerText className={`${styles.messageDocument} agent-chat-document`} data={data} markdown={answer.markdown}/>}
    {!streaming && <AgentReferencedResources data={data} exclude={changedKeys} references={answer.references}/>}
    {changeCards}
    {draftCard}
    {onSuggestion && <AgentSuggestionChips onSelect={onSuggestion} suggestions={answer.suggestions}/>}
  </>;
}

/** Retry schedule (ms) for picking up a chat title generated after the reply. */
const titleRetryDelays = [1500, 5000, 12000, 25000];

/**
 * The conversation as it looks the moment a message is sent: the user's message (replacing the edited one and
 * everything after it) followed by an empty assistant reply, which renders as the working indicator.
 */
function optimisticAgentTurn(session: AgentSession | undefined, message: string, editingId?: string, mentions: AgentMention[] = [], answer?: { partId: string; value: string }, attachments?: AgentMessageAttachment[]): AgentSession {
  const now = new Date().toISOString();
  const earlier = (session?.messages ?? []).map((item) => answer && item.parts?.some((part) => part.id === answer.partId)
    ? { ...item, parts: item.parts.map((part) => part.id === answer.partId && part.question ? { ...part, status: "completed" as const, question: { ...part.question, selected: answer.value } } : part) }
    : item);
  const kept = editingId && earlier.some((item) => item.id === editingId) ? earlier.slice(0, earlier.findIndex((item) => item.id === editingId)) : earlier;
  const stamp = Date.now();
  const messages: AgentMessage[] = [
    ...kept,
    { id: `pending-user-${stamp}`, role: "user", content: message, mentions: mentions.length ? mentions.map(({ type, id, label }) => ({ type, id, label })) : undefined, answerTo: answer?.partId, attachments: attachments?.length ? attachments : undefined, createdAt: now },
    { id: `pending-reply-${stamp}`, role: "assistant", content: "", parts: [], createdAt: now },
  ];
  if (session) return { ...session, messages, updatedAt: now };
  return { id: `pending-${stamp}`, slugId: "", userId: "", title: (message.replace(/!\[[^\]\n]*\]\([^)\s]+\)/g, "").trim() || "Image").split("\n")[0].slice(0, 80), favorite: false, location: "page", issueIds: [], skillIds: [], messages, createdAt: now, updatedAt: now };
}

/**
 * The reference app's composer reads "Something else…" while the newest reply offers choices (a pending question's options or
 * follow-up suggestions), since typing then answers instead of picking.
 */
function offersChoices(session: AgentSession, busy: boolean) {
  const last = session.messages.at(-1);
  if (busy || last?.role !== "assistant") return false;
  if (last.parts?.some((part) => part.type === "question" && part.status === "pending")) return true;
  return splitAgentSuggestions(last.content).suggestions.length > 0;
}

/** While a message is being edited the reference app hides it and everything after it (the composer holds its text). */
function visibleAgentMessages(messages: AgentMessage[], editingId?: string) {
  const index = editingId ? messages.findIndex((message) => message.id === editingId) : -1;
  return index < 0 ? messages : messages.slice(0, index);
}

/** The saved user message a reply answers (never a not-yet-saved local copy). */
function lastUserMessage(messages: AgentMessage[], before: number) {
  for (let index = before; index >= 0; index--) {
    const message = messages[index];
    if (message.role !== "user") continue;
    return message.content.trim() && !message.id.startsWith("pending-") ? message : undefined;
  }
  return undefined;
}

function markdown(session: AgentSession) {
  return session.messages
    .map(
      (message) =>
        `**${message.role === "user" ? "You" : "Flow Agent"}**\n\n${message.role === "user" ? message.content : splitAgentSuggestions(message.content).prose}`,
    )
    .join("\n\n");
}


/** Controls inside the composer that keep their own press behaviour. */
const composerControlSelector = 'button, a, input, select, textarea, label, [role="button"], [role="menuitem"], [contenteditable="true"]';
/** Overlays that own the keyboard while open (same notion as the global shortcut guard). */
const openOverlaySelector = '[role="dialog"], [role="menu"][data-state="open"], [data-radix-popper-content-wrapper]';

function typingElsewhere(editor: HTMLElement) {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement) || active === document.body || active === editor) return false;
  return active instanceof HTMLInputElement || active instanceof HTMLTextAreaElement || active.isContentEditable || active.getAttribute("contenteditable") === "true";
}

function placeCaretAtEnd(editor: HTMLElement | null) {
  const selection = window.getSelection();
  if (!editor || !selection) return;
  const range = document.createRange();
  range.selectNodeContents(editor);
  range.collapse(false);
  selection.removeAllRanges();
  selection.addRange(range);
}

function writeInputToEditor(editorRef: RefObject<HTMLDivElement | null>, value: string) {
  const editor = editorRef.current;
  if (!editor) return;
  // A message with pasted files comes back with its blocks (editing it, restoring a draft).
  if (splitAgentBlocks(value).some((part) => part.type !== "text")) fillAgentComposer(editor, value);
  else if (editor.textContent !== value) editor.textContent = value;
}

/** Shows the working indicator under a message whose reply is still on its way. */
function withReplyPlaceholder(session: AgentSession): AgentSession {
  const last = session.messages.at(-1);
  return { ...session, messages: [...session.messages, { id: `awaiting-reply-${last?.id ?? session.id}`, role: "assistant", content: "", parts: [], createdAt: last?.createdAt ?? session.updatedAt }] };
}
