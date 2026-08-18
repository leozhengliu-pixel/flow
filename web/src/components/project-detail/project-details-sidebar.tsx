import { Component, useMemo, useState, type ErrorInfo, type KeyboardEvent, type ReactNode } from 'react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Line, type LineCustomSvgLayerProps, type SliceTooltipProps } from '@nivo/line'
import { CalendarDays, Check, ChevronDown, ChevronRight, MessageSquare as Slack, MoreHorizontal, Plus, Tags, Trash2, Users, X } from 'lucide-react'
import { addDays, format, formatDistanceToNowStrict, startOfDay } from 'date-fns'
import { toast } from 'sonner'
import { Avatar } from '@/components/issue/issue-row'
import { NoAssigneeIcon, PriorityIcon, TeamIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import type { Issue, IssueLabel, Project, ProjectMilestone, ProjectUpdate, Team, User } from '@/types/flow'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { ProjectDetailTab, ProjectDetailProps } from './project-detail-types'
import { PRIORITY_LABELS } from './project-detail-types'

export function ProjectDetailsSidebar({ labels, onCreateMilestone, onDeleteMilestone, onOpenIssueFilter, onTabChange, onUpdate, onUpdateMilestone, project, projectIssues, projects, projectUpdates, teams, users, viewer }: {
  labels: IssueLabel[]
  onCreateMilestone: ProjectDetailProps['onCreateMilestone']
  onDeleteMilestone: ProjectDetailProps['onDeleteMilestone']
  onOpenIssueFilter: (field: 'assignee'|'labels', value: string, valueLabel: string) => void
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
  const issueLabels = labels.filter(label => projectIssues.some(issue => issue.labels.some(item => item.id === label.id))).sort((left, right) => left.name.localeCompare(right.name))
  const assignees = users.filter(user => projectIssues.some(issue => issue.assignee?.id === user.id)).sort((left, right) => left.displayName.localeCompare(right.displayName))
  const milestoneBuckets = (project.milestones?.length ?? 0) + 1
  const events = useMemo(() => projectEvents(project, projectUpdates, viewer), [project, projectUpdates, viewer])

  return <aside aria-label="Project sidebar" className="project-details-sidebar">
    <SidebarSection compact onToggle={() => setPropertiesOpen(value => !value)} open={propertiesOpen} title="Properties" action={<DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Add dependency" type="button"><Plus size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Label>Add dependency</DropdownMenu.Label>{projects.filter(item => item.id !== project.id).map(item => <DropdownMenu.Item key={item.id} onSelect={() => void onUpdate({ dependencyIds: (project.dependencyIds ?? []).includes(item.id) ? project.dependencyIds.filter(id => id !== item.id) : [...(project.dependencyIds ?? []), item.id] })}><span className="project-details-sidebar__project-dot" style={{ background: item.color }}/><span>{item.name}</span>{(project.dependencyIds ?? []).includes(item.id) && <Check size={13}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}>
      <SidebarPropertiesBoundary><SidebarProperties labels={labels} onUpdate={onUpdate} project={project} projects={projects} teams={teams} users={users}/></SidebarPropertiesBoundary>
      {dependencies.length > 0 && <div className="project-details-sidebar__dependencies">{dependencies.map(item => <div key={item.id}><span className="project-details-sidebar__project-dot" style={{ background: item.color }}/><span>{item.name}</span><button aria-label={`Remove ${item.name}`} onClick={() => void onUpdate({ dependencyIds: project.dependencyIds.filter(id => id !== item.id) })} type="button"><Trash2 size={11}/></button></div>)}</div>}
    </SidebarSection>

    <SidebarSection onToggle={() => setMilestonesOpen(value => !value)} open={milestonesOpen} title="Milestones" action={<button aria-label="Add milestone" data-project-milestone-add onClick={() => { setMilestonesOpen(true); setMilestoneEditor('new') }} type="button"><Plus size={13}/></button>}>
      <div className="project-details-sidebar__milestones">
        {(project.milestones ?? []).map((milestone, index) => { const stats = milestoneStats(projectIssues, index, milestoneBuckets); return <div className="project-details-sidebar__milestone" key={milestone.id}><span className="project-details-sidebar__milestone-mark"/><div><strong>{milestone.name}</strong><span>{stats.progress}%</span><span>{stats.count}</span></div><time>{milestone.targetDate ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : 'No date'}</time><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${milestone.name} actions`} type="button"><MoreHorizontal size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Item onSelect={() => setMilestoneEditor(milestone)}><CalendarDays size={14}/><span>Edit milestone</span></DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="is-danger" onSelect={() => void onDeleteMilestone(project.id, milestone.id)}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div> })}
        {(project.milestones?.length ?? 0) > 0 && (() => { const stats = milestoneStats(projectIssues, milestoneBuckets - 1, milestoneBuckets); return <div className="project-details-sidebar__milestone is-unassigned"><span className="project-details-sidebar__milestone-mark"/><div><strong>No milestone</strong><span>{stats.progress}%</span><span>{stats.count}</span></div></div> })()}
        {milestoneEditor && <MilestoneEditor milestone={milestoneEditor === 'new' ? undefined : milestoneEditor} onCancel={() => setMilestoneEditor(undefined)} onSubmit={async input => { if (milestoneEditor === 'new') await onCreateMilestone(project.id, input as { name: string; targetDate?: string }); else await onUpdateMilestone(project.id, milestoneEditor.id, input); setMilestoneEditor(undefined) }}/>} 
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
  const members = users.filter(user => memberIds.includes(user.id))
  const selectedTeams = teams.filter(team => teamIds.includes(team.id))
  const selectedLabels = labels.filter(label => labelIds.includes(label.id))
  return <div className="project-details-sidebar__properties">
    <PropertyMenu label="Status" value={project.status.name} selectedId={project.status.id} icon={<StatusDot color={project.status.color}/>} options={statuses.map(status => ({ id: status.id, label: status.name, icon: <StatusDot color={status.color}/> }))} onChange={statusId => void onUpdate({ statusId })}/>
    <PropertyMenu label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} icon={<PriorityIcon priority={project.priority} size={14}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={14}/>, shortcut: String(priority) }))} onChange={priority => void onUpdate({ priority: Number(priority) })}/>
    <PropertyMenu label="Lead" value={project.lead?.displayName ?? 'Lead'} selectedId={project.lead?.id ?? ''} icon={project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No lead', icon: <NoAssigneeIcon size={14}/> }, ...users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))]} onChange={leadId => void onUpdate({ leadId })}/>
    <PropertyMenu multiple label="Members" value={members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Add member'} selectedIds={memberIds} icon={members[0] ? <Avatar name={members[0].displayName}/> : <Users size={14}/>} options={users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))} onChange={memberId => void onUpdate({ memberIds: toggleId(memberIds, memberId) })}/>
    <div className="project-details-sidebar__property-dates"><span>Dates</span><div><ProjectDatePicker label="Start date" onChange={startDate => void onUpdate({ startDate })} value={project.startDate}><CalendarDays size={14}/><span>{formatProjectDate(project.startDate, 'Start')}</span></ProjectDatePicker><span>→</span><ProjectDatePicker label="Target date" onChange={targetDate => void onUpdate({ targetDate })} value={project.targetDate}><CalendarDays size={14}/><span>{formatProjectDate(project.targetDate, 'Target')}</span></ProjectDatePicker></div></div>
    <PropertyMenu multiple label="Teams" value={selectedTeams.map(team => team.name).join(', ') || 'Add team'} selectedIds={teamIds} icon={<TeamIcon size={14}/>} options={teams.map(team => ({ id: team.id, label: team.name, color: team.color, icon: <TeamIcon size={14}/> }))} onChange={teamId => void onUpdate({ teamIds: toggleId(teamIds, teamId) })}/>
    <button className="project-details-sidebar__property-row" onClick={() => toast.info('Slack integration is not connected in this workspace.')} type="button"><Slack size={14}/><span>Slack</span><strong>Slack channel</strong></button>
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
      axisBottom={{ tickPadding: 3, tickSize: 6, tickValues: [chart.startDate, chart.endDate], format: value => format(value as Date, 'MMM d') }}
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

function ProgressCompletionBars({ changes, innerHeight, xScale }: LineCustomSvgLayerProps<ProgressSeries> & { changes: { x: Date; y: number }[] }) {
  const maxChange = Math.max(1, ...changes.map(item => item.y))
  return <g>{changes.map(item => {
    if (!item.y) return null
    const height = Math.max(3, innerHeight * .3 * item.y / maxChange)
    return <rect className="project-details-sidebar__completed-bar" height={height} key={item.x.toISOString()} rx="1" width="4" x={xScale(item.x) - 2} y={innerHeight - height}/>
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
  const completedChanges = completedData.map((item, index) => ({ x: item.x, y: item.y - (completedData[index - 1]?.y ?? 0) }))
  return { startDate, targetDate, endDate, currentDate, completedChanges, series: [{ id: 'Scope' as const, data: scope }, { id: 'Started' as const, data: startedData }, { id: 'Completed' as const, data: completedData }] }
}

function uniqueDates(values: Date[]) { return [...new Map(values.filter(date => !Number.isNaN(date.getTime())).map(date => [date.getTime(), date])).values()].sort((left, right) => left.getTime() - right.getTime()) }

function ProgressBreakdownRow({ icon, issues, label, onOpen }: { icon: ReactNode; issues: Issue[]; label: string; onOpen: () => void }) {
  const completed = issues.filter(issue => issue.state.type === 'completed').length
  const engaged = issues.filter(issue => ['started','completed'].includes(issue.state.type)).length
  const percent = issues.length ? Math.round(completed / issues.length * 100) : 0
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onOpen() } }
  return <div aria-label={`${label} ${percent}% of ${issues.length}`} className="project-details-sidebar__breakdown-row" onClick={onOpen} onKeyDown={onKeyDown} role="button" tabIndex={0}>
    <div className="project-details-sidebar__breakdown-name">{icon}<span>{label}</span><button onClick={event => { event.stopPropagation(); onOpen() }} tabIndex={-1} type="button">See issues</button></div>
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

function StatusDot({ color }: { color: string }) { return <span className="project-details-sidebar__status-dot" style={{ borderColor: color }}/> }
function formatProjectDate(value: string | undefined, fallback: string) { return value ? format(new Date(`${value}T00:00:00`), 'MMM do') : fallback }
function milestoneStats(issues: Issue[], bucket: number, bucketCount: number) { const items = issues.filter((_, index) => index % bucketCount === bucket); const completed = items.filter(issue => issue.state.type === 'completed').length; return { count: items.length, progress: items.length ? Math.round(completed / items.length * 100) : 0 } }
function toggleId(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
function uniqueById<T extends { id: string }>(values: T[]) { return [...new Map(values.map(value => [value.id, value])).values()] }
