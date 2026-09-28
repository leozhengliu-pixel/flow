import { describe, expect, it } from 'vitest'
import type { BootstrapData } from '@/types/flow'
import { useShowPulseWelcomeBanner } from './use-show-pulse-welcome-banner'

function data(overrides?: {
  dismissed?: boolean
  hide?: boolean
}): BootstrapData {
  return {
    viewer: { id: 'user-1' },
    userSettings: {
      'user-1': {
        userId: 'user-1',
        pulseWelcomeDismissed: overrides?.dismissed,
      },
    },
    workspaceSettings: {
      featureFlags: {
        'hide-feed-welcome-banner': overrides?.hide === true,
      },
    },
  } as unknown as BootstrapData
}

describe('useShowPulseWelcomeBanner (LS-0767)', () => {
  it('shows when not dismissed and flag off', () => {
    expect(useShowPulseWelcomeBanner(data())).toBe(true)
  })
  it('hides when dismissed', () => {
    expect(useShowPulseWelcomeBanner(data({ dismissed: true }))).toBe(false)
  })
  it('hides when feature flag set', () => {
    expect(useShowPulseWelcomeBanner(data({ hide: true }))).toBe(false)
  })
  it('hides on team view', () => {
    expect(useShowPulseWelcomeBanner(data(), { isTeamView: true })).toBe(false)
  })
})
