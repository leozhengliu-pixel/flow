import { GitPullRequest } from 'lucide-react'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import styles from './issues-split-view.module.css'
import type { IssueWidgetAdornmentsModel, PullRequestLifecycle } from './issue-widget-adornments.model'

function prLabel(lifecycle: PullRequestLifecycle | undefined, count: number) {
  if (!count) return 'No pull requests'
  if (lifecycle === 'merged') return count === 1 ? 'Merged PR' : `${count} merged PRs`
  if (lifecycle === 'closed') return count === 1 ? 'Closed PR' : `${count} closed PRs`
  if (lifecycle === 'draft') return count === 1 ? 'Draft PR' : `${count} draft PRs`
  if (lifecycle === 'approved' || lifecycle === 'inReview') {
    return count === 1 ? 'Linked PR' : `${count} linked PRs`
  }
  return count === 1 ? 'Linked PR' : `${count} pull requests`
}

function PrIcon({ lifecycle }: { lifecycle?: PullRequestLifecycle }) {
  // Single glyph with lifecycle communicated via aria-label / data-pr-lifecycle.
  void lifecycle
  return <GitPullRequest size={12} aria-hidden="true" />
}

/**
 * LS-0359 — IssueWidget adornments (PR lifecycle + Blocked/Blocking) for list rows.
 */
export function IssueWidgetAdornments({
  pullRequestLifecycle,
  pullRequestCount,
  blockedByCount,
  blockingCount,
  showPullRequests = true,
}: IssueWidgetAdornmentsModel & { showPullRequests?: boolean }) {
  const showPr = showPullRequests && pullRequestCount > 0
  const showBlocked = blockedByCount > 0
  const showBlocking = blockingCount > 0
  if (!showPr && !showBlocked && !showBlocking) return null

  return (
    <span className={styles.adornments} data-issue-widget-adornments="">
      {showPr ? (
        <span
          className={styles.adornment}
          data-pr-lifecycle={pullRequestLifecycle ?? 'open'}
          aria-label={prLabel(pullRequestLifecycle, pullRequestCount)}
          title={prLabel(pullRequestLifecycle, pullRequestCount)}
        >
          <PrIcon lifecycle={pullRequestLifecycle} />
          <span>{pullRequestCount}</span>
        </span>
      ) : null}
      {showBlocked ? (
        <span
          className={styles.adornment}
          data-relation="blocked_by"
          aria-label={blockedByCount === 1 ? 'Blocked by 1 issue' : `Blocked by ${blockedByCount} issues`}
          title="Blocked by"
        >
          <IssueActionGlyph label="Blocked by…" fallback={<span aria-hidden="true">⊘</span>} />
          {blockedByCount > 1 ? <span>{blockedByCount}</span> : null}
        </span>
      ) : null}
      {showBlocking ? (
        <span
          className={styles.adornment}
          data-relation="blocks"
          aria-label={blockingCount === 1 ? 'Blocking 1 issue' : `Blocking ${blockingCount} issues`}
          title="Blocking"
        >
          <IssueActionGlyph label="Blocking…" fallback={<span aria-hidden="true">⊞</span>} />
          {blockingCount > 1 ? <span>{blockingCount}</span> : null}
        </span>
      ) : null}
    </span>
  )
}
