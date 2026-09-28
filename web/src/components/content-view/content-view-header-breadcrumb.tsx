import { ChevronRight } from 'lucide-react'
import type { ReactNode } from 'react'
import { AppLink } from '@/components/ui/app-link'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewBreadcrumbItem {
  id?: string
  label: ReactNode
  href?: string
  icon?: ReactNode
  muted?: boolean
  current?: boolean
  onClick?: () => void
}

export interface ContentViewHeaderBreadcrumbProps {
  items: ContentViewBreadcrumbItem[]
  className?: string
  'aria-label'?: string
}

/** LS-0136 */
export function ContentViewHeaderBreadcrumbChevron({ className }: { className?: string }) {
  return (
    <span className={cn(styles.chevron, className)} aria-hidden>
      <ChevronRight size={13} strokeWidth={2} />
    </span>
  )
}

/** LS-0137 */
export function ContentViewHeaderBreadcrumbLink({
  href,
  children,
  className,
  icon,
  muted,
  current,
  onClick,
}: {
  href?: string
  children: ReactNode
  className?: string
  icon?: ReactNode
  muted?: boolean
  current?: boolean
  onClick?: () => void
}) {
  const classNames = cn(styles.breadcrumbItem, className)
  const body = (
    <>
      {icon ? <span className={styles.breadcrumbIcon}>{icon}</span> : null}
      <span>{children}</span>
    </>
  )
  if (href) {
    return (
      <AppLink
        href={href}
        className={classNames}
        data-muted={muted || undefined}
        data-current={current || undefined}
        onClick={event => {
          if (!onClick) return
          if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
          event.preventDefault()
          onClick()
        }}
      >
        {body}
      </AppLink>
    )
  }
  if (onClick) {
    return (
      <button type="button" className={classNames} data-muted={muted || undefined} data-current={current || undefined} onClick={onClick}>
        {body}
      </button>
    )
  }
  return (
    <span className={classNames} data-muted={muted || undefined} data-current={current || undefined}>
      {body}
    </span>
  )
}

/** LS-0135 — breadcrumb trail for ContentView headers. */
export function ContentViewHeaderBreadcrumb({
  items,
  className,
  'aria-label': ariaLabel = 'Breadcrumb',
}: ContentViewHeaderBreadcrumbProps) {
  if (!items.length) return null
  return (
    <nav className={cn(styles.breadcrumb, className)} aria-label={ariaLabel} data-content-view-breadcrumb="">
      {items.map((item, index) => {
        const isLast = index === items.length - 1
        return (
          <span key={item.id ?? `${index}-${typeof item.label === 'string' ? item.label : 'item'}`} style={{ display: 'inline-flex', alignItems: 'center', minWidth: 0 }}>
            {index > 0 ? <ContentViewHeaderBreadcrumbChevron /> : null}
            <ContentViewHeaderBreadcrumbLink
              href={item.href}
              icon={item.icon}
              muted={item.muted}
              current={item.current ?? isLast}
              onClick={item.onClick}
            >
              {item.label}
            </ContentViewHeaderBreadcrumbLink>
          </span>
        )
      })}
    </nav>
  )
}
