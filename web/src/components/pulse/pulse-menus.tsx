import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Bell, Check, Filter, X } from 'lucide-react'
import { useState } from 'react'
import { ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import type { PulseSavedView, PulseSourceFilter } from './pulse-model'

export type PulseCadence = 'daily' | 'weekly' | 'never'

export function PulseSubscriptionMenu({ cadence, onChange }: { cadence: PulseCadence; onChange: (cadence: PulseCadence) => void }) {
  const [query, setQuery] = useState('')
  const values = (['daily', 'weekly', 'never'] as PulseCadence[]).filter(value => value.includes(query.trim().toLowerCase()))
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery('') }}><DropdownMenu.Trigger asChild><button aria-label="Subscription" className="pulse-icon-button" type="button"><Bell size={14}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="pulse-menu pulse-subscription-menu" sideOffset={5}>
    <div className="pulse-menu-search"><input aria-label="Filter…" autoFocus placeholder="Filter…" value={query} onChange={event => setQuery(event.target.value)}/></div>
    <DropdownMenu.Label>Inbox notifications for Pulse summaries</DropdownMenu.Label>
    {values.map(value => <DropdownMenu.Item key={value} onSelect={() => onChange(value)}><span>{title(value)}</span>{cadence === value && <Check className="pulse-menu-end" size={13}/>}</DropdownMenu.Item>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

export function PulseNewViewEditor({ draft, onCancel, onChange, onSave }: { draft: Omit<PulseSavedView, 'id'>; onCancel: () => void; onChange: (draft: Omit<PulseSavedView, 'id'>) => void; onSave: () => void }) {
  return <div className="pulse-new-view">
    <ViewIconPicker color={draft.color} icon={draft.icon} onChange={(visual: ViewVisual) => onChange({ ...draft, ...visual })}/>
    <input aria-label="View name" autoFocus placeholder="All updates" value={draft.name} onChange={event => onChange({ ...draft, name: event.target.value })} onKeyDown={event => { if (event.key === 'Escape') onCancel(); if (event.key === 'Enter' && draft.name.trim()) onSave() }}/>
    <button className="pulse-text-button" onClick={onCancel} type="button">Cancel</button>
    <button className="pulse-text-button is-primary" disabled={!draft.name.trim()} onClick={onSave} type="button">Save</button>
    <PulseSourceMenu source={draft.source} onChange={source => onChange({ ...draft, source })}/>
  </div>
}

export function PulseSourceMenu({ source, onChange }: { source: PulseSourceFilter; onChange: (source: PulseSourceFilter) => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button className="pulse-filter-button" type="button"><Filter size={13}/>{source === 'all' ? 'Add filter' : source === 'projects' ? 'Projects' : 'Initiatives'}{source !== 'all' && <X size={11}/>}</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="pulse-menu pulse-filter-menu" sideOffset={5}>
    <DropdownMenu.Label>Update source</DropdownMenu.Label>
    {(['all', 'projects', 'initiatives'] as PulseSourceFilter[]).map(value => <DropdownMenu.Item key={value} onSelect={() => onChange(value)}><span>{value === 'all' ? 'All updates' : title(value)}</span>{source === value && <Check className="pulse-menu-end" size={13}/>}</DropdownMenu.Item>)}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function title(value: string) { return value[0].toUpperCase() + value.slice(1) }
