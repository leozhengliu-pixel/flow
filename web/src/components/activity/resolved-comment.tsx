import { CheckCircle2, RotateCcw } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import type { Comment, CommentThreadSummary } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import './resolved-comment.css'

export type ResolvedCommentProps = {
  comment: Comment
  /** When true, show Thread summary block for resolved threads (team `resolvedThreadSummaries`). */
  threadSummariesEnabled?: boolean
  busy?: boolean
  /** Controlled expand; defaults to expanded when unresolved. */
  expanded?: boolean
  defaultExpanded?: boolean
  onExpandedChange?: (expanded: boolean) => void
  onResolve?: (resolved: boolean) => Promise<void> | void
  hideThreadSeparator?: boolean
  children: ReactNode
}

/**
 * Resolved-thread chrome (Linear shows it only once a thread is resolved; open threads resolve from
 * the comment menu). Re-open + optional thread summary for activity Comment hosts (LS-0531).
 * Pairs with `inlineCommentsState` marks and the orphaned `resolvedThreadSummaries` setting.
 */
export function ResolvedComment({
  comment,
  threadSummariesEnabled = false,
  busy = false,
  expanded,
  defaultExpanded,
  onExpandedChange,
  onResolve,
  hideThreadSeparator = false,
  children,
}: ResolvedCommentProps) {
  const { t } = useI18n()
  const resolved = Boolean(comment.resolved)
  const [internalExpanded, setInternalExpanded] = useState(defaultExpanded ?? !resolved)
  const isExpanded = expanded ?? internalExpanded
  const setExpanded = (next: boolean) => {
    onExpandedChange?.(next)
    if (expanded === undefined) setInternalExpanded(next)
  }

  const summary = comment.threadSummary
  const showSummary = resolved && threadSummariesEnabled && Boolean(summary?.content) && !isExpanded

  return (
    <div className={`resolved-comment${resolved ? ' resolved-comment--resolved' : ''}`} data-comment-id={comment.id} data-resolved={resolved ? 'true' : 'false'}>
      {resolved && (
        <div className="resolved-comment__bar" role="group" aria-label={t('Comment thread')}>
          <span className="resolved-comment__bar-label">
            {resolved ? <CheckCircle2 size={14} aria-hidden /> : null}
            {resolved ? t('Resolved') : t('Open thread')}
          </span>
          <div className="resolved-comment__bar-actions">
            {resolved && (
              <button type="button" onClick={() => setExpanded(!isExpanded)} aria-expanded={isExpanded}>
                {isExpanded ? t('Hide thread') : t('Show thread')}
              </button>
            )}
            {onResolve && (
              <button
                type="button"
                disabled={busy}
                onClick={() => void onResolve(false)}
              >
                <RotateCcw size={12} aria-hidden /> {t('Re-open')}
              </button>
            )}
          </div>
        </div>
      )}

      {showSummary && <ThreadSummaryBlock summary={summary!} />}

      {!hideThreadSeparator && resolved && isExpanded && showSummary ? null : null}

      <div className="resolved-comment__thread" data-expanded={isExpanded || !resolved ? 'true' : 'false'}>
        {(isExpanded || !resolved) ? children : (
          <p className="resolved-comment__collapsed">{t('Thread collapsed')}</p>
        )}
      </div>
    </div>
  )
}

function ThreadSummaryBlock({ summary }: { summary: CommentThreadSummary }) {
  const { t } = useI18n()
  return (
    <div className="resolved-comment__summary" aria-label={t('Thread summary')}>
      <span className="resolved-comment__summary-label">{t('Thread summary')}</span>
      <p>{summary.content}</p>
    </div>
  )
}
