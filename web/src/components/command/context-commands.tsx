/**
 * Context actions for ⌘K (Linear parity): with an issue open or issues selected
 * the menu leads with issue actions, and "…" actions open a nested page of
 * options inside the same menu. Project and document pages get a smaller set.
 */
import { useState, type ReactNode } from 'react'
import { parseDate } from 'chrono-node'
import {
  Archive, ArrowDown, ArrowUp, Bell, BellOff, CalendarRange, CircleDashed, Clipboard, Copy, CopyPlus, GitBranch, Hash, Layers3,
  Lightbulb, Link2, ListTree, Trash2, UserRound, Users, X,
} from 'lucide-react'
import { toast } from 'sonner'

import { CalendarIcon, CycleIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, ProjectStatusIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { UserAvatar } from '@/components/ui/user-avatar'
import { AgentBadge } from '@/components/agent/agent-badge'
import { assigneeCandidates } from '@/lib/agent-members'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { useIssueSearch } from '@/components/issue/use-issue-search'
import { useIssuesById } from '@/components/issue/use-issues-by-id'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'
import { documentPath, issuePath, projectPath } from '@/lib/app-routes'
import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import { PulseIcon } from '@/components/pulse/pulse-icon'
import { changePulseSubscription, projectPulseSubscribed, sessionPulseChoice } from '@/lib/pulse-subscriptions'
import { estimatePickerOptions } from '@/lib/estimates'
import { configuredIssueBranch, copyIssueForWork } from '@/lib/issue-work-actions'
import { labelTeamScopeIds, labelsForResource, setGroupedLabelSelected } from '@/lib/labels'
import { resolvedTeamSettings } from '@/lib/team-hierarchy'
import type { BootstrapData, Issue, IssueRelationType, IssueUpdateInput, WorkflowState } from '@/types/flow'
import type { CommandContext, CommandIssueRef, IssueCommandContext } from './command-context'

export type CommandPageId =
  | 'status' | 'assignee' | 'priority' | 'labels' | 'dueDate' | 'project' | 'cycle' | 'estimate' | 'team'
  | 'relation' | 'relatedIssue' | 'parent' | 'subIssue'
  | 'projectStatus' | 'projectLead' | 'projectTargetDate' | 'projectInitiative'

export interface CommandPage { id: CommandPageId; label: string; relationType?: IssueRelationType }

export interface ContextAction {
  id: string
  label: string
  icon: ReactNode
  shortcut?: string[]
  keywords?: string
  /** Opens a nested page instead of running. */
  page?: CommandPage
  run?: () => void | Promise<unknown>
  /** Toggle state shown with a check (e.g. "Subscribe to project updates in Pulse"). */
  checked?: boolean
}

export interface PageOption {
  id: string
  label: string
  icon?: ReactNode
  keywords?: string
  /** Multi-select pages: all (`true`), some (`'mixed'`) or none of the targets have it. */
  checked?: boolean | 'mixed'
  /** Single-select pages: the targets' current value. */
  current?: boolean
  detail?: ReactNode
  entity?: boolean
  /** Always rendered regardless of the query (typed custom dates). */
  forceMount?: boolean
  /** Push another page instead of selecting. */
  page?: CommandPage
  select?: () => void | Promise<unknown>
}

export interface ContextCommandHandlers {
  onUpdateIssue?: (issueId: string, input: IssueUpdateInput) => Promise<Issue>
  onUpdateIssues?: (issueIds: string[], input: IssueUpdateInput) => Promise<Issue[]>
  onDeleteIssues?: (issueIds: string[]) => Promise<void>
  onCreateRelation?: (issueId: string, type: IssueRelationType, relatedIssueId: string) => Promise<void>
  onCreateIssueWith?: (context: MyIssuesCreateContext) => void
}

const PRIORITIES = [
  { value: 0, label: 'No priority' }, { value: 1, label: 'Urgent' }, { value: 2, label: 'High' }, { value: 3, label: 'Medium' }, { value: 4, label: 'Low' },
]
const RELATION_PAGES: { type: IssueRelationType; label: string; icon: ReactNode }[] = [
  { type: 'related', label: 'Related to…', icon: <Link2/> },
  { type: 'blocked_by', label: 'Blocked by…', icon: <ArrowDown/> },
  { type: 'blocks', label: 'Blocking…', icon: <ArrowUp/> },
]
const EMPTY_DATA = { issues: [], workspace: { urlKey: '' } } as unknown as BootstrapData

export function contextChip(context: CommandContext | undefined, t: (value: string) => string): { label: string; entity: boolean } | undefined {
  if (!context) return
  if (context.kind === 'issues') {
    if (context.issues.length === 1) return { label: `${context.issues[0].identifier} ${context.issues[0].title}`, entity: true }
    return { label: `${context.issues.length} ${t('issues')}`, entity: false }
  }
  if (context.kind === 'project') return { label: context.project.name, entity: true }
  return { label: context.document.title || t('Untitled document'), entity: true }
}

/** Resolves the context issues to full records (loaded, surface-provided, or fetched). */
function useContextIssues(context: CommandContext | undefined, data: BootstrapData | undefined) {
  const refs: CommandIssueRef[] = context?.kind === 'issues' ? context.issues : []
  const [overlay, setOverlay] = useState<Map<string, Issue>>(() => new Map())
  const loaded = new Map((data?.issues ?? []).map(issue => [issue.id, issue]))
  const missing = refs.filter(ref => !loaded.has(ref.id) && !isFullIssue(ref)).map(ref => ref.id)
  const fetched = useIssuesById(data ? missing : [], data ?? EMPTY_DATA)
  const issues = refs.flatMap(ref => {
    const issue = loaded.get(ref.id) ?? (isFullIssue(ref) ? ref : undefined) ?? overlay.get(ref.id) ?? fetched.get(ref.id)
    return issue ? [issue] : []
  })
  const remember = (updated: unknown) => {
    const list = (Array.isArray(updated) ? updated : [updated]).filter((item): item is Issue => Boolean(item && typeof item === 'object' && 'id' in item))
    if (list.length) setOverlay(current => { const next = new Map(current); for (const issue of list) next.set(issue.id, issue); return next })
  }
  return { refs, issues, remember }
}

function isFullIssue(ref: CommandIssueRef): ref is Issue {
  return Boolean(ref.team && ref.state && Array.isArray(ref.labels) && Array.isArray(ref.subscriberIds))
}

export function useContextCommands({ context, data, page, query, handlers, close, t }: {
  context: CommandContext | undefined
  data: BootstrapData | undefined
  page: CommandPage | undefined
  query: string
  handlers: ContextCommandHandlers
  close: () => void
  t: (value: string) => string
}): { heading: string; actions: ContextAction[]; options: PageOption[] } {
  const { refs, issues, remember } = useContextIssues(context, data)
  const pickerOpen = Boolean(data && page && ['relatedIssue', 'parent', 'subIssue'].includes(page.id))
  const candidates = useIssueSearch(pickerOpen ? query : '', data?.issues ?? [], pickerOpen)
  if (!context || !data) return { actions: [], options: [], heading: '' }
  if (context.kind === 'project') return projectCommands(context, data, page, query, close, t)
  if (context.kind === 'document') {
    const url = `${location.origin}${documentPath(data.workspace.urlKey, context.document)}`
    return { heading: 'Document', options: [], actions: [
      { id: 'ctx-copy-document-url', label: 'Copy document URL', icon: <Clipboard/>, shortcut: ['⌘', '⇧', ','], run: () => copyText(url, t('Copied to clipboard')) },
      { id: 'ctx-copy-document-title', label: 'Copy document title', icon: <Copy/>, run: () => copyText(context.document.title, t('Copied to clipboard')) },
    ] }
  }
  return issueCommands({ context, data, page, query, handlers, close, t, refs, issues, remember, candidates })
}

function issueCommands({ context, data, page, query, handlers, close, t, refs, issues, remember, candidates }: {
  context: IssueCommandContext
  data: BootstrapData
  page: CommandPage | undefined
  query: string
  handlers: ContextCommandHandlers
  close: () => void
  t: (value: string) => string
  refs: CommandIssueRef[]
  issues: Issue[]
  remember: (updated: unknown) => void
  candidates: Issue[]
}) {
  const ids = refs.map(ref => ref.id)
  const single = ids.length === 1
  const first = issues[0]
  const viewerId = data.viewer.id
  const teamIds = [...new Set(issues.map(issue => issue.team.id))]
  const oneTeam = teamIds.length === 1 ? teamIds[0] : undefined
  const guard = (work: Promise<unknown>) => work.then(result => { remember(result); return result }, () => undefined)
  /** One uniform change for every target: the surface's handler for one issue, the bulk API for several. */
  const apply = (input: IssueUpdateInput) => {
    if (single && context.onUpdate) return guard(context.onUpdate(input))
    if (single && handlers.onUpdateIssue) return guard(handlers.onUpdateIssue(ids[0], input))
    if (handlers.onUpdateIssues) return guard(handlers.onUpdateIssues(ids, input))
    return Promise.resolve()
  }
  /** Per-issue changes (labels, subscribers) that depend on each issue's current value. */
  const applyEach = (build: (issue: Issue) => IssueUpdateInput | undefined) => Promise.all(issues.map(issue => {
    const input = build(issue)
    if (!input) return undefined
    if (single && context.onUpdate) return guard(context.onUpdate(input))
    return handlers.onUpdateIssue ? guard(handlers.onUpdateIssue(issue.id, input)) : undefined
  }))
  const same = <T,>(read: (issue: Issue) => T) => issues.length === ids.length && issues.length > 0 && issues.every(issue => read(issue) === read(issues[0])) ? read(issues[0]) : undefined
  const choose = (work: () => Promise<unknown>) => () => { close(); return work() }

  const cycles = oneTeam ? data.cycles.filter(cycle => cycle.teamId === oneTeam && cycle.status !== 'completed').sort((a, b) => a.startsAt.localeCompare(b.startsAt)) : []
  const teamSettings = oneTeam ? resolvedTeamSettings(data.teamSettings ?? {}, oneTeam) : undefined
  const estimatesUsed = Boolean(teamSettings && teamSettings.estimateType && teamSettings.estimateType !== 'notUsed')
  const allMine = issues.length > 0 && issues.every(issue => issue.assignee?.id === viewerId)
  const subscribed = issues.length > 0 && issues.every(issue => issue.subscriberIds.includes(viewerId))
  const url = (issue: Pick<Issue, 'identifier' | 'title'>) => `${location.origin}${issuePath(data.workspace.urlKey, issue)}`
  const copyIssues = (text: (issue: CommandIssueRef) => string, message: string) => copyText(refs.map(ref => text(issues.find(issue => issue.id === ref.id) ?? ref)).join('\n'), t(message))
  const copyBranch = async () => {
    if (single && first) {
      try {
        await copyIssueForWork(configuredIssueBranch(first, data), 'branch', first, data, input => apply(input).then(() => undefined))
        toast.success(t('Copied git branch name'))
      } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not write to clipboard')) }
      return
    }
    await copyIssues(issue => isFullIssue(issue) ? configuredIssueBranch(issue, data) : issue.identifier.toLowerCase(), 'Copied git branch names')
  }

  const actions: ContextAction[] = [
    { id: 'ctx-status', label: 'Change status…', icon: <Layers3/>, shortcut: ['S'], page: { id: 'status', label: 'Change status…' } },
    { id: 'ctx-assignee', label: 'Assign to…', icon: <UserRound/>, shortcut: ['A'], keywords: 'assignee owner', page: { id: 'assignee', label: 'Assign to…' } },
    allMine
      ? { id: 'ctx-assign-me', label: 'Unassign from me', icon: <NoAssigneeIcon/>, shortcut: ['I'], run: choose(() => apply({ assigneeId: '' })) }
      : { id: 'ctx-assign-me', label: 'Assign to me', icon: <UserRound/>, shortcut: ['I'], keywords: 'self', run: choose(() => apply({ assigneeId: viewerId })) },
    { id: 'ctx-priority', label: 'Change priority…', icon: <PriorityIcon priority={first?.priority ?? 0}/>, shortcut: ['P'], page: { id: 'priority', label: 'Change priority…' } },
    { id: 'ctx-labels', label: 'Add labels…', icon: <LabelIcon/>, shortcut: ['L'], keywords: 'tags change labels', page: { id: 'labels', label: 'Add labels…' } },
    { id: 'ctx-due-date', label: 'Set due date…', icon: <CalendarIcon/>, shortcut: ['⇧', 'D'], keywords: 'deadline', page: { id: 'dueDate', label: 'Set due date…' } },
    { id: 'ctx-project', label: 'Move to project…', icon: <ProjectIcon/>, shortcut: ['⇧', 'P'], keywords: 'add to project', page: { id: 'project', label: 'Move to project…' } },
    ...(cycles.length ? [{ id: 'ctx-cycle', label: 'Add to cycle…', icon: <CycleIcon/>, shortcut: ['⇧', 'C'], keywords: 'sprint', page: { id: 'cycle' as const, label: 'Add to cycle…' } }] : []),
    ...(estimatesUsed ? [{ id: 'ctx-estimate', label: 'Set estimate…', icon: <Hash/>, shortcut: ['⇧', 'E'], keywords: 'points', page: { id: 'estimate' as const, label: 'Set estimate…' } }] : []),
    { id: 'ctx-parent', label: 'Set parent issue…', icon: <ListTree/>, keywords: 'parent', page: { id: 'parent', label: 'Set parent issue…' } },
    ...(single ? [
      { id: 'ctx-sub-issue', label: 'Add sub-issue…', icon: <ListTree/>, keywords: 'child existing', page: { id: 'subIssue' as const, label: 'Add sub-issue…' } },
      ...(handlers.onCreateIssueWith ? [{ id: 'ctx-create-sub-issue', label: 'Create sub-issue', icon: <ListTree/>, keywords: 'new child', run: () => { close(); handlers.onCreateIssueWith?.({ parentId: ids[0], teamId: first?.team.id }) } }] : []),
    ] : []),
    { id: 'ctx-duplicate', label: 'Mark as duplicate…', icon: <Copy/>, keywords: 'duplicate of', page: { id: 'relatedIssue', label: 'Mark as duplicate…', relationType: 'duplicate' } },
    { id: 'ctx-relation', label: 'Add relation…', icon: <Link2/>, keywords: 'related blocking blocked by', page: { id: 'relation', label: 'Add relation…' } },
    subscribed
      ? { id: 'ctx-subscribe', label: 'Unsubscribe', icon: <BellOff/>, keywords: 'notifications', run: choose(() => applyEach(issue => ({ subscriberIds: issue.subscriberIds.filter(id => id !== viewerId) }))) }
      : { id: 'ctx-subscribe', label: 'Subscribe', icon: <Bell/>, keywords: 'notifications', run: choose(() => applyEach(issue => issue.subscriberIds.includes(viewerId) ? undefined : ({ subscriberIds: [...issue.subscriberIds, viewerId] }))) },
    { id: 'ctx-copy-id', label: 'Copy issue ID', icon: <Clipboard/>, shortcut: ['⌘', '.'], keywords: 'identifier', run: choose(() => copyIssues(issue => issue.identifier, 'Copied issue ID')) },
    { id: 'ctx-copy-url', label: 'Copy issue URL', icon: <Link2/>, shortcut: ['⌘', '⇧', ','], keywords: 'link', run: choose(() => copyIssues(url, 'Copied issue URL')) },
    { id: 'ctx-copy-branch', label: 'Copy git branch name', icon: <GitBranch/>, shortcut: ['⌘', '⇧', '.'], keywords: 'git', run: choose(copyBranch) },
    { id: 'ctx-copy-title', label: 'Copy issue title', icon: <Copy/>, shortcut: ['⌘', '⇧', "'"], run: choose(() => copyIssues(issue => issue.title, 'Copied issue title')) },
    ...(single && first && handlers.onCreateIssueWith ? [{ id: 'ctx-make-copy', label: 'Make a copy…', icon: <CopyPlus/>, keywords: 'duplicate clone', run: () => { close(); handlers.onCreateIssueWith?.(copyContext(first)) } }] : []),
    { id: 'ctx-team', label: 'Move to team…', icon: <Users/>, keywords: 'transfer', page: { id: 'team', label: 'Move to team…' } },
    { id: 'ctx-archive', label: 'Archive', icon: <Archive/>, keywords: 'close', run: choose(() => apply({ archived: true })) },
    ...(handlers.onDeleteIssues || (single && context.onDelete) ? [{ id: 'ctx-delete', label: 'Delete', icon: <Trash2/>, shortcut: ['⌘', '⌫'], keywords: 'remove', run: choose(async () => {
      const title = single ? `${t('Delete')} ${refs[0].identifier}?` : `${t('Delete')} ${ids.length} ${t('issues')}?`
      if (!await confirmAction(title, { description: t('This cannot be undone.'), confirmLabel: t('Delete') })) return
      if (single && context.onDelete) await context.onDelete().catch(() => undefined)
      else await handlers.onDeleteIssues?.(ids).catch(() => undefined)
    }) }] : []),
  ]

  const options = page ? issuePageOptions() : []
  return { heading: single ? 'Issue' : 'Issues', actions, options }

  function issuePageOptions(): PageOption[] {
    if (!page) return []
    if (page.id === 'status') {
      const current = same(issue => issue.state.id)
      return statesFor(data, oneTeam).map(state => ({ id: state.id, label: state.name, icon: <StatusIcon state={state}/>, current: current === state.id, select: choose(() => apply({ stateId: state.id })) }))
    }
    if (page.id === 'priority') {
      const current = same(issue => issue.priority)
      return PRIORITIES.map(priority => ({ id: String(priority.value), label: priority.label, icon: <PriorityIcon priority={priority.value}/>, current: current === priority.value, select: choose(() => apply({ priority: priority.value })) }))
    }
    if (page.id === 'assignee') {
      const current = same(issue => issue.assignee?.id ?? '')
      return [
        { id: '', label: 'No assignee', icon: <NoAssigneeIcon/>, keywords: 'unassign none', current: current === '', select: choose(() => apply({ assigneeId: '' })) },
        ...assigneeCandidates(data.users.filter(user => user.active !== false), oneTeam).map(user => ({ id: user.id, label: user.displayName || user.name, entity: true, keywords: `${user.name} ${user.email ?? ''}${user.app ? ' agent' : ''}`, icon: <UserAvatar className="command-avatar" avatarUrl={user.avatarUrl} name={user.displayName || user.name}/>, ...(user.app ? { detail: <AgentBadge/> } : {}), current: user.app ? issues.every(issue => issue.delegate?.id === user.id) : current === user.id, select: choose(() => apply(user.app ? { delegateId: user.id } : { assigneeId: user.id })) })),
      ]
    }
    if (page.id === 'labels') {
      const labels = labelsFor(data, teamIds)
      const groups = new Map(data.labelGroups.map(group => [group.id, group.name]))
      return labels.map(label => {
        const count = issues.filter(issue => issue.labels.some(item => item.id === label.id)).length
        const checked = count > 0 && count === issues.length ? true : count > 0 ? 'mixed' as const : false
        return { id: label.id, label: label.name, entity: true, icon: <span className="option-dot" style={{ background: label.color }}/>, detail: label.groupId ? groups.get(label.groupId) : undefined, keywords: label.groupId ? groups.get(label.groupId) : undefined, checked,
          select: () => applyEach(issue => ({ labelIds: setGroupedLabelSelected(issue.labels.map(item => item.id), label.id, labels, checked !== true) })) }
      })
    }
    if (page.id === 'dueDate') return datePageOptions(query, same(issue => issue.dueDate ?? ''), issues.some(issue => issue.dueDate) && !issues.some(issue => issue.recurrence), value => choose(() => apply({ dueDate: value })), t)
    if (page.id === 'project') {
      const current = same(issue => issue.project?.id ?? '')
      return [
        { id: '', label: 'No project', icon: <NoProjectIcon/>, keywords: 'remove none', current: current === '', select: choose(() => apply({ projectId: '' })) },
        ...data.projects.filter(project => !project.archivedAt).map(project => ({ id: project.id, label: project.name, entity: true, icon: <ProjectIcon style={{ color: project.color }}/>, current: current === project.id, select: choose(() => apply({ projectId: project.id })) })),
      ]
    }
    if (page.id === 'cycle') {
      const current = same(issue => issue.cycleId ?? '')
      return [
        { id: '', label: 'No cycle', icon: <CycleIcon noCycle/>, keywords: 'remove none', current: current === '', select: choose(() => apply({ cycleId: '' })) },
        ...cycles.map(cycle => ({ id: cycle.id, label: cycle.name, entity: true, icon: <CycleIcon cycle={cycle}/>, current: current === cycle.id, select: choose(() => apply({ cycleId: cycle.id })) })),
      ]
    }
    if (page.id === 'estimate') {
      const current = same(issue => issue.estimate ?? -1)
      return estimatePickerOptions(teamSettings).map(option => ({ id: option.id, label: option.label, icon: option.value > 0 ? <span className="command-estimate">{option.value}</span> : <CircleDashed/>, current: current === option.value, select: choose(() => apply({ estimate: option.value })) }))
    }
    if (page.id === 'team') {
      return data.teams.filter(team => !team.archivedAt).map(team => ({ id: team.id, label: team.name, entity: true, keywords: team.key, icon: <TeamIcon team={team}/>, current: oneTeam === team.id, select: choose(() => apply({ teamId: team.id })) }))
    }
    if (page.id === 'relation') return RELATION_PAGES.map(relation => ({ id: relation.type, label: relation.label, icon: relation.icon, page: { id: 'relatedIssue', label: relation.label, relationType: relation.type } }))
    const targets = candidates.filter(issue => !ids.includes(issue.id)).slice(0, 50)
    const pick = (select: (target: Issue) => Promise<unknown>) => targets.map(target => ({ id: target.id, label: `${target.identifier} ${target.title}`, entity: true, icon: <StatusIcon state={target.state}/>, select: choose(() => select(target)) }))
    if (page.id === 'parent') return pick(target => apply({ parentId: target.id }))
    if (page.id === 'subIssue') return pick(target => handlers.onUpdateIssue ? guard(handlers.onUpdateIssue(target.id, { parentId: ids[0] })) : Promise.resolve())
    if (page.id === 'relatedIssue' && page.relationType) {
      const type = page.relationType
      return pick(target => Promise.all(ids.map(id => single && context.onRelation ? context.onRelation(type, target.id) : handlers.onCreateRelation?.(id, type, target.id))).catch(() => undefined))
    }
    return []
  }
}

function projectCommands(context: Extract<CommandContext, { kind: 'project' }>, data: BootstrapData, page: CommandPage | undefined, query: string, close: () => void, t: (value: string) => string) {
  const { project } = context
  const update = (input: Parameters<typeof context.onUpdate>[0]) => () => { close(); return context.onUpdate(input).catch(() => undefined) }
  const actions: ContextAction[] = [
    { id: 'ctx-project-status', label: 'Change project status…', icon: <ProjectStatusIcon color={project.status.color} name={project.status.name} type={project.status.type}/>, page: { id: 'projectStatus', label: 'Change project status…' } },
    { id: 'ctx-project-lead', label: 'Change project lead…', icon: <UserRound/>, keywords: 'owner', page: { id: 'projectLead', label: 'Change project lead…' } },
    { id: 'ctx-project-target', label: 'Set target date…', icon: <CalendarRange/>, keywords: 'deadline due', page: { id: 'projectTargetDate', label: 'Set target date…' } },
    { id: 'ctx-project-initiative', label: 'Add to initiative…', icon: <Lightbulb/>, page: { id: 'projectInitiative', label: 'Add to initiative…' } },
    { id: 'ctx-project-url', label: 'Copy project URL', icon: <Clipboard/>, shortcut: ['⌘', '⇧', ','], keywords: 'link', run: () => { close(); return copyText(`${location.origin}${projectPath(data.workspace.urlKey, project)}`, t('Copied to clipboard')) } },
  ]
  if (data.viewerRole !== 'guest' && workspaceFeatureEnabled(data.workspaceSettings.featureFlags, 'pulse')) {
    const subscribed = sessionPulseChoice('project', project.id) ?? projectPulseSubscribed(project, { viewerId: data.viewer.id, subscriptions: data.subscriptions, teamMembers: data.teamMembers, projects: data.projects, initiatives: data.initiatives })
    actions.push({ id: 'ctx-project-pulse', label: 'Subscribe to project updates in Pulse', icon: <PulseIcon size={14}/>, keywords: 'feed pulse updates', checked: subscribed, run: () => { close(); return changePulseSubscription('project', project.id, !subscribed).catch(() => toast.error(t('Could not update subscription'))) } })
  }
  let options: PageOption[] = []
  if (page?.id === 'projectStatus') options = [...data.projectStatuses].sort((a, b) => (a.position ?? 0) - (b.position ?? 0)).map(status => ({ id: status.id, label: status.name, icon: <ProjectStatusIcon color={status.color} name={status.name} type={status.type}/>, current: project.status.id === status.id, select: update({ statusId: status.id }) }))
  if (page?.id === 'projectLead') options = [
    { id: '', label: 'No lead', icon: <NoAssigneeIcon/>, current: !project.lead, select: update({ leadId: '' }) },
    ...data.users.filter(user => user.active !== false).map(user => ({ id: user.id, label: user.displayName || user.name, entity: true, keywords: `${user.name} ${user.email ?? ''}`, icon: <UserAvatar className="command-avatar" avatarUrl={user.avatarUrl} name={user.displayName || user.name}/>, current: project.lead?.id === user.id, select: update({ leadId: user.id }) })),
  ]
  if (page?.id === 'projectTargetDate') options = datePageOptions(query, project.targetDate ?? '', Boolean(project.targetDate), value => update({ targetDate: value, targetDateResolution: '' }), t, true)
  if (page?.id === 'projectInitiative') options = data.initiatives.map(initiative => {
    const checked = project.initiatives.includes(initiative.id)
    return { id: initiative.id, label: initiative.name, entity: true, icon: <Lightbulb/>, checked, select: () => context.onUpdate({ initiatives: checked ? project.initiatives.filter(id => id !== initiative.id) : [...project.initiatives, initiative.id] }).catch(() => undefined) }
  })
  return { heading: 'Project', actions, options }
}

function datePageOptions(query: string, current: string | undefined, hasValue: boolean, select: (value: string) => () => Promise<unknown>, t: (value: string) => string, projectDates = false): PageOption[] {
  const today = new Date()
  const presets = projectDates
    ? [{ label: 'End of this week', date: endOfWorkWeek(today) }, { label: 'End of this month', date: new Date(today.getFullYear(), today.getMonth() + 1, 0) }, { label: 'End of this quarter', date: new Date(today.getFullYear(), Math.floor(today.getMonth() / 3) * 3 + 3, 0) }, { label: 'End of next quarter', date: new Date(today.getFullYear(), Math.floor(today.getMonth() / 3) * 3 + 6, 0) }]
    : [{ label: 'Tomorrow', date: addDays(today, 1) }, { label: 'End of this week', date: endOfWorkWeek(today) }, { label: 'In one week', date: addDays(today, 7) }, { label: 'In two weeks', date: addDays(today, 14) }]
  const typed = query.trim() ? parseDate(query.trim(), today, { forwardDate: true }) : null
  const options: PageOption[] = presets.map(preset => { const value = isoDate(preset.date); return { id: preset.label, label: preset.label, icon: <CalendarIcon/>, detail: formatShortDate(value), current: current === value, select: select(value) } })
  if (typed) { const value = isoDate(typed); options.unshift({ id: `custom-${value}`, label: `${t('Set to')} ${formatShortDate(value)}`, icon: <CalendarRange/>, forceMount: true, select: select(value) }) }
  if (hasValue) options.push({ id: 'remove', label: projectDates ? 'Remove target date' : 'Remove due date', icon: <X/>, keywords: 'clear none', select: select('') })
  return options
}

function statesFor(data: BootstrapData, teamId: string | undefined): WorkflowState[] {
  const resolve = (id: string, seen = new Set<string>()): string => { const settings = data.teamSettings?.[id]; return settings?.inheritWorkflowStatuses && settings.parentTeamId && !seen.has(id) ? resolve(settings.parentTeamId, seen.add(id)) : id }
  const scope = teamId ? resolve(teamId) : undefined
  const scoped = scope && data.states.some(state => state.teamId === scope) ? data.states.filter(state => state.teamId === scope) : data.states.filter(state => !state.teamId)
  return (scoped.length ? scoped : data.states).slice().sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
}

function labelsFor(data: BootstrapData, teamIds: string[]) {
  const scopes = new Set(teamIds.flatMap(teamId => labelTeamScopeIds(teamId, data.teams, data.teamSettings)))
  return labelsForResource(data.labels, 'issue', data.labelGroups).filter(label => !label.scope || label.scope === 'Workspace' || scopes.has(label.scope))
}

function copyContext(issue: Issue): MyIssuesCreateContext {
  return { title: issue.title, description: issue.description, teamId: issue.team.id, stateId: issue.state.id, priority: Math.max(0, Math.min(4, issue.priority)) as MyIssuesCreateContext['priority'], assigneeId: issue.assignee?.id, projectId: issue.project?.id, projectMilestoneId: issue.projectMilestoneId, cycleId: issue.cycleId, labelIds: issue.labels.map(label => label.id), parentId: issue.parentId }
}

async function copyText(text: string, message: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success(message)
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Could not write to clipboard')
  }
}

function addDays(date: Date, days: number) { const next = new Date(date); next.setDate(next.getDate() + days); return next }
function endOfWorkWeek(date: Date) { return addDays(date, (5 - date.getDay() + 7) % 7) }
function isoDate(date: Date) { const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000); return local.toISOString().slice(0, 10) }
function formatShortDate(value: string) { const [year, month, day] = value.split('-').map(Number); return new Date(year, month - 1, day).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) }

