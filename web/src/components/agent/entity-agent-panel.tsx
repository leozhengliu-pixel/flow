import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { fetchAgentStatus, getAgentSession, listAgentSessions, resolveAgentApproval } from '@/lib/api'
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from '@/lib/agent-stream'
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { useI18n } from '@/i18n/i18n'
import { AgentPanel, type AgentPanelDock } from './agent-panel'
import {
  clearEntityThreadDraft,
  EntityAgentThread,
} from './entity-agent-thread'
import { conversationDraftKeyFor } from './agent-drafts'
import styles from './entity-agent-panel.module.css'

export type EntityAgentEntityType = 'issue' | 'document' | 'initiative' | 'project'

export type AgentSidebarTarget =
  | { type: 'default' }
  | { type: 'aiConversationMention'; aiConversationId: string; turnId?: string }

export type EntityAgentTarget = {
  type: EntityAgentEntityType
  id: string
  title: string
  identifier?: string
  /** Issue IDs attached to new sessions (REST create payload). */
  issueIds?: string[]
}

export type EntityAgentPanelProps = {
  target: EntityAgentTarget
  open: boolean
  onRequestClose: () => void
  onOpenFullPage?: (session?: AgentSession) => void
  onSessionChange?: (session: AgentSession) => void
  agentSidebarTarget?: AgentSidebarTarget
  initialPrompt?: string
  contextIssues?: MyIssuesRowData[]
  dock?: AgentPanelDock
  variant?: 'sidebar' | 'floating'
  emptyLabel?: string
}

type HydrationState = {
  isHydrated: boolean
  observing: boolean
}

/**
 * LS-0252 EntityAgentPanel — entity-bound sidebar Agent built on AgentPanel chrome
 * + EntityAgentThread, reusing REST stream + local drafts.
 */
export function EntityAgentPanel({
  target,
  open,
  onRequestClose,
  onOpenFullPage,
  onSessionChange,
  agentSidebarTarget,
  initialPrompt = '',
  contextIssues = [],
  dock,
  variant = 'sidebar',
  emptyLabel,
}: EntityAgentPanelProps) {
  const { t } = useI18n()
  const [messages, setMessages] = useState<AgentMessage[]>([])
  const [session, setSession] = useState<AgentSession>()
  const [input, setInput] = useState(initialPrompt)
  const [status, setStatus] = useState<AgentStatus>()
  const [loading, setLoading] = useState(false)
  const [streamParts, setStreamParts] = useState<AgentMessagePart[]>([])
  const [error, setError] = useState<string>()
  const [approvalBusy, setApprovalBusy] = useState<string>()
  const [hydration, setHydration] = useState<HydrationState>({ isHydrated: false, observing: false })
  const [draftEpoch, setDraftEpoch] = useState(0)
  const abortRef = useRef<AbortController | undefined>(undefined)
  const entityKey = `${target.type}:${target.id}`

  const conversationDraftKey = useMemo(() => {
    if (agentSidebarTarget?.type === 'aiConversationMention') {
      return `${target.id}:${agentSidebarTarget.aiConversationId}:${agentSidebarTarget.turnId ?? 'mention'}`
    }
    return conversationDraftKeyFor(target.id, session?.id) + (draftEpoch ? `:new-${draftEpoch}` : '')
  }, [agentSidebarTarget, draftEpoch, session?.id, target.id])

  const observeHydrationState = useCallback(() => {
    setHydration(current => ({ ...current, observing: true }))
  }, [])

  useEffect(() => {
    if (!open) return
    let active = true
    setError(undefined)
    observeHydrationState()
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
  }, [observeHydrationState, open, t])

  useEffect(() => {
    if (!open) return
    let active = true
    setHydration({ isHydrated: false, observing: true })
    const hydrate = async () => {
      try {
        const sessions = await listAgentSessions()
        if (!active) return
        const matched = pickEntitySession(sessions, target, agentSidebarTarget)
        if (matched) {
          const full = matched.messages?.length ? matched : await getAgentSession(matched.id)
          if (!active) return
          setSession(full)
          setMessages(full.messages)
          onSessionChange?.(full)
        } else {
          setSession(undefined)
          setMessages([])
        }
      } catch (reason) {
        if (active) setError(reason instanceof Error ? reason.message : t('Flow Agent is unavailable'))
      } finally {
        if (active) setHydration({ isHydrated: true, observing: false })
      }
    }
    void hydrate()
    return () => {
      active = false
    }
  }, [agentSidebarTarget, entityKey, onSessionChange, open, t, target])

  useEffect(() => {
    if (initialPrompt) setInput(initialPrompt)
  }, [initialPrompt, entityKey])

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

  const resetConversation = () => {
    abortRef.current?.abort()
    clearEntityThreadDraft(conversationDraftKey)
    setSession(undefined)
    setMessages([])
    setStreamParts([])
    setInput('')
    setError(undefined)
    setLoading(false)
    setDraftEpoch(value => value + 1)
  }

  const submit = async () => {
    const message = input.trim()
    if (!message || loading || !status?.enabled) return
    setMessages(current => [
      ...current,
      { id: `pending-${Date.now()}`, role: 'user', content: message, createdAt: new Date().toISOString() },
    ])
    setInput('')
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
        }
      }
      const issueIds = target.issueIds ?? contextIssues.map(issue => issue.id)
      next = session
        ? await streamAgentSessionMessage(session.id, message, onEvent, controller.signal)
        : await streamNewAgentSession(
            { message, issueIds, location: 'toolbar' },
            onEvent,
            controller.signal,
          )
      if (!next) return
      setSession(next)
      onSessionChange?.(next)
      setMessages(next.messages)
      clearEntityThreadDraft(conversationDraftKey)
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

  const contextTypeLabel =
    target.type === 'issue'
      ? t('issue')
      : target.type === 'document'
        ? t('document')
        : target.type === 'initiative'
          ? t('initiative')
          : t('project')

  return (
    <div className={styles.shell} data-entity-type={target.type} data-hydrated={hydration.isHydrated || undefined}>
      <AgentPanel
        aria-label={t('Entity agent panel')}
        dock={dock}
        loading={hydration.observing && !hydration.isHydrated}
        onNewChat={resetConversation}
        onOpenFullPage={onOpenFullPage ? () => onOpenFullPage(session) : undefined}
        onRequestClose={onRequestClose}
        open={open}
        title={session?.title ?? t('New chat')}
        variant={variant}
      >
        <EntityAgentThread
        viewerId={session?.userId}
          approvalBusy={approvalBusy}
          contextIssues={contextIssues}
          conversationDraftKey={conversationDraftKey}
          emptyLabel={emptyLabel ?? `${t('Ask anything about this')} ${contextTypeLabel}`}
          enabled={Boolean(status?.enabled)}
          error={error}
          input={input}
          loading={loading}
          messages={messages}
          onInputChange={setInput}
          onStop={() => abortRef.current?.abort()}
          onSubmit={() => void submit()}
          onToolApproval={(call, decision) => void decideToolApproval(call, decision)}
          placeholder={
            status?.enabled
              ? `${t('Ask about this')} ${contextTypeLabel}…`
              : t('Flow Agent is not configured')
          }
          streamParts={streamParts}
        />
      </AgentPanel>
    </div>
  )
}

function pickEntitySession(
  sessions: AgentSession[],
  target: EntityAgentTarget,
  agentSidebarTarget?: AgentSidebarTarget,
) {
  if (agentSidebarTarget?.type === 'aiConversationMention') {
    return sessions.find(session => session.id === agentSidebarTarget.aiConversationId)
  }
  const issueIds = new Set(target.issueIds ?? [])
  if (issueIds.size) {
    const matched = sessions
      .filter(session => session.issueIds.some(id => issueIds.has(id)))
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
    if (matched[0]) return matched[0]
  }
  // Document / initiative: match by title prefix heuristic when no issue linkage.
  const needle = target.identifier ?? target.title
  return sessions
    .filter(session => session.title.includes(needle) || session.slugId.includes(target.id.slice(0, 8)))
    .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))[0]
}
