import type { ApplicationActivity } from '@/lib/application-agents'

export type AgentThreadItem = { id: string; kind: 'message' | 'thinking' | 'action'; activity: ApplicationActivity; body: string; output?: boolean; selected?: string; resolved?: boolean }

export function agentThreadItems(activities: ApplicationActivity[]): AgentThreadItem[] {
  let items: AgentThreadItem[] = []
  let turn = 0
  let pending: AgentThreadItem | undefined
  for (let index = 0; index < activities.length; index++) {
    const activity = activities[index]
    if (activity.type === 'feedback') continue
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
    items.push(item)
    if (activity.type === 'elicitation') pending = item
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
