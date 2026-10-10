import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { addMonths, eachDayOfInterval, endOfMonth, endOfWeek, isSameDay, isSameMonth, startOfMonth, startOfWeek, subMonths } from 'date-fns'
import { useState, type ReactNode } from 'react'

import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuSeparator, LinearSubmenu } from '@/components/ui/row-context-menu'
import { useI18n } from '@/i18n/i18n'

import { parseReleaseDateQuery, releaseDateQuickOptions } from './release-view-model'

const DateIcon = () => <IssueActionGlyph label="Due date" fallback={<LinearGlyph name="dateAdd"/>}/>

/**
 * Linear's release target-date picker (FuzzyDatePicker without resolutions): a "Try: 24h, 7 days,
 * Feb 9" field, Custom… calendar and quick picks.
 */
export function ReleaseDateMenu({ value, onChange, trigger, align = 'start', open, onOpenChange }: { value?: string; onChange: (value: string) => void; trigger: ReactNode; align?: 'start' | 'end'; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [internalOpen, setInternalOpen] = useState(false)
  const shown = open ?? internalOpen
  const setOpen = (next: boolean) => { setInternalOpen(next); onOpenChange?.(next); if (!next) setQuery('') }
  const choose = (next: string) => { onChange(next); setOpen(false) }
  const parsed = parseReleaseDateQuery(query)
  return <DropdownMenu.Root open={shown} onOpenChange={setOpen}>
    <DropdownMenu.Trigger asChild>{trigger}</DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <LinearDropdownMenuContent align={align} label={t('Target date')} className="flow-release-date-menu">
        <div className="linear-menu__search">
          <input aria-label={t('Try: 24h, 7 days, Feb 9')} autoFocus autoComplete="off" placeholder={t('Try: 24h, 7 days, Feb 9')} spellCheck={false} value={query}
            onChange={event => setQuery(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Escape') return
              event.stopPropagation()
              if (event.key === 'Enter' && parsed) { event.preventDefault(); choose(parsed) }
              if (event.key === 'ArrowDown') { event.preventDefault(); event.currentTarget.closest('[role=menu]')?.querySelector<HTMLElement>('[role^=menuitem]')?.focus() }
            }}/>
        </div>
        {query.trim() ? parsed
          ? <LinearMenuItem icon={<DateIcon/>} label={new Date(`${parsed}T12:00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })} translate={false} onSelect={() => choose(parsed)}/>
          : <div className="linear-menu__empty">{t('No results')}</div>
          : <>
            <LinearSubmenu icon={<DateIcon/>} label="Custom…" className="flow-release-calendar-submenu">
              {({ close }) => <ReleaseCalendar value={value} onCancel={close} onSave={choose}/>}
            </LinearSubmenu>
            {releaseDateQuickOptions().map(option => <LinearMenuItem key={option.id} icon={<DateIcon/>} label={option.label} onSelect={() => choose(option.value)}/>)}
            {value && <><LinearMenuSeparator/><LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Remove target date" onSelect={() => choose('')}/></>}
          </>}
      </LinearDropdownMenuContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function dateValue(date: Date) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

/** Month calendar behind "Custom…". */
export function ReleaseCalendar({ value, onCancel, onSave }: { value?: string; onCancel: () => void; onSave: (value: string) => void }) {
  const { t, formatDate } = useI18n()
  const initial = value ? new Date(`${value}T12:00:00`) : new Date()
  const [month, setMonth] = useState(startOfMonth(initial))
  const [draft, setDraft] = useState(initial)
  const days = eachDayOfInterval({ start: startOfWeek(startOfMonth(month)), end: endOfWeek(endOfMonth(month)) })
  const weekdays = days.slice(0, 7)
  return <div className="flow-release-calendar" onKeyDown={event => { if (event.key !== 'Escape') event.stopPropagation() }}>
    <header>
      <button aria-label={t('Previous month')} onClick={() => setMonth(current => subMonths(current, 1))} type="button"><ChevronLeft/></button>
      <strong>{formatDate(month.toISOString(), { month: 'long', year: 'numeric' })}</strong>
      <button aria-label={t('Next month')} onClick={() => setMonth(current => addMonths(current, 1))} type="button"><ChevronRight/></button>
    </header>
    <div className="flow-release-calendar__weekdays">{weekdays.map(day => <span key={day.getDay()}>{formatDate(day.toISOString(), { weekday: 'narrow' })}</span>)}</div>
    <div className="flow-release-calendar__grid">{days.map(day => <button aria-label={formatDate(day.toISOString(), { month: 'long', day: 'numeric', year: 'numeric' })} aria-pressed={isSameDay(day, draft)} className={!isSameMonth(day, month) ? 'is-outside' : isSameDay(day, new Date()) ? 'is-today' : ''} key={day.toISOString()} onClick={() => setDraft(day)} onDoubleClick={() => onSave(dateValue(day))} type="button">{day.getDate()}</button>)}</div>
    <footer><button onClick={onCancel} type="button">{t('Cancel')}</button><button className="is-primary" onClick={() => onSave(dateValue(draft))} type="button">{t('Save target date')}</button></footer>
  </div>
}
