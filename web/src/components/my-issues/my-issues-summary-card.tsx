import { useRef, useState, type KeyboardEvent } from 'react'
import { Box } from 'lucide-react'
import { PriorityIcon } from '@/components/issue/issue-icons'
import type { MyIssuesDetailsSummary, MyIssuesSummaryItem, MyIssuesSummaryTab } from './my-issues-details-pane'
import styles from './my-issues-summary-card.module.css'

const TABS: { id: MyIssuesSummaryTab; label: string }[] = [
  { id: 'labels', label: 'Labels' },
  { id: 'priority', label: 'Priority' },
  { id: 'projects', label: 'Projects' },
]

/**
 * Linear's My issues details panel: a floating card beside the list with
 * Labels / Priority / Projects pill tabs and one clickable row per value.
 */
export function MyIssuesSummaryCard({ summary, onItemSelect }: { summary: MyIssuesDetailsSummary; onItemSelect?: (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => void }) {
  const [active, setActive] = useState<MyIssuesSummaryTab>('labels')
  const tabRefs = useRef<Partial<Record<MyIssuesSummaryTab, HTMLButtonElement | null>>>({})
  const empty = !summary.labels.length && !summary.priority.length && !summary.projects.length
  if (empty) return <aside className={`${styles.card} ${styles.emptyCard}`} aria-label="Issue view details">No issue details</aside>
  const items = summary[active]
  const moveFocus = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const next = event.key === 'ArrowRight' ? (index + 1) % TABS.length
      : event.key === 'ArrowLeft' ? (index - 1 + TABS.length) % TABS.length
        : event.key === 'Home' ? 0 : event.key === 'End' ? TABS.length - 1 : undefined
    if (next === undefined) return
    event.preventDefault()
    setActive(TABS[next].id)
    tabRefs.current[TABS[next].id]?.focus()
  }
  return <aside className={styles.card} aria-label="Issue view details">
    <div className={styles.tabs} role="tablist" aria-label="Issue view summary">
      {TABS.map((tab, index) => <button key={tab.id} ref={node => { tabRefs.current[tab.id] = node }} type="button" role="tab" className={styles.tab} aria-selected={active === tab.id} tabIndex={active === tab.id ? 0 : -1} onKeyDown={event => moveFocus(event, index)} onClick={() => setActive(tab.id)}>{tab.label}</button>)}
    </div>
    <div className={styles.list} role="tabpanel" aria-label={`${TABS.find(tab => tab.id === active)?.label} summary`}>
      {items.length ? items.map(item => <button key={item.id} type="button" className={styles.row} aria-label={`${item.label} ${active === 'labels' ? 'label' : active === 'priority' ? 'priority' : 'project'} ${item.count}`} onClick={() => onItemSelect?.(active, item)}>
        <SummaryIcon tab={active} item={item}/>
        <span className={styles.label}>{item.label}</span>
        <span className={styles.seeIssues}>See issues</span>
        <span className={styles.count}>{item.count}</span>
      </button>) : <div className={styles.none}>{active === 'labels' ? 'No labels used' : active === 'priority' ? 'No priorities used' : 'No projects used'}</div>}
    </div>
  </aside>
}

function SummaryIcon({ tab, item }: { tab: MyIssuesSummaryTab; item: MyIssuesSummaryItem }) {
  if (tab === 'labels') return <span className={styles.icon}><i className={styles.dot} style={{ backgroundColor: item.color ?? 'var(--theme-text-secondary)' }}/></span>
  if (tab === 'priority') return <span className={styles.icon}><PriorityIcon priority={Number(item.id)} size={16}/></span>
  return <span className={styles.icon}><Box size={16} strokeWidth={1.75}/></span>
}
