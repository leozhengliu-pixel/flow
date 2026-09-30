import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import { ArrowUp, Check, Copy, LoaderCircle, ThumbsDown, ThumbsUp, X } from 'lucide-react'
import { AgentElicitation } from './agent-elicitation'
import { AgentElicitationResponseQueue, summarizeElicitationQueue } from './agent-elicitation-response-queue'
import { AgentRichText } from './agent-rich-text'
import { AgentAnswerText, AgentReferencedIssues, AgentSuggestionChips } from './agent-answer'
import { parseAgentAnswer } from './agent-answer-content'
import { useAgentEntityData } from './agent-entity-data'
import { AgentWorkGroup } from './agent-work-group'
import { formatAgentTime, shouldShowAgentTime } from './agent-time'
import { StatusIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import type { AgentMessage, AgentMessagePart, BootstrapData } from '@/types/flow'
import { AgentMentionInput, mentionIcon, type AgentMention } from './agent-mention-input'
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
  /** Sends a follow-up suggestion chip as the next user message; without it the chip fills the composer. */
  onSendSuggestion?: (message: string) => void
  onStop?: () => void
  enabled?: boolean
  emptyLabel?: string
  /** Composer placeholder for an empty conversation; once messages exist the composer reads "Reply…". */
  placeholder?: string
  contextIssues?: MyIssuesRowData[]
  /** Page entities attached to the next new conversation (e.g. the project being viewed). */
  contextEntities?: AgentContextEntity[]
  /** Entities that were attached when the conversation started; shown as "added to context" under the first message. */
  addedContext?: AgentContextEntity[]
  conversationDraftKey: string
  approvalBusy?: string
  onToolApproval?: (call: AgentMessagePart['toolCall'] | undefined, decision: 'approve' | 'reject') => void
  highlightedMessageId?: string
  composerDisabled?: boolean
  /** Empty-state greeting with suggestion pills that fill the composer. */
  welcome?: { title: string; subtitle: string; suggestions?: { label: string; icon?: ReactNode; prompt: string }[] }
  /** Workspace data for @-mentions; when set the composer becomes a mention editor. */
  mentionData?: BootstrapData
  onMentionsChange?: (mentions: AgentMention[]) => void
  /** Controls at the start of the composer footer (e.g. the Skills picker). */
  footerStart?: ReactNode
  onRemoveContext?: (issueId: string) => void
  /** Extra controls rendered under a message (e.g. "Insert into update"). */
  renderMessageActions?: (message: AgentMessage, index: number) => ReactNode
  /** Content rendered between a reply and its actions (e.g. Linear's "Created draft" card). */
  renderMessageAttachment?: (message: AgentMessage, index: number) => ReactNode
  /**
   * Replaces an assistant message's body (work group, questions, text, attachment) — the loop builder lays its
   * questions, answers and tool cards out in order. Feedback buttons still follow.
   */
  renderAssistantBody?: (message: AgentMessage, state: { index: number; streaming: boolean }) => ReactNode
}

export type AgentContextEntity = { key: string; icon?: ReactNode; label: string; onRemove?: () => void }

/** LS-0253 EntityAgentThread — shared conversation + draft renderer for page/sidebar panels. */
export function EntityAgentThread({
  messages,
  streamParts = [],
  loading = false,
  error,
  input,
  onInputChange,
  onSubmit,
  onSendSuggestion,
  onStop,
  enabled = true,
  emptyLabel,
  placeholder,
  contextIssues = [],
  contextEntities = [],
  addedContext = [],
  conversationDraftKey,
  approvalBusy,
  onToolApproval,
  highlightedMessageId,
  composerDisabled = false,
  welcome,
  mentionData,
  onMentionsChange,
  footerStart,
  onRemoveContext,
  renderMessageActions,
  renderMessageAttachment,
  renderAssistantBody,
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
  const firstUserIndex = messages.findIndex(message => message.role === 'user')
  const latestAssistantIndex = messages.findLastIndex(message => message.role === 'assistant')
  const entityData = useAgentEntityData(mentionData)
  const sendSuggestion = (suggestion: string) => {
    if (onSendSuggestion) onSendSuggestion(suggestion)
    else { onInputChange(suggestion); inputRef.current?.focus() }
  }
  const streamWork = streamParts.filter(part => part.type === 'reasoning' || part.type === 'step' || part.type === 'toolCall')
  const streamOther = streamParts.filter(part => part.type !== 'reasoning' && part.type !== 'toolCall')
  const composerPlaceholder = messages.length && enabled
    ? t('Reply…')
    : placeholder ?? (enabled ? t('Ask a question…') : t('Flow Agent is not configured'))

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
          welcome ? (
            <div className={styles.welcome} data-state="empty">
              <div className={styles.welcomePattern} aria-hidden="true"/>
              <strong>{welcome.title}</strong>
              <span>{welcome.subtitle}</span>
              {welcome.suggestions?.length ? (
                <div className={styles.suggestions}>
                  {welcome.suggestions.map(item => (
                    <button key={item.label} type="button" disabled={!enabled} onClick={() => { onInputChange(item.prompt); inputRef.current?.focus() }}>
                      {item.icon}{item.label}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          ) : (
          <div className={styles.empty} data-state="empty">
            <span>{emptyLabel ?? t('Ask anything or propose changes')}</span>
          </div>
          )
        )}
        {messages.map((message, index) => {
          const isUser = message.role === 'user'
          const time = shouldShowAgentTime(messages, index) ? formatAgentTime(message.createdAt, t('Today')) : ''
          const work = isUser ? [] : (message.parts ?? []).filter(part => part.type === 'reasoning' || part.type === 'step' || part.type === 'toolCall')
          const streaming = loading && index === messages.length - 1
          const extraActions = renderMessageActions?.(message, index)
          const answer = isUser ? undefined : parseAgentAnswer(message.content, entityData)
          const showFeedback = !isUser && Boolean(answer?.prose.trim()) && !streaming
          const suggestions = answer && index === latestAssistantIndex && !loading ? answer.suggestions : []
          return (
            <Fragment key={message.id || `${message.role}-${index}`}>
              {time && <time className={styles.messageTime} dateTime={message.createdAt}>{time}</time>}
              <article
                className={isUser ? styles.userMessage : styles.agentMessage}
                data-highlighted={highlightedMessageId === message.id || undefined}
              >
                {!isUser && renderAssistantBody ? renderAssistantBody(message, { index, streaming }) : !isUser && (
                  <>
                    {work.length > 0 && (
                      <AgentWorkGroup
                        approvalBusy={approvalBusy}
                        className={styles.work}
                        message={message}
                        onToolApproval={decide}
                        parts={work}
                      />
                    )}
                    {(() => {
                      const elicitations = message.parts?.filter(part => part.type === 'elicitation') ?? []
                      const queue = summarizeElicitationQueue(elicitations)
                      return (
                        <>
                          <AgentElicitationResponseQueue
                            answeredCount={queue.answeredCount}
                            elicitationCount={queue.elicitationCount}
                            isSubmitting={elicitations.some(part => part.status === 'running')}
                          />
                          {elicitations.map(part => <AgentElicitation key={part.id} part={part} />)}
                        </>
                      )
                    })()}
                  </>
                )}
                {!isUser && renderAssistantBody ? null : message.content && isUser && mentionData && (message.mentions?.length || /@[A-Z][A-Z0-9]*-\d+/.test(message.content)) ? (
                  <p className={styles.messageDocument} aria-label={t('Your message')}><MentionedText data={mentionData} mentions={message.mentions} text={message.content}/></p>
                ) : answer ? answer.markdown && (
                  <AgentAnswerText
                    ariaLabel={t('AI message')}
                    className={styles.messageDocument}
                    data={entityData}
                    markdown={answer.markdown}
                  />
                ) : message.content && (
                  <AgentRichText
                    ariaLabel={t('Your message')}
                    className={styles.messageDocument}
                    content={message.content}
                  />
                )}
                {answer && !streaming && !renderAssistantBody && <AgentReferencedIssues data={entityData} issues={answer.referencedIssues} />}
                {!isUser && !streaming && !renderAssistantBody && renderMessageAttachment?.(message, index)}
                <AgentSuggestionChips disabled={!enabled || composerDisabled} onSelect={sendSuggestion} suggestions={suggestions} />
                {isUser && index === firstUserIndex && addedContext.length > 0 && (
                  <div className={styles.addedContext}>
                    {addedContext.map(item => (
                      <span key={item.key}>
                        {item.icon}
                        <b data-i18n-ignore>{item.label}</b>
                        <span>{t('added to context')}</span>
                      </span>
                    ))}
                  </div>
                )}
                {(showFeedback || extraActions) && (
                  <div className={styles.messageActions}>
                    {showFeedback && <MessageFeedback content={answer?.prose ?? message.content} />}
                    {extraActions}
                  </div>
                )}
              </article>
            </Fragment>
          )
        })}
        {loading && streamWork.length > 0 && (
          <AgentWorkGroup
            approvalBusy={approvalBusy}
            className={styles.work}
            message={{}}
            onToolApproval={decide}
            parts={streamWork}
            running
          />
        )}
        {/* A custom body shows its own progress once the reply has started. */}
        {loading && !streamWork.length && !(renderAssistantBody && messages.at(-1)?.role === 'assistant') && (
          <div className={styles.thinking} data-state="loading">
            <LoaderCircle />
            {t('Thinking…')}
          </div>
        )}
        {streamOther.map(part => (
          <div className={styles.streamPart} key={part.id}>
            {part.type === 'elicitation' ? <AgentElicitation part={part} /> : part.text}
          </div>
        ))}
      </div>
      {/* Context chips describe what a new conversation will start with; once it has started they read as "added to context". */}
      {!messages.length && (contextIssues.length > 0 || contextEntities.length > 0) && (
          <div className={styles.context}>
            {contextEntities.map(item => (
              <span key={item.key}>
                {item.icon}
                <b data-i18n-ignore>{item.label}</b>
                {item.onRemove && <button type="button" className={styles.contextRemove} aria-label={t('Remove from context')} onClick={item.onRemove}><X size={14}/></button>}
              </span>
            ))}
            {contextIssues.map(issue => (
              <span data-i18n-ignore key={issue.id}>
                <StatusIcon state={issue.state} size={14} />
                <small>{issue.identifier}</small>
                <b>{issue.title}</b>
                {onRemoveContext && <button type="button" className={styles.contextRemove} aria-label={t('Remove from context')} onClick={() => onRemoveContext(issue.id)}><X size={14}/></button>}
              </span>
            ))}
          </div>
        )}
      <div className={styles.composer}>
        {mentionData ? (
          <AgentMentionInput
            ariaLabel={t('Send a message to Flow Agent')}
            data={mentionData}
            disabled={!enabled || composerDisabled || loading}
            pageIssues={contextIssues}
            placeholder={composerPlaceholder}
            value={input}
            onChange={(value, mentions) => { onInputChange(value); onMentionsChange?.(mentions) }}
            onSubmit={onSubmit}
          />
        ) : (
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
          placeholder={composerPlaceholder}
          ref={inputRef}
          rows={2}
          value={input}
        />
        )}
        <footer>
          {footerStart}
          {error ? <span role="alert">{error}</span> : <span />}
          {loading ? (
            <button aria-label={t('Stop responding')} onClick={onStop} type="button">
              <X />
            </button>
          ) : (
            <button
              aria-label={t('Send message')}
              disabled={!input.trim() || !enabled || composerDisabled}
              onClick={onSubmit}
              type="button"
            >
              <ArrowUp />
            </button>
          )}
        </footer>
      </div>
    </div>
  )
}

/** Thumbs up / down and copy for a finished assistant reply. */
function MessageFeedback({ content }: { content: string }) {
  const { t } = useI18n()
  const [rating, setRating] = useState<'up' | 'down'>()
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const timer = window.setTimeout(() => setCopied(false), 1500)
    return () => window.clearTimeout(timer)
  }, [copied])
  const copy = () => {
    void navigator.clipboard?.writeText(content).then(() => setCopied(true), () => undefined)
  }
  return (
    <>
      <button className={styles.feedbackButton} aria-label={t('Good response')} aria-pressed={rating === 'up'} onClick={() => setRating(current => current === 'up' ? undefined : 'up')} type="button">
        <ThumbsUp />
      </button>
      <button className={styles.feedbackButton} aria-label={t('Bad response')} aria-pressed={rating === 'down'} onClick={() => setRating(current => current === 'down' ? undefined : 'down')} type="button">
        <ThumbsDown />
      </button>
      <button className={styles.feedbackButton} aria-label={copied ? t('Copied to clipboard') : t('Copy message')} onClick={copy} type="button">
        {copied ? <Check /> : <Copy />}
      </button>
    </>
  )
}

export function clearEntityThreadDraft(conversationDraftKey: string) {
  clearAgentDraft(agentDraftStorageKey(conversationDraftKey))
}

/** A sent message with its @-mentions shown as chips (stored mentions, or issue identifiers). */
function MentionedText({ data, mentions, text }: { data: BootstrapData; mentions?: AgentMessage['mentions']; text: string }) {
  const chips = mentions?.length
    ? mentions.map(item => ({ token: `@${item.label}`, mention: item }))
    : [...new Set(text.match(/@[A-Z][A-Z0-9]*-\d+/g) ?? [])].flatMap(token => {
        const issue = data.issues.find(item => item.identifier === token.slice(1))
        return issue ? [{ token, mention: { type: 'issue' as const, id: issue.id, label: issue.identifier } }] : []
      })
  if (!chips.length) return <>{text}</>
  const pattern = new RegExp(`(${chips.map(item => item.token.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).sort((a, b) => b.length - a.length).join('|')})`)
  return <>{text.split(pattern).map((part, index) => {
    const chip = chips.find(item => item.token === part)
    if (!chip) return <span key={index}>{part}</span>
    const { mention } = chip
    const issue = mention.type === 'issue' ? data.issues.find(item => item.id === mention.id) : undefined
    return <span key={index} className={styles.sentMention} data-i18n-ignore>
      {mentionIcon(mention, data)}
      {issue ? <><span>{issue.identifier}</span> {issue.title}</> : mention.label}
    </span>
  })}</>
}
