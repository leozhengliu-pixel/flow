import { useRef, useState, type ReactNode } from 'react'
import { GitBranch, RotateCcw } from 'lucide-react'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { restartCodingSession, setCodingSessionSubscription, startReviewCodingSession, stopCodingSession } from '@/lib/coding-sessions-api'
import { AgentAvatarMark, AgentCodingGlyph } from '@/components/ui/agent-glyph'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionConversation } from './coding-session-conversation'
import { notifyCodingSessionChanged } from './coding-session-events'
import { codingModelLabel, codingSessionCapabilities, codingSessionStalled } from './coding-session-model'
import { CodingChanges, CodingPullRequests } from './coding-session-parts'
import { useCodingModelLabel, useCodingSession } from './use-coding-sessions'
import { CodingSessionArtifacts } from './artifacts/coding-session-artifacts'
import { CodingDiffViewer } from './coding-session-diff'
import { CodingSessionErrorBoundary } from './coding-session-error-boundary'
import { codingSessionHref, codingSessionSubscribed } from './coding-session-page-model'
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
  return <CodingSessionErrorBoundary><CodingSessionPanel session={session} onChange={apply} {...props}/></CodingSessionErrorBoundary>
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

  return <section className={`coding-session-panel${agentHidden ? ' is-agent-hidden' : ''}${className ? ` ${className}` : ''}`} data-state={session.state} data-phase={session.phase} aria-label={t('Coding session')}>
    <div className="coding-session-changes" ref={changes}>
      {session.state === 'stale' && session.stoppedBy && <p className="coding-session-muted">{t('Stopped by {name}').replace('{name}', data.users.find(user => user.id === session.stoppedBy)?.displayName || t('a member'))}</p>}
      <CodingPullRequests session={session} reviews={data.reviews ?? []} busy={busy} onFixChecks={(_, review) => void startOther(() => startReviewCodingSession(review.id, { kind: 'fix' }), 'Started a session to fix failing checks')}/>
      <CodingChanges session={session} commitsOnly/>
      {/* Plain screenshots show in the conversation, under the turn that took them; pairs and recordings stay here. */}
      <CodingSessionArtifacts sessionId={session.id} artifacts={session.artifacts} refreshKey={session.updatedAt} withScreenshots={false}/>
      <CodingDiffViewer session={session}/>
    </div>
    <CodingAgentPanel session={session} data={data} onChange={onChange} onDismiss={onDismiss} onOpenChanges={openChanges} hidden={agentHidden} error={error}/>
  </section>
}

/**
 * The agent panel: the header (session title, Session details, and "Close chat" where the host can close it) over the
 * conversation and the composer. Mounted beside the session's changes, and on the review page of the session's pull
 * request (`/review/<slug>/changes?showAgent=true&agentSessionId=<id>`).
 */
export function CodingAgentPanel({ session, data, onChange, onDismiss, onOpenChanges, onClose, hidden, error: hostError, className }: { session: CodingSession; data: BootstrapData; onChange: (session: CodingSession) => void; onDismiss?: () => void; /** Shows the session's changes (the end-of-turn diff chip). */ onOpenChanges?: () => void; /** "Close chat" (the review page). */ onClose?: () => void; hidden?: boolean; /** An error of the host's own actions, shown under the conversation. */ error?: string; className?: string }) {
  const { t } = useI18n()
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [error, setError] = useState('')
  const can = codingSessionCapabilities(session)
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
  const title = session.displayTitle || data.issues.find(issue => issue.id === session.issueId)?.title || session.repository || t('Coding session')
  const shownError = error || hostError
  return <aside className={`coding-agent-panel${className ? ` ${className}` : ''}`} aria-label={t('Agent')} hidden={hidden}>
      <header className="coding-agent-panel-header">
        <div className="coding-agent-panel-header-inner">
          <CodingAgentAvatar state={session.state} stalled={codingSessionStalled(session)}/>
          <h3 data-i18n-ignore>{title}</h3>
          {session.archivedAt && <span className="coding-agent-archived">{t('Archived')}</span>}
          <CodingSessionMenu session={session} data={data} busy={busy} onChange={onChange} items={can.stop || can.restartWithDefaultModel ? <>
          {can.stop && <DropdownMenuItem disabled={busy || session.stopRequested} onSelect={() => void run(() => stopCodingSession(session.id))}>{t('Send stop request')}</DropdownMenuItem>}
          {can.restartWithDefaultModel && <DropdownMenuItem disabled={busy} onSelect={() => void run(() => restartCodingSession(session.id, { useDefaultModel: true }), 'Restarted with the default model')}><RotateCcw size={14} aria-hidden="true"/>{t('Start over with {model}').replace('{model}', 'Auto')}</DropdownMenuItem>}
          </> : undefined} onStartOver={can.startOver ? model => void run(() => restartCodingSession(session.id, { model }), 'Started over') : undefined} onArchive={can.dismiss ? onDismiss : undefined}/>
        </div>
        {onClose && <ScopedFlowTooltip label={t('Close')} shortcut="Esc" align="end"><button type="button" className="coding-session-icon-button coding-agent-close" aria-label={t('Close chat')} onClick={onClose}><FlowGlyph name="closeChat" size={14}/></button></ScopedFlowTooltip>}
      </header>
      <CodingSessionConversation session={session} data={data} onChange={onChange} onOpenChanges={onOpenChanges}/>
      {shownError && <p className="coding-session-error-text" role="alert">{shownError}</p>}
    </aside>
}

/**
 * "…" Session details, like the reference app's: the model with Auto's routing reason, "Subscribe to session", Copy,
 * the session actions ("Start over with <next model up>") and Archive.
 */
export function CodingSessionMenu({ session, data, busy, items, onArchive, onStartOver, onChange }: { session: CodingSession; data: BootstrapData; busy?: boolean; items?: ReactNode; onArchive?: () => void; /** "Start over with <model>"; shown when the session offers a more capable model. */ onStartOver?: (model: string) => void; /** Receives the session after a subscription change. */ onChange?: (session: CodingSession) => void }) {
  const { t } = useI18n()
  const model = codingModelLabel(session)
  // Catalog labels load when the menu first opens.
  const [opened, setOpened] = useState(false)
  const routedLabel = useCodingModelLabel(opened ? session.model : undefined, session.harness)
  const escalationLabel = useCodingModelLabel(opened && onStartOver ? session.escalationModel : undefined, session.harness)
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
  const startOver = onStartOver && session.escalationModel ? onStartOver : undefined
  return <DropdownMenu onOpenChange={open => { if (open) setOpened(true) }}>
    <ScopedFlowTooltip label={t('Session details')} align="end">
      <DropdownMenuTrigger asChild>
        <button type="button" className="coding-session-icon-button coding-agent-menu-trigger" aria-label={t('Session details')} disabled={busy}><FlowGlyph name="ellipsis" size={14}/></button>
      </DropdownMenuTrigger>
    </ScopedFlowTooltip>
    <DropdownMenuContent align="end" className="coding-session-menu">
      <DropdownMenuLabel>{t('Session details')}</DropdownMenuLabel>
      <DropdownMenuItem disabled className="coding-session-menu-detail coding-session-menu-model">
        <AgentCodingGlyph size={16}/>
        <span className="coding-session-menu-model-text">
          <span data-i18n-ignore>{routedLabel || model.model}</span>
          <span className="coding-session-menu-model-reason" data-i18n-ignore>{session.modelReason || model.harness}</span>
        </span>
      </DropdownMenuItem>
      {(session.repository || session.branchName) && <DropdownMenuItem disabled className="coding-session-menu-detail">
        <GitBranch size={16} aria-hidden="true"/>
        <span className="coding-session-menu-model-text">
          {session.repository && <span data-i18n-ignore>{session.repository}</span>}
          {session.branchName && <span className="coding-session-menu-model-reason" data-i18n-ignore>{session.branchName}{session.baseRef ? ` → ${session.baseRef}` : ''}</span>}
        </span>
      </DropdownMenuItem>}
      <DropdownMenuSeparator/>
      <DropdownMenuItem role="menuitemcheckbox" aria-checked={subscribed} disabled={subscribing} className="coding-session-menu-subscribe" onSelect={event => { event.preventDefault(); void toggleSubscription() }}>
        <span className="coding-session-menu-grow">{t('Subscribe to session')}</span>
        <span className="coding-session-menu-toggle" data-checked={subscribed || undefined} aria-hidden="true"/>
      </DropdownMenuItem>
      <DropdownMenuSeparator/>
      <DropdownMenuSub>
        <DropdownMenuSubTrigger><FlowGlyph name="copy" size={16}/>{t('Copy')}</DropdownMenuSubTrigger>
        <DropdownMenuSubContent className="coding-session-menu is-submenu">
          <DropdownMenuItem onSelect={() => void copy(`${location.origin}${codingSessionHref(session, data)}`, 'Link copied to clipboard')}>{t('Session URL')}</DropdownMenuItem>
        </DropdownMenuSubContent>
      </DropdownMenuSub>
      {(items || startOver || onArchive) && <DropdownMenuSeparator/>}
      {items}
      {startOver && <DropdownMenuItem disabled={busy} onSelect={() => startOver(session.escalationModel!)}><RotateCcw size={14} aria-hidden="true"/>{t('Start over with {model}').replace('{model}', escalationLabel)}</DropdownMenuItem>}
      {onArchive && <DropdownMenuItem onSelect={onArchive}><FlowGlyph name="agentSessionDismissed" size={16}/>{t('Archive')}</DropdownMenuItem>}
    </DropdownMenuContent>
  </DropdownMenu>
}
