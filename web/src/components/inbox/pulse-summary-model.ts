import { fetchPulseCapabilities } from '@/lib/api'
import type { BootstrapData, Notification, PulseCapabilities } from '@/types/flow'

/** A Pulse summary schedule the viewer can pick ("Pulse frequency"). */
export type PulseFrequency = 'daily' | 'weekly' | 'never'
export const PULSE_FREQUENCIES: Array<{ value: PulseFrequency; label: string }> = [
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
  { value: 'never', label: 'Never' },
]

export function isPulseSummaryNotification(notification: Pick<Notification, 'type' | 'category'>) {
  return notification.type === 'pulseSummary' || notification.category === 'pulse'
}

/** Personal schedule, else the workspace default (which the server treats as Daily when unset). */
export function effectivePulseFrequency(data: Pick<BootstrapData, 'userSettings' | 'workspaceSettings' | 'viewer'>): PulseFrequency {
  const personal = data.userSettings?.[data.viewer.id]?.pulseSchedule
  if (personal === 'daily' || personal === 'weekly' || personal === 'never') return personal
  const workspace = data.workspaceSettings?.featureSettings?.pulseWorkspaceSchedule
  return workspace === 'weekly' || workspace === 'never' ? workspace : 'daily'
}

/**
 * Linear's `getTitle`: "Daily Pulse" / "Weekly Pulse" from the schedule that
 * produced the summary, otherwise "Pulse". Server-provided titles win; older
 * records fall back to the payload schedule, then the viewer's schedule.
 */
export function pulseSummaryTitle(notification: Pick<Notification, 'title' | 'payload'>, fallbackSchedule?: PulseFrequency) {
  if (notification.title?.trim()) return notification.title.trim()
  const schedule = notification.payload?.schedule ?? fallbackSchedule
  if (schedule === 'daily') return 'Daily Pulse'
  if (schedule === 'weekly') return 'Weekly Pulse'
  return 'Pulse'
}

/** "Update from {X}" / "{a}, {b} and N other updates" from the server, else "{n} updates". */
export function pulseSummaryText(notification: Pick<Notification, 'text' | 'payload' | 'occurrenceCount'>) {
  if (notification.text?.trim()) return notification.text.trim()
  const payload = notification.payload as { updateIds?: string[]; total?: number } | undefined
  const count = (typeof payload?.total === 'number' && payload.total > 0 ? payload.total : payload?.updateIds?.length) || notification.occurrenceCount || 0
  return `${count} ${count === 1 ? 'update' : 'updates'}`
}

/* "Pulse display options": Summaries (default) or Updates, remembered per user. */
export type PulseSummaryDisplay = 'summaries' | 'updates'
const DISPLAY_KEY = 'flow.pulse.summaryDisplay'

export function readPulseSummaryDisplay(userId: string): PulseSummaryDisplay {
  try {
    const stored = JSON.parse(window.localStorage.getItem(DISPLAY_KEY) ?? '{}') as Record<string, unknown>
    return stored[userId] === 'updates' ? 'updates' : 'summaries'
  } catch {
    return 'summaries'
  }
}

export function writePulseSummaryDisplay(userId: string, display: PulseSummaryDisplay) {
  try {
    const stored = JSON.parse(window.localStorage.getItem(DISPLAY_KEY) ?? '{}') as Record<string, unknown>
    window.localStorage.setItem(DISPLAY_KEY, JSON.stringify({ ...(stored && typeof stored === 'object' ? stored : {}), [userId]: display }))
  } catch {
    // Storage unavailable: the in-memory choice still applies for this session.
  }
}

/* Capabilities are workspace configuration; fetch once per page load. */
let capabilities: Promise<PulseCapabilities> | undefined

export function loadPulseCapabilities(): Promise<PulseCapabilities> {
  capabilities ??= fetchPulseCapabilities().catch(() => {
    capabilities = undefined
    return { aiSummaries: false, audio: false }
  })
  return capabilities
}

/** Test hook: forget the cached capabilities. */
export function resetPulseCapabilitiesCache() {
  capabilities = undefined
}
