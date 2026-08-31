import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as Popover from "@radix-ui/react-popover";
import {
  AlertCircle,
  Check,
  Box,
  ChevronRight,
  CircleCheck,
  Copy,
  LoaderCircle,
  MoreHorizontal,
  PanelTop,
  Plus,
  Search,
  Star,
  Trash2,
  UsersRound,
  X,
} from "lucide-react";
import {
  deleteAgentSession,
  fetchAgentStatus,
  updateAgentSession,
} from "@/lib/api";
import { streamAgentSessionMessage, streamAgentSessionMessageEdit, streamNewAgentSession, type AgentStreamEvent } from "@/lib/agent-stream";
import { agentPath, newAgentSkillPath } from "@/lib/app-routes";
import type { AgentMessage, AgentSession, AgentStatus, BootstrapData } from "@/types/flow";
import { useI18n } from "@/i18n/i18n";
import { usePropertyCommand } from "@/components/property/use-property-command";
import {
  AgentAttachIcon,
  AgentChevronDownIcon,
  AgentSkillsIcon,
  AgentSubmitIcon,
} from "./agent-icons";
import { AgentRichText } from "./agent-rich-text";
import styles from "./agent-page.module.css";
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { applyAgentStreamEvent, markAgentSessionStopped } from './agent-stream-state'

export function AgentPage({
  chatSlug,
  data,
  onNavigate,
  onOpenSidebar,
  onReload,
}: {
  chatSlug?: string;
  data: BootstrapData;
  onNavigate: (href: string) => void;
  onOpenSidebar: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [sessions, setSessions] = useState(data.agentSessions ?? []),
    [status, setStatus] = useState<AgentStatus>(),
    [input, setInput] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState<string>(),
    [historyOpen, setHistoryOpen] = useState(false),
    [skillsOpen, setSkillsOpen] = useState(false),
    [selectedSkills, setSelectedSkills] = useState<string[]>([]),
    [deleteTarget, setDeleteTarget] = useState<AgentSession>(),
    [editingId, setEditingId] = useState<string>(),
    [activeStreamId, setActiveStreamId] = useState<string>(),
    [examplesVisible, setExamplesVisible] = useState(true),
    [attachments, setAttachments] = useState<File[]>([]);
  const editorRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const streamAbortRef = useRef<AbortController | undefined>(undefined);
  const historyRequested = new URLSearchParams(window.location.search).get("history") === "1";
  useEffect(() => setSessions(data.agentSessions ?? []), [data.agentSessions]);
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
  const current = useMemo(
    () =>
      chatSlug
        ? sessions.find(
            (item) => item.slugId === chatSlug || item.id === chatSlug,
          )
        : sessions.find((item) => item.id === activeStreamId),
    [activeStreamId, chatSlug, sessions],
  );
  useEffect(
    () => setSelectedSkills(current?.skillIds ?? []),
    [current?.id, current?.skillIds],
  );
  const writeInput = (value: string) => {
    setInput(value);
    if (editorRef.current && editorRef.current.textContent !== value)
      editorRef.current.textContent = value;
  };
  const commitSession = (next: AgentSession) => {
    setSessions((list) => [
      next,
      ...list.filter((item) => item.id !== next.id),
    ]);
    setSelectedSkills(next.skillIds);
    onNavigate(agentPath(data.workspace.urlKey, next.slugId));
    void onReload();
  };
  const send = async (message = input) => {
    message = message.trim();
    if (!message || busy || !status?.enabled) return;
    setBusy(true);
    setError(undefined);
    try {
      const attachmentContext = await Promise.all(
        attachments.map(agentFileContext),
      );
      const providerMessage = attachmentContext.length
        ? `${message}\n\n${attachmentContext.join("\n\n")}`
        : message;
      const controller = new AbortController();
      streamAbortRef.current = controller;
      let streamed = current;
      const onEvent = (event: AgentStreamEvent) => {
          streamed = applyAgentStreamEvent(streamed, event);
          if (!streamed) return;
          const next = streamed;
          setSessions((list) => [next, ...list.filter((item) => item.id !== next.id)]);
          if (event.type === "session.started") {
            setActiveStreamId(next.id);
            onNavigate(agentPath(data.workspace.urlKey, next.slugId));
          }
          if (event.type === "session.completed") {
            onNavigate(agentPath(data.workspace.urlKey, next.slugId));
            void onReload();
          }
      };
      if (current && editingId) await streamAgentSessionMessageEdit(current.id, editingId, providerMessage, onEvent, controller.signal);
      else if (current) await streamAgentSessionMessage(current.id, providerMessage, onEvent, controller.signal);
      else await streamNewAgentSession({ message: providerMessage, skillIds: selectedSkills, location: "page" }, onEvent, controller.signal);
      writeInput("");
      setAttachments([]);
      setEditingId(undefined);
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === "AbortError") {
        setSessions(list => list.map(markAgentSessionStopped));
        void onReload();
        return;
      }
      setError(
        reason instanceof Error
          ? reason.message
          : t("Flow Agent is unavailable"),
      );
    } finally {
	  streamAbortRef.current = undefined;
      setBusy(false);
      requestAnimationFrame(() => editorRef.current?.focus());
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
  const skillOptions = useMemo(
    () => [
      ...(data.agentSkills ?? []).map((item) => ({
        id: item.id,
        label: item.name,
      })),
      { id: "__create__", label: t("Create skill") },
    ],
    [data.agentSkills, t],
  );
  const skillCommand = usePropertyCommand({
    closeOnSelect: false,
    open: skillsOpen,
    options: skillOptions,
    selectedIds: selectedSkills,
    onOpenChange: setSkillsOpen,
    onSelect: (option) => {
      if (option.id === "__create__") {
        onNavigate(newAgentSkillPath(data.workspace.urlKey));
        return;
      }
      setSelectedSkills((ids) =>
        ids.includes(option.id)
          ? ids.filter((id) => id !== option.id)
          : [...ids, option.id],
      );
    },
  });
  const newChat = () => {
    setHistoryOpen(false);
    setActiveStreamId(undefined);
    writeInput("");
    setSelectedSkills([]);
    onNavigate(agentPath(data.workspace.urlKey));
  };
  return (
    <main className={styles.page}>
      <header>
        <button
          className={styles.mobileMenu}
          aria-label={t("Open navigation")}
          onClick={onOpenSidebar}
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
              <h2 data-i18n-ignore={Boolean(current) || undefined}>
                {current?.title ?? t("New chat")}
              </h2>
              <AgentChevronDownIcon />
            </button>
          </Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
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
                        <i aria-hidden="true" />
                        <strong data-i18n-ignore>{item.title}</strong>
                        <span>{current?.id === item.id ? t("Current") : ""}</span>
                        <time>{relative(item.updatedAt)}</time>
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
                <DropdownMenu.Content
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
            }).then(async (next) => {
              setSessions((list) =>
                list.map((item) => (item.id === next.id ? next : item)),
              );
              await onReload();
              newChat();
            })
          }
          type="button"
        >
          <PanelTop />
        </button>}
      </header>
      <section className={`${styles.body}${current ? ` ${styles.hasConversation}` : ""}`}>
        {current ? (
          <Conversation
            busy={busy}
            session={current}
            editingId={editingId}
            onRetry={(message) => void send(message)}
            onEdit={(message) => {
              setEditingId(message.id);
              writeInput(message.content);
              requestAnimationFrame(() => editorRef.current?.focus());
            }}
          />
        ) : <AgentBackground />}
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
        <div className={`${styles.composer}${current ? ` ${styles.conversationComposer}` : ""}`}>
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
            <div
              ref={editorRef}
              aria-label={t("Send a message to Flow AI")}
              className={styles.editor}
              contentEditable={!busy}
              data-placeholder={current ? t("Reply…") : t("Ask Flow…")}
              role="textbox"
              suppressContentEditableWarning
              onInput={(event) =>
                setInput(event.currentTarget.textContent ?? "")
              }
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  void send();
                }
              }}
            />
          </div>
          <footer>
            <Popover.Root open={skillsOpen} onOpenChange={setSkillsOpen}>
              <Popover.Trigger asChild>
                <button
                  aria-expanded={skillsOpen}
                  aria-label={t("Skills")}
                  className={styles.skillsButton}
                  type="button"
                >
                  <AgentSkillsIcon />
                  <span>{t("Skills")}</span>
                  <AgentChevronDownIcon />
                </button>
              </Popover.Trigger>
              <Popover.Portal>
                <Popover.Content
                  align="start"
                  className={styles.skillsMenu}
                  side="bottom"
                  sideOffset={4}
                  onKeyDown={skillCommand.onKeyDown}
                >
                  <div className={styles.menuSearch}>
                    <Search />
                    <input
                      ref={skillCommand.inputRef}
                      autoFocus
                      aria-label={t("Search skills…")}
                      placeholder={t("Search skills…")}
                      value={skillCommand.query}
                      onChange={(event) =>
                        skillCommand.onQueryChange(event.target.value)
                      }
                    />
                  </div>
                  <div role="listbox">
                    {skillCommand.filteredOptions.map((option) => (
                      <button
                        aria-selected={skillCommand.activeId === option.id}
                        key={option.id}
                        onMouseMove={() => skillCommand.setActiveId(option.id)}
                        onClick={() => skillCommand.choose(option)}
                        role="option"
                        type="button"
                      >
                        {option.id === "__create__" ? (
                          <Plus />
                        ) : (
                          <AgentSkillsIcon />
                        )}
                        <span data-i18n-ignore>{option.label}</span>
                        {option.id !== "__create__" &&
                          skillCommand.isSelected(option.id) && <Check />}
                      </button>
                    ))}
                  </div>
                </Popover.Content>
              </Popover.Portal>
            </Popover.Root>
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
              accept="image/*,video/*,text/*,application/json,application/xml,application/javascript,application/x-yaml,application/yaml,application/pdf,application/rtf,application/vnd.oasis.opendocument.text,application/msword,application/vnd.apple.keynote,application/vnd.apple.pages,application/vnd.ms-powerpoint,application/vnd.ms-excel,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.openxmlformats-officedocument.presentationml.presentation,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,.md,.markdown,.mdx,.csv"
              className={styles.fileInput}
              multiple
              onChange={(event) => {
                const files = [...(event.target.files ?? [])].filter(
                  (file) => file.size <= 2 * 1024 * 1024,
                );
                setAttachments((items) => [...items, ...files].slice(0, 8));
                event.target.value = "";
              }}
              type="file"
            />
            {busy ? <button aria-label={t("Stop generating")} onClick={() => streamAbortRef.current?.abort()} type="button"><X /></button> : <button
              aria-label={t("Submit comment")}
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
          {status && !status.enabled && (
            <span className={styles.error}>
              {t("Flow Agent is not configured")}
            </span>
          )}
        </div>
        {!current && examplesVisible && <section className={`${styles.examples}${status&&!status.enabled?` ${styles.examplesWithError}`:''}`}>
          <header><span>{t("Get started with some examples")}</span><button aria-label={t("Dismiss")} onClick={()=>setExamplesVisible(false)} type="button"><X/></button></header>
          <div>
            <AgentExample icon={<Box/>} title={t("Create a new project")} description={t("Turn an idea into a well-scoped project")} onClick={()=>{writeInput(t("Help me create a new project"));requestAnimationFrame(()=>editorRef.current?.focus())}}/>
            <AgentExample icon={<Search/>} title={t("Research a topic")} description={t("Research a topic across the issue backlog")} onClick={()=>{writeInput(t("Research a topic across the issue backlog"));requestAnimationFrame(()=>editorRef.current?.focus())}}/>
            <AgentExample icon={<UsersRound/>} title={t("Set up new team")} description={t("Create a team that matches how your organization works")} onClick={()=>{writeInput(t("Help me set up a new team"));requestAnimationFrame(()=>editorRef.current?.focus())}}/>
          </div>
        </section>}
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

function AgentExample({description,icon,onClick,title}:{description:string;icon:ReactNode;onClick:()=>void;title:string}){return <button onClick={onClick} type="button">{icon}<span><strong>{title}</strong><small>{description}</small></span></button>}

function AgentBackground(){return <svg aria-hidden="true" className={styles.emptyGraphic} viewBox="0 0 48 48"><path d="M10 6a4 4 0 0 0-4 4v28a4 4 0 0 0 8 0v-8h16a4 4 0 0 0 0-8H14v-8h24a4 4 0 0 0 0-8H10Z"/><circle cx="35" cy="38" r="4"/></svg>}

function Conversation({
  busy,
  editingId,
  onEdit,
  onRetry,
  session,
}: {
  busy: boolean;
  editingId?: string;
  onEdit: (message: AgentSession["messages"][number]) => void;
  onRetry: (message: string) => void;
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
                  ? <div aria-live="polite" className={styles.thinkingPlaceholder}><LoaderCircle className={styles.spin}/><span>{t("Thinking…")}</span></div>
                  : message.parts?.length
                    ? <AgentMessageParts message={message} onRetry={lastUserMessage(session.messages, index) ? () => onRetry(lastUserMessage(session.messages, index)) : undefined}/>
                    : <AgentRichText className={styles.messageDocument} content={message.content}/>}
              </div>
              <div className={styles.messageActions}>
                <button
                  aria-label={t("Copy message")}
                  onClick={() => void navigator.clipboard.writeText(message.content)}
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

function AgentMessageParts({ message, onRetry }: { message: AgentMessage; onRetry?: () => void }) {
  const { t } = useI18n();
  const text = message.parts?.filter(part => part.type === "text").map(part => part.text ?? "").join("") || message.content;
  const work = message.parts?.filter(part => part.type === "reasoning" || part.type === "toolCall") ?? [];
  const other = message.parts?.filter(part => !["text", "reasoning", "toolCall"].includes(part.type)) ?? [];
  return <div className={styles.messageParts}>
    {work.length > 0 && <AgentWorkGroup message={message} parts={work}/>}
    {other.map(part => part.type === "error"
      ? <div className={styles.partError} key={part.id} role="alert"><AlertCircle/><span>{part.text}</span>{onRetry && <button onClick={onRetry} type="button">{t("Retry")}</button>}</div>
      : <div className={styles.eventPart} key={part.id}><span>{part.text}</span></div>)}
    {text && <AgentRichText className={styles.messageDocument} content={text}/>}
  </div>;
}

function AgentWorkGroup({ message, parts }: { message: AgentMessage; parts: NonNullable<AgentMessage["parts"]> }) {
  const { t } = useI18n();
  const running = parts.some(part => part.status === "running" || part.status === "pending" || part.toolCall?.status === "running" || part.toolCall?.status === "pending");
  const failed = parts.some(part => part.status === "error" || part.toolCall?.status === "error");
  const [open, setOpen] = useState(running || failed);
  useEffect(() => {
    if (running || failed) setOpen(true);
  }, [failed, running]);
  const toolCount = parts.filter(part => part.type === "toolCall").length;
  const duration = Math.max(1, Math.round((message.durationMs ?? 0) / 1000));
  const label = running
    ? t("Working…")
    : message.durationMs
      ? `${t("Worked for")} ${duration} ${t(duration === 1 ? "second" : "seconds")}`
      : toolCount > 2
        ? `${t("Used")} ${toolCount} ${t("tools")}`
        : t("Work completed");
  return <details className={`${styles.workGroup}${failed ? ` ${styles.workFailed}` : ""}`} open={open} onToggle={event => setOpen(event.currentTarget.open)}>
    <summary>{running ? <LoaderCircle className={styles.spin}/> : failed ? <AlertCircle/> : <CircleCheck/>}<span>{label}</span><ChevronRight/></summary>
    <div className={styles.workItems}>
      {parts.map(part => part.type === "reasoning"
        ? <div className={styles.reasoningRow} key={part.id}><span>{part.status === "running" ? t("Thinking…") : t("Reasoning")}</span>{part.text && <p>{part.text}</p>}</div>
        : part.toolCall ? <AgentToolCallItem key={part.id} part={part}/> : null)}
    </div>
  </details>;
}

function AgentToolCallItem({ part }: { part: NonNullable<AgentMessage["parts"]>[number] }) {
  const call = part.toolCall!;
  const running = call.status === "running" || call.status === "pending";
  const detail = readableToolDetail(call.arguments);
  return <details className={`${styles.toolCall} ${call.status === "error" ? styles.toolCallError : ""}`} open={call.status === "error" || undefined}>
    <summary>{running ? <LoaderCircle className={styles.spin}/> : call.status === "error" ? <AlertCircle/> : <Check/>}<span>{toolStatusLabel(call.name, running)}</span>{detail && <small>{detail}</small>}<ChevronRight/></summary>
    <div>{call.error && <p role="alert">{call.error}</p>}<code>{JSON.stringify(call.result ?? call.arguments ?? {}, null, 2)}</code></div>
  </details>;
}

function toolStatusLabel(name: string, running: boolean) {
  const labels: Record<string, [string, string]> = {
    list_issues: ["Looking at issues…", "Looked at issues"], list_projects: ["Looking at projects…", "Looked at projects"],
    list_initiatives: ["Looking at initiatives…", "Looked at initiatives"], list_documents: ["Looking at documents…", "Looked at documents"],
    search_documentation: ["Searching documentation…", "Searched documentation"], save_issue: ["Updating issue…", "Updated issue"],
    save_project: ["Updating project…", "Updated project"], save_initiative: ["Updating initiative…", "Updated initiative"],
  };
  if (labels[name]) return labels[name][running ? 0 : 1];
  const [verb, ...words] = name.split("_");
  const subject = words.join(" ") || "workspace";
  const verbs: Record<string, [string, string]> = {
    list: ["Looking at", "Looked at"], get: ["Looking at", "Looked at"], search: ["Searching", "Searched"], extract: ["Extracting", "Extracted"],
    save: ["Updating", "Updated"], update: ["Updating", "Updated"], create: ["Creating", "Created"], delete: ["Deleting", "Deleted"],
    prepare: ["Preparing", "Prepared"], merge: ["Merging", "Merged"], submit: ["Submitting", "Submitted"], resolve: ["Resolving", "Resolved"],
  };
  const action = verbs[verb]?.[running ? 0 : 1];
  if (action) return `${action} ${subject}${running ? "…" : ""}`;
  const fallback = name.replaceAll("_", " ").replace(/^./, value => value.toUpperCase());
  return running ? `${fallback}…` : fallback;
}

function readableToolDetail(value: Record<string, unknown> | undefined) {
  if (!value) return "";
  for (const key of ["query", "id", "name", "issueId", "projectId"]) {
    if (typeof value[key] === "string") return String(value[key]);
  }
  return "";
}

function shouldShowAgentTime(messages: AgentMessage[], index: number) {
  if (index === 0) return true;
  const current = Date.parse(messages[index].createdAt);
  const previous = Date.parse(messages[index - 1].createdAt);
  return !Number.isFinite(current) || !Number.isFinite(previous) || current - previous >= 12 * 60 * 60 * 1000;
}

function formatAgentTime(value: string, todayLabel: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const now = new Date();
  const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
  const time = date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (sameDay) return `${todayLabel} ${time}`;
  return `${date.toLocaleDateString(undefined, { month: "short", day: "numeric" })} ${time}`;
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

function relative(value: string) {
  const seconds = Math.max(
    0,
    Math.round((Date.now() - Date.parse(value)) / 1000),
  );
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}
function markdown(session: AgentSession) {
  return session.messages
    .map(
      (message) =>
        `**${message.role === "user" ? "You" : "Flow Agent"}**\n\n${message.content}`,
    )
    .join("\n\n");
}

async function agentFileContext(file: File) {
  if (
    file.type.startsWith("text/") ||
    /\.(md|markdown|mdx|csv|json|xml|ya?ml)$/i.test(file.name)
  ) {
    return `Attached file ${file.name}:\n${await file.text()}`;
  }
  if (file.type.startsWith("image/")) {
    return `Attached image ${file.name} (${file.type}, ${file.size} bytes): ${await dataURL(file)}`;
  }
  return `Attached file ${file.name} (${file.type || "application/octet-stream"}, ${file.size} bytes).`;
}

function dataURL(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}
