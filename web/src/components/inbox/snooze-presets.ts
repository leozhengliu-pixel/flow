import type { InboxSnoozePreset } from './notification-row'

/** The snooze presets that apply right now: "This evening" (18:00) only while it is still the working day. */
export function snoozePresetItems(now: Date): Array<[Exclude<InboxSnoozePreset, { kind: 'custom' }>, string]> {
  return [
    ['hour', 'An hour from now'],
    ['threeHours', 'In 3 hours'],
    ...(now.getHours() < 17 ? [['evening', 'This evening'] as [Exclude<InboxSnoozePreset, { kind: 'custom' }>, string]] : []),
    ['tomorrow', 'Tomorrow'],
    ['nextWeek', 'Next week'],
    ['month', 'A month from now'],
  ]
}

export function resolveSnoozeDate(now: Date, preset: Exclude<InboxSnoozePreset, { kind: 'custom' }>) {
  const next = new Date(now)
  if (preset === 'hour') return new Date(next.getTime() + 60 * 60 * 1000)
  if (preset === 'threeHours') return new Date(next.getTime() + 3 * 60 * 60 * 1000)
  next.setSeconds(0, 0)
  if (preset === 'evening') { next.setHours(18, 0, 0, 0); return next }
  next.setHours(9, 0, 0, 0)
  if (preset === 'tomorrow') next.setDate(next.getDate() + 1)
  else if (preset === 'nextWeek') next.setDate(next.getDate() + ((8 - next.getDay()) % 7 || 7))
  else if (preset === 'month') next.setMonth(next.getMonth() + 1)
  return next
}

