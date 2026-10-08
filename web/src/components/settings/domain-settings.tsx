import { AppLink } from '@/components/ui/app-link'
import { refreshResourcePreferences } from '@/lib/resource-preferences'
import { Archive, ChevronDown, ChevronRight, Merge, MoreHorizontal, RotateCcw, Search, Star, Trash2, X } from 'lucide-react'
import * as Popover from '@radix-ui/react-popover'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import {
  createLabelGroup, createTeamLabel, createWorkspaceLabel, deleteLabelGroup, deleteTeamLabel, deleteTeamLabelGroup,
  deleteWorkspaceLabel, mergeLabels, moveWorkspaceLabelToTeams, updateLabelGroup, updateTeamLabel, updateWorkspaceLabel,
} from '@/lib/api'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent,
  DropdownMenuSubTrigger, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { TeamIcon } from '@/components/issue/issue-icons'
import type { BootstrapData, IssueLabel, LabelGroup, LabelResourceType, Team } from '@/types/flow'
import { toggleFavoriteFor } from '@/lib/favorites'
import { groupsForResource, isWorkspaceLabel, labelResourceType } from '@/lib/labels'
import { labelPath } from '@/lib/app-routes'
import { useStoredState } from '@/hooks/use-stored-state'
import { useI18n } from '@/i18n/i18n'
import { FLOW_COLOR_PALETTE } from '@/components/ui/color-palette'

import {
  DEFAULT_LABEL_DISPLAY, EMPTY_LABEL_FILTERS, canMergeLabels, compareLabels, isFilteringLabels, labelGridTemplate, labelRuleUsage,
  labelRulesVisible, labelTeamUsage, labelUsage, matchesLabelFilters, mergeTarget, visibleLabelColumns,
  type LabelColumn, type LabelDisplayState, type LabelFilters, type LabelRuleUsage,
} from './label-settings-model'
import { LabelDisplayOptions, LabelFilterBar, LabelFilterMenu, LabelsEmptyState } from './label-settings-toolbar'

export { ProjectStatusesSettings } from './issues-projects-settings'

type Entry = { kind: 'label'; label: IssueLabel } | { kind: 'group'; group: LabelGroup; labels: IssueLabel[] }
interface LabelSection { id: string; label?: string; entries: Entry[] }

/** What a row needs to render Linear's value columns. */
interface RowContext {
  data: BootstrapData
  resourceType: LabelResourceType
  columns: LabelColumn[]
  usage: (label: IssueLabel) => number
  rules: (label: IssueLabel) => LabelRuleUsage
}

const NOUNS: Record<LabelResourceType, { one: string; many: string; zh: string }> = {
  issue: { one: 'issue', many: 'issues', zh: '事项' },
  project: { one: 'project', many: 'projects', zh: '项目' },
  initiative: { one: 'initiative', many: 'initiatives', zh: '目标' },
}

function useViewportWidth() {
  const [width, setWidth] = useState(() => typeof window === 'undefined' ? 1440 : window.innerWidth)
  useEffect(() => {
    const update = () => setWidth(window.innerWidth)
    window.addEventListener('resize', update)
    return () => window.removeEventListener('resize', update)
  }, [])
  return width
}

/** Labels table for the workspace, or for one team when `team` is set. */
export function DomainLabelsSettings({ data, resourceType, team, onReload }: { data: BootstrapData; resourceType: LabelResourceType; team?: Team; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const width = useViewportWidth()
  const [query, setQuery] = useState('')
  const [filters, setFilters] = useState<LabelFilters>(EMPTY_LABEL_FILTERS)
  const [storedDisplay, setStoredDisplay] = useStoredState<LabelDisplayState>(`flow:label-settings:${resourceType}:${team?.id ?? 'workspace'}`, DEFAULT_LABEL_DISPLAY, 'local')
  const display: LabelDisplayState = { ...DEFAULT_LABEL_DISPLAY, ...storedDisplay }
  const [creating, setCreating] = useState<{ kind: 'label'|'group'; groupId?: string }|null>(null)
  const [selected, setSelected] = useState<string[]>([])
  const [toggledScopes, setToggledScopes] = useState<string[]>([])
  const [deletedIds, setDeletedIds] = useState<Set<string>>(() => new Set())
  const [merging, setMerging] = useState<IssueLabel[]|null>(null)

  const activeTeams = useMemo(() => data.teams.filter(item => !item.archivedAt && !item.retiredAt), [data.teams])
  const teamIds = useMemo(() => new Set(data.teams.map(item => item.id)), [data.teams])
  const canScopeTeams = !team && resourceType !== 'initiative' && activeTeams.length > 0
  const includeTeamLabels = canScopeTeams && (display.showTeamLabels || filters.teams.length > 0)
  const grouping = canScopeTeams ? display.grouping : 'none'
  const filtering = isFilteringLabels(filters, query)
  const rulesVisible = labelRulesVisible(data, resourceType)
  const columns = visibleLabelColumns({ resourceType, rulesVisible, showTeamColumn: includeTeamLabels && grouping === 'none', showArchived: display.showArchived, width })
  const ordering = columns.includes(display.ordering) ? display.ordering : 'title'
  const setDisplay = (next: LabelDisplayState) => setStoredDisplay(next)

  const usageById = useMemo(() => new Map(data.labels.map(label => [label.id, labelUsage(label, data, resourceType)])), [data, resourceType])
  const usage = (label: IssueLabel) => usageById.get(label.id) ?? labelUsage(label, data, resourceType)
  const rulesById = useMemo(() => new Map(data.labels.map(label => [label.id, labelRuleUsage(label, data)])), [data])
  const rules = (label: IssueLabel) => rulesById.get(label.id) ?? labelRuleUsage(label, data)
  const teamName = (label: IssueLabel) => isWorkspaceLabel(label) ? '' : data.teams.find(item => item.id === label.scope)?.name ?? ''

  const scopeIncluded = (scope?: string) => {
    const workspace = !scope || scope.toLowerCase() === 'workspace' || !teamIds.has(scope)
    if (team) return scope === team.id
    return workspace || (includeTeamLabels && (!filters.teams.length || filters.teams.includes(scope ?? '') || filters.teamOperator === 'isNot'))
  }
  const allGroups = useMemo(() => groupsForResource(data.labelGroups, resourceType).filter(group => !deletedIds.has(group.id) && (team ? group.scope === team.id : !teamIds.has(group.scope ?? '') || canScopeTeams)), [data.labelGroups, resourceType, deletedIds, team, teamIds, canScopeTeams])
  const groups = allGroups.filter(group => scopeIncluded(group.scope) && (display.showArchived || !group.archivedAt))
  const groupIds = new Set(groups.map(group => group.id))
  const candidates = data.labels.filter(item => {
    if (deletedIds.has(item.id) || deletedIds.has(item.groupId ?? '')) return false
    if (labelResourceType(item) !== resourceType || !scopeIncluded(item.scope)) return false
    return display.showArchived || !item.archivedAt
  })
  const queryValue = query.trim().toLowerCase()
  const matches = (label: IssueLabel) => (!queryValue || label.name.toLowerCase().includes(queryValue) || (label.description ?? '').toLowerCase().includes(queryValue))
    && matchesLabelFilters(label, filters, { usage: usage(label), teamUsage: () => labelTeamUsage(label, data) })
  const sortContext = { usage, rules: (label: IssueLabel) => rules(label).total, teamName }
  const compare = (left: IssueLabel, right: IssueLabel) => compareLabels(left, right, ordering, display.descending, sortContext)
  const groupAsLabel = (group: LabelGroup, children: IssueLabel[]): IssueLabel => ({
    id: group.id, name: group.name, color: group.color, description: group.description, scope: group.scope, createdAt: group.createdAt, archivedAt: group.archivedAt,
    issueCount: children.reduce((sum, label) => sum + usage(label), 0), lastAppliedAt: children.map(label => label.lastAppliedAt ?? '').sort().at(-1) || undefined,
  })
  const entries: Entry[] = []
  for (const group of groups) {
    const children = candidates.filter(label => label.groupId === group.id)
    const visible = children.filter(matches).sort(compare)
    const groupMatches = !filtering || (Boolean(queryValue) && !filters.teams.length && !filters.lastApplied && group.name.toLowerCase().includes(queryValue))
    if (visible.length || groupMatches) entries.push({ kind: 'group', group, labels: visible })
  }
  for (const label of candidates) if ((!label.groupId || !groupIds.has(label.groupId)) && matches(label)) entries.push({ kind: 'label', label })
  const entryLabel = (entry: Entry) => entry.kind === 'label' ? entry.label : groupAsLabel(entry.group, entry.labels)
  entries.sort((left, right) => compareLabels(entryLabel(left), entryLabel(right), ordering, display.descending, { ...sortContext, rules: label => groupIds.has(label.id) ? 0 : rules(label).total, usage: label => groupIds.has(label.id) ? label.issueCount ?? 0 : usage(label) }))
  const scopeOf = (entry: Entry) => { const scope = entry.kind === 'label' ? entry.label.scope : entry.group.scope; return !scope || !teamIds.has(scope) ? 'workspace' : scope }
  const sections: LabelSection[] = grouping === 'team'
    ? [{ id: 'workspace', label: t('Workspace') }, ...activeTeams.slice().sort((a, b) => a.name.localeCompare(b.name)).map(item => ({ id: item.id, label: item.name }))]
      .map(section => ({ ...section, entries: entries.filter(entry => scopeOf(entry) === section.id) }))
      .filter(section => section.entries.length > 0)
    : [{ id: 'all', entries }]
  const scopeCollapsed = (id: string) => filtering ? toggledScopes.includes(id) : id === 'workspace' ? toggledScopes.includes(id) : !toggledScopes.includes(id)
  const hasRows = entries.length > 0

  const run = async <T,>(action: () => Promise<T>, preferencesOnly = false): Promise<T | undefined> => {
    try { const result = await action(); if (preferencesOnly) await refreshResourcePreferences(data.workspace.urlKey); else await onReload(); return result } catch (error) { toast.error(message(error)); return undefined }
  }
  const saveLabel = async (label: IssueLabel, input: Partial<IssueLabel>) => { await run(() => isWorkspaceLabel(label) ? updateWorkspaceLabel(label.id, input) : updateTeamLabel(label.scope!, label.id, input)) }
  const finishDeletion = (ids: string[], refresh: boolean) => {
    setDeletedIds(current => new Set([...current, ...ids]))
    setSelected(current => current.filter(id => !ids.includes(id)))
    // The acknowledged deletion is complete even if background reconciliation
    // is slow or unavailable. Keep the deleted rows hidden until it catches up.
    if (refresh) void onReload().catch(error => toast.error(message(error)))
  }
  const removeLabel = (label: IssueLabel) => isWorkspaceLabel(label) ? deleteWorkspaceLabel(label.id) : deleteTeamLabel(label.scope!, label.id)
  const deleteLabel = async (label: IssueLabel, refresh = true) => {
    await removeLabel(label)
    finishDeletion([label.id], refresh)
  }
  const removeGroup = (group: LabelGroup) => group.scope && teamIds.has(group.scope) ? deleteTeamLabelGroup(group.scope, group.id) : deleteLabelGroup(group.id)
  const deleteGroup = async (group: LabelGroup, refresh = true) => {
    await removeGroup(group)
    finishDeletion([group.id, ...data.labels.filter(label => label.groupId === group.id).map(label => label.id)], refresh)
  }
  const archiveLabel = (label: IssueLabel) => saveLabel(label, { archivedAt: label.archivedAt ? '' : new Date().toISOString() })
  const moveLabelToTeams = async (label: IssueLabel) => { await run(() => moveWorkspaceLabelToTeams(label.id)) }
  const createLabel = (input: NewLabelInput, groupId?: string, scope = team?.id) => scope
    ? createTeamLabel(scope, { ...input, color: input.color ?? '#5E6AD2', resourceType: resourceType === 'project' ? 'project' : 'issue', groupId })
    : createWorkspaceLabel({ ...input, resourceType, groupId })
  const convertLabelToGroup = async (label: IssueLabel) => {
    const done = await run(async () => {
      await createLabelGroup({ name: label.name, color: label.color, description: label.description, resourceType, scope: isWorkspaceLabel(label) ? undefined : label.scope })
      await removeLabel(label)
      return true
    })
    if (done) toast.success(t('Converted "{name}" to group').replace('{name}', label.name))
  }
  const convertGroupToLabel = async (group: LabelGroup) => {
    const scope = group.scope && teamIds.has(group.scope) ? group.scope : undefined
    const done = await run(async () => {
      await createLabel({ name: group.name, color: group.color, description: group.description ?? '' }, undefined, scope)
      await removeGroup(group)
      return true
    })
    if (done) toast.success(t('Converted "{name}" to label').replace('{name}', group.name))
  }
  const inheritedCount = team ? data.labels.filter(item => isWorkspaceLabel(item) && !item.archivedAt && labelResourceType(item) === resourceType).length : 0
  const noun = NOUNS[resourceType]
  const toggleSelected = (id: string) => setSelected(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id])
  const toggleGroupSelected = (group: LabelGroup, childIds: string[]) => setSelected(current => {
    const ids = [group.id, ...childIds]
    return current.includes(group.id) ? current.filter(id => !ids.includes(id)) : [...new Set([...current, ...ids])]
  })
  const context: RowContext = { data, resourceType, columns, usage, rules }
  const renderEntry = (entry: Entry) => entry.kind === 'group'
    ? <GroupSectionRows
      key={entry.group.id}
      entry={entry}
      context={context}
      availableGroups={groups}
      selected={selected}
      filtering={filtering}
      creatingGroupId={creating?.kind === 'label' ? creating.groupId : undefined}
      onCancelCreate={() => setCreating(null)}
      onCreateInGroup={groupId => setCreating({ kind: 'label', groupId })}
      onCreateLabel={async (input, groupId) => Boolean(await run(() => createLabel(input, groupId, entry.group.scope && teamIds.has(entry.group.scope) ? entry.group.scope : team?.id)))}
      onToggleGroupSelected={toggleGroupSelected}
      onToggleSelected={toggleSelected}
      onSaveLabel={saveLabel}
      onArchiveLabel={archiveLabel}
      onMoveLabelToTeams={moveLabelToTeams}
      onConvertLabel={convertLabelToGroup}
      onDeleteLabel={deleteLabel}
      onSaveGroup={async (group, input) => { await run(() => updateLabelGroup(group.id, input)) }}
      onArchiveGroup={async group => { await run(() => updateLabelGroup(group.id, { archivedAt: group.archivedAt ? '' : new Date().toISOString() })) }}
      onConvertGroup={convertGroupToLabel}
      onDeleteGroup={deleteGroup}
    />
    : <LabelRow
      key={entry.label.id}
      context={context}
      label={entry.label}
      grouped={false}
      selected={selected.includes(entry.label.id)}
      groups={groups}
      onToggleSelected={() => toggleSelected(entry.label.id)}
      onSave={input => saveLabel(entry.label, input)}
      onArchive={() => archiveLabel(entry.label)}
      onMoveToTeams={() => moveLabelToTeams(entry.label)}
      onConvert={() => convertLabelToGroup(entry.label)}
      onDelete={() => deleteLabel(entry.label)}
    />
  const selectedGroups = allGroups.filter(group => selected.includes(group.id))
  const selectedLabels = data.labels.filter(label => selected.includes(label.id))
  const mergeable = !selectedGroups.length && canMergeLabels(selectedLabels)
  const allArchived = selected.length > 0 && [...selectedGroups, ...selectedLabels].every(item => item.archivedAt)
  const bulk = async (action: 'favorite'|'archive'|'delete') => {
    const selectedGroupIds = new Set(selectedGroups.map(group => group.id))
    if (action === 'favorite') {
      const existing = new Set(data.favorites.filter(item => item.resourceType === 'label').map(item => item.resourceId))
      for (const id of selected.filter(id => !existing.has(id))) void toggleFavoriteFor(data, 'label', id, true)
    } else if (action === 'archive') {
      const archivedAt = allArchived ? '' : new Date().toISOString()
      await run(() => Promise.all([
        ...selectedGroups.map(group => updateLabelGroup(group.id, { archivedAt })),
        ...selectedLabels.filter(label => !selectedGroupIds.has(label.groupId ?? '')).map(label => isWorkspaceLabel(label) ? updateWorkspaceLabel(label.id, { archivedAt }) : updateTeamLabel(label.scope!, label.id, { archivedAt })),
      ]))
    } else {
      const results = await Promise.allSettled([
        ...selectedLabels.filter(label => !selectedGroupIds.has(label.groupId ?? '')).map(label => deleteLabel(label, false)),
        ...selectedGroups.map(group => deleteGroup(group, false)),
      ])
      if (results.some(result => result.status === 'fulfilled')) void onReload().catch(error => toast.error(message(error)))
      const failure = results.find(result => result.status === 'rejected')
      if (failure?.status === 'rejected') { toast.error(message(failure.reason)); return }
    }
    setSelected([])
  }
  const merge = async (labels: IssueLabel[]) => {
    const target = mergeTarget(labels, usage)
    const merged = await run(() => mergeLabels(target.id, labels.filter(label => label.id !== target.id).map(label => label.id)))
    if (!merged) return false
    finishDeletion(labels.filter(label => label.id !== target.id).map(label => label.id), false)
    toast.success(t('Labels merged'))
    return true
  }
  const startCreating = (kind: 'label'|'group') => { setCreating({ kind }); setSelected([]) }
  const columnLabel = (column: LabelColumn) => t(({ title: 'Name', description: 'Description', team: 'Team', rules: 'Rules', usage: resourceType === 'issue' ? 'Issues' : resourceType === 'project' ? 'Projects' : 'Initiatives', lastAppliedAt: 'Last applied', createdAt: 'Created', archivedAt: 'Archived' } as Record<LabelColumn, string>)[column])
  const sort = (column: LabelColumn) => setDisplay({ ...display, ordering: column, descending: column === ordering ? !display.descending : column === 'rules' || column === 'usage' || column === 'archivedAt' })

  return <TooltipProvider delayDuration={450} skipDelayDuration={300}><div className="domain-labels-page" data-i18n-ignore>
    <header className="settings-page-header domain-labels-header">
      <div><h1>{t(`${team ? 'Team ' + noun.one : noun.one.charAt(0).toUpperCase() + noun.one.slice(1)} labels`)}</h1></div>
      <div className="settings-header-actions">
        <button className="settings-action" onClick={() => startCreating('group')}>{t('New group')}</button>
        <button className="settings-action primary" onClick={() => startCreating('label')}>{t('New label')}</button>
      </div>
    </header>
    <div className="domain-labels-toolbar label-settings-toolbar">
      <div className="settings-list-toolbar domain-labels-search"><Search size={14}/><input aria-label={t('Filter by name…')} placeholder={t('Filter by name…')} value={query} onChange={event => setQuery(event.target.value)}/></div>
      <span/>
      <LabelFilterMenu teams={canScopeTeams ? activeTeams : []} filters={filters} onChange={setFilters}/>
      <LabelDisplayOptions
        display={{ ...display, ordering }}
        columns={columns.map(id => ({ id, label: columnLabel(id) }))}
        canGroup={canScopeTeams}
        canShowTeamLabels={canScopeTeams}
        teamLabelsLabel={t('Show team labels')}
        onChange={setDisplay}
      />
    </div>
    <LabelFilterBar teams={activeTeams} filters={filters} onChange={setFilters}/>
    <section className="settings-section domain-labels-section"><div className="domain-labels-grid" style={{ gridTemplateColumns: labelGridTemplate(columns, resourceType) }}>
      {(hasRows || creating || team) && <div className="domain-labels-table-header">
        {columns.map(column => <LabelSortHeader key={column} column={column} label={columnLabel(column)} active={ordering === column} descending={display.descending} onSort={() => sort(column)}/>)}
      </div>}
      {creating?.kind === 'group' && (
        <InlineLabelRow kind="group" showDescription={columns.includes('description')} onCancel={() => setCreating(null)} onSave={async input => { const group = await run(() => createLabelGroup({ ...input, resourceType, scope: team?.id })); if (group) setCreating({ kind: 'label', groupId: group.id }); return Boolean(group) }}/>
      )}
      {creating?.kind === 'label' && !creating.groupId && (
        <InlineLabelRow kind="label" showDescription={columns.includes('description')} onCancel={() => setCreating(null)} onSave={async input => { const label = await run(() => createLabel(input)); if (label) setCreating(null); return Boolean(label) }}/>
      )}
      {sections.map(section => section.label
        ? <div className="domain-label-scope-block" key={section.id}>
          <ScopeSectionHeader label={section.label} count={section.entries.length} collapsed={scopeCollapsed(section.id)} onToggle={() => setToggledScopes(current => current.includes(section.id) ? current.filter(id => id !== section.id) : [...current, section.id])}/>
          {!scopeCollapsed(section.id) && section.entries.map(renderEntry)}
        </div>
        : <div className="domain-label-scope-block" key={section.id}>{section.entries.map(renderEntry)}</div>)}
      {!hasRows && !creating && (team && !filtering
        ? <div className="domain-labels-empty is-team">{t(`This team doesn’t have any ${noun.one} labels yet`)}</div>
        : <LabelsEmptyState filtering={filtering}/>)}
      {team && !filtering && inheritedCount > 0 && <p className="domain-labels-inherited"><strong>{inheritedCount} {t(inheritedCount === 1 ? 'label' : 'labels')}</strong> {t('inherited from workspace')}</p>}
    </div></section>
    {selected.length > 0 && (
      <BulkLabelBar count={selected.length} archived={allArchived} canMerge={mergeable} onFavorite={() => void bulk('favorite')} onArchive={() => void bulk('archive')} onMerge={() => setMerging(selectedLabels)} onDelete={() => void bulk('delete')} onClear={() => setSelected([])}/>
    )}
    <MergeLabelsDialog labels={merging} resourceType={resourceType} usage={usage} onClose={() => setMerging(null)} onMerge={async labels => { if (await merge(labels)) { setMerging(null); setSelected([]) } }}/>
  </div></TooltipProvider>
}


function LabelSortHeader({ column, label, active, descending, onSort }: { column: LabelColumn; label: string; active: boolean; descending: boolean; onSort: () => void }) {
  const { locale } = useI18n()
  const state = active ? (locale === 'zh-CN' ? (descending ? '，降序' : '，升序') : `, sorted ${descending ? 'descending' : 'ascending'}`) : ''
  return <button style={{ gridColumn: column }} aria-label={locale === 'zh-CN' ? `按${label}排序${state}` : `Order by ${label}${state}`} className={active ? 'active' : ''} onClick={onSort}>{label}{active && <SortArrow className={descending ? 'descending' : 'ascending'}/>}</button>
}

function SortArrow({ className }: { className: string }) {
  return <svg aria-hidden="true" className={className} width="12" height="12" viewBox="0 0 16 16" fill="currentColor"><path d="M11.5361 10.2745C11.8024 10.0029 11.8249 9.56807 11.5762 9.26961C11.3275 8.97139 10.8961 8.91526 10.5811 9.12801L10.5195 9.17391L8.00001 11.2735L5.48048 9.17391L5.41895 9.12801C5.10388 8.91526 4.67252 8.97139 4.42384 9.26961C4.17512 9.56807 4.19758 10.0029 4.46387 10.2745L4.51954 10.3263L7.51954 12.8263C7.79767 13.058 8.20234 13.058 8.48048 12.8263L11.4805 10.3263L11.5361 10.2745Z"/><path d="M8.75 12.25C8.75 12.6642 8.41421 13 8 13C7.58579 13 7.25 12.6642 7.25 12.25L7.25 3.75C7.25 3.33579 7.58579 3 8 3C8.41421 3 8.75 3.33579 8.75 3.75V12.25Z"/></svg>
}

function GroupSectionRows({ entry, context, availableGroups, selected, filtering, creatingGroupId, onCancelCreate, onCreateInGroup, onCreateLabel, onToggleGroupSelected, onToggleSelected, onSaveLabel, onArchiveLabel, onMoveLabelToTeams, onConvertLabel, onDeleteLabel, onSaveGroup, onArchiveGroup, onConvertGroup, onDeleteGroup }: {
  entry: Extract<Entry, { kind: 'group' }>; context: RowContext; availableGroups: LabelGroup[]; selected: string[]; filtering: boolean; creatingGroupId?: string;
  onCancelCreate: () => void; onCreateInGroup: (groupId: string) => void; onCreateLabel: (input: NewLabelInput, groupId: string) => Promise<boolean>; onToggleGroupSelected: (group: LabelGroup, childIds: string[]) => void; onToggleSelected: (id: string) => void; onSaveLabel: (label: IssueLabel, input: Partial<IssueLabel>) => Promise<void>;
  onArchiveLabel: (label: IssueLabel) => Promise<void>; onMoveLabelToTeams: (label: IssueLabel) => Promise<void>; onConvertLabel: (label: IssueLabel) => Promise<void>; onDeleteLabel: (label: IssueLabel) => Promise<void>; onSaveGroup: (group: LabelGroup, input: Partial<LabelGroup>) => Promise<void>; onArchiveGroup: (group: LabelGroup) => Promise<void>; onConvertGroup: (group: LabelGroup) => Promise<void>; onDeleteGroup: (group: LabelGroup) => Promise<void>;
}) {
  const [collapsed, setCollapsed] = useState(false)
  const { group, labels } = entry
  const open = !collapsed || filtering
  return <>
    <GroupRow group={group} context={context} labels={labels} collapsed={!open} selected={selected.includes(group.id)} onToggleCollapsed={labels.length ? () => setCollapsed(value => !value) : undefined} onToggleSelected={() => onToggleGroupSelected(group, labels.map(label => label.id))} onCreateLabel={() => { setCollapsed(false); onCreateInGroup(group.id) }} onSave={input => onSaveGroup(group, input)} onArchive={() => onArchiveGroup(group)} onConvert={() => onConvertGroup(group)} onDelete={() => onDeleteGroup(group)}/>
    {creatingGroupId === group.id && <InlineLabelRow kind="label" grouped treeLast={!labels.length} continuous showDescription={context.columns.includes('description')} onCancel={onCancelCreate} onSave={input => onCreateLabel(input, group.id)}/>}
    {open && labels.map((label, index) => <LabelRow key={label.id} context={context} label={label} grouped treeLast={index === labels.length - 1} selected={selected.includes(label.id)} groups={availableGroups} onToggleSelected={() => onToggleSelected(label.id)} onSave={input => onSaveLabel(label, input)} onArchive={() => onArchiveLabel(label)} onMoveToTeams={() => onMoveLabelToTeams(label)} onConvert={() => onConvertLabel(label)} onDelete={() => onDeleteLabel(label)}/>)}
  </>
}

interface NewLabelInput { name: string; description: string; color: string }
function InlineLabelRow({ kind, grouped = false, treeLast = false, continuous = false, showDescription = true, onCancel, onSave }: { kind: 'label'|'group'; grouped?: boolean; treeLast?:boolean; continuous?: boolean; showDescription?: boolean; onCancel: () => void; onSave: (input: NewLabelInput) => Promise<boolean> }) {
  const { t } = useI18n()
  const formRef = useRef<HTMLFormElement>(null)
  const savingRef = useRef(false)
  const [name, setName] = useState(''); const [description, setDescription] = useState(''); const [color, setColor] = useState(kind === 'group' ? '#8b8d98' : '#5E6AD2')
  const submit = async () => {
    if (savingRef.current || !name.trim()) return
    savingRef.current = true
    try {
      const saved = await onSave({ name: name.trim(), description, color })
      if (saved && continuous) { setName(''); setDescription('') }
    } finally { savingRef.current = false }
  }
  const saveWhenFocusLeaves = () => requestAnimationFrame(() => {
    const active = document.activeElement
    if (active instanceof Element && (formRef.current?.contains(active) || active.closest('.domain-label-color-popover'))) return
    void submit()
  })
  useEffect(() => {
    const saveOnOutsidePointer = (event: PointerEvent) => {
      const target = event.target
      if (!(target instanceof Element) || formRef.current?.contains(target) || target.closest('.domain-label-color-popover')) return
      void submit()
    }
    document.addEventListener('pointerdown', saveOnOutsidePointer, true)
    return () => document.removeEventListener('pointerdown', saveOnOutsidePointer, true)
  })
  return <form ref={formRef} className={`domain-labels-row is-editing${kind === 'group' ? ' is-group' : ''}${grouped ? ' is-nested' : ''}`} onBlur={saveWhenFocusLeaves} onSubmit={event => { event.preventDefault(); void submit() }} onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); onCancel() } else if (event.key === 'Enter') { event.preventDefault(); void submit() } }}>
    <span className="domain-label-indent"/>
    <div className={`domain-label-name${kind === 'group' ? ' is-group-name' : ''}`}>{grouped&&<GroupTreeBranch last={treeLast}/>}{kind === 'group' && <span className="domain-label-group-chevron is-placeholder"/>}<LabelColorPicker color={color} kind={kind} label={t(kind === 'group' ? 'Choose group color' : 'Choose label color')} onChange={setColor}/><input autoFocus placeholder={t(kind === 'group' ? 'Group name' : 'Label name')} value={name} onChange={event => setName(event.target.value)}/></div>
    {showDescription && <input className="domain-label-description" aria-label={t(kind === 'group' ? 'Group description' : 'Label description')} disabled={!name.trim()} placeholder={t('Add label description…')} value={description} onChange={event => setDescription(event.target.value)}/>}
  </form>
}

/** Value cells after Name and Description, placed on the named grid columns. */
function ValueCells({ context, label, usage, rules, teamUsage, lastAppliedAt, group = false }: { context: RowContext; label: IssueLabel | LabelGroup; usage: number; rules?: LabelRuleUsage; teamUsage?: () => Map<string, number>; lastAppliedAt?: string; group?: boolean }) {
  const { locale, t } = useI18n()
  const { data, resourceType, columns } = context
  const created = label.createdAt
  const creatorId = 'creatorId' in label ? label.creatorId : undefined
  const creator = creatorId ? data.users.find(user => user.id === creatorId) : undefined
  const team = label.scope && data.teams.find(item => item.id === label.scope)
  const cells: ReactNode[] = []
  for (const column of columns) {
    if (column === 'title' || column === 'description') continue
    let content: ReactNode = null
    if (column === 'team') content = team ? <span data-i18n-ignore>{team.name}</span> : null
    else if (column === 'rules') content = rules && rules.total > 0 ? <RulesCount rules={rules} data={data}/> : null
    else if (column === 'usage') content = usage > 0 ? group ? <span className="label-settings-usage is-static">{usage}</span> : <UsageCount count={usage} label={label as IssueLabel} resourceType={resourceType} data={data} teamUsage={teamUsage}/> : null
    else if (column === 'lastAppliedAt') content = lastAppliedAt ? <DateCell value={lastAppliedAt} relative title={t('Last applied on {date}').replace('{date}', longDate(lastAppliedAt, locale))}/> : null
    else if (column === 'createdAt') content = created ? <DateCell value={created} title={creator ? t('Created by {name}').replace('{name}', creator.name) : t('Created {date}').replace('{date}', longDate(created, locale))}/> : null
    else if (column === 'archivedAt') content = label.archivedAt ? <DateCell value={label.archivedAt} relative title={t('Archived on {date}').replace('{date}', longDate(label.archivedAt, locale))}/> : null
    cells.push(<span key={column} className="label-settings-cell" style={{ gridColumn: column }}>{content}</span>)
  }
  return <>{cells}</>
}

function DateCell({ value, relative = false, title }: { value: string; relative?: boolean; title: string }) {
  const { locale } = useI18n()
  return <FlowTooltip label={title}><span>{relative ? relativeDate(value, locale) : shortDate(value, locale)}</span></FlowTooltip>
}

function UsageCount({ count, label, resourceType, data, teamUsage }: { count: number; label: IssueLabel; resourceType: LabelResourceType; data: BootstrapData; teamUsage?: () => Map<string, number> }) {
  const { locale, t } = useI18n()
  const href = labelPath(data.workspace.urlKey, resourceType, label.name)
  const teams = resourceType === 'issue' && teamUsage ? [...teamUsage().entries()].map(([id, value]) => ({ team: data.teams.find(item => item.id === id), value })).filter(item => item.team) : []
  const noun = NOUNS[resourceType]
  const card = teams.length
    ? <div className="label-settings-usage-card" data-i18n-ignore>
      <strong>{(teams.length === 1 ? t('Used by {count} team') : t('Used by {count} teams')).replace('{count}', String(teams.length))}</strong>
      {teams.slice(0, 3).map(({ team, value }) => <AppLink key={team!.id} href={href}><TeamIcon team={team} size={14}/><span>{team!.name}</span><em>{value}</em></AppLink>)}
      {teams.length > 3 && <strong>{t('+ {count} more').replace('{count}', String(teams.length - 3))}</strong>}
    </div>
    : <span>{locale === 'zh-CN' ? `${count} 个${noun.zh}` : `${count} ${count === 1 ? noun.one : noun.many}`}</span>
  return <FlowTooltip side="top" contentClassName="flow-tooltip-content--title label-settings-usage-tooltip" label={card}><AppLink className="label-settings-usage" href={href} aria-label={locale === 'zh-CN' ? `查看 ${count} 个带此标签的${noun.zh}` : `View ${count} labeled ${count === 1 ? noun.one : noun.many}`}>{count}</AppLink></FlowTooltip>
}

function RulesCount({ rules, data }: { rules: LabelRuleUsage; data: BootstrapData }) {
  const { t } = useI18n()
  const card = <div className="label-settings-usage-card" data-i18n-ignore>
    {rules.sla > 0 && <><strong>{t('Workspace')}</strong><span>{t('SLA rules')} · {rules.sla}</span></>}
    {rules.triage.size > 0 && <><strong>{t('Team triage rules')}</strong>{[...rules.triage.entries()].map(([teamId, value]) => { const team = data.teams.find(item => item.id === teamId); return <span key={teamId}>{team?.name ?? teamId} · {value}</span> })}</>}
  </div>
  return <FlowTooltip side="top" contentClassName="flow-tooltip-content--title label-settings-usage-tooltip" label={card}><span className="label-settings-usage">{rules.total}</span></FlowTooltip>
}

function GroupRow({ group, context, labels, collapsed = false, selected, onToggleCollapsed, onToggleSelected, onCreateLabel, onSave, onArchive, onConvert, onDelete }: { group: LabelGroup; context: RowContext; labels: IssueLabel[]; collapsed?: boolean; selected: boolean; onToggleCollapsed?: () => void; onToggleSelected: () => void; onCreateLabel?: () => void; onSave?: (input: Partial<LabelGroup>) => Promise<void>; onArchive: () => Promise<void>; onConvert?: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { locale, t } = useI18n()
  const [editing, setEditing] = useState<'name'|'description'|null>(null)
  const [confirm, setConfirm] = useState<'archive'|'delete'|null>(null)
  const children = context.data.labels.filter(label => label.groupId === group.id)
  const usage = labels.reduce((sum, label) => sum + context.usage(label), 0)
  const lastApplied = labels.map(label => label.lastAppliedAt ?? '').sort().at(-1) || undefined
  const actionLabel = (action: 'expand'|'collapse'|'color') => locale === 'zh-CN' ? `${action === 'expand' ? '展开' : action === 'collapse' ? '收起' : '选择颜色'} ${group.name}` : `${action === 'expand' ? 'Expand' : action === 'collapse' ? 'Collapse' : 'Choose'} ${group.name}${action === 'color' ? ' color' : ''}`
  return <><div className={`domain-labels-row is-group${group.archivedAt ? ' is-archived' : ''}${selected ? ' is-selected' : ''}`}>
    <SelectionCell kind="group" selected={selected} onToggle={onToggleSelected}/>
    <div className="domain-label-name is-group-name">{onToggleCollapsed ? <button aria-label={actionLabel(collapsed ? 'expand' : 'collapse')} className="domain-label-group-chevron" onClick={onToggleCollapsed} type="button">{collapsed ? <ChevronRight/> : <ChevronDown/>}</button> : <span className="domain-label-group-chevron is-placeholder"/>}<LabelColorPicker color={group.color} disabled={Boolean(group.archivedAt)} kind="group" label={actionLabel('color')} onChange={color => onSave?.({ color })}/><LabelEditableText value={group.name} field={t('Name')} editing={editing === 'name'} archived={Boolean(group.archivedAt)} onEdit={() => setEditing('name')} onCancel={() => setEditing(null)} onSave={async value => { await onSave?.({ name: value }); setEditing(null) }}/></div>
    {context.columns.includes('description') && <EditableDescription value={group.description ?? ''} editing={editing === 'description'} archived={Boolean(group.archivedAt)} onEdit={() => setEditing('description')} onCancel={() => setEditing(null)} onSave={async value => { await onSave?.({ description: value }); setEditing(null) }}/>}
    <ValueCells context={context} label={group} usage={usage} lastAppliedAt={lastApplied} group/>
    <GroupRowMenu group={group} canConvert={!children.length && Boolean(onConvert)} onEdit={() => setEditing('name')} onCreateLabel={onCreateLabel} onArchive={async () => { if (group.archivedAt || usage === 0) await onArchive(); else setConfirm('archive') }} onConvert={() => void onConvert?.()} onDelete={async () => setConfirm('delete')}/>
  </div><LabelConfirmDialog kind="group" action={confirm} name={group.name} archived={Boolean(group.archivedAt)} resourceType={context.resourceType} usage={usage} onArchive={onArchive} onClose={() => setConfirm(null)} onConfirm={async () => { if (confirm === 'archive') await onArchive(); else await onDelete(); setConfirm(null) }}/></>
}

function LabelRow({ context, label, grouped, treeLast = false, selected, groups, onToggleSelected, onSave, onArchive, onMoveToTeams, onConvert, onDelete }: { context: RowContext; label: IssueLabel; grouped: boolean; treeLast?: boolean; selected: boolean; groups: LabelGroup[]; onToggleSelected: () => void; onSave: (input: Partial<IssueLabel>) => Promise<void>; onArchive: () => Promise<void>; onMoveToTeams?: () => Promise<void>; onConvert?: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { locale, t } = useI18n()
  const { data, resourceType } = context
  const [editing, setEditing] = useState<'name'|'description'|null>(null)
  const [confirm, setConfirm] = useState<'archive'|'delete'|null>(null)
  const count = context.usage(label)
  const rules = context.rules(label)
  const colorLabel = locale === 'zh-CN' ? `选择 ${label.name} 的颜色` : `Choose ${label.name} color`
  const archive = async () => {
    if (label.archivedAt) { await onArchive(); return }
    if (count > 0 || rules.total > 0) { setConfirm('archive'); return }
    await onArchive()
    toast.success(t('Label archived'), { description: t('"{name}" has been archived').replace('{name}', label.name) })
  }
  return <><div className={`domain-labels-row${grouped ? ' is-nested' : ''}${label.archivedAt ? ' is-archived' : ''}${selected ? ' is-selected' : ''}`}>
    <SelectionCell kind="label" selected={selected} onToggle={onToggleSelected}/>
    <div className="domain-label-name">{grouped && <GroupTreeBranch last={treeLast}/>}<LabelColorPicker color={label.color} disabled={Boolean(label.archivedAt)} kind="label" label={colorLabel} onChange={color => onSave({ color })}/><LabelEditableText value={label.name} field={t('Name')} editing={editing === 'name'} archived={Boolean(label.archivedAt)} onEdit={() => setEditing('name')} onCancel={() => setEditing(null)} onSave={async value => { await onSave({ name: value }); setEditing(null) }}/></div>
    {context.columns.includes('description') && <EditableDescription value={label.description ?? ''} editing={editing === 'description'} archived={Boolean(label.archivedAt)} onEdit={() => setEditing('description')} onCancel={() => setEditing(null)} onSave={async value => { await onSave({ description: value }); setEditing(null) }}/>}
    <ValueCells context={context} label={label} usage={count} rules={rules} teamUsage={() => labelTeamUsage(label, data)} lastAppliedAt={label.lastAppliedAt}/>
    <LabelRowMenu
      label={label}
      groups={groups}
      workspaceSlug={data.workspace.urlKey}
      resourceType={resourceType}
      usage={count}
      onEdit={() => setEditing('name')}
      onGroup={groupId => onSave({ groupId })}
      onArchive={() => void archive()}
      onMoveToTeams={resourceType === 'issue' && isWorkspaceLabel(label) && count > 0 ? onMoveToTeams : undefined}
      onConvert={!label.groupId && !label.archivedAt && count === 0 && rules.total === 0 ? onConvert : undefined}
      onDelete={async () => setConfirm('delete')}
    />
  </div><LabelConfirmDialog kind="label" action={confirm} name={label.name} archived={Boolean(label.archivedAt)} resourceType={resourceType} usage={count} onArchive={onArchive} onClose={() => setConfirm(null)} onConfirm={async () => { if (confirm === 'archive') await onArchive(); else await onDelete(); setConfirm(null) }}/></>
}

function GroupTreeBranch({last}:{last:boolean}){return <span aria-hidden="true" className={`domain-label-tree-branch${last?' is-last':''}`}/>}

function LabelColorPicker({ color, disabled = false, kind, label, onChange }: { color: string; disabled?: boolean; kind: 'group'|'label'; label: string; onChange: (color: string) => void | Promise<void> }) {
  const { t } = useI18n()
  const customColorRef = useRef<HTMLInputElement>(null)
  const normalized = color.toLowerCase()
  const preset = FLOW_COLOR_PALETTE.some(option => option.value === normalized)
  return <Popover.Root><Popover.Trigger asChild><button aria-label={label} className="domain-label-color" data-kind={kind} disabled={disabled} type="button">{kind === 'group' ? <PaletteMark color={color}/> : <i style={{ background: color }}/>}</button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" data-i18n-ignore align="center" className="domain-label-color-popover" collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()} side="bottom" sideOffset={3}><div className="domain-label-color-presets">{FLOW_COLOR_PALETTE.map(option => <button aria-label={t(option.name)} data-selected={normalized === option.value} key={option.value} onClick={() => void onChange(option.value)} style={{ color: option.value }} type="button"><span style={{ background: option.value }}>{normalized === option.value && <ColorCheck/>}</span></button>)}</div><button aria-label={t('Set custom color')} className="domain-label-custom-color" data-selected={!preset} onClick={() => customColorRef.current?.click()} type="button"><span/>{!preset && <i/>}</button><input aria-hidden="true" className="domain-label-native-color" onChange={event => void onChange(event.target.value.toLowerCase())} ref={customColorRef} tabIndex={-1} type="color" value={/^#[0-9a-f]{6}$/i.test(color) ? color : '#95a2b3'}/></Popover.Content></Popover.Portal></Popover.Root>
}

function PaletteMark({ color }: { color: string }) { return <svg aria-hidden="true" fill={color} viewBox="0 0 16 16"><path clipRule="evenodd" d="M7.95 6A1.75 1.75 0 1 0 7.95 2.5 1.75 1.75 0 0 0 7.95 6ZM4.45 9.5A1.75 1.75 0 1 0 4.45 6a1.75 1.75 0 0 0 0 3.5ZM7.95 13a1.75 1.75 0 1 0 0-3.5 1.75 1.75 0 0 0 0 3.5Zm5.25-5.25a1.75 1.75 0 1 1-3.5 0 1.75 1.75 0 0 1 3.5 0Z" fillRule="evenodd"/></svg> }
function ColorCheck() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 10 8"><path d="M3.47 5.708 1.884 4.123a.576.576 0 0 0-.815.814l1.996 1.994a.576.576 0 0 0 .814 0L8.931 1.883a.576.576 0 0 0-.815-.814L3.47 5.708Z"/></svg> }

function SelectionCell({ kind, selected, onToggle }: { kind: 'label'|'group'; selected: boolean; onToggle: () => void }) { const { t } = useI18n(); return <label className="domain-label-indent domain-label-select"><input type="checkbox" aria-label={t(kind === 'group' ? 'Select group' : 'Select label')} checked={selected} onChange={onToggle}/></label> }

function LabelEditableText({ value, field, editing, archived, onEdit, onCancel, onSave }: { value: string; field: string; editing: boolean; archived: boolean; onEdit: () => void; onCancel: () => void; onSave: (value: string) => Promise<void> }) {
  const [draft, setDraft] = useState(value)
  const inputRef=useRef<HTMLInputElement>(null)
  useEffect(()=>setDraft(value),[value])
  useEffect(()=>{if(editing){inputRef.current?.focus();inputRef.current?.select()}},[editing])
  return <input ref={inputRef} className={`domain-label-inline-input domain-label-name-input${editing?' is-editing':''}`} aria-label={editing?`Edit ${field}`:value} readOnly={!editing||archived} value={editing?draft:value} onFocus={()=>{if(!archived&&!editing)onEdit()}} onClick={()=>{if(!archived&&!editing)onEdit()}} onChange={event=>setDraft(event.target.value)} onBlur={()=>{if(!editing)return;const next=draft.trim();if(next&&next!==value)void onSave(next);else onCancel()}} onKeyDown={event=>{if(!editing)return;if(event.key==='Enter')event.currentTarget.blur();if(event.key==='Escape'){event.preventDefault();setDraft(value);onCancel();requestAnimationFrame(()=>inputRef.current?.blur())}}}/>
}

function EditableDescription({ value, editing, archived, onEdit, onCancel, onSave }: { value: string; editing: boolean; archived: boolean; onEdit: () => void; onCancel: () => void; onSave: (value: string) => Promise<void> }) {
  const { t } = useI18n()
  const [draft, setDraft] = useState(value)
  const editorRef = useRef<HTMLTextAreaElement>(null)
  useEffect(() => setDraft(value), [value])
  useEffect(() => { if (editing) { editorRef.current?.focus(); editorRef.current?.select() } }, [editing])
  if (editing) return <textarea ref={editorRef} rows={1} className="domain-label-description domain-label-description-editor" aria-label={t('Edit description')} placeholder={t('Add label description…')} value={draft} onChange={event => setDraft(event.target.value.replace(/[\r\n]+/g, ' '))} onBlur={() => { if (draft !== value) void onSave(draft); else onCancel() }} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); event.currentTarget.blur() } if (event.key === 'Escape') { event.preventDefault(); setDraft(value); onCancel(); requestAnimationFrame(() => editorRef.current?.blur()) } }}/>
  return <button type="button" tabIndex={-1} className={`domain-label-description domain-label-description-button${value ? '' : ' is-empty'}`} disabled={archived} onClick={onEdit}>{value || t('Add label description…')}</button>
}

function MenuShell({ label, kind, children }: { label: string; kind: 'group'|'label'|'bulk'; children: ReactNode }) {
  const { locale } = useI18n()
  return <DropdownMenu><DropdownMenuTrigger asChild><button className="domain-label-menu-button" aria-label={locale === 'zh-CN' ? `打开 ${label} 菜单` : `Open ${label} menu`}><MoreHorizontal size={15}/></button></DropdownMenuTrigger><DropdownMenuContent data-i18n-ignore className={`domain-label-row-menu is-${kind}-menu`} align="end" sideOffset={4}>{children}</DropdownMenuContent></DropdownMenu>
}

function GroupRowMenu({ group, canConvert, onEdit, onCreateLabel, onArchive, onConvert, onDelete }: { group: LabelGroup; canConvert: boolean; onEdit: () => void; onCreateLabel?: () => void; onArchive: () => Promise<void>; onConvert: () => void; onDelete: () => Promise<void> }) {
  const { t } = useI18n()
  return <MenuShell kind="group" label={group.name}>{group.archivedAt ? <><DropdownMenuItem onSelect={() => void onArchive()}><MenuIcon name="restore"/><span>{t('Restore')}</span></DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem></> : <>
    <DropdownMenuItem onSelect={onEdit}><MenuIcon name="edit"/><span>{t('Edit label name')}</span><MenuShortcut>E</MenuShortcut></DropdownMenuItem>
    <DropdownMenuItem onSelect={onCreateLabel}><MenuIcon name="add"/><span>{t('Add label to group')}</span></DropdownMenuItem>
    {canConvert && <DropdownMenuItem onSelect={onConvert}><MenuIcon name="label"/><span>{t('Convert to label')}</span></DropdownMenuItem>}
    <DropdownMenuSeparator/>
    <DropdownMenuItem onSelect={() => void onArchive()}><MenuIcon name="archive"/><span>{t('Archive…')}</span></DropdownMenuItem>
    <DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem>
  </>}</MenuShell>
}

function LabelRowMenu({ label, groups, workspaceSlug, resourceType, usage, onEdit, onGroup, onArchive, onMoveToTeams, onConvert, onDelete }: { label: IssueLabel; groups: LabelGroup[]; workspaceSlug: string; resourceType: LabelResourceType; usage: number; onEdit: () => void; onGroup: (groupId: string) => Promise<void>; onArchive: () => void; onMoveToTeams?: () => Promise<void>; onConvert?: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { locale, t } = useI18n()
  const [moveOpen, setMoveOpen] = useState(false)
  const [moving, setMoving] = useState(false)
  const groupOptions = labelGroupOptions(label, groups)
  const canMoveToGroup = Boolean(label.groupId) || groupOptions.length > 0
  const move = async () => { if (!onMoveToTeams) return; setMoving(true); try { await onMoveToTeams(); setMoveOpen(false) } finally { setMoving(false) } }
  const viewLabel = resourceType === 'project' ? 'View labeled projects' : resourceType === 'initiative' ? 'View labeled initiatives' : 'View labeled issues'
  return <><MenuShell kind="label" label={label.name}>{label.archivedAt ? <><DropdownMenuItem onSelect={onArchive}><MenuIcon name="restore"/><span>{t('Restore')}</span></DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem></> : <>
    <DropdownMenuItem onSelect={onEdit}><MenuIcon name="edit"/><span>{t('Edit label name')}</span><MenuShortcut>E</MenuShortcut></DropdownMenuItem>
    {canMoveToGroup && <DropdownMenuSub><DropdownMenuSubTrigger><MenuIcon name="move"/><span>{t(label.groupId ? 'Change group' : 'Move to group…')}</span></DropdownMenuSubTrigger><DropdownMenuSubContent data-i18n-ignore className="domain-label-group-submenu" sideOffset={4}>{label.groupId && <DropdownMenuItem onSelect={() => void onGroup('')}><span>{t('Remove from group')}</span></DropdownMenuItem>}{label.groupId && groupOptions.length > 0 && <DropdownMenuSeparator/>}{groupOptions.map(group => <DropdownMenuItem key={group.id} onSelect={() => void onGroup(group.id)}><PaletteMark color={group.color}/><span>{group.name}</span></DropdownMenuItem>)}</DropdownMenuSubContent></DropdownMenuSub>}
    {onConvert && <DropdownMenuItem onSelect={() => void onConvert()}><MenuIcon name="group"/><span>{t('Convert to label group')}</span></DropdownMenuItem>}
    {onMoveToTeams && <DropdownMenuItem onSelect={() => setMoveOpen(true)}><MenuIcon name="teams"/><span>{t('Move to teams…')}</span></DropdownMenuItem>}
    {usage > 0 && <><DropdownMenuSeparator/><DropdownMenuItem asChild><AppLink href={labelPath(workspaceSlug, resourceType, label.name)}><MenuIcon name="view"/><span>{t(viewLabel)}</span></AppLink></DropdownMenuItem></>}
    <DropdownMenuSeparator/>
    <DropdownMenuItem onSelect={onArchive}><MenuIcon name="archive"/><span>{t('Archive…')}</span></DropdownMenuItem>
    <DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem>
  </>}</MenuShell><Dialog open={moveOpen} onOpenChange={setMoveOpen}><DialogContent data-i18n-ignore className="domain-label-move-dialog"><DialogTitle>{locale === 'zh-CN' ? <>将“<strong>{label.name}</strong>”移动到团队？</> : <>Move <strong>"{label.name}"</strong> to teams?</>}</DialogTitle><p>{t('This label will be moved to each team that uses it and will no longer be available for the whole workspace.')}</p><p>{t('This action cannot be undone.')}</p><footer><button disabled={moving} onClick={() => setMoveOpen(false)}>{t('Cancel')}</button><button className="primary" disabled={moving} onClick={() => void move()}>{t(moving ? 'Moving…' : 'Move label')}</button></footer></DialogContent></Dialog></>
}

function labelGroupOptions(label: IssueLabel, groups: LabelGroup[]) {
  const resource = labelResourceType(label)
  const scope = isWorkspaceLabel(label) ? 'workspace' : label.scope
  return groups.filter(group => {
    if (group.id === label.groupId || group.archivedAt || group.resourceType !== resource) return false
    const groupScope = !group.scope || group.scope.toLowerCase() === 'workspace' ? 'workspace' : group.scope
    return groupScope === scope
  })
}

function LabelConfirmDialog({kind,action,name,archived,resourceType,usage,onArchive,onClose,onConfirm}:{kind:'label'|'group';action:'archive'|'delete'|null;name:string;archived:boolean;resourceType:LabelResourceType;usage:number;onArchive:()=>void|Promise<void>;onClose:()=>void;onConfirm:()=>Promise<void>}){
  const{locale,t}=useI18n(),[busy,setBusy]=useState(false)
  const verb=action==='archive'?'Archive':'Delete'
  const noun=NOUNS[resourceType]
  const plural=usage===1?noun.one:noun.many
  const title=locale==='zh-CN'
    ? action==='delete'&&kind==='group'?<>删除分组“<strong>{name}</strong>”？</>:<>{t(verb)}“<strong>{name}</strong>”？</>
    : action==='delete'&&kind==='group'?<>Delete the group <strong>"{name}"</strong>?</>:<>{verb} <strong>"{name}"</strong>?</>
  const archiveCopy=locale==='zh-CN'
    ? kind==='group'?`此分组内的标签将无法再应用到${noun.zh}。已经应用这些标签的${noun.zh}不会改变。`:`此标签将无法再应用到${noun.zh}。已经应用此标签的${noun.zh}不会改变。`
    : kind==='group'?`The labels in this group will no longer be available to apply to ${noun.many}. ${noun.many[0].toUpperCase()+noun.many.slice(1)} with these labels already applied will remain unchanged.`:`This label will no longer be available to apply to ${noun.many}. ${noun.many[0].toUpperCase()+noun.many.slice(1)} with this label already applied will remain unchanged.`
  const deleteCopy=locale==='zh-CN'
    ? `${kind==='group'?'此分组内的所有标签':'此标签'}将从 ${usage} 个${noun.zh}中移除，并且可能影响私有团队。删除后无法撤销。`
    : `${kind==='group'?'All labels in this group':'This label'} will be removed from ${usage} ${plural} and private teams may be affected. Deletion cannot be undone.`
  const execute=(actionFn:()=>void|Promise<void>)=>{if(busy)return;setBusy(true);void Promise.resolve().then(actionFn).then(onClose).catch(error=>toast.error(message(error))).finally(()=>setBusy(false))}
  return <Dialog open={Boolean(action)} onOpenChange={open=>!open&&onClose()}><DialogContent data-i18n-ignore className="domain-label-confirm"><DialogTitle>{title}</DialogTitle><p>{action==='archive'?archiveCopy:deleteCopy}</p>{action==='delete'&&!archived&&<p>{locale==='zh-CN'?'如需阻止以后继续使用，请改为归档。':'To prevent future application of this label, archive the label instead.'}</p>}<footer>{action==='delete'&&!archived&&<button disabled={busy} onClick={()=>execute(onArchive)}>{t('Archive')}</button>}<span/><button disabled={busy} onClick={onClose}>{t('Cancel')}</button><button className="primary" disabled={busy} onClick={()=>execute(onConfirm)}>{t(verb)}</button></footer></DialogContent></Dialog>
}

/** Linear's "Merge N labels?" confirmation; the broadest, most used label survives. */
function MergeLabelsDialog({ labels, resourceType, usage, onClose, onMerge }: { labels: IssueLabel[] | null; resourceType: LabelResourceType; usage: (label: IssueLabel) => number; onClose: () => void; onMerge: (labels: IssueLabel[]) => Promise<void> }) {
  const { locale, t } = useI18n()
  const [busy, setBusy] = useState(false)
  const items = labels ?? []
  const target = items.length >= 2 ? mergeTarget(items, usage) : undefined
  const total = items.reduce((sum, label) => sum + usage(label), 0)
  const noun = NOUNS[resourceType]
  const applied = total > 0 ? (locale === 'zh-CN' ? `${total} 个${noun.zh}` : `${total} ${total === 1 ? noun.one : noun.many}`) : ''
  const confirm = async () => { if (busy) return; setBusy(true); try { await onMerge(items) } finally { setBusy(false) } }
  return <Dialog open={Boolean(target)} onOpenChange={open => !open && onClose()}><DialogContent data-i18n-ignore className="domain-label-confirm label-settings-merge-dialog">
    <DialogTitle>{t('Merge {count} labels?').replace('{count}', String(items.length))}</DialogTitle>
    {target && <p>{t('These labels will be merged into')} <i className="label-settings-merge-dot" style={{ background: target.color }}/><strong data-i18n-ignore>{target.name}</strong>{applied && <>{locale === 'zh-CN' ? '。' : '. '}{t('The merged label will be applied to')} <span>{applied}</span></>}{locale === 'zh-CN' ? '。' : '.'}</p>}
    <p>{t('This action cannot be undone.')}</p>
    <footer><span/><button disabled={busy} onClick={onClose}>{t('Cancel')}</button><button className="primary" disabled={busy} onClick={() => void confirm()}>{t('Merge')}</button></footer>
  </DialogContent></Dialog>
}

type LabelMenuIcon = 'edit'|'add'|'move'|'teams'|'view'|'archive'|'delete'|'restore'|'group'|'label'
function MenuIcon({ name }: { name: LabelMenuIcon }) {
  if (name === 'edit') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M10.1805 3.34195 4.14166 9.416c1.18782.35421 2.15072 1.2469 2.59842 2.4024L12.6877 5.8425c-1.0235-.62127-1.8834-1.47898-2.5072-2.50055Z"/><path d="M13.7391 4.71631c.4184-.68683.3336-1.59893-.2545-2.19441-.5938-.60118-1.5062-.68298-2.1866-.24541.5567 1.03483 1.4057 1.8835 2.4411 2.43982Z"/><path d="M3.03104 10.7502c1.27192.0156 2.33541.9921 2.46679 2.2612-.66515.4146-2.09586.7808-2.96669.9772-.33104.0746-.61088-.2284-.51039-.5513.23251-.7471.62517-1.9237 1.01029-2.6871Z"/></svg>
  if (name === 'add') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M8.75 4a.75.75 0 0 0-1.5 0v3.25H4a.75.75 0 0 0 0 1.5h3.25V12a.75.75 0 0 0 1.5 0V8.75H12a.75.75 0 0 0 0-1.5H8.75V4Z"/></svg>
  if (name === 'move') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M10.3262 4.51988a.75.75 0 0 0-1.15235.96028L10.6487 7.25H3.75a.75.75 0 0 0 0 1.5h6.8988l-1.47495 1.7699a.75.75 0 0 0 1.15235.9603l2.5-3.00004a.75.75 0 0 0 0-.96028l-2.5-3Z"/></svg>
  if (name === 'teams') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="m11.6 9 .072.005.016.001a.6.6 0 0 1 .313.148l2.887 2.592a.4.4 0 0 1 0 .595l-2.887 2.593A.6.6 0 0 1 11 14.487v-1.114C6.333 12.77 4 12.313 4 12.002c0-.311 2.333-.768 7-1.371V9.6a.6.6 0 0 1 .6-.6ZM6.782 7.645l.914.562a2.702 2.702 0 0 1 1.2 1.668c-2.96.437-4.76.785-5.084 1.044-.476.207-.765.419-.807.944L3 12h-.5A1.5 1.5 0 0 1 1 10.5c0-.87.42-1.684 1.119-2.19l.874-.551a3.768 3.768 0 0 1 3.789-.114Zm6 0 .914.562A2.702 2.702 0 0 1 14.969 9.5c0 .285-.08.552-.218.78l-2.082-1.87a1.6 1.6 0 0 0-2.662 1.044L10 9.719l-.031.004A3 3 0 0 0 8.76 7.724l-.605-.439.838-.526a3.768 3.768 0 0 1 3.789-.114ZM4.969 2a2 2 0 1 1 0 4 2 2 0 0 1 0-4Zm6-1a2 2 0 1 1 0 4 2 2 0 0 1 0-4Z"/></svg>
  if (name === 'view') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" d="M13.25 5.25A1.75 1.75 0 0 1 15 7v4.75A3.25 3.25 0 0 1 11.75 15h-5a1.75 1.75 0 0 1-1.75-1.75.75.75 0 0 1 1.5 0c0 .138.112.25.25.25h5a1.75 1.75 0 0 0 1.75-1.75V7a.25.25 0 0 0-.25-.25.75.75 0 0 1 0-1.5Z" clipRule="evenodd"/><path fillRule="evenodd" d="M8.154 1.004A3 3 0 0 1 11 4v4a3 3 0 0 1-2.846 2.996L8 11H4a3 3 0 0 1-2.996-2.846L1 8V4a3 3 0 0 1 2.846-2.996L4 1h4l.154.004ZM4 2.5A1.5 1.5 0 0 0 2.5 4v4A1.5 1.5 0 0 0 4 9.5h4A1.5 1.5 0 0 0 9.5 8V4A1.5 1.5 0 0 0 8 2.5H4Z" clipRule="evenodd"/></svg>
  if (name === 'archive') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" d="M9.25 8a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1 0-1.5h2.5Z" clipRule="evenodd"/><path fillRule="evenodd" d="M12.75 2A2.25 2.25 0 0 1 15 4.25v1.5c0 .605-.43 1.109-1 1.225v4.775A2.25 2.25 0 0 1 11.75 14H4.2a2.25 2.25 0 0 1-2.25-2.25V6.962A1.25 1.25 0 0 1 1 5.75v-1.5A2.25 2.25 0 0 1 3.25 2h9.5ZM3.45 11.75c0 .414.336.75.75.75h7.55a.75.75 0 0 0 .75-.75V7H3.45v4.75ZM3.25 3.5a.75.75 0 0 0-.75.75V5.5h11V4.25a.75.75 0 0 0-.75-.75h-9.5Z" clipRule="evenodd"/></svg>
  if (name === 'delete') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" d="m2 3 1.652 9.911A2.5 2.5 0 0 0 6.118 15h3.764a2.5 2.5 0 0 0 2.466-2.089L14 3H2Zm1.77 1.5 1.361 8.164a1 1 0 0 0 .987.836h3.764a1 1 0 0 0 .987-.836l1.36-8.164H3.771Z" clipRule="evenodd"/><path d="M5.5 2.5A1.5 1.5 0 0 1 7 1h2a1.5 1.5 0 0 1 1.5 1.5v1h-5v-1Z"/><path d="M1 3.75A.75.75 0 0 1 1.75 3h12.5a.75.75 0 0 1 0 1.5H1.75A.75.75 0 0 1 1 3.75Z"/></svg>
  if (name === 'group') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" clipRule="evenodd" d="M7.95 6A1.75 1.75 0 1 0 7.95 2.5 1.75 1.75 0 0 0 7.95 6ZM4.45 9.5A1.75 1.75 0 1 0 4.45 6a1.75 1.75 0 0 0 0 3.5ZM7.95 13a1.75 1.75 0 1 0 0-3.5 1.75 1.75 0 0 0 0 3.5Zm5.25-5.25a1.75 1.75 0 1 1-3.5 0 1.75 1.75 0 0 1 3.5 0Z"/></svg>
  if (name === 'label') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" clipRule="evenodd" d="M2.5 4.75A2.25 2.25 0 0 1 4.75 2.5h4.172a2.25 2.25 0 0 1 1.59.659l3.33 3.33a2.25 2.25 0 0 1 0 3.182l-3.33 3.33a2.25 2.25 0 0 1-1.59.659H4.75A2.25 2.25 0 0 1 2.5 11.25v-6.5ZM4.75 4a.75.75 0 0 0-.75.75v6.5c0 .414.336.75.75.75h4.172a.75.75 0 0 0 .53-.22l3.33-3.33a.75.75 0 0 0 0-1.06l-3.33-3.33a.75.75 0 0 0-.53-.22H4.75Z"/></svg>
  return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M8 2.25a5.75 5.75 0 1 1-5.51 7.4.75.75 0 1 1 1.436-.43A4.25 4.25 0 1 0 5.2 4.75H7a.75.75 0 0 1 0 1.5H3.5a.75.75 0 0 1-.75-.75V2a.75.75 0 0 1 1.5 0v1.54A5.72 5.72 0 0 1 8 2.25Z"/></svg>
}

function MenuShortcut({ children }: { children: ReactNode }) { return <kbd className="domain-label-menu-shortcut">{children}</kbd> }

function ScopeSectionHeader({ label, count, collapsed, onToggle }: { label: string; count: number; collapsed: boolean; onToggle: () => void }) {
  const { t } = useI18n()
  return <div className="domain-label-scope-header"><button aria-label={t(collapsed ? 'Expand group' : 'Collapse group')} onClick={onToggle}>{collapsed ? <ChevronRight size={13}/> : <ChevronDown size={13}/>}</button><strong data-i18n-ignore>{label}</strong><span>{count}</span></div>
}

function BulkLabelBar({ count, archived, canMerge, onFavorite, onArchive, onMerge, onDelete, onClear }: { count: number; archived: boolean; canMerge: boolean; onFavorite: () => void; onArchive: () => void; onMerge: () => void; onDelete: () => void; onClear: () => void }) {
  const { t } = useI18n()
  return <div className="domain-label-bulk" data-i18n-ignore><strong>{count}</strong><span>{t('selected')}</span><DropdownMenu><DropdownMenuTrigger asChild><button>{t('Actions')}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent data-i18n-ignore className="domain-label-row-menu is-bulk-menu" align="center"><DropdownMenuItem onSelect={onFavorite}><Star size={14}/>{t('Favorite labels')}</DropdownMenuItem>{canMerge && <DropdownMenuItem onSelect={onMerge}><Merge size={14}/>{t('Merge labels…')}</DropdownMenuItem>}<DropdownMenuSeparator/><DropdownMenuItem onSelect={onArchive}>{archived ? <RotateCcw size={14}/> : <Archive size={14}/>} {t(archived ? 'Restore labels…' : 'Archive labels…')}</DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={onDelete}><Trash2 size={14}/>{t('Delete labels…')}</DropdownMenuItem></DropdownMenuContent></DropdownMenu><button aria-label={t('Clear selected')} onClick={onClear}><X size={14}/></button></div>
}

function shortDate(value: string, locale: 'en-US'|'zh-CN') { return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(new Date(value)) }
function longDate(value: string, locale: 'en-US'|'zh-CN') { return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(value)) }
function relativeDate(value: string, locale: 'en-US'|'zh-CN') { const days = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 86400000)); if (locale === 'zh-CN') { if (days === 0) return '今天'; if (days < 30) return `${days} 天前` } else { if (days === 0) return 'Today'; if (days === 1) return '1 day ago'; if (days < 30) return `${days} days ago` } return shortDate(value, locale) }

function message(error:unknown){return error instanceof Error?error.message:'Could not save setting'}
