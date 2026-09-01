import { Component, useId, useEffect, useMemo, useState, type DragEvent, type ErrorInfo, type KeyboardEvent, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Line, type LineCustomSvgLayerProps, type SliceTooltipProps } from '@nivo/line'
import { Blocks, ChevronDown, ChevronRight, Flag, MoreHorizontal, OctagonMinus, Plus, X } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { CalendarIcon, LabelIcon, MembersIcon, NoAssigneeIcon, PriorityIcon, ProjectIcon, ProjectStatusIcon, SlackIcon, TeamIcon } from '@/components/issue/issue-icons'
import { projectStatusOptionColor } from '@/lib/project-status-color'
import { PropertyMenu } from '@/components/property/property-menu'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import type { Initiative, IntegrationConnection, Issue, IssueLabel, LabelGroup, Project, ProjectMilestone, ProjectStatus, ProjectUpdate, Team, User } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { ProjectDetailTab, ProjectDetailProps } from './project-detail-types'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { PRIORITY_LABELS } from './project-detail-types'
import { toggleGroupedLabelIds } from '@/lib/labels'
import { useI18n } from '@/i18n/i18n'
import { formatProjectPropertyDate, initiativeStatusLabel, inviteProjectMember } from './project-detail-helpers'
import { buildProgressData, type PersistedProgressHistory, type ProgressSeries } from './project-progress-data'

export function ProjectDetailsSidebar({ initiatives, integrationConnections, labelGroups, labels, onConvertMilestone, onCreateMilestone, onDeleteMilestone, onMoveMilestone, onOpenIssueFilter, onOpenMilestoneIssues, onReorderMilestones, onTabChange, onUpdate, onUpdateProject, onUpdateMilestone, project, projectIssues, projects, projectStatuses, projectUpdates, teams, users, viewer }: {
  initiatives: Initiative[]
  integrationConnections: IntegrationConnection[]
  labelGroups: LabelGroup[]
  labels: IssueLabel[]
  onConvertMilestone: ProjectDetailProps['onConvertMilestone']
  onCreateMilestone: ProjectDetailProps['onCreateMilestone']
  onDeleteMilestone: ProjectDetailProps['onDeleteMilestone']
  onMoveMilestone: ProjectDetailProps['onMoveMilestone']
  onOpenIssueFilter: (field: 'assignee'|'labels', value: string, valueLabel: string) => void
  onOpenMilestoneIssues: (milestoneId?: string) => void
  onReorderMilestones: ProjectDetailProps['onReorderMilestones']
  onTabChange: (tab: ProjectDetailTab) => void
  onUpdate: (input: ProjectMutationInput) => Promise<void>
  onUpdateProject: ProjectDetailProps['onUpdate']
  onUpdateMilestone: ProjectDetailProps['onUpdateMilestone']
  project: Project
  projectIssues: Issue[]
  projects: Project[]
  projectStatuses: ProjectStatus[]
  projectUpdates: ProjectUpdate[]
  teams: Team[]
  users: User[]
  viewer: User
}) {
  const [propertiesOpen, setPropertiesOpen] = useState(true)
  const [milestonesOpen, setMilestonesOpen] = useState(true)
  const [progressOpen, setProgressOpen] = useState(true)
  const [activityOpen, setActivityOpen] = useState(true)
  const [milestoneEditor, setMilestoneEditor] = useState<ProjectMilestone | 'new'>()
  const [draggingMilestoneId, setDraggingMilestoneId] = useState<string>()
  const [milestoneDrop, setMilestoneDrop] = useState<{ id: string; edge: 'before'|'after' }>()
  const [progressTab, setProgressTab] = useState<'assignees'|'labels'>('assignees')
  const blockedByProjects = projects.filter(item => (project.dependencyIds ?? []).includes(item.id))
  const blockingProjects = projects.filter(item => (item.dependencyIds ?? []).includes(project.id))
  const started = projectIssues.filter(issue => issue.state.type === 'started').length
  const completed = projectIssues.filter(issue => issue.state.type === 'completed').length
  const issueLabels = labels.filter(label => projectIssues.some(issue => issue.labels.some(item => item.id === label.id))).sort((left, right) => left.name.localeCompare(right.name))
  const assignees = users.filter(user => projectIssues.some(issue => issue.assignee?.id === user.id)).sort((left, right) => left.displayName.localeCompare(right.displayName))
  const unassignedMilestoneStats = milestoneStats(projectIssues)
  const events = useMemo(() => projectEvents(project, projectUpdates, viewer), [project, projectUpdates, viewer])
  const reorderMilestone = (sourceId: string, targetId: string, edge: 'before'|'after') => {
    const ids = (project.milestones ?? []).map(item => item.id)
    const withoutSource = ids.filter(id => id !== sourceId)
    const targetIndex = withoutSource.indexOf(targetId)
    if (targetIndex < 0) return
    withoutSource.splice(targetIndex + (edge === 'after' ? 1 : 0), 0, sourceId)
    if (withoutSource.every((id, index) => id === ids[index])) return
    void onReorderMilestones(project.id, withoutSource)
  }

  return <aside aria-label="Project sidebar" className="project-details-sidebar">
    <SidebarSection compact onToggle={() => setPropertiesOpen(value => !value)} open={propertiesOpen} title="Properties" action={<ProjectDependencyMenu onUpdate={onUpdate} onUpdateProject={onUpdateProject} project={project} projects={projects} viewer={viewer}/> }>
      <SidebarPropertiesBoundary><SidebarProperties initiatives={initiatives} integrationConnections={integrationConnections} labelGroups={labelGroups} labels={labels} onUpdate={onUpdate} project={project} projects={projects} projectStatuses={projectStatuses} teams={teams} users={users}/></SidebarPropertiesBoundary>
      <ProjectDependencyRows blockedBy={blockedByProjects} blocking={blockingProjects} onUpdate={onUpdate} onUpdateProject={onUpdateProject} project={project}/>
    </SidebarSection>

    <SidebarSection onToggle={() => setMilestonesOpen(value => !value)} open={milestonesOpen} title="Milestones" action={<button aria-label="Add milestone" data-project-milestone-add onClick={() => { setMilestonesOpen(true); setMilestoneEditor('new') }} type="button"><svg aria-hidden="true" height="16" viewBox="0 0 16 16" width="16"><use href="#plus"/></svg></button>}>
      <div className="project-details-sidebar__milestones">
        {(project.milestones ?? []).map((milestone) => {
          const stats = milestoneStats(projectIssues, milestone.id)
          if (milestoneEditor !== 'new' && milestoneEditor?.id === milestone.id) return <MilestoneEditor key={milestone.id} milestone={milestone} onCancel={() => setMilestoneEditor(undefined)} onSubmit={async input => { await onUpdateMilestone(project.id, milestone.id, input); setMilestoneEditor(undefined) }} progress={stats.progress}/>
          return <MilestoneRow disabled={Boolean(milestoneEditor)} dragging={draggingMilestoneId === milestone.id} dropEdge={milestoneDrop?.id === milestone.id ? milestoneDrop.edge : undefined} key={milestone.id} milestone={milestone} onConvert={async () => { await onConvertMilestone(project.id, milestone.id); toast.success('Milestone converted to project') }} onDelete={() => onDeleteMilestone(project.id, milestone.id)} onDragEnd={() => { setDraggingMilestoneId(undefined); setMilestoneDrop(undefined) }} onDragOver={(event) => { if (!draggingMilestoneId || draggingMilestoneId === milestone.id) return; event.preventDefault(); event.dataTransfer.dropEffect = 'move'; const rect = event.currentTarget.getBoundingClientRect(); setMilestoneDrop({ id: milestone.id, edge: event.clientY < rect.top + rect.height / 2 ? 'before' : 'after' }) }} onDragStart={(event) => { setDraggingMilestoneId(milestone.id); event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', milestone.id) }} onDrop={(event) => { event.preventDefault(); const sourceId = draggingMilestoneId ?? event.dataTransfer.getData('text/plain'); if (sourceId && milestoneDrop) reorderMilestone(sourceId, milestone.id, milestoneDrop.edge); setDraggingMilestoneId(undefined); setMilestoneDrop(undefined) }} onEdit={() => setMilestoneEditor(milestone)} onMove={async targetProjectId => { await onMoveMilestone(project.id, milestone.id, targetProjectId); toast.success('Milestone moved') }} onOpenIssues={() => onOpenMilestoneIssues(milestone.id)} onUpdateDate={targetDate => onUpdateMilestone(project.id, milestone.id, { targetDate })} projects={projects.filter(item => item.id !== project.id)} stats={stats}/>
        })}
        {milestoneEditor === 'new' && <MilestoneEditor onCancel={() => setMilestoneEditor(undefined)} onSubmit={async input => { await onCreateMilestone(project.id, input as { name: string; description?: string; targetDate?: string }); setMilestoneEditor(undefined) }} progress={0}/>}
        {(project.milestones?.length ?? 0) > 0 && <div aria-label={`No milestone ${unassignedMilestoneStats.count} issues`} className="project-details-sidebar__milestone is-unassigned" role="button" tabIndex={0} onClick={() => onOpenMilestoneIssues()} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpenMilestoneIssues() } }}><MilestoneProgressIcon unassigned/><strong>No milestone</strong><span>{unassignedMilestoneStats.count}</span></div>}
        {!project.milestones?.length && !milestoneEditor && <div className="project-details-sidebar__milestone-empty"><p>Add milestones to organize work within your project and break it into more granular stages.</p><a href="https://flow.app/docs/project-milestones" rel="noreferrer" target="_blank">Learn more</a></div>}
      </div>
    </SidebarSection>

    <SidebarSection onToggle={() => setProgressOpen(value => !value)} open={progressOpen} title="Progress">
      <div className="project-details-sidebar__progress">
        <div className="project-details-sidebar__stats"><span><i className="is-scope"/>Scope<strong>{projectIssues.length}</strong></span><span><i className="is-started"/>Started<strong>{started}</strong></span><span><i className="is-completed"/>Completed<strong>{completed}</strong></span></div>
        <ProgressChart issues={projectIssues} progress={project.progress} start={project.startDate} target={project.targetDate} persistedHistory={project}/>
        <div aria-label="Progress grouping" className="project-details-sidebar__segments" role="tablist"><button aria-selected={progressTab === 'assignees'} onClick={() => setProgressTab('assignees')} role="tab" type="button">Assignees</button><button aria-selected={progressTab === 'labels'} onClick={() => setProgressTab('labels')} role="tab" type="button">Labels</button></div>
        <div className="project-details-sidebar__breakdown">{progressTab === 'assignees' ? assignees.map(user => {
          const userIssues = projectIssues.filter(issue => issue.assignee?.id === user.id)
          return <ProgressBreakdownRow icon={<Avatar name={user.displayName}/>} issues={userIssues} key={user.id} label={user.displayName} onOpen={() => onOpenIssueFilter('assignee', user.id, user.displayName)}/>
        }) : issueLabels.map(label => {
          const labelIssues = projectIssues.filter(issue => issue.labels.some(item => item.id === label.id))
          return <ProgressBreakdownRow icon={<i className="project-details-sidebar__label-dot" style={{ background: label.color }}/>} issues={labelIssues} key={label.id} label={label.name} onOpen={() => onOpenIssueFilter('labels', label.id, label.name)}/>
        })}{progressTab === 'assignees' && !assignees.length && <p>No assigned issues</p>}{progressTab === 'labels' && !issueLabels.length && <p>No labels in scope</p>}</div>
      </div>
    </SidebarSection>

    <SidebarSection onToggle={() => setActivityOpen(value => !value)} open={activityOpen} title="Activity" action={<button className="project-details-sidebar__see-all" onClick={() => onTabChange('activity')} type="button">See all</button>}>
    <div className="project-details-sidebar__activity">{events.slice(0, 6).map(event => <div key={event.id}><Avatar name={event.actor}/><p><strong data-i18n-ignore>{event.actor}</strong> {event.text} <time>· {event.date}</time></p></div>)}</div>
    </SidebarSection>
  </aside>
}

function ProjectDependencyRows({ blockedBy, blocking, onUpdate, onUpdateProject, project }: {
  blockedBy: Project[]
  blocking: Project[]
  onUpdate: (input: ProjectMutationInput) => Promise<void>
  onUpdateProject: ProjectDetailProps['onUpdate']
  project: Project
}) {
  if (!blockedBy.length && !blocking.length) return null
  const workspaceKey = window.location.pathname.split('/').filter(Boolean)[0] ?? ''
  const remove = async (item: Project, direction: ProjectDependencyDirection) => {
    if (direction === 'blockedBy') await onUpdate({ dependencyIds: (project.dependencyIds ?? []).filter(id => id !== item.id) })
    else await onUpdateProject(item.id, { dependencyIds: (item.dependencyIds ?? []).filter(id => id !== project.id) })
  }
  const changeDirection = async (item: Project, direction: ProjectDependencyDirection) => {
    try {
      if (direction === 'blockedBy') {
        await onUpdateProject(item.id, { dependencyIds: [...new Set([...(item.dependencyIds ?? []), project.id])] })
        await onUpdate({ dependencyIds: (project.dependencyIds ?? []).filter(id => id !== item.id) })
      } else {
        await onUpdate({ dependencyIds: [...new Set([...(project.dependencyIds ?? []), item.id])] })
        await onUpdateProject(item.id, { dependencyIds: (item.dependencyIds ?? []).filter(id => id !== project.id) })
      }
    } catch (error) {
      toast.error('Could not update project dependency', { description: error instanceof Error ? error.message : undefined })
    }
  }
  const group = (label: 'Blocked by'|'Blocking', items: Project[], direction: ProjectDependencyDirection) => items.length ? <section className="project-dependency-group">
    <h4>{label}</h4>
    {items.map(item => <div className="project-dependency-row" key={`${direction}-${item.id}`}>
      <a aria-label={item.name} href={`/${workspaceKey}/project/${item.slugId}/overview`}><ProjectIcon size={16} style={{ color: item.color }}/><span data-i18n-ignore>{item.name}</span></a>
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Menu" className="project-dependency-row__menu-trigger" type="button"><MoreHorizontal size={12}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu project-dependency-row__menu" collisionPadding={10} onCloseAutoFocus={event => event.preventDefault()} sideOffset={-2}>
        <DropdownMenu.Item onSelect={() => void changeDirection(item, direction)}><ProjectIcon size={16}/><span>{direction === 'blockedBy' ? 'Change to blocking' : 'Change to blocked by'}</span></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => void remove(item, direction)}><X size={16}/><span>Remove dependency</span></DropdownMenu.Item>
      </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    </div>)}
  </section> : null
  return <div className="project-details-sidebar__dependencies">{group('Blocked by', blockedBy, 'blockedBy')}{group('Blocking', blocking, 'blocking')}</div>
}

function MilestoneRow({ disabled, dragging, dropEdge, milestone, onConvert, onDelete, onDragEnd, onDragOver, onDragStart, onDrop, onEdit, onMove, onOpenIssues, onUpdateDate, projects, stats }: {
  disabled: boolean
  dragging: boolean
  dropEdge?: 'before'|'after'
  milestone: ProjectMilestone
  onConvert: () => Promise<void>
  onDelete: () => Promise<void>
  onDragEnd: () => void
  onDragOver: (event: DragEvent<HTMLDivElement>) => void
  onDragStart: (event: DragEvent<HTMLDivElement>) => void
  onDrop: (event: DragEvent<HTMLDivElement>) => void
  onEdit: () => void
  onMove: (targetProjectId: string) => Promise<void>
  onOpenIssues: () => void
  onUpdateDate: (targetDate: string) => Promise<ProjectMilestone>
  projects: Project[]
  stats: { count: number; progress: number }
}) {
  const [dateDialogOpen, setDateDialogOpen] = useState(false)
  const link = `${location.origin}${location.pathname.replace(/\/(overview|activity|issues)$/, '/issues')}`
  const copy = (value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(message))
  return <div aria-disabled={disabled} aria-label={`${milestone.name} ${stats.progress}% of ${stats.count}`} className="project-details-sidebar__milestone" data-disabled={disabled || undefined} data-dragging={dragging || undefined} data-drop-edge={dropEdge} draggable={!disabled} onClick={event => { if (!disabled && !(event.target as HTMLElement).closest('button,[role=menuitem]')) onOpenIssues() }} onDragEnd={onDragEnd} onDragOver={onDragOver} onDragStart={onDragStart} onDrop={onDrop} role="button" tabIndex={disabled ? -1 : 0} onKeyDown={event => { if (!disabled && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); onOpenIssues() } }}>
    <div className="project-details-sidebar__milestone-summary"><MilestoneProgressIcon progress={stats.progress}/><strong data-i18n-ignore>{milestone.name}</strong><span className="project-details-sidebar__milestone-stats"><span>{stats.progress}% of</span><button aria-label={`View ${stats.count} issues in ${milestone.name}`} onClick={event => { event.stopPropagation(); onOpenIssues() }} tabIndex={-1} type="button">{stats.count}</button></span><button className="project-details-sidebar__milestone-see-issues" onClick={event => { event.stopPropagation(); onOpenIssues() }} tabIndex={-1} type="button">See issues</button></div>
    <ProjectDatePicker buttonClassName="project-details-sidebar__milestone-date" contentClassName="project-details-sidebar__date-menu" label="Target date" onChange={targetDate => void onUpdateDate(targetDate)} side="left" value={milestone.targetDate}><span>{milestone.targetDate ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : 'No date'}</span></ProjectDatePicker>
    <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${milestone.name} actions`} className="project-details-sidebar__milestone-actions" onClick={event => event.stopPropagation()} type="button"><MilestoneMenuIcon name="more-horizontal"/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" alignOffset={-25} className="project-milestone-menu" collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()} sideOffset={4}>
      <MilestoneMenuItem icon="edit" label="Edit…" onSelect={onEdit}/>
      <MilestoneMenuItem end={milestone.targetDate ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : 'No date'} icon="calendar" label="Edit target date…" onSelect={() => setDateDialogOpen(true)}/>
      <DropdownMenu.Sub><MilestoneSubTrigger icon="copy" label="Copy"/><DropdownMenu.Portal><DropdownMenu.SubContent alignOffset={-7} className="project-milestone-menu project-milestone-copy-menu" collisionPadding={8} sideOffset={-2}>
        <MilestoneMenuItem icon="link" label="Copy link" onSelect={() => copy(link, 'Milestone link copied')}/>
        <MilestoneMenuItem end="⌘ C" icon="link-name" label="Copy name as link" onSelect={() => copy(`[${milestone.name}](${link})`, 'Milestone name and link copied')}/>
        <MilestoneMenuItem icon="issues" label="Copy link to issues" onSelect={() => copy(link, 'Issues link copied')}/>
      </DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Separator/>
      <DropdownMenu.Sub><MilestoneSubTrigger icon="milestone" label="Move milestone to"/><DropdownMenu.Portal><DropdownMenu.SubContent alignOffset={-7} className="project-milestone-menu project-milestone-move-menu" collisionPadding={8} sideOffset={-2}>
        {projects.map(project => <MilestoneMenuItem icon="project" key={project.id} label={project.name} onSelect={() => void onMove(project.id)}/>)}
        {!projects.length && <DropdownMenu.Label>No other projects</DropdownMenu.Label>}
      </DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <MilestoneMenuItem icon="project" label="Convert to project" onSelect={() => void onConvert()}/>
      <DropdownMenu.Separator/>
      <MilestoneMenuItem className="is-danger" end="⌘ ⌫" icon="trash" label="Delete…" onSelect={() => { void confirmAction(`Delete “${milestone.name}”?`,{confirmLabel:'Delete milestone'}).then(confirmed=>{if(confirmed)return onDelete()}) }}/>
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    <MilestoneDateDialog milestone={milestone} onOpenChange={setDateDialogOpen} onSubmit={onUpdateDate} open={dateDialogOpen}/>
  </div>
}

type MilestoneMenuIconName = 'calendar'|'chevron-right'|'close'|'copy'|'edit'|'issues'|'link'|'link-name'|'milestone'|'more-horizontal'|'project'|'trash'

function MilestoneMenuIcon({ name }: { name: MilestoneMenuIconName }) {
  return <svg aria-hidden="true" height="16" viewBox="0 0 16 16" width="16"><use href={`#${name}`}/></svg>
}

function MilestoneMenuItem({ className = '', end, icon, label, onSelect }: { className?: string; end?: string; icon: MilestoneMenuIconName; label: string; onSelect: () => void }) {
  return <DropdownMenu.Item className={className} onSelect={onSelect}><span className="project-milestone-menu__item-background"/><MilestoneMenuIcon name={icon}/><span>{label}</span>{end && <kbd>{end}</kbd>}</DropdownMenu.Item>
}

function MilestoneSubTrigger({ icon, label }: { icon: MilestoneMenuIconName; label: string }) {
  return <DropdownMenu.SubTrigger><span className="project-milestone-menu__item-background"/><MilestoneMenuIcon name={icon}/><span>{label}</span><MilestoneMenuIcon name="chevron-right"/></DropdownMenu.SubTrigger>
}

function MilestoneProgressIcon({ progress = 0, unassigned = false }: { progress?: number; unassigned?: boolean }) {
  const clamped = Math.max(0, Math.min(100, progress))
  const pathLength = 31
  const completedLength = pathLength * clamped / 100
  if (unassigned || clamped === 0) return <svg aria-hidden="true" className="project-details-sidebar__milestone-icon is-unassigned" height="16" viewBox="0 0 16 16" width="16"><use href="#milestone-shape"/></svg>
  if (clamped === 100) return <svg aria-hidden="true" className="project-details-sidebar__milestone-icon is-complete" height="16" viewBox="0 0 16 16" width="16"><use href="#milestone-shape"/></svg>
  return <svg aria-hidden="true" className="project-details-sidebar__milestone-icon is-progress" height="16" viewBox="0 0 16 16" width="16"><use className="is-track" href="#milestone-shape"/><use className="is-value" href="#milestone-shape" strokeDasharray={`${completedLength} ${pathLength - completedLength}`}/></svg>
}

function SidebarSection({ action, children, compact, onToggle, open, title }: { action?: ReactNode; children: ReactNode; compact?: boolean; onToggle: () => void; open: boolean; title: string }) {
  return <section className={`project-details-sidebar__section ${compact ? 'is-compact' : ''}`}><header><button aria-expanded={open} aria-label={`${open ? 'Collapse' : 'Expand'} ${title.toLowerCase()} section`} onClick={onToggle} type="button"><span>{title}</span>{open ? <ChevronDown size={11}/> : <ChevronRight size={11}/>}</button>{action}</header>{open && children}</section>
}

type ProjectDependencyDirection = 'blockedBy' | 'blocking'

function ProjectDependencyMenu({ onUpdate, onUpdateProject, project, projects, viewer }: {
  onUpdate: (input: ProjectMutationInput) => Promise<void>
  onUpdateProject: ProjectDetailProps['onUpdate']
  project: Project
  projects: Project[]
  viewer: User
}) {
  const [open, setOpen] = useState(false)
  const [direction, setDirection] = useState<ProjectDependencyDirection>()
  const [query, setQuery] = useState('')
  const directions = [
    { id: 'blockedBy' as const, label: 'Blocked by…', icon: <OctagonMinus size={16}/> },
    { id: 'blocking' as const, label: 'Blocking…', icon: <Blocks size={16}/> },
  ].filter(item => item.label.toLowerCase().includes(query.trim().toLowerCase()))

  const reset = () => { setDirection(undefined); setQuery('') }
  return <DropdownMenu.Root open={open} onOpenChange={next => { setOpen(next); if (!next) reset() }}>
    <DropdownMenu.Trigger asChild><button aria-label="Add dependency" type="button"><Plus size={13}/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu project-dependency-menu" collisionPadding={10} sideOffset={4}>
      <div className="project-dependency-menu__search"><input aria-label="Dependencies…" autoFocus onChange={event => setQuery(event.target.value)} onKeyDown={event => event.stopPropagation()} placeholder="Dependencies…" value={query}/></div>
      <div className="project-dependency-menu__directions">{directions.map(item => <DropdownMenu.Sub key={item.id} open={direction === item.id} onOpenChange={next => setDirection(current => next ? item.id : current === item.id ? undefined : current)}>
          <DropdownMenu.SubTrigger className="project-dependency-menu__direction"><span className="project-dependency-menu__item-background"/>{item.icon}<span>{item.label}</span><ChevronRight size={13}/></DropdownMenu.SubTrigger>
          <DropdownMenu.Portal><DropdownMenu.SubContent alignOffset={-61} className="project-detail-page__menu project-dependency-projects" collisionPadding={10} sideOffset={3}>
            <DependencyProjectPicker direction={item.id} onUpdate={onUpdate} onUpdateProject={onUpdateProject} project={project} projects={projects} viewer={viewer}/>
          </DropdownMenu.SubContent></DropdownMenu.Portal>
        </DropdownMenu.Sub>)}
        {!directions.length && <DropdownMenu.Label>No results</DropdownMenu.Label>}
      </div>
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function DependencyProjectPicker({ direction, onUpdate, onUpdateProject, project, projects, viewer }: {
  direction: ProjectDependencyDirection
  onUpdate: (input: ProjectMutationInput) => Promise<void>
  onUpdateProject: ProjectDetailProps['onUpdate']
  project: Project
  projects: Project[]
  viewer: User
}) {
  const [query, setQuery] = useState('')
  const candidates = projects.filter(item => item.id !== project.id && item.name.toLowerCase().includes(query.trim().toLowerCase()))
  const yourProjects = candidates.filter(item => item.lead?.id === viewer.id || (item.memberIds ?? []).includes(viewer.id))
  const otherProjects = candidates.filter(item => !yourProjects.includes(item))
  const checked = (candidate: Project) => direction === 'blockedBy' ? (project.dependencyIds ?? []).includes(candidate.id) : (candidate.dependencyIds ?? []).includes(project.id)
  const toggle = async (candidate: Project) => {
    if (direction === 'blockedBy') {
      await onUpdate({ dependencyIds: toggleId(project.dependencyIds ?? [], candidate.id) })
      return
    }
    try { await onUpdateProject(candidate.id, { dependencyIds: toggleId(candidate.dependencyIds ?? [], project.id) }) }
    catch (error) { toast.error('Could not update project dependency', { description: error instanceof Error ? error.message : undefined }) }
  }
  const rows = (items: Project[], label: string) => items.length ? <DropdownMenu.Group>
    <DropdownMenu.Label className="project-dependency-projects__group-label">{label}</DropdownMenu.Label>
    {items.map(candidate => <DropdownMenu.CheckboxItem checked={checked(candidate)} className="project-dependency-projects__item" key={candidate.id} onCheckedChange={() => void toggle(candidate)} onSelect={event => event.preventDefault()}>
      <span className="project-dependency-menu__item-background"/><span className="project-dependency-projects__checkbox">{checked(candidate) && <CheckboxMark/>}</span><span className="project-dependency-projects__label"><ProjectIcon size={16} style={{ color: candidate.color }}/><span data-i18n-ignore>{candidate.name}</span></span>
    </DropdownMenu.CheckboxItem>)}
  </DropdownMenu.Group> : null

  return <>
    <div className="project-dependency-menu__search"><input aria-label={direction === 'blockedBy' ? 'Mark as blocked by…' : 'Mark as blocking…'} autoFocus onChange={event => setQuery(event.target.value)} onKeyDown={event => event.stopPropagation()} placeholder={direction === 'blockedBy' ? 'Mark as blocked by…' : 'Mark as blocking…'} value={query}/></div>
    <div className="project-dependency-projects__list">{rows(yourProjects, 'Your projects')}{rows(otherProjects, 'Other projects')}{!candidates.length && <DropdownMenu.Label>No projects found</DropdownMenu.Label>}</div>
  </>
}

function SidebarProperties({ initiatives, integrationConnections, labelGroups, labels, onUpdate, project, projects, projectStatuses, teams, users }: { initiatives: Initiative[]; integrationConnections: IntegrationConnection[]; labelGroups: LabelGroup[]; labels: IssueLabel[]; onUpdate: (input: ProjectMutationInput) => Promise<void>; project: Project; projects: Project[]; projectStatuses: ProjectStatus[]; teams: Team[]; users: User[] }) {
  const { formatDate, locale } = useI18n()
  const statuses = uniqueById(projectStatuses.length ? projectStatuses : projects.map(item => item.status))
  const groupNames = new Map(labelGroups.filter(group => group.resourceType === 'project').map(group => [group.id, group.name]))
  const memberIds = project.memberIds ?? []
  const teamIds = project.teamIds ?? []
  const labelIds = (project.labelIds ?? []).filter(id => labels.some(label => label.id === id))
  const members = users.filter(user => memberIds.includes(user.id))
  const selectedMemberIds = [...new Set([...memberIds, ...(project.lead?.id ? [project.lead.id] : [])])]
  const selectedTeams = teams.filter(team => teamIds.includes(team.id))
  const selectedLabels = labels.filter(label => labelIds.includes(label.id))
  const selectedInitiatives = project.initiatives ?? []
  const toggleLabel = (id: string) => { void onUpdate({ labelIds: toggleGroupedLabelIds(labelIds, id, labels) }) }
  return <div className="project-details-sidebar__properties">
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Status</span><PropertyMenu label="Status" value={project.status.name} selectedId={project.status.id} triggerClassName="project-details-sidebar__property-trigger is-status" surfaceClassName="project-details-sidebar__property-menu is-standard" side="left" alignOffset={-4} searchPlaceholder="Change status…" searchShortcut="P, then S" trigger={<><ProjectStatusIcon color={project.status.color} name={project.status.name} size={16} type={project.status.type}/><span>{project.status.name}</span></>} icon={<ProjectStatusIcon color={project.status.color} name={project.status.name} size={14} type={project.status.type}/>} options={statuses.map((status, index) => ({ id: status.id, label: status.name, icon: <ProjectStatusIcon color={projectStatusOptionColor(status, project.status)} name={status.name} size={16} type={status.type}/>, shortcut: String(index + 1) }))} onChange={statusId => void onUpdate({ statusId })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Priority</span><PropertyMenu label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-standard" side="left" alignOffset={-4} searchPlaceholder="Change priority…" searchShortcut="P, then P" trigger={<><PriorityIcon priority={project.priority} size={16}/><span>{project.priorityLabel}</span></>} icon={<PriorityIcon priority={project.priority} size={14}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={16}/>, shortcut: String(priority) }))} onChange={priority => void onUpdate({ priority: Number(priority) })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Lead</span><PropertyMenu label="Lead" value={project.lead?.displayName ?? 'Lead'} valueIsEntityName={Boolean(project.lead)} selectedId={project.lead?.id ?? ''} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-standard" side="left" alignOffset={-4} searchPlaceholder="Change lead…" searchShortcut="P, then A" trigger={<>{project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={16}/>}<span data-i18n-ignore={project.lead ? true : undefined}>{project.lead?.displayName ?? 'Lead'}</span></>} icon={project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No lead', icon: <NoAssigneeIcon size={16}/>, shortcut: '0' }, ...users.map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/>, groupLabel: user.id === project.lead?.id ? undefined : 'Users from the project team', end: user.active ? undefined : 'Invited', i18nIgnore: true })), { id: '__invite-project-member__', label: 'Invite and add…', icon: <Plus size={14}/>, groupLabel: 'New user' }]} onChange={leadId => leadId === '__invite-project-member__' ? inviteProjectMember() : void onUpdate({ leadId })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Members</span><PropertyMenu multiple label="Members" value={members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Add member'} valueIsEntityName={members.length === 1} selectedIds={selectedMemberIds} triggerClassName="project-details-sidebar__property-trigger is-members" surfaceClassName="project-details-sidebar__property-menu is-members" side="left" alignOffset={-4} searchPlaceholder="Change members…" searchShortcut="P, then M" trigger={<>{members[0] ? <Avatar name={members[0].displayName}/> : <MembersIcon size={16}/>}<span data-i18n-ignore={members.length === 1 ? true : undefined}>{members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Add member'}</span></>} icon={members[0] ? <Avatar name={members[0].displayName}/> : <MembersIcon size={14}/>} options={[...users.map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/>, groupLabel: selectedMemberIds.includes(user.id) ? undefined : 'Users from the project team', end: project.lead?.id === user.id ? 'Project lead' : user.active ? undefined : 'Invited', i18nIgnore: true })), { id: '__invite-project-member__', label: 'Invite and add…', icon: <Plus size={14}/>, groupLabel: 'New user' }]} onChange={memberId => { if (memberId === '__invite-project-member__') { inviteProjectMember(); return } if (memberId === project.lead?.id) return; void onUpdate({ memberIds: toggleId(memberIds, memberId) }) }}/></div>
    <div className="project-details-sidebar__property-dates"><span>Dates</span><div><ProjectDatePicker align="start" contentClassName="project-details-sidebar__date-menu" label="Start date" max={project.targetDate} onChange={(startDate, startDateResolution) => void onUpdate({ startDate, startDateResolution: startDateResolution ?? '' })} resolution={project.startDateResolution} side="left" value={project.startDate}><CalendarIcon size={14} variant="start"/><span>{formatProjectDate(project.startDate, project.startDateResolution, 'Start', locale, formatDate)}</span></ProjectDatePicker><span>→</span><ProjectDatePicker align="start" contentClassName="project-details-sidebar__date-menu" label="Target date" min={project.startDate} onChange={(targetDate, targetDateResolution) => void onUpdate({ targetDate, targetDateResolution: targetDateResolution ?? '' })} resolution={project.targetDateResolution} side="left" value={project.targetDate}><CalendarIcon size={14}/><span>{formatProjectDate(project.targetDate, project.targetDateResolution, 'Target', locale, formatDate)}</span></ProjectDatePicker></div></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Teams</span>{teams.length===1?<button className="project-details-sidebar__property-trigger" disabled type="button"><TeamIcon size={16}/><span data-i18n-ignore>{selectedTeams[0]?.name??teams[0].name}</span></button>:<PropertyMenu multiple label="Teams" value={selectedTeams.map(team => team.name).join(', ') || 'Add team'} valueIsEntityName={selectedTeams.length > 0} selectedIds={teamIds} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-members" side="left" alignOffset={-4} searchPlaceholder="Change teams…" trigger={<><TeamIcon size={16}/><span data-i18n-ignore={selectedTeams.length ? true : undefined}>{selectedTeams.map(team => team.name).join(', ') || 'Add team'}</span></>} icon={<TeamIcon size={14}/>} options={teams.map(team => ({ id: team.id, label: team.name, color: team.color, icon: <TeamIcon size={16}/>, i18nIgnore: true }))} onChange={teamId => { if (teamIds.includes(teamId)&&teamIds.length===1)return; void onUpdate({ teamIds: toggleId(teamIds, teamId) }) }}/>}</div>
    <ProjectSlackMenu integrationConnections={integrationConnections} onUpdate={onUpdate} project={project}/>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Initiatives</span><PropertyMenu hideSearch multiple label="Initiatives" value={selectedInitiatives.length===1?initiatives.find(item=>item.id===selectedInitiatives[0])?.name??'No initiative':selectedInitiatives.length?`${selectedInitiatives.length} initiatives`:'No initiative'} valueIsEntityName={selectedInitiatives.length===1} selectedIds={selectedInitiatives} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-members is-initiatives" side="left" alignOffset={-4} searchPlaceholder="Change initiatives…" searchShortcut="P, then N" trigger={<><Flag size={16}/><span data-i18n-ignore={selectedInitiatives.length===1||undefined}>{selectedInitiatives.length===1?initiatives.find(item=>item.id===selectedInitiatives[0])?.name??'No initiative':selectedInitiatives.length?`${selectedInitiatives.length} initiatives`:'No initiative'}</span></>} icon={<Flag size={14}/>} options={initiatives.map(item=>({id:item.id,label:item.name,icon:<Flag size={16}/>,groupLabel:initiativeStatusLabel(item.status),i18nIgnore:true}))} onChange={initiativeId=>void onUpdate({initiatives:toggleId(selectedInitiatives,initiativeId)})}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Labels</span><PropertyMenu multiple label="Labels" value={selectedLabels.map(label => label.name).join(', ') || 'Add label'} valueIsEntityName={selectedLabels.length > 0} selectedIds={labelIds} triggerClassName="project-details-sidebar__property-trigger is-labels" surfaceClassName="project-details-sidebar__property-menu is-labels" side="left" alignOffset={-4} searchPlaceholder="Change labels…" searchShortcut="P, then L" showGroupHeadings={false} trigger={<><LabelIcon size={16}/><span data-i18n-ignore={selectedLabels.length ? true : undefined}>{selectedLabels.map(label => label.name).join(', ') || 'Add label'}</span></>} icon={<LabelIcon size={14}/>} options={labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, resourceType: label.resourceType, groupId: label.groupId, groupLabel: label.groupId ? groupNames.get(label.groupId) : undefined, i18nIgnore: true }))} onChange={toggleLabel}/></div>
  </div>
}

function ProjectSlackMenu({ integrationConnections, onUpdate, project }: { integrationConnections: IntegrationConnection[]; onUpdate: (input: ProjectMutationInput) => Promise<void>; project: Project }) {
  const channels = [...new Set(integrationConnections.filter(connection => connection.provider.toLowerCase() === 'slack' && connection.status === 'connected').flatMap(connection => connection.channels))]
  const openIntegrations = () => {
    const workspace = location.pathname.split('/').filter(Boolean)[0]
    if (workspace) location.assign(`/${workspace}/settings/integrations`)
  }
  return <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Slack</span><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Slack channel" className="project-details-sidebar__property-trigger" type="button"><SlackIcon size={16}/><span data-i18n-ignore={project.slackChannelName ? true : undefined}>{project.slackChannelName ? `#${project.slackChannelName}` : 'Slack channel'}</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" alignOffset={-4} className="project-detail-page__menu project-details-sidebar__slack-menu" side="left" sideOffset={4}>
    {channels.length ? channels.map(channel => <DropdownMenu.CheckboxItem checked={project.slackChannelId === channel} key={channel} onCheckedChange={() => void onUpdate({ slackChannelId: channel, slackChannelName: channel })}><SlackIcon size={16}/><span data-i18n-ignore>{channel}</span>{project.slackChannelId === channel && <CheckboxMark/>}</DropdownMenu.CheckboxItem>) : <DropdownMenu.Item onSelect={openIntegrations}><SlackIcon size={16}/><span>Connect channel</span></DropdownMenu.Item>}
    {project.slackChannelId && <DropdownMenu.Item onSelect={() => void onUpdate({ slackChannelId: '', slackChannelName: '' })}><X size={16}/><span>Disconnect channel</span></DropdownMenu.Item>}
    <DropdownMenu.Separator/><DropdownMenu.Item onSelect={openIntegrations}><SlackIcon size={16}/><span>Manage Slack settings…</span></DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>
}

class SidebarPropertiesBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('Project properties failed to render', error, info) }
  render() { return this.state.error ? <p className="project-details-sidebar__property-error">{this.state.error.message}</p> : this.props.children }
}

function MilestoneEditor({ milestone, onCancel, onSubmit, progress }: { milestone?: ProjectMilestone; onCancel: () => void; onSubmit: (input: { name?: string; description?: string; targetDate?: string }) => Promise<void>; progress: number }) {
  const [name, setName] = useState(milestone?.name ?? '')
  const [date, setDate] = useState(milestone?.targetDate ?? '')
  const [saving, setSaving] = useState(false)
  const submit = () => {
    if (!name.trim() || saving) return
    setSaving(true)
    void onSubmit({ name: name.trim(), targetDate: date }).finally(() => setSaving(false))
  }
  return <form className="project-details-sidebar__milestone-editor" onSubmit={event => { event.preventDefault(); submit() }}>
    <MilestoneProgressIcon progress={progress}/>
    <input autoFocus aria-label="Milestone name" disabled={saving} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); onCancel() } }} placeholder="Milestone name" value={name}/>
    <ProjectDatePicker buttonClassName="project-details-sidebar__milestone-editor-date" label="Target date" onChange={setDate} value={date}><span>{date ? format(new Date(`${date}T00:00:00`), 'MMM d') : 'No date'}</span></ProjectDatePicker>
    <button aria-label="Cancel" className="project-details-sidebar__milestone-editor-cancel" onClick={onCancel} type="button"><MilestoneMenuIcon name="close"/></button>
  </form>
}

function MilestoneDateDialog({ milestone, onOpenChange, onSubmit, open }: { milestone: ProjectMilestone; onOpenChange: (open: boolean) => void; onSubmit: (targetDate: string) => Promise<ProjectMilestone>; open: boolean }) {
  const initial = parseMilestoneDate(milestone.targetDate) ?? new Date()
  const [selected, setSelected] = useState(initial)
  const [cursor, setCursor] = useState(() => new Date(initial.getFullYear(), initial.getMonth(), 1))
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    if (!open) return
    const next = parseMilestoneDate(milestone.targetDate) ?? new Date()
    setSelected(next)
    setCursor(new Date(next.getFullYear(), next.getMonth(), 1))
  }, [milestone.targetDate, open])
  const secondMonth = new Date(cursor.getFullYear(), cursor.getMonth() + 1, 1)
  const save = async () => {
    setSaving(true)
    try {
      await onSubmit(format(selected, 'yyyy-MM-dd'))
      onOpenChange(false)
    } finally { setSaving(false) }
  }
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal>
    <Dialog.Overlay className="project-milestone-date-dialog__overlay"/>
    <Dialog.Content aria-describedby={undefined} className="project-milestone-date-dialog" onOpenAutoFocus={event => event.preventDefault()}>
      <div className="project-milestone-date-dialog__surface"><form onSubmit={event => { event.preventDefault(); void save() }}>
        <div className="project-milestone-date-dialog__body">
          <Dialog.Title>Set {milestone.name} target date</Dialog.Title>
          <div className="project-milestone-date-dialog__calendars">
            <MilestoneCalendarMonth month={cursor} onSelect={setSelected} selected={selected}/>
            <MilestoneCalendarMonth month={secondMonth} onSelect={setSelected} selected={selected}/>
            <button aria-label="Previous month" className="project-milestone-date-dialog__previous" onClick={() => setCursor(new Date(cursor.getFullYear(), cursor.getMonth() - 1, 1))} type="button"><ChevronRight size={14}/></button>
            <button aria-label="Next month" className="project-milestone-date-dialog__next" onClick={() => setCursor(new Date(cursor.getFullYear(), cursor.getMonth() + 1, 1))} type="button"><ChevronRight size={14}/></button>
          </div>
        </div>
        <footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={saving} type="submit">Set</button></footer>
      </form></div>
    </Dialog.Content>
  </Dialog.Portal></Dialog.Root>
}

function MilestoneCalendarMonth({ month, onSelect, selected }: { month: Date; onSelect: (date: Date) => void; selected: Date }) {
  const today = new Date()
  const days = milestoneMonthDays(month)
  const monthLabel = format(month, 'MMMM yyyy')
  return <section className="project-milestone-date-dialog__month"><h3>{monthLabel}</h3><div aria-label={monthLabel} className="project-milestone-date-dialog__grid" role="grid">
    <div className="project-milestone-date-dialog__weekdays" role="row">{['Mo','Tu','We','Th','Fr','Sa','Su'].map(day => <span key={day} role="columnheader">{day}</span>)}</div>
    <div className="project-milestone-date-dialog__days">{days.map((date, index) => date ? <button aria-label={format(date, 'EEEE, MMMM do, yyyy')} aria-selected={sameMilestoneDay(date, selected)} className={sameMilestoneDay(date, selected) ? 'is-selected' : sameMilestoneDay(date, today) ? 'is-today' : ''} key={format(date, 'yyyy-MM-dd')} onClick={() => onSelect(date)} role="gridcell" type="button">{date.getDate()}</button> : <span aria-hidden="true" key={`empty-${index}`} role="gridcell"/>)}</div>
  </div></section>
}

function parseMilestoneDate(value?: string) { if (!value) return undefined; const date = new Date(`${value}T00:00:00`); return Number.isNaN(date.getTime()) ? undefined : date }
function sameMilestoneDay(left: Date, right: Date) { return left.getFullYear() === right.getFullYear() && left.getMonth() === right.getMonth() && left.getDate() === right.getDate() }
function milestoneMonthDays(month: Date) {
  const first = new Date(month.getFullYear(), month.getMonth(), 1)
  const offset = (first.getDay() + 6) % 7
  const count = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  return Array.from({ length: 42 }, (_, index) => {
    const day = index - offset + 1
    return day > 0 && day <= count ? new Date(month.getFullYear(), month.getMonth(), day) : null
  })
}

function ProgressChart({ issues, persistedHistory, progress, start, target }: { issues: Issue[]; persistedHistory?: PersistedProgressHistory; progress: number; start?: string; target?: string }) {
  const chart = useMemo(() => buildProgressData(issues, start, target, persistedHistory), [issues, persistedHistory, start, target])
  const chartId = useId().replaceAll(':', '')
  const markers = [
    { axis: 'x' as const, value: chart.currentDate, lineStyle: { stroke: 'var(--progress-current-line)', strokeWidth: 1 } },
    { axis: 'x' as const, value: chart.targetDate, lineStyle: { stroke: 'var(--progress-target-line)', strokeWidth: 1 } },
  ]
  const defs = [
    { id: 'flowScopeGradient', type: 'linearGradient' as const, colors: [{ offset: 0, color: 'var(--progress-scope-fill)', opacity: .3 }, { offset: 20, color: 'var(--progress-scope-fill)', opacity: .1 }, { offset: 60, color: 'var(--progress-scope-fill)', opacity: 0 }] },
    { id: 'flowStartedGradient', type: 'linearGradient' as const, colors: [{ offset: 0, color: 'var(--progress-started)', opacity: .1 }, { offset: 28, color: 'var(--progress-started)', opacity: 0 }] },
    { id: 'flowCompletedGradient', type: 'linearGradient' as const, colors: [{ offset: 0, color: 'var(--progress-completed)', opacity: .4 }, { offset: 80, color: 'var(--progress-completed)', opacity: 0 }] },
  ]
  const fill = [
    { match: { id: 'Scope' }, id: 'flowScopeGradient' },
    { match: { id: 'Started' }, id: 'flowStartedGradient' },
    { match: { id: 'Completed' }, id: 'flowCompletedGradient' },
  ]
  const CompletionBars = (props: LineCustomSvgLayerProps<ProgressSeries>) => <ProgressCompletionBars {...props} changes={chart.completedChanges}/>
  const Forecast = (props: LineCustomSvgLayerProps<ProgressSeries>) => <ProgressForecastLayer {...props} chartId={chartId} completed={chart.forecast.completed} currentDate={chart.currentDate} optimisticDate={chart.forecast.optimisticDate} pessimisticDate={chart.forecast.pessimisticDate} targetDate={chart.targetDate} total={chart.forecast.total}/>

  return <div className="project-details-sidebar__chart">
    <Line<ProgressSeries>
      animate={false}
      ariaLabel={`${Math.round(progress * 100)}% project progress`}
      areaOpacity={1}
      axisBottom={{ tickPadding: 3, tickSize: 6, tickValues: [chart.startDate, chart.endDate], renderTick: ProgressAxisTick }}
      axisLeft={null}
      colors={['var(--progress-scope-line)', 'var(--progress-started)', 'var(--progress-completed)']}
      curve="monotoneX"
      data={chart.series}
      defs={defs}
      enableArea
      enableCrosshair={false}
      enableGridX={false}
      enableGridY={false}
      enablePoints={false}
      enableSlices="x"
      fill={fill}
      isInteractive
      layers={['markers', 'axes', 'areas', Forecast, ProgressLinesLayer, CompletionBars, ProgressActiveSliceLayer, 'slices']}
      lineWidth={1}
      margin={{ top: 38, right: 0, bottom: 30, left: 0 }}
      markers={markers}
      role="img"
      sliceTooltip={ProgressSliceTooltip}
      theme={{ axis: { ticks: { line: { stroke: 'var(--pd-label)', strokeWidth: 1 }, text: { fill: 'var(--pd-label)', fontSize: 12 } } }, tooltip: { container: { background: 'transparent', boxShadow: 'none', padding: 0 } } }}
      useMesh={false}
      width={359}
      xScale={{ type: 'time', format: 'native', precision: 'day', min: chart.startDate, max: chart.endDate }}
      yScale={{ type: 'linear', stacked: false, min: 0, max: Math.max(1, issues.length) }}
      height={200}
    />
  </div>
}

function ProgressLinesLayer({ lineGenerator, series }: LineCustomSvgLayerProps<ProgressSeries>) {
  return <g>{series.map(item => {
    const path = lineGenerator(item.data.map(point => point.position))
    const end = item.data[item.data.length - 1]?.position
    return <g key={item.id}><path className={`project-details-sidebar__progress-line is-${String(item.id).toLowerCase()}`} d={path ?? undefined}/>{item.id !== 'Scope' && end && <circle className={`project-details-sidebar__progress-end is-${String(item.id).toLowerCase()}`} cx={end.x} cy={end.y} r="2.5"/>}</g>
  })}</g>
}

function ProgressForecastLayer({ chartId, completed, currentDate, lineGenerator, optimisticDate, pessimisticDate, targetDate, total, xScale, yScale, innerHeight }: LineCustomSvgLayerProps<ProgressSeries> & { chartId: string; completed: number; currentDate: Date; optimisticDate?: Date; pessimisticDate?: Date; targetDate: Date; total: number }) {
  const currentX = xScale(currentDate)
  const optimisticX = optimisticDate ? xScale(optimisticDate) : Number.NaN
  const pessimisticX = pessimisticDate ? xScale(pessimisticDate) : Number.NaN
  const targetX = xScale(targetDate)
  const hatchStart = Number.isFinite(targetX) && Number.isFinite(optimisticX) ? Math.min(targetX, optimisticX) : Number.NaN
  const hatchEnd = Number.isFinite(targetX) && Number.isFinite(optimisticX) ? Math.max(targetX, optimisticX) : Number.NaN
  const hatchWidth = hatchEnd - hatchStart
  const hasForecast = Number.isFinite(currentX) && (Number.isFinite(optimisticX) || Number.isFinite(pessimisticX))
  const hasHatch = Number.isFinite(hatchStart) && hatchWidth > 0
  if (!hasForecast) return null
  const patternId = `${chartId}-forecast-hatch`
  const maskId = `${chartId}-forecast-fade`
  const completedPath = Number.isFinite(optimisticX) ? lineGenerator([{ x: currentX, y: yScale(completed) }, { x: optimisticX, y: yScale(total) }]) : null
  const pessimisticPath = Number.isFinite(pessimisticX) ? lineGenerator([{ x: currentX, y: yScale(completed) }, { x: pessimisticX, y: yScale(total) }]) : null
  return <g className="project-details-sidebar__forecast" pointerEvents="none">
    {hasHatch && <defs>
      <pattern height="8" id={patternId} patternUnits="userSpaceOnUse" width="8">
        <path d="M-2 8 8-2M6 10 10 6" fill="none" stroke="var(--progress-target-line)" strokeWidth="1.5"/>
      </pattern>
      <linearGradient id={maskId} x1="0" x2="1" y1="0" y2="0">
        <stop offset="0%" stopColor="white" stopOpacity=".75"/>
        <stop offset="65%" stopColor="white" stopOpacity=".34"/>
        <stop offset="100%" stopColor="white" stopOpacity="0"/>
      </linearGradient>
      <mask id={`${maskId}-mask`} maskUnits="userSpaceOnUse" x={hatchStart} y="0" width={hatchWidth} height={innerHeight}>
        <rect fill={`url(#${maskId})`} height={innerHeight} width={hatchWidth} x={hatchStart} y="0"/>
      </mask>
    </defs>}
    {hasHatch && <rect className="project-details-sidebar__forecast-hatch" fill={`url(#${patternId})`} height={innerHeight} mask={`url(#${maskId}-mask)`} width={hatchWidth} x={hatchStart} y="0"/>}
    {completedPath && <path className="project-details-sidebar__progress-line is-completed-forecast" d={completedPath}/>}
    {pessimisticPath && <path className="project-details-sidebar__progress-line is-completed-forecast is-pessimistic-forecast" d={pessimisticPath}/>}
  </g>
}

function ProgressAxisTick({ lineX, lineY, textY, tickIndex, value, x, y }: { lineX: number; lineY: number; textY: number; tickIndex: number; value: Date | string | number; x: number; y: number }) {
  const date = value instanceof Date ? value : new Date(value)
  const isStart = tickIndex === 0
  return <g transform={`translate(${x},${y})`}><line className="project-details-sidebar__axis-tick-line" x1="0" x2={lineX} y1="1.5" y2={lineY}/><text className="project-details-sidebar__axis-tick-label" dominantBaseline="text-before-edge" textAnchor={isStart ? 'start' : 'end'} transform={`translate(${isStart ? 2 : -2},${textY})`}>{format(date, 'MMM d')}</text></g>
}

function ProgressCompletionBars({ changes, innerHeight, xScale }: LineCustomSvgLayerProps<ProgressSeries> & { changes: { x: Date; y: number }[] }) {
  const maxChange = Math.max(1, ...changes.map(item => item.y))
  return <g>{changes.map(item => {
    if (!item.y) return null
    const height = Math.max(3, Math.round(innerHeight * .3 * item.y / maxChange))
    return <rect className="project-details-sidebar__completed-bar" height={height} key={item.x.toISOString()} rx="1" width="4" x={Math.round(xScale(item.x)) - 2} y={innerHeight - height}/>
  })}</g>
}

function ProgressActiveSliceLayer({ currentSlice, innerHeight }: LineCustomSvgLayerProps<ProgressSeries>) {
  if (!currentSlice) return null
  return <g className="project-details-sidebar__chart-hover" pointerEvents="none"><line x1={currentSlice.x} x2={currentSlice.x} y1="0" y2={innerHeight}/>{currentSlice.points.map(point => <circle className={`is-${String(point.seriesId).toLowerCase()}`} cx={point.x} cy={point.y} key={point.id} r="3"/>)}</g>
}

function ProgressSliceTooltip({ slice }: SliceTooltipProps<ProgressSeries>) {
  const date = slice.points[0]?.data.x as Date | undefined
  return <div className="project-details-sidebar__chart-tooltip"><time>{date ? format(date, 'MMM d, yyyy') : ''}</time>{slice.points.slice().reverse().map(point => <div key={point.id}><i className={`is-${String(point.seriesId).toLowerCase()}`}/><span>{point.seriesId}</span><strong>{point.data.y}</strong></div>)}</div>
}

function ProgressBreakdownRow({ icon, issues, label, onOpen }: { icon: ReactNode; issues: Issue[]; label: string; onOpen: () => void }) {
  const completed = issues.filter(issue => issue.state.type === 'completed').length
  const engaged = issues.filter(issue => ['started','completed'].includes(issue.state.type)).length
  const percent = issues.length ? Math.round(completed / issues.length * 100) : 0
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpen() } }
  return <div aria-label={`${label} ${percent}% of ${issues.length}`} className="project-details-sidebar__breakdown-row" onClick={onOpen} onKeyDown={onKeyDown} role="button" tabIndex={0}>
    <div className="project-details-sidebar__breakdown-name">{icon}<span data-i18n-ignore>{label}</span><button onClick={event => { event.stopPropagation(); onOpen() }} tabIndex={-1} type="button">See issues</button></div>
    <ProgressRing completed={completed} engaged={engaged} total={issues.length}/><span className="project-details-sidebar__breakdown-percent">{percent}% of</span><button className="project-details-sidebar__breakdown-count" onClick={event => { event.stopPropagation(); onOpen() }} tabIndex={-1} type="button">{issues.length}</button>
  </div>
}

function ProgressRing({ completed, engaged, total }: { completed: number; engaged: number; total: number }) {
  const circumference = 2 * Math.PI * 7
  const completeLength = total ? circumference * completed / total : 0
  const startedLength = total ? circumference * Math.max(0, engaged - completed) / total : 0
  const startedRotation = -90 + (total ? completed / total * 360 : 0)
  return <svg aria-hidden="true" className="project-details-sidebar__progress-ring" height="16" viewBox="0 0 16 16" width="16"><circle className="is-track" cx="8" cy="8" fill="none" r="7" strokeWidth="2"/><circle className="is-started" cx="8" cy="8" fill="none" r="7" strokeDasharray={`${startedLength} ${circumference}`} strokeLinecap="round" strokeWidth="2" transform={`rotate(${startedRotation} 8 8)`}/><circle className="is-completed" cx="8" cy="8" fill="none" r="7" strokeDasharray={`${completeLength} ${circumference}`} strokeLinecap="round" strokeWidth="2" transform="rotate(-90 8 8)"/></svg>
}

function projectEvents(project: Project, updates: ProjectUpdate[], viewer: User) {
  const events = updates.map(update => ({ id: update.id, actor: update.user.displayName, text: `posted an ${update.health === 'noUpdate' ? '' : update.health.replace(/[A-Z]/g, match => ` ${match.toLowerCase()}`)} update`, date: formatDistanceToNowStrict(new Date(update.createdAt), { addSuffix: true }) }))
  if (project.priority > 0) events.push({ id: 'priority', actor: project.lead?.displayName ?? viewer.displayName, text: `changed priority from No priority to ${project.priorityLabel}`, date: format(new Date(project.updatedAt), 'MMM d') })
  if (project.lead) events.push({ id: 'lead', actor: project.lead.displayName, text: 'assigned themselves as a lead', date: format(new Date(project.updatedAt), 'MMM d') })
  events.push({ id: 'created', actor: project.lead?.displayName ?? viewer.displayName, text: 'created the project', date: format(new Date(project.createdAt), 'MMM d') })
  return events
}

const formatProjectDate = formatProjectPropertyDate
function milestoneStats(issues: Issue[], milestoneId?: string) { const items = issues.filter(issue => (issue.projectMilestoneId ?? '') === (milestoneId ?? '')); const completed = items.filter(issue => issue.state.type === 'completed').length; return { count: items.length, progress: items.length ? Math.round(completed / items.length * 100) : 0 } }
function toggleId(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
function uniqueById<T extends { id: string }>(values: T[]) { return [...new Map(values.map(value => [value.id, value])).values()] }
