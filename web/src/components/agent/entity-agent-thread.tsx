import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, ChevronRight, LoaderCircle, Send, X } from 'lucide-react'
import { AgentElicitation } from './agent-elicitation'
import { AgentRichText } from './agent-rich-text'
import { StatusIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { AgentMessage, AgentMessagePart } from '@/types/flow'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import {
  agentDraftStorageKey,
  clearAgentDraft,
  readAgentDraft,
  writeAgentDraft,
} from './agent-drafts'
import styles from './entity-agent-thread.module.css'

export type EntityAgentThreadProps = {
  messages: AgentMessage[]
  streamParts?: AgentMessagePart[]
  loading?: boolean
  error?: string
  input: string
  onInputChange: (value: string) => void
  onSubmit: () => void
  onStop?: () => void
  enabled?: boolean
  emptyLabel?: string
  placeholder?: string
  contextIssues?: MyIssuesRowData[]
  contextChips?: ReactNode
  conversationDraftKey: string
  approvalBusy?: string
  onToolApproval?: (call: AgentMessagePart['toolCall'] | undefined, decision: 'approve' | 'reject') => void
  highlightedMessageId?: string
  composerDisabled?: boolean
}

/** LS-0253 EntityAgentThread — shared conversation + draft renderer for page/sidebar panels. */
export function EntityAgentThread({
  messages,
  streamParts = [],
  loading = false,
  error,
  input,
  onInputChange,
  onSubmit,
  onStop,
  enabled = true,
  emptyLabel,
  placeholder,
  contextIssues = [],
  contextChips,
  conversationDraftKey,
  approvalBusy,
  onToolApproval,
  highlightedMessageId,
  composerDisabled = false,
}: EntityAgentThreadProps) {
  const { t } = useI18n()
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const logRef = useRef<HTMLDivElement>(null)
  const draftKey = agentDraftStorageKey(conversationDraftKey)
  const [hydratedDraft, setHydratedDraft] = useState(false)

  useEffect(() => {
    if (hydratedDraft) return
    const draft = readAgentDraft(draftKey)
    if (draft?.input && !input) onInputChange(draft.input)
    setHydratedDraft(true)
  }, [draftKey, hydratedDraft, input, onInputChange])

  useEffect(() => {
    if (!hydratedDraft) return
    const timer = window.setTimeout(() => {
      if (!input.trim()) clearAgentDraft(draftKey)
      else writeAgentDraft(draftKey, { input })
    }, 250)
    return () => window.clearTimeout(timer)
  }, [draftKey, hydratedDraft, input])

  useEffect(() => {
    const node = logRef.current
    if (!node) return
    node.scrollTop = node.scrollHeight
  }, [messages, streamParts, loading])

  useEffect(() => {
    requestAnimationFrame(() => inputRef.current?.focus())
  }, [conversationDraftKey])

  const decide = onToolApproval ?? (() => undefined)

  return (
    <div className={styles.thread}>
      <div
        aria-label={t('Agent conversation')}
        aria-live="polite"
        className={styles.conversation}
        ref={logRef}
        role="log"
      >
        {!messages.length && !loading && (
          <div className={styles.empty} data-state="empty">
            <span>{emptyLabel ?? t('Ask anything or propose changes')}</span>
          </div>
        )}
        {messages.map((message, index) => (
          <article
            className={message.role === 'user' ? styles.userMessage : styles.agentMessage}
            data-highlighted={highlightedMessageId === message.id || undefined}
            key={message.id || `${message.role}-${index}`}
          >
            <strong>{message.role === 'user' ? t('You') : t('Flow Agent')}</strong>
            {message.role === 'assistant' && (
              <>
                <ThreadMessageActivity
                  approvalBusy={approvalBusy}
                  onToolApproval={decide}
                  parts={message.parts ?? []}
                />
                {message.parts
                  ?.filter(part => part.type === 'elicitation')
                  .map(part => <AgentElicitation key={part.id} part={part} />)}
              </>
            )}
            {message.content && (
              <AgentRichText
                ariaLabel={message.role === 'user' ? t('Your message') : t('AI message')}
                className={styles.messageDocument}
                content={message.content}
              />
            )}
          </article>
        ))}
        {loading && (
          <div className={styles.thinking} data-state="loading">
            <LoaderCircle />
            {t('Thinking…')}
          </div>
        )}
        {streamParts.map(part => (
          <div className={styles.streamPart} key={part.id}>
            {part.type === 'elicitation' ? (
              <AgentElicitation part={part} />
            ) : part.type === 'toolCall' ? (
              <>
                <span>{`${part.status === 'completed' ? '✓' : part.status === 'pending' ? '!' : '…'} ${part.toolCall?.name.replaceAll('_', ' ')}`}</span>
                {part.status === 'pending' && part.toolCall?.approvalId && (
                  <span className={styles.approvalActions}>
                    <button disabled={approvalBusy === part.toolCall.approvalId} onClick={() => void decide(part.toolCall, 'reject')} type="button">
                      {t('Reject tool')}
                    </button>
                    <button disabled={approvalBusy === part.toolCall.approvalId} onClick={() => void decide(part.toolCall, 'approve')} type="button">
                      {t('Approve tool')}
                    </button>
                  </span>
                )}
              </>
            ) : part.type === 'reasoning' ? (
              `Thinking: ${part.text ?? ''}`
            ) : (
              part.text
            )}
          </div>
        ))}
      </div>
      <div className={styles.composer}>
        {(contextIssues.length > 0 || contextChips) && (
          <div className={styles.context}>
            {contextChips}
            {contextIssues.map(issue => (
              <span data-i18n-ignore key={issue.id}>
                <StatusIcon state={issue.state} size={14} />
                <small>{issue.identifier}</small>
                <b>{issue.title}</b>
              </span>
            ))}
          </div>
        )}
        <textarea
          aria-label={t('Send a message to Flow Agent')}
          disabled={!enabled || composerDisabled || loading}
          onChange={event => onInputChange(event.target.value)}
          onKeyDown={event => {
            if (event.key === 'Enter' && !event.shiftKey) {
              event.preventDefault()
              onSubmit()
            }
          }}
          placeholder={
            placeholder ??
            (enabled ? t('Ask a question…') : t('Flow Agent is not configured'))
          }
          ref={inputRef}
          rows={2}
          value={input}
        />
        <footer>
          {error ? <span role="alert">{error}</span> : <span />}
          {loading ? (
            <button aria-label={t('Stop generating')} onClick={onStop} type="button">
              <X />
            </button>
          ) : (
            <button
              aria-label={t('Send message')}
              disabled={!input.trim() || !enabled || composerDisabled}
              onClick={onSubmit}
              type="button"
            >
              <Send />
            </button>
          )}
        </footer>
      </div>
    </div>
  )
}

function ThreadMessageActivity({
  parts,
  onToolApproval,
  approvalBusy,
}: {
  parts: AgentMessagePart[]
  onToolApproval: (call: AgentMessagePart['toolCall'] | undefined, decision: 'approve' | 'reject') => void
  approvalBusy?: string
}) {
  const { t } = useI18n()
  const work = parts.filter(part => part.type === 'reasoning' || part.type === 'toolCall')
  if (!work.length) return null
  const running = work.some(
    part =>
      part.status === 'running' ||
      part.status === 'pending' ||
      part.toolCall?.status === 'running' ||
      part.toolCall?.status === 'pending',
  )
  return (
    <details className={styles.messageActivity} open={running || undefined}>
      <summary>
        {running ? <LoaderCircle /> : <Check />}
        <span>{running ? t('Thinking…') : t('Work completed')}</span>
        <ChevronRight />
      </summary>
      <div>
        {work.map(part => (
          <div key={part.id}>
            {part.type === 'reasoning' ? (
              <>
                <strong>{t('Reasoning')}</strong>
                <p>{part.text}</p>
              </>
            ) : (
              <>
                <span>{part.toolCall?.name.replaceAll('_', ' ')}</span>
                {part.status === 'pending' && part.toolCall?.approvalId && (
                  <span className={styles.approvalActions}>
                    <button disabled={approvalBusy === part.toolCall.approvalId} onClick={() => void onToolApproval(part.toolCall, 'reject')} type="button">
                      {t('Reject tool')}
                    </button>
                    <button disabled={approvalBusy === part.toolCall.approvalId} onClick={() => void onToolApproval(part.toolCall, 'approve')} type="button">
                      {t('Approve tool')}
                    </button>
                  </span>
                )}
              </>
            )}
          </div>
        ))}
      </div>
    </details>
  )
}

export function clearEntityThreadDraft(conversationDraftKey: string) {
  clearAgentDraft(agentDraftStorageKey(conversationDraftKey))
}
