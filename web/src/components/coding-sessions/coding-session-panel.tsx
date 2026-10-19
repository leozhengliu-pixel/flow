import { useEffect, useRef, useState, type DragEvent, type FormEvent, type ReactNode } from 'react'
import { AlertTriangle, MoreHorizontal, RotateCcw, ShieldQuestion, X } from 'lucide-react'
import {
  approveCodingSession,
  dismissCodingSession,
  createCodingSessionPullRequest,
  fixCodingEnvironmentWithAgent,
  promptCodingSession,
  restartCodingSession,
  setCodingSessionSubscription,
  startReviewCodingSession,
  stopCodingSession,
} from '@/lib/coding-sessions-api'
import { AgentAttachIcon, AgentStopIcon, AgentSubmitIcon } from '@/components/agent/agent-icons'
import { AGENT_ATTACHMENT_ACCEPT, AGENT_UPLOAD_MAX_ITEMS, agentFileKind, agentUploadLimit, pastedAgentFiles, shrinkAgentImage } from '@/components/agent/agent-attachments'
import { uploadAgentFile } from '@/lib/api'
import { AgentAvatarMark, AgentCodingGlyph } from '@/components/ui/agent-glyph'
import { AppLink } from '@/components/ui/app-link'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { toast } from '@/lib/toast'
import type { AgentMessageAttachment, BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionActivity, type CodingActivityMode } from './coding-session-activity'
import { notifyCodingSessionChanged } from './coding-session-events'
import { CODING_PHASE_LABELS, codingErrorInfo, codingModelLabel, codingRunStartedAt, codingSessionCapabilities, codingStatusLine, currentSandbox, formatCodingCost, formatCodingElapsed, isCodingSessionLive, type CodingErrorAction } from './coding-session-model'
import { CodingChanges, CodingPhaseProgress, CodingPlan, CodingPullRequests, CodingSandboxInfo, CodingSandboxLogs, CodingStateBadge } from './coding-session-parts'
import { useCodingSession, useNow } from './use-coding-sessions'
import { CodingSessionArtifacts } from './artifacts/coding-session-artifacts'
import { CodingDiffViewer } from './coding-session-diff'
import { codingSessionSubscribed, codingSessionURL } from './coding-session-page-model'
import './coding-sessions.css'

export type CodingSessionPanelProps = {
  data: BootstrapData
  /** Activity stream mode; `steps` folds thoughts, actions and errors (the composer and plan stay out). */
  activity?: CodingActivityMode
  /** Shows "Archive" for a finished session (lists and chat cards). */
  onDismiss?: () => void
  /** Opens another session (the one "Fix with agent" or "Fix failing checks" started). */
  onOpenSession?: (session: CodingSession) => void
  /** Hides the agent panel (the "Hide agent" toggle of the session view). */
  agentHidden?: boolean
  className?: string
}

/** A live session view by id (fetches it and follows realtime updates). */
export function CodingSessionView({ sessionId, initial, ...props }: CodingSessionPanelProps & { sessionId: string; initial?: CodingSession }) {
  const { t } = useI18n()
  const { session, error, apply } = useCodingSession(sessionId, initial)
  if (!session) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : <p className="coding-session-muted" role="status">{t('Loading coding session…')}</p>
  return <CodingSessionPanel session={session} onChange={apply} {...props}/>
}

/** The agent avatar with the session's status dot (the reference app's 18px avatar with a 6px dot ringed in the panel colour). */
export function CodingAgentAvatar({ state }: { state: CodingSession['state'] }) {
  return <span className="coding-agent-avatar" data-state={state} aria-hidden="true"><AgentAvatarMark/><span className="coding-agent-avatar-dot"/></span>
}

/** "Today 11:00 PM" / "Oct 18, 11:00 PM" above the session stream. */
function dayLabel(value: string, locale: string, today: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const time = date.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
  return date.toDateString() === new Date().toDateString() ? `${today} ${time}` : `${date.toLocaleDateString(locale, { month: 'short', day: 'numeric' })}, ${time}`
}

/**
 * The coding session view, laid out like the reference app's session page: the changes (PRs, plan, diff summary,
 * commits, sandbox, screenshots) on the left, the agent panel on the right (header with the session menu, the activity
 * stream with the error card and the "Thinking…" row, and the follow-up composer with Stop).
 */
export function CodingSessionPanel({ session, data, onChange, activity = 'all', onDismiss, onOpenSession, agentHidden, className }: CodingSessionPanelProps & { session: CodingSession; onChange: (session: CodingSession) => void }) {
  const { t, locale } = useI18n()
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [error, setError] = useState('')
  const [showLogs, setShowLogs] = useState(false)
  const can = codingSessionCapabilities(session)
  const steps = activity === 'steps'
  const model = codingModelLabel(session)
  const phase = CODING_PHASE_LABELS[session.phase]
  const admin = data.viewerRole === 'admin' || data.viewerRole === 'owner'
  /** Runs a session action; false when it failed (the error shows under the panel). */
  const run = async (action: () => Promise<CodingSession | void>, done?: string) => {
    if (busyRef.current) return false
    busyRef.current = true; setBusy(true); setError('')
    try {
      const next = await action()
      if (next && next.id === session.id) onChange(next)
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
      if (done) toast.success(t(done))
      return true
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); return false }
    finally { busyRef.current = false; setBusy(false) }
  }
  const startOther = (start: () => Promise<CodingSession>, done: string) => run(async () => {
    const other = await start()
    onOpenSession?.(other)
    toast.success(t(done))
  })
  const restart = () => run(() => restartCodingSession(session.id), 'Restarted coding session')
  const restartDefault = () => run(() => restartCodingSession(session.id, { useDefaultModel: true }), 'Restarted with the default model')
  const createPullRequest = () => run(() => createCodingSessionPullRequest(session.id), 'Creating pull request')
  const errorInfo = session.state === 'error' ? codingErrorInfo(session.errorCode, { workspace: data.workspace.urlKey, codeHost: session.codeHost }) : undefined
  const sandbox = currentSandbox(session)
  const errorAction = (action: CodingErrorAction) => {
    switch (action.kind) {
      case 'link': return <AppLink key={action.href} className="coding-session-button is-primary" href={action.href}>{t(action.label)}</AppLink>
      case 'restart': return <button key="restart" type="button" className="coding-session-button" disabled={busy} onClick={() => void restart()}>{t('Retry')}</button>
      case 'restartWithDefaultModel': return session.modelSelection === 'auto' && session.errorCode !== 'modelUnavailable' ? null : <button key="default-model" type="button" className="coding-session-button" disabled={busy} onClick={() => void restartDefault()}>{t('Restart with default model')}</button>
      case 'createPullRequest': return session.branchName && !session.latestPullRequest && !session.pullRequests?.length ? <button key="create-pr" type="button" className="coding-session-button" disabled={busy} onClick={() => void createPullRequest()}>{t(session.codeHost === 'gitlab' ? 'Create merge request' : 'Create pull request')}</button> : null
      case 'fixWithAgent': return session.codingEnvironmentId ? <button key="fix" type="button" className="coding-session-button" disabled={busy} onClick={() => void startOther(() => fixCodingEnvironmentWithAgent(session.codingEnvironmentId!, session.snapshotId), 'Started a session to fix the environment')}>{t('Fix with agent')}</button> : null
      case 'viewLogs': return sandbox ? <button key="logs" type="button" className="coding-session-button" aria-expanded={showLogs} onClick={() => setShowLogs(value => !value)}>{t(showLogs ? 'Hide logs' : 'View logs')}</button> : null
    }
  }
  const status = codingStatusLine(session)
  const live = isCodingSessionLive(session) && session.phase !== 'awaitingApproval'
  const now = useNow(live)
  const started = codingRunStartedAt(session)
  const elapsed = live && started && !Number.isNaN(Date.parse(started)) ? formatCodingElapsed(now - Date.parse(started)) : ''
  const errorActions = errorInfo ? errorInfo.actions.map(errorAction).filter(Boolean) : []
  const menuRestart = can.restart && !errorInfo?.actions.some(action => action.kind === 'restart')
  const menuRestartDefault = can.restartWithDefaultModel && !errorInfo?.actions.some(action => action.kind === 'restartWithDefaultModel')
  const menuCreatePullRequest = can.createPullRequest && !errorInfo?.actions.some(action => action.kind === 'createPullRequest')

  const title = session.displayTitle || data.issues.find(issue => issue.id === session.issueId)?.title || session.repository || t('Coding session')
  return <section className={`coding-session-panel${agentHidden ? ' is-agent-hidden' : ''}${className ? ` ${className}` : ''}`} data-state={session.state} data-phase={session.phase} aria-label={t('Coding session')}>
    <div className="coding-session-changes">
      <div className="coding-session-overview">
        <CodingStateBadge state={session.state}/>
        {phase && session.state !== 'error' && <span className="coding-session-phase">{t(phase)}</span>}
        <span className="coding-session-meta">
          <span className="coding-session-model" data-i18n-ignore>{model.harness} · {model.model}</span>
          <span className="coding-session-cost" aria-label={t('Cost')}>{formatCodingCost(session.costMicros)}</span>
        </span>
      </div>
      <CodingPhaseProgress session={session}/>
      {session.state === 'stale' && session.stoppedBy && <p className="coding-session-muted">{t('Stopped by {name}').replace('{name}', data.users.find(user => user.id === session.stoppedBy)?.displayName || t('a member'))}</p>}
      {!steps && <CodingPlan plan={session.plan}/>}
      {session.summary && !steps && session.state === 'complete' && <p className="coding-session-summary" data-i18n-ignore>{session.summary}</p>}
      <CodingPullRequests session={session} reviews={data.reviews ?? []} busy={busy} onFixChecks={(_, review) => void startOther(() => startReviewCodingSession(review.id, { kind: 'fix' }), 'Started a session to fix failing checks')}/>
      <CodingSandboxInfo session={session}/>
      <CodingChanges session={session} commitsOnly/>
      <CodingSessionArtifacts sessionId={session.id} artifacts={session.artifacts} refreshKey={session.updatedAt}/>
      <CodingDiffViewer session={session}/>
    </div>
    <aside className="coding-agent-panel" aria-label={t('Agent')} hidden={agentHidden}>
      <header className="coding-agent-panel-header">
        <CodingAgentAvatar state={session.state}/>
        <h3 data-i18n-ignore>{title}</h3>
        <CodingSessionMenu session={session} data={data} busy={busy} onChange={onChange} items={<>
          {can.stop && <DropdownMenuItem disabled={busy} onSelect={() => void run(() => stopCodingSession(session.id), 'Stopping coding session')}>{t('Stop session')}</DropdownMenuItem>}
          {menuRestart && <DropdownMenuItem disabled={busy} onSelect={() => void restart()}><RotateCcw size={14} aria-hidden="true"/>{t('Restart')}</DropdownMenuItem>}
          {menuRestartDefault && <DropdownMenuItem disabled={busy} onSelect={() => void restartDefault()}>{t('Restart with default model')}</DropdownMenuItem>}
          {menuCreatePullRequest && <DropdownMenuItem disabled={busy} onSelect={() => void createPullRequest()}>{t(session.codeHost === 'gitlab' ? 'Create merge request' : 'Create pull request')}</DropdownMenuItem>}
        </>} onArchive={can.dismiss ? onDismiss : undefined}/>
      </header>
      <div className="coding-agent-stream" role="group" aria-label={t('Session stream')}>
        <time className="coding-agent-day" dateTime={session.createdAt}>{dayLabel(session.createdAt, locale, t('Today'))}</time>
        {can.approve && <div className="coding-session-callout" data-kind="approval" role="group" aria-label={t('Approval required')}>
          <div className="coding-session-callout-title"><ShieldQuestion size={14} aria-hidden="true"/><strong>{t('Approval required')}</strong></div>
          <p>{t('This session comes from an external source. A member must approve it before the agent starts.')}</p>
          <div className="coding-session-callout-actions">
            <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => approveCodingSession(session.id, true), 'Rejected coding session')}>{t('Reject')}</button>
            <button type="button" className="coding-session-button is-primary" disabled={busy} onClick={() => void run(() => approveCodingSession(session.id), 'Approved coding session')}>{t('Approve')}</button>
          </div>
        </div>}
        <CodingSessionActivity data={data} agentSessionId={session.agentSessionId} issueId={session.issueId} mode={activity} awaitingInput={session.phase === 'awaitingUser'} revision={`${session.version}:${session.updatedAt}`}/>
        {session.phase === 'awaitingUser' && <div className="coding-session-callout" data-kind="input" role="group" aria-label={t('Needs input')}>
          <div className="coding-session-callout-title"><ShieldQuestion size={14} aria-hidden="true"/><strong>{t('Needs input')}</strong></div>
          <p>{t('The agent asked a question. Answer it to continue.')}</p>
          <div className="coding-session-callout-actions">
            <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => dismissCodingSession(session.id), 'Ended coding session')}>{t('End session')}</button>
          </div>
        </div>}
        {errorInfo && <div className="coding-session-callout" data-kind="error" role="alert" data-code={session.errorCode || 'internal'}>
          <div className="coding-session-callout-title"><AlertTriangle size={14} aria-hidden="true"/><strong>{t(errorInfo.title)}</strong></div>
          <p>{t(errorInfo.message)}</p>
          {session.error && (!errorInfo.adminDetail || admin) && <p className="coding-session-error-detail" data-i18n-ignore>{session.error}</p>}
          {errorActions.length > 0 && <div className="coding-session-callout-actions">{errorActions}</div>}
        </div>}
        {showLogs && sandbox && <CodingSandboxLogs sessionId={session.id} sandbox={sandbox} defaultOpen/>}
        {live && <div className="coding-agent-working" role="status">
          <span className="coding-agent-spinner" aria-hidden="true"/>
          <span className="coding-agent-working-label">{t(status.label)}{status.ellipsis ? '…' : ''}<span aria-hidden="true" className="coding-agent-working-shine">{t(status.label)}{status.ellipsis ? '…' : ''}</span></span>
          {elapsed && <time className="coding-agent-elapsed" aria-label={t('Elapsed time')}>{elapsed}</time>}
        </div>}
      </div>
      {!steps && can.prompt && <CodingFollowUp disabled={busy} awaitingInput={session.phase === 'awaitingUser'} stop={can.stop ? () => void run(() => stopCodingSession(session.id), 'Stopping coding session') : undefined}
        onSubmit={(prompt, files) => run(() => promptCodingSession(session.id, prompt, undefined, files))}/>}
      {steps && can.stop && <div className="coding-session-actions"><button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => stopCodingSession(session.id), 'Stopping coding session')}>{t('Stop')}</button></div>}
      {error && <p className="coding-session-error-text" role="alert">{error}</p>}
    </aside>
  </section>
}

/** "…" Session details: the model, "Subscribe to session", copy actions, session actions and Archive. */
export function CodingSessionMenu({ session, data, busy, items, onArchive, onChange }: { session: CodingSession; data: BootstrapData; busy?: boolean; items?: ReactNode; onArchive?: () => void; /** Receives the session after a subscription change. */ onChange?: (session: CodingSession) => void }) {
  const { t } = useI18n()
  const model = codingModelLabel(session)
  const subscribed = codingSessionSubscribed(session, data.viewer.id)
  const [subscribing, setSubscribing] = useState(false)
  const copy = async (text: string, done: string) => {
    try { await navigator.clipboard.writeText(text); toast.success(t(done)) }
    catch { toast.error(t('Could not copy')) }
  }
  const toggleSubscription = async () => {
    if (subscribing) return
    setSubscribing(true)
    try {
      const next = await setCodingSessionSubscription(session.id, !subscribed)
      onChange?.(next)
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
      toast.success(t(subscribed ? 'Unsubscribed from session' : 'Subscribed to session'))
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : t('Could not update subscription')) }
    finally { setSubscribing(false) }
  }
  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <button type="button" className="coding-session-icon-button coding-agent-menu-trigger" aria-label={t('Session details')} disabled={busy}><MoreHorizontal size={14}/></button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" className="coding-session-menu">
      <DropdownMenuLabel>{t('Session details')}</DropdownMenuLabel>
      <DropdownMenuItem disabled className="coding-session-menu-detail"><AgentCodingGlyph size={16}/><span data-i18n-ignore>{model.harness} · {model.model}</span></DropdownMenuItem>
      <DropdownMenuItem disabled className="coding-session-menu-detail"><span className="coding-session-menu-cost">{t('Cost')}</span><span data-i18n-ignore>{formatCodingCost(session.costMicros)}</span></DropdownMenuItem>
      <DropdownMenuSeparator/>
      <DropdownMenuItem role="menuitemcheckbox" aria-checked={subscribed} disabled={subscribing} className="coding-session-menu-subscribe" onSelect={event => { event.preventDefault(); void toggleSubscription() }}>
        <span className="coding-session-menu-cost">{t('Subscribe to session')}</span>
        <span className="coding-session-menu-toggle" data-checked={subscribed || undefined} aria-hidden="true"/>
      </DropdownMenuItem>
      {items && <><DropdownMenuSeparator/>{items}</>}
      <DropdownMenuSeparator/>
      <DropdownMenuSub>
        <DropdownMenuSubTrigger>{t('Copy')}</DropdownMenuSubTrigger>
        <DropdownMenuSubContent className="coding-session-menu">
          <DropdownMenuItem onSelect={() => void copy(codingSessionURL(data.workspace.urlKey, session.id), 'Copied session URL')}>{t('Session URL')}</DropdownMenuItem>
          {session.branchName && <DropdownMenuItem onSelect={() => void copy(session.branchName!, 'Copied branch name')}>{t('Branch name')}</DropdownMenuItem>}
        </DropdownMenuSubContent>
      </DropdownMenuSub>
      {onArchive && <><DropdownMenuSeparator/><DropdownMenuItem onSelect={onArchive}>{t('Archive')}</DropdownMenuItem></>}
    </DropdownMenuContent>
  </DropdownMenu>
}

type ComposerFile = { key: string; name: string; contentType: string; size: number; url?: string; preview?: string; failed?: boolean }

/**
 * The follow-up composer ("Tell Flow what to do next…"): a new turn on the warm sandbox, or a restore after the idle TTL
 * (§4.3). Files attach with the paperclip, by pasting or by dropping them, upload at once like the agent chat's, and go
 * with the prompt (the runner gets signed URLs). Stop replaces send while the agent works.
 */
function CodingFollowUp({ disabled, awaitingInput, stop, onSubmit }: { disabled: boolean; awaitingInput: boolean; stop?: () => void; onSubmit: (prompt: string, files: AgentMessageAttachment[]) => Promise<boolean> }) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const [files, setFiles] = useState<ComposerFile[]>([])
  const [dragging, setDragging] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const filesRef = useRef(files)
  filesRef.current = files
  useEffect(() => () => { for (const file of filesRef.current) if (file.preview) URL.revokeObjectURL(file.preview) }, [])
  const body = value.trim()
  const uploading = files.some(file => !file.url && !file.failed)
  const ready = files.filter((file): file is ComposerFile & { url: string } => Boolean(file.url) && !file.failed)
  const canSend = !disabled && !uploading && Boolean(body || ready.length)
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
  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    if (!canSend) return
    if (!await onSubmit(body, ready.map(({ name, contentType, size, url }) => ({ name, contentType, size, url })))) return
    setValue('')
    for (const file of filesRef.current) if (file.preview) URL.revokeObjectURL(file.preview)
    setFiles([])
  }
  const hasFiles = (event: DragEvent) => [...(event.dataTransfer?.types ?? [])].includes('Files')
  return <form className="coding-session-composer" data-dragging={dragging || undefined} onSubmit={event => void submit(event)}
    onDragOver={event => { if (!hasFiles(event)) return; event.preventDefault(); setDragging(true) }}
    onDragLeave={() => setDragging(false)}
    onDrop={event => { if (!hasFiles(event)) return; event.preventDefault(); setDragging(false); void attach([...event.dataTransfer.files]) }}>
    {files.length > 0 && <ul className="coding-session-composer-files" aria-label={t('Attachments')}>
      {files.map(file => <li key={file.key} data-state={file.failed ? 'failed' : file.url ? 'ready' : 'uploading'}>
        {file.preview ? <img src={file.preview} alt=""/> : <AgentAttachIcon size={12} aria-hidden="true"/>}
        <span data-i18n-ignore title={file.name}>{file.name}</span>
        {!file.url && !file.failed && <span className="sr-only">{t('Uploading…')}</span>}
        <button type="button" className="coding-session-icon-button" aria-label={t('Remove {name}').replace('{name}', file.name)} onClick={() => remove(file.key)}><X size={12}/></button>
      </li>)}
    </ul>}
    <textarea rows={1} aria-label={t('Follow-up prompt')} placeholder={t(awaitingInput ? 'Answer the agent…' : 'Tell Flow what to do next…')} value={value} disabled={disabled} onChange={event => setValue(event.target.value)}
      onPaste={event => {
        const pasted = pastedAgentFiles(event.clipboardData)
        if (!pasted.length) return
        event.preventDefault()
        void attach(pasted)
      }}
      onKeyDown={event => {
        if (commentShortcutMatches(event) && canSend) { event.preventDefault(); void submit() }
      }}/>
    <div className="coding-session-composer-tools">
      <input ref={input} type="file" multiple hidden accept={AGENT_ATTACHMENT_ACCEPT} data-testid="coding-session-file-input" onChange={event => {
        const picked = [...(event.target.files ?? [])]
        event.target.value = ''
        void attach(picked)
      }}/>
      <button type="button" className="coding-session-composer-attach" aria-label={t('Attach files')} disabled={disabled || files.length >= AGENT_UPLOAD_MAX_ITEMS} onClick={() => input.current?.click()}><AgentAttachIcon size={14}/></button>
      {stop && !body && !ready.length
        ? <button type="button" className="coding-session-composer-send is-stop" aria-label={t('Stop')} disabled={disabled} onClick={stop}><AgentStopIcon size={14}/></button>
        : <button type="submit" className="coding-session-composer-send" aria-label={t('Send follow-up')} disabled={!canSend}><AgentSubmitIcon size={14}/></button>}
    </div>
  </form>
}
