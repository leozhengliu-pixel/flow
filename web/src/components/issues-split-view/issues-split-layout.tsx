import type { ReactNode } from 'react'
import { useIsSplitView } from '@/components/split-view'
import { IssuesSplitViewPage } from './issues-split-view-page'
import styles from './issues-split-view.module.css'

export interface IssuesSplitLayoutProps {
  /** When true and viewport matches, render shared SplitView (Inbox parity). */
  detailsOpen: boolean
  list: ReactNode
  /** Detail pane for desktop SplitView (IssueViewSplitPage / summary / preview). */
  detail?: ReactNode
  /** Absolute / overlay details for mobile or when split is off. */
  fallbackDetail?: ReactNode
  /** Linear's floating details card beside the list (desktop, no issue open). */
  aside?: ReactNode
}

/**
 * Chooses IssuesSplitViewPage vs overlay details based on viewport + detailsOpen.
 */
export function IssuesSplitLayout({ detailsOpen, list, detail, fallbackDetail, aside }: IssuesSplitLayoutProps) {
  const isSplitView = useIsSplitView(detailsOpen || Boolean(aside))
  if (isSplitView && aside) {
    return <div className={styles.asideLayout} data-aside-layout=""><div className={styles.asideList}>{list}</div>{aside}</div>
  }
  if (isSplitView && detailsOpen) {
    return <IssuesSplitViewPage list={list} detail={detail} />
  }
  return (
    <>
      {list}
      {fallbackDetail}
    </>
  )
}
