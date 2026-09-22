import { useEffect, useMemo, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { fetchAgentStatus, resolveAgentApproval } from '@/lib/api'
import { streamAgentSessionMessage, streamNewAgentSession, type AgentStreamEvent } from '@/lib/agent-stream'
import type { AgentMessage, AgentMessagePart, AgentSession, AgentStatus } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { useI18n } from '@/i18n/i18n'
import { AgentPanel } from './agent-panel'
import { EntityAgentThread, clearEntityThreadDraft } from './entity-agent-thread'
import { conversationDraftKeyFor } from './agent-drafts'
import { AgentToolbarActions, type AgentToolbarSessionSummary } from './agent-toolbar-actions'
import { AgentSessionExternalUrlsButton } from './agent-session-external-urls-button'
import { filteredExternalUrls } from './agent-session-external-urls'
import {
  setAgentPanelOpenState,
  subscribeAgentSelectionContext,
  type AgentSelectedTextAttachment,
} from './add-selection-as-agent-context'
import styles from './entity-agent-thread.module.css'

export function AgentChatPanel({
  initialPrompt = '',
  initialAttachments = [],
  initialSession,
  issues,
  onClose,
  onOpenFullPage,
  onSessionChange,
  onNewChat,
  open,
  openSessions = [],
  onSelectSession,
}: {
  initialPrompt?: string
  initialAttachments?: AgentSelectedTextAttachment[]
  initialSession?: AgentSession
  issues: MyIssuesRowData[]
  onClose: () => void
  onOpenFullPage?: (session?: AgentSession) => void
  onSessionChange?: (session: AgentSession) => void
  onNewChat?: () => void
  open: boolean
  /** LS-0044 multi-session summaries for toolbar switcher. */
  openSessions?: AgentToolbarSessionSummary[]
  onSelectSession?: (sessionId: string) => void
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
  const [attachments, setAttachments] = useState<AgentSelectedTextAttachment[]>(initialAttachments)
  const [feedback, setFeedback] = useState<'up' | 'down'>()
  const abortRef = useRef<AbortController | undefined>(undefined)
  const draftKey = conversationDraftKeyFor(session?.id ?? `toolbar:${issues.map(issue => issue.id).join(',') || 'new'}`)

  useEffect(() => {
    setAgentPanelOpenState(open)
    return () => setAgentPanelOpenState(false)
  }, [open])

  useEffect(() => {
    if (!open) return
    return subscribeAgentSelectionContext(detail => {
      setAttachments(current => {
        if (current.some(item => item.id === detail.attachment.id)) return current
        return [...current, detail.attachment]
      })
      setMinimized(false)
    })
  }, [open])

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

  useEffect(() => {
    if (initialAttachments.length) {
      setAttachments(current => {
        const ids = new Set(current.map(item => item.id))
        const merged = [...current]
        for (const item of initialAttachments) {
          if (!ids.has(item.id)) merged.push(item)
        }
        return merged
      })
    }
  }, [initialAttachments])

  const externalUrls = useMemo(() => {
    const fromMessages = (session?.messages ?? messages).flatMap(message =>
      (message.content.match(/https?:\/\/[^\s)\]>'"]+/gi) ?? []).map(url => url.replace(/[.,;:]+$/, '')),
    )
    return filteredExternalUrls(fromMessages)
  }, [session?.messages, messages])

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
    setAttachments([])
    setFeedback(undefined)
    clearEntityThreadDraft(draftKey)
    onClose()
  }

  const startNewChat = () => {
    abortRef.current?.abort()
    setMessages([])
    setSession(undefined)
    setInput('')
    setStreamParts([])
    setError(undefined)
    setAttachments([])
    setFeedback(undefined)
    clearEntityThreadDraft(draftKey)
    onNewChat?.()
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
    const contextBlock = attachments.length
      ? attachments.map(item => `Selected context:\n"""\n${item.text}\n"""`).join('\n\n') + '\n\n'
      : ''
    const message = `${contextBlock}${input.trim()}`.trim()
    if (!message || loading || !status?.enabled) return
    setMessages(current => [
      ...current,
      { id: `pending-${Date.now()}`, role: 'user', content: message, createdAt: new Date().toISOString() },
    ])
    setInput('')
    setAttachments([])
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

  const contextChips =
    attachments.length > 0 ? (
      <>
        {attachments.map(item => (
          <span data-attachment-id={item.id} key={item.id}>
            <small>{t('Selection')}</small>
            <b>{item.text.slice(0, 80)}{item.text.length > 80 ? '…' : ''}</b>
            <button
              aria-label={t('Remove selection context')}
              onClick={() => setAttachments(current => current.filter(entry => entry.id !== item.id))}
              type="button"
            >
              <X size={12} />
            </button>
          </span>
        ))}
      </>
    ) : null

  return (
    <AgentPanel
      fullscreen={fullscreen}
      headerExtra={
        <>
          <AgentSessionExternalUrlsButton urls={externalUrls} />
          <AgentToolbarActions
            activeSessionId={session?.id}
            onFeedback={value => setFeedback(value)}
            onSelectSession={onSelectSession}
            sessions={openSessions}
          />
          {feedback && <span className={styles.srFeedback} data-feedback={feedback} />}
        </>
      }
      minimized={minimized}
      onFullscreenChange={setFullscreen}
      onMinimizedChange={setMinimized}
      onNewChat={startNewChat}
      onOpenFullPage={onOpenFullPage ? () => onOpenFullPage(session) : undefined}
      onRequestClose={close}
      open={open}
      title={session?.title ?? t('New chat')}
      variant="floating"
    >
      <div data-agent-panel-popover>
        <EntityAgentThread
          approvalBusy={approvalBusy}
          contextChips={contextChips}
          contextIssues={issues}
          conversationDraftKey={draftKey}
          emptyLabel={t('Ask Flow about the selected issues')}
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
      </div>
    </AgentPanel>
  )
}
