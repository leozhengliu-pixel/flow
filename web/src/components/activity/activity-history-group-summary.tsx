/**
 * LS-0017 ActivityHistoryGroupSummary — collapsed 「Show N events: …」 control.
 */
import {
  formatActivityHistoryGroupSummary,
  type ActivityHistorySummaryItem,
} from './activity-history-group-summary-model'
import './activity-history-group-summary.css'

export {
  formatActivityHistoryGroupSummary,
  summarizeActivityHistoryGroup,
  summaryItemsFromActivityTexts,
  type ActivityHistorySummaryAction,
  type ActivityHistorySummaryItem,
} from './activity-history-group-summary-model'

export type ActivityHistoryGroupSummaryProps = {
  count: number
  items: ActivityHistorySummaryItem[]
  maxItems?: number
  expanded?: boolean
  onToggle?: () => void
  className?: string
}

export function ActivityHistoryGroupSummary({
  count,
  items,
  maxItems = 5,
  expanded = false,
  onToggle,
  className,
}: ActivityHistoryGroupSummaryProps) {
  const label = formatActivityHistoryGroupSummary(count, items, maxItems)
  const classes = ['activity-history-group-summary', expanded ? 'is-expanded' : 'is-collapsed', className]
    .filter(Boolean)
    .join(' ')

  if (onToggle) {
    return (
      <button
        aria-expanded={expanded}
        className={classes}
        data-surface="LS-0017"
        onClick={onToggle}
        type="button"
      >
        {label}
      </button>
    )
  }

  return (
    <span className={classes} data-surface="LS-0017">
      {label}
    </span>
  )
}

ActivityHistoryGroupSummary.displayName = 'ActivityHistoryGroupSummary'
