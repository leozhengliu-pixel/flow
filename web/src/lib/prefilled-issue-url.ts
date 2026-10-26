import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import { newIssuePath } from '@/lib/app-routes'
import { estimateValues } from '@/lib/estimates'
import { issueTemplatesForTeam } from '@/lib/issue-templates'
import { labelsForIssueTeam, labelTeamScopeIds } from '@/lib/labels'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import { teamWorkflowStates } from '@/lib/team-workflow'
import type { BootstrapData, Issue, IssueTemplate, Team, User } from '@/types/flow'

/**
 * Prefilled "new issue" links: `/:workspace/new?…` and `/:workspace/team/:KEY/new?…`.
 *
 * Every parameter is optional and unknown or unresolvable values are ignored
 * silently, so a link can never produce an error: the composer simply opens with
 * whatever could be understood.
 */
export interface PrefilledIssueParams {
  title?: string
  description?: string
  team?: string
  status?: string
  priority?: string
  assignee?: string
  estimate?: string
  cycle?: string
  labels: string[]
  project?: string
  projectMilestone?: string
  template?: string
  parent?: string
  dueDate?: string
}

type PrefillData = Pick<
  BootstrapData,
  'viewer' | 'users' | 'teams' | 'states' | 'labels' | 'labelGroups' | 'cycles' | 'projects' | 'issues' | 'issueTemplates' | 'teamSettings' | 'workspace'
>

/** Flow priority numbering: 0 none, 1 urgent, 2 high, 3 medium, 4 low. */
const PRIORITY_WORDS: Record<string, 0 | 1 | 2 | 3 | 4> = {
  none: 0,
  nopriority: 0,
  'no priority': 0,
  urgent: 1,
  high: 2,
  medium: 3,
  normal: 3,
  low: 4,
}
const PRIORITY_NAMES = ['none', 'urgent', 'high', 'medium', 'low'] as const

const norm = (value: string | undefined) => (value ?? '').trim().toLowerCase()
const present = (value: string | null | undefined) => (value && value.trim() ? value : undefined)

/** Reads the supported query parameters. `+` and percent escapes are decoded by URLSearchParams. */
export function parsePrefilledIssueParams(search: string | URLSearchParams): PrefilledIssueParams {
  const params = typeof search === 'string' ? new URLSearchParams(search) : search
  const labels = [...params.getAll('label'), ...params.getAll('labels')]
    .flatMap(value => value.split(','))
    .map(value => value.trim())
    .filter(Boolean)
  return {
    title: present(params.get('title')),
    description: present(params.get('description')),
    team: present(params.get('team'))?.trim(),
    status: present(params.get('status'))?.trim(),
    priority: present(params.get('priority'))?.trim(),
    assignee: present(params.get('assignee'))?.trim(),
    estimate: present(params.get('estimate'))?.trim(),
    cycle: present(params.get('cycle'))?.trim(),
    labels,
    project: present(params.get('project'))?.trim(),
    projectMilestone: present(params.get('projectMilestone'))?.trim(),
    template: present(params.get('template'))?.trim(),
    parent: present(params.get('parent'))?.trim(),
    dueDate: present(params.get('dueDate'))?.trim(),
  }
}

export function parsePriority(value: string | undefined): 0 | 1 | 2 | 3 | 4 | undefined {
  const text = norm(value)
  if (!text) return undefined
  if (/^[0-4]$/.test(text)) return Number(text) as 0 | 1 | 2 | 3 | 4
  return PRIORITY_WORDS[text]
}

function findTeam(teams: Team[], value: string | undefined) {
  const text = norm(value)
  if (!text) return undefined
  const live = teams.filter(team => !team.archivedAt && !team.retiredAt)
  return live.find(team => team.id === value?.trim())
    ?? live.find(team => norm(team.key) === text)
    ?? live.find(team => norm(team.name) === text)
}

function findAssignee(data: PrefillData, value: string | undefined) {
  const text = norm(value)
  if (!text) return undefined
  if (text === 'me') return data.viewer.id
  const pool = data.users.filter(user => user.active !== false)
  const matchers: ((user: User) => boolean)[] = [
    user => user.id === value?.trim(),
    user => norm(user.username) === text,
    user => norm(user.displayName) === text,
    user => norm(user.name) === text,
    user => norm(user.email) === text,
  ]
  for (const matches of matchers) {
    const found = pool.find(matches)
    if (found) return found.id
  }
  return undefined
}

function findTemplate(data: PrefillData, teamId: string, value: string | undefined): IssueTemplate | undefined {
  const text = norm(value)
  if (!text) return undefined
  const candidates = issueTemplatesForTeam(data, teamId)
  return candidates.find(template => template.id === value?.trim())
    ?? candidates.find(template => norm(template.name) === text)
}

function findIssue(issues: Issue[], value: string | undefined) {
  const text = norm(value)
  if (!text) return undefined
  return issues.find(issue => issue.id === value?.trim()) ?? issues.find(issue => norm(issue.identifier) === text)
}

export interface ResolveOptions {
  /** Team key from the URL path; it takes precedence over the `team` parameter. */
  pathTeamKey?: string
}

/**
 * Resolves parsed parameters against the workspace data into the create-issue
 * composer's initial context. Anything that cannot be resolved is left out.
 */
export function resolvePrefilledIssue(params: PrefilledIssueParams, data: PrefillData, options: ResolveOptions = {}): MyIssuesCreateContext {
  const context: MyIssuesCreateContext = {}
  const defaultTeam = data.teams[0]
  let team = findTeam(data.teams, options.pathTeamKey) ?? findTeam(data.teams, params.team)

  // Project: scoped to the team when one is known, otherwise any project (which then picks the team).
  const projectText = norm(params.project)
  const projectPool = data.projects.filter(project => !project.archivedAt)
  const matchProject = (pool: typeof projectPool) =>
    pool.find(project => project.id === params.project?.trim()) ?? pool.find(project => norm(project.name) === projectText)
  const project = projectText
    ? team
      ? matchProject(projectPool.filter(item => !item.teamIds.length || item.teamIds.includes(team!.id)))
      : matchProject(projectPool)
    : undefined
  if (!team && project?.teamIds.length && !(defaultTeam && project.teamIds.includes(defaultTeam.id))) {
    team = data.teams.find(item => item.id === project.teamIds[0])
  }

  const parent = findIssue(data.issues, params.parent)
  if (!team && parent) team = data.teams.find(item => item.id === parent.team.id)

  // A workspace template may carry its own team; resolve it against the team we know so far.
  let template = findTemplate(data, (team ?? defaultTeam)?.id ?? '', params.template)
  if (!team && template?.teamId) team = data.teams.find(item => item.id === template!.teamId)
  if (template && team && !issueTemplatesForTeam(data, team.id).some(item => item.id === template!.id)) template = undefined

  const effectiveTeam = team ?? defaultTeam
  if (team) context.teamId = team.id
  const teamId = effectiveTeam?.id ?? ''
  const states = teamId ? teamWorkflowStates(data, teamId) : []

  // Template values first; explicit parameters then override them.
  if (template) {
    context.templateId = template.id
    if (template.title) context.title = template.title
    if (template.body) context.description = template.body
    if (template.stateId && states.some(state => state.id === template.stateId)) context.stateId = template.stateId
    context.priority = parsePriority(String(template.priority)) ?? 0 // out-of-range template values fall back to none
    if (template.assigneeId && data.users.some(user => user.id === template.assigneeId)) context.assigneeId = template.assigneeId
    if (template.projectId && data.projects.some(item => item.id === template.projectId)) context.projectId = template.projectId
    if (template.labelIds?.length) context.labelIds = [...template.labelIds]
  }

  if (params.title) context.title = params.title
  if (params.description) context.description = params.description

  const statusText = norm(params.status)
  if (statusText) {
    const state = states.find(item => item.id === params.status?.trim()) ?? states.find(item => norm(item.name) === statusText)
    if (state) context.stateId = state.id
  }

  const priority = parsePriority(params.priority)
  if (priority !== undefined) context.priority = priority

  const assigneeId = findAssignee(data, params.assignee)
  if (assigneeId) context.assigneeId = assigneeId

  if (params.estimate !== undefined && /^\d+$/.test(params.estimate)) {
    const estimate = Number(params.estimate)
    if (estimateValues(resolvedTeamSettings(data.teamSettings, teamId)).includes(estimate)) context.estimate = estimate
  }

  const cycleText = norm(params.cycle)
  if (cycleText && teamId) {
    const cycles = data.cycles.filter(cycle => cycle.teamId === teamId)
    const cycle = cycles.find(item => item.id === params.cycle?.trim())
      ?? (/^\d+$/.test(cycleText) ? cycles.find(item => item.number === Number(cycleText)) : undefined)
      ?? cycles.find(item => norm(item.name) === cycleText)
    if (cycle) context.cycleId = cycle.id
  }

  if (params.labels.length && teamId) {
    const ancestors = labelTeamScopeIds(teamId, data.teams, data.teamSettings).slice(1)
    const available = labelsForIssueTeam(data.labels, teamId, data.labelGroups, ancestors)
    const picked: typeof available = []
    for (const value of params.labels) {
      const text = norm(value)
      const label = available.find(item => item.id === value) ?? available.find(item => norm(item.name) === text)
      if (!label || picked.some(item => item.id === label.id)) continue
      // A label group allows a single label; the first one in the link wins.
      if (label.groupId && picked.some(item => item.groupId === label.groupId)) continue
      picked.push(label)
    }
    if (picked.length) context.labelIds = [...new Set([...(template ? context.labelIds ?? [] : []), ...picked.map(label => label.id)])]
  }

  if (project) {
    context.projectId = project.id
    const milestoneText = norm(params.projectMilestone)
    if (milestoneText) {
      const milestone = project.milestones.find(item => item.id === params.projectMilestone?.trim())
        ?? project.milestones.find(item => norm(item.name) === milestoneText)
      if (milestone) context.projectMilestoneId = milestone.id
    }
  }

  if (parent) context.parentId = parent.id
  if (params.dueDate && /^\d{4}-\d{2}-\d{2}$/.test(params.dueDate) && !Number.isNaN(Date.parse(params.dueDate))) context.dueDate = params.dueDate
  return context
}

/** Convenience: parse a query string and resolve it in one step. */
export function prefilledIssueContext(search: string | URLSearchParams, data: PrefillData, options: ResolveOptions = {}): MyIssuesCreateContext {
  return resolvePrefilledIssue(parsePrefilledIssueParams(search), data, options)
}

/**
 * The "Copy prefilled create issue URL" link for an issue: opens the composer
 * on the issue's team with the same properties set.
 */
export function buildPrefilledIssueURL(issue: Issue, data: PrefillData, origin: string): string {
  const query = new URLSearchParams()
  const team = data.teams.find(item => item.id === issue.team.id) ?? issue.team
  if (issue.title) query.set('title', issue.title)
  if (issue.description) query.set('description', issue.description)
  if (issue.state?.name) query.set('status', issue.state.name)
  const priority = PRIORITY_NAMES[issue.priority]
  if (priority && issue.priority > 0) query.set('priority', priority)
  if (issue.assignee) query.set('assignee', issue.assignee.id)
  if (issue.estimate !== undefined && issue.estimate !== null && issue.estimate >= 0) query.set('estimate', String(issue.estimate))
  const cycle = issue.cycleId ? data.cycles.find(item => item.id === issue.cycleId) : undefined
  if (cycle) query.set('cycle', String(cycle.number))
  const labelRefs = issue.labels.map(label => {
    const sameName = data.labels.filter(item => norm(item.name) === norm(label.name)).length > 1
    return sameName || label.name.includes(',') ? label.id : label.name
  })
  if (labelRefs.length) query.set('labels', labelRefs.join(','))
  if (issue.project) query.set('project', issue.project.id)
  if (issue.project && issue.projectMilestoneId) query.set('projectMilestone', issue.projectMilestoneId)
  if (issue.parentId) query.set('parent', issue.parentId)
  if (issue.dueDate) query.set('dueDate', issue.dueDate.slice(0, 10))
  const search = query.toString().replace(/%2C/gi, ',')
  return `${origin.replace(/\/+$/, '')}${newIssuePath(data.workspace.urlKey, team.key)}${search ? `?${search}` : ''}`
}
