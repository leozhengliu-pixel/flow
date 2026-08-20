import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import type { IssueLabel, LabelGroup, Project, ProjectStatus, ProjectTemplate, ProjectUpdate, SavedView, SavedViewMutationInput, Subscription, Team, User } from '@/types/flow'
import { SavedViewEditor, SavedViewMenu, type SavedViewTarget } from '@/components/issue-explorer/saved-view-editor'
import { NewProjectDialog, type NewProjectDraft } from './new-project-dialog'
import { ProjectsDataView, type ProjectAction, type ProjectPageItem, type ProjectProperty, type ProjectPropertyOptions } from './projects-data-view'
import { ProjectsPageSurface } from './projects-page-surface'
import { DEFAULT_PROJECTS_DISPLAY, type ProjectsDisplaySettings } from './projects-display-model'
import { ProjectsInsightsSidebar, type ProjectInsightFilter, type ProjectInsightMode } from './projects-insights-sidebar'
import { useProjectsViewState } from './use-projects-view-state'
import { ProjectsFilterBar } from './projects-filter-bar'
import { createProjectFilter, isProjectFilter, type ProjectFilter, type ProjectFilterField, type ProjectFilterOption } from './projects-filter-model'
import { ProjectsBulkActionBar, type ProjectBulkAction } from './projects-bulk-action-bar'
import type { ViewVisual } from '@/components/views/view-icon-picker'
import { ProjectUpdatesPreview } from './project-updates-preview'
import { labelsForResource } from '@/lib/labels'

export type ProjectMutationInput = {
  templateId?: string
  name?: string
  summary?: string
  description?: string
  icon?: string
  color?: string
  statusId?: string
  priority?: number
  health?: Project['health']
  leadId?: string
  memberIds?: string[]
  labelIds?: string[]
  teamIds?: string[]
  dependencyIds?: string[]
  initiatives?: string[]
  customers?: string[]
  startDate?: string
  targetDate?: string
  updateCadence?: Project['updateCadence']
}

export type ProjectCreateInput = Required<Pick<ProjectMutationInput, 'name'>> & ProjectMutationInput

export type ProjectsPageProps = {
  projects: Project[]
  projectUpdates?: Record<string, ProjectUpdate[]>
  projectStatuses?: ProjectStatus[]
  projectTemplates?: ProjectTemplate[]
  users: User[]
  teams: Team[]
  labels?: IssueLabel[]
  labelGroups?: LabelGroup[]
  loading?: boolean
  error?: string | null
  onCreateProject?: (input: ProjectCreateInput) => Promise<Project>
  onDeleteProject?: (projectId: string) => Promise<void>
  onOpenProject?: (project: Project) => void
  onOpenProjectIssues?: (project: Project) => void
  onRetry?: () => void
  onUpdateProject?: (projectId: string, input: ProjectMutationInput) => Promise<Project>
  onOpenSidebar?: () => void
  onSetDisplayDefault?: (display: ProjectsDisplaySettings) => Promise<void>
  projectDisplayDefault?: Record<string, unknown>
  projectHref?: (project: Project) => string
  workspaceKey?: string
  creatingView?: boolean
  savedView?: SavedView
  duplicateFrom?: SavedView
  editingView?: boolean
  savedViews?: SavedView[]
  scopeTeamId?: string
  viewerId?: string
  viewer?: User
  defaultSaveScope?: SavedView['scope']
  onCreateSavedView?: (input: SavedViewMutationInput) => Promise<SavedView>
  onUpdateSavedView?: (id: string, input: SavedViewMutationInput) => Promise<SavedView>
  onDeleteSavedView?: (view: SavedView) => Promise<void>
  savedViewSubscription?: Subscription
  onSetSavedViewSubscriptionEvents?: (view: SavedView, events: string[]) => Promise<void>
  onDuplicateSavedView?: (view: SavedView) => void
  onBeginEditSavedView?: () => void
  onFinishEditSavedView?: () => void
  onNavigateAllViews?: () => void
  onNavigateNewView?: () => void
  onNavigateSavedView?: (view: SavedView) => void
  onNewViewResourceChange?: (resource: 'issues' | 'projects') => void
  onCreateProjectUpdate?: (projectId: string, input: { body: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onUpdateProjectUpdate?: (projectId: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onDeleteProjectUpdate?: (projectId: string, updateId: string) => Promise<void>
  onCommentProjectUpdate?: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onReactProjectUpdate?: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  createOnMount?: boolean
}

export function ProjectsPage({
  projects,
  projectUpdates = {},
  projectStatuses = [],
  projectTemplates = [],
  users,
  teams,
  labels = [],
  labelGroups = [],
  loading = false,
  error = null,
  onCreateProject,
  onDeleteProject,
  onOpenProject,
  onOpenProjectIssues,
  onRetry,
  onUpdateProject,
  onOpenSidebar,
  onSetDisplayDefault,
  projectDisplayDefault,
  projectHref,
  workspaceKey = 'cleantrack',
  creatingView = false,
  savedView,
  duplicateFrom,
  editingView = false,
  savedViews = [],
  scopeTeamId,
  viewerId,
  viewer,
  defaultSaveScope,
  onCreateSavedView,
  onUpdateSavedView,
  onDeleteSavedView,
  savedViewSubscription,
  onSetSavedViewSubscriptionEvents,
  onDuplicateSavedView,
  onBeginEditSavedView,
  onFinishEditSavedView,
  onNavigateAllViews,
  onNavigateNewView,
  onNavigateSavedView,
  onNewViewResourceChange,
  onCreateProjectUpdate,
  onUpdateProjectUpdate,
  onDeleteProjectUpdate,
  onCommentProjectUpdate,
  onReactProjectUpdate,
  createOnMount = false,
}: ProjectsPageProps) {
  const sourceView = savedView ?? duplicateFrom
  const scopedProjects = useMemo(() => scopeTeamId ? projects.filter(project => project.teamIds.includes(scopeTeamId)) : projects, [projects, scopeTeamId])
  const items = useMemo(() => scopedProjects.map(project => toPageItem(project, projectHref?.(project), teams, projectUpdates[project.id]?.[0])), [projectHref, projectUpdates, scopedProjects, teams])
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [insightMode, setInsightMode] = useState<ProjectInsightMode>('health')
  const [insightFilter, setInsightFilter] = useState<ProjectInsightFilter>(() => projectFilterFromSavedView(sourceView))
  const draftFiltersKey = `flow:projects:draft-filters:${workspaceKey}:${scopeTeamId ?? 'workspace'}`
  const [projectFilters, setProjectFilters] = useState<ProjectFilter[]>(() => projectFiltersFromSavedView(sourceView, creatingView ? readDraftFilters(draftFiltersKey) : []))
  const visibleItems = useMemo(() => {
    let result = items.filter(item => projectFilters.every(filter => matchesProjectFilter(item, filter)))
    if (!insightFilter) return result
    if (insightFilter.kind === 'health') result = result.filter(item => item.health === insightFilter.value)
    else result = result.filter(item => (item.lead?.id ?? '') === insightFilter.value)
    return result
  }, [insightFilter, items, projectFilters])
  const workspaceDefault = useMemo(() => parseProjectDisplayDefault(projectDisplayDefault), [projectDisplayDefault])
  const savedDisplay = useMemo(() => parseProjectDisplayDefault(sourceView?.display), [sourceView?.display])
  const view = useProjectsViewState(visibleItems, { initial: savedDisplay ? { display: savedDisplay } : undefined, storageKey: `${workspaceKey}:${scopeTeamId ?? 'workspace'}:${savedView?.id ?? 'all'}`, workspaceDefault })
  const [createOpen, setCreateOpen] = useState(false)
  const [createStatus, setCreateStatus] = useState('Backlog')
  const [updatesProjectId, setUpdatesProjectId] = useState<string>()
  const [viewEditor, setViewEditor] = useState<'create' | 'edit' | undefined>(creatingView ? 'create' : editingView ? 'edit' : undefined)
  const [viewSaving, setViewSaving] = useState(false)
  const projectById = useMemo(() => new Map(projects.map(project => [project.id, project])), [projects])
  const projectLabels = useMemo(() => labelsForResource(labels, 'project'), [labels])
  const projectLabelGroupNames = useMemo(() => new Map(labelGroups.filter(group => group.resourceType === 'project').map(group => [group.id, group.name])), [labelGroups])
  const availableProjectStatuses = useMemo(() => projectStatuses.length ? projectStatuses : uniqueStatuses(projects.map(project => project.status)), [projectStatuses, projects])
  const statusOptions = useMemo(() => availableProjectStatuses.map((status, index) => ({ color: status.color, label: status.name, shortcut: String(index + 1), statusType: status.type, value: status.name })), [availableProjectStatuses])
  const propertyOptions: ProjectPropertyOptions = useMemo(() => ({
    lead: [{ label: 'No lead', shortcut: '0', value: '' }, ...users.filter(user => user.active).map(user => ({ avatarUrl: user.avatarUrl, group: 'Users from the project team', keywords: `${user.name} ${user.email}`, label: user.displayName, value: user.id }))],
    members: users.filter(user => user.active).map(user => ({ avatarUrl: user.avatarUrl, keywords: `${user.name} ${user.email}`, label: user.displayName, value: user.id })),
    labels: projectLabels.map(label => ({ color: label.color, group: label.groupId ? projectLabelGroupNames.get(label.groupId) : undefined, label: label.name, value: label.id })),
    status: statusOptions,
    targetDate: targetDateOptions(),
  }), [projectLabelGroupNames, projectLabels, statusOptions, users])
  const filterOptions = useMemo(() => projectFilterOptions(items, users, availableProjectStatuses), [availableProjectStatuses, items, users])
  const saveTargets = useMemo<SavedViewTarget[]>(() => [
    { scope: 'personal', label: 'Personal' },
    { scope: 'workspace', label: 'Workspace' },
    ...teams.map(team => ({ scope: 'team' as const, label: team.name, teamId: team.id })),
  ], [teams])
  const initialSaveTarget = saveTargets.find(target => target.scope === (sourceView?.scope ?? defaultSaveScope ?? (scopeTeamId ? 'team' : 'workspace')) && (target.scope !== 'team' || target.teamId === (sourceView?.teamId ?? scopeTeamId))) ?? saveTargets[0]

  useEffect(() => setViewEditor(creatingView ? 'create' : editingView ? 'edit' : undefined), [creatingView, editingView])
  useEffect(() => {
    if (createOnMount) setCreateOpen(true)
  }, [createOnMount])

  const openCreate = (status = 'Backlog') => {
    setCreateStatus(status)
    setCreateOpen(true)
  }

  const updateProperty = async (item: ProjectPageItem, property: ProjectProperty, value: string) => {
    if (!onUpdateProject) return
    const project = projectById.get(item.id)
    if (!project) return
    await onUpdateProject(project.id, propertyMutation(projects, property, value, availableProjectStatuses))
  }

  const projectAction = async (item: ProjectPageItem, action: ProjectAction) => {
    const project = projectById.get(item.id)
    if (!project) return
    if (action === 'delete' && onDeleteProject && window.confirm(`Delete ${project.name}?`)) await onDeleteProject(project.id)
    if (action === 'copy') await navigator.clipboard?.writeText(project.name)
    if (action === 'comment') setUpdatesProjectId(project.id)
    if (action === 'rename' && onUpdateProject) {
      const name = window.prompt('Rename project', project.name)?.trim()
      if (name && name !== project.name) await onUpdateProject(project.id, { name })
    }
    if (action === 'schedule') {
      const schedule = window.prompt('Project update schedule', localStorage.getItem(`flow:project:${project.id}:update-schedule`) ?? 'Weekly')?.trim()
      if (schedule) localStorage.setItem(`flow:project:${project.id}:update-schedule`, schedule)
    }
    if (action === 'initiatives' || action === 'dependencies') toast.info(`${action === 'initiatives' ? 'Initiatives' : 'Dependencies'} are managed from the project overview.`)
    if (action === 'customerRequest') toast.info('Customer requests require the Flow integration.')
  }

  const create = async (draft: NewProjectDraft) => {
    if (!onCreateProject) throw new Error('Project creation is not connected.')
    await onCreateProject(draftMutation(projects, draft, availableProjectStatuses))
    setCreateOpen(false)
  }

  const savedViewSnapshot = (): SavedViewMutationInput => ({
    resource: 'projects',
    scope: scopeTeamId ? 'team' : 'workspace',
    teamId: scopeTeamId ?? '',
    ownerId: viewerId,
    view: 'all',
    filters: [...projectFilters, ...(insightFilter ? [insightFilter] : [])],
    display: view.state.display,
  })

  const saveView = async (name: string, description: string, target: SavedViewTarget | undefined, visual: ViewVisual) => {
    if (viewSaving) return
    setViewSaving(true)
    try {
      if (viewEditor === 'edit' && savedView && onUpdateSavedView) {
        await onUpdateSavedView(savedView.id, { ...savedViewSnapshot(), name, description, ...visual })
      } else if (onCreateSavedView) {
        const destination = target ?? initialSaveTarget
        const created = await onCreateSavedView({ ...savedViewSnapshot(), name, description, ...visual, scope: destination.scope, teamId: destination.scope === 'team' ? destination.teamId : '' })
        removeDraftFilters(draftFiltersKey)
        onNavigateSavedView?.(created)
      }
      setViewEditor(undefined)
      if (viewEditor === 'edit') onFinishEditSavedView?.()
    } catch {
      // App owns the request toast; keep the editor open so the user can retry.
    } finally {
      setViewSaving(false)
    }
  }

  const projectViews = [
    { id: 'all', kind: 'all' as const, label: 'All projects' },
    ...savedViews.map(item => ({ id: item.id, kind: 'saved' as const, label: item.name, icon: item.icon, color: item.color })),
    ...(creatingView ? [{ id: 'new', kind: 'saved' as const, label: 'New view' }] : []),
  ]

  const addFilter = (label: string, option?: ProjectFilterOption) => {
    const field = PROJECT_FILTER_FIELDS[label]
    if (!field || !option) {
      toast.info(label === 'AI filter' ? 'AI filters require the Flow integration.' : `${label} is not available for the current project data.`)
      return
    }
    setProjectFilters(current => {
      const existing = current.find(filter => filter.field === field && filter.operator === 'is')
      if (!existing) return [...current, createProjectFilter(field, label, option)]
      if (existing.values.some(value => value.id === option.id)) return current
      return current.map(filter => filter.id === existing.id ? { ...filter, values: [...filter.values, option] } : filter)
    })
  }

  const beginSaveFilteredView = () => {
    writeDraftFilters(draftFiltersKey, projectFilters)
    onNavigateNewView?.()
  }

  const selectedProjects = items.filter(item => view.state.selectedIds.includes(item.id))
  const bulkActionOptions = (action: ProjectBulkAction) => {
    if (action === 'status') return statusOptions.map(option => ({ id: option.value, label: option.label, color: statusColor(option.value) }))
    if (action === 'priority') return [{ id: 'none', label: 'No priority' }, { id: 'urgent', label: 'Urgent', color: '#e56a68' }, { id: 'high', label: 'High' }, { id: 'medium', label: 'Medium' }, { id: 'low', label: 'Low' }]
    if (action === 'lead') return propertyOptions.lead?.map(option => ({ id: option.value, label: option.label }))
    if (action === 'targetDate') return propertyOptions.targetDate?.map(option => ({ id: option.value, label: option.label }))
    return undefined
  }
  const runBulkAction = async (action: ProjectBulkAction, value?: string) => {
    if (action === 'copyNames') {
      await navigator.clipboard?.writeText(selectedProjects.map(project => project.name).join('\n'))
      toast.success('Project names copied')
      return
    }
    if (action === 'delete') {
      if (!onDeleteProject || !window.confirm(`Delete ${selectedProjects.length} selected ${selectedProjects.length === 1 ? 'project' : 'projects'}?`)) return
      await Promise.all(selectedProjects.map(project => onDeleteProject(project.id)))
      view.setSelectedIds([])
      return
    }
    if (value !== undefined && onUpdateProject && ['status', 'priority', 'lead', 'targetDate'].includes(action)) {
      await Promise.all(selectedProjects.map(project => onUpdateProject(project.id, propertyMutation(projects, action as ProjectProperty, value, availableProjectStatuses))))
      view.setSelectedIds([])
      return
    }
    toast.info(`${bulkActionLabel(action)} is not available for the current project data.`)
  }

  return <ProjectsPageSurface
    activeViewId={creatingView ? 'new' : savedView?.id ?? 'all'}
    creatingView={creatingView}
    displaySettings={view.state.display}
    filterBar={<ProjectsFilterBar
      filters={projectFilters}
      onAdd={() => document.querySelector<HTMLButtonElement>('.lp-projects__actions [aria-label="Add filter"]')?.click()}
      onChange={next => setProjectFilters(current => current.map(filter => filter.id === next.id ? next : filter))}
      onClear={() => setProjectFilters([])}
      onRemove={id => setProjectFilters(current => current.filter(filter => filter.id !== id))}
      onSave={!creatingView && !savedView ? beginSaveFilteredView : undefined}
      options={filterOptions}
    />}
    filterCount={projectFilters.length + (insightFilter ? 1 : 0)}
    filterOptions={Object.fromEntries(Object.entries(PROJECT_FILTER_FIELDS).map(([label, field]) => [label, filterOptions[field] ?? []]))}
    onAddFilter={addFilter}
    onChangeDisplay={view.setDisplay}
    onCreateProject={() => openCreate()}
    onAddView={onNavigateNewView}
    onChangeView={item => {
      if (item.id === 'all') onNavigateAllViews?.()
      else if (item.id === 'new') onNavigateNewView?.()
      else {
        const next = savedViews.find(view => view.id === item.id)
        if (next) onNavigateSavedView?.(next)
      }
    }}
    onOpenAppSidebar={onOpenSidebar}
    onNewViewResourceChange={onNewViewResourceChange}
    onResetDisplay={view.resetDisplay}
    onSetDisplayDefault={() => {
      void (async () => {
        try {
          if (onSetDisplayDefault) await onSetDisplayDefault(view.state.display)
          else view.setDisplayDefault()
          toast.success('Project view default updated')
        } catch {
          // The application boundary already surfaced the failed persistence request.
        }
      })()
    }}
    onToggleSidebar={() => setSidebarOpen(current => !current)}
    sidebarOpen={sidebarOpen}
    viewActions={savedView && <SavedViewMenu
      view={savedView}
      users={users}
      teams={teams}
      subscriptionEvents={savedViewSubscription?.events?.length ? savedViewSubscription.events : savedViewSubscription || savedView.subscribed ? ['issue-added', 'issue-completed'] : []}
      onEdit={() => { setViewEditor('edit'); onBeginEditSavedView?.() }}
      onDuplicate={onDuplicateSavedView ? () => onDuplicateSavedView(savedView) : undefined}
      onUpdate={onUpdateSavedView ? input => { void onUpdateSavedView(savedView.id, input) } : undefined}
      onSetSubscriptionEvents={onSetSavedViewSubscriptionEvents ? events => { void onSetSavedViewSubscriptionEvents(savedView, events) } : undefined}
      onCopy={() => { void navigator.clipboard.writeText(window.location.href) }}
      onExport={() => exportProjectsCsv(visibleItems, savedView.name)}
      onDelete={() => { if (onDeleteSavedView && window.confirm(`Delete view "${savedView.name}"?`)) void onDeleteSavedView(savedView) }}
    />}
    viewEditor={viewEditor ? actions => <SavedViewEditor
      actions={creatingView ? undefined : actions}
      ariaLabel={viewEditor === 'edit' ? 'Edit project view' : 'New project view'}
      initialName={viewEditor === 'edit' ? savedView?.name : duplicateFrom?.name ?? ''}
      namePlaceholder="All projects"
      initialDescription={viewEditor === 'edit' ? savedView?.description : duplicateFrom?.description ?? ''}
      initialIcon={viewEditor === 'edit' ? savedView?.icon : duplicateFrom?.icon}
      initialColor={viewEditor === 'edit' ? savedView?.color : duplicateFrom?.color}
      initialTarget={initialSaveTarget}
      saveTargets={viewEditor === 'create' ? saveTargets : []}
      saving={viewSaving}
      onCancel={() => {
        setViewEditor(undefined)
        if (creatingView) {
          removeDraftFilters(draftFiltersKey)
          onNavigateAllViews?.()
        } else if (viewEditor === 'edit') {
          onFinishEditSavedView?.()
        }
      }}
      onSave={(name, description, target, visual) => { void saveView(name, description, target, visual) }}
    /> : undefined}
    views={projectViews}
  >
    <div className={`lp-projects__workspace ${sidebarOpen ? 'has-insights' : ''}`}>
      <div className="lp-projects__data"><ProjectsDataView
        {...view.dataViewProps}
        error={error}
        loading={loading}
        onCreateProject={openCreate}
        onOpenProject={item => {
          const project = projectById.get(item.id)
          if (project) onOpenProject?.(project)
        }}
        onOpenProjectIssues={item => {
          const project = projectById.get(item.id)
          if (project) onOpenProjectIssues?.(project)
        }}
        onOpenProjectUpdates={item => setUpdatesProjectId(item.id)}
        onProjectAction={projectAction}
        onProjectVisualChange={(item, icon, color) => {
          const project = projectById.get(item.id)
          if (project && onUpdateProject) void onUpdateProject(project.id, { icon, color })
        }}
        onPropertyChange={updateProperty}
        onRetry={onRetry}
        propertyOptions={propertyOptions}
      /></div>
      {sidebarOpen && <ProjectsInsightsSidebar activeFilter={insightFilter} mode={insightMode} onChangeFilter={setInsightFilter} onChangeMode={setInsightMode} projects={items} />}
    </div>
    {updatesProjectId && projectById.get(updatesProjectId) && <ProjectUpdatesPreview
      onClose={() => setUpdatesProjectId(undefined)}
      onCreate={onCreateProjectUpdate}
      onUpdate={onUpdateProjectUpdate}
      onDelete={onDeleteProjectUpdate}
      onComment={onCommentProjectUpdate}
      onReact={onReactProjectUpdate}
      onOpenProject={project => { setUpdatesProjectId(undefined); onOpenProject?.(project) }}
      project={projectById.get(updatesProjectId)!}
      updates={projectUpdates[updatesProjectId] ?? []}
      viewer={viewer ?? users.find(user => user.id === viewerId)}
    />}
    <NewProjectDialog
      defaultStatus={createStatus}
      dependencies={projects.map(project => ({ id: project.id, label: project.name, color: project.color }))}
      labels={projectLabels.map(label => ({ id: label.id, label: label.name, color: label.color, groupId: label.groupId, groupLabel: label.groupId ? projectLabelGroupNames.get(label.groupId) : undefined }))}
      leads={users.map(user => ({ id: user.id, label: user.displayName }))}
      members={users.map(user => ({ id: user.id, label: user.displayName }))}
      templates={projectTemplates.map(template => ({
        id: template.id,
        label: template.name,
        name: template.name,
        description: template.description,
        summary: template.summary,
        icon: template.icon,
        color: template.color,
        status: availableProjectStatuses.find(status => status.id === template.statusId)?.name,
        priority: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][template.priority] ?? 'No priority',
        teamIds: template.teamIds,
        labelIds: template.labelIds.filter(id => projectLabels.some(label => label.id === id)),
      }))}
      onClose={() => setCreateOpen(false)}
      onCreate={create}
      open={createOpen}
      teamLabel={teams[0]?.key ?? 'Team'}
      teams={teams.map(team => ({ id: team.id, label: team.name, color: team.color }))}
    />
    <ProjectsBulkActionBar
      onAction={(action, value) => { void runBulkAction(action, value).catch(() => undefined) }}
      onAsk={() => toast.info('Ask Flow requires the Flow AI integration.')}
      onClear={() => view.setSelectedIds([])}
      options={bulkActionOptions}
      projects={selectedProjects}
    />
  </ProjectsPageSurface>
}

function toPageItem(project: Project, href?: string, teams: Team[] = [], latestUpdate?: ProjectUpdate): ProjectPageItem {
  return {
    color: project.color,
    health: ({ onTrack: 'on-track', atRisk: 'at-risk', offTrack: 'off-track', noUpdate: 'no-update' } as const)[project.health],
    icon: project.icon && !/^[a-z0-9]$/i.test(project.icon) ? project.icon : undefined,
    id: project.id,
    href,
    issueCount: project.issueCount,
    lead: project.lead ? { avatarUrl: project.lead.avatarUrl, id: project.lead.id, name: project.lead.displayName } : undefined,
    milestone: project.summary && !project.description ? project.summary : undefined,
    name: project.name,
    priority: ({ 0: 'none', 1: 'urgent', 2: 'high', 3: 'medium', 4: 'low' } as const)[project.priority as 0 | 1 | 2 | 3 | 4] ?? 'none',
    progress: Math.round(project.progress * 100),
    status: project.status.name,
    startDate: project.startDate ? formatMonth(project.startDate) : undefined,
    summary: project.description || project.summary,
    team: project.teamIds[0] ? teams.find(team => team.id === project.teamIds[0]) : undefined,
    memberIds: project.memberIds,
    labelIds: project.labelIds,
    teamIds: project.teamIds,
    rawStartDate: project.startDate,
    rawTargetDate: project.targetDate,
    targetDate: project.targetDate ? formatMonth(project.targetDate) : undefined,
    createdAt: project.createdAt,
    updatedAt: latestUpdate?.createdAt ?? project.updatedAt,
  }
}

function propertyMutation(projects: Project[], property: ProjectProperty, value: string, projectStatuses: ProjectStatus[] = []): ProjectMutationInput {
  if (property === 'health') return { health: ({ 'on-track': 'onTrack', 'at-risk': 'atRisk', 'off-track': 'offTrack', 'no-update': 'noUpdate' } as const)[value as 'on-track' | 'at-risk' | 'off-track' | 'no-update'] }
  if (property === 'priority') return { priority: ({ none: 0, urgent: 1, high: 2, medium: 3, low: 4 })[value] }
  if (property === 'lead') return { leadId: value }
  if (property === 'members') return { memberIds: value ? value.split(',') : [] }
  if (property === 'labels') return { labelIds: value ? value.split(',') : [] }
  if (property === 'startDate') return { startDate: value }
  if (property === 'targetDate') return { targetDate: value }
  return { statusId: projectStatuses.find(status => status.name === value)?.id ?? projects.find(project => project.status.name === value)?.status.id }
}

function draftMutation(projects: Project[], draft: NewProjectDraft, projectStatuses: ProjectStatus[] = []): ProjectCreateInput {
  return {
    templateId: draft.templateId,
    color: draft.color,
    description: draft.description,
    icon: draft.icon,
    leadId: draft.leadId,
    memberIds: draft.memberIds,
    labelIds: draft.labelIds,
    dependencyIds: draft.dependencyIds,
    name: draft.name,
    priority: Math.max(0, ['No priority', 'Urgent', 'High', 'Medium', 'Low'].indexOf(draft.priority)),
    startDate: draft.startDate,
    statusId: projectStatuses.find(status => status.name === draft.status)?.id ?? projects.find(project => project.status.name === draft.status)?.status.id,
    summary: draft.summary,
    targetDate: draft.targetDate,
    teamIds: draft.teamIds,
  }
}

function targetDateOptions() {
  const today = new Date()
  const monthEnd = new Date(today.getFullYear(), today.getMonth() + 1, 0)
  return [
    { label: 'No target date', value: '' },
    { label: 'Today', value: isoDate(today) },
    { label: 'End of this month', value: isoDate(monthEnd) },
  ]
}

function isoDate(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function formatMonth(value: string) {
  const date = new Date(`${value}T00:00:00`)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('en', { month: 'short', year: 'numeric' }).format(date)
}

function uniqueStatuses(items: ProjectStatus[]) { return items.filter((item, index) => items.findIndex(candidate => candidate.id === item.id) === index) }

function parseProjectDisplayDefault(value: Record<string, unknown> | undefined): ProjectsDisplaySettings | undefined {
  if (!value || !Array.isArray(value.properties)) return undefined
  const layout = value.layout === 'board' || value.layout === 'timeline' ? value.layout : 'list'
  return {
    ...DEFAULT_PROJECTS_DISPLAY,
    ...value,
    grouping: typeof value.grouping === 'string' ? value.grouping : DEFAULT_PROJECTS_DISPLAY.grouping,
    layout,
    ordering: typeof value.ordering === 'string' ? value.ordering : DEFAULT_PROJECTS_DISPLAY.ordering,
    properties: value.properties.filter((property): property is string => typeof property === 'string'),
    showClosed: typeof value.showClosed === 'string' ? value.showClosed : DEFAULT_PROJECTS_DISPLAY.showClosed,
    showEmptyGroups: typeof value.showEmptyGroups === 'boolean' ? value.showEmptyGroups : DEFAULT_PROJECTS_DISPLAY.showEmptyGroups,
    subGrouping: typeof value.subGrouping === 'string' ? value.subGrouping : DEFAULT_PROJECTS_DISPLAY.subGrouping,
  }
}

function projectFilterFromSavedView(view?: SavedView): ProjectInsightFilter {
  if (!view || !Array.isArray(view.filters)) return null
  const filter = view.filters.find(value => value && typeof value === 'object' && ('kind' in value) && ('value' in value))
  if (!filter || typeof filter !== 'object') return null
  const { kind, value } = filter as { kind?: unknown; value?: unknown }
  return (kind === 'health' || kind === 'lead') && typeof value === 'string' ? { kind, value } : null
}

const PROJECT_FILTER_FIELDS: Record<string, ProjectFilterField> = {
  Status: 'status', Priority: 'priority', Lead: 'lead', Members: 'members', Health: 'health', Dates: 'dates', Milestones: 'milestones', 'Specific project': 'project',
}

function projectFilterOptions(items: ProjectPageItem[], users: User[], projectStatuses: ProjectStatus[]): Partial<Record<ProjectFilterField, ProjectFilterOption[]>> {
  const count = (field: ProjectFilterField, id: string) => items.filter(item => projectValueMatches(item, field, id)).length
  const values = (field: ProjectFilterField, definitions: ProjectFilterOption[]) => definitions.map(option => ({ ...option, count: count(field, option.id) }))
  return {
    status: values('status', (projectStatuses.length ? projectStatuses.map(status => ({ id: status.name, label: status.name, color: status.color })) : uniqueFilterOptions(items.map(item => ({ id: item.status, label: item.status, color: statusColor(item.status) })))).sort(statusOptionOrder)),
    priority: values('priority', [
      { id: 'urgent', label: 'Urgent', color: '#e56a68' }, { id: 'high', label: 'High', color: '#c8c8cb' }, { id: 'medium', label: 'Medium', color: '#a7a7ac' }, { id: 'low', label: 'Low', color: '#77777c' }, { id: 'none', label: 'No priority', color: '#68686d' },
    ]),
    lead: values('lead', [{ id: '', label: 'No lead', color: '#68686d' }, ...users.map(user => ({ id: user.id, label: user.displayName }))]),
    members: values('members', users.map(user => ({ id: user.id, label: user.displayName }))),
    health: values('health', [{ id: 'on-track', label: 'On track', color: '#4d9b5d' }, { id: 'at-risk', label: 'At risk', color: '#d3a036' }, { id: 'off-track', label: 'Off track', color: '#d8605f' }, { id: 'no-update', label: 'No update', color: '#57575c' }]),
    dates: values('dates', [{ id: 'has-target', label: 'Has target date' }, { id: 'no-target', label: 'No target date' }, { id: 'overdue', label: 'Target date is overdue', color: '#d8605f' }]),
    milestones: values('milestones', uniqueFilterOptions(items.filter(item => item.milestone).map(item => ({ id: item.milestone!, label: item.milestone! })))),
    project: items.map(item => ({ id: item.id, label: item.name, color: item.color, count: 1 })),
  }
}

function projectFiltersFromSavedView(view: SavedView | undefined, fallback: ProjectFilter[]) {
  const filters = view?.filters?.filter(isProjectFilter) ?? []
  return filters.length ? filters : fallback
}

function matchesProjectFilter(item: ProjectPageItem, filter: ProjectFilter) {
  const matched = filter.values.some(value => projectValueMatches(item, filter.field, value.id))
  return filter.operator === 'is' ? matched : !matched
}

function projectValueMatches(item: ProjectPageItem, field: ProjectFilterField, value: string) {
  if (field === 'status') return item.status === value
  if (field === 'priority') return item.priority === value
  if (field === 'lead') return (item.lead?.id ?? '') === value
  if (field === 'members') return item.memberIds?.includes(value) ?? false
  if (field === 'health') return item.health === value
  if (field === 'project') return item.id === value
  if (field === 'milestones') return item.milestone === value
  if (field === 'dates') {
    if (value === 'has-target') return Boolean(item.rawTargetDate)
    if (value === 'no-target') return !item.rawTargetDate
    return Boolean(item.rawTargetDate && Date.parse(item.rawTargetDate) < Date.now())
  }
  return true
}

function statusColor(status: string) { return ({ Backlog: '#77777c', Planned: '#d6b326', 'In Progress': '#5e8fd8', Completed: '#5e6ad2', Canceled: '#77777c' })[status] ?? '#77777c' }
function uniqueFilterOptions(items: ProjectFilterOption[]) { return items.filter((item, index) => items.findIndex(candidate => candidate.id === item.id) === index) }
function statusOptionOrder(left: ProjectFilterOption, right: ProjectFilterOption) { return ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled'].indexOf(left.id) - ['Backlog', 'Planned', 'In Progress', 'Completed', 'Canceled'].indexOf(right.id) }
function readDraftFilters(key: string): ProjectFilter[] { try { const value = JSON.parse(sessionStorage.getItem(key) ?? '[]'); return Array.isArray(value) ? value.filter(isProjectFilter) : [] } catch { return [] } }
function writeDraftFilters(key: string, filters: ProjectFilter[]) { try { sessionStorage.setItem(key, JSON.stringify(filters)) } catch { /* best effort */ } }
function removeDraftFilters(key: string) { try { sessionStorage.removeItem(key) } catch { /* best effort */ } }
function bulkActionLabel(action: ProjectBulkAction) { const labels: Partial<Record<ProjectBulkAction, string>> = { edit: 'Project editing', initiatives: 'Initiatives', labels: 'Project labels', dependencies: 'Project dependencies', members: 'Project members', favorite: 'Favorites', subscribe: 'Subscriptions' }; return labels[action] ?? action }
function exportProjectsCsv(projects: ProjectPageItem[], name: string) { const quote = (value: unknown) => `"${String(value ?? '').replaceAll('"', '""')}"`; const csv = [['Name', 'Status', 'Health', 'Priority', 'Lead', 'Start date', 'Target date'], ...projects.map(project => [project.name, project.status, project.health, project.priority, project.lead?.name ?? '', project.rawStartDate ?? '', project.rawTargetDate ?? ''])].map(row => row.map(quote).join(',')).join('\n'); const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' })); const link = document.createElement('a'); link.href = url; link.download = `${name.replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'view'}.csv`; link.click(); URL.revokeObjectURL(url) }
