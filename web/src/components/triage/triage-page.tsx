import { useCallback, useEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import * as Popover from '@radix-ui/react-popover'
import { ArrowDownNarrowWide, ArrowDownWideNarrow, ChevronRight, CircleCheck, CircleX, Clock3, Copy, Link2, Settings2, UserRoundCog } from 'lucide-react'
import { toast } from 'sonner'
import { useScopedIssueRecords } from '@/hooks/use-scoped-issue-records'
import { IssuesSplitViewPage } from '@/components/issues-split-view'
import { ContentViewHeaderFavoriteActionButton } from '@/components/content-view'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesFilterBar } from '@/components/my-issues/my-issues-filter-bar'
import { toggleFilterOption, updateFilterOperator, updateFilterValues, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey } from '@/components/my-issues/my-issues-surface'
import { applyExplorerFilters, explorerFilterOptions, explorerPropertyOptions, ISSUE_FILTER_LABELS } from '@/components/issue-explorer/issue-explorer-model'
import { CalendarIcon } from '@/components/issue/issue-icons'
import { DisplayIcon, FilterIcon } from '@/components/ui/view-action-icons'
import { FlowTooltip } from '@/components/ui/tooltip'
import { SelectControl } from '@/components/ui/select-control'
import { Skeleton } from '@/components/ui/skeleton'
import { Toggle } from '@/components/ui/toggle'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { updateIssue } from '@/lib/api'
import { issuePath, settingsPath } from '@/lib/app-routes'
import { findFavorite, toggleFavoriteFor } from '@/lib/favorites'
import type { BootstrapData, Issue, Team } from '@/types/flow'
import { TriageActionDialogs } from './triage-actions'
import {
  addedToTriageAt, formatSnoozeDate, isSnoozed, isTriageCandidate, readTriageDisplay, snoozePresets, sortTriageIssues, TRIAGE_DISPLAY_PROPERTIES, TRIAGE_ORDERINGS,
  TRIAGE_CHANGED_EVENT, TRIAGE_SHORTCUTS, triageDisplayKey, triageIssueFilter, writeTriageDisplay, type TriageActionKind, type TriageDisplaySettings, type TriageOrdering,
} from './triage-model'
import { TriageResponsibilityDialog } from './triage-responsibility-dialog'
import { TriageEmptyPage, TriageIllustration, TriageNotSelectedPage } from './triage-not-selected-page'
import './triage.css'

export type TriageSelectOptions = { replace?: boolean; sequence: string[] }

export type TriagePageProps = {
  data: BootstrapData
  team: Team
  /** Called after an issue changes from the list (context menu actions), with its updated record. */
  onReload?: (issue?: Issue) => Promise<void> | void
  /** Opens the create dialog for this team in its triage status. */
  onCreateIssue?: () => void
  onNavigate?: (path: string) => void
  /** Selected issue identifier when the URL owns the selection (Linear's /issue/KEY-n beside the list). */
  selectedIdentifier?: string
  /** Selects an issue (null returns to the empty state). Without it the page keeps its own selection. */
  onSelectIssue?: (issue: Issue | null, options: TriageSelectOptions) => void
  /** The full issue view for the selection, rendered by the host. */
  detail?: ReactNode
  /** Fallback issue view when the page owns the selection. */
  renderIssue?: (issue: Issue) => ReactNode
}

const FILTER_FIELDS = (Object.keys(ISSUE_FILTER_LABELS) as MyIssuesFilterKey[]).filter(field => field !== 'status')
const MINUTE = 60_000, HOUR = 60 * MINUTE, DAY = 24 * HOUR

/**
 * Linear's Triage: a 400px list of two-line rows (title + identifier, creator + when it was added) beside the full
 * issue view. Accept 1 / Decline 2 / Duplicate 3 / Snooze H, J/K to move, Esc to return to the empty state.
 */
export function TriagePage({ data, team, onReload, onCreateIssue, onNavigate, selectedIdentifier, onSelectIssue, detail, renderIssue }: TriagePageProps) {
  const { t, formatDate } = useI18n()
  const enabled = Boolean(data.teamSettings?.[team.id]?.triageEnabled)
  const [ownSelection, setOwnSelection] = useState<string | null>(null)
  const controlled = Boolean(onSelectIssue)
  const selectedKey = controlled ? (selectedIdentifier?.toUpperCase() ?? null) : ownSelection
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>([])
  const [filterOpen, setFilterOpen] = useState(false)
  const [responsibilityOpen, setResponsibilityOpen] = useState(false)
  const displayKey = triageDisplayKey(data.workspace.id, data.viewer.id, team.id)
  const [display, setDisplayState] = useState(() => readTriageDisplay(displayKey))
  useEffect(() => { setDisplayState(readTriageDisplay(displayKey)) }, [displayKey])
  const setDisplay = (next: TriageDisplaySettings) => { setDisplayState(next); writeTriageDisplay(displayKey, next) }
  const [menuAction, setMenuAction] = useState<{ issue: Issue; kind: TriageActionKind }>()

  const predicate = useCallback((issue: Issue) => isTriageCandidate(issue, team.id), [team.id])
  const query = useMemo(() => ({ teamId: team.id, archived: 'false' as const, filter: triageIssueFilter() }), [team.id])
  const { issues, loading } = useScopedIssueRecords(data, query, predicate, enabled)
  const options = useMemo(() => explorerPropertyOptions(data, issues), [data, issues])
  const active = useMemo(() => issues.filter(issue => !isSnoozed(issue)), [issues])
  const visible = useMemo(() => sortTriageIssues(applyExplorerFilters(display.showSnoozed ? issues : active, filters, data), display), [active, data, display, filters, issues])
  const selected = selectedKey ? visible.find(issue => issue.identifier.toUpperCase() === selectedKey) : undefined
  const showId = display.properties.includes('id')
  const showDue = display.properties.includes('dueDate')
  const favorited = Boolean(findFavorite(data.favorites, data.viewer.id, 'triage', team.id))

  const select = useCallback((issue: Issue | null, replace = false) => {
    const sequence = visible.map(item => item.id)
    if (onSelectIssue) onSelectIssue(issue, { replace, sequence })
    else setOwnSelection(issue?.identifier.toUpperCase() ?? null)
  }, [onSelectIssue, visible])

  // Linear moves on to the next issue once the selected one leaves triage (accepted, declined, snoozed…).
  const previous = useRef<Issue[]>(visible)
  useEffect(() => {
    const before = previous.current
    previous.current = visible
    if (!selectedKey || visible.some(issue => issue.identifier.toUpperCase() === selectedKey)) return
    const index = before.findIndex(issue => issue.identifier.toUpperCase() === selectedKey)
    if (index < 0) return
    const remaining = new Set(visible.map(issue => issue.id))
    const next = before.slice(index + 1).find(issue => remaining.has(issue.id)) ?? before.slice(0, index).reverse().find(issue => remaining.has(issue.id))
    window.dispatchEvent(new Event(TRIAGE_CHANGED_EVENT))
    select(next ?? null, true)
  }, [select, selectedKey, visible])

  // Document title carries the count, like Linear's "Team › Triage (3)".
  useEffect(() => {
    if (!enabled || selectedKey) return
    queueMicrotask(() => {
      const [title, ...rest] = document.title.split(' · ')
      const base = title.replace(/ \(\d+\)$/, '')
      document.title = [`${base} (${active.length})`, ...rest].join(' · ')
    })
  })

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target
      if (event.defaultPrevented || event.metaKey || event.ctrlKey) return
      if (target instanceof Element && target.closest('input,textarea,select,[contenteditable=true],[contenteditable=""],[role=textbox],[role=dialog],[role=menu],[role=listbox]')) return
      if (document.querySelector('[role=dialog],[role=menu][data-state=open]')) return
      const key = event.key.toLowerCase()
      if (event.altKey) {
        if (event.code === 'KeyF') { event.preventDefault(); void toggleFavoriteFor(data, 'triage', team.id, undefined, favorited) }
        return
      }
      if (key === 'j' || key === 'arrowdown' || key === 'k' || key === 'arrowup') {
        if (!visible.length) return
        event.preventDefault()
        const index = selected ? visible.findIndex(issue => issue.id === selected.id) : -1
        const next = index < 0 ? visible[0] : visible[index + (key === 'j' || key === 'arrowdown' ? 1 : -1)]
        if (next) select(next, Boolean(selected))
        return
      }
      if (key === 'escape' && selectedKey) { event.preventDefault(); select(null); return }
      if (key === 'f' && !event.shiftKey) { event.preventDefault(); setFilterOpen(true) }
    }
    addEventListener('keydown', onKey)
    return () => removeEventListener('keydown', onKey)
  }, [data, favorited, select, selected, selectedKey, team.id, visible])

  useEffect(() => {
    if (!selected) return
    document.querySelector(`[data-triage-row="${selected.id}"]`)?.scrollIntoView?.({ block: 'nearest' })
  }, [selected])

  const settle = (updated: Issue) => {
    setMenuAction(undefined)
    window.dispatchEvent(new Event(TRIAGE_CHANGED_EVENT))
    void onReload?.(updated)
  }

  if (!enabled) return <TriageDisabledPage team={team} onOpenSettings={onNavigate ? () => onNavigate(settingsPath(data.workspace.urlKey, 'team', team.key, 'triage')) : undefined}/>

  const header = <header className="flow-triage-list__header">
    <div className="flow-triage-list__title">
      <h2>{t('Triage')}</h2>
      <FlowTooltip label={t(favorited ? 'Remove from favorites' : 'Favorite page')} shortcut="⌥ F">
        <ContentViewHeaderFavoriteActionButton className="flow-triage-list__favorite" favorited={favorited} favoriteLabel={t('Favorite page')} unfavoriteLabel={t('Remove from favorites')} onClick={() => void toggleFavoriteFor(data, 'triage', team.id, undefined, favorited)}/>
      </FlowTooltip>
    </div>
    <div className="flow-triage-list__actions">
      <FlowTooltip label={t('Configure triage responsibility')}>
        <button type="button" className="flow-triage-icon-button is-small" aria-label={t('Configure triage responsibility')} onClick={() => setResponsibilityOpen(true)}><UserRoundCog size={14}/></button>
      </FlowTooltip>
      <MyIssuesFilterMenu
        open={filterOpen}
        onOpenChange={setFilterOpen}
        filters={filters}
        availableFields={FILTER_FIELDS}
        options={field => explorerFilterOptions(field, options)}
        onToggle={(field, option) => setFilters(current => toggleFilterOption(current, field, ISSUE_FILTER_LABELS[field] ?? field, option))}
        trigger={<FlowTooltipButton tooltip={t('Add Filter')} shortcut="F" label={t('Add filter')} count={filters.length}><FilterIcon/></FlowTooltipButton>}
      />
      <TriageDisplayMenu value={display} onChange={setDisplay}/>
    </div>
  </header>

  const rows = loading && !issues.length ? <TriageSkeleton/> : visible.length ? visible.map(issue => <TriageRow
    key={issue.id}
    issue={issue}
    data={data}
    selected={issue.id === selected?.id}
    showId={showId}
    showDue={showDue}
    formatDate={formatDate}
    t={t}
    onOpen={() => select(issue)}
    onAction={kind => setMenuAction({ issue, kind })}
    onSnooze={until => void updateIssue(issue.id, { snoozedUntil: until.toISOString() }).then(updated => { toast.success(t('Snoozed until {date}').replace('{date}', formatSnoozeDate(until))); settle(updated) }).catch(error => toast.error(error instanceof Error ? error.message : t('Could not update issue')))}
  />) : issues.length ? <div className="flow-triage-list__empty-filter">{t('No issues match these filters')}</div> : null

  const list = <div className="flow-triage-list" data-scroll-id={`triage-list-${team.id}`}>
    {header}
    {filters.length > 0 && <MyIssuesFilterBar filters={filters} filterOptions={filter => explorerFilterOptions(filter.field, options)} onAdd={() => setFilterOpen(true)} onClear={() => setFilters([])} onOperatorChange={(id, operator) => setFilters(current => updateFilterOperator(current, id, operator))} onRemove={id => setFilters(current => current.filter(filter => filter.id !== id))} onValuesChange={(id, values) => setFilters(current => updateFilterValues(current, id, values))}/>}
    <div className="flow-triage-list__rows" role="list" aria-label={t('Triage issues')} aria-busy={loading || undefined}>{rows}</div>
  </div>

  const selection = controlled ? (selectedKey ? detail : undefined) : (selected ? renderIssue?.(selected) : undefined)
  const rightPane = selection ?? (loading && !issues.length
    ? <div className="flow-triage-not-selected" aria-busy="true"/>
    : active.length === 0 ? <TriageEmptyPage onCreate={onCreateIssue}/> : <TriageNotSelectedPage issueCount={active.length} onCreate={onCreateIssue}/>)

  return <section className="flow-triage-page" aria-label={t('Triage')}>
    <IssuesSplitViewPage surface="triage" list={list} detail={rightPane} aria-label={t('Triage split view')}/>
    {menuAction ? <TriageActionDialogs issue={menuAction.issue} data={data} open={menuAction.kind} onOpenChange={kind => setMenuAction(kind ? { ...menuAction, kind } : undefined)} onDone={settle}/> : null}
    {responsibilityOpen ? <TriageResponsibilityDialog data={data} team={team} open onOpenChange={setResponsibilityOpen} onSaved={() => onReload?.()}/> : null}
  </section>
}

function FlowTooltipButton({ tooltip, shortcut, label, count = 0, children, ...props }: { tooltip: string; shortcut?: string; label: string; count?: number; children: ReactNode } & React.ButtonHTMLAttributes<HTMLButtonElement>) {
  return <FlowTooltip label={tooltip} shortcut={shortcut}>
    <button type="button" {...props} className="flow-triage-icon-button" aria-label={label} data-active={count > 0 || undefined}>{children}{count ? <span className="flow-triage-icon-button__count">{count}</span> : null}</button>
  </FlowTooltip>
}

function relativeAgo(value: string, t: (text: string) => string) {
  const elapsed = Math.max(0, Date.now() - Date.parse(value))
  const unit = (template: string, count: number) => t(template).replace('{count}', String(Math.max(1, Math.floor(count))))
  if (elapsed < MINUTE) return t('Just now')
  if (elapsed < HOUR) return unit('{count}m ago', elapsed / MINUTE)
  if (elapsed < DAY) return unit('{count}h ago', elapsed / HOUR)
  if (elapsed < 7 * DAY) return unit('{count}d ago', elapsed / DAY)
  if (elapsed < 30 * DAY) return unit('{count}w ago', elapsed / (7 * DAY))
  if (elapsed < 365 * DAY) return unit('{count}mo ago', elapsed / (30 * DAY))
  return unit('{count}y ago', elapsed / (365 * DAY))
}

function TriageRow({ issue, data, selected, showId, showDue, formatDate, t, onOpen, onAction, onSnooze }: {
  issue: Issue
  data: BootstrapData
  selected: boolean
  showId: boolean
  showDue: boolean
  formatDate: (value: string | number | Date, options?: Intl.DateTimeFormatOptions) => string
  t: (text: string) => string
  onOpen: () => void
  onAction: (kind: TriageActionKind) => void
  onSnooze: (until: Date) => void
}) {
  const href = issuePath(data.workspace.urlKey, issue)
  const added = addedToTriageAt(issue)
  const creator = issue.creator?.displayName || issue.creator?.name || t('Unknown')
  const click = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    onOpen()
  }
  const copyUrl = () => void navigator.clipboard.writeText(`${location.origin}${href}`).then(() => toast.success(t('Issue URL copied to clipboard'))).catch(() => toast.error(t('Could not write to clipboard')))
  const snoozed = isSnoozed(issue)
  return <ContextMenu.Root>
    <ContextMenu.Trigger asChild>
      <a className="flow-triage-row" role="listitem" href={href} data-triage-row={issue.id} data-selected={selected || undefined} aria-current={selected ? 'page' : undefined} onClick={click}>
        <span className="flow-triage-row__line">
          <span className="flow-triage-row__title" data-i18n-ignore>{issue.title}</span>
          {showId ? <span className="flow-triage-row__id" data-i18n-ignore>{issue.identifier}</span> : null}
        </span>
        <span className="flow-triage-row__line is-meta">
          <UserAvatar className="flow-triage-row__avatar" name={creator} avatarUrl={issue.creator?.avatarUrl}/>
          <span className="flow-triage-row__creator" data-i18n-ignore>{creator}</span>
          {showDue && issue.dueDate ? <span className="flow-triage-row__due"><CalendarIcon/>{formatDate(issue.dueDate.length === 10 ? `${issue.dueDate}T00:00:00` : issue.dueDate, { month: 'short', day: 'numeric' })}</span> : null}
          {snoozed && issue.snoozedUntil ? <span className="flow-triage-row__snoozed"><Clock3 size={12}/>{formatSnoozeDate(new Date(issue.snoozedUntil))}</span> : null}
          <FlowTooltip label={`${t('Added to triage at')} ${formatDate(added, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}`}>
            <span className="flow-triage-row__date">{relativeAgo(added, t)}</span>
          </FlowTooltip>
        </span>
      </a>
    </ContextMenu.Trigger>
    <ContextMenu.Portal>
      <ContextMenu.Content data-flow-motion="floating" className="flow-triage-menu" collisionPadding={10}>
        <TriageMenuItem icon={<CircleCheck size={16}/>} shortcut={TRIAGE_SHORTCUTS.accept} onSelect={() => onAction('accept')}>{t('Accept…')}</TriageMenuItem>
        <TriageMenuItem icon={<CircleX size={16}/>} shortcut={TRIAGE_SHORTCUTS.decline} onSelect={() => onAction('decline')}>{t('Decline…')}</TriageMenuItem>
        <TriageMenuItem icon={<Copy size={16}/>} shortcut={TRIAGE_SHORTCUTS.duplicate} onSelect={() => onAction('duplicate')}>{t('Mark as duplicate…')}</TriageMenuItem>
        <ContextMenu.Separator className="flow-triage-menu__separator"/>
        <ContextMenu.Sub>
          <ContextMenu.SubTrigger className="flow-triage-menu__item"><span className="flow-triage-menu__icon"><Clock3 size={16}/></span><span>{t('Snooze')}</span><kbd>{TRIAGE_SHORTCUTS.snooze}</kbd><ChevronRight size={14} className="flow-triage-menu__chevron"/></ContextMenu.SubTrigger>
          <ContextMenu.Portal>
            <ContextMenu.SubContent data-flow-motion="floating" className="flow-triage-menu is-sub" sideOffset={4} alignOffset={-5}>
              {snoozePresets().map(preset => <TriageMenuItem key={preset.id} trailing={formatSnoozeDate(preset.until)} onSelect={() => onSnooze(preset.until)}>{t(preset.label)}</TriageMenuItem>)}
              <TriageMenuItem onSelect={() => onAction('snooze')}>{t('Custom…')}</TriageMenuItem>
            </ContextMenu.SubContent>
          </ContextMenu.Portal>
        </ContextMenu.Sub>
        <TriageMenuItem icon={<Link2 size={16}/>} shortcut="⌘⇧," onSelect={copyUrl}>{t('Copy URL')}</TriageMenuItem>
      </ContextMenu.Content>
    </ContextMenu.Portal>
  </ContextMenu.Root>
}

function TriageMenuItem({ icon, shortcut, trailing, children, onSelect }: { icon?: ReactNode; shortcut?: string; trailing?: string; children: ReactNode; onSelect: () => void }) {
  return <ContextMenu.Item className="flow-triage-menu__item" onSelect={onSelect}>
    {icon ? <span className="flow-triage-menu__icon">{icon}</span> : null}
    <span>{children}</span>
    {shortcut ? <kbd>{shortcut}</kbd> : null}
    {trailing ? <small>{trailing}</small> : null}
  </ContextMenu.Item>
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLElement && (target.isContentEditable || Boolean(target.closest("input, textarea, select, [role='textbox'], [role='searchbox']")))
}

/** Linear's triage display options: Ordering (+ direction), Show snoozed, Display properties ID / Due date. */
export function TriageDisplayMenu({ value, onChange }: { value: TriageDisplaySettings; onChange: (value: TriageDisplaySettings) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  useEffect(() => {
    // Linear: ⇧V shows display options.
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !event.shiftKey || event.altKey || event.metaKey || event.ctrlKey || event.key.toLowerCase() !== 'v' || isEditableTarget(event.target)) return
      event.preventDefault()
      setOpen(current => !current)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
  const update = (change: Partial<TriageDisplaySettings>) => onChange({ ...value, ...change })
  const direction = t(value.newestFirst ? 'Newest first' : 'Oldest first')
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <FlowTooltip disabled={open} label={t('Show display options')} shortcut="⇧ V">
      <Popover.Trigger asChild>
        <button type="button" className="flow-triage-icon-button" aria-label={t('Display options')}><DisplayIcon/></button>
      </Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" align="end" sideOffset={4} collisionPadding={8} className="flow-triage-display" aria-label={t('Display options')}>
        <div className="flow-triage-display__row">
          <span>{t('Ordering')}</span>
          <FlowTooltip label={direction}>
            <button type="button" className="flow-triage-display__direction" aria-label={direction} aria-pressed={!value.newestFirst} onClick={() => update({ newestFirst: !value.newestFirst })}>{value.newestFirst ? <ArrowDownWideNarrow/> : <ArrowDownNarrowWide/>}</button>
          </FlowTooltip>
          <SelectControl align="end" className="flow-triage-display__select" label={t('Ordering')} value={value.ordering} options={TRIAGE_ORDERINGS.map(item => ({ value: item.value, label: t(item.label) }))} onChange={ordering => update({ ordering: ordering as TriageOrdering })}/>
        </div>
        <div className="flow-triage-display__separator"/>
        <label className="flow-triage-display__row">
          <span>{t('Show snoozed')}</span>
          <Toggle label={t('Show snoozed')} checked={value.showSnoozed} onChange={showSnoozed => update({ showSnoozed })}/>
        </label>
        <div className="flow-triage-display__separator"/>
        <div className="flow-triage-display__properties">
          <h3>{t('Display properties')}</h3>
          <div>{TRIAGE_DISPLAY_PROPERTIES.map(property => {
            const on = value.properties.includes(property.value)
            return <button key={property.value} type="button" className={on ? 'is-selected' : undefined} aria-pressed={on} onClick={() => update({ properties: on ? value.properties.filter(item => item !== property.value) : [...value.properties, property.value] })}>{t(property.label)}</button>
          })}</div>
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function TriageSkeleton() {
  const { t } = useI18n()
  return <div className="flow-triage-skeleton" role="status" aria-label={t("Loading triage")}>
    {Array.from({ length: 6 }, (_, index) => <div className="flow-triage-skeleton__row" key={index}><Skeleton className="flow-triage-skeleton__title"/><Skeleton className="flow-triage-skeleton__meta"/></div>)}
  </div>
}

/** Triage turned off for the team: Linear explains it and links to the team's triage settings. */
function TriageDisabledPage({ team, onOpenSettings }: { team: Team; onOpenSettings?: () => void }) {
  const { t } = useI18n()
  return <section className="flow-triage-page flow-triage-disabled" aria-label={t('Triage')} data-triage-disabled="">
    <div className="flow-triage-not-selected__body">
      <TriageIllustration/>
      <h2>{t('Triage is turned off')}</h2>
      <p>{t('Turn on triage to review issues created by integrations and members outside {team} before they enter the team workflow.').replace('{team}', team.name)}</p>
      {onOpenSettings ? <button type="button" className="flow-triage-not-selected__cta" onClick={onOpenSettings}><Settings2 size={14}/>{t('Go to triage settings')}</button> : null}
    </div>
  </section>
}
