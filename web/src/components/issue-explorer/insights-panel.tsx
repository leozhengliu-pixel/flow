import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactElement, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { BarChart3, ChevronDown, Palette, X } from 'lucide-react'
import { toast } from 'sonner'
import { FlowOptionsIcon, FlowUrlIcon } from '@/components/issue/flow-header-icons'
import { MyIssuesList, type MyIssuesRowData } from '@/components/my-issues/my-issues-list'
import type { MyIssuesProperty } from '@/components/my-issues/my-issues-surface'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import type { BootstrapData, SavedView } from '@/types/flow'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { Toggle } from '@/components/ui/toggle'
import { FlowTooltip, TooltipContent, TooltipProvider, TooltipRoot, TooltipTrigger } from '@/components/ui/tooltip'
import { DisplayIcon, FilterIcon, InsightsIcon, SidebarIcon } from '@/components/ui/view-action-icons'
import type { IssueQueryInput } from '@/lib/api'
import { useInsightSource } from './use-insight-source'
import { aggregateInsightValues, insightTargetMatches, sameInsightTarget, type InsightTarget, type InsightAggregation } from './insight-interaction'
import { buildInsightData, formatMetric, isEmptyInsightValue, titleCase, type InsightData } from './insight-data'
import { emptyDimensionLabel, insightsConfigKey, parseInsightsConfig, percentileLabel, percentileShare, selectedAggregations, type SavedViewInsightDimension, type SavedViewInsightMeasure, type SavedViewInsightsConfig } from './insight-config'
import { InsightExplorer, InsightValueIcon } from './insight-explorer'
import { InsightCheckIcon, InsightCloseIcon, InsightCopyIcon, InsightExpandIcon, InsightRefreshIcon, InsightSelectChevron, InsightTreeBranch } from './insight-icons'
import { INSIGHT_MENU_POSITION, INSIGHTS_FULLSCREEN_SHORTCUT, MEASURE_OPTIONS, dimensionOptions, timeInStatusTree, type InsightOption, type StatusNode } from './insight-options'
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
  const hasSharedDefault = sharedSource !== '{}'
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
  // Linear resets "Hide <No value>" whenever the slice or segment it belongs to changes.
  const patchConfig = (patch: Partial<SavedViewInsightsConfig>) => setConfig(value => ({
    ...value,
    ...(patch.slice !== undefined && patch.slice !== value.slice ? { hideEmptySlice: false } : {}),
    ...(patch.segment !== undefined && patch.segment !== value.segment ? { hideEmptySegment: false } : {}),
    ...patch,
  }))
  const remote = useInsightSource(data, query ? { ...query, archived: config.showArchived ? 'all' : 'false', includeStatusHistory: config.measure === 'timeInStatus' } : undefined, refreshKey)
  const source = query ? remote.rows : config.showArchived ? allRows : rows
  const insight = useMemo(() => { void refreshKey; return buildInsightData(source, config, data) }, [config, data, refreshKey, source])
  const personal = insightsConfigKey(config) !== sharedKey
  const save = async () => {
    // Linear asks before publishing the configuration to everyone.
    if (!await confirmAction(t('Save insight'), { description: t('Publishing the configuration will make it the default for this view for everyone in the workspace.'), confirmLabel: t('Save'), danger: false })) return
    setSaving(true)
    try { await onSave(config); try { localStorage.removeItem(personalKey) } catch { /* ignore */ } toast.success(t('Insight saved as the default for everyone')) }
    catch { toast.error(t('Could not save the insight default')) }
    finally { setSaving(false) }
  }
  const reset = () => { try { localStorage.removeItem(personalKey) } catch { /* ignore */ } setConfigState(shared) }
  const dismissIntro = () => { localStorage.setItem(introKey, 'dismissed'); setShowIntro(false) }
  const copyLink = () => void navigator.clipboard.writeText(window.location.href).then(() => toast.success(t('View link copied')))
  const sliceLabel = dimensionLabel(config.slice, data)
  const copyMarkdown = () => void navigator.clipboard.writeText(insightsMarkdown(insight, config, t(sliceLabel), t)).then(() => toast.success(t('Insights copied as Markdown')))
  const exportCsv = () => exportInsightsCsv(insight, config, view.name)
  const refresh = () => setRefreshKey(value => value + 1)
  // Linear: picking another measure clears the "Time in status" statuses; ticking a status switches to that measure.
  const selectMeasure = (measure: SavedViewInsightMeasure) => setConfig(current => current.measure === measure ? current : { ...current, measure, timeInStatusIds: [] })
  const toggleStatuses = (values: string[]) => setConfig(current => {
    const selected = current.measure === 'timeInStatus' ? current.timeInStatusIds : []
    const all = values.every(value => selected.includes(value))
    return { ...current, measure: 'timeInStatus', timeInStatusIds: all ? selected.filter(value => !values.includes(value)) : [...new Set([...selected, ...values])] }
  })
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
  // Linear's Ctrl ⇧ F toggles fullscreen from anywhere outside a text field.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!event.ctrlKey || !event.shiftKey || event.metaKey || event.altKey || event.code !== 'KeyF' || event.defaultPrevented || isTextEntry(event.target)) return
      event.preventDefault()
      setExpanded(value => !value)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
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
  const actions = <InsightActionsMenu align={expanded ? 'start' : 'end'} copyLink={copyLink} copyMarkdown={copyMarkdown} exportCsv={exportCsv} onRefresh={refresh}/>
  // Linear's footer: "Set default for everyone" while the view has no shared default or you changed it, "Reset" to drop your own changes.
  const showSetDefault = canSetDefault && (personal || !hasSharedDefault)
  const showReset = personal && hasSharedDefault
  const setDefault = (showSetDefault || showReset) && <footer className={styles.footer}>
    {showReset && <button type="button" className={styles.resetDefault} onClick={reset}>{t('Reset')}</button>}
    {showSetDefault && <button type="button" className={styles.setDefault} aria-label={t('Save current insight')} disabled={saving} onClick={() => void save()}>{t(saving ? 'Saving…' : 'Set default for everyone')}</button>}
  </footer>
  const icon = viewIcon ?? <ViewGlyph color={view.color} icon={view.icon}/>
  const controls = (layout: 'row' | 'column') => <InsightControls config={config} data={data} layout={layout} onMeasure={selectMeasure} onToggleStatuses={toggleStatuses} onChange={patchConfig}/>

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
      <FlowTooltip label={t('Close fullscreen')} shortcut="Esc" align="end"><button aria-label={t('Close fullscreen')} className={styles.iconButton} onClick={() => setExpanded(false)} type="button"><InsightCloseIcon/></button></FlowTooltip>
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
            {target.aggregation && <span>{`${percentileLabel(target.aggregation as InsightAggregation)} ${target.operator === 'gt' ? '>' : '≤'} ${formatMetric(target.threshold ?? 0, config.measure)}`}</span>}
          </div>
          <span className={styles.spacer}/>
          <FlowTooltip label={t('Close panel')} shortcut="Esc"><button aria-label={t('Close panel')} className={styles.iconButton} onClick={() => setTarget(undefined)} type="button"><SidebarIcon width={14} height={14}/></button></FlowTooltip>
        </header>
        <div className={styles.selectionList}><MyIssuesList hideGroupHeaders groups={[{ id: 'insight-selection', label: t('Selected issues'), issues: selectedRows }]} displayProperties={SELECTION_PROPERTIES} onOpenIssue={onOpenIssue}/></div>
      </section> : <aside className={styles.settings} aria-label={t('Insight settings')}>
        <div className={styles.settingsBody}>
          {controls('row')}
          <div className={styles.settingsDivider}/>
          <div className={styles.settingsToggles}><InsightOptions config={config} onChange={patchConfig}/></div>
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
        <strong aria-live="polite">{remote.loading ? <span className={styles.summaryNoun}>{t('Loading…')}</span> : <><span className={styles.summaryCount}>{issueCount}</span>{' '}<span className={styles.summaryNoun}>{noun}</span>{phrase && <span className={styles.summaryPhrase}>{` ${phrase}`}</span>}</>}</strong>
        <div className={styles.cardActions}>
          <ScopedTooltip label={t('Expand to fullscreen')} shortcut={INSIGHTS_FULLSCREEN_SHORTCUT}><button aria-label={t('Expand to fullscreen')} className={styles.iconButton} type="button" onClick={() => setExpanded(true)}><InsightExpandIcon/></button></ScopedTooltip>
          <InsightDisplayMenu config={config} onChange={patchConfig}/>
          {actions}
          <button aria-label={t('Close view insights')} className={`${styles.iconButton} ${styles.panelClose}`} onClick={onClose} type="button"><X size={14}/></button>
        </div>
      </header>
      <div className={styles.controls}>{controls('column')}</div>
      <div className={styles.cardBody}>{content}</div>
      {setDefault}
    </section>
  </aside>
}

const SELECTION_PROPERTIES = new Set<MyIssuesProperty>(['priority', 'id', 'status', 'assignee'])

function isTextEntry(target: EventTarget | null) {
  return target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
}

function ScopedTooltip({ label, shortcut, children }: { label: string; shortcut?: string; children: ReactElement }) {
  return <TooltipProvider delayDuration={450} skipDelayDuration={300}><FlowTooltip label={label} shortcut={shortcut} align="end">{children}</FlowTooltip></TooltipProvider>
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

function InsightControls({ config, data, layout, onMeasure, onToggleStatuses, onChange }: { config: SavedViewInsightsConfig; data: BootstrapData; layout: 'row' | 'column'; onMeasure: (measure: SavedViewInsightMeasure) => void; onToggleStatuses: (values: string[]) => void; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  const { t } = useI18n()
  const measure = measureDisplay(config, data, t)
  const segment = config.segment === 'none' ? { label: 'No value' } : undefined
  return <>
    <InsightPicker layout={layout} label="Measure" value={config.measure} display={measure} options={MEASURE_OPTIONS} onChange={value => onMeasure(value as SavedViewInsightMeasure)}
      submenu={id => id === 'timeInStatus' ? <TimeInStatusMenu config={config} data={data} onToggle={onToggleStatuses}/> : undefined}/>
    <InsightPicker layout={layout} label="Slice" value={config.slice} options={dimensionOptions(data, true)} onChange={slice => onChange({ slice: slice as SavedViewInsightDimension })}/>
    {config.measure === 'issueCount'
      ? <InsightPicker layout={layout} label="Segment" value={config.segment} display={segment} options={segmentOptions(data)} onChange={value => onChange({ segment: value as SavedViewInsightsConfig['segment'] })}/>
      : <InsightAggregationPicker layout={layout} config={config} onChange={aggregations => onChange({ aggregations })}/>}
  </>
}

/** Linear's display options: archived issues, "Hide" per dimension with an empty value, the log scale and the chart colours. */
function InsightOptions({ config, onChange }: { config: SavedViewInsightsConfig; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  const { t } = useI18n()
  const emptySlice = emptyDimensionLabel(config.slice)
  const emptySegment = config.measure === 'issueCount' ? emptyDimensionLabel(config.segment) : undefined
  const hideRow = (empty: string, checked: boolean, change: (value: boolean) => void) => <label className={styles.option}><span>{t('Hide')}<span className={styles.chip}>{t(empty)}</span></span><Toggle label={`${t('Hide')} ${t(empty)}`} checked={checked} onChange={change}/></label>
  return <>
    <label className={styles.option}><span>{t('Show archived issues')}</span><Toggle label={t('Show archived issues')} checked={config.showArchived} onChange={showArchived => onChange({ showArchived })}/></label>
    {emptySlice && hideRow(emptySlice, Boolean(config.hideEmptySlice), hideEmptySlice => onChange({ hideEmptySlice }))}
    {emptySegment && hideRow(emptySegment, Boolean(config.hideEmptySegment), hideEmptySegment => onChange({ hideEmptySegment }))}
    {config.measure !== 'issueCount' && <label className={styles.option}><span>{t('Use log scale')}</span><Toggle label={t('Use log scale')} checked={config.latencyScale !== 'linear'} onChange={log => onChange({ latencyScale: log ? 'log' : 'linear' })}/></label>}
    {config.segment === 'none' && config.measure === 'issueCount' && <div className={styles.option}><span>{t('Colors')}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Colors')} className={styles.optionSelect} role="combobox" type="button"><Palette/>{t(config.colors === 'status' ? 'Status colors' : 'Auto-color')}<ChevronDown/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content loop data-flow-motion="floating" align="end" className={styles.menu} sideOffset={4}><DropdownMenu.RadioGroup value={config.colors} onValueChange={colors => onChange({ colors: colors as SavedViewInsightsConfig['colors'] })}>{(['status', 'auto'] as const).map(value => <DropdownMenu.RadioItem className={styles.menuItem} data-selected={config.colors === value || undefined} key={value} value={value}><span className={styles.menuLabel}>{t(value === 'status' ? 'Status colors' : 'Auto-color')}</span>{config.colors === value && <InsightCheckIcon className={styles.menuCheck}/>}</DropdownMenu.RadioItem>)}</DropdownMenu.RadioGroup></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>}
  </>
}

function InsightDisplayMenu({ config, onChange }: { config: SavedViewInsightsConfig; onChange: (patch: Partial<SavedViewInsightsConfig>) => void }) {
  const { t } = useI18n()
  // Linear gives this button no tooltip.
  return <Popover.Root><Popover.Trigger asChild><button aria-label={t('Insights display options')} className={styles.iconButton} type="button"><DisplayIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className={styles.displayPopover} collisionPadding={8} sideOffset={4}>
    <InsightOptions config={config} onChange={onChange}/>
  </Popover.Content></Popover.Portal></Popover.Root>
}

/** A row's description tooltip, shown under the row like Linear's menu item tooltips. */
function MenuRowTooltip({ label }: { label?: string }) {
  const { t } = useI18n()
  if (!label) return null
  return <TooltipRoot><TooltipTrigger asChild><span aria-hidden="true" className={styles.menuTipArea}/></TooltipTrigger><TooltipContent side="bottom" sideOffset={0}><span className="flow-tooltip-copy">{t(label)}</span></TooltipContent></TooltipRoot>
}

function InsightAggregationPicker({ config, layout, onChange }: { config: SavedViewInsightsConfig; layout: 'row' | 'column'; onChange: (values: InsightAggregation[]) => void }) {
  const { t } = useI18n()
  const selected = selectedAggregations(config)
  const label = selected.length ? selected.map(percentileLabel).join(', ') : t('None')
  return <label className={styles.picker} data-layout={layout}><span>{t('Aggregations')}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" aria-label={t('Aggregations')} className={styles.select}><span>{label}</span><span className={styles.selectChevron}><InsightSelectChevron/></span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content loop data-flow-motion="floating" {...INSIGHT_MENU_POSITION} className={styles.menu} collisionPadding={8}><TooltipProvider delayDuration={450} skipDelayDuration={300}>
    {(['median', 'p75', 'p95'] as const).map(id => {
      const checked = selected.includes(id)
      return <DropdownMenu.CheckboxItem className={`${styles.menuItem} ${styles.treeItem}`} key={id} checked={checked} onSelect={event => event.preventDefault()} onCheckedChange={next => onChange(next ? [...selected, id] : selected.filter(value => value !== id))}>
        <MenuCheckbox/><span className={styles.treeLabel}><span className={styles.menuLabel}>{percentileLabel(id)}</span></span>
        <MenuRowTooltip label={t('{value}% of issues are at or below this point').replace('{value}', String(percentileShare(id)))}/>
      </DropdownMenu.CheckboxItem>
    })}
  </TooltipProvider></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function MenuCheckbox() { return <span className={styles.treeCheck} aria-hidden="true"><i><CheckboxMark/></i></span> }

function InsightActionsMenu({ align, copyLink, copyMarkdown, exportCsv, onRefresh }: { align: 'start' | 'end'; copyLink: () => void; copyMarkdown: () => void; exportCsv: () => void; onRefresh: () => void }) {
  const { t } = useI18n()
  // Linear: no tooltip; the menu hangs from the button's right edge in the panel and its left edge in fullscreen.
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Open menu')} className={styles.iconButton} type="button"><FlowOptionsIcon/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content loop data-flow-motion="floating" align={align} className={styles.menu} collisionPadding={8} sideOffset={4}>
    <DropdownMenu.Item className={styles.menuItem} onSelect={copyLink}><FlowUrlIcon/><span className={styles.menuLabel}>{t('Copy link')}</span></DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={copyMarkdown}><InsightCopyIcon/><span className={styles.menuLabel}>{t('Copy insights as Markdown')}</span></DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={exportCsv}><LinearGlyph name="exportCsv"/><span className={styles.menuLabel}>{t('Export insights as CSV…')}</span></DropdownMenu.Item>
    <DropdownMenu.Item className={styles.menuItem} onSelect={() => window.open(DOCS_URL, '_blank', 'noopener,noreferrer')}><InsightsIcon/><span className={styles.menuLabel}>{t('Insights documentation')}</span></DropdownMenu.Item>
    <DropdownMenu.Separator className={styles.menuSeparator}/>
    <DropdownMenu.Item className={styles.menuItem} onSelect={onRefresh}><InsightRefreshIcon/><span className={styles.menuLabel}>{t('Refresh')}</span></DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function InsightPicker({ label, layout, onChange, options, value, display, submenu }: {
  label: string; layout: 'row' | 'column'; onChange: (value: string) => void; options: InsightOption[]; value: string
  /** The trigger's text and icon when they differ from the selected option (Linear: "Time in Backlog", "No value"). */
  display?: { label: string; icon?: ReactNode }
  /** A custom submenu for an option (Linear's "Time in status" tree). */
  submenu?: (id: string) => ReactNode
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const content = useRef<HTMLDivElement>(null)
  useEffect(() => { if (open) setQuery('') }, [open])
  const normalized = query.trim().toLocaleLowerCase()
  const visible = normalized ? options.filter(option => t(option.label).toLocaleLowerCase().includes(normalized) || option.label.toLocaleLowerCase().includes(normalized) || option.children?.some(child => child.label.toLocaleLowerCase().includes(normalized))) : options
  const firstFocus = useRef(true)
  useEffect(() => {
    if (!open) { firstFocus.current = true; return }
    // Linear highlights the selected row when the menu opens, and the first match while you type.
    const frame = requestAnimationFrame(() => {
      const root = content.current
      const row = (firstFocus.current && root?.querySelector<HTMLElement>('[data-selected]')) || root?.querySelector<HTMLElement>('[role^="menuitem"]')
      firstFocus.current = false
      row?.focus()
    })
    return () => cancelAnimationFrame(frame)
  }, [open, normalized])
  // Linear's menus filter as you type (the field itself stays hidden).
  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.target instanceof HTMLInputElement) return
    if (event.key.length === 1 && event.key !== ' ' && !event.metaKey && !event.ctrlKey && !event.altKey) { event.preventDefault(); setQuery(current => current + event.key) }
    else if (event.key === 'Backspace' && query) { event.preventDefault(); setQuery(current => current.slice(0, -1)) }
  }
  const selected = display ?? { label: findInsightOption(options, value)?.label ?? value }
  return <label className={styles.picker} data-layout={layout}><span>{t(label)}</span><DropdownMenu.Root open={open} onOpenChange={setOpen}><DropdownMenu.Trigger asChild><button aria-label={t(label)} className={styles.select} type="button">{selected.icon && <span className={styles.selectIcon} aria-hidden="true">{selected.icon}</span>}<span data-i18n-ignore>{t(selected.label)}</span><span className={styles.selectChevron}><InsightSelectChevron/></span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content loop ref={content} data-flow-motion="floating" {...INSIGHT_MENU_POSITION} className={`${styles.menu} ${styles.selectMenu}`} collisionPadding={8} onKeyDown={onKeyDown} onCloseAutoFocus={event => event.preventDefault()}><TooltipProvider delayDuration={450} skipDelayDuration={300}>
    <span className={styles.menuSearch} role="status">{normalized ? `${visible.length} ${t('results')}` : t('Showing all items')}</span>
    {visible.map((option, index) => <InsightPickerOption key={option.id} first={index === 0} onChange={next => { onChange(next); if (!option.children?.length && !submenu?.(option.id)) setOpen(false) }} option={option} submenu={submenu?.(option.id)} value={value}/>)}
    {!visible.length && <div className={styles.menuEmpty}>{t('No results')}</div>}
  </TooltipProvider></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></label>
}

function InsightPickerOption({ first, onChange, option, submenu, value }: { first: boolean; onChange: (value: string) => void; option: InsightOption; submenu?: ReactNode; value: string }) {
  const { t } = useI18n()
  const isSelected = option.id === value
  const text = <span className={styles.menuLabel}>{t(option.label)}</span>
  const separator = option.separatorBefore && !first && <DropdownMenu.Separator className={styles.menuSeparator}/>
  if (submenu || option.children?.length) return <>{separator}<DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}>{text}<span className={styles.menuChevron} aria-hidden="true">▶</span><MenuRowTooltip label={option.description}/></DropdownMenu.SubTrigger><DropdownMenu.Portal>
    {submenu ?? <DropdownMenu.SubContent loop data-flow-motion="floating" className={`${styles.menu} ${styles.subMenu}`} collisionPadding={8} sideOffset={4}><div className={styles.subList}>{option.children!.map(child => <DropdownMenu.Item className={styles.menuItem} data-selected={child.id === value || undefined} key={child.id} onSelect={() => onChange(child.id)}>{child.icon && <span className={styles.menuIcon}>{child.icon}</span>}<span className={styles.menuLabel} data-i18n-ignore>{child.label}</span>{child.id === value && <InsightCheckIcon className={styles.menuCheck}/>}</DropdownMenu.Item>)}</div></DropdownMenu.SubContent>}
  </DropdownMenu.Portal></DropdownMenu.Sub></>
  return <>{separator}<DropdownMenu.Item className={styles.menuItem} data-selected={isSelected || undefined} onSelect={() => onChange(option.id)}>{text}{isSelected && <InsightCheckIcon className={styles.menuCheck}/>}<MenuRowTooltip label={option.description}/></DropdownMenu.Item></>
}

/** Linear's Measure trigger: "Time in status", a single status or type with its icon, or "N statuses" / "N status types". */
function measureDisplay(config: SavedViewInsightsConfig, data: BootstrapData, t: (source: string) => string): { label: string; icon?: ReactNode } {
  if (config.measure !== 'timeInStatus') return { label: MEASURE_OPTIONS.find(option => option.id === config.measure)?.label ?? config.measure }
  const nodes = timeInStatusTree(data).filter(node => node.values.length && node.values.every(value => config.timeInStatusIds.includes(value)))
  if (!nodes.length) return { label: 'Time in status' }
  if (nodes.length > 1) return { label: t(nodes.every(node => node.depth === 0) ? '{count} status types' : '{count} statuses').replace('{count}', String(nodes.length)) }
  return { label: nodes[0].label, icon: nodes[0].icon }
}

function TimeInStatusMenu({ config, data, onToggle }: { config: SavedViewInsightsConfig; data: BootstrapData; onToggle: (values: string[]) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const tree = useMemo(() => timeInStatusTree(data), [data])
  const selected = config.measure === 'timeInStatus' ? config.timeInStatusIds : []
  const normalized = query.trim().toLocaleLowerCase()
  const matches = (node: StatusNode) => t(node.label).toLocaleLowerCase().includes(normalized) || node.label.toLocaleLowerCase().includes(normalized)
  const visible = normalized ? tree.filter((node, index) => {
    if (matches(node)) return true
    if (node.depth === 1) { const parent = tree.slice(0, index).reverse().find(item => item.depth === 0); return parent ? matches(parent) : false }
    const children = tree.slice(index + 1); const end = children.findIndex(item => item.depth === 0)
    return (end < 0 ? children : children.slice(0, end)).some(matches)
  }) : tree
  useEffect(() => { const frame = requestAnimationFrame(() => input.current?.focus()); return () => cancelAnimationFrame(frame) }, [])
  return <DropdownMenu.SubContent loop data-flow-motion="floating" className={`${styles.menu} ${styles.subMenu}`} collisionPadding={8} sideOffset={4} alignOffset={-6}>
    <div className={styles.subSearch}><input ref={input} aria-label={t('Filter…')} placeholder={t('Filter…')} value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => {
      if (event.key === 'ArrowDown') { event.preventDefault(); list.current?.querySelector<HTMLElement>('[role^="menuitem"]')?.focus() }
      else if (event.key !== 'Escape' && event.key !== 'ArrowLeft' && event.key !== 'Tab') event.stopPropagation()
    }}/></div>
    <TooltipProvider delayDuration={450}><div ref={list} className={styles.subList}>
      {visible.map(node => {
        const checked = node.values.every(value => selected.includes(value))
        return <DropdownMenu.CheckboxItem className={`${styles.menuItem} ${styles.treeItem}`} data-depth={node.depth} key={node.key} checked={checked} onSelect={event => event.preventDefault()} onCheckedChange={() => onToggle(node.values)}>
          <MenuCheckbox/>
          <span className={styles.treeLabel}>
            {node.depth === 1 && <span className={styles.treeBranch} aria-hidden="true"><InsightTreeBranch/></span>}
            <span className={styles.menuIcon}>{node.icon}</span>
            <span className={styles.menuLabel} data-i18n-ignore>{t(node.label)}</span>
          </span>
        </DropdownMenu.CheckboxItem>
      })}
      {!visible.length && <div className={styles.menuEmpty}>{t('No results')}</div>}
    </div></TooltipProvider>
  </DropdownMenu.SubContent>
}

function segmentOptions(data: BootstrapData): InsightOption[] {
  const allowed = new Set(['assignee', 'agent', 'agentSession', 'creator', 'priority', 'label', 'labelGroup', 'template', 'externalSource', 'project', 'initiative', 'projectLabel', 'projectLabelGroup', 'addedToCycle'])
  // Linear's menu says "No Value"; the select then reads "No value".
  return [{ id: 'none', label: 'No Value' }, ...dimensionOptions(data, false).filter(option => allowed.has(option.id)).map((option, index) => ({ ...option, separatorBefore: index === 0 || option.id === 'project' }))]
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
  if (target.aggregation) parts.push(`${percentileLabel(target.aggregation as InsightAggregation)} ${target.operator === 'gt' ? '>' : '≤'} ${formatMetric(target.threshold ?? 0, config.measure)}`)
  return parts.join(' ')
}

function measureLabel(value: SavedViewInsightMeasure) { return ({ issueCount: 'Issue count', cycleTime: 'Cycle time', leadTime: 'Lead time', issueAge: 'Issue age', timeInStatus: 'Time in status' })[value] }
function dimensionLabel(value: SavedViewInsightDimension, data: BootstrapData) { return findInsightOption(dimensionOptions(data, true), value)?.label ?? (value.startsWith('labelGroup:') ? 'Label group' : value.startsWith('projectLabelGroup:') ? 'Project label group' : value) }
function findInsightOption(options: InsightOption[], value: string): InsightOption | undefined { for (const option of options) { if (option.id === value) return option; const child = findInsightOption(option.children ?? [], value); if (child) return child } }

function insightTableLines(insight: InsightData, config: SavedViewInsightsConfig, sliceHeader: string) {
  const latency = config.measure !== 'issueCount'
  const aggregations = selectedAggregations(config)
  const headers = [sliceHeader, 'Issue count', ...(latency ? aggregations.map(aggregation => `${measureLabel(config.measure)} (${percentileLabel(aggregation)})`) : config.segment === 'none' ? [] : insight.segments.map(segment => segment.label))]
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
