import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Box, FileText, Plus, Search, Users } from 'lucide-react'
import { AgentDraftCard } from './agent-draft-card'
import { splitAgentDraft } from './agent-draft'
import { fetchAgentStatus, resolveAgentApproval } from '@/lib/api'
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from '@/lib/agent-stream'
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus, BootstrapData } from '@/types/flow'
import { PropertyMenu } from '@/components/property/property-menu'
import { AgentChevronDownIcon, AgentSkillsIcon } from './agent-icons'
import { mentionIcon, type AgentMention } from './agent-mention-input'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { useI18n } from '@/i18n/i18n'
import { AgentPanel } from './agent-panel'
import { EntityAgentThread, clearEntityThreadDraft, type AgentContextEntity } from './entity-agent-thread'
import styles from './agent-chat-panel.module.css'
import { conversationDraftKeyFor } from './agent-drafts'

/** The entity the user is looking at; attached to the first message of a new conversation. */
export type AgentPageContext = { type: 'project' | 'document' | 'issue'; id: string; label: string }

export function AgentChatPanel({
  data,
  onCreateSkill,
  initialPrompt = '',
  initialSession,
  issues,
  onClose,
  onOpenFullPage,
  onSessionChange,
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
  initialSession?: AgentSession
  issues: MyIssuesRowData[]
  onClose: () => void
  onOpenFullPage?: (session?: AgentSession) => void
  onSessionChange?: (session: AgentSession) => void
  /** When set, finished assistant replies offer a button that hands their text back to the caller. */
  onUseResponse?: (content: string) => void
  useResponseLabel?: string
  open: boolean
  /** Current page entity (project, document, issue) shown as a removable context chip for new conversations. */
  pageContext?: AgentPageContext
  /** Send `initialPrompt` as soon as the panel opens (Linear's "Write with Agent"). */
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
  const [input, setInput] = useState(initialPrompt)
  const [status, setStatus] = useState<AgentStatus>()
  const [loading, setLoading] = useState(false)
  const [streamParts, setStreamParts] = useState<AgentMessagePart[]>([])
  const [error, setError] = useState<string>()
  const [minimized, setMinimized] = useState(false)
  const [fullscreen, setFullscreen] = useState(false)
  const [approvalBusy, setApprovalBusy] = useState<string>()
  const abortRef = useRef<AbortController | undefined>(undefined)
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
  const card = draftCard ?? { context: pageContext?.label ?? session?.title ?? t('Project'), title: 'Update draft' }
  const autoSubmitted = useRef(false)
  const splitDraft = (content: string) => splitAgentDraft(content, draftFence)
  const displayMessages = useMemo(() => draftFence
    ? messages.map(message => message.role === 'assistant' ? { ...message, content: splitAgentDraft(message.content, draftFence).prose } : message)
    : messages, [draftFence, messages])
  const pageContextKey = pageContext ? `${pageContext.type}:${pageContext.id}` : ''
  // The page entity only applies to a conversation that has not started yet, and not once the user removed it.
  const activePageContext = pageContext && !session && !messages.length && !removedContext.includes(pageContextKey) ? pageContext : undefined
  const pageContextEntities: AgentContextEntity[] = activePageContext ? [{
    key: pageContextKey,
    icon: pageContextIcon(activePageContext, data),
    label: activePageContext.label,
    onRemove: () => setRemovedContext(current => [...current, pageContextKey]),
  }] : []
  const idsOf = (type: AgentMention['type']) => mentions.filter(item => item.type === type).map(item => item.id)
  const draftKey = conversationDraftKeyFor(session?.id ?? `toolbar:${issues.map(issue => issue.id).join(',') || 'new'}`)

  useEffect(() => {
    if (!open) return
    let active = true
    setError(undefined)
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

  useEffect(() => {
    if (!open || !initialSession) return
    setSession(initialSession)
    setMessages(initialSession.messages)
  }, [initialSession, open])

  useEffect(() => {
    if (initialPrompt) setInput(initialPrompt)
  }, [initialPrompt])

  const close = () => {
    autoSubmitted.current = false
    setMessages([])
    setSession(undefined)
    setInput('')
    setStatus(undefined)
    setError(undefined)
    setLoading(false)
    setStreamParts([])
    setMinimized(false)
    setFullscreen(false)
    setApprovalBusy(undefined)
    setMentions([])
    setSkillIds([])
    setRemovedContext([])
    setAttachedContext([])
    clearEntityThreadDraft(draftKey)
    onClose()
  }

  const decideToolApproval = async (
    call: AgentMessagePart['toolCall'] | undefined,
    decision: 'approve' | 'reject',
  ) => {
    if (!session || !call?.approvalId || approvalBusy) return
    setApprovalBusy(call.approvalId)
    try {
      await resolveAgentApproval(session.id, call.approvalId, decision)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('Flow Agent is unavailable'))
    } finally {
      setApprovalBusy(undefined)
    }
  }

  const submit = async () => {
    const message = input.trim()
    if (!message || loading || !status?.enabled) return
    setMessages(current => [
      ...current,
      { id: `pending-${Date.now()}`, role: 'user', content: message, mentions, createdAt: new Date().toISOString() },
    ])
    const mentioned = { issueIds: idsOf('issue'), projectIds: idsOf('project'), documentIds: idsOf('document'), userIds: idsOf('user'), mentions }
    const pageIdsOf = (type: AgentPageContext['type']) => activePageContext?.type === type ? [activePageContext.id] : []
    if (!session) {
      setAttachedContext([
        ...pageContextEntities.map(({ onRemove: _onRemove, ...item }) => item),
        ...contextIssues.map(issue => ({ key: issue.id, icon: <StatusIcon state={issue.state} size={14}/>, label: `${issue.identifier} ${issue.title}` })),
      ])
    }
    setInput('')
    setMentions([])
    setError(undefined)
    setLoading(true)
    setStreamParts([])
    try {
      const controller = new AbortController()
      abortRef.current = controller
      let next = session
      const onEvent = (event: AgentStreamEvent) => {
        if (event.session) {
          next = event.session
          setSession(event.session)
        }
        if (event.type === 'session.started' && event.session) setMessages(event.session.messages)
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
          setStreamParts(current => {
            const index = current.findIndex(part => part.id === nextPart.id)
            return index >= 0
              ? current.map((part, itemIndex) => (itemIndex === index ? nextPart : part))
              : [...current, nextPart]
          })
        }
        if (event.type === 'session.completed' && event.session) {
          setMessages(event.session.messages)
          setStreamParts([])
          const reply = event.session.messages.filter(item => item.role === 'assistant').at(-1)
          const draft = reply && draftFence ? splitDraft(reply.content).draft : undefined
          if (reply && draft) onDraft?.(draft)
        }
      }
      next = session
        ? await streamAgentSessionMessage(session.id, message, onEvent, controller.signal, mentioned)
        : await streamNewAgentSession(
            {
              message,
              issueIds: [...new Set([...contextIssues.map(issue => issue.id), ...pageIdsOf('issue'), ...mentioned.issueIds])],
              projectIds: [...new Set([...pageIdsOf('project'), ...mentioned.projectIds])],
              documentIds: [...new Set([...pageIdsOf('document'), ...mentioned.documentIds])],
              userIds: mentioned.userIds,
              mentions,
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
      clearEntityThreadDraft(draftKey)
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === 'AbortError') {
        setStreamParts(current =>
          current.map(part => (part.status === 'running' ? { ...part, status: 'error' } : part)),
        )
        return
      }
      setError(reason instanceof Error ? reason.message : t('Flow Agent is unavailable'))
    } finally {
      abortRef.current = undefined
      setLoading(false)
    }
  }

  // Linear's "Write with Agent" sends its request as soon as the panel is ready.
  useEffect(() => {
    if (!open || !autoSubmit || autoSubmitted.current || !status?.enabled || session || messages.length || !input.trim()) return
    autoSubmitted.current = true
    void submit()
  })

  return (
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
      onRequestClose={close}
      open={open}
      title={session?.title ?? t('New chat')}
      variant="floating"
    >
      <EntityAgentThread
        renderMessageAttachment={message => {
          const draft = drafts[message.id]
          if (!draft) return null
          // The editor may reformat markdown, so compare loosely: outdated once the host no longer holds the draft's opening words.
          const opening = draft.replace(/[#*_>`-]/g, '').replace(/\s+/g, ' ').trim().slice(0, 24)
          const current = (card.current ?? '').replace(/<[^>]+>/g, ' ').replace(/[#*_>`-]/g, '').replace(/\s+/g, ' ')
          const outdated = Boolean(onDraft) && card.current !== undefined && !current.includes(opening)
          return <AgentDraftCard context={card.context} draft={draft} icon={'icon' in card ? card.icon : undefined} onRestore={onDraft ? () => onDraft(draft) : undefined} outdated={outdated} title={card.title} />
        }}
        renderMessageActions={onUseResponse ? (message, index) => message.role === 'assistant' && message.content.trim() && !(loading && index === messages.length - 1) ? (
          <button className={styles.useResponse} onClick={() => { onUseResponse(message.content.trim()); close() }} type="button">
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
        emptyLabel={t('Ask Flow about the selected issues')}
        placeholder={status && !status.enabled ? t('Flow Agent is not configured') : data ? t('@ to mention any issue, project, or document') : t('Ask Flow…')}
        welcome={{
          title: t('Welcome to Flow'),
          subtitle: t('Ask anything or tell Flow what you need'),
          suggestions: [
            { label: t('Create a new project'), icon: <Box size={14} aria-hidden="true"/>, prompt: t('Create a new project for ') },
            { label: t('Research a topic'), icon: <Search size={14} aria-hidden="true"/>, prompt: t('Research ') },
            { label: t('Set up new team'), icon: <Users size={14} aria-hidden="true"/>, prompt: t('Set up a new team for ') },
          ],
        }}
        enabled={Boolean(status?.enabled)}
        error={error}
        input={input}
        loading={loading}
        messages={displayMessages}
        onInputChange={setInput}
        onStop={() => abortRef.current?.abort()}
        onSubmit={() => void submit()}
        onToolApproval={(call, decision) => void decideToolApproval(call, decision)}
        streamParts={streamParts}
      />
    </AgentPanel>
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

function pageContextIcon(context: AgentPageContext, data?: BootstrapData) {
  if (data) return mentionIcon(context, data)
  return context.type === 'project' ? <ProjectIcon size={14}/> : context.type === 'document' ? <FileText size={14}/> : null
}
