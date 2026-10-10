import { parseDate } from 'chrono-node'
import { useState } from 'react'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { presetApplies, reminderPresetDate, PRESETS } from './reminder-presets'
import { LinearMenuItem, LinearMenuSearch } from './row-context-menu'

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
