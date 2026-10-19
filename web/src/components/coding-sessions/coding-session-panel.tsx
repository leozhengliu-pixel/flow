import { useRef, useState, type FormEvent } from 'react'
import { AlertTriangle, ArrowUp, RotateCcw, ShieldQuestion, Square, X } from 'lucide-react'
import {
  approveCodingSession,
  dismissCodingSession,
  createCodingSessionPullRequest,
  fixCodingEnvironmentWithAgent,
  promptCodingSession,
  restartCodingSession,
  startReviewCodingSession,
  stopCodingSession,
} from '@/lib/coding-sessions-api'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionActivity, type CodingActivityMode } from './coding-session-activity'
import { notifyCodingSessionChanged } from './coding-session-events'
import { CODING_PHASE_LABELS, codingErrorInfo, codingModelLabel, codingSessionCapabilities, currentSandbox, formatCodingCost, type CodingErrorAction } from './coding-session-model'
import { CodingChanges, CodingPhaseProgress, CodingPlan, CodingPullRequests, CodingSandboxInfo, CodingSandboxLogs, CodingStateBadge } from './coding-session-parts'
import { useCodingSession } from './use-coding-sessions'
import { CodingSessionArtifacts } from './artifacts/coding-session-artifacts'
import './coding-sessions.css'

export type CodingSessionPanelProps = {
  data: BootstrapData
  /** Activity stream mode; `steps` inside an issue thread (which renders the replies, plan and reply box itself). */
  activity?: CodingActivityMode
  /** Shows "Dismiss" for a finished session (lists and chat cards). */
  onDismiss?: () => void
  /** Opens another session (the one "Fix with agent" or "Fix failing checks" started). */
  onOpenSession?: (session: CodingSession) => void
  className?: string
}

/** A live session view by id (fetches it and follows realtime updates). */
export function CodingSessionView({ sessionId, initial, ...props }: CodingSessionPanelProps & { sessionId: string; initial?: CodingSession }) {
  const { t } = useI18n()
  const { session, error, apply } = useCodingSession(sessionId, initial)
  if (!session) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : <p className="coding-session-muted" role="status">{t('Loading coding session…')}</p>
  return <CodingSessionPanel session={session} onChange={apply} {...props}/>
}

/**
 * The coding session view (§24 track G): state with Linear's labels and phase progress, harness and model, cost,
 * approval and error prompts with their next action, plan, PR/MR links with CI, sandbox and logs, changes, the
 * activity stream, and the actions (stop, restart, restart with default model, create PR, follow-up, dismiss).
 */
export function CodingSessionPanel({ session, data, onChange, activity = 'all', onDismiss, onOpenSession, className }: CodingSessionPanelProps & { session: CodingSession; onChange: (session: CodingSession) => void }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [error, setError] = useState('')
  const [showLogs, setShowLogs] = useState(false)
  const can = codingSessionCapabilities(session)
  const embedded = activity === 'steps'
  const model = codingModelLabel(session)
  const phase = CODING_PHASE_LABELS[session.phase]
  const admin = data.viewerRole === 'admin' || data.viewerRole === 'owner'
  const run = async (action: () => Promise<CodingSession | void>, done?: string) => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true); setError('')
    try {
      const next = await action()
      if (next && next.id === session.id) onChange(next)
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
      if (done) toast.success(t(done))
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { busyRef.current = false; setBusy(false) }
  }
  const startOther = (start: () => Promise<CodingSession>, done: string) => run(async () => {
    const other = await start()
    onOpenSession?.(other)
    toast.success(t(done))
  })
  const errorInfo = session.state === 'error' ? codingErrorInfo(session.errorCode, { workspace: data.workspace.urlKey, codeHost: session.codeHost }) : undefined
  const sandbox = currentSandbox(session)
  const errorAction = (action: CodingErrorAction) => {
    switch (action.kind) {
      case 'link': return <AppLink key={action.href} className="coding-session-button is-primary" href={action.href}>{t(action.label)}</AppLink>
      case 'restart': return <button key="restart" type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => restartCodingSession(session.id), 'Restarted coding session')}><RotateCcw size={13} aria-hidden="true"/>{t('Restart')}</button>
      case 'restartWithDefaultModel': return session.modelSelection === 'auto' && session.errorCode !== 'modelUnavailable' ? null : <button key="default-model" type="button" className="coding-session-button is-primary" disabled={busy} onClick={() => void run(() => restartCodingSession(session.id, { useDefaultModel: true }), 'Restarted with the default model')}>{t('Restart with default model')}</button>
      case 'createPullRequest': return session.branchName && !session.latestPullRequest && !session.pullRequests?.length ? <button key="create-pr" type="button" className="coding-session-button is-primary" disabled={busy} onClick={() => void run(() => createCodingSessionPullRequest(session.id), 'Creating pull request')}>{t(session.codeHost === 'gitlab' ? 'Create merge request' : 'Create pull request')}</button> : null
      case 'fixWithAgent': return session.codingEnvironmentId ? <button key="fix" type="button" className="coding-session-button is-primary" disabled={busy} onClick={() => void startOther(() => fixCodingEnvironmentWithAgent(session.codingEnvironmentId!, session.snapshotId), 'Started a session to fix the environment')}>{t('Fix with agent')}</button> : null
      case 'viewLogs': return sandbox ? <button key="logs" type="button" className="coding-session-button" aria-expanded={showLogs} onClick={() => setShowLogs(value => !value)}>{t(showLogs ? 'Hide logs' : 'View logs')}</button> : null
    }
  }
  return <section className={`coding-session-panel${embedded ? ' is-embedded' : ''}${className ? ` ${className}` : ''}`} data-state={session.state} data-phase={session.phase} aria-label={t('Coding session')}>
    <header className="coding-session-header">
      <CodingStateBadge state={session.state}/>
      {session.displayTitle && !embedded && <h3 data-i18n-ignore>{session.displayTitle}</h3>}
      {phase && session.state !== 'error' && <span className="coding-session-phase">{t(phase)}</span>}
      <span className="coding-session-meta">
        <span className="coding-session-model" data-i18n-ignore>{model.harness} · {model.model}</span>
        <span className="coding-session-cost" aria-label={t('Cost')}>{formatCodingCost(session.costMicros)}</span>
      </span>
    </header>
    <CodingPhaseProgress session={session}/>
    {can.approve && <div className="coding-session-callout" data-kind="approval" role="group" aria-label={t('Approval required')}>
      <ShieldQuestion size={15} aria-hidden="true"/>
      <div><strong>{t('Approval required')}</strong><p>{t('This session comes from an external source. A member must approve it before the agent starts.')}</p></div>
      <div className="coding-session-callout-actions">
        <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => approveCodingSession(session.id, true), 'Rejected coding session')}>{t('Reject')}</button>
        <button type="button" className="coding-session-button is-primary" disabled={busy} onClick={() => void run(() => approveCodingSession(session.id), 'Approved coding session')}>{t('Approve')}</button>
      </div>
    </div>}
    {session.phase === 'awaitingUser' && <div className="coding-session-callout" data-kind="input" role="group" aria-label={t('Needs input')}>
      <ShieldQuestion size={15} aria-hidden="true"/>
      <div><strong>{t('Needs input')}</strong><p>{t('The agent asked a question. Answer it to continue.')}</p></div>
      <div className="coding-session-callout-actions">
        <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => dismissCodingSession(session.id), 'Ended coding session')}>{t('End session')}</button>
      </div>
    </div>}
    {session.state === 'stale' && session.stoppedBy && <p className="coding-session-muted">{t('Stopped by {name}').replace('{name}', data.users.find(user => user.id === session.stoppedBy)?.displayName || t('a member'))}</p>}
    {errorInfo && <div className="coding-session-callout" data-kind="error" role="alert" data-code={session.errorCode || 'internal'}>
      <AlertTriangle size={15} aria-hidden="true"/>
      <div>
        <strong>{t(errorInfo.title)}</strong>
        <p>{t(errorInfo.message)}</p>
        {session.error && (!errorInfo.adminDetail || admin) && <p className="coding-session-error-detail" data-i18n-ignore>{session.error}</p>}
      </div>
      {errorInfo.actions.length > 0 && <div className="coding-session-callout-actions">{errorInfo.actions.map(errorAction)}</div>}
    </div>}
    {showLogs && sandbox && <CodingSandboxLogs sessionId={session.id} sandbox={sandbox} defaultOpen/>}
    {!embedded && <CodingPlan plan={session.plan}/>}
    {session.summary && !embedded && session.state === 'complete' && <p className="coding-session-summary" data-i18n-ignore>{session.summary}</p>}
    <CodingPullRequests session={session} reviews={data.reviews ?? []} busy={busy} onFixChecks={(_, review) => void startOther(() => startReviewCodingSession(review.id, { kind: 'fix' }), 'Started a session to fix failing checks')}/>
    <CodingSandboxInfo session={session}/>
    <CodingChanges session={session}/>
    <CodingSessionArtifacts sessionId={session.id} artifacts={session.artifacts} refreshKey={session.updatedAt}/>
    <CodingSessionActivity data={data} agentSessionId={session.agentSessionId} issueId={session.issueId} mode={activity} awaitingInput={session.phase === 'awaitingUser'} revision={`${session.version}:${session.updatedAt}`}/>
    <div className="coding-session-actions">
      {can.stop && <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => stopCodingSession(session.id), 'Stopping coding session')}><Square size={12} aria-hidden="true"/>{t('Stop')}</button>}
      {can.restart && !errorInfo?.actions.some(action => action.kind === 'restart') && <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => restartCodingSession(session.id), 'Restarted coding session')}><RotateCcw size={13} aria-hidden="true"/>{t('Restart')}</button>}
      {can.restartWithDefaultModel && !errorInfo?.actions.some(action => action.kind === 'restartWithDefaultModel') && <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => restartCodingSession(session.id, { useDefaultModel: true }), 'Restarted with the default model')}>{t('Restart with default model')}</button>}
      {can.createPullRequest && !errorInfo?.actions.some(action => action.kind === 'createPullRequest') && <button type="button" className="coding-session-button" disabled={busy} onClick={() => void run(() => createCodingSessionPullRequest(session.id), 'Creating pull request')}>{t(session.codeHost === 'gitlab' ? 'Create merge request' : 'Create pull request')}</button>}
      {can.dismiss && onDismiss && <button type="button" className="coding-session-button" onClick={onDismiss}><X size={13} aria-hidden="true"/>{t('Dismiss')}</button>}
    </div>
    {!embedded && can.prompt && <CodingFollowUp disabled={busy} awaitingInput={session.phase === 'awaitingUser'} onSubmit={prompt => run(() => promptCodingSession(session.id, prompt))}/>}
    {error && <p className="coding-session-error-text" role="alert">{error}</p>}
  </section>
}

/** The follow-up composer: a new turn on the warm sandbox, or a restore after the idle TTL (§4.3). */
function CodingFollowUp({ disabled, awaitingInput, onSubmit }: { disabled: boolean; awaitingInput: boolean; onSubmit: (prompt: string) => Promise<void> }) {
  const { t } = useI18n()
  const [value, setValue] = useState('')
  const body = value.trim()
  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    if (!body || disabled) return
    await onSubmit(body)
    setValue('')
  }
  return <form className="coding-session-composer" onSubmit={event => void submit(event)}>
    <textarea aria-label={t('Follow-up prompt')} placeholder={t(awaitingInput ? 'Answer the agent…' : 'Ask for follow-up changes…')} value={value} disabled={disabled} onChange={event => setValue(event.target.value)} onKeyDown={event => {
      if (commentShortcutMatches(event) && body) { event.preventDefault(); void submit() }
    }}/>
    <button type="submit" aria-label={t('Send follow-up')} disabled={disabled || !body}><ArrowUp size={14} aria-hidden="true"/></button>
  </form>
}
