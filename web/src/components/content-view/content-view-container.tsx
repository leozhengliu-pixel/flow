import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewContainerProps extends HTMLAttributes<HTMLElement> {
  children: ReactNode
  /** Apply the standard 8px framed-workspace inset used by list pages. */
  framed?: boolean
  /** Taller chrome inset (my-issues uses 44px bottom agent bar clearance). */
  inset?: 'default' | 'tall'
  as?: 'main' | 'section' | 'div'
}

/** LS-0134 — shared page chrome container (Linear `ContentViewContainer`). */
export function ContentViewContainer({
  children,
  className,
  framed = true,
  inset = 'default',
  as: Comp = 'main',
  ...rest
}: ContentViewContainerProps) {
  return (
    <Comp
      className={cn(styles.container, className)}
      data-content-view-container=""
      data-framed={framed || undefined}
      data-inset={framed && inset === 'tall' ? 'tall' : undefined}
      {...rest}
    >
      {children}
    </Comp>
  )
}
