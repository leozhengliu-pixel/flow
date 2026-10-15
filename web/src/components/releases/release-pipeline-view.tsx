import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Star } from 'lucide-react'
import { Suspense, forwardRef, useMemo, useState, type CSSProperties, type HTMLAttributes, type MouseEvent } from 'react'
import { toast } from '@/lib/toast'

import { openCommandMenu } from '@/components/command/command-context'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { DisclosureTriangle } from '@/components/ui/disclosure-triangle'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { useFlowHotkeys, useFlowRowShortcuts } from '@/components/ui/menu-shortcuts'
import { FlowDropdownMenuContent, FlowMenuItem } from '@/components/ui/row-context-menu'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { DisplayIcon } from '@/components/ui/view-action-icons'
import { toggleFavoriteFor } from '@/lib/favorites'
import { canCreateReleasePipeline } from '@/lib/settings-access'
import { restoreTrashEntry, updateRelease } from '@/lib/api'
import { releasePath, releasePipelinePath, releasePipelineSettingsPath, releasePipelinesPath, type ReleasePipelineTab } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import { AgentChatPanel } from '@/lib/route-pages'
import type { BootstrapData, Release, ReleasePipeline, TrashEntry } from '@/types/flow'

import { ReleaseChangelog } from './release-changelog'
import { ReleaseDateMenu } from './release-date-menu'
import { RELEASE_MENU_SHORTCUTS } from './release-menu-shortcuts'
import { DisplayOptionsBody, SortHeader } from './release-display-options'
import { ReleasesIllustration } from './release-illustrations'
import { ReleaseDateIcon, ReleaseStatusIcon } from './release-icons'
import { PipelineActionsDropdown, ReleaseRowContextMenu, type PipelineMenuActions, type ReleaseMenuActions } from './release-menus'
import { useReleaseSelectionCommands } from './release-selection-commands'
import { HeaderIconButton, RELEASE_DOCS_URL, ReleaseBreadcrumbs, ReleaseEmptyState, ReleasesHeader, ToolbarPillButton } from './release-page-chrome'
import { dayValue, deletedReleasesForPipeline, releaseCompletion, releaseDateTone, releaseHasNotes, releaseStageGroups, releaseStatusForStage, releasesForPipeline, sortReleases, type ReleaseOrdering, type SortDirection } from './release-view-model'
import { absoluteUrl, copyToClipboard, isFavorite } from './release-actions-model'
import { useReleaseActions } from './use-release-actions'
import { PipelineTabContextMenu } from './pipeline-tab-context-menu'
import { trashRetentionDays } from '@/lib/trash-retention'

type Props = {
  data: BootstrapData
  pipeline: ReleasePipeline
  tab: ReleasePipelineTab
  onCreate: () => void
  onNavigate: (path: string) => void
  onOpenSidebar: () => void
  onReload: () => Promise<void>
}


/** A release pipeline: Releases (scheduled or continuous list), Changelog, archive and recently deleted. */
export function ReleasePipelineView({ data, pipeline, tab, onCreate, onNavigate, onOpenSidebar, onReload }: Props) {
  const { t } = useI18n()
  const workspace = data.workspace.urlKey
  const canManage = canCreateReleasePipeline(data)
  const favorite = isFavorite(data, 'release_pipeline', pipeline.id)
  const archive = tab === 'archive'
  const deleted = tab === 'deleted'
  const scheduled = pipeline.type === 'scheduled'
  const [grouping, setGrouping] = useState<'none' | 'stage'>('stage')
  const [ordering, setOrdering] = useState<ReleaseOrdering>('releaseDate')
  const [direction, setDirection] = useState<SortDirection>('desc')
  const [properties, setProperties] = useState({ description: true, version: true, date: true, completion: true, notes: true })
  const pipelineActions: PipelineMenuActions = {
    onCreateRelease: onCreate,
    onToggleFavorite: () => { void toggleFavoriteFor(data, 'release_pipeline', pipeline.id, undefined, favorite) },
    onCopyUrl: () => void copyToClipboard(absoluteUrl(releasePipelinePath(workspace, pipeline.slugId)), t('URL copied to clipboard'), t('Could not copy URL')),
    onOpenSettings: () => onNavigate(releasePipelineSettingsPath(workspace, pipeline.slugId)),
    onToggleArchive: () => onNavigate(releasePipelinePath(workspace, pipeline.slugId, archive ? 'releases' : 'archive')),
    onOpenDeleted: () => onNavigate(releasePipelinePath(workspace, pipeline.slugId, 'deleted')),
  }
  const hotkeys: Record<string, () => void> = {
    '⌥ F': pipelineActions.onToggleFavorite,
    '⌘ ⇧ ,': pipelineActions.onCopyUrl,
  }
  if (scheduled && canManage) hotkeys['N then R'] = onCreate
  if (!archive && !deleted) { hotkeys['1'] = () => onNavigate(releasePipelinePath(workspace, pipeline.slugId, 'releases')); hotkeys['2'] = () => onNavigate(releasePipelinePath(workspace, pipeline.slugId, 'changelog')) }
  useFlowHotkeys(hotkeys)
  const section = archive ? t('Archived releases') : deleted ? t('Recently deleted releases') : undefined
  const crumbs = [
    // The reference app leads continuous pipelines with a Releases crumb; scheduled pipelines show only their own name.
    ...(scheduled ? [] : [{ label: t('Releases'), translate: true, href: releasePipelinesPath(workspace), onClick: () => onNavigate(releasePipelinesPath(workspace)) }]),
    { label: pipeline.name, ...(section ? { href: releasePipelinePath(workspace, pipeline.slugId), onClick: () => onNavigate(releasePipelinePath(workspace, pipeline.slugId)) } : {}) },
    ...(section ? [{ label: <>{section}{archive && <span className="flow-releases-crumbs__count" data-i18n-ignore>{releasesForPipeline(data, pipeline, true).length}</span>}</>, translate: true }] : []),
  ]
  const tabLink = (value: 'releases' | 'changelog', label: string, key: string) => {
    const href = releasePipelinePath(workspace, pipeline.slugId, value)
    return <ScopedFlowTooltip label={t(`View ${label}`)} shortcut={key}><PipelineTabContextMenu data={data} pipeline={pipeline} tab={value}><a aria-current={tab === value ? 'page' : undefined} className="flow-releases-tab ui-pill" href={href} onClick={event => { if (event.metaKey || event.ctrlKey || event.button !== 0) return; event.preventDefault(); onNavigate(href) }}>{t(label)}</a></PipelineTabContextMenu></ScopedFlowTooltip>
  }
  const display = scheduled
    ? <DisplayOptionsBody
      grouping={{ value: grouping, onChange: setGrouping, options: [['none', 'No grouping'], ['stage', 'Stage']] }}
      ordering={{ value: ordering, onChange: next => { setOrdering(next); setDirection(next === 'releaseDate' ? 'desc' : 'asc') }, options: [['version', 'Release'], ['stage', 'Stage'], ['releaseDate', 'Release date']], direction, onDirection: setDirection }}
      properties={([['description', 'Description'], ['version', 'Version'], ['date', 'Release date'], ['completion', 'Completion'], ['notes', 'Release notes']] as const).map(([key, label]) => ({ key, label, active: properties[key], onToggle: () => setProperties(current => ({ ...current, [key]: !current[key] })) }))}/>
    : <DisplayOptionsBody properties={([['version', 'Version'], ['date', 'Release date'], ['notes', 'Release notes']] as const).map(([key, label]) => ({ key, label, active: properties[key], onToggle: () => setProperties(current => ({ ...current, [key]: !current[key] })) }))}/>
  return <main className="main-panel flow-releases-page" aria-label={`${pipeline.name} ${section ?? t(tab === 'changelog' ? 'Changelog' : 'Releases')}`}>
    <ReleasesHeader onOpenSidebar={onOpenSidebar} actions={scheduled && canManage && !archive && !deleted && <HeaderIconButton label={t('Create new release')} shortcut="N then R" onClick={onCreate}><FlowGlyph name="plus"/></HeaderIconButton>}>
      <ReleaseBreadcrumbs crumbs={crumbs}/>
      {!section && <>
        <HeaderIconButton aria-pressed={favorite} className="flow-releases-favorite" label={t(favorite ? 'Remove from favorites' : 'Add to favorites')} onClick={pipelineActions.onToggleFavorite}>{favorite ? <Star fill="currentColor"/> : <FlowGlyph name="favorite"/>}</HeaderIconButton>
        <PipelineActionsDropdown pipeline={pipeline} favorite={favorite} archive={archive} canManage={canManage} actions={pipelineActions} trigger={<HeaderIconButton label={t('Pipeline options')} tooltip=""><FlowGlyph name="ellipsis"/></HeaderIconButton>}/>
      </>}
    </ReleasesHeader>
    {!archive && !deleted && <div className="flow-releases-subheader">
      <nav className="flow-releases-tabs" aria-label={t('Pipeline views')}>{tabLink('releases', 'Releases', '1')}{tabLink('changelog', 'Changelog', '2')}</nav>
      {tab === 'releases' && <Popover.Root>
        <Popover.Trigger asChild><ToolbarPillButton label={t('Display options')}><DisplayIcon/></ToolbarPillButton></Popover.Trigger>
        <Popover.Portal><Popover.Content data-flow-motion="floating" aria-label={t('Display options')} className="flow-pipeline-display-popover" align="end" collisionPadding={8} sideOffset={4}>{display}</Popover.Content></Popover.Portal>
      </Popover.Root>}
    </div>}
    {deleted ? <DeletedReleasesList items={deletedReleasesForPipeline(data, pipeline)} retentionDays={trashRetentionDays(data)} onReload={onReload}/>
      : tab === 'changelog' ? <ReleaseChangelog data={data} pipeline={pipeline} onCreate={onCreate} onNavigate={onNavigate} onReload={onReload}/>
        : <PipelineReleaseList data={data} pipeline={pipeline} archived={archive} grouping={scheduled && !archive ? grouping : 'none'} ordering={ordering} direction={direction} properties={properties} onSort={next => { if (ordering === next) setDirection(value => value === 'asc' ? 'desc' : 'asc'); else { setOrdering(next); setDirection(next === 'releaseDate' ? 'desc' : 'asc') } }} canManage={canManage} onCreate={onCreate} onNavigate={onNavigate} onReload={onReload}/>}
  </main>
}

type ListProps = {
  data: BootstrapData
  pipeline: ReleasePipeline
  archived: boolean
  grouping: 'none' | 'stage'
  ordering: ReleaseOrdering
  direction: SortDirection
  properties: { description: boolean; version: boolean; date: boolean; completion: boolean; notes: boolean }
  onSort: (ordering: ReleaseOrdering) => void
  canManage: boolean
  onCreate: () => void
  onNavigate: (path: string) => void
  onReload: () => Promise<void>
}

function PipelineReleaseList({ data, pipeline, archived, grouping, ordering, direction, properties, onSort, canManage, onCreate, onNavigate, onReload }: ListProps) {
  const { t } = useI18n()
  const workspace = data.workspace.urlKey
  const scheduled = pipeline.type === 'scheduled'
  const releases = useMemo(() => sortReleases(releasesForPipeline(data, pipeline, archived), pipeline, scheduled ? ordering : 'releaseDate', scheduled ? direction : 'desc'), [archived, data, direction, ordering, pipeline, scheduled])
  const [collapsed, setCollapsed] = useState<string[]>([])
  const [selected, setSelected] = useState<string[]>([])
  const { actionsFor, dialogs } = useReleaseActions({ data, onReload, onNavigate })
  useFlowRowShortcuts(RELEASE_MENU_SHORTCUTS)
  useFlowHotkeys(selected.length ? { Escape: () => setSelected([]) } : {})
  if (!releases.length) {
    if (archived) return <ReleaseEmptyState title={t('No archived releases')} paragraphs={[t('Archived releases will appear here. You can restore them at any time.')]}/>
    if (!scheduled) return <ReleaseEmptyState art={<ReleasesIllustration/>} title={t('Awaiting first release')} paragraphs={[t('Releases added to this pipeline will appear here. Integrate with your CI/CD tool to automatically group issues into releases.')]} secondary={canManage ? { label: 'Set up integration', href: `${RELEASE_DOCS_URL}#example-for-continuous-deployments` } : undefined}/>
    return <ReleaseEmptyState art={<ReleasesIllustration/>} title={t(canManage ? 'Create your first release' : 'No releases yet')} paragraphs={[t(canManage ? "Create a new release to start tracking what's shipping. Integrate with your CI/CD tool to automatically add issues to your releases." : 'Releases added to this pipeline will appear here.')]} primary={canManage ? { label: 'Create new release', onClick: onCreate, shortcut: 'N then R' } : undefined} secondary={{ label: 'Documentation', href: `${RELEASE_DOCS_URL}#ci-setup` }}/>
  }
  const columns = scheduled
    ? ['24px', 'minmax(320px,1fr)', properties.notes && '140px', properties.date && '100px', properties.completion && '132px', '4px'].filter(Boolean).join(' ')
    : ['16px', 'minmax(240px,max-content)', 'minmax(80px,1fr)', properties.notes && '140px', properties.date && '100px', '4px'].filter(Boolean).join(' ')
  const groups = grouping === 'stage' ? releaseStageGroups(releases, pipeline) : [{ stage: '', releases }]
  const toggleSelected = (id: string) => setSelected(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])
  const open = (release: Release, tab: 'issues' | 'release-notes' = 'issues') => onNavigate(releasePath(workspace, pipeline.slugId, release.slugId, tab))
  return <div className={`flow-releases-list is-releases${scheduled ? '' : ' is-continuous'}`} role="grid" aria-label={t('Releases')} aria-multiselectable="true">
    <div className="flow-releases-list__head" role="row" style={{ gridTemplateColumns: columns } as CSSProperties}>
      <span/>
      {scheduled ? <SortHeader label="Release" active={ordering === 'version'} direction={direction} onClick={() => onSort('version')}/> : <span className="flow-releases-list__label">{t('Release')}</span>}
      {!scheduled && <span className="flow-releases-list__label">{t('Issues')}</span>}
      {properties.notes && <span className="flow-releases-list__label">{t('Release notes')}</span>}
      {properties.date && (scheduled ? <SortHeader label="Release date" active={ordering === 'releaseDate'} direction={direction} ariaLabel={t(direction === 'desc' ? 'Newest first' : 'Oldest first')} onClick={() => onSort('releaseDate')}/> : <span className="flow-releases-list__label">{t('Release date')}</span>)}
      {scheduled && properties.completion && <span className="flow-releases-list__label">{t('Completion')}</span>}
      <span/>
    </div>
    {groups.map(group => {
      const isCollapsed = collapsed.includes(group.stage)
      return <section className="flow-releases-list__group" key={group.stage || 'all'}>
        {grouping === 'stage' && <button className="flow-releases-group-header" aria-expanded={!isCollapsed} onClick={() => setCollapsed(current => isCollapsed ? current.filter(value => value !== group.stage) : [...current, group.stage])} type="button">
          <span className="flow-releases-group-header__toggle"><DisclosureTriangle open={!isCollapsed}/></span>
          <strong data-i18n-ignore={group.stage ? '' : undefined}>{group.stage || t('No stage')}</strong>
        </button>}
        {!isCollapsed && group.releases.map(release => <ReleaseRowContextMenu key={release.id} pipeline={pipeline} release={release} favorite={isFavorite(data, 'release', release.id)} actions={actionsFor(pipeline, release)}>
          <ReleaseRow data={data} pipeline={pipeline} release={release} columns={columns} properties={properties} canManage={canManage} selected={selected.includes(release.id)} onSelect={() => toggleSelected(release.id)} onOpen={tab => open(release, tab)} onReload={onReload}/>
        </ReleaseRowContextMenu>)}
      </section>
    })}
    {selected.length > 0 && <SelectionBar data={data} pipeline={pipeline} ids={selected} actions={selected.length === 1 ? (release => release && actionsFor(pipeline, release))(data.releases.find(item => item.id === selected[0])) : undefined} onClear={() => setSelected([])} onReload={onReload}/>}
    {dialogs}
  </div>
}

type ReleaseRowProps = { data: BootstrapData; pipeline: ReleasePipeline; release: Release; columns: string; properties: ListProps['properties']; canManage: boolean; selected: boolean; onSelect: () => void; onOpen: (tab?: 'issues' | 'release-notes') => void; onReload: () => Promise<void> } & Omit<HTMLAttributes<HTMLAnchorElement>, 'onSelect'>

/** A release row; Radix's context-menu trigger props land on the anchor. */
const ReleaseRow = forwardRef<HTMLAnchorElement, ReleaseRowProps>(function ReleaseRow({ data, pipeline, release, columns, properties, canManage, selected, onSelect, onOpen, onReload, style, ...rest }, ref) {
  const { t } = useI18n()
  const scheduled = pipeline.type === 'scheduled'
  const href = releasePath(data.workspace.urlKey, pipeline.slugId, release.slugId)
  const stop = (event: MouseEvent) => { event.preventDefault(); event.stopPropagation() }
  const version = release.version?.trim()
  return <a {...rest} ref={ref} className="flow-releases-row flow-release-row" data-flow-menu-row="" data-selected={selected || undefined} aria-selected={selected} href={href} role="row" style={{ ...style, gridTemplateColumns: columns }} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) { if (event.shiftKey) { event.preventDefault(); onSelect() } return } event.preventDefault(); onOpen() }}>
    <span className="flow-release-row__check"><button aria-label={t('Select release')} aria-pressed={selected} className="flow-release-checkbox" onClick={event => { stop(event); onSelect() }} type="button">{selected && <FlowGlyph name="checkboxCheck" width={10} height={9}/>}</button></span>
    <span className="flow-release-row__title">
      <span className="flow-release-row__icon">{release.stage && <ReleaseStatusIcon status={releaseStatusForStage(pipeline, release.stage, release.status)}/>}</span>
      <span className="flow-release-row__copy">
        <span className="flow-release-row__line">
          <strong className="flow-release-row__name" data-i18n-ignore>{release.name}</strong>
          {properties.version && version && <span className="flow-release-version" data-i18n-ignore>{version}</span>}
        </span>
        {scheduled && properties.description && release.description && <small className="flow-release-row__description" data-i18n-ignore>{release.description.replace(/[#*_`>[\]()]/g, '').slice(0, 280)}</small>}
      </span>
    </span>
    {!scheduled && <span className="flow-release-row__issues">{release.issueIds.length ? <span className="flow-release-chip">{t(release.issueIds.length === 1 ? '1 issue' : '{count} issues').replace('{count}', String(release.issueIds.length))}</span> : <span className="flow-release-row__muted">-</span>}</span>}
    {properties.notes && <span className="flow-release-row__notes">{releaseHasNotes(data, release)
      ? <ScopedFlowTooltip label={t('Open release notes')}><button aria-label={t('Open release notes')} className="flow-release-cell-button" onClick={event => { stop(event); onOpen('release-notes') }} type="button"><FlowGlyph name="document" className="flow-release-cell-button__icon"/><span>{t('Release notes')}</span></button></ScopedFlowTooltip>
      : canManage && <ScopedFlowTooltip label={t('Create release notes')}><button aria-label={t('Create release notes')} className="flow-release-cell-button is-new" onClick={event => { stop(event); onOpen('release-notes') }} type="button"><FlowGlyph name="plus"/><span>{t('New')}</span></button></ScopedFlowTooltip>}</span>}
    {properties.date && <span className="flow-release-row__date" onClick={event => event.preventDefault()}><ReleaseDateCell release={release} scheduled={scheduled} canManage={canManage} onReload={onReload}/></span>}
    {scheduled && properties.completion && <span className="flow-release-row__completion"><CompletionCell data={data} release={release}/></span>}
    <span/>
  </a>
})

function CompletionCell({ data, release }: { data: BootstrapData; release: Release }) {
  const { t } = useI18n()
  const completion = releaseCompletion(data, release)
  if (!completion) return null
  const parts = [t('{count} done').replace('{count}', String(completion.done))]
  if (completion.started) parts.push(t('{count} started').replace('{count}', String(completion.started)))
  if (completion.notStarted) parts.push(t('{count} not started').replace('{count}', String(completion.notStarted)))
  const tooltip = !completion.total ? t('No scoped issues') : release.status === 'released' ? t('{done} of {total} completed').replace('{done}', String(completion.done)).replace('{total}', String(completion.total)) : parts.join(t(', '))
  return <ScopedFlowTooltip label={tooltip}><span className="flow-release-completion"><ProgressRing progress={completion.progress}/><span>{completion.summary}</span></span></ScopedFlowTooltip>
}

/**
 * The reference app's completion ring: a 16px circle (r 7, 2px stroke) with a round-capped value arc starting just
 * right of the top and a track arc that leaves a 3-unit gap on both sides; 0% still draws the value dot.
 */
export function ProgressRing({ progress }: { progress: number }) {
  const radius = 7
  const circumference = 2 * Math.PI * radius
  const gap = 3
  const minimum = 0.48
  const start = -76
  const value = Math.min(Math.max(progress, 0), 1)
  const full = value >= 1
  const valueLength = full ? circumference : minimum + value * (circumference - 2 * gap - minimum)
  const trackLength = Math.max(circumference - valueLength - 2 * gap, 0)
  const trackStart = start + ((valueLength + gap) / circumference) * 360
  return <svg className="flow-release-ring" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true">
    {!full && trackLength > 0 && <circle cx="8" cy="8" r={radius} fill="none" strokeWidth="2" className="flow-release-ring__track" strokeDasharray={`${trackLength} ${circumference}`} transform={`rotate(${trackStart} 8 8)`}/>}
    <circle cx="8" cy="8" r={radius} fill="none" strokeWidth="2" className="flow-release-ring__value" strokeDasharray={`${valueLength} ${circumference}`} strokeLinecap="round" transform={`rotate(${start} 8 8)`}/>
  </svg>
}

export function ReleaseDateCell({ release, scheduled, canManage, onReload, long = false }: { release: Release; scheduled: boolean; canManage: boolean; onReload: () => Promise<void>; long?: boolean }) {
  const { t, formatDate, formatRelative } = useI18n()
  const short = (value: string) => long && new Date(dayValue(value)).getFullYear() !== new Date().getFullYear() ? formatDate(dayValue(value), { month: 'short', day: 'numeric', year: 'numeric' }) : formatDate(dayValue(value), { month: 'short', day: 'numeric' })
  const full = (value: string) => formatDate(value, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' })
  if (!scheduled && long) {
    const value = release.releasedAt ?? release.createdAt
    return <ScopedFlowTooltip label={full(value)}><span className="flow-release-date"><ReleaseDateIcon tone="released"/><span>{short(value)}</span></span></ScopedFlowTooltip>
  }
  if (!scheduled) {
    const value = release.releasedAt ?? release.createdAt
    return <span className="flow-release-date is-muted" title={full(value)}>{formatRelative(value)}</span>
  }
  if (release.status === 'released' && release.releasedAt) {
    const title = release.targetDate ? t('Released {date} (Initial target: {target})').replace('{date}', full(release.releasedAt)).replace('{target}', short(release.targetDate)) : t('Released {date}').replace('{date}', full(release.releasedAt))
    return <ScopedFlowTooltip label={title}><span className="flow-release-date"><ReleaseDateIcon tone="released"/><span>{short(release.releasedAt)}</span></span></ScopedFlowTooltip>
  }
  if (release.status === 'canceled') return <span className="flow-release-date">{release.updatedAt ? short(release.updatedAt) : ''}</span>
  const body = release.targetDate ? <><ReleaseDateIcon tone={release.status === 'inProgress' || release.status === 'planned' ? releaseDateTone(release.targetDate) : 'set'}/><span>{short(release.targetDate)}</span></> : long ? <><FlowGlyph name="dateAdd"/><span>{t('Release date')}</span></> : <FlowGlyph name="dateAdd"/>
  if (!canManage) return release.targetDate ? <span className="flow-release-date">{body}</span> : null
  const change = (value: string) => { void updateRelease(release.id, { targetDate: value }).then(onReload).catch(error => toast.error(error instanceof Error ? error.message : t('Could not save release'))) }
  return <ReleaseDateMenu value={release.targetDate} onChange={change} align="start" trigger={<button aria-label={t('Change release target date')} className={`flow-release-date flow-release-cell-button${release.targetDate || long ? '' : ' is-add'}`} onClick={event => event.stopPropagation()} title={t(release.targetDate ? 'Change target date' : 'Add target date')} type="button">{body}</button>}/>
}

function SelectionBar({ data, pipeline, ids, actions, onClear, onReload }: { data: BootstrapData; pipeline: ReleasePipeline; ids: string[]; actions?: ReleaseMenuActions; onClear: () => void; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [agentOpen, setAgentOpen] = useState(false)
  const releases = data.releases.filter(item => ids.includes(item.id))
  useReleaseSelectionCommands({ data, pipeline, releases, actions, onReload })
  return <div className="flow-release-selection-slot">
    <div className="flow-release-selection" role="toolbar" aria-label={t('Selected releases')}>
      <span className="flow-release-selection__count">{t('{count} selected').replace('{count}', String(ids.length))}</span>
      <div className="flow-release-selection__actions">
        <button className="flow-release-selection__button is-actions" onClick={() => openCommandMenu()} type="button"><FlowGlyph name="command" width={14} height={14}/><span>{t('Actions')}</span></button>
        <ScopedFlowTooltip label={t('Ask agent')}><button aria-label={t('Ask agent')} className="flow-release-selection__button is-icon" onClick={() => setAgentOpen(true)} type="button"><AgentCursorGlyph size={14}/></button></ScopedFlowTooltip>
        <ScopedFlowTooltip label={t('Clear selection')}><button aria-label={t('Clear selection')} className="flow-release-selection__button is-icon is-ghost" onClick={onClear} type="button"><FlowGlyph name="closeChat" width={14} height={14}/></button></ScopedFlowTooltip>
      </div>
    </div>
    {agentOpen && <Suspense fallback={null}><AgentChatPanel data={data} issues={[]} open onClose={() => setAgentOpen(false)}/></Suspense>}
  </div>
}

function DeletedReleasesList({ items, retentionDays, onReload }: { items: TrashEntry[]; retentionDays: number; onReload: () => Promise<void> }) {
  const { t, formatDate } = useI18n()
  if (!items.length) return <ReleaseEmptyState title={t('No recently deleted releases')} paragraphs={[t('Deleted releases will appear here for {days} days before being permanently removed.').replace('{days}', String(retentionDays))]}/>
  return <div className="flow-releases-list is-deleted" aria-label={t('Recently deleted releases')}>
    {items.map(item => <DropdownMenu.Root key={item.id}>
      <div className="flow-releases-row flow-release-deleted-row">
        <ReleaseStatusIcon status="canceled"/>
        <span className="flow-release-deleted-row__copy"><strong data-i18n-ignore>{item.title}</strong><small>{t('Deleted by')} <span data-i18n-ignore>{item.deletedBy.displayName}</span> · {formatDate(item.deletedAt, { month: 'short', day: 'numeric', year: 'numeric' })}</small></span>
        <DropdownMenu.Trigger asChild><button className="flow-releases-header-button" aria-label={`${t('Open actions')} ${item.title}`} type="button"><FlowGlyph name="ellipsis"/></button></DropdownMenu.Trigger>
      </div>
      <DropdownMenu.Portal><FlowDropdownMenuContent align="end" label={t('Release options')} className="flow-release-actions-menu">
        <FlowMenuItem icon={<FlowGlyph name="moveTop"/>} label="Restore" onSelect={() => { void restoreTrashEntry(item.id).then(onReload).catch(error => toast.error(error instanceof Error ? error.message : t('Could not restore release'))) }}/>
      </FlowDropdownMenuContent></DropdownMenu.Portal>
    </DropdownMenu.Root>)}
  </div>
}
