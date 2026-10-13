import type { ApplicationActivity } from '@/lib/application-agents'
import { splitAgentSuggestions } from './agent-answer-content'
import { toolStatusLabel } from './agent-step-labels'

export type AgentThreadItem = { id: string; kind: 'message' | 'thinking' | 'action'; activity: ApplicationActivity; body: string; output?: boolean; selected?: string; resolved?: boolean }

export function agentThreadItems(activities: ApplicationActivity[]): AgentThreadItem[] {
  let items: AgentThreadItem[] = []
  let turn = 0
  let pending: AgentThreadItem | undefined
  // Replies a person deleted from the thread (the reference app's comment menu "Delete").
  const deleted = new Set(activities.filter(activity => activity.type === 'deleted' && activity.metadata?.activityId).map(activity => activity.metadata!.activityId!))
  for (let index = 0; index < activities.length; index++) {
    const activity = activities[index]
    if (activity.type === 'feedback' || activity.type === 'deleted') continue
    if (activity.type === 'prompt' || activity.type === 'retry') {
      turn = items.length
      const selected = pending?.activity.signal === 'select' && pending.activity.signalMetadata?.options?.find(option => option.value === activity.body || activity.body.startsWith(`${option.value}\n\n`))
      if (pending) { pending.resolved = true; pending.selected = selected ? selected.value : activity.body; pending = undefined }
      const body = selected ? activity.body.slice(selected.value.length).trim() : activity.body
      if (body || activity.type === 'retry') items.push({ id: activity.id, kind: 'message', activity, body: body || 'Retry requested' })
      continue
    }
    if (activity.type === 'thought') {
      const last = items.at(-1)
      if (last?.kind === 'thinking') last.body += activity.body
      else items.push({ id: activity.id, kind: 'thinking', activity, body: activity.body })
      continue
    }
    if (activity.type === 'output') {
      const last = items.at(-1)
      if (last?.output) last.body += activity.body
      else items.push({ id: activity.id, kind: 'message', activity, body: activity.body, output: true })
      continue
    }
    if (activity.type === 'canceled') {
      if (pending) { pending.resolved = true; pending = undefined }
      items.push({ id: activity.id, kind: 'action', activity, body: activity.body || 'Stop requested' })
      continue
    }
    if (['response', 'elicitation'].includes(activity.type)) items = items.filter((item, position) => position < turn || !item.output)
    if (pending) { pending.resolved = true; pending = undefined }
    const item: AgentThreadItem = { id: activity.id, kind: activity.type === 'action' ? 'action' : 'message', activity, body: activity.body }
    if (deleted.has(activity.id)) continue
    items.push(item)
    if (activity.type === 'elicitation') pending = item
  }
  // Comment threads have no suggestion chips (the reference app's threads neither): an agent's trailing ```suggestions block,
  // which only the chat panel turns into chips, is dropped instead of showing as a code block.
  for (const item of items) {
    if (item.kind === 'message' && !['prompt', 'retry', 'canceled'].includes(item.activity.type)) item.body = splitAgentSuggestions(item.body).prose
  }
  return items
}

export function agentThreadReactions(activities: ApplicationActivity[]): Record<string, Record<string, string[]>> {
  const reactions: Record<string, Record<string, string[]>> = Object.create(null)
  for (const activity of activities) {
    const target = activity.metadata?.activityId
    if (activity.type !== 'feedback' || !target || !activity.actorId) continue
    const group = reactions[target] ??= Object.create(null)
    const users = new Set(group[activity.body] ?? [])
    if (activity.metadata?.enabled) users.add(activity.actorId)
    else users.delete(activity.actorId)
    if (users.size) group[activity.body] = [...users]
    else delete group[activity.body]
  }
  return reactions
}

/**
 * The phase shown on a working agent's row (the reference app: "Working…", then what it is doing, e.g. "Looking at issues…"):
 * the newest step of the current turn, as a running label.
 */
export function agentWorkingPhase(activities: ApplicationActivity[], t: (source: string) => string) {
  const turn = activities.findLastIndex(activity => activity.type === 'prompt' || activity.type === 'retry')
  const action = activities.slice(turn + 1).findLast(activity => activity.type === 'action')
  const name = (action?.metadata?.action || action?.body || '').trim()
  if (!name || /^(Approved action|Action rejected):/.test(name)) return t('Working…')
  if (/^[a-z][a-z0-9_.]*$/.test(name)) return toolStatusLabel(name, true, t)
  return `${name.replace(/[.…]+$/, '')}…`
}


/**
 * How many messages a comment thread shows for one agent session (the agent's replies and the people's replies to it),
 * for the thread's "N comments" count. A reply still streaming is not counted until it lands.
 */
export function agentThreadMessageCount(activities: ApplicationActivity[], status: string) {
  const generating = status === 'pending' || status === 'active'
  return agentThreadItems(activities).filter(item => item.kind === 'message' && !(generating && item.output)).length
}
