import { useEffect, useState, type ComponentType } from 'react'
import { CheckCircle2, CircleDashed, CircleDot, ExternalLink, FileText, GitBranch, GitCommit, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft, Loader2, Server, XCircle } from 'lucide-react'
import { fetchCodingSandboxLogs } from '@/lib/coding-sessions-api'
import { useI18n } from '@/i18n/i18n'
import type { CodeReview } from '@/types/flow'
import type { CodingSandbox, CodingSession, CodingSessionState, SessionPullRequest } from '@/types/coding-sessions'
import {
  CI_STATUS_LABELS,
  CODING_PHASE_LABELS,
  CODING_PROGRESS_STEPS,
  CODING_STATE_LABELS,
  codingStateLabel,
  ciStatus,
  currentSandbox,
  PULL_REQUEST_STATE_LABELS,
  pullRequestDisplayState,
  pullRequestNumberLabel,
  reviewForPullRequest,
} from './coding-session-model'

/** The state pill with Linear's labels; `data-state` drives its colour. */
export function CodingStateBadge({ state, session }: { state: CodingSessionState; /** Names a stale session's reason (stopped, never started, stopped responding). */ session?: Pick<CodingSession, 'state' | 'stoppedBy' | 'startedAt'> }) {
  const { t } = useI18n()
  const Icon = state === 'complete' ? CheckCircle2 : state === 'error' ? XCircle : state === 'awaitingInput' ? CircleDot : state === 'stale' ? CircleDashed : Loader2
  return <span className="coding-state-badge" data-state={state}><Icon size={12} aria-hidden="true" className={state === 'pending' || state === 'active' || state === 'stopping' ? 'coding-spin' : undefined}/>{t(session ? codingStateLabel(session) : CODING_STATE_LABELS[state] ?? state)}</span>
}

/** Queued → … → Opening pull request, with the current phase highlighted (only while the session starts or works). */
export function CodingPhaseProgress({ session }: { session: Pick<CodingSession, 'phase' | 'state'> }) {
  const { t } = useI18n()
  const index = CODING_PROGRESS_STEPS.indexOf(session.phase)
  if (index < 0 || (session.state !== 'pending' && session.state !== 'active')) return null
  return <ol className="coding-phase-progress" aria-label={t('Session progress')}>
    {CODING_PROGRESS_STEPS.map((phase, position) => <li key={phase} data-status={position < index ? 'done' : position === index ? 'current' : 'todo'} aria-current={position === index ? 'step' : undefined}>{t(CODING_PHASE_LABELS[phase])}</li>)}
  </ol>
}

const PR_ICONS: Record<ReturnType<typeof pullRequestDisplayState>, ComponentType<{ size?: number; 'aria-hidden'?: boolean }>> = { draft: GitPullRequestDraft, open: GitPullRequest, merged: GitMerge, closed: GitPullRequestClosed }

/** PR/MR links with their state (draft, open, merged, closed) and the synced review's CI status. */
export function CodingPullRequests({ session, reviews, onFixChecks, busy }: { session: CodingSession; reviews: readonly CodeReview[]; onFixChecks?: (pr: SessionPullRequest, review: CodeReview) => void; busy?: boolean }) {
  const { t } = useI18n()
  const list = session.pullRequests?.length ? session.pullRequests : session.latestPullRequest ? [session.latestPullRequest] : []
  if (!list.length) return null
  return <ul className="coding-pull-requests" aria-label={t('Pull requests')}>
    {list.map(pr => {
      const state = pullRequestDisplayState(pr)
      const Icon = PR_ICONS[state]
      const review = reviewForPullRequest(pr, reviews)
      const ci = ciStatus(review)
      const noun = pr.codeHost === 'gitlab' ? 'merge request' : 'pull request'
      return <li key={`${pr.codeHost}:${pr.repository}:${pr.number}`} data-state={state}>
        <Icon size={14} aria-hidden/>
        <a href={/^https?:\/\//i.test(pr.url) ? pr.url : undefined} target="_blank" rel="noreferrer" aria-label={`${t(`Open ${noun}`)} ${pullRequestNumberLabel(pr)}`}>
          <span className="coding-pr-number" data-i18n-ignore>{pr.repository} {pullRequestNumberLabel(pr)}</span>
          {pr.title && <span className="coding-pr-title" data-i18n-ignore>{pr.title}</span>}
          <ExternalLink size={12} aria-hidden="true"/>
        </a>
        <span className="coding-pr-state" data-state={state}>{t(PULL_REQUEST_STATE_LABELS[state])}</span>
        {ci !== 'none' && <span className="coding-pr-ci" data-ci={ci}>{t(CI_STATUS_LABELS[ci])}</span>}
        {ci === 'failed' && review && onFixChecks && state !== 'merged' && state !== 'closed' && <button type="button" className="coding-session-button" disabled={busy} onClick={() => onFixChecks(pr, review)}>{t('Fix failing checks')}</button>}
      </li>
    })}
  </ul>
}

/** Repository, branch, base ref, size, region and logs of the current sandbox. */
export function CodingSandboxInfo({ session }: { session: CodingSession }) {
  const { t } = useI18n()
  const sandbox = currentSandbox(session)
  const repository = sandbox?.repository || session.repository
  const branch = sandbox?.branchName || session.branchName
  const baseRef = sandbox?.baseRef || session.baseRef
  if (!repository && !branch && !sandbox) return null
  return <section className="coding-sandbox" aria-label={t('Sandbox')}>
    <dl>
      {repository && <><dt>{t('Repository')}</dt><dd data-i18n-ignore>{repository}</dd></>}
      {branch && <><dt>{t('Branch')}</dt><dd data-i18n-ignore><GitBranch size={12} aria-hidden="true"/>{branch}</dd></>}
      {baseRef && <><dt>{t('Base')}</dt><dd data-i18n-ignore>{baseRef}</dd></>}
      {sandbox?.size && <><dt>{t('Size')}</dt><dd>{t(sandbox.size === 'small' ? 'Small' : sandbox.size === 'large' ? 'Large' : 'Medium')}</dd></>}
      {(sandbox?.region || session.region) && <><dt>{t('Region')}</dt><dd data-i18n-ignore>{sandbox?.region || session.region}</dd></>}
      {sandbox && <><dt>{t('Sandbox')}</dt><dd><Server size={12} aria-hidden="true"/>{t(SANDBOX_STATUS_LABELS[sandbox.status] ?? sandbox.status)}</dd></>}
    </dl>
    {sandbox && <CodingSandboxLogs sessionId={session.id} sandbox={sandbox}/>}
  </section>
}

const SANDBOX_STATUS_LABELS: Record<CodingSandbox['status'], string> = { requested: 'Requested', creating: 'Creating', running: 'Running', stopping: 'Stopping', stopped: 'Stopped', failed: 'Failed', lost: 'Lost' }

/** "View logs": the sandbox's live or archived logs, loaded on demand. An external `logsUrl` opens directly. */
export function CodingSandboxLogs({ sessionId, sandbox, defaultOpen = false }: { sessionId: string; sandbox: CodingSandbox; defaultOpen?: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(defaultOpen)
  const [logs, setLogs] = useState<{ text: string; complete: boolean }>()
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const load = async () => {
    setLoading(true); setError('')
    try {
      const result = await fetchCodingSandboxLogs(sessionId, sandbox.id)
      setLogs({ text: result.logs, complete: result.complete })
    } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)) }
    finally { setLoading(false) }
  }
  // Opened from the error callout ("View logs"): load at once.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => { if (defaultOpen) void load() }, [])
  const toggle = () => {
    const next = !open
    setOpen(next)
    if (next) void load()
  }
  if (sandbox.logsUrl && /^https?:\/\//i.test(sandbox.logsUrl)) return <a className="coding-session-link" href={sandbox.logsUrl} target="_blank" rel="noreferrer"><FileText size={13} aria-hidden="true"/>{t('View logs')}</a>
  return <div className="coding-sandbox-logs">
    <button type="button" className="coding-session-link" aria-expanded={open} onClick={toggle}><FileText size={13} aria-hidden="true"/>{t(open ? 'Hide logs' : 'View logs')}</button>
    {open && <>
      {loading && !logs && <p className="coding-session-muted" role="status">{t('Loading logs…')}</p>}
      {error && <p className="coding-session-error-text" role="alert">{error}</p>}
      {logs && <pre aria-label={t('Sandbox logs')} data-i18n-ignore>{logs.text || t('No logs yet')}</pre>}
      {logs && !logs.complete && <button type="button" className="coding-session-link" disabled={loading} onClick={() => void load()}>{t('Refresh logs')}</button>}
    </>}
  </div>
}

/** Changed files (+/−) and the commits pushed in each turn. */
export function CodingChanges({ session, commitsOnly }: { session: CodingSession; /** The session page shows the files in its diff viewer; only the commits list stays. */ commitsOnly?: boolean }) {
  const { t } = useI18n()
  const diffs = commitsOnly ? [] : session.diffs ?? []
  const commits = Object.values(session.commitsByTurnActivityId ?? {}).flat().sort((a, b) => (Date.parse(a.createdAt) || 0) - (Date.parse(b.createdAt) || 0))
  if (!diffs.length && !commits.length) return null
  const additions = diffs.reduce((sum, diff) => sum + diff.additions, 0)
  const deletions = diffs.reduce((sum, diff) => sum + diff.deletions, 0)
  return <section className="coding-changes" aria-label={t('Changes')}>
    {diffs.length > 0 && <details>
      <summary>{t('{count} files changed').replace('{count}', String(diffs.length))} <span className="coding-additions">+{additions}</span> <span className="coding-deletions">−{deletions}</span></summary>
      <ul>{diffs.map(diff => <li key={diff.path} data-status={diff.status}><code data-i18n-ignore>{diff.path}</code><span className="coding-additions">+{diff.additions}</span><span className="coding-deletions">−{diff.deletions}</span></li>)}</ul>
    </details>}
    {commits.length > 0 && <ul className="coding-commits" aria-label={t('Commits')}>
      {commits.map(commit => <li key={commit.sha}><GitCommit size={12} aria-hidden="true"/><code data-i18n-ignore>{commit.sha.slice(0, 7)}</code><span data-i18n-ignore>{commit.message?.split('\n')[0]}</span>{commit.signed && <span className="coding-commit-signed">{t('Signed')}</span>}</li>)}
    </ul>}
  </section>
}
