import { describe, expect, it } from 'vitest'
import type { Subscription } from '@/types/flow'
import { isActiveSubscription, subscriptionAfterGenericDelete } from './subscription-records'

const record = (events?: string[], optOutEvents?: string[]): Subscription => ({ id: 's', userId: 'u', resourceType: 'project', resourceId: 'p', events, optOutEvents, createdAt: '' })

describe('subscription records', () => {
  it('treats an opt-out-only record as not subscribed', () => {
    expect(isActiveSubscription(record([], ['pulse']))).toBe(false)
    expect(isActiveSubscription(record(undefined, ['pulse']))).toBe(false)
    expect(isActiveSubscription(record(['pulse']))).toBe(true)
    expect(isActiveSubscription(record(['newUpdate'], ['pulse']))).toBe(true)
    expect(isActiveSubscription(record())).toBe(true)
    expect(isActiveSubscription(undefined)).toBe(false)
  })

  it('keeps what the server keeps after a generic DELETE', () => {
    // Plain subscription: gone.
    expect(subscriptionAfterGenericDelete(record(['newUpdate']), undefined)).toBeUndefined()
    // Explicit Pulse subscribe survives as events ["pulse"].
    expect(subscriptionAfterGenericDelete(record(['newUpdate', 'pulse']), undefined)?.events).toEqual(['pulse'])
    // A Pulse opt-out survives without events.
    expect(subscriptionAfterGenericDelete(record(['newUpdate'], ['pulse']), undefined)).toMatchObject({ events: [], optOutEvents: ['pulse'] })
    // The server's answer wins when it sends the kept record.
    const kept = record(['pulse'])
    expect(subscriptionAfterGenericDelete(record(['newUpdate']), kept)).toBe(kept)
    expect(subscriptionAfterGenericDelete(undefined, undefined)).toBeUndefined()
  })
})
