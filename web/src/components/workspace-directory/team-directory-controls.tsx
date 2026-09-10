import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { format } from 'date-fns'
import { DateTimeControl } from '@/components/ui/date-time-control'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import { DirectoryFilterMenu, type DirectoryFilterGroup } from './directory-menus'
import { emptyTeamFilters, teamDateLabel, type TeamDirectoryFilters, type TeamFilterField } from './team-directory-model'

export function TeamFilterBar({ filters, groups, onChange, onChoice, onDate }: {
  filters: TeamDirectoryFilters; groups: DirectoryFilterGroup[]
  onChange: (filters: TeamDirectoryFilters) => void
  onChoice: (field: string, id: string, checked: boolean) => void
  onDate: () => void
}) {
  const selected = Object.fromEntries(groups.map(group => [group.id, new Set(filters[group.id as TeamFilterField])]))
  const menu = (field?: TeamFilterField, triggerNode?: React.ReactNode) => <DirectoryFilterMenu groups={field ? groups.filter(group => group.id === field) : groups} selected={selected} onChoice={onChoice} onDirect={id => onChoice(id, 'true', true)} onAdvanced={() => onChange({ ...filters, advanced: true })} trigger="add" triggerNode={triggerNode} showAdvanced={!field}/>
  return <div className={`workspace-filter-bar workspace-team-filter-bar${filters.advanced ? ' workspace-team-advanced' : ''}`} aria-label="Team filters">
    {filters.advanced && <div className="workspace-team-advanced__heading"><span>Match</span><SelectControl label="Filter conjunction" value={filters.conjunction} options={[{ value: 'and', label: 'all filters' }, { value: 'or', label: 'any filter' }]} onChange={value => onChange({ ...filters, conjunction: value as 'and' | 'or' })}/><button aria-label="Remove advanced filter" onClick={() => onChange({ ...filters, advanced: false, conjunction: 'and' })}><X size={14}/></button></div>}
    {groups.map(group => {
      const field = group.id as TeamFilterField, values = filters[field]
      if (!values.length) return null
      const label = field === 'created' ? teamDateLabel(values[0]) : values.map(value => group.choices?.find(choice => choice.id === value)?.label ?? 'Unknown user').join(', ')
      return <span className="workspace-filter-chip" key={field}>
        <strong>{group.icon}{group.label}</strong>
        <SelectControl label={`${group.label} operator`} value={filters.operators[field] ?? 'is'} options={[{ value: 'is', label: field === 'created' ? (values[0].includes('/') ? 'between' : 'after') : 'is' }, { value: 'isNot', label: field === 'created' ? (values[0].includes('/') ? 'not between' : 'before') : 'is not' }]} onChange={operator => onChange({ ...filters, operators: { ...filters.operators, [field]: operator } })}/>
        {field === 'private' ? <SelectControl label="Private values" value={values[0]} options={[{ value: 'true', label: 'true' }, { value: 'false', label: 'false' }]} onChange={value => onChange({ ...filters, private: [value] })}/> : menu(field, <button type="button" className="workspace-team-filter-value" aria-label={`${group.label} values`}>{label}</button>)}
        {field === 'created' && values[0].startsWith('date:') && <button type="button" className="workspace-team-filter-value" onClick={onDate}>Edit date</button>}
        <button type="button" aria-label={`Remove ${group.label} filter`} onClick={() => onChange({ ...filters, [field]: [] })}><X/></button>
      </span>
    })}
    {menu()}<button className="workspace-filter-bar__clear" type="button" onClick={() => onChange(emptyTeamFilters())}>Clear</button>
  </div>
}

export function TeamDateFilterDialog({ open, value, onClose, onApply }: { open: boolean; value?: string; onClose: () => void; onApply: (value: string) => void }) {
  const [from, setFrom] = useState(''), [to, setTo] = useState('')
  useEffect(() => { if (open) { const [start, end] = value?.startsWith('date:') ? value.slice(5).split('/') : []; setFrom(start ?? format(new Date(), 'yyyy-MM-dd')); setTo(end ?? '') } }, [open, value])
  return <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}><DialogContent className="workspace-team-date-dialog"><DialogTitle>Created date</DialogTitle>
    <label>On or after<DateTimeControl label="Created on or after" value={from} onChange={setFrom}/></label>
    {to ? <><label>Through<DateTimeControl label="Created through" min={from || undefined} value={to} onChange={setTo}/></label><button type="button" onClick={() => setTo('')}>Remove end date</button></> : <button type="button" onClick={() => setTo(from)}>Add end date</button>}
    <footer><button type="button" onClick={onClose}>Cancel</button><button type="button" disabled={!from || Boolean(to && to < from)} onClick={() => onApply(`date:${from}${to ? `/${to}` : ''}`)}>Apply filter</button></footer>
  </DialogContent></Dialog>
}
