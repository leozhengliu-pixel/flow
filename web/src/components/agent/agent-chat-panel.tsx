import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { FileText, Lightbulb, Plus } from 'lucide-react'
import { useLatestRef } from '@/hooks/use-latest-ref'
import { AgentDraftCard } from './agent-draft-card'
import { splitAgentDraft } from './agent-draft'
import { splitAgentSuggestions } from './agent-answer-content'
import { fetchAgentStatus, getAgentSession, updateAgentSession, uploadAgentFile } from '@/lib/api'
import { toast } from '@/lib/toast'
import { streamAgentSessionMessage, streamAgentSessionMessageEdit, streamNewAgentSession, type AgentStreamEvent } from '@/lib/agent-stream'
import { createAgentStreamEventCoalescer } from '@/lib/agent-stream-coalescer'
import { markStoppedAgentReply } from './agent-stopped-reply'
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus, BootstrapData } from '@/types/flow'
import { PropertyMenu } from '@/components/property/property-menu'
import { AgentAttachIcon, AgentChevronDownIcon, AgentPageContextIcon, AgentSkillsIcon } from './agent-icons'
import { agentFileAttachment, attachAgentFiles } from './agent-attachments'
import { quoteAgentMessage } from './agent-floating-request'
import { claimAgentQuoteHighlight, clearAgentQuoteHighlight } from './agent-quote-highlight'
import { AgentDiscardDraftDialog } from './agent-discard-draft-dialog'
import threadStyles from './entity-agent-thread.module.css'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { mentionIcon } from './agent-mention-icon'
import type { AgentMention } from './agent-mention-input'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { useI18n } from '@/i18n/i18n'
import { AgentPanel } from './agent-panel'
import { EntityAgentThread, type AgentContextEntity } from './entity-agent-thread'
import { clearEntityThreadDraft } from './entity-thread-draft'
import styles from './agent-chat-panel.module.css'
import { conversationDraftKeyFor } from './agent-drafts'
import { useMarkAgentSessionRead } from './agent-read-state'
import { AgentChatMenu, AgentRenamePalette } from './agent-chat-actions'
import { deleteAgentChat, useAgentChatHotkeys } from './agent-chat-commands'
import { AgentSessionFeedbackDialog } from './agent-message-actions'
import { agentShownParts, useAgentApprovalAnswer, useAgentReplyFollow } from './use-agent-reply-follow'
// coding sessions (track G)
import { ChatCodingSessionCards } from '@/components/coding-sessions/chat-coding-session-cards'

/** The entity the user is looking at; attached to the first message of a new conversation. */
export type AgentPageContext = { type: 'project' | 'document' | 'issue' | 'initiative'; id: string; label: string }

export function AgentChatPanel({
  data,
  onCreateSkill,
  initialPrompt = '',
  initialQuote,
  initialMentions,
  initialSession,
  issues,
  onClose,
  onHide,
  onOpenFullPage,
  onSessionChange,
  onSessionDeleted,
  onSessionRestored,
  onUseResponse,
  useResponseLabel = 'Use response',
  open,
  pageContext,
  autoSubmit = false,
  draftFence = 'update',
  onDraft,
  draftCard,
}: {
  /** Workspace data enables @-mentions and the Skills picker. */
  data?: BootstrapData
  onCreateSkill?: () => void
  initialPrompt?: string
  /** Text selected on the page ("Ask agent" on a selection): a chip at the start of the composer, sent as a quote. */
  initialQuote?: string
  /** Mention chips the composer starts with (the reference app's "Ask the reference app" on an "Issue created" toast). */
  initialMentions?: AgentMention[]
  initialSession?: AgentSession
  issues: MyIssuesRowData[]
  onClose: () => void
  /** Escape hides the chat, keeping its draft (the reference app's floating chat); without it Escape closes like the X. */
  onHide?: () => void
  onOpenFullPage?: (session?: AgentSession) => void
  onSessionChange?: (session: AgentSession) => void
  /** The chat was deleted (the host drops it from its lists). */
  onSessionDeleted?: (id: string) => void
  /** Undo on the "Chat deleted" toast brought the chat back; the host lists it again and reopens it. */
  onSessionRestored?: (session: AgentSession) => void
  /** When set, finished assistant replies offer a button that hands their text back to the caller. */
  onUseResponse?: (content: string) => void
  useResponseLabel?: string
  open: boolean
  /** Current page entity (project, initiative, document, issue) shown as a removable context chip for new conversations; a list selection passes several. */
  pageContext?: AgentPageContext | AgentPageContext[]
  /** Send `initialPrompt` as soon as the panel opens (the reference app's "Write with Agent"). */
  autoSubmit?: boolean
  /** Fence tag (e.g. `update`) whose block is taken out of the reply and handed to `onDraft`. */
  draftFence?: string
  onDraft?: (draft: string) => void
  /** Labels for the "Created draft" card; `current` is the host's text, used to mark the draft outdated. */
  draftCard?: { context: string; icon?: ReactNode; title: string; current?: string }
}) {
  const { t } = useI18n()
  const [messages, setMessages] = useState<AgentMessage[]>([])
  const [session, setSession] = useState<AgentSession>()
  const [renameOpen, setRenameOpen] = useState(false)
  const [feedbackOpen, setFeedbackOpen] = useState(false)
  const [input, setInput] = useState(initialPrompt)
  const [quote, setQuote] = useState(initialQuote)
  const [attachments, setAttachments] = useState<File[]>([])
  const insertFilesRef = useRef<((files: File[]) => void) | null>(null)
  const [filesUploading, setFilesUploading] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [status, setStatus] = useState<AgentStatus>()
  const [loading, setLoading] = useState(false)
  const [scrollRequest, setScrollRequest] = useState(0)
  const [streamParts, setStreamParts] = useState<AgentMessagePart[]>([])
  const [error, setError] = useState<string>()
  const [minimized, setMinimized] = useState(false)
  // A reply that finishes while the chat is minimized marks its bottom-bar pill with a dot.
  const [unread, setUnread] = useState(false)
  const minimizedRef = useLatestRef(minimized)
  const [seenMinimized, setSeenMinimized] = useState(minimized)
  if (seenMinimized !== minimized) {
    setSeenMinimized(minimized)
    if (!minimized) setUnread(false)
  }
  const [fullscreen, setFullscreen] = useState(false)
  const abortRef = useRef<AbortController | undefined>(undefined)
  const retryMessage = useRef('')
  // A reply running on the server without this panel streaming it (opened or reloaded mid-reply, or asked from another
  // surface): its saved progress shows with live approval and question cards, followed until it finishes.
  const shownSession = useMemo(() => session && { ...session, messages }, [messages, session])
  const follow = useAgentReplyFollow({
    session: shownSession,
    streaming: loading,
    enabled: open,
    onUpdate: latest => { setSession(latest); setMessages(latest.messages); onSessionChange?.(latest) },
  })
  const replyRunning = loading || follow.following
  const shownParts = useMemo(() => agentShownParts(messages, streamParts), [messages, streamParts])
  const { approvalBusy, decideToolApproval, retryApproval } = useAgentApprovalAnswer({ sessionId: session?.id, parts: shownParts, onError: setError, onStale: follow.refresh })
  // Seeing the chat here (once its reply has settled) clears its unread dot on the Agent page too.
  useMarkAgentSessionRead(open ? session : undefined, replyRunning, (read, lastReadAt) => {
    if (!session || session.id !== read.id) return
    const next = { ...session, lastReadAt }
    setSession(next)
    onSessionChange?.(next)
  })
  const [mentions, setMentions] = useState<AgentMention[]>([])
  const [skillIds, setSkillIds] = useState<string[]>([])
  const [removedContext, setRemovedContext] = useState<string[]>([])
  const contextIssues = useMemo(() => issues.filter(issue => !removedContext.includes(issue.id)), [issues, removedContext])
  const [attachedContext, setAttachedContext] = useState<AgentContextEntity[]>([])
  // Drafts are derived from the stored replies, so a resumed chat still shows its "Created draft" card.
  const drafts = useMemo(() => Object.fromEntries(messages.flatMap(message => {
    const draft = message.role === 'assistant' ? splitAgentDraft(message.content, draftFence).draft : undefined
    return draft ? [[message.id, draft]] : []
  })), [draftFence, messages])
  const pageContexts = useMemo(() => pageContext ? Array.isArray(pageContext) ? pageContext : [pageContext] : [], [pageContext])
  const card = draftCard ?? { context: pageContexts[0]?.label ?? session?.title ?? t('Project'), title: 'Update draft' }
  const autoSubmitted = useRef(false)
  const splitDraft = (content: string) => splitAgentDraft(content, draftFence)
  const displayMessages = useMemo(() => draftFence
    ? messages.map(message => message.role === 'assistant' ? { ...message, content: splitAgentDraft(message.content, draftFence).prose } : message)
    : messages, [draftFence, messages])
  const contextKeyOf = (context: AgentPageContext) => `${context.type}:${context.id}`
  // Page entities only apply to a conversation that has not started yet, and not once the user removed them.
  const activePageContexts = !session && !messages.length ? pageContexts.filter(context => !removedContext.includes(contextKeyOf(context))) : []
  const pageContextEntities: AgentContextEntity[] = activePageContexts.map(context => ({
    key: contextKeyOf(context),
    icon: pageContextIcon(context, data),
    label: context.label,
    onRemove: () => setRemovedContext(current => [...current, contextKeyOf(context)]),
  }))
  // The reference app's "Use document as context" toggle (Tab in the composer): the page's entities ride along until switched off.
  const pageContextKeys = [...pageContexts.map(contextKeyOf), ...issues.map(issue => issue.id)]
  const canTogglePageContext = !session && !messages.length && pageContextKeys.length > 0
  const pageContextOn = canTogglePageContext && pageContextKeys.some(key => !removedContext.includes(key))
  const togglePageContext = () => setRemovedContext(current => pageContextOn
    ? [...new Set([...current, ...pageContextKeys])]
    : current.filter(key => !pageContextKeys.includes(key)))
  const pageContextLabel = t(PAGE_CONTEXT_LABELS[pageContexts[0]?.type ?? 'issue'])
  const idsOf = (type: AgentMention['type']) => mentions.filter(item => item.type === type).map(item => item.id)
  const draftKey = conversationDraftKeyFor(session?.id ?? `toolbar:${issues.map(issue => issue.id).join(',') || 'new'}`)

  // Opening (or a language change) clears the last error before the status is read again.
  const [statusFor, setStatusFor] = useState({ open, t })
  if (statusFor.open !== open || statusFor.t !== t) {
    setStatusFor({ open, t })
    if (open) setError(undefined)
  }
  useEffect(() => {
    if (!open) return
    let active = true
    fetchAgentStatus()
      .then(next => {
        if (active) {
          setStatus(next)
          if (!next.enabled) setError(t('Flow Agent is not configured'))
        }
      })
      .catch(reason => {
        if (active) setError(reason instanceof Error ? reason.message : t('Flow Agent is unavailable'))
      })
    return () => {
      active = false
    }
  }, [open, t])

  // A chat handed in (opened from a list) replaces what the panel holds.
  const [seenInitial, setSeenInitial] = useState<{ initialSession?: AgentSession; open: boolean }>({ open: false })
  if (seenInitial.initialSession !== initialSession || seenInitial.open !== open) {
    setSeenInitial({ initialSession, open })
    if (open && initialSession) {
      setSession(initialSession)
      setMessages(initialSession.messages)
    }
  }
  useEffect(() => {
    if (!open || !initialSession) return
    // A long chat is listed without its messages: read it, so a reply still running shows (and is followed) too.
    if (initialSession.messages.length || initialSession.id.startsWith('pending-')) return
    let active = true
    const id = initialSession.id
    void Promise.resolve().then(() => getAgentSession(id)).then(full => {
      if (!active || !full?.messages) return
      setSession(full)
      setMessages(full.messages)
    }).catch(() => undefined)
    return () => { active = false }
  }, [initialSession, open])

  const [seenPrompt, setSeenPrompt] = useState(initialPrompt)
  if (seenPrompt !== initialPrompt) {
    setSeenPrompt(initialPrompt)
    if (initialPrompt) setInput(initialPrompt)
  }

  // The quoted text keeps a faint highlight on the page while its chip is here; this chat owns it.
  const quoteHighlight = useRef(claimAgentQuoteHighlight(initialQuote))
  const [seenQuote, setSeenQuote] = useState(initialQuote)
  if (seenQuote !== initialQuote) {
    setSeenQuote(initialQuote)
    if (initialQuote) setQuote(initialQuote)
  }
  useEffect(() => {
    if (!initialQuote) return
    quoteHighlight.current = claimAgentQuoteHighlight(initialQuote) ?? quoteHighlight.current
  }, [initialQuote])
  // It goes once the chip is removed or sent, or the chat is minimized or closed (and stays gone on reopening).
  useEffect(() => {
    if (!quote || minimized || !open) clearAgentQuoteHighlight(quoteHighlight.current)
  }, [quote, minimized, open])
  // Unmounting (another chat replaced this one) clears it too; after a tick, so StrictMode's remount keeps it.
  const mounted = useRef(false)
  useEffect(() => {
    mounted.current = true
    const highlight = quoteHighlight
    return () => {
      mounted.current = false
      setTimeout(() => { if (!mounted.current) clearAgentQuoteHighlight(highlight.current) })
    }
  }, [])

  const close = () => {
    autoSubmitted.current = false
    setMessages([])
    setSession(undefined)
    setInput('')
    setQuote(undefined)
    setAttachments([])
    setStatus(undefined)
    setError(undefined)
    setLoading(false)
    setStreamParts([])
    setMinimized(false)
    setFullscreen(false)
    setMentions([])
    setSkillIds([])
    setRemovedContext([])
    setAttachedContext([])
    clearEntityThreadDraft(draftKey)
    onClose()
  }

  // Deleting is immediate (no confirmation): the panel closes and the toast's Undo restores the chat.
  const deleteChat = () => {
    if (!session) return
    const deleted = session
    void deleteAgentChat(deleted, {
      t,
      onDeleted: () => { onSessionDeleted?.(deleted.id); close() },
      onRestored: restored => onSessionRestored?.(restored),
    })
  }
  useAgentChatHotkeys(open && !minimized && Boolean(session) && !session?.id.startsWith('pending-'), () => setRenameOpen(true), deleteChat, '[data-agent-panel]')

  // Like the reference app, closing a chat whose composer holds something asks "Discard draft?" first; spaces alone do not count.
  const [discardOpen, setDiscardOpen] = useState(false)
  const hasDraft = Boolean(input.trim() || quote || mentions.length || attachments.length)
  const requestClose = () => { if (hasDraft) setDiscardOpen(true); else close() }

  /**
   * Sends the composer text, or `followUp` (a suggestion chip or a question's option) without touching the composer.
   * `answer` picks that option of the reply's pending question: it shows as the chosen pill, not as a message.
   */
  const submit = async (followUp?: string, answer?: { partId: string; value: string }, retryOf?: AgentMessage) => {
    // Like the reference app, a quoted selection goes first (the model reads it as a quote, the chat shows it as a chip).
    const message = followUp !== undefined ? followUp.trim() : quoteAgentMessage(quote, input).trim()
    if (!message || replyRunning || !status?.enabled || (followUp === undefined && filesUploading)) return
    retryMessage.current = message
    const sentFiles = followUp === undefined ? attachments : []
    // Files go with the message for the server to keep and give to the model (images as images, not text).
    const sentAttachments = await Promise.all(sentFiles.map(agentFileAttachment))
    const sentMentions = followUp === undefined ? mentions : []
    const sentIdsOf = (type: AgentMention['type']) => followUp === undefined ? idsOf(type) : []
    setMessages(current => [
      ...(retryOf ? current.slice(0, current.findIndex(item => item.id === retryOf.id)) : current).map(item => answer && item.parts?.some(part => part.id === answer.partId)
        ? { ...item, parts: item.parts.map(part => part.id === answer.partId && part.question ? { ...part, status: 'completed' as const, question: { ...part.question, selected: answer.value } } : part) }
        : item),
      { id: `pending-${Date.now()}`, role: 'user', content: message, mentions: sentMentions, answerTo: answer?.partId, attachments: sentFiles.length ? sentFiles.map(({ name, type, size }) => ({ name, contentType: type, size })) : undefined, createdAt: new Date().toISOString() },
    ])
    const mentioned = { issueIds: sentIdsOf('issue'), projectIds: sentIdsOf('project'), documentIds: sentIdsOf('document'), initiativeIds: sentIdsOf('initiative'), userIds: sentIdsOf('user'), mentions: sentMentions, ...(answer ? { answer } : {}) }
    const pageIdsOf = (type: AgentPageContext['type']) => activePageContexts.filter(context => context.type === type).map(context => context.id)
    if (!session) {
      setAttachedContext([
        ...pageContextEntities.map(({ onRemove: _onRemove, ...item }) => item),
        ...contextIssues.map(issue => ({ key: issue.id, icon: <StatusIcon state={issue.state} size={14}/>, label: `${issue.identifier} ${issue.title}` })),
      ])
    }
    if (followUp === undefined) {
      setInput('')
      setQuote(undefined)
      setAttachments([])
      setMentions([])
    }
    setError(undefined)
    setLoading(true)
    setScrollRequest(value => value + 1)
    setStreamParts([])
    // The work shown while streaming, kept for a stopped reply (it folds to "Worked for N seconds").
    const liveWork = new Map<string, AgentMessagePart>()
    let started = false
    let coalescer: ReturnType<typeof createAgentStreamEventCoalescer> | undefined
    try {
      const controller = new AbortController()
      abortRef.current = controller
      let next = session
      const handleEvent = (event: AgentStreamEvent) => {
        if (event.session) {
          next = event.session
          setSession(event.session)
        }
        if (event.type === 'session.started' && event.session) { started = true; setMessages(event.session.messages) }
        if (event.type === 'text.replaced') {
          setMessages(current => current.at(-1)?.role === 'assistant'
            ? current.map((item, index) => index === current.length - 1 ? { ...item, content: event.delta ?? '' } : item)
            : current)
        }
        if (event.type === 'text.delta') {
          setMessages(current =>
            current.at(-1)?.role === 'assistant'
              ? current.map((item, index) =>
                  index === current.length - 1
                    ? { ...item, content: item.content + (event.delta ?? '') }
                    : item,
                )
              : [
                  ...current,
                  {
                    id: event.messageId ?? `stream-${Date.now()}`,
                    role: 'assistant',
                    content: event.delta ?? '',
                    createdAt: new Date().toISOString(),
                  },
                ],
          )
        }
        if (
          (event.type.startsWith('tool.') ||
            event.type.startsWith('elicitation.') ||
            event.type === 'reasoning.delta') &&
          event.part
        ) {
          const nextPart = event.part
          liveWork.set(nextPart.id, nextPart)
          setStreamParts(current => {
            const index = current.findIndex(part => part.id === nextPart.id)
            return index >= 0
              ? current.map((part, itemIndex) => (itemIndex === index ? nextPart : part))
              : [...current, nextPart]
          })
        }
        if (event.type === 'session.completed' && event.session) {
          if (minimizedRef.current) setUnread(true)
          setMessages(event.session.messages)
          setStreamParts([])
          const reply = event.session.messages.filter(item => item.role === 'assistant').at(-1)
          const draft = reply && draftFence ? splitDraft(reply.content).draft : undefined
          if (reply && draft) onDraft?.(draft)
        }
      }
      coalescer = createAgentStreamEventCoalescer(handleEvent)
      const onEvent = coalescer.push
      next = session && retryOf && !retryOf.id.startsWith('pending-')
        ? await streamAgentSessionMessageEdit(session.id, retryOf.id, message, onEvent, controller.signal)
        : session
        ? await streamAgentSessionMessage(session.id, message, onEvent, controller.signal, { ...mentioned, ...(sentAttachments.length ? { attachments: sentAttachments } : {}) })
        : await streamNewAgentSession(
            {
              message,
              ...(sentAttachments.length ? { attachments: sentAttachments } : {}),
              issueIds: [...new Set([...contextIssues.map(issue => issue.id), ...pageIdsOf('issue'), ...mentioned.issueIds])],
              projectIds: [...new Set([...pageIdsOf('project'), ...mentioned.projectIds])],
              documentIds: [...new Set([...pageIdsOf('document'), ...mentioned.documentIds])],
              initiativeIds: [...new Set([...pageIdsOf('initiative'), ...mentioned.initiativeIds])],
              userIds: mentioned.userIds,
              mentions: sentMentions,
              skillIds,
              location: 'toolbar',
            },
            onEvent,
            controller.signal,
          )
      if (!next) return
      setSession(next)
      onSessionChange?.(next)
      setMessages(next.messages)
      if (followUp === undefined) clearEntityThreadDraft(draftKey)
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === 'AbortError') {
        // Like the reference app: the message you sent stays, the partial answer is dropped, and only the work is kept.
        setStreamParts([])
        setMessages(current => markStoppedAgentReply(current, [...liveWork.values()].filter(part => part.type === 'reasoning' || part.type === 'step' || part.type === 'toolCall')))
        return
      }
      if (!started) setInput(message)
      setError(reason instanceof Error ? reason.message : t('Flow Agent is unavailable'))
    } finally {
      coalescer?.flush()
      abortRef.current = undefined
      setLoading(false)
    }
  }

  // The reference app's "Write with Agent" sends its request as soon as the panel is ready.
  useEffect(() => {
    if (!open || !autoSubmit || autoSubmitted.current || !status?.enabled || session || messages.length || !input.trim()) return
    autoSubmitted.current = true
    void submit()
  })

  return (
    <>
    <AgentPanel
      fullscreen={fullscreen}
      minimized={minimized}
      onFullscreenChange={setFullscreen}
      onMinimizedChange={setMinimized}
      onOpenFullPage={
        onOpenFullPage
          ? () => onOpenFullPage(session)
          : undefined
      }
      onRequestClose={requestClose}
      onRequestHide={onHide}
      open={open}
      title={session?.title ?? t('New chat')}
      titleExtra={session ? (
        // The reference app's chat menu, right after the title once the chat exists.
        <AgentChatMenu
          unsaved={session.id.startsWith('pending-')}
          onCopy={() => void navigator.clipboard.writeText(conversationMarkdown(messages)).then(
            () => toast.info(t('Copied conversation'), { description: t('Conversation copied to clipboard as Markdown') }),
            () => toast.error(t('Could not copy to clipboard')),
          )}
          onDelete={deleteChat}
          onFeedback={() => setFeedbackOpen(true)}
          onRename={() => setRenameOpen(true)}
        />
      ) : undefined}
      unread={unread}
      variant="floating"
    >
      <EntityAgentThread
        renderMessageAttachment={message => {
          const draft = drafts[message.id]
          // coding sessions (track G): a session the reply started shows as a live card.
          if (!draft) return <ChatCodingSessionCards message={message} data={data}/>
          // The editor may reformat markdown, so compare loosely: outdated once the host no longer holds the draft's opening words.
          const opening = draft.replace(/[#*_>`-]/g, '').replace(/\s+/g, ' ').trim().slice(0, 24)
          const current = (card.current ?? '').replace(/<[^>]+>/g, ' ').replace(/[#*_>`-]/g, '').replace(/\s+/g, ' ')
          const outdated = Boolean(onDraft) && card.current !== undefined && !current.includes(opening)
          return <AgentDraftCard context={card.context} draft={draft} icon={'icon' in card ? card.icon : undefined} onRestore={onDraft ? () => onDraft(draft) : undefined} outdated={outdated} title={card.title} />
        }}
        renderMessageActions={onUseResponse ? (message, index) => message.role === 'assistant' && message.content.trim() && !(replyRunning && index === messages.length - 1) ? (
          <button className={styles.useResponse} onClick={() => { onUseResponse(splitAgentSuggestions(message.content).prose.trim()); close() }} type="button">
            {t(useResponseLabel)}
          </button>
        ) : null : undefined}
        approvalBusy={approvalBusy}
        addedContext={attachedContext}
        contextEntities={pageContextEntities}
        contextIssues={contextIssues}
        mentionData={data}
        onMentionsChange={setMentions}
        onRemoveContext={id => setRemovedContext(current => [...current, id])}
        footerStart={data ? <SkillsPicker data={data} disabled={Boolean(session)} selectedIds={skillIds} onChange={setSkillIds} onCreate={onCreateSkill}/> : undefined}
        conversationDraftKey={draftKey}
        // The reference app's floating chat is blank until you write: no greeting, no suggestions.
        blankEmpty
        placeholder={status && !status.enabled ? t('Flow Agent is not configured') : t('Ask Flow…')}
        quote={quote}
        onQuoteRemove={() => setQuote(undefined)}
        initialMentions={initialMentions}
        canSend={Boolean(input.trim() || quote) && !filesUploading}
        onComposerTab={canTogglePageContext ? () => { togglePageContext(); return true } : undefined}
        onPasteFiles={files => void attachAgentFiles(files, t, setAttachments)}
        onUploadFile={file => uploadAgentFile(file)}
        insertFilesRef={insertFilesRef}
        onFilesBusyChange={setFilesUploading}
        composerHeader={attachments.length > 0 ? (
          <div className={threadStyles.attachments}>
            {attachments.map((file, index) => (
              <span key={`${file.name}-${file.lastModified}-${index}`}>
                <b data-i18n-ignore>{file.name}</b>
                <AttachmentRemoveButton label={`${t('Remove attachment')} ${file.name}`} onClick={() => setAttachments(items => items.filter((_, itemIndex) => itemIndex !== index))}/>
              </span>
            ))}
          </div>
        ) : undefined}
        composerTools={<>
          {canTogglePageContext && (
            <ScopedFlowTooltip label={pageContextLabel} shortcut="Tab" sideOffset={8}>
              <button aria-label={pageContextLabel} aria-pressed={pageContextOn} className={threadStyles.composerTool} onClick={togglePageContext} type="button">
                <AgentPageContextIcon/>
              </button>
            </ScopedFlowTooltip>
          )}
          <ScopedFlowTooltip label={t('Attach images, files, or videos')} sideOffset={8}>
            <button aria-label={t('Attach images, files, or videos')} className={threadStyles.composerTool} onClick={() => fileInputRef.current?.click()} type="button">
              <AgentAttachIcon size={14}/>
            </button>
          </ScopedFlowTooltip>
          <input
            ref={fileInputRef}
            aria-hidden="true"
            className={threadStyles.fileInput}
            multiple
            onChange={event => {
              // Every file goes into the message like a pasted one; without the rich composer they are attached.
              const files = [...(event.target.files ?? [])]
              if (insertFilesRef.current) insertFilesRef.current(files)
              else void attachAgentFiles(files, t, setAttachments)
              event.target.value = ''
            }}
            tabIndex={-1}
            type="file"
          />
        </>}
        enabled={Boolean(status?.enabled)}
        error={error}
        input={input}
        loading={replyRunning}
        messages={displayMessages}
        onInputChange={setInput}
        // The reply runs on the server even without this panel, so Stop ends it there too.
        onStop={() => { void follow.stop(); abortRef.current?.abort() }}
        onSubmit={() => void submit()}
        onRetry={target => void submit(target?.content ?? retryMessage.current, undefined, target ?? session?.messages.findLast(item => item.role === 'user'))}
        onRetryError={retryApproval ?? (!status?.enabled ? () => { void fetchAgentStatus().then(next => { setStatus(next); setError(next.enabled ? undefined : t('Flow Agent is not configured')) }).catch(reason => setError(String(reason))) } : undefined)}
        onSendSuggestion={message => void submit(message)}
        onAnswer={(part, choice) => void submit(choice.label, { partId: part.id, value: choice.value })}
        sessionId={session?.id}
        onSessionChange={next => { setSession(next); setMessages(next.messages); onSessionChange?.(next) }}
        onToolApproval={(call, decision) => void decideToolApproval(call, decision)}
        streamParts={streamParts}
        scrollRequest={scrollRequest}
      />
    </AgentPanel>
    {/* Outside the panel, so the minimized pill's close can ask too. */}
    <AgentDiscardDraftDialog open={open && discardOpen} onCancel={() => setDiscardOpen(false)} onDiscard={() => { setDiscardOpen(false); close() }}/>
    {session && <AgentRenamePalette
      open={open && renameOpen}
      title={session.title}
      onOpenChange={setRenameOpen}
      onRename={title => void updateAgentSession(session.id, { title }).then(
        next => { const renamed = { ...session, title: next.title, slugId: next.slugId, updatedAt: next.updatedAt }; setSession(renamed); onSessionChange?.(renamed) },
        reason => toast.error(reason instanceof Error ? reason.message : t('Could not rename chat')),
      )}
    />}
    {session && <AgentSessionFeedbackDialog
      open={open && feedbackOpen}
      onOpenChange={setFeedbackOpen}
      sessionId={session.id}
      onSessionChange={next => { setSession(next); onSessionChange?.(next) }}
    />}
    </>
  )
}

/** Skills picker in the composer footer; skills apply when a conversation starts. */
function SkillsPicker({ data, disabled, selectedIds, onChange, onCreate }: { data: BootstrapData; disabled: boolean; selectedIds: string[]; onChange: (ids: string[]) => void; onCreate?: () => void }) {
  const { t } = useI18n()
  const skills = data.agentSkills ?? []
  return <PropertyMenu
    label={t('Skills')}
    ariaLabel={t('Skills')}
    value={t('Skills')}
    multiple
    hideSearch
    side="top"
    align="start"
    selectedIds={selectedIds}
    searchPlaceholder={t('Search skills…')}
    triggerRole="button"
    triggerClassName="agent-skills-trigger"
    trigger={<><AgentSkillsIcon/><span>{selectedIds.length ? `${t('Skills')} · ${selectedIds.length}` : t('Skills')}</span><AgentChevronDownIcon/></>}
    options={[
      ...skills.map(skill => ({ id: skill.id, label: skill.name, icon: <AgentSkillsIcon/>, i18nIgnore: true, disabled })),
      ...(onCreate ? [{ id: '__create__', label: t('Create skill'), icon: <Plus size={16}/> }] : []),
    ]}
    onChange={id => {
      if (id === '__create__') { onCreate?.(); return }
      onChange(selectedIds.includes(id) ? selectedIds.filter(item => item !== id) : [...selectedIds, id])
    }}
  />
}

/** The conversation as Markdown ("Copy as markdown" in the chat menu). */
function conversationMarkdown(messages: AgentMessage[]) {
  return messages.map(message => `**${message.role === 'user' ? 'You' : 'Flow Agent'}**\n\n${message.role === 'user' ? message.content : splitAgentSuggestions(message.content).prose}`).join('\n\n')
}

const PAGE_CONTEXT_LABELS: Record<AgentPageContext['type'], string> = {
  document: 'Use document as context',
  project: 'Use project as context',
  issue: 'Use issue as context',
  initiative: 'Use initiative as context',
}

function pageContextIcon(context: AgentPageContext, data?: BootstrapData) {
  if (data) return mentionIcon({ type: context.type, id: context.id }, data)
  return context.type === 'project' ? <ProjectIcon size={14}/> : context.type === 'document' ? <FileText size={14}/> : context.type === 'initiative' ? <Lightbulb size={14}/> : null
}
