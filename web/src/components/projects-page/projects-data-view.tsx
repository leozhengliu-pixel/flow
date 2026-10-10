import * as Tooltip from '@radix-ui/react-tooltip'
import { CustomerRevenueRowChip, CustomersRowChip } from '@/components/customer/customer-row-properties'
import { revenueSuffix } from '@/components/issue-explorer/customer-filter'
import { currencySymbol } from '@/lib/customer-settings'
import type { FeatureSettings } from '@/types/flow'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown, MoreHorizontal } from 'lucide-react'
import { useEffect, useMemo, useState, type ChangeEvent, type CSSProperties, type KeyboardEvent, type MouseEvent, type ReactElement } from 'react'
import { VirtualColumnList } from '@/components/ui/virtual-column-list'
import { CalendarIcon, NoAssigneeIcon } from '@/components/issue/issue-icons'
import { MilestoneProgressIcon } from '@/components/issue/milestone-progress-icon'
import { isMilestoneDateOverdue } from '@/components/issue/milestone-progress'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import { CheckIcon, ChevronRightIcon, PlusIcon } from './projects-page-icons'
import { ProjectPropertyPicker, ProjectStatusGlyph, type ProjectPropertyOption } from './project-property-picker'
import { ProjectTargetDatePicker } from './project-target-date-picker'
import { projectLabelGroupProperty } from './projects-display-model'
import { ProjectRowMenu } from './project-row-menu'
import { useProjectRowShortcuts } from './project-row-shortcuts'
import './projects-page.css'
import './projects-bundle-parity.css'
import { ProjectsPageEmptyIcon } from './projects-page-empty-icon'
import { ProjectTimeline } from './project-timeline'
import type { TimelineZoom } from './project-timeline-model'
import { AvatarImage } from '@/components/ui/user-avatar'

export type ProjectPageItem = {
  id: string
  /** Short project identifier shown by the "ID" display property. */
  slugId?: string
  name: string
  href?: string
  icon?: string
  color?: string
  summary?: string
  milestone?: string
  milestoneDate?: string
  milestoneProgress?: number
  rawMilestoneDate?: string
  health: 'on-track' | 'off-track' | 'at-risk' | 'no-update'
  healthLabel?: string
  priority: 'urgent' | 'high' | 'medium' | 'low' | 'none'
  position?: number
  lead?: { id: string, name: string, initials?: string, avatarUrl?: string, color?: string }
  targetDate?: string
  issueCount: number
  progress: number
  status: string
  statusId?: string
  statusType?: string
  statusColor?: string
  team?: { id: string, name: string }
  /** The project's lead team (Flow: the first of its teams), for "Only show lead team projects". */
  leadTeamId?: string
  memberIds?: string[]
  labelIds?: string[]
  initiativeNames?: string[]
  labelsByGroup?: Record<string, Array<{ id: string; name: string; color: string }>>
  teamIds?: string[]
  rawStartDate?: string
  rawTargetDate?: string
  /** Every milestone with its issue progress (0–100), for timeline diamonds. */
  milestones?: Array<{ id: string; name: string; targetDate?: string; progress: number }>
  /** Ids of projects that block this one (dependency edges point blocker → this). */
  blockedByIds?: string[]
  startDate?: string
  createdAt?: string
  updatedAt?: string
  /** Requesting customers (project requests and requests on its issues). */
  customers?: Array<{ id: string; name: string; logoUrl?: string; annualRevenue?: number }>
  customerCount?: number
  importantCustomerIds?: string[]
  customerSettings?: Partial<FeatureSettings>
  hasUnknownCustomer?: boolean
  customerIds?: string[]
  customerOwnerIds?: string[]
  customerStatuses?: string[]
  customerTiers?: string[]
  customerRevenues?: number[]
  customerSizes?: number[]
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
  grouping?: string
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
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
  timelineZoom?: TimelineZoom
  onTimelineZoomChange?: (zoom: TimelineZoom) => void
  onUpdateMilestone?: (projectId: string, milestoneId: string, input: { targetDate: string }) => Promise<unknown>
  onOpenMilestone?: (project: ProjectPageItem, milestoneId: string) => void
  onCreateProjectDependency?: (blockerId: string, blockedId: string) => Promise<unknown>
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
export type ProjectAction = 'copy' | 'move' | 'moveTop' | 'moveUp' | 'moveDown' | 'moveBottom' | 'favorite' | 'subscribe' | 'comment' | 'delete' | 'rename' | 'initiatives' | 'dependencies' | 'schedule' | 'customerRequest'
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

type ProjectListEntry =
  | { key: string; kind: 'group'; group: ProjectDataGroup; collapsed: boolean }
  | { key: string; kind: 'subgroup'; group: ProjectDataGroup }
  | { key: string; kind: 'project'; project: ProjectPageItem }

const PROJECT_VIRTUALIZATION_THRESHOLD = 80

export function ProjectsDataView({
  groups,
  layout = 'list',
  grouping,
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
  hasMore = false,
  loadingMore = false,
  onLoadMore,
  timelineZoom,
  onTimelineZoomChange,
  onUpdateMilestone,
  onOpenMilestone,
  onCreateProjectDependency,
}: ProjectsDataViewProps) {
  useProjectRowShortcuts(layout !== 'timeline')
  const [collapsed, setCollapsed] = useState<string[]>([])
  const [hiddenGroupIds, setHiddenGroupIds] = useState<string[]>([])
  const [sort, setSort] = useState<{ column: ProjectSortColumn, direction: 'asc' | 'desc' }>(externalSort ?? { column: 'name', direction: 'asc' })

  useEffect(() => {
    if (externalSort) setSort(externalSort)
  }, [externalSort])

  const selectedSet = useMemo(() => new Set(selectedIds), [selectedIds])
  const listEntries = useMemo<ProjectListEntry[]>(() => groups.flatMap(group => {
    const isCollapsed = collapsed.includes(group.id)
    const entries: ProjectListEntry[] = [{ key: `group:${group.id}`, kind: 'group', group, collapsed: isCollapsed }]
    if (isCollapsed) return entries
    if (group.subgroups?.length) {
      for (const subgroup of group.subgroups) {
        entries.push({ key: `subgroup:${group.id}:${subgroup.id}`, kind: 'subgroup', group: subgroup })
        entries.push(...subgroup.projects.map(project => ({ key: `project:${group.id}:${subgroup.id}:${project.id}`, kind: 'project' as const, project })))
      }
    } else {
      entries.push(...group.projects.map(project => ({ key: `project:${group.id}:${project.id}`, kind: 'project' as const, project })))
    }
    return entries
  }), [collapsed, groups])

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
  const defaultCreateStatus = projectCreateStatus(undefined, false, propertyOptions)
  if (layout !== 'board' && !groups.some(groupHasProjects)) return <ProjectsEmptyState onCreate={() => onCreateProject?.(defaultCreateStatus)} />
  if (layout === 'board' && !groups.length) return <ProjectsEmptyState onCreate={() => onCreateProject?.(defaultCreateStatus)} />

  const visible = new Set(visibleProperties)
  if (layout === 'board') {
    const hiddenSet = new Set(hiddenGroupIds)
    const boardGroups = groups.filter(group => !hiddenSet.has(group.id))
    const hiddenGroups = groups.filter(group => hiddenSet.has(group.id))
    return <div className="lp-project-board" role="list">
      {boardGroups.map((group, groupIndex) => <ProjectBoardColumn
        group={group}
        key={group.id}
        selectedIds={selectedIds}
        manualOrdering={manualOrdering}
        onCreateProject={onCreateProject}
        onHide={() => setHiddenGroupIds(current => current.includes(group.id) ? current : [...current, group.id])}
        onOpenProject={onOpenProject}
        onOpenProjectIssues={onOpenProjectIssues}
        onOpenProjectUpdates={onOpenProjectUpdates}
        onProjectAction={onProjectAction}
        onProjectVisualChange={onProjectVisualChange}
        onPropertyChange={onPropertyChange}
        onSelectAll={() => {
          const ids = group.subgroups?.flatMap(item => item.projects.map(project => project.id)) ?? group.projects.map(project => project.id)
          onSelectionChange?.([...new Set([...selectedIds, ...ids])])
        }}
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
          const destinationGroup = boardGroups[groupIndex + direction]
          const destination = destinationGroup && projectGroupProperty(destinationGroup)
          if (!destination || !onPropertyChange) return
          onPropertyChange(project, destination.property, destination.value)
        }}
        propertyOptions={propertyOptions}
        showStatus={grouping !== 'Status'}
        visible={visible}
      />)}
      {hiddenGroups.length > 0 && <section aria-label="Hidden columns" className="lp-project-board__hidden">
        <header><button aria-expanded="true" className="lp-project-board__hidden-label" type="button"><ChevronDown size={12}/> Hidden columns</button></header>
        <div className="lp-project-board__hidden-list">
          {hiddenGroups.map(group => <button className="lp-project-board__hidden-row" key={group.id} onClick={() => setHiddenGroupIds(current => current.filter(id => id !== group.id))} type="button">
            <ProjectGroupStatus color={group.color} name={group.name} propertyOptions={propertyOptions}/>
            <span data-i18n-ignore>{group.name}</span>
            <small>{projectCount(group)}</small>
          </button>)}
        </div>
      </section>}
      {hasMore && <button className="lp-project-board__load-more" disabled={loadingMore} onClick={onLoadMore} type="button">{loadingMore ? 'Loading…' : 'Load more projects'}</button>}
    </div>
  }

  if (layout === 'timeline') return <div className="lp-project-timeline-shell"><ProjectTimeline groupCount={projectCount} groups={groups} onCreateDependency={onCreateProjectDependency} onOpenMilestone={onOpenMilestone} onOpenProject={onOpenProject} onUpdateMilestone={onUpdateMilestone} onUpdateProject={onUpdateProject} onZoomChange={onTimelineZoomChange} renderGroupIcon={group => <ProjectGroupStatus color={group.color} compact name={group.name} propertyOptions={propertyOptions}/>} zoom={timelineZoom}/>{hasMore && <button className="lp-project-timeline__load-more" disabled={loadingMore} onClick={onLoadMore} type="button">{loadingMore ? 'Loading…' : 'Load more projects'}</button>}</div>

  const customerSettings = groups.flatMap(group => [...group.projects, ...(group.subgroups ?? []).flatMap(subgroup => subgroup.projects)]).find(project => project.customerSettings)?.customerSettings
  const renderProject = (project: ProjectPageItem) => <ProjectListRow
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
    selected={selectedSet.has(project.id)}
    visible={visible}
  />

  if (listEntries.length > PROJECT_VIRTUALIZATION_THRESHOLD) return <div className="lp-project-table is-virtual" role="grid" style={{ '--lp-project-grid': projectGrid(visible, labelGroupProperties) } as CSSProperties}>
    <VirtualColumnList
      header={<ProjectTableHeader customerSettings={customerSettings} labelGroupProperties={labelGroupProperties} sort={sort} onSort={changeSort} visible={visible}/>}
      className="lp-project-table__virtual"
      data={listEntries}
      computeItemKey={(_index, entry) => entry.key}
      increaseViewportBy={{ top: 192, bottom: 480 }}
      itemContent={(_index, entry) => {
        if (entry.kind === 'project') return renderProject(entry.project)
        if (entry.kind === 'subgroup') return <div className="lp-project-subgroup"><div className="lp-project-subgroup__header"><ProjectGroupStatus color={entry.group.color} compact name={entry.group.name} propertyOptions={propertyOptions}/><span data-i18n-ignore>{entry.group.name}</span><small>{projectCount(entry.group)}</small></div></div>
        return <ProjectGroupHeader
          collapsed={entry.collapsed}
          color={entry.group.color}
          count={projectCount(entry.group)}
          name={entry.group.name}
          onCreate={() => onCreateProject?.(projectCreateStatus(entry.group.name, grouping === 'Status', propertyOptions))}
          onToggle={() => setCollapsed(current => current.includes(entry.group.id) ? current.filter(id => id !== entry.group.id) : [...current, entry.group.id])}
          propertyOptions={propertyOptions}
        />
      }}
      endReached={() => { if (hasMore && !loadingMore) onLoadMore?.() }}
    />
  </div>

  return <div className="lp-project-table" role="grid" style={{ '--lp-project-grid': projectGrid(visible, labelGroupProperties) } as CSSProperties}>
    <ProjectTableHeader customerSettings={customerSettings} labelGroupProperties={labelGroupProperties} sort={sort} onSort={changeSort} visible={visible} />
    {groups.map(group => {
      const isCollapsed = collapsed.includes(group.id)
    return <section aria-label={group.name} className="lp-project-group" key={group.id} role="rowgroup">
        <ProjectGroupHeader
          collapsed={isCollapsed}
          color={group.color}
          count={projectCount(group)}
          name={group.name}
          onCreate={() => onCreateProject?.(projectCreateStatus(group.name, grouping === 'Status', propertyOptions))}
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
          selected={selectedSet.has(project.id)}
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

function ProjectTableHeader({ customerSettings, labelGroupProperties, sort, onSort, visible }: { customerSettings?: Partial<FeatureSettings>; labelGroupProperties: Array<{ id: string; name: string }>; sort: { column: ProjectSortColumn, direction: 'asc' | 'desc' }, onSort: (column: ProjectSortColumn) => void, visible: Set<string> }) {
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
    <div aria-hidden={!visible.has('Customers') || undefined} data-column-hidden={!visible.has('Customers') || undefined} role="columnheader"><span>Customers</span></div>
    <div aria-hidden={!visible.has('Customer revenue') || undefined} className="lp-project-table__revenue-header" data-column-hidden={!visible.has('Customer revenue') || undefined} role="columnheader"><span>Revenue</span><span className="lp-project-table__revenue-unit" data-i18n-ignore>{revenueColumnUnit(customerSettings)}</span></div>
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
  const statusOption = (propertyOptions?.status ?? PROPERTY_OPTIONS.status).find(option => option.value === project.status)
  const rowKey = (event: KeyboardEvent<HTMLAnchorElement>) => {
    if (event.target !== event.currentTarget) return
    if (event.key === 'Enter') onOpen?.(project)
    if (event.key === ' ') {
      event.preventDefault()
      onSelect(project.id, event.shiftKey)
    }
  }
  return <ProjectItemMenu manualOrdering={manualOrdering} onProjectAction={onProjectAction} onPropertyChange={onPropertyChange} options={propertyOptions} project={project} projectMenu={projectMenu}>
    <a
      aria-label={project.name}
      aria-selected={selected}
      className={`lp-project-row ${selected ? 'is-selected' : ''}`}
      data-linear-menu-row=""
      href={project.href}
      onClick={event => openProjectLink(event, project, onOpen)}
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
        <div>{visible.has('ID') && project.slugId && <span className="lp-project-row__id" data-i18n-ignore>{project.slugId}</span>}<strong>{project.name}</strong>{project.summary && <small>{project.summary}</small>}</div>
      </div>
      <div aria-hidden={!visible.has('Health') || undefined} data-column-hidden={!visible.has('Health') || undefined} role="gridcell"><button aria-label={project.healthLabel ?? `${healthText(project.health)}. Click to open updates.`} className="lp-project-row__health" onClick={event => { stopPropagation(event); onOpenUpdates?.(project) }} type="button"><HealthIcon value={project.health} /><span>{healthText(project.health)}</span>{project.health !== 'no-update' && <small>· {compactAge(project.updatedAt)}</small>}</button></div>
      <div aria-hidden={!visible.has('Priority') || undefined} data-column-hidden={!visible.has('Priority') || undefined} role="gridcell"><ProjectPropertyPicker label={`${priorityText(project.priority)} Priority`} onChange={value => onPropertyChange?.(project, 'priority', value)} options={propertyOptions?.priority ?? PROPERTY_OPTIONS.priority} property="priority" value={project.priority}><DataViewPriorityIcon value={project.priority} /></ProjectPropertyPicker></div>
      <div aria-hidden={!visible.has('Lead') || undefined} className={`lp-project-row__lead ${project.lead ? '' : 'is-empty'}`} data-column-hidden={!visible.has('Lead') || undefined} role="gridcell"><LeadPropertyButton lead={project.lead} onChange={value => onPropertyChange?.(project, 'lead', value)} options={propertyOptions?.lead} /></div>
      <div aria-hidden={!visible.has('Initiatives') || undefined} className="lp-project-row__initiatives" data-column-hidden={!visible.has('Initiatives') || undefined} role="gridcell"><span data-i18n-ignore>{project.initiativeNames?.join(', ')}</span></div>
      <div aria-hidden={!visible.has('Target date') || undefined} data-column-hidden={!visible.has('Target date') || undefined} role="gridcell"><ProjectTargetDatePicker buttonClassName="lp-project-row__date" displayValue={project.targetDate} onChange={value => onPropertyChange?.(project, 'targetDate', value)} value={project.rawTargetDate}>{project.targetDate || <span className="lp-project-row__date-placeholder">Set date</span>}</ProjectTargetDatePicker></div>
      <div aria-hidden={!visible.has('Issues') || undefined} data-column-hidden={!visible.has('Issues') || undefined} role="gridcell"><button aria-label={`Open ${project.name} issues`} className="lp-project-row__issues" onClick={event => { stopPropagation(event); onOpenIssues?.(project) }} type="button">{project.issueCount}</button></div>
      <div aria-hidden={!visible.has('Status') || undefined} className="lp-project-row__status" data-column-hidden={!visible.has('Status') || undefined} role="gridcell"><ProjectPropertyPicker buttonClassName="lp-project-row__progress" label={`${project.progress}%`} onChange={value => onPropertyChange?.(project, 'status', value)} options={propertyOptions?.status ?? PROPERTY_OPTIONS.status} property="status" value={project.status}><ProjectStatusGlyph color={statusOption?.color} name={project.status} progress={project.progress / 100} type={statusOption?.statusType}/><span>{project.progress}%</span></ProjectPropertyPicker><ProjectProgressSparkline createdAt={project.createdAt} progress={project.progress} startDate={project.rawStartDate} targetDate={project.rawTargetDate}/></div>
      <div aria-hidden={!visible.has('Customers') || undefined} className="lp-project-row__customers" data-column-hidden={!visible.has('Customers') || undefined} role="gridcell">{visible.has('Customers') && project.customerCount ? <CustomersRowChip variant="plain" customers={project.customers ?? []} customerCount={project.customerCount} importantCustomerIds={project.importantCustomerIds ?? []} settings={project.customerSettings}/> : null}</div>
      <div aria-hidden={!visible.has('Customer revenue') || undefined} className="lp-project-row__revenue" data-column-hidden={!visible.has('Customer revenue') || undefined} role="gridcell">{visible.has('Customer revenue') && project.customers?.length ? <CustomerRevenueRowChip variant="plain" customers={project.customers} settings={project.customerSettings}/> : null}</div>
      {labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).map(group => <div className="lp-project-row__label-group" data-i18n-ignore key={group.id} role="gridcell">{(project.labelsByGroup?.[group.id] ?? []).map(label => <span key={label.id}><i style={{ background: label.color }}/>{label.name}</span>)}</div>)}
      <button aria-label={`Project actions for ${project.name}`} className="lp-project-row__more" onClick={openRowMenu} type="button"><MoreHorizontal size={14}/></button>
      <span />
    </a>
  </ProjectItemMenu>
}

function ProjectBoardColumn({ group, manualOrdering, onCreateProject, onDropProject, onHide, onKeyboardMove, onOpenProject, onOpenProjectIssues, onOpenProjectUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, onSelectAll, propertyOptions, projectMenu, labelGroupProperties, selectedIds, showStatus, visible }: {
  group: ProjectsDataViewProps['groups'][number]
  selectedIds: string[]
  manualOrdering?: boolean
  onCreateProject?: (status: string) => void
  onHide: () => void
  onOpenProject?: (project: ProjectPageItem) => void
  onOpenProjectIssues?: (project: ProjectPageItem) => void
  onOpenProjectUpdates?: (project: ProjectPageItem) => void
  onProjectAction?: (project: ProjectPageItem, action: ProjectAction) => void
  onProjectVisualChange?: (project: ProjectPageItem, icon: string, color: string) => void
  onPropertyChange?: (project: ProjectPageItem, property: ProjectProperty, value: string) => void
  onSelectAll: () => void
  onDropProject: (projectId: string) => boolean
  onKeyboardMove: (project: ProjectPageItem, direction: -1 | 1) => void
  propertyOptions?: ProjectPropertyOptions
  projectMenu?: ProjectMenuIntegration
  labelGroupProperties?: Array<{ id: string; name: string }>
  showStatus: boolean
  visible: Set<string>
}) {
  const [dragOver, setDragOver] = useState(false)
  const card = (project: ProjectPageItem) => <ProjectBoardCard key={project.id} manualOrdering={manualOrdering} onKeyboardMove={direction => onKeyboardMove(project, direction)} onOpen={onOpenProject} onOpenIssues={onOpenProjectIssues} onOpenUpdates={onOpenProjectUpdates} onProjectAction={onProjectAction} onProjectVisualChange={onProjectVisualChange} onPropertyChange={onPropertyChange} project={project} projectMenu={projectMenu} propertyOptions={propertyOptions} labelGroupProperties={labelGroupProperties} selected={selectedIds.includes(project.id)} showStatus={showStatus} visible={visible} />
  return <section aria-label={group.name} className="lp-project-board__column" data-drop-target={dragOver || undefined} onDragEnter={event => { if (event.dataTransfer.types.includes(PROJECT_DRAG_TYPE)) setDragOver(true) }} onDragLeave={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragOver(false) }} onDragOver={event => { if (event.dataTransfer.types.includes(PROJECT_DRAG_TYPE)) { event.preventDefault(); event.dataTransfer.dropEffect = 'move' } }} onDrop={event => { const projectId = event.dataTransfer.getData(PROJECT_DRAG_TYPE); if (projectId) { event.preventDefault(); onDropProject(projectId) } setDragOver(false) }}>
    <header>
      <span className="lp-project-board__heading"><ProjectGroupStatus color={group.color} name={group.name} propertyOptions={propertyOptions}/><strong data-i18n-ignore>{group.name}</strong><span aria-label="Project count" className="lp-project-board__count">{projectCount(group)}</span></span>
      <span className="lp-project-board__actions">
        <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className="lp-project-board__menu" type="button"><MoreHorizontal size={16}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className="lp-project-board__group-menu" sideOffset={4}><DropdownMenu.Item onSelect={onSelectAll}>Select all in column</DropdownMenu.Item><DropdownMenu.Item onSelect={onHide}>Hide column</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        <button aria-label="Create new project" className="lp-project-board__create" onClick={() => onCreateProject?.(projectCreateStatus(group.name, !showStatus, propertyOptions))} type="button"><PlusIcon height={14} width={14} /></button>
      </span>
    </header>
    <div className="lp-project-board__cards">
      {group.subgroups?.map(subgroup => <section className="lp-project-board__subgroup" key={subgroup.id}><header><ProjectGroupStatus color={subgroup.color} compact name={subgroup.name} propertyOptions={propertyOptions}/><span data-i18n-ignore>{subgroup.name}</span><small>{projectCount(subgroup)}</small></header>{subgroup.projects.map(card)}</section>)}
      {!group.subgroups?.length && group.projects.map(card)}
      <button aria-label="Add new project" className="lp-project-board__add" onClick={() => onCreateProject?.(projectCreateStatus(group.name, !showStatus, propertyOptions))} type="button"><PlusIcon /></button>
    </div>
  </section>
}

function ProjectBoardCard({ project, manualOrdering, onKeyboardMove, onOpen, onOpenIssues, onOpenUpdates, onProjectAction, onProjectVisualChange, onPropertyChange, projectMenu, propertyOptions, labelGroupProperties = [], selected = false, showStatus, visible }: ProjectItemActions & { onKeyboardMove: (direction: -1 | 1) => void; selected?: boolean; showStatus: boolean }) {
  const statusOption = (propertyOptions?.status ?? PROPERTY_OPTIONS.status).find(option => option.value === project.status)
  const overdue = isTargetDateOverdue(project.rawTargetDate ?? project.targetDate)
  const metaLabels = labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).flatMap(group => project.labelsByGroup?.[group.id] ?? [])
  const showDate = visible.has('Target date') && Boolean(project.targetDate)
  const showCustomers = visible.has('Customers') && Boolean(project.customerCount)
  const showRevenue = visible.has('Customer revenue') && Boolean(project.customers?.some(customer => (customer.annualRevenue ?? 0) > 0))
  const showMeta = showDate || (visible.has('Initiatives') && Boolean(project.initiativeNames?.length)) || metaLabels.length > 0 || Boolean(project.milestone) || showCustomers || showRevenue
  return <ProjectItemMenu manualOrdering={manualOrdering} onProjectAction={onProjectAction} onPropertyChange={onPropertyChange} options={propertyOptions} project={project} projectMenu={projectMenu}>
    <a
      aria-label={project.name}
      className="lp-project-card"
      data-linear-menu-row=""
      data-selected={selected || undefined}
      draggable={Boolean(onPropertyChange)}
      href={project.href}
      onClick={event => openProjectLink(event, project, onOpen)}
      onDragEnd={event => { event.currentTarget.removeAttribute('data-dragging') }}
      onDragStart={event => { event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData(PROJECT_DRAG_TYPE, project.id); event.currentTarget.setAttribute('data-dragging', 'true') }}
      onKeyDown={event => { if (event.target !== event.currentTarget) return; if (event.key === 'Enter') onOpen?.(project); if (event.altKey && (event.key === 'ArrowLeft' || event.key === 'ArrowRight')) { event.preventDefault(); onKeyboardMove(event.key === 'ArrowLeft' ? -1 : 1) } }}
      role="button"
      tabIndex={0}
    >
      <div className="lp-project-card__top"><span className="lp-project-card__identity"><ViewIconPicker color={project.color} icon={project.icon || 'Project'} onChange={visual => onProjectVisualChange?.(project, visual.icon, visual.color)} triggerClassName="lp-project-card__icon"/>{visible.has('ID') && project.slugId && <span className="lp-project-card__id" data-i18n-ignore>{project.slugId}</span>}<strong data-i18n-ignore>{project.name}</strong></span><span className="lp-project-card__properties">
        {visible.has('Health') && <button aria-label={project.healthLabel ?? `${healthText(project.health)}. Click to open updates.`} className="lp-project-property-trigger" onClick={event => { stopPropagation(event); onOpenUpdates?.(project) }} type="button"><HealthIcon value={project.health} /></button>}
        {showStatus && visible.has('Status') && <ProjectPropertyPicker label={project.status} onChange={value => onPropertyChange?.(project, 'status', value)} options={propertyOptions?.status ?? PROPERTY_OPTIONS.status} property="status" value={project.status}><ProjectStatusGlyph color={statusOption?.color} name={project.status} progress={project.progress / 100} type={statusOption?.statusType}/></ProjectPropertyPicker>}
        {visible.has('Priority') && <ProjectPropertyPicker label={`${priorityText(project.priority)} Priority`} onChange={value => onPropertyChange?.(project, 'priority', value)} options={propertyOptions?.priority ?? PROPERTY_OPTIONS.priority} property="priority" value={project.priority}><DataViewPriorityIcon value={project.priority} /></ProjectPropertyPicker>}
        {visible.has('Lead') && <span className={`lp-project-card__lead ${project.lead ? '' : 'is-empty'}`}><LeadPropertyButton lead={project.lead} onChange={value => onPropertyChange?.(project, 'lead', value)} options={propertyOptions?.lead} /></span>}
      </span></div>
      {visible.has('Summary') && project.summary && <p className="lp-project-card__summary">{project.summary}</p>}
      {showMeta && <div className="lp-project-card__meta">
        {showDate && <ProjectTargetDatePicker buttonClassName={`lp-project-card__date${overdue ? ' is-overdue' : ''}`} displayValue={project.targetDate} onChange={value => onPropertyChange?.(project, 'targetDate', value)} value={project.rawTargetDate}><BoardTargetDateIcon overdue={overdue}/><span>{project.targetDate}</span></ProjectTargetDatePicker>}
        {visible.has('Initiatives') && project.initiativeNames?.map(name => <span className="lp-project-card__initiative" data-i18n-ignore key={name}>{name}</span>)}
        {metaLabels.map(label => <span className="lp-project-card__initiative" data-i18n-ignore key={label.id}><i style={{ background: label.color }}/>{label.name}</span>)}
        {showCustomers && <CustomersRowChip variant="square" customers={project.customers ?? []} customerCount={project.customerCount ?? 0} importantCustomerIds={project.importantCustomerIds ?? []} settings={project.customerSettings}/>}
        {showRevenue && <CustomerRevenueRowChip variant="square" customers={project.customers ?? []} settings={project.customerSettings}/>}
        {project.milestone && <button className="lp-project-card__milestone" onClick={stopPropagation} type="button"><MilestoneProgressIcon className="lp-project-card__milestone-icon" label={`Milestone ${project.milestone}. Progress: ${project.milestoneProgress ?? 0}%.`} overdue={isMilestoneDateOverdue(project.rawMilestoneDate)} progress={project.milestoneProgress ?? 0} /><span className="lp-project-card__milestone-name">{project.milestone}</span>{project.milestoneDate && <span className="lp-project-card__milestone-date">{project.milestoneDate}</span>}</button>}
      </div>}
      {visible.has('Issues') && <div className="lp-project-card__footer"><button className="lp-project-card__issues" onClick={event => { stopPropagation(event); onOpenIssues?.(project) }} type="button">{issueCountLabel(project.issueCount)}</button></div>}
    </a>
  </ProjectItemMenu>
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

function ProjectItemMenu({ children, manualOrdering, onProjectAction, onPropertyChange, options, project, projectMenu }: Pick<ProjectItemActions,'manualOrdering'|'onProjectAction'|'onPropertyChange'|'project'|'projectMenu'> & { children: ReactElement; options?: ProjectPropertyOptions }) {
  const resolved = useMemo(() => ({ ...PROPERTY_OPTIONS, ...options }), [options])
  return <ProjectRowMenu integration={projectMenu} manualOrdering={manualOrdering} options={resolved} project={project} onAction={action => onProjectAction?.(project, action)} onPropertyChange={(property, value) => onPropertyChange?.(project, property, value)}>{children}</ProjectRowMenu>
}

/** The row "…" button opens the row's context menu where it was clicked, like Linear. */
function openRowMenu(event: MouseEvent<HTMLButtonElement>) {
  event.preventDefault()
  event.stopPropagation()
  event.currentTarget.closest('[data-linear-menu-row]')?.dispatchEvent(new globalThis.MouseEvent('contextmenu', { bubbles: true, cancelable: true, clientX: event.clientX, clientY: event.clientY }))
}

function LeadPropertyButton({ lead, onChange, options }: {
  lead?: ProjectPageItem['lead']
  onChange: (value: string) => void
  options?: ProjectPropertyOption[]
}) {
  const property = <ProjectPropertyPicker label={lead?.name ?? 'No lead'} onChange={onChange} options={options ?? PROPERTY_OPTIONS.lead} property="lead" value={lead?.id ?? ''}><ProjectAvatar lead={lead} /></ProjectPropertyPicker>
  if (lead) return property
  return <Tooltip.Provider delayDuration={450} skipDelayDuration={300}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild><span className="lp-project-lead-trigger">{property}</span></Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content data-flow-motion="tooltip" className="lp-project-lead-tooltip" side="bottom" sideOffset={6}>
          <span>Set project lead</span><span className="lp-project-lead-tooltip__shortcut"><kbd>P</kbd><span>then</span><kbd>A</kbd></span>
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>
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
  if (lead.avatarUrl) return <AvatarImage alt={lead.name} className="lp-project-avatar" src={lead.avatarUrl} />
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

function issueCountLabel(count: number) {
  return count === 1 ? '1 issue' : `${count} issues`
}

function isTargetDateOverdue(value?: string) {
  if (!value) return false
  const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T00:00:00` : value)
  if (!Number.isFinite(date.getTime())) return false
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  return date < today
}

function BoardTargetDateIcon({ overdue }: { overdue: boolean }) {
  if (!overdue) return <CalendarIcon size={16} />
  return <svg aria-hidden="true" className="lp-project-card__date-icon" fill="currentColor" focusable="false" height="16" viewBox="0 0 16 16" width="16"><path d="M11 1C13.2091 1 15 2.79086 15 5V6.25C15 6.66421 14.6642 7 14.25 7C13.8358 7 13.5 6.66421 13.5 6.25V6H2.5V11C2.5 12.3807 3.61929 13.5 5 13.5H6.25C6.66421 13.5 7 13.8358 7 14.25C7 14.6642 6.66421 15 6.25 15H5C2.79086 15 1 13.2091 1 11V5C1 2.79086 2.79086 1 5 1H11ZM9.53033 8.46967L11.5 10.4393L13.4697 8.46967C13.7626 8.17678 14.2374 8.17678 14.5303 8.46967C14.8232 8.76256 14.8232 9.23744 14.5303 9.53033L12.5607 11.5L14.5303 13.4697C14.8232 13.7626 14.8232 14.2374 14.5303 14.5303C14.2374 14.8232 13.7626 14.8232 13.4697 14.5303L11.5 12.5607L9.53033 14.5303C9.23744 14.8232 8.76256 14.8232 8.46967 14.5303C8.17678 14.2374 8.17678 13.7626 8.46967 13.4697L10.4393 11.5L8.46967 9.53033C8.17678 9.23744 8.17678 8.76256 8.46967 8.46967C8.76256 8.17678 9.23744 8.17678 9.53033 8.46967Z"/></svg>
}

function groupHasProjects(group: ProjectDataGroup) { return projectCount(group) > 0 }

function projectCreateStatus(groupName: string | undefined, groupedByStatus: boolean, propertyOptions?: ProjectPropertyOptions) {
  const statuses = propertyOptions?.status ?? []
  return (groupedByStatus ? statuses.find(option => option.label === groupName || option.value === groupName)?.value : undefined)
    ?? statuses.find(option => option.statusType === 'backlog')?.value
    ?? statuses[0]?.value
    ?? ''
}

function ProjectsLoadingState({ layout }: { layout: 'list' | 'board' | 'timeline' }) {
  if (layout === 'board') return <div aria-busy="true" aria-label="Loading projects" className="lp-project-board is-loading">
    {Array.from({ length: 4 }, (_, index) => <section className="lp-project-board__column" key={index}>
      <header><span className="lp-project-board__heading"><span className="lp-project-state__skel lp-project-state__skel-icon" /><span className="lp-project-state__skel lp-project-state__skel-title" /></span></header>
      <div className="lp-project-board__cards"><span className="lp-project-state__skel-card" /><span className="lp-project-state__skel-card is-tall" /></div>
    </section>)}
  </div>
  return <div aria-busy="true" aria-label="Loading projects" className={`lp-project-state lp-project-state--loading is-${layout}`}>{Array.from({ length: 8 }, (_, index) => <span key={index} />)}</div>
}

function ProjectsEmptyState({ onCreate }: { onCreate: () => void }) {
  return <div className="lp-project-state lp-project-state--message"><ProjectsPageEmptyIcon /><h2>No projects</h2><p>Create a project to start grouping related issues.</p><button onClick={onCreate} type="button"><PlusIcon /> New project</button></div>
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
    visible.has('Customers') ? '140px' : '0px',
    visible.has('Customer revenue') ? '110px' : '0px',
    ...labelGroupProperties.filter(group => visible.has(projectLabelGroupProperty(group.id))).map(() => '120px'),
    '8px',
  ].join(' ')
}

/** Linear's revenue column header unit: "Revenue ($/yr)" in the workspace currency and revenue unit. */
function revenueColumnUnit(settings?: Partial<FeatureSettings>) {
  return ` (${currencySymbol(settings?.customerRevenueCurrency || 'USD')}${revenueSuffix(settings)})`
}
