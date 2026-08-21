import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { BarChart3, Building2, Check, ChevronDown, Ellipsis, Expand, Link2, LockKeyhole, RefreshCw, SlidersHorizontal, Star, X } from 'lucide-react'
import { toast } from 'sonner'
import { TeamIcon } from '@/components/issue/issue-icons'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { SavedView, Team, User, Workspace } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import styles from './saved-view-panels.module.css'

export type SavedViewInsightDimension = 'status' | 'priority' | 'assignee' | 'project' | 'labels'
export interface SavedViewInsightsConfig {
  measure: 'issueCount'
  slice: SavedViewInsightDimension
  segment: SavedViewInsightDimension | 'none'
  showArchived: boolean
  hideNoPriority: boolean
}

const DEFAULT_INSIGHTS: SavedViewInsightsConfig = {
  measure: 'issueCount', slice: 'status', segment: 'priority', showArchived: false, hideNoPriority: false,
}

function savedViewInsightsConfig(view: SavedView): SavedViewInsightsConfig {
  const value = view.insights ?? {}
  return {
    ...DEFAULT_INSIGHTS,
    ...(value.measure === 'issueCount' ? { measure: value.measure } : {}),
    ...(isDimension(value.slice) ? { slice: value.slice } : {}),
    ...(value.segment === 'none' || isDimension(value.segment) ? { segment: value.segment } : {}),
    ...(typeof value.showArchived === 'boolean' ? { showArchived: value.showArchived } : {}),
    ...(typeof value.hideNoPriority === 'boolean' ? { hideNoPriority: value.hideNoPriority } : {}),
  }
}

export function SavedViewDetailsPanel({ favorite, menu, onClose, onSummaryItemSelect, onToggleFavorite, rows, team, users, view, workspace }: {
  favorite: boolean
  menu: ReactNode
  onClose: () => void
  onSummaryItemSelect: (dimension: 'assignee' | 'labels' | 'project', id: string, label: string, color?: string) => void
  onToggleFavorite: () => void
  rows: MyIssuesRowData[]
  team?: Team
  users: User[]
  view: SavedView
  workspace: Workspace
}) {
  const [tab, setTab] = useState<'assignee' | 'labels' | 'project'>('assignee')
  const owner = users.find(user => user.id === view.ownerId) ?? users[0]
  const items = useMemo(() => summaryItems(rows, tab), [rows, tab])
  const scopeLabel = view.scope === 'personal' ? 'Personal' : view.scope === 'team' ? team?.name ?? 'Team' : workspace.name
  return <aside aria-label="View sidebar" className={styles.panel}>
    <section className={styles.identityCard}>
      <ViewGlyph className={styles.viewIcon} color={view.color} icon={view.icon}/>
      <h2 data-i18n-ignore>{view.name}</h2>
      <button aria-checked={favorite} aria-label={favorite ? 'Remove from favorites' : 'Add to favorites'} className={styles.iconButton} onClick={onToggleFavorite} role="switch" type="button"><Star fill={favorite ? 'currentColor' : 'none'} size={14}/></button>
      {menu}
      <button aria-label="Close" className={styles.mobileClose} onClick={onClose} type="button"><X size={14}/></button>
      {view.description && <p data-i18n-ignore>{view.description}</p>}
    </section>
    <section className={styles.metadataCard}>
      <div><span>Visibility</span><strong>{view.scope === 'personal' ? <LockKeyhole/> : view.scope === 'team' ? <TeamIcon/> : <Building2/>}<span data-i18n-ignore={view.scope === 'personal' ? undefined : true}>{scopeLabel}</span></strong></div>
      <div><span>Owner</span><strong data-i18n-ignore><Avatar user={owner}/>{owner?.displayName ?? 'Unknown'}</strong></div>
    </section>
    <section className={styles.summaryCard}>
      <div aria-label="View summary" className={styles.tabs} role="tablist">
        {([['assignee', 'Assignees'], ['labels', 'Labels'], ['project', 'Projects']] as const).map(([id, label]) => <button aria-selected={tab === id} key={id} onClick={() => setTab(id)} onKeyDown={handleSummaryTabKeyDown} role="tab" tabIndex={tab === id ? 0 : -1} type="button">{label}</button>)}
      </div>
      <div aria-label={`${tab} summary`} className={styles.summaryList} role="tabpanel">
        {items.length ? items.map(item => <button aria-label={`${item.label} ${item.count}`} className={styles.summaryItem} key={item.id} onClick={() => onSummaryItemSelect(tab, item.id, item.label, item.color)} type="button">
          <SummaryMark color={item.color} kind={tab}/><span data-i18n-ignore>{item.label}</span><small>See issues</small><b>{item.count}</b>
        </button>) : <div className={styles.empty}>No matching data</div>}
      </div>
    </section>
  </aside>
}

export function SavedViewInsightsPanel({ allRows, onClose, onSave, rows, view }: {
  allRows: MyIssuesRowData[]
  onClose: () => void
  onSave: (config: SavedViewInsightsConfig) => Promise<void>
  rows: MyIssuesRowData[]
  view: SavedView
}) {
  const { t } = useI18n()
  const persisted = useMemo(() => savedViewInsightsConfig(view), [view])
  const [config, setConfig] = useState(persisted)
  const [saving, setSaving] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const introKey = `flow:saved-view:${view.id}:insights-intro`
  const [showIntro, setShowIntro] = useState(() => localStorage.getItem(introKey) !== 'dismissed')
  const panelRef = useRef<HTMLElement>(null)
  useEffect(() => setConfig(persisted), [persisted])
  useEffect(() => setShowIntro(localStorage.getItem(introKey) !== 'dismissed'), [introKey])
  useEffect(() => {
    if (!expanded) return
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') setExpanded(false) }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [expanded])
  const source = config.showArchived ? allRows : rows
  const chartRows = config.hideNoPriority ? source.filter(row => row.priority !== 0) : source
  const insight = useMemo(() => buildInsightData(chartRows, config.slice, config.segment), [chartRows, config.segment, config.slice])
  const max = Math.max(1, ...insight.rows.map(item => item.total))
  const dirty = JSON.stringify(config) !== JSON.stringify(persisted)
  const save = async () => { setSaving(true); try { await onSave(config) } finally { setSaving(false) } }
  const dismissIntro = () => { localStorage.setItem(introKey, 'dismissed'); setShowIntro(false) }
  const copyLink = () => void navigator.clipboard.writeText(window.location.href).then(() => toast.success('View link copied'))
  const tableStyle = { '--insight-columns': `minmax(132px,1.2fr) 80px repeat(${Math.max(1, insight.segments.length)},minmax(74px,.8fr))` } as CSSProperties
  return <aside aria-label="View insights" className={`${styles.panel} ${styles.insightsPanel} ${expanded ? styles.expanded : ''}`} ref={panelRef}>
    {showIntro && <section className={styles.insightIntro}>
      <p>Insights makes it easy to analyze issue data. Create reports to reveal trends and find outlier issues that need attention.</p>
      <button aria-label="Dismiss insights introduction" className={styles.iconButton} onClick={dismissIntro} type="button"><X size={14}/></button>
      <a href="https://linear.app/docs/insights" rel="noreferrer" target="_blank">Documentation</a>
    </section>}
    <section className={styles.insightCard}>
      <header><strong>{chartRows.length} {chartRows.length === 1 ? 'issue' : 'issues'}</strong><div>
        <button aria-label={expanded ? 'Exit fullscreen' : 'Expand to fullscreen'} aria-pressed={expanded} className={styles.iconButton} type="button" onClick={() => setExpanded(value => !value)}><Expand size={13}/></button>
        <InsightDisplayMenu config={config} onChange={patch => setConfig(value => ({ ...value, ...patch }))}/>
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className={styles.iconButton} type="button"><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className={styles.insightMenu} collisionPadding={8} sideOffset={4}>
          <DropdownMenu.Item className={styles.insightMenuItem} onSelect={copyLink}><Link2/>Copy link</DropdownMenu.Item>
          <DropdownMenu.Item className={styles.insightMenuItem} onSelect={() => window.open('https://linear.app/docs/insights', '_blank', 'noopener,noreferrer')}><BarChart3/>Insights examples</DropdownMenu.Item>
          <DropdownMenu.Separator/>
          <DropdownMenu.Item className={styles.insightMenuItem} onSelect={() => window.location.reload()}><RefreshCw/>Refresh</DropdownMenu.Item>
        </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        <button aria-label="Close view insights" className={`${styles.iconButton} ${styles.panelClose}`} onClick={onClose} type="button"><X size={14}/></button>
      </div></header>
      <div className={styles.insightControls}>
        <InsightSelect label="Measure" value={config.measure} options={[['issueCount', 'Issue count']]} onChange={measure => setConfig(value => ({ ...value, measure: measure as 'issueCount' }))}/>
        <InsightSelect label="Slice" value={config.slice} options={dimensionOptions()} onChange={slice => setConfig(value => ({ ...value, slice: slice as SavedViewInsightDimension }))}/>
        <InsightSelect label="Segment" value={config.segment} options={[['none', 'No segment'], ...dimensionOptions()]} onChange={segment => setConfig(value => ({ ...value, segment: segment as SavedViewInsightsConfig['segment'] }))}/>
      </div>
      <div aria-label="Insight chart" className={styles.chart} role="img">
        {insight.rows.length ? insight.rows.map(item => <div className={styles.chartColumn} key={item.id}>
          <small>{item.total}</small>
          <div className={styles.chartBar} style={{ height: `${Math.max(4, item.total / max * 190)}px` }}>{insight.segments.map((segment, index) => item.segments[segment.id] ? <i aria-label={`${item.label}, ${segment.label}: ${item.segments[segment.id]}`} key={segment.id} style={{ backgroundColor: insightColor(segment.color, index), flexGrow: item.segments[segment.id] }} title={`${segment.label}: ${item.segments[segment.id]}`}/> : null)}</div>
          <span data-i18n-ignore>{insightValueLabel(t, config.slice, item.label)}</span>
        </div>) : <div className={styles.empty}>No data for this insight</div>}
      </div>
      <div className={styles.insightTable} style={tableStyle}>
        <div className={styles.insightTableRow} role="row"><span>{dimensionLabel(config.slice)}</span><span>Issue count</span>{insight.segments.map((segment, index) => <span key={segment.id}><i className={styles.legendMark} style={{ backgroundColor: insightColor(segment.color, index) }}/><b data-i18n-ignore>{insightValueLabel(t, config.segment, segment.label)}</b></span>)}</div>
        {insight.rows.map(item => <div className={styles.insightTableRow} key={item.id} role="row"><span><i className={styles.tableMark} style={{ backgroundColor: item.color }}/><b data-i18n-ignore>{insightValueLabel(t, config.slice, item.label)}</b></span><span>{item.total}</span>{insight.segments.map(segment => <span key={segment.id}>{item.segments[segment.id] ?? 0}</span>)}</div>)}
      </div>
      <button className={styles.saveInsight} disabled={!dirty || saving} onClick={() => void save()} type="button">{saving ? 'Saving…' : 'Set default for everyone'}</button>
    </section>
  </aside>
}

function InsightSelect({ label, onChange, options, value }: { label: string; onChange: (value: string) => void; options: [string, string][]; value: string }) {
  const selected = options.find(([id]) => id === value)?.[1] ?? value
  return <label><span>{label}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={label} className={styles.insightSelect} type="button"><span>{selected}</span><ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className={`${styles.insightMenu} ${styles.insightSelectMenu}`} collisionPadding={8} sideOffset={4}><DropdownMenu.RadioGroup onValueChange={onChange} value={value}>{options.map(([id, name]) => <DropdownMenu.RadioItem className={styles.insightMenuItem} key={id} value={id}><span className={styles.menuCheck}><DropdownMenu.ItemIndicator><Check/></DropdownMenu.ItemIndicator></span><span>{name}</span></DropdownMenu.RadioItem>)}</DropdownMenu.RadioGroup></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function InsightDisplayMenu({ config, onChange }: { config: SavedViewInsightsConfig; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Insights display options" className={styles.iconButton} type="button"><SlidersHorizontal size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className={`${styles.insightMenu} ${styles.insightDisplayMenu}`} collisionPadding={8} sideOffset={4}>
    <DropdownMenu.CheckboxItem checked={config.showArchived} className={styles.insightMenuItem} onCheckedChange={checked => onChange({ showArchived: Boolean(checked) })}><span>Show archived issues</span><span className={styles.menuCheckbox}><DropdownMenu.ItemIndicator><Check/></DropdownMenu.ItemIndicator></span></DropdownMenu.CheckboxItem>
    <DropdownMenu.Separator/>
    <DropdownMenu.CheckboxItem checked={config.hideNoPriority} className={styles.insightMenuItem} onCheckedChange={checked => onChange({ hideNoPriority: Boolean(checked) })}><span>Hide</span><span className={styles.menuCheckbox}><DropdownMenu.ItemIndicator><Check/></DropdownMenu.ItemIndicator></span><span>No Priority</span></DropdownMenu.CheckboxItem>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function handleSummaryTabKeyDown(event: ReactKeyboardEvent<HTMLButtonElement>) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  event.preventDefault()
  const tabs = Array.from(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [])
  const current = tabs.indexOf(event.currentTarget)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
  tabs[next]?.focus()
  tabs[next]?.click()
}

function summaryItems(rows: MyIssuesRowData[], dimension: 'assignee' | 'labels' | 'project') {
  const values = dimension === 'labels'
    ? rows.flatMap(row => row.labels ?? []).map(item => ({ id: item.id, label: item.name, color: item.color }))
    : dimension === 'project'
      ? rows.filter(row => row.project).map(row => ({ id: row.project!.id, label: row.project!.name, color: row.project!.color }))
      : rows.filter(row => row.assignee).map(row => ({ id: row.assignee!.id, label: row.assignee!.name, color: row.assignee!.color }))
  const counts = new Map<string, { id: string; label: string; color?: string; count: number }>()
  for (const item of values) counts.set(item.id, { ...item, count: (counts.get(item.id)?.count ?? 0) + 1 })
  return [...counts.values()].sort((left, right) => right.count - left.count || left.label.localeCompare(right.label))
}

type InsightValue = { id: string; label: string; color?: string }
type InsightRow = InsightValue & { total: number; segments: Record<string, number> }

function buildInsightData(rows: MyIssuesRowData[], slice: SavedViewInsightDimension, segment: SavedViewInsightsConfig['segment']) {
  const rowMap = new Map<string, InsightRow>()
  const segmentMap = new Map<string, InsightValue & { count: number }>()
  for (const row of rows) {
    const slices = dimensionValues(row, slice)
    const segments = segment === 'none' ? [{ id: 'all', label: 'Issue count' }] : dimensionValues(row, segment)
    for (const segmentValue of segments) {
      const current = segmentMap.get(segmentValue.id)
      segmentMap.set(segmentValue.id, { ...segmentValue, count: (current?.count ?? 0) + 1 })
    }
    for (const sliceValue of slices) {
      const current = rowMap.get(sliceValue.id) ?? { ...sliceValue, total: 0, segments: {} }
      current.total += 1
      for (const segmentValue of segments) current.segments[segmentValue.id] = (current.segments[segmentValue.id] ?? 0) + 1
      rowMap.set(sliceValue.id, current)
    }
  }
  return {
    rows: [...rowMap.values()].sort((left, right) => right.total - left.total || left.label.localeCompare(right.label)),
    segments: [...segmentMap.values()].sort((left, right) => right.count - left.count || left.label.localeCompare(right.label)),
  }
}

function dimensionValues(row: MyIssuesRowData, dimension: SavedViewInsightDimension): InsightValue[] {
  if (dimension === 'labels') return row.labels?.length ? row.labels.map(label => ({ id: label.id, label: label.name, color: label.color })) : [{ id: 'none', label: 'No labels' }]
  if (dimension === 'status') return [{ id: row.state.id, label: row.state.name, color: row.state.color }]
  if (dimension === 'priority') return [{ id: String(row.priority), label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][row.priority], color: ['#8a8a92', '#ef4444', '#f59e0b', '#eab308', '#60a5fa'][row.priority] }]
  if (dimension === 'project') return [{ id: row.project?.id ?? 'none', label: row.project?.name ?? 'No project', color: row.project?.color }]
  return [{ id: row.assignee?.id ?? 'none', label: row.assignee?.name ?? 'No assignee', color: row.assignee?.color }]
}

function insightColor(color: string | undefined, index: number) { return color || ['#73737f', '#5e6ad2', '#2d9d78', '#d6b326', '#4aa3f7', '#ef787c'][index % 6] }
function dimensionOptions(): [string, string][] { return [['status', 'Status'], ['priority', 'Priority'], ['assignee', 'Assignee'], ['project', 'Project'], ['labels', 'Labels']] }
function dimensionLabel(value: SavedViewInsightDimension) { return dimensionOptions().find(([id]) => id === value)?.[1] ?? value }
function insightValueLabel(t: (source: string) => string, dimension: SavedViewInsightDimension | 'none', label: string) { return dimension === 'status' || dimension === 'priority' || dimension === 'none' ? t(label) : label }
function isDimension(value: unknown): value is SavedViewInsightDimension { return typeof value === 'string' && ['status', 'priority', 'assignee', 'project', 'labels'].includes(value) }
function SummaryMark({ color, kind }: { color?: string; kind: string }) { return kind === 'assignee' ? <span className={styles.avatarMark} style={{ backgroundColor: color }}>•</span> : kind === 'project' ? <span className={styles.projectMark}>◇</span> : <i className={styles.dot} style={{ backgroundColor: color }}/>}
function Avatar({ user }: { user?: User }) { return <span className={styles.avatar} data-i18n-ignore>{user ? user.displayName.split(/\s+/).filter(Boolean).slice(0, 2).map(part => part[0]).join('').toUpperCase() : '?'}</span> }
