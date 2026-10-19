import { useState } from 'react'
import { ChevronRight, Code2, GitBranch, X } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { issuePath } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CODING_PHASE_LABELS, isCodingSessionTerminal, PULL_REQUEST_STATE_LABELS, pullRequestDisplayState, pullRequestNumberLabel } from './coding-session-model'
import { CodingStateBadge } from './coding-session-parts'
import { CodingSessionPanel, CodingSessionView } from './coding-session-panel'
import { useCodingSession, useCodingSessions, useDismissedCodingSessions } from './use-coding-sessions'
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
 * The session card shown in agent chat (and anywhere a session is referenced): a live summary that expands into the
 * full session view in place. Issue sessions also link to their issue.
 */
export function CodingSessionCard({ sessionId, initial, data, onDismiss }: { sessionId: string; initial?: CodingSession; data: BootstrapData; onDismiss?: () => void }) {
  const { t } = useI18n()
  const { session, error, apply } = useCodingSession(sessionId, initial)
  const [open, setOpen] = useState(false)
  const [other, setOther] = useState<CodingSession>()
  if (!session) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : <div className="coding-session-card is-loading" role="status">{t('Loading coding session…')}</div>
  const issue = session.issueId ? data.issues.find(item => item.id === session.issueId) : undefined
  return <article className="coding-session-card" data-state={session.state} aria-label={t('Coding session')}>
    <div className="coding-session-card-row">
      <Code2 size={14} aria-hidden="true" className="coding-session-card-icon"/>
      <button type="button" className="coding-session-card-toggle" aria-expanded={open} onClick={() => setOpen(value => !value)}>
        <CodingSessionSummary session={session} data={data}/>
        <ChevronRight size={14} aria-hidden="true" data-open={open || undefined}/>
        <span className="sr-only">{t(open ? 'Hide session' : 'Show session')}</span>
      </button>
      {issue && <AppLink className="coding-session-link" href={`${issuePath(data.workspace.urlKey, issue)}#agent-session-${session.agentSessionId}`}>{t('Open issue')}</AppLink>}
      {onDismiss && isCodingSessionTerminal(session) && <button type="button" className="coding-session-icon-button" aria-label={t('Dismiss')} onClick={onDismiss}><X size={13}/></button>}
    </div>
    {open && <CodingSessionPanel session={session} data={data} onChange={apply} onOpenSession={setOther}/>}
    {other && <CodingSessionDialog sessionId={other.id} initial={other} data={data} open onOpenChange={value => { if (!value) setOther(undefined) }}/>}
  </article>
}

/** The full session view in a dialog (from lists and from actions that start another session). */
export function CodingSessionDialog({ sessionId, initial, data, open, onOpenChange, onDismiss }: { sessionId: string; initial?: CodingSession; data: BootstrapData; open: boolean; onOpenChange: (open: boolean) => void; onDismiss?: () => void }) {
  const { t } = useI18n()
  // Mounted while open; "Fix with agent" and similar actions switch it to the session they started.
  const [shown, setShown] = useState<{ id: string; initial?: CodingSession }>({ id: sessionId, initial })
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="coding-session-dialog" aria-describedby={undefined} closeLabel={t('Close')}>
      <DialogTitle className="coding-session-dialog-title">{t('Coding session')}</DialogTitle>
      <CodingSessionView key={shown.id} sessionId={shown.id} initial={shown.initial} data={data} onDismiss={onDismiss ? () => { onDismiss(); onOpenChange(false) } : undefined} onOpenSession={next => setShown({ id: next.id, initial: next })}/>
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
        <button type="button" className="coding-sessions-list-row" onClick={() => setOpen(session)}>
          <CodingSessionSummary session={session} data={data}/>
          <time dateTime={session.updatedAt}>{time(session.updatedAt)}</time>
        </button>
        {isCodingSessionTerminal(session) && <button type="button" className="coding-session-icon-button" aria-label={`${t('Dismiss')} ${sessionTitle(session, data)}`} onClick={() => dismiss(session.id)}><X size={13}/></button>}
      </li>)}
    </ul>
    {open && <CodingSessionDialog sessionId={open.id} initial={open} data={data} open onOpenChange={value => { if (!value) setOpen(undefined) }} onDismiss={() => dismiss(open.id)}/>}
  </section>
}
