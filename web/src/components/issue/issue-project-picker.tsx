import { useEffect, useRef, useState } from 'react'

import { ProjectIcon, NoProjectIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import { PropertyShortcutTooltip } from '@/components/property/issue-property-hover'
import { NewProjectDialog, type NewProjectDraft } from '@/components/projects-page/new-project-dialog'
import { projectPeopleChoices } from '@/components/projects-page/project-people'
import { ProjectStatusGlyph } from '@/components/projects-page/project-property-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { labelsForResource } from '@/lib/labels'
import { createProject as createProjectRequest, createProjectMilestone as createProjectMilestoneRequest } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Issue, IssueUpdateInput, Presence, Project, ProjectMilestone } from '@/types/flow'
import type { ProjectCreateInput } from '@/components/projects-page/projects-page'

import './issue-project-picker.css'

export function IssueProjectPicker({ data, issue, grouped = false, presence = [], onUpdate, onCreateProject, onCreateMilestone }: {
  data: BootstrapData
  issue: Issue
  grouped?: boolean
  presence?: Presence[]
  onUpdate: (input: IssueUpdateInput) => Promise<void>
  onCreateProject?: (draft: NewProjectDraft) => Promise<Project>
  onCreateMilestone?: (projectId: string, input: { name: string }) => Promise<ProjectMilestone>
}) {
  const { t, formatDate } = useI18n()
  const onlineUserIds = new Set([data.viewer.id, ...presence.map(item => item.user.id)])
  const peopleChoices = projectPeopleChoices(data.users, data.invitations, onlineUserIds)
  const rootRef = useRef<HTMLDivElement>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [createdProjects, setCreatedProjects] = useState<Project[]>([])
  const [createdMilestones, setCreatedMilestones] = useState<Record<string, ProjectMilestone[]>>({})
  const allProjects = [...createdProjects, ...data.projects.filter(item => !createdProjects.some(created => created.id === item.id))]
  const project = allProjects.find(item => item.id === issue.project?.id)
  const projects = allProjects.filter(item => !item.archivedAt && (!item.teamIds.length || item.teamIds.includes(issue.team.id)))
  const milestones = [...(project?.milestones ?? []), ...(project ? createdMilestones[project.id] ?? [] : [])].filter((item, index, values) => values.findIndex(value => value.id === item.id) === index)
  const milestone = milestones.find(item => item.id === issue.projectMilestoneId)
  const projectLabels = labelsForResource(data.labels, 'project', data.labelGroups)
  const labelGroupNames = new Map(data.labelGroups.map(group => [group.id, group.name]))
  const projectOptions = [
    { id: '', label: t('No project'), shortcut: '0', icon: <NoProjectIcon size={16}/> },
    ...projects.map(item => ({ id: item.id, label: item.name, icon: <ProjectIcon size={16} style={{ color: item.color }}/>, i18nIgnore: true })),
  ]
  const milestoneOptions = [
    { id: '', label: t('No milestone'), shortcut: '0', icon: <MilestoneIcon unassigned/> },
    ...milestones.map(item => ({ id: item.id, label: item.name, icon: <MilestoneIcon progress={milestoneProgress(data.issues, project?.id, item.id)}/>, end: item.targetDate ? formatDate(item.targetDate, { month: 'short', day: 'numeric' }) : undefined, i18nIgnore: true })),
  ]
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!event.shiftKey || event.metaKey || event.ctrlKey || event.altKey || isEditable(event.target)) return
      const key = event.key.toLowerCase()
      if (key !== 'p' && key !== 'm') return
      const selector = key === 'p' ? '.issue-project-trigger' : '.issue-milestone-trigger'
      if (!rootRef.current?.getClientRects().length) return
      const trigger = rootRef.current?.querySelector<HTMLButtonElement>(selector)
      if (!trigger) return
      event.preventDefault()
      trigger.click()
    }
    addEventListener('keydown', onKeyDown)
    return () => removeEventListener('keydown', onKeyDown)
  }, [])
  const projectMenu = <PropertyMenu
    label="Project"
    value={project?.name ?? t('Add to project')}
    valueIsEntityName={Boolean(project)}
    selectedId={project?.id ?? ''}
    options={projectOptions}
    kind="project"
    teamName={issue.team.name}
    searchPlaceholder={project ? t('Move to project…') : t('Add to project…')}
    searchShortcut="Shift P"
    ariaLabel={project ? `${t('Change project')}. ${project.name}` : t('Add to project')}
    triggerClassName="label-project-trigger issue-project-trigger"
    trigger={<><ProjectIcon size={16} style={{ color: project?.color }}/><span data-i18n-ignore={project ? true : undefined}>{project?.name ?? t('Add to project')}</span></>}
    hoverContent={<PropertyShortcutTooltip label={project ? t('Change project') : t('Add to project')} shortcut="⇧ P"/>}
    onChange={projectId => onUpdate({ projectId, projectMilestoneId: '' })}
    onCreate={() => setCreateOpen(true)}
  />
  const milestoneMenu = project ? <PropertyMenu
    label="Milestone"
    value={milestone?.name ?? t('Set milestone')}
    valueIsEntityName={Boolean(milestone)}
    selectedId={milestone?.id ?? ''}
    options={milestoneOptions}
    kind="milestone"
    searchPlaceholder={milestone ? t('Move to milestone…') : t('Add to milestone…')}
    searchShortcut="Shift M"
    ariaLabel={milestone ? `${t('Change milestone')}. ${milestone.name}` : t('Set milestone')}
    triggerClassName="label-project-trigger issue-milestone-trigger"
    trigger={<><MilestoneIcon progress={milestone ? milestoneProgress(data.issues, project.id, milestone.id) : undefined} unassigned={!milestone}/><span data-i18n-ignore={milestone ? true : undefined}>{milestone?.name ?? t('Set milestone')}</span></>}
    onChange={projectMilestoneId => onUpdate({ projectMilestoneId })}
    onCreate={async name => { const created = onCreateMilestone ? await onCreateMilestone(project.id, { name }) : await createProjectMilestoneRequest(project.id, { name }); setCreatedMilestones(current => ({ ...current, [project.id]: [...(current[project.id] ?? []), created] })); await onUpdate({ projectMilestoneId: created.id }) }}
  /> : null
  const body = <><div className="issue-project-row">{projectMenu}{project && <a className="issue-project-open" href={`/${data.workspace.urlKey}/project/${project.slugId}/overview`} aria-label={t('Open project')}><OpenChevron/></a>}</div>{milestoneMenu&&<div className="issue-milestone-row">{milestoneMenu}</div>}</>

  return <>
    {grouped ? <div ref={rootRef}><section className="property-group issue-project-group"><h4>{t('Project')}</h4>{body}</section></div> : <div ref={rootRef} className="issue-project-property">{body}</div>}
    <NewProjectDialog
      dependencies={data.projects.filter(item => !item.archivedAt && item.id !== issue.project?.id).map(item => ({ id: item.id, label: item.name, icon: normalizeProjectIcon(item.icon), color: item.color, group: data.viewer?.id && (item.lead?.id === data.viewer.id || (item.memberIds ?? []).includes(data.viewer.id)) ? 'your' : 'other', previewData: { summary: item.summary || item.description, status: item.status.name, milestone: (item.milestones ?? [])[0]?.name, team: (item.teamIds ?? []).map(id => data.teams.find(team => team.id === id)?.name).filter(Boolean).join(', '), lead: item.lead?.displayName, member: (item.memberIds ?? []).map(id => data.users.find(user => user.id === id)?.displayName).find(Boolean), memberAvatarUrl: (item.memberIds ?? []).map(id => data.users.find(user => user.id === id)?.avatarUrl).find(Boolean), priority: item.priorityLabel, targetDate: item.targetDate, progress: Math.round(item.progress * 100), issueCount: item.issueCount } }))}
      initiatives={data.initiatives.map(item => ({ id: item.id, label: item.name, color: item.color }))}
      labels={projectLabels.map(label => ({ id: label.id, label: label.name, color: label.color, groupId: label.groupId, groupLabel: label.groupId ? labelGroupNames.get(label.groupId) : undefined }))}
      leads={peopleChoices}
      members={peopleChoices}
      statuses={data.projectStatuses.map(status => ({ id: status.name, label: status.name, color: status.color, icon: <ProjectStatusGlyph color={status.color} name={status.name} type={status.type}/> }))}
      templates={data.projectTemplates.map(template => ({ id: template.id, label: template.name, name: template.name, description: template.description, summary: template.summary, icon: template.icon, color: template.color, status: data.projectStatuses.find(status => status.id === template.statusId)?.name, priority: ['No priority', 'Urgent', 'High', 'Medium', 'Low'][template.priority] ?? 'No priority', teamIds: template.teamIds, initiativeIds: template.initiativeIds, labelIds: template.labelIds.filter(id => projectLabels.some(label => label.id === id)), dependencyIds: template.dependencyIds }))}
      onClose={() => setCreateOpen(false)}
      onCreate={async draft => { const created = onCreateProject ? await onCreateProject(draft) : await createDirectProject(draft, data); setCreatedProjects(current => [created, ...current.filter(item => item.id !== created.id)]); await onUpdate({ projectId: created.id, projectMilestoneId: '' }); setCreateOpen(false) }}
      open={createOpen}
      agentSkills={data.agentSkills}
      teamLabel={issue.team.name}
      teams={data.teams.filter(team => !team.retiredAt).map(team => ({ id: team.id, label: team.name, color: team.color }))}
      workspaceName={issue.team.name}
    />
  </>
}

function milestoneProgress(issues: Issue[], projectId: string | undefined, milestoneId: string) {
  const scoped = issues.filter(issue => issue.project?.id === projectId && issue.projectMilestoneId === milestoneId && !issue.archivedAt)
  const completed = scoped.filter(issue => issue.state.type === 'completed' || issue.state.type === 'canceled').length
  return scoped.length ? Math.round(completed / scoped.length * 100) : 0
}

function MilestoneIcon({ progress = 0, unassigned = false }: { progress?: number; unassigned?: boolean }) {
  const path = 'M7.3406 2.32C7.68741 1.89333 8.31259 1.89333 8.6594 2.32L12.7903 7.402C13.0699 7.74597 13.0699 8.25403 12.7903 8.598L8.6594 13.68C8.31259 14.1067 7.68741 14.1067 7.3406 13.68L3.2097 8.598C2.9301 8.25403 2.9301 7.74597 3.2097 7.402L7.3406 2.32Z'
  if (unassigned) return <svg aria-hidden="true" className="issue-milestone-icon is-unassigned" viewBox="0 0 16 16"><path d={path}/></svg>
  if (progress >= 100) return <svg aria-hidden="true" className="issue-milestone-icon is-complete" viewBox="0 0 16 16"><path d={path}/></svg>
  const length = Math.max(2.5, Math.min(31, 31 * progress / 100))
  return <svg aria-hidden="true" className="issue-milestone-icon is-progress" viewBox="0 0 16 16"><path className="is-track" d={path}/><path className="is-value" d={path} strokeDasharray={`${length} ${31 - length}`}/></svg>
}

function OpenChevron() { return <svg aria-hidden="true" viewBox="0 0 9 5"><path d="M1.915.557a.667.667 0 0 0-.943.943l2.862 2.862a.942.942 0 0 0 1.333 0L8.028 1.5a.667.667 0 0 0-.943-.943L4.5 3.14 1.915.557Z" fill="currentColor"/></svg> }

async function createDirectProject(draft: NewProjectDraft, data: BootstrapData) {
  const input: ProjectCreateInput = {
    templateId: draft.templateId,
    color: draft.color,
    description: draft.description,
    icon: draft.icon,
    leadId: draft.leadId,
    memberIds: draft.memberIds,
    milestones: draft.milestones,
    milestoneDetails: draft.milestoneDetails,
    labelIds: draft.labelIds,
    dependencyIds: draft.dependencyIds,
    dependencyRelations: draft.dependencyRelations,
    initiatives: draft.initiativeIds,
    name: draft.name,
    priority: Math.max(0, ['No priority', 'Urgent', 'High', 'Medium', 'Low'].indexOf(draft.priority)),
    startDate: draft.startDate,
    startDateResolution: draft.startDateResolution,
    statusId: data.projectStatuses.find(status => status.name === draft.status)?.id ?? data.projects.find(project => project.status.name === draft.status)?.status.id,
    summary: draft.summary,
    targetDate: draft.targetDate,
    targetDateResolution: draft.targetDateResolution,
    teamIds: draft.teamIds,
  }
  return createProjectRequest(input)
}

function isEditable(target: EventTarget | null) { return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLElement && target.isContentEditable }
