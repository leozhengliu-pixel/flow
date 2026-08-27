import { useMemo, useState } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, Expand, MoreHorizontal, Shrink } from 'lucide-react'
import { toast } from 'sonner'
import type { Issue, IssueLabel, User } from '@/types/flow'
import { DisplayIcon as Settings2 } from '@/components/ui/view-action-icons'

type Dimension = 'status'|'priority'|'assignee'|'label'
type SegmentDimension = Dimension|'none'
type InsightRow = { id: string; label: string; count: number; color: string }

export function ProjectInsights({ issues, labels, users }: { issues: Issue[]; labels: IssueLabel[]; users: User[] }) {
  const [slice, setSlice] = useState<Dimension>('status')
  const [segment, setSegment] = useState<SegmentDimension>('none')
  const [showEmpty, setShowEmpty] = useState(false)
  const [fullscreen, setFullscreen] = useState(false)
  const rows = useMemo(() => aggregate(issues, slice, labels, users, showEmpty), [issues, labels, showEmpty, slice, users])
  const segmentedRows = useMemo(() => rows.map(row => ({ ...row, segments: segment === 'none' ? [] : aggregate(issues.filter(issue => dimensionIds(issue, slice).includes(row.id)), segment, labels, users, false) })), [issues, labels, rows, segment, slice, users])
  const total = issues.length
  const max = Math.max(1, ...rows.map(row => row.count))
  const csv = ['Group,Issue count', ...rows.map(row => `"${row.label.replaceAll('"', '""')}",${row.count}`)].join('\n')
  return <aside aria-label="Project insights" className={`project-insights ${fullscreen ? 'is-fullscreen' : ''}`}>
    <header><div><strong>{total}</strong><span>issues</span></div><div><button aria-label={fullscreen ? 'Exit fullscreen' : 'Expand to fullscreen'} onClick={() => setFullscreen(value => !value)} title={fullscreen ? 'Exit fullscreen' : 'Expand to fullscreen'} type="button">{fullscreen ? <Shrink size={14}/> : <Expand size={14}/>}</button><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Insights display options" type="button"><Settings2 size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu project-insights__display-menu" sideOffset={5}><DropdownMenu.CheckboxItem checked={showEmpty} onCheckedChange={value => setShowEmpty(Boolean(value))}><span>Show empty groups</span>{showEmpty && <Check size={13}/>}</DropdownMenu.CheckboxItem></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={5}><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(csv).then(() => toast.success('Insight copied as CSV'))}><span>Copy as CSV</span></DropdownMenu.Item><DropdownMenu.Item onSelect={() => { setSlice('status'); setSegment('priority'); setShowEmpty(false) }}><span>Reset insight</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div></header>
    <div className="project-insights__controls"><InsightControl label="Measure" value="Issue count"/><InsightControl label="Slice" onChange={value => { if (value !== 'none') setSlice(value) }} value={slice}/><InsightControl label="Segment" onChange={setSegment} value={segment}/></div>
    <div aria-label={`Issues sliced by ${dimensionLabel(slice)}`} className="project-insights__chart" role="img">{segmentedRows.map(row => <div className="project-insights__bar-column" key={row.id}><div className="project-insights__bar-track"><i data-segmented={row.segments.length ? true : undefined} style={{ height: `${Math.max(row.count ? 9 : 0, row.count / max * 100)}%`, background: row.segments.length ? undefined : row.color }}>{row.segments.map(part => <span aria-label={`${part.label}: ${part.count}`} data-i18n-ignore={segment === 'label' || segment === 'assignee' ? true : undefined} key={part.id} style={{ flexGrow: part.count, background: part.color }}/>)}</i><b>{row.count || ''}</b></div><span data-i18n-ignore={slice === 'label' || slice === 'assignee' ? true : undefined}>{row.label}</span></div>)}</div>
    <div className="project-insights__table"><div className="project-insights__table-header"><span>{dimensionLabel(slice)}</span><span>Issue count</span></div>{rows.map(row => <div className="project-insights__row" key={row.id}><i style={{ background: row.color }}/><span data-i18n-ignore={slice === 'label' || slice === 'assignee' ? true : undefined}>{row.label}</span><strong>{row.count}</strong></div>)}</div>
    <footer><button disabled title="Workspace insight defaults require administrator permission" type="button">Set default for everyone</button></footer>
  </aside>
}

function InsightControl({ label, onChange, value }: { label: string; onChange?: (value: SegmentDimension) => void; value: string }) {
  const options: SegmentDimension[] = label === 'Segment' ? ['none','status','priority','assignee','label'] : ['status','priority','assignee','label']
  return <label><span>{label}</span>{onChange ? <DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button">{dimensionLabel(value as SegmentDimension)}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu" sideOffset={4}>{options.map(item => <DropdownMenu.Item key={item} onSelect={() => onChange(item)}><span>{dimensionLabel(item)}</span>{item === value && <Check size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> : <button aria-disabled="true" disabled type="button">Issue count</button>}</label>
}

function aggregate(issues: Issue[], dimension: Dimension, labels: IssueLabel[], users: User[], showEmpty: boolean): InsightRow[] {
  const rows = new Map<string, InsightRow>()
  const add = (id: string, label: string, color: string) => { const current = rows.get(id); rows.set(id, { id, label, color, count: (current?.count ?? 0) + 1 }) }
  if (showEmpty) {
    if (dimension === 'priority') ['No priority','Urgent','High','Medium','Low'].forEach((label, id) => rows.set(String(id), { id: String(id), label, count: 0, color: priorityColor(id) }))
    if (dimension === 'assignee') users.forEach(user => rows.set(user.id, { id: user.id, label: user.displayName, count: 0, color: '#7b7f87' }))
    if (dimension === 'label') labels.forEach(label => rows.set(label.id, { id: label.id, label: label.name, count: 0, color: label.color }))
  }
  for (const issue of issues) {
    if (dimension === 'status') add(issue.state.id, issue.state.name, issue.state.color)
    else if (dimension === 'priority') add(String(issue.priority), issue.priorityLabel, priorityColor(issue.priority))
    else if (dimension === 'assignee') add(issue.assignee?.id ?? 'unassigned', issue.assignee?.displayName ?? 'No assignee', '#7b7f87')
    else if (!issue.labels.length) add('none', 'No labels', '#7b7f87')
    else issue.labels.forEach(label => add(label.id, label.name, label.color))
  }
  return [...rows.values()].sort((left, right) => right.count - left.count || left.label.localeCompare(right.label))
}

function dimensionIds(issue: Issue, dimension: Dimension) {
  if (dimension === 'status') return [issue.state.id]
  if (dimension === 'priority') return [String(issue.priority)]
  if (dimension === 'assignee') return [issue.assignee?.id ?? 'unassigned']
  return issue.labels.length ? issue.labels.map(label => label.id) : ['none']
}
function dimensionLabel(value: SegmentDimension) { return ({ none: 'No segment', status: 'Status', priority: 'Priority', assignee: 'Assignee', label: 'Labels' })[value] }
function priorityColor(value: number) { return ['#8b8d92','#ef5f5f','#e79342','#d7b43f','#5e9ad2'][value] ?? '#8b8d92' }
