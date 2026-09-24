import type { BootstrapData } from '@/types/flow'
import { filterValues, type IssueQueryAstNode, type MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'

/**
 * Translate a filter-bar filter into the server query vocabulary (`compileIssueFilter`), so every
 * field the filter menu offers also works on server-paged lists. Fields whose meaning depends on
 * workspace data (initiatives, releases, project properties) are resolved to ids here.
 */
export interface IssueFilterQueryContext {
  data: Pick<BootstrapData, 'projects' | 'releases' | 'viewer'> & Partial<Pick<BootstrapData, 'labels'>>
  now?: number
}

const DAY = 86_400_000
const any = (nodes: IssueQueryAstNode[]): IssueQueryAstNode => nodes.length === 1 ? nodes[0] : { or: nodes }
const none: IssueQueryAstNode = { field: 'id', operator: 'in', values: [] }

export function filterToQueryNode(filter: MyIssuesAppliedFilter, context: IssueFilterQueryContext): IssueQueryAstNode {
  const values = filterValues(filter).map(value => value.value)
  const node = positiveNode(filter.field, values, context)
  return filter.operator === 'isNot' ? { not: node } : node
}

function positiveNode(field: MyIssuesAppliedFilter['field'], values: string[], { data, now = Date.now() }: IssueFilterQueryContext): IssueQueryAstNode {
  const since = (days: number) => new Date(now - days * DAY).toISOString()
  const isoDay = (offset: number) => { const date = new Date(now + offset * DAY); date.setHours(0, 0, 0, 0); return date.toISOString().slice(0, 10) }
  switch (field) {
    case 'status': return { field: 'status', operator: 'in', values }
    case 'priority': return { field: 'priority', operator: 'in', values }
    case 'assignee': return nullable('assignee', values)
    case 'creator': return { field: 'creator', operator: 'in', values }
    case 'labels': return { field: 'labels', operator: 'in', values }
    case 'project': return nullable('project', values)
    case 'projectMilestone': return nullable('projectMilestoneId', values)
    case 'cycle': return nullable('cycle', values)
    case 'subscribers': return { field: 'subscribers', operator: 'in', values }
    case 'template': return nullable('templateId', values)
    case 'externalSource': return nullable('externalSource', values)
    case 'addedToCycle': return { field: 'addedToCycle', operator: 'in', values }
    case 'agent': return values.includes('*') ? { field: 'delegateId', operator: 'isNotEmpty' } : nullable('delegateId', values)
    case 'agentSession': return values.includes('*') ? { field: 'agentSessionId', operator: 'isNotEmpty' } : { field: 'agentSessionId', operator: 'isEmpty' }
    case 'autoClosed': return values.includes('true') ? { field: 'autoClosed', operator: 'isNotEmpty' } : { field: 'autoClosed', operator: 'isEmpty' }
    case 'links': return values.includes('has-links') ? { field: 'hasLinks', operator: 'isNotEmpty' } : { field: 'hasLinks', operator: 'isEmpty' }
    case 'relations': return values.includes('')
      ? { not: any(['blocks', 'blocked_by', 'related', 'duplicate'].map((type): IssueQueryAstNode => ({ field: `relation:${type}`, operator: 'isNotEmpty' }))) }
      : any(values.map(type => ({ field: `relation:${type}`, operator: 'isNotEmpty' })))
    case 'suggestedLabel': {
      const chosen: IssueQueryAstNode[] = values.filter(Boolean).map(id => ({ field: `suggestedLabel:${id}`, operator: 'isNotEmpty' }))
      const known: IssueQueryAstNode[] = (data.labels ?? []).map(label => ({ field: `suggestedLabel:${label.id}`, operator: 'isNotEmpty' }))
      const nodes: IssueQueryAstNode[] = [...chosen, ...(values.includes('') ? [{ not: any(known.length ? known : [none]) }] : [])]
      return any(nodes.length ? nodes : [none])
    }
    case 'content': return any(values.filter(value => value.startsWith('query:')).map(value => ({ field: 'title', operator: 'contains', values: [value.slice(6)] })))
    case 'initiative': {
      const projectIds = data.projects.filter(project => project.initiatives?.some(id => values.includes(id))).map(project => project.id)
      const withoutInitiative = data.projects.filter(project => !project.initiatives?.length).map(project => project.id)
      const nodes: IssueQueryAstNode[] = [
        ...(projectIds.length ? [{ field: 'project', operator: 'in', values: projectIds }] : []),
        ...(values.includes('') ? [{ field: 'project', operator: 'isEmpty' }, ...(withoutInitiative.length ? [{ field: 'project', operator: 'in', values: withoutInitiative }] : [])] : []),
      ]
      return any(nodes.length ? nodes : [none])
    }
    case 'releases': {
      const releases = data.releases ?? []
      const ids = (predicate: (release: (typeof releases)[number]) => boolean) => [...new Set(releases.filter(predicate).flatMap(release => release.issueIds ?? []))]
      return any(values.map(value => {
        if (value === 'no-releases') return { not: { field: 'id', operator: 'in', values: ids(() => true) } }
        if (value === 'released-any') return { field: 'id', operator: 'in', values: ids(release => Boolean(release.releasedAt)) }
        if (value.startsWith('release:')) return { field: 'id', operator: 'in', values: ids(release => release.id === value.slice(8)) }
        if (value.startsWith('release-pipeline:')) return { field: 'id', operator: 'in', values: ids(release => release.pipelineId === value.slice(17)) }
        if (value.startsWith('release-stage:')) return { field: 'id', operator: 'in', values: ids(release => release.stage === value.slice(14)) }
        if (value.startsWith('release-stage-type:')) return { field: 'id', operator: 'in', values: ids(release => release.status === value.slice(19)) }
        return none
      }))
    }
    case 'dates': return any(values.map(value => {
      const window = value.endsWith('day') ? 1 : value.endsWith('week') ? 7 : 30
      if (value.startsWith('created-past-')) return { field: 'createdAt', operator: 'after', values: [since(window)] }
      if (value.startsWith('updated-past-')) return { field: 'updatedAt', operator: 'after', values: [since(window)] }
      if (value === 'started-any') return { field: 'startedAt', operator: 'isNotEmpty' }
      if (value === 'completed-any') return { field: 'completedAt', operator: 'isNotEmpty' }
      if (value === 'auto-closed-any') return { field: 'autoClosed', operator: 'isNotEmpty' }
      if (value === 'triaged-any') return { field: 'triagedAt', operator: 'isNotEmpty' }
      if (value === 'status-over-week') return { field: 'statusChangedAt', operator: 'before', values: [since(7)] }
      if (value === 'has-due-date') return { field: 'dueDate', operator: 'isNotEmpty' }
      if (value === 'no-due-date') return { field: 'dueDate', operator: 'isEmpty' }
      if (value === 'overdue') return { field: 'dueDate', operator: 'before', values: [isoDay(0)] }
      if (value === 'today') return { and: [{ field: 'dueDate', operator: 'gte', values: [isoDay(0)] }, { field: 'dueDate', operator: 'lte', values: [isoDay(0)] }] }
      if (value === 'next-week') return { and: [{ field: 'dueDate', operator: 'gte', values: [isoDay(0)] }, { field: 'dueDate', operator: 'lte', values: [isoDay(7)] }] }
      return none
    }))
    case 'projectProperties': {
      const projectIds = data.projects.filter(project => values.some(value =>
        value.startsWith('project-status:') ? project.status?.id === value.slice(15)
          : value.startsWith('project-status-type:') ? project.status?.type === value.slice(20)
            : value.startsWith('project-priority:') ? String(project.priority) === value.slice(17)
              : value.startsWith('project-label:') ? project.labelIds?.includes(value.slice(14))
                : value.startsWith('project-lead:') ? (project.lead?.id ?? '') === value.slice(13)
                  : value.startsWith('project-milestone-name-contains:') ? project.milestones?.some(milestone => milestone.name.toLocaleLowerCase().includes(value.slice(32).toLocaleLowerCase())) : false)).map(project => project.id)
      return projectIds.length ? { field: 'project', operator: 'in', values: projectIds } : none
    }
    case 'ai': return any(values.map(value => value === 'assigned-to-me' ? { field: 'assignee', operator: 'in', values: [data.viewer.id] }
      : value === 'completed-last-month' ? { field: 'completedAt', operator: 'after', values: [since(30)] }
        : value === 'due-next-two-weeks' ? { field: 'dueDate', operator: 'lte', values: [isoDay(14)] }
          : value.startsWith('query:') ? { field: 'title', operator: 'contains', values: [value.slice(6)] } : none))
    case 'advanced': return { and: values.map(value => { const [key, ...rest] = value.split(':'); const expected = rest.join(':'); return key === 'labels' ? { field: 'labels', values: [expected] } : ['status', 'assignee', 'priority', 'project'].includes(key) ? { field: key, values: [expected] } : { and: [] } }) }
    case 'customers': return { field: 'customerId', operator: 'in', values }
    default: return { and: [] }
  }
}

/** `is` on a nullable column: "" means unset. */
function nullable(field: string, values: string[]): IssueQueryAstNode {
  const set = values.filter(Boolean)
  const nodes: IssueQueryAstNode[] = []
  if (set.length) nodes.push({ field, operator: 'in', values: set })
  if (values.includes('')) nodes.push({ field, operator: 'isEmpty' })
  return nodes.length ? any(nodes) : none
}
