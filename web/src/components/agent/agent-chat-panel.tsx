import { useEffect, useRef, useState } from 'react'
import { Box, Search, Users } from 'lucide-react'
import { fetchAgentStatus, resolveAgentApproval } from '@/lib/api'
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from '@/lib/agent-stream'
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { useI18n } from '@/i18n/i18n'
import { AgentPanel } from './agent-panel'
import { EntityAgentThread, clearEntityThreadDraft } from './entity-agent-thread'
import { conversationDraftKeyFor } from './agent-drafts'

export function AgentChatPanel({
  initialPrompt = '',
  initialSession,
  issues,
  onClose,
  onOpenFullPage,
  onSessionChange,
  open,
}: {
  initialPrompt?: string
  initialSession?: AgentSession
  issues: MyIssuesRowData[]
  onClose: () => void
  onOpenFullPage?: (session?: AgentSession) => void
  onSessionChange?: (session: AgentSession) => void
  open: boolean
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
      next = session
        ? await streamAgentSessionMessage(session.id, message, onEvent, controller.signal)
        : await streamNewAgentSession(
            { message, issueIds: issues.map(issue => issue.id), location: 'toolbar' },
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
        approvalBusy={approvalBusy}
        contextIssues={issues}
        conversationDraftKey={draftKey}
        emptyLabel={t('Ask Flow about the selected issues')}
        placeholder={status && !status.enabled ? t('Flow Agent is not configured') : t('Ask Flow…')}
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
        messages={messages}
        onInputChange={setInput}
        onStop={() => abortRef.current?.abort()}
        onSubmit={() => void submit()}
        onToolApproval={(call, decision) => void decideToolApproval(call, decision)}
        streamParts={streamParts}
      />
    </AgentPanel>
  )
}
