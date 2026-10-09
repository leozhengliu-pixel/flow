import type { AgentEntity, AgentEntityKind } from '@/components/agent/agent-entity-refs'
import { agentEntityLabel } from '@/components/agent/agent-entity-refs'
import { personMatchesQuery } from '@/lib/people'
import type { BootstrapData, Issue, User } from '@/types/flow'
import { mentionAttrsForEntity, type MentionAttrs } from './mention-model'

/** One row of the "@" menu: a resource the cursor can reference. */
export type MentionOption = {
  key: string
  kind: AgentEntityKind
  /** Menu section heading (translated by the menu). */
  group: string
  entity: AgentEntity
  attrs: MentionAttrs
  /** Secondary text (issue title, project of a milestone, …). */
  detail?: string
}

const GROUPS: Array<{ kind: AgentEntityKind; group: string; limit: number }> = [
  { kind: 'user', group: 'People', limit: 6 },
  { kind: 'issue', group: 'Issues', limit: 6 },
  { kind: 'project', group: 'Projects', limit: 4 },
  { kind: 'document', group: 'Documents', limit: 4 },
  { kind: 'initiative', group: 'Initiatives', limit: 3 },
  { kind: 'review', group: 'Reviews', limit: 3 },
  { kind: 'customer', group: 'Customers', limit: 3 },
  { kind: 'view', group: 'Views', limit: 3 },
  { kind: 'release', group: 'Releases', limit: 3 },
  { kind: 'team', group: 'Teams', limit: 3 },
  { kind: 'cycle', group: 'Cycles', limit: 3 },
  { kind: 'milestone', group: 'Milestones', limit: 3 },
  { kind: 'label', group: 'Labels', limit: 3 },
]

function rank(query: string, ...fields: Array<string | undefined>) {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return 1
  let best = 0
  for (const field of fields) {
    const value = (field ?? '').toLocaleLowerCase()
    if (!value) continue
    if (value === needle) return 4
    if (value.startsWith(needle)) best = Math.max(best, 3)
    else if (value.split(/[\s\-_/]+/).some(word => word.startsWith(needle))) best = Math.max(best, 2)
    else if (value.includes(needle)) best = Math.max(best, 1)
  }
  return best
}

function option(data: BootstrapData | undefined, group: string, entity: AgentEntity, detail?: string): MentionOption {
  const attrs = mentionAttrsForEntity(data, entity)
  return { key: `${attrs.mentionType}:${attrs.id}`, kind: entity.kind === 'link' ? entity.icon : entity.kind, group, entity, attrs, ...(detail ? { detail } : {}) }
}

function top<T>(items: T[], score: (item: T) => number, limit: number) {
  return items
    .map(item => ({ item, score: score(item) }))
    .filter(entry => entry.score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, limit)
    .map(entry => entry.item)
}

/**
 * What the "@" menu offers for a query: people first, then issues, projects, documents and the other resource types,
 * each section ranked and capped. With no query only people, issues and the main resources show (like Linear's menu).
 * `issues` is the issue pool (workspace data plus what a fetch added), `users` the people the viewer may mention.
 */
export function mentionOptions(data: BootstrapData | undefined, query: string, pools: { users: User[]; issues: Issue[] }): MentionOption[] {
  const needle = query.trim()
  const sections: MentionOption[][] = []
  for (const { kind, group, limit } of GROUPS) {
    if (!data && kind !== 'user') continue
    // Everything beyond the first five sections only appears once the person starts typing.
    if (!needle && !['user', 'issue', 'project', 'document'].includes(kind)) continue
    switch (kind) {
      case 'user':
        sections.push(top(pools.users, user => personMatchesQuery(user, needle) ? rank(needle, user.displayName, user.name, user.username) + 1 : 0, limit).map(user => option(data, group, { kind: 'user', user })))
        break
      case 'issue':
        sections.push(top(pools.issues.filter(issue => !issue.archivedAt), issue => rank(needle, issue.identifier, issue.title), limit).map(issue => option(data, group, { kind: 'issue', issue }, issue.title)))
        break
      case 'project':
        sections.push(top((data?.projects ?? []).filter(project => !project.archivedAt), project => rank(needle, project.name), limit).map(project => option(data, group, { kind: 'project', project })))
        break
      case 'document':
        sections.push(top((data?.documents ?? []).filter(document => !document.archivedAt), document => rank(needle, document.title), limit).map(document => option(data, group, { kind: 'document', document })))
        break
      case 'initiative':
        sections.push(top(data?.initiatives ?? [], initiative => rank(needle, initiative.name), limit).map(initiative => option(data, group, { kind: 'initiative', initiative })))
        break
      case 'review':
        sections.push(top(data?.reviews ?? [], review => rank(needle, review.title, `${review.repositoryName}#${review.number}`), limit).map(review => option(data, group, { kind: 'review', review }, `${review.repositoryOwner}/${review.repositoryName}#${review.number}`)))
        break
      case 'customer':
        sections.push(top(data?.customers ?? [], customer => rank(needle, customer.name), limit).map(customer => option(data, group, { kind: 'customer', customer })))
        break
      case 'view':
        sections.push(top(data?.savedViews ?? [], view => rank(needle, view.name), limit).map(view => option(data, group, { kind: 'view', view })))
        break
      case 'release':
        sections.push(top(data?.releases ?? [], release => rank(needle, release.name, release.version), limit).map(release => option(data, group, { kind: 'release', release, pipeline: (data?.releasePipelines ?? []).find(item => item.id === release.pipelineId) })))
        break
      case 'team':
        sections.push(top(data?.teams ?? [], team => rank(needle, team.name, team.key), limit).map(team => option(data, group, { kind: 'team', team }, team.key)))
        break
      case 'cycle': {
        const cycles = data?.cycles ?? []
        sections.push(top(cycles, cycle => rank(needle, cycle.name, `cycle ${cycle.number}`), limit).map(cycle => {
          const team = (data?.teams ?? []).find(item => item.id === cycle.teamId)
          return option(data, group, { kind: 'cycle', cycle, team }, team?.name)
        }))
        break
      }
      case 'milestone': {
        const milestones = (data?.projects ?? []).flatMap(project => (project.milestones ?? []).map(milestone => ({ milestone, project })))
        sections.push(top(milestones, entry => rank(needle, entry.milestone.name), limit).map(entry => option(data, group, { kind: 'milestone', ...entry }, entry.project.name)))
        break
      }
      case 'label':
        sections.push(top((data?.labels ?? []).filter(label => !label.archivedAt), label => rank(needle, label.name), limit).map(label => option(data, group, { kind: 'label', label })))
        break
      default:
        break
    }
  }
  return sections.flat().filter((entry, index, all) => all.findIndex(other => other.key === entry.key) === index)
}

/** Plain text a person reads for an option (used as its accessible name). */
export function mentionOptionName(entry: MentionOption) {
  return entry.detail && entry.kind !== 'milestone' && entry.kind !== 'cycle' && entry.kind !== 'team' ? `${entry.attrs.label} ${entry.detail}` : agentEntityLabel(entry.entity)
}

/** Replaces the "@query" range with the chosen mention and a trailing space. */
export function insertMentionOption(view: import('@tiptap/pm/view').EditorView, range: { from: number; to: number } | null | undefined, entry: MentionOption) {
  if (!range) return
  const node = view.state.schema.nodes.mention?.create(entry.attrs)
  if (!node) return
  const transaction = view.state.tr.replaceWith(range.from, range.to, node)
  transaction.insertText(' ', range.from + node.nodeSize)
  view.dispatch(transaction)
}
