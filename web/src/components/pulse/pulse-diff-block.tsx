import { ArrowRight, Check } from 'lucide-react'
import type { ReactNode } from 'react'
import { format } from 'date-fns'
import { PriorityIcon } from '@/components/issue/issue-icons'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { PulseDiff, PulseDiffChange, PulseDiffValue } from '@/types/flow'
import { pulseDiffHasChanges } from './pulse-diff'
import './pulse-diff.css'

const PRIORITY_LABELS = ['No priority', 'Urgent', 'High', 'Medium', 'Low']

type Field = 'status' | 'priority' | 'lead' | 'owner' | 'startDate' | 'targetDate'
const FIELD_LABELS: Record<Field, string> = { status: 'Status', priority: 'Priority', lead: 'Lead', owner: 'Owner', startDate: 'Start date', targetDate: 'Target date' }
const EMPTY_LABELS: Record<Field, string> = { status: 'No status', priority: 'No priority', lead: 'No lead', owner: 'No owner', startDate: 'No date', targetDate: 'No date' }

function valueName(value: PulseDiffValue | undefined) {
  if (value == null) return ''
  if (typeof value === 'string' || typeof value === 'number') return String(value)
  return value.name ?? (value.value != null ? String(value.value) : '')
}

function priorityNumber(value: PulseDiffValue | undefined) {
  if (typeof value === 'number') return value
  if (value && typeof value === 'object' && typeof value.value === 'number') return value.value
  if (typeof value === 'string') {
    const index = PRIORITY_LABELS.findIndex(label => label.toLowerCase() === value.toLowerCase())
    if (index >= 0) return index
    if (/^\d$/.test(value)) return Number(value)
  }
  return undefined
}

function formatDay(value: string) {
  const date = /^\d{4}-\d{2}-\d{2}$/.test(value) ? new Date(`${value}T00:00:00`) : new Date(value)
  return Number.isNaN(+date) ? value : format(date, 'MMM d')
}

function DiffValue({ field, value }: { field: Field; value?: PulseDiffValue }) {
  const { t } = useI18n()
  if (field === 'priority') {
    const priority = priorityNumber(value)
    const label = priority != null ? PRIORITY_LABELS[priority] ?? valueName(value) : valueName(value)
    return <span className="pulse-diff-value"><PriorityIcon priority={priority ?? 0} size={14}/><span>{label ? t(label) : t(EMPTY_LABELS.priority)}</span></span>
  }
  const name = valueName(value)
  if (!name) return <span className="pulse-diff-value is-empty">{t(EMPTY_LABELS[field])}</span>
  if (field === 'status') {
    const color = value && typeof value === 'object' ? value.color : undefined
    return <span className="pulse-diff-value"><i className="pulse-diff-status" style={color ? { background: color } : undefined}/><span data-i18n-ignore>{name}</span></span>
  }
  if (field === 'lead' || field === 'owner') {
    const avatarUrl = value && typeof value === 'object' ? value.avatarUrl : undefined
    return <span className="pulse-diff-value"><UserAvatar avatarUrl={avatarUrl} className="avatar pulse-diff-avatar" name={name}/><span data-i18n-ignore>{name}</span></span>
  }
  return <span className="pulse-diff-value">{formatDay(name)}</span>
}

function ChangeRow({ field, change, kind }: { field: Field; change: PulseDiffChange; kind: 'project' | 'initiative' }) {
  const { t } = useI18n()
  // Initiatives record the owner change in `lead`.
  const label = kind === 'initiative' && field === 'lead' ? FIELD_LABELS.owner : FIELD_LABELS[field]
  return <div className="pulse-diff-row" data-field={field}>
    <span className="pulse-diff-label">{t(label)}</span>
    <span className="pulse-diff-change">
      {change.from !== undefined && <><DiffValue field={field} value={change.from}/><ArrowRight aria-hidden="true" className="pulse-diff-arrow" size={12}/></>}
      <DiffValue field={field} value={change.to}/>
    </span>
  </div>
}

function percent(value: number) {
  const normalized = value <= 1 ? value * 100 : value
  return `${Math.round(normalized)}%`
}

function ProgressBar({ value }: { value: number }) {
  const width = Math.max(0, Math.min(100, value <= 1 ? value * 100 : value))
  return <span aria-hidden="true" className="pulse-diff-progress"><i style={{ width: `${width}%` }}/></span>
}

function ListChange({ label, added, removed }: { label: string; added?: PulseDiffValue[]; removed?: PulseDiffValue[] }) {
  const { t } = useI18n()
  if (!added?.length && !removed?.length) return null
  return <div className="pulse-diff-row pulse-diff-list" data-field={label}>
    <span className="pulse-diff-label">{t(label)}</span>
    <span className="pulse-diff-change is-list">
      {added?.map((value, index) => <span className="pulse-diff-chip is-added" data-i18n-ignore key={`a${index}`}>+ {valueName(value)}</span>)}
      {removed?.map((value, index) => <span className="pulse-diff-chip is-removed" data-i18n-ignore key={`r${index}`}>− {valueName(value)}</span>)}
    </span>
  </div>
}

/** "Changes since the previous update": recorded by the server when the update was posted. */
export function PulseDiffBlock({ diff, kind, title }: { diff?: PulseDiff; kind: 'project' | 'initiative'; title?: ReactNode }) {
  const { t } = useI18n()
  if (!diff || !pulseDiffHasChanges(diff)) return null
  const fields: Field[] = kind === 'initiative' ? ['status', 'owner', 'lead', 'priority', 'startDate', 'targetDate'] : ['status', 'priority', 'lead', 'owner', 'startDate', 'targetDate']
  return <section aria-label={t('Changes since last update')} className="pulse-diff">
    {title}
    {fields.map(field => diff[field] ? <ChangeRow change={diff[field]!} field={field} key={field} kind={kind}/> : null)}
    {diff.progressSince && <div className="pulse-diff-row" data-field="progress">
      <span className="pulse-diff-label">{t('Progress since {date}').replace('{date}', formatDay(diff.progressSince.date))}</span>
      <span className="pulse-diff-change"><ProgressBar value={diff.progressSince.to}/><span>{percent(diff.progressSince.from)}</span><ArrowRight aria-hidden="true" className="pulse-diff-arrow" size={12}/><strong>{percent(diff.progressSince.to)}</strong></span>
    </div>}
    {diff.milestones?.length ? <ul className="pulse-diff-milestones">
      {diff.milestones.map((milestone, index) => <li key={milestone.id ?? index}>
        {milestone.completed ? <Check aria-hidden="true" className="pulse-diff-milestone-done" size={12}/> : <i aria-hidden="true" className="pulse-diff-milestone"/>}
        <span data-i18n-ignore>{milestone.name}</span>{milestone.added && <span className="pulse-diff-chip is-added">{t('New milestone')}</span>}
        {milestone.to !== undefined && <span className="pulse-diff-milestone-progress">{milestone.from !== undefined && milestone.from !== milestone.to ? `${percent(milestone.from)} → ` : ''}{percent(milestone.to)}</span>}
      </li>)}
    </ul> : null}
    <ListChange added={diff.projects?.added} label="Projects" removed={diff.projects?.removed}/>
    <ListChange added={diff.initiatives?.added} label="Initiatives" removed={diff.initiatives?.removed}/>
  </section>
}
