import * as Tooltip from '@radix-ui/react-tooltip'
import type { ReactElement } from 'react'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { avatarColor } from '@/components/issue/avatar-color'
import { ReviewStatusValueGlyph } from '@/components/issue/issue-icons'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { CodeReview } from '@/types/flow'
import type { CodingSession, SessionPullRequest } from '@/types/coding-sessions'
import { PULL_REQUEST_STATE_LABELS, pullRequestDisplayState, pullRequestNumberLabel } from './coding-session-model'

/** A pull request's state glyph (open, draft, merged or closed), tinted by state through `data-pr-state`. */
export function CodingPullRequestGlyph({ pr, size = 16, className }: { pr: SessionPullRequest; size?: number; className?: string }) {
  const state = pullRequestDisplayState(pr)
  return <ReviewStatusValueGlyph status={state === 'open' ? 'open' : state} size={size} className={`coding-pr-glyph${className ? ` ${className}` : ''}`}/>
}

/**
 * The pull request hover card of the issue's Diffs row and the session card's PR chip: the PR glyph, title and
 * author over "<repo>#<n>", then "<State> <when> · <n> files +a -d".
 */
export function CodingPullRequestHoverCard({ pr, review, session }: { pr: SessionPullRequest; review?: CodeReview; session?: Pick<CodingSession, 'diffs'> }) {
  const { t, locale } = useI18n()
  const state = pullRequestDisplayState(pr)
  const files = review?.files?.length || session?.diffs?.length || 0
  const additions = review ? review.additions : (session?.diffs ?? []).reduce((sum, diff) => sum + diff.additions, 0)
  const deletions = review ? review.deletions : (session?.diffs ?? []).reduce((sum, diff) => sum + diff.deletions, 0)
  const at = state === 'closed' ? review?.closedAt ?? pr.updatedAt : state === 'merged' ? review?.mergedAt ?? pr.updatedAt : undefined
  const when = at && !Number.isNaN(Date.parse(at)) ? activityTimeLabel(at, Date.now(), locale) : ''
  const author = review?.author
  const authorName = author?.displayName || author?.name || pr.openedAs || ''
  return <div className="coding-pr-hover-card" data-pr-state={state}>
    <div className="coding-pr-hover-card-head">
      <CodingPullRequestGlyph pr={pr}/>
      <div className="coding-pr-hover-card-titles">
        <strong data-i18n-ignore>{pr.title || review?.title || `${pr.repository}${pullRequestNumberLabel(pr)}`}</strong>
        <span data-i18n-ignore>{`${pr.repository.split('/').at(-1)}${pullRequestNumberLabel(pr)}`}</span>
      </div>
      {authorName && <UserAvatar className="coding-pr-hover-card-avatar" avatarUrl={author?.avatarUrl} color={avatarColor(author?.id || authorName)} name={authorName} title={authorName}/>}
    </div>
    <div className="coding-pr-hover-card-meta">
      <span>{when ? `${t(PULL_REQUEST_STATE_LABELS[state])} ${when}` : t(PULL_REQUEST_STATE_LABELS[state])}</span>
      {files > 0 && <>
        <span aria-hidden="true">·</span>
        <span>{t(files === 1 ? '1 file' : '{count} files').replace('{count}', String(files))}</span>
        {additions > 0 && <span className="coding-additions">+{additions}</span>}
        {deletions > 0 && <span className="coding-deletions">-{deletions}</span>}
      </>}
    </div>
  </div>
}

/**
 * Opens the pull request hover card beside `children`: to its left in the issue sidebar, above the session card's PR
 * chip (left-aligned, 8px off), like the reference app.
 */
export function CodingPullRequestHover({ children, side = 'left', align = 'center', ...card }: { children: ReactElement; side?: 'left' | 'top' | 'bottom' | 'right'; align?: 'start' | 'center' | 'end'; pr: SessionPullRequest; review?: CodeReview; session?: Pick<CodingSession, 'diffs'> }) {
  return <Tooltip.Provider delayDuration={400} skipDelayDuration={150}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content data-flow-motion="tooltip" className="coding-pr-hover" side={side} align={align} sideOffset={side === 'top' ? 8 : 7} collisionPadding={8}>
          <CodingPullRequestHoverCard {...card}/>
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>
}
