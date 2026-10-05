import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Initiative, Project, Subscription, User } from '@/types/flow'

const mocks = vi.hoisted(() => ({ setPulseSubscription: vi.fn(), request: vi.fn() }))
vi.mock('@/lib/api', () => ({ setPulseSubscription: mocks.setPulseSubscription }))
vi.mock('@/lib/api-client', () => ({ request: mocks.request }))

import {
  PULSE_SUBSCRIPTIONS_CHANGED_EVENT,
  explicitPulseChoice,
  initiativePulseSubscribed,
  projectPulseSubscribed,
  resetPulseSessionChoices,
  teamPulseSubscribed,
  usePulseSubscription,
} from './pulse-subscriptions'

const me = 'user-me'
const user = (id: string) => ({ id }) as User
const project = (overrides: Partial<Project> = {}) => ({ id: 'project-1', memberIds: [], teamIds: [], initiatives: [], ...overrides }) as Project
const initiative = (overrides: Partial<Initiative> = {}) => ({ id: 'initiative-1', projectIds: [], parentInitiativeIds: [], ...overrides }) as Initiative
const subscription = (resourceType: string, resourceId: string, events: string[] = [], optOutEvents?: string[]) =>
  ({ id: `${resourceType}-${resourceId}`, userId: me, resourceType, resourceId, events, optOutEvents, createdAt: '' }) as Subscription & { optOutEvents?: string[] }

beforeEach(() => { resetPulseSessionChoices(); mocks.setPulseSubscription.mockReset(); mocks.request.mockReset() })
afterEach(() => vi.restoreAllMocks())

describe('explicit Pulse choices', () => {
  it('reads the pulse event as a subscribe and optOutEvents as an unsubscribe', () => {
    expect(explicitPulseChoice(undefined)).toBeUndefined()
    expect(explicitPulseChoice(subscription('project', 'p', ['issueAdded']))).toBeUndefined()
    expect(explicitPulseChoice(subscription('project', 'p', ['pulse']))).toBe(true)
    expect(explicitPulseChoice(subscription('project', 'p', ['issueAdded'], ['pulse']))).toBe(false)
  })
})

describe('default subscription rules', () => {
  it('subscribes project leads and members by default', () => {
    expect(projectPulseSubscribed(project({ lead: user(me) }), { viewerId: me })).toBe(true)
    expect(projectPulseSubscribed(project({ memberIds: [me] }), { viewerId: me })).toBe(true)
    expect(projectPulseSubscribed(project(), { viewerId: me })).toBe(false)
  })

  it('subscribes owners of one of the project initiatives', () => {
    const owned = initiative({ owner: user(me), projectIds: ['project-1'] })
    expect(projectPulseSubscribed(project(), { viewerId: me, initiatives: [owned] })).toBe(true)
    expect(projectPulseSubscribed(project({ initiatives: ['initiative-2'] }), { viewerId: me, initiatives: [initiative({ id: 'initiative-2', owner: user(me) })] })).toBe(true)
    expect(projectPulseSubscribed(project(), { viewerId: me, initiatives: [initiative({ owner: user('someone') , projectIds: ['project-1'] })] })).toBe(false)
  })

  it('follows team project updates for the teams you belong to unless you opted out', () => {
    const teamMembers = [{ teamId: 'team-1', userId: me }]
    expect(teamPulseSubscribed('team-1', { viewerId: me, teamMembers })).toBe(true)
    expect(teamPulseSubscribed('team-2', { viewerId: me, teamMembers })).toBe(false)
    expect(teamPulseSubscribed('team-1', { viewerId: me, teamMembers, subscriptions: [subscription('team', 'team-1', [], ['pulse'])] })).toBe(false)
    expect(teamPulseSubscribed('team-2', { viewerId: me, teamMembers, subscriptions: [subscription('team', 'team-2', ['pulse'])] })).toBe(true)
    expect(projectPulseSubscribed(project({ teamIds: ['team-1'] }), { viewerId: me, teamMembers })).toBe(true)
    expect(projectPulseSubscribed(project({ teamIds: ['team-1'] }), { viewerId: me, teamMembers, subscriptions: [subscription('team', 'team-1', [], ['pulse'])] })).toBe(false)
  })

  it('lets an explicit project choice override every default', () => {
    expect(projectPulseSubscribed(project({ memberIds: [me] }), { viewerId: me, subscriptions: [subscription('project', 'project-1', ['issueAdded'], ['pulse'])] })).toBe(false)
    expect(projectPulseSubscribed(project(), { viewerId: me, subscriptions: [subscription('project', 'project-1', ['pulse'])] })).toBe(true)
    // Another user's record never applies.
    expect(projectPulseSubscribed(project(), { viewerId: me, subscriptions: [{ ...subscription('project', 'project-1', ['pulse']), userId: 'someone' }] })).toBe(false)
  })

  it('subscribes initiative owners and members of its projects, including sub-initiative projects', () => {
    const parent = initiative({ projectIds: ['project-1'] })
    const child = initiative({ id: 'initiative-2', parentInitiativeIds: ['initiative-1'], projectIds: ['project-2'] })
    const grandchild = initiative({ id: 'initiative-3', parentInitiativeIds: ['initiative-2'], projectIds: ['project-3'] })
    const initiatives = [parent, child, grandchild]
    expect(initiativePulseSubscribed(initiative({ owner: user(me) }), { viewerId: me })).toBe(true)
    expect(initiativePulseSubscribed(parent, { viewerId: me, initiatives, projects: [project({ memberIds: [me] })] })).toBe(true)
    expect(initiativePulseSubscribed(parent, { viewerId: me, initiatives, projects: [project({ id: 'project-3', lead: user(me) })] })).toBe(true)
    expect(initiativePulseSubscribed(parent, { viewerId: me, initiatives, projects: [project({ id: 'project-9', memberIds: [me] })] })).toBe(false)
    expect(initiativePulseSubscribed(initiative({ owner: user(me) }), { viewerId: me, subscriptions: [subscription('initiative', 'initiative-1', [], ['pulse'])] })).toBe(false)
  })
})

describe('usePulseSubscription', () => {
  it('toggles optimistically, records the session choice and notifies the Pulse feed', async () => {
    mocks.setPulseSubscription.mockResolvedValue({ subscribed: false })
    const events: unknown[] = []
    const listener = (event: Event) => events.push((event as CustomEvent).detail)
    window.addEventListener(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, listener)
    const { result } = renderHook(() => usePulseSubscription('project', 'project-1', true))
    expect(result.current.subscribed).toBe(true)
    await act(async () => { await result.current.toggle(false) })
    expect(mocks.setPulseSubscription).toHaveBeenCalledWith('project', 'project-1', false)
    expect(result.current.subscribed).toBe(false)
    expect(events).toEqual([{ type: 'project', id: 'project-1', subscribed: false }])
    // A second consumer (e.g. another menu) picks up the session choice.
    expect(renderHook(() => usePulseSubscription('project', 'project-1', true)).result.current.subscribed).toBe(false)
    window.removeEventListener(PULSE_SUBSCRIPTIONS_CHANGED_EVENT, listener)
  })

  it('rolls back and rethrows when the server rejects the change', async () => {
    mocks.setPulseSubscription.mockRejectedValue(new Error('Offline'))
    const { result } = renderHook(() => usePulseSubscription('initiative', 'initiative-1', false))
    await act(async () => { await expect(result.current.toggle(true)).rejects.toThrow('Offline') })
    expect(result.current.subscribed).toBe(false)
    expect(result.current.saving).toBe(false)
  })

  it('uses the server state while refreshing', async () => {
    mocks.request.mockResolvedValue({ subscribed: true, explicit: true })
    const { result, rerender } = renderHook(({ refresh }) => usePulseSubscription('team', 'team-1', false, refresh), { initialProps: { refresh: false } })
    expect(mocks.request).not.toHaveBeenCalled()
    rerender({ refresh: true })
    await waitFor(() => expect(result.current.subscribed).toBe(true))
    expect(mocks.request).toHaveBeenCalledWith('/api/pulse/subscriptions/team/team-1', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })
})
