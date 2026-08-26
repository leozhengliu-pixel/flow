import { Archive, Check, ChevronDown, ChevronRight, MoreHorizontal, Plus, RotateCcw, Search, Star, Trash2, X } from 'lucide-react'
import * as Popover from '@radix-ui/react-popover'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import {
  addFavorite, createLabelGroup, createWorkspaceLabel, deleteLabelGroup, deleteTeamLabel,
  deleteWorkspaceLabel, moveWorkspaceLabelToTeams, updateLabelGroup, updateTeamLabel, updateWorkspaceLabel,
} from '@/lib/api'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuSub, DropdownMenuSubContent,
  DropdownMenuSubTrigger, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import type { BootstrapData, IssueLabel, LabelGroup } from '@/types/flow'
import { groupsForResource, isWorkspaceLabel, labelResourceType } from '@/lib/labels'
import { useI18n } from '@/i18n/i18n'
import { FLOW_COLOR_PALETTE } from '@/components/ui/color-palette'

export { ProjectStatusesSettings } from './issues-projects-settings'

type ScopeFilter = 'workspace' | 'all' | 'archived'
type LabelSort = 'workflow' | 'name' | 'description' | 'usage' | 'lastAppliedAt' | 'createdAt'

export function DomainLabelsSettings({ data, resourceType, onReload }: { data: BootstrapData; resourceType: 'issue'|'project'; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [creating, setCreating] = useState<{ kind: 'label'|'group'; groupId?: string }|null>(null)
  const [scope, setScope] = useState<ScopeFilter>('workspace')
  const [sort, setSort] = useState<{ key: LabelSort; descending: boolean }>({ key: 'workflow', descending: false })
  const [selected, setSelected] = useState<string[]>([])
  const [collapsedScopes, setCollapsedScopes] = useState<string[]>([])
  const allGroups = useMemo(() => groupsForResource(data.labelGroups, resourceType), [data.labelGroups, resourceType])
  const groups = useMemo(() => allGroups.filter(group => scope === 'archived' ? Boolean(group.archivedAt) : !group.archivedAt), [allGroups, scope])
  const labels = useMemo(() => data.labels.filter(item => {
    if (labelResourceType(item) !== resourceType) return false
    if (scope === 'archived') return Boolean(item.archivedAt)
    if (item.archivedAt) return false
    if (scope === 'workspace' && !isWorkspaceLabel(item)) return false
    const value = query.trim().toLowerCase()
    return !value || item.name.toLowerCase().includes(value) || (item.description ?? '').toLowerCase().includes(value)
  }), [data.labels, query, resourceType, scope])
  const sortedLabels = useMemo(() => sortLabels(labels, sort, data, resourceType), [labels, sort, data, resourceType])
  const sections = useMemo(
    () => labelSections(sortedLabels, groups, Boolean(query.trim()) || scope === 'archived'),
    [sortedLabels, groups, query, scope],
  )
  const scopes = useMemo(() => scopeSections(sortedLabels, data), [sortedLabels, data])
  const run = async <T,>(action: () => Promise<T>): Promise<T | undefined> => {
    try { const result = await action(); await onReload(); return result } catch (error) { toast.error(message(error)); return undefined }
  }
  const saveLabel = async (label: IssueLabel, input: Partial<IssueLabel>) => { await run(() => isWorkspaceLabel(label) ? updateWorkspaceLabel(label.id, input) : updateTeamLabel(label.scope!, label.id, input)) }
  const deleteLabel = async (label: IssueLabel) => { await run(() => isWorkspaceLabel(label) ? deleteWorkspaceLabel(label.id) : deleteTeamLabel(label.scope!, label.id)) }
  const archiveLabel = (label: IssueLabel) => saveLabel(label, { archivedAt: label.archivedAt ? '' : new Date().toISOString() })
  const moveLabelToTeams = async (label: IssueLabel) => { await run(() => moveWorkspaceLabelToTeams(label.id)) }
  const toggleSelected = (id: string) => setSelected(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id])
  const toggleGroupSelected = (group: LabelGroup, childIds: string[]) => setSelected(current => {
    const ids = [group.id, ...childIds]
    return current.includes(group.id) ? current.filter(id => !ids.includes(id)) : [...new Set([...current, ...ids])]
  })
  const renderSections = (items: LabelSection[]) => items.map(section => <LabelSectionRows
    key={section.id}
    section={section}
    data={data}
    resourceType={resourceType}
    availableGroups={groups}
    selected={selected}
    creatingGroupId={creating?.kind === 'label' ? creating.groupId : undefined}
    onCancelCreate={() => setCreating(null)}
    onCreateInGroup={groupId => setCreating({ kind: 'label', groupId })}
    onCreateLabel={async (input, groupId) => Boolean(await run(() => createWorkspaceLabel({ ...input, resourceType, groupId })))}
    onToggleGroupSelected={toggleGroupSelected}
    onToggleSelected={toggleSelected}
    onSaveLabel={saveLabel}
    onArchiveLabel={archiveLabel}
    onMoveLabelToTeams={moveLabelToTeams}
    onDeleteLabel={deleteLabel}
    onSaveGroup={async (group, input) => { await run(() => updateLabelGroup(group.id, input)) }}
    onArchiveGroup={async group => { await run(() => updateLabelGroup(group.id, { archivedAt: group.archivedAt ? '' : new Date().toISOString() })) }}
    onDeleteGroup={async group => { await run(() => deleteLabelGroup(group.id)) }}
  />)
  const bulk = async (action: 'favorite'|'archive'|'delete') => {
    const selectedGroups = allGroups.filter(group => selected.includes(group.id))
    const selectedGroupIds = new Set(selectedGroups.map(group => group.id))
    const selectedLabels = data.labels.filter(label => selected.includes(label.id))
    if (action === 'favorite') {
      const existing = new Set(data.favorites.filter(item => item.resourceType === 'label').map(item => item.resourceId))
      await run(() => Promise.all(selected.filter(id => !existing.has(id)).map(id => addFavorite('label', id))))
    } else if (action === 'archive') {
      const archivedAt = scope === 'archived' ? '' : new Date().toISOString()
      await run(() => Promise.all([
        ...selectedGroups.map(group => updateLabelGroup(group.id, { archivedAt })),
        ...selectedLabels.filter(label => !selectedGroupIds.has(label.groupId ?? '')).map(label => isWorkspaceLabel(label) ? updateWorkspaceLabel(label.id, { archivedAt }) : updateTeamLabel(label.scope!, label.id, { archivedAt })),
      ]))
    } else {
      await run(async () => {
        await Promise.all(selectedLabels.map(label => isWorkspaceLabel(label) ? deleteWorkspaceLabel(label.id) : deleteTeamLabel(label.scope!, label.id)))
        await Promise.all(selectedGroups.map(group => deleteLabelGroup(group.id)))
      })
    }
    setSelected([])
  }
  const startCreating = (kind: 'label'|'group') => { setScope('workspace'); setCreating({ kind }); setSelected([]) }

  return <div className="domain-labels-page" data-i18n-ignore>
    <header className="settings-page-header domain-labels-header"><div><h1>{t(resourceType === 'issue' ? 'Issue labels' : 'Project labels')}</h1></div></header>
    <div className="domain-labels-toolbar">
      <div className="settings-list-toolbar domain-labels-search"><Search size={14}/><input aria-label={t('Filter labels')} placeholder={t('Filter by name…')} value={query} onChange={event => setQuery(event.target.value)}/></div>
      <ScopeButton value={scope} resourceType={resourceType} onChange={value => { setScope(value); setSelected([]) }}/>
      <span/>
      <button className="settings-action" disabled={scope === 'archived'} onClick={() => startCreating('group')}><Plus size={14}/>{t('New group')}</button>
      <button className="settings-action primary" disabled={scope === 'archived'} onClick={() => startCreating('label')}><Plus size={14}/>{t('New label')}</button>
    </div>
    <section className="settings-section domain-labels-section"><div className="domain-labels-grid">
      <div className="domain-labels-table-header">
        <LabelSortHeader label={t('Name')} sortKey="name" sort={sort} onSort={setSort}/>
        <LabelSortHeader label={t('Description')} sortKey="description" sort={sort} onSort={setSort}/>
        <LabelSortHeader label={t(resourceType === 'issue' ? 'Issues' : 'Projects')} sortKey="usage" sort={sort} onSort={setSort}/>
        <LabelSortHeader label={t('Last applied')} sortKey="lastAppliedAt" sort={sort} onSort={setSort}/>
        <LabelSortHeader label={t(scope === 'archived' ? 'Archived' : 'Created')} sortKey="createdAt" sort={sort} onSort={setSort}/>
      </div>
      {creating?.kind === 'group' && (
        <InlineLabelRow kind="group" onCancel={() => setCreating(null)} onSave={async input => { const group = await run(() => createLabelGroup({ ...input, resourceType })); if (group) setCreating({ kind: 'label', groupId: group.id }); return Boolean(group) }}/>
      )}
      {creating?.kind === 'label' && !creating.groupId && (
        <InlineLabelRow kind="label" onCancel={() => setCreating(null)} onSave={async input => { const label = await run(() => createWorkspaceLabel({ ...input, resourceType })); if (label) setCreating(null); return Boolean(label) }}/>
      )}
      {scope === 'all' ? scopes.map(item => <div className="domain-label-scope-block" key={item.id}>
        <ScopeSectionHeader label={item.id === 'workspace' ? t('Workspace') : item.label} count={item.labels.length} collapsed={collapsedScopes.includes(item.id)} onToggle={() => setCollapsedScopes(current => current.includes(item.id) ? current.filter(id => id !== item.id) : [...current, item.id])}/>
        {!collapsedScopes.includes(item.id) && renderSections(labelSections(item.labels, item.id === 'workspace' ? groups : [], true))}
      </div>) : scope === 'archived' ? <ArchivedRows groups={groups} labels={sortedLabels} data={data} resourceType={resourceType} availableGroups={allGroups.filter(group => !group.archivedAt)} selected={selected} onToggleGroupSelected={toggleGroupSelected} onToggleSelected={toggleSelected} onSaveLabel={saveLabel} onArchiveLabel={archiveLabel} onDeleteLabel={deleteLabel} onArchiveGroup={async group => { await run(() => updateLabelGroup(group.id, { archivedAt: '' })) }} onDeleteGroup={async group => { await run(() => deleteLabelGroup(group.id)) }}/> : renderSections(sections)}
      {!labels.length && !groups.length && !creating && <div className="domain-labels-empty">{t(scope === 'archived' ? 'No archived labels' : resourceType === 'issue' ? 'No issue labels' : 'No project labels')}</div>}
    </div></section>
    {selected.length > 0 && (
      <BulkLabelBar count={selected.length} archived={scope === 'archived'} onFavorite={() => void bulk('favorite')} onArchive={() => void bulk('archive')} onDelete={() => void bulk('delete')} onClear={() => setSelected([])}/>
    )}
  </div>
}

function ScopeButton({ value, resourceType, onChange }: { value: ScopeFilter; resourceType: 'issue'|'project'; onChange: (value: ScopeFilter) => void }) {
  const { t } = useI18n()
  const label = value === 'all' ? 'Workspace and teams' : value === 'archived' ? 'Archived' : 'Workspace'
  const options: ScopeFilter[] = resourceType === 'issue' ? ['workspace', 'all', 'archived'] : ['workspace', 'archived']
  return <DropdownMenu><DropdownMenuTrigger asChild><button className="settings-scope-select" role="combobox">{t(label)}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent data-i18n-ignore className="settings-scope-menu" align="start" alignOffset={-2} sideOffset={-37}>{options.map(item => <DropdownMenuItem key={item} onSelect={() => onChange(item)}>{t(item === 'all' ? 'Workspace and teams' : item === 'archived' ? 'Archived' : 'Workspace')}{item === value && <Check size={13}/>}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>
}

function LabelSortHeader({ label, sortKey, sort, onSort }: { label: string; sortKey: LabelSort; sort: { key: LabelSort; descending: boolean }; onSort: (value: { key: LabelSort; descending: boolean }) => void }) {
  const { locale } = useI18n()
  const active = sort.key === sortKey
  return <button aria-label={locale === 'zh-CN' ? `按${label}排序` : `Order by ${label}`} className={active ? 'active' : ''} onClick={() => onSort({ key: sortKey, descending: active ? !sort.descending : false })}>{label}{active && <ChevronDown size={12} className={sort.descending ? 'descending' : 'ascending'}/>}</button>
}

function LabelSectionRows({ section, data, resourceType, availableGroups, selected, creatingGroupId, onCancelCreate, onCreateInGroup, onCreateLabel, onToggleGroupSelected, onToggleSelected, onSaveLabel, onArchiveLabel, onMoveLabelToTeams, onDeleteLabel, onSaveGroup, onArchiveGroup, onDeleteGroup }: {
  section: LabelSection; data: BootstrapData; resourceType: 'issue'|'project'; availableGroups: LabelGroup[]; selected: string[]; creatingGroupId?: string;
  onCancelCreate: () => void; onCreateInGroup: (groupId: string) => void; onCreateLabel: (input: NewLabelInput, groupId: string) => Promise<boolean>; onToggleGroupSelected: (group: LabelGroup, childIds: string[]) => void; onToggleSelected: (id: string) => void; onSaveLabel: (label: IssueLabel, input: Partial<IssueLabel>) => Promise<void>;
  onArchiveLabel: (label: IssueLabel) => void; onMoveLabelToTeams: (label: IssueLabel) => Promise<void>; onDeleteLabel: (label: IssueLabel) => Promise<void>; onSaveGroup: (group: LabelGroup, input: Partial<LabelGroup>) => Promise<void>; onArchiveGroup: (group: LabelGroup) => Promise<void>; onDeleteGroup: (group: LabelGroup) => Promise<void>;
}) {
  const [collapsed, setCollapsed] = useState(false)
  return <>
    {section.group && <GroupRow group={section.group} collapsed={collapsed} selected={selected.includes(section.group.id)} onToggleCollapsed={() => setCollapsed(value => !value)} onToggleSelected={() => onToggleGroupSelected(section.group!, section.labels.map(label => label.id))} onCreateLabel={() => onCreateInGroup(section.group!.id)} onSave={input => onSaveGroup(section.group!, input)} onArchive={() => onArchiveGroup(section.group!)} onDelete={() => onDeleteGroup(section.group!)}/>}
    {!collapsed && section.group && creatingGroupId === section.group.id && <InlineLabelRow kind="label" grouped treeLast={!section.labels.length} continuous onCancel={onCancelCreate} onSave={input => onCreateLabel(input, section.group!.id)}/>}
    {!collapsed && section.labels.map((label,index) => <LabelRow key={label.id} data={data} label={label} grouped={Boolean(section.group)} treeLast={index===section.labels.length-1} resourceType={resourceType} selected={selected.includes(label.id)} groups={availableGroups} onToggleSelected={() => onToggleSelected(label.id)} onSave={input => onSaveLabel(label, input)} onArchive={() => onArchiveLabel(label)} onMoveToTeams={() => onMoveLabelToTeams(label)} onDelete={() => onDeleteLabel(label)}/>)}</>
}

interface NewLabelInput { name: string; description: string; color: string }
function InlineLabelRow({ kind, grouped = false, treeLast = false, continuous = false, onCancel, onSave }: { kind: 'label'|'group'; grouped?: boolean; treeLast?:boolean; continuous?: boolean; onCancel: () => void; onSave: (input: NewLabelInput) => Promise<boolean> }) {
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
    <input className="domain-label-description" aria-label={t(kind === 'group' ? 'Group description' : 'Label description')} disabled={!name.trim()} placeholder={t('Add label description…')} value={description} onChange={event => setDescription(event.target.value)}/><span/><span/><span/>
  </form>
}

function GroupRow({ group, collapsed = false, selected, onToggleCollapsed, onToggleSelected, onCreateLabel, onSave, onArchive, onDelete }: { group: LabelGroup; collapsed?: boolean; selected: boolean; onToggleCollapsed?: () => void; onToggleSelected: () => void; onCreateLabel?: () => void; onSave?: (input: Partial<LabelGroup>) => Promise<void>; onArchive: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { locale, t } = useI18n()
  const [editing, setEditing] = useState<'name'|'description'|null>(null)
  const [confirm,setConfirm]=useState<'archive'|'delete'|null>(null)
  const actionLabel = (action: 'expand'|'collapse'|'color') => locale === 'zh-CN' ? `${action === 'expand' ? '展开' : action === 'collapse' ? '收起' : '选择颜色'} ${group.name}` : `${action === 'expand' ? 'Expand' : action === 'collapse' ? 'Collapse' : 'Choose'} ${group.name}${action === 'color' ? ' color' : ''}`
  return <><div className={`domain-labels-row is-group${group.archivedAt ? ' is-archived' : ''}${selected ? ' is-selected' : ''}`}><SelectionCell kind="group" selected={selected} onToggle={onToggleSelected}/><div className="domain-label-name is-group-name">{onToggleCollapsed ? <button aria-label={actionLabel(collapsed ? 'expand' : 'collapse')} className="domain-label-group-chevron" onClick={onToggleCollapsed} type="button">{collapsed ? <ChevronRight/> : <ChevronDown/>}</button> : <span className="domain-label-group-chevron is-placeholder"/>}<LabelColorPicker color={group.color} disabled={Boolean(group.archivedAt)} kind="group" label={actionLabel('color')} onChange={color => onSave?.({ color })}/><LabelEditableText value={group.name} field={t('Name')} editing={editing === 'name'} archived={Boolean(group.archivedAt)} onEdit={() => setEditing('name')} onCancel={() => setEditing(null)} onSave={async value => { await onSave?.({ name: value }); setEditing(null) }}/></div><EditableDescription value={group.description ?? ''} editing={editing === 'description'} archived={Boolean(group.archivedAt)} onEdit={() => setEditing('description')} onCancel={() => setEditing(null)} onSave={async value => { await onSave?.({ description: value }); setEditing(null) }}/><span/><span/><span>{shortDate(group.archivedAt ?? group.createdAt, locale)}</span><GroupRowMenu group={group} onEdit={() => setEditing('name')} onCreateLabel={onCreateLabel} onArchive={async()=>{if(group.archivedAt)await onArchive();else setConfirm('archive')}} onDelete={async()=>setConfirm('delete')}/></div><LabelConfirmDialog kind="group" action={confirm} name={group.name} onClose={()=>setConfirm(null)} onConfirm={async()=>{if(confirm==='archive')await onArchive();else await onDelete();setConfirm(null)}}/></>
}

function LabelRow({ data, label, grouped, treeLast=false, resourceType, selected, groups, onToggleSelected, onSave, onArchive, onMoveToTeams, onDelete }: { data: BootstrapData; label: IssueLabel; grouped: boolean; treeLast?:boolean; resourceType: 'issue'|'project'; selected: boolean; groups: LabelGroup[]; onToggleSelected: () => void; onSave: (input: Partial<IssueLabel>) => Promise<void>; onArchive: () => void; onMoveToTeams?: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { locale, t } = useI18n()
  const [editing, setEditing] = useState<'name'|'description'|null>(null); const [confirm,setConfirm]=useState<'archive'|'delete'|null>(null); const count = labelUsage(label, data, resourceType)
  const colorLabel = locale === 'zh-CN' ? `选择 ${label.name} 的颜色` : `Choose ${label.name} color`
  return <><div className={`domain-labels-row${grouped ? ' is-nested' : ''}${label.archivedAt ? ' is-archived' : ''}${selected ? ' is-selected' : ''}`}><SelectionCell kind="label" selected={selected} onToggle={onToggleSelected}/><div className="domain-label-name">{grouped&&<GroupTreeBranch last={treeLast}/>}<LabelColorPicker color={label.color} disabled={Boolean(label.archivedAt)} kind="label" label={colorLabel} onChange={color => onSave({ color })}/><LabelEditableText value={label.name} field={t('Name')} editing={editing === 'name'} archived={Boolean(label.archivedAt)} onEdit={() => setEditing('name')} onCancel={() => setEditing(null)} onSave={async value => { await onSave({ name: value }); setEditing(null) }}/></div><EditableDescription value={label.description ?? ''} editing={editing === 'description'} archived={Boolean(label.archivedAt)} onEdit={() => setEditing('description')} onCancel={() => setEditing(null)} onSave={async value => { await onSave({ description: value }); setEditing(null) }}/><span>{count}</span><span>{label.lastAppliedAt ? relativeDate(label.lastAppliedAt, locale) : count ? t('Recently') : t('Never')}</span><span>{label.archivedAt ? shortDate(label.archivedAt, locale) : label.createdAt ? shortDate(label.createdAt, locale) : '—'}</span><LabelRowMenu label={label} groups={isWorkspaceLabel(label) ? groups : []} workspaceSlug={data.workspace.urlKey} resourceType={resourceType} onEdit={() => setEditing('name')} onGroup={groupId => onSave({ groupId })} onArchive={()=>{if(label.archivedAt)onArchive();else setConfirm('archive')}} onMoveToTeams={resourceType === 'issue' && isWorkspaceLabel(label) ? onMoveToTeams : undefined} onDelete={async()=>setConfirm('delete')}/></div><LabelConfirmDialog kind="label" action={confirm} name={label.name} onClose={()=>setConfirm(null)} onConfirm={async()=>{if(confirm==='archive')await onArchive();else await onDelete();setConfirm(null)}}/></>
}

function GroupTreeBranch({last}:{last:boolean}){return <span aria-hidden="true" className={`domain-label-tree-branch${last?' is-last':''}`}/>}

function LabelColorPicker({ color, disabled = false, kind, label, onChange }: { color: string; disabled?: boolean; kind: 'group'|'label'; label: string; onChange: (color: string) => void | Promise<void> }) {
  const { t } = useI18n()
  const customColorRef = useRef<HTMLInputElement>(null)
  const normalized = color.toLowerCase()
  const preset = FLOW_COLOR_PALETTE.some(option => option.value === normalized)
  return <Popover.Root><Popover.Trigger asChild><button aria-label={label} className="domain-label-color" data-kind={kind} disabled={disabled} type="button">{kind === 'group' ? <PaletteMark color={color}/> : <i style={{ background: color }}/>}</button></Popover.Trigger><Popover.Portal><Popover.Content data-i18n-ignore align="center" className="domain-label-color-popover" collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()} side="bottom" sideOffset={3}><div className="domain-label-color-presets">{FLOW_COLOR_PALETTE.map(option => <button aria-label={t(option.name)} data-selected={normalized === option.value} key={option.value} onClick={() => void onChange(option.value)} style={{ color: option.value }} type="button"><span style={{ background: option.value }}>{normalized === option.value && <ColorCheck/>}</span></button>)}</div><button aria-label={t('Set custom color')} className="domain-label-custom-color" data-selected={!preset} onClick={() => customColorRef.current?.click()} type="button"><span/>{!preset && <i/>}</button><input aria-hidden="true" className="domain-label-native-color" onChange={event => void onChange(event.target.value.toLowerCase())} ref={customColorRef} tabIndex={-1} type="color" value={/^#[0-9a-f]{6}$/i.test(color) ? color : '#95a2b3'}/></Popover.Content></Popover.Portal></Popover.Root>
}

function PaletteMark({ color }: { color: string }) { return <svg aria-hidden="true" fill={color} viewBox="0 0 16 16"><path clipRule="evenodd" d="M7.95 6A1.75 1.75 0 1 0 7.95 2.5 1.75 1.75 0 0 0 7.95 6ZM4.45 9.5A1.75 1.75 0 1 0 4.45 6a1.75 1.75 0 0 0 0 3.5ZM7.95 13a1.75 1.75 0 1 0 0-3.5 1.75 1.75 0 0 0 0 3.5Zm5.25-5.25a1.75 1.75 0 1 1-3.5 0 1.75 1.75 0 0 1 3.5 0Z" fillRule="evenodd"/></svg> }
function ColorCheck() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 10 8"><path d="M3.47 5.708 1.884 4.123a.576.576 0 0 0-.815.814l1.996 1.994a.576.576 0 0 0 .814 0L8.931 1.883a.576.576 0 0 0-.815-.814L3.47 5.708Z"/></svg> }

function SelectionCell({ kind, selected, onToggle }: { kind: 'label'|'group'; selected: boolean; onToggle: () => void }) { const { t } = useI18n(); return <label className="domain-label-indent domain-label-select"><input type="checkbox" aria-label={t(kind === 'group' ? 'Select group' : 'Select label')} checked={selected} onChange={onToggle}/><span>{selected && <Check size={11}/>}</span></label> }

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

function GroupRowMenu({ group, onEdit, onCreateLabel, onArchive, onDelete }: { group: LabelGroup; onEdit: () => void; onCreateLabel?: () => void; onArchive: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { t } = useI18n()
  return <MenuShell kind="group" label={group.name}>{group.archivedAt ? <><DropdownMenuItem onSelect={() => void onArchive()}><MenuIcon name="restore"/><span>{t('Restore')}</span></DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem></> : <><DropdownMenuItem onSelect={onEdit}><MenuIcon name="edit"/><span>{t('Edit label name')}</span><MenuShortcut>E</MenuShortcut></DropdownMenuItem><DropdownMenuItem onSelect={onCreateLabel}><MenuIcon name="add"/><span>{t('Add label to group')}</span></DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem onSelect={() => void onArchive()}><MenuIcon name="archive"/><span>{t('Archive…')}</span></DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem></>}</MenuShell>
}

function LabelRowMenu({ label, groups, workspaceSlug, resourceType, onEdit, onGroup, onArchive, onMoveToTeams, onDelete }: { label: IssueLabel; groups: LabelGroup[]; workspaceSlug: string; resourceType:'issue'|'project'; onEdit: () => void; onGroup: (groupId: string) => Promise<void>; onArchive: () => void; onMoveToTeams?: () => Promise<void>; onDelete: () => Promise<void> }) {
  const { locale, t } = useI18n()
  const [moveOpen, setMoveOpen] = useState(false)
  const [moving, setMoving] = useState(false)
  const viewLabeledIssues = () => { window.location.href = `/${encodeURIComponent(workspaceSlug)}/${resourceType==='project'?'projects/all':'issues/all'}?label=${encodeURIComponent(label.id)}` }
  const groupOptions = groups.filter(group => group.id !== label.groupId)
  const move = async () => { if (!onMoveToTeams) return; setMoving(true); try { await onMoveToTeams(); setMoveOpen(false) } finally { setMoving(false) } }
  return <><MenuShell kind="label" label={label.name}>{label.archivedAt ? <><DropdownMenuItem onSelect={onArchive}><MenuIcon name="restore"/><span>{t('Restore')}</span></DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem></> : <><DropdownMenuItem onSelect={onEdit}><MenuIcon name="edit"/><span>{t('Edit label name')}</span><MenuShortcut>E</MenuShortcut></DropdownMenuItem><DropdownMenuSub><DropdownMenuSubTrigger><MenuIcon name="move"/><span>{t('Move to group')}</span></DropdownMenuSubTrigger><DropdownMenuSubContent data-i18n-ignore className="domain-label-group-submenu" sideOffset={4}>{label.groupId && <DropdownMenuItem onSelect={() => void onGroup('')}><span>{t('Remove from group')}</span></DropdownMenuItem>}{label.groupId && groupOptions.length > 0 && <DropdownMenuSeparator/>}{groupOptions.map(group => <DropdownMenuItem key={group.id} onSelect={() => void onGroup(group.id)}><PaletteMark color={group.color}/><span>{group.name}</span></DropdownMenuItem>)}</DropdownMenuSubContent></DropdownMenuSub>{onMoveToTeams && <DropdownMenuItem onSelect={() => setMoveOpen(true)}><MenuIcon name="teams"/><span>{t('Move to teams…')}</span></DropdownMenuItem>}<DropdownMenuSeparator/><DropdownMenuItem onSelect={viewLabeledIssues}><MenuIcon name="view"/><span>{t(resourceType==='project'?'View labeled projects':'View labeled issues')}</span></DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem onSelect={onArchive}><MenuIcon name="archive"/><span>{t('Archive…')}</span></DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={() => void onDelete()}><MenuIcon name="delete"/><span>{t('Delete')}</span></DropdownMenuItem></>}</MenuShell><Dialog open={moveOpen} onOpenChange={setMoveOpen}><DialogContent data-i18n-ignore className="domain-label-move-dialog"><DialogTitle>{locale === 'zh-CN' ? <>将“<strong>{label.name}</strong>”移动到团队？</> : <>Move <strong>"{label.name}"</strong> to teams?</>}</DialogTitle><p>{t('This label will be moved to each team that uses it and will no longer be available for the whole workspace.')}</p><p>{t('This action cannot be undone.')}</p><footer><button disabled={moving} onClick={() => setMoveOpen(false)}>{t('Cancel')}</button><button className="primary" disabled={moving} onClick={() => void move()}>{t(moving ? 'Moving…' : 'Move label')}</button></footer></DialogContent></Dialog></>
}

function LabelConfirmDialog({kind,action,name,onClose,onConfirm}:{kind:'label'|'group';action:'archive'|'delete'|null;name:string;onClose:()=>void;onConfirm:()=>Promise<void>}){const{locale,t}=useI18n(),[busy,setBusy]=useState(false);const verb=action==='archive'?'Archive':'Delete';return <Dialog open={Boolean(action)} onOpenChange={open=>!open&&onClose()}><DialogContent data-i18n-ignore className="domain-label-confirm"><DialogTitle>{locale==='zh-CN'?<>{t(verb)}“<strong>{name}</strong>”？</>:<>{verb} <strong>"{name}"</strong>?</>}</DialogTitle><p>{t(action==='archive'?(kind==='label'?'This label will no longer be available to apply to projects. Projects with the label already applied will remain unchanged.':'Labels in this group will no longer be available to apply. Existing projects remain unchanged.'):(kind==='label'?'This label will be permanently deleted from the workspace.':'This label group will be permanently deleted from the workspace.'))}</p><footer><button disabled={busy} onClick={onClose}>{t('Cancel')}</button><button className="primary" disabled={busy} onClick={()=>{setBusy(true);void onConfirm().finally(()=>setBusy(false))}}>{t(verb)}</button></footer></DialogContent></Dialog>}

type LabelMenuIcon = 'edit'|'add'|'move'|'teams'|'view'|'archive'|'delete'|'restore'
function MenuIcon({ name }: { name: LabelMenuIcon }) {
  if (name === 'edit') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M10.1805 3.34195 4.14166 9.416c1.18782.35421 2.15072 1.2469 2.59842 2.4024L12.6877 5.8425c-1.0235-.62127-1.8834-1.47898-2.5072-2.50055Z"/><path d="M13.7391 4.71631c.4184-.68683.3336-1.59893-.2545-2.19441-.5938-.60118-1.5062-.68298-2.1866-.24541.5567 1.03483 1.4057 1.8835 2.4411 2.43982Z"/><path d="M3.03104 10.7502c1.27192.0156 2.33541.9921 2.46679 2.2612-.66515.4146-2.09586.7808-2.96669.9772-.33104.0746-.61088-.2284-.51039-.5513.23251-.7471.62517-1.9237 1.01029-2.6871Z"/></svg>
  if (name === 'add') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M8.75 4a.75.75 0 0 0-1.5 0v3.25H4a.75.75 0 0 0 0 1.5h3.25V12a.75.75 0 0 0 1.5 0V8.75H12a.75.75 0 0 0 0-1.5H8.75V4Z"/></svg>
  if (name === 'move') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M10.3262 4.51988a.75.75 0 0 0-1.15235.96028L10.6487 7.25H3.75a.75.75 0 0 0 0 1.5h6.8988l-1.47495 1.7699a.75.75 0 0 0 1.15235.9603l2.5-3.00004a.75.75 0 0 0 0-.96028l-2.5-3Z"/></svg>
  if (name === 'teams') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="m11.6 9 .072.005.016.001a.6.6 0 0 1 .313.148l2.887 2.592a.4.4 0 0 1 0 .595l-2.887 2.593A.6.6 0 0 1 11 14.487v-1.114C6.333 12.77 4 12.313 4 12.002c0-.311 2.333-.768 7-1.371V9.6a.6.6 0 0 1 .6-.6ZM6.782 7.645l.914.562a2.702 2.702 0 0 1 1.2 1.668c-2.96.437-4.76.785-5.084 1.044-.476.207-.765.419-.807.944L3 12h-.5A1.5 1.5 0 0 1 1 10.5c0-.87.42-1.684 1.119-2.19l.874-.551a3.768 3.768 0 0 1 3.789-.114Zm6 0 .914.562A2.702 2.702 0 0 1 14.969 9.5c0 .285-.08.552-.218.78l-2.082-1.87a1.6 1.6 0 0 0-2.662 1.044L10 9.719l-.031.004A3 3 0 0 0 8.76 7.724l-.605-.439.838-.526a3.768 3.768 0 0 1 3.789-.114ZM4.969 2a2 2 0 1 1 0 4 2 2 0 0 1 0-4Zm6-1a2 2 0 1 1 0 4 2 2 0 0 1 0-4Z"/></svg>
  if (name === 'view') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" d="M13.25 5.25A1.75 1.75 0 0 1 15 7v4.75A3.25 3.25 0 0 1 11.75 15h-5a1.75 1.75 0 0 1-1.75-1.75.75.75 0 0 1 1.5 0c0 .138.112.25.25.25h5a1.75 1.75 0 0 0 1.75-1.75V7a.25.25 0 0 0-.25-.25.75.75 0 0 1 0-1.5Z" clipRule="evenodd"/><path fillRule="evenodd" d="M8.154 1.004A3 3 0 0 1 11 4v4a3 3 0 0 1-2.846 2.996L8 11H4a3 3 0 0 1-2.996-2.846L1 8V4a3 3 0 0 1 2.846-2.996L4 1h4l.154.004ZM4 2.5A1.5 1.5 0 0 0 2.5 4v4A1.5 1.5 0 0 0 4 9.5h4A1.5 1.5 0 0 0 9.5 8V4A1.5 1.5 0 0 0 8 2.5H4Z" clipRule="evenodd"/></svg>
  if (name === 'archive') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" d="M9.25 8a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1 0-1.5h2.5Z" clipRule="evenodd"/><path fillRule="evenodd" d="M12.75 2A2.25 2.25 0 0 1 15 4.25v1.5c0 .605-.43 1.109-1 1.225v4.775A2.25 2.25 0 0 1 11.75 14H4.2a2.25 2.25 0 0 1-2.25-2.25V6.962A1.25 1.25 0 0 1 1 5.75v-1.5A2.25 2.25 0 0 1 3.25 2h9.5ZM3.45 11.75c0 .414.336.75.75.75h7.55a.75.75 0 0 0 .75-.75V7H3.45v4.75ZM3.25 3.5a.75.75 0 0 0-.75.75V5.5h11V4.25a.75.75 0 0 0-.75-.75h-9.5Z" clipRule="evenodd"/></svg>
  if (name === 'delete') return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path fillRule="evenodd" d="m2 3 1.652 9.911A2.5 2.5 0 0 0 6.118 15h3.764a2.5 2.5 0 0 0 2.466-2.089L14 3H2Zm1.77 1.5 1.361 8.164a1 1 0 0 0 .987.836h3.764a1 1 0 0 0 .987-.836l1.36-8.164H3.771Z" clipRule="evenodd"/><path d="M5.5 2.5A1.5 1.5 0 0 1 7 1h2a1.5 1.5 0 0 1 1.5 1.5v1h-5v-1Z"/><path d="M1 3.75A.75.75 0 0 1 1.75 3h12.5a.75.75 0 0 1 0 1.5H1.75A.75.75 0 0 1 1 3.75Z"/></svg>
  return <svg aria-hidden="true" className="domain-label-menu-icon" viewBox="0 0 16 16"><path d="M8 2.25a5.75 5.75 0 1 1-5.51 7.4.75.75 0 1 1 1.436-.43A4.25 4.25 0 1 0 5.2 4.75H7a.75.75 0 0 1 0 1.5H3.5a.75.75 0 0 1-.75-.75V2a.75.75 0 0 1 1.5 0v1.54A5.72 5.72 0 0 1 8 2.25Z"/></svg>
}

function MenuShortcut({ children }: { children: ReactNode }) { return <kbd className="domain-label-menu-shortcut">{children}</kbd> }

function ScopeSectionHeader({ label, count, collapsed, onToggle }: { label: string; count: number; collapsed: boolean; onToggle: () => void }) {
  const { t } = useI18n()
  return <div className="domain-label-scope-header"><button aria-label={t(collapsed ? 'Expand group' : 'Collapse group')} onClick={onToggle}>{collapsed ? <ChevronRight size={13}/> : <ChevronDown size={13}/>}</button><strong>{label}</strong><span>{count}</span></div>
}

function BulkLabelBar({ count, archived, onFavorite, onArchive, onDelete, onClear }: { count: number; archived: boolean; onFavorite: () => void; onArchive: () => void; onDelete: () => void; onClear: () => void }) {
  const { t } = useI18n()
  return <div className="domain-label-bulk" data-i18n-ignore><strong>{count}</strong><span>{t('selected')}</span><DropdownMenu><DropdownMenuTrigger asChild><button>{t('Actions')}<ChevronDown size={13}/></button></DropdownMenuTrigger><DropdownMenuContent data-i18n-ignore className="domain-label-row-menu is-bulk-menu" align="center"><DropdownMenuItem onSelect={onFavorite}><Star size={14}/>{t('Favorite labels')}</DropdownMenuItem><DropdownMenuSeparator/><DropdownMenuItem onSelect={onArchive}>{archived ? <RotateCcw size={14}/> : <Archive size={14}/>} {t(archived ? 'Restore labels…' : 'Archive labels…')}</DropdownMenuItem><DropdownMenuItem className="danger-item" onSelect={onDelete}><Trash2 size={14}/>{t('Delete labels…')}</DropdownMenuItem></DropdownMenuContent></DropdownMenu><button aria-label={t('Clear selected')} onClick={onClear}><X size={14}/></button></div>
}

function ArchivedRows({ groups, labels, data, resourceType, availableGroups, selected, onToggleGroupSelected, onToggleSelected, onSaveLabel, onArchiveLabel, onDeleteLabel, onArchiveGroup, onDeleteGroup }: { groups: LabelGroup[]; labels: IssueLabel[]; data: BootstrapData; resourceType: 'issue'|'project'; availableGroups: LabelGroup[]; selected: string[]; onToggleGroupSelected: (group: LabelGroup, childIds: string[]) => void; onToggleSelected: (id: string) => void; onSaveLabel: (label: IssueLabel, input: Partial<IssueLabel>) => Promise<void>; onArchiveLabel: (label: IssueLabel) => void; onDeleteLabel: (label: IssueLabel) => Promise<void>; onArchiveGroup: (group: LabelGroup) => Promise<void>; onDeleteGroup: (group: LabelGroup) => Promise<void> }) {
  const { t } = useI18n()
  const childIds = (group: LabelGroup) => labels.filter(label => label.groupId === group.id).map(label => label.id)
  return <><div className="domain-label-scope-header is-static"><strong>{t('Workspace')}</strong><span>{groups.length + labels.length}</span></div>{[...groups].sort((a, b) => a.name.localeCompare(b.name)).map(group => <GroupRow key={group.id} group={group} selected={selected.includes(group.id)} onToggleSelected={() => onToggleGroupSelected(group, childIds(group))} onArchive={() => onArchiveGroup(group)} onDelete={() => onDeleteGroup(group)}/>)}{labels.map(label => <LabelRow key={label.id} data={data} label={label} grouped={false} resourceType={resourceType} selected={selected.includes(label.id)} groups={availableGroups} onToggleSelected={() => onToggleSelected(label.id)} onSave={input => onSaveLabel(label, input)} onArchive={() => onArchiveLabel(label)} onDelete={() => onDeleteLabel(label)}/>)}</>
}

interface LabelSection { id: string; group?: LabelGroup; labels: IssueLabel[] }
function labelSections(labels: IssueLabel[], groups: LabelGroup[], hideEmpty: boolean): LabelSection[] { const grouped = new Set<string>(); const sections: LabelSection[] = groups.map(group => { const items = labels.filter(label => label.groupId === group.id); items.forEach(label => grouped.add(label.id)); return { id: group.id, group, labels: items } }).filter(section => !hideEmpty || section.labels.length > 0); const rest = labels.filter(label => !grouped.has(label.id)); if (rest.length) sections.unshift({ id: `ungrouped-${rest[0]?.scope ?? 'workspace'}`, labels: rest }); return sections }
function scopeSections(labels: IssueLabel[], data: BootstrapData) { const sections: { id: string; label: string; labels: IssueLabel[] }[] = []; const workspace = labels.filter(isWorkspaceLabel); if (workspace.length) sections.push({ id: 'workspace', label: 'Workspace', labels: workspace }); for (const team of data.teams) { const items = labels.filter(label => label.scope === team.id); if (items.length) sections.push({ id: team.id, label: team.name, labels: items }) } return sections }
function labelUsage(label: IssueLabel, data: BootstrapData, resourceType: 'issue'|'project') { return resourceType === 'issue' ? data.issues.filter(issue => issue.labels.some(item => item.id === label.id)).length : data.projects.filter(project => (project.labelIds ?? []).includes(label.id)).length }
const deliveryWorkflowOrder = new Map([
  'label_type_requirement', 'label_type_development', 'label_type_defect',
].map((id, index) => [id, index]))
function sortLabels(labels: IssueLabel[], sort: { key: LabelSort; descending: boolean }, data: BootstrapData, resourceType: 'issue'|'project') { return [...labels].sort((left, right) => { if (sort.key === 'workflow') return (deliveryWorkflowOrder.get(left.id) ?? 1000) - (deliveryWorkflowOrder.get(right.id) ?? 1000) || left.name.localeCompare(right.name); const value = (label: IssueLabel) => sort.key === 'usage' ? labelUsage(label, data, resourceType) : sort.key === 'name' ? label.name.toLowerCase() : sort.key === 'description' ? (label.description ?? '').toLowerCase() : sort.key === 'lastAppliedAt' ? (label.lastAppliedAt ?? '') : label.createdAt ?? ''; const result = String(value(left)).localeCompare(String(value(right)), undefined, { numeric: true }); return sort.descending ? -result : result }) }
function shortDate(value: string, locale: 'en-US'|'zh-CN') { return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(new Date(value)) }
function relativeDate(value: string, locale: 'en-US'|'zh-CN') { const days = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 86400000)); if (locale === 'zh-CN') { if (days === 0) return '今天'; if (days < 30) return `${days} 天前` } else { if (days === 0) return 'Today'; if (days === 1) return '1 day ago'; if (days < 30) return `${days} days ago` } return shortDate(value, locale) }

function message(error:unknown){return error instanceof Error?error.message:'Could not save setting'}
