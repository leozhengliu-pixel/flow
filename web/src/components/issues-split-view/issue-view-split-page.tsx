import type { ReactNode } from 'react'
import {
  resolveSplitViewOrigin,
  type SplitViewOrigin,
} from '@/components/split-view'
import { IssueViewSplitView } from './issue-view-split-view'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDetailsSummary, MyIssuesSummaryItem, MyIssuesSummaryTab } from '@/components/my-issues/my-issues-details-pane'

export interface IssueViewSplitPageProps {
  workspaceSlug: string
  origin?: SplitViewOrigin | null
  selectedIssue?: MyIssuesRowData
  preview?: ReactNode
  summary?: MyIssuesDetailsSummary
  sidebar?: ReactNode
  agentPanel?: ReactNode
  onClose?: () => void
  onSummaryItemSelect?: (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => void
}

/**
 * LS-0357 — IssueViewSplitPage.
 * Router/chrome shell: resolves SplitView origin/viewKind then renders IssueViewSplitView.
 */
export function IssueViewSplitPage({
  workspaceSlug,
  origin,
  selectedIssue,
  preview,
  summary,
  sidebar,
  agentPanel,
  onClose,
  onSummaryItemSelect,
}: IssueViewSplitPageProps) {
  const originLink = resolveSplitViewOrigin(workspaceSlug, origin)
  return (
    <IssueViewSplitView
      originLink={originLink}
      selectedIssue={selectedIssue}
      preview={preview}
      summary={summary}
      sidebar={sidebar}
      agentPanel={agentPanel}
      onClose={onClose}
      onSummaryItemSelect={onSummaryItemSelect}
    />
  )
}
