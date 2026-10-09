import {
  customerPath,
  cyclePath,
  documentPath,
  initiativePath,
  issuePath,
  labelPath,
  memberProfilePath,
  parseAppRoute,
  projectPath,
  releasePath,
  reviewPath,
  savedViewPathId,
  teamHomePath,
  workspaceSavedViewPath,
} from '@/lib/app-routes'
import type {
  BootstrapData,
  CodeReview,
  Customer,
  Cycle,
  FlowDocument,
  Initiative,
  Issue,
  IssueLabel,
  Project,
  ProjectMilestone,
  Release,
  ReleasePipeline,
  SavedView,
  Team,
  User,
} from '@/types/flow'

/** Every resource type an agent answer can mention as an inline chip with a hover card. */
export type AgentEntityKind =
  | 'issue'
  | 'project'
  | 'initiative'
  | 'document'
  | 'user'
  | 'team'
  | 'cycle'
  | 'label'
  | 'milestone'
  | 'customer'
  | 'release'
  | 'view'
  | 'review'
  | 'update'
  | 'comment'

export const AGENT_ENTITY_KINDS: readonly AgentEntityKind[] = ['issue', 'project', 'initiative', 'document', 'user', 'team', 'cycle', 'label', 'milestone', 'customer', 'release', 'view', 'review', 'update', 'comment']

/** What an answer's entity shortcode carries: the kind, a resolvable key, the visible label and (for link-only kinds) the href. */
export type AgentEntityTarget = { kind: AgentEntityKind; id: string; label: string; href?: string }

/** A target resolved against workspace data (or a record fetched by id). */
export type AgentEntity =
  | { kind: 'issue'; issue: Issue }
  | { kind: 'project'; project: Project }
  | { kind: 'initiative'; initiative: Initiative }
  | { kind: 'document'; document: FlowDocument }
  | { kind: 'user'; user: User }
  | { kind: 'team'; team: Team }
  | { kind: 'cycle'; cycle: Cycle; team?: Team }
  | { kind: 'label'; label: IssueLabel }
  | { kind: 'milestone'; milestone: ProjectMilestone; project: Project }
  | { kind: 'customer'; customer: Customer }
  | { kind: 'release'; release: Release; pipeline?: ReleasePipeline }
  | { kind: 'view'; view: SavedView }
  | { kind: 'review'; review: CodeReview }
  /** Updates, comments and milestones the client cannot look up are addressed by link: the label and href come from the answer. */
  | { kind: 'link'; icon: 'update' | 'comment' | 'milestone'; label: string; href: string }

export function findAgentIssue(data: BootstrapData, identifier: string) {
  const key = identifier.toUpperCase()
  return data.issues.find(issue => issue.identifier.toUpperCase() === key)
}

/** Team keys of the workspace; in paged mode an identifier with one of them may name an issue the client does not hold. */
export function agentTeamKeys(data: BootstrapData) {
  return new Set((data.teams ?? []).map(team => team.key.toUpperCase()))
}

export function isAgentIdentifier(data: BootstrapData, identifier: string) {
  return agentTeamKeys(data).has(identifier.toUpperCase().replace(/-\d+$/, ''))
}

function lower(value: string | undefined) {
  return (value ?? '').toLowerCase()
}

function safeDecode(value: string) {
  try { return decodeURIComponent(value) } catch { return value }
}

function findTeamByKey(data: BootstrapData, key: string) {
  return (data.teams ?? []).find(team => lower(team.key) === lower(key))
}

function findProject(data: BootstrapData, slugOrId: string) {
  return (data.projects ?? []).find(project => project.slugId === slugOrId || project.id === slugOrId)
}

/** Resolves a shortcode target to the workspace entity it names; undefined when the client does not hold it. */
export function resolveAgentEntity(data: BootstrapData, target: Pick<AgentEntityTarget, 'kind' | 'id'> & Partial<AgentEntityTarget>): AgentEntity | undefined {
  const { kind, id } = target
  switch (kind) {
    case 'issue': {
      const key = id.toUpperCase()
      const issue = data.issues.find(item => item.id === id || item.identifier.toUpperCase() === key)
      return issue ? { kind, issue } : undefined
    }
    case 'project': {
      const project = findProject(data, id)
      return project ? { kind, project } : undefined
    }
    case 'initiative': {
      const initiative = (data.initiatives ?? []).find(item => item.id === id || item.slugId === id)
      return initiative ? { kind, initiative } : undefined
    }
    case 'document': {
      const document = (data.documents ?? []).find(item => item.id === id || item.slugId === id)
      return document ? { kind, document } : undefined
    }
    case 'user': {
      const user = (data.users ?? []).find(item => item.id === id)
      return user ? { kind, user } : undefined
    }
    case 'team': {
      const team = (data.teams ?? []).find(item => item.id === id)
      return team ? { kind, team } : undefined
    }
    case 'cycle': {
      const cycle = (data.cycles ?? []).find(item => item.id === id)
      return cycle ? { kind, cycle, team: (data.teams ?? []).find(item => item.id === cycle.teamId) } : undefined
    }
    case 'label': {
      const label = (data.labels ?? []).find(item => item.id === id)
      return label ? { kind, label } : undefined
    }
    case 'milestone': {
      for (const project of data.projects ?? []) {
        const milestone = project.milestones?.find(item => item.id === id)
        if (milestone) return { kind, milestone, project }
      }
      return target.href ? { kind: 'link', icon: 'milestone', label: target.label ?? '', href: target.href } : undefined
    }
    case 'customer': {
      const customer = (data.customers ?? []).find(item => item.id === id)
      return customer ? { kind, customer } : undefined
    }
    case 'release': {
      const release = (data.releases ?? []).find(item => item.id === id)
      return release ? { kind, release, pipeline: (data.releasePipelines ?? []).find(item => item.id === release.pipelineId) } : undefined
    }
    case 'view': {
      const view = (data.savedViews ?? []).find(item => item.id === id || item.slugId === id)
      return view ? { kind, view } : undefined
    }
    case 'review': {
      const review = (data.reviews ?? []).find(item => item.id === id || item.slugId === id)
      return review ? { kind, review } : undefined
    }
    case 'update':
    case 'comment':
      return target.href ? { kind: 'link', icon: kind, label: target.label ?? '', href: target.href } : undefined
  }
}

/** The Flow route an entity chip opens. */
export function agentEntityPath(data: BootstrapData, entity: AgentEntity): string {
  const workspace = data.workspace.urlKey
  switch (entity.kind) {
    case 'issue': return issuePath(workspace, entity.issue)
    case 'project': return projectPath(workspace, entity.project)
    case 'initiative': return initiativePath(workspace, entity.initiative)
    case 'document': return documentPath(workspace, entity.document)
    case 'user': return memberProfilePath(workspace, entity.user.username || entity.user.id)
    case 'team': return teamHomePath(workspace, entity.team.key)
    case 'cycle': return cyclePath(workspace, entity.team?.key ?? '', entity.cycle)
    case 'label': return labelPath(workspace, entity.label.resourceType === 'project' ? 'project' : entity.label.resourceType === 'initiative' ? 'initiative' : 'issue', entity.label.name)
    case 'milestone': return `${projectPath(workspace, entity.project)}#milestone-${entity.milestone.id}`
    case 'customer': return customerPath(workspace, entity.customer)
    case 'release': return releasePath(workspace, entity.pipeline?.slugId ?? entity.release.pipelineId ?? '', entity.release.slugId)
    case 'view': return workspaceSavedViewPath(workspace, savedViewPathId(entity.view))
    case 'review': return reviewPath(workspace, entity.review)
    case 'link': return entity.href
  }
}

/** The shortcode id for an entity: stable and enough to resolve it again from workspace data. */
export function agentEntityKey(entity: AgentEntity): string {
  switch (entity.kind) {
    case 'issue': return entity.issue.id
    case 'project': return entity.project.id
    case 'initiative': return entity.initiative.id
    case 'document': return entity.document.id
    case 'user': return entity.user.id
    case 'team': return entity.team.id
    case 'cycle': return entity.cycle.id
    case 'label': return entity.label.id
    case 'milestone': return entity.milestone.id
    case 'customer': return entity.customer.id
    case 'release': return entity.release.id
    case 'view': return entity.view.id
    case 'review': return entity.review.id
    case 'link': return entity.href
  }
}

/** The label an entity gets in a shortcode (also what shows when the chip cannot be drawn). */
export function agentEntityLabel(entity: AgentEntity): string {
  switch (entity.kind) {
    case 'issue': return entity.issue.identifier
    case 'project': return entity.project.name
    case 'initiative': return entity.initiative.name
    case 'document': return entity.document.title
    case 'user': return entity.user.displayName || entity.user.name
    case 'team': return entity.team.name
    case 'cycle': return entity.cycle.name || `Cycle ${entity.cycle.number}`
    case 'label': return entity.label.name
    case 'milestone': return entity.milestone.name
    case 'customer': return entity.customer.name
    case 'release': return entity.release.name
    case 'view': return entity.view.name
    case 'review': return entity.review.title
    case 'link': return entity.label
  }
}

function userByName(data: BootstrapData, username: string) {
  const key = lower(safeDecode(username))
  return (data.users ?? []).find(user => lower(user.username) === key || user.id === username)
}

/**
 * A Flow URL (any workspace page URL on this app) resolved to the entity target it names. Unknown ids resolve to
 * undefined, except issues (with a team-key identifier) and projects the client does not hold (paged workspaces, or
 * records created since the page loaded), which are fetched when the chip renders and fall back to plain text if missing.
 */
export function parseAgentEntityUrl(url: string, data: BootstrapData, linkText?: string): AgentEntityTarget | undefined {
  let parsed: URL
  const origin = typeof window !== 'undefined' ? window.location.origin : 'http://flow.local'
  try {
    parsed = new URL(url, origin)
  } catch {
    return undefined
  }
  if (/^[a-z][a-z0-9+.-]*:/i.test(url) && parsed.origin !== origin) return undefined
  const route = parseAppRoute(parsed.pathname)
  if (!('workspaceSlug' in route) || route.workspaceSlug !== data.workspace.urlKey) return undefined
  const hash = parsed.hash.replace(/^#/, '')
  const href = `${parsed.pathname}${parsed.search}${parsed.hash}`
  const text = linkText?.trim() || ''
  const entityTarget = (entity: AgentEntity | undefined): AgentEntityTarget | undefined =>
    entity ? { kind: entity.kind === 'link' ? entity.icon : entity.kind, id: agentEntityKey(entity), label: agentEntityLabel(entity), ...(entity.kind === 'link' ? { href: entity.href } : {}) } : undefined
  const linkOnly = (kind: 'update' | 'comment'): AgentEntityTarget => ({ kind, id: href, label: text || (kind === 'update' ? 'Update' : 'Comment'), href })
  switch (route.kind) {
    case 'issue': {
      if (/^comment-/.test(hash)) return linkOnly('comment')
      const issue = findAgentIssue(data, route.identifier)
      if (issue) return entityTarget({ kind: 'issue', issue })
      return isAgentIdentifier(data, route.identifier)
        ? { kind: 'issue', id: route.identifier.toUpperCase(), label: route.identifier.toUpperCase() }
        : undefined
    }
    case 'project': {
      const slug = route.projectSlugId
      if (/^(?:project-)?update-/.test(hash)) return linkOnly('update')
      if (/^milestone-/.test(hash)) {
        const milestoneId = hash.slice('milestone-'.length)
        const project = findProject(data, slug)
        const milestone = project?.milestones?.find(item => item.id === milestoneId)
        return milestone && project ? entityTarget({ kind: 'milestone', milestone, project }) : { kind: 'milestone', id: href, label: text || 'Milestone', href }
      }
      const project = findProject(data, slug)
      if (project) return entityTarget({ kind: 'project', project })
      return { kind: 'project', id: slug, label: text || slug }
    }
    case 'initiative': {
      if (/^(?:initiative-)?update-/.test(hash)) return linkOnly('update')
      const initiative = (data.initiatives ?? []).find(item => item.slugId === route.initiativeSlugId || item.id === route.initiativeSlugId)
      return entityTarget(initiative ? { kind: 'initiative', initiative } : undefined)
    }
    case 'document': {
      const document = (data.documents ?? []).find(item => item.slugId === route.documentSlugId || item.id === route.documentSlugId)
      return entityTarget(document ? { kind: 'document', document } : undefined)
    }
    case 'member-profile': {
      const user = userByName(data, route.username)
      return entityTarget(user ? { kind: 'user', user } : undefined)
    }
    case 'team-overview': {
      const team = findTeamByKey(data, route.teamKey)
      return entityTarget(team ? { kind: 'team', team } : undefined)
    }
    case 'cycle': {
      const team = findTeamByKey(data, route.teamKey)
      const cycle = team && (data.cycles ?? []).find(item => item.teamId === team.id && (String(item.number) === route.cycleId || item.id === route.cycleId))
      return entityTarget(cycle ? { kind: 'cycle', cycle, team } : undefined)
    }
    case 'label': {
      const name = lower(route.resourceName)
      const label = (data.labels ?? []).find(item => lower(item.name) === name && (item.resourceType ?? 'issue') === route.resourceType)
        ?? (data.labels ?? []).find(item => lower(item.name) === name)
      return entityTarget(label ? { kind: 'label', label } : undefined)
    }
    case 'customer': {
      const suffix = route.customerSlugId.slice(-12)
      const customer = (data.customers ?? []).find(item => item.id === route.customerSlugId || item.id.slice(-12) === suffix)
      return entityTarget(customer ? { kind: 'customer', customer } : undefined)
    }
    case 'release': {
      const pipeline = (data.releasePipelines ?? []).find(item => item.slugId === route.pipelineSlug || item.id === route.pipelineSlug)
      const release = (data.releases ?? []).find(item => (item.slugId === route.releaseSlug || item.id === route.releaseSlug) && (!pipeline || !item.pipelineId || item.pipelineId === pipeline.id))
      return entityTarget(release ? { kind: 'release', release, pipeline } : undefined)
    }
    case 'review': {
      const review = (data.reviews ?? []).find(item => item.slugId === route.reviewSlug || item.id === route.reviewSlug)
      return entityTarget(review ? { kind: 'review', review } : undefined)
    }
    case 'workspace-saved-view':
    case 'team-saved-view':
    case 'projects-saved-view':
    case 'team-projects-saved-view':
    case 'project-saved-view': {
      const view = (data.savedViews ?? []).find(item => item.id === route.viewId || item.slugId === route.viewId)
      return entityTarget(view ? { kind: 'view', view } : undefined)
    }
    default:
      return undefined
  }
}

/** Kinds that read as plain text mentions (an @name) instead of a boxed chip. */
export function isAgentTextMention(kind: AgentEntityKind) {
  return kind === 'user'
}
