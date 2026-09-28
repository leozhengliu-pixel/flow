import { useState, type KeyboardEvent, type ReactNode } from 'react'
import { Box, X } from 'lucide-react'
import type { SplitViewOriginLink } from '@/components/split-view'
import { PriorityIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type {
  MyIssuesDetailsSummary,
  MyIssuesSummaryItem,
  MyIssuesSummaryTab,
} from '@/components/my-issues/my-issues-details-pane'
import { SplitIssueView } from './split-issue-view'
import styles from './issues-split-view.module.css'

export interface IssueViewSplitViewProps {
  originLink?: SplitViewOriginLink
  selectedIssue?: MyIssuesRowData
  preview?: ReactNode
  summary?: MyIssuesDetailsSummary
  sidebar?: ReactNode
  agentPanel?: ReactNode
  onClose?: () => void
  onSummaryItemSelect?: (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => void
}

const SUMMARY_TABS: { id: MyIssuesSummaryTab; label: string }[] = [
  { id: 'labels', label: 'Labels' },
  { id: 'priority', label: 'Priority' },
  { id: 'projects', label: 'Projects' },
]

/**
 * LS-0358 — IssueViewSplitView chrome.
 * Empty state ("Select an issue…"), summary tabs, or SplitIssueView when selected.
 */
export function IssueViewSplitView({
  originLink,
  selectedIssue,
  preview,
  summary,
  sidebar,
  agentPanel,
  onClose,
  onSummaryItemSelect,
}: IssueViewSplitViewProps) {
  if (selectedIssue || preview) {
    return (
      <SplitIssueView
        issue={selectedIssue}
        preview={preview}
        sidebar={sidebar}
        agentPanel={agentPanel}
        originLink={originLink}
        onClose={onClose}
      />
    )
  }

  if (summary) {
    return (
      <SplitSummaryChrome
        summary={summary}
        originLink={originLink}
        onClose={onClose}
        onSummaryItemSelect={onSummaryItemSelect}
      />
    )
  }

  return (
    <div className={styles.empty} data-issue-view-split-empty="" aria-label="Issue split view">
      {originLink ? (
        <header className={styles.emptyHeader}>
          <span className={styles.originLabel}>{originLink.label}</span>
          {onClose ? (
            <button type="button" className={styles.closeButton} aria-label="Close details" onClick={onClose}>
              <X size={15} />
            </button>
          ) : null}
        </header>
      ) : null}
      <div className={styles.emptyBody}>
        <p>Select an issue to open it here</p>
      </div>
    </div>
  )
}

function SplitSummaryChrome({
  summary,
  originLink,
  onClose,
  onSummaryItemSelect,
}: {
  summary: MyIssuesDetailsSummary
  originLink?: SplitViewOriginLink
  onClose?: () => void
  onSummaryItemSelect?: (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => void
}) {
  const [activeTab, setActiveTab] = useState<MyIssuesSummaryTab>('labels')
  const items = summary[activeTab]
  return (
    <div className={styles.summary} data-issue-view-split-summary="" aria-label="Issue view details">
      <header className={styles.summaryHeader}>
        <span>{originLink?.label ?? 'Details'}</span>
        {onClose ? (
          <button type="button" className={styles.closeButton} aria-label="Close details" onClick={onClose}>
            <X size={15} />
          </button>
        ) : null}
      </header>
      <div className={styles.summaryTabs} role="tablist" aria-label="Issue view summary">
        {SUMMARY_TABS.map((tab, index) => (
          <button
            key={tab.id}
            role="tab"
            type="button"
            aria-selected={activeTab === tab.id}
            tabIndex={activeTab === tab.id ? 0 : -1}
            onClick={() => setActiveTab(tab.id)}
            onKeyDown={(event) => {
              const next = tabFromKey(event, index)
              if (next) setActiveTab(next)
            }}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div className={styles.summaryList} role="tabpanel" aria-label={`${SUMMARY_TABS.find((t) => t.id === activeTab)?.label} summary`}>
        {items.length ? (
          items.map((item) => (
            <button
              key={item.id}
              type="button"
              className={styles.summaryItem}
              aria-label={`${item.label} ${activeTab === 'labels' ? 'label' : activeTab === 'priority' ? 'priority' : 'project'} ${item.count}`}
              onClick={() => onSummaryItemSelect?.(activeTab, item)}
            >
              <span className={styles.summaryLabel}>
                <SummaryIcon tab={activeTab} item={item} />
                <span>{item.label}</span>
              </span>
              <span className={styles.summaryCount}>{item.count}</span>
            </button>
          ))
        ) : (
          <div className={styles.summaryEmpty}>
            {activeTab === 'labels' ? 'No labels used' : activeTab === 'priority' ? 'No priorities used' : 'No projects used'}
          </div>
        )}
      </div>
    </div>
  )
}

function SummaryIcon({ tab, item }: { tab: MyIssuesSummaryTab; item: MyIssuesSummaryItem }) {
  if (tab === 'labels') return <i className={styles.labelDot} style={{ backgroundColor: item.color ?? 'var(--theme-text-secondary)' }} />
  if (tab === 'priority') return <span className={styles.summaryIcon}><PriorityIcon priority={Number(item.id)} size={16} /></span>
  return <Box className={styles.summaryIcon} size={16} />
}

function tabFromKey(event: KeyboardEvent<HTMLButtonElement>, index: number): MyIssuesSummaryTab | undefined {
  let nextIndex: number | undefined
  if (event.key === 'ArrowRight') nextIndex = (index + 1) % SUMMARY_TABS.length
  if (event.key === 'ArrowLeft') nextIndex = (index - 1 + SUMMARY_TABS.length) % SUMMARY_TABS.length
  if (event.key === 'Home') nextIndex = 0
  if (event.key === 'End') nextIndex = SUMMARY_TABS.length - 1
  if (nextIndex === undefined) return undefined
  event.preventDefault()
  return SUMMARY_TABS[nextIndex].id
}
