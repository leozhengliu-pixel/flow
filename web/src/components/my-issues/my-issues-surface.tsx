import { forwardRef, useEffect, useState, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { DetailsIcon, FilterIcon } from './my-issues-icons'
import { defaultMyIssuesDisplayOptions } from './my-issues-display-defaults'
import { MyIssuesDisplayMenu } from './my-issues-display-menu'
import { MyIssuesFilterMenu } from './my-issues-filter-menu'
import type { MyIssuesAppliedFilter } from './my-issues-filter-types'
import styles from './my-issues.module.css'

export type MyIssuesView = 'assigned' | 'created' | 'subscribed' | 'activity'
export type MyIssuesGrouping = 'focus' | 'status' | 'priority' | 'project' | 'assignee' | 'agent' | 'cycle' | 'label' | 'team' | 'customer' | 'none'
export type MyIssuesProperty = 'id' | 'status' | 'assignee' | 'priority' | 'project' | 'dueDate' | 'milestone' | 'labels' | 'links' | 'customers' | 'customerRevenue' | 'timeInStatus' | 'created' | 'updated'

export interface MyIssuesDisplayOptions {
  layout: 'list' | 'board'
  grouping: MyIssuesGrouping
  groupOrder: 'asc' | 'desc'
  subGrouping: MyIssuesGrouping
  ordering: 'importance' | 'created' | 'updated' | 'priority'
  completedWindow: 'all' | 'pastDay' | 'pastWeek' | 'pastMonth' | 'currentCycle' | 'none'
  orderCompletedByRecency: boolean
  showSubIssues: boolean
  showEmptyGroups: boolean
  nestedSubIssues: boolean
  properties: Set<MyIssuesProperty>
}

export interface MyIssuesSurfaceProps {
  activeView?: MyIssuesView
  children?: ReactNode
  filterBar?: ReactNode
  viewCounts?: Partial<Record<MyIssuesView, number>>
  viewHref?: (view: MyIssuesView) => string
  detailsOpen?: boolean
  displayOptions?: MyIssuesDisplayOptions
  filterOpenSignal?: number
  filters?: MyIssuesAppliedFilter[]
  onDetailsOpenChange?: (open: boolean) => void
  onDisplayOptionsChange?: (options: MyIssuesDisplayOptions) => void
  filterOptions?: (filter: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined
  onFilterSelect?: (filter: MyIssuesFilterKey, option?: MyIssuesFilterOption) => void
  onFilterToggle?: (filter: MyIssuesFilterKey, option: MyIssuesFilterOption) => void
  onViewChange?: (view: MyIssuesView) => void
  onOpenSidebar?: () => void
}

export type MyIssuesFilterKey = typeof filterGroups[number]['items'][number]['id']
export interface MyIssuesFilterOption { id: string; label: string; color?: string; count?: number }

const views: { id: MyIssuesView; label: string }[] = [
  { id: 'assigned', label: 'Assigned' },
  { id: 'created', label: 'Created' },
  { id: 'subscribed', label: 'Subscribed' },
  { id: 'activity', label: 'Activity' },
]

const filterGroups = [
  { items: [{ id: 'ai', label: 'AI filter' }] },
  { items: [{ id: 'advanced', label: 'Advanced filter' }] },
  { items: [
    { id: 'status', label: 'Status', submenu: true }, { id: 'assignee', label: 'Assignee', submenu: true },
    { id: 'agent', label: 'Agent', submenu: true }, { id: 'agentSession', label: 'Agent Session', submenu: true },
    { id: 'creator', label: 'Creator', submenu: true }, { id: 'priority', label: 'Priority', submenu: true },
    { id: 'labels', label: 'Labels', submenu: true }, { id: 'relations', label: 'Relations', submenu: true },
    { id: 'suggestedLabel', label: 'Suggested label', submenu: true }, { id: 'dates', label: 'Dates', submenu: true },
  ] },
  { items: [
    { id: 'project', label: 'Project', submenu: true }, { id: 'projectProperties', label: 'Project properties', submenu: true },
    { id: 'initiative', label: 'Initiative', submenu: true }, { id: 'customers', label: 'Customers', submenu: true },
  ] },
  { items: [
    { id: 'subscribers', label: 'Subscribers', submenu: true }, { id: 'autoClosed', label: 'Auto-closed' },
    { id: 'content', label: 'Content', submenu: true }, { id: 'links', label: 'Links', submenu: true },
    { id: 'template', label: 'Template', submenu: true },
  ] },
] as const

export function MyIssuesSurface({
  activeView = 'assigned', children, filterBar, detailsOpen = false, displayOptions = defaultMyIssuesDisplayOptions, filterOpenSignal = 0, filters = [], viewCounts, viewHref,
  filterOptions, onDetailsOpenChange, onDisplayOptionsChange, onFilterSelect, onFilterToggle, onViewChange, onOpenSidebar,
}: MyIssuesSurfaceProps) {
  const [filterOpen, setFilterOpen] = useState(false)
  const [displayOpen, setDisplayOpen] = useState(false)
  useEffect(() => { if (filterOpenSignal > 0) setFilterOpen(true) }, [filterOpenSignal])
  useEffect(() => {
    const toggleDetails = (event: globalThis.KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'i') return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, [contenteditable="true"], [role="textbox"]')) return
      event.preventDefault()
      onDetailsOpenChange?.(!detailsOpen)
    }
    window.addEventListener('keydown', toggleDetails)
    return () => window.removeEventListener('keydown', toggleDetails)
  }, [detailsOpen, onDetailsOpenChange])
  return <main className={styles.surface} data-my-issues-surface="true">
    <header className={styles.header}>
      <button className={styles.mobileSidebarButton} aria-label="Open sidebar" onClick={onOpenSidebar}><span/><span/><span/></button>
      <h2>My issues</h2>
    </header>
    <div className={styles.toolbar}>
      <nav className={styles.tabs} aria-label="My issues views">
        {views.map(view => <a key={view.id} href={viewHref?.(view.id) ?? `#${view.id}`} className={styles.tab} data-active={activeView === view.id} data-disabled={activeView === view.id} aria-current={activeView === view.id ? 'page' : undefined} aria-label={viewCounts?.[view.id] == null ? view.label : `${view.label}, ${viewCounts[view.id]} issues`} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); onViewChange?.(view.id) }}>{view.label}</a>)}
      </nav>
      <div className={styles.actions}>
        <MyIssuesFilterMenu open={filterOpen} onOpenChange={setFilterOpen} filters={filters} options={filterOptions} onToggle={(field, option) => { if (onFilterToggle) onFilterToggle(field, option); else onFilterSelect?.(field, option) }} trigger={<ToolbarButton label="Add filter"><FilterIcon/></ToolbarButton>}/>
        <MyIssuesDisplayMenu open={displayOpen} onOpenChange={setDisplayOpen} options={displayOptions} onChange={options => onDisplayOptionsChange?.(options)}/>
        <ToolbarButton label={detailsOpen ? 'Close details' : 'Open details'} title={`${detailsOpen ? 'Close' : 'Open'} details (⌘I)`} pressed={detailsOpen} aria-expanded={detailsOpen} onClick={() => onDetailsOpenChange?.(!detailsOpen)}><DetailsIcon open={detailsOpen}/></ToolbarButton>
      </div>
    </div>
    {filterBar}<div className={styles.content}>{children}</div>
  </main>
}

const ToolbarButton = forwardRef<HTMLButtonElement, ButtonHTMLAttributes<HTMLButtonElement> & { label: string; pressed?: boolean }>(function ToolbarButton({ children, label, pressed, ...props }, ref) {
  return <button ref={ref} type="button" className={styles.iconButton} aria-label={label} aria-pressed={pressed} {...props}>{children}</button>
})
