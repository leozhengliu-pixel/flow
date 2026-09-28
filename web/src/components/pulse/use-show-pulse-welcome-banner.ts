import type { BootstrapData } from '@/types/flow'

export const PULSE_WELCOME_DISMISSED_FLAG = 'pulseWelcomeDismissed'
export const HIDE_FEED_WELCOME_BANNER_FLAG = 'hide-feed-welcome-banner'

/**
 * LS-0767 — useShowPulseWelcomeBanner.
 * Show when user has not dismissed AND feature flag hide-feed-welcome-banner is off.
 */
export function useShowPulseWelcomeBanner(data: BootstrapData, options?: { isTeamView?: boolean }) {
  if (options?.isTeamView) return false
  const settings = data.userSettings[data.viewer.id]
  const dismissed = Boolean(settings?.pulseWelcomeDismissed)
  const hide =
    data.workspaceSettings.featureFlags?.[HIDE_FEED_WELCOME_BANNER_FLAG] === true ||
    data.workspaceSettings.featureFlags?.hideFeedWelcomeBanner === true
  return !dismissed && !hide
}

export function pulseWelcomeBannerCopy() {
  return {
    title: 'Welcome to Pulse',
    body: 'Your feed to keep up with your company’s product work, tailored to your needs and interests.',
    scheduleLabel: 'Choose your summary notification frequency',
    scheduleHelp: 'Summary of Pulse updates delivered straight to your Inbox',
    confirm: 'Confirm',
  } as const
}
