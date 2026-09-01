import * as Tooltip from '@radix-ui/react-tooltip'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, Box, CalendarPlus, Clipboard, FileText, LayoutGrid, Link2, MessageCirclePlus, MoreHorizontal, Move, Package, Search, Star, Tag, Trash2, UserRound } from 'lucide-react'
import { useEffect, useRef, useState, type ChangeEvent, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { MembersIcon, NoAssigneeIcon } from '@/components/issue/issue-icons'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import { CheckIcon, ChevronRightIcon, PlusIcon } from './projects-page-icons'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import { ProjectPropertyPicker, ProjectStatusGlyph, type ProjectPropertyOption } from './project-property-picker'
import { ProjectDatePicker, ProjectTargetDatePicker } from './project-target-date-picker'
import { projectLabelGroupProperty } from './projects-display-model'
import './projects-page.css'

export type ProjectPageItem = {
  id: string
  name: string
  href?: string
  icon?: string
  color?: string
  summary?: string
  milestone?: string
  health: 'on-track' | 'off-track' | 'at-risk' | 'no-update'
  healthLabel?: string
  priority: 'urgent' | 'high' | 'medium' | 'low' | 'none'
  position?: number
  lead?: { id: string, name: string, initials?: string, avatarUrl?: string, color?: string }
  targetDate?: string
  issueCount: number
  progress: number
  status: string
  team?: { id: string, name: string }
  memberIds?: string[]
  labelIds?: string[]
  initiativeNames?: string[]
  labelsByGroup?: Record<string, Array<{ id: string; name: string; color: string }>>
  teamIds?: string[]
  rawStartDate?: string
  rawTargetDate?: string
  startDate?: string
  createdAt?: string
  updatedAt?: string
}

export type ProjectDataGroup = {
  id: string
  name: string
  color?: string
  projects: ProjectPageItem[]
  subgroups?: ProjectDataGroup[]
}

export type ProjectsDataViewProps = {
  groups: ProjectDataGroup[]
  layout?: 'list' | 'board' | 'timeline'
  loading?: boolean
  error?: string | null
  selectedIds?: string[]
  visibleProperties?: string[]
  manualOrdering?: boolean
  propertyOptions?: ProjectPropertyOptions
  onCreateProject?: (status: string) => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenProjectIssues?: (project: ProjectPageItem) => void
  onOpenProjectUpdates?: (project: ProjectPageItem) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onRetry?: () => void
  onUpdateProject?: (projectId: string, input: { startDate?: string; targetDate?: string }) => Promise<unknown>
  onSelectionChange?: (ids: string[]) => void
  onSort?: (column: ProjectSortColumn, direction: 'asc' | 'desc') => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  projectMenu?: ProjectMenuIntegration
  labelGroupProperties?: Array<{ id: string; name: string }>
  sort?: { column: ProjectSortColumn, direction: 'asc' | 'desc' }
}

export type ProjectMenuIntegration = {
  isFavorite: (projectId: string) => boolean
  subscriptionEvents: (projectId: string) => string[]
  onFavoriteChange: (projectId: string, favorite: boolean) => Promise<unknown>
  onSubscriptionEventsChange: (projectId: string, events: string[]) => Promise<unknown>
  onCreateReminder: (projectId: string, remindAt: string) => Promise<unknown>
}

export type ProjectProperty = 'health' | 'priority' | 'lead' | 'members' | 'labels' | 'startDate' | 'targetDate' | 'status'
export type ProjectSortColumn = 'name' | 'health' | 'priority' | 'targetDate' | 'status'
export type ProjectAction = 'copy' | 'move' | 'moveDown' | 'moveBottom' | 'favorite' | 'subscribe' | 'comment' | 'delete' | 'rename' | 'initiatives' | 'dependencies' | 'schedule' | 'customerRequest'
export type ProjectPropertyOptions = Partial<Record<ProjectProperty, ProjectPropertyOption[]>>

const PROPERTY_OPTIONS: Record<ProjectProperty, ProjectPropertyOption[]> = {
  health: [
    { label: 'No update', value: 'no-update' },
    { label: 'On track', value: 'on-track' },
    { label: 'At risk', value: 'at-risk' },
    { label: 'Off track', value: 'off-track' },
  ],
  priority: [
    { label: 'No priority', shortcut: '0', value: 'none' },
    { label: 'Urgent', shortcut: '1', value: 'urgent' },
    { label: 'High', shortcut: '2', value: 'high' },
    { label: 'Medium', shortcut: '3', value: 'medium' },
    { label: 'Low', shortcut: '4', value: 'low' },
  ],
  lead: [{ label: 'No lead', shortcut: '0', value: '' }],
  members: [],
  labels: [],
  startDate: [],
  targetDate: [
    { label: 'No target date', value: '' },
    { label: 'Today', value: 'today' },
    { label: 'End of this month', value: 'month' },
    { label: 'Custom…', value: 'custom' },
  ],
  status: [
    { label: 'Backlog', shortcut: '1', statusType: 'backlog', value: 'Backlog' },
    { label: 'Planned', shortcut: '2', statusType: 'planned', value: 'Planned' },
    { label: 'In Progress', shortcut: '3', statusType: 'started', value: 'In Progress' },
    { label: 'Completed', shortcut: '4', statusType: 'completed', value: 'Completed' },
    { label: 'Canceled', shortcut: '5', statusType: 'canceled', value: 'Canceled' },
  ],
}

export function ProjectsDataView({
  groups,
  layout = 'list',
  loading = false,
  error = null,
  selectedIds = [],
  visibleProperties = ['Summary', 'Priority', 'Health', 'Lead', 'Target date', 'Issues', 'Status'],
  manualOrdering = false,
  propertyOptions,
  onCreateProject,
  onOpenProject,
  onOpenProjectIssues,
  onOpenProjectUpdates,
  onProjectAction,
  onProjectVisualChange,
  onPropertyChange,
  onRetry,
  onSelectionChange,
  onSort,
  onUpdateProject,
  projectMenu,
  labelGroupProperties = [],
  sort: externalSort,
}: ProjectsDataViewProps) {
  const [collapsed, setCollapsed] = useState<string[]>([])
  const [sort, setSort] = useState<{ column: ProjectSortColumn, direction: 'asc' | 'desc' }>(externalSort ?? { column: 'name', direction: 'asc' })

  useEffect(() => {
    if (externalSort) setSort(externalSort)
  }, [externalSort])

  const toggleSelection = (id: string, range = false) => {
    const next = selectedIds.includes(id) ? selectedIds.filter(item => item !== id) : range ? [...new Set([...selectedIds, id])] : [...selectedIds, id]
    onSelectionChange?.(next)
  }

  const changeSort = (column: ProjectSortColumn) => {
    const direction = sort.column === column && sort.direction === 'asc' ? 'desc' : 'asc'
    setSort({ column, direction })
    onSort?.(column, direction)
  }

  if (loading) return <ProjectsLoadingState layout={layout} />
  if (error) return <ProjectsErrorState error={error} onRetry={onRetry} />
  if (!groups.some(groupHasProjects)) return <ProjectsEmptyState onCreate={() => onCreateProject?.('Backlog')} />

  const visible = new Set(visibleProperties)
  if (layout === 'board') return <div className="lp-project-board" role="list">
    {groups.map((group, groupIndex) => <ProjectBoardColumn
      group={group}
      key={group.id}
      manualOrdering={manualOrdering}
      onCreateProject={onCreateProject}
      onOpenProject={onOpenProject}
      onOpenProjectIssues={onOpenProjectIssues}
      onOpenProjectUpdates={onOpenProjectUpdates}
      onProjectAction={onProjectAction}
      onProjectVisualChange={onProjectVisualChange}
      onPropertyChange={onPropertyChange}
      projectMenu={projectMenu}
      labelGroupProperties={labelGroupProperties}
      onDropProject={projectId => {
        const project = findProject(groups, projectId)
        const destination = projectGroupProperty(group)
        if (!project || !destination || !onPropertyChange) return false
        onPropertyChange(project, destination.property, destination.value)
        return true
      }}
      onKeyboardMove={(project, direction) => {
        const destinationGroup = groups[groupIndex + direction]
        const destination = destinationGroup && projectGroupProperty(destinationGroup)
        if (!destination || !onPropertyChange) return
        onPropertyChange(project, destination.property, destination.value)
      }}
      propertyOptions={propertyOptions}
      visible={visible}
    />)}
  </div>

  if (layout === 'timeline') return <ProjectTimeline groups={groups} onOpenProject={onOpenProject} onUpdateProject={onUpdateProject} propertyOptions={propertyOptions}/>

  return <div className="lp-project-table" role="grid" style={{ '--lp-project-grid': projectGrid(visible, labelGroupProperties) } as CSSProperties}>
    <ProjectTableHeader labelGroupProperties={labelGroupProperties} sort={sort} onSort={changeSort} visible={visible} />
    {groups.map(group => {
      const isCollapsed = collapsed.includes(group.id)
    return <section aria-label={group.name} className="lp-project-group" key={group.id} role="rowgroup">
        <ProjectGroupHeader
          collapsed={isCollapsed}
          color={group.color}
          count={projectCount(group)}
          name={group.name}
          onCreate={() => onCreateProject?.(projectCreateStatus(group.name))}
          onToggle={() => setCollapsed(current => current.includes(group.id) ? current.filter(id => id !== group.id) : [...current, group.id])}
          propertyOptions={propertyOptions}
        />
        {!isCollapsed && (group.subgroups?.length ? group.subgroups.map(subgroup => <ProjectSubgroup
          group={subgroup}
          key={subgroup.id}
          onOpen={onOpenProject}
          onOpenIssues={onOpenProjectIssues}
          onOpenUpdates={onOpenProjectUpdates}
          onProjectAction={onProjectAction}
          manualOrdering={manualOrdering}
          onProjectVisualChange={onProjectVisualChange}
          onPropertyChange={onPropertyChange}
          onSelect={toggleSelection}
          propertyOptions={propertyOptions}
          projectMenu={projectMenu}
          labelGroupProperties={labelGroupProperties}
          selectedIds={selectedIds}
          visible={visible}
        />) : group.projects.map(project => <ProjectListRow
          key={project.id}
          onOpen={onOpenProject}
          onOpenIssues={onOpenProjectIssues}
          onOpenUpdates={onOpenProjectUpdates}
          onProjectAction={onProjectAction}
          manualOrdering={manualOrdering}
          onProjectVisualChange={onProjectVisualChange}
          onPropertyChange={onPropertyChange}
          onSelect={toggleSelection}
          propertyOptions={propertyOptions}
          projectMenu={projectMenu}
          labelGroupProperties={labelGroupProperties}
          project={project}
          selected={selectedIds.includes(project.id)}
          visible={visible}
        />))}
      </section>
    })}
  </div>
}

function ProjectSubgroup({ group, manualOrdering, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, onSelect, propertyOptions, projectMenu, labelGroupProperties, selectedIds, visible }: {
  group: ProjectDataGroup
  onOpen?: (project: ProjectPageItem) => void
  onOpenIssues?: (project: ProjectPageItem) => void
  onOpenUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  manualOrdering?: boolean
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onSelect: (id: string, range?: boolean) => void
  propertyOptions?: ProjectPropertyOptions
  projectMenu?: ProjectMenuIntegration
  labelGroupProperties?: Array<{ id: string; name: string }>
  selectedIds: string[]
  visible: Set<string>
}) {
  return <div className="lp-project-subgroup">
    <div className="lp-project-subgroup__header"><ProjectGroupStatus color={group.color} compact name={group.name} propertyOptions={propertyOptions}/><span data-i18n-ignore>{group.name}</span><small>{projectCount(group)}</small></div>
    {group.projects.map(project => <ProjectListRow
      key={project.id}
      onOpen={onOpen}
      onOpenIssues={onOpenIssues}
      onOpenUpdates={onOpenUpdates}
      onProjectAction={onProjectAction}
      manualOrdering={manualOrdering}
      onProjectVisualChange={onProjectVisualChange}
      onPropertyChange={onPropertyChange}
      onSelect={onSelect}
      propertyOptions={propertyOptions}
      projectMenu={projectMenu}
      labelGroupProperties={labelGroupProperties}
      project={project}
      selected={selectedIds.includes(project.id)}
      visible={visible}
    />)}
  </div>
}

function ProjectTableHeader({ labelGroupProperties, sort, onSort, visible }: { labelGroupProperties: Array<{ id: string; name: string }>; sort: { column: ProjectSortColumn, direction: 'asc' | 'desc' }, onSort: (column: ProjectSortColumn) => void, visible: Set<string> }) {
  const header = (column: ProjectSortColumn, label: string) => <button
    aria-label={`${sort.column === column ? sort.direction === 'asc' ? 'A–Z' : 'Z–A' : 'Order by'} ${label}`}
    className={sort.column === column ? 'is-sorted' : ''}
    onClick={() => onSort(column)}
    type="button"
  ><span>{label}</span><span aria-hidden="true" className="lp-project-table__sort-icon"><ProjectSortIcon/></span></button>
  return <div className="lp-project-table__header" role="row">
    <span />
    <span />
    <div role="columnheader">{header('name', 'Name')}</div>
    <div aria-hidden={!visible.has('Health') || undefined} data-column-hidden={!visible.has('Health') || undefined} role="columnheader">{header('health', 'Health')}</div>
    <div aria-hidden={!visible.has('Priority') || undefined} data-column-hidden={!visible.has('Priority') || undefined} role="columnheader">{header('priority', 'Priority')}</div>
    <div aria-hidden={!visible.has('Lead') || undefined} data-column-hidden={!visible.has('Lead') || undefined} role="columnheader"><span>Lead</span></div>
    <div aria-hidden={!visible.has('Initiatives') || undefined} data-column-hidden={!visible.has('Initiatives') || undefined} role="columnheader"><span>Initiatives</span></div>
    <div aria-hidden={!visible.has('Target date') || undefined} data-column-hidden={!visible.has('Target date') || undefined} role="columnheader">{header('targetDate', 'Target date')}</div>
    <div aria-hidden={!visible.has('Issues') || undefined} data-column-hidden={!visible.has('Issues') || undefined} role="columnheader"><span>Issues</span></div>
    <div aria-hidden={!visible.has('Status') || undefined} data-column-hidden={!visible.has('Status') || undefined} role="columnheader">{header('status', 'Status')}</div>
    {labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).map(group => <div data-i18n-ignore key={group.id} role="columnheader"><span>{group.name}</span></div>)}
    <span />
  </div>
}

function ProjectGroupHeader({ collapsed, color = '#d6b326', count, name, onCreate, onToggle, propertyOptions }: {
  collapsed: boolean
  color?: string
  count: number
  name: string
  onCreate: () => void
  onToggle: () => void
  propertyOptions?: ProjectPropertyOptions
}) {
  return <div className="lp-project-group__header" data-group-status={projectGroupStatusType(name, propertyOptions) || undefined} role="row">
    <span />
    <button aria-expanded={!collapsed} aria-label={collapsed ? 'Expand group' : 'Collapse group'} className="lp-project-group__toggle" onClick={onToggle} type="button"><ChevronRightIcon /></button>
    <span className="lp-project-group__title"><ProjectGroupStatus color={color} name={name} propertyOptions={propertyOptions}/><strong data-i18n-ignore>{name}</strong><span aria-label="Project count" className="lp-project-group__count">{count}</span></span>
    <button aria-label="Create new project" className="lp-project-group__create" onClick={onCreate} type="button"><PlusIcon /></button>
  </div>
}

type ProjectItemActions = {
  project: ProjectPageItem
  onOpen?: (project: ProjectPageItem) => void
  onOpenIssues?: (project: ProjectPageItem) => void
  onOpenUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  manualOrdering?: boolean
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  propertyOptions?: ProjectPropertyOptions
  projectMenu?: ProjectMenuIntegration
  labelGroupProperties?: Array<{ id: string; name: string }>
  visible: Set<string>
}

function ProjectListRow({ project, selected, manualOrdering, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, onSelect, propertyOptions, projectMenu, labelGroupProperties = [], visible }: ProjectItemActions & { selected: boolean; onSelect: (id: string, range?: boolean) => void }) {
  const [menuPoint, setMenuPoint] = useState<{ x: number, y: number } | null>(null)
  const statusOption = (propertyOptions?.status ?? PROPERTY_OPTIONS.status).find(option => option.value === project.status)
  const rowKey = (event: KeyboardEvent<HTMLAnchorElement>) => {
    if (event.target !== event.currentTarget) return
    if (event.key === 'Enter') onOpen?.(project)
    if (event.key === ' ') {
      event.preventDefault()
      onSelect(project.id, event.shiftKey)
    }
  }
  return <>
    <a
      aria-label={project.name}
      aria-selected={selected}
      className={`lp-project-row ${selected ? 'is-selected' : ''}`}
      href={project.href}
      onClick={event => openProjectLink(event, project, onOpen)}
      onContextMenu={event => {
        event.preventDefault()
        setMenuPoint({ x: event.clientX, y: event.clientY })
      }}
      onKeyDown={rowKey}
      role="row"
      tabIndex={0}
    >
      <span />
      <label aria-label="Select project" className="lp-project-row__select" onClick={stopPropagation}>
        <input checked={selected} onChange={() => onSelect(project.id)} type="checkbox" />
        <span><CheckIcon /></span>
      </label>
      <div className="lp-project-row__name" role="gridcell">
        <ViewIconPicker color={project.color} icon={project.icon || 'Project'} onChange={visual => onProjectVisualChange?.(project, visual.icon, visual.color)} triggerClassName="lp-project-row__project-icon" />
        <div><strong>{project.name}</strong>{project.summary && <small>{project.summary}</small>}</div>
      </div>
      <div aria-hidden={!visible.has('Health') || undefined} data-column-hidden={!visible.has('Health') || undefined} role="gridcell"><button aria-label={project.healthLabel ?? `${healthText(project.health)}. Click to open updates.`} className="lp-project-row__health" onClick={event => { stopPropagation(event); onOpenUpdates?.(project) }} type="button"><HealthIcon value={project.health} /><span>{healthText(project.health)}</span>{project.health !== 'no-update' && <small>· {compactAge(project.updatedAt)}</small>}</button></div>
      <div aria-hidden={!visible.has('Priority') || undefined} data-column-hidden={!visible.has('Priority') || undefined} role="gridcell"><ProjectPropertyPicker label={`${priorityText(project.priority)} Priority`} onChange={value => onPropertyChange?.(project, 'priority', value)} options={propertyOptions?.priority ?? PROPERTY_OPTIONS.priority} property="priority" value={project.priority}><DataViewPriorityIcon value={project.priority} /></ProjectPropertyPicker></div>
      <div aria-hidden={!visible.has('Lead') || undefined} className={`lp-project-row__lead ${project.lead ? '' : 'is-empty'}`} data-column-hidden={!visible.has('Lead') || undefined} role="gridcell"><LeadPropertyButton lead={project.lead} onChange={value => onPropertyChange?.(project, 'lead', value)} options={propertyOptions?.lead} /></div>
      <div aria-hidden={!visible.has('Initiatives') || undefined} className="lp-project-row__initiatives" data-column-hidden={!visible.has('Initiatives') || undefined} role="gridcell"><span data-i18n-ignore>{project.initiativeNames?.join(', ')}</span></div>
      <div aria-hidden={!visible.has('Target date') || undefined} data-column-hidden={!visible.has('Target date') || undefined} role="gridcell"><ProjectTargetDatePicker buttonClassName="lp-project-row__date" displayValue={project.targetDate} onChange={value => onPropertyChange?.(project, 'targetDate', value)} value={project.rawTargetDate}>{project.targetDate || <span className="lp-project-row__date-placeholder">Set date</span>}</ProjectTargetDatePicker></div>
      <div aria-hidden={!visible.has('Issues') || undefined} data-column-hidden={!visible.has('Issues') || undefined} role="gridcell"><button aria-label={`Open ${project.name} issues`} className="lp-project-row__issues" onClick={event => { stopPropagation(event); onOpenIssues?.(project) }} type="button">{project.issueCount}</button></div>
      <div aria-hidden={!visible.has('Status') || undefined} className="lp-project-row__status" data-column-hidden={!visible.has('Status') || undefined} role="gridcell"><ProjectPropertyPicker buttonClassName="lp-project-row__progress" label={`${project.progress}%`} onChange={value => onPropertyChange?.(project, 'status', value)} options={propertyOptions?.status ?? PROPERTY_OPTIONS.status} property="status" value={project.status}><ProjectStatusGlyph color={statusOption?.color} name={project.status} progress={project.progress / 100} type={statusOption?.statusType}/><span>{project.progress}%</span></ProjectPropertyPicker><ProjectProgressSparkline createdAt={project.createdAt} progress={project.progress} startDate={project.rawStartDate} targetDate={project.rawTargetDate}/></div>
      {labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).map(group => <div className="lp-project-row__label-group" data-i18n-ignore key={group.id} role="gridcell">{(project.labelsByGroup?.[group.id] ?? []).map(label => <span key={label.id}><i style={{ background: label.color }}/>{label.name}</span>)}</div>)}
      <span />
    </a>
    <ProjectItemMenu manualOrdering={manualOrdering} onProjectAction={onProjectAction} onPropertyChange={onPropertyChange} options={propertyOptions} point={menuPoint} project={project} projectMenu={projectMenu} setPoint={setMenuPoint}/>
  </>
}

function ProjectBoardColumn({ group, manualOrdering, onCreateProject, onDropProject, onKeyboardMove, onOpenProject, onOpenProjectIssues, onOpenProjectUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, propertyOptions, projectMenu, labelGroupProperties, visible }: {
  group: ProjectsDataViewProps['groups'][number]
  manualOrdering?: boolean
  onCreateProject?: (status: string) => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenProjectIssues?: (project: ProjectPageItem) => void
  onOpenProjectUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onDropProject: (projectId: string) => boolean
  onKeyboardMove: (project: ProjectPageItem, direction: -1 | 1) => void
  propertyOptions?: ProjectPropertyOptions
  projectMenu?: ProjectMenuIntegration
  labelGroupProperties?: Array<{ id: string; name: string }>
  visible: Set<string>
}) {
  const [collapsed, setCollapsed] = useState(false)
  const [dragOver, setDragOver] = useState(false)
  const card = (project: ProjectPageItem) => <ProjectBoardCard key={project.id} manualOrdering={manualOrdering} onKeyboardMove={direction => onKeyboardMove(project, direction)} onOpen={onOpenProject} onOpenIssues={onOpenProjectIssues} onOpenUpdates={onOpenProjectUpdates} onProjectAction={onProjectAction} onProjectVisualChange={onProjectVisualChange} onPropertyChange={onPropertyChange} project={project} projectMenu={projectMenu} propertyOptions={propertyOptions} labelGroupProperties={labelGroupProperties} visible={visible} />
  return <section aria-label={group.name} className="lp-project-board__column" data-collapsed={collapsed || undefined} data-drop-target={dragOver || undefined} onDragEnter={event => { if (event.dataTransfer.types.includes(PROJECT_DRAG_TYPE)) setDragOver(true) }} onDragLeave={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragOver(false) }} onDragOver={event => { if (event.dataTransfer.types.includes(PROJECT_DRAG_TYPE)) { event.preventDefault(); event.dataTransfer.dropEffect = 'move' } }} onDrop={event => { const projectId = event.dataTransfer.getData(PROJECT_DRAG_TYPE); if (projectId) { event.preventDefault(); onDropProject(projectId) } setDragOver(false) }}>
    <header><ProjectGroupStatus color={group.color} name={group.name} propertyOptions={propertyOptions}/><strong data-i18n-ignore>{group.name}</strong><span>{projectCount(group)}</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open group menu" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="lp-project-board__group-menu" sideOffset={4}><DropdownMenu.Item onSelect={() => setCollapsed(value => !value)}>{collapsed ? 'Expand group' : 'Collapse group'}</DropdownMenu.Item><DropdownMenu.Item onSelect={() => onCreateProject?.(projectCreateStatus(group.name))}>Create new project</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root><button aria-label="Create new project" onClick={() => onCreateProject?.(projectCreateStatus(group.name))} type="button"><PlusIcon /></button></header>
    {!collapsed && <div className="lp-project-board__cards">
      {group.subgroups?.map(subgroup => <section className="lp-project-board__subgroup" key={subgroup.id}><header><ProjectGroupStatus color={subgroup.color} compact name={subgroup.name} propertyOptions={propertyOptions}/><span data-i18n-ignore>{subgroup.name}</span><small>{projectCount(subgroup)}</small></header>{subgroup.projects.map(card)}</section>)}
      {!group.subgroups?.length && group.projects.map(card)}
      <button className="lp-project-board__add" onClick={() => onCreateProject?.(projectCreateStatus(group.name))} type="button"><PlusIcon /> Add new project</button>
    </div>}
  </section>
}

function ProjectBoardCard({ project, manualOrdering, onKeyboardMove, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, projectMenu, propertyOptions, labelGroupProperties = [], visible }: ProjectItemActions & { onKeyboardMove: (direction: -1 | 1) => void }) {
  const [menuPoint, setMenuPoint] = useState<{ x: number, y: number } | null>(null)
  return <>
    <a
      aria-label={project.name}
      className="lp-project-card"
      draggable={Boolean(onPropertyChange)}
      href={project.href}
      onClick={event => openProjectLink(event, project, onOpen)}
      onContextMenu={event => {
        event.preventDefault()
        setMenuPoint({ x: event.clientX, y: event.clientY })
      }}
      onDragEnd={event => { event.currentTarget.removeAttribute('data-dragging') }}
      onDragStart={event => { event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData(PROJECT_DRAG_TYPE, project.id); event.currentTarget.setAttribute('data-dragging', 'true') }}
      onKeyDown={event => { if (event.target !== event.currentTarget) return; if (event.key === 'Enter') onOpen?.(project); if (event.altKey && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) { event.preventDefault(); onKeyboardMove(event.key === 'ArrowLeft' ? -1 : 1) } }}
      role="button"
      tabIndex={0}
    >
      <div className="lp-project-card__top"><span className="lp-project-card__identity"><ViewIconPicker color={project.color} icon={project.icon || 'Project'} onChange={visual => onProjectVisualChange?.(project, visual.icon, visual.color)} triggerClassName="lp-project-row__project-icon"/><strong>{project.name}</strong></span><span className="lp-project-card__properties">
        {visible.has('Health') && <button aria-label={project.healthLabel ?? `${healthText(project.health)}. Click to open updates.`} className="lp-project-property-trigger" onClick={event => { stopPropagation(event); onOpenUpdates?.(project) }} type="button"><HealthIcon value={project.health} /></button>}
        {visible.has('Priority') && <ProjectPropertyPicker label={`${priorityText(project.priority)} Priority`} onChange={value => onPropertyChange?.(project, 'priority', value)} options={propertyOptions?.priority ?? PROPERTY_OPTIONS.priority} property="priority" value={project.priority}><DataViewPriorityIcon value={project.priority} /></ProjectPropertyPicker>}
        {visible.has('Lead') && <span className={`lp-project-card__lead ${project.lead ? '' : 'is-empty'}`}><LeadPropertyButton lead={project.lead} onChange={value => onPropertyChange?.(project, 'lead', value)} options={propertyOptions?.lead} /></span>}
      </span></div>
      {visible.has('Summary') && project.summary && <p>{project.summary}</p>}
      <div className="lp-project-card__bottom">
        {visible.has('Target date') && <ProjectTargetDatePicker displayValue={project.targetDate} onChange={value => onPropertyChange?.(project, 'targetDate', value)} value={project.rawTargetDate}>{project.targetDate}</ProjectTargetDatePicker>}
        {visible.has('Initiatives') && project.initiativeNames?.map(name => <span className="lp-project-card__initiative" data-i18n-ignore key={name}>{name}</span>)}
        {labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).flatMap(group => project.labelsByGroup?.[group.id] ?? []).map(label => <span className="lp-project-card__initiative" data-i18n-ignore key={label.id}><i style={{ background: label.color }}/>{label.name}</span>)}
        {project.milestone && <button className="lp-project-card__milestone" onClick={stopPropagation} type="button"><span />{project.milestone}</button>}
        {visible.has('Issues') && <button className="lp-project-card__issues" onClick={event => { stopPropagation(event); onOpenIssues?.(project) }} type="button">{project.issueCount} issues</button>}
      </div>
    </a>
    <ProjectItemMenu manualOrdering={manualOrdering} onProjectAction={onProjectAction} onPropertyChange={onPropertyChange} options={propertyOptions} point={menuPoint} project={project} projectMenu={projectMenu} setPoint={setMenuPoint}/>
  </>
}

const PROJECT_DRAG_TYPE = 'application/x-flow-project-id'

function findProject(groups: ProjectDataGroup[], projectId: string): ProjectPageItem | undefined {
  for (const group of groups) {
    const project = group.projects.find(item => item.id === projectId) ?? group.subgroups?.flatMap(item => item.projects).find(item => item.id === projectId)
    if (project) return project
  }
  return undefined
}

function projectGroupProperty(group: ProjectDataGroup): { property: ProjectProperty; value: string } | undefined {
  if (group.id.startsWith('status-')) return { property: 'status', value: group.name }
  if (group.id.startsWith('priority-')) return { property: 'priority', value: group.id.slice('priority-'.length) }
  if (group.id.startsWith('health-')) return { property: 'health', value: group.id.slice('health-'.length) }
  if (group.id === 'lead-none') return { property: 'lead', value: '' }
  if (group.id.startsWith('lead-')) return { property: 'lead', value: group.id.slice('lead-'.length) }
  return undefined
}

function openProjectLink(event: MouseEvent<HTMLAnchorElement>, project: ProjectPageItem, onOpen?: (project: ProjectPageItem) => void) {
  if ((event.target as Element).closest('button,input,label')) { event.preventDefault(); return }
  if (onOpen && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) { event.preventDefault(); onOpen(project) }
}

function ProjectItemMenu({ manualOrdering, onProjectAction, onPropertyChange, options, point, project, projectMenu, setPoint }: Pick<ProjectItemActions,'manualOrdering'|'onProjectAction'|'onPropertyChange'|'project'|'projectMenu'> & { options?: ProjectPropertyOptions; point: {x:number;y:number}|null; setPoint: (point:{x:number;y:number}|null)=>void }) {
  return point ? <ProjectContextMenu integration={projectMenu} manualOrdering={manualOrdering} options={options} project={project} onPropertyChange={(property,value)=>onPropertyChange?.(project,property,value)} onAction={action=>{setPoint(null);onProjectAction?.(project,action)}} onClose={()=>setPoint(null)} point={point}/> : null
}

function LeadPropertyButton({ lead, onChange, options }: {
  lead?: ProjectPageItem['lead']
  onChange: (value: string) => void
  options?: ProjectPropertyOption[]
}) {
  const property = <ProjectPropertyPicker label={lead?.name ?? 'No lead'} onChange={onChange} options={options ?? PROPERTY_OPTIONS.lead} property="lead" value={lead?.id ?? ''}><ProjectAvatar lead={lead} /></ProjectPropertyPicker>
  if (lead) return property
  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild><span className="lp-project-lead-trigger">{property}</span></Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="lp-project-lead-tooltip" side="bottom" sideOffset={6}>
          <span>Set project lead</span><span className="lp-project-lead-tooltip__shortcut"><kbd>P</kbd><span>then</span><kbd>A</kbd></span>
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>
}

type ProjectContextKind = ProjectProperty | 'copy-menu' | 'move-menu' | 'subscribe-menu' | 'remind-menu' | 'more-menu'
type ProjectContextItem = { label: string; icon: ReactNode; action?: ProjectAction; kind?: ProjectContextKind; shortcut?: string; danger?: boolean; date?: 'startDate' | 'targetDate' }

function ProjectContextMenu({ integration, manualOrdering = false, point, onAction, onClose, onPropertyChange, options, project }: { integration?: ProjectMenuIntegration; manualOrdering?: boolean; point: { x: number, y: number }, onAction: (action: ProjectAction) => void, onClose: () => void, onPropertyChange: (property: ProjectProperty, value: string) => void, options?: ProjectPropertyOptions, project: ProjectPageItem }) {
  const ref = useRef<HTMLDivElement>(null)
  const nestedRef = useRef<HTMLDivElement>(null)
  const [nested, setNested] = useState<ProjectContextKind | null>(null)
  const [query, setQuery] = useState('')
  const [nestedQuery, setNestedQuery] = useState('')
  const favorite = integration?.isFavorite(project.id) ?? false
  const subscriptionEvents = new Set(integration?.subscriptionEvents(project.id) ?? [])
  const subscriptions = Object.fromEntries(Object.keys(SUBSCRIPTION_LABELS).map(event => [event, subscriptionEvents.has(event)]))
  useEffect(() => { ref.current?.querySelector<HTMLInputElement>('.lp-project-context__search input')?.focus() }, [])
  useEffect(() => setNestedQuery(''), [nested])
  useDismissibleLayer({ open: true, refs: [ref], onDismiss: onClose })
  useDismissibleLayer({ open: nested !== null, refs: [nestedRef], onDismiss: () => setNested(null) })

  const items: ProjectContextItem[][] = [
    [
      { label: 'Status', icon: <Box/>, kind: 'status', shortcut: 'P then S' },
      { label: 'Priority', icon: <LayoutGrid/>, kind: 'priority', shortcut: 'P then P' },
      { label: 'Project lead', icon: <UserRound/>, kind: 'lead', shortcut: 'P then A' },
      { label: 'Members', icon: <MembersIcon/>, kind: 'members', shortcut: 'P then M' },
      { label: 'Start date…', icon: <CalendarPlus/>, date: 'startDate', shortcut: 'Ctrl ⌥ S' },
      { label: 'Target date…', icon: <CalendarPlus/>, date: 'targetDate', shortcut: 'Ctrl ⌥ D' },
      { label: 'Labels', icon: <Tag/>, kind: 'labels', shortcut: 'P then L' },
      { label: 'More properties', icon: <Package/>, kind: 'more-menu' },
    ],
    [{ label: 'Copy', icon: <Clipboard/>, kind: 'copy-menu' }, { label: 'Move', icon: <Move/>, kind: 'move-menu' }],
    [{ label: favorite ? 'Unfavorite' : 'Favorite', icon: <Star/>, shortcut: '⌥ F' }, { label: 'Subscribe', icon: <Bell/>, kind: 'subscribe-menu' }, { label: 'Remind me', icon: <Bell/>, kind: 'remind-menu', shortcut: '⇧ H' }],
    [{ label: 'New comment…', icon: <MessageCirclePlus/>, action: 'comment', shortcut: 'N then C' }],
    [{ label: 'Delete', icon: <Trash2/>, action: 'delete', danger: true }],
  ]
  const normalized = query.trim().toLowerCase()
  const left = Math.max(8, Math.min(point.x, window.innerWidth - 244))
  const top = Math.max(8, Math.min(point.y, window.innerHeight - 536))

  const invoke = (item: ProjectContextItem) => {
    if (item.kind) { setNested(item.kind); return }
    if (item.label === 'Favorite' || item.label === 'Unfavorite') {
      void integration?.onFavoriteChange(project.id, !favorite)
      onClose()
      return
    }
    if (item.action) onAction(item.action)
  }

  return <div className="lp-project-context" ref={ref} role="menu" style={{ left, top }} onKeyDown={event => menuKeyboard(event, () => setNested(null))}>
    <label className="lp-project-context__search"><Search size={13}/><input aria-label="Filter…" onChange={event => setQuery(event.target.value)} placeholder="Filter…" value={query}/></label>
    {items.map((group, index) => {
      const filtered = group.filter(item => item.label.toLowerCase().includes(normalized))
      if (!filtered.length) return null
      return <div key={index} role="group">{filtered.map(item => item.date ? <ProjectDatePicker
        buttonClassName="lp-project-context__item"
        displayValue={item.date === 'startDate' ? project.startDate : project.targetDate}
        key={item.label}
        label={item.date === 'startDate' ? 'Start date' : 'Target date'}
        onChange={value => { onPropertyChange(item.date!, value); onClose() }}
        portalled={false}
        value={item.date === 'startDate' ? project.rawStartDate : project.rawTargetDate}
      ><ContextItemContent item={item}/></ProjectDatePicker> : <button
        aria-haspopup={item.kind ? 'menu' : undefined}
        className={`lp-project-context__item ${item.danger ? 'is-danger' : ''}`}
        key={item.label}
        onClick={() => invoke(item)}
        onMouseEnter={() => setNested(item.kind ?? null)}
        role="menuitem"
        type="button"
      ><ContextItemContent item={item}/></button>)}</div>
    })}
    {nested && <div className="lp-project-context__nested" onKeyDown={event => menuKeyboard(event, () => setNested(null))} ref={nestedRef} role="menu">
      <ProjectContextSubmenu
        kind={nested}
        manualOrdering={manualOrdering}
        onAction={onAction}
        onClose={onClose}
        onPropertyChange={onPropertyChange}
        options={options}
        project={project}
        query={nestedQuery}
        setQuery={setNestedQuery}
        setSubscriptions={next => { void integration?.onSubscriptionEventsChange(project.id, Object.entries(next).filter(([, enabled]) => enabled).map(([event]) => event)) }}
        onCreateReminder={integration ? remindAt => integration.onCreateReminder(project.id, remindAt) : undefined}
        subscriptions={subscriptions}
      />
    </div>}
  </div>
}

function ContextItemContent({ item }: { item: ProjectContextItem }) {
  return <><span className="lp-project-context__icon">{item.icon}</span><span className="lp-project-context__label">{item.label}</span>{item.shortcut && <kbd>{item.shortcut}</kbd>}{(item.kind || item.date) && <ChevronRightIcon />}</>
}

function ProjectContextSubmenu({ kind, manualOrdering, onAction, onClose, onPropertyChange, options, project, query, setQuery, setSubscriptions, subscriptions, onCreateReminder }: {
  kind: ProjectContextKind
  manualOrdering: boolean
  onAction: (action: ProjectAction) => void
  onClose: () => void
  onPropertyChange: (property: ProjectProperty, value: string) => void
  options?: ProjectPropertyOptions
  project: ProjectPageItem
  query: string
  setQuery: (value: string) => void
  setSubscriptions: (value: Record<string, boolean>) => void
  subscriptions: Record<string, boolean>
  onCreateReminder?: (remindAt: string) => Promise<unknown>
}) {
  if (kind === 'copy-menu') return <SimpleSubmenu searchable items={[
    { icon: <Link2/>, label: 'Copy URL', shortcut: '⌘ ⇧ ,' },
    { icon: <Clipboard/>, label: 'Copy title', shortcut: "⌘ ⇧ '" },
    { icon: <Link2/>, label: 'Copy title as link', shortcut: '⌘ C' },
    { icon: <FileText/>, label: 'Copy overview as Markdown', shortcut: '⌘ ⌥ C' },
    { icon: <MessageCirclePlus/>, label: 'Copy latest project update' },
  ]} onChoose={label => { void copyProjectValue(project, label); onClose() }} query={query} setQuery={setQuery}/>

  if (kind === 'move-menu') return <SimpleSubmenu searchable items={[{ icon: <Move/>, label: 'Move down', shortcut: '⌥ ↓', disabled: !manualOrdering }, { icon: <Move/>, label: 'Move to bottom', shortcut: '⌥ ⇧ ↓', disabled: !manualOrdering }]} onChoose={label => { onAction(label === 'Move down' ? 'moveDown' : 'moveBottom'); onClose() }} query={query} setQuery={setQuery}/>

  if (kind === 'subscribe-menu') return <SimpleSubmenu searchable items={Object.entries(SUBSCRIPTION_LABELS).map(([id, label]) => ({ checked: subscriptions[id], id, label }))} onChoose={(_label, id) => setSubscriptions({ ...subscriptions, [id]: !subscriptions[id] })} query={query} setQuery={setQuery}/>

  if (kind === 'remind-menu') return <SimpleSubmenu searchable items={reminderChoices()} onChoose={(_label, id) => { void onCreateReminder?.(reminderTimestamp(id)); onClose() }} query={query} setQuery={setQuery}/>

  if (kind === 'more-menu') return <SimpleSubmenu searchable items={[
    { label: 'Initiatives', shortcut: 'P then N' }, { label: 'Dependencies' }, { label: 'Add customer request…', shortcut: 'Ctrl R' }, { label: 'Change update schedule…' }, { label: 'Configure Slack notifications…', disabled: true }, { label: 'Rename…', shortcut: '⇧ R' },
  ]} onChoose={label => {
    const action = ({ 'Initiatives': 'initiatives', 'Dependencies': 'dependencies', 'Add customer request…': 'customerRequest', 'Change update schedule…': 'schedule', 'Rename…': 'rename' } as Partial<Record<string, ProjectAction>>)[label]
    if (action) onAction(action)
    onClose()
  }} query={query} setQuery={setQuery}/>

  const property = kind as ProjectProperty
  const propertyOptions = options?.[property] ?? PROPERTY_OPTIONS[property]
  const multiple = property === 'members' || property === 'labels'
  const optionValues = new Set(propertyOptions.map(option => option.value))
  const selected = new Set(property === 'members' ? project.memberIds ?? [] : property === 'labels' ? (project.labelIds ?? []).filter(id => optionValues.has(id)) : [contextPropertyValue(project, property)])
  const filtered = propertyOptions.filter(option => `${option.label} ${option.keywords ?? ''}`.toLowerCase().includes(query.trim().toLowerCase()))
  const sections = property === 'labels' ? groupProjectOptions(filtered) : [{ id: 'all', options: filtered }]
  return <>
    <label className="lp-project-context__nested-search"><Search size={13}/><input autoFocus aria-label={contextSearchPlaceholder(property)} onChange={event => setQuery(event.target.value)} placeholder={contextSearchPlaceholder(property)} value={query}/></label>
    <div className="lp-project-context__nested-list">{sections.map(section => <div key={section.id}>{section.label && <div className="lp-project-context__group-label">{section.label}</div>}{section.options.map(option => <button aria-checked={selected.has(option.value)} key={option.value || '__empty'} onClick={() => {
      if (multiple) {
        const next = new Set(selected)
        if (next.has(option.value)) next.delete(option.value); else next.add(option.value)
        onPropertyChange(property, [...next].join(','))
      } else {
        onPropertyChange(property, option.value)
        onClose()
      }
    }} role={multiple ? 'menuitemcheckbox' : 'menuitemradio'} type="button">
      {multiple && <span className={`lp-project-context__checkbox ${selected.has(option.value) ? 'is-checked' : ''}`}>{selected.has(option.value) && <CheckIcon/>}</span>}
      <ContextOptionIcon option={option} property={property}/><span className="lp-project-context__label">{option.label}</span>{!multiple && selected.has(option.value) && <CheckIcon/>}
    </button>)}</div>)}</div>
    {property === 'labels' && !filtered.length && <div className="lp-project-context__empty">{query ? `Create “${query}”` : 'Start typing to create a new label'}</div>}
  </>
}

type SimpleItem = { id?: string; label: string; icon?: ReactNode; shortcut?: string; checked?: boolean; disabled?: boolean; detail?: string }
function SimpleSubmenu({ items, onChoose, query, searchable, setQuery }: { items: SimpleItem[]; onChoose: (label: string, id: string) => void; query: string; searchable?: boolean; setQuery: (value: string) => void }) {
  const filtered = items.filter(item => item.label.toLowerCase().includes(query.trim().toLowerCase()))
  return <>{searchable && <label className="lp-project-context__nested-search"><Search size={13}/><input autoFocus aria-label="Filter…" onChange={event => setQuery(event.target.value)} placeholder="Filter…" value={query}/></label>}<div className="lp-project-context__nested-list">{filtered.map(item => <button aria-checked={item.checked} disabled={item.disabled} key={item.id ?? item.label} onClick={() => onChoose(item.label, item.id ?? item.label)} role={item.checked === undefined ? 'menuitem' : 'menuitemcheckbox'} type="button">{item.checked !== undefined && <span className={`lp-project-context__checkbox ${item.checked ? 'is-checked' : ''}`}>{item.checked && <CheckIcon/>}</span>}{item.icon && <span className="lp-project-context__icon">{item.icon}</span>}<span className="lp-project-context__label">{item.label}{item.detail && <small>{item.detail}</small>}</span>{item.shortcut && <kbd>{item.shortcut}</kbd>}</button>)}</div></>
}

function ContextOptionIcon({ option, property }: { option: ProjectPropertyOption; property: ProjectProperty }) {
  if (property === 'priority') return <DataViewPriorityIcon value={option.value as ProjectPageItem['priority']}/>
  if (property === 'lead' || property === 'members') {
    if (!option.value) return <NoAssigneeIcon size={15}/>
    if (option.avatarUrl) return <img alt="" className="lp-project-avatar" src={option.avatarUrl}/>
    return <span className="lp-project-avatar" style={{ background: '#c65b5b' }}>{initials(option.label)}</span>
  }
  if (property === 'status') return <ProjectStatusGlyph color={option.color} name={option.label} type={option.statusType}/>
  if (property === 'labels') return <span className="lp-project-context__label-dot" style={{ background: option.color ?? '#77777c' }}/>
  return <CalendarPlus size={14}/>
}

function contextPropertyValue(project: ProjectPageItem, property: ProjectProperty) {
  if (property === 'priority') return project.priority
  if (property === 'lead') return project.lead?.id ?? ''
  if (property === 'status') return project.status
  if (property === 'startDate') return project.rawStartDate ?? ''
  if (property === 'targetDate') return project.rawTargetDate ?? ''
  return project.health
}
function contextSearchPlaceholder(property: ProjectProperty) {
  if (property === 'members') return 'Change members…'
  if (property === 'labels') return 'Add labels…'
  if (property === 'lead') return 'Set lead…'
  if (property === 'priority') return 'Change priority…'
  if (property === 'status') return 'Change status…'
  return 'Filter…'
}

function groupProjectOptions(options: ProjectPropertyOption[]) {
  const sections: { id: string; label?: string; options: ProjectPropertyOption[] }[] = []
  const indexes = new Map<string, number>()
  for (const option of options) {
    const id = option.group || 'ungrouped'
    let index = indexes.get(id)
    if (index === undefined) {
      index = sections.length
      indexes.set(id, index)
      sections.push({ id, label: id === 'ungrouped' ? undefined : option.group, options: [] })
    }
    sections[index].options.push(option)
  }
  return sections
}

function menuKeyboard(event: KeyboardEvent<HTMLDivElement>, closeNested: () => void) {
  if ((event.target as HTMLElement).tagName === 'INPUT' && !['ArrowDown', 'ArrowUp', 'Escape'].includes(event.key)) return
  const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>(':scope > div > button:not(:disabled), :scope > button:not(:disabled)')]
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    const delta = event.key === 'ArrowDown' ? 1 : -1
    buttons[(index + delta + buttons.length) % buttons.length]?.focus()
  } else if (event.key === 'Home' || event.key === 'End') {
    event.preventDefault(); buttons[event.key === 'Home' ? 0 : buttons.length - 1]?.focus()
  } else if (event.key === 'ArrowRight') {
    const active = document.activeElement as HTMLButtonElement
    if (active?.getAttribute('aria-haspopup') === 'menu') { event.preventDefault(); active.click() }
  } else if (event.key === 'ArrowLeft') {
    event.preventDefault(); closeNested()
  }
}

const SUBSCRIPTION_LABELS: Record<string, string> = {
  issueAdded: 'An issue is added to the project',
  issueCompleted: 'An issue is marked completed or canceled',
  descriptionChanged: 'Comments and changes to project description',
  customerRequest: 'A customer request is added',
  updatePosted: 'New project update is posted',
  pulse: 'Subscribe to project updates in Pulse',
}
function reminderChoices(): SimpleItem[] { return [{ id: 'hour', label: 'An hour from now', detail: formatReminder(60) }, { id: 'tomorrow', label: 'Tomorrow', detail: formatReminder(24 * 60) }, { id: 'week', label: 'Next week', detail: formatReminder(7 * 24 * 60) }, { id: 'month', label: 'A month from now', detail: formatReminder(30 * 24 * 60) }, { id: 'custom', label: 'Custom…' }] }
function reminderTimestamp(id: string) { const minutes = ({ hour: 60, tomorrow: 1440, week: 10080, month: 43200, custom: 1440 } as Record<string, number>)[id] ?? 1440; return new Date(Date.now() + minutes * 60_000).toISOString() }
function formatReminder(minutes: number) { return new Intl.DateTimeFormat('en', { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }).format(new Date(Date.now() + minutes * 60_000)) }
async function copyProjectValue(project: ProjectPageItem, label: string) {
  const url = project.href ? new URL(project.href, window.location.origin).href : window.location.href
  const value = label === 'Copy URL' ? url : label === 'Copy title' ? project.name : label === 'Copy title as link' ? `[${project.name}](${url})` : label === 'Copy overview as Markdown' ? `# ${project.name}\n\n${project.summary ?? ''}` : project.summary ?? project.name
  await navigator.clipboard?.writeText(value)
}

function DataViewProjectIcon({ color = '#8b8b90', icon }: { color?: string, icon?: string }) {
  return <span aria-hidden="true" className="lp-project-symbol"><ViewGlyph color={color} icon={icon || 'Project'}/></span>
}

function HealthIcon({ value }: { value: ProjectPageItem['health'] }) {
  return <span aria-hidden="true" className={`lp-project-health is-${value}`}><svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" strokeDasharray={value === 'no-update' ? '2 2' : undefined} /><path d="m4.2 9 2.2-2.4 2.1 1.8 3.1-3.1" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" /></svg></span>
}

function DataViewPriorityIcon({ value }: { value: ProjectPageItem['priority'] }) {
  const bars = value === 'none' ? 0 : value === 'low' ? 1 : value === 'medium' ? 2 : 3
  return <span aria-hidden="true" className={`lp-project-priority is-${value}`}><svg viewBox="0 0 16 16">{[0, 1, 2].map(index => <rect fill={index < bars ? 'currentColor' : 'currentColor'} opacity={index < bars ? 1 : .25} height={[6, 9, 12][index]} key={index} rx="1" width="3" x={1.5 + index * 5} y={[9, 6, 3][index]} />)}</svg></span>
}

function ProjectAvatar({ lead }: { lead?: ProjectPageItem['lead'] }) {
  if (!lead) return <NoAssigneeIcon aria-label="No lead" className="lp-project-avatar is-empty" size={16} />
  if (lead.avatarUrl) return <img alt={lead.name} className="lp-project-avatar" src={lead.avatarUrl} />
  return <span aria-label={lead.name} className="lp-project-avatar" style={{ backgroundColor: lead.color ?? '#c65b5b' }}>{lead.initials ?? initials(lead.name)}</span>
}

function ProjectSortIcon() {
  return <svg viewBox="0 0 16 16"><path d="M11.5361 10.2745C11.8024 10.0029 11.8249 9.56807 11.5762 9.26961C11.3275 8.97139 10.8961 8.91526 10.5811 9.12801L10.5195 9.17391L8.00001 11.2735L5.48048 9.17391L5.41895 9.12801C5.10388 8.91526 4.67252 8.97139 4.42384 9.26961C4.17512 9.56807 4.19758 10.0029 4.46387 10.2745L4.51954 10.3263L7.51954 12.8263C7.79767 13.058 8.20234 13.058 8.48048 12.8263L11.4805 10.3263L11.5361 10.2745Z"/><path d="M8.75 12.25C8.75 12.6642 8.41421 13 8 13C7.58579 13 7.25 12.6642 7.25 12.25L7.25 3.75C7.25 3.33579 7.58579 3 8 3C8.41421 3 8.75 3.33579 8.75 3.75V12.25Z"/></svg>
}

function ProjectProgressSparkline({ createdAt, progress, startDate, targetDate }: { createdAt?: string, progress: number, startDate?: string, targetDate?: string }) {
  if (!targetDate) return null
  const start = new Date(startDate ?? createdAt ?? targetDate).getTime()
  const target = new Date(targetDate).getTime()
  const now = Date.now()
  const timeline = target > start ? Math.min(1, Math.max(.12, (now - start) / (target - start))) : 1
  const elbow = Math.round(timeline * 32 * 1000) / 1000
  const completionY = Math.round((16 - Math.min(100, Math.max(0, progress)) * .16) * 10) / 10
  const expectedY = Math.round((16 - timeline * 16) * 10) / 10
  const currentMidY = Math.round((16 + completionY) / 2 * 10) / 10
  const expectedMidY = Math.round((16 + expectedY) / 2 * 10) / 10
  const currentControl = Math.round(elbow / 3 * 1000) / 1000
  const currentControlTwo = Math.round(elbow * 2 / 3 * 1000) / 1000
  const remainingControl = Math.round((elbow + (32 - elbow) / 3) * 1000) / 1000
  const remainingControlTwo = Math.round((elbow + (32 - elbow) * 2 / 3) * 1000) / 1000
  const currentPath = `M0,16C${currentControl},${currentMidY},${currentControlTwo},${completionY},${elbow},${completionY}C${remainingControl},${completionY},${remainingControlTwo},${completionY},32,${completionY}`
  const targetPath = `M0,16C${currentControl},${expectedMidY},${currentControlTwo},${expectedY},${elbow},${expectedY}C${remainingControl},${expectedY},${remainingControlTwo},0,32,0`
  return <svg aria-label="Project progress trend" className="lp-project-progress-sparkline" focusable="false" height="16" role="img" viewBox="0 0 32 16" width="32"><rect fill="transparent" height="16" width="32"/><path d={currentPath} fill="none" stroke="var(--project-progress-value)" strokeWidth="1.25"/><path d={targetPath} fill="none" stroke="var(--project-progress-target)" strokeWidth="1.25"/></svg>
}

function ProjectTimeline({ groups, onOpenProject, onUpdateProject, propertyOptions }: { groups: ProjectDataGroup[], onOpenProject?: (project: ProjectPageItem) => void, onUpdateProject?: (projectId: string, input: { startDate?: string; targetDate?: string }) => Promise<unknown>, propertyOptions?: ProjectPropertyOptions }) {
  const [drag, setDrag] = useState<{ id: string; startX: number; originalStart?: string; originalTarget?: string }>()
  const draggedRef = useRef(false)
  const rows = groups.flatMap(group => group.subgroups?.length ? group.subgroups.map(subgroup => ({ ...subgroup, name: `${group.name} / ${subgroup.name}` })) : [group])
  const allProjects = rows.flatMap(group => group.projects)
  const today = new Date()
  const datedValues = allProjects.flatMap(project => [project.rawStartDate, project.rawTargetDate]).filter((value): value is string => Boolean(value)).map(value => new Date(`${value}T00:00:00`)).filter(date => Number.isFinite(date.getTime()))
  const timelineStart = startOfTimeline(datedValues.length ? new Date(Math.min(today.getTime(), ...datedValues.map(Number))) : today)
  const furthestDate = datedValues.length ? new Date(Math.max(today.getTime(), ...datedValues.map(Number))) : today
  const timelineEnd = new Date(Math.max(addTimelineMonths(timelineStart, 6).getTime(), addTimelineMonths(startOfTimeline(furthestDate), 2).getTime()))
  const months = timelineMonths(timelineStart, timelineEnd)
  const rangeMs = Math.max(86400000, timelineEnd.getTime() - timelineStart.getTime())
  const rangeDays = Math.max(1, Math.round(rangeMs / 86400000))
  return <div aria-label="Project timeline" className="lp-project-timeline" role="grid">
    <header><span>Projects</span><div style={{ gridTemplateColumns: `repeat(${months.length}, minmax(88px, 1fr))` }}>{months.map(month => <span key={month.toISOString()}>{month.toLocaleDateString(undefined, { month: 'short', year: month.getMonth() === 0 ? 'numeric' : undefined })}</span>)}</div></header>
    <div className="lp-project-timeline__body">{rows.map(group => <section key={group.id}>
      <h2><ProjectGroupStatus color={group.color} compact name={group.name} propertyOptions={propertyOptions}/><span data-i18n-ignore>{group.name}</span><small>{projectCount(group)}</small></h2>
      <div className="lp-project-timeline__tracks">{group.projects.map((project, index) => {
        const start = project.rawStartDate ?? project.startDate
        const target = project.rawTargetDate ?? project.targetDate
        const fallbackStart = new Date(timelineStart); fallbackStart.setDate(fallbackStart.getDate() + index * 14)
        const startDate = start ? new Date(start) : fallbackStart
        const endDate = target ? new Date(target) : new Date(startDate.getTime() + 21 * 86400000)
        const startPct = Math.max(0, Math.min(96, (startDate.getTime() - timelineStart.getTime()) / rangeMs * 100))
        const widthPct = Math.max(4, Math.min(100 - startPct, (endDate.getTime() - startDate.getTime()) / rangeMs * 100))
        const isDragging = drag?.id === project.id
        return <button aria-label={`${project.name} timeline bar`} data-dragging={isDragging || undefined} key={project.id} onClick={() => { if (!draggedRef.current) onOpenProject?.(project); draggedRef.current = false }} onKeyDown={async event => { if (!onUpdateProject || !['ArrowLeft', 'ArrowRight'].includes(event.key)) return; event.preventDefault(); const days = event.key === 'ArrowLeft' ? -1 : 1; const nextStart = new Date(startDate); const nextTarget = new Date(endDate); nextStart.setDate(nextStart.getDate() + days); nextTarget.setDate(nextTarget.getDate() + days); await onUpdateProject(project.id, { startDate: nextStart.toISOString().slice(0, 10), targetDate: nextTarget.toISOString().slice(0, 10) }) }} onPointerDown={event => { if (!onUpdateProject) return; event.preventDefault(); draggedRef.current = false; event.currentTarget.setPointerCapture(event.pointerId); setDrag({ id: project.id, startX: event.clientX, originalStart: start, originalTarget: target }) }} onPointerMove={event => { if (drag?.id !== project.id) return; if (Math.abs(event.clientX - drag.startX) > 3) draggedRef.current = true; const width = event.currentTarget.parentElement?.getBoundingClientRect().width ?? 1; const days = Math.round((event.clientX - drag.startX) / Math.max(width, 1) * rangeDays); event.currentTarget.style.setProperty('--timeline-drag-offset', `${days / rangeDays * 100}%`) }} onPointerCancel={event => { if (drag?.id !== project.id) return; draggedRef.current = false; setDrag(undefined); event.currentTarget.style.removeProperty('--timeline-drag-offset') }} onPointerUp={async event => { if (drag?.id !== project.id) return; const width = event.currentTarget.parentElement?.getBoundingClientRect().width ?? 1; const days = Math.round((event.clientX - drag.startX) / Math.max(width, 1) * rangeDays); const baseStart = drag.originalStart ? new Date(drag.originalStart) : startDate; const baseTarget = drag.originalTarget ? new Date(drag.originalTarget) : endDate; baseStart.setDate(baseStart.getDate() + days); baseTarget.setDate(baseTarget.getDate() + days); setDrag(undefined); event.currentTarget.style.removeProperty('--timeline-drag-offset'); if (days !== 0) await onUpdateProject?.(project.id, { startDate: baseStart.toISOString().slice(0, 10), targetDate: baseTarget.toISOString().slice(0, 10) }) }} style={{ '--timeline-start': `${startPct}%`, '--timeline-width': `${widthPct}%` } as CSSProperties} type="button"><span>{project.name}</span></button>
      })}</div>
    </section>)}</div>
  </div>
}

function startOfTimeline(value: Date) { return new Date(value.getFullYear(), value.getMonth(), 1) }
function addTimelineMonths(value: Date, count: number) { return new Date(value.getFullYear(), value.getMonth() + count, 1) }
function timelineMonths(start: Date, end: Date) { const values: Date[] = []; for (let cursor = new Date(start); cursor < end && values.length < 24; cursor = addTimelineMonths(cursor, 1)) values.push(cursor); return values }

function ProjectGroupStatus({ color = '#77777c', compact = false, name, propertyOptions }: { color?: string, compact?: boolean, name: string, propertyOptions?: ProjectPropertyOptions }) {
  const status = (propertyOptions?.status ?? PROPERTY_OPTIONS.status).find(option => option.value.toLocaleLowerCase() === name.toLocaleLowerCase())
  if (status) return <span aria-hidden="true" className={`lp-project-group__status-icon${compact ? ' is-compact' : ''}`}><ProjectStatusGlyph color={status.color ?? color} name={status.label} type={status.statusType}/></span>
  return <span aria-hidden="true" className={`lp-project-group__status${compact ? ' is-compact' : ''}`} style={{ '--project-status-color': color } as CSSProperties}/>
}

function projectGroupStatusType(name: string, propertyOptions?: ProjectPropertyOptions) {
  const option = propertyOptions?.status?.find(item => item.value.toLocaleLowerCase() === name.toLocaleLowerCase())
  const normalized = `${option?.statusType ?? ''} ${name}`.toLocaleLowerCase()
  if (normalized.includes('backlog')) return 'backlog'
  if (normalized.includes('progress') || normalized.includes('started')) return 'started'
  if (normalized.includes('complete')) return 'completed'
  if (normalized.includes('cancel')) return 'canceled'
  return undefined
}

function projectCount(group: ProjectDataGroup): number {
  return group.subgroups?.reduce((total, subgroup) => total + projectCount(subgroup), 0) ?? group.projects.length
}

function groupHasProjects(group: ProjectDataGroup) { return projectCount(group) > 0 }

function projectCreateStatus(groupName: string) {
  return ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled'].includes(groupName) ? groupName : 'Backlog'
}

function ProjectsLoadingState({ layout }: { layout: 'list' | 'board' | 'timeline' }) {
  return <div aria-busy="true" aria-label="Loading projects" className={`lp-project-state lp-project-state--loading is-${layout}`}>{Array.from({ length: layout === 'board' ? 6 : 8 }, (_, index) => <span key={index} />)}</div>
}

function ProjectsEmptyState({ onCreate }: { onCreate: () => void }) {
  return <div className="lp-project-state lp-project-state--message"><DataViewProjectIcon /><h2>No projects</h2><button onClick={onCreate} type="button"><PlusIcon /> New project</button></div>
}

function ProjectsErrorState({ error, onRetry }: { error: string, onRetry?: () => void }) {
  return <div className="lp-project-state lp-project-state--message" role="alert"><span className="lp-project-state__error">!</span><h2>Projects couldn't load</h2><p>{error}</p>{onRetry && <button onClick={onRetry} type="button">Try again</button>}</div>
}

function stopPropagation(event: MouseEvent<HTMLElement> | ChangeEvent<HTMLInputElement>) {
  event.preventDefault()
  event.stopPropagation()
}

function initials(name: string) {
  return name.split(/\s|@/).filter(Boolean).slice(0, 2).map(part => part[0]?.toUpperCase()).join('')
}

function compactAge(value?: string) {
  if (!value) return 'now'
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime())
  const days = Math.floor(elapsed / 86_400_000)
  if (days < 1) return 'now'
  if (days < 30) return `${days}d`
  if (days < 365) return `${Math.floor(days / 30)}mo`
  return `${Math.floor(days / 365)}y`
}

function priorityText(priority: ProjectPageItem['priority']) {
  return priority === 'none' ? 'No' : priority[0].toUpperCase() + priority.slice(1)
}

function healthText(health: ProjectPageItem['health']) {
  return ({ 'on-track': 'On track', 'at-risk': 'At risk', 'off-track': 'Off track', 'no-update': 'No updates' })[health]
}

function projectGrid(visible: Set<string>, labelGroupProperties: Array<{ id: string; name: string }>) {
  return [
    '8px',
    '18px',
    'minmax(300px, 1fr)',
    visible.has('Health') ? '130px' : '0px',
    visible.has('Priority') ? '68px' : '0px',
    visible.has('Lead') ? '48px' : '0px',
    visible.has('Initiatives') ? '120px' : '0px',
    visible.has('Target date') ? '92px' : '0px',
    visible.has('Issues') ? '49px' : '0px',
    visible.has('Status') ? '120px' : '0px',
    ...labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).map(() => '120px'),
    '8px',
  ].join(' ')
}
