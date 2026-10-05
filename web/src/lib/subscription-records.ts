import type { Subscription } from '@/types/flow'

type SubscriptionLike = Pick<Subscription, 'events' | 'optOutEvents'>

/**
 * Whether a subscription record means "subscribed". A record that only
 * carries opt-outs (a Pulse unsubscribe: no events, optOutEvents present) is
 * kept by the server to override the default Pulse rules — it is not a
 * subscription. Legacy records without events or opt-outs still count.
 */
export function isActiveSubscription(record: SubscriptionLike | undefined): boolean {
  if (!record) return false
  return !(record.optOutEvents?.length && !record.events?.length)
}

/**
 * The record left after a generic DELETE /api/subscriptions/{type}/{id}: the
 * server keeps the Pulse part (events ["pulse"]) and any Pulse opt-out, and
 * returns the kept record when it sends one. Undefined = the record is gone.
 */
export function subscriptionAfterGenericDelete<T extends Subscription>(local: T | undefined, response: unknown): T | undefined {
  if (response && typeof response === 'object' && 'resourceId' in response) return response as T
  if (!local) return undefined
  const pulse = local.events?.includes('pulse') ?? false
  if (!pulse && !local.optOutEvents?.length) return undefined
  return { ...local, events: pulse ? ['pulse'] : [] }
}
