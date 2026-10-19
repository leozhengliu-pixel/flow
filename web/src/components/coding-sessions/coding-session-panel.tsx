import { useRef, useState, type ReactNode } from 'react'
import { MoreHorizontal } from 'lucide-react'
import { restartCodingSession, setCodingSessionSubscription, startReviewCodingSession, stopCodingSession } from '@/lib/coding-sessions-api'
import { AgentAvatarMark, AgentCodingGlyph } from '@/components/ui/agent-glyph'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionConversation } from './coding-session-conversation'
import { notifyCodingSessionChanged } from './coding-session-events'
import { CODING_PHASE_LABELS, codingModelLabel, codingSessionCapabilities, codingSessionStalled, formatCodingCost } from './coding-session-model'
import { CodingChanges, CodingPhaseProgress, CodingPullRequests, CodingSandboxInfo, CodingStateBadge } from './coding-session-parts'
import { useCodingSession } from './use-coding-sessions'
import { CodingSessionArtifacts } from './artifacts/coding-session-artifacts'
import { CodingDiffViewer } from './coding-session-diff'
import { codingSessionSubscribed, codingSessionURL } from './coding-session-page-model'
import './coding-sessions.css'

export type CodingSessionPanelProps = {
  data: BootstrapData
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
export function CodingAgentAvatar({ state, stalled }: { state: CodingSession['state']; /** Failed, or the agent never answered: the red dot. */ stalled?: boolean }) {
  return <span className="coding-agent-avatar" data-state={state} data-stalled={stalled || undefined} aria-hidden="true"><AgentAvatarMark/><span className="coding-agent-avatar-dot"/></span>
}

/**
 * The coding session view, laid out like the reference app's session page: the changes (PRs, diff, commits, sandbox,
 * screenshots) on the left, the agent panel on the right (header with the session menu, then the conversation: the
 * activity stream, the plan pill and the composer with Stop / Queue and the "Input needed" frame).
 */
export function CodingSessionPanel({ session, data, onChange, onDismiss, onOpenSession, agentHidden, className }: CodingSessionPanelProps & { session: CodingSession; onChange: (session: CodingSession) => void }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [error, setError] = useState('')
  const changes = useRef<HTMLDivElement>(null)
  const can = codingSessionCapabilities(session)
  const model = codingModelLabel(session)
  const phase = CODING_PHASE_LABELS[session.phase]
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
  const openChanges = () => changes.current?.querySelector('.coding-diff, .coding-session-empty')?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })

  const title = session.displayTitle || data.issues.find(issue => issue.id === session.issueId)?.title || session.repository || t('Coding session')
  return <section className={`coding-session-panel${agentHidden ? ' is-agent-hidden' : ''}${className ? ` ${className}` : ''}`} data-state={session.state} data-phase={session.phase} aria-label={t('Coding session')}>
    <div className="coding-session-changes" ref={changes}>
      <div className="coding-session-overview">
        <CodingStateBadge state={session.state} session={session}/>
        {phase && session.state !== 'error' && <span className="coding-session-phase">{t(phase)}</span>}
        <span className="coding-session-meta">
          <span className="coding-session-model" data-i18n-ignore>{model.harness} · {model.model}</span>
          <span className="coding-session-cost" aria-label={t('Cost')}>{formatCodingCost(session.costMicros)}</span>
        </span>
      </div>
      <CodingPhaseProgress session={session}/>
      {session.state === 'stale' && session.stoppedBy && <p className="coding-session-muted">{t('Stopped by {name}').replace('{name}', data.users.find(user => user.id === session.stoppedBy)?.displayName || t('a member'))}</p>}
      {session.summary && session.state === 'complete' && <p className="coding-session-summary" data-i18n-ignore>{session.summary}</p>}
      <CodingPullRequests session={session} reviews={data.reviews ?? []} busy={busy} onFixChecks={(_, review) => void startOther(() => startReviewCodingSession(review.id, { kind: 'fix' }), 'Started a session to fix failing checks')}/>
      <CodingSandboxInfo session={session}/>
      <CodingChanges session={session} commitsOnly/>
      <CodingSessionArtifacts sessionId={session.id} artifacts={session.artifacts} refreshKey={session.updatedAt}/>
      <CodingDiffViewer session={session}/>
    </div>
    <aside className="coding-agent-panel" aria-label={t('Agent')} hidden={agentHidden}>
      <header className="coding-agent-panel-header">
        <CodingAgentAvatar state={session.state} stalled={codingSessionStalled(session)}/>
        <h3 data-i18n-ignore>{title}</h3>
        <CodingSessionMenu session={session} data={data} busy={busy} onChange={onChange} items={can.stop || can.restartWithDefaultModel ? <>
          {can.stop && <DropdownMenuItem disabled={busy || session.stopRequested} onSelect={() => void run(() => stopCodingSession(session.id))}>{t('Send stop request')}</DropdownMenuItem>}
          {can.restartWithDefaultModel && <DropdownMenuItem disabled={busy} onSelect={() => void run(() => restartCodingSession(session.id, { useDefaultModel: true }), 'Restarted with the default model')}>{t('Start over with {model}').replace('{model}', 'Auto')}</DropdownMenuItem>}
        </> : undefined} onArchive={can.dismiss ? onDismiss : undefined}/>
      </header>
      <CodingSessionConversation session={session} data={data} onChange={onChange} onOpenChanges={openChanges}/>
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
