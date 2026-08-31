import * as Popover from '@radix-ui/react-popover'
import { Check } from 'lucide-react'
import { useState } from 'react'
import { ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { SubscriptionIcon } from '@/components/ui/view-action-icons'
import type { BootstrapData } from '@/types/flow'
import { PulseFilterChips, PulseFilterMenu, PulseMatchSummary } from './pulse-filters'
import type { PulseFilter, PulseFilterMatch } from './pulse-model'

export type PulseCadence = 'daily' | 'weekly' | 'never'
export type PulseViewDraft = { name:string;icon:string;color:string;filters:PulseFilter[];match:PulseFilterMatch }

export function PulseSubscriptionMenu({ cadence, onChange }: { cadence: PulseCadence; onChange: (cadence: PulseCadence) => void }) {
  const [open, setOpen] = useState(false)
  const options = (['daily', 'weekly', 'never'] as PulseCadence[]).map(value => ({ id: value, label: title(value) }))
  const command = usePropertyCommand({ open, options, selectedIds: [cadence], onOpenChange: setOpen, onSelect: option => { onChange(option.id as PulseCadence); setOpen(false) } })
  return <Popover.Root open={open} onOpenChange={next => { setOpen(next); if (!next) command.onQueryChange('') }}><Popover.Trigger asChild><button aria-label="Subscription" className="pulse-icon-button" type="button"><SubscriptionIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content align="end" className="pulse-subscription-menu" collisionPadding={8} data-has-query={Boolean(command.query) || undefined} onKeyDown={command.onKeyDown} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => command.inputRef.current?.focus()) }} sideOffset={3.5}>
    <span aria-live="polite" className="sr-only" role="status">{command.filteredOptions.length === options.length ? 'Showing all items' : `Showing ${command.filteredOptions.length} ${command.filteredOptions.length === 1 ? 'item' : 'items'}`}</span>
    <input aria-activedescendant={command.activeId ? `pulse-subscription-${command.activeId}` : undefined} aria-controls="pulse-subscription-options" aria-label="Filter…" className="pulse-subscription-search" placeholder="Filter…" ref={command.inputRef} role="searchbox" value={command.query} onChange={event => command.onQueryChange(event.target.value)}/>
    <div aria-multiselectable="false" id="pulse-subscription-options" role="listbox">
      {!command.query && <div className="pulse-subscription-label">Inbox notifications for Pulse summaries</div>}
      {command.filteredOptions.map(option => <button aria-checked={cadence === option.id} aria-selected={command.activeId === option.id} id={`pulse-subscription-${option.id}`} key={option.id} onClick={() => command.choose(option)} onFocus={() => command.setActiveId(option.id)} onPointerMove={() => command.setActiveId(option.id)} role="option" type="button"><span>{option.label}</span>{cadence === option.id && <Check size={13}/>}</button>)}
    </div>
  </Popover.Content></Popover.Portal></Popover.Root>
}

export function PulseNewViewEditor({ data,draft,onCancel,onChange,onSave }: { data:BootstrapData;draft:PulseViewDraft;onCancel:()=>void;onChange:(draft:PulseViewDraft)=>void;onSave:()=>void }) {
  return <form className="pulse-new-view" onSubmit={event => { event.preventDefault(); onSave() }}>
    <div className="pulse-new-view-top"><div className="pulse-new-view-identity"><ViewIconPicker color={draft.color} icon={draft.icon} onChange={(visual: ViewVisual) => onChange({ ...draft, ...visual })}/><input aria-label="View name" autoFocus placeholder="All updates" value={draft.name} onChange={event => onChange({ ...draft, name: event.target.value })} onKeyDown={event => { if (event.key === 'Escape') onCancel() }}/></div><div className="pulse-new-view-actions"><button className="pulse-new-view-cancel" onClick={onCancel} type="button">Cancel</button><button className="pulse-new-view-save" type="submit">Save</button></div></div>
    <div className="pulse-new-view-filters"><PulseMatchSummary count={draft.filters.length} match={draft.match}/><PulseFilterChips data={data} filters={draft.filters} onChange={filters=>onChange({...draft,filters})}/><span/><PulseFilterMenu align="end" compact data={data} filters={draft.filters} match={draft.match} onChange={filters=>onChange({...draft,filters})} onMatchChange={match=>onChange({...draft,match})}/></div>
  </form>
}

function title(value: string) { return value[0].toUpperCase() + value.slice(1) }
