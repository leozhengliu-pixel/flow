import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Check, ChevronDown, ChevronRight, Copy, Edit3, Link2, MoreHorizontal, Paperclip, PanelRightClose, PanelRightOpen, Plus, Send, Sparkles, Star, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { NoAssigneeIcon, PriorityIcon } from '@/components/issue/issue-icons'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { ViewGlyph, ViewIconPicker } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { ProjectPropertyPicker, ProjectStatusGlyph, type ProjectPropertyOption } from '@/components/projects-page/project-property-picker'
import { ProjectUpdatesPreview } from '@/components/projects-page/project-updates-preview'
import { NewProjectDialog, type NewProjectDraft } from '@/components/projects-page/new-project-dialog'
import type { ProjectCreateInput, ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { Initiative, InitiativeMutationInput, InitiativeResource, InitiativeUpdate, IssueLabel, LabelGroup, Project, ProjectStatus, ProjectTemplate, ProjectUpdate, SavedView, SavedViewMutationInput, Team, User } from '@/types/flow'
import type { InitiativeRouteTab } from '@/lib/app-routes'
import { InitiativeLabelsPicker, InitiativeProperties, ProjectAssociationPicker } from './initiative-shared'
import { DisplayIcon as SlidersHorizontal, FilterIcon as Filter } from '@/components/ui/view-action-icons'
import { AddProjectMenu, InitiativeActionsMenu, InitiativeNotificationMenu } from './initiative-header-menus'
import { InitiativeResources } from './initiative-resources'
import { formatTarget, titleCase } from './initiative-model'
import { labelsForResource } from '@/lib/labels'
import './initiatives.css'

type Props = {
  initiative: Initiative
  initiatives: Initiative[]
  initiativeUpdates: InitiativeUpdate[]
  projects: Project[]
  projectUpdates: Record<string, ProjectUpdate[]>
  users: User[]
  teams: Team[]
  projectStatuses: ProjectStatus[]
  projectTemplates: ProjectTemplate[]
  labels: IssueLabel[]
  labelGroups: LabelGroup[]
  viewer: User
  tab: InitiativeRouteTab
  viewId?: string
  onBack: () => void
  onTabChange: (tab: InitiativeRouteTab) => void
  onOpenView: (viewId: string) => void
  onOpenProject: (project: Project) => void
  onCreateProject: (input: ProjectCreateInput) => Promise<Project>
  onCreateLabel: (name: string) => Promise<IssueLabel>
  onUpdateProject: (id: string, input: ProjectMutationInput) => Promise<Project>
  onCreateProjectUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onUpdateProjectUpdate: (projectId: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<ProjectUpdate>
  onDeleteProjectUpdate: (projectId: string, updateId: string) => Promise<void>
  onCommentProjectUpdate: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>
  onReactProjectUpdate: (projectId: string, updateId: string, emoji: string) => Promise<ProjectUpdate>
  onUpdate: (id: string, input: InitiativeMutationInput) => Promise<Initiative>
  onDelete: (id: string) => Promise<void>
  onCreateReminder: (id: string, remindAt: string) => Promise<unknown>
  onCreateUpdate: (id: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onUpdateInitiativeUpdate: (id: string, updateId: string, input: { body?: string; health?: Project['health'] }) => Promise<InitiativeUpdate>
  onDeleteUpdate: (id: string, updateId: string) => Promise<void>
  onComment: (id: string, body: string) => Promise<void>
  onUpdateComment: (id: string, commentId: string, body: string) => Promise<unknown>
  onDeleteComment: (id: string, commentId: string) => Promise<void>
  onReactComment: (id: string, commentId: string, emoji: string) => Promise<unknown>
  onCreateResource: (id: string, input: { type?: 'link' | 'document'; title?: string; url?: string; documentId?: string }) => Promise<InitiativeResource>
  onUpdateResource: (id: string, resourceId: string, input: { type?: 'link' | 'document'; title?: string; url?: string; documentId?: string }) => Promise<InitiativeResource>
  onDeleteResource: (id: string, resourceId: string) => Promise<void>
  savedViews: SavedView[]
  onCreateSavedView: (input: SavedViewMutationInput) => Promise<SavedView>
  onUpdateSavedView: (id: string, input: SavedViewMutationInput) => Promise<SavedView>
  onDeleteSavedView: (view: SavedView) => Promise<void>
  onOpenSidebar?: () => void
}

type TimelineZoom = 'Year' | 'Quarter' | 'Month' | 'Week'
type InitiativeStoredView = SavedView & { slugId: string; icon: string; color: string; zoom: TimelineZoom; query: string; health?: Project['health']; properties: { health: boolean; priority: boolean; lead: boolean } }
type InitiativeViewDraft = Pick<InitiativeStoredView, 'name'|'description'|'icon'|'color'|'zoom'|'query'|'health'|'properties'> & { slugId?: string; favorite?: boolean }

export function InitiativeDetailPage(props: Props) {
  const { initiative, tab, onTabChange, onUpdate } = props
  const [detailsOpen, setDetailsOpen] = useStoredBoolean(`flow:initiative:${initiative.id}:details`, false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [projectCreateOpen, setProjectCreateOpen] = useState(false)
  const [roadmapQuery, setRoadmapQuery] = useState('')
  const [roadmapZoom, setRoadmapZoom] = useState<TimelineZoom>('Year')
  const [roadmapHealth, setRoadmapHealth] = useState<Project['health']>()
  const [roadmapProperties, setRoadmapProperties] = useState({ health: true, priority: true, lead: true })
  const [activityDisplay, setActivityDisplay] = useState({ updates: true, comments: true, activity: true })
  const storedViews = useMemo(() => props.savedViews.filter(view => savedViewMatchesInitiative(view, initiative.id)).map(initiativeViewFromSavedView), [initiative.id, props.savedViews])
  const [editingView, setEditingView] = useState<InitiativeStoredView>()
  const [deletingView, setDeletingView] = useState<InitiativeStoredView>()
  const activeView = tab === 'view' ? storedViews.find(view => view.slugId === props.viewId || view.id === props.viewId) : undefined
  const update = (input: InitiativeMutationInput) => onUpdate(initiative.id, input)
  const initiativeLabels = useMemo(() => labelsForResource(props.labels, 'initiative', props.labelGroups), [props.labelGroups, props.labels])
  const projectLabels = useMemo(() => labelsForResource(props.labels, 'project', props.labelGroups), [props.labelGroups, props.labels])
  const projectLabelGroupNames = useMemo(() => new Map(props.labelGroups.filter(group => group.resourceType === 'project').map(group => [group.id, group.name])), [props.labelGroups])

  useEffect(() => {
    if (!activeView) return
    setRoadmapQuery(activeView.query); setRoadmapZoom(activeView.zoom); setRoadmapHealth(activeView.health); setRoadmapProperties(activeView.properties)
  }, [activeView])

  const createStoredView = async (draft: InitiativeViewDraft) => {
    const created = await props.onCreateSavedView(initiativeViewMutation(initiative.id, draft))
    props.onOpenView(initiativeViewFromSavedView(created).slugId)
  }
  const updateStoredView = async (view: InitiativeStoredView) => {
    await props.onUpdateSavedView(view.id, initiativeViewMutation(initiative.id, view))
  }
  const duplicateStoredView = async (view: InitiativeStoredView) => {
    const name = `${view.name} copy`
    const created = await props.onCreateSavedView(initiativeViewMutation(initiative.id, { ...view, favorite: false, name, slugId: undefined }))
    props.onOpenView(initiativeViewFromSavedView(created).slugId)
  }
  const deleteStoredView = async (view: InitiativeStoredView) => {
    await props.onDeleteSavedView(view)
    setDeletingView(undefined)
    if (activeView?.id === view.id) onTabChange('projects')
  }
  const persistViewDisplay = (changes: Partial<Pick<InitiativeStoredView, 'zoom'|'query'|'health'|'properties'>>) => {
    if (!activeView) return
    void props.onUpdateSavedView(activeView.id, initiativeViewMutation(initiative.id, { ...activeView, ...changes }))
  }
  const changeRoadmapQuery = (query: string) => { setRoadmapQuery(query); persistViewDisplay({ query }) }
  const changeRoadmapHealth = (health?: Project['health']) => { setRoadmapHealth(health); persistViewDisplay({ health }) }
  const changeRoadmapProperties = (properties: typeof roadmapProperties) => { setRoadmapProperties(properties); persistViewDisplay({ properties }) }
  const changeRoadmapZoom = (zoom: TimelineZoom) => { setRoadmapZoom(zoom); persistViewDisplay({ zoom }) }

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'i') return
      if ((event.target as HTMLElement | null)?.closest('input,textarea,[contenteditable="true"],[role="textbox"]')) return
      event.preventDefault(); setDetailsOpen(value => !value)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [setDetailsOpen])

  return <main className="main-panel li-detail">
    <header className="li-detail-header">
      <button className="li-mobile-menu" onClick={props.onOpenSidebar} type="button">☰</button>
      <button className="li-detail-all" onClick={props.onBack} type="button">Initiatives</button><ChevronRight className="li-detail-separator" size={12}/>
      <button className="li-detail-crumb" data-i18n-ignore onClick={() => onTabChange('overview')} type="button"><ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/><strong>{initiative.name}</strong></button>
      <button aria-checked={initiative.favorite} aria-label="Add to favorites" className={initiative.favorite ? 'is-active' : ''} onClick={() => update({ favorite: !initiative.favorite })} role="switch" type="button"><Star fill={initiative.favorite ? 'currentColor' : 'none'} size={14}/></button>
      <InitiativeActionsMenu initiative={initiative} onCreateReminder={remindAt => props.onCreateReminder(initiative.id, remindAt)} onDelete={() => setDeleteOpen(true)} onNewUpdate={() => onTabChange('activity')} onShowActivity={() => onTabChange('activity')} onUpdate={update}/>
      <span/>
      <button aria-label="Copy page URL" onClick={() => void navigator.clipboard.writeText(window.location.href).then(() => toast.success('Initiative URL copied'))} type="button"><Link2 size={14}/></button>
      <InitiativeNotificationMenu initiative={initiative} onUpdate={update}/>
      {tab !== 'activity' && <AddProjectMenu initiative={initiative} projects={props.projects} onCreateNew={() => setProjectCreateOpen(true)} onUpdate={update}/>} 
    </header>
    <div className="li-detail-toolbar"><nav>{(['overview', 'activity', 'projects'] as InitiativeRouteTab[]).map(item => <a aria-current={tab === item ? 'page' : undefined} href={location.pathname.replace(/\/(overview|activity|projects|view\/[^/]+)$/, `/${item}`)} key={item} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); onTabChange(item) }}>{titleCase(item)}</a>)}{storedViews.map(view => <ContextMenu.Root key={view.id}><ContextMenu.Trigger asChild><a aria-current={activeView?.id === view.id ? 'page' : undefined} aria-label={view.name} className="li-saved-view-tab" data-i18n-ignore href={location.pathname.replace(/\/(overview|activity|projects|view\/[^/]+)$/, `/view/${view.slugId}`)} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey) return; event.preventDefault(); props.onOpenView(view.slugId) }}><ViewGlyph color={view.color} icon={view.icon}/><span>{view.name}</span></a></ContextMenu.Trigger><ContextMenu.Portal><ContextMenu.Content className="li-menu li-saved-view-menu"><ContextMenu.Item onSelect={() => void navigator.clipboard.writeText(`${location.origin}${location.pathname.replace(/\/(overview|activity|projects|view\/[^/]+)$/, `/view/${view.slugId}`)}`)}><Copy size={14}/>Copy link</ContextMenu.Item><ContextMenu.Item onSelect={() => void updateStoredView({ ...view, favorite: !view.favorite })}><Star fill={view.favorite ? 'currentColor' : 'none'} size={14}/>{view.favorite ? 'Unfavorite' : 'Favorite'}</ContextMenu.Item><ContextMenu.Separator/><ContextMenu.Item onSelect={() => setEditingView(view)}><Edit3 size={14}/>Edit…</ContextMenu.Item><ContextMenu.Item onSelect={() => void duplicateStoredView(view)}><Copy size={14}/>Duplicate…</ContextMenu.Item><ContextMenu.Item className="danger" onSelect={() => setDeletingView(view)}><Trash2 size={14}/>Delete</ContextMenu.Item></ContextMenu.Content></ContextMenu.Portal></ContextMenu.Root>)}{tab === 'new' ? <a aria-current="page" className="li-new-view-tab" href={location.pathname}><ViewGlyph color="#8a8f98" icon="CustomView"/><span>New view</span><Edit3 size={11}/></a> : <button aria-label="Add new view" onClick={() => onTabChange('new')} type="button"><ViewGlyph color="#8a8f98" icon="CustomView"/></button>}</nav><div>
      {tab === 'activity' && <ActivityDisplayMenu value={activityDisplay} onChange={setActivityDisplay}/>} 
      {(tab === 'projects' || tab === 'view') && <><RoadmapFilterMenu health={roadmapHealth} query={roadmapQuery} onHealth={changeRoadmapHealth} onQuery={changeRoadmapQuery}/><RoadmapDisplayMenu properties={roadmapProperties} onChange={changeRoadmapProperties}/></>}
      <button aria-expanded={detailsOpen} aria-label={detailsOpen ? 'Close Initiative details' : 'Open Initiative details'} onClick={() => setDetailsOpen(open => !open)} title={`${detailsOpen ? 'Close' : 'Open'} Initiative details (⌘I)`} type="button">{detailsOpen ? <PanelRightClose size={15}/> : <PanelRightOpen size={15}/>}</button>
    </div></div>
    <div className={`li-detail-body is-${tab} ${detailsOpen ? 'has-details' : ''}`}><section className="li-detail-main">{tab === 'overview' && <InitiativeOverview {...props} update={update}/>} {tab === 'activity' && <InitiativeActivity {...props} display={activityDisplay}/>} {(tab === 'projects' || tab === 'view') && <InitiativeRoadmap {...props} healthFilter={roadmapHealth} properties={roadmapProperties} query={roadmapQuery} zoom={roadmapZoom} onZoom={changeRoadmapZoom}/>} {tab === 'new' && <InitiativeNewView {...props} onCancel={() => onTabChange('projects')} onSave={createStoredView}/>}</section>{detailsOpen && <InitiativeSidebar {...props} labels={initiativeLabels} update={update}/>}</div>
    <DeleteInitiativeDialog initiative={initiative} open={deleteOpen} onOpenChange={setDeleteOpen} onDelete={async () => { await props.onDelete(initiative.id); props.onBack() }}/>
    <StoredViewEditDialog open={Boolean(editingView)} view={editingView} onOpenChange={open => { if (!open) setEditingView(undefined) }} onSave={async view => { await updateStoredView(view); setEditingView(undefined); if (activeView?.id === view.id) props.onOpenView(view.slugId) }}/>
    <StoredViewDeleteDialog open={Boolean(deletingView)} view={deletingView} onOpenChange={open => { if (!open) setDeletingView(undefined) }} onDelete={() => deletingView ? deleteStoredView(deletingView) : Promise.resolve()}/>
    <NewProjectDialog dependencies={props.projects.map(project => ({ id: project.id, label: project.name, color: project.color }))} labels={projectLabels.map(label => ({ id: label.id, label: label.name, color: label.color, groupId: label.groupId, groupLabel: label.groupId ? projectLabelGroupNames.get(label.groupId) : undefined }))} leads={props.users.map(user => ({ id: user.id, label: user.displayName || user.name }))} members={props.users.map(user => ({ id: user.id, label: user.displayName || user.name }))} statuses={props.projectStatuses.map(status=>({id:status.name,label:status.name,color:status.color,icon:<ProjectStatusGlyph color={status.color} name={status.name} type={status.type}/> }))} templates={props.projectTemplates.map(template => ({ id: template.id, label: template.name, name: template.name, description: template.description, summary: template.summary, icon: template.icon, color: template.color, status: props.projectStatuses.find(status => status.id === template.statusId)?.name, priority: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][template.priority] ?? 'No priority', teamIds: template.teamIds, labelIds: template.labelIds.filter(id => projectLabels.some(label => label.id === id)) }))} onClose={() => setProjectCreateOpen(false)} onCreate={async draft => { const project = await props.onCreateProject(projectDraftMutation(draft, props.projects, props.projectStatuses)); await update({ projectIds: [...new Set([...initiative.projectIds, project.id])] }); setProjectCreateOpen(false) }} open={projectCreateOpen} teamLabel={props.teams[0]?.key ?? 'Team'} teams={props.teams.map(team => ({ id: team.id, label: team.name, color: team.color }))}/>
  </main>
}

function InitiativeOverview(props: Props & { update: (input: InitiativeMutationInput) => Promise<Initiative> }) {
  const { initiative, projects, projectUpdates, users, update, onOpenProject, onTabChange } = props
  const [projectProperties, setProjectProperties] = useState(() => new Set(['health', 'priority', 'lead', 'target', 'status']))
  const [projectSort, setProjectSort] = useState<'name' | 'health' | 'priority' | 'target' | 'status'>('name')
  const linked = projects.filter(project => initiative.projectIds.includes(project.id)).sort((a, b) => {
    if (projectSort === 'name') return a.name.localeCompare(b.name)
    if (projectSort === 'health') return a.health.localeCompare(b.health)
    if (projectSort === 'priority') return a.priority - b.priority
    if (projectSort === 'target') return (a.targetDate ?? '').localeCompare(b.targetDate ?? '')
    return a.status.name.localeCompare(b.status.name)
  })
  const projectColumns = ['health', 'priority', 'lead', 'target', 'status'].filter(property => projectProperties.has(property))
  const projectGrid = { gridTemplateColumns: `minmax(200px,1fr) ${projectColumns.map(property => property === 'health' ? '115px' : property === 'priority' ? '90px' : property === 'lead' ? '80px' : '105px').join(' ')}` }
  return <div className="li-overview">
    <div className="li-overview-title"><ViewIconPicker color={initiative.color} icon={initiative.icon || 'Initiative'} onChange={update} triggerClassName="li-overview-icon"/><InitiativeEditableText className="li-title-input" label="Initiative name" value={initiative.name} onCommit={name => update({ name })}/><InitiativeEditableText className="li-summary-input" label="Initiative summary" placeholder="Add a short summary…" value={initiative.summary} onCommit={summary => update({ summary })}/></div>
    <section><h3>Properties</h3><InitiativeProperties initiative={initiative} teams={props.teams} users={users} onUpdate={update}/><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="More properties" className="li-more-properties" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu"><DropdownMenu.Item onSelect={() => document.querySelector<HTMLElement>('.li-detail-sidebar [aria-label="Add labels"]')?.click()}>Labels</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></section>
    <InitiativeResources initiativeId={initiative.id} resources={initiative.resources} onCreate={props.onCreateResource} onUpdate={props.onUpdateResource} onDelete={props.onDeleteResource}/>
    <button className="li-first-update" onClick={() => onTabChange('activity')} type="button"><Send size={15}/>{props.initiativeUpdates.length ? 'View initiative updates' : 'Write first initiative update'}</button>
    <section className="li-description"><h3>Description</h3><EditableArea label="Initiative description" placeholder="Add description…" value={initiative.description} onCommit={description => update({ description })}/></section>
    <section className="li-overview-projects"><header><h3>Projects</h3><span/><OverviewProjectDisplayMenu properties={projectProperties} onChange={setProjectProperties}/><ProjectAssociationPicker initiative={initiative} projects={projects} onUpdate={update}><button type="button"><Plus size={14}/> Add a project</button></ProjectAssociationPicker></header>{linked.length ? <><div className="li-project-columns" style={projectGrid}><button onClick={() => setProjectSort('name')} type="button">Name</button>{projectColumns.map(property => property === 'lead' ? <span key={property}>Lead</span> : <button key={property} onClick={() => setProjectSort(property as typeof projectSort)} type="button">{property === 'target' ? 'Target date' : titleCase(property)}</button>)}</div><div className="li-project-group"><button aria-label="Collapse group" type="button"><ChevronDown size={12}/>In Progress</button>{linked.map(project => <button className="li-detail-project-row" key={project.id} onClick={() => onOpenProject(project)} style={projectGrid} type="button"><span className="li-project-primary"><i><ViewGlyph color={project.color} icon={normalizeProjectIcon(project.icon)}/></i><strong data-i18n-ignore>{project.name}</strong></span>{projectColumns.map(property => property === 'health' ? <span className={`li-health is-${project.health}`} key={property}><i/>{projectUpdates[project.id]?.length ? healthLabel(project.health) : 'No updates'}</span> : property === 'priority' ? <span key={property}><PriorityIcon priority={project.priority} size={13}/>{project.priorityLabel}</span> : property === 'lead' ? <span key={property}>{project.lead ? <Avatar name={project.lead.displayName || project.lead.name}/> : ''}</span> : property === 'target' ? <span key={property}>{project.targetDate ? formatTarget(project.targetDate) : ''}</span> : <span key={property}><ProjectStatusGlyph name={project.status.name} type={project.status.type}/><span data-i18n-ignore>{project.status.name}</span></span>)}</button>)}</div></> : <div className="li-overview-projects-empty">No projects in this initiative</div>}</section>
  </div>
}

function InitiativeActivity(props: Props & { display: { updates: boolean; comments: boolean; activity: boolean } }) {
  const { initiative, initiativeUpdates, viewer, onCreateUpdate, onUpdateInitiativeUpdate, onDeleteUpdate, onComment } = props
  const [mode, setMode] = useState<'comment' | 'update'>('update')
  const [body, setBody] = useState('')
  const [health, setHealth] = useState<Project['health']>(initiative.health === 'noUpdate' ? 'onTrack' : initiative.health)
  const [saving, setSaving] = useState(false)
  const [editing, setEditing] = useState<InitiativeUpdate>()
  const [editingComment, setEditingComment] = useState<Initiative['comments'][number]>()
  const feed = useMemo(() => [...initiativeUpdates.map(update => ({ type: 'update' as const, date: update.createdAt, update })), ...initiative.comments.map(comment => ({ type: 'comment' as const, date: comment.createdAt, comment }))].sort((a, b) => +new Date(b.date) - +new Date(a.date)), [initiative.comments, initiativeUpdates])
  const submit = async () => { if (!body.trim() || saving) return; setSaving(true); try { if (mode === 'comment') await onComment(initiative.id, body.trim()); else await onCreateUpdate(initiative.id, { body: body.trim(), health }); setBody('') } finally { setSaving(false) } }
  return <div className="li-activity"><div className="li-activity-composer"><header><div role="tablist"><button aria-selected={mode === 'comment'} onClick={() => setMode('comment')} role="tab" type="button">Comment</button><button aria-selected={mode === 'update'} onClick={() => setMode('update')} role="tab" type="button">Update</button>{mode === 'update' && <InitiativeHealthMenu health={health} onHealth={setHealth}/>}</div></header><textarea aria-label={mode === 'update' ? 'Initiative update' : 'Initiative comment'} placeholder={mode === 'update' ? 'Write an initiative update…' : 'Leave a comment…'} value={body} onChange={event => setBody(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void submit() }}/><footer><button aria-disabled="true" className="li-agent-write" disabled title="Flow AI is not configured for this workspace" type="button"><Sparkles size={13}/>Write with Agent</button><span/><button aria-disabled="true" aria-label="Attach images, files, or videos" disabled title="Initiative update attachments are not available" type="button"><Paperclip size={14}/></button><button disabled={!body.trim() || saving} onClick={() => void submit()} type="button">{mode === 'update' ? 'Post update' : 'Comment'}</button></footer></div>
    <div className="li-feed">{feed.map(item => item.type === 'update' ? props.display.updates && <article className="li-update-card" key={item.update.id}><header><Avatar name={item.update.user.displayName || item.update.user.name}/><strong data-i18n-ignore>{item.update.user.displayName || item.update.user.name}</strong><span>{formatDistanceToNowStrict(new Date(item.update.createdAt), { addSuffix: true })}{item.update.editedAt ? ' · edited' : ''}</span><span className={`li-update-health is-${item.update.health}`}/><small>{healthLabel(item.update.health)}</small><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Update actions" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu"><DropdownMenu.Item onSelect={() => { setEditing(item.update); setBody('') }}>Edit update</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={() => onDeleteUpdate(initiative.id, item.update.id)}><Trash2 size={14}/>Delete update</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></header>{editing?.id === item.update.id ? <InlineUpdateEditor update={editing} onCancel={() => setEditing(undefined)} onSave={async input => { await onUpdateInitiativeUpdate(initiative.id, editing.id, input); setEditing(undefined) }}/> : <p data-i18n-ignore>{item.update.body}</p>}</article> : props.display.comments && <article className="li-comment-card" key={item.comment.id}><Avatar name={item.comment.user.displayName || item.comment.user.name}/><div><header><strong data-i18n-ignore>{item.comment.user.displayName || item.comment.user.name}</strong><span>{formatDistanceToNowStrict(new Date(item.comment.createdAt), { addSuffix: true })}{item.comment.editedAt ? ' · edited' : ''}</span><EmojiPicker align="end" onSelect={async emoji => { await props.onReactComment(initiative.id, item.comment.id, emoji) }}><button aria-label="Add reaction" type="button">☺</button></EmojiPicker><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Comment actions" type="button"><MoreHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu"><DropdownMenu.Item onSelect={() => setEditingComment(item.comment)}>Edit comment</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={() => props.onDeleteComment(initiative.id, item.comment.id)}><Trash2 size={14}/>Delete comment</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></header>{editingComment?.id === item.comment.id ? <InlineCommentEditor comment={editingComment} onCancel={() => setEditingComment(undefined)} onSave={async value => { await props.onUpdateComment(initiative.id, item.comment.id, value); setEditingComment(undefined) }}/> : <p data-i18n-ignore>{item.comment.body}</p>}<ReactionPills reactions={item.comment.reactions} viewerId={viewer.id} onToggle={async emoji => { await props.onReactComment(initiative.id, item.comment.id, emoji) }}/></div></article>)}{props.display.activity && <div className="li-created-event"><ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/><span><strong data-i18n-ignore>{viewer.displayName || viewer.name}</strong> created the initiative · {new Date(initiative.createdAt).toLocaleDateString('en', { month: 'short', day: 'numeric' })}</span></div>}</div>
  </div>
}

function InitiativeRoadmap({ initiative, projects, projectUpdates, projectStatuses, users, viewer, onOpenProject, onUpdateProject, onCreateProjectUpdate, onUpdateProjectUpdate, onDeleteProjectUpdate, onCommentProjectUpdate, onReactProjectUpdate, healthFilter, properties, query, zoom, onZoom }: Props & { healthFilter?: Project['health']; properties: { health: boolean; priority: boolean; lead: boolean }; query: string; zoom: TimelineZoom; onZoom: (zoom: TimelineZoom) => void }) {
  const root = useRef<HTMLDivElement>(null)
  const [updatesProjectId, setUpdatesProjectId] = useState<string>()
  const [roadmapDrag, setRoadmapDrag] = useState<{ id: string; startX: number; start: Date; target: Date }>()
  const roadmapDragged = useRef(false)
  const today = new Date()
  const start = new Date(today.getFullYear(), today.getMonth() - 11, 1)
  const months = Array.from({ length: 24 }, (_, index) => new Date(start.getFullYear(), start.getMonth() + index, 1))
  const ticks: Date[] = []
  const timelineEnd = new Date(start.getFullYear(), start.getMonth() + months.length, 1)
  for (let date = new Date(start); date < timelineEnd; date = new Date(date.getFullYear(), date.getMonth(), date.getDate() + 14)) ticks.push(date)
  const linked = projects.filter(project => initiative.projectIds.includes(project.id) && (!healthFilter || project.health === healthFilter) && `${project.name} ${project.summary}`.toLowerCase().includes(query.trim().toLowerCase()))
  const monthWidth = ({ Year: 82, Quarter: 132, Month: 240, Week: 520 } as const)[zoom]
  const statusOptions = projectStatuses.map<ProjectPropertyOption>((status, index) => ({ color: status.color, label: status.name, shortcut: String(index + 1), statusType: status.type, value: status.name }))
  const priorityOptions: ProjectPropertyOption[] = ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, index) => ({ label, shortcut: String(index), value: ['none', 'urgent', 'high', 'medium', 'low'][index] }))
  const leadOptions: ProjectPropertyOption[] = [{ label: 'No lead', shortcut: '0', value: '' }, ...users.filter(user => user.active).map(user => ({ avatarUrl: user.avatarUrl, group: 'Users from the project team', keywords: `${user.name} ${user.email}`, label: user.displayName || user.name, value: user.id }))]
  const todayOffset = (today.getFullYear() - start.getFullYear()) * 12 + today.getMonth() - start.getMonth() + (today.getDate() - 1) / 31
  const centerToday = useCallback(() => { const element = root.current; if (element) element.scrollTo({ left: Math.max(0, 315 + todayOffset * monthWidth - element.clientWidth / 2), behavior: 'smooth' }) }, [monthWidth, todayOffset])
  useEffect(() => { const frame = requestAnimationFrame(centerToday); return () => cancelAnimationFrame(frame) }, [centerToday])
  const updateProperty = (project: Project, property: 'status' | 'priority' | 'lead', value: string) => {
    if (property === 'status') return onUpdateProject(project.id, { statusId: projectStatuses.find(status => status.name === value)?.id })
    if (property === 'priority') return onUpdateProject(project.id, { priority: ({ none: 0, urgent: 1, high: 2, medium: 3, low: 4 } as Record<string, number>)[value] ?? 0 })
    return onUpdateProject(project.id, { leadId: value })
  }
  return <><div className="li-roadmap" ref={root} style={{ '--li-month-width': `${monthWidth}px`, '--li-month-count': months.length, '--li-today-left': `${315 + todayOffset * monthWidth}px` } as React.CSSProperties}><div className="li-roadmap-canvas"><div className="li-roadmap-top"><div className="li-roadmap-rail"/><div className="li-roadmap-months">{months.map(date => <span key={date.toISOString()}>{date.toLocaleDateString('en', { month: 'short' })}{date.getMonth() === 0 && <small>{date.getFullYear()}</small>}</span>)}</div><div className="li-roadmap-ticks">{ticks.map(date => <span key={date.toISOString()} style={{ left: `${315 + ((+date - +start) / 86400000 / 30.44) * monthWidth}px` }}>{date.getDate()}</span>)}</div><button aria-label="Center the timeline on today’s date" onClick={centerToday} type="button">Today</button><ZoomMenu onZoom={onZoom} zoom={zoom}/></div><div className="li-roadmap-grid">{ticks.map(date => <i key={date.toISOString()} style={{ left: `${315 + ((+date - +start) / 86400000 / 30.44) * monthWidth}px` }}/>)}</div>{linked.length ? <div className="li-roadmap-projects">{linked.map(project => <div className="li-roadmap-row" key={project.id}><div className="li-roadmap-project-cell"><ViewIconPicker color={project.color} icon={normalizeProjectIcon(project.icon)} onChange={visual => void onUpdateProject(project.id, visual)} triggerClassName="li-roadmap-project-icon"/><button aria-label={`Open ${project.name}`} className="li-roadmap-project-name" onClick={() => onOpenProject(project)} type="button"><strong data-i18n-ignore>{project.name}</strong></button><div className="li-roadmap-metadata"><button aria-label={projectUpdates[project.id]?.length ? `Open updates for ${project.name}` : 'There are no updates for this project'} className={`li-roadmap-property ${properties.health ? '' : 'is-hidden'}`} onClick={() => setUpdatesProjectId(project.id)} type="button"><i className={`li-update-health is-${projectUpdates[project.id]?.length ? project.health : 'noUpdate'}`}/></button><ProjectPropertyPicker buttonClassName="li-roadmap-property" label={`Change ${project.name} status`} onChange={value => void updateProperty(project, 'status', value)} options={statusOptions} property="status" value={project.status.name}><ProjectStatusGlyph color={project.status.color} name={project.status.name} progress={project.progress} type={project.status.type}/></ProjectPropertyPicker><ProjectPropertyPicker buttonClassName={`li-roadmap-property ${properties.priority ? '' : 'is-hidden'}`} label={`${project.priorityLabel} Priority`} onChange={value => void updateProperty(project, 'priority', value)} options={priorityOptions} property="priority" value={['none', 'urgent', 'high', 'medium', 'low'][project.priority] ?? 'none'}><PriorityIcon priority={project.priority} size={15}/></ProjectPropertyPicker><ProjectPropertyPicker buttonClassName={`li-roadmap-property ${properties.lead ? '' : 'is-hidden'}`} label={project.lead?.displayName || project.lead?.name || 'No lead'} onChange={value => void updateProperty(project, 'lead', value)} options={leadOptions} property="lead" value={project.lead?.id ?? ''}>{project.lead ? <Avatar name={project.lead.displayName || project.lead.name}/> : <NoAssigneeIcon size={15}/>}</ProjectPropertyPicker></div></div><div aria-label={`${project.name} timeline bar`} className="li-roadmap-bar" data-dragging={roadmapDrag?.id === project.id || undefined} onClick={() => { if (!roadmapDragged.current) onOpenProject(project); roadmapDragged.current = false }} onKeyDown={event => { if (!['ArrowLeft','ArrowRight'].includes(event.key)) return; event.preventDefault(); const days = event.key === 'ArrowLeft' ? -1 : 1; const projectStart = project.startDate ? new Date(`${project.startDate}T00:00:00`) : new Date(); const projectTarget = project.targetDate ? new Date(`${project.targetDate}T00:00:00`) : new Date(projectStart.getFullYear(), projectStart.getMonth() + 2, projectStart.getDate()); projectStart.setDate(projectStart.getDate() + days); projectTarget.setDate(projectTarget.getDate() + days); void onUpdateProject(project.id, { startDate: isoRoadmapDate(projectStart), targetDate: isoRoadmapDate(projectTarget) }) }} onPointerDown={event => { event.preventDefault(); event.currentTarget.setPointerCapture(event.pointerId); roadmapDragged.current = false; const projectStart = project.startDate ? new Date(`${project.startDate}T00:00:00`) : new Date(); const projectTarget = project.targetDate ? new Date(`${project.targetDate}T00:00:00`) : new Date(projectStart.getFullYear(), projectStart.getMonth() + 2, projectStart.getDate()); setRoadmapDrag({ id: project.id, startX: event.clientX, start: projectStart, target: projectTarget }) }} onPointerMove={event => { if (roadmapDrag?.id !== project.id) return; const delta = event.clientX - roadmapDrag.startX; if (Math.abs(delta) > 3) roadmapDragged.current = true; event.currentTarget.style.transform = `translateX(${delta}px)` }} onPointerCancel={event => { if (roadmapDrag?.id !== project.id) return; event.currentTarget.style.removeProperty('transform'); setRoadmapDrag(undefined); roadmapDragged.current = false }} onPointerUp={event => { if (roadmapDrag?.id !== project.id) return; const days = Math.round((event.clientX - roadmapDrag.startX) / monthWidth * 30.44); const nextStart = new Date(roadmapDrag.start); const nextTarget = new Date(roadmapDrag.target); nextStart.setDate(nextStart.getDate() + days); nextTarget.setDate(nextTarget.getDate() + days); event.currentTarget.style.removeProperty('transform'); setRoadmapDrag(undefined); if (days) void onUpdateProject(project.id, { startDate: isoRoadmapDate(nextStart), targetDate: isoRoadmapDate(nextTarget) }) }} role="button" style={roadmapPosition(project, start, monthWidth)} tabIndex={0}><span data-i18n-ignore>{project.name}</span>{!projectUpdates[project.id]?.length && <i/>}</div></div>)}</div> : <div className="li-roadmap-empty">No projects match this view</div>}<div className="li-today-line"><span>{today.toLocaleDateString('en', { month: 'short', day: 'numeric' }).toUpperCase()}</span></div></div></div>{updatesProjectId && projects.find(project => project.id === updatesProjectId) && <ProjectUpdatesPreview onClose={() => setUpdatesProjectId(undefined)} onComment={onCommentProjectUpdate} onCreate={onCreateProjectUpdate} onDelete={onDeleteProjectUpdate} onOpenProject={project => { setUpdatesProjectId(undefined); onOpenProject(project) }} onReact={onReactProjectUpdate} onUpdate={onUpdateProjectUpdate} project={projects.find(project => project.id === updatesProjectId)!} updates={projectUpdates[updatesProjectId] ?? []} viewer={viewer}/>}</>
}

function InitiativeNewView(props: Props & { onCancel: () => void; onSave: (view: InitiativeViewDraft) => Promise<void> }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [visual, setVisual] = useState({ icon: 'CustomView', color: '#8a8f98' })
  const [zoom, setZoom] = useState<TimelineZoom>('Year')
  const [query, setQuery] = useState('')
  const [health, setHealth] = useState<Project['health']>()
  const [properties, setProperties] = useState({ health: true, priority: true, lead: true })
  const [saving, setSaving] = useState(false)
  const save = async () => {
    if (saving) return
    const viewName = name.trim() || 'All projects'
    setSaving(true)
    try {
      await props.onSave({ name: viewName, description: description.trim(), ...visual, zoom, query, health, properties })
      toast.success(`View “${viewName}” created`)
    } finally { setSaving(false) }
  }
  return <div className="li-new-view"><div className="li-new-view__editor"><ViewIconPicker color={visual.color} icon={visual.icon} onChange={setVisual}/><input autoFocus aria-label="All projects" className="li-new-view__name" placeholder="All projects" value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void save(); if (event.key === 'Escape') props.onCancel() }}/><button disabled={saving} onClick={props.onCancel} type="button">Cancel</button><button disabled={saving} onClick={() => void save()} type="button">Save</button><input aria-label="Description (optional)" className="li-new-view__description" placeholder="Description (optional)" value={description} onChange={event => setDescription(event.target.value)}/><div className="li-new-view__controls"><RoadmapFilterMenu health={health} query={query} onHealth={setHealth} onQuery={setQuery}/><span/><button onClick={() => document.querySelector<HTMLElement>('.li-new-view .li-roadmap [aria-label="Center the timeline on today’s date"]')?.click()} type="button">Today</button><ZoomMenu onZoom={setZoom} zoom={zoom}/><RoadmapDisplayMenu properties={properties} onChange={setProperties}/></div></div><InitiativeRoadmap {...props} healthFilter={health} properties={properties} query={query} zoom={zoom} onZoom={setZoom}/></div>
}

function InitiativeSidebar({ initiative, users, teams, labels, projects, projectUpdates, viewer, update, onCreateLabel, onTabChange }: Props & { update: (input: InitiativeMutationInput) => Promise<Initiative> }) {
  const [propertiesOpen, setPropertiesOpen] = useState(true)
  const [progressOpen, setProgressOpen] = useState(true)
  const [progressTab, setProgressTab] = useState<'health'|'status'|'leads'>('health')
  const [activityOpen, setActivityOpen] = useState(true)
  const linked = projects.filter(project => initiative.projectIds.includes(project.id))
  const updateMissing = linked.filter(project => !['completed', 'canceled'].includes(project.status.type) && !(projectUpdates[project.id]?.length)).length
  return <aside className="li-detail-sidebar"><section><button aria-expanded={propertiesOpen} className="li-sidebar-heading" onClick={() => setPropertiesOpen(open => !open)} type="button">Properties <ChevronDown size={11}/></button>{propertiesOpen && <dl><dt>Status</dt><dd><InitiativeProperties compact only="status" initiative={initiative} teams={teams} users={users} onUpdate={update}/></dd><dt>Priority</dt><dd><InitiativeProperties compact only="priority" initiative={initiative} teams={teams} users={users} onUpdate={update}/></dd><dt>Owner</dt><dd><InitiativeProperties compact only="owner" initiative={initiative} teams={teams} users={users} onUpdate={update}/></dd><dt>Lead team</dt><dd><InitiativeProperties compact only="leadTeam" initiative={initiative} teams={teams} users={users} onUpdate={update}/></dd><dt>Contributing teams</dt><dd><InitiativeProperties compact only="contributingTeams" initiative={initiative} teams={teams} users={users} onUpdate={update}/></dd><dt>Target date</dt><dd><InitiativeProperties compact only="target" initiative={initiative} teams={teams} users={users} onUpdate={update}/></dd><dt>Labels</dt><dd><InitiativeLabelsPicker initiative={initiative} labels={labels} onCreateLabel={onCreateLabel} onUpdate={update}/></dd></dl>}</section><section><button aria-expanded={progressOpen} className="li-sidebar-heading" onClick={() => setProgressOpen(open => !open)} type="button">Progress <ChevronDown size={11}/></button>{progressOpen && <div className="li-sidebar-progress"><div role="tablist">{(['health','status','leads'] as const).map(item => <button aria-selected={progressTab === item} key={item} onClick={() => setProgressTab(item)} role="tab" type="button">{titleCase(item)}</button>)}</div><ProgressBreakdown linked={linked} projectUpdates={projectUpdates} tab={progressTab} updateMissing={updateMissing}/></div>}</section><section><button aria-expanded={activityOpen} className="li-sidebar-heading" onClick={() => setActivityOpen(open => !open)} type="button">Activity <ChevronDown size={11}/><span onClick={event => { event.stopPropagation(); onTabChange('activity') }}>See all</span></button>{activityOpen && <div className="li-sidebar-activity"><ViewGlyph color={initiative.color} icon={initiative.icon || 'Initiative'}/><span><strong data-i18n-ignore>{viewer.displayName || viewer.name}</strong> created the initiative · {new Date(initiative.createdAt).toLocaleDateString('en', { month: 'short', day: 'numeric' })}</span></div>}</section></aside>
}

function ProgressBreakdown({ linked, projectUpdates, tab, updateMissing }: { linked: Project[]; projectUpdates: Record<string, ProjectUpdate[]>; tab: 'health'|'status'|'leads'; updateMissing: number }) {
  if (!linked.length) return <span className="li-progress-empty">No projects</span>
  if (tab === 'health') {
    const counts = (['onTrack', 'atRisk', 'offTrack'] as Project['health'][]).map(value => ({ key: value, label: healthLabel(value), count: linked.filter(project => projectUpdates[project.id]?.length && project.health === value).length })).filter(item => item.count)
    return <><div className="li-progress-summary"><strong>{updateMissing}</strong><span>{updateMissing === 1 ? 'project needs an update' : 'projects need updates'}</span></div><ProgressRows rows={counts}/></>
  }
  if (tab === 'status') {
    const counts = [...new Set(linked.map(project => project.status.name))].map(label => ({ key: label, label, count: linked.filter(project => project.status.name === label).length }))
    return <ProgressRows entity rows={counts}/>
  }
  const counts = [...new Set(linked.map(project => project.lead?.id ?? ''))].map(id => ({ key: id || 'unassigned', label: linked.find(project => (project.lead?.id ?? '') === id)?.lead?.displayName || linked.find(project => (project.lead?.id ?? '') === id)?.lead?.name || 'No lead', count: linked.filter(project => (project.lead?.id ?? '') === id).length }))
  return <ProgressRows entity rows={counts}/>
}

function ProgressRows({ rows, entity = false }: { rows: { key: string; label: string; count: number }[]; entity?: boolean }) { return <div className="li-progress-rows">{rows.map(row => <div key={row.key}><span data-i18n-ignore={entity ? '' : undefined}>{row.label}</span><i style={{ width: `${Math.max(8, row.count / Math.max(...rows.map(item => item.count)) * 100)}%` }}/><small>{row.count}</small></div>)}</div> }

function InitiativeHealthMenu({ health, onHealth }: { health: Project['health']; onHealth: (health: Project['health']) => void }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className={`li-health is-${health}`} type="button"><i/>{healthLabel(health)}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="li-menu">{(['onTrack', 'atRisk', 'offTrack'] as Project['health'][]).map(item => <DropdownMenu.Item key={item} onSelect={() => onHealth(item)}><span className={`li-update-health is-${item}`}/>{healthLabel(item)}{item === health && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }

function InlineUpdateEditor({ update, onCancel, onSave }: { update: InitiativeUpdate; onCancel: () => void; onSave: (input: { body?: string; health?: Project['health'] }) => Promise<void> }) { const [body, setBody] = useState(update.body); const [health, setHealth] = useState(update.health); const [saving, setSaving] = useState(false); return <div className="li-inline-update-editor"><InitiativeHealthMenu health={health} onHealth={setHealth}/><textarea autoFocus value={body} onChange={event => setBody(event.target.value)}/><footer><button onClick={onCancel} type="button">Cancel</button><button disabled={!body.trim() || saving} onClick={() => { setSaving(true); void onSave({ body: body.trim(), health }).finally(() => setSaving(false)) }} type="button">Save</button></footer></div> }
function InlineCommentEditor({ comment, onCancel, onSave }: { comment: Initiative['comments'][number]; onCancel: () => void; onSave: (body: string) => Promise<void> }) { const [body, setBody] = useState(comment.body); const [saving, setSaving] = useState(false); return <div className="li-inline-update-editor"><textarea autoFocus value={body} onChange={event => setBody(event.target.value)}/><footer><button onClick={onCancel} type="button">Cancel</button><button disabled={!body.trim() || saving} onClick={() => { setSaving(true); void onSave(body.trim()).finally(() => setSaving(false)) }} type="button">Save</button></footer></div> }

function ActivityDisplayMenu({ value, onChange }: { value: { updates: boolean; comments: boolean; activity: boolean }; onChange: (value: { updates: boolean; comments: boolean; activity: boolean }) => void }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Display options" type="button"><SlidersHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu"><DropdownMenu.Label>Show in activity</DropdownMenu.Label>{(['updates', 'comments', 'activity'] as const).map(property => <DropdownMenu.CheckboxItem checked={value[property]} key={property} onCheckedChange={() => onChange({ ...value, [property]: !value[property] })}>{value[property] && <Check size={12}/>} {titleCase(property)}</DropdownMenu.CheckboxItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function OverviewProjectDisplayMenu({ properties, onChange }: { properties: Set<string>; onChange: (properties: Set<string>) => void }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Display options" type="button"><SlidersHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu"><DropdownMenu.Label>Display properties</DropdownMenu.Label>{(['health', 'priority', 'lead', 'target', 'status'] as const).map(property => <DropdownMenu.CheckboxItem checked={properties.has(property)} key={property} onCheckedChange={() => { const next = new Set(properties); if (next.has(property)) next.delete(property); else next.add(property); onChange(next) }}>{properties.has(property) && <Check size={12}/>} {property === 'target' ? 'Target date' : titleCase(property)}</DropdownMenu.CheckboxItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function RoadmapFilterMenu({ query, health, onQuery, onHealth }: { query: string; health?: Project['health']; onQuery: (value: string) => void; onHealth: (value?: Project['health']) => void }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Add filter" type="button"><Filter size={14}/>{(query || health) && <i/>}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu li-roadmap-filter"><div className="li-menu-search"><input autoFocus aria-label="Filter projects" placeholder="Filter projects…" value={query} onChange={event => onQuery(event.target.value)}/></div><DropdownMenu.Label>Health</DropdownMenu.Label>{(['onTrack', 'atRisk', 'offTrack', 'noUpdate'] as Project['health'][]).map(value => <DropdownMenu.Item key={value} onSelect={() => onHealth(value)}><span className={`li-update-health is-${value}`}/>{healthLabel(value)}{health === value && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}{(query || health) && <><DropdownMenu.Separator/><DropdownMenu.Item onSelect={() => { onQuery(''); onHealth(undefined) }}>Clear filters</DropdownMenu.Item></>}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function RoadmapDisplayMenu({ properties, onChange }: { properties: { health: boolean; priority: boolean; lead: boolean }; onChange: (value: { health: boolean; priority: boolean; lead: boolean }) => void }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Display options" type="button"><SlidersHorizontal size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu"><DropdownMenu.Label>Display properties</DropdownMenu.Label>{(['health', 'priority', 'lead'] as const).map(property => <DropdownMenu.CheckboxItem checked={properties[property]} key={property} onCheckedChange={() => onChange({ ...properties, [property]: !properties[property] })}>{properties[property] && <Check size={12}/>} {titleCase(property)}</DropdownMenu.CheckboxItem>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }
function ZoomMenu({ zoom, onZoom }: { zoom: TimelineZoom; onZoom: (zoom: TimelineZoom) => void }) { return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Zoom" type="button">{zoom}<ChevronDown size={11}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="li-menu li-zoom-menu"><div className="li-menu-search"><input aria-label="Zoom" placeholder="Zoom"/></div>{(['Year', 'Quarter', 'Month', 'Week'] as TimelineZoom[]).map((item, index) => <DropdownMenu.Item key={item} onSelect={() => onZoom(item)}>{item}<kbd>{['Y', 'Q', 'M', 'W'][index]}</kbd>{zoom === item && <Check className="li-menu-end" size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root> }

function DeleteInitiativeDialog({ initiative, open, onOpenChange, onDelete }: { initiative: Initiative; open: boolean; onOpenChange: (open: boolean) => void; onDelete: () => Promise<void> }) { const [deleting, setDeleting] = useState(false); return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content aria-describedby="initiative-delete-description" className="li-delete-dialog"><Dialog.Title>Delete <span data-i18n-ignore>{initiative.name}</span>?</Dialog.Title><Dialog.Description id="initiative-delete-description">Projects will not be deleted or archived. Deleted initiatives can be restored from Recently deleted for 30 days.</Dialog.Description><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button autoFocus disabled={deleting} onClick={() => { setDeleting(true); void onDelete().finally(() => setDeleting(false)) }} type="button">Delete</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root> }

function StoredViewEditDialog({ view, open, onOpenChange, onSave }: { view?: InitiativeStoredView; open: boolean; onOpenChange: (open: boolean) => void; onSave: (view: InitiativeStoredView) => Promise<void> }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [visual, setVisual] = useState({ icon: 'CustomView', color: '#8a8f98' })
  const [saving, setSaving] = useState(false)
  useEffect(() => { if (view) { setName(view.name); setDescription(view.description); setVisual({ icon: view.icon, color: view.color }); setSaving(false) } }, [view])
  if (!view) return null
  const save = async () => { if (saving) return; const nextName = name.trim() || view.name; setSaving(true); try { await onSave({ ...view, ...visual, name: nextName, description: description.trim(), slugId: `${slugifyView(nextName)}-${view.id.replaceAll('-', '').slice(-12)}` }) } finally { setSaving(false) } }
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content aria-describedby="initiative-view-edit-description" className="li-link-dialog li-stored-view-dialog"><Dialog.Title>Edit view</Dialog.Title><Dialog.Description className="sr-only" id="initiative-view-edit-description">Change the view name, description, icon, or color.</Dialog.Description><div><ViewIconPicker color={visual.color} icon={visual.icon} onChange={setVisual}/><input aria-label="View name" autoFocus value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') void save() }}/></div><label><span>Description</span><input aria-label="View description" value={description} onChange={event => setDescription(event.target.value)}/></label><footer><Dialog.Close asChild><button disabled={saving} type="button">Cancel</button></Dialog.Close><button disabled={!name.trim() || saving} onClick={() => void save()} type="button">Save</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function StoredViewDeleteDialog({ view, open, onOpenChange, onDelete }: { view?: InitiativeStoredView; open: boolean; onOpenChange: (open: boolean) => void; onDelete: () => Promise<void> }) {
  const [deleting, setDeleting] = useState(false)
  if (!view) return null
  return <Dialog.Root onOpenChange={next => { onOpenChange(next); if (!next) setDeleting(false) }} open={open}><Dialog.Portal><Dialog.Overlay className="li-dialog-overlay"/><Dialog.Content aria-describedby="initiative-view-delete-description" className="li-delete-dialog"><Dialog.Title>Delete the view “<span data-i18n-ignore>{view.name}</span>”?</Dialog.Title><Dialog.Description className="sr-only" id="initiative-view-delete-description">This saved initiative view will be permanently deleted.</Dialog.Description><footer><Dialog.Close asChild><button disabled={deleting} type="button">Cancel</button></Dialog.Close><button autoFocus disabled={deleting} onClick={() => { setDeleting(true); void onDelete().finally(() => setDeleting(false)) }} type="button">Delete view</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function InitiativeEditableText({ value, placeholder, label, className, onCommit }: { value: string; placeholder?: string; label: string; className: string; onCommit: (value: string) => void }) { const [draft, setDraft] = useState(value); useEffect(() => setDraft(value), [value]); return <input aria-label={label} className={className} placeholder={placeholder} value={draft} onChange={event => setDraft(event.target.value)} onBlur={() => { if (draft.trim() !== value && draft.trim()) onCommit(draft.trim()) }} onKeyDown={event => { if (event.key === 'Enter') event.currentTarget.blur(); if (event.key === 'Escape') { setDraft(value); event.currentTarget.blur() } }}/>}
function EditableArea({ value, placeholder, label, onCommit }: { value: string; placeholder?: string; label: string; onCommit: (value: string) => void }) { const [draft, setDraft] = useState(value); useEffect(() => setDraft(value), [value]); return <textarea aria-label={label} placeholder={placeholder} value={draft} onChange={event => setDraft(event.target.value)} onBlur={() => { if (draft !== value) onCommit(draft) }}/>} 
function healthLabel(value: Project['health']) { return ({ onTrack: 'On track', atRisk: 'At risk', offTrack: 'Off track', noUpdate: 'No updates' } as const)[value] }
function roadmapPosition(project: Project, origin: Date, monthWidth: number) { const start = project.startDate ? new Date(`${project.startDate}T00:00:00`) : new Date(); const target = project.targetDate ? new Date(`${project.targetDate}T00:00:00`) : new Date(start.getFullYear(), start.getMonth() + 2, start.getDate()); const days = (date: Date) => (+date - +origin) / 86400000; return { left: `${315 + Math.max(0, days(start) / 30.44 * monthWidth)}px`, width: `${Math.max(90, (days(target) - days(start)) / 30.44 * monthWidth)}px` } }
function isoRoadmapDate(date: Date) { return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}` }
function projectDraftMutation(draft: NewProjectDraft, projects: Project[], statuses: ProjectStatus[]): ProjectCreateInput { return { templateId: draft.templateId, color: draft.color, description: draft.description, icon: draft.icon, leadId: draft.leadId, memberIds: draft.memberIds, labelIds: draft.labelIds, dependencyIds: draft.dependencyIds, name: draft.name, priority: Math.max(0, ['No priority', 'Urgent', 'High', 'Medium', 'Low'].indexOf(draft.priority)), startDate: draft.startDate, statusId: statuses.find(status => status.name === draft.status)?.id ?? projects.find(project => project.status.name === draft.status)?.status.id, summary: draft.summary, targetDate: draft.targetDate, teamIds: draft.teamIds } }
function useStoredBoolean(key: string, fallback: boolean) { const [value, setValue] = useState(() => localStorage.getItem(key) === null ? fallback : localStorage.getItem(key) === 'true'); const update = (next: boolean | ((current: boolean) => boolean)) => setValue(current => { const resolved = typeof next === 'function' ? next(current) : next; localStorage.setItem(key, String(resolved)); return resolved }); return [value, update] as const }
function slugifyView(value: string) { return value.normalize('NFKC').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-+|-+$/g, '').slice(0, 64) || 'view' }
function savedViewMatchesInitiative(view: SavedView, initiativeId: string) { return view.resource === 'initiativeProjects' && view.filters.some(item => { if (!item || typeof item !== 'object') return false; const filter = item as { field?: unknown; values?: unknown }; return filter.field === 'initiative' && Array.isArray(filter.values) && filter.values.includes(initiativeId) }) }
function initiativeViewFromSavedView(view: SavedView): InitiativeStoredView {
  const display = view.display ?? {}
  const rawProperties = display.properties && typeof display.properties === 'object' ? display.properties as Record<string, unknown> : {}
  const zoom = ['Year', 'Quarter', 'Month', 'Week'].includes(String(display.zoom)) ? display.zoom as TimelineZoom : 'Year'
  const health = ['onTrack', 'atRisk', 'offTrack', 'noUpdate'].includes(String(display.health)) ? display.health as Project['health'] : undefined
  return { ...view, icon: view.icon || 'CustomView', color: view.color || '#8a8f98', slugId: typeof display.slugId === 'string' && display.slugId ? display.slugId : `${slugifyView(view.name)}-${view.id.replaceAll('-', '').slice(-12)}`, zoom, query: typeof display.query === 'string' ? display.query : '', health, properties: { health: rawProperties.health !== false, priority: rawProperties.priority !== false, lead: rawProperties.lead !== false } }
}
function initiativeViewMutation(initiativeId: string, view: InitiativeViewDraft): SavedViewMutationInput {
  const slugId = view.slugId || `${slugifyView(view.name)}-${crypto.randomUUID().replaceAll('-', '').slice(0, 12)}`
  return { name: view.name, description: view.description, icon: view.icon, color: view.color, favorite: view.favorite, resource: 'initiativeProjects', scope: 'workspace', view: 'all', filters: [{ field: 'initiative', operator: 'is', values: [initiativeId] }], display: { slugId, zoom: view.zoom, query: view.query, health: view.health ?? '', properties: view.properties } }
}
