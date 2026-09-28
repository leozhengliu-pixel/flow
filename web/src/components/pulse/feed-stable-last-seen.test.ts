import { describe, expect, it, beforeEach } from 'vitest'
import { FeedStableLastSeen } from './feed-stable-last-seen'

describe('FeedStableLastSeen (LS-0270)', () => {
  beforeEach(() => {
    FeedStableLastSeen.clear()
  })

  it('publishes, reads effective time, and clears', () => {
    expect(FeedStableLastSeen.published).toBeNull()
    expect(FeedStableLastSeen.effectiveLastSeenTime('2026-01-01T00:00:00.000Z')).toBe(
      +new Date('2026-01-01T00:00:00.000Z'),
    )
    FeedStableLastSeen.publish('2026-09-20T08:00:00.000Z')
    expect(FeedStableLastSeen.published).toBe(+new Date('2026-09-20T08:00:00.000Z'))
    expect(FeedStableLastSeen.effectiveLastSeenTime('2026-01-01T00:00:00.000Z')).toBe(
      +new Date('2026-09-20T08:00:00.000Z'),
    )
    FeedStableLastSeen.clear()
    expect(FeedStableLastSeen.published).toBeNull()
  })
})
