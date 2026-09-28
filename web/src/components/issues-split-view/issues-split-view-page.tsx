import type { CSSProperties, ReactNode } from 'react'
import { SplitView, type SplitViewSurface } from '@/components/split-view'
import styles from './issues-split-view.module.css'

export interface IssuesSplitViewPageProps {
  list: ReactNode
  detail?: ReactNode
  /** Persist list width under this SplitView surface key (default issueView). */
  surface?: SplitViewSurface
  className?: string
  style?: CSSProperties
  'aria-label'?: string
}

/**
 * LS-0363 — IssuesSplitViewPage.
 * Shared SplitView host for explorer / my-issues / triage list+detail parity with Inbox.
 */
export function IssuesSplitViewPage({
  list,
  detail,
  surface = 'issueView',
  className,
  style,
  'aria-label': ariaLabel = 'Issues split view',
}: IssuesSplitViewPageProps) {
  return (
    <SplitView
      surface={surface}
      list={list}
      detail={detail}
      className={[styles.page, className].filter(Boolean).join(' ')}
      style={style}
      aria-label={ariaLabel}
    />
  )
}
