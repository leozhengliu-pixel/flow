import { ChevronRight } from 'lucide-react'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { codingSessionPath, reviewPath } from '@/lib/app-routes'
import type { BootstrapData, Issue } from '@/types/flow'
import { CodingDiffGlyph } from './coding-diff-glyph'
import { CodingPullRequestGlyph, CodingPullRequestHover } from './coding-pr-hover-card'
import { pullRequestDisplayState, reviewForPullRequest } from './coding-session-model'
import { useCodingAvailability, useCodingSessions } from './use-coding-sessions'
import './coding-sessions.css'

/**
 * The issue sidebar's "Diffs" section (the reference app shows it once the issue has a coding session): "Code changes"
 * with the latest session's +/- counts while it has no PR, then the PR's title (its glyph tinted by state, with the PR
 * hover card), opening the synced review or the session page.
 */
export function IssueCodingDiffs({ issue, data }: { issue: Pick<Issue, 'id'>; data: BootstrapData }) {
  const { t } = useI18n()
  const available = useCodingAvailability(data)
  const { sessions } = useCodingSessions({ issueId: issue.id }, available)
  const latest = [...sessions].sort((a, b) => (Date.parse(b.createdAt) || 0) - (Date.parse(a.createdAt) || 0))[0]
  if (!available || !latest) return null
  const additions = (latest.diffs ?? []).reduce((sum, diff) => sum + diff.additions, 0)
  const deletions = (latest.diffs ?? []).reduce((sum, diff) => sum + diff.deletions, 0)
  const pr = latest.latestPullRequest ?? latest.pullRequests?.at(-1)
  const review = pr ? reviewForPullRequest(pr, data.reviews ?? []) : undefined
  const href = review ? reviewPath(data.workspace.urlKey, review) : codingSessionPath(data.workspace.urlKey, latest.id)
  const row = <AppLink className="issue-coding-diffs-row" href={href} data-pr-state={pr ? pullRequestDisplayState(pr) : undefined}>
    {pr ? <CodingPullRequestGlyph pr={pr} className="issue-coding-diffs-pr-icon"/> : <CodingDiffGlyph size={16}/>}
    {pr?.title ? <span className="issue-coding-diffs-label" data-i18n-ignore>{pr.title}</span> : <span className="issue-coding-diffs-label">{t('Code changes')}</span>}
    {!pr && (additions > 0 || deletions > 0) && <span className="coding-diff-counts"><span className="coding-additions">+{additions}</span><span className="coding-deletions">−{deletions}</span></span>}
    <ChevronRight size={14} aria-hidden="true" className="issue-coding-diffs-chevron"/>
  </AppLink>
  return <section className="property-group issue-coding-diffs" aria-label={t('Diffs')}>
    <h4>{t('Diffs')}</h4>
    {pr ? <CodingPullRequestHover pr={pr} review={review} session={latest}>{row}</CodingPullRequestHover> : row}
  </section>
}
