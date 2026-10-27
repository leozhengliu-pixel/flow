import { useState } from 'react'
import { AlertTriangle, CircleSlash, GitBranch, GitPullRequest, X } from 'lucide-react'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { GridLoader } from '@/components/ui/grid-loader'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { issuePath } from '@/lib/app-routes'
import { useCodingSessionOpener } from './use-coding-session-opener'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { codingErrorInfo, codingSessionCapabilities, codingSessionStalled, codingRunStartedAt, codingStatusLine, formatCodingElapsed, isCodingSessionTerminal, plainSummary, PULL_REQUEST_STATE_LABELS, pullRequestDisplayState, pullRequestNumberLabel, reviewForPullRequest } from './coding-session-model'
import { CodingPullRequestHover } from './coding-pr-hover-card'
import { CodingAgentAvatar, CodingSessionView } from './coding-session-panel'
import { useCodingSession, useNow } from './use-coding-sessions'
import { stopCodingSession } from '@/lib/coding-sessions-api'
import { toast } from '@/lib/toast'
import { AgentStopIcon } from '@/components/agent/agent-icons'
import { notifyCodingSessionChanged } from './coding-session-events'
import './coding-sessions.css'
import './coding-session-stream.css'

const sessionTitle = (session: CodingSession, data: BootstrapData) => session.displayTitle || data.issues.find(issue => issue.id === session.issueId)?.title || session.repository || 'Coding session'

/**
 * The session's status row, like the reference app's session card in issue activity: a status icon (a spinner while the
 * agent works), the label ("Thinking…", "Error"), the detail (error title, summary) on one line, the running timer and a
 * chevron. The whole row opens the session view.
 */
export function CodingSessionStatusRow({ session, data, href, onOpen, surface = 'issue' }: { session: CodingSession; data: BootstrapData; /** The session page; without it the row is a button calling onOpen (the dialog fallback). */ href?: string; onOpen?: () => void; /** `chat`: the agent chat card ("Working for 02:42", "Completed"). */ surface?: 'issue' | 'chat' }) {
  const { t } = useI18n()
  const errorTitle = session.state === 'error' ? t(codingErrorInfo(session.errorCode, { workspace: data.workspace.urlKey, codeHost: session.codeHost }).title) : undefined
  const status = codingStatusLine(session, errorTitle, surface)
  const live = status.live && session.phase !== 'awaitingApproval'
  const now = useNow(live)
  const started = codingRunStartedAt(session)
  const elapsed = live && started && !Number.isNaN(Date.parse(started)) ? formatCodingElapsed(now - Date.parse(started)) : ''
  // The issue row ends with the session's PR ("#1") once one is open; the chat card has its own PR line.
  const pr = surface === 'issue' ? session.latestPullRequest ?? session.pullRequests?.at(-1) : undefined
  const content = <>
    <CodingStatusIcon tone={status.tone}/>
    <span className="coding-session-status-label" data-i18n-ignore={status.repo || status.rawLabel ? true : undefined}>{status.rawLabel ? status.label : t(status.label).replace('{count}', String(status.count ?? '')).replace('{repo}', status.repo ?? '')}</span>
    {status.detail && (status.codeDetail
      ? <code className="coding-session-status-code" data-i18n-ignore title={status.detail}>{status.detail}</code>
      : <span className="coding-session-status-detail" data-i18n-ignore={status.translateDetail ? undefined : true}>{status.translateDetail ? t(status.detail) : plainSummary(status.detail)}</span>)}
    <span className="coding-session-status-end">
      {elapsed && <time aria-label={t('Elapsed time')}>{elapsed}</time>}
      {/* Hovering the chip shows the PR hover card above it, left-aligned (as in the reference app). */}
      {pr && <CodingPullRequestHover pr={pr} review={reviewForPullRequest(pr, data.reviews ?? [])} session={session} side="top" align="start">
        <span className="coding-session-pr-chip" data-state={pullRequestDisplayState(pr)} aria-label={[pr.title, `${pr.repository.split('/').at(-1)}${pullRequestNumberLabel(pr)}`, t(PULL_REQUEST_STATE_LABELS[pullRequestDisplayState(pr)])].filter(Boolean).join(', ')}>
          <GitPullRequest size={14} aria-hidden="true"/><span data-i18n-ignore>{pullRequestNumberLabel(pr)}</span>
        </span>
      </CodingPullRequestHover>}
      <FlowGlyph name="rowChevron" size={16} className="coding-session-status-chevron"/>
    </span>
    <span className="sr-only">{t('Show session')}</span>
  </>
  return href
    ? <AppLink className="coding-session-status-row" data-tone={status.tone} href={href}>{content}</AppLink>
    : <button type="button" className="coding-session-status-row" data-tone={status.tone} onClick={onOpen}>{content}</button>
}

function CodingStatusIcon({ tone }: { tone: ReturnType<typeof codingStatusLine>['tone'] }) {
  // The reference app's dotted-grid sprite: animated while the agent works, the static "read" frame in purple when it needs input.
  if (tone === 'working') return <GridLoader variant="agent" size={16} className="coding-session-status-icon is-working"/>
  if (tone === 'input') return <GridLoader variant="read" size={16} className="coding-session-status-icon is-input"/>
  if (tone === 'error') return <AlertTriangle size={16} aria-hidden="true" className="coding-session-status-icon"/>
  // Finished: the static plus of the dotted grid (lit dots only).
  if (tone === 'done') return <GridLoader variant="unread" size={16} dimColor="transparent" className="coding-session-status-icon is-done"/>
  return <CircleSlash size={16} aria-hidden="true" className="coding-session-status-icon"/>
}

/**
 * The session card shown in agent chat (and anywhere a session is referenced), like the reference app's session card in
 * issue activity: "<Agent> started by <member> <time>" over the status row; the row opens the session view.
 */
export function CodingSessionCard({ sessionId, initial, data, onDismiss }: { sessionId: string; initial?: CodingSession; data: BootstrapData; onDismiss?: () => void }) {
  const { t, locale } = useI18n()
  const clock = useCurrentTimeMs()
  const { session, error, apply } = useCodingSession(sessionId, initial)
  const opener = useCodingSessionOpener(session, data, onDismiss)
  const [stopping, setStopping] = useState(false)
  const stop = async () => {
    if (!session || stopping) return
    setStopping(true)
    try {
      apply(await stopCodingSession(session.id))
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : t('Could not stop the session')) }
    finally { setStopping(false) }
  }
  if (!session) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : <div className="coding-session-card is-loading" role="status">{t('Loading coding session…')}</div>
  const issue = session.issueId ? data.issues.find(item => item.id === session.issueId) : undefined
  const creator = data.users.find(user => user.id === session.creatorId)
  const created = Number.isNaN(Date.parse(session.createdAt)) ? '' : activityTimeLabel(session.createdAt, clock, locale).replace(/m(?= ago$)/, 'min')
  return <article className="coding-session-card" data-state={session.state} aria-label={t('Coding session')}>
    <header className="coding-session-card-header">
      <CodingAgentAvatar state={session.state} stalled={codingSessionStalled(session)}/>
      <span className="coding-session-card-byline">
        <strong>{t('Flow')}</strong>
        {creator && <><span>{t('started by')}</span><strong data-i18n-ignore>{creator.displayName || creator.name}</strong></>}
        {created && <time dateTime={session.createdAt}>{created}</time>}
      </span>
      <span className="coding-session-card-subject" data-i18n-ignore>{sessionTitle(session, data)}</span>
      {issue && <AppLink className="coding-session-link" href={`${issuePath(data.workspace.urlKey, issue)}#agent-session-${session.agentSessionId}`}>{t('Open issue')}</AppLink>}
      {onDismiss && isCodingSessionTerminal(session) && <button type="button" className="coding-session-icon-button" aria-label={t('Dismiss')} onClick={onDismiss}><X size={14}/></button>}
    </header>
    <div className="coding-session-card-status">
      <CodingSessionStatusRow session={session} data={data} surface="chat" {...opener.rowProps}/>
      {codingSessionCapabilities(session).stop && <button type="button" className="cs-button is-small coding-session-card-stop" aria-label={t('Send stop request')} title={session.stopRequested ? t('Stop request already sent, waiting for {agent}').replace('{agent}', t('Flow')) : t('Send stop request')} disabled={stopping || session.stopRequested}
        onClick={() => void stop()}><AgentStopIcon size={14}/></button>}
    </div>
    {opener.dialog}
  </article>
}

/**
 * The full session view in a dialog (the fallback where no app router is mounted; entry points open the session page),
 * laid out like the reference app's session page: a 44px bar with the issue and
 * session title and the "Hide agent" toggle over the changes and the agent panel. Actions that start another session
 * switch the dialog to it.
 */
export function CodingSessionDialog({ sessionId, initial, data, open, onOpenChange, onDismiss }: { sessionId: string; initial?: CodingSession; data: BootstrapData; open: boolean; onOpenChange: (open: boolean) => void; onDismiss?: () => void }) {
  const { t } = useI18n()
  // Mounted while open; "Fix with agent" and similar actions switch it to the session they started.
  const [shown, setShown] = useState<{ id: string; initial?: CodingSession }>({ id: sessionId, initial })
  const [agentHidden, setAgentHidden] = useState(false)
  const { session } = useCodingSession(shown.id, shown.initial)
  const issue = session?.issueId ? data.issues.find(item => item.id === session.issueId) : undefined
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="coding-session-dialog" aria-describedby={undefined} closeLabel={t('Close')}>
      <header className="coding-session-dialog-bar">
        {issue && <AppLink className="coding-session-dialog-crumb" href={issuePath(data.workspace.urlKey, issue)}><GitBranch size={14} aria-hidden="true"/><span data-i18n-ignore>{issue.identifier}</span></AppLink>}
        {issue && <span className="coding-session-dialog-separator" aria-hidden="true">›</span>}
        <DialogTitle className="coding-session-dialog-title"><span className="sr-only">{t('Coding session')}</span><span aria-hidden="true" data-i18n-ignore>{session ? sessionTitle(session, data) : ''}</span></DialogTitle>
        <button type="button" className="coding-session-agent-toggle" aria-pressed={!agentHidden} aria-label={t(agentHidden ? 'Show agent' : 'Hide agent')} onClick={() => setAgentHidden(value => !value)}><AgentCursorGlyph size={14}/></button>
      </header>
      <CodingSessionView key={shown.id} sessionId={shown.id} initial={shown.initial} data={data} agentHidden={agentHidden} onDismiss={onDismiss ? () => { onDismiss(); onOpenChange(false) } : undefined} onOpenSession={next => setShown({ id: next.id, initial: next })}/>
    </DialogContent>
  </Dialog>
}
