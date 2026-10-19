import { useState } from 'react'
import { useInRouterContext } from 'react-router-dom'
import { AlertTriangle, CheckCircle2, ChevronRight, CircleDot, CircleSlash, GitBranch, X } from 'lucide-react'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { codingSessionPath, issuePath } from '@/lib/app-routes'
import { useCodingSessionOpener } from './use-coding-session-opener'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CODING_PHASE_LABELS, codingErrorInfo, codingRunStartedAt, codingStatusLine, formatCodingElapsed, isCodingSessionTerminal, PULL_REQUEST_STATE_LABELS, pullRequestDisplayState, pullRequestNumberLabel } from './coding-session-model'
import { CodingStateBadge } from './coding-session-parts'
import { CodingAgentAvatar, CodingSessionView } from './coding-session-panel'
import { useCodingSession, useCodingSessions, useDismissedCodingSessions, useNow } from './use-coding-sessions'
import './coding-sessions.css'

const sessionTitle = (session: CodingSession, data: BootstrapData) => session.displayTitle || data.issues.find(issue => issue.id === session.issueId)?.title || session.repository || 'Coding session'

/** One line describing a session: state, title, repository and branch, PR state, current phase. */
function CodingSessionSummary({ session, data }: { session: CodingSession; data: BootstrapData }) {
  const { t } = useI18n()
  const pr = session.latestPullRequest ?? session.pullRequests?.at(-1)
  const phase = CODING_PHASE_LABELS[session.phase]
  return <span className="coding-session-summary-line">
    <CodingStateBadge state={session.state}/>
    <span className="coding-session-card-title" data-i18n-ignore>{sessionTitle(session, data)}</span>
    {(session.repository || session.branchName) && <span className="coding-session-card-repo" data-i18n-ignore><GitBranch size={12} aria-hidden="true"/>{[session.repository, session.branchName].filter(Boolean).join(' · ')}</span>}
    {pr && <span className="coding-pr-state" data-state={pullRequestDisplayState(pr)}><span data-i18n-ignore>{pullRequestNumberLabel(pr)}</span> {t(PULL_REQUEST_STATE_LABELS[pullRequestDisplayState(pr)])}</span>}
    {phase && !isCodingSessionTerminal(session) && <span className="coding-session-phase">{t(phase)}</span>}
  </span>
}

/**
 * The session's status row, like the reference app's session card in issue activity: a status icon (a spinner while the
 * agent works), the label ("Thinking…", "Error"), the detail (error title, summary) on one line, the running timer and a
 * chevron. The whole row opens the session view.
 */
export function CodingSessionStatusRow({ session, data, href, onOpen }: { session: CodingSession; data: BootstrapData; /** The session page; without it the row is a button calling onOpen (the dialog fallback). */ href?: string; onOpen?: () => void }) {
  const { t } = useI18n()
  const errorTitle = session.state === 'error' ? t(codingErrorInfo(session.errorCode, { workspace: data.workspace.urlKey, codeHost: session.codeHost }).title) : undefined
  const status = codingStatusLine(session, errorTitle)
  const live = status.live && session.phase !== 'awaitingApproval'
  const now = useNow(live)
  const started = codingRunStartedAt(session)
  const elapsed = live && started && !Number.isNaN(Date.parse(started)) ? formatCodingElapsed(now - Date.parse(started)) : ''
  const content = <>
    <CodingStatusIcon tone={status.tone}/>
    <span className="coding-session-status-label">{t(status.label)}{status.ellipsis ? '…' : ''}</span>
    {status.detail && <span className="coding-session-status-detail" data-i18n-ignore>{status.detail}</span>}
    <span className="coding-session-status-end">
      {elapsed && <time aria-label={t('Elapsed time')}>{elapsed}</time>}
      <ChevronRight size={16} aria-hidden="true"/>
    </span>
    <span className="sr-only">{t('Show session')}</span>
  </>
  return href
    ? <AppLink className="coding-session-status-row" data-tone={status.tone} href={href}>{content}</AppLink>
    : <button type="button" className="coding-session-status-row" data-tone={status.tone} onClick={onOpen}>{content}</button>
}

function CodingStatusIcon({ tone }: { tone: ReturnType<typeof codingStatusLine>['tone'] }) {
  if (tone === 'working') return <span className="coding-agent-spinner" aria-hidden="true"/>
  if (tone === 'error') return <AlertTriangle size={16} aria-hidden="true" className="coding-session-status-icon"/>
  if (tone === 'done') return <CheckCircle2 size={16} aria-hidden="true" className="coding-session-status-icon"/>
  if (tone === 'input') return <CircleDot size={16} aria-hidden="true" className="coding-session-status-icon"/>
  return <CircleSlash size={16} aria-hidden="true" className="coding-session-status-icon"/>
}

/**
 * The session card shown in agent chat (and anywhere a session is referenced), like the reference app's session card in
 * issue activity: "<Agent> started by <member> <time>" over the status row; the row opens the session view.
 */
export function CodingSessionCard({ sessionId, initial, data, onDismiss }: { sessionId: string; initial?: CodingSession; data: BootstrapData; onDismiss?: () => void }) {
  const { t, locale } = useI18n()
  const { session, error } = useCodingSession(sessionId, initial)
  const opener = useCodingSessionOpener(session, data, onDismiss)
  if (!session) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : <div className="coding-session-card is-loading" role="status">{t('Loading coding session…')}</div>
  const issue = session.issueId ? data.issues.find(item => item.id === session.issueId) : undefined
  const creator = data.users.find(user => user.id === session.creatorId)
  const created = Number.isNaN(Date.parse(session.createdAt)) ? '' : activityTimeLabel(session.createdAt, Date.now(), locale).replace(/m(?= ago$)/, 'min')
  return <article className="coding-session-card" data-state={session.state} aria-label={t('Coding session')}>
    <header className="coding-session-card-header">
      <CodingAgentAvatar state={session.state}/>
      <span className="coding-session-card-byline">
        <strong>{t('Flow')}</strong>
        {creator && <><span>{t('started by')}</span><strong data-i18n-ignore>{creator.displayName || creator.name}</strong></>}
        {created && <time dateTime={session.createdAt}>{created}</time>}
      </span>
      <span className="coding-session-card-subject" data-i18n-ignore>{sessionTitle(session, data)}</span>
      {issue && <AppLink className="coding-session-link" href={`${issuePath(data.workspace.urlKey, issue)}#agent-session-${session.agentSessionId}`}>{t('Open issue')}</AppLink>}
      {onDismiss && isCodingSessionTerminal(session) && <button type="button" className="coding-session-icon-button" aria-label={t('Dismiss')} onClick={onDismiss}><X size={14}/></button>}
    </header>
    <CodingSessionStatusRow session={session} data={data} {...opener.rowProps}/>
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

/**
 * Coding sessions listed where Flow lists agent work (the Agent page, an issue): newest first, each opening the full
 * view; finished ones can be dismissed from the list.
 */
export function CodingSessionsList({ data, issueId, creatorId, title = 'Coding sessions', limit = 20 }: { data: BootstrapData; issueId?: string; creatorId?: string; title?: string; limit?: number }) {
  const { t, locale } = useI18n()
  const { sessions, error } = useCodingSessions({ issueId, creatorId })
  const { dismissed, dismiss } = useDismissedCodingSessions(data.workspace.urlKey, data.viewer.id)
  const [open, setOpen] = useState<CodingSession>()
  const routed = useInRouterContext()
  const shown = sessions
    .filter(session => !dismissed.has(session.id))
    .sort((a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0))
    .slice(0, limit)
  if (!shown.length) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : null
  const time = (value: string) => {
    const date = new Date(value)
    return Number.isNaN(date.getTime()) ? '' : date.toLocaleDateString(locale, { month: 'short', day: 'numeric' })
  }
  return <section className="coding-sessions-list" aria-label={t(title)}>
    <h3>{t(title)}</h3>
    <ul>
      {shown.map(session => <li key={session.id} data-state={session.state}>
        {routed
          ? <AppLink className="coding-sessions-list-row" href={codingSessionPath(data.workspace.urlKey, session.id)}>
            <CodingSessionSummary session={session} data={data}/>
            <time dateTime={session.updatedAt}>{time(session.updatedAt)}</time>
          </AppLink>
          : <button type="button" className="coding-sessions-list-row" onClick={() => setOpen(session)}>
            <CodingSessionSummary session={session} data={data}/>
            <time dateTime={session.updatedAt}>{time(session.updatedAt)}</time>
          </button>}
        {isCodingSessionTerminal(session) && <button type="button" className="coding-session-icon-button" aria-label={`${t('Dismiss')} ${sessionTitle(session, data)}`} onClick={() => dismiss(session.id)}><X size={13}/></button>}
      </li>)}
    </ul>
    {open && <CodingSessionDialog sessionId={open.id} initial={open} data={data} open onOpenChange={value => { if (!value) setOpen(undefined) }} onDismiss={() => dismiss(open.id)}/>}
  </section>
}
