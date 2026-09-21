import type { ReactNode } from 'react'
import { useIsSplitView } from '@/components/split-view'
import { IssuesSplitViewPage } from './issues-split-view-page'

export interface IssuesSplitLayoutProps {
  /** When true and viewport matches, render shared SplitView (Inbox parity). */
  detailsOpen: boolean
  list: ReactNode
  /** Detail pane for desktop SplitView (IssueViewSplitPage / summary / preview). */
  detail?: ReactNode
  /** Absolute / overlay details for mobile or when split is off. */
  fallbackDetail?: ReactNode
}

/**
 * Chooses IssuesSplitViewPage vs overlay details based on viewport + detailsOpen.
 */
export function IssuesSplitLayout({ detailsOpen, list, detail, fallbackDetail }: IssuesSplitLayoutProps) {
  const isSplitView = useIsSplitView(detailsOpen)
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
