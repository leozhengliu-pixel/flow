import * as Dialog from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { ArchiveRestore, ArrowDown, ArrowUp, X } from 'lucide-react'
import { useEffect, useMemo, useState, type MouseEvent, type ReactNode } from 'react'
import { toast } from 'sonner'

import { TeamIcon } from '@/components/issue/issue-icons'
import styles from '@/components/my-issues/my-issues-bulk-action-bar.module.css'
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from '@/components/ui/context-menu'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { restoreTrashEntry } from '@/lib/api'
import { canAdministerReleasePipeline, canCreateReleasePipeline } from '@/lib/settings-access'
import type { BootstrapData, ReleasePipeline, Team, TrashEntry } from '@/types/flow'

import { PipelineDeleteDialog } from './pipeline-delete-dialog'
import { PipelineEmptyIllustration } from './pipeline-empty-illustration'
import { pipelineRowSummaries, RELEASES_DOCS_URL } from './pipeline-settings-model'
import { ReleasesIcon } from './release-icons'
import './pipeline-settings-list.css'

type SortKey = 'name' | 'type' | 'releases' | 'latest'
type Sort = { key: SortKey; direction: 'asc' | 'desc' }
/** Columns that start descending when first sorted, like Linear's orderingDirection. */
const DESC_FIRST: SortKey[] = ['releases', 'latest']

type BulkAction = { id: string; label: string; run: () => void }

/** Selection shared by the active and deleted lists; Escape clears it. */
function useSelection(ids: string[]) {
  const [selected, setSelected] = useState<string[]>([])
  const key = ids.join(',')
  useEffect(() => { setSelected(current => current.filter(id => key.split(',').includes(id))) }, [key])
  useEffect(() => {
    if (!selected.length) return
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape' && !document.querySelector('[role=dialog]')) setSelected([]) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [selected.length])
  const toggle = (id: string) => setSelected(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])
  return { selected, toggle, clear: () => setSelected([]) }
}

/** Linear's multi-select bar: "N selected", "⌘ Actions" opening a command menu, and a clear button. */
function PipelineBulkBar({ count, actions, onClear }: { count: number; actions: BulkAction[]; onClear: () => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  if (!count) return null
  const title = t('{count} selected').replace('{count}', String(count))
  return <>
    <div aria-label={title} className={styles.bar} role="toolbar">
      <span className={styles.count}>{title}</span>
      <button type="button" aria-expanded={open} className={styles.actionsButton} onClick={() => setOpen(true)}><kbd className="flow-pipelines-bulk-kbd">⌘</kbd>{t('Actions')}</button>
      <button type="button" aria-label={t('Clear selection')} className={styles.clearButton} onClick={onClear}><X size={15}/></button>
    </div>
    <Dialog.Root open={open} onOpenChange={setOpen}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className={styles.overlay}/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className={styles.commandDialog}>
      <Dialog.Title className={styles.commandTitle}>{title}</Dialog.Title>
      <Command className={styles.command} loop>
        <div className={styles.commandInput}><Command.Input aria-label={t('Type a command…')} autoFocus placeholder={t('Type a command…')}/></div>
        <Command.List className={styles.commandList}>
          <Command.Empty className={styles.commandEmpty}>{t('No commands found')}</Command.Empty>
          {actions.map(action => <Command.Item className={styles.commandItem} key={action.id} value={action.label} onSelect={() => { setOpen(false); action.run() }}><span>{action.label}</span></Command.Item>)}
        </Command.List>
      </Command>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
  </>
}

function SelectBox({ checked, label, onToggle }: { checked: boolean; label: string; onToggle: () => void }) {
  return <span className="flow-pipelines-settings-select" role="cell" onClick={event => event.stopPropagation()}>
    <input type="checkbox" aria-label={label} checked={checked} onChange={onToggle}/>
  </span>
}

/** Linear's settings list rows for release pipelines (active pipelines). */
export function PipelineSettingsRows({ data, pipelines, onOpen, onOpenReleases, onDuplicate, onDeleted, onViewDeleted, onClearFilter, filtering }: {
  data: BootstrapData
  pipelines: ReleasePipeline[]
  onOpen: (pipeline: ReleasePipeline) => void
  onOpenReleases: (pipeline: ReleasePipeline) => void
  onDuplicate: (pipeline: ReleasePipeline) => void
  onDeleted: () => Promise<void>
  onViewDeleted: () => void
  onClearFilter: () => void
  filtering: boolean
}) {
  const { t, formatDate, formatRelative } = useI18n()
  // Linear orders this list by name by default (the header shows ↓).
  const [sort, setSort] = useState<Sort>({ key: 'name', direction: 'asc' })
  const [deleting, setDeleting] = useState<ReleasePipeline[]>()
  const summaries = useMemo(() => pipelineRowSummaries(data), [data])
  const selection = useSelection(pipelines.map(pipeline => pipeline.id))
  const sorted = useMemo(() => {
    const value = (pipeline: ReleasePipeline): string | number => sort.key === 'name' ? pipeline.name.toLocaleLowerCase()
      : sort.key === 'type' ? pipeline.type
        : sort.key === 'releases' ? summaries.get(pipeline.id)?.releaseCount ?? 0
          : summaries.get(pipeline.id)?.latestCompletedAt ?? ''
    return [...pipelines].sort((left, right) => {
      const a = value(left), b = value(right)
      const comparison = typeof a === 'number' && typeof b === 'number' ? a - b : String(a).localeCompare(String(b))
      return sort.direction === 'asc' ? comparison : -comparison
    })
  }, [pipelines, sort, summaries])
  const sortBy = (key: SortKey) => setSort(current => current.key === key
    ? { key, direction: current.direction === 'asc' ? 'desc' : 'asc' }
    : { key, direction: DESC_FIRST.includes(key) ? 'desc' : 'asc' })
  if (!pipelines.length) return <div className="flow-pipelines-settings-empty">
    <span>{t(filtering ? 'No matching pipelines' : 'No pipelines')}</span>
    {filtering && <button type="button" className="flow-pipelines-settings-pill" onClick={onClearFilter}>{t('Clear filters')}</button>}
  </div>
  const header = (key: SortKey, label: string) => <button type="button" className="flow-pipelines-settings-sort" aria-label={`${t('Order by')} ${t(label)}`} onClick={() => sortBy(key)}>
    <span>{t(label)}</span>{sort.key === key && (sort.direction === 'asc' ? <ArrowDown/> : <ArrowUp/>)}
  </button>
  const selectedPipelines = pipelines.filter(pipeline => selection.selected.includes(pipeline.id))
  const canCreate = canCreateReleasePipeline(data)
  const bulkActions: BulkAction[] = [
    ...(selectedPipelines.length === 1 ? [
      { id: 'view', label: t('View releases'), run: () => onOpenReleases(selectedPipelines[0]) },
      ...(canCreate ? [{ id: 'duplicate', label: t('Duplicate pipeline…'), run: () => onDuplicate(selectedPipelines[0]) }] : []),
    ] : []),
    ...(selectedPipelines.length && selectedPipelines.every(pipeline => canAdministerReleasePipeline(data, pipeline))
      ? [{ id: 'delete', label: selectedPipelines.length === 1 ? t('Delete pipeline') : t('Delete {count} pipelines').replace('{count}', String(selectedPipelines.length)), run: () => setDeleting(selectedPipelines) }] : []),
  ]
  const rowActions = (pipeline: ReleasePipeline, Item: typeof DropdownMenuItem | typeof ContextMenuItem, Separator: typeof DropdownMenuSeparator | typeof ContextMenuSeparator) => <>
    <Item onSelect={() => onOpenReleases(pipeline)}><ReleasesIcon size={14}/><span>{t('View releases')}</span></Item>
    {canCreate && <Item onSelect={() => onDuplicate(pipeline)}><LinearGlyph name="copy" size={14}/><span>{t('Duplicate…')}</span></Item>}
    {canAdministerReleasePipeline(data, pipeline) && <><Separator/><Item onSelect={() => setDeleting([pipeline])}><LinearGlyph name="delete" size={14}/><span>{t('Delete')}</span></Item></>}
  </>
  return <div className={`flow-pipelines-settings-table${selection.selected.length ? ' has-selection' : ''}`} role="table" aria-label={t('Release pipelines')}>
    <div className="flow-pipelines-settings-head" role="row">
      <span/>{header('name', 'Pipeline name')}<span>{t('Teams')}</span>{header('type', 'Type')}{header('releases', 'Releases')}{header('latest', 'Latest release')}<span/>
    </div>
    {sorted.map(pipeline => {
      const summary = summaries.get(pipeline.id) ?? { releaseCount: 0 }
      const count = summary.releaseCount
      const countLabel = t(count === 1 ? 'View 1 release' : 'View {count} releases').replace('{count}', String(count))
      const selected = selection.selected.includes(pipeline.id)
      return <ContextMenu key={pipeline.id}>
        <ContextMenuTrigger asChild>
          <div className={`flow-pipelines-settings-row${selected ? ' is-selected' : ''}`} role="row" aria-selected={selected} tabIndex={0} onClick={() => selection.selected.length ? selection.toggle(pipeline.id) : onOpen(pipeline)} onKeyDown={event => { if (event.target !== event.currentTarget) return; if (event.key === 'Enter') onOpen(pipeline); if (event.key === 'x') selection.toggle(pipeline.id) }}>
            <SelectBox checked={selected} label={t('Select pipeline')} onToggle={() => selection.toggle(pipeline.id)}/>
            <span className="flow-pipelines-settings-name" role="cell"><strong data-i18n-ignore>{pipeline.name}</strong>{pipeline.production && <em>{t('Production')}</em>}</span>
            <span role="cell"><PipelineTeamsLabel teams={pipeline.teamIds.map(id => data.teams.find(team => team.id === id)).filter((team): team is Team => Boolean(team))}/></span>
            <span role="cell" className="flow-pipelines-settings-muted">{t(pipeline.type === 'scheduled' ? 'Scheduled' : 'Continuous')}</span>
            <span role="cell"><FlowTooltip label={countLabel}>
              <button type="button" className="flow-pipelines-settings-count" aria-label={countLabel} onClick={(event: MouseEvent) => { event.stopPropagation(); onOpenReleases(pipeline) }}>{count.toLocaleString()}</button>
            </FlowTooltip></span>
            <span role="cell" className="flow-pipelines-settings-muted">{summary.latestCompletedAt && <FlowTooltip label={formatDate(summary.latestCompletedAt, { dateStyle: 'medium', timeStyle: 'short' })}><time dateTime={summary.latestCompletedAt}>{formatRelative(summary.latestCompletedAt)}</time></FlowTooltip>}</span>
            <span role="cell" onClick={event => event.stopPropagation()}>
              <RowMenu label={`${t('Open menu')} ${pipeline.name}`}>{rowActions(pipeline, DropdownMenuItem, DropdownMenuSeparator)}</RowMenu>
            </span>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="flow-pipelines-settings-menu">{rowActions(pipeline, ContextMenuItem, ContextMenuSeparator)}</ContextMenuContent>
      </ContextMenu>
    })}
    <PipelineBulkBar count={selectedPipelines.length} actions={bulkActions} onClear={selection.clear}/>
    {deleting && <PipelineDeleteDialog pipelines={deleting} onClose={() => setDeleting(undefined)} onDeleted={async () => { selection.clear(); await onDeleted() }} onViewDeleted={onViewDeleted}/>}
  </div>
}

function deletedPipelinePayload(entry: TrashEntry): Partial<ReleasePipeline> {
  const payload = entry.payload
  return payload && typeof payload === 'object' ? payload as Partial<ReleasePipeline> : {}
}

/** Recently deleted pipelines: name, teams, type, deleted date and a restore action. */
export function DeletedPipelineRows({ data, entries, filtering, onRestored }: { data: BootstrapData; entries: TrashEntry[]; filtering: boolean; onRestored: () => Promise<void> }) {
  const { t, formatDate, formatRelative } = useI18n()
  const selection = useSelection(entries.map(entry => entry.id))
  if (!entries.length) return <div className="flow-pipelines-settings-empty"><span>{t(filtering ? 'No matching pipelines' : 'No recently deleted pipelines')}</span></div>
  const restorable = (entry: TrashEntry) => {
    const teamIds = deletedPipelinePayload(entry).teamIds
    return canAdministerReleasePipeline(data, { teamIds: Array.isArray(teamIds) ? teamIds.filter((id): id is string => typeof id === 'string') : [] })
  }
  const restore = async (items: TrashEntry[]) => {
    try {
      for (const entry of items) await restoreTrashEntry(entry.id)
      selection.clear()
      await onRestored()
      toast.success(items.length === 1 ? t('Pipeline restored') : t('{count} pipelines restored').replace('{count}', String(items.length)))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not restore release pipeline'))
    }
  }
  const selected = entries.filter(entry => selection.selected.includes(entry.id))
  const restoreLabel = (count: number) => count === 1 ? t('Restore pipeline') : t('Restore {count} pipelines').replace('{count}', String(count))
  return <div className={`flow-pipelines-settings-table is-deleted${selection.selected.length ? ' has-selection' : ''}`} role="table" aria-label={t('Recently deleted pipelines')}>
    <div className="flow-pipelines-settings-head" role="row"><span/><span>{t('Pipeline name')}</span><span>{t('Teams')}</span><span>{t('Type')}</span><span>{t('Deleted')}</span><span/></div>
    {[...entries].sort((left, right) => right.deletedAt.localeCompare(left.deletedAt)).map(entry => {
      const payload = deletedPipelinePayload(entry)
      const teamIds = Array.isArray(payload.teamIds) ? payload.teamIds.filter((id): id is string => typeof id === 'string') : []
      const isSelected = selection.selected.includes(entry.id)
      return <ContextMenu key={entry.id}>
        <ContextMenuTrigger asChild disabled={!restorable(entry)}>
          <div className={`flow-pipelines-settings-row${isSelected ? ' is-selected' : ''}`} role="row" aria-selected={isSelected} tabIndex={0} onClick={() => { if (selection.selected.length) selection.toggle(entry.id) }}>
            <SelectBox checked={isSelected} label={t('Select pipeline')} onToggle={() => selection.toggle(entry.id)}/>
            <span className="flow-pipelines-settings-name" role="cell"><strong data-i18n-ignore>{entry.title}</strong>{payload.production && <em>{t('Production')}</em>}</span>
            <span role="cell"><PipelineTeamsLabel teams={teamIds.map(id => data.teams.find(team => team.id === id)).filter((team): team is Team => Boolean(team))}/></span>
            <span role="cell" className="flow-pipelines-settings-muted">{payload.type ? t(payload.type === 'scheduled' ? 'Scheduled' : 'Continuous') : ''}</span>
            <span role="cell" className="flow-pipelines-settings-muted"><FlowTooltip label={formatDate(entry.deletedAt, { dateStyle: 'medium', timeStyle: 'short' })}><time dateTime={entry.deletedAt}>{formatRelative(entry.deletedAt)}</time></FlowTooltip></span>
            <span role="cell" onClick={event => event.stopPropagation()}>{restorable(entry) && <RowMenu label={`${t('Open menu')} ${entry.title}`}>
              <DropdownMenuItem onSelect={() => void restore([entry])}><ArchiveRestore size={14}/><span>{t('Restore pipeline')}</span></DropdownMenuItem>
            </RowMenu>}</span>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="flow-pipelines-settings-menu"><ContextMenuItem onSelect={() => void restore([entry])}><ArchiveRestore size={14}/><span>{t('Restore pipeline')}</span></ContextMenuItem></ContextMenuContent>
      </ContextMenu>
    })}
    <PipelineBulkBar count={selected.length} actions={selected.length && selected.every(restorable) ? [{ id: 'restore', label: restoreLabel(selected.length), run: () => void restore(selected) }] : []} onClear={selection.clear}/>
  </div>
}

function RowMenu({ label, children }: { label: string; children: ReactNode }) {
  return <DropdownMenu>
    <DropdownMenuTrigger asChild><button type="button" className="flow-pipelines-settings-menu-trigger" aria-label={label}><LinearGlyph name="ellipsis" size={16}/></button></DropdownMenuTrigger>
    <DropdownMenuContent align="end" className="flow-pipelines-settings-menu">{children}</DropdownMenuContent>
  </DropdownMenu>
}

/** Linear's ReleasePipelineTeamsLabel: team icon (or a group mark) and up to two keys, "+N" for the rest. */
export function PipelineTeamsLabel({ teams }: { teams: Team[] }) {
  if (!teams.length) return null
  const label = <span className="flow-pipeline-teams-label" data-i18n-ignore>
    {teams.length === 1 ? <TeamIcon team={teams[0]} size={14}/> : <TeamIcon size={14}/>}
    <span>{teams.slice(0, 2).map(team => team.key).join(', ')}{teams.length > 2 ? ` +${teams.length - 2}` : ''}</span>
  </span>
  return <FlowTooltip label={teams.map(team => team.name).join(', ')} side="bottom" align="start">{label}</FlowTooltip>
}

export function PipelinesEmptyState() {
  const { t } = useI18n()
  return <div className="flow-pipelines-settings-zero">
    <PipelineEmptyIllustration className="flow-pipelines-settings-art"/>
    <h3>{t('Create release pipelines')}</h3>
    <p>{t("Create a new release pipeline to manage upcoming releases and track what's ready to ship.")}</p>
    <p>{t('Continuous pipelines capture changes as they deploy. Scheduled pipelines model planned releases with stages, target dates, and freezes.')}</p>
    <a className="flow-pipelines-settings-pill" href={RELEASES_DOCS_URL} target="_blank" rel="noreferrer">{t('Documentation')}</a>
  </div>
}
