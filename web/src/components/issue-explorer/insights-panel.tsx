import { useCallback, useEffect, useMemo, useState, type ReactElement, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { BarChart3, Bot, CalendarDays, Check, ChevronDown, CircleDot, Clock3, Copy, Download, Ellipsis, Expand, Flame, FolderKanban, History, Layers3, Link2, Palette, RefreshCw, Search, SlidersHorizontal, Tag, UserRound, X } from 'lucide-react'
import { toast } from 'sonner'
import { CycleIcon } from '@/components/issue/issue-icons'
import { MyIssuesList, type MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { BootstrapData, SavedView } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { SelectControl } from '@/components/ui/select-control'
import { Toggle } from '@/components/ui/toggle'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { FilterIcon, SidebarIcon } from '@/components/ui/view-action-icons'
import type { IssueQueryInput } from '@/lib/api'
import { useInsightSource } from './use-insight-source'
import { aggregateInsightValues, aggregationLabels, insightTargetMatches, sameInsightTarget, type InsightTarget, type InsightAggregation } from './insight-interaction'
import { buildInsightData, formatMetric, isEmptyInsightValue, titleCase, type InsightData } from './insight-data'
import { hideableDimension, insightsConfigKey, parseInsightsConfig, type SavedViewInsightDimension, type SavedViewInsightMeasure, type SavedViewInsightsConfig } from './insight-config'
import { InsightExplorer, InsightValueIcon } from './insight-explorer'
import styles from './insights-panel.module.css'

/** The page's filter menu, mirrored by the fullscreen view's round filter button (Linear). */
export interface InsightFilterControl {
  filters: MyIssuesAppliedFilter[]
  options?: (field: MyIssuesFilterKey) => MyIssuesFilterOption[] | undefined
  onToggle: (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => void
  onAdvanced?: () => void
}

const DOCS_URL = 'https://flow.app/docs/insights'

export function SavedViewInsightsPanel({ allRows, data, onClose, onSave, rows, view, query, onDrillChange, onOpenIssue, viewTitle, viewIcon, canSetDefault = true, filterControl }: {
  allRows: MyIssuesRowData[]
  data: BootstrapData
  onClose: () => void
  /** "Set default for everyone": stores the configuration as the view's shared default. */
  onSave: (config: SavedViewInsightsConfig) => Promise<void>
  rows: MyIssuesRowData[]
  view: SavedView
  query?: IssueQueryInput
  onDrillChange?: (rows: MyIssuesRowData[] | undefined) => void
  onOpenIssue?: (row: MyIssuesRowData) => void
  /** Fullscreen breadcrumb (Linear: the view tab or saved view name). */
  viewTitle?: string
  viewIcon?: ReactNode
  /** Hides "Set default for everyone" for viewers who cannot change the shared default. */
  canSetDefault?: boolean
  filterControl?: InsightFilterControl
}) {
  const { t } = useI18n()
  const sharedSource = JSON.stringify(view.insights ?? {})
  const shared = useMemo(() => parseInsightsConfig(JSON.parse(sharedSource) as Record<string, unknown>), [sharedSource])
  // Linear: each person's own Measure/Slice/Segment choices override the view's shared default.
  const personalKey = `flow:saved-view:${view.id}:insights`
  const readPersonal = useCallback(() => {
    try { const stored = localStorage.getItem(personalKey); return stored ? parseInsightsConfig(JSON.parse(stored) as Record<string, unknown>, shared) : undefined } catch { return undefined }
  }, [personalKey, shared])
  const [config, setConfigState] = useState(() => readPersonal() ?? shared)
  const [saving, setSaving] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [target, setTarget] = useState<InsightTarget>()
  const introKey = `flow:saved-view:${view.id}:insights-intro`
  const [showIntro, setShowIntro] = useState(() => localStorage.getItem(introKey) !== 'dismissed')
  const [refreshKey, setRefreshKey] = useState(0)
  useEffect(() => setConfigState(readPersonal() ?? shared), [readPersonal, shared])
  useEffect(() => setShowIntro(localStorage.getItem(introKey) !== 'dismissed'), [introKey])
  const sharedKey = insightsConfigKey(shared)
  const setConfig = (update: (current: SavedViewInsightsConfig) => SavedViewInsightsConfig) => setConfigState(current => {
    const next = update(current)
    try { if (insightsConfigKey(next) === sharedKey) localStorage.removeItem(personalKey); else localStorage.setItem(personalKey, JSON.stringify(next)) } catch { /* private mode: the choice lasts for this visit */ }
    return next
  })
  const patchConfig = (patch: Partial<SavedViewInsightsConfig>) => setConfig(value => ({ ...value, ...patch }))
  const remote = useInsightSource(data, query ? { ...query, archived: config.showArchived ? 'all' : 'false', includeStatusHistory: config.measure === 'timeInStatus' } : undefined, refreshKey)
  const source = query ? remote.rows : config.showArchived ? allRows : rows
  const insight = useMemo(() => { void refreshKey; return buildInsightData(source, config, data) }, [config, data, refreshKey, source])
  const dirty = insightsConfigKey(config) !== sharedKey
  const save = async () => {
    setSaving(true)
    try { await onSave(config); try { localStorage.removeItem(personalKey) } catch { /* ignore */ } toast.success(t('Insight saved as the default for everyone')) }
    catch { toast.error(t('Could not save the insight default')) }
    finally { setSaving(false) }
  }
  const dismissIntro = () => { localStorage.setItem(introKey, 'dismissed'); setShowIntro(false) }
  const copyLink = () => void navigator.clipboard.writeText(window.location.href).then(() => toast.success(t('View link copied')))
  const sliceLabel = dimensionLabel(config.slice, data)
  const copyMarkdown = () => void navigator.clipboard.writeText(insightsMarkdown(insight, config, t(sliceLabel), t)).then(() => toast.success(t('Insights copied as Markdown')))
  const exportCsv = () => exportInsightsCsv(insight, config, view.name)
  const refresh = () => setRefreshKey(value => value + 1)
  const updateMeasure = (value: string) => setConfig(current => ({ ...current, ...(value.startsWith('timeInStatus:') ? { measure: 'timeInStatus' as const, timeInStatusIds: toggleValue(current.timeInStatusIds, value.slice(13)) } : { measure: value as SavedViewInsightMeasure }) }))
  const selectedSlice = insight.rows.find(item => item.id === target?.slice)
  const selectedSegment = insight.segments.find(item => item.id === target?.segment)
  const selectedRows = useMemo(() => insight.samples.filter(sample => !target || insightTargetMatches(sample, target)).map(sample => sample.item), [insight, target])
  const issueCount = target ? selectedRows.length : insight.samples.length
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
  // Fullscreen: Escape first returns from a selection to the settings panel, then closes fullscreen.
  useEffect(() => {
    if (!expanded) { document.body.style.overflow = ''; return }
    document.body.style.overflow = 'hidden'
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || document.querySelector('[data-radix-popper-content-wrapper], [role="dialog"]')) return
      if (target) setTarget(undefined)
      else setExpanded(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => { window.removeEventListener('keydown', onKeyDown); document.body.style.overflow = '' }
  }, [expanded, target])
  const selectTarget = (next: InsightTarget) => setTarget(current => sameInsightTarget(current, next) ? undefined : next)
  const noun = t(issueCount === 1 ? 'issue' : 'issues')
  const phrase = target ? selectionPhrase(target, config, data, t, selectedSlice?.label, selectedSegment?.label) : ''
  const content = remote.error
    ? <div role="alert" className={styles.state}><span>{t('Could not load insights')}</span><button type="button" onClick={refresh}>{t('Retry')}</button></div>
    : remote.loading ? <div role="status" className={styles.state}>{t('Loading…')}</div>
    : !insight.rows.length ? <div role="status" className={styles.state}><span className={styles.emptyMark} aria-hidden="true"><BarChart3/></span><span>{t('No matching issues')}</span></div>
    : <InsightExplorer onOpenIssue={onOpenIssue} sliceLabel={sliceLabel} config={config} data={data} insight={insight} expanded={expanded} target={target} onSelect={selectTarget} onClear={() => setTarget(undefined)}/>
  const actions = <InsightActionsMenu copyLink={copyLink} copyMarkdown={copyMarkdown} exportCsv={exportCsv} onRefresh={refresh}/>
  const setDefault = canSetDefault && <footer className={styles.footer}><button type="button" className={styles.setDefault} aria-label={t('Save current insight')} disabled={!dirty || saving} onClick={() => void save()}>{t(saving ? 'Saving…' : 'Set default for everyone')}</button></footer>
  const icon = viewIcon ?? <ViewGlyph color={view.color} icon={view.icon}/>

  if (expanded) return <TooltipProvider delayDuration={450} skipDelayDuration={300}><aside aria-label={t('View insights')} className={styles.fullscreen} data-selection={target ? true : undefined}>
    <header className={styles.fullscreenHeader}>
      <div className={styles.crumbs}>
        <span className={styles.crumbIcon} aria-hidden="true">{icon}</span>
        <strong data-i18n-ignore>{viewTitle ?? view.name}</strong>
        <span className={styles.crumbSeparator} aria-hidden="true">›</span>
        <h3>{t('Insights')}</h3>
      </div>
      {actions}
      <span className={styles.spacer}/>
      <FlowTooltip label={t('Close fullscreen')}><button aria-label={t('Close fullscreen')} className={styles.iconButton} onClick={() => setExpanded(false)} type="button"><X size={14}/></button></FlowTooltip>
    </header>
    <div className={styles.fullscreenBody}>
      <section className={styles.fullscreenMain} aria-label={t('Insight chart')}>
        <header className={styles.summary}>
          <strong aria-live="polite">{remote.loading ? <span className={styles.summaryNoun}>{t('Loading…')}</span> : <><span className={styles.summaryCount}>{issueCount}</span>{' '}<span className={styles.summaryNoun}>{noun}</span></>}</strong>
          {filterControl && <InsightFilterButton control={filterControl} onAdvanced={filterControl.onAdvanced ? () => { setExpanded(false); filterControl.onAdvanced?.() } : undefined}/>}
        </header>
        {content}
      </section>
      {target ? <section className={styles.selection} aria-label={t('Selected issues')}>
        <header className={styles.selectionHeader}>
          <div className={styles.selectionTitle}>
            {selectedSlice && <><InsightValueIcon dimension={config.slice} id={selectedSlice.id} color={selectedSlice.color} data={data}/><span data-i18n-ignore>{t(selectedSlice.label)}</span></>}
            {selectedSlice && (selectedSegment || target.aggregation) && <span aria-hidden="true">·</span>}
            {selectedSegment && config.segment !== 'none' && <><InsightValueIcon dimension={config.segment} id={selectedSegment.id} color={selectedSegment.color} data={data} size={16}/><span data-i18n-ignore>{t(selectedSegment.label)}</span></>}
            {target.aggregation && <span>{`${aggregationLabels[target.aggregation as InsightAggregation]} ${target.operator === 'gt' ? '>' : '≤'} ${formatMetric(target.threshold ?? 0, config.measure)}`}</span>}
          </div>
          <span className={styles.spacer}/>
          <FlowTooltip label={t('Close panel')} shortcut="Esc"><button aria-label={t('Close panel')} className={styles.iconButton} onClick={() => setTarget(undefined)} type="button"><SidebarIcon width={14} height={14}/></button></FlowTooltip>
        </header>
        <div className={styles.selectionList}><MyIssuesList hideGroupHeaders groups={[{ id: 'insight-selection', label: t('Selected issues'), issues: selectedRows }]} displayProperties={SELECTION_PROPERTIES} onOpenIssue={onOpenIssue}/></div>
      </section> : <aside className={styles.settings} aria-label={t('Insight settings')}>
        <div className={styles.settingsBody}>
          <InsightControls config={config} data={data} layout="row" onMeasure={updateMeasure} onChange={patchConfig}/>
          <div className={styles.settingsDivider}/>
          <div className={styles.settingsToggles}><InsightOptions config={config} data={data} onChange={patchConfig}/></div>
        </div>
        {setDefault}
      </aside>}
    </div>
  </aside></TooltipProvider>

  return <aside aria-label={t('View insights')} className={styles.panel}>
    {showIntro && <section className={styles.intro}>
      <p>{t('Insights makes it easy to analyze issue data. Create reports to reveal trends and find outlier issues that need attention.')}</p>
      <button aria-label={t('Dismiss insights introduction')} className={styles.iconButton} onClick={dismissIntro} type="button"><X size={14}/></button>
      <a href={DOCS_URL} rel="noreferrer" target="_blank">{t('Documentation')}</a>
    </section>}
    <section className={styles.card}>
      <header className={styles.cardHeader}>
        <strong aria-live="polite">{remote.loading ? <span className={styles.summaryNoun}>{t('Loading…')}</span> : <><span className={styles.summaryCount}>{issueCount}</span>{' '}<span className={styles.summaryNoun}>{phrase ? `${noun} ${phrase}` : noun}</span></>}</strong>
        <div className={styles.cardActions}>
          <ScopedTooltip label={t('Expand to fullscreen')}><button aria-label={t('Expand to fullscreen')} className={styles.iconButton} type="button" onClick={() => setExpanded(true)}><Expand size={14}/></button></ScopedTooltip>
          <InsightDisplayMenu config={config} data={data} onChange={patchConfig}/>
          {actions}
          <button aria-label={t('Close view insights')} className={`${styles.iconButton} ${styles.panelClose}`} onClick={onClose} type="button"><X size={14}/></button>
        </div>
      </header>
      <div className={styles.controls}><InsightControls config={config} data={data} layout="column" onMeasure={updateMeasure} onChange={patchConfig}/></div>
      <div className={styles.cardBody}>{content}</div>
      {setDefault}
    </section>
  </aside>
}

const SELECTION_PROPERTIES = new Set<MyIssuesProperty>(['priority', 'id', 'status', 'assignee'])

function ScopedTooltip({ label, children }: { label: string; children: ReactElement }) {
  return <TooltipProvider delayDuration={450} skipDelayDuration={300}><FlowTooltip label={label}>{children}</FlowTooltip></TooltipProvider>
}

export function InsightHiddenNotice({ hidden, onShow }: { hidden: number; onShow: () => void }) {
  const { t } = useI18n()
  if (hidden <= 0) return null
  return <div className={styles.drillNotice}><span>{hidden} {t('issues hidden by display options')}</span><button type="button" onClick={onShow}>{t('Show hidden issues')}</button></div>
}

function InsightFilterButton({ control, onAdvanced }: { control: InsightFilterControl; onAdvanced?: () => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  return <MyIssuesFilterMenu open={open} onOpenChange={setOpen} filters={control.filters} options={control.options} onToggle={control.onToggle} onAdvanced={onAdvanced} align="end"
    trigger={<button type="button" aria-label={t('Add filter')} className={styles.filterButton}><FilterIcon size={14}/></button>}/>
}

function InsightControls({ config, data, layout, onMeasure, onChange }: { config: SavedViewInsightsConfig; data: BootstrapData; layout: 'row' | 'column'; onMeasure: (value: string) => void; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  return <>
    <InsightPicker layout={layout} label="Measure" value={config.measure} options={measureOptions(data, config)} onChange={onMeasure}/>
    <InsightPicker layout={layout} label="Slice" value={config.slice} options={dimensionOptions(data, true)} onChange={slice => onChange({ slice: slice as SavedViewInsightDimension })}/>
    {config.measure === 'issueCount'
      ? <InsightPicker layout={layout} label="Segment" value={config.segment} options={segmentOptions(data)} onChange={segment => onChange({ segment: segment as SavedViewInsightsConfig['segment'] })}/>
      : <InsightAggregationPicker layout={layout} config={config} onChange={aggregations => onChange({ aggregations })}/>}
  </>
}

/** Linear's display options: archived issues, hiding the empty segment value, and the chart scale/colours. */
function InsightOptions({ config, data, onChange }: { config: SavedViewInsightsConfig; data: BootstrapData; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  const { t } = useI18n()
  const hideable = hideableDimension(config)
  return <>
    <label className={styles.option}><span>{t('Show archived issues')}</span><Toggle label={t('Show archived issues')} checked={config.showArchived} onChange={showArchived => onChange({ showArchived })}/></label>
    {hideable && <label className={styles.option}><span>{t('Hide')}<span className={styles.chip}>{t('No {value}').replace('{value}', t(dimensionLabel(hideable, data)))}</span></span><Toggle label={`${t('Hide')} ${t('No {value}').replace('{value}', t(dimensionLabel(hideable, data)))}`} checked={Boolean(config.hideEmptySegment)} onChange={hideEmptySegment => onChange({ hideEmptySegment })}/></label>}
    {config.measure !== 'issueCount' && <div className={styles.option}><span>{t('Y-axis scale')}</span><SelectControl className={styles.optionSelect} label={t('Y-axis scale')} value={config.latencyScale ?? 'log'} options={[{ value: 'log', label: t('Log scale') }, { value: 'linear', label: t('Linear scale') }]} onChange={value => onChange({ latencyScale: value as 'log' | 'linear' })}/></div>}
    {config.segment === 'none' && config.measure === 'issueCount' && <div className={styles.option}><span>{t('Colors')}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Colors')} className={styles.optionSelect} role="combobox" type="button"><Palette/>{t(config.colors === 'status' ? 'Status colors' : 'Auto-color')}<ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className={styles.menu} sideOffset={4}><DropdownMenu.RadioGroup value={config.colors} onValueChange={colors => onChange({ colors: colors as SavedViewInsightsConfig['colors'] })}><DropdownMenu.RadioItem className={styles.menuItem} value="status">{t('Status colors')}{config.colors === 'status' && <Check className={styles.trailingCheck}/>}</DropdownMenu.RadioItem><DropdownMenu.RadioItem className={styles.menuItem} value="auto">{t('Auto-color')}{config.colors === 'auto' && <Check className={styles.trailingCheck}/>}</DropdownMenu.RadioItem></DropdownMenu.RadioGroup></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>}
  </>
}

function InsightDisplayMenu({ config, data, onChange }: { config: SavedViewInsightsConfig; data: BootstrapData; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  const { t } = useI18n()
  return <Popover.Root><ScopedTooltip label={t('Insights display options')}><Popover.Trigger asChild><button aria-label={t('Insights display options')} className={styles.iconButton} type="button"><SlidersHorizontal size={14}/></button></Popover.Trigger></ScopedTooltip><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className={styles.displayPopover} collisionPadding={8} sideOffset={4}>
    <InsightOptions config={config} data={data} onChange={onChange}/>
  </Popover.Content></Popover.Portal></Popover.Root>
}

function InsightAggregationPicker({ config, layout, onChange }: { config: SavedViewInsightsConfig; layout: 'row' | 'column'; onChange: (values: InsightAggregation[]) => void }) {
  const { t } = useI18n()
  const selected = config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as InsightAggregation[])
  return <label className={styles.picker} data-layout={layout}><span>{t('Aggregations')}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" aria-label={t('Aggregations')} className={styles.select}><span>{t('Percentiles')}</span><SelectChevron/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" className={styles.menu} sideOffset={4}>{(['median', 'p75', 'p95'] as const).map(id => <DropdownMenu.CheckboxItem className={styles.menuItem} key={id} checked={selected.includes(id)} disabled={selected.length === 1 && selected.includes(id)} onSelect={event => event.preventDefault()} onCheckedChange={checked => onChange(checked ? [...selected, id] : selected.filter(value => value !== id))}><span>{id === 'median' ? 'P50' : aggregationLabels[id]}</span>{selected.includes(id) && <Check size={13}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function InsightActionsMenu({ copyLink, copyMarkdown, exportCsv, onRefresh }: { copyLink: () => void; copyMarkdown: () => void; exportCsv: () => void; onRefresh: () => void }) {
  const { t } = useI18n()
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Open menu')} className={styles.iconButton} type="button"><Ellipsis size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className={styles.menu} collisionPadding={8} sideOffset={4}>
    <DropdownMenu.Item className={styles.menuItem} onSelect={copyLink}><Link2/>{t('Copy link')}</DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={copyMarkdown}><Copy/>{t('Copy insights as Markdown')}</DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={exportCsv}><Download/>{t('Export insights as CSV…')}</DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={() => window.open(DOCS_URL, '_blank', 'noopener,noreferrer')}><BarChart3/>{t('Insights documentation')}</DropdownMenu.Item>
    <DropdownMenu.Separator className={styles.menuSeparator}/>
    <DropdownMenu.Item className={styles.menuItem} onSelect={onRefresh}><RefreshCw/>{t('Refresh')}</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

type InsightOption = { id: string; label: string; description?: string; separatorBefore?: boolean; icon?: ReactNode; checked?: boolean; children?: InsightOption[] }

function SelectChevron() { return <span className={styles.selectChevron} aria-hidden="true"><svg width="10" height="5" viewBox="0 0 10 5"><path d="M1 .5 5 4.5 9 .5" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25"/></svg></span> }

function InsightPicker({ label, layout, onChange, options, value }: { label: string; layout: 'row' | 'column'; onChange: (value: string) => void; options: InsightOption[]; value: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const command = usePropertyCommand({ open, options, selectedIds: [value], onOpenChange: setOpen, onSelect: option => onChange(option.id) })
  const selected = findInsightOption(options, value)?.label ?? value
  return <label className={styles.picker} data-layout={layout}><span>{t(label)}</span><DropdownMenu.Root open={open} onOpenChange={setOpen}><DropdownMenu.Trigger asChild><button aria-label={t(label)} className={styles.select} type="button"><span>{t(selected)}</span><SelectChevron/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className={`${styles.menu} ${styles.selectMenu}`} collisionPadding={8} onKeyDown={command.onKeyDown} sideOffset={4}>
    <div className={styles.menuSearch}><Search/><input aria-label={t('Filter…')} autoFocus ref={command.inputRef} onChange={event => command.onQueryChange(event.target.value)} onKeyDown={event => { command.onKeyDown(event); event.stopPropagation() }} placeholder={t('Filter…')} value={command.query}/></div>
    <div className={styles.menuScroll} role="listbox">{command.filteredOptions.map(option => <InsightPickerOption key={option.id} onChange={next => { onChange(next); if (!option.children?.length) setOpen(false) }} option={option} value={value}/>)}</div>
    {!command.filteredOptions.length && <div className={styles.menuEmpty}>{t('No results')}</div>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function InsightPickerOption({ onChange, option, value }: { onChange: (value: string) => void; option: InsightOption; value: string }) {
  const { t } = useI18n()
  const item = <><span className={styles.menuCopy} title={option.description ? t(option.description) : undefined}><b>{t(option.label)}</b></span>{option.id === value && <Check className={styles.trailingCheck}/>}</>
  return <>{option.separatorBefore && <DropdownMenu.Separator className={styles.menuSeparator}/>}{option.children?.length ? <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}>{item}<span className={styles.menuChevron}>▶</span></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" className={`${styles.menu} ${styles.subMenu}`} sideOffset={4}>{option.children.map(child => <DropdownMenu.CheckboxItem checked={child.checked ?? child.id === value} className={styles.menuItem} key={child.id} onSelect={event => { if (child.id.startsWith('timeInStatus:')) event.preventDefault(); onChange(child.id) }}>{child.icon && <span className={styles.menuIcon}>{child.icon}</span>}<span data-i18n-ignore>{child.label}</span>{(child.checked ?? child.id === value) && <Check className={styles.trailingCheck}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub> : <DropdownMenu.Item className={styles.menuItem} onSelect={() => onChange(option.id)}>{item}</DropdownMenu.Item>}</>
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
  const allowed = new Set(['assignee', 'agent', 'agentSession', 'creator', 'priority', 'label', 'labelGroup', 'template', 'externalSource', 'project', 'initiative', 'projectLabel', 'projectLabelGroup', 'addedToCycle'])
  return [{ id: 'none', label: 'No value' }, ...dimensionOptions(data, false).filter(option => allowed.has(option.id)).map((option, index) => ({ ...option, separatorBefore: index === 0 || option.id === 'project' }))]
}

/** Linear's header sentence: "4 issues in Todo without priority", "1 issue in Backlog in low priority". */
function selectionPhrase(target: InsightTarget, config: SavedViewInsightsConfig, data: BootstrapData, t: (source: string) => string, sliceLabel?: string, segmentLabel?: string) {
  const parts: string[] = []
  if (sliceLabel) parts.push(t('in {value}').replace('{value}', t(sliceLabel)))
  if (config.segment !== 'none' && target.segment !== undefined) {
    const dimension = t(dimensionLabel(config.segment, data)).toLowerCase()
    if (isEmptyInsightValue(config.segment, target.segment)) parts.push(t('without {value}').replace('{value}', dimension))
    else if (config.segment === 'priority' && segmentLabel) parts.push(t('in {value} priority').replace('{value}', t(segmentLabel).toLowerCase()))
    else if (segmentLabel) parts.push(t('with {value}').replace('{value}', t(segmentLabel)))
  }
  if (target.aggregation) parts.push(`${aggregationLabels[target.aggregation as InsightAggregation]} ${target.operator === 'gt' ? '>' : '≤'} ${formatMetric(target.threshold ?? 0, config.measure)}`)
  return parts.join(' ')
}

function measureLabel(value: SavedViewInsightMeasure) { return ({ issueCount: 'Issue count', cycleTime: 'Cycle time', leadTime: 'Lead time', issueAge: 'Issue age', timeInStatus: 'Time in status' })[value] }
function dimensionLabel(value: SavedViewInsightDimension, data: BootstrapData) { return findInsightOption(dimensionOptions(data, true), value)?.label ?? value }
function findInsightOption(options: InsightOption[], value: string): InsightOption | undefined { for (const option of options) { if (option.id === value) return option; const child = findInsightOption(option.children ?? [], value); if (child) return child } }
function toggleValue(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }

function insightTableLines(insight: InsightData, config: SavedViewInsightsConfig, sliceHeader: string) {
  const latency = config.measure !== 'issueCount'
  const aggregations = config.aggregations ?? (config.aggregation ? [config.aggregation] : ['median', 'p75', 'p95'] as const)
  const headers = [sliceHeader, 'Issue count', ...(latency ? aggregations.map(aggregation => `${measureLabel(config.measure)} (${aggregationLabels[aggregation]})`) : config.segment === 'none' ? [] : insight.segments.map(segment => segment.label))]
  return [headers, ...insight.rows.map(row => [row.label, String(latency ? row.values.length : row.total), ...(latency ? aggregations.map(aggregation => formatMetric(row.aggregations[aggregation] ?? 0, config.measure)) : config.segment === 'none' ? [] : insight.segments.map(segment => String(row.segments[segment.id] ?? 0)))])]
}

function insightsMarkdown(insight: InsightData, config: SavedViewInsightsConfig, sliceHeader: string, t: (source: string) => string) {
  const [headers, ...body] = insightTableLines(insight, config, sliceHeader)
  const cell = (value: string) => t(value).replaceAll('|', '\\|')
  return [`| ${headers.map(cell).join(' | ')} |`, `| ${headers.map(() => '---').join(' | ')} |`, ...body.map(line => `| ${line.map(cell).join(' | ')} |`)].join('\n')
}

function exportInsightsCsv(insight: InsightData, config: SavedViewInsightsConfig, name: string) {
  const lines = insightTableLines(insight, config, dimensionLabelFallback(config.slice))
  const csv = lines.map(line => line.map(value => `"${String(value).replaceAll('"', '""')}"`).join(',')).join('\n')
  const link = document.createElement('a')
  link.href = `data:text/csv;charset=utf-8,${encodeURIComponent(`﻿${csv}`)}`
  link.download = `${name.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'insights'}-insights.csv`
  link.hidden = true
  document.body.append(link)
  link.click()
  window.setTimeout(() => link.remove(), 1_000)
}
function dimensionLabelFallback(value: SavedViewInsightDimension) { const match = value.match(/^[^:]+/); return titleCase(match?.[0] ?? value) }
