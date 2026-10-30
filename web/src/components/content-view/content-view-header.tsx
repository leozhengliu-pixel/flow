import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'
import { PanelLeft, Plus } from 'lucide-react'
import { useOptionalTranslate } from '@/i18n/i18n'

export interface ContentViewHeaderProps extends HTMLAttributes<HTMLElement> {
  children: ReactNode
  compact?: boolean
  onOpenSidebar?: () => void
  sidebarLabel?: string
  onCreateIssue?: () => void
}

/** Header bar host used by ContentView pages (44px the reference app list header). */
export function ContentViewHeader({
  children,
  className,
  compact = false,
  onOpenSidebar,
  sidebarLabel = 'Open sidebar',
  onCreateIssue,
  ...rest
}: ContentViewHeaderProps) {
  const t = useOptionalTranslate()
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
          <PanelLeft size={14} aria-hidden="true" />
        </button>
      ) : null}
      {children}
      {onCreateIssue && <button className="phone-create-issue" type="button" aria-label={t('New issue')} title={t('New issue')} onClick={onCreateIssue}><Plus size={14}/></button>}
    </header>
  )
}
