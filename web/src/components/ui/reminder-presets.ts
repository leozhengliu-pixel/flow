import { addMonths } from 'date-fns'

export type ReminderPreset = 'hour' | 'threeHours' | 'evening' | 'tomorrow' | 'week' | 'month'
export const PRESETS: Array<{ id: ReminderPreset; label: string }> = [
  { id: 'hour', label: 'An hour from now' },
  { id: 'threeHours', label: 'In 3 hours' },
  { id: 'evening', label: 'This evening' },
  { id: 'tomorrow', label: 'Tomorrow' },
  { id: 'week', label: 'Next week' },
  { id: 'month', label: 'A month from now' },
]

/**
 * Linear's reminder presets: an hour / three hours out and this evening (18:00) during the working
 * day, then 9:00 tomorrow, next Monday and a month out.
 */
export function reminderPresetDate(kind: ReminderPreset, from: Date) {
  if (kind === 'hour') return new Date(from.getTime() + 60 * 60 * 1000)
  if (kind === 'threeHours') return new Date(from.getTime() + 3 * 60 * 60 * 1000)
  const date = new Date(from)
  if (kind === 'evening') { date.setHours(18, 0, 0, 0); return date }
  date.setHours(9, 0, 0, 0)
  if (kind === 'tomorrow') date.setDate(date.getDate() + 1)
  if (kind === 'week') date.setDate(date.getDate() + ((8 - date.getDay()) % 7 || 7))
  if (kind === 'month') return addMonths(date, 1)
  return date
}

export function presetApplies(kind: ReminderPreset, from: Date) {
  const hour = from.getHours()
  if (kind === 'threeHours') return hour >= 8 && reminderPresetDate(kind, from).getHours() < 18 && reminderPresetDate(kind, from).getDate() === from.getDate()
  if (kind === 'evening') return hour >= 8 && hour < 17
  return true
}

/** The presets that apply right now with their dates (the ⌘K "Remind me" page uses the same list). */
export function reminderPresetOptions(now = new Date()) {
  return PRESETS.filter(preset => presetApplies(preset.id, now)).map(preset => ({ ...preset, date: reminderPresetDate(preset.id, now) }))
}
