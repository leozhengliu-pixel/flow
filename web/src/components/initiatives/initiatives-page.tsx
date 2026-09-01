import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import * as Popover from '@radix-ui/react-popover'
import { Bell, Check, ChevronRight, Clock3, Copy, Edit3, MessageSquare, MoreHorizontal, MousePointer2, Plus, Search, Send, Star, Trash2, X } from 'lucide-react'
import { Fragment, useEffect, useMemo, useState } from 'react'
import { Avatar } from '@/components/issue/issue-row'
import { PriorityIcon } from '@/components/issue/issue-icons'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { Initiative, InitiativeMutationInput, InitiativeStatus, InitiativeUpdate, IssueLabel, Project, ProjectUpdate, Team, User } from '@/types/flow'
import { initiativePath, initiativesPath, type InitiativeRouteTab, type InitiativesRouteView } from '@/lib/app-routes'
import { InitiativeLabelsPicker, InitiativeProperties, InitiativeStatusIcon } from './initiative-shared'
import { DisplayIcon, FilterIcon as Filter, PlusIcon, SidebarIcon } from '@/components/ui/view-action-icons'
import { SelectControl } from '@/components/ui/select-control'
import { titleCase } from './initiative-model'
import './initiatives.css'
import './initiatives-list-parity.css'
import './initiative-controls.css'

type Props = {
  initiatives: Initiative[]
  initiativeUpdates: Record<string, InitiativeUpdate[]>
  projects: Project[]
  projectUpdates: Record<string, ProjectUpdate[]>
  users: User[]
  teams: Team[]
  labels: IssueLabel[]
  viewer: User
  view: InitiativesRouteView
  onViewChange: (view: InitiativesRouteView) => void
  onOpen: (initiative: Initiative, tab?: InitiativeRouteTab) => void
  onCreate: (input: InitiativeMutationInput & { name: string }) => Promise<Initiative>
  onCreateLabel: (name: string) => Promise<IssueLabel>
  onCreateUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdate: (id: string, input: InitiativeMutationInput) => Promise<Initiative>
  onDelete: (id: string) => Promise<void>
  onCreateReminder: (id: string, remindAt: string) => Promise<unknown>
  onOpenSidebar?: () => void
  createOnMount?: boolean
  displayDefault?: { grouping?: string; ordering?: string; properties?: string[]; showTeamInitiatives?: boolean }
  onSetDefault: (value: { grouping: string; ordering: string; properties: string[]; showTeamInitiatives: boolean }) => Promise<void>
}

const PROPERTY_ORDER = ['description', 'owner', 'status', 'leadTeam', 'teams', 'priority', 'health', 'projects', 'activeProjects', 'target', 'created', 'updated', 'completed', 'labels'] as const
type Property = typeof PROPERTY_ORDER[number]
const TABLE_PROPERTY_ORDER: Property[] = ['status', 'priority', 'owner', 'leadTeam', 'target', 'projects', 'health', 'activeProjects', 'labels', 'teams', 'created', 'updated', 'completed']
const DEFAULT_PROPERTIES: Property[] = ['description', 'status', 'priority', 'owner', 'leadTeam', 'target', 'projects', 'health', 'activeProjects']
type Sort = 'manual' | 'name' | 'status' | 'priority' | 'target' | 'health' | 'created' | 'updated'
type Grouping = 'none'|'contributingTeam'|'leadTeam'|'owner'|'health'|'status'|'priority'|'label'
type FilterState = { status?: InitiativeStatus; priority?: number; ownerId?: string; creatorId?: string; leadTeamId?: string; teamId?: string; health?: Project['health']; labelId?: string; date?: 'created7'|'updated7'|'targetMonth'|'completed' }

export function InitiativesPage(props: Props) {
  const { initiatives, initiativeUpdates, projects, projectUpdates, users, teams, labels, viewer, view, displayDefault, onViewChange, onOpen, onCreate, onCreateLabel, onCreateUpdate, onUpdate, onDelete, onCreateReminder, onSetDefault, onOpenSidebar, createOnMount = false } = props
  const defaultProperties = displayDefault?.properties?.filter((item): item is Property => PROPERTY_ORDER.includes(item as Property)) ?? DEFAULT_PROPERTIES
  const defaultSort = displayDefault?.ordering && ['manual', 'name', 'status', 'priority', 'target', 'health', 'created', 'updated'].includes(displayDefault.ordering) ? displayDefault.ordering as Sort : 'manual'
  const defaultGrouping = displayDefault?.grouping && ['none','contributingTeam','leadTeam','owner','health','status','priority','label'].includes(displayDefault.grouping) ? displayDefault.grouping as Grouping : 'none'
  const defaultShowTeam = displayDefault?.showTeamInitiatives ?? true
  const [creating, setCreating] = useState(false)
  const [filters, setFilters] = useState<FilterState>({})
  const [filterMode, setFilterMode] = useState<'all'|'any'>('all')
  const [advancedFilterEnabled, setAdvancedFilterEnabled] = useState(false)
  const [advancedFilterOpen, setAdvancedFilterOpen] = useState(false)
  const [properties, setProperties] = useState<Set<Property>>(() => displayDefault ? new Set(defaultProperties) : readInitiativeProperties())
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [sort, setSort] = useState<Sort>(() => displayDefault ? defaultSort : readInitiativeSetting('sort', ['manual', 'name', 'status', 'priority', 'target', 'health', 'created', 'updated'], 'manual'))
  const [grouping, setGrouping] = useState<Grouping>(() => displayDefault ? defaultGrouping : readInitiativeSetting('grouping', ['none','contributingTeam','leadTeam','owner','health','status','priority','label'], 'none'))
  const [showTeamInitiatives, setShowTeamInitiatives] = useState(() => displayDefault ? defaultShowTeam : localStorage.getItem('flow:initiatives:show-team') !== 'false')
  const [updatesInitiative, setUpdatesInitiative] = useState<Initiative>()
  const [detailsOpen, setDetailsOpen] = useState(() => window.innerWidth > 800 && localStorage.getItem('flow:initiatives:details-open') === 'true')

  const visible = useMemo(() => initiatives.filter(item => {
    const inView = view === 'active' ? item.status === 'active' : view === 'planned' ? item.status === 'planned' || item.status === 'proposed' : true
    const matches = Object.entries(filters).map(([key, value]) => matchesInitiativeFilter(item, key as keyof FilterState, value as NonNullable<FilterState[keyof FilterState]>))
    const matchesFilters = matches.length === 0 || (filterMode === 'all' ? matches.every(Boolean) : matches.some(Boolean))
    return inView
      && (showTeamInitiatives || !(item.leadTeamId && item.contributingTeamIds.includes(item.leadTeamId)))
      && matchesFilters
  }).sort((a, b) => {
    if (sort === 'manual') return (a.position ?? 0) - (b.position ?? 0)
    if (sort === 'name') return a.name.localeCompare(b.name)
    if (sort === 'status') return ['proposed', 'planned', 'active', 'completed', 'canceled'].indexOf(a.status) - ['proposed', 'planned', 'active', 'completed', 'canceled'].indexOf(b.status)
    if (sort === 'priority') return a.priority - b.priority
    if (sort === 'target') return (a.targetDate ?? '').localeCompare(b.targetDate ?? '')
    if (sort === 'health') return a.health.localeCompare(b.health)
    if (sort === 'created') return a.createdAt.localeCompare(b.createdAt)
    if (sort === 'updated') return a.updatedAt.localeCompare(b.updatedAt)
    return 0
  }), [filterMode, filters, initiatives, showTeamInitiatives, sort, view])

  const columns = TABLE_PROPERTY_ORDER.filter(property => properties.has(property))
  const showDetails = detailsOpen && view !== 'planned'
  const columnGrid = `8px 20px minmax(280px,1fr) ${columns.map(property => columnWidth(property, showDetails)).join(' ')} 12px`
  const workspaceSlug = location.pathname.split('/').filter(Boolean)[0] ?? ''
  const grouped = groupInitiatives(visible, grouping, teams, labels)
  const displayDirty = grouping !== defaultGrouping || sort !== defaultSort || showTeamInitiatives !== defaultShowTeam || !sameStringSet(properties, new Set(defaultProperties))
  const resetDisplay = () => { setGrouping(defaultGrouping); setSort(defaultSort); setShowTeamInitiatives(defaultShowTeam); setProperties(new Set(defaultProperties)) }
  const toggleProperty = (property: Property) => setProperties(current => {
    const next = new Set(current)
    if (next.has(property)) next.delete(property); else next.add(property)
    return next
  })
  const toggleSelected = (id: string) => setSelected(current => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })

  useEffect(() => {
    let newShortcutAt = 0
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.target as HTMLElement | null)?.closest('input,textarea,[contenteditable="true"],[role="textbox"]')) return
      const key = event.key.toLowerCase()
      if (key === 'n') { newShortcutAt = Date.now(); return }
      if (key === 'i' && Date.now() - newShortcutAt < 1200) {
        event.preventDefault(); setCreating(true); newShortcutAt = 0
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
  useEffect(() => {
    if (createOnMount) setCreating(true)
  }, [createOnMount])
  useEffect(() => { localStorage.setItem('flow:initiatives:properties-version', '2'); localStorage.setItem('flow:initiatives:properties', JSON.stringify([...properties])) }, [properties])
  useEffect(() => localStorage.setItem('flow:initiatives:sort', sort), [sort])
  useEffect(() => localStorage.setItem('flow:initiatives:grouping', grouping), [grouping])
  useEffect(() => localStorage.setItem('flow:initiatives:show-team', String(showTeamInitiatives)), [showTeamInitiatives])

  const toggleDetails = () => setDetailsOpen(open => {
    localStorage.setItem('flow:initiatives:details-open', String(!open))
    return !open
  })

  return <main className="main-panel li-page">
    <header className="li-page-header">
      <button className="li-mobile-menu" onClick={onOpenSidebar} type="button">☰</button>
      <h2>Initiatives</h2>
      <button aria-label="New initiative" className="li-new-initiative" onClick={() => setCreating(true)} type="button"><PlusIcon/><span>New initiative</span></button>
    </header>
    <div className="li-toolbar">
      <nav>{(['active', 'planned', 'all'] as InitiativesRouteView[]).map(item => <a className="ui-pill" aria-current={view === item ? 'page' : undefined} href={initiativesPath(workspaceSlug, item)} key={item} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); onViewChange(item) }}>{item === 'all' ? 'All initiatives' : titleCase(item)}</a>)}</nav>
      <div className="li-toolbar-actions">
        <InitiativeFilterMenu filters={filters} initiatives={initiatives} labels={labels} onAdvanced={() => { setAdvancedFilterEnabled(true); setAdvancedFilterOpen(true) }} onChange={setFilters} teams={teams} users={users}/>
        <InitiativeDisplayMenu dirty={displayDirty} grouping={grouping} properties={properties} showTeamInitiatives={showTeamInitiatives} sort={sort} onGrouping={setGrouping} onProperty={toggleProperty} onReset={resetDisplay} onSetDefault={() => onSetDefault({ grouping, ordering: sort, properties: [...properties], showTeamInitiatives })} onShowTeamInitiatives={setShowTeamInitiatives} onSort={setSort}/>
        {view !== 'planned' && <button aria-expanded={detailsOpen} aria-label="Close sidebar" className="li-icon-button ui-pill" onClick={toggleDetails} type="button"><SidebarIcon/></button>}
      </div>
    </div>
    {advancedFilterEnabled ? <AdvancedFilterBar filters={filters} initiatives={initiatives} labels={labels} mode={filterMode} open={advancedFilterOpen} teams={teams} users={users} onChange={setFilters} onMode={setFilterMode} onOpenChange={setAdvancedFilterOpen} onRemove={() => { setFilters({}); setAdvancedFilterEnabled(false); setAdvancedFilterOpen(false) }}/> : Object.keys(filters).length > 0 && <div className="li-filter-chips">{Object.entries(filters).map(([key, value]) => <button key={key} onClick={() => setFilters(current => { const next = { ...current }; delete next[key as keyof FilterState]; return next })} type="button"><span>{filterLabel(key, value, users, teams, labels)}</span><X size={11}/></button>)}<button onClick={() => setFilters({})} type="button">Clear all</button></div>}
    <div className={`li-list-body${showDetails ? ' has-details' : ''}`}>
      <div className={`li-table${showDetails ? ' has-details' : ''}`} style={{ '--li-extra-columns': columns.length } as React.CSSProperties}>
        {visible.length > 0 && <div className="li-columns" style={{ gridTemplateColumns: columnGrid }}><span aria-hidden="true"/><span aria-hidden="true"/><button aria-label="Order by Name" onClick={() => setSort('name')} style={{ gridColumn: 3 }} type="button">Name<InitiativeSortIcon/></button>{columns.map((property, index) => <ColumnHeader gridColumn={index + 4} key={property} property={property} onSort={setSort}/>)}</div>}
        {creating && <InitiativeCreateRow labels={labels} teams={teams} users={users} viewer={viewer} view={view} onCancel={() => setCreating(false)} onCreate={async input => { await onCreate(input); setCreating(false) }} onCreateLabel={onCreateLabel}/>}
        {grouped.map(group => <Fragment key={group.key}>{grouping !== 'none' && <div className="li-group-heading"><span data-i18n-ignore={['owner','leadTeam','contributingTeam','label'].includes(grouping) ? true : undefined}>{group.label}</span><small>{group.items.length}</small></div>}{group.items.map(initiative => <InitiativeRow columns={columns} grid={columnGrid} href={initiativePath(workspaceSlug, initiative)} initiative={initiative} initiativeUpdates={initiativeUpdates[initiative.id] ?? []} key={initiative.id} labels={labels} projects={projects} projectUpdates={projectUpdates} properties={properties} selected={selected.has(initiative.id)} teams={teams} users={users} onCreateLabel={onCreateLabel} onCreateReminder={remindAt => onCreateReminder(initiative.id, remindAt)} onDelete={onDelete} onOpen={onOpen} onOpenUpdates={() => setUpdatesInitiative(initiative)} onSelect={() => toggleSelected(initiative.id)} onUpdate={input => onUpdate(initiative.id, input)}/>)}</Fragment>)}
        {!creating && !visible.length && <InitiativesEmpty filtered={Object.keys(filters).length > 0} onCreate={() => setCreating(true)} view={view}/>} 
      </div>
      {showDetails && <InitiativesListSidebar initiatives={visible} teams={teams} users={users} view={view}/>}
    </div>
    {selected.size > 0 && <InitiativesBulkBar initiatives={initiatives.filter(item => selected.has(item.id))} labels={labels} users={users} onClear={() => setSelected(new Set())} onDelete={async () => { await Promise.all([...selected].map(onDelete)); setSelected(new Set()) }} onUpdate={input => Promise.all([...selected].map(id => onUpdate(id, input)))}/>} 
    {updatesInitiative && <InitiativeUpdatesPanel initiative={updatesInitiative} updates={initiativeUpdates[updatesInitiative.id] ?? []} viewer={viewer} onClose={() => setUpdatesInitiative(undefined)} onCreateUpdate={onCreateUpdate} onOpen={() => onOpen(updatesInitiative, 'activity')} onUpdate={input => onUpdate(updatesInitiative.id, input)}/>}
  </main>
}

function InitiativesListSidebar({ initiatives, users, teams, view }: { initiatives: Initiative[]; users: User[]; teams: Team[]; view: InitiativesRouteView }) {
  const [tab, setTab] = useState<'owner' | 'leadTeam' | 'health'>('owner')
  if (view === 'planned' || initiatives.length === 0) return <aside className="li-list-sidebar"><div className="li-list-sidebar-empty">No initiative details</div></aside>
  const ownerCounts = users.map(user => ({ user, count: initiatives.filter(item => item.owner?.id === user.id).length })).filter(item => item.count > 0)
  const teamCounts = teams.map(team => ({ team, count: initiatives.filter(item => item.leadTeamId === team.id).length })).filter(item => item.count > 0)
  const healthCounts = (['onTrack', 'atRisk', 'offTrack', 'noUpdate'] as Project['health'][]).map(health => ({ health, count: initiatives.filter(item => item.health === health).length })).filter(item => item.count > 0)
  return <aside className="li-list-sidebar"><div className="li-list-sidebar-tabs" role="tablist"><button aria-selected={tab === 'owner'} onClick={() => setTab('owner')} role="tab" type="button">Owner</button><button aria-selected={tab === 'leadTeam'} onClick={() => setTab('leadTeam')} role="tab" type="button">Lead team</button><button aria-selected={tab === 'health'} onClick={() => setTab('health')} role="tab" type="button">Health</button></div><div className="li-list-sidebar-items">{tab === 'owner' ? ownerCounts.map(({ user, count }) => <button key={user.id} type="button"><Avatar name={user.displayName || user.name}/><span className="li-list-sidebar-label" data-i18n-ignore>{user.displayName || user.name}</span><small>{count}</small></button>) : tab === 'leadTeam' ? teamCounts.map(({ team, count }) => <button key={team.id} type="button"><span className="li-team-mark" style={{ background: team.color }}>{team.key.slice(0, 2)}</span><span className="li-list-sidebar-label" data-i18n-ignore>{team.name}</span><small>{count}</small></button>) : healthCounts.map(({ health, count }) => <button key={health} type="button"><i className={`li-update-health is-${health}`}/><span className="li-list-sidebar-label">{healthLabel(health)}</span><small>{count}</small></button>)}</div></aside>
}

function InitiativesBulkBar({ initiatives, users, labels, onClear, onDelete, onUpdate }: { initiatives: Initiative[]; users: User[]; labels: IssueLabel[]; onClear: () => void; onDelete: () => Promise<void>; onUpdate: (input: InitiativeMutationInput) => Promise<unknown> }) {
  const allHaveLabel = (id: string) => initiatives.every(item => item.labelIds.includes(id))
  return <div aria-label={`${initiatives.length} selected initiatives`} className="li-bulk" role="toolbar"><span><strong>{initiatives.length}</strong> selected</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button">⌘&nbsp; Actions</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="center" className="li-menu li-bulk-menu" sideOffset={7}>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger>Status<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{(['proposed', 'planned', 'active', 'completed', 'canceled'] as InitiativeStatus[]).map(status => <DropdownMenu.Item key={status} onSelect={() => void onUpdate({ status })}><InitiativeStatusIcon status={status}/>{titleCase(status)}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger>Priority<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{[0, 1, 2, 3, 4].map(priority => <DropdownMenu.Item key={priority} onSelect={() => void onUpdate({ priority })}><PriorityIcon priority={priority} size={14}/>{['No priority', 'Urgent', 'High', 'Medium', 'Low'][priority]}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger>Owner<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu"><DropdownMenu.Item onSelect={() => void onUpdate({ ownerId: '' })}>No owner</DropdownMenu.Item>{users.map(user => <DropdownMenu.Item key={user.id} onSelect={() => void onUpdate({ ownerId: user.id })}><Avatar name={user.displayName || user.name}/><span data-i18n-ignore>{user.displayName || user.name}</span></DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger>Labels<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{labels.map(label => <DropdownMenu.Item key={label.id} onSelect={() => void onUpdate({ labelIds: allHaveLabel(label.id) ? initiatives.flatMap(item => item.labelIds).filter((id, index, ids) => id !== label.id && ids.indexOf(id) === index) : [...new Set(initiatives.flatMap(item => item.labelIds).concat(label.id))] })}><i className="li-filter-color" style={{ background: label.color }}/><span data-i18n-ignore>{label.name}</span>{allHaveLabel(label.id) && <Check className="li-menu-end" size={12}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={() => void onDelete()}><Trash2 size={14}/>Delete</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root><button aria-label="Ask Flow" aria-disabled="true" disabled title="Flow AI is not configured for this workspace" type="button"><MousePointer2 size={14}/></button><button aria-label="Clear selected" onClick={onClear} type="button"><X size={14}/></button></div>
}

function InitiativeRow({ initiative, initiativeUpdates, projects, projectUpdates, properties, columns, grid, href, selected, users, teams, labels, onCreateLabel, onCreateReminder, onDelete, onOpen, onOpenUpdates, onSelect, onUpdate }: {
  initiative: Initiative; initiativeUpdates: InitiativeUpdate[]; projects: Project[]; projectUpdates: Record<string, ProjectUpdate[]>; properties: Set<Property>; columns: Property[]; grid: string; href: string; selected: boolean; users: User[]; teams: Team[]; labels: IssueLabel[]
  onCreateLabel: (name: string) => Promise<IssueLabel>; onCreateReminder: (remindAt: string) => Promise<unknown>; onDelete: (id: string) => Promise<void>; onOpen: (initiative: Initiative, tab?: InitiativeRouteTab) => void; onOpenUpdates: () => void; onSelect: () => void; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const linked = projects.filter(project => initiative.projectIds.includes(project.id))
  const completed = linked.filter(project => project.status.type === 'completed').length
  const needingUpdate = linked.filter(project => !['completed', 'canceled'].includes(project.status.type) && !(projectUpdates[project.id]?.length)).length
  const selectedLabels = labels.filter(label => initiative.labelIds.includes(label.id))
  return <ContextMenu.Root><ContextMenu.Trigger asChild><a className="li-row" data-selected={selected} href={href} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); onOpen(initiative) }} style={{ gridTemplateColumns: grid }}>
    <button aria-label="Select initiative" className="li-select" onClick={event => { event.preventDefault(); event.stopPropagation(); onSelect() }} style={{ gridColumn: 2 }} type="button"><span>{selected && <Check size={11}/>}</span></button>
    <div className="li-name" style={{ gridColumn: 3 }}><span className="li-row-icon" onClick={event => { event.preventDefault(); event.stopPropagation() }}><ViewIconPicker color={initiative.color} icon={initiative.icon || 'Initiative'} onChange={onUpdate} triggerClassName="li-initiative-icon"/></span><strong data-i18n-ignore>{initiative.name}</strong>{properties.has('description') && <small data-i18n-ignore>{initiative.summary}</small>}</div>
    {columns.map((property, index) => <div className={`li-cell li-cell--${property}`} key={property} onClick={event => { event.preventDefault(); event.stopPropagation() }} style={{ gridColumn: index + 4 }}>{renderCell(property)}</div>)}
    <button aria-label="Initiative actions" className="li-row-more" onClick={event => { event.preventDefault(); event.stopPropagation(); event.currentTarget.closest('.li-row')?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: event.clientX, clientY: event.clientY })) }} type="button"><MoreHorizontal size={14}/></button>
  </a></ContextMenu.Trigger><ContextMenu.Portal><InitiativeRowContextMenu initiative={initiative} labels={labels} teams={teams} users={users} onCreateReminder={onCreateReminder} onDelete={() => onDelete(initiative.id)} onOpen={tab => onOpen(initiative, tab)} onUpdate={onUpdate}/></ContextMenu.Portal></ContextMenu.Root>

  function renderCell(property: Property) {
    if (property === 'priority') return <InitiativeProperties compact only="priority" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'owner') return <InitiativeProperties compact only="owner" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'leadTeam') return <InitiativeProperties compact only="leadTeam" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'target') return <InitiativeProperties compact only="target" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'status') return <InitiativeProperties compact only="status" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'projects') return <button aria-label={`${completed} of ${linked.length} project completed. Click to view projects.`} className="li-project-count" onClick={() => onOpen(initiative, 'projects')} type="button"><span>{completed} /</span> {linked.length}</button>
    if (property === 'health') return <button aria-label={initiativeUpdates.length ? healthLabel(initiative.health) : 'There are no updates for this initiative'} className={`li-health is-${initiative.health}`} onClick={onOpenUpdates} type="button"><i/><span>{initiativeUpdates.length ? healthLabel(initiative.health) : 'No updates'}</span></button>
    if (property === 'activeProjects') return <button aria-label={`${needingUpdate} project${needingUpdate === 1 ? '' : 's'} need an update. Click to open updates.`} className="li-active-projects" onClick={onOpenUpdates} type="button"><i/>{needingUpdate}</button>
    if (property === 'labels') return <InitiativeLabelsPicker compact initiative={initiative} labels={labels} onCreateLabel={onCreateLabel} onUpdate={onUpdate}/>
    if (property === 'created' || property === 'updated') return <time>{new Date(property === 'created' ? initiative.createdAt : initiative.updatedAt).toLocaleDateString('en', { month: 'short', day: 'numeric' })}</time>
    if (property === 'completed') return <span>{initiative.status === 'completed' ? new Date(initiative.updatedAt).toLocaleDateString('en', { month: 'short', day: 'numeric' }) : '–'}</span>
    if (property === 'teams') { const names = teams.filter(team => initiative.contributingTeamIds.includes(team.id)).map(team => team.name); return <span className="li-muted" data-i18n-ignore>{names.join(', ') || '—'}</span> }
    if (property === 'description') return null
    return selectedLabels.length ? <span className="li-label-summary"><i style={{ background: selectedLabels[0].color }}/>{selectedLabels.length === 1 ? selectedLabels[0].name : `${selectedLabels.length} labels`}</span> : <span/>
  }
}

function InitiativeRowContextMenu({ initiative, users, teams, labels, onCreateReminder, onDelete, onOpen, onUpdate }: { initiative: Initiative; users: User[]; teams: Team[]; labels: IssueLabel[]; onCreateReminder: (remindAt: string) => Promise<unknown>; onDelete: () => void; onOpen: (tab?: InitiativeRouteTab) => void; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown> }) {
  const [query, setQuery] = useState('')
  const visible = (label: string) => !query || label.toLowerCase().includes(query.toLowerCase())
  const due = new Date(); due.setDate(due.getDate() + 1)
  return <ContextMenu.Content className="li-menu li-row-context" onCloseAutoFocus={() => setQuery('')}>
    <div className="li-menu-search"><input aria-label="Filter initiative actions" autoFocus placeholder="Filter…" value={query} onChange={event => setQuery(event.target.value)}/></div>
    {visible('Edit') && <ContextMenu.Item onSelect={() => onOpen()}><Edit3 size={14}/>Edit</ContextMenu.Item>}
    {visible('Status') && <ContextMenu.Sub><ContextMenu.SubTrigger><InitiativeStatusIcon status={initiative.status}/>Status<kbd>S</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu">{(['proposed', 'planned', 'active', 'completed', 'canceled'] as InitiativeStatus[]).map(status => <ContextMenu.Item key={status} onSelect={() => onUpdate({ status })}><InitiativeStatusIcon status={status}/>{titleCase(status)}{initiative.status === status && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Priority') && <ContextMenu.Sub><ContextMenu.SubTrigger><PriorityIcon priority={initiative.priority} size={14}/>Priority<ChevronRight className="li-menu-end" size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu">{[0, 1, 2, 3, 4].map(priority => <ContextMenu.Item key={priority} onSelect={() => onUpdate({ priority })}><PriorityIcon priority={priority} size={14}/>{['No priority', 'Urgent', 'High', 'Medium', 'Low'][priority]}{initiative.priority === priority && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Owner') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-owner-empty">+</span>Owner<kbd>N then O</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => onUpdate({ ownerId: '' })}><span className="li-owner-empty">+</span>No owner</ContextMenu.Item>{users.map(user => <ContextMenu.Item key={user.id} onSelect={() => onUpdate({ ownerId: user.id })}><Avatar name={user.displayName || user.name}/><span data-i18n-ignore>{user.displayName || user.name}</span>{initiative.owner?.id === user.id && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Lead team') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-owner-empty">+</span>Lead team<ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => onUpdate({ leadTeamId: '' })}>No lead team</ContextMenu.Item>{teams.map(team => <ContextMenu.Item key={team.id} onSelect={() => onUpdate({ leadTeamId: team.id })}><span className="li-team-mark" style={{ background: team.color }}>{team.key.slice(0, 2)}</span><span data-i18n-ignore>{team.name}</span>{initiative.leadTeamId === team.id && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Target date') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-target-glyph is-empty"/>Target date…<kbd>Ctrl ⌥ D</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => onUpdate({ targetDate: '' })}>No target date</ContextMenu.Item><ContextMenu.Item onSelect={() => onUpdate({ targetDate: isoDate(new Date()) })}>Today</ContextMenu.Item><ContextMenu.Item onSelect={() => onUpdate({ targetDate: isoDate(due) })}>Tomorrow</ContextMenu.Item></ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Labels') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-label-outline"/>Labels<kbd>N then L</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu">{labels.map(label => <ContextMenu.Item key={label.id} onSelect={() => onUpdate({ labelIds: initiative.labelIds.includes(label.id) ? initiative.labelIds.filter(id => id !== label.id) : [...initiative.labelIds, label.id] })}><i className="li-filter-color" style={{ background: label.color }}/><span data-i18n-ignore>{label.name}</span>{initiative.labelIds.includes(label.id) && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    <ContextMenu.Separator/>
    {visible('Copy') && <ContextMenu.Sub><ContextMenu.SubTrigger><Copy size={14}/>Copy<ChevronRight className="li-menu-end" size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => void navigator.clipboard.writeText(location.href)}>Copy page URL</ContextMenu.Item><ContextMenu.Item onSelect={() => void navigator.clipboard.writeText(initiative.name)}>Copy initiative title</ContextMenu.Item></ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    <ContextMenu.Separator/>
    {visible('Favorite') && <ContextMenu.Item onSelect={() => onUpdate({ favorite: !initiative.favorite })}><Star fill={initiative.favorite ? 'currentColor' : 'none'} size={14}/>{initiative.favorite ? 'Unfavorite' : 'Favorite'}<kbd>⌥ F</kbd></ContextMenu.Item>}
    {visible('Subscribe') && <ContextMenu.Item onSelect={() => onUpdate({ subscribed: !initiative.subscribed })}><Bell size={14}/>{initiative.subscribed ? 'Unsubscribe' : 'Subscribe'}<ChevronRight className="li-menu-end" size={12}/></ContextMenu.Item>}
    {visible('Remind me') && <ContextMenu.Sub><ContextMenu.SubTrigger><Clock3 size={14}/>Remind me<kbd>⇧ H</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => void onCreateReminder(new Date(Date.now() + 3600000).toISOString())}>In one hour</ContextMenu.Item><ContextMenu.Item onSelect={() => void onCreateReminder(new Date(Date.now() + 86400000).toISOString())}>Tomorrow</ContextMenu.Item><ContextMenu.Item onSelect={() => void onCreateReminder(new Date(Date.now() + 604800000).toISOString())}>Next week</ContextMenu.Item></ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    <ContextMenu.Separator/>
    {visible('New initiative update') && <ContextMenu.Item onSelect={() => onOpen('activity')}><Send size={14}/>New initiative update<kbd>N then U</kbd></ContextMenu.Item>}
    {visible('New comment') && <ContextMenu.Item onSelect={() => onOpen('activity')}><MessageSquare size={14}/>New comment…<kbd>N then C</kbd></ContextMenu.Item>}
    <ContextMenu.Separator/>
    {visible('Delete') && <ContextMenu.Item className="danger" onSelect={onDelete}><Trash2 size={14}/>Delete</ContextMenu.Item>}
  </ContextMenu.Content>
}

function InitiativeCreateRow({ labels, users, teams, viewer, view, onCancel, onCreate, onCreateLabel }: { labels: IssueLabel[]; users: User[]; teams: Team[]; viewer: User; view: InitiativesRouteView; onCancel: () => void; onCreate: (input: InitiativeMutationInput & { name: string }) => Promise<void>; onCreateLabel: (name: string) => Promise<IssueLabel> }) {
  const initialStatus: InitiativeStatus = view === 'planned' ? 'planned' : 'active'
  const [draft, setDraft] = useState<Initiative>({ id: 'draft', name: '', slugId: '', summary: '', description: '', icon: 'Initiative', color: '#d6a526', status: initialStatus, priority: 0, priorityLabel: 'No priority', health: 'noUpdate', creator: viewer, contributingTeamIds: [], labelIds: [], projectIds: [], resources: [], comments: [], favorite: false, subscribed: false, notificationRules: { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }, updateSchedule: { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' }, descriptionHistory: [], createdAt: '', updatedAt: '' })
  const [saving, setSaving] = useState(false)
  const update = (input: InitiativeMutationInput) => setDraft(current => ({ ...current, ...input, targetDateResolution: input.targetDateResolution === '' ? undefined : input.targetDateResolution ?? current.targetDateResolution, owner: input.ownerId !== undefined ? users.find(user => user.id === input.ownerId) : current.owner, priorityLabel: input.priority !== undefined ? ['No priority', 'Urgent', 'High', 'Medium', 'Low'][input.priority] : current.priorityLabel }))
  const submit = async () => {
    if (!draft.name.trim() || saving) return
    setSaving(true)
    try { await onCreate({ name: draft.name.trim(), summary: draft.summary, icon: draft.icon, color: draft.color, status: draft.status, priority: draft.priority, ownerId: draft.owner?.id, leadTeamId: draft.leadTeamId, targetDate: draft.targetDate, targetDateResolution: draft.targetDateResolution, labelIds: draft.labelIds }) } finally { setSaving(false) }
  }
  return <section className="li-create-row" onKeyDown={event => { if (event.key === 'Escape' && (event.target as HTMLElement).closest('.li-create-row') === event.currentTarget) onCancel(); if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void submit() }}>
    <ViewIconPicker color={draft.color} icon={draft.icon} onChange={update} triggerClassName="li-initiative-icon is-create"/>
    <div className="li-create-fields"><input autoFocus aria-label="Initiative name" placeholder="New initiative" value={draft.name} onChange={event => update({ name: event.target.value })}/><input aria-label="Initiative summary" placeholder="Add a short summary…" value={draft.summary} onChange={event => update({ summary: event.target.value })}/><div className="li-create-properties"><InitiativeProperties compact initiative={draft} only="status" teams={teams} users={users} onUpdate={update}/><InitiativeProperties compact initiative={draft} only="priority" teams={teams} users={users} onUpdate={update}/><InitiativeProperties compact initiative={draft} only="owner" teams={teams} users={users} onUpdate={update}/><InitiativeProperties compact initiative={draft} only="leadTeam" teams={teams} users={users} onUpdate={update}/><InitiativeProperties compact initiative={draft} only="target" teams={teams} users={users} onUpdate={update}/><InitiativeLabelsPicker compact initiative={draft} labels={labels} onCreateLabel={onCreateLabel} onUpdate={update}/></div></div>
    <footer><button onClick={onCancel} type="button">Cancel</button><button disabled={!draft.name.trim() || saving} onClick={() => void submit()} type="button">{saving ? 'Creating…' : 'Create'}</button></footer>
  </section>
}

function InitiativesEmpty({ filtered, onCreate, view }: { filtered: boolean; onCreate: () => void; view: InitiativesRouteView }) {
  if (filtered) return <div className="li-empty li-empty--filtered"><strong>No initiatives match these filters</strong><p>Try removing or changing a filter.</p></div>
  if (view === 'planned') return <div className="li-empty li-empty--planned"><span className="li-empty-initiatives" aria-hidden="true"><i/><i/><i/></span><strong>Upcoming initiatives</strong><p>Initiatives are larger, strategic product efforts that set the direction of your company. They are comprised of all projects that align with the goals of the initiative and allow you to monitor their progress at scale.</p><p>Once you create an upcoming initiative that hasn’t started yet, it will show up here.</p><div><button onClick={onCreate} type="button">Create new initiative <kbd>N</kbd><span>then</span><kbd>I</kbd></button><a href="https://flow.app/docs/initiatives" rel="noreferrer" target="_blank">Documentation</a></div></div>
  return <div className="li-empty li-empty--planned"><span className="li-empty-initiatives" aria-hidden="true"><i/><i/><i/></span><strong>{view === 'active' ? 'Active initiatives' : 'Create your first initiative'}</strong><p>{view === 'active' ? 'Initiatives in progress will appear here.' : 'Coordinate strategic work and monitor project progress at scale.'}</p><div><button onClick={onCreate} type="button">Create new initiative</button></div></div>
}

function ColumnHeader({ gridColumn, property, onSort }: { gridColumn: number; property: Property; onSort: (sort: Sort) => void }) {
  const label = ({ owner: 'Owner', status: 'Status', priority: 'Priority', leadTeam: 'Lead team', teams: 'Contributing teams', health: 'Health', projects: 'Projects', activeProjects: 'Active Projects', target: 'Target', created: 'Created', updated: 'Updated', completed: 'Completed', labels: 'Labels', description: 'Description' } as const)[property]
  const sortable: Partial<Record<Property, Sort>> = { status: 'status', priority: 'priority', target: 'target', health: 'health', created: 'created', updated: 'updated' }
  return sortable[property] ? <button aria-label={`Order by ${label}`} onClick={() => onSort(sortable[property]!)} style={{ gridColumn }} type="button">{label}<InitiativeSortIcon/></button> : <span style={{ gridColumn }}>{label}</span>
}

function InitiativeSortIcon() { return <svg aria-hidden="true" className="li-column-sort-icon" viewBox="0 0 16 16"><path d="M11.536 10.275c.266-.272.289-.707.04-1.005-.249-.299-.68-.355-.995-.142l-.062.046L8 11.274 5.48 9.174l-.061-.046c-.315-.213-.747-.157-.995.142-.249.298-.226.733.04 1.005l.056.051 3 2.5a.75.75 0 0 0 .96 0l3-2.5.056-.051Z"/><path d="M8.75 12.25a.75.75 0 0 1-1.5 0v-8.5a.75.75 0 0 1 1.5 0v8.5Z"/></svg> }

function InitiativeDisplayMenu({ dirty, grouping, properties, showTeamInitiatives, sort, onGrouping, onProperty, onReset, onSetDefault, onShowTeamInitiatives, onSort }: { dirty: boolean; grouping: Grouping; properties: Set<Property>; showTeamInitiatives: boolean; sort: Sort; onGrouping: (grouping: Grouping) => void; onProperty: (property: Property) => void; onReset: () => void; onSetDefault: () => Promise<void>; onShowTeamInitiatives: (show: boolean) => void; onSort: (sort: Sort) => void }) {
  const [saving, setSaving] = useState(false)
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Display options" className="li-icon-button ui-pill" type="button"><DisplayIcon/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className={`li-menu li-display-menu${dirty ? ' has-footer' : ''}`} sideOffset={4}>
    <div className="li-display-row"><span>Grouping</span><DropdownMenu.Sub><DropdownMenu.SubTrigger>{groupingLabel(grouping)} <ChevronRight size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{(['none','contributingTeam','leadTeam','owner','health','status','priority','label'] as Grouping[]).map(item => <DropdownMenu.Item key={item} onSelect={() => onGrouping(item)}>{groupingLabel(item)}{grouping === item && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub></div>
    <div className="li-display-row"><span>Ordering</span><DropdownMenu.Sub><DropdownMenu.SubTrigger>{titleCase(sort)} <ChevronRight size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{(['manual', 'name', 'priority', 'target', 'health', 'created', 'updated'] as Sort[]).map(item => <DropdownMenu.Item key={item} onSelect={() => onSort(item)}>{titleCase(item)}{sort === item && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub></div>
    <DropdownMenu.Separator/><DropdownMenu.CheckboxItem checked={showTeamInitiatives} className="li-display-team-toggle" onCheckedChange={value => onShowTeamInitiatives(value === true)}><span>Show team initiatives</span><span aria-hidden="true" className="li-display-switch" data-checked={showTeamInitiatives}><i/></span></DropdownMenu.CheckboxItem><DropdownMenu.Separator/><DropdownMenu.Label className="li-display-properties-label">Display properties</DropdownMenu.Label><div className="li-display-properties">{PROPERTY_ORDER.map(property => <button aria-pressed={properties.has(property)} className={properties.has(property) ? 'is-active' : ''} key={property} onClick={event => { event.preventDefault(); onProperty(property) }} type="button">{columnLabel(property)}</button>)}</div>{dirty && <footer className="li-display-footer"><button onClick={event => { event.preventDefault(); onReset() }} type="button">Reset</button><button disabled={saving} onClick={event => { event.preventDefault(); setSaving(true); void onSetDefault().finally(() => setSaving(false)) }} type="button">Set default for everyone</button></footer>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function InitiativeFilterMenu({ filters, initiatives, users, teams, labels, onChange, onAdvanced }: { filters: FilterState; initiatives: Initiative[]; users: User[]; teams: Team[]; labels: IssueLabel[]; onChange: (filters: FilterState) => void; onAdvanced: () => void }) {
  const [query, setQuery] = useState('')
  const entries = [
    { id: 'status', label: 'Status' }, { id: 'priority', label: 'Priority' }, { id: 'ownerId', label: 'Owner' }, { id: 'creatorId', label: 'Creator' },
    { id: 'leadTeamId', label: 'Lead team' }, { id: 'teamId', label: 'Contributing teams' }, { id: 'labelId', label: 'Labels' }, { id: 'health', label: 'Health' }, { id: 'date', label: 'Dates' },
  ].filter(item => item.label.toLowerCase().includes(query.toLowerCase()))
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}><DropdownMenu.Trigger asChild><button aria-label="Add filter" className="li-icon-button ui-pill" type="button"><Filter size={14}/>{Object.keys(filters).length > 0 && <i/>}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu li-filter-menu" sideOffset={4}>
    <div className="li-menu-search"><Search size={13}/><input aria-label="Add Filter…" autoFocus placeholder="Add Filter…" value={query} onChange={event => setQuery(event.target.value)}/><kbd>F</kbd></div>
    {!query && <><DropdownMenu.Item disabled title="Requires the Flow AI integration"><span className="li-ai-filter">✦</span>AI filter</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item onSelect={onAdvanced}>Advanced filter</DropdownMenu.Item><DropdownMenu.Separator/></>}
    {entries.map(entry => <DropdownMenu.Sub key={entry.id}><DropdownMenu.SubTrigger>{entry.label}<ChevronRight className="li-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className={`li-menu li-filter-values is-${entry.id}`} sideOffset={5}><InitiativeFilterValues entry={entry} filters={filters} initiatives={initiatives} labels={labels} onChange={onChange} teams={teams} users={users}/></DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function InitiativeFilterValues({ entry, filters, initiatives, labels, onChange, teams, users }: { entry: { id: string; label: string }; filters: FilterState; initiatives: Initiative[]; labels: IssueLabel[]; onChange: (filters: FilterState) => void; teams: Team[]; users: User[] }) {
  const [query, setQuery] = useState('')
  const options = filterOptions(entry.id, initiatives, users, teams, labels).filter(option => option.label.toLowerCase().includes(query.toLowerCase()))
  return <><div className="li-menu-search"><input aria-label="Filter…" autoFocus onChange={event => setQuery(event.target.value)} placeholder="Filter…" value={query}/></div>{options.map(option => { const checked = filters[entry.id as keyof FilterState] === option.value; const count = initiatives.filter(item => matchesInitiativeFilter(item, entry.id as keyof FilterState, option.value as NonNullable<FilterState[keyof FilterState]>)).length; return <DropdownMenu.Item key={String(option.value)} onSelect={() => onChange({ ...filters, [entry.id]: option.value })}><span className="li-filter-checkbox">{checked && <Check size={10}/>}</span>{option.icon}<span data-i18n-ignore={['ownerId','creatorId','leadTeamId','teamId','labelId'].includes(entry.id) ? true : undefined}>{option.label}</span>{count > 0 && <small className="li-menu-end">{count} initiative{count === 1 ? '' : 's'}</small>}</DropdownMenu.Item> })}</>
}

const ADVANCED_FILTER_FIELDS = [
  { id: 'status', label: 'Status' }, { id: 'priority', label: 'Priority' }, { id: 'ownerId', label: 'Owner' }, { id: 'creatorId', label: 'Creator' },
  { id: 'leadTeamId', label: 'Lead team' }, { id: 'teamId', label: 'Contributing teams' }, { id: 'labelId', label: 'Labels' }, { id: 'health', label: 'Health' }, { id: 'date', label: 'Dates' },
] as const

type AdvancedFilterProps = {
  filters: FilterState
  initiatives: Initiative[]
  users: User[]
  teams: Team[]
  labels: IssueLabel[]
  mode: 'all'|'any'
  onChange: (value: FilterState) => void
  onMode: (value: 'all'|'any') => void
}

function AdvancedFilterBar(props: AdvancedFilterProps & { open: boolean; onOpenChange: (open: boolean) => void; onRemove: () => void }) {
  const { filters, open, onChange, onOpenChange, onRemove } = props
  const { t } = useI18n()
  return <div className="li-filter-chips li-advanced-filter-bar">
    <Popover.Root onOpenChange={onOpenChange} open={open}>
      <div className="li-advanced-filter-chip"><Popover.Trigger asChild><button aria-label={t('Open advanced filter')} type="button">Advanced filter</button></Popover.Trigger><button aria-label={t('Remove advanced filter')} onClick={onRemove} type="button"><X size={12}/></button></div>
      <Popover.Portal><Popover.Content align="start" className="li-advanced-filter-popover" collisionPadding={8} side="bottom" sideOffset={4} onOpenAutoFocus={event => event.preventDefault()}>
        <AdvancedFilterBuilder {...props}/>
      </Popover.Content></Popover.Portal>
    </Popover.Root>
    <button aria-label={t('Add another filter')} className="li-advanced-filter-add" onClick={() => onOpenChange(true)} type="button"><Plus size={13}/></button>
    <button className="li-advanced-filter-clear" disabled={!Object.keys(filters).length} onClick={() => onChange({})} type="button">Clear</button>
  </div>
}

function AdvancedFilterBuilder(props: AdvancedFilterProps) {
  const { filters, mode, onChange, onMode } = props
  const { t } = useI18n()
  const entries = Object.entries(filters) as [keyof FilterState, NonNullable<FilterState[keyof FilterState]>][]
  const remove = (field: keyof FilterState) => { const updated = { ...filters }; delete updated[field]; onChange(updated) }
  const replaceField = (previous: keyof FilterState, next: keyof FilterState) => {
    const option = filterOptions(next, props.initiatives, props.users, props.teams, props.labels)[0]
    if (!option) return
    const updated = { ...filters }; delete updated[previous]; Object.assign(updated, { [next]: option.value }); onChange(updated)
  }
  return <>
    <div className="li-advanced-filter-group">
      <div className="li-advanced-filter-group-content">{entries.length ? entries.map(([field, value]) => <AdvancedFilterRule field={field} key={field} value={value} {...props} onRemove={() => remove(field)} onReplaceField={next => replaceField(field, next)}/>) : <AdvancedFilterPicker {...props}/>}</div>
      <button aria-label={t('Delete group')} onClick={() => onChange({})} type="button"><X size={13}/></button>
    </div>
    <div className="li-advanced-filter-footer"><button aria-label={t(`Toggle filter operator, currently ${mode === 'all' ? 'and' : 'or'}`)} onClick={() => onMode(mode === 'all' ? 'any' : 'all')} type="button">{mode === 'all' ? 'and' : 'or'}</button><AdvancedFilterPicker {...props}/></div>
  </>
}

function AdvancedFilterRule({ field, value, onRemove, onReplaceField, ...props }: AdvancedFilterProps & { field: keyof FilterState; value: NonNullable<FilterState[keyof FilterState]>; onRemove: () => void; onReplaceField: (field: keyof FilterState) => void }) {
  const fieldLabel = ADVANCED_FILTER_FIELDS.find(item => item.id === field)?.label ?? titleCase(field)
  const { t } = useI18n()
  const options = filterOptions(field, props.initiatives, props.users, props.teams, props.labels)
  const valueOption = options.find(option => String(option.value) === String(value))
  return <div className="li-advanced-filter-rule">
    <DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button">{fieldLabel}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu li-advanced-filter-menu" sideOffset={4}>{ADVANCED_FILTER_FIELDS.map(item => <DropdownMenu.Item disabled={item.id !== field && props.filters[item.id] !== undefined} key={item.id} onSelect={() => onReplaceField(item.id)}>{item.label}{item.id === field && <Check className="li-menu-end" size={12}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    <span>is</span>
    <DropdownMenu.Root><DropdownMenu.Trigger asChild><button data-i18n-ignore={['ownerId','creatorId','leadTeamId','teamId','labelId'].includes(field) ? '' : undefined} type="button">{valueOption?.label ?? String(value)}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu li-advanced-filter-menu" sideOffset={4}>{options.map(option => <DropdownMenu.Item data-i18n-ignore={['ownerId','creatorId','leadTeamId','teamId','labelId'].includes(field) ? '' : undefined} key={String(option.value)} onSelect={() => props.onChange({ ...props.filters, [field]: option.value })}>{option.icon}{option.label}{String(option.value) === String(value) && <Check className="li-menu-end" size={12}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    <button aria-label={`${t('Remove filter')}: ${t(fieldLabel)}`} onClick={onRemove} type="button"><X size={12}/></button>
  </div>
}

function AdvancedFilterPicker(props: AdvancedFilterProps) {
  const { t } = useI18n()
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Filter')} className="li-advanced-filter-picker" disabled={Object.keys(props.filters).length >= ADVANCED_FILTER_FIELDS.length} type="button"><Plus size={12}/>Filter</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu li-advanced-filter-menu" sideOffset={4}>{ADVANCED_FILTER_FIELDS.map(field => <DropdownMenu.Sub key={field.id}><DropdownMenu.SubTrigger disabled={props.filters[field.id] !== undefined}>{field.label}<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu li-filter-values" sideOffset={4}>{filterOptions(field.id, props.initiatives, props.users, props.teams, props.labels).map(option => <DropdownMenu.Item data-i18n-ignore={['ownerId','creatorId','leadTeamId','teamId','labelId'].includes(field.id) ? '' : undefined} key={String(option.value)} onSelect={() => props.onChange({ ...props.filters, [field.id]: option.value })}>{option.icon}{option.label}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function InitiativeUpdatesPanel({ initiative, updates, viewer, onClose, onCreateUpdate, onOpen, onUpdate }: { initiative: Initiative; updates: InitiativeUpdate[]; viewer: User; onClose: () => void; onCreateUpdate: Props['onCreateUpdate']; onOpen: () => void; onUpdate: (input: InitiativeMutationInput) => Promise<Initiative> }) {
  const [composing, setComposing] = useState(false)
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>(initiative.health === 'noUpdate' ? 'onTrack' : initiative.health)
  const [saving, setSaving] = useState(false)
  const submit = async () => { if (!body.trim() || saving) return; setSaving(true); try { await onCreateUpdate(initiative.id, { body: body.trim(), health }); setBody(''); setComposing(false) } finally { setSaving(false) } }
  return <div className="li-update-panel-backdrop" onMouseDown={event => { if (event.currentTarget === event.target) onClose() }}><aside className="li-update-panel" aria-label="Initiative updates">
    <header><button data-i18n-ignore onClick={onOpen} type="button"><ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/>{initiative.name}</button><button aria-pressed={initiative.subscribed} onClick={() => void onUpdate({ subscribed: !initiative.subscribed })} type="button">{initiative.subscribed ? 'Subscribed' : 'Subscribe'}</button><button onClick={() => setComposing(true)} type="button">New update</button><button aria-label="Close initiative updates" onClick={onClose} type="button"><X size={15}/></button></header>
    {composing && <div className="li-update-panel-composer"><div><Avatar name={viewer.displayName || viewer.name}/><strong>{viewer.displayName || viewer.name}</strong><SelectControl label="Update health" value={health} onChange={value => setHealth(value as Project['health'])} options={[{value:'onTrack',label:'On track'},{value:'atRisk',label:'At risk'},{value:'offTrack',label:'Off track'}]}/></div><textarea autoFocus aria-label="Initiative update" placeholder="Write an initiative update…" value={body} onChange={event => setBody(event.target.value)}/><footer><button onClick={() => setComposing(false)} type="button">Cancel</button><button disabled={!body.trim() || saving} onClick={() => void submit()} type="button">Post update</button></footer></div>}
    {!updates.length && !composing ? <div className="li-update-panel-empty"><span><Send size={22}/></span><strong>Initiative updates</strong><p>Write a short status report to keep everyone up-to-date on the progress and health of this initiative</p><button onClick={() => setComposing(true)} type="button">New initiative update <kbd>N</kbd><span>then</span><kbd>U</kbd></button><a href="https://flow.app/docs/initiative-and-project-updates" rel="noreferrer" target="_blank">Documentation</a></div> : <div className="li-update-panel-list">{updates.map(update => <article key={update.id}><header><Avatar name={update.user.displayName}/><strong data-i18n-ignore>{update.user.displayName}</strong><time>{new Date(update.createdAt).toLocaleDateString('en', { month: 'short', day: 'numeric' })}</time><span className={`li-update-health is-${update.health}`}/>{healthLabel(update.health)}</header><p data-i18n-ignore>{update.body}</p></article>)}</div>}
  </aside></div>
}

function filterOptions(id: string, initiatives: Initiative[], users: User[], teams: Team[], labels: IssueLabel[]) {
  if (id === 'status') return (['proposed', 'planned', 'active', 'completed', 'canceled'] as InitiativeStatus[]).map(value => ({ value, label: titleCase(value), icon: <InitiativeStatusIcon status={value}/> }))
  if (id === 'priority') return [0, 1, 2, 3, 4].map(value => ({ value, label: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][value], icon: <PriorityIcon priority={value} size={14}/> }))
  if (id === 'ownerId') return [{ value: '', label: 'No owner', icon: <span className="li-owner-empty">+</span> }, ...users.map(user => ({ value: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))]
  if (id === 'creatorId') return users.map(user => ({ value: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))
  if (id === 'labelId') return labels.map(label => ({ value: label.id, label: label.name, icon: <i className="li-filter-color" style={{ background: label.color }}/> }))
  if (id === 'health') return (['onTrack', 'atRisk', 'offTrack', 'noUpdate'] as Project['health'][]).map(value => ({ value, label: healthLabel(value), icon: <span className={`li-update-health is-${value}`}/> }))
  if (id === 'leadTeamId' || id === 'teamId') return teams.map(team => ({ value: team.id, label: team.name, icon: <span className="li-team-mark" style={{ background: team.color }}>{team.key.slice(0, 2)}</span> }))
  if (id === 'date') return [{ value: 'created7', label: 'Created in last 7 days', icon: <Clock3 size={13}/> }, { value: 'updated7', label: 'Updated in last 7 days', icon: <Clock3 size={13}/> }, { value: 'targetMonth', label: 'Targeted this month', icon: <Clock3 size={13}/> }, { value: 'completed', label: 'Completed', icon: <Check size={13}/> }]
  return initiatives.map(item => ({ value: item.id, label: item.name, icon: <span/> }))
}

function columnLabel(property: Property) { return ({ description: 'Description', owner: 'Owner', status: 'Status', priority: 'Priority', leadTeam: 'Lead team', teams: 'Contributing teams', health: 'Health', projects: 'Projects', activeProjects: 'Active projects', target: 'Target date', created: 'Created', updated: 'Updated', completed: 'Completed', labels: 'Labels' } as const)[property] }
function filterLabel(key: string, value: unknown, users: User[], teams: Team[], labels: IssueLabel[]) { if (key === 'ownerId' || key === 'creatorId') return `${key === 'ownerId' ? 'Owner' : 'Creator'}: ${users.find(user => user.id === value)?.displayName ?? 'No owner'}`; if (key === 'leadTeamId' || key === 'teamId') return `${key === 'leadTeamId' ? 'Lead team' : 'Contributing teams'}: ${teams.find(team => team.id === value)?.name ?? value}`; if (key === 'labelId') return `Labels: ${labels.find(label => label.id === value)?.name ?? value}`; if (key === 'priority') return `Priority: ${['No priority', 'Urgent', 'High', 'Medium', 'Low'][Number(value)]}`; return `${titleCase(key)}: ${titleCase(String(value))}` }
function groupingLabel(grouping: Grouping) { return ({ none: 'No grouping', contributingTeam: 'Contributing team', leadTeam: 'Lead team', owner: 'Owner', health: 'Health', status: 'Status', priority: 'Priority', label: 'Label' })[grouping] }
function columnWidth(property: Property, detailsOpen = false) { return ({ description: '0px', owner: detailsOpen ? '40px' : '50px', status: '100px', priority: '56px', leadTeam: '100px', teams: '110px', health: '52px', projects: '59px', activeProjects: '98px', target: '100px', created: '78px', updated: '78px', completed: '82px', labels: '100px' })[property] }
function groupInitiatives(initiatives: Initiative[], grouping: Grouping, teams: Team[], labels: IssueLabel[]) {
  if (grouping === 'none') return [{ key: 'all', label: '', items: initiatives }]
  const buckets = new Map<string, { key: string; label: string; items: Initiative[] }>()
  const add = (key: string, label: string, initiative: Initiative) => { const bucket = buckets.get(key) ?? { key, label, items: [] }; bucket.items.push(initiative); buckets.set(key, bucket) }
  for (const initiative of initiatives) {
    if (grouping === 'owner') add(initiative.owner?.id ?? 'none', initiative.owner?.displayName || 'No owner', initiative)
    else if (grouping === 'leadTeam') { const team = teams.find(item => item.id === initiative.leadTeamId); add(team?.id ?? 'none', team?.name ?? 'No lead team', initiative) }
    else if (grouping === 'contributingTeam') { const selected = teams.filter(team => initiative.contributingTeamIds.includes(team.id)); if (selected.length) selected.forEach(team => add(team.id, team.name, initiative)); else add('none', 'No contributing team', initiative) }
    else if (grouping === 'health') add(initiative.health, healthLabel(initiative.health), initiative)
    else if (grouping === 'status') add(initiative.status, titleCase(initiative.status), initiative)
    else if (grouping === 'priority') add(String(initiative.priority), initiative.priorityLabel, initiative)
    else { const selected = labels.filter(label => initiative.labelIds.includes(label.id)); if (selected.length) selected.forEach(label => add(label.id, label.name, initiative)); else add('none', 'No labels', initiative) }
  }
  return [...buckets.values()]
}
function matchesDateFilter(initiative: Initiative, filter: NonNullable<FilterState['date']>) { const weekAgo = Date.now() - 7 * 86400000; if (filter === 'created7') return new Date(initiative.createdAt).getTime() >= weekAgo; if (filter === 'updated7') return new Date(initiative.updatedAt).getTime() >= weekAgo; if (filter === 'completed') return initiative.status === 'completed'; if (!initiative.targetDate) return false; const target = new Date(`${initiative.targetDate}T00:00:00`); const now = new Date(); return target.getFullYear() === now.getFullYear() && target.getMonth() === now.getMonth() }
function matchesInitiativeFilter(initiative: Initiative, key: keyof FilterState, value: NonNullable<FilterState[keyof FilterState]>) { if (key === 'status') return initiative.status === value; if (key === 'priority') return initiative.priority === value; if (key === 'ownerId') return (initiative.owner?.id ?? '') === value; if (key === 'creatorId') return initiative.creator.id === value; if (key === 'leadTeamId') return (initiative.leadTeamId ?? '') === value; if (key === 'teamId') return initiative.contributingTeamIds.includes(String(value)); if (key === 'health') return initiative.health === value; if (key === 'labelId') return initiative.labelIds.includes(String(value)); return matchesDateFilter(initiative, value as NonNullable<FilterState['date']>) }
function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No updates' } as const)[value] }
function isoDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function readInitiativeProperties() { if (localStorage.getItem('flow:initiatives:properties-version') !== '2') return new Set(DEFAULT_PROPERTIES); try { const stored = JSON.parse(localStorage.getItem('flow:initiatives:properties') ?? 'null'); if (Array.isArray(stored)) return new Set<Property>(stored.filter((item): item is Property => PROPERTY_ORDER.includes(item))) } catch { /* use defaults */ } return new Set(DEFAULT_PROPERTIES) }
function readInitiativeSetting<T extends string>(key: string, values: readonly T[], fallback: T) { const stored = localStorage.getItem(`flow:initiatives:${key}`) as T | null; return stored && values.includes(stored) ? stored : fallback }
function sameStringSet<T extends string>(left: Set<T>, right: Set<T>) { return left.size === right.size && [...left].every(value => right.has(value)) }
