import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { MoreHorizontal, Plus, Star, X } from 'lucide-react'
import { Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'

import { DocumentGlyph } from '@/components/documents/document-icon'
import { MentionTextField } from '@/components/editor/mention-text-field'
import { MentionBody } from '@/components/editor/mentions/mention-body'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { MyIssuesDisplayMenu } from '@/components/my-issues/my-issues-display-menu'
import { defaultMyIssuesDisplayOptions } from '@/components/my-issues/my-issues-display-defaults'
import { MyIssuesFilterBar, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-bar'
import { MyIssuesFilterMenu } from '@/components/my-issues/my-issues-filter-menu'
import { MyIssuesList, type MyIssuesEditableProperty } from '@/components/my-issues/my-issues-list'
import type { MyIssuesDisplayOptions, MyIssuesFilterKey, MyIssuesFilterOption } from '@/components/my-issues/my-issues-surface'
import { toggleFilterOption, updateFilterOperator, updateFilterValues } from '@/components/my-issues/my-issues-filter-types'
import { ISSUE_FILTER_LABELS, applyExplorerFilters, buildExplorerIssueGroups, explorerFilterOptions, explorerPropertyOptions, explorerUpdateForProperty, issueToExplorerRow } from '@/components/issue-explorer/issue-explorer-model'
import { AgentWriteGlyph } from '@/components/ui/agent-glyph'
import { useLinearHotkeys } from '@/components/ui/menu-shortcuts'
import { LinearDropdownMenuContent, LinearMenuItem } from '@/components/ui/row-context-menu'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { FilterIcon } from '@/components/ui/view-action-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { AgentChatPanel } from '@/lib/route-pages'
import { canCreateReleasePipeline } from '@/lib/settings-access'
import { createDocument, createReleaseNote, listIssueRecords, recordRecentResource, updateIssue, updateRelease, updateReleaseNote } from '@/lib/api'
import { documentPath, releasePath, releasePipelinePath, releasePipelinesPath, type ReleaseRouteTab } from '@/lib/app-routes'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, Release, ReleasePipeline, ReleaseResource } from '@/types/flow'

import { ReleaseIssuesIllustration } from './release-illustrations'
import { DetailsPanelIcon, ReleaseStatusIcon } from './release-icons'
import { ReleaseActionsDropdown, ReleaseStageOptions } from './release-menus'
import { HeaderIconButton, ReleaseBasicDialog, ReleaseBreadcrumbs, ReleaseEmptyState, ReleasesHeader, ToolbarPillButton } from './release-page-chrome'
import { ReleaseDateCell } from './release-pipeline-view'
import { releaseIssueQuery, releaseStatusForStage } from './release-view-model'
import { isFavorite } from './release-actions-model'
import { useReleaseActions } from './use-release-actions'

type Props = { data: BootstrapData; pipeline: ReleasePipeline; release: Release; tab: ReleaseRouteTab; onNavigate: (path: string) => void; onOpenSidebar: () => void; onReload: () => Promise<void> }

/** Linear ReleasePage: Issues / Release notes tabs plus the release details card. */
export function ReleaseDetailPage({ data, pipeline, release, tab, onNavigate, onOpenSidebar, onReload }: Props) {
  const { t } = useI18n()
  const workspace = data.workspace.urlKey
  const [detailsOpen, setDetailsOpen] = useState(true)
  const favorite = isFavorite(data, 'release', release.id)
  const { actionsFor, dialogs, saveResources } = useReleaseActions({ data, onReload, onNavigate, afterDelete: () => onNavigate(releasePipelinePath(workspace, pipeline.slugId)) })
  const actions = actionsFor(pipeline, release)
  const route = (next: ReleaseRouteTab) => onNavigate(releasePath(workspace, pipeline.slugId, release.slugId, next))
  useEffect(() => { void recordRecentResource('release', release.id) }, [release.id])
  useLinearHotkeys({
    '⌥ R': actions.onAddIssues,
    'Ctrl L': actions.onAddLink,
    '⌥ F': actions.onToggleFavorite,
    '⌘ ⇧ ,': actions.onCopyUrl,
    '1': () => route('issues'),
    '2': () => route('release-notes'),
  })
  const stageStatus = releaseStatusForStage(pipeline, release.stage ?? '', release.status)
  const crumbs = [
    { label: t('Releases'), translate: true, href: releasePipelinesPath(workspace), onClick: () => onNavigate(releasePipelinesPath(workspace)) },
    { label: pipeline.name, href: releasePipelinePath(workspace, pipeline.slugId), onClick: () => onNavigate(releasePipelinePath(workspace, pipeline.slugId)) },
    { label: release.name, icon: <ReleaseStatusIcon status={stageStatus}/> },
  ]
  const tabLink = (value: ReleaseRouteTab, label: string, key: string) => {
    const href = releasePath(workspace, pipeline.slugId, release.slugId, value)
    return <ScopedFlowTooltip label={t(`View ${label}`)} shortcut={key}><a aria-current={tab === value ? 'page' : undefined} className="flow-releases-tab ui-pill" href={href} role="tab" aria-selected={tab === value} onClick={event => { if (event.metaKey || event.ctrlKey || event.button !== 0) return; event.preventDefault(); route(value) }}>{t(label)}</a></ScopedFlowTooltip>
  }
  const issuesState = useReleaseIssues(data, release)
  const [filterOpen, setFilterOpen] = useState(false)
  const [displayOpen, setDisplayOpen] = useState(false)
  const [filters, setFilters] = useState<MyIssuesAppliedFilter[]>([])
  const [display, setDisplay] = useState<MyIssuesDisplayOptions>(() => ({ ...defaultMyIssuesDisplayOptions, grouping: 'status', completedWindow: 'all', properties: new Set(defaultMyIssuesDisplayOptions.properties) }))
  const issueOptions = useMemo(() => explorerPropertyOptions(data, issuesState.issues), [data, issuesState.issues])
  const toggleIssueFilter = (field: MyIssuesFilterKey, option: MyIssuesFilterOption) => { const label = ISSUE_FILTER_LABELS[field]; if (label) setFilters(current => toggleFilterOption(current, field, label, option)) }
  return <main className="main-panel flow-releases-page flow-release-page" aria-label={`${pipeline.name} ${release.name}`}>
    <ReleasesHeader onOpenSidebar={onOpenSidebar}>
      <ReleaseBreadcrumbs crumbs={crumbs}/>
      <HeaderIconButton aria-pressed={favorite} className="flow-releases-favorite" label={t(favorite ? 'Remove from favorites' : 'Add to favorites')} onClick={actions.onToggleFavorite}><Star fill={favorite ? 'currentColor' : 'none'}/></HeaderIconButton>
      <ReleaseActionsDropdown pipeline={pipeline} release={release} favorite={favorite} actions={actions} trigger={<HeaderIconButton label={t('Release options')} tooltip=""><MoreHorizontal/></HeaderIconButton>}/>
    </ReleasesHeader>
    <div className="flow-releases-subheader">
      <nav className="flow-releases-tabs" role="tablist" aria-label={t('Release views')}>{tabLink('issues', 'Issues', '1')}{tabLink('release-notes', 'Release notes', '2')}</nav>
      <div className="flow-releases-subheader__actions">
        {tab === 'issues' && <>
          <MyIssuesFilterMenu open={filterOpen} onOpenChange={open => { setFilterOpen(open); if (open) setDisplayOpen(false) }} filters={filters} options={field => explorerFilterOptions(field, issueOptions)} onToggle={toggleIssueFilter} trigger={<ToolbarPillButton label={t('Add filter')} tooltip={t('Filter')} shortcut="F"><FilterIcon/></ToolbarPillButton>}/>
          <MyIssuesDisplayMenu open={displayOpen} onOpenChange={open => { setDisplayOpen(open); if (open) setFilterOpen(false) }} options={display} onChange={setDisplay}/>
        </>}
        <ToolbarPillButton aria-pressed={detailsOpen} label={t(detailsOpen ? 'Close release details' : 'Open release details')} onClick={() => setDetailsOpen(value => !value)}><DetailsPanelIcon/></ToolbarPillButton>
      </div>
    </div>
    <div className={`flow-release-page__body${detailsOpen ? ' has-details' : ''}`}>
      <section className="flow-release-page__content">
        {tab === 'issues'
          ? <ReleaseIssuesTab data={data} release={release} pipeline={pipeline} state={issuesState} filters={filters} setFilters={setFilters} issueOptions={issueOptions} display={display} onAddIssues={actions.onAddIssues} onAddFilter={() => setFilterOpen(true)} onNavigate={onNavigate} onReload={onReload}/>
          : <ReleaseNotesTab data={data} pipeline={pipeline} release={release} issues={issuesState.issues} onReload={onReload}/>}
      </section>
      {detailsOpen && <ReleaseDetailsPanel data={data} pipeline={pipeline} release={release} favorite={favorite} actions={actions} issues={tab === 'issues' ? issuesState.issues : undefined} onAddLink={actions.onAddLink} onSaveResources={next => saveResources(release, next)} onNavigate={onNavigate} onReload={onReload}/>}
    </div>
    {dialogs}
  </main>
}

type IssuesState = { issues: Issue[]; loading: boolean }

function useReleaseIssues(data: BootstrapData, release: Release): IssuesState {
  const { t } = useI18n()
  const [loaded, setLoaded] = useState<Issue[]>([])
  const [loading, setLoading] = useState(() => release.issueIds.length > 0)
  const scope = useRef('')
  const key = release.issueIds.join(',')
  useEffect(() => {
    const next = `${release.id}:${key}`
    if (scope.current !== next) { scope.current = next; setLoaded([]) }
    if (!key) { setLoaded([]); setLoading(false); return }
    const controller = new AbortController()
    let cancelled = false
    setLoading(true)
    void (async () => {
      const collected: Issue[] = []
      let cursor: string | undefined
      try {
        do {
          const page = await listIssueRecords(releaseIssueQuery(release.id, cursor), controller.signal)
          collected.push(...page.items)
          cursor = page.hasMore ? page.nextCursor : undefined
        } while (cursor && !cancelled)
        if (!cancelled) setLoaded(collected)
      } catch (error) {
        if (!cancelled && !(error instanceof DOMException && error.name === 'AbortError')) toast.error(error instanceof Error ? error.message : t('Could not load issues'))
      } finally { if (!cancelled) setLoading(false) }
    })()
    return () => { cancelled = true; controller.abort() }
  }, [key, data.issueCollectionRevision, release.id, t])
  return { issues: loaded, loading }
}

function ReleaseIssuesTab({ data, release, pipeline, state, filters, setFilters, issueOptions, display, onAddIssues, onAddFilter, onNavigate, onReload }: { data: BootstrapData; release: Release; pipeline: ReleasePipeline; state: IssuesState; filters: MyIssuesAppliedFilter[]; setFilters: (update: (current: MyIssuesAppliedFilter[]) => MyIssuesAppliedFilter[]) => void; issueOptions: ReturnType<typeof explorerPropertyOptions>; display: MyIssuesDisplayOptions; onAddIssues: () => void; onAddFilter: () => void; onNavigate: (path: string) => void; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const visible = useMemo(() => applyExplorerFilters(state.issues, filters, data), [data, filters, state.issues])
  const rows = useMemo(() => visible.map(issue => issueToExplorerRow(issue, data.workspace.urlKey, data.issues, data)), [data, visible])
  const groups = useMemo(() => buildExplorerIssueGroups(rows, display, data), [data, display, rows])
  const change = async (row: ReturnType<typeof issueToExplorerRow>, property: MyIssuesEditableProperty, value: string | string[]) => {
    const input = explorerUpdateForProperty(property, value)
    if (!input) return
    try { await updateIssue(row.id, input); await onReload() } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update issue')) }
  }
  const scheduled = pipeline.type === 'scheduled'
  return <div className="flow-release-issues">
    {filters.length > 0 && <MyIssuesFilterBar filters={filters} filterOptions={filter => explorerFilterOptions(filter.field, issueOptions)} onAdd={onAddFilter} onClear={() => setFilters(() => [])} onOperatorChange={(id, operator) => setFilters(current => updateFilterOperator(current, id, operator))} onRemove={id => setFilters(current => current.filter(filter => filter.id !== id))} onValuesChange={(id, options) => setFilters(current => updateFilterValues(current, id, options))}/>}
    {state.loading && !state.issues.length
      ? <div className="flow-releases-loading" role="status">{t('Loading issues')}</div>
      : state.issues.length
        ? <MyIssuesList groups={groups} collapsedGroupIds={collapsed} displayProperties={display.properties} nestedSubIssues={display.nestedSubIssues} propertyOptions={issueOptions} onGroupCollapsedChange={(id, isCollapsed) => setCollapsed(current => { const next = new Set(current); if (isCollapsed) next.add(id); else next.delete(id); return next })} onOpenIssue={row => onNavigate(row.href ?? `/${data.workspace.urlKey}/issue/${row.identifier}`)} onPropertyChange={change}/>
        : <ReleaseEmptyState art={<ReleaseIssuesIllustration/>} title={<span data-i18n-ignore>{release.name}</span>} paragraphs={[t(scheduled ? 'No issues in this release yet.' : 'No issues are associated with this release.')]} primary={scheduled ? { label: 'Add issues', onClick: onAddIssues, shortcut: '⌥ R' } : undefined}/>}
  </div>
}

function ReleaseNotesTab({ data, pipeline, release, issues, onReload }: { data: BootstrapData; pipeline: ReleasePipeline; release: Release; issues: Issue[]; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const note = data.releaseNotes?.find(item => item.releaseId === release.id)
  const canManage = canCreateReleasePipeline(data)
  const initial = note?.body ?? release.releaseNotes ?? ''
  const [notes, setNotes] = useState(initial)
  const [title, setTitle] = useState(note?.title || release.name)
  const [agentOpen, setAgentOpen] = useState(() => new URLSearchParams(window.location.search).get('agent') === '1')
  const saved = useRef({ notes: initial, title: note?.title || release.name })
  const noteId = useRef(note?.id)
  noteId.current = note?.id ?? noteId.current
  const save = async (nextNotes: string, nextTitle: string) => {
    if (nextNotes === saved.current.notes && nextTitle === saved.current.title) return
    saved.current = { notes: nextNotes, title: nextTitle }
    try {
      if (noteId.current) await updateReleaseNote(release.id, noteId.current, { body: nextNotes, title: nextTitle })
      else { const created = await createReleaseNote(release.id, { title: nextTitle, body: nextNotes }); noteId.current = created?.id }
      await updateRelease(release.id, { releaseNotes: nextNotes })
      await onReload()
    } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not save release notes')) }
  }
  const latest = useRef({ notes, title })
  latest.current = { notes, title }
  useEffect(() => {
    const timer = window.setTimeout(() => void save(notes, title), 800)
    return () => window.clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- autosave on edits only
  }, [notes, title])
  // eslint-disable-next-line react-hooks/exhaustive-deps -- flush the last edit when leaving the tab
  useEffect(() => () => { void save(latest.current.notes, latest.current.title) }, [])
  const stage = release.stage
  const empty = !issues.length && !release.issueIds.length
  return <div className="flow-release-notes">
    <div className="flow-release-notes__column">
      <input aria-label={t('Release notes title')} className="flow-release-notes__title" readOnly={!canManage} value={title} onChange={event => setTitle(event.target.value)} data-i18n-ignore/>
      <div className="flow-release-notes__meta">
        <span className="flow-release-notes__release">
          {release.version && <span className="flow-release-version is-chip" data-i18n-ignore>{release.version}</span>}
          {stage && <span className="flow-release-notes__stage"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, stage, release.status)}/><span data-i18n-ignore>{stage}</span></span>}
        </span>
        {canManage && !notes.trim() && <ScopedFlowTooltip label={empty ? t('Add issues to this release to write release notes') : undefined}><span><button className="flow-releases-secondary is-small" disabled={empty} onClick={() => setAgentOpen(true)} type="button"><AgentWriteGlyph size={14}/>{t('Write with Agent')}</button></span></ScopedFlowTooltip>}
      </div>
      <MentionTextField ariaLabel={t('Release notes')} autoFocus={!initial} className="flow-release-notes__editor" onChange={setNotes} onBlur={() => void save(notes, title)} onSubmit={() => void save(notes, title)} placeholder={t(canManage ? 'Write release notes…' : 'Release notes are read-only')} value={notes}/>
    </div>
    {agentOpen && <Suspense fallback={null}><AgentChatPanel autoSubmit data={data} initialPrompt={t('Write these release notes')} issues={[]} open onClose={() => setAgentOpen(false)} onUseResponse={content => { setNotes(content); setAgentOpen(false) }} useResponseLabel={t('Use as release notes')}/></Suspense>}
  </div>
}

function ReleaseDetailsPanel({ data, pipeline, release, favorite, actions, issues, onAddLink, onSaveResources, onNavigate, onReload }: { data: BootstrapData; pipeline: ReleasePipeline; release: Release; favorite: boolean; actions: ReturnType<ReturnType<typeof useReleaseActions>['actionsFor']>; issues?: Issue[]; onAddLink: () => void; onSaveResources: (next: ReleaseResource[]) => Promise<void>; onNavigate: (path: string) => void; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const canManage = canCreateReleasePipeline(data)
  const scheduled = pipeline.type === 'scheduled'
  const [stageOpen, setStageOpen] = useState(false)
  const [viewing, setViewing] = useState<ReleaseResource>()
  const [detailTab, setDetailTab] = useState<'assignees' | 'labels' | 'priority' | 'projects'>('assignees')
  const resources = release.resources ?? []
  const stage = release.stage || pipeline.stages[0] || ''
  const status = releaseStatusForStage(pipeline, stage, release.status)
  const createDocumentResource = async () => {
    try {
      const document = await createDocument({ title: release.name })
      await onSaveResources([...resources, { id: `release_resource_${Date.now()}`, type: 'document', title: document.title, documentId: document.id, createdAt: new Date().toISOString() }])
      onNavigate(documentPath(data.workspace.urlKey, document))
    } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not create document')) }
  }
  const breakdown = useMemo(() => {
    const list = issues ?? []
    const count = <K extends string>(entries: { id: K; label: string }[]) => [...entries.reduce((map, entry) => map.set(entry.id, { ...entry, count: (map.get(entry.id)?.count ?? 0) + 1 }), new Map<K, { id: K; label: string; count: number }>()).values()]
    if (detailTab === 'assignees') return count(list.filter(issue => issue.assignee).map(issue => ({ id: issue.assignee!.id, label: issue.assignee!.displayName })))
    if (detailTab === 'labels') return count(list.flatMap(issue => issue.labels.map(label => ({ id: label.id, label: label.name }))))
    if (detailTab === 'priority') return count(list.map(issue => ({ id: String(issue.priority), label: issue.priorityLabel })))
    return count(list.filter(issue => issue.project).map(issue => ({ id: issue.project!.id, label: issue.project!.name })))
  }, [detailTab, issues])
  return <aside className="flow-release-details" aria-label={t('Release details')}>
    <div className="flow-release-details__card">
      {release.version && release.version !== release.name && <span className="flow-release-details__version" data-i18n-ignore>{release.version}</span>}
      <div className="flow-release-details__title-row">
        <strong className="flow-release-details__title" data-i18n-ignore>{release.name}</strong>
        <span className="flow-release-details__title-actions">
          <HeaderIconButton aria-pressed={favorite} className="flow-releases-favorite" label={t(favorite ? 'Remove from favorites' : 'Add to favorites')} onClick={actions.onToggleFavorite}><Star fill={favorite ? 'currentColor' : 'none'}/></HeaderIconButton>
          <ReleaseActionsDropdown align="end" label={t('Open menu')} pipeline={pipeline} release={release} favorite={favorite} actions={actions} trigger={<HeaderIconButton className="is-small" label={t('Open menu')} tooltip=""><MoreHorizontal/></HeaderIconButton>}/>
        </span>
      </div>
      <div className="flow-release-details__properties">
        {scheduled && canManage
          ? <DropdownMenu.Root open={stageOpen} onOpenChange={setStageOpen}>
            <DropdownMenu.Trigger asChild><button className="flow-release-property" aria-label={`${t('Change release stage')} ${stage}`} type="button"><ReleaseStatusIcon status={status}/><span data-i18n-ignore>{stage}</span></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><LinearDropdownMenuContent label={t('Change stage…')} className="flow-release-stage-menu"><ReleaseStageOptions pipeline={pipeline} value={stage} onChoose={next => { setStageOpen(false); actions.onStage(next) }}/></LinearDropdownMenuContent></DropdownMenu.Portal>
          </DropdownMenu.Root>
          : stage && <span className="flow-release-property is-static"><ReleaseStatusIcon status={status}/><span data-i18n-ignore>{stage}</span></span>}
        {(scheduled || release.releasedAt) && <span className="flow-release-details__date"><ReleaseDateCell release={release} scheduled={scheduled} canManage={canManage} onReload={onReload} long/></span>}
      </div>
      {release.description && <MentionBody className="flow-release-details__description" body={release.description}/>}
      <div className="flow-release-resources">
        {resources.map(resource => {
          const document = data.documents.find(item => item.id === resource.documentId)
          const inline = resource.type === 'document' && !resource.documentId && Boolean(resource.content)
          const icon = resource.type === 'document' && document ? <DocumentGlyph document={document}/> : resource.type === 'document' ? <ViewGlyph icon="Page" color="currentColor"/> : <IssueActionGlyph label="Copy link" fallback={null}/>
          return <div className="flow-release-resource" key={resource.id}>
            {icon}
            {inline ? <button type="button" className="flow-release-resource__title" onClick={() => setViewing(resource)} data-i18n-ignore>{resource.title}</button>
              : <a className="flow-release-resource__title" href={resource.url || documentPath(data.workspace.urlKey, { slugId: document?.slugId || resource.documentId || '' })} data-i18n-ignore>{resource.title}</a>}
            {canManage && <button className="flow-release-resource__remove" aria-label={t('Remove')} onClick={() => void onSaveResources(resources.filter(item => item.id !== resource.id))} type="button"><X/></button>}
          </div>
        })}
        {canManage && <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild><button className="flow-release-resource-add" type="button"><Plus/><span>{t('Add document or link…')}</span></button></DropdownMenu.Trigger>
          <DropdownMenu.Portal><LinearDropdownMenuContent label={t('Add document or link…')} className="flow-release-actions-menu">
            <LinearMenuItem icon={<ViewGlyph icon="Page" color="currentColor"/>} label="Create new document…" onSelect={() => void createDocumentResource()}/>
            <LinearMenuItem icon={<IssueActionGlyph label="Copy link" fallback={null}/>} label="Add a link…" shortcut="Ctrl L" onSelect={onAddLink}/>
          </LinearDropdownMenuContent></DropdownMenu.Portal>
        </DropdownMenu.Root>}
      </div>
    </div>
    {issues && issues.length > 0 && <div className="flow-release-breakdown">
      <div className="flow-release-breakdown__tabs" role="tablist">{(['assignees', 'labels', 'priority', 'projects'] as const).map(value => <button aria-selected={detailTab === value} className="ui-pill" key={value} onClick={() => setDetailTab(value)} role="tab" type="button">{t(value === 'assignees' ? 'Assignees' : value === 'labels' ? 'Labels' : value === 'priority' ? 'Priority' : 'Projects')}</button>)}</div>
      <div className="flow-release-breakdown__values">{breakdown.map(value => <div key={value.id}><span data-i18n-ignore>{value.label}</span><b>{value.count}</b></div>)}{!breakdown.length && <p>{t('None')}</p>}</div>
    </div>}
    {viewing && <ReleaseBasicDialog title={<span data-i18n-ignore>{viewing.title}</span>} onClose={() => setViewing(undefined)}><MentionBody className="flow-release-resource-content" body={viewing.content ?? ''}/></ReleaseBasicDialog>}
  </aside>
}

