import type { ReactNode } from 'react'
import { X } from 'lucide-react'
import type { SplitViewOriginLink } from '@/components/split-view'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import { IssueDetailsPaneSidebar } from './issue-details-pane-sidebar'
import styles from './issues-split-view.module.css'

export interface SplitIssueViewProps {
  issue?: MyIssuesRowData
  preview?: ReactNode
  /** Explicit sidebar override; defaults to IssueDetailsPaneSidebar for `issue`. */
  sidebar?: ReactNode
  /** LS-0252 EntityAgentPanel slot — render when Wave 3 agent panel is available. */
  agentPanel?: ReactNode
  originLink?: SplitViewOriginLink
  onClose?: () => void
}

/**
 * LS-0559 — SplitIssueView.
 * Selected-issue chrome inside split: main preview + details sidebar; agent slot ready.
 */
export function SplitIssueView({
  issue,
  preview,
  sidebar,
  agentPanel,
  originLink,
  onClose,
}: SplitIssueViewProps) {
  const resolvedSidebar =
    sidebar ??
    (issue ? (
      <IssueDetailsPaneSidebar issue={issue} agentPanel={agentPanel} />
    ) : agentPanel ? (
      <IssueDetailsPaneSidebar agentPanel={agentPanel} />
    ) : null)

  return (
    <div className={styles.splitIssue} data-split-issue-view="" aria-label={issue ? `Issue ${issue.identifier}` : 'Issue preview'}>
      <div className={styles.splitIssueMain}>
        <header className={styles.splitIssueHeader}>
          <div className={styles.splitIssueTitleRow}>
            {originLink ? <span className={styles.originLabel}>{originLink.label}</span> : null}
            {issue ? <strong className={styles.issueId}>{issue.identifier}</strong> : null}
          </div>
          {onClose ? (
            <button type="button" className={styles.closeButton} aria-label="Close issue preview" onClick={onClose}>
              <X size={15} />
            </button>
          ) : null}
        </header>
        <div className={styles.splitIssueBody} data-split-issue-body="">
          {preview ?? (issue ? <IssueLitePreview issue={issue} /> : null)}
        </div>
      </div>
      {resolvedSidebar ? (
        <aside className={styles.splitIssueSidebar} data-split-issue-sidebar="" aria-label="Issue details sidebar">
          {resolvedSidebar}
        </aside>
      ) : null}
    </div>
  )
}

function IssueLitePreview({ issue }: { issue: MyIssuesRowData }) {
  return (
    <div className={styles.litePreview}>
      <h3>{issue.title}</h3>
      <dl className={styles.liteProperties}>
        <div><dt>Status</dt><dd><i style={{ backgroundColor: issue.state.color }} />{issue.state.name}</dd></div>
        <div><dt>Priority</dt><dd>{['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority]}</dd></div>
        <div><dt>Assignee</dt><dd>{issue.assignee?.name ?? 'Unassigned'}</dd></div>
        <div><dt>Project</dt><dd>{issue.project?.name ?? 'Add to project'}</dd></div>
        <div><dt>Labels</dt><dd>{issue.labels?.map((label) => label.name).join(', ') || 'Add labels'}</dd></div>
        <div><dt>Due date</dt><dd>{issue.dueDate ?? 'No due date'}</dd></div>
      </dl>
    </div>
  )
}
