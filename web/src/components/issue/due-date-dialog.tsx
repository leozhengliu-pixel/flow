import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { useCurrentTime } from '@/hooks/use-current-time'
import { currentDueDateRequest, settleDueDate, subscribeDueDateRequests } from './due-date-dialog-service'
import { formatFieldDate, isoDate, parseNaturalDate } from './due-date-utils'
import './due-date-dialog.css'

/** Mounted once near the app root, like the other action dialogs. */
export function DueDateDialogHost() {
  const request = useSyncExternalStore(subscribeDueDateRequests, currentDueDateRequest)
  return request ? <DueDateDialog key={request.id} value={request.value} onCancel={() => settleDueDate(null)} onSave={value => settleDueDate(value)}/> : null
}

function monthKey(date: Date) { return new Date(date.getFullYear(), date.getMonth(), 1) }
function addMonths(date: Date, count: number) { return new Date(date.getFullYear(), date.getMonth() + count, 1) }

/** Weeks of a month starting on Sunday; days outside the month are null. */
function weeksOf(month: Date) {
  const first = month.getDay()
  const count = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  const cells: (Date | null)[] = [...Array<null>(first).fill(null), ...Array.from({ length: count }, (_, index) => new Date(month.getFullYear(), month.getMonth(), index + 1))]
  while (cells.length % 7) cells.push(null)
  const weeks: (Date | null)[][] = []
  for (let index = 0; index < cells.length; index += 7) weeks.push(cells.slice(index, index + 7))
  while (weeks.length < 6) weeks.push([])
  return weeks
}

export function DueDateDialog({ value, onSave, onCancel }: { value?: string; onSave: (value: string) => void; onCancel: () => void }) {
  const { t, formatDate } = useI18n()
  const [text, setText] = useState(value ? formatFieldDate(value) : '')
  const [month, setMonth] = useState(() => monthKey(value ? new Date(`${value}T12:00:00`) : new Date()))
  const input = useRef<HTMLInputElement>(null)
  const parsed = parseNaturalDate(text)
  const today = isoDate(useCurrentTime())
  // The menu the dialog was opened from hands focus back to its trigger while it closes; take it after that.
  useEffect(() => { const timer = setTimeout(() => input.current?.focus(), 60); return () => clearTimeout(timer) }, [])
  const months = [month, addMonths(month, 1)]
  const weekdays = Array.from({ length: 7 }, (_, index) => formatDate(new Date(2026, 0, 4 + index, 12), { weekday: 'short' }).slice(0, 2))
  const save = () => { if (parsed) onSave(parsed) }

  return <Dialog open onOpenChange={open => { if (!open) onCancel() }}>
    <DialogContent aria-describedby={undefined} className="flow-due-dialog" closeLabel={t('Close modal dialog')} data-dialog-placement="window" onOpenAutoFocus={event => { event.preventDefault(); input.current?.focus() }} onCloseAutoFocus={event => event.preventDefault()} overlayClassName="flow-due-dialog-overlay">
      <DialogTitle><span>{t('Set')}</span> <strong>{t('due date')}</strong></DialogTitle>
      <label className="flow-due-dialog__field">
        <span className="flow-due-dialog__label">{t('Due date')}</span>
        <span className="flow-due-dialog__hint">{t('Issue needs to be completed by this date')}</span>
        <input aria-label={t('Due date')} autoComplete="off" onChange={event => setText(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); save() } }} ref={input} spellCheck={false} value={text}/>
      </label>
      <div className="flow-due-dialog__months">
        {months.map((first, position) => <div className="flow-due-dialog__month" key={first.toISOString()}>
          <div className="flow-due-dialog__month-head">
            <span>{formatDate(first, { month: 'long', year: 'numeric' })}</span>
            {position === 1 && <span className="flow-due-dialog__nav">
              <button aria-label={t('Previous month')} onClick={() => setMonth(addMonths(month, -1))} type="button"><ChevronLeft size={14}/></button>
              <button aria-label={t('Next month')} onClick={() => setMonth(addMonths(month, 1))} type="button"><ChevronRight size={14}/></button>
            </span>}
          </div>
          <table aria-label={formatDate(first, { month: 'long', year: 'numeric' })}>
            <thead><tr>{weekdays.map((label, index) => <th aria-label={formatDate(new Date(2026, 0, 4 + index, 12), { weekday: 'long' })} key={index}>{label}</th>)}</tr></thead>
            <tbody>{weeksOf(first).map((week, row) => <tr data-empty={week.length === 0 || undefined} key={row}>{week.length === 0 ? <td colSpan={7}/> : week.map((day, column) => <td key={column}>{day && <button
              aria-label={formatDate(day, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric' })}
              aria-current={isoDate(day) === today ? 'date' : undefined}
              data-today={isoDate(day) === today || undefined}
              onClick={() => onSave(isoDate(day))}
              type="button">{day.getDate()}</button>}</td>)}</tr>)}</tbody>
          </table>
        </div>)}
      </div>
      <footer>
        <button className="flow-due-dialog__cancel" onClick={onCancel} type="button">{t('Cancel')}</button>
        <button className="flow-due-dialog__save" onClick={save} type="button">{t('Save due date')}</button>
      </footer>
    </DialogContent>
  </Dialog>
}
