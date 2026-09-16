import { useEffect, useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { BarChart3, Bot, Building2, CalendarDays, Check, ChevronDown, ChevronRight, CircleDot, Clock3, Copy, Download, Ellipsis, Expand, Flame, FolderKanban, History, Layers3, Link2, LockKeyhole, Palette, RefreshCw, Search, SlidersHorizontal, Star, Tag, UserRound, X } from 'lucide-react'
import { toast } from 'sonner'
import { CycleIcon, TeamIcon } from '@/components/issue/issue-icons'
import { MyIssuesList, type MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { BootstrapData, SavedView, Team, User, Workspace } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { usePropertyCommand } from '@/components/property/use-property-command'
import styles from './saved-view-panels.module.css'
import { UserAvatar } from '@/components/ui/user-avatar'
import { SelectControl } from '@/components/ui/select-control'
import type { IssueQueryInput } from '@/lib/api'
import { useInsightSource } from './use-insight-source'
import { aggregateInsightValues, aggregationLabels, insightAggregations, insightTargetMatches, sameInsightTarget, type InsightTarget, type InsightAggregation } from './insight-interaction'
import { buildInsightData, formatMetric, titleCase, type InsightData } from './insight-data'
import { InsightExplorer } from './insight-explorer'

export type SavedViewInsightMeasure = 'issueCount' | 'cycleTime' | 'leadTime' | 'issueAge' | 'timeInStatus'
export type SavedViewInsightDimension =
  | 'status' | 'statusType' | 'assignee' | 'agent' | 'agentSession' | 'creator' | 'priority' | 'label' | `labelGroup:${string}` | 'template' | 'externalSource'
  | 'project' | 'initiative' | 'projectLabel' | `projectLabelGroup:${string}` | 'cycle' | 'addedToCycle'
  | 'createdDate' | 'completedDate' | 'canceledDate' | 'startedDate' | 'dueDate' | 'burnUp'
export interface SavedViewInsightsConfig {
  measure: SavedViewInsightMeasure
  timeInStatusIds: string[]
  slice: SavedViewInsightDimension
  segment: SavedViewInsightDimension | 'none'
  showArchived: boolean
  colors: 'status' | 'auto'
  aggregation?: InsightAggregation
  aggregations?: InsightAggregation[]
  latencyScale?: 'linear' | 'log'
}

const DEFAULT_INSIGHTS: SavedViewInsightsConfig = {
  measure: 'issueCount', timeInStatusIds: [], slice: 'status', segment: 'none', showArchived: false, colors: 'status',
}

function savedViewInsightsConfig(view: SavedView): SavedViewInsightsConfig {
  const value = view.insights ?? {}
  return {
    ...DEFAULT_INSIGHTS,
    ...(isMeasure(value.measure) ? { measure: value.measure } : {}),
    ...(Array.isArray(value.timeInStatusIds) && value.timeInStatusIds.every(item => typeof item === 'string') ? { timeInStatusIds: value.timeInStatusIds } : {}),
    ...(isDimension(value.slice) ? { slice: value.slice } : {}),
    ...(value.segment === 'none' || isDimension(value.segment) ? { segment: value.segment } : {}),
    ...(typeof value.showArchived === 'boolean' ? { showArchived: value.showArchived } : {}),
    ...(value.colors === 'status' || value.colors === 'auto' ? { colors: value.colors } : {}),
    ...(insightAggregations.includes(value.aggregation as InsightAggregation) ? { aggregation: value.aggregation as InsightAggregation } : {}),
    ...(Array.isArray(value.aggregations) && value.aggregations.length && value.aggregations.every(item => insightAggregations.includes(item as InsightAggregation)) ? { aggregations: [...new Set(value.aggregations)] as InsightAggregation[] } : {}),
    ...(value.latencyScale === 'linear' || value.latencyScale === 'log' ? { latencyScale: value.latencyScale } : {}),
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
      <div><span>Visibility</span><strong>{view.scope === 'personal' ? <LockKeyhole/> : view.scope === 'team' ? <TeamIcon team={team}/> : <Building2/>}<span data-i18n-ignore={view.scope === 'personal' ? undefined : true}>{scopeLabel}</span></strong></div>
      <div><span>Owner</span><strong data-i18n-ignore><InsightOwnerAvatar user={owner}/>{owner?.displayName ?? 'Unknown'}</strong></div>
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

export function SavedViewInsightsPanel({ allRows, data, onClose, onSave, rows, view, query, onDrillChange, onOpenIssue }: {
  allRows: MyIssuesRowData[]
  data: BootstrapData
  onClose: () => void
  onSave: (config: SavedViewInsightsConfig) => Promise<void>
  rows: MyIssuesRowData[]
  view: SavedView
  query?: IssueQueryInput
  onDrillChange?: (rows: MyIssuesRowData[] | undefined) => void
  onOpenIssue?: (row: MyIssuesRowData) => void
}) {
  const { t } = useI18n()
  const persisted = useMemo(() => savedViewInsightsConfig({ insights: view.insights } as SavedView), [view.insights])
  const [config, setConfig] = useState(persisted)
  const [saving, setSaving] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [target, setTarget] = useState<InsightTarget>()
  const introKey = `flow:saved-view:${view.id}:insights-intro`
  const [showIntro, setShowIntro] = useState(() => localStorage.getItem(introKey) !== 'dismissed')
  const [refreshKey, setRefreshKey] = useState(0)
  useEffect(() => setConfig(persisted), [persisted])
  useEffect(() => setShowIntro(localStorage.getItem(introKey) !== 'dismissed'), [introKey])
  useEffect(() => {
    if (!expanded) { document.body.style.overflow = ''; return }
    document.body.style.overflow = 'hidden'
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') setExpanded(false) }
    window.addEventListener('keydown', onKeyDown)
    return () => { window.removeEventListener('keydown', onKeyDown); document.body.style.overflow = '' }
  }, [expanded])
  const remote = useInsightSource(data, query ? { ...query, archived: config.showArchived ? 'all' : 'false', includeStatusHistory: config.measure === 'timeInStatus' } : undefined, refreshKey)
  const source = query ? remote.rows : config.showArchived ? allRows : rows
  const insight = useMemo(() => { void refreshKey; return buildInsightData(source, config, data) }, [config, data, refreshKey, source])
  const dirty = JSON.stringify(config) !== JSON.stringify(persisted)
  const save = async () => { setSaving(true); try { await onSave(config) } finally { setSaving(false) } }
  const dismissIntro = () => { localStorage.setItem(introKey, 'dismissed'); setShowIntro(false) }
  const copyLink = () => void navigator.clipboard.writeText(window.location.href).then(() => toast.success(t('View link copied')))
  const exportCsv = () => exportInsightsCsv(insight, config, view.name)
  const updateMeasure = (value: string) => setConfig(current => ({ ...current, ...(value.startsWith('timeInStatus:') ? { measure: 'timeInStatus' as const, timeInStatusIds: toggleValue(current.timeInStatusIds, value.slice(13)) } : { measure: value as SavedViewInsightMeasure }) }))
  const selectedSlice = insight.rows.find(item => item.id === target?.slice)
  const selectedSegment = insight.segments.find(item => item.id === target?.segment)
  const selectedRows = useMemo(() => insight.samples.filter(sample => !target || insightTargetMatches(sample, target)).map(sample => sample.item), [insight, target])
  const issueCount = target ? selectedRows.length : insight.samples.length
  const drillLabel = [selectedSlice?.label, selectedSegment?.label, target?.aggregation && `${target.aggregation} ${target.operator === 'gt' ? '>' : '≤'} ${formatMetric(target.threshold ?? 0, config.measure)}`].filter(Boolean).join(' · ')
  const timeInStatusKey = JSON.stringify(config.timeInStatusIds)
  const aggregationsKey = JSON.stringify(config.aggregations)
  useEffect(() => { setTarget(undefined) }, [config.slice, config.segment, config.measure, config.aggregation, aggregationsKey, timeInStatusKey, view.id])
  useEffect(() => {
    if (remote.loading || remote.error || !target) return
    if (target.slice !== undefined && !insight.rows.some(row => row.id === target.slice) || target.segment !== undefined && !insight.segments.some(segment => segment.id === target.segment)) { setTarget(undefined); return }
    if (target.aggregation) {
      const threshold = aggregateInsightValues(insight.samples.filter(sample => target.slice === undefined || sample.slices.includes(target.slice)).map(sample => sample.value), target.aggregation as InsightAggregation)
      if (threshold === undefined) setTarget(undefined)
      else if (threshold !== target.threshold) setTarget({ ...target, threshold })
    }
  }, [insight, target, remote.loading, remote.error])
  useEffect(() => { onDrillChange?.(target && !remote.loading && !remote.error ? selectedRows : undefined) }, [target, selectedRows, onDrillChange, remote.loading, remote.error])
  useEffect(() => () => onDrillChange?.(undefined), [onDrillChange])
  const selectTarget = (next: InsightTarget) => setTarget(current => sameInsightTarget(current, next) ? undefined : next)
  return <aside aria-label="View insights" className={`${styles.panel} ${styles.insightsPanel} ${expanded ? styles.expanded : ''}`}>
    {expanded && <header className={styles.fullscreenHeader}><strong data-i18n-ignore>{view.name}</strong><ChevronRight/><span>Insights</span><InsightActionsMenu copyLink={copyLink} exportCsv={exportCsv} onRefresh={() => setRefreshKey(value => value + 1)}/><button aria-label="Close fullscreen" className={styles.iconButton} onClick={() => setExpanded(false)} type="button"><X size={14}/></button></header>}
    {showIntro && <section className={styles.insightIntro}>
      <p>Insights makes it easy to analyze issue data. Create reports to reveal trends and find outlier issues that need attention.</p>
      <button aria-label="Dismiss insights introduction" className={styles.iconButton} onClick={dismissIntro} type="button"><X size={14}/></button>
      <a href="https://flow.app/docs/insights" rel="noreferrer" target="_blank">Documentation</a>
    </section>}
    <section className={styles.insightCard}>
      <header><strong aria-live="polite">{remote.loading ? t('Loading…') : `${issueCount} ${t(issueCount === 1 ? 'issue' : 'issues')}${target && drillLabel ? ` ${t('in')} ${drillLabel}` : ''}`}</strong><div>
        <button aria-label={expanded ? 'Close fullscreen' : 'Expand to fullscreen'} aria-pressed={expanded} className={styles.iconButton} type="button" onClick={() => setExpanded(value => !value)}>{expanded ? <X size={14}/> : <Expand size={14}/>}</button>
        <InsightDisplayMenu config={config} onChange={patch => setConfig(value => ({ ...value, ...patch }))}/>
        <InsightActionsMenu copyLink={copyLink} exportCsv={exportCsv} onRefresh={() => setRefreshKey(value => value + 1)}/>
        <button aria-label="Close view insights" className={`${styles.iconButton} ${styles.panelClose}`} onClick={onClose} type="button"><X size={14}/></button>
      </div></header>
      <div className={styles.insightControls}>
        <InsightPicker label="Measure" value={config.measure} options={measureOptions(data, config)} onChange={updateMeasure}/>
        <InsightPicker label="Slice" value={config.slice} options={dimensionOptions(data, true)} onChange={slice => setConfig(value => ({ ...value, slice: slice as SavedViewInsightDimension }))}/>
        {config.measure === 'issueCount' ? <InsightPicker label="Segment" value={config.segment} options={segmentOptions(data)} onChange={segment => setConfig(value => ({ ...value, segment: segment as SavedViewInsightsConfig['segment'] }))}/> : <InsightAggregationPicker config={config} onChange={aggregations => setConfig(value => ({ ...value, aggregations }))}/>}
      </div>
      {remote.error ? <div role="alert" className={styles.empty}>{t('Could not load insights')}<button type="button" onClick={() => setRefreshKey(value => value + 1)}>{t('Retry')}</button></div> : remote.loading ? <div role="status" className={styles.empty}>{t('Loading…')}</div> : !insight.rows.length ? <div role="status" className={styles.empty}>{t('No matching issues')}</div> : <InsightExplorer onOpenIssue={onOpenIssue} sliceLabel={dimensionLabel(config.slice, data)} config={config} data={data} insight={insight} expanded={expanded} target={target} onSelect={selectTarget} onClear={() => setTarget(undefined)} />}
      <button className={styles.saveInsight} disabled={!dirty || saving} onClick={() => void save()} type="button">{saving ? 'Saving…' : 'Set default for everyone'}</button>
    </section>
    {expanded && <section className={styles.drillIssueList} aria-label={`${drillLabel || 'All'} issues`}><MyIssuesList groups={[{ id: 'insight-details', label: drillLabel || t('All issues'), issues: selectedRows }]} displayProperties={new Set(['id', 'status', 'assignee'])} onOpenIssue={onOpenIssue}/></section>}
  </aside>
}

export function InsightHiddenNotice({ hidden, onShow }: { hidden: number; onShow: () => void }) {
  const { t } = useI18n()
  if (hidden <= 0) return null
  return <div className={styles.drillNotice}><span>{hidden} {t('issues hidden by display options')}</span><button type="button" onClick={onShow}>{t('Show hidden issues')}</button></div>
}

function InsightAggregationPicker({ config, onChange }: { config: SavedViewInsightsConfig; onChange: (values: InsightAggregation[]) => void }) {
  const { t } = useI18n()
  const selected = config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as InsightAggregation[])
  return <label><span>{t('Aggregations')}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" aria-label={t('Aggregations')} className={styles.insightSelect}><span>{t('Percentiles')}</span><ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" className={styles.insightMenu} sideOffset={4}>{(['median','p75','p95'] as const).map(id => <DropdownMenu.CheckboxItem className={styles.insightMenuItem} key={id} checked={selected.includes(id)} disabled={selected.length === 1 && selected.includes(id)} onSelect={event => event.preventDefault()} onCheckedChange={checked => onChange(checked ? [...selected, id] : selected.filter(value => value !== id))}><span>{id === 'median' ? 'P50' : aggregationLabels[id]}</span>{selected.includes(id) && <Check size={13}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function InsightActionsMenu({ copyLink, exportCsv, onRefresh }: { copyLink: () => void; exportCsv: () => void; onRefresh: () => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className={styles.iconButton} type="button"><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className={styles.insightMenu} collisionPadding={8} sideOffset={4}><DropdownMenu.Item className={styles.insightMenuItem} onSelect={copyLink}><Link2/>Copy link</DropdownMenu.Item><DropdownMenu.Item className={styles.insightMenuItem} onSelect={exportCsv}><Download/>Export insights as CSV…</DropdownMenu.Item><DropdownMenu.Item className={styles.insightMenuItem} onSelect={() => window.open('https://flow.app/docs/insights', '_blank', 'noopener,noreferrer')}><BarChart3/>Insights examples</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className={styles.insightMenuItem} onSelect={onRefresh}><RefreshCw/>Refresh</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

type InsightOption = { id: string; label: string; description?: string; separatorBefore?: boolean; icon?: ReactNode; checked?: boolean; children?: InsightOption[] }

function InsightPicker({ label, onChange, options, value }: { label: string; onChange: (value: string) => void; options: InsightOption[]; value: string }) {
  const [open, setOpen] = useState(false)
  const command = usePropertyCommand({ open, options, selectedIds: [value], onOpenChange: setOpen, onSelect: option => onChange(option.id) })
  const selected = findInsightOption(options, value)?.label ?? value
  return <label><span>{label}</span><DropdownMenu.Root open={open} onOpenChange={setOpen}><DropdownMenu.Trigger asChild><button aria-label={label} className={styles.insightSelect} type="button"><span>{selected}</span><ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className={`${styles.insightMenu} ${styles.insightSelectMenu}`} collisionPadding={8} onKeyDown={command.onKeyDown} sideOffset={4}>
    <div className={styles.insightSearch}><Search/><input aria-label="Filter…" autoFocus ref={command.inputRef} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={event => { command.onKeyDown(event); event.stopPropagation() }} placeholder="Filter…" value={command.query}/></div>
    <div className={styles.insightMenuScroll} role="listbox">{command.filteredOptions.map(option => <InsightPickerOption key={option.id} onChange={value => { onChange(value); if (!option.children?.length) setOpen(false) }} option={option} value={value}/>)}</div>
    {!command.filteredOptions.length && <div className={styles.insightMenuEmpty}>No results</div>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function InsightPickerOption({ onChange, option, value }: { onChange: (value: string) => void; option: InsightOption; value: string }) {
  const { t } = useI18n()
  const item = <><span className={styles.insightMenuCopy} title={option.description ? t(option.description) : undefined}><b>{option.label}</b></span>{option.id === value && <Check className={styles.trailingCheck}/>}</>
  return <>{option.separatorBefore && <DropdownMenu.Separator/>}{option.children?.length ? <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.insightMenuItem}>{item}<span className={styles.menuChevron}>▶</span></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={`${styles.insightMenu} ${styles.insightSubMenu}`} sideOffset={4}>{option.children.map(child => <DropdownMenu.CheckboxItem checked={child.checked ?? child.id === value} className={styles.insightMenuItem} key={child.id} onSelect={event => { if (child.id.startsWith('timeInStatus:')) event.preventDefault(); onChange(child.id) }}>{child.icon && <span className={styles.insightMenuIcon}>{child.icon}</span>}<span>{child.label}</span>{(child.checked ?? child.id === value) && <Check className={styles.trailingCheck}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub> : <DropdownMenu.Item className={styles.insightMenuItem} onSelect={() => onChange(option.id)}>{item}</DropdownMenu.Item>}</>
}

function InsightDisplayMenu({ config, onChange }: { config: SavedViewInsightsConfig; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  return <Popover.Root><Popover.Trigger asChild><button aria-label="Insights display options" className={styles.iconButton} type="button"><SlidersHorizontal size={13}/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className={styles.insightDisplayPopover} collisionPadding={8} sideOffset={4}>
    <div className={styles.insightDisplayRow}><span>Show archived issues</span><button aria-checked={config.showArchived} aria-label="Show archived issues" className={styles.insightToggle} onClick={() => onChange({ showArchived: !config.showArchived })} role="switch" type="button"><i/></button></div>
    <div className={styles.insightDisplaySeparator}/>
    {config.measure !== 'issueCount' && <div className={styles.insightDisplayRow}><span>Y-axis scale</span><SelectControl className={styles.insightColorSelect} label="Y-axis scale" value={config.latencyScale ?? 'log'} options={[{ value: 'log', label: 'Logarithmic' }, { value: 'linear', label: 'Linear' }]} onChange={value => onChange({ latencyScale: value as 'log' | 'linear' })}/></div>}
    <div className={styles.insightDisplayRow}><span>Colors</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Colors" className={styles.insightColorSelect} role="combobox" type="button"><Palette/>{config.colors === 'status' ? 'Status colors' : 'Auto-color'}<ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className={styles.insightMenu} sideOffset={4}><DropdownMenu.RadioGroup value={config.colors} onValueChange={colors => onChange({ colors: colors as SavedViewInsightsConfig['colors'] })}><DropdownMenu.RadioItem className={styles.insightMenuItem} value="status">Status colors{config.colors === 'status' && <Check className={styles.trailingCheck}/>}</DropdownMenu.RadioItem><DropdownMenu.RadioItem className={styles.insightMenuItem} value="auto">Auto-color{config.colors === 'auto' && <Check className={styles.trailingCheck}/>}</DropdownMenu.RadioItem></DropdownMenu.RadioGroup></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>
  </Popover.Content></Popover.Portal></Popover.Root>
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

function measureOptions(data: BootstrapData, config: SavedViewInsightsConfig): InsightOption[] {
  const stateTypes = [...new Set(data.states.map(state => state.type))]
  return [
    { id: 'issueCount', label: 'Issue count', description: 'Number of individual issues', icon: <CircleDot/> },
    { id: 'cycleTime', label: 'Cycle time', description: 'Time from started to completed', separatorBefore: true, icon: <History/> },
    { id: 'leadTime', label: 'Lead time', description: 'Time from created to completed', icon: <Clock3/> },
    { id: 'issueAge', label: 'Issue age', description: 'Time from created to now (not completed)', icon: <CalendarDays/> },
    { id: 'timeInStatus', label: 'Time in status', description: 'Time spent in status', icon: <Layers3/>, children: [
      ...stateTypes.map(type => ({ id: `timeInStatus:type:${type}`, label: titleCase(type), checked: config.timeInStatusIds.includes(`type:${type}`), icon: <CircleDot/> })),
      ...data.states.map(state => ({ id: `timeInStatus:${state.id}`, label: state.name, checked: config.timeInStatusIds.includes(state.id), icon: <i className={styles.optionDot} style={{ backgroundColor: state.color }}/> })),
    ] },
  ]
}

function dimensionOptions(data: BootstrapData, includeDates: boolean): InsightOption[] {
  const issueGroups = data.labelGroups.filter(group => group.resourceType === 'issue' && !group.archivedAt)
  const projectGroups = data.labelGroups.filter(group => group.resourceType === 'project' && !group.archivedAt)
  const options: InsightOption[] = [
    { id: 'status', label: 'Status', icon: <CircleDot/> }, { id: 'statusType', label: 'Status type', icon: <CircleDot/> },
    { id: 'assignee', label: 'Assignee', icon: <UserRound/> }, { id: 'agent', label: 'Agent', icon: <Bot/> },
    { id: 'agentSession', label: 'Agent session', icon: <Bot/> }, { id: 'creator', label: 'Creator', icon: <UserRound/> },
    { id: 'priority', label: 'Priority', icon: <Flame/> }, { id: 'label', label: 'Label', icon: <Tag/> },
    { id: 'labelGroup', label: 'Label group', icon: <Layers3/>, children: issueGroups.map(group => ({ id: `labelGroup:${group.id}`, label: group.name, icon: <i className={styles.optionDot} style={{ backgroundColor: group.color }}/> })) },
    { id: 'template', label: 'Template', icon: <Copy/> }, { id: 'externalSource', label: 'External source', icon: <Link2/> },
    { id: 'project', label: 'Project', separatorBefore: true, icon: <FolderKanban/> }, { id: 'initiative', label: 'Initiative', icon: <Layers3/> },
    { id: 'projectLabel', label: 'Project label', icon: <Tag/> },
    { id: 'projectLabelGroup', label: 'Project label group', icon: <Layers3/>, children: projectGroups.map(group => ({ id: `projectLabelGroup:${group.id}`, label: group.name, icon: <i className={styles.optionDot} style={{ backgroundColor: group.color }}/> })) },
    { id: 'cycle', label: 'Cycle', icon: <CycleIcon/> }, { id: 'addedToCycle', label: 'Added to cycle', icon: <CalendarDays/> },
  ]
  if (includeDates) options.push(
    { id: 'createdDate', label: 'Created date', separatorBefore: true, icon: <CalendarDays/> }, { id: 'completedDate', label: 'Completed date', icon: <CalendarDays/> },
    { id: 'canceledDate', label: 'Canceled date', icon: <CalendarDays/> }, { id: 'startedDate', label: 'Started date', icon: <CalendarDays/> },
    { id: 'dueDate', label: 'Due date', icon: <CalendarDays/> }, { id: 'burnUp', label: 'Burn-up', separatorBefore: true, icon: <BarChart3/> },
  )
  return options
}

function segmentOptions(data: BootstrapData): InsightOption[] {
  const allowed = new Set(['assignee','agent','agentSession','creator','priority','label','labelGroup','template','externalSource','project','initiative','projectLabel','projectLabelGroup','addedToCycle'])
  return [{ id: 'none', label: 'No Value' }, ...dimensionOptions(data, false).filter(option => allowed.has(option.id)).map((option, index) => ({ ...option, separatorBefore: index === 0 || option.id === 'project' }))]
}

function measureLabel(value: SavedViewInsightMeasure) { return ({ issueCount: 'Issue count', cycleTime: 'Cycle time', leadTime: 'Lead time', issueAge: 'Issue age', timeInStatus: 'Time in status' })[value] }
function dimensionLabel(value: SavedViewInsightDimension, data: BootstrapData) { return findInsightOption(dimensionOptions(data, true), value)?.label ?? value }
function isMeasure(value: unknown): value is SavedViewInsightMeasure { return typeof value === 'string' && ['issueCount','cycleTime','leadTime','issueAge','timeInStatus'].includes(value) }
function isDimension(value: unknown): value is SavedViewInsightDimension { return typeof value === 'string' && (['status','statusType','assignee','agent','agentSession','creator','priority','label','template','externalSource','project','initiative','projectLabel','cycle','addedToCycle','createdDate','completedDate','canceledDate','startedDate','dueDate','burnUp'].includes(value) || value.startsWith('labelGroup:') || value.startsWith('projectLabelGroup:')) }
function findInsightOption(options: InsightOption[], value: string): InsightOption | undefined { for (const option of options) { if (option.id === value) return option; const child = findInsightOption(option.children ?? [], value); if (child) return child } }
function toggleValue(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
function exportInsightsCsv(insight: InsightData, config: SavedViewInsightsConfig, name: string) {
  const latency = config.measure !== 'issueCount'
  const aggregations = config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median','p75','p95'] as const)
  const headers = [dimensionLabelFallback(config.slice), 'Issue count', ...(latency ? aggregations.map(aggregation => `${measureLabel(config.measure)} (${aggregationLabels[aggregation]})`) : config.segment === 'none' ? [] : insight.segments.map(segment => segment.label))]
  const lines = [headers, ...insight.rows.map(row => [row.label, latency ? row.values.length : row.total, ...(latency ? aggregations.map(aggregation => formatMetric(row.aggregations[aggregation] ?? 0, config.measure)) : config.segment === 'none' ? [] : insight.segments.map(segment => String(row.segments[segment.id] ?? 0)))])]
  const csv = lines.map(line => line.map(value => `"${String(value).replaceAll('"', '""')}"`).join(',')).join('\n')
  const link = document.createElement('a')
  link.href = `data:text/csv;charset=utf-8,${encodeURIComponent(`\uFEFF${csv}`)}`
  link.download = `${name.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'insights'}-insights.csv`
  link.hidden = true
  document.body.append(link)
  link.click()
  window.setTimeout(() => link.remove(), 1_000)
}
function dimensionLabelFallback(value: SavedViewInsightDimension) { const match = value.match(/^[^:]+/); return titleCase(match?.[0] ?? value) }
function SummaryMark({ color, kind }: { color?: string; kind: string }) { return kind === 'assignee' ? <span className={styles.avatarMark} style={{ backgroundColor: color }}>•</span> : kind === 'project' ? <span className={styles.projectMark}>◇</span> : <i className={styles.dot} style={{ backgroundColor: color }}/>}
function InsightOwnerAvatar({ user }: { user?: User }) { return <UserAvatar avatarUrl={user?.avatarUrl} className={styles.avatar} name={user?.displayName ?? 'Unknown user'}/> }
