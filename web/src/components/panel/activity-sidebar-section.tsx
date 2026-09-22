import { useMemo, useState, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import {
  actorBadgeLabel,
  groupActivityHistory,
  type GroupableActivityItem,
} from '@/components/activity/entity-activity-history-grouping'
import { ActivityHistoryGroupSummary, summaryItemsFromActivityTexts } from '@/components/activity/activity-history-group-summary'
import './activity-sidebar-section.css'

export type ActivitySidebarFilter = 'all' | 'updates' | 'comments' | 'history'

export type ActivitySidebarItem = GroupableActivityItem & {
  text: string
  actorLabel?: string
}

export type ActivitySidebarSectionProps = {
  entityId: string
  items: ActivitySidebarItem[]
  open?: boolean
  onOpenChange?: (open: boolean) => void
  onSeeAll?: () => void
  teaserLimit?: number
  /** When true, render beyond the See all teaser with filter + grouped history. */
  expanded?: boolean
  filter?: ActivitySidebarFilter
  onFilterChange?: (filter: ActivitySidebarFilter) => void
  emptyLabel?: string
  headerAction?: ReactNode
  className?: string
}

const FILTERS: { id: ActivitySidebarFilter; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'updates', label: 'Updates' },
  { id: 'comments', label: 'Comments' },
  { id: 'history', label: 'History' },
]

/**
 * LS-0019 ActivitySidebarSection — details sidebar Activity kit with
 * See all teaser, optional filter, and small-activity-section history grouping.
 */
export function ActivitySidebarSection({
  entityId,
  items,
  open: openProp,
  onOpenChange,
  onSeeAll,
  teaserLimit = 6,
  expanded = false,
  filter: filterProp,
  onFilterChange,
  emptyLabel = 'No recent activity',
  headerAction,
  className,
}: ActivitySidebarSectionProps) {
  const [openState, setOpenState] = useState(true)
  const [filterState, setFilterState] = useState<ActivitySidebarFilter>('all')
  const open = openProp ?? openState
  const filter = filterProp ?? filterState

  const setOpen = (next: boolean) => {
    onOpenChange?.(next)
    if (openProp === undefined) setOpenState(next)
  }

  const setFilter = (next: ActivitySidebarFilter) => {
    onFilterChange?.(next)
    if (filterProp === undefined) setFilterState(next)
  }

  const filtered = useMemo(() => {
    if (filter === 'all') return items
    if (filter === 'updates') return items.filter(item => item.category === 'update')
    if (filter === 'comments') return items.filter(item => item.category === 'comment')
    return items.filter(item => item.category !== 'update' && item.category !== 'comment')
  }, [filter, items])

  const visible = expanded ? filtered : filtered.slice(0, teaserLimit)
  const groups = useMemo(
    () => (expanded ? groupActivityHistory(visible, { entityId }) : null),
    [entityId, expanded, visible],
  )

  return (
    <section
      className={['activity-sidebar-section', expanded ? 'is-expanded' : 'is-teaser', className].filter(Boolean).join(' ')}
      data-activity-sidebar-section=""
      data-small-activity-section={expanded ? undefined : ''}
    >
      <button
        aria-expanded={open}
        className="activity-sidebar-section__heading"
        onClick={() => setOpen(!open)}
        type="button"
      >
        <span>Activity</span>
        <ChevronDown size={11} data-open={open || undefined} />
        {onSeeAll && (
          <span
            className="activity-sidebar-section__see-all"
            onClick={event => {
              event.stopPropagation()
              onSeeAll()
            }}
            onKeyDown={event => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault()
                event.stopPropagation()
                onSeeAll()
              }
            }}
            role="link"
            tabIndex={0}
          >
            See all
          </span>
        )}
        {headerAction}
      </button>

      {open && (
        <div className="activity-sidebar-section__body">
          {expanded && (
            <div aria-label="Activity filters" className="activity-sidebar-section__filters" role="toolbar">
              {FILTERS.map(item => (
                <button
                  aria-pressed={filter === item.id}
                  key={item.id}
                  onClick={() => setFilter(item.id)}
                  type="button"
                >
                  {item.label}
                </button>
              ))}
            </div>
          )}

          {!visible.length ? (
            <p className="activity-sidebar-section__empty">{emptyLabel}</p>
          ) : groups ? (
            <div className="activity-sidebar-section__history">
              {groups.map(group => (
                <ActivitySidebarGroup key={group.key} group={group} />
              ))}
            </div>
          ) : (
            <ul className="activity-sidebar-section__teaser">
              {visible.map(item => (
                <li key={item.id}>
                  <strong>{item.actorLabel ?? item.userNames?.[0] ?? 'Someone'}</strong>
                  <span>{item.text}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}

function ActivitySidebarGroup({
  group,
}: {
  group: { key: string; grouping: import('@/components/activity/entity-activity-history-grouping').ActivityGrouping; items: ActivitySidebarItem[] }
}) {
  const [expanded, setExpanded] = useState(group.items.length <= 3)
  const summaryItems = summaryItemsFromActivityTexts(group.items)
  const visible = expanded ? group.items : group.items.slice(0, 1)

  return (
    <div className="activity-sidebar-section__group">
      <header>
        <strong>{group.grouping.summaryActor}</strong>
        <span className="activity-sidebar-section__badge">
          {actorBadgeLabel(group.grouping.actorKey, group.grouping.summaryActor)}
        </span>
        {group.items.length > 1 && <small>{group.items.length} events</small>}
      </header>
      <ul>
        {visible.map(item => (
          <li key={item.id}>
            <span>{item.text}</span>
          </li>
        ))}
      </ul>
      {group.items.length > 1 && (
        <ActivityHistoryGroupSummary
          count={group.items.length}
          expanded={expanded}
          items={summaryItems}
          onToggle={() => setExpanded(value => !value)}
        />
      )}
    </div>
  )
}

