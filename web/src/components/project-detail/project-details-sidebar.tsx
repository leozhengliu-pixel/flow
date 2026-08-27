import { Component, useEffect, useMemo, useState, type DragEvent, type ErrorInfo, type KeyboardEvent, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Line, type LineCustomSvgLayerProps, type SliceTooltipProps } from '@nivo/line'
import { Blocks, ChevronDown, ChevronRight, OctagonMinus, Plus, Trash2 } from 'lucide-react'
import { addDays, differenceInCalendarDays, format, formatDistanceToNowStrict, startOfDay } from 'date-fns'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { CalendarIcon, LabelIcon, MembersIcon, NoAssigneeIcon, PriorityIcon, ProjectIcon, ProjectStatusIcon, SlackIcon, TeamIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import type { Issue, IssueLabel, LabelGroup, Project, ProjectMilestone, ProjectStatus, ProjectUpdate, Team, User } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { ProjectDetailTab, ProjectDetailProps } from './project-detail-types'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { PRIORITY_LABELS } from './project-detail-types'

export function ProjectDetailsSidebar({ labelGroups, labels, onConvertMilestone, onCreateMilestone, onDeleteMilestone, onMoveMilestone, onOpenIssueFilter, onOpenMilestoneIssues, onReorderMilestones, onTabChange, onUpdate, onUpdateProject, onUpdateMilestone, project, projectIssues, projects, projectStatuses, projectUpdates, teams, users, viewer }: {
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
      <SidebarPropertiesBoundary><SidebarProperties labelGroups={labelGroups} labels={labels} onUpdate={onUpdate} project={project} projects={projects} projectStatuses={projectStatuses} teams={teams} users={users}/></SidebarPropertiesBoundary>
      {(blockedByProjects.length > 0 || blockingProjects.length > 0) && <div className="project-details-sidebar__dependencies">
        {blockedByProjects.map(item => <div key={`blocked-by-${item.id}`}><OctagonMinus className="project-details-sidebar__dependency-icon" size={14}/><small>Blocked by</small><span data-i18n-ignore>{item.name}</span><button aria-label={`Remove ${item.name}`} onClick={() => void onUpdate({ dependencyIds: (project.dependencyIds ?? []).filter(id => id !== item.id) })} type="button"><Trash2 size={11}/></button></div>)}
        {blockingProjects.map(item => <div key={`blocking-${item.id}`}><Blocks className="project-details-sidebar__dependency-icon" size={14}/><small>Blocking</small><span data-i18n-ignore>{item.name}</span><button aria-label={`Remove ${item.name}`} onClick={() => void onUpdateProject(item.id, { dependencyIds: (item.dependencyIds ?? []).filter(id => id !== project.id) })} type="button"><Trash2 size={11}/></button></div>)}
      </div>}
    </SidebarSection>

    <SidebarSection onToggle={() => setMilestonesOpen(value => !value)} open={milestonesOpen} title="Milestones" action={<button aria-label="Add milestone" data-project-milestone-add onClick={() => { setMilestonesOpen(true); setMilestoneEditor('new') }} type="button"><svg aria-hidden="true" height="16" viewBox="0 0 16 16" width="16"><use href="/flow-milestone-icons.svg?v=3#plus"/></svg></button>}>
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
        <ProgressChart issues={projectIssues} progress={project.progress} start={project.startDate} target={project.targetDate}/>
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
      <MilestoneMenuItem className="is-danger" end="⌘ ⌫" icon="trash" label="Delete…" onSelect={() => { if (window.confirm(`Delete “${milestone.name}”?`)) void onDelete() }}/>
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    <MilestoneDateDialog milestone={milestone} onOpenChange={setDateDialogOpen} onSubmit={onUpdateDate} open={dateDialogOpen}/>
  </div>
}

type MilestoneMenuIconName = 'calendar'|'chevron-right'|'close'|'copy'|'edit'|'issues'|'link'|'link-name'|'milestone'|'more-horizontal'|'project'|'trash'

function MilestoneMenuIcon({ name }: { name: MilestoneMenuIconName }) {
  return <svg aria-hidden="true" height="16" viewBox="0 0 16 16" width="16"><use href={`/flow-milestone-icons.svg?v=3#${name}`}/></svg>
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
  if (unassigned || clamped === 0) return <svg aria-hidden="true" className="project-details-sidebar__milestone-icon is-unassigned" height="16" viewBox="0 0 16 16" width="16"><use href="/flow-milestone-icons.svg?v=3#milestone-shape"/></svg>
  if (clamped === 100) return <svg aria-hidden="true" className="project-details-sidebar__milestone-icon is-complete" height="16" viewBox="0 0 16 16" width="16"><use href="/flow-milestone-icons.svg?v=3#milestone-shape"/></svg>
  return <svg aria-hidden="true" className="project-details-sidebar__milestone-icon is-progress" height="16" viewBox="0 0 16 16" width="16"><use className="is-track" href="/flow-milestone-icons.svg?v=3#milestone-shape"/><use className="is-value" href="/flow-milestone-icons.svg?v=3#milestone-shape" strokeDasharray={`${completedLength} ${pathLength - completedLength}`}/></svg>
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

function SidebarProperties({ labelGroups, labels, onUpdate, project, projects, projectStatuses, teams, users }: { labelGroups: LabelGroup[]; labels: IssueLabel[]; onUpdate: (input: ProjectMutationInput) => Promise<void>; project: Project; projects: Project[]; projectStatuses: ProjectStatus[]; teams: Team[]; users: User[] }) {
  const statuses = uniqueById(projectStatuses.length ? projectStatuses : projects.map(item => item.status))
  const groupNames = new Map(labelGroups.filter(group => group.resourceType === 'project').map(group => [group.id, group.name]))
  const memberIds = project.memberIds ?? []
  const teamIds = project.teamIds ?? []
  const labelIds = (project.labelIds ?? []).filter(id => labels.some(label => label.id === id))
  const members = users.filter(user => memberIds.includes(user.id))
  const selectedTeams = teams.filter(team => teamIds.includes(team.id))
  const selectedLabels = labels.filter(label => labelIds.includes(label.id))
  return <div className="project-details-sidebar__properties">
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Status</span><PropertyMenu label="Status" value={project.status.name} selectedId={project.status.id} triggerClassName="project-details-sidebar__property-trigger is-status" surfaceClassName="project-details-sidebar__property-menu is-standard" side="left" alignOffset={-4} trigger={<><ProjectStatusIcon color={project.status.color} name={project.status.name} size={16} type={project.status.type}/><span>{project.status.name}</span></>} icon={<ProjectStatusIcon color={project.status.color} name={project.status.name} size={14} type={project.status.type}/>} options={statuses.map((status, index) => ({ id: status.id, label: status.name, icon: <ProjectStatusIcon color={status.color} name={status.name} size={16} type={status.type}/>, shortcut: String(index + 1) }))} onChange={statusId => void onUpdate({ statusId })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Priority</span><PropertyMenu label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-standard" side="left" alignOffset={-4} trigger={<><PriorityIcon priority={project.priority} size={16}/><span>{project.priorityLabel}</span></>} icon={<PriorityIcon priority={project.priority} size={14}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={16}/>, shortcut: String(priority) }))} onChange={priority => void onUpdate({ priority: Number(priority) })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Lead</span><PropertyMenu label="Lead" value={project.lead?.displayName ?? 'Lead'} valueIsEntityName={Boolean(project.lead)} selectedId={project.lead?.id ?? ''} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-standard" side="left" alignOffset={-4} trigger={<>{project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={16}/>}<span data-i18n-ignore={project.lead ? true : undefined}>{project.lead?.displayName ?? 'Lead'}</span></>} icon={project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No lead', icon: <NoAssigneeIcon size={16}/> }, ...users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/>, i18nIgnore: true }))]} onChange={leadId => void onUpdate({ leadId })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Members</span><PropertyMenu multiple label="Members" value={members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Add member'} valueIsEntityName={members.length === 1} selectedIds={memberIds} triggerClassName="project-details-sidebar__property-trigger is-members" surfaceClassName="project-details-sidebar__property-menu is-members" side="left" alignOffset={-4} trigger={<>{members[0] ? <Avatar name={members[0].displayName}/> : <MembersIcon size={16}/>}<span data-i18n-ignore={members.length === 1 ? true : undefined}>{members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Add member'}</span></>} icon={members[0] ? <Avatar name={members[0].displayName}/> : <MembersIcon size={14}/>} options={users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/>, i18nIgnore: true }))} onChange={memberId => void onUpdate({ memberIds: toggleId(memberIds, memberId) })}/></div>
    <div className="project-details-sidebar__property-dates"><span>Dates</span><div><ProjectDatePicker align="start" contentClassName="project-details-sidebar__date-menu" label="Start date" onChange={startDate => void onUpdate({ startDate })} side="left" value={project.startDate}><CalendarIcon size={14} variant="start"/><span>{formatProjectDate(project.startDate, 'Start')}</span></ProjectDatePicker><span>→</span><ProjectDatePicker align="start" contentClassName="project-details-sidebar__date-menu" label="Target date" onChange={targetDate => void onUpdate({ targetDate })} side="left" value={project.targetDate}><CalendarIcon size={14}/><span>{formatProjectDate(project.targetDate, 'Target')}</span></ProjectDatePicker></div></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Teams</span><PropertyMenu multiple label="Teams" value={selectedTeams.map(team => team.name).join(', ') || 'Add team'} valueIsEntityName={selectedTeams.length > 0} selectedIds={teamIds} triggerClassName="project-details-sidebar__property-trigger" surfaceClassName="project-details-sidebar__property-menu is-members" side="left" alignOffset={-4} trigger={<><TeamIcon size={16}/><span data-i18n-ignore={selectedTeams.length ? true : undefined}>{selectedTeams.map(team => team.name).join(', ') || 'Add team'}</span></>} icon={<TeamIcon size={14}/>} options={teams.map(team => ({ id: team.id, label: team.name, color: team.color, icon: <TeamIcon size={16}/>, i18nIgnore: true }))} onChange={teamId => void onUpdate({ teamIds: toggleId(teamIds, teamId) })}/></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Slack</span><button aria-disabled="true" className="project-details-sidebar__property-trigger" disabled title="Connect Slack in workspace settings first" type="button"><SlackIcon size={16}/><span>Slack channel</span></button></div>
    <div className="project-details-sidebar__property"><span className="project-details-sidebar__property-label">Labels</span><PropertyMenu multiple label="Labels" value={selectedLabels.map(label => label.name).join(', ') || 'Add label'} valueIsEntityName={selectedLabels.length > 0} selectedIds={labelIds} triggerClassName="project-details-sidebar__property-trigger is-labels" surfaceClassName="project-details-sidebar__property-menu is-labels" side="left" alignOffset={-4} trigger={<><LabelIcon size={16}/><span data-i18n-ignore={selectedLabels.length ? true : undefined}>{selectedLabels.map(label => label.name).join(', ') || 'Add label'}</span></>} icon={<LabelIcon size={14}/>} options={labels.map(label => ({ id: label.id, label: label.name, color: label.color, description: label.description, issueCount: label.issueCount, scope: label.scope, resourceType: label.resourceType, groupId: label.groupId, groupLabel: label.groupId ? groupNames.get(label.groupId) : undefined, i18nIgnore: true }))} onChange={labelId => void onUpdate({ labelIds: toggleId(labelIds, labelId) })}/></div>
  </div>
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

type ProgressSeries = { id: 'Scope'|'Started'|'Completed'; data: { x: Date; y: number }[] }

function ProgressChart({ issues, progress, start, target }: { issues: Issue[]; progress: number; start?: string; target?: string }) {
  const chart = useMemo(() => buildProgressData(issues, start, target), [issues, start, target])
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
      layers={['markers', 'axes', 'areas', ProgressLinesLayer, CompletionBars, ProgressActiveSliceLayer, 'slices']}
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

function buildProgressData(issues: Issue[], start?: string, target?: string) {
  const today = startOfDay(new Date())
  const createdDates = issues.map(issue => startOfDay(new Date(issue.createdAt))).filter(date => !Number.isNaN(date.getTime()))
  const requestedStart = start ? startOfDay(new Date(`${start}T00:00:00`)) : createdDates[0]
  const startDate = requestedStart && !Number.isNaN(requestedStart.getTime()) ? requestedStart : today
  const requestedTarget = target ? startOfDay(new Date(`${target}T00:00:00`)) : addDays(startDate, 14)
  const targetDate = requestedTarget > startDate ? requestedTarget : addDays(startDate, 1)
  const endDate = today > targetDate ? today : targetDate
  const currentDate = today < startDate ? startDate : today > endDate ? endDate : today
  const isCreated = (issue: Issue, date: Date) => startOfDay(new Date(issue.createdAt)) <= date
  const isStarted = (issue: Issue, date: Date) => ['started','completed'].includes(issue.state.type) && isCreated(issue, date)
  const isCompleted = (issue: Issue, date: Date) => {
    if (issue.state.type !== 'completed') return false
    const completedDate = startOfDay(new Date(issue.completedAt ?? issue.updatedAt))
    return completedDate <= date
  }
  const eventDays = uniqueDates([
    startDate,
    currentDate,
    targetDate,
    endDate,
    ...issues.flatMap(issue => [startOfDay(new Date(issue.createdAt)), ...(issue.state.type === 'completed' ? [startOfDay(new Date(issue.completedAt ?? issue.updatedAt))] : [])]),
  ]).filter(date => date >= startDate && date <= endDate)
  const scope = eventDays.map(date => ({ x: date, y: issues.filter(issue => isCreated(issue, date)).length }))
  const activeDays = eventDays.filter(date => date <= currentDate)
  const startedData = activeDays.map(date => ({ x: date, y: issues.filter(issue => isStarted(issue, date)).length }))
  const completedData = activeDays.map(date => ({ x: date, y: issues.filter(issue => isCompleted(issue, date)).length }))
  const completedChanges = buildCompletionChanges(issues, startDate, currentDate)
  return { startDate, targetDate, endDate, currentDate, completedChanges, series: [{ id: 'Scope' as const, data: scope }, { id: 'Started' as const, data: startedData }, { id: 'Completed' as const, data: completedData }] }
}

function buildCompletionChanges(issues: Issue[], startDate: Date, currentDate: Date) {
  const rangeDays = Math.max(1, differenceInCalendarDays(currentDate, startDate))
  const bucketDays = rangeDays > 90 ? 7 : 1
  const buckets = new Map<number, number>()
  for (const issue of issues) {
    if (issue.state.type !== 'completed') continue
    const completedDate = startOfDay(new Date(issue.completedAt ?? issue.updatedAt))
    const offset = differenceInCalendarDays(completedDate, startDate)
    if (offset < 0 || completedDate > currentDate) continue
    const bucketStart = addDays(startDate, Math.floor(offset / bucketDays) * bucketDays)
    const bucketDate = addDays(bucketStart, Math.floor((bucketDays - 1) / 2))
    buckets.set(bucketDate.getTime(), (buckets.get(bucketDate.getTime()) ?? 0) + 1)
  }
  return [...buckets].map(([timestamp, y]) => ({ x: new Date(timestamp), y })).sort((left, right) => left.x.getTime() - right.x.getTime())
}

function uniqueDates(values: Date[]) { return [...new Map(values.filter(date => !Number.isNaN(date.getTime())).map(date => [date.getTime(), date])).values()].sort((left, right) => left.getTime() - right.getTime()) }

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

function formatProjectDate(value: string | undefined, fallback: string) { return value ? format(new Date(`${value}T00:00:00`), 'MMM do') : fallback }
function milestoneStats(issues: Issue[], milestoneId?: string) { const items = issues.filter(issue => (issue.projectMilestoneId ?? '') === (milestoneId ?? '')); const completed = items.filter(issue => issue.state.type === 'completed').length; return { count: items.length, progress: items.length ? Math.round(completed / items.length * 100) : 0 } }
function toggleId(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
function uniqueById<T extends { id: string }>(values: T[]) { return [...new Map(values.map(value => [value.id, value])).values()] }
