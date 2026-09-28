import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewHeaderProps extends HTMLAttributes<HTMLElement> {
  children: ReactNode
  compact?: boolean
  onOpenSidebar?: () => void
  sidebarLabel?: string
}

/** Header bar host used by ContentView pages (44px Linear list header). */
export function ContentViewHeader({
  children,
  className,
  compact = false,
  onOpenSidebar,
  sidebarLabel = 'Open sidebar',
  ...rest
}: ContentViewHeaderProps) {
  return (
    <header
      className={cn(styles.header, className)}
      data-content-view-header=""
      data-compact={compact || undefined}
      {...rest}
    >
      {onOpenSidebar ? (
        <button
          type="button"
          className={styles.mobileSidebarButton}
          aria-label={sidebarLabel}
          data-sidebar-trigger
          onClick={onOpenSidebar}
        >
          <span /><span /><span />
        </button>
      ) : null}
      {children}
    </header>
  )
}
