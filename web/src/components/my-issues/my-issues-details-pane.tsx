import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import { AlertCircle, Box, X } from 'lucide-react'
import { PriorityIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from './my-issues-list'
import styles from './my-issues-details-pane.module.css'

export type MyIssuesSummaryTab = 'labels' | 'priority' | 'projects'
export interface MyIssuesSummaryItem { id: string; label: string; count: number; color?: string }
export interface MyIssuesDetailsSummary { labels: MyIssuesSummaryItem[]; priority: MyIssuesSummaryItem[]; projects: MyIssuesSummaryItem[] }

export interface MyIssuesDetailsPaneProps {
  open: boolean
  selectedIssue?: MyIssuesRowData
  summary: MyIssuesDetailsSummary
  loading?: boolean
  error?: string
  width?: number
  previewContent?: ReactNode
  onClose: () => void
  onRetry?: () => void
  onSummaryItemSelect?: (tab: MyIssuesSummaryTab, item: MyIssuesSummaryItem) => void
  onWidthChange?: (width: number) => void
}

const summaryTabs: { id: MyIssuesSummaryTab; label: string }[] = [{ id: 'labels', label: 'Labels' }, { id: 'priority', label: 'Priority' }, { id: 'projects', label: 'Projects' }]
const MIN_DETAILS_WIDTH = 280
const MAX_DETAILS_WIDTH = 620

export function MyIssuesDetailsPane({ open, selectedIssue, summary, loading = false, error, width = 350, previewContent, onClose, onRetry, onSummaryItemSelect, onWidthChange }: MyIssuesDetailsPaneProps) {
  const [activeTab, setActiveTab] = useState<MyIssuesSummaryTab>('labels')
  const [dragging, setDragging] = useState(false)
  const pointerStart = useRef({ x: 0, width })
  useEffect(() => { if (!open) setDragging(false) }, [open])
  if (!open) return null

  const startResize = (event: PointerEvent<HTMLButtonElement>) => {
    pointerStart.current = { x: event.clientX, width }
    event.currentTarget.setPointerCapture(event.pointerId)
    setDragging(true)
  }
  const resize = (event: PointerEvent<HTMLButtonElement>) => {
    if (!dragging) return
    onWidthChange?.(clamp(pointerStart.current.width + pointerStart.current.x - event.clientX, MIN_DETAILS_WIDTH, MAX_DETAILS_WIDTH))
  }
  const stopResize = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
    setDragging(false)
  }
  const resizeWithKeyboard = (event: KeyboardEvent<HTMLButtonElement>) => {
    const step = event.shiftKey ? 50 : 10
    const next = event.key === 'ArrowLeft' ? width + step : event.key === 'ArrowRight' ? width - step : event.key === 'Home' ? MIN_DETAILS_WIDTH : event.key === 'End' ? MAX_DETAILS_WIDTH : undefined
    if (next === undefined) return
    event.preventDefault()
    onWidthChange?.(clamp(next, MIN_DETAILS_WIDTH, MAX_DETAILS_WIDTH))
  }

  return <aside className={styles.pane} data-dragging={dragging} style={{ '--details-width': `${width}px` } as CSSProperties} aria-label={selectedIssue ? `Issue preview ${selectedIssue.identifier}` : 'Issue view details'}>
    <button className={styles.resizer} role="separator" aria-label="Resize details" aria-orientation="vertical" aria-valuemin={MIN_DETAILS_WIDTH} aria-valuemax={MAX_DETAILS_WIDTH} aria-valuenow={Math.round(width)} onKeyDown={resizeWithKeyboard} onPointerDown={startResize} onPointerMove={resize} onPointerUp={stopResize} onPointerCancel={stopResize}/>
    {selectedIssue && !previewContent && <IssuePreviewHeader issue={selectedIssue} onClose={onClose}/>}
    <div className={previewContent ? styles.fullPreview : styles.scroller}>
      {loading
        ? <DetailsSkeleton/>
        : error
          ? <DetailsError message={error} onRetry={onRetry}/>
          : selectedIssue
            ? previewContent ?? <IssuePreview issue={selectedIssue}/>
            : <SummaryView activeTab={activeTab} items={summary[activeTab]} onTabChange={setActiveTab} onItem={item => onSummaryItemSelect?.(activeTab, item)}/>}
    </div>
  </aside>
}

function SummaryView({ activeTab, items, onTabChange, onItem }: { activeTab: MyIssuesSummaryTab; items: MyIssuesSummaryItem[]; onTabChange: (tab: MyIssuesSummaryTab) => void; onItem: (item: MyIssuesSummaryItem) => void }) {
  const tabRefs = useRef<Record<MyIssuesSummaryTab, HTMLButtonElement | null>>({ labels: null, priority: null, projects: null })
  const changeTabFromKey = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let nextIndex: number | undefined
    if (event.key === 'ArrowRight') nextIndex = (index + 1) % summaryTabs.length
    if (event.key === 'ArrowLeft') nextIndex = (index - 1 + summaryTabs.length) % summaryTabs.length
    if (event.key === 'Home') nextIndex = 0
    if (event.key === 'End') nextIndex = summaryTabs.length - 1
    if (nextIndex === undefined) return
    event.preventDefault()
    const next = summaryTabs[nextIndex]
    onTabChange(next.id)
    tabRefs.current[next.id]?.focus()
  }
  return <div className={styles.summary}>
    <div className={styles.summaryTabs} role="tablist" aria-label="Issue view summary">{summaryTabs.map((tab, index) => <button key={tab.id} ref={node => { tabRefs.current[tab.id] = node }} role="tab" type="button" aria-selected={activeTab === tab.id} tabIndex={activeTab === tab.id ? 0 : -1} onKeyDown={event => changeTabFromKey(event, index)} onClick={() => onTabChange(tab.id)}>{tab.label}</button>)}</div>
    <div className={styles.summaryList} role="tabpanel" aria-label={`${summaryTabs.find(tab => tab.id === activeTab)?.label} summary`}>{items.length ? items.map(item => <button key={item.id} type="button" className={styles.summaryItem} aria-label={`${item.label} ${summaryItemKind(activeTab)} ${item.count}`} onClick={() => onItem(item)}><span className={styles.summaryLabel}><SummaryItemIcon tab={activeTab} item={item}/><span>{item.label}</span><span className={styles.seeIssues}>See issues</span></span><span className={styles.summaryCount}>{item.count}</span></button>) : <div className={styles.detailsEmpty}>{emptySummaryLabel(activeTab)}</div>}</div>
  </div>
}

function SummaryItemIcon({ tab, item }: { tab: MyIssuesSummaryTab; item: MyIssuesSummaryItem }) {
  if (tab === 'labels') return <i className={styles.labelDot} style={{ backgroundColor: item.color ?? 'lch(63.304% 1.425 272)' }}/>
  if (tab === 'priority') return <span className={styles.summaryIcon}><PriorityIcon priority={Number(item.id)} size={16}/></span>
  return <Box className={styles.summaryIcon} size={16}/>
}

function summaryItemKind(tab: MyIssuesSummaryTab) { return tab === 'labels' ? 'label' : tab === 'priority' ? 'priority' : 'project' }
function emptySummaryLabel(tab: MyIssuesSummaryTab) { return tab === 'labels' ? 'No labels used' : tab === 'priority' ? 'No priorities used' : 'No projects used' }

function IssuePreviewHeader({ issue, onClose }: { issue: MyIssuesRowData; onClose: () => void }) {
  return <header className={styles.previewHeader}><span>{issue.identifier}</span><button aria-label="Close issue preview" onClick={onClose}><X size={15}/></button></header>
}
function IssuePreview({ children, issue }: { children?: ReactNode; issue: MyIssuesRowData }) {
  return <div className={styles.preview}>
    <h3>{issue.title}</h3>
    {children ?? <div className={styles.properties}>
      <PreviewProperty label="Status"><i style={{ backgroundColor: issue.state.color }}/>{issue.state.name}</PreviewProperty>
      <PreviewProperty label="Priority">{['No priority', 'Urgent', 'High', 'Medium', 'Low'][issue.priority]}</PreviewProperty>
      <PreviewProperty label="Assignee">{issue.assignee?.name ?? 'Unassigned'}</PreviewProperty>
      <PreviewProperty label="Project">{issue.project?.name ?? 'Add to project'}</PreviewProperty>
      <PreviewProperty label="Labels">{issue.labels?.map(label => label.name).join(', ') || 'Add labels'}</PreviewProperty>
      <PreviewProperty label="Due date">{issue.dueDate ?? 'No due date'}</PreviewProperty>
    </div>}
  </div>
}
function PreviewProperty({ children, label }: { children: ReactNode; label: string }) { return <div className={styles.property}><span>{label}</span><strong>{children}</strong></div> }
function DetailsSkeleton() { return <div className={styles.detailsSkeleton} aria-busy="true" aria-label="Loading details"><i/><i/><i/><i/><i/></div> }
function DetailsError({ message, onRetry }: { message: string; onRetry?: () => void }) { return <div className={styles.detailsError} role="alert"><AlertCircle size={18}/><strong>Could not load details</strong><span>{message}</span>{onRetry && <button onClick={onRetry}>Try again</button>}</div> }
function clamp(value: number, minimum: number, maximum: number) { return Math.min(maximum, Math.max(minimum, value)) }
