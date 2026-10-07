import { addMonths } from 'date-fns'
import { parseDate } from 'chrono-node'
import { useState } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { LinearMenuItem, LinearMenuSearch } from './row-context-menu'

type ReminderPreset = 'hour' | 'threeHours' | 'evening' | 'tomorrow' | 'week' | 'month'
const PRESETS: Array<{ id: ReminderPreset; label: string }> = [
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
function reminderPresetDate(kind: ReminderPreset, from: Date) {
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

function presetApplies(kind: ReminderPreset, from: Date) {
  const hour = from.getHours()
  if (kind === 'threeHours') return hour >= 8 && reminderPresetDate(kind, from).getHours() < 18 && reminderPresetDate(kind, from).getDate() === from.getDate()
  if (kind === 'evening') return hour >= 8 && hour < 17
  return true
}

const alarm = <ViewGlyph icon="Alarm" color="currentColor"/>

/** The "Remind me" submenu body: a natural-language field over the presets and Custom…. */
export function LinearReminderOptions({ onChoose, onCustom, now = new Date() }: { onChoose: (date: Date) => void; onCustom: () => void; now?: Date }) {
  const { t, locale } = useI18n()
  const [query, setQuery] = useState('')
  const normalized = query.trim().toLocaleLowerCase()
  const format = (date: Date) => date.toLocaleString(locale, { weekday: 'short', day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' })
  const presets = PRESETS.filter(preset => presetApplies(preset.id, now)).map(preset => ({ ...preset, date: reminderPresetDate(preset.id, now) })).filter(preset => !normalized || `${preset.label} ${t(preset.label)}`.toLocaleLowerCase().includes(normalized))
  const parsed = normalized && !presets.length ? parseDate(query, now, { forwardDate: true }) : null
  const showCustom = !normalized || `custom ${t('Custom…')}`.toLocaleLowerCase().includes(normalized)
  return <>
    <LinearMenuSearch value={query} onChange={setQuery} placeholder="Try: 4 pm, 2 days, in 5 weeks…"/>
    <div className="linear-menu__list">
      {presets.map(preset => <LinearMenuItem key={preset.id} icon={alarm} label={preset.label} detail={format(preset.date)} onSelect={() => onChoose(preset.date)}/>)}
      {parsed && parsed.getTime() > now.getTime() && <LinearMenuItem icon={alarm} label={format(parsed)} translate={false} onSelect={() => onChoose(parsed)}/>}
      {showCustom && <LinearMenuItem icon={alarm} label="Custom…" onSelect={onCustom}/>}
      {!presets.length && !parsed && !showCustom && <div className="linear-menu__empty">{t('No results')}</div>}
    </div>
  </>
}
