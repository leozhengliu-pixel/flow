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

/** An issue is in Triage when its team has triage enabled, it sits in a backlog-type state and was never triaged. */
export function isIssueInTriage(
  issue: Pick<Issue, 'team' | 'state' | 'triagedAt'>,
  teamSettings: Record<string, { triageEnabled?: boolean } | undefined> | undefined,
) {
  return Boolean(teamSettings?.[issue.team.id]?.triageEnabled && issue.state.type === 'backlog' && !issue.triagedAt)
}

/** Pseudo workflow state used to display Linear's orange "Triage" status glyph. */
export const TRIAGE_STATUS = { id: 'triage', name: 'Triage', color: 'var(--inbox-status-triage)', type: 'backlog' } as const

/** Linear triage shortcuts: 1 Accept, 2 Decline, 3 (or M M) Mark as duplicate, H Snooze. */
export const TRIAGE_SHORTCUTS = { accept: '1', decline: '2', duplicate: '3', snooze: 'H' } as const
export type TriageActionKind = keyof typeof TRIAGE_SHORTCUTS

/** Team statuses ordered by position (team-scoped plus workspace-wide). */
export function teamStates(states: WorkflowState[], teamId: string) {
  return states.filter(state => state.teamId === teamId || !state.teamId).sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
}

/** Status an accepted triage issue moves to: the team default when it is not a triage/backlog state, else the first unstarted. */
export function resolveAcceptState(states: WorkflowState[], defaultStateId?: string): WorkflowState | undefined {
  if (defaultStateId) {
    const configured = states.find(state => state.id === defaultStateId && state.type !== 'backlog')
    if (configured) return configured
  }
  return (
    states.find(state => state.type === 'unstarted') ??
    states.find(state => state.type === 'started') ??
    states.find(state => state.type !== 'backlog')
  )
}
