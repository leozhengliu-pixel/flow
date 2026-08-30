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
  { id: '33', identifier: 'CLE-33', title: '[Power Export] Production investigation: TC Number repeatedly filled after continuing scan', priority: 2, state: { id: 'todo', name: 'Todo', type: 'unstarted', color: '#e2e2e2' }, labels: [{ id: 'testing', name: 'Testing', color: '#29abc2', description: '测试任务/测试资产', issueCount: 3, scope: 'Workspace' }, { id: 'operations', name: 'Operations', color: '#52b788', description: 'Operational delivery work', issueCount: 7, scope: 'Workspace' }], project: { id: 'power', name: 'Power Export', color: '#2563eb' }, assignee: { id: 'zl', name: 'zheng liu', color: '#e96f73' }, parentId: 'parent', parent: { id: 'parent', identifier: 'CLE-10', title: 'Long parent issue title used to verify property alignment' }, ancestors: [{ id: 'parent', identifier: 'CLE-10', title: 'Long parent issue title used to verify property alignment' }], createdAt: '2026-03-28T10:23:10Z', updatedAt: '2026-08-12T11:04:00Z' },
  { id: '20', identifier: 'CLE-20', title: 'Production cleaning task room photo mismatch and OCR validation did not trigger', priority: 2, state: { id: 'todo', name: 'Todo', type: 'unstarted', color: '#e2e2e2' }, labels: [{ id: 'testing', name: 'Testing', color: '#29abc2', description: '测试任务/测试资产', issueCount: 3, scope: 'Workspace' }], project: { id: 'power', name: 'Power Export', color: '#2563eb' }, assignee: { id: 'zl', name: 'zheng liu', color: '#e96f73' }, createdAt: '2026-03-23T10:48:24Z', updatedAt: '2026-08-12T11:04:00Z' },
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
  { id: 'review', label: 'In Review', color: '#4cb782', kind: 'status' as const, stateType: 'started' as const },
  { id: 'done', label: 'Done', color: '#5e6ad2', kind: 'status' as const, stateType: 'completed' as const },
  { id: 'canceled', label: 'Canceled', color: '#a8b2c1', kind: 'status' as const, stateType: 'canceled' as const },
  { id: 'duplicate', label: 'Duplicate', color: '#a8b2c1', kind: 'status' as const, stateType: 'canceled' as const },
]
const priorityOptions = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, id) => ({ id: String(id), label }))
const labelOptions = [{ id: 'testing', label: 'Testing', color: '#29abc2', description: '测试任务/测试资产', issueCount: 3, scope: 'Workspace' }, { id: 'operations', label: 'Operations', color: '#52b788', description: 'Operational delivery work', issueCount: 7, scope: 'Workspace' }, ...Array.from({ length: 24 }, (_, index) => ({ id: `label-${index}`, label: `Regression label ${String(index + 1).padStart(2, '0')}`, color: `hsl(${index * 23} 70% 52%)`, issueCount: index, scope: 'Workspace' }))]
const propertyOptions = {
  ai:[{id:'assigned-to-me',label:'assigned to me'},{id:'completed-last-month',label:'completed in the last month'},{id:'due-next-two-weeks',label:'due in the next 2 weeks'}],advanced:[{id:'new-group',label:'Add filter group',children:[{id:'advanced-status',label:'Status',children:[{id:'status:todo',label:'Todo'}]},{id:'advanced-priority',label:'Priority',children:[{id:'priority:2',label:'High'}]}]}],
  status: statusOptions, priority: priorityOptions, assignee: [{ id: '', label: 'No assignee' }, { id: 'zl', label: 'zheng liu' }], agent:[{id:'',label:'No agent'},{id:'*',label:'Any agent'}],agentSession:[{id:'',label:'No agent session'},{id:'*',label:'Any agent session'}],creator:[{id:'zl',label:'zheng liu'}], dueDate: [{ id: '', label: 'No due date' }, { id: '2026-08-14', label: 'Tomorrow' }],
  dates:[{id:'due-date',label:'Due date',children:[{id:'has-due-date',label:'Has due date'},{id:'no-due-date',label:'No due date'}]},{id:'created-date',label:'Created date',children:[{id:'created-past-day',label:'Past day'}]},{id:'updated-date',label:'Updated date',children:[{id:'updated-past-day',label:'Past day'}]},{id:'started-date',label:'Started date',children:[{id:'started-any',label:'Has started date'}]},{id:'completed-date',label:'Completed date',children:[{id:'completed-any',label:'Has completed date'}]},{id:'auto-closed-date',label:'Auto-closed date',children:[{id:'auto-closed-any',label:'Has auto-closed date'}]},{id:'released-date',label:'Released date',children:[{id:'released-any',label:'Has released date'}]},{id:'triaged-date',label:'Triaged date',children:[{id:'triaged-any',label:'Has triaged date'}]},{id:'time-current-status',label:'Time in current status',children:[{id:'status-over-week',label:'More than one week'}]}],
  labels: labelOptions,suggestedLabel:[{id:'',label:'No suggested label'},...labelOptions], project: [{ id: '', label: 'No project' }, { id: 'power', label: 'Power Export', color: '#2563eb' }],projectProperties:[{id:'project-status',label:'Project status',children:[{id:'project-status:active',label:'Active'}]},{id:'project-status-type',label:'Project status type',children:[{id:'project-status-type:started',label:'Started'}]},{id:'project-priority',label:'Project priority',children:priorityOptions},{id:'project-labels',label:'Project labels',children:labelOptions},{id:'project-lead',label:'Project lead',children:[{id:'project-lead:zl',label:'zheng liu'}]},{id:'project-milestone',label:'Project milestone name',children:[{id:'project-milestone:GA',label:'GA'}]}],initiative:[{id:'',label:'No initiative'},{id:'initiative',label:'Platform initiative'}],cycle:[{id:'',label:'No cycle'}],addedToCycle:[{id:'planned',label:'Planned'},{id:'during',label:'During cycle'},{id:'after',label:'After cycle'}],
  releases:[{id:'release',label:'Release',children:[{id:'release:qa',label:'QA 1'}]},{id:'release-pipeline',label:'Release pipeline',children:[{id:'release-pipeline:flow',label:'Flow QA'}]},{id:'release-stage',label:'Release stage',children:[{id:'release-stage:released',label:'Released'}]},{id:'release-stage-type',label:'Release stage type',children:[{id:'release-stage-type:released',label:'Released'}]},{id:'released-date',label:'Released date',children:[{id:'released-any',label:'Has released date'}]},{id:'no-releases',label:'No releases'}],subscribers:[{id:'',label:'No subscribers'},{id:'zl',label:'zheng liu'}],externalSource:[{id:'',label:'No external source'},{id:'github',label:'GitHub'}],autoClosed:[{id:'true',label:'Auto-closed'},{id:'false',label:'Not auto-closed'}],template:[{id:'',label:'No template'},{id:'bug-template',label:'Bug report'}],
  relations:[{id:'parent_of',label:'Parent issues'},{id:'sub_issue_of',label:'Sub-issues'},{id:'blocked_by',label:'Blocked issues'},{id:'blocks',label:'Blocking issues'},{id:'recurring',label:'Recurring issues',children:[{id:'recurring-any',label:'Any recurring issue'}]},{id:'has-relations',label:'Issues with relations'},{id:'duplicate',label:'Duplicates'}],content:[{id:'content-prompt',label:'Filter by content…'}],links:[{id:'has-links',label:'Issues with links'},{id:'no-links',label:'Issues without links'}]
}

export function MyIssuesVisualFixture() {
  const persistFilters = new URLSearchParams(location.search).get('persistence') === '1'
  const [view, setView] = useState<MyIssuesView>('assigned')
  const [detailsOpen, setDetailsOpen] = useState(new URLSearchParams(location.search).get('details') === '1')
  const [detailsWidth, setDetailsWidth] = useState(350)
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>(() => { if (persistFilters) { try { const value = JSON.parse(localStorage.getItem('flow:fixture:filters') ?? '[]'); if (Array.isArray(value)) return value } catch {} } return new URLSearchParams(location.search).get('filters') === '1' ? [{ id: 'high', field: 'priority', fieldLabel: 'Priority', operator: 'is', value: '2', valueLabel: 'High' }] : [] })
  const commitFilters = (next: MyIssuesAppliedFilter[]) => { if (persistFilters) localStorage.setItem('flow:fixture:filters', JSON.stringify(next)); setFilters(next) }
  const selection = useMyIssuesSelection(groups)
  const visibleGroups = filters.length ? groups.map(group => ({ ...group, issues: group.issues.filter(issue => filters.every(filter => filter.field === 'status' ? issue.state.id === filter.value : filter.field === 'priority' ? String(issue.priority) === filter.value : true)) })).filter(group => group.issues.length) : groups
  return <div className={styles.app}>
    <aside className={styles.sidebar}><div className={styles.workspace}><i>AC</i><strong>acme</strong></div>{['Inbox', 'My issues', 'Pulse', 'Agent'].map(label => <span key={label} data-active={label === 'My issues'}>{label}</span>)}<small>Workspace</small>{['Initiatives', 'Projects', 'Views', 'More'].map(label => <span key={label}>{label}</span>)}</aside>
    <MyIssuesSurface
      activeView={view}
      viewHref={next => `/acme/my-issues/${next}`}
      onViewChange={setView}
      detailsOpen={detailsOpen}
      onDetailsOpenChange={setDetailsOpen}
      displayOptions={defaultMyIssuesDisplayOptions}
      filterOptions={field => (propertyOptions as unknown as Partial<Record<typeof field, typeof priorityOptions>>)[field]}
      onFilterToggle={(field, option) => commitFilters([...filters.filter(filter => filter.field !== field), { id: `${field}-${option.id}`, field, fieldLabel: String(field), operator: 'is', value: option.id, valueLabel: option.label, color: option.color }])}
      filterBar={<MyIssuesFilterBar filters={filters} onClear={() => commitFilters([])} onRemove={id => commitFilters(filters.filter(filter => filter.id !== id))}/>}
    >
      <MyIssuesList groups={visibleGroups} selectedIds={selection.selectedIds} displayProperties={defaultMyIssuesDisplayOptions.properties} propertyOptions={propertyOptions} onSelectIssue={selection.selectIssue} onOpenIssue={selection.openPreview}/>
      <MyIssuesDetailsPane open={detailsOpen} width={detailsWidth} onWidthChange={setDetailsWidth} onClose={() => setDetailsOpen(false)} selectedIssue={selection.previewIssue} summary={summary}/>
    </MyIssuesSurface>
    <MyIssuesBulkActionBar selectedIssues={selection.selectedIssues} actionOptions={action => action === 'priority' ? priorityOptions : action === 'status' ? statusOptions : undefined} onAction={() => {}} onClear={selection.clearSelection}/>
  </div>
}
