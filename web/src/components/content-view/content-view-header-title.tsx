import type { ReactNode } from 'react'
import { AppLink } from '@/components/ui/app-link'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewHeaderTitleProps {
  title: ReactNode
  sectionTitle?: ReactNode
  icon?: ReactNode
  href?: string
  onClick?: () => void
  truncate?: boolean
  className?: string
  titleClassName?: string
  as?: 'h1' | 'h2' | 'h3'
}

/** LS-0144 — plain ContentView header title. */
export function ContentViewHeaderTitle({
  title,
  sectionTitle,
  icon,
  href,
  onClick,
  truncate = true,
  className,
  titleClassName,
  as: Tag = 'h2',
}: ContentViewHeaderTitleProps) {
  const titleNode = href ? (
    <AppLink href={href} className={styles.titleLink} onClick={onClick ? event => { event.preventDefault(); onClick() } : undefined}>
      <Tag className={cn(styles.title, titleClassName)} data-truncate={truncate || undefined}>{title}</Tag>
    </AppLink>
  ) : onClick ? (
    <Tag className={cn(styles.title, titleClassName)} data-truncate={truncate || undefined}>
      <button type="button" className={styles.titleButton} onClick={onClick}>{title}</button>
    </Tag>
  ) : (
    <Tag className={cn(styles.title, titleClassName)} data-truncate={truncate || undefined}>{title}</Tag>
  )

  return (
    <div className={cn(styles.titleRow, className)} data-content-view-title="">
      {icon ? <span className={styles.titleIcon} aria-hidden>{icon}</span> : null}
      {titleNode}
      {sectionTitle ? (
        <>
          <span className={styles.chevron} aria-hidden>/</span>
          <h3 className={styles.sectionTitle}>{sectionTitle}</h3>
        </>
      ) : null}
    </div>
  )
}

export interface NewContentViewHeaderTitleProps extends ContentViewHeaderTitleProps {
  actions?: ReactNode
}

/** LS-0420 — icon + title + trailing header actions pack. */
export function NewContentViewHeaderTitle({
  actions,
  className,
  ...rest
}: NewContentViewHeaderTitleProps) {
  return (
    <div className={cn(styles.titleRow, className)} data-new-content-view-header-title="">
      <ContentViewHeaderTitle {...rest} className={undefined} />
      {actions ? <span className={styles.titleActions}>{actions}</span> : null}
    </div>
  )
}
