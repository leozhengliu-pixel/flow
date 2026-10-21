import { GitPullRequest } from 'lucide-react'
import { IssueRelationChip } from './issue-relation-chip'
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
 * Pull request and blocked / blocking chips of a list row (`compact`: icon-only, as on a board card).
 */
export function IssueWidgetAdornments({
  pullRequestLifecycle,
  pullRequestCount,
  blockedByCount,
  blockingCount,
  blockedByIds,
  blockingIds,
  showPullRequests = true,
  compact = false,
}: IssueWidgetAdornmentsModel & { showPullRequests?: boolean; compact?: boolean }) {
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
      {showBlocked ? <IssueRelationChip kind="blocked_by" count={blockedByCount} ids={blockedByIds} compact={compact}/> : null}
      {showBlocking ? <IssueRelationChip kind="blocks" count={blockingCount} ids={blockingIds} compact={compact}/> : null}
    </span>
  )
}
