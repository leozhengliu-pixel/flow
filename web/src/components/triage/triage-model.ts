import type { Issue, WorkflowState } from '@/types/flow'

export function canceledState(states: WorkflowState[], teamId: string) {
  const scoped = states.filter(state => !state.teamId || state.teamId === teamId)
  return scoped.find(state => state.type === 'canceled' && !/duplicate/i.test(state.name)) ?? scoped.find(state => state.type === 'canceled')
}

export function duplicateState(states: WorkflowState[], teamId: string) {
  return states.find(state => (!state.teamId || state.teamId === teamId) && state.type === 'canceled' && /duplicate/i.test(state.name))
}

export function snoozePresets(now = new Date()) {
  const at = (days: number) => { const date = new Date(now); date.setDate(date.getDate() + days); date.setHours(8, 0, 0, 0); return date }
  const monday = at(((8 - now.getDay()) % 7) || 7)
  return [
    { label: 'Tomorrow', until: at(1) },
    { label: 'Next week', until: monday },
    { label: 'In two weeks', until: at(14) },
    { label: 'In a month', until: at(30) },
  ]
}

export function isSnoozed(issue: Pick<Issue, 'snoozedUntil'>, now = Date.now()) {
  return Boolean(issue.snoozedUntil && Date.parse(issue.snoozedUntil) > now)
}
