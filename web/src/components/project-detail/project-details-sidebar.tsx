import { Component, useMemo, useState, type ErrorInfo, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { CalendarDays, Check, ChevronDown, ChevronRight, Flag, MessageSquare as Slack, MoreHorizontal, Plus, Tags, Trash2, Users, X } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { NoAssigneeIcon, PriorityIcon, TeamIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import type { Issue, IssueLabel, Project, ProjectMilestone, ProjectUpdate, Team, User } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { ProjectDetailTab, ProjectDetailProps } from './project-detail-types'
import { PRIORITY_LABELS } from './project-detail-types'

export function ProjectDetailsSidebar({ labels, onCreateMilestone, onDeleteMilestone, onTabChange, onUpdate, onUpdateMilestone, project, projectIssues, projects, projectUpdates, teams, users, viewer }: {
  labels: IssueLabel[]
  onCreateMilestone: ProjectDetailProps['onCreateMilestone']
  onDeleteMilestone: ProjectDetailProps['onDeleteMilestone']
  onTabChange: (tab: ProjectDetailTab) => void
  onUpdate: (input: ProjectMutationInput) => Promise<void>
  onUpdateMilestone: ProjectDetailProps['onUpdateMilestone']
  project: Project
  projectIssues: Issue[]
  projects: Project[]
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
  const [progressTab, setProgressTab] = useState<'assignees'|'labels'>('assignees')
  const dependencies = projects.filter(item => (project.dependencyIds ?? []).includes(item.id))
  const started = projectIssues.filter(issue => issue.state.type === 'started').length
  const completed = projectIssues.filter(issue => issue.state.type === 'completed').length
  const issueLabels = labels.filter(label => projectIssues.some(issue => issue.labels.some(item => item.id === label.id)))
  const assignees = users.filter(user => projectIssues.some(issue => issue.assignee?.id === user.id))
  const events = useMemo(() => projectEvents(project, projectUpdates, viewer), [project, projectUpdates, viewer])

  return <aside aria-label="Project sidebar" className="project-details-sidebar">
    <SidebarSection compact onToggle={() => setPropertiesOpen(value => !value)} open={propertiesOpen} title="Properties" action={<DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Add dependency" type="button"><Plus size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Label>Add dependency</DropdownMenu.Label>{projects.filter(item => item.id !== project.id).map(item => <DropdownMenu.Item key={item.id} onSelect={() => void onUpdate({ dependencyIds: (project.dependencyIds ?? []).includes(item.id) ? project.dependencyIds.filter(id => id !== item.id) : [...(project.dependencyIds ?? []), item.id] })}><span className="project-details-sidebar__project-dot" style={{ background: item.color }}/><span>{item.name}</span>{(project.dependencyIds ?? []).includes(item.id) && <Check size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}>
      <SidebarPropertiesBoundary><SidebarProperties labels={labels} onUpdate={onUpdate} project={project} projects={projects} teams={teams} users={users}/></SidebarPropertiesBoundary>
      {dependencies.length > 0 && <div className="project-details-sidebar__dependencies">{dependencies.map(item => <div key={item.id}><span className="project-details-sidebar__project-dot" style={{ background: item.color }}/><span>{item.name}</span><button aria-label={`Remove ${item.name}`} onClick={() => void onUpdate({ dependencyIds: project.dependencyIds.filter(id => id !== item.id) })} type="button"><Trash2 size={11}/></button></div>)}</div>}
    </SidebarSection>

    <SidebarSection onToggle={() => setMilestonesOpen(value => !value)} open={milestonesOpen} title="Milestones" action={<button aria-label="Add milestone" data-project-milestone-add onClick={() => { setMilestonesOpen(true); setMilestoneEditor('new') }} type="button"><Plus size={13}/></button>}>
      <div className="project-details-sidebar__milestones">
        {(project.milestones ?? []).map(milestone => <div className="project-details-sidebar__milestone" key={milestone.id}><span className="project-details-sidebar__milestone-mark"/><div><strong>{milestone.name}</strong><span>{milestone.targetDate ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : 'No target date'}</span></div><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${milestone.name} actions`} type="button"><MoreHorizontal size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Item onSelect={() => setMilestoneEditor(milestone)}><CalendarDays size={14}/><span>Edit milestone</span></DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="is-danger" onSelect={() => void onDeleteMilestone(project.id, milestone.id)}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>)}
        {milestoneEditor && <MilestoneEditor milestone={milestoneEditor === 'new' ? undefined : milestoneEditor} onCancel={() => setMilestoneEditor(undefined)} onSubmit={async input => { if (milestoneEditor === 'new') await onCreateMilestone(project.id, input as { name: string; targetDate?: string }); else await onUpdateMilestone(project.id, milestoneEditor.id, input); setMilestoneEditor(undefined) }}/>} 
        {!project.milestones?.length && !milestoneEditor && <div className="project-details-sidebar__milestone-empty"><p>Add milestones to organize work within your project and break it into more granular stages.</p><a href="https://flow.app/docs/project-milestones" rel="noreferrer" target="_blank">Learn more</a></div>}
      </div>
    </SidebarSection>

    <SidebarSection onToggle={() => setProgressOpen(value => !value)} open={progressOpen} title="Progress">
      <div className="project-details-sidebar__progress">
        <div className="project-details-sidebar__stats"><span><i className="is-scope"/>Scope<strong>{projectIssues.length}</strong></span><span><i className="is-started"/>Started<strong>{started}</strong></span><span><i className="is-completed"/>Completed<strong>{completed}</strong></span></div>
        <ProgressChart progress={project.progress} start={project.startDate} target={project.targetDate}/>
        <div aria-label="Progress grouping" className="project-details-sidebar__segments" role="tablist"><button aria-selected={progressTab === 'assignees'} onClick={() => setProgressTab('assignees')} role="tab" type="button">Assignees</button><button aria-selected={progressTab === 'labels'} onClick={() => setProgressTab('labels')} role="tab" type="button">Labels</button></div>
        <div className="project-details-sidebar__breakdown">{progressTab === 'assignees' ? assignees.map(user => { const count = projectIssues.filter(issue => issue.assignee?.id === user.id).length; const done = projectIssues.filter(issue => issue.assignee?.id === user.id && issue.state.type === 'completed').length; return <button key={user.id} type="button"><Avatar name={user.displayName}/><span>{user.displayName}</span><em>{count ? Math.round(done / count * 100) : 0}%</em><small>of {count}</small></button> }) : issueLabels.map(label => { const count = projectIssues.filter(issue => issue.labels.some(item => item.id === label.id)).length; return <button key={label.id} type="button"><i style={{ background: label.color }}/><span>{label.name}</span><small>{count}</small></button> })}{progressTab === 'assignees' && !assignees.length && <p>No assigned issues</p>}{progressTab === 'labels' && !issueLabels.length && <p>No labels in scope</p>}</div>
      </div>
    </SidebarSection>

    <SidebarSection onToggle={() => setActivityOpen(value => !value)} open={activityOpen} title="Activity" action={<button className="project-details-sidebar__see-all" onClick={() => onTabChange('activity')} type="button">See all</button>}>
      <div className="project-details-sidebar__activity">{events.slice(0, 6).map(event => <div key={event.id}><Avatar name={event.actor}/><p><strong>{event.actor}</strong> {event.text} <time>· {event.date}</time></p></div>)}</div>
    </SidebarSection>
  </aside>
}

function SidebarSection({ action, children, compact, onToggle, open, title }: { action?: ReactNode; children: ReactNode; compact?: boolean; onToggle: () => void; open: boolean; title: string }) {
  return <section className={`project-details-sidebar__section ${compact ? 'is-compact' : ''}`}><header><button aria-expanded={open} aria-label={`${open ? 'Collapse' : 'Expand'} ${title.toLowerCase()} section`} onClick={onToggle} type="button"><span>{title}</span>{open ? <ChevronDown size={11}/> : <ChevronRight size={11}/>}</button>{action}</header>{open && children}</section>
}

function SidebarProperties({ labels, onUpdate, project, projects, teams, users }: { labels: IssueLabel[]; onUpdate: (input: ProjectMutationInput) => Promise<void>; project: Project; projects: Project[]; teams: Team[]; users: User[] }) {
  const statuses = uniqueById(projects.map(item => item.status))
  const memberIds = project.memberIds ?? []
  const teamIds = project.teamIds ?? []
  const labelIds = project.labelIds ?? []
  const projectInitiatives = project.initiatives ?? []
  const members = users.filter(user => memberIds.includes(user.id))
  const selectedTeams = teams.filter(team => teamIds.includes(team.id))
  const selectedLabels = labels.filter(label => labelIds.includes(label.id))
  const initiatives = [...new Set(projects.flatMap(item => item.initiatives ?? []))]
  return <div className="project-details-sidebar__properties">
    <PropertyMenu label="Status" value={project.status.name} selectedId={project.status.id} icon={<StatusDot color={project.status.color}/>} options={statuses.map(status => ({ id: status.id, label: status.name, icon: <StatusDot color={status.color}/> }))} onChange={statusId => void onUpdate({ statusId })}/>
    <PropertyMenu label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} icon={<PriorityIcon priority={project.priority} size={14}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={14}/>, shortcut: String(priority) }))} onChange={priority => void onUpdate({ priority: Number(priority) })}/>
    <PropertyMenu label="Lead" value={project.lead?.displayName ?? 'Lead'} selectedId={project.lead?.id ?? ''} icon={project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No lead', icon: <NoAssigneeIcon size={14}/> }, ...users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))]} onChange={leadId => void onUpdate({ leadId })}/>
    <PropertyMenu multiple label="Members" value={members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Add member'} selectedIds={memberIds} icon={members[0] ? <Avatar name={members[0].displayName}/> : <Users size={14}/>} options={users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))} onChange={memberId => void onUpdate({ memberIds: toggleId(memberIds, memberId) })}/>
    <div className="project-details-sidebar__property-dates"><span>Dates</span><div><ProjectDatePicker label="Start date" onChange={startDate => void onUpdate({ startDate })} value={project.startDate}><CalendarDays size={14}/><span>{formatProjectDate(project.startDate, 'Start')}</span></ProjectDatePicker><span>→</span><ProjectDatePicker label="Target date" onChange={targetDate => void onUpdate({ targetDate })} value={project.targetDate}><CalendarDays size={14}/><span>{formatProjectDate(project.targetDate, 'Target')}</span></ProjectDatePicker></div></div>
    <PropertyMenu multiple label="Teams" value={selectedTeams.map(team => team.name).join(', ') || 'Add team'} selectedIds={teamIds} icon={<TeamIcon size={14}/>} options={teams.map(team => ({ id: team.id, label: team.name, color: team.color, icon: <TeamIcon size={14}/> }))} onChange={teamId => void onUpdate({ teamIds: toggleId(teamIds, teamId) })}/>
    <button className="project-details-sidebar__property-row" onClick={() => toast.info('Slack integration is not connected in this workspace.')} type="button"><Slack size={14}/><span>Slack</span><strong>Slack channel</strong></button>
    <PropertyMenu multiple label="Initiatives" value={projectInitiatives.join(', ') || 'Add initiative…'} selectedIds={projectInitiatives} icon={<Flag size={14}/>} options={initiatives.map(name => ({ id: name, label: name, icon: <Flag size={14}/> }))} onChange={name => void onUpdate({ initiatives: toggleId(projectInitiatives, name) })}/>
    <PropertyMenu multiple label="Labels" value={selectedLabels.map(label => label.name).join(', ') || 'Add label'} selectedIds={labelIds} icon={<Tags size={14}/>} options={labels.map(label => ({ id: label.id, label: label.name, color: label.color }))} onChange={labelId => void onUpdate({ labelIds: toggleId(labelIds, labelId) })}/>
  </div>
}

class SidebarPropertiesBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('Project properties failed to render', error, info) }
  render() { return this.state.error ? <p className="project-details-sidebar__property-error">{this.state.error.message}</p> : this.props.children }
}

function MilestoneEditor({ milestone, onCancel, onSubmit }: { milestone?: ProjectMilestone; onCancel: () => void; onSubmit: (input: { name?: string; targetDate?: string }) => Promise<void> }) {
  const [name, setName] = useState(milestone?.name ?? '')
  const [date, setDate] = useState(milestone?.targetDate ?? '')
  const [saving, setSaving] = useState(false)
  return <div className="project-details-sidebar__milestone-editor"><span className="project-details-sidebar__milestone-diamond">◇</span><input autoFocus aria-label="Milestone name" onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && name.trim() && !saving) { setSaving(true); void onSubmit({ name: name.trim(), targetDate: date }).finally(() => setSaving(false)) } }} placeholder="Milestone name" value={name}/><ProjectDatePicker buttonClassName="project-details-sidebar__milestone-date" label="Target date" onChange={setDate} value={date}><CalendarDays size={13}/></ProjectDatePicker><button aria-label="Cancel milestone" onClick={onCancel} type="button"><X size={13}/></button><button aria-label={milestone ? 'Save milestone' : 'Create milestone'} className="is-primary" disabled={!name.trim() || saving} onClick={() => { setSaving(true); void onSubmit({ name: name.trim(), targetDate: date }).finally(() => setSaving(false)) }} type="button"><Check size={13}/></button></div>
}

function ProgressChart({ progress, start, target }: { progress: number; start?: string; target?: string }) {
  const x = 8 + Math.max(0, Math.min(1, progress)) * 304
  return <div className="project-details-sidebar__chart"><svg aria-label={`${Math.round(progress * 100)}% project progress`} preserveAspectRatio="none" viewBox="0 0 320 118"><path className="baseline" d="M8 104H312"/><path className="trend" d={`M8 104 L20 28 L${Math.max(20, x)} 28 L${x} ${104 - progress * 76}`}/><circle cx={x} cy={104 - progress * 76} r="3"/></svg><div><span>{start ? format(new Date(`${start}T00:00:00`), 'MMM d') : 'Start'}</span><span>{target ? format(new Date(`${target}T00:00:00`), 'MMM d') : 'Target'}</span></div></div>
}

function projectEvents(project: Project, updates: ProjectUpdate[], viewer: User) {
  const events = updates.map(update => ({ id: update.id, actor: update.user.displayName, text: `posted an ${update.health === 'noUpdate' ? '' : update.health.replace(/[A-Z]/g, match => ` ${match.toLowerCase()}`)} update`, date: formatDistanceToNowStrict(new Date(update.createdAt), { addSuffix: true }) }))
  if (project.priority > 0) events.push({ id: 'priority', actor: project.lead?.displayName ?? viewer.displayName, text: `changed priority from No priority to ${project.priorityLabel}`, date: format(new Date(project.updatedAt), 'MMM d') })
  if (project.lead) events.push({ id: 'lead', actor: project.lead.displayName, text: 'assigned themselves as a lead', date: format(new Date(project.updatedAt), 'MMM d') })
  events.push({ id: 'created', actor: project.lead?.displayName ?? viewer.displayName, text: 'created the project', date: format(new Date(project.createdAt), 'MMM d') })
  return events
}

function StatusDot({ color }: { color: string }) { return <span className="project-details-sidebar__status-dot" style={{ borderColor: color }}/> }
function formatProjectDate(value: string | undefined, fallback: string) { return value ? format(new Date(`${value}T00:00:00`), 'MMM do') : fallback }
function toggleId(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
function uniqueById<T extends { id: string }>(values: T[]) { return [...new Map(values.map(value => [value.id, value])).values()] }
