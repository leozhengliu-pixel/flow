import type { HTMLAttributes, ReactNode } from 'react'
import { cn } from '@/lib/utils'
import styles from './content-view.module.css'

export interface ContentViewSubheaderProps extends HTMLAttributes<HTMLDivElement> {
  start?: ReactNode
  end?: ReactNode
  borderless?: boolean
}

/** LS-0145 — tabs / filters / view-options subheader row. */
export function ContentViewSubheader({
  start,
  end,
  children,
  borderless = false,
  className,
  ...rest
}: ContentViewSubheaderProps) {
  return (
    <div
      className={cn(styles.subheader, className)}
      data-content-view-subheader=""
      data-borderless={borderless || undefined}
      {...rest}
    >
      <div className={styles.subheaderStart}>{start ?? children}</div>
      {end ? <div className={styles.subheaderEnd}>{end}</div> : null}
    </div>
  )
}
