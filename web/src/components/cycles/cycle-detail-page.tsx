import * as Popover from '@radix-ui/react-popover'
import { BarChart3, Check, ChevronLeft, Filter, LayoutList, Menu, MoreHorizontal, PanelRight, Plus, Search, Star, Columns3 } from 'lucide-react'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import type { BootstrapData, Cycle, CycleMutationInput, Issue, IssueUpdateInput, Team } from '@/types/flow'
import { CycleActions } from './cycle-menus'
import { CycleGraph } from './cycle-graph'
import { cycleStatusLabel, cycleStats, formatCycleRange } from './cycle-model'
import { MyIssuesList, type MyIssuesEditableProperty, type MyIssuesGroupData, type MyIssuesRowData, type MyIssuesRowPropertyOptions } from '@/components/my-issues/my-issues-list'
import { IssueBoard } from '@/components/issue-explorer/issue-board'
import { labelsForResource } from '@/lib/labels'
import './cycles.css'

const LIST_PROPERTIES = new Set(['id', 'status', 'priority', 'labels', 'project', 'dueDate', 'assignee'] as const)
const BOARD_PROPERTIES = new Set(['id', 'status', 'priority', 'labels', 'project', 'dueDate', 'assignee'] as const)

export function CycleDetailPage({ cycle, team, data, onBack, onUpdateCycle, onStartCycle, onCompleteCycle, onUpdateIssue, renderIssuePreview, onOpenSidebar }: {
  cycle: Cycle
  team: Team
  data: BootstrapData
  onBack: () => void
  onUpdateCycle: (input: CycleMutationInput) => Promise<unknown>
  onStartCycle: () => Promise<unknown>
  onCompleteCycle: () => Promise<unknown>
  onUpdateIssue: (issue: Issue, input: IssueUpdateInput) => Promise<unknown>
  renderIssuePreview?: (issue: Issue, onClose: () => void) => ReactNode
  onOpenSidebar: () => void
}) {
  const [layout, setLayout] = useState<'list'|'board'>('list')
  const [detailsOpen, setDetailsOpen] = useState(true)
  const [previewId, setPreviewId] = useState<string>()
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [selected, setSelected] = useState(new Set<string>())
  const cycleIssues = useMemo(() => data.issues.filter(issue => issue.cycleId === cycle.id && !issue.archivedAt && issue.team.id === team.id && issue.title.toLowerCase().includes(query.toLowerCase())), [cycle.id, data.issues, query, team.id])
  const rows = useMemo(() => cycleIssues.map(toRow), [cycleIssues])
  const groups = useMemo(() => data.states.filter(state => state.type !== 'canceled').map(state => ({ id: state.id, label: state.name, stateType: state.type, issues: rows.filter(issue => issue.state.id === state.id) } satisfies MyIssuesGroupData)), [data.states, rows])
  const propertyOptions = useMemo(() => buildPropertyOptions(data), [data])
  const stats = cycleStats(cycle, data.issues)
  const preview = data.issues.find(issue => issue.id === previewId)
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'i') { event.preventDefault(); setDetailsOpen(open => !open) }
      if (event.key === '/' && !(event.target instanceof HTMLInputElement) && !(event.target instanceof HTMLTextAreaElement)) { event.preventDefault(); setSearching(true) }
    }
    addEventListener('keydown', keydown); return () => removeEventListener('keydown', keydown)
  }, [])
  const openRow = (row: MyIssuesRowData) => setPreviewId(row.id)
  const updateProperty = async (row: MyIssuesRowData, property: MyIssuesEditableProperty, value: string | string[]) => {
    const issue = data.issues.find(item => item.id === row.id)
    if (!issue) return
    const input: IssueUpdateInput = property === 'status' ? { stateId: String(value) } : property === 'priority' ? { priority: Number(value) } : property === 'assignee' ? { assigneeId: String(value) } : property === 'project' ? { projectId: String(value) } : property === 'dueDate' ? { dueDate: String(value) } : { labelIds: value as string[] }
    await onUpdateIssue(issue, input)
  }
  return <main className="main-panel cycle-detail-page">
    <header className="cycle-detail-header">
      <button aria-label="Open sidebar" className="cycles-mobile-menu" onClick={onOpenSidebar} type="button"><Menu size={15}/></button>
      <button aria-label="Back to all cycles" className="cycle-icon-button" onClick={onBack} type="button"><ChevronLeft size={16}/></button>
      <span className="cycles-team-mark" style={{ color: team.color }}>{team.icon || team.name[0]}</span><span>{team.key}</span><span className="cycle-detail-header__divider">/</span><strong>{cycle.name}</strong>
      <button aria-label={cycle.favorite ? 'Remove from favorites' : 'Add to favorites'} className={`cycle-icon-button ${cycle.favorite ? 'is-favorite' : ''}`} onClick={() => void onUpdateCycle({ favorite: !cycle.favorite })} type="button"><Star size={16}/></button>
      <CycleActions cycle={cycle} onUpdate={onUpdateCycle} onStart={onStartCycle} onComplete={onCompleteCycle}/>
    </header>
    <div className="cycle-detail-toolbar">
      <div className="cycle-layout-switch"><button aria-pressed={layout === 'list'} onClick={() => setLayout('list')} type="button"><LayoutList size={14}/><span>List</span></button><button aria-pressed={layout === 'board'} onClick={() => setLayout('board')} type="button"><Columns3 size={14}/><span>Board</span></button></div>
      <div className="cycle-detail-toolbar__actions">
        {searching && <label className="cycle-search"><Search size={13}/><input autoFocus placeholder="Filter issues…" value={query} onChange={event => setQuery(event.target.value)} onBlur={() => { if (!query) setSearching(false) }}/></label>}
        {!searching && <button aria-label="Filter issues" onClick={() => setSearching(true)} type="button"><Filter size={14}/></button>}
        <CycleIssuePicker cycle={cycle} issues={data.issues.filter(issue => issue.team.id === team.id && !issue.archivedAt)} onToggle={issue => onUpdateIssue(issue, { cycleId: issue.cycleId === cycle.id ? '' : cycle.id })}/>
        <button aria-label="Toggle cycle details" aria-pressed={detailsOpen} onClick={() => setDetailsOpen(open => !open)} type="button"><PanelRight size={14}/></button>
        <button aria-label="More display options" type="button"><MoreHorizontal size={14}/></button>
      </div>
    </div>
    <div className={`cycle-detail-layout ${detailsOpen ? 'has-details' : ''}`}>
      <section className="cycle-issues">
        <div className="cycle-title-block"><div><span className={`cycle-status is-${cycle.status}`}>{cycleStatusLabel(cycle.status)}</span><span className="cycle-date-chip">{formatCycleRange(cycle, true)}</span></div><h1>{cycle.name}</h1>{cycle.description && <p>{cycle.description}</p>}</div>
        <div className="cycle-issues__surface">
          {layout === 'list' ? <MyIssuesList displayProperties={LIST_PROPERTIES} groups={groups} propertyOptions={propertyOptions} selectedIds={selected} onOpenIssue={openRow} onPropertyChange={updateProperty} onSelectIssue={(id, checked) => setSelected(current => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })}/>
            : <IssueBoard groups={groups} properties={BOARD_PROPERTIES} selectedIds={selected} onMove={(row, _source, target) => { const issue = data.issues.find(item => item.id === row.id); if (issue) void onUpdateIssue(issue, { stateId: target }) }} onOpenIssue={openRow} onSelectIssue={(id, checked) => setSelected(current => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next })}/>} 
        </div>
      </section>
      {detailsOpen && <aside className="cycle-details-panel"><div className="cycle-details-panel__heading"><BarChart3 size={14}/><span>Progress</span></div><div className="cycle-details-stats"><Stat label="Scope" value={stats.scope} color="#77777d"/><Stat label="Started" value={`${stats.started} · ${stats.startedPercent}%`} color="#f2c200"/><Stat label="Completed" value={`${stats.completed} · ${stats.completedPercent}%`} color="#5e6ad2"/></div><CycleGraph cycle={cycle} issues={data.issues}/><dl><div><dt>Duration</dt><dd>{formatCycleRange(cycle)}</dd></div><div><dt>Capacity</dt><dd>{cycle.capacity} issues</dd></div><div><dt>Success</dt><dd>{stats.successPercent}%</dd></div></dl></aside>}
    </div>
    {preview && renderIssuePreview?.(preview, () => setPreviewId(undefined))}
  </main>
}

function CycleIssuePicker({ cycle, issues, onToggle }: { cycle: Cycle; issues: Issue[]; onToggle: (issue: Issue) => void | Promise<unknown> }) {
  const [query, setQuery] = useState('')
  const filtered = issues.filter(issue => `${issue.identifier} ${issue.title}`.toLowerCase().includes(query.toLowerCase())).sort((a, b) => Number(b.cycleId === cycle.id) - Number(a.cycleId === cycle.id))
  return <Popover.Root onOpenChange={open => { if (!open) setQuery('') }}><Popover.Trigger asChild><button className="cycle-add-issue" type="button"><Plus size={14}/><span>Add issue</span></button></Popover.Trigger><Popover.Portal><Popover.Content align="end" className="cycle-issue-picker" sideOffset={5} collisionPadding={10} onOpenAutoFocus={event => event.preventDefault()}>
    <label><Search size={14}/><input autoFocus placeholder="Add issues to cycle…" value={query} onChange={event => setQuery(event.target.value)}/></label><div>{filtered.map(issue => <button aria-pressed={issue.cycleId === cycle.id} key={issue.id} onClick={() => void onToggle(issue)} type="button"><span className="cycle-picker-check">{issue.cycleId === cycle.id && <Check size={11}/>}</span><small>{issue.identifier}</small><span>{issue.title}</span></button>)}</div>
  </Popover.Content></Popover.Portal></Popover.Root>
}

function toRow(issue: Issue): MyIssuesRowData { return { id: issue.id, identifier: issue.identifier, title: issue.title, priority: issue.priority as 0|1|2|3|4, state: issue.state, labels: issue.labels, project: issue.project, assignee: issue.assignee ? { id: issue.assignee.id, name: issue.assignee.displayName, avatarUrl: issue.assignee.avatarUrl } : undefined, estimate: issue.estimate, dueDate: issue.dueDate, createdAt: issue.createdAt, updatedAt: issue.updatedAt, parentId: issue.parentId, sortOrder: issue.sortOrder } }

function buildPropertyOptions(data: BootstrapData): MyIssuesRowPropertyOptions {
  const issueLabels = labelsForResource(data.labels, 'issue')
  const labelGroupNames = new Map(data.labelGroups.filter(group => group.resourceType === 'issue').map(group => [group.id, group.name]))
  return {
    status: data.states.map(state => ({ id: state.id, label: state.name, color: state.color, stateType: state.type, kind: 'status' as const })),
    priority: ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, priority) => ({ id: String(priority), label, priority: priority as 0|1|2|3|4, kind: 'priority' as const })),
    assignee: [{ id: '', label: 'No assignee', kind: 'assignee' as const }, ...data.users.map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, kind: 'assignee' as const }))],
    dueDate: [{ id: '', label: 'No due date', kind: 'dueDate' as const }, ...[0, 1, 7, 14].map(days => { const date = new Date(); date.setDate(date.getDate() + days); return { id: date.toISOString().slice(0, 10), label: days === 0 ? 'Today' : days === 1 ? 'Tomorrow' : `In ${days} days`, kind: 'dueDate' as const } })],
    labels: issueLabels.map(label => ({ ...label, label: label.name, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined, kind: 'labels' as const })),
    project: [{ id: '', label: 'No project', kind: 'project' as const }, ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, kind: 'project' as const }))],
  }
}

function Stat({ color, label, value }: { color: string; label: string; value: string | number }) { return <div><i style={{ background: color }}/><span>{label}</span><strong>△ {value}</strong></div> }
