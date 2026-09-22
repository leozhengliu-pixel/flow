/**
 * LS-0140 ContentViewHeaderFilterButton — shared header control that opens
 * ContentViewHeaderFilterPanel / UniversalCustomFilterPanel.
 */
import { ListFilter, X } from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewHeaderFilterButtonProps
  extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  active?: boolean
  count?: number
  label?: ReactNode
  clearLabel?: string
  onClear?: () => void
}

export function ContentViewHeaderFilterButton({
  active = false,
  count = 0,
  label = 'Filter',
  clearLabel = 'Clear filters',
  onClear,
  className,
  ...rest
}: ContentViewHeaderFilterButtonProps) {
  const filtering = active || count > 0
  return (
    <div className={cn(styles.filterButtonGroup, className)} data-content-view-filter-button="">
      <button
        type="button"
        className={cn(styles.filterButton, filtering && styles.filterButtonActive)}
        aria-pressed={filtering}
        data-filtering={filtering || undefined}
        {...rest}
      >
        <ListFilter size={14} aria-hidden="true" />
        <span>{label}</span>
        {count > 0 ? <span className={styles.filterCount}>{count}</span> : null}
      </button>
      {filtering && onClear ? (
        <button
          type="button"
          className={styles.filterClearButton}
          aria-label={clearLabel}
          onClick={event => {
            event.stopPropagation()
            onClear()
          }}
        >
          <X size={12} aria-hidden="true" />
        </button>
      ) : null}
    </div>
  )
}
