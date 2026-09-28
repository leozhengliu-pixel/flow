/**
 * LS-0141 ContentViewHeaderFilterPanel — mounts UniversalCustomFilterPanel under
 * ContentView headers (OR / multi-value / within→in via FilterBlock platform).
 */
import { useEffect, useState } from 'react'
import {
  IssueUniversalCustomFilterPanelShouldBeLazyLoaded,
  UniversalCustomFilterPanelShouldBeLazyLoaded,
  type UniversalCustomFilterPanelShouldBeLazyLoadedProps,
} from '@/components/filter'
import type { FilterEntityType, FilterModelNode } from '@/components/filter'
import { ContentViewHeaderFilterButton } from './content-view-header-filter-button'
import styles from './content-view.module.css'

export interface ContentViewHeaderFilterPanelProps
  extends Omit<UniversalCustomFilterPanelShouldBeLazyLoadedProps, 'entityType' | 'filter' | 'onChange' | 'onClose'> {
  entityType?: FilterEntityType
  filter?: FilterModelNode | null
  onChange: (filter: FilterModelNode) => void
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?: (open: boolean) => void
  buttonLabel?: string
  /** Count of active blocks for the header badge. */
  activeCount?: number
}

export function ContentViewHeaderFilterPanel({
  entityType = 'issue',
  filter = null,
  onChange,
  open: openControlled,
  defaultOpen = false,
  onOpenChange,
  buttonLabel,
  activeCount,
  ...panelProps
}: ContentViewHeaderFilterPanelProps) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(defaultOpen)
  const open = openControlled ?? uncontrolledOpen
  const setOpen = (next: boolean) => {
    if (openControlled === undefined) setUncontrolledOpen(next)
    onOpenChange?.(next)
  }

  useEffect(() => {
    if (!open) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  const count =
    activeCount ??
    (filter && Array.isArray(filter.and)
      ? filter.and.length
      : filter && Array.isArray(filter.or)
        ? filter.or.length
        : filter && filter.field
          ? 1
          : 0)

  const Panel =
    entityType === 'issue'
      ? IssueUniversalCustomFilterPanelShouldBeLazyLoaded
      : UniversalCustomFilterPanelShouldBeLazyLoaded

  return (
    <div className={styles.filterPanelHost} data-content-view-filter-panel="" data-open={open || undefined}>
      <ContentViewHeaderFilterButton
        active={open || count > 0}
        count={count}
        label={buttonLabel}
        aria-expanded={open}
        aria-controls="content-view-header-filter-panel"
        onClick={() => setOpen(!open)}
        onClear={
          count > 0
            ? () => {
                onChange({})
                setOpen(false)
              }
            : undefined
        }
      />
      {open ? (
        <div id="content-view-header-filter-panel" className={styles.filterPanelPopover} role="dialog" aria-label="Filters">
          <Panel
            entityType={entityType}
            filter={filter}
            onChange={onChange}
            onClose={() => setOpen(false)}
            {...panelProps}
          />
        </div>
      ) : null}
    </div>
  )
}
