import { useEffect, useMemo, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Layers2, Link2, Pencil, Star, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { ProjectOverview } from './project-overview'
import { ProjectActivity } from './project-activity'
import { ProjectIssueDisplayMenu, ProjectIssueFilterMenu, ProjectIssues, ProjectNewView, type ProjectIssueFilters } from './project-issues'
import { DEFAULT_PROJECT_ISSUE_DISPLAY } from './project-issue-display'
import { ProjectDetailsSidebar } from './project-details-sidebar'
import { ProjectInsights } from './project-insights'
import { ProjectActionsMenu, ProjectDescriptionHistoryDialog, ProjectNotificationMenu } from './project-header-menus'
import type { ProjectDetailProps, ProjectDetailTab } from './project-detail-types'
import { labelsForResource } from '@/lib/labels'
import { AddViewIcon, InsightsIcon, SidebarIcon } from '@/components/ui/view-action-icons'
import './project-detail-page.css'

export type { ProjectDetailTab } from './project-detail-types'

export function ProjectDetailPage(props: ProjectDetailProps) {
  const { project, projects, projectUpdates, issues, users, labels, labelGroups, viewer, tab, onTabChange, onUpdate, onDelete, onOpenSidebar, onToggleFavorite } = props
  const [detailsOpen, setDetailsOpen] = useStoredBoolean(`flow:project:${project.id}:details`, true)
  const [insightsOpen, setInsightsOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [notificationOpen, setNotificationOpen] = useState(false)
  const issueStateKey = `flow:project:${project.id}:issues`
  const [issueFilters, setIssueFilters] = useState<ProjectIssueFilters>(() => readIssueFilters(issueStateKey))
  const [issueDisplay, setIssueDisplay] = useState(() => readIssueDisplay(issueStateKey))
  const [activeSavedViewId, setActiveSavedViewId] = useState<string>()
  const [milestoneScopeId, setMilestoneScopeId] = useState<string>(() => new URLSearchParams(location.search).get('projectMilestoneId') ?? '')
  const projectIssues = useMemo(() => issues.filter(issue => issue.project?.id === project.id), [issues, project.id])
  const scopedProjectIssues = useMemo(() => milestoneScopeId ? projectIssues.filter(issue => issue.projectMilestoneId === milestoneScopeId) : projectIssues, [milestoneScopeId, projectIssues])
  const milestoneScope = (project.milestones ?? []).find(milestone => milestone.id === milestoneScopeId)
  const issueLabels = useMemo(() => labelsForResource(labels, 'issue'), [labels])
  const projectLabels = useMemo(() => labelsForResource(labels, 'project'), [labels])
  const projectSavedViews = useMemo(() => props.savedViews.filter(view => view.resource === 'issues' && JSON.stringify(view.filters).includes(project.id)), [project.id, props.savedViews])
  const favorited = Boolean(props.favorite)
  const changeIssueFilters = (next: ProjectIssueFilters) => { setIssueFilters(next); localStorage.setItem(`${issueStateKey}:filters`, JSON.stringify(next)) }
  const changeIssueDisplay = (next: typeof issueDisplay) => { setIssueDisplay(next); localStorage.setItem(`${issueStateKey}:display`, JSON.stringify({ ...next, properties: [...next.properties] })) }
  const openSavedView = (view: typeof projectSavedViews[number]) => { setActiveSavedViewId(view.id); changeIssueFilters(filtersFromSavedView(view.filters, issueLabels)); changeIssueDisplay(displayFromSavedView(view.display)); onTabChange('issues') }
  const openIssueFilter = (field: 'assignee'|'labels', value: string, valueLabel: string) => {
    setMilestoneScopeId('')
    setActiveSavedViewId(undefined)
    changeIssueFilters([{ id: `progress-${field}-${value || 'none'}`, field, fieldLabel: field === 'assignee' ? 'Assignee' : 'Labels', operator: 'is', value, valueLabel, values: [{ value, valueLabel }] }])
    onTabChange('issues')
  }
  const openMilestoneIssues = (milestoneId = '') => {
    setActiveSavedViewId(undefined)
    setMilestoneScopeId(milestoneId)
    onTabChange('issues')
    window.setTimeout(() => {
      const url = new URL(location.href)
      if (milestoneId) url.searchParams.set('projectMilestoneId', milestoneId)
      else url.searchParams.delete('projectMilestoneId')
      history.replaceState(history.state, '', url)
    }, 0)
  }

  useEffect(() => {
    const toggle = (event: KeyboardEvent) => {
      if ((event.target as HTMLElement | null)?.closest('input,textarea,[contenteditable="true"],[role="textbox"]')) return
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'i') { event.preventDefault(); setInsightsOpen(false); setDetailsOpen(value => !value) }
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'u') { event.preventDefault(); onTabChange('activity') }
      if (event.altKey && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        void onToggleFavorite(project.id, !favorited).catch(error => toast.error('Could not update favorite', { description: error instanceof Error ? error.message : undefined }))
      }
    }
    window.addEventListener('keydown', toggle)
    return () => window.removeEventListener('keydown', toggle)
  }, [favorited, onTabChange, onToggleFavorite, project.id, setDetailsOpen])

  const save = async (input: Parameters<ProjectDetailProps['onUpdate']>[1]) => {
    try { await onUpdate(project.id, input) }
    catch (error) { toast.error('Could not update project', { description: error instanceof Error ? error.message : undefined }) }
  }
  const toggleFavorite = async () => { try { await props.onToggleFavorite(project.id, !favorited) } catch (error) { toast.error('Could not update favorite', { description: error instanceof Error ? error.message : undefined }) } }
  const setEvents = async (events: string[]) => { try { await props.onSetSubscriptionEvents(project.id, events) } catch (error) { toast.error('Could not update project notifications', { description: error instanceof Error ? error.message : undefined }) } }

  return <main className="project-detail-page">
    <header className="project-detail-page__header">
      <button aria-label="Open workspace sidebar" className="project-detail-page__mobile-menu" onClick={onOpenSidebar} type="button"><span/><span/><span/></button>
      <a className="project-detail-page__all-projects" href={`/${location.pathname.split('/')[1]}/projects/all`}>Projects</a>
      <span aria-hidden="true" className="project-detail-page__crumb-separator">›</span>
      <a className="project-detail-page__crumb" href={`/${location.pathname.split('/')[1]}/project/${project.slugId}/overview`} onClick={event => { event.preventDefault(); onTabChange('overview') }}><ViewGlyph color={project.color} icon={project.icon || 'Project'}/><span data-i18n-ignore>{project.name}</span></a>
      <button aria-checked={favorited} aria-label={favorited ? 'Remove from favorites' : 'Add to favorites'} className="project-detail-page__header-action" data-active={favorited} onClick={() => void toggleFavorite()} role="switch" type="button"><Star fill={favorited ? 'currentColor' : 'none'} size={14}/></button>
      <ProjectActionsMenu favorited={favorited} onDelete={() => setDeleteOpen(true)} onFavorite={() => void toggleFavorite()} onRemind={remindAt => props.onCreateReminder(project.id, remindAt).then(() => undefined)} onSetEvents={setEvents} onShowActivity={() => onTabChange('activity')} onShowHistory={() => setHistoryOpen(true)} onShowNotifications={() => setNotificationOpen(true)} project={project} subscription={props.subscription}/>
      <div className="project-detail-page__header-spacer"/>
      <button aria-label="Copy page URL" className="project-detail-page__header-action" onClick={() => void navigator.clipboard.writeText(location.href).then(() => toast.success('Project URL copied'))} type="button"><Link2 size={14}/></button>
      <ProjectNotificationMenu onOpenChange={setNotificationOpen} open={notificationOpen} onSetEvents={setEvents} onUpdate={save} project={project} subscription={props.subscription}/>
    </header>

    <div className="project-detail-page__toolbar">
      <nav aria-label="Project views" className="project-detail-page__tabs">
        <ProjectTab active={tab === 'overview'} id="overview" onChange={onTabChange}>Overview</ProjectTab>
        <ProjectTab active={tab === 'activity'} id="activity" onChange={onTabChange}>Activity</ProjectTab>
        <ProjectTab active={tab === 'issues'} id="issues" onChange={onTabChange}>Issues</ProjectTab>
        {projectSavedViews.map(view => <ContextMenu.Root key={view.id}><ContextMenu.Trigger asChild><button aria-current={tab === 'issues' && activeSavedViewId === view.id ? 'page' : undefined} className="project-detail-page__saved-view-tab" data-active={tab === 'issues' && activeSavedViewId === view.id} onClick={() => openSavedView(view)} title={`${view.description || view.name} · Right-click for view actions`} type="button"><ViewGlyph color={view.color || '#8a8f98'} icon={view.icon || 'CustomView'}/><span>{view.name}</span></button></ContextMenu.Trigger><ContextMenu.Portal><ContextMenu.Content className="project-detail-page__menu"><ContextMenu.Item onSelect={() => { const name = window.prompt('Rename view', view.name)?.trim(); if (name && name !== view.name) void props.onUpdateSavedView(view.id, { name }) }}><Pencil size={13}/><span>Rename view…</span></ContextMenu.Item><ContextMenu.Separator/><ContextMenu.Item className="is-danger" onSelect={() => { if (window.confirm(`Delete view “${view.name}”?`)) void props.onDeleteSavedView(view).then(() => { if (activeSavedViewId === view.id) setActiveSavedViewId(undefined) }) }}><Trash2 size={13}/><span>Delete view</span></ContextMenu.Item></ContextMenu.Content></ContextMenu.Portal></ContextMenu.Root>)}
        {tab === 'new' ? <button aria-current="page" className="project-detail-page__new-view-tab" type="button"><Layers2 size={13}/><span>New view</span><Pencil size={10}/></button> : <button aria-label="Add new view" className="project-detail-page__add-view" onClick={() => onTabChange('new')} type="button"><AddViewIcon/></button>}
      </nav>
      <div className="project-detail-page__toolbar-actions">
        {tab === 'issues' && <><ProjectIssueFilterMenu filters={issueFilters} issues={projectIssues} onChange={changeIssueFilters}/><ProjectIssueDisplayMenu display={issueDisplay} onChange={changeIssueDisplay}/></>}
        <button aria-label={insightsOpen ? 'Close project insights' : 'Open project insights'} aria-pressed={insightsOpen} className="project-detail-page__toolbar-button ui-pill" onClick={() => { setInsightsOpen(value => !value); setDetailsOpen(false) }} type="button"><InsightsIcon/></button>
        <button aria-label={detailsOpen ? 'Close project details' : 'Open project details'} aria-pressed={detailsOpen} className="project-detail-page__toolbar-button ui-pill" onClick={() => { setDetailsOpen(value => !value); setInsightsOpen(false) }} title={`${detailsOpen ? 'Close' : 'Open'} project details (⌘I)`} type="button"><SidebarIcon/></button>
      </div>
    </div>

    <div className={`project-detail-page__workspace ${detailsOpen || insightsOpen ? 'has-details' : ''} ${tab === 'new' ? 'is-new-view' : ''}`}>
      <div className="project-detail-page__main">
        {tab === 'overview' && <ProjectOverview {...props} labels={projectLabels} onOpenMilestoneIssues={openMilestoneIssues} projectIssues={projectIssues} save={save}/>}
        {tab === 'activity' && <ProjectActivity {...props}/>}
        {tab === 'issues' && <ProjectIssues {...props} labels={issueLabels} display={issueDisplay} filters={issueFilters} milestoneScope={milestoneScope} onClearMilestoneScope={() => openMilestoneIssues()} onFiltersChange={changeIssueFilters} projectIssues={scopedProjectIssues}/>}
        {tab === 'new' && <ProjectNewView {...props} labels={issueLabels} display={issueDisplay} filters={issueFilters} onDisplayChange={changeIssueDisplay} onFiltersChange={changeIssueFilters} projectIssues={projectIssues}/>}
      </div>
      {detailsOpen && <ProjectDetailsSidebar labelGroups={labelGroups} labels={projectLabels} onConvertMilestone={props.onConvertMilestone} onCreateMilestone={props.onCreateMilestone} onDeleteMilestone={props.onDeleteMilestone} onMoveMilestone={props.onMoveMilestone} onOpenIssueFilter={openIssueFilter} onOpenMilestoneIssues={openMilestoneIssues} onReorderMilestones={props.onReorderMilestones} onTabChange={onTabChange} onUpdate={save} onUpdateProject={props.onUpdate} onUpdateMilestone={props.onUpdateMilestone} project={project} projectIssues={projectIssues} projects={projects} projectStatuses={props.projectStatuses} projectUpdates={projectUpdates} teams={props.teams} users={users} viewer={viewer}/>}
      {insightsOpen && <ProjectInsights issues={projectIssues} labels={issueLabels} users={users}/>}
    </div>

    <Dialog.Root onOpenChange={setDeleteOpen} open={deleteOpen}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby="project-delete-description" className="project-detail-page__delete-dialog">
      <Dialog.Title>Delete “{project.name}”?</Dialog.Title>
      <Dialog.Description id="project-delete-description">All of its {projectIssues.length} issues will be archived and unassociated from the project.<br/><br/>Deleted projects are available in the “Recently deleted” view for 30 days, before they are permanently deleted.</Dialog.Description>
      <footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button autoFocus className="is-danger" onClick={() => void onDelete(project.id)} type="button">Delete</button></footer>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
    <ProjectDescriptionHistoryDialog onOpenChange={setHistoryOpen} open={historyOpen} project={project}/>
  </main>
}

function ProjectTab({ active, children, id, onChange }: { active: boolean; children: string; id: ProjectDetailTab; onChange: (tab: ProjectDetailTab) => void }) {
  const projectBase = location.pathname.replace(/\/(overview|activity|issues|view\/new)$/, '')
  return <a aria-current={active ? 'page' : undefined} className="project-detail-page__tab" data-active={active} href={`${projectBase}/${id === 'new' ? 'view/new' : id}`} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); onChange(id) }}>{children}</a>
}

function useStoredBoolean(key: string, fallback: boolean) {
  const [value, setValue] = useState(() => localStorage.getItem(key) === null ? fallback : localStorage.getItem(key) === 'true')
  const update = (next: boolean | ((current: boolean) => boolean)) => setValue(current => { const resolved = typeof next === 'function' ? next(current) : next; localStorage.setItem(key, String(resolved)); return resolved })
  return [value, update] as const
}

function readIssueFilters(key: string): ProjectIssueFilters { try { const value = JSON.parse(localStorage.getItem(`${key}:filters`) ?? '[]'); return Array.isArray(value) ? value : [] } catch { return [] } }
function readIssueDisplay(key: string) { try { const value = JSON.parse(localStorage.getItem(`${key}:display`) ?? 'null'); return value ? { ...DEFAULT_PROJECT_ISSUE_DISPLAY, ...value, properties: new Set(value.properties ?? [...DEFAULT_PROJECT_ISSUE_DISPLAY.properties]) } : DEFAULT_PROJECT_ISSUE_DISPLAY } catch { return DEFAULT_PROJECT_ISSUE_DISPLAY } }
function displayFromSavedView(value: Record<string, unknown>) { return { ...DEFAULT_PROJECT_ISSUE_DISPLAY, ...value, properties: new Set(Array.isArray(value.properties) ? value.properties : [...DEFAULT_PROJECT_ISSUE_DISPLAY.properties]) } }
function filtersFromSavedView(value: unknown[], labels: ProjectDetailProps['labels']): ProjectIssueFilters {
  const labelsById = new Map(labels.map(label => [label.id, label]))
  return value.flatMap((item, index) => {
    if (!item || typeof item !== 'object') return []
    const raw = item as { field?: string; operator?: string; valueLabel?: string; color?: string; values?: unknown[] }
    if (raw.field === 'project') return []
    const field = raw.field === 'label' ? 'labels' : raw.field
    if (!['status','assignee','priority','labels'].includes(field ?? '')) return []
    const values = (raw.values ?? []).flatMap(entry => {
      const stored = typeof entry === 'string' ? { value: entry } : entry && typeof entry === 'object' ? entry as { value?: unknown; id?: unknown; valueLabel?: unknown; label?: unknown; color?: unknown } : undefined
      const id = stored?.value ?? stored?.id
      if (typeof id !== 'string' || !id) return []
      const label = field === 'labels' ? labelsById.get(id) : undefined
      return [{ value: id, valueLabel: label?.name ?? (typeof stored?.valueLabel === 'string' ? stored.valueLabel : typeof stored?.label === 'string' ? stored.label : raw.valueLabel ?? id), color: label?.color ?? (typeof stored?.color === 'string' ? stored.color : raw.color) }]
    })
    if (!values.length) return []
    return [{ id: `saved-${index}`, field: field as ProjectIssueFilters[number]['field'], fieldLabel: ({status:'Status',assignee:'Assignee',priority:'Priority',labels:'Labels'} as Record<string,string>)[field!], operator: raw.operator === 'isNot' ? 'isNot' as const : 'is' as const, ...values[0], values }]
  })
}
