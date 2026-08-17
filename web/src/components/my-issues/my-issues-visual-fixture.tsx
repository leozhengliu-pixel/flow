import { useState } from 'react'
import { MyIssuesBulkActionBar } from './my-issues-bulk-action-bar'
import { MyIssuesDetailsPane } from './my-issues-details-pane'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from './my-issues-filter-bar'
import { MyIssuesList, type MyIssuesGroupData, type MyIssuesRowData } from './my-issues-list'
import { MyIssuesSurface, type MyIssuesView } from './my-issues-surface'
import { defaultMyIssuesDisplayOptions } from './my-issues-display-defaults'
import { useMyIssuesSelection } from './use-my-issues-state'
import styles from './my-issues-visual-fixture.module.css'

const issues: MyIssuesRowData[] = [
  { id: '33', identifier: 'CLE-33', title: '[Power Export] Production investigation: TC Number repeatedly filled after continuing scan', priority: 2, state: { id: 'todo', name: 'Todo', type: 'unstarted', color: '#e2e2e2' }, labels: [{ id: 'bug', name: 'Bug', color: '#eb5757' }], project: { id: 'power', name: 'Power Export', color: '#2563eb' }, assignee: { id: 'zl', name: 'zheng liu', color: '#e96f73' }, createdAt: '2026-03-28T10:23:10Z', updatedAt: '2026-08-12T11:04:00Z' },
  { id: '20', identifier: 'CLE-20', title: 'Production cleaning task room photo mismatch and OCR validation did not trigger', priority: 2, state: { id: 'todo', name: 'Todo', type: 'unstarted', color: '#e2e2e2' }, assignee: { id: 'zl', name: 'zheng liu', color: '#e96f73' }, createdAt: '2026-03-23T10:48:24Z', updatedAt: '2026-08-12T11:04:00Z' },
  { id: '25', identifier: 'CLE-25', title: 'Web: supervisor inspection page sporadically shows Task not found due to stale cache', priority: 3, state: { id: 'backlog', name: 'Backlog', type: 'backlog', color: '#8b8b8d' }, assignee: { id: 'zl', name: 'zheng liu', color: '#e96f73' }, createdAt: '2026-03-25T11:04:53Z', updatedAt: '2026-08-12T11:04:00Z' },
  { id: '88', identifier: 'CLE-88', title: '[Codex test archived] ProseMirror storage probe 2026-08-12', priority: 0, state: { id: 'canceled', name: 'Canceled', type: 'canceled', color: '#9b9b9d' }, assignee: { id: 'zl', name: 'zheng liu', color: '#e96f73' }, createdAt: '2026-08-12T22:42:44Z', updatedAt: '2026-08-12T22:42:44Z' },
]
const groups: MyIssuesGroupData[] = [
  { id: 'active', label: 'Other active', stateType: 'started', issues: issues.slice(0, 2) },
  { id: 'backlog', label: 'Backlog', stateType: 'backlog', issues: issues.slice(2, 3) },
  { id: 'canceled', label: 'Canceled', stateType: 'canceled', issues: issues.slice(3) },
]
const summary = { labels: [{ id: 'bug', label: 'Bug', color: '#eb5757', count: 1 }], priority: [{ id: '2', label: 'High', count: 2 }, { id: '3', label: 'Medium', count: 1 }], projects: [{ id: 'power', label: 'Power Export', color: '#2563eb', count: 1 }] }
const statusOptions = [
  { id: 'backlog', label: 'Backlog', color: '#8b8b8d', kind: 'status' as const, stateType: 'backlog' as const },
  { id: 'todo', label: 'Todo', color: '#e2e2e2', kind: 'status' as const, stateType: 'unstarted' as const },
  { id: 'progress', label: 'In Progress', color: '#f2c94c', kind: 'status' as const, stateType: 'started' as const },
  { id: 'done', label: 'Done', color: '#5e6ad2', kind: 'status' as const, stateType: 'completed' as const },
  { id: 'canceled', label: 'Canceled', color: '#a8b2c1', kind: 'status' as const, stateType: 'canceled' as const },
  { id: 'duplicate', label: 'Duplicate', color: '#a8b2c1', kind: 'status' as const, stateType: 'canceled' as const },
]
const priorityOptions = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({ id: String(id), label }))
const propertyOptions = { status: statusOptions, priority: priorityOptions, assignee: [{ id: '', label: 'No assignee' }, { id: 'zl', label: 'zheng liu' }], dueDate: [{ id: '', label: 'No due date' }, { id: '2026-08-14', label: 'Tomorrow' }], labels: [{ id: 'bug', label: 'Bug', color: '#eb5757' }, { id: 'feature', label: 'Feature', color: '#5e6ad2' }], project: [{ id: '', label: 'No project' }, { id: 'power', label: 'Power Export', color: '#2563eb' }] }

export function MyIssuesVisualFixture() {
  const [view, setView] = useState<MyIssuesView>('assigned')
  const [detailsOpen, setDetailsOpen] = useState(new URLSearchParams(location.search).get('details') === '1')
  const [detailsWidth, setDetailsWidth] = useState(350)
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>(new URLSearchParams(location.search).get('filters') === '1' ? [{ id: 'high', field: 'priority', fieldLabel: 'Priority', operator: 'is', value: '2', valueLabel: 'High' }] : [])
  const selection = useMyIssuesSelection(groups)
  const visibleGroups = filters.length ? groups.map(group => ({ ...group, issues: group.issues.filter(issue => issue.priority === 2) })).filter(group => group.issues.length) : groups
  return <div className={styles.app}>
    <aside className={styles.sidebar}><div className={styles.workspace}><i>CL</i><strong>cleantrack</strong></div>{['Inbox', 'My issues', 'Pulse', 'Agent'].map(label => <span key={label} data-active={label === 'My issues'}>{label}</span>)}<small>Workspace</small>{['Initiatives', 'Projects', 'Views', 'More'].map(label => <span key={label}>{label}</span>)}</aside>
    <MyIssuesSurface activeView={view} viewHref={next => `/cleantrack/my-issues/${next}`} onViewChange={setView} detailsOpen={detailsOpen} onDetailsOpenChange={setDetailsOpen} displayOptions={defaultMyIssuesDisplayOptions} filterOptions={field => field === 'priority' ? priorityOptions : field === 'status' ? statusOptions : undefined} onFilterSelect={(field, option) => { if (field === 'priority' && option) setFilters([{ id: option.id, field, fieldLabel: 'Priority', operator: 'is', value: option.id, valueLabel: option.label }]) }} filterBar={<MyIssuesFilterBar filters={filters} onClear={() => setFilters([])} onRemove={id => setFilters(current => current.filter(filter => filter.id !== id))}/>}>
      <MyIssuesList groups={visibleGroups} selectedIds={selection.selectedIds} displayProperties={defaultMyIssuesDisplayOptions.properties} propertyOptions={propertyOptions} onSelectIssue={selection.selectIssue} onOpenIssue={selection.openPreview}/>
      <MyIssuesDetailsPane open={detailsOpen} width={detailsWidth} onWidthChange={setDetailsWidth} onClose={() => setDetailsOpen(false)} selectedIssue={selection.previewIssue} summary={summary}/>
    </MyIssuesSurface>
    <MyIssuesBulkActionBar selectedIssues={selection.selectedIssues} actionOptions={action => action === 'priority' ? priorityOptions : action === 'status' ? statusOptions : undefined} onAction={() => {}} onClear={selection.clearSelection}/>
  </div>
}
