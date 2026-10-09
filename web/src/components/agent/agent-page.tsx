import { Fragment, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as Popover from "@radix-ui/react-popover";
import {
  AlertCircle,
  Copy,
  MoreHorizontal,
  PanelTop,
  Plus,
  Star,
  Trash2,
  X,
} from "lucide-react";
import {
  deleteAgentSession,
  fetchAgentStatus,
  getAgentSession,
  stopAgentSession,
  resolveAgentApproval,
  updateAgentSession,
} from "@/lib/api";
import { streamAgentSessionMessage, streamAgentSessionMessageEdit, streamNewAgentSession, type AgentStreamEvent } from "@/lib/agent-stream";
import { agentPath } from "@/lib/app-routes";
import type { AgentMessage, AgentSession, AgentStatus, AgentToolCall, BootstrapData, Project } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { usePropertyCommand } from "@/components/property/use-property-command";
import {
  AgentAttachIcon,
  AgentChevronDownIcon,
  AgentStopIcon,
  AgentSubmitIcon,
} from "./agent-icons";
import { AgentRichText } from "./agent-rich-text";
import { AgentDraftCard } from "./agent-draft-card";
import { HealthGlyph, healthColor } from "@/components/project-detail/health-glyph";
import { splitAgentDraft } from "./agent-draft";
import { AgentAnswerText, AgentReferencedResources, AgentSuggestionChips } from "./agent-answer";
import { parseAgentAnswer, splitAgentSuggestions } from "./agent-answer-content";
import { AgentWorkGroup } from "./agent-work-group";
import { formatAgentTime, shouldShowAgentTime } from "./agent-time";
import { clearAgentDraft, readAgentDraft, writeAgentDraft } from "./agent-drafts";
import styles from "./agent-page.module.css";
import { AgentMentionInput, type AgentMention } from "./agent-mention-input";
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { AGENT_ATTACHMENT_ACCEPT, addAgentAttachments, agentFileContext } from './agent-attachments'
import { AgentSkillsPicker } from './agent-skills-picker'
import { applyAgentStreamEvent, markAgentSessionStopped } from './agent-stream-state'
import { clearLiveAgentSession, liveAgentSession, setLiveAgentSession, useLiveAgentSessionsVersion } from './agent-live-sessions'
import { AgentElicitation } from './agent-elicitation';
import {
  asPersistedConversation,
  useDeferredHydratedConversation,
} from '@/hooks/use-deferred-hydrated-conversation';
import { AgentElicitationResponseQueue, summarizeElicitationQueue } from './agent-elicitation-response-queue';
import { FlowLogo } from '@/components/ui/flow-logo';
import { AgentHistoryList } from './agent-history-list';
import { agentSessionUnread, formatAgentHistoryTime, useMarkAgentSessionRead } from './agent-read-state';

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
    [mentions, setMentions] = useState<AgentMention[]>([]),
    [selectedSkills, setSelectedSkills] = useState<string[]>([]),
    [deleteTarget, setDeleteTarget] = useState<AgentSession>(),
    [editingId, setEditingId] = useState<string>(),
    [approvalBusy, setApprovalBusy] = useState<string>(),
    [activeStreamId, setActiveStreamId] = useState<string>(),
    // The message just sent, shown with a working indicator until the server's stream catches up.
    [pending, setPending] = useState<AgentSession>(),
    [attachments, setAttachments] = useState<File[]>([]);
  const editorRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const streamAbortRef = useRef<AbortController | undefined>(undefined);
  // Chats already re-read from the server since this page opened.
  const hydratedIdsRef = useRef(new Set<string>());
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
  useEffect(() => setSessions(data.agentSessions ?? []), [data.agentSessions]);
  useEffect(() => {
    if (chatSlug || !restoredAgentDraft.current?.input) return;
    const restored = restoredAgentDraft.current;
    setInput(restored.input);
    setSelectedSkills(restored.skillIds);
    requestAnimationFrame(() => writeInputToEditor(editorRef, restored.input));
  }, [chatSlug]);
  useEffect(() => {
    if (historyRequested) setHistoryOpen(true);
  }, [chatSlug, historyRequested]);
  useEffect(() => {
    let active = true;
    fetchAgentStatus()
      .then((next) => active && setStatus(next))
      .catch(() => active && setStatus({ enabled: false, model: "" }));
    return () => {
      active = false;
    };
  }, []);
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
    if (hydratedIdsRef.current.has(currentSummary.id)) return currentSummary;
    return asPersistedConversation(currentSummary, async (id) => {
      const full = await getAgentSession(id);
      hydratedIdsRef.current.add(id);
      setSessions((list) =>
        list.map((item) => (item.id === full.id ? full : item)),
      );
      return full;
    });
  }, [currentSummary]);
  const hydratedCurrent = useDeferredHydratedConversation(deferredConversation) ?? undefined;
  const current = live ?? hydratedCurrent;
  // The last message is still waiting for its reply on the server (the page
  // that asked was closed or reloaded): poll just this chat until it lands.
  const awaitingReply = Boolean(!busy && !live && current && current.messages.at(-1)?.role === "user" && Date.now() - Date.parse(current.messages.at(-1)?.createdAt ?? "") < 10 * 60_000);
  useEffect(() => {
    if (!awaitingReply || !current) return;
    const id = current.id;
    let active = true;
    const timer = window.setInterval(() => {
      void getAgentSession(id).then((latest) => {
        if (!active || latest.messages.at(-1)?.role !== "assistant") return;
        setSessions((list) => list.map((item) => (item.id === latest.id ? latest : item)));
        onSessionChange(latest.id, latest);
      }).catch(() => undefined);
    }, 2000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [awaitingReply, current?.id]);
  const replyRunning = busy || Boolean(live) || awaitingReply;
  // Seeing a chat (once its reply has settled) clears its unread dot here and in the history list.
  useMarkAgentSessionRead(current, replyRunning, (read, lastReadAt) => {
    const listed = sessions.find((item) => item.id === read.id);
    setSessions((list) => list.map((item) => (item.id === read.id ? { ...item, lastReadAt } : item)));
    onSessionChange(read.id, listed ? { ...listed, lastReadAt } : read);
  });
  const shown = pending ?? (awaitingReply && current ? withReplyPlaceholder(current) : current);
  useEffect(() => {
    if (chatSlug || current || !input.trim()) {
      if (!chatSlug && !current && !input.trim()) clearAgentDraft(agentDraftKey);
      return;
    }
    const timer = window.setTimeout(() => writeAgentDraft(agentDraftKey, { input, skillIds: selectedSkills }), 250);
    return () => window.clearTimeout(timer);
  }, [agentDraftKey, chatSlug, current, input, selectedSkills]);
  useEffect(
    () => setSelectedSkills(current?.skillIds ?? []),
    [current?.id, current?.skillIds],
  );
  const writeInput = (value: string) => {
    setInput(value);
    if (editorRef.current && editorRef.current.textContent !== value)
      editorRef.current.textContent = value;
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
  const send = async (message = input) => {
    message = message.trim();
    if (!message || replyRunning || !status?.enabled) return;
    setBusy(true);
    setError(undefined);
    // Linear shows the sent message and its working state at once; the stream replaces this copy when it starts.
    setPending(optimisticAgentTurn(current, message, editingId, mentions));
    let streamed = current, started = false;
    try {
      const attachmentContext = await Promise.all(
        attachments.map(agentFileContext),
      );
      const providerMessage = attachmentContext.length
        ? `${message}\n\n${attachmentContext.join("\n\n")}`
        : message;
      const controller = new AbortController();
      streamAbortRef.current = controller;
      const onEvent = (event: AgentStreamEvent) => {
          streamed = applyAgentStreamEvent(streamed, event);
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
            onSessionChange(next.id, next);
            // Don't pull someone back to the chat if they've left the page.
            if (mountedRef.current) onNavigate(agentPath(data.workspace.urlKey, next.slugId));
            if (next.messages.length <= 2) refreshTitle(next);
          }
      };
      const mentioned = {
        issueIds: mentions.filter((item) => item.type === "issue").map((item) => item.id),
        projectIds: mentions.filter((item) => item.type === "project").map((item) => item.id),
        documentIds: mentions.filter((item) => item.type === "document").map((item) => item.id),
        userIds: mentions.filter((item) => item.type === "user").map((item) => item.id),
        mentions,
      };
      // Linear clears the composer as soon as the message is sent; restore it below if sending fails.
      setMentions([]);
      writeInput("");
      setAttachments([]);
      if (current && editingId) await streamAgentSessionMessageEdit(current.id, editingId, providerMessage, onEvent, controller.signal);
      else if (current) await streamAgentSessionMessage(current.id, providerMessage, onEvent, controller.signal, mentioned);
      else await streamNewAgentSession({ message: providerMessage, ...mentioned, skillIds: selectedSkills, location: "page" }, onEvent, controller.signal);
      clearAgentDraft(agentDraftKey);
      setEditingId(undefined);
    } catch (reason) {
      setPending(undefined);
      writeInput(message);
      if (reason instanceof DOMException && reason.name === "AbortError") {
        // Keep the stopped chat (the server already saved the message) in the workspace data too.
        if (started && streamed) saveSession(markAgentSessionStopped(streamed));
        return;
      }
      setError(
        reason instanceof Error
          ? reason.message
          : t("Flow Agent is unavailable"),
      );
    } finally {
      clearLiveAgentSession(streamed?.id);
	  streamAbortRef.current = undefined;
      setBusy(false);
      requestAnimationFrame(() => editorRef.current?.focus());
    }
  };
  const decideToolApproval = async (call: AgentToolCall | undefined, decision: "approve" | "reject") => {
    if (!current || !call?.approvalId || approvalBusy) return;
    setApprovalBusy(call.approvalId);
    setError(undefined);
    try {
      await resolveAgentApproval(current.id, call.approvalId, decision);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t("Flow Agent is unavailable"));
    } finally {
      setApprovalBusy(undefined);
    }
  };
  const historyOptions = useMemo(
    () => [
      { id: "__new__", label: t("New chat") },
      ...[...sessions]
        .sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt))
        .map((item) => ({ id: item.id, label: item.title })),
    ],
    [sessions, t],
  );
  const historyCommand = usePropertyCommand({
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
    () => groupAgentHistory(historyCommand.filteredOptions
      .filter((option) => option.id !== "__new__")
      .map((option) => sessions.find((session) => session.id === option.id))
      .filter((session): session is AgentSession => Boolean(session))),
    [historyCommand.filteredOptions, sessions],
  );
  const newChat = () => {
    setHistoryOpen(false);
    setActiveStreamId(undefined);
    writeInput("");
    clearAgentDraft(agentDraftKey);
    setSelectedSkills([]);
    onNavigate(agentPath(data.workspace.urlKey));
  };
  return (
    <main className={`${styles.page}${shown ? "" : ` ${styles.emptyPage}`}`}>
      <header>
        <button
          className={styles.mobileMenu}
          aria-label={t("Open navigation")}
          data-sidebar-trigger onClick={onOpenSidebar}
          type="button"
        >
          ☰
        </button>
        <Popover.Root open={historyOpen} onOpenChange={setHistoryOpen}>
          <Popover.Trigger asChild>
            <button
              aria-expanded={historyOpen}
              aria-label={t("Switch agent chat")}
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
              align="start"
              alignOffset={8}
              className={styles.historyMenu}
              side="bottom"
              sideOffset={3}
              onOpenAutoFocus={(event) => event.preventDefault()}
              onKeyDown={historyCommand.onKeyDown}
            >
              <input
                ref={historyCommand.inputRef}
                autoFocus
                aria-label={t("Chat history")}
                className={styles.historySearch}
                value={historyCommand.query}
                onChange={(event) => historyCommand.onQueryChange(event.target.value)}
              />
              <div role="listbox">
                {historyCommand.filteredOptions.some(({ id }) => id === "__new__") && <button
                  aria-selected={historyCommand.activeId === "__new__"}
                  className={styles.newChat}
                  onMouseMove={() => historyCommand.setActiveId("__new__")}
                  onClick={() => historyCommand.choose(historyOptions[0])}
                  role="option"
                  type="button"
                >
                  <Plus />
                  <span>{t("New chat")}</span>
                </button>}
                {historyGroups.map((group) => (
                  <Fragment key={group.label}>
                    <div className={styles.historySeparator} role="separator" />
                    <div aria-label={t(group.label)} className={styles.historyGroup} role="group">{t(group.label)}</div>
                    {group.sessions.map((item) => {
                      const option = historyCommand.filteredOptions.find(({ id }) => id === item.id)!;
                      return <button
                        aria-selected={historyCommand.activeId === item.id}
                        key={item.id}
                        onMouseMove={() => historyCommand.setActiveId(item.id)}
                        onClick={() => historyCommand.choose(option)}
                        role="option"
                        type="button"
                      >
                        <i aria-hidden="true" data-unread={agentSessionUnread(item) || undefined} />
                        <strong data-i18n-ignore>{item.title}</strong>
                        <span>{current?.id === item.id ? t("Current") : ""}</span>
                        <time>{formatAgentHistoryTime(item.updatedAt, t)}</time>
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
              <Star />
            </button>
            <DropdownMenu.Root>
              <DropdownMenu.Trigger asChild>
                <button
                  className={styles.headerIcon}
                  aria-label={t("Chat options")}
                  type="button"
                >
                  <MoreHorizontal />
                </button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <DropdownMenu.Content data-flow-motion="floating"
                  align="end"
                  className={styles.optionsMenu}
                  sideOffset={4}
                >
                  <DropdownMenu.Item
                    onSelect={() =>
                      void navigator.clipboard.writeText(markdown(current))
                    }
                  >
                    <Copy />
                    {t("Copy as markdown")}
                  </DropdownMenu.Item>
                  <DropdownMenu.Separator />
                  <DropdownMenu.Item
                    className={styles.danger}
                    onSelect={() => setDeleteTarget(current)}
                  >
                    <Trash2 />
                    {t("Delete")}
                  </DropdownMenu.Item>
                </DropdownMenu.Content>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
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
          <PanelTop />
        </button>}
      </header>
      <section className={`${styles.body}${shown ? ` ${styles.hasConversation}` : ""}`}>
        {shown ? (
          <Conversation
            busy={replyRunning}
            data={data}
            onSuggestion={status?.enabled ? (message) => void send(message) : undefined}
            draftProject={data.projects.find((project) => shown.projectIds?.includes(project.id))}
            draftContext={data.projects.find((project) => shown.projectIds?.includes(project.id))?.name ?? shown.title}
            session={shown}
            editingId={editingId}
            onRetry={(message) => void send(message)}
            onToolApproval={decideToolApproval}
            approvalBusy={approvalBusy}
            onEdit={(message) => {
              setEditingId(message.id);
              writeInput(message.content);
              requestAnimationFrame(() => editorRef.current?.focus());
            }}
          />
        ) : null}
        {editingId && (
          <div className={styles.editing}>
            <span>{t("Editing message")}</span>
            <button
              aria-label={t("Cancel editing")}
              onClick={() => {
                setEditingId(undefined);
                writeInput("");
              }}
              type="button"
            >
              <X />
            </button>
          </div>
        )}
        {/* Before a chat starts, Linear stacks its faint mark, the composer and the recent chats in one centred column. */}
        <div className={shown ? styles.stagePassthrough : styles.emptyStage}>
        <div className={shown ? styles.stagePassthrough : styles.emptyColumn}>
        <div className={shown ? styles.stagePassthrough : styles.emptyComposerSlot}>
        {!shown && <FlowLogo className={styles.emptyLogo} variant="outline" />}
        <div className={`${styles.composer}${shown ? ` ${styles.conversationComposer}` : ""}`}>
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
              placeholder={shown ? t("Reply…") : t("Ask Flow…")}
              value={input}
              onChange={(value, next) => { setInput(value); setMentions(next); }}
              onSubmit={() => void send()}
            />
          </div>
          <footer>
            <AgentSkillsPicker data={data} selectedIds={selectedSkills} onChange={setSelectedSkills} onNavigate={onNavigate} />
            <span />
            <button
              aria-label={t("Attach images, files, or videos")}
              onClick={() => fileInputRef.current?.click()}
              type="button"
            >
              <AgentAttachIcon />
            </button>
            <input
              ref={fileInputRef}
              accept={AGENT_ATTACHMENT_ACCEPT}
              className={styles.fileInput}
              multiple
              onChange={(event) => {
                const files = event.target.files;
                setAttachments((items) => addAgentAttachments(items, files));
                event.target.value = "";
              }}
              type="file"
            />
            {replyRunning ? <button
              aria-label={t("Stop responding")}
              className={styles.sendButton}
              data-state="working"
              onClick={() => {
                // The reply runs on the server even without this page, so stop it there too.
                if (current) void stopAgentSession(current.id).catch(() => undefined);
                streamAbortRef.current?.abort();
              }}
              type="button"
            ><AgentStopIcon /></button> : <button
              aria-label={t("Submit comment")}
              className={styles.sendButton}
              // Like Linear, an empty composer keeps the quiet send button (hover included); text turns it accent.
              data-state={!status?.enabled ? "unavailable" : input.trim() ? "ready" : "empty"}
              disabled={!input.trim() || !status?.enabled}
              onClick={() => void send()}
              type="button"
            ><AgentSubmitIcon /></button>}
          </footer>
          {error && (
            <span className={styles.error} role="alert">
              {error}
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
        </div>
        </div>
      </section>
      {deleteTarget && (
        <div className={styles.confirmOverlay} role="presentation">
          <section aria-label={t("Delete chat")} role="dialog">
            <h3>{t("Delete chat")}</h3>
            <p data-i18n-ignore>{deleteTarget.title}</p>
            <footer>
              <button onClick={() => setDeleteTarget(undefined)} type="button">
                {t("Cancel")}
              </button>
              <button
                className={styles.deleteButton}
                onClick={() =>
                  void deleteAgentSession(deleteTarget.id).then(() => {
                    setSessions((list) =>
                      list.filter((item) => item.id !== deleteTarget.id),
                    );
                    onSessionChange(deleteTarget.id);
                    setDeleteTarget(undefined);
                    newChat();
                  })
                }
                type="button"
              >
                {t("Delete")}
              </button>
            </footer>
          </section>
        </div>
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
  session,
}: {
  busy: boolean;
  data: BootstrapData;
  /** Sends a follow-up suggestion chip as the next message. */
  onSuggestion?: (message: string) => void;
  draftContext: string;
  draftProject?: Project;
  editingId?: string;
  onEdit: (message: AgentSession["messages"][number]) => void;
  onRetry: (message: string) => void;
  onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void;
  approvalBusy?: string;
  session: AgentSession;
}) {
  const { t } = useI18n();
  const scrollRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const viewport = scrollRef.current;
    if (!viewport) return;
    requestAnimationFrame(() => {
      if (typeof viewport.scrollTo === "function") viewport.scrollTo({ top: viewport.scrollHeight });
      else viewport.scrollTop = viewport.scrollHeight;
    });
  }, [session.messages.length, session.updatedAt]);
  const latestAssistantIndex = session.messages.findLastIndex((message) => message.role === "assistant");
  return (
    <div
      ref={scrollRef}
      aria-label={t("Agent conversation")}
      className={styles.conversation}
      role="group"
    >
      <div className={styles.conversationInner}>
        {session.messages.map((message, index) => {
          const waiting = busy && index === session.messages.length - 1 && message.role === "assistant" && !message.content && !message.parts?.length;
          const answer: AnswerProps = message.role === "assistant"
            ? { data, onSuggestion: index === latestAssistantIndex && !busy ? onSuggestion : undefined, streaming: busy && index === session.messages.length - 1 }
            : {};
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
                    ? <AgentMessageParts {...answer} draftContext={draftContext} draftProject={draftProject} message={message} onRetry={lastUserMessage(session.messages, index) ? () => onRetry(lastUserMessage(session.messages, index)) : undefined} onToolApproval={onToolApproval} approvalBusy={approvalBusy}/>
                    : <AgentMessageText {...answer} content={message.content} draftContext={draftContext} draftProject={draftProject}/>}
              </div>
              <div className={styles.messageActions}>
                <button
                  aria-label={t("Copy message")}
                  onClick={() => void navigator.clipboard.writeText(message.role === "assistant" ? splitAgentSuggestions(message.content).prose : message.content)}
                  type="button"
                >
                  <Copy />
                </button>
                {message.role === "user" && (
                  <button
                    aria-label={t("Edit message")}
                    onClick={() => onEdit(message)}
                    type="button"
                  >
                    <span>{t("Edit")}</span>
                  </button>
                )}
              </div>
            </article>
          </Fragment>
        )})}
      </div>
    </div>
  );
}

/** Answer chrome for an assistant reply: entity chips need `data`; suggestion chips show only when `onSuggestion` is set. */
type AnswerProps = { data?: BootstrapData; onSuggestion?: (message: string) => void; streaming?: boolean };

function AgentMessageParts({ draftContext, draftProject, message, onRetry, onToolApproval, approvalBusy, ...answer }: AnswerProps & { draftContext: string; draftProject?: Project; message: AgentMessage; onRetry?: () => void; onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void; approvalBusy?: string }) {
  const { t } = useI18n();
  const text = message.parts?.filter(part => part.type === "text").map(part => part.text ?? "").join("") || message.content;
  const work = message.parts?.filter(part => part.type === "reasoning" || part.type === "step" || part.type === "toolCall") ?? [];
  const other = message.parts?.filter(part => !["text", "reasoning", "step", "toolCall"].includes(part.type)) ?? [];
  const queue = summarizeElicitationQueue(other);
  const submitting = other.some(part => part.type === "elicitation" && part.status === "running");
  return <div className={styles.messageParts}>
    {work.length > 0 && <AgentWorkGroup message={message} parts={work} onToolApproval={onToolApproval} approvalBusy={approvalBusy}/>}
    <AgentElicitationResponseQueue answeredCount={queue.answeredCount} elicitationCount={queue.elicitationCount} isSubmitting={submitting} />
    {other.map(part => part.type === "elicitation" ? <AgentElicitation key={part.id} part={part}/> : part.type === "error"
      ? <div className={styles.partError} key={part.id} role="alert"><AlertCircle/><span>{part.text}</span>{onRetry && <button onClick={onRetry} type="button">{t("Retry")}</button>}</div>
      : <div className={styles.eventPart} key={part.id}><span>{part.text}</span></div>)}
    {text && <AgentMessageText {...answer} content={text} draftContext={draftContext} draftProject={draftProject}/>}
  </div>;
}

/**
 * Message text with any ```update block shown as Linear's "Created draft" card instead of raw code. Assistant replies
 * (`data` set) also get Linear's answer chrome: inline entity chips, referenced issues and follow-up suggestions.
 */
function AgentMessageText({ content, data, draftContext, draftProject, onSuggestion, streaming = false }: AnswerProps & { content: string; draftContext: string; draftProject?: Project }) {
  const { prose, draft } = splitAgentDraft(content, "update");
  const answer = useMemo(() => data ? parseAgentAnswer(prose, data) : undefined, [data, prose]);
  const draftCard = draft && <AgentDraftCard context={draftContext} draft={draft} icon={draftProject ? <span style={{ color: healthColor(draftProject.health), display: "inline-flex" }}><HealthGlyph health={draftProject.health}/></span> : undefined} title="Update draft"/>;
  if (!answer) return <>
    {prose && <AgentRichText className={styles.messageDocument} content={prose}/>}
    {draftCard}
  </>;
  return <>
    {answer.markdown && <AgentAnswerText className={styles.messageDocument} data={data} markdown={answer.markdown}/>}
    {!streaming && <AgentReferencedResources data={data} references={answer.references}/>}
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
function optimisticAgentTurn(session: AgentSession | undefined, message: string, editingId?: string, mentions: AgentMention[] = []): AgentSession {
  const now = new Date().toISOString();
  const earlier = session?.messages ?? [];
  const kept = editingId && earlier.some((item) => item.id === editingId) ? earlier.slice(0, earlier.findIndex((item) => item.id === editingId)) : earlier;
  const stamp = Date.now();
  const messages: AgentMessage[] = [
    ...kept,
    { id: `pending-user-${stamp}`, role: "user", content: message, mentions: mentions.length ? mentions.map(({ type, id, label }) => ({ type, id, label })) : undefined, createdAt: now },
    { id: `pending-reply-${stamp}`, role: "assistant", content: "", parts: [], createdAt: now },
  ];
  if (session) return { ...session, messages, updatedAt: now };
  return { id: `pending-${stamp}`, slugId: "", userId: "", title: message.split("\n")[0].slice(0, 80), favorite: false, location: "page", issueIds: [], skillIds: [], messages, createdAt: now, updatedAt: now };
}

function lastUserMessage(messages: AgentMessage[], before: number) {
  for (let index = before; index >= 0; index--) {
    if (messages[index].role === "user") return messages[index].content;
  }
  return "";
}

function groupAgentHistory(sessions: AgentSession[]) {
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

function markdown(session: AgentSession) {
  return session.messages
    .map(
      (message) =>
        `**${message.role === "user" ? "You" : "Flow Agent"}**\n\n${message.role === "user" ? message.content : splitAgentSuggestions(message.content).prose}`,
    )
    .join("\n\n");
}


function writeInputToEditor(editorRef: RefObject<HTMLDivElement | null>, value: string) {
  if (editorRef.current && editorRef.current.textContent !== value) editorRef.current.textContent = value;
}

/** Shows the working indicator under a message whose reply is still on its way. */
function withReplyPlaceholder(session: AgentSession): AgentSession {
  const last = session.messages.at(-1);
  return { ...session, messages: [...session.messages, { id: `awaiting-reply-${last?.id ?? session.id}`, role: "assistant", content: "", parts: [], createdAt: last?.createdAt ?? session.updatedAt }] };
}
