import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowUp, X } from 'lucide-react'
import { AgentElicitation } from './agent-elicitation'
import { AgentElicitationResponseQueue } from './agent-elicitation-response-queue'
import { summarizeElicitationQueue } from './agent-elicitation-queue-model'
import { AgentRichText } from './agent-rich-text'
import { AgentAnswerText, AgentReferencedResources, AgentSuggestionChips } from './agent-answer'
import { AgentChangeCards } from './agent-change-cards'
import { agentChangeEntityKeys } from './agent-change-model'
import { AgentReplyActions, AgentUserMessageActions } from './agent-message-actions'
import { parseAgentAnswer } from './agent-answer-content'
import { useAgentEntityData } from './agent-entity-data'
import { AgentQuestionPrompt } from './agent-approval-question'
import { AgentWorkGroup } from './agent-work-group'
import { isBlankStoppedReply, settleStoppedAgentReply } from './agent-stopped-reply'
import { formatAgentTime, shouldShowAgentTime } from './agent-time'
import { StatusIcon } from '@/components/issue/issue-icons'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import type { AgentMessage, AgentMessagePart, AgentSession, BootstrapData } from '@/types/flow'
import { AgentMentionInput, type AgentMention } from './agent-mention-input'
import { mentionIcon } from './agent-mention-icon'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import {
  agentDraftStorageKey,
  clearAgentDraft,
  readAgentDraft,
  writeAgentDraft,
} from './agent-drafts'
import styles from './entity-agent-thread.module.css'
import { AgentErrorDetail } from './agent-error-detail'
import { GridLoader } from '@/components/ui/grid-loader'
import { splitAgentQuote } from './agent-floating-request'
import { AgentSelectedTextIcon } from './agent-icons'

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
  /** Content rendered between a reply and its actions (e.g. The reference app's "Created draft" card). */
  renderMessageAttachment?: (message: AgentMessage, index: number) => ReactNode
  /**
   * Replaces an assistant message's body (work group, questions, text, attachment) — the loop builder lays its
   * questions, answers and tool cards out in order. Feedback buttons still follow.
   */
  renderAssistantBody?: (message: AgentMessage, state: { index: number; streaming: boolean }) => ReactNode
  /** Answers the latest reply's pending question (ask_user) with one of its options. */
  onAnswer?: (part: AgentMessagePart, choice: { label: string; value: string }) => void
  /** The saved chat, so reply ratings are kept on the server. */
  sessionId?: string
  onSessionChange?: (session: AgentSession) => void
  /** Selected text shown as a chip at the start of the composer (the reference app's "Ask agent" on a selection). */
  quote?: string
  onQuoteRemove?: () => void
  /** Mention chips the composer starts with (the reference app's "Ask the reference app" on an "Issue created" toast). */
  initialMentions?: AgentMention[]
  /** Extra composer buttons before Send (e.g. the page-context toggle and attach). */
  composerTools?: ReactNode
  /** Tab in the composer; return true when handled. */
  onComposerTab?: () => boolean
  /** Whether Send is enabled; defaults to "the composer has text". */
  canSend?: boolean
  /** The reference app's floating chat shows nothing in an empty conversation (no greeting, no hint). */
  blankEmpty?: boolean
  /** Rows shown right above the composer (e.g. attached files). */
  composerHeader?: ReactNode
}

export type AgentContextEntity = { key: string; icon?: ReactNode; label: string; onRemove?: () => void }

/** LS-0253 EntityAgentThread — shared conversation + draft renderer for page/sidebar panels. */
export function EntityAgentThread({
  messages: savedMessages,
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
  onAnswer,
  sessionId,
  onSessionChange,
  quote,
  onQuoteRemove,
  initialMentions,
  composerTools,
  onComposerTab,
  canSend,
  blankEmpty = false,
  composerHeader,
}: EntityAgentThreadProps) {
  const { t } = useI18n()
  // A reply the user stopped keeps only its work (no partial text, "stopped" row or rating).
  const messages = useMemo(() => savedMessages.map(settleStoppedAgentReply), [savedMessages])
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
    requestAnimationFrame(() => {
      // Don't steal focus from the page (e.g. the document editor the user is clicking into) or scroll it.
      const active = document.activeElement
      const pageHasFocus = active && active !== document.body && !inputRef.current?.closest('[data-agent-thread]')?.contains(active)
      if (pageHasFocus) return
      inputRef.current?.focus({ preventScroll: true })
    })
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
  // Steps render in the work group above (their plan text as markdown), never again as a plain row.
  const streamOther = streamParts.filter(part => part.type !== 'reasoning' && part.type !== 'step' && part.type !== 'toolCall')
  // Once the reply has started (its text streams into the last message), its live work goes above that text, like
  // The reference app's "Worked for…" group above the answer — not after it, where text written after the tools ran read first.
  const liveWorkInReply = loading && streamWork.length > 0 && savedMessages.at(-1)?.role === 'assistant' && !renderAssistantBody
  const lastMessage = messages.at(-1)
  // The reference app's composer reads "Something else…" while the newest reply offers choices (question options or suggestions).
  const offersChoices = !loading && lastMessage?.role === 'assistant' && (Boolean(lastMessage.parts?.some(part => part.type === 'question' && part.status === 'pending')) || parseAgentAnswer(lastMessage.content, entityData).suggestions.length > 0)
  const composerPlaceholder = messages.length && enabled
    ? t(offersChoices ? 'Something else…' : 'Reply…')
    : placeholder ?? (enabled ? t('Ask a question…') : t('Flow Agent is not configured'))

  return (
    <div className={styles.thread} data-agent-thread="">
      <div
        aria-label={t('Agent conversation')}
        aria-live="polite"
        className={styles.conversation}
        ref={logRef}
        role="log"
      >
        {!messages.length && !loading && !blankEmpty && (
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
          // An option picked on a question shows as the chosen pill there, not as a separate message.
          if (isUser && message.answerTo) return null
          // A reply stopped before it did anything leaves only the message you sent.
          if (!isUser && isBlankStoppedReply(savedMessages[index])) return null
          const question = isUser ? undefined : message.parts?.find(part => part.type === 'question' && part.question)
          const replyText = question ? (message.parts ?? []).filter(part => part.type === 'text').map(part => part.text ?? '').join('') : message.content
          const time = shouldShowAgentTime(messages, index) ? formatAgentTime(message.createdAt, t('Today')) : ''
          const work = isUser ? [] : (message.parts ?? []).filter(part => part.type === 'reasoning' || part.type === 'step' || part.type === 'toolCall')
          const streaming = loading && index === messages.length - 1
          const extraActions = renderMessageActions?.(message, index)
          const answer = isUser ? undefined : parseAgentAnswer(replyText, entityData)
          // The reference app rates answers only: a reply that asks a question (ask_user) has no 👍👎 row, answered or not.
          const showFeedback = !isUser && Boolean(answer?.prose.trim()) && !streaming && !question
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
                    {streaming && liveWorkInReply ? (
                      <AgentWorkGroup
                        approvalBusy={approvalBusy}
                        className={styles.work}
                        message={{}}
                        onToolApproval={decide}
                        parts={streamWork}
                        running
                      />
                    ) : work.length > 0 && (
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
                {!isUser && renderAssistantBody ? null : isUser && splitAgentQuote(message.content).quote !== undefined ? (
                  <p className={`${styles.messageDocument} agent-chat-document`} aria-label={t('Your message')}><QuotedText data={mentionData} mentions={message.mentions} content={message.content}/></p>
                ) : message.content && isUser && mentionData && (message.mentions?.length || /@[A-Z][A-Z0-9]*-\d+/.test(message.content)) ? (
                  <p className={`${styles.messageDocument} agent-chat-document`} aria-label={t('Your message')}><MentionedText data={mentionData} mentions={message.mentions} text={message.content}/></p>
                ) : answer ? answer.markdown && (
                  <AgentAnswerText
                    ariaLabel={t('AI message')}
                    className={`${styles.messageDocument} agent-chat-document`}
                    data={entityData}
                    markdown={answer.markdown}
                  />
                ) : message.content && (
                  <AgentRichText
                    ariaLabel={t('Your message')}
                    className={`${styles.messageDocument} agent-chat-document`}
                    content={message.content}
                  />
                )}
                {answer && !streaming && !renderAssistantBody && <AgentReferencedResources data={entityData} exclude={agentChangeEntityKeys(message)} references={answer.references} />}
                {!isUser && !renderAssistantBody && <AgentChangeCards data={entityData} message={message} onSessionChange={onSessionChange} sessionId={sessionId} />}
                {!isUser && !streaming && !renderAssistantBody && renderMessageAttachment?.(message, index)}
                {question?.question && !renderAssistantBody && (
                  <div className={styles.question}>
                    <AgentQuestionPrompt
                      ariaLabel={t('AI message')}
                      className={`${styles.messageDocument} agent-chat-document`}
                      data={entityData}
                      disabled={!enabled || composerDisabled || loading || question.status !== 'pending' || index !== latestAssistantIndex}
                      onSelect={choice => onAnswer?.(question, choice)}
                      part={question}
                    />
                  </div>
                )}
                {!question && <AgentSuggestionChips disabled={!enabled || composerDisabled} onSelect={sendSuggestion} suggestions={suggestions} />}
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
                {isUser && <AgentUserMessageActions className={styles.userActions} content={message.content}/>}
                {showFeedback && <AgentReplyActions className={`${styles.replyActions}${index === latestAssistantIndex ? ` ${styles.latestReplyActions}` : ''}`} content={answer?.prose ?? message.content} message={message} onSessionChange={onSessionChange} sessionId={sessionId}/>}
                {extraActions && <div className={styles.messageActions}>{extraActions}</div>}
              </article>
            </Fragment>
          )
        })}
        {loading && streamWork.length > 0 && !liveWorkInReply && (
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
            <GridLoader variant="agent" size={14} />
            {t('Thinking…')}
          </div>
        )}
        {streamOther.map(part => (
          <div className={styles.streamPart} key={part.id}>
            {part.type === 'elicitation' ? <AgentElicitation part={part} /> : part.type === 'error' ? <AgentErrorDetail error={part.text ?? ''} /> : part.type === 'question' && part.question
              // ask_user's question streams while the model writes it; its options arrive with the finished call.
              ? <AgentQuestionPrompt ariaLabel={t('AI message')} className={`${styles.messageDocument} agent-chat-document`} data={entityData} disabled part={part}/>
              : part.text}
          </div>
        ))}
      </div>
      <div className={styles.composerArea}>
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
      {composerHeader}
      <div className={styles.composer} data-tooltip-elevated="">
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
            quote={quote}
            onQuoteRemove={onQuoteRemove}
            initialMentions={initialMentions}
            onTab={onComposerTab}
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
          {error ? <span role="alert"><AgentErrorDetail error={error} /></span> : <span />}
          {composerTools}
          {loading ? (
            <ScopedFlowTooltip label={t('Stop responding')} sideOffset={8}>
              <button aria-label={t('Stop responding')} onClick={onStop} type="button">
                <X />
              </button>
            </ScopedFlowTooltip>
          ) : (
            <ScopedFlowTooltip label={t('Send message')} shortcut="Enter" sideOffset={8}>
              <button
                aria-label={t('Send message')}
                data-state={(canSend ?? Boolean(input.trim())) && enabled ? 'ready' : 'empty'}
                disabled={!(canSend ?? input.trim()) || !enabled || composerDisabled}
                onClick={onSubmit}
                type="button"
              >
                <ArrowUp />
              </button>
            </ScopedFlowTooltip>
          )}
        </footer>
      </div>
      </div>
    </div>
  )
}

/** A sent message that starts with a quoted selection: the selection as the reference app's inline chip, then the text. */
function QuotedText({ data, mentions, content }: { data?: BootstrapData; mentions?: AgentMessage['mentions']; content: string }) {
  const { quote, text } = splitAgentQuote(content)
  return <>
    <span className={styles.sentQuote} data-i18n-ignore><AgentSelectedTextIcon className={styles.sentQuoteIcon}/>{quote}</span>
    {text ? ' ' : null}
    {text && (data ? <MentionedText data={data} mentions={mentions} text={text}/> : text)}
  </>
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
