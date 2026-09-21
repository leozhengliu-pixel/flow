import type { CSSProperties, ReactNode } from 'react'
import { SplitView } from '@/components/split-view'
import styles from './issues-split-view.module.css'

export interface IssuesSplitViewPageProps {
  list: ReactNode
  detail?: ReactNode
  className?: string
  style?: CSSProperties
  'aria-label'?: string
}

/**
 * LS-0363 — IssuesSplitViewPage.
 * Shared SplitView host for explorer / my-issues list+detail parity with Inbox.
 */
export function IssuesSplitViewPage({
  list,
  detail,
  className,
  style,
  'aria-label': ariaLabel = 'Issues split view',
}: IssuesSplitViewPageProps) {
  return (
    <SplitView
      surface="issueView"
      list={list}
      detail={detail}
      className={[styles.page, className].filter(Boolean).join(' ')}
      style={style}
      aria-label={ariaLabel}
    />
  )
}
