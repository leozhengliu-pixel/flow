import type { ReactNode } from 'react'
import {
  resolveSplitViewOrigin,
  type SplitViewOrigin,
  type SplitViewOriginLink,
} from '@/components/split-view'
import { IssueViewSplitView } from './issue-view-split-view'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDetailsSummary, MyIssuesSummaryItem, MyIssuesSummaryTab } from '@/components/my-issues/my-issues-details-pane'

export interface SplitViewIssueViewProps {
  workspaceSlug: string
  origin?: SplitViewOrigin | null
  /** Pre-resolved origin link (skips resolve when provided). */
  originLink?: SplitViewOriginLink
  selectedIssue?: MyIssuesRowData
  preview?: ReactNode
  summary?: MyIssuesDetailsSummary
  sidebar?: ReactNode
  agentPanel?: ReactNode
  onClose?: () => void
  onSummaryItemSelect?: (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => void
}

/**
 * LS-0561 — SplitViewIssueView.
 * Origin/viewKind helper consumer for issue split hosts (team / myIssues / customView…).
 */
export function SplitViewIssueView({
  workspaceSlug,
  origin,
  originLink: originLinkProp,
  selectedIssue,
  preview,
  summary,
  sidebar,
  agentPanel,
  onClose,
  onSummaryItemSelect,
}: SplitViewIssueViewProps) {
  const originLink = originLinkProp ?? resolveSplitViewOrigin(workspaceSlug, origin)
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
