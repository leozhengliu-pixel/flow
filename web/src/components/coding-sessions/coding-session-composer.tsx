import { useEffect, useRef, useState, type DragEvent, type FormEvent } from 'react'
import { ChevronDown, CornerDownLeft, Link2, Reply, X } from 'lucide-react'
import { AgentAttachIcon, AgentStopIcon, AgentSubmitIcon } from '@/components/agent/agent-icons'
import { AGENT_ATTACHMENT_ACCEPT, AGENT_UPLOAD_MAX_ITEMS, agentFileKind, agentUploadLimit, pastedAgentFiles, shrinkAgentImage } from '@/components/agent/agent-attachments'
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { SearchableMenuItems } from '@/components/ui/searchable-menu-items'
import { useI18n } from '@/i18n/i18n'
import { uploadAgentFile } from '@/lib/api'
import { type ApplicationActivity } from '@/lib/application-agents'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { toast } from '@/lib/toast'
import type { AgentMessageAttachment, BootstrapData } from '@/types/flow'
import { readQueuedMessages, selectOptionsInline, writeQueuedMessages, type QueuedMessage } from './coding-session-stream-model'
import './coding-session-stream.css'

type ComposerOption = { label: string; value: string }

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
    add: (text: string, files: AgentMessageAttachment[]) => update(current => [...current, { id: `q-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`, text, files: files.length ? files : undefined, createdAt: new Date().toISOString() }]),
    remove: (id: string) => update(current => current.filter(message => message.id !== id)),
  }
}

type ComposerFile = { key: string; name: string; contentType: string; size: number; url?: string; preview?: string; failed?: boolean }

/** Files attached with the paperclip, by pasting or by dropping them; each uploads at once and goes with the next message. */
function useComposerFiles() {
  const { t } = useI18n()
  const [files, setFiles] = useState<ComposerFile[]>([])
  const filesRef = useRef(files)
  filesRef.current = files
  useEffect(() => () => { for (const file of filesRef.current) if (file.preview) URL.revokeObjectURL(file.preview) }, [])
  const attach = async (picked: File[]) => {
    const usable = picked.filter(file => agentFileKind(file) !== 'audio')
    const room = AGENT_UPLOAD_MAX_ITEMS - filesRef.current.length
    if (usable.length > room) toast.error(t('A message can carry up to 8 files'), { id: 'coding-attachment-limit' })
    for (const original of usable.slice(0, Math.max(0, room))) {
      const file = await shrinkAgentImage(original)
      if (file.size > agentUploadLimit(file)) { toast.error(t('{name} is too large to attach').replace('{name}', file.name)); continue }
      const key = `${Date.now()}-${Math.random().toString(36).slice(2)}`
      const preview = agentFileKind(file) === 'image' && typeof URL.createObjectURL === 'function' ? URL.createObjectURL(file) : undefined
      setFiles(items => [...items, { key, name: file.name, contentType: file.type || 'application/octet-stream', size: file.size, preview }])
      uploadAgentFile(file)
        .then(upload => setFiles(items => items.map(item => item.key === key ? { ...item, url: upload.url, name: upload.name || item.name, contentType: upload.contentType || item.contentType, size: upload.size } : item)))
        .catch(error => {
          setFiles(items => items.map(item => item.key === key ? { ...item, failed: true } : item))
          toast.error(error instanceof Error ? error.message : t('Could not upload file'))
        })
    }
  }
  const remove = (key: string) => setFiles(items => items.filter(item => {
    if (item.key === key && item.preview) URL.revokeObjectURL(item.preview)
    return item.key !== key
  }))
  const clear = () => {
    for (const file of filesRef.current) if (file.preview) URL.revokeObjectURL(file.preview)
    setFiles([])
  }
  const ready: AgentMessageAttachment[] = files.filter((file): file is ComposerFile & { url: string } => Boolean(file.url) && !file.failed).map(({ name, contentType, size, url }) => ({ name, contentType, size, url }))
  return { files, ready, uploading: files.some(file => !file.url && !file.failed), attach, remove, clear }
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
      <SearchableMenuItems options={options.map(option => ({ id: option.value, label: option.label, entity: true, keywords: option.value }))} selectedId={selected?.value} searchLabel="Filter options" itemClassName="menu-item" searchClassName="property-command-search"
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
  /** Sends a message (with its files); false when it failed and should stay. Flow infers the turn's write scope from the text. */
  onSend: (text: string, files: AgentMessageAttachment[]) => Promise<boolean>
  onStop: () => void
  onApprove: (reject: boolean) => Promise<void>
}

/**
 * The follow-up composer, like the reference app's: Stop (square) while the agent works and nothing is typed, Queue
 * when something is (⌘⇧Enter sends right away), the queued messages above the editor, and, when the agent needs input,
 * a purple frame with the question's options in the toolbar and "Continue" once one is chosen. Files attach with the
 * paperclip, by pasting or by dropping them, and go with the message.
 */
export function CodingComposer({ data, sessionId, generating, stopPending, canPrompt, busy, request, onSend, onStop, onApprove }: CodingComposerProps) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const [dragging, setDragging] = useState(false)
  const picker = useRef<HTMLInputElement>(null)
  const queue = useCodingMessageQueue(data.workspace.urlKey, sessionId)
  const attachments = useComposerFiles()
  const flushing = useRef(false)
  const body = value.trim()
  const hasFiles = attachments.ready.length > 0
  const elicitation = request?.kind === 'elicitation' ? request.activity : undefined
  const approval = request?.kind === 'approval'
  const options: ComposerOption[] = approval
    ? [{ label: t('Approve'), value: 'approve' }, { label: t('Reject'), value: 'reject' }]
    : []
  const requestKey = approval ? 'approval' : elicitation?.id ?? ''
  const [selected, setSelected] = useState<string | undefined>()
  // A new request (or none) clears the choice.
  useEffect(() => { setSelected(undefined) }, [requestKey])
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
    void onSend(next.text, next.files ?? []).then(sent => { if (sent) queue.remove(next.id) }).finally(() => { flushing.current = false })
  }, [generating, busy, canPrompt, request, queue.messages]) // eslint-disable-line react-hooks/exhaustive-deps -- flush on state changes only

  const sendNow = async (text: string) => {
    if (await onSend(text, attachments.ready)) { setValue(''); attachments.clear() }
  }
  const submit = async (event?: FormEvent, immediate = false) => {
    event?.preventDefault()
    if (busy || attachments.uploading) return
    if (approval) {
      if (choice) await onApprove(choice.value === 'reject')
      return
    }
    if (choice) {
      await sendNow(body ? `${choice.value}\n\n${body}` : choice.value)
      return
    }
    if (!body && !hasFiles) return
    if (generating && !immediate) {
      queue.add(body, attachments.ready)
      setValue('')
      attachments.clear()
      return
    }
    if (!canPrompt) return
    await sendNow(body)
  }

  const typed = Boolean(body) || hasFiles
  const queueMode = generating && typed && !choice
  const stopMode = generating && !typed && !choice
  const selectPending = options.length > 0 && !choice && !body
  const inline = options.length > 0 && selectOptionsInline(options)
  const isFileDrag = (event: DragEvent) => [...(event.dataTransfer?.types ?? [])].includes('Files')

  const editor = <form className="coding-session-composer cs-composer" data-dragging={dragging || undefined} onSubmit={event => void submit(event)}
    onDragOver={event => { if (!isFileDrag(event)) return; event.preventDefault(); setDragging(true) }}
    onDragLeave={() => setDragging(false)}
    onDrop={event => { if (!isFileDrag(event)) return; event.preventDefault(); setDragging(false); void attachments.attach([...event.dataTransfer.files]) }}>
    {queue.messages.length > 0 && <ul className="cs-queue" aria-label={t('Queued messages')}>
      {queue.messages.map(message => <li key={message.id}>
        <Reply size={16} aria-hidden="true" className="cs-queue-icon"/>
        <span className="cs-queue-text" title={message.text} data-i18n-ignore>{message.text || message.files?.map(file => file.name).join(', ')}</span>
        <span className="cs-queue-actions">
          <button type="button" className="cs-button is-ghost is-small" title={t('Send immediately (steer)')} disabled={busy || !canPrompt} onClick={() => void onSend(message.text, message.files ?? []).then(sent => { if (sent) queue.remove(message.id) })}>{t('Send')}</button>
          <button type="button" className="coding-session-icon-button" aria-label={t('Remove from queue')} title={t('Remove from queue')} onClick={() => queue.remove(message.id)}><X size={14}/></button>
        </span>
      </li>)}
    </ul>}
    {attachments.files.length > 0 && <ul className="coding-session-composer-files" aria-label={t('Attachments')}>
      {attachments.files.map(file => <li key={file.key} data-state={file.failed ? 'failed' : file.url ? 'ready' : 'uploading'}>
        {file.preview ? <img src={file.preview} alt=""/> : <AgentAttachIcon size={12} aria-hidden="true"/>}
        <span data-i18n-ignore title={file.name}>{file.name}</span>
        {!file.url && !file.failed && <span className="sr-only">{t('Uploading…')}</span>}
        <button type="button" className="coding-session-icon-button" aria-label={t('Remove {name}').replace('{name}', file.name)} onClick={() => attachments.remove(file.key)}><X size={12}/></button>
      </li>)}
    </ul>}
    <textarea rows={1} aria-label={t('Send a message to {agent}').replace('{agent}', agentName)} placeholder={t('Tell {agent} what to do next…').replace('{agent}', agentName)} value={value} disabled={!canPrompt}
      onChange={event => setValue(event.target.value)}
      onPaste={event => {
        const pasted = pastedAgentFiles(event.clipboardData)
        if (!pasted.length) return
        event.preventDefault()
        void attachments.attach(pasted)
      }}
      onKeyDown={event => {
        if (isShortcutSend(event)) { event.preventDefault(); void submit(undefined, true); return }
        if (commentShortcutMatches(event) && (typed || choice)) { event.preventDefault(); void submit() }
      }}/>
    <div className="coding-session-composer-tools cs-composer-tools">
      <input ref={picker} type="file" multiple hidden accept={AGENT_ATTACHMENT_ACCEPT} data-testid="coding-session-file-input" onChange={event => {
        const picked = [...(event.target.files ?? [])]
        event.target.value = ''
        void attachments.attach(picked)
      }}/>
      {options.length > 0 && <div className="cs-options" role="group" aria-label={t('Options')}>
        {inline
          ? options.map(option => <button key={option.value} type="button" className="cs-option-chip" aria-pressed={selected === option.value} title={option.label === option.value ? option.label : `${option.label}\n${option.value}`}
            onClick={() => setSelected(current => current === option.value ? undefined : option.value)}><span data-i18n-ignore={approval ? undefined : true}>{option.label}</span></button>)
          : <OptionDropdown options={options} selected={choice} onSelect={option => setSelected(option.value)}/>}
      </div>}
      <span className="cs-composer-spacer"/>
      {/* Attach sits beside send on the right, like the reference app's composer. */}
      <button type="button" className="coding-session-composer-attach" aria-label={t('Attach images, files, or videos')} title={t('Attach images, files, or videos')} disabled={!canPrompt || attachments.files.length >= AGENT_UPLOAD_MAX_ITEMS} onClick={() => picker.current?.click()}><AgentAttachIcon size={14}/></button>
      {stopMode
        ? <button type="button" className="coding-session-composer-send is-stop" aria-label={t('Stop agent')} title={stopPending ? t('Stop request already sent, waiting for {agent}').replace('{agent}', agentName) : t('Stop agent')} disabled={busy || stopPending} onClick={onStop}><AgentStopIcon size={14}/></button>
        : choice || queueMode
          ? <button type="submit" className={`cs-send-label${choice ? ' is-primary' : ''}`} aria-label={choice ? t('Continue with selected option') : t('Queue message')}
            title={choice ? t('Enter to continue with selected option') : `${t('Enter to queue message')}\n${t('Ctrl/⌘ Shift Enter to send immediately')}`} disabled={busy || attachments.uploading}>
            {choice ? t('Continue') : t('Queue')}{choice && <CornerDownLeft size={12} aria-hidden="true"/>}
          </button>
          : <button type="submit" className="coding-session-composer-send" aria-label={t('Submit message')} disabled={busy || attachments.uploading || selectPending || !typed || !canPrompt}><AgentSubmitIcon size={14}/></button>}
    </div>
  </form>
  // A choice question is plain text in the stream, answered in the normal composer; only approvals and account links frame it.
  if (!request || (elicitation && !auth)) return editor
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
