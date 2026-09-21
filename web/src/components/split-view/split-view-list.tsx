import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './split-view.module.css'

export interface SplitViewListProps extends Omit<HTMLAttributes<HTMLElement>, 'title'> {
  title?: ReactNode
  children: ReactNode
  header?: ReactNode
  as?: 'section' | 'div' | 'nav'
}

/**
 * LS-0562 — list chrome for SplitView (optional title row + body).
 * Surfaces that already own a ContentView header can omit `title`/`header`.
 */
export function SplitViewList({
  title,
  header,
  children,
  className,
  as: Comp = 'section',
  ...rest
}: SplitViewListProps) {
  return (
    <Comp className={cn(styles.list, className)} data-split-view-list="" {...rest}>
      {header}
      {title != null && title !== false ? (
        <h2 className={styles.listTitle} data-split-view-list-title="">
          {title}
        </h2>
      ) : null}
      {children}
    </Comp>
  )
}

export function SplitViewListTitle({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <h2 className={cn(styles.listTitle, className)} data-split-view-list-title="">
      {children}
    </h2>
  )
}
