import { useEffect, useRef, useState, type FormEvent, type ReactNode, type TextareaHTMLAttributes } from 'react'
import { ChevronDown, CornerDownLeft, Link2, Reply, X } from 'lucide-react'
import { AgentStopIcon, AgentSubmitIcon } from '@/components/agent/agent-icons'
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { SearchableMenuItems } from '@/components/ui/searchable-menu-items'
import { useI18n } from '@/i18n/i18n'
import { recommendedChoiceValue, type ApplicationActivity } from '@/lib/application-agents'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import type { BootstrapData } from '@/types/flow'
import { readQueuedMessages, selectOptionsInline, writeQueuedMessages, type QueuedMessage } from './coding-session-stream-model'
import './coding-session-stream.css'

export type ComposerOption = { label: string; value: string }

/** What the composer asks for: an agent question (with select options or an account link) or a member's approval. */
export type ComposerRequest =
  | { kind: 'elicitation'; activity: ApplicationActivity }
  | { kind: 'approval' }

/** The queue of messages typed while the agent works, kept per session in this browser. */
function useCodingMessageQueue(workspace: string, sessionId: string) {
  const [messages, setMessages] = useState<QueuedMessage[]>(() => readQueuedMessages(workspace, sessionId))
  useEffect(() => { setMessages(readQueuedMessages(workspace, sessionId)) }, [workspace, sessionId])
  const update = (next: (current: QueuedMessage[]) => QueuedMessage[]) => setMessages(current => {
    const value = next(current)
    writeQueuedMessages(workspace, sessionId, value)
    return value
  })
  return {
    messages,
    add: (text: string) => update(current => [...current, { id: `q-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`, text, createdAt: new Date().toISOString() }]),
    remove: (id: string) => update(current => current.filter(message => message.id !== id)),
  }
}

const isShortcutSend = (event: { key: string; shiftKey: boolean; metaKey: boolean; ctrlKey: boolean }) => event.key === 'Enter' && event.shiftKey && (event.metaKey || event.ctrlKey)

function OptionDropdown({ options, selected, onSelect }: { options: ComposerOption[]; selected?: ComposerOption; onSelect: (option: ComposerOption) => void }) {
  const { t } = useI18n()
  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <button type="button" className={`cs-option-select${selected ? ' is-selected' : ''}`} title={selected?.label}>
        <span data-i18n-ignore={selected ? true : undefined}>{selected ? selected.label : t('Select an option')}</span><ChevronDown size={14} aria-hidden="true"/>
      </button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" className="coding-session-menu cs-option-menu">
      <SearchableMenuItems options={options.map(option => ({ id: option.value, label: option.label, entity: true, keywords: option.value }))} selectedId={selected?.value} searchLabel="Filter options"
        renderOption={option => <span className="cs-option-menu-label" title={option.label} data-i18n-ignore>{option.label}</span>}
        onSelect={option => onSelect({ label: option.label, value: option.id })}/>
    </DropdownMenuContent>
  </DropdownMenu>
}

export type CodingComposerProps = {
  data: BootstrapData
  sessionId: string
  /** The agent is producing output: an empty composer stops it, a typed message is queued. */
  generating: boolean
  /** A stop was requested and the agent has not stopped yet. */
  stopPending: boolean
  /** Follow-ups are accepted (not while stopping or waiting for approval). */
  canPrompt: boolean
  busy: boolean
  request?: ComposerRequest
  onSend: (text: string) => Promise<boolean | void>
  onStop: () => void
  onApprove: (reject: boolean) => Promise<void>
  /** Attachment chips (above the editor) and the attach button (toolbar start), when the host supports them. */
  attachments?: ReactNode
  toolbarStart?: ReactNode
  /** Files still uploading: sending waits. */
  uploading?: boolean
  textareaProps?: Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'value' | 'onChange' | 'onKeyDown'>
  /** Something besides the text can be sent (attachments). */
  hasPayload?: boolean
}

/**
 * The follow-up composer, like the reference app's: Stop (square) while the agent works and nothing is typed, Queue
 * when something is (⌘⇧Enter sends right away), the queued messages above the editor, and, when the agent needs input,
 * a purple frame with the question's options in the toolbar and "Continue" once one is chosen.
 */
export function CodingComposer({ data, sessionId, generating, stopPending, canPrompt, busy, request, onSend, onStop, onApprove, attachments, toolbarStart, uploading, textareaProps, hasPayload }: CodingComposerProps) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const queue = useCodingMessageQueue(data.workspace.urlKey, sessionId)
  const flushing = useRef(false)
  const body = value.trim()
  const elicitation = request?.kind === 'elicitation' ? request.activity : undefined
  const approval = request?.kind === 'approval'
  const options: ComposerOption[] = approval
    ? [{ label: t('Approve'), value: 'approve' }, { label: t('Reject'), value: 'reject' }]
    : elicitation?.signal === 'select' ? (elicitation.signalMetadata?.options ?? []).map(option => ({ label: option.label || option.value, value: option.value })) : []
  const requestKey = approval ? 'approval' : elicitation?.id ?? ''
  const recommended = elicitation?.signal === 'select' ? recommendedChoiceValue(elicitation.signalMetadata) : undefined
  const [selected, setSelected] = useState<string | undefined>(recommended)
  // A new question (or none) resets the choice to its recommendation.
  useEffect(() => { setSelected(recommended) }, [requestKey, recommended])
  const choice = options.find(option => option.value === selected)
  const agentName = t('Flow')
  const auth = elicitation?.signal === 'auth' ? elicitation : undefined
  const authUser = auth?.signalMetadata?.userId ? data.users.find(user => user.id === auth.signalMetadata!.userId) : undefined
  const authForOther = Boolean(authUser && authUser.id !== data.viewer.id)
  const authUrl = auth?.signalMetadata?.url && /^https?:\/\//i.test(auth.signalMetadata.url) ? auth.signalMetadata.url : undefined
  const provider = auth?.signalMetadata?.providerName || 'GitHub'

  // Queued messages go out one at a time once the agent is idle.
  useEffect(() => {
    if (generating || busy || !canPrompt || request || flushing.current || !queue.messages.length) return
    const [next] = queue.messages
    flushing.current = true
    void onSend(next.text).then(sent => { if (sent !== false) queue.remove(next.id) }).finally(() => { flushing.current = false })
  }, [generating, busy, canPrompt, request, queue.messages]) // eslint-disable-line react-hooks/exhaustive-deps -- flush on state changes only

  const sendNow = async (text: string) => {
    const sent = await onSend(text)
    if (sent !== false) setValue('')
  }
  const submit = async (event?: FormEvent, immediate = false) => {
    event?.preventDefault()
    if (busy || uploading) return
    if (approval) {
      if (choice) await onApprove(choice.value === 'reject')
      return
    }
    if (choice) {
      await sendNow(body ? `${choice.value}\n\n${body}` : choice.value)
      return
    }
    if (!body && !hasPayload) return
    if (generating && !immediate && body) {
      queue.add(body)
      setValue('')
      return
    }
    if (!canPrompt) return
    await sendNow(body)
  }

  const queueMode = generating && Boolean(body) && !choice
  const stopMode = generating && !body && !choice && !hasPayload
  const selectPending = options.length > 0 && !choice && !body
  const framed = Boolean(request)
  const inline = options.length > 0 && selectOptionsInline(options)
  const sendLabel = choice ? t('Continue') : queueMode ? t('Queue') : ''

  const editor = <form className="coding-session-composer cs-composer" onSubmit={event => void submit(event)} data-framed={framed || undefined}>
    {queue.messages.length > 0 && <ul className="cs-queue" aria-label={t('Queued messages')}>
      {queue.messages.map(message => <li key={message.id}>
        <Reply size={16} aria-hidden="true" className="cs-queue-icon"/>
        <span className="cs-queue-text" title={message.text} data-i18n-ignore>{message.text}</span>
        <span className="cs-queue-actions">
          <button type="button" className="cs-button is-ghost is-small" title={t('Send immediately (steer)')} disabled={busy || !canPrompt} onClick={() => void onSend(message.text).then(sent => { if (sent !== false) queue.remove(message.id) })}>{t('Send')}</button>
          <button type="button" className="coding-session-icon-button" aria-label={t('Remove from queue')} title={t('Remove from queue')} onClick={() => queue.remove(message.id)}><X size={14}/></button>
        </span>
      </li>)}
    </ul>}
    {attachments}
    <textarea rows={1} aria-label={t('Follow-up prompt')} placeholder={t('Tell {agent} what to do next…').replace('{agent}', agentName)} value={value} disabled={!canPrompt} {...textareaProps}
      onChange={event => setValue(event.target.value)}
      onKeyDown={event => {
        if (isShortcutSend(event)) { event.preventDefault(); void submit(undefined, true); return }
        if (commentShortcutMatches(event) && (body || choice || hasPayload)) { event.preventDefault(); void submit() }
      }}/>
    <div className="coding-session-composer-tools cs-composer-tools">
      {toolbarStart}
      {options.length > 0 && <div className="cs-options" role="group" aria-label={t('Options')}>
        {inline
          ? options.map(option => <button key={option.value} type="button" className="cs-option-chip" aria-pressed={selected === option.value} title={option.label === option.value ? option.label : `${option.label}\n${option.value}`}
            onClick={() => setSelected(current => current === option.value ? undefined : option.value)}><span data-i18n-ignore={approval ? undefined : true}>{option.label}</span></button>)
          : <OptionDropdown options={options} selected={choice} onSelect={option => setSelected(option.value)}/>}
      </div>}
      <span className="cs-composer-spacer"/>
      {stopMode
        ? <button type="button" className="coding-session-composer-send is-stop" aria-label={t('Stop agent')} title={stopPending ? t('Stop request already sent, waiting for {agent}').replace('{agent}', agentName) : t('Stop agent')} disabled={busy || stopPending} onClick={onStop}><AgentStopIcon size={14}/></button>
        : sendLabel
          ? <button type="submit" className={`cs-send-label${choice ? ' is-primary' : ''}`} aria-label={choice ? t('Continue with selected option') : t('Queue message')}
            title={choice ? t('Enter to continue with selected option') : `${t('Enter to queue message')}\n${t('Ctrl/⌘ Shift Enter to send immediately')}`} disabled={busy || uploading}>
            {sendLabel}{choice && <CornerDownLeft size={12} aria-hidden="true"/>}
          </button>
          : <button type="submit" className="coding-session-composer-send" aria-label={t('Submit message')} disabled={busy || uploading || selectPending || (!body && !hasPayload) || !canPrompt}><AgentSubmitIcon size={14}/></button>}
    </div>
  </form>
  if (!framed) return editor
  return <div className="cs-input-frame">
    <div className="cs-input-frame-header">
      {auth
        ? <>
          <span>{authForOther ? t('Waiting for {name} to connect their {provider} account to continue').replace('{name}', authUser?.displayName || authUser?.name || '').replace('{provider}', provider) : t('Connect your {provider} account to continue').replace('{provider}', provider)}</span>
          {!authForOther && authUrl && <a className="cs-connect-pill" href={authUrl} target="_blank" rel="noreferrer"><Link2 size={14} aria-hidden="true"/>{t('Connect')}</a>}
        </>
        : <span>{t('Input needed to continue')}</span>}
    </div>
    {editor}
  </div>
}
