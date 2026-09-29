import { describe, expect, it } from 'vitest'
import type { ActivityEvent, WorkflowState } from '@/types/flow'
import { formatStatusDuration, timeInStatus } from './time-in-status'

const states = [
  { id: 'backlog', name: 'Backlog', type: 'backlog', color: '#bec2c8', position: 0 },
  { id: 'todo', name: 'Todo', type: 'unstarted', color: '#e2e2e2', position: 1 },
  { id: 'progress', name: 'In Progress', type: 'started', color: '#f2c94c', position: 2 },
  { id: 'done', name: 'Done', type: 'completed', color: '#5e6ad2', position: 3 },
] as WorkflowState[]
const change = (at: string, from: WorkflowState, to: WorkflowState) => ({
  id: at, type: 'issue.updated', createdAt: at, actor: { id: 'u' },
  metadata: { stateBefore: from.name, stateBeforeId: from.id, stateBeforeType: from.type, state: to.name, stateId: to.id, stateType: to.type },
}) as unknown as ActivityEvent
const [backlog, todo, progress, done] = states
const t = (seconds: number) => new Date(Date.UTC(2026, 8, 28, 10) + seconds * 1000).toISOString()

describe('timeInStatus', () => {
  it('sums time per status across revisits and orders them like the workflow', () => {
    const stints = timeInStatus({
      createdAt: t(0),
      current: backlog,
      states,
      now: Date.parse(t(100)),
      activities: [change(t(40), backlog, done), change(t(10), backlog, todo), change(t(30), progress, backlog), change(t(12), todo, progress), change(t(45), done, backlog)],
    })
    expect(stints.map(stint => [stint.name, stint.ms / 1000, stint.current])).toEqual([
      ['Backlog', 10 + 10 + 55, true],
      ['Todo', 2, false],
      ['In Progress', 18, false],
      ['Done', 5, false],
    ])
  })

  it('counts time in the triage queue as Triage, like Linear', () => {
    const stints = timeInStatus({ createdAt: t(0), current: todo, states, triagedAt: t(3600), now: Date.parse(t(3700)), activities: [change(t(3600), backlog, todo)] })
    expect(stints.map(stint => [stint.name, stint.ms / 1000, stint.current])).toEqual([['Triage', 3600, false], ['Todo', 100, true]])
  })

  it('shows the current status even without history', () => {
    const stints = timeInStatus({ createdAt: t(0), current: progress, states, now: Date.parse(t(7200)), activities: [] })
    expect(stints).toMatchObject([{ name: 'In Progress', ms: 7_200_000, current: true }])
  })
})

describe('formatStatusDuration', () => {
  it('uses Linear compact units', () => {
    expect([6e3, 125e3, 4 * 3600e3, 3 * 86400e3, 15 * 86400e3, 70 * 86400e3, 400 * 86400e3].map(formatStatusDuration)).toEqual(['6s', '2m', '4h', '3d', '2w', '2mo', '1y'])
  })
})

describe('timeInStatus with incomplete history', () => {
  it('merges entries that name a status without its id', () => {
    const named = { ...change(t(10), backlog, todo), metadata: { stateBefore: 'Backlog', state: 'Todo' } } as unknown as ActivityEvent
    const stints = timeInStatus({ createdAt: t(0), current: todo, states, now: Date.parse(t(40)), activities: [named] })
    expect(stints.map(stint => [stint.name, stint.ms / 1000])).toEqual([['Backlog', 10], ['Todo', 30]])
  })
})
