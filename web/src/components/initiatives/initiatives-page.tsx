import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Bell, Check, ChevronRight, Clock3, Copy, Edit3, Filter, MessageSquare, MoreHorizontal, MousePointer2, PanelRightClose, PanelRightOpen, Plus, Search, Send, SlidersHorizontal, Star, Trash2, X } from 'lucide-react'
import { Fragment, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { PriorityIcon } from '@/components/issue/issue-icons'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import type { Initiative, InitiativeMutationInput, InitiativeStatus, InitiativeUpdate, IssueLabel, Project, ProjectUpdate, Team, User } from '@/types/flow'
import type { InitiativeRouteTab, InitiativesRouteView } from '@/lib/app-routes'
import { InitiativeLabelsPicker, InitiativeProperties, InitiativeStatusIcon } from './initiative-shared'
import { titleCase } from './initiative-model'
import './initiatives.css'
import './initiatives-audit.css'

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
  onCreateUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdate: (id: string, input: InitiativeMutationInput) => Promise<Initiative>
  onDelete: (id: string) => Promise<void>
  onCreateReminder: (id: string, remindAt: string) => Promise<unknown>
  onOpenSidebar?: () => void
  createOnMount?: boolean
}

const PROPERTY_ORDER = ['description', 'owner', 'status', 'priority', 'leadTeam', 'teams', 'health', 'projects', 'activeProjects', 'target', 'created', 'updated', 'completed', 'labels'] as const
type Property = typeof PROPERTY_ORDER[number]
const TABLE_PROPERTY_ORDER: Property[] = ['status', 'priority', 'owner', 'leadTeam', 'target', 'projects', 'health', 'activeProjects', 'labels', 'teams', 'created', 'updated', 'completed']
const DEFAULT_PROPERTIES: Property[] = ['owner', 'priority', 'leadTeam', 'health', 'projects', 'activeProjects', 'target']
type Sort = 'manual' | 'name' | 'priority' | 'target' | 'health' | 'created' | 'updated'
type Grouping = 'none'|'contributingTeam'|'leadTeam'|'owner'|'health'|'status'|'priority'|'label'
type FilterState = { status?: InitiativeStatus; priority?: number; ownerId?: string; creatorId?: string; leadTeamId?: string; teamId?: string; health?: Project['health']; labelId?: string; date?: 'created7'|'updated7'|'targetMonth'|'completed' }

export function InitiativesPage(props: Props) {
  const { initiatives, initiativeUpdates, projects, projectUpdates, users, teams, labels, viewer, view, onViewChange, onOpen, onCreate, onCreateUpdate, onUpdate, onDelete, onCreateReminder, onOpenSidebar, createOnMount = false } = props
  const [creating, setCreating] = useState(false)
  const [filters, setFilters] = useState<FilterState>({})
  const [properties, setProperties] = useState<Set<Property>>(() => new Set(DEFAULT_PROPERTIES))
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [sort, setSort] = useState<Sort>('manual')
  const [grouping, setGrouping] = useState<Grouping>('none')
  const [showTeamInitiatives, setShowTeamInitiatives] = useState(true)
  const [updatesInitiative, setUpdatesInitiative] = useState<Initiative>()
  const [detailsOpen, setDetailsOpen] = useState(() => window.innerWidth > 800 && localStorage.getItem('flow:initiatives:details-open') !== 'false')

  const visible = useMemo(() => initiatives.filter(item => {
    const inView = view === 'active' ? item.status === 'active' : view === 'planned' ? item.status === 'planned' || item.status === 'proposed' : true
    return inView
      && (showTeamInitiatives || !(item.leadTeamId && item.contributingTeamIds.includes(item.leadTeamId)))
      && (filters.status === undefined || item.status === filters.status)
      && (filters.priority === undefined || item.priority === filters.priority)
      && (filters.ownerId === undefined || (item.owner?.id ?? '') === filters.ownerId)
      && (filters.creatorId === undefined || item.creator.id === filters.creatorId)
      && (filters.leadTeamId === undefined || (item.leadTeamId ?? '') === filters.leadTeamId)
      && (filters.teamId === undefined || item.contributingTeamIds.includes(filters.teamId))
      && (filters.health === undefined || item.health === filters.health)
      && (filters.labelId === undefined || item.labelIds.includes(filters.labelId))
      && (filters.date === undefined || matchesDateFilter(item, filters.date))
  }).sort((a, b) => {
    if (sort === 'name') return a.name.localeCompare(b.name)
    if (sort === 'priority') return a.priority - b.priority
    if (sort === 'target') return (a.targetDate ?? '').localeCompare(b.targetDate ?? '')
    if (sort === 'health') return a.health.localeCompare(b.health)
    if (sort === 'created') return a.createdAt.localeCompare(b.createdAt)
    if (sort === 'updated') return a.updatedAt.localeCompare(b.updatedAt)
    return 0
  }), [filters, initiatives, showTeamInitiatives, sort, view])

  const columns = TABLE_PROPERTY_ORDER.filter(property => properties.has(property))
  const columnGrid = `300px ${columns.map(columnWidth).join(' ')}`
  const grouped = groupInitiatives(visible, grouping, teams, labels)
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

  const toggleDetails = () => setDetailsOpen(open => {
    localStorage.setItem('flow:initiatives:details-open', String(!open))
    return !open
  })

  return <main className="main-panel li-page">
    <header className="li-page-header">
      <button className="li-mobile-menu" onClick={onOpenSidebar} type="button">☰</button>
      <h1>Initiatives</h1>
      <button aria-label="New initiative" className="li-icon-button" onClick={() => setCreating(true)} type="button"><Plus size={15}/></button>
    </header>
    <div className="li-toolbar">
      <nav>{(['active', 'planned', 'all'] as InitiativesRouteView[]).map(item => <button aria-current={view === item ? 'page' : undefined} key={item} onClick={() => onViewChange(item)} type="button">{item === 'all' ? 'All initiatives' : titleCase(item)}</button>)}</nav>
      <div className="li-toolbar-actions">
        <FilterMenu filters={filters} initiatives={initiatives} labels={labels} onChange={setFilters} teams={teams} users={users}/>
        <DisplayMenu grouping={grouping} properties={properties} showTeamInitiatives={showTeamInitiatives} sort={sort} onGrouping={setGrouping} onProperty={toggleProperty} onShowTeamInitiatives={setShowTeamInitiatives} onSort={setSort}/>
        <button aria-expanded={detailsOpen} aria-label={detailsOpen ? 'Close sidebar' : 'Open sidebar'} className="li-icon-button" onClick={toggleDetails} type="button">{detailsOpen ? <PanelRightClose size={14}/> : <PanelRightOpen size={14}/>}</button>
      </div>
    </div>
    {Object.keys(filters).length > 0 && <div className="li-filter-chips">{Object.entries(filters).map(([key, value]) => <button key={key} onClick={() => setFilters(current => { const next = { ...current }; delete next[key as keyof FilterState]; return next })} type="button"><span>{filterLabel(key, value, users, teams, labels)}</span><X size={11}/></button>)}<button onClick={() => setFilters({})} type="button">Clear all</button></div>}
    <div className="li-list-body">
      <div className="li-table" style={{ '--li-extra-columns': columns.length } as React.CSSProperties}>
        {visible.length > 0 && <div className="li-columns" style={{ gridTemplateColumns: columnGrid }}><button onClick={() => setSort('name')} type="button">Name</button>{columns.map(property => <ColumnHeader key={property} property={property} onSort={setSort}/>)}</div>}
        {creating && <InitiativeCreateRow labels={labels} teams={teams} users={users} viewer={viewer} view={view} onCancel={() => setCreating(false)} onCreate={async input => { await onCreate(input); setCreating(false) }}/>}
        {grouped.map(group => <Fragment key={group.key}>{grouping !== 'none' && <div className="li-group-heading"><span>{group.label}</span><small>{group.items.length}</small></div>}{group.items.map(initiative => <InitiativeRow columns={columns} initiative={initiative} initiativeUpdates={initiativeUpdates[initiative.id] ?? []} key={initiative.id} labels={labels} projects={projects} projectUpdates={projectUpdates} properties={properties} selected={selected.has(initiative.id)} teams={teams} users={users} onCreateReminder={remindAt => onCreateReminder(initiative.id, remindAt)} onDelete={onDelete} onOpen={onOpen} onOpenUpdates={() => setUpdatesInitiative(initiative)} onSelect={() => toggleSelected(initiative.id)} onUpdate={input => onUpdate(initiative.id, input)}/>)}</Fragment>)}
        {!creating && !visible.length && <InitiativesEmpty filtered={Object.keys(filters).length > 0} onCreate={() => setCreating(true)} view={view}/>} 
      </div>
      {detailsOpen && <InitiativesListSidebar initiatives={visible} teams={teams} users={users} view={view}/>}
    </div>
    {selected.size > 0 && <InitiativesBulkBar initiatives={initiatives.filter(item => selected.has(item.id))} labels={labels} users={users} onClear={() => setSelected(new Set())} onDelete={async () => { await Promise.all([...selected].map(onDelete)); setSelected(new Set()) }} onUpdate={input => Promise.all([...selected].map(id => onUpdate(id, input)))}/>} 
    {updatesInitiative && <InitiativeUpdatesPanel initiative={updatesInitiative} updates={initiativeUpdates[updatesInitiative.id] ?? []} viewer={viewer} onClose={() => setUpdatesInitiative(undefined)} onCreateUpdate={onCreateUpdate} onOpen={() => onOpen(updatesInitiative, 'activity')}/>} 
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
    <DropdownMenu.Sub><DropdownMenu.SubTrigger>Owner<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu"><DropdownMenu.Item onSelect={() => void onUpdate({ ownerId: '' })}>No owner</DropdownMenu.Item>{users.map(user => <DropdownMenu.Item key={user.id} onSelect={() => void onUpdate({ ownerId: user.id })}><Avatar name={user.displayName || user.name}/>{user.displayName || user.name}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Sub><DropdownMenu.SubTrigger>Labels<ChevronRight className="li-menu-end" size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{labels.map(label => <DropdownMenu.Item key={label.id} onSelect={() => void onUpdate({ labelIds: allHaveLabel(label.id) ? initiatives.flatMap(item => item.labelIds).filter((id, index, ids) => id !== label.id && ids.indexOf(id) === index) : [...new Set(initiatives.flatMap(item => item.labelIds).concat(label.id))] })}><i className="li-filter-color" style={{ background: label.color }}/>{label.name}{allHaveLabel(label.id) && <Check className="li-menu-end" size={12}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
    <DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={() => void onDelete()}><Trash2 size={14}/>Delete</DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root><button aria-label="Ask Flow" onClick={() => toast.info('Ask Flow requires the Flow AI integration.')} type="button"><MousePointer2 size={14}/></button><button aria-label="Clear selected" onClick={onClear} type="button"><X size={14}/></button></div>
}

function InitiativeRow({ initiative, initiativeUpdates, projects, projectUpdates, properties, columns, selected, users, teams, labels, onCreateReminder, onDelete, onOpen, onOpenUpdates, onSelect, onUpdate }: {
  initiative: Initiative; initiativeUpdates: InitiativeUpdate[]; projects: Project[]; projectUpdates: Record<string, ProjectUpdate[]>; properties: Set<Property>; columns: Property[]; selected: boolean; users: User[]; teams: Team[]; labels: IssueLabel[]
  onCreateReminder: (remindAt: string) => Promise<unknown>; onDelete: (id: string) => Promise<void>; onOpen: (initiative: Initiative, tab?: InitiativeRouteTab) => void; onOpenUpdates: () => void; onSelect: () => void; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const linked = projects.filter(project => initiative.projectIds.includes(project.id))
  const completed = linked.filter(project => project.status.type === 'completed').length
  const needingUpdate = linked.filter(project => !['completed', 'canceled'].includes(project.status.type) && !(projectUpdates[project.id]?.length)).length
  const selectedLabels = labels.filter(label => initiative.labelIds.includes(label.id))
  return <ContextMenu.Root><ContextMenu.Trigger asChild><div className="li-row" data-selected={selected} onClick={() => onOpen(initiative)} style={{ gridTemplateColumns: `28px 272px ${columns.map(columnWidth).join(' ')}` }}>
    <button aria-label="Select initiative" className="li-select" onClick={event => { event.stopPropagation(); onSelect() }} type="button"><span>{selected && <Check size={11}/>}</span></button>
    <div className="li-row-icon" onClick={event => event.stopPropagation()}><ViewIconPicker color={initiative.color} icon={initiative.icon || 'Initiative'} onChange={onUpdate}/></div>
    <div className="li-name"><strong data-i18n-ignore>{initiative.name}</strong>{properties.has('description') && <small data-i18n-ignore>{initiative.summary}</small>}</div>
    {columns.map(property => <div className={`li-cell li-cell--${property}`} key={property} onClick={event => event.stopPropagation()}>{renderCell(property)}</div>)}
    <button aria-label="Initiative actions" className="li-row-more" onClick={event => { event.stopPropagation(); event.currentTarget.closest('.li-row')?.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: event.clientX, clientY: event.clientY })) }} type="button"><MoreHorizontal size={14}/></button>
  </div></ContextMenu.Trigger><ContextMenu.Portal><InitiativeRowContextMenu initiative={initiative} labels={labels} teams={teams} users={users} onCreateReminder={onCreateReminder} onDelete={() => onDelete(initiative.id)} onOpen={tab => onOpen(initiative, tab)} onUpdate={onUpdate}/></ContextMenu.Portal></ContextMenu.Root>

  function renderCell(property: Property) {
    if (property === 'priority') return <InitiativeProperties compact only="priority" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'owner') return <InitiativeProperties compact only="owner" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'leadTeam') return <InitiativeProperties compact only="leadTeam" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'target') return <InitiativeProperties compact only="target" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'status') return <InitiativeProperties compact only="status" initiative={initiative} teams={teams} users={users} onUpdate={onUpdate}/>
    if (property === 'projects') return <button aria-label={`${completed} of ${linked.length} project completed. Click to view projects.`} className="li-project-count" onClick={() => onOpen(initiative, 'projects')} type="button"><span>{completed} /</span> {linked.length}</button>
    if (property === 'health') return <button className={`li-health is-${initiative.health}`} onClick={onOpenUpdates} type="button"><i/>{initiativeUpdates.length ? healthLabel(initiative.health) : 'No updates'}</button>
    if (property === 'activeProjects') return <button className="li-active-projects" onClick={onOpenUpdates} type="button"><i/>{needingUpdate}</button>
    if (property === 'labels') return <InitiativeLabelsPicker compact initiative={initiative} labels={labels} onUpdate={onUpdate}/>
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
    {visible('Owner') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-owner-empty">+</span>Owner<kbd>N then O</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => onUpdate({ ownerId: '' })}><span className="li-owner-empty">+</span>No owner</ContextMenu.Item>{users.map(user => <ContextMenu.Item key={user.id} onSelect={() => onUpdate({ ownerId: user.id })}><Avatar name={user.displayName || user.name}/>{user.displayName || user.name}{initiative.owner?.id === user.id && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Lead team') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-owner-empty">+</span>Lead team<ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => onUpdate({ leadTeamId: '' })}>No lead team</ContextMenu.Item>{teams.map(team => <ContextMenu.Item key={team.id} onSelect={() => onUpdate({ leadTeamId: team.id })}><span className="li-team-mark" style={{ background: team.color }}>{team.key.slice(0, 2)}</span><span data-i18n-ignore>{team.name}</span>{initiative.leadTeamId === team.id && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Target date') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-target-glyph is-empty"/>Target date…<kbd>Ctrl ⌥ D</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu"><ContextMenu.Item onSelect={() => onUpdate({ targetDate: '' })}>No target date</ContextMenu.Item><ContextMenu.Item onSelect={() => onUpdate({ targetDate: isoDate(new Date()) })}>Today</ContextMenu.Item><ContextMenu.Item onSelect={() => onUpdate({ targetDate: isoDate(due) })}>Tomorrow</ContextMenu.Item></ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
    {visible('Labels') && <ContextMenu.Sub><ContextMenu.SubTrigger><span className="li-label-outline"/>Labels<kbd>N then L</kbd><ChevronRight size={12}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className="li-menu">{labels.map(label => <ContextMenu.Item key={label.id} onSelect={() => onUpdate({ labelIds: initiative.labelIds.includes(label.id) ? initiative.labelIds.filter(id => id !== label.id) : [...initiative.labelIds, label.id] })}><i className="li-filter-color" style={{ background: label.color }}/>{label.name}{initiative.labelIds.includes(label.id) && <Check className="li-menu-end" size={12}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>}
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

function InitiativeCreateRow({ labels, users, teams, viewer, view, onCancel, onCreate }: { labels: IssueLabel[]; users: User[]; teams: Team[]; viewer: User; view: InitiativesRouteView; onCancel: () => void; onCreate: (input: InitiativeMutationInput & { name: string }) => Promise<void> }) {
  const initialStatus: InitiativeStatus = view === 'planned' ? 'planned' : 'active'
  const [draft, setDraft] = useState<Initiative>({ id: 'draft', name: '', slugId: '', summary: '', description: '', icon: 'Initiative', color: '#8a8f98', status: initialStatus, priority: 0, priorityLabel: 'No priority', health: 'noUpdate', creator: viewer, contributingTeamIds: [], labelIds: [], projectIds: [], resources: [], comments: [], favorite: false, subscribed: false, notificationRules: { descriptionChanges: true, newUpdate: true, allProjectUpdates: false }, updateSchedule: { cadence: 'none', weekday: 1, timeRange: '09:00-12:00' }, descriptionHistory: [], createdAt: '', updatedAt: '' })
  const [saving, setSaving] = useState(false)
  const update = (input: InitiativeMutationInput) => setDraft(current => ({ ...current, ...input, owner: input.ownerId !== undefined ? users.find(user => user.id === input.ownerId) : current.owner, priorityLabel: input.priority !== undefined ? ['No priority', 'Urgent', 'High', 'Medium', 'Low'][input.priority] : current.priorityLabel }))
  const submit = async () => {
    if (!draft.name.trim() || saving) return
    setSaving(true)
    try { await onCreate({ name: draft.name.trim(), summary: draft.summary, icon: draft.icon, color: draft.color, status: draft.status, priority: draft.priority, ownerId: draft.owner?.id, leadTeamId: draft.leadTeamId, contributingTeamIds: draft.contributingTeamIds, targetDate: draft.targetDate, labelIds: draft.labelIds }) } finally { setSaving(false) }
  }
  return <section className="li-create-row" onKeyDown={event => { if (event.key === 'Escape') onCancel(); if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void submit() }}>
    <ViewIconPicker color={draft.color} icon={draft.icon} onChange={update}/>
    <div className="li-create-fields"><input autoFocus aria-label="Initiative name" placeholder="New initiative" value={draft.name} onChange={event => update({ name: event.target.value })}/><input aria-label="Initiative summary" placeholder="Add a short summary…" value={draft.summary} onChange={event => update({ summary: event.target.value })}/><div className="li-create-properties"><InitiativeProperties compact initiative={draft} teams={teams} users={users} onUpdate={update}/><InitiativeLabelsPicker compact initiative={draft} labels={labels} onUpdate={update}/></div></div>
    <footer><button onClick={onCancel} type="button">Cancel</button><button disabled={!draft.name.trim() || saving} onClick={() => void submit()} type="button">{saving ? 'Creating…' : 'Create'}</button></footer>
  </section>
}

function InitiativesEmpty({ filtered, onCreate, view }: { filtered: boolean; onCreate: () => void; view: InitiativesRouteView }) {
  if (filtered) return <div className="li-empty li-empty--filtered"><strong>No initiatives match these filters</strong><p>Try removing or changing a filter.</p></div>
  if (view === 'planned') return <div className="li-empty li-empty--planned"><span className="li-empty-initiatives" aria-hidden="true"><i/><i/><i/></span><strong>Upcoming initiatives</strong><p>Initiatives are larger, strategic product efforts that set the direction of your company. They are comprised of all projects that align with the goals of the initiative and allow you to monitor their progress at scale.</p><p>Once you create an upcoming initiative that hasn’t started yet, it will show up here.</p><div><button onClick={onCreate} type="button">Create new initiative <kbd>N</kbd><span>then</span><kbd>I</kbd></button><a href="https://flow.app/docs/initiatives" rel="noreferrer" target="_blank">Documentation</a></div></div>
  return <div className="li-empty li-empty--planned"><span className="li-empty-initiatives" aria-hidden="true"><i/><i/><i/></span><strong>{view === 'active' ? 'Active initiatives' : 'Create your first initiative'}</strong><p>{view === 'active' ? 'Initiatives in progress will appear here.' : 'Coordinate strategic work and monitor project progress at scale.'}</p><div><button onClick={onCreate} type="button">Create new initiative</button></div></div>
}

function ColumnHeader({ property, onSort }: { property: Property; onSort: (sort: Sort) => void }) {
  const label = ({ owner: 'Owner', status: 'Status', priority: 'Priority', leadTeam: 'Lead team', teams: 'Contributing teams', health: 'Health', projects: 'Projects', activeProjects: 'Active Projects', target: 'Target', created: 'Created', updated: 'Updated', completed: 'Completed', labels: 'Labels', description: 'Description' } as const)[property]
  const sortable: Partial<Record<Property, Sort>> = { priority: 'priority', target: 'target', health: 'health', created: 'created', updated: 'updated' }
  return sortable[property] ? <button onClick={() => onSort(sortable[property]!)} type="button">{label}</button> : <span>{label}</span>
}

function DisplayMenu({ grouping, properties, showTeamInitiatives, sort, onGrouping, onProperty, onShowTeamInitiatives, onSort }: { grouping: Grouping; properties: Set<Property>; showTeamInitiatives: boolean; sort: Sort; onGrouping: (grouping: Grouping) => void; onProperty: (property: Property) => void; onShowTeamInitiatives: (show: boolean) => void; onSort: (sort: Sort) => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Display options" className="li-icon-button" type="button"><SlidersHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu li-display-menu" sideOffset={4}>
    <div className="li-display-row"><span>Grouping</span><DropdownMenu.Sub><DropdownMenu.SubTrigger>{groupingLabel(grouping)} <ChevronRight size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{(['none','contributingTeam','leadTeam','owner','health','status','priority','label'] as Grouping[]).map(item => <DropdownMenu.Item key={item} onSelect={() => onGrouping(item)}>{groupingLabel(item)}{grouping === item && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub></div>
    <div className="li-display-row"><span>Ordering</span><DropdownMenu.Sub><DropdownMenu.SubTrigger>{titleCase(sort)} <ChevronRight size={12}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu">{(['manual', 'name', 'priority', 'target', 'health', 'created', 'updated'] as Sort[]).map(item => <DropdownMenu.Item key={item} onSelect={() => onSort(item)}>{titleCase(item)}{sort === item && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub></div>
    <DropdownMenu.Separator/><DropdownMenu.CheckboxItem checked={showTeamInitiatives} className="li-display-team-toggle" onCheckedChange={value => onShowTeamInitiatives(value === true)}>{showTeamInitiatives ? <Check size={12}/> : <span/>}Show team initiatives</DropdownMenu.CheckboxItem><DropdownMenu.Separator/><DropdownMenu.Label>Display properties</DropdownMenu.Label><div className="li-display-properties">{PROPERTY_ORDER.map(property => <button aria-pressed={properties.has(property)} className={properties.has(property) ? 'is-active' : ''} key={property} onClick={event => { event.preventDefault(); onProperty(property) }} type="button"><span>{properties.has(property) && <Check size={10}/>}</span>{columnLabel(property)}</button>)}</div>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function FilterMenu({ filters, initiatives, users, teams, labels, onChange }: { filters: FilterState; initiatives: Initiative[]; users: User[]; teams: Team[]; labels: IssueLabel[]; onChange: (filters: FilterState) => void }) {
  const [query, setQuery] = useState('')
  const entries = [
    { id: 'status', label: 'Status' }, { id: 'priority', label: 'Priority' }, { id: 'ownerId', label: 'Owner' }, { id: 'creatorId', label: 'Creator' },
    { id: 'leadTeamId', label: 'Lead team' }, { id: 'teamId', label: 'Contributing teams' }, { id: 'labelId', label: 'Labels' }, { id: 'health', label: 'Health' }, { id: 'date', label: 'Dates' },
  ].filter(item => item.label.toLowerCase().includes(query.toLowerCase()))
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}><DropdownMenu.Trigger asChild><button aria-label="Add filter" className="li-icon-button" type="button"><Filter size={14}/>{Object.keys(filters).length > 0 && <i/>}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu li-filter-menu" sideOffset={4}>
    <div className="li-menu-search"><Search size={13}/><input aria-label="Add Filter…" autoFocus placeholder="Add Filter…" value={query} onChange={event => setQuery(event.target.value)}/><kbd>F</kbd></div>
    {!query && <><DropdownMenu.Item disabled title="Requires the Flow AI integration"><span className="li-ai-filter">✦</span>AI filter</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item disabled title="Add multiple property filters below">Advanced filter</DropdownMenu.Item><DropdownMenu.Separator/></>}
    {entries.map(entry => <DropdownMenu.Sub key={entry.id}><DropdownMenu.SubTrigger>{entry.label}<ChevronRight className="li-menu-end" size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="li-menu li-filter-values" sideOffset={5}>{filterOptions(entry.id, initiatives, users, teams, labels).map(option => <DropdownMenu.Item key={String(option.value)} onSelect={() => onChange({ ...filters, [entry.id]: option.value })}>{option.icon}{option.label}{filters[entry.id as keyof FilterState] === option.value && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function InitiativeUpdatesPanel({ initiative, updates, viewer, onClose, onCreateUpdate, onOpen }: { initiative: Initiative; updates: InitiativeUpdate[]; viewer: User; onClose: () => void; onCreateUpdate: Props['onCreateUpdate']; onOpen: () => void }) {
  const [composing, setComposing] = useState(false)
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>(initiative.health === 'noUpdate' ? 'onTrack' : initiative.health)
  const [saving, setSaving] = useState(false)
  const submit = async () => { if (!body.trim() || saving) return; setSaving(true); try { await onCreateUpdate(initiative.id, { body: body.trim(), health }); setBody(''); setComposing(false) } finally { setSaving(false) } }
  return <div className="li-update-panel-backdrop" onMouseDown={event => { if (event.currentTarget === event.target) onClose() }}><aside className="li-update-panel" aria-label="Initiative updates">
    <header><button onClick={onOpen} type="button"><ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/>{initiative.name}</button><button onClick={() => toast.success('Subscribed to initiative updates')} type="button">Subscribed</button><button onClick={() => setComposing(true)} type="button">New update</button><button aria-label="Close initiative updates" onClick={onClose} type="button"><X size={15}/></button></header>
    {composing && <div className="li-update-panel-composer"><div><Avatar name={viewer.displayName || viewer.name}/><strong>{viewer.displayName || viewer.name}</strong><select aria-label="Update health" value={health} onChange={event => setHealth(event.target.value as Project['health'])}><option value="onTrack">On track</option><option value="atRisk">At risk</option><option value="offTrack">Off track</option></select></div><textarea autoFocus aria-label="Initiative update" placeholder="Write an initiative update…" value={body} onChange={event => setBody(event.target.value)}/><footer><button onClick={() => setComposing(false)} type="button">Cancel</button><button disabled={!body.trim() || saving} onClick={() => void submit()} type="button">Post update</button></footer></div>}
    {!updates.length && !composing ? <div className="li-update-panel-empty"><span><Send size={22}/></span><strong>Initiative updates</strong><p>Write a short status report to keep everyone up-to-date on the progress and health of this initiative</p><button onClick={() => setComposing(true)} type="button">New initiative update <kbd>N</kbd><span>then</span><kbd>U</kbd></button><a href="https://flow.app/docs/initiative-and-project-updates" rel="noreferrer" target="_blank">Documentation</a></div> : <div className="li-update-panel-list">{updates.map(update => <article key={update.id}><header><Avatar name={update.user.displayName}/><strong>{update.user.displayName}</strong><time>{new Date(update.createdAt).toLocaleDateString('en', { month: 'short', day: 'numeric' })}</time><span className={`li-update-health is-${update.health}`}/>{healthLabel(update.health)}</header><p>{update.body}</p></article>)}</div>}
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
function columnWidth(property: Property) { return ({ description: '0px', owner: '72px', status: '78px', priority: '72px', leadTeam: '84px', teams: '110px', health: '72px', projects: '70px', activeProjects: '60px', target: '86px', created: '78px', updated: '78px', completed: '82px', labels: '100px' })[property] }
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
function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No updates' } as const)[value] }
function isoDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
