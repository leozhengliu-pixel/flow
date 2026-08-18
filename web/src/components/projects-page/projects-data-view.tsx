import * as Tooltip from '@radix-ui/react-tooltip'
import { Bell, Box, CalendarPlus, Clipboard, FileText, LayoutGrid, Link2, MessageCirclePlus, Move, Package, Search, Star, Tag, Trash2, UserRound } from 'lucide-react'
import { useEffect, useRef, useState, type ChangeEvent, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { MembersIcon, NoAssigneeIcon } from '@/components/issue/issue-icons'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import { CheckIcon, ChevronRightIcon, PlusIcon } from './projects-page-icons'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import { ProjectPropertyPicker, ProjectStatusGlyph, type ProjectPropertyOption } from './project-property-picker'
import { ProjectDatePicker, ProjectTargetDatePicker } from './project-target-date-picker'
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
  lead?: { id: string, name: string, initials?: string, avatarUrl?: string, color?: string }
  targetDate?: string
  issueCount: number
  progress: number
  status: string
  team?: { id: string, name: string }
  memberIds?: string[]
  labelIds?: string[]
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
  propertyOptions?: ProjectPropertyOptions
  onCreateProject?: (status: string) => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenProjectIssues?: (project: ProjectPageItem) => void
  onOpenProjectUpdates?: (project: ProjectPageItem) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onRetry?: () => void
  onSelectionChange?: (ids: string[]) => void
  onSort?: (column: ProjectSortColumn, direction: 'asc' | 'desc') => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  sort?: { column: ProjectSortColumn, direction: 'asc' | 'desc' }
}

export type ProjectProperty = 'health' | 'priority' | 'lead' | 'members' | 'labels' | 'startDate' | 'targetDate' | 'status'
export type ProjectSortColumn = 'name' | 'health' | 'priority' | 'targetDate' | 'status'
export type ProjectAction = 'copy' | 'move' | 'favorite' | 'subscribe' | 'comment' | 'delete' | 'rename' | 'initiatives' | 'dependencies' | 'schedule' | 'customerRequest'
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
    {groups.map(group => <ProjectBoardColumn
      group={group}
      key={group.id}
      onCreateProject={onCreateProject}
      onOpenProject={onOpenProject}
      onOpenProjectIssues={onOpenProjectIssues}
      onOpenProjectUpdates={onOpenProjectUpdates}
      onProjectAction={onProjectAction}
      onProjectVisualChange={onProjectVisualChange}
      onPropertyChange={onPropertyChange}
      propertyOptions={propertyOptions}
      visible={visible}
    />)}
  </div>

  if (layout === 'timeline') return <ProjectTimeline groups={groups} onOpenProject={onOpenProject} />

  return <div className="lp-project-table" role="grid" style={{ '--lp-project-grid': projectGrid(visible) } as CSSProperties}>
    <ProjectTableHeader sort={sort} onSort={changeSort} visible={visible} />
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
        />
        {!isCollapsed && (group.subgroups?.length ? group.subgroups.map(subgroup => <ProjectSubgroup
          group={subgroup}
          key={subgroup.id}
          onOpen={onOpenProject}
          onOpenIssues={onOpenProjectIssues}
          onOpenUpdates={onOpenProjectUpdates}
          onProjectAction={onProjectAction}
          onProjectVisualChange={onProjectVisualChange}
          onPropertyChange={onPropertyChange}
          onSelect={toggleSelection}
          propertyOptions={propertyOptions}
          selectedIds={selectedIds}
          visible={visible}
        />) : group.projects.map(project => <ProjectListRow
          key={project.id}
          onOpen={onOpenProject}
          onOpenIssues={onOpenProjectIssues}
          onOpenUpdates={onOpenProjectUpdates}
          onProjectAction={onProjectAction}
          onProjectVisualChange={onProjectVisualChange}
          onPropertyChange={onPropertyChange}
          onSelect={toggleSelection}
          propertyOptions={propertyOptions}
          project={project}
          selected={selectedIds.includes(project.id)}
          visible={visible}
        />))}
      </section>
    })}
  </div>
}

function ProjectSubgroup({ group, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, onSelect, propertyOptions, selectedIds, visible }: {
  group: ProjectDataGroup
  onOpen?: (project: ProjectPageItem) => void
  onOpenIssues?: (project: ProjectPageItem) => void
  onOpenUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onSelect: (id: string, range?: boolean) => void
  propertyOptions?: ProjectPropertyOptions
  selectedIds: string[]
  visible: Set<string>
}) {
  return <div className="lp-project-subgroup">
    <div className="lp-project-subgroup__header"><span className="lp-project-group__status" style={{ '--project-status-color': group.color ?? '#77777c' } as CSSProperties} /><span>{group.name}</span><small>{projectCount(group)}</small></div>
    {group.projects.map(project => <ProjectListRow
      key={project.id}
      onOpen={onOpen}
      onOpenIssues={onOpenIssues}
      onOpenUpdates={onOpenUpdates}
      onProjectAction={onProjectAction}
      onProjectVisualChange={onProjectVisualChange}
      onPropertyChange={onPropertyChange}
      onSelect={onSelect}
      propertyOptions={propertyOptions}
      project={project}
      selected={selectedIds.includes(project.id)}
      visible={visible}
    />)}
  </div>
}

function ProjectTableHeader({ sort, onSort, visible }: { sort: { column: ProjectSortColumn, direction: 'asc' | 'desc' }, onSort: (column: ProjectSortColumn) => void, visible: Set<string> }) {
  const header = (column: ProjectSortColumn, label: string) => <button
    aria-label={`${sort.column === column ? sort.direction === 'asc' ? 'Z–A' : 'A–Z' : 'Order by'} ${label}`}
    className={sort.column === column ? 'is-sorted' : ''}
    onClick={() => onSort(column)}
    type="button"
  ><span>{label}</span>{sort.column === column && <span aria-hidden="true">{sort.direction === 'asc' ? '↑' : '↓'}</span>}</button>
  return <div className="lp-project-table__header" role="row">
    <span />
    <span />
    <div role="columnheader">{header('name', 'Name')}</div>
    <div hidden={!visible.has('Health')} role="columnheader">{header('health', 'Health')}</div>
    <div hidden={!visible.has('Priority')} role="columnheader">{header('priority', 'Priority')}</div>
    <div hidden={!visible.has('Lead')} role="columnheader"><span>Lead</span></div>
    <div hidden={!visible.has('Target date')} role="columnheader">{header('targetDate', 'Target date')}</div>
    <div hidden={!visible.has('Issues')} role="columnheader"><span>Issues</span></div>
    <div hidden={!visible.has('Status')} role="columnheader">{header('status', 'Status')}</div>
    <span />
  </div>
}

function ProjectGroupHeader({ collapsed, color = '#d6b326', count, name, onCreate, onToggle }: {
  collapsed: boolean
  color?: string
  count: number
  name: string
  onCreate: () => void
  onToggle: () => void
}) {
  return <div className="lp-project-group__header" role="row">
    <span />
    <button aria-expanded={!collapsed} aria-label={collapsed ? 'Expand group' : 'Collapse group'} className="lp-project-group__toggle" onClick={onToggle} type="button"><ChevronRightIcon /></button>
    <span className="lp-project-group__title"><span className="lp-project-group__status" style={{ '--project-status-color': color } as CSSProperties} /><strong>{name}</strong><span aria-label="Project count" className="lp-project-group__count">{count}</span></span>
    <button aria-label="Create new project" className="lp-project-group__create" onClick={onCreate} type="button"><PlusIcon /></button>
  </div>
}

function ProjectListRow({ project, selected, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, onSelect, propertyOptions, visible }: {
  project: ProjectPageItem
  selected: boolean
  onOpen?: (project: ProjectPageItem) => void
  onOpenIssues?: (project: ProjectPageItem) => void
  onOpenUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onSelect: (id: string, range?: boolean) => void
  propertyOptions?: ProjectPropertyOptions
  visible: Set<string>
}) {
  const [menuPoint, setMenuPoint] = useState<{ x: number, y: number } | null>(null)
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
      onClick={event => { if ((event.target as Element).closest('button,input,label')) { event.preventDefault(); return } if (onOpen && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) { event.preventDefault(); onOpen(project) } }}
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
      <div hidden={!visible.has('Health')} role="gridcell"><button aria-label={project.healthLabel ?? `${healthText(project.health)}. Click to open updates.`} className="lp-project-row__health" onClick={event => { stopPropagation(event); onOpenUpdates?.(project) }} type="button"><HealthIcon value={project.health} /><span>{healthText(project.health)}</span>{project.health !== 'no-update' && <small>· {compactAge(project.updatedAt)}</small>}</button></div>
      <div hidden={!visible.has('Priority')} role="gridcell"><ProjectPropertyPicker label={`${priorityText(project.priority)} Priority`} onChange={value => onPropertyChange?.(project, 'priority', value)} options={propertyOptions?.priority ?? PROPERTY_OPTIONS.priority} property="priority" value={project.priority}><PriorityIcon value={project.priority} /></ProjectPropertyPicker></div>
      <div className={`lp-project-row__lead ${project.lead ? '' : 'is-empty'}`} hidden={!visible.has('Lead')} role="gridcell"><LeadPropertyButton lead={project.lead} onChange={value => onPropertyChange?.(project, 'lead', value)} options={propertyOptions?.lead} /></div>
      <div hidden={!visible.has('Target date')} role="gridcell"><ProjectTargetDatePicker buttonClassName="lp-project-row__date" displayValue={project.targetDate} onChange={value => onPropertyChange?.(project, 'targetDate', value)} value={project.rawTargetDate}>{project.targetDate || <span className="lp-project-row__date-placeholder">Set date</span>}</ProjectTargetDatePicker></div>
      <div hidden={!visible.has('Issues')} role="gridcell"><button aria-label={`Open ${project.name} issues`} className="lp-project-row__issues" onClick={event => { stopPropagation(event); onOpenIssues?.(project) }} type="button">{project.issueCount}</button></div>
      <div hidden={!visible.has('Status')} role="gridcell"><ProjectPropertyPicker buttonClassName="lp-project-row__progress" label={`${project.progress}%`} onChange={value => onPropertyChange?.(project, 'status', value)} options={propertyOptions?.status ?? PROPERTY_OPTIONS.status} property="status" value={project.status}><ProgressIcon progress={project.progress} /><span>{project.progress}%</span><ProgressBar progress={project.progress} /></ProjectPropertyPicker></div>
      <span />
    </a>
    {menuPoint && <ProjectContextMenu options={propertyOptions} project={project} onPropertyChange={(property, value) => onPropertyChange?.(project, property, value)} onAction={action => {
      setMenuPoint(null)
      onProjectAction?.(project, action)
    }} onClose={() => setMenuPoint(null)} point={menuPoint} />}
  </>
}

function ProjectBoardColumn({ group, onCreateProject, onOpenProject, onOpenProjectIssues, onOpenProjectUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, propertyOptions, visible }: {
  group: ProjectsDataViewProps['groups'][number]
  onCreateProject?: (status: string) => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenProjectIssues?: (project: ProjectPageItem) => void
  onOpenProjectUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  propertyOptions?: ProjectPropertyOptions
  visible: Set<string>
}) {
  return <section aria-label={group.name} className="lp-project-board__column">
    <header><span className="lp-project-group__status" style={{ '--project-status-color': group.color ?? '#d6b326' } as CSSProperties} /><strong>{group.name}</strong><span>{projectCount(group)}</span><button aria-label="Open menu" type="button">...</button><button aria-label="Create new project" onClick={() => onCreateProject?.(projectCreateStatus(group.name))} type="button"><PlusIcon /></button></header>
    <div className="lp-project-board__cards">
      {group.subgroups?.map(subgroup => <section className="lp-project-board__subgroup" key={subgroup.id}><header><span>{subgroup.name}</span><small>{projectCount(subgroup)}</small></header>{subgroup.projects.map(project => <ProjectBoardCard key={project.id} onOpen={onOpenProject} onOpenIssues={onOpenProjectIssues} onOpenUpdates={onOpenProjectUpdates} onProjectAction={onProjectAction} onProjectVisualChange={onProjectVisualChange} onPropertyChange={onPropertyChange} project={project} propertyOptions={propertyOptions} visible={visible} />)}</section>)}
      {!group.subgroups?.length && group.projects.map(project => <ProjectBoardCard key={project.id} onOpen={onOpenProject} onOpenIssues={onOpenProjectIssues} onOpenUpdates={onOpenProjectUpdates} onProjectAction={onProjectAction} onProjectVisualChange={onProjectVisualChange} onPropertyChange={onPropertyChange} project={project} propertyOptions={propertyOptions} visible={visible} />)}
      <button className="lp-project-board__add" onClick={() => onCreateProject?.(projectCreateStatus(group.name))} type="button"><PlusIcon /> Add new project</button>
    </div>
  </section>
}

function ProjectBoardCard({ project, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, propertyOptions, visible }: {
  project: ProjectPageItem
  onOpen?: (project: ProjectPageItem) => void
  onOpenIssues?: (project: ProjectPageItem) => void
  onOpenUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  propertyOptions?: ProjectPropertyOptions
  visible: Set<string>
}) {
  const [menuPoint, setMenuPoint] = useState<{ x: number, y: number } | null>(null)
  return <>
    <a
      aria-label={project.name}
      className="lp-project-card"
      href={project.href}
      onClick={event => { if ((event.target as Element).closest('button,input,label')) { event.preventDefault(); return } if (onOpen && !event.metaKey && !event.ctrlKey && !event.shiftKey && event.button === 0) { event.preventDefault(); onOpen(project) } }}
      onContextMenu={event => {
        event.preventDefault()
        setMenuPoint({ x: event.clientX, y: event.clientY })
      }}
      onKeyDown={event => event.target === event.currentTarget && event.key === 'Enter' && onOpen?.(project)}
      role="button"
      tabIndex={0}
    >
      <div className="lp-project-card__top"><span className="lp-project-card__identity"><ViewIconPicker color={project.color} icon={project.icon || 'Project'} onChange={visual => onProjectVisualChange?.(project, visual.icon, visual.color)} triggerClassName="lp-project-row__project-icon"/><strong>{project.name}</strong></span><span className="lp-project-card__properties">
        {visible.has('Health') && <button aria-label={project.healthLabel ?? `${healthText(project.health)}. Click to open updates.`} className="lp-project-property-trigger" onClick={event => { stopPropagation(event); onOpenUpdates?.(project) }} type="button"><HealthIcon value={project.health} /></button>}
        {visible.has('Priority') && <ProjectPropertyPicker label={`${priorityText(project.priority)} Priority`} onChange={value => onPropertyChange?.(project, 'priority', value)} options={propertyOptions?.priority ?? PROPERTY_OPTIONS.priority} property="priority" value={project.priority}><PriorityIcon value={project.priority} /></ProjectPropertyPicker>}
        {visible.has('Lead') && <span className={`lp-project-card__lead ${project.lead ? '' : 'is-empty'}`}><LeadPropertyButton lead={project.lead} onChange={value => onPropertyChange?.(project, 'lead', value)} options={propertyOptions?.lead} /></span>}
      </span></div>
      {visible.has('Summary') && project.summary && <p>{project.summary}</p>}
      <div className="lp-project-card__bottom">
        {visible.has('Target date') && <ProjectTargetDatePicker displayValue={project.targetDate} onChange={value => onPropertyChange?.(project, 'targetDate', value)} value={project.rawTargetDate}>{project.targetDate}</ProjectTargetDatePicker>}
        {project.milestone && <button className="lp-project-card__milestone" onClick={stopPropagation} type="button"><span />{project.milestone}</button>}
        {visible.has('Issues') && <button className="lp-project-card__issues" onClick={event => { stopPropagation(event); onOpenIssues?.(project) }} type="button">{project.issueCount} issues</button>}
      </div>
    </a>
    {menuPoint && <ProjectContextMenu options={propertyOptions} project={project} onPropertyChange={(property, value) => onPropertyChange?.(project, property, value)} onAction={action => {
      setMenuPoint(null)
      onProjectAction?.(project, action)
    }} onClose={() => setMenuPoint(null)} point={menuPoint} />}
  </>
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

function ProjectContextMenu({ point, onAction, onClose, onPropertyChange, options, project }: { point: { x: number, y: number }, onAction: (action: ProjectAction) => void, onClose: () => void, onPropertyChange: (property: ProjectProperty, value: string) => void, options?: ProjectPropertyOptions, project: ProjectPageItem }) {
  const ref = useRef<HTMLDivElement>(null)
  const nestedRef = useRef<HTMLDivElement>(null)
  const [nested, setNested] = useState<ProjectContextKind | null>(null)
  const [query, setQuery] = useState('')
  const [nestedQuery, setNestedQuery] = useState('')
  const [favorite, setFavorite] = useState(() => readStoredBoolean(`flow:project:${project.id}:favorite`, false))
  const [subscriptions, setSubscriptions] = useState<Record<string, boolean>>(() => readStoredRecord(`flow:project:${project.id}:subscriptions`, DEFAULT_SUBSCRIPTIONS))
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
      const next = !favorite
      setFavorite(next)
      localStorage.setItem(`flow:project:${project.id}:favorite`, JSON.stringify(next))
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
        onAction={onAction}
        onClose={onClose}
        onPropertyChange={onPropertyChange}
        options={options}
        project={project}
        query={nestedQuery}
        setQuery={setNestedQuery}
        setSubscriptions={next => { setSubscriptions(next); localStorage.setItem(`flow:project:${project.id}:subscriptions`, JSON.stringify(next)) }}
        subscriptions={subscriptions}
      />
    </div>}
  </div>
}

function ContextItemContent({ item }: { item: ProjectContextItem }) {
  return <><span className="lp-project-context__icon">{item.icon}</span><span className="lp-project-context__label">{item.label}</span>{item.shortcut && <kbd>{item.shortcut}</kbd>}{(item.kind || item.date) && <ChevronRightIcon />}</>
}

function ProjectContextSubmenu({ kind, onAction, onClose, onPropertyChange, options, project, query, setQuery, setSubscriptions, subscriptions }: {
  kind: ProjectContextKind
  onAction: (action: ProjectAction) => void
  onClose: () => void
  onPropertyChange: (property: ProjectProperty, value: string) => void
  options?: ProjectPropertyOptions
  project: ProjectPageItem
  query: string
  setQuery: (value: string) => void
  setSubscriptions: (value: Record<string, boolean>) => void
  subscriptions: Record<string, boolean>
}) {
  if (kind === 'copy-menu') return <SimpleSubmenu searchable items={[
    { icon: <Link2/>, label: 'Copy URL', shortcut: '⌘ ⇧ ,' },
    { icon: <Clipboard/>, label: 'Copy title', shortcut: "⌘ ⇧ '" },
    { icon: <Link2/>, label: 'Copy title as link', shortcut: '⌘ C' },
    { icon: <FileText/>, label: 'Copy overview as Markdown', shortcut: '⌘ ⌥ C' },
    { icon: <MessageCirclePlus/>, label: 'Copy latest project update' },
  ]} onChoose={label => { void copyProjectValue(project, label); onClose() }} query={query} setQuery={setQuery}/>

  if (kind === 'move-menu') return <SimpleSubmenu searchable items={[{ icon: <Move/>, label: 'Move down', shortcut: '⌥ ↓', disabled: true }, { icon: <Move/>, label: 'Move to bottom', shortcut: '⌥ ⇧ ↓', disabled: true }]} onChoose={() => undefined} query={query} setQuery={setQuery}/>

  if (kind === 'subscribe-menu') return <SimpleSubmenu searchable items={Object.entries(SUBSCRIPTION_LABELS).map(([id, label]) => ({ checked: subscriptions[id], id, label }))} onChoose={(_label, id) => setSubscriptions({ ...subscriptions, [id]: !subscriptions[id] })} query={query} setQuery={setQuery}/>

  if (kind === 'remind-menu') return <SimpleSubmenu searchable items={reminderChoices()} onChoose={(_label, id) => { localStorage.setItem(`flow:project:${project.id}:reminder`, reminderTimestamp(id)); onClose() }} query={query} setQuery={setQuery}/>

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
  const selected = new Set(property === 'members' ? project.memberIds ?? [] : property === 'labels' ? project.labelIds ?? [] : [contextPropertyValue(project, property)])
  const filtered = propertyOptions.filter(option => `${option.label} ${option.keywords ?? ''}`.toLowerCase().includes(query.trim().toLowerCase()))
  return <>
    <label className="lp-project-context__nested-search"><Search size={13}/><input autoFocus aria-label={contextSearchPlaceholder(property)} onChange={event => setQuery(event.target.value)} placeholder={contextSearchPlaceholder(property)} value={query}/></label>
    <div className="lp-project-context__nested-list">{filtered.map(option => <button aria-checked={selected.has(option.value)} key={option.value || '__empty'} onClick={() => {
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
    </button>)}</div>
    {property === 'labels' && !filtered.length && <div className="lp-project-context__empty">{query ? `Create “${query}”` : 'Start typing to create a new label'}</div>}
    {property === 'members' && <button onClick={onClose} type="button"><UserRound/><span className="lp-project-context__label">Invite and add…</span></button>}
  </>
}

type SimpleItem = { id?: string; label: string; icon?: ReactNode; shortcut?: string; checked?: boolean; disabled?: boolean; detail?: string }
function SimpleSubmenu({ items, onChoose, query, searchable, setQuery }: { items: SimpleItem[]; onChoose: (label: string, id: string) => void; query: string; searchable?: boolean; setQuery: (value: string) => void }) {
  const filtered = items.filter(item => item.label.toLowerCase().includes(query.trim().toLowerCase()))
  return <>{searchable && <label className="lp-project-context__nested-search"><Search size={13}/><input autoFocus aria-label="Filter…" onChange={event => setQuery(event.target.value)} placeholder="Filter…" value={query}/></label>}<div className="lp-project-context__nested-list">{filtered.map(item => <button aria-checked={item.checked} disabled={item.disabled} key={item.id ?? item.label} onClick={() => onChoose(item.label, item.id ?? item.label)} role={item.checked === undefined ? 'menuitem' : 'menuitemcheckbox'} type="button">{item.checked !== undefined && <span className={`lp-project-context__checkbox ${item.checked ? 'is-checked' : ''}`}>{item.checked && <CheckIcon/>}</span>}{item.icon && <span className="lp-project-context__icon">{item.icon}</span>}<span className="lp-project-context__label">{item.label}{item.detail && <small>{item.detail}</small>}</span>{item.shortcut && <kbd>{item.shortcut}</kbd>}</button>)}</div></>
}

function ContextOptionIcon({ option, property }: { option: ProjectPropertyOption; property: ProjectProperty }) {
  if (property === 'priority') return <PriorityIcon value={option.value as ProjectPageItem['priority']}/>
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
const DEFAULT_SUBSCRIPTIONS = { issueAdded: false, issueCompleted: false, descriptionChanged: true, customerRequest: true, updatePosted: true, pulse: false }
function reminderChoices(): SimpleItem[] { return [{ id: 'hour', label: 'An hour from now', detail: formatReminder(60) }, { id: 'tomorrow', label: 'Tomorrow', detail: formatReminder(24 * 60) }, { id: 'week', label: 'Next week', detail: formatReminder(7 * 24 * 60) }, { id: 'month', label: 'A month from now', detail: formatReminder(30 * 24 * 60) }, { id: 'custom', label: 'Custom…' }] }
function reminderTimestamp(id: string) { const minutes = ({ hour: 60, tomorrow: 1440, week: 10080, month: 43200, custom: 1440 } as Record<string, number>)[id] ?? 1440; return new Date(Date.now() + minutes * 60_000).toISOString() }
function formatReminder(minutes: number) { return new Intl.DateTimeFormat('en', { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }).format(new Date(Date.now() + minutes * 60_000)) }
function readStoredBoolean(key: string, fallback: boolean) { try { const value = localStorage.getItem(key); return value === null ? fallback : Boolean(JSON.parse(value)) } catch { return fallback } }
function readStoredRecord(key: string, fallback: Record<string, boolean>) { try { const value = localStorage.getItem(key); return value ? { ...fallback, ...JSON.parse(value) } : fallback } catch { return fallback } }
async function copyProjectValue(project: ProjectPageItem, label: string) {
  const url = project.href ? new URL(project.href, window.location.origin).href : window.location.href
  const value = label === 'Copy URL' ? url : label === 'Copy title' ? project.name : label === 'Copy title as link' ? `[${project.name}](${url})` : label === 'Copy overview as Markdown' ? `# ${project.name}\n\n${project.summary ?? ''}` : project.summary ?? project.name
  await navigator.clipboard?.writeText(value)
}

function ProjectIcon({ color = '#8b8b90', icon }: { color?: string, icon?: string }) {
  return <span aria-hidden="true" className="lp-project-symbol"><ViewGlyph color={color} icon={icon || 'Project'}/></span>
}

function HealthIcon({ value }: { value: ProjectPageItem['health'] }) {
  return <span aria-hidden="true" className={`lp-project-health is-${value}`}><svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6.5" fill="none" stroke="currentColor" strokeDasharray={value === 'no-update' ? '2 2' : undefined} /><path d="m4.2 9 2.2-2.4 2.1 1.8 3.1-3.1" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" /></svg></span>
}

function PriorityIcon({ value }: { value: ProjectPageItem['priority'] }) {
  const bars = value === 'none' ? 0 : value === 'low' ? 1 : value === 'medium' ? 2 : 3
  return <span aria-hidden="true" className={`lp-project-priority is-${value}`}><svg viewBox="0 0 16 16">{[0, 1, 2].map(index => <rect fill={index < bars ? 'currentColor' : 'currentColor'} opacity={index < bars ? 1 : .25} height={[6, 9, 12][index]} key={index} rx="1" width="3" x={1.5 + index * 5} y={[9, 6, 3][index]} />)}</svg></span>
}

function ProjectAvatar({ lead }: { lead?: ProjectPageItem['lead'] }) {
  if (!lead) return <NoAssigneeIcon aria-label="No lead" className="lp-project-avatar is-empty" size={16} />
  if (lead.avatarUrl) return <img alt={lead.name} className="lp-project-avatar" src={lead.avatarUrl} />
  return <span aria-label={lead.name} className="lp-project-avatar" style={{ backgroundColor: lead.color ?? '#c65b5b' }}>{lead.initials ?? initials(lead.name)}</span>
}

function ProgressIcon({ progress }: { progress: number }) {
  return <svg aria-hidden="true" className="lp-project-progress-icon" viewBox="0 0 16 16"><circle cx="8" cy="8" fill="none" r="5.5" stroke="currentColor" strokeOpacity=".3" strokeWidth="1.5" /><circle cx="8" cy="8" fill="none" r="5.5" stroke="currentColor" strokeDasharray={`${Math.min(100, Math.max(0, progress))} 100`} strokeLinecap="round" strokeWidth="1.5" pathLength="100" transform="rotate(-90 8 8)" /></svg>
}

function ProgressBar({ progress }: { progress: number }) {
  return <span aria-hidden="true" className="lp-project-progress-bar"><span style={{ width: `${Math.min(100, Math.max(0, progress))}%` }} /></span>
}

function ProjectTimeline({ groups, onOpenProject }: { groups: ProjectDataGroup[], onOpenProject?: (project: ProjectPageItem) => void }) {
  const months = ['Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
  const rows = groups.flatMap(group => group.subgroups?.length ? group.subgroups.map(subgroup => ({ ...subgroup, name: `${group.name} / ${subgroup.name}` })) : [group])
  return <div aria-label="Project timeline" className="lp-project-timeline" role="grid">
    <header><span>Projects</span><div>{months.map(month => <span key={month}>{month}</span>)}</div></header>
    <div className="lp-project-timeline__body">{rows.map(group => <section key={group.id}>
      <h2><span className="lp-project-group__status" style={{ '--project-status-color': group.color ?? '#77777c' } as CSSProperties} />{group.name}<small>{projectCount(group)}</small></h2>
      <div className="lp-project-timeline__tracks">{group.projects.map((project, index) => <button key={project.id} onClick={() => onOpenProject?.(project)} style={{ '--timeline-start': `${(index * 17 + project.name.length * 5) % 67}%`, '--timeline-width': `${Math.max(19, Math.min(47, 28 + project.name.length))}%` } as CSSProperties} type="button"><span>{project.name}</span></button>)}</div>
    </section>)}</div>
  </div>
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
  return <div className="lp-project-state lp-project-state--message"><ProjectIcon /><h2>No projects</h2><button onClick={onCreate} type="button"><PlusIcon /> New project</button></div>
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

function projectGrid(visible: Set<string>) {
  return [
    '8px',
    '18px',
    'minmax(300px, 425px)',
    visible.has('Health') ? '130px' : '0px',
    visible.has('Priority') ? '68px' : '0px',
    visible.has('Lead') ? '48px' : '0px',
    visible.has('Target date') ? '92px' : '0px',
    visible.has('Issues') ? '49px' : '0px',
    visible.has('Status') ? '120px' : '0px',
    '8px',
  ].join(' ')
}
